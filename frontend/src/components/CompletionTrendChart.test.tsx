import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CompletionTrendChart } from "./CompletionTrendChart";

const g2 = vi.hoisted(() => ({
  instances: [] as Array<{
    options: ReturnType<typeof vi.fn>;
    render: ReturnType<typeof vi.fn>;
    destroy: ReturnType<typeof vi.fn>;
  }>,
  Chart: vi.fn(function Chart() {
    const instance = {
      options: vi.fn(),
      render: vi.fn().mockResolvedValue(undefined),
      destroy: vi.fn(),
    };
    g2.instances.push(instance);
    return instance;
  }),
}));

vi.mock("../lib/g2", () => ({ Chart: g2.Chart }));

afterEach(() => {
  cleanup();
  g2.instances.length = 0;
  g2.Chart.mockClear();
});

describe("CompletionTrendChart", () => {
  it("使用 G2 双折线并保留汇总与每日可访问文本", async () => {
    render(
      <CompletionTrendChart
        completionItems={[
          { date: "2026-07-10", count: 2 },
          { date: "2026-07-11", count: 0 },
        ]}
        gitItems={[
          { date: "2026-07-10", count: 1 },
          { date: "2026-07-11", count: 3 },
        ]}
        gitAvailable
      />,
    );

    expect(screen.getByText("90 天项目趋势")).toBeInTheDocument();
    expect(screen.queryByText("每日完成任务数与当前 HEAD 可达的 Git 提交数")).not.toBeInTheDocument();
    expect(screen.getByLabelText("2026-07-10，完成任务 2 个")).toBeInTheDocument();
    expect(screen.getByLabelText("2026-07-11，Git 提交 3 次")).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "最近 90 天每日完成任务与 Git 提交折线图" })).toBeInTheDocument();

    await waitFor(() => expect(g2.instances[0]?.render).toHaveBeenCalled());
    expect(g2.Chart).toHaveBeenCalledWith(expect.objectContaining({ height: 126 }));
    const options = g2.instances[0].options.mock.calls.at(-1)?.[0];
    expect(options.type).toBe("view");
    expect(options.children.map((child: { type: string }) => child.type)).toEqual(["line", "point"]);
    expect(options.scale.color.domain).toEqual(["完成任务", "Git 提交"]);
    expect(options.paddingLeft).toBe(0);
    expect(options.paddingRight).toBe(0);
    expect(options.paddingBottom).toBe(16);
    expect(options.scale.x.padding).toBe(0);
    expect(options.axis.y).toBe(false);
    expect(options.axis.x.labelFormatter("2026-07-02")).toBe("02");
    expect(options.axis.x.labelFormatter("2026-07-10")).toBe("10");
  });

  it("Git 失败时只给 G2 任务序列并明确提示不可用", async () => {
    render(
      <CompletionTrendChart
        completionItems={[{ date: "2026-07-10", count: 2 }]}
        gitItems={[]}
        gitAvailable={false}
      />,
    );

    expect(screen.getByText("Git 数据暂不可用")).toBeInTheDocument();
    expect(screen.getByLabelText("2026-07-10，完成任务 2 个")).toBeInTheDocument();
    expect(screen.queryByLabelText(/Git 提交 .*次/)).not.toBeInTheDocument();
    await waitFor(() => expect(g2.instances[0]?.render).toHaveBeenCalled());
    expect(g2.instances[0].options.mock.calls.at(-1)?.[0].data).toEqual([
      { date: "2026-07-10", count: 2, series: "完成任务" },
    ]);
  });
});
