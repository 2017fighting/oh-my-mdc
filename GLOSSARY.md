# oh-my-mdc

把 JavDB 自建清单里的番号，自动变成 Jellyfin 媒体库里的条目：轮询清单 → 加下载 → 刮削 → 硬链接整理 → 刷新媒体库 → 通知。
本文件只收本项目的领域词汇，不收实现细节。

## Language · 上游与清单

**作品** (Work)：
JavDB 上的一部片，主键是它的 **movie_id**。一部作品有一个番号，但番号不是唯一键。
_Avoid_: 影片、movie、条目；用「番号」当主键

**番号** (Number)：
形如 `KV-328` 的作品编号，出现在种子标签、目录名与 NFO 标题里。合集与不同片商同名会让一个番号对应多部作品。
_Avoid_: 车牌、号码、code

**清单** (List)：
用户在 JavDB App 里自建的片单，有 id。本项目不认清单名，只认 id。
_Avoid_: 列表、片单、订阅

**想看** (WantWatch)：
本项目轮询的那一张自建清单。它是「一有磁力就交给我下载」的待办，不是 App 里的 `want_watch` 标记——同名两物，v1 只读前者。
_Avoid_: want_watch 状态、收藏（那是女优）

**磁力候选** (Magnet)：
某个数据源针对一部作品给出的一条可下载候选。身份是 infohash，与交付形态无关。
_Avoid_: 资源、种子（那指交付物形态）

**种子文件** (Torrent)：
M-Team 的交付形态：私有、无磁力，infohash 由本项目从字节里算出。
_Avoid_: 磁力

## Language · 任务域

**任务** (Task)：
以 movie_id 为主键的聚合，承载该作品的刮削元数据与整体状态。一个番号作品一条任务。
_Avoid_: 番号任务、job、条目

**版本** (Version)：
任务下的一条下载候选，主键 infohash。普通、中字、破解各是一条版本；同一作品可以有多条，各自独立走路。
_Avoid_: 副本、磁力任务、分盘

**分片** (Part)：
同一条版本（同一个种子）内的多个视频文件，如 VR 的 cd1/cd2。分片是文件，不是版本。
_Avoid_: 分盘任务、子任务

**刮削** (Scrape)：
从 DMM（图片、原始日文元数据）与 JavDB（演员、番号信息）取字段，再按需 AI 翻译成中文标题。
_Avoid_: 爬取、抓取

**入库** (Filed)：
版本完成硬链接、NFO 与图片落盘的那一刻。任务级 tag 到此刻才从 `mdc:in-progress` 换成 `mdc:finished`。
_Avoid_: 完成、处理完、scraped

**复用元数据** (Reuse)：
任务的元数据已存在时，新版本不联网、只做落盘。中文/破解版本「省去刮削」就是这条，判据是元数据存在与否，不是名称分类。
_Avoid_: skip scrape、缓存命中

**旁路副作用** (Side effect)：
入库之外的三件事：删清单项、刷新 Jellyfin、发 Telegram。各自带 pending 标记独立重试，失败不阻塞入库。
_Avoid_: 后置任务、通知阶段

## Language · 循环与状态

**版本状态** (Version state)：
`downloading` → `processing` → `finished`，或 `failed(phase, reason)`。下载阶段的 failed 允许重下，加工阶段的 failed 允许重试加工。
_Avoid_: 进度、phase

**任务状态** (Task state)：
同一组状态名，但由版本聚合而来：任一版本在加工 → processing；任一在下载 → downloading；有版本入库 → finished；全部失败 → failed。
_Avoid_: 整体进度

**对账** (Reconcile)：
定时扫 qBittorrent 里带本项目 tag 的种子，补 webhook 漏掉的通知。
_Avoid_: 轮询（那指清单轮询）、扫描

**占用** (Claim)：
版本行上的一份带超时的租约，保证清单轮询、webhook、对账三条触发路径不会同时处理同一条版本。
_Avoid_: 锁、in-flight

**清单项删除** (List removal)：
把番号从想看清单里移除，恒在加下载之后发生，由旁路副作用循环执行并重试。
_Avoid_: 移除、标记看过

**项目 tag** (Project tag)：
打在 qBittorrent 种子上的 `mdc:in-progress` / `mdc:finished`。它是给人看与给对账认的线索，不是状态真相；失败不新增 tag。
_Avoid_: 状态标签、系统 tag
