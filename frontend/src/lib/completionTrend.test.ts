import { describe, expect, it } from "vitest";
import { buildCompletionTrend } from "./completionTrend";

describe("buildCompletionTrend", () => {
  it("按日期合并任务完成与 Git 提交数据", () => {
    const result = buildCompletionTrend(
      [
        { date: "2026-07-10", count: 2 },
        { date: "2026-07-11", count: 0 },
      ],
      [
        { date: "2026-07-10", count: 1 },
        { date: "2026-07-12", count: 3 },
      ],
    );

    expect(result.data).toEqual([
      { date: "2026-07-10", completionCount: 2, gitCommitCount: 1 },
      { date: "2026-07-11", completionCount: 0, gitCommitCount: 0 },
      { date: "2026-07-12", completionCount: 0, gitCommitCount: 3 },
    ]);
    expect(result.completionTotal).toBe(2);
    expect(result.gitCommitTotal).toBe(4);
    expect(result.maxCount).toBe(3);
  });

  it("忽略无效日期并将异常计数归零", () => {
    const result = buildCompletionTrend(
      [
        { date: "invalid", count: 5 },
        { date: "2026-02-30", count: 3 },
        { date: "2026-07-12", count: -1 },
      ],
      [{ date: "2026-07-12", count: Number.POSITIVE_INFINITY }],
    );

    expect(result.data).toEqual([
      { date: "2026-07-12", completionCount: 0, gitCommitCount: 0 },
    ]);
    expect(result.maxCount).toBe(0);
  });
});
