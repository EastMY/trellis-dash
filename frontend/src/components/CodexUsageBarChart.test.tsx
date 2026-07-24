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
    { date: "2026-07-20", tokens: 1000, costUsd: 0.012, costPartial: false },
    { date: "2026-07-21", tokens: 2500, costUsd: 0.025, costPartial: true },
  ];

  it("使用 G2 interval 绘制 Token，并提供可查询的每日明细", async () => {
    render(<CodexUsageBarChart items={items} metric="tokens" />);

    expect(screen.getByRole("img", { name: "Codex 每日Token柱状图" })).toBeInTheDocument();
    expect(screen.getByText("2026-07-21：2,500 Token")).toBeInTheDocument();
    await waitFor(() => expect(g2.instances[0]?.render).toHaveBeenCalled());
    const options = g2.instances[0].options.mock.calls.at(-1)?.[0];
    expect(options.type).toBe("interval");
    expect(options.encode).toEqual({ x: "date", y: "value", color: "priceState" });
  });

  it("迷你费用图显示紧凑 X 轴并标记部分未计价", async () => {
    render(<CodexUsageBarChart items={items} metric="cost" compact />);

    expect(screen.getByText("2026-07-21：$0.025000，部分未计价")).toBeInTheDocument();
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
    expect(options.data[1].priceState).toBe("部分未计价");
  });
});
