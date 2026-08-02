package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

/* 角色分层与操作审计。见 migrations/000012。
   鉴权使用账号角色。禁止使用全权 token 鉴权。
   每个写操作写入 admin_audit_log。 */

const (
	RoleUser     = "user"
	RoleOperator = "operator"
	RoleAdmin    = "admin"
)

// roleRank 比较权限高低。未知角色取最低。
var roleRank = map[string]int{RoleUser: 0, RoleOperator: 1, RoleAdmin: 2}

func RoleAtLeast(role, required string) bool { return roleRank[role] >= roleRank[required] }

// IsKnownRole 供 API 层校验参数。
func IsKnownRole(role string) bool { _, ok := roleRank[role]; return ok }

var ErrLastAdmin = errors.New("cannot remove the last admin")

// GetUserRole 返回账号角色。账号不存在或已禁用时返回空串。
func GetUserRole(ctx context.Context, pool *pgxpool.Pool, userID string) (string, error) {
	var role string
	err := pool.QueryRow(ctx, `
		SELECT role FROM user_accounts WHERE id=$1::uuid AND status='active'
	`, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read user role: %w", err)
	}
	return role, nil
}

type AdminUserView struct {
	ID              string     `json:"id"`
	Email           string     `json:"email"`
	Nickname        string     `json:"nickname"`
	Role            string     `json:"role"`
	Status          string     `json:"status"`
	CreatedAt       time.Time  `json:"created_at"`
	LastLoginAt     *time.Time `json:"last_login_at"`
	EmailVerifiedAt *time.Time `json:"email_verified_at"`
	ActiveSessions  int        `json:"active_sessions"`
	Meter           *string    `json:"meter"`
	Place           string     `json:"place"`
	// 已启用渠道数。用于判断是否实际使用推送。
	ChannelsEnabled int `json:"channels_enabled"`
}

type AdminUserPage struct {
	Items []AdminUserView `json:"items"`
	Total int             `json:"total"`
}

/*
ListAdminUsers 返回用户管理列表。

	q 同时匹配邮箱、昵称和电表号。
*/
func ListAdminUsers(ctx context.Context, pool *pgxpool.Pool, q, role, status string, limit, offset int) (AdminUserPage, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	q = strings.TrimSpace(strings.ToLower(q))
	page := AdminUserPage{Items: []AdminUserView{}}
	// 两条语句的过滤条件必须一致。否则 total 与 items 不匹配。
	const filter = `
		FROM user_accounts u
		LEFT JOIN user_meter_bindings b ON b.user_id=u.id AND b.unbound_at IS NULL
		LEFT JOIN meters m ON m.id=b.meter_id
		WHERE ($1='' OR u.email LIKE '%'||$1||'%' OR lower(u.nickname) LIKE '%'||$1||'%'
		       OR COALESCE(m.meter_no,'') LIKE '%'||$1||'%')
		  AND ($2='' OR u.role=$2) AND ($3='' OR u.status=$3)`
	if err := pool.QueryRow(ctx, `SELECT count(*) `+filter, q, role, status).Scan(&page.Total); err != nil {
		return page, fmt.Errorf("count admin users: %w", err)
	}
	rows, err := pool.Query(ctx, `
		SELECT u.id::text,u.email,u.nickname,u.role,u.status,u.created_at,u.last_login_at,u.email_verified_at,
			(SELECT count(DISTINCT x.family_id) FROM auth_refresh_sessions x
			 WHERE x.user_id=u.id AND x.revoked_at IS NULL AND x.expires_at > now()),
			m.meter_no,COALESCE(m.building,''),COALESCE(m.floor,''),COALESCE(m.room,''),
			(SELECT count(*) FROM user_notification_channels c WHERE c.user_id=u.id AND c.enabled)
		`+filter+`
		ORDER BY CASE u.role WHEN 'admin' THEN 0 WHEN 'operator' THEN 1 ELSE 2 END, u.created_at DESC
		LIMIT $4 OFFSET $5`, q, role, status, limit, offset)
	if err != nil {
		return page, fmt.Errorf("list admin users: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item AdminUserView
		var building, floor, room string
		if err := rows.Scan(
			&item.ID, &item.Email, &item.Nickname, &item.Role, &item.Status,
			&item.CreatedAt, &item.LastLoginAt, &item.EmailVerifiedAt, &item.ActiveSessions,
			&item.Meter, &building, &floor, &room, &item.ChannelsEnabled,
		); err != nil {
			return page, err
		}
		if item.Meter != nil {
			item.Place = strings.Trim(strings.Join([]string{building, floor, room}, " · "), " ·")
		}
		page.Items = append(page.Items, item)
	}
	return page, rows.Err()
}

/*
UpdateAdminUser 更新角色或启停状态。

	禁止降级或禁用最后一个 admin。否则无人可进面板。
	禁用时吊销全部会话。否则现有 token 仍可用。
*/
func UpdateAdminUser(ctx context.Context, pool *pgxpool.Pool, userID string, role, status *string) (AdminUserView, bool, error) {
	disabledNow := false
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var currentRole, currentStatus string
		if err := tx.QueryRow(ctx,
			`SELECT role,status FROM user_accounts WHERE id=$1::uuid FOR UPDATE`, userID,
		).Scan(&currentRole, &currentStatus); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrAuthUserNotFound
			}
			return err
		}
		nextRole, nextStatus := currentRole, currentStatus
		if role != nil {
			nextRole = *role
		}
		if status != nil {
			nextStatus = *status
		}
		disabledNow = currentStatus != "disabled" && nextStatus == "disabled"
		losingAdmin := currentRole == RoleAdmin && (nextRole != RoleAdmin || nextStatus != "active")
		if losingAdmin {
			var remaining int
			if err := tx.QueryRow(ctx,
				`SELECT count(*) FROM user_accounts WHERE role='admin' AND status='active' AND id <> $1::uuid`,
				userID,
			).Scan(&remaining); err != nil {
				return err
			}
			if remaining == 0 {
				return ErrLastAdmin
			}
		}
		if _, err := tx.Exec(ctx,
			`UPDATE user_accounts SET role=$2,status=$3,updated_at=now() WHERE id=$1::uuid`,
			userID, nextRole, nextStatus,
		); err != nil {
			return err
		}
		if nextStatus != "active" {
			if _, err := tx.Exec(ctx, `
				UPDATE auth_refresh_sessions
				SET revoked_at=now(), revoked_reason='account_disabled'
				WHERE user_id=$1::uuid AND revoked_at IS NULL
			`, userID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return AdminUserView{}, false, err
	}
	user, err := getAdminUser(ctx, pool, userID)
	return user, disabledNow, err
}

func getAdminUser(ctx context.Context, pool *pgxpool.Pool, userID string) (AdminUserView, error) {
	var item AdminUserView
	var building, floor, room string
	err := pool.QueryRow(ctx, `
		SELECT u.id::text,u.email,u.nickname,u.role,u.status,u.created_at,u.last_login_at,u.email_verified_at,
			(SELECT count(DISTINCT x.family_id) FROM auth_refresh_sessions x
			 WHERE x.user_id=u.id AND x.revoked_at IS NULL AND x.expires_at > now()),
			m.meter_no,COALESCE(m.building,''),COALESCE(m.floor,''),COALESCE(m.room,''),
			(SELECT count(*) FROM user_notification_channels c WHERE c.user_id=u.id AND c.enabled)
		FROM user_accounts u
		LEFT JOIN user_meter_bindings b ON b.user_id=u.id AND b.unbound_at IS NULL
		LEFT JOIN meters m ON m.id=b.meter_id
		WHERE u.id=$1::uuid`, userID).Scan(
		&item.ID, &item.Email, &item.Nickname, &item.Role, &item.Status,
		&item.CreatedAt, &item.LastLoginAt, &item.EmailVerifiedAt, &item.ActiveSessions,
		&item.Meter, &building, &floor, &room, &item.ChannelsEnabled,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminUserView{}, ErrAuthUserNotFound
	}
	if err != nil {
		return AdminUserView{}, fmt.Errorf("read admin user: %w", err)
	}
	if item.Meter != nil {
		item.Place = strings.Trim(strings.Join([]string{building, floor, room}, " · "), " ·")
	}
	return item, nil
}

/* ---- 审计 ---- */

type AuditEntry struct {
	ID         string          `json:"id"`
	ActorLabel string          `json:"actor_label"`
	Action     string          `json:"action"`
	Target     string          `json:"target"`
	Detail     json.RawMessage `json:"detail"`
	RequestID  string          `json:"request_id"`
	CreatedAt  time.Time       `json:"created_at"`
}

/*
WriteAudit 写入一条面板操作记录。

	detail 仅含字段变更元信息。禁止写入凭证值。
	调用方忽略错误并记日志。禁止因审计失败中断业务。
*/
func WriteAudit(
	ctx context.Context, pool *pgxpool.Pool,
	actorID *string, actorLabel, action, target string, detail map[string]any, requestID string,
) error {
	payload, err := json.Marshal(detail)
	if err != nil || detail == nil {
		payload = []byte(`{}`)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO admin_audit_log (actor_id,actor_label,action,target,detail,request_id)
		VALUES (NULLIF($1,'')::uuid,$2,$3,$4,$5::jsonb,$6)
	`, valueOrEmpty(actorID), actorLabel, action, target, payload, requestID)
	if err != nil {
		return fmt.Errorf("write audit: %w", err)
	}
	return nil
}

func ListAudit(ctx context.Context, pool *pgxpool.Pool, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := pool.Query(ctx, `
		SELECT id::text,actor_label,action,target,detail,request_id,created_at
		FROM admin_audit_log ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list audit: %w", err)
	}
	defer rows.Close()
	items := make([]AuditEntry, 0)
	for rows.Next() {
		var item AuditEntry
		var detail []byte
		if err := rows.Scan(&item.ID, &item.ActorLabel, &item.Action, &item.Target, &detail, &item.RequestID, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.Detail = json.RawMessage(detail)
		items = append(items, item)
	}
	return items, rows.Err()
}

/*
CountActiveAdmins 用于引导检查。

	无 admin 时面板提示用 ADMIN_TOKEN 自举。
*/
func CountActiveAdmins(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	var n int
	err := pool.QueryRow(ctx, `SELECT count(*) FROM user_accounts WHERE role='admin' AND status='active'`).Scan(&n)
	return n, err
}

// PromoteUserByEmail 为自举通道。使用 ADMIN_TOKEN 提升已注册邮箱为管理员。
func PromoteUserByEmail(ctx context.Context, pool *pgxpool.Pool, email, role string) (AdminUserView, error) {
	var userID string
	err := pool.QueryRow(ctx,
		`UPDATE user_accounts SET role=$2,updated_at=now() WHERE email=$1 AND status='active' RETURNING id::text`,
		strings.ToLower(strings.TrimSpace(email)), role,
	).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminUserView{}, ErrAuthUserNotFound
	}
	if err != nil {
		return AdminUserView{}, fmt.Errorf("promote user: %w", err)
	}
	return getAdminUser(ctx, pool, userID)
}
