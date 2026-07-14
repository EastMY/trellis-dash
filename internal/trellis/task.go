package trellis

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/yunnnn/trellis-dash/internal/model"
)

type rawTask struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	Status       string          `json:"status"`
	DevType      *string         `json:"dev_type"`
	Scope        *string         `json:"scope"`
	Package      *string         `json:"package"`
	Priority     string          `json:"priority"`
	Creator      string          `json:"creator"`
	Assignee     string          `json:"assignee"`
	CreatedAt    string          `json:"createdAt"`
	CompletedAt  *string         `json:"completedAt"`
	Branch       *string         `json:"branch"`
	BaseBranch   *string         `json:"base_branch"`
	WorktreePath *string         `json:"worktree_path"`
	Commit       *string         `json:"commit"`
	PRURL        *string         `json:"pr_url"`
	Subtasks     json.RawMessage `json:"subtasks"`
	Children     json.RawMessage `json:"children"`
	Parent       *string         `json:"parent"`
	RelatedFiles json.RawMessage `json:"relatedFiles"`
	Notes        string          `json:"notes"`
	Meta         json.RawMessage `json:"meta"`
}

func parseTask(root, projectID string, file taskFile, logicalSource string) (model.Task, int64, error) {
	content, info, err := readSafeFile(root, file.path, MaxJSONBytes)
	if err != nil {
		return model.Task{}, 0, err
	}
	if !strings.HasPrefix(strings.TrimSpace(string(content)), "{") {
		return model.Task{}, 0, fmt.Errorf("无效 task.json: 顶层必须是 JSON 对象")
	}
	var raw rawTask
	if err := json.Unmarshal(content, &raw); err != nil {
		return model.Task{}, 0, fmt.Errorf("无效 task.json: %w", err)
	}

	directory := filepath.Base(filepath.Dir(file.path))
	raw.ID = firstNonEmpty(raw.ID, raw.Name, directory)
	raw.Name = firstNonEmpty(raw.Name, raw.ID, directory)
	raw.Title = firstNonEmpty(raw.Title, raw.Name, raw.ID, directory)
	if strings.TrimSpace(raw.Status) == "" {
		raw.Status = "planning"
	}
	if strings.TrimSpace(raw.Priority) == "" {
		raw.Priority = "P2"
	}

	task := model.Task{
		ProjectID:    projectID,
		Key:          initialTaskKey(directory, file),
		ID:           raw.ID,
		Directory:    directory,
		Name:         raw.Name,
		Title:        raw.Title,
		Description:  raw.Description,
		Status:       raw.Status,
		DevType:      raw.DevType,
		Scope:        raw.Scope,
		Package:      raw.Package,
		Priority:     raw.Priority,
		Creator:      raw.Creator,
		Assignee:     raw.Assignee,
		CreatedAt:    raw.CreatedAt,
		CompletedAt:  raw.CompletedAt,
		Branch:       raw.Branch,
		BaseBranch:   raw.BaseBranch,
		WorktreePath: raw.WorktreePath,
		Commit:       raw.Commit,
		PRURL:        raw.PRURL,
		Subtasks:     normalizedRaw(raw.Subtasks, `[]`),
		Children:     normalizedRaw(raw.Children, `[]`),
		Parent:       raw.Parent,
		RelatedFiles: normalizedRaw(raw.RelatedFiles, `[]`),
		Notes:        raw.Notes,
		Meta:         normalizedRaw(raw.Meta, `{}`),
		Archived:     file.archived,
		ArchiveMonth: file.archiveMonth,
		SourcePath:   logicalSource,
		SourceHash:   hashBytes(content),
		ModifiedAt:   info.ModTime().UTC(),
	}
	task.RuntimePhase = deriveRuntimePhase(task.Status, task.Archived)
	return task, int64(len(content)), nil
}

func initialTaskKey(directory string, file taskFile) string {
	directoryPath := filepath.Dir(file.relativePath)
	parts := splitPath(directoryPath)
	canonical := len(parts) == 1 ||
		(len(parts) == 2 && parts[0] == "active") ||
		(file.archived && len(parts) == 3)
	if canonical {
		return directory
	}
	digest := hashBytes([]byte(filepath.ToSlash(directoryPath)))
	return directory + "~" + digest[:12]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func normalizedRaw(value json.RawMessage, fallback string) json.RawMessage {
	trimmed := strings.TrimSpace(string(value))
	if trimmed == "" || trimmed == "null" {
		return json.RawMessage(fallback)
	}
	return append(json.RawMessage(nil), value...)
}

func deriveRuntimePhase(status string, archived bool) string {
	if archived {
		return "completed"
	}
	normalized := strings.ToLower(strings.TrimSpace(status))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.ReplaceAll(normalized, " ", "_")
	switch normalized {
	case "planning", "planned", "backlog", "todo", "new":
		return "planning"
	case "in_progress", "implementing", "implementation", "doing", "active":
		return "implementing"
	case "checking", "check", "verifying", "verification", "testing":
		return "checking"
	case "review", "waiting", "pending_review", "on_hold", "paused":
		return "waiting"
	case "blocked", "error", "failed":
		return "blocked"
	case "completed", "complete", "done", "finished", "archived":
		return "completed"
	default:
		return "idle"
	}
}
