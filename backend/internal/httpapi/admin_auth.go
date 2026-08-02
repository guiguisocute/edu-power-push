package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/edu-power-push/edu-power-push/backend/internal/storage"
)

/*
管理端点鉴权。两条通道用途不同。

	1. 用户会话加角色（operator / admin）。角色每次现查库。
	2. ADMIN_TOKEN 供机器使用。全权。禁止进入浏览器。

	本中间件是唯一门禁。角色变更立即生效。
*/

const actorKey contextKey = "admin_actor"

// AdminActor 是通过鉴权的执行者。审计与会话接口使用它。
type AdminActor struct {
	UserID string `json:"user_id"`
	Label  string `json:"label"`
	Role   string `json:"role"`
	// ViaToken 为 true 表示机器凭 ADMIN_TOKEN 进入。
	ViaToken bool `json:"via_token"`
}

// actorFrom 返回当前请求的执行者。缺失时返回零值。
func actorFrom(ctx context.Context) AdminActor {
	actor, _ := ctx.Value(actorKey).(AdminActor)
	return actor
}

/*
adminAuth 创建至少 required 级别的中间件。

	ADMIN_TOKEN 视同 admin。会话通道要求账号 active 且角色达标。
	401 表示未表明身份。403 表示级别不够。
*/
func (s *Server) adminAuth(required string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.adminTokenMatches(r) {
			actor := AdminActor{Label: "admin-token", Role: storage.RoleAdmin, ViaToken: true}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), actorKey, actor)))
			return
		}
		userID := s.optionalUserID(r)
		if userID == "" || s.pool == nil {
			s.writeError(w, r, http.StatusUnauthorized, "unauthorized", "sign in with an operator account")
			return
		}
		// 角色现查。降权必须立刻生效。禁止等待 access token 过期。
		role, err := storage.GetUserRole(r.Context(), s.pool, userID)
		if err != nil {
			s.databaseError(w, r, err)
			return
		}
		if role == "" {
			s.writeError(w, r, http.StatusUnauthorized, "unauthorized", "account is unavailable")
			return
		}
		if !storage.RoleAtLeast(role, required) {
			s.writeError(w, r, http.StatusForbidden, "forbidden", "this account does not have access to the admin panel")
			return
		}
		user, err := storage.GetAuthUser(r.Context(), s.pool, userID)
		label := userID
		if err == nil {
			label = user.Email
		}
		actor := AdminActor{UserID: userID, Label: label, Role: role}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), actorKey, actor)))
	})
}

// operatorRoute 注册运维级管理端点。
func (s *Server) operatorRoute(method, pattern string, handler http.HandlerFunc) {
	s.mux.Handle(method+" "+pattern, s.adminAuth(storage.RoleOperator, handler))
}

// adminRoute 注册管理员级管理端点。
func (s *Server) adminRoute(method, pattern string, handler http.HandlerFunc) {
	s.mux.Handle(method+" "+pattern, s.adminAuth(storage.RoleAdmin, handler))
}

/*
audit 记录一次面板写动作。

	detail 仅放字段变更元信息。禁止写入凭证值。
	写失败只记日志。禁止阻断业务请求。
*/
func (s *Server) audit(r *http.Request, action, target string, detail map[string]any) {
	actor := actorFrom(r.Context())
	var actorID *string
	if actor.UserID != "" {
		id := actor.UserID
		actorID = &id
	}
	requestID, _ := r.Context().Value(requestIDKey).(string)
	if err := storage.WriteAudit(
		r.Context(), s.pool, actorID, actor.Label, action, target, detail, requestID,
	); err != nil {
		s.logger.Error("write audit", "action", action, "error", err)
	}
}

// adminSession 返回当前执行者身份与权限摘要。
func (s *Server) adminSession(w http.ResponseWriter, r *http.Request) {
	actor := actorFrom(r.Context())
	admins, err := storage.CountActiveAdmins(r.Context(), s.pool)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"actor":       actor,
		"environment": s.cfg.App.Environment,
		"version":     releaseVersion,
		// secrets_ready=false 时面板必须提前说明凭证不可存。
		"secrets_ready": s.secrets.Enabled(),
		"admin_count":   admins,
	})
}

/*
promoteAdminUser 自举第一个管理员。

	仅认 ADMIN_TOKEN。库中无 admin 时会话通道无法进入。
	之后改角色走 /admin/users/{id}。
*/
func (s *Server) promoteAdminUser(w http.ResponseWriter, r *http.Request) {
	if !actorFrom(r.Context()).ViaToken {
		s.writeError(w, r, http.StatusForbidden, "forbidden",
			"bootstrapping an admin requires the machine admin token")
		return
	}
	var request struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if !s.decodeJSON(w, r, &request) {
		return
	}
	role := strings.TrimSpace(request.Role)
	if role == "" {
		role = storage.RoleAdmin
	}
	if !storage.IsKnownRole(role) {
		s.writeError(w, r, http.StatusBadRequest, "invalid_role", "role must be user, operator, or admin")
		return
	}
	user, err := storage.PromoteUserByEmail(r.Context(), s.pool, request.Email, role)
	if err != nil {
		s.writeAdminUserError(w, r, err)
		return
	}
	s.audit(r, "user.promote", user.ID, map[string]any{"role": role})
	s.writeJSON(w, http.StatusOK, user)
}
