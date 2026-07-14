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

func TestValidateRoot(t *testing.T) {
	t.Run("规范化根目录符号链接", func(t *testing.T) {
		realRoot := newTrellisProject(t)
		linkParent := t.TempDir()
		link := filepath.Join(linkParent, "project-link")
		if err := os.Symlink(realRoot, link); err != nil {
			t.Skipf("当前文件系统不支持 symlink: %v", err)
		}
		got, err := ValidateRoot(link)
		if err != nil {
			t.Fatalf("ValidateRoot() error = %v", err)
		}
		expected, _ := filepath.EvalSymlinks(realRoot)
		if got != expected {
			t.Fatalf("ValidateRoot() = %q, want %q", got, expected)
		}
	})

	for _, test := range []struct {
		name string
		root func(*testing.T) string
	}{
		{name: "空路径", root: func(*testing.T) string { return "" }},
		{name: "不存在", root: func(t *testing.T) string { return filepath.Join(t.TempDir(), "missing") }},
		{name: "缺少 trellis", root: func(t *testing.T) string { return t.TempDir() }},
		{name: "trellis 不是目录", root: func(t *testing.T) string {
			root := t.TempDir()
			writeTestFile(t, root, ".trellis", "file")
			return root
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := ValidateRoot(test.root(t))
			if !errors.Is(err, ErrInvalidRoot) {
				t.Fatalf("ValidateRoot() error = %v, want ErrInvalidRoot", err)
			}
		})
	}
}

func TestValidateRootRejectsTrellisSymlinkOutside(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".trellis")); err != nil {
		t.Skipf("当前文件系统不支持 symlink: %v", err)
	}
	_, err := ValidateRoot(root)
	if !errors.Is(err, ErrInvalidRoot) || !errors.Is(err, ErrPathOutsideRoot) {
		t.Fatalf("ValidateRoot() error = %v, want ErrInvalidRoot + ErrPathOutsideRoot", err)
	}
}

func TestScannerRejectsTaskSymlinkOutsideRoot(t *testing.T) {
	root := newTrellisProject(t)
	outside := writeTestFile(t, t.TempDir(), "task.json", `{}`)
	taskPath := filepath.Join(root, ".trellis", "tasks", "evil", "task.json")
	if err := os.MkdirAll(filepath.Dir(taskPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, taskPath); err != nil {
		t.Skipf("当前文件系统不支持 symlink: %v", err)
	}
	_, err := NewScanner().Scan(context.Background(), model.Project{ID: "security", Root: root})
	if !errors.Is(err, ErrPathOutsideRoot) {
		t.Fatalf("Scan() error = %v, want ErrPathOutsideRoot", err)
	}
}

func TestScannerRejectsArtifactSymlinkOutsideRoot(t *testing.T) {
	root := newTrellisProject(t)
	writeTestFile(t, root, ".trellis/tasks/evil/task.json", `{}`)
	outside := writeTestFile(t, t.TempDir(), "prd.md", "secret")
	if err := os.Symlink(outside, filepath.Join(root, ".trellis", "tasks", "evil", "prd.md")); err != nil {
		t.Skipf("当前文件系统不支持 symlink: %v", err)
	}
	_, err := NewScanner().Scan(context.Background(), model.Project{ID: "security", Root: root})
	if !errors.Is(err, ErrPathOutsideRoot) {
		t.Fatalf("Scan() error = %v, want ErrPathOutsideRoot", err)
	}
}

func TestContextAndSessionOutsidePathsAreReportedWithoutReading(t *testing.T) {
	root := newTrellisProject(t)
	writeTestFile(t, root, ".trellis/tasks/safe/task.json", `{"status":"in_progress"}`)
	outsideRoot := t.TempDir()
	outside := writeTestFile(t, outsideRoot, "secret.md", "do not read")
	link := filepath.Join(root, ".trellis", "outside-link")
	if err := os.Symlink(outsideRoot, link); err != nil {
		t.Skipf("当前文件系统不支持 symlink: %v", err)
	}
	writeTestJSON(t, root, ".trellis/tasks/safe/implement.jsonl", map[string]string{
		"file": ".trellis/outside-link/secret.md",
	})
	writeTestJSON(t, root, ".trellis/.runtime/sessions/outside.json", map[string]any{
		"current_task": outside,
		"current_run":  nil,
	})

	snapshot := mustScan(t, model.Project{ID: "security", Root: root})
	if len(snapshot.ContextEntries) != 1 || snapshot.ContextEntries[0].Valid ||
		!strings.Contains(snapshot.ContextEntries[0].Error, "越过") {
		t.Fatalf("越界 Context 未正确标记: %+v", snapshot.ContextEntries)
	}
	if len(snapshot.Sessions) != 1 || !snapshot.Sessions[0].Stale || snapshot.Sessions[0].TaskKey != "" {
		t.Fatalf("越界 Session 未正确标记 stale: %+v", snapshot.Sessions)
	}
}

func TestResolvePathAllowMissingDetectsSymlinkParentEscape(t *testing.T) {
	root := newTrellisProject(t)
	outside := t.TempDir()
	link := filepath.Join(root, ".trellis", "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("当前文件系统不支持 symlink: %v", err)
	}
	_, exists, err := resolvePathAllowMissing(root, filepath.Join(link, "not-created.md"))
	if exists || !errors.Is(err, ErrPathOutsideRoot) {
		t.Fatalf("resolvePathAllowMissing() = exists %v, err %v; want escape error", exists, err)
	}
}
