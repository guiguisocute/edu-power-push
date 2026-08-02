package httpapi

/*
第三方登录凭证管理端点。

	约定与邮件、人机验证相同。
	读：明文原样返回。client secret 仅返回打码值与是否已配置。
	写：字段缺席保留原值。显式 null 清空。给字符串存新值。
	存：AES-GCM 加密。保存后登录端点立刻使用新配置。
*/

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/edu-power-push/edu-power-push/backend/internal/oauth"
	"github.com/edu-power-push/edu-power-push/backend/internal/secrets"
	"github.com/edu-power-push/edu-power-push/backend/internal/storage"
)

type oauthSettingsResponse struct {
	// Settings 是当前生效的明文配置。面板存储优先，空字段用 .env 补。
	// 面板直接渲染进输入框。看到的就是当前真正使用的配置。
	Settings oauth.Settings `json:"settings"`
	Source   string         `json:"source"`
	// RedirectURIs 是要贴进提供方后台的回调地址。
	RedirectURIs map[string]string `json:"redirect_uris"`
	// Configured 表示各提供方是否配齐。Missing 列出仍缺项。
	Configured        map[string]bool     `json:"configured"`
	Missing           map[string][]string `json:"missing"`
	SecretSet         map[string]bool     `json:"secret_set"`
	SecretMasked      map[string]string   `json:"secret_masked"`
	SecretsWritable   bool                `json:"secrets_writable"`
	SecretsUnreadable bool                `json:"secrets_unreadable"`
	// EnvBaseURL 是 .env 兜底站点地址。面板留空时使用它。
	EnvBaseURL string  `json:"env_base_url"`
	UpdatedAt  *string `json:"updated_at"`
	UpdatedBy  string  `json:"updated_by"`
}

func (s *Server) oauthSettingsView(ctx context.Context) (oauthSettingsResponse, error) {
	effective, err := s.oauthSettings.Effective(ctx)
	if err != nil {
		return oauthSettingsResponse{}, err
	}
	response := oauthSettingsResponse{
		Settings: effective.Settings, Source: effective.Source,
		RedirectURIs: map[string]string{}, Configured: map[string]bool{},
		Missing: map[string][]string{}, SecretSet: map[string]bool{}, SecretMasked: map[string]string{},
		SecretsWritable: s.secrets.Enabled(), SecretsUnreadable: effective.SecretsUnreadable,
		EnvBaseURL: s.cfg.OAuth.BaseURL, UpdatedBy: effective.UpdatedBy,
	}
	for _, provider := range oauth.Providers {
		response.RedirectURIs[provider] = effective.RedirectURI(provider)
		response.Configured[provider] = effective.Configured(provider)
		response.Missing[provider] = effective.Missing(provider)
	}
	for field, value := range map[string]string{
		oauth.SecretGoogleClientSecret: effective.GoogleClientSecret,
		oauth.SecretGitHubClientSecret: effective.GitHubClientSecret,
	} {
		response.SecretSet[field] = value != ""
		response.SecretMasked[field] = secrets.Mask(value)
	}
	if effective.UpdatedAt != nil {
		updated := effective.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z")
		response.UpdatedAt = &updated
	}
	return response, nil
}

func (s *Server) getOAuthSettings(w http.ResponseWriter, r *http.Request) {
	response, err := s.oauthSettingsView(r.Context())
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, response)
}

func (s *Server) putOAuthSettings(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Settings oauth.Settings `json:"settings"`
		// 使用 json.RawMessage 区分「未给该栏」与「给了 null」。
		Secrets map[string]json.RawMessage `json:"secrets"`
	}
	if !s.decodeJSON(w, r, &request) {
		return
	}
	request.Settings = oauth.NormalizeSettings(request.Settings)
	if err := oauth.ValidateSettings(request.Settings); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_oauth_settings", err.Error())
		return
	}
	_, stored, err := s.oauthSettings.Stored(r.Context())
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	next := make(map[string]string, len(stored))
	for key, value := range stored {
		next[key] = value
	}
	changed := make([]string, 0, len(request.Secrets))
	for _, field := range oauth.SecretFields {
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
	/* 验证合并环境变量后的结果。
	   只填 ID 忘 Secret 会导致按钮不出现，且日志无提示。 */
	if err := oauth.ValidateEffective(s.oauthSettings.Merge(request.Settings, next)); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "oauth_not_configured", err.Error())
		return
	}
	actor := actorFrom(r.Context())
	if _, err := storage.SaveSetting(
		r.Context(), s.pool, s.secrets, storage.SettingKeyOAuth, request.Settings, next, actor.Label,
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
	s.audit(r, "settings.oauth.save", storage.SettingKeyOAuth, map[string]any{
		"base_url": request.Settings.BaseURL, "secrets_changed": changed,
		"google_client_id_set": request.Settings.GoogleClientID != "",
		"github_client_id_set": request.Settings.GitHubClientID != "",
	})
	s.oauthSettings.Invalidate()
	response, err := s.oauthSettingsView(r.Context())
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, response)
}
