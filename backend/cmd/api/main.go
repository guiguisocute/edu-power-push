package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/config"
	"github.com/edu-power-push/edu-power-push/backend/internal/httpapi"
	"github.com/edu-power-push/edu-power-push/backend/internal/mailer"
	"github.com/edu-power-push/edu-power-push/backend/internal/provider"
	// 注册内置上游。接入自有爬虫时替换此包。见 docs/PROVIDERS.md。
	_ "github.com/edu-power-push/edu-power-push/backend/internal/provider/bdfairy"
	"github.com/edu-power-push/edu-power-push/backend/internal/rediscache"
	"github.com/edu-power-push/edu-power-push/backend/internal/school"
	"github.com/edu-power-push/edu-power-push/backend/internal/secrets"
	"github.com/edu-power-push/edu-power-push/backend/internal/storage"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load configuration", "error", err)
		os.Exit(1)
	}
	if cfg.Database.URL == "" {
		slog.Error("DATABASE_URL is required")
		os.Exit(1)
	}
	if cfg.HTTP.AdminToken == "" {
		slog.Error("ADMIN_TOKEN is required")
		os.Exit(1)
	}
	if cfg.Auth.JWTSecret == "" {
		slog.Error("AUTH_JWT_SECRET is required")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sharedCache := rediscache.New(
		cfg.Cache.Address, cfg.Cache.Password, cfg.Cache.Database, cfg.Cache.PoolSize, cfg.Cache.Prefix,
	)
	if sharedCache != nil {
		defer sharedCache.Close()
		if err := sharedCache.Ping(ctx); err != nil {
			slog.Warn("redis cache unavailable at startup; falling back to PostgreSQL", "error", err)
		} else {
			slog.Info("redis cache ready", "address", cfg.Cache.Address)
		}
	}
	pool, err := storage.Open(ctx, cfg.Database.URL, cfg.Database.MaxConns, cfg.App.Timezone.String())
	if err != nil {
		slog.Error("open database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := storage.Migrate(ctx, pool); err != nil {
		slog.Error("apply database migrations", "error", err)
		os.Exit(1)
	}
	/* 邮件服务读取 system_settings。
	   面板改供应商或密钥后，下次发信用新值。
	   未配置时回落到环境变量。 */
	settingsBox, err := secrets.New(cfg.HTTP.SettingsKey)
	if err != nil {
		slog.Error("configure settings encryption", "error", err)
		os.Exit(1)
	}
	/* 学校绑定动态加载。
	   面板换学校后，下次刷新用新爬虫。
	   未选学校时 client 返回 school_not_configured。 */
	client := provider.NewDynamic()
	binder := school.NewBinder(pool, settingsBox, school.FromConfig(cfg), cfg.Scan.Timeout, client, slog.Default())
	if err := binder.Refresh(ctx); err != nil {
		slog.Warn("initial school binding failed; collectors stay idle until it is fixed", "error", err)
	}
	go binder.Watch(ctx, 15*time.Second)
	if err := storage.EncryptLegacyChannelCredentials(ctx, pool, settingsBox); err != nil {
		slog.Error("encrypt legacy channel credentials", "error", err)
		os.Exit(1)
	}
	api := httpapi.NewWithSharedCache(ctx, cfg, pool, client, mailer.NewDynamic(cfg.Mail, pool, settingsBox), slog.Default(), sharedCache)
	server := &http.Server{Addr: cfg.HTTP.Address, Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 2 * time.Minute}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		api.StopAndWait()
	}()
	slog.Info("API listening", "address", cfg.HTTP.Address)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("serve API", "error", err)
		os.Exit(1)
	}
	<-shutdownDone
}
