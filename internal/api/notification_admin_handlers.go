package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"productor-api/internal/domain"
)

func (s *Server) myNotifications(w http.ResponseWriter, r *http.Request, userIDUser domain.User) {
	limit, offset := pagination(r)
	rows, err := s.db.Query(r.Context(), `
		SELECT jsonb_build_object(
			'id',id::text,'kind',kind,'title',title,'body',body,'url',NULLIF(url,''),
			'readAt',read_at,'createdAt',created_at
		)
		FROM notifications WHERE user_id=$1 ORDER BY created_at DESC,id LIMIT $2 OFFSET $3`,
		userIDUser.ID, limit, offset)
	if err != nil {
		s.databaseError(w, "list notifications", err)
		return
	}
	items := make([]domain.Notification, 0)
	for rows.Next() {
		var raw []byte
		var item domain.Notification
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			s.databaseError(w, "scan notification", err)
			return
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			rows.Close()
			s.databaseError(w, "decode notification", err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		s.databaseError(w, "read notifications", err)
		return
	}
	rows.Close()
	var total, unread int
	if err := s.db.QueryRow(r.Context(), `
		SELECT COUNT(*)::int,COUNT(*) FILTER (WHERE read_at IS NULL)::int
		FROM notifications WHERE user_id=$1`, userIDUser.ID).Scan(&total, &unread); err != nil {
		s.databaseError(w, "count notifications", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items, "total": total, "unread": unread, "limit": limit, "offset": offset,
	})
}

func (s *Server) markNotificationRead(w http.ResponseWriter, r *http.Request, user domain.User) {
	tag, err := s.db.Exec(r.Context(), `
		UPDATE notifications SET read_at=COALESCE(read_at,now())
		WHERE id=$1 AND user_id=$2`, r.PathValue("id"), user.ID)
	if err != nil {
		s.databaseError(w, "mark notification read", err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "notification_not_found", "اعلان پیدا نشد")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) markAllNotificationsRead(w http.ResponseWriter, r *http.Request, user domain.User) {
	if _, err := s.db.Exec(r.Context(), `
		UPDATE notifications SET read_at=now()
		WHERE user_id=$1 AND read_at IS NULL`, user.ID); err != nil {
		s.databaseError(w, "mark all notifications read", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request, _ domain.User) {
	limit, offset := pagination(r)
	q := trimmed(r.URL.Query().Get("q"), 120)
	rows, err := s.db.Query(r.Context(), `
		SELECT jsonb_build_object(
			'id',u.id::text,'phone',u.phone_e164,'role',u.role,'blocked',u.is_blocked,'createdAt',u.created_at,
			'adminRoleId',COALESCE(ar.id::text,''),'adminRoleName',COALESCE(ar.name,''),
			'permissions',COALESCE(ar.permissions,'{}'::text[])
		)
		FROM users u LEFT JOIN admin_roles ar ON ar.id=u.admin_role_id
		WHERE ($1='' OR u.phone_e164 ILIKE '%'||$1||'%')
		ORDER BY u.created_at DESC,u.id LIMIT $2 OFFSET $3`, q, limit, offset)
	if err != nil {
		s.databaseError(w, "list users", err)
		return
	}
	items := make([]map[string]any, 0)
	for rows.Next() {
		var raw []byte
		var item map[string]any
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			s.databaseError(w, "scan user", err)
			return
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			rows.Close()
			s.databaseError(w, "decode user", err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		s.databaseError(w, "read users", err)
		return
	}
	rows.Close()
	var total int
	if err := s.db.QueryRow(r.Context(), "SELECT COUNT(*) FROM users WHERE ($1='' OR phone_e164 ILIKE '%'||$1||'%')", q).Scan(&total); err != nil {
		s.databaseError(w, "count users", err)
		return
	}
	writeJSON(w, http.StatusOK, pageResponse[map[string]any]{Items: items, Total: total, Limit: limit, Offset: offset})
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request, actor domain.User) {
	var input struct {
		Role        *string `json:"role"`
		AdminRoleID *string `json:"adminRoleId"`
		Blocked     *bool   `json:"blocked"`
	}
	if err := decodeJSON(w, r, &input, 8<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", badJSONMessage(err))
		return
	}
	if input.Role == nil && input.AdminRoleID == nil && input.Blocked == nil {
		writeError(w, http.StatusBadRequest, "empty_update", "نقش، نقش کارمندی یا وضعیت مسدودی را مشخص کنید")
		return
	}
	if input.Role != nil && *input.Role != "user" && *input.Role != "moderator" && *input.Role != "admin" {
		writeError(w, http.StatusBadRequest, "invalid_role", "نقش کاربر معتبر نیست")
		return
	}
	if input.Role != nil && input.AdminRoleID != nil && strings.TrimSpace(*input.AdminRoleID) != "" && *input.Role != "moderator" {
		writeError(w, http.StatusBadRequest, "conflicting_roles", "نقش کلی و نقش کارمندی با هم سازگار نیستند")
		return
	}
	if actor.Role != domain.RoleAdmin && (input.Role != nil || input.AdminRoleID != nil) {
		writeError(w, http.StatusForbidden, "forbidden", "تغییر نقش فقط برای مدیر کل مجاز است")
		return
	}
	targetID := r.PathValue("id")
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.databaseError(w, "begin user update", err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var id, phone, role, adminRoleID string
	var blocked bool
	err = tx.QueryRow(r.Context(), `
		SELECT id::text,phone_e164,role,COALESCE(admin_role_id::text,''),is_blocked
		FROM users WHERE id::text=$1 FOR UPDATE`, targetID).Scan(&id, &phone, &role, &adminRoleID, &blocked)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "user_not_found", "کاربر پیدا نشد")
		return
	}
	if err != nil {
		s.databaseError(w, "load user for update", err)
		return
	}
	if actor.Role != domain.RoleAdmin && role == "admin" {
		writeError(w, http.StatusForbidden, "forbidden", "تغییر وضعیت مدیر کل مجاز نیست")
		return
	}
	newRole, newAdminRoleID, newBlocked := role, adminRoleID, blocked
	if input.Role != nil {
		newRole = *input.Role
	}
	if input.AdminRoleID != nil {
		newAdminRoleID = strings.TrimSpace(*input.AdminRoleID)
		if newAdminRoleID == "" {
			if input.Role == nil && newRole == "moderator" {
				newRole = "user"
			}
		} else {
			var exists bool
			if err := tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM admin_roles WHERE id::text=$1)`, newAdminRoleID).Scan(&exists); err != nil {
				s.databaseError(w, "validate user admin role", err)
				return
			}
			if !exists {
				writeError(w, http.StatusBadRequest, "role_not_found", "نقش دسترسی انتخاب‌شده پیدا نشد")
				return
			}
			newRole = "moderator"
		}
	}
	if input.Role != nil && (*input.Role == "user" || *input.Role == "admin") {
		newAdminRoleID = ""
	}
	if newRole == "moderator" && newAdminRoleID == "" {
		if err := tx.QueryRow(r.Context(), `SELECT id::text FROM admin_roles WHERE key='moderator' AND is_system`).Scan(&newAdminRoleID); err != nil {
			s.databaseError(w, "load base moderator role", err)
			return
		}
	}
	if input.Blocked != nil {
		newBlocked = *input.Blocked
	}
	if actor.ID == id && (newBlocked || newRole != "admin") {
		writeError(w, http.StatusConflict, "self_lockout", "نمی‌توانید دسترسی مدیر کل خودتان را حذف یا مسدود کنید")
		return
	}
	_, err = tx.Exec(r.Context(), `
		UPDATE users SET role=$2,admin_role_id=NULLIF($3,'')::uuid,is_blocked=$4,updated_at=now()
		WHERE id=$1`, id, newRole, newAdminRoleID, newBlocked)
	if err != nil {
		s.databaseError(w, "update user", err)
		return
	}
	if newBlocked && !blocked {
		if _, err := tx.Exec(r.Context(), `UPDATE sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, id); err != nil {
			s.databaseError(w, "revoke blocked user sessions", err)
			return
		}
	}
	details, _ := json.Marshal(map[string]any{
		"phone":        phone,
		"previousRole": role,
		"role":         newRole,
		"adminRoleId":  newAdminRoleID,
		"blocked":      newBlocked,
	})
	if _, err := tx.Exec(r.Context(), `INSERT INTO admin_audit_events(actor_id,target_user_id,action,details) VALUES($1,$2,'user_updated',$3::jsonb)`, actor.ID, id, details); err != nil {
		s.databaseError(w, "record admin user action", err)
		return
	}
	var response map[string]any
	var raw []byte
	err = tx.QueryRow(r.Context(), `
		SELECT jsonb_build_object(
			'id',u.id::text,'phone',u.phone_e164,'role',u.role,'blocked',u.is_blocked,
			'adminRoleId',COALESCE(ar.id::text,''),'adminRoleName',COALESCE(ar.name,''),
			'permissions',COALESCE(ar.permissions,'{}'::text[])
		)
		FROM users u LEFT JOIN admin_roles ar ON ar.id=u.admin_role_id WHERE u.id=$1`, id).Scan(&raw)
	if err != nil {
		s.databaseError(w, "read updated user", err)
		return
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		s.databaseError(w, "decode updated user", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.databaseError(w, "commit user update", err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) adminAudit(w http.ResponseWriter, r *http.Request, _ domain.User) {
	limit, offset := pagination(r)
	rows, err := s.db.Query(r.Context(), `
		SELECT jsonb_build_object(
			'id',e.id::text,'actor',COALESCE(a.phone_e164,''),'target',COALESCE(t.phone_e164,''),
			'action',e.action,'details',e.details,'createdAt',e.created_at
		)
		FROM admin_audit_events e
		LEFT JOIN users a ON a.id=e.actor_id
		LEFT JOIN users t ON t.id=e.target_user_id
		ORDER BY e.created_at DESC,e.id LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		s.databaseError(w, "list admin audit", err)
		return
	}
	items := make([]map[string]any, 0)
	for rows.Next() {
		var raw []byte
		var item map[string]any
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			s.databaseError(w, "scan admin audit", err)
			return
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			rows.Close()
			s.databaseError(w, "decode admin audit", err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		s.databaseError(w, "read admin audit", err)
		return
	}
	rows.Close()
	var total int
	if err := s.db.QueryRow(r.Context(), "SELECT COUNT(*) FROM admin_audit_events").Scan(&total); err != nil {
		s.databaseError(w, "count admin audit", err)
		return
	}
	writeJSON(w, http.StatusOK, pageResponse[map[string]any]{Items: items, Total: total, Limit: limit, Offset: offset})
}

func notificationURL(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return ""
	}
	return value
}
