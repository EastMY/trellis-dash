import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import { api } from "../api/client";
import type { RevisionBundle } from "../types";
import { usePageVisibility } from "./usePageVisibility";

const revisionResources: Array<keyof Pick<
  RevisionBundle,
  "tasks" | "sessions" | "git" | "activity" | "specs" | "agents"
>> = ["tasks", "sessions", "git", "activity", "specs", "agents"];

/**
 * 浏览器只轮询轻量 revision。资源版本变化后再定向失效 Query，
 * 页面不可见时停止定时器，重新可见或窗口聚焦时由 Query 立即刷新。
 */
export function useProjectPolling(projectId?: string) {
  const visible = usePageVisibility();
  const queryClient = useQueryClient();
  const previous = useRef<RevisionBundle | undefined>(undefined);

  const revisionQuery = useQuery({
    queryKey: ["project", projectId, "revision"],
    queryFn: () => api.getRevision(projectId!),
    enabled: Boolean(projectId),
    refetchInterval: visible ? 10_000 : false,
    refetchOnWindowFocus: "always",
    refetchOnReconnect: "always",
    retry: 3,
    retryDelay: (attempt) => Math.min(5000 * 2 ** attempt, 60_000),
  });

  useEffect(() => {
    const current = revisionQuery.data;
    if (!projectId || !current) return;

    const old = previous.current;
    if (old) {
      if (old.generation !== current.generation) {
        // 同 ID 项目被删除并重建时，所有旧资源都属于上一代项目。
        void queryClient.invalidateQueries({ queryKey: ["project", projectId] });
        void queryClient.invalidateQueries({ queryKey: ["projects"] });
      }
      if (old.day !== current.day) {
        void queryClient.invalidateQueries({ queryKey: ["project", projectId, "dashboard"] });
        void queryClient.invalidateQueries({ queryKey: ["projects"] });
      }
      for (const resource of revisionResources) {
        if (old[resource] !== current[resource]) {
          if (resource === "activity") {
            // InfiniteQuery 若直接 invalidate 会逐页重放所有历史请求；新活动到达时回到最新页。
            void queryClient.resetQueries({ queryKey: ["project", projectId, "activity"] });
          } else {
            void queryClient.invalidateQueries({ queryKey: ["project", projectId, resource] });
          }
          if (resource === "sessions" || resource === "activity") {
            void queryClient.invalidateQueries({ queryKey: ["project", projectId, "tasks", "detail"] });
          }
          if (resource === "sessions") {
            // Session 会更新任务的运行阶段和活动会话数，分页列表也需要同步。
            void queryClient.invalidateQueries({ queryKey: ["project", projectId, "tasks"] });
          }
          // Worktree 的任务映射来自任务 branch/worktree_path，任务变化时也要重取 Git 表示。
          if (resource === "tasks") {
            void queryClient.invalidateQueries({ queryKey: ["project", projectId, "git"] });
          }
        }
      }
      if (revisionResources.some((resource) => old[resource] !== current[resource])) {
        void queryClient.invalidateQueries({ queryKey: ["project", projectId, "dashboard"] });
        void queryClient.invalidateQueries({ queryKey: ["projects"] });
      }
    }
    previous.current = current;
  }, [projectId, queryClient, revisionQuery.data]);

  useEffect(() => {
    previous.current = undefined;
  }, [projectId]);

  return {
    ...revisionQuery,
    visible,
  };
}
