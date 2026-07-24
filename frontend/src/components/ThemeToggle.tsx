import { BgColorsOutlined, DesktopOutlined, FormatPainterOutlined, MoonOutlined, SunOutlined } from "@ant-design/icons";
import { Button, Tooltip } from "antd";
import { nextThemeMode, nextThemeStyle, themeModeLabel, themeStyleLabel } from "../lib/theme";
import type { ThemeMode, ThemeStyle } from "../lib/theme";
import { useAppStore } from "../store/app";

const themeIcons: Record<ThemeMode, React.ReactNode> = {
  system: <DesktopOutlined />,
  light: <SunOutlined />,
  dark: <MoonOutlined />,
};

const themeStyleIcons: Record<ThemeStyle, React.ReactNode> = {
  classic: <BgColorsOutlined />,
  illustration: <FormatPainterOutlined />,
};

interface ThemeToggleProps {
  className?: string;
}

export function ThemeToggle({ className }: ThemeToggleProps) {
  const themeMode = useAppStore((state) => state.themeMode);
  const setThemeMode = useAppStore((state) => state.setThemeMode);
  const themeStyle = useAppStore((state) => state.themeStyle);
  const setThemeStyle = useAppStore((state) => state.setThemeStyle);
  const nextMode = nextThemeMode(themeMode);
  const currentLabel = themeModeLabel[themeMode];
  const nextLabel = themeModeLabel[nextMode];
  const nextStyle = nextThemeStyle(themeStyle);
  const currentStyleLabel = themeStyleLabel[themeStyle];
  const nextStyleLabel = themeStyleLabel[nextStyle];

  return (
    // 两个按钮必须处于同一容器：作为 antd Space 的子节点时 Fragment 会被视为
    // 单个 item，内部按钮得不到 Space 的间距，导致按钮间距不一致。
    <div className={className ? `theme-toggle ${className}` : "theme-toggle"}>
      <Tooltip title={`当前：${currentStyleLabel}；点击切换至${nextStyleLabel}`}>
        <Button
          type="text"
          icon={themeStyleIcons[themeStyle]}
          aria-label={`界面风格：${currentStyleLabel}，点击切换至${nextStyleLabel}`}
          onClick={() => setThemeStyle(nextStyle)}
        />
      </Tooltip>
      <Tooltip title={`当前：${currentLabel}；点击切换至${nextLabel}`}>
        <Button
          type="text"
          icon={themeIcons[themeMode]}
          aria-label={`主题：${currentLabel}，点击切换至${nextLabel}`}
          onClick={() => setThemeMode(nextMode)}
        />
      </Tooltip>
    </div>
  );
}
