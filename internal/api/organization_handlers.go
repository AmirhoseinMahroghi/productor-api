package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"productor-api/internal/domain"

	"github.com/jackc/pgx/v5"
)

type organizationInput struct {
	Slug    string        `json:"slug,omitempty"`
	Name    string        `json:"name"`
	Logo    string        `json:"logo"`
	Bio     string        `json:"bio"`
	Website string        `json:"website"`
	Links   []domain.Link `json:"links"`
}

func (s *Server) createOrganization(w http.ResponseWriter, r *http.Request, user domain.User) {
	var input organizationInput
	if err := decodeJSON(w, r, &input, 64<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", badJSONMessage(err))
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Logo = strings.TrimSpace(input.Logo)
	input.Website = strings.TrimSpace(input.Website)
	input.Bio = strings.TrimSpace(input.Bio)
	if input.Links == nil {
		input.Links = []domain.Link{}
	}
	org := domain.Organization{
		Name: input.Name, Logo: input.Logo, Bio: input.Bio,
		Website: input.Website, Links: input.Links,
	}
	if err := domain.ValidateOrganization(org); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_organization", err.Error())
		return
	}
	slug, err := organizationSlug(input.Slug, input.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "ساخت نشانی ارگان انجام نشد")
		return
	}
	linksJSON, err := json.Marshal(input.Links)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_organization", "پیوندهای ارگان معتبر نیستند")
		return
	}

	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.databaseError(w, "begin create organization", err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	err = tx.QueryRow(r.Context(), `
		INSERT INTO organizations(slug, name, logo, bio, website, links, created_by)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7)
		RETURNING id::text, created_at`,
		slug, input.Name, input.Logo, input.Bio, input.Website, linksJSON, user.ID,
	).Scan(&org.ID, &org.CreatedAt)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "organizations_slug_key") {
			writeError(w, http.StatusConflict, "slug_taken", "این نشانی قبلاً استفاده شده است")
			return
		}
		s.databaseError(w, "insert organization", err)
		return
	}
	if _, err := tx.Exec(r.Context(), `
		INSERT INTO organization_memberships(organization_id, user_id, role)
		VALUES ($1, $2, 'owner')`, org.ID, user.ID); err != nil {
		s.databaseError(w, "create organization owner", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.databaseError(w, "commit organization", err)
		return
	}
	org.Slug = slug
	org.ProductCount = 0
	writeJSON(w, http.StatusCreated, org)
}

func (s *Server) updateOrganization(w http.ResponseWriter, r *http.Request, user domain.User) {
	slug := r.PathValue("slug")
	role, err := s.organizationRole(r.Context(), user.ID, slug)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "organization_not_found", "ارگانی با این نشانی پیدا نشد")
		return
	}
	if err != nil {
		s.databaseError(w, "check organization membership", err)
		return
	}
	if role != "owner" && role != "admin" && user.Role != domain.RoleAdmin {
		writeError(w, http.StatusForbidden, "forbidden", "ویرایش این ارگان مجاز نیست")
		return
	}

	var input organizationInput
	if err := decodeJSON(w, r, &input, 64<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", badJSONMessage(err))
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Logo = strings.TrimSpace(input.Logo)
	input.Bio = strings.TrimSpace(input.Bio)
	input.Website = strings.TrimSpace(input.Website)
	if input.Links == nil {
		input.Links = []domain.Link{}
	}
	org := domain.Organization{Name: input.Name, Logo: input.Logo, Bio: input.Bio, Website: input.Website, Links: input.Links}
	if err := domain.ValidateOrganization(org); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_organization", err.Error())
		return
	}
	linksJSON, err := json.Marshal(input.Links)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_organization", "پیوندهای ارگان معتبر نیستند")
		return
	}
	_, err = s.db.Exec(r.Context(), `
		UPDATE organizations
		SET name = $2, logo = $3, bio = $4, website = $5, links = $6::jsonb, updated_at = now()
		WHERE slug = $1`, slug, input.Name, input.Logo, input.Bio, input.Website, linksJSON)
	if err != nil {
		s.databaseError(w, "update organization", err)
		return
	}
	updated, err := s.organizationBySlug(r.Context(), slug)
	if err != nil {
		s.databaseError(w, "read updated organization", err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) myOrganizations(w http.ResponseWriter, r *http.Request, user domain.User) {
	rows, err := s.db.Query(r.Context(), `
		SELECT jsonb_build_object(
			'id', o.id::text, 'slug', o.slug, 'name', o.name, 'logo', o.logo,
			'bio', o.bio, 'website', o.website, 'links', o.links,
			'productCount', (SELECT COUNT(*)::int FROM products p WHERE p.organization_id = o.id AND p.published_version_id IS NOT NULL),
			'role', m.role, 'createdAt', o.created_at
		)
		FROM organizations o
		JOIN organization_memberships m ON m.organization_id = o.id
		WHERE m.user_id = $1
		ORDER BY o.created_at DESC`, user.ID)
	if err != nil {
		s.databaseError(w, "list user organizations", err)
		return
	}
	defer rows.Close()
	items := make([]domain.Organization, 0)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			s.databaseError(w, "scan user organization", err)
			return
		}
		var org domain.Organization
		if err := json.Unmarshal(raw, &org); err != nil {
			s.databaseError(w, "decode user organization", err)
			return
		}
		items = append(items, org)
	}
	if err := rows.Err(); err != nil {
		s.databaseError(w, "read user organizations", err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) organizationRole(ctx context.Context, userID, slug string) (string, error) {
	var role string
	err := s.db.QueryRow(ctx, `
		SELECT m.role
		FROM organization_memberships m
		JOIN organizations o ON o.id = m.organization_id
		WHERE m.user_id = $1 AND o.slug = $2`, userID, slug).Scan(&role)
	return role, err
}

func organizationSlug(preferred, name string) (string, error) {
	base := domain.Slug(preferred, "")
	if base == "" {
		base = domain.Slug(name, "")
	}
	if base != "" && preferred != "" {
		return base, nil
	}
	random := make([]byte, 4)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	suffix := hex.EncodeToString(random)
	if base == "" {
		base = "organization"
	}
	return fmt.Sprintf("%s-%s", base, suffix), nil
}
