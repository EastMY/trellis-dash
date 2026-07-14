package store

import (
	"testing"
	"time"
)

func TestProjectLocksOnlySerializeSameProject(t *testing.T) {
	store := &Store{}
	unlockA := store.lockProject("a")

	otherProject := make(chan struct{})
	go func() {
		unlock := store.lockProject("b")
		unlock()
		close(otherProject)
	}()
	select {
	case <-otherProject:
	case <-time.After(time.Second):
		t.Fatal("不同项目不应争用同一把 Go 写锁")
	}

	sameProject := make(chan struct{})
	go func() {
		unlock := store.lockProject("a")
		unlock()
		close(sameProject)
	}()
	select {
	case <-sameProject:
		t.Fatal("同一项目写入必须保持串行")
	case <-time.After(30 * time.Millisecond):
	}
	unlockA()
	select {
	case <-sameProject:
	case <-time.After(time.Second):
		t.Fatal("释放项目锁后等待写入未继续")
	}
}
