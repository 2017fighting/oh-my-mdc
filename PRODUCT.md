# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

**只有作者本人**——单用户、自托管、私有部署（回答已确认）。一个人在自己的集群上跑，浏览器经网关访问 `mdc.raenzo.com`；同一个人既是在 JavDB App 里往「想看」清单丢番号的日常用户，也是回来处理「需要人工」的运维者。没有第二类角色、没有多租户、没有陌生访客。

## Product Purpose

把 JavDB 自建清单里的番号，自动变成 Jellyfin 媒体库里的条目：轮询清单 → 定源加下载 → 刮削 → 硬链接整理 + NFO/图片落盘 → 刷新媒体库 → 通知。

它存在是为了让「想看」变成一次性的动作：加了清单就不再管。成功意味着无人值守——番号在合理时间内以正确的目录结构、NFO 与图片出现在 Jellyfin 里，需要人出手的只剩上游确实没有资源的情况。

## Positioning

相邻工具（javdb_rss、open-mdcng、mdc-lite）各自覆盖了链路的一段；本项目的机制是一整套**可恢复的单向流水线**，单看每一条都能被抄，合起来才难被如实复制：

- **认 id 不认名字**：清单按 JavDB 的 id 登记，「想看」是绑在 id 上的用途；名字只是快照。
- **两级聚合**：任务（`movie_id`）承载作品与元数据，版本（`infohash`）各自独立走路；普通/中字/破解是不同版本，VR 的 cd1/cd2 是同版本内的分片。
- **先落库后动手**：加种、删清单项、刷新、通知之前先写下意图，任何一步中断都能接着走。
- **先加下载再删清单**：顺序不可颠倒，删失败不影响已下的东西。
- **DB 是状态真相**：qBittorrent 的 `mdc:in-progress`/`mdc:finished` tag 与 webhook 通知都只是线索，丢了靠对账补。
- **元数据三面**：值 + 逐字段 provenance + 逐源 raw snapshot；复用判据是「元数据是否已存在」，不是名称分类。
- **旁路副作用出箱**：删清单、刷 Jellyfin、发通知各自独立重试，失败不阻塞入库。
- **人工重试三分支**：回加工开头、按交付材料重新加种、强制重刮已入库版本；qBittorrent 里还在下载的版本永不重下。

## Operating Context

- **全程网页操作，不走配置文件**（PLAN 第 0 条）。用户可改的东西一律存 Postgres；环境变量只有带默认值的 bootstrap 参数（ADR-0002）。
- **三个常驻循环 + 一条即时通道**：poll（清单轮询）、reconcile（对账 qBittorrent）、sidefx（副作用重试），加 qBittorrent 下载完成 webhook。部署恒定单副本（Recreate），绝不双实例跑循环。
- **上游六件套**：JavDB App API（token）、qBittorrent（autorun 回调，须与既有 autorun 共存且幂等，ADR-0007）、Jellyfin（刷新与硬链接共用同路径）、M-Team（可选，有则由它优先出种）、OpenAI 兼容翻译（可选）、Telegram（可选，事件通知）。
- **自家集群形态**：home-ops 里的 `oh-my-mdc` ns，唯一卷是共享 media PVC（`/media`）；与 qBittorrent/gateway 同 PUID 1000 / PGID 1500，硬链接才成立；DMM 出站经路由器上的 mihomo 日本出口。
- **访问控制**：公网经 Gateway + authelia，机器回调走集群内 svc DNS。
- **人机节奏**：日常是「在 JavDB 里加清单，然后不看」；回到面板时是在清「需要人工」队列，或改集成设置。配完就不碰是常态。

## Capabilities and Constraints

- **v1 清单用途只启用「想看」**；「想看中文」在结构与枚举上留位，不启用。
- **定源顺序固定**：JavDB 没有磁力即结束；JavDB 有才查 M-Team，有则优先 M-Team 私种（无磁力，infohash 由字节算出）。
- **v1 不解析番号**：只从项目 tag 取，取不到即失败。
- **媒体库布局固定**，无模板、无用户可编排结构（ADR-0008）。
- **状态词汇固定**：版本 `downloading → processing → finished`，或 `failed(phase, reason)`，`phase ∈ {downloading, scrape, organize}`；任务状态由版本聚合（视图 `task_state`，ADR-0006）。
- **无应用层鉴权，是长期约束**（回答已确认）：永不引入登录、会话、账号、权限概念；安全边界恒在网关层。
- **Postgres only**（ADR-0001）：无 SQLite 回退；迁移 goosed 内嵌、启动自动 Up、只前进。
- **单二进制 `linux/amd64`**：前端 `go:embed`，配置只有 DSN/监听/密码三件 bootstrap。
- **REST + SPA，错误信封 `{ok:false,error:{code,message}}`**；测试连接错误码是稳定机器码。
- **面板 v1 不渲染刮削字段与 provenance**，也不留占位（只给版本、事件、重试）。
- **明确未决**：移动端适配无要求；「想看中文」启用时点未定；演员详情页刮削未做（PLAN 15 只是结构就位）；应用层鉴权不做（已裁决为永久）。

## Brand Commitments

- 名称 **oh-my-mdc**；仓库 `2017fighting/oh-my-mdc`，镜像 `ghcr.io/2017fighting/oh-my-mdc`。
- **全中文界面（zh-CN）**，文案是产品的一部分，不是翻译产物：状态名固定为「下载中 / 加工中 / 已入库 / 需人工」，按钮固定为「重试 / 强制重刮 / 测试连接 / 保存本步 / 重发」；「重试」家族含行级「重试 N 条版本」与卡片级「重试 N 部 · M 条版本」。
- **术语以 `GLOSSARY.md` 为准**，含每条的 _Avoid_ 名单：说「任务」「版本」「清单项删除」「旁路副作用」「复用元数据」，不说「job」「副本」「移除」「pending」。
- 上游业务错原样透给用户（如 JavDB「您已創建了同名清單」），不改写成笼统提示。
- 唯一身份资产是 `web/public/favicon.ico`；没有 logo、品牌字体或配色承诺。许可 MIT。

## Evidence on Hand

- `docs/spec.md`（473 行，可 `/to-tickets` 的构建规格）、`GLOSSARY.md`、`docs/adr/`（0001–0011）——产品真值的书面来源。
- 可运行实现：`cmd/oh-my-mdc/`、`internal/` 各域（appapi、javdb、mteam、qbittorrent、scrape、organize、jellyfin、telegram、translate、poll、reconcile、sidefx…）、`web/app/`（面板 + 八步向导）。
- 真实依赖与部署：作者自己的集群、qBittorrent、Jellyfin、mihomo 出口，以及 home-ops 侧清单。
- 参考项目源码在作者机器上（`/home/zhao/clone/…`）：javdb_rss（MIT，在用）、open-mdcng（MIT）、mdc-lite（无 license，只循其思想，不移植代码）。
- **不存在**：用户量、证言、客户案例、第三方部署验证、上线时长与效果数据、定价或商业承诺。后续工作不得编造这些。

## Product Principles

1. **无人值守优先**：每条失败都要能自愈或重试，自愈不了才进「需要人工」，且只进一次。
2. **先落库后动手**：任何对外动作之前先写下意图；重启、断网、漏通知都不该丢账。
3. **外部系统只是线索**：tag、webhook、上游状态都可以丢、可以对账补；真相在本项目的库里。
4. **稳定 id 高于人类可读名**：清单、版本、来源一律按 id 判定，名字只用于展示与推荐。
5. **只对作者负责**：不为陌生人做首启引导、多租户或权限抽象；把同样的力气用在失败可见与恢复可控上。

## Accessibility & Inclusion

无正式标准（回答已确认）：保持键盘可达、语义正确、对比度可用即可，不承诺 WCAG 合规，不做屏幕阅读器专项验收。
