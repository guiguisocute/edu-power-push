package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const refreshRotationGrace = 5 * time.Second

var (
	ErrEmailExists       = errors.New("email already registered")
	ErrAuthUserNotFound  = errors.New("user not found")
	ErrMeterNotEligible  = errors.New("meter not found or not eligible")
	ErrMeterNotSupported = errors.New("meter building is not supported by the platform")
	ErrMeterAlreadyBound = errors.New("meter already bound")
	ErrMeterBindingFull  = errors.New("meter has reached its binding limit")
	ErrRefreshInvalid    = errors.New("refresh session invalid")
	ErrRefreshExpired    = errors.New("refresh session expired")
	ErrRefreshReuse      = errors.New("refresh token reuse detected")
)

/*
MaxMeterBinders 为一块表的最大绑定账号数。

	真正约束在库中。见 migrations/000025 槽位唯一索引。
	先 count 再 insert 在并发下会漏。
	本常量仅用于生成候选槽位与错误文案。必须与 CHECK 同步修改。
*/
const MaxMeterBinders = 4

/*
bindMeterSlot 占用当前空着的最小槽位。

	新增绑定必须走此处。直接 INSERT 会绕过槽位分配。
	表满时插入 0 行。使用 RowsAffected 判定。
*/
func bindMeterSlot(ctx context.Context, tx pgx.Tx, userID, meterID string) error {
	tag, err := tx.Exec(ctx, `
		INSERT INTO user_meter_bindings (user_id,meter_id,slot)
		SELECT $1::uuid, $2::uuid, s.slot
		FROM generate_series(1,$3::int) AS s(slot)
		WHERE NOT EXISTS (
			SELECT 1 FROM user_meter_bindings b
			WHERE b.meter_id=$2::uuid AND b.unbound_at IS NULL AND b.slot=s.slot
		)
		ORDER BY s.slot
		LIMIT 1
	`, userID, meterID, MaxMeterBinders)
	if err != nil {
		// 并发可选中同一槽位。唯一索引兜底。
		if isUniqueViolation(err) {
			return ErrMeterBindingFull
		}
		return fmt.Errorf("bind meter slot: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrMeterBindingFull
	}
	return nil
}

/*
countActiveBinders 返回该表当前生效绑定数。

	exceptUserID 非空时排除该用户。避免本人占用名额。
*/
func countActiveBinders(ctx context.Context, pool *pgxpool.Pool, meterNo, exceptUserID string) (int, error) {
	var n int
	err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM user_meter_bindings b
		JOIN meters m ON m.id=b.meter_id
		WHERE m.meter_no=$1 AND b.unbound_at IS NULL
		  AND (NULLIF($2,'') IS NULL OR b.user_id<>NULLIF($2,'')::uuid)
	`, meterNo, exceptUserID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count meter binders: %w", err)
	}
	return n, nil
}

type AuthUser struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Nickname string `json:"nickname"`
	Status   string `json:"status"`
	// Role 控制前端是否显示管理入口。真正鉴权在服务端每次现查。
	Role            string     `json:"role"`
	CreatedAt       time.Time  `json:"created_at"`
	LastLoginAt     *time.Time `json:"last_login_at"`
	EmailVerifiedAt *time.Time `json:"email_verified_at"`
	// ActiveSessions 为未吊销且未过期的 refresh family 数。
	ActiveSessions int `json:"active_sessions"`
	// HasPassword 为假表示仅第三方登录。尚未设置密码。
	// 改密与注销必须先有密码。界面据此显示「设置密码」。
	HasPassword bool `json:"has_password"`
	// OAuthProviders 为已绑定第三方登录（google / github）。
	OAuthProviders []string   `json:"oauth_providers"`
	PasswordHash   string     `json:"-"`
	Meter          *UserMeter `json:"meter"`
}

type UserMeter struct {
	Meter    string     `json:"meter"`
	Campus   string     `json:"campus"`
	Building string     `json:"building"`
	Floor    string     `json:"floor"`
	Room     string     `json:"room"`
	BoundAt  *time.Time `json:"bound_at"`
}

type RefreshSession struct {
	ID            string
	FamilyID      string
	UserID        string
	JTI           string
	TokenHash     []byte
	ExpiresAt     time.Time
	UserAgentHash []byte
}

func CreateAuthUser(
	ctx context.Context,
	pool *pgxpool.Pool,
	userID, email, passwordHash, nickname, meterNo string,
	session RefreshSession,
) (AuthUser, error) {
	var result AuthUser
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		// meterNo 为空时仅创建账号。登录后经 PUT /api/v1/me/meter 绑定。
		var meterID string
		if meterNo != "" {
			if err := tx.QueryRow(ctx, `
				SELECT id::text
				FROM meters
				WHERE meter_no=$1 AND active AND NOT excluded AND building<>$2
			`, meterNo, nonPlatformBillingBuilding).Scan(&meterID); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return ErrMeterNotEligible
				}
				return fmt.Errorf("look up registration meter: %w", err)
			}
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO user_accounts (id,email,password_hash,nickname,email_verified_at)
			VALUES ($1::uuid,$2,$3,$4,now())
		`, userID, email, passwordHash, nickname)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return ErrEmailExists
			}
			return fmt.Errorf("insert user account: %w", err)
		}
		if meterID != "" {
			if err := bindMeterSlot(ctx, tx, userID, meterID); err != nil {
				return err
			}
		}
		if err := insertRefreshSession(ctx, tx, session); err != nil {
			return err
		}
		var scanErr error
		result, scanErr = getAuthUser(ctx, tx, userID, true)
		return scanErr
	})
	return result, err
}

func CheckMeterBindingAvailable(ctx context.Context, pool *pgxpool.Pool, meterNo, exceptUserID string) error {
	var eligible bool
	var building string
	err := pool.QueryRow(ctx, `
		SELECT active AND NOT excluded,building FROM meters WHERE meter_no=$1
	`, meterNo).Scan(&eligible, &building)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !eligible) {
		return ErrMeterNotEligible
	}
	if err != nil {
		return fmt.Errorf("check binding meter: %w", err)
	}
	if !platformSupportsBuilding(building) {
		return ErrMeterNotSupported
	}
	// 一块表最多 MaxMeterBinders 个账号共用。满了才拒绝。
	binders, err := countActiveBinders(ctx, pool, meterNo, exceptUserID)
	if err != nil {
		return err
	}
	if binders >= MaxMeterBinders {
		return ErrMeterBindingFull
	}
	return nil
}

// MeterPreview 为绑表前位置回显。禁止包含余额或读数。
// 该表此刻不属于请求者。见 frontend/docs/AUTH-GAPS.md §3。
type MeterPreview struct {
	Meter     string  `json:"meter"`
	Campus    string  `json:"campus"`
	Building  string  `json:"building"`
	Floor     string  `json:"floor"`
	Room      string  `json:"room"`
	Claimable bool    `json:"claimable"`
	Reason    *string `json:"reason"`
}

// PreviewMeter 返回电表位置与可绑定状态。
// exceptUserID 已绑定同一块表时视为可绑定。换绑幂等。
// 表号不在库存时返回 ErrMeterNotEligible。调用方据此回 404。
func PreviewMeter(ctx context.Context, pool *pgxpool.Pool, meterNo, exceptUserID string) (MeterPreview, error) {
	preview := MeterPreview{Meter: meterNo, Claimable: true}
	var excluded bool
	err := pool.QueryRow(ctx, `
		SELECT campus,building,floor,room,excluded
		FROM meters WHERE meter_no=$1 AND active
	`, meterNo).Scan(&preview.Campus, &preview.Building, &preview.Floor, &preview.Room, &excluded)
	if errors.Is(err, pgx.ErrNoRows) {
		return MeterPreview{}, ErrMeterNotEligible
	}
	if err != nil {
		return MeterPreview{}, fmt.Errorf("preview meter: %w", err)
	}
	if excluded {
		reason := "excluded"
		preview.Claimable, preview.Reason = false, &reason
		return preview, nil
	}
	if !platformSupportsBuilding(preview.Building) {
		reason := "unsupported_building"
		preview.Claimable, preview.Reason = false, &reason
		return preview, nil
	}
	binders, err := countActiveBinders(ctx, pool, meterNo, exceptUserID)
	if err != nil {
		return MeterPreview{}, err
	}
	if binders >= MaxMeterBinders {
		// 沿用 already_bound。占用满与被独占对用户同一处理。
		reason := "already_bound"
		preview.Claimable, preview.Reason = false, &reason
	}
	return preview, nil
}

func FindAuthUserByEmail(ctx context.Context, pool *pgxpool.Pool, email string) (AuthUser, error) {
	row := pool.QueryRow(ctx, authUserQuery+` WHERE u.email=$1`, email)
	return scanAuthUser(row)
}

func GetAuthUser(ctx context.Context, pool *pgxpool.Pool, userID string) (AuthUser, error) {
	return getAuthUser(ctx, pool, userID, false)
}

// GetAuthUserWithPassword 供改密或换绑邮箱校验当前密码。禁止直接返回客户端。
func GetAuthUserWithPassword(ctx context.Context, pool *pgxpool.Pool, userID string) (AuthUser, error) {
	return getAuthUser(ctx, pool, userID, true)
}

// AccessSessionActive 使 access JWT 吊销跟随 refresh family。
// 登出、改密、吊销会话或禁用账号在下次受保护请求生效。
func AccessSessionActive(ctx context.Context, pool *pgxpool.Pool, userID, familyID string) (bool, error) {
	var active bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM user_accounts u
			JOIN auth_refresh_sessions s ON s.user_id=u.id
			WHERE u.id=$1::uuid AND u.status='active'
			  AND s.family_id=$2::uuid
			  AND s.revoked_at IS NULL AND s.expires_at > now()
		)
	`, userID, familyID).Scan(&active)
	if err != nil {
		return false, fmt.Errorf("validate access session: %w", err)
	}
	return active, nil
}

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func getAuthUser(ctx context.Context, db rowQuerier, userID string, includePassword bool) (AuthUser, error) {
	user, err := scanAuthUser(db.QueryRow(ctx, authUserQuery+` WHERE u.id=$1::uuid`, userID))
	if err != nil {
		return AuthUser{}, err
	}
	if !includePassword {
		user.PasswordHash = ""
	}
	return user, nil
}

// 账号设置所需字段：邮箱验证、活跃会话、绑表时间。一次查询返回。
const authUserQuery = `
	SELECT
		u.id::text,COALESCE(u.password_hash,''),u.password_hash IS NOT NULL,
		u.email,u.nickname,u.status,u.role,u.created_at,u.last_login_at,
		u.email_verified_at,
		(
			SELECT count(DISTINCT x.family_id) FROM auth_refresh_sessions x
			WHERE x.user_id=u.id AND x.revoked_at IS NULL AND x.expires_at > now()
		),
		COALESCE((
			SELECT array_agg(i.provider ORDER BY i.provider) FROM user_oauth_identities i
			WHERE i.user_id=u.id
		), ARRAY[]::text[]),
		m.meter_no,m.campus,m.building,m.floor,m.room,b.bound_at
	FROM user_accounts u
	LEFT JOIN user_meter_bindings b ON b.user_id=u.id AND b.unbound_at IS NULL
	LEFT JOIN meters m ON m.id=b.meter_id
`

func scanAuthUser(row pgx.Row) (AuthUser, error) {
	var user AuthUser
	var meter, campus, building, floor, room *string
	var boundAt *time.Time
	err := row.Scan(
		&user.ID, &user.PasswordHash, &user.HasPassword,
		&user.Email, &user.Nickname, &user.Status, &user.Role,
		&user.CreatedAt, &user.LastLoginAt, &user.EmailVerifiedAt, &user.ActiveSessions,
		&user.OAuthProviders,
		&meter, &campus, &building, &floor, &room, &boundAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthUser{}, ErrAuthUserNotFound
	}
	if err != nil {
		return AuthUser{}, fmt.Errorf("query auth user: %w", err)
	}
	if user.OAuthProviders == nil {
		user.OAuthProviders = []string{}
	}
	if meter != nil {
		user.Meter = &UserMeter{
			Meter: *meter, Campus: valueOrEmpty(campus), Building: valueOrEmpty(building),
			Floor: valueOrEmpty(floor), Room: valueOrEmpty(room), BoundAt: boundAt,
		}
	}
	return user, nil
}

/*
RevokeAllUserSessions 吊销该用户全部会话。

	不改密码即可收回全部会话。返回吊销条数。
*/
func RevokeAllUserSessions(ctx context.Context, pool *pgxpool.Pool, userID string) (int64, error) {
	tag, err := pool.Exec(ctx, `
		UPDATE auth_refresh_sessions
		SET revoked_at=now(), revoked_reason='user_revoked_all'
		WHERE user_id=$1::uuid AND revoked_at IS NULL AND expires_at > now()
	`, userID)
	if err != nil {
		return 0, fmt.Errorf("revoke all sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}

func CreateRefreshSession(ctx context.Context, pool *pgxpool.Pool, session RefreshSession) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		return insertRefreshSession(ctx, tx, session)
	})
}

func insertRefreshSession(ctx context.Context, tx pgx.Tx, session RefreshSession) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO auth_refresh_sessions (
			id,family_id,user_id,jti,token_hash,expires_at,user_agent_hash
		) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7)
	`, session.ID, session.FamilyID, session.UserID, session.JTI, session.TokenHash, session.ExpiresAt, nullableBytes(session.UserAgentHash))
	if err != nil {
		return fmt.Errorf("insert refresh session: %w", err)
	}
	return nil
}

func RotateRefreshSession(
	ctx context.Context,
	pool *pgxpool.Pool,
	oldHash []byte,
	claimedUserID, claimedFamilyID, claimedJTI string,
	requestStartedAt time.Time,
	next RefreshSession,
) error {
	var resultErr error
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var sessionID, familyID, userID, jti, userStatus string
		var expiresAt time.Time
		var revokedAt, lastUsedAt *time.Time
		var revokedReason *string
		var storedUserAgentHash []byte
		err := tx.QueryRow(ctx, `
			SELECT s.id::text,s.family_id::text,s.user_id::text,s.jti::text,
			       s.expires_at,s.revoked_at,s.revoked_reason,s.last_used_at,
			       s.user_agent_hash,u.status
			FROM auth_refresh_sessions s
			JOIN user_accounts u ON u.id=s.user_id
			WHERE s.token_hash=$1
			FOR UPDATE OF s
		`, oldHash).Scan(
			&sessionID, &familyID, &userID, &jti, &expiresAt, &revokedAt,
			&revokedReason, &lastUsedAt, &storedUserAgentHash, &userStatus,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			resultErr = ErrRefreshInvalid
			return nil
		}
		if err != nil {
			return fmt.Errorf("lock refresh session: %w", err)
		}
		if userID != claimedUserID || familyID != claimedFamilyID || jti != claimedJTI || userStatus != "active" {
			resultErr = ErrRefreshInvalid
			return nil
		}
		if revokedAt != nil {
			if concurrentRefreshRotation(
				revokedReason, lastUsedAt, storedUserAgentHash, next.UserAgentHash,
				requestStartedAt, time.Now(),
			) {
				// 多标签共享 cookie，但 JS 域分离。
				// 首次轮换后紧随的请求获得合法子会话。禁止判为盗用。
				// 下次正常轮换删除未用兄弟会话。
				return insertRefreshSession(ctx, tx, next)
			}
			if _, err := tx.Exec(ctx, `
				UPDATE auth_refresh_sessions
				SET revoked_at=COALESCE(revoked_at,now()), revoked_reason=COALESCE(revoked_reason,'reuse_detected')
				WHERE family_id=$1::uuid
			`, familyID); err != nil {
				return fmt.Errorf("revoke reused refresh family: %w", err)
			}
			resultErr = ErrRefreshReuse
			return nil
		}
		if !expiresAt.After(time.Now()) {
			if _, err := tx.Exec(ctx, `
				UPDATE auth_refresh_sessions
				SET revoked_at=now(),revoked_reason='expired'
				WHERE id=$1::uuid
			`, sessionID); err != nil {
				return fmt.Errorf("expire refresh session: %w", err)
			}
			resultErr = ErrRefreshExpired
			return nil
		}
		if err := insertRefreshSession(ctx, tx, next); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE auth_refresh_sessions
			SET revoked_at=clock_timestamp(),revoked_reason='rotated',
			    last_used_at=clock_timestamp(),replaced_by_id=$2::uuid
			WHERE id=$1::uuid
		`, sessionID, next.ID); err != nil {
			return fmt.Errorf("rotate refresh session: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE auth_refresh_sessions
			SET revoked_at=now(),revoked_reason='superseded'
			WHERE family_id=$1::uuid AND id<>$2::uuid AND revoked_at IS NULL
		`, familyID, next.ID); err != nil {
			return fmt.Errorf("revoke superseded refresh siblings: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return resultErr
}

func concurrentRefreshRotation(
	reason *string,
	lastUsedAt *time.Time,
	storedUserAgentHash, nextUserAgentHash []byte,
	requestStartedAt, now time.Time,
) bool {
	if reason == nil || *reason != "rotated" || lastUsedAt == nil {
		return false
	}
	if !bytes.Equal(storedUserAgentHash, nextUserAgentHash) {
		return false
	}
	// 仅首次轮换提交前已在途的请求算并发。提交后重放仍为盗用。
	if requestStartedAt.After(*lastUsedAt) {
		return false
	}
	age := now.Sub(*lastUsedAt)
	return age >= -time.Second && age <= refreshRotationGrace
}

func RevokeRefreshFamily(ctx context.Context, pool *pgxpool.Pool, tokenHash []byte, reason string) error {
	if reason == "" {
		reason = "logout"
	}
	_, err := pool.Exec(ctx, `
		UPDATE auth_refresh_sessions
		SET revoked_at=COALESCE(revoked_at,now()),revoked_reason=COALESCE(revoked_reason,$2)
		WHERE family_id=(SELECT family_id FROM auth_refresh_sessions WHERE token_hash=$1)
	`, tokenHash, reason)
	if err != nil {
		return fmt.Errorf("revoke refresh family: %w", err)
	}
	return nil
}

func MarkAuthLogin(ctx context.Context, pool *pgxpool.Pool, userID string) error {
	_, err := pool.Exec(ctx, `UPDATE user_accounts SET last_login_at=now(),updated_at=now() WHERE id=$1::uuid`, userID)
	return err
}

// UpdatePassword 更新密码并吊销该用户全部 refresh family。所有设备下线。
func UpdatePassword(ctx context.Context, pool *pgxpool.Pool, userID, passwordHash string) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE user_accounts SET password_hash=$2,updated_at=now() WHERE id=$1::uuid AND status='active'
		`, userID, passwordHash)
		if err != nil {
			return fmt.Errorf("update password: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrAuthUserNotFound
		}
		if _, err := tx.Exec(ctx, `
			UPDATE auth_refresh_sessions
			SET revoked_at=COALESCE(revoked_at,now()),revoked_reason=COALESCE(revoked_reason,'password_reset')
			WHERE user_id=$1::uuid AND revoked_at IS NULL
		`, userID); err != nil {
			return fmt.Errorf("revoke sessions after password reset: %w", err)
		}
		return nil
	})
}

// UpdateNickname 更新展示昵称。长度 ≤80。空串由调用方回退到邮箱本地部分。
func UpdateNickname(ctx context.Context, pool *pgxpool.Pool, userID, nickname string) (AuthUser, error) {
	tag, err := pool.Exec(ctx, `
		UPDATE user_accounts SET nickname=$2,updated_at=now()
		WHERE id=$1::uuid AND status='active'
	`, userID, nickname)
	if err != nil {
		return AuthUser{}, fmt.Errorf("update nickname: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return AuthUser{}, ErrAuthUserNotFound
	}
	return GetAuthUser(ctx, pool, userID)
}

// UpdateEmail 换绑登录邮箱。新邮箱必须尚未被占用。
func UpdateEmail(ctx context.Context, pool *pgxpool.Pool, userID, newEmail string) (AuthUser, error) {
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var oldEmail string
		if err := tx.QueryRow(ctx, `
			SELECT email FROM user_accounts WHERE id=$1::uuid AND status='active' FOR UPDATE
		`, userID).Scan(&oldEmail); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrAuthUserNotFound
			}
			return fmt.Errorf("read current email: %w", err)
		}
		tag, err := tx.Exec(ctx, `
			UPDATE user_accounts SET email=$2,email_verified_at=now(),updated_at=now()
			WHERE id=$1::uuid AND status='active'
		`, userID, newEmail)
		if err != nil {
			if isUniqueViolation(err) {
				return ErrEmailExists
			}
			return fmt.Errorf("update email: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrAuthUserNotFound
		}

		// 登录邮箱为默认已验证推送地址。换绑时同步替换列表中的旧地址。
		var raw []byte
		err = tx.QueryRow(ctx, `
			SELECT config FROM user_notification_channels
			WHERE user_id=$1::uuid AND channel='mail' FOR UPDATE
		`, userID).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read mail channel during email change: %w", err)
		}
		config := map[string]any{}
		if err := json.Unmarshal(raw, &config); err != nil {
			return fmt.Errorf("decode mail channel during email change: %w", err)
		}
		recipients, err := parseMailRecipients(config["to"])
		if err != nil {
			return err
		}
		changed := false
		updatedRecipients := make([]string, 0, len(recipients))
		seenRecipients := make(map[string]bool, len(recipients))
		for _, recipient := range recipients {
			if recipient == oldEmail {
				recipient = newEmail
				changed = true
			}
			if !seenRecipients[recipient] {
				seenRecipients[recipient] = true
				updatedRecipients = append(updatedRecipients, recipient)
			}
		}
		if !changed {
			return nil
		}
		config["to"] = updatedRecipients
		payload, err := json.Marshal(config)
		if err != nil {
			return fmt.Errorf("encode mail channel during email change: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE user_notification_channels SET config=$2::jsonb,updated_at=now()
			WHERE user_id=$1::uuid AND channel='mail'
		`, userID, payload); err != nil {
			return fmt.Errorf("update mail channel during email change: %w", err)
		}
		return nil
	})
	if err != nil {
		return AuthUser{}, err
	}
	return GetAuthUser(ctx, pool, userID)
}

/*
DeleteAuthUser 注销账号。真删除，不是置 disabled。

	用户数据随 ON DELETE CASCADE 删除。
	电表读数、日用量与月账单挂在 meters 上。禁止删除。
	审计日志 actor_id 置 NULL。保留操作痕迹。
	禁止注销最后一个 admin。
*/
func DeleteAuthUser(ctx context.Context, pool *pgxpool.Pool, userID string) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var role string
		if err := tx.QueryRow(ctx,
			`SELECT role FROM user_accounts WHERE id=$1::uuid FOR UPDATE`, userID,
		).Scan(&role); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrAuthUserNotFound
			}
			return fmt.Errorf("load account for deletion: %w", err)
		}
		if role == "admin" {
			var admins int
			if err := tx.QueryRow(ctx,
				`SELECT count(*) FROM user_accounts
				 WHERE role='admin' AND status='active' AND id<>$1::uuid`, userID,
			).Scan(&admins); err != nil {
				return fmt.Errorf("count admins: %w", err)
			}
			if admins == 0 {
				return ErrLastAdmin
			}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO user_account_deletions (user_id,deleted_at)
			VALUES ($1::uuid,now())
			ON CONFLICT (user_id) DO UPDATE SET deleted_at=EXCLUDED.deleted_at
		`, userID); err != nil {
			return fmt.Errorf("record account deletion: %w", err)
		}
		tag, err := tx.Exec(ctx, `DELETE FROM user_accounts WHERE id=$1::uuid`, userID)
		if err != nil {
			return fmt.Errorf("delete account: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrAuthUserNotFound
		}
		return nil
	})
}

func RebindUserMeter(ctx context.Context, pool *pgxpool.Pool, userID, meterNo string) (AuthUser, error) {
	var result AuthUser
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var meterID string
		if err := tx.QueryRow(ctx, `
			SELECT id::text FROM meters
			WHERE meter_no=$1 AND active AND NOT excluded AND building<>$2
		`, meterNo, nonPlatformBillingBuilding).Scan(&meterID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrMeterNotEligible
			}
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE user_meter_bindings SET unbound_at=now()
			WHERE user_id=$1::uuid AND unbound_at IS NULL
		`, userID); err != nil {
			return err
		}
		if err := bindMeterSlot(ctx, tx, userID, meterID); err != nil {
			return err
		}
		var scanErr error
		result, scanErr = getAuthUser(ctx, tx, userID, false)
		return scanErr
	})
	return result, err
}

// UnbindUserMeter 仅解除有效产品绑定。
// 读数、官方日用量与账单仍挂在 meters 上。禁止删除。
// 返回本次变更的绑定。nil 表示已解绑。重复点击不发重复通知邮件。
func UnbindUserMeter(ctx context.Context, pool *pgxpool.Pool, userID string) (AuthUser, *UserMeter, error) {
	var user AuthUser
	var unbound *UserMeter
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var meter UserMeter
		err := tx.QueryRow(ctx, `
			UPDATE user_meter_bindings b SET unbound_at=now()
			FROM meters m
			WHERE b.user_id=$1::uuid AND b.unbound_at IS NULL AND m.id=b.meter_id
			RETURNING m.meter_no,m.campus,m.building,m.floor,m.room,b.bound_at
		`, userID).Scan(&meter.Meter, &meter.Campus, &meter.Building, &meter.Floor, &meter.Room, &meter.BoundAt)
		switch {
		case err == nil:
			unbound = &meter
		case errors.Is(err, pgx.ErrNoRows):
			// 无有效绑定为成功幂等空操作。仍加载账号。不存在则 404。
		default:
			return fmt.Errorf("unbind user meter: %w", err)
		}
		var loadErr error
		user, loadErr = getAuthUser(ctx, tx, userID, false)
		return loadErr
	})
	if err != nil {
		return AuthUser{}, nil, err
	}
	return user, unbound, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
