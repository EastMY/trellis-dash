package trellis

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	// MaxJSONBytes 限制 task.json、Session JSON 等单个 JSON 文件的大小。
	MaxJSONBytes int64 = 2 * 1024 * 1024
	// MaxMarkdownBytes 限制单个 Markdown 文档的大小。
	MaxMarkdownBytes int64 = 10 * 1024 * 1024
	// MaxJSONLLineBytes 限制 Context Manifest 单行大小。
	MaxJSONLLineBytes = 1024 * 1024
	// MaxArtifactsPerTask 防止恶意 research 目录制造过多内存对象。
	MaxArtifactsPerTask = 1_000
	// MaxTaskArtifactBytes 限制单个任务一次索引保留的 Markdown 总量。
	MaxTaskArtifactBytes int64 = 50 * 1024 * 1024
	// MaxProjectArtifactBytes 限制单个项目一次快照保留的 Markdown 总量。
	MaxProjectArtifactBytes int64 = 200 * 1024 * 1024
	// MaxProjectArtifacts 限制单个项目一次快照中的文档数量。
	MaxProjectArtifacts = 10_000
	// MaxTasksPerProject 与总 JSON 大小共同限制任务快照内存。
	MaxTasksPerProject          = 10_000
	MaxTaskJSONTotalBytes int64 = 200 * 1024 * 1024
	// Context Manifest 同时限制行数与总字节，防止大量合法小行绕过单行上限。
	MaxContextEntriesPerManifest       = 20_000
	MaxContextManifestBytes      int64 = 20 * 1024 * 1024
	MaxProjectContextEntries           = 200_000
	MaxProjectContextBytes       int64 = 100 * 1024 * 1024
	// Session、Spec 及目录遍历均设置项目级数量上限。
	MaxSessionsPerProject          = 10_000
	MaxSessionJSONTotalBytes int64 = 100 * 1024 * 1024
	MaxSpecFiles                   = 10_000
	MaxSpecTotalBytes        int64 = 100 * 1024 * 1024
	MaxWalkEntries                 = 100_000
)

var (
	ErrInvalidRoot     = errors.New("无效的 Trellis 项目根目录")
	ErrPathOutsideRoot = errors.New("路径越过项目根目录")
	ErrFileTooLarge    = errors.New("文件超过大小限制")
	ErrResourceLimit   = errors.New("项目资源超过数量或总量限制")
)

// ValidateRoot 校验项目根目录并返回绝对、无符号链接的规范路径。
//
// .trellis 可以是指向项目根内部的符号链接，但不能借此跳出项目根。
func ValidateRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("%w: 路径为空", ErrInvalidRoot)
	}

	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("%w: 解析绝对路径: %v", ErrInvalidRoot, err)
	}
	canonical, err := filepath.EvalSymlinks(filepath.Clean(abs))
	if err != nil {
		return "", fmt.Errorf("%w: 解析项目根目录: %v", ErrInvalidRoot, err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("%w: 读取项目根目录: %v", ErrInvalidRoot, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: %s 不是目录", ErrInvalidRoot, canonical)
	}

	trellisPath, err := resolveExistingPath(canonical, filepath.Join(canonical, ".trellis"))
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidRoot, err)
	}
	trellisInfo, err := os.Stat(trellisPath)
	if err != nil {
		return "", fmt.Errorf("%w: 读取 .trellis: %v", ErrInvalidRoot, err)
	}
	if !trellisInfo.IsDir() {
		return "", fmt.Errorf("%w: .trellis 不是目录", ErrInvalidRoot)
	}

	return canonical, nil
}

// isWithinRoot 使用 filepath.Rel 判断路径是否仍位于根目录中，避免字符串前缀误判。
func isWithinRoot(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolveExistingPath 同时执行词法路径和 realpath 边界校验。
func resolveExistingPath(root, candidate string) (string, error) {
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("解析路径 %q: %w", candidate, err)
	}
	abs = filepath.Clean(abs)
	if !isWithinRoot(root, abs) {
		return "", fmt.Errorf("%w: %s", ErrPathOutsideRoot, candidate)
	}

	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	if !isWithinRoot(root, canonical) {
		return "", fmt.Errorf("%w: %s", ErrPathOutsideRoot, candidate)
	}
	return canonical, nil
}

// resolvePathAllowMissing 会解析最近的现存父目录，因此即使目标不存在，也能识别父目录符号链接越界。
func resolvePathAllowMissing(root, candidate string) (canonical string, exists bool, err error) {
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", false, fmt.Errorf("解析路径 %q: %w", candidate, err)
	}
	abs = filepath.Clean(abs)
	if !isWithinRoot(root, abs) {
		return "", false, fmt.Errorf("%w: %s", ErrPathOutsideRoot, candidate)
	}

	if resolved, resolveErr := filepath.EvalSymlinks(abs); resolveErr == nil {
		if !isWithinRoot(root, resolved) {
			return "", false, fmt.Errorf("%w: %s", ErrPathOutsideRoot, candidate)
		}
		return resolved, true, nil
	} else if !errors.Is(resolveErr, os.ErrNotExist) {
		return "", false, resolveErr
	}

	probe := abs
	missingParts := make([]string, 0, 4)
	for {
		resolved, resolveErr := filepath.EvalSymlinks(probe)
		if resolveErr == nil {
			for i := len(missingParts) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missingParts[i])
			}
			resolved = filepath.Clean(resolved)
			if !isWithinRoot(root, resolved) {
				return "", false, fmt.Errorf("%w: %s", ErrPathOutsideRoot, candidate)
			}
			return resolved, false, nil
		}
		if !errors.Is(resolveErr, os.ErrNotExist) {
			return "", false, resolveErr
		}

		parent := filepath.Dir(probe)
		if parent == probe {
			return "", false, resolveErr
		}
		missingParts = append(missingParts, filepath.Base(probe))
		probe = parent
	}
}

// optionalDirectory 返回可选目录的 realpath；目录不存在时不视为错误。
func optionalDirectory(root, path string) (string, bool, error) {
	resolved, exists, err := resolvePathAllowMissing(root, path)
	if err != nil {
		return "", false, err
	}
	if !exists {
		return resolved, false, nil
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", false, err
	}
	if !info.IsDir() {
		return "", false, fmt.Errorf("%s 不是目录", path)
	}
	return resolved, true, nil
}

// openSafeRegular 先完成 realpath 校验，再通过 os.Root 打开普通文件。
// os.Root 会在打开过程中阻止符号链接竞态跳出项目根，避免“校验后替换”的 TOCTOU 读取。
func openSafeRegular(root, path string) (*os.File, os.FileInfo, error) {
	resolved, err := resolveExistingPath(root, path)
	if err != nil {
		return nil, nil, err
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil {
		return nil, nil, err
	}
	rootHandle, err := os.OpenRoot(root)
	if err != nil {
		return nil, nil, err
	}
	file, err := rootHandle.Open(relative)
	closeErr := rootHandle.Close()
	if err != nil {
		return nil, nil, err
	}
	if closeErr != nil {
		file.Close()
		return nil, nil, closeErr
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, nil, fmt.Errorf("%s 不是普通文件", path)
	}
	return file, info, nil
}

func readSafeFile(root, path string, limit int64) ([]byte, os.FileInfo, error) {
	file, info, err := openSafeRegular(root, path)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()

	if info.Size() > limit {
		return nil, nil, fmt.Errorf("%w: %s（%d > %d 字节）", ErrFileTooLarge, path, info.Size(), limit)
	}
	content, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, nil, err
	}
	if int64(len(content)) > limit {
		return nil, nil, fmt.Errorf("%w: %s（超过 %d 字节）", ErrFileTooLarge, path, limit)
	}
	return content, info, nil
}
