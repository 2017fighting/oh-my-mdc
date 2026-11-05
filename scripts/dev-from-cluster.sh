#!/usr/bin/env bash
# 把生产库的快照灌进本地开发栈：`make dev` 的面板从此跑真实条目，不是手写 mock。
#
# 为什么需要它：面板上绝大多数设计决策（虚警、回执、抽屉、循环健康）只有在
# 「真实形状的数据」上才看得出对错——一个 finished 的任务和一百个失败的版本，
# 在空库上长得一模一样。真实部署在 home-ops 的 oh-my-mdc ns，库在
# database ns 的 pg-cluster；它就在手边，不必再编假数据。
#
# 三条硬规矩，缺一条就把开发环境变成生产事故：
#   1. **回滚本地库**：先 drop schema 再灌，不和生产混着留半截。
#   2. **断网**：快照里带着真凭据（JavDB token、Telegram bot token、qbt 密码）。
#      灌完立刻把四个出站地址改成黑洞 127.0.0.1:9、凭据换成占位串——
#      开发环境里点「重试」「测试连接」只会失败在本地，绝不触达真实上游。
#      面板照常渲染，因为它读的是库里的状态，不是上游的活连接。
#   3. **暂停循环**：app_config.paused=true。否则开发进程一起来就去轮询真 JavDB。
#
# 媒体目录：门禁的第二半（P4-2）要求 jellyfin 的 media_root 在本机可见。
# 真实值是 /media/jellyfin/jav（k8s 里是共享 PVC）；本地按库里的值建空目录即可，
# 只是为了让门禁通过，不写任何文件进去。
#
# 用法：
#   ./scripts/dev-from-cluster.sh          # 拉快照 + 脱敏 + 建目录
#   make dev                               # 起本地栈，面板就是真实数据
#
# 前置：kubectl 能读 home-ops 的 oh-my-mdc 与 database 两个 ns；compose Postgres 在跑。
set -euo pipefail

APP_NS=${APP_NS:-oh-my-mdc}
DB_NS=${DB_NS:-database}
DB_POD=${DB_POD:-pg-cluster-1}
DB_SERVICE_USER=${DB_SERVICE_USER:-oh-my-mdc}
DB_NAME=${DB_NAME:-oh-my-mdc}
SECRET=${SECRET:-oh-my-mdc-role-password}

# 本地开发库：与 compose.yaml、config.DefaultDatabaseURL 对齐。
LOCAL_DSN=${LOCAL_DSN:-postgres://mdc:mdc@localhost:5432/mdc?sslmode=disable}
BLACKHOLE=${BLACKHOLE:-http://127.0.0.1:9}

info() { printf '\033[36m→\033[0m %s\n' "$*"; }
ok() { printf '\033[32m✓\033[0m %s\n' "$*"; }
die() { printf '\033[31m✗\033[0m %s\n' "$*" >&2; exit 1; }

psql_local() { psql "$LOCAL_DSN" "$@"; }

command -v kubectl >/dev/null || die "找不到 kubectl"
command -v psql >/dev/null || die "找不到 psql"
kubectl get ns "$APP_NS" >/dev/null 2>&1 || die "读不到 $APP_NS（kubectl 连的是哪个集群？）"

info "读生产库口令（$DB_NS/$SECRET）"
PW=$(kubectl -n "$APP_NS" get secret "$SECRET" -o jsonpath='{.data.password}' | base64 -d)
[ -n "$PW" ] || die "secret $SECRET 里没有 password"

info "灌快照：$DB_NS/$DB_POD → $LOCAL_DSN"
# 先清空再灌：半截的合并比空库更难排查。drop schema 会连带视图 task_state，
# 灌回来的 dump 自己会重建它（pg_dump 默认带 CREATE VIEW）。
psql_local -q -c "set client_min_messages to warning; drop schema public cascade; create schema public;" >/dev/null
kubectl -n "$DB_NS" exec -i "$DB_POD" -- \
  env PGPASSWORD="$PW" pg_dump -h 127.0.0.1 -U "$DB_SERVICE_USER" -d "$DB_NAME" \
  --no-owner --no-privileges 2>/dev/null \
  | psql_local -q -v ON_ERROR_STOP=1 >/dev/null
ok "快照就位"

info "断网 + 暂停：开发环境不得触达真实上游"
psql_local -q <<SQL
update qbittorrent    set url = '$BLACKHOLE', username = 'dev', password = 'dev';
update jellyfin       set url = '$BLACKHOLE', api_key = 'dev-placeholder';
update ai_translation set base_url = '$BLACKHOLE', api_key = 'dev-placeholder';
update javdb          set token = 'dev-placeholder';
update mteam          set api_key = 'dev-placeholder';
update telegram       set bot_token = 'dev-placeholder';
update app_config     set paused = true;
SQL
ok "四个出站地址指向 $BLACKHOLE，凭据已换成占位串，循环已暂停"

info "按库里的 media_root 建空目录（门禁 P4-2 要求它本机可见）"
MEDIA_ROOT=$(psql_local -At -c "select coalesce(nullif(trim(media_root), ''), '/media') from jellyfin limit 1;")
if [ -d "$MEDIA_ROOT" ]; then
  ok "$MEDIA_ROOT 已存在"
else
  mkdir -p "$MEDIA_ROOT" 2>/dev/null && ok "建了 $MEDIA_ROOT" || die "建不了 $MEDIA_ROOT（需要 sudo mkdir -p $MEDIA_ROOT）"
fi

COUNT=$(psql_local -At -c "select count(*) from task;")
DONE=$(psql_local -At -c "select count(*) from task_version where status = 'finished';")
ok "本地库：$COUNT 个任务，$DONE 条已入库版本"
printf '\n现在跑 \033[36mmake dev\033[0m，面板读的就是这份真实形状的数据。\n'
printf '记住：循环是暂停的，任何动作都只会失败在本地（%s）。\n' "$BLACKHOLE"
