export function selectedMenuKey(pathname: string): string {
  if (pathname.includes("/tasks/archive")) return "archive";
  if (pathname.includes("/tasks")) return "tasks";
  if (pathname.includes("/sessions")) return "sessions";
  if (pathname.includes("/git")) return "git";
  if (pathname.includes("/activity")) return "activity";
  if (pathname.includes("/codegraph")) return "codegraph";
  if (pathname.includes("/settings")) return "settings";
  return "overview";
}

/**
 * 切换项目时只保留当前一级功能页，避免把任务详情中的任务 ID 带到另一个项目。
 */
export function projectSectionPath(pathname: string): string {
  const section = selectedMenuKey(pathname);
  const paths: Record<string, string> = {
    overview: "",
    tasks: "/tasks",
    archive: "/tasks/archive",
    sessions: "/sessions",
    git: "/git",
    activity: "/activity",
    codegraph: "/codegraph",
    settings: "/settings",
  };
  return paths[section] ?? "";
}
