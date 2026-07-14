package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yunnnn/trellis-dash/internal/model"
	_ "modernc.org/sqlite"
)

var (
	ErrNotFound               = errors.New("resource not found")
	ErrDatabaseProjectOverlap = errors.New("数据库目录与项目根目录重叠")
	ErrProjectRootConflict    = errors.New("项目 ID 已绑定到其他根目录")
)

// Store 是可重建的 SQLite 查询缓存；业务事实仍然来自项目文件。
type Store struct {
	db          *sql.DB
	path        string
	memory      bool
	lifecycleMu sync.RWMutex
	registryMu  sync.Mutex
	// projectLocks 只串行化同一项目的写入；不同项目交给 SQLite WAL 与 busy_timeout 协调。
	projectLocks sync.Map
}

func Open(path string) (*Store, error) {
	resolved, err := expandPath(path)
	if err != nil {
		return nil, err
	}
	// 必须先只读检查 SQLite 与 sidecar，避免发现路径越界或注册表损坏前
	// 就创建数据库目录、WAL/SHM 或执行迁移。
	if err := ValidateStoredProjectLocations(resolved); err != nil {
		return nil, fmt.Errorf("数据库打开前检查失败: %w", err)
	}
	if resolved != ":memory:" {
		if err := ensurePrivateDirectory(filepath.Dir(resolved)); err != nil {
			return nil, fmt.Errorf("创建数据库目录: %w", err)
		}
	}

	dsn := sqliteDSN(resolved)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开 SQLite: %w", err)
	}
	memory := resolved == ":memory:"
	if memory {
		// 普通 :memory: 的每条连接各有独立数据库，因此内存测试库保持单连接。
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
	} else {
		// WAL 允许查询并发执行；同项目写入由项目锁串行，不同项目交给 SQLite 协调。
		db.SetMaxOpenConns(8)
		db.SetMaxIdleConns(8)
	}

	s := &Store{db: db, path: resolved, memory: memory}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.restoreProjectsFromRegistry(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("恢复项目注册表: %w", err)
	}
	if err := s.tightenFilePermissions(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	closeErr := s.db.Close()
	permissionErr := s.tightenFilePermissions()
	return errors.Join(closeErr, permissionErr)
}

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// DatabasePath 返回展开并规范化后的数据库路径，内存库返回 :memory:。
func (s *Store) DatabasePath() string { return s.path }

// DatabaseFileSizes 返回 SQLite 主文件与 WAL 当前占用，内存库返回 0。
func (s *Store) DatabaseFileSizes() (databaseBytes, walBytes int64) {
	if s.path == ":memory:" {
		return 0, 0
	}
	if info, err := os.Stat(s.path); err == nil {
		databaseBytes = info.Size()
	}
	if info, err := os.Stat(s.path + "-wal"); err == nil {
		walBytes = info.Size()
	}
	return databaseBytes, walBytes
}

// sqliteDSN 将关键 PRAGMA 写入 DSN，使连接池中新建的每条连接都采用相同配置。
func sqliteDSN(path string) string {
	query := url.Values{}
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "synchronous(NORMAL)")
	if path == ":memory:" {
		return path + "?" + query.Encode()
	}
	databaseURL := &url.URL{Scheme: "file", Path: path, RawQuery: query.Encode()}
	return databaseURL.String()
}

func ensurePrivateDirectory(path string) error {
	info, err := os.Stat(path)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("%s 不是目录", path)
		}
		// 不修改调用方提供的既有父目录（例如 /tmp 或挂载点）权限。
		// 数据库文件本身仍会固定为 0600；新建的专用目录才收紧为 0700。
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	// umask 只会进一步收紧权限；显式 chmod 让新建最终目录的契约可验证。
	return os.Chmod(path, 0o700)
}

func (s *Store) tightenFilePermissions() error {
	if s.memory {
		return nil
	}
	for _, path := range []string{s.path, s.path + "-wal", s.path + "-shm"} {
		if err := os.Chmod(path, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("收紧数据库文件权限 %s: %w", path, err)
		}
	}
	return nil
}

func expandPath(path string) (string, error) {
	if path == "" {
		path = "~/.local/share/trellis-dashboard/dashboard.db"
	}
	if path == ":memory:" {
		return path, nil
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("获取用户目录: %w", err)
		}
		if path == "~" {
			path = home
		} else {
			path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("解析数据库路径: %w", err)
	}
	return filepath.Clean(abs), nil
}

// ValidateDatabaseOutsideProject 拒绝数据库文件位于被观察项目内部。
// SQLite 只会写同名前缀的 DB/WAL/SHM 文件，因此项目位于数据库父目录下并不越界；
// 路径仍会解析现有的符号链接前缀，例如 macOS 的 /var -> /private/var。
func ValidateDatabaseOutsideProject(databasePath, projectRoot string) error {
	if strings.TrimSpace(projectRoot) == "" {
		return errors.New("项目根目录不能为空")
	}
	databasePath, err := expandPath(databasePath)
	if err != nil {
		return err
	}
	if databasePath == ":memory:" {
		return nil
	}
	canonicalDatabase, err := canonicalPath(databasePath)
	if err != nil {
		return fmt.Errorf("解析数据库路径: %w", err)
	}
	canonicalProject, err := canonicalPath(projectRoot)
	if err != nil {
		return fmt.Errorf("解析项目根目录: %w", err)
	}
	if pathWithin(canonicalDatabase, canonicalProject) {
		return fmt.Errorf("%w: database=%s project=%s", ErrDatabaseProjectOverlap, canonicalDatabase, canonicalProject)
	}
	return nil
}

// ValidateStoredProjectLocations 在任何读写打开前，以 SQLite 只读模式检查数据库中
// 已持久化的项目根。这样即使数据库文件后来被移动进项目，也不会先创建 WAL/执行迁移。
func ValidateStoredProjectLocations(databasePath string) error {
	resolved, err := expandPath(databasePath)
	if err != nil || resolved == ":memory:" {
		return err
	}
	registry, _, err := readProjectRegistry(resolved)
	if err != nil {
		return err
	}
	registryRoots := make(map[string]string, len(registry.Projects))
	for _, project := range registry.Projects {
		if err := ValidateDatabaseOutsideProject(resolved, project.Root); err != nil {
			return err
		}
		registryRoots[project.ID] = project.Root
	}
	if _, err := os.Stat(resolved); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	// immutable 让 SQLite 不尝试创建 WAL/SHM；CLI 在启动自己的写连接前执行此预检。
	query := url.Values{"mode": []string{"ro"}, "immutable": []string{"1"}}
	databaseURL := &url.URL{Scheme: "file", Path: resolved, RawQuery: query.Encode()}
	db, err := sql.Open("sqlite", databaseURL.String())
	if err != nil {
		return fmt.Errorf("只读打开 SQLite: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	var projectsTable int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'projects'`).Scan(&projectsTable); err != nil {
		return fmt.Errorf("检查项目注册表: %w", err)
	}
	if projectsTable == 0 {
		return nil
	}
	rows, err := db.Query(`SELECT id, root FROM projects`)
	if err != nil {
		return fmt.Errorf("只读查询持久化项目: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, root string
		if err := rows.Scan(&id, &root); err != nil {
			return err
		}
		if err := ValidateDatabaseOutsideProject(resolved, root); err != nil {
			return err
		}
		if registeredRoot, exists := registryRoots[id]; exists && !sameProjectRoot(root, registeredRoot) {
			return fmt.Errorf("%w: id=%s existing=%s requested=%s", ErrProjectRootConflict, id, root, registeredRoot)
		}
	}
	return rows.Err()
}

// canonicalPath 会解析最长的现有路径前缀，再拼回尚未创建的尾部。
func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	candidate := filepath.Clean(abs)
	missing := make([]string, 0)
	for {
		resolved, resolveErr := filepath.EvalSymlinks(candidate)
		if resolveErr == nil {
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(resolveErr, os.ErrNotExist) {
			return "", resolveErr
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return filepath.Clean(abs), nil
		}
		missing = append(missing, filepath.Base(candidate))
		candidate = parent
	}
}

func pathWithin(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func (s *Store) migrate(ctx context.Context) error {
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	if _, err := s.db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("初始化数据库: %w", err)
	}
	// CREATE TABLE IF NOT EXISTS 不会为旧表补列，保留轻量迁移以兼容
	// 已经由早期版本生成的可重建缓存。
	for _, column := range []struct {
		table      string
		name       string
		definition string
	}{
		{table: "projects", name: "generation", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "tasks", name: "index_hash", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "task_context_entries", name: "entry_type", definition: "TEXT NOT NULL DEFAULT 'file'"},
		{table: "task_context_entries", name: "is_duplicate", definition: "INTEGER NOT NULL DEFAULT 0"},
		{table: "runtime_sessions", name: "source_hash", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "git_snapshots", name: "summary_json", definition: "TEXT NOT NULL DEFAULT '{}'"},
	} {
		if err := ensureTableColumn(ctx, s.db, column.table, column.name, column.definition); err != nil {
			return fmt.Errorf("迁移 %s.%s: %w", column.table, column.name, err)
		}
	}
	// 旧缓存补列时只能使用常量 DEFAULT；随后为既有项目分配稳定实例标识。
	if _, err := s.db.ExecContext(ctx, `
		UPDATE projects
		SET generation = lower(hex(randomblob(16)))
		WHERE generation = ''`); err != nil {
		return fmt.Errorf("初始化项目 generation: %w", err)
	}
	return nil
}

func ensureTableColumn(ctx context.Context, db *sql.DB, table, column, definition string) error {
	exists, err := tableHasColumn(ctx, db, table, column)
	if err != nil || exists {
		return err
	}
	// 表名、列名和定义只来自上面的固定迁移清单，不接受外部输入。
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, column, definition)); err != nil {
		// 多进程同时首次启动时，另一进程可能已抢先补列；复查后即可安全接受。
		if existsAfter, checkErr := tableHasColumn(ctx, db, table, column); checkErr == nil && existsAfter {
			return nil
		}
		return err
	}
	return nil
}

func tableHasColumn(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&id, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (s *Store) UpsertProject(ctx context.Context, project model.Project) error {
	unlockProject := s.lockProject(project.ID)
	defer unlockProject()
	// 注册表 sidecar 是整份原子替换，因此注册增删仍需全局串行；普通项目快照不受该锁影响。
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	// 项目注册跨 SQLite 与 sidecar；一旦进入临界区便不再服从客户端取消，
	// 避免 sidecar 已替换而 transaction 因请求断开自动回滚。
	mutationCtx := context.WithoutCancel(ctx)
	now := time.Now().UTC()
	if project.CreatedAt.IsZero() {
		project.CreatedAt = now
	}
	project.UpdatedAt = now
	if project.Mode == "" {
		project.Mode = model.ProjectModeObserver
	}
	if project.Mode != model.ProjectModeObserver {
		return fmt.Errorf("%w: 首版仅允许 observer 模式，实际为 %s", ErrProjectRegistryInvalid, project.Mode)
	}

	tx, err := s.db.BeginTx(mutationCtx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var previousRegistry []byte
	if !s.memory {
		previousRegistry, err = encodeProjectRegistry(mutationCtx, tx)
		if err != nil {
			return err
		}
	}
	var existingRoot string
	if err := tx.QueryRowContext(mutationCtx, `SELECT root FROM projects WHERE id = ?`, project.ID).Scan(&existingRoot); err == nil {
		left, leftErr := canonicalPath(existingRoot)
		right, rightErr := canonicalPath(project.Root)
		if leftErr != nil || rightErr != nil {
			left, right = filepath.Clean(existingRoot), filepath.Clean(project.Root)
		}
		if left != right {
			return fmt.Errorf("%w: id=%s existing=%s requested=%s", ErrProjectRootConflict, project.ID, existingRoot, project.Root)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	_, err = tx.ExecContext(mutationCtx, `
		INSERT INTO projects (id, name, root, mode, generation, created_at, updated_at)
		VALUES (?, ?, ?, ?, lower(hex(randomblob(16))), ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			root = excluded.root,
			mode = excluded.mode,
			updated_at = excluded.updated_at`,
		project.ID, project.Name, project.Root, project.Mode,
		formatTime(project.CreatedAt), formatTime(project.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("保存项目: %w", err)
	}
	for _, resource := range allResourceTypes() {
		if _, err := tx.ExecContext(mutationCtx, `
			INSERT OR IGNORE INTO resource_revisions
				(project_id, resource_type, revision, updated_at)
			VALUES (?, ?, 0, ?)`, project.ID, resource, formatTime(now)); err != nil {
			return fmt.Errorf("初始化资源版本: %w", err)
		}
	}
	var registryPayload []byte
	if !s.memory {
		registryPayload, err = encodeProjectRegistry(mutationCtx, tx)
		if err != nil {
			return err
		}
	}
	if err := s.commitProjectMutation(tx, previousRegistry, registryPayload); err != nil {
		return fmt.Errorf("提交项目注册: %w", err)
	}
	return nil
}

func (s *Store) DeleteProject(ctx context.Context, projectID string) error {
	unlockProject := s.lockProject(projectID)
	defer unlockProject()
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	mutationCtx := context.WithoutCancel(ctx)
	tx, err := s.db.BeginTx(mutationCtx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var previousRegistry []byte
	if !s.memory {
		previousRegistry, err = encodeProjectRegistry(mutationCtx, tx)
		if err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(mutationCtx, `DELETE FROM projects WHERE id = ?`, projectID)
	if err != nil {
		return fmt.Errorf("删除项目: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	var registryPayload []byte
	if !s.memory {
		registryPayload, err = encodeProjectRegistry(mutationCtx, tx)
		if err != nil {
			return err
		}
	}
	if err := s.commitProjectMutation(tx, previousRegistry, registryPayload); err != nil {
		return fmt.Errorf("提交项目删除: %w", err)
	}
	return nil
}

func (s *Store) GetProject(ctx context.Context, projectID string) (model.Project, error) {
	row := s.db.QueryRowContext(ctx, projectSelect+` WHERE p.id = ? GROUP BY p.id`, projectID)
	project, err := scanProject(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Project{}, ErrNotFound
	}
	return project, err
}

func (s *Store) GetProjectByRoot(ctx context.Context, root string) (model.Project, error) {
	row := s.db.QueryRowContext(ctx, projectSelect+` WHERE p.root = ? GROUP BY p.id`, root)
	project, err := scanProject(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Project{}, ErrNotFound
	}
	return project, err
}

func (s *Store) ListProjects(ctx context.Context) ([]model.Project, error) {
	rows, err := s.db.QueryContext(ctx, projectSelect+` GROUP BY p.id ORDER BY lower(p.name), p.id`)
	if err != nil {
		return nil, fmt.Errorf("查询项目: %w", err)
	}
	defer rows.Close()

	projects := make([]model.Project, 0)
	for rows.Next() {
		project, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	return projects, rows.Err()
}

func (s *Store) SetIndexError(ctx context.Context, projectID string, indexErr error) error {
	unlockProject := s.lockProject(projectID)
	defer unlockProject()
	message := ""
	if indexErr != nil {
		message = indexErr.Error()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current string
	if err := tx.QueryRowContext(ctx, `SELECT index_error FROM projects WHERE id = ?`, projectID).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if current == message {
		return nil
	}
	nowValue := time.Now().UTC()
	_, err = tx.ExecContext(ctx, `
		UPDATE projects SET index_error = ?, updated_at = ? WHERE id = ?`,
		message, formatTime(nowValue), projectID)
	if err != nil {
		return err
	}
	if indexErr != nil {
		now := formatTime(nowValue)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO index_errors(project_id, source_path, message, created_at)
			VALUES (?, '', ?, ?)`, projectID, message, now); err != nil {
			return err
		}
		// 每个项目只保留最近 100 条诊断，防止反复失败无限增长。
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM index_errors
			WHERE project_id = ? AND id NOT IN (
				SELECT id FROM index_errors WHERE project_id = ? ORDER BY id DESC LIMIT 100
			)`, projectID, projectID); err != nil {
			return err
		}
	}
	eventType := "index.recovered"
	if message != "" {
		eventType = "index.failed"
	}
	payload, _ := json.Marshal(map[string]string{"message": message})
	if err := insertActivity(ctx, tx, projectID, "", eventType, "indexer", payload, nowValue); err != nil {
		return err
	}
	if err := pruneActivity(ctx, tx, projectID); err != nil {
		return err
	}
	if err := incrementRevision(ctx, tx, projectID, model.ResourceActivity, nowValue); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) GetRevisions(ctx context.Context, projectID string) (model.RevisionBundle, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT (SELECT value FROM cache_metadata WHERE key = 'generation') || '-' || p.generation,
		       rr.resource_type, rr.revision, rr.updated_at
		FROM projects p
		LEFT JOIN resource_revisions rr ON rr.project_id = p.id
		WHERE p.id = ?`, projectID)
	if err != nil {
		return model.RevisionBundle{}, err
	}
	defer rows.Close()

	bundle := model.RevisionBundle{Day: time.Now().Format("2006-01-02")}
	found := false
	for rows.Next() {
		found = true
		var generation string
		var resource, updated sql.NullString
		var revision sql.NullInt64
		if err := rows.Scan(&generation, &resource, &revision, &updated); err != nil {
			return bundle, err
		}
		bundle.Generation = generation
		if resource.Valid && revision.Valid {
			setRevision(&bundle, model.ResourceType(resource.String), revision.Int64)
		}
		if updated.Valid {
			if parsed, err := parseTime(updated.String); err == nil && parsed.After(bundle.Updated) {
				bundle.Updated = parsed
			}
		}
	}
	if !found {
		return bundle, ErrNotFound
	}
	return bundle, rows.Err()
}

const projectSelect = `
	SELECT p.id, p.name, p.root, p.mode, p.created_at, p.updated_at,
	       p.indexed_at, p.index_error,
	       COALESCE((SELECT COUNT(*) FROM tasks t WHERE t.project_id = p.id AND t.archived = 0), 0),
	       (SELECT value FROM cache_metadata WHERE key = 'generation') || '-' || p.generation,
	       COALESCE(MAX(CASE WHEN rr.resource_type = 'tasks' THEN rr.revision END), 0),
	       COALESCE(MAX(CASE WHEN rr.resource_type = 'sessions' THEN rr.revision END), 0),
	       COALESCE(MAX(CASE WHEN rr.resource_type = 'git' THEN rr.revision END), 0),
	       COALESCE(MAX(CASE WHEN rr.resource_type = 'activity' THEN rr.revision END), 0),
	       COALESCE(MAX(CASE WHEN rr.resource_type = 'specs' THEN rr.revision END), 0),
	       COALESCE(MAX(CASE WHEN rr.resource_type = 'agents' THEN rr.revision END), 0),
	       COALESCE(MAX(rr.updated_at), p.updated_at)
	FROM projects p
	LEFT JOIN resource_revisions rr ON rr.project_id = p.id`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanProject(scanner rowScanner) (model.Project, error) {
	var p model.Project
	var mode string
	var created, updated string
	var indexed sql.NullString
	var revisionsUpdated string
	if err := scanner.Scan(
		&p.ID, &p.Name, &p.Root, &mode, &created, &updated,
		&indexed, &p.IndexError, &p.ActiveTaskCount, &p.Revisions.Generation,
		&p.Revisions.Tasks, &p.Revisions.Sessions, &p.Revisions.Git,
		&p.Revisions.Activity, &p.Revisions.Specs, &p.Revisions.Agents,
		&revisionsUpdated,
	); err != nil {
		return p, err
	}
	p.Mode = model.ProjectMode(mode)
	p.CreatedAt, _ = parseTime(created)
	p.UpdatedAt, _ = parseTime(updated)
	p.Revisions.Day = time.Now().Format("2006-01-02")
	p.Revisions.Updated, _ = parseTime(revisionsUpdated)
	if indexed.Valid {
		value, err := parseTime(indexed.String)
		if err == nil {
			p.IndexedAt = &value
		}
	}
	return p, nil
}

func allResourceTypes() []model.ResourceType {
	return []model.ResourceType{
		model.ResourceTasks,
		model.ResourceSessions,
		model.ResourceGit,
		model.ResourceActivity,
		model.ResourceSpecs,
		model.ResourceAgents,
	}
}

func setRevision(bundle *model.RevisionBundle, resource model.ResourceType, value int64) {
	switch resource {
	case model.ResourceTasks:
		bundle.Tasks = value
	case model.ResourceSessions:
		bundle.Sessions = value
	case model.ResourceGit:
		bundle.Git = value
	case model.ResourceActivity:
		bundle.Activity = value
	case model.ResourceSpecs:
		bundle.Specs = value
	case model.ResourceAgents:
		bundle.Agents = value
	}
}

func formatTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func parseTime(value string) (time.Time, error) { return time.Parse(time.RFC3339Nano, value) }
