package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/secrets"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

/* 管理面板可改且立即生效的系统设置。见 migrations/000013。
   明文进 value。凭证进 secrets（AES-GCM）。
   缺省回落环境变量。未在面板配置的部署行为不变。 */

const (
	SettingKeyMail    = "mail"
	SettingKeyScanner = "scanner"
	SettingKeyCaptcha = "captcha"
	SettingKeyOAuth   = "oauth"
	// SettingKeySchool 存当前采集学校。含上游实现名、区域与入口地址。
	// 保存后立即生效。与扫描器参数同走 system_settings。
	SettingKeySchool = "school"
)

var ErrSettingNotFound = errors.New("setting not found")

// SettingRecord 为一条设置的原始形态。明文 JSON 加已解开凭证。
type SettingRecord struct {
	Key       string
	Value     json.RawMessage
	Secrets   map[string]string
	Version   int64
	UpdatedAt time.Time
	UpdatedBy string
	// SecretsUnreadable：密文在，但当前密钥解不开。
	// 必须告知上层。禁止显示成未配置后覆盖真凭证。
	SecretsUnreadable bool
}

/*
GetSetting 读取一条设置。

	不存在时返回 ErrSettingNotFound。调用方回落环境变量。
*/
func GetSetting(ctx context.Context, pool *pgxpool.Pool, box *secrets.Box, key string) (SettingRecord, error) {
	var rec SettingRecord
	var raw []byte
	var sealed []byte
	var keyID string
	err := pool.QueryRow(ctx, `
		SELECT key,value,secrets,secrets_key_id,version,updated_at,updated_by
		FROM system_settings WHERE key=$1
	`, key).Scan(&rec.Key, &raw, &sealed, &keyID, &rec.Version, &rec.UpdatedAt, &rec.UpdatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return SettingRecord{}, ErrSettingNotFound
	}
	if err != nil {
		return SettingRecord{}, fmt.Errorf("read setting %s: %w", key, err)
	}
	rec.Value = json.RawMessage(raw)
	rec.Secrets = map[string]string{}
	if len(sealed) > 0 {
		opened, err := box.Open(sealed)
		if err != nil {
			// 解不开不是致命错误。明文仍可用。凭证须重填。
			rec.SecretsUnreadable = true
		} else {
			rec.Secrets = opened
		}
	}
	return rec, nil
}

/*
SaveSetting 写入一条设置并将 version 加一。

	version 供邮件供应商缓存比较。变更后按新凭证重建 provider。无需重启。
*/
func SaveSetting(
	ctx context.Context, pool *pgxpool.Pool, box *secrets.Box,
	key string, value any, secretFields map[string]string, updatedBy string,
) (SettingRecord, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return SettingRecord{}, fmt.Errorf("encode setting %s: %w", key, err)
	}
	sealed, err := box.Seal(secretFields)
	if err != nil {
		return SettingRecord{}, err
	}
	var rec SettingRecord
	var raw []byte
	err = pool.QueryRow(ctx, `
		INSERT INTO system_settings (key,value,secrets,secrets_key_id,version,updated_at,updated_by)
		VALUES ($1,$2::jsonb,$3,$4,1,now(),$5)
		ON CONFLICT (key) DO UPDATE SET
			value=EXCLUDED.value, secrets=EXCLUDED.secrets, secrets_key_id=EXCLUDED.secrets_key_id,
			version=system_settings.version+1, updated_at=now(), updated_by=EXCLUDED.updated_by
		RETURNING key,value,version,updated_at,updated_by
	`, key, payload, sealed, box.KeyID(), updatedBy).
		Scan(&rec.Key, &raw, &rec.Version, &rec.UpdatedAt, &rec.UpdatedBy)
	if err != nil {
		return SettingRecord{}, fmt.Errorf("save setting %s: %w", key, err)
	}
	rec.Value = json.RawMessage(raw)
	rec.Secrets = secretFields
	if rec.Secrets == nil {
		rec.Secrets = map[string]string{}
	}
	return rec, nil
}
