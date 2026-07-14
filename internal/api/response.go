package api

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/yunnnn/trellis-dash/internal/store"
)

type errorEnvelope struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if value != nil {
		_ = json.NewEncoder(w).Encode(value)
	}
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorEnvelope{Error: apiError{Code: code, Message: message}})
}

func writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeAPIError(w, http.StatusNotFound, "not_found", "资源不存在")
		return
	}
	writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求 JSON 无效: "+err.Error())
		return false
	}
	return true
}

func etagMatches(r *http.Request, etag string) bool {
	for _, candidate := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == strings.TrimPrefix(etag, "W/") {
			return true
		}
	}
	return false
}

// payloadETag 为实际响应表示生成强校验器，适合 Git Diff 等无法只靠 revision 判断的内容。
func payloadETag(prefix string, value any) string {
	payload, _ := json.Marshal(value)
	digest := sha256.Sum256(payload)
	return fmt.Sprintf(`"%s-%x"`, prefix, digest[:12])
}

func revisionETag(prefix, generation string, values ...any) string {
	return payloadETag(prefix, struct {
		Generation string `json:"generation"`
		Values     []any  `json:"values"`
	}{Generation: generation, Values: values})
}

func setCacheValidator(w http.ResponseWriter, r *http.Request, etag string) bool {
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-cache")
	if etagMatches(r, etag) {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	return false
}
