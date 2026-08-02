package notification

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
	"time"
	"unicode/utf8"
)

const (
	maxTitleTemplateRunes = 200
	maxBodyTemplateRunes  = 4000
	maxRenderedTitleRunes = 200
	maxRenderedBodyRunes  = 10000
)

// MessageTemplate 控制非邮件渠道的标题与正文。
type MessageTemplate struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type Templates struct {
	LowBalance MessageTemplate `json:"low_balance"`
	Digest     MessageTemplate `json:"digest"`
}

func DefaultTemplates() Templates {
	return Templates{
		LowBalance: MessageTemplate{
			Title: "低额度预警",
			Body: strings.Join([]string{
				"当前余额：{{.balance_yuan}} 元（提醒阈值：{{.threshold_yuan}} 元）",
				"电表号：{{.meter_number}}",
				"宿舍楼栋：{{.building}}",
				"楼层：{{.floor}}",
				"房间号：{{.room}}",
				"请及时充值。余额不足会影响用电。",
			}, "\n"),
		},
		Digest: MessageTemplate{
			Title: "用电摘要",
			Body: strings.Join([]string{
				"统计周期：{{.period}}",
				"本周期用电：{{.usage_kwh}} 度",
				"当前余额：{{.balance_yuan}} 元",
				"电表号：{{.meter_number}}",
				"宿舍楼栋：{{.building}}",
				"楼层：{{.floor}}",
				"房间号：{{.room}}",
			}, "\n"),
		},
	}
}

// NormalizeTemplates 将空字段回落产品默认，避免存空通知。
func NormalizeTemplates(in Templates) Templates {
	defaults := DefaultTemplates()
	if strings.TrimSpace(in.LowBalance.Title) == "" {
		in.LowBalance.Title = defaults.LowBalance.Title
	}
	if strings.TrimSpace(in.LowBalance.Body) == "" {
		in.LowBalance.Body = defaults.LowBalance.Body
	}
	if strings.TrimSpace(in.Digest.Title) == "" {
		in.Digest.Title = defaults.Digest.Title
	}
	if strings.TrimSpace(in.Digest.Body) == "" {
		in.Digest.Body = defaults.Digest.Body
	}
	return in
}

// ValidateTemplates 解析并执行模板，落库前拒绝语法与超长错误。
func ValidateTemplates(in Templates) error {
	in = NormalizeTemplates(in)
	meter := Meter{Number: "31240718", Campus: "示例校区", Building: "12栋", Floor: "4楼", Room: "402"}
	samples := []Message{
		LowBalance(time.Now(), meter, "8.50", "10.00"),
		Digest(time.Now(), meter, "8.50", "3.20", "07-25 至 07-31"),
	}
	for _, message := range samples {
		if _, err := ApplyTemplates(message, in); err != nil {
			return err
		}
	}
	return nil
}

// ApplyTemplates 用用户模板渲染标题与正文。
// 无显式模板的事件类型保持规范文案。
func ApplyTemplates(message Message, in Templates) (Message, error) {
	in = NormalizeTemplates(in)
	var selected MessageTemplate
	switch message.Event {
	case "low_balance_alert":
		selected = in.LowBalance
	case "scheduled_digest":
		selected = in.Digest
	default:
		return message, nil
	}

	data := templateData(message)
	title, err := renderTemplate("title", selected.Title, maxTitleTemplateRunes, maxRenderedTitleRunes, data)
	if err != nil {
		return message, err
	}
	body, err := renderTemplate("body", selected.Body, maxBodyTemplateRunes, maxRenderedBodyRunes, data)
	if err != nil {
		return message, err
	}
	message.Title, message.Body = title, body
	return message, nil
}

func renderTemplate(name, source string, maxSource, maxOutput int, data map[string]string) (string, error) {
	if utf8.RuneCountInString(source) > maxSource {
		return "", fmt.Errorf("%s template exceeds %d characters", name, maxSource)
	}
	parsed, err := template.New(name).Option("missingkey=error").Parse(source)
	if err != nil {
		return "", fmt.Errorf("parse %s template: %w", name, err)
	}
	var out bytes.Buffer
	if err := parsed.Execute(&out, data); err != nil {
		return "", fmt.Errorf("render %s template: %w", name, err)
	}
	rendered := strings.TrimSpace(out.String())
	if rendered == "" {
		return "", fmt.Errorf("%s template renders an empty value", name)
	}
	if utf8.RuneCountInString(rendered) > maxOutput {
		return "", fmt.Errorf("rendered %s exceeds %d characters", name, maxOutput)
	}
	return rendered, nil
}

func templateData(message Message) map[string]string {
	return map[string]string{
		"event":          message.Event,
		"title":          message.Title,
		"message":        message.Body,
		"sent_at":        message.SentAt.Format(time.RFC3339),
		"meter_number":   display(message.Meter.Number),
		"campus":         display(message.Meter.Campus),
		"building":       display(message.Meter.Building),
		"floor":          display(message.Meter.Floor),
		"room":           display(message.Meter.Room),
		"balance_yuan":   display(message.Data.BalanceYuan),
		"threshold_yuan": display(message.Data.ThresholdYuan),
		"usage_kwh":      display(message.Data.UsageKWH),
		"period":         display(message.Data.Period),
		"updated_at":     display(message.Data.UpdatedAt),
	}
}
