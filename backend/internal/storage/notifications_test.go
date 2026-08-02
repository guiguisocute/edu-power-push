package storage

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/edu-power-push/edu-power-push/backend/internal/secrets"
)

func TestMaskSecret(t *testing.T) {
	if got := maskSecret("short"); got != "••••••••" {
		t.Fatalf("short secret: %q", got)
	}
	got := maskSecret("abcdefghijklmnop")
	if got != "abcd••••mnop" {
		t.Fatalf("long secret: %q", got)
	}
}

func TestValidateNotificationSettings(t *testing.T) {
	ok := defaultNotificationSettings()
	if err := validateNotificationSettings(ok); err != nil {
		t.Fatal(err)
	}
	bad := ok
	bad.Period = "hourly"
	if err := validateNotificationSettings(bad); err == nil {
		t.Fatal("expected invalid period")
	}
	bad = ok
	bad.PushTime = "25:00"
	if err := validateNotificationSettings(bad); err == nil {
		t.Fatal("expected invalid push_time")
	}
	bad = ok
	bad.ThresholdYuan = "-1"
	if err := validateNotificationSettings(bad); err == nil {
		t.Fatal("expected invalid threshold")
	}
	for _, threshold := range []string{"0", "0.99", "50.01", "51"} {
		bad = ok
		bad.ThresholdYuan = threshold
		if err := validateNotificationSettings(bad); err == nil {
			t.Fatalf("expected threshold %s to be rejected", threshold)
		}
	}
	for _, threshold := range []string{"1", "10", "50"} {
		good := ok
		good.ThresholdYuan = threshold
		if err := validateNotificationSettings(good); err != nil {
			t.Fatalf("expected threshold %s to be accepted: %v", threshold, err)
		}
	}
	bad = ok
	bad.Templates.Digest.Body = "{{.unknown_template_variable}}"
	if err := validateNotificationSettings(bad); err == nil {
		t.Fatal("expected invalid notification template")
	}
}

func TestParseMailRecipients(t *testing.T) {
	got, err := parseMailRecipients([]any{"A@QQ.COM", "a@qq.com", " ", "bad"})
	if err == nil {
		t.Fatal("expected invalid email error")
	}
	got, err = parseMailRecipients([]any{"A@QQ.COM", "b@qq.com", " "})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "a@qq.com" {
		t.Fatalf("got %#v", got)
	}
	tooMany := make([]any, 0, MaxMailRecipients+1)
	for i := 0; i <= MaxMailRecipients; i++ {
		tooMany = append(tooMany, fmt.Sprintf("user%d@example.com", i))
	}
	if _, err := parseMailRecipients(tooMany); err == nil {
		t.Fatalf("accepted more than %d mail recipients", MaxMailRecipients)
	}
}

func TestValidateWebhookURL(t *testing.T) {
	if err := validateWebhookURL("dingtalk", "https://oapi.dingtalk.com/robot/send?access_token=x"); err != nil {
		t.Fatalf("legit dingtalk webhook rejected: %v", err)
	}
	for _, tc := range []struct{ channel, raw string }{
		{"wecom_webhook", "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=693a91f6-7abc-4bc4-97a0-0ec2a5aaa111"},
		{"discord", "https://discord.com/api/webhooks/123456789/secret-token"},
		{"discord", "https://discord.com/api/v10/webhooks/123456789/secret-token"},
		{"whatsapp", "https://api.callmebot.com/whatsapp.php?phone=%2B8613800000000&apikey=secret-token"},
	} {
		if err := validateWebhookURL(tc.channel, tc.raw); err != nil {
			t.Fatalf("legit %s webhook rejected: %v", tc.channel, err)
		}
	}
	rejected := []struct{ name, raw string }{
		// 子串匹配会放过：允许域名在 query 而非 host。
		{"allowlisted host in query", "https://attacker.example/?x=://oapi.dingtalk.com/"},
		{"allowlisted host in path", "https://attacker.example/://oapi.dingtalk.com/send"},
		{"allowlisted host in userinfo", "https://oapi.dingtalk.com@attacker.example/send"},
		{"subdomain lookalike", "https://oapi.dingtalk.com.attacker.example/send"},
		{"plain http", "http://oapi.dingtalk.com/robot/send"},
		{"other vendor", "https://open.feishu.cn/hook"},
	}
	for _, tc := range rejected {
		if err := validateWebhookURL("dingtalk", tc.raw); err == nil {
			t.Errorf("%s: expected rejection for %q", tc.name, tc.raw)
		}
	}
	for _, tc := range []struct{ channel, raw string }{
		{"wecom_webhook", "https://qyapi.weixin.qq.com/cgi-bin/webhook/send"},
		{"wecom_webhook", "https://attacker.example/cgi-bin/webhook/send?key=x"},
		{"discord", "https://discord.com/api/webhooks/only-an-id"},
		{"discord", "https://discord.com.evil.example/api/webhooks/123/token"},
		{"whatsapp", "https://api.callmebot.com/whatsapp.php?phone=1"},
		{"whatsapp", "https://example.com/whatsapp.php?phone=1&apikey=x"},
	} {
		if err := validateWebhookURL(tc.channel, tc.raw); err == nil {
			t.Errorf("expected %s rejection for %q", tc.channel, tc.raw)
		}
	}
	// 无固定域名的渠道仍必须挡住内网目标。
	for _, raw := range []string{
		"https://127.0.0.1/bot", "https://localhost/bot",
		"https://10.0.0.5/bot", "https://[::1]/bot", "https://169.254.169.254/latest/meta-data",
	} {
		if err := validateWebhookURL("telegram", raw); err == nil {
			t.Errorf("expected private host rejection for %q", raw)
		}
	}
	if err := validateWebhookURL("telegram", "https://tg-proxy.example.com/bot"); err != nil {
		t.Fatalf("public proxy rejected: %v", err)
	}
}

func TestTruncateText(t *testing.T) {
	// 按字节切会切开中文 rune。PostgreSQL 拒收整条日志。
	got := TruncateText("邮件服务返回了一段很长的中文错误信息", 5)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated to invalid utf-8: %q", got)
	}
	if got != "邮件服务返…" {
		t.Fatalf("got %q", got)
	}
	if got := TruncateText("short", 100); got != "short" {
		t.Fatalf("unexpected truncation: %q", got)
	}
}

func TestResolveMailRecipients(t *testing.T) {
	// 从未配置。回退账号邮箱。
	c := PushCandidate{Email: "u@qq.com"}
	if got := ResolveMailRecipients(c); len(got) != 1 || got[0] != "u@qq.com" {
		t.Fatalf("fallback: %#v", got)
	}
	// 配置过但关闭。不发。
	c = PushCandidate{Email: "u@qq.com", MailConfigured: true, MailEnabled: false, MailRecipients: []string{"x@qq.com"}}
	if got := ResolveMailRecipients(c); got != nil {
		t.Fatalf("disabled: %#v", got)
	}
	// 启用。
	c = PushCandidate{MailConfigured: true, MailEnabled: true, MailRecipients: []string{"x@qq.com"}}
	if got := ResolveMailRecipients(c); len(got) != 1 || got[0] != "x@qq.com" {
		t.Fatalf("enabled: %#v", got)
	}
}

func TestFilterVerifiedMailRecipients(t *testing.T) {
	got := filterVerifiedMailRecipients(
		[]string{"account@example.com", "verified@example.com", "victim@example.com"},
		"account@example.com",
		map[string]bool{"verified@example.com": true},
	)
	if len(got) != 2 || got[0] != "account@example.com" || got[1] != "verified@example.com" {
		t.Fatalf("verified filter = %#v", got)
	}
	if got := filterVerifiedMailRecipients([]string{"account@example.com"}, "", nil); len(got) != 0 {
		t.Fatalf("unverified account email passed filter: %#v", got)
	}
}

func TestPersonalFreeChannelValidation(t *testing.T) {
	if err := validateChannelConfig("pushplus", map[string]any{
		"token": "0123456789abcdef0123456789abcdef",
	}, true); err != nil {
		t.Fatal(err)
	}
	if err := validateChannelConfig("telegram", map[string]any{
		"token": "123456789:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghi",
		"chat":  "123456789",
	}, true); err != nil {
		t.Fatal(err)
	}
	if err := validateChannelConfig("telegram", map[string]any{
		"token": "123456789:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghi",
		"chat":  "-1001234567890",
	}, true); err != nil {
		t.Fatal(err)
	}
	for _, chat := range []string{"@public_channel", "not-a-chat", "0", "-0"} {
		err := validateChannelConfig("telegram", map[string]any{
			"token": "123456789:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghi",
			"chat":  chat,
		}, true)
		if err == nil {
			t.Fatalf("invalid Telegram chat %q was accepted", chat)
		}
	}
	if err := validateChannelConfig("pushplus", map[string]any{"token": "short"}, true); err == nil {
		t.Fatal("invalid PushPlus token was accepted")
	}
	for _, tc := range []struct{ channel, webhook string }{
		{"dingtalk", "https://oapi.dingtalk.com/robot/send?access_token=x"},
		{"feishu", "https://open.feishu.cn/open-apis/bot/v2/hook/x"},
		{"lark", "https://open.larksuite.com/open-apis/bot/v2/hook/x"},
	} {
		if err := validateChannelConfig(tc.channel, map[string]any{"webhook": tc.webhook, "secret": "secret"}, true); err != nil {
			t.Fatalf("%s rejected: %v", tc.channel, err)
		}
		if err := validateChannelConfig(tc.channel, map[string]any{"webhook": tc.webhook}, true); err == nil {
			t.Fatalf("%s accepted without signing secret", tc.channel)
		}
	}
	for _, tc := range []struct{ channel, webhook string }{
		{"wecom_webhook", "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=693a91f6-7abc-4bc4-97a0-0ec2a5aaa111"},
		{"discord", "https://discord.com/api/webhooks/123456789/secret-token"},
		{"whatsapp", "https://api.callmebot.com/whatsapp.php?phone=%2B8613800000000&apikey=secret-token"},
	} {
		if err := validateChannelConfig(tc.channel, map[string]any{"webhook": tc.webhook}, true); err != nil {
			t.Fatalf("%s rejected: %v", tc.channel, err)
		}
		if err := validateChannelConfig(tc.channel, map[string]any{}, true); err == nil {
			t.Fatalf("%s accepted without webhook", tc.channel)
		}
	}
	for channel, config := range map[string]map[string]any{
		"wecom": {"botid": "bot-1", "secret": "secret-1", "chatid": "chat-1"},
		"mp":    {"appid": "wx-app", "secret": "secret-1", "tpl": "tpl-1", "openid": "openid-1"},
		"qq":    {"appid": "qq-app", "secret": "secret-1", "user_openid": "user-1"},
	} {
		if err := validateChannelConfig(channel, config, true); err != nil {
			t.Fatalf("%s rejected: %v", channel, err)
		}
		delete(config, "secret")
		if err := validateChannelConfig(channel, config, true); err == nil {
			t.Fatalf("%s accepted without secret", channel)
		}
	}
	for _, target := range []string{"group:123456", "user:654321", "123456"} {
		if err := validateChannelConfig("napcat", map[string]any{
			"base_url": "http://192.168.1.100:3000", "target": target,
		}, true); err != nil {
			t.Fatalf("NapCat target %q rejected: %v", target, err)
		}
	}
	for _, target := range []string{"group:", "user:"} {
		if err := validateChannelConfig("napcat", map[string]any{"target": target}, false); err != nil {
			t.Fatalf("disabled NapCat empty target %q rejected: %v", target, err)
		}
		if err := validateChannelConfig("napcat", map[string]any{
			"base_url": "http://192.168.1.100:3000", "target": target,
		}, true); err == nil {
			t.Fatalf("enabled NapCat empty target %q was accepted", target)
		}
	}
	if err := validateChannelConfig("qq", map[string]any{
		"appid": "qq-app", "secret": "secret-1", "group_openid": "legacy-group",
	}, true); err == nil {
		t.Fatal("QQ official channel accepted legacy group_openid without user_openid")
	}
	for _, baseURL := range []string{"ftp://example.com", "http://user:pass@example.com", "https://example.com?token=x"} {
		if err := validateChannelConfig("napcat", map[string]any{
			"base_url": baseURL, "target": "group:123456",
		}, true); err == nil {
			t.Fatalf("invalid NapCat URL %q was accepted", baseURL)
		}
	}
	for channel, sendKey := range map[string]string{
		"serverchan_turbo": "SCT123456789abcdef",
		"serverchan3":      "sctp42t123456789abcdef",
	} {
		if err := validateChannelConfig(channel, map[string]any{"sendkey": sendKey}, true); err != nil {
			t.Fatalf("%s rejected: %v", channel, err)
		}
		if err := validateChannelConfig(channel, map[string]any{"sendkey": "invalid"}, true); err == nil {
			t.Fatalf("%s accepted invalid SendKey", channel)
		}
	}
	for channel, config := range map[string]map[string]any{
		"webhook": {"webhook": "https://notify.example.com/hooks/power", "token": "optional-token"},
		"bark":    {"base_url": "https://api.day.app", "device_key": "bark-device-key"},
		"gotify":  {"base_url": "https://push.example.com", "token": "application-token", "priority": "5"},
	} {
		if err := validateChannelConfig(channel, config, true); err != nil {
			t.Fatalf("%s rejected: %v", channel, err)
		}
	}
	if err := validateChannelConfig("gotify", map[string]any{
		"base_url": "https://push.example.com", "token": "token", "priority": "high",
	}, true); err == nil {
		t.Fatal("Gotify accepted non-integer priority")
	}
	if err := validateChannelConfig("webhook", map[string]any{"webhook": "https://127.0.0.1/hook"}, true); err == nil {
		t.Fatal("generic Webhook accepted a private destination")
	}
}

func TestChannelCredentialsAreSeparatedAndEncrypted(t *testing.T) {
	box, err := secrets.New(strings.Repeat("01", 32))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		channel string
		field   string
		value   string
		config  map[string]any
	}{
		{"pushplus", "token", "0123456789abcdef0123456789abcdef", map[string]any{"token": "0123456789abcdef0123456789abcdef"}},
		{"wecom", "secret", "wecom-secret-value", map[string]any{"botid": "bot-1", "secret": "wecom-secret-value", "chatid": "chat-1"}},
		{"wecom_webhook", "webhook", "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=secret-key", map[string]any{"webhook": "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=secret-key"}},
		{"discord", "webhook", "https://discord.com/api/webhooks/123/secret-token", map[string]any{"webhook": "https://discord.com/api/webhooks/123/secret-token"}},
		{"mp", "secret", "wechat-secret-value", map[string]any{"appid": "wx-app", "secret": "wechat-secret-value", "tpl": "tpl-1", "openid": "openid-1"}},
		{"qq", "secret", "qq-secret-value", map[string]any{"appid": "qq-app", "secret": "qq-secret-value", "user_openid": "user-1"}},
		{"napcat", "token", "napcat-token-value", map[string]any{"base_url": "https://napcat.example.com", "token": "napcat-token-value", "target": "group:123456"}},
		{"whatsapp", "webhook", "https://api.callmebot.com/whatsapp.php?phone=%2B8613800000000&apikey=secret-token", map[string]any{"webhook": "https://api.callmebot.com/whatsapp.php?phone=%2B8613800000000&apikey=secret-token"}},
		{"serverchan_turbo", "sendkey", "SCT123456789abcdef", map[string]any{"sendkey": "SCT123456789abcdef"}},
		{"serverchan3", "sendkey", "sctp42t123456789abcdef", map[string]any{"sendkey": "sctp42t123456789abcdef"}},
		{"webhook", "token", "webhook-bearer-token", map[string]any{"webhook": "https://notify.example.com/hooks/power", "token": "webhook-bearer-token"}},
		{"bark", "device_key", "bark-device-key", map[string]any{"base_url": "https://api.day.app", "device_key": "bark-device-key"}},
		{"gotify", "token", "gotify-application-token", map[string]any{"base_url": "https://push.example.com", "token": "gotify-application-token", "priority": "5"}},
	} {
		publicConfig, secretConfig, err := splitChannelConfig(tc.channel, tc.config)
		if err != nil {
			t.Fatalf("%s: %v", tc.channel, err)
		}
		if _, leaked := publicConfig[tc.field]; leaked {
			t.Fatalf("%s remained in public config", tc.field)
		}
		sealed, err := box.Seal(secretConfig)
		if err != nil {
			t.Fatalf("%s: %v", tc.channel, err)
		}
		raw, _ := json.Marshal(publicConfig)
		if strings.Contains(string(raw), tc.value) || strings.Contains(string(sealed), tc.value) {
			t.Fatalf("%s credential leaked in stored representation", tc.channel)
		}
		keyID := box.KeyID()
		decoded, err := decodeChannelConfig(tc.channel, raw, sealed, &keyID, box)
		if err != nil || decoded[tc.field] != tc.value {
			t.Fatalf("%s decoded=%#v err=%v", tc.channel, decoded, err)
		}
	}
}

func TestPushPlusTestIntervalRespectsProviderLimits(t *testing.T) {
	if got := channelTestInterval("pushplus"); got != 20*time.Minute {
		t.Fatalf("PushPlus interval = %s", got)
	}
	if got := channelTestInterval("telegram"); got != time.Minute {
		t.Fatalf("Telegram interval = %s", got)
	}
}
