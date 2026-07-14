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
  MiniMap: ({ position, style }: { position?: string; style?: React.CSSProperties }) => (
    <div aria-label="调用链缩略图" data-position={position} style={style} />
  ),
  ReactFlow: ({ nodes, edges, children }: {
    nodes: Array<{ id: string; data: { label: React.ReactNode } }>;
    edges: Array<{ id: string }>;
    children: React.ReactNode;
  }) => (
    <div aria-label="测试 React Flow">
      {nodes.map((node) => <div key={node.id} aria-label={`节点-${node.id}`}>{node.data.label}</div>)}
      <output aria-label="边数量">{edges.length}</output>
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
    expect(screen.getByLabelText("调用链缩略图")).toHaveAttribute("data-position", "top-left");
    expect(screen.getByLabelText("调用链缩略图")).toHaveStyle({ width: "120px", height: "80px" });

    fireEvent.click(within(screen.getByLabelText("节点-callee")).getByRole("button", { name: "下游" }));
    await waitFor(() => expect(api.getCodeGraphRelations).toHaveBeenCalledWith("demo", "callee", "callees", 50, 0));
  });
});
