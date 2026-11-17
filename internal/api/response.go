package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
)

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type pageResponse[T any] struct {
	Items  []T `json:"items"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]errorBody{
		"error": {Code: code, Message: message},
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any, maxBytes int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func pagination(r *http.Request) (limit, offset int) {
	limit = 24
	if parsed, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && parsed > 0 {
		limit = parsed
	}
	if limit > 100 {
		limit = 100
	}
	if parsed, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && parsed >= 0 {
		offset = parsed
	}
	return
}

func trimmed(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	if len([]rune(value)) > maxRunes {
		return string([]rune(value)[:maxRunes])
	}
	return value
}

func badJSONMessage(err error) string {
	var maxBytes *http.MaxBytesError
	if errors.As(err, &maxBytes) {
		return "حجم درخواست بیش از حد مجاز است"
	}
	return "بدنهٔ درخواست JSON معتبر نیست"
}
