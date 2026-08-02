package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/edu-power-push/edu-power-push/backend/internal/storage"
)

/* Web 管理面板端点：用户管理与审计。
   设置类端点在 admin_settings.go。 */

func (s *Server) listAdminUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, offset := 50, 0
	if raw := q.Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}
	if raw := q.Get("offset"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			offset = n
		}
	}
	role, status := q.Get("role"), q.Get("status")
	if role != "" && !storage.IsKnownRole(role) {
		s.writeError(w, r, http.StatusBadRequest, "invalid_role", "role must be user, operator, or admin")
		return
	}
	if status != "" && status != "active" && status != "disabled" {
		s.writeError(w, r, http.StatusBadRequest, "invalid_status", "status must be active or disabled")
		return
	}
	page, err := storage.ListAdminUsers(r.Context(), s.pool, q.Get("q"), role, status, limit, offset)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, page)
}

/*
updateAdminUser 更新用户角色或启停状态。

	必须 admin。能授权他人者本身等同管理员。
*/
func (s *Server) updateAdminUser(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("user_id")
	var request struct {
		Role   *string `json:"role"`
		Status *string `json:"status"`
	}
	if !s.decodeJSON(w, r, &request) {
		return
	}
	if request.Role == nil && request.Status == nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_request", "role or status is required")
		return
	}
	if request.Role != nil && !storage.IsKnownRole(*request.Role) {
		s.writeError(w, r, http.StatusBadRequest, "invalid_role", "role must be user, operator, or admin")
		return
	}
	if request.Status != nil && *request.Status != "active" && *request.Status != "disabled" {
		s.writeError(w, r, http.StatusBadRequest, "invalid_status", "status must be active or disabled")
		return
	}
	// 禁止对自己降级或禁用。否则会立即锁死当前会话。
	if actor := actorFrom(r.Context()); actor.UserID == userID && actor.UserID != "" {
		if (request.Role != nil && *request.Role != storage.RoleAdmin) ||
			(request.Status != nil && *request.Status != "active") {
			s.writeError(w, r, http.StatusConflict, "self_lockout",
				"you cannot demote or disable your own account")
			return
		}
	}
	user, disabledNow, err := storage.UpdateAdminUser(r.Context(), s.pool, userID, request.Role, request.Status)
	if err != nil {
		s.writeAdminUserError(w, r, err)
		return
	}
	detail := map[string]any{}
	if request.Role != nil {
		detail["role"] = *request.Role
	}
	if request.Status != nil {
		detail["status"] = *request.Status
	}
	s.audit(r, "user.update", user.ID, detail)
	if disabledNow {
		requestID, _ := r.Context().Value(requestIDKey).(string)
		s.notifyAccountDisabled(requestID, user.Email, "管理员在管理面板中停用")
	}
	s.writeJSON(w, http.StatusOK, user)
}

func (s *Server) writeAdminUserError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, storage.ErrAuthUserNotFound):
		s.writeError(w, r, http.StatusNotFound, "not_found", "no such account")
	case errors.Is(err, storage.ErrLastAdmin):
		s.writeError(w, r, http.StatusConflict, "last_admin",
			"this is the only active admin; promote someone else first")
	default:
		s.databaseError(w, r, err)
	}
}

func (s *Server) listAdminAudit(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}
	items, err := storage.ListAudit(r.Context(), s.pool, limit)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

/*
revokeAdminUserSessions 吊销指定账号全部会话。

	用于账号被盗时止血。比直接禁用更温和。
	用户仍可重新登录。
*/
func (s *Server) revokeAdminUserSessions(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("user_id")
	revoked, err := storage.RevokeAllUserSessions(r.Context(), s.pool, userID)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.audit(r, "user.revoke_sessions", userID, map[string]any{"revoked": revoked})
	s.writeJSON(w, http.StatusOK, map[string]any{"revoked": revoked})
}

// 仅清除当前绑定关系。电表读数、日电量与账单底账全部保留。
func (s *Server) unbindAdminUserMeter(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("user_id")
	user, meter, err := storage.UnbindUserMeter(r.Context(), s.pool, userID)
	if err != nil {
		s.writeAdminUserError(w, r, err)
		return
	}
	s.rankingCache.invalidate()
	s.audit(r, "user.meter_unbind", userID, nil)
	if meter != nil {
		requestID, _ := r.Context().Value(requestIDKey).(string)
		s.notifyMeterUnbound(requestID, user.Email, meter.Meter, userMeterLocation(meter))
	}
	w.WriteHeader(http.StatusNoContent)
}

// 管理员删除账号无需用户密码。禁止删除自己。
// 最后一个管理员保护由存储层兜底。
func (s *Server) deleteAdminUser(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("user_id")
	if actor := actorFrom(r.Context()); actor.UserID == userID && actor.UserID != "" {
		s.writeError(w, r, http.StatusConflict, "self_lockout", "you cannot delete your own account here")
		return
	}
	user, err := storage.GetAuthUser(r.Context(), s.pool, userID)
	if err != nil {
		s.writeAdminUserError(w, r, err)
		return
	}
	if err := storage.DeleteAuthUser(r.Context(), s.pool, userID); err != nil {
		s.writeAdminUserError(w, r, err)
		return
	}
	s.rankingCache.invalidate()
	s.audit(r, "user.delete", userID, nil)
	requestID, _ := r.Context().Value(requestIDKey).(string)
	s.notifyAccountDeleted(requestID, user.Email)
	w.WriteHeader(http.StatusNoContent)
}
