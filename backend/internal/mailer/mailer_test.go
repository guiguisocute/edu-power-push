package mailer

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/config"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	ses "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/ses/v20201002"
)

type fakeProvider struct{}

func (fakeProvider) Name() string                                  { return "smtp" }
func (fakeProvider) Send(context.Context, Message) (string, error) { return "id", nil }

type scriptedProvider struct {
	errors []error
	calls  int
}

func (p *scriptedProvider) Name() string { return "smtp" }
func (p *scriptedProvider) Send(context.Context, Message) (string, error) {
	p.calls++
	if p.calls <= len(p.errors) && p.errors[p.calls-1] != nil {
		return "", p.errors[p.calls-1]
	}
	return "message-id", nil
}

type temporaryNetworkError struct{}

func (temporaryNetworkError) Error() string   { return "temporary network failure" }
func (temporaryNetworkError) Timeout() bool   { return false }
func (temporaryNetworkError) Temporary() bool { return true }

type fakeTencentSender struct {
	request *ses.SendEmailRequest
	err     error
}

func (f *fakeTencentSender) SendEmailWithContext(_ context.Context, request *ses.SendEmailRequest) (*ses.SendEmailResponse, error) {
	f.request = request
	if f.err != nil {
		return nil, f.err
	}
	return &ses.SendEmailResponse{Response: &ses.SendEmailResponseParams{MessageId: common.StringPtr("ses-message-id")}}, nil
}

func TestMaskAddress(t *testing.T) {
	if got := maskAddress("admin@example.com"); got != "a***@example.com" {
		t.Fatalf("maskAddress() = %q", got)
	}
}

func TestSMTPSendAgainstLocalServer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	received := make(chan string, 1)
	serverErrors := make(chan error, 1)
	go serveOneSMTP(listener, received, serverErrors)

	port := listener.Addr().(*net.TCPAddr).Port
	provider, err := NewSMTP(config.Mail{
		From: "sender@example.com",
		SMTP: config.SMTPMail{Host: "127.0.0.1", Port: port, TLSMode: "none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	messageID, err := provider.Send(ctx, Message{Recipient: "admin@example.com", Subject: "test", Text: "plain", HTML: "<p>html</p>"})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if messageID == "" {
		t.Fatal("Send() returned an empty message ID")
	}
	select {
	case err := <-serverErrors:
		t.Fatal(err)
	case raw := <-received:
		if !strings.Contains(raw, "plain") || !strings.Contains(raw, "<p>html</p>") {
			t.Fatalf("SMTP body is incomplete: %q", raw)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func serveOneSMTP(listener net.Listener, received chan<- string, failures chan<- error) {
	conn, err := listener.Accept()
	if err != nil {
		failures <- err
		return
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	write := func(value string) bool {
		if _, err := writer.WriteString(value); err != nil {
			failures <- err
			return false
		}
		if err := writer.Flush(); err != nil {
			failures <- err
			return false
		}
		return true
	}
	if !write("220 localhost ESMTP\r\n") {
		return
	}
	var body strings.Builder
	inData := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			failures <- err
			return
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if inData {
			if trimmed == "." {
				if !write("250 queued\r\n") {
					return
				}
				received <- body.String()
				inData = false
				continue
			}
			body.WriteString(line)
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "EHLO"):
			if !write("250-localhost\r\n250 OK\r\n") {
				return
			}
		case strings.HasPrefix(trimmed, "MAIL FROM:"), strings.HasPrefix(trimmed, "RCPT TO:"):
			if !write("250 OK\r\n") {
				return
			}
		case trimmed == "DATA":
			if !write("354 end with dot\r\n") {
				return
			}
			inData = true
		case trimmed == "QUIT":
			_ = write("221 bye\r\n")
			return
		default:
			failures <- fmt.Errorf("unexpected SMTP command %q on port %s", trimmed, strconv.Itoa(listener.Addr().(*net.TCPAddr).Port))
			return
		}
	}
}

func TestComposeMIME(t *testing.T) {
	raw, id, err := composeMIME("Power Push <sender@example.com>", Message{
		Recipient: "admin@example.com", Subject: "测试", Text: "plain", HTML: "<p>html</p>",
	})
	if err != nil {
		t.Fatalf("composeMIME() error = %v", err)
	}
	message := string(raw)
	for _, expected := range []string{"multipart/alternative", "plain", "<p>html</p>", "Message-ID: <" + id + ">"} {
		if !strings.Contains(message, expected) {
			t.Fatalf("message does not contain %q", expected)
		}
	}
}

func TestComposeMIMERejectsHeaderInjection(t *testing.T) {
	_, _, err := composeMIME("sender@example.com", Message{Recipient: "admin@example.com", Subject: "ok\r\nBcc: bad@example.com"})
	if err == nil {
		t.Fatal("composeMIME() expected an error")
	}
}

func TestNewSMTPRequiresSecureConfiguration(t *testing.T) {
	_, err := NewSMTP(config.Mail{From: "sender@example.com", SMTP: config.SMTPMail{Host: "smtp.example.com", Port: 587, TLSMode: "invalid"}})
	if err == nil {
		t.Fatal("NewSMTP() expected an error")
	}
}

func TestNewSMTPRejectsUnencryptedRemoteServer(t *testing.T) {
	_, err := NewSMTP(config.Mail{
		From: "sender@example.com",
		SMTP: config.SMTPMail{Host: "smtp.example.com", Port: 25, TLSMode: "none"},
	})
	if err == nil || !strings.Contains(err.Error(), "local Mailpit") {
		t.Fatalf("NewSMTP() error = %v, want local-test-only error", err)
	}
}

func TestNewSMTPAllowsUnencryptedLoopbackTestServers(t *testing.T) {
	for _, host := range []string{"localhost", "LOCALHOST.", "mailpit", "MAILPIT.", "127.0.0.1", "127.20.30.40", "::1"} {
		t.Run(host, func(t *testing.T) {
			provider, err := NewSMTP(config.Mail{
				From: "sender@example.com",
				SMTP: config.SMTPMail{Host: host, Port: 1025, TLSMode: "none"},
			})
			if err != nil {
				t.Fatalf("NewSMTP() error = %v", err)
			}
			if provider.host != host {
				t.Fatalf("NewSMTP() host = %q, want %q", provider.host, host)
			}
		})
	}
}

func TestTencentSESSendBuildsTemplateRequest(t *testing.T) {
	fake := &fakeTencentSender{}
	provider := &TencentSES{from: "sender@example.com", templateID: 42, client: fake}
	messageID, err := provider.Send(context.Background(), Message{
		Recipient:    "admin@example.com",
		Subject:      "扫描摘要",
		TemplateData: map[string]string{"title": "扫描摘要", "message": "完成"},
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if messageID != "ses-message-id" {
		t.Fatalf("Send() message ID = %q", messageID)
	}
	request := fake.request
	if request == nil || request.FromEmailAddress == nil || *request.FromEmailAddress != "sender@example.com" {
		t.Fatalf("Send() sender request = %#v", request)
	}
	if request.Subject == nil || *request.Subject != "扫描摘要" {
		t.Fatalf("Send() subject = %#v", request.Subject)
	}
	if len(request.Destination) != 1 || request.Destination[0] == nil || *request.Destination[0] != "admin@example.com" {
		t.Fatalf("Send() destinations = %#v", request.Destination)
	}
	if request.Template == nil || request.Template.TemplateID == nil || *request.Template.TemplateID != 42 {
		t.Fatalf("Send() template = %#v", request.Template)
	}
	var templateData map[string]string
	if request.Template.TemplateData == nil || json.Unmarshal([]byte(*request.Template.TemplateData), &templateData) != nil {
		t.Fatalf("Send() template data = %#v", request.Template.TemplateData)
	}
	if templateData["title"] != "扫描摘要" || templateData["message"] != "完成" {
		t.Fatalf("Send() decoded template data = %#v", templateData)
	}
	if request.TriggerType == nil || *request.TriggerType != 1 {
		t.Fatalf("Send() trigger type = %#v", request.TriggerType)
	}
}

func TestNewTencentSESRequiresCompleteConfiguration(t *testing.T) {
	_, err := NewTencentSES(config.Mail{Tencent: config.TencentMail{SecretID: "incomplete"}})
	if err == nil {
		t.Fatal("NewTencentSES() expected an error")
	}
}

func TestSendWithRetryRetriesTransientFailure(t *testing.T) {
	provider := &scriptedProvider{errors: []error{temporaryNetworkError{}}}
	messageID, attempts, err := sendWithRetry(context.Background(), provider, Message{}, []time.Duration{0})
	if err != nil {
		t.Fatalf("sendWithRetry() error = %v", err)
	}
	if messageID != "message-id" || attempts != 2 || provider.calls != 2 {
		t.Fatalf("sendWithRetry() = id %q, attempts %d, calls %d", messageID, attempts, provider.calls)
	}
}

func TestSendWithRetryDoesNotRetryPermanentFailure(t *testing.T) {
	provider := &scriptedProvider{errors: []error{&textproto.Error{Code: 550, Msg: "rejected"}}}
	_, attempts, err := sendWithRetry(context.Background(), provider, Message{}, []time.Duration{0, 0})
	if err == nil {
		t.Fatal("sendWithRetry() expected an error")
	}
	if attempts != 1 || provider.calls != 1 {
		t.Fatalf("sendWithRetry() attempts = %d, calls = %d", attempts, provider.calls)
	}
}

func TestTransientMailErrorClassification(t *testing.T) {
	if !isTransientMailError(&textproto.Error{Code: 451, Msg: "try later"}) {
		t.Fatal("SMTP 451 should be transient")
	}
	if isTransientMailError(&textproto.Error{Code: 550, Msg: "rejected"}) {
		t.Fatal("SMTP 550 should be permanent")
	}
}

func TestSendRejectsDisplayNameBeforeDatabase(t *testing.T) {
	service := &Service{provider: fakeProvider{}}
	_, err := service.Send(context.Background(), Message{Recipient: "Admin <admin@example.com>", Subject: "test"})
	if err == nil {
		t.Fatal("Send() expected an error")
	}
}
