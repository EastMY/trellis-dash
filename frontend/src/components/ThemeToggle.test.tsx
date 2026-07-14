import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import { useAppStore } from "../store/app";
import { ThemeToggle } from "./ThemeToggle";

describe("ThemeToggle", () => {
  beforeEach(() => {
    useAppStore.setState({ themeMode: "system" });
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
});
