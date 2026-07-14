import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { CompletionTrendChart } from "./CompletionTrendChart";

afterEach(cleanup);

describe("CompletionTrendChart", () => {
  it("展示双曲线汇总与每日可访问提示", () => {
    const { container } = render(
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
    expect(screen.getByLabelText("2026-07-10，完成任务 2 个")).toBeInTheDocument();
    expect(screen.getByLabelText("2026-07-11，Git 提交 3 次")).toBeInTheDocument();
    expect(container.querySelector(".completion-trend-chart")).toHaveAttribute("viewBox", "0 0 860 112");
    expect(container.querySelector(".trend-line-completion")?.tagName.toLowerCase()).toBe("path");
    expect(container.querySelector(".trend-line-completion")?.getAttribute("d")).toContain(" C ");
    expect(container.querySelector("polyline")).not.toBeInTheDocument();
  });

  it("Git 失败时保留任务曲线并明确提示不可用", () => {
    const { container } = render(
      <CompletionTrendChart
        completionItems={[{ date: "2026-07-10", count: 2 }]}
        gitItems={[]}
        gitAvailable={false}
      />,
    );

    expect(screen.getByText("Git 数据暂不可用")).toBeInTheDocument();
    expect(screen.getByLabelText("2026-07-10，完成任务 2 个")).toBeInTheDocument();
    expect(container.querySelector(".trend-line-git")).not.toBeInTheDocument();
  });
});
