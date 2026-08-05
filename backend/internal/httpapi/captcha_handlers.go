package httpapi

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/edu-power-push/edu-power-push/backend/internal/captcha"
	"github.com/edu-power-push/edu-power-push/backend/internal/secrets"
	"github.com/edu-power-push/edu-power-push/backend/internal/storage"
)

type captchaSettingsResponse struct {
	Settings          captcha.Settings  `json:"settings"`
	Source            string            `json:"source"`
	Providers         []string          `json:"providers"`
	ProtectedActions  []string          `json:"protected_actions"`
	SecretSet         map[string]bool   `json:"secret_set"`
	SecretMasked      map[string]string `json:"secret_masked"`
	SecretsWritable   bool              `json:"secrets_writable"`
	SecretsUnreadable bool              `json:"secrets_unreadable"`
	Missing           []string          `json:"missing"`
	Ready             bool              `json:"ready"`
	UpdatedAt         *string           `json:"updated_at"`
	UpdatedBy         string            `json:"updated_by"`
}

func (s *Server) publicCaptchaConfig(w http.ResponseWriter, r *http.Request) {
	effective, err := s.captcha.Effective(r.Context())
	if err != nil {
		// 读配置失败时对外宣称 disabled，避免前端画死 widget 把登录按钮锁灰。
		s.writeJSON(w, http.StatusOK, map[string]any{
			"provider": captcha.ProviderDisabled,
			"site_key": "",
			"actions":  []string{},
		})
		return
	}
	// 凭据不全时伪装成 disabled（见 captcha.PublicSettings）。
	pub := captcha.PublicSettings(effective)
	s.writeJSON(w, http.StatusOK, map[string]any{
		"provider": pub.Provider,
		"site_key": pub.SiteKey,
		"actions":  pub.Actions,
	})
}

func (s *Server) requireCaptcha(w http.ResponseWriter, r *http.Request, action string) bool {
	effective, enabled, err := s.captcha.Enabled(r.Context(), action)
	if err != nil {
		// 配置读失败：放行。锁死登录比暂时关掉验证更糟。
		s.logger.Error("captcha config unavailable; allowing request", "action", action, "error", err)
		return true
	}
	if !enabled {
		return true
	}
	result, err := s.captcha.Verify(r.Context(), effective, action, s.captchaRequestMeta(r))
	if err == nil && result.Passed {
		if !result.Available {
			// 凭据错误 / 上游不可达等兜底放行。打日志方便事后修配置。
			s.logger.Error("captcha fail-open", "provider", result.Provider, "action", action, "error_codes", result.ErrorCodes)
		}
		return true
	}
	switch {
	case errors.Is(err, captcha.ErrRequired):
		s.writeCaptchaError(w, r, http.StatusForbidden, "captcha_required", "complete human verification before retrying", result)
	case errors.Is(err, captcha.ErrInvalid):
		s.writeCaptchaError(w, r, http.StatusForbidden, "captcha_invalid", "human verification failed or expired", result)
	default:
		// 未归类错误也放行，避免未知故障锁站。
		s.logger.Error("captcha verification error; allowing request", "provider", effective.Settings.Provider, "action", action, "error", err)
		return true
	}
	return false
}

func (s *Server) captchaRequestMeta(r *http.Request) captcha.RequestMeta {
	return captcha.RequestMeta{
		Token: r.Header.Get("X-Captcha-Token"), RemoteIP: captchaClientIP(r),
	}
}

func captchaClientIP(r *http.Request) string {
	remote := r.RemoteAddr
	if host, _, err := net.SplitHostPort(remote); err == nil {
		remote = host
	}
	peer := net.ParseIP(strings.TrimSpace(remote))
	if peer != nil && (peer.IsLoopback() || peer.IsPrivate()) {
		for _, raw := range []string{r.Header.Get("X-Real-IP"), strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]} {
			if candidate := net.ParseIP(strings.TrimSpace(raw)); candidate != nil {
				return candidate.String()
			}
		}
	}
	if peer != nil {
		return peer.String()
	}
	return ""
}

func (s *Server) writeCaptchaError(w http.ResponseWriter, r *http.Request, status int, code, message string, result captcha.Result) {
	requestID, _ := r.Context().Value(requestIDKey).(string)
	payload := map[string]any{
		"code": code, "message": message, "request_id": requestID,
	}
	if result.Provider != "" {
		payload["provider"] = result.Provider
	}
	s.writeJSON(w, status, map[string]any{"error": payload})
}

func (s *Server) getCaptchaSettings(w http.ResponseWriter, r *http.Request) {
	response, err := s.loadCaptchaSettings(r)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, response)
}

func (s *Server) loadCaptchaSettings(r *http.Request) (captchaSettingsResponse, error) {
	effective, err := s.captcha.Effective(r.Context())
	if err != nil {
		return captchaSettingsResponse{}, err
	}
	response := captchaSettingsResponse{
		Settings: effective.Settings, Source: effective.Source,
		Providers: captcha.Providers, ProtectedActions: captcha.Actions,
		SecretSet:       map[string]bool{captcha.SecretTurnstile: effective.TurnstileSecret != ""},
		SecretMasked:    map[string]string{captcha.SecretTurnstile: secrets.Mask(effective.TurnstileSecret)},
		SecretsWritable: s.secrets.Enabled(), SecretsUnreadable: effective.SecretsUnreadable,
		UpdatedBy: effective.UpdatedBy,
	}
	if effective.UpdatedAt != nil {
		updated := effective.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z")
		response.UpdatedAt = &updated
	}
	response.Missing = captchaSettingsMissing(effective.Settings, effective.TurnstileSecret)
	response.Ready = len(response.Missing) == 0
	return response, nil
}

func captchaSettingsMissing(settings captcha.Settings, turnstileSecret string) []string {
	missing := []string{}
	switch settings.Provider {
	case captcha.ProviderTurnstile:
		if strings.TrimSpace(settings.SiteKey) == "" {
			missing = append(missing, "site_key")
		}
		if strings.TrimSpace(turnstileSecret) == "" {
			missing = append(missing, captcha.SecretTurnstile)
		}
	case captcha.ProviderDisabled:
	default:
		missing = append(missing, "provider")
	}
	return missing
}

func (s *Server) putCaptchaSettings(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Settings captcha.Settings           `json:"settings"`
		Secrets  map[string]json.RawMessage `json:"secrets"`
	}
	if !s.decodeJSON(w, r, &request) {
		return
	}
	request.Settings.Provider = strings.ToLower(strings.TrimSpace(request.Settings.Provider))
	request.Settings.SiteKey = strings.TrimSpace(request.Settings.SiteKey)
	request.Settings.Hostname = strings.TrimSpace(request.Settings.Hostname)
	if err := captcha.ValidateSettings(request.Settings); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_captcha_settings", err.Error())
		return
	}
	stored := map[string]string{}
	if record, err := storage.GetSetting(r.Context(), s.pool, s.secrets, storage.SettingKeyCaptcha); err == nil {
		for key, value := range record.Secrets {
			stored[key] = value
		}
	} else if !errors.Is(err, storage.ErrSettingNotFound) {
		s.databaseError(w, r, err)
		return
	}
	changed := []string{}
	if raw, present := request.Secrets[captcha.SecretTurnstile]; present {
		var value *string
		if err := json.Unmarshal(raw, &value); err != nil {
			s.writeError(w, r, http.StatusBadRequest, "invalid_secret", "secret values must be a string or null")
			return
		}
		if value == nil || strings.TrimSpace(*value) == "" {
			delete(stored, captcha.SecretTurnstile)
			changed = append(changed, captcha.SecretTurnstile+":cleared")
		} else if trimmed := strings.TrimSpace(*value); !strings.Contains(trimmed, "••") {
			stored[captcha.SecretTurnstile] = trimmed
			changed = append(changed, captcha.SecretTurnstile)
		}
	}
	if len(stored) > 0 && !s.secrets.Enabled() {
		s.writeError(w, r, http.StatusPreconditionFailed, "secrets_unavailable", "SETTINGS_ENCRYPTION_KEY is not configured on the server")
		return
	}
	effectiveSecret := strings.TrimSpace(stored[captcha.SecretTurnstile])
	if effectiveSecret == "" {
		effectiveSecret = strings.TrimSpace(s.cfg.Captcha.SecretKey)
	}
	if missing := captchaSettingsMissing(request.Settings, effectiveSecret); len(missing) > 0 && request.Settings.Provider != captcha.ProviderDisabled {
		s.writeError(w, r, http.StatusBadRequest, "captcha_not_configured", "the selected captcha provider is missing required configuration")
		return
	}
	actor := actorFrom(r.Context())
	if _, err := storage.SaveSetting(r.Context(), s.pool, s.secrets, storage.SettingKeyCaptcha, request.Settings, stored, actor.Label); err != nil {
		if errors.Is(err, secrets.ErrNoKey) {
			s.writeError(w, r, http.StatusPreconditionFailed, "secrets_unavailable", "SETTINGS_ENCRYPTION_KEY is not configured on the server")
			return
		}
		s.databaseError(w, r, err)
		return
	}
	s.audit(r, "settings.captcha.save", storage.SettingKeyCaptcha, map[string]any{
		"provider": request.Settings.Provider, "actions": request.Settings.Actions, "secrets_changed": changed,
	})
	s.captcha.Invalidate()
	response, err := s.loadCaptchaSettings(r)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, response)
}

func (s *Server) postCaptchaSettingsTest(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Token string `json:"token"`
	}
	if !s.decodeJSON(w, r, &request) {
		return
	}
	effective, err := s.captcha.Effective(r.Context())
	if err != nil {
		s.writeError(w, r, http.StatusServiceUnavailable, "captcha_unavailable", "human verification configuration is unavailable")
		return
	}
	meta := s.captchaRequestMeta(r)
	meta.Token = request.Token
	result, verifyErr := s.captcha.Verify(r.Context(), effective, captcha.ActionAdminProbe, meta)
	if verifyErr != nil && !errors.Is(verifyErr, captcha.ErrRequired) && !errors.Is(verifyErr, captcha.ErrInvalid) {
		s.writeCaptchaError(w, r, http.StatusServiceUnavailable, "captcha_unavailable", "captcha provider probe failed", result)
		return
	}
	s.audit(r, "settings.captcha.test", result.Provider, map[string]any{"passed": result.Passed, "available": result.Available})
	s.writeJSON(w, http.StatusOK, map[string]any{
		"provider": result.Provider, "available": result.Available, "passed": result.Passed,
	})
}
