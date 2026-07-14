package gitstate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yunnnn/trellis-dash/internal/model"
)

func parseStatus(output []byte, snapshot *model.GitSnapshot) error {
	records := bytes.Split(output, []byte{0})
	for index := 0; index < len(records); index++ {
		record := string(records[index])
		if record == "" {
			continue
		}

		switch {
		case strings.HasPrefix(record, "# "):
			if err := parseBranchHeader(record, snapshot); err != nil {
				return err
			}
		case strings.HasPrefix(record, "1 "):
			file, err := parseOrdinaryFile(record)
			if err != nil {
				return err
			}
			appendFile(snapshot, file)
		case strings.HasPrefix(record, "2 "):
			file, err := parseRenamedFile(record)
			if err != nil {
				return err
			}
			index++
			if index >= len(records) {
				return fmt.Errorf("重命名记录缺少原路径")
			}
			file.OldPath = string(records[index])
			appendFile(snapshot, file)
		case strings.HasPrefix(record, "u "):
			file, err := parseUnmergedFile(record)
			if err != nil {
				return err
			}
			appendFile(snapshot, file)
		case strings.HasPrefix(record, "? "):
			file := model.GitFile{
				Path:      strings.TrimPrefix(record, "? "),
				Index:     "?",
				Worktree:  "?",
				Status:    "untracked",
				Untracked: true,
			}
			appendFile(snapshot, file)
		case strings.HasPrefix(record, "! "):
			// ignored 文件不属于工作区变化，porcelain 通常也不会返回此记录。
			continue
		default:
			return fmt.Errorf("未知 porcelain v2 记录 %q", record)
		}
	}

	snapshot.Dirty = len(snapshot.Files) > 0
	return nil
}

// parseNumStat 汇总 git diff --numstat -z 的文本行变化。
// 二进制文件以 "-\t-" 表示，无法定义文本行数，因此按需求忽略。
func parseNumStat(output []byte) (int, int, error) {
	records := bytes.Split(output, []byte{0})
	var addedTotal int64
	var deletedTotal int64
	maxInt := int64(^uint(0) >> 1)

	for index := 0; index < len(records); index++ {
		record := records[index]
		if len(record) == 0 {
			continue
		}
		fields := bytes.SplitN(record, []byte{'\t'}, 3)
		if len(fields) != 3 {
			return 0, 0, fmt.Errorf("无效 numstat 记录 %q", record)
		}
		if len(fields[2]) == 0 {
			// -z 模式下，重命名记录会把旧、新路径放在后续两个 NUL 记录中。
			if index+2 >= len(records) || len(records[index+1]) == 0 || len(records[index+2]) == 0 {
				return 0, 0, fmt.Errorf("无效 numstat 重命名记录 %q", record)
			}
			index += 2
		}
		if bytes.Equal(fields[0], []byte("-")) && bytes.Equal(fields[1], []byte("-")) {
			continue
		}

		added, err := strconv.ParseInt(string(fields[0]), 10, 64)
		if err != nil || added < 0 {
			return 0, 0, fmt.Errorf("无效新增行数 %q", fields[0])
		}
		deleted, err := strconv.ParseInt(string(fields[1]), 10, 64)
		if err != nil || deleted < 0 {
			return 0, 0, fmt.Errorf("无效删除行数 %q", fields[1])
		}
		if addedTotal > maxInt-added || deletedTotal > maxInt-deleted {
			return 0, 0, fmt.Errorf("无效 numstat: 行数统计超出整数范围")
		}
		addedTotal += added
		deletedTotal += deleted
	}
	return int(addedTotal), int(deletedTotal), nil
}

func parseBranchHeader(record string, snapshot *model.GitSnapshot) error {
	key, value, found := strings.Cut(strings.TrimPrefix(record, "# "), " ")
	if !found {
		return fmt.Errorf("无效分支记录 %q", record)
	}

	switch key {
	case "branch.oid":
		if value != "(initial)" {
			snapshot.Head = value
		}
	case "branch.head":
		snapshot.Branch = value
	case "branch.upstream":
		snapshot.Upstream = value
	case "branch.ab":
		fields := strings.Fields(value)
		if len(fields) != 2 || !strings.HasPrefix(fields[0], "+") || !strings.HasPrefix(fields[1], "-") {
			return fmt.Errorf("无效 ahead/behind 记录 %q", record)
		}
		ahead, err := strconv.Atoi(strings.TrimPrefix(fields[0], "+"))
		if err != nil {
			return fmt.Errorf("解析 ahead 失败: %w", err)
		}
		behind, err := strconv.Atoi(strings.TrimPrefix(fields[1], "-"))
		if err != nil {
			return fmt.Errorf("解析 behind 失败: %w", err)
		}
		snapshot.Ahead = ahead
		snapshot.Behind = behind
	}
	return nil
}

func parseOrdinaryFile(record string) (model.GitFile, error) {
	fields := strings.SplitN(record, " ", 9)
	if len(fields) != 9 || len(fields[1]) != 2 {
		return model.GitFile{}, fmt.Errorf("无效普通文件记录 %q", record)
	}
	return gitFile(fields[1], fields[8], false), nil
}

func parseRenamedFile(record string) (model.GitFile, error) {
	fields := strings.SplitN(record, " ", 10)
	if len(fields) != 10 || len(fields[1]) != 2 {
		return model.GitFile{}, fmt.Errorf("无效重命名文件记录 %q", record)
	}
	return gitFile(fields[1], fields[9], false), nil
}

func parseUnmergedFile(record string) (model.GitFile, error) {
	fields := strings.SplitN(record, " ", 11)
	if len(fields) != 11 || len(fields[1]) != 2 {
		return model.GitFile{}, fmt.Errorf("无效冲突文件记录 %q", record)
	}
	return gitFile(fields[1], fields[10], true), nil
}

func gitFile(xy, path string, conflict bool) model.GitFile {
	file := model.GitFile{
		Path:     path,
		Index:    xy[0:1],
		Worktree: xy[1:2],
		Conflict: conflict,
	}
	file.Status = fileStatus(xy, conflict)
	return file
}

func fileStatus(xy string, conflict bool) string {
	if conflict || strings.ContainsRune(xy, 'U') {
		return "conflicted"
	}
	if strings.ContainsRune(xy, 'D') {
		return "deleted"
	}
	if strings.ContainsRune(xy, 'A') {
		return "added"
	}
	if strings.ContainsRune(xy, 'R') {
		return "renamed"
	}
	if strings.ContainsRune(xy, 'C') {
		return "copied"
	}
	if strings.ContainsRune(xy, 'T') {
		return "type_changed"
	}
	if strings.ContainsRune(xy, 'M') {
		return "modified"
	}
	return "unknown"
}

func appendFile(snapshot *model.GitSnapshot, file model.GitFile) {
	snapshot.Files = append(snapshot.Files, file)
	if file.Conflict {
		snapshot.Conflicted++
		return
	}
	if file.Untracked {
		snapshot.Untracked++
		return
	}

	xy := file.Index + file.Worktree
	if strings.ContainsRune(xy, 'A') {
		snapshot.Added++
	}
	if strings.ContainsRune(xy, 'D') {
		snapshot.Deleted++
	}
	if strings.ContainsAny(xy, "MRCT") {
		snapshot.Modified++
	}
}

func parseWorktrees(output []byte) ([]model.Worktree, error) {
	worktrees := make([]model.Worktree, 0)
	var current *model.Worktree
	flush := func() error {
		if current == nil {
			return nil
		}
		if current.Path == "" {
			return errorsForWorktree("记录缺少路径")
		}
		worktrees = append(worktrees, *current)
		current = nil
		return nil
	}

	lines := strings.Split(strings.ReplaceAll(string(output), "\r\n", "\n"), "\n")
	for _, line := range lines {
		if line == "" {
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}

		key, value, hasValue := strings.Cut(line, " ")
		if key == "worktree" {
			if err := flush(); err != nil {
				return nil, err
			}
			if !hasValue || value == "" {
				return nil, errorsForWorktree("worktree 行缺少路径")
			}
			current = &model.Worktree{Path: value}
			continue
		}
		if current == nil {
			return nil, errorsForWorktree(fmt.Sprintf("属性 %q 前缺少 worktree 行", key))
		}

		switch key {
		case "HEAD":
			current.Head = value
		case "branch":
			current.Branch = strings.TrimPrefix(value, "refs/heads/")
		case "bare":
			current.Bare = true
		case "detached":
			current.Detached = true
		case "locked":
			current.Locked = markerReason("locked", value, hasValue)
		case "prunable":
			current.Prunable = markerReason("prunable", value, hasValue)
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return worktrees, nil
}

func markerReason(fallback, value string, hasValue bool) string {
	if hasValue && value != "" {
		return value
	}
	return fallback
}

func errorsForWorktree(message string) error {
	return fmt.Errorf("无效 worktree porcelain: %s", message)
}

func parseCommits(output []byte) ([]model.GitCommit, error) {
	records := strings.Split(string(output), "\x1e")
	commits := make([]model.GitCommit, 0, len(records))
	for _, record := range records {
		record = strings.Trim(record, "\r\n")
		if record == "" {
			continue
		}
		fields := strings.SplitN(record, "\x1f", 6)
		if len(fields) != 6 {
			return nil, fmt.Errorf("无效 Git 提交记录: 需要 6 个字段，实际 %d 个", len(fields))
		}
		createdAt, err := time.Parse(time.RFC3339, fields[4])
		if err != nil {
			return nil, fmt.Errorf("解析 Git 提交时间 %q 失败: %w", fields[4], err)
		}
		commits = append(commits, model.GitCommit{
			Hash:      fields[0],
			ShortHash: fields[1],
			Author:    fields[2],
			Email:     fields[3],
			CreatedAt: createdAt,
			Subject:   fields[5],
		})
	}
	return commits, nil
}

func sortSnapshot(snapshot *model.GitSnapshot) {
	sort.Slice(snapshot.Files, func(left, right int) bool {
		if snapshot.Files[left].Path == snapshot.Files[right].Path {
			return snapshot.Files[left].OldPath < snapshot.Files[right].OldPath
		}
		return snapshot.Files[left].Path < snapshot.Files[right].Path
	})
	sort.Slice(snapshot.Worktrees, func(left, right int) bool {
		return snapshot.Worktrees[left].Path < snapshot.Worktrees[right].Path
	})
}

func snapshotHash(snapshot model.GitSnapshot) string {
	// 明确列出参与 Hash 的字段，排除 UpdatedAt，确保相同 Git 状态得到相同结果。
	canonical := struct {
		Branch       string           `json:"branch"`
		Head         string           `json:"head"`
		Upstream     string           `json:"upstream"`
		Ahead        int              `json:"ahead"`
		Behind       int              `json:"behind"`
		Modified     int              `json:"modified"`
		Added        int              `json:"added"`
		Deleted      int              `json:"deleted"`
		LinesAdded   int              `json:"linesAdded"`
		LinesDeleted int              `json:"linesDeleted"`
		Untracked    int              `json:"untracked"`
		Conflicted   int              `json:"conflicted"`
		Dirty        bool             `json:"dirty"`
		Files        []model.GitFile  `json:"files"`
		Worktrees    []model.Worktree `json:"worktrees"`
		Error        string           `json:"error"`
	}{
		Branch:       snapshot.Branch,
		Head:         snapshot.Head,
		Upstream:     snapshot.Upstream,
		Ahead:        snapshot.Ahead,
		Behind:       snapshot.Behind,
		Modified:     snapshot.Modified,
		Added:        snapshot.Added,
		Deleted:      snapshot.Deleted,
		LinesAdded:   snapshot.LinesAdded,
		LinesDeleted: snapshot.LinesDeleted,
		Untracked:    snapshot.Untracked,
		Conflicted:   snapshot.Conflicted,
		Dirty:        snapshot.Dirty,
		Files:        snapshot.Files,
		Worktrees:    snapshot.Worktrees,
		Error:        snapshot.Error,
	}

	encoded, err := json.Marshal(canonical)
	if err != nil {
		// 当前模型只包含可序列化标量；这里保留确定性兜底，避免状态采集 panic。
		encoded = []byte(fmt.Sprintf("%#v", canonical))
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
