import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api/client";
import { useAppStore } from "../store/app";
import type { Project } from "../types";
import { AppShell, orderProjects, projectListPollingOptions } from "./AppShell";

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

vi.mock("./SidebarActivity", () => ({
  SidebarActivity: ({ onViewAll }: { onViewAll: () => void }) => (
    <button onClick={onViewAll}>查看全部活动</button>
  ),
}));
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

  beforeEach(() => {
    window.localStorage.clear();
    useAppStore.setState({ projectOrder: [] });
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("按已保存顺序展示项目，并把新增项目追加在末尾", () => {
    const projects = [
      project("alpha", "Alpha", 2),
      project("beta", "Beta", 0),
      project("gamma", "Gamma", 1),
    ];

    expect(orderProjects(projects, ["beta", "missing", "alpha"]).map((item) => item.id))
      .toEqual(["beta", "alpha", "gamma"]);
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
              <Route path="activity" element={<div>活动页面内容</div>} />
            </Route>
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    expect(await screen.findByRole("button", { name: "Alpha，2 个活跃任务" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Beta，0 个活跃任务" })).toBeInTheDocument();
    expect(screen.getByText("Codex 统计")).toBeInTheDocument();
    expect(screen.queryByText("活动记录")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "查看全部活动" }));
    expect(await screen.findByText("活动页面内容")).toBeInTheDocument();
  });

  it("拖动项目标签后立即重排并持久化顺序", async () => {
    vi.mocked(api.listProjects).mockResolvedValue([
      project("alpha", "Alpha", 2),
      project("beta", "Beta", 0),
      project("gamma", "Gamma", 1),
    ]);
    vi.mocked(api.getDashboard).mockResolvedValue({
      project: project("alpha", "Alpha", 2),
      statistics: { total: 3, active: 3, archived: 0, blocked: 0, completedToday: 0, byStatus: {} },
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

    const alpha = await screen.findByRole("button", { name: "Alpha，2 个活跃任务" });
    const gamma = screen.getByRole("button", { name: "Gamma，1 个活跃任务" });
    const transfer = {
      effectAllowed: "uninitialized",
      dropEffect: "none",
      setData: vi.fn(),
      getData: vi.fn(() => "gamma"),
    };
    fireEvent.dragStart(gamma, { dataTransfer: transfer });
    fireEvent.dragOver(alpha, { dataTransfer: transfer });
    fireEvent.drop(alpha, { dataTransfer: transfer });

    const tagGroup = screen.getByRole("group", { name: "切换并排序项目" });
    expect(within(tagGroup).getAllByRole("button").map((button) => button.getAttribute("aria-label")))
      .toEqual([
        "Gamma，1 个活跃任务",
        "Alpha，2 个活跃任务",
        "Beta，0 个活跃任务",
      ]);
    expect(useAppStore.getState().projectOrder).toEqual(["gamma", "alpha", "beta"]);
    expect(JSON.parse(window.localStorage.getItem("trellis-dashboard-preferences") ?? "{}").state.projectOrder)
      .toEqual(["gamma", "alpha", "beta"]);
  });
});
