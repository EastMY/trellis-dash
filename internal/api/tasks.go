package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/yunnnn/trellis-dash/internal/model"
)

type taskDetailResponse struct {
	Task      model.Task            `json:"task"`
	Artifacts []model.Artifact      `json:"artifacts"`
	Context   []model.ContextEntry  `json:"context"`
	Sessions  []model.Session       `json:"sessions"`
	Activity  []model.ActivityEvent `json:"activity"`
}

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	filter := model.TaskFilter{
		Status: r.URL.Query().Get("status"), Priority: r.URL.Query().Get("priority"),
		Assignee: r.URL.Query().Get("assignee"), Query: r.URL.Query().Get("q"),
		Limit:  parseInt(r.URL.Query().Get("limit"), 100),
		Offset: parseInt(r.URL.Query().Get("offset"), 0),
	}
	if archived := r.URL.Query().Get("archived"); archived != "" {
		value, err := strconv.ParseBool(archived)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_filter", "archived 必须是布尔值")
			return
		}
		filter.Archived = &value
	}
	revisions, err := s.store.GetRevisions(r.Context(), projectID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	etag := revisionETag(
		"tasks", revisions.Generation, revisions.Tasks,
		filter.Archived, filter.Status, filter.Priority, filter.Assignee, filter.Query, filter.Limit, filter.Offset,
	)
	if setCacheValidator(w, r, etag) {
		return
	}
	page, err := s.store.ListTasks(r.Context(), projectID, filter)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) getTaskDetail(w http.ResponseWriter, r *http.Request) {
	projectID, taskKey := chi.URLParam(r, "projectID"), chi.URLParam(r, "taskKey")
	task, err := s.store.GetTask(r.Context(), projectID, taskKey)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	revisions, err := s.store.GetRevisions(r.Context(), projectID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	etag := revisionETag("task", revisions.Generation, taskKey, revisions.Tasks, revisions.Sessions, revisions.Activity)
	if setCacheValidator(w, r, etag) {
		return
	}
	artifacts, err := s.store.ListArtifactSummaries(r.Context(), projectID, taskKey)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	contextEntries, err := s.store.ListContext(r.Context(), projectID, taskKey)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	allSessions, err := s.store.ListSessions(r.Context(), projectID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	sessions := make([]model.Session, 0)
	for _, session := range allSessions {
		if session.TaskKey == taskKey {
			sessions = append(sessions, session)
		}
	}
	activity, err := s.store.RecentTaskActivity(r.Context(), projectID, taskKey, 100)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, taskDetailResponse{
		Task: task, Artifacts: artifacts, Context: contextEntries,
		Sessions: sessions, Activity: activity,
	})
}

func (s *Server) listTaskArtifacts(w http.ResponseWriter, r *http.Request) {
	projectID, taskKey := chi.URLParam(r, "projectID"), chi.URLParam(r, "taskKey")
	if _, err := s.store.GetTask(r.Context(), projectID, taskKey); err != nil {
		writeStoreError(w, err)
		return
	}
	revisions, err := s.store.GetRevisions(r.Context(), projectID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if setCacheValidator(w, r, revisionETag("artifacts", revisions.Generation, taskKey, revisions.Tasks)) {
		return
	}
	items, err := s.store.ListArtifactSummaries(r.Context(), projectID, taskKey)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) getTaskArtifact(w http.ResponseWriter, r *http.Request) {
	projectID, taskKey := chi.URLParam(r, "projectID"), chi.URLParam(r, "taskKey")
	artifactPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if artifactPath == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_artifact_path", "path 不能为空")
		return
	}
	revisions, err := s.store.GetRevisions(r.Context(), projectID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if setCacheValidator(w, r, revisionETag("artifact", revisions.Generation, taskKey, artifactPath, revisions.Tasks)) {
		return
	}
	artifact, err := s.store.GetArtifact(r.Context(), projectID, taskKey, artifactPath)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, artifact)
}

func (s *Server) listTaskContext(w http.ResponseWriter, r *http.Request) {
	projectID, taskKey := chi.URLParam(r, "projectID"), chi.URLParam(r, "taskKey")
	if _, err := s.store.GetTask(r.Context(), projectID, taskKey); err != nil {
		writeStoreError(w, err)
		return
	}
	revisions, err := s.store.GetRevisions(r.Context(), projectID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if setCacheValidator(w, r, revisionETag("context", revisions.Generation, taskKey, revisions.Tasks)) {
		return
	}
	items, err := s.store.ListContext(r.Context(), projectID, taskKey)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) listWorkflowStates(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	revisions, err := s.store.GetRevisions(r.Context(), projectID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if setCacheValidator(w, r, revisionETag("workflow", revisions.Generation, revisions.Tasks)) {
		return
	}
	items, err := s.store.ListWorkflowStates(r.Context(), projectID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	revisions, err := s.store.GetRevisions(r.Context(), projectID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	etag := revisionETag("sessions", revisions.Generation, revisions.Sessions)
	if setCacheValidator(w, r, etag) {
		return
	}
	items, err := s.store.ListSessions(r.Context(), projectID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) listActivity(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	afterID := int64(parseInt(r.URL.Query().Get("afterId"), 0))
	beforeID := int64(parseInt(r.URL.Query().Get("beforeId"), 0))
	limit := parseInt(r.URL.Query().Get("limit"), 100)
	if afterID > 0 && beforeID > 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid_pagination", "afterId 与 beforeId 不能同时使用")
		return
	}
	revisions, err := s.store.GetRevisions(r.Context(), projectID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if setCacheValidator(w, r, revisionETag("activity", revisions.Generation, revisions.Activity, afterID, beforeID, limit)) {
		return
	}
	page, err := s.store.ListActivity(
		r.Context(), projectID, afterID, beforeID, limit,
	)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) listTaskActivity(w http.ResponseWriter, r *http.Request) {
	projectID, taskKey := chi.URLParam(r, "projectID"), chi.URLParam(r, "taskKey")
	if _, err := s.store.GetTask(r.Context(), projectID, taskKey); err != nil {
		writeStoreError(w, err)
		return
	}
	revisions, err := s.store.GetRevisions(r.Context(), projectID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if setCacheValidator(w, r, revisionETag("task-activity", revisions.Generation, taskKey, revisions.Activity)) {
		return
	}
	items, err := s.store.RecentTaskActivity(r.Context(), projectID, taskKey, 100)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func parseInt(value string, fallback int) int {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
