package oauth

/*
第三方登录运行期配置。
明文进 value，client secret 进 AES-GCM secrets。
空字段按项回落环境变量，非整份覆盖。
*/

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/secrets"
	"github.com/edu-power-push/edu-power-push/backend/internal/storage"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	SecretGoogleClientSecret = "google_client_secret"
	SecretGitHubClientSecret = "github_client_secret"
)

var (
	Providers    = []string{ProviderGoogle, ProviderGitHub}
	SecretFields = []string{SecretGoogleClientSecret, SecretGitHubClientSecret}

	// ErrNotConfigured：缺 base URL、client id 或 secret。
	ErrNotConfigured = errors.New("oauth provider is not configured")
)

// Settings 为面板明文部分。client secret 进加密列。
type Settings struct {
	/* BaseURL 生成回调地址，必须与提供方登记 URI 一致。
	   禁止从请求 Host 推导：代理后 Host 可伪造。 */
	BaseURL        string `json:"base_url"`
	GoogleClientID string `json:"google_client_id"`
	GitHubClientID string `json:"github_client_id"`
}

// Fallback 为环境变量兜底值。
type Fallback struct {
	BaseURL            string
	GoogleClientID     string
	GoogleClientSecret string
	GitHubClientID     string
	GitHubClientSecret string
}

// Effective 为合并后生效配置。
type Effective struct {
	Settings           Settings
	GoogleClientSecret string
	GitHubClientSecret string
	// Source 为 env 或 panel。
	Source            string
	Version           int64
	UpdatedAt         *time.Time
	UpdatedBy         string
	SecretsUnreadable bool
}

func (e Effective) clientID(provider string) string {
	if provider == ProviderGitHub {
		return e.Settings.GitHubClientID
	}
	return e.Settings.GoogleClientID
}

func (e Effective) clientSecret(provider string) string {
	if provider == ProviderGitHub {
		return e.GitHubClientSecret
	}
	return e.GoogleClientSecret
}

// Missing 列出仍缺的配置项；空表示已配齐。
func (e Effective) Missing(provider string) []string {
	missing := make([]string, 0, 3)
	if e.Settings.BaseURL == "" {
		missing = append(missing, "base_url")
	}
	if e.clientID(provider) == "" {
		missing = append(missing, provider+"_client_id")
	}
	if e.clientSecret(provider) == "" {
		missing = append(missing, provider+"_client_secret")
	}
	return missing
}

func (e Effective) Configured(provider string) bool {
	if !IsKnownProvider(provider) {
		return false
	}
	return len(e.Missing(provider)) == 0
}

// RedirectURI 由服务端计算并展示，降低手抄错误。
func (e Effective) RedirectURI(provider string) string {
	if e.Settings.BaseURL == "" || !IsKnownProvider(provider) {
		return ""
	}
	return e.Settings.BaseURL + "/api/v1/auth/oauth/" + provider + "/callback"
}

// Client 按生效凭证创建客户端；未配齐返回错误。
func (e Effective) Client(provider string) (*Client, error) {
	if !e.Configured(provider) {
		return nil, fmt.Errorf("%w: %s (missing %s)", ErrNotConfigured, provider, strings.Join(e.Missing(provider), ", "))
	}
	return New(provider, e.clientID(provider), e.clientSecret(provider))
}

func IsKnownProvider(provider string) bool {
	for _, item := range Providers {
		if provider == item {
			return true
		}
	}
	return false
}

// ValidateSettings 校验面板明文。配对检查见 ValidateEffective。
func ValidateSettings(settings Settings) error {
	if settings.BaseURL != "" {
		parsed, err := url.Parse(settings.BaseURL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return errors.New("base_url must be an absolute HTTP(S) URL, e.g. https://power.example.edu")
		}
		if parsed.Path != "" && parsed.Path != "/" {
			return errors.New("base_url must not contain a path")
		}
	}
	for _, id := range []string{settings.GoogleClientID, settings.GitHubClientID} {
		if len(id) > 255 {
			return errors.New("client id is too long")
		}
	}
	return nil
}

// ValidateEffective 拒绝半份凭证，避免按钮静默消失。
func ValidateEffective(effective Effective) error {
	for _, provider := range Providers {
		id, secret := effective.clientID(provider), effective.clientSecret(provider)
		if id == "" && secret == "" {
			continue
		}
		// 错误文案提示可同时清空两项以禁用。
		if id == "" {
			return fmt.Errorf("%s: client id is missing; fill it in, or clear the client secret too to disable %s", provider, provider)
		}
		if secret == "" {
			return fmt.Errorf("%s: client secret is missing; fill it in, or clear the client id too to disable %s", provider, provider)
		}
		if effective.Settings.BaseURL == "" {
			return fmt.Errorf("%s is configured but base_url is empty; the redirect URI cannot be built", provider)
		}
	}
	return nil
}

func NormalizeSettings(settings Settings) Settings {
	settings.BaseURL = strings.TrimRight(strings.TrimSpace(settings.BaseURL), "/")
	settings.GoogleClientID = strings.TrimSpace(settings.GoogleClientID)
	settings.GitHubClientID = strings.TrimSpace(settings.GitHubClientID)
	return settings
}

/*
Service 合并环境变量与面板配置。
缓存 5 秒；保存后 Invalidate 立即生效。
*/
type Service struct {
	pool     *pgxpool.Pool
	box      *secrets.Box
	fallback Fallback

	mu       sync.Mutex
	cached   Effective
	cachedAt time.Time
}

func NewService(fallback Fallback, pool *pgxpool.Pool, box *secrets.Box) *Service {
	return &Service{pool: pool, box: box, fallback: normalizeFallback(fallback)}
}

func normalizeFallback(fallback Fallback) Fallback {
	fallback.BaseURL = strings.TrimRight(strings.TrimSpace(fallback.BaseURL), "/")
	fallback.GoogleClientID = strings.TrimSpace(fallback.GoogleClientID)
	fallback.GoogleClientSecret = strings.TrimSpace(fallback.GoogleClientSecret)
	fallback.GitHubClientID = strings.TrimSpace(fallback.GitHubClientID)
	fallback.GitHubClientSecret = strings.TrimSpace(fallback.GitHubClientSecret)
	return fallback
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
	s.cached, s.cachedAt = result, time.Now()
	s.mu.Unlock()
	return result, nil
}

// Stored 返回面板原样配置，供保存时增量修改。
func (s *Service) Stored(ctx context.Context) (Settings, map[string]string, error) {
	stored := map[string]string{}
	if s.pool == nil {
		return Settings{}, stored, nil
	}
	record, err := storage.GetSetting(ctx, s.pool, s.box, storage.SettingKeyOAuth)
	if errors.Is(err, storage.ErrSettingNotFound) {
		return Settings{}, stored, nil
	}
	if err != nil {
		return Settings{}, nil, err
	}
	var saved Settings
	if len(record.Value) > 0 {
		if err := json.Unmarshal(record.Value, &saved); err != nil {
			return Settings{}, nil, fmt.Errorf("decode oauth settings: %w", err)
		}
	}
	for key, value := range record.Secrets {
		stored[key] = value
	}
	return NormalizeSettings(saved), stored, nil
}

// Merge 合并面板与环境变量；空字段用环境变量补。
func (s *Service) Merge(saved Settings, storedSecrets map[string]string) Effective {
	saved = NormalizeSettings(saved)
	result := Effective{Settings: Settings{
		BaseURL:        firstNonEmpty(saved.BaseURL, s.fallback.BaseURL),
		GoogleClientID: firstNonEmpty(saved.GoogleClientID, s.fallback.GoogleClientID),
		GitHubClientID: firstNonEmpty(saved.GitHubClientID, s.fallback.GitHubClientID),
	}}
	result.GoogleClientSecret = firstNonEmpty(
		strings.TrimSpace(storedSecrets[SecretGoogleClientSecret]), s.fallback.GoogleClientSecret)
	result.GitHubClientSecret = firstNonEmpty(
		strings.TrimSpace(storedSecrets[SecretGitHubClientSecret]), s.fallback.GitHubClientSecret)
	return result
}

func (s *Service) load(ctx context.Context) (Effective, error) {
	if s.pool == nil {
		result := s.Merge(Settings{}, nil)
		result.Source = "env"
		return result, nil
	}
	record, err := storage.GetSetting(ctx, s.pool, s.box, storage.SettingKeyOAuth)
	if errors.Is(err, storage.ErrSettingNotFound) {
		result := s.Merge(Settings{}, nil)
		result.Source = "env"
		return result, nil
	}
	if err != nil {
		return Effective{}, err
	}
	var saved Settings
	if len(record.Value) > 0 {
		if err := json.Unmarshal(record.Value, &saved); err != nil {
			return Effective{}, fmt.Errorf("decode oauth settings: %w", err)
		}
	}
	result := s.Merge(saved, record.Secrets)
	result.Source = "panel"
	result.Version = record.Version
	result.UpdatedAt = &record.UpdatedAt
	result.UpdatedBy = record.UpdatedBy
	result.SecretsUnreadable = record.SecretsUnreadable
	return result, nil
}

func firstNonEmpty(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
