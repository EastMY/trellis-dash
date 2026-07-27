package codexusage

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var errFileBudget = errors.New("Codex 日志文件数超过限制")

type Service struct {
	mu          sync.Mutex
	codexHome   string
	cachePath   string
	location    *time.Location
	now         func() time.Time
	logger      *slog.Logger
	maxFiles    int
	maxFileSize int64
	maxLineSize int
	loaded      bool
	files       map[string]fileData
	parsedFiles int
}

type candidateFile struct {
	Path        string
	PathKey     string
	Fingerprint fingerprint
}

// CachePathForDatabase 返回与 SQLite 同生命周期的可重建缓存位置。
func CachePathForDatabase(databasePath string) string {
	if databasePath == "" || databasePath == ":memory:" {
		return ""
	}
	return databasePath + ".codex-usage.json"
}

func NewService(options Options) *Service {
	home := normalizeAbsolutePath(options.CodexHome)
	if home == "" {
		if envHome := strings.TrimSpace(os.Getenv("CODEX_HOME")); envHome != "" {
			home = normalizeAbsolutePath(envHome)
		} else if userHome, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(userHome, ".codex")
		}
	}
	location := options.Location
	if location == nil {
		location = time.Local
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	maxFiles := options.MaxFiles
	if maxFiles <= 0 {
		maxFiles = defaultMaxFiles
	}
	maxFileSize := options.MaxFileSize
	if maxFileSize <= 0 {
		maxFileSize = defaultMaxFileBytes
	}
	maxLineSize := options.MaxLineSize
	if maxLineSize <= 0 {
		maxLineSize = defaultMaxLineBytes
	}
	return &Service{
		codexHome: home, cachePath: options.CachePath, location: location, now: now,
		logger: logger, maxFiles: maxFiles, maxFileSize: maxFileSize, maxLineSize: maxLineSize,
		files: make(map[string]fileData),
	}
}

func NewServiceForDatabase(databasePath string, logger *slog.Logger) *Service {
	return NewService(Options{CachePath: CachePathForDatabase(databasePath), Logger: logger})
}

func (s *Service) Summary(ctx context.Context, query Query) (Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if query.Scope == "" {
		query.Scope = ScopeProject
	}
	if query.Days <= 0 {
		query.Days = defaultDays
	}
	skipped, err := s.refresh(ctx)
	if err != nil {
		return Summary{}, err
	}
	return s.aggregate(query, skipped), nil
}

func (s *Service) refresh(ctx context.Context) (int, error) {
	candidates, skipped, err := s.discover(ctx)
	if err != nil {
		return 0, err
	}
	ownCachePresent := false
	if !s.loaded {
		cached, present, cacheErr := readOwnCache(s.cachePath)
		ownCachePresent = present
		if cacheErr != nil {
			s.logger.Warn("Codex 统计缓存无效，将自动重建", "error", cacheErr)
		} else {
			s.files = cached
		}
		s.loaded = true
	}
	previousCount := len(s.files)
	reference := map[string]referenceFileData(nil)
	if !ownCachePresent && len(s.files) == 0 {
		reference = readReferenceCache(s.codexHome)
	}

	next := make(map[string]fileData, len(candidates))
	changed := len(s.files) != len(candidates)
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if cached, ok := s.files[candidate.PathKey]; ok && cached.Fingerprint == candidate.Fingerprint {
			next[candidate.PathKey] = cached
			continue
		}
		changed = true
		if seed, ok := reference[candidate.PathKey]; ok {
			if imported, ok := importReferenceFile(ctx, candidate, seed, s.maxLineSize); ok {
				next[candidate.PathKey] = imported
				continue
			}
		}
		parsed, parseErr := parseSessionFile(ctx, candidate.Path, candidate.PathKey, candidate.Fingerprint, s.maxLineSize)
		if parseErr != nil {
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			skipped++
			continue
		}
		s.parsedFiles++
		next[candidate.PathKey] = parsed
	}
	s.files = next
	if changed && s.cachePath != "" && (len(candidates) > 0 || previousCount > 0 || ownCachePresent) {
		if err := writeOwnCache(s.cachePath, s.files); err != nil {
			s.logger.Warn("Codex 统计缓存写入失败，本次结果仍可用", "error", err)
		}
	}
	if skipped > 0 {
		s.logger.Warn("部分 Codex 日志已跳过", "skippedFiles", skipped)
	}
	return skipped, nil
}

func (s *Service) discover(ctx context.Context) ([]candidateFile, int, error) {
	if s.codexHome == "" {
		return nil, 0, nil
	}
	result := make([]candidateFile, 0)
	skipped := 0
	for _, relativeRoot := range []string{"sessions", "archived_sessions"} {
		root := filepath.Join(s.codexHome, relativeRoot)
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if walkErr != nil {
				if os.IsNotExist(walkErr) && path == root {
					return fs.SkipDir
				}
				skipped++
				if entry != nil && entry.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				skipped++
				if entry.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".jsonl") {
				return nil
			}
			if len(result) >= s.maxFiles {
				skipped++
				return errFileBudget
			}
			info, err := entry.Info()
			if err != nil || info.Size() > s.maxFileSize {
				skipped++
				return nil
			}
			pathKey, err := filepath.Rel(s.codexHome, path)
			if err != nil || strings.HasPrefix(pathKey, "..") {
				skipped++
				return nil
			}
			modified := info.ModTime()
			result = append(result, candidateFile{
				Path: path, PathKey: filepath.ToSlash(pathKey),
				Fingerprint: fingerprint{Size: info.Size(), ModifiedSeconds: modified.Unix(), ModifiedNanos: int64(modified.Nanosecond())},
			})
			return nil
		})
		if err != nil && !errors.Is(err, errFileBudget) && !errors.Is(err, fs.ErrNotExist) {
			if ctx.Err() != nil {
				return nil, 0, ctx.Err()
			}
			skipped++
		}
		if errors.Is(err, errFileBudget) {
			break
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].PathKey < result[j].PathKey })
	return result, skipped, nil
}

func (s *Service) aggregate(query Query, skipped int) Summary {
	now := s.now().In(s.location)
	dateTo := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.location)
	dateFrom := dateTo.AddDate(0, 0, -(query.Days - 1))
	exclusiveTo := dateTo.AddDate(0, 0, 1)
	result := Summary{
		Scope: query.Scope, Days: query.Days, DateFrom: dateFrom.Format("2006-01-02"),
		DateTo: dateTo.Format("2006-01-02"), SkippedFiles: skipped,
		Items: make([]DayItem, query.Days),
	}
	byDate := make(map[string]*DayItem, query.Days)
	for index := range result.Items {
		date := dateFrom.AddDate(0, 0, index).Format("2006-01-02")
		result.Items[index].Date = date
		byDate[date] = &result.Items[index]
	}
	relayModels := loadRelayPriceModels(s.codexHome)
	for _, item := range dedupeFiles(s.files) {
		if query.Scope == ScopeProject && !pathWithin(query.ProjectRoot, item.CWD) {
			continue
		}
		sessionIncluded := false
		for _, event := range item.Events {
			timestamp := time.Unix(event.Timestamp, 0).In(s.location)
			if timestamp.Before(dateFrom) || !timestamp.Before(exclusiveTo) {
				continue
			}
			day := byDate[timestamp.Format("2006-01-02")]
			if day == nil {
				continue
			}
			sessionIncluded = true
			day.Tokens += event.Usage.TotalTokens
			result.TotalTokens += event.Usage.TotalTokens
			if price, ok := priceForModel(event.Model, relayModels); ok {
				cost := usageCost(event.Usage, price)
				day.CostBreakdown.add(cost)
				day.CostUSD += cost.total()
				result.TotalCostUSD += cost.total()
			} else if event.Usage.hasTokens() {
				day.CostPartial = true
				result.CostPartial = true
			}
		}
		if sessionIncluded {
			result.SessionCount++
		}
	}
	return result
}

func dedupeFiles(files map[string]fileData) []fileData {
	chosen := make(map[string]fileData, len(files))
	for _, item := range files {
		key := item.SessionID
		if key == "" {
			key = item.PathKey
		}
		current, exists := chosen[key]
		if !exists || item.EventCount > current.EventCount ||
			(item.EventCount == current.EventCount && newerFingerprint(item.Fingerprint, current.Fingerprint)) {
			chosen[key] = item
		}
	}
	result := make([]fileData, 0, len(chosen))
	for _, item := range chosen {
		result = append(result, item)
	}
	sortFileData(result)
	return result
}

func newerFingerprint(left, right fingerprint) bool {
	if left.ModifiedSeconds != right.ModifiedSeconds {
		return left.ModifiedSeconds > right.ModifiedSeconds
	}
	return left.ModifiedNanos > right.ModifiedNanos
}

func sortFileData(items []fileData) {
	sort.Slice(items, func(i, j int) bool { return items[i].PathKey < items[j].PathKey })
}
