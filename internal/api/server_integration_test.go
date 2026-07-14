package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yunnnn/trellis-dash/internal/api"
	"github.com/yunnnn/trellis-dash/internal/app"
	"github.com/yunnnn/trellis-dash/internal/gitstate"
	"github.com/yunnnn/trellis-dash/internal/model"
	"github.com/yunnnn/trellis-dash/internal/store"
	"github.com/yunnnn/trellis-dash/internal/trellis"
)

const activeTaskKey = "07-10-api-monitor"

type apiHarness struct {
	server     *httptest.Server
	client     *http.Client
	projectDir string
	taskFile   string
}

type listResponse[T any] struct {
	Items []T `json:"items"`
	Total int `json:"total"`
}

type revisionResponse struct {
	ProjectID string               `json:"projectId"`
	Resources model.RevisionBundle `json:"resources"`
	UpdatedAt time.Time            `json:"updatedAt"`
}

type taskDetailResponse struct {
	Task      model.Task            `json:"task"`
	Artifacts []model.Artifact      `json:"artifacts"`
	Context   []model.ContextEntry  `json:"context"`
	Sessions  []model.Session       `json:"sessions"`
	Activity  []model.ActivityEvent `json:"activity"`
}

type apiErrorResponse struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// TestServerEndToEnd 穿过真实 HTTP Router、Supervisor、Scanner、Git Inspector
// 与 SQLite Store，避免只验证各层 Mock 拼接后的理想路径。
func TestServerEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("当前环境没有 git，跳过真实 Git 集成测试")
	}

	harness := newAPIHarness(t)
	baseURL := harness.server.URL + "/api/v1"

	// 注册前应能稳定返回空项目列表，路径与前端调用保持一致（无尾斜杠）。
	response := requestJSON(t, harness.client, http.MethodGet, baseURL+"/projects", nil, nil)
	expectStatus(t, response, http.StatusOK)
	emptyProjects := decodeResponse[listResponse[model.Project]](t, response)
	if emptyProjects.Total != 0 || len(emptyProjects.Items) != 0 {
		t.Fatalf("初始项目列表应为空: %+v", emptyProjects)
	}

	// 普通目录不属于 Trellis 项目，必须在写入 SQLite 之前被拒绝。
	invalidRoot := t.TempDir()
	response = requestJSON(t, harness.client, http.MethodPost, baseURL+"/projects", map[string]any{
		"name": "无效项目",
		"root": invalidRoot,
		"mode": "observer",
	}, nil)
	expectStatus(t, response, http.StatusUnprocessableEntity)
	invalidRootError := decodeResponse[apiErrorResponse](t, response)
	if invalidRootError.Error.Code != "invalid_project_root" {
		t.Fatalf("非法根目录错误码 = %q，期望 invalid_project_root；响应=%s", invalidRootError.Error.Code, response.body)
	}

	response = requestJSON(t, harness.client, http.MethodPost, baseURL+"/projects", map[string]any{
		"name": "API E2E",
		"root": harness.projectDir,
		"mode": "observer",
	}, nil)
	expectStatus(t, response, http.StatusCreated)
	project := decodeResponse[model.Project](t, response)
	if project.ID != "api-e2e" || project.Root != harness.projectDir || project.Mode != model.ProjectModeObserver {
		t.Fatalf("创建项目响应异常: %+v", project)
	}

	response = requestJSON(t, harness.client, http.MethodGet, baseURL+"/projects", nil, nil)
	expectStatus(t, response, http.StatusOK)
	projects := decodeResponse[listResponse[model.Project]](t, response)
	if projects.Total != 1 || len(projects.Items) != 1 || projects.Items[0].ID != project.ID {
		t.Fatalf("项目列表未返回刚创建的项目: %+v", projects)
	}

	// Register 会启动真实后台首次扫描；等待任务、Session 和 Git 都进入读模型。
	initialRevision := waitForIndexedProject(t, harness, baseURL, project.ID)
	if initialRevision.Resources.Activity == 0 || initialRevision.Resources.Specs == 0 ||
		initialRevision.Resources.Generation == "" || initialRevision.Resources.Day == "" {
		t.Fatalf("首次索引未生成完整资源版本: %+v", initialRevision.Resources)
	}

	assertDashboardAPI(t, harness, baseURL, project.ID)
	assertTasksAPI(t, harness, baseURL, project.ID)
	assertTaskDetailAPI(t, harness, baseURL, project.ID)
	assertSessionsAPI(t, harness, baseURL, project.ID)
	assertActivityAPI(t, harness, baseURL, project.ID)
	assertGitAPI(t, harness, baseURL, project.ID)
	assertCodeGraphAPI(t, harness, baseURL, project.ID, initialRevision.Resources.CodeGraph)

	// 修改事实源后显式 rescan，验证 API 没有把 SQLite 当成不可重建的事实源。
	writeActiveTask(t, harness.taskFile, "review", "API 监控进入检查")
	response = requestJSON(t, harness.client, http.MethodPost,
		fmt.Sprintf("%s/projects/%s/rescan", baseURL, project.ID), nil, nil)
	expectStatus(t, response, http.StatusOK)
	rescannedProject := decodeResponse[model.Project](t, response)
	if rescannedProject.Revisions.Tasks <= initialRevision.Resources.Tasks {
		t.Fatalf("rescan 后 tasks revision 未提升: before=%d after=%d",
			initialRevision.Resources.Tasks, rescannedProject.Revisions.Tasks)
	}

	response = requestJSON(t, harness.client, http.MethodGet,
		fmt.Sprintf("%s/projects/%s/tasks/%s", baseURL, project.ID, activeTaskKey), nil, nil)
	expectStatus(t, response, http.StatusOK)
	updatedDetail := decodeResponse[taskDetailResponse](t, response)
	if updatedDetail.Task.Status != "review" || updatedDetail.Task.Title != "API 监控进入检查" {
		t.Fatalf("rescan 后任务仍是旧版本: %+v", updatedDetail.Task)
	}

	response = requestJSON(t, harness.client, http.MethodGet,
		fmt.Sprintf("%s/projects/%s/revision", baseURL, project.ID), nil, nil)
	expectStatus(t, response, http.StatusOK)
	afterRevision := decodeResponse[revisionResponse](t, response)
	if afterRevision.ProjectID != project.ID || afterRevision.Resources.Tasks <= initialRevision.Resources.Tasks {
		t.Fatalf("revision 接口未反映重扫结果: before=%+v after=%+v", initialRevision, afterRevision)
	}

	// 删除项目要同时移除 Supervisor runner 与 SQLite 中的所有级联数据。
	response = requestJSON(t, harness.client, http.MethodDelete,
		fmt.Sprintf("%s/projects/%s", baseURL, project.ID), nil, nil)
	expectStatus(t, response, http.StatusNoContent)
	if len(response.body) != 0 {
		t.Fatalf("DELETE 204 不应返回响应体: %q", response.body)
	}

	response = requestJSON(t, harness.client, http.MethodGet, baseURL+"/projects", nil, nil)
	expectStatus(t, response, http.StatusOK)
	projects = decodeResponse[listResponse[model.Project]](t, response)
	if projects.Total != 0 || len(projects.Items) != 0 {
		t.Fatalf("删除后项目仍在列表中: %+v", projects)
	}

	response = requestJSON(t, harness.client, http.MethodGet,
		fmt.Sprintf("%s/projects/%s/revision", baseURL, project.ID), nil, nil)
	expectStatus(t, response, http.StatusNotFound)
}

func TestDashboardKeepsTaskTrendWhenGitUnavailable(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("当前环境没有 git，跳过真实 Git 降级测试")
	}

	harness := newAPIHarness(t)
	baseURL := harness.server.URL + "/api/v1"
	createResponse := requestJSON(t, harness.client, http.MethodPost, baseURL+"/projects", map[string]any{
		"name": "Git 降级测试",
		"root": harness.projectDir,
		"mode": "observer",
	}, nil)
	expectStatus(t, createResponse, http.StatusCreated)
	project := decodeResponse[model.Project](t, createResponse)
	waitForIndexedProject(t, harness, baseURL, project.ID)

	gitDirectory := filepath.Join(harness.projectDir, ".git")
	if err := os.Rename(gitDirectory, gitDirectory+"-unavailable"); err != nil {
		t.Fatalf("模拟 Git 仓库不可用: %v", err)
	}

	url := fmt.Sprintf("%s/projects/%s/dashboard", baseURL, project.ID)
	response := requestJSON(t, harness.client, http.MethodGet, url, nil, nil)
	expectStatus(t, response, http.StatusOK)
	dashboard := decodeResponse[model.DashboardSnapshot](t, response)
	if len(dashboard.CompletionTrend) != 90 {
		t.Fatalf("Git 不可用时任务趋势应继续返回: %d", len(dashboard.CompletionTrend))
	}
	if dashboard.GitCommitTrendAvailable || len(dashboard.GitCommitTrend) != 0 {
		t.Fatalf("Git 不可用状态异常: available=%t items=%d", dashboard.GitCommitTrendAvailable, len(dashboard.GitCommitTrend))
	}
}

func assertDashboardAPI(t *testing.T, harness *apiHarness, baseURL, projectID string) {
	t.Helper()
	url := fmt.Sprintf("%s/projects/%s/dashboard", baseURL, projectID)
	response := requestJSON(t, harness.client, http.MethodGet, url, nil, nil)
	expectStatus(t, response, http.StatusOK)
	dashboard := decodeResponse[model.DashboardSnapshot](t, response)
	if dashboard.Project.ID != projectID || dashboard.Statistics.Total != 2 ||
		dashboard.Statistics.Active != 1 || dashboard.Statistics.Archived != 1 {
		t.Fatalf("Dashboard 统计异常: %+v", dashboard)
	}
	if len(dashboard.ActiveTasks) != 1 || dashboard.ActiveTasks[0].Key != activeTaskKey {
		t.Fatalf("Dashboard 活跃任务异常: %+v", dashboard.ActiveTasks)
	}
	if len(dashboard.Sessions) != 1 || dashboard.Sessions[0].TaskKey != activeTaskKey {
		t.Fatalf("Dashboard Session 异常: %+v", dashboard.Sessions)
	}
	if dashboard.Git == nil || dashboard.Git.Branch != "main" || !dashboard.Git.Dirty {
		t.Fatalf("Dashboard Git 快照异常: %+v", dashboard.Git)
	}
	if len(dashboard.CompletionTrend) != 90 {
		t.Fatalf("Dashboard 完成趋势应包含 90 天: %d", len(dashboard.CompletionTrend))
	}
	if !dashboard.GitCommitTrendAvailable || len(dashboard.GitCommitTrend) != 90 {
		t.Fatalf("Dashboard Git 趋势异常: available=%t items=%d", dashboard.GitCommitTrendAvailable, len(dashboard.GitCommitTrend))
	}
	assertNotModified(t, harness.client, url, response.header.Get("ETag"))
	variantURL := fmt.Sprintf("%s/projects/%s/tasks?archived=false&limit=1", baseURL, projectID)
	variant := requestJSON(t, harness.client, http.MethodGet, variantURL, nil, map[string]string{
		"If-None-Match": response.header.Get("ETag"),
	})
	expectStatus(t, variant, http.StatusOK)
}

func assertActivityAPI(t *testing.T, harness *apiHarness, baseURL, projectID string) {
	t.Helper()
	url := fmt.Sprintf("%s/projects/%s/activity?limit=1", baseURL, projectID)
	response := requestJSON(t, harness.client, http.MethodGet, url, nil, nil)
	expectStatus(t, response, http.StatusOK)
	page := decodeResponse[model.ActivityPage](t, response)
	if len(page.Items) != 1 || page.FirstID == 0 || page.LastID == 0 {
		t.Fatalf("Activity 首屏异常: %+v", page)
	}
	assertNotModified(t, harness.client, url, response.header.Get("ETag"))
	invalidURL := fmt.Sprintf("%s/projects/%s/activity?afterId=1&beforeId=2", baseURL, projectID)
	invalid := requestJSON(t, harness.client, http.MethodGet, invalidURL, nil, nil)
	expectStatus(t, invalid, http.StatusBadRequest)
}

func assertTasksAPI(t *testing.T, harness *apiHarness, baseURL, projectID string) {
	t.Helper()
	url := fmt.Sprintf("%s/projects/%s/tasks?archived=false", baseURL, projectID)
	response := requestJSON(t, harness.client, http.MethodGet, url, nil, nil)
	expectStatus(t, response, http.StatusOK)
	page := decodeResponse[model.TaskPage](t, response)
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("活跃任务列表数量异常: %+v", page)
	}
	task := page.Items[0]
	if task.Key != activeTaskKey || task.Status != "in_progress" || task.RuntimePhase != "checking" ||
		task.ActiveSessions != 1 || task.ArtifactCount != 1 || task.ContextIssues != 0 {
		t.Fatalf("索引后的任务派生字段异常: %+v", task)
	}
	assertNotModified(t, harness.client, url, response.header.Get("ETag"))

	archivedURL := fmt.Sprintf("%s/projects/%s/tasks?archived=true", baseURL, projectID)
	response = requestJSON(t, harness.client, http.MethodGet, archivedURL, nil, nil)
	expectStatus(t, response, http.StatusOK)
	archived := decodeResponse[model.TaskPage](t, response)
	if archived.Total != 1 || len(archived.Items) != 1 || !archived.Items[0].Archived ||
		archived.Items[0].ArchiveMonth != "2026-07" {
		t.Fatalf("归档任务过滤异常: %+v", archived)
	}
}

func assertTaskDetailAPI(t *testing.T, harness *apiHarness, baseURL, projectID string) {
	t.Helper()
	detailURL := fmt.Sprintf("%s/projects/%s/tasks/%s", baseURL, projectID, activeTaskKey)
	response := requestJSON(t, harness.client, http.MethodGet, detailURL, nil, nil)
	expectStatus(t, response, http.StatusOK)
	detail := decodeResponse[taskDetailResponse](t, response)
	if detail.Task.Key != activeTaskKey || len(detail.Artifacts) != 1 ||
		detail.Artifacts[0].Kind != "prd" || detail.Artifacts[0].Content != "" {
		t.Fatalf("任务详情文档异常: %+v", detail)
	}
	artifactURL := fmt.Sprintf("%s/projects/%s/tasks/%s/artifact?path=%s",
		baseURL, projectID, activeTaskKey, neturl.QueryEscape(detail.Artifacts[0].Path))
	artifactResponse := requestJSON(t, harness.client, http.MethodGet, artifactURL, nil, nil)
	expectStatus(t, artifactResponse, http.StatusOK)
	artifact := decodeResponse[model.Artifact](t, artifactResponse)
	if !strings.Contains(artifact.Content, "验收标准") {
		t.Fatalf("延迟加载文档正文异常: %+v", artifact)
	}
	if len(detail.Context) != 2 || !detail.Context[0].Valid || !detail.Context[0].Exists ||
		!detail.Context[1].Example || !detail.Context[1].Valid {
		t.Fatalf("任务详情 Context 异常: %+v", detail.Context)
	}
	if len(detail.Sessions) != 1 || detail.Sessions[0].TaskKey != activeTaskKey {
		t.Fatalf("任务详情 Session 关联异常: %+v", detail.Sessions)
	}
	if len(detail.Activity) == 0 {
		t.Fatal("任务详情应包含首次索引产生的活动记录")
	}
	assertNotModified(t, harness.client, detailURL, response.header.Get("ETag"))
}

func assertSessionsAPI(t *testing.T, harness *apiHarness, baseURL, projectID string) {
	t.Helper()
	url := fmt.Sprintf("%s/projects/%s/sessions", baseURL, projectID)
	response := requestJSON(t, harness.client, http.MethodGet, url, nil, nil)
	expectStatus(t, response, http.StatusOK)
	sessions := decodeResponse[listResponse[model.Session]](t, response)
	if sessions.Total != 1 || len(sessions.Items) != 1 || sessions.Items[0].Key != "codex-e2e" ||
		sessions.Items[0].TaskKey != activeTaskKey || sessions.Items[0].Stale {
		t.Fatalf("Session 接口异常: %+v", sessions)
	}
	assertNotModified(t, harness.client, url, response.header.Get("ETag"))
}

func assertGitAPI(t *testing.T, harness *apiHarness, baseURL, projectID string) {
	t.Helper()
	statusURL := fmt.Sprintf("%s/projects/%s/git/status", baseURL, projectID)
	response := requestJSON(t, harness.client, http.MethodGet, statusURL, nil, nil)
	expectStatus(t, response, http.StatusOK)
	snapshot := decodeResponse[model.GitSnapshot](t, response)
	if snapshot.ProjectID != projectID || snapshot.Branch != "main" || snapshot.Head == "" ||
		!snapshot.Dirty || snapshot.Modified < 1 || len(snapshot.Worktrees) != 1 {
		t.Fatalf("Git 状态接口异常: %+v", snapshot)
	}
	assertNotModified(t, harness.client, statusURL, response.header.Get("ETag"))

	worktreesURL := fmt.Sprintf("%s/projects/%s/git/worktrees", baseURL, projectID)
	response = requestJSON(t, harness.client, http.MethodGet, worktreesURL, nil, nil)
	expectStatus(t, response, http.StatusOK)
	worktrees := decodeResponse[listResponse[model.Worktree]](t, response)
	if worktrees.Total != 1 || len(worktrees.Items) != 1 || worktrees.Items[0].Branch != "main" {
		t.Fatalf("Git Worktree 接口异常: %+v", worktrees)
	}

	commitsURL := fmt.Sprintf("%s/projects/%s/git/commits?limit=1", baseURL, projectID)
	response = requestJSON(t, harness.client, http.MethodGet, commitsURL, nil, nil)
	expectStatus(t, response, http.StatusOK)
	commits := decodeResponse[listResponse[model.GitCommit]](t, response)
	if commits.Total != 1 || len(commits.Items) != 1 || commits.Items[0].Subject != "初始化 Trellis 测试项目" {
		t.Fatalf("Git Commit 接口异常: %+v", commits)
	}

	diffURL := fmt.Sprintf("%s/projects/%s/git/diff?path=tracked.txt", baseURL, projectID)
	diffResponse := requestJSON(t, harness.client, http.MethodGet, diffURL, nil, nil)
	expectStatus(t, diffResponse, http.StatusOK)
	if !strings.Contains(string(diffResponse.body), "+索引后的工作区内容") {
		t.Fatalf("Git Diff 缺少工作区变化:\n%s", diffResponse.body)
	}

	// Observer 项目允许显式推送已有提交；成功后接口应立即刷新 ahead 状态。
	runGit(t, harness.projectDir, "add", "--", "tracked.txt")
	runGit(t, harness.projectDir, "commit", "-m", "准备 API Push")
	pushURL := fmt.Sprintf("%s/projects/%s/git/push", baseURL, projectID)
	pushResponse := requestJSON(t, harness.client, http.MethodPost, pushURL, nil, nil)
	expectStatus(t, pushResponse, http.StatusOK)
	pushResult := decodeResponse[struct {
		Branch    string `json:"branch"`
		Upstream  string `json:"upstream"`
		Refreshed bool   `json:"refreshed"`
	}](t, pushResponse)
	if pushResult.Branch != "main" || pushResult.Upstream != "origin/main" || !pushResult.Refreshed {
		t.Fatalf("Git Push 响应异常: %+v", pushResult)
	}

	response = requestJSON(t, harness.client, http.MethodGet, statusURL, nil, nil)
	expectStatus(t, response, http.StatusOK)
	snapshot = decodeResponse[model.GitSnapshot](t, response)
	if snapshot.Ahead != 0 || snapshot.Upstream != "origin/main" {
		t.Fatalf("Git Push 后状态未刷新: %+v", snapshot)
	}

	// 无上游时必须拒绝，不能替用户猜测远端或自动创建跟踪关系。
	runGit(t, harness.projectDir, "branch", "--unset-upstream")
	missingUpstreamResponse := requestJSON(t, harness.client, http.MethodPost, pushURL, nil, nil)
	expectStatus(t, missingUpstreamResponse, http.StatusConflict)
	missingUpstreamError := decodeResponse[apiErrorResponse](t, missingUpstreamResponse)
	if missingUpstreamError.Error.Code != "git_upstream_missing" {
		t.Fatalf("无上游错误码 = %q，期望 git_upstream_missing", missingUpstreamError.Error.Code)
	}
}

func waitForIndexedProject(t *testing.T, harness *apiHarness, baseURL, projectID string) revisionResponse {
	t.Helper()
	url := fmt.Sprintf("%s/projects/%s/revision", baseURL, projectID)
	deadline := time.Now().Add(10 * time.Second)
	var latest httpTestResponse
	for time.Now().Before(deadline) {
		latest = requestJSON(t, harness.client, http.MethodGet, url, nil, nil)
		if latest.status == http.StatusOK {
			revision := decodeResponse[revisionResponse](t, latest)
			if revision.Resources.Tasks > 0 && revision.Resources.Sessions > 0 && revision.Resources.Git > 0 {
				return revision
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("等待项目完成首次索引超时，最后响应 status=%d body=%s", latest.status, latest.body)
	return revisionResponse{}
}

func assertNotModified(t *testing.T, client *http.Client, url, etag string) {
	t.Helper()
	if etag == "" {
		t.Fatalf("GET %s 未返回 ETag", url)
	}
	response := requestJSON(t, client, http.MethodGet, url, nil, map[string]string{"If-None-Match": etag})
	expectStatus(t, response, http.StatusNotModified)
	if len(response.body) != 0 {
		t.Fatalf("304 响应不应包含响应体: %q", response.body)
	}
}

func newAPIHarness(t *testing.T) *apiHarness {
	t.Helper()
	projectDir, taskFile := createTrellisGitFixture(t)
	createAPICodeGraphFixture(t, projectDir)
	repository, err := store.Open(filepath.Join(t.TempDir(), "dashboard.db"))
	if err != nil {
		t.Fatalf("打开 SQLite: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	inspector := gitstate.NewInspector(3*time.Second, gitstate.MaxDiffBytes)
	supervisor := app.NewSupervisor(
		repository,
		trellis.NewScanner(),
		inspector,
		logger,
		app.SupervisorOptions{
			Debounce:           20 * time.Millisecond,
			RefreshInterval:    time.Hour,
			FullRescanInterval: time.Hour,
		},
	)
	ctx, cancel := context.WithCancel(context.Background())
	if err := supervisor.Start(ctx); err != nil {
		cancel()
		_ = repository.Close()
		t.Fatalf("启动 Supervisor: %v", err)
	}

	server := httptest.NewServer(api.NewServer(repository, supervisor, inspector, logger))
	t.Cleanup(func() {
		server.Close()
		cancel()
		supervisor.Stop()
		if err := repository.Close(); err != nil {
			t.Errorf("关闭 SQLite: %v", err)
		}
	})
	return &apiHarness{
		server:     server,
		client:     &http.Client{Timeout: 5 * time.Second},
		projectDir: projectDir,
		taskFile:   taskFile,
	}
}

func createTrellisGitFixture(t *testing.T) (string, string) {
	t.Helper()
	// macOS 的临时目录常经由 /var -> /private/var 符号链接；与生产校验一样
	// 先取 realpath，避免把等价路径误判成 API 返回错误。
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("解析临时项目真实路径: %v", err)
	}
	activeDir := filepath.Join(root, ".trellis", "tasks", activeTaskKey)
	archivedDir := filepath.Join(root, ".trellis", "tasks", "archive", "2026-07", "07-09-finished")
	sessionDir := filepath.Join(root, ".trellis", ".runtime", "sessions")
	specDir := filepath.Join(root, ".trellis", "spec", "backend")
	for _, directory := range []string{activeDir, archivedDir, sessionDir, specDir} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatalf("创建测试目录 %s: %v", directory, err)
		}
	}

	taskFile := filepath.Join(activeDir, "task.json")
	writeActiveTask(t, taskFile, "in_progress", "实现 API 监控")
	writeTestFile(t, filepath.Join(activeDir, "prd.md"), "# API 监控\n\n## 验收标准\n\n- REST 查询可用\n")
	writeTestFile(t, filepath.Join(activeDir, "implement.jsonl"),
		`{"file":".trellis/spec/backend/index.md","reason":"后端规范"}`+"\n"+
			`{"_example":"示例项不注入"}`+"\n")
	writeTestFile(t, filepath.Join(archivedDir, "task.json"), `{
  "id": "finished",
  "name": "finished",
  "title": "已归档任务",
  "status": "completed",
  "priority": "P2",
  "createdAt": "2026-07-09T08:00:00+08:00",
  "completedAt": "2026-07-09T10:00:00+08:00"
}
`)
	writeTestFile(t, filepath.Join(sessionDir, "codex-e2e.json"), fmt.Sprintf(`{
  "platform": "codex",
  "current_task": ".trellis/tasks/%s",
  "last_seen_at": "2026-07-10T08:30:00+08:00",
  "current_run": {"phase": "checking"}
}
`, activeTaskKey))
	writeTestFile(t, filepath.Join(specDir, "index.md"), "# Go 后端规范\n")
	writeTestFile(t, filepath.Join(root, ".trellis", "workflow.md"), strings.Join([]string{
		"# Workflow",
		"[workflow-state:planning]",
		"[workflow-state:in_progress]",
		"[workflow-state:review]",
		"[workflow-state:completed]",
		"",
	}, "\n"))
	writeTestFile(t, filepath.Join(root, "tracked.txt"), "初始工作区内容\n")

	runGit(t, root, "init", "-b", "main")
	runGit(t, root, "config", "user.name", "API 测试用户")
	runGit(t, root, "config", "user.email", "api-test@example.com")
	runGit(t, root, "add", "--", ".")
	runGit(t, root, "commit", "-m", "初始化 Trellis 测试项目")
	remote, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("解析临时远端真实路径: %v", err)
	}
	runGit(t, remote, "init", "--bare")
	runGit(t, root, "remote", "add", "origin", remote)
	runGit(t, root, "push", "-u", "origin", "main")
	writeTestFile(t, filepath.Join(root, "tracked.txt"), "索引后的工作区内容\n")
	return root, taskFile
}

func writeActiveTask(t *testing.T, path, status, title string) {
	t.Helper()
	content := fmt.Sprintf(`{
  "id": "api-monitor",
  "name": "api-monitor",
  "title": %q,
  "description": "验证 Trellis Dashboard REST 链路",
  "status": %q,
  "priority": "P1",
  "creator": "tester",
  "assignee": "codex",
  "createdAt": "2026-07-10T08:00:00+08:00",
  "branch": "main",
  "subtasks": [{"title":"索引","done":true}],
  "relatedFiles": ["internal/api/server.go"],
  "meta": {"source":"e2e"}
}
`, title, status)
	writeTestFile(t, path, content)
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建文件目录 %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入测试文件 %s: %v", path, err)
	}
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "GIT_TERMINAL_PROMPT=0")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s 失败: %v\n%s", strings.Join(args, " "), err, output)
	}
}

type httpTestResponse struct {
	status int
	header http.Header
	body   []byte
}

func requestJSON(
	t *testing.T,
	client *http.Client,
	method, url string,
	body any,
	headers map[string]string,
) httpTestResponse {
	t.Helper()
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("序列化请求 JSON: %v", err)
		}
		reader = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(context.Background(), method, url, reader)
	if err != nil {
		t.Fatalf("创建 %s %s: %v", method, url, err)
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("请求 %s %s: %v", method, url, err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("读取 %s %s 响应: %v", method, url, err)
	}
	return httpTestResponse{status: response.StatusCode, header: response.Header.Clone(), body: payload}
}

func expectStatus(t *testing.T, response httpTestResponse, expected int) {
	t.Helper()
	if response.status != expected {
		t.Fatalf("HTTP 状态 = %d，期望 %d；响应=%s", response.status, expected, response.body)
	}
}

func decodeResponse[T any](t *testing.T, response httpTestResponse) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(response.body, &value); err != nil {
		t.Fatalf("解析响应 JSON 失败: %v；响应=%s", err, response.body)
	}
	return value
}
