package api

import (
	"context"
	"fmt"
	"sync"
	"time"
)

const maxGitCacheEntries = 512

type gitCacheEntry struct {
	ready    chan struct{}
	value    any
	err      error
	created  time.Time
	complete bool
}

// gitResultCache 同时承担有界结果缓存和相同 key 的并发请求合并。
// key 必须包含项目 generation、Git revision 与所有影响结果的请求参数。
type gitResultCache struct {
	mu      sync.Mutex
	entries map[string]*gitCacheEntry
}

func newGitResultCache() *gitResultCache {
	return &gitResultCache{entries: make(map[string]*gitCacheEntry)}
}

func cachedGitValue[T any](
	ctx context.Context,
	cache *gitResultCache,
	key string,
	load func() (T, error),
) (T, error) {
	var zero T
	if cache == nil {
		return load()
	}

	cache.mu.Lock()
	if existing := cache.entries[key]; existing != nil {
		ready := existing.ready
		cache.mu.Unlock()
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-ready:
		}
		if existing.err != nil {
			return zero, existing.err
		}
		value, ok := existing.value.(T)
		if !ok {
			return zero, fmt.Errorf("Git 缓存类型不匹配: %s", key)
		}
		return value, nil
	}
	if len(cache.entries) >= maxGitCacheEntries {
		cache.evictOldestCompleteLocked()
	}
	entry := &gitCacheEntry{ready: make(chan struct{}), created: time.Now()}
	cache.entries[key] = entry
	cache.mu.Unlock()

	value, err := load()
	cache.mu.Lock()
	entry.value = value
	entry.err = err
	entry.complete = true
	if err != nil {
		// Git 故障不缓存，下一次请求仍可在仓库恢复后立即重试。
		delete(cache.entries, key)
	}
	close(entry.ready)
	for len(cache.entries) > maxGitCacheEntries && cache.evictOldestCompleteLocked() {
	}
	cache.mu.Unlock()
	return value, err
}

func (cache *gitResultCache) evictOldestCompleteLocked() bool {
	var oldestKey string
	var oldest time.Time
	for candidateKey, candidate := range cache.entries {
		if !candidate.complete {
			continue
		}
		if oldestKey == "" || candidate.created.Before(oldest) {
			oldestKey, oldest = candidateKey, candidate.created
		}
	}
	if oldestKey == "" {
		return false
	}
	delete(cache.entries, oldestKey)
	return true
}
