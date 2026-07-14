package trellis

import (
	"errors"
	"testing"
)

func TestScanBudgetEnforcesSharedProjectLimits(t *testing.T) {
	budget := &scanBudget{walkEntries: MaxProjectWalkEntries - 1}
	if err := budget.addWalk("first"); err != nil {
		t.Fatal(err)
	}
	if err := budget.addWalk("second"); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("遍历预算错误 = %v，期望 ErrResourceLimit", err)
	}

	budget = &scanBudget{rawBytes: MaxProjectRawReadBytes - 1}
	if err := budget.addRead(1, "first"); err != nil {
		t.Fatal(err)
	}
	if err := budget.addRead(1, "second"); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("读取预算错误 = %v，期望 ErrResourceLimit", err)
	}
}
