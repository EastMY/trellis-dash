import { render, screen } from "@testing-library/react";
import { ConfigProvider, theme } from "antd";
import { describe, expect, it } from "vitest";
import { StatusTag } from "./StatusTag";

describe("StatusTag", () => {
  it("使用文字和图标共同表达活动状态", () => {
    const { container } = render(
      <ConfigProvider theme={{ algorithm: theme.darkAlgorithm }}>
        <StatusTag status="implementing" live />
      </ConfigProvider>,
    );
    expect(screen.getByText("实施中")).toBeInTheDocument();
    expect(container.querySelector(".anticon")).toBeInTheDocument();
    expect(container.querySelector(".status-active")).toBeInTheDocument();
  });

  it("未知状态仍显示可读文本", () => {
    render(<StatusTag status="security_review" />);
    expect(screen.getByText("security review")).toBeInTheDocument();
  });
});
