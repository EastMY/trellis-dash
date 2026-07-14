import {
  DatabaseOutlined,
  DisconnectOutlined,
  FolderOpenOutlined,
  ReloadOutlined,
} from "@ant-design/icons";
import { Alert, Button, Empty, Skeleton, Space, Typography } from "antd";
import type { ReactNode } from "react";

interface ErrorStateProps {
  error: unknown;
  onRetry?: () => void;
  compact?: boolean;
}

export function ErrorState({ error, onRetry, compact = false }: ErrorStateProps) {
  const message = error instanceof Error ? error.message : "暂时无法读取数据";
  if (compact) {
    return (
      <Alert
        type="error"
        showIcon
        message="加载失败"
        description={message}
        action={onRetry ? <Button onClick={onRetry}>重试</Button> : undefined}
      />
    );
  }
  return (
    <div className="page-state page-state-error">
      <DisconnectOutlined className="page-state-icon" />
      <Typography.Title level={4}>服务暂时不可用</Typography.Title>
      <Typography.Paragraph type="secondary">{message}</Typography.Paragraph>
      {onRetry && (
        <Button icon={<ReloadOutlined />} onClick={onRetry}>
          重新加载
        </Button>
      )}
    </div>
  );
}

interface EmptyStateProps {
  title: string;
  description?: string;
  action?: ReactNode;
  kind?: "project" | "task" | "data";
}

export function EmptyState({ title, description, action, kind = "data" }: EmptyStateProps) {
  const icon: ReactNode =
    kind === "project" ? (
      <FolderOpenOutlined />
    ) : kind === "task" ? (
      <DatabaseOutlined />
    ) : Empty.PRESENTED_IMAGE_SIMPLE;
  return (
    <Empty
      className="page-empty"
      image={icon}
      imageStyle={kind === "data" ? undefined : { height: 42, fontSize: 40 }}
      description={
        <Space direction="vertical" size={4}>
          <Typography.Text strong>{title}</Typography.Text>
          {description && <Typography.Text type="secondary">{description}</Typography.Text>}
        </Space>
      }
    >
      {action}
    </Empty>
  );
}

export function PageSkeleton({ rows = 5 }: { rows?: number }) {
  return (
    <div className="page-skeleton" aria-label="正在加载">
      <Skeleton.Input active size="small" style={{ width: 210 }} />
      <div className="skeleton-metrics">
        {Array.from({ length: 4 }, (_, index) => (
          <Skeleton.Node active key={index} className="skeleton-metric" />
        ))}
      </div>
      <Skeleton active paragraph={{ rows }} title={false} />
    </div>
  );
}
