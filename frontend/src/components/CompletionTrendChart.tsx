import { WarningOutlined } from "@ant-design/icons";
import { Tag, Typography } from "antd";
import { useMemo } from "react";
import { buildCompletionTrend } from "../lib/completionTrend";
import type { DailyCount } from "../types";

const CHART_WIDTH = 860;
const CHART_HEIGHT = 112;
const PADDING = { top: 7, right: 18, bottom: 24, left: 42 };

interface CompletionTrendChartProps {
  completionItems: DailyCount[];
  gitItems: DailyCount[];
  gitAvailable: boolean;
}

function formatDateLabel(date: string): string {
  return `${Number(date.slice(5, 7))}/${Number(date.slice(8, 10))}`;
}

function monotoneTangents(values: number[]): number[] {
  if (values.length <= 1) return values.map(() => 0);
  const deltas = values.slice(1).map((value, index) => value - values[index]);
  const tangents = values.map((_, index) => {
    if (index === 0) return deltas[0];
    if (index === values.length - 1) return deltas[deltas.length - 1];
    const previous = deltas[index - 1];
    const next = deltas[index];
    // 峰谷处切线归零；同方向区间取调和平均，避免平滑后越过原始极值。
    if (previous === 0 || next === 0 || Math.sign(previous) !== Math.sign(next)) return 0;
    return (2 * previous * next) / (previous + next);
  });

  deltas.forEach((delta, index) => {
    if (delta === 0) {
      tangents[index] = 0;
      tangents[index + 1] = 0;
      return;
    }
    const alpha = tangents[index] / delta;
    const beta = tangents[index + 1] / delta;
    const magnitude = Math.hypot(alpha, beta);
    if (magnitude > 3) {
      const scale = 3 / magnitude;
      tangents[index] = scale * alpha * delta;
      tangents[index + 1] = scale * beta * delta;
    }
  });
  return tangents;
}

function smoothLinePath(values: number[], maxCount: number): string {
  if (!values.length) return "";
  const plotWidth = CHART_WIDTH - PADDING.left - PADDING.right;
  const plotHeight = CHART_HEIGHT - PADDING.top - PADDING.bottom;
  const xStep = values.length <= 1 ? 0 : plotWidth / (values.length - 1);
  const yScale = plotHeight / Math.max(1, maxCount);
  const points = values.map((value, index) => ({
    x: PADDING.left + index * xStep,
    y: PADDING.top + plotHeight - value * yScale,
  }));
  if (points.length === 1) return `M ${points[0].x.toFixed(2)} ${points[0].y.toFixed(2)}`;

  const tangents = monotoneTangents(values);
  let path = `M ${points[0].x.toFixed(2)} ${points[0].y.toFixed(2)}`;
  for (let index = 0; index < points.length - 1; index += 1) {
    const current = points[index];
    const next = points[index + 1];
    const controlX1 = current.x + xStep / 3;
    const controlY1 = current.y - (tangents[index] * yScale) / 3;
    const controlX2 = next.x - xStep / 3;
    const controlY2 = next.y + (tangents[index + 1] * yScale) / 3;
    path += ` C ${controlX1.toFixed(2)} ${controlY1.toFixed(2)}, ${controlX2.toFixed(2)} ${controlY2.toFixed(2)}, ${next.x.toFixed(2)} ${next.y.toFixed(2)}`;
  }
  return path;
}

function axisMaximum(maxCount: number): number {
  // 纵轴固定四等分并保持整数刻度，避免低计数场景出现重复标签。
  return Math.max(4, Math.ceil(maxCount / 4) * 4);
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
  const plotWidth = CHART_WIDTH - PADDING.left - PADDING.right;
  const plotHeight = CHART_HEIGHT - PADDING.top - PADDING.bottom;
  const axisMax = axisMaximum(trend.maxCount);
  const labelIndexes = useMemo(() => {
    if (!trend.data.length) return [];
    return [...new Set([0, 22, 44, 66, trend.data.length - 1].filter((index) => index < trend.data.length))];
  }, [trend.data.length]);

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

      <div className="completion-trend-scroll">
        <svg
          className="completion-trend-chart"
          viewBox={`0 0 ${CHART_WIDTH} ${CHART_HEIGHT}`}
          role="img"
          aria-label="最近 90 天每日完成任务与 Git 提交曲线"
        >
          {[0, 1, 2, 3, 4].map((step) => {
            const y = PADDING.top + (step / 4) * plotHeight;
            const value = Math.round(axisMax * (1 - step / 4));
            return (
              <g key={step}>
                <line className="trend-grid-line" x1={PADDING.left} x2={PADDING.left + plotWidth} y1={y} y2={y} />
                <text className="trend-axis-label" x={PADDING.left - 9} y={y + 4} textAnchor="end">{value}</text>
              </g>
            );
          })}

          {labelIndexes.map((index) => {
            const x = PADDING.left + (trend.data.length <= 1 ? 0 : (index / (trend.data.length - 1)) * plotWidth);
            return (
              <text key={trend.data[index].date} className="trend-axis-label" x={x} y={CHART_HEIGHT - 10} textAnchor="middle">
                {formatDateLabel(trend.data[index].date)}
              </text>
            );
          })}

          {trend.data.length > 0 && (
            <>
              <path
                className="trend-line trend-line-completion"
                d={smoothLinePath(trend.data.map((item) => item.completionCount), axisMax)}
              />
              {gitAvailable && (
                <path
                  className="trend-line trend-line-git"
                  d={smoothLinePath(trend.data.map((item) => item.gitCommitCount), axisMax)}
                />
              )}
              {trend.data.map((item, index) => {
                const x = PADDING.left + (trend.data.length <= 1 ? 0 : (index / (trend.data.length - 1)) * plotWidth);
                return (
                  <g key={item.date}>
                    <circle
                      className="trend-hit-area"
                      cx={x}
                      cy={PADDING.top + plotHeight - (item.completionCount / axisMax) * plotHeight}
                      r="7"
                      tabIndex={0}
                      aria-label={`${item.date}，完成任务 ${item.completionCount} 个`}
                    >
                      <title>{`${item.date}：完成任务 ${item.completionCount} 个`}</title>
                    </circle>
                    {gitAvailable && (
                      <circle
                        className="trend-hit-area"
                        cx={x}
                        cy={PADDING.top + plotHeight - (item.gitCommitCount / axisMax) * plotHeight}
                        r="7"
                        tabIndex={0}
                        aria-label={`${item.date}，Git 提交 ${item.gitCommitCount} 次`}
                      >
                        <title>{`${item.date}：Git 提交 ${item.gitCommitCount} 次`}</title>
                      </circle>
                    )}
                  </g>
                );
              })}
            </>
          )}
        </svg>
      </div>
    </section>
  );
}
