# oh-my-mdc v1 规格

> 本文是 wayfinder 地图 [oh-my-mdc v1 — the way to a build-ready spec](https://github.com/2017fighting/oh-my-mdc/issues/1) 的终点产物：把 PLAN.md 的原始想法与 14 张已闭票据的裁决装配成一份**可直接 `/to-tickets` 的构建规格**。每节标注裁决来源（票号）；无标注处为本装配票（[#14](https://github.com/2017fighting/oh-my-mdc/issues/14)）落定的细节。领域词汇以 [GLOSSARY.md](../GLOSSARY.md) 为准；架构级取舍见 `docs/adr/`。

## 1. 产品概述

把 JavDB 自建清单里的番号，自动变成 Jellyfin 媒体库里的条目：

```
JavDB 想看清单（按 id 轮询）
  → 磁力/种子定源（M-Team 优先，JavDB 磁力兜底）
  → qBittorrent 下载（tag 标记，autorun 回调 + 对账双通道）
  → 刮削（DMM 图片/日文元数据 + JavDB 演员/其余 + 可选 AI 翻译）
  → 硬链接整理 + NFO/图片落盘（一番号一目录）
  → Jellyfin 刷新 + Telegram 通知（旁路副作用，独立重试）
```

Go 单二进制内嵌前端（Vite + React Router SPA + Tailwind + shadcn/ui），全中文界面，单用户自托管，无应用层鉴权（暴露策略在网关层）。状态全存 Postgres；循环为 poll / reconcile / sidefx 三循环 + webhook 即时通道。

## 2. 常设决策（平台与栈）

（来源：地图 Notes + #5 + #6 + #12）

| 项 | 决策 |
|---|---|
| 语言/形态 | Go 单二进制，`go:embed` 前端；**Postgres only**（CNPG 生产 / 仓内 compose + CI 服务容器开发，无 SQLite —— ADR-0001） |
| 存储 | `pgx/v5` + `goose/v3`（embed，启动自动 Up、只前进，advisory lock 防双迁） |
| 前端 | Vite + React Router（framework mode，`ssr:false`）+ Tailwind v4 + shadcn/ui；`vite build` → `internal/web/dist` → `//go:embed all:dist`（#5） |
| 鉴权 | 无应用层鉴权；公网走 Gateway gated/authelia；机器回调走集群内 svc DNS |
| 移植 | javdb_rss（MIT）：`internal/appapi`（app-API 传输层）、`internal/mteam`、`internal/pin/policy.go`（磁力选择策略种子）；open-mdcng（MIT）：`organize/nfo.go`+`images.go`、`plugins/image.go`（右裁 2:3）、`plugins/translate.go`、`scrape/dmm.go`+`field.go`。mdc-lite 无 license，**不移植其代码**（其 ADR 思想可循）；`signature.go` 内嵌 javdb-cli MIT 归属须保留；本仓自选 LICENSE（建议 MIT）（#6） |
| AI 翻译 | OpenAI 兼容端点（base URL + key + model），可选集成（#6/见 §5.7） |
| 部署 | 仓内出 amd64 alpine 镜像 + make 驱动 CI；home-ops 新 ns `oh-my-mdc`，mdc.raenzo.com，详见 §10（#12） |

## 3. 领域模型

（来源：#7，词汇见 GLOSSARY.md）

**两级聚合**（ADR-0003）：

- **任务**（`task`，主键 `movie_id`）：番号只是展示/目录属性；承载刮削元数据（值 + 逐字段 provenance + 逐源 raw snapshot）与聚合状态。
- **版本**（`task_version`，主键 `infohash`，全局唯一）：一条下载候选。普通/中字/破解各是一条**版本**（`label` best-effort 展示标签）；VR 的 cd1/cd2 是同一条版本内的**分片**（`version_file`），不是版本。

**状态集**：`downloading → processing → finished`，或 `failed(phase, reason)`，`phase ∈ {downloading, scrape, organize}`。

- 任务级状态由版本聚合（视图 `task_state`，不落列，ADR-0006）：任一 processing → `processing`；否则任一 downloading → `downloading`；否则任一 finished → `finished`；全部 failed → `failed`。
- 完成是版本级的：入库即通知；新版本到来任务重开（v2 中字场景，v1 仅结构上允许）。
- 删清单项 / 刷 Jellyfin / 发 Telegram **不是状态**，是旁路副作用（ADR-0005）。
- qBittorrent tag（`mdc:in-progress` / `mdc:finished`）只是对账线索与人看标记，**DB 是状态真相**；失败不引入第三个 tag。

**元数据三面**：强类型列（值）+ `provenance`（逐字段 `{source, fetched_at, raw_value}`）+ `raw_snapshot`（逐源原始返回，force 重刮对比用）。

## 4. 数据模型

（来源：#8 及其事后修订；DDL 级清单，迁移文件命名 `000NN_name.sql`）

枚举一律 `text + CHECK`；时间 `timestamptz`（UTC）；主键自然键优先。

### 4.1 任务域

- **`task`**：`movie_id text PK`、`number text NOT NULL`（番号）、`list_id text NOT NULL REFERENCES list_registry`（首次发现来源；清单 id 是字符串，见 §4.3）、刮削强类型列（清单见 §7.1：`title_ja`、`title_zh`、`outline`、`outline_zh`、`release_date date`、`runtime_minutes int`、`studio text`、`publisher text`、`series text`、`director text`、`trailer_url text`、`rating numeric`、`tags text[]`）、`provenance jsonb`、`raw_snapshot jsonb`、`created_at`、`updated_at`。
- **`task_version`**：`infohash text PK`、`movie_id FK`、`source text CHECK ('javdb_magnet','mteam_torrent')`、`delivery text CHECK ('magnet','torrent')`、`label text`（普通/中文/破解，best-effort）、`status text CHECK ('downloading','processing','finished','failed')`、`error_phase text CHECK ('downloading','scrape','organize')`、`error_reason text`、`claimed_at timestamptz`、`claimed_by text`、`attempts int NOT NULL DEFAULT 0`、`next_attempt_at timestamptz`、`add_pending bool`、`list_removal_pending bool`、`force_rescrape bool`（强制重刮：下一次取数不复用元数据，#31）、`payload text`（magnet 串或 .torrent 备份路径）、`created_at`、`updated_at`。
- **`version_file`**：`id bigint identity PK`、`infohash FK`、`rel_path`、`size_bytes`、`part_index`、`is_primary bool`。（入库幂等与「已落盘」判定的永久记录。）
- **`task_event`**（append-only）：`id identity PK`、`movie_id`、`infohash NULL`、`kind text CHECK ('state_change','side_effect','alert','manual_retry')`、`from_state`、`to_state`、`phase`、`reason`、`detail jsonb`（结构化附注；强制重刮把旧 `raw_snapshot` 存这里做对比基准，#31）、`at timestamptz`。承担告警「只发一次」判据、面板历史、失败追问。
- **`actor`**：`javdb_actor_id text PK`、`name`、`gender NULL`、`avatar_url NULL`、`updated_at`；**`task_actor (movie_id, javdb_actor_id, role)`** 多对多。刮削时随 JavDB 数据全量落行（含性别）；v1 不抓演员详情页（PLAN 15 就位）。

### 4.2 副作用出箱

- **`side_effect`**：`id identity PK`、`kind text CHECK ('list_removal','jellyfin_refresh','notify')`、`target_key text`、`payload jsonb`、`next_attempt_at timestamptz`、`attempts int`、`last_error text`、`done_at timestamptz NULL`；**部分唯一索引** `UNIQUE(kind, target_key) WHERE done_at IS NULL`（#8 修订：同片再进清单可再删/再发，不被「已完成」永久封条）。`target_key`：list_removal = `"<list_id>:<movie_id>"`、jellyfin_refresh = `movie_id`、notify = `"<event>:<infohash>"`。
- **驻留**（需人工）定义：`done_at IS NULL AND next_attempt_at IS NULL`（重试耗尽或永久错误）。jellyfin_refresh 靠唯一索引天然 coalesce。
- **视图 `task_state`**：按 §3 优先级实时聚合。

### 4.3 设置域（ADR-0002）

- **`list_registry`**：`list_id text PK`（JavDB 清单 id，判定永远靠 id；**是字符串不是整数**——#16 实测清单 id 为 6 字符 nanoid，如 `e8r1OM`，建清单响应的 `data.id` 也是字符串，见 §5.1）、`purpose text CHECK ('want_watch','want_watch_zh') UNIQUE`、`name text`（快照，仅展示）、`created_at`。
- **六张集成表**，统一契约：**行存在＝已配置**；可选表（mteam / ai_translation / telegram）另带 `enabled bool`；每表统一 `last_test_at timestamptz`、`last_test_ok bool`、`last_test_error text`：
  - `javdb`：`token`（敏感）、`last_test_*`。
  - `qbittorrent`：`url`、`username`、`password`（敏感）、`category text DEFAULT 'mdc'`、`download_root text`、`webhook_base_url text`、`last_test_*`。（#9 裁决：webhook base URL / 下载根目录 / category 落 qbt 表；#8 的 `app_config.base_url` 因此作废。）
  - `jellyfin`：`url`、`api_key`（敏感）、`media_root`（媒体库根目录；#22 从 `app_config` 搬来——三容器同路径，刷新与硬链接共用）、`last_test_*`。
  - `mteam`：`api_key`（敏感）、`enabled`、`last_test_*`。
  - `ai_translation`：`base_url`、`api_key`（敏感）、`model`、`enabled`、`last_test_*`。
  - `telegram`：`bot_token`（敏感）、`chat_id`、`enabled`、**`notify_download_started bool DEFAULT true`、`notify_filed bool DEFAULT true`、`notify_processing_failed bool DEFAULT true`、`notify_download_stalled bool DEFAULT true`**、`last_test_*`（#13 列名落定）。
- **`app_config`**（单行 `id=1`）：`poll_interval DEFAULT 15m`、`reconcile_interval DEFAULT 5m`、`sidefx_interval DEFAULT 1m`、`max_attempts DEFAULT 3`、`paused bool DEFAULT false`、`updated_at`。程序自己写的时间戳不进本表（#8 修订；`last_test_*` 在集成表，不受此限）。
- **env 只有 bootstrap**：`MDC_DATABASE_URL`（默认 `postgres://mdc:mdc@localhost:5432/mdc?sslmode=disable`）、可选 `MDC_DB_PASSWORD`（进程内拼进 DSN，prod 形态见 §10）、`MDC_LISTEN`（默认 `:8080`）。~~`MDC_DATA_DIR` 作废~~（#12：无应用数据目录）。用户设置零 env 覆盖路径。

### 4.4 索引与语义要点

- 索引：`task_version(status, next_attempt_at)`、`task_version(claimed_at)`、`side_effect(kind, next_attempt_at)`、`task_event(movie_id, id DESC)`。
- **claim**（版本互斥，#7+#8）：`UPDATE task_version SET claimed_at=now(), claimed_by=$2 WHERE infohash=$1 AND (claimed_at IS NULL OR claimed_at < now()-interval '10 minutes') RETURNING ...`，完事清零；配哨兵错误 `ErrNoPendingTask`（本轮没活）/ `ErrClaimLost`（抢输别当失败），进程内再加 per-version 锁。
- **循环单飞**：poll/reconcile/sidefx 各持一把 `pg_advisory_lock`（专用连接），防 Flux 滚动更新时新旧 pod 重叠；版本级互斥仍归 claim。
- **启动顺序**：迁移（goose Up，advisory lock）→ 读设置 → 三件套齐（JavDB 可读且清单登记就位、qBittorrent 可连、Jellyfin 可写＝`url`+`api_key`+`media_root` 齐且目录在本机可见）才算 `initialized`，三循环才启动；**迁移失败不 abort 启动**：页面能看、`/readyz` 回 `503 db_unavailable` 带原因、循环不跑（#8 修订）。
- 敏感列：代码常量清单（表.列）驱动掩码与日志脱敏，配一条反射单测——新增 string 列未登记即测试失败。DB 明文（信任边界＝CNPG 与其备份）。
- 历史永久保留，无 retention；UI 单条删除只删记录不碰媒体（判重依赖版本行存在，不做自动清理）。

## 5. 集成契约

### 5.1 JavDB（App 私有 JSON API）

（来源：#2 传输层 + #7 修正 + #16 实测）

- 传输层整体移植 javdb_rss `internal/appapi`（MIT）：每个请求 8 个公共 query 参数 + `jdsignature` 头（常量可轮换，`InvalidSignature` = 修代码）；错误按信封 `action` 分类，不看 HTTP 状态。**业务校验错 = `success:0` + `action:null`（zh-TW message）；`action` 非空才是传输/凭据类错**（#16 分层）。
- **凭据**：单个非过期 Bearer JWT（配在向导，存 DB）。PLAN 1 的「优先环境变量」被 ADR-0002 推翻：token 只走向导入库。**单会话账号**：新登录会踢掉手机 → **绝不自动重登**；共享手机 token（javdb_rss `scripts/export-phone-token.sh` 先例）或专用账号。token 失效（`JWTVerificationError`）→ 上游 permanent 错误（喂 §9 凭据告警）+ 需人工。
- 端点实测表（v1 全部所需）：

| 操作 | 端点 | 要点 |
|---|---|---|
| 读清单列表 | `GET /api/v1/lists/simple` | 向导探测用；按 id 匹配，名字只是快照（默认清单在两种读法下名字不同） |
| 读清单项 | `GET /api/v1/movies/tags?filter_by=0:l:{list_id}`（zone=0） | 每项带 `id`（=movie_id）+ `number`（番号）+ movieSlim；`page`/`limit` 有效，无总页数，**空页即止**；匿名可读（token 在场则携带） |
| 建清单 | `POST /api/v1/lists` | **form-encoded**（JSON 被拒）；`name` 必填 4–30 字符、**同名拒**（`名稱 您已創建了同名清單`）；`description` 可选；响应 `data.id` 即新清单 id |
| 增/删清单项 | `POST /api/v1/lists/{list_id}/movie_actions` | form **`name`=`"add"`|`"remove"`** + `movie_id`；⚠️ 动作不在 `action` 字段（被忽略），⚠️ **未知/缺失 `name` 默认执行 remove**——取值必须是代码常量，绝不接用户输入 |
| 磁力候选 | `GET /api/v1/movies/{movie_id}/magnets` | `hash/cnsub/size(MiB)/hd/created_at`；匿名可读 |

- 顺带实测可用（备用）：`PATCH`/`DELETE /api/v1/lists/{id}`（改名/删清单）。
- 幂等（#16 实测）：重复 add、重复 remove、remove 不存在的 movie 全部 `success:1`；add 不存在的 movie → `success:0` + Rails 校验泄漏。成功响应 `data` 恒 null。
- 并发自帽 8（javdb_rss 先例，~0.85s/req）；无已知 429。
- **镜像与链路抖动**（2026-11 真机抓包，见 `docs/javdb-app-traffic.md`）：`jdforrepam.com` 只是镜像之一（App 侧看到 `apidd.spthgb.com` / `apidd.czssdgz.com`，形状一致）；到各镜像的 TLS 握手有 3–35% 的**阵发**断链（跨域名跨节点，握手期就断，一个响应字节都没有）。处置：传输层共用一份 Transport（连接复用）+ 幂等 GET 的有界重试（2 次、400ms 起翻倍），不是凭据问题，**不要**据此报凭据告警。
- **清单名陷阱**：服务端要求 4–30 字符，「想看」二字不合规 → 向导新建默认名 **「想看清单」**（description 建议「oh-my-mdc 自动创建」），用户可改；探测匹配按 id，名字仅展示。

### 5.2 M-Team

（来源：#3；移植 javdb_rss `internal/mteam`）

- 鉴权 `x-api-key` 头；**失败是 HTTP 200 + 信封 `code`**，成功 `code=="0"`；UA 非空且不含 `curl`。
- 搜索：`POST /api/torrent/search`，**JSON** `{mode:"adult", keyword:<番号>, pageNumber, pageSize≤200, visible:1}`。行内 `infoHash` 恒 null；`hasChineseSubtitle` 撒谎——**中字判定用 `labelsNew`**。折扣 `status.discount`（FREE/PERCENT_50/…）+ `discountEndTime`。
- 下载两步：`POST /api/torrent/genDlToken`（**form** `id=<tid>`）→ ~5 分钟时效 URL → `GET`（跟 302 到 CDN；**API 端点不得跟随重定向**，下载 URL 要跟）→ 私有 `.torrent`（`private=1`、per-issue announce credential、无磁力可能）→ **infohash 本地算**（原始 bencode `info` 字节 SHA-1）。`.torrent` 字节瞬态走 `/tmp`（emptyDir），落 `payload` 备份路径。
- **限速**（官方「建议值」，binding 的是下载动作 100/h、日配额 1000）：配额 API 不可读。仅在「JavDB 有磁力的 movie 且尚无版本」时才搜索（PLAN：javdb 没有 → 直接结束）。
- 种子选择策略（本票落定，可调）：`status.seeders` 降序 → 折扣（FREE 优先）→ 体积降序；命中即取一条候选。
- 取种失败/无 key/配额疑尽 → **降级 JavDB 磁力，产生第二条版本**（#8），一条版本永远只下一种形态。
- **备份失效后的重取**（#36）：`payload` 存的备份路径读不到时不重试那条路（文件不会再出现），而是回 M-Team 按番号**重取一次**，且只认 infohash 与版本行相符的那条（今天按选种策略挑中的可能是另一条种子——API 不在搜索里给 infohash，只有下完才核得出来；不符就是「站点上已无该种」，不拿别的种子凑合）。重取**守 M-Team 的启停开关**（禁用时给「启用后再重试」的永久失败，不去撞配额）。重取成功 → 落盘、把新路径写回 `payload`、照常加种；永久失败（站点上已无该种 / 凭据不对）→ `failed(downloading, reason)` 进需人工区，不再每轮拉红 poll；瞬时失败（连不上 / 超时）→ 保持 `add_pending`，下一轮再试且不把这一轮判成循环故障。

### 5.3 DMM

（来源：#4；移植 open-mdcng `scrape/dmm.go`+`field.go`；明细 `docs/research/dmm-scraping.md` @ 分支 `research/dmm-scraping`）

- **不是 HTML 抓取**：player GraphQL `POST https://api.video.dmm.co.jp/graphql`，五个硬头（`Referer: https://video.dmm.co.jp/`、`Content-Type`/`Accept: application/json`、`Fanza-Device: BROWSER` 等，全集合见研究文档）。
- 发现：`legacySearchPPV(limit:30, floor:AV, sort:SALES_RANK_SCORE, queryWord:<番号>)` → 候选 product id。**id ≠ 番号，有两种零填充拼写**（`ssis001`/`ssis00001`）都要试；命中按**身份匹配**（前缀 + 数值相等），绝不按位置。1–2 搜索单 + 1 内容单。
- 内容：`ppvContent(id)+reviewSummary` 一次 POST：ja `title`/`description`（AI 翻译输入）、RFC3339 日期、`duration` 秒、ja genres、maker/label、女优。图片绝对 URL：package `…/<cid>pl.jpg`（+`ps.jpg`）、预览 `…/<cid>jp-N.jpg`、演员 `mono/actjpgs/*.jpg`。CDN 无需 Referer，但**与 API 同需日本出口**（生产 mihomo 已覆盖；开发机 TLS 被阻，见 §13）。
- 海报几何（本票落定的算法化裁决）：poster 取「横版 Sleeve」右裁 2:3 —— 在 pl/ps 中**取宽>高的那张**为横版（首次刮削时一次 `curl + file` 探针核对，两说并存的研究悬案以算法兜底，pl 更可能为横版）；裁剪失败＝部分成功不失败任务（mdc-lite ADR-0015 机制）。DMM 整体缺失（无码/FC2/素人）→ title/outline/图片字段级回退 JavDB（poster 由 JavDB 横版封面右裁），演员等归属照旧。

### 5.4 qBittorrent

（来源：#9；事实基线 qBittorrent 4.6.5 生产 + 5.3 分支源码）

- 登录拿会话 cookie（4.6.5 / 5.0–5.1 叫 `SID`，5.2.0 起改名 `QBT_SID_<port>` 且成功回 204 空体，名字与值都要原样回发）；登录回执三解析（4.6.5 纯文本 `Ok.`/`Fails.`、5.x JSON、5.2 起空体 + 会话 cookie）随客户端移植（实现配方非决策）。加种 `POST /api/v2/torrents/add`：磁力 `urls=`，私有种子 multipart `torrents=`（`Content-Type` 是 `application/x-bittorrent`）；**每次显式 `savepath`=download_root、固定 category（默认 `mdc`，`createCategories` 显式）、不设 autoTMM、contentLayout 原样**。
- **回调**：`GET http://<svc>.<ns>.svc.cluster.local:<port>/api/hooks/qbt?ih=%I`，载荷只带 infohash（**qbt 是事实源**，本项目回调 `torrents/properties` 读权威值）；未知/非本项目 infohash → 200 静默 no-op。
- **autorun 共存**（ADR-0007）：向导把 `autorun_program` 重写为

  ```
  /bin/sh -c "curl 'http://moviepilot…/transfer/now?token=<原token>' ; curl -fsS -m 10 'http://<svc>/api/hooks/qbt?ih=%I'"
  ```

  外层双引号给 qbt 拆 argv、payload 内一律单引号、`;` 两命令独立；幂等＝现值已含本项目 URL 则跳过；改写前展示**前后完整对比**、人确认后落；旧命令双引号归一成单引号；**绝不碰 `autorun_on_torrent_added_*`**；唯一变量 `%I` 纯 hex（qbt Linux autorun 是 argv 拆分不走 shell）。
- **撞车**（同 infohash 在 qbt 但无项目 tag）：**收编**——补 tag + category，savepath 不动，版本继续走流程。加种前先查 `torrents/properties`：在且无 tag → 收编；在且有 → 直接推进；不在 → add，竞态报「已存在」但种子在 → 视为成功。
- 做种/删种/限速全不碰；`mdc:finished` 继续在 qbt 做种，生死由 qbt 全局分享率策略裁决。
- tag 生命周期：加种时 `mdc:in-progress` + 番号 tag + category；入库成功换 `mdc:finished`；失败不新增 tag（qbt 里永远只有两个项目 tag）。
- 对账发现种子消失 → `failed(下载阶段, 种子已从 qBittorrent 移除)` + 一次性告警；人工重试＝重新加种。

### 5.5 Jellyfin

（来源：#10）

- 设置：`url` + `api_key` + `media_root`；「可写」判定＝`GET /System/Info` 鉴权通过（向导要管理员 API key；该端点只要被接受的凭据就读得到，权限不足的 token 会在第一次刷新时以 auth 暴露，见 §9）**且** `media_root` 在本机可见（三容器同路径，缺目录就没有硬链接与刷新）。
- 刷新：**目录级 rescan**（`POST /Library/Media/Updated`，目标 `{media_root}/{番号}` 目录，目录在盘才调）；被拒降级全库；**2xx 只当「已受理」**（未知路径也回 204，实测陷阱）；两次失败 = warning，走 side_effect 独立重试 + coalesce。
- 三容器同路径约定：qbt / oh-my-mdc / Jellyfin 把同一 host 目录挂同一路径（`/media`）；设置仅 `media_root` 一项，无路径翻译层。

### 5.6 Telegram

（来源：#13）

- **v1 单向 sendMessage**：无 getUpdates/webhook/命令/按钮；单一 `chat_id`；人工重试只走面板。
- 四事件，逐事件开关（§4.3 四布尔列）默认全开，逐条即时、无摘要聚合：

| 事件 | 触发 | 去重 |
|---|---|---|
| 下载开始 | 加种成功（`add_pending` 清掉），**收编同发** | `(event, infohash)` 一条 |
| 入库完成 | 版本硬链+落盘后；任务重开再入库 = 「新版本入库」 | `(event, infohash)` |
| 加工失败 ×1 | 第 3 次自动重试耗尽落 `failed(phase, reason)` | `task_event` |
| 下载停滞 ×1 | 对账发现 progress 长时间不动，保持 downloading | `task_event`（版本级） |

- 格式：`sendMessage + parse_mode=HTML`，用户内容 HTML 转义，**无图**。模板（zh-CN，文案照此进实现）：

  ```
  🎬 <b>{番号}</b> 开始下载\n{标签} ｜ {M-Team/磁力} ｜ {大小}
  ✅ <b>{番号}</b> 已入库\n{标题：有中文译名用译名，否则原文}\n{标签} ｜ {来源} ｜ {大小}
  ⚠️ <b>{番号}</b> 加工失败\n阶段：{phase} ｜ 原因：{reason}\n请到面板重试
  ⏳ <b>{番号}</b> 下载停滞\n最后进度 {progress}，已停滞 {时长}
  ```

- 发送失败分流：**瞬时**（网络/429/5xx）sidefx 指数退避最多 5 次后驻留；**永久**（400/401/403）不重试立即驻留。驻留 = 面板需人工区置顶 + 抽屉逐条「重发」；**绝不自动禁用集成**。告警兜底不依赖 Telegram 自身：UI 事件流 + `/metrics`（§9）。
- 测试连接：**getMe（验 token）→ sendMessage 测试消息（验 chat_id）**，两步都过才绿；错误区分「token 无效」/「chat 未激活」；chat_id 获取提示：先跟 bot 说句话 / @userinfobot。
- 入箱 payload 契约（#28 落定，整理票 #29 按此入箱）：公共键 `movie_id`、`infohash`、`number`、`label`、`source`（`javdb_magnet`/`mteam_torrent`）、`size_mib`（前两个是驻留时事件流兜底的锚点：没有它们，一条发不出的通知在时间线上留不下任何痕迹）；`filed` 另带 `title`（有中文译名用译名）与 `new_version`（任务重开再入库 = true，走「新版本已入库」文案分支）；`processing_failed` 带 `phase`/`reason`；`download_stalled` 带 `progress`（0..1）/`stalled_minutes`。缺 `number` 或事件名不认识 = 本地校验错，**驻留等人工**而不是静默丢。被执行半段按成功收尾的两种「不发」：telegram 节不存在（可选集成没配，告警兜底是事件流 + `/metrics`）、集成 disabled 或该事件开关关着（关掉即不发，之后补发旧事件没有意义）。

### 5.7 AI 翻译（可选）

（来源：#6 移植 `plugins/translate.go` + 地图 Notes）

OpenAI 兼容 chat completions（`base_url` + `api_key` + `model` + `enabled`）。刮削时译 `title_ja → title_zh` 与 `outline → outline_zh`（一次请求可合并，实现自由度）。**失败 → title/plot 降级日文原题照常入库** + `task_event` 告警（一条，带分类与原因），不阻塞入库。测试连接 = 1 token 极小请求。

**翻译不是取数**：复用判据命中的版本仍是 0 次 JavDB/DMM 调用，但 AI 翻译启用而译文缺失时会补一次翻译请求（用户常在任务刮完以后才启用翻译）；补译失败与正式翻译同一处置。实测落定（#30）：一次请求回 JSON `{title, outline}`，`temperature=0`；错误分类 `auth`（401/403，或没填 `api_key`——缺凭据与 mteam/telegram 同一判词）/`business`（本地缺 `base_url`/`model`、上游 400/404/422）/`network`（429/5xx）/`timeout`/`protocol`（非 JSON、无 choices、译文不是 JSON 对象；容忍 markdown 代码围栏与 JSON 前后的一句客套）。失败告警带分类与原因（`AI 翻译失败（<code>）：…`），同一版本只留一条。

## 6. 状态机与循环

（来源：#7；转移表为准）

### 6.1 转移表

| 转移 | 触发 | 规则 |
|---|---|---|
| 建任务/版本 | poll（默认 15m，全量扫到底，每项查 `magnets(movie_id)`） | 磁力为空 → **不落任何行**，下轮重查；非空 → 定源后按 hash upsert 版本行（`add_pending=true`） |
| 定源 | 同上 | JavDB 磁力非空才查 M-Team；M-Team 命中 → 私有 `.torrent`；无 key/配额/取种失败 → **降级 JavDB 磁力**（第二条版本）。JavDB 多磁力选择策略（本票落定，种子 `internal/pin/policy.go`）：`cnsub` 降序 → `hd` 降序 → 体积降序 → `created_at` 降序，取一条 |
| 加下载 | 同上（**先落库再加种**，ADR-0004） | qbt 添加成功或「已存在」→ `add_pending=false`、发「下载开始」；失败 → 告警下轮重试 |
| 去重 | 任意重入 | 同 infohash 已有版本行 → 不做任何 qbt/清单写入（不重复加种/删项），pending 标记照旧重试；**任务已有版本则 poll 不再重复查源** |
| 置待删 | 加下载后同轮 | `list_removal_pending=true`；真正删清单项只在旁路循环执行（恒在加下载之后） |
| → processing | webhook 即时 / 对账 | `progress==1.0` **且** qbt state ∈ {uploading, stalledUP, pausedUP} **且** `content_path` 可读含视频文件；checking/moving → 下轮再看 |
| processing | — | 任务元数据已存在 → **复用（0 次网络）**，只硬链新视频文件；不存在 → 全量刮削 → NFO/图片 → 硬链 |
| → finished | 硬链接 + NFO/图片落盘后 | tag `mdc:in-progress` → `mdc:finished`；置 jellyfin_refresh / notify 待办 |
| 失败 | 加工每一步 | 最多 3 次（`max_attempts`）指数退避，第 3 次落 `failed(phase, reason)` + Telegram ×1；此后不自动碰 |
| 下载异常 | 对账 | 种子消失 → `failed(下载阶段, 种子已移除)`；完成但文件缺 → `failed(下载阶段, 文件丢失)`；**progress 持续不动（常量 60 分钟，非设置项）→ 只告警 ×1，保持 downloading** |
| 人工重试 | 面板抽屉 | `retry(movie_id, force=false)`：非 finished 版本回 processing 开头；下载阶段的版本（含下载中但种子已不在 qbt 的）按存的 magnet/种子重加（允许重下；私种备份缺失时同样回 M-Team 重取一次，#36）；`force=true` 才重刮已 finished 版本（旧 `raw_snapshot` 先落 `manual_retry` 事件的 `detail`），并对任何分支都置起 `force_rescrape`；**qbt 里仍在下载的永不重下**（该版本跳过并附原因） |
| 重启 | 进程启动 | downloading 不动（真实进度在 qbt）；processing 阶段级复位重跑（幂等，无步骤游标）；finished/failed 不动 |
| 互斥 | 全部 | 版本行条件 claim（§4.4）+ 进程内 per-version 锁 |

### 6.2 四个触发通道

| 通道 | 默认间隔（设置项） | 职责 |
|---|---|---|
| poll | 15m | 清单全量扫（空页即止）→ 磁力 → 定源 → 建版本 → 加种 → 置待删 |
| reconcile | 5m | 扫 `mdc:in-progress` 补 webhook 漏；推进 processing；判定下载异常/停滞 |
| sidefx | 1m | 删清单项 / 刷 Jellyfin / 发 Telegram，各自重试（瞬时退避、永久驻留） |
| webhook | 即时 | 单版本推进（`GET /api/hooks/qbt?ih=`） |

## 7. 刮削与整理

（来源：#10；字段映射全表与分片细则是本票装配落定）

### 7.1 字段映射全表

固定归属 + 字段级回退（唯一回退方向 DMM→JavDB；JavDB 字段缺失留空不回退）：

| 字段（task 列） | 默认源 | 回退 | NFO 槽 |
|---|---|---|---|
| `title_ja` | DMM `ppvContent.title` | JavDB 标题 | `<originaltitle>` = 番号+日文原题；`<title>` = 番号+(译名 \| 原题) |
| `title_zh` | AI 翻译（可选） | 无（降级原题） | 同上 |
| `outline` | DMM `description` | JavDB 简介 | `<plot>` 单槽（译文或原文，outline 槽不用） |
| `outline_zh` | AI 翻译 | 无 | 同上 |
| 图片 | DMM `pl/ps` + `jp-N` | JavDB 封面/预览 | poster.jpg / fanart.jpg / extrafanart |
| 演员 | JavDB（含性别） | — | `<actor>` 全写、女优排前、组内保 JavDB 顺序 |
| `tags` | JavDB tags | — | `<tag>`（**不写 `<genre>`**，Jellyfin 按 `/` 拆） |
| `publisher` | JavDB label | — | 降级为 tag「发行: X」 |
| `rating` | JavDB | — | `<rating>` |
| `studio` | JavDB maker | — | `<studio>` |
| `series` | JavDB series | — | `<set>` |
| `release_date` | JavDB | — | `premiered` + `releasedate` **双写** |
| `runtime_minutes` | JavDB | — | `<runtime>` |
| `director` | JavDB | — | `<director>` |
| `trailer_url` | JavDB | — | `<trailer>`（有则写） |

**NFO 约定**（照搬 open-mdcng 实测 + 标题槽改造）：固定名 `movie.nfo`（多片目录里番号命名不会被读）；原子写 temp+rename；内容 hash 同则不重写（免无谓 rescan）；**`<sorttitle>` = 纯番号**；不写 uniqueid/dateadded/lockedfields/watched/playcount；数字零值省略；多值重复元素不拼接。

**图片落盘**：`poster.jpg`（横版 sleeve 右裁 2:3，DMM 缺则 JavDB 封面右裁）、`fanart.jpg`（原横版）、`extrafanart/fanart{N}.jpg`（jp-N 预览）；扩展名随源 URL 不转格式；预览收缩时清理旧 fanartN。`<actor><thumb>` 直写 JavDB URL（Jellyfin 自拉），v1 不下载头像。

### 7.2 目录布局（ADR-0008）

- 固定 `{media_root}/{番号}/{番号}[-中文|-破解][-cdN]{扩展名}`，**v1 无模板**（词表 v1 仅「中文/破解」二词，扩词只加词不改布局）。
- **多版本同目录**共享一份 movie.nfo + 图片 → 复用元数据 = **只硬链新视频文件**，零拷贝零改写。
- **分片细则**（本票落定）：视频文件判定 = 扩展名 ∈ {mp4,mkv,avi,wmv,mov,m2ts,ts,rmvb,flv,webm,mpg,mpeg}；分片序号 = stem 匹配 `[ _.\-]?(?:cd|disc|disk|part)[ _.\-]?(\d{1,2})`（大小写不敏感）；**多片全显式含 cd1**，单片版本裸名；多片但无序号 → 自然排序赋顺序号；`is_primary` = cd1/最前。字幕扩展名 {srt,ass,ssa,vtt,sub,idx,sup} 随对应视频同名硬链；同视频多字幕撞名时保留源文件语言后缀。
- 非视频文件（txt/广告图/种子自带 nfo）不落盘（视频+字幕白名单）。
- **硬链模式唯一**（无 move/copy/symlink 配置面）；**EXDEV = 任务失败**并明示换挂载，绝不静默降级 copy。
- **碰撞**：同番号两条无标签版本（不同 infohash 都指向 `{番号}.ext`）→ 后到者失败驻留等人工；同番号不同 movie_id 的两任务相撞同样失败等人工。不覆盖、不自动 -v2（宁噪勿静默丢数据）。
- AI 翻译失败 → title 降级日文原题照常入库 + task_event 告警，不阻塞。

## 8. Web UI 与 REST 契约

（来源：#11 变体 A 全取 + #8/#13 装配期遗留；REST 形状本票落定）

### 8.1 向导（分步，一屏一步）

左侧竖向步骤条，顺序：**JavDB 账号与清单 → qBittorrent → Jellyfin → M-Team（可选）→ AI 翻译（可选）→ Telegram（可选）→ 完成（间隔与重试在最后一步）**。可选步可跳过。每步独立「保存本步」＝按表 PATCH；敏感字段只吐掩码、省略或空串＝保持原值；并发编辑靠 `revision` 指纹提示冲突；每步一个「测试连接/测试读取/测试可写」按钮，结果 chip 写在按钮旁（写 `last_test_*`）。

- **JavDB 步**：token（掩码）+ 清单按 id 登记——读取已有清单优先（探测列表，按 id/名字匹配「想看清单」），新建 = `POST /api/v1/lists` 探测 + **手动粘贴 id 兜底**（#15/#16 流程，名字 4–30 规则见 §5.1）。
- **qBittorrent 步**：地址/账号（可选）/category/下载根目录/回调 base URL（集群内 svc DNS）+ **autorun 改写前后完整对比**，保存前再确认一次（§5.4）。
- **Jellyfin 步**：地址 + api key + `media_root`（三者同一步一次保存；「测试可写」＝鉴权 + 目录本机可见）。
- **M-Team / AI 翻译 / Telegram 步**：凭据 + enabled（+ Telegram 四勾选框；测试 = getMe → 测试消息）。
- 定位：**配完就不碰**——再配置走侧边栏「集成设置」回同一向导（编辑态），不做两套界面。

### 8.2 面板（表格为主体，要动手的事置顶）

区块顺序：统计卡（下载中/加工中/已入库/需人工）→ **「需要人工」告警区置顶**（`failed(phase,reason)` + 停滞 + 驻留副作用，行内重试/重发按钮）→ 稠密任务表（一行一任务按 movie_id 聚合，版本以小 chip 展示）→ 底部副作用队列 + 循环健康 + 最近事件。停滞行按最近一条停滞告警认；人工重试重新加种之后那条旧告警退场（#31）。

任务详情 = **右侧抽屉**，保持精简：版本列表（infohash、来源、标签、失败原因）+ 事件时间线 + 重试/强制重刮（force）。**不看刮削字段与 provenance**（v1 不做，不留占位）。

### 8.3 门禁与文案

未初始化（三件套不齐）＝整站只剩向导，面板入口禁用 + 一张解释卡。文案照原型（`prototype/wizard-dashboard` 分支）：

- 解释卡：「主线三件套（JavDB 可读且清单登记就位、qBittorrent 可连、Jellyfin 可写）齐了，循环才会启动」；Jellyfin 一行另列「媒体库目录在盘可见」（`media_root_visible`），未初始化时一眼看出是哪一半没到。
- 状态名「下载中/加工中/已入库/需人工」；按钮「重试/强制重刮/测试连接/保存本步/重发」；版本失败原因直译 phase。全部 copy zh-CN。

### 8.4 REST 契约（本票落定；全部 JSON；无应用鉴权）

**设置**（sections：`javdb|qbittorrent|jellyfin|mteam|ai_translation|telegram|app|list_registry`）：

| 端点 | 语义 |
|---|---|
| `GET /api/config` | 全部 section + `initialized` + `media_root_visible`（门禁第二半，向导解释卡用）；每节 `{revision, configured, enabled?, data}`；敏感字段呈 `{configured: bool, masked: "ab***yz"}`，原文永不回显 |
| `GET /api/config/{section}` | 单节，带 `revision`（内容指纹） |
| `PATCH /api/config/{section}` | 部分字段 + `revision`；省略＝不改，敏感字段空串也＝不改；`revision` 冲突 → `409 revision_conflict`；校验错 → `400` |
| `DELETE /api/config/{section}` | 删行＝解除配置；主线三件套被删 → `initialized` 变 false 回到向导 |
| `POST /api/config/{section}/test` | 跑测试连接，写 `last_test_*`，返回 `{ok, code, detail, duration_ms}` |

`revision` = 该节当前内容的规范 JSON sha256 短指纹，读写往返检测并发编辑（open-mdcng 先例）。`list_registry` PATCH = `{list_id, purpose:'want_watch', name}` upsert；向导辅助端点 `GET /api/javdb/lists`（探测）、`POST /api/javdb/lists {name, description?}`（代建，同名拒原样透出）。 qBittorrent 的 autorun 共存改写同样走向导辅助端点：`GET /api/qbittorrent/autorun` 只读预览 `{before, after, skip, changed, enabled, warning}`（前后**完整**命令串），`POST /api/qbittorrent/autorun` 是人工确认后的落地，只写 `autorun_program`（ADR-0007）。

**向导辅助端点的错误**（`GET/POST /api/javdb/lists`，与下面测试错误码同一套分类）：`auth`/`business` → `400`（要用户改凭据/输入），`protocol`/`network` → `502`（服务端行为异常或网络，查或重试）；`{ok:false, error:{code, message}}`，`business` 的 `message` 原样带上游文案（如「您已創建了同名清單」）。

**测试连接错误码**（稳定机器码；超时一律 **10s**）：`timeout`、`auth`（凭据无效/被踢/API key 拒）、`network`、`protocol`（非预期响应/信封）、`business`（业务校验错，如 JavDB `success:0`+`action:null`、清单同名拒）、`token_invalid` / `chat_unavailable`（Telegram 两步测试专用）。各节测试内容：javdb = 用户域读 + 登记清单读；qbittorrent = 登录 + 版本读 + autorun 现值读（供 diff）；jellyfin = `GET /System/Info` 鉴权 + `media_root` 存在；mteam = pageSize=1 单行搜索；ai_translation = 1 token 补全；telegram = getMe → sendMessage。

**任务与运维**：

| 端点 | 语义 |
|---|---|
| `GET /api/overview` | `{initialized, paused, counts:{downloading,processing,finished,needs_attention}, loops:{poll/reconcile/sidefx:{last_run_at,last_ok}}}` |
| `GET /api/tasks?state=&q=&page=` | 任务行（`task_state` 视图 + 版本摘要 chip 数据） |
| `GET /api/tasks/{movie_id}` | 任务 + 版本列表 + 最近事件（抽屉数据） |
| `POST /api/tasks/{movie_id}/retry` | `{force: bool=false}`，语义照 §6.1 人工重试行；回执 `{results:[{infohash, action, from_state, to_state, reason, force_rescrape}]}`——逐版结果，版本侧被拒/失败不是 HTTP 错误 |
| `GET /api/events?movie_id=&limit=` | 事件时间线 |
| `GET /api/attention` | 「需要人工」区的任务侧两半（#26 落地 §8.2 时新增）：`{failed:[{movie_id,number,infohash,phase,reason,at}], stalled:[…]}`；驻留副作用由下面那一条提供 |
| `GET /api/side-effects?state=pending\|stuck` | 副作用队列（stuck = §4.2 驻留） |
| `POST /api/side-effects/{id}/retry` | 人工重发（清驻留、重排 next_attempt_at） |
| `GET /api/hooks/qbt?ih=<hex>` | qbt autorun 回调；恒 200；未知 ih 静默 no-op（§5.4） |

任务事件行的 `detail` 原样回出（人工重试把旧 `raw_snapshot` 留在 `detail.raw_snapshot_before`、结局留在 `detail.action`）：对比基准只经接口可查，面板 v1 不渲染刮削字段（#31）。

错误信封统一 `{ok:false, error:{code, message}}`。SPA 路由：Go handler 把所有未匹配 URL 导向 index.html（#5）。

## 9. 可观测性（指标名本票落定）

- 端点：`/healthz` 只看进程（liveness 绝不折叠 DB/上游）；`/readyz` ping Postgres（迁移失败 → `503 db_unavailable` + 原因）；`/metrics` Prometheus；`/version`（ldflags 注入，默认 `dev`，启动日志一行）。
- 指标（`mdc_` 前缀，v1 最小集）：

| 指标 | 类型 | 标签 | 用途 |
|---|---|---|---|
| `mdc_loop_last_success_unix_seconds` | gauge | `loop∈{poll,reconcile,sidefx}` | 循环停滞判定 |
| `mdc_loop_iterations_total` | counter | `loop,result∈{ok,error}` | 循环健康 |
| `mdc_upstream_requests_total` | counter | `upstream∈{javdb,mteam,dmm,qbittorrent,jellyfin,telegram,openai},result∈{ok,transient,permanent}` | 凭据失效判定；permanent = 401/403 类（javdb JWTVerificationError、telegram 400/401/403、mteam 鉴权 code 等） |
| `mdc_side_effect_stuck` | gauge | `kind` | 驻留副作用数（喂需人工区，不做告警规则） |

- 最小 VMRule 两条（#12）：**LoopStalled**：`time() - mdc_loop_last_success_unix_seconds{loop="poll"} > 2400`（reconcile > 900、sidefx > 600）；**UpstreamCredentialsInvalid**：`increase(mdc_upstream_requests_total{result="permanent"}[15m]) > 0`。Grafana 面板不做。

## 10. 构建与部署

（来源：#12）

**仓内**：多阶段 Dockerfile（node 构建前端 → `--platform=$BUILDPLATFORM` golang `CGO_ENABLED=0` + embed → `alpine:3.21`，ca-certificates + tzdata，`adduser -D -H -u 10001`）；**仅 linux/amd64**。镜像 `ghcr.io/2017fighting/oh-my-mdc`，推 `vX.Y.Z` tag → verify → buildx；tag 出精确 `X.Y.Z`（非预发布加 `latest`），无浮动 tag，`provenance:false`。CI（main+PR，make 目标为唯一真源）：gofmt/vet/test -race + 前端 job（pnpm lint/typecheck/build）+ **postgres:18 服务容器**（goose Up + 集成测试）+ **镜像门**（`make image-smoke`：docker build 后起容器接 compose Postgres，断言 /readyz、版本注入、壳页可开与非 root；日常 CI 与发版前的 verify 共用同一份 `verify.yml`，不各写一份）。开发 = 仓内 `compose.yaml`（postgres:18-alpine + healthcheck）+ `make dev`/`make test`，DSN 默认值零配置可跑。**仓内零 k8s 清单、不配仓内 Renovate**。

**home-ops**（唯一事实源；`apps/mdcng` 原样保留，切换另行决定——out of scope）：新 ns `oh-my-mdc`（app-registry：`profile: proxy` DMM 走 mihomo 日本出口、`edge: gated`、deps traefik/authelia/infra-database-cluster/qbittorrent-private/jellyfin）。`apps/oh-my-mdc/`：app-template HelmRelease（chartRef OCIRepository）+ HTTPRoute（**mdc.raenzo.com**，gated，authelia access_control 加条目，lint.py 校验两侧一致）+ VMPodScrape + VMRule。端口 8080 命名 http；探针按 §9。存储：唯一卷 = 共享 media PVC（hardlink-substrate ResourceSet 加成员行）挂 `/media`；无应用 PVC；`.torrent` 瞬态 emptyDir `/tmp`；`readOnlyRootFilesystem` + drop ALL + seccomp RuntimeDefault；**runAsUser 1000 / runAsGroup 1500 / fsGroup 1500**（与 qbt PUID 1000/PGID 1500、jellyfin 一致，硬链成立）；向导默认 `download_root=/media/downloads/completed/av`、`media_root=/media/jellyfin/jav`。DB：共享 pg-cluster + Database CR `pg-oh-my-mdc`（db+role `oh_my_mdc`）+ role 密码 SOPS 于 database ns + reflector 镜像到应用 ns（**唯一 SOPS 引导密钥**）；DSN 进程内拼装 + 密码 env（§4.3）。副本 1 + **Recreate**（绝不双实例跑循环）+ reloader；req 100m/128Mi limit 512Mi；Service 带 glance 磁贴注释。升级走 hosted Renovate helm-values 房规分组，零新配置；发版动作 = 人工推 git tag。

## 11. PLAN 逐条验收

每条：裁决去向 + 验收清单（AC）。AC 编号 `P{PLAN 条}-{序号}`。

**1 初始化 JavDB token** —— 裁决：向导 JavDB 步，token 入 DB（env 优先被 ADR-0002 推翻）。
- P1-1 全新部署 → 未初始化，整站只有向导；JavDB 步可输入 token、点「测试连接」，通过写 `last_test_ok=true`。
- P1-2 坏 token → chip 显示 `auth` 类错误（区分于网络错）；不落 `configured`。
- P1-3 token 存 `javdb.token` 明文，API 永不回显原文（掩码）；日志脱敏单测在位。

**2 初始化清单** —— 裁决：一键读清单、可代建、按 id 登记（#15/#16）。
- P2-1 已有「想看清单」→ 探测列出、选中登记，`list_registry(purpose='want_watch')` 落 id + 名字快照。
- P2-2 无 → 一键创建（默认名「想看清单」），成功拿 `data.id` 登记；同名拒错误原样透出给用户。
- P2-3 手动粘贴 id 兜底路径可用（探测失败/用户自有清单场景）。
- P2-4 判定永远按 id：清单改名不影响轮询（有回归测试）。

**3 连接 qBittorrent** —— 裁决：#5.4 契约。
- P3-1 向导保存 url/账号/category/download_root/webhook_base_url，测试连接绿（登录+版本+autorun 现值）。
- P3-2 autorun 改写展示前后完整对比，确认后落 `sh -c` 共存命令；moviepilot 原 curl 保留且仍工作。
- P3-3 幂等：重复保存不再改写（现值已含本项目 URL → 跳过）；`autorun_on_torrent_added` 不被触碰。
- P3-4 既有 moviepilot autorun 的双引号被归一为单引号，语义不变。

**4 设置 Jellyfin** —— 裁决：#5.5。
- P4-1 向导 Jellyfin 步一次存 url+api_key+`media_root`（#22），测试连接 = System/Info 鉴权 + `media_root` 存在性。
- P4-2 media_root 不可见 → 明确错误提示（三件套不齐，面板不开放：`initialized=false`，解释卡点名这一条）。

**5 M-Team（可选）** —— 裁决：#5.2。
- P5-1 未配置/禁用 → 定源只走 JavDB 磁力，无 M-Team 网络调用。
- P5-2 配置且启用 → 新磁力 movie 先搜 M-Team；命中 → genDlToken → `.torrent` → 本地 SHA-1 infohash → multipart 加种，版本 `source=mteam_torrent`。
- P5-3 genDlToken 失败/超时 → 同轮降级 JavDB 磁力，产生第二条版本，任务状态正确聚合。
- P5-4 搜索/取种计入 §9 upstream 指标；`code!="0"` 分类为 auth/protocol。

**6 AI 翻译（可选）** —— 裁决：#5.7。
- P6-1 配置且启用 → 刮削产出 `title_zh`/`outline_zh`；NFO title/plot 用译文。
- P6-2 翻译失败 → 入库不阻塞，title/plot 降级日文原文，`task_event` 告警一条。
- P6-3 禁用 → NFO title/plot 全用日文原文，无 OpenAI 调用。

**7 内部循环** —— 裁决：#6 poll 行。
- P7-1 清单项有磁力 → 先落版本行（`add_pending=true`）再 add；成功后 `add_pending=false` + 「下载开始」通知 + `list_removal_pending=true`。
- P7-2 加种带 番号 tag + `mdc:in-progress` + category + 显式 savepath；不设 autoTMM。
- P7-3 清单项无磁力 → 零行落库，下轮重查。
- P7-4 同 infohash 重入 → 无 qbt/清单写入；删清单项失败下轮重试成功即删（删恒在加后）。
- P7-5 番号从 qbt tag 取（`%I`→查 DB）；文件名解析不实现。
- P7-6 同 movie 已有版本 → poll 不再对该 movie 查源。

**8 qbt 下载/回调** —— 裁决：#5.4 + #6.1。
- P8-1 下载完成 → autorun curl 打 `GET /api/hooks/qbt?ih=`，服务回 200，按 §6.1 三条件推进 processing；checking/moving 等下轮。
- P8-2 非本项目 ih 回调 → 200 静默 no-op，无任何写。
- P8-3 webhook 丢失 → reconcile 5m 扫 `mdc:in-progress` 补推进（双通道闭环）。
- P8-4 撞车（同 infohash 无项目 tag）→ 收编：补 tag+cate、savepath 不动、「下载开始」照发。

**9 刮削** —— 裁决：#7 全章。
- P9-1 固定归属 + 字段级回退按 §7.1；`provenance` 逐字段、`raw_snapshot` 逐源在位。
- P9-2 元数据已存在的任务来新版本 → 0 次网络，只硬链视频文件（复用判据 = 元数据存在，与标签无关；AI 翻译的补译请求不计入「取数」，见 §5.7）。
- P9-3 NFO/图片/布局全按 §7.1–7.2（sorttitle=纯番号、tag 不写 genre、右裁 2:3、原子写、hash 跳过）。
- P9-4 入库成功 → tag 换 `mdc:finished`；失败不新增第三 tag。
- P9-5 三次重试耗尽 → `failed(phase,reason)` 驻留，Telegram ×1，需人工区可见。

**10 任务/版本模型** —— 裁决：#3。
- P10-1 task 主键 movie_id、version 主键 infohash；中文/破解 = 新版本行（`label`），聚合状态正确（任一 processing → processing…）。
- P10-2 M-Team 降级产生的两条版本各自独立走路、互不干扰。
- P10-3 `task_state` 是视图；改版本状态后聚合即时正确。

**11 Jellyfin 刷新** —— 裁决：#5.5。
- P11-1 入库后目录级 rescan 只对存在的目录调；同任务多版本 coalesce 成一条待办。
- P11-2 被拒 → 降级全库刷新；2xx 仅记「已受理」不当成功证据；两次失败 → warning + side_effect 重试。
- P11-3 刷新失败不阻塞任务 finished、不阻塞通知。

**12 Telegram** —— 裁决：#5.6。
- P12-1 四事件各自开关生效（关掉即不发），默认全开；收编也发「下载开始」。
- P12-2 模板照 §5.6；用户内容 HTML 转义；无 sendPhoto。
- P12-3 瞬时错退避 5 次驻留；永久错立即驻留；驻留出现在需人工区、可逐条重发；集成绝不自动禁用。
- P12-4 告警去重：同事件只发一次（`(event,infohash)` / task_event 判据）。

**13 手动重试** —— 裁决：#6.1。
- P13-1 抽屉「重试」：非 finished 版本回 processing 开头重跑（幂等）。
- P13-2 `failed(下载阶段)` 重试按存的 payload 重加种；qbt 仍在下载的永不重下。
- P13-3 「强制重刮」（force）才重刮已 finished 版本，重刮前 raw_snapshot 对比可查。

**14 VR cd1/cd2** —— 裁决：分片不是版本（#7）+ §7.2 细则。
- P14-1 同一种子多视频 → 全显式 `-cdN`（含 cd1），`is_primary`=cd1，Jellyfin 正常识别。
- P14-2 单片版本裸名 `{番号}.ext`；多片无序号 → 自然排序赋序。

**15 记录演员** —— 裁决：#4.1 actor 表。
- P15-1 刮削落 `actor`（name/gender/javdb_actor_id/avatar_url）+ `task_actor` 多对多；NFO 女优排前全写。
- P15-2 不抓演员详情页（v1 边界）。

**16 轮询登录态** —— 裁决：修正。
- P16-1 轮询读清单项不需要登录写侧（token 在场则携带；匿名读为文档化回退）；删清单项才用 token。
- P16-2 绝不自动重登（单会话踢号防护）；token 失效 → permanent 指标 + 需人工，不自动恢复。

**附注（PLAN 尾部想法）**：想看中文 → out of scope（§12）；mihomo 代理 → out of scope（DMM 日本出口由集群侧覆盖）；参考仓/前端选型 → §2 常设决策；「网页操作不走 yaml」→ ADR-0002 全覆盖。

## 12. 明确不做（out of scope）

（来源：地图 Out of scope）

- 想看中文清单语义（PLAN 自身 v2 递延：有磁力就下、有中字磁力才删）。
- 女优信息刮削（PLAN 15 后续；v1 只落 actor 基础数据）。
- 代理/路由管理（v1 完全不管代理；DMM 日本出口由路由器 mihomo 保证）。
- 文件名解析番号（v1 只从 qbt tag 取；失败期解析明确不实现）。
- mdcng 切换/runbook（除 oh-my-mdc 自身 home-ops 清单外）。
- 另：面板不看刮削字段与 provenance（#11）；无摘要聚合通知、无 Telegram 收侧（#13）；无多架构镜像、无仓内 Renovate/k8s 清单（#12）；无应用层鉴权（地图）。

## 13. 已知风险与实现期探针

不是未决决策，是实现时按此行事的已知事实：

- **DMM pl/ps 几何未实测**：算法已兜底（§5.3 取宽>高者为横版）；首次 JP 出口刮削时一次 `curl + file` 核对并记录。
- **DMM 全主机非日本出口 TLS 被阻**：生产 mihomo 覆盖；开发机不可达 → 开发验证需 JP 出口（CI 不做 DMM 集成测试，用 fixture：mdc-lite 2026-10-29 真实捕获在库）。
- **M-Team 限速是「建议值」且配额不可读**：只在新磁力 movie 时搜索（§5.2），失败即降级磁力，不做配额推断。
- **JavDB `jdsignature` 常量可轮换**：`InvalidSignature` = 修代码不是修凭据；app-API 移植自带。
- **清单写接口陷阱**：`name` 字段字面量代码常量（未知值默认 remove）；form-encoded；同名拒/4–30 字符（§5.1）。
- **DMM GraphQL schema 漂移**：raw_snapshot 留底，字段缺失按字段级回退处理，不失败任务。
- **单会话 token 踢号**：绝不自动重登；共享手机 token 或专用账号。

## 附录：ADR 索引

`docs/adr/`：0001 Postgres-only；0002 设置在 DB、env 只 bootstrap；0003 两级聚合、DB 为真相；0004 先落库再加种；0005 side_effect 出箱表；0006 任务级状态用视图；0007 autorun 以 sh -c 共存；0008 固定目录布局无模板；0009 已初始化看配置就位、不看测试通过。
