package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestGoogleAuthorizeURLCarriesPKCE(t *testing.T) {
	client, err := New(ProviderGoogle, "id", "secret")
	if err != nil {
		t.Fatalf("new google client: %v", err)
	}
	raw := client.AuthorizeURL("https://example.test/api/v1/auth/oauth/google/callback", "state-value", "verifier-value")
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse authorize url: %v", err)
	}
	query := parsed.Query()
	if got := query.Get("code_challenge"); got != PKCEChallenge("verifier-value") {
		t.Fatalf("code_challenge = %q, want the S256 digest of the verifier", got)
	}
	if got := query.Get("code_challenge_method"); got != "S256" {
		t.Fatalf("code_challenge_method = %q, want S256", got)
	}
	if got := query.Get("state"); got != "state-value" {
		t.Fatalf("state = %q", got)
	}
}

// GitHub OAuth App 不支持 PKCE，授权 URL 不得带 code_challenge。
func TestGitHubAuthorizeURLOmitsPKCE(t *testing.T) {
	client, err := New(ProviderGitHub, "id", "secret")
	if err != nil {
		t.Fatalf("new github client: %v", err)
	}
	raw := client.AuthorizeURL("https://example.test/cb", "state-value", "verifier-value")
	if strings.Contains(raw, "code_challenge") {
		t.Fatalf("github authorize url should not carry PKCE: %s", raw)
	}
}

func TestGoogleExchangeReturnsVerifiedProfile(t *testing.T) {
	var sawVerifier string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_ = r.ParseForm()
			sawVerifier = r.Form.Get("code_verifier")
			writeJSON(t, w, map[string]any{"access_token": "at", "token_type": "Bearer"})
		case "/userinfo":
			if r.Header.Get("Authorization") != "Bearer at" {
				t.Errorf("userinfo called without the bearer token")
			}
			writeJSON(t, w, map[string]any{
				"sub": "1234567890", "email": "Student@Example.com",
				"email_verified": true, "name": "同学",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, _ := New(ProviderGoogle, "id", "secret")
	client.Endpoints.Token = server.URL + "/token"
	client.Endpoints.UserInfo = server.URL + "/userinfo"

	profile, err := client.Exchange(context.Background(), "https://example.test/cb", "code", "verifier-value")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if sawVerifier != "verifier-value" {
		t.Fatalf("code_verifier = %q, want it forwarded to the token endpoint", sawVerifier)
	}
	if profile.Subject != "1234567890" || !profile.EmailVerified {
		t.Fatalf("profile = %+v", profile)
	}
	// 邮箱必须小写，对齐库内 lower(email) CHECK。
	if profile.Email != "student@example.com" {
		t.Fatalf("email = %q, want it lowercased", profile.Email)
	}
}

// 账号归属优先 /user/emails 的 primary 且 verified 地址。
func TestGitHubExchangePrefersPrimaryVerifiedEmail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			writeJSON(t, w, map[string]any{"access_token": "at"})
		case "/user":
			writeJSON(t, w, map[string]any{"id": 42, "login": "octocat", "name": "", "email": "public@example.com"})
		case "/user/emails":
			writeJSON(t, w, []map[string]any{
				{"email": "unverified@example.com", "primary": false, "verified": false},
				{"email": "Real@Example.com", "primary": true, "verified": true},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, _ := New(ProviderGitHub, "id", "secret")
	client.Endpoints.Token = server.URL + "/token"
	client.Endpoints.UserInfo = server.URL + "/user"
	client.Endpoints.UserEmails = server.URL + "/user/emails"

	profile, err := client.Exchange(context.Background(), "https://example.test/cb", "code", "")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if profile.Subject != "42" {
		t.Fatalf("subject = %q, want the numeric github id", profile.Subject)
	}
	if profile.Email != "real@example.com" || !profile.EmailVerified {
		t.Fatalf("profile = %+v, want the primary verified email", profile)
	}
	if profile.Nickname != "octocat" {
		t.Fatalf("nickname = %q, want the login when name is empty", profile.Nickname)
	}
}

// 无 verified 邮箱必须失败，禁止认领未验证地址。
func TestGitHubExchangeRejectsUnverifiedEmails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			writeJSON(t, w, map[string]any{"access_token": "at"})
		case "/user":
			writeJSON(t, w, map[string]any{"id": 42, "login": "octocat"})
		case "/user/emails":
			writeJSON(t, w, []map[string]any{{"email": "nope@example.com", "primary": true, "verified": false}})
		}
	}))
	defer server.Close()

	client, _ := New(ProviderGitHub, "id", "secret")
	client.Endpoints.Token = server.URL + "/token"
	client.Endpoints.UserInfo = server.URL + "/user"
	client.Endpoints.UserEmails = server.URL + "/user/emails"

	if _, err := client.Exchange(context.Background(), "https://example.test/cb", "code", ""); err == nil {
		t.Fatal("expected an error when no verified email exists")
	}
}

// GitHub 可以 HTTP 200 在 body 返回 error。
func TestExchangeTreatsBodyErrorAsRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{"error": "bad_verification_code"})
	}))
	defer server.Close()

	client, _ := New(ProviderGitHub, "id", "secret")
	client.Endpoints.Token = server.URL

	_, err := client.Exchange(context.Background(), "https://example.test/cb", "code", "")
	if err == nil || !strings.Contains(err.Error(), "bad_verification_code") {
		t.Fatalf("err = %v, want the provider rejection", err)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Errorf("encode test payload: %v", err)
	}
}
