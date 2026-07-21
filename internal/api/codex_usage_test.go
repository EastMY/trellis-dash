package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/yunnnn/trellis-dash/internal/codexusage"
	"github.com/yunnnn/trellis-dash/internal/model"
	"github.com/yunnnn/trellis-dash/internal/store"
)

type fakeCodexUsage struct {
	query  codexusage.Query
	result codexusage.Summary
	err    error
}

func (f *fakeCodexUsage) Summary(_ context.Context, query codexusage.Query) (codexusage.Summary, error) {
	f.query = query
	result := f.result
	result.Scope = query.Scope
	result.Days = query.Days
	return result, f.err
}

func TestCodexUsageAPIContract(t *testing.T) {
	repository, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	root := filepath.Join(t.TempDir(), "project")
	now := time.Now().UTC()
	project := model.Project{ID: "usage-project", Name: "Usage", Root: root, Mode: model.ProjectModeObserver, CreatedAt: now, UpdatedAt: now}
	if err := repository.UpsertProject(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	fake := &fakeCodexUsage{result: codexusage.Summary{
		DateFrom: "2026-06-22", DateTo: "2026-07-21", TotalTokens: 42,
		Items: []codexusage.DayItem{},
	}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := newServer(repository, nil, nil, logger, fake)

	response := serveCodexUsageRequest(t, handler, "/api/v1/projects/usage-project/codex-usage", "")
	if response.Code != http.StatusOK {
		t.Fatalf("默认请求状态 = %d body=%s", response.Code, response.Body.String())
	}
	if fake.query.Scope != codexusage.ScopeProject || fake.query.Days != 30 || fake.query.ProjectRoot != root {
		t.Fatalf("默认查询未正确传给服务: %+v", fake.query)
	}
	var payload codexusage.Summary
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.TotalTokens != 42 || payload.Items == nil {
		t.Fatalf("响应契约异常: %+v", payload)
	}
	etag := response.Header().Get("ETag")
	if etag == "" || response.Header().Get("Cache-Control") != "private, no-cache" {
		t.Fatalf("缓存响应头异常: %+v", response.Header())
	}
	notModified := serveCodexUsageRequest(t, handler, "/api/v1/projects/usage-project/codex-usage", etag)
	if notModified.Code != http.StatusNotModified || notModified.Body.Len() != 0 {
		t.Fatalf("ETag 命中异常: status=%d body=%q", notModified.Code, notModified.Body.String())
	}

	filtered := serveCodexUsageRequest(t, handler, "/api/v1/projects/usage-project/codex-usage?scope=all&days=90", "")
	if filtered.Code != http.StatusOK || fake.query.Scope != codexusage.ScopeAll || fake.query.Days != 90 {
		t.Fatalf("显式筛选异常: status=%d query=%+v", filtered.Code, fake.query)
	}
}

func TestCodexUsageAPIRejectsFiltersAndMissingProject(t *testing.T) {
	repository, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := newServer(repository, nil, nil, logger, &fakeCodexUsage{})
	for _, path := range []string{
		"/api/v1/projects/missing/codex-usage?scope=mine",
		"/api/v1/projects/missing/codex-usage?days=8",
		"/api/v1/projects/missing/codex-usage?days=bad",
	} {
		response := serveCodexUsageRequest(t, handler, path, "")
		if response.Code != http.StatusUnprocessableEntity || errorCode(t, response) != "invalid_codex_usage_filter" {
			t.Fatalf("非法筛选响应异常: path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	missing := serveCodexUsageRequest(t, handler, "/api/v1/projects/missing/codex-usage", "")
	if missing.Code != http.StatusNotFound || errorCode(t, missing) != "not_found" {
		t.Fatalf("不存在项目响应异常: status=%d body=%s", missing.Code, missing.Body.String())
	}
}

func serveCodexUsageRequest(t *testing.T, handler http.Handler, path, etag string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
	if etag != "" {
		request.Header.Set("If-None-Match", etag)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func errorCode(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var payload errorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return payload.Error.Code
}
