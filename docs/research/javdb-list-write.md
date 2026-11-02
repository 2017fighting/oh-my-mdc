# Research: JavDB 清单写接口实测（create / movie_actions / patch / delete）

- **Date**: 2026-11-02
- **Ticket**: [#16](https://github.com/2017fighting/oh-my-mdc/issues/16)（替代原计划的抓包：环境无法抓包，改用用户 token 对一次性探针清单做 live probe）
- **Method**: 用与 javdb_rss 生产客户端完全相同的传输层（`jdsignature` 签名 + 8 公共 query 参数 + Bearer token，host `jdforrepam.com`，identity 伪装 v1.9.35 Pixel 6）直接调用 App 私有 API。**所有写操作只落在两张一次性探针清单上，验证完毕后已删除，最终 `lists/simple` 确认账户回到基线 5 张清单。** 全文均为**实测**（2026-11-02 02:58–03:05 UTC），无推断项。
- **Prior findings**: [javdb-api.md](https://github.com/2017fighting/oh-my-mdc/blob/research/javdb-api/docs/research/javdb-api.md)（#2，读侧 + 传输层）、[javdb-create-list.md](https://github.com/2017fighting/oh-my-mdc/blob/research/javdb-create-list/docs/research/javdb-create-list.md)（#15，静态 strings sweep —— 本文实测**修正**其两处推断）。
- **Prior art check（用户问的第三方库）**: `/root/clone/javdb-cli`（FlanChanXwO/javdb-cli，MIT）**没有**实现这两个写接口 —— 它只有 `GET /api/v1/lists`（MyLists，带 sort_by）、`GET /api/v1/lists/{id}`、`GET /api/v1/lists/related`，以及 reviews 的 POST/DELETE（mark/unmark）。`movie_actions`、建清单、改/删清单均无先例，本文为首测。

## 传输层（与 #2 实测一致，不赘述）

每个请求：8 公共 query 参数 + `jdsignature` header + `Authorization: Bearer <token>`；信封 `{success, action, message, data}`。本票据新观察：**业务校验失败（如名字过短）返回 `success:0, action:null` + zh-TW message** —— `action` 只在传输级错误（`ParameterInvalid`/`InvalidSignature`/auth 类）有值，分类时先看 `success` 再看 `action`。

## 1. 建清单 ✅ `POST /api/v1/lists`

- **Verb/body 实测**: `POST`，`application/x-www-form-urlencoded`，form 字段：
  - `name` —— **必填**，服务端校验 4–30 字符（实测 3 字符 → `success:0`，`"名稱 過短（最短爲 4 個字符）"`）；**同一账户内名字唯一**（实测重复 → `"名稱 您已創建了同名清單"`）。
  - `description` —— 可选，实测与 name 同发即持久化（在 `GET /api/v1/lists` 分页读里回读到了）。
  - `privacy` —— 未实测传值；不传时默认 `privacy:"open"`。
- **JSON body 会被拒**（实测 `{"movie_id":...}` 变体 → `ParameterInvalid: 參數不能爲空: name`；本端点未单独再试 JSON，form 已确认可用）。
- **响应**（实测原文）：

```json
{ "success": 1, "action": null, "message": "清單創建成功",
  "data": { "id": "e8r1OM", "name": "ohmymdc-probe", "privacy": "open",
            "is_default": false, "movies_count": 0, "has_movie": false } }
```

**清单 id 就在 `data.id`**（6 字符 nanoid 风格，与 URL `{id}` 一致）—— 向导建清单后无需再读回。读回兜底仍可用 `GET /api/v1/lists/simple` 按 **id**（不是名字）匹配。

## 2. 清单项增删 ✅ `POST /api/v1/lists/{list_id}/movie_actions`

- **Verb**: `POST`，form-encoded。路径参数 = 清单 id；**只需 8 公共 query 参数，无 zone、无其他 query**。
- **字段实测（修正 #15 的推断）**：
  - `name` = **动作动词**：`"add"` 或 `"remove"`。不是清单名、不是电影名 —— 字段名就叫 `name`（服务端 Rails strong-params 如此命名，可能就是「action name」）。
  - `movie_id` = 电影 id（如 `"YwrO3B"`）。
  - **`action` 字段无效**：传 `action=add` 连同 `name=x` 依然执行了 remove —— #15 从 strings 推断的 `action` 字段被服务端忽略。
- **⚠️ 头号陷阱：未知/缺失 `name` 默认执行 remove**。实测 `name="x"`（未知值）→ 直接移除成功（`"已從清單ohmymdc-probe中移除"`）。客户端必须**字面量拼写** `add`/`remove`，绝不能把用户输入或猜测值塞进 `name`；写侧单测要覆盖这个默认分支。
- **幂等性（双向实测）**：
  - 重复 add（已在清单）→ `success:1`（`"已將影片保存至清單…中"`），无「已存在」错误。
  - 重复 remove（不在清单）→ `success:1`（`"已從清單…中移除"`），无错误。**remove 对不存在的电影也不报错**（bogus movie_id + remove → success）。
- **失败形态**：`name=add` + 不存在的 `movie_id` → `success:0, action:null`，`"Translation missing: zh.activerecord.attributes.videoslistsrelation.video 必須存在"`（Rails i18n 泄漏：模型叫 `Videoslistsrelation`，校验是「video 必须存在」—— add 路径校验电影存在性，remove 不校验）。
- **成功响应**: `{"success":1, "action":null, "message":"已將影片保存至清單…中"/"已從清單…中移除", "data":null}` —— `data` 恒为 null，无需解析。

## 3. 改名 / 删除清单（updateMyList / deleteMyList 落实）

- `PATCH /api/v1/lists/{id}`，form `name=<新名>` → `success:1`，`data.list` 返回**完整清单对象**（含 `description/movies_count/views_count/collections_count/is_default/share_info/created_at`）。实测改名立即生效。
- `DELETE /api/v1/lists/{id}`，无 body → `{"success":1,"action":null,"message":null,"data":null}`，实测删除生效（最终读回确认消失）。

## 4. 顺带确认（票面要求）

- **清单项读** `GET /api/v1/movies/tags?filter_by=0:l:{list_id}`（zone=0 规则不变）：`data.movies[]` 每项**带 `id`（即 movie_id，写接口直接用它）与 `number`（番号）**，另有 `title/cover_url/duration/magnets_count/has_cnsub/release_date` 等完整 movieSlim。
- **分页**：`page` + `limit` query 参数实测有效（limit=2、翻到 page=2 各回 2 条）；响应只有 `current_page`，**没有总页数/总数** —— 翻到空页即止，与 #2 的结论同款。默认页大小未测（probe 清单只有 1 部），不阻塞：轮询方可显式传 `limit`。
- `lists/simple` 的 `has_movie` 在无电影上下文时恒 `false`，对轮询无用；`movies_count` 会随 add/remove 实时变化，可当对账信号。
- `GET /api/v1/lists`（bare，分页读）：**必须带 `sort_by`**，否则不是信封而是**裸 HTTP 500**（`{"status":500,"error":"Internal Server Error"}`）；带 `sort_by=created&page=&limit=` 返回富实体（含 `description/views_count/collections_count/created_at`）。另注意：默认清单名字在这里显示为服务端本地化的「預設清單」，而 `lists/simple` 显示 `default` —— 按 id 匹配的又一理由。轮询检测继续用 `/lists/simple` 即可（#15 同判）。

## 给 #14（spec 装配）的直接结论

1. 向导「一键建清单」可直接实现：`POST /api/v1/lists` form `name`（4–30，唯一）→ 取 `data.id` 存 `list_registry`；重名错误（message 含「同名清單」）→ 转人工/提示改名。live-probe 兜底不再必要，但读回校验可留作防御。
2. 删清单项（sidefx 循环）：`POST /api/v1/lists/{id}/movie_actions` form `name="remove", movie_id=<movie.id>`；幂等，重复删/电影不存在均 success —— 重试天然安全。**`name` 字段拼写必须由代码常量提供**（陷阱见上）。
3. 失败分类：`success:0` + `action:null` + zh-TW message = 业务校验错（不重试）；`action` 非空才走 #2 的传输/凭据分类表。

## 环境与复现

- host `https://jdforrepam.com`；identity `app_channel=official, app_version=1.9.35, app_version_number=10935, platform=android, system_version=13, device_model=Pixel 6, device_name=Pixel, device_uuid=<固定>`；签名 `"{ts}.lpw6vgqzsp.{md5(ts+prefix)}"`（常量见 javdb_rss `internal/appapi/signature.go`，对 1.9.35 服务端仍有效）。
- 探针脚本未入库（token 走环境变量，从未落盘）；如需复现，参照 javdb_rss `internal/appapi/client.go` 的传输层 + 本文各端点 form 字段即可。
- 探针账户副作用清单（全部已回滚）：建 2 张探针清单（`e8r1OM`/`4DbJdJ`，已删）、对探针清单 add/remove 电影 `YwrO3B`（ATID-637，最终不在任何清单）、对既有清单**只做过读**。
