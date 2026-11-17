package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"productor-api/internal/domain"

	"github.com/jackc/pgx/v5"
)

func (s *Server) productComments(w http.ResponseWriter, r *http.Request) {
	s.deleteExpiredRejectedComments(r.Context())
	limit, offset := pagination(r)
	rows, err := s.db.Query(r.Context(), `
		SELECT jsonb_build_object(
			'id',c.id::text,'productSlug',p.slug,
			'author','کاربر ' || left(u.phone_e164,4) || '***' || right(u.phone_e164,3),
			'body',c.body,'status',c.status,'createdAt',c.created_at
		)
		FROM comments c
		JOIN products p ON p.id=c.product_id
		JOIN users u ON u.id=c.user_id
		WHERE p.slug=$1 AND p.published_version_id IS NOT NULL AND c.status='published'
		ORDER BY c.created_at DESC,c.id
		LIMIT $2 OFFSET $3`, r.PathValue("slug"), limit, offset)
	if err != nil {
		s.databaseError(w, "list product comments", err)
		return
	}
	items, err := scanComments(rows)
	if err != nil {
		s.databaseError(w, "scan product comments", err)
		return
	}
	var total int
	if err := s.db.QueryRow(r.Context(), `
		SELECT COUNT(*) FROM comments c JOIN products p ON p.id=c.product_id
		WHERE p.slug=$1 AND p.published_version_id IS NOT NULL AND c.status='published'`, r.PathValue("slug")).Scan(&total); err != nil {
		s.databaseError(w, "count product comments", err)
		return
	}
	writeJSON(w, http.StatusOK, pageResponse[domain.Comment]{Items: items, Total: total, Limit: limit, Offset: offset})
}

func (s *Server) createComment(w http.ResponseWriter, r *http.Request, user domain.User) {
	var input struct {
		Body string `json:"body"`
	}
	if err := decodeJSON(w, r, &input, 16<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", badJSONMessage(err))
		return
	}
	input.Body = strings.TrimSpace(input.Body)
	if n := utf8.RuneCountInString(input.Body); n < 10 || n > 2000 {
		writeError(w, http.StatusBadRequest, "invalid_comment", "متن نظر باید بین ۱۰ تا ۲۰۰۰ نویسه باشد")
		return
	}
	s.deleteExpiredRejectedComments(r.Context())
	var productID string
	if err := s.db.QueryRow(r.Context(), "SELECT id::text FROM products WHERE slug=$1 AND published_version_id IS NOT NULL", r.PathValue("slug")).Scan(&productID); errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "product_not_found", "نظر فقط برای محصول منتشرشده ثبت می‌شود")
		return
	} else if err != nil {
		s.databaseError(w, "find product for comment", err)
		return
	}
	var count int
	if err := s.db.QueryRow(r.Context(), "SELECT COUNT(*) FROM comments WHERE user_id=$1 AND created_at>now()-interval '24 hours'", user.ID).Scan(&count); err != nil {
		s.databaseError(w, "check comment rate limit", err)
		return
	}
	if count >= 10 {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "سقف ثبت نظر روزانه پر شده است")
		return
	}
	var comment domain.Comment
	err := s.db.QueryRow(r.Context(), `
		INSERT INTO comments(product_id,user_id,body,status)
		VALUES($1,$2,$3,'pending')
		RETURNING id::text,created_at`, productID, user.ID, input.Body).Scan(&comment.ID, &comment.CreatedAt)
	if err != nil {
		s.databaseError(w, "create comment", err)
		return
	}
	comment.ProductSlug = r.PathValue("slug")
	comment.Author = "کاربر " + maskPhoneForDisplay(user.Phone)
	comment.Body = input.Body
	comment.Status = domain.CommentPending
	writeJSON(w, http.StatusCreated, comment)
}

func (s *Server) myComments(w http.ResponseWriter, r *http.Request, user domain.User) {
	s.deleteExpiredRejectedComments(r.Context())
	limit, offset := pagination(r)
	rows, err := s.db.Query(r.Context(), `
		SELECT jsonb_build_object(
			'id',c.id::text,'productSlug',p.slug,
			'author','کاربر ' || left(u.phone_e164,4) || '***' || right(u.phone_e164,3),
			'body',c.body,'status',c.status,'createdAt',c.created_at,
			'rejectReason',NULLIF(c.reject_reason,''),'editDeadline',c.edit_deadline
		)
		FROM comments c JOIN products p ON p.id=c.product_id JOIN users u ON u.id=c.user_id
		WHERE c.user_id=$1 ORDER BY c.created_at DESC,c.id LIMIT $2 OFFSET $3`, user.ID, limit, offset)
	if err != nil {
		s.databaseError(w, "list user comments", err)
		return
	}
	items, err := scanComments(rows)
	if err != nil {
		s.databaseError(w, "scan user comments", err)
		return
	}
	var total int
	if err := s.db.QueryRow(r.Context(), "SELECT COUNT(*) FROM comments WHERE user_id=$1", user.ID).Scan(&total); err != nil {
		s.databaseError(w, "count user comments", err)
		return
	}
	writeJSON(w, http.StatusOK, pageResponse[domain.Comment]{Items: items, Total: total, Limit: limit, Offset: offset})
}

func (s *Server) editRejectedComment(w http.ResponseWriter, r *http.Request, user domain.User) {
	var input struct {
		Body string `json:"body"`
	}
	if err := decodeJSON(w, r, &input, 16<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", badJSONMessage(err))
		return
	}
	input.Body = strings.TrimSpace(input.Body)
	if n := utf8.RuneCountInString(input.Body); n < 10 || n > 2000 {
		writeError(w, http.StatusBadRequest, "invalid_comment", "متن نظر باید بین ۱۰ تا ۲۰۰۰ نویسه باشد")
		return
	}
	var comment domain.Comment
	err := s.db.QueryRow(r.Context(), `
		UPDATE comments c SET body=$3,status='pending',reject_reason='',edit_deadline=NULL,
			reviewer_id=NULL,reviewed_at=NULL,updated_at=now()
		FROM products p
		WHERE c.id=$1 AND c.user_id=$2 AND p.id=c.product_id
			AND c.status='rejected' AND c.edit_deadline>now()
		RETURNING c.id::text,p.slug,c.created_at,c.status`,
		r.PathValue("id"), user.ID, input.Body).Scan(&comment.ID, &comment.ProductSlug, &comment.CreatedAt, &comment.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusConflict, "comment_not_editable", "نظر ردشده پیدا نشد یا مهلت ۴۸ ساعتهٔ ویرایش پایان یافته است")
		return
	}
	if err != nil {
		s.databaseError(w, "edit rejected comment", err)
		return
	}
	comment.Author = "کاربر " + maskPhoneForDisplay(user.Phone)
	comment.Body = input.Body
	writeJSON(w, http.StatusOK, comment)
}

func (s *Server) deleteComment(w http.ResponseWriter, r *http.Request, user domain.User) {
	tag, err := s.db.Exec(r.Context(), "DELETE FROM comments WHERE id=$1 AND user_id=$2", r.PathValue("id"), user.ID)
	if err != nil {
		s.databaseError(w, "delete comment", err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "comment_not_found", "نظر پیدا نشد")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type moderationComment struct {
	domain.Comment
	UserID      string `json:"userId"`
	ProductName string `json:"productName"`
}

func (s *Server) moderationComments(w http.ResponseWriter, r *http.Request, _ domain.User) {
	s.deleteExpiredRejectedComments(r.Context())
	limit, offset := pagination(r)
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status == "" {
		status = "pending"
	}
	if status != "pending" && status != "rejected" && status != "published" && status != "all" {
		writeError(w, http.StatusBadRequest, "invalid_status", "وضعیت نظر معتبر نیست")
		return
	}
	q := trimmed(r.URL.Query().Get("q"), 120)
	rows, err := s.db.Query(r.Context(), `
		SELECT jsonb_build_object(
			'id',c.id::text,'productSlug',p.slug,
			'author','کاربر ' || left(u.phone_e164,4) || '***' || right(u.phone_e164,3),
			'body',c.body,'status',c.status,'createdAt',c.created_at,
			'rejectReason',NULLIF(c.reject_reason,''),'editDeadline',c.edit_deadline,
			'userId',c.user_id::text,'productName',COALESCE(pv.payload->>'name','')
		)
		FROM comments c JOIN products p ON p.id=c.product_id
		JOIN product_versions pv ON pv.id=COALESCE(p.published_version_id,p.current_version_id)
		JOIN users u ON u.id=c.user_id
		WHERE ($1='all' OR c.status=$1) AND ($2='' OR c.body ILIKE '%'||$2||'%' OR p.slug ILIKE '%'||$2||'%' OR pv.payload->>'name' ILIKE '%'||$2||'%')
		ORDER BY c.created_at ASC,c.id LIMIT $3 OFFSET $4`, status, q, limit, offset)
	if err != nil {
		s.databaseError(w, "list moderation comments", err)
		return
	}
	items := make([]moderationComment, 0)
	for rows.Next() {
		var raw []byte
		var item moderationComment
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			s.databaseError(w, "scan moderation comment", err)
			return
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			rows.Close()
			s.databaseError(w, "decode moderation comment", err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		s.databaseError(w, "read moderation comments", err)
		return
	}
	rows.Close()
	var total int
	if err := s.db.QueryRow(r.Context(), "SELECT COUNT(*) FROM comments c JOIN products p ON p.id=c.product_id JOIN product_versions pv ON pv.id=COALESCE(p.published_version_id,p.current_version_id) WHERE ($1='all' OR c.status=$1) AND ($2='' OR c.body ILIKE '%'||$2||'%' OR p.slug ILIKE '%'||$2||'%' OR pv.payload->>'name' ILIKE '%'||$2||'%')", status, q).Scan(&total); err != nil {
		s.databaseError(w, "count moderation comments", err)
		return
	}
	writeJSON(w, http.StatusOK, pageResponse[moderationComment]{Items: items, Total: total, Limit: limit, Offset: offset})
}

func (s *Server) decideComment(w http.ResponseWriter, r *http.Request, user domain.User) {
	var input struct {
		Action string `json:"action"`
		Note   string `json:"note"`
	}
	if err := decodeJSON(w, r, &input, 16<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", badJSONMessage(err))
		return
	}
	input.Note = strings.TrimSpace(input.Note)
	if input.Action != "approve" && input.Action != "reject" {
		writeError(w, http.StatusBadRequest, "invalid_action", "تصمیم نظر باید approve یا reject باشد")
		return
	}
	if len([]rune(input.Note)) > 300 || (input.Action == "reject" && input.Note == "") {
		writeError(w, http.StatusBadRequest, "invalid_note", "برای رد نظر دلیل لازم است و حداکثر ۳۰۰ نویسه باشد")
		return
	}
	nextStatus := "published"
	var deadline *time.Time
	title, body := "نظر شما منتشر شد", "نظر شما پس از بررسی در صفحهٔ محصول نمایش داده می‌شود."
	if input.Action == "reject" {
		nextStatus = "rejected"

		title, body = "نظر نیاز به اصلاح دارد", input.Note
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.databaseError(w, "begin comment moderation", err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var userID, productSlug string
	err = tx.QueryRow(r.Context(), `
		UPDATE comments c SET status=$2,reject_reason=CASE WHEN $2='rejected' THEN $3 ELSE '' END,edit_deadline=CASE WHEN $2='rejected' THEN now()+interval '48 hours' ELSE NULL END,
			reviewer_id=$4,reviewed_at=now(),updated_at=now()
		FROM products p
		WHERE c.id=$1 AND p.id=c.product_id AND c.status='pending'
		RETURNING c.user_id::text,p.slug,c.edit_deadline`,
		r.PathValue("id"), nextStatus, input.Note, user.ID).Scan(&userID, &productSlug, &deadline)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusConflict, "comment_not_pending", "نظر در انتظار بررسی پیدا نشد")
		return
	}
	if err != nil {
		s.databaseError(w, "save comment decision", err)
		return
	}
	if err := notifyUserTx(r.Context(), tx, userID, "comment_review", title, body, "/products/"+productSlug); err != nil {
		s.databaseError(w, "notify comment author", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.databaseError(w, "commit comment decision", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": r.PathValue("id"), "status": nextStatus, "editDeadline": deadline})
}

func (s *Server) userVote(w http.ResponseWriter, r *http.Request, user domain.User) {
	var voted bool
	if err := s.db.QueryRow(r.Context(), `
		SELECT EXISTS (
			SELECT 1 FROM votes v JOIN products p ON p.id=v.product_id
			WHERE p.slug=$1 AND v.user_id=$2 AND p.published_version_id IS NOT NULL
		)`, r.PathValue("slug"), user.ID).Scan(&voted); err != nil {
		s.databaseError(w, "read user vote", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"voted": voted})
}

func (s *Server) castVote(w http.ResponseWriter, r *http.Request, user domain.User) {
	var productID string
	if err := s.db.QueryRow(r.Context(), "SELECT id::text FROM products WHERE slug=$1 AND published_version_id IS NOT NULL", r.PathValue("slug")).Scan(&productID); errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "product_not_found", "رأی فقط برای محصول منتشرشده ثبت می‌شود")
		return
	} else if err != nil {
		s.databaseError(w, "find product for vote", err)
		return
	}
	if _, err := s.db.Exec(r.Context(), `INSERT INTO votes(user_id,product_id) VALUES($1,$2) ON CONFLICT(user_id,product_id) DO NOTHING`, user.ID, productID); err != nil {
		s.databaseError(w, "cast product vote", err)
		return
	}
	s.writeVoteSummary(w, r, productID, user.ID)
}

func (s *Server) removeVote(w http.ResponseWriter, r *http.Request, user domain.User) {
	var productID string
	if err := s.db.QueryRow(r.Context(), "SELECT id::text FROM products WHERE slug=$1 AND published_version_id IS NOT NULL", r.PathValue("slug")).Scan(&productID); errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "product_not_found", "محصول منتشرشده پیدا نشد")
		return
	} else if err != nil {
		s.databaseError(w, "find product to remove vote", err)
		return
	}
	if _, err := s.db.Exec(r.Context(), "DELETE FROM votes WHERE user_id=$1 AND product_id=$2", user.ID, productID); err != nil {
		s.databaseError(w, "remove product vote", err)
		return
	}
	s.writeVoteSummary(w, r, productID, user.ID)
}

func (s *Server) writeVoteSummary(w http.ResponseWriter, r *http.Request, productID, userID string) {
	var count int
	var voted bool
	if err := s.db.QueryRow(r.Context(), "SELECT COUNT(*)::int FROM votes WHERE product_id=$1", productID).Scan(&count); err != nil {
		s.databaseError(w, "count product votes", err)
		return
	}
	if err := s.db.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM votes WHERE product_id=$1 AND user_id=$2)", productID, userID).Scan(&voted); err != nil {
		s.databaseError(w, "read vote state", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"up": count, "voted": voted})
}

func (s *Server) myVotes(w http.ResponseWriter, r *http.Request, user domain.User) {
	rows, err := s.db.Query(r.Context(), `
		SELECT p.slug,v.created_at FROM votes v JOIN products p ON p.id=v.product_id
		WHERE v.user_id=$1 AND p.published_version_id IS NOT NULL ORDER BY v.created_at DESC`, user.ID)
	if err != nil {
		s.databaseError(w, "list user votes", err)
		return
	}
	defer rows.Close()
	type vote struct {
		Slug      string    `json:"slug"`
		CreatedAt time.Time `json:"createdAt"`
	}
	items := make([]vote, 0)
	for rows.Next() {
		var item vote
		if err := rows.Scan(&item.Slug, &item.CreatedAt); err != nil {
			s.databaseError(w, "scan user vote", err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		s.databaseError(w, "read user votes", err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) deleteExpiredRejectedComments(ctx context.Context) {
	_, err := s.db.Exec(ctx, "DELETE FROM comments WHERE status='rejected' AND edit_deadline IS NOT NULL AND edit_deadline<=now()")
	if err != nil {
		s.logger.Error("delete expired rejected comments", "error", err)
	}
}

func maskPhoneForDisplay(phone string) string {
	if len(phone) < 7 {
		return "***"
	}
	return phone[:4] + "***" + phone[len(phone)-3:]
}

func scanComments(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close()
}) ([]domain.Comment, error) {
	defer rows.Close()
	items := make([]domain.Comment, 0)
	for rows.Next() {
		var raw []byte
		var item domain.Comment
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
