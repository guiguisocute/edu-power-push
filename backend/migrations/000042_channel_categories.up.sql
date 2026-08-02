-- 用户端推送渠道展示分类；组内顺序用 channel_order。
-- 仅对尚未有 channel_categories 的库播种。

UPDATE frontend_config
SET config = jsonb_set(
        config,
        '{features,channel_categories}',
        '[
          {
            "id": "essentials",
            "name": "开箱即用",
            "en": "READY TO USE",
            "desc": "装个 App 或注册一个免费服务，填一个凭证就能收到推送，不用自己部署。",
            "channels": ["mail", "sms", "pushplus", "serverchan_turbo", "serverchan3", "bark", "whatsapp"]
          },
          {
            "id": "group_bots",
            "name": "群聊机器人",
            "en": "GROUP CHAT BOTS",
            "desc": "在钉钉、飞书、企业微信这类群里加一个自定义机器人，把 Webhook 复制过来即可。",
            "channels": ["dingtalk", "feishu", "lark", "wecom", "wecom_webhook", "discord", "telegram"]
          },
          {
            "id": "developer",
            "name": "进阶开发者渠道",
            "en": "SELF-HOSTED & DEVELOPER",
            "desc": "需要自建服务或申请开发者应用，适合愿意折腾的同学。",
            "channels": ["webhook", "gotify", "napcat", "qq", "mp"]
          }
        ]'::jsonb,
        true
    ),
    updated_at = now()
WHERE singleton
  AND COALESCE(jsonb_array_length(config #> '{features,channel_categories}'), 0) = 0;

INSERT INTO frontend_config_audit (version, config, changed_by)
SELECT version + 1, config, 'migration-000042'
FROM frontend_config WHERE singleton;

UPDATE frontend_config SET version = version + 1 WHERE singleton;
