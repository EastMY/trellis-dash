package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type stubDirectoryPicker struct {
	platform  string
	supported bool
	path      string
	canceled  bool
	err       error
}

func (p stubDirectoryPicker) Platform() string { return p.platform }
func (p stubDirectoryPicker) Supported() bool  { return p.supported }
func (p stubDirectoryPicker) Pick(context.Context) (string, bool, error) {
	return p.path, p.canceled, p.err
}

func TestNativeDirectoryPicker(t *testing.T) {
	t.Run("macOS 返回规范绝对路径", func(t *testing.T) {
		picker := &nativeDirectoryPicker{
			platform: "darwin",
			run: func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name != "/usr/bin/osascript" || len(args) != 2 {
					t.Fatalf("osascript 调用参数异常: name=%q args=%v", name, args)
				}
				return []byte("/Users/demo/project/\n"), nil
			},
		}
		path, canceled, err := picker.Pick(context.Background())
		if err != nil || canceled || path != "/Users/demo/project" {
			t.Fatalf("选择结果异常: path=%q canceled=%v err=%v", path, canceled, err)
		}
	})

	t.Run("用户取消不是错误", func(t *testing.T) {
		picker := &nativeDirectoryPicker{
			platform: "darwin",
			run: func(context.Context, string, ...string) ([]byte, error) {
				return []byte("execution error: User canceled. (-128)\n"), errors.New("exit status 1")
			},
		}
		path, canceled, err := picker.Pick(context.Background())
		if err != nil || !canceled || path != "" {
			t.Fatalf("取消结果异常: path=%q canceled=%v err=%v", path, canceled, err)
		}
	})

	t.Run("非 macOS 明确拒绝", func(t *testing.T) {
		picker := &nativeDirectoryPicker{platform: "linux"}
		_, _, err := picker.Pick(context.Background())
		if !errors.Is(err, errDirectoryPickerUnsupported) {
			t.Fatalf("非 macOS 错误 = %v，期望 unsupported", err)
		}
	})
}

func TestDirectoryPickerHandlers(t *testing.T) {
	t.Run("能力接口返回服务端平台", func(t *testing.T) {
		server := &Server{picker: stubDirectoryPicker{platform: "darwin", supported: true}}
		response := httptest.NewRecorder()
		server.systemCapabilities(response, httptest.NewRequest(http.MethodGet, "/", nil))
		if response.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，期望 200", response.Code)
		}
		var body struct {
			Platform        string `json:"platform"`
			DirectoryPicker bool   `json:"directoryPicker"`
		}
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Platform != "darwin" || !body.DirectoryPicker {
			t.Fatalf("能力响应异常: %+v", body)
		}
	})

	t.Run("选择成功返回路径", func(t *testing.T) {
		server := &Server{picker: stubDirectoryPicker{path: "/Users/demo/project"}}
		response := httptest.NewRecorder()
		server.selectDirectory(response, httptest.NewRequest(http.MethodPost, "/", nil))
		if response.Code != http.StatusOK || response.Body.String() != "{\"path\":\"/Users/demo/project\"}\n" {
			t.Fatalf("选择响应异常: status=%d body=%s", response.Code, response.Body.String())
		}
	})

	t.Run("取消返回空响应", func(t *testing.T) {
		server := &Server{picker: stubDirectoryPicker{canceled: true}}
		response := httptest.NewRecorder()
		server.selectDirectory(response, httptest.NewRequest(http.MethodPost, "/", nil))
		if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
			t.Fatalf("取消响应异常: status=%d body=%s", response.Code, response.Body.String())
		}
	})

	t.Run("不支持时返回稳定错误码", func(t *testing.T) {
		server := &Server{picker: stubDirectoryPicker{err: errDirectoryPickerUnsupported}}
		response := httptest.NewRecorder()
		server.selectDirectory(response, httptest.NewRequest(http.MethodPost, "/", nil))
		if response.Code != http.StatusNotImplemented ||
			!containsJSONErrorCode(response.Body.Bytes(), "directory_picker_unsupported") {
			t.Fatalf("不支持响应异常: status=%d body=%s", response.Code, response.Body.String())
		}
	})
}

func containsJSONErrorCode(data []byte, expected string) bool {
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	return json.Unmarshal(data, &body) == nil && body.Error.Code == expected
}
