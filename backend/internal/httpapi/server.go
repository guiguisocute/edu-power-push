package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	authn "github.com/edu-power-push/edu-power-push/backend/internal/auth"
	"github.com/edu-power-push/edu-power-push/backend/internal/biller"
	"github.com/edu-power-push/edu-power-push/backend/internal/captcha"
	"github.com/edu-power-push/edu-power-push/backend/internal/config"
	"github.com/edu-power-push/edu-power-push/backend/internal/detailer"
	"github.com/edu-power-push/edu-power-push/backend/internal/importer"
	"github.com/edu-power-push/edu-power-push/backend/internal/mailer"
	"github.com/edu-power-push/edu-power-push/backend/internal/notification"
	"github.com/edu-power-push/edu-power-push/backend/internal/oauth"
	"github.com/edu-power-push/edu-power-push/backend/internal/provider"
	"github.com/edu-power-push/edu-power-push/backend/internal/push"
	"github.com/edu-power-push/edu-power-push/backend/internal/rediscache"
	"github.com/edu-power-push/edu-power-push/backend/internal/scanner"
	"github.com/edu-power-push/edu-power-push/backend/internal/secrets"
	"github.com/edu-power-push/edu-power-push/backend/internal/storage"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var electricityRatePattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]{1,4})?$`)
var rankingRefreshTimePattern = regexp.MustCompile(`^(?:[01][0-9]|2[0-3]):[0-5][0-9]$`)
var channelCategoryIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

var semesterKeyPattern = regexp.MustCompile(`^[0-9]{4}-(?:0[1-9]|1[0-2])$`)

const releaseVersion = "version_1"

type Mailer interface {
	/* Ready 决定现在能否发信。禁止再用 mailer == nil 判断。
	   管理面板允许运行期配置供应商。服务对象一直存在。
	   变化的是背后是否有可用凭证。 */
	Ready(ctx context.Context) bool
	ProviderName(ctx context.Context) string
	Invalidate()
	SendTest(context.Context, string) (mailer.Delivery, error)
	SendScanSummary(context.Context, string, string, string, any) (mailer.Delivery, error)
	SendWorkerFailure(context.Context, string, string) (mailer.Delivery, error)
	SendAnomalySummary(context.Context, string, string, int) (mailer.Delivery, error)
	// 产品邮件（mailtemplate HTML）。运维面板可用示例数据预览。
	SendBalanceAlert(ctx context.Context, recipient string, message notification.Message) (mailer.Delivery, error)
	SendPushTest(ctx context.Context, recipient, balance, updatedAt, location, meter string) (mailer.Delivery, error)
	SendUsageSummary(ctx context.Context, recipient string, message notification.Message) (mailer.Delivery, error)
	SendVerificationCode(ctx context.Context, recipient, code string, expireMinutes int) (mailer.Delivery, error)
	SendNotificationRecipientVerificationCode(ctx context.Context, recipient, code string, expireMinutes int) (mailer.Delivery, error)
	SendPasswordReset(ctx context.Context, recipient, code string, expireMinutes int) (mailer.Delivery, error)
	SendMeterUnbound(ctx context.Context, recipient, meter, location, unboundAt string) (mailer.Delivery, error)
	SendAccountDisabled(ctx context.Context, recipient, disabledAt, reason string) (mailer.Delivery, error)
	SendAccountDeleted(ctx context.Context, recipient, deletedAt string) (mailer.Delivery, error)
}

type Server struct {
	cfg  config.Config
	pool *pgxpool.Pool
	/* upstream 是可热切换的查询器。面板换学校后下一轮采集与手动刷新直接用新配置。
	   无需重启 API。未选学校时返回 school_not_configured。 */
	upstream *provider.Dynamic
	mailer   Mailer
	captcha  *captcha.Service
	channels push.Deliverer
	logger   *slog.Logger
	ctx      context.Context
	mux      *http.ServeMux
	tokens   *authn.Manager
	// oauthSettings 解析 .env 与面板合并后的第三方登录凭证。
	oauthSettings *oauth.Service
	authGate      *ipRateLimiter
	// 密码哈希与账号枚举填充故意昂贵。
	// 更窄闸门防止突发占满全部 CPU 核心。
	expensiveAuthGate *ipRateLimiter
	// 绑表预览按用户限流（每 3 秒 1 次、突发 20）。带会话后限流主体是账号而非 IP。
	previewGate *ipRateLimiter
	// 即时刷新按电表严格冷却。浏览器端冷却只负责交互，不能代替服务端保护上游。
	refreshGate *meterRefreshGate
	// 手动刷新不再排队等批量扫描。本通道自行限并发。
	// 超出请求排队等位。等不到则降级。
	refreshSlots chan struct{}
	requestGate  *storage.UpstreamRequestGate
	sharedCache  *rediscache.Client
	// 榜单按日周月固定刷新点缓存。完整响应按查看者隔离，禁止隐私数据串号。
	rankingCache *rankingResponseCache
	// 公开聚合端点保持匿名。数据库成本仍有界。
	campusGate  *ipRateLimiter
	campusCache *campusResponseCache
	dummyPHC    string
	// secrets 给 system_settings 凭证列加解密。未配密钥时为禁用态。
	secrets  *secrets.Box
	scanMu   sync.Mutex
	scanWG   sync.WaitGroup
	stopping bool
}

/*
mailReady 判断此刻能否发信。

	禁止再写 s.mailer == nil。管理面板允许运行期配置供应商。
	服务对象一直存在。变化的是背后是否有可用凭证。
*/
func (s *Server) mailReady(ctx context.Context) bool {
	return s.mailer != nil && s.mailer.Ready(ctx)
}

func New(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, client *provider.Dynamic, mailer Mailer, logger *slog.Logger) *Server {
	return NewWithSharedCache(ctx, cfg, pool, client, mailer, logger, nil)
}

func NewWithSharedCache(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, client *provider.Dynamic, mailer Mailer, logger *slog.Logger, shared *rediscache.Client) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	// Config.Load 会填充这些值。直接构造测试或服务时也必须安全。
	if cfg.Mail.UserMinuteLimit <= 0 {
		cfg.Mail.UserMinuteLimit = 20
	}
	if cfg.Mail.UserDayLimit <= 0 {
		cfg.Mail.UserDayLimit = 500
	}
	s := &Server{
		cfg: cfg, pool: pool, upstream: client, mailer: mailer, logger: logger, ctx: ctx, sharedCache: shared,
		channels: push.NewChannelSender(nil), mux: http.NewServeMux(),
		authGate:          newIPRateLimiter(cfg.HTTP.RateProxySecret),
		expensiveAuthGate: newExpensiveAuthRateLimiter(cfg.HTTP.RateProxySecret),
		previewGate:       newRateLimiter(3*time.Second, 20),
		refreshGate:       newMeterRefreshGate(30 * time.Second),
		refreshSlots:      make(chan struct{}, meterRefreshConcurrency),
		rankingCache:      newRankingResponseCacheWithShared(shared),
		campusGate:        newCampusRateLimiter(cfg.HTTP.RateProxySecret),
		campusCache:       newCampusResponseCacheWithShared(ctx, cfg.App.Timezone, shared),
	}
	if pool != nil && cfg.Upstream.GlobalQPS > 0 && cfg.Upstream.GlobalConcurrency > 0 {
		gate, err := storage.NewUpstreamRequestGate(pool, cfg.Upstream.GlobalQPS, cfg.Upstream.GlobalConcurrency)
		if err != nil {
			logger.Error("configure shared upstream request gate", "error", err)
		} else {
			s.requestGate = gate
		}
	}
	/* 未配置 SETTINGS_ENCRYPTION_KEY 不是致命错误。
	   面板仍可查看与修改非敏感设置。写凭证会被明确拒绝。
	   禁止静默把 API key 明文存进库。 */
	box, err := secrets.New(cfg.HTTP.SettingsKey)
	if err != nil {
		logger.Error("configure settings encryption", "error", err)
		box, _ = secrets.New("")
	}
	s.secrets = box
	/* 第三方登录凭证可在面板填写。环境变量退化为兜底。
	   仅当面板字段留空时使用环境变量。未配加密密钥时面板只能读凭证。 */
	s.oauthSettings = oauth.NewService(oauth.Fallback{
		BaseURL:            cfg.OAuth.BaseURL,
		GoogleClientID:     cfg.OAuth.Google.ClientID,
		GoogleClientSecret: cfg.OAuth.Google.ClientSecret,
		GitHubClientID:     cfg.OAuth.GitHub.ClientID,
		GitHubClientSecret: cfg.OAuth.GitHub.ClientSecret,
	}, pool, box)
	s.captcha = captcha.New(captcha.Config{
		Fallback: captcha.Settings{
			Provider: cfg.Captcha.Provider,
			SiteKey:  cfg.Captcha.SiteKey,
			Hostname: cfg.Captcha.Hostname,
			Actions:  cfg.Captcha.Actions,
		},
		TurnstileSecret:    cfg.Captcha.SecretKey,
		TurnstileVerifyURL: cfg.Captcha.TurnstileVerifyURL,
	}, pool, box)
	if cfg.Auth.JWTSecret != "" {
		manager, err := authn.NewManager(cfg.Auth)
		if err != nil {
			logger.Error("configure user authentication", "error", err)
		} else {
			s.tokens = manager
			s.dummyPHC, _ = authn.HashPassword("not-a-real-user-password")
		}
	}
	/* 闸门先按 .env 创建，再用面板已存值覆盖。
	   否则每次发布会把面板共享上限静默退回环境变量默认值。 */
	if s.requestGate != nil {
		if settings, err := s.effectiveScannerSettings(ctx); err != nil {
			logger.Error("load shared upstream gate settings", "error", err)
		} else if err := s.requestGate.SetLimits(settings.Shared.QPS, settings.Shared.Concurrency); err != nil {
			logger.Error("apply shared upstream gate", "error", err)
		}
	}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler { return s.securityHeaders(s.requestID(s.mux)) }

func (s *Server) acquireUpstreamRequest(ctx context.Context) (func(), error) {
	if s.requestGate == nil {
		return func() {}, nil
	}
	return s.requestGate.Acquire(ctx)
}

func (s *Server) StopAndWait() {
	s.scanMu.Lock()
	s.stopping = true
	s.scanMu.Unlock()
	s.scanWG.Wait()
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /health/live", s.live)
	s.mux.HandleFunc("GET /health/ready", s.ready)
	s.mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	s.mux.HandleFunc("GET /admin/", s.admin)
	s.mux.HandleFunc("GET /openapi.yaml", s.openAPISpec)
	s.mux.HandleFunc("GET /api/v1/frontend-config", s.frontendConfig)
	s.mux.HandleFunc("GET /api/v1/captcha/config", s.publicCaptchaConfig)
	s.mux.Handle("POST /api/v1/auth/register/code", s.authGate.limit(s.requestRegisterCode))
	s.mux.Handle("POST /api/v1/auth/register", s.expensiveAuthGate.limit(s.authGate.limit(s.registerUser).ServeHTTP))
	s.mux.Handle("POST /api/v1/auth/login", s.expensiveAuthGate.limit(s.authGate.limit(s.loginUser).ServeHTTP))
	s.mux.Handle("POST /api/v1/auth/password/forgot", s.expensiveAuthGate.limit(s.authGate.limit(s.requestPasswordReset).ServeHTTP))
	s.mux.Handle("POST /api/v1/auth/password/reset", s.expensiveAuthGate.limit(s.authGate.limit(s.resetPassword).ServeHTTP))
	/* 第三方登录是整页导航，不是 fetch。失败时 302 回站点并带原因码。
	   callback 走 expensiveAuthGate。每次要向提供方发多个请求，并可建号。
	   成本高于一次密码校验。 */
	s.mux.Handle("GET /api/v1/auth/oauth/{provider}/start", s.authGate.limit(s.startOAuth))
	s.mux.Handle("GET /api/v1/auth/oauth/{provider}/callback",
		s.expensiveAuthGate.limit(s.authGate.limit(s.completeOAuth).ServeHTTP))
	s.mux.Handle("POST /api/v1/auth/refresh", s.authGate.limit(s.refreshUserToken))
	s.mux.Handle("POST /api/v1/auth/logout", s.authGate.limit(s.logoutUser))
	s.userProtected("GET", "/api/v1/me", s.currentUser)
	s.userProtected("DELETE", "/api/v1/me", s.deleteCurrentUser)
	s.userProtected("PUT", "/api/v1/me/profile", s.updateCurrentUserProfile)
	s.userProtected("PUT", "/api/v1/me/password", s.changeCurrentUserPassword)
	s.userProtected("POST", "/api/v1/me/email/code", s.requestEmailChangeCode)
	s.userProtected("PUT", "/api/v1/me/email", s.changeCurrentUserEmail)
	s.userProtected("POST", "/api/v1/me/sessions/revoke", s.revokeCurrentUserSessions)
	s.userProtected("PUT", "/api/v1/me/meter", s.rebindCurrentUserMeter)
	s.userProtected("GET", "/api/v1/me/meter/preview", s.previewMeterForBinding)
	s.userProtected("GET", "/api/v1/me/overview", s.currentUserMeterOverview)
	s.userProtected("POST", "/api/v1/me/refresh", s.refreshCurrentUserMeter)
	s.userProtected("GET", "/api/v1/me/series", s.currentUserMeterSeries)
	s.userProtected("GET", "/api/v1/me/day-night", s.currentUserMeterDayNight)
	s.userProtected("GET", "/api/v1/me/bills", s.currentUserMeterBills)
	s.userProtected("GET", "/api/v1/me/leaderboard", s.currentUserLeaderboard)
	s.userProtected("PUT", "/api/v1/me/leaderboard", s.updateCurrentUserLeaderboard)
	s.userProtected("GET", "/api/v1/me/channels", s.listCurrentUserChannels)
	s.userProtected("PUT", "/api/v1/me/channels", s.replaceCurrentUserChannels)
	s.userProtected("POST", "/api/v1/me/channels/mail/recipient/code", s.requestMailRecipientCode)
	s.userProtected("POST", "/api/v1/me/channels/mail/recipient/verify", s.verifyMailRecipient)
	s.userProtected("PUT", "/api/v1/me/channels/{channel}", s.updateCurrentUserChannel)
	s.userProtected("POST", "/api/v1/me/channels/{channel}/test", s.testCurrentUserChannel)
	s.userProtected("GET", "/api/v1/me/notification-settings", s.getCurrentUserNotificationSettings)
	s.userProtected("PUT", "/api/v1/me/notification-settings", s.replaceCurrentUserNotificationSettings)
	s.userProtected("GET", "/api/v1/me/push-logs", s.listCurrentUserPushLogs)

	s.operatorRoute("GET", "/api/v1/admin/overview", s.adminOverview)
	s.operatorRoute("GET", "/api/v1/admin/scan-runs", s.listScanRuns)
	s.operatorRoute("POST", "/api/v1/admin/scan-runs", s.createScanRun)
	s.operatorRoute("GET", "/api/v1/admin/scan-runs/{run_id}", s.getScanRun)
	s.operatorRoute("GET", "/api/v1/admin/scan-runs/{run_id}/results", s.listScanResults)
	s.operatorRoute("POST", "/api/v1/admin/scan-runs/{run_id}/retry", s.retryScanRun)
	s.operatorRoute("POST", "/api/v1/admin/scan-runs/{run_id}/cancel", s.cancelScanRun)
	s.operatorRoute("GET", "/api/v1/admin/bill-runs", s.listBillRuns)
	s.operatorRoute("POST", "/api/v1/admin/bill-runs", s.createBillRun)
	s.operatorRoute("GET", "/api/v1/admin/bill-runs/{run_id}", s.getBillRun)
	s.operatorRoute("GET", "/api/v1/admin/bill-runs/{run_id}/results", s.listBillResults)
	s.operatorRoute("POST", "/api/v1/admin/bill-runs/{run_id}/retry", s.retryBillRun)
	s.operatorRoute("POST", "/api/v1/admin/bill-runs/{run_id}/cancel", s.cancelBillRun)
	s.operatorRoute("GET", "/api/v1/admin/daily-detail-runs", s.listDailyDetailRuns)
	s.operatorRoute("POST", "/api/v1/admin/daily-detail-runs", s.createDailyDetailRun)
	s.operatorRoute("GET", "/api/v1/admin/daily-detail-runs/{run_id}", s.getDailyDetailRun)
	s.operatorRoute("GET", "/api/v1/admin/daily-detail-runs/{run_id}/results", s.listDailyDetailResults)
	s.operatorRoute("POST", "/api/v1/admin/daily-detail-runs/{run_id}/retry", s.retryDailyDetailRun)
	s.operatorRoute("POST", "/api/v1/admin/daily-detail-runs/{run_id}/cancel", s.cancelDailyDetailRun)
	s.operatorRoute("GET", "/api/v1/admin/bill-revisions", s.listBillRevisions)
	s.operatorRoute("PATCH", "/api/v1/admin/bill-revisions/{revision_id}", s.acknowledgeBillRevision)
	s.operatorRoute("GET", "/api/v1/admin/anomalies", s.listAnomalies)
	s.operatorRoute("PATCH", "/api/v1/admin/anomalies/{anomaly_id}", s.acknowledgeAnomaly)
	s.operatorRoute("GET", "/api/v1/admin/inventory/imports", s.listInventoryImports)
	s.operatorRoute("POST", "/api/v1/admin/inventory/imports", s.applyInventoryImport)
	s.operatorRoute("POST", "/api/v1/admin/inventory/imports/validate", s.validateInventoryImport)
	s.operatorRoute("GET", "/api/v1/inventory/tree", s.inventoryTree)
	s.operatorRoute("GET", "/api/v1/meters/{meter}/overview", s.meterOverview)
	s.operatorRoute("POST", "/api/v1/meters/{meter}/refresh", s.refreshOperatorMeter)
	s.operatorRoute("GET", "/api/v1/meters/{meter}/series", s.meterSeries)
	s.operatorRoute("GET", "/api/v1/meters/{meter}/bills", s.meterBills)
	/* 校园聚合数据对匿名开放。不登录也可看全校用电与排行榜。
	   登录主要为查看本人电表。排行榜位置由存储层按偏好脱敏。
	   明文位置仅返回给有效 access token 对应的本人。 */
	s.publicCampusRoute("/api/v1/campus/scopes", s.campusScopes, nil)
	s.publicCampusRoute("/api/v1/campus/summary", s.campusSummary, []string{"from", "to", "building", "floor"})
	s.publicCampusRoute("/api/v1/campus/series", s.campusSeries, []string{"from", "to", "granularity", "building", "floor"})
	s.publicCampusRoute("/api/v1/campus/breakdown", s.campusBreakdown, []string{"source", "from", "to", "from_month", "to_month", "granularity", "building"})
	s.publicCampusRoute("/api/v1/campus/bills", s.campusBills, []string{"from_month", "to_month", "building"})
	// 排行榜已有按查看者隔离的缓存。共享公开缓存禁止混入 self 字段。
	s.mux.Handle("GET /api/v1/campus/rankings", s.campusGate.limitWithMessage(s.campusRankings, "too many campus requests"))
	s.publicCampusRoute("/api/v1/campus/hourly-heatmap", s.hourlyHeatmap, nil)
	s.operatorRoute("POST", "/api/v1/admin/mail/test", s.sendTestMail)
	s.operatorRoute("PUT", "/api/v1/admin/frontend-config", s.updateFrontendConfig)

	/* ---- Web 管理面板 ----
	   运维级：看板、扫描控制、渠道与前端配置。
	   管理员级：改角色、改系统凭证。能授权者本身等同管理员。 */
	s.operatorRoute("GET", "/api/v1/admin/session", s.adminSession)
	s.operatorRoute("GET", "/api/v1/admin/audit", s.listAdminAudit)
	s.operatorRoute("GET", "/api/v1/admin/settings/school", s.getSchoolSettings)
	s.operatorRoute("PUT", "/api/v1/admin/settings/school", s.putSchoolSettings)
	s.operatorRoute("GET", "/api/v1/admin/settings/scanner", s.getScannerSettings)
	s.operatorRoute("PUT", "/api/v1/admin/settings/scanner", s.putScannerSettings)
	s.operatorRoute("GET", "/api/v1/admin/users", s.listAdminUsers)
	s.adminRoute("PATCH", "/api/v1/admin/users/{user_id}", s.updateAdminUser)
	s.adminRoute("DELETE", "/api/v1/admin/users/{user_id}", s.deleteAdminUser)
	s.adminRoute("DELETE", "/api/v1/admin/users/{user_id}/meter", s.unbindAdminUserMeter)
	s.adminRoute("POST", "/api/v1/admin/users/{user_id}/sessions/revoke", s.revokeAdminUserSessions)
	// 自举第一个管理员。库中无 admin 时会话通道进不来，只能凭 ADMIN_TOKEN。
	s.operatorRoute("POST", "/api/v1/admin/users/promote", s.promoteAdminUser)
	s.adminRoute("GET", "/api/v1/admin/settings/mail", s.getMailSettings)
	s.adminRoute("PUT", "/api/v1/admin/settings/mail", s.putMailSettings)
	s.adminRoute("POST", "/api/v1/admin/settings/mail/test", s.postMailSettingsTest)
	// 第三方登录 client secret 与邮件、人机验证凭证同级。仅 admin 可读写。
	s.adminRoute("GET", "/api/v1/admin/settings/oauth", s.getOAuthSettings)
	s.adminRoute("PUT", "/api/v1/admin/settings/oauth", s.putOAuthSettings)
	s.adminRoute("GET", "/api/v1/admin/settings/captcha", s.getCaptchaSettings)
	s.adminRoute("PUT", "/api/v1/admin/settings/captcha", s.putCaptchaSettings)
	s.adminRoute("POST", "/api/v1/admin/settings/captcha/test", s.postCaptchaSettingsTest)
}

func (s *Server) publicCampusRoute(pattern string, handler http.HandlerFunc, queryKeys []string) {
	s.mux.Handle("GET "+pattern, s.campusGate.limitWithMessage(s.campusCache.wrap(handler, queryKeys), "too many campus requests"))
}

func (s *Server) userProtected(method, pattern string, handler http.HandlerFunc) {
	s.mux.Handle(method+" "+pattern, s.userAuth(handler))
}

type contextKey string

const requestIDKey contextKey = "request_id"
const userIDKey contextKey = "user_id"

func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

func newRequestID() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(raw[:])
}

// adminTokenMatches 判断请求是否凭 ADMIN_TOKEN 进入。
// 唯一消费者是 adminAuth。token 通道视同 admin。会话通道才查角色。
func (s *Server) adminTokenMatches(r *http.Request) bool {
	provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	expected := s.cfg.HTTP.AdminToken
	return expected != "" && len(provided) == len(expected) &&
		subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func (s *Server) live(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": releaseVersion, "time": time.Now().UTC()})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	cacheStatus := "disabled"
	if s.sharedCache != nil {
		cacheStatus = "ready"
		if err := s.sharedCache.Ping(ctx); err != nil {
			cacheStatus = "unavailable"
		}
	}
	if err := s.pool.Ping(ctx); err != nil {
		s.writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "degraded", "version": releaseVersion, "database": "unavailable", "migrations": "unknown", "worker": "unavailable", "cache": cacheStatus})
		return
	}
	version, err := storage.MigrationVersion(ctx, s.pool)
	if err != nil || version < 1 {
		s.writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "degraded", "version": releaseVersion, "database": "ready", "migrations": "missing", "worker": "unavailable", "cache": cacheStatus})
		return
	}
	worker, err := storage.WorkerState(ctx, s.pool, 3*time.Minute)
	if err != nil {
		worker = "unavailable"
	}
	status := "ready"
	if worker != "ready" {
		status = "degraded"
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"status": status, "version": releaseVersion, "database": "ready", "migrations": "ready", "worker": worker, "cache": cacheStatus})
}

/*
admin 仅保留跳转。

	运维面板已迁到前端 /admin 路由。后端不再 embed 手写 HTML。
	老书签访问此处时重定向过去。禁止返回 404。
*/
func (s *Server) admin(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/admin", http.StatusMovedPermanently)
}

func (s *Server) openAPISpec(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSpace(os.Getenv("OPENAPI_FILE"))
	if path == "" {
		path = filepath.Join("api", "openapi.yaml")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		s.writeError(w, r, http.StatusNotFound, "not_found", "OpenAPI document is unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	_, _ = w.Write(data)
}

func (s *Server) adminOverview(w http.ResponseWriter, r *http.Request) {
	counters, err := storage.InventoryCounters(r.Context(), s.pool)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	runs, err := storage.ListScanRuns(r.Context(), s.pool, "", 1, 0)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	var latest *storage.ScanRunView
	if len(runs) > 0 {
		latest = &runs[0]
	}
	billRuns, err := storage.ListBillRuns(r.Context(), s.pool, "", 1, 0)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	var latestBill *storage.BillRunView
	if len(billRuns) > 0 {
		latestBill = &billRuns[0]
	}
	anomalies, criticalAnomalies, err := storage.UnacknowledgedAnomalyCounts(r.Context(), s.pool)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	billRevisions, err := storage.UnacknowledgedBillRevisionCount(r.Context(), s.pool)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	version, err := storage.MigrationVersion(r.Context(), s.pool)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	if version < 1 {
		s.writeError(w, r, http.StatusServiceUnavailable, "migrations_missing", "database migrations are not ready")
		return
	}
	workerHeartbeat, err := storage.WorkerHeartbeat(r.Context(), s.pool)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	lastAggregate, err := storage.LatestRollupAt(r.Context(), s.pool)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	worker, _ := storage.WorkerState(r.Context(), s.pool, 3*time.Minute)
	// 供应商可由面板配置。查询运行时状态，而非启动时环境变量。
	mailProvider := ""
	mailReady := s.mailReady(r.Context())
	if s.mailer != nil {
		mailProvider = s.mailer.ProviderName(r.Context())
	}
	if mailProvider == "" {
		mailProvider = "unconfigured"
	}
	control, err := s.effectiveScannerSettings(r.Context())
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	balanceCron, boundCron := "", ""
	if control.Balance.Enabled {
		balanceCron, boundCron = control.Balance.Cron, control.Balance.BoundCron
	}
	billCron := ""
	if control.Bills.Enabled {
		billCron = control.Bills.Cron
	}
	detailCron := ""
	if control.DailyDetails.Enabled {
		detailCron = control.DailyDetails.Cron
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"inventory": counters, "latest_run": latest, "latest_bill_run": latestBill,
		"unacknowledged_anomalies": anomalies, "unacknowledged_critical_anomalies": criticalAnomalies,
		"unacknowledged_bill_revisions": billRevisions,
		"version":                       releaseVersion, "database": "ready", "migrations": "ready",
		"worker": worker, "worker_heartbeat_at": workerHeartbeat,
		"last_aggregate_at": lastAggregate,
		"mail_provider":     mailProvider, "mail_configured": mailReady,
		"scan_schedule": map[string]any{
			"cron": balanceCron, "bound_cron": boundCron,
			"qps": control.Balance.QPS, "concurrency": control.Balance.Concurrency,
		},
		"bill_schedule": map[string]any{
			"cron": billCron, "qps": control.Bills.QPS, "concurrency": control.Bills.Concurrency,
			"mode": "visible_months",
		},
		"detail_schedule": map[string]any{
			"cron": detailCron, "qps": control.DailyDetails.QPS,
			"concurrency": control.DailyDetails.Concurrency,
		},
	})
}

func (s *Server) frontendConfig(w http.ResponseWriter, r *http.Request) {
	result, err := storage.GetFrontendConfig(r.Context(), s.pool)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	result.OAuth = s.oauthAvailability(r.Context())
	w.Header().Set("Cache-Control", "public, max-age=60")
	s.writeJSON(w, http.StatusOK, result)
}

/*
oauthAvailability 报告后端此刻是否有第三方登录凭证。

	面板开关管是否露出。本字段管是否可用。两端都为真才画按钮。
	读取失败时按没有返回并记日志。禁止拖垮整个前端配置端点。
*/
func (s *Server) oauthAvailability(ctx context.Context) storage.FrontendOAuth {
	effective, err := s.oauthSettings.Effective(ctx)
	if err != nil {
		s.logger.Error("load oauth settings", "error", err)
		return storage.FrontendOAuth{}
	}
	return storage.FrontendOAuth{
		Google: effective.Configured(oauth.ProviderGoogle),
		GitHub: effective.Configured(oauth.ProviderGitHub),
	}
}

func (s *Server) updateFrontendConfig(w http.ResponseWriter, r *http.Request) {
	var settings storage.FrontendConfigSettings
	if !s.decodeJSON(w, r, &settings) {
		return
	}
	if settings.Features.Channels == nil {
		settings.Features.Channels = map[string]bool{}
	}
	if err := validateFrontendConfig(settings); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_frontend_config", err.Error())
		return
	}
	result, err := storage.UpdateFrontendConfig(r.Context(), s.pool, settings)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	result.OAuth = s.oauthAvailability(r.Context())
	s.campusCache.invalidate()
	s.rankingCache.invalidate()
	s.writeJSON(w, http.StatusOK, result)
}

func validateFrontendConfig(settings storage.FrontendConfigSettings) error {
	auth := settings.Features.Auth
	if !auth.EmailLogin {
		return errors.New("email_login must remain enabled while no other account login method exists")
	}
	if auth.SMSLogin || auth.SMSCode {
		return errors.New("sms_login and sms_code cannot be enabled before SMS authentication is implemented")
	}
	// email_code 对应找回密码（auth/password/*）。已提供，允许开启。
	allowedChannels := map[string]bool{
		"mail": true, "sms": true, "dingtalk": true, "wecom": true,
		"wecom_webhook": true, "discord": true, "feishu": true, "lark": true,
		"pushplus": true, "mp": true, "qq": true, "napcat": true, "telegram": true,
		"whatsapp": true, "serverchan_turbo": true, "serverchan3": true,
		"webhook": true, "bark": true, "gotify": true,
	}
	for channel := range settings.Features.Channels {
		if !allowedChannels[channel] {
			return fmt.Errorf("unsupported channel %q", channel)
		}
	}
	for channel := range settings.Features.ChannelComingSoon {
		if !allowedChannels[channel] {
			return fmt.Errorf("unsupported channel %q", channel)
		}
	}
	seenOrder := make(map[string]bool, len(settings.Features.ChannelOrder))
	for _, channel := range settings.Features.ChannelOrder {
		if !allowedChannels[channel] {
			return fmt.Errorf("unsupported channel %q in channel_order", channel)
		}
		if seenOrder[channel] {
			return fmt.Errorf("duplicate channel %q in channel_order", channel)
		}
		seenOrder[channel] = true
	}
	for channel, visible := range settings.Features.Channels {
		if visible && !settings.Features.ChannelComingSoon[channel] && !storage.IsImplementedChannel(channel) {
			return fmt.Errorf("channel %q cannot be marked ready before delivery is implemented", channel)
		}
	}
	if err := validateChannelCategories(settings.Features.ChannelCategories, allowedChannels); err != nil {
		return err
	}
	if refresh := strings.TrimSpace(settings.Display.RankingRefreshTime); refresh != "" && !rankingRefreshTimePattern.MatchString(refresh) {
		return errors.New("ranking_refresh_time must be HH:MM")
	}
	if strings.TrimSpace(settings.Display.CampusName) == "" || len(settings.Display.CampusName) > 120 {
		return errors.New("campus_name must contain 1 to 120 characters")
	}
	/* 学校名允许留空。多校部署不必在抬头挂某一校名。
	   必填会迫使新部署随手编名且不再修改。 */
	if len(settings.Display.AreaName) > 120 {
		return errors.New("area_name must contain at most 120 characters")
	}
	if len(settings.Display.BrandName) > 120 {
		return errors.New("brand_name must contain at most 120 characters")
	}
	if settings.Display.ElectricityRate != nil {
		rate := strings.TrimSpace(*settings.Display.ElectricityRate)
		if !electricityRatePattern.MatchString(rate) || rate == "0" || strings.Trim(rate, "0.") == "" {
			return errors.New("electricity_rate must be a positive decimal with at most four fractional digits")
		}
	}
	if settings.Display.EmptyRoomThresholdKWH != nil {
		threshold := strings.TrimSpace(*settings.Display.EmptyRoomThresholdKWH)
		value, err := strconv.ParseFloat(threshold, 64)
		if !electricityRatePattern.MatchString(threshold) || err != nil || value <= 0 || value > 10 {
			return errors.New("empty_room_threshold_kwh must be a decimal greater than 0 and at most 10, with at most four fractional digits")
		}
	}
	return validateSemesters(settings.Display.Semesters)
}

/*
渠道分类校验。

	分类只影响用户端排版。规则仅拦截会破坏页面的输入。
	重复 ID、渠道跨组、空标题均拒绝。文案长度设上限。
*/
const (
	maxChannelCategories   = 12
	maxChannelCategoryName = 40
	maxChannelCategoryDesc = 160
)

func validateChannelCategories(categories []storage.FrontendChannelCategory, allowedChannels map[string]bool) error {
	if len(categories) > maxChannelCategories {
		return fmt.Errorf("at most %d channel categories can be configured", maxChannelCategories)
	}
	seenID := make(map[string]bool, len(categories))
	seenChannel := make(map[string]bool, len(allowedChannels))
	for _, category := range categories {
		id := strings.TrimSpace(category.ID)
		if !channelCategoryIDPattern.MatchString(id) {
			return fmt.Errorf("channel category id %q must be 1 to 32 characters of a-z, 0-9, _ or -", category.ID)
		}
		if seenID[id] {
			return fmt.Errorf("duplicate channel category id %q", id)
		}
		seenID[id] = true
		name := strings.TrimSpace(category.Name)
		if name == "" || utf8.RuneCountInString(name) > maxChannelCategoryName {
			return fmt.Errorf("channel category %q must have a name of 1 to %d characters", id, maxChannelCategoryName)
		}
		if utf8.RuneCountInString(strings.TrimSpace(category.EN)) > maxChannelCategoryName {
			return fmt.Errorf("channel category %q label must be at most %d characters", id, maxChannelCategoryName)
		}
		if utf8.RuneCountInString(strings.TrimSpace(category.Desc)) > maxChannelCategoryDesc {
			return fmt.Errorf("channel category %q description must be at most %d characters", id, maxChannelCategoryDesc)
		}
		for _, channel := range category.Channels {
			if !allowedChannels[channel] {
				return fmt.Errorf("unsupported channel %q in category %q", channel, id)
			}
			if seenChannel[channel] {
				return fmt.Errorf("channel %q belongs to more than one category", channel)
			}
			seenChannel[channel] = true
		}
	}
	return nil
}

/*
学期配置校验。

	界面第 N 周依赖这些日期。错误记录不会立刻报错，但会弄坏横轴。
	写库前拦截：键与开学月一致、日期合法、区间正且不重叠。
*/
const (
	maxSemesters    = 40
	maxSemesterDays = 300
)

func validateSemesters(items []storage.FrontendSemester) error {
	if len(items) > maxSemesters {
		return fmt.Errorf("at most %d semesters can be configured", maxSemesters)
	}
	seen := map[string]bool{}
	type span struct {
		key        string
		start, end time.Time
	}
	spans := make([]span, 0, len(items))
	for _, item := range items {
		if !semesterKeyPattern.MatchString(item.Key) {
			return fmt.Errorf("semester key %q must look like YYYY-MM", item.Key)
		}
		if seen[item.Key] {
			return fmt.Errorf("duplicate semester %q", item.Key)
		}
		seen[item.Key] = true
		start, err := time.ParseInLocation(time.DateOnly, item.Start, time.UTC)
		if err != nil {
			return fmt.Errorf("semester %q start must be a YYYY-MM-DD date", item.Key)
		}
		end, err := time.ParseInLocation(time.DateOnly, item.End, time.UTC)
		if err != nil {
			return fmt.Errorf("semester %q end must be a YYYY-MM-DD date", item.Key)
		}
		if !end.After(start) {
			return fmt.Errorf("semester %q must end after it starts", item.Key)
		}
		if days := int(end.Sub(start).Hours()/24) + 1; days < 7 || days > maxSemesterDays {
			return fmt.Errorf("semester %q must span 7 to %d days", item.Key, maxSemesterDays)
		}
		// 键即开学年月。下拉写 2026-03 时，图必须从 3 月那天开始。
		if start.Format("2006-01") != item.Key {
			return fmt.Errorf("semester %q must start inside its own month", item.Key)
		}
		spans = append(spans, span{key: item.Key, start: start, end: end})
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start.Before(spans[j].start) })
	for i := 1; i < len(spans); i++ {
		if !spans[i].start.After(spans[i-1].end) {
			return fmt.Errorf("semesters %q and %q overlap", spans[i-1].key, spans[i].key)
		}
	}
	return nil
}

func (s *Server) listScanRuns(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := pagination(r)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_pagination", err.Error())
		return
	}
	status := r.URL.Query().Get("status")
	items, err := storage.ListScanRuns(r.Context(), s.pool, status, limit+1, offset)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	next := nextCursor(&items, limit, offset)
	s.writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (s *Server) getScanRun(w http.ResponseWriter, r *http.Request) {
	item, err := storage.GetScanRun(r.Context(), s.pool, r.PathValue("run_id"))
	if err != nil {
		s.handleNotFound(w, r, err, "scan run")
		return
	}
	s.writeJSON(w, http.StatusOK, item)
}

type createScanRequest struct {
	Limit       *int     `json:"limit"`
	Full        bool     `json:"full"`
	Building    *string  `json:"building"`
	Floor       *string  `json:"floor"`
	QPS         *float64 `json:"qps"`
	Concurrency *int     `json:"concurrency"`
	DryRun      bool     `json:"dry_run"`
}

func (s *Server) createScanRun(w http.ResponseWriter, r *http.Request) {
	var request createScanRequest
	if !s.decodeJSON(w, r, &request) {
		return
	}
	if request.Full && request.Limit != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_scan_scope", "full and limit cannot be combined")
		return
	}
	if !request.Full && request.Limit == nil && !request.DryRun {
		s.writeError(w, r, http.StatusBadRequest, "explicit_scope_required", "set a positive limit or explicitly set full=true")
		return
	}
	scope := storage.ScanScope{Building: stringValue(request.Building), Floor: stringValue(request.Floor)}
	if request.Limit != nil {
		if *request.Limit < 1 {
			s.writeError(w, r, http.StatusBadRequest, "invalid_limit", "limit must be positive")
			return
		}
		scope.Limit = *request.Limit
	}
	control, err := s.effectiveScannerSettings(r.Context())
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	qps, concurrency := control.Balance.QPS, control.Balance.Concurrency
	if request.QPS != nil {
		qps = *request.QPS
	}
	if request.Concurrency != nil {
		concurrency = *request.Concurrency
	}
	if qps <= 0 || concurrency < 1 {
		s.writeError(w, r, http.StatusBadRequest, "invalid_scan_settings", "qps and concurrency must be positive")
		return
	}
	if qps > s.cfg.Scan.MaxQPS || concurrency > s.cfg.Scan.MaxConcurrency {
		s.writeError(w, r, http.StatusBadRequest, "scan_safety_limit", fmt.Sprintf(
			"qps must be <= %g and concurrency must be <= %d", s.cfg.Scan.MaxQPS, s.cfg.Scan.MaxConcurrency,
		))
		return
	}
	if request.DryRun {
		counts, err := storage.CountScanScope(r.Context(), s.pool, scope)
		if err != nil {
			s.databaseError(w, r, err)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"dry_run": true, "counters": counts, "qps": qps, "concurrency": concurrency})
		return
	}
	runSettings := s.settings(qps, concurrency, control.Balance.RetryMax)
	runID, err := storage.CreateScanRun(r.Context(), s.pool, "manual", nil, scope, runSettings)
	if err != nil {
		if errors.Is(err, storage.ErrScanAlreadyRunning) {
			s.writeError(w, r, http.StatusConflict, "scan_already_running", err.Error())
			return
		}
		s.databaseError(w, r, err)
		return
	}
	item, err := storage.GetScanRun(r.Context(), s.pool, runID)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.launchScan(runID, qps, concurrency, control.Balance.RetryMax)
	s.writeJSON(w, http.StatusAccepted, item)
}

func (s *Server) listScanResults(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := pagination(r)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_pagination", err.Error())
		return
	}
	runID := r.PathValue("run_id")
	if _, err := storage.GetScanRun(r.Context(), s.pool, runID); err != nil {
		s.handleNotFound(w, r, err, "scan run")
		return
	}
	items, err := storage.ListScanResults(r.Context(), s.pool, runID, r.URL.Query().Get("status"), limit+1, offset)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	next := nextCursor(&items, limit, offset)
	s.writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (s *Server) retryScanRun(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Statuses []string `json:"statuses"`
	}
	if !s.decodeJSON(w, r, &request) {
		return
	}
	allowed := map[string]bool{"stale": true, "empty": true, "error": true, "parse_error": true, "canceled": true}
	if len(request.Statuses) == 0 {
		s.writeError(w, r, http.StatusBadRequest, "invalid_retry_status", "at least one failed status is required")
		return
	}
	for _, status := range request.Statuses {
		if !allowed[status] {
			s.writeError(w, r, http.StatusBadRequest, "invalid_retry_status", "only stale, empty, error, parse_error, and canceled may be retried")
			return
		}
	}
	control, err := s.effectiveScannerSettings(r.Context())
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	if control.Balance.QPS <= 0 || control.Balance.QPS > s.cfg.Scan.MaxQPS ||
		control.Balance.Concurrency < 1 || control.Balance.Concurrency > s.cfg.Scan.MaxConcurrency {
		s.writeError(w, r, http.StatusBadRequest, "scan_safety_limit", "saved balance settings are outside the server safety limit")
		return
	}
	runID, err := storage.CreateRetryScanRun(r.Context(), s.pool, r.PathValue("run_id"), request.Statuses,
		s.settings(control.Balance.QPS, control.Balance.Concurrency, control.Balance.RetryMax))
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrScanAlreadyRunning):
			s.writeError(w, r, http.StatusConflict, "scan_already_running", err.Error())
		case errors.Is(err, pgx.ErrNoRows):
			s.writeError(w, r, http.StatusNotFound, "not_found", "scan run not found")
		default:
			s.writeError(w, r, http.StatusBadRequest, "retry_unavailable", err.Error())
		}
		return
	}
	item, err := storage.GetScanRun(r.Context(), s.pool, runID)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.launchScan(runID, control.Balance.QPS, control.Balance.Concurrency, control.Balance.RetryMax)
	s.writeJSON(w, http.StatusAccepted, item)
}

type createBillRunRequest struct {
	Limit       *int     `json:"limit"`
	Full        bool     `json:"full"`
	Building    *string  `json:"building"`
	Floor       *string  `json:"floor"`
	QPS         *float64 `json:"qps"`
	Concurrency *int     `json:"concurrency"`
	DryRun      bool     `json:"dry_run"`
}

func (s *Server) listBillRuns(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := pagination(r)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_pagination", err.Error())
		return
	}
	items, err := storage.ListBillRuns(r.Context(), s.pool, r.URL.Query().Get("status"), limit+1, offset)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	next := nextCursor(&items, limit, offset)
	s.writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (s *Server) getBillRun(w http.ResponseWriter, r *http.Request) {
	item, err := storage.GetBillRun(r.Context(), s.pool, r.PathValue("run_id"))
	if err != nil {
		s.handleNotFound(w, r, err, "bill run")
		return
	}
	s.writeJSON(w, http.StatusOK, item)
}

func (s *Server) createBillRun(w http.ResponseWriter, r *http.Request) {
	var request createBillRunRequest
	if !s.decodeJSON(w, r, &request) {
		return
	}
	if request.Full && request.Limit != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_bill_scope", "full and limit cannot be combined")
		return
	}
	if !request.Full && request.Limit == nil && !request.DryRun {
		s.writeError(w, r, http.StatusBadRequest, "explicit_scope_required", "set a positive limit or explicitly set full=true")
		return
	}
	scope := storage.BillRunScope{Building: stringValue(request.Building), Floor: stringValue(request.Floor)}
	if request.Limit != nil {
		if *request.Limit < 1 {
			s.writeError(w, r, http.StatusBadRequest, "invalid_limit", "limit must be positive")
			return
		}
		scope.Limit = *request.Limit
	}
	control, err := s.effectiveScannerSettings(r.Context())
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	bill := control.Bills
	qps, concurrency := bill.QPS, bill.Concurrency
	if request.QPS != nil {
		qps = *request.QPS
	}
	if request.Concurrency != nil {
		concurrency = *request.Concurrency
	}
	if qps <= 0 || concurrency < 1 || qps > s.cfg.Bill.MaxQPS || concurrency > s.cfg.Bill.MaxConcurrency {
		s.writeError(w, r, http.StatusBadRequest, "bill_safety_limit", fmt.Sprintf("qps must be > 0 and <= %g; concurrency must be between 1 and %d", s.cfg.Bill.MaxQPS, s.cfg.Bill.MaxConcurrency))
		return
	}
	months := biller.VisibleMonths(time.Now(), s.cfg.App.Timezone)
	if request.DryRun {
		counts, err := storage.CountBillScope(r.Context(), s.pool, scope, len(months))
		if err != nil {
			s.databaseError(w, r, err)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{
			"dry_run": true, "counters": counts, "months": months,
			"month_requests": counts.EligibleTotal * len(months),
			"request_floor":  counts.EligibleTotal * (len(months) + 4), "qps": qps, "concurrency": concurrency,
		})
		return
	}
	runSettings := s.billRunStorageSettings(qps, concurrency, bill.RetryMax, bill.MonthRetryMax)
	runID, err := storage.CreateBillRun(r.Context(), s.pool, "manual", nil, months, scope, runSettings)
	if err != nil {
		if errors.Is(err, storage.ErrBillAlreadyRunning) {
			s.writeError(w, r, http.StatusConflict, "bill_already_running", err.Error())
			return
		}
		s.databaseError(w, r, err)
		return
	}
	item, err := storage.GetBillRun(r.Context(), s.pool, runID)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.launchBillRun(runID, runSettings)
	s.writeJSON(w, http.StatusAccepted, item)
}

func (s *Server) retryBillRun(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Statuses    []string `json:"statuses"`
		QPS         *float64 `json:"qps"`
		Concurrency *int     `json:"concurrency"`
	}
	if !s.decodeJSON(w, r, &request) {
		return
	}
	allowed := map[string]bool{"partial": true, "empty": true, "error": true, "canceled": true}
	if len(request.Statuses) == 0 {
		s.writeError(w, r, http.StatusBadRequest, "invalid_retry_status", "at least one failed status is required")
		return
	}
	for _, status := range request.Statuses {
		if !allowed[status] {
			s.writeError(w, r, http.StatusBadRequest, "invalid_retry_status", "only partial, empty, error, and canceled may be retried")
			return
		}
	}
	control, err := s.effectiveScannerSettings(r.Context())
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	bill := control.Bills
	qps, concurrency := bill.QPS, bill.Concurrency
	if request.QPS != nil {
		qps = *request.QPS
	}
	if request.Concurrency != nil {
		concurrency = *request.Concurrency
	}
	if qps <= 0 || concurrency < 1 || qps > s.cfg.Bill.MaxQPS || concurrency > s.cfg.Bill.MaxConcurrency {
		s.writeError(w, r, http.StatusBadRequest, "bill_safety_limit", fmt.Sprintf("qps must be > 0 and <= %g; concurrency must be between 1 and %d", s.cfg.Bill.MaxQPS, s.cfg.Bill.MaxConcurrency))
		return
	}
	runSettings := s.billRunStorageSettings(qps, concurrency, bill.RetryMax, bill.MonthRetryMax)
	runID, err := storage.CreateRetryBillRun(
		r.Context(), s.pool, r.PathValue("run_id"), request.Statuses,
		runSettings,
	)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrBillAlreadyRunning):
			s.writeError(w, r, http.StatusConflict, "bill_already_running", err.Error())
		case errors.Is(err, pgx.ErrNoRows):
			s.writeError(w, r, http.StatusNotFound, "not_found", "terminal bill run not found")
		default:
			s.writeError(w, r, http.StatusBadRequest, "retry_unavailable", err.Error())
		}
		return
	}
	item, err := storage.GetBillRun(r.Context(), s.pool, runID)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.launchBillRun(runID, runSettings)
	s.writeJSON(w, http.StatusAccepted, item)
}

func (s *Server) listBillResults(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := pagination(r)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_pagination", err.Error())
		return
	}
	runID := r.PathValue("run_id")
	if _, err := storage.GetBillRun(r.Context(), s.pool, runID); err != nil {
		s.handleNotFound(w, r, err, "bill run")
		return
	}
	items, err := storage.ListBillResults(r.Context(), s.pool, runID, r.URL.Query().Get("status"), limit+1, offset)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	next := nextCursor(&items, limit, offset)
	s.writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

type createDailyDetailRunRequest struct {
	Limit          *int     `json:"limit"`
	Full           bool     `json:"full"`
	Building       *string  `json:"building"`
	Floor          *string  `json:"floor"`
	QPS            *float64 `json:"qps"`
	Concurrency    *int     `json:"concurrency"`
	Initialization bool     `json:"initialization"`
	DryRun         bool     `json:"dry_run"`
}

func (s *Server) listDailyDetailRuns(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := pagination(r)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_pagination", err.Error())
		return
	}
	items, err := storage.ListDailyDetailRuns(r.Context(), s.pool, r.URL.Query().Get("status"), limit+1, offset)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	next := nextCursor(&items, limit, offset)
	s.writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (s *Server) getDailyDetailRun(w http.ResponseWriter, r *http.Request) {
	item, err := storage.GetDailyDetailRun(r.Context(), s.pool, r.PathValue("run_id"))
	if err != nil {
		s.handleNotFound(w, r, err, "daily detail run")
		return
	}
	s.writeJSON(w, http.StatusOK, item)
}

func (s *Server) createDailyDetailRun(w http.ResponseWriter, r *http.Request) {
	var request createDailyDetailRunRequest
	if !s.decodeJSON(w, r, &request) {
		return
	}
	if request.Full && request.Limit != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_daily_detail_scope", "full and limit cannot be combined")
		return
	}
	if !request.Full && request.Limit == nil && !request.DryRun {
		s.writeError(w, r, http.StatusBadRequest, "explicit_scope_required", "set a positive limit or explicitly set full=true")
		return
	}
	scope := storage.DailyDetailRunScope{Building: stringValue(request.Building), Floor: stringValue(request.Floor)}
	if request.Limit != nil {
		if *request.Limit < 1 {
			s.writeError(w, r, http.StatusBadRequest, "invalid_limit", "limit must be positive")
			return
		}
		scope.Limit = *request.Limit
	}
	control, err := s.effectiveScannerSettings(r.Context())
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	detail := control.DailyDetails
	qps, concurrency := detail.QPS, detail.Concurrency
	if request.QPS != nil {
		qps = *request.QPS
	}
	if request.Concurrency != nil {
		concurrency = *request.Concurrency
	}
	if qps <= 0 || qps > s.cfg.Detail.MaxQPS || concurrency < 1 || concurrency > s.cfg.Detail.MaxConcurrency {
		s.writeError(w, r, http.StatusBadRequest, "daily_detail_safety_limit", fmt.Sprintf(
			"qps must be > 0 and <= %g; concurrency must be between 1 and %d",
			s.cfg.Detail.MaxQPS, s.cfg.Detail.MaxConcurrency,
		))
		return
	}
	months := detailer.IncrementalMonths(time.Now(), s.cfg.App.Timezone)
	if request.Initialization {
		months, err = detailer.BootstrapMonths(time.Now(), s.cfg.App.Timezone, detail.BootstrapFrom)
		if err != nil {
			s.writeError(w, r, http.StatusBadRequest, "invalid_bootstrap_range", err.Error())
			return
		}
	}
	if request.DryRun {
		counts, err := storage.CountDailyDetailScope(r.Context(), s.pool, scope)
		if err != nil {
			s.databaseError(w, r, err)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{
			"dry_run": true, "counters": counts, "months": months,
			"month_requests": counts.EligibleTotal * len(months), "qps": qps, "concurrency": concurrency,
		})
		return
	}
	settings := storage.DailyDetailRunSettings{
		QPS: qps, Concurrency: concurrency, RetryMax: detail.RetryMax,
		MonthRetryMax: detail.MonthRetryMax, TimeoutSeconds: int(s.cfg.Scan.Timeout.Seconds()),
		BootstrapFrom: detail.BootstrapFrom, Initialization: request.Initialization,
	}
	runID, err := storage.CreateDailyDetailRun(r.Context(), s.pool, "manual", nil, months, scope, settings)
	if err != nil {
		if errors.Is(err, storage.ErrDailyDetailAlreadyRunning) {
			s.writeError(w, r, http.StatusConflict, "daily_detail_already_running", err.Error())
			return
		}
		s.databaseError(w, r, err)
		return
	}
	item, err := storage.GetDailyDetailRun(r.Context(), s.pool, runID)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.launchDailyDetailRun(runID, settings)
	s.writeJSON(w, http.StatusAccepted, item)
}

func (s *Server) retryDailyDetailRun(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Statuses    []string `json:"statuses"`
		QPS         *float64 `json:"qps"`
		Concurrency *int     `json:"concurrency"`
	}
	if !s.decodeJSON(w, r, &request) {
		return
	}
	allowed := map[string]bool{"partial": true, "empty": true, "error": true, "canceled": true}
	if len(request.Statuses) == 0 {
		s.writeError(w, r, http.StatusBadRequest, "invalid_retry_status", "at least one failed status is required")
		return
	}
	for _, status := range request.Statuses {
		if !allowed[status] {
			s.writeError(w, r, http.StatusBadRequest, "invalid_retry_status", "only partial, empty, error, and canceled may be retried")
			return
		}
	}
	control, err := s.effectiveScannerSettings(r.Context())
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	detail := control.DailyDetails
	if request.QPS != nil {
		detail.QPS = *request.QPS
	}
	if request.Concurrency != nil {
		detail.Concurrency = *request.Concurrency
	}
	if detail.QPS <= 0 || detail.QPS > s.cfg.Detail.MaxQPS || detail.Concurrency < 1 || detail.Concurrency > s.cfg.Detail.MaxConcurrency {
		s.writeError(w, r, http.StatusBadRequest, "daily_detail_safety_limit", "daily detail qps or concurrency is outside the server safety limit")
		return
	}
	settings := storage.DailyDetailRunSettings{
		QPS: detail.QPS, Concurrency: detail.Concurrency, RetryMax: detail.RetryMax,
		MonthRetryMax: detail.MonthRetryMax, TimeoutSeconds: int(s.cfg.Scan.Timeout.Seconds()),
		BootstrapFrom: detail.BootstrapFrom,
	}
	runID, err := storage.CreateRetryDailyDetailRun(r.Context(), s.pool, r.PathValue("run_id"), request.Statuses, settings)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrDailyDetailAlreadyRunning):
			s.writeError(w, r, http.StatusConflict, "daily_detail_already_running", err.Error())
		case errors.Is(err, pgx.ErrNoRows):
			s.writeError(w, r, http.StatusNotFound, "not_found", "terminal daily detail run not found")
		default:
			s.writeError(w, r, http.StatusBadRequest, "retry_unavailable", err.Error())
		}
		return
	}
	item, err := storage.GetDailyDetailRun(r.Context(), s.pool, runID)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.launchDailyDetailRun(runID, settings)
	s.writeJSON(w, http.StatusAccepted, item)
}

func (s *Server) listDailyDetailResults(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := pagination(r)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_pagination", err.Error())
		return
	}
	runID := r.PathValue("run_id")
	if _, err := storage.GetDailyDetailRun(r.Context(), s.pool, runID); err != nil {
		s.handleNotFound(w, r, err, "daily detail run")
		return
	}
	items, err := storage.ListDailyDetailResults(r.Context(), s.pool, runID, r.URL.Query().Get("status"), limit+1, offset)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	next := nextCursor(&items, limit, offset)
	s.writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (s *Server) listBillRevisions(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := pagination(r)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_pagination", err.Error())
		return
	}
	var acknowledged *bool
	if raw := r.URL.Query().Get("acknowledged"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			s.writeError(w, r, http.StatusBadRequest, "invalid_acknowledged", "acknowledged must be a boolean")
			return
		}
		acknowledged = &value
	}
	items, err := storage.ListBillRevisions(r.Context(), s.pool, acknowledged, limit+1, offset)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	next := nextCursor(&items, limit, offset)
	s.writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (s *Server) acknowledgeBillRevision(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Acknowledged bool   `json:"acknowledged"`
		Note         string `json:"note"`
	}
	if !s.decodeJSON(w, r, &request) {
		return
	}
	if len(request.Note) > 1000 {
		s.writeError(w, r, http.StatusBadRequest, "note_too_long", "note must contain at most 1000 characters")
		return
	}
	item, err := storage.AcknowledgeBillRevision(r.Context(), s.pool, r.PathValue("revision_id"), request.Acknowledged, request.Note)
	if err != nil {
		s.handleNotFound(w, r, err, "bill revision")
		return
	}
	s.writeJSON(w, http.StatusOK, item)
}

func (s *Server) launchBillRun(runID string, settings storage.BillRunSettings) {
	s.scanMu.Lock()
	if s.stopping {
		s.scanMu.Unlock()
		return
	}
	s.scanWG.Add(1)
	s.scanMu.Unlock()
	go func() {
		defer s.scanWG.Done()
		runner, err := biller.New(s.upstream, biller.PostgreSQLRepository{Pool: s.pool}, biller.Config{
			Concurrency: settings.Concurrency, QPS: settings.QPS, AcquireRequest: s.acquireUpstreamRequest,
			RetryMax:      settings.RetryMax,
			MonthRetryMax: settings.MonthRetryMax, ProgressEvery: 25,
		}, s.logger)
		if err != nil {
			s.logger.Error("create bill runner", "run_id", runID, "error", err)
			return
		}
		counters, err := runner.Run(s.ctx, runID)
		if err != nil {
			if errors.Is(err, storage.ErrBillNotRunnable) {
				s.logger.Info("bill run was claimed by another process", "run_id", runID)
				return
			}
			if errors.Is(err, context.Canceled) {
				s.logger.Info("bill run checkpointed during API shutdown", "run_id", runID)
				return
			}
			s.logger.Error("bill run failed", "run_id", runID, "error", err)
			if s.mailReady(context.Background()) && s.cfg.Mail.AdminTo != "" {
				_, _ = s.mailer.SendWorkerFailure(context.Background(), s.cfg.Mail.AdminTo, "bill run "+runID+" failed: "+err.Error())
			}
			return
		}
		s.logger.Info("bill reconciliation completed", "run_id", runID, "counters", counters)
		s.campusCache.invalidate()
	}()
}

func (s *Server) launchDailyDetailRun(runID string, settings storage.DailyDetailRunSettings) {
	s.scanMu.Lock()
	if s.stopping {
		s.scanMu.Unlock()
		return
	}
	s.scanWG.Add(1)
	s.scanMu.Unlock()
	go func() {
		defer s.scanWG.Done()
		runner, err := detailer.New(s.upstream, detailer.PostgreSQLRepository{Pool: s.pool}, detailer.Config{
			Concurrency: settings.Concurrency, QPS: settings.QPS, AcquireRequest: s.acquireUpstreamRequest,
			RetryMax:      settings.RetryMax,
			MonthRetryMax: settings.MonthRetryMax, ProgressEvery: 25, Location: s.cfg.App.Timezone,
		}, s.logger)
		if err != nil {
			s.logger.Error("create daily detail runner", "run_id", runID, "error", err)
			return
		}
		counters, err := runner.Run(s.ctx, runID)
		if err != nil {
			if errors.Is(err, storage.ErrDailyDetailNotRunnable) {
				s.logger.Info("daily detail run was claimed by another process", "run_id", runID)
				return
			}
			if errors.Is(err, context.Canceled) {
				s.logger.Info("daily detail run checkpointed during API shutdown", "run_id", runID)
				return
			}
			s.logger.Error("daily detail run failed", "run_id", runID, "error", err)
			return
		}
		if err := storage.RefreshCampusRollup(context.Background(), s.pool); err != nil {
			s.logger.Error("refresh campus rollup", "run_id", runID, "error", err)
		}
		if err := storage.RefreshRollupsForDailyDetailRun(context.Background(), s.pool, runID); err != nil {
			s.logger.Error("refresh consumption rollups after daily detail", "run_id", runID, "error", err)
		}
		s.campusCache.invalidate()
		s.rankingCache.invalidate()
		s.logger.Info("daily detail collection completed", "run_id", runID, "counters", counters)
	}()
}

func (s *Server) billRunStorageSettings(qps float64, concurrency, retryMax, monthRetryMax int) storage.BillRunSettings {
	return storage.BillRunSettings{QPS: qps, Concurrency: concurrency, RetryMax: retryMax, MonthRetryMax: monthRetryMax, TimeoutSeconds: int(s.cfg.Scan.Timeout.Seconds())}
}

func (s *Server) launchScan(runID string, qps float64, concurrency, retryMax int) {
	s.scanMu.Lock()
	if s.stopping {
		s.scanMu.Unlock()
		return
	}
	s.scanWG.Add(1)
	s.scanMu.Unlock()
	go func() {
		defer s.scanWG.Done()
		runner, err := scanner.New(s.upstream, scanner.PostgreSQLRepository{Pool: s.pool}, scanner.Config{
			Concurrency: concurrency, QPS: qps, AcquireRequest: s.acquireUpstreamRequest, RetryMax: retryMax,
			BaseDelay: s.cfg.Scan.RetryBaseDelay, MaxDelay: s.cfg.Scan.RetryMaxDelay,
			StaleAfter: s.cfg.Scan.StaleAfter, Location: s.cfg.App.Timezone, ProgressEvery: 50,
		}, s.logger)
		if err != nil {
			s.logger.Error("create scan runner", "run_id", runID, "error", err)
			return
		}
		counters, err := runner.Run(s.ctx, runID)
		if err != nil {
			if errors.Is(err, storage.ErrScanNotRunnable) {
				s.logger.Info("scan was claimed by another worker", "run_id", runID)
				return
			}
			if errors.Is(err, context.Canceled) {
				s.logger.Info("scan checkpointed during API shutdown", "run_id", runID)
				return
			}
			s.logger.Error("scan run failed", "run_id", runID, "error", err)
			if s.mailReady(context.Background()) && s.cfg.Mail.AdminTo != "" {
				if _, mailErr := s.mailer.SendWorkerFailure(context.Background(), s.cfg.Mail.AdminTo, "scan "+runID+" failed: "+err.Error()); mailErr != nil {
					s.logger.Error("send scan failure notification", "run_id", runID, "error", mailErr)
				}
			}
			return
		}
		if err := storage.RefreshRollupsForRun(context.Background(), s.pool, runID); err != nil {
			s.logger.Error("refresh scan rollups", "run_id", runID, "error", err)
		}
		s.campusCache.invalidate()
		if s.mailReady(context.Background()) && s.cfg.Mail.AdminTo != "" {
			if _, mailErr := s.mailer.SendScanSummary(context.Background(), s.cfg.Mail.AdminTo, runID, "completed", counters); mailErr != nil {
				s.logger.Error("send scan summary", "run_id", runID, "error", mailErr)
			}
			critical, countErr := storage.CountOpenCriticalAnomaliesForRun(context.Background(), s.pool, runID)
			if countErr != nil {
				s.logger.Error("count critical scan anomalies", "run_id", runID, "error", countErr)
			} else if critical > 0 {
				if _, mailErr := s.mailer.SendAnomalySummary(context.Background(), s.cfg.Mail.AdminTo, runID, critical); mailErr != nil {
					s.logger.Error("send anomaly summary", "run_id", runID, "error", mailErr)
				}
			}
		}
	}()
}

func (s *Server) settings(qps float64, concurrency, retryMax int) storage.ScanSettings {
	return storage.ScanSettings{QPS: qps, Concurrency: concurrency, RetryMax: retryMax, TimeoutSeconds: int(s.cfg.Scan.Timeout.Seconds())}
}

func (s *Server) listAnomalies(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := pagination(r)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_pagination", err.Error())
		return
	}
	var acknowledged *bool
	if raw := r.URL.Query().Get("acknowledged"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			s.writeError(w, r, http.StatusBadRequest, "invalid_filter", "acknowledged must be true or false")
			return
		}
		acknowledged = &value
	}
	items, err := storage.ListAnomalies(r.Context(), s.pool, acknowledged, r.URL.Query().Get("type"), limit+1, offset)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	next := nextCursor(&items, limit, offset)
	s.writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (s *Server) acknowledgeAnomaly(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Acknowledged bool   `json:"acknowledged"`
		Note         string `json:"note"`
	}
	if !s.decodeJSON(w, r, &request) {
		return
	}
	if len(request.Note) > 1000 {
		s.writeError(w, r, http.StatusBadRequest, "note_too_long", "note must not exceed 1000 characters")
		return
	}
	item, err := storage.AcknowledgeAnomaly(r.Context(), s.pool, r.PathValue("anomaly_id"), request.Acknowledged, request.Note)
	if err != nil {
		s.handleNotFound(w, r, err, "anomaly")
		return
	}
	s.writeJSON(w, http.StatusOK, item)
}

func (s *Server) listInventoryImports(w http.ResponseWriter, r *http.Request) {
	items, err := storage.ListInventoryImports(r.Context(), s.pool)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, items)
}

func (s *Server) validateInventoryImport(w http.ResponseWriter, r *http.Request) {
	file, header, err := readUpload(r, "file")
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_upload", err.Error())
		return
	}
	defer file.Close()
	/* 校区名跟随面板配置。禁止写死校区名。
	   否则其它部署导库存会被打上错误学校标签。 */
	campus := storage.DefaultCampusName
	if view, cfgErr := storage.GetFrontendConfig(r.Context(), s.pool); cfgErr == nil {
		if name := strings.TrimSpace(view.Display.CampusName); name != "" {
			campus = name
		}
	}
	plan, err := importer.ValidateInventory(file, filepath.Base(header.Filename), s.cfg.Upstream.AreaID, campus, s.cfg.App.Timezone, importer.DefaultExcludedBuildings())
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_inventory", err.Error())
		return
	}
	result, err := storage.SaveInventoryValidation(r.Context(), s.pool, plan, 30*time.Minute)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, result)
}

func readUpload(r *http.Request, field string) (multipart.File, *multipart.FileHeader, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, 33<<20)
	if err := r.ParseMultipartForm(33 << 20); err != nil {
		return nil, nil, fmt.Errorf("parse multipart upload: %w", err)
	}
	return r.FormFile(field)
}

func (s *Server) applyInventoryImport(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ValidationID string `json:"validation_id"`
	}
	if !s.decodeJSON(w, r, &request) {
		return
	}
	if request.ValidationID == "" {
		s.writeError(w, r, http.StatusBadRequest, "validation_id_required", "validation_id is required")
		return
	}
	result, err := storage.ApplyInventoryValidation(r.Context(), s.pool, request.ValidationID)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrValidationExpired), errors.Is(err, storage.ErrValidationApplied):
			s.writeError(w, r, http.StatusConflict, "validation_unavailable", err.Error())
		case storage.IsNotFound(err):
			s.writeError(w, r, http.StatusNotFound, "not_found", "inventory validation not found")
		default:
			s.writeError(w, r, http.StatusBadRequest, "inventory_apply_failed", err.Error())
		}
		return
	}
	s.campusCache.invalidate()
	s.rankingCache.invalidate()
	s.writeJSON(w, http.StatusCreated, result)
}

func (s *Server) inventoryTree(w http.ResponseWriter, r *http.Request) {
	include, err := strconv.ParseBool(defaultString(r.URL.Query().Get("include_excluded"), "false"))
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_filter", "include_excluded must be true or false")
		return
	}
	tree, err := storage.GetInventoryTree(r.Context(), s.pool, include)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, tree)
}

func (s *Server) campusScopes(w http.ResponseWriter, r *http.Request) {
	scopes, err := storage.GetCampusScopes(r.Context(), s.pool)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, scopes)
}

func (s *Server) meterOverview(w http.ResponseWriter, r *http.Request) {
	result, err := storage.GetMeterOverview(r.Context(), s.pool, r.PathValue("meter"), time.Now())
	if err != nil {
		s.handleNotFound(w, r, err, "meter")
		return
	}
	s.writeJSON(w, http.StatusOK, result)
}

func (s *Server) meterSeries(w http.ResponseWriter, r *http.Request) {
	from, to, ok := s.timeRange(w, r)
	if !ok {
		return
	}
	metric := r.URL.Query().Get("metric")
	if metric != "balance" && metric != "consumption" {
		s.writeError(w, r, http.StatusBadRequest, "invalid_metric", "metric must be balance or consumption")
		return
	}
	granularity := r.URL.Query().Get("granularity")
	result, err := storage.GetSeries(r.Context(), s.pool, r.PathValue("meter"), metric, granularity, from, to, "", "")
	if err != nil {
		s.handleNotFound(w, r, err, "meter")
		return
	}
	s.writeJSON(w, http.StatusOK, result)
}

func (s *Server) meterBills(w http.ResponseWriter, r *http.Request) {
	items, err := storage.ListMeterBills(r.Context(), s.pool, r.PathValue("meter"), r.URL.Query().Get("from_month"), r.URL.Query().Get("to_month"))
	if err != nil {
		s.handleNotFound(w, r, err, "meter")
		return
	}
	s.writeJSON(w, http.StatusOK, items)
}

func (s *Server) campusSummary(w http.ResponseWriter, r *http.Request) {
	from, to, ok := s.campusTimeRange(w, r)
	if !ok {
		return
	}
	result, err := storage.GetCampusSummary(r.Context(), s.pool, from, to, r.URL.Query().Get("building"), r.URL.Query().Get("floor"))
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, result)
}

func (s *Server) campusSeries(w http.ResponseWriter, r *http.Request) {
	from, to, ok := s.campusTimeRange(w, r)
	if !ok {
		return
	}
	result, err := storage.GetSeries(r.Context(), s.pool, "", "consumption", r.URL.Query().Get("granularity"), from, to, r.URL.Query().Get("building"), r.URL.Query().Get("floor"))
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_series", err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, result)
}

/*
campusBreakdown 按楼栋或楼层与时间返回用电矩阵。

	用电构成用行合计。热力图用格子。两视图共用一次查询与口径。
	source=monthly_bill 时走月账单矩阵。需要 from_month / to_month。
*/
func (s *Server) campusBreakdown(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	building := q.Get("building")
	if q.Get("source") == "monthly_bill" {
		if !s.validCampusMonthRange(w, r, q.Get("from_month"), q.Get("to_month"), true) {
			return
		}
		result, err := storage.GetCampusBillBreakdown(
			r.Context(), s.pool, q.Get("from_month"), q.Get("to_month"), building,
		)
		if err != nil {
			s.writeError(w, r, http.StatusBadRequest, "invalid_breakdown", err.Error())
			return
		}
		s.writeJSON(w, http.StatusOK, result)
		return
	}
	from, to, ok := s.campusTimeRange(w, r)
	if !ok {
		return
	}
	granularity := q.Get("granularity")
	if granularity == "" {
		granularity = "day"
	}
	result, err := storage.GetCampusBreakdown(
		r.Context(), s.pool, granularity, from, to, building,
	)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_breakdown", err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, result)
}

/*
全校月度用量。账单口径。上游官方数字。权威。

	与 campusSeries 分开：粒度与对账用途不同（月 vs 日）。
	本接口走 monthly_bills。用于完整自然月核对与修订追踪。
*/
func (s *Server) campusBills(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if !s.validCampusMonthRange(w, r, q.Get("from_month"), q.Get("to_month"), false) {
		return
	}
	result, err := storage.GetCampusBills(
		r.Context(), s.pool, q.Get("from_month"), q.Get("to_month"), q.Get("building"),
	)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_bill_range", err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, result)
}

func (s *Server) campusRankings(w http.ResponseWriter, r *http.Request) {
	period, mode := r.URL.Query().Get("period"), r.URL.Query().Get("mode")
	if period != "day" && period != "week" && period != "month" {
		s.writeError(w, r, http.StatusBadRequest, "invalid_period", "period must be day, week, or month")
		return
	}
	if mode != "usage" && mode != "saving" && mode != "surge" && mode != "drop" {
		s.writeError(w, r, http.StatusBadRequest, "invalid_mode", "mode must be usage, saving, surge, or drop")
		return
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			s.writeError(w, r, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 100")
			return
		}
		limit = parsed
	}
	/* 匿名可读。有效 access token 才识别「我」那一行。
	   operator / admin 额外跳过全部脱敏。
	   角色每次现查 user_accounts。不信任 token 中旧 role。 */
	viewerID := s.optionalUserID(r)
	reveal := false
	if viewerID != "" {
		role, err := storage.GetUserRole(r.Context(), s.pool, viewerID)
		if err != nil {
			s.databaseError(w, r, err)
			return
		}
		reveal = storage.RoleAtLeast(role, storage.RoleOperator)
	}
	building, floor := r.URL.Query().Get("building"), r.URL.Query().Get("floor")
	location := s.cfg.App.Timezone
	if location == nil {
		location = time.Local
	}
	now := time.Now().In(location)
	key := fmt.Sprintf("%q|%q|%q|%q|%d|%q|%t", period, mode, building, floor, limit, viewerID, reveal)
	if s.rankingCache == nil {
		s.rankingCache = newRankingResponseCacheWithShared(s.sharedCache)
	}
	result, cacheStatus, err := s.rankingCache.get(r.Context(), key, now, func() (storage.RankingView, error) {
		frontend, err := storage.GetFrontendConfig(r.Context(), s.pool)
		if err != nil {
			return storage.RankingView{}, err
		}
		return storage.GetRanking(
			r.Context(), s.pool, period, mode, building, floor,
			limit, now, frontend.Display.RankingRefreshTime, viewerID, reveal,
		)
	})
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	w.Header().Set("X-Ranking-Cache", cacheStatus)
	w.Header().Set("Cache-Control", "private, max-age=60")
	w.Header().Add("Vary", "Authorization")
	s.writeJSON(w, http.StatusOK, result)
}

func (s *Server) hourlyHeatmap(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]any{
		"availability": "unavailable", "reason_code": "upstream_hourly_data_unavailable",
		"message": "Reliable hourly electricity data is not available from the upstream source.",
		"quality": storage.Quality{},
	})
}

func (s *Server) sendTestMail(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Recipient string `json:"recipient"`
		// 可选。用示例数据发产品模板，用于核对样式与占位符。
		Template string `json:"template"`
	}
	if !s.decodeJSON(w, r, &request) {
		return
	}
	if !s.mailReady(r.Context()) {
		s.writeError(w, r, http.StatusServiceUnavailable, "mail_not_configured", "mail provider is not configured")
		return
	}
	result, err := s.sendMailPreview(r.Context(), strings.TrimSpace(request.Template), request.Recipient)
	if err != nil {
		if errors.Is(err, errUnknownMailTemplate) {
			s.writeError(w, r, http.StatusBadRequest, "invalid_template",
				"template must be empty or one of balance_alert, usage_summary, verification_code, password_reset, push_test, meter_unbound, account_disabled, account_deleted")
			return
		}
		s.writeError(w, r, http.StatusBadGateway, "mail_delivery_failed", err.Error())
		return
	}
	s.writeJSON(w, http.StatusAccepted, result)
}

var errUnknownMailTemplate = errors.New("unknown mail template")

// 示例数据仅用于预览。真实推送由推送引擎填真值。
func (s *Server) sendMailPreview(ctx context.Context, template, recipient string) (mailer.Delivery, error) {
	switch template {
	case "":
		return s.mailer.SendTest(ctx, recipient)
	case "balance_alert":
		return s.mailer.SendBalanceAlert(ctx, recipient, notification.LowBalance(time.Now(), notification.Meter{
			Number: "31240718", Building: "12栋", Floor: "4楼", Room: "402",
		}, "12.34", "20.00"))
	case "usage_summary":
		return s.mailer.SendUsageSummary(ctx, recipient, notification.Digest(time.Now(), notification.Meter{
			Number: "31240718", Building: "12栋", Floor: "4楼", Room: "402",
		}, "43.87", "16.20", "07-21 至 07-27"))
	case "verification_code":
		return s.mailer.SendVerificationCode(ctx, recipient, "824913", 5)
	case "password_reset":
		return s.mailer.SendPasswordReset(ctx, recipient, "824913", 5)
	case "push_test":
		return s.mailer.SendPushTest(ctx, recipient,
			"43.87 元", "2026-07-29 15:08", "示例校区 · 12栋 · 4楼 · 402", "31240718")
	case "meter_unbound":
		return s.mailer.SendMeterUnbound(ctx, recipient,
			"31240718", "示例校区 · 12栋 · 4楼 · 402", "2026-07-30 10:05")
	case "account_disabled":
		return s.mailer.SendAccountDisabled(ctx, recipient, "2026-07-30 10:06", "管理员在管理面板中停用")
	case "account_deleted":
		return s.mailer.SendAccountDeleted(ctx, recipient, "2026-07-30 10:07")
	default:
		return mailer.Delivery{}, errUnknownMailTemplate
	}
}

func (s *Server) timeRange(w http.ResponseWriter, r *http.Request) (time.Time, time.Time, bool) {
	from, err := time.Parse(time.RFC3339, r.URL.Query().Get("from"))
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_from", "from must be an RFC3339 timestamp")
		return time.Time{}, time.Time{}, false
	}
	to, err := time.Parse(time.RFC3339, r.URL.Query().Get("to"))
	if err != nil || !to.After(from) {
		s.writeError(w, r, http.StatusBadRequest, "invalid_to", "to must be an RFC3339 timestamp later than from")
		return time.Time{}, time.Time{}, false
	}
	return from, to, true
}

func (s *Server) campusTimeRange(w http.ResponseWriter, r *http.Request) (time.Time, time.Time, bool) {
	from, to, ok := s.timeRange(w, r)
	if !ok {
		return time.Time{}, time.Time{}, false
	}
	if to.Sub(from) > 370*24*time.Hour {
		s.writeError(w, r, http.StatusBadRequest, "range_too_large", "campus aggregate range must not exceed 370 days")
		return time.Time{}, time.Time{}, false
	}
	return from, to, true
}

func (s *Server) validCampusMonthRange(w http.ResponseWriter, r *http.Request, fromRaw, toRaw string, required bool) bool {
	if fromRaw == "" || toRaw == "" {
		if !required && fromRaw == "" && toRaw == "" {
			return true
		}
		s.writeError(w, r, http.StatusBadRequest, "invalid_bill_range", "from_month and to_month must be provided together")
		return false
	}
	from, fromErr := time.Parse("2006-01", fromRaw)
	to, toErr := time.Parse("2006-01", toRaw)
	if fromErr != nil || toErr != nil || to.Before(from) {
		s.writeError(w, r, http.StatusBadRequest, "invalid_bill_range", "month range is invalid")
		return false
	}
	if months := (to.Year()-from.Year())*12 + int(to.Month()-from.Month()) + 1; months > 24 {
		s.writeError(w, r, http.StatusBadRequest, "range_too_large", "campus bill range must not exceed 24 months")
		return false
	}
	return true
}

func pagination(r *http.Request) (int, int, error) {
	limit := 50
	if raw := r.URL.Query().Get("page_size"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 200 {
			return 0, 0, errors.New("page_size must be between 1 and 200")
		}
		limit = value
	}
	offset, err := cursorOffset(r)
	if err != nil {
		return 0, 0, err
	}
	return limit, offset, nil
}

// cursorOffset 解析 ?cursor= 中的偏移量。游标只是 base64 编码的偏移。
// 不透明是刻意的。调用方必须原样回传，禁止自行计算。
func cursorOffset(r *http.Request) (int, error) {
	raw := r.URL.Query().Get("cursor")
	if raw == "" {
		return 0, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return 0, errors.New("cursor is invalid")
	}
	value, err := strconv.Atoi(string(decoded))
	if err != nil || value < 0 {
		return 0, errors.New("cursor is invalid")
	}
	return value, nil
}

func nextCursor[T any](items *[]T, limit, offset int) *string {
	if len(*items) <= limit {
		return nil
	}
	*items = (*items)[:limit]
	value := base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset + limit)))
	return &value
}

func (s *Server) decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_json", err.Error())
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		s.writeError(w, r, http.StatusBadRequest, "invalid_json", "request body must contain one JSON object")
		return false
	}
	return true
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (s *Server) writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	id, _ := r.Context().Value(requestIDKey).(string)
	s.writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message, "request_id": id}})
}

func (s *Server) databaseError(w http.ResponseWriter, r *http.Request, err error) {
	s.logger.Error("database operation failed", "request_id", r.Context().Value(requestIDKey), "error", err)
	s.writeError(w, r, http.StatusInternalServerError, "database_error", "database operation failed")
}

func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request, err error, resource string) {
	if errors.Is(err, pgx.ErrNoRows) {
		s.writeError(w, r, http.StatusNotFound, "not_found", resource+" not found")
		return
	}
	s.databaseError(w, r, err)
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
