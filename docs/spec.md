# oh-my-mdc v1 规格

> 本文是 wayfinder 地图 [oh-my-mdc v1 — the way to a build-ready spec](https://github.com/2017fighting/oh-my-mdc/issues/1) 的终点产物：把 PLAN.md 的原始想法与 14 张已闭票据的裁决装配成一份**可直接 `/to-tickets` 的构建规格**。每节标注裁决来源（票号）；无标注处为本装配票（[#14](https://github.com/2017fighting/oh-my-mdc/issues/14)）落定的细节。领域词汇以 [GLOSSARY.md](../GLOSSARY.md) 为准；架构级取舍见 `docs/adr/`。

## 1. 产品概述

把 JavDB 自建清单里的番号，自动变成 Jellyfin 媒体库里的条目：

```
JavDB 想看清单（按 id 轮询）／女优订阅（按水位线追新）   ← 两个待办来源
  → 磁力/种子定源（M-Team 优先，JavDB 磁力兜底）
  → qBittorrent 下载（tag 标记，autorun 回调 + 对账双通道）
  → 刮削（DMM 图片/日文元数据 + JavDB 演员/其余 + 可选 AI 翻译）
  → 硬链接整理 + NFO/图片落盘（一番号一目录）
  → Jellyfin 刷新 + Telegram 通知（旁路副作用，独立重试）
```

Go 单二进制内嵌前端（Vite + React Router SPA + Tailwind + shadcn/ui），全中文界面，单用户自托管，无应用层鉴权（暴露策略在网关层）。状态全存 Postgres；循环为 poll / reconcile / sidefx / subscription 四循环 + webhook 即时通道。

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

- **`task`**：`movie_id text PK`、`number text NOT NULL`（番号）、`list_id text NULL REFERENCES list_registry`（首次发现来源；清单 id 是字符串，见 §4.3。**可空**：手工登记与补录的首次发现不是任何清单，空串冒充 id 过不了外键，写死想看清单是往「首次发现来源」里塞假话，00012 放宽）、`subscription_id bigint NULL REFERENCES subscription`（首次发现来源的另一半：女优订阅发现的作品，00015；与 `list_id` 并存、同为「首次发现」语义，写入后不覆盖；两列可以同时有值）、刮削强类型列（清单见 §7.1：`title_ja`、`title_zh`、`outline`、`outline_zh`、`release_date date`、`runtime_minutes int`、`studio text`、`publisher text`、`series text`、`director text`、`trailer_url text`、`rating numeric`、`tags text[]`）、`provenance jsonb`、`raw_snapshot jsonb`、`missing_faces text[] NOT NULL DEFAULT '{}'`（待补齐标注，#42，迁移 00013）、`created_at`、`updated_at`。
- **`task_version`**：`infohash text PK`、`movie_id FK`、`source text CHECK ('javdb_magnet','mteam_torrent','manual','backfill')`（00012 放行 manual、00014 放行 backfill）、`delivery text CHECK ('magnet','torrent','none')`（00014 放行 none：补录零下载不过 qBittorrent，无交付形态；保持 NOT NULL 的诚实，读方按字面量理解）、`label text`（普通/中文/破解，best-effort）、`status text CHECK ('downloading','processing','finished','failed')`、`error_phase text CHECK ('downloading','scrape','organize')`、`error_reason text`、`claimed_at timestamptz`、`claimed_by text`、`attempts int NOT NULL DEFAULT 0`、`next_attempt_at timestamptz`、`add_pending bool`、`list_removal_pending bool`、`force_rescrape bool`（强制重刮：下一次取数不复用元数据，#31）、`payload text`（magnet 串或 .torrent 备份路径；补录版本恒空串，无交付材料）、`created_at`、`updated_at`。
- **`version_file`**：`id bigint identity PK`、`infohash FK`、`rel_path`、`size_bytes`、`part_index`、`is_primary bool`。（入库幂等与「已落盘」判定的永久记录。）
- **`task_event`**（append-only）：`id identity PK`、`movie_id`、`infohash NULL`、`kind text CHECK ('state_change','side_effect','alert','manual_retry')`、`from_state`、`to_state`、`phase`、`reason`、`detail jsonb`（结构化附注；强制重刮把旧 `raw_snapshot` 存这里做对比基准，#31）、`at timestamptz`。承担告警「只发一次」判据、面板历史、失败追问。
- **`actor`**：`javdb_actor_id text PK`、`name`、`gender NULL`、`avatar_url NULL`、`updated_at`；**`task_actor (movie_id, javdb_actor_id, role)`** 多对多。刮削时随 JavDB 数据全量落行（含性别）；v1 不抓演员详情页（PLAN 15 就位）。

### 4.2 副作用出箱

- **`side_effect`**：`id identity PK`、`kind text CHECK ('list_removal','jellyfin_refresh','notify')`、`target_key text`、`payload jsonb`、`next_attempt_at timestamptz`、`attempts int`、`last_error text`、`done_at timestamptz NULL`；**部分唯一索引** `UNIQUE(kind, target_key) WHERE done_at IS NULL`（#8 修订：同片再进清单可再删/再发，不被「已完成」永久封条）。`target_key`：list_removal = `"<list_id>:<movie_id>"`、jellyfin_refresh = `movie_id`、notify = `"<event>:<infohash>"`。
- **驻留**（需人工）定义：`done_at IS NULL AND next_attempt_at IS NULL`（重试耗尽或永久错误）。jellyfin_refresh 靠唯一索引天然 coalesce。
- **视图 `task_state`**：按 §3 优先级实时聚合。

### 4.3 设置域（ADR-0002）

- **`list_registry`**：`list_id text PK`（JavDB 清单 id，判定永远靠 id；**是字符串不是整数**——#16 实测清单 id 为 6 字符 nanoid，如 `e8r1OM`，建清单响应的 `data.id` 也是字符串，见 §5.1）、`purpose text CHECK ('want_watch','want_watch_zh') UNIQUE`、`name text`（快照，仅展示）、`created_at`。
- **六张集成表**，统一契约：**行存在＝已配置**；可选表（mteam / ai_translation / telegram）另带 `enabled bool`；每表统一 `last_test_at timestamptz`、`last_test_ok bool`、`last_test_error text`、`last_test_list_id text DEFAULT ''`（只有 javdb 会写：测试实际读到的登记清单 id，门禁「清单验读」的凭据；00008；测试失败不清旧值）：
  - `javdb`：`token`（敏感）、`last_test_*`。
  - `qbittorrent`：`url`、`username`、`password`（敏感）、`category text DEFAULT 'mdc'`、`download_root text`、`webhook_base_url text`、`last_test_*`。（#9 裁决：webhook base URL / 下载根目录 / category 落 qbt 表；#8 的 `app_config.base_url` 因此作废。）
  - `jellyfin`：`url`、`api_key`（敏感）、`media_root`（媒体库根目录；#22 从 `app_config` 搬来——三容器同路径，刷新与硬链接共用）、`last_test_*`。
  - `mteam`：`api_key`（敏感）、`enabled`、`last_test_*`。
  - `ai_translation`：`base_url`、`api_key`（敏感）、`model`、`enabled`、`last_test_*`。
  - `telegram`：`bot_token`（敏感）、`chat_id`、`enabled`、**`notify_download_started bool DEFAULT true`、`notify_filed bool DEFAULT true`、`notify_processing_failed bool DEFAULT true`、`notify_download_stalled bool DEFAULT true`**、`last_test_*`（#13 列名落定）。
- **`app_config`**（单行 `id=1`）：`poll_interval DEFAULT 15m`、`reconcile_interval DEFAULT 5m`、`sidefx_interval DEFAULT 1m`、`max_attempts DEFAULT 3`、`subscription_interval DEFAULT 15m`、`subscription_max_adds DEFAULT 10`（00015，GLOSSARY「订阅」的第四个循环与它的每轮限量）、`paused bool DEFAULT false`、`updated_at`。程序自己写的时间戳不进本表（#8 修订；`last_test_*` 在集成表，不受此限）。
- **env 只有 bootstrap**：`MDC_DATABASE_URL`（默认 `postgres://mdc:mdc@localhost:5432/mdc?sslmode=disable`）、可选 `MDC_DB_PASSWORD`（进程内拼进 DSN，prod 形态见 §10）、`MDC_LISTEN`（默认 `:8080`）。~~`MDC_DATA_DIR` 作废~~（#12：无应用数据目录）。用户设置零 env 覆盖路径。

### 4.4 索引与语义要点

- 索引：`task_version(status, next_attempt_at)`、`task_version(claimed_at)`、`side_effect(kind, next_attempt_at)`、`task_event(movie_id, id DESC)`。
- **claim**（版本互斥，#7+#8）：`UPDATE task_version SET claimed_at=now(), claimed_by=$2 WHERE infohash=$1 AND (claimed_at IS NULL OR claimed_at < now()-interval '10 minutes') RETURNING ...`，完事清零；配哨兵错误 `ErrNoPendingTask`（本轮没活）/ `ErrClaimLost`（抢输别当失败），进程内再加 per-version 锁。
- **循环单飞**：poll/reconcile/sidefx 各持一把 `pg_advisory_lock`（专用连接），防 Flux 滚动更新时新旧 pod 重叠；版本级互斥仍归 claim。
- **启动顺序**：迁移（goose Up，advisory lock）→ 读设置 → 六判据齐（JavDB token 已保存、清单登记且验读过——javdb.last_test_list_id 记着登记的 id、qBittorrent 可连、Jellyfin 可写＝`url`+`api_key`+`media_root` 齐且目录在本机可见）才算 `initialized`，三循环才启动；**迁移失败不 abort 启动**：页面能看、`/readyz` 回 `503 db_unavailable` 带原因、循环不跑（#8 修订）。
- 敏感列：代码常量清单（表.列）驱动掩码与日志脱敏，配一条反射单测——新增 string 列未登记即测试失败。DB 明文（信任边界＝CNPG 与其备份）。
- 历史永久保留，无 retention；UI 单条删除只删记录不碰媒体（判重依赖版本行存在，不做自动清理）。

### 4.5 订阅域（ADR-0015，迁移 00015）

- **`subscription`**：`id bigint identity PK`、`kind text CHECK ('actress') NOT NULL DEFAULT 'actress'`（将来加番号/全站来源就加在这里，不再起第三张表）、`actress_actor_id text NOT NULL REFERENCES actor (javdb_actor_id)`（外键立得起来的前提是建订阅时 upsert 一行 actor：`name` 取上游、`gender`/`avatar_url` 先 NULL 等刮削补）、`watermark_date date NULL`（上一轮见过的最晚发行日；新订阅 = 建订阅当天，且封顶到今天）、`floor_date date NOT NULL DEFAULT CURRENT_DATE`（起追日：这条订阅**永不看她这一天之前**的作品，建订阅当天定下，只有回灌能把它往前挪）、`lookback_days int NOT NULL DEFAULT 30`、`backfill_from date NULL`（回灌设置的取数下界，窗口扫完清 NULL）、`enabled bool NOT NULL DEFAULT true`、`created_at`、`updated_at`，`UNIQUE (kind, actress_actor_id)`（同一位女优不会订两遍）。**行永不删除**：面板只有停用/恢复，`task.subscription_id` 的外键与历史因此永远可解。
- **`subscription_skip`**（GLOSSARY「跳过」，00016）：`id bigint identity PK`、`subscription_id bigint NOT NULL REFERENCES subscription`、`movie_id text`、`number text`、`title text DEFAULT ''`、`release_date date NULL`、`reason text CHECK ('not_single','compilation','long_form')`（三条判据，与 §9 的 `reason` 标签同一套词）、`skipped_at timestamptz DEFAULT now()`、`accepted_at timestamptz NULL`；**部分唯一索引** `UNIQUE (subscription_id, movie_id) WHERE accepted_at IS NULL`（回看窗口会让同一部作品每轮再现，落行必须幂等）。超过 30 天**没被收下**的行由订阅循环清掉；已收下的行留着——它既是「用户裁决过这一部」的记录，也是同一部作品不再被重新排一遍的依据。
- **`actor` 词义扩宽**：从「刮削沉淀的演员」扩为「已知的演员」——订阅先建行、刮削再补 `gender`/`avatar_url`；`task_actor` 与 NFO 的「女优排前」不受影响（依赖 `gender`，未知时按中性顺序）。

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
| 磁力候选 | `GET /api/v1/movies/{movie_id}/magnets` | `hash/cnsub/size(MiB)/hd/created_at`；**匿名已不可读**：2026-11 实测匿名 `JWTVerificationError`（§13），token 在场可用 |

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

### 6.2 五个触发通道

| 通道 | 默认间隔（设置项） | 职责 |
|---|---|---|
| poll | 15m | 清单全量扫（空页即止）→ 磁力 → 定源 → 建版本 → 加种 → 置待删 |
| subscription | 15m | 女优订阅追新（§6.4）：水位线 + 回看窗口取数 → 筛子 → 节流入队；与 poll 共用同一段 ingest。独立 advisory lock（`4230004`）与独立节拍：一位女优的接口挂掉不拖死清单轮询 |
| reconcile | 5m | 扫 `mdc:in-progress` 补 webhook 漏；推进 processing；判定下载异常/停滞 |
| sidefx | 1m | 删清单项 / 刷 Jellyfin / 发 Telegram，各自重试（瞬时退避、永久驻留） |
| webhook | 即时 | 单版本推进（`GET /api/hooks/qbt?ih=`） |

### 6.3 补录（#43，GLOSSARY「补录」，迁移 00014）

把媒体库里已落盘、不归本项目的旧目录收进任务域。面板常驻「未补录目录」列表，一键补录后由现成流水线接手，本节只落补录自己的规则：

- **扫描**：实时扫 `media_root` 下的一级目录 ∧ 任务表无此番号（每次面板读取现扫现比，不落快照）；隐藏目录（`.` 开头）与没有视频的目录不算。列表与任务表实时一致：任务一落，目录从列表里消失。
- **建任务**：番号即任务主键（`movie_id = number`，`list_id NULL`）。JavDB App API 没有番号→movie_id 的检索端点，番号是补录唯一拿得到的身份；因此**取数只问 DMM**（按番号），JavDB 详情跳过——拿番号当 movie_id 调详情只会得到 404 类协议错被当永久错。演员/厂牌/发行日期等 JavDB 侧面因此缺席（§7.1 字段图不扩 DMM 兑底，#42 裁决），照常入库；其中**演员面是结构性永缺**：不取 JavDB 就永远没有演员，强刮也变不出来，计入待补齐则「一键强刮摘标」（#42）永远摘不掉——待补齐判据对补录任务跳过演员面（无图/无简介/缺中文标题三面照旧）；将来加了番号→movie_id 解析（JavDB 详情可查）即可放开这条豁免。
- **登记版本**：目录里每个现有视频一条版本，`infohash = sha256(番号 + rel_path)`（确定性合成，重跑认得出同一条视频）；`source='backfill'`、`delivery='none'`、`payload=''`、`add_pending=false`（poll 不得把它捡去加种）、`status='processing'` + `force_rescrape=true`——「立即强制重刮」就是登记本身，下一轮对账自动取数重入库，无需再点。label 沿 §7.2 文件名分词（中文/破解/cdN；词表外后缀不猜），分片账（`version_file`）在登记时一次落好。
- **重入库**：整理段对 `source='backfill'` 的版本不问 qBittorrent（不回读、不硬链、不换 tag、分片账不重算），只按取好的元数据重写 movie.nfo 与图片，然后走与普通入库同一条收尾：置刷库/「入库完成」通知待办、落 finished、标待补齐（演员面豁免，见「建任务」）。「下载开始」不发——无下载。目录不在盘上是永久错（重试变不出一个目录）。
- **删旧 NFO**：登记成功后当场删目录里 `{番号}*.nfo`（movie.nfo 除外，词表外的 NFO 不碰）。依据：Jellyfin ≥10.9 读 movie.nfo 优先（jellyfin/jellyfin#10339，生产 10.11.11），旧件是死重；用户裁决直接删、不归档。顺序是**登记→删**（登记在先）：先删后登记的崩溃窗口是「旧件已删、任务未建」——目录从「未补录」里消失却没人接管；登记失败时旧件原地不动，崩在登记与删除之间也只是留下无害死重（movie.nfo 优先，旧件不干扰读数），不会造成状态错。
- **碰撞与幂等**：番号已有任务（轮询/手工/上次补录）→ 幂等放行（outcome=duplicate），零写入零删文件；两目录同番号在「目录名即番号」的口径下不存在。重复点击不重复建任务、不重复建版本、不重复删文件。
- **门禁**：未初始化/已暂停时扫描与补录都拒（`gate_closed`）——与手工登记同一口径。

### 6.4 女优订阅（ADR-0014，§4.5，迁移 00015）

第二个待办来源。它只负责「产出一批待办作品」，之后与清单轮询走同一段 ingest（定源 → 先落库 → 加种），所以本节只写订阅自己的规则。

- **名单与身份**：订阅身份是 `actor.javdb_actor_id`（不是名字——名字只是快照，改名不该断订阅）。面板订阅区两种添加方式：**贴 id / 女优页链接**（上游没有按名字搜女优的端点，§13 实测）；或从 App 收藏的女优列表（`GET /api/v1/users/collected_actors`，需 token）勾选导入。同 id 重复添加幂等放行。
- **取数**：`GET /api/v1/movies/tags?filter_by=0:a:{id}&sort_by=release&order_by=desc&page=N`，匿名可读；掩码按上游段规则构造、只写到合法段（**第 5 段非空且后面还有段 = 上游静默丢年份**那类坑，照 javdb_rss 的实测记录处理）。空页即止。**不带 `s`**：§13 实测 `s` 是「只留恰好一位女优出演」的准确判据，但它在上游**静默**地排——被排掉的作品进不了跳过表，也就没有「收下」这个出口。筛子因此整体下沉到详情层（ADR-0017），取数只取全集；`c`/`tags`/`year` 原样透传一律不搬。
- **筛子**（ADR-0017，§13 探针结论）：全集统一拉一次 `/api/v4/movies/{id}`，三条判据各自落 `subscription_skip`——`actors` 数 > 1 → `not_single`（多人一起的）；类別「精選綜合」→ `compilation`；类別「4 小時以上作品」/「16 小時以上作品」→ `long_form`（她自己的多部合集，见 §13 的漏网实测）。标签 id 不硬编码：按名字去 `GET /api/v2/tags?type=0` 的词表里查，进程内缓存，重启重取（该端点 2026-11 起要 token，§13）。
- **水位线、起追日与回看**（GLOSSARY「水位线 / 起追日 / 回看窗口 / 回灌」）：取数下界 = `GREATEST(floor_date, LEAST(watermark_date − lookback_days, COALESCE(backfill_from, +∞)))`。一轮跑完把 `watermark_date` 推成「这轮见过的最晚 `release_date`」并**封顶到今天**（`LEAST(GREATEST(COALESCE(watermark_date, today), newest), today)`，理由见 §13 的预告片）。
  - **起追日**（`floor_date`）是最外层那一层，也是「新订阅不追历史」的机制本身：建订阅当天定下，此后永不自动回看更早的作品；只有**回灌**能把它往前挪，且挪了就不退回去。代价要说清：**一部比她订阅更早发行、后来才出水（拿到磁力）的作品永远不会被捡起**，除非点一次回灌——「出水晚」不再等于「总会被捡到」。
  - **回看窗口**（`lookback_days`，默认 30）只在起追日之**后**那一段里回看：它让「出水时还没磁力」的作品下一轮仍被看见（磁力为空不落行那条规则的配套），不是补历史的手段。
  - **回灌** = 把 `floor_date` 与 `backfill_from` 一并往回拨 N 天（`floor_date = LEAST(floor_date, COALESCE(watermark_date, CURRENT_DATE) − N)`，`backfill_from` 设为同一天），`all` 则是 `1970-01-01`（＝没有下界）。回灌窗口扫完只清 `backfill_from`，起追日留在原处。
  - **水位线只由「见过」推进，不由「入队成功」推进**：出水即无磁力的作品靠回看窗口下轮重见，与 §6.1「磁力为空不落行、下轮重查」是同一条承诺。
- **节流与去重**：每轮最多建 `subscription_max_adds`（默认 10）个任务，余量留到下一轮且 `backfill_from` 不被清；已有任务的 `movie_id` 不再查源（§6.1 去重行，跨来源白送）——同一部作品既在清单又在某位女优的新作里，只会有一条任务。限量只管「订阅再往下产、入队」，不挡清单轮询与已建任务的推进。
  - **范围**：这个预算是**一轮所有订阅共享**的，不是每条订阅各一份（按条给预算，场上一有 N 位女优就变成 N 倍，限量就白设了）。
  - **顺序**：按发行日**从旧到新**（同一天按 `movie_id`），跨订阅统一排队。下界（水位线 − 回看窗口 / 回灌下界）只会往前挪，离它最近的先做，才不会被余量轮挤到窗口外。
  - **余量不落状态**：留在门外的作品什么都不写，下一轮的窗口原样再取一遍；`backfill_from` 只在「窗口扫完 **且** 这条订阅没有余量」时才清——提前清会把下界跳回水位线 − 回看窗口，被留下的老作品当场掉出窗口。
  - **预算数的是建出来的任务**，不是递进 ingest 的作品：窗口里本来就看得到上一轮建过的作品（它们零写入），「磁力为空不落行、下轮重查」的那些也不该占预算——否则每轮都被没有磁力的老作品吃光，窗口就永远排不空。
  - **先筛后排队**：筛子（ADR-0017）在入队之**前**——被排掉的作品根本不进这一轮的队列，既不建任务也不占预算。详情读不到的那些也不算「留在门外」（它们没被看过，只是被重新递一遍），下一轮原样重取。
  - **收下不排在预算后面**：`收下` 是人的显式裁决（与 `POST /api/subscriptions/{id}/skips/{skip_id}/accept` 同一条 ingest），所以循环里万一撞见一条已被收下的跳过，也照常入队，不因为余量走完而留到下一轮，也不占别人的名额。
- **来源记录**：订阅建的任务 `list_id` 为 NULL（没有清单项可删，故 list_removal 天然不触发），`subscription_id` 记这一行（§4.1）。
- **门禁**：同 poll（三件套齐 + 未暂停）；女优订阅**不进** initialized 判据——一条订阅都没有时循环空转即返回。
- **生命周期**：只有停用/恢复，没有删除；停用只停追新，已建任务照走完，跳过行按 30 天自然过期。
- **跳过与收下**（GLOSSARY「跳过 / 收下」）：被排掉的落 `subscription_skip`，面板订阅区可见并一键「收下」转成正常待办——判据可以错，出口必须留。

## 7. 刮削与整理

（来源：#10；字段映射全表与分片细则是本票装配落定）

### 7.1 字段映射全表

固定归属 + 字段级回退（唯一回退方向 DMM→JavDB；JavDB 字段缺失留空不回退；**不做 DMM↔JavDB 双向字段兑底**——#42 裁决：上游挂了的解法是重试，不是拿另一边的字段凑）：

**刮削韧性（#42）**：取数两源各取各的，一个源挂了不挡另一个源。瞬时失败（网络/超时/5xx/地区阻断）按指数退避跨对账循环重试 **5 轮**（1 分钟起、翻倍、封顶 30 分钟，与副作用通知同一节奏；不跟 `max_attempts` 走）；期间版本留 processing、循环健康位不变红（上游不可达不算循环失败，ADR-0011 口径，可达性归 §9 上游指标）。五轮耗尽照常入库不阻塞——按手头已有的源落库，并按四面判据标**待补齐**（GLOSSARY）：无任何图 / 无演员 / 无简介 / 翻译启用却无中文标题，任一命中即算；个别 tag 缺不算。永久错（凭据失效、协议不兼容）不烧预算，一次落 `failed(scrape)` 进需人工。修复动作是面板逐部「强制重刮」，四面补齐自动摘标；`missing_faces` 落 `task` 行（迁移 00013），面板聚合成区、`/metrics` 出 `mdc_task_incomplete`。图片下载的瞬时失败同节奏：poster/fanart 的网络错退避重试 5 轮，耗尽照常入库（部分成功告警不变）；裁剪失败是文件的病，不重试只告警。

| 字段（task 列） | 默认源 | 回退 | NFO 槽 |
|---|---|---|---|
| `title_ja` | DMM `ppvContent.title` | JavDB 标题 | `<originaltitle>` = 番号+日文原题；`<title>` = 番号+(译名 \| 原题) |
| `title_zh` | AI 翻译（可选） | 无（降级原题） | 同上 |
| `outline` | DMM `description` | JavDB 简介 | `<plot>` 单槽（译文或原文，outline 槽不用） |
| `outline_zh` | AI 翻译 | 无 | 同上 |
| 图片 | DMM `pl/ps` + `jp-N` | JavDB 封面/预览 | poster.jpg / fanart.jpg / extrafanart |
| 演员 | JavDB（含性别） | — | `<actor>` 全写、女优排前、组内保 JavDB 顺序 |
| `tags` | JavDB tags | — | `<tag>`（**不写 `<genre>`**，ADR-0012） |
| `publisher` | JavDB label | — | 降级为 tag「发行: X」 |
| `rating` | JavDB | — | `<rating>` |
| `studio` | JavDB maker | — | `<studio>` |
| `series` | JavDB series | — | `<set>` |
| `release_date` | JavDB | — | `premiered` + `releasedate` **双写** |
| `runtime_minutes` | JavDB | — | `<runtime>` |
| `director` | JavDB | — | `<director>` |
| `trailer_url` | JavDB | — | `<trailer>`（有则写） |

**NFO 约定**（照搬 open-mdcng 实测 + 标题槽改造）：固定名 `movie.nfo`（多片目录里番号命名不会被读）；原子写 temp+rename；内容 hash 同则不重写（免无谓 rescan）；**`<sorttitle>` = 纯番号**；不写 uniqueid/dateadded/lockedfields/watched/playcount；数字零值省略；多值重复元素不拼接。

**渲染对账**（#41，GLOSSARY 同名词条）：补译与字段修订只更新 DB、不重跑 organize，盘上 `movie.nfo` 会停在旧值——对账循环每轮用库内元数据重新 Render，与盘上文件**逐槽位**比对（槽位集 = 上表 NFO 槽列 + 演员序列；期望值由 Render 产出再解析回来，保证与整理段同一标准），漂移才重写，且走同一套原子写 + 内容同不重写。Jellyfin 的 NFO writer 会改写同一文件（lockdata/dateadded/playcount/watched/resume/fileinfo、演员头像换本地缓存路径），槽位值不变——这些不算漂移，**整文件比对会两边互覆永动**；演员只比 name 序列（thumb 归 Jellyfin 本地化）。对账是兑底回流：入库与强制重刮的落盘路径照旧；空转零写盘（mtime 不变，不触发 Jellyfin rescan）；盘上文件缺失不重建（挂载抖动时批量重建只会喂假条目，重建归补录）；同番号目录归别的任务时跳过（§7.2 碰撞行等人工，对账不掺和）。渲染对账顺带**扫场代评**待补齐标注（#44）：0.3.0 前入库的存量任务 `missing_faces` 全空，对账逐任务调**同一份四面判据**（#42 的 `MissingFaces` 纯函数；dir＝`media_root/{番号}`、meta＝库值、演员数＝task_actor 行数、译文开关与排版同源），算出的定态与库中标注不符才写（相同零写库）；补录任务的演员面豁免与入库处同一条规则（§6.3）。入库时的评估照旧是主时刻（强刮摘标的时机不变），扫场代评是兜底回流；目录不在盘上（挂载抖动）没法判「无任何图」，跳过不写。

**图片落盘**：`poster.jpg`（横版 sleeve 右裁 2:3，DMM 缺则 JavDB 封面右裁）、`fanart.jpg`（原横版）、`extrafanart/fanart{N}.jpg`（jp-N 预览）；扩展名随源 URL 不转格式；预览收缩时清理旧 fanartN。`<actor><thumb>` 直写 JavDB URL（Jellyfin 自拉），v1 不下载头像。

### 7.2 目录布局（ADR-0008）

- 固定 `{media_root}/{番号}/{番号}[-中文|-破解][-cdN]{扩展名}`，**v1 无模板**（词表 v1 仅「中文/破解」二词，扩词只加词不改布局）。
- **多版本同目录**共享一份 movie.nfo + 图片 → 复用元数据 = **只硬链新视频文件**，零拷贝零改写。
- **分片细则**（本票落定）：视频文件判定 = 扩展名 ∈ {mp4,mkv,avi,wmv,mov,m2ts,ts,rmvb,flv,webm,mpg,mpeg}；分片序号 = stem 匹配 `[ _.\-]?(?:cd|disc|disk|part)[ _.\-]?(\d{1,2})`（大小写不敏感）；**多片全显式含 cd1**，单片版本裸名；多片但无序号 → 自然排序赋顺序号；`is_primary` = cd1/最前。字幕扩展名 {srt,ass,ssa,vtt,sub,idx,sup} 随对应视频同名硬链；同视频多字幕撞名时保留源文件语言后缀。
- 非视频文件（txt/广告图/种子自带 nfo）不落盘（视频+字幕白名单）。
- **硬链模式唯一**（无 move/copy/symlink 配置面）；**EXDEV = 任务失败**并明示换挂载，绝不静默降级 copy。
- **碰撞**：同番号两条无标签版本（不同 infohash 都指向 `{番号}.ext`）→ 后到者失败驻留等人工；同番号不同 movie_id 的两任务相撞同样失败等人工。不覆盖、不自动 -v2（宁噪勿静默丢数据）。
- AI 翻译失败 → title 降级日文原题照常入库 + task_event 告警，不阻塞；若 AI 翻译已启用，缺中文标题计入待补齐四面判据（#42，标见 §7.1 刮削韧性），补译补齐时单面摘标。

## 8. Web UI 与 REST 契约

（来源：#11 变体 A 全取 + #8/#13 装配期遗留；REST 形状本票落定）

### 8.1 向导（分步，一屏一步）

左侧竖向步骤条，顺序：**JavDB 账号与清单 → qBittorrent → Jellyfin → M-Team（可选）→ AI 翻译（可选）→ Telegram（可选）→ 完成（间隔与重试在最后一步）**。可选步可跳过。每步独立「保存本步」＝按表 PATCH；敏感字段只吐掩码、省略或空串＝保持原值；并发编辑靠 `revision` 指纹提示冲突；每步一个「测试连接/测试读取/测试可写」按钮，结果 chip 写在按钮旁（写 `last_test_*`）；编辑或保存（未重测）把 chip 收回「还没测试」，不拿旧结论代表新配置；javdb 的绿标深度（「含清单验读 / 只验了 token」）。

- **JavDB 步**：token（掩码）+ 清单按 id 登记——读取已有清单优先（探测列表，按 id/名字匹配「想看清单」），新建 = `POST /api/v1/lists` 探测 + **手动粘贴 id 兜底**（#15/#16 流程，名字 4–30 规则见 §5.1）。
- **qBittorrent 步**：地址/账号（可选）/category/下载根目录/回调 base URL（集群内 svc DNS）+ **autorun 改写前后完整对比**，保存前再确认一次（§5.4）。
- **Jellyfin 步**：地址 + api key + `media_root`（三者同一步一次保存；「测试可写」＝鉴权 + 目录本机可见）。
- **M-Team / AI 翻译 / Telegram 步**：凭据 + enabled（+ Telegram 四勾选框；测试 = getMe → 测试消息）。
- 定位：**配完就不碰**——再配置走侧边栏「集成设置」回同一向导（编辑态），不做两套界面。

### 8.2 面板（表格为主体，要动手的事置顶）

区块顺序：统计行（下载中/加工中；已入库降级到右端与新鲜度同处）→ **「需要人工」告警区置顶**（`failed(phase,reason)` + 停滞 + 驻留副作用，行内重试/重发按钮）→ **「待补齐」区**（#42：已入库但四面判据有缺口的任务聚合成区，逐部一键强制重刮，四面补齐自动摘标；不是状态、不算需人工）→ **「未补录目录」区**（#43：媒体库里不归本项目的旧目录逐行一键补录，空区不渲染，GLOSSARY「补录」）→ **「订阅」区**（只列已订阅的女优——上游没有搜名字的端点，所以没有搜索框，添加入口是「贴 id / 女优页链接」与「从 App 收藏导入」；每行 actor 快照、水位线、带来 N 部、启停、回灌；区内的「最近被排掉的」折叠格带计数徽标与逐行「收下」）→ 稠密任务表（一行一任务按 movie_id 聚合，版本以小 chip 展示）→ 底部副作用队列 + 循环健康 + 最近事件。停滞行按最近一条停滞告警认；人工重试重新加种之后那条旧告警退场（#31）。

任务详情 = **右侧抽屉**，保持精简：版本列表（infohash、来源、标签、失败原因）+ 事件时间线 + 重试/强制重刮（force）。**不看刮削字段与 provenance**（v1 不做，不留占位）。

### 8.3 门禁与文案

未初始化（门禁不齐）＝整站只剩向导，面板入口禁用 + 一张解释卡。文案照原型（`prototype/wizard-dashboard` 分支）：

- 解释卡：「主线三件套齐了，循环才会启动：JavDB 可读、清单登记并验读过、qBittorrent 可连、Jellyfin 可写」；Jellyfin 一行另列「媒体库目录在盘可见」（`media_root_visible`），未初始化时一眼看出是哪一半没到。门禁计数行报「门禁 N/6 · 差 X」。
- 状态名「下载中/加工中/已入库/失败」（任务级与版本级同名，任务级「失败」= 全部版本失败）；「需要人工」是待办区的名字，不当状态名；按钮「重试/强制重刮/测试连接/保存本步/重发」；版本失败原因直译 phase。全部 copy zh-CN。

### 8.4 REST 契约（本票落定；全部 JSON；无应用鉴权）

**设置**（sections：`javdb|qbittorrent|jellyfin|mteam|ai_translation|telegram|app|list_registry`）：

| 端点 | 语义 |
|---|---|
| `GET /api/config` | 全部 section + `initialized` + `media_root_visible`（门禁第二半，向导解释卡用）；每节 `{revision, configured, enabled?, data}`；敏感字段呈 `{configured: bool, masked: "ab***yz"}`，原文永不回显 |
| `GET /api/config/{section}` | 单节，带 `revision`（内容指纹） |
| `PATCH /api/config/{section}` | 部分字段 + `revision`；省略＝不改，敏感字段空串也＝不改；`revision` 冲突 → `409 revision_conflict`；校验错 → `400` |
| `DELETE /api/config/{section}` | 删行＝解除配置；主线三件套被删 → `initialized` 变 false 回到向导 |
| `POST /api/config/{section}/test` | 跑测试连接，写 `last_test_*`，返回 `{ok, code, detail, duration_ms, list_id?}`（javdb 读到登记清单时带 `list_id`，是「清单验读」的凭据） |

`revision` = 该节当前内容的规范 JSON sha256 短指纹，读写往返检测并发编辑（open-mdcng 先例）。`list_registry` PATCH = `{list_id, purpose:'want_watch', name}` upsert；向导辅助端点 `GET /api/javdb/lists`（探测）、`POST /api/javdb/lists {name, description?}`（代建，同名拒原样透出）。 qBittorrent 的 autorun 共存改写同样走向导辅助端点：`GET /api/qbittorrent/autorun` 只读预览 `{before, after, skip, changed, enabled, warning}`（前后**完整**命令串），`POST /api/qbittorrent/autorun` 是人工确认后的落地，只写 `autorun_program`（ADR-0007）。

**向导辅助端点的错误**（`GET/POST /api/javdb/lists`，与下面测试错误码同一套分类）：`auth`/`business` → `400`（要用户改凭据/输入），`protocol`/`network` → `502`（服务端行为异常或网络，查或重试）；`{ok:false, error:{code, message}}`，`business` 的 `message` 原样带上游文案（如「您已創建了同名清單」）。

**测试连接错误码**（稳定机器码；超时一律 **10s**）：`timeout`、`auth`（凭据无效/被踢/API key 拒）、`network`、`protocol`（非预期响应/信封）、`business`（业务校验错，如 JavDB `success:0`+`action:null`、清单同名拒）、`token_invalid` / `chat_unavailable`（Telegram 两步测试专用）。各节测试内容：javdb = 用户域读 + 登记清单读；qbittorrent = 登录 + 版本读 + autorun 现值读（供 diff）；jellyfin = `GET /System/Info` 鉴权 + `media_root` 存在；mteam = pageSize=1 单行搜索；ai_translation = 1 token 补全；telegram = getMe → sendMessage。

**任务与运维**：

| 端点 | 语义 |
|---|---|
| `GET /api/overview` | `{initialized, paused, counts:{downloading,processing,finished,failed}, loops:{poll/reconcile/sidefx/subscription:{last_run_at,last_ok}}}`。`counts.failed` 是**全部版本都失败**的任务数（与 `?state=failed` 同一口径）；「有几件事要动手」不在这里，它是 `/api/attention` 的行数——两个口径分开报，不再合成一个 `needs_attention` |
| `GET /api/tasks?state=&q=&page=` | 任务行（`task_state` 视图 + 版本摘要 chip 数据） |
| `GET /api/tasks/{movie_id}` | 任务 + 版本列表 + 最近事件（抽屉数据） |
| `POST /api/tasks/{movie_id}/retry` | `{force: bool=false, infohashes?: [..]}`，语义照 §6.1 人工重试行；`infohashes` 省略＝整个任务（抽屉的强制重刮），给了就**只动这几条版本**（面板的行是版本组，按钮写几条就只动几条）；回执 `{results:[{infohash, action, from_state, to_state, reason, force_rescrape}]}`——逐版结果，版本侧被拒/失败不是 HTTP 错误 |
| `POST /api/manual-tasks` | 手工登记（GLOSSARY）：`{magnet, javdb_url}`；回执 `{movie_id, infohash, number, outcome, message?}`，`outcome ∈ added/adopted/already_tracked/deferred/duplicate`（deferred＝登记成功、加种由轮询接着试）。错误信封：输入形状 400 `validation_error`、番号撞车 409 `number_taken`、种子已属别的任务 409 `infohash_taken`、门禁未开 409 `gate_closed`、详情读不到 502 `upstream_error` |
| `GET /api/backfill/scan` | 未补录目录列表（GLOSSARY「补录」，#43）：`{dirs: [{number, videos, bytes}]}`，实时扫盘比对任务表；门禁未开 409 `gate_closed` |
| `POST /api/backfill` | 一键补录（GLOSSARY「补录」）：`{number}`；回执 `{movie_id, number, outcome, versions, message?}`，`outcome ∈ backfilled/duplicate`（duplicate＝番号已有任务，幂等放行零写入）。错误信封：输入形状 400 `validation_error`、门禁未开 409 `gate_closed`、目录已不在盘上 409 `dir_missing`、目录无视频 409 `no_videos` |
| `GET /api/events?movie_id=&limit=` | 事件时间线 |
| `GET /api/attention` | 「需要人工」区的任务侧两半（#26 落地 §8.2 时新增）：`{failed:[{movie_id,number,infohash,phase,reason,at}], stalled:[…], failed_truncated, stalled_truncated}`；每半上限 50 行，被截断时 `*_truncated=true`（面板据此写「50+」）。驻留副作用由下面那一条提供 |
| `GET /api/side-effects?state=pending\|stuck` | 副作用队列（stuck = §4.2 驻留） |
| `POST /api/side-effects/{id}/retry` | 人工重发（清驻留、重排 next_attempt_at） |
| `GET /api/hooks/qbt?ih=<hex>` | qbt autorun 回调；恒 200；未知 ih 静默 no-op（§5.4） |

**订阅**（§6.4，面板订阅区）：

| 端点 | 语义 |
|---|---|
| `GET /api/subscriptions` | `{actresses:[{id, actor:{javdb_actor_id,name,avatar_url,gender}, watermark_date, lookback_days, backfill_from, enabled, tasks_count, pending_count, deferred_count}]}`；`tasks_count` 数 `task.subscription_id`；`pending_count` 数这批里还在等加种的版本，`deferred_count` 是「还有 N 部待入队」（§6.4 的每轮限量，循环每轮末尾现算，不落库） |
| `POST /api/subscriptions` | `{actress_id}` 或 `{javdb_url}`：解析 id → upsert actor → 建订阅；同 id 重复 → `outcome=duplicate`（幂等 200）。错误信封：形状 400 `validation_error`、id 读不到 502 `upstream_error`、门禁未开 409 `gate_closed` |
| `POST /api/subscriptions/{id}/enable` / `disable` | 启停；停用只停追新，不碰任务与跳过行 |
| `POST /api/subscriptions/{id}/backfill` | `{days}` 或 `{all: true}`（二选一）：把起追日与回灌下界一并往回拨 `days` 天，`all` 则拨到 `1970-01-01`（＝没有下界）；回执 `{backfill_from}`。`days` 必须是**整数**，且 **`days` ≤ `lookback_days` 一律 `400 validation_error`**——回看窗口本来就每轮看那么远，那样的回灌是空操作（message 说清「回看窗口已经覆盖」） |
| `GET /api/subscriptions/{id}/skips` | 最近 30 天跳过：`{skips:[{id, movie_id, number, title, release_date, reason, skipped_at}]}` |
| `POST /api/subscriptions/{id}/skips/{skip_id}/accept` | 收下：建任务并入队（走同一 ingest），回执 `{movie_id, outcome}`；重复收下幂等 |
| `GET /api/javdb/collected-actors` | App 收藏的女优（需 token）：`{actresses:[{id, name, videos_count, gender, subscribed}]}`；`subscribed` 让导入面板把已订的置灰。**没有搜索端点**——上游不提供（§13），面板不开搜索框 |

任务事件行的 `detail` 原样回出（人工重试把旧 `raw_snapshot` 留在 `detail.raw_snapshot_before`、结局留在 `detail.action`）：对比基准只经接口可查，面板 v1 不渲染刮削字段（#31）。

错误信封统一 `{ok:false, error:{code, message}}`。SPA 路由：Go handler 把所有未匹配 URL 导向 index.html（#5）。

## 9. 可观测性（指标名本票落定）

- 端点：`/healthz` 只看进程（liveness 绝不折叠 DB/上游）；`/readyz` ping Postgres（迁移失败 → `503 db_unavailable` + 原因）；`/metrics` Prometheus；`/version`（ldflags 注入，默认 `dev`，启动日志一行）。
- 指标（`mdc_` 前缀，v1 最小集）：

| 指标 | 类型 | 标签 | 用途 |
|---|---|---|---|
| `mdc_loop_last_success_unix_seconds` | gauge | `loop∈{poll,reconcile,sidefx,subscription}` | 循环停滞判定 |
| `mdc_loop_iterations_total` | counter | `loop,result∈{ok,error}` | 循环健康 |
| `mdc_upstream_requests_total` | counter | `upstream∈{javdb,mteam,dmm,qbittorrent,jellyfin,telegram,openai},result∈{ok,transient,permanent}` | 凭据失效判定；permanent = 401/403 类（javdb JWTVerificationError、telegram 400/401/403、mteam 鉴权 code 等） |
| `mdc_side_effect_stuck` | gauge | `kind` | 驻留副作用数（喂需人工区，不做告警规则） |
| `mdc_task_incomplete` | gauge | — | 待补齐（#42：四面判据命中且上游重试耗尽）的任务数；与面板待补齐区同源 |
| `mdc_subscription_skipped_total` | counter | `reason∈{not_single,compilation,long_form}` | 筛子命中数：三条判据各一路。长期全场只有 `long_form`，说明共演与精選綜合的判据可能已失效 |
| `mdc_subscription_pending` | gauge | — | 回看窗口内已发现、被每轮限量留在门外的待入队数（每轮末尾置数；`backfill_from` 未清时为非零是预期） |

- 最小 VMRule 两条（#12）：**LoopStalled**：`time() - mdc_loop_last_success_unix_seconds{loop="poll"} > 2400`（reconcile > 900、sidefx > 600、subscription > 2400）；**UpstreamCredentialsInvalid**：`increase(mdc_upstream_requests_total{result="permanent"}[15m]) > 0`。Grafana 面板不做。

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
- **`main=s`（單體影片）已实测——它筛的是「只有她一个人」，不是「没有合集」**（#45，2026-11，12 位女优、全程匿名）：`s` 只认 `filter_by` 掩码的第 4 段（`0:a:{id}:s`；写成独立查询参数 `main=s` 被**静默忽略**，EvkJ 仍 232 部）。全集→`s` 的收缩很陡：EvkJ 232→104、Av2e 322→132、kzx6 234→108、1KBW 1260→485。差集以合集号段为主（`OFJE-*` 最多），但**凡有两位以上女优出演的作品，`s` 一律排掉**：12 位女优 42 对两两交集共 394 部「≥2 女优」的作品，通过 `s` 的 **0 部**——含真正的单体共演（`REBD-725`/`REBDB-711` REbecca STARS10，京香×桃园怜奈，192 分钟）。反过来，**她自己的 BEST/总集编留在 `s` 里**：带「精選綜合」（tag 8）却通过 `s` 的每女优 3–46 部（EvkJ 21 部：`OFJE-186` 初ベスト 6 タイトル 8 時間、`OFJE-380` 1st BEST 12title 12hour、`OFJE-416` 全 50SEX 16 時間；KxPb 14 部：`PPR-*`/`PPT-*`/`PPX-*` 8 時間 BEST；kzx6 26 部：`IDBD-887` COMPLETE BEST 48 時間 BOX）。即 `s` ＝「单女优出演」，与類別标签「單體作品」（tag 28）近似但**不逐位相同**（每女优 1–11 部双向出入：`s∖tag28` 见 `FC2-*`/`SIVR-302`，`tag28∖s` 见 `WICP-004`/`SSIS-241`）。**裁决（ADR-0016 已废弃，由 ADR-0017 取代）**：取数取全集、**不用 `s`**——`s` 排得准，但它在上游静默地排，被排掉的共演进不了跳过表，也就没有「收下」这个出口；筛子整体下沉到详情层，三条判据（`actors`>1 / 精選綜合 / 4 小时以上）各自落行。多翻几页的代价被水位线吃掉（列表按 release 倒序，见过下界即停）。仍然留着的坑：**「精選綜合」不是合集的全集**——`OFJE-665`（「…5年分…120本番12時間」）既通过 `s`、也不带 tag 8，`MKCK-223`/`ONSD-854` 这类混编合集同样不带；`long_form`（类別 4/16 小时以上）就是为收住这类自带长合集而加的；仍会漏掉的只剩「既无第二人、也不带长片标签」的少数件，而反方向的误伤（真正的四小时单体作品被当合集）可以用面板「收下」救回来。另：`tags[].videos_count` 仍不可信（1KBW 的 tag 28 声明 188 部，按 tag 筛实得 418 部）。
- **女优检索端点：不存在**（#45 实测）。`/api/v1/actors` 必须带 `type`（＝片库号 0–3，缺了报 `ParameterInvalid: 参数不能为空: type`），它是**分页名单**不是搜索：`q`/`name`/`keyword`/`search`/`term`/`query`/`actor_name` 与 `letter`/`sort_by`/`filter_by` 全被静默忽略（首页逐字节相同）；`limit` 有效（默认 10，最大 50），zone 0 的真数据到第 200 页、**第 202 页起重复返回第 200 页**（「空页即止」在这条端点上不成立），约一万位女优——本地按名字匹配要整库翻完，不能当搜索用。`/api/v1/actors/search`、`/search_by_name`、`/autocomplete` → 信封级 `ResourceNotFound`（路由不存在）；`/api/v1/search*`、`/api/v2/search/actors`、`/api/v3/search` → 404 HTML。唯一像搜索的是 `/api/v2/search`：**路由存在但匿名 `JWTVerificationError`**，无 token 无法确认参数与语义。面板加女优因此仍按「贴 id / 女优页链接」，`GET /api/javdb/actress-search` 回 501；将来拿到 token 先验这条再决定是否开搜索。
- **匿名面 2026-11 收窄**（#45 顺带实测）：`/api/v4/movies/{id}` 详情、`/api/v1/movies/{id}/magnets`、`/api/v2/tags?type=0` 词表现在**匿名一律 `JWTVerificationError`**（javdb_rss 2026-10 记的是匿名可读），仍匿名的只剩列表类（`/api/v1/movies/tags`）与 `/api/v1/actors/{id}`。生产带 token 不受影响（§5.1），但离线探针与复跑只能用列表类端点。
- **上游会把预告作品先登在列表里**（#47）：`release_date` 可以是未来某天（还没发行），而列表按 `release` 倒序正是把它们排在最前。所以「这轮见过的最晚发行日」可能落在未来——水位线一跳进未来，取数下界（水位线 − 回看窗口）就落到她全部现存作品之后，订阅会静默停追到那个日子为止。水位线因此封顶到今天。
- **JavDB `jdsignature` 常量可轮换**：`InvalidSignature` = 修代码不是修凭据；app-API 移植自带。
- **清单写接口陷阱**：`name` 字段字面量代码常量（未知值默认 remove）；form-encoded；同名拒/4–30 字符（§5.1）。
- **DMM GraphQL schema 漂移**：raw_snapshot 留底，字段缺失按字段级回退处理，不失败任务。
- **单会话 token 踢号**：绝不自动重登；共享手机 token 或专用账号。

## 附录：ADR 索引

`docs/adr/`：0001 Postgres-only；0002 设置在 DB、env 只 bootstrap；0003 两级聚合、DB 为真相；0004 先落库再加种；0005 side_effect 出箱表；0006 任务级状态用视图；0007 autorun 以 sh -c 共存；0008 固定目录布局无模板；0009 已初始化看配置就位、不看测试通过；0010 刮削字段图；0011 材料缺失回上游重取；0012 NFO 只写 `tag`；0013 双主题走 `light-dark()`；0014 女优订阅并进流水线不产 RSS；0015「订阅」上位词但两张表；0016 合集筛子两层且可收下（**已废弃**，由 0017 取代）；0017 合集筛子下沉到详情层，三条判据。
