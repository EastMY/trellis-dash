package trellis

import (
	"fmt"

	"github.com/yunnnn/trellis-dash/internal/model"
)

const (
	// MaxProjectWalkEntries 跨 tasks、research、spec、session 共享，不能按任务重复获得预算。
	MaxProjectWalkEntries = 200_000
	// MaxProjectRawReadBytes 统计实际读取的事实源原始字节，包括空白 JSONL 行。
	MaxProjectRawReadBytes int64 = 512 * 1024 * 1024
)

type scanBudget struct {
	walkEntries int
	rawBytes    int64
}

func (budget *scanBudget) addWalk(label string) error {
	budget.walkEntries++
	if budget.walkEntries > MaxProjectWalkEntries {
		return fmt.Errorf("%w: %s 使项目遍历项超过 %d", ErrResourceLimit, label, MaxProjectWalkEntries)
	}
	return nil
}

func (budget *scanBudget) addRead(size int64, label string) error {
	if size < 0 || budget.rawBytes > MaxProjectRawReadBytes-size {
		return fmt.Errorf("%w: %s 使项目原始读取量超过 %d 字节", ErrResourceLimit, label, MaxProjectRawReadBytes)
	}
	budget.rawBytes += size
	return nil
}

// merge 按任务稳定顺序合并并发扫描的局部预算，避免调度顺序改变资源上限错误。
func (budget *scanBudget) merge(stats model.ScanStats, label string) error {
	if stats.WalkEntries < 0 || budget.walkEntries > MaxProjectWalkEntries-stats.WalkEntries {
		return fmt.Errorf("%w: %s 使项目遍历项超过 %d", ErrResourceLimit, label, MaxProjectWalkEntries)
	}
	if stats.RawBytes < 0 || budget.rawBytes > MaxProjectRawReadBytes-stats.RawBytes {
		return fmt.Errorf("%w: %s 使项目原始读取量超过 %d 字节", ErrResourceLimit, label, MaxProjectRawReadBytes)
	}
	budget.walkEntries += stats.WalkEntries
	budget.rawBytes += stats.RawBytes
	return nil
}

func (budget *scanBudget) stats() model.ScanStats {
	return model.ScanStats{WalkEntries: budget.walkEntries, RawBytes: budget.rawBytes}
}
