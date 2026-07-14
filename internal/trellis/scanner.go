package trellis

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/yunnnn/trellis-dash/internal/model"
	"github.com/yunnnn/trellis-dash/internal/streamwalk"
)

var archiveMonthPattern = regexp.MustCompile(`^\d{4}-(?:0[1-9]|1[0-2])$`)

// Scanner 将 .trellis 文件系统事实源读取为一次可原子替换的快照。
// Scanner 不持有可变状态，可安全地被多个 goroutine 并发调用。
type Scanner struct{}

func NewScanner() *Scanner { return &Scanner{} }

type scannedTask struct {
	task       model.Task
	directory  string
	logicalDir string
}

type taskFile struct {
	path         string
	relativePath string
	archived     bool
	archiveMonth string
}

const taskResourceWorkers = 4

type taskResourceResult struct {
	artifacts []model.Artifact
	contexts  []model.ContextEntry
	stats     model.ScanStats
	phase     string
	err       error
}

type scannedSession struct {
	session    model.Session
	sourceHash string
	runPhase   string
}

// Scan 完整扫描任务、文档、Context、Session、工作流状态和规范树哈希。
func (s *Scanner) Scan(ctx context.Context, project model.Project) (model.TrellisSnapshot, error) {
	snapshot := model.TrellisSnapshot{
		Tasks:          make([]model.Task, 0),
		Artifacts:      make([]model.Artifact, 0),
		ContextEntries: make([]model.ContextEntry, 0),
		Sessions:       make([]model.Session, 0),
		WorkflowStates: make([]model.WorkflowState, 0),
	}
	if err := contextError(ctx); err != nil {
		return snapshot, err
	}

	root, err := ValidateRoot(project.Root)
	if err != nil {
		return snapshot, err
	}
	if filepath.Clean(root) != filepath.Clean(project.Root) {
		return snapshot, fmt.Errorf("项目根目录真实路径已变化: 注册=%s 当前=%s", project.Root, root)
	}
	budget := &scanBudget{}
	trellisRoot, err := resolveExistingPath(root, filepath.Join(root, ".trellis"))
	if err != nil {
		return snapshot, fmt.Errorf("解析 .trellis: %w", err)
	}

	items, err := s.scanTasks(ctx, root, trellisRoot, project.ID, budget)
	if err != nil {
		return snapshot, err
	}
	assignUniqueTaskKeys(items)

	taskByDirectory := make(map[string]string, len(items))
	for i := range items {
		taskByDirectory[filepath.Clean(items[i].directory)] = items[i].task.Key
	}
	resources := scanTaskResources(ctx, root, project.ID, items)
	var projectArtifactBytes int64
	var projectContextBytes int64
	for i := range items {
		result := resources[i]
		if result.err != nil {
			return snapshot, fmt.Errorf("扫描任务 %s %s: %w", items[i].task.Key, result.phase, result.err)
		}
		if err := budget.merge(result.stats, "任务 "+items[i].task.Key); err != nil {
			return snapshot, err
		}
		artifacts := result.artifacts
		if len(snapshot.Artifacts)+len(artifacts) > MaxProjectArtifacts {
			return snapshot, fmt.Errorf("%w: 项目文档超过 %d 个", ErrFileTooLarge, MaxProjectArtifacts)
		}
		for _, artifact := range artifacts {
			projectArtifactBytes += artifact.Size
		}
		if projectArtifactBytes > MaxProjectArtifactBytes {
			return snapshot, fmt.Errorf("%w: 项目文档总量超过 %d 字节", ErrFileTooLarge, MaxProjectArtifactBytes)
		}
		contexts := result.contexts
		if len(snapshot.ContextEntries)+len(contexts) > MaxProjectContextEntries {
			return snapshot, fmt.Errorf("%w: 项目 Context 超过 %d 条", ErrResourceLimit, MaxProjectContextEntries)
		}
		for _, entry := range contexts {
			projectContextBytes += int64(len(entry.File) + len(entry.Reason) + len(entry.Error))
		}
		if projectContextBytes > MaxProjectContextBytes {
			return snapshot, fmt.Errorf("%w: 项目 Context 内容超过 %d 字节", ErrResourceLimit, MaxProjectContextBytes)
		}
		items[i].task.ArtifactCount = len(artifacts)
		for _, entry := range contexts {
			if !entry.Valid {
				items[i].task.ContextIssues++
			}
		}
		snapshot.Artifacts = append(snapshot.Artifacts, artifacts...)
		snapshot.ContextEntries = append(snapshot.ContextEntries, contexts...)
	}

	workflowStates, err := scanWorkflowStates(ctx, root, trellisRoot, project.ID, budget)
	if err != nil {
		return snapshot, fmt.Errorf("扫描 workflow.md: %w", err)
	}
	snapshot.WorkflowStates = workflowStates

	sessions, err := scanSessions(ctx, root, trellisRoot, project.ID, taskByDirectory, budget)
	if err != nil {
		return snapshot, fmt.Errorf("扫描 Session: %w", err)
	}
	latestSession := make(map[string]int)
	taskIndex := make(map[string]int, len(items))
	for i := range items {
		taskIndex[items[i].task.Key] = i
	}
	for sessionIndex, session := range sessions {
		if session.session.TaskKey == "" || session.session.Stale {
			continue
		}
		if index, found := taskIndex[session.session.TaskKey]; found {
			items[index].task.ActiveSessions++
		}
		currentIndex, selected := latestSession[session.session.TaskKey]
		if !selected || newerSession(session.session, sessions[currentIndex].session) {
			latestSession[session.session.TaskKey] = sessionIndex
		}
	}
	for taskKey, sessionIndex := range latestSession {
		if index, found := taskIndex[taskKey]; found && sessions[sessionIndex].runPhase != "" {
			items[index].task.RuntimePhase = sessions[sessionIndex].runPhase
		}
	}

	for _, item := range items {
		snapshot.Tasks = append(snapshot.Tasks, item.task)
	}
	for _, session := range sessions {
		snapshot.Sessions = append(snapshot.Sessions, session.session)
	}

	sort.Slice(snapshot.Artifacts, func(i, j int) bool {
		if snapshot.Artifacts[i].TaskKey != snapshot.Artifacts[j].TaskKey {
			return snapshot.Artifacts[i].TaskKey < snapshot.Artifacts[j].TaskKey
		}
		return snapshot.Artifacts[i].Path < snapshot.Artifacts[j].Path
	})
	sort.Slice(snapshot.ContextEntries, func(i, j int) bool {
		left, right := snapshot.ContextEntries[i], snapshot.ContextEntries[j]
		if left.TaskKey != right.TaskKey {
			return left.TaskKey < right.TaskKey
		}
		if left.Action != right.Action {
			return left.Action < right.Action
		}
		return left.Line < right.Line
	})

	setTaskIndexHashes(&snapshot)
	snapshot.TasksHash = hashTaskResources(snapshot)
	snapshot.SessionsHash = hashSessions(sessions)
	snapshot.SpecsHash, err = hashSpecs(ctx, root, trellisRoot, budget)
	if err != nil {
		return snapshot, fmt.Errorf("扫描规范目录: %w", err)
	}
	snapshot.Stats = budget.stats()
	return snapshot, nil
}

// scanTaskResources 用固定 worker 数并发读取不同任务；每个任务使用独立预算，
// 调用方随后按任务稳定顺序合并，既提升 I/O 利用率又保持错误与上限确定性。
func scanTaskResources(
	ctx context.Context,
	root, projectID string,
	items []scannedTask,
) []taskResourceResult {
	results := make([]taskResourceResult, len(items))
	if len(items) == 0 {
		return results
	}
	workerCount := taskResourceWorkers
	if len(items) < workerCount {
		workerCount = len(items)
	}
	jobs := make(chan int, len(items))
	for index := range items {
		jobs <- index
	}
	close(jobs)

	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for index := range jobs {
				localBudget := &scanBudget{}
				artifacts, err := scanArtifacts(ctx, root, projectID, items[index], localBudget)
				if err != nil {
					results[index] = taskResourceResult{phase: "文档", err: err, stats: localBudget.stats()}
					continue
				}
				contexts, err := scanContextManifests(ctx, root, projectID, items[index], localBudget)
				if err != nil {
					results[index] = taskResourceResult{phase: "Context", err: err, stats: localBudget.stats()}
					continue
				}
				results[index] = taskResourceResult{
					artifacts: artifacts,
					contexts:  contexts,
					stats:     localBudget.stats(),
				}
			}
		}()
	}
	workers.Wait()
	return results
}

func newerSession(candidate, current model.Session) bool {
	switch {
	case candidate.LastSeenAt != nil && current.LastSeenAt == nil:
		return true
	case candidate.LastSeenAt == nil && current.LastSeenAt != nil:
		return false
	case candidate.LastSeenAt != nil && current.LastSeenAt != nil && !candidate.LastSeenAt.Equal(*current.LastSeenAt):
		return candidate.LastSeenAt.After(*current.LastSeenAt)
	default:
		// 时间缺失或相同时用 Key 保持确定性，不依赖 os.ReadDir 返回顺序。
		return candidate.Key > current.Key
	}
}

// assignUniqueTaskKeys 优先保留活跃任务的常用目录键；发生历史目录碰撞时，
// 使用完整相对目录的 SHA-256 前缀生成稳定且适合放进单段 URL 的无斜杠键。
func assignUniqueTaskKeys(items []scannedTask) {
	order := make([]int, len(items))
	for i := range items {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		left, right := items[order[i]].task, items[order[j]].task
		if left.Archived != right.Archived {
			return !left.Archived
		}
		return left.SourcePath < right.SourcePath
	})

	used := make(map[string]struct{}, len(items))
	for _, index := range order {
		desired := items[index].task.Key
		if _, exists := used[desired]; exists {
			desired = uniquePathKey(desired, items[index].logicalDir, used)
		}
		items[index].task.Key = desired
		used[desired] = struct{}{}
	}
}

func uniquePathKey(base, logicalDir string, used map[string]struct{}) string {
	digest := hashBytes([]byte(filepath.ToSlash(logicalDir)))
	for length := 12; length <= len(digest); length += 4 {
		if length > len(digest) {
			length = len(digest)
		}
		candidate := base + "~" + digest[:length]
		if _, exists := used[candidate]; !exists {
			return candidate
		}
		if length == len(digest) {
			break
		}
	}
	// SHA-256 全长碰撞在实践中不可达；保留确定性计数兜底，避免静默覆盖。
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s~%s-%d", base, digest, suffix)
		if _, exists := used[candidate]; !exists {
			return candidate
		}
	}
}

func (s *Scanner) scanTasks(ctx context.Context, root, trellisRoot, projectID string, budget *scanBudget) ([]scannedTask, error) {
	tasksRoot, exists, err := optionalDirectory(root, filepath.Join(trellisRoot, "tasks"))
	if err != nil {
		return nil, fmt.Errorf("解析 tasks 目录: %w", err)
	}
	if !exists {
		return make([]scannedTask, 0), nil
	}

	files := make([]taskFile, 0)
	walkEntries := 0
	err = streamwalk.Walk(ctx, root, tasksRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := contextError(ctx); err != nil {
			return err
		}
		walkEntries++
		if err := budget.addWalk("tasks"); err != nil {
			return err
		}
		if walkEntries > MaxWalkEntries {
			return fmt.Errorf("%w: tasks 遍历项超过 %d", ErrResourceLimit, MaxWalkEntries)
		}

		if entry.Type()&os.ModeSymlink != 0 {
			if _, err := resolveExistingPath(root, path); err != nil {
				return err
			}
		}
		if entry.IsDir() || entry.Name() != "task.json" {
			return nil
		}

		relative, err := filepath.Rel(tasksRoot, path)
		if err != nil {
			return err
		}
		parts := splitPath(relative)
		archived := len(parts) > 0 && parts[0] == "archive"
		archiveMonth := ""
		if archived {
			// 归档事实源只接受 archive/YYYY-MM/...，避免误收模板或临时目录。
			if len(parts) < 3 || !archiveMonthPattern.MatchString(parts[1]) {
				return nil
			}
			archiveMonth = parts[1]
		}
		files = append(files, taskFile{
			path:         path,
			relativePath: relative,
			archived:     archived,
			archiveMonth: archiveMonth,
		})
		if len(files) > MaxTasksPerProject {
			return fmt.Errorf("%w: task.json 超过 %d 个", ErrResourceLimit, MaxTasksPerProject)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(files, func(i, j int) bool {
		return filepath.ToSlash(files[i].relativePath) < filepath.ToSlash(files[j].relativePath)
	})
	result := make([]scannedTask, 0, len(files))
	var taskJSONBytes int64
	for _, file := range files {
		if err := contextError(ctx); err != nil {
			return nil, err
		}
		logicalSource := filepath.ToSlash(filepath.Join(".trellis", "tasks", file.relativePath))
		task, bytesRead, err := parseTask(root, projectID, file, logicalSource)
		if err != nil {
			return nil, fmt.Errorf("解析 %s: %w", logicalSource, err)
		}
		taskJSONBytes += bytesRead
		if err := budget.addRead(bytesRead, "task.json"); err != nil {
			return nil, err
		}
		if taskJSONBytes > MaxTaskJSONTotalBytes {
			return nil, fmt.Errorf("%w: task.json 总量超过 %d 字节", ErrResourceLimit, MaxTaskJSONTotalBytes)
		}
		logicalDir := filepath.ToSlash(filepath.Dir(logicalSource))
		result = append(result, scannedTask{
			task:       task,
			directory:  filepath.Dir(file.path),
			logicalDir: logicalDir,
		})
	}
	return result, nil
}

func contextError(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func splitPath(path string) []string {
	cleaned := filepath.ToSlash(filepath.Clean(path))
	if cleaned == "." || cleaned == "" {
		return nil
	}
	return strings.Split(cleaned, "/")
}

func hashTaskResources(snapshot model.TrellisSnapshot) string {
	hasher := newStableHasher()
	for _, task := range snapshot.Tasks {
		hasher.add(struct {
			TaskKey   string
			IndexHash string
		}{task.Key, task.IndexHash})
	}
	for _, state := range snapshot.WorkflowStates {
		hasher.add(state)
	}
	return hasher.sum()
}

func setTaskIndexHashes(snapshot *model.TrellisSnapshot) {
	artifacts := make(map[string][]model.Artifact)
	for _, artifact := range snapshot.Artifacts {
		artifacts[artifact.TaskKey] = append(artifacts[artifact.TaskKey], artifact)
	}
	entries := make(map[string][]model.ContextEntry)
	for _, entry := range snapshot.ContextEntries {
		entries[entry.TaskKey] = append(entries[entry.TaskKey], entry)
	}
	for index := range snapshot.Tasks {
		task := &snapshot.Tasks[index]
		task.IndexHash = taskIndexHash(*task, artifacts[task.Key], entries[task.Key])
	}
}

func hashSessions(sessions []scannedSession) string {
	hasher := newStableHasher()
	for _, session := range sessions {
		hasher.add(struct {
			Key        string
			SourceHash string
			TaskKey    string
			Stale      bool
		}{session.session.Key, session.sourceHash, session.session.TaskKey, session.session.Stale})
	}
	return hasher.sum()
}

func hashSpecs(ctx context.Context, root, trellisRoot string, budget *scanBudget) (string, error) {
	specRoot, exists, err := optionalDirectory(root, filepath.Join(trellisRoot, "spec"))
	if err != nil {
		return "", err
	}
	hasher := newStableHasher()
	if !exists {
		return hasher.sum(), nil
	}

	type specRecord struct {
		Path string
		Hash string
	}
	records := make([]specRecord, 0)
	walkEntries := 0
	var totalBytes int64
	err = streamwalk.Walk(ctx, root, specRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := contextError(ctx); err != nil {
			return err
		}
		walkEntries++
		if err := budget.addWalk("spec"); err != nil {
			return err
		}
		if walkEntries > MaxWalkEntries {
			return fmt.Errorf("%w: spec 遍历项超过 %d", ErrResourceLimit, MaxWalkEntries)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if _, err := resolveExistingPath(root, path); err != nil {
				return err
			}
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			return nil
		}
		content, _, err := readSafeFile(root, path, MaxMarkdownBytes)
		if err != nil {
			return err
		}
		totalBytes += int64(len(content))
		if err := budget.addRead(int64(len(content)), "spec"); err != nil {
			return err
		}
		if totalBytes > MaxSpecTotalBytes {
			return fmt.Errorf("%w: spec 总量超过 %d 字节", ErrResourceLimit, MaxSpecTotalBytes)
		}
		relative, err := filepath.Rel(specRoot, path)
		if err != nil {
			return err
		}
		records = append(records, specRecord{filepath.ToSlash(relative), hashBytes(content)})
		if len(records) > MaxSpecFiles {
			return fmt.Errorf("%w: spec 文件超过 %d 个", ErrResourceLimit, MaxSpecFiles)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Path < records[j].Path })
	for _, record := range records {
		hasher.add(record)
	}
	return hasher.sum(), nil
}
