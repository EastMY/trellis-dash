package codegraph

import "time"

const (
	DefaultStructureLimit = 100
	MaxStructureLimit     = 200
	DefaultSearchLimit    = 30
	MaxSearchLimit        = 100
	DefaultRelationLimit  = 20
	MaxRelationLimit      = 50
	MaxQueryLength        = 200
)

// Direction 表示代码关系相对当前符号的方向。
type Direction string

const (
	DirectionCallers Direction = "callers"
	DirectionCallees Direction = "callees"
)

// LanguageStat 是按文件数量聚合的语言统计。
type LanguageStat struct {
	Name      string `json:"name"`
	FileCount int    `json:"fileCount"`
}

// SyncMode 是 Dashboard 允许触发的固定 CodeGraph 操作，不接受任意 CLI 参数。
type SyncMode string

const (
	SyncModeIncremental SyncMode = "sync"
	SyncModeRebuild     SyncMode = "rebuild"
)

// OperationState 描述后台 CodeGraph 操作的生命周期。
type OperationState string

const (
	OperationRunning   OperationState = "running"
	OperationSucceeded OperationState = "succeeded"
	OperationFailed    OperationState = "failed"
)

// Operation 是单个项目当前或最近一次 CodeGraph 后台操作。
type Operation struct {
	Mode       SyncMode       `json:"mode"`
	State      OperationState `json:"state"`
	StartedAt  time.Time      `json:"startedAt"`
	FinishedAt *time.Time     `json:"finishedAt,omitempty"`
	Message    string         `json:"message,omitempty"`
}

// Status 描述一个项目 CodeGraph 索引及其受控同步能力。
type Status struct {
	Available      bool           `json:"available"`
	Reason         string         `json:"reason,omitempty"`
	Message        string         `json:"message,omitempty"`
	Revision       string         `json:"revision"`
	IndexedAt      *time.Time     `json:"indexedAt,omitempty"`
	FileCount      int            `json:"fileCount,omitempty"`
	NodeCount      int            `json:"nodeCount,omitempty"`
	EdgeCount      int            `json:"edgeCount,omitempty"`
	DatabaseBytes  int64          `json:"databaseBytes,omitempty"`
	Languages      []LanguageStat `json:"languages,omitempty"`
	SchemaVersions []int          `json:"schemaVersions,omitempty"`
	CLIAvailable   bool           `json:"cliAvailable"`
	Operation      *Operation     `json:"operation,omitempty"`
}

// Symbol 是对外稳定的符号投影，不暴露 CodeGraph 的内部表结构。
type Symbol struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	QualifiedName string `json:"qualifiedName"`
	FilePath      string `json:"filePath"`
	Language      string `json:"language"`
	StartLine     int    `json:"startLine"`
	EndLine       int    `json:"endLine"`
	Signature     string `json:"signature,omitempty"`
}

// StructureEntry 是目录、文件或符号组成的懒加载结构节点。
type StructureEntry struct {
	ID         string  `json:"id"`
	Type       string  `json:"type"`
	Name       string  `json:"name"`
	Path       string  `json:"path"`
	Language   string  `json:"language,omitempty"`
	FileCount  int     `json:"fileCount,omitempty"`
	NodeCount  int     `json:"nodeCount,omitempty"`
	Size       int64   `json:"size,omitempty"`
	Expandable bool    `json:"expandable"`
	Symbol     *Symbol `json:"symbol,omitempty"`
}

// Relation 保留一跳关系的方向和两端符号，避免前端追加 N+1 查询。
type Relation struct {
	ID         int64  `json:"id"`
	Kind       string `json:"kind"`
	Direction  string `json:"direction"`
	Line       int    `json:"line,omitempty"`
	Provenance string `json:"provenance,omitempty"`
	Source     Symbol `json:"source"`
	Target     Symbol `json:"target"`
}

// Page 是 CodeGraph 所有有界列表接口的统一分页契约。
type Page[T any] struct {
	Items   []T  `json:"items"`
	Total   int  `json:"total"`
	Limit   int  `json:"limit"`
	Offset  int  `json:"offset"`
	HasMore bool `json:"hasMore"`
}

// RelationPage 返回中心符号及一个方向的一跳代码关系。
type RelationPage struct {
	Symbol    Symbol     `json:"symbol"`
	Direction Direction  `json:"direction"`
	Items     []Relation `json:"items"`
	Total     int        `json:"total"`
	Limit     int        `json:"limit"`
	Offset    int        `json:"offset"`
	HasMore   bool       `json:"hasMore"`
}
