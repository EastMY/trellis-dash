import { DesktopOutlined, MoonOutlined, SunOutlined } from "@ant-design/icons";
import { Button, Tooltip } from "antd";
import { nextThemeMode, themeModeLabel } from "../lib/theme";
import type { ThemeMode } from "../lib/theme";
import { useAppStore } from "../store/app";

const themeIcons: Record<ThemeMode, React.ReactNode> = {
  system: <DesktopOutlined />,
  light: <SunOutlined />,
  dark: <MoonOutlined />,
};

interface ThemeToggleProps {
  className?: string;
}

export function ThemeToggle({ className }: ThemeToggleProps) {
  const themeMode = useAppStore((state) => state.themeMode);
  const setThemeMode = useAppStore((state) => state.setThemeMode);
  const nextMode = nextThemeMode(themeMode);
  const currentLabel = themeModeLabel[themeMode];
  const nextLabel = themeModeLabel[nextMode];

  return (
    <Tooltip title={`当前：${currentLabel}；点击切换至${nextLabel}`}>
      <Button
        type="text"
        className={className}
        icon={themeIcons[themeMode]}
        aria-label={`主题：${currentLabel}，点击切换至${nextLabel}`}
        onClick={() => setThemeMode(nextMode)}
      />
    </Tooltip>
  );
}
