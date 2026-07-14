import type { DailyCount } from "../types";

export interface CompletionTrendDatum {
  date: string;
  completionCount: number;
  gitCommitCount: number;
}

export interface CompletionTrendData {
  data: CompletionTrendDatum[];
  completionTotal: number;
  gitCommitTotal: number;
  maxCount: number;
}

function validDate(date: string): boolean {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(date);
  if (!match) return false;
  const value = new Date(Date.UTC(Number(match[1]), Number(match[2]) - 1, Number(match[3])));
  return !Number.isNaN(value.getTime()) && value.toISOString().slice(0, 10) === date;
}

function normalize(items: DailyCount[]): Map<string, number> {
  const values = new Map<string, number>();
  for (const item of items) {
    if (!validDate(item.date)) continue;
    // 防御异常负数或非有限值，避免 SVG 坐标被污染。
    const count = Number.isFinite(item.count) ? Math.max(0, item.count) : 0;
    values.set(item.date, count);
  }
  return values;
}

export function buildCompletionTrend(
  completionItems: DailyCount[],
  gitItems: DailyCount[],
): CompletionTrendData {
  const completions = normalize(completionItems);
  const gitCommits = normalize(gitItems);
  const dates = [...new Set([...completions.keys(), ...gitCommits.keys()])].sort();
  const data = dates.map((date) => ({
    date,
    completionCount: completions.get(date) ?? 0,
    gitCommitCount: gitCommits.get(date) ?? 0,
  }));

  return {
    data,
    completionTotal: data.reduce((sum, item) => sum + item.completionCount, 0),
    gitCommitTotal: data.reduce((sum, item) => sum + item.gitCommitCount, 0),
    maxCount: data.reduce(
      (maximum, item) => Math.max(maximum, item.completionCount, item.gitCommitCount),
      0,
    ),
  };
}
