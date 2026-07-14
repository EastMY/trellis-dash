import { describe, expect, it } from "vitest";
import { nextThemeMode, resolveThemeMode } from "./theme";

describe("主题规则", () => {
  it("按跟随系统、浅色、深色的顺序循环", () => {
    expect(nextThemeMode("system")).toBe("light");
    expect(nextThemeMode("light")).toBe("dark");
    expect(nextThemeMode("dark")).toBe("system");
  });

  it("仅在跟随系统时读取系统配色", () => {
    expect(resolveThemeMode("system", true)).toBe("dark");
    expect(resolveThemeMode("system", false)).toBe("light");
    expect(resolveThemeMode("light", true)).toBe("light");
    expect(resolveThemeMode("dark", false)).toBe("dark");
  });
});
