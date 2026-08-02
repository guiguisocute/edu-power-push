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

	"github.com/edu-power-push/edu-power-push/backend/internal/secrets"
	"github.com/edu-power-push/edu-power-push/backend/internal/storage"
	"github.com/jackc/pgx/v5/pgxpool"
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
	for _, item := range effective.Settings.Actions {
		if item == action {
			return effective, true, nil
		}
	}
	return effective, false, nil
}

func (s *Service) Verify(ctx context.Context, effective Effective, action string, meta RequestMeta) (Result, error) {
	switch effective.Settings.Provider {
	case ProviderDisabled:
		return Result{Provider: ProviderDisabled, Passed: true, Available: true}, nil
	case ProviderTurnstile:
		return s.verifyTurnstile(ctx, effective, action, meta)
	default:
		return Result{Provider: effective.Settings.Provider}, ErrUnavailable
	}
}

func (s *Service) verifyTurnstile(ctx context.Context, effective Effective, action string, meta RequestMeta) (Result, error) {
	result := Result{Provider: ProviderTurnstile, Available: true}
	if strings.TrimSpace(meta.Token) == "" {
		return result, ErrRequired
	}
	if effective.Settings.SiteKey == "" || effective.TurnstileSecret == "" {
		result.Available = false
		return result, ErrUnavailable
	}
	form := url.Values{"secret": {effective.TurnstileSecret}, "response": {strings.TrimSpace(meta.Token)}}
	if meta.RemoteIP != "" {
		form.Set("remoteip", meta.RemoteIP)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.TurnstileVerifyURL, strings.NewReader(form.Encode()))
	if err != nil {
		result.Available = false
		return result, ErrUnavailable
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := s.client.Do(req)
	if err != nil {
		result.Available = false
		return result, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		result.Available = false
		return result, fmt.Errorf("%w: turnstile returned HTTP %d", ErrUnavailable, response.StatusCode)
	}
	var payload struct {
		Success    bool     `json:"success"`
		Hostname   string   `json:"hostname"`
		Action     string   `json:"action"`
		ErrorCodes []string `json:"error-codes"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&payload); err != nil {
		result.Available = false
		return result, fmt.Errorf("%w: decode turnstile response: %v", ErrUnavailable, err)
	}
	result.ErrorCodes = payload.ErrorCodes
	if !payload.Success {
		return result, ErrInvalid
	}
	if payload.Action != "" && payload.Action != TurnstileAction(action) {
		return result, ErrInvalid
	}
	if effective.Settings.Hostname != "" && !strings.EqualFold(payload.Hostname, effective.Settings.Hostname) {
		return result, ErrInvalid
	}
	result.Passed = true
	return result, nil
}
