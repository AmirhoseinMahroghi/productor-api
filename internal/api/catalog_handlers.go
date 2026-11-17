package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"productor-api/internal/domain"

	"github.com/jackc/pgx/v5"
)

const publicProductSelect = `
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
		'productCount', (SELECT COUNT(*)::int FROM products op WHERE op.organization_id=o.id AND op.published_version_id IS NOT NULL)
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
	'votes', jsonb_build_object('up', (SELECT COUNT(*)::int FROM votes v WHERE v.product_id=p.id)),
	'status', 'published',
	'submittedAt', pv.submitted_at,
	'reviewerNote', '',
	'history', COALESCE((
		SELECT jsonb_agg(jsonb_build_object('at',e.created_at,'actor',e.actor_name,'action',e.action,'note',NULLIF(e.note,'')) ORDER BY e.created_at,e.id)
		FROM review_events e WHERE e.product_id=p.id AND e.action IN ('ارسال برای بررسی','تأیید و انتشار','برگشت برای اصلاح','رد محصول','خروج محصول از انتشار')
	), '[]'::jsonb)
) AS product_json
FROM products p
JOIN product_versions pv ON pv.id=p.published_version_id
JOIN organizations o ON o.id=p.organization_id
`

func (s *Server) listProducts(w http.ResponseWriter, r *http.Request) {
	limit, offset := pagination(r)
	q := trimmed(r.URL.Query().Get("q"), 120)
	category := trimmed(r.URL.Query().Get("category"), 120)
	platform := trimmed(r.URL.Query().Get("platform"), 40)
	pricing := trimmed(r.URL.Query().Get("pricing"), 40)
	openSource := trimmed(r.URL.Query().Get("openSource"), 5)
	if openSource != "" && openSource != "true" && openSource != "false" {
		writeError(w, http.StatusBadRequest, "invalid_filter", "فیلتر متن‌باز نامعتبر است")
		return
	}
	org := trimmed(r.URL.Query().Get("org"), 100)
	sortBy := r.URL.Query().Get("sort")
	if sortBy != "votes" && sortBy != "name" {
		sortBy = "recent"
	}
	args := []any{q, category, platform, pricing, openSource, org, sortBy}
	query := publicProductSelect + `
WHERE p.published_version_id IS NOT NULL
  AND ($1 = '' OR pv.payload->>'name' ILIKE '%' || $1 || '%' OR pv.payload->>'tagline' ILIKE '%' || $1 || '%' OR pv.payload->>'description' ILIKE '%' || $1 || '%')
  AND ($2 = '' OR COALESCE(pv.payload->'categories', '[]'::jsonb) ? $2 OR EXISTS (
        SELECT 1 FROM topics t WHERE t.slug=$2 AND COALESCE(pv.payload->'categories','[]'::jsonb) ? t.label
      ))
  AND ($3 = '' OR COALESCE(pv.payload->'platforms','[]'::jsonb) ? $3)
  AND ($4 = '' OR pv.payload->>'pricing'=$4)
  AND ($5 = '' OR pv.payload->>'openSource'=$5)
  AND ($6 = '' OR o.slug=$6)
ORDER BY
  CASE WHEN $7='votes' THEN (SELECT COUNT(*) FROM votes v WHERE v.product_id=p.id) END DESC,
  CASE WHEN $7='name' THEN lower(pv.payload->>'name') END ASC,
  COALESCE(pv.published_at,pv.submitted_at) DESC,p.id ASC
LIMIT $8 OFFSET $9`
	args = append(args, limit, offset)
	rows, err := s.db.Query(r.Context(), query, args...)
	if err != nil {
		s.databaseError(w, "list products", err)
		return
	}
	items, err := scanProducts(rows)
	if err != nil {
		s.databaseError(w, "scan products", err)
		return
	}
	var total int
	if err := s.db.QueryRow(r.Context(), `
		SELECT COUNT(*)
		FROM products p JOIN product_versions pv ON pv.id=p.published_version_id
		JOIN organizations o ON o.id=p.organization_id
		WHERE p.published_version_id IS NOT NULL
		  AND ($1='' OR pv.payload->>'name' ILIKE '%'||$1||'%' OR pv.payload->>'tagline' ILIKE '%'||$1||'%' OR pv.payload->>'description' ILIKE '%'||$1||'%')
		  AND ($2='' OR COALESCE(pv.payload->'categories','[]'::jsonb) ? $2 OR EXISTS (
			SELECT 1 FROM topics t WHERE t.slug=$2 AND COALESCE(pv.payload->'categories','[]'::jsonb) ? t.label
		  ))
		  AND ($3='' OR COALESCE(pv.payload->'platforms','[]'::jsonb) ? $3)
		  AND ($4='' OR pv.payload->>'pricing'=$4)
		  AND ($5='' OR pv.payload->>'openSource'=$5)
		  AND ($6='' OR o.slug=$6)`, q, category, platform, pricing, openSource, org).Scan(&total); err != nil {
		s.databaseError(w, "count products", err)
		return
	}
	writeJSON(w, http.StatusOK, pageResponse[domain.Product]{Items: items, Total: total, Limit: limit, Offset: offset})
}

func (s *Server) getProduct(w http.ResponseWriter, r *http.Request) {
	var raw []byte
	err := s.db.QueryRow(r.Context(), publicProductSelect+`
WHERE p.slug=$1 AND p.published_version_id IS NOT NULL`, r.PathValue("slug")).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "product_not_found", "محصول منتشرشده‌ای با این نشانی پیدا نشد")
		return
	}
	if err != nil {
		s.databaseError(w, "get product", err)
		return
	}
	writeRawJSON(w, http.StatusOK, raw)
}

func (s *Server) listOrganizations(w http.ResponseWriter, r *http.Request) {
	limit, offset := pagination(r)
	q := trimmed(r.URL.Query().Get("q"), 120)
	rows, err := s.db.Query(r.Context(), `
		SELECT jsonb_build_object(
			'id',o.id::text,'slug',o.slug,'name',o.name,'logo',o.logo,
			'bio',o.bio,'website',o.website,'links',o.links,
			'productCount',COUNT(p.id)::int,'createdAt',o.created_at
		)
		FROM organizations o
		LEFT JOIN products p ON p.organization_id=o.id AND p.published_version_id IS NOT NULL
		WHERE ($1='' OR o.name ILIKE '%'||$1||'%' OR o.bio ILIKE '%'||$1||'%')
		GROUP BY o.id
		ORDER BY COUNT(p.id) DESC,o.created_at DESC,o.id
		LIMIT $2 OFFSET $3`, q, limit, offset)
	if err != nil {
		s.databaseError(w, "list organizations", err)
		return
	}
	items, err := scanOrganizations(rows)
	if err != nil {
		s.databaseError(w, "scan organizations", err)
		return
	}
	var total int
	if err := s.db.QueryRow(r.Context(), "SELECT COUNT(*) FROM organizations WHERE ($1='' OR name ILIKE '%'||$1||'%' OR bio ILIKE '%'||$1||'%')", q).Scan(&total); err != nil {
		s.databaseError(w, "count organizations", err)
		return
	}
	writeJSON(w, http.StatusOK, pageResponse[domain.Organization]{Items: items, Total: total, Limit: limit, Offset: offset})
}

func (s *Server) getOrganization(w http.ResponseWriter, r *http.Request) {
	org, err := s.organizationBySlug(r.Context(), r.PathValue("slug"))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "organization_not_found", "ارگانی با این نشانی پیدا نشد")
		return
	}
	if err != nil {
		s.databaseError(w, "get organization", err)
		return
	}
	writeJSON(w, http.StatusOK, org)
}

func (s *Server) organizationBySlug(ctx context.Context, slug string) (domain.Organization, error) {
	var raw []byte
	err := s.db.QueryRow(ctx, `
		SELECT jsonb_build_object(
			'id',o.id::text,'slug',o.slug,'name',o.name,'logo',o.logo,'bio',o.bio,
			'website',o.website,'links',o.links,
			'productCount',(SELECT COUNT(*)::int FROM products p WHERE p.organization_id=o.id AND p.published_version_id IS NOT NULL),
			'createdAt',o.created_at
		) FROM organizations o WHERE o.slug=$1`, slug).Scan(&raw)
	if err != nil {
		return domain.Organization{}, err
	}
	var org domain.Organization
	if err := json.Unmarshal(raw, &org); err != nil {
		return domain.Organization{}, err
	}
	return org, nil
}

func (s *Server) organizationProducts(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	query.Set("org", r.PathValue("slug"))
	r.URL.RawQuery = query.Encode()
	s.listProducts(w, r)
}

func (s *Server) listTopics(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), "SELECT slug,label,tone,icon FROM topics WHERE is_active=TRUE ORDER BY position,slug")
	if err != nil {
		s.databaseError(w, "list topics", err)
		return
	}
	defer rows.Close()
	items := make([]domain.Topic, 0)
	for rows.Next() {
		var item domain.Topic
		if err := rows.Scan(&item.Slug, &item.Label, &item.Tone, &item.Icon); err != nil {
			s.databaseError(w, "scan topic", err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		s.databaseError(w, "read topics", err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

type leaderboardEntry struct {
	Product domain.Product `json:"product"`
	Score   int            `json:"score"`
}

func (s *Server) leaderboard(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("period")
	location, err := time.LoadLocation("Asia/Tehran")
	if err != nil {
		location = time.FixedZone("Asia/Tehran", 3*60*60+30*60)
	}
	now := time.Now().In(location)
	var start time.Time
	switch period {
	case "today":
		start = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
	case "month":
		start = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, location)
	default:
		writeError(w, http.StatusBadRequest, "invalid_period", "بازه باید today یا month باشد")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 50 {
		limit = 5
	}
	query := `
SELECT public.product_json, ranking.score
FROM (` + publicProductSelect + ` WHERE p.published_version_id IS NOT NULL) AS public
JOIN products p ON p.id::text=(public.product_json->>'id')
JOIN LATERAL (
	SELECT COUNT(*)::int AS score
	FROM votes v
	WHERE v.product_id=p.id AND v.created_at >= $1 AND v.created_at < $2
) ranking ON TRUE
ORDER BY ranking.score DESC,p.id
LIMIT $3`
	rows, err := s.db.Query(r.Context(), query, start.UTC(), now.UTC(), limit)
	if err != nil {
		s.databaseError(w, "leaderboard", err)
		return
	}
	defer rows.Close()
	items := make([]leaderboardEntry, 0)
	for rows.Next() {
		var raw []byte
		var score int
		if err := rows.Scan(&raw, &score); err != nil {
			s.databaseError(w, "scan leaderboard", err)
			return
		}
		var product domain.Product
		if err := json.Unmarshal(raw, &product); err != nil {
			s.databaseError(w, "decode leaderboard product", err)
			return
		}
		items = append(items, leaderboardEntry{Product: product, Score: score})
	}
	if err := rows.Err(); err != nil {
		s.databaseError(w, "read leaderboard", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"period": period, "items": items})
}

func scanProducts(rows pgx.Rows) ([]domain.Product, error) {
	defer rows.Close()
	items := make([]domain.Product, 0)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var product domain.Product
		if err := json.Unmarshal(raw, &product); err != nil {
			return nil, err
		}
		items = append(items, product)
	}
	return items, rows.Err()
}

func scanOrganizations(rows pgx.Rows) ([]domain.Organization, error) {
	defer rows.Close()
	items := make([]domain.Organization, 0)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var org domain.Organization
		if err := json.Unmarshal(raw, &org); err != nil {
			return nil, err
		}
		items = append(items, org)
	}
	return items, rows.Err()
}

func (s *Server) databaseError(w http.ResponseWriter, operation string, err error) {
	s.logger.Error(operation, "error", err)
	writeError(w, http.StatusInternalServerError, "database_error", "خطای پایگاه‌داده")
}
