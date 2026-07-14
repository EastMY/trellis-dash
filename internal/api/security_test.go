package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLocalRequestGuardRejectsDNSRebinding(t *testing.T) {
	handler := localRequestGuard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "http://evil.example/api/v1/projects", nil)
	request.Host = "evil.example"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("恶意 Host 状态码 = %d, want 403", response.Code)
	}
}

func TestLocalRequestGuardChecksWriteOrigin(t *testing.T) {
	handler := localRequestGuard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, test := range []struct {
		name       string
		origin     string
		fetchSite  string
		wantStatus int
	}{
		{name: "same origin", origin: "http://127.0.0.1:7465", wantStatus: http.StatusNoContent},
		{name: "cli without origin", wantStatus: http.StatusNoContent},
		{name: "other port", origin: "http://127.0.0.1:9999", wantStatus: http.StatusForbidden},
		{name: "cross site", origin: "https://evil.example", fetchSite: "cross-site", wantStatus: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7465/api/v1/projects", nil)
			request.Host = "127.0.0.1:7465"
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			if test.fetchSite != "" {
				request.Header.Set("Sec-Fetch-Site", test.fetchSite)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("状态码 = %d, want %d", response.Code, test.wantStatus)
			}
		})
	}
}
