/*
Package oauth 实现 Google / GitHub 授权码登录，仅用标准库。
PKCE 仅用于 Google；GitHub OAuth App 不接受 code_challenge。
Google 走 userinfo，避免不验签读取 JWT。
身份主键为 Subject，禁止用可改邮箱。
*/
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	ProviderGoogle = "google"
	ProviderGitHub = "github"
)

// ErrProviderRejected 表示提供方拒绝交换。对用户仅提示登录失败，细节进日志。
var ErrProviderRejected = errors.New("oauth provider rejected the authorization")

// ErrEmailUnavailable 表示提供方无可用邮箱。
// GitHub 私有邮箱时 /user 与 /user/emails 可皆无 verified。
var ErrEmailUnavailable = errors.New("oauth provider returned no usable email")

// Profile 为提供方返回的身份事实。
type Profile struct {
	Provider      string
	Subject       string
	Email         string
	EmailVerified bool
	Nickname      string
}

// Endpoints 供测试指向 httptest；生产使用默认端点。
type Endpoints struct {
	Authorize string
	Token     string
	UserInfo  string
	// UserEmails 仅 GitHub 使用。
	UserEmails string
}

type Client struct {
	ClientID     string
	ClientSecret string
	Provider     string
	Endpoints    Endpoints
	HTTP         *http.Client
}

var googleEndpoints = Endpoints{
	Authorize: "https://accounts.google.com/o/oauth2/v2/auth",
	Token:     "https://oauth2.googleapis.com/token",
	UserInfo:  "https://openidconnect.googleapis.com/v1/userinfo",
}

var githubEndpoints = Endpoints{
	Authorize:  "https://github.com/login/oauth/authorize",
	Token:      "https://github.com/login/oauth/access_token",
	UserInfo:   "https://api.github.com/user",
	UserEmails: "https://api.github.com/user/emails",
}

func New(provider, clientID, clientSecret string) (*Client, error) {
	client := &Client{
		Provider:     provider,
		ClientID:     clientID,
		ClientSecret: clientSecret,
		// 回调页等待中，超时必须短。
		HTTP: &http.Client{Timeout: 12 * time.Second},
	}
	switch provider {
	case ProviderGoogle:
		client.Endpoints = googleEndpoints
	case ProviderGitHub:
		client.Endpoints = githubEndpoints
	default:
		return nil, fmt.Errorf("unknown oauth provider %q", provider)
	}
	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("oauth provider %q is missing credentials", provider)
	}
	return client, nil
}

// UsesPKCE：GitHub OAuth App 不支持 code_challenge。
func (c *Client) UsesPKCE() bool { return c.Provider == ProviderGoogle }

/*
AuthorizeURL 生成提供方授权地址。
Google 使用 prompt=select_account，避免共用设备串号。
*/
func (c *Client) AuthorizeURL(redirectURI, state, verifier string) string {
	params := url.Values{}
	params.Set("client_id", c.ClientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("state", state)
	switch c.Provider {
	case ProviderGoogle:
		params.Set("response_type", "code")
		params.Set("scope", "openid email profile")
		params.Set("access_type", "online")
		params.Set("prompt", "select_account")
		params.Set("code_challenge", PKCEChallenge(verifier))
		params.Set("code_challenge_method", "S256")
	case ProviderGitHub:
		params.Set("scope", "read:user user:email")
	}
	return c.Endpoints.Authorize + "?" + params.Encode()
}

// Exchange 用授权码换令牌并拉取身份。
func (c *Client) Exchange(ctx context.Context, redirectURI, code, verifier string) (Profile, error) {
	token, err := c.exchangeCode(ctx, redirectURI, code, verifier)
	if err != nil {
		return Profile{}, err
	}
	switch c.Provider {
	case ProviderGoogle:
		return c.googleProfile(ctx, token)
	case ProviderGitHub:
		return c.githubProfile(ctx, token)
	default:
		return Profile{}, fmt.Errorf("unknown oauth provider %q", c.Provider)
	}
}

func (c *Client) exchangeCode(ctx context.Context, redirectURI, code, verifier string) (string, error) {
	form := url.Values{}
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("grant_type", "authorization_code")
	if c.UsesPKCE() {
		form.Set("code_verifier", verifier)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoints.Token, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// 强制 JSON，避免 GitHub 默认 form-encoded。
	request.Header.Set("Accept", "application/json")
	body, err := c.do(request)
	if err != nil {
		return "", err
	}
	var payload struct {
		AccessToken      string `json:"access_token"`
		TokenType        string `json:"token_type"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("decode %s token response: %w", c.Provider, err)
	}
	// GitHub 可以 HTTP 200 在 body 中返回 error。
	if payload.Error != "" || payload.AccessToken == "" {
		return "", fmt.Errorf("%w: %s %s", ErrProviderRejected, payload.Error, payload.ErrorDescription)
	}
	return payload.AccessToken, nil
}

func (c *Client) googleProfile(ctx context.Context, token string) (Profile, error) {
	body, err := c.authorizedGet(ctx, c.Endpoints.UserInfo, token, "")
	if err != nil {
		return Profile{}, err
	}
	var payload struct {
		Sub           string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return Profile{}, fmt.Errorf("decode google userinfo: %w", err)
	}
	if payload.Sub == "" {
		return Profile{}, fmt.Errorf("%w: google userinfo has no subject", ErrProviderRejected)
	}
	if payload.Email == "" {
		return Profile{}, ErrEmailUnavailable
	}
	return Profile{
		Provider:      ProviderGoogle,
		Subject:       payload.Sub,
		Email:         strings.ToLower(strings.TrimSpace(payload.Email)),
		EmailVerified: payload.EmailVerified,
		Nickname:      strings.TrimSpace(payload.Name),
	}, nil
}

func (c *Client) githubProfile(ctx context.Context, token string) (Profile, error) {
	body, err := c.authorizedGet(ctx, c.Endpoints.UserInfo, token, "application/vnd.github+json")
	if err != nil {
		return Profile{}, err
	}
	var user struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := json.Unmarshal(body, &user); err != nil {
		return Profile{}, fmt.Errorf("decode github user: %w", err)
	}
	if user.ID == 0 {
		return Profile{}, fmt.Errorf("%w: github user has no id", ErrProviderRejected)
	}
	nickname := strings.TrimSpace(user.Name)
	if nickname == "" {
		nickname = user.Login
	}
	profile := Profile{
		Provider: ProviderGitHub,
		Subject:  strconv.FormatInt(user.ID, 10),
		Nickname: nickname,
	}
	/* 账号归属必须取 /user/emails 中 primary 且 verified 的地址。 */
	emails, err := c.githubEmails(ctx, token)
	if err != nil {
		return Profile{}, err
	}
	for _, candidate := range emails {
		if candidate.Primary && candidate.Verified {
			profile.Email = strings.ToLower(strings.TrimSpace(candidate.Email))
			profile.EmailVerified = true
			return profile, nil
		}
	}
	for _, candidate := range emails {
		if candidate.Verified {
			profile.Email = strings.ToLower(strings.TrimSpace(candidate.Email))
			profile.EmailVerified = true
			return profile, nil
		}
	}
	return Profile{}, ErrEmailUnavailable
}

type githubEmail struct {
	Email    string `json:"email"`
	Primary  bool   `json:"primary"`
	Verified bool   `json:"verified"`
}

func (c *Client) githubEmails(ctx context.Context, token string) ([]githubEmail, error) {
	body, err := c.authorizedGet(ctx, c.Endpoints.UserEmails, token, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	var emails []githubEmail
	if err := json.Unmarshal(body, &emails); err != nil {
		return nil, fmt.Errorf("decode github emails: %w", err)
	}
	return emails, nil
}

func (c *Client) authorizedGet(ctx context.Context, endpoint, token, accept string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if accept == "" {
		accept = "application/json"
	}
	request.Header.Set("Accept", accept)
	// GitHub 拒绝无 User-Agent 请求。
	request.Header.Set("User-Agent", "edu-power-push")
	return c.do(request)
}

// do 读取 body，上限 1 MiB，避免异常端点拖垮内存。
func (c *Client) do(request *http.Request) ([]byte, error) {
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 12 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("call %s: %w", c.Provider, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read %s response: %w", c.Provider, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: %s returned %d", ErrProviderRejected, c.Provider, response.StatusCode)
	}
	return body, nil
}

// RandomToken 生成 state 与 PKCE verifier（URL 安全、无填充）。
func RandomToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate oauth token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func PKCEChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
