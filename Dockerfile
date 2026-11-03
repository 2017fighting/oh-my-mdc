# 发布镜像（规格 §10，票据 #33）。
#
# 三段：node 构建前端 → 静态单二进制（前端经 go:embed 进去）→ alpine 运行层。
#
# 为什么构建阶段钉在 $BUILDPLATFORM：本仓只发 linux/amd64，但钉住它仍然是
# 正确的写法 —— 一旦有人在 arm 机器上 `make docker-build`，构建阶段原生跑，
# 不会平白多花一个数量级的模拟器时间。只有最后的 alpine 阶段随目标架构走。
#
# 前面三个 ARG 只在 FROM 行里可用，所以写在所有 FROM 之前。

ARG NODE_VERSION=22
ARG GO_VERSION=1.27
ARG ALPINE_VERSION=3.21

# ---------------------------------------------------------------------------
# 前端阶段：Vite 产物是 go:embed 的输入，必须在镜像里现做一次
# ---------------------------------------------------------------------------
# 前端产物是 gitignore 掉的（仓库里只有占位壳页），所以这一阶段不是可选优化，
# 而是「镜像里有没有真界面」的唯一来源。
FROM --platform=$BUILDPLATFORM node:${NODE_VERSION}-alpine AS web

WORKDIR /src/web

# pnpm 的版本以 web/package.json 的 packageManager 为准（corepack 读它），
# 不在这里另写一份版本号 —— 两处版本号迟早会分叉。
RUN corepack enable

# 先只拷依赖清单：改前端代码不会让 pnpm install 这一层缓存失效。
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile

COPY web/ ./
# `pnpm build` 收尾的 scripts/copy-dist.mjs 会把产物抄到 ../../internal/web/dist，
# 也就是下面 Go 阶段要考的那个路径。
RUN pnpm build

# ---------------------------------------------------------------------------
# 后端阶段：CGO_ENABLED=0 的静态二进制
# ---------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build

WORKDIR /src

# 同样先只拷依赖清单：改代码不会让 go mod download 这一层缓存失效。
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# 仓库里只有占位壳页，真正的 dist 来自 web 阶段。放在 COPY . . 之后，
# 是为了让它覆盖占位页而不是被覆盖。
COPY --from=web /src/internal/web/dist ./internal/web/dist

# buildx 会按目标平台填入（这里只有 linux/amd64）；本地 `make docker-build`
# 也显式传 --platform，所以两者一致。
#
# 刻意**不给默认值**：写死 amd64 会让「忘了传」变成一个静默正确的假象。
ARG TARGETOS
ARG TARGETARCH

# VERSION 由 `make docker-build VERSION=...` 或 release workflow 传入，进 /version
# 与启动日志那一行。默认 dev 而不是编造一个版本号。
# 发布时传的是 tag 原文（v1.2.3），与 `make build VERSION=v1.2.3` 同形，
# 好让「镜像里的版本」与「本地构建的版本」能逐字比对。
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
	-trimpath \
	-ldflags "-s -w -X main.version=${VERSION}" \
	-o /out/oh-my-mdc \
	./cmd/oh-my-mdc

# ---------------------------------------------------------------------------
# 运行层
# ---------------------------------------------------------------------------
# 用 alpine 而不是 scratch：出问题时能 exec 进去看，HEALTHCHECK 也能用 busybox。
FROM alpine:${ALPINE_VERSION}

# ca-certificates 是**必需**的：上游（JavDB / DMM / M-Team / Jellyfin ...）全走 HTTPS，
# 没有根证书就是 "x509: certificate signed by unknown authority"。
#
# tzdata 是给下游用的：进程自己的时间戳默认就是 UTC（TZ 不设时 slog 走 local=UTC），
# 而 home-ops 若给容器设 TZ，得有 zoneinfo 才能生效。
#
# adduser -D -H -u 10001：非 root、无 home。uid 在镜像里固定，
# 好让下游的 securityContext 有据可依；-H 让镜像里不多一个空 home 目录。
RUN apk add --no-cache ca-certificates tzdata \
	&& adduser -D -H -u 10001 -s /sbin/nologin app

COPY --from=build /out/oh-my-mdc /usr/local/bin/oh-my-mdc

# 许可随镜像分发：MIT 要求分发副本时带上版权与许可声明，
# 而本服务主要的二进制分发渠道就是这个镜像。
# appapi/signature.go 里的 javdb-cli 归属声明留在源码里（同一份源码进镜像上下文）。
COPY LICENSE /usr/share/licenses/oh-my-mdc/

# .torrent 备份走 os.TempDir()（默认 /tmp，规格 §10 的「瞬态」）：应用不自己建目录，
# 靠 alpine 的 /tmp 权限；这里不挂卷、也不预先造目录。

USER app

# 容器里必须监听 0.0.0.0（默认值就是 :8080，见 config.DefaultListen）。
EXPOSE 8080

# liveness：只看进程，不掺数据库与上游 —— 签名失效重启一千次也没用。
# 这条是本地 `make docker-run` 的火警；生产探针由 home-ops 的 livenessProbe 负责（规格 §10），
# 两者看的是同一个 /healthz，互不干涉。
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
	CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1

# exec 形式：让进程成为 PID 1 直接收到 SIGTERM（main 里等循环退出的那段靠它）。
ENTRYPOINT ["/usr/local/bin/oh-my-mdc"]
