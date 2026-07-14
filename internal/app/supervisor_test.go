package app

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/yunnnn/trellis-dash/internal/model"
	"github.com/yunnnn/trellis-dash/internal/store"
)

type scannerFunc func(context.Context, model.Project) (model.TrellisSnapshot, error)

func (fn scannerFunc) Scan(ctx context.Context, project model.Project) (model.TrellisSnapshot, error) {
	return fn(ctx, project)
}

type inspectorFunc func(context.Context, string, string) (model.GitSnapshot, error)

func (fn inspectorFunc) Snapshot(ctx context.Context, projectID, root string) (model.GitSnapshot, error) {
	return fn(ctx, projectID, root)
}

type controlledScanner struct {
	mu          sync.Mutex
	calls       int
	inFlight    int
	maxInFlight int
	entered     chan int
	gates       map[int]chan struct{}
}

func (scanner *controlledScanner) Scan(ctx context.Context, _ model.Project) (model.TrellisSnapshot, error) {
	scanner.mu.Lock()
	scanner.calls++
	call := scanner.calls
	scanner.inFlight++
	if scanner.inFlight > scanner.maxInFlight {
		scanner.maxInFlight = scanner.inFlight
	}
	gate := scanner.gates[call]
	scanner.mu.Unlock()

	scanner.entered <- call
	defer func() {
		scanner.mu.Lock()
		scanner.inFlight--
		scanner.mu.Unlock()
	}()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return model.TrellisSnapshot{}, ctx.Err()
		}
	}
	return model.TrellisSnapshot{}, ctx.Err()
}

func (scanner *controlledScanner) stats() (calls, maxInFlight int) {
	scanner.mu.Lock()
	defer scanner.mu.Unlock()
	return scanner.calls, scanner.maxInFlight
}

func TestSupervisorCoalescesDirtySignalsAndWaitsForManualRescan(t *testing.T) {
	firstGate := make(chan struct{})
	manualGate := make(chan struct{})
	scanner := &controlledScanner{
		entered: make(chan int, 8),
		gates: map[int]chan struct{}{
			1: firstGate,
			3: manualGate,
		},
	}
	supervisor, project, cleanup := newSupervisorFixture(t, scanner)
	defer cleanup()

	if err := supervisor.Start(context.Background()); err != nil {
		t.Fatalf("启动 Supervisor: %v", err)
	}
	defer supervisor.Stop()
	waitScanCall(t, scanner.entered, 1)

	supervisor.mu.Lock()
	runner := supervisor.runners[project.ID]
	supervisor.mu.Unlock()
	if runner == nil {
		t.Fatal("项目 worker 未注册")
	}
	for range 1_000 {
		runner.markDirty()
	}
	if got := len(runner.dirty); got != 1 {
		t.Fatalf("脏信号应合并为 1 个，实际=%d", got)
	}
	close(firstGate)
	waitScanCall(t, scanner.entered, 2)
	waitFor(t, time.Second, func() bool {
		calls, _ := scanner.stats()
		return calls == 2
	}, "合并后的第二次扫描完成")

	result := make(chan error, 1)
	go func() { result <- supervisor.Rescan(context.Background(), project.ID) }()
	waitScanCall(t, scanner.entered, 3)
	select {
	case err := <-result:
		t.Fatalf("手动扫描尚未完成就提前返回: %v", err)
	case <-time.After(40 * time.Millisecond):
	}
	close(manualGate)
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("手动扫描失败: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("手动扫描完成后未返回")
	}

	calls, maxInFlight := scanner.stats()
	if calls != 3 {
		t.Fatalf("预期首次、合并事件、手动重扫共 3 次，实际=%d", calls)
	}
	if maxInFlight != 1 {
		t.Fatalf("同一项目扫描发生并发，最大并发=%d", maxInFlight)
	}
}

func TestSupervisorStopAndRemoveWaitForWorker(t *testing.T) {
	for _, operation := range []string{"stop", "remove"} {
		t.Run(operation, func(t *testing.T) {
			scanStarted := make(chan struct{})
			scanCanceled := make(chan struct{})
			allowExit := make(chan struct{})
			var once sync.Once
			scanner := scannerFunc(func(ctx context.Context, _ model.Project) (model.TrellisSnapshot, error) {
				once.Do(func() { close(scanStarted) })
				<-ctx.Done()
				close(scanCanceled)
				<-allowExit
				return model.TrellisSnapshot{}, ctx.Err()
			})
			supervisor, project, cleanup := newSupervisorFixture(t, scanner)
			defer cleanup()
			if err := supervisor.Start(context.Background()); err != nil {
				t.Fatalf("启动 Supervisor: %v", err)
			}
			select {
			case <-scanStarted:
			case <-time.After(time.Second):
				t.Fatal("首次扫描未开始")
			}

			finished := make(chan struct{})
			go func() {
				if operation == "stop" {
					supervisor.Stop()
				} else {
					supervisor.Remove(project.ID)
				}
				close(finished)
			}()
			select {
			case <-scanCanceled:
			case <-time.After(time.Second):
				t.Fatal("生命周期操作未取消正在执行的扫描")
			}
			select {
			case <-finished:
				t.Fatal("worker 尚未退出时生命周期操作提前返回")
			case <-time.After(40 * time.Millisecond):
			}
			close(allowExit)
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("worker 退出后生命周期操作仍未返回")
			}
			if operation == "remove" {
				supervisor.Stop()
			}
		})
	}
}

func TestSupervisorRescanRecreatesMissingRunner(t *testing.T) {
	calls := make(chan struct{}, 4)
	scanner := scannerFunc(func(ctx context.Context, _ model.Project) (model.TrellisSnapshot, error) {
		select {
		case calls <- struct{}{}:
		case <-ctx.Done():
			return model.TrellisSnapshot{}, ctx.Err()
		}
		return model.TrellisSnapshot{TasksHash: "tasks", SessionsHash: "sessions", SpecsHash: "specs"}, nil
	})
	supervisor, project, cleanup := newSupervisorFixture(t, scanner)
	defer cleanup()
	if err := supervisor.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("首次扫描未开始")
	}
	supervisor.Remove(project.ID)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- supervisor.Rescan(ctx, project.ID) }()
	// 恢复 runner 会先做首次扫描，再执行显式 rescan。
	for index := 0; index < 2; index++ {
		select {
		case <-calls:
		case <-ctx.Done():
			t.Fatalf("等待恢复扫描 %d 超时: %v", index+1, ctx.Err())
		}
	}
	if err := <-result; err != nil {
		t.Fatalf("缺失 runner 自愈失败: %v", err)
	}
}

func TestMatchChangedTasksUsesMostSpecificDirectory(t *testing.T) {
	tasks := []model.Task{
		{Key: "active", SourcePath: ".trellis/tasks/active/task.json"},
		{Key: "archived", SourcePath: ".trellis/tasks/archive/2026-07/active/task.json"},
	}
	matched, complete := matchChangedTasks([]string{
		".trellis/tasks/archive/2026-07/active/prd.md",
		".trellis/tasks/active/task.json",
	}, tasks)
	if !complete || len(matched) != 2 || matched[0].Key != "active" || matched[1].Key != "archived" {
		t.Fatalf("任务事件映射错误: complete=%v matched=%+v", complete, matched)
	}

	if _, complete := matchChangedTasks([]string{".trellis/tasks/new-task/task.json"}, tasks); complete {
		t.Fatal("新增任务无法复用既有 Key，应回退全量扫描")
	}
}

func newSupervisorFixture(t *testing.T, scanner snapshotScanner) (*Supervisor, model.Project, func()) {
	t.Helper()
	repository, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("打开测试 SQLite: %v", err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		repository.Close()
		t.Fatalf("解析测试目录真实路径: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".trellis"), 0o755); err != nil {
		repository.Close()
		t.Fatalf("创建 Trellis 目录: %v", err)
	}
	project := model.Project{ID: "demo", Name: "Demo", Root: root, Mode: model.ProjectModeObserver}
	if err := repository.UpsertProject(context.Background(), project); err != nil {
		repository.Close()
		t.Fatalf("保存测试项目: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	inspector := inspectorFunc(func(context.Context, string, string) (model.GitSnapshot, error) {
		return model.GitSnapshot{}, nil
	})
	supervisor := newSupervisor(repository, scanner, inspector, logger, SupervisorOptions{
		Debounce:           10 * time.Millisecond,
		RefreshInterval:    time.Hour,
		FullRescanInterval: time.Hour,
	})
	return supervisor, project, func() {
		supervisor.Stop()
		if err := repository.Close(); err != nil {
			t.Errorf("关闭测试 SQLite: %v", err)
		}
	}
}

func waitScanCall(t *testing.T, calls <-chan int, want int) {
	t.Helper()
	select {
	case got := <-calls:
		if got != want {
			t.Fatalf("扫描序号错误: got=%d want=%d", got, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("等待第 %d 次扫描超时", want)
	}
}

func waitFor(t *testing.T, timeout time.Duration, ready func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("等待%s超时", description)
}
