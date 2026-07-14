import { FolderAddOutlined, ProjectOutlined } from "@ant-design/icons";
import { useQuery } from "@tanstack/react-query";
import { Button, Typography } from "antd";
import { useState } from "react";
import { Navigate, useNavigate } from "react-router-dom";
import { api } from "../api/client";
import { AddProjectModal } from "../components/AddProjectModal";
import { ErrorState, PageSkeleton } from "../components/PageState";
import { ThemeToggle } from "../components/ThemeToggle";
import { useAppStore } from "../store/app";

export function RootPage() {
  const [open, setOpen] = useState(false);
  const navigate = useNavigate();
  const currentProjectId = useAppStore((state) => state.currentProjectId);
  const projectsQuery = useQuery({ queryKey: ["projects"], queryFn: api.listProjects });

  if (projectsQuery.isLoading) return <PageSkeleton rows={7} />;
  if (projectsQuery.isError) {
    return <ErrorState error={projectsQuery.error} onRetry={() => void projectsQuery.refetch()} />;
  }

  const projects = projectsQuery.data ?? [];
  if (projects.length) {
    const selected = projects.some((project) => project.id === currentProjectId)
      ? currentProjectId
      : projects[0].id;
    return <Navigate to={`/projects/${selected}`} replace />;
  }

  return (
    <main className="welcome-page">
      <ThemeToggle className="standalone-theme-toggle" />
      <section className="welcome-panel">
        <div className="welcome-mark"><ProjectOutlined /></div>
        <Typography.Title level={1}>把 Trellis 工作流放进一个视野</Typography.Title>
        <Typography.Paragraph>
          添加本机项目后，即可只读查看任务、Session、Context、Git 与 Worktree 状态。
        </Typography.Paragraph>
        <Button type="primary" size="large" icon={<FolderAddOutlined />} onClick={() => setOpen(true)}>
          添加首个项目
        </Button>
      </section>
      <AddProjectModal
        open={open}
        onCancel={() => setOpen(false)}
        onCreated={(project) => navigate(`/projects/${project.id}`)}
      />
    </main>
  );
}
