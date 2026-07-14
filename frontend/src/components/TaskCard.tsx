import {
  ApartmentOutlined,
  BranchesOutlined,
  FileTextOutlined,
  IssuesCloseOutlined,
  UserOutlined,
} from "@ant-design/icons";
import { Progress, Tag, Tooltip, Typography } from "antd";
import { Link } from "react-router-dom";
import { relativeDate, subtaskProgress, taskTitle } from "../lib/format";
import type { Task } from "../types";
import { StatusTag } from "./StatusTag";

export function TaskCard({ task }: { task: Task }) {
  const progress = subtaskProgress(task);
  const percent = progress.total ? Math.round((progress.done / progress.total) * 100) : 0;

  return (
    <Link className="task-card-link" to={`/projects/${encodeURIComponent(task.projectId)}/tasks/${encodeURIComponent(task.key)}`}>
      <article className="task-card">
        <div className="task-card-topline">
          <Tag variant="filled" className={`priority priority-${(task.priority || "p2").toLowerCase()}`}>
            {task.priority || "P2"}
          </Tag>
          <Typography.Text type="secondary" className="mono task-updated">
            {relativeDate(task.modifiedAt)}
          </Typography.Text>
        </div>
        {/* 宽卡片用右侧区域承载状态与负责人信息，减少无效留白和纵向占用。 */}
        <div className="task-card-body">
          <div className="task-card-summary">
            <Typography.Title level={5} ellipsis={{ rows: 2 }} title={taskTitle(task)}>
              {taskTitle(task)}
            </Typography.Title>
            <Typography.Paragraph type="secondary" ellipsis={{ rows: 2 }} className="task-card-description">
              {task.description || "暂无任务说明"}
            </Typography.Paragraph>
          </div>

          <div className="task-card-meta">
            <div className="task-card-state">
              <StatusTag status={task.runtimePhase || task.status} live={task.activeSessions > 0} />
              {progress.total > 0 && (
                <Tooltip title={`${progress.done}/${progress.total} 个子任务已完成`}>
                  <div className="task-progress">
                    <Progress percent={percent} showInfo={false} size="small" />
                    <span className="mono">{progress.done}/{progress.total}</span>
                  </div>
                </Tooltip>
              )}
            </div>

            <div className="task-card-facts">
              <Tooltip title="负责人">
                <span><UserOutlined /> {task.assignee || "未分配"}</span>
              </Tooltip>
              <Tooltip title="分支">
                <span><BranchesOutlined /> {task.branch || "未绑定"}</span>
              </Tooltip>
            </div>
          </div>
        </div>
        <div className="task-card-footer">
          <span><FileTextOutlined /> {task.artifactCount ?? 0}</span>
          <span className={task.contextIssues > 0 ? "text-danger" : undefined}>
            <IssuesCloseOutlined /> {task.contextIssues ?? 0}
          </span>
          <span><ApartmentOutlined /> {task.activeSessions ?? 0}</span>
          <span className="task-key mono">{task.key}</span>
        </div>
      </article>
    </Link>
  );
}
