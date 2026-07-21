import { describe, expect, it } from "vitest";
import { projectSectionPath } from "./navigation";

describe("projectSectionPath", () => {
  it.each([
    ["/projects/yuni", ""],
    ["/projects/yuni/tasks", "/tasks"],
    ["/projects/yuni/tasks/archive", "/tasks/archive"],
    ["/projects/yuni/sessions", "/sessions"],
    ["/projects/yuni/git", "/git"],
    ["/projects/yuni/codex-usage", "/codex-usage"],
    ["/projects/yuni/activity", "/activity"],
    ["/projects/yuni/codegraph", "/codegraph"],
    ["/projects/yuni/settings", "/settings"],
  ])("保留一级功能页：%s", (pathname, expected) => {
    expect(projectSectionPath(pathname)).toBe(expected);
  });

  it("从任务详情切换项目时回到目标项目的任务列表", () => {
    expect(projectSectionPath("/projects/yuni/tasks/07-12-example-task")).toBe("/tasks");
  });
});
