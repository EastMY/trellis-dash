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

function formatDayLabel(date: string): string {
  return date.slice(8, 10);
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
    return {
      type: "view",
      data: series,
      paddingTop: 8,
      paddingRight: 0,
      paddingBottom: 16,
      paddingLeft: 0,
      scale: {
        // 移除点比例尺默认的首尾半格留白，让 90 天趋势充分利用横向空间。
        x: { type: "point", padding: 0 },
        y: { domainMin: 0, nice: true, tickCount: 5 },
        color: { domain: ["完成任务", "Git 提交"], range: [accent, warning] },
      },
      axis: {
        x: { title: false, tickCount: 5, labelFormatter: formatDayLabel, labelFill: text, tick: false },
        // 折线高低已经足够表达趋势，隐藏 Y 轴把水平空间留给 90 天数据。
        y: false,
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
        <Typography.Title level={4}>90 天项目趋势</Typography.Title>
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
          height={126}
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
