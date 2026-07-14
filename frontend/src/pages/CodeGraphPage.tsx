import {
  CodeOutlined,
  FileOutlined,
  FolderOpenOutlined,
  SearchOutlined,
  ShareAltOutlined,
} from "@ant-design/icons";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Button, Input, Segmented, Select, Space, Spin, Statistic, Tag, Tree, Typography } from "antd";
import type { DataNode } from "antd/es/tree";
import { useEffect, useMemo, useState } from "react";
import { api } from "../api/client";
import { CodeGraphCanvas } from "../components/CodeGraphCanvas";
import { useProjectContext } from "../components/AppShell";
import { PageHeader } from "../components/PageHeader";
import { EmptyState, ErrorState, PageSkeleton } from "../components/PageState";
import { codeGraphKindLabel } from "../lib/codeGraph";
import type { CodeGraphStructureEntry, CodeGraphSymbol } from "../types";

interface CodeGraphTreeNode extends DataNode {
  entry: CodeGraphStructureEntry;
  children?: CodeGraphTreeNode[];
}

function treeNode(entry: CodeGraphStructureEntry): CodeGraphTreeNode {
  const icon = entry.type === "directory"
    ? <FolderOpenOutlined />
    : entry.type === "file"
      ? <FileOutlined />
      : <CodeOutlined />;
  const count = entry.type === "directory" ? entry.fileCount : entry.nodeCount;
  return {
    key: entry.id,
    entry,
    icon,
    isLeaf: !entry.expandable,
    title: (
      <span className="codegraph-tree-title">
        <span>{entry.name}</span>
        <span className="codegraph-tree-meta">
          {entry.language ? <Typography.Text type="secondary">{entry.language}</Typography.Text> : null}
          {Boolean(count) && <Typography.Text type="secondary">{count}</Typography.Text>}
        </span>
      </span>
    ),
  };
}

function updateTreeChildren(
  nodes: CodeGraphTreeNode[],
  key: React.Key,
  children: CodeGraphTreeNode[],
): CodeGraphTreeNode[] {
  return nodes.map((node) => {
    if (node.key === key) return { ...node, children };
    if (node.children) return { ...node, children: updateTreeChildren(node.children, key, children) };
    return node;
  });
}

function findTreeNode(nodes: CodeGraphTreeNode[], key: React.Key): CodeGraphTreeNode | undefined {
  for (const node of nodes) {
    if (node.key === key) return node;
    const child = node.children && findTreeNode(node.children, key);
    if (child) return child;
  }
  return undefined;
}

function formatBytes(value = 0): string {
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`;
  return `${(value / 1024 / 1024).toFixed(1)} MB`;
}

export function CodeGraphPage() {
  const { project } = useProjectContext();
  const queryClient = useQueryClient();
  const [treeData, setTreeData] = useState<CodeGraphTreeNode[]>([]);
  const [selectedSymbol, setSelectedSymbol] = useState<CodeGraphSymbol>();
  const [searchInput, setSearchInput] = useState("");
  const [searchText, setSearchText] = useState("");
  const [symbolKind, setSymbolKind] = useState<string>();
  const [mobilePanel, setMobilePanel] = useState<"structure" | "graph">("structure");

  const statusQuery = useQuery({
    queryKey: ["project", project.id, "codegraph", "status"],
    queryFn: () => api.getCodeGraphStatus(project.id),
  });
  const structureQuery = useQuery({
    queryKey: ["project", project.id, "codegraph", "structure", "", 0, 200],
    queryFn: () => api.getCodeGraphStructure(project.id, "", 200),
    enabled: statusQuery.data?.available === true,
  });
  const searchQuery = useQuery({
    queryKey: ["project", project.id, "codegraph", "search", searchText, symbolKind ?? "", 0, 30],
    queryFn: () => api.searchCodeGraphSymbols(project.id, searchText, symbolKind),
    enabled: statusQuery.data?.available === true && searchText.length > 0,
  });

  useEffect(() => {
    setTreeData((structureQuery.data?.items ?? []).map(treeNode));
  }, [structureQuery.data]);

  useEffect(() => {
    setSelectedSymbol(undefined);
    setSearchInput("");
    setSearchText("");
    setMobilePanel("structure");
  }, [project.id, statusQuery.data?.revision]);

  const loadTreeNode = async (rawNode: DataNode): Promise<void> => {
    const node = rawNode as CodeGraphTreeNode;
    if (node.children || !node.entry.expandable) return;
    const page = await queryClient.fetchQuery({
      queryKey: ["project", project.id, "codegraph", "structure", node.entry.path, 0, 200],
      queryFn: () => api.getCodeGraphStructure(project.id, node.entry.path, 200),
    });
    setTreeData((current) => updateTreeChildren(current, node.key, page.items.map(treeNode)));
  };

  const selectSymbol = (symbol: CodeGraphSymbol) => {
    setSelectedSymbol(symbol);
    setMobilePanel("graph");
  };

  const languageTags = useMemo(() => statusQuery.data?.languages?.slice(0, 6) ?? [], [statusQuery.data]);

  if (statusQuery.isLoading) return <PageSkeleton rows={8} />;
  if (statusQuery.isError) return <ErrorState error={statusQuery.error} onRetry={() => void statusQuery.refetch()} />;

  const status = statusQuery.data;
  if (!status?.available) {
    return (
      <div className="page codegraph-page">
        <PageHeader title="代码图谱" description="读取项目现有 .codegraph 索引，检索代码结构与调用关系" />
        <EmptyState
          title="当前项目暂无可用 CodeGraph"
          description={status?.message || "请先在项目目录生成 CodeGraph 索引，再回到面板刷新。"}
          action={<Button onClick={() => void statusQuery.refetch()}>重新检查</Button>}
        />
      </div>
    );
  }

  return (
    <div className="page codegraph-page">
      <PageHeader
        title="代码图谱"
        description="按项目浏览代码结构，并从任意符号向上游、下游展开调用链"
        meta={(
          <Space size={6} wrap>
            <Tag icon={<ShareAltOutlined />} color="green">只读索引</Tag>
            {languageTags.map((language) => (
              <Tag key={language.name}>{language.name} · {language.fileCount}</Tag>
            ))}
          </Space>
        )}
      />

      <div className="codegraph-metrics metric-strip">
        <Statistic title="文件" value={status.fileCount ?? 0} />
        <Statistic title="符号" value={status.nodeCount ?? 0} />
        <Statistic title="关系" value={status.edgeCount ?? 0} />
        <Statistic title="索引大小" value={formatBytes(status.databaseBytes)} />
      </div>

      <Segmented
        className="codegraph-mobile-switch"
        block
        value={mobilePanel}
        options={[{ label: "结构与搜索", value: "structure" }, { label: "调用链", value: "graph" }]}
        onChange={(value) => setMobilePanel(value as "structure" | "graph")}
      />

      <div className="codegraph-workspace">
        <aside className={`codegraph-explorer${mobilePanel === "graph" ? " codegraph-mobile-hidden" : ""}`}>
          <div className="codegraph-searchbar">
            <Input.Search
              allowClear
              value={searchInput}
              maxLength={200}
              placeholder="搜索符号或限定名"
              enterButton={<SearchOutlined />}
              onChange={(event) => {
                setSearchInput(event.target.value);
                if (!event.target.value) setSearchText("");
              }}
              onSearch={(value) => setSearchText(value.trim())}
            />
            <Select
              allowClear
              value={symbolKind}
              placeholder="全部类型"
              onChange={setSymbolKind}
              options={[
                { label: "函数", value: "function" },
                { label: "方法", value: "method" },
                { label: "类", value: "class" },
                { label: "接口", value: "interface" },
                { label: "类型", value: "type" },
              ]}
            />
          </div>

          {searchText ? (
            <div className="codegraph-search-results">
              <div className="codegraph-panel-heading">
                <Typography.Text strong>搜索结果</Typography.Text>
                <Typography.Text type="secondary">{searchQuery.data?.total ?? 0}</Typography.Text>
              </div>
              {searchQuery.isError ? (
                <ErrorState compact error={searchQuery.error} onRetry={() => void searchQuery.refetch()} />
              ) : (
                <div className="codegraph-result-list" aria-label="符号搜索结果">
                  {searchQuery.isLoading ? <Spin aria-label="正在搜索符号" /> : null}
                  {!searchQuery.isLoading && !searchQuery.data?.items.length ? (
                    <Typography.Text type="secondary">没有匹配符号</Typography.Text>
                  ) : null}
                  {searchQuery.data?.items.map((symbol) => (
                    <button
                      key={symbol.id}
                      type="button"
                      aria-label={symbol.qualifiedName}
                      className={`codegraph-result${selectedSymbol?.id === symbol.id ? " codegraph-result-selected" : ""}`}
                      onClick={() => selectSymbol(symbol)}
                    >
                      <strong>{symbol.qualifiedName}</strong>
                      <span>{codeGraphKindLabel(symbol.kind)} · {symbol.filePath}:{symbol.startLine}</span>
                      {symbol.signature ? <span className="mono">{symbol.signature}</span> : null}
                    </button>
                  ))}
                </div>
              )}
            </div>
          ) : (
            <div className="codegraph-tree-panel">
              <div className="codegraph-panel-heading">
                <Typography.Text strong>项目结构</Typography.Text>
                <Typography.Text type="secondary">按需展开</Typography.Text>
              </div>
              {structureQuery.isError ? (
                <ErrorState compact error={structureQuery.error} onRetry={() => void structureQuery.refetch()} />
              ) : (
                <Tree
                  showIcon
                  blockNode
                  loadData={loadTreeNode}
                  treeData={treeData}
                  selectedKeys={selectedSymbol ? [selectedSymbol.id] : []}
                  onSelect={(keys) => {
                    const key = keys[0];
                    if (key === undefined) return;
                    const node = findTreeNode(treeData, key);
                    if (node?.entry.symbol) selectSymbol(node.entry.symbol);
                  }}
                />
              )}
            </div>
          )}
        </aside>

        <section className={`codegraph-graph-panel${mobilePanel === "structure" ? " codegraph-mobile-hidden" : ""}`}>
          <CodeGraphCanvas projectId={project.id} rootSymbol={selectedSymbol} />
        </section>
      </div>
    </div>
  );
}
