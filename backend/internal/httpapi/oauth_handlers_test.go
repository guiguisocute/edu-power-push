package httpapi

import (
	"testing"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/auth"
	"github.com/edu-power-push/edu-power-push/backend/internal/config"
	"github.com/edu-power-push/edu-power-push/backend/internal/storage"
)

// ?next= 决定登录成功后的返回页。原样重定向会造成开放重定向。
func TestSafeReturnPathRejectsOffSiteTargets(t *testing.T) {
	cases := map[string]string{
		"":                        "/",
		"/":                       "/",
		"/usage":                  "/usage",
		"/admin?tab=users":        "/admin?tab=users",
		"//evil.example":          "/",
		"/\\evil.example":         "/",
		"https://evil.example":    "/",
		"http://evil.example/x":   "/",
		"evil.example":            "/",
		"javascript:alert(1)":     "/",
		"/redirect?to=://evil.io": "/",
	}
	for input, want := range cases {
		if got := safeReturnPath(input); got != want {
			t.Fatalf("safeReturnPath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSecureEqual(t *testing.T) {
	if !secureEqual("abc", "abc") {
		t.Error("identical states must compare equal")
	}
	if secureEqual("abc", "abd") || secureEqual("abc", "ab") || secureEqual("", "x") {
		t.Error("different states must not compare equal")
	}
}

// 首次 OAuth 注册可直接使用候选 ID；再次登录必须改用身份表解析出的既有账号 ID。
// 这个不变量同时覆盖 refresh session 外键与 refresh JWT subject。
func TestNewOAuthSessionUsesResolvedUserID(t *testing.T) {
	manager, err := auth.NewManager(config.Auth{
		JWTSecret:  "unit-test-only-jwt-signing-key-material",
		Issuer:     "test",
		AccessTTL:  15 * time.Minute,
		RefreshTTL: 30 * 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{tokens: manager}
	resolvedUserID := auth.NewID()
	refresh, session, err := server.newOAuthSession(storage.OAuthLoginOutcome{
		User: storage.AuthUser{ID: resolvedUserID},
	}, "oauth-regression-test")
	if err != nil {
		t.Fatal(err)
	}
	if session.UserID != resolvedUserID {
		t.Fatalf("refresh session user = %q, want resolved user %q", session.UserID, resolvedUserID)
	}
	claims, err := manager.ParseRefresh(refresh)
	if err != nil {
		t.Fatalf("parse refresh: %v", err)
	}
	if claims.Subject != resolvedUserID {
		t.Fatalf("refresh subject = %q, want resolved user %q", claims.Subject, resolvedUserID)
	}
}
