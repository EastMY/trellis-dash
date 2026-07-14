package watcher

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/yunnnn/trellis-dash/internal/streamwalk"
	"github.com/yunnnn/trellis-dash/internal/trellis"
)

// Watcher 将短时间内密集的原子写/重命名事件合并成一次项目重扫。
type Watcher struct {
	root               string
	debounce           time.Duration
	pollInterval       time.Duration
	onChange           func(context.Context, ChangeSet)
	onError            func(error)
	watchedDirectories int
	watchedPaths       map[string]struct{}
	observedPaths      map[string]struct{}
}

const (
	MaxWatchedDirectories = 20_000
	// MaxWatchedEntries 同时覆盖文件和目录，阻止单个超大目录绕过目录数量上限。
	MaxWatchedEntries = 20_000
	MaxPolledEntries  = 200_000
	// minPollInterval 是 macOS/BSD 的默认刷新上限；扫描本身更慢时继续按耗时退避。
	minPollInterval = 10 * time.Second
)

// BSD/macOS 最多允许两个项目同时扫描；单项目内部仍有固定 worker 上限，
// 避免原先全局串行让一个大项目长期阻塞其他项目。
var metadataPollSemaphore = make(chan struct{}, 2)

const knownMetadataWorkers = 8

var ErrWatchLimit = errors.New("监听目录超过数量上限")

// ChangeSet 描述一次合并后的资源变化，Paths 均为项目根下的正斜杠相对路径。
type ChangeSet struct {
	Full     bool
	Tasks    bool
	Sessions bool
	Specs    bool
	Workflow bool
	Paths    []string
}

func (changes ChangeSet) Empty() bool {
	return !changes.Full && !changes.Tasks && !changes.Sessions && !changes.Specs && !changes.Workflow
}

// Merge 合并密集事件，同时保持路径去重和稳定顺序。
func (changes *ChangeSet) Merge(other ChangeSet) {
	changes.Full = changes.Full || other.Full
	changes.Tasks = changes.Tasks || other.Tasks
	changes.Sessions = changes.Sessions || other.Sessions
	changes.Specs = changes.Specs || other.Specs
	changes.Workflow = changes.Workflow || other.Workflow
	paths := make(map[string]struct{}, len(changes.Paths)+len(other.Paths))
	for _, path := range append(changes.Paths, other.Paths...) {
		paths[path] = struct{}{}
	}
	changes.Paths = changes.Paths[:0]
	for path := range paths {
		changes.Paths = append(changes.Paths, path)
	}
	sort.Strings(changes.Paths)
}

// New 保留简单回调入口；需要资源级变化的调用方使用 NewWithChanges。
func New(root string, debounce time.Duration, onChange func(context.Context), onError func(error)) *Watcher {
	var detailed func(context.Context, ChangeSet)
	if onChange != nil {
		detailed = func(ctx context.Context, _ ChangeSet) { onChange(ctx) }
	}
	return NewWithChanges(root, debounce, minPollInterval, detailed, onError)
}

func NewWithChanges(
	root string,
	debounce, pollInterval time.Duration,
	onChange func(context.Context, ChangeSet),
	onError func(error),
) *Watcher {
	if debounce <= 0 {
		debounce = 250 * time.Millisecond
	}
	if pollInterval <= 0 {
		pollInterval = minPollInterval
	}
	if onError == nil {
		onError = func(error) {}
	}
	return &Watcher{root: root, debounce: debounce, pollInterval: pollInterval, onChange: onChange, onError: onError}
}

func (w *Watcher) Run(ctx context.Context) error {
	return w.run(ctx, nil)
}

// RunWithPollTrigger 允许 Supervisor 把项目级刷新节拍广播给 BSD/macOS 元数据轮询。
// 其他平台仍由 fsnotify 驱动，不会因为传入 trigger 而增加周期扫描。
func (w *Watcher) RunWithPollTrigger(ctx context.Context, trigger <-chan struct{}) error {
	return w.run(ctx, trigger)
}

func (w *Watcher) run(ctx context.Context, trigger <-chan struct{}) error {
	// fsnotify 的 kqueue 后端会给目录内每个文件打开 FD，并在 Add/变更时
	// os.ReadDir 整个目录。BSD/macOS 改用有界元数据轮询，避免 EMFILE 与大分配。
	if kqueuePlatform() {
		return w.runPolling(ctx, trigger)
	}
	native, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("创建文件监听器: %w", err)
	}
	defer native.Close()

	w.watchedDirectories = 0
	w.watchedPaths = make(map[string]struct{})
	w.observedPaths = make(map[string]struct{})
	if err := w.addInitialPaths(ctx, native); err != nil {
		return err
	}
	return w.runLoop(ctx, native, native.Events, native.Errors)
}

// runLoop 独立承接 channel 生命周期，便于验证 fsnotify 任一 channel 关闭时不会热循环。
func (w *Watcher) runLoop(
	ctx context.Context,
	native *fsnotify.Watcher,
	events <-chan fsnotify.Event,
	errorsC <-chan error,
) error {
	var timer *time.Timer
	var timerC <-chan time.Time
	pendingPaths := make(map[string]struct{})
	reset := func() {
		if timer == nil {
			timer = time.NewTimer(w.debounce)
		} else {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(w.debounce)
		}
		timerC = timer.C
	}
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-events:
			if !ok {
				events = nil
				if errorsC == nil {
					return nil
				}
				continue
			}
			if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
				if err := w.forgetPath(native, event.Name); err != nil {
					return err
				}
			}
			structuralChange := false
			if event.Op&fsnotify.Create != 0 {
				if err := w.observePath(event.Name); err != nil {
					return err
				}
				if info, statErr := os.Stat(event.Name); statErr == nil && info.IsDir() {
					if err := w.addRecursive(ctx, native, event.Name); err != nil {
						w.onError(err)
					} else {
						// 目录内可能已在 watcher 注册前写入文件，必须重扫一次补齐。
						structuralChange = true
					}
				}
			}
			if structuralChange || w.relevant(event.Name) {
				pendingPaths[event.Name] = struct{}{}
				reset()
			}
		case err, ok := <-errorsC:
			if !ok {
				errorsC = nil
				if events == nil {
					return nil
				}
				continue
			}
			if err != nil {
				w.onError(fmt.Errorf("文件监听错误: %w", err))
			}
		case <-timerC:
			timerC = nil
			if w.onChange != nil {
				paths := make([]string, 0, len(pendingPaths))
				for path := range pendingPaths {
					paths = append(paths, path)
				}
				pendingPaths = make(map[string]struct{})
				changes := classifyPaths(w.root, paths)
				if !changes.Empty() {
					w.onChange(ctx, changes)
				}
			}
		}
	}
}

func (w *Watcher) addInitialPaths(ctx context.Context, native *fsnotify.Watcher) error {
	validatedRoot, err := trellis.ValidateRoot(w.root)
	if err != nil {
		return fmt.Errorf("校验监听根目录: %w", err)
	}
	if filepath.Clean(validatedRoot) != filepath.Clean(w.root) {
		return fmt.Errorf("项目根目录真实路径已变化: 注册=%s 当前=%s", w.root, validatedRoot)
	}
	trellisRoot := filepath.Join(validatedRoot, ".trellis")
	// 根目录用于捕获 workflow.md/config.yaml 的原子替换。
	if err := w.addDirectory(native, trellisRoot); err != nil {
		return fmt.Errorf("监听 %s: %w", trellisRoot, err)
	}
	for _, relative := range []string{"tasks", ".runtime", "spec"} {
		path := filepath.Join(trellisRoot, relative)
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			if err := w.addRecursive(ctx, native, path); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *Watcher) relevant(path string) bool {
	clean := filepath.Clean(path)
	trellis := filepath.Join(w.root, ".trellis")
	if clean == filepath.Join(trellis, "workflow.md") || clean == filepath.Join(trellis, "config.yaml") {
		return true
	}
	rel, err := filepath.Rel(trellis, clean)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	if rel == "tasks" || rel == "spec" || rel == ".runtime" || rel == filepath.Join(".runtime", "sessions") {
		return true
	}
	if strings.HasPrefix(rel, "tasks"+string(filepath.Separator)) ||
		strings.HasPrefix(rel, filepath.Join(".runtime", "sessions")+string(filepath.Separator)) ||
		strings.HasPrefix(rel, "spec"+string(filepath.Separator)) {
		return true
	}
	return false
}

func (w *Watcher) addRecursive(ctx context.Context, native *fsnotify.Watcher, root string) error {
	err := streamwalk.Walk(ctx, w.root, root, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrPermission) {
				return fs.SkipDir
			}
			return walkErr
		}
		if err := w.observePath(path); err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fs.SkipDir
		}
		if err := w.addDirectory(native, path); err != nil {
			return fmt.Errorf("监听 %s: %w", path, err)
		}
		return nil
	})
	return err
}

func (w *Watcher) addDirectory(native *fsnotify.Watcher, path string) error {
	path = filepath.Clean(path)
	if w.watchedPaths == nil {
		w.watchedPaths = make(map[string]struct{})
	}
	if _, exists := w.watchedPaths[path]; exists {
		return nil
	}
	if w.watchedDirectories >= MaxWatchedDirectories {
		return fmt.Errorf("%w: 上限 %d", ErrWatchLimit, MaxWatchedDirectories)
	}
	if err := native.Add(path); err != nil {
		return err
	}
	w.watchedDirectories++
	w.watchedPaths[path] = struct{}{}
	return nil
}

func (w *Watcher) observePath(path string) error {
	path = filepath.Clean(path)
	if w.observedPaths == nil {
		w.observedPaths = make(map[string]struct{})
	}
	if _, exists := w.observedPaths[path]; exists {
		return nil
	}
	if len(w.observedPaths) >= MaxWatchedEntries {
		return fmt.Errorf("%w: 文件与目录总数上限 %d", ErrWatchLimit, MaxWatchedEntries)
	}
	w.observedPaths[path] = struct{}{}
	return nil
}

func (w *Watcher) forgetPath(native *fsnotify.Watcher, path string) error {
	path = filepath.Clean(path)
	prefix := path + string(filepath.Separator)
	var removeErr error
	for watched := range w.watchedPaths {
		if watched == path || strings.HasPrefix(watched, prefix) {
			if err := native.Remove(watched); err != nil && !errors.Is(err, fsnotify.ErrNonExistentWatch) && !errors.Is(err, os.ErrNotExist) {
				removeErr = errors.Join(removeErr, fmt.Errorf("移除监听 %s: %w", watched, err))
			}
			delete(w.watchedPaths, watched)
			if w.watchedDirectories > 0 {
				w.watchedDirectories--
			}
		}
	}
	for observed := range w.observedPaths {
		if observed == path || strings.HasPrefix(observed, prefix) {
			delete(w.observedPaths, observed)
		}
	}
	return removeErr
}

func kqueuePlatform() bool {
	switch runtime.GOOS {
	case "darwin", "dragonfly", "freebsd", "netbsd", "openbsd":
		return true
	default:
		return false
	}
}

func (w *Watcher) runPolling(ctx context.Context, trigger <-chan struct{}) error {
	previous, duration, err := w.limitedPollMetadata(ctx)
	if err != nil {
		return err
	}
	if trigger != nil {
		var blockedUntil time.Time
		if duration > w.pollInterval {
			blockedUntil = time.Now().Add(duration)
		}
		for {
			select {
			case <-ctx.Done():
				return nil
			case _, ok := <-trigger:
				if !ok {
					return nil
				}
				if time.Now().Before(blockedUntil) {
					// 慢扫描期间到达的共享 tick 直接合并，避免元数据轮询形成满占空比热循环。
					continue
				}
				current, pollDuration, pollErr := w.limitedPollKnownMetadata(ctx, previous)
				if pollDuration > w.pollInterval {
					blockedUntil = time.Now().Add(pollDuration)
				} else {
					blockedUntil = time.Time{}
				}
				if pollErr != nil {
					if ctx.Err() != nil {
						return nil
					}
					w.onError(fmt.Errorf("轮询 Trellis 元数据: %w", pollErr))
					continue
				}
				changes := diffMetadata(w.root, previous, current)
				previous = current
				if !changes.Empty() && w.onChange != nil {
					w.onChange(ctx, changes)
				}
			}
		}
	}
	delay := pollDelayFor(w.pollInterval, duration)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			current, duration, err := w.limitedPollKnownMetadata(ctx, previous)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				w.onError(fmt.Errorf("轮询 Trellis 元数据: %w", err))
				timer.Reset(pollDelayFor(w.pollInterval, duration))
				continue
			}
			changes := diffMetadata(w.root, previous, current)
			previous = current
			if !changes.Empty() && w.onChange != nil {
				w.onChange(ctx, changes)
			}
			timer.Reset(pollDelayFor(w.pollInterval, duration))
		}
	}
}

type metadataStamp struct {
	Mode    uint32
	Size    int64
	ModTime int64
}

type metadataSnapshot map[string]metadataStamp

func (w *Watcher) limitedPollMetadata(ctx context.Context) (metadataSnapshot, time.Duration, error) {
	return w.withMetadataPollLimit(ctx, w.pollMetadata)
}

func (w *Watcher) limitedPollKnownMetadata(
	ctx context.Context,
	previous metadataSnapshot,
) (metadataSnapshot, time.Duration, error) {
	return w.withMetadataPollLimit(ctx, func(ctx context.Context) (metadataSnapshot, error) {
		return w.pollKnownMetadata(ctx, previous)
	})
}

func (w *Watcher) withMetadataPollLimit(
	ctx context.Context,
	poll func(context.Context) (metadataSnapshot, error),
) (metadataSnapshot, time.Duration, error) {
	select {
	case metadataPollSemaphore <- struct{}{}:
		defer func() { <-metadataPollSemaphore }()
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	}
	started := time.Now()
	metadata, err := poll(ctx)
	return metadata, time.Since(started), err
}

func pollDelay(previousDuration time.Duration) time.Duration {
	return pollDelayFor(minPollInterval, previousDuration)
}

func pollDelayFor(interval, previousDuration time.Duration) time.Duration {
	if previousDuration > interval {
		// 扫描本身很慢时至少等待同等时长，将单项目轮询占空比限制在约 50%。
		return previousDuration
	}
	return interval
}

func (w *Watcher) pollMetadata(ctx context.Context) (metadataSnapshot, error) {
	validatedRoot, err := trellis.ValidateRoot(w.root)
	if err != nil || filepath.Clean(validatedRoot) != filepath.Clean(w.root) {
		if err == nil {
			err = fmt.Errorf("项目根目录真实路径已变化: 注册=%s 当前=%s", w.root, validatedRoot)
		}
		return nil, err
	}
	metadata := make(metadataSnapshot)
	entries := 0
	record := func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entries++
		if entries > MaxPolledEntries {
			return fmt.Errorf("%w: 轮询项超过 %d", ErrWatchLimit, MaxPolledEntries)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(w.root, path)
		if err != nil {
			return err
		}
		metadata[filepath.ToSlash(relative)] = metadataStamp{
			Mode: uint32(info.Mode()), Size: info.Size(), ModTime: info.ModTime().UnixNano(),
		}
		return nil
	}
	trellisRoot := filepath.Join(w.root, ".trellis")
	for _, relative := range []string{"workflow.md", "config.yaml"} {
		path := filepath.Join(trellisRoot, relative)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			metadata[filepath.ToSlash(filepath.Join(".trellis", relative))] = metadataStamp{}
			continue
		}
		if err != nil {
			return nil, err
		}
		if err := record(path, fs.FileInfoToDirEntry(info), nil); err != nil {
			return nil, err
		}
	}
	for _, relative := range []string{"tasks", ".runtime", "spec"} {
		path := filepath.Join(trellisRoot, relative)
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			metadata[filepath.ToSlash(filepath.Join(".trellis", relative))] = metadataStamp{}
			continue
		} else if err != nil {
			return nil, err
		}
		if err := streamwalk.Walk(ctx, w.root, path, record); err != nil {
			return nil, err
		}
	}
	return metadata, nil
}

// pollKnownMetadata 在目录结构稳定时只 stat 首次遍历得到的路径，避免每 10 秒
// 对每个任务目录再次执行 ReadDir。目录时间或类型变化意味着新增、删除或重命名，
// 此时立即回退完整遍历，保证新路径也进入下一轮已知集合。
func (w *Watcher) pollKnownMetadata(ctx context.Context, previous metadataSnapshot) (metadataSnapshot, error) {
	validatedRoot, err := trellis.ValidateRoot(w.root)
	if err != nil || filepath.Clean(validatedRoot) != filepath.Clean(w.root) {
		if err == nil {
			err = fmt.Errorf("项目根目录真实路径已变化: 注册=%s 当前=%s", w.root, validatedRoot)
		}
		return nil, err
	}
	if len(previous) > MaxPolledEntries {
		return nil, fmt.Errorf("%w: 轮询项超过 %d", ErrWatchLimit, MaxPolledEntries)
	}
	paths := make([]string, 0, len(previous))
	for relative := range previous {
		paths = append(paths, relative)
	}
	sort.Strings(paths)
	type statResult struct {
		stamp    metadataStamp
		fallback bool
		err      error
	}
	results := make([]statResult, len(paths))
	workerCount := knownMetadataWorkers
	if len(paths) < workerCount {
		workerCount = len(paths)
	}
	jobs := make(chan int, len(paths))
	for index := range paths {
		jobs <- index
	}
	close(jobs)
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for index := range jobs {
				if err := ctx.Err(); err != nil {
					results[index].err = err
					continue
				}
				relative := paths[index]
				old := previous[relative]
				path := filepath.Join(w.root, filepath.FromSlash(relative))
				info, err := os.Lstat(path)
				if errors.Is(err, os.ErrNotExist) {
					if old == (metadataStamp{}) {
						// 缺失的可选资源保留零值哨兵，以便后续出现时被发现。
						results[index].stamp = old
						continue
					}
					results[index].fallback = true
					continue
				}
				if err != nil {
					results[index].err = err
					continue
				}
				stamp := metadataStampFromInfo(info)
				results[index].stamp = stamp
				results[index].fallback = stamp != old && (os.FileMode(old.Mode).IsDir() || info.IsDir())
			}
		}()
	}
	workers.Wait()

	current := make(metadataSnapshot, len(previous))
	fallback := false
	for index, relative := range paths {
		if results[index].err != nil {
			return nil, results[index].err
		}
		fallback = fallback || results[index].fallback
		current[relative] = results[index].stamp
	}
	if fallback {
		return w.pollMetadata(ctx)
	}
	return current, nil
}

func metadataStampFromInfo(info fs.FileInfo) metadataStamp {
	return metadataStamp{Mode: uint32(info.Mode()), Size: info.Size(), ModTime: info.ModTime().UnixNano()}
}

func diffMetadata(root string, previous, current metadataSnapshot) ChangeSet {
	paths := make([]string, 0)
	for path, stamp := range current {
		if old, exists := previous[path]; !exists || old != stamp {
			paths = append(paths, filepath.Join(root, filepath.FromSlash(path)))
		}
	}
	for path := range previous {
		if _, exists := current[path]; !exists {
			paths = append(paths, filepath.Join(root, filepath.FromSlash(path)))
		}
	}
	return classifyPaths(root, paths)
}

func classifyPaths(root string, paths []string) ChangeSet {
	changes := ChangeSet{}
	seen := make(map[string]struct{}, len(paths))
	for _, candidate := range paths {
		relative, err := filepath.Rel(root, candidate)
		if err != nil {
			changes.Full = true
			continue
		}
		relative = filepath.ToSlash(filepath.Clean(relative))
		if relative == "." || relative == ".." || strings.HasPrefix(relative, "../") {
			changes.Full = true
			continue
		}
		if _, exists := seen[relative]; !exists {
			seen[relative] = struct{}{}
			changes.Paths = append(changes.Paths, relative)
		}
		switch {
		case relative == ".trellis/workflow.md":
			changes.Workflow = true
		case relative == ".trellis/config.yaml":
			changes.Full = true
		case relative == ".trellis/tasks" || strings.HasPrefix(relative, ".trellis/tasks/"):
			changes.Tasks = true
		case relative == ".trellis/.runtime" || strings.HasPrefix(relative, ".trellis/.runtime/"):
			changes.Sessions = true
		case relative == ".trellis/spec" || strings.HasPrefix(relative, ".trellis/spec/"):
			changes.Specs = true
		default:
			changes.Full = true
		}
	}
	sort.Strings(changes.Paths)
	return changes
}
