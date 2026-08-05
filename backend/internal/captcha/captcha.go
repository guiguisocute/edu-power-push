package captcha

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/edu-power-push/edu-power-push/backend/internal/secrets"
	"github.com/edu-power-push/edu-power-push/backend/internal/storage"
)

const (
	ProviderDisabled  = "disabled"
	ProviderTurnstile = "turnstile"

	ActionLogin             = "auth.login"
	ActionRegisterCode      = "auth.register_code"
	ActionPasswordResetCode = "auth.password_reset_code"
	ActionMeterPreview      = "meter.preview"
	ActionAdminProbe        = "admin.captcha_probe"

	SecretTurnstile = "turnstile_secret"
)

var (
	Providers = []string{ProviderDisabled, ProviderTurnstile}
	Actions   = []string{ActionLogin, ActionRegisterCode, ActionPasswordResetCode, ActionMeterPreview}

	ErrRequired    = errors.New("captcha required")
	ErrInvalid     = errors.New("captcha invalid")
	ErrUnavailable = errors.New("captcha unavailable")
)

type Settings struct {
	Provider string   `json:"provider"`
	SiteKey  string   `json:"site_key"`
	Hostname string   `json:"hostname"`
	Actions  []string `json:"actions"`
}

type Config struct {
	Fallback           Settings
	TurnstileSecret    string
	TurnstileVerifyURL string
}

type Effective struct {
	Settings          Settings
	TurnstileSecret   string
	Source            string
	Version           int64
	UpdatedAt         *time.Time
	UpdatedBy         string
	SecretStored      bool
	SecretsUnreadable bool
}

type RequestMeta struct {
	Token    string
	RemoteIP string
}

type Result struct {
	Provider   string
	Passed     bool
	Available  bool
	ErrorCodes []string
}

type Service struct {
	pool   *pgxpool.Pool
	box    *secrets.Box
	cfg    Config
	client *http.Client

	mu       sync.Mutex
	cached   Effective
	cachedAt time.Time
}

func New(cfg Config, pool *pgxpool.Pool, box *secrets.Box) *Service {
	if strings.TrimSpace(cfg.Fallback.Provider) == "" {
		cfg.Fallback.Provider = ProviderDisabled
	}
	return &Service{
		cfg: cfg, pool: pool, box: box,
		client: &http.Client{
			Timeout:       8 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func IsKnownProvider(provider string) bool {
	for _, item := range Providers {
		if provider == item {
			return true
		}
	}
	return false
}

func IsKnownAction(action string) bool {
	if action == ActionAdminProbe {
		return true
	}
	for _, item := range Actions {
		if action == item {
			return true
		}
	}
	return false
}

func TurnstileAction(action string) string {
	return strings.NewReplacer(".", "_", ":", "_").Replace(action)
}

func ValidateSettings(settings Settings) error {
	settings.Provider = normalizedProvider(settings.Provider)
	if !IsKnownProvider(settings.Provider) {
		return fmt.Errorf("provider must be one of %s", strings.Join(Providers, ", "))
	}
	seen := map[string]struct{}{}
	for _, action := range settings.Actions {
		if !IsKnownAction(action) || action == ActionAdminProbe {
			return fmt.Errorf("unknown protected action %q", action)
		}
		if _, ok := seen[action]; ok {
			return fmt.Errorf("duplicate protected action %q", action)
		}
		seen[action] = struct{}{}
	}
	return nil
}

func normalizedProvider(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return ProviderDisabled
	}
	return provider
}

func (s *Service) Invalidate() {
	s.mu.Lock()
	s.cachedAt = time.Time{}
	s.mu.Unlock()
}

func (s *Service) Effective(ctx context.Context) (Effective, error) {
	s.mu.Lock()
	if !s.cachedAt.IsZero() && time.Since(s.cachedAt) < 5*time.Second {
		result := s.cached
		s.mu.Unlock()
		return result, nil
	}
	s.mu.Unlock()

	result, err := s.load(ctx)
	if err != nil {
		return Effective{}, err
	}
	s.mu.Lock()
	s.cached = result
	s.cachedAt = time.Now()
	s.mu.Unlock()
	return result, nil
}

func (s *Service) load(ctx context.Context) (Effective, error) {
	result := Effective{
		Settings:        normalizeSettings(s.cfg.Fallback),
		TurnstileSecret: strings.TrimSpace(s.cfg.TurnstileSecret),
		Source:          "env",
	}
	if s.pool == nil {
		return result, nil
	}
	record, err := storage.GetSetting(ctx, s.pool, s.box, storage.SettingKeyCaptcha)
	if errors.Is(err, storage.ErrSettingNotFound) {
		return result, nil
	}
	if err != nil {
		return Effective{}, err
	}
	var saved Settings
	if len(record.Value) > 0 {
		if err := json.Unmarshal(record.Value, &saved); err != nil {
			return Effective{}, fmt.Errorf("decode captcha settings: %w", err)
		}
	}
	result.Settings = normalizeSettings(saved)
	result.Source = "panel"
	result.Version = record.Version
	result.UpdatedAt = &record.UpdatedAt
	result.UpdatedBy = record.UpdatedBy
	result.SecretsUnreadable = record.SecretsUnreadable
	if secret := strings.TrimSpace(record.Secrets[SecretTurnstile]); secret != "" {
		result.TurnstileSecret = secret
		result.SecretStored = true
	}
	return result, nil
}

func normalizeSettings(settings Settings) Settings {
	settings.Provider = normalizedProvider(settings.Provider)
	settings.SiteKey = strings.TrimSpace(settings.SiteKey)
	settings.Hostname = strings.TrimSpace(settings.Hostname)
	if settings.Actions == nil {
		settings.Actions = []string{}
	}
	return settings
}

func (s *Service) Enabled(ctx context.Context, action string) (Effective, bool, error) {
	effective, err := s.Effective(ctx)
	if err != nil {
		return Effective{}, false, err
	}
	if effective.Settings.Provider == ProviderDisabled {
		return effective, false, nil
	}
	// 凭据不全时当未启用：禁止前端画 widget、后端拦登录，避免把自己锁在门外。
	if !TurnstileReady(effective) {
		return effective, false, nil
	}
	for _, item := range effective.Settings.Actions {
		if item == action {
			return effective, true, nil
		}
	}
	return effective, false, nil
}

// TurnstileReady 表示 provider=turnstile 且 site_key / secret 都已填。
// 任一缺失时公开配置应伪装成 disabled，校验也应直接放行。
func TurnstileReady(effective Effective) bool {
	if effective.Settings.Provider != ProviderTurnstile {
		return effective.Settings.Provider != ProviderDisabled
	}
	return strings.TrimSpace(effective.Settings.SiteKey) != "" && strings.TrimSpace(effective.TurnstileSecret) != ""
}

// PublicSettings 给匿名 /captcha/config 用。凭据不全时伪装成 disabled，
// 前端就不会画死 widget、也不会把登录按钮置灰。
func PublicSettings(effective Effective) Settings {
	if effective.Settings.Provider == ProviderTurnstile && !TurnstileReady(effective) {
		return Settings{Provider: ProviderDisabled, SiteKey: "", Hostname: "", Actions: []string{}}
	}
	return effective.Settings
}

// turnstileCredentialBroken 识别 Cloudflare 明确返回的密钥类错误。
// 真人校验失败（invalid-input-response 等）不在此列，仍应拒绝。
func turnstileCredentialBroken(codes []string) bool {
	for _, code := range codes {
		switch strings.ToLower(strings.TrimSpace(code)) {
		case "missing-input-secret", "invalid-input-secret":
			return true
		}
	}
	return false
}

// passOpen 基础设施坏了时放行。Passed=true 让调用方当成功；
// Available=false 便于日志与管理面板区分「真过了」和「兜底放行」。
func passOpen(provider string, codes []string) (Result, error) {
	return Result{Provider: provider, Passed: true, Available: false, ErrorCodes: codes}, nil
}

func (s *Service) Verify(ctx context.Context, effective Effective, action string, meta RequestMeta) (Result, error) {
	switch effective.Settings.Provider {
	case ProviderDisabled:
		return Result{Provider: ProviderDisabled, Passed: true, Available: true}, nil
	case ProviderTurnstile:
		return s.verifyTurnstile(ctx, effective, action, meta)
	default:
		// 未知 provider 等同配置错误，放行以免锁死。
		return passOpen(effective.Settings.Provider, nil)
	}
}

func (s *Service) verifyTurnstile(ctx context.Context, effective Effective, action string, meta RequestMeta) (Result, error) {
	// 密钥/站点键没配齐：直接放行。Enabled() 通常已短路，这里再兜一层。
	if !TurnstileReady(effective) {
		return passOpen(ProviderTurnstile, []string{"not_configured"})
	}
	token := strings.TrimSpace(meta.Token)
	if token == "" {
		// 无 token 一律 fail-open。
		// 场景：widget 红字「无法连接到网站」、site_key 域名不对、脚本被拦——
		// 前端只能空 token 提交；若这里再 captcha_required，就是「已跳过」却登不进。
		// 有 token 时仍走严格 siteverify（伪造/过期拒绝；密钥错误放行）。
		// 代价：机器人可不交 token 绕过。修好 Turnstile 后请在面板确认配置或临时关闭。
		return passOpen(ProviderTurnstile, []string{"missing_token_fail_open"})
	}
	return s.siteverify(ctx, effective, action, token, meta.RemoteIP)
}

func (s *Service) siteverify(ctx context.Context, effective Effective, action, token, remoteIP string) (Result, error) {
	result, err := s.siteverifyRaw(ctx, effective.TurnstileSecret, token, remoteIP)
	if err != nil {
		return passOpen(ProviderTurnstile, []string{"upstream_unreachable"})
	}
	if !result.success {
		// 密钥填错：放行。令牌伪造/过期：拒绝。
		if turnstileCredentialBroken(result.ErrorCodes) {
			return passOpen(ProviderTurnstile, result.ErrorCodes)
		}
		return Result{Provider: ProviderTurnstile, Available: true, ErrorCodes: result.ErrorCodes}, ErrInvalid
	}
	if result.Action != "" && result.Action != TurnstileAction(action) {
		return Result{Provider: ProviderTurnstile, Available: true, ErrorCodes: result.ErrorCodes}, ErrInvalid
	}
	if effective.Settings.Hostname != "" && !strings.EqualFold(result.Hostname, effective.Settings.Hostname) {
		return Result{Provider: ProviderTurnstile, Available: true, ErrorCodes: result.ErrorCodes}, ErrInvalid
	}
	return Result{Provider: ProviderTurnstile, Passed: true, Available: true, ErrorCodes: result.ErrorCodes}, nil
}

type siteverifyPayload struct {
	success    bool
	Hostname   string
	Action     string
	ErrorCodes []string
}

func (s *Service) siteverifyRaw(ctx context.Context, secret, token, remoteIP string) (siteverifyPayload, error) {
	form := url.Values{"secret": {secret}, "response": {token}}
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.TurnstileVerifyURL, strings.NewReader(form.Encode()))
	if err != nil {
		return siteverifyPayload{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := s.client.Do(req)
	if err != nil {
		return siteverifyPayload{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return siteverifyPayload{}, fmt.Errorf("turnstile returned HTTP %d", response.StatusCode)
	}
	var payload struct {
		Success    bool     `json:"success"`
		Hostname   string   `json:"hostname"`
		Action     string   `json:"action"`
		ErrorCodes []string `json:"error-codes"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&payload); err != nil {
		return siteverifyPayload{}, err
	}
	return siteverifyPayload{
		success:    payload.Success,
		Hostname:   payload.Hostname,
		Action:     payload.Action,
		ErrorCodes: payload.ErrorCodes,
	}, nil
}
