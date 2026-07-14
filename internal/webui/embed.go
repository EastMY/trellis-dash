package webui

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
)

//go:embed all:dist
var embedded embed.FS

// Handler 为前端静态资源提供服务，并将未知页面路由回 index.html。
func Handler() http.Handler {
	assets, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if requested == "" || requested == "." {
			requested = "index.html"
		}
		if _, statErr := fs.Stat(assets, requested); statErr != nil {
			requested = "index.html"
		}
		representation := requested
		if _, err := fs.Stat(assets, requested+".br"); err == nil {
			w.Header().Set("Vary", "Accept-Encoding")
		} else if _, err := fs.Stat(assets, requested+".gz"); err == nil {
			w.Header().Set("Vary", "Accept-Encoding")
		}
		encoding := preferredEncoding(r.Header.Get("Accept-Encoding"))
		if encoding != "" {
			suffix := map[string]string{"br": ".br", "gzip": ".gz"}[encoding]
			if _, err := fs.Stat(assets, requested+suffix); err == nil {
				representation = requested + suffix
				w.Header().Set("Content-Encoding", encoding)
			}
		}
		data, readErr := fs.ReadFile(assets, representation)
		if readErr != nil {
			http.Error(w, "frontend is not built", http.StatusServiceUnavailable)
			return
		}
		if contentType := mime.TypeByExtension(path.Ext(requested)); contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		if requested == "index.html" {
			w.Header().Set("Cache-Control", "no-cache")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(data)
		}
	})
}

func preferredEncoding(header string) string {
	accepted := make(map[string]float64)
	for _, item := range strings.Split(header, ",") {
		parts := strings.Split(strings.TrimSpace(item), ";")
		name := strings.ToLower(strings.TrimSpace(parts[0]))
		quality := 1.0
		for _, parameter := range parts[1:] {
			key, value, found := strings.Cut(strings.TrimSpace(parameter), "=")
			if found && strings.EqualFold(key, "q") {
				if parsed, err := strconv.ParseFloat(value, 64); err == nil {
					quality = parsed
				}
			}
		}
		accepted[name] = quality
	}
	quality := func(name string) float64 {
		if value, exists := accepted[name]; exists {
			return value
		}
		return accepted["*"]
	}
	brQuality, gzipQuality := quality("br"), quality("gzip")
	if brQuality > 0 && brQuality >= gzipQuality {
		return "br"
	}
	if gzipQuality > 0 {
		return "gzip"
	}
	return ""
}
