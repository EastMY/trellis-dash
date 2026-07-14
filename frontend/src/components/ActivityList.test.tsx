import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { ActivityEvent } from "../types";
import { ActivityList } from "./ActivityList";

function gitActivity(id: number, createdAt: string): ActivityEvent {
  return {
    id,
    projectId: "demo",
    type: "git.updated",
    source: "git",
    createdAt,
  };
}

describe("ActivityList", () => {
  it("将连续 Git 更新展示为带数量的单条记录", () => {
    render(<ActivityList items={[
      gitActivity(1, "2026-07-13T08:00:00Z"),
      gitActivity(2, "2026-07-13T09:00:00Z"),
      gitActivity(3, "2026-07-13T10:00:00Z"),
    ]} />);

    expect(screen.getByText("Git 状态更新 × 3")).toBeInTheDocument();
    expect(screen.queryByText("Git 状态更新")).not.toBeInTheDocument();
    expect(screen.getAllByText(/来源：git/)).toHaveLength(1);
  });

  it("完整活动页可渲染连续往返的蛇形时间轴", () => {
    const { container } = render(<ActivityList layout="snake" items={[
      gitActivity(1, "2026-07-13T08:00:00Z"),
      { ...gitActivity(2, "2026-07-13T09:00:00Z"), type: "session.updated", source: "filesystem" },
    ]} />);

    expect(screen.getByRole("list", { name: "活动记录时间轴" })).toBeInTheDocument();
    expect(container.querySelector(".activity-snake-path path")).toHaveAttribute("d", expect.stringContaining("M 20 18"));
    expect(container.querySelector(".activity-snake-canvas")).toHaveAttribute("data-columns", "1");
    expect(container.querySelectorAll(".activity-snake-entry[data-direction='down']")).toHaveLength(2);
  });

  it("任务事件将任务名称标签另起一行", () => {
    const { container } = render(<ActivityList items={[{
      ...gitActivity(1, "2026-07-13T08:00:00Z"),
      type: "task.created",
      source: "filesystem",
      taskKey: "07-13-deploy-elasticsearch-vectorstore",
    }]} />);

    expect(container.querySelector(".activity-item-heading--task .activity-task-key")).toHaveTextContent(
      "07-13-deploy-elasticsearch-vectorstore",
    );
  });
});
