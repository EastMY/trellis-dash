package trellis

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yunnnn/trellis-dash/internal/model"
)

func TestScannerFileSizeLimits(t *testing.T) {
	t.Run("task JSON 2MB", func(t *testing.T) {
		root := newTrellisProject(t)
		path := filepath.Join(root, ".trellis", "tasks", "large", "task.json")
		createLargeFile(t, path, MaxJSONBytes+1)
		_, err := NewScanner().Scan(context.Background(), model.Project{ID: "large", Root: root})
		if !errors.Is(err, ErrFileTooLarge) {
			t.Fatalf("Scan() error = %v, want ErrFileTooLarge", err)
		}
	})

	t.Run("Markdown 10MB", func(t *testing.T) {
		root := newTrellisProject(t)
		writeTestFile(t, root, ".trellis/tasks/large/task.json", `{}`)
		path := filepath.Join(root, ".trellis", "tasks", "large", "prd.md")
		createLargeFile(t, path, MaxMarkdownBytes+1)
		_, err := NewScanner().Scan(context.Background(), model.Project{ID: "large", Root: root})
		if !errors.Is(err, ErrFileTooLarge) {
			t.Fatalf("Scan() error = %v, want ErrFileTooLarge", err)
		}
	})

	t.Run("Session JSON 2MB", func(t *testing.T) {
		root := newTrellisProject(t)
		path := filepath.Join(root, ".trellis", ".runtime", "sessions", "large.json")
		createLargeFile(t, path, MaxJSONBytes+1)
		_, err := NewScanner().Scan(context.Background(), model.Project{ID: "large", Root: root})
		if !errors.Is(err, ErrFileTooLarge) {
			t.Fatalf("Scan() error = %v, want ErrFileTooLarge", err)
		}
	})

	t.Run("JSONL 单行 1MB", func(t *testing.T) {
		root := newTrellisProject(t)
		writeTestFile(t, root, ".trellis/tasks/large/task.json", `{}`)
		line := `{"file":"` + strings.Repeat("a", MaxJSONLLineBytes) + `"}`
		writeTestFile(t, root, ".trellis/tasks/large/implement.jsonl", line)
		_, err := NewScanner().Scan(context.Background(), model.Project{ID: "large", Root: root})
		if err == nil || !strings.Contains(err.Error(), "JSONL 单行") {
			t.Fatalf("Scan() error = %v, want JSONL line limit error", err)
		}
	})
}

func TestScannerMalformedJSONBoundaries(t *testing.T) {
	t.Run("task 顶层 null", func(t *testing.T) {
		root := newTrellisProject(t)
		writeTestFile(t, root, ".trellis/tasks/bad/task.json", `null`)
		_, err := NewScanner().Scan(context.Background(), model.Project{ID: "bad", Root: root})
		if err == nil || !strings.Contains(err.Error(), "顶层必须是 JSON 对象") {
			t.Fatalf("Scan() error = %v", err)
		}
	})

	t.Run("Session 无效 JSON", func(t *testing.T) {
		root := newTrellisProject(t)
		writeTestFile(t, root, ".trellis/.runtime/sessions/bad.json", `{`)
		_, err := NewScanner().Scan(context.Background(), model.Project{ID: "bad", Root: root})
		if err == nil || !strings.Contains(err.Error(), "无效 Session JSON") {
			t.Fatalf("Scan() error = %v", err)
		}
	})
}

func TestScannerAcceptsJSONLLineAtExactLimit(t *testing.T) {
	root := newTrellisProject(t)
	writeTestFile(t, root, ".trellis/tasks/exact/task.json", `{}`)
	prefix, suffix := `{"_example":"`, `"}`
	line := prefix + strings.Repeat("x", MaxJSONLLineBytes-len(prefix)-len(suffix)) + suffix
	if len(line) != MaxJSONLLineBytes {
		t.Fatalf("测试数据长度 = %d, want %d", len(line), MaxJSONLLineBytes)
	}
	writeTestFile(t, root, ".trellis/tasks/exact/implement.jsonl", line)
	snapshot := mustScan(t, model.Project{ID: "exact", Root: root})
	if len(snapshot.ContextEntries) != 1 || !snapshot.ContextEntries[0].Example || !snapshot.ContextEntries[0].Valid {
		t.Fatalf("恰好达到上限的 JSONL 行解析错误: %+v", snapshot.ContextEntries)
	}
}

func TestScannerRejectsAggregateArtifactSize(t *testing.T) {
	root := newTrellisProject(t)
	writeTestFile(t, root, ".trellis/tasks/large/task.json", `{}`)
	for _, relative := range []string{
		"prd.md", "design.md", "implement.md", "info.md",
		"research/one.md", "research/two.md",
	} {
		createLargeFile(t, filepath.Join(root, ".trellis", "tasks", "large", relative), MaxMarkdownBytes)
	}
	_, err := NewScanner().Scan(context.Background(), model.Project{ID: "large", Root: root})
	if !errors.Is(err, ErrFileTooLarge) || !strings.Contains(err.Error(), "文档总量") {
		t.Fatalf("Scan() error = %v, want aggregate artifact limit", err)
	}
}

func TestScannerRejectsTooManyContextLines(t *testing.T) {
	root := newTrellisProject(t)
	writeTestFile(t, root, ".trellis/tasks/context/task.json", `{}`)
	writeTestFile(t, root, ".trellis/tasks/context/implement.jsonl", strings.Repeat("{}\n", MaxContextEntriesPerManifest+1))
	_, err := NewScanner().Scan(context.Background(), model.Project{ID: "context", Root: root})
	if !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("Scan() error = %v, want ErrResourceLimit", err)
	}
}

func createLargeFile(t *testing.T, path string, size int64) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := file.Truncate(size); err != nil {
		file.Close()
		t.Fatalf("Truncate: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
