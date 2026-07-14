package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/yunnnn/trellis-dash/internal/model"
)

const taskSelect = `
	SELECT project_id, task_key, task_id, directory_name, name, title, description,
	       status, runtime_phase, dev_type, scope, package_name, priority, creator,
	       assignee, created_at, completed_at, branch, base_branch, worktree_path,
	       commit_hash, pr_url, subtasks_json, children_json, parent_id,
	       related_files_json, notes, meta_json, archived, archive_month, source_path,
	       source_hash, index_hash, modified_at, artifact_count, context_issues, active_sessions
	FROM tasks`

func (s *Store) ListTasks(ctx context.Context, projectID string, filter model.TaskFilter) (model.TaskPage, error) {
	// 所有视图都使用服务端分页；单页最多 200 条，防止调用方重新制造大响应。
	if filter.Limit <= 0 {
		filter.Limit = 100
	} else if filter.Limit > 200 {
		filter.Limit = 200
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}

	where := []string{"project_id = ?"}
	args := []any{projectID}
	if filter.Archived != nil {
		where = append(where, "archived = ?")
		args = append(args, *filter.Archived)
	}
	if filter.Status != "" {
		where = append(where, "status = ?")
		args = append(args, filter.Status)
	}
	if filter.Priority != "" {
		where = append(where, "priority = ?")
		args = append(args, filter.Priority)
	}
	if filter.Assignee != "" {
		where = append(where, "assignee = ?")
		args = append(args, filter.Assignee)
	}
	if filter.Query != "" {
		where = append(where, `(lower(title) LIKE ? OR lower(description) LIKE ? OR lower(task_id) LIKE ? OR lower(branch) LIKE ?)`)
		needle := "%" + strings.ToLower(filter.Query) + "%"
		args = append(args, needle, needle, needle, needle)
	}
	whereSQL := strings.Join(where, " AND ")

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return model.TaskPage{}, err
	}
	queryArgs := append(append([]any{}, args...), filter.Limit, filter.Offset)
	rows, err := s.db.QueryContext(ctx, taskSelect+` WHERE `+whereSQL+`
		ORDER BY archived ASC,
		CASE priority WHEN 'P0' THEN 0 WHEN 'P1' THEN 1 WHEN 'P2' THEN 2 WHEN 'P3' THEN 3 ELSE 9 END,
		modified_at DESC, lower(title)
		LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return model.TaskPage{}, err
	}
	defer rows.Close()
	items := make([]model.Task, 0)
	for rows.Next() {
		item, err := scanTask(rows)
		if err != nil {
			return model.TaskPage{}, err
		}
		items = append(items, item)
	}
	return model.TaskPage{Items: items, Total: total, Limit: filter.Limit, Offset: filter.Offset}, rows.Err()
}

func (s *Store) GetTask(ctx context.Context, projectID, taskKey string) (model.Task, error) {
	item, err := scanTask(s.db.QueryRowContext(ctx, taskSelect+` WHERE project_id = ? AND task_key = ?`, projectID, taskKey))
	if errors.Is(err, sql.ErrNoRows) {
		return model.Task{}, ErrNotFound
	}
	return item, err
}

func scanTask(scanner rowScanner) (model.Task, error) {
	var item model.Task
	var devType, scope, packageName, completedAt, branch, baseBranch sql.NullString
	var worktreePath, commit, prURL, parent sql.NullString
	var subtasks, children, related, meta string
	var modified string
	if err := scanner.Scan(
		&item.ProjectID, &item.Key, &item.ID, &item.Directory, &item.Name,
		&item.Title, &item.Description, &item.Status, &item.RuntimePhase,
		&devType, &scope, &packageName, &item.Priority, &item.Creator, &item.Assignee,
		&item.CreatedAt, &completedAt, &branch, &baseBranch, &worktreePath, &commit, &prURL,
		&subtasks, &children, &parent, &related, &item.Notes, &meta,
		&item.Archived, &item.ArchiveMonth, &item.SourcePath, &item.SourceHash, &item.IndexHash,
		&modified, &item.ArtifactCount, &item.ContextIssues, &item.ActiveSessions,
	); err != nil {
		return item, err
	}
	item.DevType = nullStringPtr(devType)
	item.Scope = nullStringPtr(scope)
	item.Package = nullStringPtr(packageName)
	item.CompletedAt = nullStringPtr(completedAt)
	item.Branch = nullStringPtr(branch)
	item.BaseBranch = nullStringPtr(baseBranch)
	item.WorktreePath = nullStringPtr(worktreePath)
	item.Commit = nullStringPtr(commit)
	item.PRURL = nullStringPtr(prURL)
	item.Parent = nullStringPtr(parent)
	item.Subtasks = json.RawMessage(subtasks)
	item.Children = json.RawMessage(children)
	item.RelatedFiles = json.RawMessage(related)
	item.Meta = json.RawMessage(meta)
	item.ModifiedAt, _ = parseTime(modified)
	return item, nil
}

// ListAllTasksForIndex 返回索引器所需的全部任务，不受页面分页上限影响。
func (s *Store) ListAllTasksForIndex(ctx context.Context, projectID string) ([]model.Task, error) {
	rows, err := s.db.QueryContext(ctx, taskSelect+` WHERE project_id = ? ORDER BY source_path`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.Task, 0)
	for rows.Next() {
		item, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ListArtifacts(ctx context.Context, projectID, taskKey string) ([]model.Artifact, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT project_id, task_key, kind, name, path, content_type, content, size, hash, modified_at
		FROM task_artifacts WHERE project_id = ? AND task_key = ?
		ORDER BY CASE kind WHEN 'prd' THEN 0 WHEN 'design' THEN 1 WHEN 'implementation' THEN 2 ELSE 3 END, path`,
		projectID, taskKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.Artifact, 0)
	for rows.Next() {
		var item model.Artifact
		var modified string
		if err := rows.Scan(&item.ProjectID, &item.TaskKey, &item.Kind, &item.Name, &item.Path,
			&item.ContentType, &item.Content, &item.Size, &item.Hash, &modified); err != nil {
			return nil, err
		}
		item.ModifiedAt, _ = parseTime(modified)
		items = append(items, item)
	}
	return items, rows.Err()
}

// ListArtifactSummaries 不读取正文，任务详情首屏只返回文档元数据。
func (s *Store) ListArtifactSummaries(ctx context.Context, projectID, taskKey string) ([]model.Artifact, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT project_id, task_key, kind, name, path, content_type, size, hash, modified_at
		FROM task_artifacts WHERE project_id = ? AND task_key = ?
		ORDER BY CASE kind WHEN 'prd' THEN 0 WHEN 'design' THEN 1 WHEN 'implementation' THEN 2 ELSE 3 END, path`,
		projectID, taskKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.Artifact, 0)
	for rows.Next() {
		var item model.Artifact
		var modified string
		if err := rows.Scan(&item.ProjectID, &item.TaskKey, &item.Kind, &item.Name,
			&item.Path, &item.ContentType, &item.Size, &item.Hash, &modified); err != nil {
			return nil, err
		}
		item.ModifiedAt, _ = parseTime(modified)
		items = append(items, item)
	}
	return items, rows.Err()
}

// GetArtifact 按逻辑路径读取一份文档正文，供激活标签页时延迟加载。
func (s *Store) GetArtifact(ctx context.Context, projectID, taskKey, artifactPath string) (model.Artifact, error) {
	var item model.Artifact
	var modified string
	err := s.db.QueryRowContext(ctx, `
		SELECT project_id, task_key, kind, name, path, content_type, content, size, hash, modified_at
		FROM task_artifacts WHERE project_id = ? AND task_key = ? AND path = ?`,
		projectID, taskKey, artifactPath).Scan(
		&item.ProjectID, &item.TaskKey, &item.Kind, &item.Name, &item.Path,
		&item.ContentType, &item.Content, &item.Size, &item.Hash, &modified,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return item, ErrNotFound
	}
	if err != nil {
		return item, err
	}
	item.ModifiedAt, _ = parseTime(modified)
	return item, nil
}

func (s *Store) ListContext(ctx context.Context, projectID, taskKey string) ([]model.ContextEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT project_id, task_key, action, line_no, file_path, reason,
		       entry_type, is_example, is_duplicate, is_valid, file_exists, error
		FROM task_context_entries WHERE project_id = ? AND task_key = ?
		ORDER BY action, line_no`, projectID, taskKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.ContextEntry, 0)
	for rows.Next() {
		var item model.ContextEntry
		if err := rows.Scan(&item.ProjectID, &item.TaskKey, &item.Action, &item.Line,
			&item.File, &item.Reason, &item.Type, &item.Example, &item.Duplicate,
			&item.Valid, &item.Exists, &item.Error); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// ListAllContextForIndex 返回轻量重新校验所需的全部 Context 行。
func (s *Store) ListAllContextForIndex(ctx context.Context, projectID string) ([]model.ContextEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT project_id, task_key, action, line_no, file_path, reason,
		       entry_type, is_example, is_duplicate, is_valid, file_exists, error
		FROM task_context_entries WHERE project_id = ?
		ORDER BY task_key, action, line_no`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.ContextEntry, 0)
	for rows.Next() {
		var item model.ContextEntry
		if err := rows.Scan(&item.ProjectID, &item.TaskKey, &item.Action, &item.Line,
			&item.File, &item.Reason, &item.Type, &item.Example, &item.Duplicate,
			&item.Valid, &item.Exists, &item.Error); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ListSessions(ctx context.Context, projectID string) ([]model.Session, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT project_id, session_key, platform, current_task, task_key,
		       last_seen_at, current_run_json, stale, source_path, source_hash
		FROM runtime_sessions WHERE project_id = ?
		ORDER BY last_seen_at DESC, session_key`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.Session, 0)
	for rows.Next() {
		var item model.Session
		var lastSeen sql.NullString
		var currentRun string
		if err := rows.Scan(&item.ProjectID, &item.Key, &item.Platform, &item.CurrentTask,
			&item.TaskKey, &lastSeen, &currentRun, &item.Stale, &item.SourcePath, &item.SourceHash); err != nil {
			return nil, err
		}
		if lastSeen.Valid {
			value, err := parseTime(lastSeen.String)
			if err == nil {
				item.LastSeenAt = &value
			}
		}
		item.CurrentRun = json.RawMessage(currentRun)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ListWorkflowStates(ctx context.Context, projectID string) ([]model.WorkflowState, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT project_id, name, label, sort_order FROM workflow_states
		WHERE project_id = ? ORDER BY sort_order, name`, projectID)
	if err != nil {
		return nil, err
	}
	items := make([]model.WorkflowState, 0)
	known := make(map[string]struct{})
	for rows.Next() {
		var item model.WorkflowState
		if err := rows.Scan(&item.ProjectID, &item.Name, &item.Label, &item.Order); err != nil {
			return nil, err
		}
		items = append(items, item)
		known[item.Name] = struct{}{}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// workflow.md 可能暂时没有收录自定义状态；分页看板仍必须能访问这些任务。
	statusRows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT status FROM tasks
		WHERE project_id = ? AND archived = 0 AND status <> '' ORDER BY status`, projectID)
	if err != nil {
		return nil, err
	}
	defer statusRows.Close()
	order := len(items)
	for statusRows.Next() {
		var status string
		if err := statusRows.Scan(&status); err != nil {
			return nil, err
		}
		if _, exists := known[status]; exists {
			continue
		}
		items = append(items, model.WorkflowState{ProjectID: projectID, Name: status, Order: order})
		order++
	}
	return items, statusRows.Err()
}

func (s *Store) ListActivity(ctx context.Context, projectID string, afterID, beforeID int64, limit int) (model.ActivityPage, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := `
		SELECT id, project_id, task_key, event_type, source, payload_json, created_at
		FROM activity_events WHERE project_id = ? AND id > ?
		ORDER BY id ASC LIMIT ?`
	queryID := afterID
	descending := false
	if beforeID > 0 {
		query = `
			SELECT id, project_id, task_key, event_type, source, payload_json, created_at
			FROM activity_events WHERE project_id = ? AND id < ?
			ORDER BY id DESC LIMIT ?`
		queryID = beforeID
		descending = true
	} else if afterID <= 0 {
		// 首次进入直接取得最新一页；后续带 lastId 时再严格按 ID 增量读取。
		query = `
			SELECT id, project_id, task_key, event_type, source, payload_json, created_at
			FROM activity_events WHERE project_id = ? AND id > ?
			ORDER BY id DESC LIMIT ?`
		queryID = 0
		descending = true
	}
	rows, err := s.db.QueryContext(ctx, query, projectID, queryID, limit+1)
	if err != nil {
		return model.ActivityPage{}, err
	}
	defer rows.Close()
	items := make([]model.ActivityEvent, 0, limit)
	for rows.Next() {
		var item model.ActivityEvent
		var payload, created string
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.TaskKey, &item.Type,
			&item.Source, &payload, &created); err != nil {
			return model.ActivityPage{}, err
		}
		item.Payload = json.RawMessage(payload)
		item.CreatedAt, _ = parseTime(created)
		items = append(items, item)
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	if descending {
		for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
			items[left], items[right] = items[right], items[left]
		}
	}
	var firstID, lastID int64
	if len(items) > 0 {
		firstID = items[0].ID
		lastID = items[len(items)-1].ID
	}
	return model.ActivityPage{Items: items, FirstID: firstID, LastID: lastID, HasMore: hasMore}, rows.Err()
}

func (s *Store) RecentActivity(ctx context.Context, projectID string, limit int) ([]model.ActivityEvent, error) {
	return s.recentActivity(ctx, projectID, "", false, limit)
}

// RecentTaskActivity 精确查询单个任务的最近活动，避免先截取项目活动再在内存过滤。
func (s *Store) RecentTaskActivity(ctx context.Context, projectID, taskKey string, limit int) ([]model.ActivityEvent, error) {
	return s.recentActivity(ctx, projectID, taskKey, true, limit)
}

func (s *Store) recentActivity(ctx context.Context, projectID, taskKey string, filterTask bool, limit int) ([]model.ActivityEvent, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	query := `
		SELECT id, project_id, task_key, event_type, source, payload_json, created_at
		FROM activity_events WHERE project_id = ? ORDER BY id DESC LIMIT ?`
	args := []any{projectID, limit}
	if filterTask {
		query = `
			SELECT id, project_id, task_key, event_type, source, payload_json, created_at
			FROM activity_events WHERE project_id = ? AND task_key = ? ORDER BY id DESC LIMIT ?`
		args = []any{projectID, taskKey, limit}
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.ActivityEvent, 0, limit)
	for rows.Next() {
		var item model.ActivityEvent
		var payload, created string
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.TaskKey, &item.Type,
			&item.Source, &payload, &created); err != nil {
			return nil, err
		}
		item.Payload = json.RawMessage(payload)
		item.CreatedAt, _ = parseTime(created)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ReplaceGitSnapshot(ctx context.Context, snapshot model.GitSnapshot) (bool, error) {
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return false, err
	}
	summaryPayload, err := json.Marshal(snapshot.Summary())
	if err != nil {
		return false, err
	}
	unlockProject := s.lockProject(snapshot.ProjectID)
	defer unlockProject()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	changed, err := resourceChanged(ctx, tx, snapshot.ProjectID, model.ResourceGit, snapshot.Hash)
	if err != nil {
		return false, err
	}
	if !changed {
		// 旧数据库补列后的默认值是 {}。即使 Git Hash 没变化，也要轻量回填摘要；
		// 这属于缓存格式升级，不提升 Git 或 Activity revision。
		if _, err := tx.ExecContext(ctx, `
			UPDATE git_snapshots SET summary_json = ?
			WHERE project_id = ? AND (summary_json = '' OR summary_json = '{}')`,
			string(summaryPayload), snapshot.ProjectID); err != nil {
			return false, err
		}
		return false, tx.Commit()
	}
	now := snapshot.UpdatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO git_snapshots(project_id, snapshot_json, summary_json, content_hash, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(project_id) DO UPDATE SET snapshot_json = excluded.snapshot_json,
			summary_json = excluded.summary_json, content_hash = excluded.content_hash,
			updated_at = excluded.updated_at`,
		snapshot.ProjectID, string(payload), string(summaryPayload), snapshot.Hash, formatTime(now)); err != nil {
		return false, err
	}
	if err := setResourceState(ctx, tx, snapshot.ProjectID, model.ResourceGit, snapshot.Hash, now); err != nil {
		return false, err
	}
	eventPayload, _ := json.Marshal(map[string]any{
		"branch": snapshot.Branch, "dirty": snapshot.Dirty, "files": len(snapshot.Files),
	})
	if err := insertActivity(ctx, tx, snapshot.ProjectID, "", "git.updated", "git", eventPayload, now); err != nil {
		return false, err
	}
	if err := pruneActivity(ctx, tx, snapshot.ProjectID); err != nil {
		return false, err
	}
	if err := incrementRevision(ctx, tx, snapshot.ProjectID, model.ResourceActivity, now); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// GetGitSummary 只读取概览所需字段，避免反序列化完整文件列表。
func (s *Store) GetGitSummary(ctx context.Context, projectID string) (*model.GitSummary, error) {
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT summary_json FROM git_snapshots WHERE project_id = ?`, projectID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var summary model.GitSummary
	if err := json.Unmarshal([]byte(payload), &summary); err != nil {
		return nil, fmt.Errorf("解析 Git 摘要: %w", err)
	}
	if summary.ProjectID == "" {
		// 旧缓存没有 summary_json 时，从完整快照兼容恢复；下一次 Git 刷新会正式回填。
		snapshot, err := s.GetGitSnapshot(ctx, projectID)
		if err != nil || snapshot == nil {
			return nil, err
		}
		value := snapshot.Summary()
		return &value, nil
	}
	return &summary, nil
}

func (s *Store) GetGitSnapshot(ctx context.Context, projectID string) (*model.GitSnapshot, error) {
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT snapshot_json FROM git_snapshots WHERE project_id = ?`, projectID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var snapshot model.GitSnapshot
	if err := json.Unmarshal([]byte(payload), &snapshot); err != nil {
		return nil, fmt.Errorf("解析 Git 快照: %w", err)
	}
	if err := s.mapWorktreeTasks(ctx, projectID, &snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

type worktreeTaskCandidate struct {
	key          string
	branch       string
	worktreePath string
}

// mapWorktreeTasks 只丰富返回对象，不回写 git_snapshots，也不参与 Git hash。
func (s *Store) mapWorktreeTasks(ctx context.Context, projectID string, snapshot *model.GitSnapshot) error {
	if len(snapshot.Worktrees) == 0 {
		return nil
	}
	var projectRoot string
	if err := s.db.QueryRowContext(ctx, `SELECT root FROM projects WHERE id = ?`, projectID).Scan(&projectRoot); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("查询 Worktree 项目根目录: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT task_key, COALESCE(branch, ''), COALESCE(worktree_path, '')
		FROM tasks WHERE project_id = ? AND archived = 0
		ORDER BY task_key`, projectID)
	if err != nil {
		return fmt.Errorf("查询 Worktree 关联任务: %w", err)
	}
	candidates := make([]worktreeTaskCandidate, 0)
	for rows.Next() {
		var candidate worktreeTaskCandidate
		if err := rows.Scan(&candidate.key, &candidate.branch, &candidate.worktreePath); err != nil {
			rows.Close()
			return err
		}
		candidate.branch = normalizeTaskBranch(candidate.branch)
		candidates = append(candidates, candidate)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}

	pathTasks := make(map[string]string)
	branchTasks := make(map[string][]string)
	for _, candidate := range candidates {
		if candidate.worktreePath != "" {
			if normalized, err := normalizeTaskWorktreePath(candidate.worktreePath, projectRoot); err == nil {
				// 查询按 task_key 排序；异常重复路径时保留第一个，保证输出稳定。
				if _, exists := pathTasks[normalized]; !exists {
					pathTasks[normalized] = candidate.key
				}
			}
		}
		if candidate.branch != "" {
			branchTasks[candidate.branch] = append(branchTasks[candidate.branch], candidate.key)
		}
	}

	for index := range snapshot.Worktrees {
		worktree := &snapshot.Worktrees[index]
		// 持久化 JSON 中即使存在旧映射，也以当前任务读模型重新计算。
		worktree.TaskKey = ""
		if normalized, err := normalizeTaskWorktreePath(worktree.Path, projectRoot); err == nil {
			if taskKey := pathTasks[normalized]; taskKey != "" {
				worktree.TaskKey = taskKey
				continue
			}
		}
		branch := normalizeTaskBranch(worktree.Branch)
		if matches := branchTasks[branch]; branch != "" && len(matches) == 1 {
			worktree.TaskKey = matches[0]
		}
	}
	return nil
}

func normalizeTaskWorktreePath(path, projectRoot string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(projectRoot, path)
	}
	return canonicalPath(path)
}

func normalizeTaskBranch(branch string) string {
	return strings.TrimPrefix(strings.TrimSpace(branch), "refs/heads/")
}

func (s *Store) TaskStatistics(ctx context.Context, projectID string) (model.TaskStatistics, error) {
	stats := model.TaskStatistics{ByStatus: make(map[string]int)}
	rows, err := s.db.QueryContext(ctx, `
		SELECT status, runtime_phase, archived, COUNT(*) FROM tasks WHERE project_id = ?
		GROUP BY status, runtime_phase, archived`, projectID)
	if err != nil {
		return stats, err
	}
	defer rows.Close()
	for rows.Next() {
		var status, runtimePhase string
		var archived bool
		var count int
		if err := rows.Scan(&status, &runtimePhase, &archived, &count); err != nil {
			return stats, err
		}
		stats.Total += count
		if archived {
			stats.Archived += count
		} else {
			stats.Active += count
			stats.ByStatus[status] += count
			if status == "blocked" || runtimePhase == "blocked" {
				stats.Blocked += count
			}
		}
	}
	// 日期以数据库中的 ISO 日期前缀比较，避免受本机时区格式影响。
	today := time.Now().Format("2006-01-02")
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM tasks WHERE project_id = ? AND completed_at LIKE ?`,
		projectID, today+"%").Scan(&stats.CompletedToday); err != nil {
		return stats, err
	}
	return stats, rows.Err()
}

// TaskCompletionCounts 按 completed_at 中保存的自然日聚合完成数量。
// 使用日期前缀而不做时区换算，保证任务归档或后续状态变化不会改写历史归属日。
func (s *Store) TaskCompletionCounts(ctx context.Context, projectID, startDate, endDate string) ([]model.DailyCompletion, error) {
	start, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		return nil, fmt.Errorf("解析完成趋势开始日期: %w", err)
	}
	end, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		return nil, fmt.Errorf("解析完成趋势结束日期: %w", err)
	}
	if start.After(end) {
		return []model.DailyCompletion{}, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT completed_day, COUNT(*)
		FROM (
			SELECT substr(completed_at, 1, 10) AS completed_day
			FROM tasks
			WHERE project_id = ? AND completed_at IS NOT NULL
		)
		WHERE completed_day BETWEEN ? AND ?
			AND completed_day GLOB '????-??-??'
			AND date(completed_day) = completed_day
		GROUP BY completed_day
		ORDER BY completed_day`, projectID, startDate, endDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var item model.DailyCompletion
		if err := rows.Scan(&item.Date, &item.Count); err != nil {
			return nil, err
		}
		counts[item.Date] = item.Count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 返回完整日期序列，让前端在没有完成记录时也能稳定绘制零值折线。
	items := make([]model.DailyCompletion, 0, int(end.Sub(start).Hours()/24)+1)
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		date := day.Format("2006-01-02")
		items = append(items, model.DailyCompletion{Date: date, Count: counts[date]})
	}
	return items, nil
}

func (s *Store) Dashboard(ctx context.Context, projectID string) (model.DashboardSnapshot, error) {
	project, err := s.GetProject(ctx, projectID)
	if err != nil {
		return model.DashboardSnapshot{}, err
	}
	archived := false
	page, err := s.ListTasks(ctx, projectID, model.TaskFilter{Archived: &archived, Limit: 20})
	if err != nil {
		return model.DashboardSnapshot{}, err
	}
	stats, err := s.TaskStatistics(ctx, projectID)
	if err != nil {
		return model.DashboardSnapshot{}, err
	}
	now := time.Now()
	today := now.Format("2006-01-02")
	// 首页趋势固定包含今天及此前 89 个自然日，共 90 天。
	completionTrend, err := s.TaskCompletionCounts(ctx, projectID, now.AddDate(0, 0, -89).Format("2006-01-02"), today)
	if err != nil {
		return model.DashboardSnapshot{}, err
	}
	sessions, err := s.ListSessions(ctx, projectID)
	if err != nil {
		return model.DashboardSnapshot{}, err
	}
	activeSessions := make([]model.Session, 0, len(sessions))
	for _, session := range sessions {
		if !session.Stale && session.TaskKey != "" {
			activeSessions = append(activeSessions, session)
		}
	}
	git, err := s.GetGitSummary(ctx, projectID)
	if err != nil {
		return model.DashboardSnapshot{}, err
	}
	activity, err := s.RecentActivity(ctx, projectID, 20)
	if err != nil {
		return model.DashboardSnapshot{}, err
	}
	return model.DashboardSnapshot{
		Project: project, Statistics: stats, CompletionTrend: completionTrend,
		ActiveTasks: page.Items, Sessions: activeSessions, Git: git, RecentActivity: activity,
	}, nil
}

func nullStringPtr(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	copy := value.String
	return &copy
}
