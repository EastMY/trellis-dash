import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { App } from "antd";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api/client";
import { AddProjectModal } from "./AddProjectModal";

vi.mock("../api/client", () => ({
  api: {
    getSystemCapabilities: vi.fn(),
    selectDirectory: vi.fn(),
    createProject: vi.fn(),
  },
}));

function renderModal() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <App>
        <AddProjectModal open onCancel={vi.fn()} />
      </App>
    </QueryClientProvider>,
  );
}

describe("AddProjectModal 目录选择", () => {
  beforeEach(() => {
    vi.mocked(api.getSystemCapabilities).mockReset();
    vi.mocked(api.selectDirectory).mockReset();
  });

  afterEach(() => cleanup());

  it("macOS 使用只读输入框并回填所选目录", async () => {
    vi.mocked(api.getSystemCapabilities).mockResolvedValue({ platform: "darwin", directoryPicker: true });
    vi.mocked(api.selectDirectory).mockResolvedValue("/Users/demo/trellis-project");
    renderModal();

    const button = await screen.findByRole("button", { name: /选择目录/ });
    const input = screen.getByPlaceholderText("请选择项目根目录");
    expect(input).toHaveAttribute("readonly");

    fireEvent.click(button);
    await waitFor(() => expect(input).toHaveValue("/Users/demo/trellis-project"));
  });

  it("非 macOS 保留手动输入", async () => {
    vi.mocked(api.getSystemCapabilities).mockResolvedValue({ platform: "linux", directoryPicker: false });
    renderModal();

    const input = await screen.findByPlaceholderText("/Users/name/projects/example");
    expect(input).not.toHaveAttribute("readonly");
    expect(screen.queryByRole("button", { name: /选择目录/ })).not.toBeInTheDocument();
  });
});
