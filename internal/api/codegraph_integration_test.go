package api_test

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/yunnnn/trellis-dash/internal/codegraph"
)

func assertCodeGraphAPI(t *testing.T, harness *apiHarness, baseURL, projectID, initialRevision string) {
	t.Helper()
	if initialRevision == "" {
		t.Fatal("项目 revision 未包含 CodeGraph 指纹")
	}
	statusURL := fmt.Sprintf("%s/projects/%s/codegraph/status", baseURL, projectID)
	response := requestJSON(t, harness.client, http.MethodGet, statusURL, nil, nil)
	expectStatus(t, response, http.StatusOK)
	status := decodeResponse[codegraph.Status](t, response)
	if !status.Available || status.FileCount != 2 || status.NodeCount != 5 || status.EdgeCount != 3 {
		t.Fatalf("CodeGraph 状态异常: %+v", status)
	}
	assertNotModified(t, harness.client, statusURL, response.header.Get("ETag"))

	structureURL := fmt.Sprintf("%s/projects/%s/codegraph/structure?path=internal", baseURL, projectID)
	response = requestJSON(t, harness.client, http.MethodGet, structureURL, nil, nil)
	expectStatus(t, response, http.StatusOK)
	structure := decodeResponse[codegraph.Page[codegraph.StructureEntry]](t, response)
	if structure.Total != 2 || len(structure.Items) != 2 {
		t.Fatalf("CodeGraph 结构异常: %+v", structure)
	}

	searchURL := fmt.Sprintf("%s/projects/%s/codegraph/search?q=handleRequest", baseURL, projectID)
	response = requestJSON(t, harness.client, http.MethodGet, searchURL, nil, nil)
	expectStatus(t, response, http.StatusOK)
	search := decodeResponse[codegraph.Page[codegraph.Symbol]](t, response)
	if search.Total != 1 || search.Items[0].ID != "function:root" {
		t.Fatalf("CodeGraph 搜索异常: %+v", search)
	}

	relationsURL := fmt.Sprintf(
		"%s/projects/%s/codegraph/symbols/%s/relations?direction=callers",
		baseURL, projectID, "function%3Aroot",
	)
	response = requestJSON(t, harness.client, http.MethodGet, relationsURL, nil, nil)
	expectStatus(t, response, http.StatusOK)
	relations := decodeResponse[codegraph.RelationPage](t, response)
	if relations.Total != 2 || relations.Items[0].Source.ID != "function:caller" || relations.Items[1].Source.ID != "route:login" {
		t.Fatalf("CodeGraph 上游关系异常: %+v", relations)
	}
	assertNotModified(t, harness.client, relationsURL, response.header.Get("ETag"))

	routeRelationsURL := fmt.Sprintf(
		"%s/projects/%s/codegraph/symbols/%s/relations?direction=callees",
		baseURL, projectID, "route%3Alogin",
	)
	response = requestJSON(t, harness.client, http.MethodGet, routeRelationsURL, nil, nil)
	expectStatus(t, response, http.StatusOK)
	routeRelations := decodeResponse[codegraph.RelationPage](t, response)
	if routeRelations.Total != 1 || routeRelations.Items[0].Kind != "references" || routeRelations.Items[0].Target.ID != "function:root" {
		t.Fatalf("CodeGraph 路由桥接关系异常: %+v", routeRelations)
	}

	invalid := requestJSON(t, harness.client, http.MethodGet,
		fmt.Sprintf("%s/projects/%s/codegraph/search?q=handle&limit=oops", baseURL, projectID), nil, nil)
	expectStatus(t, invalid, http.StatusUnprocessableEntity)
	apiError := decodeResponse[apiErrorResponse](t, invalid)
	if apiError.Error.Code != "invalid_codegraph_query" {
		t.Fatalf("CodeGraph 非法查询错误码 = %q", apiError.Error.Code)
	}

	// 外部索引变化只更新不透明 revision，不需要写 Dashboard SQLite 或新增轮询器。
	db, err := sql.Open("sqlite", filepath.Join(harness.projectDir, ".codegraph", "codegraph.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE nodes SET updated_at = updated_at + 1 WHERE id = 'function:root'`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	revisionHTTPResponse := requestJSON(t, harness.client, http.MethodGet,
		fmt.Sprintf("%s/projects/%s/revision", baseURL, projectID), nil, nil)
	expectStatus(t, revisionHTTPResponse, http.StatusOK)
	revision := decodeResponse[revisionResponse](t, revisionHTTPResponse)
	if revision.Resources.CodeGraph == "" || revision.Resources.CodeGraph == initialRevision {
		t.Fatalf("CodeGraph 索引变化未更新 revision: before=%s after=%s", initialRevision, revision.Resources.CodeGraph)
	}
}

func createAPICodeGraphFixture(t *testing.T, projectRoot string) {
	t.Helper()
	directory := filepath.Join(projectRoot, ".codegraph")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(directory, "codegraph.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		PRAGMA journal_mode = WAL;
		CREATE TABLE files (
			path TEXT PRIMARY KEY, content_hash TEXT NOT NULL DEFAULT '', language TEXT NOT NULL,
			size INTEGER NOT NULL, modified_at INTEGER NOT NULL, indexed_at INTEGER NOT NULL,
			node_count INTEGER DEFAULT 0, errors TEXT
		);
		CREATE TABLE nodes (
			id TEXT PRIMARY KEY, kind TEXT NOT NULL, name TEXT NOT NULL, qualified_name TEXT NOT NULL,
			file_path TEXT NOT NULL, language TEXT NOT NULL, start_line INTEGER NOT NULL, end_line INTEGER NOT NULL,
			start_column INTEGER NOT NULL DEFAULT 0, end_column INTEGER NOT NULL DEFAULT 0,
			docstring TEXT, signature TEXT, visibility TEXT, is_exported INTEGER DEFAULT 0,
			is_async INTEGER DEFAULT 0, is_static INTEGER DEFAULT 0, is_abstract INTEGER DEFAULT 0,
			decorators TEXT, type_parameters TEXT, updated_at INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE edges (
			id INTEGER PRIMARY KEY, source TEXT NOT NULL, target TEXT NOT NULL, kind TEXT NOT NULL,
			metadata TEXT, line INTEGER, col INTEGER, provenance TEXT
		);
		CREATE TABLE schema_versions (version INTEGER PRIMARY KEY, applied_at INTEGER, description TEXT);
		INSERT INTO schema_versions(version, applied_at, description) VALUES (1, 1, 'initial'), (4, 2, 'current');
		INSERT INTO files(path, language, size, modified_at, indexed_at, node_count) VALUES
			('internal/api/server.go', 'go', 200, 1, 1783987201000, 3),
			('internal/store/store.go', 'go', 300, 1, 1783987202000, 1);
		INSERT INTO nodes(id, kind, name, qualified_name, file_path, language, start_line, end_line, signature) VALUES
			('function:root', 'function', 'handleRequest', 'handleRequest', 'internal/api/server.go', 'go', 10, 20, '(ctx context.Context) error'),
			('function:caller', 'function', 'serveHTTP', 'Server::serveHTTP', 'internal/api/server.go', 'go', 1, 9, '()'),
			('function:callee', 'function', 'writeJSON', 'writeJSON', 'internal/api/server.go', 'go', 22, 30, '()'),
			('route:login', 'route', 'POST /login', 'server.go::route:/login', 'internal/api/server.go', 'go', 31, 31, ''),
			('function:other', 'function', 'save', 'Store::save', 'internal/store/store.go', 'go', 50, 60, '()');
		INSERT INTO edges(id, source, target, kind, line, provenance) VALUES
			(1, 'function:caller', 'function:root', 'calls', 5, 'static'),
			(2, 'function:root', 'function:callee', 'calls', 15, 'static'),
			(3, 'route:login', 'function:root', 'references', 31, 'static');
	`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}
