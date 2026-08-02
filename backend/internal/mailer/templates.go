package mailer

import (
	"embed"
	"fmt"
	"maps"
	"regexp"
	"strings"
)

/* 产品邮件模板经 go:embed 进二进制，运行时无外部路径。
   占位符为 {{name}}；值经 HTML 转义后替换。 */

//go:embed templates/*
var templateFiles embed.FS

// 品牌标用 PNG 内嵌；邮件客户端常剥离内联 SVG。
const brandMarkContentID = "brand-mark"

func brandMarkPNGBytes() []byte {
	raw, err := templateFiles.ReadFile("templates/brand-mark.png")
	if err != nil {
		return nil
	}
	return raw
}

type TemplateName string

const (
	// 低额预警：balance、threshold、meter、building、floor、room、unsubscribe_url
	TemplateBalanceAlert TemplateName = "balance_alert"
	// 用电摘要：balance、usage、period、meter、building、floor、room、unsubscribe_url
	TemplateUsageSummary TemplateName = "usage_summary"
	// 注册验证码：code、expire_minutes、base_url
	TemplateVerificationCode TemplateName = "verification_code"
	// 推送收件邮箱验证：code、expire_minutes、base_url
	TemplateNotificationRecipientVerification TemplateName = "notification_recipient_verification"
	// 找回密码：code、expire_minutes、base_url
	TemplatePasswordReset TemplateName = "password_reset"
	// 渠道测试：balance、updated_at、location、meter、base_url
	TemplatePushTest TemplateName = "push_test"
	// 解除电表绑定：meter、location、unbound_at、base_url
	TemplateMeterUnbound TemplateName = "meter_unbound"
	// 禁用账号：email、disabled_at、reason、base_url
	TemplateAccountDisabled TemplateName = "account_disabled"
	// 账号删除：email、deleted_at、base_url
	TemplateAccountDeleted TemplateName = "account_deleted"
)

var placeholderPattern = regexp.MustCompile(`\{\{([a-zA-Z_]+)\}\}`)

// DefaultBrandName 为 {{brand}} 兜底，避免缺占位符导致发信失败。
const DefaultBrandName = "POWER·PUSH"

// RenderTemplate 填充模板；缺占位符报错，禁止发出 {{code}}。
func RenderTemplate(name TemplateName, data map[string]string) (string, error) {
	raw, err := templateFiles.ReadFile("templates/" + string(name) + ".html")
	if err != nil {
		return "", fmt.Errorf("load mail template %q: %w", name, err)
	}
	if _, ok := data["brand"]; !ok {
		branded := make(map[string]string, len(data)+1)
		maps.Copy(branded, data)
		branded["brand"] = DefaultBrandName
		data = branded
	}
	var missing []string
	rendered := placeholderPattern.ReplaceAllStringFunc(string(raw), func(match string) string {
		key := match[2 : len(match)-2]
		value, ok := data[key]
		if !ok {
			missing = append(missing, key)
			return match
		}
		return htmlEscape(value)
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("mail template %q is missing values for: %s", name, strings.Join(unique(missing), ", "))
	}
	return rendered, nil
}

func unique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
