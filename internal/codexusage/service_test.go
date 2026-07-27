package codexusage

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSummaryParsesFiltersPricesAndLocalDates(t *testing.T) {
	home := t.TempDir()
	projectRoot := filepath.Join(t.TempDir(), "project")
	child := filepath.Join(projectRoot, "child")
	other := projectRoot + "-other"
	for _, directory := range []string{child, other} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeSession(t, filepath.Join(home, "sessions", "a.jsonl"), []any{
		sessionRow("same-session", child),
		map[string]any{"timestamp": "2026-07-20T16:30:00Z", "type": "turn_context", "payload": map[string]any{"model": "gpt-5.4"}},
		tokenRow("2026-07-20T16:31:00Z", "1000", 200, 100, 50, "1100"),
		"{broken-json",
		map[string]any{"timestamp": "2026-07-21T02:00:00+08:00", "type": "turn_context", "payload": map[string]any{"model": "future-model"}},
		tokenRow("2026-07-21T02:01:00+08:00", 10, nil, "", 0, 10),
	})
	writeSession(t, filepath.Join(home, "sessions", "other.jsonl"), []any{
		sessionRow("other-session", other),
		map[string]any{"timestamp": "2026-07-21T03:00:00+08:00", "type": "turn_context", "payload": map[string]any{"model": "gpt-5.4"}},
		tokenRow("2026-07-21T03:01:00+08:00", 99, 0, 1, 0, 100),
	})
	// archived_sessions 里可能保留同一会话副本；事件较少的副本不能重复计数。
	writeKnownSession(t, filepath.Join(home, "archived_sessions", "same-copy.jsonl"), "same-session", child, 9999)

	location := time.FixedZone("Asia/Shanghai", 8*60*60)
	service := newTestService(home, "", location)
	project, err := service.Summary(context.Background(), Query{Scope: ScopeProject, Days: 7, ProjectRoot: projectRoot})
	if err != nil {
		t.Fatal(err)
	}
	if project.DateFrom != "2026-07-15" || project.DateTo != "2026-07-21" || len(project.Items) != 7 {
		t.Fatalf("日期序列异常: %+v", project)
	}
	if project.TotalTokens != 1110 || project.SessionCount != 1 || !project.CostPartial {
		t.Fatalf("项目统计异常: %+v", project)
	}
	if project.Items[6].Date != "2026-07-21" || project.Items[6].Tokens != 1110 || !project.Items[6].CostPartial {
		t.Fatalf("本机时区归日异常: %+v", project.Items[6])
	}
	if !almostEqual(project.TotalCostUSD, 0.00355) {
		t.Fatalf("已知模型费用 = %.9f，期望 0.00355", project.TotalCostUSD)
	}
	breakdown := project.Items[6].CostBreakdown
	if !almostEqual(breakdown.UncachedInputUSD, 0.002) ||
		!almostEqual(breakdown.CachedInputUSD, 0.00005) ||
		!almostEqual(breakdown.OutputUSD, 0.0015) ||
		breakdown.CacheWriteUSD != 0 ||
		!almostEqual(breakdown.total(), project.Items[6].CostUSD) {
		t.Fatalf("每日费用分类异常: %+v", breakdown)
	}

	all, err := service.Summary(context.Background(), Query{Scope: ScopeAll, Days: 7})
	if err != nil {
		t.Fatal(err)
	}
	if all.TotalTokens != 1210 || all.SessionCount != 2 {
		t.Fatalf("全部范围统计异常: %+v", all)
	}
}

func TestIncrementalCacheHandlesAddModifyDeleteAndRestart(t *testing.T) {
	home := t.TempDir()
	cachePath := filepath.Join(t.TempDir(), "dashboard.db.codex-usage.json")
	firstPath := filepath.Join(home, "sessions", "first.jsonl")
	secondPath := filepath.Join(home, "archived_sessions", "second.jsonl")
	writeKnownSession(t, firstPath, "first", "/workspace/a", 10)
	service := newTestService(home, cachePath, time.UTC)

	first := mustSummary(t, service, ScopeAll, "")
	if first.TotalTokens != 10 || service.parsedFiles != 1 {
		t.Fatalf("首次解析异常: summary=%+v parsed=%d", first, service.parsedFiles)
	}
	cacheInfo, err := os.Stat(cachePath)
	if err != nil || cacheInfo.Mode().Perm() != 0o600 {
		t.Fatalf("缓存权限异常: info=%v err=%v", cacheInfo, err)
	}
	_ = mustSummary(t, service, ScopeAll, "")
	if service.parsedFiles != 1 {
		t.Fatalf("未变化文件不应重解析: %d", service.parsedFiles)
	}

	writeKnownSession(t, secondPath, "second", "/workspace/b", 20)
	added := mustSummary(t, service, ScopeAll, "")
	if added.TotalTokens != 30 || service.parsedFiles != 2 {
		t.Fatalf("新增文件刷新异常: summary=%+v parsed=%d", added, service.parsedFiles)
	}
	writeKnownSession(t, firstPath, "first", "/workspace/a", 1000)
	modified := mustSummary(t, service, ScopeAll, "")
	if modified.TotalTokens != 1020 || service.parsedFiles != 3 {
		t.Fatalf("修改文件刷新异常: summary=%+v parsed=%d", modified, service.parsedFiles)
	}
	if err := os.Remove(secondPath); err != nil {
		t.Fatal(err)
	}
	deleted := mustSummary(t, service, ScopeAll, "")
	if deleted.TotalTokens != 1000 || service.parsedFiles != 3 {
		t.Fatalf("删除文件刷新异常: summary=%+v parsed=%d", deleted, service.parsedFiles)
	}

	restarted := newTestService(home, cachePath, time.UTC)
	afterRestart := mustSummary(t, restarted, ScopeAll, "")
	if afterRestart.TotalTokens != 1000 || restarted.parsedFiles != 0 {
		t.Fatalf("重启后应命中磁盘缓存: summary=%+v parsed=%d", afterRestart, restarted.parsedFiles)
	}
}

func TestReferenceCacheSeedAndCorruptInputsDegrade(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "sessions", "seed.jsonl")
	writeSession(t, path, []any{sessionRow("seed-session", "/workspace/project"), "{not-json"})
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	referencePath := filepath.Join(home, "cache", "codex-token", "index-v1.json")
	if err := os.MkdirAll(filepath.Dir(referencePath), 0o755); err != nil {
		t.Fatal(err)
	}
	reference := map[string]any{
		"version": 1,
		"files": []any{map[string]any{
			"p": "sessions/seed.jsonl",
			"f": map[string]any{"s": info.Size(), "t": info.ModTime().Unix(), "n": info.ModTime().Nanosecond()},
			"i": "seed-session", "n": 1, "m": []string{"gpt-5.4"},
			"e": [][]int64{{time.Date(2026, 7, 21, 1, 0, 0, 0, time.UTC).Unix(), 0, 10, 0, 1, 0, 11}},
		}},
	}
	writeJSONFile(t, referencePath, reference)
	service := newTestService(home, filepath.Join(t.TempDir(), "usage.json"), time.UTC)
	result := mustSummary(t, service, ScopeProject, "/workspace/project")
	if result.TotalTokens != 11 || service.parsedFiles != 0 {
		t.Fatalf("参考缓存种子未生效: summary=%+v parsed=%d", result, service.parsedFiles)
	}

	oversized := filepath.Join(home, "sessions", "oversized.jsonl")
	if err := os.WriteFile(oversized, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	limited := NewService(Options{
		CodexHome: home, Location: time.UTC, Now: fixedNow,
		Logger: discardLogger(), MaxFileSize: 5,
	})
	degraded := mustSummary(t, limited, ScopeAll, "")
	if degraded.SkippedFiles == 0 {
		t.Fatalf("超限文件应计入降级数量: %+v", degraded)
	}

	empty := newTestService(filepath.Join(t.TempDir(), "missing"), "", time.UTC)
	zero := mustSummary(t, empty, ScopeAll, "")
	if zero.TotalTokens != 0 || zero.SessionCount != 0 || len(zero.Items) != 7 {
		t.Fatalf("缺失日志目录应返回完整零值: %+v", zero)
	}
}

func TestCorruptOwnCacheFallsBackToSource(t *testing.T) {
	home := t.TempDir()
	writeKnownSession(t, filepath.Join(home, "sessions", "source.jsonl"), "source", "/workspace", 15)
	cachePath := filepath.Join(t.TempDir(), "usage.json")
	if err := os.WriteFile(cachePath, []byte("{bad-cache"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := newTestService(home, cachePath, time.UTC)
	result := mustSummary(t, service, ScopeAll, "")
	if result.TotalTokens != 15 || service.parsedFiles != 1 {
		t.Fatalf("损坏缓存应回退原始日志: summary=%+v parsed=%d", result, service.parsedFiles)
	}
	var rebuilt cacheIndex
	data, err := os.ReadFile(cachePath)
	if err != nil || json.Unmarshal(data, &rebuilt) != nil || rebuilt.Version != cacheVersion {
		t.Fatalf("损坏缓存未被重建: data=%q err=%v", data, err)
	}
}

func TestPathWithinRejectsSimilarPrefix(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	if !pathWithin(root, filepath.Join(root, "child")) || !pathWithin(root, root) {
		t.Fatal("根目录及子目录应匹配")
	}
	if pathWithin(root, root+"-copy") {
		t.Fatal("相似字符串前缀不应匹配")
	}
}

func newTestService(home, cachePath string, location *time.Location) *Service {
	return NewService(Options{
		CodexHome: home, CachePath: cachePath, Location: location,
		Now: fixedNow, Logger: discardLogger(),
	})
}

func fixedNow() time.Time { return time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC) }

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func mustSummary(t *testing.T, service *Service, scope Scope, root string) Summary {
	t.Helper()
	result, err := service.Summary(context.Background(), Query{Scope: scope, Days: 7, ProjectRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func sessionRow(id, cwd string) map[string]any {
	return map[string]any{"timestamp": "2026-07-21T00:00:00Z", "type": "session_meta", "payload": map[string]any{"id": id, "cwd": cwd}}
}

func tokenRow(timestamp string, input, cached, output, reasoning, total any) map[string]any {
	return map[string]any{
		"timestamp": timestamp, "type": "event_msg",
		"payload": map[string]any{"type": "token_count", "info": map[string]any{"last_token_usage": map[string]any{
			"input_tokens": input, "cached_input_tokens": cached, "output_tokens": output,
			"reasoning_output_tokens": reasoning, "total_tokens": total,
		}}},
	}
}

func writeKnownSession(t *testing.T, path, id, cwd string, total int64) {
	t.Helper()
	writeSession(t, path, []any{
		sessionRow(id, cwd),
		map[string]any{"timestamp": "2026-07-21T00:01:00Z", "type": "turn_context", "payload": map[string]any{"model": "gpt-5.4"}},
		tokenRow("2026-07-21T00:02:00Z", total, 0, 0, 0, total),
	})
}

func writeSession(t *testing.T, path string, rows []any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	for _, row := range rows {
		if text, ok := row.(string); ok {
			if _, err := file.WriteString(text + "\n"); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := encoder.Encode(row); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(file).Encode(value); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func almostEqual(left, right float64) bool {
	difference := left - right
	if difference < 0 {
		difference = -difference
	}
	return difference < 0.000000001
}
