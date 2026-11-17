package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"productor-api/internal/domain"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const managedProductSelect = `
SELECT jsonb_build_object(
	'id', p.id::text,
	'slug', p.slug,
	'name', COALESCE(pv.payload->>'name', ''),
	'logo', COALESCE(pv.payload->>'logo', ''),
	'tagline', COALESCE(pv.payload->>'tagline', ''),
	'description', COALESCE(pv.payload->>'description', ''),
	'org', jsonb_build_object(
		'id', o.id::text, 'slug', o.slug, 'name', o.name, 'logo', o.logo,
		'bio', o.bio, 'website', o.website, 'links', o.links,
		'productCount', (SELECT COUNT(*)::int FROM products op WHERE op.organization_id = o.id AND op.published_version_id IS NOT NULL)
	),
	'mainCategory', COALESCE(pv.payload->>'mainCategory', ''),
	'categories', COALESCE(pv.payload->'categories', '[]'::jsonb),
	'platforms', COALESCE(pv.payload->'platforms', '[]'::jsonb),
	'pricing', COALESCE(pv.payload->>'pricing', ''),
	'openSource', COALESCE((pv.payload->>'openSource')::boolean, FALSE),
	'repoUrl', pv.payload->'repoUrl',
	'website', COALESCE(pv.payload->>'website', ''),
	'features', COALESCE(pv.payload->'features', '[]'::jsonb),
	'pros', COALESCE(pv.payload->'pros', '[]'::jsonb),
	'cons', COALESCE(pv.payload->'cons', '[]'::jsonb),
	'media', COALESCE(pv.payload->'media', '[]'::jsonb),
	'userTags', COALESCE(pv.payload->'userTags', '[]'::jsonb),
	'similar', COALESCE(pv.payload->'similar', '[]'::jsonb),
	'audience', COALESCE(pv.payload->>'audience', ''),
	'votes', jsonb_build_object('up', (SELECT COUNT(*)::int FROM votes v WHERE v.product_id = p.id)),
	'status', pv.status,
	'submittedAt', pv.submitted_at,
	'reviewerNote', pv.reviewer_note,
	'history', COALESCE((
		SELECT jsonb_agg(jsonb_build_object('at', e.created_at, 'actor', e.actor_name, 'action', e.action, 'note', NULLIF(e.note, '')) ORDER BY e.created_at, e.id)
		FROM review_events e WHERE e.product_id = p.id
	), '[]'::jsonb)
) AS product_json
FROM products p
JOIN product_versions pv ON pv.id = p.current_version_id
JOIN organizations o ON o.id = p.organization_id
`

type createProductInput struct {
	domain.ProductPayload
	Slug string `json:"slug,omitempty"`
	Mode string `json:"mode,omitempty"`
}

func (s *Server) createProduct(w http.ResponseWriter, r *http.Request, user domain.User) {
	orgSlug := r.PathValue("slug")
	role, err := s.organizationRole(r.Context(), user.ID, orgSlug)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		s.databaseError(w, "check organization membership for product", err)
		return
	}
	if errors.Is(err, pgx.ErrNoRows) && user.Role != domain.RoleAdmin {
		writeError(w, http.StatusForbidden, "forbidden", "برای ثبت محصول باید عضو ارگان باشید")
		return
	}
	if err == nil && role != "owner" && role != "admin" && role != "editor" && user.Role != domain.RoleAdmin {
		writeError(w, http.StatusForbidden, "forbidden", "اجازهٔ ثبت محصول در این ارگان را ندارید")
		return
	}

	var input createProductInput
	if err := decodeJSON(w, r, &input, 1<<20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", badJSONMessage(err))
		return
	}
	payload := normalizeProductPayload(input.ProductPayload)
	if err := domain.ValidateProductDraft(payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_product", err.Error())
		return
	}
	mode := input.Mode
	if mode == "" {
		mode = "draft"
	}
	if mode != "draft" && mode != "submit" {
		writeError(w, http.StatusBadRequest, "invalid_mode", "حالت محصول باید draft یا submit باشد")
		return
	}
	if mode == "submit" {
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
	}
	slug, err := newProductSlug(input.Slug, payload.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "ساخت نشانی محصول انجام نشد")
		return
	}
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_product", "اطلاعات محصول معتبر نیست")
		return
	}
	status := string(domain.ProductDraft)
	action := "ذخیرهٔ پیش‌نویس"
	if mode == "submit" {
		status = string(domain.ProductSubmitted)
		action = "ارسال برای بررسی"
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.databaseError(w, "begin create product", err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var productID, versionID string
	err = tx.QueryRow(r.Context(), `
		INSERT INTO products(organization_id, slug)
		SELECT id, $2 FROM organizations WHERE slug = $1
		RETURNING id::text`, orgSlug, slug).Scan(&productID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "organization_not_found", "ارگان پیدا نشد")
		return
	}
	if isUniqueViolation(err) {
		writeError(w, http.StatusConflict, "slug_taken", "این نشانی محصول قبلاً استفاده شده است")
		return
	}
	if err != nil {
		s.databaseError(w, "insert product", err)
		return
	}
	err = tx.QueryRow(r.Context(), `
		INSERT INTO product_versions(product_id, version_no, payload, status, created_by, submitted_at)
		VALUES ($1, 1, $2::jsonb, $3, $4, CASE WHEN $3 = 'submitted' THEN now() ELSE now() END)
		RETURNING id::text`, productID, rawPayload, status, user.ID).Scan(&versionID)
	if err != nil {
		s.databaseError(w, "insert initial product version", err)
		return
	}
	if _, err := tx.Exec(r.Context(), "UPDATE products SET current_version_id = $2 WHERE id = $1", productID, versionID); err != nil {
		s.databaseError(w, "set current product version", err)
		return
	}
	if mode == "submit" {
		if err := addReviewEvent(r.Context(), tx, productID, versionID, user, action, ""); err != nil {
			s.databaseError(w, "record product submission", err)
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.databaseError(w, "commit product", err)
		return
	}
	raw, err := s.managedProduct(r.Context(), slug, user.ID)
	if err != nil {
		s.databaseError(w, "read created product", err)
		return
	}
	writeRawJSON(w, http.StatusCreated, raw)
}

func (s *Server) myProducts(w http.ResponseWriter, r *http.Request, user domain.User) {
	limit, offset := pagination(r)
	orgSlug := trimmed(r.URL.Query().Get("org"), 100)
	query := managedProductSelect + `
WHERE EXISTS (
	SELECT 1 FROM organization_memberships m
	JOIN organizations mo ON mo.id = m.organization_id
	WHERE m.organization_id = p.organization_id AND m.user_id = $1
)
AND ($2 = '' OR o.slug = $2)
ORDER BY pv.updated_at DESC, p.id
LIMIT $3 OFFSET $4`
	rows, err := s.db.Query(r.Context(), query, user.ID, orgSlug, limit, offset)
	if err != nil {
		s.databaseError(w, "list managed products", err)
		return
	}
	items, err := scanProducts(rows)
	if err != nil {
		s.databaseError(w, "scan managed products", err)
		return
	}
	var total int
	if err := s.db.QueryRow(r.Context(), `
		SELECT COUNT(*) FROM products p
		JOIN organization_memberships m ON m.organization_id = p.organization_id
		JOIN organizations o ON o.id = p.organization_id
		WHERE m.user_id = $1 AND ($2 = '' OR o.slug = $2)`, user.ID, orgSlug).Scan(&total); err != nil {
		s.databaseError(w, "count managed products", err)
		return
	}
	writeJSON(w, http.StatusOK, pageResponse[domain.Product]{Items: items, Total: total, Limit: limit, Offset: offset})
}

func (s *Server) organizationManagedProducts(w http.ResponseWriter, r *http.Request, user domain.User) {
	orgSlug := r.PathValue("slug")
	role, err := s.organizationRole(r.Context(), user.ID, orgSlug)
	if errors.Is(err, pgx.ErrNoRows) && user.Role != domain.RoleAdmin {
		writeError(w, http.StatusNotFound, "organization_not_found", "ارگان پیدا نشد")
		return
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		s.databaseError(w, "check organization access", err)
		return
	}
	if err == nil && role != "owner" && role != "admin" && role != "editor" && user.Role != domain.RoleAdmin {
		writeError(w, http.StatusForbidden, "forbidden", "دسترسی به پنل ارگان مجاز نیست")
		return
	}
	limit, offset := pagination(r)
	rows, err := s.db.Query(r.Context(), managedProductSelect+`
WHERE o.slug = $1
ORDER BY pv.updated_at DESC, p.id
LIMIT $2 OFFSET $3`, orgSlug, limit, offset)
	if err != nil {
		s.databaseError(w, "list organization products", err)
		return
	}
	items, err := scanProducts(rows)
	if err != nil {
		s.databaseError(w, "scan organization products", err)
		return
	}
	var total int
	if err := s.db.QueryRow(r.Context(), "SELECT COUNT(*) FROM products p JOIN organizations o ON o.id=p.organization_id WHERE o.slug=$1", orgSlug).Scan(&total); err != nil {
		s.databaseError(w, "count organization products", err)
		return
	}
	writeJSON(w, http.StatusOK, pageResponse[domain.Product]{Items: items, Total: total, Limit: limit, Offset: offset})
}

func (s *Server) getManagedProduct(w http.ResponseWriter, r *http.Request, user domain.User) {
	raw, err := s.managedProduct(r.Context(), r.PathValue("slug"), user.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "product_not_found", "محصول پیدا نشد یا دسترسی ندارید")
		return
	}
	if err != nil {
		s.databaseError(w, "get managed product", err)
		return
	}
	writeRawJSON(w, http.StatusOK, raw)
}

func (s *Server) managedProduct(ctx context.Context, slug, userID string) ([]byte, error) {
	var raw []byte
	err := s.db.QueryRow(ctx, managedProductSelect+`
WHERE p.slug = $1 AND (
	EXISTS (SELECT 1 FROM organization_memberships m WHERE m.organization_id=p.organization_id AND m.user_id=$2) OR
	EXISTS (SELECT 1 FROM users u WHERE u.id::text=$2 AND u.role='admin')
)`, slug, userID).Scan(&raw)
	return raw, err
}

func (s *Server) updateProduct(w http.ResponseWriter, r *http.Request, user domain.User) {
	slug := r.PathValue("slug")
	var payload domain.ProductPayload
	if err := decodeJSON(w, r, &payload, 1<<20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", badJSONMessage(err))
		return
	}
	payload = normalizeProductPayload(payload)
	if err := domain.ValidateProductDraft(payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_product", err.Error())
		return
	}
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_product", "اطلاعات محصول معتبر نیست")
		return
	}

	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.databaseError(w, "begin update product", err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var productID, orgID, currentVersionID, currentStatus string
	var versionNo int
	err = tx.QueryRow(r.Context(), `
		SELECT p.id::text, p.organization_id::text, p.current_version_id::text, pv.status, pv.version_no
		FROM products p
		JOIN product_versions pv ON pv.id = p.current_version_id
		JOIN organization_memberships m ON m.organization_id = p.organization_id AND m.user_id = $2
		WHERE p.slug = $1
		FOR UPDATE OF p, pv`, slug, user.ID).Scan(&productID, &orgID, &currentVersionID, &currentStatus, &versionNo)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "product_not_found", "محصول پیدا نشد یا دسترسی ندارید")
		return
	}
	if err != nil {
		s.databaseError(w, "lock product for update", err)
		return
	}
	if currentStatus == string(domain.ProductSubmitted) || currentStatus == string(domain.ProductInReview) {
		writeError(w, http.StatusConflict, "product_in_review", "تا پایان بررسی امکان ویرایش این نسخه وجود ندارد")
		return
	}
	var versionID string
	if currentStatus == string(domain.ProductDraft) {
		_, err = tx.Exec(r.Context(), `
			UPDATE product_versions SET payload=$2::jsonb, updated_at=now()
			WHERE id=$1`, currentVersionID, rawPayload)
		versionID = currentVersionID
	} else {
		if _, err = tx.Exec(r.Context(), `
			UPDATE product_versions SET status='superseded', updated_at=now()
			WHERE id=$1 AND status IN ('changes_requested','rejected','unpublished')`, currentVersionID); err == nil {
			err = tx.QueryRow(r.Context(), `
				INSERT INTO product_versions(product_id, version_no, payload, status, created_by)
				VALUES ($1,$2,$3::jsonb,'draft',$4)
				RETURNING id::text`, productID, versionNo+1, rawPayload, user.ID).Scan(&versionID)
		}
	}
	if err != nil {
		s.databaseError(w, "save product version", err)
		return
	}
	if versionID != currentVersionID {
		if _, err := tx.Exec(r.Context(), "UPDATE products SET current_version_id=$2, updated_at=now() WHERE id=$1", productID, versionID); err != nil {
			s.databaseError(w, "point to edited product version", err)
			return
		}
	}
	if err := addReviewEvent(r.Context(), tx, productID, versionID, user, "ویرایش پیش‌نویس", ""); err != nil {
		s.databaseError(w, "record product edit", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.databaseError(w, "commit product update", err)
		return
	}
	raw, err := s.managedProduct(r.Context(), slug, user.ID)
	if err != nil {
		s.databaseError(w, "read updated product", err)
		return
	}
	writeRawJSON(w, http.StatusOK, raw)
}

func (s *Server) submitProduct(w http.ResponseWriter, r *http.Request, user domain.User) {
	slug := r.PathValue("slug")
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.databaseError(w, "begin product submission", err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var productID, versionID, status string
	var rawPayload []byte
	err = tx.QueryRow(r.Context(), `
		SELECT p.id::text, pv.id::text, pv.status, pv.payload
		FROM products p
		JOIN product_versions pv ON pv.id=p.current_version_id
		JOIN organization_memberships m ON m.organization_id=p.organization_id AND m.user_id=$2
		WHERE p.slug=$1 FOR UPDATE OF p,pv`, slug, user.ID).Scan(&productID, &versionID, &status, &rawPayload)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "product_not_found", "محصول پیدا نشد یا دسترسی ندارید")
		return
	}
	if err != nil {
		s.databaseError(w, "load product for submission", err)
		return
	}
	if status != string(domain.ProductDraft) {
		writeError(w, http.StatusConflict, "invalid_product_status", "فقط پیش‌نویس قابل ارسال برای بررسی است")
		return
	}
	var payload domain.ProductPayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		s.databaseError(w, "decode product for submission", err)
		return
	}
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
	if _, err := tx.Exec(r.Context(), `
		UPDATE product_versions
		SET status='submitted', submitted_at=now(), reviewer_id=NULL, reviewer_note='', reviewed_at=NULL, updated_at=now()
		WHERE id=$1`, versionID); err != nil {
		s.databaseError(w, "mark product submitted", err)
		return
	}
	if err := addReviewEvent(r.Context(), tx, productID, versionID, user, "ارسال برای بررسی", ""); err != nil {
		s.databaseError(w, "record submission event", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.databaseError(w, "commit product submission", err)
		return
	}
	raw, err := s.managedProduct(r.Context(), slug, user.ID)
	if err != nil {
		s.databaseError(w, "read submitted product", err)
		return
	}
	writeRawJSON(w, http.StatusOK, raw)
}

func (s *Server) invalidProductCategory(ctx context.Context, categories []string) (string, error) {
	const query = "SELECT EXISTS(SELECT 1 FROM category_domains d CROSS JOIN LATERAL jsonb_array_elements_text(d.tags) AS item(label) WHERE item.label=$1)"
	for _, category := range categories {
		var exists bool
		if err := s.db.QueryRow(ctx, query, category).Scan(&exists); err != nil {
			return "", err
		}
		if !exists {
			return category, nil
		}
	}
	return "", nil
}

func normalizeProductPayload(payload domain.ProductPayload) domain.ProductPayload {
	payload.Name = strings.TrimSpace(payload.Name)
	payload.Tagline = strings.TrimSpace(payload.Tagline)
	payload.Description = strings.TrimSpace(payload.Description)
	payload.Logo = strings.TrimSpace(payload.Logo)
	payload.Website = strings.TrimSpace(payload.Website)
	payload.RepoURL = strings.TrimSpace(payload.RepoURL)
	payload.MainCategory = strings.TrimSpace(payload.MainCategory)
	payload.Audience = strings.TrimSpace(payload.Audience)
	payload.Categories = trimStrings(payload.Categories, 10)
	payload.Platforms = trimStrings(payload.Platforms, 10)
	payload.Pros = trimStrings(payload.Pros, 20)
	payload.Cons = trimStrings(payload.Cons, 20)
	payload.Similar = trimStrings(payload.Similar, 20)
	payload.UserTags = trimStrings(payload.UserTags, 20)
	if payload.Categories == nil {
		payload.Categories = []string{}
	}
	if payload.Platforms == nil {
		payload.Platforms = []string{}
	}
	if payload.Features == nil {
		payload.Features = []domain.Feature{}
	}
	if payload.Pros == nil {
		payload.Pros = []string{}
	}
	if payload.Cons == nil {
		payload.Cons = []string{}
	}
	if payload.Media == nil {
		payload.Media = []domain.Media{}
	}
	if payload.Similar == nil {
		payload.Similar = []string{}
	}
	if payload.UserTags == nil {
		payload.UserTags = []string{}
	}
	for i := range payload.Features {
		payload.Features[i].Key = strings.TrimSpace(payload.Features[i].Key)
		payload.Features[i].Label = strings.TrimSpace(payload.Features[i].Label)
		payload.Features[i].Value = strings.TrimSpace(payload.Features[i].Value)
	}
	return payload
}

func trimStrings(values []string, max int) []string {
	result := make([]string, 0, min(len(values), max))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(result) >= max {
			continue
		}
		result = append(result, value)
	}
	return result
}

func newProductSlug(preferred, name string) (string, error) {
	base := domain.Slug(preferred, "")
	if base == "" {
		base = domain.Slug(name, "product")
	}
	if preferred != "" {
		return base, nil
	}
	random := make([]byte, 3)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return base + "-" + hex.EncodeToString(random), nil
}

func addReviewEvent(ctx context.Context, tx pgx.Tx, productID, versionID string, user domain.User, action, note string) error {
	actorName := "صاحب ارگان"
	if user.Role == domain.RoleModerator || user.Role == domain.RoleAdmin {
		actorName = "ناظر"
	} else if user.Phone != "" {
		actorName = "کاربر " + maskPhoneForDisplay(user.Phone)
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO review_events(product_id, version_id, actor_id, actor_name, actor_role, action, note)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`, productID, nullIfEmpty(versionID), user.ID, actorName, string(user.Role), action, note)
	return err
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func validateTextLength(value string, minRunes, maxRunes int) bool {
	length := utf8.RuneCountInString(strings.TrimSpace(value))
	return length >= minRunes && length <= maxRunes
}

func unusedProductContext(ctx context.Context) context.Context { return ctx }
