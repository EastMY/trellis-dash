import {
  AppstoreOutlined,
  BarsOutlined,
  SearchOutlined,
} from "@ant-design/icons";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import {
  Button,
  Input,
  Segmented,
  Select,
  Space,
  Spin,
  Table,
  Tag,
  Typography,
} from "antd";
import type { ColumnsType, TablePaginationConfig } from "antd/es/table";
import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api/client";
import { useProjectContext } from "../components/AppShell";
import { PageHeader } from "../components/PageHeader";
import { EmptyState, ErrorState, PageSkeleton } from "../components/PageState";
import { StatusTag } from "../components/StatusTag";
import { TaskCard } from "../components/TaskCard";
import { fullDate, statusLabel, taskTitle } from "../lib/format";
import type { Task, WorkflowState } from "../types";

const BOARD_PAGE_SIZE = 50;

const fallbackStates: WorkflowState[] = [
  { projectId: "", name: "planning", label: "规划中", order: 10 },
  { projectId: "", name: "in_progress", label: "实施中", order: 20 },
  { projectId: "", name: "review", label: "待检查", order: 30 },
  { projectId: "", name: "completed", label: "已完成", order: 40 },
];

interface BoardColumnProps {
  projectId: string;
  state: WorkflowState;
  search: string;
  priority?: string;
}

function TaskBoardColumn({ projectId, state, search, priority }: BoardColumnProps) {
  const query = useInfiniteQuery({
    queryKey: ["project", projectId, "tasks", "board", state.name, { search, priority }],
    queryFn: ({ pageParam }) => api.getTasks(projectId, {
      archived: false,
      status: state.name,
      q: search,
      priority,
      limit: BOARD_PAGE_SIZE,
      offset: pageParam,
    }),
    initialPageParam: 0,
    getNextPageParam: (page) => {
      const nextOffset = page.offset + page.items.length;
      return nextOffset < page.total ? nextOffset : undefined;
    },
  });
  const tasks = query.data?.pages.flatMap((page) => page.items) ?? [];
  const total = query.data?.pages[0]?.total ?? 0;

  return (
    <section className="kanban-column">
      <div className="kanban-header">
        <StatusTag status={state.name} />
        <span className="mono">{tasks.length}/{total}</span>
      </div>
      <div className="kanban-stack">
        {query.isLoading ? (
          <div className="kanban-loading"><Spin size="small" /></div>
        ) : query.isError ? (
          <ErrorState error={query.error} onRetry={() => void query.refetch()} compact />
        ) : tasks.length ? (
          tasks.map((task) => <TaskCard key={task.key} task={task} />)
        ) : (
          <div className="kanban-empty">该状态暂无任务</div>
        )}
        {query.hasNextPage && (
          <Button block loading={query.isFetchingNextPage} onClick={() => void query.fetchNextPage()}>
            加载更多（剩余 {Math.max(0, total - tasks.length)}）
          </Button>
        )}
      </div>
    </section>
  );
}

export function TasksPage({ archived = false }: { archived?: boolean }) {
  const { project } = useProjectContext();
  const [view, setView] = useState<"board" | "list">(archived ? "list" : "board");
  const [search, setSearch] = useState("");
  const [status, setStatus] = useState<string>();
  const [priority, setPriority] = useState<string>();
  const [pagination, setPagination] = useState({ current: 1, pageSize: 30 });

  const statesQuery = useQuery({
    queryKey: ["project", project.id, "tasks", "workflow-states"],
    queryFn: () => api.getWorkflowStates(project.id),
    enabled: !archived,
  });
  const listEnabled = archived || view === "list";
  const listQuery = useQuery({
    queryKey: ["project", project.id, "tasks", "list", {
      archived, search, status, priority,
      page: pagination.current, pageSize: pagination.pageSize,
    }],
    queryFn: () => api.getTasks(project.id, {
      archived,
      q: search,
      status,
      priority,
      limit: pagination.pageSize,
      offset: (pagination.current - 1) * pagination.pageSize,
    }),
    enabled: listEnabled,
  });
  const countQuery = useQuery({
    queryKey: ["project", project.id, "tasks", "count", { archived, search, status, priority }],
    queryFn: () => api.getTasks(project.id, { archived, q: search, status, priority, limit: 1, offset: 0 }),
    enabled: !listEnabled,
  });

  useEffect(() => {
    setPagination((current) => ({ ...current, current: 1 }));
  }, [archived, priority, search, status]);

  const states = useMemo(() => {
    const configured = statesQuery.data?.length ? [...statesQuery.data] : [...fallbackStates];
    return configured.sort((left, right) => left.order - right.order || left.name.localeCompare(right.name));
  }, [statesQuery.data]);
  const visibleStates = status ? states.filter((state) => state.name === status) : states;
  const tasks = listQuery.data?.items ?? [];
  const total = listEnabled ? (listQuery.data?.total ?? 0) : (countQuery.data?.total ?? 0);

  const columns: ColumnsType<Task> = [
    {
      title: "任务",
      key: "title",
      width: 340,
      render: (_, task) => (
        <div className="task-title-cell">
          <Link to={`/projects/${project.id}/tasks/${encodeURIComponent(task.key)}`}>{taskTitle(task)}</Link>
          <Typography.Text type="secondary" className="mono">{task.key}</Typography.Text>
        </div>
      ),
    },
    { title: "状态", dataIndex: "status", width: 120, render: (value: string) => <StatusTag status={value} /> },
    { title: "优先级", dataIndex: "priority", width: 90, render: (value: string) => <Tag variant="filled">{value || "P2"}</Tag> },
    { title: "负责人", dataIndex: "assignee", width: 130, render: (value: string) => value || "未分配" },
    { title: "分支", dataIndex: "branch", width: 210, ellipsis: true, render: (value: string) => <span className="mono">{value || "-"}</span> },
    { title: "文档", dataIndex: "artifactCount", width: 76, align: "right" },
    { title: "问题", dataIndex: "contextIssues", width: 76, align: "right", render: (value: number) => <span className={value ? "text-danger" : undefined}>{value ?? 0}</span> },
    { title: archived ? "完成时间" : "更新时间", dataIndex: archived ? "completedAt" : "modifiedAt", width: 170, render: (value: string) => fullDate(value) },
  ];

  const loading = listEnabled ? listQuery.isLoading : statesQuery.isLoading;
  const error = listEnabled ? listQuery.error : statesQuery.error;
  if (loading) return <PageSkeleton rows={9} />;
  if (error) return <ErrorState error={error} />;

  const onTableChange = (next: TablePaginationConfig) => {
    setPagination({
      current: next.current ?? 1,
      pageSize: next.pageSize ?? pagination.pageSize,
    });
  };

  return (
    <div className="page tasks-page">
      <PageHeader
        title={archived ? "任务归档" : "任务工作台"}
        description={archived ? "按时间检索已完成并归档的任务" : "状态列按需分页，Observer 模式不修改任务状态"}
        meta={<Tag variant="filled">{total} 个任务</Tag>}
        actions={!archived && (
          <Segmented
            value={view}
            onChange={(value) => setView(value as "board" | "list")}
            options={[
              { label: "看板", value: "board", icon: <AppstoreOutlined /> },
              { label: "列表", value: "list", icon: <BarsOutlined /> },
            ]}
          />
        )}
      />

      <div className="task-toolbar">
        <Input.Search
          allowClear
          prefix={<SearchOutlined />}
          placeholder="搜索标题、说明、分支"
          onSearch={setSearch}
          className="task-search"
        />
        <Space wrap>
          <Select
            allowClear
            placeholder="全部状态"
            value={status}
            onChange={setStatus}
            options={states.map((item) => ({ value: item.name, label: item.label || statusLabel(item.name) }))}
          />
          <Select
            allowClear
            placeholder="全部优先级"
            value={priority}
            onChange={setPriority}
            options={["P0", "P1", "P2", "P3"].map((value) => ({ value, label: value }))}
          />
          {(search || status || priority) && (
            <Button onClick={() => { setSearch(""); setStatus(undefined); setPriority(undefined); }}>清除筛选</Button>
          )}
        </Space>
      </div>

      {view === "board" && !archived ? (
        <div className="kanban" aria-label="任务看板">
          {visibleStates.map((state) => (
            <TaskBoardColumn
              key={state.name}
              projectId={project.id}
              state={state}
              search={search}
              priority={priority}
            />
          ))}
        </div>
      ) : !tasks.length ? (
        <EmptyState
          kind="task"
          title={archived ? "还没有归档任务" : "没有符合条件的任务"}
          description={archived ? "完成并归档的 Trellis 任务会显示在这里。" : "请调整筛选条件，或在项目中创建 Trellis 任务。"}
        />
      ) : (
        <Table<Task>
          className="data-table"
          rowKey="key"
          columns={columns}
          dataSource={tasks}
          scroll={{ x: 1220 }}
          pagination={{
            current: pagination.current,
            pageSize: pagination.pageSize,
            total: listQuery.data?.total ?? 0,
            showSizeChanger: true,
            showTotal: (value) => `共 ${value} 条`,
          }}
          onChange={onTableChange}
          size="small"
        />
      )}
    </div>
  );
}
