package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"productor-api/internal/domain"

	"github.com/jackc/pgx/v5"
)

var adminKeyPattern = regexp.MustCompile("^[a-z0-9]+(?:-[a-z0-9]+)*$")
var adminIconPattern = regexp.MustCompile("^[A-Za-z][A-Za-z0-9._-]{0,79}$")

type adminTopic struct {
	Slug     string `json:"slug"`
	Label    string `json:"label"`
	Tone     int    `json:"tone"`
	Icon     string `json:"icon"`
	Position int    `json:"position"`
	IsActive bool   `json:"isActive"`
}

func (s *Server) adminProducts(w http.ResponseWriter, r *http.Request, user domain.User) {
	s.adminWithDefaultStatus(w, r, user, "all", s.moderationProducts)
}

func (s *Server) adminComments(w http.ResponseWriter, r *http.Request, user domain.User) {
	s.adminWithDefaultStatus(w, r, user, "all", s.moderationComments)
}

func (s *Server) adminWithDefaultStatus(w http.ResponseWriter, r *http.Request, user domain.User, status string, handler func(http.ResponseWriter, *http.Request, domain.User)) {
	if r.URL.Query().Get("status") == "" {
		u := *r.URL
		q := u.Query()
		q.Set("status", status)
		u.RawQuery = q.Encode()
		r = r.Clone(r.Context())
		r.URL = &u
	}
	handler(w, r, user)
}

func (s *Server) adminOrganizations(w http.ResponseWriter, r *http.Request, _ domain.User) {
	limit, offset := pagination(r)
	q := trimmed(r.URL.Query().Get("q"), 120)
	rows, err := s.db.Query(r.Context(), `
		SELECT jsonb_build_object('id',o.id::text,'slug',o.slug,'name',o.name,'logo',o.logo,
			'bio',o.bio,'website',o.website,'links',o.links,'createdAt',o.created_at,
			'productCount',(SELECT COUNT(*)::int FROM products p WHERE p.organization_id=o.id AND p.published_version_id IS NOT NULL),
			'pendingProductCount',(SELECT COUNT(*)::int FROM products p JOIN product_versions pv ON pv.id=p.current_version_id WHERE p.organization_id=o.id AND pv.status IN ('submitted','in_review')),
			'memberCount',(SELECT COUNT(*)::int FROM organization_memberships m WHERE m.organization_id=o.id))
		FROM organizations o WHERE $1='' OR o.name ILIKE '%'||$1||'%' OR o.slug ILIKE '%'||$1||'%'
		ORDER BY o.created_at DESC,o.id LIMIT $2 OFFSET $3`, q, limit, offset)
	if err != nil {
		s.databaseError(w, "list admin organizations", err)
		return
	}
	items, err := scanAdminJSONRows(rows)
	if err != nil {
		s.databaseError(w, "scan admin organizations", err)
		return
	}
	var total int
	if err := s.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM organizations o WHERE $1='' OR o.name ILIKE '%'||$1||'%' OR o.slug ILIKE '%'||$1||'%'`, q).Scan(&total); err != nil {
		s.databaseError(w, "count admin organizations", err)
		return
	}
	writeJSON(w, http.StatusOK, pageResponse[map[string]any]{Items: items, Total: total, Limit: limit, Offset: offset})
}

func (s *Server) updateAdminOrganization(w http.ResponseWriter, r *http.Request, actor domain.User) {
	slug := r.PathValue("slug")
	var in organizationInput
	if err := decodeJSON(w, r, &in, 64<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", badJSONMessage(err))
		return
	}
	if in.Slug != "" && in.Slug != slug {
		writeError(w, http.StatusBadRequest, "slug_immutable", "نشانی ارگان از این مسیر قابل تغییر نیست")
		return
	}
	in.Name, in.Logo, in.Bio, in.Website = strings.TrimSpace(in.Name), strings.TrimSpace(in.Logo), strings.TrimSpace(in.Bio), strings.TrimSpace(in.Website)
	if in.Links == nil {
		in.Links = []domain.Link{}
	}
	org := domain.Organization{Name: in.Name, Logo: in.Logo, Bio: in.Bio, Website: in.Website, Links: in.Links}
	if err := domain.ValidateOrganization(org); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_organization", err.Error())
		return
	}
	links, err := json.Marshal(in.Links)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_organization", "پیوندهای ارگان معتبر نیستند")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.databaseError(w, "begin admin organization update", err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	err = tx.QueryRow(r.Context(), `UPDATE organizations SET name=$2,logo=$3,bio=$4,website=$5,links=$6::jsonb,updated_at=now()
		WHERE slug=$1 RETURNING id::text,slug,name,logo,bio,website,links,created_at`, slug, in.Name, in.Logo, in.Bio, in.Website, links).
		Scan(&org.ID, &org.Slug, &org.Name, &org.Logo, &org.Bio, &org.Website, &links, &org.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "organization_not_found", "ارگان پیدا نشد")
		return
	}
	if err != nil {
		s.databaseError(w, "update admin organization", err)
		return
	}
	if err := json.Unmarshal(links, &org.Links); err != nil {
		s.databaseError(w, "decode organization links", err)
		return
	}
	if err := tx.QueryRow(r.Context(), "SELECT COUNT(*)::int FROM products WHERE organization_id=$1 AND published_version_id IS NOT NULL", org.ID).Scan(&org.ProductCount); err != nil {
		s.databaseError(w, "count organization products", err)
		return
	}
	if err := recordAdminAudit(r, tx, actor.ID, "organization_updated", map[string]string{"slug": slug}); err != nil {
		s.databaseError(w, "audit organization update", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.databaseError(w, "commit organization update", err)
		return
	}
	writeJSON(w, http.StatusOK, org)
}

func (s *Server) adminCategories(w http.ResponseWriter, r *http.Request, _ domain.User) {
	s.listCategories(w, r)
}

type categoryCreateInput struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Tags     []string `json:"tags"`
	Position *int     `json:"position"`
}
type categoryPatchInput struct {
	Name     *string   `json:"name"`
	Tags     *[]string `json:"tags"`
	Position *int      `json:"position"`
}

func (s *Server) createAdminCategory(w http.ResponseWriter, r *http.Request, actor domain.User) {
	var in categoryCreateInput
	if err := decodeJSON(w, r, &in, 32<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", badJSONMessage(err))
		return
	}
	in.ID, in.Name = strings.TrimSpace(in.ID), strings.TrimSpace(in.Name)
	tags, err := normalizeAdminTags(in.Tags)
	if err != nil || !adminKeyPattern.MatchString(in.ID) || utf8.RuneCountInString(in.ID) < 2 || utf8.RuneCountInString(in.ID) > 80 || utf8.RuneCountInString(in.Name) < 2 || utf8.RuneCountInString(in.Name) > 120 {
		writeError(w, http.StatusBadRequest, "invalid_category", "شناسه، نام یا برچسب‌های دسته‌بندی معتبر نیست")
		return
	}
	position := 0
	if in.Position != nil {
		position = *in.Position
	}
	if position < 0 || position > 32767 {
		writeError(w, http.StatusBadRequest, "invalid_category", "جایگاه دسته‌بندی معتبر نیست")
		return
	}
	tagsJSON, _ := json.Marshal(tags)
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.databaseError(w, "begin category create", err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	if in.Position == nil {
		if err := tx.QueryRow(r.Context(), "SELECT COALESCE(MAX(position),0)+1 FROM category_domains").Scan(&position); err != nil {
			s.databaseError(w, "choose category position", err)
			return
		}
	}
	if position > 32767 {
		writeError(w, http.StatusConflict, "category_position_limit", "ظرفیت جایگاه دسته‌بندی پر شده است")
		return
	}
	_, err = tx.Exec(r.Context(), "INSERT INTO category_domains(id,name,tags,position) VALUES($1,$2,$3::jsonb,$4)", in.ID, in.Name, tagsJSON, position)
	if err != nil {
		if strings.Contains(err.Error(), "category_domains_pkey") || strings.Contains(err.Error(), "category_domains_name_key") {
			writeError(w, http.StatusConflict, "category_exists", "شناسه یا نام دسته‌بندی تکراری است")
			return
		}
		s.databaseError(w, "create category", err)
		return
	}
	if err := recordAdminAudit(r, tx, actor.ID, "category_created", map[string]string{"id": in.ID, "name": in.Name}); err != nil {
		s.databaseError(w, "audit category create", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.databaseError(w, "commit category create", err)
		return
	}
	writeJSON(w, http.StatusCreated, categoryDomain{ID: in.ID, Name: in.Name, Tags: tags, Position: position})
}

func (s *Server) updateAdminCategory(w http.ResponseWriter, r *http.Request, actor domain.User) {
	id := r.PathValue("id")
	var in categoryPatchInput
	if err := decodeJSON(w, r, &in, 32<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", badJSONMessage(err))
		return
	}
	if in.Name == nil && in.Tags == nil && in.Position == nil {
		writeError(w, http.StatusBadRequest, "empty_update", "حداقل یک فیلد برای تغییر لازم است")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.databaseError(w, "begin category update", err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var item categoryDomain
	var raw []byte
	err = tx.QueryRow(r.Context(), "SELECT id,name,tags,position FROM category_domains WHERE id=$1 FOR UPDATE", id).Scan(&item.ID, &item.Name, &raw, &item.Position)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "category_not_found", "دسته‌بندی پیدا نشد")
		return
	}
	if err != nil {
		s.databaseError(w, "load category for update", err)
		return
	}
	if err := json.Unmarshal(raw, &item.Tags); err != nil {
		s.databaseError(w, "decode category tags", err)
		return
	}
	if in.Name != nil {
		item.Name = strings.TrimSpace(*in.Name)
		if utf8.RuneCountInString(item.Name) < 2 || utf8.RuneCountInString(item.Name) > 120 {
			writeError(w, http.StatusBadRequest, "invalid_category", "نام دسته‌بندی باید بین ۲ تا ۱۲۰ نویسه باشد")
			return
		}
	}
	if in.Tags != nil {
		next, err := normalizeAdminTags(*in.Tags)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_category", err.Error())
			return
		}
		for _, old := range item.Tags {
			if containsAdminString(next, old) {
				continue
			}
			var used bool
			if err := tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM product_versions WHERE COALESCE(payload->'categories','[]'::jsonb) ? $1)", old).Scan(&used); err != nil {
				s.databaseError(w, "check category tag usage", err)
				return
			}
			if used {
				writeError(w, http.StatusConflict, "category_tag_in_use", "برچسب استفاده‌شده در نسخهٔ محصول را نمی‌توان حذف کرد")
				return
			}
		}
		item.Tags = next
	}
	if in.Position != nil {
		item.Position = *in.Position
	}
	if item.Position < 0 || item.Position > 32767 {
		writeError(w, http.StatusBadRequest, "invalid_category", "جایگاه دسته‌بندی معتبر نیست")
		return
	}
	tagsJSON, _ := json.Marshal(item.Tags)
	_, err = tx.Exec(r.Context(), "UPDATE category_domains SET name=$2,tags=$3::jsonb,position=$4 WHERE id=$1", item.ID, item.Name, tagsJSON, item.Position)
	if err != nil {
		if strings.Contains(err.Error(), "category_domains_name_key") {
			writeError(w, http.StatusConflict, "category_name_exists", "نام دسته‌بندی تکراری است")
			return
		}
		s.databaseError(w, "update category", err)
		return
	}
	if err := recordAdminAudit(r, tx, actor.ID, "category_updated", map[string]string{"id": item.ID}); err != nil {
		s.databaseError(w, "audit category update", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.databaseError(w, "commit category update", err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) adminTopics(w http.ResponseWriter, r *http.Request, _ domain.User) {
	rows, err := s.db.Query(r.Context(), "SELECT jsonb_build_object('slug',slug,'label',label,'tone',tone,'icon',icon,'position',position,'isActive',is_active) FROM topics ORDER BY position,slug")
	if err != nil {
		s.databaseError(w, "list admin topics", err)
		return
	}
	defer rows.Close()
	items := make([]adminTopic, 0)
	for rows.Next() {
		var b []byte
		var item adminTopic
		if err := rows.Scan(&b); err != nil {
			s.databaseError(w, "scan admin topic", err)
			return
		}
		if err := json.Unmarshal(b, &item); err != nil {
			s.databaseError(w, "decode admin topic", err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		s.databaseError(w, "read admin topics", err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

type topicCreateInput struct {
	Slug     string `json:"slug"`
	Label    string `json:"label"`
	Tone     int    `json:"tone"`
	Icon     string `json:"icon"`
	Position *int   `json:"position"`
}
type topicPatchInput struct {
	Tone     *int    `json:"tone"`
	Icon     *string `json:"icon"`
	Position *int    `json:"position"`
	IsActive *bool   `json:"isActive"`
}

func (s *Server) createAdminTopic(w http.ResponseWriter, r *http.Request, actor domain.User) {
	var in topicCreateInput
	if err := decodeJSON(w, r, &in, 16<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", badJSONMessage(err))
		return
	}
	in.Slug, in.Label, in.Icon = strings.TrimSpace(in.Slug), strings.TrimSpace(in.Label), strings.TrimSpace(in.Icon)
	position := 0
	if in.Position != nil {
		position = *in.Position
	}
	if !adminKeyPattern.MatchString(in.Slug) || utf8.RuneCountInString(in.Slug) < 2 || utf8.RuneCountInString(in.Slug) > 80 || utf8.RuneCountInString(in.Label) < 2 || utf8.RuneCountInString(in.Label) > 120 || in.Tone < 1 || in.Tone > 7 || !adminIconPattern.MatchString(in.Icon) || position < 0 || position > 32767 {
		writeError(w, http.StatusBadRequest, "invalid_topic", "موضوع، رنگ، آیکن یا جایگاه معتبر نیست")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.databaseError(w, "begin topic create", err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	if in.Position == nil {
		if err := tx.QueryRow(r.Context(), "SELECT COALESCE(MAX(position),0)+1 FROM topics").Scan(&position); err != nil {
			s.databaseError(w, "choose topic position", err)
			return
		}
	}
	if position > 32767 {
		writeError(w, http.StatusConflict, "topic_position_limit", "ظرفیت جایگاه موضوع پر شده است")
		return
	}
	_, err = tx.Exec(r.Context(), "INSERT INTO topics(slug,label,tone,icon,position,is_active) VALUES($1,$2,$3,$4,$5,TRUE)", in.Slug, in.Label, in.Tone, in.Icon, position)
	if err != nil {
		if strings.Contains(err.Error(), "topics_pkey") || strings.Contains(err.Error(), "topics_label_key") {
			writeError(w, http.StatusConflict, "topic_exists", "نشانی یا نام موضوع تکراری است")
			return
		}
		s.databaseError(w, "create topic", err)
		return
	}
	if err := recordAdminAudit(r, tx, actor.ID, "topic_created", map[string]string{"slug": in.Slug}); err != nil {
		s.databaseError(w, "audit topic create", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.databaseError(w, "commit topic create", err)
		return
	}
	writeJSON(w, http.StatusCreated, adminTopic{Slug: in.Slug, Label: in.Label, Tone: in.Tone, Icon: in.Icon, Position: position, IsActive: true})
}

func (s *Server) updateAdminTopic(w http.ResponseWriter, r *http.Request, actor domain.User) {
	slug := r.PathValue("slug")
	var in topicPatchInput
	if err := decodeJSON(w, r, &in, 16<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", badJSONMessage(err))
		return
	}
	if in.Tone == nil && in.Icon == nil && in.Position == nil && in.IsActive == nil {
		writeError(w, http.StatusBadRequest, "empty_update", "حداقل یک فیلد برای تغییر لازم است")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.databaseError(w, "begin topic update", err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var item adminTopic
	err = tx.QueryRow(r.Context(), "SELECT slug,label,tone,icon,position,is_active FROM topics WHERE slug=$1 FOR UPDATE", slug).Scan(&item.Slug, &item.Label, &item.Tone, &item.Icon, &item.Position, &item.IsActive)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "topic_not_found", "موضوع پیدا نشد")
		return
	}
	if err != nil {
		s.databaseError(w, "load topic for update", err)
		return
	}
	if in.Tone != nil {
		item.Tone = *in.Tone
	}
	if in.Icon != nil {
		item.Icon = strings.TrimSpace(*in.Icon)
	}
	if in.Position != nil {
		item.Position = *in.Position
	}
	if in.IsActive != nil {
		item.IsActive = *in.IsActive
	}
	if item.Tone < 1 || item.Tone > 7 || !adminIconPattern.MatchString(item.Icon) || item.Position < 0 || item.Position > 32767 {
		writeError(w, http.StatusBadRequest, "invalid_topic", "رنگ، آیکن یا جایگاه موضوع معتبر نیست")
		return
	}
	_, err = tx.Exec(r.Context(), "UPDATE topics SET tone=$2,icon=$3,position=$4,is_active=$5 WHERE slug=$1", item.Slug, item.Tone, item.Icon, item.Position, item.IsActive)
	if err != nil {
		s.databaseError(w, "update topic", err)
		return
	}
	if err := recordAdminAudit(r, tx, actor.ID, "topic_updated", map[string]any{"slug": slug, "isActive": item.IsActive}); err != nil {
		s.databaseError(w, "audit topic update", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.databaseError(w, "commit topic update", err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deactivateAdminTopic(w http.ResponseWriter, r *http.Request, actor domain.User) {
	slug := r.PathValue("slug")
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.databaseError(w, "begin topic deactivation", err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	tag, err := tx.Exec(r.Context(), "UPDATE topics SET is_active=FALSE WHERE slug=$1 AND is_active=TRUE", slug)
	if err != nil {
		s.databaseError(w, "deactivate topic", err)
		return
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err := tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM topics WHERE slug=$1)", slug).Scan(&exists); err != nil {
			s.databaseError(w, "find topic", err)
			return
		}
		if !exists {
			writeError(w, http.StatusNotFound, "topic_not_found", "موضوع پیدا نشد")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "inactive"})
		return
	}
	if err := recordAdminAudit(r, tx, actor.ID, "topic_deactivated", map[string]string{"slug": slug}); err != nil {
		s.databaseError(w, "audit topic deactivation", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.databaseError(w, "commit topic deactivation", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "inactive"})
}

func normalizeAdminTags(values []string) ([]string, error) {
	if len(values) > 150 {
		return nil, errors.New("دسته‌بندی حداکثر ۱۵۰ برچسب می‌تواند داشته باشد")
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		n := utf8.RuneCountInString(value)
		if n == 0 || n > 100 {
			return nil, errors.New("طول هر برچسب باید بین ۱ تا ۱۰۰ نویسه باشد")
		}
		if _, ok := seen[value]; ok {
			return nil, errors.New("برچسب تکراری مجاز نیست")
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out, nil
}

func containsAdminString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func scanAdminJSONRows(rows pgx.Rows) ([]map[string]any, error) {
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var raw []byte
		var item map[string]any
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func recordAdminAudit(r *http.Request, tx pgx.Tx, actorID, action string, details any) error {
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = tx.Exec(r.Context(), "INSERT INTO admin_audit_events(actor_id,action,details) VALUES($1,$2,$3::jsonb)", actorID, action, raw)
	return err
}
