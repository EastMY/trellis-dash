package trellis

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yunnnn/trellis-dash/internal/model"
)

// ScanTask 只重读一个已经存在于缓存中的任务目录。
// 新增任务和目录重命名由调用方回退到全量扫描，以继续保证唯一 Key 语义。
func (s *Scanner) ScanTask(ctx context.Context, project model.Project, existing model.Task) (model.TaskBundle, bool, error) {
	var bundle model.TaskBundle
	root, err := ValidateRoot(project.Root)
	if err != nil {
		return bundle, false, err
	}
	logicalSource := filepath.ToSlash(filepath.Clean(existing.SourcePath))
	const taskPrefix = ".trellis/tasks/"
	if !strings.HasPrefix(logicalSource, taskPrefix) || filepath.Base(logicalSource) != "task.json" {
		return bundle, false, fmt.Errorf("任务 %s 的源路径无效: %s", existing.Key, existing.SourcePath)
	}
	path := filepath.Join(root, filepath.FromSlash(logicalSource))
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return bundle, false, nil
	} else if err != nil {
		return bundle, false, err
	}

	relative := strings.TrimPrefix(logicalSource, taskPrefix)
	parts := splitPath(relative)
	archived := len(parts) > 0 && parts[0] == "archive"
	archiveMonth := ""
	if archived {
		if len(parts) < 3 || !archiveMonthPattern.MatchString(parts[1]) {
			return bundle, false, fmt.Errorf("任务 %s 的归档路径无效: %s", existing.Key, logicalSource)
		}
		archiveMonth = parts[1]
	}
	file := taskFile{
		path: path, relativePath: filepath.FromSlash(relative),
		archived: archived, archiveMonth: archiveMonth,
	}
	task, bytesRead, err := parseTask(root, project.ID, file, logicalSource)
	if err != nil {
		return bundle, false, err
	}
	task.Key = existing.Key
	budget := &scanBudget{}
	if err := budget.addRead(bytesRead, "task.json"); err != nil {
		return bundle, false, err
	}
	item := scannedTask{task: task, directory: filepath.Dir(path), logicalDir: filepath.ToSlash(filepath.Dir(logicalSource))}
	artifacts, err := scanArtifacts(ctx, root, project.ID, item, budget)
	if err != nil {
		return bundle, false, err
	}
	entries, err := scanContextManifests(ctx, root, project.ID, item, budget)
	if err != nil {
		return bundle, false, err
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Path < artifacts[j].Path })
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Action != entries[j].Action {
			return entries[i].Action < entries[j].Action
		}
		return entries[i].Line < entries[j].Line
	})
	task.ArtifactCount = len(artifacts)
	for _, entry := range entries {
		if !entry.Valid {
			task.ContextIssues++
		}
	}
	// 活动 Session 未变化时沿用当前派生状态；无活动 Session 时按新的事实状态重新计算。
	if existing.ActiveSessions > 0 {
		task.ActiveSessions = existing.ActiveSessions
		task.RuntimePhase = existing.RuntimePhase
	}
	task.IndexHash = taskIndexHash(task, artifacts, entries)
	bundle = model.TaskBundle{Task: task, Artifacts: artifacts, ContextEntries: entries, Stats: budget.stats()}
	return bundle, true, nil
}

// ScanSessions 只读取 .runtime/sessions，并重新计算任务的 Session 派生状态。
func (s *Scanner) ScanSessions(ctx context.Context, project model.Project, tasks []model.Task) (model.SessionIndexSnapshot, error) {
	result := model.SessionIndexSnapshot{Sessions: make([]model.Session, 0), TaskState: make(map[string]model.TaskRuntimeState, len(tasks))}
	root, err := ValidateRoot(project.Root)
	if err != nil {
		return result, err
	}
	trellisRoot, err := resolveExistingPath(root, filepath.Join(root, ".trellis"))
	if err != nil {
		return result, err
	}
	taskByDirectory := make(map[string]string, len(tasks))
	for _, task := range tasks {
		logicalDir := filepath.Dir(task.SourcePath)
		taskByDirectory[filepath.Clean(filepath.Join(root, filepath.FromSlash(logicalDir)))] = task.Key
		result.TaskState[task.Key] = model.TaskRuntimeState{RuntimePhase: deriveRuntimePhase(task.Status, task.Archived)}
	}
	budget := &scanBudget{}
	scanned, err := scanSessions(ctx, root, trellisRoot, project.ID, taskByDirectory, budget)
	if err != nil {
		return result, err
	}
	latest := make(map[string]int)
	for index, item := range scanned {
		result.Sessions = append(result.Sessions, item.session)
		if item.session.TaskKey == "" || item.session.Stale {
			continue
		}
		state := result.TaskState[item.session.TaskKey]
		state.ActiveSessions++
		result.TaskState[item.session.TaskKey] = state
		current, selected := latest[item.session.TaskKey]
		if !selected || newerSession(item.session, scanned[current].session) {
			latest[item.session.TaskKey] = index
		}
	}
	for taskKey, index := range latest {
		if phase := scanned[index].runPhase; phase != "" {
			state := result.TaskState[taskKey]
			state.RuntimePhase = phase
			result.TaskState[taskKey] = state
		}
	}
	result.Hash = hashSessions(scanned)
	result.Stats = budget.stats()
	return result, nil
}

// ScanWorkflowStates 只读取 workflow.md。
func (s *Scanner) ScanWorkflowStates(ctx context.Context, project model.Project) ([]model.WorkflowState, model.ScanStats, error) {
	root, err := ValidateRoot(project.Root)
	if err != nil {
		return nil, model.ScanStats{}, err
	}
	trellisRoot, err := resolveExistingPath(root, filepath.Join(root, ".trellis"))
	if err != nil {
		return nil, model.ScanStats{}, err
	}
	budget := &scanBudget{}
	states, err := scanWorkflowStates(ctx, root, trellisRoot, project.ID, budget)
	return states, budget.stats(), err
}

// ScanSpecsHash 只遍历规范目录并返回内容指纹。
func (s *Scanner) ScanSpecsHash(ctx context.Context, project model.Project) (string, model.ScanStats, error) {
	root, err := ValidateRoot(project.Root)
	if err != nil {
		return "", model.ScanStats{}, err
	}
	trellisRoot, err := resolveExistingPath(root, filepath.Join(root, ".trellis"))
	if err != nil {
		return "", model.ScanStats{}, err
	}
	budget := &scanBudget{}
	hash, err := hashSpecs(ctx, root, trellisRoot, budget)
	return hash, budget.stats(), err
}
