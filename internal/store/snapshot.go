package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yunnnn/trellis-dash/internal/model"
)

type previousTask struct {
	Hash      string
	IndexHash string
	Status    string
	Archived  bool
}

const (
	maxActivityTitleRunes   = 2_048
	maxActivityPayloadBytes = 64 * 1024
	maxActivityPreviewBytes = 8 * 1024
	maxActivityPerProject   = 5_000
)

// ReplaceTrellisSnapshot 只在内容哈希变化时替换对应读模型并提升 revision。
func (s *Store) ReplaceTrellisSnapshot(ctx context.Context, projectID string, snapshot model.TrellisSnapshot) (bool, error) {
	unlockProject := s.lockProject(projectID)
	defer unlockProject()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	changed := false
	activityChanged := false
	var previousIndexError string
	if err := tx.QueryRowContext(ctx, `SELECT index_error FROM projects WHERE id = ?`, projectID).Scan(&previousIndexError); err != nil {
		return false, err
	}

	tasksChanged, err := resourceChanged(ctx, tx, projectID, model.ResourceTasks, snapshot.TasksHash)
	if err != nil {
		return false, err
	}
	if tasksChanged {
		previous, err := loadPreviousTasks(ctx, tx, projectID)
		if err != nil {
			return false, err
		}
		artifactsByTask, contextByTask := groupTaskResources(snapshot)
		taskFactsChanged := false
		taskCacheChanged := false
		for _, task := range snapshot.Tasks {
			old, existed := previous[task.Key]
			if existed && old.IndexHash != "" && old.IndexHash == task.IndexHash {
				delete(previous, task.Key)
				continue
			}
			if err := insertTask(ctx, tx, task); err != nil {
				return false, err
			}
			if err := replaceTaskResources(ctx, tx, projectID, task.Key, artifactsByTask[task.Key], contextByTask[task.Key]); err != nil {
				return false, err
			}
			taskCacheChanged = true
			eventType := ""
			switch {
			case !existed:
				eventType = "task.created"
			case !old.Archived && task.Archived:
				eventType = "task.archived"
			case old.Hash != task.SourceHash || old.Status != task.Status:
				eventType = "task.updated"
			}
			if eventType != "" {
				taskFactsChanged = true
				payload, _ := json.Marshal(map[string]any{
					"title": truncateRunes(task.Title, maxActivityTitleRunes), "status": task.Status, "archived": task.Archived,
				})
				if err := insertActivity(ctx, tx, projectID, task.Key, eventType, "filesystem", payload, now); err != nil {
					return false, err
				}
				activityChanged = true
			}
			delete(previous, task.Key)
		}
		for taskKey := range previous {
			if _, err := tx.ExecContext(ctx, `DELETE FROM tasks WHERE project_id = ? AND task_key = ?`, projectID, taskKey); err != nil {
				return false, err
			}
			taskCacheChanged = true
			taskFactsChanged = true
			payload, _ := json.Marshal(map[string]any{"taskKey": taskKey})
			if err := insertActivity(ctx, tx, projectID, taskKey, "task.deleted", "filesystem", payload, now); err != nil {
				return false, err
			}
			activityChanged = true
		}
		workflowChanged, err := replaceWorkflowStates(ctx, tx, projectID, snapshot.WorkflowStates)
		if err != nil {
			return false, err
		}
		taskCacheChanged = taskCacheChanged || workflowChanged
		if taskCacheChanged && !taskFactsChanged {
			payload, _ := json.Marshal(map[string]any{
				"artifacts": len(snapshot.Artifacts), "contextEntries": len(snapshot.ContextEntries), "workflowStates": len(snapshot.WorkflowStates),
			})
			if err := insertActivity(ctx, tx, projectID, "", "trellis.resources.updated", "filesystem", payload, now); err != nil {
				return false, err
			}
			activityChanged = true
		}
		if err := storeResourceHash(ctx, tx, projectID, model.ResourceTasks, snapshot.TasksHash); err != nil {
			return false, err
		}
		if taskCacheChanged {
			if err := incrementRevision(ctx, tx, projectID, model.ResourceTasks, now); err != nil {
				return false, err
			}
			changed = true
		}
	}

	sessionsChanged, err := resourceChanged(ctx, tx, projectID, model.ResourceSessions, snapshot.SessionsHash)
	if err != nil {
		return false, err
	}
	if sessionsChanged {
		runtimeState := make(map[string]model.TaskRuntimeState, len(snapshot.Tasks))
		for _, task := range snapshot.Tasks {
			runtimeState[task.Key] = model.TaskRuntimeState{RuntimePhase: task.RuntimePhase, ActiveSessions: task.ActiveSessions}
		}
		sessionCacheChanged, taskRuntimeChanged, err := reconcileSessions(ctx, tx, projectID, snapshot.Sessions, runtimeState)
		if err != nil {
			return false, err
		}
		if err := storeResourceHash(ctx, tx, projectID, model.ResourceSessions, snapshot.SessionsHash); err != nil {
			return false, err
		}
		if sessionCacheChanged || taskRuntimeChanged {
			if err := incrementRevision(ctx, tx, projectID, model.ResourceSessions, now); err != nil {
				return false, err
			}
			payload, _ := json.Marshal(map[string]any{"count": len(snapshot.Sessions)})
			if err := insertActivity(ctx, tx, projectID, "", "sessions.updated", "filesystem", payload, now); err != nil {
				return false, err
			}
			activityChanged = true
			changed = true
		}
	}

	if snapshot.SpecsHash != "" {
		specsChanged, err := resourceChanged(ctx, tx, projectID, model.ResourceSpecs, snapshot.SpecsHash)
		if err != nil {
			return false, err
		}
		if specsChanged {
			if err := setResourceState(ctx, tx, projectID, model.ResourceSpecs, snapshot.SpecsHash, now); err != nil {
				return false, err
			}
			changed = true
		}
	}

	if previousIndexError != "" {
		payload, _ := json.Marshal(map[string]string{"previousError": previousIndexError})
		if err := insertActivity(ctx, tx, projectID, "", "index.recovered", "indexer", payload, now); err != nil {
			return false, err
		}
		activityChanged = true
		changed = true
	}

	if activityChanged {
		if err := pruneActivity(ctx, tx, projectID); err != nil {
			return false, err
		}
		if err := incrementRevision(ctx, tx, projectID, model.ResourceActivity, now); err != nil {
			return false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE projects SET indexed_at = ?, index_error = '', updated_at = ? WHERE id = ?`,
		formatTime(now), formatTime(now), projectID); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return changed, nil
}

// ReplaceTaskBundle 原子替换一个任务缓存单元，避免小改动重写整个项目。
func (s *Store) ReplaceTaskBundle(ctx context.Context, projectID string, bundle model.TaskBundle) (bool, error) {
	unlockProject := s.lockProject(projectID)
	defer unlockProject()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var previous previousTask
	err = tx.QueryRowContext(ctx, `
		SELECT source_hash, index_hash, status, archived FROM tasks
		WHERE project_id = ? AND task_key = ?`, projectID, bundle.Task.Key).
		Scan(&previous.Hash, &previous.IndexHash, &previous.Status, &previous.Archived)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	if previous.IndexHash != "" && previous.IndexHash == bundle.Task.IndexHash {
		return false, tx.Commit()
	}
	if err := insertTask(ctx, tx, bundle.Task); err != nil {
		return false, err
	}
	if err := replaceTaskResources(ctx, tx, projectID, bundle.Task.Key, bundle.Artifacts, bundle.ContextEntries); err != nil {
		return false, err
	}
	now := time.Now().UTC()
	if err := touchResourceState(ctx, tx, projectID, model.ResourceTasks, bundle.Task.IndexHash, now); err != nil {
		return false, err
	}
	eventType := "trellis.resources.updated"
	if previous.Hash != bundle.Task.SourceHash || previous.Status != bundle.Task.Status || previous.Archived != bundle.Task.Archived {
		eventType = "task.updated"
	}
	payload, _ := json.Marshal(map[string]any{
		"title":  truncateRunes(bundle.Task.Title, maxActivityTitleRunes),
		"status": bundle.Task.Status, "archived": bundle.Task.Archived,
	})
	if err := insertActivity(ctx, tx, projectID, bundle.Task.Key, eventType, "filesystem", payload, now); err != nil {
		return false, err
	}
	if err := finishIncrementalIndex(ctx, tx, projectID, now, true); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// DeleteTask 删除一个已经消失的任务；目录新增或重命名仍由全量校验补齐。
func (s *Store) DeleteTask(ctx context.Context, projectID, taskKey string) (bool, error) {
	unlockProject := s.lockProject(projectID)
	defer unlockProject()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM tasks WHERE project_id = ? AND task_key = ?`, projectID, taskKey)
	if err != nil {
		return false, err
	}
	deleted, _ := result.RowsAffected()
	if deleted == 0 {
		return false, tx.Commit()
	}
	now := time.Now().UTC()
	if err := touchResourceState(ctx, tx, projectID, model.ResourceTasks, "delete:"+taskKey, now); err != nil {
		return false, err
	}
	payload, _ := json.Marshal(map[string]any{"taskKey": taskKey})
	if err := insertActivity(ctx, tx, projectID, taskKey, "task.deleted", "filesystem", payload, now); err != nil {
		return false, err
	}
	if err := finishIncrementalIndex(ctx, tx, projectID, now, true); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// ReplaceSessionSnapshot 增量同步 Session，并只更新派生状态发生变化的任务。
func (s *Store) ReplaceSessionSnapshot(ctx context.Context, projectID string, snapshot model.SessionIndexSnapshot) (bool, error) {
	unlockProject := s.lockProject(projectID)
	defer unlockProject()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	changed, err := resourceChanged(ctx, tx, projectID, model.ResourceSessions, snapshot.Hash)
	if err != nil || !changed {
		if err != nil {
			return false, err
		}
		return false, tx.Commit()
	}
	sessionChanged, taskChanged, err := reconcileSessions(ctx, tx, projectID, snapshot.Sessions, snapshot.TaskState)
	if err != nil {
		return false, err
	}
	if err := storeResourceHash(ctx, tx, projectID, model.ResourceSessions, snapshot.Hash); err != nil {
		return false, err
	}
	if !sessionChanged && !taskChanged {
		return false, tx.Commit()
	}
	now := time.Now().UTC()
	if err := incrementRevision(ctx, tx, projectID, model.ResourceSessions, now); err != nil {
		return false, err
	}
	payload, _ := json.Marshal(map[string]any{"count": len(snapshot.Sessions)})
	if err := insertActivity(ctx, tx, projectID, "", "sessions.updated", "filesystem", payload, now); err != nil {
		return false, err
	}
	if err := finishIncrementalIndex(ctx, tx, projectID, now, true); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// ReplaceWorkflowStates 只更新工作流列定义。
func (s *Store) ReplaceWorkflowStates(ctx context.Context, projectID string, states []model.WorkflowState) (bool, error) {
	unlockProject := s.lockProject(projectID)
	defer unlockProject()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	changed, err := replaceWorkflowStates(ctx, tx, projectID, states)
	if err != nil || !changed {
		if err != nil {
			return false, err
		}
		return false, tx.Commit()
	}
	now := time.Now().UTC()
	if err := touchResourceState(ctx, tx, projectID, model.ResourceTasks, "workflow", now); err != nil {
		return false, err
	}
	payload, _ := json.Marshal(map[string]any{"workflowStates": len(states)})
	if err := insertActivity(ctx, tx, projectID, "", "trellis.resources.updated", "filesystem", payload, now); err != nil {
		return false, err
	}
	if err := finishIncrementalIndex(ctx, tx, projectID, now, true); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// ReplaceSpecsHash 提升规范资源版本，但不触碰任务缓存。
func (s *Store) ReplaceSpecsHash(ctx context.Context, projectID, hash string) (bool, error) {
	unlockProject := s.lockProject(projectID)
	defer unlockProject()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	changed, err := resourceChanged(ctx, tx, projectID, model.ResourceSpecs, hash)
	if err != nil || !changed {
		if err != nil {
			return false, err
		}
		return false, tx.Commit()
	}
	now := time.Now().UTC()
	if err := setResourceState(ctx, tx, projectID, model.ResourceSpecs, hash, now); err != nil {
		return false, err
	}
	if err := finishIncrementalIndex(ctx, tx, projectID, now, false); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// ReplaceContextValidity 只写入路径校验结果，Git 变化不再触发完整 Trellis 扫描。
func (s *Store) ReplaceContextValidity(ctx context.Context, projectID string, entries []model.ContextEntry) (bool, error) {
	unlockProject := s.lockProject(projectID)
	defer unlockProject()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	changed := false
	issues := make(map[string]int)
	changedTasks := make(map[string]struct{})
	for _, entry := range entries {
		if _, exists := issues[entry.TaskKey]; !exists {
			issues[entry.TaskKey] = 0
		}
		if !entry.Valid {
			issues[entry.TaskKey]++
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE task_context_entries
			SET entry_type = ?, is_duplicate = ?, is_valid = ?, file_exists = ?, error = ?
			WHERE project_id = ? AND task_key = ? AND action = ? AND line_no = ?
			  AND (entry_type <> ? OR is_duplicate <> ? OR is_valid <> ? OR file_exists <> ? OR error <> ?)`,
			entry.Type, entry.Duplicate, entry.Valid, entry.Exists, entry.Error,
			projectID, entry.TaskKey, entry.Action, entry.Line,
			entry.Type, entry.Duplicate, entry.Valid, entry.Exists, entry.Error,
		)
		if err != nil {
			return false, err
		}
		rows, _ := result.RowsAffected()
		changed = changed || rows > 0
		if rows > 0 {
			changedTasks[entry.TaskKey] = struct{}{}
		}
	}
	if !changed {
		return false, tx.Commit()
	}
	for taskKey := range changedTasks {
		count := issues[taskKey]
		if _, err := tx.ExecContext(ctx, `
			UPDATE tasks SET context_issues = ?, index_hash = ''
			WHERE project_id = ? AND task_key = ?`,
			count, projectID, taskKey); err != nil {
			return false, err
		}
	}
	now := time.Now().UTC()
	if err := touchResourceState(ctx, tx, projectID, model.ResourceTasks, "context-validity", now); err != nil {
		return false, err
	}
	payload, _ := json.Marshal(map[string]any{"contextEntries": len(entries)})
	if err := insertActivity(ctx, tx, projectID, "", "context.validity.updated", "git", payload, now); err != nil {
		return false, err
	}
	if err := finishIncrementalIndex(ctx, tx, projectID, now, true); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func groupTaskResources(snapshot model.TrellisSnapshot) (map[string][]model.Artifact, map[string][]model.ContextEntry) {
	artifacts := make(map[string][]model.Artifact)
	for _, artifact := range snapshot.Artifacts {
		artifacts[artifact.TaskKey] = append(artifacts[artifact.TaskKey], artifact)
	}
	entries := make(map[string][]model.ContextEntry)
	for _, entry := range snapshot.ContextEntries {
		entries[entry.TaskKey] = append(entries[entry.TaskKey], entry)
	}
	return artifacts, entries
}

func replaceTaskResources(
	ctx context.Context,
	tx *sql.Tx,
	projectID, taskKey string,
	artifacts []model.Artifact,
	entries []model.ContextEntry,
) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM task_artifacts WHERE project_id = ? AND task_key = ?`, projectID, taskKey); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM task_context_entries WHERE project_id = ? AND task_key = ?`, projectID, taskKey); err != nil {
		return err
	}
	for _, artifact := range artifacts {
		if err := insertArtifact(ctx, tx, artifact); err != nil {
			return err
		}
	}
	for _, entry := range entries {
		if err := insertContextEntry(ctx, tx, entry); err != nil {
			return err
		}
	}
	return nil
}

func replaceWorkflowStates(ctx context.Context, tx *sql.Tx, projectID string, incoming []model.WorkflowState) (bool, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT project_id, name, label, sort_order FROM workflow_states
		WHERE project_id = ? ORDER BY sort_order, name`, projectID)
	if err != nil {
		return false, err
	}
	current := make([]model.WorkflowState, 0)
	for rows.Next() {
		var state model.WorkflowState
		if err := rows.Scan(&state.ProjectID, &state.Name, &state.Label, &state.Order); err != nil {
			rows.Close()
			return false, err
		}
		current = append(current, state)
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	if workflowStatesEqual(current, incoming) {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM workflow_states WHERE project_id = ?`, projectID); err != nil {
		return false, err
	}
	for _, state := range incoming {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO workflow_states(project_id, name, label, sort_order)
			VALUES (?, ?, ?, ?)`, projectID, state.Name, state.Label, state.Order); err != nil {
			return false, fmt.Errorf("保存工作流状态: %w", err)
		}
	}
	return true, nil
}

func workflowStatesEqual(left, right []model.WorkflowState) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Name != right[index].Name || left[index].Label != right[index].Label || left[index].Order != right[index].Order {
			return false
		}
	}
	return true
}

type previousSession struct {
	SourceHash string
	TaskKey    string
	Stale      bool
}

func reconcileSessions(
	ctx context.Context,
	tx *sql.Tx,
	projectID string,
	incoming []model.Session,
	taskState map[string]model.TaskRuntimeState,
) (bool, bool, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT session_key, source_hash, task_key, stale
		FROM runtime_sessions WHERE project_id = ?`, projectID)
	if err != nil {
		return false, false, err
	}
	previous := make(map[string]previousSession)
	for rows.Next() {
		var key string
		var item previousSession
		if err := rows.Scan(&key, &item.SourceHash, &item.TaskKey, &item.Stale); err != nil {
			rows.Close()
			return false, false, err
		}
		previous[key] = item
	}
	if err := rows.Close(); err != nil {
		return false, false, err
	}
	if err := rows.Err(); err != nil {
		return false, false, err
	}

	sessionChanged := false
	for _, session := range incoming {
		old, exists := previous[session.Key]
		if !exists || old.SourceHash != session.SourceHash || old.TaskKey != session.TaskKey || old.Stale != session.Stale {
			if err := insertSession(ctx, tx, session); err != nil {
				return false, false, err
			}
			sessionChanged = true
		}
		delete(previous, session.Key)
	}
	for key := range previous {
		if _, err := tx.ExecContext(ctx, `DELETE FROM runtime_sessions WHERE project_id = ? AND session_key = ?`, projectID, key); err != nil {
			return false, false, err
		}
		sessionChanged = true
	}

	taskChanged := false
	rows, err = tx.QueryContext(ctx, `
		SELECT task_key, runtime_phase, active_sessions FROM tasks WHERE project_id = ?`, projectID)
	if err != nil {
		return false, false, err
	}
	type taskRuntimeUpdate struct {
		key   string
		state model.TaskRuntimeState
	}
	updates := make([]taskRuntimeUpdate, 0)
	for rows.Next() {
		var key, currentPhase string
		var currentSessions int
		if err := rows.Scan(&key, &currentPhase, &currentSessions); err != nil {
			rows.Close()
			return false, false, err
		}
		next, exists := taskState[key]
		if !exists || next.RuntimePhase == "" {
			continue
		}
		if next.RuntimePhase == currentPhase && next.ActiveSessions == currentSessions {
			continue
		}
		updates = append(updates, taskRuntimeUpdate{key: key, state: next})
	}
	if err := rows.Close(); err != nil {
		return false, false, err
	}
	if err := rows.Err(); err != nil {
		return false, false, err
	}
	for _, update := range updates {
		if _, err := tx.ExecContext(ctx, `
			UPDATE tasks SET runtime_phase = ?, active_sessions = ?
			WHERE project_id = ? AND task_key = ?`, update.state.RuntimePhase, update.state.ActiveSessions, projectID, update.key); err != nil {
			return false, false, err
		}
		taskChanged = true
	}
	return sessionChanged, taskChanged, nil
}

func touchResourceState(
	ctx context.Context,
	tx *sql.Tx,
	projectID string,
	resource model.ResourceType,
	seed string,
	now time.Time,
) error {
	var current string
	err := tx.QueryRowContext(ctx, `
		SELECT content_hash FROM resource_states
		WHERE project_id = ? AND resource_type = ?`, projectID, resource).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	digest := sha256.Sum256([]byte(current + "\x00" + seed + "\x00" + formatTime(now)))
	if err := storeResourceHash(ctx, tx, projectID, resource, "incremental:"+hex.EncodeToString(digest[:])); err != nil {
		return err
	}
	return incrementRevision(ctx, tx, projectID, resource, now)
}

func finishIncrementalIndex(ctx context.Context, tx *sql.Tx, projectID string, now time.Time, activityChanged bool) error {
	if activityChanged {
		if err := pruneActivity(ctx, tx, projectID); err != nil {
			return err
		}
		if err := incrementRevision(ctx, tx, projectID, model.ResourceActivity, now); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE projects SET indexed_at = ?, index_error = '', updated_at = ? WHERE id = ?`,
		formatTime(now), formatTime(now), projectID)
	return err
}

func resourceChanged(ctx context.Context, tx *sql.Tx, projectID string, resource model.ResourceType, hash string) (bool, error) {
	var current string
	err := tx.QueryRowContext(ctx, `
		SELECT content_hash FROM resource_states
		WHERE project_id = ? AND resource_type = ?`, projectID, resource).Scan(&current)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return current != hash, nil
}

func setResourceState(ctx context.Context, tx *sql.Tx, projectID string, resource model.ResourceType, hash string, now time.Time) error {
	if err := storeResourceHash(ctx, tx, projectID, resource, hash); err != nil {
		return err
	}
	return incrementRevision(ctx, tx, projectID, resource, now)
}

func storeResourceHash(ctx context.Context, tx *sql.Tx, projectID string, resource model.ResourceType, hash string) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO resource_states(project_id, resource_type, content_hash)
		VALUES (?, ?, ?)
		ON CONFLICT(project_id, resource_type) DO UPDATE SET content_hash = excluded.content_hash`,
		projectID, resource, hash); err != nil {
		return err
	}
	return nil
}

func incrementRevision(ctx context.Context, tx *sql.Tx, projectID string, resource model.ResourceType, now time.Time) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO resource_revisions(project_id, resource_type, revision, updated_at)
		VALUES (?, ?, 1, ?)
		ON CONFLICT(project_id, resource_type) DO UPDATE SET
			revision = resource_revisions.revision + 1,
			updated_at = excluded.updated_at`,
		projectID, resource, formatTime(now))
	return err
}

func loadPreviousTasks(ctx context.Context, tx *sql.Tx, projectID string) (map[string]previousTask, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT task_key, source_hash, index_hash, status, archived FROM tasks WHERE project_id = ?`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]previousTask)
	for rows.Next() {
		var key string
		var item previousTask
		if err := rows.Scan(&key, &item.Hash, &item.IndexHash, &item.Status, &item.Archived); err != nil {
			return nil, err
		}
		result[key] = item
	}
	return result, rows.Err()
}

func insertTask(ctx context.Context, tx *sql.Tx, task model.Task) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO tasks (
			project_id, task_key, task_id, directory_name, name, title, description,
			status, runtime_phase, dev_type, scope, package_name, priority, creator,
			assignee, created_at, completed_at, branch, base_branch, worktree_path,
			commit_hash, pr_url, subtasks_json, children_json, parent_id,
			related_files_json, notes, meta_json, archived, archive_month, source_path,
			source_hash, index_hash, modified_at, artifact_count, context_issues, active_sessions
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id, task_key) DO UPDATE SET
			task_id = excluded.task_id, directory_name = excluded.directory_name,
			name = excluded.name, title = excluded.title, description = excluded.description,
			status = excluded.status, runtime_phase = excluded.runtime_phase,
			dev_type = excluded.dev_type, scope = excluded.scope, package_name = excluded.package_name,
			priority = excluded.priority, creator = excluded.creator, assignee = excluded.assignee,
			created_at = excluded.created_at, completed_at = excluded.completed_at,
			branch = excluded.branch, base_branch = excluded.base_branch, worktree_path = excluded.worktree_path,
			commit_hash = excluded.commit_hash, pr_url = excluded.pr_url,
			subtasks_json = excluded.subtasks_json, children_json = excluded.children_json,
			parent_id = excluded.parent_id, related_files_json = excluded.related_files_json,
			notes = excluded.notes, meta_json = excluded.meta_json,
			archived = excluded.archived, archive_month = excluded.archive_month,
			source_path = excluded.source_path, source_hash = excluded.source_hash,
			index_hash = excluded.index_hash, modified_at = excluded.modified_at,
			artifact_count = excluded.artifact_count, context_issues = excluded.context_issues,
			active_sessions = excluded.active_sessions`,
		task.ProjectID, task.Key, task.ID, task.Directory, task.Name, task.Title, task.Description,
		task.Status, task.RuntimePhase, task.DevType, task.Scope, task.Package, task.Priority,
		task.Creator, task.Assignee, task.CreatedAt, task.CompletedAt, task.Branch,
		task.BaseBranch, task.WorktreePath, task.Commit, task.PRURL,
		rawString(task.Subtasks, "[]"), rawString(task.Children, "[]"), task.Parent,
		rawString(task.RelatedFiles, "[]"), task.Notes, rawString(task.Meta, "{}"),
		task.Archived, task.ArchiveMonth, task.SourcePath, task.SourceHash, task.IndexHash,
		formatTime(task.ModifiedAt), task.ArtifactCount, task.ContextIssues, task.ActiveSessions,
	)
	if err != nil {
		return fmt.Errorf("保存任务 %s: %w", task.Key, err)
	}
	return nil
}

func insertArtifact(ctx context.Context, tx *sql.Tx, artifact model.Artifact) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO task_artifacts (
			project_id, task_key, kind, name, path, content_type, content, size, hash, modified_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		artifact.ProjectID, artifact.TaskKey, artifact.Kind, artifact.Name, artifact.Path,
		artifact.ContentType, artifact.Content, artifact.Size, artifact.Hash, formatTime(artifact.ModifiedAt))
	if err != nil {
		return fmt.Errorf("保存任务文档 %s: %w", artifact.Path, err)
	}
	return nil
}

func insertContextEntry(ctx context.Context, tx *sql.Tx, entry model.ContextEntry) error {
	entryType := entry.Type
	if entryType == "" {
		entryType = "file"
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO task_context_entries (
			project_id, task_key, action, line_no, file_path, reason,
			entry_type, is_example, is_duplicate, is_valid, file_exists, error
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.ProjectID, entry.TaskKey, entry.Action, entry.Line, entry.File, entry.Reason,
		entryType, entry.Example, entry.Duplicate, entry.Valid, entry.Exists, entry.Error)
	return err
}

func insertSession(ctx context.Context, tx *sql.Tx, session model.Session) error {
	var lastSeen any
	if session.LastSeenAt != nil {
		lastSeen = formatTime(*session.LastSeenAt)
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO runtime_sessions (
			project_id, session_key, platform, current_task, task_key,
			last_seen_at, current_run_json, stale, source_path, source_hash
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id, session_key) DO UPDATE SET
			platform = excluded.platform, current_task = excluded.current_task,
			task_key = excluded.task_key, last_seen_at = excluded.last_seen_at,
			current_run_json = excluded.current_run_json, stale = excluded.stale,
			source_path = excluded.source_path, source_hash = excluded.source_hash`,
		session.ProjectID, session.Key, session.Platform, session.CurrentTask, session.TaskKey,
		lastSeen, rawString(session.CurrentRun, "null"), session.Stale, session.SourcePath, session.SourceHash)
	return err
}

func insertActivity(ctx context.Context, tx *sql.Tx, projectID, taskKey, eventType, source string, payload []byte, now time.Time) error {
	if len(payload) > maxActivityPayloadBytes {
		preview := strings.ToValidUTF8(string(payload[:maxActivityPreviewBytes]), "�")
		payload, _ = json.Marshal(map[string]any{"truncated": true, "preview": preview})
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO activity_events(project_id, task_key, event_type, source, payload_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		projectID, taskKey, eventType, source, rawString(payload, "{}"), formatTime(now))
	return err
}

func pruneActivity(ctx context.Context, tx *sql.Tx, projectID string) error {
	_, err := tx.ExecContext(ctx, `
		DELETE FROM activity_events
		WHERE project_id = ? AND id < COALESCE((
			SELECT id FROM activity_events
			WHERE project_id = ? ORDER BY id DESC LIMIT 1 OFFSET ?
		), 0)`, projectID, projectID, maxActivityPerProject-1)
	return err
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

func rawString(value []byte, fallback string) string {
	if len(value) == 0 || !json.Valid(value) {
		return fallback
	}
	return string(value)
}
