package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"productor-api/internal/auth"
	"productor-api/internal/config"
	"productor-api/internal/domain"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Server struct {
	db      *pgxpool.Pool
	cfg     config.Config
	auth    *auth.Service
	logger  *slog.Logger
	origins map[string]struct{}
}

func New(db *pgxpool.Pool, cfg config.Config, logger *slog.Logger) *Server {
	origins := make(map[string]struct{}, len(cfg.FrontendOrigins))
	for _, origin := range cfg.FrontendOrigins {
		origins[strings.TrimRight(origin, "/")] = struct{}{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		db: db, cfg: cfg, auth: auth.NewService(db, cfg, logger),
		logger: logger, origins: origins,
	}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.ready)
	mux.HandleFunc("GET /api/v1", s.apiInfo)

	mux.HandleFunc("GET /api/v1/products", s.listProducts)
	mux.HandleFunc("GET /api/v1/products/{slug}", s.getProduct)
	mux.Handle("GET /api/v1/products/{slug}/manage", s.sameOrigin(s.requireAuth(s.getManagedProduct)))
	mux.Handle("PATCH /api/v1/products/{slug}", s.sameOrigin(s.requireAuth(s.updateProduct)))
	mux.Handle("POST /api/v1/products/{slug}/submit", s.sameOrigin(s.requireAuth(s.submitProduct)))
	mux.Handle("GET /api/v1/products/{slug}/comments", http.HandlerFunc(s.productComments))
	mux.Handle("POST /api/v1/products/{slug}/comments", s.sameOrigin(s.requireAuth(s.createComment)))
	mux.Handle("GET /api/v1/products/{slug}/vote", s.sameOrigin(s.requireAuth(s.userVote)))
	mux.Handle("PUT /api/v1/products/{slug}/vote", s.sameOrigin(s.requireAuth(s.castVote)))
	mux.Handle("DELETE /api/v1/products/{slug}/vote", s.sameOrigin(s.requireAuth(s.removeVote)))

	mux.HandleFunc("GET /api/v1/organizations", s.listOrganizations)
	mux.HandleFunc("GET /api/v1/organizations/{slug}", s.getOrganization)
	mux.HandleFunc("GET /api/v1/organizations/{slug}/products", s.organizationProducts)
	mux.Handle("GET /api/v1/organizations/{slug}/products/manage", s.sameOrigin(s.requireAuth(s.organizationManagedProducts)))
	mux.Handle("POST /api/v1/organizations/{slug}/products", s.sameOrigin(s.requireAuth(s.createProduct)))
	mux.Handle("POST /api/v1/organizations/{slug}/media", s.sameOrigin(s.requireAuth(s.uploadMedia)))
	mux.Handle("PATCH /api/v1/organizations/{slug}", s.sameOrigin(s.requireAuth(s.updateOrganization)))
	mux.Handle("POST /api/v1/organizations", s.sameOrigin(s.requireAuth(s.createOrganization)))

	mux.HandleFunc("GET /api/v1/topics", s.listTopics)
	mux.HandleFunc("GET /api/v1/categories", s.listCategories)
	mux.HandleFunc("GET /api/v1/leaderboard", s.leaderboard)
	mux.HandleFunc("GET /api/v1/media/{key}", s.getMedia)

	mux.Handle("GET /api/v1/me", s.sameOrigin(s.requireAuth(s.me)))
	mux.Handle("GET /api/v1/me/organizations", s.sameOrigin(s.requireAuth(s.myOrganizations)))
	mux.Handle("GET /api/v1/me/products", s.sameOrigin(s.requireAuth(s.myProducts)))
	mux.Handle("GET /api/v1/me/comments", s.sameOrigin(s.requireAuth(s.myComments)))
	mux.Handle("GET /api/v1/me/votes", s.sameOrigin(s.requireAuth(s.myVotes)))
	mux.Handle("GET /api/v1/me/notifications", s.sameOrigin(s.requireAuth(s.myNotifications)))
	mux.Handle("POST /api/v1/me/notifications/read-all", s.sameOrigin(s.requireAuth(s.markAllNotificationsRead)))
	mux.Handle("POST /api/v1/notifications/{id}/read", s.sameOrigin(s.requireAuth(s.markNotificationRead)))
	mux.Handle("PATCH /api/v1/comments/{id}", s.sameOrigin(s.requireAuth(s.editRejectedComment)))
	mux.Handle("DELETE /api/v1/comments/{id}", s.sameOrigin(s.requireAuth(s.deleteComment)))

	mux.HandleFunc("POST /api/v1/auth/otp/request", s.requestOTP)
	mux.HandleFunc("POST /api/v1/auth/otp/verify", s.verifyOTP)
	mux.Handle("POST /api/v1/auth/logout", s.sameOrigin(s.requireAuth(s.logout)))

	mux.Handle("GET /api/v1/moderation/products", s.sameOrigin(s.requirePermission("products.view", s.moderationProducts)))
	mux.Handle("GET /api/v1/moderation/products/{slug}/versions", s.sameOrigin(s.requirePermission("products.view", s.productVersions)))
	mux.Handle("POST /api/v1/moderation/products/{slug}/claim", s.sameOrigin(s.requirePermission("products.review", s.claimProductReview)))
	mux.Handle("POST /api/v1/moderation/products/{slug}/decision", s.sameOrigin(s.requirePermission("products.review", s.decideProduct)))
	mux.Handle("PATCH /api/v1/moderation/products/{slug}", s.sameOrigin(s.requirePermission("products.edit", s.editProductAsModerator)))
	mux.Handle("POST /api/v1/moderation/products/{slug}/unpublish", s.sameOrigin(s.requirePermission("products.unpublish", s.unpublishProduct)))
	mux.Handle("GET /api/v1/moderation/comments", s.sameOrigin(s.requirePermission("comments.view", s.moderationComments)))
	mux.Handle("POST /api/v1/moderation/comments/{id}/decision", s.sameOrigin(s.requirePermission("comments.moderate", s.decideComment)))

	mux.Handle("GET /api/v1/admin/overview", s.sameOrigin(s.requirePermission("dashboard.view", s.adminOverview)))
	mux.Handle("GET /api/v1/admin/users", s.sameOrigin(s.requirePermission("users.view", s.adminUsers)))
	mux.Handle("PATCH /api/v1/admin/users/{id}", s.sameOrigin(s.requirePermission("users.manage", s.updateUser)))
	mux.Handle("GET /api/v1/admin/products", s.sameOrigin(s.requirePermission("products.view", s.adminProducts)))
	mux.Handle("GET /api/v1/admin/comments", s.sameOrigin(s.requirePermission("comments.view", s.adminComments)))
	mux.Handle("GET /api/v1/admin/organizations", s.sameOrigin(s.requirePermission("organizations.view", s.adminOrganizations)))
	mux.Handle("PATCH /api/v1/admin/organizations/{slug}", s.sameOrigin(s.requirePermission("organizations.manage", s.updateAdminOrganization)))
	mux.Handle("GET /api/v1/admin/categories", s.sameOrigin(s.requirePermission("taxonomy.view", s.adminCategories)))
	mux.Handle("POST /api/v1/admin/categories", s.sameOrigin(s.requirePermission("taxonomy.manage", s.createAdminCategory)))
	mux.Handle("PATCH /api/v1/admin/categories/{id}", s.sameOrigin(s.requirePermission("taxonomy.manage", s.updateAdminCategory)))
	mux.Handle("GET /api/v1/admin/topics", s.sameOrigin(s.requirePermission("taxonomy.view", s.adminTopics)))
	mux.Handle("POST /api/v1/admin/topics", s.sameOrigin(s.requirePermission("taxonomy.manage", s.createAdminTopic)))
	mux.Handle("PATCH /api/v1/admin/topics/{slug}", s.sameOrigin(s.requirePermission("taxonomy.manage", s.updateAdminTopic)))
	mux.Handle("DELETE /api/v1/admin/topics/{slug}", s.sameOrigin(s.requirePermission("taxonomy.manage", s.deactivateAdminTopic)))
	mux.Handle("GET /api/v1/admin/audit", s.sameOrigin(s.requirePermission("audit.view", s.adminAudit)))
	mux.Handle("GET /api/v1/admin/roles", s.sameOrigin(s.requireAdmin(s.adminRoles)))
	mux.Handle("POST /api/v1/admin/roles", s.sameOrigin(s.requireAdmin(s.createAdminRole)))
	mux.Handle("PATCH /api/v1/admin/roles/{id}", s.sameOrigin(s.requireAdmin(s.updateAdminRole)))
	mux.Handle("DELETE /api/v1/admin/roles/{id}", s.sameOrigin(s.requireAdmin(s.deleteAdminRole)))

	return s.requestLog(s.recoverPanics(s.cors(mux)))
}

func (s *Server) StartMaintenance(ctx context.Context) {
	go func() {
		s.cleanup(ctx)
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.cleanup(ctx)
			}
		}
	}()
}

func (s *Server) cleanup(ctx context.Context) {
	cleanupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	statements := []string{
		"DELETE FROM comments WHERE status='rejected' AND edit_deadline IS NOT NULL AND edit_deadline<=now()",
		"DELETE FROM otp_challenges WHERE requested_at < now()-interval '2 days'",
		"DELETE FROM sessions WHERE expires_at < now()-interval '30 days' OR revoked_at < now()-interval '30 days'",
	}
	for _, statement := range statements {
		if _, err := s.db.Exec(cleanupCtx, statement); err != nil && !errors.Is(err, context.Canceled) {
			s.logger.Error("maintenance cleanup failed", "error", err)
		}
	}
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.db.Ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "پایگاه‌داده در دسترس نیست")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) apiInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"service": "productor-api", "version": "v1"})
}

func (s *Server) requireAuth(next func(http.ResponseWriter, *http.Request, domain.User)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(s.cfg.CookieName)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "برای ادامه وارد حساب شوید")
			return
		}
		user, err := s.auth.Authenticate(r.Context(), cookie.Value)
		if errors.Is(err, auth.ErrUnauthenticated) {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "نشست معتبر نیست؛ دوباره وارد شوید")
			return
		}
		if err != nil {
			s.logger.Error("authenticate request", "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "خطای داخلی سرور")
			return
		}
		next(w, r, user)
	})
}

func (s *Server) requireModerator(next func(http.ResponseWriter, *http.Request, domain.User)) http.Handler {
	return s.requireAuth(func(w http.ResponseWriter, r *http.Request, user domain.User) {
		if user.Role != domain.RoleModerator && user.Role != domain.RoleAdmin {
			writeError(w, http.StatusForbidden, "forbidden", "دسترسی ناظر لازم است")
			return
		}
		next(w, r, user)
	})
}

func (s *Server) requirePermission(permission string, next func(http.ResponseWriter, *http.Request, domain.User)) http.Handler {
	return s.requireAuth(func(w http.ResponseWriter, r *http.Request, user domain.User) {
		if !domain.HasPermission(user, permission) {
			writeError(w, http.StatusForbidden, "forbidden", "مجوز لازم برای این بخش را ندارید")
			return
		}
		next(w, r, user)
	})
}
func (s *Server) requireAdmin(next func(http.ResponseWriter, *http.Request, domain.User)) http.Handler {
	return s.requireAuth(func(w http.ResponseWriter, r *http.Request, user domain.User) {
		if user.Role != domain.RoleAdmin {
			writeError(w, http.StatusForbidden, "forbidden", "دسترسی مدیر لازم است")
			return
		}
		next(w, r, user)
	})
}

func (s *Server) sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimRight(r.Header.Get("Origin"), "/")
		if origin != "" {
			if _, ok := s.origins[origin]; !ok {
				writeError(w, http.StatusForbidden, "origin_not_allowed", "مبدأ درخواست مجاز نیست")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimRight(r.Header.Get("Origin"), "/")
		if _, ok := s.origins[origin]; ok && origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Add("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Requested-With")
			w.Header().Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions {
			if origin != "" {
				if _, ok := s.origins[origin]; !ok {
					writeError(w, http.StatusForbidden, "origin_not_allowed", "مبدأ درخواست مجاز نیست")
					return
				}
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (s *Server) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		s.logger.Info("http request",
			"method", r.Method, "path", r.URL.Path,
			"status", recorder.status, "duration_ms", time.Since(started).Milliseconds(),
		)
	})
}

func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("panic handling request", "panic", recovered, "path", r.URL.Path)
				writeError(w, http.StatusInternalServerError, "internal_error", "خطای داخلی سرور")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
