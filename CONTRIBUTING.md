# Contributing

感谢你改进 Trellis Dashboard。

## 开发约定

1. 保持 `.trellis` 为唯一事实源，SQLite 中的数据必须可重建。
2. Observer 模式不得写入被监控项目。
3. 不要把 `implement.jsonl`、`check.jsonl` 当成 Agent 运行日志。
4. 新增文件读取入口时必须验证真实路径、大小上限和符号链接边界。
5. Git 命令必须使用独立参数的 `exec.CommandContext`，禁止 Shell 字符串拼接。
6. 前端实时更新继续使用 REST Revision 轮询，不加入 WebSocket 或 SSE。

## 提交前检查

```bash
gofmt -w ./cmd ./internal
go test -race ./...
go vet ./...
pnpm --dir frontend test -- --run
pnpm --dir frontend build
git diff --check
```

涉及 UI 的改动还应验证 1280×800 桌面视口和 390px 移动视口，并检查加载、空数据、错误和外部更新状态。
