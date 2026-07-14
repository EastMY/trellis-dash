package trellis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yunnnn/trellis-dash/internal/model"
)

func TestScannerScanCompleteSnapshot(t *testing.T) {
	root := newTrellisProject(t)
	writeTestFile(t, root, ".trellis/spec/backend/rules.md", "# Rules\n")
	writeTestFile(t, root, ".trellis/workflow.md", strings.Join([]string{
		"正文引用 [workflow-state:ignored] 不应当成为定义。",
		"```markdown",
		"[workflow-state:my-status]",
		"```",
		"~~~~",
		"[workflow-state:fenced-too]",
		"~~~~",
		"[workflow-state:no_task]",
		"[workflow-state:planning]",
		"[workflow-state:planning-inline]",
		"[workflow-state:in_progress]",
		"[workflow-state:in_progress_inline]",
		"[workflow-state:review]",
		"[workflow-state:planning]",
	}, "\n"))

	activeDir := ".trellis/tasks/07-active"
	writeTestFile(t, root, activeDir+"/task.json", `{
  "id": "active",
  "title": "Active task",
  "status": "in_progress",
  "subtasks": [{"title":"first"}]
}`)
	writeTestFile(t, root, activeDir+"/prd.md", "# PRD\n")
	writeTestFile(t, root, activeDir+"/design.md", "# Design\n")
	writeTestFile(t, root, activeDir+"/implement.md", "# Plan\n")
	writeTestFile(t, root, activeDir+"/research/deep/note.md", "# Research\n")
	writeTestFile(t, root, activeDir+"/implement.jsonl", strings.Join([]string{
		`{"_example":"示例占位行"}`,
		`{"file":".trellis/spec/backend/rules.md","reason":"实现规范"}`,
		`{"file":".trellis/spec/backend/missing.md","reason":"缺失文件"}`,
		`{"file":"../outside.md","reason":"越界文件"}`,
		`{"file":`,
	}, "\n"))
	writeTestFile(t, root, activeDir+"/check.jsonl", `{"file":".trellis/spec/backend/rules.md","reason":"检查规范"}`+"\n")

	// 兼容旧版 active/ 子目录和缺失可选字段的 task.json。
	writeTestFile(t, root, ".trellis/tasks/active/legacy/task.json", `{"name":"legacy"}`)
	writeTestFile(t, root, ".trellis/tasks/archive/2026-06/nested/06-old/task.json", `{"id":"old","status":"in_progress"}`)
	writeTestFile(t, root, ".trellis/tasks/archive/not-a-month/ignored/task.json", `{"id":"ignored"}`)

	writeTestFile(t, root, ".trellis/.runtime/sessions/a-active.json", `{
  "platform":"codex",
  "last_seen_at":"2026-07-10T01:02:03+08:00",
  "current_task":".trellis/tasks/07-active",
  "current_run":{"phase":"check"}
}`)
	writeTestFile(t, root, ".trellis/.runtime/sessions/b-stale.json", `{
  "platform":"opencode",
	"current_task":".trellis/spec/backend",
  "current_run":null
}`)
	writeTestFile(t, root, ".trellis/.runtime/sessions/c-idle.json", `{
  "platform":"codex",
  "current_task":null,
  "current_run":null
}`)
	writeTestFile(t, root, ".trellis/.runtime/sessions/z-older.json", `{
  "platform":"codex",
  "last_seen_at":"2026-07-09T01:02:03+08:00",
  "current_task":".trellis/tasks/07-active",
  "current_run":{"phase":"implement"}
}`)

	snapshot, err := NewScanner().Scan(context.Background(), model.Project{ID: "demo", Root: root})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if got, want := len(snapshot.Tasks), 3; got != want {
		t.Fatalf("len(Tasks) = %d, want %d", got, want)
	}
	if got := []string{snapshot.Tasks[0].Key, snapshot.Tasks[1].Key, snapshot.Tasks[2].Key}; got[0] != "07-active" || got[1] != "legacy" || !strings.HasPrefix(got[2], "06-old~") {
		t.Fatalf("任务顺序或键错误: %v", got)
	}

	active := snapshot.Tasks[0]
	if active.ProjectID != "demo" || active.ID != "active" || active.Name != "active" || active.Title != "Active task" {
		t.Fatalf("活跃任务基础字段错误: %+v", active)
	}
	if active.RuntimePhase != "checking" || active.ActiveSessions != 2 {
		t.Fatalf("Session 派生状态错误: phase=%q sessions=%d", active.RuntimePhase, active.ActiveSessions)
	}
	if active.ArtifactCount != 4 || active.ContextIssues != 3 {
		t.Fatalf("任务计数错误: artifacts=%d issues=%d", active.ArtifactCount, active.ContextIssues)
	}
	if string(active.Children) != "[]" || string(active.RelatedFiles) != "[]" || string(active.Meta) != "{}" {
		t.Fatalf("缺失字段默认值错误: children=%s related=%s meta=%s", active.Children, active.RelatedFiles, active.Meta)
	}
	if active.SourcePath != ".trellis/tasks/07-active/task.json" || len(active.SourceHash) != 64 {
		t.Fatalf("任务来源字段错误: path=%q hash=%q", active.SourcePath, active.SourceHash)
	}

	legacy := snapshot.Tasks[1]
	if legacy.ID != "legacy" || legacy.Title != "legacy" || legacy.Status != "planning" || legacy.RuntimePhase != "planning" || legacy.Priority != "P2" {
		t.Fatalf("缺字段兼容错误: %+v", legacy)
	}
	archived := snapshot.Tasks[2]
	if !archived.Archived || archived.ArchiveMonth != "2026-06" || archived.RuntimePhase != "completed" {
		t.Fatalf("归档派生字段错误: %+v", archived)
	}

	if got, want := len(snapshot.Artifacts), 4; got != want {
		t.Fatalf("len(Artifacts) = %d, want %d", got, want)
	}
	artifactByKind := make(map[string]model.Artifact)
	for _, artifact := range snapshot.Artifacts {
		artifactByKind[artifact.Kind] = artifact
		if artifact.ContentType != "text/markdown" || artifact.Size == 0 || len(artifact.Hash) != 64 {
			t.Errorf("Artifact 元数据错误: %+v", artifact)
		}
	}
	if artifactByKind["research"].Name != "research/deep/note.md" ||
		artifactByKind["research"].Path != ".trellis/tasks/07-active/research/deep/note.md" {
		t.Fatalf("Research 路径错误: %+v", artifactByKind["research"])
	}

	if got, want := len(snapshot.ContextEntries), 6; got != want {
		t.Fatalf("len(ContextEntries) = %d, want %d", got, want)
	}
	var example, valid, missing, outside, malformed *model.ContextEntry
	for i := range snapshot.ContextEntries {
		entry := &snapshot.ContextEntries[i]
		switch {
		case entry.Example:
			example = entry
		case entry.File == ".trellis/spec/backend/rules.md" && entry.Action == "implement":
			valid = entry
		case strings.Contains(entry.File, "missing.md"):
			missing = entry
		case entry.File == "../outside.md":
			outside = entry
		case strings.Contains(entry.Error, "无效 JSON"):
			malformed = entry
		}
	}
	if example == nil || !example.Valid || !example.Example || example.Reason != "示例占位行" {
		t.Fatalf("_example 识别错误: %+v", example)
	}
	if valid == nil || !valid.Valid || !valid.Exists {
		t.Fatalf("有效 Context 识别错误: %+v", valid)
	}
	if missing == nil || missing.Valid || missing.Exists || !strings.Contains(missing.Error, "不存在") {
		t.Fatalf("缺失 Context 识别错误: %+v", missing)
	}
	if outside == nil || outside.Valid || !strings.Contains(outside.Error, "越过") {
		t.Fatalf("越界 Context 识别错误: %+v", outside)
	}
	if malformed == nil || malformed.Valid {
		t.Fatalf("无效 JSONL 识别错误: %+v", malformed)
	}

	if got, want := len(snapshot.Sessions), 4; got != want {
		t.Fatalf("len(Sessions) = %d, want %d", got, want)
	}
	if snapshot.Sessions[0].TaskKey != "07-active" || snapshot.Sessions[0].Stale {
		t.Fatalf("活动 Session 关联错误: %+v", snapshot.Sessions[0])
	}
	if snapshot.Sessions[0].LastSeenAt == nil || snapshot.Sessions[0].LastSeenAt.Format(time.RFC3339) != "2026-07-09T17:02:03Z" {
		t.Fatalf("Session 时间解析错误: %+v", snapshot.Sessions[0].LastSeenAt)
	}
	if !snapshot.Sessions[1].Stale || snapshot.Sessions[1].TaskKey != "" {
		t.Fatalf("陈旧 Session 识别错误: %+v", snapshot.Sessions[1])
	}
	if snapshot.Sessions[2].Stale {
		t.Fatalf("空任务 Session 不应视为 stale: %+v", snapshot.Sessions[2])
	}

	if got, want := len(snapshot.WorkflowStates), 3; got != want {
		t.Fatalf("len(WorkflowStates) = %d, want %d: %+v", got, want, snapshot.WorkflowStates)
	}
	if snapshot.WorkflowStates[0].Name != "planning" || snapshot.WorkflowStates[0].Label != "Planning" ||
		snapshot.WorkflowStates[1].Name != "in_progress" || snapshot.WorkflowStates[1].Label != "In Progress" ||
		snapshot.WorkflowStates[2].Name != "review" || snapshot.WorkflowStates[2].Label != "Review" {
		t.Fatalf("工作流状态解析错误: %+v", snapshot.WorkflowStates)
	}
	for name, value := range map[string]string{
		"TasksHash": snapshot.TasksHash, "SessionsHash": snapshot.SessionsHash, "SpecsHash": snapshot.SpecsHash,
	} {
		if len(value) != 64 {
			t.Errorf("%s = %q，不是 SHA-256 十六进制", name, value)
		}
	}
}

func TestScannerConcurrentTaskResourcesRemainStable(t *testing.T) {
	root := newTrellisProject(t)
	writeTestFile(t, root, ".trellis/spec/rules.md", "# Rules\n")
	for index := range 16 {
		directory := fmt.Sprintf(".trellis/tasks/%02d-task", index)
		writeTestFile(t, root, directory+"/task.json", fmt.Sprintf(`{"id":"task-%d","status":"in_progress"}`, index))
		writeTestFile(t, root, directory+"/prd.md", fmt.Sprintf("# Task %d\n", index))
		writeTestFile(t, root, directory+"/implement.jsonl", `{"file":".trellis/spec/rules.md","reason":"并发稳定性"}`+"\n")
	}

	const scans = 6
	type result struct {
		snapshot model.TrellisSnapshot
		err      error
	}
	results := make(chan result, scans)
	var workers sync.WaitGroup
	workers.Add(scans)
	for range scans {
		go func() {
			defer workers.Done()
			snapshot, err := NewScanner().Scan(context.Background(), model.Project{ID: "parallel", Root: root})
			results <- result{snapshot: snapshot, err: err}
		}()
	}
	workers.Wait()
	close(results)

	var expected *model.TrellisSnapshot
	for item := range results {
		if item.err != nil {
			t.Fatalf("并发扫描失败: %v", item.err)
		}
		if expected == nil {
			copy := item.snapshot
			expected = &copy
			continue
		}
		if item.snapshot.TasksHash != expected.TasksHash || item.snapshot.Stats != expected.Stats || len(item.snapshot.Tasks) != 16 {
			t.Fatalf("并发扫描结果不稳定: hash=%q stats=%+v tasks=%d", item.snapshot.TasksHash, item.snapshot.Stats, len(item.snapshot.Tasks))
		}
	}
}

func TestScannerScanTaskOnlyReadsMatchedTask(t *testing.T) {
	root := newTrellisProject(t)
	writeTestFile(t, root, ".trellis/tasks/task-a/task.json", `{"id":"task-a","status":"in_progress"}`)
	writeTestFile(t, root, ".trellis/tasks/task-a/prd.md", "# A v1\n")
	writeTestFile(t, root, ".trellis/tasks/task-b/task.json", `{"id":"task-b","status":"planning"}`)
	writeTestFile(t, root, ".trellis/tasks/task-b/prd.md", strings.Repeat("B", 128*1024))
	scanner := NewScanner()
	project := model.Project{ID: "incremental", Root: root}
	full, err := scanner.Scan(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	var taskA model.Task
	for _, task := range full.Tasks {
		if task.ID == "task-a" {
			taskA = task
		}
	}
	writeTestFile(t, root, ".trellis/tasks/task-a/prd.md", "# A v2\n")
	bundle, exists, err := scanner.ScanTask(context.Background(), project, taskA)
	if err != nil || !exists {
		t.Fatalf("单任务扫描失败: exists=%v err=%v", exists, err)
	}
	if bundle.Task.Key != taskA.Key || len(bundle.Artifacts) != 1 || bundle.Artifacts[0].Content != "# A v2\n" {
		t.Fatalf("单任务结果异常: %+v", bundle)
	}
	if bundle.Task.IndexHash == taskA.IndexHash {
		t.Fatal("文档变化后单任务 IndexHash 未变化")
	}
	if bundle.Stats.RawBytes >= full.Stats.RawBytes {
		t.Fatalf("增量扫描读取量未下降: incremental=%d full=%d", bundle.Stats.RawBytes, full.Stats.RawBytes)
	}
}

func TestScannerRevalidateContextEntriesWithoutReadingManifests(t *testing.T) {
	root := newTrellisProject(t)
	writeTestFile(t, root, "docs/rules.md", "# Rules\n")
	entries := []model.ContextEntry{
		{ProjectID: "demo", TaskKey: "task-a", Action: "implement", Line: 1, Type: "file", File: "docs/rules.md"},
		{ProjectID: "demo", TaskKey: "task-a", Action: "implement", Line: 2, Type: "file", File: "docs/rules.md"},
		{ProjectID: "demo", TaskKey: "task-a", Action: "check", Line: 1, Type: "file", File: "docs/missing.md"},
		{ProjectID: "demo", TaskKey: "task-a", Action: "check", Line: 2, Type: "example", Example: true},
	}

	validated, err := NewScanner().RevalidateContextEntries(context.Background(), root, entries)
	if err != nil {
		t.Fatal(err)
	}
	if !validated[0].Valid || !validated[0].Exists {
		t.Fatalf("首条有效引用校验错误: %+v", validated[0])
	}
	if validated[1].Valid || !validated[1].Duplicate || !strings.Contains(validated[1].Error, "重复") {
		t.Fatalf("重复引用校验错误: %+v", validated[1])
	}
	if validated[2].Valid || validated[2].Exists || !strings.Contains(validated[2].Error, "不存在") {
		t.Fatalf("缺失引用校验错误: %+v", validated[2])
	}
	if !validated[3].Valid || validated[3].Exists || validated[3].Duplicate {
		t.Fatalf("模板行校验错误: %+v", validated[3])
	}

	if err := os.Remove(filepath.Join(root, "docs", "rules.md")); err != nil {
		t.Fatal(err)
	}
	validated, err = NewScanner().RevalidateContextEntries(context.Background(), root, validated)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 2; index++ {
		if validated[index].Valid || validated[index].Exists || validated[index].Duplicate {
			t.Fatalf("文件删除后引用 %d 未失效: %+v", index, validated[index])
		}
	}
}

func TestSessionFailurePhaseOverridesAction(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  string
	}{
		{name: "复合字符串", raw: `"check_failed"`},
		{name: "实施错误", raw: `"implementation_error"`},
		{name: "对象失败状态", raw: `{"action":"check","status":"failed"}`},
		{name: "对象阻塞状态", raw: `{"phase":"implement","status":"blocked"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if phase := phaseFromCurrentRun(json.RawMessage(test.raw)); phase != "blocked" {
				t.Fatalf("phase = %q，期望 blocked", phase)
			}
		})
	}
}

func TestScannerHashesAreContentStable(t *testing.T) {
	root := newTrellisProject(t)
	taskPath := ".trellis/tasks/hash-task/task.json"
	artifactPath := ".trellis/tasks/hash-task/prd.md"
	specPath := ".trellis/spec/rules.md"
	sessionPath := ".trellis/.runtime/sessions/hash.json"
	writeTestFile(t, root, taskPath, `{"id":"hash","status":"in_progress"}`)
	writeTestFile(t, root, artifactPath, "first artifact")
	writeTestFile(t, root, specPath, "first spec")
	writeTestFile(t, root, sessionPath, `{"current_task":"hash-task","current_run":null}`)
	project := model.Project{ID: "hash", Root: root}

	first := mustScan(t, project)
	future := time.Now().Add(2 * time.Hour)
	for _, path := range []string{taskPath, artifactPath, specPath, sessionPath} {
		if err := os.Chtimes(filepath.Join(root, filepath.FromSlash(path)), future, future); err != nil {
			t.Fatalf("Chtimes(%s): %v", path, err)
		}
	}
	second := mustScan(t, project)
	if first.TasksHash != second.TasksHash || first.SessionsHash != second.SessionsHash || first.SpecsHash != second.SpecsHash {
		t.Fatalf("仅修改 mtime 不应改变内容哈希:\nfirst=%+v\nsecond=%+v", first, second)
	}

	writeTestFile(t, root, artifactPath, "changed artifact")
	third := mustScan(t, project)
	if third.TasksHash == second.TasksHash {
		t.Fatal("Artifact 内容变化后 TasksHash 未变化")
	}
	if third.SessionsHash != second.SessionsHash || third.SpecsHash != second.SpecsHash {
		t.Fatal("Artifact 内容变化不应影响 SessionsHash/SpecsHash")
	}

	writeTestFile(t, root, specPath, "changed spec")
	fourth := mustScan(t, project)
	if fourth.SpecsHash == third.SpecsHash {
		t.Fatal("规范内容变化后 SpecsHash 未变化")
	}

	writeTestFile(t, root, sessionPath, `{"current_task":"missing","current_run":null}`)
	fifth := mustScan(t, project)
	if fifth.SessionsHash == fourth.SessionsHash {
		t.Fatal("Session 内容变化后 SessionsHash 未变化")
	}
}

func TestScannerContextDirectoryAndDuplicate(t *testing.T) {
	root := newTrellisProject(t)
	writeTestFile(t, root, ".trellis/tasks/context/task.json", `{}`)
	if err := os.MkdirAll(filepath.Join(root, ".trellis", "spec", "backend"), 0o750); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, ".trellis/spec/backend/rules.md", "# rules")
	writeTestFile(t, root, ".trellis/tasks/context/implement.jsonl", strings.Join([]string{
		`{"file":".trellis/spec/backend","type":"directory","reason":"规范目录"}`,
		`{"file":".trellis/spec/backend/rules.md","type":"file"}`,
		`{"file":"./.trellis/spec/backend/rules.md"}`,
	}, "\n"))

	snapshot := mustScan(t, model.Project{ID: "demo", Root: root})
	if len(snapshot.ContextEntries) != 3 {
		t.Fatalf("Context 数量 = %d，期望 3", len(snapshot.ContextEntries))
	}
	directory, original, duplicate := snapshot.ContextEntries[0], snapshot.ContextEntries[1], snapshot.ContextEntries[2]
	if directory.Type != "directory" || !directory.Valid || !directory.Exists {
		t.Fatalf("目录 Context 解析错误: %+v", directory)
	}
	if original.Type != "file" || !original.Valid || original.Duplicate {
		t.Fatalf("首个文件 Context 解析错误: %+v", original)
	}
	if duplicate.Type != "file" || duplicate.Valid || !duplicate.Duplicate || !strings.Contains(duplicate.Error, "重复") {
		t.Fatalf("重复 Context 未标记: %+v", duplicate)
	}
	if snapshot.Tasks[0].ContextIssues != 1 {
		t.Fatalf("ContextIssues = %d，期望 1", snapshot.Tasks[0].ContextIssues)
	}
}

func TestScannerHonorsCanceledContext(t *testing.T) {
	root := newTrellisProject(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewScanner().Scan(ctx, model.Project{ID: "cancel", Root: root})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Scan() error = %v, want context.Canceled", err)
	}
}

func TestScannerUniquifiesDuplicateTaskKeys(t *testing.T) {
	root := newTrellisProject(t)
	writeTestFile(t, root, ".trellis/tasks/same/task.json", `{}`)
	writeTestFile(t, root, ".trellis/tasks/archive/2026-06/same/task.json", `{}`)
	writeTestFile(t, root, ".trellis/tasks/archive/2026-06/same/same/task.json", `{}`)
	writeTestFile(t, root, ".trellis/.runtime/sessions/nested.json", `{"current_task":".trellis/tasks/archive/2026-06/same/same"}`)
	snapshot := mustScan(t, model.Project{ID: "duplicate", Root: root})
	if got, want := len(snapshot.Tasks), 3; got != want {
		t.Fatalf("len(Tasks) = %d, want %d", got, want)
	}
	keys := make(map[string]struct{}, len(snapshot.Tasks))
	for _, task := range snapshot.Tasks {
		if task.Directory != "same" {
			t.Fatalf("Directory 未保留 basename: %+v", task)
		}
		if _, duplicate := keys[task.Key]; duplicate {
			t.Fatalf("任务键仍然重复: %q", task.Key)
		}
		keys[task.Key] = struct{}{}
	}
	if _, exists := keys["same"]; !exists {
		t.Fatalf("活跃任务没有保留简短键: %v", keys)
	}
	nestedKey := ""
	for key := range keys {
		if key != "same" && (!strings.HasPrefix(key, "same~") || len(key) < len("same~")+12) {
			t.Fatalf("碰撞键格式错误: %q", key)
		}
	}
	for _, task := range snapshot.Tasks {
		if task.SourcePath == ".trellis/tasks/archive/2026-06/same/same/task.json" {
			nestedKey = task.Key
		}
	}
	if nestedKey == "" || len(snapshot.Sessions) != 1 || snapshot.Sessions[0].TaskKey != nestedKey || snapshot.Sessions[0].Stale {
		t.Fatalf("嵌套任务 Session 关联错误: nested=%q sessions=%+v", nestedKey, snapshot.Sessions)
	}
}

func mustScan(t *testing.T, project model.Project) model.TrellisSnapshot {
	t.Helper()
	snapshot, err := NewScanner().Scan(context.Background(), project)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	return snapshot
}

func newTrellisProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".trellis"), 0o750); err != nil {
		t.Fatalf("MkdirAll(.trellis): %v", err)
	}
	// macOS 的 /var 是 /private/var 的符号链接；测试夹具也应模拟 API 注册后保存的真实路径。
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", root, err)
	}
	return canonical
}

func writeTestFile(t *testing.T, root, relative, content string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
	return path
}

func writeTestJSON(t *testing.T, root, relative string, value any) string {
	t.Helper()
	content, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return writeTestFile(t, root, relative, string(content))
}
