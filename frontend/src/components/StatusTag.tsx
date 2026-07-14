import {
  CheckCircleOutlined,
  ClockCircleOutlined,
  CloseCircleOutlined,
  LoadingOutlined,
  PauseCircleOutlined,
  PlayCircleOutlined,
  QuestionCircleOutlined,
} from "@ant-design/icons";
import { Tag } from "antd";
import type { ReactNode } from "react";
import { statusLabel } from "../lib/format";

type StatusTone = "neutral" | "active" | "waiting" | "danger" | "success";

const statusTone: Record<string, StatusTone> = {
  planning: "neutral",
  idle: "neutral",
  in_progress: "active",
  implementing: "active",
  checking: "waiting",
  review: "waiting",
  waiting: "waiting",
  blocked: "danger",
  error: "danger",
  completed: "success",
};

const icons: Record<StatusTone, ReactNode> = {
  neutral: <ClockCircleOutlined />,
  active: <PlayCircleOutlined />,
  waiting: <PauseCircleOutlined />,
  danger: <CloseCircleOutlined />,
  success: <CheckCircleOutlined />,
};

export function StatusTag({ status, live = false }: { status?: string; live?: boolean }) {
  const tone = statusTone[status ?? ""] ?? "neutral";
  return (
    <Tag
      variant="filled"
      className={`status-tag status-${tone}`}
      icon={live && tone === "active" ? <LoadingOutlined spin /> : icons[tone] ?? <QuestionCircleOutlined />}
    >
      {statusLabel(status)}
    </Tag>
  );
}
