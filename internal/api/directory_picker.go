package api

import (
	"context"
	"errors"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

var errDirectoryPickerUnsupported = errors.New("当前系统不支持原生目录选择器")

// directoryPicker 隔离平台实现，便于 HTTP 层覆盖选择成功、取消与失败场景。
type directoryPicker interface {
	Platform() string
	Supported() bool
	Pick(context.Context) (path string, canceled bool, err error)
}

type nativeDirectoryPicker struct {
	platform string
	run      func(context.Context, string, ...string) ([]byte, error)
}

func newNativeDirectoryPicker() directoryPicker {
	return &nativeDirectoryPicker{
		platform: runtime.GOOS,
		run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).CombinedOutput()
		},
	}
}

func (p *nativeDirectoryPicker) Platform() string { return p.platform }

func (p *nativeDirectoryPicker) Supported() bool { return p.platform == "darwin" }

func (p *nativeDirectoryPicker) Pick(ctx context.Context) (string, bool, error) {
	if !p.Supported() {
		return "", false, errDirectoryPickerUnsupported
	}

	// AppleScript 返回所选目录的 POSIX 绝对路径；参数完全固定，不拼接用户输入。
	output, err := p.run(ctx, "/usr/bin/osascript", "-e",
		`POSIX path of (choose folder with prompt "请选择 Trellis 项目根目录")`)
	if err != nil {
		if ctx.Err() != nil {
			return "", false, ctx.Err()
		}
		// 用户点击“取消”时 osascript 以 -128 退出，这属于正常交互而非接口错误。
		if strings.Contains(string(output), "(-128)") || strings.Contains(string(output), "User canceled") {
			return "", true, nil
		}
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return "", false, errors.New(message)
	}

	path := filepath.Clean(strings.TrimSpace(string(output)))
	if !filepath.IsAbs(path) {
		return "", false, errors.New("目录选择器未返回绝对路径")
	}
	return path, false, nil
}

func (s *Server) systemCapabilities(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"platform":        s.picker.Platform(),
		"directoryPicker": s.picker.Supported(),
	})
}

func (s *Server) selectDirectory(w http.ResponseWriter, r *http.Request) {
	path, canceled, err := s.picker.Pick(r.Context())
	if errors.Is(err, errDirectoryPickerUnsupported) {
		writeAPIError(w, http.StatusNotImplemented, "directory_picker_unsupported", err.Error())
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "directory_picker_failed", "打开目录选择器失败: "+err.Error())
		return
	}
	if canceled {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": path})
}
