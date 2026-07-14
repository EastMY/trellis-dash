package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/yunnnn/trellis-dash/internal/model"
)

const (
	projectRegistryVersion  = 1
	maxProjectRegistryBytes = 1024 * 1024
	maxProjectRegistryItems = 1_000
)

var (
	ErrProjectRegistryInvalid  = errors.New("项目注册表无效")
	ErrProjectRegistryTooLarge = errors.New("项目注册表超过限制")
)

// registeredProject 只保留重建项目注册和初始 revision 所需的稳定字段。
type registeredProject struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Root      string            `json:"root"`
	Mode      model.ProjectMode `json:"mode"`
	CreatedAt time.Time         `json:"createdAt"`
}

type projectRegistry struct {
	Version  int                 `json:"version"`
	Projects []registeredProject `json:"projects"`
}

type projectRegistryQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func projectRegistryPath(databasePath string) string {
	return databasePath + ".projects.json"
}

func readProjectRegistry(databasePath string) (projectRegistry, bool, error) {
	path := projectRegistryPath(databasePath)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return projectRegistry{}, false, nil
	}
	if err != nil {
		return projectRegistry{}, false, fmt.Errorf("读取项目注册表: %w", err)
	}
	if !info.Mode().IsRegular() {
		return projectRegistry{}, false, fmt.Errorf("%w: %s 必须是普通文件", ErrProjectRegistryInvalid, path)
	}
	if info.Size() > maxProjectRegistryBytes {
		return projectRegistry{}, false, fmt.Errorf("%w: 文件大小 %d > %d", ErrProjectRegistryTooLarge, info.Size(), maxProjectRegistryBytes)
	}

	file, err := os.Open(path)
	if err != nil {
		return projectRegistry{}, false, fmt.Errorf("打开项目注册表: %w", err)
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, maxProjectRegistryBytes+1))
	if err != nil {
		return projectRegistry{}, false, fmt.Errorf("读取项目注册表: %w", err)
	}
	if len(payload) > maxProjectRegistryBytes {
		return projectRegistry{}, false, fmt.Errorf("%w: 文件超过 %d 字节", ErrProjectRegistryTooLarge, maxProjectRegistryBytes)
	}

	var registry projectRegistry
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&registry); err != nil {
		return projectRegistry{}, false, fmt.Errorf("%w: %v", ErrProjectRegistryInvalid, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return projectRegistry{}, false, fmt.Errorf("%w: %v", ErrProjectRegistryInvalid, err)
	}
	if err := validateProjectRegistry(registry); err != nil {
		return projectRegistry{}, false, err
	}
	return registry, true, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("包含多余 JSON 值")
}

func validateProjectRegistry(registry projectRegistry) error {
	if registry.Version != projectRegistryVersion {
		return fmt.Errorf("%w: 不支持版本 %d", ErrProjectRegistryInvalid, registry.Version)
	}
	if len(registry.Projects) > maxProjectRegistryItems {
		return fmt.Errorf("%w: 项目数 %d > %d", ErrProjectRegistryTooLarge, len(registry.Projects), maxProjectRegistryItems)
	}
	byID := make(map[string]string, len(registry.Projects))
	for index := range registry.Projects {
		project := &registry.Projects[index]
		if strings.TrimSpace(project.ID) == "" || strings.TrimSpace(project.Root) == "" || project.CreatedAt.IsZero() {
			return fmt.Errorf("%w: 第 %d 个项目缺少 id/root/createdAt", ErrProjectRegistryInvalid, index+1)
		}
		if project.Mode == "" {
			project.Mode = model.ProjectModeObserver
		}
		if project.Mode != model.ProjectModeObserver {
			return fmt.Errorf("%w: 项目 %s 仅允许 observer，mode=%s", ErrProjectRegistryInvalid, project.ID, project.Mode)
		}
		if existingRoot, exists := byID[project.ID]; exists {
			if !sameProjectRoot(existingRoot, project.Root) {
				return fmt.Errorf("%w: id=%s existing=%s requested=%s", ErrProjectRootConflict, project.ID, existingRoot, project.Root)
			}
			return fmt.Errorf("%w: 项目 ID %s 重复", ErrProjectRegistryInvalid, project.ID)
		}
		byID[project.ID] = project.Root
	}
	return nil
}

func sameProjectRoot(left, right string) bool {
	canonicalLeft, leftErr := canonicalPath(left)
	canonicalRight, rightErr := canonicalPath(right)
	if leftErr == nil && rightErr == nil {
		return canonicalLeft == canonicalRight
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func encodeProjectRegistry(ctx context.Context, queryer projectRegistryQueryer) ([]byte, error) {
	rows, err := queryer.QueryContext(ctx, `
		SELECT id, name, root, mode, created_at
		FROM projects ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("查询项目注册表: %w", err)
	}
	defer rows.Close()
	projects := make([]registeredProject, 0)
	for rows.Next() {
		if len(projects) >= maxProjectRegistryItems {
			return nil, fmt.Errorf("%w: 项目数超过 %d", ErrProjectRegistryTooLarge, maxProjectRegistryItems)
		}
		var project registeredProject
		var createdAt string
		if err := rows.Scan(&project.ID, &project.Name, &project.Root, &project.Mode, &createdAt); err != nil {
			return nil, err
		}
		project.CreatedAt, err = parseTime(createdAt)
		if err != nil {
			return nil, fmt.Errorf("项目 %s created_at 无效: %w", project.ID, err)
		}
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// SQL 已按 ID 排序；再次排序让未来查询实现变化也不会改变文件的确定性。
	sort.Slice(projects, func(left, right int) bool { return projects[left].ID < projects[right].ID })
	payload, err := json.MarshalIndent(projectRegistry{Version: projectRegistryVersion, Projects: projects}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("编码项目注册表: %w", err)
	}
	payload = append(payload, '\n')
	if len(payload) > maxProjectRegistryBytes {
		return nil, fmt.Errorf("%w: 编码后 %d > %d 字节", ErrProjectRegistryTooLarge, len(payload), maxProjectRegistryBytes)
	}
	return payload, nil
}

func writeProjectRegistry(databasePath string, payload []byte) error {
	if len(payload) > maxProjectRegistryBytes {
		return fmt.Errorf("%w: 写入内容超过 %d 字节", ErrProjectRegistryTooLarge, maxProjectRegistryBytes)
	}
	path := projectRegistryPath(databasePath)
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("创建项目注册表临时文件: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("设置项目注册表权限: %w", err)
	}
	if _, err := temporary.Write(payload); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("写入项目注册表: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("同步项目注册表: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("关闭项目注册表: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("替换项目注册表: %w", err)
	}
	// 文件 fsync 只保证内容；目录 fsync 才能让 rename 在掉电后保持可见。
	if runtime.GOOS != "windows" {
		directory, err := os.Open(filepath.Dir(path))
		if err != nil {
			return fmt.Errorf("打开项目注册表目录: %w", err)
		}
		if err := directory.Sync(); err != nil {
			_ = directory.Close()
			return fmt.Errorf("同步项目注册表目录: %w", err)
		}
		if err := directory.Close(); err != nil {
			return fmt.Errorf("关闭项目注册表目录: %w", err)
		}
	}
	return nil
}

// commitProjectMutation 以 sidecar 为跨数据库重建时的注册事实源。
// 若落盘或 SQLite commit 失败，会尽力恢复修改前的 sidecar，避免失败请求在重启后反向生效。
func (s *Store) commitProjectMutation(tx *sql.Tx, previous, next []byte) error {
	if !s.memory {
		if err := writeProjectRegistry(s.path, next); err != nil {
			restoreErr := writeProjectRegistry(s.path, previous)
			return errors.Join(err, restoreErr)
		}
	}
	if err := tx.Commit(); err != nil {
		if s.memory {
			return err
		}
		restoreErr := writeProjectRegistry(s.path, previous)
		return errors.Join(err, restoreErr)
	}
	return nil
}

// restoreProjectsFromRegistry 在迁移完成后恢复缺失项目，并重新生成初始 revision 行。
func (s *Store) restoreProjectsFromRegistry(ctx context.Context) error {
	if s.memory {
		return nil
	}
	registry, exists, err := readProjectRegistry(s.path)
	if err != nil {
		return err
	}
	if exists {
		for _, project := range registry.Projects {
			if err := ValidateDatabaseOutsideProject(s.path, project.Root); err != nil {
				return err
			}
		}
	}

	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	rows, err := tx.QueryContext(ctx, `SELECT id, root FROM projects`)
	if err != nil {
		return err
	}
	type currentProject struct{ id, root string }
	current := make([]currentProject, 0)
	for rows.Next() {
		var project currentProject
		if err := rows.Scan(&project.id, &project.root); err != nil {
			rows.Close()
			return err
		}
		if err := ValidateDatabaseOutsideProject(s.path, project.root); err != nil {
			rows.Close()
			return fmt.Errorf("校验数据库中的项目 %s: %w", project.id, err)
		}
		current = append(current, project)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if exists {
		desired := make(map[string]registeredProject, len(registry.Projects))
		for _, project := range registry.Projects {
			desired[project.ID] = project
		}
		for _, project := range current {
			registered, keep := desired[project.id]
			if !keep {
				if _, err := tx.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, project.id); err != nil {
					return fmt.Errorf("移除 sidecar 外项目 %s: %w", project.id, err)
				}
				continue
			}
			if !sameProjectRoot(project.root, registered.Root) {
				return fmt.Errorf("%w: id=%s existing=%s requested=%s", ErrProjectRootConflict, project.id, project.root, registered.Root)
			}
		}
		for _, project := range registry.Projects {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO projects (id, name, root, mode, generation, created_at, updated_at)
				VALUES (?, ?, ?, ?, lower(hex(randomblob(16))), ?, ?)
				ON CONFLICT(id) DO UPDATE SET
					name = excluded.name,
					root = excluded.root,
					mode = excluded.mode,
					created_at = excluded.created_at`,
				project.ID, project.Name, project.Root, project.Mode,
				formatTime(project.CreatedAt), formatTime(now)); err != nil {
				return fmt.Errorf("恢复项目 %s: %w", project.ID, err)
			}
			for _, resource := range allResourceTypes() {
				if _, err := tx.ExecContext(ctx, `
					INSERT OR IGNORE INTO resource_revisions
						(project_id, resource_type, revision, updated_at)
					VALUES (?, ?, 0, ?)`, project.ID, resource, formatTime(now)); err != nil {
					return fmt.Errorf("恢复项目 %s revision: %w", project.ID, err)
				}
			}
		}
	}
	payload, err := encodeProjectRegistry(ctx, tx)
	if err != nil {
		return err
	}
	// sidecar 是项目注册事实源；先原子落盘，失败时事务直接回滚。
	if err := writeProjectRegistry(s.path, payload); err != nil {
		return err
	}
	return tx.Commit()
}
