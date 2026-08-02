package captcha

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTurnstileVerification(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("secret") != "test-secret" || r.Form.Get("response") != "good-token" {
			t.Fatalf("unexpected siteverify form: %v", r.Form)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"hostname":"test.example","action":"auth_login"}`))
	}))
	defer server.Close()

	service := New(Config{
		Fallback:        Settings{Provider: ProviderTurnstile, SiteKey: "site-key", Hostname: "test.example", Actions: []string{ActionLogin}},
		TurnstileSecret: "test-secret", TurnstileVerifyURL: server.URL,
	}, nil, nil)
	effective, err := service.Effective(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Verify(context.Background(), effective, ActionLogin, RequestMeta{Token: "good-token"})
	if err != nil || !result.Passed || !result.Available {
		t.Fatalf("verification = %#v, %v", result, err)
	}
}

func TestTurnstileRejectsMissingAndMismatchedTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"hostname":"test.example","action":"wrong.action"}`))
	}))
	defer server.Close()
	service := New(Config{
		Fallback:        Settings{Provider: ProviderTurnstile, SiteKey: "site", Actions: []string{ActionLogin}},
		TurnstileSecret: "secret", TurnstileVerifyURL: server.URL,
	}, nil, nil)
	effective, _ := service.Effective(context.Background())
	if _, err := service.Verify(context.Background(), effective, ActionLogin, RequestMeta{}); !errors.Is(err, ErrRequired) {
		t.Fatalf("missing token error = %v", err)
	}
	if _, err := service.Verify(context.Background(), effective, ActionLogin, RequestMeta{Token: "token"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("action mismatch error = %v", err)
	}
}

func TestTurnstileFailsClosedWhenProviderIsDown(t *testing.T) {
	service := New(Config{
		Fallback:        Settings{Provider: ProviderTurnstile, SiteKey: "site", Actions: []string{ActionLogin}},
		TurnstileSecret: "secret", TurnstileVerifyURL: "http://127.0.0.1:1/siteverify",
	}, nil, nil)
	effective, _ := service.Effective(context.Background())
	result, err := service.Verify(context.Background(), effective, ActionLogin, RequestMeta{Token: "token"})
	if !errors.Is(err, ErrUnavailable) || result.Available {
		t.Fatalf("provider outage = %#v, %v", result, err)
	}
}

func TestValidateSettingsRejectsUnknownAndDuplicateActions(t *testing.T) {
	for _, settings := range []Settings{
		{Provider: "hcaptcha"},
		{Provider: ProviderTurnstile, Actions: []string{"unknown"}},
		{Provider: ProviderTurnstile, Actions: []string{ActionLogin, ActionLogin}},
	} {
		if err := ValidateSettings(settings); err == nil {
			t.Fatalf("expected rejection for %#v", settings)
		}
	}
	if err := ValidateSettings(Settings{Provider: ProviderTurnstile, Actions: append([]string{}, Actions...)}); err != nil {
		t.Fatalf("valid settings rejected: %v", err)
	}
}
