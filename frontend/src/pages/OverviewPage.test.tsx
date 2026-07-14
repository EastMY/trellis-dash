import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { App } from "antd";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "../api/client";
import type { DashboardSnapshot, Project } from "../types";
import { OverviewPage } from "./OverviewPage";

const project: Project = {
  id: "demo",
  name: "Demo",
  root: "/tmp/demo",
  mode: "observer",
  createdAt: "2026-07-13T00:00:00Z",
  updatedAt: "2026-07-13T00:00:00Z",
  activeTaskCount: 0,
  revisions: {
    generation: "demo",
    day: "2026-07-13",
    tasks: 1,
    sessions: 1,
    git: 1,
    activity: 1,
    specs: 1,
    agents: 1,
    updatedAt: "2026-07-13T00:00:00Z",
  },
};

vi.mock("../api/client", () => ({
  api: { getDashboard: vi.fn(), pushGit: vi.fn() },
}));

vi.mock("../components/AppShell", () => ({
  useProjectContext: () => ({ project }),
}));

function renderPage(dashboard: DashboardSnapshot) {
  vi.mocked(api.getDashboard).mockResolvedValue(dashboard);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <App>
        <MemoryRouter>
          <OverviewPage />
        </MemoryRouter>
      </App>
    </QueryClientProvider>,
  );
}

describe("OverviewPage Git 工作区", () => {
  afterEach(() => {
    cleanup();
    vi.mocked(api.getDashboard).mockReset();
    vi.mocked(api.pushGit).mockReset();
  });

  it("显示相对 HEAD 的代码增删行数", async () => {
    renderPage({
      project,
      statistics: { total: 0, active: 0, archived: 0, blocked: 0, completedToday: 0, byStatus: {} },
      completionTrend: [],
      gitCommitTrend: [],
      gitCommitTrendAvailable: true,
      activeTasks: [],
      sessions: [],
      recentActivity: [],
      git: {
        projectId: project.id,
        branch: "main",
        head: "0123456789abcdef",
        upstream: "origin/main",
        ahead: 0,
        behind: 0,
        modified: 1,
        added: 0,
        deleted: 0,
        linesAdded: 12,
        linesDeleted: 5,
        untracked: 1,
        conflicted: 0,
        dirty: true,
        worktrees: 1,
        updatedAt: "2026-07-13T00:00:00Z",
      },
    });

    expect(await screen.findByText("+12")).toBeInTheDocument();
    expect(screen.getByText("-5")).toBeInTheDocument();
    expect(screen.getByText(/增加行/)).toBeInTheDocument();
    expect(screen.getByText(/删除行/)).toBeInTheDocument();
  });

  it("在卡片底部推送当前分支并显示领先提交数", async () => {
    const dashboard: DashboardSnapshot = {
      project,
      statistics: { total: 0, active: 0, archived: 0, blocked: 0, completedToday: 0, byStatus: {} },
      completionTrend: [],
      gitCommitTrend: [],
      gitCommitTrendAvailable: true,
      activeTasks: [],
      sessions: [],
      recentActivity: [],
      git: {
        projectId: project.id,
        branch: "main",
        head: "0123456789abcdef",
        upstream: "origin/main",
        ahead: 25,
        behind: 0,
        modified: 1,
        added: 0,
        deleted: 0,
        linesAdded: 10,
        linesDeleted: 0,
        untracked: 4,
        conflicted: 0,
        dirty: true,
        worktrees: 1,
        updatedAt: "2026-07-13T00:00:00Z",
      },
    };
    vi.mocked(api.pushGit).mockResolvedValue({
      branch: "main",
      upstream: "origin/main",
      refreshed: true,
    });
    renderPage(dashboard);

    const button = await screen.findByRole("button", { name: "推送当前分支，共 25 个领先提交" });
    expect(button).toHaveTextContent("Push (25)");
    fireEvent.click(button);

    await waitFor(() => expect(api.pushGit).toHaveBeenCalledWith(project.id));
  });

  it("没有待推送提交时禁用按钮并显示原因", async () => {
    renderPage({
      project,
      statistics: { total: 0, active: 0, archived: 0, blocked: 0, completedToday: 0, byStatus: {} },
      completionTrend: [],
      gitCommitTrend: [],
      gitCommitTrendAvailable: true,
      activeTasks: [],
      sessions: [],
      recentActivity: [],
      git: {
        projectId: project.id,
        branch: "main",
        head: "0123456789abcdef",
        upstream: "origin/main",
        ahead: 0,
        behind: 0,
        modified: 0,
        added: 0,
        deleted: 0,
        linesAdded: 0,
        linesDeleted: 0,
        untracked: 0,
        conflicted: 0,
        dirty: false,
        worktrees: 1,
        updatedAt: "2026-07-13T00:00:00Z",
      },
    });

    const button = await screen.findByRole("button", { name: "推送当前分支" });
    expect(button).toBeDisabled();
    fireEvent.mouseOver(button.parentElement!);
    expect(api.pushGit).not.toHaveBeenCalled();
    expect(await screen.findByText("没有待推送的提交")).toBeInTheDocument();
  });
});
