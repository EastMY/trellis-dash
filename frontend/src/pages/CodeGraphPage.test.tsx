import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { App as AntApp } from "antd";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api/client";
import type { Project } from "../types";
import { CodeGraphPage } from "./CodeGraphPage";

vi.mock("../api/client", () => ({
  api: {
    getCodeGraphStatus: vi.fn(),
    getCodeGraphStructure: vi.fn(),
    searchCodeGraphSymbols: vi.fn(),
    syncCodeGraph: vi.fn(),
  },
}));

const project: Project = {
  id: "demo",
  name: "Demo",
  root: "/tmp/demo",
  mode: "observer",
  createdAt: "2026-07-14T00:00:00Z",
  updatedAt: "2026-07-14T00:00:00Z",
  activeTaskCount: 0,
  revisions: {
    generation: "demo",
    day: "2026-07-14",
    tasks: 0,
    sessions: 0,
    git: 0,
    activity: 0,
    specs: 0,
    agents: 0,
    updatedAt: "2026-07-14T00:00:00Z",
  },
};

vi.mock("../components/AppShell", () => ({ useProjectContext: () => ({ project }) }));
vi.mock("../components/CodeGraphCanvas", () => ({
  CodeGraphCanvas: ({ rootSymbol }: { rootSymbol?: { qualifiedName: string } }) => (
    <div aria-label="测试调用链">{rootSymbol?.qualifiedName ?? "尚未选择"}</div>
  ),
}));

function renderPage(queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
  return render(
    <QueryClientProvider client={queryClient}>
      <AntApp>
        <CodeGraphPage />
      </AntApp>
    </QueryClientProvider>,
  );
}

describe("CodeGraphPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.getCodeGraphStructure).mockResolvedValue({
      items: [], total: 0, limit: 100, offset: 0, hasMore: false,
    });
    vi.mocked(api.syncCodeGraph).mockResolvedValue({
      mode: "sync", state: "running", startedAt: "2026-07-15T08:45:00Z",
    });
  });

  afterEach(() => cleanup());

  it("缺少索引时保留页面入口、原因和重试操作", async () => {
    vi.mocked(api.getCodeGraphStatus).mockResolvedValue({
      available: false,
      cliAvailable: true,
      revision: "missing",
      reason: "not_initialized",
      message: "当前项目尚未初始化 CodeGraph",
    });
    renderPage();

    expect(await screen.findByText("当前项目暂无可用 CodeGraph")).toBeInTheDocument();
    expect(screen.getByText("当前项目尚未初始化 CodeGraph")).toBeInTheDocument();
    expect(screen.getByText("请先在项目目录初始化 CodeGraph")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /同步$/ })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "重新检查" }));
    await waitFor(() => expect(api.getCodeGraphStatus).toHaveBeenCalledTimes(2));
  });

  it("状态读取失败时显示统一错误并可重试", async () => {
    vi.mocked(api.getCodeGraphStatus)
      .mockRejectedValueOnce(new Error("索引暂时不可读"))
      .mockResolvedValueOnce({ available: false, cliAvailable: true, revision: "missing", message: "尚未初始化" });
    renderPage();

    expect(await screen.findByText("索引暂时不可读")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /重新加载/ }));
    expect(await screen.findByText("当前项目暂无可用 CodeGraph")).toBeInTheDocument();
    expect(api.getCodeGraphStatus).toHaveBeenCalledTimes(2);
  });

  it("在标题区明确显示 CodeGraph 来源和本地更新时间", async () => {
    const localIndexedAt = new Date(2026, 6, 15, 8, 45).toISOString();
    vi.mocked(api.getCodeGraphStatus).mockResolvedValue({
      available: true,
      cliAvailable: true,
      revision: "v1",
      indexedAt: localIndexedAt,
      languages: [{ name: "go", fileCount: 12 }],
    });
    renderPage();

    expect(await screen.findByText(/数据来源：CodeGraph/)).toBeInTheDocument();
    expect(screen.getByText("CodeGraph 索引")).toBeInTheDocument();
    expect(screen.getByText("更新于 2026-07-15 08:45")).toBeInTheDocument();
    expect(screen.getByText("go · 12")).toBeInTheDocument();
  });

  it("CLI 缺失时禁用两个操作并明确说明原因", async () => {
    vi.mocked(api.getCodeGraphStatus).mockResolvedValue({
      available: true,
      cliAvailable: false,
      revision: "v1",
    });
    renderPage();

    expect(await screen.findByText("未检测到 CodeGraph CLI")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /同步$/ })).toBeDisabled();
    expect(screen.getByRole("button", { name: "更多 CodeGraph 同步操作" })).toBeDisabled();
  });

  it("主按钮直接同步，悬停下拉后确认完整重建", async () => {
    vi.mocked(api.getCodeGraphStatus).mockResolvedValue({
      available: true,
      cliAvailable: true,
      revision: "v1",
    });
    vi.mocked(api.syncCodeGraph).mockImplementation(async (_projectId, mode) => ({
      mode, state: "running", startedAt: "2026-07-15T08:45:00Z",
    }));
    const firstView = renderPage();

    const syncButton = await screen.findByRole("button", { name: /同步$/ });
    fireEvent.click(syncButton);
    await waitFor(() => expect(api.syncCodeGraph).toHaveBeenCalledWith("demo", "sync"));
    firstView.unmount();

    // 模拟新的空闲状态，验证整个分裂按钮悬停即可发现完整重建。
    vi.mocked(api.getCodeGraphStatus).mockResolvedValue({ available: true, cliAvailable: true, revision: "v1" });
    const secondView = renderPage();
    const secondSyncButton = await screen.findByRole("button", { name: /同步$/ });
    fireEvent.mouseEnter(secondSyncButton.closest(".ant-space-compact") as HTMLElement);
    fireEvent.click(await screen.findByText("完整重建"));
    expect(await screen.findByText(/可能耗时较长/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /取\s*消/ }));
    expect(api.syncCodeGraph).not.toHaveBeenCalledWith("demo", "rebuild");

    // 点击箭头同样可达，供键盘聚焦后执行完整重建。
    fireEvent.click(screen.getByRole("button", { name: "更多 CodeGraph 同步操作" }));
    fireEvent.click(await screen.findByText("完整重建"));
    fireEvent.click(screen.getByRole("button", { name: "开始完整重建" }));
    await waitFor(() => expect(api.syncCodeGraph).toHaveBeenCalledWith("demo", "rebuild"));
    secondView.unmount();
  });

  it("运行期间保留现有内容并禁用重复操作", async () => {
    vi.mocked(api.getCodeGraphStatus).mockResolvedValue({
      available: true,
      cliAvailable: true,
      revision: "v1",
      operation: { mode: "rebuild", state: "running", startedAt: "2026-07-15T08:45:00Z" },
    });
    renderPage();

    expect(await screen.findByText("项目结构")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /正在完整重建$/ })).toBeDisabled();
    expect(screen.getByRole("button", { name: "更多 CodeGraph 同步操作" })).toBeDisabled();
  });

  it("写库期间状态短暂 busy 时仍保留操作前的图谱", async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    vi.mocked(api.getCodeGraphStatus).mockResolvedValue({
      available: true,
      cliAvailable: true,
      revision: "v1",
      fileCount: 2,
    });
    renderPage(queryClient);
    expect(await screen.findByText("项目结构")).toBeInTheDocument();

    queryClient.setQueryData(["project", "demo", "codegraph", "status"], {
      available: false,
      cliAvailable: true,
      revision: "v1-writing",
      reason: "busy",
      message: "CodeGraph 索引正在更新",
      operation: { mode: "sync", state: "running", startedAt: "2026-07-15T08:45:00Z" },
    });

    expect(await screen.findByRole("button", { name: /正在同步$/ })).toBeDisabled();
    expect(screen.getByText("项目结构")).toBeInTheDocument();
    expect(screen.queryByText("当前项目暂无可用 CodeGraph")).not.toBeInTheDocument();
  });

  it("同步失败且 revision 未变时保留当前调用链中心", async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    vi.mocked(api.getCodeGraphStatus).mockResolvedValue({ available: true, cliAvailable: true, revision: "v1" });
    vi.mocked(api.searchCodeGraphSymbols).mockResolvedValue({
      items: [{
        id: "method:Service/run", kind: "method", name: "run", qualifiedName: "Service.run",
        filePath: "service.go", language: "go", startLine: 12, endLine: 18,
      }],
      total: 1, limit: 30, offset: 0, hasMore: false,
    });
    renderPage(queryClient);
    const input = await screen.findByPlaceholderText("搜索符号或限定名");
    fireEvent.change(input, { target: { value: "run" } });
    fireEvent.keyDown(input, { key: "Enter", code: "Enter" });
    fireEvent.click(await screen.findByRole("button", { name: "Service.run" }));
    expect(screen.getByLabelText("测试调用链")).toHaveTextContent("Service.run");

    const startedAt = "2026-07-15T08:45:00Z";
    queryClient.setQueryData(["project", "demo", "codegraph", "status"], {
      available: true, cliAvailable: true, revision: "v1",
      operation: { mode: "sync", state: "running", startedAt },
    });
    queryClient.setQueryData(["project", "demo", "codegraph", "status"], {
      available: true, cliAvailable: true, revision: "v1",
      operation: {
        mode: "sync", state: "failed", startedAt, finishedAt: "2026-07-15T08:46:00Z", message: "同步失败",
      },
    });

    expect(await screen.findByText("同步失败")).toBeInTheDocument();
    expect(screen.getByLabelText("测试调用链")).toHaveTextContent("Service.run");
  });

  it("操作成功刷新全部 CodeGraph 查询，失败时保留现有内容", async () => {
    const succeededClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const invalidate = vi.spyOn(succeededClient, "invalidateQueries");
    vi.mocked(api.getCodeGraphStatus).mockResolvedValue({
      available: true,
      cliAvailable: true,
      revision: "v2",
      operation: {
        mode: "sync", state: "succeeded", startedAt: "2026-07-15T08:45:00Z", finishedAt: "2026-07-15T08:46:00Z",
      },
    });
    const succeededView = renderPage(succeededClient);
    expect(await screen.findByText("CodeGraph 索引同步完成")).toBeInTheDocument();
    await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: ["project", "demo", "codegraph"] }));
    succeededView.unmount();

    vi.mocked(api.getCodeGraphStatus).mockResolvedValue({
      available: true,
      cliAvailable: true,
      revision: "v1",
      operation: {
        mode: "rebuild", state: "failed", startedAt: "2026-07-15T08:45:00Z",
        finishedAt: "2026-07-15T08:46:00Z", message: "重建失败，旧索引仍可用",
      },
    });
    renderPage();
    expect(await screen.findByText("重建失败，旧索引仍可用")).toBeInTheDocument();
    expect(screen.getByText("项目结构")).toBeInTheDocument();
  });

  it("所有屏幕尺寸均可在结构与调用链之间切换", async () => {
    vi.mocked(api.getCodeGraphStatus).mockResolvedValue({
      available: true,
      cliAvailable: true,
      revision: "v1",
      fileCount: 2,
      nodeCount: 5,
      edgeCount: 4,
    });
    renderPage();

    const structurePanel = (await screen.findByText("项目结构")).closest("aside");
    const graphPanel = screen.getByLabelText("测试调用链").closest("section");
    expect(structurePanel).not.toHaveAttribute("hidden");
    expect(graphPanel).toHaveAttribute("hidden");

    fireEvent.click(screen.getByText("调用链"));
    expect(structurePanel).toHaveAttribute("hidden");
    expect(graphPanel).not.toHaveAttribute("hidden");

    fireEvent.click(screen.getByText("结构与搜索"));
    expect(structurePanel).not.toHaveAttribute("hidden");
    expect(graphPanel).toHaveAttribute("hidden");
  });

  it("索引可用时启用页面高度锁定，并在卸载后清理", async () => {
    vi.mocked(api.getCodeGraphStatus).mockResolvedValue({
      available: true,
      cliAvailable: true,
      revision: "v1",
      fileCount: 2,
      nodeCount: 5,
      edgeCount: 4,
    });
    const view = renderPage();

    await screen.findByText("项目结构");
    expect(document.documentElement).toHaveClass("codegraph-page-active");
    expect(document.body).toHaveClass("codegraph-page-active");

    view.unmount();
    expect(document.documentElement).not.toHaveClass("codegraph-page-active");
    expect(document.body).not.toHaveClass("codegraph-page-active");
  });

  it("点击目录名称时递归合并唯一子目录链，并复用已加载结果", async () => {
    vi.mocked(api.getCodeGraphStatus).mockResolvedValue({
      available: true,
      cliAvailable: true,
      revision: "v1",
      fileCount: 3,
      nodeCount: 2,
      edgeCount: 1,
    });
    vi.mocked(api.getCodeGraphStructure).mockImplementation(async (_projectId, path) => {
      const pages = {
        "": [{ id: "dir:java", type: "directory" as const, name: "java", path: "java", fileCount: 3, expandable: true }],
        java: [{ id: "dir:java/com", type: "directory" as const, name: "com", path: "java/com", fileCount: 3, expandable: true }],
        "java/com": [{ id: "dir:java/com/ruoyi", type: "directory" as const, name: "ruoyi", path: "java/com/ruoyi", fileCount: 3, expandable: true }],
        "java/com/ruoyi": [
          { id: "dir:java/com/ruoyi/web", type: "directory" as const, name: "web", path: "java/com/ruoyi/web", fileCount: 2, expandable: true },
          { id: "file:java/com/ruoyi/App.java", type: "file" as const, name: "App.java", path: "java/com/ruoyi/App.java", language: "java", nodeCount: 1, expandable: true },
        ],
      };
      const items = pages[path as keyof typeof pages] ?? [];
      return { items, total: items.length, limit: 200, offset: 0, hasMore: false };
    });
    renderPage();

    fireEvent.click(await screen.findByText("java"));

    const compactName = await screen.findByText("java / com / ruoyi");
    expect(screen.getByText("web")).toBeInTheDocument();
    expect(screen.getByText("App.java")).toBeInTheDocument();
    expect(vi.mocked(api.getCodeGraphStructure).mock.calls.map((call) => call[1])).toEqual([
      "", "java", "java/com", "java/com/ruoyi",
    ]);

    fireEvent.click(compactName);
    await waitFor(() => expect(screen.queryByText("web")).not.toBeInTheDocument());
    fireEvent.click(compactName);
    expect(await screen.findByText("web")).toBeInTheDocument();
    expect(api.getCodeGraphStructure).toHaveBeenCalledTimes(4);
  });

  it("点击可展开文件名称后显示符号，并可选择符号进入调用链", async () => {
    vi.mocked(api.getCodeGraphStatus).mockResolvedValue({
      available: true,
      cliAvailable: true,
      revision: "v1",
      fileCount: 1,
      nodeCount: 1,
      edgeCount: 0,
    });
    vi.mocked(api.getCodeGraphStructure).mockImplementation(async (_projectId, path) => {
      if (!path) {
        return {
          items: [{
            id: "file:App.java",
            type: "file",
            name: "App.java",
            path: "App.java",
            language: "java",
            nodeCount: 1,
            expandable: true,
          }],
          total: 1,
          limit: 200,
          offset: 0,
          hasMore: false,
        };
      }
      return {
        items: [{
          id: "method:App.main",
          type: "symbol",
          name: "main",
          path: "App.java",
          language: "java",
          expandable: false,
          symbol: {
            id: "method:App.main",
            kind: "method",
            name: "main",
            qualifiedName: "App.main",
            filePath: "App.java",
            language: "java",
            startLine: 8,
            endLine: 10,
            signature: "main(String[] args)",
          },
        }],
        total: 1,
        limit: 200,
        offset: 0,
        hasMore: false,
      };
    });
    renderPage();

    fireEvent.click(await screen.findByText("App.java"));
    fireEvent.click(await screen.findByText("main"));

    expect(screen.getByLabelText("测试调用链")).toHaveTextContent("App.main");
    expect(vi.mocked(api.getCodeGraphStructure).mock.calls.map((call) => call[1])).toEqual(["", "App.java"]);
  });

  it("搜索唯一符号后将其设为调用链中心，并显示签名", async () => {
    vi.mocked(api.getCodeGraphStatus).mockResolvedValue({
      available: true,
      cliAvailable: true,
      revision: "v1",
      fileCount: 2,
      nodeCount: 5,
      edgeCount: 4,
    });
    vi.mocked(api.searchCodeGraphSymbols).mockResolvedValue({
      items: [{
        id: "method:Service/run",
        kind: "method",
        name: "run",
        qualifiedName: "Service.run",
        filePath: "internal/service.go",
        language: "go",
        startLine: 12,
        endLine: 18,
        signature: "run(ctx context.Context) error",
      }],
      total: 1,
      limit: 30,
      offset: 0,
      hasMore: false,
    });
    renderPage();

    const input = await screen.findByPlaceholderText("搜索符号或限定名");
    fireEvent.change(input, { target: { value: "run" } });
    fireEvent.keyDown(input, { key: "Enter", code: "Enter" });

    expect(await screen.findByText("Service.run")).toBeInTheDocument();
    expect(screen.getByText("run(ctx context.Context) error")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Service.run" }));
    expect(screen.getByLabelText("测试调用链")).toHaveTextContent("Service.run");
  });
});
