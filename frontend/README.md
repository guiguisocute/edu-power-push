# EDU Power Push · 产品前端

产品界面的 React + TypeScript 实现。用户界面与管理界面在**同一应用**内；管理界面为懒加载 chunk（`src/admin/`）。

- PC：沿用既定信息架构、视觉与交互。
- 移动端（≤960px）：侧栏改抽屉，布局单独适配。

后端契约：[`../backend/api/openapi.yaml`](../backend/api/openapi.yaml)。  
前后端落地状态索引：[`docs/README.md`](docs/README.md)。

---

## 运行

```bash
pnpm install
pnpm dev          # development，默认 mock
pnpm typecheck
pnpm build        # production
pnpm build:test   # test
```

开发服务器把 `/api`、`/health` 代理到 `DEV_API_PROXY`（默认 `http://127.0.0.1:8080`）。该变量无 `VITE_` 前缀，**不会进浏览器产物**。

### 环境变量

| 文件 | 作用 | 是否提交 |
|---|---|---|
| `.env` | 三套环境共享默认值 | 是 |
| `.env.development` / `.env.test` / `.env.production` | 各 mode 覆盖 | 是 |
| `.env.local`（模板 `.env.local.example`） | 本机覆盖，可含 `VITE_API_TOKEN` | **否** |

| 变量 | 含义 |
|---|---|
| `VITE_API_BASE` | 默认空 = **同源**。产物不含域名，同一 `dist` 可挂任意域名 |
| `VITE_API_MODE` | `live` 走后端；其他为 mock |
| `VITE_API_TOKEN` | 仅本机调试用的 `ADMIN_TOKEN`。**禁止进线上构建** |
| `VITE_ADMIN_PANEL` | `true` 时编入管理界面；否则侧栏无「系统管理」 |
| `DEV_API_PROXY` | 仅 Vite 开发代理目标 |

---

## 应用结构

```
src/
├── main.tsx                 # 入口
├── App.tsx                  # 外壳：侧栏 + 顶栏 + Gate + 路由 + 抽屉 + Toast
├── smoke.tsx                # 冒烟挂载用
├── vite-env.d.ts            # Vite 环境类型
│
├── api/                     # 后端客户端与类型（对齐 openapi.yaml）
│   ├── client.ts            # fetch 封装、401→refresh 重试、在途计数
│   ├── session.ts           # 启动静默恢复会话
│   ├── live.ts              # live 数据 hooks（SWR 式缓存键）
│   ├── types.ts             # 响应类型
│   ├── errors.ts            # error.code → 中文提示
│   ├── mode.ts              # mock / live 判定
│   ├── leaderboard.ts       # 榜单隐私读写
│   └── notifications.ts     # 渠道 / 规则 / 默认推送模板
│
├── lib/
│   ├── store.tsx            # 全局状态 + localStorage 偏好 + 认证/绑表/推送动作
│   ├── mock.ts              # 确定性 mock 引擎（基准日 2026-07-25）
│   ├── channels.ts          # CH_DEFS / 分类兜底 / 校验缺字段
│   ├── routes.ts / deeplink.ts
│   ├── privacy.ts / rankingPeriods.ts / scopes.ts / semesters.ts
│   ├── format.ts / months.ts / meter.ts / exportUsage.ts
│   ├── balanceChart.ts / campusYear.ts
│   ├── adminAccess.ts       # 侧栏是否露出「系统管理」
│   └── useBrush.ts / useSize.ts
│
├── config/
│   ├── features.ts          # 本地默认 + GET /frontend-config 合并
│   └── emailDomains.ts      # 注册邮箱域名白名单提示
│
├── components/              # 壳层与共用控件
│   ├── Sidebar.tsx / TopBar.tsx / AccountMenu.tsx
│   ├── AuthOverlay.tsx / OAuthButtons.tsx / CaptchaGate.tsx
│   ├── GateToast.tsx / InlineBindMeter.tsx / RefreshButton.tsx
│   ├── PushHistory.tsx / UsageExport.tsx / MonthWindowPicker.tsx
│   └── …
│
├── views/                   # 五个主视图 + 归档片段
│   ├── OverviewView.tsx     # 概览（余额、用电柱图、手动刷新）
│   ├── UsageView.tsx        # 用电分析（曲线、日历、对比、导出）
│   ├── CampusView.tsx       # 全校数据看板
│   ├── BoardView.tsx        # 排行榜
│   ├── ConfigView.tsx       # 推送与账号设置
│   └── archive/             # 上游暂不支持的分时 UI（见 archive/README.md）
│
├── admin/                   # 管理面板（lazy）
│   ├── AdminSection.tsx     # 入口与会话校验
│   ├── api.ts               # /api/v1/admin/* 客户端
│   ├── DashboardView / ScannerView / SchoolView / UsersView
│   ├── ChannelsView / CaptchaView / DisplayView / OAuthCredentials
│   └── ui.tsx               # 管理端原语
│
└── styles/
    ├── global.css           # 变量、主题、hv-*、移动端规则
    └── fonts.css
```

---

## 运行时调用关系

### 启动

```
main.tsx
  → App
      → useCreateStore（lib/store.tsx）
      → session 静默恢复（api/session.ts → POST /auth/refresh）
      → fetchRemoteFeatures（GET /frontend-config，失败用本地默认）
      → 深链 / OAuth 回跳处理（lib/deeplink.ts）
```

### 视图路由（无 React Router）

状态字段 `view` 在 `store` 中：`overview` | `usage` | `campus` | `board` | `config` | `account` | 管理页枚举。

| 条件 | 表现 |
|---|---|
| 未登录且 view ∈ 概览/用电/推送/账号 | `Gate` 挡个人数据 |
| 已登录未绑表且 view ∈ 用电/推送 | `Gate` 要求绑表；概览用内联绑表 |
| 管理 view 且角色不足 | 踢回概览（不用 Gate 遮罩） |
| 匿名 | 仍可看 `campus`、`board` |

### mock vs live 数据流

```
                  ┌─ mock ──▶ lib/mock.ts（确定性序列）
views / hooks ────┤
                  └─ live ──▶ api/live.ts hooks
                                   → api/client.ts
                                   → /api/v1/*
                                   → 失败：errors.ts 映射；缺端点则降级 UI
```

`client.ts` 行为要点：

1. access JWT 只在内存；`Authorization: Bearer` 仅用户令牌路径。
2. 401 且原请求带用户令牌 → 单飞 `POST /auth/refresh` → 重试一次。
3. 并发 401 共享同一次 refresh，避免 cookie 轮换后被判复用并整 family 吊销。
4. 在途请求计数驱动顶栏进度线；`/auth/refresh` 不计入。

### 管理端调用

```
AdminSection
  → GET /api/v1/admin/session（角色 + 环境角标）
  → 各 *View → admin/api.ts
       → operator：扫描、展示配置、用户列表…
       → admin：邮件密钥、OAuth 密钥、角色变更…
```

侧栏是否显示「系统管理」由 `lib/adminAccess.ts` 根据 `user.role` 判断；**真正门禁在后端**每次现查角色。

---

## 视图 × 后端端点

| 视图 / 能力 | live 数据源 | 缺口时的表现 |
|---|---|---|
| 注册 / 登录 / 登出 / 找回密码 | `POST /auth/register(/code)`、`login`、`logout`、`password/*` | `api/errors.ts` 中文映射 |
| 会话恢复 | `POST /auth/refresh` | 无 cookie = 未登录 |
| 绑表 | `GET /me/meter/preview` → `PUT /me/meter` | 404 / 409 就地红字 |
| 概览余额与累计 | `GET /me/overview` | 「—」+ 状态 pill；stale 提示陈旧 |
| 概览用电柱图 + 同楼线 | `GET /me/series` + `campus/series` 或 `campus/bills` | 空柱；无楼栋则隐藏同楼线 |
| 用电分析 | `GET /me/series`、`/me/bills`、`campus/summary` | 缺账期显示暂无数据 |
| 分时热力 / 时段画像 | `GET /campus/hourly-heatmap`（默认关） | 契约 message / reason_code |
| 数据看板 | `campus/series`、`summary`、`breakdown`、`bills` | 「日」周期默认关 |
| 筛选项 | `GET /campus/scopes` | 仅「全部」 |
| 排行榜 | `GET /campus/rankings`（`self` / `neighbors`） | 空榜 CTA |
| 推送 / 规则 / 记录 | `/me/channels*`、`notification-settings`、`push-logs` | mock 写 localStorage；live 写库 |
| 单表刷新 | `POST /me/refresh`（30s 冷却） | 404 降级清缓存；429 对齐 Retry-After |

契约细节：[`docs/METER-REFRESH.md`](docs/METER-REFRESH.md)、[`docs/USER-PREFERENCES.md`](docs/USER-PREFERENCES.md)、[`docs/FRONTEND-CONFIG.md`](docs/FRONTEND-CONFIG.md)。

---

## 会话模型

| 令牌 | 存放 | 生命周期 | 用途 |
|---|---|---|---|
| access JWT | 内存 | 默认 15 分钟 | `Authorization` 访问 `/me/*` |
| refresh JWT | HttpOnly cookie，`Path=/api/v1/auth`，SameSite=Strict | 默认 30 天 | 轮换 access；禁止进 JSON/localStorage |

- **前端必须与 API 同源**（开发走 Vite 代理；生产同源反代）。
- `campus/*` 匿名：未登录可看看板与排行榜。
- 浏览器**禁止**注入 `ADMIN_TOKEN`。`VITE_API_TOKEN` 仅本机隧道调试运维端点。

---

## 特性开关

`src/config/features.ts`：

1. 内置默认值（可离线运行）。
2. live 启动时 `GET /api/v1/frontend-config` 深合并。
3. 失败则保持默认，不白屏。

当前常见默认：

- 邮箱登录 / 注册 / 邮箱验证码：开
- 短信登录与短信渠道：关或 Coming Soon
- `charts.day_range` / `hourly_usage`：关（上游无可靠小时数据）
- 电价展示默认 `"0.62"`（展示用，非账单依据，直至运维确认）

运维在「前端展示 / 推送渠道」修改后，用户刷新即生效。

---

## 概览手动刷新

Hero 右上刷新按钮 + 「更新于 HH:MM:SS」，**30 秒冷却**（进度线 + 倒计时）。

1. live：`POST /api/v1/me/refresh` 向上游拉一次 → 清本地 60s 缓存 → 重取 overview/series。
2. 旧环境 `404/405/501`：降级为仅清缓存重取（仍是真实请求）。
3. `429`：按 `Retry-After` 对齐冷却。
4. mock：无网络，重放动效并更新时间戳。

服务端按表限流是硬约束；前端冷却只是体验层。

---

## 移动端（≤960px）

- 侧栏隐藏；顶栏左侧 32px 菜单按钮。
- 238px 抽屉 + 遮罩；导航后收回。
- hero/split/calwrap 等堆叠规则沿用原型；宽表横向滚动。

---

## 复刻公约

- 样式：内联样式 + `global.css` 变量与 `hv-*` 悬停类 + `data-r` 响应式锚点。
- mock 算式禁止随手改，否则与真数据口径对不上。
- 缺后端能力时：真实降级文案，**不伪造数值、不假装成功**。

---

## 相关文档

| 文档 | 内容 |
|---|---|
| [`docs/README.md`](docs/README.md) | 契约落地索引 |
| [`docs/FRONTEND-CONFIG.md`](docs/FRONTEND-CONFIG.md) | 前端配置字段 |
| [`docs/USER-PREFERENCES.md`](docs/USER-PREFERENCES.md) | 推送与榜单偏好 |
| [`docs/METER-REFRESH.md`](docs/METER-REFRESH.md) | 单表刷新语义 |
| [`docs/AUTH-GAPS.md`](docs/AUTH-GAPS.md) / [`CAMPUS-GAPS.md`](docs/CAMPUS-GAPS.md) | 历史提案（多已接通） |
| [`src/views/archive/README.md`](src/views/archive/README.md) | 归档的分时 UI |
| 仓库根 [`README.md`](../README.md) | 整仓拓扑与调用总览 |
