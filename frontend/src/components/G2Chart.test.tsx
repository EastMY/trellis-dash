import { cleanup, render, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { G2Chart } from "./G2Chart";

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
  document.documentElement.removeAttribute("data-theme");
});

describe("G2Chart", () => {
  it("创建后渲染、配置变化时更新，并在卸载时销毁", async () => {
    const firstOptions = () => ({ type: "interval" as const, data: [{ value: 1 }] });
    const secondOptions = () => ({ type: "interval" as const, data: [{ value: 2 }] });
    const view = render(
      <G2Chart ariaLabel="测试图表" height={100} buildOptions={firstOptions} />,
    );

    await waitFor(() => expect(g2.instances[0]?.render).toHaveBeenCalledTimes(1));
    expect(g2.Chart).toHaveBeenCalledWith(expect.objectContaining({ autoFit: true, height: 100 }));

    view.rerender(<G2Chart ariaLabel="测试图表" height={100} buildOptions={secondOptions} />);
    await waitFor(() => expect(g2.instances[0].render).toHaveBeenCalledTimes(2));
    expect(g2.instances[0].options.mock.calls.at(-1)?.[0].data).toEqual([{ value: 2 }]);

    document.documentElement.dataset.theme = "light";
    await waitFor(() => expect(g2.instances[0].render).toHaveBeenCalledTimes(3));

    view.unmount();
    expect(g2.instances[0].destroy).toHaveBeenCalledTimes(1);
  });
});
