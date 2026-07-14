import {
  ArrowLeftOutlined,
  BranchesOutlined,
  CheckCircleOutlined,
  CloseCircleOutlined,
  FileMarkdownOutlined,
  LinkOutlined,
} from "@ant-design/icons";
import { useQuery } from "@tanstack/react-query";
import {
  Alert,
  Button,
  Descriptions,
  List,
  Space,
  Table,
  Tabs,
  Tag,
  Typography,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { api } from "../api/client";
import { ActivityList } from "../components/ActivityList";
import { useProjectContext } from "../components/AppShell";
import { MarkdownViewer, RawViewer } from "../components/MarkdownViewer";
import { PageHeader } from "../components/PageHeader";
import { ErrorState, PageSkeleton } from "../components/PageState";
import { StatusTag } from "../components/StatusTag";
import { formatBytes, fullDate, relativeDate, taskTitle, toArray } from "../lib/format";
import type { Artifact, ContextEntry, Subtask } from "../types";

function findArtifact(artifacts: Artifact[], kinds: string[]): Artifact | undefined {
  return artifacts.find((artifact) => {
    const haystack = `${artifact.kind} ${artifact.name}`.toLowerCase();
    return kinds.some((kind) => haystack.includes(kind));
  });
}

function ArtifactPanel({
  projectId,
  taskKey,
  artifact,
  label,
  enabled,
}: {
  projectId: string;
  taskKey: string;
  artifact?: Artifact;
  label: string;
  enabled: boolean;
}) {
  const query = useQuery({
    queryKey: ["project", projectId, "tasks", "detail", taskKey, "artifact", artifact?.path],
    queryFn: () => api.getArtifact(projectId, taskKey, artifact!.path),
    // 显式服从当前标签，避免依赖 Tabs 对隐藏面板的挂载实现。
    enabled: Boolean(artifact) && enabled,
  });
  return (
    <div className="artifact-panel">
      {artifact && (
        <div className="artifact-meta">
          <span><FileMarkdownOutlined /> {artifact.name}</span>
          <span>{formatBytes(artifact.size)}</span>
          <span>{fullDate(artifact.modifiedAt)}</span>
          <Typography.Text className="mono" copyable>{artifact.path}</Typography.Text>
        </div>
      )}
      {query.isLoading ? (
        <PageSkeleton rows={7} />
      ) : query.isError ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} compact />
      ) : (
        <MarkdownViewer content={query.data?.content} name={label} />
      )}
    </div>
  );
}

export function TaskDetailPage() {
  const { project } = useProjectContext();
  const { taskKey = "" } = useParams();
  const [activeTab, setActiveTab] = useState("overview");
  const query = useQuery({
    queryKey: ["project", project.id, "tasks", "detail", taskKey],
    queryFn: () => api.getTask(project.id, taskKey),
    enabled: Boolean(taskKey),
  });

  if (query.isLoading) return <PageSkeleton rows={12} />;
  if (query.isError || !query.data?.task) {
    return <ErrorState error={query.error ?? new Error("任务不存在或已移动")} onRetry={() => void query.refetch()} />;
  }

  const detail = query.data;
  const task = detail.task;
  const artifacts = detail.artifacts ?? [];
  const context = detail.context ?? [];
  const sessions = detail.sessions ?? [];
  const activity = detail.activity ?? [];
  const prd = findArtifact(artifacts, ["prd"]);
  const design = findArtifact(artifacts, ["design"]);
  const implementation = findArtifact(artifacts, ["implement.md", "implementation"]);
  const extraArtifacts = artifacts.filter((artifact) => artifact !== prd && artifact !== design && artifact !== implementation);
  const subtasks = toArray<Subtask>(task.subtasks);
  const relatedFiles = toArray<string>(task.relatedFiles);

  const contextColumns: ColumnsType<ContextEntry> = [
    {
      title: "阶段",
      dataIndex: "action",
      width: 170,
      render: (value: string, entry) => (
        <Space size={4}>
          <Tag variant="filled">{value}</Tag>
          {entry.example && <Tag>模板</Tag>}
        </Space>
      ),
    },
    { title: "行", dataIndex: "line", width: 60, align: "right" },
    {
      title: "类型",
      dataIndex: "type",
      width: 90,
      render: (value: string, entry) => entry.example ? "模板" : value === "directory" ? "目录" : "文件",
    },
    { title: "文件", dataIndex: "file", ellipsis: true, render: (value: string) => <Typography.Text className="mono" copyable>{value || "-"}</Typography.Text> },
    { title: "用途", dataIndex: "reason", ellipsis: true, render: (value: string) => value || "未说明" },
    { title: "存在", dataIndex: "exists", width: 70, align: "center", render: (value: boolean, entry) => entry.example ? "-" : value ? <CheckCircleOutlined className="text-success" /> : <CloseCircleOutlined className="text-danger" /> },
    { title: "有效", dataIndex: "valid", width: 70, align: "center", render: (value: boolean) => value ? <CheckCircleOutlined className="text-success" /> : <CloseCircleOutlined className="text-danger" /> },
    { title: "诊断", dataIndex: "error", width: 230, render: (value: string, entry) => entry.example ? <Typography.Text type="secondary">模板占位行</Typography.Text> : entry.duplicate ? <Typography.Text type="danger">重复引用</Typography.Text> : value ? <Typography.Text type="danger">{value}</Typography.Text> : "-" },
  ];

  const overview = (
    <div className="task-overview">
      {task.contextIssues > 0 && (
        <Alert
          showIcon
          type="warning"
          message={`发现 ${task.contextIssues} 个 Context 问题`}
          description="请在 Context 标签中检查路径、重复项与示例占位项。"
        />
      )}
      <Descriptions
        bordered
        size="small"
        column={{ xs: 1, sm: 2, lg: 3 }}
        items={[
          { key: "key", label: "任务 Key", children: <Typography.Text className="mono" copyable>{task.key}</Typography.Text> },
          { key: "status", label: "事实状态", children: <StatusTag status={task.status} /> },
          { key: "phase", label: "运行阶段", children: <StatusTag status={task.runtimePhase || task.status} live={task.activeSessions > 0} /> },
          { key: "priority", label: "优先级", children: task.priority || "P2" },
          { key: "assignee", label: "负责人", children: task.assignee || "未分配" },
          { key: "creator", label: "创建者", children: task.creator || "未知" },
          { key: "branch", label: "分支", children: <Typography.Text className="mono">{task.branch || "-"}</Typography.Text> },
          { key: "base", label: "基线分支", children: <Typography.Text className="mono">{task.baseBranch || "-"}</Typography.Text> },
          { key: "commit", label: "提交", children: <Typography.Text className="mono" copyable={Boolean(task.commit)}>{task.commit?.slice(0, 12) || "-"}</Typography.Text> },
          { key: "created", label: "创建时间", children: fullDate(task.createdAt) },
          { key: "modified", label: "更新时间", children: `${fullDate(task.modifiedAt)}（${relativeDate(task.modifiedAt)}）` },
          { key: "completed", label: "完成时间", children: fullDate(task.completedAt) },
          { key: "source", label: "源路径", span: 3, children: <Typography.Text className="mono" copyable>{task.sourcePath}</Typography.Text> },
          { key: "worktree", label: "Worktree", span: 3, children: <Typography.Text className="mono" copyable={Boolean(task.worktreePath)}>{task.worktreePath || "-"}</Typography.Text> },
        ]}
      />

      <div className="detail-columns">
        <section className="section-panel">
          <Typography.Title level={4}>子任务</Typography.Title>
          <List
            size="small"
            locale={{ emptyText: "暂无子任务" }}
            dataSource={subtasks}
            renderItem={(item, index) => (
              <List.Item>
                <Space>
                  {item.completed || item.status === "completed" ? <CheckCircleOutlined className="text-success" /> : <span className="subtask-index mono">{index + 1}</span>}
                  <Typography.Text>{item.title || item.name || item.id || `子任务 ${index + 1}`}</Typography.Text>
                </Space>
                {item.status && <StatusTag status={item.status} />}
              </List.Item>
            )}
          />
        </section>
        <section className="section-panel">
          <Typography.Title level={4}>关联文件</Typography.Title>
          <List
            size="small"
            locale={{ emptyText: "暂无关联文件" }}
            dataSource={relatedFiles}
            renderItem={(file) => <List.Item><Typography.Text className="mono" copyable>{file}</Typography.Text></List.Item>}
          />
        </section>
      </div>

      {task.notes && (
        <section className="section-panel">
          <Typography.Title level={4}>备注</Typography.Title>
          <Typography.Paragraph>{task.notes}</Typography.Paragraph>
        </section>
      )}
    </div>
  );

  const contextPanel = (
    <div className="context-panel">
      <div className="context-summary">
        <Typography.Text>{context.length} 条 Context 记录</Typography.Text>
        <Typography.Text type="secondary">{context.filter((entry) => entry.example).length} 条模板</Typography.Text>
        <Typography.Text type={context.some((entry) => entry.duplicate) ? "warning" : "secondary"}>
          {context.filter((entry) => entry.duplicate).length} 条重复
        </Typography.Text>
        <Typography.Text type={context.some((entry) => !entry.valid) ? "danger" : "secondary"}>
          {context.filter((entry) => !entry.valid).length} 条异常
        </Typography.Text>
      </div>
      <Table<ContextEntry>
        size="small"
        rowKey={(entry) => `${entry.action}-${entry.line}`}
        columns={contextColumns}
        dataSource={context}
        scroll={{ x: 1180 }}
        pagination={false}
        locale={{ emptyText: "暂无 Context 记录" }}
        rowClassName={(entry) => entry.valid ? "" : "row-invalid"}
      />
    </div>
  );

  return (
    <div className="page task-detail-page">
      <PageHeader
        title={taskTitle(task)}
        description={task.description || "暂无任务说明"}
        meta={<StatusTag status={task.runtimePhase || task.status} live={task.activeSessions > 0} />}
        actions={
          <Space>
            {task.prUrl && <Button icon={<LinkOutlined />} href={task.prUrl} target="_blank">打开 PR</Button>}
            <Link to={`/projects/${project.id}/tasks`}><Button icon={<ArrowLeftOutlined />}>返回任务</Button></Link>
          </Space>
        }
      />

      <div className="task-detail-facts">
        <Tag variant="filled">{task.priority || "P2"}</Tag>
        <span><BranchesOutlined /> <Typography.Text className="mono">{task.branch || "未绑定分支"}</Typography.Text></span>
        <span>{artifacts.length} 个文档</span>
        <span>{sessions.length} 个 Session</span>
      </div>

      <Tabs
        className="detail-tabs"
        activeKey={activeTab}
        onChange={setActiveTab}
        items={[
          { key: "overview", label: "概览", children: overview },
          { key: "prd", label: "PRD", children: <ArtifactPanel projectId={project.id} taskKey={taskKey} artifact={prd} label="PRD" enabled={activeTab === "prd"} /> },
          { key: "design", label: "设计", children: <ArtifactPanel projectId={project.id} taskKey={taskKey} artifact={design} label="设计文档" enabled={activeTab === "design"} /> },
          { key: "implementation", label: "实施", children: <ArtifactPanel projectId={project.id} taskKey={taskKey} artifact={implementation} label="实施计划" enabled={activeTab === "implementation"} /> },
          ...extraArtifacts.map((artifact) => {
            const key = `artifact-${artifact.path}`;
            return {
              key,
            label: artifact.kind === "info" ? "技术说明" : artifact.name,
              children: <ArtifactPanel projectId={project.id} taskKey={taskKey} artifact={artifact} label={artifact.name} enabled={activeTab === key} />,
            };
          }),
          { key: "context", label: `Context (${context.length})`, children: contextPanel },
          { key: "activity", label: `活动 (${activity.length})`, children: <ActivityList items={activity} /> },
          { key: "raw", label: "Raw", children: <RawViewer value={task} /> },
        ]}
      />
    </div>
  );
}
