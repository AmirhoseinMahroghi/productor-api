package api

import (
	"encoding/json"
	"net/http"

	"productor-api/internal/domain"
)

func (s *Server) productVersions(w http.ResponseWriter, r *http.Request, _ domain.User) {
	slug := r.PathValue("slug")
	var exists bool
	if err := s.db.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM products WHERE slug=$1)", slug).Scan(&exists); err != nil {
		s.databaseError(w, "find product versions", err)
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "product_not_found", "محصول پیدا نشد")
		return
	}
	limit, offset := pagination(r)
	rows, err := s.db.Query(r.Context(), `
		SELECT jsonb_build_object(
			'id',pv.id::text,'version',pv.version_no,'status',pv.status,'payload',pv.payload,
			'createdBy',pv.created_by::text,'reviewerId',pv.reviewer_id::text,
			'reviewerNote',NULLIF(pv.reviewer_note,''),'createdAt',pv.created_at,
			'submittedAt',pv.submitted_at,'reviewedAt',pv.reviewed_at,'publishedAt',pv.published_at,
			'isCurrent',p.current_version_id=pv.id,'isPublished',p.published_version_id=pv.id
		)
		FROM product_versions pv JOIN products p ON p.id=pv.product_id
		WHERE p.slug=$1 ORDER BY pv.version_no DESC
		LIMIT $2 OFFSET $3`, slug, limit, offset)
	if err != nil {
		s.databaseError(w, "list product versions", err)
		return
	}
	items := make([]map[string]any, 0)
	for rows.Next() {
		var raw []byte
		var item map[string]any
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			s.databaseError(w, "scan product version", err)
			return
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			rows.Close()
			s.databaseError(w, "decode product version", err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		s.databaseError(w, "read product versions", err)
		return
	}
	rows.Close()
	var total int
	if err := s.db.QueryRow(r.Context(), "SELECT COUNT(*) FROM product_versions pv JOIN products p ON p.id=pv.product_id WHERE p.slug=$1", slug).Scan(&total); err != nil {
		s.databaseError(w, "count product versions", err)
		return
	}
	writeJSON(w, http.StatusOK, pageResponse[map[string]any]{Items: items, Total: total, Limit: limit, Offset: offset})
}
