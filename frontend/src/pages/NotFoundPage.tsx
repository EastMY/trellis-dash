import { Button, Result } from "antd";
import { Link } from "react-router-dom";
import { ThemeToggle } from "../components/ThemeToggle";

export function NotFoundPage() {
  return (
    <main className="standalone-state">
      <ThemeToggle className="standalone-theme-toggle" />
      <Result
        status="404"
        title="页面不存在"
        subTitle="目标地址可能已失效，或任务已归档到其他位置。"
        extra={<Link to="/"><Button type="primary">返回项目</Button></Link>}
      />
    </main>
  );
}
