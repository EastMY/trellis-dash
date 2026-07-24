import { WarningOutlined } from "@ant-design/icons";
import { Statistic, Tag, Typography } from "antd";
import type { CodexUsageResponse } from "../types";

interface CodexUsageSummaryProps {
  data: CodexUsageResponse;
  compact?: boolean;
}

export function CodexUsageSummary({ data, compact = false }: CodexUsageSummaryProps) {
  // dateTo 是服务端按本机自然日生成的统计截止日，直接匹配可避免浏览器时区换算偏移。
  const todayCostUsd = data.items.find((item) => item.date === data.dateTo)?.costUsd ?? 0;

  return (
    <div
      className={`codex-usage-summary${compact ? " codex-usage-summary--compact" : ""}`}
      aria-label={`Codex 使用汇总，总 Token ${data.totalTokens}，总费用 ${data.totalCostUsd} 美元，今日费用 ${todayCostUsd} 美元`}
    >
      <Statistic title="总 Token" value={data.totalTokens} groupSeparator="," />
      <Statistic
        title="已知模型总费用"
        value={data.totalCostUsd}
        prefix="$"
        precision={compact ? 4 : 6}
      />
      <Statistic
        title="今日费用"
        value={todayCostUsd}
        prefix="$"
        precision={compact ? 4 : 6}
      />
      {!compact && (
        <div className="codex-usage-status">
          <Typography.Text type="secondary">
            {data.sessionCount.toLocaleString()} 个会话 · {data.dateFrom} 至 {data.dateTo}
          </Typography.Text>
          <div className="codex-usage-tags">
            {data.costPartial && (
              <Tag icon={<WarningOutlined />} color="warning">部分费用未计价</Tag>
            )}
            {data.skippedFiles > 0 && (
              <Tag color="warning">已跳过 {data.skippedFiles.toLocaleString()} 个文件</Tag>
            )}
            {data.sessionCount === 0 && <Tag>暂无匹配会话</Tag>}
          </div>
        </div>
      )}
    </div>
  );
}
