package watcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func TestWatcherDebouncesTaskChanges(t *testing.T) {
	root := t.TempDir()
	taskDir := filepath.Join(root, ".trellis", "tasks", "07-10-demo")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	taskFile := filepath.Join(taskDir, "task.json")
	if err := os.WriteFile(taskFile, []byte(`{"id":"demo"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	taskFile = filepath.Join(root, ".trellis", "tasks", "07-10-demo", "task.json")

	changed := make(chan struct{}, 2)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	w := NewWithChanges(root, 40*time.Millisecond, 100*time.Millisecond, func(_ context.Context, changes ChangeSet) {
		if !changes.Tasks {
			t.Errorf("任务变化分类错误: %+v", changes)
		}
		changed <- struct{}{}
	}, func(err error) {
		t.Errorf("监听异常: %v", err)
	})
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	// fsnotify 没有 ready 回调，给目录注册一个很短的初始化窗口。
	time.Sleep(80 * time.Millisecond)
	if err := os.WriteFile(taskFile, []byte(`{"id":"demo","status":"in_progress"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	case err := <-done:
		t.Fatalf("监听器提前退出: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("未收到文件变化回调")
	}
}

func TestPollDelayLimitsDutyCycle(t *testing.T) {
	if got := pollDelay(time.Second); got != minPollInterval {
		t.Fatalf("快速扫描间隔 = %v，期望 %v", got, minPollInterval)
	}
	if got := pollDelay(12 * time.Second); got != 12*time.Second {
		t.Fatalf("慢扫描间隔 = %v，期望按耗时自适应", got)
	}
}

func TestRunPollingUsesExternalRefreshTrigger(t *testing.T) {
	root := t.TempDir()
	taskDir := filepath.Join(root, ".trellis", "tasks", "demo")
	for _, relative := range []string{taskDir, filepath.Join(root, ".trellis", ".runtime"), filepath.Join(root, ".trellis", "spec")} {
		if err := os.MkdirAll(relative, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	taskFile := filepath.Join(taskDir, "task.json")
	if err := os.WriteFile(taskFile, []byte(`{"id":"demo"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	trigger := make(chan struct{}, 1)
	changed := make(chan ChangeSet, 1)
	w := NewWithChanges(root, time.Millisecond, time.Millisecond, func(_ context.Context, changes ChangeSet) {
		changed <- changes
	}, func(err error) { t.Errorf("轮询错误: %v", err) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.runPolling(ctx, trigger) }()
	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(taskFile, []byte(`{"id":"demo","status":"review"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
		t.Fatal("外部 ticker 未触发时不应自行轮询")
	case <-time.After(30 * time.Millisecond):
	}
	trigger <- struct{}{}
	select {
	case changes := <-changed:
		if !changes.Tasks {
			t.Fatalf("外部触发后的变化分类错误: %+v", changes)
		}
	case err := <-done:
		t.Fatalf("轮询提前退出: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("外部刷新信号未触发元数据轮询")
	}
}

func TestClassifyPathsSeparatesResources(t *testing.T) {
	root := t.TempDir()
	changes := classifyPaths(root, []string{
		filepath.Join(root, ".trellis", "tasks", "demo", "task.json"),
		filepath.Join(root, ".trellis", ".runtime", "sessions", "session.json"),
		filepath.Join(root, ".trellis", "spec", "rules.md"),
		filepath.Join(root, ".trellis", "workflow.md"),
	})
	if !changes.Tasks || !changes.Sessions || !changes.Specs || !changes.Workflow || changes.Full {
		t.Fatalf("资源分类错误: %+v", changes)
	}
}

func TestPollKnownMetadataUsesFastPathAndDiscoversNewFiles(t *testing.T) {
	root := t.TempDir()
	for _, relative := range []string{
		filepath.Join(".trellis", "tasks", "demo"),
		filepath.Join(".trellis", ".runtime"),
		filepath.Join(".trellis", "spec"),
	} {
		if err := os.MkdirAll(filepath.Join(root, relative), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	taskJSON := filepath.Join(root, ".trellis", "tasks", "demo", "task.json")
	if err := os.WriteFile(taskJSON, []byte(`{"id":"demo"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	w := NewWithChanges(root, time.Millisecond, time.Second, nil, nil)
	first, err := w.pollMetadata(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(taskJSON, []byte(`{"id":"demo","status":"review"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := w.pollKnownMetadata(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	changes := diffMetadata(root, first, second)
	if !changes.Tasks || changes.Full {
		t.Fatalf("已知文件变化应走任务增量路径: %+v", changes)
	}

	// 新文件会改变父目录元数据；快速路径随后完整遍历一次，将新路径纳入集合。
	design := filepath.Join(root, ".trellis", "tasks", "demo", "design.md")
	if err := os.WriteFile(design, []byte("# Design\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	taskDir := filepath.Dir(design)
	future := time.Now().Add(time.Second)
	if err := os.Chtimes(taskDir, future, future); err != nil {
		t.Fatal(err)
	}
	third, err := w.pollKnownMetadata(context.Background(), second)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := third[".trellis/tasks/demo/design.md"]; !exists {
		t.Fatal("目录结构变化后未发现新增文档")
	}
}

func TestWatcherRejectsTrellisSymlinkOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".trellis")); err != nil {
		t.Skipf("当前文件系统不支持 symlink: %v", err)
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	err = New(root, time.Millisecond, nil, nil).Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "越过项目根目录") {
		t.Fatalf("外链 .trellis 应被拒绝，实际错误: %v", err)
	}
}

func TestWatcherDirectoryBudgetAndCancellation(t *testing.T) {
	native, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	root := t.TempDir()
	w := New(root, time.Millisecond, nil, nil)
	w.watchedDirectories = MaxWatchedDirectories
	if err := w.addRecursive(context.Background(), native, root); !errors.Is(err, ErrWatchLimit) {
		t.Fatalf("监听预算错误 = %v，期望 ErrWatchLimit", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w.watchedDirectories = 0
	if err := w.addRecursive(ctx, native, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消错误 = %v，期望 context.Canceled", err)
	}
}

func TestWatcherEntryBudgetAndResourceRoots(t *testing.T) {
	w := New(t.TempDir(), time.Millisecond, nil, nil)
	w.observedPaths = make(map[string]struct{}, MaxWatchedEntries)
	for index := 0; index < MaxWatchedEntries; index++ {
		w.observedPaths[filepath.Join(w.root, "entry", fmt.Sprintf("%05d", index))] = struct{}{}
	}
	if err := w.observePath(filepath.Join(w.root, "overflow")); !errors.Is(err, ErrWatchLimit) {
		t.Fatalf("文件与目录共享预算错误 = %v", err)
	}
	for _, relative := range []string{"tasks", "spec", ".runtime", filepath.Join(".runtime", "sessions")} {
		if !w.relevant(filepath.Join(w.root, ".trellis", relative)) {
			t.Fatalf("资源根 %q 应触发重扫", relative)
		}
	}
}

func TestForgetPathRemovesNativeDescendantWatches(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	child := filepath.Join(parent, "child")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	native, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	w := New(root, time.Millisecond, nil, nil)
	w.watchedPaths = make(map[string]struct{})
	w.observedPaths = make(map[string]struct{})
	for _, path := range []string{parent, child} {
		if err := w.addDirectory(native, path); err != nil {
			t.Fatal(err)
		}
		w.observedPaths[path] = struct{}{}
	}
	if err := w.forgetPath(native, parent); err != nil {
		t.Fatal(err)
	}
	if len(w.watchedPaths) != 0 || len(w.observedPaths) != 0 || len(native.WatchList()) != 0 {
		t.Fatalf("后代监听未清理: watched=%v observed=%v native=%v", w.watchedPaths, w.observedPaths, native.WatchList())
	}
}

func TestRunLoopContinuesWhenErrorsChannelCloses(t *testing.T) {
	root := t.TempDir()
	native, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("创建原生 watcher: %v", err)
	}
	defer native.Close()

	events := make(chan fsnotify.Event, 1)
	errorsC := make(chan error)
	close(errorsC)
	changed := make(chan struct{}, 1)
	w := New(root, time.Millisecond, func(context.Context) { changed <- struct{}{} }, nil)
	done := make(chan error, 1)
	go func() { done <- w.runLoop(context.Background(), native, events, errorsC) }()

	events <- fsnotify.Event{Name: filepath.Join(root, ".trellis", "workflow.md"), Op: fsnotify.Write}
	select {
	case <-changed:
	case <-time.After(time.Second):
		t.Fatal("Errors channel 关闭后事件循环未继续处理 Events")
	}
	close(events)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("事件循环退出失败: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("两个 channel 均关闭后事件循环未退出")
	}
}

func TestRunLoopContinuesWhenEventsChannelCloses(t *testing.T) {
	native, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("创建原生 watcher: %v", err)
	}
	defer native.Close()

	events := make(chan fsnotify.Event)
	close(events)
	errorsC := make(chan error, 1)
	reported := make(chan error, 1)
	w := New(t.TempDir(), time.Millisecond, nil, func(err error) { reported <- err })
	done := make(chan error, 1)
	go func() { done <- w.runLoop(context.Background(), native, events, errorsC) }()

	errorsC <- errors.New("boom")
	select {
	case err := <-reported:
		if err == nil || err.Error() != "文件监听错误: boom" {
			t.Fatalf("监听错误包装不正确: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Events channel 关闭后事件循环未继续处理 Errors")
	}
	close(errorsC)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("事件循环退出失败: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("两个 channel 均关闭后事件循环未退出")
	}
}
