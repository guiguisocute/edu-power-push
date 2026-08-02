package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/config"
	"github.com/edu-power-push/edu-power-push/backend/internal/storage"
)

func validFrontendConfig() storage.FrontendConfigSettings {
	emptyRoomThreshold := storage.DefaultEmptyRoomThresholdKWH
	return storage.FrontendConfigSettings{
		Features: storage.FrontendFeatures{
			Auth:     storage.FrontendAuthFeatures{EmailLogin: true, Registration: true},
			Channels: map[string]bool{"mail": true, "sms": false},
		},
		Display: storage.FrontendDisplay{
			CampusName: "示例校区", AreaName: "示例大学",
			EmptyRoomThresholdKWH: &emptyRoomThreshold, RankingRefreshTime: "09:00",
		},
	}
}

func TestValidateFrontendConfig(t *testing.T) {
	valid := validFrontendConfig()
	if err := validateFrontendConfig(valid); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*storage.FrontendConfigSettings)
	}{
		{"email login disabled", func(v *storage.FrontendConfigSettings) { v.Features.Auth.EmailLogin = false }},
		{"sms enabled", func(v *storage.FrontendConfigSettings) { v.Features.Auth.SMSLogin = true }},
		{"unknown channel", func(v *storage.FrontendConfigSettings) { v.Features.Channels["unknown"] = true }},
		{"unknown channel in order", func(v *storage.FrontendConfigSettings) { v.Features.ChannelOrder = []string{"mail", "unknown"} }},
		{"duplicate channel in order", func(v *storage.FrontendConfigSettings) { v.Features.ChannelOrder = []string{"mail", "mail"} }},
		{"invalid ranking refresh time", func(v *storage.FrontendConfigSettings) { v.Display.RankingRefreshTime = "25:00" }},
		{"category without an id", func(v *storage.FrontendConfigSettings) {
			v.Features.ChannelCategories = []storage.FrontendChannelCategory{{Name: "开箱即用", Channels: []string{"mail"}}}
		}},
		{"category id with uppercase", func(v *storage.FrontendConfigSettings) {
			v.Features.ChannelCategories = []storage.FrontendChannelCategory{{ID: "Easy", Name: "开箱即用"}}
		}},
		{"category without a name", func(v *storage.FrontendConfigSettings) {
			v.Features.ChannelCategories = []storage.FrontendChannelCategory{{ID: "easy", Name: "  "}}
		}},
		{"duplicate category id", func(v *storage.FrontendConfigSettings) {
			v.Features.ChannelCategories = []storage.FrontendChannelCategory{
				{ID: "easy", Name: "开箱即用"},
				{ID: "easy", Name: "又一个"},
			}
		}},
		{"unknown channel in category", func(v *storage.FrontendConfigSettings) {
			v.Features.ChannelCategories = []storage.FrontendChannelCategory{{ID: "easy", Name: "开箱即用", Channels: []string{"unknown"}}}
		}},
		{"channel in two categories", func(v *storage.FrontendConfigSettings) {
			v.Features.ChannelCategories = []storage.FrontendChannelCategory{
				{ID: "easy", Name: "开箱即用", Channels: []string{"mail"}},
				{ID: "bots", Name: "群聊机器人", Channels: []string{"mail"}},
			}
		}},
		{"unimplemented channel marked ready", func(v *storage.FrontendConfigSettings) { v.Features.Channels["sms"] = true }},
		{"empty campus", func(v *storage.FrontendConfigSettings) { v.Display.CampusName = "" }},
		{"invalid rate", func(v *storage.FrontendConfigSettings) { value := "0"; v.Display.ElectricityRate = &value }},
		{"zero empty room threshold", func(v *storage.FrontendConfigSettings) {
			value := "0"
			v.Display.EmptyRoomThresholdKWH = &value
		}},
		{"oversized empty room threshold", func(v *storage.FrontendConfigSettings) {
			value := "10.0001"
			v.Display.EmptyRoomThresholdKWH = &value
		}},
		{"semester key not a month", func(v *storage.FrontendConfigSettings) {
			v.Display.Semesters = []storage.FrontendSemester{{Key: "2026-3", Start: "2026-03-02", End: "2026-07-12"}}
		}},
		{"semester starts outside its own month", func(v *storage.FrontendConfigSettings) {
			v.Display.Semesters = []storage.FrontendSemester{{Key: "2026-03", Start: "2026-02-23", End: "2026-07-12"}}
		}},
		{"semester ends before it starts", func(v *storage.FrontendConfigSettings) {
			v.Display.Semesters = []storage.FrontendSemester{{Key: "2026-03", Start: "2026-03-02", End: "2026-03-01"}}
		}},
		{"semester shorter than a week", func(v *storage.FrontendConfigSettings) {
			v.Display.Semesters = []storage.FrontendSemester{{Key: "2026-03", Start: "2026-03-02", End: "2026-03-05"}}
		}},
		{"duplicate semester", func(v *storage.FrontendConfigSettings) {
			v.Display.Semesters = []storage.FrontendSemester{
				{Key: "2026-03", Start: "2026-03-02", End: "2026-07-12"},
				{Key: "2026-03", Start: "2026-03-09", End: "2026-07-19"},
			}
		}},
		{"overlapping semesters", func(v *storage.FrontendConfigSettings) {
			v.Display.Semesters = []storage.FrontendSemester{
				{Key: "2026-03", Start: "2026-03-02", End: "2026-07-12"},
				{Key: "2026-07", Start: "2026-07-06", End: "2026-12-20"},
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := validFrontendConfig()
			test.mutate(&value)
			if err := validateFrontendConfig(value); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestBuildNotificationTestMessageUsesBoundMeterFacts(t *testing.T) {
	observed := time.Date(2026, 7, 29, 7, 8, 0, 0, time.UTC)
	user := storage.AuthUser{Meter: &storage.UserMeter{
		Meter: "31240718", Campus: "示例校区", Building: "12栋", Floor: "4楼", Room: "402",
	}}
	overview := &storage.MeterOverviewView{Latest: &storage.LatestReadingView{
		TotalYuan: "43.87", ObservedAt: observed,
	}}
	message := buildNotificationTestMessage(user, overview)
	for _, want := range []string{
		"POWER·PUSH 测试", "剩余电费：43.87 元", "数据更新时间：2026-07-29 15:08",
		"电表号：31240718", "宿舍楼栋：12栋", "楼层：4楼", "房间号：402",
	} {
		if !strings.Contains(message.Title+"\n"+message.Body, want) {
			t.Fatalf("message missing %q: %#v", want, message)
		}
	}

	unbound := buildNotificationTestMessage(storage.AuthUser{}, nil)
	if !strings.Contains(unbound.Body, "剩余电费：暂无可用读数") || !strings.Contains(unbound.Body, "电表号：暂无") {
		t.Fatalf("unbound message invented facts: %#v", unbound)
	}
	// 结构化字段是 Webhook 解析契约。未绑定时必须留空，禁止塞占位文案。
	if unbound.Meter.Number != "" || unbound.Meter.Building != "" {
		t.Fatalf("unbound meter must stay empty in the machine-readable contract: %#v", unbound.Meter)
	}
}

func TestValidateScannerSettingsCoversAllCollectors(t *testing.T) {
	server := &Server{cfg: config.Config{
		Upstream: config.Upstream{MaxQPS: 30, MaxConcurrency: 32},
		Scan:     config.Scan{MaxQPS: 10, MaxConcurrency: 16},
		Bill:     config.Bill{MaxQPS: 30, MaxConcurrency: 8},
		Detail:   config.Detail{MaxQPS: 30, MaxConcurrency: 8},
	}}
	settings := storage.ScannerControlSettings{
		Shared:  storage.SharedUpstreamSettings{QPS: 12, Concurrency: 16},
		Balance: storage.BalanceScannerSettings{Cron: "15 */4 * * *", BoundCron: "5 * * * *", QPS: 4, Concurrency: 4, RetryMax: 2, Enabled: true},
		Bills:   storage.BatchScannerSettings{Cron: "0 19 1 * *", QPS: 10, Concurrency: 6, RetryMax: 2, MonthRetryMax: 2, Enabled: true},
		DailyDetails: storage.DetailScannerSettings{
			BatchScannerSettings: storage.BatchScannerSettings{Cron: "30 6 * * *", QPS: 8, Concurrency: 8, RetryMax: 1, MonthRetryMax: 1, Enabled: true},
			RetryCron:            "15 10 * * *",
			BootstrapFrom:        "2025-01",
		},
	}
	if err := server.validateScannerSettings(settings); err != nil {
		t.Fatalf("valid scanner settings: %v", err)
	}

	invalidCron := settings
	invalidCron.Bills.Cron = "99 99 * * *"
	if err := server.validateScannerSettings(invalidCron); err == nil {
		t.Fatal("invalid bill cron was accepted")
	}

	invalidRetryCron := settings
	invalidRetryCron.DailyDetails.RetryCron = "99 99 * * *"
	if err := server.validateScannerSettings(invalidRetryCron); err == nil {
		t.Fatal("invalid daily detail retry cron was accepted")
	}

	overLimit := settings
	overLimit.DailyDetails.QPS = 31
	if err := server.validateScannerSettings(overLimit); err == nil {
		t.Fatal("daily detail qps above its safety ceiling was accepted")
	}

	// 共享闸门可一次抬高全部三类扫描器。它本身必须有上限。
	sharedOverLimit := settings
	sharedOverLimit.Shared.Concurrency = 33
	if err := server.validateScannerSettings(sharedOverLimit); err == nil {
		t.Fatal("shared gate concurrency above its safety ceiling was accepted")
	}
	sharedMissing := settings
	sharedMissing.Shared.QPS = 0
	if err := server.validateScannerSettings(sharedMissing); err == nil {
		t.Fatal("shared gate without a qps was accepted")
	}
}

/* 合法且不重叠的校历必须通过。只测拒绝等于没测校验。 */
func TestValidFrontendConfigAcceptsSemesters(t *testing.T) {
	value := validFrontendConfig()
	value.Display.Semesters = []storage.FrontendSemester{
		{Key: "2025-09", Start: "2025-09-01", End: "2026-01-18"},
		{Key: "2026-03", Start: "2026-03-02", End: "2026-07-12"},
	}
	if err := validateFrontendConfig(value); err != nil {
		t.Fatalf("合法校历被拒：%v", err)
	}
}

/*
运维面板已迁到前端 /admin 路由。

	后端老路径仅保留跳转。老书签访问时重定向过去。
	禁止返回 404。
*/
func TestLegacyAdminPathRedirectsToPanel(t *testing.T) {
	server := New(context.Background(), config.Config{}, nil, nil, nil, nil)
	request := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusMovedPermanently)
	}
	if location := response.Header().Get("Location"); location != "/admin" {
		t.Fatalf("Location = %q, want /admin", location)
	}
	// 后端禁止再向产物塞入手写运维 HTML。
	if body := response.Body.String(); strings.Contains(body, "adminToken") {
		t.Error("the legacy embedded admin page is still being served")
	}
}

func TestProtectedEndpointRequiresBearerToken(t *testing.T) {
	server := New(context.Background(), config.Config{HTTP: config.HTTP{AdminToken: "test-token"}}, nil, nil, nil, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/mail/test", bytes.NewBufferString(`{"recipient":"admin@example.com"}`))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("security headers were not applied")
	}
	if response.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatal("clickjacking compatibility header was not applied")
	}
	if response.Header().Get("Permissions-Policy") != "camera=(), microphone=(), geolocation=()" {
		t.Fatal("browser capability policy was not applied")
	}
}

func TestUnconfiguredMailReturnsServiceUnavailable(t *testing.T) {
	server := New(context.Background(), config.Config{HTTP: config.HTTP{AdminToken: "test-token"}}, nil, nil, nil, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/mail/test", bytes.NewBufferString(`{"recipient":"admin@example.com"}`))
	request.Header.Set("Authorization", "Bearer test-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusServiceUnavailable, response.Body.String())
	}
}

// 校园聚合数据匿名可读。无令牌禁止被 401 挡住。
// 具体数据由 storage 层负责。
func TestCampusEndpointsAreAnonymous(t *testing.T) {
	server := New(context.Background(), testAuthConfig(), nil, nil, nil, nil)
	// hourly-heatmap 是唯一不碰数据库的 campus 端点。可在 pool=nil 下走完整链路。
	request := httptest.NewRequest(http.MethodGet, "/api/v1/campus/hourly-heatmap", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("anonymous campus request: status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "upstream_hourly_data_unavailable") {
		t.Errorf("unexpected body: %s", response.Body.String())
	}
}

// 运维端点不接受产品用户令牌。放开 campus/* 禁止顺带放开 /admin/* 与 /meters/*。
func TestAdminEndpointsRejectUserAccessToken(t *testing.T) {
	server := New(context.Background(), testAuthConfig(), nil, nil, nil, nil)
	accessToken, _, err := server.tokens.SignAccess(
		"11111111-1111-4111-8111-111111111111",
		"22222222-2222-4222-8222-222222222222",
	)
	if err != nil {
		t.Fatalf("sign access token: %v", err)
	}
	for _, path := range []string{"/api/v1/inventory/tree", "/api/v1/meters/31240718/overview", "/api/v1/admin/overview"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer "+accessToken)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		/* 普通用户的 access token 进不了管理端点。
		   401 表示无法定角色。403 表示级别不够。
		   两者都算挡住。禁止出现 2xx。 */
		if response.Code != http.StatusUnauthorized && response.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 401 or 403", path, response.Code)
		}
	}
}

func testAuthConfig() config.Config {
	return config.Config{
		HTTP: config.HTTP{AdminToken: "test-token"},
		Auth: config.Auth{
			JWTSecret: strings.Repeat("test-secret-", 4), Issuer: "edu-power-push",
			AccessTTL: 15 * time.Minute, RefreshTTL: 720 * time.Hour, CookieName: "edu_power_refresh",
		},
	}
}

func TestAuthRateLimiterRejectsBurst(t *testing.T) {
	limiter := newIPRateLimiter("")
	accepted := 0
	handler := limiter.limit(func(w http.ResponseWriter, _ *http.Request) {
		accepted++
		w.WriteHeader(http.StatusNoContent)
	})
	for i := 0; i < 10; i++ {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		request.RemoteAddr = "192.0.2.10:43210"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent {
			t.Fatalf("burst request %d status = %d, want %d", i+1, response.Code, http.StatusNoContent)
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	request.RemoteAddr = "192.0.2.10:43211"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("limited request status = %d, want %d", response.Code, http.StatusTooManyRequests)
	}
	if response.Header().Get("Retry-After") != "2" || !strings.Contains(response.Body.String(), `"code":"rate_limited"`) {
		t.Fatalf("rate-limit response is incomplete: headers=%v body=%s", response.Header(), response.Body.String())
	}
	if accepted != 10 {
		t.Fatalf("accepted = %d, want 10", accepted)
	}
}

func TestAuthRateLimiterHasAggregateCap(t *testing.T) {
	limiter := newIPRateLimiter("")
	for i := 0; i < limiter.global.Burst(); i++ {
		if !limiter.allow(fmt.Sprintf("client-%d", i)) {
			t.Fatalf("aggregate limiter rejected request %d inside burst", i+1)
		}
	}
	if limiter.allow("client-overflow") {
		t.Fatal("aggregate limiter accepted a reconnect-style burst above its cap")
	}
}

func TestClientRateKeyUsesTrustedProxyHeader(t *testing.T) {
	const proxySecret = "0123456789abcdef0123456789abcdef"
	tests := []struct {
		name, remote, rateKey, rateSecret, realIP, want string
	}{
		{"direct ipv4", "192.0.2.10:43210", "", "", "", "ip:192.0.2.10"},
		{"direct ipv6", "[2001:db8::1]:443", "", "", "", "ip:2001:db8::1"},
		{"authenticated gateway", "172.20.0.3:43210", "123:456:A1B2", proxySecret, "", "proxy:123:456:A1B2"},
		{"forged key without secret", "172.20.0.3:43210", "999:999:A1B2", "", "", "ip:172.20.0.3"},
		{"forged key with wrong secret", "172.20.0.3:43210", "999:999:A1B2", "wrong", "", "ip:172.20.0.3"},
		{"generic proxy header ignored", "172.20.0.3:43210", "", proxySecret, "198.51.100.7", "ip:172.20.0.3"},
		{"public peer cannot spoof", "192.0.2.10:43210", "123:456:A1B2", proxySecret, "", "ip:192.0.2.10"},
		{"invalid proxy header", "127.0.0.1:43210", "not-a-rate-key", proxySecret, "", "ip:127.0.0.1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
			request.RemoteAddr = tc.remote
			if tc.rateKey != "" {
				request.Header.Set("X-Rate-Proxy-Key", tc.rateKey)
			}
			if tc.rateSecret != "" {
				request.Header.Set("X-Rate-Proxy-Secret", tc.rateSecret)
			}
			if tc.realIP != "" {
				request.Header.Set("X-Real-IP", tc.realIP)
			}
			if got := clientRateKey(request, proxySecret); got != tc.want {
				t.Errorf("clientRateKey() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCampusAggregateRangesAreBoundedBeforeDatabaseWork(t *testing.T) {
	server := &Server{}
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	daily := httptest.NewRequest(http.MethodGet,
		"/api/v1/campus/summary?from="+url.QueryEscape(from.Format(time.RFC3339))+"&to="+url.QueryEscape(from.Add(371*24*time.Hour).Format(time.RFC3339)), nil)
	dailyResponse := httptest.NewRecorder()
	server.campusSummary(dailyResponse, daily)
	if dailyResponse.Code != http.StatusBadRequest || !strings.Contains(dailyResponse.Body.String(), `"code":"range_too_large"`) {
		t.Fatalf("oversized daily range status=%d body=%s", dailyResponse.Code, dailyResponse.Body.String())
	}

	monthly := httptest.NewRequest(http.MethodGet, "/api/v1/campus/bills?from_month=2024-01&to_month=2026-01", nil)
	monthlyResponse := httptest.NewRecorder()
	server.campusBills(monthlyResponse, monthly)
	if monthlyResponse.Code != http.StatusBadRequest || !strings.Contains(monthlyResponse.Body.String(), `"code":"range_too_large"`) {
		t.Fatalf("oversized month range status=%d body=%s", monthlyResponse.Code, monthlyResponse.Body.String())
	}
}

/*
管理端点无会话时返回 401。禁止返回 404 或 2xx。

	每个管理端点仍要求 operator 及以上角色。
	角色每次请求现查库。路由存在，但无身份进不去。
*/
func TestAdminEndpointsRequireIdentity(t *testing.T) {
	paths := []string{
		"/api/v1/admin/session",
		"/api/v1/admin/overview",
		"/api/v1/admin/users",
		"/api/v1/admin/settings/mail",
	}
	for _, env := range []string{config.EnvDevelopment, config.EnvTest, config.EnvProduction} {
		for _, path := range paths {
			t.Run(env+" "+path, func(t *testing.T) {
				cfg := config.Config{
					App:  config.App{Environment: env},
					HTTP: config.HTTP{AdminToken: "test-token"},
				}
				server := New(context.Background(), cfg, nil, nil, nil, nil)
				request := httptest.NewRequest(http.MethodGet, path, nil)
				response := httptest.NewRecorder()
				server.Handler().ServeHTTP(response, request)
				if response.Code != http.StatusUnauthorized {
					t.Fatalf("status = %d, want 401 (body %s)", response.Code, response.Body.String())
				}
			})
		}
	}
}
