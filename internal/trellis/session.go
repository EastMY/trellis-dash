package trellis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yunnnn/trellis-dash/internal/model"
)

type rawSession struct {
	Platform    string          `json:"platform"`
	CurrentTask *string         `json:"current_task"`
	LastSeenAt  *string         `json:"last_seen_at"`
	CurrentRun  json.RawMessage `json:"current_run"`
}

func scanSessions(
	ctx context.Context,
	root, trellisRoot, projectID string,
	taskByDirectory map[string]string,
	budget *scanBudget,
) ([]scannedSession, error) {
	sessionsRoot, exists, err := optionalDirectory(root, filepath.Join(trellisRoot, ".runtime", "sessions"))
	if err != nil {
		return nil, err
	}
	if !exists {
		return make([]scannedSession, 0), nil
	}

	directory, err := os.Open(sessionsRoot)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	entries := make([]os.DirEntry, 0)
	visited := 0
	for {
		batch, readErr := directory.ReadDir(256)
		for _, entry := range batch {
			visited++
			if err := budget.addWalk("Session"); err != nil {
				return nil, err
			}
			if visited > MaxWalkEntries {
				return nil, fmt.Errorf("%w: Session 目录项超过 %d", ErrResourceLimit, MaxWalkEntries)
			}
			if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
				entries = append(entries, entry)
				if len(entries) > MaxSessionsPerProject {
					return nil, fmt.Errorf("%w: Session 超过 %d 个", ErrResourceLimit, MaxSessionsPerProject)
				}
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	result := make([]scannedSession, 0, len(entries))
	var totalBytes int64
	for _, entry := range entries {
		if err := contextError(ctx); err != nil {
			return nil, err
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		path := filepath.Join(sessionsRoot, entry.Name())
		content, _, err := readSafeFile(root, path, MaxJSONBytes)
		if err != nil {
			return nil, err
		}
		totalBytes += int64(len(content))
		if err := budget.addRead(int64(len(content)), "Session JSON"); err != nil {
			return nil, err
		}
		if totalBytes > MaxSessionJSONTotalBytes {
			return nil, fmt.Errorf("%w: Session JSON 总量超过 %d 字节", ErrResourceLimit, MaxSessionJSONTotalBytes)
		}
		if !strings.HasPrefix(strings.TrimSpace(string(content)), "{") {
			return nil, fmt.Errorf("无效 Session JSON %s: 顶层必须是 JSON 对象", entry.Name())
		}
		var raw rawSession
		if err := json.Unmarshal(content, &raw); err != nil {
			return nil, fmt.Errorf("无效 Session JSON %s: %w", entry.Name(), err)
		}

		session := model.Session{
			ProjectID:  projectID,
			Key:        strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())),
			Platform:   raw.Platform,
			CurrentRun: normalizedRaw(raw.CurrentRun, `null`),
			SourcePath: filepath.ToSlash(filepath.Join(".trellis", ".runtime", "sessions", entry.Name())),
			SourceHash: hashBytes(content),
		}
		if raw.CurrentTask != nil {
			session.CurrentTask = strings.TrimSpace(*raw.CurrentTask)
		}
		if raw.LastSeenAt != nil && strings.TrimSpace(*raw.LastSeenAt) != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(*raw.LastSeenAt)); err == nil {
				parsed = parsed.UTC()
				session.LastSeenAt = &parsed
			}
		}
		if session.CurrentTask != "" {
			resolved, validDirectory := resolveSessionTask(root, session.CurrentTask)
			session.Stale = !validDirectory
			if validDirectory {
				var found bool
				session.TaskKey, found = taskByDirectory[filepath.Clean(resolved)]
				session.Stale = !found
			}
		}

		result = append(result, scannedSession{
			session:    session,
			sourceHash: session.SourceHash,
			runPhase:   phaseFromCurrentRun(session.CurrentRun),
		})
	}
	return result, nil
}

func resolveSessionTask(root, reference string) (string, bool) {
	normalized := strings.TrimSpace(reference)
	if normalized == "" || strings.IndexByte(normalized, 0) >= 0 {
		return "", false
	}
	normalized = strings.ReplaceAll(normalized, "\\", "/")
	for strings.HasPrefix(normalized, "./") {
		normalized = strings.TrimPrefix(normalized, "./")
	}

	path := filepath.FromSlash(normalized)
	if !filepath.IsAbs(path) {
		slashPath := filepath.ToSlash(path)
		switch {
		case slashPath == ".trellis" || strings.HasPrefix(slashPath, ".trellis/"):
			path = filepath.Join(root, path)
		case slashPath == "tasks" || strings.HasPrefix(slashPath, "tasks/"):
			path = filepath.Join(root, ".trellis", path)
		default:
			path = filepath.Join(root, ".trellis", "tasks", path)
		}
	}

	resolved, exists, err := resolvePathAllowMissing(root, path)
	if err != nil || !exists {
		return "", false
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", false
	}
	return resolved, true
}

func phaseFromCurrentRun(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return phaseFromRunText(text)
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return ""
	}
	values := make([]string, 0, 5)
	for _, key := range []string{"phase", "action", "mode", "type", "status"} {
		value, ok := object[key]
		if !ok || json.Unmarshal(value, &text) != nil {
			continue
		}
		values = append(values, text)
	}
	// 任一显式失败/阻塞状态都应覆盖 action=check/implement 等动作字段。
	for _, value := range values {
		if phase := phaseFromRunText(value); phase == "blocked" {
			return phase
		}
	}
	for _, value := range values {
		if phase := phaseFromRunText(value); phase != "" {
			return phase
		}
	}
	return ""
}

func phaseFromRunText(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch {
	case strings.Contains(normalized, "block"), strings.Contains(normalized, "fail"), strings.Contains(normalized, "error"):
		return "blocked"
	case strings.Contains(normalized, "check"),
		strings.Contains(normalized, "review"),
		strings.Contains(normalized, "verify"),
		strings.Contains(normalized, "test"):
		return "checking"
	case strings.Contains(normalized, "implement"),
		strings.Contains(normalized, "develop"),
		strings.Contains(normalized, "code"):
		return "implementing"
	case strings.Contains(normalized, "plan"), strings.Contains(normalized, "research"):
		return "planning"
	default:
		return ""
	}
}
