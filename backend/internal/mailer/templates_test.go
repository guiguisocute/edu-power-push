package mailer

import (
	"strings"
	"testing"
)

func TestRenderTemplateFillsPlaceholders(t *testing.T) {
	html, err := RenderTemplate(TemplateBalanceAlert, map[string]string{
		"balance": "12.34", "threshold": "20.00", "unsubscribe_url": "https://example.com/config",
		"meter": "31240718", "building": "12栋", "floor": "4楼", "room": "402",
	})
	if err != nil {
		t.Fatalf("RenderTemplate: %v", err)
	}
	for _, want := range []string{"12.34", "20.00", "https://example.com/config"} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered mail is missing %q", want)
		}
	}
	if strings.Contains(html, "{{") {
		t.Error("rendered mail still contains placeholders")
	}
}

// 缺占位符必须报错，禁止发出 {{code}}。
func TestRenderTemplateRejectsMissingValues(t *testing.T) {
	_, err := RenderTemplate(TemplatePasswordReset, map[string]string{"code": "123456"})
	if err == nil {
		t.Fatal("expected an error for missing values")
	}
	for _, key := range []string{"expire_minutes", "base_url"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error should name the missing key %q: %v", key, err)
		}
	}
}

// 用户可控值必须 HTML 转义。
func TestRenderTemplateEscapesValues(t *testing.T) {
	html, err := RenderTemplate(TemplateUsageSummary, map[string]string{
		"balance": "1", "usage": "2", "unsubscribe_url": "/config",
		"meter": "31240718", "building": "12栋", "floor": "4楼", "room": "402",
		"period": `<script>alert(1)</script>`,
	})
	if err != nil {
		t.Fatalf("RenderTemplate: %v", err)
	}
	if strings.Contains(html, "<script>") {
		t.Fatal("template did not escape an injected tag")
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Error("expected the escaped form in the output")
	}
}

func TestAllTemplatesLoad(t *testing.T) {
	names := []TemplateName{
		TemplateBalanceAlert, TemplateUsageSummary, TemplateVerificationCode, TemplateNotificationRecipientVerification,
		TemplatePasswordReset, TemplatePushTest, TemplateMeterUnbound, TemplateAccountDisabled, TemplateAccountDeleted,
	}
	for _, name := range names {
		raw, err := templateFiles.ReadFile("templates/" + string(name) + ".html")
		if err != nil {
			t.Errorf("template %q is not embedded: %v", name, err)
			continue
		}
		html := string(raw)
		// 模板禁止内联 svg。
		if strings.Contains(html, "<svg") {
			t.Errorf("template %q still contains inline SVG (email clients strip it)", name)
		}
		if !strings.Contains(html, `src="cid:brand-mark"`) {
			t.Errorf("template %q is missing cid:brand-mark brand image", name)
		}
	}
	if len(brandMarkPNGBytes()) < 100 {
		t.Fatal("brand-mark.png is missing or too small")
	}
}

func TestRenderAccountEventTemplates(t *testing.T) {
	tests := []struct {
		name TemplateName
		data map[string]string
		want []string
	}{
		{
			name: TemplateMeterUnbound,
			data: map[string]string{
				"meter": "31240718", "location": "示例校区 · 12栋 · 4楼 · 402",
				"unbound_at": "2026-07-30 10:05", "base_url": "https://example.com/",
			},
			want: []string{"31240718", "12栋", "2026-07-30 10:05", "已解绑"},
		},
		{
			name: TemplateAccountDisabled,
			data: map[string]string{
				"email": "student@example.com", "disabled_at": "2026-07-30 10:06",
				"reason": "管理员在管理面板中停用", "base_url": "https://example.com/",
			},
			want: []string{"student@example.com", "2026-07-30 10:06", "管理员在管理面板中停用", "已禁用"},
		},
		{
			name: TemplateAccountDeleted,
			data: map[string]string{
				"email": "student@example.com", "deleted_at": "2026-07-30 10:07",
				"base_url": "https://example.com/",
			},
			want: []string{"student@example.com", "2026-07-30 10:07", "已删除", "不可恢复"},
		},
	}

	for _, test := range tests {
		t.Run(string(test.name), func(t *testing.T) {
			html, err := RenderTemplate(test.name, test.data)
			if err != nil {
				t.Fatalf("RenderTemplate: %v", err)
			}
			for _, want := range test.want {
				if !strings.Contains(html, want) {
					t.Errorf("rendered template is missing %q", want)
				}
			}
			if strings.Contains(html, "{{") {
				t.Error("rendered template still contains placeholders")
			}
		})
	}
}

func TestRenderPushTestTemplate(t *testing.T) {
	html, err := RenderTemplate(TemplatePushTest, map[string]string{
		"balance":    "43.87 元",
		"updated_at": "2026-07-29 15:08",
		"location":   "示例校区 · 12栋 · 4楼 · 402",
		"meter":      "31240718",
		"base_url":   "https://example.com/",
	})
	if err != nil {
		t.Fatalf("RenderTemplate: %v", err)
	}
	for _, want := range []string{
		"43.87 元", "2026-07-29 15:08", "示例校区 · 12栋 · 4楼 · 402", "31240718", "渠道测试",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered push_test mail is missing %q", want)
		}
	}
	if strings.Contains(html, "{{") {
		t.Error("rendered mail still contains placeholders")
	}
}

func TestPasswordResetTemplateContainsCodeOnly(t *testing.T) {
	html, err := RenderTemplate(TemplatePasswordReset, map[string]string{
		"code": "123456", "expire_minutes": "5", "base_url": "https://example.com/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, "RESET LINK") || strings.Contains(html, "reset=1") {
		t.Fatal("password reset mail must not contain the removed reset link")
	}
}

func TestComposeMIMEIncludesInlineBrand(t *testing.T) {
	html, err := RenderTemplate(TemplatePasswordReset, map[string]string{
		"code": "123456", "expire_minutes": "5",
		"base_url": "https://example.com/",
	})
	if err != nil {
		t.Fatal(err)
	}
	msg := withBrandMark(Message{
		Recipient: "a@example.com", Subject: "test",
		Text: "code 123456", HTML: html,
	})
	raw, _, err := composeMIME("Test <test@example.com>", msg)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, "multipart/related") {
		t.Error("expected multipart/related for inline image")
	}
	// textproto 将 Content-ID 规范为 Content-Id。
	if !strings.Contains(strings.ToLower(s), "content-id: <brand-mark>") {
		t.Error("expected Content-ID for brand mark")
	}
	if !strings.Contains(s, "image/png") {
		t.Error("expected image/png part")
	}
	if !strings.Contains(s, "cid:brand-mark") {
		t.Error("HTML should reference cid:brand-mark")
	}
	if !strings.Contains(s, "iVBORw0KGgo") { // PNG 魔数的 base64 头
		t.Error("expected base64-encoded PNG payload")
	}
}
