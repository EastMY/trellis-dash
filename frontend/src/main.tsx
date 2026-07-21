import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { App as AntApp, ConfigProvider, theme } from "antd";
import zhCN from "antd/locale/zh_CN";
import React, { lazy, Suspense } from "react";
import ReactDOM from "react-dom/client";
import { BrowserRouter, Route, Routes } from "react-router-dom";
import type { ThemeConfig } from "antd";
import "antd/dist/reset.css";
import { PageSkeleton } from "./components/PageState";
import { resolveThemeMode } from "./lib/theme";
import { useAppStore } from "./store/app";
import "./styles.css";

const AppShell = lazy(() => import("./components/AppShell").then((module) => ({ default: module.AppShell })));
const ActivityPage = lazy(() => import("./pages/ActivityPage").then((module) => ({ default: module.ActivityPage })));
const CodeGraphPage = lazy(() => import("./pages/CodeGraphPage").then((module) => ({ default: module.CodeGraphPage })));
const CodexUsagePage = lazy(() => import("./pages/CodexUsagePage").then((module) => ({ default: module.CodexUsagePage })));
const GitPage = lazy(() => import("./pages/GitPage").then((module) => ({ default: module.GitPage })));
const NotFoundPage = lazy(() => import("./pages/NotFoundPage").then((module) => ({ default: module.NotFoundPage })));
const OverviewPage = lazy(() => import("./pages/OverviewPage").then((module) => ({ default: module.OverviewPage })));
const RootPage = lazy(() => import("./pages/RootPage").then((module) => ({ default: module.RootPage })));
const SessionsPage = lazy(() => import("./pages/SessionsPage").then((module) => ({ default: module.SessionsPage })));
const SettingsPage = lazy(() => import("./pages/SettingsPage").then((module) => ({ default: module.SettingsPage })));
const TaskDetailPage = lazy(() => import("./pages/TaskDetailPage").then((module) => ({ default: module.TaskDetailPage })));
const TasksPage = lazy(() => import("./pages/TasksPage").then((module) => ({ default: module.TasksPage })));

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 1000,
      retry: 2,
      retryDelay: (attempt) => Math.min(1000 * 2 ** attempt, 10_000),
      refetchOnWindowFocus: true,
    },
  },
});

const systemThemeQuery = "(prefers-color-scheme: dark)";

const darkTheme: ThemeConfig = {
  algorithm: theme.darkAlgorithm,
  cssVar: { prefix: "trellis" },
  token: {
    colorPrimary: "#70b892",
    colorInfo: "#70b892",
    colorSuccess: "#70b892",
    colorWarning: "#c9a96e",
    colorError: "#d27474",
    colorBgBase: "#101310",
    colorBgLayout: "#101310",
    colorBgContainer: "#171b18",
    colorBgElevated: "#1c211d",
    colorBorder: "#303831",
    colorBorderSecondary: "#262d27",
    colorText: "#e5e9e5",
    colorTextSecondary: "#929b93",
    colorTextTertiary: "#687169",
    boxShadow: "0 12px 34px rgba(4, 8, 5, 0.28)",
  },
  components: {
    Layout: { headerBg: "#141815", siderBg: "#121613", bodyBg: "#101310" },
    Menu: { darkItemBg: "transparent", itemBg: "transparent", itemSelectedBg: "#223128", itemSelectedColor: "#a5d4b8", itemHoverBg: "#1b211c" },
    Table: { headerBg: "#1b201c", headerColor: "#aeb6af", rowHoverBg: "#1c241e", borderColor: "#293029" },
    Tabs: { itemSelectedColor: "#98c9ac", inkBarColor: "#70b892" },
    Card: { colorBgContainer: "#171b18" },
  },
};

const lightTheme: ThemeConfig = {
  algorithm: theme.defaultAlgorithm,
  cssVar: { prefix: "trellis" },
  token: {
    colorPrimary: "#327a52",
    colorInfo: "#327a52",
    colorSuccess: "#327a52",
    colorWarning: "#a46d22",
    colorError: "#b44f4f",
    colorBgBase: "#f5f7f5",
    colorBgLayout: "#f5f7f5",
    colorBgContainer: "#ffffff",
    colorBgElevated: "#ffffff",
    colorBorder: "#cbd5cd",
    colorBorderSecondary: "#dce3dd",
    colorText: "#1d2720",
    colorTextSecondary: "#5d6b61",
    colorTextTertiary: "#7b887e",
    boxShadow: "0 12px 34px rgba(28, 49, 35, 0.12)",
  },
  components: {
    Layout: { headerBg: "#ffffff", siderBg: "#f8faf8", bodyBg: "#f5f7f5" },
    Menu: { itemBg: "transparent", itemSelectedBg: "#e4f0e8", itemSelectedColor: "#245f3e", itemHoverBg: "#edf3ef" },
    Table: { headerBg: "#f1f5f2", headerColor: "#4f5e53", rowHoverBg: "#f3f8f4", borderColor: "#d8e0da" },
    Tabs: { itemSelectedColor: "#286d47", inkBarColor: "#327a52" },
    Card: { colorBgContainer: "#ffffff" },
  },
};

function useSystemPrefersDark() {
  const [prefersDark, setPrefersDark] = React.useState(() => {
    // 无法读取系统主题时按需求回退到深色模式。
    if (typeof window.matchMedia !== "function") return true;
    return window.matchMedia(systemThemeQuery).matches;
  });

  React.useEffect(() => {
    if (typeof window.matchMedia !== "function") return undefined;
    const media = window.matchMedia(systemThemeQuery);
    const handleChange = (event: MediaQueryListEvent) => setPrefersDark(event.matches);
    setPrefersDark(media.matches);
    media.addEventListener("change", handleChange);
    return () => media.removeEventListener("change", handleChange);
  }, []);

  return prefersDark;
}

function DashboardApp() {
  const density = useAppStore((state) => state.density);
  const themeMode = useAppStore((state) => state.themeMode);
  const systemPrefersDark = useSystemPrefersDark();
  const resolvedTheme = resolveThemeMode(themeMode, systemPrefersDark);

  React.useLayoutEffect(() => {
    // data-theme 同时驱动原生 CSS 变量与浏览器内建控件的配色。
    document.documentElement.dataset.theme = resolvedTheme;
    document.documentElement.style.colorScheme = resolvedTheme;
  }, [resolvedTheme]);

  return (
    <ConfigProvider
      locale={zhCN}
      componentSize={density === "compact" ? "small" : "middle"}
      theme={{
        ...(resolvedTheme === "dark" ? darkTheme : lightTheme),
        token: {
          ...(resolvedTheme === "dark" ? darkTheme.token : lightTheme.token),
          fontFamily: "-apple-system, BlinkMacSystemFont, 'Segoe UI', 'Noto Sans SC', sans-serif",
          fontFamilyCode: "'SFMono-Regular', Consolas, 'Liberation Mono', monospace",
          borderRadius: 7,
          borderRadiusLG: 8,
          controlHeight: 34,
          fontSize: 13,
          lineWidth: 1,
        },
      }}
    >
      <AntApp>
        <BrowserRouter>
          <Suspense fallback={<PageSkeleton rows={8} />}>
            <Routes>
              <Route path="/" element={<RootPage />} />
              <Route path="/projects/:projectId" element={<AppShell />}>
                <Route index element={<OverviewPage />} />
                <Route path="tasks" element={<TasksPage />} />
                <Route path="tasks/archive" element={<TasksPage archived />} />
                <Route path="tasks/:taskKey" element={<TaskDetailPage />} />
                <Route path="sessions" element={<SessionsPage />} />
                <Route path="git" element={<GitPage />} />
                <Route path="codex-usage" element={<CodexUsagePage />} />
                <Route path="activity" element={<ActivityPage />} />
                <Route path="codegraph" element={<CodeGraphPage />} />
                <Route path="settings" element={<SettingsPage />} />
                <Route path="*" element={<NotFoundPage />} />
              </Route>
              <Route path="*" element={<NotFoundPage />} />
            </Routes>
          </Suspense>
        </BrowserRouter>
      </AntApp>
    </ConfigProvider>
  );
}

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <DashboardApp />
    </QueryClientProvider>
  </React.StrictMode>,
);
