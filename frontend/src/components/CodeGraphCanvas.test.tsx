import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import React from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api/client";
import type { CodeGraphDirection, CodeGraphRelationPage, CodeGraphSymbol } from "../types";
import { CodeGraphCanvas } from "./CodeGraphCanvas";

vi.mock("../api/client", () => ({ api: { getCodeGraphRelations: vi.fn() } }));
vi.mock("@xyflow/react", async () => ({
  Background: () => null,
  BackgroundVariant: { Dots: "dots" },
  Controls: () => <div aria-label="调用链缩放控制" />,
  MarkerType: { ArrowClosed: "arrowclosed" },
  Position: { Left: "left", Right: "right" },
  MiniMap: ({ position, style }: { position?: string; style?: React.CSSProperties }) => (
    <div aria-label="调用链缩略图" data-position={position} style={style} />
  ),
  ReactFlow: ({ nodes, edges, children }: {
    nodes: Array<{
      id: string;
      sourcePosition?: string;
      targetPosition?: string;
      data: { label: React.ReactNode };
    }>;
    edges: Array<{ id: string; label?: React.ReactNode }>;
    children: React.ReactNode;
  }) => (
    <div aria-label="测试 React Flow">
      {nodes.map((node) => (
        <div
          key={node.id}
          aria-label={`节点-${node.id}`}
          data-source-position={node.sourcePosition}
          data-target-position={node.targetPosition}
        >
          {node.data.label}
        </div>
      ))}
      <output aria-label="边数量" data-has-label={edges.some((edge) => edge.label != null)}>{edges.length}</output>
      {children}
    </div>
  ),
  useNodesState: <T,>(initial: T[]) => {
    const [nodes, setNodes] = React.useState(initial);
    return [nodes, setNodes, vi.fn()] as const;
  },
}));

const symbol = (id: string): CodeGraphSymbol => ({
  id,
  kind: "function",
  name: id,
  qualifiedName: id,
  filePath: `${id}.go`,
  language: "go",
  startLine: 1,
  endLine: 2,
});

function relationPage(center: CodeGraphSymbol, direction: CodeGraphDirection): CodeGraphRelationPage {
  const adjacent = symbol(direction === "callers" ? "caller" : "callee");
  return {
    symbol: center,
    direction,
    items: [{
      id: direction === "callers" ? 1 : 2,
      kind: "calls",
      direction,
      source: direction === "callers" ? adjacent : center,
      target: direction === "callers" ? center : adjacent,
    }],
    total: 1,
    limit: 50,
    offset: 0,
    hasMore: false,
  };
}

function renderCanvas(root: CodeGraphSymbol) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <CodeGraphCanvas projectId="demo" rootSymbol={root} />
    </QueryClientProvider>,
  );
}

describe("CodeGraphCanvas", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.getCodeGraphRelations).mockImplementation(async (_project, symbolID, direction) => {
      const center = symbol(symbolID);
      return symbolID === "root" ? relationPage(center, direction) : {
        symbol: center, direction, items: [], total: 0, limit: 50, offset: 0, hasMore: false,
      };
    });
  });

  afterEach(() => cleanup());

  it("选择根符号后同时加载上下游，并可从关系节点继续展开", async () => {
    renderCanvas(symbol("root"));

    await waitFor(() => expect(api.getCodeGraphRelations).toHaveBeenCalledTimes(2));
    expect(api.getCodeGraphRelations).toHaveBeenCalledWith("demo", "root", "callers", 50, 0);
    expect(api.getCodeGraphRelations).toHaveBeenCalledWith("demo", "root", "callees", 50, 0);
    expect(await screen.findByLabelText("节点-caller")).toBeInTheDocument();
    expect(screen.getByLabelText("节点-callee")).toBeInTheDocument();
    expect(screen.getByLabelText("边数量")).toHaveTextContent("2");
    expect(screen.getByLabelText("边数量")).toHaveAttribute("data-has-label", "false");
    expect(screen.getByText("3 个符号 · 2 条关系")).toBeInTheDocument();
    expect(screen.getByLabelText("节点-root")).toHaveAttribute("data-source-position", "right");
    expect(screen.getByLabelText("节点-root")).toHaveAttribute("data-target-position", "left");
    expect(screen.getByLabelText("调用链缩略图")).toHaveAttribute("data-position", "top-left");
    expect(screen.getByLabelText("调用链缩略图")).toHaveStyle({ width: "120px", height: "80px" });

    const rootActions = within(screen.getByLabelText("节点-root"));
    expect(rootActions.getByRole("button", { name: "上游" })).toHaveAttribute("aria-pressed", "true");
    expect(rootActions.getByRole("button", { name: "下游" })).toHaveAttribute("aria-pressed", "true");

    fireEvent.click(rootActions.getByRole("button", { name: "下游" }));
    await waitFor(() => expect(screen.queryByLabelText("节点-callee")).not.toBeInTheDocument());
    expect(screen.getByLabelText("边数量")).toHaveTextContent("1");
    expect(within(screen.getByLabelText("节点-root")).getByRole("button", { name: "下游" }))
      .toHaveAttribute("aria-pressed", "false");

    fireEvent.click(within(screen.getByLabelText("节点-root")).getByRole("button", { name: "下游" }));
    expect(await screen.findByLabelText("节点-callee")).toBeInTheDocument();
    expect(screen.getByLabelText("边数量")).toHaveTextContent("2");
    expect(within(screen.getByLabelText("节点-root")).getByRole("button", { name: "下游" }))
      .toHaveAttribute("aria-pressed", "true");

    fireEvent.click(within(screen.getByLabelText("节点-callee")).getByRole("button", { name: "下游" }));
    await waitFor(() => expect(api.getCodeGraphRelations).toHaveBeenCalledWith("demo", "callee", "callees", 50, 0));
  });

  it("保留路由引用边，并可从处理方法继续展开调用链", async () => {
    const route = { ...symbol("route:login"), kind: "route", name: "POST /login", language: "java" };
    const handler = { ...symbol("method:login"), kind: "method", name: "login", language: "java" };
    vi.mocked(api.getCodeGraphRelations).mockImplementation(async (_project, symbolID, direction) => {
      const center = symbolID === route.id ? route : handler;
      if (symbolID === route.id && direction === "callees") {
        return {
          symbol: route,
          direction,
          items: [{
            id: 3,
            kind: "references",
            direction,
            source: route,
            target: handler,
          }],
          total: 1,
          limit: 50,
          offset: 0,
          hasMore: false,
        };
      }
      return { symbol: center, direction, items: [], total: 0, limit: 50, offset: 0, hasMore: false };
    });

    renderCanvas(route);

    expect(await screen.findByLabelText("节点-method:login")).toBeInTheDocument();
    expect(screen.getByLabelText("边数量")).toHaveTextContent("1");
    expect(screen.getByText("2 个符号 · 1 条关系")).toBeInTheDocument();
    expect(within(screen.getByLabelText("节点-route:login")).getByRole("button", { name: "下游" }))
      .toHaveAttribute("aria-pressed", "true");

    fireEvent.click(within(screen.getByLabelText("节点-method:login")).getByRole("button", { name: "下游" }));
    await waitFor(() => expect(api.getCodeGraphRelations).toHaveBeenCalledWith("demo", "method:login", "callees", 50, 0));
  });
});
