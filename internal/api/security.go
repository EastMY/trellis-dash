package api

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// localRequestGuard 阻止 DNS rebinding 与浏览器跨站写请求。
// 首版没有认证，因此只接受显式的本机 Host；CLI 不带 Origin 的请求仍可使用。
func localRequestGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowedLocalHost(r.Host) {
			writeAPIError(w, http.StatusForbidden, "invalid_host", "仅允许通过本机地址访问 Dashboard")
			return
		}
		if !safeMethod(r.Method) && !allowedWriteOrigin(r) {
			writeAPIError(w, http.StatusForbidden, "cross_origin_forbidden", "拒绝跨站写请求")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func allowedLocalHost(hostport string) bool {
	host := hostport
	if parsed, _, err := net.SplitHostPort(hostport); err == nil {
		host = parsed
	} else if strings.HasPrefix(hostport, "[") && strings.HasSuffix(hostport, "]") {
		host = strings.Trim(hostport, "[]")
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" || host == "0.0.0.0" || host == "::" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func safeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

func allowedWriteOrigin(r *http.Request) bool {
	if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host) && allowedLocalHost(parsed.Host)
}
