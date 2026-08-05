package httpapi

/*
第三方登录（Google / GitHub）端点。

	浏览器走整页导航，不是 fetch。失败时禁止返回 JSON。
	一律 302 回站点，原因放在 ?oauth_error=。前端再转为用户可读文案。

	回调仅下发 refresh cookie，然后重定向回站点。
	SPA 再 POST /auth/refresh 换取 access token。
	access token 禁止放入 URL。
*/

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	authn "github.com/edu-power-push/edu-power-push/backend/internal/auth"
	"github.com/edu-power-push/edu-power-push/backend/internal/oauth"
	"github.com/edu-power-push/edu-power-push/backend/internal/storage"
)

const (
	oauthStateCookie = "edu_oauth"
	oauthStateTTL    = 10 * time.Minute
)

// oauthFlow 存在 state cookie 中。HttpOnly。JS 读不到也改不了。
type oauthFlow struct {
	Provider string `json:"p"`
	State    string `json:"s"`
	Verifier string `json:"v"`
	Next     string `json:"n"`
}

/*
oauthProviderClient 按路径取提供方。凭证取面板与 .env 合并结果。

	同时返回 Effective。回调地址必须由同一份配置计算。
	授权与换令牌使用不同 redirect_uri 会被提供方拒绝。
*/
func (s *Server) oauthProviderClient(ctx context.Context, provider string) (*oauth.Client, oauth.Effective, error) {
	effective, err := s.oauthSettings.Effective(ctx)
	if err != nil {
		return nil, oauth.Effective{}, err
	}
	client, err := effective.Client(provider)
	if err != nil {
		return nil, effective, err
	}
	return client, effective, nil
}

/*
startOAuth 把用户送去提供方。

	state 与 PKCE verifier 存入短命 cookie。
	state 同时在 cookie 与 URL 中。回来时两边必须一致。
	该设计阻止登录 CSRF。
*/
func (s *Server) startOAuth(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	next := safeReturnPath(r.URL.Query().Get("next"))
	if s.tokens == nil || s.pool == nil {
		s.redirectOAuthError(w, r, next, "unavailable", errors.New("user authentication is not configured"))
		return
	}
	client, effective, err := s.oauthProviderClient(r.Context(), provider)
	if err != nil {
		s.redirectOAuthError(w, r, next, "unavailable", err)
		return
	}
	enabled, err := storage.OAuthProviderEnabled(r.Context(), s.pool, provider)
	if err != nil {
		s.redirectOAuthError(w, r, next, "unavailable", err)
		return
	}
	if !enabled {
		s.redirectOAuthError(w, r, next, "disabled_entry", fmt.Errorf("oauth provider %q is switched off in the admin panel", provider))
		return
	}
	state, err := oauth.RandomToken()
	if err != nil {
		s.redirectOAuthError(w, r, next, "unavailable", err)
		return
	}
	verifier, err := oauth.RandomToken()
	if err != nil {
		s.redirectOAuthError(w, r, next, "unavailable", err)
		return
	}
	s.setOAuthStateCookie(w, oauthFlow{Provider: provider, State: state, Verifier: verifier, Next: next})
	// 授权页禁止被任何一层缓存。
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, client.AuthorizeURL(effective.RedirectURI(provider), state, verifier), http.StatusFound)
}

/*
completeOAuth 处理提供方回调。

	验证 state，换令牌，换身份，创建账号与会话。
	然后下发 refresh cookie 并返回站点。
*/
func (s *Server) completeOAuth(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	flow, ok := s.readOAuthStateCookie(r)
	s.clearOAuthStateCookie(w)
	next := "/"
	if ok {
		next = safeReturnPath(flow.Next)
	}
	if s.tokens == nil || s.pool == nil {
		s.redirectOAuthError(w, r, next, "unavailable", errors.New("user authentication is not configured"))
		return
	}
	// 用户取消授权，或提供方拒绝本次授权。
	if providerError := strings.TrimSpace(r.URL.Query().Get("error")); providerError != "" {
		code := "denied"
		if providerError != "access_denied" {
			code = "exchange"
		}
		s.redirectOAuthError(w, r, next, code, fmt.Errorf("provider returned %q", providerError))
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if !ok || flow.Provider != provider || flow.State == "" || !secureEqual(flow.State, state) || code == "" {
		// state 不匹配则终止。本次回调不是本站发出的那一次。
		s.redirectOAuthError(w, r, next, "state", errors.New("oauth state did not match the stored flow"))
		return
	}
	client, effective, err := s.oauthProviderClient(r.Context(), provider)
	if err != nil {
		s.redirectOAuthError(w, r, next, "unavailable", err)
		return
	}
	profile, err := client.Exchange(r.Context(), effective.RedirectURI(provider), code, flow.Verifier)
	if errors.Is(err, oauth.ErrEmailUnavailable) {
		s.redirectOAuthError(w, r, next, "email_missing", err)
		return
	}
	if err != nil {
		s.redirectOAuthError(w, r, next, "exchange", err)
		return
	}
	registrationEnabled, err := storage.RegistrationEnabled(r.Context(), s.pool)
	if err != nil {
		s.redirectOAuthError(w, r, next, "unavailable", err)
		return
	}
	userID := authn.NewID()
	outcome, err := storage.LinkOrCreateOAuthUser(r.Context(), s.pool, storage.OAuthProfile{
		Provider:      profile.Provider,
		Subject:       profile.Subject,
		Email:         profile.Email,
		EmailVerified: profile.EmailVerified,
		Nickname:      oauthNickname(profile),
	}, userID, registrationEnabled)
	switch {
	case errors.Is(err, storage.ErrOAuthEmailUnverified):
		s.redirectOAuthError(w, r, next, "email_unverified", err)
		return
	case errors.Is(err, storage.ErrOAuthEmailMissing):
		s.redirectOAuthError(w, r, next, "email_missing", err)
		return
	case errors.Is(err, storage.ErrOAuthRegistrationDisabled):
		s.redirectOAuthError(w, r, next, "registration_disabled", err)
		return
	case errors.Is(err, storage.ErrOAuthAlreadyLinked), errors.Is(err, storage.ErrEmailExists):
		s.redirectOAuthError(w, r, next, "conflict", err)
		return
	case err != nil:
		s.redirectOAuthError(w, r, next, "unavailable", err)
		return
	}
	if outcome.User.Status != "active" {
		s.redirectOAuthError(w, r, next, "account_disabled", errors.New("account is disabled"))
		return
	}
	// 必须等身份查找结束后再签发会话。首次注册时 outcome.User.ID 等于
	// 上面的候选 userID；再次登录或关联已有账号时则不同。若提前用候选 ID
	// 签发，refresh session 会引用不存在的用户并触发外键错误。
	refresh, session, err := s.newOAuthSession(outcome, r.UserAgent())
	if err != nil {
		s.redirectOAuthError(w, r, next, "unavailable", err)
		return
	}
	if err := storage.CreateRefreshSession(r.Context(), s.pool, session); err != nil {
		s.redirectOAuthError(w, r, next, "unavailable", err)
		return
	}
	s.setRefreshCookie(w, refresh)
	s.logger.Info("oauth login",
		"request_id", r.Context().Value(requestIDKey),
		"provider", provider, "user_id", outcome.User.ID,
		"created", outcome.Created, "linked", outcome.Linked,
	)
	if outcome.Created {
		s.rankingCache.invalidate()
	}
	result := "signed_in"
	switch {
	case outcome.Created:
		result = "created"
	case outcome.Linked:
		result = "linked"
	}
	s.redirectOAuthResult(w, r, next, url.Values{"oauth": {provider}, "oauth_result": {result}})
}

// newOAuthSession 只接受已经解析完成的登录结果，确保 JWT subject、
// refresh session 外键与实际登录账号始终是同一个 ID。
func (s *Server) newOAuthSession(outcome storage.OAuthLoginOutcome, userAgent string) (string, storage.RefreshSession, error) {
	_, refresh, session, err := s.newTokenSet(outcome.User.ID, "", userAgent)
	return refresh, session, err
}

// oauthNickname 返回落库展示名。提供方无名字时使用邮箱本地部分。
// 与邮箱注册路径一致。禁止因入口不同得到空名字。
func oauthNickname(profile oauth.Profile) string {
	nickname := strings.TrimSpace(profile.Nickname)
	if len(nickname) > 80 {
		nickname = strings.TrimSpace(string([]rune(nickname)[:80]))
	}
	if nickname != "" {
		return nickname
	}
	return strings.SplitN(profile.Email, "@", 2)[0]
}

func (s *Server) setOAuthStateCookie(w http.ResponseWriter, flow oauthFlow) {
	payload, err := json.Marshal(flow)
	if err != nil {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:   oauthStateCookie,
		Value:  base64.RawURLEncoding.EncodeToString(payload),
		Path:   "/api/v1/auth/oauth",
		MaxAge: int(oauthStateTTL.Seconds()),
		// 使用 Lax 而非 Strict。该 cookie 必须在从提供方回跳的顶层导航中送回。
		// Strict 会挡掉它，导致每次登录 state 不匹配。
		HttpOnly: true,
		Secure:   s.cfg.Auth.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) readOAuthStateCookie(r *http.Request) (oauthFlow, bool) {
	cookie, err := r.Cookie(oauthStateCookie)
	if err != nil || cookie.Value == "" {
		return oauthFlow{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil {
		return oauthFlow{}, false
	}
	var flow oauthFlow
	if err := json.Unmarshal(raw, &flow); err != nil {
		return oauthFlow{}, false
	}
	return flow, true
}

func (s *Server) clearOAuthStateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: oauthStateCookie, Value: "", Path: "/api/v1/auth/oauth",
		MaxAge: -1, Expires: time.Unix(1, 0), HttpOnly: true,
		Secure: s.cfg.Auth.CookieSecure, SameSite: http.SameSiteLaxMode,
	})
}

/*
redirectOAuthError 把失败原因交给前端展示。

	原因码供用户分类查看。真实错误仅写日志。
	提供方报错常含 client_id 与回调地址，禁止出现在地址栏。
*/
func (s *Server) redirectOAuthError(w http.ResponseWriter, r *http.Request, next, code string, cause error) {
	s.logger.Warn("oauth login failed",
		"request_id", r.Context().Value(requestIDKey),
		"provider", r.PathValue("provider"), "reason", code, "error", cause,
	)
	s.redirectOAuthResult(w, r, next, url.Values{"oauth_error": {code}})
}

func (s *Server) redirectOAuthResult(w http.ResponseWriter, r *http.Request, next string, params url.Values) {
	target := safeReturnPath(next)
	separator := "?"
	if strings.Contains(target, "?") {
		separator = "&"
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, target+separator+params.Encode(), http.StatusFound)
}

func secureEqual(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

/*
safeReturnPath 仅接受本站相对路径。

	原样重定向 ?next= 会造成开放重定向。
	必须以单个 / 开头。禁止协议或反斜杠。
*/
func safeReturnPath(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" || !strings.HasPrefix(value, "/") {
		return "/"
	}
	if strings.HasPrefix(value, "//") || strings.Contains(value, "\\") || strings.Contains(value, "://") {
		return "/"
	}
	if len(value) > 200 {
		return "/"
	}
	return value
}
