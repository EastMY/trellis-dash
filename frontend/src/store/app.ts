import { create } from "zustand";
import { persist } from "zustand/middleware";
import type { ThemeMode } from "../lib/theme";

type Density = "default" | "compact";

interface AppState {
  currentProjectId?: string;
  density: Density;
  themeMode: ThemeMode;
  projectOrder: string[];
  setCurrentProjectId: (projectId?: string) => void;
  setDensity: (density: Density) => void;
  setThemeMode: (themeMode: ThemeMode) => void;
  setProjectOrder: (projectOrder: string[]) => void;
}

export const useAppStore = create<AppState>()(
  persist(
    (set) => ({
      density: "compact",
      themeMode: "system",
      projectOrder: [],
      setCurrentProjectId: (currentProjectId) => set({ currentProjectId }),
      setDensity: (density) => set({ density }),
      setThemeMode: (themeMode) => set({ themeMode }),
      setProjectOrder: (projectOrder) => set({ projectOrder }),
    }),
    {
      name: "trellis-dashboard-preferences",
      partialize: ({ currentProjectId, density, themeMode, projectOrder }) => ({
        currentProjectId,
        density,
        themeMode,
        projectOrder,
      }),
    },
  ),
);
