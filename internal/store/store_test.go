package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yunnnn/trellis-dash/internal/model"
)

func TestMigrateLegacyContextColumns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = legacy.Exec(`
		CREATE TABLE task_context_entries (
			project_id TEXT NOT NULL,
			task_key TEXT NOT NULL,
			action TEXT NOT NULL,
			line_no INTEGER NOT NULL,
			file_path TEXT NOT NULL,
			reason TEXT NOT NULL,
			is_example INTEGER NOT NULL DEFAULT 0,
			is_valid INTEGER NOT NULL DEFAULT 0,
			file_exists INTEGER NOT NULL DEFAULT 0,
			error TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (project_id, task_key, action, line_no)
		);
		INSERT INTO task_context_entries (
			project_id, task_key, action, line_no, file_path, reason,
			is_example, is_valid, file_exists, error
		) VALUES ('demo', 'task-a', 'implement', 1, 'README.md', '旧数据', 0, 1, 1, '');`)
	if err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	repository, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	for _, column := range []string{"entry_type", "is_duplicate"} {
		exists, err := tableHasColumn(ctx, repository.db, "task_context_entries", column)
		if err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("旧数据库未补齐列 %s", column)
		}
	}
	entries, err := repository.ListContext(ctx, "demo", "task-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Type != "file" || entries[0].Duplicate {
		t.Fatalf("旧 Context 默认值迁移异常: %#v", entries)
	}
}

func TestMigrateLegacyProjectGeneration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "legacy-project.db")
	legacy, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	now := formatTime(time.Date(2026, 7, 10, 8, 30, 0, 0, time.UTC))
	_, err = legacy.Exec(`
		CREATE TABLE projects (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			root TEXT NOT NULL UNIQUE,
			mode TEXT NOT NULL DEFAULT 'observer',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			indexed_at TEXT,
			index_error TEXT NOT NULL DEFAULT ''
		);
		INSERT INTO projects(id, name, root, mode, created_at, updated_at)
		VALUES ('demo', 'Demo', '/tmp/legacy-generation', 'observer', ?, ?);`, now, now)
	if err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	repository, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	if exists, err := tableHasColumn(ctx, repository.db, "projects", "generation"); err != nil || !exists {
		t.Fatalf("旧数据库未补齐 projects.generation: exists=%v err=%v", exists, err)
	}
	revisions, err := repository.GetRevisions(ctx, "demo")
	if err != nil || revisions.Generation == "" || strings.HasSuffix(revisions.Generation, "-") {
		t.Fatalf("旧项目 generation 初始化异常: revisions=%+v err=%v", revisions, err)
	}
}

func TestOpenUsesPrivatePermissionsAndConnectionPragmas(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDirectory := filepath.Join(t.TempDir(), "private-data")
	databasePath := filepath.Join(dataDirectory, "dashboard.db")
	repository, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })

	directoryInfo, err := os.Stat(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got := directoryInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("数据库目录权限应为 0700，实际为 %04o", got)
	}
	databaseInfo, err := os.Stat(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := databaseInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("数据库文件权限应为 0600，实际为 %04o", got)
	}
	registryInfo, err := os.Stat(projectRegistryPath(databasePath))
	if err != nil {
		t.Fatal(err)
	}
	if got := registryInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("项目注册表权限应为 0600，实际为 %04o", got)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		info, statErr := os.Stat(databasePath + suffix)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			t.Fatal(statErr)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("SQLite 辅助文件 %s 权限应为 0600，实际为 %04o", suffix, got)
		}
	}

	if got := repository.db.Stats().MaxOpenConnections; got != 8 {
		t.Fatalf("文件数据库应允许 8 条并发连接，实际为 %d", got)
	}
	// 同时占用四条连接，确认 PRAGMA 不是只配置在首条连接上。
	connections := make([]interface{ Close() error }, 0, 4)
	for index := 0; index < 4; index++ {
		connection, err := repository.db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, connection)
		var foreignKeys, busyTimeout, synchronous int
		var journalMode string
		if err := connection.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
			t.Fatal(err)
		}
		if err := connection.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
			t.Fatal(err)
		}
		if err := connection.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journalMode); err != nil {
			t.Fatal(err)
		}
		if err := connection.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&synchronous); err != nil {
			t.Fatal(err)
		}
		if foreignKeys != 1 || busyTimeout != 5000 || strings.ToLower(journalMode) != "wal" || synchronous != 1 {
			t.Fatalf("第 %d 条连接 PRAGMA 异常: foreign_keys=%d busy_timeout=%d journal_mode=%s synchronous=%d",
				index+1, foreignKeys, busyTimeout, journalMode, synchronous)
		}
	}
	for _, connection := range connections {
		if err := connection.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestProjectRegistryRestoresAfterDatabaseDeletion(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	databasePath := filepath.Join(base, "dashboard.db")
	createdAt := time.Date(2026, 7, 10, 8, 30, 0, 123, time.UTC)

	repository, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	projects := []model.Project{
		{ID: "alpha", Name: "Alpha", Root: filepath.Join(base, "alpha"), Mode: model.ProjectModeObserver, CreatedAt: createdAt},
		{ID: "beta", Name: "Beta", Root: filepath.Join(base, "beta"), Mode: model.ProjectModeObserver, CreatedAt: createdAt.Add(time.Minute)},
	}
	for _, project := range projects {
		if err := repository.UpsertProject(ctx, project); err != nil {
			t.Fatal(err)
		}
	}
	beforeRebuild, err := repository.GetRevisions(ctx, "alpha")
	if err != nil || beforeRebuild.Generation == "" {
		t.Fatalf("读取初始 generation: revisions=%+v err=%v", beforeRebuild, err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	registryPath := projectRegistryPath(databasePath)
	registryInfo, err := os.Stat(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && registryInfo.Mode().Perm() != 0o600 {
		t.Fatalf("sidecar 权限 = %04o，期望 0600", registryInfo.Mode().Perm())
	}
	if matches, err := filepath.Glob(filepath.Join(base, "."+filepath.Base(registryPath)+".tmp-*")); err != nil || len(matches) != 0 {
		t.Fatalf("原子写临时文件未清理: matches=%v err=%v", matches, err)
	}
	stable, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	stableRevision, err := stable.GetRevisions(ctx, "alpha")
	if err != nil || stableRevision.Generation != beforeRebuild.Generation {
		t.Fatalf("普通重启不应改变 generation: before=%q after=%q err=%v", beforeRebuild.Generation, stableRevision.Generation, err)
	}
	now := formatTime(time.Now().UTC())
	if _, err := stable.db.ExecContext(ctx, `
		INSERT INTO projects(id, name, root, mode, created_at, updated_at)
		VALUES ('rogue', 'Rogue', '/tmp/rogue', 'observer', ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if err := stable.Close(); err != nil {
		t.Fatal(err)
	}
	authoritative, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authoritative.GetProject(ctx, "rogue"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("sidecar 外项目应在启动恢复时移除，实际错误: %v", err)
	}
	if err := authoritative.Close(); err != nil {
		t.Fatal(err)
	}

	removeSQLiteFiles(t, databasePath)
	restored, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	restoredProjects, err := restored.ListProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(restoredProjects) != 2 {
		t.Fatalf("恢复项目数 = %d，期望 2: %+v", len(restoredProjects), restoredProjects)
	}
	afterRebuild, err := restored.GetRevisions(ctx, "alpha")
	if err != nil || afterRebuild.Generation == "" || afterRebuild.Generation == beforeRebuild.Generation {
		t.Fatalf("重建数据库必须轮换 generation: before=%q after=%q err=%v", beforeRebuild.Generation, afterRebuild.Generation, err)
	}
	byID := make(map[string]model.Project, len(restoredProjects))
	for _, project := range restoredProjects {
		byID[project.ID] = project
		if project.Revisions.Tasks != 0 || project.Revisions.Sessions != 0 || project.Revisions.Git != 0 || project.Revisions.Activity != 0 {
			t.Fatalf("恢复项目 revision 应从 0 开始: %+v", project.Revisions)
		}
	}
	if got := byID["alpha"]; got.Name != "Alpha" || got.Root != projects[0].Root || got.Mode != model.ProjectModeObserver || !got.CreatedAt.Equal(createdAt) {
		t.Fatalf("alpha 恢复异常: %+v", got)
	}
	if got := byID["beta"]; got.Name != "Beta" || got.Root != projects[1].Root || got.Mode != model.ProjectModeObserver || !got.CreatedAt.Equal(createdAt.Add(time.Minute)) {
		t.Fatalf("beta 恢复异常: %+v", got)
	}

	// 删除操作也必须同步 sidecar，否则再次丢失 SQLite 后会复活已删除项目。
	if err := restored.DeleteProject(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	if err := restored.Close(); err != nil {
		t.Fatal(err)
	}
	removeSQLiteFiles(t, databasePath)
	restoredAgain, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer restoredAgain.Close()
	remaining, err := restoredAgain.ListProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].ID != "beta" {
		t.Fatalf("删除后 sidecar 恢复异常: %+v", remaining)
	}
}

func TestProjectRegistryWriteFailureRollsBackMutation(t *testing.T) {
	for _, operation := range []string{"upsert", "delete"} {
		t.Run(operation, func(t *testing.T) {
			databasePath := filepath.Join(t.TempDir(), "dashboard.db")
			repository, err := Open(databasePath)
			if err != nil {
				t.Fatal(err)
			}
			defer repository.Close()
			ctx := context.Background()
			project := model.Project{ID: "demo", Name: "Demo", Root: "/tmp/demo", Mode: model.ProjectModeObserver}
			if operation == "delete" {
				if err := repository.UpsertProject(ctx, project); err != nil {
					t.Fatal(err)
				}
			}
			registryPath := projectRegistryPath(databasePath)
			if err := os.Remove(registryPath); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(registryPath, 0o700); err != nil {
				t.Fatal(err)
			}

			if operation == "upsert" {
				err = repository.UpsertProject(ctx, project)
			} else {
				err = repository.DeleteProject(ctx, project.ID)
			}
			if err == nil || (!strings.Contains(err.Error(), "提交项目注册") && !strings.Contains(err.Error(), "提交项目删除")) {
				t.Fatalf("sidecar 写失败未向调用方返回: %v", err)
			}
			_, lookupErr := repository.GetProject(ctx, project.ID)
			if operation == "upsert" && !errors.Is(lookupErr, ErrNotFound) {
				t.Fatalf("Upsert 失败后数据库未回滚: %v", lookupErr)
			}
			if operation == "delete" && lookupErr != nil {
				t.Fatalf("Delete 失败后项目不应消失: %v", lookupErr)
			}
		})
	}
}

func removeSQLiteFiles(t *testing.T, databasePath string) {
	t.Helper()
	for _, path := range []string{databasePath, databasePath + "-wal", databasePath + "-shm"} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}
}

func TestProjectRegistryRejectsCorruptionAndLimits(t *testing.T) {
	createdAt := time.Date(2026, 7, 10, 8, 30, 0, 0, time.UTC)
	tests := []struct {
		name    string
		payload func() []byte
		want    error
	}{
		{
			name:    "损坏 JSON",
			payload: func() []byte { return []byte(`{"version":1,"projects":[`) },
			want:    ErrProjectRegistryInvalid,
		},
		{
			name:    "文件超限",
			payload: func() []byte { return bytes.Repeat([]byte("x"), maxProjectRegistryBytes+1) },
			want:    ErrProjectRegistryTooLarge,
		},
		{
			name: "项目数超限",
			payload: func() []byte {
				projects := make([]registeredProject, maxProjectRegistryItems+1)
				for index := range projects {
					projects[index] = registeredProject{
						ID: fmt.Sprintf("p-%04d", index), Name: "P",
						Root: fmt.Sprintf("/tmp/registry-p-%04d", index),
						Mode: model.ProjectModeObserver, CreatedAt: createdAt,
					}
				}
				payload, err := json.Marshal(projectRegistry{Version: projectRegistryVersion, Projects: projects})
				if err != nil {
					t.Fatal(err)
				}
				return payload
			},
			want: ErrProjectRegistryTooLarge,
		},
		{
			name: "拒绝 control 模式",
			payload: func() []byte {
				payload, err := json.Marshal(projectRegistry{Version: projectRegistryVersion, Projects: []registeredProject{{
					ID: "control", Name: "Control", Root: "/tmp/control-project",
					Mode: model.ProjectModeControl, CreatedAt: createdAt,
				}}})
				if err != nil {
					t.Fatal(err)
				}
				return payload
			},
			want: ErrProjectRegistryInvalid,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base := t.TempDir()
			databasePath := filepath.Join(base, "dashboard.db")
			if err := os.WriteFile(projectRegistryPath(databasePath), test.payload(), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := ValidateStoredProjectLocations(databasePath); !errors.Is(err, test.want) {
				t.Fatalf("只读预检错误 = %v，期望 %v", err, test.want)
			}
			if repository, err := Open(databasePath); !errors.Is(err, test.want) {
				if repository != nil {
					_ = repository.Close()
				}
				t.Fatalf("Open 错误 = %v，期望 %v", err, test.want)
			}
			if _, err := os.Stat(databasePath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("失败的只读预检不得创建数据库，Stat error=%v", err)
			}
		})
	}
}

func TestProjectRegistryPreservesRootConflict(t *testing.T) {
	base := t.TempDir()
	databasePath := filepath.Join(base, "dashboard.db")
	repository, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Now().UTC()
	if err := repository.UpsertProject(context.Background(), model.Project{
		ID: "demo", Name: "Demo", Root: filepath.Join(base, "root-a"), CreatedAt: createdAt,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(projectRegistry{Version: projectRegistryVersion, Projects: []registeredProject{{
		ID: "demo", Name: "Demo", Root: filepath.Join(base, "root-b"),
		Mode: model.ProjectModeObserver, CreatedAt: createdAt,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectRegistryPath(databasePath), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateStoredProjectLocations(databasePath); !errors.Is(err, ErrProjectRootConflict) {
		t.Fatalf("只读预检错误 = %v，期望 ErrProjectRootConflict", err)
	}
	if reopened, err := Open(databasePath); !errors.Is(err, ErrProjectRootConflict) {
		if reopened != nil {
			_ = reopened.Close()
		}
		t.Fatalf("Open 错误 = %v，期望 ErrProjectRootConflict", err)
	}
}

func TestValidateStoredProjectLocationsChecksSidecarWithoutDatabase(t *testing.T) {
	base := t.TempDir()
	projectRoot := filepath.Join(base, "project")
	databasePath := filepath.Join(projectRoot, ".cache", "dashboard.db")
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o700); err != nil {
		t.Fatal(err)
	}
	registry := projectRegistry{Version: projectRegistryVersion, Projects: []registeredProject{{
		ID: "demo", Name: "Demo", Root: projectRoot,
		Mode: model.ProjectModeObserver, CreatedAt: time.Now().UTC(),
	}}}
	payload, err := json.Marshal(registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectRegistryPath(databasePath), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateStoredProjectLocations(databasePath); !errors.Is(err, ErrDatabaseProjectOverlap) {
		t.Fatalf("sidecar 预检错误 = %v，期望 ErrDatabaseProjectOverlap", err)
	}
	if repository, err := Open(databasePath); !errors.Is(err, ErrDatabaseProjectOverlap) {
		if repository != nil {
			_ = repository.Close()
		}
		t.Fatalf("Open 错误 = %v，期望 ErrDatabaseProjectOverlap", err)
	}
	for _, path := range []string{databasePath, databasePath + "-wal", databasePath + "-shm"} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("sidecar 只读预检不得创建 %s，Stat error=%v", filepath.Base(path), err)
		}
	}
}

func TestOpenDoesNotChmodExistingParentDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不提供等价的 Unix 权限位语义")
	}
	parent := filepath.Join(t.TempDir(), "shared-parent")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	repository, err := Open(filepath.Join(parent, "dashboard.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(parent)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("不得修改既有父目录权限，实际=%04o", got)
	}
}

func TestConcurrentReadsAndSerializedWrites(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository, err := Open(filepath.Join(t.TempDir(), "dashboard.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })

	const writerCount = 24
	const readerCount = 8
	start := make(chan struct{})
	errorsChannel := make(chan error, writerCount+readerCount)
	var workers sync.WaitGroup
	for index := 0; index < writerCount; index++ {
		index := index
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			err := repository.UpsertProject(ctx, model.Project{
				ID:   fmt.Sprintf("project-%02d", index),
				Name: fmt.Sprintf("Project %02d", index),
				Root: fmt.Sprintf("/tmp/trellis-store-project-%02d", index),
			})
			if err != nil {
				errorsChannel <- err
			}
		}()
	}
	for index := 0; index < readerCount; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for iteration := 0; iteration < 20; iteration++ {
				if _, err := repository.ListProjects(ctx); err != nil {
					errorsChannel <- err
					return
				}
			}
		}()
	}
	close(start)
	workers.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		t.Errorf("并发读写失败: %v", err)
	}
	projects, err := repository.ListProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != writerCount {
		t.Fatalf("写入项目数异常: want=%d got=%d", writerCount, len(projects))
	}
}

func TestUpsertProjectRejectsRootRebinding(t *testing.T) {
	repository, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	if err := repository.UpsertProject(ctx, model.Project{ID: "demo", Name: "Old", Root: "/tmp/old-root"}); err != nil {
		t.Fatal(err)
	}
	err = repository.UpsertProject(ctx, model.Project{ID: "demo", Name: "New", Root: "/tmp/new-root"})
	if !errors.Is(err, ErrProjectRootConflict) {
		t.Fatalf("重绑定错误 = %v，期望 ErrProjectRootConflict", err)
	}
	project, err := repository.GetProject(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if project.Root != "/tmp/old-root" || project.Name != "Old" {
		t.Fatalf("冲突后旧项目被修改: %+v", project)
	}
}

func TestProjectGenerationChangesOnlyForNewIncarnation(t *testing.T) {
	repository, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	project := model.Project{
		ID: "demo", Name: "Demo", Root: "/tmp/project-generation",
		CreatedAt: time.Date(2026, 7, 10, 8, 30, 0, 0, time.UTC),
	}
	if err := repository.UpsertProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	first, err := repository.GetRevisions(ctx, project.ID)
	if err != nil || first.Generation == "" {
		t.Fatalf("读取初始 generation: revisions=%+v err=%v", first, err)
	}

	project.Name = "Renamed"
	if err := repository.UpsertProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	updated, err := repository.GetRevisions(ctx, project.ID)
	if err != nil || updated.Generation != first.Generation {
		t.Fatalf("更新现有项目不应换代: before=%q after=%q err=%v", first.Generation, updated.Generation, err)
	}

	if err := repository.DeleteProject(ctx, project.ID); err != nil {
		t.Fatal(err)
	}
	// 即使调用方复用完全相同的 createdAt，同 ID 新实例也必须轮换 ETag generation。
	if err := repository.UpsertProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	recreated, err := repository.GetRevisions(ctx, project.ID)
	if err != nil || recreated.Generation == "" || recreated.Generation == first.Generation {
		t.Fatalf("删除后重建必须换代: before=%q after=%q err=%v", first.Generation, recreated.Generation, err)
	}
}

func TestUpsertProjectRejectsControlMode(t *testing.T) {
	repository, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	err = repository.UpsertProject(context.Background(), model.Project{
		ID: "control", Name: "Control", Root: "/tmp/control", Mode: model.ProjectModeControl,
	})
	if !errors.Is(err, ErrProjectRegistryInvalid) {
		t.Fatalf("control 模式错误 = %v，期望 ErrProjectRegistryInvalid", err)
	}
	projects, listErr := repository.ListProjects(context.Background())
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(projects) != 0 {
		t.Fatalf("被拒绝的 control 项目不应写入数据库: %+v", projects)
	}
}

func TestValidateDatabaseOutsideProject(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	realRoot := filepath.Join(base, "real")
	projectRoot := filepath.Join(realRoot, "project")
	if err := os.MkdirAll(projectRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	aliasRoot := filepath.Join(base, "alias")
	if err := os.Symlink(realRoot, aliasRoot); err != nil {
		t.Skipf("当前文件系统不支持符号链接: %v", err)
	}

	tests := []struct {
		name        string
		database    string
		project     string
		wantOverlap bool
	}{
		{
			name:        "数据库经符号链接落在项目内",
			database:    filepath.Join(aliasRoot, "project", ".dashboard", "dashboard.db"),
			project:     projectRoot,
			wantOverlap: true,
		},
		{
			name:     "项目可位于数据库父目录的其他路径",
			database: filepath.Join(realRoot, "data", "dashboard.db"),
			project:  filepath.Join(aliasRoot, "data", "nested-project"),
		},
		{
			name:     "互相独立",
			database: filepath.Join(realRoot, "data", "dashboard.db"),
			project:  projectRoot,
		},
		{
			name:     "内存数据库不写项目",
			database: ":memory:",
			project:  projectRoot,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateDatabaseOutsideProject(test.database, test.project)
			if test.wantOverlap && !errors.Is(err, ErrDatabaseProjectOverlap) {
				t.Fatalf("应识别路径重叠，实际错误: %v", err)
			}
			if !test.wantOverlap && err != nil {
				t.Fatalf("独立路径不应报错: %v", err)
			}
		})
	}

	if runtime.GOOS == "darwin" {
		canonicalBase, err := filepath.EvalSymlinks(base)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(canonicalBase, "/private/var/") {
			varAlias := strings.TrimPrefix(canonicalBase, "/private")
			macProject := filepath.Join(canonicalBase, "mac-project")
			if err := os.MkdirAll(macProject, 0o700); err != nil {
				t.Fatal(err)
			}
			err := ValidateDatabaseOutsideProject(
				filepath.Join(varAlias, "mac-project", "dashboard.db"),
				macProject,
			)
			if !errors.Is(err, ErrDatabaseProjectOverlap) {
				t.Fatalf("应解析 macOS /var 符号链接，实际错误: %v", err)
			}
		}
	}
}

func TestValidateStoredProjectLocationsBeforeReadWriteOpen(t *testing.T) {
	base := t.TempDir()
	projectRoot := filepath.Join(base, "project")
	if err := os.MkdirAll(filepath.Join(projectRoot, ".trellis"), 0o700); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(base, "outside.db")
	repository, err := Open(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.UpsertProject(context.Background(), model.Project{ID: "demo", Name: "Demo", Root: projectRoot}); err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(projectRoot, ".cache", "dashboard.db")
	if err := os.MkdirAll(filepath.Dir(inside), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(original, inside); err != nil {
		t.Fatal(err)
	}
	if err := ValidateStoredProjectLocations(inside); !errors.Is(err, ErrDatabaseProjectOverlap) {
		t.Fatalf("只读预检错误 = %v，期望 ErrDatabaseProjectOverlap", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(inside + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("只读预检不得创建 %s，Stat error=%v", suffix, err)
		}
	}
}

func TestOpenRevalidatesProjectsVisibleOnlyThroughWAL(t *testing.T) {
	base := t.TempDir()
	projectRoot := filepath.Join(base, "project")
	if err := os.MkdirAll(filepath.Join(projectRoot, ".trellis", "tasks"), 0o700); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(projectRoot, ".cache", "dashboard.db")
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o700); err != nil {
		t.Fatal(err)
	}
	writer, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	now := formatTime(time.Now().UTC())
	if _, err := writer.Exec(`
		PRAGMA journal_mode=WAL;
		PRAGMA wal_autocheckpoint=0;
		CREATE TABLE projects (
			id TEXT PRIMARY KEY, name TEXT NOT NULL, root TEXT NOT NULL UNIQUE,
			mode TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
			indexed_at TEXT, index_error TEXT NOT NULL DEFAULT ''
		);
		INSERT INTO projects(id, name, root, mode, created_at, updated_at)
		VALUES ('inside', 'Inside', ?, 'observer', ?, ?);`, projectRoot, now, now); err != nil {
		t.Fatal(err)
	}
	if repository, err := Open(databasePath); !errors.Is(err, ErrDatabaseProjectOverlap) {
		if repository != nil {
			_ = repository.Close()
		}
		t.Fatalf("正常连接读取 WAL 后必须拒绝路径重叠，实际错误: %v", err)
	}
}

func TestTaskStatisticsCountsRuntimeBlockedOnlyWhenActive(t *testing.T) {
	ctx := context.Background()
	repository, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	if err := repository.UpsertProject(ctx, model.Project{ID: "stats", Name: "Stats", Root: "/tmp/stats"}); err != nil {
		t.Fatal(err)
	}
	task := func(key, phase string, archived bool) model.Task {
		return model.Task{
			ProjectID: "stats", Key: key, ID: key, Directory: key, Name: key, Title: key,
			Status: "in_progress", RuntimePhase: phase, Priority: "P2", CreatedAt: "2026-07-10",
			Subtasks: json.RawMessage(`[]`), Children: json.RawMessage(`[]`), RelatedFiles: json.RawMessage(`[]`), Meta: json.RawMessage(`{}`),
			Archived: archived, SourcePath: key + "/task.json", SourceHash: key, ModifiedAt: time.Now().UTC(),
		}
	}
	_, err = repository.ReplaceTrellisSnapshot(ctx, "stats", model.TrellisSnapshot{
		TasksHash: "stats-v1", SessionsHash: "sessions", SpecsHash: "specs",
		Tasks: []model.Task{task("active-blocked", "blocked", false), task("active-ok", "implementing", false), task("archived-blocked", "blocked", true)},
	})
	if err != nil {
		t.Fatal(err)
	}
	statistics, err := repository.TaskStatistics(ctx, "stats")
	if err != nil {
		t.Fatal(err)
	}
	if statistics.Blocked != 1 || statistics.Active != 2 || statistics.Archived != 1 {
		t.Fatalf("阻塞统计异常: %+v", statistics)
	}
	projects, err := repository.ListProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].ActiveTaskCount != statistics.Active {
		t.Fatalf("项目列表活跃任务数应与概览统计一致: projects=%+v statistics=%+v", projects, statistics)
	}
}

func TestTaskCompletionCountsIncludesArchivedAndIgnoresInvalidDates(t *testing.T) {
	ctx := context.Background()
	repository, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	if err := repository.UpsertProject(ctx, model.Project{ID: "heatmap", Name: "Heatmap", Root: "/tmp/heatmap"}); err != nil {
		t.Fatal(err)
	}

	completedDay := "2026-07-10T09:30:00+08:00"
	completedDateOnly := "2026-07-10"
	outsideRange := "2025-06-01"
	invalidDate := "not-a-date"
	task := func(key string, completedAt *string, archived bool) model.Task {
		return model.Task{
			ProjectID: "heatmap", Key: key, ID: key, Directory: key, Name: key, Title: key,
			Status: "in_progress", RuntimePhase: "implementing", Priority: "P2", CreatedAt: "2026-07-01",
			CompletedAt: completedAt, Subtasks: json.RawMessage(`[]`), Children: json.RawMessage(`[]`),
			RelatedFiles: json.RawMessage(`[]`), Meta: json.RawMessage(`{}`), Archived: archived,
			SourcePath: key + "/task.json", SourceHash: key, ModifiedAt: time.Now().UTC(),
		}
	}
	_, err = repository.ReplaceTrellisSnapshot(ctx, "heatmap", model.TrellisSnapshot{
		TasksHash: "heatmap-v1", SessionsHash: "sessions", SpecsHash: "specs",
		Tasks: []model.Task{
			task("active", &completedDay, false),
			task("archived", &completedDateOnly, true),
			task("outside", &outsideRange, false),
			task("invalid", &invalidDate, false),
			task("unfinished", nil, false),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	items, err := repository.TaskCompletionCounts(ctx, "heatmap", "2026-07-09", "2026-07-11")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || items[0].Count != 0 || items[1].Date != "2026-07-10" || items[1].Count != 2 || items[2].Count != 0 {
		t.Fatalf("完成任务日历聚合异常: %#v", items)
	}
}

func TestReplaceSnapshotAndQueries(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository, err := Open(filepath.Join(t.TempDir(), "dashboard.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })

	project := model.Project{ID: "demo", Name: "Demo", Root: "/tmp/demo", Mode: model.ProjectModeObserver}
	if err := repository.UpsertProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	snapshot := model.TrellisSnapshot{
		TasksHash: "tasks-v1", SessionsHash: "sessions-v1", SpecsHash: "specs-v1",
		Tasks: []model.Task{{
			ProjectID: "demo", Key: "07-10-demo", ID: "demo", Directory: "07-10-demo",
			Name: "demo", Title: "演示任务", Status: "in_progress", RuntimePhase: "implementing",
			Priority: "P1", Creator: "tester", Assignee: "tester", CreatedAt: "2026-07-10",
			Subtasks: json.RawMessage(`[]`), Children: json.RawMessage(`[]`),
			RelatedFiles: json.RawMessage(`[]`), Meta: json.RawMessage(`{}`),
			SourcePath: ".trellis/tasks/07-10-demo/task.json", SourceHash: "task-v1",
			ModifiedAt: now, ArtifactCount: 1,
		}},
		Artifacts: []model.Artifact{{
			ProjectID: "demo", TaskKey: "07-10-demo", Kind: "prd", Name: "prd.md",
			Path: ".trellis/tasks/07-10-demo/prd.md", ContentType: "text/markdown",
			Content: "# PRD", Size: 5, Hash: "artifact-v1", ModifiedAt: now,
		}},
		ContextEntries: []model.ContextEntry{{
			ProjectID: "demo", TaskKey: "07-10-demo", Action: "implement", Line: 1,
			Type: "directory", File: ".trellis/spec/backend", Reason: "规范",
			Duplicate: true, Valid: false, Exists: true,
		}},
		Sessions: []model.Session{{
			ProjectID: "demo", Key: "codex_1", Platform: "codex",
			CurrentTask: ".trellis/tasks/07-10-demo", TaskKey: "07-10-demo",
			LastSeenAt: &now, CurrentRun: json.RawMessage(`null`),
		}},
		WorkflowStates: []model.WorkflowState{{ProjectID: "demo", Name: "in_progress", Label: "In Progress", Order: 0}},
	}
	changed, err := repository.ReplaceTrellisSnapshot(ctx, "demo", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("首次快照应被识别为变化")
	}

	page, err := repository.ListTasks(ctx, "demo", model.TaskFilter{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Title != "演示任务" {
		t.Fatalf("任务查询异常: %#v", page)
	}
	artifacts, err := repository.ListArtifacts(ctx, "demo", "07-10-demo")
	if err != nil || len(artifacts) != 1 || artifacts[0].Content != "# PRD" {
		t.Fatalf("文档查询异常: %#v, %v", artifacts, err)
	}
	entries, err := repository.ListContext(ctx, "demo", "07-10-demo")
	if err != nil || len(entries) != 1 || entries[0].Type != "directory" ||
		!entries[0].Duplicate || entries[0].Valid {
		t.Fatalf("Context 查询异常: %#v, %v", entries, err)
	}

	revisions, err := repository.GetRevisions(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if revisions.Tasks != 1 || revisions.Sessions != 1 || revisions.Specs != 1 || revisions.Activity != 1 {
		t.Fatalf("首次 revision 异常: %#v", revisions)
	}
	changed, err = repository.ReplaceTrellisSnapshot(ctx, "demo", snapshot)
	if err != nil || changed {
		t.Fatalf("相同快照不应变化: changed=%v err=%v", changed, err)
	}
	after, _ := repository.GetRevisions(ctx, "demo")
	if after != revisions {
		t.Fatalf("相同快照不应提升 revision: before=%#v after=%#v", revisions, after)
	}

	snapshot.TasksHash = "tasks-v2"
	snapshot.Tasks[0].SourceHash = "task-v2"
	snapshot.Tasks[0].Status = "completed"
	if _, err := repository.ReplaceTrellisSnapshot(ctx, "demo", snapshot); err != nil {
		t.Fatal(err)
	}
	updated, err := repository.GetTask(ctx, "demo", "07-10-demo")
	if err != nil || updated.Status != "completed" {
		t.Fatalf("任务更新异常: %#v, %v", updated, err)
	}

	// 只有 PRD/Context/workflow 变化时也应留下活动，而不是只提升 tasks revision。
	snapshot.TasksHash = "tasks-v3"
	snapshot.Artifacts[0].Hash = "artifact-v2"
	snapshot.Artifacts[0].Content = "# PRD v2"
	if _, err := repository.ReplaceTrellisSnapshot(ctx, "demo", snapshot); err != nil {
		t.Fatal(err)
	}
	activity, err := repository.RecentActivity(ctx, "demo", 1)
	if err != nil || len(activity) != 1 || activity[0].Type != "trellis.resources.updated" {
		t.Fatalf("资源变化活动异常: activity=%#v err=%v", activity, err)
	}
}

func TestReplaceTaskBundleDoesNotRewriteUnchangedTasks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository, err := Open(filepath.Join(t.TempDir(), "dashboard.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	if err := repository.UpsertProject(ctx, model.Project{ID: "incremental", Name: "Incremental", Root: "/tmp/incremental"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	task := func(key, indexHash string) model.Task {
		return model.Task{
			ProjectID: "incremental", Key: key, ID: key, Directory: key, Name: key, Title: key,
			Status: "in_progress", RuntimePhase: "implementing", Priority: "P2",
			Subtasks: json.RawMessage(`[]`), Children: json.RawMessage(`[]`),
			RelatedFiles: json.RawMessage(`[]`), Meta: json.RawMessage(`{}`),
			SourcePath: ".trellis/tasks/" + key + "/task.json", SourceHash: key + "-source",
			IndexHash: indexHash, ModifiedAt: now, ArtifactCount: 1,
		}
	}
	artifact := func(key, hash, content string) model.Artifact {
		return model.Artifact{
			ProjectID: "incremental", TaskKey: key, Kind: "prd", Name: "prd.md",
			Path: ".trellis/tasks/" + key + "/prd.md", ContentType: "text/markdown",
			Hash: hash, Content: content, Size: int64(len(content)), ModifiedAt: now,
		}
	}
	initial := model.TrellisSnapshot{
		TasksHash: "canonical-v1", SessionsHash: "sessions-v1",
		Tasks:     []model.Task{task("task-a", "task-a-v1"), task("task-b", "task-b-v1")},
		Artifacts: []model.Artifact{artifact("task-a", "a-v1", "A v1"), artifact("task-b", "b-v1", "B v1")},
	}
	if _, err := repository.ReplaceTrellisSnapshot(ctx, "incremental", initial); err != nil {
		t.Fatal(err)
	}
	var taskBRowID, artifactBRowID int64
	if err := repository.db.QueryRowContext(ctx, `SELECT rowid FROM tasks WHERE project_id = ? AND task_key = ?`, "incremental", "task-b").Scan(&taskBRowID); err != nil {
		t.Fatal(err)
	}
	if err := repository.db.QueryRowContext(ctx, `SELECT rowid FROM task_artifacts WHERE project_id = ? AND task_key = ?`, "incremental", "task-b").Scan(&artifactBRowID); err != nil {
		t.Fatal(err)
	}
	before, _ := repository.GetRevisions(ctx, "incremental")
	updatedTask := task("task-a", "task-a-v2")
	updatedTask.Title = "A v2"
	changed, err := repository.ReplaceTaskBundle(ctx, "incremental", model.TaskBundle{
		Task: updatedTask, Artifacts: []model.Artifact{artifact("task-a", "a-v2", "A v2")},
	})
	if err != nil || !changed {
		t.Fatalf("单任务增量更新失败: changed=%v err=%v", changed, err)
	}
	var currentTaskBRowID, currentArtifactBRowID int64
	_ = repository.db.QueryRowContext(ctx, `SELECT rowid FROM tasks WHERE project_id = ? AND task_key = ?`, "incremental", "task-b").Scan(&currentTaskBRowID)
	_ = repository.db.QueryRowContext(ctx, `SELECT rowid FROM task_artifacts WHERE project_id = ? AND task_key = ?`, "incremental", "task-b").Scan(&currentArtifactBRowID)
	if currentTaskBRowID != taskBRowID || currentArtifactBRowID != artifactBRowID {
		t.Fatalf("未变化任务被重写: task %d->%d artifact %d->%d", taskBRowID, currentTaskBRowID, artifactBRowID, currentArtifactBRowID)
	}
	after, _ := repository.GetRevisions(ctx, "incremental")
	if after.Tasks != before.Tasks+1 {
		t.Fatalf("增量更新 revision 异常: before=%d after=%d", before.Tasks, after.Tasks)
	}
	initial.TasksHash = "canonical-v2"
	initial.Tasks[0] = updatedTask
	initial.Artifacts[0] = artifact("task-a", "a-v2", "A v2")
	changed, err = repository.ReplaceTrellisSnapshot(ctx, "incremental", initial)
	if err != nil || changed {
		t.Fatalf("全量校验不应重写已同步缓存: changed=%v err=%v", changed, err)
	}
	finalRevision, _ := repository.GetRevisions(ctx, "incremental")
	if finalRevision.Tasks != after.Tasks {
		t.Fatalf("仅规范化全局 Hash 不应提升 revision: before=%d after=%d", after.Tasks, finalRevision.Tasks)
	}
}

func TestRecentTaskActivityUsesExactDatabaseFilter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository, err := Open(filepath.Join(t.TempDir(), "dashboard.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	if err := repository.UpsertProject(ctx, model.Project{ID: "demo", Name: "Demo", Root: "/tmp/activity-demo"}); err != nil {
		t.Fatal(err)
	}

	task := func(key string) model.Task {
		return model.Task{
			ProjectID: "demo", Key: key, ID: key, Directory: key, Name: key,
			Title: key, Status: "in_progress", RuntimePhase: "implementing",
			Priority: "P2", CreatedAt: "2026-07-10", SourcePath: key + "/task.json",
			SourceHash: key + "-v1", ModifiedAt: time.Now().UTC(),
			Subtasks: json.RawMessage(`[]`), Children: json.RawMessage(`[]`),
			RelatedFiles: json.RawMessage(`[]`), Meta: json.RawMessage(`{}`),
		}
	}
	if _, err := repository.ReplaceTrellisSnapshot(ctx, "demo", model.TrellisSnapshot{
		TasksHash: "tasks-v1", SessionsHash: "sessions-v1",
		Tasks: []model.Task{task("task-a"), task("task-b")},
	}); err != nil {
		t.Fatal(err)
	}

	// 在 task-a 的旧事件之后写入超过项目默认窗口的 task-b 事件，防止回退成
	// “先截项目最近 100 条、再在内存过滤”的错误实现。
	unlockProject := repository.lockProject("demo")
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		unlockProject()
		t.Fatal(err)
	}
	for index := 0; index < 125; index++ {
		payload, _ := json.Marshal(map[string]int{"index": index})
		if err := insertActivity(ctx, tx, "demo", "task-b", "task.updated", "test", payload, time.Now().UTC()); err != nil {
			_ = tx.Rollback()
			unlockProject()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		unlockProject()
		t.Fatal(err)
	}
	unlockProject()

	items, err := repository.RecentTaskActivity(ctx, "demo", "task-a", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].TaskKey != "task-a" || items[0].Type != "task.created" {
		t.Fatalf("任务活动过滤异常: %#v", items)
	}
	bItems, err := repository.RecentTaskActivity(ctx, "demo", "task-b", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(bItems) != 5 {
		t.Fatalf("任务活动 limit 异常: want=5 got=%d", len(bItems))
	}
	for _, item := range bItems {
		if item.TaskKey != "task-b" {
			t.Fatalf("查询混入其他任务: %#v", item)
		}
	}
}

func TestActivityRetentionAndPayloadBound(t *testing.T) {
	repository, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	if err := repository.UpsertProject(ctx, model.Project{ID: "demo", Name: "Demo", Root: "/tmp/demo"}); err != nil {
		t.Fatal(err)
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < maxActivityPerProject+25; index++ {
		payload := []byte(`{"index":1}`)
		if index == maxActivityPerProject+24 {
			payload = bytes.Repeat([]byte("x"), maxActivityPayloadBytes+1)
		}
		if err := insertActivity(ctx, tx, "demo", "", "test.event", "test", payload, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	if err := pruneActivity(ctx, tx, "demo"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var count, maxPayload int
	if err := repository.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(MAX(length(payload_json)), 0)
		FROM activity_events WHERE project_id = 'demo'`).Scan(&count, &maxPayload); err != nil {
		t.Fatal(err)
	}
	if count != maxActivityPerProject {
		t.Fatalf("活动保留数 = %d，期望 %d", count, maxActivityPerProject)
	}
	if maxPayload > maxActivityPayloadBytes {
		t.Fatalf("活动 payload 仍超过上限: %d", maxPayload)
	}
	latest, err := repository.ListActivity(ctx, "demo", 0, 0, 2)
	if err != nil || len(latest.Items) != 2 || !latest.HasMore || latest.FirstID == 0 {
		t.Fatalf("最新活动页异常: page=%+v err=%v", latest, err)
	}
	older, err := repository.ListActivity(ctx, "demo", 0, latest.FirstID, 2)
	if err != nil || len(older.Items) != 2 || older.LastID >= latest.FirstID {
		t.Fatalf("向前分页异常: page=%+v err=%v", older, err)
	}
}

func TestGitSnapshotAndDashboard(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository, err := Open(filepath.Join(t.TempDir(), "dashboard.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	if err := repository.UpsertProject(ctx, model.Project{ID: "demo", Name: "Demo", Root: "/tmp/demo"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	git := model.GitSnapshot{
		ProjectID: "demo", Branch: "main", Head: "abc", Hash: "git-v1", UpdatedAt: now,
		LinesAdded: 12, LinesDeleted: 5,
	}
	changed, err := repository.ReplaceGitSnapshot(ctx, git)
	if err != nil || !changed {
		t.Fatalf("保存 Git 快照失败: changed=%v err=%v", changed, err)
	}
	changed, err = repository.ReplaceGitSnapshot(ctx, git)
	if err != nil || changed {
		t.Fatalf("相同 Git 快照不应变化: changed=%v err=%v", changed, err)
	}
	if _, err := repository.db.ExecContext(ctx, `UPDATE git_snapshots SET summary_json = '{}' WHERE project_id = ?`, "demo"); err != nil {
		t.Fatal(err)
	}
	changed, err = repository.ReplaceGitSnapshot(ctx, git)
	if err != nil || changed {
		t.Fatalf("旧缓存摘要回填不应提升 revision: changed=%v err=%v", changed, err)
	}
	summary, err := repository.GetGitSummary(ctx, "demo")
	if err != nil || summary == nil || summary.ProjectID != "demo" || summary.Branch != "main" {
		t.Fatalf("旧缓存摘要未回填: summary=%+v err=%v", summary, err)
	}
	if summary.LinesAdded != 12 || summary.LinesDeleted != 5 {
		t.Fatalf("概览摘要缺少代码行统计: %+v", summary)
	}
	dashboard, err := repository.Dashboard(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if dashboard.Git == nil || dashboard.Git.Branch != "main" {
		t.Fatalf("概览 Git 数据异常: %#v", dashboard.Git)
	}
}

func TestGetGitSnapshotMapsWorktreesToActiveTasks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	base := t.TempDir()
	repository, err := Open(filepath.Join(base, "dashboard.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	projectRoot := filepath.Join(base, "project")
	if err := os.MkdirAll(projectRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := repository.UpsertProject(ctx, model.Project{ID: "demo", Name: "Demo", Root: projectRoot}); err != nil {
		t.Fatal(err)
	}

	worktreeRoot := filepath.Join(base, "worktrees")
	realExact := filepath.Join(worktreeRoot, "exact-real")
	aliasExact := filepath.Join(worktreeRoot, "exact-alias")
	branchOnly := filepath.Join(worktreeRoot, "branch-only")
	sharedBranch := filepath.Join(worktreeRoot, "shared-branch")
	archivedPath := filepath.Join(worktreeRoot, "archived")
	for _, path := range []string{realExact, branchOnly, sharedBranch, archivedPath} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(realExact, aliasExact); err != nil {
		aliasExact = realExact
	}
	pointer := func(value string) *string { return &value }
	task := func(key, branch string) model.Task {
		return model.Task{
			ProjectID: "demo", Key: key, ID: key, Directory: key, Name: key,
			Title: key, Status: "in_progress", RuntimePhase: "implementing",
			Priority: "P2", CreatedAt: "2026-07-10", Branch: pointer(branch),
			SourcePath: key + "/task.json", SourceHash: key + "-v1", ModifiedAt: time.Now().UTC(),
			Subtasks: json.RawMessage(`[]`), Children: json.RawMessage(`[]`),
			RelatedFiles: json.RawMessage(`[]`), Meta: json.RawMessage(`{}`),
		}
	}
	exactTask := task("task-exact", "feature/shared")
	exactTask.WorktreePath = pointer(aliasExact)
	uniqueTask := task("task-unique", "feature/unique")
	sharedTaskA := task("task-shared-a", "feature/shared")
	sharedTaskB := task("task-shared-b", "feature/shared")
	archivedTask := task("task-archived", "feature/archived")
	archivedTask.Archived = true
	archivedTask.WorktreePath = pointer(archivedPath)
	if _, err := repository.ReplaceTrellisSnapshot(ctx, "demo", model.TrellisSnapshot{
		TasksHash: "tasks-v1", SessionsHash: "sessions-v1",
		Tasks: []model.Task{exactTask, uniqueTask, sharedTaskA, sharedTaskB, archivedTask},
	}); err != nil {
		t.Fatal(err)
	}
	gitSnapshot := model.GitSnapshot{
		ProjectID: "demo", Branch: "main", Head: "abc", Hash: "git-v1", UpdatedAt: time.Now().UTC(),
		Worktrees: []model.Worktree{
			{Path: realExact, Branch: "feature/shared"},
			{Path: branchOnly, Branch: "refs/heads/feature/unique"},
			{Path: sharedBranch, Branch: "feature/shared"},
			{Path: archivedPath, Branch: "feature/archived"},
		},
	}
	if _, err := repository.ReplaceGitSnapshot(ctx, gitSnapshot); err != nil {
		t.Fatal(err)
	}
	stored, err := repository.GetGitSnapshot(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if stored == nil || len(stored.Worktrees) != 4 {
		t.Fatalf("Worktree 快照异常: %#v", stored)
	}
	if stored.Worktrees[0].TaskKey != "task-exact" {
		t.Fatalf("规范路径优先映射失败: %#v", stored.Worktrees[0])
	}
	if stored.Worktrees[1].TaskKey != "task-unique" {
		t.Fatalf("唯一分支映射失败: %#v", stored.Worktrees[1])
	}
	if stored.Worktrees[2].TaskKey != "" {
		t.Fatalf("重复分支不应猜测任务: %#v", stored.Worktrees[2])
	}
	if stored.Worktrees[3].TaskKey != "" {
		t.Fatalf("归档任务不应参与映射: %#v", stored.Worktrees[3])
	}
}

func TestIndexHealthChangesRevision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository, err := Open(filepath.Join(t.TempDir(), "dashboard.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	if err := repository.UpsertProject(ctx, model.Project{ID: "demo", Name: "Demo", Root: "/tmp/demo"}); err != nil {
		t.Fatal(err)
	}
	snapshot := model.TrellisSnapshot{TasksHash: "tasks", SessionsHash: "sessions"}
	if _, err := repository.ReplaceTrellisSnapshot(ctx, "demo", snapshot); err != nil {
		t.Fatal(err)
	}
	before, _ := repository.GetRevisions(ctx, "demo")
	indexErr := errors.New("task.json 写入中")
	if err := repository.SetIndexError(ctx, "demo", indexErr); err != nil {
		t.Fatal(err)
	}
	failed, _ := repository.GetRevisions(ctx, "demo")
	if failed.Activity != before.Activity+1 {
		t.Fatalf("索引失败未提升 activity revision: before=%d after=%d", before.Activity, failed.Activity)
	}
	if err := repository.SetIndexError(ctx, "demo", indexErr); err != nil {
		t.Fatal(err)
	}
	repeated, _ := repository.GetRevisions(ctx, "demo")
	if repeated.Activity != failed.Activity {
		t.Fatal("相同错误不应反复提升 revision")
	}
	changed, err := repository.ReplaceTrellisSnapshot(ctx, "demo", snapshot)
	if err != nil || !changed {
		t.Fatalf("恢复健康状态失败: changed=%v err=%v", changed, err)
	}
	recovered, _ := repository.GetRevisions(ctx, "demo")
	if recovered.Activity != failed.Activity+1 {
		t.Fatalf("恢复未提升 activity revision: failed=%d recovered=%d", failed.Activity, recovered.Activity)
	}
	project, _ := repository.GetProject(ctx, "demo")
	if project.IndexError != "" {
		t.Fatalf("恢复后仍有错误: %s", project.IndexError)
	}
}
