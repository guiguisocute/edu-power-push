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
