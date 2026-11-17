package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"productor-api/internal/domain"
)

func (s *Server) adminOverview(w http.ResponseWriter, r *http.Request, user domain.User) {
	var users, staff, pendingProducts, pendingComments, publishedProducts, auditEvents24h int
	err := s.db.QueryRow(r.Context(), `
		SELECT
			(SELECT COUNT(*)::int FROM users),
			(SELECT COUNT(*)::int FROM users WHERE role IN ('moderator','admin') AND is_blocked=FALSE),
			(SELECT COUNT(*)::int FROM product_versions WHERE status IN ('submitted','in_review')),
			(SELECT COUNT(*)::int FROM comments WHERE status='pending'),
			(SELECT COUNT(*)::int FROM product_versions WHERE status='published'),
			(SELECT COUNT(*)::int FROM admin_audit_events WHERE created_at >= now()-interval '24 hours')`).Scan(
		&users, &staff, &pendingProducts, &pendingComments, &publishedProducts, &auditEvents24h,
	)
	if err != nil {
		s.databaseError(w, "load admin overview", err)
		return
	}
	overview := make(map[string]int)
	if domain.HasPermission(user, "users.view") {
		overview["users"] = users
		overview["staff"] = staff
	}
	if domain.HasPermission(user, "products.view") {
		overview["pendingProducts"] = pendingProducts
		overview["publishedProducts"] = publishedProducts
	}
	if domain.HasPermission(user, "comments.view") {
		overview["pendingComments"] = pendingComments
	}
	if domain.HasPermission(user, "audit.view") {
		overview["auditEvents24h"] = auditEvents24h
	}
	writeJSON(w, http.StatusOK, overview)
}

func (s *Server) adminRoles(w http.ResponseWriter, r *http.Request, _ domain.User) {
	rows, err := s.db.Query(r.Context(), `
		SELECT jsonb_build_object(
			'id', ar.id::text, 'key', ar.key, 'name', ar.name, 'description', ar.description,
			'permissions', ar.permissions, 'system', ar.is_system,
			'assignedCount', (SELECT COUNT(*)::int FROM users u WHERE u.admin_role_id=ar.id),
			'createdAt', ar.created_at, 'updatedAt', ar.updated_at
		)
		FROM admin_roles ar ORDER BY ar.is_system DESC, ar.name`)
	if err != nil {
		s.databaseError(w, "list admin roles", err)
		return
	}
	items := make([]map[string]any, 0)
	for rows.Next() {
		var raw []byte
		var item map[string]any
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			s.databaseError(w, "scan admin role", err)
			return
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			rows.Close()
			s.databaseError(w, "decode admin role", err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		s.databaseError(w, "read admin roles", err)
		return
	}
	rows.Close()
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "permissions": domain.AdminPermissions})
}

func (s *Server) createAdminRole(w http.ResponseWriter, r *http.Request, actor domain.User) {
	var input struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Permissions []string `json:"permissions"`
	}
	if err := decodeJSON(w, r, &input, 16<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", badJSONMessage(err))
		return
	}
	name := strings.TrimSpace(input.Name)
	description := strings.TrimSpace(input.Description)
	permissions, valid := normalizeAdminPermissions(input.Permissions)
	if len(name) < 2 || len(name) > 80 || len(description) > 240 || !valid {
		writeError(w, http.StatusBadRequest, "invalid_role", "نام، توضیح یا فهرست مجوزهای نقش معتبر نیست")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.databaseError(w, "begin admin role create", err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var id string
	err = tx.QueryRow(r.Context(), `
		INSERT INTO admin_roles(name, description, permissions, created_by)
		VALUES ($1,$2,$3,$4) RETURNING id::text`, name, description, permissions, actor.ID).Scan(&id)
	if err != nil {
		s.databaseError(w, "create admin role", err)
		return
	}
	details, _ := json.Marshal(map[string]any{"roleId": id, "name": name, "permissions": permissions})
	if _, err := tx.Exec(r.Context(), `INSERT INTO admin_audit_events(actor_id,action,details) VALUES($1,'role_created',$2::jsonb)`, actor.ID, details); err != nil {
		s.databaseError(w, "audit admin role create", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.databaseError(w, "commit admin role create", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "name": name, "description": description, "permissions": permissions})
}

func (s *Server) updateAdminRole(w http.ResponseWriter, r *http.Request, actor domain.User) {
	var input struct {
		Name        *string   `json:"name"`
		Description *string   `json:"description"`
		Permissions *[]string `json:"permissions"`
	}
	if err := decodeJSON(w, r, &input, 16<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", badJSONMessage(err))
		return
	}
	if input.Name == nil && input.Description == nil && input.Permissions == nil {
		writeError(w, http.StatusBadRequest, "empty_update", "دست‌کم یک فیلد برای تغییر لازم است")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.databaseError(w, "begin admin role update", err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var name, description string
	var permissions []string
	var system bool
	err = tx.QueryRow(r.Context(), `SELECT name,description,permissions,is_system FROM admin_roles WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&name, &description, &permissions, &system)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "role_not_found", "نقش پیدا نشد")
		return
	}
	if err != nil {
		s.databaseError(w, "load admin role", err)
		return
	}
	if system {
		writeError(w, http.StatusConflict, "system_role", "نقش پایهٔ سامانه قابل ویرایش نیست")
		return
	}
	if input.Name != nil {
		name = strings.TrimSpace(*input.Name)
	}
	if input.Description != nil {
		description = strings.TrimSpace(*input.Description)
	}
	if input.Permissions != nil {
		var valid bool
		permissions, valid = normalizeAdminPermissions(*input.Permissions)
		if !valid {
			writeError(w, http.StatusBadRequest, "invalid_permissions", "یکی از مجوزهای انتخاب‌شده معتبر نیست")
			return
		}
	}
	if len(name) < 2 || len(name) > 80 || len(description) > 240 {
		writeError(w, http.StatusBadRequest, "invalid_role", "نام یا توضیح نقش معتبر نیست")
		return
	}
	if _, err := tx.Exec(r.Context(), `UPDATE admin_roles SET name=$2,description=$3,permissions=$4,updated_at=now() WHERE id::text=$1`, r.PathValue("id"), name, description, permissions); err != nil {
		s.databaseError(w, "update admin role", err)
		return
	}
	details, _ := json.Marshal(map[string]any{"roleId": r.PathValue("id"), "name": name, "permissions": permissions})
	if _, err := tx.Exec(r.Context(), `INSERT INTO admin_audit_events(actor_id,action,details) VALUES($1,'role_updated',$2::jsonb)`, actor.ID, details); err != nil {
		s.databaseError(w, "audit admin role update", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.databaseError(w, "commit admin role update", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": r.PathValue("id"), "name": name, "description": description, "permissions": permissions})
}

func (s *Server) deleteAdminRole(w http.ResponseWriter, r *http.Request, actor domain.User) {
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.databaseError(w, "begin admin role delete", err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var id, name string
	var system bool
	var assigned int
	err = tx.QueryRow(r.Context(), `
		SELECT ar.id::text,ar.name,ar.is_system,(SELECT COUNT(*)::int FROM users u WHERE u.admin_role_id=ar.id)
		FROM admin_roles ar WHERE ar.id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&id, &name, &system, &assigned)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "role_not_found", "نقش پیدا نشد")
		return
	}
	if err != nil {
		s.databaseError(w, "load admin role for deletion", err)
		return
	}
	if system {
		writeError(w, http.StatusConflict, "system_role", "نقش پایهٔ سامانه قابل حذف نیست")
		return
	}
	if assigned > 0 {
		writeError(w, http.StatusConflict, "role_in_use", "ابتدا این نقش را از حساب‌های کارمندان بردارید")
		return
	}
	if _, err := tx.Exec(r.Context(), `DELETE FROM admin_roles WHERE id::text=$1`, id); err != nil {
		s.databaseError(w, "delete admin role", err)
		return
	}
	details, _ := json.Marshal(map[string]any{"roleId": id, "name": name})
	if _, err := tx.Exec(r.Context(), `INSERT INTO admin_audit_events(actor_id,action,details) VALUES($1,'role_deleted',$2::jsonb)`, actor.ID, details); err != nil {
		s.databaseError(w, "audit admin role delete", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.databaseError(w, "commit admin role delete", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func normalizeAdminPermissions(input []string) ([]string, bool) {
	permissions := make([]string, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	for _, value := range input {
		permission := strings.TrimSpace(value)
		if !domain.ValidAdminPermission(permission) {
			return nil, false
		}
		if _, exists := seen[permission]; exists {
			continue
		}
		seen[permission] = struct{}{}
		permissions = append(permissions, permission)
	}
	return permissions, true
}
