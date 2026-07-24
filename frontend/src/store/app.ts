import { create } from "zustand";
import { persist } from "zustand/middleware";
import type { ThemeMode, ThemeStyle } from "../lib/theme";

type Density = "default" | "compact";

interface AppState {
  currentProjectId?: string;
  density: Density;
  themeMode: ThemeMode;
  themeStyle: ThemeStyle;
  projectOrder: string[];
  setCurrentProjectId: (projectId?: string) => void;
  setDensity: (density: Density) => void;
  setThemeMode: (themeMode: ThemeMode) => void;
  setThemeStyle: (themeStyle: ThemeStyle) => void;
  setProjectOrder: (projectOrder: string[]) => void;
}

export const useAppStore = create<AppState>()(
  persist(
    (set) => ({
      density: "compact",
      themeMode: "system",
      themeStyle: "classic",
      projectOrder: [],
      setCurrentProjectId: (currentProjectId) => set({ currentProjectId }),
      setDensity: (density) => set({ density }),
      setThemeMode: (themeMode) => set({ themeMode }),
      setThemeStyle: (themeStyle) => set({ themeStyle }),
      setProjectOrder: (projectOrder) => set({ projectOrder }),
    }),
    {
      name: "trellis-dashboard-preferences",
      partialize: ({ currentProjectId, density, themeMode, themeStyle, projectOrder }) => ({
        currentProjectId,
        density,
        themeMode,
        themeStyle,
        projectOrder,
      }),
    },
  ),
);
