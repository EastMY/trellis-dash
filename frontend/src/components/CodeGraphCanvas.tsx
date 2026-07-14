import { LoadingOutlined, WarningOutlined } from "@ant-design/icons";
import { useQueries } from "@tanstack/react-query";
import {
  Background,
  BackgroundVariant,
  Controls,
  MarkerType,
  MiniMap,
  ReactFlow,
  useNodesState,
} from "@xyflow/react";
import type { Edge, Node } from "@xyflow/react";
import { Alert, Button, Space, Tag, Typography } from "antd";
import { useCallback, useEffect, useMemo, useState } from "react";
import { api } from "../api/client";
import {
  codeGraphEdgeKey,
  codeGraphKindLabel,
  deriveCodeGraph,
  layoutCodeGraph,
  MAX_CODEGRAPH_EDGES,
  MAX_CODEGRAPH_NODES,
} from "../lib/codeGraph";
import type { CodeGraphDirection, CodeGraphSymbol } from "../types";
import "@xyflow/react/dist/style.css";

interface CodeGraphCanvasProps {
  projectId: string;
  rootSymbol?: CodeGraphSymbol;
}

interface ExpansionRequest {
  symbol: CodeGraphSymbol;
  depth: number;
  direction: CodeGraphDirection;
}

const expansionKey = (request: ExpansionRequest) => `${request.symbol.id}:${request.direction}`;

export function CodeGraphCanvas({ projectId, rootSymbol }: CodeGraphCanvasProps) {
  const [expansions, setExpansions] = useState<ExpansionRequest[]>([]);
  const [flowNodes, setFlowNodes, onNodesChange] = useNodesState<Node>([]);

  useEffect(() => {
    setFlowNodes([]);
    setExpansions(rootSymbol ? [
      { symbol: rootSymbol, depth: 0, direction: "callers" },
      { symbol: rootSymbol, depth: 0, direction: "callees" },
    ] : []);
  }, [rootSymbol, setFlowNodes]);

  const relationQueries = useQueries({
    queries: expansions.map((request) => ({
      queryKey: ["project", projectId, "codegraph", "relations", request.symbol.id, request.direction, 0, 50],
      queryFn: () => api.getCodeGraphRelations(projectId, request.symbol.id, request.direction, 50, 0),
    })),
  });

  const dataRevision = relationQueries.map((query) => query.dataUpdatedAt).join(":");
  const loadingRevision = relationQueries.map((query) => query.isFetching ? "1" : "0").join("");

  const graph = useMemo(() => deriveCodeGraph(rootSymbol, expansions.map((request, index) => ({
    direction: request.direction,
    depth: request.depth,
    page: relationQueries[index]?.data,
  // dataUpdatedAt 是 Query 拥有的结果版本；避免把服务端响应复制进本地 state。
  }))), [dataRevision, expansions, rootSymbol]);

  const expandedKeys = useMemo(() => new Set(expansions.map(expansionKey)), [expansions]);
  const loadingKeys = useMemo(() => new Set(expansions.flatMap((request, index) =>
    relationQueries[index]?.isFetching ? [expansionKey(request)] : [])), [expansions, loadingRevision]);
  const queryError = relationQueries.find((query) => query.isError)?.error;
  const limitReached = graph.nodeLimitReached || graph.edgeLimitReached;

  const expandSymbol = useCallback((
    symbol: CodeGraphSymbol,
    depth: number,
    direction: CodeGraphDirection,
  ) => {
    if (limitReached) return;
    setExpansions((current) => {
      const request = { symbol, depth, direction };
      return current.some((item) => expansionKey(item) === expansionKey(request)) ? current : [...current, request];
    });
  }, [limitReached]);

  const positionedNodes = useMemo<Node[]>(() => layoutCodeGraph(graph.records.values()).map((record) => {
    const callerKey = `${record.symbol.id}:callers`;
    const calleeKey = `${record.symbol.id}:callees`;
    const isRoot = record.symbol.id === rootSymbol?.id;
    return {
      id: record.symbol.id,
      position: { x: record.x, y: record.y },
      ariaLabel: `${codeGraphKindLabel(record.symbol.kind)} ${record.symbol.qualifiedName}`,
      className: `codegraph-node${isRoot ? " codegraph-node--root" : ""}`,
      data: {
        label: (
          <div className="codegraph-node-content">
            <Space size={5} wrap>
              <Tag color={isRoot ? "green" : undefined}>{codeGraphKindLabel(record.symbol.kind)}</Tag>
              <Typography.Text type="secondary" className="mono">{record.symbol.language}</Typography.Text>
            </Space>
            <Typography.Text strong ellipsis={{ tooltip: record.symbol.qualifiedName }}>
              {record.symbol.name}
            </Typography.Text>
            <Typography.Text type="secondary" className="codegraph-node-path" ellipsis={{ tooltip: record.symbol.filePath }}>
              {record.symbol.filePath}:{record.symbol.startLine}
            </Typography.Text>
            <div className="codegraph-node-actions nodrag nowheel">
              <Button
                type="text"
                size="small"
                disabled={expandedKeys.has(callerKey) || limitReached}
                icon={loadingKeys.has(callerKey) ? <LoadingOutlined /> : undefined}
                onClick={() => expandSymbol(record.symbol, record.depth, "callers")}
              >
                上游
              </Button>
              <Button
                type="text"
                size="small"
                disabled={expandedKeys.has(calleeKey) || limitReached}
                icon={loadingKeys.has(calleeKey) ? <LoadingOutlined /> : undefined}
                onClick={() => expandSymbol(record.symbol, record.depth, "callees")}
              >
                下游
              </Button>
            </div>
          </div>
        ),
      },
      style: { width: 250 },
    };
  }), [expandSymbol, expandedKeys, graph.records, limitReached, loadingKeys, rootSymbol?.id]);

  useEffect(() => {
    // 追加关系时保留用户已拖动坐标，只给新节点应用稳定分层布局。
    setFlowNodes((current) => {
      const positions = new Map(current.map((node) => [node.id, node.position]));
      return positionedNodes.map((node) => ({ ...node, position: positions.get(node.id) ?? node.position }));
    });
  }, [positionedNodes, setFlowNodes]);

  const nodeIDs = useMemo(() => new Set(graph.records.keys()), [graph.records]);
  const edges = useMemo<Edge[]>(() => [...graph.relations.values()]
    .filter((relation) => nodeIDs.has(relation.source.id) && nodeIDs.has(relation.target.id))
    .map((relation) => ({
      id: codeGraphEdgeKey(relation),
      source: relation.source.id,
      target: relation.target.id,
      label: relation.line ? `${relation.kind} · L${relation.line}` : relation.kind,
      type: "smoothstep",
      markerEnd: { type: MarkerType.ArrowClosed },
      className: "codegraph-edge",
    })), [graph.relations, nodeIDs]);

  const budgetMessage = graph.nodeLimitReached
    ? `画布已达到 ${MAX_CODEGRAPH_NODES} 个符号上限，请重新选择中心符号缩小范围。`
    : graph.edgeLimitReached
      ? `画布已达到 ${MAX_CODEGRAPH_EDGES} 条调用上限，请重新选择中心符号缩小范围。`
      : graph.truncatedRelations[0]
        ? `“${graph.truncatedRelations[0].symbol.name}”的${graph.truncatedRelations[0].direction === "callers" ? "上游" : "下游"}关系较多，当前显示前 ${graph.truncatedRelations[0].visible} 条。`
        : undefined;

  if (!rootSymbol) {
    return (
      <div className="codegraph-canvas-empty">
        <Typography.Text type="secondary">从左侧目录或搜索结果选择一个符号，查看调用链。</Typography.Text>
      </div>
    );
  }

  return (
    <div className="codegraph-canvas" aria-label="代码调用链画布">
      {queryError && (
        <Alert
          className="codegraph-canvas-alert"
          type="warning"
          showIcon
          icon={<WarningOutlined />}
          message={queryError instanceof Error ? queryError.message : "调用关系加载失败"}
          action={<Button size="small" onClick={() => relationQueries.forEach((query) => { if (query.isError) void query.refetch(); })}>重试</Button>}
        />
      )}
      {budgetMessage && !queryError && (
        <Alert className="codegraph-canvas-alert" type="info" showIcon message={budgetMessage} />
      )}
      <ReactFlow
        key={rootSymbol.id}
        nodes={flowNodes}
        edges={edges}
        onNodesChange={onNodesChange}
        fitView
        fitViewOptions={{ padding: 0.25, maxZoom: 1.15 }}
        minZoom={0.12}
        maxZoom={1.8}
        nodesConnectable={false}
        nodesDraggable
        elementsSelectable
        panOnScroll
        zoomOnScroll
        zoomOnPinch
        deleteKeyCode={null}
        proOptions={{ hideAttribution: true }}
      >
        {/* React Flow 会根据 style 尺寸计算缩略图 viewBox，不能只缩放 CSS 外壳。 */}
        <MiniMap
          style={{ width: 120, height: 80 }}
          position="top-left"
          pannable
          zoomable
          aria-label="调用链缩略图"
        />
        <Controls showInteractive={false} aria-label="调用链缩放控制" />
        <Background variant={BackgroundVariant.Dots} gap={20} size={1} />
      </ReactFlow>
      <div className="codegraph-canvas-count" aria-live="polite">
        {graph.records.size} 个符号 · {edges.length} 条调用
      </div>
    </div>
  );
}
