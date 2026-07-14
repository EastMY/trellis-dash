package trellis

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/yunnnn/trellis-dash/internal/model"
)

// 只把独占一行的标记视为状态定义，避免把正文中的说明和交叉引用重复收进看板。
var workflowStatePattern = regexp.MustCompile(`(?m)^[\t ]*\[workflow-state:([A-Za-z0-9][A-Za-z0-9_-]*)\][\t ]*$`)

func scanWorkflowStates(
	ctx context.Context,
	root, trellisRoot, projectID string,
	budget *scanBudget,
) ([]model.WorkflowState, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	path := filepath.Join(trellisRoot, "workflow.md")
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return make([]model.WorkflowState, 0), nil
	}
	if err != nil {
		return nil, err
	}
	content, _, err := readSafeFile(root, path, MaxMarkdownBytes)
	if err != nil {
		return nil, err
	}
	if err := budget.addRead(int64(len(content)), "workflow.md"); err != nil {
		return nil, err
	}
	if err := contextError(ctx); err != nil {
		return nil, err
	}

	// 标准模板会在教程代码围栏中展示 [workflow-state:my-status]，示例不能成为真实看板列。
	content, err = markdownOutsideFences(ctx, content)
	if err != nil {
		return nil, err
	}
	matches := workflowStatePattern.FindAllSubmatch(content, -1)
	states := make([]model.WorkflowState, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		name := string(match[1])
		if !isTaskWorkflowState(name) {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		states = append(states, model.WorkflowState{
			ProjectID: projectID,
			Name:      name,
			Label:     workflowStateLabel(name),
			Order:     len(states),
		})
	}
	return states, nil
}

func markdownOutsideFences(ctx context.Context, content []byte) ([]byte, error) {
	lines := strings.Split(string(content), "\n")
	result := make([]string, 0, len(lines))
	var fence byte
	var fenceLength int
	for index, line := range lines {
		if index%256 == 0 {
			if err := contextError(ctx); err != nil {
				return nil, err
			}
		}
		trimmed := strings.TrimLeft(line, " \t")
		indent := len(line) - len(trimmed)
		marker, length := markdownFence(trimmed)
		if fence == 0 {
			if indent <= 3 && length >= 3 {
				fence, fenceLength = marker, length
				continue
			}
			result = append(result, line)
			continue
		}
		if indent <= 3 && marker == fence && length >= fenceLength && strings.Trim(strings.TrimLeft(trimmed, string(fence)), " \t") == "" {
			fence, fenceLength = 0, 0
		}
	}
	return []byte(strings.Join(result, "\n")), nil
}

func markdownFence(trimmed string) (byte, int) {
	if trimmed == "" || (trimmed[0] != '`' && trimmed[0] != '~') {
		return 0, 0
	}
	marker := trimmed[0]
	length := 0
	for length < len(trimmed) && trimmed[length] == marker {
		length++
	}
	return marker, length
}

// no_task 与 *-inline 是 AI 会话注入状态，不会写入 task.json.status，
// 因此不能成为任务看板列；其他名称一律保留以支持项目自定义状态。
func isTaskWorkflowState(name string) bool {
	if name == "no_task" {
		return false
	}
	return !strings.HasSuffix(name, "-inline") && !strings.HasSuffix(name, "_inline")
}

func workflowStateLabel(name string) string {
	words := strings.FieldsFunc(name, func(r rune) bool { return r == '_' || r == '-' })
	for i, word := range words {
		runes := []rune(strings.ToLower(word))
		if len(runes) > 0 {
			runes[0] = unicode.ToUpper(runes[0])
		}
		words[i] = string(runes)
	}
	return strings.Join(words, " ")
}
