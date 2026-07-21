package codexusage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type tokenValue int64

func (v *tokenValue) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if bytes.Equal(data, []byte("null")) || bytes.Equal(data, []byte(`""`)) {
		*v = 0
		return nil
	}
	if len(data) > 1 && data[0] == '"' && data[len(data)-1] == '"' {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		parsed, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return fmt.Errorf("token 字段必须是整数: %w", err)
		}
		*v = tokenValue(parsed)
		return nil
	}
	parsed, err := strconv.ParseInt(string(data), 10, 64)
	if err != nil {
		return fmt.Errorf("token 字段必须是整数: %w", err)
	}
	*v = tokenValue(parsed)
	return nil
}

type logRow struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		ID    string `json:"id"`
		CWD   string `json:"cwd"`
		Model string `json:"model"`
		Type  string `json:"type"`
		Info  struct {
			Last *struct {
				Input     tokenValue `json:"input_tokens"`
				Cached    tokenValue `json:"cached_input_tokens"`
				Output    tokenValue `json:"output_tokens"`
				Reasoning tokenValue `json:"reasoning_output_tokens"`
				Total     tokenValue `json:"total_tokens"`
			} `json:"last_token_usage"`
		} `json:"info"`
	} `json:"payload"`
}

func parseSessionFile(ctx context.Context, path, pathKey string, fp fingerprint, maxLineSize int) (fileData, error) {
	file, err := os.Open(path)
	if err != nil {
		return fileData{}, err
	}
	defer file.Close()

	result := fileData{PathKey: pathKey, Fingerprint: fp, Events: make([]tokenEvent, 0)}
	currentModel := ""
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), maxLineSize)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return fileData{}, err
		}
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var row logRow
		if json.Unmarshal(line, &row) != nil {
			continue
		}
		switch row.Type {
		case "session_meta":
			if row.Payload.ID != "" {
				result.SessionID = row.Payload.ID
			}
			if row.Payload.CWD != "" {
				result.CWD = normalizeAbsolutePath(row.Payload.CWD)
			}
			if row.Payload.Model != "" {
				currentModel = row.Payload.Model
			}
		case "turn_context":
			if row.Payload.Model != "" {
				currentModel = row.Payload.Model
			}
		case "event_msg":
			if row.Payload.Type != "token_count" || row.Payload.Info.Last == nil {
				continue
			}
			result.EventCount++
			timestamp, err := time.Parse(time.RFC3339Nano, row.Timestamp)
			if err != nil {
				continue
			}
			last := row.Payload.Info.Last
			result.Events = append(result.Events, tokenEvent{
				Timestamp: timestamp.Unix(),
				Model:     currentModel,
				Usage: tokenUsage{
					InputTokens: int64(last.Input), CachedInputTokens: int64(last.Cached),
					OutputTokens: int64(last.Output), ReasoningOutputTokens: int64(last.Reasoning),
					TotalTokens: int64(last.Total),
				},
			})
		}
	}
	if err := scanner.Err(); err != nil {
		return fileData{}, fmt.Errorf("读取 JSONL（可能单行超过限制）: %w", err)
	}
	return result, nil
}

func readSessionMetadata(ctx context.Context, path string, maxLineSize int) (sessionID, cwd string, err error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	scanner := bufio.NewScanner(io.LimitReader(file, int64(maxLineSize)*64))
	scanner.Buffer(make([]byte, 64<<10), maxLineSize)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return "", "", err
		}
		var row logRow
		if json.Unmarshal(scanner.Bytes(), &row) != nil || row.Type != "session_meta" {
			continue
		}
		return row.Payload.ID, normalizeAbsolutePath(row.Payload.CWD), nil
	}
	return "", "", scanner.Err()
}

func normalizeAbsolutePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	return filepath.Clean(absolute)
}

func pathWithin(root, candidate string) bool {
	root, candidate = normalizeAbsolutePath(root), normalizeAbsolutePath(candidate)
	if root == "" || candidate == "" {
		return false
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}
