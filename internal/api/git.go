package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/yunnnn/trellis-dash/internal/gitstate"
	"github.com/yunnnn/trellis-dash/internal/model"
)

func (s *Server) getGitStatus(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	if _, err := s.store.GetProject(r.Context(), projectID); err != nil {
		writeStoreError(w, err)
		return
	}
	snapshot, err := s.store.GetGitSnapshot(r.Context(), projectID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if snapshot == nil {
		writeAPIError(w, http.StatusNotFound, "git_not_indexed", "Git 状态尚未采集")
		return
	}
	if setCacheValidator(w, r, payloadETag("git", snapshot)) {
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) listWorktrees(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	if _, err := s.store.GetProject(r.Context(), projectID); err != nil {
		writeStoreError(w, err)
		return
	}
	snapshot, err := s.store.GetGitSnapshot(r.Context(), projectID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if snapshot == nil {
		response := map[string]any{"items": []any{}, "total": 0}
		if setCacheValidator(w, r, payloadETag("worktrees", response)) {
			return
		}
		writeJSON(w, http.StatusOK, response)
		return
	}
	response := map[string]any{"items": snapshot.Worktrees, "total": len(snapshot.Worktrees)}
	if setCacheValidator(w, r, payloadETag("worktrees", response)) {
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) listCommits(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	project, err := s.store.GetProject(r.Context(), projectID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	version, err := s.gitCacheVersion(r.Context(), projectID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	limit := parseInt(r.URL.Query().Get("limit"), 50)
	// 与 Inspector 的边界保持一致，避免等价 limit 生成大量不同缓存 key。
	if limit <= 0 {
		limit = 20
	} else if limit > 100 {
		limit = 100
	}
	items, err := cachedGitValue(r.Context(), s.gitCache,
		fmt.Sprintf("commits:%s:%d", version, limit),
		func() ([]model.GitCommit, error) { return s.git.Commits(r.Context(), project.Root, limit) })
	if err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "git_command_failed", err.Error())
		return
	}
	response := map[string]any{"items": items, "total": len(items)}
	if setCacheValidator(w, r, payloadETag("commits", response)) {
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) getDiff(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	project, err := s.store.GetProject(r.Context(), projectID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	staged, _ := strconv.ParseBool(r.URL.Query().Get("staged"))
	path := r.URL.Query().Get("path")
	version, err := s.gitCacheVersion(r.Context(), projectID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	diff, err := cachedGitValue(r.Context(), s.gitCache,
		fmt.Sprintf("diff:%s:%q:%t", version, path, staged),
		func() (string, error) { return s.git.Diff(r.Context(), project.Root, path, staged) })
	if err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "git_diff_failed", err.Error())
		return
	}
	if setCacheValidator(w, r, payloadETag("diff", diff)) {
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(diff))
}

func (s *Server) gitCacheVersion(ctx context.Context, projectID string) (string, error) {
	revisions, err := s.store.GetRevisions(ctx, projectID)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s:%d", revisions.Generation, revisions.Git), nil
}

func (s *Server) pushGit(w http.ResponseWriter, r *http.Request) {
	project, err := s.store.GetProject(r.Context(), chi.URLParam(r, "projectID"))
	if err != nil {
		writeStoreError(w, err)
		return
	}

	branch, upstream, err := s.git.Push(r.Context(), project.Root)
	if err != nil {
		switch {
		case errors.Is(err, gitstate.ErrDetachedHead):
			writeAPIError(w, http.StatusConflict, "git_detached_head", gitstate.ErrDetachedHead.Error())
		case errors.Is(err, gitstate.ErrNoUpstream):
			writeAPIError(w, http.StatusConflict, "git_upstream_missing", gitstate.ErrNoUpstream.Error())
		default:
			writeAPIError(w, http.StatusUnprocessableEntity, "git_push_failed", err.Error())
		}
		return
	}

	response := map[string]any{
		"branch":    branch,
		"upstream":  upstream,
		"refreshed": true,
	}
	// Push 已成功时，即使快照刷新失败也不能把响应伪装成“推送失败”，避免用户重复操作。
	snapshot, inspectErr := s.git.Snapshot(r.Context(), project.ID, project.Root)
	if inspectErr == nil && snapshot.Hash != "" {
		if _, storeErr := s.store.ReplaceGitSnapshot(r.Context(), snapshot); storeErr != nil {
			response["refreshed"] = false
			response["warning"] = "推送已完成，但刷新 Git 状态失败：" + storeErr.Error()
			s.logger.Warn("Git 推送后保存快照失败", "projectId", project.ID, "error", storeErr)
		}
	} else {
		response["refreshed"] = false
		if inspectErr != nil {
			response["warning"] = "推送已完成，但刷新 Git 状态失败：" + inspectErr.Error()
		} else {
			response["warning"] = "推送已完成，但未生成新的 Git 快照"
		}
		s.logger.Warn("Git 推送后刷新快照失败", "projectId", project.ID, "error", inspectErr)
	}

	writeJSON(w, http.StatusOK, response)
}
