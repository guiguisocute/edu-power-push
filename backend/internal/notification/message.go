package notification

import (
	"strings"
	"time"
)

// Meter 为结构化电表与宿舍事实，供各渠道共用。
type Meter struct {
	Number   string `json:"number"`
	Campus   string `json:"campus,omitempty"`
	Building string `json:"building"`
	Floor    string `json:"floor"`
	Room     string `json:"room"`
}

// Data 为事件用电事实。空字段从通用 Webhook 省略。
type Data struct {
	BalanceYuan   string `json:"balance_yuan,omitempty"`
	ThresholdYuan string `json:"threshold_yuan,omitempty"`
	UsageKWH      string `json:"usage_kwh,omitempty"`
	Period        string `json:"period,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
}

// Message 为规范通知契约。Body 序列化为 message 字段。
type Message struct {
	Event  string    `json:"event"`
	Title  string    `json:"title"`
	Body   string    `json:"message"`
	SentAt time.Time `json:"sent_at"`
	Meter  Meter     `json:"meter"`
	Data   Data      `json:"data"`
}

func LowBalance(sentAt time.Time, meter Meter, balance, threshold string) Message {
	return Message{
		Event:  "low_balance_alert",
		Title:  "低额度预警",
		SentAt: sentAt,
		Meter:  meter,
		Data:   Data{BalanceYuan: balance, ThresholdYuan: threshold},
		Body: strings.Join([]string{
			"当前余额：" + balance + " 元（提醒阈值：" + threshold + " 元）",
			"电表号：" + display(meter.Number),
			"宿舍楼栋：" + display(meter.Building),
			"楼层：" + display(meter.Floor),
			"房间号：" + display(meter.Room),
			"请及时充值。余额不足会影响用电。",
		}, "\n"),
	}
}

func Digest(sentAt time.Time, meter Meter, balance, usage, period string) Message {
	return Message{
		Event:  "scheduled_digest",
		Title:  "用电摘要",
		SentAt: sentAt,
		Meter:  meter,
		Data:   Data{BalanceYuan: balance, UsageKWH: usage, Period: period},
		Body: strings.Join([]string{
			"统计周期：" + period,
			"本周期用电：" + usage + " 度",
			"当前余额：" + balance + " 元",
			"电表号：" + display(meter.Number),
			"宿舍楼栋：" + display(meter.Building),
			"楼层：" + display(meter.Floor),
			"房间号：" + display(meter.Room),
		}, "\n"),
	}
}

func Test(sentAt time.Time, meter Meter, balance, updatedAt string) Message {
	return Message{
		Event:  "test",
		Title:  "POWER·PUSH 测试",
		SentAt: sentAt,
		Meter:  meter,
		Data:   Data{BalanceYuan: balance, UpdatedAt: updatedAt},
		Body: strings.Join([]string{
			"剩余电费：" + display(balance),
			"数据更新时间：" + display(updatedAt),
			"电表号：" + display(meter.Number),
			"宿舍楼栋：" + display(meter.Building),
			"楼层：" + display(meter.Floor),
			"房间号：" + display(meter.Room),
			"",
			"请核对以上信息是否与当前绑定一致。",
			"此消息由已登录用户主动触发。",
		}, "\n"),
	}
}

func display(value string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return "暂无"
}
