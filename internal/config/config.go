package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yunnnn/trellis-dash/internal/model"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server          ServerConfig    `yaml:"server"`
	Database        DatabaseConfig  `yaml:"database"`
	Projects        []ProjectConfig `yaml:"projects"`
	RefreshInterval Duration        `yaml:"refresh_interval"`
	Watcher         WatcherConfig   `yaml:"watcher"`
	Git             GitConfig       `yaml:"git"`
}

type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

type DatabaseConfig struct {
	Path string `yaml:"path"`
}

type ProjectConfig struct {
	ID   string            `yaml:"id"`
	Name string            `yaml:"name"`
	Root string            `yaml:"root"`
	Mode model.ProjectMode `yaml:"mode"`
}

type WatcherConfig struct {
	Debounce           Duration `yaml:"debounce"`
	FullRescanInterval Duration `yaml:"full_rescan_interval"`
}

type GitConfig struct {
	MaxDiffBytes   int64    `yaml:"max_diff_bytes"`
	CommandTimeout Duration `yaml:"command_timeout"`
}

// Duration 让 YAML 可以直接使用 250ms、5s、10m 这类可读写法。
type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	parsed, err := time.ParseDuration(node.Value)
	if err != nil {
		return fmt.Errorf("无效时长 %q: %w", node.Value, err)
	}
	d.Duration = parsed
	return nil
}

func (d Duration) MarshalYAML() (any, error) { return d.String(), nil }

func Default() Config {
	return Config{
		Server:   ServerConfig{Host: "127.0.0.1", Port: 7465},
		Database: DatabaseConfig{Path: "~/.local/share/trellis-dashboard/dashboard.db"},
		// Git 与 BSD/macOS 元数据轮询共用同一项目级刷新节拍。
		RefreshInterval: Duration{10 * time.Second},
		Watcher: WatcherConfig{
			Debounce:           Duration{250 * time.Millisecond},
			FullRescanInterval: Duration{10 * time.Minute},
		},
		Git: GitConfig{
			MaxDiffBytes:   2 << 20,
			CommandTimeout: Duration{5 * time.Second},
		},
	}
}

// Load 在默认值之上解码配置，缺失字段不会被清零。
func Load(path string) (Config, error) {
	result := Default()
	if path == "" {
		return result, nil
	}
	resolved, err := expandPath(path)
	if err != nil {
		return result, err
	}
	content, err := os.ReadFile(resolved)
	if err != nil {
		return result, fmt.Errorf("读取配置 %s: %w", resolved, err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	// 配置已完成破坏性重构；旧轮询字段和其他未知字段必须明确报错，不能静默忽略。
	decoder.KnownFields(true)
	if err := decoder.Decode(&result); err != nil {
		return result, fmt.Errorf("解析配置 %s: %w", resolved, err)
	}
	if result.Server.Host == "" {
		result.Server.Host = "127.0.0.1"
	}
	if result.Server.Port <= 0 || result.Server.Port > 65535 {
		return result, fmt.Errorf("server.port 超出范围: %d", result.Server.Port)
	}
	if result.RefreshInterval.Duration <= 0 {
		return result, fmt.Errorf("refresh_interval 必须大于 0")
	}
	return result, nil
}

func expandPath(path string) (string, error) {
	if path == "~" || len(path) > 2 && path[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			return home, nil
		}
		path = filepath.Join(home, path[2:])
	}
	return filepath.Abs(path)
}
