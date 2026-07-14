package trellis

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/yunnnn/trellis-dash/internal/model"
)

// RevalidateContextEntries 只重新检查已索引路径，不重读 task.json、文档或 Manifest。
func (s *Scanner) RevalidateContextEntries(
	ctx context.Context,
	root string,
	entries []model.ContextEntry,
) ([]model.ContextEntry, error) {
	root, err := ValidateRoot(root)
	if err != nil {
		return nil, err
	}
	byTask := make(map[string][]int)
	for index := range entries {
		if index%256 == 0 {
			if err := contextError(ctx); err != nil {
				return nil, err
			}
		}
		entry := &entries[index]
		if entry.Example {
			entry.Valid = true
			entry.Exists = false
			entry.Duplicate = false
			entry.Error = ""
			continue
		}
		// 语法本身无效的行必须等待 Manifest 变化后重新解析。
		if entry.File == "" || (entry.Type != "file" && entry.Type != "directory") {
			continue
		}
		entry.Valid = false
		entry.Exists = false
		entry.Duplicate = false
		entry.Error = ""
		candidate := filepath.FromSlash(entry.File)
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(root, candidate)
		}
		resolved, exists, resolveErr := resolvePathAllowMissing(root, candidate)
		if resolveErr != nil {
			if errors.Is(resolveErr, ErrPathOutsideRoot) {
				entry.Error = ErrPathOutsideRoot.Error()
			} else {
				entry.Error = "无法解析引用路径: " + resolveErr.Error()
			}
			continue
		}
		if !exists {
			entry.Error = "引用文件不存在"
			continue
		}
		info, statErr := os.Stat(resolved)
		if statErr != nil {
			entry.Error = "读取引用文件: " + statErr.Error()
			continue
		}
		entry.Exists = true
		if entry.Type == "directory" && !info.IsDir() {
			entry.Error = "引用路径不是目录"
			continue
		}
		if entry.Type == "file" && !info.Mode().IsRegular() {
			entry.Error = "引用路径不是普通文件"
			continue
		}
		entry.Valid = true
		byTask[entry.TaskKey] = append(byTask[entry.TaskKey], index)
	}
	for _, indexes := range byTask {
		group := make([]model.ContextEntry, 0, len(indexes))
		for _, index := range indexes {
			group = append(group, entries[index])
		}
		markDuplicateContextEntries(root, group)
		for offset, index := range indexes {
			entries[index] = group[offset]
		}
	}
	return entries, nil
}
