import dayjs from "dayjs";
import relativeTime from "dayjs/plugin/relativeTime";
import "dayjs/locale/zh-cn";
import type { Subtask, Task } from "../types";

dayjs.extend(relativeTime);
dayjs.locale("zh-cn");

export const statusLabels: Record<string, string> = {
  planning: "规划中",
  in_progress: "实施中",
  review: "待检查",
  completed: "已完成",
  blocked: "已阻塞",
  idle: "空闲",
  implementing: "实施中",
  checking: "检查中",
  waiting: "等待中",
};

export function statusLabel(status?: string): string {
  if (!status) return "未知";
  return statusLabels[status] ?? status.replaceAll("_", " ");
}

export function relativeDate(value?: string): string {
  if (!value) return "暂无记录";
  const date = dayjs(value);
  return date.isValid() ? date.fromNow() : value;
}

export function fullDate(value?: string): string {
  if (!value) return "-";
  const date = dayjs(value);
  return date.isValid() ? date.format("YYYY-MM-DD HH:mm:ss") : value;
}

export function shortHash(value?: string): string {
  return value ? value.slice(0, 8) : "-";
}

export function formatBytes(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  const rank = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  return `${(value / 1024 ** rank).toFixed(rank === 0 ? 0 : 1)} ${units[rank]}`;
}

export function toArray<T>(value: T[] | string | null | undefined): T[] {
  if (Array.isArray(value)) return value;
  if (typeof value !== "string" || !value.trim()) return [];
  try {
    const parsed: unknown = JSON.parse(value);
    return Array.isArray(parsed) ? (parsed as T[]) : [];
  } catch {
    return [];
  }
}

export function subtaskProgress(task: Task): { done: number; total: number } {
  const items = toArray<Subtask>(task.subtasks);
  return {
    total: items.length,
    done: items.filter((item) => item.completed || item.status === "completed").length,
  };
}

export function taskTitle(task: Task): string {
  return task.title || task.name || task.key;
}

export function eventLabel(type: string): string {
  const labels: Record<string, string> = {
    "task.created": "创建任务",
    "task.updated": "更新任务",
    "task.archived": "归档任务",
    "task.deleted": "移除任务",
    "session.updated": "Session 更新",
    "sessions.updated": "Session 更新",
    "git.updated": "Git 状态更新",
    "trellis.resources.updated": "任务文档或 Context 更新",
    "project.indexed": "完成项目索引",
    "index.error": "索引失败",
    "index.failed": "索引失败",
    "index.recovered": "索引恢复",
  };
  return labels[type] ?? type;
}
