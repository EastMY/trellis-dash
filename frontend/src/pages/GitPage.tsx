import {
  ArrowDownOutlined,
  ArrowUpOutlined,
  BranchesOutlined,
  CheckCircleOutlined,
  CopyOutlined,
  FileSearchOutlined,
  WarningOutlined,
} from "@ant-design/icons";
import { useQuery } from "@tanstack/react-query";
import {
  Button,
  Descriptions,
  Empty,
  Segmented,
  Space,
  Table,
  Tabs,
  Tag,
  Tooltip,
  Typography,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import { useEffect, useState } from "react";
import { api } from "../api/client";
import { useProjectContext } from "../components/AppShell";
import { PageHeader } from "../components/PageHeader";
import { ErrorState, PageSkeleton } from "../components/PageState";
import { fullDate, relativeDate, shortHash } from "../lib/format";
import type { GitCommit, GitFile, Worktree } from "../types";

function FileStatus({ file }: { file: GitFile }) {
  const status = file.conflict ? "冲突" : file.untracked ? "未跟踪" : file.status || `${file.index}${file.worktree}`;
  return <Tag variant="filled" color={file.conflict ? "error" : undefined}>{status}</Tag>;
}

function DiffViewer({ content }: { content: string }) {
  if (!content.trim()) return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="该文件暂无 Diff" />;
  const allLines = content.split("\n");
  const maxRenderedLines = 5000;
  const lines = allLines.slice(0, maxRenderedLines);
  return (
    <div className="diff-viewer-wrap">
      {allLines.length > maxRenderedLines && (
        <div className="diff-truncated">Diff 共 {allLines.length} 行，仅渲染前 {maxRenderedLines} 行以控制浏览器占用。</div>
      )}
      <pre className="diff-viewer" aria-label="Git diff">
        {lines.map((line, index) => {
          const className = line.startsWith("+") && !line.startsWith("+++")
            ? "diff-add"
            : line.startsWith("-") && !line.startsWith("---")
              ? "diff-delete"
              : line.startsWith("@@")
                ? "diff-hunk"
                : "";
          return <span className={className} key={`${index}-${line.slice(0, 16)}`}>{line || " "}{"\n"}</span>;
        })}
      </pre>
    </div>
  );
}

export function GitPage() {
  const { project } = useProjectContext();
  const [selectedPath, setSelectedPath] = useState<string>();
  const [diffSource, setDiffSource] = useState<"worktree" | "staged">("worktree");
  const statusQuery = useQuery({
    queryKey: ["project", project.id, "git", "status"],
    queryFn: () => api.getGitStatus(project.id),
  });
  const commitsQuery = useQuery({
    queryKey: ["project", project.id, "git", "commits"],
    queryFn: () => api.getGitCommits(project.id),
  });
  const diffQuery = useQuery({
    queryKey: ["project", project.id, "git", "diff", selectedPath, diffSource],
    queryFn: () => api.getGitDiff(project.id, selectedPath, diffSource === "staged"),
    enabled: Boolean(selectedPath),
  });

  useEffect(() => {
    const first = statusQuery.data?.files[0];
    if (!selectedPath && first) {
      setSelectedPath(first.path);
      setDiffSource(first.worktree === "." && first.index !== "." ? "staged" : "worktree");
    }
  }, [selectedPath, statusQuery.data?.files]);

  if (statusQuery.isLoading) return <PageSkeleton rows={10} />;
  if (statusQuery.isError || !statusQuery.data) {
    return <ErrorState error={statusQuery.error} onRetry={() => void statusQuery.refetch()} />;
  }

  const git = statusQuery.data;
  const files = git.files ?? [];
  const worktrees = git.worktrees ?? [];
  const commits = commitsQuery.data ?? [];
  const selectedFile = files.find((file) => file.path === selectedPath);
  const canShowStaged = Boolean(selectedFile && selectedFile.index !== "." && selectedFile.index !== "?");
  const canShowWorktree = Boolean(selectedFile && !selectedFile.untracked && selectedFile.worktree !== "." && selectedFile.worktree !== "?");
  const selectFile = (file: GitFile) => {
    setSelectedPath(file.path);
    setDiffSource(file.worktree === "." && file.index !== "." ? "staged" : "worktree");
  };
  const fileColumns: ColumnsType<GitFile> = [
    { title: "状态", key: "status", width: 95, render: (_, file) => <FileStatus file={file} /> },
    {
      title: "文件",
      dataIndex: "path",
      render: (value: string, file) => (
        <div className="git-file-name">
          <Button type="link" onClick={() => selectFile(file)}>{value}</Button>
          {file.oldPath && <Typography.Text type="secondary" className="mono">原路径：{file.oldPath}</Typography.Text>}
        </div>
      ),
    },
    { title: "暂存区", dataIndex: "index", width: 80, align: "center", render: (value: string) => <span className="mono">{value}</span> },
    { title: "工作区", dataIndex: "worktree", width: 80, align: "center", render: (value: string) => <span className="mono">{value}</span> },
  ];
  const worktreeColumns: ColumnsType<Worktree> = [
    { title: "路径", dataIndex: "path", ellipsis: true, render: (value: string) => <Typography.Text className="mono" copyable>{value}</Typography.Text> },
    { title: "分支", dataIndex: "branch", width: 230, render: (value: string, item) => <span className="mono">{value || (item.detached ? "DETACHED" : "-")}</span> },
    { title: "HEAD", dataIndex: "head", width: 130, render: (value: string) => <Typography.Text className="mono" copyable>{shortHash(value)}</Typography.Text> },
    { title: "工作区", dataIndex: "dirty", width: 90, align: "center", render: (value: boolean) => <Tag color={value ? "warning" : "success"}>{value ? "Dirty" : "Clean"}</Tag> },
    { title: "关联任务", dataIndex: "taskKey", width: 180, render: (value: string) => value ? <Typography.Text className="mono" copyable>{value}</Typography.Text> : <Typography.Text type="secondary">未关联</Typography.Text> },
    { title: "状态", key: "state", width: 160, render: (_, item) => <Space>{item.bare && <Tag>bare</Tag>}{item.locked && <Tag color="warning">locked</Tag>}{item.prunable && <Tag color="error">prunable</Tag>}{!item.bare && !item.locked && !item.prunable && <Tag variant="filled">正常</Tag>}</Space> },
  ];
  const commitColumns: ColumnsType<GitCommit> = [
    { title: "提交", dataIndex: "shortHash", width: 110, render: (value: string) => <Typography.Text className="mono" copyable={{ icon: <CopyOutlined /> }}>{value}</Typography.Text> },
    { title: "说明", dataIndex: "subject", ellipsis: true },
    { title: "作者", dataIndex: "author", width: 180 },
    { title: "时间", dataIndex: "createdAt", width: 190, render: (value: string) => <span title={fullDate(value)}>{relativeDate(value)}</span> },
  ];

  return (
    <div className="page git-page">
      <PageHeader
        title="Git / Worktree"
        description="由系统 Git 的 porcelain 输出生成只读快照"
        meta={<Tag variant="filled" icon={git.dirty ? <WarningOutlined /> : <CheckCircleOutlined />}>{git.dirty ? "工作区有变更" : "工作区干净"}</Tag>}
      />

      {git.error && <ErrorState error={new Error(git.error)} compact />}
      <section className="git-summary">
        <div className="git-identity">
          <BranchesOutlined />
          <div>
            <Typography.Title level={3}>{git.branch || "DETACHED"}</Typography.Title>
            <Typography.Text type="secondary" className="mono">{shortHash(git.head)} / {git.upstream || "无上游"}</Typography.Text>
          </div>
        </div>
        <Descriptions
          size="small"
          column={4}
          items={[
            { key: "modified", label: "修改", children: git.modified },
            { key: "added", label: "新增", children: git.added },
            { key: "untracked", label: "未跟踪", children: git.untracked },
            { key: "conflict", label: "冲突", children: <span className={git.conflicted ? "text-danger" : undefined}>{git.conflicted}</span> },
            { key: "ahead", label: <><ArrowUpOutlined /> ahead</>, children: git.ahead },
            { key: "behind", label: <><ArrowDownOutlined /> behind</>, children: git.behind },
            { key: "worktrees", label: "Worktrees", children: worktrees.length },
            { key: "updated", label: "刷新", children: relativeDate(git.updatedAt) },
          ]}
        />
      </section>

      <Tabs
        className="git-tabs"
        defaultActiveKey="files"
        items={[
          {
            key: "files",
            label: `变更文件 (${files.length})`,
            children: files.length ? (
              <div className="git-files-layout">
                <Table<GitFile>
                  className="data-table"
                  rowKey={(file) => `${file.path}-${file.oldPath ?? ""}`}
                  size="small"
                  columns={fileColumns}
                  dataSource={files}
                  pagination={{ pageSize: 40, showSizeChanger: false, size: "small" }}
                  scroll={{ x: 520 }}
                  rowClassName={(file) => file.path === selectedPath ? "row-selected" : ""}
                  onRow={(file) => ({ onClick: () => selectFile(file) })}
                />
                <section className="diff-panel">
                  <div className="diff-toolbar">
                    <Typography.Text className="mono" ellipsis>{selectedPath || "请选择文件"}</Typography.Text>
                    {canShowStaged && canShowWorktree ? (
                      <Segmented
                        size="small"
                        value={diffSource}
                        onChange={(value) => setDiffSource(value as "worktree" | "staged")}
                        options={[{ label: "工作区", value: "worktree" }, { label: "暂存区", value: "staged" }]}
                      />
                    ) : selectedPath ? <Tag>{diffSource === "staged" ? "暂存区" : "工作区"}</Tag> : null}
                    <Tooltip title="重新读取 Diff"><Button type="text" icon={<FileSearchOutlined />} onClick={() => void diffQuery.refetch()} disabled={!selectedPath} /></Tooltip>
                  </div>
                  {selectedFile?.untracked
                    ? <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="未跟踪文件尚未进入 Git Diff" />
                    : diffQuery.isLoading
                      ? <PageSkeleton rows={8} />
                      : diffQuery.isError
                        ? <ErrorState error={diffQuery.error} onRetry={() => void diffQuery.refetch()} compact />
                        : <DiffViewer content={diffQuery.data ?? ""} />}
                </section>
              </div>
            ) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="工作区没有文件变更" />,
          },
          {
            key: "worktrees",
            label: `Worktrees (${worktrees.length})`,
            children: <Table<Worktree> className="data-table" rowKey="path" size="small" columns={worktreeColumns} dataSource={worktrees} scroll={{ x: 900 }} pagination={false} />,
          },
          {
            key: "commits",
            label: "最近提交",
            children: commitsQuery.isError ? <ErrorState error={commitsQuery.error} compact /> : <Table<GitCommit> className="data-table" rowKey="hash" size="small" loading={commitsQuery.isLoading} columns={commitColumns} dataSource={commits} pagination={{ pageSize: 20 }} scroll={{ x: 850 }} />,
          },
        ]}
      />
    </div>
  );
}
