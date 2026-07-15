package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/yunnnn/trellis-dash/internal/codegraph"
	"github.com/yunnnn/trellis-dash/internal/model"
	"github.com/yunnnn/trellis-dash/internal/store"
	_ "modernc.org/sqlite"
)

type fakeCodeGraphSyncController struct {
	cliAvailable bool
	operation    *codegraph.Operation
	startErr     error
	startedID    string
	startedRoot  string
	startedMode  codegraph.SyncMode
}

func (f *fakeCodeGraphSyncController) CLIAvailable() bool { return f.cliAvailable }
func (f *fakeCodeGraphSyncController) Operation(_, _ string) *codegraph.Operation {
	return f.operation
}
func (f *fakeCodeGraphSyncController) Start(projectID, projectRoot string, mode codegraph.SyncMode) (codegraph.Operation, error) {
	f.startedID, f.startedRoot, f.startedMode = projectID, projectRoot, mode
	if f.startErr != nil {
		return codegraph.Operation{}, f.startErr
	}
	return *f.operation, nil
}

func TestCodeGraphStatusIncludesCLIAndOperationInETag(t *testing.T) {
	server, _, root := newCodeGraphSyncTestServer(t, true)
	startedAt := time.Date(2026, 7, 15, 8, 45, 0, 0, time.UTC)
	fake := &fakeCodeGraphSyncController{
		cliAvailable: true,
		operation:    &codegraph.Operation{Mode: codegraph.SyncModeIncremental, State: codegraph.OperationRunning, StartedAt: startedAt},
	}
	server.codegraphSync = fake

	request := codeGraphProjectRequest(http.MethodGet, "/api/v1/projects/demo/codegraph/status", "", "demo")
	first := httptest.NewRecorder()
	server.getCodeGraphStatus(first, request)
	if first.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，响应 %s", first.Code, first.Body.String())
	}
	var status codegraph.Status
	if err := json.NewDecoder(first.Body).Decode(&status); err != nil {
		t.Fatalf("解析状态: %v", err)
	}
	if !status.CLIAvailable || status.Operation == nil || status.Operation.State != codegraph.OperationRunning {
		t.Fatalf("状态未包含 CLI/Operation: %#v", status)
	}
	if fake.startedRoot != "" || root == "" {
		t.Fatal("读取状态不应启动同步")
	}

	finishedAt := startedAt.Add(time.Minute)
	fake.operation = &codegraph.Operation{
		Mode: codegraph.SyncModeIncremental, State: codegraph.OperationSucceeded,
		StartedAt: startedAt, FinishedAt: &finishedAt,
	}
	secondRequest := codeGraphProjectRequest(http.MethodGet, request.URL.String(), "", "demo")
	secondRequest.Header.Set("If-None-Match", first.Header().Get("ETag"))
	second := httptest.NewRecorder()
	server.getCodeGraphStatus(second, secondRequest)
	if second.Code != http.StatusOK || second.Header().Get("ETag") == first.Header().Get("ETag") {
		t.Fatalf("Operation 变化后应返回新表示，状态=%d ETag=%q", second.Code, second.Header().Get("ETag"))
	}
}

func TestSyncCodeGraphAcceptsOnlyFixedModeAndStoredRoot(t *testing.T) {
	server, fake, root := newCodeGraphSyncTestServer(t, true)
	startedAt := time.Now().UTC()
	fake.operation = &codegraph.Operation{Mode: codegraph.SyncModeIncremental, State: codegraph.OperationRunning, StartedAt: startedAt}

	response := httptest.NewRecorder()
	server.syncCodeGraph(response, codeGraphProjectRequest(http.MethodPost, "/api/v1/projects/demo/codegraph/sync", `{"mode":"sync"}`, "demo"))
	if response.Code != http.StatusAccepted {
		t.Fatalf("状态码 = %d，响应 %s", response.Code, response.Body.String())
	}
	if fake.startedID != "demo" || fake.startedRoot != root || fake.startedMode != codegraph.SyncModeIncremental {
		t.Fatalf("启动参数 = id:%q root:%q mode:%q", fake.startedID, fake.startedRoot, fake.startedMode)
	}

	fake.startedRoot = ""
	injected := httptest.NewRecorder()
	server.syncCodeGraph(injected, codeGraphProjectRequest(
		http.MethodPost, "/api/v1/projects/demo/codegraph/sync", `{"mode":"sync","projectRoot":"/tmp/evil"}`, "demo",
	))
	if injected.Code != http.StatusBadRequest || fake.startedRoot != "" {
		t.Fatalf("未知路径字段应被拒绝，状态=%d root=%q", injected.Code, fake.startedRoot)
	}

	invalid := httptest.NewRecorder()
	server.syncCodeGraph(invalid, codeGraphProjectRequest(http.MethodPost, "/api/v1/projects/demo/codegraph/sync", `{"mode":"init"}`, "demo"))
	if invalid.Code != http.StatusUnprocessableEntity || !strings.Contains(invalid.Body.String(), "invalid_codegraph_sync") {
		t.Fatalf("非法 mode 响应 = %d %s", invalid.Code, invalid.Body.String())
	}
}

func TestSyncCodeGraphMapsControllerErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
		code string
	}{
		{name: "CLI 缺失", err: codegraph.ErrCLIUnavailable, want: http.StatusServiceUnavailable, code: "codegraph_cli_unavailable"},
		{name: "同项目冲突", err: codegraph.ErrSyncConflict, want: http.StatusConflict, code: "codegraph_sync_conflict"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, fake, _ := newCodeGraphSyncTestServer(t, true)
			fake.startErr = tt.err
			response := httptest.NewRecorder()
			server.syncCodeGraph(response, codeGraphProjectRequest(http.MethodPost, "/sync", `{"mode":"rebuild"}`, "demo"))
			if response.Code != tt.want || !strings.Contains(response.Body.String(), tt.code) {
				t.Fatalf("响应 = %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestSyncCodeGraphRejectsMissingIndexBeforeStarting(t *testing.T) {
	server, fake, _ := newCodeGraphSyncTestServer(t, false)
	response := httptest.NewRecorder()
	server.syncCodeGraph(response, codeGraphProjectRequest(http.MethodPost, "/sync", `{"mode":"sync"}`, "demo"))
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "codegraph_not_initialized") {
		t.Fatalf("响应 = %d %s", response.Code, response.Body.String())
	}
	if fake.startedRoot != "" {
		t.Fatalf("索引缺失时不应启动命令: %q", fake.startedRoot)
	}
}

func newCodeGraphSyncTestServer(t *testing.T, withIndex bool) (*Server, *fakeCodeGraphSyncController, string) {
	t.Helper()
	repository, err := store.Open(filepath.Join(t.TempDir(), "dashboard.db"))
	if err != nil {
		t.Fatalf("打开 Store: %v", err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	root := t.TempDir()
	if withIndex {
		createCodeGraphStatusFixture(t, root)
	}
	if err := repository.UpsertProject(context.Background(), model.Project{ID: "demo", Name: "Demo", Root: root}); err != nil {
		t.Fatalf("写入项目: %v", err)
	}
	fake := &fakeCodeGraphSyncController{cliAvailable: true}
	return &Server{store: repository, codegraph: codegraph.NewReader(), codegraphSync: fake}, fake, root
}

func createCodeGraphStatusFixture(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, ".codegraph", "codegraph.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建 CodeGraph 目录: %v", err)
	}
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("打开 CodeGraph fixture: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	statements := []string{
		`CREATE TABLE files (path TEXT, language TEXT, size INTEGER, indexed_at INTEGER, node_count INTEGER)`,
		`CREATE TABLE nodes (id TEXT, kind TEXT, name TEXT, qualified_name TEXT, file_path TEXT, language TEXT, start_line INTEGER, end_line INTEGER, signature TEXT)`,
		`CREATE TABLE edges (id INTEGER, source TEXT, target TEXT, kind TEXT, line INTEGER, provenance TEXT)`,
		`INSERT INTO files VALUES ('main.go', 'go', 12, 1784085900000, 1)`,
	}
	for _, statement := range statements {
		if _, err := database.Exec(statement); err != nil {
			t.Fatalf("执行 fixture SQL: %v", err)
		}
	}
}

func codeGraphProjectRequest(method, target, body, projectID string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("projectID", projectID)
	return request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routeContext))
}
