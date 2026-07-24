import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { App } from "antd";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "../api/client";
import type { CodexUsageResponse, DashboardSnapshot, Project } from "../types";
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
  api: { getDashboard: vi.fn(), getCodexUsage: vi.fn(), pushGit: vi.fn() },
}));

vi.mock("../components/CompletionTrendChart", () => ({
  CompletionTrendChart: () => <div>项目趋势图</div>,
}));

vi.mock("../components/CodexUsageBarChart", () => ({
  CodexUsageBarChart: ({ metric }: { metric: "tokens" | "cost" }) => (
    <div aria-label={`Codex 迷你${metric === "tokens" ? "Token" : "费用"}柱状图`} />
  ),
}));

vi.mock("../components/AppShell", () => ({
  useProjectContext: () => ({ project }),
}));

function renderPage(dashboard: DashboardSnapshot, codexResult: CodexUsageResponse | Error = codexUsage) {
  vi.mocked(api.getDashboard).mockResolvedValue(dashboard);
  if (codexResult instanceof Error) {
    vi.mocked(api.getCodexUsage).mockRejectedValue(codexResult);
  } else {
    vi.mocked(api.getCodexUsage).mockResolvedValue(codexResult);
  }
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

const codexUsage: CodexUsageResponse = {
  scope: "project",
  days: 30,
  dateFrom: "2026-06-22",
  dateTo: "2026-07-21",
  totalTokens: 1234,
  totalCostUsd: 0.012345,
  costPartial: false,
  sessionCount: 2,
  skippedFiles: 0,
  items: [{ date: "2026-07-21", tokens: 1234, costUsd: 0.012345, costPartial: false }],
};

describe("OverviewPage Git 工作区", () => {
  afterEach(() => {
    cleanup();
    vi.mocked(api.getDashboard).mockReset();
    vi.mocked(api.getCodexUsage).mockReset();
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
    }, { ...codexUsage, costPartial: true, skippedFiles: 1 });

    expect(await screen.findByText("+12")).toBeInTheDocument();
    expect(screen.getByText("-5")).toBeInTheDocument();
    expect(screen.getByText(/增加行/)).toBeInTheDocument();
    expect(screen.getByText(/删除行/)).toBeInTheDocument();
    expect(await screen.findByText("Codex 使用统计")).toBeInTheDocument();
    expect(screen.getByText("最近 30 天")).toBeInTheDocument();
    expect(screen.getByLabelText("统计范围")).toHaveTextContent("当前项目");
    expect(screen.getByLabelText("统计范围")).toHaveTextContent("全部会话");
    expect(screen.queryByText(/个会话 ·/)).not.toBeInTheDocument();
    expect(screen.queryByText("部分费用未计价")).not.toBeInTheDocument();
    expect(screen.queryByText("已跳过 1 个文件")).not.toBeInTheDocument();
    expect(screen.getByText("今日费用")).toBeInTheDocument();
    expect(screen.getByLabelText(/今日费用 0\.012345 美元/)).toBeInTheDocument();
    expect(screen.getByLabelText("Codex 迷你Token柱状图")).toBeInTheDocument();

    fireEvent.click(screen.getByText("费用"));
    expect(screen.getByLabelText("Codex 迷你费用柱状图")).toBeInTheDocument();
    expect(api.getCodexUsage).toHaveBeenCalledWith(project.id, "project", 30);
    expect(api.getCodexUsage).toHaveBeenCalledTimes(1);

    vi.mocked(api.getCodexUsage).mockResolvedValueOnce({ ...codexUsage, scope: "all", totalTokens: 4321 });
    fireEvent.click(screen.getByText("全部会话"));
    await waitFor(() => expect(api.getCodexUsage).toHaveBeenCalledWith(project.id, "all", 30));
    expect(await screen.findByLabelText(/总 Token 4321/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /查看详情/ })).toHaveAttribute("href", "/codex-usage");
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

  it("Codex 统计失败时只降级对应区域", async () => {
    renderPage({
      project,
      statistics: { total: 0, active: 0, archived: 0, blocked: 0, completedToday: 0, byStatus: {} },
      completionTrend: [],
      gitCommitTrend: [],
      gitCommitTrendAvailable: true,
      activeTasks: [],
      sessions: [],
      recentActivity: [],
    }, new Error("Codex 统计暂不可用"));

    expect(await screen.findByText("Codex 统计暂不可用")).toBeInTheDocument();
    expect(screen.getByText("项目概览")).toBeInTheDocument();
    expect(screen.getByText("当前没有活跃任务")).toBeInTheDocument();
  });
});
