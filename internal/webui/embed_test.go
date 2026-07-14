package webui

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
)

func TestPreferredEncoding(t *testing.T) {
	tests := []struct {
		header string
		want   string
	}{
		{header: "gzip, br", want: "br"},
		{header: "br;q=0, gzip;q=1", want: "gzip"},
		{header: "gzip;q=0", want: ""},
		{header: "*;q=0.5", want: "br"},
		{header: "br;q=0, gzip;q=1, *;q=0.5", want: "gzip"},
		{header: "br;q=0.5, gzip;q=1", want: "gzip"},
	}
	for _, test := range tests {
		if got := preferredEncoding(test.header); got != test.want {
			t.Fatalf("preferredEncoding(%q) = %q，期望 %q", test.header, got, test.want)
		}
	}
}

func TestHandlerServesPrecompressedAsset(t *testing.T) {
	indexRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	indexResponse := httptest.NewRecorder()
	Handler().ServeHTTP(indexResponse, indexRequest)
	assetPattern := regexp.MustCompile(`src="([^"]+\.js)"`)
	match := assetPattern.FindStringSubmatch(indexResponse.Body.String())
	if len(match) != 2 {
		// 纯 Go 测试允许使用仓库中的空占位 index；前端构建后的嵌入测试会覆盖压缩表示。
		t.Skip("前端尚未构建")
	}

	request := httptest.NewRequest(http.MethodGet, match[1], nil)
	request.Header.Set("Accept-Encoding", "br, gzip")
	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Encoding") != "br" {
		t.Fatalf("Brotli 表示异常: status=%d encoding=%q", response.Code, response.Header().Get("Content-Encoding"))
	}
	if response.Header().Get("Vary") != "Accept-Encoding" || response.Body.Len() == 0 {
		t.Fatalf("压缩缓存头或响应体异常: headers=%v bytes=%d", response.Header(), response.Body.Len())
	}

	headRequest := httptest.NewRequest(http.MethodHead, match[1], nil)
	headRequest.Header.Set("Accept-Encoding", "gzip")
	headResponse := httptest.NewRecorder()
	Handler().ServeHTTP(headResponse, headRequest)
	if headResponse.Header().Get("Content-Encoding") != "gzip" || headResponse.Body.Len() != 0 {
		t.Fatalf("Gzip HEAD 表示异常: encoding=%q bytes=%d", headResponse.Header().Get("Content-Encoding"), headResponse.Body.Len())
	}
}
