package api

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/yunnnn/trellis-dash/internal/codegraph"
)

func (s *Server) getCodeGraphStatus(w http.ResponseWriter, r *http.Request) {
	project, err := s.store.GetProject(r.Context(), chi.URLParam(r, "projectID"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	status, err := s.codegraph.Status(r.Context(), project.Root)
	if err != nil {
		status = unavailableCodeGraphStatus(status.Revision, err)
	}
	status.CLIAvailable = s.codegraphSync.CLIAvailable()
	status.Operation = s.codegraphSync.Operation(project.ID, project.Root)
	if setCacheValidator(w, r, payloadETag("codegraph-status", status)) {
		return
	}
	writeJSON(w, http.StatusOK, status)
}

type codeGraphSyncInput struct {
	Mode codegraph.SyncMode `json:"mode"`
}

func (s *Server) syncCodeGraph(w http.ResponseWriter, r *http.Request) {
	project, err := s.store.GetProject(r.Context(), chi.URLParam(r, "projectID"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	var input codeGraphSyncInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Mode != codegraph.SyncModeIncremental && input.Mode != codegraph.SyncModeRebuild {
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid_codegraph_sync", "mode 必须为 sync 或 rebuild")
		return
	}
	// 未初始化或不可读时不得启动写操作，旧索引状态继续由 Reader 统一判定。
	if _, err := s.codegraph.Status(r.Context(), project.Root); err != nil {
		writeCodeGraphError(w, err, "codegraph_entry_not_found")
		return
	}
	operation, err := s.codegraphSync.Start(project.ID, project.Root, input.Mode)
	if err != nil {
		switch {
		case errors.Is(err, codegraph.ErrCLIUnavailable):
			writeAPIError(w, http.StatusServiceUnavailable, "codegraph_cli_unavailable", "未检测到 CodeGraph CLI")
		case errors.Is(err, codegraph.ErrSyncConflict):
			writeAPIError(w, http.StatusConflict, "codegraph_sync_conflict", "当前项目已有 CodeGraph 操作正在执行")
		default:
			writeAPIError(w, http.StatusUnprocessableEntity, "invalid_codegraph_sync", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusAccepted, operation)
}

func (s *Server) getCodeGraphStructure(w http.ResponseWriter, r *http.Request) {
	project, err := s.store.GetProject(r.Context(), chi.URLParam(r, "projectID"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	limit, offset, ok := parseCodeGraphPage(w, r, codegraph.DefaultStructureLimit, codegraph.MaxStructureLimit)
	if !ok {
		return
	}
	page, err := s.codegraph.Structure(r.Context(), project.Root, r.URL.Query().Get("path"), limit, offset)
	if err != nil {
		writeCodeGraphError(w, err, "codegraph_entry_not_found")
		return
	}
	if setCacheValidator(w, r, payloadETag("codegraph-structure", page)) {
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) searchCodeGraphSymbols(w http.ResponseWriter, r *http.Request) {
	project, err := s.store.GetProject(r.Context(), chi.URLParam(r, "projectID"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	limit, offset, ok := parseCodeGraphPage(w, r, codegraph.DefaultSearchLimit, codegraph.MaxSearchLimit)
	if !ok {
		return
	}
	page, err := s.codegraph.Search(
		r.Context(), project.Root, r.URL.Query().Get("q"), strings.TrimSpace(r.URL.Query().Get("kind")), limit, offset,
	)
	if err != nil {
		writeCodeGraphError(w, err, "codegraph_symbol_not_found")
		return
	}
	if setCacheValidator(w, r, payloadETag("codegraph-search", page)) {
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) getCodeGraphRelations(w http.ResponseWriter, r *http.Request) {
	project, err := s.store.GetProject(r.Context(), chi.URLParam(r, "projectID"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	limit, offset, ok := parseCodeGraphPage(w, r, codegraph.DefaultRelationLimit, codegraph.MaxRelationLimit)
	if !ok {
		return
	}
	// 浏览器会用 encodeURIComponent 编码符号 ID；Chi 保留了该动态路径段的转义形式。
	symbolID, err := url.PathUnescape(chi.URLParam(r, "symbolID"))
	if err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid_codegraph_query", "符号 ID 编码无效")
		return
	}
	page, err := s.codegraph.Relations(
		r.Context(), project.Root, symbolID,
		codegraph.Direction(strings.TrimSpace(r.URL.Query().Get("direction"))), limit, offset,
	)
	if err != nil {
		writeCodeGraphError(w, err, "codegraph_symbol_not_found")
		return
	}
	if setCacheValidator(w, r, payloadETag("codegraph-relations", page)) {
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func parseCodeGraphPage(w http.ResponseWriter, r *http.Request, defaultLimit, maxLimit int) (int, int, bool) {
	limit, err := parseStrictQueryInt(r.URL.Query().Get("limit"), defaultLimit)
	if err != nil || limit < 1 || limit > maxLimit {
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid_codegraph_query", "limit 必须是允许范围内的正整数")
		return 0, 0, false
	}
	offset, err := parseStrictQueryInt(r.URL.Query().Get("offset"), 0)
	if err != nil || offset < 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid_codegraph_query", "offset 必须是非负整数")
		return 0, 0, false
	}
	return limit, offset, true
}

func parseStrictQueryInt(value string, fallback int) (int, error) {
	if strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	return strconv.Atoi(value)
}

func unavailableCodeGraphStatus(revision string, err error) codegraph.Status {
	status := codegraph.Status{Revision: revision}
	switch {
	case errors.Is(err, codegraph.ErrNotInitialized):
		status.Reason = "not_initialized"
		status.Message = "当前项目尚未初始化 CodeGraph，请在项目目录完成索引后重试"
	case errors.Is(err, codegraph.ErrIncompatibleSchema):
		status.Reason = "incompatible_schema"
		status.Message = "当前 CodeGraph 索引版本不兼容，请使用 CodeGraph 工具更新索引"
	case errors.Is(err, codegraph.ErrBusy):
		status.Reason = "busy"
		status.Message = "CodeGraph 索引正在更新，请稍后重试"
	default:
		status.Reason = "unreadable"
		status.Message = "CodeGraph 索引暂时无法读取，请检查索引文件"
	}
	return status
}

func writeCodeGraphError(w http.ResponseWriter, err error, notFoundCode string) {
	switch {
	case errors.Is(err, codegraph.ErrLimit):
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid_codegraph_query", err.Error())
	case errors.Is(err, codegraph.ErrNotInitialized):
		writeAPIError(w, http.StatusUnprocessableEntity, "codegraph_not_initialized", "当前项目尚未初始化 CodeGraph")
	case errors.Is(err, codegraph.ErrIncompatibleSchema):
		writeAPIError(w, http.StatusUnprocessableEntity, "codegraph_incompatible", "当前 CodeGraph 索引版本不兼容")
	case errors.Is(err, codegraph.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, notFoundCode, "CodeGraph 资源不存在")
	case errors.Is(err, codegraph.ErrBusy):
		writeAPIError(w, http.StatusServiceUnavailable, "codegraph_busy", "CodeGraph 索引正在更新，请稍后重试")
	case errors.Is(err, codegraph.ErrInvalidDatabase):
		writeAPIError(w, http.StatusInternalServerError, "codegraph_unavailable", "CodeGraph 索引无法读取")
	default:
		writeAPIError(w, http.StatusInternalServerError, "codegraph_unavailable", err.Error())
	}
}
