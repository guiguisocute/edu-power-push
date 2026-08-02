package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	authn "github.com/edu-power-push/edu-power-push/backend/internal/auth"
	"github.com/edu-power-push/edu-power-push/backend/internal/storage"
)

/* 账号设置端点：改昵称、改密码、换绑邮箱。
   换绑手机号尚未提供。前端按 features.auth.sms_login 隐藏入口。 */

/*
requirePasswordSet 拒绝无密码账号。

	OAuth 注册账号的密码列为空。直接比对会误报密码错误。
	本函数返回明确错误。用户必须先走找回密码设置密码。
	注销与换邮箱仍要求密码。未锁屏手机不得等同处置权。
*/
func (s *Server) requirePasswordSet(w http.ResponseWriter, r *http.Request, user storage.AuthUser) bool {
	if user.HasPassword {
		return true
	}
	s.writeError(w, r, http.StatusConflict, "password_not_set",
		"this account has no password yet; set one through password recovery first")
	return false
}

func (s *Server) updateCurrentUserProfile(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(userIDKey).(string)
	var request struct {
		Nickname *string `json:"nickname"`
	}
	if !s.decodeJSON(w, r, &request) {
		return
	}
	if request.Nickname == nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_request", "nickname is required")
		return
	}
	nickname := strings.TrimSpace(*request.Nickname)
	if len(nickname) > 80 {
		s.writeError(w, r, http.StatusBadRequest, "invalid_nickname", "nickname must not exceed 80 characters")
		return
	}
	if nickname == "" {
		// 空昵称时使用邮箱 @ 前本地部分。
		user, err := storage.GetAuthUser(r.Context(), s.pool, userID)
		if err != nil {
			s.databaseError(w, r, err)
			return
		}
		nickname = strings.SplitN(user.Email, "@", 2)[0]
	}
	saved, err := storage.UpdateNickname(r.Context(), s.pool, userID, nickname)
	if errors.Is(err, storage.ErrAuthUserNotFound) {
		s.writeError(w, r, http.StatusUnauthorized, "unauthorized", "user is unavailable")
		return
	}
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.rankingCache.invalidate()
	s.writeJSON(w, http.StatusOK, saved)
}

// 验证当前密码后更新密码，并吊销全部会话（含本机）。
// 客户端必须退出到登录页。
func (s *Server) changeCurrentUserPassword(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(userIDKey).(string)
	var request struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !s.decodeJSON(w, r, &request) {
		return
	}
	full, err := storage.GetAuthUserWithPassword(r.Context(), s.pool, userID)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	if !s.requirePasswordSet(w, r, full) {
		return
	}
	if full.Status != "active" || !authn.VerifyPassword(request.CurrentPassword, full.PasswordHash) {
		s.writeError(w, r, http.StatusUnauthorized, "invalid_credentials", "current password is incorrect")
		return
	}
	newHash, err := authn.HashPassword(request.NewPassword)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_password", err.Error())
		return
	}
	if err := storage.UpdatePassword(r.Context(), s.pool, userID, newHash); err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.clearRefreshCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

/*
revokeCurrentUserSessions 吊销当前用户全部会话。

	含本机。无需改密码即可收回会话。
	用于怀疑账号被他人登录时止血。
*/
func (s *Server) revokeCurrentUserSessions(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(userIDKey).(string)
	revoked, err := storage.RevokeAllUserSessions(r.Context(), s.pool, userID)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.clearRefreshCookie(w)
	s.writeJSON(w, http.StatusOK, map[string]any{"revoked": revoked})
}

/*
deleteCurrentUser 注销当前账号。

	必须验证当前密码。仅有未过期 access token 不够。
	删除后清除 refresh cookie。客户端回到未登录态。
*/
func (s *Server) deleteCurrentUser(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(userIDKey).(string)
	var request struct {
		Password string `json:"password"`
	}
	if !s.decodeJSON(w, r, &request) {
		return
	}
	full, err := storage.GetAuthUserWithPassword(r.Context(), s.pool, userID)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	if !s.requirePasswordSet(w, r, full) {
		return
	}
	if !authn.VerifyPassword(request.Password, full.PasswordHash) {
		s.writeError(w, r, http.StatusUnauthorized, "invalid_credentials", "password is incorrect")
		return
	}
	err = storage.DeleteAuthUser(r.Context(), s.pool, userID)
	switch {
	case errors.Is(err, storage.ErrAuthUserNotFound):
		s.writeError(w, r, http.StatusUnauthorized, "unauthorized", "user is unavailable")
		return
	case errors.Is(err, storage.ErrLastAdmin):
		s.writeError(w, r, http.StatusConflict, "last_admin",
			"the last admin account cannot be deleted; grant admin to someone else first")
		return
	case err != nil:
		s.databaseError(w, r, err)
		return
	}
	s.logger.Info("account deleted", "request_id", r.Context().Value(requestIDKey), "user_id", userID)
	s.rankingCache.invalidate()
	requestID, _ := r.Context().Value(requestIDKey).(string)
	s.notifyAccountDeleted(requestID, full.Email)
	s.clearRefreshCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func userMeterLocation(meter *storage.UserMeter) string {
	if meter == nil {
		return "未记录宿舍位置"
	}
	parts := make([]string, 0, 4)
	for _, value := range []string{meter.Campus, meter.Building, meter.Floor, meter.Room} {
		if value = strings.TrimSpace(value); value != "" {
			parts = append(parts, value)
		}
	}
	if len(parts) == 0 {
		return "未记录宿舍位置"
	}
	return strings.Join(parts, " · ")
}

func (s *Server) accountEventTime(at time.Time) string {
	location := s.cfg.App.Timezone
	if location == nil {
		location = time.Local
	}
	return at.In(location).Format("2006-01-02 15:04")
}

// 生命周期邮件仅作事后通知。邮件不可用时禁止阻断解绑、禁用或注销。
func (s *Server) enqueueAccountEventMail(requestID, event string, send func(context.Context) error) {
	if s.mailer == nil {
		return
	}
	go func() {
		base := s.ctx
		if base == nil {
			base = context.Background()
		}
		ctx, cancel := context.WithTimeout(base, 30*time.Second)
		defer cancel()
		if !s.mailReady(ctx) {
			return
		}
		if err := send(ctx); err != nil {
			s.logger.Error("send account event mail", "event", event, "request_id", requestID, "error", err)
		}
	}()
}

func (s *Server) notifyMeterUnbound(requestID, recipient, meter, location string) {
	unboundAt := s.accountEventTime(time.Now())
	s.enqueueAccountEventMail(requestID, "meter_unbound", func(ctx context.Context) error {
		_, err := s.mailer.SendMeterUnbound(ctx, recipient, meter, location, unboundAt)
		return err
	})
}

func (s *Server) notifyAccountDisabled(requestID, recipient, reason string) {
	disabledAt := s.accountEventTime(time.Now())
	s.enqueueAccountEventMail(requestID, "account_disabled", func(ctx context.Context) error {
		_, err := s.mailer.SendAccountDisabled(ctx, recipient, disabledAt, reason)
		return err
	})
}

func (s *Server) notifyAccountDeleted(requestID, recipient string) {
	deletedAt := s.accountEventTime(time.Now())
	s.enqueueAccountEventMail(requestID, "account_deleted", func(ctx context.Context) error {
		_, err := s.mailer.SendAccountDeleted(ctx, recipient, deletedAt)
		return err
	})
}

// 向新邮箱发送验证码。目标不是当前邮箱。
func (s *Server) requestEmailChangeCode(w http.ResponseWriter, r *http.Request) {
	if !s.mailReady(r.Context()) {
		s.writeError(w, r, http.StatusServiceUnavailable, "mail_not_configured",
			"changing email requires a configured mail provider")
		return
	}
	userID, _ := r.Context().Value(userIDKey).(string)
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
	current, err := storage.GetAuthUser(r.Context(), s.pool, userID)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	if email == current.Email {
		s.writeError(w, r, http.StatusBadRequest, "same_email", "new email is the same as the current one")
		return
	}
	// 邮箱已被占用时直接返回 409。禁止白发邮件。
	if _, err := storage.FindAuthUserByEmail(r.Context(), s.pool, email); err == nil {
		s.writeError(w, r, http.StatusConflict, "email_exists", "email is already registered")
		return
	} else if !errors.Is(err, storage.ErrAuthUserNotFound) {
		s.databaseError(w, r, err)
		return
	}
	code, hash, err := newVerificationCode()
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "code_error", "could not generate a verification code")
		return
	}
	wait, err := storage.IssueEmailCodeWithQuota(
		r.Context(), s.pool, email, "email_change", hash, emailCodeTTL,
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
	if _, err := s.mailer.SendVerificationCode(r.Context(), email, code, int(emailCodeTTL.Minutes())); err != nil {
		s.logger.Error("send email-change code", "request_id", r.Context().Value(requestIDKey), "error", err)
		s.writeError(w, r, http.StatusBadGateway, "mail_delivery_failed", "could not send the verification code")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// 必须提供当前密码与新邮箱验证码。
// 成功后更新邮箱并保留会话。access token 仍有效。
func (s *Server) changeCurrentUserEmail(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(userIDKey).(string)
	var request struct {
		Email    string `json:"email"`
		Code     string `json:"code"`
		Password string `json:"password"`
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
	full, err := storage.GetAuthUserWithPassword(r.Context(), s.pool, userID)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	if !s.requirePasswordSet(w, r, full) {
		return
	}
	if full.Status != "active" || !authn.VerifyPassword(request.Password, full.PasswordHash) {
		s.writeError(w, r, http.StatusUnauthorized, "invalid_credentials", "password is incorrect")
		return
	}
	err = storage.ConsumeEmailCode(r.Context(), s.pool, email, "email_change", authn.TokenHash(code))
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
	saved, err := storage.UpdateEmail(r.Context(), s.pool, userID, email)
	if errors.Is(err, storage.ErrEmailExists) {
		s.writeError(w, r, http.StatusConflict, "email_exists", "email is already registered")
		return
	}
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, saved)
}
