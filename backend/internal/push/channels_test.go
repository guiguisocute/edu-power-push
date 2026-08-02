package push

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/notification"
	"github.com/gorilla/websocket"
)

func TestSignedRobotChannels(t *testing.T) {
	const secret = "SEC-test-signing-key"
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.URL.Path] = true
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if r.URL.Path == "/ding" {
			timestamp := r.URL.Query().Get("timestamp")
			mac := hmac.New(sha256.New, []byte(secret))
			_, _ = mac.Write([]byte(timestamp + "\n" + secret))
			if r.URL.Query().Get("sign") != base64.StdEncoding.EncodeToString(mac.Sum(nil)) {
				t.Fatal("invalid DingTalk signature")
			}
			if body["msgtype"] != "text" {
				t.Fatalf("DingTalk payload=%#v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "errmsg": "ok"})
			return
		}
		if body["msg_type"] != "text" || body["timestamp"] == "" || body["sign"] == "" {
			t.Fatalf("robot payload=%#v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "success"})
	}))
	defer server.Close()
	sender := newChannelSender(server.Client(), server.URL, server.URL+"/pushplus")
	for _, tc := range []struct{ channel, path string }{
		{"dingtalk", "/ding"}, {"feishu", "/feishu"}, {"lark", "/lark"},
	} {
		_, err := sender.Send(context.Background(), tc.channel, map[string]any{
			"webhook": server.URL + tc.path, "secret": secret,
		}, Message{Title: "测试", Body: "签名机器人已接通"})
		if err != nil {
			t.Fatalf("%s: %v", tc.channel, err)
		}
		if !seen[tc.path] {
			t.Fatalf("%s was not called", tc.path)
		}
	}
}

func TestWeComIntelligentBotSend(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		for i := 0; i < 2; i++ {
			var frame map[string]any
			if err := conn.ReadJSON(&frame); err != nil {
				t.Error(err)
				return
			}
			headers := frame["headers"].(map[string]any)
			reqID := headers["req_id"].(string)
			if i == 0 {
				body := frame["body"].(map[string]any)
				if frame["cmd"] != "aibot_subscribe" || body["bot_id"] != "bot-1" || body["secret"] != "secret-1" {
					t.Errorf("auth frame=%#v", frame)
				}
			} else {
				body := frame["body"].(map[string]any)
				if frame["cmd"] != "aibot_send_msg" || body["chatid"] != "chat-1" || body["msgtype"] != "markdown" {
					t.Errorf("send frame=%#v", frame)
				}
			}
			if err := conn.WriteJSON(map[string]any{"headers": map[string]string{"req_id": reqID}, "errcode": 0, "errmsg": "ok"}); err != nil {
				t.Error(err)
				return
			}
		}
	}))
	defer server.Close()
	sender := newChannelSender(server.Client(), server.URL, server.URL)
	sender.wecomWSURL = "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	delivery, err := sender.Send(ctx, "wecom", map[string]any{
		"botid": "bot-1", "secret": "secret-1", "chatid": "chat-1",
	}, Message{Title: "测试", Body: "企业微信已接通"})
	if err != nil {
		t.Fatal(err)
	}
	if delivery.ProviderID == "" {
		t.Fatal("missing WeCom request ID")
	}
}

func TestSimpleGroupWebhookChannels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		switch r.URL.Path {
		case "/cgi-bin/webhook/send":
			if r.URL.Query().Get("key") != "wecom-key" || body["msgtype"] != "text" {
				t.Fatalf("WeCom request = %s %#v", r.URL.String(), body)
			}
			text, _ := body["text"].(map[string]any)
			if !strings.Contains(text["content"].(string), "企业微信消息推送已接通") {
				t.Fatalf("WeCom body=%#v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "errmsg": "ok"})
		case "/api/webhooks/123/token":
			if r.URL.Query().Get("wait") != "true" {
				t.Fatal("Discord delivery must request a confirmed response")
			}
			if !strings.Contains(body["content"].(string), "Discord 已接通") {
				t.Fatalf("Discord body=%#v", body)
			}
			mentions, _ := body["allowed_mentions"].(map[string]any)
			if parse, ok := mentions["parse"].([]any); !ok || len(parse) != 0 {
				t.Fatalf("Discord mentions were not disabled: %#v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "discord-message-1"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	sender := newChannelSender(server.Client(), server.URL, server.URL)
	if _, err := sender.Send(context.Background(), "wecom_webhook", map[string]any{
		"webhook": server.URL + "/cgi-bin/webhook/send?key=wecom-key",
	}, Message{Title: "测试", Body: "企业微信消息推送已接通"}); err != nil {
		t.Fatal(err)
	}
	delivery, err := sender.Send(context.Background(), "discord", map[string]any{
		"webhook": server.URL + "/api/webhooks/123/token",
	}, Message{Title: "测试", Body: "Discord 已接通"})
	if err != nil {
		t.Fatal(err)
	}
	if delivery.ProviderID != "discord-message-1" {
		t.Fatalf("Discord delivery=%#v", delivery)
	}
}

func TestWeChatMPAndQQBotSend(t *testing.T) {
	tokenCalls := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/token":
			tokenCalls["wechat"]++
			if r.URL.Query().Get("appid") != "wx-app" || r.URL.Query().Get("secret") != "wx-secret" {
				t.Fatalf("unexpected WeChat token query")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "wx-token", "expires_in": 7200})
		case "/cgi-bin/message/template/send":
			if r.URL.Query().Get("access_token") != "wx-token" {
				t.Fatal("missing WeChat access token")
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["touser"] != "openid-1" || body["template_id"] != "tpl-1" {
				t.Fatalf("wechat body=%#v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "errmsg": "ok", "msgid": 123})
		case "/app/getAppAccessToken":
			tokenCalls["qq"]++
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "qq-token", "expires_in": "7200"})
		case "/v2/users/user-openid-1/messages":
			if r.Header.Get("Authorization") != "QQBot qq-token" || r.Header.Get("X-Union-Appid") != "qq-app" {
				t.Fatalf("qq headers=%#v", r.Header)
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["msg_type"] != float64(0) || !strings.Contains(body["content"].(string), "QQ 已接通") {
				t.Fatalf("qq body=%#v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "qq-message-1", "timestamp": "2026-07-29T12:00:00+08:00"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	sender := newChannelSender(server.Client(), server.URL, server.URL)
	sender.wechatBaseURL = server.URL
	sender.qqTokenURL = server.URL + "/app/getAppAccessToken"
	sender.qqAPIBaseURL = server.URL

	for i := 0; i < 2; i++ {
		if _, err := sender.Send(context.Background(), "mp", map[string]any{
			"appid": "wx-app", "secret": "wx-secret", "openid": "openid-1", "tpl": "tpl-1",
		}, Message{Title: "测试", Body: "微信已接通"}); err != nil {
			t.Fatal(err)
		}
		if _, err := sender.Send(context.Background(), "qq", map[string]any{
			"appid": "qq-app", "secret": "qq-secret", "user_openid": "user-openid-1",
		}, Message{Title: "测试", Body: "QQ 已接通"}); err != nil {
			t.Fatal(err)
		}
	}
	if tokenCalls["wechat"] != 1 || tokenCalls["qq"] != 1 {
		t.Fatalf("tokens were not cached: %#v", tokenCalls)
	}
}

func TestNapCatSendSupportsGroupPrivateAndAuthFallback(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost || r.URL.Path != "/onebot/send_msg" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if requests == 1 {
			if r.Header.Get("Authorization") != "Bearer napcat-token" {
				t.Fatalf("authorization=%q", r.Header.Get("Authorization"))
			}
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"token verify failed"}`))
			return
		}
		if requests == 2 && r.URL.Query().Get("access_token") != "napcat-token" {
			t.Fatalf("access_token=%q", r.URL.Query().Get("access_token"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		switch requests {
		case 2:
			if body["message_type"] != "group" || body["group_id"] != "123456" || body["auto_escape"] != true {
				t.Fatalf("group body=%#v", body)
			}
		case 3:
			if body["message_type"] != "private" || body["user_id"] != "654321" {
				t.Fatalf("private body=%#v", body)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "ok", "retcode": 0, "data": map[string]any{"message_id": requests},
		})
	}))
	defer server.Close()

	sender := newChannelSender(server.Client(), server.URL, server.URL)
	delivery, err := sender.Send(context.Background(), "napcat", map[string]any{
		"base_url": server.URL + "/onebot", "token": "napcat-token", "target": "group:123456",
	}, Message{Title: "测试", Body: "NapCat 群消息"})
	if err != nil || delivery.ProviderID != "2" {
		t.Fatalf("group delivery=%#v err=%v", delivery, err)
	}
	delivery, err = sender.Send(context.Background(), "napcat", map[string]any{
		"base_url": server.URL + "/onebot", "target": "user:654321",
	}, Message{Title: "测试", Body: "NapCat 私聊消息"})
	if err != nil || delivery.ProviderID != "3" {
		t.Fatalf("private delivery=%#v err=%v", delivery, err)
	}
}

func TestWhatsAppCallMeBotUsesSingleCredentialURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/whatsapp.php" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("phone") != "+8613800000000" || r.URL.Query().Get("apikey") != "secret-key" {
			t.Fatalf("credential query = %q", r.URL.RawQuery)
		}
		if text := r.URL.Query().Get("text"); !strings.Contains(text, "余额预警") || !strings.Contains(text, "请及时充值") {
			t.Fatalf("text = %q", text)
		}
		_, _ = w.Write([]byte("Message queued."))
	}))
	defer server.Close()

	sender := newChannelSender(server.Client(), server.URL, server.URL)
	sender.whatsAppURL = server.URL + "/whatsapp.php"
	delivery, err := sender.Send(context.Background(), "whatsapp", map[string]any{
		"webhook": server.URL + "/whatsapp.php?phone=%2B8613800000000&apikey=secret-key",
	}, Message{Title: "余额预警", Body: "请及时充值"})
	if err != nil {
		t.Fatal(err)
	}
	if !delivery.Async || !strings.Contains(delivery.Note, "CallMeBot") {
		t.Fatalf("delivery = %#v", delivery)
	}
}

func TestServerChanRoutesBothSendKeyFormats(t *testing.T) {
	const turboKey = "SCT123456789abcdef"
	const threeKey = "sctp42t123456789abcdef"
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.URL.Path] = true
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["title"] != "每日摘要" || body["desp"] != "今日用电 3.2 kWh" {
			t.Fatalf("body = %#v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "message": "success"})
	}))
	defer server.Close()

	sender := newChannelSender(server.Client(), server.URL, server.URL)
	sender.serverChanURL = server.URL
	sender.serverChan3URL = server.URL
	for _, tc := range []struct {
		channel string
		key     string
		path    string
	}{
		{"serverchan_turbo", turboKey, "/" + turboKey + ".send"},
		{"serverchan3", threeKey, "/send/" + threeKey + ".send"},
	} {
		delivery, err := sender.Send(context.Background(), tc.channel, map[string]any{"sendkey": tc.key}, Message{
			Title: "每日摘要", Body: "今日用电 3.2 kWh",
		})
		if err != nil {
			t.Fatalf("%s: %v", tc.channel, err)
		}
		if !delivery.Async || !seen[tc.path] {
			t.Fatalf("%s delivery=%#v seen=%#v", tc.channel, delivery, seen)
		}
	}
}

func TestTelegramSendUsesFreeSendMessageOnly(t *testing.T) {
	const token = "123456789:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghi"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/bot"+token+"/sendMessage" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["chat_id"] != "123456789" || !strings.Contains(body["text"].(string), "测试消息") {
			t.Fatalf("unexpected body: %#v", body)
		}
		if _, exists := body["allow_paid_broadcast"]; exists {
			t.Fatal("paid Telegram broadcasting must never be enabled")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": 42}})
	}))
	defer server.Close()

	sender := newChannelSender(server.Client(), server.URL, server.URL+"/pushplus")
	delivery, err := sender.Send(context.Background(), "telegram", map[string]any{
		"token": token, "chat": "123456789",
	}, Message{Title: "测试消息", Body: "免费链路已接通"})
	if err != nil {
		t.Fatal(err)
	}
	if delivery.ProviderID != "42" || delivery.Async {
		t.Fatalf("delivery = %#v", delivery)
	}
}

func TestTelegramDiscoversUniqueGroupChat(t *testing.T) {
	const token = "123456789:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghi"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/bot"+token+"/getUpdates" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("allowed_updates") != `["message"]` {
			t.Fatalf("allowed_updates = %q", r.URL.Query().Get("allowed_updates"))
		}
		if r.URL.Query().Get("offset") != "-100" {
			t.Fatalf("offset = %q", r.URL.Query().Get("offset"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": []any{
				map[string]any{"update_id": 1, "message": map[string]any{"chat": map[string]any{"id": -100246801357, "type": "supergroup"}}},
				map[string]any{"update_id": 2, "message": map[string]any{"chat": map[string]any{"id": -100246801357, "type": "supergroup"}}},
				map[string]any{"update_id": 3, "message": map[string]any{"chat": map[string]any{"id": -100999999999, "type": "channel"}}},
			},
		})
	}))
	defer server.Close()

	sender := newChannelSender(server.Client(), server.URL, server.URL+"/pushplus")
	chatID, err := sender.DiscoverTelegramChat(context.Background(), map[string]any{"token": token})
	if err != nil {
		t.Fatal(err)
	}
	if chatID != "-100246801357" {
		t.Fatalf("chat ID = %q", chatID)
	}
}

func TestTelegramDiscoveryRejectsAmbiguousChats(t *testing.T) {
	const token = "123456789:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghi"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": []any{
				map[string]any{"message": map[string]any{"chat": map[string]any{"id": 123456789, "type": "private"}}},
				map[string]any{"message": map[string]any{"chat": map[string]any{"id": -100987654321, "type": "supergroup"}}},
			},
		})
	}))
	defer server.Close()

	sender := newChannelSender(server.Client(), server.URL, server.URL+"/pushplus")
	if _, err := sender.DiscoverTelegramChat(context.Background(), map[string]any{"token": token}); err == nil || !strings.Contains(err.Error(), "多个可用会话") {
		t.Fatalf("expected ambiguous-chat error, got %v", err)
	}
}

func TestPushPlusSendIsPersonalFreeWechatOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/send" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["channel"] != "wechat" || body["template"] != "txt" {
			t.Fatalf("free channel was not pinned: %#v", body)
		}
		for _, forbidden := range []string{"sms", "voice", "pre", "option", "to", "topic", "callbackUrl"} {
			if _, exists := body[forbidden]; exists {
				t.Fatalf("forbidden PushPlus field %q was sent", forbidden)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "msg": "请求成功", "data": "trace-123"})
	}))
	defer server.Close()

	sender := newChannelSender(server.Client(), server.URL, server.URL+"/send")
	delivery, err := sender.Send(context.Background(), "pushplus", map[string]any{
		"token": "0123456789abcdef0123456789abcdef",
	}, Message{Title: "测试消息", Body: "免费链路已接通"})
	if err != nil {
		t.Fatal(err)
	}
	if !delivery.Async || delivery.ProviderID != "trace-123" || delivery.Note == "" {
		t.Fatalf("delivery = %#v", delivery)
	}
}

func TestTelegramNetworkErrorNeverLeaksToken(t *testing.T) {
	const token = "123456789:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghi"
	sender := newChannelSender(&http.Client{}, "http://127.0.0.1:1", "http://127.0.0.1:1")
	_, err := sender.Send(context.Background(), "telegram", map[string]any{
		"token": token, "chat": "123456789",
	}, Message{Title: "test", Body: "test"})
	if err == nil {
		t.Fatal("expected network error")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatal("Telegram token leaked through error text")
	}
}

func TestTelegramSendsToVerifiedGroupWithoutPaidBroadcast(t *testing.T) {
	const token = "123456789:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghi"
	const chatID = "-1001234567890"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bot" + token + "/getChat":
			if r.Method != http.MethodGet || r.URL.Query().Get("chat_id") != chatID {
				t.Fatalf("group verification = %s %s", r.Method, r.URL.String())
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"id": chatID, "type": "supergroup"}})
		case "/bot" + token + "/sendMessage":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["chat_id"] != chatID {
				t.Fatalf("chat_id = %#v", body["chat_id"])
			}
			if _, exists := body["allow_paid_broadcast"]; exists {
				t.Fatal("paid Telegram broadcasting must never be enabled")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": 43}})
		default:
			t.Fatalf("unexpected request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	sender := newChannelSender(server.Client(), server.URL, server.URL+"/pushplus")
	delivery, err := sender.Send(context.Background(), "telegram", map[string]any{
		"token": token, "chat": chatID,
	}, Message{Title: "test", Body: "group"})
	if err != nil {
		t.Fatal(err)
	}
	if delivery.ProviderID != "43" {
		t.Fatalf("delivery = %#v", delivery)
	}
}

func TestTelegramRejectsChannelDestination(t *testing.T) {
	const token = "123456789:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghi"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bot"+token+"/getChat" {
			t.Fatalf("channel destination reached send endpoint: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"type": "channel"}})
	}))
	defer server.Close()

	sender := newChannelSender(server.Client(), server.URL, server.URL+"/pushplus")
	_, err := sender.Send(context.Background(), "telegram", map[string]any{
		"token": token, "chat": "-1001234567890",
	}, Message{Title: "test", Body: "test"})
	if err == nil || !strings.Contains(err.Error(), "频道 Chat ID 不受支持") {
		t.Fatalf("expected channel rejection, got %v", err)
	}
}

func TestTelegramRejectsUsernameDestination(t *testing.T) {
	sender := newChannelSender(&http.Client{}, "http://127.0.0.1:1", "http://127.0.0.1:1")
	_, err := sender.Send(context.Background(), "telegram", map[string]any{
		"token": "123456789:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghi", "chat": "@broadcast_channel",
	}, Message{Title: "test", Body: "test"})
	if err == nil {
		t.Fatal("username destination was accepted")
	}
}

func TestBarkSendUsesOfficialPushJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/push" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["device_key"] != "bark-device-key" || body["title"] != "余额预警" || !strings.Contains(body["body"], "电表号") {
			t.Fatalf("Bark payload = %#v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "success"})
	}))
	defer server.Close()

	sender := newChannelSender(server.Client(), server.URL, server.URL+"/pushplus")
	delivery, err := sender.Send(context.Background(), "bark", map[string]any{
		"base_url": server.URL, "device_key": "bark-device-key",
	}, Message{Title: "余额预警", Body: "电表号：31240718"})
	if err != nil || delivery.Async {
		t.Fatalf("delivery=%#v err=%v", delivery, err)
	}
}

func TestGotifySendUsesApplicationHeaderAndOptionalPriority(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/gotify/message" || r.URL.RawQuery != "" {
			t.Fatalf("request = %s %s", r.Method, r.URL.String())
		}
		if got := r.Header.Get("X-Gotify-Key"); got != "application-token" {
			t.Fatalf("X-Gotify-Key = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["title"] != "用电摘要" || body["priority"] != float64(5) {
			t.Fatalf("Gotify payload = %#v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 42, "message": body["message"]})
	}))
	defer server.Close()

	sender := newChannelSender(server.Client(), server.URL, server.URL+"/pushplus")
	delivery, err := sender.Send(context.Background(), "gotify", map[string]any{
		"base_url": server.URL + "/gotify", "token": "application-token", "priority": "5",
	}, Message{Title: "用电摘要", Body: "本周期用电：1.25 度"})
	if err != nil || delivery.ProviderID != "42" {
		t.Fatalf("delivery=%#v err=%v", delivery, err)
	}
}

func TestGenericWebhookPayloadUsesStableContract(t *testing.T) {
	message := Message{
		Event:  "low_balance_alert",
		Title:  "低额度预警",
		Body:   "请及时充值",
		SentAt: time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC),
		Meter:  notification.Meter{Number: "31240718", Building: "12栋", Floor: "4楼", Room: "402"},
		Data:   notification.Data{BalanceYuan: "8.50", ThresholdYuan: "10.00"},
	}
	raw, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"event":"low_balance_alert"`, `"message":"请及时充值"`, `"number":"31240718"`, `"balance_yuan":"8.50"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("Webhook JSON missing %s: %s", want, raw)
		}
	}
}

// 发送前 DNS 解析到内网必须失败，且零字节出站。
func TestGenericWebhookRejectsPrivateResolution(t *testing.T) {
	for _, tc := range []struct {
		name string
		ip   string
	}{
		{"loopback", "127.0.0.1"},
		{"private", "10.1.2.3"},
		{"link local", "169.254.169.254"},
		{"unspecified", "0.0.0.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sender := newChannelSender(&http.Client{}, "http://127.0.0.1:1", "http://127.0.0.1:1")
			sender.resolveHost = func(context.Context, string) ([]net.IPAddr, error) {
				return []net.IPAddr{{IP: net.ParseIP(tc.ip)}}, nil
			}
			dialed := false
			sender.dialContext = func(context.Context, string, string) (net.Conn, error) {
				dialed = true
				return nil, errors.New("must not dial")
			}
			_, err := sender.Send(context.Background(), "webhook", map[string]any{
				"webhook": "https://notify.example.com/hooks/power",
			}, Message{Event: "test", Title: "测试", Body: "测试"})
			if err == nil {
				t.Fatal("expected the send to be rejected")
			}
			if !strings.Contains(err.Error(), "内网") {
				t.Fatalf("error = %v, want the private-network rejection", err)
			}
			if dialed {
				t.Fatal("resolved private address must never be dialed")
			}
		})
	}
}

func TestGenericWebhookSendsBearerAndStableJSON(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/hooks/power" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer interface-token" {
			t.Fatalf("Authorization = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		meter, _ := body["meter"].(map[string]any)
		if body["event"] != "low_balance_alert" || body["message"] != "请及时充值" || meter["number"] != "31240718" {
			t.Fatalf("Webhook payload = %#v", body)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	sender := newChannelSender(server.Client(), server.URL, server.URL+"/pushplus")
	sender.resolveHost = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}
	actual, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	sender.dialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, actual.Host)
	}
	message := Message{
		Event: "low_balance_alert", Title: "低额度预警", Body: "请及时充值",
		Meter: notification.Meter{Number: "31240718", Building: "12栋", Floor: "4楼", Room: "402"},
	}
	if _, err := sender.Send(context.Background(), "webhook", map[string]any{
		"webhook": server.URL + "/hooks/power", "token": "interface-token",
	}, message); err != nil {
		t.Fatal(err)
	}
}
