# oh-my-mdc

把 JavDB 自建清单里的番号，自动变成 Jellyfin 媒体库里的条目：轮询清单 → 加下载 → 刮削 → 硬链接整理 → 刷新媒体库 → 通知。

Go 单二进制内嵌前端（Vite + React Router SPA + Tailwind + shadcn/ui），全中文界面，状态全存 Postgres。规格正文见 [`docs/spec.md`](docs/spec.md)，领域词汇见 [`GLOSSARY.md`](GLOSSARY.md)，架构取舍见 [`docs/adr/`](docs/adr/)。

## 现在能跑什么

这一步是**行走骨架**（[#18](https://github.com/2017fighting/oh-my-mdc/issues/18)）：一条命令起本地栈、迁移自动跑、内嵌壳页打得开、探针与版本端点就位。业务循环要等后续票据。

## 快速开始

```bash
make dev
```

它会用 `compose.yaml` 起 Postgres（等 healthy），跑 `vite build` 把前端产出到 `internal/web/dist`，然后 `go run` 起服务。浏览器打开 <http://localhost:8080/>。

零配置：数据库 DSN 与监听地址都有默认值。

## 常用命令

| 命令 | 作用 |
|---|---|
| `make dev` | 起完整本地栈（Postgres + 服务） |
| `make test-local` | 起 Postgres 并跑全量测试（含迁移与 HTTP 面的集成测试，`-race`） |
| `make test` | 只跑测试，数据库由 `MDC_TEST_DATABASE_URL` 提供，没设则跳过集成测试 |
| `make check-local` | 本地全门：gofmt / vet / 前端 lint+typecheck / 测试 |
| `make check` | CI 的 Go 门（Postgres 由 CI 服务容器提供） |
| `make build` | 构建单二进制 `bin/oh-my-mdc`（前端先构建再 embed） |
| `make web-build` | 只构建前端到 `internal/web/dist` |
| `make db-down` / `make db-reset` | 停 Postgres / 停并清库 |

`make` 是构建门的唯一真源：CI 只调这些 target。

## 环境变量

用户设置不走环境变量（ADR-0002）；这里只有 bootstrap 参数，全部带默认值。

| 变量 | 默认值 | 说明 |
|---|---|---|
| `MDC_DATABASE_URL` | `postgres://mdc:mdc@localhost:5432/mdc?sslmode=disable` | Postgres DSN |
| `MDC_LISTEN` | `:8080` | 监听地址 |
| `MDC_DB_PASSWORD` | 空 | 给出时在进程内拼进 DSN（生产形态） |

## 端点

| 端点 | 行为 |
|---|---|
| `GET /healthz` | 只看进程：数据库挂了也回 200 |
| `GET /readyz` | ping Postgres；迁移失败或不可达回 `503 db_unavailable` 带原因 |
| `GET /version` | `-ldflags -X main.version` 注入的版本，默认 `dev` |
| 其余 URL | 未匹配的一律导向内嵌的 `index.html`（SPA 路由自己认路） |

## 目录

```
cmd/oh-my-mdc/      进程入口：配置 → 连库 → 迁移 → 监听
internal/config/    bootstrap 参数（env 三件套 + 默认值）
internal/db/        pgx 连接池 + 内嵌 goose 迁移（migrations/*.sql）
internal/server/    HTTP 面：探针、版本、SPA 静态资源
internal/web/       前端构建产物 + //go:embed
web/                前端源码（Vite + React Router SPA 模式）
```

`internal/web/dist/index.html` 是仓库里带的一份占位壳页，让没装 Node 的机器也能 `go build` / `go vet` / `go test`；`make web-build` 会用 Vite 的真实构建覆盖整个 `dist/`（产物本身被 git 忽略），提交前想恢复占位页就跑 `make web-reset`。

## 许可

[MIT](LICENSE)
