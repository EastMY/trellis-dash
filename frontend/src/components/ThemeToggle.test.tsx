import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import { useAppStore } from "../store/app";
import { ThemeToggle } from "./ThemeToggle";

describe("ThemeToggle", () => {
  beforeEach(() => {
    cleanup();
    useAppStore.setState({ themeMode: "system", themeStyle: "classic" });
  });

  it("按约定顺序切换并写入全局偏好", () => {
    render(<ThemeToggle />);

    fireEvent.click(screen.getByRole("button", { name: /主题：跟随系统/ }));
    expect(useAppStore.getState().themeMode).toBe("light");
    expect(JSON.parse(localStorage.getItem("trellis-dashboard-preferences") ?? "{}").state.themeMode).toBe("light");

    fireEvent.click(screen.getByRole("button", { name: /主题：浅色模式/ }));
    expect(useAppStore.getState().themeMode).toBe("dark");

    fireEvent.click(screen.getByRole("button", { name: /主题：深色模式/ }));
    expect(useAppStore.getState().themeMode).toBe("system");
  });

  it("风格切换与明暗模式互不影响", () => {
    render(<ThemeToggle />);

    fireEvent.click(screen.getByRole("button", { name: /界面风格：经典风格/ }));
    expect(useAppStore.getState().themeStyle).toBe("illustration");
    expect(useAppStore.getState().themeMode).toBe("system");
    expect(JSON.parse(localStorage.getItem("trellis-dashboard-preferences") ?? "{}").state.themeStyle).toBe("illustration");

    fireEvent.click(screen.getByRole("button", { name: /界面风格：插画风格/ }));
    expect(useAppStore.getState().themeStyle).toBe("classic");
  });
});
