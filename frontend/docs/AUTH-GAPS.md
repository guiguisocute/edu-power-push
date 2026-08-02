# 认证缺口与鉴权模型（提案）· 供后端使用

> **状态（2026-07-30）**：§0 鉴权模型、§1 注册解耦绑表、§2 找回密码、§3 绑表前电表预览
> **均已上线并接通**。
> `campus/*` 最终采纳匿名开放。
> 产品前端会话为 access JWT 内存 + refresh HttpOnly cookie。
> 注册验证码与找回密码邮件链路已上线。
> 下面各节保留原始论证。
> 落地部分标注了实际结果与差异。

---

## 0. 鉴权模型断层（阻塞项，先定这个）· ✅ 已解决

**实际落地**：采纳了「方案 1」的**匿名开放**分支。
`campus/summary`、`campus/series`、`campus/rankings`、`campus/hourly-heatmap`、
`campus/scopes`、`campus/breakdown`、`campus/bills` 全部 `security: []`
（见 `server.go` 注释与路由注册）。
`meters/*`、`inventory/tree`、`admin/*` 保持运维边界（用户角色或 `ADMIN_TOKEN`）。
配套前端：未登录默认可落在数据看板 / 排行榜。
Gate 只挡概览、用电分析、推送配置。
个人数据一律走 `/me/*`（`api/live.ts`）。

下面是当时的原始论证：

`openapi.yaml` 的 `security:` 覆写只出现在第 42–312 行，
即 `health/*`、`frontend-config`、`auth/*`、`me/*`。
从 `/api/v1/admin/*` 往后的所有路径都回落到全局：

```yaml
security:
  - bearerAuth: []      # description: Internal administrator token from ADMIN_TOKEN.
```

于是这些**产品前端要用**的端点全部被管理员令牌保护：

| 端点 | 前端用在哪 | 当时的 security |
|---|---|---|
| `GET /api/v1/campus/summary` | 数据看板摘要 | `bearerAuth`（ADMIN_TOKEN） |
| `GET /api/v1/campus/series` | 数据看板负荷图 | `bearerAuth` |
| `GET /api/v1/campus/rankings` | 排行榜四榜 | `bearerAuth` |
| `GET /api/v1/campus/hourly-heatmap` | 用电分析热力图槽位 | `bearerAuth` |
| `GET /api/v1/inventory/tree` | 楼栋/楼层筛选项 | `bearerAuth` |
| `GET /api/v1/meters/{meter}/*` | 概览 / 用电分析（早期前端实际在调） | `bearerAuth` |

浏览器里的产品前端禁止持有 `ADMIN_TOKEN`。
前端未登录时**默认落在数据看板**。
匿名用户第一屏就要读 campus 数据。

### 方案

1. **校园聚合数据放开到匿名**：
   `campus/summary`、`campus/series`、`campus/rankings`、`campus/hourly-heatmap`
   改 `security: []`。
   这些是楼栋/楼层级聚合，不含个人身份。
   放开理由与 `frontend-config` 一致。
   如果不接受匿名，则至少改成
   `security: [{ userAccessToken: [] }, { bearerAuth: [] }]`。
   前端把未登录时的默认视图从 campus 改成登录页。
2. **筛选项走新端点**：
   `inventory/tree` 保持管理端（它含全量房间与 excluded 信息）。
   前端改用轻量的 `GET /api/v1/campus/scopes`（见 `CAMPUS-GAPS.md` §1）。
3. **个人数据一律走 `/me/*`**：
   `/api/v1/meters/{meter}/*` 保持管理端不变。
   前端从 `/meters/{m}/overview|series` 迁到 `/me/overview|series`。

---

## 1. 注册解耦电表绑定（**需修改现有 `RegisterRequest`**）· ✅ 已解决

**实际落地**：`meter` 从 `required` 移除。
但**字段保留并标 `deprecated`**（而非删除）。
理由是已发布的契约里它是必填。
直接删会让任何仍在发送该字段的客户端拿 400。
后端在 `meter != ""` 时仍按老路径校验并绑定。
前端注册**不再发送** `meter`。
注册还需先走 `POST /api/v1/auth/register/code` 拿到邮箱验证码。

现契约 `RegisterRequest` 的 `required` 含 `email, password, code`（及可选 `nickname`）。
注册只创建账号。
电表在登录后于「账号用电」页绑定。

理由是收窄匿名面：注册页在登录前。
如果那一步就要求填电表号并回显宿舍地址，
就给匿名访客提供了一个「8 位数字 → 宿舍位置」的查询接口。
8 位表号完全可枚举。
把绑表挪到登录后，这个反查接口天然受会话保护。

### 前端已写好

- `store.tsx` `doRegister()` 只创建账号（`meter: null`），落地视图为概览页。
- **概览页**：不做遮罩，hero 照常渲染。
  余额与三项统计显示「—」。
  状态 pill 显示「未绑定电表」。
  **「绑定电表」按钮内联在大余额右侧**。
  用电量图为空序列，最近推送显示「待绑定」。
- **用电分析 / 推送与配置**：这两页整体以电表为前提。
  复用既有 `Gate` 遮罩的 `mode="bind"` 变体（`components/GateToast.tsx`）。
- **绑表本身不走全屏浮层**：
  `components/InlineBindMeter.tsx` 就地展开在 hero 的余额位置。
  首次绑表与换表是同一个组件，由 `startBind()` 统一入口。
  `AuthView` 已不含 `rebind`。
  全屏浮层只剩登录 / 注册 / 找回密码。

### 邮箱域名白名单（纯前端，不涉及后端）

注册入口只放行知名个人邮箱服务商 + 教育域名后缀，其余拒绝。
名单与判定在 `src/config/emailDomains.ts`，**不产生任何后端请求**。

- 白名单而非黑名单。
  登录**不做**此校验，避免历史账号被挡在门外。
- 这只是注册入口的前置过滤，**不是安全边界**。
  真正的账号有效性仍需后端邮箱验证链路。

---

## 2. 找回密码 `POST /api/v1/auth/password/forgot` + `/reset` · ✅ 已解决

**实际落地**：两端点已在 `server.go` 注册并由 `auth_handlers.go` 使用。
前端 `api/client.ts` 与 `store.tsx` 的 `sendCode()` / `doReset()` 已接 live 路径。
邮件模板 `password_reset.html` 已 embed。
登录页「忘记密码？」在邮箱登录开启时显示（见 `AuthOverlay.tsx`）。
运维仍可通过 `features.auth.email_code` 在面板关掉该能力。

```
POST /api/v1/auth/password/forgot        security: []
{ "email": "you@qq.com" }
→ 204

POST /api/v1/auth/password/reset         security: []
{ "email": "you@qq.com", "code": "123456", "password": "新密码≥8位" }
→ 204
```

- 发码**恒定返回 204**，不区分邮箱是否注册。
  否则接口变成账号枚举器。
- 限流：同邮箱冷却 + 同 IP 限流，`429` + `Retry-After`。
- 验证码 6 位数字，短时有效，一次性。
  存哈希不存明文。
- 成功后**吊销该用户全部 refresh token family**。
  改密码即踢下线所有设备。
- 前端提交成功后跳回登录页，不自动登录。

---

## 3. 绑表前电表预览 `GET /api/v1/me/meter/preview` · ✅ 已解决

**实际落地**：按提案上线，两处与提案不同。
`reason` 的枚举里去掉了 `not_in_inventory`。
不在库存直接 404，无需再用 reason 表达一次。
限流为**按用户每 3 秒 1 次、突发 20**（`previewGate`）。
与提案的「每分钟 20 次」等价而更平滑。

```
GET /api/v1/me/meter/preview?meter=31240718     security: userAccessToken
→ 200
{
  "meter": "31240718",
  "campus": "示例校区",
  "building": "河东 12 栋",
  "floor": "4",
  "room": "402",
  "claimable": true,
  "reason": null
}
```

- `claimable: false` + `reason`: `already_bound` / `excluded`
- **绝不返回余额或任何用电数据。**
- 表号不存在返回 `404`。
- 限流：**按用户**，带会话后限流主体是账号而非 IP。

> **前端已配套**：绑表卡片只显示位置 + 表号（`InlineBindMeter`）。
> mock 与 live 行为一致。

---

## OpenAPI 片段

下列路径已并入 `backend/api/openapi.yaml`。
此处保留提案原文便于对照。

```yaml
  /api/v1/auth/password/forgot:
    post:
      tags: [Auth]
      operationId: requestPasswordReset
      summary: Send a password-reset code; always 204 to prevent account enumeration
      security: []
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [email]
              additionalProperties: false
              properties:
                email: { type: string, format: email, maxLength: 254 }
      responses:
        "204": { description: Accepted regardless of whether the email is registered }
        "400": { $ref: "#/components/responses/BadRequest" }
        "429": { $ref: "#/components/responses/RateLimited" }
        "503": { $ref: "#/components/responses/ServiceUnavailable" }

  /api/v1/auth/password/reset:
    post:
      tags: [Auth]
      operationId: resetPassword
      summary: Reset the password with an emailed code and revoke all sessions
      security: []
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [email, code, password]
              additionalProperties: false
              properties:
                email: { type: string, format: email, maxLength: 254 }
                code: { type: string, pattern: "^[0-9]{6}$" }
                password: { type: string, format: password, minLength: 8, maxLength: 128 }
      responses:
        "204": { description: Password reset; all refresh-token families revoked }
        "400": { $ref: "#/components/responses/BadRequest" }
        "429": { $ref: "#/components/responses/RateLimited" }

  /api/v1/me/meter/preview:
    get:
      tags: [Users]
      operationId: previewMeterForBinding
      summary: Location preview for meter confirmation before binding
      security:
        - userAccessToken: []
      parameters:
        - name: meter
          in: query
          required: true
          schema: { type: string, pattern: "^[0-9]{6,32}$" }
      responses:
        "200":
          description: Location and claimability; never includes readings or balance
          content: { application/json: { schema: { $ref: "#/components/schemas/MeterPreview" } } }
        "401": { $ref: "#/components/responses/Unauthorized" }
        "404": { $ref: "#/components/responses/NotFound" }
        "429": { $ref: "#/components/responses/RateLimited" }
```
