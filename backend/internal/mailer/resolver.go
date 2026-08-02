package mailer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/config"
	"github.com/edu-power-push/edu-power-push/backend/internal/secrets"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

/*
Resolver 缓存邮件供应商实例。
按 system_settings.mail 版本号重建，面板改完无需重启。
版本检查节流 2 秒，避免每封信打库。
*/

const settingsRecheckInterval = 2 * time.Second

type settingsSource interface {
	MailSettings(ctx context.Context) (Settings, map[string]string, int64, error)
	MailSettingsVersion(ctx context.Context) (int64, error)
}

type Resolver struct {
	base   config.Mail
	source settingsSource

	mu          sync.Mutex
	provider    Provider
	merged      config.Mail
	version     int64
	loaded      bool
	lastChecked time.Time
	lastErr     error
}

func NewResolver(base config.Mail, source settingsSource) *Resolver {
	return &Resolver{base: base, source: source}
}

// Current 返回生效供应商与合并配置；nil 表示未配置。
func (r *Resolver) Current(ctx context.Context) (Provider, config.Mail, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if r.loaded && now.Sub(r.lastChecked) < settingsRecheckInterval {
		return r.provider, r.merged, r.lastErr
	}
	version, err := r.source.MailSettingsVersion(ctx)
	if err != nil {
		// 读版本失败时沿用缓存，避免邮件整体停摆。
		if r.loaded {
			return r.provider, r.merged, r.lastErr
		}
		return nil, r.base, err
	}
	r.lastChecked = now
	if r.loaded && version == r.version {
		return r.provider, r.merged, r.lastErr
	}
	settings, secretFields, _, err := r.source.MailSettings(ctx)
	if err != nil {
		if r.loaded {
			return r.provider, r.merged, r.lastErr
		}
		return nil, r.base, err
	}
	merged := settings.Apply(r.base, secretFields)
	provider, buildErr := buildProvider(merged)
	r.provider, r.merged, r.version, r.loaded, r.lastErr = provider, merged, version, true, buildErr
	return provider, merged, buildErr
}

// Invalidate 使下次调用立即重读设置。
func (r *Resolver) Invalidate() {
	r.mu.Lock()
	r.loaded = false
	r.lastChecked = time.Time{}
	r.mu.Unlock()
}

func buildProvider(cfg config.Mail) (Provider, error) {
	switch cfg.Provider {
	case "":
		return nil, nil
	case "smtp":
		return NewSMTP(cfg)
	case "tencent_ses":
		return NewTencentSES(cfg)
	case "resend":
		return NewResend(cfg)
	default:
		return nil, fmt.Errorf("unsupported mail provider %q", cfg.Provider)
	}
}

/* ---- system_settings 数据源 --------------------------------------------- */

// DBSettings 读邮件配置；放本包以避免 storage 反向依赖 mailer。
type DBSettings struct {
	Pool *pgxpool.Pool
	Box  *secrets.Box
}

func (d DBSettings) MailSettingsVersion(ctx context.Context) (int64, error) {
	var version int64
	err := d.Pool.QueryRow(ctx, `SELECT version FROM system_settings WHERE key='mail'`).Scan(&version)
	if err != nil {
		if isNoRows(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read mail settings version: %w", err)
	}
	return version, nil
}

func (d DBSettings) MailSettings(ctx context.Context) (Settings, map[string]string, int64, error) {
	var raw, sealed []byte
	var version int64
	err := d.Pool.QueryRow(ctx,
		`SELECT value,secrets,version FROM system_settings WHERE key='mail'`,
	).Scan(&raw, &sealed, &version)
	if err != nil {
		if isNoRows(err) {
			// 面板未配置时完全走环境变量。
			return Settings{}, map[string]string{}, 0, nil
		}
		return Settings{}, nil, 0, fmt.Errorf("read mail settings: %w", err)
	}
	var settings Settings
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &settings); err != nil {
			return Settings{}, nil, 0, fmt.Errorf("decode mail settings: %w", err)
		}
	}
	fields := map[string]string{}
	if len(sealed) > 0 {
		opened, err := d.Box.Open(sealed)
		if err != nil {
			/* 解密失败禁止回落环境变量，避免面板显示已配置却用错钥。 */
			return settings, nil, version, fmt.Errorf("mail credentials cannot be decrypted: %w", err)
		}
		fields = opened
	}
	return settings, fields, version, nil
}

func isNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
