package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	authn "github.com/edu-power-push/edu-power-push/backend/internal/auth"
	"github.com/edu-power-push/edu-power-push/backend/internal/captcha"
	"github.com/edu-power-push/edu-power-push/backend/internal/storage"
	"golang.org/x/time/rate"
)

var meterPattern = regexp.MustCompile(`^[0-9]{6,32}$`)

var trustedRateKeyPattern = regexp.MustCompile(`^[0-9A-Fa-f:]{3,160}$`)

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Nickname string `json:"nickname"`
	Code     string `json:"code"`
	Meter    string `json:"meter"`
}

var verificationCodePattern = regexp.MustCompile(`^[0-9]{6}$`)

// 验证码有效期。与邮件模板中「5 分钟内有效」一致。
const emailCodeTTL = 5 * time.Minute

/*
requestRegisterCode 发送注册验证码。

	注册必须验证邮箱归属。该邮箱用于推送与找回密码。
	邮件通道未配置时禁止注册。禁止免校验注册。
*/
func (s *Server) requestRegisterCode(w http.ResponseWriter, r *http.Request) {
	if !s.requireCaptcha(w, r, captcha.ActionRegisterCode) {
		return
	}
	if !s.requireUserAuthConfigured(w, r) {
		return
	}
	if !s.mailReady(r.Context()) {
		s.writeError(w, r, http.StatusServiceUnavailable, "mail_not_configured",
			"registration requires a configured mail provider")
		return
	}
	registrationEnabled, err := storage.RegistrationEnabled(r.Context(), s.pool)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	if !registrationEnabled {
		s.writeError(w, r, http.StatusForbidden, "registration_disabled", "new account registration is currently disabled")
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
	// 已注册邮箱在发码前直接拦截。禁止白发验证码邮件。
	// 最终注册仍保留唯一约束兜底。
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
		r.Context(), s.pool, email, "register", hash, emailCodeTTL,
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
		s.logger.Error("send registration code", "request_id", r.Context().Value(requestIDKey), "error", err)
		s.writeError(w, r, http.StatusBadGateway, "mail_delivery_failed", "could not send the verification code")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// newVerificationCode 返回明文（发信）与哈希（落库）。明文禁止落库。
func newVerificationCode() (string, []byte, error) {
	limit := big.NewInt(1000000)
	value, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return "", nil, err
	}
	code := fmt.Sprintf("%06d", value.Int64())
	return code, authn.TokenHash(code), nil
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type rebindMeterRequest struct {
	Meter string `json:"meter"`
}

type tokenResponse struct {
	AccessToken string           `json:"access_token"`
	TokenType   string           `json:"token_type"`
	ExpiresIn   int64            `json:"expires_in"`
	User        storage.AuthUser `json:"user"`
}

func (s *Server) registerUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireUserAuthConfigured(w, r) {
		return
	}
	// 无邮件通道则无法证明邮箱归属。禁止开放注册。
	if !s.mailReady(r.Context()) {
		s.writeError(w, r, http.StatusServiceUnavailable, "mail_not_configured",
			"registration requires a configured mail provider")
		return
	}
	registrationEnabled, err := storage.RegistrationEnabled(r.Context(), s.pool)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	if !registrationEnabled {
		s.writeError(w, r, http.StatusForbidden, "registration_disabled", "new account registration is currently disabled")
		return
	}
	var request registerRequest
	if !s.decodeJSON(w, r, &request) {
		return
	}
	email, ok := normalizeEmail(request.Email)
	if !ok {
		s.writeError(w, r, http.StatusBadRequest, "invalid_email", "email address is invalid")
		return
	}
	/* 邮箱验证码必须先验证。否则任何人可用他人邮箱建号。
	   验证放在密码哈希（Argon2id，故意很慢）之前。
	   否则错码会放大攻击成本。 */
	code := strings.TrimSpace(request.Code)
	if !verificationCodePattern.MatchString(code) {
		s.writeError(w, r, http.StatusBadRequest, "invalid_code", "verification code must contain 6 digits")
		return
	}
	err = storage.ConsumeEmailCode(r.Context(), s.pool, email, "register", authn.TokenHash(code))
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
	nickname := strings.TrimSpace(request.Nickname)
	if len(nickname) > 80 {
		s.writeError(w, r, http.StatusBadRequest, "invalid_nickname", "nickname must not exceed 80 characters")
		return
	}
	if nickname == "" {
		nickname = strings.SplitN(email, "@", 2)[0]
	}
	// 注册解耦绑表：省略 meter 则仅创建账号。
	// 电表在登录后于概览页绑定（frontend/docs/AUTH-GAPS.md §1）。
	// 仍接受携带 meter 的旧客户端。
	meter := strings.TrimSpace(request.Meter)
	if meter != "" {
		if !meterPattern.MatchString(meter) {
			s.writeError(w, r, http.StatusBadRequest, "invalid_meter", "meter must contain 6 to 32 digits")
			return
		}
		if !s.validateBindableMeter(w, r, meter, "") {
			return
		}
	}
	passwordHash, err := authn.HashPassword(request.Password)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_password", err.Error())
		return
	}
	userID := authn.NewID()
	tokens, refresh, session, err := s.newTokenSet(userID, "", r.UserAgent())
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "token_error", "could not create session")
		return
	}
	user, err := storage.CreateAuthUser(r.Context(), s.pool, userID, email, passwordHash, nickname, meter, session)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrEmailExists):
			s.writeError(w, r, http.StatusConflict, "email_exists", "email is already registered")
		case errors.Is(err, storage.ErrMeterNotEligible):
			s.writeError(w, r, http.StatusNotFound, "meter_not_found", "meter was not found")
		case errors.Is(err, storage.ErrMeterNotSupported):
			s.writeError(w, r, http.StatusConflict, "meter_not_supported", "meter building is not currently served by this platform")
		// 沿用 meter_already_bound。前端已有对应文案。
		// 独占与名额占满对用户同义：当前绑不上。
		case errors.Is(err, storage.ErrMeterAlreadyBound), errors.Is(err, storage.ErrMeterBindingFull):
			s.writeError(w, r, http.StatusConflict, "meter_already_bound",
				fmt.Sprintf("meter already has the maximum of %d bound accounts", storage.MaxMeterBinders))
		default:
			s.databaseError(w, r, err)
		}
		return
	}
	tokens.User = user
	s.setRefreshCookie(w, refresh)
	s.writeJSON(w, http.StatusCreated, tokens)
}

/*
requestPasswordReset 发送找回密码验证码。

	恒定返回 204。不区分邮箱是否注册，避免账号枚举。
	邮件通道未配置时返回 503。
*/
func (s *Server) requestPasswordReset(w http.ResponseWriter, r *http.Request) {
	if !s.requireCaptcha(w, r, captcha.ActionPasswordResetCode) {
		return
	}
	if !s.requireUserAuthConfigured(w, r) {
		return
	}
	if !s.mailReady(r.Context()) {
		s.writeError(w, r, http.StatusServiceUnavailable, "mail_not_configured",
			"password recovery requires a configured mail provider")
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
		// 格式错误返回 400。是否注册仍不泄露。
		s.writeError(w, r, http.StatusBadRequest, "invalid_email", "email address is invalid")
		return
	}
	// 仅对已注册且 active 的账号真正发码。其余静默 204。
	user, err := storage.FindAuthUserByEmail(r.Context(), s.pool, email)
	if err != nil && !errors.Is(err, storage.ErrAuthUserNotFound) {
		s.databaseError(w, r, err)
		return
	}
	if err == nil && user.Status == "active" {
		code, hash, genErr := newVerificationCode()
		if genErr != nil {
			s.logger.Error("generate password reset code", "request_id", r.Context().Value(requestIDKey), "error", genErr)
		} else {
			_, issueErr := storage.IssueEmailCodeWithQuota(
				r.Context(), s.pool, email, "password_reset", hash, emailCodeTTL,
				s.cfg.Mail.UserMinuteLimit, s.cfg.Mail.UserDayLimit,
			)
			switch {
			case errors.Is(issueErr, storage.ErrEmailCodeTooSoon):
				// 对外仍返回 204。禁止用重发状态枚举账号。
			case errors.Is(issueErr, storage.ErrMailQuotaExceeded):
				// 配额状态保持不透明。仅对已存在用户暴露会变成账号枚举源。
			case issueErr != nil:
				s.logger.Error("store password reset code", "request_id", r.Context().Value(requestIDKey), "error", issueErr)
			default:
				requestID, _ := r.Context().Value(requestIDKey).(string)
				go func() {
					base := s.ctx
					if base == nil {
						base = context.Background()
					}
					ctx, cancel := context.WithTimeout(base, 30*time.Second)
					defer cancel()
					if _, sendErr := s.mailer.SendPasswordReset(ctx, email, code, int(emailCodeTTL.Minutes())); sendErr != nil {
						s.logger.Error("send password reset code", "request_id", requestID, "error", sendErr)
					}
				}()
			}
		}
	}
	// 合法表单请求支付相同密码校验成本。发信在公开响应路径之后排队。
	// 账号是否存在不得反映在状态码或邮件延迟上。
	_ = authn.VerifyPassword("timing-padding", s.dummyPHC)
	w.WriteHeader(http.StatusNoContent)
}

// resetPassword 用邮箱验证码设置新密码，并吊销该用户全部会话。
func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request) {
	if !s.requireUserAuthConfigured(w, r) {
		return
	}
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
	// 先验证码再哈希密码。Argon2id 很慢。错码禁止变成放大攻击。
	err := storage.ConsumeEmailCode(r.Context(), s.pool, email, "password_reset", authn.TokenHash(code))
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
	passwordHash, err := authn.HashPassword(request.Password)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_password", err.Error())
		return
	}
	user, err := storage.FindAuthUserByEmail(r.Context(), s.pool, email)
	if errors.Is(err, storage.ErrAuthUserNotFound) || (err == nil && user.Status != "active") {
		// 码已消费。账号异常按成功返回，避免枚举。
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	if err := storage.UpdatePassword(r.Context(), s.pool, user.ID, passwordHash); err != nil {
		s.databaseError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) loginUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireCaptcha(w, r, captcha.ActionLogin) {
		return
	}
	if !s.requireUserAuthConfigured(w, r) {
		return
	}
	var request loginRequest
	if !s.decodeJSON(w, r, &request) {
		return
	}
	email, ok := normalizeEmail(request.Email)
	if !ok {
		s.rejectCredentials(w, r)
		return
	}
	user, err := storage.FindAuthUserByEmail(r.Context(), s.pool, email)
	if errors.Is(err, storage.ErrAuthUserNotFound) {
		_ = authn.VerifyPassword(request.Password, s.dummyPHC)
		s.rejectCredentials(w, r)
		return
	}
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	if user.Status != "active" || !authn.VerifyPassword(request.Password, user.PasswordHash) {
		s.rejectCredentials(w, r)
		return
	}
	tokens, refresh, session, err := s.newTokenSet(user.ID, "", r.UserAgent())
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "token_error", "could not create session")
		return
	}
	if err := storage.CreateRefreshSession(r.Context(), s.pool, session); err != nil {
		s.databaseError(w, r, err)
		return
	}
	if err := storage.MarkAuthLogin(r.Context(), s.pool, user.ID); err != nil {
		s.logger.Warn("record user login", "request_id", r.Context().Value(requestIDKey), "error", err)
	}
	user.PasswordHash = ""
	user.LastLoginAt = timePointer(time.Now().UTC())
	tokens.User = user
	s.setRefreshCookie(w, refresh)
	s.writeJSON(w, http.StatusOK, tokens)
}

func (s *Server) refreshUserToken(w http.ResponseWriter, r *http.Request) {
	requestStartedAt := time.Now()
	if !s.requireUserAuthConfigured(w, r) {
		return
	}
	cookie, err := r.Cookie(s.cfg.Auth.CookieName)
	if err != nil || cookie.Value == "" {
		s.writeError(w, r, http.StatusUnauthorized, "invalid_refresh_token", "refresh token is missing or invalid")
		return
	}
	claims, err := s.tokens.ParseRefresh(cookie.Value)
	if err != nil {
		s.clearRefreshCookie(w)
		s.writeError(w, r, http.StatusUnauthorized, "invalid_refresh_token", "refresh token is missing or invalid")
		return
	}
	tokens, refresh, next, err := s.newTokenSet(claims.Subject, claims.FamilyID, r.UserAgent())
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "token_error", "could not rotate session")
		return
	}
	err = storage.RotateRefreshSession(
		r.Context(), s.pool, authn.TokenHash(cookie.Value),
		claims.Subject, claims.FamilyID, claims.ID, requestStartedAt, next,
	)
	if err != nil {
		s.clearRefreshCookie(w)
		if errors.Is(err, storage.ErrRefreshReuse) {
			s.writeError(w, r, http.StatusUnauthorized, "refresh_reuse_detected", "refresh token reuse revoked this session")
			return
		}
		if errors.Is(err, storage.ErrRefreshInvalid) || errors.Is(err, storage.ErrRefreshExpired) {
			s.writeError(w, r, http.StatusUnauthorized, "invalid_refresh_token", "refresh token is missing or invalid")
			return
		}
		s.databaseError(w, r, err)
		return
	}
	user, err := storage.GetAuthUser(r.Context(), s.pool, claims.Subject)
	if err != nil || user.Status != "active" {
		s.clearRefreshCookie(w)
		s.writeError(w, r, http.StatusUnauthorized, "invalid_refresh_token", "refresh token is missing or invalid")
		return
	}
	tokens.User = user
	s.setRefreshCookie(w, refresh)
	s.writeJSON(w, http.StatusOK, tokens)
}

func (s *Server) logoutUser(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(s.cfg.Auth.CookieName); err == nil && cookie.Value != "" && s.pool != nil {
		if err := storage.RevokeRefreshFamily(r.Context(), s.pool, authn.TokenHash(cookie.Value), "logout"); err != nil {
			s.logger.Warn("revoke refresh session", "request_id", r.Context().Value(requestIDKey), "error", err)
		}
	}
	s.clearRefreshCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) currentUser(w http.ResponseWriter, r *http.Request) {
	user, ok := s.authenticatedUser(w, r)
	if !ok {
		return
	}
	s.writeJSON(w, http.StatusOK, user)
}

func (s *Server) currentUserMeterOverview(w http.ResponseWriter, r *http.Request) {
	user, ok := s.authenticatedUser(w, r)
	if !ok || !s.requireBoundMeter(w, r, user) {
		return
	}
	result, err := storage.GetMeterOverview(r.Context(), s.pool, user.Meter.Meter, time.Now())
	if err != nil {
		s.handleNotFound(w, r, err, "meter")
		return
	}
	s.writeJSON(w, http.StatusOK, result)
}

func (s *Server) currentUserMeterSeries(w http.ResponseWriter, r *http.Request) {
	user, ok := s.authenticatedUser(w, r)
	if !ok || !s.requireBoundMeter(w, r, user) {
		return
	}
	from, to, ok := s.timeRange(w, r)
	if !ok {
		return
	}
	metric := r.URL.Query().Get("metric")
	if metric != "balance" && metric != "consumption" {
		s.writeError(w, r, http.StatusBadRequest, "invalid_metric", "metric must be balance or consumption")
		return
	}
	result, err := storage.GetSeries(r.Context(), s.pool, user.Meter.Meter, metric, r.URL.Query().Get("granularity"), from, to, "", "")
	if err != nil {
		s.handleNotFound(w, r, err, "meter")
		return
	}
	s.writeJSON(w, http.StatusOK, result)
}

/*
currentUserMeterDayNight 返回窗口内日夜用电拆分。

	单独端点。数据来源与口径不同于 /me/series。
	series 的 consumption 走官方日用量。
	此处走扫描区间 consumption_deltas。能否拆分随结果返回。
*/
func (s *Server) currentUserMeterDayNight(w http.ResponseWriter, r *http.Request) {
	user, ok := s.authenticatedUser(w, r)
	if !ok || !s.requireBoundMeter(w, r, user) {
		return
	}
	from, to, ok := s.timeRange(w, r)
	if !ok {
		return
	}
	result, err := storage.GetDayNightSplit(r.Context(), s.pool, user.Meter.Meter, from, to, s.cfg.App.Timezone)
	if err != nil {
		s.handleNotFound(w, r, err, "meter")
		return
	}
	s.writeJSON(w, http.StatusOK, result)
}

func (s *Server) currentUserMeterBills(w http.ResponseWriter, r *http.Request) {
	user, ok := s.authenticatedUser(w, r)
	if !ok || !s.requireBoundMeter(w, r, user) {
		return
	}
	items, err := storage.ListMeterBills(r.Context(), s.pool, user.Meter.Meter, r.URL.Query().Get("from_month"), r.URL.Query().Get("to_month"))
	if err != nil {
		s.handleNotFound(w, r, err, "meter")
		return
	}
	s.writeJSON(w, http.StatusOK, items)
}

func (s *Server) rebindCurrentUserMeter(w http.ResponseWriter, r *http.Request) {
	var request rebindMeterRequest
	if !s.decodeJSON(w, r, &request) {
		return
	}
	meter := strings.TrimSpace(request.Meter)
	if !meterPattern.MatchString(meter) {
		s.writeError(w, r, http.StatusBadRequest, "invalid_meter", "meter must contain 6 to 32 digits")
		return
	}
	userID, _ := r.Context().Value(userIDKey).(string)
	if !s.validateBindableMeter(w, r, meter, userID) {
		return
	}
	user, err := storage.RebindUserMeter(r.Context(), s.pool, userID, meter)
	if errors.Is(err, storage.ErrMeterNotEligible) {
		s.writeError(w, r, http.StatusNotFound, "meter_not_found", "meter was not found")
		return
	}
	if errors.Is(err, storage.ErrMeterNotSupported) {
		s.writeError(w, r, http.StatusConflict, "meter_not_supported", "meter building is not currently served by this platform")
		return
	}
	if errors.Is(err, storage.ErrMeterAlreadyBound) || errors.Is(err, storage.ErrMeterBindingFull) {
		s.writeError(w, r, http.StatusConflict, "meter_already_bound",
			fmt.Sprintf("meter already has the maximum of %d bound accounts", storage.MaxMeterBinders))
		return
	}
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.rankingCache.invalidate()
	s.writeJSON(w, http.StatusOK, user)
}

// previewMeterForBinding 在确认绑定前供用户核对宿舍位置。
// 仅返回位置与可绑定状态。禁止返回余额或读数。
func (s *Server) previewMeterForBinding(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(userIDKey).(string)
	if !s.previewGate.allow(userID) {
		w.Header().Set("Retry-After", "3")
		requestID, _ := r.Context().Value(requestIDKey).(string)
		writeStandaloneError(w, http.StatusTooManyRequests, "rate_limited", "too many meter preview requests", requestID)
		return
	}
	if !s.requireCaptcha(w, r, captcha.ActionMeterPreview) {
		return
	}
	meter := strings.TrimSpace(r.URL.Query().Get("meter"))
	if !meterPattern.MatchString(meter) {
		s.writeError(w, r, http.StatusBadRequest, "invalid_meter", "meter must contain 6 to 32 digits")
		return
	}
	preview, err := storage.PreviewMeter(r.Context(), s.pool, meter, userID)
	if errors.Is(err, storage.ErrMeterNotEligible) {
		s.writeError(w, r, http.StatusNotFound, "meter_not_found", "meter was not found")
		return
	}
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, preview)
}

func (s *Server) validateBindableMeter(w http.ResponseWriter, r *http.Request, meter, exceptUserID string) bool {
	if err := storage.CheckMeterBindingAvailable(r.Context(), s.pool, meter, exceptUserID); err != nil {
		switch {
		case errors.Is(err, storage.ErrMeterNotEligible):
			s.writeError(w, r, http.StatusNotFound, "meter_not_found", "meter was not found")
		case errors.Is(err, storage.ErrMeterNotSupported):
			s.writeError(w, r, http.StatusConflict, "meter_not_supported", "meter building is not currently served by this platform")
		// 沿用 meter_already_bound。前端已有对应文案。
		// 独占与名额占满对用户同义：当前绑不上。
		case errors.Is(err, storage.ErrMeterAlreadyBound), errors.Is(err, storage.ErrMeterBindingFull):
			s.writeError(w, r, http.StatusConflict, "meter_already_bound",
				fmt.Sprintf("meter already has the maximum of %d bound accounts", storage.MaxMeterBinders))
		default:
			s.databaseError(w, r, err)
		}
		return false
	}
	return true
}

func (s *Server) authenticatedUser(w http.ResponseWriter, r *http.Request) (storage.AuthUser, bool) {
	userID, _ := r.Context().Value(userIDKey).(string)
	user, err := storage.GetAuthUser(r.Context(), s.pool, userID)
	if err != nil || user.Status != "active" {
		s.writeError(w, r, http.StatusUnauthorized, "unauthorized", "user is unavailable")
		return storage.AuthUser{}, false
	}
	return user, true
}

func (s *Server) requireBoundMeter(w http.ResponseWriter, r *http.Request, user storage.AuthUser) bool {
	if user.Meter == nil {
		s.writeError(w, r, http.StatusConflict, "meter_not_bound", "user has no active meter binding")
		return false
	}
	return true
}

/*
榜单参与与脱敏偏好。隐私开关读写必须落在服务端。

	仅存在浏览器中的开关等于没有开关。
*/
func (s *Server) currentUserLeaderboard(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(userIDKey).(string)
	pref, err := storage.GetLeaderboardPreference(r.Context(), s.pool, userID)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, pref)
}

func (s *Server) updateCurrentUserLeaderboard(w http.ResponseWriter, r *http.Request) {
	// 缺省取当前偏好而非零值。PUT 少写字段禁止静默清空展示设置。
	userID, _ := r.Context().Value(userIDKey).(string)
	current, err := storage.GetLeaderboardPreference(r.Context(), s.pool, userID)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	request := struct {
		OptedIn      *bool `json:"opted_in"`
		ShowBuilding *bool `json:"show_building"`
		ShowFloor    *bool `json:"show_floor"`
		ShowRoom     *bool `json:"show_room"`
		ShowNickname *bool `json:"show_nickname"`
		MaskBuilding *bool `json:"mask_building"`
		MaskFloor    *bool `json:"mask_floor"`
		MaskRoom     *bool `json:"mask_room"`
	}{}
	if !s.decodeJSON(w, r, &request) {
		return
	}
	apply := func(target *bool, value *bool) {
		if value != nil {
			*target = *value
		}
	}
	// opted_in 已强制 true。客户端改不了。
	apply(&current.ShowBuilding, request.ShowBuilding)
	apply(&current.ShowFloor, request.ShowFloor)
	apply(&current.ShowRoom, request.ShowRoom)
	apply(&current.ShowNickname, request.ShowNickname)
	apply(&current.MaskBuilding, request.MaskBuilding)
	apply(&current.MaskFloor, request.MaskFloor)
	apply(&current.MaskRoom, request.MaskRoom)
	current.OptedIn = true

	saved, err := storage.SaveLeaderboardPreference(r.Context(), s.pool, userID, current)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.rankingCache.invalidate()
	s.writeJSON(w, http.StatusOK, saved)
}

/*
optionalUserID 在匿名端点上识别本人。

	有效 access token 则返回用户 ID。否则当匿名。
	禁止返回 401。校园数据本身不要求登录。
*/
func (s *Server) optionalUserID(r *http.Request) string {
	if s.tokens == nil || s.pool == nil {
		return ""
	}
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return ""
	}
	claims, err := s.tokens.ParseAccess(strings.TrimPrefix(header, "Bearer "))
	if err != nil {
		return ""
	}
	active, err := storage.AccessSessionActive(r.Context(), s.pool, claims.Subject, claims.FamilyID)
	if err != nil || !active {
		return ""
	}
	return claims.Subject
}

func (s *Server) userAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.requireUserAuthConfigured(w, r) {
			return
		}
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			s.writeError(w, r, http.StatusUnauthorized, "unauthorized", "missing or invalid access token")
			return
		}
		claims, err := s.tokens.ParseAccess(strings.TrimPrefix(header, "Bearer "))
		if err != nil {
			s.writeError(w, r, http.StatusUnauthorized, "unauthorized", "missing or invalid access token")
			return
		}
		active, err := storage.AccessSessionActive(r.Context(), s.pool, claims.Subject, claims.FamilyID)
		if err != nil {
			s.databaseError(w, r, err)
			return
		}
		if !active {
			s.writeError(w, r, http.StatusUnauthorized, "unauthorized", "missing or invalid access token")
			return
		}
		ctx := context.WithValue(r.Context(), userIDKey, claims.Subject)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) newTokenSet(userID, familyID, userAgent string) (tokenResponse, string, storage.RefreshSession, error) {
	if familyID == "" {
		familyID = authn.NewID()
	}
	refreshID := authn.NewID()
	refreshJTI := authn.NewID()
	access, _, err := s.tokens.SignAccess(userID, familyID)
	if err != nil {
		return tokenResponse{}, "", storage.RefreshSession{}, err
	}
	refresh, refreshExpiry, err := s.tokens.SignRefresh(userID, familyID, refreshJTI)
	if err != nil {
		return tokenResponse{}, "", storage.RefreshSession{}, err
	}
	return tokenResponse{
			AccessToken: access,
			TokenType:   "Bearer",
			ExpiresIn:   int64(s.tokens.AccessTTL().Seconds()),
		}, refresh, storage.RefreshSession{
			ID: refreshID, FamilyID: familyID, UserID: userID, JTI: refreshJTI,
			TokenHash: authn.TokenHash(refresh), ExpiresAt: refreshExpiry,
			UserAgentHash: authn.UserAgentHash(userAgent),
		}, nil
}

func (s *Server) setRefreshCookie(w http.ResponseWriter, value string) {
	http.SetCookie(w, &http.Cookie{
		Name: s.cfg.Auth.CookieName, Value: value, Path: "/api/v1/auth",
		MaxAge: int(s.tokens.RefreshTTL().Seconds()), HttpOnly: true,
		Secure: s.cfg.Auth.CookieSecure, SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) clearRefreshCookie(w http.ResponseWriter) {
	name := s.cfg.Auth.CookieName
	if name == "" {
		name = "edu_power_refresh"
	}
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: "/api/v1/auth", MaxAge: -1,
		Expires: time.Unix(1, 0), HttpOnly: true,
		Secure: s.cfg.Auth.CookieSecure, SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) requireUserAuthConfigured(w http.ResponseWriter, r *http.Request) bool {
	if s.tokens == nil || s.pool == nil {
		s.writeError(w, r, http.StatusServiceUnavailable, "auth_unavailable", "user authentication is not configured")
		return false
	}
	return true
}

func (s *Server) rejectCredentials(w http.ResponseWriter, r *http.Request) {
	s.writeError(w, r, http.StatusUnauthorized, "invalid_credentials", "email or password is incorrect")
}

func normalizeEmail(raw string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if len(normalized) < 3 || len(normalized) > 254 {
		return "", false
	}
	parsed, err := mail.ParseAddress(normalized)
	if err != nil || strings.ToLower(parsed.Address) != normalized {
		return "", false
	}
	return normalized, true
}

func timePointer(value time.Time) *time.Time { return &value }

type ipRateLimiter struct {
	mu          sync.Mutex
	clients     map[string]*ipRateEntry
	hits        int
	every       time.Duration
	burst       int
	global      *rate.Limiter
	proxySecret string
}

type ipRateEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func newIPRateLimiter(proxySecret string) *ipRateLimiter {
	limiter := newRateLimiter(2*time.Second, 10)
	// HTTP 看不到原始 TCP 源地址。按连接分键，禁止无关用户共用桶。
	// 全局粗上限仍约束重连绕过与昂贵密码哈希。
	limiter.global = rate.NewLimiter(10, 50)
	limiter.proxySecret = proxySecret
	return limiter
}

func newExpensiveAuthRateLimiter(proxySecret string) *ipRateLimiter {
	limiter := newRateLimiter(2*time.Second, 2)
	limiter.global = rate.NewLimiter(2, 2)
	limiter.proxySecret = proxySecret
	return limiter
}

func newCampusRateLimiter(proxySecret string) *ipRateLimiter {
	limiter := newRateLimiter(250*time.Millisecond, 20)
	limiter.global = rate.NewLimiter(50, 100)
	limiter.proxySecret = proxySecret
	return limiter
}

// newRateLimiter 的键由调用方决定。
// 认证端点按可信连接或 IP。绑表预览按用户 ID。
func newRateLimiter(every time.Duration, burst int) *ipRateLimiter {
	return &ipRateLimiter{clients: make(map[string]*ipRateEntry), every: every, burst: burst}
}

func (l *ipRateLimiter) limit(next http.HandlerFunc) http.Handler {
	return l.limitWithMessage(next, "too many authentication requests")
}

func (l *ipRateLimiter) limitWithMessage(next http.HandlerFunc, message string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.allow(clientRateKey(r, l.proxySecret)) {
			w.Header().Set("Retry-After", "2")
			requestID, _ := r.Context().Value(requestIDKey).(string)
			writeStandaloneError(w, http.StatusTooManyRequests, "rate_limited", message, requestID)
			return
		}
		next(w, r)
	})
}

func (l *ipRateLimiter) allow(address string) bool {
	if l.global != nil && !l.global.Allow() {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	entry := l.clients[address]
	if entry == nil {
		entry = &ipRateEntry{limiter: rate.NewLimiter(rate.Every(l.every), l.burst)}
		l.clients[address] = entry
	}
	entry.lastSeen = now
	l.hits++
	if l.hits%1000 == 0 {
		for key, candidate := range l.clients {
			if now.Sub(candidate.lastSeen) > time.Hour {
				delete(l.clients, key)
			}
		}
	}
	return entry.limiter.Allow()
}

func clientRateKey(r *http.Request, proxySecret string) string {
	remote := r.RemoteAddr
	host, _, err := net.SplitHostPort(remote)
	if err == nil {
		remote = host
	}
	peer := net.ParseIP(remote)
	providedSecret := strings.TrimSpace(r.Header.Get("X-Rate-Proxy-Secret"))
	secretMatches := proxySecret != "" && len(providedSecret) == len(proxySecret) &&
		subtle.ConstantTimeCompare([]byte(providedSecret), []byte(proxySecret)) == 1
	if peer != nil && (peer.IsLoopback() || peer.IsPrivate()) && secretMatches {
		if trusted := strings.TrimSpace(r.Header.Get("X-Rate-Proxy-Key")); trustedRateKeyPattern.MatchString(trusted) {
			return "proxy:" + trusted
		}
	}
	return "ip:" + remote
}

func writeStandaloneError(w http.ResponseWriter, status int, code, message, requestID string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":{"code":"` + code + `","message":"` + message + `","request_id":"` + requestID + `"}}`))
}
