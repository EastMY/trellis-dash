package api

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func TestCachedGitValueReusesAndCoalescesRequests(t *testing.T) {
	cache := newGitResultCache()
	var loads atomic.Int32
	load := func() (string, error) {
		loads.Add(1)
		return "cached", nil
	}
	for range 2 {
		value, err := cachedGitValue(context.Background(), cache, "same", load)
		if err != nil || value != "cached" {
			t.Fatalf("读取缓存: value=%q err=%v", value, err)
		}
	}
	if loads.Load() != 1 {
		t.Fatalf("相同 key 执行了 %d 次 loader", loads.Load())
	}

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	loads.Store(0)
	const callers = 8
	results := make(chan error, callers)
	var callersDone sync.WaitGroup
	callersDone.Add(callers)
	for range callers {
		go func() {
			defer callersDone.Done()
			value, err := cachedGitValue(context.Background(), cache, "coalesced", func() (int, error) {
				loads.Add(1)
				started <- struct{}{}
				<-release
				return 42, nil
			})
			if err != nil || value != 42 {
				results <- fmt.Errorf("value=%d err=%v", value, err)
			}
		}()
	}
	<-started
	close(release)
	callersDone.Wait()
	close(results)
	for err := range results {
		t.Error(err)
	}
	if loads.Load() != 1 {
		t.Fatalf("并发相同 key 执行了 %d 次 loader", loads.Load())
	}
}

func TestCachedGitValueDoesNotCacheErrors(t *testing.T) {
	cache := newGitResultCache()
	var loads atomic.Int32
	for range 2 {
		_, err := cachedGitValue(context.Background(), cache, "error", func() (string, error) {
			loads.Add(1)
			return "", errors.New("git failed")
		})
		if err == nil {
			t.Fatal("Git 错误不应被吞掉")
		}
	}
	if loads.Load() != 2 {
		t.Fatalf("错误结果被缓存: loads=%d", loads.Load())
	}
}
