import { useQuery } from "@tanstack/react-query";
import { Segmented, Spin, Typography } from "antd";
import { useState } from "react";
import { api } from "../api/client";
import { useProjectContext } from "../components/AppShell";
import { CodexUsageBarChart } from "../components/CodexUsageBarChart";
import { CodexUsageSummary } from "../components/CodexUsageSummary";
import { PageHeader } from "../components/PageHeader";
import { EmptyState, ErrorState } from "../components/PageState";
import type { CodexUsageDays, CodexUsageMetric, CodexUsageScope } from "../types";

export function CodexUsagePage() {
  const { project } = useProjectContext();
  const [scope, setScope] = useState<CodexUsageScope>("project");
  const [days, setDays] = useState<CodexUsageDays>(30);
  const [metric, setMetric] = useState<CodexUsageMetric>("tokens");
  const query = useQuery({
    // metric 只改变客户端展示；服务端 Query key 仅包含真正改变响应的筛选条件。
    queryKey: ["project", project.id, "codex-usage", scope, days],
    queryFn: () => api.getCodexUsage(project.id, scope, days),
  });

  return (
    <div className="page codex-usage-page">
      <PageHeader
        title="Codex 使用统计"
        description="按本机自然日汇总 Codex 会话的 Token 与已知模型费用"
      />

      <section className="section-panel codex-usage-filters" aria-label="Codex 统计筛选">
        <label>
          <Typography.Text type="secondary">统计范围</Typography.Text>
          <Segmented
            aria-label="统计范围"
            value={scope}
            options={[
              { label: "当前项目", value: "project" },
              { label: "全部会话", value: "all" },
            ]}
            onChange={(value) => setScope(value as CodexUsageScope)}
          />
        </label>
        <label>
          <Typography.Text type="secondary">时间范围</Typography.Text>
          <Segmented
            aria-label="时间范围"
            value={days}
            options={[
              { label: "最近 7 天", value: 7 },
              { label: "最近 30 天", value: 30 },
              { label: "最近 90 天", value: 90 },
            ]}
            onChange={(value) => setDays(value as CodexUsageDays)}
          />
        </label>
        <label>
          <Typography.Text type="secondary">图表指标</Typography.Text>
          <Segmented
            aria-label="图表指标"
            value={metric}
            options={[
              { label: "Token", value: "tokens" },
              { label: "费用", value: "cost" },
            ]}
            onChange={(value) => setMetric(value as CodexUsageMetric)}
          />
        </label>
      </section>

      {query.isLoading ? (
        <section className="section-panel codex-usage-loading" aria-label="正在加载 Codex 使用统计">
          <Spin />
          <Typography.Text type="secondary">正在汇总 Codex 日志…</Typography.Text>
        </section>
      ) : query.isError || !query.data ? (
        <ErrorState compact error={query.error} onRetry={() => void query.refetch()} />
      ) : (
        <div className="codex-usage-content">
          <section className="section-panel">
            <CodexUsageSummary data={query.data} />
          </section>
          {query.data.sessionCount === 0 ? (
            <EmptyState
              title="所选范围内暂无 Codex 使用记录"
              description="切换统计范围或时间范围后再试。"
            />
          ) : (
            <section className="section-panel codex-usage-chart-panel">
              <div className="section-heading">
                <div>
                  <Typography.Title level={4}>每日{metric === "tokens" ? " Token" : "费用"}</Typography.Title>
                  <Typography.Text type="secondary">{query.data.dateFrom} 至 {query.data.dateTo}</Typography.Text>
                </div>
              </div>
              <CodexUsageBarChart items={query.data.items} metric={metric} />
            </section>
          )}
        </div>
      )}
    </div>
  );
}
