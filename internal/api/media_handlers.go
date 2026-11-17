package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"productor-api/internal/domain"

	"github.com/jackc/pgx/v5"
)

const maxUploadSize = 10 << 20

var mediaKeyPattern = regexp.MustCompile(`^[a-f0-9]{32}\.(jpg|png|webp)$`)

func (s *Server) uploadMedia(w http.ResponseWriter, r *http.Request, user domain.User) {
	orgSlug := r.PathValue("slug")
	role, err := s.organizationRole(r.Context(), user.ID, orgSlug)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "organization_not_found", "ارگان پیدا نشد یا دسترسی ندارید")
		return
	}
	if err != nil {
		s.databaseError(w, "check media upload membership", err)
		return
	}
	if role != "owner" && role != "admin" && role != "editor" {
		writeError(w, http.StatusForbidden, "forbidden", "اجازهٔ بارگذاری برای این ارگان را ندارید")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize+(1<<20))
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_upload", "درخواست بارگذاری معتبر نیست یا فایل بیش از ۱۰ مگابایت است")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file_required", "فیلد file باید شامل یک تصویر باشد")
		return
	}
	defer file.Close()
	if header.Size <= 0 || header.Size > maxUploadSize {
		writeError(w, http.StatusBadRequest, "file_too_large", "اندازهٔ تصویر باید کمتر از ۱۰ مگابایت باشد")
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, maxUploadSize+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_upload", "خواندن فایل انجام نشد")
		return
	}
	if len(data) == 0 || len(data) > maxUploadSize {
		writeError(w, http.StatusBadRequest, "file_too_large", "اندازهٔ تصویر باید کمتر از ۱۰ مگابایت باشد")
		return
	}
	contentType := http.DetectContentType(data[:min(512, len(data))])
	extension := ""
	switch contentType {
	case "image/jpeg":
		extension = ".jpg"
	case "image/png":
		extension = ".png"
	case "image/webp":
		extension = ".webp"
	default:
		writeError(w, http.StatusBadRequest, "unsupported_file_type", "فقط تصویرهای JPEG، PNG و WebP پذیرفته می‌شوند")
		return
	}
	randomKey := make([]byte, 16)
	if _, err := rand.Read(randomKey); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "شناسهٔ فایل ساخته نشد")
		return
	}
	key := hex.EncodeToString(randomKey) + extension
	path := filepath.Join(s.cfg.UploadDir, key)
	if err := os.WriteFile(path, data, 0600); err != nil {
		s.logger.Error("save uploaded media", "error", err)
		writeError(w, http.StatusInternalServerError, "storage_error", "ذخیرهٔ فایل انجام نشد")
		return
	}
	var id string
	err = s.db.QueryRow(r.Context(), `
		INSERT INTO media_files(organization_id,uploader_id,storage_key,content_type,file_size)
		SELECT id,$2,$3,$4,$5 FROM organizations WHERE slug=$1
		RETURNING id::text`, orgSlug, user.ID, key, contentType, len(data)).Scan(&id)
	if err != nil {
		_ = os.Remove(path)
		s.databaseError(w, "register uploaded media", err)
		return
	}
	url := s.cfg.PublicAPIURL + "/api/v1/media/" + key
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "url": url, "contentType": contentType, "size": len(data)})
}

func (s *Server) getMedia(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !mediaKeyPattern.MatchString(key) {
		http.NotFound(w, r)
		return
	}
	var id, orgID, uploaderID, contentType string
	var fileSize int64
	var isPublic bool
	err := s.db.QueryRow(r.Context(), `
		SELECT id::text,organization_id::text,uploader_id::text,content_type,file_size,is_public
		FROM media_files WHERE storage_key=$1`, key).Scan(&id, &orgID, &uploaderID, &contentType, &fileSize, &isPublic)
	if errors.Is(err, pgx.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.databaseError(w, "load media metadata", err)
		return
	}
	if !isPublic {
		cookie, cookieErr := r.Cookie(s.cfg.CookieName)
		if cookieErr != nil {
			http.NotFound(w, r)
			return
		}
		user, authErr := s.auth.Authenticate(r.Context(), cookie.Value)
		if authErr != nil {
			http.NotFound(w, r)
			return
		}
		if user.ID != uploaderID {
			var allowed bool
			if err := s.db.QueryRow(r.Context(), `
				SELECT EXISTS(SELECT 1 FROM organization_memberships WHERE organization_id=$1 AND user_id=$2)`, orgID, user.ID).Scan(&allowed); err != nil {
				s.databaseError(w, "check private media access", err)
				return
			}
			if !allowed {
				http.NotFound(w, r)
				return
			}
		}
	}
	filePath := filepath.Join(s.cfg.UploadDir, key)
	file, err := os.Open(filePath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", fileSize))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	if isPublic {
		w.Header().Set("Cache-Control", "public, max-age=86400, immutable")
	} else {
		w.Header().Set("Cache-Control", "private, no-store")
	}
	http.ServeContent(w, r, key, info.ModTime(), file)
}

func refreshPublishedMedia(ctx context.Context, tx pgx.Tx, organizationID string) error {
	_, err := tx.Exec(ctx, `
		UPDATE media_files mf SET is_public=EXISTS(
			SELECT 1
			FROM products p JOIN product_versions pv ON pv.id=p.published_version_id
			WHERE p.organization_id=mf.organization_id AND (
				right(COALESCE(pv.payload->>'logo',''),length('/api/v1/media/'||mf.storage_key))='/api/v1/media/'||mf.storage_key OR
				right(COALESCE(pv.payload->>'logo',''),length('/media/'||mf.storage_key))='/media/'||mf.storage_key OR
				EXISTS (
					SELECT 1 FROM jsonb_array_elements(COALESCE(pv.payload->'media','[]'::jsonb)) m
					WHERE right(COALESCE(m->>'src',''),length('/api/v1/media/'||mf.storage_key))='/api/v1/media/'||mf.storage_key
					   OR right(COALESCE(m->>'poster',''),length('/api/v1/media/'||mf.storage_key))='/api/v1/media/'||mf.storage_key
					   OR right(COALESCE(m->>'src',''),length('/media/'||mf.storage_key))='/media/'||mf.storage_key
					   OR right(COALESCE(m->>'poster',''),length('/media/'||mf.storage_key))='/media/'||mf.storage_key
				)
			)
		)
		WHERE mf.organization_id=$1`, organizationID)
	return err
}

func safeMediaURL(value string) bool {
	return strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "http://")
}
