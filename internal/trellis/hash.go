package trellis

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"hash"

	"github.com/yunnnn/trellis-dash/internal/model"
)

func hashBytes(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// stableHasher 使用长度前缀编码字段，避免简单拼接产生边界碰撞。
type stableHasher struct {
	h hash.Hash
}

func newStableHasher() *stableHasher {
	return &stableHasher{h: sha256.New()}
}

func (s *stableHasher) add(value any) {
	data, err := json.Marshal(value)
	if err != nil {
		// 当前调用只传入可 JSON 编码的基础模型；保留显式 panic 可尽早发现编程错误。
		panic(err)
	}
	var length [8]byte
	n := uint64(len(data))
	for i := 7; i >= 0; i-- {
		length[i] = byte(n)
		n >>= 8
	}
	_, _ = s.h.Write(length[:])
	_, _ = s.h.Write(data)
}

func (s *stableHasher) sum() string {
	return hex.EncodeToString(s.h.Sum(nil))
}

// taskIndexHash 只汇总会影响单任务读模型的事实，不包含 Session 派生状态。
// Session 变化由独立 revision 驱动前端刷新，避免心跳让任务资源反复重建。
func taskIndexHash(task model.Task, artifacts []model.Artifact, entries []model.ContextEntry) string {
	hasher := newStableHasher()
	hasher.add(struct {
		Key          string
		SourcePath   string
		SourceHash   string
		Archived     bool
		ArchiveMonth string
	}{task.Key, task.SourcePath, task.SourceHash, task.Archived, task.ArchiveMonth})
	for _, artifact := range artifacts {
		hasher.add(struct {
			Kind string
			Path string
			Hash string
		}{artifact.Kind, artifact.Path, artifact.Hash})
	}
	for _, entry := range entries {
		hasher.add(entry)
	}
	return hasher.sum()
}
