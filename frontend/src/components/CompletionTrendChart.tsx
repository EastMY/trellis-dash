import { WarningOutlined } from "@ant-design/icons";
import { Tag, Typography } from "antd";
import { useCallback, useMemo } from "react";
import { buildCompletionTrend } from "../lib/completionTrend";
import type { G2Spec } from "../lib/g2";
import type { DailyCount } from "../types";
import { chartCssColor, G2Chart } from "./G2Chart";

interface CompletionTrendChartProps {
  completionItems: DailyCount[];
  gitItems: DailyCount[];
  gitAvailable: boolean;
}

function formatDateLabel(date: string): string {
  return `${Number(date.slice(5, 7))}/${Number(date.slice(8, 10))}`;
}

export function CompletionTrendChart({
  completionItems,
  gitItems,
  gitAvailable,
}: CompletionTrendChartProps) {
  const trend = useMemo(
    () => buildCompletionTrend(completionItems, gitAvailable ? gitItems : []),
    [completionItems, gitAvailable, gitItems],
  );
  const series = useMemo(
    () => trend.data.flatMap((item) => [
      { date: item.date, count: item.completionCount, series: "完成任务" },
      ...(gitAvailable ? [{ date: item.date, count: item.gitCommitCount, series: "Git 提交" }] : []),
    ]),
    [gitAvailable, trend.data],
  );
  const buildOptions = useCallback((): G2Spec => {
    const accent = chartCssColor("--accent", "#327a52");
    const warning = chartCssColor("--warning", "#a46d22");
    const text = chartCssColor("--text-tertiary", "#7b887e");
    const grid = chartCssColor("--chart-grid", "#d8e0da");
    return {
      type: "view",
      data: series,
      paddingTop: 8,
      paddingRight: 18,
      paddingBottom: 26,
      paddingLeft: 42,
      scale: {
        x: { type: "point" },
        y: { domainMin: 0, nice: true, tickCount: 5 },
        color: { domain: ["完成任务", "Git 提交"], range: [accent, warning] },
      },
      axis: {
        x: { title: false, tickCount: 5, labelFormatter: formatDateLabel, labelFill: text, tick: false },
        y: { title: false, tickCount: 5, labelFill: text, gridStroke: grid, tick: false },
      },
      legend: { color: false },
      interaction: { tooltip: { shared: true } },
      children: [
        {
          type: "line",
          encode: { x: "date", y: "count", color: "series" },
          style: { lineWidth: 2 },
          animate: false,
        },
        {
          type: "point",
          encode: { x: "date", y: "count", color: "series" },
          style: { r: 2.5 },
          tooltip: { title: "date", items: [{ field: "count" }] },
          animate: false,
        },
      ],
    };
  }, [series]);

  return (
    <section
      className="section-panel completion-trend"
      aria-label={`最近 90 天趋势，完成 ${trend.completionTotal} 个任务${gitAvailable ? `，Git 提交 ${trend.gitCommitTotal} 次` : "，Git 数据暂不可用"}`}
    >
      <div className="completion-trend-heading">
        <div>
          <Typography.Title level={4}>90 天项目趋势</Typography.Title>
          <Typography.Text type="secondary">每日完成任务数与当前 HEAD 可达的 Git 提交数</Typography.Text>
        </div>
        <div className="completion-trend-legend" aria-label="图例">
          <span><i className="trend-swatch trend-swatch-completion" />完成任务 <strong>{trend.completionTotal}</strong></span>
          {gitAvailable ? (
            <span><i className="trend-swatch trend-swatch-git" />Git 提交 <strong>{trend.gitCommitTotal}</strong></span>
          ) : (
            <Tag icon={<WarningOutlined />} color="warning">Git 数据暂不可用</Tag>
          )}
        </div>
      </div>

      <div className="completion-trend-chart-wrap">
        <G2Chart
          className="completion-trend-chart"
          height={112}
          ariaLabel="最近 90 天每日完成任务与 Git 提交折线图"
          buildOptions={buildOptions}
        />
        <ul className="visually-hidden" aria-label="最近 90 天项目趋势每日明细">
          {trend.data.flatMap((item) => [
            <li key={`${item.date}-completion`} aria-label={`${item.date}，完成任务 ${item.completionCount} 个`}>
              {item.date}：完成任务 {item.completionCount} 个
            </li>,
            ...(gitAvailable ? [
              <li key={`${item.date}-git`} aria-label={`${item.date}，Git 提交 ${item.gitCommitCount} 次`}>
                {item.date}：Git 提交 {item.gitCommitCount} 次
              </li>,
            ] : []),
          ])}
        </ul>
      </div>
    </section>
  );
}
