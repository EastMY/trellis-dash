package codegraph

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestReaderStatusAndStructure(t *testing.T) {
	t.Parallel()
	root := createCodeGraphFixture(t)
	reader := NewReader()
	ctx := context.Background()

	status, err := reader.Status(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Available || status.FileCount != 3 || status.NodeCount != 7 || status.EdgeCount != 5 {
		t.Fatalf("状态统计异常: %+v", status)
	}
	if len(status.Languages) != 2 || len(status.SchemaVersions) != 2 || status.IndexedAt == nil {
		t.Fatalf("状态明细异常: %+v", status)
	}

	page, err := reader.Structure(ctx, root, "", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.Items) != 2 || page.Items[0].Name != "frontend" || page.Items[1].Name != "internal" {
		t.Fatalf("根目录结构异常: %+v", page)
	}
	internal, err := reader.Structure(ctx, root, "internal", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if internal.Total != 2 || internal.Items[0].Name != "api" || internal.Items[1].Name != "store" {
		t.Fatalf("internal 目录结构异常: %+v", internal)
	}
	file, err := reader.Structure(ctx, root, "internal/api/server.go", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if file.Total != 5 || file.Items[0].Type != "symbol" || file.Items[0].Symbol == nil {
		t.Fatalf("文件符号结构异常: %+v", file)
	}
}

func TestReaderSearchAndRelationsUseExactSymbolID(t *testing.T) {
	t.Parallel()
	root := createCodeGraphFixture(t)
	reader := NewReader()
	ctx := context.Background()

	results, err := reader.Search(ctx, root, "handleRequest", "", 30, 0)
	if err != nil {
		t.Fatal(err)
	}
	if results.Total != 2 || results.Items[0].ID != "function:root" || results.Items[1].ID != "function:duplicate" {
		t.Fatalf("同名符号搜索顺序或 ID 异常: %+v", results)
	}
	callers, err := reader.Relations(ctx, root, "function:root", DirectionCallers, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if callers.Total != 3 || callers.Symbol.ID != "function:root" {
		t.Fatalf("上游调用关系异常: %+v", callers)
	}
	if callers.Items[0].Target.ID != "function:root" {
		t.Fatalf("上游关系方向异常: %+v", callers.Items[0])
	}
	if callers.Items[2].Source.ID != "route:login" || callers.Items[2].Kind != "references" {
		t.Fatalf("处理方法缺少路由上游引用: %+v", callers.Items)
	}
	callees, err := reader.Relations(ctx, root, "function:root", DirectionCallees, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if callees.Total != 1 || callees.Items[0].Source.ID != "function:root" || callees.Items[0].Target.ID != "function:callee" {
		t.Fatalf("下游调用关系异常: %+v", callees)
	}
	routeCallees, err := reader.Relations(ctx, root, "route:login", DirectionCallees, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if routeCallees.Total != 1 || routeCallees.Items[0].Kind != "references" || routeCallees.Items[0].Target.ID != "function:root" {
		t.Fatalf("路由未桥接到处理方法: %+v", routeCallees)
	}
	classCallees, err := reader.Relations(ctx, root, "class:server", DirectionCallees, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if classCallees.Total != 0 || len(classCallees.Items) != 0 {
		t.Fatalf("普通 references 不应混入调用链: %+v", classCallees)
	}
	if _, err := reader.Relations(ctx, root, "handleRequest", DirectionCallers, 20, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("关系查询不应按重名名称回退: %v", err)
	}
}

func TestReaderSearchPreservesSubstringRankingAndPagination(t *testing.T) {
	t.Parallel()
	root := createCodeGraphFixture(t)
	database, err := sql.Open("sqlite", databasePath(root))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO nodes(id, kind, name, qualified_name, file_path, language, start_line, end_line, signature) VALUES
			('function:exact', 'function', 'handle', 'handle', 'internal/store/store.go', 'go', 60, 60, '()'),
			('function:prefix', 'function', 'handler', 'handler', 'internal/store/store.go', 'go', 61, 61, '()'),
			('function:substring', 'function', 'preHandlePost', 'preHandlePost', 'internal/store/store.go', 'go', 62, 62, '()');
	`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	reader := NewReader()
	results, err := reader.Search(context.Background(), root, "HANDLE", "function", 30, 0)
	if err != nil {
		t.Fatal(err)
	}
	if results.Total != 5 || len(results.Items) != 5 {
		t.Fatalf("大小写不敏感子串结果异常: %+v", results)
	}
	if results.Items[0].ID != "function:exact" || results.Items[len(results.Items)-1].ID != "function:substring" {
		t.Fatalf("精确、前缀、子串排序异常: %+v", results.Items)
	}

	last, err := reader.Search(context.Background(), root, "handle", "function", 1, 4)
	if err != nil {
		t.Fatal(err)
	}
	if last.Total != 5 || len(last.Items) != 1 || last.Items[0].ID != "function:substring" || last.HasMore {
		t.Fatalf("末页分页异常: %+v", last)
	}
	beyond, err := reader.Search(context.Background(), root, "handle", "function", 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if beyond.Total != 5 || len(beyond.Items) != 0 || beyond.HasMore {
		t.Fatalf("offset 越界分页异常: %+v", beyond)
	}
	empty, err := reader.Search(context.Background(), root, "not-present", "function", 30, 0)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Total != 0 || len(empty.Items) != 0 {
		t.Fatalf("空搜索结果异常: %+v", empty)
	}
}

func TestReaderCachesSchemaUntilIndexIdentityChanges(t *testing.T) {
	t.Parallel()
	root := createCodeGraphFixture(t)
	reader := NewReader()
	baseValidator := reader.validateSchema
	validationCount := 0
	reader.validateSchema = func(ctx context.Context, db *sql.DB) error {
		validationCount++
		return baseValidator(ctx, db)
	}

	if _, err := reader.Status(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Search(context.Background(), root, "handle", "", 30, 0); err != nil {
		t.Fatal(err)
	}
	if validationCount != 1 {
		t.Fatalf("未变化索引的 schema 校验次数 = %d，期望 1", validationCount)
	}

	database, err := sql.Open("sqlite", databasePath(root))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`ALTER TABLE nodes RENAME COLUMN signature TO old_signature`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Status(context.Background(), root); !errors.Is(err, ErrIncompatibleSchema) {
		t.Fatalf("索引身份变化后错误 = %v，期望重新识别不兼容 schema", err)
	}
	if validationCount != 2 {
		t.Fatalf("索引变化后的 schema 校验次数 = %d，期望 2", validationCount)
	}
}

func TestReaderValidationAndUnavailableStates(t *testing.T) {
	t.Parallel()
	reader := NewReader()
	ctx := context.Background()
	missingRoot := t.TempDir()
	if _, err := reader.Status(ctx, missingRoot); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("缺少索引错误 = %v", err)
	}

	invalidRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(invalidRoot, ".codegraph"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(databasePath(invalidRoot), []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Status(ctx, invalidRoot); !errors.Is(err, ErrInvalidDatabase) {
		t.Fatalf("损坏索引错误 = %v", err)
	}

	incompatibleRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(incompatibleRoot, ".codegraph"), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", databasePath(incompatibleRoot))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE files (path TEXT PRIMARY KEY)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Status(ctx, incompatibleRoot); !errors.Is(err, ErrIncompatibleSchema) {
		t.Fatalf("不兼容索引错误 = %v", err)
	}

	root := createCodeGraphFixture(t)
	for _, value := range []string{"/absolute", "../escape", "internal/../api", "internal/./api", "bad\x00path"} {
		if _, err := reader.Structure(ctx, root, value, 100, 0); !errors.Is(err, ErrLimit) {
			t.Errorf("非法路径 %q 错误 = %v", value, err)
		}
	}
	if _, err := reader.Search(ctx, root, "", "", 30, 0); !errors.Is(err, ErrLimit) {
		t.Errorf("空搜索错误 = %v", err)
	}
	if _, err := reader.Search(ctx, root, "handle", "field", 30, 0); !errors.Is(err, ErrLimit) {
		t.Errorf("非法 kind 错误 = %v", err)
	}
	if _, err := reader.Relations(ctx, root, "function:root", Direction("both"), 20, 0); !errors.Is(err, ErrLimit) {
		t.Errorf("非法方向错误 = %v", err)
	}
	for _, limit := range []int{-1, MaxSearchLimit + 1} {
		if _, err := reader.Search(ctx, root, "handle", "", limit, 0); !errors.Is(err, ErrLimit) {
			t.Errorf("非法 limit %d 错误 = %v", limit, err)
		}
	}
	canceledContext, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := reader.Status(canceledContext, root); !errors.Is(err, context.Canceled) {
		t.Errorf("取消上下文错误 = %v", err)
	}
}

func TestReaderDoesNotModifyIndexFiles(t *testing.T) {
	t.Parallel()
	root := createCodeGraphFixture(t)
	reader := NewReader()
	before := indexFileState(t, root)
	ctx := context.Background()
	if _, err := reader.Status(ctx, root); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Structure(ctx, root, "", 100, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Search(ctx, root, "handle", "", 30, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Relations(ctx, root, "function:root", DirectionCallees, 20, 0); err != nil {
		t.Fatal(err)
	}
	after := indexFileState(t, root)
	if before != after {
		t.Fatalf("只读查询修改了索引文件元数据\nbefore=%s\nafter=%s", before, after)
	}
}

func createCodeGraphFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	directory := filepath.Join(root, ".codegraph")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", databasePath(root))
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
			('frontend/src/main.tsx', 'typescript', 100, 1, 1783987200000, 1),
			('internal/api/server.go', 'go', 200, 1, 1783987201000, 4),
			('internal/store/store.go', 'go', 300, 1, 1783987202000, 1);
		INSERT INTO nodes(id, kind, name, qualified_name, file_path, language, start_line, end_line, signature) VALUES
			('function:root', 'function', 'handleRequest', 'handleRequest', 'internal/api/server.go', 'go', 10, 20, '(ctx context.Context) error'),
			('function:caller', 'function', 'serveHTTP', 'Server::serveHTTP', 'internal/api/server.go', 'go', 1, 9, '()'),
			('function:callee', 'function', 'writeJSON', 'writeJSON', 'internal/api/server.go', 'go', 22, 30, '()'),
			('class:server', 'class', 'Server', 'Server', 'internal/api/server.go', 'go', 32, 40, ''),
			('route:login', 'route', 'POST /login', 'server.go::route:/login', 'internal/api/server.go', 'go', 41, 41, ''),
			('function:duplicate', 'function', 'handleRequest', 'Store::handleRequest', 'internal/store/store.go', 'go', 50, 60, '()'),
			('component:app', 'component', 'DashboardApp', 'DashboardApp', 'frontend/src/main.tsx', 'typescript', 1, 20, '()');
		INSERT INTO edges(id, source, target, kind, line, provenance) VALUES
			(1, 'function:caller', 'function:root', 'calls', 5, 'static'),
			(2, 'function:root', 'function:callee', 'calls', 15, 'static'),
			(3, 'function:callee', 'function:root', 'calls', 25, 'static'),
			(4, 'route:login', 'function:root', 'references', 41, 'static'),
			(5, 'class:server', 'function:root', 'references', 32, 'static');
	`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	// 保持 writer 到用例结束，让 WAL/SHM fixture 与真实 CodeGraph watcher 一致。
	t.Cleanup(func() { _ = db.Close() })
	return root
}

func indexFileState(t *testing.T, root string) string {
	t.Helper()
	result := ""
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, err := os.Stat(databasePath(root) + suffix)
		if errors.Is(err, os.ErrNotExist) {
			result += suffix + ":missing;"
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		result += fmt.Sprintf("%s:%s:%d;", suffix, info.ModTime().UTC().String(), info.Size())
	}
	return result
}
