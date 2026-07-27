package codexusage

import (
	"log/slog"
	"time"
)

const (
	defaultDays         = 30
	defaultMaxFiles     = 20_000
	defaultMaxFileBytes = int64(512 << 20)
	defaultMaxLineBytes = 4 << 20
	cacheVersion        = 1
)

type Scope string

const (
	ScopeProject Scope = "project"
	ScopeAll     Scope = "all"
)

// Query 是统计服务的稳定输入；HTTP 参数校验由 API 层负责。
type Query struct {
	Scope       Scope
	Days        int
	ProjectRoot string
}

type DayItem struct {
	Date          string        `json:"date"`
	Tokens        int64         `json:"tokens"`
	CostUSD       float64       `json:"costUsd"`
	CostBreakdown CostBreakdown `json:"costBreakdown"`
	CostPartial   bool          `json:"costPartial"`
}

// CostBreakdown 是每日费用堆叠图使用的稳定费用分类。
// 当前 Codex 日志未提供缓存写入 Token，因此 CacheWriteUSD 会保持为零，
// 但仍保留该字段，避免前端用总费用反推或混淆费用口径。
type CostBreakdown struct {
	UncachedInputUSD float64 `json:"uncachedInputUsd"`
	CachedInputUSD   float64 `json:"cachedInputUsd"`
	OutputUSD        float64 `json:"outputUsd"`
	CacheWriteUSD    float64 `json:"cacheWriteUsd"`
}

func (cost CostBreakdown) total() float64 {
	return cost.UncachedInputUSD + cost.CachedInputUSD + cost.OutputUSD + cost.CacheWriteUSD
}

func (cost *CostBreakdown) add(other CostBreakdown) {
	cost.UncachedInputUSD += other.UncachedInputUSD
	cost.CachedInputUSD += other.CachedInputUSD
	cost.OutputUSD += other.OutputUSD
	cost.CacheWriteUSD += other.CacheWriteUSD
}

type Summary struct {
	Scope        Scope     `json:"scope"`
	Days         int       `json:"days"`
	DateFrom     string    `json:"dateFrom"`
	DateTo       string    `json:"dateTo"`
	TotalTokens  int64     `json:"totalTokens"`
	TotalCostUSD float64   `json:"totalCostUsd"`
	CostPartial  bool      `json:"costPartial"`
	SessionCount int       `json:"sessionCount"`
	SkippedFiles int       `json:"skippedFiles"`
	Items        []DayItem `json:"items"`
}

type Options struct {
	CodexHome   string
	CachePath   string
	Location    *time.Location
	Now         func() time.Time
	Logger      *slog.Logger
	MaxFiles    int
	MaxFileSize int64
	MaxLineSize int
}

type tokenUsage struct {
	InputTokens           int64 `json:"inputTokens"`
	CachedInputTokens     int64 `json:"cachedInputTokens"`
	OutputTokens          int64 `json:"outputTokens"`
	ReasoningOutputTokens int64 `json:"reasoningOutputTokens"`
	TotalTokens           int64 `json:"totalTokens"`
}

func (u tokenUsage) hasTokens() bool {
	return u.InputTokens != 0 || u.CachedInputTokens != 0 || u.OutputTokens != 0 ||
		u.ReasoningOutputTokens != 0 || u.TotalTokens != 0
}

type tokenEvent struct {
	Timestamp int64      `json:"timestamp"`
	Model     string     `json:"model"`
	Usage     tokenUsage `json:"usage"`
}

type fingerprint struct {
	Size            int64 `json:"size"`
	ModifiedSeconds int64 `json:"modifiedSeconds"`
	ModifiedNanos   int64 `json:"modifiedNanos"`
}

type fileData struct {
	PathKey     string       `json:"path"`
	Fingerprint fingerprint  `json:"fingerprint"`
	SessionID   string       `json:"sessionId,omitempty"`
	CWD         string       `json:"cwd,omitempty"`
	EventCount  int          `json:"eventCount"`
	Events      []tokenEvent `json:"events"`
}

type cacheIndex struct {
	Version int        `json:"version"`
	Files   []fileData `json:"files"`
}
