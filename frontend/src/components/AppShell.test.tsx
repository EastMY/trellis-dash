import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeAll, describe, expect, it, vi } from "vitest";
import { api } from "../api/client";
import type { Project } from "../types";
import { AppShell, projectListPollingOptions } from "./AppShell";

vi.mock("../api/client", () => ({
  api: {
    listProjects: vi.fn(),
    getDashboard: vi.fn(),
  },
}));

vi.mock("../hooks/useProjectPolling", () => ({
  useProjectPolling: () => ({
    visible: true,
    isError: false,
    isFetching: false,
    data: { updatedAt: "2026-07-13T00:00:00Z" },
  }),
}));

vi.mock("./SidebarActivity", () => ({ SidebarActivity: () => null }));
vi.mock("./ThemeToggle", () => ({ ThemeToggle: () => null }));
vi.mock("./AddProjectModal", () => ({ AddProjectModal: () => null }));

const project = (id: string, name: string, activeTaskCount: number): Project => ({
  id,
  name,
  root: `/tmp/${id}`,
  mode: "observer",
  createdAt: "2026-07-13T00:00:00Z",
  updatedAt: "2026-07-13T00:00:00Z",
  activeTaskCount,
  revisions: {
    generation: id,
    day: "2026-07-13",
    tasks: 1,
    sessions: 0,
    git: 0,
    activity: 0,
    specs: 0,
    agents: 0,
    updatedAt: "2026-07-13T00:00:00Z",
  },
});

describe("AppShell 项目标签", () => {
  beforeAll(() => {
    // jsdom 未实现该浏览器 API，项目标签自动滚动逻辑需要一个空实现。
    Object.defineProperty(HTMLElement.prototype, "scrollIntoView", {
      configurable: true,
      value: vi.fn(),
    });
  });

  it("仅在页面可见时轮询全部项目，并在恢复前台或网络后立即刷新", () => {
    expect(projectListPollingOptions(true)).toEqual({
      refetchInterval: 10_000,
      refetchOnWindowFocus: "always",
      refetchOnReconnect: "always",
    });
    expect(projectListPollingOptions(false)).toEqual({
      refetchInterval: false,
      refetchOnWindowFocus: "always",
      refetchOnReconnect: "always",
    });
  });

  it("所有项目都直接显示活跃任务数，包括 0", async () => {
    vi.mocked(api.listProjects).mockResolvedValue([
      project("alpha", "Alpha", 2),
      project("beta", "Beta", 0),
    ]);
    vi.mocked(api.getDashboard).mockResolvedValue({
      project: project("alpha", "Alpha", 2),
      statistics: { total: 2, active: 2, archived: 0, blocked: 0, completedToday: 0, byStatus: {} },
      completionTrend: [],
      gitCommitTrend: [],
      gitCommitTrendAvailable: false,
      activeTasks: [],
      sessions: [],
      recentActivity: [],
    });
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });

    render(
      <QueryClientProvider client={queryClient}>
        <MemoryRouter initialEntries={["/projects/alpha"]}>
          <Routes>
            <Route path="/projects/:projectId" element={<AppShell />}>
              <Route index element={<div>项目内容</div>} />
            </Route>
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    expect(await screen.findByRole("button", { name: "Alpha，2 个活跃任务" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Beta，0 个活跃任务" })).toBeInTheDocument();
  });
});
