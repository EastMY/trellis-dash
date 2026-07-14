package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDurations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dashboard.yaml")
	content := []byte(`
server:
  host: 127.0.0.1
  port: 8000
refresh_interval: 9s
watcher:
  debounce: 300ms
  full_rescan_interval: 15m
git:
  max_diff_bytes: 1024
  command_timeout: 3s
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Watcher.Debounce.Duration != 300*time.Millisecond || cfg.RefreshInterval.Duration != 9*time.Second {
		t.Fatalf("时长解析异常: %#v", cfg)
	}
}

func TestLoadRejectsLegacyPollingFields(t *testing.T) {
	for name, content := range map[string]string{
		"watcher poll": "watcher:\n  poll_interval: 10s\n",
		"git refresh":  "git:\n  refresh_interval: 5s\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "dashboard.yaml")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("旧轮询字段应被明确拒绝")
			}
		})
	}
}
