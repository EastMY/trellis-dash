import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CodexUsageBarChart } from "./CodexUsageBarChart";

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

describe("CodexUsageBarChart", () => {
  const items = [
    {
      date: "2026-07-20",
      tokens: 1000,
      costUsd: 0.012,
      costBreakdown: {
        uncachedInputUsd: 0.003,
        cachedInputUsd: 0.001,
        outputUsd: 0.006,
        cacheWriteUsd: 0.002,
      },
      costPartial: false,
    },
    {
      date: "2026-07-21",
      tokens: 2500,
      costUsd: 0.025,
      costBreakdown: {
        uncachedInputUsd: 0.008,
        cachedInputUsd: 0.002,
        outputUsd: 0.011,
        cacheWriteUsd: 0.004,
      },
      costPartial: true,
    },
  ];

  it("使用 G2 interval 绘制 Token，并提供可查询的每日明细", async () => {
    render(<CodexUsageBarChart items={items} metric="tokens" />);

    expect(screen.getByRole("img", { name: "Codex 每日Token柱状图" })).toBeInTheDocument();
    expect(screen.getByText("2026-07-21：2,500 Token")).toBeInTheDocument();
    await waitFor(() => expect(g2.instances[0]?.render).toHaveBeenCalled());
    const options = g2.instances[0].options.mock.calls.at(-1)?.[0];
    expect(options.type).toBe("interval");
    expect(options.encode).toEqual({ x: "date", y: "value", color: "category" });
    expect(options.transform).toBeUndefined();
  });

  it("迷你费用图按四类费用堆叠，并保留部分未计价提示", async () => {
    render(<CodexUsageBarChart items={items} metric="cost" compact />);

    expect(screen.getByRole("img", { name: "Codex 每日费用堆叠柱状图" })).toBeInTheDocument();
    expect(screen.getByRole("list", { name: "费用分类图例" })).toHaveTextContent(
      "非缓存输入费用缓存输入费用输出费用缓存写入费用",
    );
    expect(screen.getByText(/2026-07-21：\$0\.025000.*非缓存输入费用 \$0\.008000.*缓存写入费用 \$0\.004000.*部分未计价/)).toBeInTheDocument();
    await waitFor(() => expect(g2.instances[0]?.render).toHaveBeenCalled());
    const options = g2.instances[0].options.mock.calls.at(-1)?.[0];
    expect(options.paddingLeft).toBe(0);
    expect(options.paddingRight).toBe(0);
    expect(options.paddingBottom).toBe(18);
    expect(options.scale.x.padding).toBe(0.05);
    expect(options.axis.y).toBe(false);
    expect(options.axis.x.labelFormatter("2026-07-02")).toBe("02");
    expect(options.axis.x.labelFormatter("2026-07-21")).toBe("21");
    expect(g2.Chart).toHaveBeenCalledWith(expect.objectContaining({ height: 104 }));
    expect(options.transform).toEqual([{ type: "stackY" }]);
    expect(options.data).toHaveLength(8);
    expect(options.data.slice(4)).toEqual([
      { date: "2026-07-21", value: 0.008, totalValue: 0.025, category: "非缓存输入费用" },
      { date: "2026-07-21", value: 0.002, totalValue: 0.025, category: "缓存输入费用" },
      { date: "2026-07-21", value: 0.011, totalValue: 0.025, category: "输出费用" },
      { date: "2026-07-21", value: 0.004, totalValue: 0.025, category: "缓存写入费用" },
    ]);
    expect(options.scale.color.domain).toEqual([
      "非缓存输入费用",
      "缓存输入费用",
      "输出费用",
      "缓存写入费用",
    ]);
    expect(options.legend.color).toBe(false);
    expect(options.interaction.tooltip.shared).toBe(true);
    const [categoryTooltip, totalTooltip] = options.tooltip.items;
    expect(categoryTooltip(options.data[4])).toEqual({
      name: "非缓存输入费用",
      value: "$0.008000",
    });
    expect(totalTooltip(options.data[4])).toEqual({
      name: "总费用",
      value: "$0.025000",
      color: expect.any(String),
    });
    expect([
      "缓存写入费用",
      "总费用",
      "非缓存输入费用",
      "输出费用",
      "缓存输入费用",
    ].sort((left, right) => (
      options.interaction.tooltip.sort({ name: left }) -
      options.interaction.tooltip.sort({ name: right })
    ))).toEqual([
      "非缓存输入费用",
      "缓存输入费用",
      "输出费用",
      "缓存写入费用",
      "总费用",
    ]);
  });
});
