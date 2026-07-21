import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

function dependencyName(id: string): string | undefined {
  const normalizedId = id.replaceAll("\\", "/");
  const nodeModulesMarker = "/node_modules/";
  const markerIndex = normalizedId.lastIndexOf(nodeModulesMarker);

  if (markerIndex < 0) {
    return undefined;
  }

  const pathParts = normalizedId.slice(markerIndex + nodeModulesMarker.length).split("/");
  return pathParts[0]?.startsWith("@") && pathParts[1]
    ? `${pathParts[0]}/${pathParts[1]}`
    : pathParts[0];
}

function manualChunks(id: string): string | undefined {
  const packageName = dependencyName(id);

  if (!packageName) {
    return undefined;
  }

  // 只拆稳定的框架边界；Ant Design 与 RC 组件存在双向依赖，交给 Rollup 自动分配。
  if (packageName === "react" || packageName === "react-dom" || packageName === "scheduler") {
    return "react-vendor";
  }
  if (packageName === "react-router" || packageName === "react-router-dom") {
    return "router-vendor";
  }
  if (packageName.startsWith("@tanstack/")) {
    return "query-vendor";
  }
  if (packageName.startsWith("@antv/")) {
    // AntV 各包拥有明确的单向依赖边界；按包拆分可复用 G2 运行时，同时避免单个图表页面形成超大 chunk。
    return `antv-${packageName.slice("@antv/".length).replaceAll("/", "-")}`;
  }

  return undefined;
}

export default defineConfig({
  plugins: [react()],
  build: {
    rollupOptions: {
      output: {
        manualChunks,
      },
    },
  },
  server: {
    port: 5173,
    proxy: {
      "/api": "http://127.0.0.1:7465",
      "/healthz": "http://127.0.0.1:7465",
      "/readyz": "http://127.0.0.1:7465",
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: "./src/test/setup.ts",
    css: true,
  },
});
