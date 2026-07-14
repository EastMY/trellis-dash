import { FileMarkdownOutlined } from "@ant-design/icons";
import { Empty, Typography } from "antd";
import ReactMarkdown from "react-markdown";

export function MarkdownViewer({ content, name }: { content?: string; name?: string }) {
  if (!content?.trim()) {
    return (
      <Empty
        image={<FileMarkdownOutlined />}
        imageStyle={{ height: 42, fontSize: 38 }}
        description={`${name || "文档"} 暂无内容`}
      />
    );
  }

  return (
    <article className="markdown-viewer">
      <ReactMarkdown
        components={{
          a: ({ children, ...props }) => <a {...props} target="_blank" rel="noreferrer">{children}</a>,
          code: ({ children, className, ...props }) => (
            <code className={className} {...props}>{children}</code>
          ),
        }}
      >
        {content}
      </ReactMarkdown>
    </article>
  );
}

export function RawViewer({ value }: { value: unknown }) {
  return (
    <div className="raw-viewer">
      <Typography.Text copyable={{ text: JSON.stringify(value, null, 2) }}>复制 JSON</Typography.Text>
      <pre>{JSON.stringify(value, null, 2)}</pre>
    </div>
  );
}
