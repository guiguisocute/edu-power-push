package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/edu-power-push/edu-power-push/backend/internal/captcha"
	"github.com/edu-power-push/edu-power-push/backend/internal/config"
)

func TestLoginFailsClosedWhenCaptchaTokenIsMissing(t *testing.T) {
	cfg := testAuthConfig()
	cfg.Captcha = config.Captcha{
		Provider: captcha.ProviderTurnstile, SiteKey: "site", SecretKey: "secret",
		Actions: []string{captcha.ActionLogin}, TurnstileVerifyURL: "http://127.0.0.1:1/siteverify",
	}
	server := New(context.Background(), cfg, nil, nil, nil, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"student@example.com","password":"password"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"code":"captcha_required"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestDisabledCaptchaPreservesExistingHandlerBehavior(t *testing.T) {
	server := New(context.Background(), testAuthConfig(), nil, nil, nil, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"student@example.com","password":"password"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code == http.StatusForbidden && strings.Contains(response.Body.String(), "captcha_") {
		t.Fatalf("disabled captcha changed login behavior: %s", response.Body.String())
	}
}

func TestLoginAllowedWhenTurnstileCredentialsMissing(t *testing.T) {
	// provider=turnstile 但 secret 空：不能拦登录，否则管理员凭证填错会把自己锁在门外。
	cfg := testAuthConfig()
	cfg.Captcha = config.Captcha{
		Provider: captcha.ProviderTurnstile, SiteKey: "site-key-only",
		Actions: []string{captcha.ActionLogin}, TurnstileVerifyURL: "http://127.0.0.1:1/siteverify",
	}
	server := New(context.Background(), cfg, nil, nil, nil, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"student@example.com","password":"password"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code == http.StatusForbidden && strings.Contains(response.Body.String(), "captcha_") {
		t.Fatalf("incomplete turnstile credentials must not block login: %s", response.Body.String())
	}
	if response.Code == http.StatusServiceUnavailable && strings.Contains(response.Body.String(), "captcha_") {
		t.Fatalf("incomplete turnstile credentials must not 503 login: %s", response.Body.String())
	}
}

func TestPublicCaptchaConfigHidesIncompleteTurnstile(t *testing.T) {
	cfg := testAuthConfig()
	cfg.Captcha = config.Captcha{
		Provider: captcha.ProviderTurnstile, SiteKey: "pk_test",
		Actions: []string{captcha.ActionLogin},
	}
	server := New(context.Background(), cfg, nil, nil, nil, nil)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/captcha/config", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"provider":"disabled"`) {
		t.Fatalf("public config must advertise disabled when secret missing: %s", response.Body.String())
	}
}
