import {
  DeleteOutlined,
  EyeOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
} from "@ant-design/icons";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { App, Button, Descriptions, Radio, Space, Tag, Typography } from "antd";
import { useNavigate } from "react-router-dom";
import { api } from "../api/client";
import { useProjectContext } from "../components/AppShell";
import { PageHeader } from "../components/PageHeader";
import { fullDate } from "../lib/format";
import { useAppStore } from "../store/app";

export function SettingsPage() {
  const { project } = useProjectContext();
  const { message, modal } = App.useApp();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const density = useAppStore((state) => state.density);
  const setDensity = useAppStore((state) => state.setDensity);

  const rescan = useMutation({
    mutationFn: () => api.rescanProject(project.id),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["project", project.id] }),
        queryClient.invalidateQueries({ queryKey: ["projects"] }),
      ]);
      message.success("已提交全量重新索引");
    },
    onError: (error) => message.error(error instanceof Error ? error.message : "重新索引失败"),
  });
  const remove = useMutation({
    mutationFn: () => api.deleteProject(project.id),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ["projects"] });
      message.success("项目已从 Dashboard 移除，本地文件未受影响");
      navigate("/");
    },
    onError: (error) => message.error(error instanceof Error ? error.message : "移除项目失败"),
  });

  const confirmRemove = () => {
    modal.confirm({
      title: `移除「${project.name}」？`,
      icon: <DeleteOutlined />,
      content: "仅删除 Dashboard 的注册信息与可重建缓存，不会删除项目目录或 Trellis 任务。",
      okText: "确认移除",
      okType: "danger",
      cancelText: "取消",
      onOk: () => remove.mutateAsync(),
    });
  };

  return (
    <div className="page settings-page">
      <PageHeader title="项目设置" description="查看索引边界并执行安全的项目级维护操作" />

      <section className="settings-section">
        <div className="settings-heading">
          <div>
            <Typography.Title level={4}>项目信息</Typography.Title>
            <Typography.Text type="secondary">项目路径由后端执行 realpath、Observer 与数据库边界校验。</Typography.Text>
          </div>
          <Tag variant="filled" icon={<EyeOutlined />}>{project.mode === "observer" ? "Observer" : "Control"}</Tag>
        </div>
        <Descriptions
          bordered
          size="small"
          column={1}
          items={[
            { key: "id", label: "项目 ID", children: <Typography.Text className="mono" copyable>{project.id}</Typography.Text> },
            { key: "name", label: "名称", children: project.name },
            { key: "root", label: "根目录", children: <Typography.Text className="mono" copyable>{project.root}</Typography.Text> },
            { key: "mode", label: "模式", children: project.mode },
            { key: "created", label: "添加时间", children: fullDate(project.createdAt) },
            { key: "indexed", label: "最近索引", children: fullDate(project.indexedAt) },
          ]}
        />
      </section>

      <section className="settings-section">
        <div className="settings-heading">
          <div>
            <Typography.Title level={4}>界面密度</Typography.Title>
            <Typography.Text type="secondary">设置只保存在当前浏览器，不影响其他用户或项目。</Typography.Text>
          </div>
        </div>
        <Radio.Group value={density} onChange={(event) => setDensity(event.target.value as "default" | "compact")}>
          <Radio.Button value="compact">紧凑</Radio.Button>
          <Radio.Button value="default">标准</Radio.Button>
        </Radio.Group>
      </section>

      <section className="settings-section">
        <div className="settings-heading">
          <div>
            <Typography.Title level={4}>索引维护</Typography.Title>
            <Typography.Text type="secondary">全量扫描会从 .trellis 与 Git 重建当前项目的 SQLite 读模型。</Typography.Text>
          </div>
        </div>
        <Button icon={<ReloadOutlined />} loading={rescan.isPending} onClick={() => rescan.mutate()}>
          全量重新索引
        </Button>
      </section>

      <section className="settings-section danger-zone">
        <div className="settings-heading">
          <div>
            <Typography.Title level={4}>移除项目</Typography.Title>
            <Typography.Text type="secondary">Dashboard 不会执行文件删除，本地仓库与 .trellis 数据保持不变。</Typography.Text>
          </div>
          <SafetyCertificateOutlined />
        </div>
        <Space>
          <Button danger icon={<DeleteOutlined />} loading={remove.isPending} onClick={confirmRemove}>从 Dashboard 移除</Button>
        </Space>
      </section>
    </div>
  );
}
