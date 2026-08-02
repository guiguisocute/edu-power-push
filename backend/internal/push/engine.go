package push

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/mailer"
	"github.com/edu-power-push/edu-power-push/backend/internal/notification"
	"github.com/edu-power-push/edu-power-push/backend/internal/secrets"
	"github.com/edu-power-push/edu-power-push/backend/internal/storage"
	"github.com/jackc/pgx/v5/pgxpool"
)

/* 推送引擎：按用户规则与最新余额投递个人通知。
   触发：全量扫描结束；worker 每分钟评估摘要。
   各渠道共用触发去重状态。 */

type Mailer interface {
	SendBalanceAlert(ctx context.Context, recipient string, message notification.Message) (mailer.Delivery, error)
	SendUsageSummary(ctx context.Context, recipient string, message notification.Message) (mailer.Delivery, error)
}

type Engine struct {
	pool        *pgxpool.Pool
	mailer      Mailer
	channels    Deliverer
	credentials *secrets.Box
	logger      *slog.Logger
	now         func() time.Time
	// running 防并发：两触发点重叠会重复发摘要。
	running atomic.Bool
}

func New(pool *pgxpool.Pool, m Mailer, credentials *secrets.Box, logger *slog.Logger) *Engine {
	if logger == nil {
		logger = slog.Default()
	}
	return &Engine{
		pool: pool, mailer: m, channels: NewChannelSender(nil), credentials: credentials,
		logger: logger, now: time.Now,
	}
}

// Run 评估绑表用户的低额预警与到期摘要。
func (e *Engine) Run(ctx context.Context) {
	if e == nil || e.pool == nil {
		return
	}
	// 上一轮未结束则跳过，禁止重复发信。
	if !e.running.CompareAndSwap(false, true) {
		e.logger.Warn("push engine run skipped: previous run still in flight")
		return
	}
	defer e.running.Store(false)
	candidates, err := storage.ListPushCandidates(ctx, e.pool, e.credentials)
	if err != nil {
		e.logger.Error("list push candidates", "error", err)
		return
	}
	now := e.now()
	for _, c := range candidates {
		if ctx.Err() != nil {
			return
		}
		recipients := storage.ResolveMailRecipients(c)
		if len(recipients) == 0 && len(c.PersonalChannels) == 0 {
			continue
		}
		e.evaluateLowBalance(ctx, c, recipients, now)
		e.evaluateDigest(ctx, c, recipients, now)
	}
}

func (e *Engine) evaluateLowBalance(ctx context.Context, c storage.PushCandidate, recipients []string, now time.Time) {
	if !c.LowBalanceAlert {
		// 关闭预警时解除 latch，避免再开立刻重发。
		if c.AlertLatched {
			_ = storage.MarkAlertLatched(ctx, e.pool, c.UserID, false)
		}
		return
	}
	balance, err := strconv.ParseFloat(c.BalanceYuan, 64)
	if err != nil {
		return
	}
	threshold, err := strconv.ParseFloat(c.ThresholdYuan, 64)
	if err != nil {
		return
	}
	// 回升超过阈值+缓冲则解除 latch。
	if c.AlertLatched && balance >= threshold+storage.LowBalanceAlertBufferYuan {
		if err := storage.MarkAlertLatched(ctx, e.pool, c.UserID, false); err != nil {
			e.logger.Error("clear alert latch", "user_id", c.UserID, "error", err)
		}
		return
	}
	if balance >= threshold || c.AlertLatched {
		return
	}
	// 跌破阈值且未 latch 则推送一次。
	balanceText := formatYuan(c.BalanceYuan)
	thresholdText := formatYuan(c.ThresholdYuan)
	message := notification.LowBalance(now, notification.Meter{
		Number: c.MeterNo, Building: c.Building, Floor: c.Floor, Room: c.Room,
	}, balanceText, thresholdText)
	summary := fmt.Sprintf("低额度预警 · 余额 %s 元已低于 %s 元阈值", balanceText, thresholdText)
	okAny, mailOK := false, false
	for _, to := range recipients {
		if e.mailer == nil {
			break
		}
		_, err := e.mailer.SendBalanceAlert(ctx, to, message)
		status := "delivered"
		var errMsg *string
		if err != nil {
			status = "failed"
			msg := friendlyMailError(err)
			errMsg = &msg
			e.logger.Error("send balance alert", "user_id", c.UserID, "to", maskEmail(to), "error", err)
		} else {
			okAny, mailOK = true, true
		}
		_ = storage.InsertPushLog(ctx, e.pool, c.UserID, "mail", "low_balance", status, summary, errMsg)
	}
	if e.sendPersonalChannels(ctx, c, "low_balance", summary, message) {
		okAny = true
	}
	if okAny {
		if err := storage.MarkAlertLatched(ctx, e.pool, c.UserID, true); err != nil {
			e.logger.Error("set alert latch", "user_id", c.UserID, "error", err)
		}
		if mailOK {
			_ = storage.RecordChannelResult(ctx, e.pool, c.UserID, "mail", "ok", nil)
		}
	}
	if !mailOK && len(recipients) > 0 && e.mailer != nil {
		msg := "低额度预警发送失败"
		_ = storage.RecordChannelResult(ctx, e.pool, c.UserID, "mail", "failed", &msg)
	}
	_ = now // 保留签名一致，便于以后加冷却
}

func (e *Engine) evaluateDigest(ctx context.Context, c storage.PushCandidate, recipients []string, now time.Time) {
	if !c.ScheduledDigest {
		return
	}
	loc := loadLocation(c.Timezone)
	local := now.In(loc)
	if !digestDue(c, local) {
		return
	}
	balanceText := formatYuan(c.BalanceYuan)
	usageText := "—"
	if c.Recent7DKWH != nil && *c.Recent7DKWH != "" {
		usageText = formatKWH(*c.Recent7DKWH)
	}
	periodText := digestPeriodLabel(c.Period, local)
	message := notification.Digest(now, notification.Meter{
		Number: c.MeterNo, Building: c.Building, Floor: c.Floor, Room: c.Room,
	}, balanceText, usageText, periodText)
	summary := fmt.Sprintf("用电摘要 · %s 用电 %s 度，余额 %s 元", periodText, usageText, balanceText)
	okAny, mailOK := false, false
	for _, to := range recipients {
		if e.mailer == nil {
			break
		}
		_, err := e.mailer.SendUsageSummary(ctx, to, message)
		status := "delivered"
		var errMsg *string
		if err != nil {
			status = "failed"
			msg := friendlyMailError(err)
			errMsg = &msg
			e.logger.Error("send usage summary", "user_id", c.UserID, "to", maskEmail(to), "error", err)
		} else {
			okAny, mailOK = true, true
		}
		_ = storage.InsertPushLog(ctx, e.pool, c.UserID, "mail", "digest", status, summary, errMsg)
	}
	if e.sendPersonalChannels(ctx, c, "digest", summary, message) {
		okAny = true
	}
	if okAny {
		if err := storage.MarkDigestSent(ctx, e.pool, c.UserID); err != nil {
			e.logger.Error("mark digest sent", "user_id", c.UserID, "error", err)
		}
		if mailOK {
			_ = storage.RecordChannelResult(ctx, e.pool, c.UserID, "mail", "ok", nil)
		}
	}
	if !mailOK && len(recipients) > 0 && e.mailer != nil {
		msg := "用电摘要发送失败"
		_ = storage.RecordChannelResult(ctx, e.pool, c.UserID, "mail", "failed", &msg)
	}
}

func (e *Engine) sendPersonalChannels(
	ctx context.Context, c storage.PushCandidate, kind, summary string, message Message,
) bool {
	if e.channels == nil {
		return false
	}
	if rendered, err := notification.ApplyTemplates(message, c.Templates); err != nil {
		e.logger.Warn("render personal push template", "user_id", c.UserID, "event", message.Event, "error", err)
	} else {
		message = rendered
	}
	okAny := false
	for _, target := range c.PersonalChannels {
		delivery, err := e.channels.Send(ctx, target.Channel, target.Config, message)
		status := "delivered"
		resultStatus := "ok"
		var resultMessage, logErr *string
		if err != nil {
			status, resultStatus = "failed", "failed"
			msg := storage.TruncateText(err.Error(), 160)
			resultMessage, logErr = &msg, &msg
			e.logger.Error("send personal push", "user_id", c.UserID, "channel", target.Channel, "error", err)
		} else {
			okAny = true
			if delivery.Async {
				status = "accepted"
				msg := delivery.Note
				resultMessage = &msg
			}
		}
		_ = storage.RecordChannelResult(ctx, e.pool, c.UserID, target.Channel, resultStatus, resultMessage)
		_ = storage.InsertPushLog(ctx, e.pool, c.UserID, target.Channel, kind, status, summary, logErr)
	}
	return okAny
}

// digestDue：本地 push_time 起 15 分钟窗口内可发。
// 周期相对 last_digest_at：daily / twice(+12h) / every3 / weekly。
func digestDue(c storage.PushCandidate, local time.Time) bool {
	hour, min, ok := parseHHMM(c.PushTime)
	if !ok {
		return false
	}
	// 候选：主推送点；twice 另加 +12h。
	slots := []time.Time{time.Date(local.Year(), local.Month(), local.Day(), hour, min, 0, 0, local.Location())}
	if c.Period == "twice" {
		slots = append(slots, slots[0].Add(12*time.Hour))
	}
	inWindow := false
	var slotTime time.Time
	for _, slot := range slots {
		// 跨午夜的 +12h 归一到最近 24h 内。
		for slot.After(local) {
			slot = slot.Add(-24 * time.Hour)
		}
		if d := local.Sub(slot); d >= 0 && d < 15*time.Minute {
			inWindow = true
			slotTime = slot
			break
		}
	}
	if !inWindow {
		return false
	}
	if c.LastDigestAt == nil {
		return true
	}
	last := c.LastDigestAt.In(local.Location())
	// 同一推送窗口内不重复。
	if !last.Before(slotTime) {
		return false
	}
	switch c.Period {
	case "daily", "twice":
		return local.Sub(last) >= 10*time.Hour // twice 间隔 12h，阈值取 10h
	case "every3":
		return local.Sub(last) >= 70*time.Hour // every3 下限 70 小时
	case "weekly":
		return local.Sub(last) >= 6*24*time.Hour
	default:
		return false
	}
}

func parseHHMM(value string) (hour, min int, ok bool) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return 0, 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

func loadLocation(name string) *time.Location {
	if name == "" {
		name = "Asia/Shanghai"
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}

func formatYuan(raw string) string {
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return raw
	}
	return strconv.FormatFloat(v, 'f', 2, 64)
}

func formatKWH(raw string) string {
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return raw
	}
	return strconv.FormatFloat(v, 'f', 2, 64)
}

func digestPeriodLabel(period string, local time.Time) string {
	switch period {
	case "twice":
		return local.Format("01-02 15:04") + " 半日摘要"
	case "every3":
		start := local.AddDate(0, 0, -3)
		return start.Format("01-02") + " 至 " + local.Format("01-02")
	case "weekly":
		start := local.AddDate(0, 0, -7)
		return start.Format("01-02") + " 至 " + local.Format("01-02")
	default:
		return local.Format("01-02") + " 日摘要"
	}
}

func friendlyMailError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "rate") || strings.Contains(msg, "429"):
		return "发送过于频繁，请稍后再试"
	case strings.Contains(msg, "not configured"):
		return "邮件服务未配置"
	default:
		return storage.TruncateText(msg, 120)
	}
}

func maskEmail(address string) string {
	parts := strings.SplitN(address, "@", 2)
	if len(parts) != 2 || parts[0] == "" {
		return "***"
	}
	return string([]rune(parts[0])[0]) + "***@" + parts[1]
}
