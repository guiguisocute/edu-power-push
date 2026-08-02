# 产品邮件模板

本目录是产品邮件 HTML 的**唯一源**。样式与占位符只在这里维护，用 `go:embed` 编进二进制。

---

## 为什么放在 backend 内

1. **Docker 构建上下文**是 `backend/`。放在仓库根的文件进不了镜像。
2. 容器以**只读根文件系统**运行；运行时不读外部路径。模板必须进二进制。

历史上根目录曾另有一份「设计源」与此处逐字节相同。双源迟早分叉，因此只保留本目录。

---

## 在调用链中的位置

```
事件发生
  ├─ 用户注册 / 找回密码 / 换邮箱 / 附加收件人验证
  │     → httpapi 发码
  │     → mailer.Service.Send*
  │     → templates.go 读 embed 的 HTML，替换 {{name}}
  │
  ├─ 低额预警 / 用电摘要（worker push.Engine，渠道 = mail）
  │     → mailer 使用 balance_alert / usage_summary
  │
  ├─ 管理员测试邮件
  │     → POST /api/v1/admin/mail/test?template=…
  │
  └─ 解绑 / 停用 / 注销等账号事件
        → 对应 account_* / meter_unbound 模板
```

供应商（Resend / SMTP / 腾讯云 SES）由 `mailer.Resolver` 按 `system_settings` + 环境变量解析；**模板与供应商无关**，同一 HTML 走不同发送通道。

---

## 文件与占位符

| 文件 | 用途 | 占位符 |
|---|---|---|
| `balance_alert.html` | 低余额预警 | `balance` `threshold` `unsubscribe_url` |
| `usage_summary.html` | 定时用电摘要 | `balance` `usage` `period` `unsubscribe_url` |
| `verification_code.html` | 注册验证码 | `code` `expire_minutes` `base_url` |
| `notification_recipient_verification.html` | 附加推送邮箱验证 | `code` `expire_minutes` `base_url` |
| `password_reset.html` | 找回密码验证码 | `code` `expire_minutes` `base_url` |
| `push_test.html` | 渠道测试 | `balance` `updated_at` `location` `meter` `base_url` |
| `meter_unbound.html` | 电表解绑通知 | `meter` `location` `unbound_at` `base_url` |
| `account_disabled.html` | 账号停用 | `email` `disabled_at` `reason` `base_url` |
| `account_deleted.html` | 账号注销 | `email` `deleted_at` `base_url` |
| `brand-mark.png` | 页眉品牌图 | （静态资源） |

替换逻辑在上级 `../templates.go`：

- 占位符形式：`{{name}}`
- 所有值先做 HTML 转义再写入
- **任一必填值缺失则整封不发**，禁止把未替换的 `{{code}}` 发给用户

---

## 如何预览

1. 配置可用的 `MAIL_PROVIDER`（开发可用 Mailpit / SMTP）。
2. 调用 `POST /api/v1/admin/mail/test`，`template` 取上表文件名（无 `.html` 后缀，与代码枚举一致）。
3. 运维面板「推送渠道」页也可选模板发测试信。

改样式：直接改本目录 HTML，重新编译 `api` / `worker` 即可，无需同步其他副本。
