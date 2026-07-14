import { create } from "zustand";
import { persist } from "zustand/middleware";
import type { ThemeMode } from "../lib/theme";

type Density = "default" | "compact";

interface AppState {
  currentProjectId?: string;
  density: Density;
  themeMode: ThemeMode;
  setCurrentProjectId: (projectId?: string) => void;
  setDensity: (density: Density) => void;
  setThemeMode: (themeMode: ThemeMode) => void;
}

export const useAppStore = create<AppState>()(
  persist(
    (set) => ({
      density: "compact",
      themeMode: "system",
      setCurrentProjectId: (currentProjectId) => set({ currentProjectId }),
      setDensity: (density) => set({ density }),
      setThemeMode: (themeMode) => set({ themeMode }),
    }),
    {
      name: "trellis-dashboard-preferences",
      partialize: ({ currentProjectId, density, themeMode }) => ({
        currentProjectId,
        density,
        themeMode,
      }),
    },
  ),
);
