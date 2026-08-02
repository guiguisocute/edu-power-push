-- 邮件通道增加 resend。
-- CHECK 必须同步放开，否则审计写入 23514。
ALTER TABLE mail_deliveries DROP CONSTRAINT IF EXISTS mail_deliveries_provider_check;
ALTER TABLE mail_deliveries ADD CONSTRAINT mail_deliveries_provider_check
    CHECK (provider IN ('smtp', 'tencent_ses', 'resend'));
