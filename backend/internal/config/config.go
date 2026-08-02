package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	/* 多校部署禁止默认学校。
	   留空时采集器返回 school_not_configured。
	   须在面板或 .env 配置 AREA_ID。 */
	defaultAreaID   = ""
	defaultAreaName = ""

	defaultDatabaseMaxConns = 24
	/* 池连接必须预留给写结果、心跳与查询。
	   闸门占满池子时扫描器无法写结果。 */
	dbConnectionHeadroom = 8
)

// APP_ENV 决定安全默认值与启动校验。
// 非法取值必须启动失败。
// 错误拼写曾被当成非生产，导致 Secure cookie 静默关闭。
const (
	EnvDevelopment = "development"
	EnvTest        = "test"
	EnvProduction  = "production"
)

func normalizeEnvironment(raw string) (string, error) {
	environment := strings.ToLower(strings.TrimSpace(raw))
	switch environment {
	case EnvDevelopment, EnvTest, EnvProduction:
		return environment, nil
	case "":
		return EnvDevelopment, nil
	default:
		return "", fmt.Errorf("APP_ENV must be one of development, test, production (got %q)", raw)
	}
}

type Config struct {
	App      App
	Upstream Upstream
	Database Database
	HTTP     HTTP
	Auth     Auth
	OAuth    OAuth
	Captcha  Captcha
	Scan     Scan
	Bill     Bill
	Detail   Detail
	Mail     Mail
}

type App struct {
	Environment string
	Timezone    *time.Location
}

type Upstream struct {
	/* Provider 为 internal/provider 注册名。
	   仅注册一套时可留空。 */
	Provider string
	AreaID   string
	AreaName string
	/* BaseURL 为 nil 时由 Provider.DefaultBaseURL() 兜底。
	   config 不绑定具体上游地址。 */
	BaseURL *url.URL
	/* GlobalQPS / GlobalConcurrency 为闸门默认值，可被 system_settings 覆盖。
	   MaxQPS / MaxConcurrency 为面板不可越过的上限。
	   合并前面板误把默认当上限，只能改 .env 重启。 */
	GlobalQPS         float64
	GlobalConcurrency int
	MaxQPS            float64
	MaxConcurrency    int
}

type Database struct {
	URL string
	/* MaxConns 同时限制闸门在途请求数。
	   每个并发槽占用一条池连接。 */
	MaxConns int32
}

type HTTP struct {
	Address    string
	AdminToken string
	/* SettingsKey 加密 system_settings 凭证列（AES-256-GCM，32 字节）。
	   留空时面板禁止写入凭证，避免明文入库。 */
	SettingsKey string
	/* RateProxySecret 校验网关签发的连接限流密钥。
	   仅信任内网 peer 不够：客户端可经内网代理伪造桶。 */
	RateProxySecret string
}

type Auth struct {
	JWTSecret    string
	Issuer       string
	AccessTTL    time.Duration
	RefreshTTL   time.Duration
	CookieName   string
	CookieSecure bool
}

/*
OAuth 为 Google / GitHub 第三方登录。
未配 client id/secret 时按钮隐藏，端点返回 503。
BaseURL 生成回调地址，必须与提供方登记的重定向 URI 一致。
禁止从请求 Host 推导：反向代理后 Host 可伪造。
留空时退回 PUBLIC_BASE_URL。
*/
type OAuth struct {
	BaseURL string
	Google  OAuthProvider
	GitHub  OAuthProvider
}

type OAuthProvider struct {
	ClientID     string
	ClientSecret string
}

func (p OAuthProvider) Configured() bool {
	return p.ClientID != "" && p.ClientSecret != ""
}

func (o OAuth) Configured(provider string) bool {
	if o.BaseURL == "" {
		return false
	}
	switch provider {
	case "google":
		return o.Google.Configured()
	case "github":
		return o.GitHub.Configured()
	default:
		return false
	}
}

type Captcha struct {
	Provider           string
	SiteKey            string
	SecretKey          string
	Hostname           string
	Actions            []string
	TurnstileVerifyURL string
}

type Scan struct {
	Cron            string
	BoundCron       string
	Concurrency     int
	QPS             float64
	Timeout         time.Duration
	RetryMax        int
	RetryBaseDelay  time.Duration
	RetryMaxDelay   time.Duration
	StaleAfter      time.Duration
	ResultRetention time.Duration
	LogRetention    time.Duration
	MaxConcurrency  int
	MaxQPS          float64
}

type Bill struct {
	Cron           string
	Concurrency    int
	QPS            float64
	RetryMax       int
	MonthRetryMax  int
	MaxConcurrency int
	MaxQPS         float64
}

type Detail struct {
	Cron           string
	RetryCron      string
	BootstrapFrom  string
	AutoBootstrap  bool
	Concurrency    int
	QPS            float64
	RetryMax       int
	MonthRetryMax  int
	MaxConcurrency int
	MaxQPS         float64
}

type Mail struct {
	Provider string
	From     string
	AdminTo  string
	// 用户触发的验证/找回邮件使用库内全局配额。
	UserMinuteLimit int
	UserDayLimit    int
	// 邮件内站点链接前缀。留空时模板按钮不可用。
	BaseURL string
	SMTP    SMTPMail
	Tencent TencentMail
	Resend  ResendMail
}

type ResendMail struct {
	APIKey  string
	ReplyTo string
}

type SMTPMail struct {
	Host    string
	Port    int
	User    string
	Pass    string
	TLSMode string
}

type TencentMail struct {
	SecretID   string
	SecretKey  string
	Region     string
	FromEmail  string
	TemplateID uint64
}

func Load() (Config, error) {
	environment, err := normalizeEnvironment(os.Getenv("APP_ENV"))
	if err != nil {
		return Config{}, err
	}
	locName := value("APP_TIMEZONE", "Asia/Shanghai")
	loc, err := time.LoadLocation(locName)
	if err != nil {
		return Config{}, fmt.Errorf("APP_TIMEZONE: %w", err)
	}

	var baseURL *url.URL
	if raw := value("ELECTRICITY_BASE_URL", ""); raw != "" {
		parsed, parseErr := url.Parse(raw)
		if parseErr != nil || parsed.Scheme == "" || parsed.Host == "" {
			return Config{}, errors.New("ELECTRICITY_BASE_URL must be an absolute HTTP(S) URL")
		}
		baseURL = parsed
	}
	if baseURL != nil && baseURL.Scheme != "http" && baseURL.Scheme != "https" {
		return Config{}, errors.New("ELECTRICITY_BASE_URL scheme must be http or https")
	}
	upstreamGlobalQPS, err := floatValue("UPSTREAM_GLOBAL_QPS", 8, 0.01)
	if err != nil {
		return Config{}, err
	}
	upstreamGlobalConcurrency, err := intValue("UPSTREAM_GLOBAL_CONCURRENCY", 8, 1)
	if err != nil {
		return Config{}, err
	}
	upstreamMaxQPS, err := floatValue("UPSTREAM_MAX_QPS", 30, 0.01)
	if err != nil {
		return Config{}, err
	}
	upstreamMaxConcurrency, err := intValue("UPSTREAM_MAX_CONCURRENCY", 16, 1)
	if err != nil {
		return Config{}, err
	}
	if upstreamGlobalQPS > upstreamMaxQPS || upstreamGlobalConcurrency > upstreamMaxConcurrency {
		return Config{}, errors.New("default upstream gate QPS/concurrency must not exceed configured maxima")
	}
	databaseMaxConns, err := intValue("DATABASE_MAX_CONNS", defaultDatabaseMaxConns, 2)
	if err != nil {
		return Config{}, err
	}
	/* 闸门并发槽占用池连接，必须预留写结果与查询余量。
	   无余量时抬高闸门只会自堵：在途请求数受 MaxConns 限制。 */
	if upstreamMaxConcurrency+dbConnectionHeadroom > databaseMaxConns {
		return Config{}, fmt.Errorf(
			"UPSTREAM_MAX_CONCURRENCY %d needs DATABASE_MAX_CONNS >= %d (got %d)",
			upstreamMaxConcurrency, upstreamMaxConcurrency+dbConnectionHeadroom, databaseMaxConns,
		)
	}

	concurrency, err := intValue("SCAN_CONCURRENCY", 1, 1)
	if err != nil {
		return Config{}, err
	}
	qps, err := floatValue("SCAN_QPS", 0.5, 0.01)
	if err != nil {
		return Config{}, err
	}
	timeout, err := secondsValue("SCAN_TIMEOUT_SEC", 20, 1)
	if err != nil {
		return Config{}, err
	}
	retryMax, err := intValue("SCAN_RETRY_MAX", 3, 0)
	if err != nil {
		return Config{}, err
	}
	retryBase, err := millisValue("SCAN_RETRY_BASE_DELAY_MS", 1000, 1)
	if err != nil {
		return Config{}, err
	}
	retryMaxDelay, err := millisValue("SCAN_RETRY_MAX_DELAY_MS", 30000, 1)
	if err != nil {
		return Config{}, err
	}
	if retryMaxDelay < retryBase {
		return Config{}, errors.New("SCAN_RETRY_MAX_DELAY_MS must be >= SCAN_RETRY_BASE_DELAY_MS")
	}
	staleAfterHours, err := intValue("SCAN_STALE_AFTER_HOURS", 36, 1)
	if err != nil {
		return Config{}, err
	}
	resultRetentionDays, err := intValue("SCAN_RESULT_RETENTION_DAYS", 180, 1)
	if err != nil {
		return Config{}, err
	}
	logRetentionDays, err := intValue("LOG_RETENTION_DAYS", 30, 1)
	if err != nil {
		return Config{}, err
	}
	maxConcurrency, err := intValue("SCAN_MAX_CONCURRENCY", 16, 1)
	if err != nil {
		return Config{}, err
	}
	maxQPS, err := floatValue("SCAN_MAX_QPS", 10, 0.01)
	if err != nil {
		return Config{}, err
	}
	if concurrency > maxConcurrency || qps > maxQPS {
		return Config{}, errors.New("default scan concurrency/QPS must not exceed configured maxima")
	}
	billConcurrency, err := intValue("BILL_CONCURRENCY", 4, 1)
	if err != nil {
		return Config{}, err
	}
	billQPS, err := floatValue("BILL_QPS", 5, 0.01)
	if err != nil {
		return Config{}, err
	}
	billRetryMax, err := intValue("BILL_RETRY_MAX", 1, 0)
	if err != nil {
		return Config{}, err
	}
	billMonthRetryMax, err := intValue("BILL_MONTH_RETRY_MAX", 1, 0)
	if err != nil {
		return Config{}, err
	}
	billMaxConcurrency, err := intValue("BILL_MAX_CONCURRENCY", 8, 1)
	if err != nil {
		return Config{}, err
	}
	billMaxQPS, err := floatValue("BILL_MAX_QPS", 30, 0.01)
	if err != nil {
		return Config{}, err
	}
	if billConcurrency > billMaxConcurrency || billQPS > billMaxQPS {
		return Config{}, errors.New("default bill concurrency/QPS must not exceed configured maxima")
	}
	detailConcurrency, err := intValue("DETAIL_CONCURRENCY", 8, 1)
	if err != nil {
		return Config{}, err
	}
	detailQPS, err := floatValue("DETAIL_QPS", 8, 0.01)
	if err != nil {
		return Config{}, err
	}
	detailRetryMax, err := intValue("DETAIL_RETRY_MAX", 1, 0)
	if err != nil {
		return Config{}, err
	}
	detailMonthRetryMax, err := intValue("DETAIL_MONTH_RETRY_MAX", 1, 0)
	if err != nil {
		return Config{}, err
	}
	detailMaxConcurrency, err := intValue("DETAIL_MAX_CONCURRENCY", 8, 1)
	if err != nil {
		return Config{}, err
	}
	detailMaxQPS, err := floatValue("DETAIL_MAX_QPS", 30, 0.01)
	if err != nil {
		return Config{}, err
	}
	if detailConcurrency > detailMaxConcurrency || detailQPS > detailMaxQPS {
		return Config{}, errors.New("default detail concurrency/QPS must not exceed configured maxima")
	}
	detailBootstrapFrom := value("DETAIL_BOOTSTRAP_FROM", "2025-01")
	if _, err := time.Parse("2006-01", detailBootstrapFrom); err != nil {
		return Config{}, errors.New("DETAIL_BOOTSTRAP_FROM must be YYYY-MM")
	}
	detailAutoBootstrap, err := boolValue("DETAIL_AUTO_BOOTSTRAP", false)
	if err != nil {
		return Config{}, err
	}
	smtpPort, err := intValue("SMTP_PORT", 587, 1)
	if err != nil {
		return Config{}, err
	}
	mailUserMinuteLimit, err := intValue("MAIL_USER_LIMIT_PER_MINUTE", 20, 1)
	if err != nil {
		return Config{}, err
	}
	mailUserDayLimit, err := intValue("MAIL_USER_LIMIT_PER_DAY", 500, 1)
	if err != nil {
		return Config{}, err
	}
	templateID, err := uint64Value("TENCENT_SES_TEMPLATE_ID")
	if err != nil {
		return Config{}, err
	}
	accessTTL, err := secondsValue("AUTH_ACCESS_TTL_SEC", 15*60, 60)
	if err != nil {
		return Config{}, err
	}
	refreshDays, err := intValue("AUTH_REFRESH_TTL_DAYS", 30, 1)
	if err != nil {
		return Config{}, err
	}
	cookieSecure, err := boolValue("AUTH_COOKIE_SECURE", environment == "production")
	if err != nil {
		return Config{}, err
	}
	publicBaseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")), "/")
	oauth := OAuth{
		BaseURL: strings.TrimRight(value("OAUTH_BASE_URL", publicBaseURL), "/"),
		Google: OAuthProvider{
			ClientID:     strings.TrimSpace(os.Getenv("GOOGLE_OAUTH_CLIENT_ID")),
			ClientSecret: strings.TrimSpace(os.Getenv("GOOGLE_OAUTH_CLIENT_SECRET")),
		},
		GitHub: OAuthProvider{
			ClientID:     strings.TrimSpace(os.Getenv("GITHUB_OAUTH_CLIENT_ID")),
			ClientSecret: strings.TrimSpace(os.Getenv("GITHUB_OAUTH_CLIENT_SECRET")),
		},
	}
	/* 半份 OAuth 凭证必须启动失败。
	   否则按钮静默消失，排查无线索。 */
	for name, provider := range map[string]OAuthProvider{"GOOGLE": oauth.Google, "GITHUB": oauth.GitHub} {
		if (provider.ClientID == "") != (provider.ClientSecret == "") {
			return Config{}, fmt.Errorf("%s_OAUTH_CLIENT_ID and %s_OAUTH_CLIENT_SECRET must be set together", name, name)
		}
	}
	if oauth.BaseURL != "" {
		parsed, err := url.Parse(oauth.BaseURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return Config{}, errors.New("OAUTH_BASE_URL (or PUBLIC_BASE_URL) must be an absolute HTTP(S) URL")
		}
	} else if oauth.Google.Configured() || oauth.GitHub.Configured() {
		return Config{}, errors.New("OAuth login requires OAUTH_BASE_URL or PUBLIC_BASE_URL for the redirect URI")
	}
	captchaProvider := strings.ToLower(strings.TrimSpace(os.Getenv("CAPTCHA_PROVIDER")))
	switch captchaProvider {
	case "", "disabled", "turnstile":
	default:
		return Config{}, errors.New("CAPTCHA_PROVIDER must be one of disabled, turnstile")
	}
	captchaActions := splitCSV(value("CAPTCHA_ACTIONS", "auth.login,auth.register_code,auth.password_reset_code,meter.preview"))
	jwtSecret := os.Getenv("AUTH_JWT_SECRET")
	if jwtSecret != "" && len(jwtSecret) < 32 {
		return Config{}, errors.New("AUTH_JWT_SECRET must contain at least 32 bytes")
	}
	adminToken := strings.TrimSpace(os.Getenv("ADMIN_TOKEN"))
	rateProxySecret := strings.TrimSpace(os.Getenv("AUTH_RATE_PROXY_SECRET"))
	if rateProxySecret != "" && len(rateProxySecret) < 32 {
		return Config{}, errors.New("AUTH_RATE_PROXY_SECRET must contain at least 32 characters when configured")
	}
	/* 生产环境在启动期强制安全项。
	   否则明文 cookie 与弱令牌会静默上线。 */
	if environment == EnvProduction {
		if !cookieSecure {
			return Config{}, errors.New("APP_ENV=production requires AUTH_COOKIE_SECURE=true; use APP_ENV=test for an HTTP-only deployment")
		}
		if len(adminToken) < 32 {
			return Config{}, errors.New("APP_ENV=production requires ADMIN_TOKEN with at least 32 characters")
		}
		if jwtSecret == "" {
			return Config{}, errors.New("APP_ENV=production requires AUTH_JWT_SECRET")
		}
	}

	return Config{
		App: App{
			Environment: environment,
			Timezone:    loc,
		},
		Upstream: Upstream{
			Provider:          value("PROVIDER", ""),
			AreaID:            value("AREA_ID", defaultAreaID),
			AreaName:          value("AREA_NAME", defaultAreaName),
			BaseURL:           baseURL,
			GlobalQPS:         upstreamGlobalQPS,
			GlobalConcurrency: upstreamGlobalConcurrency,
			MaxQPS:            upstreamMaxQPS,
			MaxConcurrency:    upstreamMaxConcurrency,
		},
		Database: Database{
			URL:      strings.TrimSpace(os.Getenv("DATABASE_URL")),
			MaxConns: int32(databaseMaxConns),
		},
		HTTP: HTTP{
			Address:         value("HTTP_ADDR", "127.0.0.1:8080"),
			AdminToken:      adminToken,
			SettingsKey:     strings.TrimSpace(os.Getenv("SETTINGS_ENCRYPTION_KEY")),
			RateProxySecret: rateProxySecret,
		},
		Auth: Auth{
			JWTSecret:    jwtSecret,
			Issuer:       value("AUTH_JWT_ISSUER", "edu-power-push"),
			AccessTTL:    accessTTL,
			RefreshTTL:   time.Duration(refreshDays) * 24 * time.Hour,
			CookieName:   value("AUTH_REFRESH_COOKIE_NAME", "edu_power_refresh"),
			CookieSecure: cookieSecure,
		},
		OAuth: oauth,
		Captcha: Captcha{
			Provider:           captchaProvider,
			SiteKey:            strings.TrimSpace(os.Getenv("TURNSTILE_SITE_KEY")),
			SecretKey:          strings.TrimSpace(os.Getenv("TURNSTILE_SECRET_KEY")),
			Hostname:           strings.TrimSpace(os.Getenv("TURNSTILE_HOSTNAME")),
			Actions:            captchaActions,
			TurnstileVerifyURL: value("TURNSTILE_VERIFY_URL", "https://challenges.cloudflare.com/turnstile/v0/siteverify"),
		},
		Scan: Scan{
			Cron:            strings.TrimSpace(os.Getenv("SCAN_CRON")),
			BoundCron:       strings.TrimSpace(value("BOUND_SCAN_CRON", "5 * * * *")),
			Concurrency:     concurrency,
			QPS:             qps,
			Timeout:         timeout,
			RetryMax:        retryMax,
			RetryBaseDelay:  retryBase,
			RetryMaxDelay:   retryMaxDelay,
			StaleAfter:      time.Duration(staleAfterHours) * time.Hour,
			ResultRetention: time.Duration(resultRetentionDays) * 24 * time.Hour,
			LogRetention:    time.Duration(logRetentionDays) * 24 * time.Hour,
			MaxConcurrency:  maxConcurrency,
			MaxQPS:          maxQPS,
		},
		Bill: Bill{
			Cron:        strings.TrimSpace(value("BILL_CRON", "0 19 1 * *")),
			Concurrency: billConcurrency, QPS: billQPS, RetryMax: billRetryMax,
			MonthRetryMax: billMonthRetryMax, MaxConcurrency: billMaxConcurrency, MaxQPS: billMaxQPS,
		},
		Detail: Detail{
			Cron:          strings.TrimSpace(value("DETAIL_CRON", "30 6 * * *")),
			RetryCron:     strings.TrimSpace(value("DETAIL_RETRY_CRON", "15 10 * * *")),
			BootstrapFrom: detailBootstrapFrom,
			AutoBootstrap: detailAutoBootstrap,
			Concurrency:   detailConcurrency, QPS: detailQPS, RetryMax: detailRetryMax,
			MonthRetryMax: detailMonthRetryMax, MaxConcurrency: detailMaxConcurrency, MaxQPS: detailMaxQPS,
		},
		Mail: Mail{
			Provider:        strings.TrimSpace(os.Getenv("MAIL_PROVIDER")),
			From:            strings.TrimSpace(os.Getenv("MAIL_FROM")),
			AdminTo:         strings.TrimSpace(os.Getenv("MAIL_ADMIN_TO")),
			UserMinuteLimit: mailUserMinuteLimit,
			UserDayLimit:    mailUserDayLimit,
			BaseURL:         publicBaseURL,
			Resend: ResendMail{
				APIKey:  strings.TrimSpace(os.Getenv("RESEND_API_KEY")),
				ReplyTo: strings.TrimSpace(os.Getenv("RESEND_REPLY_TO")),
			},
			SMTP: SMTPMail{
				Host:    strings.TrimSpace(os.Getenv("SMTP_HOST")),
				Port:    smtpPort,
				User:    strings.TrimSpace(os.Getenv("SMTP_USER")),
				Pass:    os.Getenv("SMTP_PASS"),
				TLSMode: value("SMTP_TLS_MODE", "starttls"),
			},
			Tencent: TencentMail{
				SecretID:   strings.TrimSpace(os.Getenv("TENCENTCLOUD_SECRET_ID")),
				SecretKey:  os.Getenv("TENCENTCLOUD_SECRET_KEY"),
				Region:     value("TENCENTCLOUD_REGION", "ap-guangzhou"),
				FromEmail:  strings.TrimSpace(os.Getenv("TENCENT_SES_FROM_EMAIL")),
				TemplateID: templateID,
			},
		},
	}, nil
}

func splitCSV(raw string) []string {
	items := make([]string, 0)
	seen := map[string]struct{}{}
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		items = append(items, item)
	}
	return items
}

func boolValue(key string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean", key)
	}
	return value, nil
}

func uint64Value(key string) (uint64, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return value, nil
}

func value(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func intValue(key string, fallback, minimum int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < minimum {
		return 0, fmt.Errorf("%s must be an integer >= %d", key, minimum)
	}
	return n, nil
}

func floatValue(key string, fallback, minimum float64) (float64, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.ParseFloat(raw, 64)
	if err != nil || n < minimum {
		return 0, fmt.Errorf("%s must be a number >= %g", key, minimum)
	}
	return n, nil
}

func secondsValue(key string, fallback, minimum int) (time.Duration, error) {
	n, err := intValue(key, fallback, minimum)
	return time.Duration(n) * time.Second, err
}

func millisValue(key string, fallback, minimum int) (time.Duration, error) {
	n, err := intValue(key, fallback, minimum)
	return time.Duration(n) * time.Millisecond, err
}
