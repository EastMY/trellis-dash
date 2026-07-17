package gitstate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yunnnn/trellis-dash/internal/model"
)

const (
	// MaxDiffBytes 是接口允许返回的硬上限，避免大文件 Diff 占满服务内存。
	MaxDiffBytes int64 = 2 * 1024 * 1024
	// MaxStatusBytes 限制单个 worktree 的 status 输出；超限时拒绝不完整快照。
	MaxStatusBytes int64 = 4 * 1024 * 1024
	// MaxNumStatBytes 限制相对 HEAD 的行数统计输出，避免超大仓库产生无界内存占用。
	MaxNumStatBytes int64 = 4 * 1024 * 1024
	// MaxWorktreeListBytes 限制 worktree list 输出，避免异常仓库产生无界内存占用。
	MaxWorktreeListBytes int64 = 1024 * 1024
	// MaxCommitOutputBytes 限制提交列表与趋势统计的格式化输出总大小。
	MaxCommitOutputBytes int64 = 1024 * 1024
	// MaxPushOutputBytes 限制远端返回内容，避免异常服务端输出占满进程内存。
	MaxPushOutputBytes int64 = 1024 * 1024
	// MaxWorktrees 限制单次快照需要派生的 Git 子进程总数。
	MaxWorktrees = 128

	defaultTimeout     = 5 * time.Second
	defaultPushTimeout = 20 * time.Second
	defaultCommitLimit = 20
	maxCommitLimit     = 100
	maxErrorBytes      = 64 * 1024
	maxRefOutputBytes  = 64 * 1024
)

var (
	// ErrGitOutputTooLarge 表示任一 Git 命令的标准输出超过安全上限。
	ErrGitOutputTooLarge = errors.New("Git 命令输出超过大小上限")
	// ErrDiffTooLarge 表示 Git 输出超过 Dashboard 可安全展示的上限。
	ErrDiffTooLarge = fmt.Errorf("Git Diff 超过大小上限: %w", ErrGitOutputTooLarge)
	// ErrPathOutsideRoot 表示调用方传入了越过项目根目录的路径。
	ErrPathOutsideRoot = errors.New("路径越过项目根目录")
	// ErrDetachedHead 表示当前 HEAD 未关联本地分支，不能安全推送。
	ErrDetachedHead = errors.New("当前处于 detached HEAD，无法推送")
	// ErrNoUpstream 表示当前分支没有已配置的上游，接口不会自动创建上游。
	ErrNoUpstream = errors.New("当前分支未配置上游分支")
)

// Inspector 通过系统 Git 读取仓库状态，并仅在显式调用 Push 时写入远端仓库。
type Inspector struct {
	timeout        time.Duration
	pushTimeout    time.Duration
	maxDiffBytes   int64
	commandFactory gitCommandFactory
	semaphore      chan struct{}
}

type gitCommandFactory func(context.Context, string, ...string) *exec.Cmd

// NewInspector 创建 Git 检查器。Diff 上限始终不会超过 2 MiB。
func NewInspector(timeout time.Duration, maxDiffBytes int64) *Inspector {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	if maxDiffBytes <= 0 || maxDiffBytes > MaxDiffBytes {
		maxDiffBytes = MaxDiffBytes
	}

	return &Inspector{
		timeout:        timeout,
		pushTimeout:    defaultPushTimeout,
		maxDiffBytes:   maxDiffBytes,
		commandFactory: newGitCommand,
		semaphore:      make(chan struct{}, 4),
	}
}

// Snapshot 返回工作区、分支和 worktree 的一次稳定快照。
//
// 非 Git 目录会同时返回带 Error 的快照和非空 error，HTTP 层因此可以保留
// 项目卡片并直接展示原因，而不是让一次轮询导致服务崩溃。
func (i *Inspector) Snapshot(ctx context.Context, projectID, root string) (model.GitSnapshot, error) {
	snapshot := model.GitSnapshot{
		ProjectID: projectID,
		Files:     make([]model.GitFile, 0),
		Worktrees: make([]model.Worktree, 0),
		UpdatedAt: time.Now().UTC(),
	}

	cleanRoot, err := normalizeRoot(root)
	if err != nil {
		return snapshotWithError(snapshot, err)
	}
	// timeout 覆盖整个快照，而不是让每个 worktree 各自重新获得一份超时预算。
	snapshotContext, cancel := context.WithTimeout(ctx, i.timeout)
	defer cancel()
	ctx = snapshotContext

	statusOutput, err := i.runLimited(
		ctx,
		cleanRoot,
		"读取 Git 状态",
		MaxStatusBytes,
		ErrGitOutputTooLarge,
		"status", "--porcelain=v2", "--branch", "--untracked-files=all", "-z",
	)
	if err != nil {
		return snapshotWithError(snapshot, err)
	}
	if err := parseStatus(statusOutput, &snapshot); err != nil {
		return snapshotWithError(snapshot, fmt.Errorf("解析 Git 状态失败: %w", err))
	}
	snapshot.LinesAdded, snapshot.LinesDeleted, err = i.lineStats(ctx, cleanRoot, snapshot.Head, snapshot.Files)
	if err != nil {
		return snapshotWithError(snapshot, err)
	}

	worktreeOutput, err := i.runLimited(
		ctx,
		cleanRoot,
		"读取 Git Worktree",
		MaxWorktreeListBytes,
		ErrGitOutputTooLarge,
		"worktree", "list", "--porcelain",
	)
	if err != nil {
		return snapshotWithError(snapshot, err)
	}
	snapshot.Worktrees, err = parseWorktrees(worktreeOutput)
	if err != nil {
		return snapshotWithError(snapshot, fmt.Errorf("解析 Git Worktree 失败: %w", err))
	}
	if len(snapshot.Worktrees) > MaxWorktrees {
		return snapshotWithError(snapshot, fmt.Errorf("%w: Worktree 超过 %d 个", ErrGitOutputTooLarge, MaxWorktrees))
	}
	if err := i.populateWorktreeDirty(ctx, cleanRoot, &snapshot); err != nil {
		return snapshotWithError(snapshot, err)
	}

	sortSnapshot(&snapshot)
	snapshot.Hash = snapshotHash(snapshot)
	return snapshot, nil
}

// lineStats 汇总已跟踪变化和未跟踪文件相对空文件的文本行变化。
func (i *Inspector) lineStats(
	ctx context.Context,
	root, head string,
	files []model.GitFile,
) (int, int, error) {
	var outputs []byte
	if head != "" {
		// 直接相对 HEAD 计算工作树，合并暂存与未暂存后的净变化不会重复计数。
		trackedOutput, err := i.runLimited(
			ctx,
			root,
			"读取 Git 行数统计",
			MaxNumStatBytes,
			ErrGitOutputTooLarge,
			"diff", "--no-ext-diff", "--no-textconv", "--numstat", "-z", "HEAD", "--",
		)
		if err != nil {
			return 0, 0, err
		}
		outputs = append(outputs, trackedOutput...)
	}

	untrackedPathspec := make([]byte, 0)
	for _, file := range files {
		if !file.Untracked {
			continue
		}
		untrackedPathspec = append(untrackedPathspec, file.Path...)
		untrackedPathspec = append(untrackedPathspec, 0)
	}
	if len(untrackedPathspec) > 0 {
		untrackedOutput, err := i.untrackedNumStat(ctx, root, untrackedPathspec)
		if err != nil {
			return 0, 0, err
		}
		outputs = append(outputs, untrackedOutput...)
	}

	added, deleted, err := parseNumStat(outputs)
	if err != nil {
		return 0, 0, fmt.Errorf("解析 Git 行数统计失败: %w", err)
	}
	return added, deleted, nil
}

// untrackedNumStat 使用隔离的临时 index 标记 intent-to-add，让 Git 自身负责
// 文本、二进制和行数语义；真实仓库 index 不会被 observer 修改。
func (i *Inspector) untrackedNumStat(ctx context.Context, root string, pathspec []byte) ([]byte, error) {
	indexDirectory, err := os.MkdirTemp("", "trellis-dashboard-git-index-")
	if err != nil {
		return nil, fmt.Errorf("创建 Git 临时索引失败: %w", err)
	}
	defer os.RemoveAll(indexDirectory)

	environment := []string{"GIT_INDEX_FILE=" + filepath.Join(indexDirectory, "index")}
	if _, err := i.runLimitedCommand(
		ctx,
		i.timeout,
		root,
		"准备未跟踪文件行数统计",
		maxRefOutputBytes,
		ErrGitOutputTooLarge,
		pathspec,
		environment,
		"add", "--intent-to-add", "--pathspec-from-file=-", "--pathspec-file-nul",
	); err != nil {
		return nil, err
	}

	return i.runLimitedCommand(
		ctx,
		i.timeout,
		root,
		"读取未跟踪文件行数统计",
		MaxNumStatBytes,
		ErrGitOutputTooLarge,
		nil,
		environment,
		"diff", "--no-ext-diff", "--no-textconv", "--numstat", "-z", "--",
	)
}

// Commits 返回最近提交，limit 缺省为 20，并限制为最多 100 条。
func (i *Inspector) Commits(ctx context.Context, root string, limit int) ([]model.GitCommit, error) {
	cleanRoot, err := normalizeRoot(root)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultCommitLimit
	}
	if limit > maxCommitLimit {
		limit = maxCommitLimit
	}

	// 用不可见分隔符传输字段，避免作者名、主题中的普通空格破坏解析。
	const format = "%H%x1f%h%x1f%an%x1f%ae%x1f%aI%x1f%s%x1e"
	output, err := i.runLimited(
		ctx,
		cleanRoot,
		"读取 Git 提交",
		MaxCommitOutputBytes,
		ErrGitOutputTooLarge,
		"log",
		"--no-show-signature",
		"--no-decorate",
		"-n",
		strconv.Itoa(limit),
		"--format="+format,
	)
	if err != nil {
		return nil, err
	}

	return parseCommits(output)
}

// DailyCommitCounts 统计当前 HEAD 可达提交在指定自然日范围内的每日数量。
// Git 输出使用提交时间，再统一转换到服务端时区，确保与首页任务趋势共用日期轴。
func (i *Inspector) DailyCommitCounts(
	ctx context.Context,
	root, startDate, endDate string,
	location *time.Location,
) ([]model.DailyCompletion, error) {
	cleanRoot, err := normalizeRoot(root)
	if err != nil {
		return nil, err
	}
	if location == nil {
		location = time.Local
	}
	start, err := time.ParseInLocation("2006-01-02", startDate, location)
	if err != nil {
		return nil, fmt.Errorf("解析 Git 趋势开始日期: %w", err)
	}
	end, err := time.ParseInLocation("2006-01-02", endDate, location)
	if err != nil {
		return nil, fmt.Errorf("解析 Git 趋势结束日期: %w", err)
	}
	if start.After(end) {
		return []model.DailyCompletion{}, nil
	}

	output, err := i.runLimited(
		ctx,
		cleanRoot,
		"读取 Git 提交趋势",
		MaxCommitOutputBytes,
		ErrGitOutputTooLarge,
		"log",
		"--no-show-signature",
		"--no-decorate",
		"--since="+start.Format(time.RFC3339),
		"--until="+end.AddDate(0, 0, 1).Format(time.RFC3339),
		"--format=%cI",
	)
	if err != nil {
		return nil, err
	}

	counts := make(map[string]int)
	for _, value := range strings.Fields(string(output)) {
		createdAt, parseErr := time.Parse(time.RFC3339, value)
		if parseErr != nil {
			return nil, fmt.Errorf("解析 Git 提交时间 %q: %w", value, parseErr)
		}
		date := createdAt.In(location).Format("2006-01-02")
		if date >= startDate && date <= endDate {
			counts[date]++
		}
	}

	items := make([]model.DailyCompletion, 0, int(end.Sub(start).Hours()/24)+1)
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		date := day.Format("2006-01-02")
		items = append(items, model.DailyCompletion{Date: date, Count: counts[date]})
	}
	return items, nil
}

// Diff 返回工作区或暂存区的统一 Diff。path 为空时返回整个项目的 Diff。
func (i *Inspector) Diff(ctx context.Context, root, path string, staged bool) (string, error) {
	cleanRoot, err := normalizeRoot(root)
	if err != nil {
		return "", err
	}

	cleanPath, err := validateRelativePath(cleanRoot, path)
	if err != nil {
		return "", err
	}

	args := []string{
		"diff",
		"--no-ext-diff",
		"--no-textconv",
		"--no-color",
		"--src-prefix=a/",
		"--dst-prefix=b/",
	}
	if staged {
		args = append(args, "--cached")
	}
	if cleanPath != "" {
		// -- 将路径固定为 pathspec 参数，防止文件名被解释为 Git 选项。
		args = append(args, "--", filepath.ToSlash(cleanPath))
	}

	output, err := i.runLimited(ctx, cleanRoot, "读取 Git Diff", i.maxDiffBytes, ErrDiffTooLarge, args...)
	if err != nil {
		return "", err
	}
	return string(output), nil
}

// Push 将当前分支精确推送到它已经配置的上游，不自动创建或改写上游配置。
// 工作区中的未提交内容不会进入推送，行为与原生 git push 保持一致。
func (i *Inspector) Push(ctx context.Context, root string) (string, string, error) {
	cleanRoot, err := normalizeRoot(root)
	if err != nil {
		return "", "", err
	}

	branchOutput, err := i.runLimited(
		ctx,
		cleanRoot,
		"读取当前 Git 分支",
		maxRefOutputBytes,
		ErrGitOutputTooLarge,
		"branch", "--show-current",
	)
	if err != nil {
		return "", "", err
	}
	branch := strings.TrimSpace(string(branchOutput))
	if branch == "" {
		return "", "", ErrDetachedHead
	}

	// 同时读取远端名、远端 ref 与可展示名称，避免受 push.default 等全局配置影响。
	upstreamOutput, err := i.runLimited(
		ctx,
		cleanRoot,
		"读取 Git 上游分支",
		maxRefOutputBytes,
		ErrGitOutputTooLarge,
		"for-each-ref",
		"--format=%(upstream:remotename)%00%(upstream:remoteref)%00%(upstream:short)",
		"--count=1",
		"refs/heads/"+branch,
	)
	if err != nil {
		return branch, "", err
	}
	parts := strings.Split(strings.TrimRight(string(upstreamOutput), "\r\n"), "\x00")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return branch, "", ErrNoUpstream
	}
	remote, remoteRef, upstream := parts[0], parts[1], parts[2]

	_, err = i.runLimitedWithTimeout(
		ctx,
		i.pushTimeout,
		cleanRoot,
		"推送 Git 分支",
		MaxPushOutputBytes,
		ErrGitOutputTooLarge,
		"push", "--porcelain", "--", remote, "HEAD:"+remoteRef,
	)
	if err != nil {
		return branch, upstream, err
	}
	return branch, upstream, nil
}

func (i *Inspector) runLimited(
	ctx context.Context,
	root string,
	operation string,
	limit int64,
	limitError error,
	args ...string,
) ([]byte, error) {
	return i.runLimitedCommand(ctx, i.timeout, root, operation, limit, limitError, nil, nil, args...)
}

func (i *Inspector) runLimitedWithTimeout(
	ctx context.Context,
	timeout time.Duration,
	root string,
	operation string,
	limit int64,
	limitError error,
	args ...string,
) ([]byte, error) {
	return i.runLimitedCommand(ctx, timeout, root, operation, limit, limitError, nil, nil, args...)
}

func (i *Inspector) runLimitedCommand(
	ctx context.Context,
	timeout time.Duration,
	root string,
	operation string,
	limit int64,
	limitError error,
	input []byte,
	environment []string,
	args ...string,
) ([]byte, error) {
	commandContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if i.semaphore != nil {
		select {
		case i.semaphore <- struct{}{}:
			defer func() { <-i.semaphore }()
		case <-commandContext.Done():
			return nil, commandError(operation, commandContext, commandContext.Err(), nil)
		}
	}

	cmd := i.newCommand(commandContext, root, args...)
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	if len(environment) > 0 {
		cmd.Env = append(cmd.Env, environment...)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("%s失败: 创建输出管道: %w", operation, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("%s失败: 创建错误管道: %w", operation, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, commandError(operation, commandContext, err, nil)
	}

	type readResult struct {
		data []byte
		err  error
	}
	stdoutDone := make(chan readResult, 1)
	stderrDone := make(chan readResult, 1)

	go func() {
		var output bytes.Buffer
		_, readErr := io.Copy(&output, io.LimitReader(stdout, limit+1))
		stdoutDone <- readResult{data: output.Bytes(), err: readErr}
	}()
	go func() {
		var output limitedBuffer
		output.limit = maxErrorBytes
		_, readErr := io.Copy(&output, stderr)
		stderrDone <- readResult{data: output.Bytes(), err: readErr}
	}()

	stdoutResult := <-stdoutDone
	if int64(len(stdoutResult.data)) > limit {
		// 先终止仍在继续产出的命令，再回收进程和 stderr 读取协程。
		_ = cmd.Process.Kill()
		<-stderrDone
		_ = cmd.Wait()
		if limitError == nil {
			limitError = ErrGitOutputTooLarge
		}
		return nil, fmt.Errorf("%s失败: %w（上限 %d 字节）", operation, limitError, limit)
	}

	stderrResult := <-stderrDone
	// StdoutPipe/StderrPipe 要求读取完成后再 Wait，避免关闭管道时截断输出。
	waitErr := cmd.Wait()
	if stdoutResult.err != nil {
		return nil, fmt.Errorf("%s失败: 读取输出: %w", operation, stdoutResult.err)
	}
	if stderrResult.err != nil {
		return nil, fmt.Errorf("%s失败: 读取错误输出: %w", operation, stderrResult.err)
	}
	if waitErr != nil {
		return nil, commandError(operation, commandContext, waitErr, stderrResult.data)
	}
	return stdoutResult.data, nil
}

func (i *Inspector) newCommand(ctx context.Context, root string, args ...string) *exec.Cmd {
	if i.commandFactory != nil {
		return i.commandFactory(ctx, root, args...)
	}
	return newGitCommand(ctx, root, args...)
}

// populateWorktreeDirty 为每个可用 worktree 填充 Dirty。
// 主 worktree 直接复用已经解析的完整 status，其他 worktree 只读取轻量 porcelain。
func (i *Inspector) populateWorktreeDirty(ctx context.Context, cleanRoot string, snapshot *model.GitSnapshot) error {
	type dirtyResult struct {
		dirty bool
		err   error
	}
	results := make([]dirtyResult, len(snapshot.Worktrees))
	jobs := make([]int, 0, len(snapshot.Worktrees))

	// 路径校验和无需采集的 worktree 先按稳定顺序处理，错误行为不受 goroutine 调度影响。
	for index := range snapshot.Worktrees {
		worktree := &snapshot.Worktrees[index]
		if worktree.Bare || worktree.Prunable != "" {
			// bare 和已可清理 worktree 没有可读取的工作区，Dirty 明确为 false。
			worktree.Dirty = false
			continue
		}
		if !filepath.IsAbs(worktree.Path) {
			return fmt.Errorf("校验 Git Worktree %q 失败: 路径必须为绝对路径", worktree.Path)
		}

		cleanWorktree, err := normalizeRoot(worktree.Path)
		if err != nil {
			return fmt.Errorf("校验 Git Worktree %q 失败: %w", worktree.Path, err)
		}
		worktree.Path = cleanWorktree
		if cleanWorktree == cleanRoot {
			worktree.Dirty = snapshot.Dirty
			continue
		}
		jobs = append(jobs, index)
	}

	if len(jobs) == 0 {
		return nil
	}
	workerCount := 4
	if i.semaphore != nil && cap(i.semaphore) > 0 && cap(i.semaphore) < workerCount {
		workerCount = cap(i.semaphore)
	}
	if len(jobs) < workerCount {
		workerCount = len(jobs)
	}
	jobQueue := make(chan int)
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for index := range jobQueue {
				worktree := snapshot.Worktrees[index]
				output, err := i.runLimited(
					ctx,
					worktree.Path,
					fmt.Sprintf("读取 Git Worktree %q 状态", worktree.Path),
					MaxStatusBytes,
					ErrGitOutputTooLarge,
					"status", "--porcelain=v2", "--untracked-files=normal", "-z",
				)
				results[index] = dirtyResult{dirty: len(output) > 0, err: err}
			}
		}()
	}
	for _, index := range jobs {
		jobQueue <- index
	}
	close(jobQueue)
	workers.Wait()

	// 统一按 worktree 原顺序提交结果，保证错误与输出稳定可复现。
	for _, index := range jobs {
		if results[index].err != nil {
			return results[index].err
		}
		snapshot.Worktrees[index].Dirty = results[index].dirty
	}
	return nil
}

func newGitCommand(ctx context.Context, root string, args ...string) *exec.Cmd {
	commandArgs := make([]string, 0, len(args)+7)
	// 禁用 fsmonitor，避免只读状态采集触发仓库 hook 或启动内置 daemon。
	// safe.directory 只在当前命令作用域信任已经注册并规范化的项目/Worktree，
	// 让非 root 容器也能读取只读 bind mount，不改写用户或系统 Git 配置。
	commandArgs = append(commandArgs, "--no-pager", "-c", "core.fsmonitor=false", "-c", "safe.directory="+root, "-C", root)
	commandArgs = append(commandArgs, args...)

	cmd := exec.CommandContext(ctx, "git", commandArgs...)
	cmd.Env = append(
		sanitizedGitEnvironment(),
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_LITERAL_PATHSPECS=1",
		"LC_ALL=C",
	)
	return cmd
}

func sanitizedGitEnvironment() []string {
	blocked := map[string]struct{}{
		"GIT_DIR": {}, "GIT_WORK_TREE": {}, "GIT_INDEX_FILE": {},
		"GIT_OBJECT_DIRECTORY": {}, "GIT_ALTERNATE_OBJECT_DIRECTORIES": {},
		"GIT_COMMON_DIR": {}, "GIT_CEILING_DIRECTORIES": {},
		"GIT_DISCOVERY_ACROSS_FILESYSTEM": {}, "GIT_EXEC_PATH": {},
		"GIT_CONFIG": {}, "GIT_CONFIG_PARAMETERS": {}, "GIT_CONFIG_COUNT": {},
		"GIT_EXTERNAL_DIFF": {}, "GIT_DIFF_OPTS": {}, "GIT_PAGER": {},
		"GIT_OPTIONAL_LOCKS": {}, "GIT_TERMINAL_PROMPT": {},
		"GIT_LITERAL_PATHSPECS": {}, "LC_ALL": {},
	}
	environment := make([]string, 0, len(os.Environ()))
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if _, found := blocked[key]; found || strings.HasPrefix(key, "GIT_CONFIG_KEY_") || strings.HasPrefix(key, "GIT_CONFIG_VALUE_") {
			continue
		}
		environment = append(environment, item)
	}
	return environment
}

func commandError(operation string, ctx context.Context, err error, stderr []byte) error {
	if contextErr := ctx.Err(); contextErr != nil {
		if errors.Is(contextErr, context.DeadlineExceeded) {
			return fmt.Errorf("%s超时: %w", operation, contextErr)
		}
		return fmt.Errorf("%s已取消: %w", operation, contextErr)
	}

	detail := strings.TrimSpace(string(stderr))
	if detail == "" {
		return fmt.Errorf("%s失败: %w", operation, err)
	}
	return fmt.Errorf("%s失败: %s: %w", operation, detail, err)
}

func normalizeRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("项目根目录不能为空")
	}

	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("解析项目根目录失败: %w", err)
	}
	absolute = filepath.Clean(absolute)
	realPath, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("解析项目根目录真实路径失败: %w", err)
	}
	realPath = filepath.Clean(realPath)
	if realPath != absolute {
		return "", fmt.Errorf("项目根目录真实路径已变化: 注册=%s 当前=%s", absolute, realPath)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("读取项目根目录失败: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("项目根目录不是目录: %s", absolute)
	}
	return absolute, nil
}

func validateRelativePath(root, path string) (string, error) {
	if path == "" || path == "." {
		return "", nil
	}
	if strings.IndexByte(path, 0) >= 0 {
		return "", fmt.Errorf("%w: 路径包含空字符", ErrPathOutsideRoot)
	}
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("%w: 不允许绝对路径 %q", ErrPathOutsideRoot, path)
	}

	cleanPath := filepath.Clean(path)
	candidate := filepath.Join(root, cleanPath)
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return "", fmt.Errorf("校验 Diff 路径失败: %w", err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q", ErrPathOutsideRoot, path)
	}
	return relative, nil
}

func snapshotWithError(snapshot model.GitSnapshot, err error) (model.GitSnapshot, error) {
	snapshot.Error = err.Error()
	// 错误快照也必须有稳定 Hash，才能进入 SQLite 并在前端持续展示。
	sortSnapshot(&snapshot)
	snapshot.Hash = snapshotHash(snapshot)
	return snapshot, err
}

// limitedBuffer 会继续消费超限内容但只保留前 limit 字节，防止 stderr 堵塞子进程。
type limitedBuffer struct {
	buffer bytes.Buffer
	limit  int64
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	originalLength := len(data)
	remaining := b.limit - int64(b.buffer.Len())
	if remaining > 0 {
		if int64(len(data)) > remaining {
			data = data[:remaining]
		}
		_, _ = b.buffer.Write(data)
	}
	return originalLength, nil
}

func (b *limitedBuffer) Bytes() []byte {
	return b.buffer.Bytes()
}
