package store

import "sync"

// lockProject 返回对应项目写锁的释放函数。同项目写入保持原有顺序，
// 不同项目不再在 Go 层争用一把全局互斥锁。
func (s *Store) lockProject(projectID string) func() {
	// 共享生命周期锁不串行写入，只确保 Close 会等待已经开始的项目事务结束。
	s.lifecycleMu.RLock()
	value, _ := s.projectLocks.LoadOrStore(projectID, &sync.Mutex{})
	lock := value.(*sync.Mutex)
	lock.Lock()
	return func() {
		lock.Unlock()
		s.lifecycleMu.RUnlock()
	}
}
