package notification

import (
	"strings"
	"testing"
	"time"
)

func TestApplyTemplatesUsesStructuredNotificationData(t *testing.T) {
	message := LowBalance(time.Now(), Meter{
		Number: "31240718", Building: "12栋", Floor: "4楼", Room: "402",
	}, "8.50", "10.00")
	templates := DefaultTemplates()
	templates.LowBalance = MessageTemplate{
		Title: "{{.building}} {{.room}} 余额提醒",
		Body:  "余额 {{.balance_yuan}} 元，阈值 {{.threshold_yuan}} 元；电表 {{.meter_number}}。",
	}

	got, err := ApplyTemplates(message, templates)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "12栋 402 余额提醒" {
		t.Fatalf("title = %q", got.Title)
	}
	if got.Body != "余额 8.50 元，阈值 10.00 元；电表 31240718。" {
		t.Fatalf("body = %q", got.Body)
	}
	if got.Event != message.Event || got.Meter != message.Meter || got.Data != message.Data {
		t.Fatal("template rendering changed structured notification fields")
	}
}

func TestDefaultTemplatesPreserveCanonicalMessages(t *testing.T) {
	meter := Meter{Number: "31240718", Building: "12栋", Floor: "4楼", Room: "402"}
	messages := []Message{
		LowBalance(time.Now(), meter, "8.50", "10.00"),
		Digest(time.Now(), meter, "8.50", "3.20", "07-25 至 07-31"),
	}
	for _, message := range messages {
		got, err := ApplyTemplates(message, DefaultTemplates())
		if err != nil {
			t.Fatalf("%s: %v", message.Event, err)
		}
		if got.Title != message.Title || got.Body != message.Body {
			t.Fatalf("%s default changed message:\nwant %q / %q\ngot  %q / %q", message.Event, message.Title, message.Body, got.Title, got.Body)
		}
	}
}

func TestApplyTemplatesLeavesTestMessagesCanonical(t *testing.T) {
	message := Test(time.Now(), Meter{Number: "31240718"}, "8.50", "2026-07-31 08:00")
	templates := DefaultTemplates()
	templates.LowBalance.Title = "custom"
	got, err := ApplyTemplates(message, templates)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != message.Title || got.Body != message.Body {
		t.Fatal("test message unexpectedly used a business-event template")
	}
}

func TestValidateTemplatesRejectsUnknownVariablesAndOversizedSources(t *testing.T) {
	templates := DefaultTemplates()
	templates.Digest.Body = "{{.not_a_supported_variable}}"
	if err := ValidateTemplates(templates); err == nil || !strings.Contains(err.Error(), "not_a_supported_variable") {
		t.Fatalf("unknown variable error = %v", err)
	}

	templates = DefaultTemplates()
	templates.LowBalance.Body = strings.Repeat("x", maxBodyTemplateRunes+1)
	if err := ValidateTemplates(templates); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized template error = %v", err)
	}
}

func TestNormalizeTemplatesRestoresBlankFields(t *testing.T) {
	got := NormalizeTemplates(Templates{})
	defaults := DefaultTemplates()
	if got != defaults {
		t.Fatalf("normalized templates = %#v", got)
	}
}
