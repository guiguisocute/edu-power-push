package mailer

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/config"
)

type SMTP struct {
	from    string
	host    string
	port    int
	user    string
	pass    string
	tlsMode string
}

func NewSMTP(cfg config.Mail) (*SMTP, error) {
	host := strings.TrimSpace(cfg.SMTP.Host)
	if cfg.From == "" || host == "" || cfg.SMTP.Port < 1 {
		return nil, errors.New("MAIL_FROM, SMTP_HOST, and SMTP_PORT are required for smtp")
	}
	if _, err := mail.ParseAddress(cfg.From); err != nil {
		return nil, fmt.Errorf("MAIL_FROM: %w", err)
	}
	mode := strings.ToLower(cfg.SMTP.TLSMode)
	if mode != "tls" && mode != "starttls" && mode != "none" {
		return nil, errors.New("SMTP_TLS_MODE must be tls, starttls, or none")
	}
	if mode == "none" && !isLocalTestSMTPHost(host) {
		return nil, errors.New("SMTP_TLS_MODE=none is allowed only for a local Mailpit/test server")
	}
	if (cfg.SMTP.User == "") != (cfg.SMTP.Pass == "") {
		return nil, errors.New("SMTP_USER and SMTP_PASS must be configured together")
	}
	return &SMTP{from: cfg.From, host: host, port: cfg.SMTP.Port, user: cfg.SMTP.User, pass: cfg.SMTP.Pass, tlsMode: mode}, nil
}

func isLocalTestSMTPHost(host string) bool {
	normalized := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	// mailpit 为 compose 固定服务名，等同容器内 localhost。
	if normalized == "localhost" || normalized == "mailpit" {
		return true
	}
	ip := net.ParseIP(normalized)
	return ip != nil && ip.IsLoopback()
}

func (s *SMTP) Name() string { return "smtp" }

func (s *SMTP) Send(ctx context.Context, message Message) (string, error) {
	fromAddress, err := mail.ParseAddress(s.from)
	if err != nil {
		return "", err
	}
	raw, messageID, err := composeMIME(s.from, message)
	if err != nil {
		return "", err
	}
	address := net.JoinHostPort(s.host, strconv.Itoa(s.port))
	dialer := &net.Dialer{Timeout: 20 * time.Second}
	var conn net.Conn
	if s.tlsMode == "tls" {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: s.host}}).DialContext(ctx, "tcp", address)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	client, err := smtp.NewClient(conn, s.host)
	if err != nil {
		return "", err
	}
	defer client.Close()
	if s.tlsMode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return "", errors.New("SMTP server does not advertise STARTTLS")
		}
		if err := client.StartTLS(&tls.Config{MinVersion: tls.VersionTLS12, ServerName: s.host}); err != nil {
			return "", err
		}
	}
	if s.user != "" {
		if err := client.Auth(smtp.PlainAuth("", s.user, s.pass, s.host)); err != nil {
			return "", err
		}
	}
	if err := client.Mail(fromAddress.Address); err != nil {
		return "", err
	}
	if err := client.Rcpt(message.Recipient); err != nil {
		return "", err
	}
	writer, err := client.Data()
	if err != nil {
		return "", err
	}
	if _, err := writer.Write(raw); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	if err := client.Quit(); err != nil {
		return "", err
	}
	return messageID, nil
}

func composeMIME(from string, message Message) ([]byte, string, error) {
	if hasHeaderInjection(from) || hasHeaderInjection(message.Recipient) || hasHeaderInjection(message.Subject) {
		return nil, "", errors.New("mail header contains a newline")
	}
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, "", err
	}
	messageID := hex.EncodeToString(random[:]) + "@edu-power-push.internal"

	// 有内嵌图用 multipart/related；否则仅 alternative。
	altBody, altBoundary, err := composeAlternative(message.Text, message.HTML)
	if err != nil {
		return nil, "", err
	}
	var body bytes.Buffer
	if len(message.Inline) == 0 {
		headers := []string{
			"From: " + from,
			"To: " + message.Recipient,
			"Subject: " + mime.QEncoding.Encode("UTF-8", message.Subject),
			"Date: " + time.Now().Format(time.RFC1123Z),
			"Message-ID: <" + messageID + ">",
			"MIME-Version: 1.0",
			"Content-Type: multipart/alternative; boundary=" + strconv.Quote(altBoundary),
			"",
		}
		if _, err := io.WriteString(&body, strings.Join(headers, "\r\n")+"\r\n"); err != nil {
			return nil, "", err
		}
		if _, err := body.Write(altBody); err != nil {
			return nil, "", err
		}
		return body.Bytes(), messageID, nil
	}

	related := multipart.NewWriter(&body)
	headers := []string{
		"From: " + from,
		"To: " + message.Recipient,
		"Subject: " + mime.QEncoding.Encode("UTF-8", message.Subject),
		"Date: " + time.Now().Format(time.RFC1123Z),
		"Message-ID: <" + messageID + ">",
		"MIME-Version: 1.0",
		"Content-Type: multipart/related; boundary=" + strconv.Quote(related.Boundary()),
		"",
	}
	if _, err := io.WriteString(&body, strings.Join(headers, "\r\n")+"\r\n"); err != nil {
		return nil, "", err
	}
	// 先写 alternative 子体。
	altHeader := make(textproto.MIMEHeader)
	altHeader.Set("Content-Type", "multipart/alternative; boundary="+strconv.Quote(altBoundary))
	altPart, err := related.CreatePart(altHeader)
	if err != nil {
		return nil, "", err
	}
	if _, err := altPart.Write(altBody); err != nil {
		return nil, "", err
	}
	for _, img := range message.Inline {
		if len(img.Data) == 0 {
			continue
		}
		imgHeader := make(textproto.MIMEHeader)
		ct := img.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		imgHeader.Set("Content-Type", ct+"; name=\""+img.Filename+"\"")
		imgHeader.Set("Content-Transfer-Encoding", "base64")
		imgHeader.Set("Content-ID", "<"+img.ContentID+">")
		imgHeader.Set("Content-Disposition", "inline; filename=\""+img.Filename+"\"")
		part, err := related.CreatePart(imgHeader)
		if err != nil {
			return nil, "", err
		}
		encoded := make([]byte, base64.StdEncoding.EncodedLen(len(img.Data)))
		base64.StdEncoding.Encode(encoded, img.Data)
		// 按 RFC 每行 76 字符。
		for i := 0; i < len(encoded); i += 76 {
			end := i + 76
			if end > len(encoded) {
				end = len(encoded)
			}
			if _, err := part.Write(encoded[i:end]); err != nil {
				return nil, "", err
			}
			if _, err := part.Write([]byte("\r\n")); err != nil {
				return nil, "", err
			}
		}
	}
	if err := related.Close(); err != nil {
		return nil, "", err
	}
	return body.Bytes(), messageID, nil
}

func composeAlternative(text, html string) (body []byte, boundary string, err error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	boundary = w.Boundary()
	// 固定 plain 在前、html 在后。
	for _, part := range []struct {
		contentType string
		content     string
	}{
		{"text/plain; charset=UTF-8", text},
		{"text/html; charset=UTF-8", html},
	} {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Type", part.contentType)
		header.Set("Content-Transfer-Encoding", "8bit")
		pw, err := w.CreatePart(header)
		if err != nil {
			return nil, "", err
		}
		if _, err := io.WriteString(pw, part.content); err != nil {
			return nil, "", err
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), boundary, nil
}
