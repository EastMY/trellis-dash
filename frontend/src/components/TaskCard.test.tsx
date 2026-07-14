import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";
import type { Task } from "../types";
import { TaskCard } from "./TaskCard";

const task = {
  projectId: "demo",
  key: "07-13-compact-card",
  title: "提高任务卡片信息密度",
  description: "保留全部信息并利用右侧留白",
  priority: "P1",
  status: "in_progress",
  runtimePhase: "implementing",
  modifiedAt: "2026-07-13T00:00:00Z",
  assignee: "yunnnn",
  branch: "feat/compact-cards",
  subtasks: [
    { name: "调整布局", completed: true },
    { name: "验证窄屏", completed: false },
  ],
  artifactCount: 2,
  contextIssues: 0,
  activeSessions: 1,
} as Task;

describe("TaskCard", () => {
  it("紧凑布局仍保留任务信息和详情入口", () => {
    const { container } = render(
      <MemoryRouter>
        <TaskCard task={task} />
      </MemoryRouter>,
    );

    expect(screen.getByText("P1")).toBeInTheDocument();
    expect(screen.getByText("提高任务卡片信息密度")).toBeInTheDocument();
    expect(screen.getByText("保留全部信息并利用右侧留白")).toBeInTheDocument();
    expect(screen.getByText("实施中")).toBeInTheDocument();
    expect(screen.getByText("1/2")).toBeInTheDocument();
    expect(screen.getByText("yunnnn")).toBeInTheDocument();
    expect(screen.getByText("feat/compact-cards")).toBeInTheDocument();
    expect(container.querySelector(".task-card-body")).toBeInTheDocument();
    expect(screen.getByRole("link")).toHaveAttribute("href", "/projects/demo/tasks/07-13-compact-card");
  });
});
