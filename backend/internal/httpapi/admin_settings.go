package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/config"
	"github.com/edu-power-push/edu-power-push/backend/internal/mailer"
	"github.com/edu-power-push/edu-power-push/backend/internal/secrets"
	"github.com/edu-power-push/edu-power-push/backend/internal/storage"
	"github.com/robfig/cron/v3"
)

/*
系统设置端点。目标是在网页填写凭证后即可发信。

	读：明文字段原样返回。凭证仅返回打码值与是否已配置。
	写：字段省略则保留原值。显式 null 则清空。
	存：凭证 AES-GCM 加密。保存后邮件服务立刻重读。
*/

// mailSettingsResponse 是面板看到的邮件设置。
type mailSettingsResponse struct {
	Settings mailer.Settings `json:"settings"`
	// SecretSet 表示各凭证是否已配置。SecretMasked 是打码值，用于核对。
	SecretSet    map[string]bool   `json:"secret_set"`
	SecretMasked map[string]string `json:"secret_masked"`
	// Source 标明生效值来源：env 或 panel。
	Source string `json:"source"`
	// EffectiveProvider 是合并环境变量后真正使用的供应商。
	EffectiveProvider string `json:"effective_provider"`
	// Missing 列出发信前仍缺的配置。空表示配置可用。
	Missing []string `json:"missing"`
	Ready   bool     `json:"ready"`
	// SecretsWritable=false 时面板必须禁用凭证输入框。
	SecretsWritable bool `json:"secrets_writable"`
	// SecretsUnreadable=true 表示库中有密文但当前密钥无法解密。
	SecretsUnreadable bool     `json:"secrets_unreadable"`
	Providers         []string `json:"providers"`
	UpdatedAt         *string  `json:"updated_at"`
	UpdatedBy         string   `json:"updated_by"`
}

func (s *Server) getMailSettings(w http.ResponseWriter, r *http.Request) {
	response, _, err := s.loadMailSettings(r)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, response)
}

// loadMailSettings 供读接口与写接口共用。两边必须看到同一份合并逻辑。
func (s *Server) loadMailSettings(r *http.Request) (mailSettingsResponse, map[string]string, error) {
	response := mailSettingsResponse{
		Source: "env", Providers: mailer.Providers,
		SecretsWritable: s.secrets.Enabled(),
		SecretSet:       map[string]bool{}, SecretMasked: map[string]string{},
	}
	settings := mailer.SettingsFromConfig(s.cfg.Mail)
	stored := map[string]string{}

	record, err := storage.GetSetting(r.Context(), s.pool, s.secrets, storage.SettingKeyMail)
	switch {
	case errors.Is(err, storage.ErrSettingNotFound):
		// 面板尚未配置时回显环境变量那一份。
	case err != nil:
		return response, nil, err
	default:
		response.Source = "panel"
		response.SecretsUnreadable = record.SecretsUnreadable
		stored = record.Secrets
		var saved mailer.Settings
		if len(record.Value) > 0 {
			if err := json.Unmarshal(record.Value, &saved); err != nil {
				return response, nil, err
			}
		}
		settings = saved
		updatedAt := record.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z")
		response.UpdatedAt = &updatedAt
		response.UpdatedBy = record.UpdatedBy
	}

	merged := settings.Apply(s.cfg.Mail, stored)
	response.Settings = settings
	response.EffectiveProvider = merged.Provider
	response.Missing = mailer.Validate(merged)
	response.Ready = len(response.Missing) == 0
	// 凭证仅返回是否已配置与打码值。明文禁止出服务端。
	for _, field := range mailer.SecretFields {
		value := effectiveSecret(field, stored, s.cfg.Mail)
		response.SecretSet[field] = value != ""
		response.SecretMasked[field] = secrets.Mask(value)
	}
	return response, stored, nil
}

// effectiveSecret 返回凭证当前生效值。面板存储优先，否则使用环境变量。
func effectiveSecret(field string, stored map[string]string, base config.Mail) string {
	if v := strings.TrimSpace(stored[field]); v != "" {
		return v
	}
	switch field {
	case mailer.SecretResendAPIKey:
		return base.Resend.APIKey
	case mailer.SecretSMTPPass:
		return base.SMTP.Pass
	case mailer.SecretTencentSecretKey:
		return base.Tencent.SecretKey
	}
	return ""
}

/*
putMailSettings 保存邮件设置。

	凭证三态：
	  字段缺席 → 保留库中原值
	  显式 null → 清空并改回环境变量
	  给字符串 → 存新值
	缺少区分会把未改动密钥写成打码串。
*/
func (s *Server) putMailSettings(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Settings mailer.Settings `json:"settings"`
		// Secrets 使用 json.RawMessage 区分「未给」与「给了 null」。
		Secrets map[string]json.RawMessage `json:"secrets"`
	}
	if !s.decodeJSON(w, r, &request) {
		return
	}
	if !mailer.IsKnownProvider(request.Settings.Provider) {
		s.writeError(w, r, http.StatusBadRequest, "invalid_provider",
			"provider must be one of resend, smtp, tencent_ses (or empty to disable mail)")
		return
	}
	_, stored, err := s.loadMailSettings(r)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	next := map[string]string{}
	for k, v := range stored {
		next[k] = v
	}
	changed := make([]string, 0, len(request.Secrets))
	for _, field := range mailer.SecretFields {
		raw, present := request.Secrets[field]
		if !present {
			continue
		}
		var value *string
		if err := json.Unmarshal(raw, &value); err != nil {
			s.writeError(w, r, http.StatusBadRequest, "invalid_secret", "secret values must be a string or null")
			return
		}
		if value == nil || strings.TrimSpace(*value) == "" {
			delete(next, field)
			changed = append(changed, field+":cleared")
			continue
		}
		trimmed := strings.TrimSpace(*value)
		// 打码值原样回传表示用户未改该栏。禁止把圆点当新密钥存入。
		if strings.Contains(trimmed, "••") {
			continue
		}
		next[field] = trimmed
		changed = append(changed, field)
	}
	if len(next) > 0 && !s.secrets.Enabled() {
		s.writeError(w, r, http.StatusPreconditionFailed, "secrets_unavailable",
			"SETTINGS_ENCRYPTION_KEY is not configured on the server, so credentials cannot be stored")
		return
	}

	merged := request.Settings.Apply(s.cfg.Mail, next)
	missing := mailer.Validate(merged)
	actor := actorFrom(r.Context())
	if _, err := storage.SaveSetting(
		r.Context(), s.pool, s.secrets, storage.SettingKeyMail,
		request.Settings, next, actor.Label,
	); err != nil {
		if errors.Is(err, secrets.ErrNoKey) {
			s.writeError(w, r, http.StatusPreconditionFailed, "secrets_unavailable",
				"SETTINGS_ENCRYPTION_KEY is not configured on the server")
			return
		}
		s.databaseError(w, r, err)
		return
	}
	// 审计仅记变更字段名。禁止记值。
	s.audit(r, "settings.mail.save", storage.SettingKeyMail, map[string]any{
		"provider": request.Settings.Provider, "secrets_changed": changed, "missing": missing,
	})
	// 下次发信立刻使用新凭证。无需等待缓存过期或重启。
	if s.mailer != nil {
		s.mailer.Invalidate()
	}
	response, _, err := s.loadMailSettings(r)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, response)
}

/*
postMailSettingsTest 用当前生效配置真实发信。

	必须当场验证配置。投递失败返回 200 且 ok=false。
	该结果表示已尝试，不是服务端故障。
*/
func (s *Server) postMailSettingsTest(w http.ResponseWriter, r *http.Request) {
	var request struct {
		To string `json:"to"`
	}
	if !s.decodeJSON(w, r, &request) {
		return
	}
	to, ok := normalizeEmail(request.To)
	if !ok {
		s.writeError(w, r, http.StatusBadRequest, "invalid_email", "to must be a valid email address")
		return
	}
	if !s.mailReady(r.Context()) {
		s.writeError(w, r, http.StatusServiceUnavailable, "mail_not_configured",
			"mail provider is not configured; save a working configuration first")
		return
	}
	delivery, err := s.mailer.SendTest(r.Context(), to)
	s.audit(r, "settings.mail.test", to, map[string]any{"ok": err == nil})
	if err != nil {
		s.writeJSON(w, http.StatusOK, map[string]any{
			"ok": false, "error": storage.TruncateText(err.Error(), 300),
		})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "delivery": delivery})
}

/* ---- 扫描器设置 --------------------------------------------------------- */

/*
scannerSettingsResponse 是面板可改的扫描参数。

	上限仍由环境变量把关（SCAN_MAX_QPS / SCAN_MAX_CONCURRENCY）。
	面板供运维调节。禁止一键打挂上游。
*/
type scannerSettingsResponse struct {
	Settings storage.ScannerControlSettings `json:"settings"`
	Source   string                         `json:"source"`
	Limits   struct {
		Shared struct {
			MaxQPS         float64 `json:"max_qps"`
			MaxConcurrency int     `json:"max_concurrency"`
		} `json:"shared"`
		Balance struct {
			MaxQPS         float64 `json:"max_qps"`
			MaxConcurrency int     `json:"max_concurrency"`
		} `json:"balance"`
		Bills struct {
			MaxQPS         float64 `json:"max_qps"`
			MaxConcurrency int     `json:"max_concurrency"`
		} `json:"bills"`
		DailyDetails struct {
			MaxQPS         float64 `json:"max_qps"`
			MaxConcurrency int     `json:"max_concurrency"`
		} `json:"daily_details"`
		// 保留一个前端版本，供旧面板继续渲染上限。
		MaxQPS         float64 `json:"max_qps"`
		MaxConcurrency int     `json:"max_concurrency"`
	} `json:"limits"`
	UpdatedAt *string `json:"updated_at"`
	UpdatedBy string  `json:"updated_by"`
}

func (s *Server) scannerSettingsFromEnv() storage.ScannerControlSettings {
	return storage.ScannerControlSettings{
		Shared: storage.SharedUpstreamSettings{
			QPS: s.cfg.Upstream.GlobalQPS, Concurrency: s.cfg.Upstream.GlobalConcurrency,
		},
		Balance: storage.BalanceScannerSettings{
			Cron: s.cfg.Scan.Cron, BoundCron: s.cfg.Scan.BoundCron, QPS: s.cfg.Scan.QPS,
			Concurrency: s.cfg.Scan.Concurrency, RetryMax: s.cfg.Scan.RetryMax,
			Enabled: s.cfg.Scan.Cron != "" || s.cfg.Scan.BoundCron != "",
		},
		Bills: storage.BatchScannerSettings{
			Cron: s.cfg.Bill.Cron, QPS: s.cfg.Bill.QPS, Concurrency: s.cfg.Bill.Concurrency,
			RetryMax: s.cfg.Bill.RetryMax, MonthRetryMax: s.cfg.Bill.MonthRetryMax,
			Enabled: s.cfg.Bill.Cron != "",
		},
		DailyDetails: storage.DetailScannerSettings{
			BatchScannerSettings: storage.BatchScannerSettings{
				Cron: s.cfg.Detail.Cron, QPS: s.cfg.Detail.QPS, Concurrency: s.cfg.Detail.Concurrency,
				RetryMax: s.cfg.Detail.RetryMax, MonthRetryMax: s.cfg.Detail.MonthRetryMax,
				Enabled: s.cfg.Detail.Cron != "",
			},
			RetryCron: s.cfg.Detail.RetryCron, BootstrapFrom: s.cfg.Detail.BootstrapFrom,
			AutoBootstrap: s.cfg.Detail.AutoBootstrap,
		},
	}
}

func (s *Server) effectiveScannerSettings(ctx context.Context) (storage.ScannerControlSettings, error) {
	record, err := storage.LoadScannerControlSettings(ctx, s.pool, s.secrets, s.scannerSettingsFromEnv())
	if err != nil {
		return storage.ScannerControlSettings{}, err
	}
	return record.Settings, nil
}

func (s *Server) getScannerSettings(w http.ResponseWriter, r *http.Request) {
	record, err := storage.LoadScannerControlSettings(r.Context(), s.pool, s.secrets, s.scannerSettingsFromEnv())
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	response := scannerSettingsResponse{
		Settings: record.Settings, Source: record.Source, UpdatedAt: record.UpdatedAt, UpdatedBy: record.UpdatedBy,
	}
	response.Limits.MaxQPS = s.cfg.Scan.MaxQPS
	response.Limits.MaxConcurrency = s.cfg.Scan.MaxConcurrency
	response.Limits.Shared.MaxQPS = s.cfg.Upstream.MaxQPS
	response.Limits.Shared.MaxConcurrency = s.cfg.Upstream.MaxConcurrency
	response.Limits.Balance.MaxQPS = s.cfg.Scan.MaxQPS
	response.Limits.Balance.MaxConcurrency = s.cfg.Scan.MaxConcurrency
	response.Limits.Bills.MaxQPS = s.cfg.Bill.MaxQPS
	response.Limits.Bills.MaxConcurrency = s.cfg.Bill.MaxConcurrency
	response.Limits.DailyDetails.MaxQPS = s.cfg.Detail.MaxQPS
	response.Limits.DailyDetails.MaxConcurrency = s.cfg.Detail.MaxConcurrency
	s.writeJSON(w, http.StatusOK, response)
}

func (s *Server) putScannerSettings(w http.ResponseWriter, r *http.Request) {
	var request storage.ScannerControlSettings
	if !s.decodeJSON(w, r, &request) {
		return
	}
	if err := s.validateScannerSettings(request); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_scanner_settings", err.Error())
		return
	}
	actor := actorFrom(r.Context())
	if _, err := storage.SaveSetting(
		r.Context(), s.pool, s.secrets, storage.SettingKeyScanner, request, nil, actor.Label,
	); err != nil {
		s.databaseError(w, r, err)
		return
	}
	// 本进程闸门立刻切换。worker 在下次取设置时跟上。
	if s.requestGate != nil {
		if err := s.requestGate.SetLimits(request.Shared.QPS, request.Shared.Concurrency); err != nil {
			s.logger.Error("apply shared upstream gate", "error", err)
		}
	}
	s.audit(r, "settings.scanner.save", storage.SettingKeyScanner, map[string]any{
		"shared": request.Shared, "balance": request.Balance,
		"bills": request.Bills, "daily_details": request.DailyDetails,
	})
	s.getScannerSettings(w, r)
}

func (s *Server) validateScannerSettings(v storage.ScannerControlSettings) error {
	if v.Shared.QPS <= 0 || v.Shared.QPS > s.cfg.Upstream.MaxQPS {
		return errors.New("shared gate qps is outside the server safety limit")
	}
	if v.Shared.Concurrency < 1 || v.Shared.Concurrency > s.cfg.Upstream.MaxConcurrency {
		return errors.New("shared gate concurrency is outside the server safety limit")
	}
	if err := validateBatchScanner("balance", "", false, v.Balance.QPS,
		v.Balance.Concurrency, v.Balance.RetryMax, s.cfg.Scan.MaxQPS, s.cfg.Scan.MaxConcurrency); err != nil {
		return err
	}
	if v.Balance.Enabled {
		fullCron, boundCron := strings.TrimSpace(v.Balance.Cron), strings.TrimSpace(v.Balance.BoundCron)
		if fullCron == "" && boundCron == "" {
			return errors.New("balance needs a full or bound cron when scheduling is enabled")
		}
		if fullCron != "" {
			if err := validateCron(fullCron); err != nil {
				return errors.New("balance full cron: " + err.Error())
			}
		}
		if boundCron != "" {
			if err := validateCron(boundCron); err != nil {
				return errors.New("balance bound cron: " + err.Error())
			}
		}
	}
	if err := validateBatchScanner("bills", v.Bills.Cron, v.Bills.Enabled, v.Bills.QPS,
		v.Bills.Concurrency, v.Bills.RetryMax, s.cfg.Bill.MaxQPS, s.cfg.Bill.MaxConcurrency); err != nil {
		return err
	}
	if v.Bills.MonthRetryMax < 0 {
		return errors.New("bills month_retry_max must not be negative")
	}
	if err := validateBatchScanner("daily details", v.DailyDetails.Cron, v.DailyDetails.Enabled,
		v.DailyDetails.QPS, v.DailyDetails.Concurrency, v.DailyDetails.RetryMax,
		s.cfg.Detail.MaxQPS, s.cfg.Detail.MaxConcurrency); err != nil {
		return err
	}
	if v.DailyDetails.MonthRetryMax < 0 {
		return errors.New("daily details month_retry_max must not be negative")
	}
	if v.DailyDetails.RetryCron != "" {
		if err := validateCron(v.DailyDetails.RetryCron); err != nil {
			return errors.New("daily details retry cron: " + err.Error())
		}
	}
	if _, err := time.Parse("2006-01", v.DailyDetails.BootstrapFrom); err != nil {
		return errors.New("daily details bootstrap_from must be YYYY-MM")
	}
	return nil
}

func validateBatchScanner(name, cronExpr string, enabled bool, qps float64, concurrency, retryMax int, maxQPS float64, maxConcurrency int) error {
	if qps <= 0 || qps > maxQPS {
		return errors.New(name + " qps is outside the server safety limit")
	}
	if concurrency <= 0 || concurrency > maxConcurrency {
		return errors.New(name + " concurrency is outside the server safety limit")
	}
	if retryMax < 0 {
		return errors.New(name + " retry_max must not be negative")
	}
	if enabled {
		if err := validateCron(cronExpr); err != nil {
			return errors.New(name + " cron: " + err.Error())
		}
	}
	return nil
}

func validateCron(expr string) error {
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) != 5 {
		return errors.New("cron must have 5 fields, e.g. \"0 */12 * * *\"")
	}
	if _, err := cron.ParseStandard(expr); err != nil {
		return errors.New("invalid cron expression: " + err.Error())
	}
	return nil
}
