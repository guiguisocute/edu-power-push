# 前端远程配置契约 · 运维面板已接通

> 状态：前端已写好消费端（`src/config/features.ts`）。
> 后端 `GET /api/v1/frontend-config` 与运维面板
> `PUT /api/v1/admin/frontend-config` 已上线。
> 如果网络失败或 404，前端回退本地默认值。
> 字段深合并，向后兼容新增键。

## 端点

```
GET /api/v1/frontend-config
```

- 无需认证。
  产品前端在浏览器中运行，不持有管理 Token 也应能读取。
- 如果返回失败，前端使用内置默认值（见下），不报错。
- 字段全部可选，深合并进默认值。
  新增键向后兼容。

## 响应示例（与当前代码默认值对齐）

```json
{
  "version": 1,
  "features": {
    "auth": {
      "email_login": true,
      "sms_login": false,
      "email_code": true,
      "sms_code": false,
      "registration": true,
      "google_oauth": true,
      "github_oauth": true
    },
    "channels": {
      "mail": true,
      "dingtalk": true,
      "wecom": true,
      "wecom_webhook": true,
      "feishu": true,
      "lark": true,
      "discord": true,
      "pushplus": true,
      "mp": true,
      "qq": true,
      "telegram": true,
      "sms": true
    },
    "channel_coming_soon": {
      "sms": true
    },
    "channel_order": ["mail", "dingtalk", "lark", "wecom", "wecom_webhook", "feishu", "discord", "webhook", "bark", "gotify", "whatsapp", "pushplus", "serverchan_turbo", "serverchan3", "mp", "qq", "napcat", "telegram", "sms"],
    "channel_categories": [
      { "id": "essentials", "name": "开箱即用", "en": "READY TO USE", "desc": "装个 App 或注册免费服务，填一个凭证就能收到推送。", "channels": ["mail", "sms", "pushplus", "serverchan_turbo", "serverchan3", "bark", "whatsapp"] },
      { "id": "group_bots", "name": "群聊机器人", "en": "GROUP CHAT BOTS", "desc": "在钉钉、飞书、企业微信群里加自定义机器人，复制 Webhook 即可。", "channels": ["dingtalk", "feishu", "lark", "wecom", "wecom_webhook", "discord", "telegram"] },
      { "id": "developer", "name": "进阶开发者渠道", "en": "SELF-HOSTED & DEVELOPER", "desc": "需要自建服务或申请开发者应用。", "channels": ["webhook", "gotify", "napcat", "qq", "mp"] }
    ],
    "charts": {
      "day_range": false,
      "hourly_usage": false
    }
  },
  "display": {
    "electricity_rate": "0.62",
    "empty_room_threshold_kwh": "0.3",
    "ranking_refresh_time": "09:00",
    "campus_name": "示例校区",
    "area_name": "示例大学",
    "semesters": []
  },
  "oauth": {
    "google": true,
    "github": true
  }
}
```

说明：库内初始种子（迁移 `000004`）里 `email_code` 曾为 `false`。
迁移 `000009` 起写成 `true`。
前端本地默认也是 `true`。
运维面板可随时改。

## 字段语义

| 字段 | 作用 | 当前默认 |
|---|---|---|
| `features.auth.email_login` | 登录/注册使用邮箱账号 | `true` |
| `features.auth.sms_login` | 手机号账号 + 短信认证（后端未写） | `false` → 界面完全不出现手机号登录 |
| `features.auth.email_code` | 邮箱验证码能力（注册发码 / 找回密码链路） | `true` |
| `features.auth.sms_code` | 找回密码走短信验证码 | `false` |
| `features.auth.registration` | 是否开放注册入口（Gate/登录页的注册按钮） | `true` |
| `features.auth.google_oauth` | 登录/注册页是否出现「使用 Google 登录」 | `true`（迁移 `000044` 播种） |
| `features.auth.github_oauth` | 登录/注册页是否出现「使用 GitHub 登录」 | `true`（同上） |
| `oauth.google` / `oauth.github` | **只读**：服务端是否已配置 client id/secret。保存时忽略。按钮出现条件：本字段与开关**都**为真。 | 随部署配置 |
| `features.channels.<id>` | 推送渠道可见性。`false` = 隐藏并停用已有投递。 | 缺省显示；id 见 `CH_DEFS` |
| `features.channel_coming_soon.<id>` | `true` = 展示 Coming Soon。禁止配置。停用已有投递。 | 短信默认 `true` |
| `features.channel_order` | 用户端渠道卡片顺序。管理面板可上移/下移。漏掉的新渠道补到末尾。 | 见上表 |
| `features.channel_categories` | 渠道分组。成员名单。组内顺序取自 `channel_order`。一渠道最多一组。空数组 = 平铺。 | 迁移 `000042` |
| `features.charts.day_range` | 数据看板「日」周期（24h 负荷）。无可靠小时数据时保持关。 | `false` |
| `features.charts.hourly_usage` | 用电分析「分时热力图」。代码归档在 `views/archive/`。 | `false` |
| `display.electricity_rate` | 电价（元/kWh，十进制字符串）。用于 kWh↔元 折算展示。 | `"0.62"`（电价确认前的展示值） |
| `display.empty_room_threshold_kwh` | 空房判定日用量阈值。影响户均除数与省电榜。 | `"0.3"` |
| `display.ranking_refresh_time` | 日榜每日、周榜周一、月榜每月 1 日切换到上一完整周期的时刻。 | `"09:00"` |
| `display.semesters` | 校历（学期起止）。空数组时学期视图退回「近 18 周」滚动窗口。 | `[]` |

## 运维面板侧

- 排行榜时刻、电价、校历、图表开关在「前端展示」维护。
  渠道分类、顺序与三态在「推送渠道」维护。
  两处都通过 `PUT /api/v1/admin/frontend-config` 持久化并记审计。
- 配置属于产品行为开关，不含任何密钥。
- 前端在应用启动时拉取一次（live 模式）。
  改配置后用户刷新页面生效。
