#!/usr/bin/env bash
# 镜像冒烟：把「镜像能跑」从假设变成证据（规格 §10，票据 #33）。
#
# 它验的是四件在 Dockerfile 里写、却在 CI 的 go/pnpm 门里看不见的事：
#   1. 真能连上 compose Postgres 并跑完迁移 —— 等的是 /readyz 200，
#      不是只看进程的 /healthz（后者 200 也可能一个迁移都没跑）；
#   2. 版本是构建时注入的那个 —— /version 与启动日志必须与传入的 VERSION 逐字相等
#      （发版走 tag 原文 v1.2.3，本地走 dev）；
#   3. 前端真的进了镜像 —— / 返回壳页 HTML，而不是 404 或占位页说的「尚未构建」；
#   4. 运行层该有的都在 —— 非 root（uid 10001）、ca-certificates、tzdata。
#
# 用法：IMAGE=oh-my-mdc:dev VERSION=dev ./scripts/image-smoke.sh
# 由 `make image-smoke` 调用（建镜像与起 compose Postgres 都是它的事）。
#
# 连接走 --add-host host.docker.internal:host-gateway：容器里的 localhost 是容器自己，
# compose 的 Postgres 在宿主上。
set -euo pipefail

IMAGE=${IMAGE:-oh-my-mdc:dev}
# 版本哨兵，与 Makefile 的 SMOKE_VERSION 一致：**不能是 dev**，
# 否则「ldflags 没注入」也能通过（编译期默认值就是 dev）。
VERSION=${VERSION:-v0.0.0-smoke}
HOST_PORT=${HOST_PORT:-18080}
DSN=${MDC_SMOKE_DATABASE_URL:-postgres://mdc:mdc@host.docker.internal:5432/mdc?sslmode=disable}
CONTAINER="oh-my-mdc-smoke-$$"
BASE="http://127.0.0.1:${HOST_PORT}"

info() { printf '\033[36m→\033[0m %s\n' "$*"; }
ok() { printf '\033[32m✓\033[0m %s\n' "$*"; }

fail() {
	printf '\033[31m✗\033[0m %s\n' "$*" >&2
	echo "--- 容器日志 ---" >&2
	docker logs "$CONTAINER" >&2 2>/dev/null || true
	echo "----------------" >&2
	exit 1
}

cleanup() { docker rm -f "$CONTAINER" >/dev/null 2>&1 || true; }
trap cleanup EXIT

docker image inspect "$IMAGE" >/dev/null 2>&1 || fail "镜像 $IMAGE 不存在：先 make docker-build"

info "起容器（$IMAGE，VERSION=$VERSION，Postgres 经 host-gateway）"
docker run -d --name "$CONTAINER" \
	--add-host host.docker.internal:host-gateway \
	-p "127.0.0.1:${HOST_PORT}:8080" \
	-e "MDC_DATABASE_URL=${DSN}" \
	"$IMAGE" >/dev/null

# 进程起来 → 迁移跑完 → 可服务，三步都凑齐才轮到 /readyz 回 200。
for _ in $(seq 1 60); do
	if curl -fsS -o /dev/null "${BASE}/readyz" 2>/dev/null; then
		break
	fi
	docker inspect -f '{{.State.Running}}' "$CONTAINER" 2>/dev/null | grep -q true ||
		fail "容器在就绪前退出"
	sleep 1
done

status() { curl -s -o /dev/null -w '%{http_code}' "${BASE}$1" || fail "请求 $1 失败"; }

[ "$(status /readyz)" = 200 ] || fail "/readyz 不是 200（迁移没跑完或数据库连不上）"
ok "/readyz 200（迁移已在 compose Postgres 上跑完）"

[ "$(status /healthz)" = 200 ] || fail "/healthz 不是 200"
ok "/healthz 200"

body=$(curl -fsS "${BASE}/version") || fail "取 /version 失败"
grep -Fq "\"version\":\"${VERSION}\"" <<<"$body" ||
	fail "/version 回的是 $body，want version=${VERSION}（构建时注入的那个，逐字）"
ok "/version 回 ${VERSION}"

docker logs "$CONTAINER" 2>&1 | grep -Fq "version=${VERSION}" ||
	fail "启动日志里没有 version=${VERSION}"
ok "启动日志一行带 ${VERSION}"

page=$(curl -fsS "${BASE}/") || fail "取 / 失败"
grep -Fqi '<html' <<<"$page" || fail "/ 不是 HTML 壳页"
# 区分真界面与仓库里那张占位壳页：占位页会自己说「前端尚未构建」。
if grep -Fq '前端尚未构建' <<<"$page"; then
	fail "/ 回的是占位壳页，前端没进镜像"
fi
grep -Fq '/assets/' <<<"$page" || fail "/ 里没有 /assets/ 引用，前端构建产物没进镜像"
ok "/ 返回真实前端壳页"

[ "$(docker exec "$CONTAINER" id -u)" = 10001 ] || fail "容器不是以 uid 10001 跑"
ok "非 root：uid 10001"

docker exec "$CONTAINER" test -f /etc/ssl/certs/ca-certificates.crt || fail "运行层缺 ca-certificates"
docker exec "$CONTAINER" test -f /usr/share/zoneinfo/Asia/Shanghai || fail "运行层缺 tzdata"
ok "运行层有 ca-certificates + tzdata"

echo "镜像冒烟通过：$IMAGE"
