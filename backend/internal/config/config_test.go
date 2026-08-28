package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("APP_TIMEZONE", "")
	t.Setenv("ELECTRICITY_BASE_URL", "")
	t.Setenv("UPSTREAM_GLOBAL_QPS", "")
	t.Setenv("UPSTREAM_GLOBAL_CONCURRENCY", "")
	t.Setenv("REDIS_ADDR", "")
	t.Setenv("REDIS_DB", "")
	t.Setenv("REDIS_POOL_SIZE", "")
	t.Setenv("REDIS_PREFIX", "")
	t.Setenv("SCAN_CONCURRENCY", "")
	t.Setenv("SCAN_QPS", "")
	t.Setenv("BOUND_SCAN_CRON", "")
	t.Setenv("SCAN_TIMEOUT_SEC", "")
	t.Setenv("SCAN_RETRY_MAX", "")
	t.Setenv("SCAN_RETRY_BASE_DELAY_MS", "")
	t.Setenv("SCAN_RETRY_MAX_DELAY_MS", "")
	t.Setenv("SCAN_STALE_AFTER_HOURS", "")
	t.Setenv("SCAN_RESULT_RETENTION_DAYS", "")
	t.Setenv("LOG_RETENTION_DAYS", "")
	t.Setenv("MAIL_USER_LIMIT_PER_MINUTE", "")
	t.Setenv("MAIL_USER_LIMIT_PER_DAY", "")
	t.Setenv("BILL_CRON", "")
	t.Setenv("BILL_CONCURRENCY", "")
	t.Setenv("BILL_QPS", "")
	t.Setenv("DETAIL_CRON", "")
	t.Setenv("DETAIL_RETRY_CRON", "")
	t.Setenv("DETAIL_CONCURRENCY", "")
	t.Setenv("DETAIL_QPS", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// 未配学校时保持空，采集器据此拒绝启动。
	if cfg.Upstream.AreaID != "" || cfg.Upstream.AreaName != "" {
		t.Fatalf("expected no default school, got id=%q name=%q", cfg.Upstream.AreaID, cfg.Upstream.AreaName)
	}
	if cfg.Upstream.GlobalQPS != 8 || cfg.Upstream.GlobalConcurrency != 8 {
		t.Fatalf("unexpected shared upstream limits: %+v", cfg.Upstream)
	}
	if cfg.Scan.Concurrency != 1 || cfg.Scan.QPS != 0.5 {
		t.Fatalf("unexpected safe scan defaults: %+v", cfg.Scan)
	}
	if cfg.Scan.BoundCron != "5 * * * *" {
		t.Fatalf("BoundCron = %q", cfg.Scan.BoundCron)
	}
	if cfg.Scan.Timeout != 20*time.Second {
		t.Fatalf("Timeout = %s", cfg.Scan.Timeout)
	}
	if cfg.Bill.Cron != "0 19 1 * *" || cfg.Bill.Concurrency != 4 || cfg.Bill.QPS != 5 {
		t.Fatalf("unexpected bill defaults: %+v", cfg.Bill)
	}
	if cfg.Detail.Cron != "30 6 * * *" || cfg.Detail.RetryCron != "15 10 * * *" || cfg.Detail.Concurrency != 8 || cfg.Detail.QPS != 8 {
		t.Fatalf("unexpected detail defaults: %+v", cfg.Detail)
	}
	if cfg.HTTP.Address != "127.0.0.1:8080" {
		t.Fatalf("HTTP address = %q", cfg.HTTP.Address)
	}
	if cfg.Cache.Address != "" || cfg.Cache.Database != 0 || cfg.Cache.PoolSize != 16 || cfg.Cache.Prefix != "edu-power" {
		t.Fatalf("unexpected Redis defaults: %+v", cfg.Cache)
	}
	if cfg.Mail.UserMinuteLimit != 20 || cfg.Mail.UserDayLimit != 500 {
		t.Fatalf("unexpected user mail limits: %+v", cfg.Mail)
	}
}

func TestLoadRejectsUnsafeValues(t *testing.T) {
	t.Setenv("SCAN_CONCURRENCY", "0")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected an error")
	}
}

func TestProductionRedisRequiresPassword(t *testing.T) {
	strong := "0123456789abcdef0123456789abcdef"
	t.Setenv("APP_ENV", EnvProduction)
	t.Setenv("AUTH_COOKIE_SECURE", "true")
	t.Setenv("ADMIN_TOKEN", strong)
	t.Setenv("AUTH_JWT_SECRET", strong)
	t.Setenv("REDIS_ADDR", "redis:6379")
	t.Setenv("REDIS_PASSWORD", "")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted passwordless production Redis")
	}
}

func TestLoadRejectsWeakRateProxySecret(t *testing.T) {
	t.Setenv("AUTH_RATE_PROXY_SECRET", "guessable")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted a weak AUTH_RATE_PROXY_SECRET")
	}
}

// APP_ENV 非法取值必须启动失败。
// 错误拼写曾被当成非生产，Secure cookie 静默关闭。
func TestLoadRejectsUnknownEnvironment(t *testing.T) {
	t.Setenv("APP_ENV", "prod")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted APP_ENV=prod, want an error")
	}
}

func TestEnvironmentDrivesCookieSecureDefault(t *testing.T) {
	tests := map[string]bool{EnvDevelopment: false, EnvTest: false, EnvProduction: true}
	for environment, want := range tests {
		t.Setenv("APP_ENV", environment)
		t.Setenv("AUTH_COOKIE_SECURE", "")
		t.Setenv("ADMIN_TOKEN", "0123456789abcdef0123456789abcdef")
		t.Setenv("AUTH_JWT_SECRET", "0123456789abcdef0123456789abcdef")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("APP_ENV=%s: Load() error = %v", environment, err)
		}
		if cfg.Auth.CookieSecure != want {
			t.Errorf("APP_ENV=%s: CookieSecure = %v, want %v", environment, cfg.Auth.CookieSecure, want)
		}
	}
}

// 生产环境禁止明文 cookie、弱 ADMIN_TOKEN 与缺 JWT 密钥。
func TestProductionRejectsWeakSecurityConfig(t *testing.T) {
	strong := "0123456789abcdef0123456789abcdef"
	tests := map[string]struct{ cookieSecure, adminToken, jwtSecret string }{
		"insecure cookie":    {"false", strong, strong},
		"short admin token":  {"true", "short-token", strong},
		"missing jwt secret": {"true", strong, ""},
	}
	for name, test := range tests {
		t.Setenv("APP_ENV", EnvProduction)
		t.Setenv("AUTH_COOKIE_SECURE", test.cookieSecure)
		t.Setenv("ADMIN_TOKEN", test.adminToken)
		t.Setenv("AUTH_JWT_SECRET", test.jwtSecret)
		if _, err := Load(); err == nil {
			t.Errorf("%s: Load() succeeded, want an error", name)
		}
	}
}
