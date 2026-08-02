package storage

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// 定长比较。验证码校验禁止因前缀匹配而变慢。
func constantTimeEqual(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}

/* 邮箱验证码。注册与找回密码共用。
   6 位数字本身很弱。安全依赖一次性、短有效期、失败上限与发送间隔。 */

const (
	// 同一邮箱两次发码的最小间隔。
	EmailCodeResendInterval = 60 * time.Second
	// 允许的错误尝试次数。超过即作废。必须重新发码。
	EmailCodeMaxAttempts = 5
)

var (
	ErrEmailCodeTooSoon  = errors.New("verification code was requested too recently")
	ErrEmailCodeInvalid  = errors.New("verification code is invalid or expired")
	ErrEmailCodeAttempts = errors.New("verification code has too many failed attempts")
	ErrMailQuotaExceeded = errors.New("global user mail quota exceeded")
)

/*
IssueEmailCode 写入或覆盖一条验证码。
	距上次发码不足 EmailCodeResendInterval 时返回 ErrEmailCodeTooSoon 与剩余秒数。
	调用方据此回 429 与 Retry-After。
*/
func IssueEmailCode(
	ctx context.Context, pool *pgxpool.Pool,
	email, purpose string, codeHash []byte, ttl time.Duration,
) (time.Duration, error) {
	var wait time.Duration
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var err error
		wait, err = issueEmailCode(ctx, tx, email, purpose, codeHash, ttl)
		return err
	})
	return wait, err
}

// IssueEmailCodeWithQuota 原子创建可用验证码并预留一次发送配额。
// 预留失败则回滚验证码。避免未发送却进入重发冷却。
func IssueEmailCodeWithQuota(
	ctx context.Context, pool *pgxpool.Pool,
	email, purpose string, codeHash []byte, ttl time.Duration,
	minuteLimit, dayLimit int,
) (time.Duration, error) {
	var wait time.Duration
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var err error
		wait, err = issueEmailCode(ctx, tx, email, purpose, codeHash, ttl)
		if err != nil {
			return err
		}
		return reserveUserMailQuota(ctx, tx, minuteLimit, dayLimit)
	})
	return wait, err
}

func issueEmailCode(
	ctx context.Context, tx pgx.Tx,
	email, purpose string, codeHash []byte, ttl time.Duration,
) (time.Duration, error) {
	var lastCreated *time.Time
	err := tx.QueryRow(ctx, `
		SELECT created_at FROM auth_email_codes WHERE email=$1 AND purpose=$2 FOR UPDATE
	`, email, purpose).Scan(&lastCreated)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("read email code: %w", err)
	}
	if lastCreated != nil {
		if wait := EmailCodeResendInterval - time.Since(*lastCreated); wait > 0 {
			return wait, ErrEmailCodeTooSoon
		}
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO auth_email_codes (email,purpose,code_hash,expires_at,attempts,consumed_at,created_at)
		VALUES ($1,$2,$3,now()+$4::interval,0,NULL,now())
		ON CONFLICT (email,purpose) DO UPDATE SET
			code_hash=EXCLUDED.code_hash, expires_at=EXCLUDED.expires_at,
			attempts=0, consumed_at=NULL, created_at=now()
	`, email, purpose, codeHash, durationInterval(ttl))
	if err != nil {
		return 0, fmt.Errorf("store email code: %w", err)
	}
	return 0, nil
}

func reserveUserMailQuota(ctx context.Context, tx pgx.Tx, minuteLimit, dayLimit int) error {
	windows := []struct {
		scope, trunc string
		limit        int
		duration     time.Duration
	}{
		{"user:minute", "minute", minuteLimit, 2 * time.Minute},
		{"user:day", "day", dayLimit, 48 * time.Hour},
	}
	for _, window := range windows {
		var count int
		err := tx.QueryRow(ctx, `
			INSERT INTO mail_send_quota_windows (scope,window_start,sent_count,expires_at)
			VALUES ($1,date_trunc($2,now()),1,now()+$4::interval)
			ON CONFLICT (scope,window_start) DO UPDATE
			SET sent_count=mail_send_quota_windows.sent_count+1
			WHERE mail_send_quota_windows.sent_count < $3
			RETURNING sent_count
		`, window.scope, window.trunc, window.limit, durationInterval(window.duration)).Scan(&count)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrMailQuotaExceeded
		}
		if err != nil {
			return fmt.Errorf("reserve %s mail quota: %w", window.scope, err)
		}
	}
	return nil
}

/*
ConsumeEmailCode 校验并一次性消费验证码。
	错误码仅累加 attempts。禁止泄露该邮箱是否在流程中。
*/
func ConsumeEmailCode(ctx context.Context, pool *pgxpool.Pool, email, purpose string, codeHash []byte) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var storedHash []byte
		var expiresAt time.Time
		var attempts int
		var consumedAt *time.Time
		err := tx.QueryRow(ctx, `
			SELECT code_hash,expires_at,attempts,consumed_at
			FROM auth_email_codes WHERE email=$1 AND purpose=$2 FOR UPDATE
		`, email, purpose).Scan(&storedHash, &expiresAt, &attempts, &consumedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrEmailCodeInvalid
		}
		if err != nil {
			return fmt.Errorf("read email code: %w", err)
		}
		if consumedAt != nil || time.Now().After(expiresAt) {
			return ErrEmailCodeInvalid
		}
		if attempts >= EmailCodeMaxAttempts {
			return ErrEmailCodeAttempts
		}
		if !constantTimeEqual(storedHash, codeHash) {
			if _, err := tx.Exec(ctx, `
				UPDATE auth_email_codes SET attempts=attempts+1 WHERE email=$1 AND purpose=$2
			`, email, purpose); err != nil {
				return fmt.Errorf("record failed attempt: %w", err)
			}
			return ErrEmailCodeInvalid
		}
		if _, err := tx.Exec(ctx, `
			UPDATE auth_email_codes SET consumed_at=now() WHERE email=$1 AND purpose=$2
		`, email, purpose); err != nil {
			return fmt.Errorf("consume email code: %w", err)
		}
		return nil
	})
}

// PruneEmailCodes 删除过期记录。随其他维护任务运行。
func PruneEmailCodes(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `DELETE FROM auth_email_codes WHERE expires_at < now() - interval '1 day'`)
	if err != nil {
		return fmt.Errorf("prune email codes: %w", err)
	}
	return nil
}
