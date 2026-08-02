package mailer

import (
	"strings"

	"github.com/edu-power-push/edu-power-push/backend/internal/config"
)

/*
邮件供应商的面板可编辑形态。
环境变量为缺省；面板优先；凭证单独加密。
*/

// 凭证字段名；仅密文存库，读回打码。
const (
	SecretResendAPIKey     = "resend_api_key"
	SecretSMTPPass         = "smtp_pass"
	SecretTencentSecretKey = "tencent_secret_key"
)

// SecretFields 供面板渲染「已配置 / 未配置」。
var SecretFields = []string{SecretResendAPIKey, SecretSMTPPass, SecretTencentSecretKey}

// Providers 为支持的供应商，顺序即面板展示顺序。
var Providers = []string{"resend", "smtp", "tencent_ses"}

type SMTPSettings struct {
	Host    string `json:"host"`
	Port    int    `json:"port"`
	User    string `json:"user"`
	TLSMode string `json:"tls_mode"`
}

type TencentSettings struct {
	SecretID   string `json:"secret_id"`
	Region     string `json:"region"`
	FromEmail  string `json:"from_email"`
	TemplateID uint64 `json:"template_id"`
}

// Settings 为 system_settings['mail'].value 明文部分。
type Settings struct {
	Provider string `json:"provider"`
	From     string `json:"from"`
	AdminTo  string `json:"admin_to"`
	// BaseURL 为邮件内站点链接前缀。
	BaseURL string          `json:"base_url"`
	ReplyTo string          `json:"reply_to"`
	SMTP    SMTPSettings    `json:"smtp"`
	Tencent TencentSettings `json:"tencent"`
}

// SettingsFromConfig 将环境变量转为面板回显形态。
func SettingsFromConfig(cfg config.Mail) Settings {
	return Settings{
		Provider: cfg.Provider,
		From:     cfg.From,
		AdminTo:  cfg.AdminTo,
		BaseURL:  cfg.BaseURL,
		ReplyTo:  cfg.Resend.ReplyTo,
		SMTP: SMTPSettings{
			Host: cfg.SMTP.Host, Port: cfg.SMTP.Port, User: cfg.SMTP.User, TLSMode: cfg.SMTP.TLSMode,
		},
		Tencent: TencentSettings{
			SecretID: cfg.Tencent.SecretID, Region: cfg.Tencent.Region,
			FromEmail: cfg.Tencent.FromEmail, TemplateID: cfg.Tencent.TemplateID,
		},
	}
}

// Apply 用面板设置覆盖环境变量；空字段沿用 base。
func (in Settings) Apply(base config.Mail, secrets map[string]string) config.Mail {
	out := base
	out.Provider = orDefault(in.Provider, base.Provider)
	out.From = orDefault(in.From, base.From)
	out.AdminTo = orDefault(in.AdminTo, base.AdminTo)
	out.BaseURL = orDefault(in.BaseURL, base.BaseURL)

	out.Resend.ReplyTo = orDefault(in.ReplyTo, base.Resend.ReplyTo)
	out.Resend.APIKey = orDefault(secrets[SecretResendAPIKey], base.Resend.APIKey)

	out.SMTP.Host = orDefault(in.SMTP.Host, base.SMTP.Host)
	if in.SMTP.Port > 0 {
		out.SMTP.Port = in.SMTP.Port
	}
	out.SMTP.User = orDefault(in.SMTP.User, base.SMTP.User)
	out.SMTP.TLSMode = orDefault(in.SMTP.TLSMode, base.SMTP.TLSMode)
	out.SMTP.Pass = orDefault(secrets[SecretSMTPPass], base.SMTP.Pass)

	out.Tencent.SecretID = orDefault(in.Tencent.SecretID, base.Tencent.SecretID)
	out.Tencent.Region = orDefault(in.Tencent.Region, base.Tencent.Region)
	out.Tencent.FromEmail = orDefault(in.Tencent.FromEmail, base.Tencent.FromEmail)
	if in.Tencent.TemplateID > 0 {
		out.Tencent.TemplateID = in.Tencent.TemplateID
	}
	out.Tencent.SecretKey = orDefault(secrets[SecretTencentSecretKey], base.Tencent.SecretKey)
	return out
}

// Validate 在保存前检查合并后配置能否发信。
func Validate(merged config.Mail) []string {
	var missing []string
	switch merged.Provider {
	case "":
		return []string{"provider（未选择供应商，邮件功能整体关闭）"}
	case "resend":
		if strings.TrimSpace(merged.Resend.APIKey) == "" {
			missing = append(missing, "Resend API Key")
		}
		if strings.TrimSpace(merged.From) == "" {
			missing = append(missing, "发件人（MAIL_FROM）")
		}
	case "smtp":
		if strings.TrimSpace(merged.SMTP.Host) == "" {
			missing = append(missing, "SMTP 主机")
		}
		if merged.SMTP.Port <= 0 {
			missing = append(missing, "SMTP 端口")
		}
		if strings.TrimSpace(merged.From) == "" {
			missing = append(missing, "发件人（MAIL_FROM）")
		}
	case "tencent_ses":
		if strings.TrimSpace(merged.Tencent.SecretID) == "" {
			missing = append(missing, "腾讯云 SecretId")
		}
		if strings.TrimSpace(merged.Tencent.SecretKey) == "" {
			missing = append(missing, "腾讯云 SecretKey")
		}
		if strings.TrimSpace(merged.Tencent.FromEmail) == "" {
			missing = append(missing, "腾讯云发信地址")
		}
	default:
		return []string{"provider 只能是 resend / smtp / tencent_ses（当前 " + merged.Provider + "）"}
	}
	return missing
}

// IsKnownProvider 供 API 层校验入参。
func IsKnownProvider(name string) bool {
	if name == "" {
		return true // 空表示关闭邮件
	}
	for _, p := range Providers {
		if p == name {
			return true
		}
	}
	return false
}

func orDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}
