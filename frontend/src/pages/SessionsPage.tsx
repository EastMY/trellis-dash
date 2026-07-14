import { ApartmentOutlined, LinkOutlined } from "@ant-design/icons";
import { useQuery } from "@tanstack/react-query";
import { Table, Tag, Typography } from "antd";
import type { ColumnsType } from "antd/es/table";
import { Link } from "react-router-dom";
import { api } from "../api/client";
import { useProjectContext } from "../components/AppShell";
import { PageHeader } from "../components/PageHeader";
import { EmptyState, ErrorState, PageSkeleton } from "../components/PageState";
import { StatusTag } from "../components/StatusTag";
import { fullDate, relativeDate } from "../lib/format";
import type { Session } from "../types";

export function SessionsPage() {
  const { project } = useProjectContext();
  const query = useQuery({
    queryKey: ["project", project.id, "sessions"],
    queryFn: () => api.getSessions(project.id),
  });

  const columns: ColumnsType<Session> = [
    {
      title: "Session Key",
      dataIndex: "key",
      width: 280,
      render: (value: string) => <Typography.Text className="mono" copyable>{value}</Typography.Text>,
    },
    { title: "平台", dataIndex: "platform", width: 130, render: (value: string) => <Tag variant="filled">{value || "unknown"}</Tag> },
    {
      title: "当前任务",
      dataIndex: "taskKey",
      width: 290,
      render: (value: string, session) => value ? (
        <Link to={`/projects/${project.id}/tasks/${encodeURIComponent(value)}`}><LinkOutlined /> {value}</Link>
      ) : <Typography.Text type="secondary">{session.currentTask || "未绑定"}</Typography.Text>,
    },
    { title: "状态", key: "state", width: 110, render: (_, session) => {
      const active = !session.stale && Boolean(session.taskKey);
      return <StatusTag status={active ? "in_progress" : "idle"} live={active} />;
    } },
    { title: "最后活跃", dataIndex: "lastSeenAt", width: 190, render: (value: string) => <span title={fullDate(value)}>{relativeDate(value)}</span> },
    { title: "源文件", dataIndex: "sourcePath", ellipsis: true, render: (value: string) => <Typography.Text className="mono" copyable>{value}</Typography.Text> },
  ];

  if (query.isLoading) return <PageSkeleton rows={8} />;
  if (query.isError) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />;

  const sessions = query.data ?? [];
  return (
    <div className="page sessions-page">
      <PageHeader
        title="Sessions"
        description="读取 .trellis/.runtime/sessions 中每个会话自己的活动任务指针"
        meta={<Tag variant="filled" icon={<ApartmentOutlined />}>{sessions.filter((item) => !item.stale && item.taskKey).length} 个活跃</Tag>}
      />
      {!sessions.length ? (
        <EmptyState title="暂无 Session" description="Agent 激活 Trellis 任务后，会话指针会显示在这里。" />
      ) : (
        <Table<Session>
          className="data-table"
          rowKey="key"
          size="small"
          columns={columns}
          dataSource={sessions}
          scroll={{ x: 1180 }}
          pagination={{ pageSize: 30, showTotal: (total) => `共 ${total} 个 Session` }}
          expandable={{
            expandedRowRender: (session) => (
              <pre className="inline-raw">{JSON.stringify(session.currentRun ?? {}, null, 2)}</pre>
            ),
            rowExpandable: (session) => Boolean(session.currentRun),
          }}
        />
      )}
    </div>
  );
}
