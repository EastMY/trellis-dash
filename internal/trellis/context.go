package trellis

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yunnnn/trellis-dash/internal/model"
)

func scanContextManifests(ctx context.Context, root, projectID string, item scannedTask, budget *scanBudget) ([]model.ContextEntry, error) {
	entries := make([]model.ContextEntry, 0)
	for _, manifest := range []struct {
		name   string
		action string
	}{
		{name: "implement.jsonl", action: "implement"},
		{name: "check.jsonl", action: "check"},
	} {
		path := filepath.Join(item.directory, manifest.name)
		_, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		manifestEntries, err := scanContextManifest(ctx, root, projectID, item.task.Key, manifest.action, path, budget)
		if err != nil {
			return nil, err
		}
		entries = append(entries, manifestEntries...)
	}
	markDuplicateContextEntries(root, entries)
	return entries, nil
}

// markDuplicateContextEntries 按阶段和真实路径识别重复引用；保留第一条，后续行标为异常。
// 使用 realpath 后，符号链接或不同的相对写法也无法绕过重复检查。
func markDuplicateContextEntries(root string, entries []model.ContextEntry) {
	seen := make(map[string]struct{}, len(entries))
	for index := range entries {
		entry := &entries[index]
		if entry.Example || !entry.Valid || entry.File == "" {
			continue
		}
		candidate := filepath.FromSlash(entry.File)
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(root, candidate)
		}
		resolved, err := resolveExistingPath(root, candidate)
		if err != nil {
			continue
		}
		key := entry.Action + "\x00" + resolved
		if _, exists := seen[key]; exists {
			entry.Duplicate = true
			entry.Valid = false
			entry.Error = "同一阶段重复引用该路径"
			continue
		}
		seen[key] = struct{}{}
	}
}

func scanContextManifest(ctx context.Context, root, projectID, taskKey, action, path string, budget *scanBudget) ([]model.ContextEntry, error) {
	file, _, err := openSafeRegular(root, path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	entries := make([]model.ContextEntry, 0)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), MaxJSONLLineBytes+1)
	lineNumber := 0
	var manifestBytes int64
	for scanner.Scan() {
		lineNumber++
		if err := contextError(ctx); err != nil {
			return nil, err
		}
		line := bytes.TrimSpace(scanner.Bytes())
		manifestBytes += int64(len(scanner.Bytes()) + 1)
		if err := budget.addRead(int64(len(scanner.Bytes())+1), "Context Manifest"); err != nil {
			return nil, err
		}
		if lineNumber > MaxContextEntriesPerManifest || manifestBytes > MaxContextManifestBytes {
			return nil, fmt.Errorf("%w: %s 超过 %d 行或 %d 字节", ErrResourceLimit, path, MaxContextEntriesPerManifest, MaxContextManifestBytes)
		}
		if len(line) == 0 {
			continue
		}
		if len(line) > MaxJSONLLineBytes {
			return nil, fmt.Errorf("%w: %s 第 %d 行超过 %d 字节", ErrFileTooLarge, path, lineNumber, MaxJSONLLineBytes)
		}
		entries = append(entries, parseContextLine(root, projectID, taskKey, action, lineNumber, line))
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取 %s（JSONL 单行最多 %d 字节）: %w", path, MaxJSONLLineBytes, err)
	}
	return entries, nil
}

func parseContextLine(root, projectID, taskKey, action string, lineNumber int, line []byte) model.ContextEntry {
	entry := model.ContextEntry{
		ProjectID: projectID,
		TaskKey:   taskKey,
		Action:    action,
		Line:      lineNumber,
		Type:      "file",
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(line, &object); err != nil {
		entry.Error = "无效 JSON: " + err.Error()
		return entry
	}
	if object == nil {
		entry.Error = "Context 行必须是 JSON 对象"
		return entry
	}

	if rawExample, ok := object["_example"]; ok {
		entry.Example = true
		entry.Type = "example"
		entry.Valid = true
		var message string
		if json.Unmarshal(rawExample, &message) == nil {
			entry.Reason = message
		}
		return entry
	}

	rawFile, ok := object["file"]
	if !ok {
		entry.Error = "缺少 file 字段"
		return entry
	}
	if err := json.Unmarshal(rawFile, &entry.File); err != nil {
		entry.Error = "file 字段必须是字符串"
		return entry
	}
	entry.File = strings.TrimSpace(entry.File)
	if entry.File == "" {
		entry.Error = "file 字段不能为空"
		return entry
	}
	if strings.IndexByte(entry.File, 0) >= 0 {
		entry.Error = "file 字段包含非法空字符"
		return entry
	}
	if rawReason, ok := object["reason"]; ok {
		if err := json.Unmarshal(rawReason, &entry.Reason); err != nil {
			entry.Error = "reason 字段必须是字符串"
			return entry
		}
	}
	entryType := "file"
	if rawType, ok := object["type"]; ok {
		if err := json.Unmarshal(rawType, &entryType); err != nil {
			entry.Error = "type 字段必须是字符串"
			return entry
		}
		entryType = strings.ToLower(strings.TrimSpace(entryType))
		if entryType != "file" && entryType != "directory" {
			entry.Error = "type 字段只支持 file 或 directory"
			return entry
		}
	}
	entry.Type = entryType

	candidate := filepath.FromSlash(entry.File)
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	resolved, exists, err := resolvePathAllowMissing(root, candidate)
	if err != nil {
		if errors.Is(err, ErrPathOutsideRoot) {
			entry.Error = ErrPathOutsideRoot.Error()
		} else {
			entry.Error = "无法解析引用路径: " + err.Error()
		}
		return entry
	}
	if !exists {
		entry.Error = "引用文件不存在"
		return entry
	}
	info, err := os.Stat(resolved)
	if err != nil {
		entry.Error = "读取引用文件: " + err.Error()
		return entry
	}
	entry.Exists = true
	if entryType == "directory" && !info.IsDir() {
		entry.Error = "引用路径不是目录"
		return entry
	}
	if entryType == "file" && !info.Mode().IsRegular() {
		entry.Error = "引用路径不是普通文件"
		return entry
	}
	entry.Valid = true
	return entry
}
