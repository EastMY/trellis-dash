import {
  BranchesOutlined,
  ExclamationCircleOutlined,
  FileSyncOutlined,
  FolderOpenOutlined,
} from "@ant-design/icons";
import type { ActivityEvent } from "../types";

// 统一活动图标映射，确保浮动菜单与完整时间线表达一致。
export function activityEventIcon(event: ActivityEvent) {
  if (event.type.includes("error") || event.type.includes("fail")) return <ExclamationCircleOutlined />;
  if (event.type.startsWith("git")) return <BranchesOutlined />;
  if (event.type.startsWith("project")) return <FolderOpenOutlined />;
  return <FileSyncOutlined />;
}
