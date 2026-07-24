export type ThemeMode = "system" | "light" | "dark";
export type ResolvedTheme = Exclude<ThemeMode, "system">;
export type ThemeStyle = "classic" | "illustration";

const themeSequence: readonly ThemeMode[] = ["system", "light", "dark"];
const themeStyleSequence: readonly ThemeStyle[] = ["classic", "illustration"];

export const themeModeLabel: Record<ThemeMode, string> = {
  system: "跟随系统",
  light: "浅色模式",
  dark: "深色模式",
};

export const themeStyleLabel: Record<ThemeStyle, string> = {
  classic: "经典风格",
  illustration: "插画风格",
};

/** 按已约定顺序循环主题，确保用户可以随时回到跟随系统。 */
export function nextThemeMode(mode: ThemeMode): ThemeMode {
  const currentIndex = themeSequence.indexOf(mode);
  return themeSequence[(currentIndex + 1) % themeSequence.length];
}

/** 在经典与插画两套视觉风格之间切换，风格与明暗模式相互独立。 */
export function nextThemeStyle(style: ThemeStyle): ThemeStyle {
  const currentIndex = themeStyleSequence.indexOf(style);
  return themeStyleSequence[(currentIndex + 1) % themeStyleSequence.length];
}

/** 将用户偏好与系统配色合并为页面最终使用的主题。 */
export function resolveThemeMode(mode: ThemeMode, systemPrefersDark: boolean): ResolvedTheme {
  if (mode === "system") return systemPrefersDark ? "dark" : "light";
  return mode;
}
