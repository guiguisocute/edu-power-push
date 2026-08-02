package mailer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/edu-power-push/edu-power-push/backend/internal/config"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	ses "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/ses/v20201002"
)

type TencentSES struct {
	from       string
	templateID uint64
	client     tencentSESSender
}

type tencentSESSender interface {
	SendEmailWithContext(context.Context, *ses.SendEmailRequest) (*ses.SendEmailResponse, error)
}

func NewTencentSES(cfg config.Mail) (*TencentSES, error) {
	if cfg.Tencent.SecretID == "" || cfg.Tencent.SecretKey == "" || cfg.Tencent.Region == "" || cfg.Tencent.FromEmail == "" || cfg.Tencent.TemplateID == 0 {
		return nil, errors.New("Tencent SES credentials, region, sender, and template ID are required")
	}
	clientProfile := profile.NewClientProfile()
	clientProfile.HttpProfile.Endpoint = "ses.tencentcloudapi.com"
	client, err := ses.NewClient(common.NewCredential(cfg.Tencent.SecretID, cfg.Tencent.SecretKey), cfg.Tencent.Region, clientProfile)
	if err != nil {
		return nil, err
	}
	return &TencentSES{from: cfg.Tencent.FromEmail, templateID: cfg.Tencent.TemplateID, client: client}, nil
}

func (s *TencentSES) Name() string { return "tencent_ses" }

func (s *TencentSES) Send(ctx context.Context, message Message) (string, error) {
	templateData, err := json.Marshal(message.TemplateData)
	if err != nil {
		return "", fmt.Errorf("encode Tencent template data: %w", err)
	}
	request := ses.NewSendEmailRequest()
	request.FromEmailAddress = common.StringPtr(s.from)
	request.Subject = common.StringPtr(message.Subject)
	request.Destination = []*string{common.StringPtr(message.Recipient)}
	request.Template = &ses.Template{TemplateID: common.Uint64Ptr(s.templateID), TemplateData: common.StringPtr(string(templateData))}
	request.TriggerType = common.Uint64Ptr(1)
	response, err := s.client.SendEmailWithContext(ctx, request)
	if err != nil {
		return "", err
	}
	if response == nil || response.Response == nil || response.Response.MessageId == nil {
		return "", errors.New("Tencent SES returned no message ID")
	}
	return *response.Response.MessageId, nil
}
