import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ActivityEvent } from "../types";
import { SidebarActivity } from "./SidebarActivity";

function activity(id: number): ActivityEvent {
  return {
    id,
    projectId: "demo",
    type: "project.scan",
    source: `source-${id}`,
    createdAt: "2026-07-13T08:00:00Z",
  };
}

describe("SidebarActivity", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("使用 Ant Design 时间轴展示活动并可查看全部", () => {
    const onViewAll = vi.fn();
    const { container } = render(
      <SidebarActivity
        items={[1, 2, 3, 4, 5, 6].map(activity)}
        onRetry={vi.fn()}
        onViewAll={onViewAll}
      />,
    );

    const visibleTimeline = container.querySelector<HTMLElement>(".sider-activity-body > .sider-activity-timeline");
    expect(visibleTimeline).not.toBeNull();
    expect(within(visibleTimeline!).getAllByRole("listitem")).toHaveLength(6);
    expect(within(visibleTimeline!).getByText("source-1")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /查看全部/ }));
    expect(onViewAll).toHaveBeenCalledOnce();
  });

  it("按容器实际高度动态展示可完整容纳的活动数量", () => {
    let availableHeight = 126;
    vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockImplementation(function (this: HTMLElement) {
      return this.classList.contains("sider-activity-body") ? availableHeight : 0;
    });
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
      const item = this.closest?.(".ant-timeline-item");
      const timeline = item?.closest(".ant-timeline");
      const index = item && timeline
        ? Array.from(timeline.querySelectorAll(".ant-timeline-item")).indexOf(item)
        : -1;
      const bottom = index >= 0 ? (index + 1) * 40 : 0;
      return {
        x: 0, y: 0, top: 0, right: 0, bottom,
        left: 0, width: 0, height: bottom, toJSON: () => ({}),
      };
    });

    const { container } = render(
      <SidebarActivity
        items={[1, 2, 3, 4, 5, 6].map(activity)}
        onRetry={vi.fn()}
        onViewAll={vi.fn()}
      />,
    );
    const visibleTimeline = container.querySelector<HTMLElement>(".sider-activity-body > .sider-activity-timeline");

    expect(within(visibleTimeline!).getAllByRole("listitem")).toHaveLength(3);
    availableHeight = 206;
    fireEvent(window, new Event("resize"));
    expect(within(visibleTimeline!).getAllByRole("listitem")).toHaveLength(5);
  });

  it("按时间倒序合并连续 Git 更新后再截取最近五条", () => {
    const gitItems: ActivityEvent[] = [1, 2, 3].map((id) => ({
      id,
      projectId: "demo",
      type: "git.updated",
      source: "git",
      createdAt: `2026-07-13T${String(id + 7).padStart(2, "0")}:00:00Z`,
    }));

    render(
      <SidebarActivity
        items={[{ ...activity(4), createdAt: "2026-07-13T07:00:00Z" }, ...gitItems]}
        onRetry={vi.fn()}
        onViewAll={vi.fn()}
      />,
    );

    const visibleTimeline = document.querySelector<HTMLElement>(".sider-activity-body > .sider-activity-timeline");
    expect(within(visibleTimeline!).getByText("Git 状态更新 × 3")).toBeInTheDocument();
    expect(within(visibleTimeline!).getAllByRole("listitem")).toHaveLength(2);
  });

  it("加载失败时提供重试入口", () => {
    const onRetry = vi.fn();
    render(
      <SidebarActivity
        items={[]}
        error={new Error("network")}
        onRetry={onRetry}
        onViewAll={vi.fn()}
      />,
    );

    expect(screen.getByRole("alert")).toHaveTextContent("最近活动加载失败");
    fireEvent.click(screen.getByRole("button", { name: /重试/ }));
    expect(onRetry).toHaveBeenCalledOnce();
  });
});
