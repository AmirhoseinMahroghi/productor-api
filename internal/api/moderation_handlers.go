package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"productor-api/internal/domain"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (s *Server) moderationProducts(w http.ResponseWriter, r *http.Request, _ domain.User) {
	limit, offset := pagination(r)
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	where := "pv.status IN ('submitted','in_review')"
	switch status {
	case "", "pending":
	case "submitted":
		where = "pv.status = 'submitted'"
	case "in_review":
		where = "pv.status = 'in_review'"
	case "draft", "changes_requested", "rejected", "published", "unpublished":
		where = "pv.status = '" + status + "'"
	case "all":
		where = "pv.status <> 'superseded'"
	default:
		writeError(w, http.StatusBadRequest, "invalid_status", "وضعیت محصول معتبر نیست")
		return
	}
	q := trimmed(r.URL.Query().Get("q"), 120)
	rows, err := s.db.Query(r.Context(), managedProductSelect+`
WHERE `+where+` AND ($3='' OR pv.payload->>'name' ILIKE '%'||$3||'%' OR pv.payload->>'tagline' ILIKE '%'||$3||'%' OR o.name ILIKE '%'||$3||'%')
ORDER BY pv.submitted_at ASC, p.id
LIMIT $1 OFFSET $2`, limit, offset, q)
	if err != nil {
		s.databaseError(w, "list moderation products", err)
		return
	}
	items, err := scanProducts(rows)
	if err != nil {
		s.databaseError(w, "scan moderation products", err)
		return
	}
	var total int
	if err := s.db.QueryRow(r.Context(), "SELECT COUNT(*) FROM products p JOIN product_versions pv ON pv.id=p.current_version_id JOIN organizations o ON o.id=p.organization_id WHERE "+where+" AND ($1='' OR pv.payload->>'name' ILIKE '%'||$1||'%' OR pv.payload->>'tagline' ILIKE '%'||$1||'%' OR o.name ILIKE '%'||$1||'%')", q).Scan(&total); err != nil {
		s.databaseError(w, "count moderation products", err)
		return
	}
	writeJSON(w, http.StatusOK, pageResponse[domain.Product]{Items: items, Total: total, Limit: limit, Offset: offset})
}

func (s *Server) claimProductReview(w http.ResponseWriter, r *http.Request, user domain.User) {
	result, err := s.db.Exec(r.Context(), `
		UPDATE product_versions pv
		SET status='in_review', reviewer_id=$2, updated_at=now()
		FROM products p
		WHERE p.slug=$1 AND p.current_version_id=pv.id AND pv.status='submitted'`, r.PathValue("slug"), user.ID)
	if err != nil {
		s.databaseError(w, "claim product review", err)
		return
	}
	if result.RowsAffected() == 0 {
		writeError(w, http.StatusConflict, "product_not_queued", "محصول در صف ارسال‌شده وجود ندارد")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "in_review"})
}

func (s *Server) decideProduct(w http.ResponseWriter, r *http.Request, user domain.User) {
	var input struct {
		Action string `json:"action"`
		Note   string `json:"note"`
	}
	if err := decodeJSON(w, r, &input, 16<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", badJSONMessage(err))
		return
	}
	input.Action = strings.TrimSpace(input.Action)
	input.Note = strings.TrimSpace(input.Note)
	if input.Action != "approve" && input.Action != "return" && input.Action != "reject" {
		writeError(w, http.StatusBadRequest, "invalid_action", "تصمیم باید approve، return یا reject باشد")
		return
	}
	if len([]rune(input.Note)) > 400 {
		writeError(w, http.StatusBadRequest, "invalid_note", "یادداشت حداکثر ۴۰۰ نویسه باشد")
		return
	}
	if input.Action != "approve" && input.Note == "" {
		writeError(w, http.StatusBadRequest, "note_required", "برای برگشت یا رد، توضیح لازم است")
		return
	}

	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.databaseError(w, "begin product moderation", err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var productID, versionID, status, organizationID string
	var payloadRaw []byte
	var publishedVersionID string
	err = tx.QueryRow(r.Context(), `
		SELECT p.id::text, pv.id::text, pv.status, p.organization_id::text,
		       COALESCE(p.published_version_id::text, ''), pv.payload
		FROM products p JOIN product_versions pv ON pv.id=p.current_version_id
		WHERE p.slug=$1 FOR UPDATE OF p,pv`, r.PathValue("slug")).Scan(
		&productID, &versionID, &status, &organizationID, &publishedVersionID, &payloadRaw,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "product_not_found", "محصول پیدا نشد")
		return
	}
	if err != nil {
		s.databaseError(w, "load product for moderation", err)
		return
	}
	if status != string(domain.ProductSubmitted) && status != string(domain.ProductInReview) {
		writeError(w, http.StatusConflict, "product_not_queued", "این نسخه در صف بررسی نیست")
		return
	}

	nextStatus := string(domain.ProductPublished)
	eventAction := "تأیید و انتشار"
	switch input.Action {
	case "return":
		nextStatus = string(domain.ProductChangesRequested)
		eventAction = "برگشت برای اصلاح"
	case "reject":
		nextStatus = string(domain.ProductRejected)
		eventAction = "رد محصول"
	}
	if input.Action == "approve" && publishedVersionID != "" && publishedVersionID != versionID {
		if _, err := tx.Exec(r.Context(), "UPDATE product_versions SET status='superseded', updated_at=now() WHERE id=$1 AND status='published'", publishedVersionID); err != nil {
			s.databaseError(w, "supersede previous published product version", err)
			return
		}
	}
	if _, err := tx.Exec(r.Context(), `
		UPDATE product_versions
		SET status=$2, reviewer_id=$3, reviewer_note=$4, reviewed_at=now(), published_at=CASE WHEN $2='published' THEN now() ELSE published_at END, updated_at=now()
		WHERE id=$1`, versionID, nextStatus, user.ID, input.Note); err != nil {
		s.databaseError(w, "save product review decision", err)
		return
	}
	if input.Action == "approve" {
		if _, err := tx.Exec(r.Context(), `
			UPDATE products SET published_version_id=$2, updated_at=now() WHERE id=$1`, productID, versionID); err != nil {
			s.databaseError(w, "publish product version", err)
			return
		}
		if err := refreshPublishedMedia(r.Context(), tx, organizationID); err != nil {
			s.databaseError(w, "publish product media", err)
			return
		}
	}
	if input.Action == "return" {
		eventAction = "برگشت برای اصلاح"
	}
	if err := addReviewEvent(r.Context(), tx, productID, versionID, user, eventAction, input.Note); err != nil {
		s.databaseError(w, "record moderation decision", err)
		return
	}
	title := "وضعیت محصول به‌روز شد"
	body := "ناظر دربارهٔ نسخهٔ محصول تصمیم گرفته است."
	switch input.Action {
	case "approve":
		title, body = "محصول منتشر شد", "محصول شما پس از بررسی در پروداکتر منتشر شد."
	case "return":
		title, body = "محصول نیاز به اصلاح دارد", input.Note
	case "reject":
		title, body = "محصول رد شد", input.Note
	}
	if err := notifyOrganizationOwners(r.Context(), tx, organizationID, "product_review", title, body, "/organ"); err != nil {
		s.databaseError(w, "notify product submitter", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.databaseError(w, "commit product moderation", err)
		return
	}
	var raw []byte
	err = s.db.QueryRow(r.Context(), managedProductSelect+" WHERE p.slug=$1", r.PathValue("slug")).Scan(&raw)
	if err != nil {
		s.databaseError(w, "read moderated product", err)
		return
	}
	writeRawJSON(w, http.StatusOK, raw)
}

func (s *Server) editProductAsModerator(w http.ResponseWriter, r *http.Request, user domain.User) {
	var payload domain.ProductPayload
	if err := decodeJSON(w, r, &payload, 1<<20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", badJSONMessage(err))
		return
	}
	payload = normalizeProductPayload(payload)
	if err := domain.ValidateProduct(payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_product", err.Error())
		return
	}
	invalidCategory, categoryErr := s.invalidProductCategory(r.Context(), payload.Categories)
	if categoryErr != nil {
		s.databaseError(w, "validate product categories", categoryErr)
		return
	}
	if invalidCategory != "" {
		writeError(w, http.StatusBadRequest, "invalid_category", "دسته‌بندی ناشناخته است: "+invalidCategory)
		return
	}
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_product", "اطلاعات محصول معتبر نیست")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.databaseError(w, "begin moderator product edit", err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var productID, organizationID, currentVersionID, currentStatus string
	var versionNo int
	err = tx.QueryRow(r.Context(), `
		SELECT p.id::text,p.organization_id::text,p.current_version_id::text,pv.status,pv.version_no
		FROM products p JOIN product_versions pv ON pv.id=p.current_version_id
		WHERE p.slug=$1 FOR UPDATE OF p,pv`, r.PathValue("slug")).Scan(
		&productID, &organizationID, &currentVersionID, &currentStatus, &versionNo,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "product_not_found", "محصول پیدا نشد")
		return
	}
	if err != nil {
		s.databaseError(w, "load product for moderator edit", err)
		return
	}
	versionID := currentVersionID
	if currentStatus == string(domain.ProductPublished) || currentStatus == string(domain.ProductUnpublished) ||
		currentStatus == string(domain.ProductRejected) || currentStatus == string(domain.ProductChangesRequested) {
		err = tx.QueryRow(r.Context(), `
			INSERT INTO product_versions(product_id,version_no,payload,status,created_by,reviewer_id,submitted_at)
			VALUES($1,$2,$3::jsonb,'in_review',$4,$4,now()) RETURNING id::text`,
			productID, versionNo+1, rawPayload, user.ID).Scan(&versionID)
		if err == nil {
			_, err = tx.Exec(r.Context(), `
				UPDATE products SET current_version_id=$2,updated_at=now() WHERE id=$1`, productID, versionID)
		}
	} else if currentStatus == string(domain.ProductSubmitted) || currentStatus == string(domain.ProductInReview) {
		_, err = tx.Exec(r.Context(), `
			UPDATE product_versions
			SET payload=$2::jsonb,status='in_review',reviewer_id=$3,reviewer_note='',reviewed_at=NULL,updated_at=now()
			WHERE id=$1`, currentVersionID, rawPayload, user.ID)
	} else {
		writeError(w, http.StatusConflict, "invalid_product_status", "این وضعیت محصول برای ویرایش ناظر قابل‌قبول نیست")
		return
	}
	if err != nil {
		s.databaseError(w, "save moderator product edit", err)
		return
	}
	if err := addReviewEvent(r.Context(), tx, productID, versionID, user, "ویرایش محتوای محصول توسط ناظر", ""); err != nil {
		s.databaseError(w, "record moderator product edit", err)
		return
	}
	if err := notifyOrganizationOwners(r.Context(), tx, organizationID, "product_updated", "محتوای محصول ویرایش شد", "ناظر محتوای محصول را ویرایش کرده است.", "/moderator"); err != nil {
		s.databaseError(w, "notify product owners of moderation edit", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.databaseError(w, "commit moderator product edit", err)
		return
	}
	raw, err := s.managedProduct(r.Context(), r.PathValue("slug"), "")
	if err != nil {
		// Moderator responses use the same product representation without member scoping.
		err = s.db.QueryRow(r.Context(), managedProductSelect+" WHERE p.slug=$1", r.PathValue("slug")).Scan(&raw)
	}
	if err != nil {
		s.databaseError(w, "read moderator-edited product", err)
		return
	}
	writeRawJSON(w, http.StatusOK, raw)
}

func (s *Server) unpublishProduct(w http.ResponseWriter, r *http.Request, user domain.User) {
	var input struct {
		Note string `json:"note"`
	}
	if err := decodeJSON(w, r, &input, 16<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", badJSONMessage(err))
		return
	}
	input.Note = strings.TrimSpace(input.Note)
	if len([]rune(input.Note)) > 400 {
		writeError(w, http.StatusBadRequest, "invalid_note", "یادداشت حداکثر ۴۰۰ نویسه باشد")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.databaseError(w, "begin product unpublish", err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var productID, organizationID, versionID string
	err = tx.QueryRow(r.Context(), `
		SELECT p.id::text,p.organization_id::text,p.published_version_id::text
		FROM products p JOIN product_versions pv ON pv.id=p.published_version_id
		WHERE p.slug=$1 FOR UPDATE OF p,pv`, r.PathValue("slug")).Scan(&productID, &organizationID, &versionID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "published_product_not_found", "محصول منتشرشده پیدا نشد")
		return
	}
	if err != nil {
		s.databaseError(w, "load product for unpublish", err)
		return
	}
	if _, err := tx.Exec(r.Context(), "UPDATE product_versions SET status='unpublished', reviewer_id=$2, reviewer_note=$3, reviewed_at=now(), updated_at=now() WHERE id=$1", versionID, user.ID, input.Note); err != nil {
		s.databaseError(w, "mark product unpublished", err)
		return
	}
	if _, err := tx.Exec(r.Context(), "UPDATE products SET published_version_id=NULL, updated_at=now() WHERE id=$1", productID); err != nil {
		s.databaseError(w, "remove product from public catalog", err)
		return
	}
	if err := refreshPublishedMedia(r.Context(), tx, organizationID); err != nil {
		s.databaseError(w, "hide unpublished product media", err)
		return
	}
	if err := addReviewEvent(r.Context(), tx, productID, versionID, user, "خروج محصول از انتشار", input.Note); err != nil {
		s.databaseError(w, "record unpublish event", err)
		return
	}
	if err := notifyOrganizationOwners(r.Context(), tx, organizationID, "product_unpublished", "محصول از انتشار خارج شد", input.Note, "/organ"); err != nil {
		s.databaseError(w, "notify product owner about unpublish", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.databaseError(w, "commit unpublish", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "unpublished"})
}

func notifyUserTx(ctx context.Context, tx pgx.Tx, userID, kind, title, body, url string) error {
	if strings.TrimSpace(userID) == "" {
		return nil
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO notifications(user_id,kind,title,body,url) VALUES($1,$2,$3,$4,$5)`,
		userID, kind, title, body, url)
	return err
}

func notifyOrganizationOwners(ctx context.Context, tx pgx.Tx, organizationID, kind, title, body, url string) error {
	rows, err := tx.Query(ctx, `
		SELECT user_id::text FROM organization_memberships
		WHERE organization_id=$1 AND role IN ('owner','admin')`, organizationID)
	if err != nil {
		return err
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	rows.Close()
	for _, id := range ids {
		if err := notifyUserTx(ctx, tx, id, kind, title, body, url); err != nil {
			return err
		}
	}
	return nil
}
