import {
  ApartmentOutlined,
  ArrowDownOutlined,
  ArrowUpOutlined,
  BranchesOutlined,
  CheckCircleOutlined,
  ClockCircleOutlined,
  CloudUploadOutlined,
  ExclamationCircleOutlined,
  FileDoneOutlined,
  FolderOpenOutlined,
} from "@ant-design/icons";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { App, Button, Empty, Space, Statistic, Tag, Tooltip, Typography } from "antd";
import { Link } from "react-router-dom";
import { api } from "../api/client";
import { CompletionTrendChart } from "../components/CompletionTrendChart";
import { PageHeader } from "../components/PageHeader";
import { ErrorState, PageSkeleton } from "../components/PageState";
import { StatusTag } from "../components/StatusTag";
import { TaskCard } from "../components/TaskCard";
import { useProjectContext } from "../components/AppShell";
import { relativeDate, shortHash, taskTitle } from "../lib/format";

export function OverviewPage() {
  const { project } = useProjectContext();
  const { message } = App.useApp();
  const queryClient = useQueryClient();
  const query = useQuery({
    queryKey: ["project", project.id, "dashboard"],
    queryFn: () => api.getDashboard(project.id),
  });
  const pushMutation = useMutation({
    mutationFn: () => api.pushGit(project.id),
    onSuccess: async (result) => {
      // Push 会改变 Git revision 和活动流；一次性失效相关查询，避免各区域短暂显示不同状态。
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["project", project.id, "dashboard"] }),
        queryClient.invalidateQueries({ queryKey: ["project", project.id, "git"] }),
        queryClient.invalidateQueries({ queryKey: ["project", project.id, "revision"] }),
        queryClient.invalidateQueries({ queryKey: ["project", project.id, "activity"] }),
      ]);
      if (result.warning) {
        message.warning(result.warning);
      } else {
        message.success(`已推送 ${result.branch} 到 ${result.upstream}`);
      }
    },
    onError: (error) => message.error(error instanceof Error ? error.message : "Git Push 失败"),
  });

  if (query.isLoading) return <PageSkeleton rows={8} />;
  if (query.isError || !query.data) {
    return <ErrorState error={query.error} onRetry={() => void query.refetch()} />;
  }

  const dashboard = query.data;
  const statistics = dashboard.statistics;
  const git = dashboard.git;
  const pushDisabledReason = !git?.branch
    ? "当前处于 detached HEAD"
    : !git.upstream
      ? "当前分支未配置上游分支"
      : git.ahead <= 0
        ? "没有待推送的提交"
        : undefined;

  const pushCurrentBranch = () => {
    // 前端先给出即时提示；服务端仍会基于实时仓库状态重复校验，防止缓存状态过期。
    if (!git?.branch) {
      message.warning("当前处于 detached HEAD，无法推送");
      return;
    }
    if (!git.upstream) {
      message.warning("当前分支未配置上游分支");
      return;
    }
    pushMutation.mutate();
  };

  return (
    <div className="page overview-page">
      <PageHeader
        title="项目概览"
        description={project.root}
        meta={<Tag variant="filled">{project.mode === "observer" ? "只读观察" : "控制模式"}</Tag>}
        actions={<Link to="tasks"><Button>查看全部任务</Button></Link>}
      />

      <section className="metric-strip" aria-label="任务统计">
        <Statistic title="任务总数" value={statistics.total} prefix={<FolderOpenOutlined />} />
        <Statistic title="活跃任务" value={statistics.active} prefix={<ClockCircleOutlined />} />
        <Statistic
          title="阻塞"
          value={statistics.blocked}
          prefix={<ExclamationCircleOutlined />}
          styles={statistics.blocked ? { content: { color: "var(--danger)" } } : undefined}
        />
        <Statistic title="今日完成" value={statistics.completedToday} prefix={<CheckCircleOutlined />} />
        <Statistic title="已归档" value={statistics.archived} prefix={<FileDoneOutlined />} />
      </section>

      <div className="overview-grid">
        <div className="overview-primary">
          <CompletionTrendChart
            completionItems={dashboard.completionTrend ?? []}
            gitItems={dashboard.gitCommitTrend ?? []}
            gitAvailable={dashboard.gitCommitTrendAvailable}
          />

          <section className="section-panel overview-active">
            <div className="section-heading">
              <div>
                <Typography.Title level={4}>活跃任务</Typography.Title>
                <Typography.Text type="secondary">按优先级与最近修改显示</Typography.Text>
              </div>
              <Typography.Text className="mono" type="secondary">{statistics.active} ACTIVE</Typography.Text>
            </div>
            {dashboard.activeTasks.length ? (
              <div className="task-grid task-grid-overview">
                {dashboard.activeTasks.slice(0, 6).map((task) => <TaskCard key={task.key} task={task} />)}
              </div>
            ) : (
              <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="当前没有活跃任务" />
            )}
          </section>
        </div>

        <aside className="overview-aside">
          <section className="section-panel git-brief">
            <div className="section-heading">
              <Typography.Title level={4}>Git 工作区</Typography.Title>
              <BranchesOutlined />
            </div>
            {git ? (
              <>
                <div className="git-branch-line">
                  <Typography.Text strong className="mono">{git.branch || "DETACHED"}</Typography.Text>
                  <Tag color={git.dirty ? "warning" : "success"}>{git.dirty ? "Dirty" : "Clean"}</Tag>
                </div>
                <Typography.Text type="secondary" className="mono">{shortHash(git.head)} / {git.upstream || "无上游"}</Typography.Text>
                <div className="git-counts">
                  <span>修改 <strong>{git.modified}</strong></span>
                  <span>新增 <strong>{git.added}</strong></span>
                  <span>未跟踪 <strong>{git.untracked}</strong></span>
                  <span className={git.conflicted ? "text-danger" : undefined}>冲突 <strong>{git.conflicted}</strong></span>
                  <span className="git-lines-added">增加行 <strong>+{git.linesAdded}</strong></span>
                  <span className="git-lines-deleted">删除行 <strong>-{git.linesDeleted}</strong></span>
                </div>
                <div className="git-brief-footer">
                  <Space size={14} className="ahead-behind">
                    <span><ArrowUpOutlined /> {git.ahead}</span>
                    <span><ArrowDownOutlined /> {git.behind}</span>
                    <Typography.Text type="secondary">{relativeDate(git.updatedAt)}</Typography.Text>
                  </Space>
                  <Tooltip title={pushDisabledReason ?? "仅推送已有提交，不包含工作区中的未提交修改"}>
                    {/* 禁用按钮不会触发鼠标事件，外层元素负责承载原因提示。 */}
                    <span className="git-push-button-wrap">
                      <Button
                        type="primary"
                        size="small"
                        className="git-push-button"
                        icon={<CloudUploadOutlined />}
                        loading={pushMutation.isPending}
                        disabled={Boolean(pushDisabledReason)}
                        aria-label={`推送当前分支${git.ahead > 0 ? `，共 ${git.ahead} 个领先提交` : ""}`}
                        onClick={pushCurrentBranch}
                      >
                        {git.ahead > 0 ? `Push (${git.ahead})` : "Push"}
                      </Button>
                    </span>
                  </Tooltip>
                </div>
              </>
            ) : (
              <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无 Git 快照" />
            )}
          </section>

          <section className="section-panel session-brief">
            <div className="section-heading">
              <Typography.Title level={4}>当前 Sessions</Typography.Title>
              <ApartmentOutlined />
            </div>
            {dashboard.sessions.length ? dashboard.sessions.slice(0, 5).map((session) => {
              const task = dashboard.activeTasks.find((item) => item.key === session.taskKey);
              return (
                <div className="session-row" key={session.key}>
                  <div>
                    <Typography.Text strong>{session.platform || "unknown"}</Typography.Text>
                    <Typography.Text type="secondary" className="mono">{session.key}</Typography.Text>
                  </div>
                  <div className="session-row-task">
                    <Typography.Text ellipsis>{task ? taskTitle(task) : session.currentTask || "未绑定任务"}</Typography.Text>
                    <StatusTag status={!session.stale && session.taskKey ? "in_progress" : "idle"} live={!session.stale && Boolean(session.taskKey)} />
                  </div>
                </div>
              );
            }) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无活动 Session" />}
          </section>
        </aside>
      </div>

    </div>
  );
}
