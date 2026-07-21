package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/yunnnn/trellis-dash/internal/codexusage"
)

type codexUsageService interface {
	Summary(context.Context, codexusage.Query) (codexusage.Summary, error)
}

func (s *Server) getCodexUsage(w http.ResponseWriter, r *http.Request) {
	scope := codexusage.Scope(r.URL.Query().Get("scope"))
	if scope == "" {
		scope = codexusage.ScopeProject
	}
	if scope != codexusage.ScopeProject && scope != codexusage.ScopeAll {
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid_codex_usage_filter", "scope 仅支持 project 或 all")
		return
	}
	days := 30
	if rawDays := r.URL.Query().Get("days"); rawDays != "" {
		parsed, err := strconv.Atoi(rawDays)
		if err != nil {
			writeAPIError(w, http.StatusUnprocessableEntity, "invalid_codex_usage_filter", "days 仅支持 7、30 或 90")
			return
		}
		days = parsed
	}
	if days != 7 && days != 30 && days != 90 {
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid_codex_usage_filter", "days 仅支持 7、30 或 90")
		return
	}

	project, err := s.store.GetProject(r.Context(), chi.URLParam(r, "projectID"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	result, err := s.codexUsage.Summary(r.Context(), codexusage.Query{
		Scope: scope, Days: days, ProjectRoot: project.Root,
	})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if setCacheValidator(w, r, payloadETag("codex-usage", result)) {
		return
	}
	writeJSON(w, http.StatusOK, result)
}
