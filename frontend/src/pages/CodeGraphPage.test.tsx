import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api/client";
import type { Project } from "../types";
import { CodeGraphPage } from "./CodeGraphPage";

vi.mock("../api/client", () => ({
  api: {
    getCodeGraphStatus: vi.fn(),
    getCodeGraphStructure: vi.fn(),
    searchCodeGraphSymbols: vi.fn(),
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

function renderPage() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <CodeGraphPage />
    </QueryClientProvider>,
  );
}

describe("CodeGraphPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.getCodeGraphStructure).mockResolvedValue({
      items: [], total: 0, limit: 100, offset: 0, hasMore: false,
    });
  });

  afterEach(() => cleanup());

  it("缺少索引时保留页面入口、原因和重试操作", async () => {
    vi.mocked(api.getCodeGraphStatus).mockResolvedValue({
      available: false,
      revision: "missing",
      reason: "not_initialized",
      message: "当前项目尚未初始化 CodeGraph",
    });
    renderPage();

    expect(await screen.findByText("当前项目暂无可用 CodeGraph")).toBeInTheDocument();
    expect(screen.getByText("当前项目尚未初始化 CodeGraph")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "重新检查" }));
    await waitFor(() => expect(api.getCodeGraphStatus).toHaveBeenCalledTimes(2));
  });

  it("状态读取失败时显示统一错误并可重试", async () => {
    vi.mocked(api.getCodeGraphStatus)
      .mockRejectedValueOnce(new Error("索引暂时不可读"))
      .mockResolvedValueOnce({ available: false, revision: "missing", message: "尚未初始化" });
    renderPage();

    expect(await screen.findByText("索引暂时不可读")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /重新加载/ }));
    expect(await screen.findByText("当前项目暂无可用 CodeGraph")).toBeInTheDocument();
    expect(api.getCodeGraphStatus).toHaveBeenCalledTimes(2);
  });

  it("所有屏幕尺寸均可在结构与调用链之间切换", async () => {
    vi.mocked(api.getCodeGraphStatus).mockResolvedValue({
      available: true,
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

  it("搜索唯一符号后将其设为调用链中心，并显示签名", async () => {
    vi.mocked(api.getCodeGraphStatus).mockResolvedValue({
      available: true,
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
