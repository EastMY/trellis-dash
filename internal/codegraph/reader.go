package codegraph

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

var (
	ErrNotInitialized     = errors.New("CodeGraph 尚未初始化")
	ErrInvalidDatabase    = errors.New("CodeGraph 数据库无效")
	ErrIncompatibleSchema = errors.New("CodeGraph 数据库版本不兼容")
	ErrBusy               = errors.New("CodeGraph 数据库正忙")
	ErrNotFound           = errors.New("CodeGraph 资源不存在")
	ErrLimit              = errors.New("CodeGraph 查询超过限制")
)

var keySymbolKinds = []string{
	"class", "interface", "enum", "struct", "function", "method", "component", "route", "type_alias",
}

var keySymbolKindSet = func() map[string]struct{} {
	result := make(map[string]struct{}, len(keySymbolKinds))
	for _, kind := range keySymbolKinds {
		result[kind] = struct{}{}
	}
	return result
}()

var requiredColumns = map[string][]string{
	"files": {"path", "language", "size", "indexed_at", "node_count"},
	"nodes": {"id", "kind", "name", "qualified_name", "file_path", "language", "start_line", "end_line", "signature"},
	"edges": {"id", "source", "target", "kind", "line", "provenance"},
}

// 路由是 CodeGraph 生成的入口元数据节点，通过 references 指向真实处理方法。
// 只接纳来源为 route 的引用，避免把普通符号引用混入调用链。
const relationEdgePredicate = `(e.kind = 'calls' OR (e.kind = 'references' AND source_node.kind = 'route'))`

// Reader 按请求短连接读取外部 CodeGraph 索引，只缓存已经验证过的磁盘 schema 身份。
type Reader struct {
	schemaMu       sync.Mutex
	schemaCache    map[string]schemaCacheEntry
	validateSchema func(context.Context, *sql.DB) error
}

type schemaCacheEntry struct {
	identity string
	dbInfo   os.FileInfo
}

func NewReader() *Reader {
	return &Reader{
		schemaCache:    make(map[string]schemaCacheEntry),
		validateSchema: validateSchema,
	}
}

func databasePath(projectRoot string) string {
	return filepath.Join(projectRoot, ".codegraph", "codegraph.db")
}

// Fingerprint 只读取 DB/WAL 元数据，用作现有 revision 轮询的不透明变化令牌。
// 不纳入 SHM：它只是 wal-index 的读侧协调状态，只读查询也可能刷新其 mtime
// （macOS 多进程持有索引时已实测），而真实写入必然改变 WAL 追加或 DB 回写。
func (r *Reader) Fingerprint(projectRoot string) string {
	base := databasePath(projectRoot)
	hash := sha256.New()
	for _, suffix := range []string{"", "-wal"} {
		name := base + suffix
		info, err := os.Stat(name)
		switch {
		case err == nil:
			_, _ = fmt.Fprintf(hash, "%s|present|%d|%d|%s\n", suffix, info.Size(), info.ModTime().UnixNano(), info.Mode().String())
		case errors.Is(err, os.ErrNotExist):
			_, _ = fmt.Fprintf(hash, "%s|missing\n", suffix)
		default:
			_, _ = fmt.Fprintf(hash, "%s|error|%T\n", suffix, err)
		}
	}
	return fmt.Sprintf("sha256:%x", hash.Sum(nil)[:12])
}

func (r *Reader) Status(ctx context.Context, projectRoot string) (Status, error) {
	revision := r.Fingerprint(projectRoot)
	db, info, err := r.open(ctx, projectRoot)
	if err != nil {
		return Status{Revision: revision}, err
	}
	defer db.Close()

	status := Status{Available: true, Revision: revision, DatabaseBytes: info.Size()}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM files`).Scan(&status.FileCount); err != nil {
		return Status{Revision: revision}, classifyDatabaseError("统计 CodeGraph 文件", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes`).Scan(&status.NodeCount); err != nil {
		return Status{Revision: revision}, classifyDatabaseError("统计 CodeGraph 节点", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM edges`).Scan(&status.EdgeCount); err != nil {
		return Status{Revision: revision}, classifyDatabaseError("统计 CodeGraph 关系", err)
	}

	var indexedAt sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT MAX(indexed_at) FROM files`).Scan(&indexedAt); err != nil {
		return Status{Revision: revision}, classifyDatabaseError("读取 CodeGraph 索引时间", err)
	}
	if indexedAt.Valid && indexedAt.Int64 > 0 {
		value := time.UnixMilli(indexedAt.Int64).UTC()
		status.IndexedAt = &value
	}

	languages, err := db.QueryContext(ctx, `SELECT language, COUNT(*) FROM files GROUP BY language ORDER BY COUNT(*) DESC, language`)
	if err != nil {
		return Status{Revision: revision}, classifyDatabaseError("读取 CodeGraph 语言统计", err)
	}
	for languages.Next() {
		var item LanguageStat
		if err := languages.Scan(&item.Name, &item.FileCount); err != nil {
			languages.Close()
			return Status{Revision: revision}, classifyDatabaseError("解析 CodeGraph 语言统计", err)
		}
		status.Languages = append(status.Languages, item)
	}
	if err := languages.Close(); err != nil {
		return Status{Revision: revision}, classifyDatabaseError("关闭 CodeGraph 语言统计", err)
	}

	if has, err := tableExists(ctx, db, "schema_versions"); err != nil {
		return Status{Revision: revision}, err
	} else if has {
		rows, err := db.QueryContext(ctx, `SELECT version FROM schema_versions ORDER BY version`)
		if err != nil {
			return Status{Revision: revision}, classifyDatabaseError("读取 CodeGraph schema 版本", err)
		}
		for rows.Next() {
			var version int
			if err := rows.Scan(&version); err != nil {
				rows.Close()
				return Status{Revision: revision}, classifyDatabaseError("解析 CodeGraph schema 版本", err)
			}
			status.SchemaVersions = append(status.SchemaVersions, version)
		}
		if err := rows.Close(); err != nil {
			return Status{Revision: revision}, classifyDatabaseError("关闭 CodeGraph schema 版本", err)
		}
	}
	return status, nil
}

func (r *Reader) Structure(ctx context.Context, projectRoot, indexedPath string, limit, offset int) (Page[StructureEntry], error) {
	limit, offset, err := normalizePage(limit, offset, DefaultStructureLimit, MaxStructureLimit)
	if err != nil {
		return Page[StructureEntry]{}, err
	}
	cleanPath, err := normalizeIndexedPath(indexedPath)
	if err != nil {
		return Page[StructureEntry]{}, err
	}
	db, _, err := r.open(ctx, projectRoot)
	if err != nil {
		return Page[StructureEntry]{}, err
	}
	defer db.Close()

	if cleanPath != "" {
		var exists int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM files WHERE path = ?`, cleanPath).Scan(&exists); err != nil {
			return Page[StructureEntry]{}, classifyDatabaseError("检查 CodeGraph 文件", err)
		}
		if exists > 0 {
			return listFileSymbols(ctx, db, cleanPath, limit, offset)
		}
	}
	return listDirectory(ctx, db, cleanPath, limit, offset)
}

func (r *Reader) Search(ctx context.Context, projectRoot, query, kind string, limit, offset int) (Page[Symbol], error) {
	limit, offset, err := normalizePage(limit, offset, DefaultSearchLimit, MaxSearchLimit)
	if err != nil {
		return Page[Symbol]{}, err
	}
	query = strings.TrimSpace(query)
	if query == "" || len([]rune(query)) > MaxQueryLength {
		return Page[Symbol]{}, fmt.Errorf("%w: 搜索关键字长度必须为 1～%d", ErrLimit, MaxQueryLength)
	}
	if kind != "" {
		if _, ok := keySymbolKindSet[kind]; !ok {
			return Page[Symbol]{}, fmt.Errorf("%w: 不支持的符号类型 %s", ErrLimit, kind)
		}
	}
	db, _, err := r.open(ctx, projectRoot)
	if err != nil {
		return Page[Symbol]{}, err
	}
	defer db.Close()

	kinds := strings.Repeat("?,", len(keySymbolKinds))
	kinds = strings.TrimSuffix(kinds, ",")
	where := fmt.Sprintf(`kind IN (%s) AND (instr(lower(name), lower(?)) > 0 OR instr(lower(qualified_name), lower(?)) > 0)`, kinds)
	args := make([]any, 0, len(keySymbolKinds)+3)
	for _, value := range keySymbolKinds {
		args = append(args, value)
	}
	args = append(args, query, query)
	if kind != "" {
		where += ` AND kind = ?`
		args = append(args, kind)
	}

	selectArgs := append(append([]any{}, args...), query, query, query, limit, offset)
	rows, err := db.QueryContext(ctx, `
		SELECT id, kind, name, qualified_name, file_path, language, start_line, end_line,
		       COALESCE(signature, ''), COUNT(*) OVER()
		FROM nodes WHERE `+where+`
		ORDER BY CASE
			WHEN lower(name) = lower(?) THEN 0
			WHEN lower(name) LIKE lower(?) || '%' THEN 1
			WHEN instr(lower(name), lower(?)) > 0 THEN 2
			ELSE 3 END,
			file_path, start_line, name, id
		LIMIT ? OFFSET ?`, selectArgs...)
	if err != nil {
		return Page[Symbol]{}, classifyDatabaseError("搜索 CodeGraph 符号", err)
	}
	defer rows.Close()
	items := make([]Symbol, 0, limit)
	total := 0
	for rows.Next() {
		var item Symbol
		var rowTotal int
		if err := rows.Scan(
			&item.ID, &item.Kind, &item.Name, &item.QualifiedName, &item.FilePath,
			&item.Language, &item.StartLine, &item.EndLine, &item.Signature, &rowTotal,
		); err != nil {
			return Page[Symbol]{}, classifyDatabaseError("解析 CodeGraph 搜索结果", err)
		}
		if len(items) == 0 {
			total = rowTotal
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return Page[Symbol]{}, classifyDatabaseError("遍历 CodeGraph 搜索结果", err)
	}
	if len(items) == 0 {
		// 窗口函数在空结果或 offset 越界时没有行可携带 total，仅此时回退一次 count。
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes WHERE `+where, args...).Scan(&total); err != nil {
			return Page[Symbol]{}, classifyDatabaseError("统计 CodeGraph 搜索结果", err)
		}
	}
	return Page[Symbol]{Items: items, Total: total, Limit: limit, Offset: offset, HasMore: offset+len(items) < total}, nil
}

func (r *Reader) Relations(ctx context.Context, projectRoot, symbolID string, direction Direction, limit, offset int) (RelationPage, error) {
	limit, offset, err := normalizePage(limit, offset, DefaultRelationLimit, MaxRelationLimit)
	if err != nil {
		return RelationPage{}, err
	}
	if strings.TrimSpace(symbolID) == "" || len(symbolID) > 512 {
		return RelationPage{}, fmt.Errorf("%w: 符号 ID 无效", ErrLimit)
	}
	if direction != DirectionCallers && direction != DirectionCallees {
		return RelationPage{}, fmt.Errorf("%w: 调用方向必须为 callers 或 callees", ErrLimit)
	}
	db, _, err := r.open(ctx, projectRoot)
	if err != nil {
		return RelationPage{}, err
	}
	defer db.Close()

	root, err := getSymbol(ctx, db, symbolID)
	if err != nil {
		return RelationPage{}, err
	}
	column := "e.target"
	other := "source_node"
	if direction == DirectionCallees {
		column = "e.source"
		other = "target_node"
	}
	var total int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM edges e
		JOIN nodes source_node ON source_node.id = e.source
		WHERE `+column+` = ? AND `+relationEdgePredicate, symbolID).Scan(&total); err != nil {
		return RelationPage{}, classifyDatabaseError("统计 CodeGraph 调用关系", err)
	}
	rows, err := db.QueryContext(ctx, `
		SELECT e.id, e.kind, COALESCE(e.line, 0), COALESCE(e.provenance, ''),
		       source_node.id, source_node.kind, source_node.name, source_node.qualified_name,
		       source_node.file_path, source_node.language, source_node.start_line, source_node.end_line, COALESCE(source_node.signature, ''),
		       target_node.id, target_node.kind, target_node.name, target_node.qualified_name,
		       target_node.file_path, target_node.language, target_node.start_line, target_node.end_line, COALESCE(target_node.signature, '')
		FROM edges e
		JOIN nodes source_node ON source_node.id = e.source
		JOIN nodes target_node ON target_node.id = e.target
		WHERE `+column+` = ? AND `+relationEdgePredicate+`
		ORDER BY `+other+`.file_path, `+other+`.start_line, `+other+`.id, e.id
		LIMIT ? OFFSET ?`, symbolID, limit, offset)
	if err != nil {
		return RelationPage{}, classifyDatabaseError("读取 CodeGraph 调用关系", err)
	}
	defer rows.Close()
	items := make([]Relation, 0, limit)
	for rows.Next() {
		var item Relation
		item.Direction = string(direction)
		if err := rows.Scan(
			&item.ID, &item.Kind, &item.Line, &item.Provenance,
			&item.Source.ID, &item.Source.Kind, &item.Source.Name, &item.Source.QualifiedName,
			&item.Source.FilePath, &item.Source.Language, &item.Source.StartLine, &item.Source.EndLine, &item.Source.Signature,
			&item.Target.ID, &item.Target.Kind, &item.Target.Name, &item.Target.QualifiedName,
			&item.Target.FilePath, &item.Target.Language, &item.Target.StartLine, &item.Target.EndLine, &item.Target.Signature,
		); err != nil {
			return RelationPage{}, classifyDatabaseError("解析 CodeGraph 调用关系", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return RelationPage{}, classifyDatabaseError("遍历 CodeGraph 调用关系", err)
	}
	return RelationPage{Symbol: root, Direction: direction, Items: items, Total: total, Limit: limit, Offset: offset, HasMore: offset+len(items) < total}, nil
}

func (r *Reader) open(ctx context.Context, projectRoot string) (*sql.DB, os.FileInfo, error) {
	path := databasePath(projectRoot)
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("%w: %s", ErrNotInitialized, filepath.Join(".codegraph", "codegraph.db"))
	}
	if err != nil {
		return nil, nil, fmt.Errorf("%w: 读取 CodeGraph 数据库: %v", ErrInvalidDatabase, err)
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%w: CodeGraph 数据库不是普通文件", ErrInvalidDatabase)
	}
	query := url.Values{}
	query.Set("mode", "ro")
	query.Add("_pragma", "query_only(1)")
	query.Add("_pragma", "busy_timeout(1500)")
	walInfo, walErr := os.Stat(path + "-wal")
	shmInfo, shmErr := os.Stat(path + "-shm")
	if shmErr != nil && !errors.Is(shmErr, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("%w: 读取 CodeGraph SHM: %v", ErrInvalidDatabase, shmErr)
	}
	if errors.Is(walErr, os.ErrNotExist) || walErr == nil && walInfo.Size() == 0 {
		// 已完全 checkpoint 的索引可以安全 immutable 读取，避免 SQLite 为只读访问创建 WAL/SHM。
		query.Set("immutable", "1")
	} else if walErr != nil {
		return nil, nil, fmt.Errorf("%w: 读取 CodeGraph WAL: %v", ErrInvalidDatabase, walErr)
	} else if errors.Is(shmErr, os.ErrNotExist) {
		// 非空 WAL 需要 SHM 才能得到一致快照；Observer 不代替 CodeGraph 创建它。
		return nil, nil, fmt.Errorf("%w: CodeGraph WAL 存在但共享内存尚未就绪", ErrBusy)
	}
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: query.Encode()}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, nil, classifyDatabaseError("打开 CodeGraph 数据库", err)
	}
	// 短连接避免外部索引原子替换后长期读取旧 inode，也限制单请求占用的 FD。
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, nil, classifyDatabaseError("连接 CodeGraph 数据库", err)
	}
	identity := schemaIdentity(info, walInfo, shmInfo)
	if err := r.ensureSchema(ctx, db, path, identity, info); err != nil {
		db.Close()
		return nil, nil, err
	}
	return db, info, nil
}

func schemaIdentity(info, walInfo, shmInfo os.FileInfo) string {
	fileIdentity := func(value os.FileInfo) string {
		if value == nil {
			return "missing"
		}
		return fmt.Sprintf("%d:%d:%s", value.Size(), value.ModTime().UnixNano(), value.Mode().String())
	}
	return fileIdentity(info) + "|wal=" + fileIdentity(walInfo) + "|shm=" + fileIdentity(shmInfo)
}

// ensureSchema 只缓存成功验证；索引文件或 WAL/SHM 任一身份变化都会重新探测外部 schema。
func (r *Reader) ensureSchema(ctx context.Context, db *sql.DB, path, identity string, info os.FileInfo) error {
	r.schemaMu.Lock()
	defer r.schemaMu.Unlock()
	if cached, ok := r.schemaCache[path]; ok && cached.identity == identity && os.SameFile(cached.dbInfo, info) {
		return nil
	}
	validator := r.validateSchema
	if validator == nil {
		validator = validateSchema
	}
	if err := validator(ctx, db); err != nil {
		return err
	}
	if r.schemaCache == nil {
		r.schemaCache = make(map[string]schemaCacheEntry)
	}
	r.schemaCache[path] = schemaCacheEntry{identity: identity, dbInfo: info}
	return nil
}

func validateSchema(ctx context.Context, db *sql.DB) error {
	for table, columns := range requiredColumns {
		exists, err := tableExists(ctx, db, table)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("%w: 缺少表 %s", ErrIncompatibleSchema, table)
		}
		rows, err := db.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s)`, table))
		if err != nil {
			return classifyDatabaseError("读取 CodeGraph schema", err)
		}
		found := make(map[string]struct{})
		for rows.Next() {
			var id, notNull, primaryKey int
			var name, columnType string
			var defaultValue sql.NullString
			if err := rows.Scan(&id, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
				rows.Close()
				return classifyDatabaseError("解析 CodeGraph schema", err)
			}
			found[name] = struct{}{}
		}
		if err := rows.Close(); err != nil {
			return classifyDatabaseError("关闭 CodeGraph schema", err)
		}
		for _, column := range columns {
			if _, ok := found[column]; !ok {
				return fmt.Errorf("%w: 表 %s 缺少列 %s", ErrIncompatibleSchema, table, column)
			}
		}
	}
	return nil
}

func tableExists(ctx context.Context, db *sql.DB, table string) (bool, error) {
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
		return false, classifyDatabaseError("检查 CodeGraph schema", err)
	}
	return count > 0, nil
}

func listDirectory(ctx context.Context, db *sql.DB, indexedPath string, limit, offset int) (Page[StructureEntry], error) {
	prefix := ""
	if indexedPath != "" {
		prefix = indexedPath + "/"
	}
	rows, err := db.QueryContext(ctx, `
		SELECT path, language, node_count, size
		FROM files
		WHERE path LIKE ? ESCAPE '\'
		ORDER BY path`, escapeLike(prefix)+"%")
	if err != nil {
		return Page[StructureEntry]{}, classifyDatabaseError("读取 CodeGraph 目录", err)
	}
	defer rows.Close()

	entries := make(map[string]StructureEntry)
	matched := false
	for rows.Next() {
		var filePath, language string
		var nodeCount int
		var size int64
		if err := rows.Scan(&filePath, &language, &nodeCount, &size); err != nil {
			return Page[StructureEntry]{}, classifyDatabaseError("解析 CodeGraph 目录", err)
		}
		rest := strings.TrimPrefix(filePath, prefix)
		if rest == "" || rest == filePath && prefix != "" {
			continue
		}
		matched = true
		if split := strings.IndexByte(rest, '/'); split >= 0 {
			name := rest[:split]
			path := strings.TrimPrefix(prefix+name, "/")
			entry := entries["dir:"+path]
			entry.ID, entry.Type, entry.Name, entry.Path = "dir:"+path, "directory", name, path
			entry.Expandable = true
			entry.FileCount++
			entry.NodeCount += nodeCount
			entries[entry.ID] = entry
			continue
		}
		entries["file:"+filePath] = StructureEntry{
			ID: "file:" + filePath, Type: "file", Name: rest, Path: filePath,
			Language: language, NodeCount: nodeCount, Size: size, Expandable: nodeCount > 0,
		}
	}
	if err := rows.Err(); err != nil {
		return Page[StructureEntry]{}, classifyDatabaseError("遍历 CodeGraph 目录", err)
	}
	if !matched && indexedPath != "" {
		return Page[StructureEntry]{}, fmt.Errorf("%w: 目录 %s", ErrNotFound, indexedPath)
	}
	items := make([]StructureEntry, 0, len(entries))
	for _, entry := range entries {
		items = append(items, entry)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Type != items[j].Type {
			return items[i].Type == "directory"
		}
		if items[i].Name != items[j].Name {
			return items[i].Name < items[j].Name
		}
		return items[i].Path < items[j].Path
	})
	return paginate(items, limit, offset), nil
}

func listFileSymbols(ctx context.Context, db *sql.DB, filePath string, limit, offset int) (Page[StructureEntry], error) {
	kinds := strings.TrimSuffix(strings.Repeat("?,", len(keySymbolKinds)), ",")
	args := make([]any, 0, len(keySymbolKinds)+3)
	args = append(args, filePath)
	for _, kind := range keySymbolKinds {
		args = append(args, kind)
	}
	var total int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes WHERE file_path = ? AND kind IN (`+kinds+`)`, args...).Scan(&total); err != nil {
		return Page[StructureEntry]{}, classifyDatabaseError("统计 CodeGraph 文件符号", err)
	}
	args = append(args, limit, offset)
	rows, err := db.QueryContext(ctx, `
		SELECT id, kind, name, qualified_name, file_path, language, start_line, end_line, COALESCE(signature, '')
		FROM nodes WHERE file_path = ? AND kind IN (`+kinds+`)
		ORDER BY start_line, name, id LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return Page[StructureEntry]{}, classifyDatabaseError("读取 CodeGraph 文件符号", err)
	}
	defer rows.Close()
	symbols, err := scanSymbols(rows)
	if err != nil {
		return Page[StructureEntry]{}, err
	}
	items := make([]StructureEntry, 0, len(symbols))
	for index := range symbols {
		symbol := symbols[index]
		items = append(items, StructureEntry{
			ID: symbol.ID, Type: "symbol", Name: symbol.Name, Path: symbol.FilePath,
			Language: symbol.Language, Expandable: false, Symbol: &symbol,
		})
	}
	return Page[StructureEntry]{Items: items, Total: total, Limit: limit, Offset: offset, HasMore: offset+len(items) < total}, nil
}

func getSymbol(ctx context.Context, db *sql.DB, symbolID string) (Symbol, error) {
	var symbol Symbol
	err := db.QueryRowContext(ctx, `
		SELECT id, kind, name, qualified_name, file_path, language, start_line, end_line, COALESCE(signature, '')
		FROM nodes WHERE id = ?`, symbolID).Scan(
		&symbol.ID, &symbol.Kind, &symbol.Name, &symbol.QualifiedName, &symbol.FilePath,
		&symbol.Language, &symbol.StartLine, &symbol.EndLine, &symbol.Signature,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Symbol{}, fmt.Errorf("%w: 符号 %s", ErrNotFound, symbolID)
	}
	if err != nil {
		return Symbol{}, classifyDatabaseError("读取 CodeGraph 符号", err)
	}
	return symbol, nil
}

type symbolScanner interface {
	Next() bool
	Scan(...any) error
	Err() error
}

func scanSymbols(rows symbolScanner) ([]Symbol, error) {
	items := make([]Symbol, 0)
	for rows.Next() {
		var item Symbol
		if err := rows.Scan(
			&item.ID, &item.Kind, &item.Name, &item.QualifiedName, &item.FilePath,
			&item.Language, &item.StartLine, &item.EndLine, &item.Signature,
		); err != nil {
			return nil, classifyDatabaseError("解析 CodeGraph 符号", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyDatabaseError("遍历 CodeGraph 符号", err)
	}
	return items, nil
}

func normalizePage(limit, offset, defaultLimit, maxLimit int) (int, int, error) {
	if limit == 0 {
		limit = defaultLimit
	}
	if limit < 1 || limit > maxLimit || offset < 0 {
		return 0, 0, fmt.Errorf("%w: limit 必须为 1～%d 且 offset 不能为负数", ErrLimit, maxLimit)
	}
	return limit, offset, nil
}

func normalizeIndexedPath(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" {
		return "", nil
	}
	if strings.IndexByte(value, 0) >= 0 || pathpkg.IsAbs(value) {
		return "", fmt.Errorf("%w: 索引路径必须是相对路径", ErrLimit)
	}
	for _, part := range strings.Split(value, "/") {
		if part == "." || part == ".." {
			return "", fmt.Errorf("%w: 索引路径不能包含 . 或 ..", ErrLimit)
		}
	}
	clean := pathpkg.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%w: 索引路径越界", ErrLimit)
	}
	return clean, nil
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

func paginate(items []StructureEntry, limit, offset int) Page[StructureEntry] {
	total := len(items)
	if offset > total {
		offset = total
	}
	end := min(total, offset+limit)
	page := append([]StructureEntry(nil), items[offset:end]...)
	return Page[StructureEntry]{Items: page, Total: total, Limit: limit, Offset: offset, HasMore: end < total}
}

func classifyDatabaseError(action string, err error) error {
	if err == nil {
		return nil
	}
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() & 0xff {
		case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED:
			return fmt.Errorf("%w: %s: %v", ErrBusy, action, err)
		case sqlite3.SQLITE_CORRUPT, sqlite3.SQLITE_NOTADB, sqlite3.SQLITE_CANTOPEN, sqlite3.SQLITE_IOERR:
			return fmt.Errorf("%w: %s: %v", ErrInvalidDatabase, action, err)
		}
	}
	return fmt.Errorf("%s: %w", action, err)
}
