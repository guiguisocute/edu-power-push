# 用户偏好与推送契约

> 覆盖前端「推送设置」「账号设置与隐私」两页。
> 也覆盖概览「最近推送」与排行榜「我的位置」。
>
> **状态（2026-07-30）**：渠道、阈值、定时、推送记录、榜单参与均已进入 `openapi.yaml`。
> 并由 worker 引擎投递。
> `POST /api/v1/admin/mail/test` 仍是运维自测邮件供应商，与用户渠道无关。
>
> live 模式下渠道、规则、测试发送均已接通后端。
> 概览「最近推送」也已接通。
> mock 模式只用于本地界面演示。
> 推送记录不使用 60 秒数据缓存。
> 用户成功测试发送后返回概览。
> 会立即重新读取 `/api/v1/me/push-logs`。
> 推送页采用即时保存。
> 规则和开关在变更后立刻写库。
> 文本配置在输入框失焦时写库。
> 页面持续显示同步状态。
> 「我的位置」走 `GET /campus/rankings` 的 `self`/`neighbors`。
> 不单独请求 `/me/ranking`。

全部用户偏好端点使用 `userAccessToken`（产品用户 JWT）。
端点作用于当前会话用户的绑定电表。
路径统一挂 `/api/v1/me/*`（榜单本人名次除外，见 §6）。
与 `backend/docs/API.md` 一致。

---

## 1. 推送渠道 `GET/PUT /api/v1/me/channels`

前端渠道定义在 `src/lib/channels.ts` `CH_DEFS`，共 19 个。
`id` 即契约里的 `channel`：

| id | 名称 | 配置字段 | 敏感字段 |
|---|---|---|---|
| `mail` | 邮箱 | `to: string[]`（最多 5 个已验证收件人） | — |
| `sms` | 短信 | `sms: string[]`（多号码） | — |
| `dingtalk` | 钉钉群机器人 | `webhook`, `secret` | `secret` |
| `wecom` | 企业微信智能机器人 | `botid`, `secret`, `chatid` | `secret` |
| `wecom_webhook` | 企业微信消息推送 | `webhook` | `webhook` |
| `feishu` | 飞书机器人 | `webhook`, `secret` | `secret` |
| `lark` | Lark 机器人 | `webhook`, `secret` | `secret` |
| `discord` | Discord 群组 Webhook | `webhook` | `webhook` |
| `webhook` | Webhook | `webhook`, `token`（可选） | `webhook`, `token` |
| `bark` | Bark | `base_url`, `device_key` | `device_key` |
| `gotify` | Gotify | `base_url`, `token`, `priority`（可选整数） | `token` |
| `pushplus` | PushPlus | `token`（个人一对一免费渠道） | `token` |
| `mp` | 微信公众号测试号 | `appid`, `secret`, `tpl`, `openid` | `secret` |
| `qq` | QQ 官方机器人 | `appid`, `secret`, `user_openid` | `secret` |
| `napcat` | QQ群机器人(NapCat) | `base_url`, `token`（可选）, `target` | `token` |
| `whatsapp` | WhatsApp (CallMeBot) | `webhook` | `webhook` |
| `serverchan_turbo` | Server酱Turbo | `sendkey` | `sendkey` |
| `serverchan3` | Server酱³ | `sendkey` | `sendkey` |
| `telegram` | Telegram Bot | `token`, `chat`（私聊或群聊数字 ID） | `token` |

企业微信的 Bot ID/Secret、微信测试号的 AppID/AppSecret、
QQ 官方机器人的 AppID/AppSecret 都只解决服务端鉴权。
它们不能单独确定消息接收人。
企业微信还需单聊 `userid` 或群聊 `chatid`。
微信测试号还需关注者 `openid` 和模板 ID。
QQ 官方机器人使用 C2C 事件中的 `user_openid`。
调用路径为 `/v2/users/{user_openid}/messages`。
不再依赖群白名单能力。

NapCat 连接用户自行部署的 OneBot 11 HTTP Server。
`base_url` 只填写协议、域名/IP、端口及可选反向代理基础路径。
应用固定调用 `/send_msg`。
`target` 保存为 `group:<群号>` 或 `user:<QQ号>`。
界面通过群聊/私聊分段控件管理该前缀。
Token 优先使用 `Authorization: Bearer`。
仅当明确遇到 401/403 时，兼容回退 `access_token`。

WhatsApp 渠道使用 CallMeBot 的个人用途 API。
不是 Meta 官方 Cloud API。
用户只需粘贴包含 `phone` 与 `apikey` 的完整 URL。
URL 形如 `https://api.callmebot.com/whatsapp.php`。
服务端保留凭据参数并为每次推送覆盖 `text`。
完整 URL 按 Secret 加密保存。

Server酱Turbo 与 Server酱³ 均只填写 SendKey。
Turbo 使用 `https://sctapi.ftqq.com/{SendKey}.send`。
Server酱³ 从 `sctp{uid}t…` 自动提取 UID。
并使用 `https://{uid}.push.ft07.com/send/{SendKey}.send`。
成功回执必须同时满足 HTTP 2xx 与 JSON `code = 0`。

通用 Webhook 固定使用 `POST application/json`。
可选凭证以 `Authorization: Bearer <token>` 发送。
为防止 SSRF，仅允许公开 HTTPS 地址。
拒绝本机、内网、链路本地、元数据地址与 HTTP 跳转。
请求结构如下。
`data` 只携带当前事件有意义的字段：

```json
{
  "event": "low_balance_alert",
  "title": "低额度预警",
  "message": "当前余额……",
  "sent_at": "2026-07-31T08:00:00+08:00",
  "meter": { "number": "31240718", "building": "12栋", "floor": "4楼", "room": "402" },
  "data": { "balance_yuan": "8.50", "threshold_yuan": "10.00" }
}
```

Bark 支持官方与自建服务。
固定调用 `{base_url}/push`。
按官方 JSON 协议发送 `device_key`、`title`、`body`。
Gotify 固定调用 `{base_url}/message`。
应用令牌通过 `X-Gotify-Key` 发送。
正文为 `title`、`message` 与可选整数 `priority`。

低余额预警与定时摘要由同一份结构化事实生成。
邮件、普通机器人正文和 Webhook 均含电表号与宿舍位置。
避免渠道间信息不一致。

邮箱渠道不能直接输入任意第三方地址。
登录邮箱沿用注册/换绑时的验证结果。
附加收件邮箱必须先收取验证码。
端点为 `POST /api/v1/me/channels/mail/recipient/code`。
再用 `/recipient/verify` 确认。
验证成功后才能写入 `mail.to`。
服务端在配置保存、测试投递和 Worker 正式投递三处复核。
历史未验证收件人会在迁移时回落为当前登录邮箱。

Telegram 官方发送接口仍要求 `chat_id`。
私聊向 Bot 发送 `/start`。
群聊把 Bot 加入目标群后发送 `/start@机器人用户名`。
只填写 Bot Token 并点击「发送测试」时，服务端会自动识别 Chat ID。
识别经 `getUpdates`，并保存唯一的私聊或群聊。
如果同一 Bot 出现多个可用会话则拒绝猜测。
用户也可以直接填写数字 Chat ID。
负数目标在发送前通过 `getChat` 确认为 `group` 或 `supergroup`。
频道继续拒绝。

企业微信消息推送与 Discord 使用频道/群组生成的 Incoming Webhook。
完整 URL 已包含投递凭证。
不需要额外 Bot ID、Secret 或接收人 ID。
Discord 固定附带 `wait=true` 确认消息已创建。
并禁用 mentions 解析。

依据：[企业微信消息推送配置说明](https://developer.work.weixin.qq.com/document/path/99110)、[Discord Execute Webhook](https://docs.discord.com/developers/resources/webhook#execute-webhook)。

`webhook` 本身即凭证（URL 内含 access_token / key / hook id），**也按敏感字段保存**。

### 读

```
GET /api/v1/me/channels  →  200
```

```json
{
  "channels": [
    {
      "channel": "dingtalk",
      "enabled": true,
      "config": { "webhook": "https://oapi.dingtalk.com/robot/send?access_token=a91f****", "secret": null },
      "secret_set": { "webhook": true, "secret": false },
      "last_result": {
        "status": "ok",
        "at": "2026-07-27T08:00:03+08:00",
        "message": null
      },
      "updated_at": "2026-07-26T19:20:11+08:00"
    }
  ]
}
```

- **敏感字段只进不出**：
  `config` 里的敏感字段回读时给掩码串或 `null`。
  掩码保留可辨识的头尾。
  另用 `secret_set.<field>: boolean` 告诉前端「后端存了值」。
  前端据此渲染 `••••••••` 占位而不是空输入框。
  绝不回明文。
  这些是用户的机器人凭证。
- 未配置过的渠道可以不出现在数组里。
  前端按 `CH_DEFS` 补默认关闭态。
- `last_result` 是该渠道最近一次真实投递的结果。
  定时、预警、测试均计入。
  供配置页显示状态点。

### 写

```
PUT /api/v1/me/channels/{channel}  →  200（返回同上单条对象）
```

```json
{
  "enabled": true,
  "config": { "webhook": "https://oapi.dingtalk.com/robot/send?access_token=真实值", "secret": "SEC***" }
}
```

- **部分更新语义**：`config` 中**省略**某敏感字段 = 保留后端已存值。
  显式传 `null` = 清空。
  这样前端可以在用户没重填密钥时直接提交表单。
- 后端按 `channel` 校验字段白名单，未知字段 `400`。
- `webhook` 必须校验协议、固定路径与域名白名单。
  白名单包括 `qyapi.weixin.qq.com` / `discord.com`。
  否则用户可把服务端当 SSRF 跳板。
  Telegram 不接受自定义代理地址。
- 渠道由 `features.channels.<id>` 与 `features.channel_coming_soon.<id>` 组成三态。
  三态为：可用 / Coming Soon / 隐藏。
  后两种状态会拒绝用户写入并立即停用已有投递。

整页「保存」按钮可批量提交：`PUT /api/v1/me/channels`。
请求体 `{ "channels": [ ...同上单条... ] }`。

---

## 2. 渠道测试发送 `POST /api/v1/me/channels/{channel}/test`

对应配置页每个可用渠道的「测试」按钮。
使用已保存的加密凭证真实发送。

```
POST /api/v1/me/channels/{channel}/test  →  200
```

```json
{ "status": "ok", "at": "2026-07-27T09:41:02+08:00", "message": null, "latency_ms": 412 }
```

- `status`: `ok` / `failed`。
  `failed` 时 `message` 给可直接展示的中文原因。
  原因含超时、凭证无效、被限流。
- 投递失败仍返回 **200**（这是一次成功的「已尝试」应答）。
  只有服务端自身故障用 5xx。
- **限流**：Telegram 每 60 秒一次。
  PushPlus 因固定测试文案受官方“相同内容每小时 3 条”限制。
  故每 20 分钟一次。
  均返回 `429` + `Retry-After`。
  并以数据库原子抢占避免并发穿透。
- 测试发送与正式投递开关相互独立。
  渠道关闭时仍可用已保存凭据测试。
  完全未配置时才拒绝。
  被管理端隐藏/标记 Coming Soon 时也拒绝。
  代码未写好时同样拒绝。
- 测试消息使用当前账号真实绑定事实。
  字段含剩余电费、数据更新时间、宿舍位置、电表号。
  未绑定或暂无读数时明确标注，不使用示例数据。
  这样用户能同时核对投递链路和绑定是否正确。

---

## 3. 推送规则 `GET/PUT /api/v1/me/notification-settings`

对应 `store.tsx` 的 `lowAlert` / `threshold` / `schedule` / `period` / `pushTime`。

```json
{
  "low_balance_alert": true,
  "threshold_yuan": "10",
  "scheduled_digest": true,
  "period": "daily",
  "push_time": "08:00",
  "timezone": "Asia/Shanghai",
  "updated_at": "2026-07-26T19:20:11+08:00"
}
```

| 字段 | 前端字段 | 取值 |
|---|---|---|
| `low_balance_alert` | `lowAlert` | 余额低于阈值时推送 |
| `threshold_yuan` | `threshold` | Decimal 字符串。前后端统一限制 1–50 元。默认 10 元。 |
| `scheduled_digest` | `schedule` | 定时摘要总开关 |
| `period` | `period` | `daily` / `twice` / `every3` / `weekly`（见 `channels.ts` `PERIODS`） |
| `push_time` | `pushTime` | `HH:MM`，24 小时制。`twice` 时后端在此基础上 +12h 发第二次。 |
| `timezone` | — | 前端不提供。后端固定 `Asia/Shanghai`。写进契约是为了让「08:00」无歧义。 |

`PUT` 全量覆盖。
`threshold_yuan` 用字符串保持与全局 Decimal 约定一致。

**低额预警需要去抖**：余额在阈值附近抖动不应反复推送。
后端应「跌破阈值后推一次」。
直到回升超过 阈值+缓冲 才重置。
缓冲值不必暴露给前端。

---

## 4. 推送记录 `GET /api/v1/me/push-logs`

对应概览页「最近推送」列表。
前端只需最近 5 条。
按分页契约设计便于将来做完整历史页。

```
GET /api/v1/me/push-logs?limit=5&cursor=
```

```json
{
  "items": [
    {
      "id": "0198f2b1-...",
      "sent_at": "2026-07-25T17:42:10+08:00",
      "channel": "mail",
      "kind": "low_balance",
      "status": "delivered",
      "summary": "低额度预警 · 余额 43.87 元已低于 20 元阈值",
      "error": null
    }
  ],
  "next_cursor": null
}
```

- `kind`: `low_balance` / `digest` / `test` / `system`
- `status`: `delivered` / `failed` / `skipped`（渠道被关 / 配额用尽）
- `summary` 是**后端渲染好的整句中文**。
  前端直接展示不做拼装。
  推送文案的真源在后端模板。
  前端复制一份必然漂移。
- `error` 仅 `failed` 时非空，前端渲染成 `失败 · {error}`。
- 前端列表列为：时间 `MM-DD HH:mm` / 渠道名 / `summary` / 状态。
  渠道名由前端按 `channel` 查 `CH_DEFS` 得到。

---

## 5. 榜单参与与脱敏 `GET/PUT /api/v1/me/leaderboard` · ✅ 已上线（2026-07-27）

**实际落地**与提案的差异：

- 字段名用扁平布尔。
  形如 `show_building/show_floor/show_room/show_nickname`。
  另有 `mask_building/mask_floor`。
  而不是嵌套的 `display_fields`/`mask_fields`。
  PUT 支持部分更新。
  省略的字段保持原值。
  老客户端不会把新字段悄悄清空。
- **不提供「展示电表号」**：
  榜单匿名可读。
  把表号挂上去即公开一份可枚举的表号清单。
  前端 live 模式下电表号那一行不出现。
- `RankingEntry` 按提案增加了服务端渲染好的 `label`。
  明文 `building/floor/room` 只在 `is_self` 为真时下发。
  退出榜单的电表在 SQL 层就被排除。
- **没有账号的电表**（当前 7519 块里的绝大多数）走代码默认值。
  默认：参与榜单、**完整栋号 · 完整楼层 · 房间打码**。
  匿名可读的榜单不该精确到某一间宿舍。
- 房间号默认打码展示（`mask_room=true`）。
  昵称只能展示/隐藏。

> 顺带修掉一个**原有排序 bug**：
> `SELECT current_kwh::numeric(24,4)::text` 的输出列仍叫 `current_kwh`。
> Postgres 的 `ORDER BY` 优先绑定输出列名。
> 于是榜单一直在按字典序排。
> `"9.99"` 排在 `"34.31"` 前面。
> 现已把输出列改名为 `value_kwh`。

原始提案如下（落地前的论证；现已服务端生效）：

对应 `joinBoard` / `pf` / `pmask`。
**这是隐私开关，必须服务端生效**。
如果只存 `localStorage`，用户关掉「参与排行榜」后后端仍会把他排进去。

```json
{
  "opted_in": true,
  "display_fields": { "meter": true, "building": true, "floor": true, "room": true, "nickname": false },
  "mask_fields": { "meter": true, "building": true, "floor": true },
  "updated_at": "2026-07-26T19:20:11+08:00"
}
```

- `display_fields`（前端 `pf`，键名 `meter/bldg/floor/room/nick`）：
  哪些身份字段允许出现在榜单行上。
- `mask_fields`（前端 `pmask`，仅 `meter/bldg/floor` 三项可脱敏）：
  对已展示的字段是否打码。
  脱敏规则见 `src/lib/privacy.ts`。
  表号 `3124****`、楼栋 `河东 ** 栋`、楼层 `*F`。
  房间号与昵称不支持脱敏（要么展示要么隐藏）。
- 全部 `display_fields` 为 false = 匿名参与。
  榜单只显示名次与用电量。
- `opted_in: false` 时该电表**不得出现在 rankings 的任何结果里**。
  端点为 `GET /api/v1/campus/rankings`。
  且不计入 `excluded_count` 之外的任何可反推口径。

**脱敏必须在服务端做。**
`rankings` 返回的 `RankingEntry` 目前是
`{building, floor, room, value_kwh}` 明文。
下发后前端再打码即没打码。
谁都能看响应体。
`RankingEntry` 应增加已渲染好的 `label` 字段。
由后端按该用户的 `display_fields`/`mask_fields` 生成。
明文 `building/floor/room` 仅在「该行就是请求者本人」时才下发。

---

## 6. 我的排名 · ✅ 已通过 `campus/rankings.self` 接通

**实际落地**：没有单独建 `GET /api/v1/me/ranking`。
`GET /api/v1/campus/rankings` 在请求带有效用户 access JWT 且已绑表时，额外返回：

```json
{
  "items": [ /* Top N，含 is_self 标记 */ ],
  "self": {
    "rank": 137,
    "total": 1284,
    "percentile": 0.107,
    "label": "河东 12 栋 · 4 楼",
    "name": "匿名用户",
    "value_kwh": "4.82",
    "change_ratio": -0.12,
    "in_list": false,
    "neighbors": [ /* 上/下邻 + 自己，各自按偏好脱敏 */ ]
  }
}
```

- `self` 在**完整人群**上计算，与 Top N 截断无关。
  以前只在 `items` 里找 `is_self` 会让第 51 名显示「—」。
  现已修复。
- 未登录 / 未绑表 / 不在当前筛选范围时 `self` 为 `null`。
  前端给出对应 CTA。
- 脱敏字段与公开榜单同一套服务端渲染（`label` / `name`）。
  不回发明文表号。

### 原始提案（未单独上线，保留对照）

```
GET /api/v1/me/ranking?period=month&mode=usage&building=&floor=
```

独立端点在当前产品里不是刚需。
`rankings.self` 已覆盖「我的位置」卡片与近邻行。

---

## OpenAPI 片段

```yaml
  /api/v1/me/channels:
    get:
      tags: [Users]
      operationId: listCurrentUserChannels
      summary: List notification channel configurations (secrets masked)
      security: [{ userAccessToken: [] }]
      responses:
        "200":
          description: Channel configurations
          content: { application/json: { schema: { $ref: "#/components/schemas/ChannelList" } } }
        "401": { $ref: "#/components/responses/Unauthorized" }
    put:
      tags: [Users]
      operationId: replaceCurrentUserChannels
      summary: Bulk update notification channel configurations
      security: [{ userAccessToken: [] }]
      requestBody:
        required: true
        content: { application/json: { schema: { $ref: "#/components/schemas/ChannelList" } } }
      responses:
        "200":
          description: Updated configurations
          content: { application/json: { schema: { $ref: "#/components/schemas/ChannelList" } } }
        "400": { $ref: "#/components/responses/BadRequest" }
        "401": { $ref: "#/components/responses/Unauthorized" }
        "403": { $ref: "#/components/responses/Forbidden" }

  /api/v1/me/channels/{channel}:
    put:
      tags: [Users]
      operationId: updateCurrentUserChannel
      summary: Update one notification channel; omitted secret fields keep their stored value
      security: [{ userAccessToken: [] }]
      parameters: [{ $ref: "#/components/parameters/ChannelId" }]
      requestBody:
        required: true
        content: { application/json: { schema: { $ref: "#/components/schemas/ChannelUpdate" } } }
      responses:
        "200":
          description: Updated channel
          content: { application/json: { schema: { $ref: "#/components/schemas/Channel" } } }
        "400": { $ref: "#/components/responses/BadRequest" }
        "401": { $ref: "#/components/responses/Unauthorized" }
        "403": { $ref: "#/components/responses/Forbidden" }

  /api/v1/me/channels/{channel}/test:
    post:
      tags: [Users]
      operationId: testCurrentUserChannel
      summary: Send current balance, data time, location, and meter ID through a configured channel
      security: [{ userAccessToken: [] }]
      parameters: [{ $ref: "#/components/parameters/ChannelId" }]
      responses:
        "200":
          description: Delivery attempted; see status
          content: { application/json: { schema: { $ref: "#/components/schemas/ChannelTestResult" } } }
        "401": { $ref: "#/components/responses/Unauthorized" }
        "409":
          description: Channel is unavailable, unsupported, or has no saved configuration
          content: { application/json: { schema: { $ref: "#/components/schemas/ErrorResponse" } } }
        "429": { $ref: "#/components/responses/RateLimited" }

  /api/v1/me/notification-settings:
    get:
      tags: [Users]
      operationId: getCurrentUserNotificationSettings
      security: [{ userAccessToken: [] }]
      responses:
        "200":
          description: Notification rules
          content: { application/json: { schema: { $ref: "#/components/schemas/NotificationSettings" } } }
        "401": { $ref: "#/components/responses/Unauthorized" }
    put:
      tags: [Users]
      operationId: replaceCurrentUserNotificationSettings
      security: [{ userAccessToken: [] }]
      requestBody:
        required: true
        content: { application/json: { schema: { $ref: "#/components/schemas/NotificationSettings" } } }
      responses:
        "200":
          description: Updated rules
          content: { application/json: { schema: { $ref: "#/components/schemas/NotificationSettings" } } }
        "400": { $ref: "#/components/responses/BadRequest" }
        "401": { $ref: "#/components/responses/Unauthorized" }

  /api/v1/me/push-logs:
    get:
      tags: [Users]
      operationId: listCurrentUserPushLogs
      security: [{ userAccessToken: [] }]
      parameters:
        - $ref: "#/components/parameters/Cursor"
        - $ref: "#/components/parameters/PageSize"
      responses:
        "200":
          description: Push history page
          content: { application/json: { schema: { $ref: "#/components/schemas/PushLogPage" } } }
        "401": { $ref: "#/components/responses/Unauthorized" }

  /api/v1/me/leaderboard:
    get:
      tags: [Users]
      operationId: getCurrentUserLeaderboardPrefs
      security: [{ userAccessToken: [] }]
      responses:
        "200":
          description: Leaderboard participation and privacy preferences
          content: { application/json: { schema: { $ref: "#/components/schemas/LeaderboardPrefs" } } }
        "401": { $ref: "#/components/responses/Unauthorized" }
    put:
      tags: [Users]
      operationId: replaceCurrentUserLeaderboardPrefs
      security: [{ userAccessToken: [] }]
      requestBody:
        required: true
        content: { application/json: { schema: { $ref: "#/components/schemas/LeaderboardPrefs" } } }
      responses:
        "200":
          description: Updated preferences
          content: { application/json: { schema: { $ref: "#/components/schemas/LeaderboardPrefs" } } }
        "400": { $ref: "#/components/responses/BadRequest" }
        "401": { $ref: "#/components/responses/Unauthorized" }

  /api/v1/me/ranking:
    get:
      tags: [Users]
      operationId: getCurrentUserRanking
      summary: Get the authenticated user's own position in a ranking
      security: [{ userAccessToken: [] }]
      parameters:
        - name: period
          in: query
          required: true
          schema: { type: string, enum: [day, week, month] }
        - name: mode
          in: query
          required: true
          schema: { type: string, enum: [usage, saving, surge, drop] }
        - name: building
          in: query
          schema: { type: [string, "null"] }
        - name: floor
          in: query
          schema: { type: [string, "null"] }
      responses:
        "200":
          description: Own ranking position
          content: { application/json: { schema: { $ref: "#/components/schemas/MyRanking" } } }
        "401": { $ref: "#/components/responses/Unauthorized" }
        "409":
          description: User has no active meter binding
          content: { application/json: { schema: { $ref: "#/components/schemas/ErrorResponse" } } }

# components.parameters
    ChannelId:
      name: channel
      in: path
      required: true
      schema:
        type: string
        enum: [mail, sms, dingtalk, wecom, wecom_webhook, feishu, lark, discord, webhook, bark, gotify, whatsapp, pushplus, serverchan_turbo, serverchan3, mp, qq, napcat, telegram]

# components.schemas
    Channel:
      type: object
      required: [channel, enabled, config, secret_set]
      properties:
        channel:
          type: string
          enum: [mail, sms, dingtalk, wecom, wecom_webhook, feishu, lark, discord, webhook, bark, gotify, whatsapp, pushplus, serverchan_turbo, serverchan3, mp, qq, napcat, telegram]
        enabled: { type: boolean }
        config:
          type: object
          description: Per-channel fields; secret values are masked or null on read.
          additionalProperties: true
        secret_set:
          type: object
          description: Which secret fields currently hold a stored value.
          additionalProperties: { type: boolean }
        last_result:
          oneOf:
            - $ref: "#/components/schemas/ChannelTestResult"
            - type: "null"
        updated_at: { type: string, format: date-time }

    ChannelUpdate:
      type: object
      required: [enabled, config]
      additionalProperties: false
      properties:
        enabled: { type: boolean }
        config:
          type: object
          description: Omit a secret field to keep the stored value; send null to clear it.
          additionalProperties: true

    ChannelList:
      type: object
      required: [channels]
      properties:
        channels:
          type: array
          items: { $ref: "#/components/schemas/Channel" }

    ChannelTestResult:
      type: object
      required: [status, at]
      properties:
        status: { type: string, enum: [ok, failed] }
        at: { type: string, format: date-time }
        message: { type: [string, "null"] }
        latency_ms: { type: [integer, "null"] }

    NotificationSettings:
      type: object
      required: [low_balance_alert, threshold_yuan, scheduled_digest, period, push_time]
      additionalProperties: false
      properties:
        low_balance_alert: { type: boolean }
        threshold_yuan: { type: string, description: "Decimal yuan" }
        scheduled_digest: { type: boolean }
        period: { type: string, enum: [daily, twice, every3, weekly] }
        push_time: { type: string, pattern: "^([01][0-9]|2[0-3]):[0-5][0-9]$" }
        timezone: { type: string, default: "Asia/Shanghai" }
        updated_at: { type: string, format: date-time }

    PushLog:
      type: object
      required: [id, sent_at, channel, kind, status, summary]
      properties:
        id: { type: string, format: uuid }
        sent_at: { type: string, format: date-time }
        channel: { type: string }
        kind: { type: string, enum: [low_balance, digest, test, system] }
        status: { type: string, enum: [delivered, failed, skipped] }
        summary: { type: string, description: "Fully rendered Chinese sentence; frontend displays verbatim." }
        error: { type: [string, "null"] }

    PushLogPage:
      type: object
      required: [items]
      properties:
        items:
          type: array
          items: { $ref: "#/components/schemas/PushLog" }
        next_cursor: { type: [string, "null"] }

    # 实际落地为扁平布尔（见 §5）；下列嵌套形状仅保留提案对照，勿当作现行契约。
    LeaderboardPrefs:
      type: object
      description: Implemented as flat show_*/mask_* booleans with partial PUT; see openapi.yaml.
      required: [opted_in]
      properties:
        opted_in: { type: boolean }
        show_building: { type: boolean }
        show_floor: { type: boolean }
        show_room: { type: boolean }
        show_nickname: { type: boolean }
        mask_building: { type: boolean }
        mask_floor: { type: boolean }
        mask_room: { type: boolean }
        updated_at: { type: string, format: date-time }

    # §6 最终落在 Ranking.self，不单独暴露 /me/ranking；见 openapi RankingSelf。
```

---

## 落地顺序（已结束）

1. ~~§5 榜单参与~~ ✅
2. ~~§1 + §2 渠道配置与测试~~ ✅
3. ~~§3 推送规则 + worker 引擎~~ ✅
4. ~~§4 推送记录~~ ✅、~~§6 我的排名（via rankings.self）~~ ✅
