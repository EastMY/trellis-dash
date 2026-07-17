import {
  ApartmentOutlined,
  AppstoreOutlined,
  BranchesOutlined,
  CheckCircleOutlined,
  HistoryOutlined,
  FolderAddOutlined,
  MenuOutlined,
  OrderedListOutlined,
  ShareAltOutlined,
  ReloadOutlined,
  SettingOutlined,
} from "@ant-design/icons";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Badge,
  Button,
  Dropdown,
  Layout,
  Menu,
  Space,
  Tooltip,
  Typography,
} from "antd";
import { useEffect, useMemo, useRef, useState } from "react";
import type { DragEvent, KeyboardEvent, WheelEvent } from "react";
import { Outlet, useLocation, useNavigate, useOutletContext, useParams } from "react-router-dom";
import { api } from "../api/client";
import { useProjectPolling } from "../hooks/useProjectPolling";
import { relativeDate } from "../lib/format";
import { projectSectionPath, selectedMenuKey } from "../lib/navigation";
import { useAppStore } from "../store/app";
import type { Project } from "../types";
import { AddProjectModal } from "./AddProjectModal";
import { EmptyState, ErrorState, PageSkeleton } from "./PageState";
import { SidebarActivity } from "./SidebarActivity";
import { ThemeToggle } from "./ThemeToggle";

const { Header, Sider, Content } = Layout;

interface ProjectOutletContext {
  project: Project;
}

export function useProjectContext(): ProjectOutletContext {
  return useOutletContext<ProjectOutletContext>();
}

export function projectListPollingOptions(visible: boolean): {
  refetchInterval: number | false;
  refetchOnWindowFocus: "always";
  refetchOnReconnect: "always";
} {
  return {
    // 项目列表包含所有标签的活跃任务数，前台统一轮询可避免按项目逐个请求。
    refetchInterval: visible ? 10_000 : false,
    refetchOnWindowFocus: "always" as const,
    refetchOnReconnect: "always" as const,
  };
}

export function orderProjects(projects: Project[], projectOrder: string[]): Project[] {
  const projectsById = new Map(projects.map((project) => [project.id, project]));
  const ordered = projectOrder.flatMap((projectId) => {
    const project = projectsById.get(projectId);
    if (!project) return [];
    projectsById.delete(projectId);
    return [project];
  });

  // 新增项目或旧偏好缺失的项目继续沿用服务端顺序，避免被意外隐藏。
  return [...ordered, ...projects.filter((project) => projectsById.has(project.id))];
}

export function moveProject(
  projects: Project[],
  sourceProjectId: string,
  targetProjectId: string,
): string[] {
  const projectOrder = projects.map((project) => project.id);
  const sourceIndex = projectOrder.indexOf(sourceProjectId);
  const targetIndex = projectOrder.indexOf(targetProjectId);
  if (sourceIndex < 0 || targetIndex < 0 || sourceIndex === targetIndex) return projectOrder;

  projectOrder.splice(sourceIndex, 1);
  projectOrder.splice(targetIndex, 0, sourceProjectId);
  return projectOrder;
}

export function AppShell() {
  const { projectId } = useParams();
  const navigate = useNavigate();
  const location = useLocation();
  const queryClient = useQueryClient();
  const [addOpen, setAddOpen] = useState(false);
  const [draggedProjectId, setDraggedProjectId] = useState<string>();
  const [dropTargetProjectId, setDropTargetProjectId] = useState<string>();
  const activeProjectTagRef = useRef<HTMLButtonElement>(null);
  const projectOrder = useAppStore((state) => state.projectOrder);
  const setCurrentProjectId = useAppStore((state) => state.setCurrentProjectId);
  const setProjectOrder = useAppStore((state) => state.setProjectOrder);
  const polling = useProjectPolling(projectId);
  const projectsQuery = useQuery({
    queryKey: ["projects"],
    queryFn: api.listProjects,
    ...projectListPollingOptions(polling.visible),
  });
  const orderedProjects = useMemo(
    () => orderProjects(projectsQuery.data ?? [], projectOrder),
    [projectOrder, projectsQuery.data],
  );
  const project = projectsQuery.data?.find((item) => item.id === projectId);
  const dashboardQuery = useQuery({
    queryKey: ["project", projectId, "dashboard"],
    queryFn: () => api.getDashboard(projectId!),
    enabled: Boolean(projectId && project),
  });

  useEffect(() => {
    if (projectId) setCurrentProjectId(projectId);
  }, [projectId, setCurrentProjectId]);

  useEffect(() => {
    // 项目较多时，路由切换后确保当前项目标签仍出现在可视区域内。
    activeProjectTagRef.current?.scrollIntoView({ block: "nearest", inline: "nearest" });
  }, [projectId]);

  const menuItems = useMemo(
    () => [
      { key: "overview", icon: <AppstoreOutlined />, label: "概览" },
      { key: "tasks", icon: <OrderedListOutlined />, label: "任务" },
      { key: "archive", icon: <CheckCircleOutlined />, label: "归档" },
      { key: "sessions", icon: <ApartmentOutlined />, label: "Sessions" },
      { key: "git", icon: <BranchesOutlined />, label: "Git / Worktree" },
      { key: "activity", icon: <HistoryOutlined />, label: "活动记录" },
      { key: "codegraph", icon: <ShareAltOutlined />, label: "代码图谱" },
      { type: "divider" as const },
      { key: "settings", icon: <SettingOutlined />, label: "项目设置" },
    ],
    [],
  );

  const openMenu = (key: string) => {
    if (!projectId) return;
    const path: Record<string, string> = {
      overview: "",
      tasks: "/tasks",
      archive: "/tasks/archive",
      sessions: "/sessions",
      git: "/git",
      activity: "/activity",
      codegraph: "/codegraph",
      settings: "/settings",
    };
    navigate(`/projects/${projectId}${path[key] ?? ""}`);
  };

  const switchProject = (nextId: string) => {
    if (nextId === projectId) return;
    setCurrentProjectId(nextId);
    navigate({
      pathname: `/projects/${nextId}${projectSectionPath(location.pathname)}`,
      search: location.search,
      hash: location.hash,
    });
  };

  const startProjectDrag = (event: DragEvent<HTMLButtonElement>, sourceProjectId: string) => {
    event.dataTransfer.effectAllowed = "move";
    event.dataTransfer.setData("text/plain", sourceProjectId);
    setDraggedProjectId(sourceProjectId);
  };

  const dragOverProject = (event: DragEvent<HTMLButtonElement>, targetProjectId: string) => {
    event.preventDefault();
    event.dataTransfer.dropEffect = "move";
    setDropTargetProjectId(draggedProjectId === targetProjectId ? undefined : targetProjectId);
  };

  const dropProject = (event: DragEvent<HTMLButtonElement>, targetProjectId: string) => {
    event.preventDefault();
    const sourceProjectId = event.dataTransfer.getData("text/plain") || draggedProjectId;
    if (sourceProjectId && sourceProjectId !== targetProjectId) {
      setProjectOrder(moveProject(orderedProjects, sourceProjectId, targetProjectId));
    }
    setDraggedProjectId(undefined);
    setDropTargetProjectId(undefined);
  };

  const moveProjectWithKeyboard = (
    event: KeyboardEvent<HTMLButtonElement>,
    sourceProjectId: string,
  ) => {
    if (!event.altKey || (event.key !== "ArrowLeft" && event.key !== "ArrowRight")) return;

    const sourceIndex = orderedProjects.findIndex((item) => item.id === sourceProjectId);
    const targetIndex = sourceIndex + (event.key === "ArrowLeft" ? -1 : 1);
    const targetProject = orderedProjects[targetIndex];
    if (!targetProject) return;

    event.preventDefault();
    setProjectOrder(moveProject(orderedProjects, sourceProjectId, targetProject.id));
  };

  const scrollProjectTags = (event: WheelEvent<HTMLDivElement>) => {
    const container = event.currentTarget;
    const maxScrollLeft = container.scrollWidth - container.clientWidth;
    if (maxScrollLeft <= 0 || Math.abs(event.deltaY) <= Math.abs(event.deltaX)) return;

    // 普通鼠标滚轮也可直接横向浏览项目标签；到达边缘后继续滚动页面。
    const nextScrollLeft = Math.max(0, Math.min(maxScrollLeft, container.scrollLeft + event.deltaY));
    if (nextScrollLeft === container.scrollLeft) return;
    event.preventDefault();
    container.scrollLeft = nextScrollLeft;
  };

  const refreshProject = async () => {
    if (!projectId) return;
    await queryClient.invalidateQueries({ queryKey: ["project", projectId] });
  };

  if (projectsQuery.isLoading) return <PageSkeleton rows={8} />;
  if (projectsQuery.isError) {
    return <ErrorState error={projectsQuery.error} onRetry={() => void projectsQuery.refetch()} />;
  }
  if (!project) {
    return (
      <div className="standalone-state">
        <EmptyState
          kind="project"
          title="找不到这个项目"
          description="项目可能已被移除，请返回项目列表重新选择。"
          action={<Button onClick={() => navigate("/")}>返回项目列表</Button>}
        />
      </div>
    );
  }

  const syncLabel = !polling.visible
    ? "页面已暂停刷新"
    : polling.isError
      ? "刷新失败，正在重试"
      : polling.isFetching
        ? "正在同步"
        : `已同步 ${relativeDate(polling.data?.updatedAt)}`;

  return (
    <Layout className="app-layout">
      <Sider width={248} className="app-sider">
        <button className="brand" onClick={() => navigate(`/projects/${projectId}`)} aria-label="返回概览">
          <span className="brand-mark">T</span>
          <span className="brand-copy">
            <strong>Trellis</strong>
            <small>Dashboard</small>
          </span>
        </button>
        <Menu
          mode="inline"
          items={menuItems}
          selectedKeys={[selectedMenuKey(location.pathname)]}
          onClick={({ key }) => openMenu(key)}
          className="app-menu"
        />
        <SidebarActivity
          items={dashboardQuery.data?.recentActivity ?? []}
          loading={dashboardQuery.isLoading}
          error={dashboardQuery.error}
          onRetry={() => void dashboardQuery.refetch()}
          onViewAll={() => openMenu("activity")}
        />
      </Sider>

      <Layout className="app-main">
        <Header className="app-header">
          <div className="project-switcher">
            <Dropdown
              trigger={["click"]}
              menu={{ items: menuItems, selectedKeys: [selectedMenuKey(location.pathname)], onClick: ({ key }) => openMenu(key) }}
            >
              <Button className="mobile-menu-button" icon={<MenuOutlined />} aria-label="打开导航" />
            </Dropdown>
            <div
              className="project-tag-scroll"
              role="group"
              aria-label="切换并排序项目"
              onWheel={scrollProjectTags}
            >
              {orderedProjects.map((item) => {
                const active = item.id === projectId;
                return (
                  <button
                    key={item.id}
                    ref={active ? activeProjectTagRef : undefined}
                    type="button"
                    className={`project-tag${active ? " project-tag-active" : ""}${draggedProjectId === item.id ? " project-tag-dragging" : ""}${dropTargetProjectId === item.id ? " project-tag-drop-target" : ""}`}
                    draggable
                    aria-pressed={active}
                    aria-keyshortcuts="Alt+ArrowLeft Alt+ArrowRight"
                    aria-label={`${item.name}，${item.activeTaskCount} 个活跃任务`}
                    title={`${item.name} · ${item.activeTaskCount} 个活跃任务 · 拖动排序，或按 Alt + 左右方向键调整`}
                    onClick={() => switchProject(item.id)}
                    onDragStart={(event) => startProjectDrag(event, item.id)}
                    onDragOver={(event) => dragOverProject(event, item.id)}
                    onDrop={(event) => dropProject(event, item.id)}
                    onDragEnd={() => {
                      setDraggedProjectId(undefined);
                      setDropTargetProjectId(undefined);
                    }}
                    onKeyDown={(event) => moveProjectWithKeyboard(event, item.id)}
                  >
                    <span className="project-tag-label">{item.name}</span>
                    <span className="project-tag-count" aria-hidden="true">{item.activeTaskCount}</span>
                  </button>
                );
              })}
            </div>
            <Tooltip title="添加项目">
              <Button icon={<FolderAddOutlined />} onClick={() => setAddOpen(true)} aria-label="添加项目" />
            </Tooltip>
          </div>
          <Space size={12} className="header-status">
            <Tooltip title={polling.isError ? (polling.error as Error)?.message : undefined}>
              <Badge
                status={!polling.visible ? "default" : polling.isError ? "error" : polling.isFetching ? "processing" : "success"}
                text={syncLabel}
              />
            </Tooltip>
            <Tooltip title="刷新当前项目">
              <Button
                type="text"
                icon={<ReloadOutlined spin={polling.isFetching} />}
                onClick={() => void refreshProject()}
                aria-label="刷新当前项目"
              />
            </Tooltip>
            <ThemeToggle />
          </Space>
        </Header>

        {project.indexError && (
          <div className="index-error">
            <Badge status="error" />
            <Typography.Text>最近一次索引失败：{project.indexError}</Typography.Text>
          </div>
        )}

        <Content className="app-content">
          <Outlet context={{ project }} />
        </Content>
      </Layout>
      <AddProjectModal
        open={addOpen}
        onCancel={() => setAddOpen(false)}
        onCreated={(created) => navigate(`/projects/${created.id}`)}
      />
    </Layout>
  );
}
