package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	AppEnv          string
	Port            string
	DatabaseURL     string
	FrontendOrigins []string
	PublicAPIURL    string
	UploadDir       string
	CookieName      string
	CookieDomain    string
	CookieSecure    bool
	SessionTTL      time.Duration
	OTPMode         string
	OTPHashSecret   string
	AdminPhones     []string
}

func Load() (Config, error) {
	appEnv := get("APP_ENV", "development")
	if appEnv != "development" && appEnv != "test" && appEnv != "production" {
		return Config{}, fmt.Errorf("APP_ENV must be development, test, or production")
	}
	secureDefault := appEnv == "production"
	cookieSecure, err := getBool("COOKIE_SECURE", secureDefault)
	if err != nil {
		return Config{}, err
	}
	secret := strings.TrimSpace(os.Getenv("OTP_HASH_SECRET"))
	if secret == "" {
		if appEnv == "production" {
			return Config{}, errors.New("OTP_HASH_SECRET is required in production")
		}
		random := make([]byte, 32)
		if _, err := rand.Read(random); err != nil {
			return Config{}, fmt.Errorf("generate development OTP secret: %w", err)
		}
		secret = hex.EncodeToString(random)
	}
	return Config{
		AppEnv:          appEnv,
		Port:            get("PORT", "8080"),
		DatabaseURL:     strings.TrimSpace(os.Getenv("DATABASE_URL")),
		FrontendOrigins: splitList(get("FRONTEND_ORIGINS", "http://localhost:3000,http://localhost:3002")),
		PublicAPIURL:    strings.TrimRight(get("PUBLIC_API_URL", "http://localhost:8080"), "/"),
		UploadDir:       get("UPLOAD_DIR", "uploads"),
		CookieName:      get("SESSION_COOKIE_NAME", "productor_session"),
		CookieDomain:    strings.TrimSpace(os.Getenv("SESSION_COOKIE_DOMAIN")),
		CookieSecure:    cookieSecure,
		SessionTTL:      30 * 24 * time.Hour,
		OTPMode:         get("OTP_MODE", "development"),
		OTPHashSecret:   secret,
		AdminPhones:     splitList(os.Getenv("BOOTSTRAP_ADMIN_PHONES")),
	}, nil
}

func get(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func getBool(key string, fallback bool) (bool, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false", key)
	}
	return parsed, nil
}

func splitList(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
