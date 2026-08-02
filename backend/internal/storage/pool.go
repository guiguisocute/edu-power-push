package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

/*
maxConns 由 DATABASE_MAX_CONNS 配置。

	每个闸门并发槽占用一条池连接，直至上游请求结束。
	池大小是上游并发硬上限。池过小时抬高闸门不增加吞吐。
*/
func Open(ctx context.Context, databaseURL string, maxConns int32, timezone ...string) (*pgxpool.Pool, error) {
	if databaseURL == "" {
		return nil, errors.New("DATABASE_URL is required")
	}
	if maxConns < 2 {
		return nil, fmt.Errorf("database pool needs at least 2 connections (got %d)", maxConns)
	}
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	cfg.MaxConns = maxConns
	cfg.MinConns = 1
	cfg.MaxConnIdleTime = 15 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second
	zone := "Asia/Shanghai"
	if len(timezone) > 0 && timezone[0] != "" {
		zone = timezone[0]
	}
	cfg.ConnConfig.RuntimeParams["timezone"] = zone

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create PostgreSQL pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping PostgreSQL: %w", err)
	}
	return pool, nil
}
