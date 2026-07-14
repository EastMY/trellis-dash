package api

import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/yunnnn/trellis-dash/internal/app"
	"github.com/yunnnn/trellis-dash/internal/gitstate"
	"github.com/yunnnn/trellis-dash/internal/store"
	"github.com/yunnnn/trellis-dash/internal/webui"
)

type Server struct {
	store      *store.Store
	supervisor *app.Supervisor
	git        *gitstate.Inspector
	logger     *slog.Logger
	picker     directoryPicker
	projectsMu sync.Mutex
	gitCache   *gitResultCache
}

func NewServer(repository *store.Store, supervisor *app.Supervisor, git *gitstate.Inspector, logger *slog.Logger) http.Handler {
	s := &Server{
		store: repository, supervisor: supervisor, git: git, logger: logger,
		picker: newNativeDirectoryPicker(), gitCache: newGitResultCache(),
	}
	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(localRequestGuard)
	router.Use(middleware.Recoverer)
	router.Use(apiTimeout)
	router.Use(securityHeaders)

	router.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	router.Get("/readyz", s.ready)

	router.Route("/api/v1", func(r chi.Router) {
		r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			writeAPIError(w, http.StatusNotFound, "not_found", "接口不存在")
		})
		// 根资源直接注册完整路径，确保 GET/POST 无尾斜杠契约不会触发重定向。
		r.Get("/projects", s.listProjects)
		r.Post("/projects", s.createProject)
		r.Get("/system/capabilities", s.systemCapabilities)
		r.Post("/system/directory-picker", s.selectDirectory)
		r.Get("/projects/{projectID}", s.getProject)
		r.Delete("/projects/{projectID}", s.deleteProject)
		r.Post("/projects/{projectID}/rescan", s.rescanProject)
		r.Get("/projects/{projectID}/revision", s.getRevision)
		r.Get("/projects/{projectID}/dashboard", s.getDashboard)
		r.Get("/projects/{projectID}/workflow-states", s.listWorkflowStates)
		r.Get("/projects/{projectID}/sessions", s.listSessions)
		r.Get("/projects/{projectID}/activity", s.listActivity)
		r.Get("/projects/{projectID}/tasks", s.listTasks)
		r.Get("/projects/{projectID}/tasks/{taskKey}", s.getTaskDetail)
		r.Get("/projects/{projectID}/tasks/{taskKey}/artifacts", s.listTaskArtifacts)
		r.Get("/projects/{projectID}/tasks/{taskKey}/artifact", s.getTaskArtifact)
		r.Get("/projects/{projectID}/tasks/{taskKey}/context", s.listTaskContext)
		r.Get("/projects/{projectID}/tasks/{taskKey}/activity", s.listTaskActivity)
		r.Get("/projects/{projectID}/git/status", s.getGitStatus)
		r.Get("/projects/{projectID}/git/worktrees", s.listWorktrees)
		r.Get("/projects/{projectID}/git/commits", s.listCommits)
		r.Get("/projects/{projectID}/git/diff", s.getDiff)
		r.Post("/projects/{projectID}/git/push", s.pushGit)
	})
	router.NotFound(webui.Handler().ServeHTTP)
	return router
}

func apiTimeout(next http.Handler) http.Handler {
	timed := middleware.Timeout(30 * time.Second)(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 原生目录窗口需要等待用户操作，不应被普通 API 的固定超时打断。
		if r.URL.Path == "/api/v1/system/directory-picker" {
			next.ServeHTTP(w, r)
			return
		}
		timed.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'")
		next.ServeHTTP(w, r)
	})
}
