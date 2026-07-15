package codegraph

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNewSyncCommandUsesFixedArguments(t *testing.T) {
	tests := []struct {
		mode SyncMode
		want []string
	}{
		{mode: SyncModeIncremental, want: []string{"/tmp/codegraph", "sync", "--quiet", "/tmp/project with spaces"}},
		{mode: SyncModeRebuild, want: []string{"/tmp/codegraph", "index", "--force", "--quiet", "/tmp/project with spaces"}},
	}
	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			command := newSyncCommand(context.Background(), "/tmp/codegraph", "/tmp/project with spaces", tt.mode)
			if !reflect.DeepEqual(command.Args, tt.want) {
				t.Fatalf("命令参数 = %#v，期望 %#v", command.Args, tt.want)
			}
		})
	}
}

func TestSyncManagerRejectsInvalidModeAndMissingCLI(t *testing.T) {
	manager := newTestSyncManager("success")
	if _, err := manager.Start("demo", "/tmp/demo", SyncMode("other")); !errors.Is(err, ErrInvalidSyncMode) {
		t.Fatalf("非法 mode 错误 = %v", err)
	}

	manager.lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	if manager.CLIAvailable() {
		t.Fatal("CLI 缺失时不应报告可用")
	}
	if _, err := manager.Start("demo", "/tmp/demo", SyncModeIncremental); !errors.Is(err, ErrCLIUnavailable) {
		t.Fatalf("CLI 缺失错误 = %v", err)
	}
}

func TestSyncManagerTransitionsToTerminalState(t *testing.T) {
	tests := []struct {
		name      string
		behavior  string
		wantState OperationState
		wantText  string
	}{
		{name: "成功", behavior: "success", wantState: OperationSucceeded},
		{name: "失败", behavior: "fail", wantState: OperationFailed, wantText: "fixture failure"},
		{name: "超时", behavior: "block", wantState: OperationFailed, wantText: "超时"},
		{name: "输出受限", behavior: "large", wantState: OperationFailed, wantText: "输出已截断"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := newTestSyncManager(tt.behavior)
			if tt.behavior == "block" {
				manager.timeout = 80 * time.Millisecond
			}
			if _, err := manager.Start("demo", "/tmp/demo", SyncModeIncremental); err != nil {
				t.Fatalf("启动操作: %v", err)
			}
			operation := waitForOperation(t, manager, "demo", "/tmp/demo", tt.wantState)
			if tt.wantText != "" && !strings.Contains(operation.Message, tt.wantText) {
				t.Fatalf("失败消息 = %q，期望包含 %q", operation.Message, tt.wantText)
			}
			if operation.FinishedAt == nil {
				t.Fatal("终态缺少 finishedAt")
			}
		})
	}
}

func TestSyncManagerSerializesOnlySameProject(t *testing.T) {
	manager := newTestSyncManager("block")
	manager.timeout = 120 * time.Millisecond
	if _, err := manager.Start("one", "/tmp/one", SyncModeIncremental); err != nil {
		t.Fatalf("启动项目 one: %v", err)
	}
	if _, err := manager.Start("one", "/tmp/one", SyncModeRebuild); !errors.Is(err, ErrSyncConflict) {
		t.Fatalf("同项目重复启动错误 = %v", err)
	}
	if _, err := manager.Start("two", "/tmp/two", SyncModeRebuild); err != nil {
		t.Fatalf("不同项目应允许并发: %v", err)
	}
	if operation := manager.Operation("two", "/tmp/two"); operation == nil || operation.Mode != SyncModeRebuild {
		t.Fatalf("项目 two 状态 = %#v", operation)
	}
	waitForOperation(t, manager, "one", "/tmp/one", OperationFailed)
	waitForOperation(t, manager, "two", "/tmp/two", OperationFailed)
}

func TestSyncManagerContinuesAfterStartReturns(t *testing.T) {
	manager := newTestSyncManager("delay")
	operation, err := manager.Start("demo", "/tmp/demo", SyncModeIncremental)
	if err != nil {
		t.Fatalf("启动操作: %v", err)
	}
	if operation.State != OperationRunning {
		t.Fatalf("Start 应立即返回运行态，实际 %s", operation.State)
	}
	// Start 不接收 HTTP Context；调用方返回后，后台命令仍应独立进入终态。
	waitForOperation(t, manager, "demo", "/tmp/demo", OperationSucceeded)
}

func newTestSyncManager(behavior string) *SyncManager {
	manager := NewSyncManager(slog.New(slog.NewTextHandler(io.Discard, nil)))
	manager.lookPath = func(string) (string, error) { return os.Args[0], nil }
	manager.commandFactory = func(ctx context.Context, _, _ string, _ SyncMode) *exec.Cmd {
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCodeGraphSyncHelper$", "--", behavior)
		command.Env = append(os.Environ(), "GO_WANT_CODEGRAPH_SYNC_HELPER=1")
		return command
	}
	return manager
}

func waitForOperation(t *testing.T, manager *SyncManager, projectID, root string, state OperationState) Operation {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		operation := manager.Operation(projectID, root)
		if operation != nil && operation.State == state {
			return *operation
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待 CodeGraph 操作进入 %s 超时，当前状态 %#v", state, manager.Operation(projectID, root))
	return Operation{}
}

func TestCodeGraphSyncHelper(t *testing.T) {
	if os.Getenv("GO_WANT_CODEGRAPH_SYNC_HELPER") != "1" {
		return
	}
	behavior := os.Args[len(os.Args)-1]
	switch behavior {
	case "success":
		os.Exit(0)
	case "fail":
		_, _ = fmt.Fprint(os.Stderr, "fixture failure")
		os.Exit(2)
	case "large":
		_, _ = fmt.Fprint(os.Stderr, strings.Repeat("x", maxSyncOutputBytes+100))
		os.Exit(2)
	case "block":
		time.Sleep(10 * time.Second)
		os.Exit(0)
	case "delay":
		time.Sleep(80 * time.Millisecond)
		os.Exit(0)
	default:
		os.Exit(3)
	}
}
