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

func TestTurnstileFailsOpenWhenTokenMissing(t *testing.T) {
	// 无 token：fail-open（widget 挂掉时的唯一出路）。
	service := New(Config{
		Fallback:        Settings{Provider: ProviderTurnstile, SiteKey: "site", Actions: []string{ActionLogin}},
		TurnstileSecret: "secret", TurnstileVerifyURL: "http://127.0.0.1:1/siteverify",
	}, nil, nil)
	effective, _ := service.Effective(context.Background())
	result, err := service.Verify(context.Background(), effective, ActionLogin, RequestMeta{})
	if err != nil || !result.Passed || result.Available {
		t.Fatalf("missing token should fail-open = %#v, %v", result, err)
	}
}

func TestTurnstileRejectsMismatchedAction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"hostname":"test.example","action":"wrong.action"}`))
	}))
	defer server.Close()
	service := New(Config{
		Fallback:        Settings{Provider: ProviderTurnstile, SiteKey: "site", Actions: []string{ActionLogin}},
		TurnstileSecret: "secret", TurnstileVerifyURL: server.URL,
	}, nil, nil)
	effective, _ := service.Effective(context.Background())
	if _, err := service.Verify(context.Background(), effective, ActionLogin, RequestMeta{Token: "token"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("action mismatch error = %v", err)
	}
}

func TestTurnstileFailsOpenWhenProviderIsDown(t *testing.T) {
	// 上游挂了时放行，避免人机验证把登录一起带走。
	service := New(Config{
		Fallback:        Settings{Provider: ProviderTurnstile, SiteKey: "site", Actions: []string{ActionLogin}},
		TurnstileSecret: "secret", TurnstileVerifyURL: "http://127.0.0.1:1/siteverify",
	}, nil, nil)
	effective, _ := service.Effective(context.Background())
	result, err := service.Verify(context.Background(), effective, ActionLogin, RequestMeta{Token: "token"})
	if err != nil || !result.Passed || result.Available {
		t.Fatalf("provider outage should fail-open = %#v, %v", result, err)
	}
}

func TestTurnstileFailsOpenWhenCredentialsMissing(t *testing.T) {
	service := New(Config{
		Fallback: Settings{Provider: ProviderTurnstile, SiteKey: "site", Actions: []string{ActionLogin}},
		// secret 故意留空
		TurnstileVerifyURL: "http://127.0.0.1:1/siteverify",
	}, nil, nil)
	effective, err := service.Effective(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Enabled 必须为 false，登录路径根本不碰 Verify。
	_, enabled, err := service.Enabled(context.Background(), ActionLogin)
	if err != nil || enabled {
		t.Fatalf("incomplete credentials must disable captcha, enabled=%v err=%v", enabled, err)
	}
	// 即便直接 Verify，也要放行。
	result, err := service.Verify(context.Background(), effective, ActionLogin, RequestMeta{Token: "token"})
	if err != nil || !result.Passed || result.Available {
		t.Fatalf("missing secret should fail-open = %#v, %v", result, err)
	}
	pub := PublicSettings(effective)
	if pub.Provider != ProviderDisabled || pub.SiteKey != "" {
		t.Fatalf("public settings should look disabled, got %#v", pub)
	}
}

func TestTurnstileFailsOpenOnInvalidSecret(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":false,"error-codes":["invalid-input-secret"]}`))
	}))
	defer server.Close()
	service := New(Config{
		Fallback:        Settings{Provider: ProviderTurnstile, SiteKey: "site", Actions: []string{ActionLogin}},
		TurnstileSecret: "wrong-secret", TurnstileVerifyURL: server.URL,
	}, nil, nil)
	effective, _ := service.Effective(context.Background())
	result, err := service.Verify(context.Background(), effective, ActionLogin, RequestMeta{Token: "token"})
	if err != nil || !result.Passed || result.Available {
		t.Fatalf("invalid secret should fail-open = %#v, %v", result, err)
	}
}

func TestTurnstileStillRejectsBadHumanToken(t *testing.T) {
	// 密钥正确、令牌伪造/过期：仍应拒绝，captcha 不能形同虚设。
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":false,"error-codes":["invalid-input-response","timeout-or-duplicate"]}`))
	}))
	defer server.Close()
	service := New(Config{
		Fallback:        Settings{Provider: ProviderTurnstile, SiteKey: "site", Actions: []string{ActionLogin}},
		TurnstileSecret: "secret", TurnstileVerifyURL: server.URL,
	}, nil, nil)
	effective, _ := service.Effective(context.Background())
	if _, err := service.Verify(context.Background(), effective, ActionLogin, RequestMeta{Token: "forged"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("forged token must stay invalid, err=%v", err)
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
