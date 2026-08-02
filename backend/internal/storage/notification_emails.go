package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var ErrUnverifiedMailRecipient = errors.New("mail recipient is not verified")

// IsNotificationEmailVerified 仅信任已验证登录邮箱或已验证附加收件邮箱。
func IsNotificationEmailVerified(ctx context.Context, db dbConn, userID, email string) (bool, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var verified bool
	err := db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM user_accounts
			WHERE id=$1::uuid AND status='active' AND email_verified_at IS NOT NULL AND email=$2
			UNION ALL
			SELECT 1 FROM user_verified_notification_emails
			WHERE user_id=$1::uuid AND email=$2
		)
	`, userID, email).Scan(&verified)
	if err != nil {
		return false, fmt.Errorf("check verified notification email: %w", err)
	}
	return verified, nil
}

func EnsureVerifiedMailRecipients(ctx context.Context, db dbConn, userID string, recipients []string) error {
	for _, recipient := range recipients {
		verified, err := IsNotificationEmailVerified(ctx, db, userID, recipient)
		if err != nil {
			return err
		}
		if !verified {
			return fmt.Errorf("%w: %s", ErrUnverifiedMailRecipient, recipient)
		}
	}
	return nil
}

// AddVerifiedNotificationEmail 在验证码消费成功后记录邮箱归属。重复确认幂等。
func AddVerifiedNotificationEmail(ctx context.Context, db dbConn, userID, email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	_, err := db.Exec(ctx, `
		INSERT INTO user_verified_notification_emails (user_id,email,verified_at)
		VALUES ($1::uuid,$2,now())
		ON CONFLICT (user_id,email) DO UPDATE SET verified_at=now()
	`, userID, email)
	if err != nil {
		return fmt.Errorf("record verified notification email: %w", err)
	}
	return nil
}

func filterVerifiedMailRecipients(recipients []string, accountEmail string, additional map[string]bool) []string {
	allowed := make(map[string]bool, len(additional)+1)
	if accountEmail = strings.ToLower(strings.TrimSpace(accountEmail)); accountEmail != "" {
		allowed[accountEmail] = true
	}
	for email := range additional {
		allowed[strings.ToLower(strings.TrimSpace(email))] = true
	}
	out := make([]string, 0, len(recipients))
	for _, recipient := range recipients {
		if allowed[strings.ToLower(strings.TrimSpace(recipient))] {
			out = append(out, recipient)
		}
	}
	return out
}
