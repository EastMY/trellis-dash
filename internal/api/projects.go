package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/yunnnn/trellis-dash/internal/model"
	"github.com/yunnnn/trellis-dash/internal/store"
	"github.com/yunnnn/trellis-dash/internal/trellis"
)

type createProjectRequest struct {
	Name string            `json:"name"`
	Root string            `json:"root"`
	Mode model.ProjectMode `json:"mode"`
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "database_unavailable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.store.ListProjects(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	for index := range projects {
		s.enrichProjectCodeGraph(&projects[index])
	}
	if setCacheValidator(w, r, payloadETag("projects", projects)) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": projects, "total": len(projects)})
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	s.projectsMu.Lock()
	defer s.projectsMu.Unlock()
	var input createProjectRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	root, err := trellis.ValidateRoot(input.Root)
	if err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid_project_root", err.Error())
		return
	}
	if err := store.ValidateDatabaseOutsideProject(s.store.DatabasePath(), root); err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "database_project_overlap", err.Error())
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		input.Name = filepath.Base(root)
	}
	if input.Mode == "" {
		input.Mode = model.ProjectModeObserver
	}
	if input.Mode != model.ProjectModeObserver {
		writeAPIError(w, http.StatusUnprocessableEntity, "unsupported_mode", "首版仅允许 observer 模式")
		return
	}
	if existing, err := s.store.GetProjectByRoot(r.Context(), root); err == nil {
		// 持久化项目启动时可能因目录暂时不可用而没有 runner；路径恢复后再次添加即可自愈。
		s.supervisor.Ensure(existing)
		s.enrichProjectCodeGraph(&existing)
		writeJSON(w, http.StatusOK, existing)
		return
	} else if err != store.ErrNotFound {
		writeStoreError(w, err)
		return
	}

	now := time.Now().UTC()
	project := model.Project{
		ID:   uniqueProjectID(r.Context(), s.store, input.Name, root),
		Name: input.Name, Root: root, Mode: input.Mode,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.UpsertProject(r.Context(), project); err != nil {
		writeAPIError(w, http.StatusConflict, "project_conflict", err.Error())
		return
	}
	project, _ = s.store.GetProject(r.Context(), project.ID)
	s.supervisor.Register(project)
	s.enrichProjectCodeGraph(&project)
	writeJSON(w, http.StatusCreated, project)
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	project, err := s.store.GetProject(r.Context(), chi.URLParam(r, "projectID"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.enrichProjectCodeGraph(&project)
	if setCacheValidator(w, r, payloadETag("project", project)) {
		return
	}
	writeJSON(w, http.StatusOK, project)
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	s.projectsMu.Lock()
	defer s.projectsMu.Unlock()
	projectID := chi.URLParam(r, "projectID")
	// 先原子更新 SQLite 与 sidecar；失败时保留现有 watcher，避免项目仍注册却停止刷新。
	if err := s.store.DeleteProject(r.Context(), projectID); err != nil {
		writeStoreError(w, err)
		return
	}
	s.supervisor.Remove(projectID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) rescanProject(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := s.supervisor.Rescan(ctx, projectID); err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "rescan_failed", err.Error())
		return
	}
	project, err := s.store.GetProject(r.Context(), projectID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.enrichProjectCodeGraph(&project)
	writeJSON(w, http.StatusOK, project)
}

func (s *Server) getRevision(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	project, err := s.store.GetProject(r.Context(), projectID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	revisions, err := s.store.GetRevisions(r.Context(), projectID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	revisions.CodeGraph = s.codegraph.Fingerprint(project.Root)
	response := map[string]any{
		"projectId": projectID,
		"resources": revisions,
		"updatedAt": revisions.Updated,
	}
	if setCacheValidator(w, r, payloadETag("revision", response)) {
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) getDashboard(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	dashboard, err := s.store.Dashboard(r.Context(), projectID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.enrichProjectCodeGraph(&dashboard.Project)
	if len(dashboard.CompletionTrend) > 0 {
		startDate := dashboard.CompletionTrend[0].Date
		endDate := dashboard.CompletionTrend[len(dashboard.CompletionTrend)-1].Date
		version, versionErr := s.gitCacheVersion(r.Context(), projectID)
		if versionErr != nil {
			writeStoreError(w, versionErr)
			return
		}
		gitTrend, gitErr := cachedGitValue(r.Context(), s.gitCache,
			fmt.Sprintf("trend:%s:%s:%s:%s", version, startDate, endDate, time.Local.String()),
			func() ([]model.DailyCompletion, error) {
				return s.git.DailyCommitCounts(r.Context(), dashboard.Project.Root, startDate, endDate, time.Local)
			})
		if gitErr != nil {
			// Git 故障只降级这一条曲线，不能阻断任务趋势和首页其他信息。
			s.logger.Warn("读取首页 Git 提交趋势失败", "projectId", projectID, "error", gitErr)
		} else {
			dashboard.GitCommitTrend = gitTrend
			dashboard.GitCommitTrendAvailable = true
		}
	}
	// 概览含 indexedAt 与“今日完成”等非 revision 字段，必须按完整表示校验。
	if setCacheValidator(w, r, payloadETag("dashboard", dashboard)) {
		return
	}
	writeJSON(w, http.StatusOK, dashboard)
}

func (s *Server) enrichProjectCodeGraph(project *model.Project) {
	project.Revisions.CodeGraph = s.codegraph.Fingerprint(project.Root)
}

var projectSlugPattern = regexp.MustCompile(`[^a-z0-9]+`)

func uniqueProjectID(ctx context.Context, repository *store.Store, name, root string) string {
	base := strings.Trim(projectSlugPattern.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if base == "" {
		base = "project"
	}
	if _, err := repository.GetProject(ctx, base); err == store.ErrNotFound {
		return base
	}
	hash := sha256.Sum256([]byte(root))
	return base + "-" + hex.EncodeToString(hash[:4])
}
