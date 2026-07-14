import { describe, expect, it } from "vitest";
import type { CodeGraphRelation, CodeGraphRelationPage, CodeGraphSymbol } from "../types";
import {
  codeGraphKindLabel,
  codeGraphExpansionKey,
  deriveCodeGraph,
  layoutCodeGraph,
  MAX_CODEGRAPH_EDGES,
  MAX_CODEGRAPH_NODES,
  nearestDepth,
  pruneCodeGraphExpansions,
} from "./codeGraph";

const symbol = (id: string, filePath: string): CodeGraphSymbol => ({
  id,
  kind: "function",
  name: id,
  qualifiedName: id,
  filePath,
  language: "go",
  startLine: 1,
  endLine: 2,
});

describe("代码图谱布局", () => {
  it("调用方、中心节点和被调用方按层级从左到右排列", () => {
    const result = layoutCodeGraph([
      { symbol: symbol("root", "root.go"), depth: 0 },
      { symbol: symbol("caller", "caller.go"), depth: -1 },
      { symbol: symbol("callee", "callee.go"), depth: 1 },
    ]);
    const positions = Object.fromEntries(result.map((item) => [item.symbol.id, item.x]));
    expect(positions.caller).toBeLessThan(positions.root);
    expect(positions.callee).toBeGreaterThan(positions.root);
    expect(positions.callee - positions.root).toBe(288);
  });

  it("同层节点稳定排序，并优先保留离根更近的深度", () => {
    const result = layoutCodeGraph([
      { symbol: symbol("z", "z.go"), depth: 1 },
      { symbol: symbol("a", "a.go"), depth: 1 },
    ]);
    expect(result.map((item) => item.symbol.id)).toEqual(["a", "z"]);
    expect(nearestDepth(3, -1)).toBe(-1);
    expect(nearestDepth(1, 2)).toBe(1);
    expect(codeGraphKindLabel("method")).toBe("方法");
    expect(codeGraphKindLabel("custom")).toBe("custom");
  });

  it("关系投影对环和重复边去重", () => {
    const root = symbol("root", "root.go");
    const callee = symbol("callee", "callee.go");
    const relation: CodeGraphRelation = {
      id: 1, kind: "calls", direction: "callees", source: root, target: callee,
    };
    const page: CodeGraphRelationPage = {
      symbol: root, direction: "callees", items: [relation, relation],
      total: 2, limit: 20, offset: 0, hasMore: false,
    };
    const cycle: CodeGraphRelationPage = {
      symbol: callee,
      direction: "callees",
      items: [{ id: 2, kind: "calls", direction: "callees", source: callee, target: root }],
      total: 1,
      limit: 20,
      offset: 0,
      hasMore: false,
    };
    const graph = deriveCodeGraph(root, [
      { symbol: root, direction: "callees", depth: 0, page },
      { symbol: callee, direction: "callees", depth: 1, page: cycle },
    ]);
    expect(graph.records).toHaveLength(2);
    expect(graph.relations).toHaveLength(2);
    expect(graph.records.get("root")?.depth).toBe(0);
  });

  it("画布投影严格限制节点和边预算", () => {
    const root = symbol("root", "root.go");
    const items = Array.from({ length: MAX_CODEGRAPH_NODES + 20 }, (_, index): CodeGraphRelation => ({
      id: index,
      kind: "calls",
      direction: "callees",
      source: root,
      target: symbol(`callee-${index}`, `${index}.go`),
    }));
    const page: CodeGraphRelationPage = {
      symbol: root,
      direction: "callees",
      items,
      total: items.length,
      limit: items.length,
      offset: 0,
      hasMore: true,
    };
    const graph = deriveCodeGraph(root, [{ symbol: root, direction: "callees", depth: 0, page }]);
    expect(graph.records.size).toBe(MAX_CODEGRAPH_NODES);
    expect(graph.relations.size).toBeLessThanOrEqual(MAX_CODEGRAPH_EDGES);
    expect(graph.nodeLimitReached).toBe(true);
    expect(graph.truncatedRelations).toHaveLength(1);

    const repeatedTarget = symbol("one-callee", "callee.go");
    const denseItems = Array.from({ length: MAX_CODEGRAPH_EDGES + 20 }, (_, index): CodeGraphRelation => ({
      id: 10_000 + index,
      kind: "calls",
      direction: "callees",
      source: root,
      target: repeatedTarget,
    }));
    const denseGraph = deriveCodeGraph(root, [{
      symbol: root, direction: "callees",
      depth: 0,
      page: { ...page, items: denseItems, total: denseItems.length, limit: denseItems.length },
    }]);
    expect(denseGraph.relations.size).toBe(MAX_CODEGRAPH_EDGES);
    expect(denseGraph.edgeLimitReached).toBe(true);
  });
});

describe("代码图谱分支折叠", () => {
  const page = (
    center: CodeGraphSymbol,
    direction: "callers" | "callees",
    adjacent: CodeGraphSymbol[],
  ): CodeGraphRelationPage => ({
    symbol: center,
    direction,
    items: adjacent.map((item, index) => ({
      id: Number(`${center.startLine}${index + 1}`),
      kind: "calls",
      direction,
      source: direction === "callers" ? item : center,
      target: direction === "callers" ? center : item,
    })),
    total: adjacent.length,
    limit: 20,
    offset: 0,
    hasMore: false,
  });

  it("折叠方向时移除该请求及其独占后代", () => {
    const root = symbol("root", "root.go");
    const child = symbol("child", "child.go");
    const leaf = symbol("leaf", "leaf.go");
    const expansions = [
      { symbol: root, direction: "callers" as const, depth: 0 },
      { symbol: root, direction: "callees" as const, depth: 0, page: page(root, "callees", [child]) },
      { symbol: child, direction: "callees" as const, depth: 1, page: page(child, "callees", [leaf]) },
      { symbol: leaf, direction: "callees" as const, depth: 2 },
    ];

    const result = pruneCodeGraphExpansions(root, expansions, codeGraphExpansionKey(expansions[2]));

    expect(result.map(codeGraphExpansionKey)).toEqual(["root:callers", "root:callees"]);
  });

  it("共享节点仍可从其他路径到达时保留其已展开后代", () => {
    const root = symbol("root", "root.go");
    const left = symbol("left", "left.go");
    const right = symbol("right", "right.go");
    const bridge = symbol("bridge", "bridge.go");
    const shared = symbol("shared", "shared.go");
    const leaf = symbol("leaf", "leaf.go");
    const expansions = [
      { symbol: root, direction: "callees" as const, depth: 0, page: page(root, "callees", [left, right]) },
      { symbol: left, direction: "callees" as const, depth: 1, page: page(left, "callees", [shared]) },
      { symbol: right, direction: "callees" as const, depth: 1, page: page(right, "callees", [bridge]) },
      { symbol: bridge, direction: "callees" as const, depth: 2, page: page(bridge, "callees", [shared]) },
      { symbol: shared, direction: "callees" as const, depth: 2, page: page(shared, "callees", [leaf]) },
      { symbol: leaf, direction: "callees" as const, depth: 3 },
    ];

    const result = pruneCodeGraphExpansions(root, expansions, codeGraphExpansionKey(expansions[1]));

    expect(result.map(codeGraphExpansionKey)).toEqual([
      "root:callees",
      "right:callees",
      "bridge:callees",
      "shared:callees",
      "leaf:callees",
    ]);
    expect(result.find((item) => item.symbol.id === "shared")?.depth).toBe(3);
    expect(result.find((item) => item.symbol.id === "leaf")?.depth).toBe(4);
  });

  it("无查询页的根方向可折叠，调用环不会产生额外请求", () => {
    const root = symbol("root", "root.go");
    const child = symbol("child", "child.go");
    const expansions = [
      { symbol: root, direction: "callers" as const, depth: 0 },
      { symbol: root, direction: "callees" as const, depth: 0, page: page(root, "callees", [child]) },
      { symbol: child, direction: "callees" as const, depth: 1, page: page(child, "callees", [root]) },
    ];

    expect(pruneCodeGraphExpansions(root, expansions, "root:callers").map(codeGraphExpansionKey)).toEqual([
      "root:callees",
      "child:callees",
    ]);
  });
});
