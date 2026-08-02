package storage

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	upstreamRequestRateLock     int64 = 4832481387043999
	upstreamRequestSlotLockBase int64 = 4832481387044000
)

// UpstreamRequestGate 协调同一数据库上各进程的出站用电请求。
// 事务 advisory lock 间隔启动。不产生每请求表写。
// 会话 advisory lock 限制并发 HTTP 请求。
type UpstreamRequestGate struct {
	pool *pgxpool.Pool
	/* 面板可在运行中改闸门。两值为原子。
	   在途请求保持已取到的值。下一请求用新值。
	   缩并发时已持高位槽仍按 lockID 释放。禁止漏锁。 */
	intervalNanos atomic.Int64
	concurrency   atomic.Int64
	nextSlot      atomic.Uint64
}

func NewUpstreamRequestGate(pool *pgxpool.Pool, qps float64, concurrency int) (*UpstreamRequestGate, error) {
	if pool == nil {
		return nil, errors.New("upstream request gate requires a database pool")
	}
	gate := &UpstreamRequestGate{pool: pool}
	if err := gate.SetLimits(qps, concurrency); err != nil {
		return nil, err
	}
	return gate, nil
}

// SetLimits 在不重启进程时更新闸门上限。
// 调用方传入已存面板设置。非法参数保留当前值。
func (g *UpstreamRequestGate) SetLimits(qps float64, concurrency int) error {
	if qps <= 0 || concurrency < 1 {
		return errors.New("upstream request gate QPS and concurrency must be positive")
	}
	interval := time.Duration(float64(time.Second) / qps)
	if interval < time.Microsecond {
		interval = time.Microsecond
	}
	g.intervalNanos.Store(int64(interval))
	g.concurrency.Store(int64(concurrency))
	return nil
}

// Acquire 等待分布式速率许可与一个并发槽。
// 必须在消费完 HTTP 响应体后调用返回的 release。
func (g *UpstreamRequestGate) Acquire(ctx context.Context) (func(), error) {
	conn, lockID, err := g.acquireConcurrencySlot(ctx)
	if err != nil {
		return nil, err
	}
	release := releaseAdvisorySlot(conn, lockID)
	if err := g.waitRatePermit(ctx, conn); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

func (g *UpstreamRequestGate) waitRatePermit(ctx context.Context, conn *pgxpool.Conn) error {
	return pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, upstreamRequestRateLock); err != nil {
			return fmt.Errorf("acquire upstream rate lock: %w", err)
		}
		if _, err := tx.Exec(ctx, `SELECT pg_sleep($1)`, time.Duration(g.intervalNanos.Load()).Seconds()); err != nil {
			return fmt.Errorf("wait for upstream rate permit: %w", err)
		}
		return nil
	})
}

func (g *UpstreamRequestGate) acquireConcurrencySlot(ctx context.Context) (*pgxpool.Conn, int64, error) {
	poll := time.NewTicker(20 * time.Millisecond)
	defer poll.Stop()
	for {
		conn, err := g.pool.Acquire(ctx)
		if err != nil {
			return nil, 0, fmt.Errorf("acquire upstream concurrency connection: %w", err)
		}
		concurrency := int(g.concurrency.Load())
		start := int(g.nextSlot.Add(1) % uint64(concurrency))
		for offset := 0; offset < concurrency; offset++ {
			slot := (start + offset) % concurrency
			lockID := upstreamRequestSlotLockBase + int64(slot)
			var acquired bool
			if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, lockID).Scan(&acquired); err != nil {
				conn.Release()
				return nil, 0, fmt.Errorf("acquire upstream concurrency slot: %w", err)
			}
			if acquired {
				return conn, lockID, nil
			}
		}
		conn.Release()
		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		case <-poll.C:
		}
	}
}

func releaseAdvisorySlot(conn *pgxpool.Conn, lockID int64) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			var released bool
			if err := conn.QueryRow(ctx, `SELECT pg_advisory_unlock($1)`, lockID).Scan(&released); err == nil && released {
				conn.Release()
				return
			}
			// 禁止将 advisory-lock 状态不明的会话归还连接池。
			// 关闭劫持连接后由 PostgreSQL 释放锁。
			raw := conn.Hijack()
			_ = raw.Close(context.Background())
		})
	}
}
