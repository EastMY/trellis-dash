import {
  CodeOutlined,
  DownOutlined,
  FileOutlined,
  FolderOpenOutlined,
  SearchOutlined,
  ShareAltOutlined,
  SyncOutlined,
} from "@ant-design/icons";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { App, Button, Dropdown, Input, Segmented, Select, Space, Spin, Statistic, Tag, Tooltip, Tree, Typography } from "antd";
import type { DataNode } from "antd/es/tree";
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { api } from "../api/client";
import { CodeGraphCanvas } from "../components/CodeGraphCanvas";
import { useProjectContext } from "../components/AppShell";
import { PageHeader } from "../components/PageHeader";
import { EmptyState, ErrorState, PageSkeleton } from "../components/PageState";
import { codeGraphKindLabel } from "../lib/codeGraph";
import { minuteDate } from "../lib/format";
import type { CodeGraphStatus, CodeGraphStructureEntry, CodeGraphSymbol, CodeGraphSyncMode } from "../types";

interface CodeGraphTreeNode extends DataNode {
  entry: CodeGraphStructureEntry;
  compactEntries?: CodeGraphStructureEntry[];
  children?: CodeGraphTreeNode[];
}

interface TreeNodeOptions {
  key?: React.Key;
  compactEntries?: CodeGraphStructureEntry[];
  children?: CodeGraphTreeNode[];
}

function treeNode(entry: CodeGraphStructureEntry, options: TreeNodeOptions = {}): CodeGraphTreeNode {
  const icon = entry.type === "directory"
    ? <FolderOpenOutlined />
    : entry.type === "file"
      ? <FileOutlined />
      : <CodeOutlined />;
  const count = entry.type === "directory" ? entry.fileCount : entry.nodeCount;
  const compactEntries = options.compactEntries ?? [entry];
  const compactName = compactEntries.map((item) => item.name).join(" / ");
  return {
    key: options.key ?? entry.id,
    entry,
    compactEntries,
    children: options.children,
    icon,
    isLeaf: !entry.expandable,
    title: (
      <span className="codegraph-tree-title">
        <span title={compactName}>{compactName}</span>
        <span className="codegraph-tree-meta">
          {entry.language ? <Typography.Text type="secondary">{entry.language}</Typography.Text> : null}
          {Boolean(count) && <Typography.Text type="secondary">{count}</Typography.Text>}
        </span>
      </span>
    ),
  };
}

function updateTreeNode(
  nodes: CodeGraphTreeNode[],
  key: React.Key,
  replacement: CodeGraphTreeNode,
): CodeGraphTreeNode[] {
  return nodes.map((node) => {
    if (node.key === key) return replacement;
    if (node.children) return { ...node, children: updateTreeNode(node.children, key, replacement) };
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
  const { message, modal } = App.useApp();
  const queryClient = useQueryClient();
  const [treeData, setTreeData] = useState<CodeGraphTreeNode[]>([]);
  const [selectedSymbol, setSelectedSymbol] = useState<CodeGraphSymbol>();
  const [searchInput, setSearchInput] = useState("");
  const [searchText, setSearchText] = useState("");
  const [symbolKind, setSymbolKind] = useState<string>();
  const [activePanel, setActivePanel] = useState<"structure" | "graph">("structure");
  const [expandedKeys, setExpandedKeys] = useState<React.Key[]>([]);
  const [syncMenuOpen, setSyncMenuOpen] = useState(false);
  const handledOperationRef = useRef("");
  const lastAvailableStatusRef = useRef<{ projectId: string; status: CodeGraphStatus } | undefined>(undefined);
  const resetRevisionRef = useRef("");

  const statusQueryKey = ["project", project.id, "codegraph", "status"] as const;

  const statusQuery = useQuery({
    queryKey: statusQueryKey,
    queryFn: () => api.getCodeGraphStatus(project.id),
    refetchInterval: (query) => query.state.data?.operation?.state === "running" ? 1_000 : false,
  });
  const rawStatus = statusQuery.data;
  const operation = rawStatus?.operation;
  const operationRunning = operation?.state === "running";
  if (rawStatus?.available) {
    lastAvailableStatusRef.current = { projectId: project.id, status: rawStatus };
  }
  // 写索引时 Reader 可能短暂 busy；运行期继续展示本次操作开始前的可用快照。
  const status = !rawStatus?.available
    && operationRunning
    && lastAvailableStatusRef.current?.projectId === project.id
    ? { ...lastAvailableStatusRef.current.status, cliAvailable: rawStatus?.cliAvailable ?? false, operation }
    : rawStatus;

  const syncMutation = useMutation({
    mutationFn: (mode: CodeGraphSyncMode) => api.syncCodeGraph(project.id, mode),
    onSuccess: (operation) => {
      queryClient.setQueryData<CodeGraphStatus>(statusQueryKey, (current) => (
        current ? { ...current, operation } : current
      ));
      message.info(operation.mode === "rebuild" ? "已开始完整重建 CodeGraph 索引" : "已开始同步 CodeGraph 索引");
    },
    onError: (error) => {
      message.error(error instanceof Error ? error.message : "启动 CodeGraph 操作失败");
    },
  });

  useEffect(() => {
    const operation = statusQuery.data?.operation;
    if (!operation || operation.state === "running") return;
    const operationKey = `${operation.startedAt}:${operation.state}`;
    if (handledOperationRef.current === operationKey) return;
    handledOperationRef.current = operationKey;
    if (operation.state === "succeeded") {
      message.success(operation.mode === "rebuild" ? "CodeGraph 索引完整重建完成" : "CodeGraph 索引同步完成");
      void queryClient.invalidateQueries({ queryKey: ["project", project.id, "codegraph"] });
      return;
    }
    message.error(operation.message || "CodeGraph 操作失败，已保留原索引内容");
  }, [message, project.id, queryClient, statusQuery.data?.operation]);

  useLayoutEffect(() => {
    if (status?.available !== true) return undefined;
    // 桌面端由代码图谱工作区承接内容滚动，避免根页面出现重复的纵向滚动条。
    document.documentElement.classList.add("codegraph-page-active");
    document.body.classList.add("codegraph-page-active");
    return () => {
      document.documentElement.classList.remove("codegraph-page-active");
      document.body.classList.remove("codegraph-page-active");
    };
  }, [status?.available]);

  const structureQuery = useQuery({
    queryKey: ["project", project.id, "codegraph", "structure", "", 0, 200],
    queryFn: () => api.getCodeGraphStructure(project.id, "", 200),
    enabled: status?.available === true && !operationRunning,
  });
  const searchQuery = useQuery({
    queryKey: ["project", project.id, "codegraph", "search", searchText, symbolKind ?? "", 0, 30],
    queryFn: () => api.searchCodeGraphSymbols(project.id, searchText, symbolKind),
    enabled: status?.available === true && !operationRunning && searchText.length > 0,
  });

  useEffect(() => {
    setTreeData((structureQuery.data?.items ?? []).map((entry) => treeNode(entry)));
  }, [structureQuery.data]);

  useEffect(() => {
    if (operationRunning) return;
    const resetRevision = `${project.id}:${status?.revision ?? ""}`;
    if (resetRevisionRef.current === resetRevision) return;
    resetRevisionRef.current = resetRevision;
    setSelectedSymbol(undefined);
    setSearchInput("");
    setSearchText("");
    setActivePanel("structure");
    setExpandedKeys([]);
  }, [operationRunning, project.id, status?.revision]);

  const loadTreeNode = async (rawNode: DataNode): Promise<void> => {
    const node = rawNode as CodeGraphTreeNode;
    if (node.children || !node.entry.expandable) return;
    const compactEntries = [...(node.compactEntries ?? [node.entry])];
    const visitedPaths = new Set(compactEntries.map((entry) => entry.path));
    let deepestEntry = node.entry;
    const fetchChildren = (path: string) => queryClient.fetchQuery({
      queryKey: ["project", project.id, "codegraph", "structure", path, 0, 200],
      queryFn: () => api.getCodeGraphStructure(project.id, path, 200),
    });
    let page = await fetchChildren(deepestEntry.path);

    // 目录链只有一个子目录时继续向下读取，最终以一个紧凑节点承载整条路径。
    while (deepestEntry.type === "directory" && page.total === 1 && page.items[0]?.type === "directory") {
      const nextEntry = page.items[0];
      if (visitedPaths.has(nextEntry.path)) {
        throw new Error(`CodeGraph 目录结构存在循环路径：${nextEntry.path}`);
      }
      visitedPaths.add(nextEntry.path);
      compactEntries.push(nextEntry);
      deepestEntry = nextEntry;
      page = await fetchChildren(deepestEntry.path);
    }

    const replacement = treeNode(deepestEntry, {
      key: node.key,
      compactEntries,
      children: page.items.map((entry) => treeNode(entry)),
    });
    setTreeData((current) => updateTreeNode(current, node.key, replacement));
  };

  const selectSymbol = (symbol: CodeGraphSymbol) => {
    setSelectedSymbol(symbol);
    setActivePanel("graph");
  };

  const languageTags = useMemo(() => status?.languages?.slice(0, 6) ?? [], [status?.languages]);

  if (statusQuery.isLoading) return <PageSkeleton rows={8} />;
  if (statusQuery.isError) return <ErrorState error={statusQuery.error} onRetry={() => void statusQuery.refetch()} />;

  const unavailableReason = !status?.available
    ? "请先在项目目录初始化 CodeGraph"
    : !status.cliAvailable
      ? "未检测到 CodeGraph CLI"
      : operationRunning
        ? operation.mode === "rebuild" ? "正在完整重建 CodeGraph 索引" : "正在同步 CodeGraph 索引"
        : undefined;
  const operationDisabled = Boolean(unavailableReason) || syncMutation.isPending;
  const startOperation = (mode: CodeGraphSyncMode) => {
    setSyncMenuOpen(false);
    if (!operationDisabled) syncMutation.mutate(mode);
  };
  const confirmRebuild = () => {
    setSyncMenuOpen(false);
    modal.confirm({
      title: "完整重建 CodeGraph 索引",
      content: "完整重建会强制重新索引整个项目，项目较大时可能耗时较长。确认继续吗？",
      okText: "开始完整重建",
      cancelText: "取消",
      okButtonProps: { danger: true },
      onOk: () => startOperation("rebuild"),
    });
  };
  const headerMeta = (
    <Space size={6} wrap>
      <Tag icon={<ShareAltOutlined />} color="green">CodeGraph 索引</Tag>
      {languageTags.map((language) => (
        <Tag key={language.name}>{language.name} · {language.fileCount}</Tag>
      ))}
    </Space>
  );
  const headerActions = (
    <Space className="codegraph-index-actions" size={10} wrap>
      <Typography.Text type="secondary">{minuteDate(status?.indexedAt)}</Typography.Text>
      {unavailableReason && !operationRunning ? (
        <Typography.Text type="danger">{unavailableReason}</Typography.Text>
      ) : null}
      <Tooltip title={unavailableReason}>
        <span>
          <Dropdown
            open={syncMenuOpen}
            onOpenChange={setSyncMenuOpen}
            disabled={operationDisabled}
            trigger={["hover"]}
            menu={{
              items: [{ key: "rebuild", label: "完整重建" }],
              onClick: ({ key }) => {
                if (key === "rebuild") confirmRebuild();
              },
            }}
          >
            <Space.Compact>
              <Button
                type="primary"
                icon={<SyncOutlined spin={operationRunning} />}
                disabled={operationDisabled}
                loading={syncMutation.isPending}
                onClick={() => startOperation("sync")}
              >
                {operationRunning ? (operation?.mode === "rebuild" ? "正在完整重建" : "正在同步") : "同步"}
              </Button>
              <Button
                type="primary"
                aria-label="更多 CodeGraph 同步操作"
                aria-haspopup="menu"
                aria-expanded={syncMenuOpen}
                icon={<DownOutlined />}
                disabled={operationDisabled}
                onClick={() => setSyncMenuOpen((open) => !open)}
              />
            </Space.Compact>
          </Dropdown>
        </span>
      </Tooltip>
    </Space>
  );
  if (!status?.available) {
    return (
      <div className="page codegraph-page">
        <PageHeader
          title="代码图谱"
          description="数据来源：CodeGraph。读取项目索引并检索代码结构与调用关系"
          meta={headerMeta}
          actions={headerActions}
        />
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
        description="数据来源：CodeGraph。按项目浏览代码结构，并从任意符号向上游、下游展开调用链"
        meta={headerMeta}
        actions={headerActions}
      />

      <div className="codegraph-metrics metric-strip">
        <Statistic title="文件" value={status.fileCount ?? 0} />
        <Statistic title="符号" value={status.nodeCount ?? 0} />
        <Statistic title="关系" value={status.edgeCount ?? 0} />
        <Statistic title="索引大小" value={formatBytes(status.databaseBytes)} />
      </div>

      <Segmented
        className="codegraph-panel-switch"
        block
        value={activePanel}
        options={[{ label: "结构与搜索", value: "structure" }, { label: "调用链", value: "graph" }]}
        onChange={(value) => setActivePanel(value as "structure" | "graph")}
      />

      {/* 两个面板保持挂载，切换时继续保留结构展开和画布浏览状态。 */}
      <div className="codegraph-workspace">
        <aside className="codegraph-explorer" hidden={activePanel !== "structure"}>
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
                  expandedKeys={expandedKeys}
                  selectedKeys={selectedSymbol ? [selectedSymbol.id] : []}
                  onExpand={(keys) => setExpandedKeys(keys)}
                  onSelect={(_keys, info) => {
                    const key = info.node.key;
                    const node = findTreeNode(treeData, key);
                    if (node?.entry.expandable) {
                      setExpandedKeys((current) => current.includes(key)
                        ? current.filter((item) => item !== key)
                        : [...current, key]);
                    }
                    if (node?.entry.symbol) selectSymbol(node.entry.symbol);
                  }}
                />
              )}
            </div>
          )}
        </aside>

        <section className="codegraph-graph-panel" hidden={activePanel !== "graph"}>
          <CodeGraphCanvas projectId={project.id} rootSymbol={selectedSymbol} paused={operationRunning} />
        </section>
      </div>
    </div>
  );
}
