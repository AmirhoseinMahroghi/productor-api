package domain

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"unicode/utf8"
)

func Slug(value, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || r == ' ':
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		default:
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	result := strings.Trim(b.String(), "-")
	if result == "" {
		return fallback
	}
	if len(result) > 80 {
		result = strings.Trim(result[:80], "-")
	}
	return result
}

func ValidateOrganization(org Organization) error {
	if n := utf8.RuneCountInString(strings.TrimSpace(org.Name)); n < 2 || n > 120 {
		return errors.New("نام ارگان باید بین ۲ تا ۱۲۰ نویسه باشد")
	}
	if utf8.RuneCountInString(org.Bio) > 4000 {
		return errors.New("معرفی ارگان بیش از حد طولانی است")
	}
	if err := ValidateHTTPURL(org.Website); err != nil {
		return fmt.Errorf("وب‌سایت ارگان: %w", err)
	}
	if err := validateOptionalImage(org.Logo); err != nil {
		return fmt.Errorf("لوگوی ارگان: %w", err)
	}
	if len(org.Links) > 20 {
		return errors.New("حداکثر ۲۰ پیوند برای ارگان مجاز است")
	}
	for _, link := range org.Links {
		if utf8.RuneCountInString(strings.TrimSpace(link.Label)) == 0 || utf8.RuneCountInString(link.Label) > 80 {
			return errors.New("عنوان هر پیوند باید بین ۱ تا ۸۰ نویسه باشد")
		}
		if err := ValidateHTTPURL(link.Href); err != nil {
			return fmt.Errorf("پیوند ارگان: %w", err)
		}
	}
	return nil
}

func ValidateProductDraft(payload ProductPayload) error {
	if n := utf8.RuneCountInString(strings.TrimSpace(payload.Name)); n > 120 {
		return errors.New("نام محصول بیش از ۱۲۰ نویسه است")
	}
	if n := utf8.RuneCountInString(payload.Tagline); n > 240 {
		return errors.New("معرفی کوتاه بیش از ۲۴۰ نویسه است")
	}
	if n := utf8.RuneCountInString(payload.Description); n > 30000 {
		return errors.New("توضیحات محصول بیش از ۳۰۰۰۰ نویسه است")
	}
	if n := utf8.RuneCountInString(payload.Audience); n > 500 {
		return errors.New("مخاطب هدف بیش از حد طولانی است")
	}
	if payload.Logo != "" {
		if err := validateOptionalImage(payload.Logo); err != nil {
			return fmt.Errorf("لوگوی محصول: %w", err)
		}
	}
	if payload.Website != "" {
		if err := ValidateHTTPURL(payload.Website); err != nil {
			return fmt.Errorf("وب‌سایت محصول: %w", err)
		}
	}
	if payload.RepoURL != "" {
		if err := ValidateHTTPURL(payload.RepoURL); err != nil {
			return fmt.Errorf("نشانی مخزن: %w", err)
		}
	}
	if payload.Pricing != "" && !oneOf(payload.Pricing, "free", "freemium", "paid", "open_source") {
		return errors.New("مدل قیمت‌گذاری نامعتبر است")
	}
	if len(payload.Categories) > 10 || len(payload.Platforms) > 10 || len(payload.Features) > 50 ||
		len(payload.Pros) > 20 || len(payload.Cons) > 20 || len(payload.Media) > 30 || len(payload.Similar) > 20 || len(payload.UserTags) > 20 {
		return errors.New("تعداد دسته‌بندی‌ها، پلتفرم‌ها یا ویژگی‌ها بیش از حد مجاز است")
	}
	if len(payload.Platforms) > 0 {
		for _, platform := range payload.Platforms {
			if !oneOf(platform, "web", "android", "ios", "windows", "macos", "linux", "cloud", "self_hosted") {
				return errors.New("پلتفرم نامعتبر است")
			}
		}
	}
	for _, feature := range payload.Features {
		if utf8.RuneCountInString(feature.Label) > 120 || utf8.RuneCountInString(feature.Value) > 2000 {
			return errors.New("یکی از ویژگی‌های محصول بیش از حد طولانی است")
		}
	}
	for _, media := range payload.Media {
		if err := validateMedia(media); err != nil {
			return err
		}
	}
	return nil
}

func ValidateProduct(payload ProductPayload) error {
	if n := utf8.RuneCountInString(strings.TrimSpace(payload.Name)); n < 2 || n > 120 {
		return errors.New("نام محصول باید بین ۲ تا ۱۲۰ نویسه باشد")
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(payload.Tagline)); n < 15 || n > 240 {
		return errors.New("معرفی کوتاه باید بین ۱۵ تا ۲۴۰ نویسه باشد")
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(payload.Description)); n < 40 || n > 30000 {
		return errors.New("توضیحات محصول باید بین ۴۰ تا ۳۰۰۰۰ نویسه باشد")
	}
	if err := ValidateProductDraft(payload); err != nil {
		return err
	}
	if err := ValidateHTTPURL(payload.Website); err != nil {
		return fmt.Errorf("وب‌سایت محصول: %w", err)
	}
	if payload.OpenSource && payload.RepoURL == "" {
		return errors.New("برای محصول متن‌باز، نشانی مخزن لازم است")
	}
	if strings.TrimSpace(payload.MainCategory) == "" || len(payload.Categories) == 0 {
		return errors.New("دستهٔ اصلی و دست‌کم یک دسته‌بندی لازم است")
	}
	if !contains(payload.Categories, payload.MainCategory) {
		return errors.New("دستهٔ اصلی باید در دسته‌بندی‌های محصول باشد")
	}
	if len(payload.Platforms) == 0 {
		return errors.New("حداقل یک پلتفرم لازم است")
	}
	if !oneOf(payload.Pricing, "free", "freemium", "paid", "open_source") {
		return errors.New("مدل قیمت‌گذاری نامعتبر است")
	}
	if len(payload.Features) == 0 {
		return errors.New("حداقل یک ویژگی فنی لازم است")
	}
	for _, feature := range payload.Features {
		if strings.TrimSpace(feature.Label) == "" || strings.TrimSpace(feature.Value) == "" {
			return errors.New("عنوان و مقدار هر ویژگی باید تکمیل شود")
		}
	}
	return nil
}

func validateMedia(media Media) error {
	if !oneOf(media.Kind, "image", "video") || len(media.Src) > 2048 || len(media.Poster) > 2048 {
		return errors.New("رسانهٔ محصول معتبر نیست")
	}
	if media.Src == "" {
		return errors.New("نشانی رسانهٔ محصول لازم است")
	}
	if !isLocalMediaPath(media.Src) {
		if err := ValidateHTTPURL(media.Src); err != nil {
			return fmt.Errorf("نشانی رسانه: %w", err)
		}
	}
	if media.Poster != "" && !isLocalMediaPath(media.Poster) {
		if err := ValidateHTTPURL(media.Poster); err != nil {
			return fmt.Errorf("تصویر پیش‌نمایش: %w", err)
		}
	}
	if utf8.RuneCountInString(media.Alt) > 240 || utf8.RuneCountInString(media.Caption) > 500 {
		return errors.New("توضیح رسانه بیش از حد طولانی است")
	}
	return nil
}

func validateOptionalImage(value string) error {
	if len(value) > 2048 {
		return errors.New("نشانی تصویر بیش از حد طولانی است")
	}
	if value == "" || isLocalMediaPath(value) || (strings.HasPrefix(value, "/products/") && !strings.Contains(value, "..")) {
		return nil
	}
	return ValidateHTTPURL(value)
}

func isLocalMediaPath(value string) bool {
	if !strings.HasPrefix(value, "/api/v1/media/") && !strings.HasPrefix(value, "/media/") {
		return false
	}
	clean := path.Clean(value)
	return clean == value && !strings.Contains(value, "..")
}

func ValidateHTTPURL(value string) error {
	value = strings.TrimSpace(value)
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("نشانی باید با http یا https شروع شود")
	}
	return nil
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == expected {
			return true
		}
	}
	return false
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
