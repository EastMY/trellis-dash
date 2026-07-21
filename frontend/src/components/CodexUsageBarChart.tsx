import { useCallback, useMemo } from "react";
import type { CodexUsageItem, CodexUsageMetric } from "../types";
import type { G2Spec } from "../lib/g2";
import { chartCssColor, G2Chart } from "./G2Chart";

interface CodexUsageBarChartProps {
  items: CodexUsageItem[];
  metric: CodexUsageMetric;
  compact?: boolean;
}

function shortDate(date: string): string {
  return `${Number(date.slice(5, 7))}/${Number(date.slice(8, 10))}`;
}

export function formatCodexUsageValue(metric: CodexUsageMetric, value: number): string {
  return metric === "tokens" ? `${value.toLocaleString()} Token` : `$${value.toFixed(6)}`;
}

export function CodexUsageBarChart({ items, metric, compact = false }: CodexUsageBarChartProps) {
  const metricLabel = metric === "tokens" ? "Token" : "费用";
  const rows = useMemo(() => items.map((item) => ({
    date: item.date,
    value: metric === "tokens" ? item.tokens : item.costUsd,
    priceState: metric === "cost" && item.costPartial ? "部分未计价" : "已计价",
  })), [items, metric]);
  const buildOptions = useCallback((): G2Spec => {
    const accent = chartCssColor("--accent", "#327a52");
    const warning = chartCssColor("--warning", "#a46d22");
    const text = chartCssColor("--text-tertiary", "#7b887e");
    const grid = chartCssColor("--chart-grid", "#d8e0da");
    return {
      type: "interval",
      data: rows,
      paddingTop: compact ? 4 : 12,
      paddingRight: compact ? 2 : 16,
      paddingBottom: compact ? 2 : 34,
      paddingLeft: compact ? 2 : 52,
      encode: { x: "date", y: "value", color: "priceState" },
      scale: {
        x: { padding: 0.18 },
        y: { domainMin: 0, nice: true },
        color: { domain: ["已计价", "部分未计价"], range: [accent, warning] },
      },
      axis: compact
        ? { x: false, y: false }
        : {
            x: { title: false, labelFormatter: shortDate, labelFill: text, tick: false },
            y: { title: false, labelFill: text, gridStroke: grid, tick: false },
          },
      legend: { color: false },
      style: { radiusTopLeft: 3, radiusTopRight: 3 },
      tooltip: {
        title: "date",
        items: [{ field: "value", name: metricLabel, valueFormatter: (value: number) => formatCodexUsageValue(metric, value) }],
      },
      animate: false,
    };
  }, [compact, metric, metricLabel, rows]);

  return (
    <div className={`codex-usage-chart-wrap${compact ? " codex-usage-chart-wrap--compact" : ""}`}>
      <G2Chart
        className="codex-usage-chart"
        height={compact ? 92 : 278}
        ariaLabel={`Codex 每日${metricLabel}柱状图`}
        buildOptions={buildOptions}
      />
      <ul className="visually-hidden" aria-label={`Codex 每日${metricLabel}明细`}>
        {items.map((item) => {
          const value = metric === "tokens" ? item.tokens : item.costUsd;
          const partial = metric === "cost" && item.costPartial ? "，部分未计价" : "";
          return <li key={item.date}>{item.date}：{formatCodexUsageValue(metric, value)}{partial}</li>;
        })}
      </ul>
      {items.length === 0 && <span className="codex-usage-empty-label">暂无每日数据</span>}
    </div>
  );
}
