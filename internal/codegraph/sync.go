package codegraph

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	ErrCLIUnavailable  = errors.New("未检测到 CodeGraph CLI")
	ErrSyncConflict    = errors.New("当前项目已有 CodeGraph 操作正在执行")
	ErrInvalidSyncMode = errors.New("CodeGraph 同步模式无效")
)

const (
	defaultSyncTimeout  = 30 * time.Minute
	maxSyncOutputBytes  = 16 * 1024
	maxSyncMessageRunes = 500
)

type syncCommandFactory func(ctx context.Context, executable, projectRoot string, mode SyncMode) *exec.Cmd
type lookPathFunc func(file string) (string, error)

// SyncManager 只允许用户显式触发固定的同步命令，并按项目隔离运行状态。
type SyncManager struct {
	mu             sync.RWMutex
	operations     map[string]Operation
	logger         *slog.Logger
	timeout        time.Duration
	now            func() time.Time
	lookPath       lookPathFunc
	commandFactory syncCommandFactory
}

func NewSyncManager(logger *slog.Logger) *SyncManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &SyncManager{
		operations:     make(map[string]Operation),
		logger:         logger,
		timeout:        defaultSyncTimeout,
		now:            time.Now,
		lookPath:       exec.LookPath,
		commandFactory: newSyncCommand,
	}
}

func newSyncCommand(ctx context.Context, executable, projectRoot string, mode SyncMode) *exec.Cmd {
	args := []string{"sync", "--quiet", projectRoot}
	if mode == SyncModeRebuild {
		args = []string{"index", "--force", "--quiet", projectRoot}
	}
	return exec.CommandContext(ctx, executable, args...)
}

func syncOperationKey(projectID, projectRoot string) string {
	return projectID + "\x00" + filepath.Clean(projectRoot)
}

func validSyncMode(mode SyncMode) bool {
	return mode == SyncModeIncremental || mode == SyncModeRebuild
}

// CLIAvailable 每次从当前服务环境探测 CLI，避免把启动时状态永久缓存。
func (m *SyncManager) CLIAvailable() bool {
	_, err := m.lookPath("codegraph")
	return err == nil
}

// Operation 返回状态副本，调用方不能修改管理器内部状态。
func (m *SyncManager) Operation(projectID, projectRoot string) *Operation {
	m.mu.RLock()
	operation, ok := m.operations[syncOperationKey(projectID, projectRoot)]
	m.mu.RUnlock()
	if !ok {
		return nil
	}
	return &operation
}

// Start 在请求生命周期内登记任务，再用独立的有界 Context 执行命令。
func (m *SyncManager) Start(projectID, projectRoot string, mode SyncMode) (Operation, error) {
	if !validSyncMode(mode) {
		return Operation{}, ErrInvalidSyncMode
	}
	executable, err := m.lookPath("codegraph")
	if err != nil {
		return Operation{}, ErrCLIUnavailable
	}

	key := syncOperationKey(projectID, projectRoot)
	m.mu.Lock()
	if current, ok := m.operations[key]; ok && current.State == OperationRunning {
		m.mu.Unlock()
		return Operation{}, ErrSyncConflict
	}
	operation := Operation{
		Mode:      mode,
		State:     OperationRunning,
		StartedAt: m.now().UTC(),
	}
	m.operations[key] = operation
	m.mu.Unlock()

	go m.run(key, projectID, projectRoot, executable, operation)
	return operation, nil
}

func (m *SyncManager) run(key, projectID, projectRoot, executable string, operation Operation) {
	ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
	defer cancel()

	var output limitedSyncBuffer
	output.limit = maxSyncOutputBytes
	command := m.commandFactory(ctx, executable, projectRoot, operation.Mode)
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()

	finishedAt := m.now().UTC()
	operation.FinishedAt = &finishedAt
	if err == nil {
		operation.State = OperationSucceeded
		m.logger.Info("CodeGraph 操作完成", "project", projectID, "mode", operation.Mode)
	} else {
		operation.State = OperationFailed
		operation.Message = syncFailureMessage(ctx.Err(), err, output.String(), output.truncated)
		m.logger.Warn("CodeGraph 操作失败", "project", projectID, "mode", operation.Mode, "error", err)
	}

	m.mu.Lock()
	// startedAt 是本次运行的身份；只更新仍对应当前运行的记录。
	if current, ok := m.operations[key]; ok && current.StartedAt.Equal(operation.StartedAt) {
		m.operations[key] = operation
	}
	m.mu.Unlock()
}

func syncFailureMessage(contextErr, commandErr error, output string, truncated bool) string {
	if errors.Is(contextErr, context.DeadlineExceeded) {
		return "CodeGraph 操作超时，请检查项目规模或服务日志"
	}
	message := strings.TrimSpace(output)
	if message == "" {
		message = commandErr.Error()
	}
	message = strings.Join(strings.Fields(message), " ")
	runes := []rune(message)
	if len(runes) > maxSyncMessageRunes {
		message = string(runes[:maxSyncMessageRunes]) + "…"
	}
	if truncated {
		message += "（输出已截断）"
	}
	return fmt.Sprintf("CodeGraph 操作失败：%s", message)
}

// limitedSyncBuffer 会持续消费子进程输出，但只在内存保留固定上限。
type limitedSyncBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedSyncBuffer) Write(data []byte) (int, error) {
	originalLength := len(data)
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		if len(data) > remaining {
			b.truncated = true
			data = data[:remaining]
		}
		_, _ = b.buffer.Write(data)
	} else if len(data) > 0 {
		b.truncated = true
	}
	return originalLength, nil
}

func (b *limitedSyncBuffer) String() string { return b.buffer.String() }
