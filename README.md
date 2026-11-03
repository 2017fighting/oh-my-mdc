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
| `make docker-build` | 构建发布镜像（仅 linux/amd64） |
| `make docker-run` | 起 compose Postgres 并跑镜像，<http://localhost:8080/> |
| `make image-smoke` | 镜像冒烟：起容器断言 /readyz、版本注入、壳页、非 root |
| `make db-down` / `make db-reset` | 停 Postgres / 停并清库 |

`make` 是构建门的唯一真源：CI 只调这些 target。

## 镜像与发布

镜像是三段构建（`Dockerfile`）：node 阶段跑 `vite build`，golang 阶段 `CGO_ENABLED=0` 编出静态单二进制并把前端 `go:embed` 进去，最后落到 `alpine:3.21`（ca-certificates + tzdata，非 root uid 10001）。**只发 linux/amd64**。

版本走 `-ldflags -X main.version=`：`/version` 回注入值，启动日志带同一行，未注入时是 `dev`。发布时传 tag 原文（`v1.2.3`），与 `make build VERSION=v1.2.3` 同形。

发版动作 = 人工推 git tag：

```bash
git tag v1.2.3 && git push origin v1.2.3
```

release workflow 先跑与 CI **同一份** `verify.yml`（go + 前端 + 镜像冒烟，冒烟注入的就是这个 tag），再 buildx 推 `ghcr.io/2017fighting/oh-my-mdc`。tag 只出精确的 `X.Y.Z`（正式版另加 `latest`，预发布不加），没有浮动的 `1.2` / `1`。

镜像本地验证：`make docker-run`（容器经 `host.docker.internal` 连 compose 的 Postgres），或 `make image-smoke` 跑一遍断言。`make image-smoke` 也在 CI 与发版前跑，所以 Dockerfile 坏了在 PR 就会红。

仓内**没有 k8s 清单，也不配 Renovate**：生产清单在 home-ops，升级由那边的房规处理（规格 §10）。

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
scripts/            构建/冒烟脚本（image-smoke.sh）
.github/workflows/  verify.yml（唯一的「全门」定义）+ ci.yml + release.yml
```

`internal/web/dist/index.html` 是仓库里带的一份占位壳页，让没装 Node 的机器也能 `go build` / `go vet` / `go test`；`make web-build` 会用 Vite 的真实构建覆盖整个 `dist/`（产物本身被 git 忽略），提交前想恢复占位页就跑 `make web-reset`。

## 许可

[MIT](LICENSE)
