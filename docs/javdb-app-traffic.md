# 真机 App 流量样本（JavDB 1.9.29 / Flutter）

2026-11-03 抓的一份真机样本，用来核对本服务移植的 App API 是否仍然对得上。
**本文只留请求形状，不含凭据**：token、`jdsignature` 值、账号 id、封面 CDN 路径全部略去。

## 怎么抓的

设备：Lenovo TB128FU（Android 13, arm64, Magisk root）。App 包名 `xxx.pornhub.fuck`（1.9.29，
Flutter，`libapp.so` + `libflutter.so`），已登录账号。

```bash
# 1) frida-server（版本与 frida-tools 一致）推到设备，root 起
adb push frida-server /data/local/tmp/fs && adb shell 'su -c "chmod 755 /data/local/tmp/fs; /data/local/tmp/fs -D &"'

# 2) mitmproxy 起普通代理模式（8080）
mitmdump --listen-port 8080 -s dump.py

# 3) Frida 脚本做两件事，然后 spawn App
#    a. setenv http_proxy/https_proxy → dart:io 的 HttpClient 默认 findProxyFromEnvironment
#    b. 把 BoringSSL 的 ssl_verify_peer_cert 钉成 0（模式匹配 libflutter.so）
frida -U -f xxx.pornhub.fuck -l unpin.js
```

两个坑（都真踩过）：

- **`setenv` 必须早于 Dart 起来**，所以只能 spawn + load + resume，不能 attach 已跑起来的进程。
- 这版 Flutter 的 `libflutter.so` **没有导出任何 `SSL_*` 符号**（stripped，514 个 dynsym 里一个都没有），
  按符号 hook 走不通；只能模式匹配。本机可用模式是
  `F? 0F 1C F8 F? 5? 01 A9 F? 5? 02 A9 F? ?? 03 A9 ?? ?? ?? ?? 68 1A 40 F9`，
  在 `libflutter.so` 里**只命中一次**（偏移 `0x6cd494`），换引擎版本要重新验证命中数。
- App 只在 uid 维度的 TCP 443 上做 DNAT（反代到本地 mitmproxy）不行：它的域名是轮换的镜像，
  而且非 API 域名也会被一起劫走，App 直接显示「初始化失敗」。走 `http_proxy` 环境变量最省事。

## App 实际打到哪儿

| 用途 | 主机 |
|---|---|
| `/api/v1/startup`（启动配置、公告、广告） | `apidd.czssdgz.com` |
| 其余数据接口 | `apidd.spthgb.com` |
| 图片 CDN | 另一组域名（与本服务无关） |

⚠️ **`jdforrepam.com` 只是镜像之一**，本服务现在固定用它（移植自 javdb_rss）。
App 侧看到的主机名是上面两串，站点的公告里还列着 `javdb580.com` / `app.javdb580.com` / `jav.app` 等。
镜像之间接口形状一致（抽测同一 URL 换 host 返回同构负载），但可用性各自独立。

## 请求形状

```
GET /api/v1/lists?sort_by=create&page=1&limit=24
GET /api/v1/lists/mOKEkD
GET /api/v1/movies/tags?type=0&filter_by=0%3Al%3AmOKEkD%3A&sort_by=update&page=1&limit=24
GET /api/v1/movies/recommend?period=-1
GET /api/v1/movies/may_also_like?limit=9
GET /api/v1/movies/latest?type=all&filter_by=can_play&sort_by=update&page=1&limit=9
GET /api/v1/users?app_channel=official
GET /api/v1/ads
POST /api/v2/logs/activated
GET /api/v1/startup?last_ad_id=82&platform=android&app_channel=official&app_version=official&app_version_number=1.9.35
```

与本服务的差异（都**不影响正确性**，仅记录漂移）：

| 点 | App | 本服务 |
|---|---|---|
| 清单项 query | `type=0`、`filter_by=0:l:{id}:**:**`（结尾带冒号）、`sort_by=update`、`limit=24` | `filter_by=0:l:{id}`，只带 `page` |
| 清单列表 | `/api/v1/lists?sort_by=create&page=1&limit=24` | `/api/v1/lists/simple` |
| `user-agent` | `Dart/3.5 (dart:io)` | `Dart/3.4 (dart:io)` |
| 8 个公共 query 参数 | **不发** | 每请求必带 |
| 缓存 | 带 `if-none-match`（ETag，可 304） | 无 |
| 连接 | keep-alive 复用 | 修前每请求新建 Transport |

实测（同一 URL、同一有效 `jdsignature`）：

- 少掉 8 个公共参数照样 `success:1` —— 它们是 **`/startup` 一类的必需品，不是数据接口的必需品**。
  我们多带无害，无需改。
- `filter_by` 结尾那个冒号、`type`、`sort_by`、`limit` 只影响排序与页大小：同一条清单在
  两种写法下都返回 `{"movies":[],"has_collected":false,"current_page":1}`（当时这张清单本身是空的）。

响应信封与分页都照旧：`{"success":1,"action":null,"message":null,"data":{"movies":[...],"has_collected":false,"current_page":1}}`，
`limit` 默认 24，且仍然**没有总页数**，「空页即止」的判据成立。

## 顺带测到的链路质量（为什么 poll 会偶发 `EOF`）

同一窗口内用有效签名重打同一个接口，失败都是**握手期连接被断**（一个响应字节都没有，
`curl (35) unexpected eof while reading` / Go 侧 `Get "...": EOF`、`read: connection reset by peer`）：

| 路径 | 失败率 |
|---|---|
| 本机 → 三个镜像各 12 次 | `jdforrepam` 2、`spthgb` 1、`czssdgz` 1（~10%） |
| 集群 Pod（同一时段） | 14/40（12× 握手 `SSL_ERROR_SYSCALL` + 2× DNS） |
| 集群 Pod（稍后、同节点） | 2/30；同时 `example.com` 也 1/30 |
| 真机 App（13 个请求） | 0 |

结论：**阵发性、跨域名跨节点**的连接级抖动，不是凭据/签名/HTTP 版本/keep-alive 的问题
（`reused=false` 的全新连接一样复现），也不是某个镜像独有。指标把它记 `transient`、
`permanent=0` 是对的；代码侧的处置是 `internal/appapi` 共享 Transport + 对幂等 GET 的有界重试。
