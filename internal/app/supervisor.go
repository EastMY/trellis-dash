package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/yunnnn/trellis-dash/internal/gitstate"
	"github.com/yunnnn/trellis-dash/internal/model"
	"github.com/yunnnn/trellis-dash/internal/store"
	"github.com/yunnnn/trellis-dash/internal/trellis"
	"github.com/yunnnn/trellis-dash/internal/watcher"
)

type SupervisorOptions struct {
	Debounce           time.Duration
	RefreshInterval    time.Duration
	FullRescanInterval time.Duration
}

type snapshotScanner interface {
	Scan(context.Context, model.Project) (model.TrellisSnapshot, error)
}

type incrementalSnapshotScanner interface {
	ScanTask(context.Context, model.Project, model.Task) (model.TaskBundle, bool, error)
	ScanSessions(context.Context, model.Project, []model.Task) (model.SessionIndexSnapshot, error)
	ScanWorkflowStates(context.Context, model.Project) ([]model.WorkflowState, model.ScanStats, error)
	ScanSpecsHash(context.Context, model.Project) (string, model.ScanStats, error)
}

type contextRevalidator interface {
	RevalidateContextEntries(context.Context, string, []model.ContextEntry) ([]model.ContextEntry, error)
}

type gitInspector interface {
	Snapshot(context.Context, string, string) (model.GitSnapshot, error)
}

// Supervisor 为每个项目维护文件监听、低频全量校验和 Git 状态轮询。
// 每个项目只有一个 worker，扫描和 Git 采集不会因密集文件事件而并发堆积。
type Supervisor struct {
	store     *store.Store
	scanner   snapshotScanner
	inspector gitInspector
	logger    *slog.Logger
	options   SupervisorOptions

	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	stopDone chan struct{}
	runners  map[string]*projectRunner
}

type rescanRequest struct {
	ctx    context.Context
	result chan<- error
}

type projectRunner struct {
	project model.Project
	cancel  context.CancelFunc
	// dirty 容量固定为 1：已有待处理扫描时，后续文件事件直接合并。
	dirty    chan struct{}
	rescans  chan rescanRequest
	done     chan struct{}
	changeMu sync.Mutex
	pending  watcher.ChangeSet
}

var errRunnerStopped = errors.New("项目观察已停止")

const fullScanHeapReleaseThreshold = 8 << 20

func NewSupervisor(
	repository *store.Store,
	scanner *trellis.Scanner,
	inspector *gitstate.Inspector,
	logger *slog.Logger,
	options SupervisorOptions,
) *Supervisor {
	return newSupervisor(repository, scanner, inspector, logger, options)
}

// newSupervisor 接受窄接口，便于对 worker 的取消、合并与串行语义做确定性测试。
func newSupervisor(
	repository *store.Store,
	scanner snapshotScanner,
	inspector gitInspector,
	logger *slog.Logger,
	options SupervisorOptions,
) *Supervisor {
	if options.Debounce <= 0 {
		options.Debounce = 250 * time.Millisecond
	}
	if options.RefreshInterval <= 0 {
		options.RefreshInterval = 10 * time.Second
	}
	if options.FullRescanInterval <= 0 {
		options.FullRescanInterval = 10 * time.Minute
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Supervisor{
		store: repository, scanner: scanner, inspector: inspector,
		logger: logger, options: options, runners: make(map[string]*projectRunner),
	}
}

func (s *Supervisor) Start(ctx context.Context) error {
	for {
		s.mu.Lock()
		if s.stopDone != nil {
			stopDone := s.stopDone
			s.mu.Unlock()
			select {
			case <-stopDone:
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if s.cancel != nil {
			s.mu.Unlock()
			return nil
		}
		s.ctx, s.cancel = context.WithCancel(ctx)
		s.mu.Unlock()
		break
	}

	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		s.Stop()
		return err
	}
	for _, project := range projects {
		validatedRoot, err := trellis.ValidateRoot(project.Root)
		if err != nil || validatedRoot != project.Root {
			if err == nil {
				err = fmt.Errorf("项目根目录真实路径已变化: 注册=%s 当前=%s", project.Root, validatedRoot)
			}
			s.logger.Error("拒绝观察无效的持久化项目", "project", project.ID, "error", err)
			_ = s.store.SetIndexError(context.WithoutCancel(ctx), project.ID, err)
			continue
		}
		if err := store.ValidateDatabaseOutsideProject(s.store.DatabasePath(), project.Root); err != nil {
			s.logger.Error("拒绝观察与数据库目录重叠的项目", "project", project.ID, "error", err)
			_ = s.store.SetIndexError(context.WithoutCancel(ctx), project.ID, err)
			continue
		}
		s.Register(project)
	}
	return nil
}

// Stop 取消所有项目并等待 worker（包括其文件监听器）退出后再返回。
func (s *Supervisor) Stop() {
	s.mu.Lock()
	if s.stopDone != nil {
		stopDone := s.stopDone
		s.mu.Unlock()
		<-stopDone
		return
	}
	if s.cancel == nil {
		s.mu.Unlock()
		return
	}
	stopDone := make(chan struct{})
	s.stopDone = stopDone
	s.cancel()
	runners := make([]*projectRunner, 0, len(s.runners))
	for _, runner := range s.runners {
		runner.cancel()
		runners = append(runners, runner)
	}
	s.runners = make(map[string]*projectRunner)
	s.ctx = nil
	s.cancel = nil
	s.mu.Unlock()

	for _, runner := range runners {
		<-runner.done
	}
	s.mu.Lock()
	close(stopDone)
	s.stopDone = nil
	s.mu.Unlock()
}

func (s *Supervisor) Register(project model.Project) {
	s.register(project, false)
}

// Ensure 只在项目尚无 runner 时恢复观察，适合暂时失效的持久化路径重新出现后自愈。
func (s *Supervisor) Ensure(project model.Project) {
	s.register(project, true)
}

func (s *Supervisor) register(project model.Project, onlyIfAbsent bool) {
	s.mu.Lock()
	// Register 只在 Supervisor 已启动时生效，避免 Stop 后遗留后台 goroutine。
	if s.ctx == nil || s.cancel == nil || s.stopDone != nil {
		s.mu.Unlock()
		s.logger.Warn("Supervisor 尚未启动，跳过项目观察", "project", project.ID)
		return
	}
	previous := s.runners[project.ID]
	if onlyIfAbsent && previous != nil {
		s.mu.Unlock()
		return
	}
	if previous != nil {
		previous.cancel()
	}

	ctx, cancel := context.WithCancel(s.ctx)
	runner := &projectRunner{
		project: project,
		cancel:  cancel,
		dirty:   make(chan struct{}, 1),
		rescans: make(chan rescanRequest),
		done:    make(chan struct{}),
	}
	s.runners[project.ID] = runner
	s.mu.Unlock()

	// 新 worker 先等待旧 worker 退出，避免相同项目的新旧扫描短暂重叠。
	go func() {
		defer close(runner.done)
		if previous != nil {
			<-previous.done
		}
		if ctx.Err() != nil {
			return
		}
		s.runProject(ctx, runner)
	}()
}

// Remove 取消指定项目并等待 worker 完整退出，之后该项目不会再写入缓存。
func (s *Supervisor) Remove(projectID string) {
	s.mu.Lock()
	runner := s.runners[projectID]
	if runner == nil {
		s.mu.Unlock()
		return
	}
	// 等待期间保留 retiring runner，后续 Register 会排在它之后启动，避免新旧 worker 重叠。
	runner.cancel()
	s.mu.Unlock()
	<-runner.done

	s.mu.Lock()
	if s.runners[projectID] == runner {
		delete(s.runners, projectID)
	}
	s.mu.Unlock()
}

// Rescan 将手动重扫投递给项目唯一 worker，并等待扫描和 Git 刷新完成。
func (s *Supervisor) Rescan(ctx context.Context, projectID string) error {
	s.mu.Lock()
	runner := s.runners[projectID]
	s.mu.Unlock()
	if runner == nil {
		project, err := s.store.GetProject(ctx, projectID)
		if err != nil {
			return err
		}
		validatedRoot, err := trellis.ValidateRoot(project.Root)
		if err != nil || validatedRoot != project.Root {
			if err == nil {
				err = fmt.Errorf("项目根目录真实路径已变化: 注册=%s 当前=%s", project.Root, validatedRoot)
			}
			return err
		}
		if err := store.ValidateDatabaseOutsideProject(s.store.DatabasePath(), project.Root); err != nil {
			return err
		}
		s.Ensure(project)
		s.mu.Lock()
		runner = s.runners[projectID]
		s.mu.Unlock()
		if runner == nil {
			return errRunnerStopped
		}
	}

	result := make(chan error, 1)
	request := rescanRequest{ctx: ctx, result: result}
	select {
	case runner.rescans <- request:
	case <-runner.done:
		return errRunnerStopped
	case <-ctx.Done():
		return ctx.Err()
	}

	select {
	case err := <-result:
		return err
	case <-runner.done:
		// worker 会优先尝试回传结果；若已退出，返回稳定的生命周期错误。
		select {
		case err := <-result:
			return err
		default:
			return errRunnerStopped
		}
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (runner *projectRunner) markDirty(values ...watcher.ChangeSet) {
	changes := watcher.ChangeSet{Full: true}
	if len(values) > 0 {
		changes = values[0]
	}
	runner.changeMu.Lock()
	runner.pending.Merge(changes)
	runner.changeMu.Unlock()
	select {
	case runner.dirty <- struct{}{}:
	default:
		// 已有待处理信号时直接合并，避免为每次 fsnotify 事件创建 goroutine。
	}
}

func (runner *projectRunner) takeChanges() watcher.ChangeSet {
	runner.changeMu.Lock()
	defer runner.changeMu.Unlock()
	changes := runner.pending
	runner.pending = watcher.ChangeSet{}
	return changes
}

func (s *Supervisor) runProject(ctx context.Context, runner *projectRunner) {
	s.logger.Info("启动项目观察", "project", runner.project.ID, "root", runner.project.Root)

	watch := watcher.NewWithChanges(
		runner.project.Root,
		s.options.Debounce,
		s.options.RefreshInterval,
		func(_ context.Context, changes watcher.ChangeSet) { runner.markDirty(changes) },
		func(err error) {
			s.logger.Warn("文件监听异常", "project", runner.project.ID, "error", err)
		},
	)
	// 容量 1 会合并尚未完成的元数据刷新；同一项目不会因慢磁盘积压轮询任务。
	metadataRefresh := make(chan struct{}, 1)
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		if err := watch.RunWithPollTrigger(ctx, metadataRefresh); err != nil && ctx.Err() == nil {
			s.logger.Warn("文件监听退出，将依赖周期全量扫描", "project", runner.project.ID, "error", err)
		}
	}()

	if err := s.scanProject(ctx, runner); err != nil && ctx.Err() == nil {
		s.logger.Warn("首次 Trellis 索引失败", "project", runner.project.ID, "error", err)
	}
	if _, err := s.refreshGit(ctx, runner); err != nil && ctx.Err() == nil {
		s.logger.Debug("首次 Git 采集失败", "project", runner.project.ID, "error", err)
	}

	refreshTicker := time.NewTicker(s.options.RefreshInterval)
	// 全量一致性校验使用一次性 timer，项目内只保留一个周期 ticker。
	rescanTimer := time.NewTimer(s.options.FullRescanInterval)
	defer refreshTicker.Stop()
	defer rescanTimer.Stop()
	for {
		select {
		case <-ctx.Done():
			// watcher 使用同一取消上下文；等待它退出，确保 Stop/Remove 无残留 goroutine。
			if watchDone != nil {
				<-watchDone
			}
			return
		case <-watchDone:
			watchDone = nil
		case <-runner.dirty:
			if err := s.scanChanges(ctx, runner, runner.takeChanges()); err != nil && ctx.Err() == nil {
				s.logger.Warn("增量索引失败", "project", runner.project.ID, "error", err)
			}
		case request := <-runner.rescans:
			requestCtx, cancel := joinedContext(ctx, request.ctx)
			err := s.scanAndRefresh(requestCtx, runner)
			cancel()
			request.result <- err
		case <-refreshTicker.C:
			// BSD/macOS watcher 与 Git 共用这一 ticker；Linux watcher 会忽略该信号并继续使用 fsnotify。
			select {
			case metadataRefresh <- struct{}{}:
			default:
			}
			_, err := s.refreshGit(ctx, runner)
			if err != nil && ctx.Err() == nil {
				s.logger.Debug("Git 状态刷新失败", "project", runner.project.ID, "error", err)
			}
		case <-rescanTimer.C:
			if err := s.scanProject(ctx, runner); err != nil && ctx.Err() == nil {
				s.logger.Warn("周期一致性扫描失败", "project", runner.project.ID, "error", err)
			}
			rescanTimer.Reset(s.options.FullRescanInterval)
		}
	}
}

func (s *Supervisor) scanAndRefresh(ctx context.Context, runner *projectRunner) error {
	if err := s.scanProject(ctx, runner); err != nil {
		return err
	}
	_, err := s.refreshGit(ctx, runner)
	return err
}

func (s *Supervisor) scanProject(ctx context.Context, runner *projectRunner) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	started := time.Now()
	snapshot, err := s.scanner.Scan(ctx, runner.project)
	if err != nil {
		// Stop/Remove 触发的取消不是索引故障，不能覆盖现有健康状态。
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		_ = s.store.SetIndexError(context.WithoutCancel(ctx), runner.project.ID, err)
		return fmt.Errorf("扫描 Trellis: %w", err)
	}
	scanDuration := time.Since(started)
	writeStarted := time.Now()
	changed, err := s.store.ReplaceTrellisSnapshot(ctx, runner.project.ID, snapshot)
	writeDuration := time.Since(writeStarted)
	taskCount, stats := len(snapshot.Tasks), snapshot.Stats
	// 大型全量快照会短暂持有全部文档正文。事务结束后立即断开引用并归还临时堆，
	// 避免 Go 的历史堆高水位长期表现为 Dashboard 的常驻内存。
	snapshot = model.TrellisSnapshot{}
	memoryReleaseStarted := time.Now()
	if stats.RawBytes >= fullScanHeapReleaseThreshold {
		debug.FreeOSMemory()
	}
	memoryReleaseDuration := time.Since(memoryReleaseStarted)
	databaseBytes, walBytes := s.store.DatabaseFileSizes()
	s.logger.Debug("性能指标", "project", runner.project.ID, "operation", "trellis.scan.full",
		"duration", time.Since(started), "scanDuration", scanDuration, "writeDuration", writeDuration,
		"memoryReleaseDuration", memoryReleaseDuration, "changed", changed, "tasks", taskCount,
		"walkEntries", stats.WalkEntries, "rawBytes", stats.RawBytes,
		"databaseBytes", databaseBytes, "walBytes", walBytes)
	return err
}

func (s *Supervisor) scanChanges(ctx context.Context, runner *projectRunner, changes watcher.ChangeSet) error {
	scanner, ok := s.scanner.(incrementalSnapshotScanner)
	if !ok || changes.Full || changes.Empty() {
		return s.scanProject(ctx, runner)
	}
	started := time.Now()
	changed := false
	walkEntries := 0
	var rawBytes int64
	if changes.Tasks {
		tasks, err := s.store.ListAllTasksForIndex(ctx, runner.project.ID)
		if err != nil {
			return err
		}
		matched, complete := matchChangedTasks(changes.Paths, tasks)
		if !complete {
			return s.scanProject(ctx, runner)
		}
		for _, task := range matched {
			bundle, exists, err := scanner.ScanTask(ctx, runner.project, task)
			if err != nil {
				return fmt.Errorf("增量扫描任务 %s: %w", task.Key, err)
			}
			walkEntries += bundle.Stats.WalkEntries
			rawBytes += bundle.Stats.RawBytes
			var itemChanged bool
			if exists {
				itemChanged, err = s.store.ReplaceTaskBundle(ctx, runner.project.ID, bundle)
			} else {
				itemChanged, err = s.store.DeleteTask(ctx, runner.project.ID, task.Key)
			}
			if err != nil {
				return err
			}
			changed = changed || itemChanged
		}
	}
	if changes.Workflow {
		states, stats, err := scanner.ScanWorkflowStates(ctx, runner.project)
		if err != nil {
			return err
		}
		itemChanged, err := s.store.ReplaceWorkflowStates(ctx, runner.project.ID, states)
		if err != nil {
			return err
		}
		changed = changed || itemChanged
		walkEntries += stats.WalkEntries
		rawBytes += stats.RawBytes
		s.logger.Debug("性能指标", "project", runner.project.ID, "operation", "trellis.scan.workflow",
			"walkEntries", stats.WalkEntries, "rawBytes", stats.RawBytes)
	}
	if changes.Sessions {
		tasks, err := s.store.ListAllTasksForIndex(ctx, runner.project.ID)
		if err != nil {
			return err
		}
		snapshot, err := scanner.ScanSessions(ctx, runner.project, tasks)
		if err != nil {
			return err
		}
		itemChanged, err := s.store.ReplaceSessionSnapshot(ctx, runner.project.ID, snapshot)
		if err != nil {
			return err
		}
		changed = changed || itemChanged
		walkEntries += snapshot.Stats.WalkEntries
		rawBytes += snapshot.Stats.RawBytes
	}
	if changes.Specs {
		hash, stats, err := scanner.ScanSpecsHash(ctx, runner.project)
		if err != nil {
			return err
		}
		itemChanged, err := s.store.ReplaceSpecsHash(ctx, runner.project.ID, hash)
		if err != nil {
			return err
		}
		changed = changed || itemChanged
		walkEntries += stats.WalkEntries
		rawBytes += stats.RawBytes
	}
	s.logger.Debug("性能指标", "project", runner.project.ID, "operation", "trellis.scan.incremental",
		"duration", time.Since(started), "changed", changed, "paths", len(changes.Paths),
		"walkEntries", walkEntries, "rawBytes", rawBytes)
	return nil
}

func matchChangedTasks(paths []string, tasks []model.Task) ([]model.Task, bool) {
	byKey := make(map[string]model.Task)
	for _, path := range paths {
		if path != ".trellis/tasks" && !strings.HasPrefix(path, ".trellis/tasks/") {
			continue
		}
		var selected *model.Task
		selectedLength := -1
		for _, task := range tasks {
			directory := filepath.ToSlash(filepath.Dir(task.SourcePath))
			if path == task.SourcePath || path == directory || strings.HasPrefix(path, directory+"/") {
				if len(directory) > selectedLength {
					copy := task
					selected = &copy
					selectedLength = len(directory)
				}
			}
		}
		if selected == nil {
			// 新任务、目录重命名或资源根变化需要重新分配全局唯一 Key。
			return nil, false
		}
		byKey[selected.Key] = *selected
	}
	items := make([]model.Task, 0, len(byKey))
	for _, task := range tasks {
		if matched, exists := byKey[task.Key]; exists {
			items = append(items, matched)
		}
	}
	return items, true
}

func (s *Supervisor) refreshGit(ctx context.Context, runner *projectRunner) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	started := time.Now()
	snapshot, inspectErr := s.inspector.Snapshot(ctx, runner.project.ID, runner.project.Root)
	if snapshot.Hash != "" {
		changed, err := s.store.ReplaceGitSnapshot(ctx, snapshot)
		if err != nil {
			return false, err
		}
		if changed {
			if validator, ok := s.scanner.(contextRevalidator); ok {
				entries, listErr := s.store.ListAllContextForIndex(ctx, runner.project.ID)
				if listErr != nil {
					return false, listErr
				}
				if len(entries) > 0 {
					validated, validateErr := validator.RevalidateContextEntries(ctx, runner.project.Root, entries)
					if validateErr != nil {
						return false, validateErr
					}
					if _, replaceErr := s.store.ReplaceContextValidity(ctx, runner.project.ID, validated); replaceErr != nil {
						return false, replaceErr
					}
				}
			}
		}
		// 可展示的错误快照已成功保存，项目仍可正常使用 Trellis 视图。
		s.logger.Debug("性能指标", "project", runner.project.ID, "operation", "git.snapshot",
			"duration", time.Since(started), "changed", changed, "files", len(snapshot.Files), "worktrees", len(snapshot.Worktrees))
		return changed, nil
	}
	return false, inspectErr
}

// joinedContext 同时服从项目生命周期和调用方超时，任一取消都会终止手动重扫。
func joinedContext(projectCtx, requestCtx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(requestCtx)
	stop := context.AfterFunc(projectCtx, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}
