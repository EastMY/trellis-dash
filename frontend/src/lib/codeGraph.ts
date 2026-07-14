import type {
  CodeGraphDirection,
  CodeGraphRelation,
  CodeGraphRelationPage,
  CodeGraphSymbol,
} from "../types";

export interface CodeGraphNodeRecord {
  symbol: CodeGraphSymbol;
  depth: number;
}

export interface CodeGraphPositionedNode extends CodeGraphNodeRecord {
  x: number;
  y: number;
}

export interface CodeGraphExpansionResult {
  direction: CodeGraphDirection;
  depth: number;
  page?: CodeGraphRelationPage;
}

export interface CodeGraphDerivedGraph {
  records: Map<string, CodeGraphNodeRecord>;
  relations: Map<string, CodeGraphRelation>;
  truncatedRelations: Array<{ symbol: CodeGraphSymbol; direction: CodeGraphDirection; visible: number }>;
  nodeLimitReached: boolean;
  edgeLimitReached: boolean;
}

export const MAX_CODEGRAPH_NODES = 300;
export const MAX_CODEGRAPH_EDGES = 600;

const COLUMN_GAP = 340;
const ROW_GAP = 142;

const kindLabels: Record<string, string> = {
  class: "类",
  component: "组件",
  enum: "枚举",
  function: "函数",
  interface: "接口",
  method: "方法",
  route: "路由",
  struct: "结构体",
  type: "类型",
  type_alias: "类型别名",
};

export function codeGraphKindLabel(kind: string): string {
  return kindLabels[kind] ?? (kind || "符号");
}

/**
 * 按调用层级将节点排成稳定的左右列：调用方在左，被调用方在右。
 * 同层节点按文件和限定名排序，重复请求不会让画布随机跳动。
 */
export function layoutCodeGraph(records: Iterable<CodeGraphNodeRecord>): CodeGraphPositionedNode[] {
  const columns = new Map<number, CodeGraphNodeRecord[]>();
  for (const record of records) {
    const column = columns.get(record.depth) ?? [];
    column.push(record);
    columns.set(record.depth, column);
  }

  return [...columns.entries()].flatMap(([depth, column]) => {
    column.sort((left, right) =>
      `${left.symbol.filePath}\0${left.symbol.qualifiedName}`.localeCompare(
        `${right.symbol.filePath}\0${right.symbol.qualifiedName}`,
      ));
    return column.map((record, index) => ({
      ...record,
      x: depth * COLUMN_GAP,
      y: (index - (column.length - 1) / 2) * ROW_GAP,
    }));
  });
}

/** 关系边 ID 在数据库内稳定，附加端点可以防止多项目/多图之间发生碰撞。 */
export function codeGraphEdgeKey(relation: CodeGraphRelation): string {
  return `${relation.id}:${relation.source.id}->${relation.target.id}`;
}

/** 一个符号已在图中时，优先保留离根节点更近的层级。 */
export function nearestDepth(current: number | undefined, candidate: number): number {
  if (current === undefined || Math.abs(candidate) < Math.abs(current)) return candidate;
  return current;
}

/**
 * Query 结果始终由 TanStack Query 持有；这里只把已完成响应投影成有界画布数据。
 * 符号和边按源 ID 去重，因此调用环只生成回边，不会触发递归复制。
 */
export function deriveCodeGraph(
  root: CodeGraphSymbol | undefined,
  expansions: CodeGraphExpansionResult[],
): CodeGraphDerivedGraph {
  const records = new Map<string, CodeGraphNodeRecord>();
  const relations = new Map<string, CodeGraphRelation>();
  const truncatedRelations: CodeGraphDerivedGraph["truncatedRelations"] = [];
  let nodeLimitReached = false;
  let edgeLimitReached = false;
  if (root) records.set(root.id, { symbol: root, depth: 0 });

  for (const expansion of expansions) {
    const page = expansion.page;
    if (!page) continue;
    const centerDepth = nearestDepth(records.get(page.symbol.id)?.depth, expansion.depth);
    records.set(page.symbol.id, { symbol: page.symbol, depth: centerDepth });
    if (page.hasMore) {
      truncatedRelations.push({ symbol: page.symbol, direction: expansion.direction, visible: page.items.length });
    }

    for (const relation of page.items) {
      const adjacent = expansion.direction === "callers" ? relation.source : relation.target;
      if (!records.has(adjacent.id) && records.size >= MAX_CODEGRAPH_NODES) {
        nodeLimitReached = true;
        continue;
      }
      const adjacentDepth = centerDepth + (expansion.direction === "callers" ? -1 : 1);
      records.set(adjacent.id, {
        symbol: adjacent,
        depth: nearestDepth(records.get(adjacent.id)?.depth, adjacentDepth),
      });
      const edgeKey = codeGraphEdgeKey(relation);
      if (!relations.has(edgeKey) && relations.size >= MAX_CODEGRAPH_EDGES) {
        edgeLimitReached = true;
        continue;
      }
      relations.set(edgeKey, relation);
    }
  }
  return { records, relations, truncatedRelations, nodeLimitReached, edgeLimitReached };
}
