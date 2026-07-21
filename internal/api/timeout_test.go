package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAPITimeoutOnlyBypassesLongRunningRoutes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path   string
		bypass bool
	}{
		{path: "/api/v1/system/directory-picker", bypass: true},
		{path: "/api/v1/projects/project-a/codex-usage", bypass: true},
		{path: "/api/v1/projects/project-a/dashboard", bypass: false},
		{path: "/api/v1/projects/project-a/codex-usage/extra", bypass: false},
		{path: "/api/v1/projects//codex-usage", bypass: false},
		{path: "/api/v1/projects/a/b/codex-usage", bypass: false},
	}
	for _, test := range tests {
		test := test
		t.Run(test.path, func(t *testing.T) {
			t.Parallel()
			if actual := bypassAPITimeout(test.path); actual != test.bypass {
				t.Fatalf("bypassAPITimeout(%q) = %t，期望 %t", test.path, actual, test.bypass)
			}
		})
	}
}

func TestCodexUsageBypassesServerTimeoutButKeepsClientCancellation(t *testing.T) {
	t.Parallel()

	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(30 * time.Millisecond):
			w.WriteHeader(http.StatusNoContent)
		case <-r.Context().Done():
		}
	})
	handler := apiTimeoutWithDuration(slow, 5*time.Millisecond)

	codexRequest := httptest.NewRequest(http.MethodGet, "http://localhost/api/v1/projects/project-a/codex-usage", nil)
	codexResponse := httptest.NewRecorder()
	handler.ServeHTTP(codexResponse, codexRequest)
	if codexResponse.Code != http.StatusNoContent {
		t.Fatalf("Codex 统计不应触发固定超时: status=%d", codexResponse.Code)
	}

	otherRequest := httptest.NewRequest(http.MethodGet, "http://localhost/api/v1/projects/project-a/dashboard", nil)
	otherResponse := httptest.NewRecorder()
	handler.ServeHTTP(otherResponse, otherRequest)
	if otherResponse.Code != http.StatusGatewayTimeout {
		t.Fatalf("其他 API 应继续使用固定超时: status=%d", otherResponse.Code)
	}

	canceled := make(chan error, 1)
	cancelAware := apiTimeoutWithDuration(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		canceled <- r.Context().Err()
	}), time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "http://localhost/api/v1/projects/project-a/codex-usage", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		cancelAware.ServeHTTP(httptest.NewRecorder(), request)
		close(done)
	}()
	cancel()
	select {
	case err := <-canceled:
		if err != context.Canceled {
			t.Fatalf("客户端取消错误 = %v，期望 context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Codex 统计旁路未传递客户端取消信号")
	}
	<-done
}
