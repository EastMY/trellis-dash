import { describe, expect, it } from "vitest";
import type { ActivityEvent } from "../types";
import { groupActivityEvents } from "./activity";

function activity(id: number, type: string, createdAt: string): ActivityEvent {
  return {
    id,
    projectId: "demo",
    type,
    source: type === "git.updated" ? "git" : "filesystem",
    createdAt,
  };
}

describe("groupActivityEvents", () => {
  it("按时间与事件 ID 倒序排列", () => {
    const groups = groupActivityEvents([
      activity(1, "task.updated", "2026-07-13T08:00:00Z"),
      activity(3, "session.updated", "2026-07-13T09:00:00Z"),
      activity(2, "task.archived", "2026-07-13T09:00:00Z"),
    ]);

    expect(groups.map(({ event }) => event.id)).toEqual([3, 2, 1]);
  });

  it("只合并相邻的 Git 状态更新，并保留该组最新事件", () => {
    const groups = groupActivityEvents([
      activity(1, "git.updated", "2026-07-13T08:00:00Z"),
      activity(2, "git.updated", "2026-07-13T09:00:00Z"),
      activity(3, "task.updated", "2026-07-13T10:00:00Z"),
      activity(4, "git.updated", "2026-07-13T11:00:00Z"),
      activity(5, "git.updated", "2026-07-13T12:00:00Z"),
    ]);

    expect(groups.map(({ event, count }) => ({ id: event.id, count }))).toEqual([
      { id: 5, count: 2 },
      { id: 3, count: 1 },
      { id: 2, count: 2 },
    ]);
  });

  it("能合并来自不同分页数组但排序后仍连续的 Git 更新", () => {
    const latestPage = [
      activity(4, "git.updated", "2026-07-13T11:00:00Z"),
      activity(5, "git.updated", "2026-07-13T12:00:00Z"),
    ];
    const olderPage = [
      activity(2, "task.updated", "2026-07-13T09:00:00Z"),
      activity(3, "git.updated", "2026-07-13T10:00:00Z"),
    ];

    const groups = groupActivityEvents([...latestPage, ...olderPage]);

    expect(groups[0]).toMatchObject({ event: { id: 5 }, count: 3 });
    expect(groups[1]).toMatchObject({ event: { id: 2 }, count: 1 });
  });
});
