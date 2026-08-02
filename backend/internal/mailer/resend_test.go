package mailer

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/edu-power-push/edu-power-push/backend/internal/config"
)

func testResend(t *testing.T, handler http.HandlerFunc) (*Resend, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	provider, err := NewResend(config.Mail{
		From:   "EDU Power Push <noreply@example.com>",
		Resend: config.ResendMail{APIKey: "re_test_key"},
	})
	if err != nil {
		t.Fatalf("NewResend: %v", err)
	}
	provider.client = server.Client()
	provider.endpoint = server.URL
	return provider, server
}

func TestResendRequiresAPIKeyAndSender(t *testing.T) {
	if _, err := NewResend(config.Mail{From: "a@example.com"}); err == nil {
		t.Error("missing API key was accepted")
	}
	if _, err := NewResend(config.Mail{Resend: config.ResendMail{APIKey: "re_x"}}); err == nil {
		t.Error("missing MAIL_FROM was accepted")
	}
}

func TestResendSendsExpectedPayload(t *testing.T) {
	var captured resendRequest
	var authorization string
	provider, _ := testResend(t, func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"3f1c-message-id"}`))
	})
	id, err := provider.Send(context.Background(), Message{
		Recipient: "student@example.com", Subject: "标题", HTML: "<p>正文</p>", Text: "正文",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if id != "3f1c-message-id" {
		t.Errorf("message id = %q", id)
	}
	if authorization != "Bearer re_test_key" {
		t.Errorf("Authorization = %q", authorization)
	}
	if captured.From != "EDU Power Push <noreply@example.com>" ||
		len(captured.To) != 1 || captured.To[0] != "student@example.com" ||
		captured.Subject != "标题" || captured.HTML != "<p>正文</p>" {
		t.Errorf("unexpected payload: %+v", captured)
	}
}

// 4xx 不重试；429 与 5xx 才重试。
func TestResendErrorTransience(t *testing.T) {
	tests := map[int]bool{400: false, 401: false, 422: false, 429: true, 500: true, 503: true}
	for status, want := range tests {
		err := &resendError{status: status, message: "boom"}
		if got := isTransientMailError(err); got != want {
			t.Errorf("status %d: transient = %v, want %v", status, got, want)
		}
	}
}

func TestResendErrorDoesNotLeakAPIKey(t *testing.T) {
	provider, _ := testResend(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"name":"validation_error","message":"API key is invalid"}`))
	})
	_, err := provider.Send(context.Background(), Message{
		Recipient: "student@example.com", Subject: "标题", Text: "正文",
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "re_test_key") {
		t.Fatalf("error leaked the API key: %v", err)
	}
	if !strings.Contains(err.Error(), "validation_error") {
		t.Errorf("error lost the provider code: %v", err)
	}
}
