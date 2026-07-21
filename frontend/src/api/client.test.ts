import { afterEach, describe, expect, it, vi } from "vitest";
import { api, clearApiCache } from "./client";

afterEach(() => {
  vi.unstubAllGlobals();
  clearApiCache();
});

describe("API 客户端", () => {
  it("读取服务端目录选择能力", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({
      platform: "darwin",
      directoryPicker: true,
    }), { status: 200, headers: { "Content-Type": "application/json" } })));

    await expect(api.getSystemCapabilities()).resolves.toEqual({
      platform: "darwin",
      directoryPicker: true,
    });
  });

  it("Codex 统计接口编码项目 ID 和全部筛选条件", async () => {
    const response = {
      scope: "all",
      days: 90,
      dateFrom: "2026-04-23",
      dateTo: "2026-07-21",
      totalTokens: 12,
      totalCostUsd: 0.001,
      costPartial: false,
      sessionCount: 1,
      skippedFiles: 0,
      items: [],
    };
    const fetchMock = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      expect(url).toContain("/projects/demo%20project/codex-usage");
      expect(url).toContain("scope=all");
      expect(url).toContain("days=90");
      return Promise.resolve(new Response(JSON.stringify(response), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }));
    });
    vi.stubGlobal("fetch", fetchMock);

    await expect(api.getCodexUsage("demo project", "all", 90)).resolves.toEqual(response);
  });

  it("目录选择取消时返回空结果", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(null, { status: 204 })));
    await expect(api.selectDirectory()).resolves.toBeUndefined();
  });

  it("兼容后端 resources revision 包装", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({
      projectId: "demo",
      resources: { tasks: 2, sessions: 3, git: 4, activity: 5, specs: 6, agents: 7 },
      updatedAt: "2026-07-10T10:00:00Z",
    }), { status: 200, headers: { "Content-Type": "application/json" } })));

    await expect(api.getRevision("demo")).resolves.toMatchObject({
      tasks: 2,
      sessions: 3,
      updatedAt: "2026-07-10T10:00:00Z",
    });
  });

  it("解析统一 error envelope", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({
      error: { code: "project_not_found", message: "项目不存在" },
    }), { status: 404, headers: { "Content-Type": "application/json" } })));

    await expect(api.getDashboard("missing")).rejects.toMatchObject({
      status: 404,
      code: "project_not_found",
      message: "项目不存在",
    });
  });

  it("Diff 使用 ETag 后可复用 304 响应", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response("diff --git a/a b/a\n", {
        status: 200,
        headers: { "Content-Type": "text/plain", ETag: '"diff-v1"' },
      }))
      .mockImplementationOnce((_input: RequestInfo | URL, init?: RequestInit) => {
        expect(new Headers(init?.headers).get("If-None-Match")).toBe('"diff-v1"');
        return Promise.resolve(new Response(null, { status: 304 }));
      });
    vi.stubGlobal("fetch", fetchMock);

    await expect(api.getGitDiff("demo", "a")).resolves.toContain("diff --git");
    await expect(api.getGitDiff("demo", "a")).resolves.toContain("diff --git");
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("暂存区 Diff 会传 staged=true", async () => {
    const fetchMock = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      expect(String(input)).toContain("staged=true");
      return Promise.resolve(new Response("staged diff", { status: 200, headers: { "Content-Type": "text/plain" } }));
    });
    vi.stubGlobal("fetch", fetchMock);
    await expect(api.getGitDiff("demo", "a.txt", true)).resolves.toBe("staged diff");
  });

  it("Git Push 使用 POST 请求", async () => {
    const fetchMock = vi.fn().mockImplementation((_input: RequestInfo | URL, init?: RequestInit) => {
      expect(init?.method).toBe("POST");
      return Promise.resolve(new Response(JSON.stringify({
        branch: "main",
        upstream: "origin/main",
        refreshed: true,
      }), { status: 200, headers: { "Content-Type": "application/json" } }));
    });
    vi.stubGlobal("fetch", fetchMock);

    await expect(api.pushGit("demo")).resolves.toMatchObject({
      branch: "main",
      upstream: "origin/main",
      refreshed: true,
    });
    expect(String(fetchMock.mock.calls[0][0])).toContain("/projects/demo/git/push");
  });

  it("CodeGraph 关系接口编码符号 ID 并保留分页方向", async () => {
    const fetchMock = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      expect(url).toContain("/projects/demo/codegraph/symbols/method%3AService%2Frun/relations");
      expect(url).toContain("direction=callers");
      expect(url).toContain("limit=50");
      expect(url).toContain("offset=10");
      return Promise.resolve(new Response(JSON.stringify({
        symbol: { id: "method:Service/run" },
        direction: "callers",
        items: [],
        total: 0,
        limit: 50,
        offset: 10,
        hasMore: false,
      }), { status: 200, headers: { "Content-Type": "application/json" } }));
    });
    vi.stubGlobal("fetch", fetchMock);

    await expect(api.getCodeGraphRelations("demo", "method:Service/run", "callers", 50, 10))
      .resolves.toMatchObject({ direction: "callers", offset: 10 });
  });

  it("CodeGraph 同步接口只提交固定 mode 并使用 POST", async () => {
    const fetchMock = vi.fn().mockImplementation((input: RequestInfo | URL, init?: RequestInit) => {
      expect(String(input)).toContain("/projects/demo%20project/codegraph/sync");
      expect(init?.method).toBe("POST");
      expect(JSON.parse(String(init?.body))).toEqual({ mode: "rebuild" });
      return Promise.resolve(new Response(JSON.stringify({
        mode: "rebuild",
        state: "running",
        startedAt: "2026-07-15T08:45:00Z",
      }), { status: 202, headers: { "Content-Type": "application/json" } }));
    });
    vi.stubGlobal("fetch", fetchMock);

    await expect(api.syncCodeGraph("demo project", "rebuild")).resolves.toMatchObject({
      mode: "rebuild",
      state: "running",
    });
  });

  it("删除项目后清空旧 generation 的 ETag 缓存", async () => {
    const dashboard = { project: { id: "demo" }, statistics: {}, activeTasks: [], sessions: [], recentActivity: [] };
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify(dashboard), {
        status: 200,
        headers: { "Content-Type": "application/json", ETag: '"dashboard-old"' },
      }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockImplementationOnce((_input: RequestInfo | URL, init?: RequestInit) => {
        expect(new Headers(init?.headers).has("If-None-Match")).toBe(false);
        return Promise.resolve(new Response(JSON.stringify(dashboard), {
          status: 200,
          headers: { "Content-Type": "application/json", ETag: '"dashboard-new"' },
        }));
      });
    vi.stubGlobal("fetch", fetchMock);

    await api.getDashboard("demo");
    await api.deleteProject("demo");
    await api.getDashboard("demo");
  });
});
