CREATE TABLE frontend_config (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    version bigint NOT NULL CHECK (version > 0),
    config jsonb NOT NULL CHECK (jsonb_typeof(config) = 'object'),
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by text NOT NULL
);

CREATE TABLE frontend_config_audit (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    version bigint NOT NULL,
    config jsonb NOT NULL CHECK (jsonb_typeof(config) = 'object'),
    changed_at timestamptz NOT NULL DEFAULT now(),
    changed_by text NOT NULL
);

CREATE INDEX frontend_config_audit_changed_at_idx
    ON frontend_config_audit (changed_at DESC);

WITH inserted AS (
    INSERT INTO frontend_config (singleton, version, config, updated_by)
    VALUES (
        true,
        1,
        '{
          "features": {
            "auth": {
              "email_login": true,
              "sms_login": false,
              "email_code": false,
              "sms_code": false,
              "registration": true
            },
            "channels": {
              "mail": true,
              "sms": false,
              "dingtalk": false,
              "wecom": false,
              "feishu": false,
              "pushplus": false,
              "mp": false,
              "telegram": false
            }
          },
          "display": {
            "campus_name": "主校区",
            "area_name": "",
            "brand_name": "POWER·PUSH"
          }
        }'::jsonb,
        'migration'
    )
    RETURNING version, config, updated_by
)
INSERT INTO frontend_config_audit (version, config, changed_by)
SELECT version, config, updated_by FROM inserted;
