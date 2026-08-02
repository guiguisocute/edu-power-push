package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrOAuthEmailUnverified 提供方邮箱未验证。
	// 禁止按邮箱认领已有账号。
	ErrOAuthEmailUnverified = errors.New("oauth email is not verified by the provider")
	// ErrOAuthEmailMissing 提供方未返回可用邮箱。
	// 邮箱为推送与找回密码凭据。无邮箱禁止建号。
	ErrOAuthEmailMissing = errors.New("oauth provider did not return a usable email")
	// ErrOAuthAlreadyLinked 该账号已绑同一提供方的另一身份。
	ErrOAuthAlreadyLinked = errors.New("account already linked to another identity from this provider")
	// ErrOAuthRegistrationDisabled 注册关闭，且本次登录需要新建账号。
	ErrOAuthRegistrationDisabled = errors.New("registration is disabled")
)

/*
OAuthProfile 为第三方登录返回的事实。全部由提供方给出。
	Subject 为提供方稳定用户 ID。它是身份主键。邮箱可改。
*/
type OAuthProfile struct {
	Provider      string
	Subject       string
	Email         string
	EmailVerified bool
	Nickname      string
}

// OAuthLoginOutcome 说明本次登录结果。供接口层返回文案。
type OAuthLoginOutcome struct {
	User AuthUser
	// Created 为真表示本次登录创建了账号。
	Created bool
	// Linked 为真表示挂到已有账号。按已验证邮箱认领。
	Linked bool
}

/*
LinkOrCreateOAuthUser 将第三方登录落成本站会话。
	1. 身份已存在：直接登录。更新邮箱线索与登录时间。
	2. 新身份且已验证邮箱匹配已有账号：绑定。
	3. 身份与邮箱均新：建账号。密码列留空。
	邮箱未验证时禁止认领已有账号。返回 ErrOAuthEmailUnverified。
*/
func LinkOrCreateOAuthUser(
	ctx context.Context,
	pool *pgxpool.Pool,
	profile OAuthProfile,
	newUserID string,
	registrationEnabled bool,
	session RefreshSession,
) (OAuthLoginOutcome, error) {
	var outcome OAuthLoginOutcome
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var userID string
		err := tx.QueryRow(ctx, `
			SELECT user_id::text FROM user_oauth_identities
			WHERE provider=$1 AND subject=$2
			FOR UPDATE
		`, profile.Provider, profile.Subject).Scan(&userID)
		switch {
		case err == nil:
			if _, err := tx.Exec(ctx, `
				UPDATE user_oauth_identities
				SET last_login_at=now(), email=NULLIF($3,'')
				WHERE provider=$1 AND subject=$2
			`, profile.Provider, profile.Subject, profile.Email); err != nil {
				return fmt.Errorf("touch oauth identity: %w", err)
			}
		case errors.Is(err, pgx.ErrNoRows):
			userID, err = resolveOAuthAccount(ctx, tx, profile, newUserID, registrationEnabled, &outcome)
			if err != nil {
				return err
			}
			if err := insertOAuthIdentity(ctx, tx, userID, profile); err != nil {
				return err
			}
		default:
			return fmt.Errorf("look up oauth identity: %w", err)
		}

		user, err := getAuthUser(ctx, tx, userID, false)
		if err != nil {
			return err
		}
		outcome.User = user
		// 停用账号不建会话。接口层返回账号不可用。
		// 先插身份再检查：绑定关系真实。启用后可直接登录。
		if user.Status != "active" {
			return nil
		}
		if err := insertRefreshSession(ctx, tx, session); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE user_accounts SET last_login_at=now(),updated_at=now() WHERE id=$1::uuid`, userID,
		); err != nil {
			return fmt.Errorf("record oauth login: %w", err)
		}
		return nil
	})
	if err != nil {
		return OAuthLoginOutcome{}, err
	}
	return outcome, nil
}

// resolveOAuthAccount 决定新身份落点：认领已有账号，或新建账号。
func resolveOAuthAccount(
	ctx context.Context,
	tx pgx.Tx,
	profile OAuthProfile,
	newUserID string,
	registrationEnabled bool,
	outcome *OAuthLoginOutcome,
) (string, error) {
	if profile.Email == "" {
		return "", ErrOAuthEmailMissing
	}
	var existingID string
	err := tx.QueryRow(ctx,
		`SELECT id::text FROM user_accounts WHERE email=$1 FOR UPDATE`, profile.Email,
	).Scan(&existingID)
	switch {
	case err == nil:
		if !profile.EmailVerified {
			return "", ErrOAuthEmailUnverified
		}
		outcome.Linked = true
		return existingID, nil
	case errors.Is(err, pgx.ErrNoRows):
	default:
		return "", fmt.Errorf("look up account by oauth email: %w", err)
	}
	// 建号也要求邮箱已验证。否则会向非本人地址发推送。
	if !profile.EmailVerified {
		return "", ErrOAuthEmailUnverified
	}
	if !registrationEnabled {
		return "", ErrOAuthRegistrationDisabled
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO user_accounts (id,email,password_hash,nickname,email_verified_at)
		VALUES ($1::uuid,$2,NULL,$3,now())
	`, newUserID, profile.Email, profile.Nickname); err != nil {
		if isUniqueViolation(err) {
			return "", ErrEmailExists
		}
		return "", fmt.Errorf("create account from oauth: %w", err)
	}
	outcome.Created = true
	return newUserID, nil
}

func insertOAuthIdentity(ctx context.Context, tx pgx.Tx, userID string, profile OAuthProfile) error {
	tag, err := tx.Exec(ctx, `
		INSERT INTO user_oauth_identities (user_id,provider,subject,email,last_login_at)
		VALUES ($1::uuid,$2,$3,NULLIF($4,''),now())
		ON CONFLICT (user_id,provider) DO NOTHING
	`, userID, profile.Provider, profile.Subject, profile.Email)
	if err != nil {
		return fmt.Errorf("link oauth identity: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// 该账号已绑同一提供方的另一 subject。禁止自动选择。
		return ErrOAuthAlreadyLinked
	}
	return nil
}

// ListUserOAuthProviders 返回该账号已绑定的第三方。供账号设置页展示。
func ListUserOAuthProviders(ctx context.Context, pool *pgxpool.Pool, userID string) ([]string, error) {
	rows, err := pool.Query(ctx, `
		SELECT provider FROM user_oauth_identities WHERE user_id=$1::uuid ORDER BY provider
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list oauth identities: %w", err)
	}
	defer rows.Close()
	providers := make([]string, 0, 2)
	for rows.Next() {
		var provider string
		if err := rows.Scan(&provider); err != nil {
			return nil, fmt.Errorf("scan oauth identity: %w", err)
		}
		providers = append(providers, provider)
	}
	return providers, rows.Err()
}
