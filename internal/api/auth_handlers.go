package api

import (
	"errors"
	"net"
	"net/http"
	"time"

	"productor-api/internal/auth"
	"productor-api/internal/domain"
)

func (s *Server) requestOTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Phone string `json:"phone"`
	}
	if err := decodeJSON(w, r, &input, 8<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", badJSONMessage(err))
		return
	}
	if err := s.auth.RequestOTP(r.Context(), input.Phone, clientIP(r)); err != nil {
		switch {
		case errors.Is(err, auth.ErrRateLimited):
			writeError(w, http.StatusTooManyRequests, "rate_limited", "برای درخواست رمز تازه کمی صبر کنید")
		case errors.Is(err, auth.ErrUnsupportedSMS):
			writeError(w, http.StatusServiceUnavailable, "sms_unavailable", "ارسال پیامک در این محیط تنظیم نشده است")
		default:
			writeError(w, http.StatusBadRequest, "invalid_phone", err.Error())
		}
		return
	}
	w.Header().Set("Retry-After", "60")
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted":  true,
		"expiresIn": 300,
		"message":   "اگر شماره معتبر باشد، رمز یک‌بارمصرف ارسال می‌شود",
	})
}

func (s *Server) verifyOTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Phone string `json:"phone"`
		Code  string `json:"code"`
	}
	if err := decodeJSON(w, r, &input, 8<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", badJSONMessage(err))
		return
	}
	session, err := s.auth.VerifyOTP(r.Context(), input.Phone, input.Code)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrInvalidOTP):
			writeError(w, http.StatusUnauthorized, "invalid_otp", "رمز یک‌بارمصرف معتبر نیست یا منقضی شده است")
		case errors.Is(err, auth.ErrBlocked):
			writeError(w, http.StatusForbidden, "account_blocked", "این حساب امکان ورود ندارد")
		default:
			s.logger.Error("verify OTP", "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "خطای داخلی سرور")
		}
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     s.cfg.CookieName,
		Value:    session.Token,
		Path:     "/",
		Domain:   s.cfg.CookieDomain,
		Expires:  session.ExpiresAt,
		MaxAge:   int(time.Until(session.ExpiresAt).Seconds()),
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]domain.User{"user": session.User})
}

func (s *Server) me(w http.ResponseWriter, _ *http.Request, user domain.User) {
	writeJSON(w, http.StatusOK, map[string]domain.User{"user": user})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, _ domain.User) {
	if cookie, err := r.Cookie(s.cfg.CookieName); err == nil {
		if err := s.auth.Revoke(r.Context(), cookie.Value); err != nil {
			s.logger.Error("revoke session", "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "خروج از حساب انجام نشد")
			return
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name:     s.cfg.CookieName,
		Value:    "",
		Path:     "/",
		Domain:   s.cfg.CookieDomain,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
