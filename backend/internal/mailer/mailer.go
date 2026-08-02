package mailer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/mail"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/config"
	"github.com/edu-power-push/edu-power-push/backend/internal/notification"
	"github.com/edu-power-push/edu-power-push/backend/internal/secrets"
	"github.com/jackc/pgx/v5/pgxpool"
	tencentErrors "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/errors"
)

var mailRetryDelays = []time.Duration{250 * time.Millisecond, time.Second}

type Delivery struct {
	ID              string     `json:"id"`
	Provider        string     `json:"provider"`
	MessageType     string     `json:"message_type"`
	RecipientMasked string     `json:"recipient_masked"`
	Status          string     `json:"status"`
	CreatedAt       time.Time  `json:"created_at"`
	SentAt          *time.Time `json:"sent_at"`
}

// InlineImage 为 multipart/related 内嵌图，优于外链显示。
type InlineImage struct {
	ContentID   string // 无尖括号；HTML 用 cid:ContentID
	Filename    string
	ContentType string
	Data        []byte
}

type Message struct {
	Type         string
	Recipient    string
	Subject      string
	Text         string
	HTML         string
	TemplateData map[string]string
	Inline       []InlineImage
}

type Provider interface {
	Name() string
	Send(context.Context, Message) (string, error)
}

type Service struct {
	pool     *pgxpool.Pool
	provider Provider
	// 邮件内站点链接前缀。
	baseURL string
	/* resolver 非空时现取供应商与 baseURL，面板改完即生效。 */
	resolver *Resolver
	brandMu  sync.Mutex
	brandVal string
	brandAt  time.Time
}

/*
brand 读取 frontend_config 站点名，缓存 30 秒。
直接查库以避免 mailer 反向依赖 storage。
*/
func (s *Service) brand(ctx context.Context) string {
	if s == nil || s.pool == nil {
		return DefaultBrandName
	}
	s.brandMu.Lock()
	defer s.brandMu.Unlock()
	if s.brandVal != "" && time.Since(s.brandAt) < 30*time.Second {
		return s.brandVal
	}
	var name string
	err := s.pool.QueryRow(ctx, `
		SELECT coalesce(config -> 'display' ->> 'brand_name', '') FROM frontend_config WHERE singleton
	`).Scan(&name)
	if err != nil || strings.TrimSpace(name) == "" {
		name = DefaultBrandName
	}
	s.brandVal, s.brandAt = name, time.Now()
	return name
}

// renderBranded 渲染产品邮件并注入 {{brand}}。
func (s *Service) renderBranded(ctx context.Context, name TemplateName, data map[string]string) (string, error) {
	branded := make(map[string]string, len(data)+1)
	maps.Copy(branded, data)
	branded["brand"] = s.brand(ctx)
	return RenderTemplate(name, branded)
}

// NewDynamic 构建跟随 system_settings 的邮件服务，始终非 nil。
func NewDynamic(cfg config.Mail, pool *pgxpool.Pool, box *secrets.Box) *Service {
	return &Service{
		pool:     pool,
		baseURL:  cfg.BaseURL,
		resolver: NewResolver(cfg, DBSettings{Pool: pool, Box: box}),
	}
}

// Invalidate 使下次发信立即重读设置。
func (s *Service) Invalidate() {
	if s != nil && s.resolver != nil {
		s.resolver.Invalidate()
	}
}

// current 返回本次发信用的供应商与链接前缀。
func (s *Service) current(ctx context.Context) (Provider, string, error) {
	if s == nil {
		return nil, "", errors.New("mail provider is not configured")
	}
	if s.resolver == nil {
		return s.provider, s.baseURL, nil
	}
	provider, merged, err := s.resolver.Current(ctx)
	if err != nil {
		return nil, merged.BaseURL, err
	}
	if provider == nil {
		return nil, merged.BaseURL, errors.New("mail provider is not configured")
	}
	return provider, merged.BaseURL, nil
}

// Ready 为 false 时处理器返回 503。
func (s *Service) Ready(ctx context.Context) bool {
	if s == nil {
		return false
	}
	provider, _, err := s.current(ctx)
	return err == nil && provider != nil
}

// ProviderName 返回生效供应商名；未配置返回空串。
func (s *Service) ProviderName(ctx context.Context) string {
	provider, _, err := s.current(ctx)
	if err != nil || provider == nil {
		return ""
	}
	return provider.Name()
}

func (s *Service) SendTest(ctx context.Context, recipient string) (Delivery, error) {
	brand := s.brand(ctx)
	message := brand + " 邮件链路已成功连通。"
	return s.Send(ctx, Message{
		Type:      "manual_test",
		Recipient: recipient,
		Subject:   brand + " 邮件链路测试",
		Text:      message,
		HTML:      "<p>" + htmlEscape(message) + "</p>",
		TemplateData: map[string]string{
			"title": "邮件链路测试", "message": message,
		},
	})
}

func (s *Service) SendScanSummary(ctx context.Context, recipient, runID, status string, counters any) (Delivery, error) {
	payload, err := json.Marshal(counters)
	if err != nil {
		return Delivery{}, fmt.Errorf("render scan summary: %w", err)
	}
	message := fmt.Sprintf("扫描 %s 已结束，状态：%s，计数：%s", runID, status, payload)
	return s.Send(ctx, Message{
		Type: "scan_summary", Recipient: recipient, Subject: s.brand(ctx) + " 扫描摘要",
		Text: message, HTML: "<p>" + htmlEscape(message) + "</p>",
		TemplateData: map[string]string{"title": "扫描摘要", "message": message},
	})
}

func (s *Service) SendWorkerFailure(ctx context.Context, recipient, message string) (Delivery, error) {
	return s.Send(ctx, Message{
		Type: "worker_failure", Recipient: recipient, Subject: s.brand(ctx) + " Worker 告警",
		Text: message, HTML: "<p>" + htmlEscape(message) + "</p>",
		TemplateData: map[string]string{"title": "Worker 告警", "message": message},
	})
}

func (s *Service) SendAnomalySummary(ctx context.Context, recipient, runID string, criticalCount int) (Delivery, error) {
	message := fmt.Sprintf("扫描 %s 产生 %d 条尚未确认的严重数据异常，请登录内部运维面板复核。", runID, criticalCount)
	return s.Send(ctx, Message{
		Type: "anomaly_summary", Recipient: recipient, Subject: s.brand(ctx) + " 严重数据异常",
		Text: message, HTML: "<p>" + htmlEscape(message) + "</p>",
		TemplateData: map[string]string{"title": "严重数据异常", "message": message},
	})
}

/* ---- 产品邮件（mailtemplate HTML）---------------------------------
   上文为运维告警；下文为发给用户的正式邮件。 */

// SendBalanceAlert 发送低额预警，与其它渠道共用结构化事实。
func (s *Service) SendBalanceAlert(ctx context.Context, recipient string, message notification.Message) (Delivery, error) {
	html, err := s.renderBranded(ctx, TemplateBalanceAlert, map[string]string{
		"balance":         message.Data.BalanceYuan,
		"threshold":       message.Data.ThresholdYuan,
		"meter":           message.Meter.Number,
		"building":        message.Meter.Building,
		"floor":           message.Meter.Floor,
		"room":            message.Meter.Room,
		"unsubscribe_url": s.link(ctx, "/config"),
	})
	if err != nil {
		return Delivery{}, err
	}
	return s.Send(ctx, withBrandMark(Message{
		Type: "balance_alert", Recipient: recipient, Subject: "电费余额提醒 · " + s.brand(ctx),
		Text: message.Body,
		HTML: html,
	}))
}

// SendPushTest 发送用户触发的渠道核对。字段仅由服务端事实生成。
func (s *Service) SendPushTest(ctx context.Context, recipient, balance, updatedAt, location, meter string) (Delivery, error) {
	htmlBody, err := s.renderBranded(ctx, TemplatePushTest, map[string]string{
		"balance":    balance,
		"updated_at": updatedAt,
		"location":   location,
		"meter":      meter,
		"base_url":   s.link(ctx, "/"),
	})
	if err != nil {
		return Delivery{}, err
	}
	text := strings.Join([]string{
		"剩余电费：" + balance,
		"数据更新时间：" + updatedAt,
		"宿舍位置：" + location,
		"电表号：" + meter,
		"",
		"请核对以上信息是否与当前绑定一致。此消息由已登录用户主动触发。",
	}, "\n")
	return s.Send(ctx, withBrandMark(Message{
		Type:      "push_test",
		Recipient: recipient,
		Subject:   s.brand(ctx) + " 测试",
		Text:      text,
		HTML:      htmlBody,
	}))
}

// SendUsageSummary 发送定时用电摘要，与其它渠道共用结构化事实。
func (s *Service) SendUsageSummary(ctx context.Context, recipient string, message notification.Message) (Delivery, error) {
	html, err := s.renderBranded(ctx, TemplateUsageSummary, map[string]string{
		"balance":         message.Data.BalanceYuan,
		"usage":           message.Data.UsageKWH,
		"period":          message.Data.Period,
		"meter":           message.Meter.Number,
		"building":        message.Meter.Building,
		"floor":           message.Meter.Floor,
		"room":            message.Meter.Room,
		"unsubscribe_url": s.link(ctx, "/config"),
	})
	if err != nil {
		return Delivery{}, err
	}
	return s.Send(ctx, withBrandMark(Message{
		Type: "usage_summary", Recipient: recipient, Subject: "电费情况通知 · " + s.brand(ctx),
		Text: message.Body,
		HTML: html,
	}))
}

// SendMeterUnbound 通知管理员已解除电表绑定。
func (s *Service) SendMeterUnbound(ctx context.Context, recipient, meter, location, unboundAt string) (Delivery, error) {
	html, err := s.renderBranded(ctx, TemplateMeterUnbound, map[string]string{
		"meter":      meter,
		"location":   location,
		"unbound_at": unboundAt,
		"base_url":   s.link(ctx, "/"),
	})
	if err != nil {
		return Delivery{}, err
	}
	text := fmt.Sprintf("电表 %s（%s）已于 %s 解除绑定。之后将不再接收该电表的余额提醒与用电推送。", meter, location, unboundAt)
	return s.Send(ctx, withBrandMark(Message{
		Type: "meter_unbound", Recipient: recipient, Subject: "解除电表绑定 · " + s.brand(ctx),
		Text: text, HTML: html,
		TemplateData: map[string]string{"title": "解除电表绑定", "message": text},
	}))
}

// SendAccountDisabled 通知账号已被管理员禁用。
func (s *Service) SendAccountDisabled(ctx context.Context, recipient, disabledAt, reason string) (Delivery, error) {
	html, err := s.renderBranded(ctx, TemplateAccountDisabled, map[string]string{
		"email":       recipient,
		"disabled_at": disabledAt,
		"reason":      reason,
		"base_url":    s.link(ctx, "/"),
	})
	if err != nil {
		return Delivery{}, err
	}
	text := fmt.Sprintf("账号 %s 已于 %s 被禁用。原因：%s。已登录会话已失效，推送提醒已停止。", recipient, disabledAt, reason)
	return s.Send(ctx, withBrandMark(Message{
		Type: "account_disabled", Recipient: recipient, Subject: "账号已禁用 · " + s.brand(ctx),
		Text: text, HTML: html,
		TemplateData: map[string]string{"title": "账号已禁用", "message": text},
	}))
}

// SendAccountDeleted 通知用户数据已删除。
func (s *Service) SendAccountDeleted(ctx context.Context, recipient, deletedAt string) (Delivery, error) {
	html, err := s.renderBranded(ctx, TemplateAccountDeleted, map[string]string{
		"email":      recipient,
		"deleted_at": deletedAt,
		"base_url":   s.link(ctx, "/"),
	})
	if err != nil {
		return Delivery{}, err
	}
	text := fmt.Sprintf("账号 %s 已于 %s 删除，账号资料、会话、电表绑定与推送配置已清除，此操作不可恢复。", recipient, deletedAt)
	return s.Send(ctx, withBrandMark(Message{
		Type: "account_deleted", Recipient: recipient, Subject: "账号已删除 · " + s.brand(ctx),
		Text: text, HTML: html,
		TemplateData: map[string]string{"title": "账号已删除", "message": text},
	}))
}

// SendVerificationCode 发送注册验证码。
func (s *Service) SendVerificationCode(ctx context.Context, recipient, code string, expireMinutes int) (Delivery, error) {
	return s.sendCodeMail(ctx, TemplateVerificationCode, "verification_code",
		"注册验证码 · "+s.brand(ctx), recipient, code, expireMinutes, nil)
}

// SendNotificationRecipientVerificationCode 验证附加推送收件邮箱归属。
func (s *Service) SendNotificationRecipientVerificationCode(ctx context.Context, recipient, code string, expireMinutes int) (Delivery, error) {
	return s.sendCodeMail(ctx, TemplateNotificationRecipientVerification, "verification_code",
		"验证推送收件邮箱 · "+s.brand(ctx), recipient, code, expireMinutes, nil)
}

// SendPasswordReset 发送找回密码验证码。
func (s *Service) SendPasswordReset(ctx context.Context, recipient, code string, expireMinutes int) (Delivery, error) {
	return s.sendCodeMail(ctx, TemplatePasswordReset, "password_reset",
		"找回密码 · "+s.brand(ctx), recipient, code, expireMinutes, nil)
}

func (s *Service) sendCodeMail(
	ctx context.Context, template TemplateName, messageType, subject, recipient, code string,
	expireMinutes int, extra map[string]string,
) (Delivery, error) {
	data := map[string]string{
		"code":           code,
		"expire_minutes": strconv.Itoa(expireMinutes),
		"base_url":       s.link(ctx, "/"),
	}
	for key, value := range extra {
		data[key] = value
	}
	html, err := s.renderBranded(ctx, template, data)
	if err != nil {
		return Delivery{}, err
	}
	return s.Send(ctx, withBrandMark(Message{
		Type: messageType, Recipient: recipient, Subject: subject,
		Text: fmt.Sprintf("验证码 %s，%d 分钟内有效。若不是本人操作请忽略本邮件。", code, expireMinutes),
		HTML: html,
	}))
}

// withBrandMark 内嵌品牌 PNG；cid 比 SVG 与外链更稳。
func withBrandMark(message Message) Message {
	png := brandMarkPNGBytes()
	if len(png) == 0 {
		return message
	}
	message.Inline = append(message.Inline, InlineImage{
		ContentID:   brandMarkContentID,
		Filename:    "brand-mark.png",
		ContentType: "image/png",
		Data:        png,
	})
	return message
}

// link 拼站点链接；无 base 时退回相对路径，按钮不可点。
func (s *Service) link(ctx context.Context, path string) string {
	if s == nil {
		return path
	}
	// 跟随当前配置，而非仅用启动时 baseURL。
	base := s.baseURL
	if s.resolver != nil {
		if _, merged, err := s.resolver.Current(ctx); err == nil && merged.BaseURL != "" {
			base = merged.BaseURL
		}
	}
	if base == "" {
		return path
	}
	return base + path
}

func (s *Service) Send(ctx context.Context, message Message) (Delivery, error) {
	provider, _, err := s.current(ctx)
	if err != nil {
		return Delivery{}, err
	}
	if provider == nil {
		return Delivery{}, errors.New("mail provider is not configured")
	}
	address, err := mail.ParseAddress(strings.TrimSpace(message.Recipient))
	if err != nil || address.Address != strings.TrimSpace(message.Recipient) {
		return Delivery{}, errors.New("recipient must be one valid email address without a display name")
	}
	if hasHeaderInjection(message.Subject) || message.Subject == "" {
		return Delivery{}, errors.New("subject is invalid")
	}
	var delivery Delivery
	err = s.pool.QueryRow(ctx, `
		INSERT INTO mail_deliveries (provider,message_type,recipient_masked,status)
		VALUES ($1,$2,$3,'pending')
		RETURNING id::text,provider,message_type,recipient_masked,status,created_at,sent_at
	`, provider.Name(), message.Type, maskAddress(address.Address)).Scan(
		&delivery.ID, &delivery.Provider, &delivery.MessageType, &delivery.RecipientMasked,
		&delivery.Status, &delivery.CreatedAt, &delivery.SentAt,
	)
	if err != nil {
		return Delivery{}, fmt.Errorf("create mail delivery audit: %w", err)
	}
	messageID, attempts, sendErr := sendWithRetry(ctx, provider, message, mailRetryDelays)
	if sendErr != nil {
		_, updateErr := s.pool.Exec(ctx, `
			UPDATE mail_deliveries SET status='failed',attempts=$2,error_code=$3,error_message=$4
			WHERE id=$1::uuid
		`, delivery.ID, attempts, "provider_error", truncate(sendErr.Error(), 1000))
		if updateErr != nil {
			return delivery, errors.Join(sendErr, fmt.Errorf("record mail failure: %w", updateErr))
		}
		delivery.Status = "failed"
		return delivery, sendErr
	}
	err = s.pool.QueryRow(ctx, `
		UPDATE mail_deliveries SET status='sent',attempts=$2,
			provider_message_id=NULLIF($3,''),sent_at=now()
		WHERE id=$1::uuid
		RETURNING status,sent_at
	`, delivery.ID, attempts, messageID).Scan(&delivery.Status, &delivery.SentAt)
	if err != nil {
		return delivery, fmt.Errorf("record mail success: %w", err)
	}
	return delivery, nil
}

func sendWithRetry(ctx context.Context, provider Provider, message Message, delays []time.Duration) (string, int, error) {
	for attempt := 1; ; attempt++ {
		messageID, err := provider.Send(ctx, message)
		if err == nil {
			return messageID, attempt, nil
		}
		if !isTransientMailError(err) || attempt > len(delays) {
			return "", attempt, err
		}
		timer := time.NewTimer(delays[attempt-1])
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return "", attempt, errors.Join(err, ctx.Err())
		case <-timer.C:
		}
	}
}

func isTransientMailError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var protocolError *textproto.Error
	if errors.As(err, &protocolError) {
		return protocolError.Code >= 400 && protocolError.Code < 500
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return networkError.Timeout() || networkError.Temporary()
	}
	// Resend 仅对 429 与 5xx 重试。
	var resendErr *resendError
	if errors.As(err, &resendErr) {
		return resendErr.Transient()
	}
	var tencentError *tencentErrors.TencentCloudSDKError
	if errors.As(err, &tencentError) {
		return strings.HasPrefix(tencentError.Code, "InternalError") ||
			strings.HasPrefix(tencentError.Code, "RequestLimitExceeded") ||
			strings.HasPrefix(tencentError.Code, "ResourceUnavailable")
	}
	return false
}

func maskAddress(address string) string {
	parts := strings.SplitN(address, "@", 2)
	if len(parts) != 2 || parts[0] == "" {
		return "***"
	}
	return string([]rune(parts[0])[0]) + "***@" + parts[1]
}

func hasHeaderInjection(value string) bool {
	return strings.ContainsAny(value, "\r\n")
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func htmlEscape(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&#39;")
	return replacer.Replace(value)
}
