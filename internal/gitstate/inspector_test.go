package gitstate

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yunnnn/trellis-dash/internal/model"
)

func TestInspectorSnapshot(t *testing.T) {
	root := newTestRepository(t)
	worktreeRoot := filepath.Join(t.TempDir(), "feature worktree")
	runGit(t, root, "worktree", "add", "-b", "feature/test", worktreeRoot)
	worktreeRootCanonical := canonicalTestPath(t, worktreeRoot)

	writeTestFile(t, filepath.Join(root, "modified.txt"), "修改后\n")
	if err := os.Remove(filepath.Join(root, "deleted.txt")); err != nil {
		t.Fatalf("删除测试文件: %v", err)
	}
	writeTestFile(t, filepath.Join(root, "staged file.txt"), "已暂存\n")
	runGit(t, root, "add", "--", "staged file.txt")
	writeTestFile(t, filepath.Join(root, "untracked file.txt"), "未跟踪\n")

	inspector := NewInspector(2*time.Second, MaxDiffBytes)
	first, err := inspector.Snapshot(context.Background(), "project-a", root)
	if err != nil {
		t.Fatalf("读取快照: %v", err)
	}

	if first.ProjectID != "project-a" {
		t.Fatalf("项目 ID = %q，期望 project-a", first.ProjectID)
	}
	if first.Branch != "main" {
		t.Fatalf("分支 = %q，期望 main", first.Branch)
	}
	if first.Head == "" {
		t.Fatal("HEAD 不应为空")
	}
	if !first.Dirty {
		t.Fatal("存在工作区变化时 Dirty 应为 true")
	}
	if first.Modified != 1 || first.Added != 1 || first.Deleted != 1 || first.Untracked != 1 || first.Conflicted != 0 {
		t.Fatalf(
			"状态统计不符: modified=%d added=%d deleted=%d untracked=%d conflicted=%d",
			first.Modified,
			first.Added,
			first.Deleted,
			first.Untracked,
			first.Conflicted,
		)
	}
	if first.LinesAdded != 3 || first.LinesDeleted != 2 {
		t.Fatalf("代码行统计不符: added=%d deleted=%d，期望 3/2", first.LinesAdded, first.LinesDeleted)
	}
	if len(first.Files) != 4 {
		t.Fatalf("文件数 = %d，期望 4", len(first.Files))
	}
	if len(first.Worktrees) != 2 {
		t.Fatalf("worktree 数 = %d，期望 2", len(first.Worktrees))
	}
	worktrees := worktreesByPath(first.Worktrees)
	if !worktrees[root].Dirty {
		t.Fatalf("主 worktree 应复用主快照 Dirty: %+v", worktrees[root])
	}
	if worktrees[worktreeRootCanonical].Dirty {
		t.Fatalf("未修改的 feature worktree 不应为 Dirty: %+v", worktrees[worktreeRootCanonical])
	}
	if worktrees[root].TaskKey != "" || worktrees[worktreeRootCanonical].TaskKey != "" {
		t.Fatal("Inspector 不应自行映射 TaskKey")
	}
	if first.Hash == "" {
		t.Fatal("稳定 Hash 不应为空")
	}
	if first.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt 不应为空")
	}

	files := filesByPath(first.Files)
	assertFileStatus(t, files, "modified.txt", "modified", ".", "M")
	assertFileStatus(t, files, "deleted.txt", "deleted", ".", "D")
	assertFileStatus(t, files, "staged file.txt", "added", "A", ".")
	assertFileStatus(t, files, "untracked file.txt", "untracked", "?", "?")

	second, err := inspector.Snapshot(context.Background(), "project-a", root)
	if err != nil {
		t.Fatalf("再次读取快照: %v", err)
	}
	if second.Hash != first.Hash {
		t.Fatalf("状态未变化时 Hash 不稳定: %q != %q", second.Hash, first.Hash)
	}

	writeTestFile(t, filepath.Join(worktreeRoot, "modified.txt"), "feature worktree 修改\n")
	third, err := inspector.Snapshot(context.Background(), "project-a", root)
	if err != nil {
		t.Fatalf("修改其他 worktree 后读取快照: %v", err)
	}
	thirdWorktrees := worktreesByPath(third.Worktrees)
	if !thirdWorktrees[worktreeRootCanonical].Dirty {
		t.Fatalf("其他 worktree 的 Dirty 未填充: %+v", thirdWorktrees[worktreeRootCanonical])
	}
	if third.Hash == second.Hash {
		t.Fatal("其他 worktree Dirty 变化后快照 Hash 应变化")
	}
}

func TestInspectorSnapshotIncludesUntrackedLineStats(t *testing.T) {
	root := newTestRepository(t)
	writeTestFile(t, filepath.Join(root, ".gitignore"), "ignored.txt\n")
	runGit(t, root, "add", "--", ".gitignore")
	runGit(t, root, "commit", "-m", "增加忽略规则")

	writeTestFile(t, filepath.Join(root, "untracked.txt"), "第一行\n第二行")
	writeTestFile(t, filepath.Join(root, "nested", "未跟踪.txt"), "第三行\n")
	writeTestFile(t, filepath.Join(root, "empty.txt"), "")
	if err := os.WriteFile(filepath.Join(root, "binary.dat"), []byte{0, 1, 2, '\n'}, 0o644); err != nil {
		t.Fatalf("写入未跟踪二进制文件: %v", err)
	}
	writeTestFile(t, filepath.Join(root, "ignored.txt"), "不应统计\n不应统计\n")

	snapshot, err := NewInspector(2*time.Second, MaxDiffBytes).Snapshot(context.Background(), "untracked-lines", root)
	if err != nil {
		t.Fatalf("读取未跟踪文件快照: %v", err)
	}
	if snapshot.Untracked != 4 {
		t.Fatalf("未跟踪文件数 = %d，期望 4", snapshot.Untracked)
	}
	if snapshot.LinesAdded != 3 || snapshot.LinesDeleted != 0 {
		t.Fatalf("未跟踪行数统计 = +%d/-%d，期望 +3/-0", snapshot.LinesAdded, snapshot.LinesDeleted)
	}
	if staged := strings.TrimSpace(runGitOutput(t, root, "diff", "--cached", "--name-only", "HEAD", "--")); staged != "" {
		t.Fatalf("行数统计不应修改真实 Git index，发现暂存文件: %q", staged)
	}
}

func TestInspectorSnapshotIncludesUntrackedLinesWithoutHead(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init", "-b", "main")
	writeTestFile(t, filepath.Join(root, "first.txt"), "第一行\n第二行\n")

	canonicalRoot := canonicalTestPath(t, root)
	snapshot, err := NewInspector(2*time.Second, MaxDiffBytes).Snapshot(context.Background(), "initial", canonicalRoot)
	if err != nil {
		t.Fatalf("读取无 HEAD 仓库快照: %v", err)
	}
	if snapshot.Head != "" {
		t.Fatalf("无提交仓库 HEAD = %q，期望为空", snapshot.Head)
	}
	if snapshot.LinesAdded != 2 || snapshot.LinesDeleted != 0 {
		t.Fatalf("无 HEAD 仓库行数统计 = +%d/-%d，期望 +2/-0", snapshot.LinesAdded, snapshot.LinesDeleted)
	}
}

func TestInspectorCollectsSecondaryWorktreesConcurrently(t *testing.T) {
	root := canonicalTestPath(t, t.TempDir())
	paths := []string{root}
	for range 4 {
		paths = append(paths, canonicalTestPath(t, t.TempDir()))
	}
	inspector := NewInspector(15*time.Second, MaxDiffBytes)
	baseFactory := helperGitFactory("", 0, paths)
	started := make(chan string, 4)
	release := make(chan struct{})
	inspector.commandFactory = func(ctx context.Context, commandRoot string, args ...string) *exec.Cmd {
		if len(args) > 0 && args[0] == "status" && commandRoot != root {
			started <- commandRoot
			<-release
		}
		return baseFactory(ctx, commandRoot, args...)
	}

	done := make(chan error, 1)
	go func() {
		_, err := inspector.Snapshot(context.Background(), "parallel-worktrees", root)
		done <- err
	}()
	seen := make(map[string]struct{}, 4)
	for len(seen) < 4 {
		select {
		case path := <-started:
			seen[path] = struct{}{}
		case <-time.After(5 * time.Second):
			close(release)
			t.Fatalf("仅有 %d 个 worktree worker 同时到达命令工厂", len(seen))
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("并发读取 worktree: %v", err)
	}
}

func TestInspectorCommits(t *testing.T) {
	root := newTestRepository(t)
	writeTestFile(t, filepath.Join(root, "modified.txt"), "第二版\n")
	runGit(t, root, "add", "--", "modified.txt")
	runGit(t, root, "commit", "-m", "第二个 提交")

	inspector := NewInspector(2*time.Second, MaxDiffBytes)
	commits, err := inspector.Commits(context.Background(), root, 1)
	if err != nil {
		t.Fatalf("读取最近提交: %v", err)
	}
	if len(commits) != 1 {
		t.Fatalf("提交数 = %d，期望 1", len(commits))
	}
	commit := commits[0]
	if commit.Subject != "第二个 提交" {
		t.Fatalf("提交主题 = %q", commit.Subject)
	}
	if commit.Author != "测试用户" || commit.Email != "test@example.com" {
		t.Fatalf("提交作者解析错误: %q <%s>", commit.Author, commit.Email)
	}
	if commit.Hash == "" || commit.ShortHash == "" || commit.CreatedAt.IsZero() {
		t.Fatalf("提交基础字段不完整: %+v", commit)
	}

	allCommits, err := inspector.Commits(context.Background(), root, 10)
	if err != nil {
		t.Fatalf("读取多条提交: %v", err)
	}
	if len(allCommits) != 2 || allCommits[0].Subject != "第二个 提交" || allCommits[1].Subject != "初始提交" {
		t.Fatalf("多条提交的分隔或顺序错误: %+v", allCommits)
	}
}

func TestInspectorDailyCommitCountsUsesServerTimezoneAndFillsEmptyDays(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init", "-b", "main")
	runGit(t, root, "config", "user.name", "趋势测试用户")
	runGit(t, root, "config", "user.email", "trend@example.com")

	commitAt := func(name, createdAt string) {
		writeTestFile(t, filepath.Join(root, name+".txt"), name+"\n")
		runGit(t, root, "add", "--", name+".txt")
		runGitWithEnv(t, root, []string{
			"GIT_AUTHOR_DATE=" + createdAt,
			"GIT_COMMITTER_DATE=" + createdAt,
		}, "commit", "-m", name)
	}
	commitAt("北京时间提交", "2026-07-10T09:00:00+08:00")
	// 原始时区仍是 7 月 10 日，但换算到服务端 UTC+8 后应归入 7 月 11 日。
	commitAt("跨时区提交", "2026-07-10T23:30:00-04:00")
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("解析测试仓库真实路径: %v", err)
	}

	inspector := NewInspector(2*time.Second, MaxDiffBytes)
	serverLocation := time.FixedZone("UTC+8", 8*60*60)
	items, err := inspector.DailyCommitCounts(
		context.Background(), canonicalRoot, "2026-07-09", "2026-07-12", serverLocation,
	)
	if err != nil {
		t.Fatalf("读取每日提交趋势: %v", err)
	}
	if len(items) != 4 || items[0].Count != 0 || items[1].Count != 1 || items[2].Count != 1 || items[3].Count != 0 {
		t.Fatalf("每日提交趋势异常: %#v", items)
	}
}

func TestInspectorDiff(t *testing.T) {
	root := newTestRepository(t)
	writeTestFile(t, filepath.Join(root, "modified.txt"), "工作区版本\n")

	inspector := NewInspector(2*time.Second, MaxDiffBytes)
	unstaged, err := inspector.Diff(context.Background(), root, "modified.txt", false)
	if err != nil {
		t.Fatalf("读取工作区 Diff: %v", err)
	}
	if !strings.Contains(unstaged, "+工作区版本") {
		t.Fatalf("工作区 Diff 缺少新内容:\n%s", unstaged)
	}

	runGit(t, root, "add", "--", "modified.txt")
	staged, err := inspector.Diff(context.Background(), root, "modified.txt", true)
	if err != nil {
		t.Fatalf("读取暂存区 Diff: %v", err)
	}
	if !strings.Contains(staged, "+工作区版本") {
		t.Fatalf("暂存区 Diff 缺少新内容:\n%s", staged)
	}

	if _, err := inspector.Diff(context.Background(), root, "../outside.txt", false); !errors.Is(err, ErrPathOutsideRoot) {
		t.Fatalf("相对路径越界错误 = %v，期望 ErrPathOutsideRoot", err)
	}
	if _, err := inspector.Diff(context.Background(), root, filepath.Join(root, "modified.txt"), false); !errors.Is(err, ErrPathOutsideRoot) {
		t.Fatalf("绝对路径错误 = %v，期望 ErrPathOutsideRoot", err)
	}

	writeTestFile(t, filepath.Join(root, "modified.txt"), strings.Repeat("很长的内容", 512)+"\n")
	limited := NewInspector(2*time.Second, 64)
	if _, err := limited.Diff(context.Background(), root, "modified.txt", false); !errors.Is(err, ErrDiffTooLarge) {
		t.Fatalf("超限错误 = %v，期望 ErrDiffTooLarge", err)
	} else if !errors.Is(err, ErrGitOutputTooLarge) {
		t.Fatalf("Diff 超限也应归类为 ErrGitOutputTooLarge: %v", err)
	}
}

func TestInspectorPushUsesConfiguredUpstream(t *testing.T) {
	root := newTestRepository(t)
	remote := canonicalTestPath(t, t.TempDir())
	runGit(t, remote, "init", "--bare")
	runGit(t, root, "remote", "add", "origin", remote)
	runGit(t, root, "push", "-u", "origin", "main")

	writeTestFile(t, filepath.Join(root, "modified.txt"), "准备推送\n")
	runGit(t, root, "add", "--", "modified.txt")
	runGit(t, root, "commit", "-m", "待推送提交")
	wantHead := strings.TrimSpace(runGitOutput(t, root, "rev-parse", "HEAD"))

	inspector := NewInspector(2*time.Second, MaxDiffBytes)
	inspector.pushTimeout = 2 * time.Second
	branch, upstream, err := inspector.Push(context.Background(), root)
	if err != nil {
		t.Fatalf("推送当前分支: %v", err)
	}
	if branch != "main" || upstream != "origin/main" {
		t.Fatalf("推送目标异常: branch=%q upstream=%q", branch, upstream)
	}
	if remoteHead := strings.TrimSpace(runGitOutput(t, remote, "rev-parse", "refs/heads/main")); remoteHead != wantHead {
		t.Fatalf("远端 HEAD = %q，期望 %q", remoteHead, wantHead)
	}
}

func TestInspectorPushRejectsMissingUpstreamAndDetachedHead(t *testing.T) {
	t.Run("未配置上游", func(t *testing.T) {
		root := newTestRepository(t)
		_, _, err := NewInspector(2*time.Second, MaxDiffBytes).Push(context.Background(), root)
		if !errors.Is(err, ErrNoUpstream) {
			t.Fatalf("错误 = %v，期望 ErrNoUpstream", err)
		}
	})

	t.Run("detached HEAD", func(t *testing.T) {
		root := newTestRepository(t)
		runGit(t, root, "checkout", "--detach")
		_, _, err := NewInspector(2*time.Second, MaxDiffBytes).Push(context.Background(), root)
		if !errors.Is(err, ErrDetachedHead) {
			t.Fatalf("错误 = %v，期望 ErrDetachedHead", err)
		}
	})
}

func TestInspectorCommandOutputLimits(t *testing.T) {
	root := canonicalTestPath(t, t.TempDir())

	t.Run("status", func(t *testing.T) {
		inspector := NewInspector(2*time.Second, MaxDiffBytes)
		inspector.commandFactory = helperGitFactory("status", MaxStatusBytes+1, nil)
		snapshot, err := inspector.Snapshot(context.Background(), "large-status", root)
		assertOutputLimitError(t, err, "读取 Git 状态", MaxStatusBytes)
		if snapshot.Error == "" || snapshot.Hash == "" {
			t.Fatalf("超限状态仍应生成错误快照: %+v", snapshot)
		}
	})

	t.Run("worktree list", func(t *testing.T) {
		inspector := NewInspector(2*time.Second, MaxDiffBytes)
		inspector.commandFactory = helperGitFactory("worktree", MaxWorktreeListBytes+1, []string{root})
		_, err := inspector.Snapshot(context.Background(), "large-worktree", root)
		assertOutputLimitError(t, err, "读取 Git Worktree", MaxWorktreeListBytes)
	})

	t.Run("numstat", func(t *testing.T) {
		inspector := NewInspector(2*time.Second, MaxDiffBytes)
		inspector.commandFactory = func(ctx context.Context, commandRoot string, args ...string) *exec.Cmd {
			if len(args) > 0 && args[0] == "status" {
				return helperCommand(ctx, 0, "# branch.oid 0123456789abcdef\x00# branch.head main\x00")
			}
			if len(args) > 0 && args[0] == "diff" {
				return helperCommand(ctx, MaxNumStatBytes+1, "")
			}
			return helperCommand(ctx, 0, "")
		}
		_, err := inspector.Snapshot(context.Background(), "large-numstat", root)
		assertOutputLimitError(t, err, "读取 Git 行数统计", MaxNumStatBytes)
	})

	t.Run("untracked numstat", func(t *testing.T) {
		// race 模式下测试 helper 进程启动更慢，留足预算以稳定验证输出上限本身。
		inspector := NewInspector(10*time.Second, MaxDiffBytes)
		inspector.commandFactory = func(ctx context.Context, commandRoot string, args ...string) *exec.Cmd {
			if len(args) > 0 && args[0] == "status" {
				return helperCommand(ctx, 0, "# branch.oid (initial)\x00# branch.head main\x00? untracked.txt\x00")
			}
			if len(args) > 0 && args[0] == "diff" {
				return helperCommand(ctx, MaxNumStatBytes+1, "")
			}
			return helperCommand(ctx, 0, "")
		}
		_, err := inspector.Snapshot(context.Background(), "large-untracked-numstat", root)
		assertOutputLimitError(t, err, "读取未跟踪文件行数统计", MaxNumStatBytes)
	})

	t.Run("commits", func(t *testing.T) {
		inspector := NewInspector(2*time.Second, MaxDiffBytes)
		inspector.commandFactory = helperGitFactory("log", MaxCommitOutputBytes+1, nil)
		_, err := inspector.Commits(context.Background(), root, 100)
		assertOutputLimitError(t, err, "读取 Git 提交", MaxCommitOutputBytes)
	})

	t.Run("secondary worktree status", func(t *testing.T) {
		secondary := canonicalTestPath(t, t.TempDir())
		inspector := NewInspector(10*time.Second, MaxDiffBytes)
		baseFactory := helperGitFactory("", 0, []string{root, secondary})
		inspector.commandFactory = func(ctx context.Context, commandRoot string, args ...string) *exec.Cmd {
			if len(args) > 0 && args[0] == "status" && commandRoot == secondary {
				return helperCommand(ctx, MaxStatusBytes+1, "")
			}
			return baseFactory(ctx, commandRoot, args...)
		}
		_, err := inspector.Snapshot(context.Background(), "large-secondary", root)
		assertOutputLimitError(t, err, "状态", MaxStatusBytes)
	})
}

func TestInspectorPreservesRootAndEnvironmentGuards(t *testing.T) {
	t.Run("拒绝非 canonical 根路径", func(t *testing.T) {
		root := newTestRepository(t)
		link := filepath.Join(t.TempDir(), "repo-link")
		if err := os.Symlink(root, link); err != nil {
			t.Skipf("当前文件系统不支持 symlink: %v", err)
		}
		_, err := NewInspector(time.Second, MaxDiffBytes).Snapshot(context.Background(), "link", link)
		if err == nil || !strings.Contains(err.Error(), "真实路径已变化") {
			t.Fatalf("非 canonical 根路径错误 = %v", err)
		}
	})

	t.Run("清理危险 Git 环境变量", func(t *testing.T) {
		t.Setenv("GIT_DIR", "/tmp/evil-git-dir")
		t.Setenv("GIT_CONFIG_COUNT", "1")
		t.Setenv("GIT_CONFIG_KEY_0", "core.fsmonitor")
		t.Setenv("GIT_CONFIG_VALUE_0", "evil")
		root := t.TempDir()
		cmd := newGitCommand(context.Background(), root, "status")
		environment := environmentMap(cmd.Env)
		for _, key := range []string{"GIT_DIR", "GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0"} {
			if _, exists := environment[key]; exists {
				t.Fatalf("危险环境变量 %s 未被清理", key)
			}
		}
		for key, want := range map[string]string{
			"GIT_OPTIONAL_LOCKS":    "0",
			"GIT_TERMINAL_PROMPT":   "0",
			"GIT_LITERAL_PATHSPECS": "1",
			"LC_ALL":                "C",
		} {
			if environment[key] != want {
				t.Fatalf("%s = %q，期望 %q", key, environment[key], want)
			}
		}
		if args := strings.Join(cmd.Args, "\x00"); !strings.Contains(args, "-c\x00safe.directory="+root+"\x00-C\x00"+root) {
			t.Fatalf("Git 命令未在单次调用中信任注册根目录: %q", cmd.Args)
		}
	})
}

// TestGitOutputHelper 仅由子进程调用，用于稳定地产生超限输出而不创建海量仓库文件。
func TestGitOutputHelper(t *testing.T) {
	if os.Getenv("TRELLIS_GIT_OUTPUT_HELPER") != "1" {
		return
	}
	if encoded := os.Getenv("TRELLIS_GIT_HELPER_PAYLOAD"); encoded != "" {
		payload, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			os.Exit(2)
		}
		_, _ = os.Stdout.Write(payload)
	}
	size, _ := strconv.ParseInt(os.Getenv("TRELLIS_GIT_HELPER_SIZE"), 10, 64)
	chunk := []byte(strings.Repeat("x", 32*1024))
	for size > 0 {
		writeSize := int64(len(chunk))
		if size < writeSize {
			writeSize = size
		}
		if _, err := os.Stdout.Write(chunk[:writeSize]); err != nil {
			os.Exit(0)
		}
		size -= writeSize
	}
	os.Exit(0)
}

func TestInspectorHonorsCanceledContext(t *testing.T) {
	root := newTestRepository(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := NewInspector(time.Second, MaxDiffBytes).Diff(ctx, root, "", false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("取消错误 = %v，期望 context.Canceled", err)
	}
}

func TestInspectorSnapshotNonGitDirectory(t *testing.T) {
	root := t.TempDir()
	snapshot, err := NewInspector(time.Second, MaxDiffBytes).Snapshot(context.Background(), "plain", root)
	if err == nil {
		t.Fatal("非 Git 目录应返回错误")
	}
	if snapshot.Error == "" {
		t.Fatal("非 Git 目录快照应带可展示 Error")
	}
	if snapshot.Hash == "" {
		t.Fatal("错误快照也应带稳定 Hash，供查询缓存展示")
	}
	if snapshot.ProjectID != "plain" || snapshot.UpdatedAt.IsZero() {
		t.Fatalf("错误快照仍应保留项目信息: %+v", snapshot)
	}
}

func TestParseStatusRenameConflictAndBranch(t *testing.T) {
	output := strings.Join([]string{
		"# branch.oid 0123456789abcdef",
		"# branch.head feature/demo",
		"# branch.upstream origin/feature/demo",
		"# branch.ab +2 -3",
		"2 R. N... 100644 100644 100644 abcdef1 abcdef2 R100 new name.txt",
		"old name.txt",
		"u UU N... 100644 100644 100644 100644 abcdef1 abcdef2 abcdef3 conflict.txt",
		"? untracked name.txt",
		"",
	}, "\x00")

	snapshot := model.GitSnapshot{Files: make([]model.GitFile, 0)}
	if err := parseStatus([]byte(output), &snapshot); err != nil {
		t.Fatalf("解析 porcelain v2: %v", err)
	}
	if snapshot.Branch != "feature/demo" || snapshot.Upstream != "origin/feature/demo" || snapshot.Ahead != 2 || snapshot.Behind != 3 {
		t.Fatalf("分支字段解析错误: %+v", snapshot)
	}
	if snapshot.Modified != 1 || snapshot.Conflicted != 1 || snapshot.Untracked != 1 {
		t.Fatalf("文件统计解析错误: %+v", snapshot)
	}
	if snapshot.Files[0].Path != "new name.txt" || snapshot.Files[0].OldPath != "old name.txt" || snapshot.Files[0].Status != "renamed" {
		t.Fatalf("重命名记录解析错误: %+v", snapshot.Files[0])
	}
}

func TestParseNumStatSkipsBinaryAndRenamePaths(t *testing.T) {
	output := []byte("2\t1\t普通文件.txt\x003\t4\t\x00旧名称.txt\x00新名称.txt\x00-\t-\t图片.png\x00")
	added, deleted, err := parseNumStat(output)
	if err != nil {
		t.Fatalf("解析 numstat: %v", err)
	}
	if added != 5 || deleted != 5 {
		t.Fatalf("numstat 汇总 = +%d/-%d，期望 +5/-5", added, deleted)
	}
}

func TestParseWorktrees(t *testing.T) {
	output := "" +
		"worktree /repo/main\n" +
		"HEAD abcdef\n" +
		"branch refs/heads/main\n" +
		"\n" +
		"worktree /repo/locked tree\n" +
		"HEAD 123456\n" +
		"detached\n" +
		"locked 正在被另一个进程使用\n" +
		"prunable\n" +
		"\n"

	worktrees, err := parseWorktrees([]byte(output))
	if err != nil {
		t.Fatalf("解析 worktree: %v", err)
	}
	if len(worktrees) != 2 {
		t.Fatalf("worktree 数 = %d，期望 2", len(worktrees))
	}
	if worktrees[0].Branch != "main" {
		t.Fatalf("主 worktree 分支 = %q", worktrees[0].Branch)
	}
	if !worktrees[1].Detached || worktrees[1].Locked != "正在被另一个进程使用" || worktrees[1].Prunable != "prunable" {
		t.Fatalf("标记解析错误: %+v", worktrees[1])
	}
}

func newTestRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	runGit(t, root, "init", "-b", "main")
	runGit(t, root, "config", "user.name", "测试用户")
	runGit(t, root, "config", "user.email", "test@example.com")

	writeTestFile(t, filepath.Join(root, "modified.txt"), "初始内容\n")
	writeTestFile(t, filepath.Join(root, "deleted.txt"), "稍后删除\n")
	runGit(t, root, "add", "--", "modified.txt", "deleted.txt")
	runGit(t, root, "commit", "-m", "初始提交")
	// macOS 的 /var 是 /private/var 的符号链接；Inspector 接收注册时固化的真实路径。
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", root, err)
	}
	return canonical
}

func runGit(t *testing.T, root string, args ...string) {
	runGitWithEnv(t, root, nil, args...)
}

func runGitOutput(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s 失败: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

func runGitWithEnv(t *testing.T, root string, env []string, args ...string) {
	t.Helper()
	commandArgs := append([]string{"-C", root}, args...)
	cmd := exec.Command("git", commandArgs...)
	cmd.Env = append(os.Environ(), append([]string{"LC_ALL=C", "GIT_TERMINAL_PROMPT=0"}, env...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s 失败: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建测试目录: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入测试文件: %v", err)
	}
}

func filesByPath(files []model.GitFile) map[string]model.GitFile {
	result := make(map[string]model.GitFile, len(files))
	for _, file := range files {
		result[file.Path] = file
	}
	return result
}

func worktreesByPath(worktrees []model.Worktree) map[string]model.Worktree {
	result := make(map[string]model.Worktree, len(worktrees))
	for _, worktree := range worktrees {
		result[worktree.Path] = worktree
	}
	return result
}

func helperGitFactory(oversizedCommand string, outputSize int64, worktreePaths []string) gitCommandFactory {
	return func(ctx context.Context, root string, args ...string) *exec.Cmd {
		command := ""
		if len(args) > 0 {
			command = args[0]
		}
		if command == oversizedCommand {
			return helperCommand(ctx, outputSize, "")
		}
		if command == "worktree" {
			var output strings.Builder
			for index, path := range worktreePaths {
				fmt.Fprintf(&output, "worktree %s\nHEAD %040d\nbranch refs/heads/test-%d\n\n", path, index, index)
			}
			return helperCommand(ctx, 0, output.String())
		}
		return helperCommand(ctx, 0, "")
	}
}

func helperCommand(ctx context.Context, outputSize int64, payload string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestGitOutputHelper$", "--")
	cmd.Env = append(
		os.Environ(),
		"TRELLIS_GIT_OUTPUT_HELPER=1",
		"TRELLIS_GIT_HELPER_SIZE="+strconv.FormatInt(outputSize, 10),
		"TRELLIS_GIT_HELPER_PAYLOAD="+base64.StdEncoding.EncodeToString([]byte(payload)),
	)
	return cmd
}

func assertOutputLimitError(t *testing.T, err error, operation string, limit int64) {
	t.Helper()
	if !errors.Is(err, ErrGitOutputTooLarge) {
		t.Fatalf("超限错误 = %v，期望 ErrGitOutputTooLarge", err)
	}
	if !strings.Contains(err.Error(), operation) || !strings.Contains(err.Error(), strconv.FormatInt(limit, 10)) {
		t.Fatalf("超限错误缺少操作或上限: %v", err)
	}
}

func environmentMap(environment []string) map[string]string {
	result := make(map[string]string, len(environment))
	for _, item := range environment {
		key, value, _ := strings.Cut(item, "=")
		result[key] = value
	}
	return result
}

func canonicalTestPath(t *testing.T, path string) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("解析测试路径 realpath: %v", err)
	}
	return filepath.Clean(canonical)
}

func assertFileStatus(t *testing.T, files map[string]model.GitFile, path, status, index, worktree string) {
	t.Helper()
	file, exists := files[path]
	if !exists {
		t.Fatalf("缺少文件状态 %q", path)
	}
	if file.Status != status || file.Index != index || file.Worktree != worktree {
		t.Fatalf("文件 %q 状态错误: %+v", path, file)
	}
}
