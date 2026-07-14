import { describe, expect, it } from "vitest";
import { statusLabel, subtaskProgress, toArray } from "./format";
import type { Task } from "../types";

describe("任务格式化", () => {
  it("将 Trellis 的字符串化数组安全还原", () => {
    expect(toArray<string>('["a.ts","b.ts"]')).toEqual(["a.ts", "b.ts"]);
    expect(toArray<string>("not-json")).toEqual([]);
  });

  it("同时识别 completed 布尔值和完成状态", () => {
    const task = {
      subtasks: [
        { title: "A", completed: true },
        { title: "B", status: "completed" },
        { title: "C", status: "in_progress" },
      ],
    } as Task;
    expect(subtaskProgress(task)).toEqual({ done: 2, total: 3 });
  });

  it("保留自定义工作流状态，同时翻译内置状态", () => {
    expect(statusLabel("in_progress")).toBe("实施中");
    expect(statusLabel("security_review")).toBe("security review");
  });
});
