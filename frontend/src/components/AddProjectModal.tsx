import { FolderAddOutlined, FolderOpenOutlined } from "@ant-design/icons";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { App, Button, Form, Input, Modal, Radio, Space, Typography } from "antd";
import type { ChangeEvent } from "react";
import { api } from "../api/client";
import type { Project, ProjectInput } from "../types";

interface AddProjectModalProps {
  open: boolean;
  onCancel: () => void;
  onCreated?: (project: Project) => void;
}

interface DirectoryInputProps {
  value?: string;
  onChange?: (event: ChangeEvent<HTMLInputElement>) => void;
  supportsPicker: boolean;
  capabilitiesPending: boolean;
  pickerPending: boolean;
  onSelect: () => void;
}

function DirectoryInput({
  value,
  onChange,
  supportsPicker,
  capabilitiesPending,
  pickerPending,
  onSelect,
}: DirectoryInputProps) {
  return (
    <Space.Compact block>
      <Input
        className="mono"
        value={value}
        onChange={onChange}
        placeholder={supportsPicker ? "请选择项目根目录" : "/Users/name/projects/example"}
        autoComplete="off"
        readOnly={supportsPicker}
        disabled={capabilitiesPending}
      />
      {supportsPicker && (
        <Button
          htmlType="button"
          icon={<FolderOpenOutlined />}
          loading={pickerPending}
          onClick={onSelect}
        >
          选择目录
        </Button>
      )}
    </Space.Compact>
  );
}

export function AddProjectModal({ open, onCancel, onCreated }: AddProjectModalProps) {
  const [form] = Form.useForm<ProjectInput>();
  const { message } = App.useApp();
  const queryClient = useQueryClient();
  const capabilities = useQuery({
    queryKey: ["system-capabilities"],
    queryFn: api.getSystemCapabilities,
    staleTime: Infinity,
    enabled: open,
  });
  const supportsDirectoryPicker = capabilities.data?.directoryPicker === true;
  const pickerMutation = useMutation({
    mutationFn: api.selectDirectory,
    onSuccess: async (path) => {
      // 用户取消选择时后端返回空结果，保留表单中已有路径。
      if (!path) return;
      form.setFieldValue("root", path);
      await form.validateFields(["root"]);
    },
    onError: (error) => message.error(error instanceof Error ? error.message : "打开目录选择器失败"),
  });
  const mutation = useMutation({
    mutationFn: api.createProject,
    onSuccess: async (project) => {
      await queryClient.invalidateQueries({ queryKey: ["projects"] });
      form.resetFields();
      message.success(`已添加项目「${project.name}」`);
      onCreated?.(project);
      onCancel();
    },
    onError: (error) => message.error(error instanceof Error ? error.message : "添加项目失败"),
  });

  return (
    <Modal
      title={<span><FolderAddOutlined /> 添加 Trellis 项目</span>}
      open={open}
      okText="开始索引"
      cancelText="取消"
      confirmLoading={mutation.isPending}
      onCancel={onCancel}
      onOk={() => void form.validateFields().then((values) => mutation.mutate(values))}
      destroyOnHidden
    >
      <Typography.Paragraph type="secondary" className="modal-intro">
        服务默认只读取项目中的 .trellis 与 Git 元数据；仅在你显式点击 Push 时写入远端仓库。
      </Typography.Paragraph>
      <Form<ProjectInput>
        form={form}
        layout="vertical"
        requiredMark={false}
        initialValues={{ mode: "observer" }}
      >
        <Form.Item
          name="name"
          label="项目名称"
          rules={[{ required: true, whitespace: true, message: "请输入项目名称" }]}
        >
          <Input autoFocus placeholder="例如：Android Agent" autoComplete="off" />
        </Form.Item>
        <Form.Item
          name="root"
          label="项目根目录"
          extra={supportsDirectoryPicker
            ? "请选择包含 .trellis 目录的项目根目录。"
            : "请输入包含 .trellis 目录的绝对路径。"}
          rules={[
            { required: true, whitespace: true, message: "请输入项目根目录" },
            { pattern: /^\//, message: "请输入绝对路径" },
          ]}
        >
          <DirectoryInput
            supportsPicker={supportsDirectoryPicker}
            capabilitiesPending={capabilities.isPending}
            pickerPending={pickerMutation.isPending}
            onSelect={() => pickerMutation.mutate()}
          />
        </Form.Item>
        <Form.Item name="mode" label="运行模式">
          <Radio.Group>
            <Radio.Button value="observer">Observer（默认只读）</Radio.Button>
            <Radio.Button value="control" disabled>Control（后续版本）</Radio.Button>
          </Radio.Group>
        </Form.Item>
      </Form>
    </Modal>
  );
}
