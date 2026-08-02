package push

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/notification"
	"github.com/gorilla/websocket"
)

const (
	telegramAPIBase = "https://api.telegram.org"
	pushPlusSendURL = "https://www.pushplus.plus/send"
	whatsAppAPIURL  = "https://api.callmebot.com/whatsapp.php"
	serverChanURL   = "https://sctapi.ftqq.com"
	wechatAPIBase   = "https://api.weixin.qq.com"
	qqTokenURL      = "https://bots.qq.com/app/getAppAccessToken"
	qqAPIBase       = "https://api.sgroup.qq.com"
	wecomWSURL      = "wss://openws.work.weixin.qq.com"
	providerBodyMax = 1 << 20
)

// pushUserAgent 统一出站 UA，避免多处硬编码留下旧品牌指纹。
const pushUserAgent = "EDU-Power-Push/1"

var (
	telegramBotTokenPattern = regexp.MustCompile(`^[1-9][0-9]{5,15}:[A-Za-z0-9_-]{30,50}$`)
	telegramChatIDPattern   = regexp.MustCompile(`^-?[1-9][0-9]{4,19}$`)
	pushPlusTokenPattern    = regexp.MustCompile(`^[A-Fa-f0-9]{32}$`)
	napCatTargetPattern     = regexp.MustCompile(`^[1-9][0-9]{4,19}$`)
	serverChanTurboPattern  = regexp.MustCompile(`^SCT[A-Za-z0-9_-]{8,256}$`)
	serverChanThreePattern  = regexp.MustCompile(`^sctp([1-9][0-9]*)t[A-Za-z0-9_-]{8,256}$`)
)

// Message 为规范通知契约别名。
type Message = notification.Message

// ChannelDelivery 为上游受理结果。
// PushPlus 的 200 仅异步受理，Async 为 true 时禁止宣称已送达。
type ChannelDelivery struct {
	ProviderID string
	Async      bool
	Note       string
}

// Deliverer 供 API 与 worker 共用发送实现。
type Deliverer interface {
	Send(context.Context, string, map[string]any, Message) (ChannelDelivery, error)
}

// TelegramChatDiscoverer 从 Bot 更新中识别唯一私聊或群聊。
type TelegramChatDiscoverer interface {
	DiscoverTelegramChat(context.Context, map[string]any) (string, error)
}

type ChannelSender struct {
	client          *http.Client
	telegramBaseURL string
	pushPlusURL     string
	whatsAppURL     string
	serverChanURL   string
	serverChan3URL  string
	wechatBaseURL   string
	qqTokenURL      string
	qqAPIBaseURL    string
	wecomWSURL      string
	resolveHost     func(context.Context, string) ([]net.IPAddr, error)
	dialContext     func(context.Context, string, string) (net.Conn, error)
	tokenMu         sync.Mutex
	tokens          map[string]cachedAccessToken
}

type cachedAccessToken struct {
	value     string
	expiresAt time.Time
}

func NewChannelSender(client *http.Client) *ChannelSender {
	if client == nil {
		client = &http.Client{
			Timeout: 20 * time.Second,
			// 禁止跟随跳转，避免凭证带到其它主机。
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	return newChannelSender(client, telegramAPIBase, pushPlusSendURL)
}

func newChannelSender(client *http.Client, telegramBaseURL, pushPlusURL string) *ChannelSender {
	return &ChannelSender{
		client:          client,
		telegramBaseURL: strings.TrimRight(telegramBaseURL, "/"),
		pushPlusURL:     pushPlusURL,
		whatsAppURL:     whatsAppAPIURL,
		serverChanURL:   serverChanURL,
		wechatBaseURL:   wechatAPIBase,
		qqTokenURL:      qqTokenURL,
		qqAPIBaseURL:    qqAPIBase,
		wecomWSURL:      wecomWSURL,
		resolveHost:     net.DefaultResolver.LookupIPAddr,
		tokens:          make(map[string]cachedAccessToken),
	}
}

func (s *ChannelSender) Send(ctx context.Context, channel string, config map[string]any, message Message) (ChannelDelivery, error) {
	if s == nil || s.client == nil {
		return ChannelDelivery{}, errors.New("推送客户端未初始化")
	}
	if message.SentAt.IsZero() {
		message.SentAt = time.Now().UTC()
	}
	if strings.TrimSpace(message.Event) == "" {
		message.Event = "notification"
	}
	switch channel {
	case "webhook":
		return s.sendGenericWebhook(ctx, config, message)
	case "bark":
		return s.sendBark(ctx, config, message)
	case "gotify":
		return s.sendGotify(ctx, config, message)
	case "dingtalk":
		return s.sendDingTalk(ctx, config, message)
	case "feishu":
		return s.sendLarkStyle(ctx, "飞书", config, message)
	case "lark":
		return s.sendLarkStyle(ctx, "Lark", config, message)
	case "wecom":
		return s.sendWeCom(ctx, config, message)
	case "wecom_webhook":
		return s.sendWeComWebhook(ctx, config, message)
	case "discord":
		return s.sendDiscord(ctx, config, message)
	case "mp":
		return s.sendWeChatMP(ctx, config, message)
	case "qq":
		return s.sendQQ(ctx, config, message)
	case "napcat":
		return s.sendNapCat(ctx, config, message)
	case "telegram":
		return s.sendTelegram(ctx, config, message)
	case "pushplus":
		return s.sendPushPlus(ctx, config, message)
	case "whatsapp":
		return s.sendWhatsApp(ctx, config, message)
	case "serverchan_turbo", "serverchan3":
		return s.sendServerChan(ctx, channel, config, message)
	default:
		return ChannelDelivery{}, fmt.Errorf("渠道 %s 尚未实现", channel)
	}
}

func (s *ChannelSender) sendGenericWebhook(ctx context.Context, config map[string]any, message Message) (ChannelDelivery, error) {
	endpoint, err := requiredString(config, "webhook")
	if err != nil {
		return ChannelDelivery{}, fmt.Errorf("Webhook %w", err)
	}
	parsedEndpoint, addresses, err := s.publicWebhookDestination(ctx, endpoint)
	if err != nil {
		return ChannelDelivery{}, err
	}
	headers := map[string]string{}
	if token, _ := config["token"].(string); strings.TrimSpace(token) != "" {
		headers["Authorization"] = "Bearer " + strings.TrimSpace(token)
	}
	status, raw, err := s.postPublicWebhookJSON(ctx, parsedEndpoint, addresses, message, headers)
	if err != nil {
		return ChannelDelivery{}, err
	}
	if status < 200 || status >= 300 {
		return ChannelDelivery{}, fmt.Errorf("Webhook 请求失败（HTTP %d）：%s", status, cleanProviderMessage(string(raw)))
	}
	return ChannelDelivery{}, nil
}

func (s *ChannelSender) sendBark(ctx context.Context, config map[string]any, message Message) (ChannelDelivery, error) {
	baseURL, err := requiredString(config, "base_url")
	if err != nil {
		return ChannelDelivery{}, fmt.Errorf("Bark %w", err)
	}
	deviceKey, err := requiredString(config, "device_key")
	if err != nil {
		return ChannelDelivery{}, fmt.Errorf("Bark %w", err)
	}
	endpoint, err := serviceActionURL(baseURL, "push")
	if err != nil {
		return ChannelDelivery{}, fmt.Errorf("Bark 服务地址无效")
	}
	status, raw, err := s.postJSON(ctx, "Bark", endpoint, map[string]string{
		"device_key": deviceKey,
		"title":      truncateRunes(strings.TrimSpace(message.Title), 200),
		"body":       truncateRunes(strings.TrimSpace(message.Body), 10_000),
	})
	if err != nil {
		return ChannelDelivery{}, err
	}
	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return ChannelDelivery{}, fmt.Errorf("Bark 响应格式无效（HTTP %d）", status)
	}
	if status < 200 || status >= 300 || result.Code != 200 {
		return ChannelDelivery{}, fmt.Errorf("Bark 请求失败（HTTP %d / code %d）：%s", status, result.Code, cleanProviderMessage(result.Message))
	}
	return ChannelDelivery{}, nil
}

func (s *ChannelSender) sendGotify(ctx context.Context, config map[string]any, message Message) (ChannelDelivery, error) {
	baseURL, err := requiredString(config, "base_url")
	if err != nil {
		return ChannelDelivery{}, fmt.Errorf("Gotify %w", err)
	}
	token, err := requiredString(config, "token")
	if err != nil {
		return ChannelDelivery{}, fmt.Errorf("Gotify %w", err)
	}
	endpoint, err := serviceActionURL(baseURL, "message")
	if err != nil {
		return ChannelDelivery{}, errors.New("Gotify 服务地址无效")
	}
	payload := map[string]any{
		"title":   truncateRunes(strings.TrimSpace(message.Title), 200),
		"message": truncateRunes(strings.TrimSpace(message.Body), 20_000),
	}
	if rawPriority := strings.TrimSpace(fmt.Sprint(config["priority"])); rawPriority != "" && rawPriority != "<nil>" {
		priority, parseErr := strconv.Atoi(rawPriority)
		if parseErr != nil {
			return ChannelDelivery{}, errors.New("Gotify 消息优先级必须是整数")
		}
		payload["priority"] = priority
	}
	status, raw, err := s.postJSONHeaders(ctx, "Gotify", endpoint, payload, map[string]string{"X-Gotify-Key": token})
	if err != nil {
		return ChannelDelivery{}, err
	}
	var result struct {
		ID      int64  `json:"id"`
		Error   string `json:"error"`
		Message string `json:"errorDescription"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return ChannelDelivery{}, fmt.Errorf("Gotify 响应格式无效（HTTP %d）", status)
	}
	if status < 200 || status >= 300 || result.ID <= 0 {
		providerMessage := result.Message
		if strings.TrimSpace(providerMessage) == "" {
			providerMessage = result.Error
		}
		return ChannelDelivery{}, fmt.Errorf("Gotify 请求失败（HTTP %d）：%s", status, cleanProviderMessage(providerMessage))
	}
	return ChannelDelivery{ProviderID: strconv.FormatInt(result.ID, 10)}, nil
}

func serviceActionURL(raw, action string) (string, error) {
	base, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || base.Hostname() == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return "", errors.New("invalid service URL")
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/" + action
	return base.String(), nil
}

// 通用 Webhook 禁止访问本地、内网与云元数据地址。
// 发送前再次解析 DNS，不止依赖保存时校验。
func (s *ChannelSender) publicWebhookDestination(ctx context.Context, raw string) (*url.URL, []net.IPAddr, error) {
	endpoint, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.Fragment != "" {
		return nil, nil, errors.New("Webhook 地址必须是无内嵌凭证与片段的 HTTPS URL")
	}
	resolver := s.resolveHost
	if resolver == nil {
		resolver = net.DefaultResolver.LookupIPAddr
	}
	addresses, err := resolver(ctx, endpoint.Hostname())
	if err != nil || len(addresses) == 0 {
		return nil, nil, errors.New("Webhook 域名无法解析")
	}
	for _, address := range addresses {
		ip := address.IP
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return nil, nil, errors.New("Webhook 地址不能指向本机、内网或链路本地网络")
		}
	}
	return endpoint, addresses, nil
}

func (s *ChannelSender) postPublicWebhookJSON(
	ctx context.Context, endpoint *url.URL, addresses []net.IPAddr, payload any, headers map[string]string,
) (int, []byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, errors.New("Webhook 请求编码失败")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return 0, nil, errors.New("Webhook 请求创建失败")
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if base, ok := s.client.Transport.(*http.Transport); ok && base != nil {
		transport = base.Clone()
	}
	transport.Proxy = nil
	host, port := endpoint.Hostname(), endpoint.Port()
	if port == "" {
		port = "443"
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	dialContext := dialer.DialContext
	if s.dialContext != nil {
		dialContext = s.dialContext
	}
	transport.DialContext = func(dialCtx context.Context, network, address string) (net.Conn, error) {
		requestedHost, requestedPort, splitErr := net.SplitHostPort(address)
		if splitErr != nil || !strings.EqualFold(requestedHost, host) || requestedPort != port {
			return nil, errors.New("Webhook 连接目标与已校验地址不一致")
		}
		var lastErr error
		for _, resolved := range addresses {
			conn, dialErr := dialContext(dialCtx, network, net.JoinHostPort(resolved.IP.String(), port))
			if dialErr == nil {
				return conn, nil
			}
			lastErr = dialErr
		}
		return nil, lastErr
	}
	client := &http.Client{
		Timeout:       s.client.Timeout,
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, safeNetworkError("Webhook", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, providerBodyMax))
	if err != nil {
		return resp.StatusCode, nil, errors.New("Webhook 响应读取失败")
	}
	return resp.StatusCode, raw, nil
}

func (s *ChannelSender) sendWhatsApp(ctx context.Context, config map[string]any, message Message) (ChannelDelivery, error) {
	webhook, err := requiredString(config, "webhook")
	if err != nil {
		return ChannelDelivery{}, fmt.Errorf("WhatsApp (CallMeBot) %w", err)
	}
	endpoint, err := callMeBotMessageURL(webhook, s.whatsAppURL, truncateRunes(joinMessage(message), 10_000))
	if err != nil {
		return ChannelDelivery{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ChannelDelivery{}, errors.New("WhatsApp (CallMeBot) 请求创建失败")
	}
	req.Header.Set("User-Agent", pushUserAgent)
	resp, err := s.client.Do(req)
	if err != nil {
		return ChannelDelivery{}, safeNetworkError("WhatsApp (CallMeBot)", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, providerBodyMax))
	if err != nil {
		return ChannelDelivery{}, errors.New("WhatsApp (CallMeBot) 响应读取失败")
	}
	responseText := strings.ToLower(strings.Join(strings.Fields(string(raw)), " "))
	failedBody := strings.Contains(responseText, "error") || strings.Contains(responseText, "invalid") ||
		strings.Contains(responseText, "not authorized") || strings.Contains(responseText, "not activated")
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || failedBody {
		providerMessage := cleanProviderMessage(string(raw))
		if providerMessage == "" {
			providerMessage = http.StatusText(resp.StatusCode)
		}
		return ChannelDelivery{}, fmt.Errorf("WhatsApp (CallMeBot) 请求失败（HTTP %d）：%s", resp.StatusCode, providerMessage)
	}
	return ChannelDelivery{Async: true, Note: "CallMeBot 已受理，最终结果由 WhatsApp 异步投递"}, nil
}

func callMeBotMessageURL(raw, allowedEndpoint, message string) (string, error) {
	endpoint, err := url.Parse(strings.TrimSpace(raw))
	allowed, allowedErr := url.Parse(strings.TrimSpace(allowedEndpoint))
	if err != nil || allowedErr != nil || endpoint.Host == "" || allowed.Host == "" || endpoint.User != nil || endpoint.Fragment != "" ||
		!strings.EqualFold(endpoint.Scheme, allowed.Scheme) || !strings.EqualFold(endpoint.Host, allowed.Host) || endpoint.Path != allowed.Path {
		return "", errors.New("WhatsApp (CallMeBot) API URL 无效")
	}
	query := endpoint.Query()
	if strings.TrimSpace(query.Get("phone")) == "" || strings.TrimSpace(query.Get("apikey")) == "" {
		return "", errors.New("WhatsApp (CallMeBot) API URL 必须包含 phone 与 apikey")
	}
	query.Set("text", message)
	endpoint.RawQuery = query.Encode()
	return endpoint.String(), nil
}

func (s *ChannelSender) sendServerChan(ctx context.Context, channel string, config map[string]any, message Message) (ChannelDelivery, error) {
	sendKey, err := requiredString(config, "sendkey")
	if err != nil {
		return ChannelDelivery{}, fmt.Errorf("%s %w", serverChanName(channel), err)
	}
	endpoint, err := s.serverChanEndpoint(channel, sendKey)
	if err != nil {
		return ChannelDelivery{}, err
	}
	title := truncateRunes(strings.Join(strings.Fields(message.Title), " "), 200)
	if title == "" {
		title = "EDU Power Push"
	}
	payload := map[string]string{
		"title": title,
		"desp":  truncateRunes(strings.TrimSpace(message.Body), 20_000),
	}
	status, raw, err := s.postJSON(ctx, serverChanName(channel), endpoint, payload)
	if err != nil {
		return ChannelDelivery{}, err
	}
	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return ChannelDelivery{}, fmt.Errorf("%s 响应格式无效（HTTP %d）", serverChanName(channel), status)
	}
	if status < 200 || status >= 300 || result.Code != 0 {
		providerMessage := cleanProviderMessage(result.Message)
		if providerMessage == "" {
			providerMessage = http.StatusText(status)
		}
		return ChannelDelivery{}, fmt.Errorf("%s 请求失败（HTTP %d / code %d）：%s", serverChanName(channel), status, result.Code, providerMessage)
	}
	return ChannelDelivery{Async: true, Note: serverChanName(channel) + " 已受理，最终结果由其平台异步投递"}, nil
}

func (s *ChannelSender) serverChanEndpoint(channel, sendKey string) (string, error) {
	switch channel {
	case "serverchan_turbo":
		if !serverChanTurboPattern.MatchString(sendKey) {
			return "", errors.New("Server酱Turbo SendKey 格式无效")
		}
		return strings.TrimRight(s.serverChanURL, "/") + "/" + url.PathEscape(sendKey) + ".send", nil
	case "serverchan3":
		matches := serverChanThreePattern.FindStringSubmatch(sendKey)
		if len(matches) != 2 {
			return "", errors.New("Server酱³ SendKey 格式无效")
		}
		if strings.TrimSpace(s.serverChan3URL) != "" {
			return strings.TrimRight(s.serverChan3URL, "/") + "/send/" + url.PathEscape(sendKey) + ".send", nil
		}
		return "https://" + matches[1] + ".push.ft07.com/send/" + url.PathEscape(sendKey) + ".send", nil
	default:
		return "", errors.New("Server酱渠道无效")
	}
}

func serverChanName(channel string) string {
	if channel == "serverchan3" {
		return "Server酱³"
	}
	return "Server酱Turbo"
}

func (s *ChannelSender) sendWeComWebhook(ctx context.Context, config map[string]any, message Message) (ChannelDelivery, error) {
	webhook, err := requiredString(config, "webhook")
	if err != nil {
		return ChannelDelivery{}, fmt.Errorf("企业微信消息推送 %w", err)
	}
	payload := struct {
		MessageType string `json:"msgtype"`
		Text        struct {
			Content string `json:"content"`
		} `json:"text"`
	}{MessageType: "text"}
	payload.Text.Content = truncateRunes(joinMessage(message), 2048)
	status, raw, err := s.postJSON(ctx, "企业微信消息推送", webhook, payload)
	if err != nil {
		return ChannelDelivery{}, err
	}
	var result struct {
		Code    int    `json:"errcode"`
		Message string `json:"errmsg"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return ChannelDelivery{}, fmt.Errorf("企业微信消息推送响应格式无效（HTTP %d）", status)
	}
	if status < 200 || status >= 300 || result.Code != 0 {
		return ChannelDelivery{}, fmt.Errorf("企业微信消息推送请求失败（HTTP %d / code %d）：%s", status, result.Code, cleanProviderMessage(result.Message))
	}
	return ChannelDelivery{}, nil
}

func (s *ChannelSender) sendDiscord(ctx context.Context, config map[string]any, message Message) (ChannelDelivery, error) {
	webhook, err := requiredString(config, "webhook")
	if err != nil {
		return ChannelDelivery{}, fmt.Errorf("Discord %w", err)
	}
	endpoint, err := url.Parse(webhook)
	if err != nil || endpoint.Host == "" {
		return ChannelDelivery{}, errors.New("Discord Webhook 地址无效")
	}
	query := endpoint.Query()
	query.Set("wait", "true")
	endpoint.RawQuery = query.Encode()
	payload := struct {
		Content         string `json:"content"`
		AllowedMentions struct {
			Parse []string `json:"parse"`
		} `json:"allowed_mentions"`
	}{Content: truncateRunes(joinMessage(message), 2000)}
	payload.AllowedMentions.Parse = []string{}
	status, raw, err := s.postJSON(ctx, "Discord", endpoint.String(), payload)
	if err != nil {
		return ChannelDelivery{}, err
	}
	var result struct {
		ID      string `json:"id"`
		Message string `json:"message"`
		Code    int64  `json:"code"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return ChannelDelivery{}, fmt.Errorf("Discord 响应格式无效（HTTP %d）", status)
	}
	if status < 200 || status >= 300 || result.ID == "" {
		return ChannelDelivery{}, fmt.Errorf("Discord 请求失败（HTTP %d / code %d）：%s", status, result.Code, cleanProviderMessage(result.Message))
	}
	return ChannelDelivery{ProviderID: result.ID}, nil
}

func (s *ChannelSender) sendWeCom(ctx context.Context, config map[string]any, message Message) (ChannelDelivery, error) {
	botID, err := requiredString(config, "botid")
	if err != nil {
		return ChannelDelivery{}, fmt.Errorf("企业微信 %w", err)
	}
	secret, err := requiredString(config, "secret")
	if err != nil {
		return ChannelDelivery{}, fmt.Errorf("企业微信 %w", err)
	}
	chatID, err := requiredString(config, "chatid")
	if err != nil {
		return ChannelDelivery{}, fmt.Errorf("企业微信 %w", err)
	}
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, response, err := dialer.DialContext(ctx, s.wecomWSURL, nil)
	if err != nil {
		if response != nil {
			_ = response.Body.Close()
			return ChannelDelivery{}, fmt.Errorf("企业微信连接失败（HTTP %d）", response.StatusCode)
		}
		return ChannelDelivery{}, safeNetworkError("企业微信", err)
	}
	defer conn.Close()
	deadline := time.Now().Add(15 * time.Second)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	_ = conn.SetReadDeadline(deadline)
	_ = conn.SetWriteDeadline(deadline)

	authID := requestID("aibot_subscribe")
	if err := conn.WriteJSON(map[string]any{
		"cmd": "aibot_subscribe", "headers": map[string]string{"req_id": authID},
		"body": map[string]string{"bot_id": botID, "secret": secret},
	}); err != nil {
		return ChannelDelivery{}, errors.New("企业微信认证请求发送失败")
	}
	if err := readWeComAck(conn, authID, "认证"); err != nil {
		return ChannelDelivery{}, err
	}

	sendID := requestID("aibot_send_msg")
	if err := conn.WriteJSON(map[string]any{
		"cmd": "aibot_send_msg", "headers": map[string]string{"req_id": sendID},
		"body": map[string]any{
			"chatid": chatID, "msgtype": "markdown",
			"markdown": map[string]string{"content": truncateRunes(joinMessage(message), 4000)},
		},
	}); err != nil {
		return ChannelDelivery{}, errors.New("企业微信消息发送失败")
	}
	if err := readWeComAck(conn, sendID, "发送"); err != nil {
		return ChannelDelivery{}, err
	}
	return ChannelDelivery{ProviderID: sendID}, nil
}

func readWeComAck(conn *websocket.Conn, requestID, action string) error {
	for range 8 {
		var frame struct {
			Headers struct {
				RequestID string `json:"req_id"`
			} `json:"headers"`
			ErrorCode int    `json:"errcode"`
			Error     string `json:"errmsg"`
		}
		if err := conn.ReadJSON(&frame); err != nil {
			return fmt.Errorf("企业微信%s回执读取失败", action)
		}
		if frame.Headers.RequestID != requestID {
			continue
		}
		if frame.ErrorCode != 0 {
			return fmt.Errorf("企业微信%s失败（code %d）：%s", action, frame.ErrorCode, cleanProviderMessage(frame.Error))
		}
		return nil
	}
	return fmt.Errorf("企业微信%s回执不匹配", action)
}

func requestID(prefix string) string {
	return prefix + "_" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

func (s *ChannelSender) sendWeChatMP(ctx context.Context, config map[string]any, message Message) (ChannelDelivery, error) {
	appID, err := requiredString(config, "appid")
	if err != nil {
		return ChannelDelivery{}, fmt.Errorf("微信测试号 %w", err)
	}
	secret, err := requiredString(config, "secret")
	if err != nil {
		return ChannelDelivery{}, fmt.Errorf("微信测试号 %w", err)
	}
	openID, err := requiredString(config, "openid")
	if err != nil {
		return ChannelDelivery{}, fmt.Errorf("微信测试号 %w", err)
	}
	templateID, err := requiredString(config, "tpl")
	if err != nil {
		return ChannelDelivery{}, fmt.Errorf("微信测试号 %w", err)
	}
	token, err := s.accessToken(ctx, "wechat", appID, secret)
	if err != nil {
		return ChannelDelivery{}, err
	}
	endpoint := strings.TrimRight(s.wechatBaseURL, "/") + "/cgi-bin/message/template/send?access_token=" + url.QueryEscape(token)
	payload := map[string]any{
		"touser": openID, "template_id": templateID,
		"data": map[string]any{
			"title":   map[string]string{"value": truncateRunes(message.Title, 100)},
			"content": map[string]string{"value": truncateRunes(message.Body, 1000)},
		},
	}
	status, raw, err := s.postJSON(ctx, "微信测试号", endpoint, payload)
	if err != nil {
		return ChannelDelivery{}, err
	}
	var result struct {
		Code      int64  `json:"errcode"`
		Message   string `json:"errmsg"`
		MessageID int64  `json:"msgid"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return ChannelDelivery{}, fmt.Errorf("微信测试号响应格式无效（HTTP %d）", status)
	}
	if status < 200 || status >= 300 || result.Code != 0 {
		return ChannelDelivery{}, fmt.Errorf("微信测试号请求失败（HTTP %d / code %d）：%s", status, result.Code, cleanProviderMessage(result.Message))
	}
	return ChannelDelivery{ProviderID: strconv.FormatInt(result.MessageID, 10)}, nil
}

func (s *ChannelSender) sendQQ(ctx context.Context, config map[string]any, message Message) (ChannelDelivery, error) {
	appID, err := requiredString(config, "appid")
	if err != nil {
		return ChannelDelivery{}, fmt.Errorf("QQ 官方机器人 %w", err)
	}
	secret, err := requiredString(config, "secret")
	if err != nil {
		return ChannelDelivery{}, fmt.Errorf("QQ 官方机器人 %w", err)
	}
	userOpenID, err := requiredString(config, "user_openid")
	if err != nil {
		return ChannelDelivery{}, fmt.Errorf("QQ 官方机器人 %w", err)
	}
	token, err := s.accessToken(ctx, "qq", appID, secret)
	if err != nil {
		return ChannelDelivery{}, err
	}
	path := "/v2/users/" + url.PathEscape(userOpenID) + "/messages"
	headers := map[string]string{"Authorization": "QQBot " + token, "X-Union-Appid": appID}
	status, raw, err := s.postJSONHeaders(ctx, "QQ 官方机器人", strings.TrimRight(s.qqAPIBaseURL, "/")+path,
		map[string]any{"msg_type": 0, "content": truncateRunes(joinMessage(message), 2000)}, headers)
	if err != nil {
		return ChannelDelivery{}, err
	}
	var result struct {
		ID      string `json:"id"`
		Code    int64  `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return ChannelDelivery{}, fmt.Errorf("QQ 官方机器人响应格式无效（HTTP %d）", status)
	}
	if status < 200 || status >= 300 || result.Code != 0 || result.ID == "" {
		return ChannelDelivery{}, fmt.Errorf("QQ 官方机器人请求失败（HTTP %d / code %d）：%s", status, result.Code, cleanProviderMessage(result.Message))
	}
	return ChannelDelivery{ProviderID: result.ID}, nil
}

func (s *ChannelSender) sendNapCat(ctx context.Context, config map[string]any, message Message) (ChannelDelivery, error) {
	baseURL, err := requiredString(config, "base_url")
	if err != nil {
		return ChannelDelivery{}, fmt.Errorf("NapCat %w", err)
	}
	target, err := requiredString(config, "target")
	if err != nil {
		return ChannelDelivery{}, fmt.Errorf("NapCat %w", err)
	}
	token, _ := config["token"].(string)
	token = strings.TrimSpace(token)

	messageType, targetKey, targetID, err := parseNapCatTarget(target)
	if err != nil {
		return ChannelDelivery{}, err
	}
	endpoint, err := napCatActionURL(baseURL, "send_msg")
	if err != nil {
		return ChannelDelivery{}, err
	}
	payload := map[string]any{
		"message_type": messageType,
		"message":      truncateRunes(joinMessage(message), 4000),
		"auto_escape":  true,
		targetKey:      targetID,
	}
	headers := map[string]string{}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	status, raw, err := s.postJSONHeaders(ctx, "NapCat", endpoint, payload, headers)
	if err != nil {
		return ChannelDelivery{}, err
	}
	// 鉴权明确失败时回退 access_token，避免一次发送双消息。
	if token != "" && (status == http.StatusUnauthorized || status == http.StatusForbidden) {
		fallback, parseErr := url.Parse(endpoint)
		if parseErr != nil {
			return ChannelDelivery{}, errors.New("NapCat 服务地址无效")
		}
		query := fallback.Query()
		query.Set("access_token", token)
		fallback.RawQuery = query.Encode()
		status, raw, err = s.postJSONHeaders(ctx, "NapCat", fallback.String(), payload, nil)
		if err != nil {
			return ChannelDelivery{}, err
		}
	}

	var result struct {
		Status  string `json:"status"`
		RetCode int64  `json:"retcode"`
		Message string `json:"message"`
		Wording string `json:"wording"`
		Data    struct {
			MessageID any `json:"message_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return ChannelDelivery{}, fmt.Errorf("NapCat 响应格式无效（HTTP %d）", status)
	}
	if status < 200 || status >= 300 || result.RetCode != 0 || strings.EqualFold(result.Status, "failed") {
		providerMessage := result.Wording
		if strings.TrimSpace(providerMessage) == "" {
			providerMessage = result.Message
		}
		return ChannelDelivery{}, fmt.Errorf("NapCat 请求失败（HTTP %d / retcode %d）：%s", status, result.RetCode, cleanProviderMessage(providerMessage))
	}
	return ChannelDelivery{ProviderID: oneBotMessageID(result.Data.MessageID)}, nil
}

func parseNapCatTarget(raw string) (messageType, targetKey, targetID string, err error) {
	target := strings.TrimSpace(raw)
	messageType = "group"
	targetKey = "group_id"
	if strings.HasPrefix(target, "group:") {
		target = strings.TrimPrefix(target, "group:")
	} else if strings.HasPrefix(target, "user:") {
		messageType = "private"
		targetKey = "user_id"
		target = strings.TrimPrefix(target, "user:")
	}
	if !napCatTargetPattern.MatchString(target) {
		return "", "", "", errors.New("NapCat 目标 ID 必须是有效群号或 QQ 号")
	}
	return messageType, targetKey, target, nil
}

func napCatActionURL(raw, action string) (string, error) {
	base, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || base.Hostname() == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return "", errors.New("NapCat 服务地址无效")
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/" + action
	return base.String(), nil
}

func oneBotMessageID(value any) string {
	switch id := value.(type) {
	case string:
		return id
	case float64:
		return strconv.FormatInt(int64(id), 10)
	case json.Number:
		return id.String()
	default:
		return ""
	}
}

func (s *ChannelSender) accessToken(ctx context.Context, provider, appID, secret string) (string, error) {
	digest := sha256.Sum256([]byte(secret))
	key := provider + ":" + appID + ":" + base64.RawURLEncoding.EncodeToString(digest[:])
	s.tokenMu.Lock()
	entry, ok := s.tokens[key]
	s.tokenMu.Unlock()
	if ok && time.Until(entry.expiresAt) > 60*time.Second {
		return entry.value, nil
	}

	var token string
	var expires int64
	if provider == "wechat" {
		endpoint := strings.TrimRight(s.wechatBaseURL, "/") + "/cgi-bin/token?grant_type=client_credential&appid=" + url.QueryEscape(appID) + "&secret=" + url.QueryEscape(secret)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return "", errors.New("微信测试号凭证请求创建失败")
		}
		resp, err := s.client.Do(req)
		if err != nil {
			return "", safeNetworkError("微信测试号", err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(resp.Body, providerBodyMax))
		if err != nil {
			return "", errors.New("微信测试号凭证响应读取失败")
		}
		var result struct {
			Token   string `json:"access_token"`
			Expires int64  `json:"expires_in"`
			Code    int64  `json:"errcode"`
			Message string `json:"errmsg"`
		}
		if json.Unmarshal(raw, &result) != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 || result.Code != 0 || result.Token == "" {
			return "", fmt.Errorf("微信测试号凭证获取失败（HTTP %d / code %d）：%s", resp.StatusCode, result.Code, cleanProviderMessage(result.Message))
		}
		token, expires = result.Token, result.Expires
	} else {
		status, raw, err := s.postJSON(ctx, "QQ 官方机器人", s.qqTokenURL, map[string]string{"appId": appID, "clientSecret": secret})
		if err != nil {
			return "", err
		}
		var result struct {
			Token   string `json:"access_token"`
			Expires any    `json:"expires_in"`
			Code    int64  `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &result) != nil || status < 200 || status >= 300 || result.Code != 0 || result.Token == "" {
			return "", fmt.Errorf("QQ 官方机器人凭证获取失败（HTTP %d / code %d）：%s", status, result.Code, cleanProviderMessage(result.Message))
		}
		token = result.Token
		switch value := result.Expires.(type) {
		case string:
			expires, _ = strconv.ParseInt(value, 10, 64)
		case float64:
			expires = int64(value)
		}
	}
	if expires < 120 {
		expires = 120
	}
	s.tokenMu.Lock()
	s.tokens[key] = cachedAccessToken{value: token, expiresAt: time.Now().Add(time.Duration(expires) * time.Second)}
	s.tokenMu.Unlock()
	return token, nil
}

func (s *ChannelSender) sendDingTalk(ctx context.Context, config map[string]any, message Message) (ChannelDelivery, error) {
	webhook, err := requiredString(config, "webhook")
	if err != nil {
		return ChannelDelivery{}, fmt.Errorf("钉钉 %w", err)
	}
	secret, err := requiredString(config, "secret")
	if err != nil {
		return ChannelDelivery{}, fmt.Errorf("钉钉 %w", err)
	}
	endpoint, err := url.Parse(webhook)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "https" && endpoint.Scheme != "http") {
		return ChannelDelivery{}, errors.New("钉钉 Webhook 地址无效")
	}
	timestamp := time.Now().UnixMilli()
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(strconv.FormatInt(timestamp, 10) + "\n" + secret))
	query := endpoint.Query()
	query.Set("timestamp", strconv.FormatInt(timestamp, 10))
	query.Set("sign", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	endpoint.RawQuery = query.Encode()
	payload := struct {
		MessageType string `json:"msgtype"`
		Text        struct {
			Content string `json:"content"`
		} `json:"text"`
	}{MessageType: "text"}
	payload.Text.Content = truncateRunes(joinMessage(message), 20_000)
	status, raw, err := s.postJSON(ctx, "钉钉", endpoint.String(), payload)
	if err != nil {
		return ChannelDelivery{}, err
	}
	var result struct {
		Code    int    `json:"errcode"`
		Message string `json:"errmsg"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return ChannelDelivery{}, fmt.Errorf("钉钉响应格式无效（HTTP %d）", status)
	}
	if status < 200 || status >= 300 || result.Code != 0 {
		msg := cleanProviderMessage(result.Message)
		if msg == "" {
			msg = http.StatusText(status)
		}
		return ChannelDelivery{}, fmt.Errorf("钉钉请求失败（HTTP %d / code %d）：%s", status, result.Code, msg)
	}
	return ChannelDelivery{}, nil
}

func (s *ChannelSender) sendLarkStyle(ctx context.Context, provider string, config map[string]any, message Message) (ChannelDelivery, error) {
	webhook, err := requiredString(config, "webhook")
	if err != nil {
		return ChannelDelivery{}, fmt.Errorf("%s %w", provider, err)
	}
	secret, err := requiredString(config, "secret")
	if err != nil {
		return ChannelDelivery{}, fmt.Errorf("%s %w", provider, err)
	}
	timestamp := time.Now().Unix()
	stringToSign := strconv.FormatInt(timestamp, 10) + "\n" + secret
	mac := hmac.New(sha256.New, []byte(stringToSign))
	_, _ = mac.Write(nil)
	payload := struct {
		Timestamp string `json:"timestamp"`
		Sign      string `json:"sign"`
		Message   string `json:"msg_type"`
		Content   struct {
			Text string `json:"text"`
		} `json:"content"`
	}{
		Timestamp: strconv.FormatInt(timestamp, 10),
		Sign:      base64.StdEncoding.EncodeToString(mac.Sum(nil)),
		Message:   "text",
	}
	payload.Content.Text = truncateRunes(joinMessage(message), 20_000)
	status, raw, err := s.postJSON(ctx, provider, webhook, payload)
	if err != nil {
		return ChannelDelivery{}, err
	}
	var result struct {
		Code    int    `json:"code"`
		Message string `json:"msg"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return ChannelDelivery{}, fmt.Errorf("%s 响应格式无效（HTTP %d）", provider, status)
	}
	if status < 200 || status >= 300 || result.Code != 0 {
		msg := cleanProviderMessage(result.Message)
		if msg == "" {
			msg = http.StatusText(status)
		}
		return ChannelDelivery{}, fmt.Errorf("%s 请求失败（HTTP %d / code %d）：%s", provider, status, result.Code, msg)
	}
	return ChannelDelivery{}, nil
}

func (s *ChannelSender) postJSON(ctx context.Context, provider, endpoint string, payload any) (int, []byte, error) {
	return s.postJSONHeaders(ctx, provider, endpoint, payload, nil)
}

func (s *ChannelSender) postJSONHeaders(ctx context.Context, provider, endpoint string, payload any, headers map[string]string) (int, []byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, fmt.Errorf("%s 请求编码失败", provider)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, nil, fmt.Errorf("%s 请求创建失败", provider)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", pushUserAgent)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, nil, safeNetworkError(provider, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, providerBodyMax))
	if err != nil {
		return 0, nil, fmt.Errorf("%s 响应读取失败", provider)
	}
	return resp.StatusCode, raw, nil
}

func (s *ChannelSender) sendTelegram(ctx context.Context, config map[string]any, message Message) (ChannelDelivery, error) {
	token, err := requiredString(config, "token")
	if err != nil {
		return ChannelDelivery{}, fmt.Errorf("Telegram %w", err)
	}
	chatID, err := requiredString(config, "chat")
	if err != nil {
		return ChannelDelivery{}, fmt.Errorf("Telegram %w", err)
	}
	if !telegramBotTokenPattern.MatchString(token) {
		return ChannelDelivery{}, errors.New("Telegram Bot Token 格式无效")
	}
	if !telegramChatIDPattern.MatchString(chatID) {
		return ChannelDelivery{}, errors.New("Telegram Chat ID 必须是私聊或群聊的数字 ID")
	}
	if strings.HasPrefix(chatID, "-") {
		chatType, typeErr := s.telegramChatType(ctx, token, chatID)
		if typeErr != nil {
			return ChannelDelivery{}, typeErr
		}
		if chatType != "group" && chatType != "supergroup" {
			return ChannelDelivery{}, errors.New("Telegram 目标不是群聊，频道 Chat ID 不受支持")
		}
	}

	base, err := url.Parse(s.telegramBaseURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return ChannelDelivery{}, errors.New("Telegram API 地址无效")
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/bot" + token + "/sendMessage"
	base.RawQuery = ""
	base.Fragment = ""

	// 省略 allow_paid_broadcast，避免消耗 Telegram Stars。
	payload := struct {
		ChatID string `json:"chat_id"`
		Text   string `json:"text"`
	}{
		ChatID: chatID,
		Text:   truncateRunes(joinMessage(message), 4096),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return ChannelDelivery{}, errors.New("Telegram 请求编码失败")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base.String(), bytes.NewReader(body))
	if err != nil {
		return ChannelDelivery{}, errors.New("Telegram 请求创建失败")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", pushUserAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return ChannelDelivery{}, safeNetworkError("Telegram", err)
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, providerBodyMax))
	if readErr != nil {
		return ChannelDelivery{}, errors.New("Telegram 响应读取失败")
	}
	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	_ = json.Unmarshal(raw, &result)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !result.OK {
		msg := cleanProviderMessage(result.Description)
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return ChannelDelivery{}, fmt.Errorf("Telegram 请求失败（HTTP %d）：%s", resp.StatusCode, msg)
	}
	return ChannelDelivery{ProviderID: strconv.FormatInt(result.Result.MessageID, 10)}, nil
}

func (s *ChannelSender) telegramChatType(ctx context.Context, token, chatID string) (string, error) {
	base, err := url.Parse(s.telegramBaseURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", errors.New("Telegram API 地址无效")
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/bot" + token + "/getChat"
	query := base.Query()
	query.Set("chat_id", chatID)
	base.RawQuery = query.Encode()
	base.Fragment = ""

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return "", errors.New("Telegram 群聊校验请求创建失败")
	}
	req.Header.Set("User-Agent", pushUserAgent)
	resp, err := s.client.Do(req)
	if err != nil {
		return "", safeNetworkError("Telegram", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, providerBodyMax))
	if err != nil {
		return "", errors.New("Telegram 群聊校验响应读取失败")
	}
	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      struct {
			Type string `json:"type"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", errors.New("Telegram 群聊校验响应格式无效")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !result.OK {
		message := cleanProviderMessage(result.Description)
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return "", fmt.Errorf("Telegram 群聊校验失败（HTTP %d）：%s", resp.StatusCode, message)
	}
	return result.Result.Type, nil
}

func (s *ChannelSender) DiscoverTelegramChat(ctx context.Context, config map[string]any) (string, error) {
	token, err := requiredString(config, "token")
	if err != nil {
		return "", fmt.Errorf("Telegram %w", err)
	}
	if !telegramBotTokenPattern.MatchString(token) {
		return "", errors.New("Telegram Bot Token 格式无效")
	}
	base, err := url.Parse(s.telegramBaseURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", errors.New("Telegram API 地址无效")
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/bot" + token + "/getUpdates"
	query := base.Query()
	query.Set("limit", "100")
	query.Set("offset", "-100")
	query.Set("timeout", "0")
	query.Set("allowed_updates", `["message"]`)
	base.RawQuery = query.Encode()
	base.Fragment = ""

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return "", errors.New("Telegram 自动识别请求创建失败")
	}
	req.Header.Set("User-Agent", pushUserAgent)
	resp, err := s.client.Do(req)
	if err != nil {
		return "", safeNetworkError("Telegram", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, providerBodyMax))
	if err != nil {
		return "", errors.New("Telegram 自动识别响应读取失败")
	}
	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      []struct {
			Message *struct {
				Chat struct {
					ID   int64  `json:"id"`
					Type string `json:"type"`
				} `json:"chat"`
			} `json:"message"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", errors.New("Telegram 自动识别响应格式无效")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !result.OK {
		message := cleanProviderMessage(result.Description)
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return "", fmt.Errorf("Telegram 自动识别失败（HTTP %d）：%s", resp.StatusCode, message)
	}
	chats := make(map[int64]struct{})
	for _, update := range result.Result {
		if update.Message == nil {
			continue
		}
		chat := update.Message.Chat
		if chat.Type != "private" && chat.Type != "group" && chat.Type != "supergroup" {
			continue
		}
		if telegramChatIDPattern.MatchString(strconv.FormatInt(chat.ID, 10)) {
			chats[chat.ID] = struct{}{}
		}
	}
	if len(chats) == 0 {
		return "", errors.New("未发现可用会话；请先在私聊发送 /start，或在目标群发送 /start@机器人用户名，再重试")
	}
	if len(chats) > 1 {
		return "", errors.New("发现多个可用会话，无法安全判断接收目标；请手动填写 Chat ID")
	}
	for chatID := range chats {
		return strconv.FormatInt(chatID, 10), nil
	}
	return "", errors.New("未发现可用的 Telegram 会话")
}

func (s *ChannelSender) sendPushPlus(ctx context.Context, config map[string]any, message Message) (ChannelDelivery, error) {
	token, err := requiredString(config, "token")
	if err != nil {
		return ChannelDelivery{}, fmt.Errorf("PushPlus %w", err)
	}
	if !pushPlusTokenPattern.MatchString(token) {
		return ChannelDelivery{}, errors.New("PushPlus token 格式无效")
	}
	endpoint, err := url.Parse(s.pushPlusURL)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "https" && endpoint.Scheme != "http") {
		return ChannelDelivery{}, errors.New("PushPlus API 地址无效")
	}

	// 固定免费 wechat 与纯文本；禁止付费与群发参数。
	payload := struct {
		Token    string `json:"token"`
		Title    string `json:"title"`
		Content  string `json:"content"`
		Template string `json:"template"`
		Channel  string `json:"channel"`
	}{
		Token:    token,
		Title:    truncateRunes(strings.TrimSpace(message.Title), 100),
		Content:  truncateRunes(strings.TrimSpace(message.Body), 20_000),
		Template: "txt",
		Channel:  "wechat",
	}
	if payload.Content == "" {
		payload.Content = payload.Title
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return ChannelDelivery{}, errors.New("PushPlus 请求编码失败")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.pushPlusURL, bytes.NewReader(body))
	if err != nil {
		return ChannelDelivery{}, errors.New("PushPlus 请求创建失败")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", pushUserAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return ChannelDelivery{}, safeNetworkError("PushPlus", err)
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, providerBodyMax))
	if readErr != nil {
		return ChannelDelivery{}, errors.New("PushPlus 响应读取失败")
	}
	var result struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(raw, &result)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || result.Code != 200 {
		msg := cleanProviderMessage(result.Msg)
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return ChannelDelivery{}, fmt.Errorf("PushPlus 请求失败（HTTP %d / code %d）：%s", resp.StatusCode, result.Code, msg)
	}
	providerID := ""
	_ = json.Unmarshal(result.Data, &providerID)
	return ChannelDelivery{
		ProviderID: providerID,
		Async:      true,
		Note:       "PushPlus 已受理，最终结果由其平台异步投递",
	}, nil
}

func requiredString(config map[string]any, key string) (string, error) {
	value, ok := config[key].(string)
	value = strings.TrimSpace(value)
	if !ok || value == "" {
		return "", fmt.Errorf("缺少 %s", key)
	}
	return value, nil
}

func joinMessage(message Message) string {
	title := strings.TrimSpace(message.Title)
	body := strings.TrimSpace(message.Body)
	switch {
	case title == "":
		return body
	case body == "":
		return title
	default:
		return title + "\n\n" + body
	}
}

func safeNetworkError(provider string, err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%s 请求超时", provider)
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("%s 请求已取消", provider)
	default:
		// 禁止回显完整 URL：Telegram token 在 path 中。
		return fmt.Errorf("%s 网络请求失败", provider)
	}
}

func cleanProviderMessage(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	return truncateRunes(value, 160)
}

func truncateRunes(value string, max int) string {
	runes := []rune(value)
	if max <= 0 || len(runes) <= max {
		return value
	}
	return strings.TrimSpace(string(runes[:max])) + "…"
}
