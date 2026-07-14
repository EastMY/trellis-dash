package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yunnnn/trellis-dash/internal/config"
	"github.com/yunnnn/trellis-dash/internal/store"
)

func TestValidateDatabasePlacementBeforeOpen(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".trellis"), 0o750); err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(root, ".dashboard", "dashboard.db")
	err := validateDatabasePlacement(database, []config.ProjectConfig{{Root: root}})
	if !errors.Is(err, store.ErrDatabaseProjectOverlap) {
		t.Fatalf("校验错误 = %v，期望 ErrDatabaseProjectOverlap", err)
	}
	if _, err := os.Stat(database); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("预检不得创建数据库文件，Stat error = %v", err)
	}
}
