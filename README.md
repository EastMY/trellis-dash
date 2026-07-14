# Trellis Dashboard

Trellis Dashboard 是面向本地 AI Coding 工作流的观察台。它默认只读地读取项目中的 `.trellis` 与 Git 数据，用一个 Go 进程提供 REST API 和内嵌的 React + Ant Design 6.5 前端。

> `.trellis` 文件始终是真实数据源。SQLite 只是可以随时删除并重建的查询缓存。

## 功能

- 多项目注册与切换
- Overview、动态状态 Kanban、任务列表和归档
- PRD、Design、Implement、Research 文档阅读
- `implement.jsonl` / `check.jsonl` Context 校验（文件、目录、重复引用与模板行）
- Session 与当前任务关联
- Git Branch、Dirty、ahead/behind、文件状态、Diff、Commit、Worktree Dirty 与任务关联
- 在项目概览中显式将当前分支推送到已配置的上游分支
- 资源级增量扫描、单任务 SQLite 增量写入与定期一致性全量扫描
- 纯 REST、资源 Revision、ETag/304 和活动增量查询
- React 19、TypeScript、Vite、Ant Design 6.5
- 前端嵌入 Go 二进制，预生成 Brotli/Gzip 表示，支持本机单文件和 Docker 部署

首版使用 Observer 模式：除用户显式点击 Git Push 写入远端仓库外，不会修改被监控项目，也不使用 WebSocket 或 SSE。

## 快速开始

要求：

- Go 1.25+
- Node.js 20.19+ 或 22.12+
- pnpm 10+
- 系统 Git

构建：

```bash
pnpm --dir frontend install
make build
```

直接观察一个项目：

```bash
./bin/trellis-dashboard serve \
  --project /absolute/path/to/project
```

然后打开 [http://127.0.0.1:7465](http://127.0.0.1:7465)。也可以先启动空 Dashboard，再从设置页添加项目。

## 开发

终端一启动后端：

```bash
go run ./cmd/trellis-dashboard serve \
  --database .data/dashboard.db \
  --project /absolute/path/to/project
```

终端二启动前端：

```bash
pnpm --dir frontend dev
```

Vite 开发服务器会把 `/api`、`/healthz` 和 `/readyz` 代理到 `127.0.0.1:7465`。

## 配置

复制示例文件：

```bash
cp dashboard.yaml.example dashboard.yaml
./bin/trellis-dashboard serve --config dashboard.yaml
```

核心配置：

```yaml
server:
  host: 127.0.0.1
  port: 7465

database:
  path: ~/.local/share/trellis-dashboard/dashboard.db

# Git 与 macOS/BSD 元数据轮询共用同一项目级刷新节拍
refresh_interval: 10s

projects:
  - id: android-agent
    name: Android Agent
    root: /absolute/path/to/android-agent
    mode: observer

watcher:
  debounce: 250ms
  full_rescan_interval: 10m

git:
  max_diff_bytes: 2097152
  command_timeout: 5s
```

文件数据库旁会以 `0600` 权限原子维护 `<database>.projects.json` 项目注册表；网页、配置或 CLI 注册的 Observer 项目都会写入其中。仅删除 SQLite、WAL 和 SHM 后，服务可从该 sidecar 恢复项目并重建查询缓存；删除整个数据目录（包括 sidecar）则不承诺恢复。

## 数据与刷新模型

```text
.trellis / Git
      │
      ├─ fsnotify / 元数据轮询 + 定期校验
      │
      ▼
 Go Indexer ── SQLite Read Model
      │
      └─ REST + Revision + ETag
                    │
                    ▼
        React + Ant Design 6.5
```

- Linux/Windows 使用 `fsnotify` 事件；BSD/macOS 为避免 kqueue 的逐文件 FD 开销，使用有界元数据轮询。最多两个项目同时轮询，稳态路径检查使用固定 worker，目录结构变化时才重新遍历目录树。
- 已知任务、Session、Spec、Workflow 会按资源增量扫描；单个任务只重读自己的 `task.json`、文档与 Context，并只写对应 SQLite 行。新增、重命名、未知路径以及每 10 分钟的一致性校验会安全回退全量扫描。
- 每个项目使用统一的 `refresh_interval`，默认每 10 秒采集 Git；BSD/macOS 元数据轮询与它共用同一个 ticker。旧 `git.refresh_interval`、`watcher.poll_interval` 配置会直接报错。
- Git 变化只重新校验已索引的 Context 路径，不再连带重读全部任务文档。
- 前端在可见时每 10 秒请求一个很小的 Revision 响应，隐藏时暂停。
- Revision 的 `generation` 同时标识缓存实例和项目实例；删库重建或同 ID 删除后重注册都会换代，旧 ETag 不会误命中。
- 活动接口支持 `afterId` 增量获取，活动页也可以继续加载更早记录。
- `implement.jsonl` 和 `check.jsonl` 是 Context 清单，不被误当作 Agent 日志。

## REST API

统一前缀：`/api/v1`。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET / POST` | `/projects` | 项目列表和注册 |
| `DELETE` | `/projects/{id}` | 取消注册，不删除项目文件 |
| `POST` | `/projects/{id}/rescan` | 手动重扫 |
| `GET` | `/projects/{id}/revision` | 资源 Revision |
| `GET` | `/projects/{id}/dashboard` | 概览聚合快照 |
| `GET` | `/projects/{id}/tasks` | 任务查询与过滤，服务端分页，单页最多 200 条 |
| `GET` | `/projects/{id}/tasks/{key}` | 任务、文档元数据、Context、Session、活动 |
| `GET` | `/projects/{id}/tasks/{key}/artifact?path=...` | 按需读取单份文档正文 |
| `GET` | `/projects/{id}/sessions` | Session 列表 |
| `GET` | `/projects/{id}/git/status` | Git 工作区快照 |
| `GET` | `/projects/{id}/git/commits` | 最近提交 |
| `GET` | `/projects/{id}/git/diff` | Diff 文本 |
| `POST` | `/projects/{id}/git/push` | 将当前分支推送到既有上游 |
| `GET` | `/projects/{id}/activity?afterId=...` | 增量活动 |

健康检查：`/healthz`；数据库就绪检查：`/readyz`。

概览接口只返回 Git 摘要；Git 文件和 Worktree 明细仍由 `/git/status` 按需获取。任务看板为每个状态列独立分页，详情页只在用户打开文档标签时请求正文，大型 Diff 在浏览器端最多渲染前 5,000 行。

使用 `--log-level debug` 可查看 `trellis.scan.full`、`trellis.scan.incremental`、`git.snapshot` 等结构化性能指标，其中包含扫描/写入/内存回收耗时、遍历量、原始读取量及 SQLite/WAL 大小。

## 安全边界

- 默认只监听 `127.0.0.1`。
- 首版只接受 `observer` 模式。
- 项目必须存在 `.trellis` 目录。
- SQLite 数据库不得位于被监控项目内；服务会在打开数据库前拒绝路径重叠。
- 新建的专用数据目录使用 `0700`，数据库及项目注册 sidecar 使用 `0600`；不会改写既有父目录权限。
- 读取会解析真实路径并拒绝符号链接或 `../` 越过项目根。
- JSON 最大 2 MiB，Markdown 最大 10 MiB，JSONL 单行最大 1 MiB。
- 单任务文档总量最大 50 MiB / 1,000 个，单项目最大 200 MiB / 10,000 个。
- 单项目最多索引 10,000 个任务、10,000 个 Session、10,000 个 Spec；Context 与各类源文件另有总量上限。
- 单次项目扫描共享 200,000 个遍历项与 512 MiB 原始读取预算；事件监听最多接纳 20,000 个文件/目录，BSD/macOS 元数据轮询最多检查 200,000 项。
- Git 命令使用独立参数和超时，不接受任意 Shell 字符串。
- Git Push 仅在用户显式点击时执行，只允许当前分支的既有上游，不会自动创建或改写上游配置。
- Git status 最大 4 MiB，worktree/commit 输出最大 1 MiB，Diff 最大 2 MiB；单快照最多 128 个 Worktree，并限制并发 Git 进程。
- 每个项目只保留最近 5,000 条活动，事件展示字段和 payload 均会截断到安全范围。

## 测试

```bash
go test -race ./...
pnpm --dir frontend test -- --run
pnpm --dir frontend typecheck
pnpm --dir frontend build
```

## Docker

仅使用观察功能时，建议把项目只读挂载：

```bash
docker build -t trellis-dashboard .
docker run --rm \
  -p 127.0.0.1:7465:7465 \
  -e TZ=Asia/Shanghai \
  -v dashboard-data:/data \
  -v /absolute/path/to/project:/workspace/project:ro \
  trellis-dashboard \
  --host 0.0.0.0 \
  --database /data/dashboard.db \
  --project /workspace/project
```

`TZ` 决定“今日完成”和 revision `day` 的自然日边界，请按部署所在地调整。只读挂载会禁用 Git Push；如需推送，必须改为可写挂载并向容器提供对应远端凭据。容器内 Git 命令只在单次命令作用域信任已注册目录，不修改全局 `safe.directory`。

## License

[MIT](LICENSE)
