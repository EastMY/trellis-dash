import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "../api/client";
import type { CodexUsageDays, CodexUsageResponse, CodexUsageScope } from "../types";
import { CodexUsagePage } from "./CodexUsagePage";

vi.mock("../api/client", () => ({
  api: { getCodexUsage: vi.fn() },
}));

vi.mock("../components/AppShell", () => ({
  useProjectContext: () => ({ project: { id: "demo", root: "/tmp/demo" } }),
}));

vi.mock("../components/CodexUsageBarChart", () => ({
  CodexUsageBarChart: ({ metric }: { metric: string }) => <div aria-label={`Codex 图表 ${metric}`} />,
}));

function usage(scope: CodexUsageScope, days: CodexUsageDays): CodexUsageResponse {
  return {
    scope,
    days,
    dateFrom: "2026-06-22",
    dateTo: "2026-07-21",
    totalTokens: 3000,
    totalCostUsd: 0.123456,
    costPartial: true,
    sessionCount: 2,
    skippedFiles: 1,
    items: [{ date: "2026-07-21", tokens: 3000, costUsd: 0.123456, costPartial: true }],
  };
}

function renderPage() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>
        <CodexUsagePage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.mocked(api.getCodexUsage).mockReset();
});

describe("CodexUsagePage", () => {
  it("切换范围和天数刷新查询，切换指标只改变图表", async () => {
    vi.mocked(api.getCodexUsage).mockImplementation(
      async (_projectId, scope, days) => usage(scope ?? "project", days ?? 30),
    );
    renderPage();

    await waitFor(() => expect(api.getCodexUsage).toHaveBeenCalledWith("demo", "project", 30));
    expect(await screen.findByLabelText("Codex 图表 tokens")).toBeInTheDocument();
    expect(screen.getByText("部分费用未计价")).toBeInTheDocument();

    fireEvent.click(screen.getByText("费用"));
    expect(screen.getByLabelText("Codex 图表 cost")).toBeInTheDocument();
    expect(api.getCodexUsage).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByText("全部会话"));
    await waitFor(() => expect(api.getCodexUsage).toHaveBeenCalledWith("demo", "all", 30));
    fireEvent.click(screen.getByText("最近 7 天"));
    await waitFor(() => expect(api.getCodexUsage).toHaveBeenCalledWith("demo", "all", 7));
  });

  it("零会话时保留零值汇总并显示空状态", async () => {
    vi.mocked(api.getCodexUsage).mockResolvedValue({
      ...usage("project", 30),
      totalTokens: 0,
      totalCostUsd: 0,
      sessionCount: 0,
      skippedFiles: 0,
      costPartial: false,
      items: [],
    });
    renderPage();

    expect(await screen.findByText("所选范围内暂无 Codex 使用记录")).toBeInTheDocument();
    expect(screen.getByText("暂无匹配会话")).toBeInTheDocument();
    expect(screen.getByText("总 Token")).toBeInTheDocument();
  });

  it("查询失败时只显示可重试的局部错误", async () => {
    vi.mocked(api.getCodexUsage).mockRejectedValue(new Error("统计暂不可用"));
    renderPage();

    expect(await screen.findByText("统计暂不可用")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /重\s*试/ })).toBeInTheDocument();
  });
});
