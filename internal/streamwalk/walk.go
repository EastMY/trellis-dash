// Package streamwalk 提供有边界、可取消且分批读取目录的遍历器。
package streamwalk

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const readBatchSize = 256

// Walk 在 boundary 内遍历 start。它用 os.Root 阻止并发符号链接替换越界，
// 并通过 File.ReadDir(n) 分批读取，避免 filepath.WalkDir 对超大单目录一次性分配。
func Walk(ctx context.Context, boundary, start string, walkFn fs.WalkDirFunc) error {
	if walkFn == nil {
		return errors.New("streamwalk: walkFn 不能为空")
	}
	boundary, err := filepath.Abs(boundary)
	if err != nil {
		return fmt.Errorf("解析遍历边界: %w", err)
	}
	start, err = filepath.Abs(start)
	if err != nil {
		return fmt.Errorf("解析遍历起点: %w", err)
	}
	relativeStart, err := filepath.Rel(boundary, start)
	if err != nil || relativeStart == ".." || strings.HasPrefix(relativeStart, ".."+string(filepath.Separator)) {
		return fmt.Errorf("streamwalk: 起点 %s 越过边界 %s", start, boundary)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	root, err := os.OpenRoot(boundary)
	if err != nil {
		return fmt.Errorf("打开遍历边界: %w", err)
	}
	defer root.Close()
	startInfo, err := root.Lstat(relativeStart)
	if err != nil {
		return walkFn(start, nil, err)
	}
	startEntry := fs.FileInfoToDirEntry(startInfo)
	if err := walkFn(start, startEntry, nil); err != nil {
		if errors.Is(err, fs.SkipDir) {
			return nil
		}
		return err
	}
	if !startEntry.IsDir() || startEntry.Type()&os.ModeSymlink != 0 {
		return nil
	}

	// 目录栈只保存已经通过回调预算检查的相对路径。
	directories := []string{relativeStart}
	for len(directories) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		last := len(directories) - 1
		directory := directories[last]
		directories = directories[:last]
		file, err := root.Open(directory)
		if err != nil {
			absolute := filepath.Join(boundary, directory)
			if callbackErr := walkFn(absolute, nil, err); callbackErr != nil && !errors.Is(callbackErr, fs.SkipDir) {
				return callbackErr
			}
			continue
		}
		for {
			if err := ctx.Err(); err != nil {
				_ = file.Close()
				return err
			}
			entries, readErr := file.ReadDir(readBatchSize)
			for _, entry := range entries {
				if err := ctx.Err(); err != nil {
					_ = file.Close()
					return err
				}
				relative := filepath.Join(directory, entry.Name())
				absolute := filepath.Join(boundary, relative)
				callbackErr := walkFn(absolute, entry, nil)
				if callbackErr != nil {
					if errors.Is(callbackErr, fs.SkipDir) && entry.IsDir() {
						continue
					}
					_ = file.Close()
					return callbackErr
				}
				if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
					directories = append(directories, relative)
				}
			}
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				absolute := filepath.Join(boundary, directory)
				callbackErr := walkFn(absolute, nil, readErr)
				if callbackErr != nil && !errors.Is(callbackErr, fs.SkipDir) {
					_ = file.Close()
					return callbackErr
				}
				break
			}
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("关闭遍历目录 %s: %w", filepath.Join(boundary, directory), err)
		}
	}
	return nil
}
