import { useCallback, useMemo } from "react";
import type { CodexUsageItem, CodexUsageMetric } from "../types";
import type { G2Spec } from "../lib/g2";
import { chartCssColor, G2Chart } from "./G2Chart";

const COST_SERIES = [
  {
    field: "uncachedInputUsd",
    label: "非缓存输入费用",
    cssVariable: "--chart-cost-uncached-input",
    fallback: "#327a52",
    swatchClassName: "codex-usage-cost-swatch--uncached-input",
  },
  {
    field: "cachedInputUsd",
    label: "缓存输入费用",
    cssVariable: "--chart-cost-cached-input",
    fallback: "#3b6f9f",
    swatchClassName: "codex-usage-cost-swatch--cached-input",
  },
  {
    field: "outputUsd",
    label: "输出费用",
    cssVariable: "--chart-cost-output",
    fallback: "#95631f",
    swatchClassName: "codex-usage-cost-swatch--output",
  },
  {
    field: "cacheWriteUsd",
    label: "缓存写入费用",
    cssVariable: "--chart-cost-cache-write",
    fallback: "#7e4f8f",
    swatchClassName: "codex-usage-cost-swatch--cache-write",
  },
] as const;

const COST_TOOLTIP_ORDER = [...COST_SERIES.map((series) => series.label), "总费用"];

interface CodexUsageBarChartProps {
  items: CodexUsageItem[];
  metric: CodexUsageMetric;
  compact?: boolean;
}

function shortDate(date: string): string {
  return date.slice(8, 10);
}

export function formatCodexUsageValue(metric: CodexUsageMetric, value: number): string {
  return metric === "tokens" ? `${value.toLocaleString()} Token` : `$${value.toFixed(6)}`;
}

export function CodexUsageBarChart({ items, metric, compact = false }: CodexUsageBarChartProps) {
  const metricLabel = metric === "tokens" ? "Token" : "费用";
  const rows = useMemo(() => metric === "tokens"
    ? items.map((item) => ({ date: item.date, value: item.tokens, category: "Token" }))
    : items.flatMap((item) => COST_SERIES.map((series) => ({
        date: item.date,
        value: item.costBreakdown[series.field],
        totalValue: item.costUsd,
        category: series.label,
      }))), [items, metric]);
  const buildOptions = useCallback((): G2Spec => {
    const accent = chartCssColor("--accent", "#327a52");
    const primaryText = chartCssColor("--text", "#1d2720");
    const text = chartCssColor("--text-tertiary", "#7b887e");
    const grid = chartCssColor("--chart-grid", "#d8e0da");
    const costColors = COST_SERIES.map((series) => (
      chartCssColor(series.cssVariable, series.fallback)
    ));
    const colorDomain = metric === "tokens" ? ["Token"] : COST_SERIES.map((series) => series.label);
    return {
      type: "interval",
      data: rows,
      paddingTop: compact ? 4 : 12,
      paddingRight: compact ? 0 : 16,
      paddingBottom: compact ? 18 : 34,
      paddingLeft: compact ? 0 : 52,
      encode: { x: "date", y: "value", color: "category" },
      transform: metric === "cost" ? [{ type: "stackY" }] : undefined,
      scale: {
        x: { padding: compact ? 0.05 : 0.18 },
        y: { domainMin: 0, nice: true },
        color: { domain: colorDomain, range: metric === "tokens" ? [accent] : costColors },
      },
      axis: compact
        ? {
            x: { title: false, labelFormatter: shortDate, labelFill: text, labelFontSize: 10, tick: false },
            y: false,
          }
        : {
            x: { title: false, labelFormatter: shortDate, labelFill: text, tick: false },
            y: { title: false, labelFill: text, gridStroke: grid, tick: false },
          },
      legend: { color: false },
      style: { radiusTopLeft: 3, radiusTopRight: 3 },
      tooltip: {
        title: "date",
        items: metric === "cost"
          ? [
              (datum: { category: string; value: number }) => ({
                name: datum.category,
                value: formatCodexUsageValue(metric, datum.value),
              }),
              (datum: { totalValue: number }) => ({
                name: "总费用",
                value: formatCodexUsageValue(metric, datum.totalValue),
                color: primaryText,
              }),
            ]
          : [{ field: "value", name: metricLabel, valueFormatter: (value: number) => formatCodexUsageValue(metric, value) }],
      },
      interaction: metric === "cost"
        ? {
            tooltip: {
              shared: true,
              // 共享 Tooltip 会聚合四个堆叠段；固定排序让总费用稳定显示在末尾。
              sort: ({ name }: { name?: string }) => COST_TOOLTIP_ORDER.indexOf(name ?? ""),
            },
          }
        : undefined,
      animate: false,
    };
  }, [compact, metric, metricLabel, rows]);

  return (
    <div className={`codex-usage-chart-wrap${compact ? " codex-usage-chart-wrap--compact" : ""}`}>
      {metric === "cost" && (
        <ul className="codex-usage-cost-legend" aria-label="费用分类图例">
          {COST_SERIES.map((series) => (
            <li key={series.field}>
              <span
                className={`codex-usage-cost-swatch ${series.swatchClassName}`}
                aria-hidden="true"
              />
              {series.label}
            </li>
          ))}
        </ul>
      )}
      <G2Chart
        className="codex-usage-chart"
        height={compact ? 104 : 278}
        ariaLabel={`Codex 每日${metricLabel}${metric === "cost" ? "堆叠" : ""}柱状图`}
        buildOptions={buildOptions}
      />
      <ul className="visually-hidden" aria-label={`Codex 每日${metricLabel}明细`}>
        {items.map((item) => {
          const value = metric === "tokens" ? item.tokens : item.costUsd;
          const partial = metric === "cost" && item.costPartial ? "，部分未计价" : "";
          if (metric === "tokens") {
            return <li key={item.date}>{item.date}：{formatCodexUsageValue(metric, value)}</li>;
          }
          const breakdown = COST_SERIES.map((series) => (
            `${series.label} ${formatCodexUsageValue(metric, item.costBreakdown[series.field])}`
          )).join("，");
          return (
            <li key={item.date}>
              {item.date}：{formatCodexUsageValue(metric, value)}（{breakdown}）{partial}
            </li>
          );
        })}
      </ul>
      {items.length === 0 && <span className="codex-usage-empty-label">暂无每日数据</span>}
    </div>
  );
}
