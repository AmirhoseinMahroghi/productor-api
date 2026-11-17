package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"regexp"
	"strings"
	"time"

	"productor-api/internal/config"
	"productor-api/internal/domain"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrRateLimited     = errors.New("rate limited")
	ErrInvalidOTP      = errors.New("invalid or expired OTP")
	ErrUnauthenticated = errors.New("unauthenticated")
	ErrBlocked         = errors.New("account blocked")
	ErrUnsupportedSMS  = errors.New("unsupported SMS mode")
)

type Service struct {
	db     *pgxpool.Pool
	cfg    config.Config
	logger *slog.Logger
}

type Session struct {
	User      domain.User
	Token     string
	ExpiresAt time.Time
}

func NewService(db *pgxpool.Pool, cfg config.Config, logger *slog.Logger) *Service {
	return &Service{db: db, cfg: cfg, logger: logger}
}

func (s *Service) RequestOTP(ctx context.Context, rawPhone, requestIP string) error {
	if s.cfg.OTPMode != "development" {
		return ErrUnsupportedSMS
	}
	phone, err := NormalizePhone(rawPhone)
	if err != nil {
		return err
	}
	if len(requestIP) > 128 {
		requestIP = requestIP[:128]
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin OTP request: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", phone); err != nil {
		return fmt.Errorf("lock OTP rate limit: %w", err)
	}
	var phoneRequests, ipRequests int
	if err := tx.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE phone_e164 = $1 AND requested_at > now() - interval '1 hour')::int,
			COUNT(*) FILTER (WHERE request_ip = $2 AND requested_at > now() - interval '1 hour')::int
		FROM otp_challenges`, phone, requestIP).Scan(&phoneRequests, &ipRequests); err != nil {
		return fmt.Errorf("check OTP rate limit: %w", err)
	}
	if phoneRequests >= 5 || ipRequests >= 20 {
		return ErrRateLimited
	}
	var lastRequested time.Time
	err = tx.QueryRow(ctx, "SELECT requested_at FROM otp_challenges WHERE phone_e164 = $1 ORDER BY requested_at DESC LIMIT 1", phone).Scan(&lastRequested)
	if err == nil && time.Since(lastRequested) < time.Minute {
		return ErrRateLimited
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("check OTP cooldown: %w", err)
	}

	code, err := randomOTP()
	if err != nil {
		return err
	}
	expires := time.Now().Add(5 * time.Minute)
	if _, err := tx.Exec(ctx, `
		INSERT INTO otp_challenges(phone_e164, code_hash, request_ip, expires_at)
		VALUES ($1, $2, $3, $4)`, phone, s.otpHash(phone, code), requestIP, expires); err != nil {
		return fmt.Errorf("store OTP challenge: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit OTP challenge: %w", err)
	}
	s.logger.Warn("development OTP issued", "phone", maskPhone(phone), "code", code, "expires_at", expires)
	return nil
}

func (s *Service) VerifyOTP(ctx context.Context, rawPhone, code string) (Session, error) {
	phone, err := NormalizePhone(rawPhone)
	if err != nil || len(code) != 6 || !allDigits(code) {
		return Session{}, ErrInvalidOTP
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Session{}, fmt.Errorf("begin OTP verification: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var challengeID string
	var expectedHash []byte
	var attempts int
	err = tx.QueryRow(ctx, `
		SELECT id::text, code_hash, attempts
		FROM otp_challenges
		WHERE phone_e164 = $1 AND consumed_at IS NULL AND expires_at > now()
		ORDER BY requested_at DESC
		LIMIT 1
		FOR UPDATE`, phone).Scan(&challengeID, &expectedHash, &attempts)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && attempts >= 5) {
		return Session{}, ErrInvalidOTP
	}
	if err != nil {
		return Session{}, fmt.Errorf("load OTP challenge: %w", err)
	}
	if _, err := tx.Exec(ctx, "UPDATE otp_challenges SET attempts = attempts + 1 WHERE id = $1", challengeID); err != nil {
		return Session{}, fmt.Errorf("count OTP attempt: %w", err)
	}
	if !hmac.Equal(expectedHash, s.otpHash(phone, code)) {
		if err := tx.Commit(ctx); err != nil {
			return Session{}, err
		}
		return Session{}, ErrInvalidOTP
	}
	if _, err := tx.Exec(ctx, "UPDATE otp_challenges SET consumed_at = now() WHERE id = $1", challengeID); err != nil {
		return Session{}, fmt.Errorf("consume OTP: %w", err)
	}

	var user domain.User
	var blocked bool
	err = tx.QueryRow(ctx, `
		INSERT INTO users(phone_e164)
		VALUES ($1)
		ON CONFLICT (phone_e164) DO UPDATE SET updated_at = now()
		RETURNING id::text, phone_e164, role, created_at, is_blocked`,
		phone).Scan(&user.ID, &user.Phone, &user.Role, &user.CreatedAt, &blocked)
	if err != nil {
		return Session{}, fmt.Errorf("load user: %w", err)
	}
	if s.isBootstrapAdmin(phone) && user.Role != domain.RoleAdmin {
		if _, err := tx.Exec(ctx, "UPDATE users SET role = 'admin', updated_at = now() WHERE id = $1", user.ID); err != nil {
			return Session{}, fmt.Errorf("promote bootstrap administrator: %w", err)
		}
		user.Role = domain.RoleAdmin
	}
	if err := tx.QueryRow(ctx, `
		SELECT u.id::text, u.phone_e164, u.role, u.created_at,
		       COALESCE(u.admin_role_id::text, ''), COALESCE(ar.name, ''),
		       COALESCE(ar.permissions, '{}'::text[]), u.is_blocked
		FROM users u LEFT JOIN admin_roles ar ON ar.id = u.admin_role_id
		WHERE u.id = $1`, user.ID).Scan(
		&user.ID, &user.Phone, &user.Role, &user.CreatedAt, &user.AdminRoleID, &user.AdminRoleName, &user.Permissions, &blocked,
	); err != nil {
		return Session{}, fmt.Errorf("load user permissions: %w", err)
	}
	if blocked {
		if err := tx.Commit(ctx); err != nil {
			return Session{}, err
		}
		return Session{}, ErrBlocked
	}

	token, err := randomToken()
	if err != nil {
		return Session{}, err
	}
	expires := time.Now().Add(s.cfg.SessionTTL)
	tokenHash := sha256.Sum256([]byte(token))
	if _, err := tx.Exec(ctx, "INSERT INTO sessions(token_hash, user_id, expires_at) VALUES ($1, $2, $3)", tokenHash[:], user.ID, expires); err != nil {
		return Session{}, fmt.Errorf("create session: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Session{}, fmt.Errorf("commit OTP verification: %w", err)
	}
	return Session{User: user, Token: token, ExpiresAt: expires}, nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (domain.User, error) {
	if token == "" || len(token) > 256 {
		return domain.User{}, ErrUnauthenticated
	}
	tokenHash := sha256.Sum256([]byte(token))
	var user domain.User
	err := s.db.QueryRow(ctx, `
		SELECT u.id::text, u.phone_e164, u.role, u.created_at,
		       COALESCE(u.admin_role_id::text, ''), COALESCE(ar.name, ''), COALESCE(ar.permissions, '{}'::text[])
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		LEFT JOIN admin_roles ar ON ar.id = u.admin_role_id
		WHERE s.token_hash = $1
		  AND s.revoked_at IS NULL
		  AND s.expires_at > now()
		  AND u.is_blocked = FALSE`, tokenHash[:]).Scan(
		&user.ID, &user.Phone, &user.Role, &user.CreatedAt, &user.AdminRoleID, &user.AdminRoleName, &user.Permissions,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, ErrUnauthenticated
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("authenticate session: %w", err)
	}
	_, _ = s.db.Exec(ctx, "UPDATE sessions SET last_seen_at = now() WHERE token_hash = $1", tokenHash[:])
	return user, nil
}

func (s *Service) Revoke(ctx context.Context, token string) error {
	if token == "" || len(token) > 256 {
		return nil
	}
	tokenHash := sha256.Sum256([]byte(token))
	_, err := s.db.Exec(ctx, "UPDATE sessions SET revoked_at = now() WHERE token_hash = $1 AND revoked_at IS NULL", tokenHash[:])
	return err
}

func (s *Service) otpHash(phone, code string) []byte {
	mac := hmac.New(sha256.New, []byte(s.cfg.OTPHashSecret))
	_, _ = mac.Write([]byte(phone + ":" + code))
	return mac.Sum(nil)
}

func NormalizePhone(value string) (string, error) {
	var b strings.Builder
	for _, r := range strings.TrimSpace(value) {
		switch {
		case r >= '۰' && r <= '۹':
			b.WriteByte(byte('0' + r - '۰'))
		case r >= '٠' && r <= '٩':
			b.WriteByte(byte('0' + r - '٠'))
		case r >= '0' && r <= '9', r == '+':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '(' || r == ')' || r == '\u200c':
		default:
			return "", errors.New("شمارهٔ موبایل معتبر نیست")
		}
	}
	phone := b.String()
	if strings.HasPrefix(phone, "0098") {
		phone = "+98" + phone[4:]
	} else if strings.HasPrefix(phone, "98") && len(phone) == 12 {
		phone = "+" + phone
	} else if strings.HasPrefix(phone, "0") && len(phone) == 11 {
		phone = "+98" + phone[1:]
	}
	matched, _ := regexp.MatchString("^\\+[1-9][0-9]{7,14}$", phone)
	if !matched {
		return "", errors.New("شمارهٔ موبایل باید با قالب بین‌المللی معتبر باشد")
	}
	return phone, nil
}

func randomOTP() (string, error) {
	number, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", fmt.Errorf("generate OTP: %w", err)
	}
	return fmt.Sprintf("%06d", number.Int64()), nil
}

func randomToken() (string, error) {
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(token), nil
}

func allDigits(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (s *Service) isBootstrapAdmin(phone string) bool {
	for _, adminPhone := range s.cfg.AdminPhones {
		normalized, err := NormalizePhone(adminPhone)
		if err == nil && normalized == phone {
			return true
		}
	}
	return false
}

func maskPhone(phone string) string {
	if len(phone) < 5 {
		return "***"
	}
	return phone[:4] + "****" + phone[len(phone)-3:]
}
