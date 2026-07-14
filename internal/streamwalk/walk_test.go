package streamwalk

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestWalkSupportsSkipAndBoundary(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"keep/a.txt", "skip/b.txt", "keep/nested/c.txt"} {
		absolute := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(path), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	visited := make(map[string]bool)
	err := Walk(context.Background(), root, root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, _ := filepath.Rel(root, path)
		visited[filepath.ToSlash(relative)] = true
		if entry.IsDir() && entry.Name() == "skip" {
			return fs.SkipDir
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !visited["keep/nested/c.txt"] || visited["skip/b.txt"] {
		t.Fatalf("遍历/跳过结果异常: %+v", visited)
	}
	if err := Walk(context.Background(), root, filepath.Dir(root), func(string, fs.DirEntry, error) error { return nil }); err == nil {
		t.Fatal("越界起点应被拒绝")
	}
}

func TestWalkCancellationStopsBetweenBatches(t *testing.T) {
	root := t.TempDir()
	for index := 0; index < readBatchSize*3; index++ {
		name := filepath.Join(root, "item-"+formatIndex(index))
		if err := os.WriteFile(name, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	visited := 0
	err := Walk(ctx, root, root, func(_ string, _ fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		visited++
		if visited == 20 {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || visited >= readBatchSize*3 {
		t.Fatalf("取消结果异常: visited=%d err=%v", visited, err)
	}
}

func formatIndex(index int) string {
	const digits = "0123456789"
	buffer := []byte{'0', '0', '0', '0'}
	for position := len(buffer) - 1; position >= 0; position-- {
		buffer[position] = digits[index%10]
		index /= 10
	}
	return string(buffer)
}
