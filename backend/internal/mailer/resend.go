package mailer

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/config"
)

/* Resend 邮件通道（https://resend.com）。
   直接发完整 HTML；与腾讯云 SES 切换仅改 MAIL_PROVIDER。
   单 POST API，使用 net/http，不引 SDK。 */

const (
	resendEndpoint       = "https://api.resend.com/emails"
	resendRequestTimeout = 20 * time.Second
)

type Resend struct {
	apiKey   string
	from     string
	replyTo  string
	endpoint string
	client   *http.Client
}

func NewResend(cfg config.Mail) (*Resend, error) {
	if strings.TrimSpace(cfg.Resend.APIKey) == "" {
		return nil, errors.New("RESEND_API_KEY is required when MAIL_PROVIDER=resend")
	}
	// 发件人必须属于 Resend 已验证域名。
	from := strings.TrimSpace(cfg.From)
	if from == "" {
		return nil, errors.New("MAIL_FROM is required when MAIL_PROVIDER=resend")
	}
	return &Resend{
		apiKey:   strings.TrimSpace(cfg.Resend.APIKey),
		from:     from,
		replyTo:  strings.TrimSpace(cfg.Resend.ReplyTo),
		endpoint: resendEndpoint,
		client:   &http.Client{Timeout: resendRequestTimeout},
	}, nil
}

func (r *Resend) Name() string { return "resend" }

type resendRequest struct {
	From        string             `json:"from"`
	To          []string           `json:"to"`
	Subject     string             `json:"subject"`
	HTML        string             `json:"html,omitempty"`
	Text        string             `json:"text,omitempty"`
	ReplyTo     string             `json:"reply_to,omitempty"`
	Attachments []resendAttachment `json:"attachments,omitempty"`
}

// resendAttachment 的 content_id 供 HTML cid: 引用。
type resendAttachment struct {
	Filename    string `json:"filename"`
	Content     string `json:"content"` // base64
	ContentID   string `json:"content_id,omitempty"`
	ContentType string `json:"content_type,omitempty"`
}

type resendResponse struct {
	ID      string `json:"id"`
	Message string `json:"message"`
	Name    string `json:"name"`
}

// resendError 保留状态码，供 isTransientMailError 判断重试。
type resendError struct {
	status  int
	code    string
	message string
}

func (e *resendError) Error() string {
	if e.code != "" {
		return fmt.Sprintf("resend %d %s: %s", e.status, e.code, e.message)
	}
	return fmt.Sprintf("resend %d: %s", e.status, e.message)
}

func (e *resendError) Transient() bool {
	return e.status == http.StatusTooManyRequests || e.status >= 500
}

func (r *Resend) Send(ctx context.Context, message Message) (string, error) {
	req := resendRequest{
		From:    r.from,
		To:      []string{message.Recipient},
		Subject: message.Subject,
		HTML:    message.HTML,
		Text:    message.Text,
		ReplyTo: r.replyTo,
	}
	for _, img := range message.Inline {
		if len(img.Data) == 0 {
			continue
		}
		req.Attachments = append(req.Attachments, resendAttachment{
			Filename:    img.Filename,
			Content:     base64.StdEncoding.EncodeToString(img.Data),
			ContentID:   img.ContentID,
			ContentType: img.ContentType,
		})
	}
	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("encode resend request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build resend request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+r.apiKey)
	request.Header.Set("Content-Type", "application/json")

	response, err := r.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("call resend: %w", err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<16))
	if err != nil {
		return "", fmt.Errorf("read resend response: %w", err)
	}
	var decoded resendResponse
	_ = json.Unmarshal(payload, &decoded)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		detail := decoded.Message
		if detail == "" {
			detail = truncate(strings.TrimSpace(string(payload)), 500)
		}
		// 仅回状态码与说明，禁止回显 API key。
		return "", &resendError{status: response.StatusCode, code: decoded.Name, message: detail}
	}
	if decoded.ID == "" {
		return "", errors.New("resend returned no message id")
	}
	return decoded.ID, nil
}
