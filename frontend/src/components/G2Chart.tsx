import { useEffect, useRef, useState } from "react";
import { Chart } from "../lib/g2";
import type { G2Spec } from "../lib/g2";

interface G2ChartProps {
  ariaLabel: string;
  buildOptions: () => G2Spec;
  className?: string;
  height: number;
}

export function chartCssColor(variable: string, fallback: string): string {
  if (typeof window === "undefined") return fallback;
  return getComputedStyle(document.documentElement).getPropertyValue(variable).trim() || fallback;
}

/**
 * React 只管理 G2 的宿主节点；图表实例在挂载时创建、数据/主题变化时更新、卸载时销毁。
 * G2 的 autoFit 内部使用 ResizeObserver，容器宽度变化时无需再创建第二个监听器。
 */
export function G2Chart({ ariaLabel, buildOptions, className, height }: G2ChartProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const chartRef = useRef<InstanceType<typeof Chart> | null>(null);
  const [themeRevision, setThemeRevision] = useState(0);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return undefined;

    const chart = new Chart({ container, autoFit: true, height });
    chartRef.current = chart;
    return () => {
      chart.destroy();
      if (chartRef.current === chart) chartRef.current = null;
    };
  }, [height]);

  useEffect(() => {
    const observer = new MutationObserver(() => setThemeRevision((revision) => revision + 1));
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["data-theme", "data-theme-style"],
    });
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    const chart = chartRef.current;
    if (!chart) return;
    chart.options(buildOptions());
    void chart.render();
  }, [buildOptions, height, themeRevision]);

  return <div ref={containerRef} className={className} role="img" aria-label={ariaLabel} />;
}
