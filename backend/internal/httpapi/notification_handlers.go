package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	authn "github.com/edu-power-push/edu-power-push/backend/internal/auth"
	"github.com/edu-power-push/edu-power-push/backend/internal/notification"
	"github.com/edu-power-push/edu-power-push/backend/internal/push"
	"github.com/edu-power-push/edu-power-push/backend/internal/secrets"
	"github.com/edu-power-push/edu-power-push/backend/internal/storage"
)

func (s *Server) requestMailRecipientCode(w http.ResponseWriter, r *http.Request) {
	if !s.mailReady(r.Context()) {
		s.writeError(w, r, http.StatusServiceUnavailable, "mail_not_configured", "verifying a recipient requires a configured mail provider")
		return
	}
	var request struct {
		Email string `json:"email"`
	}
	if !s.decodeJSON(w, r, &request) {
		return
	}
	email, ok := normalizeEmail(request.Email)
	if !ok {
		s.writeError(w, r, http.StatusBadRequest, "invalid_email", "email address is invalid")
		return
	}
	code, hash, err := newVerificationCode()
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "code_error", "could not generate a verification code")
		return
	}
	wait, err := storage.IssueEmailCodeWithQuota(
		r.Context(), s.pool, email, "notification_recipient", hash, emailCodeTTL,
		s.cfg.Mail.UserMinuteLimit, s.cfg.Mail.UserDayLimit,
	)
	if errors.Is(err, storage.ErrEmailCodeTooSoon) {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		s.writeError(w, r, http.StatusTooManyRequests, "rate_limited", "a verification code was already sent recently")
		return
	}
	if errors.Is(err, storage.ErrMailQuotaExceeded) {
		w.Header().Set("Retry-After", "60")
		s.writeError(w, r, http.StatusTooManyRequests, "mail_quota_exceeded", "verification email capacity is temporarily exhausted")
		return
	}
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	if _, err := s.mailer.SendNotificationRecipientVerificationCode(r.Context(), email, code, int(emailCodeTTL.Minutes())); err != nil {
		s.logger.Error("send notification-recipient code", "request_id", r.Context().Value(requestIDKey), "error", err)
		s.writeError(w, r, http.StatusBadGateway, "mail_delivery_failed", "could not send the verification code")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) verifyMailRecipient(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(userIDKey).(string)
	var request struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if !s.decodeJSON(w, r, &request) {
		return
	}
	email, ok := normalizeEmail(request.Email)
	if !ok {
		s.writeError(w, r, http.StatusBadRequest, "invalid_email", "email address is invalid")
		return
	}
	code := strings.TrimSpace(request.Code)
	if !verificationCodePattern.MatchString(code) {
		s.writeError(w, r, http.StatusBadRequest, "invalid_code", "verification code must contain 6 digits")
		return
	}
	err := storage.ConsumeEmailCode(r.Context(), s.pool, email, "notification_recipient", authn.TokenHash(code))
	switch {
	case errors.Is(err, storage.ErrEmailCodeInvalid):
		s.writeError(w, r, http.StatusBadRequest, "invalid_code", "verification code is invalid or expired")
		return
	case errors.Is(err, storage.ErrEmailCodeAttempts):
		s.writeError(w, r, http.StatusTooManyRequests, "code_attempts_exceeded", "too many failed attempts; request a new code")
		return
	case err != nil:
		s.databaseError(w, r, err)
		return
	}
	if err := storage.AddVerifiedNotificationEmail(r.Context(), s.pool, userID, email); err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"email": email})
}

/*
推送渠道与规则（frontend/docs/USER-PREFERENCES.md §1–3）。

	本文件负责写库与测试实发。
	定时与预警触发在 worker 的 push.Engine。
*/

func (s *Server) listCurrentUserChannels(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(userIDKey).(string)
	list, err := storage.ListNotificationChannels(r.Context(), s.pool, s.secrets, userID)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, list)
}

func (s *Server) replaceCurrentUserChannels(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(userIDKey).(string)
	var request struct {
		Channels []struct {
			Channel string         `json:"channel"`
			Enabled bool           `json:"enabled"`
			Config  map[string]any `json:"config"`
		} `json:"channels"`
	}
	if !s.decodeJSON(w, r, &request) {
		return
	}
	if request.Channels == nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_request", "channels is required")
		return
	}
	features, err := storage.GetFrontendConfig(r.Context(), s.pool)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	patches := make([]storage.ChannelPatch, 0, len(request.Channels))
	for _, item := range request.Channels {
		if blocked, reason := channelWriteBlocked(features, item.Channel); blocked {
			if !item.Enabled {
				continue
			}
			s.writeError(w, r, http.StatusForbidden, "channel_disabled", reason)
			return
		}
		patches = append(patches, storage.ChannelPatch{
			Channel: item.Channel, Enabled: item.Enabled, Config: item.Config,
		})
	}
	// 整批一个事务。任一条验证失败则整批回滚。禁止半更新状态。
	list, err := storage.ReplaceNotificationChannels(r.Context(), s.pool, s.secrets, userID, patches)
	if err != nil {
		s.writeChannelError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, list)
}

func (s *Server) updateCurrentUserChannel(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(userIDKey).(string)
	channel := r.PathValue("channel")
	features, err := storage.GetFrontendConfig(r.Context(), s.pool)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	if blocked, reason := channelWriteBlocked(features, channel); blocked {
		s.writeError(w, r, http.StatusForbidden, "channel_disabled", reason)
		return
	}
	var request struct {
		Enabled bool           `json:"enabled"`
		Config  map[string]any `json:"config"`
	}
	if !s.decodeJSON(w, r, &request) {
		return
	}
	if request.Config == nil {
		request.Config = map[string]any{}
	}
	saved, err := storage.SaveNotificationChannel(r.Context(), s.pool, s.secrets, userID, channel, request.Enabled, request.Config)
	if err != nil {
		s.writeChannelError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, saved)
}

func (s *Server) testCurrentUserChannel(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(userIDKey).(string)
	channel := r.PathValue("channel")
	features, err := storage.GetFrontendConfig(r.Context(), s.pool)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	if blocked, reason := channelWriteBlocked(features, channel); blocked {
		s.writeError(w, r, http.StatusForbidden, "channel_disabled", reason)
		return
	}
	user, err := storage.GetAuthUser(r.Context(), s.pool, userID)
	if err != nil || user.Status != "active" {
		s.writeError(w, r, http.StatusUnauthorized, "unauthorized", "user is unavailable")
		return
	}
	view, stored, err := storage.GetNotificationChannel(r.Context(), s.pool, s.secrets, userID, channel)
	if err != nil {
		s.writeChannelError(w, r, err)
		return
	}
	// 未配置过的 mail 允许用账号邮箱测一次。与引擎隐式默认一致。
	// 测试禁止顺手启用渠道。测试与正式投递开关是两件事。
	if channel == "mail" && !channelRowExists(view) {
		if stored == nil {
			stored = map[string]any{}
		}
		if _, has := stored["to"]; !has {
			stored["to"] = []string{user.Email}
		}
		view, err = storage.SaveNotificationChannel(r.Context(), s.pool, s.secrets, userID, "mail", false, stored)
		if err != nil {
			s.writeChannelError(w, r, err)
			return
		}
	}
	wait, err := storage.ReserveChannelTest(r.Context(), s.pool, userID, channel)
	if errors.Is(err, storage.ErrChannelRateLimited) {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		s.writeError(w, r, http.StatusTooManyRequests, "rate_limited", "channel test was already sent recently")
		return
	}
	if err != nil {
		s.writeChannelError(w, r, err)
		return
	}

	started := time.Now()
	result := storage.ChannelResult{Status: "ok", At: started}
	var summary string
	var logErr *string
	var overview *storage.MeterOverviewView
	if user.Meter != nil {
		if current, oerr := storage.GetMeterOverview(r.Context(), s.pool, user.Meter.Meter, time.Now()); oerr == nil {
			overview = &current
		}
	}
	testMessage := buildNotificationTestMessage(user, overview)
	testFacts := notificationTestFacts(user, overview)

	switch channel {
	case "mail":
		if !s.mailReady(r.Context()) {
			s.writeError(w, r, http.StatusServiceUnavailable, "mail_not_configured", "mail provider is not configured")
			return
		}
		recipients, perr := mailRecipientsFromConfig(stored, user.Email)
		if perr != nil || len(recipients) == 0 {
			s.writeError(w, r, http.StatusConflict, "channel_not_ready", "mail channel has no valid recipients")
			return
		}
		if err := storage.EnsureVerifiedMailRecipients(r.Context(), s.pool, userID, recipients); err != nil {
			s.writeChannelError(w, r, err)
			return
		}
		var lastErr error
		for _, to := range recipients {
			if _, err := s.mailer.SendPushTest(r.Context(), to, testFacts.Balance, testFacts.UpdatedAt, testFacts.Location, testFacts.Meter); err != nil {
				lastErr = err
			}
		}
		summary = notificationTestSummary(user, overview)
		if lastErr != nil {
			result.Status = "failed"
			msg := storage.TruncateText(lastErr.Error(), 200)
			result.Message = &msg
			logErr = &msg
		}
	case "telegram":
		summary = notificationTestSummary(user, overview)
		chatID, _ := stored["chat"].(string)
		if strings.TrimSpace(chatID) == "" {
			discoverer, ok := s.channels.(push.TelegramChatDiscoverer)
			if !ok {
				result.Status = "failed"
				msg := "Telegram 会话自动识别当前不可用"
				result.Message, logErr = &msg, &msg
				break
			}
			chatID, err = discoverer.DiscoverTelegramChat(r.Context(), stored)
			if err != nil {
				result.Status = "failed"
				msg := storage.TruncateText(err.Error(), 200)
				result.Message, logErr = &msg, &msg
				break
			}
			stored["chat"] = chatID
			if _, err = storage.SaveNotificationChannel(r.Context(), s.pool, s.secrets, userID, channel, view.Enabled, stored); err != nil {
				s.writeChannelError(w, r, err)
				return
			}
		}
		delivery, sendErr := s.channels.Send(r.Context(), channel, stored, testMessage)
		if sendErr != nil {
			result.Status = "failed"
			msg := storage.TruncateText(sendErr.Error(), 200)
			result.Message, logErr = &msg, &msg
		} else if delivery.Async {
			msg := delivery.Note
			result.Message = &msg
		}
	case "dingtalk", "wecom", "wecom_webhook", "discord", "feishu", "lark", "mp", "qq", "napcat", "pushplus", "whatsapp", "serverchan_turbo", "serverchan3", "webhook", "bark", "gotify":
		summary = notificationTestSummary(user, overview)
		delivery, sendErr := s.channels.Send(r.Context(), channel, stored, testMessage)
		if sendErr != nil {
			result.Status = "failed"
			msg := storage.TruncateText(sendErr.Error(), 200)
			result.Message = &msg
			logErr = &msg
		} else if delivery.Async {
			msg := delivery.Note
			result.Message = &msg
		}
	default:
		result.Status = "failed"
		msg := "该渠道投递尚未开通"
		result.Message, logErr = &msg, &msg
		summary = "测试推送 · " + channel + "（未开通）"
	}

	latency := time.Since(started).Milliseconds()
	result.LatencyMS = &latency
	result.At = time.Now()
	_ = storage.RecordChannelTestResult(r.Context(), s.pool, userID, channel, result.Status, result.Message)
	status := "delivered"
	if channel == "pushplus" && result.Status == "ok" {
		status = "accepted"
	}
	if result.Status != "ok" {
		status = "failed"
	}
	if summary != "" {
		_ = storage.InsertPushLog(r.Context(), s.pool, userID, channel, "test", status, summary, logErr)
	}
	// 契约：投递失败仍返回 200（已尝试）。仅服务端自身故障用 5xx。
	s.writeJSON(w, http.StatusOK, result)
}

type notificationTestFactSet struct {
	Balance   string
	UpdatedAt string
	Location  string
	Meter     string
}

func notificationTestFacts(user storage.AuthUser, overview *storage.MeterOverviewView) notificationTestFactSet {
	facts := notificationTestFactSet{
		Balance:   "暂无可用读数",
		UpdatedAt: "暂无可用读数",
		Meter:     "未绑定",
		Location:  "未绑定宿舍",
	}
	if user.Meter != nil {
		facts.Meter = user.Meter.Meter
		parts := make([]string, 0, 4)
		for _, value := range []string{user.Meter.Campus, user.Meter.Building, user.Meter.Floor, user.Meter.Room} {
			if value = strings.TrimSpace(value); value != "" {
				parts = append(parts, value)
			}
		}
		if len(parts) > 0 {
			facts.Location = strings.Join(parts, " · ")
		}
	}
	if overview != nil && overview.Latest != nil {
		if value := strings.TrimSpace(overview.Latest.TotalYuan); value != "" {
			facts.Balance = value + " 元"
		}
		zone, err := time.LoadLocation("Asia/Shanghai")
		if err != nil {
			zone = time.FixedZone("CST", 8*60*60)
		}
		at := overview.Latest.ObservedAt
		if at.IsZero() {
			at = overview.Latest.ReadingTime
		}
		if !at.IsZero() {
			facts.UpdatedAt = at.In(zone).Format("2006-01-02 15:04")
		}
	}
	return facts
}

func buildNotificationTestMessage(user storage.AuthUser, overview *storage.MeterOverviewView) push.Message {
	facts := notificationTestFacts(user, overview)
	// facts.Meter 中的「未绑定」是给人看的占位符。禁止写入 meter.number。
	// Webhook 消费者按字段解析，中文会被当成真电表号。
	// 未绑定时留空。与 building / floor / room 表示一致。
	meter := notification.Meter{}
	if user.Meter != nil {
		meter.Number = user.Meter.Meter
		meter.Campus = user.Meter.Campus
		meter.Building = user.Meter.Building
		meter.Floor = user.Meter.Floor
		meter.Room = user.Meter.Room
	}
	return notification.Test(time.Now(), meter, facts.Balance, facts.UpdatedAt)
}

func notificationTestSummary(user storage.AuthUser, overview *storage.MeterOverviewView) string {
	meter := "未绑定"
	balance := "暂无读数"
	if user.Meter != nil {
		meter = user.Meter.Meter
	}
	if overview != nil && overview.Latest != nil {
		if value := strings.TrimSpace(overview.Latest.TotalYuan); value != "" {
			balance = value + " 元"
		}
	}
	return "测试推送 · 余额 " + balance + " · 电表 " + meter
}

func (s *Server) getCurrentUserNotificationSettings(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(userIDKey).(string)
	settings, err := storage.GetNotificationSettings(r.Context(), s.pool, userID)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, settings)
}

func (s *Server) replaceCurrentUserNotificationSettings(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(userIDKey).(string)
	var request storage.NotificationSettings
	if !s.decodeJSON(w, r, &request) {
		return
	}
	if request.Timezone == "" {
		request.Timezone = "Asia/Shanghai"
	}
	saved, err := storage.SaveNotificationSettings(r.Context(), s.pool, userID, request)
	if err != nil {
		if errors.Is(err, storage.ErrInvalidNotifSettings) {
			s.writeError(w, r, http.StatusBadRequest, "invalid_settings", err.Error())
			return
		}
		s.databaseError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, saved)
}

func (s *Server) listCurrentUserPushLogs(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(userIDKey).(string)
	limit := 5
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxPushLogPageSize {
			s.writeError(w, r, http.StatusBadRequest, "invalid_limit",
				fmt.Sprintf("limit must be between 1 and %d", maxPushLogPageSize))
			return
		}
		limit = n
	}
	offset, err := cursorOffset(r)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_cursor", err.Error())
		return
	}
	// 多取一条仅用于判断是否还有下一页。nextCursor 会裁掉它。
	items, err := storage.ListPushLogs(r.Context(), s.pool, userID, limit+1, offset)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	next := nextCursor(&items, limit, offset)
	s.writeJSON(w, http.StatusOK, storage.PushLogPage{Items: items, NextCursor: next})
}

const maxPushLogPageSize = 50

func (s *Server) writeChannelError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, storage.ErrInvalidChannel):
		s.writeError(w, r, http.StatusBadRequest, "invalid_channel", "unsupported channel")
	case errors.Is(err, storage.ErrInvalidChannelConfig):
		s.writeError(w, r, http.StatusBadRequest, "invalid_config", err.Error())
	case errors.Is(err, storage.ErrUnverifiedMailRecipient):
		s.writeError(w, r, http.StatusConflict, "email_not_verified", "mail recipient must be verified before it can be added")
	case errors.Is(err, storage.ErrChannelDisabled):
		s.writeError(w, r, http.StatusConflict, "channel_not_ready", "channel is not configured or not enabled")
	case errors.Is(err, storage.ErrChannelUnsupported):
		s.writeError(w, r, http.StatusConflict, "channel_not_ready", "channel delivery is not implemented")
	case errors.Is(err, secrets.ErrNoKey), errors.Is(err, secrets.ErrWrongKey):
		s.writeError(w, r, http.StatusServiceUnavailable, "credential_encryption_unavailable", "channel credential encryption is not configured correctly")
	default:
		s.databaseError(w, r, err)
	}
}

func channelWriteBlocked(cfg storage.FrontendConfigView, channel string) (bool, string) {
	if !storage.IsKnownChannel(channel) {
		return true, "unsupported channel"
	}
	// features.channels 缺省或 true 表示允许。显式 false 表示运维关闭。
	if cfg.Features.Channels != nil {
		if on, ok := cfg.Features.Channels[channel]; ok && !on {
			return true, "this notification channel is disabled by the operator"
		}
	}
	if cfg.Features.ChannelComingSoon != nil && cfg.Features.ChannelComingSoon[channel] {
		return true, "this notification channel is marked coming soon by the operator"
	}
	if !storage.IsImplementedChannel(channel) {
		return true, "this notification channel is not implemented"
	}
	return false, ""
}

func channelRowExists(ch storage.NotificationChannel) bool {
	// GetNotificationChannel 对缺失行返回 Channel 有值但 UpdatedAt 为零。
	return !ch.UpdatedAt.IsZero()
}

func mailRecipientsFromConfig(stored map[string]any, fallback string) ([]string, error) {
	if stored == nil {
		if fallback != "" {
			return []string{fallback}, nil
		}
		return nil, storage.ErrChannelDisabled
	}
	raw, ok := stored["to"]
	if !ok || raw == nil {
		if fallback != "" {
			return []string{strings.ToLower(fallback)}, nil
		}
		return nil, storage.ErrChannelDisabled
	}
	switch v := raw.(type) {
	case []string:
		out := make([]string, 0, len(v))
		for _, s := range v {
			s = strings.ToLower(strings.TrimSpace(s))
			if s != "" {
				out = append(out, s)
			}
		}
		if len(out) == 0 && fallback != "" {
			return []string{strings.ToLower(fallback)}, nil
		}
		return out, nil
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				continue
			}
			s = strings.ToLower(strings.TrimSpace(s))
			if s != "" {
				out = append(out, s)
			}
		}
		if len(out) == 0 && fallback != "" {
			return []string{strings.ToLower(fallback)}, nil
		}
		return out, nil
	default:
		if fallback != "" {
			return []string{strings.ToLower(fallback)}, nil
		}
		return nil, storage.ErrChannelDisabled
	}
}
