package model

import (
	"encoding/json"
	"time"
)

// ResourceType 是前端增量轮询使用的资源版本分类。
type ResourceType string

const (
	ResourceTasks    ResourceType = "tasks"
	ResourceSessions ResourceType = "sessions"
	ResourceGit      ResourceType = "git"
	ResourceActivity ResourceType = "activity"
	ResourceSpecs    ResourceType = "specs"
	ResourceAgents   ResourceType = "agents"
)

// ProjectMode 明确区分默认观察模式和未来可选的完整控制模式。
type ProjectMode string

const (
	ProjectModeObserver ProjectMode = "observer"
	ProjectModeControl  ProjectMode = "control"
)

// Project 表示一个已注册的 Trellis 项目。Root 始终保存清理后的绝对路径。
type Project struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Root       string      `json:"root"`
	Mode       ProjectMode `json:"mode"`
	CreatedAt  time.Time   `json:"createdAt"`
	UpdatedAt  time.Time   `json:"updatedAt"`
	IndexedAt  *time.Time  `json:"indexedAt,omitempty"`
	IndexError string      `json:"indexError,omitempty"`
	// ActiveTaskCount 沿用概览统计口径，仅统计尚未归档的任务。
	ActiveTaskCount int            `json:"activeTaskCount"`
	Revisions       RevisionBundle `json:"revisions"`
}

// RevisionBundle 是一个项目所有资源的单调递增版本集合。
type RevisionBundle struct {
	Generation string    `json:"generation"`
	Day        string    `json:"day"`
	Tasks      int64     `json:"tasks"`
	Sessions   int64     `json:"sessions"`
	Git        int64     `json:"git"`
	Activity   int64     `json:"activity"`
	Specs      int64     `json:"specs"`
	Agents     int64     `json:"agents"`
	Updated    time.Time `json:"updatedAt"`
}

// Task 保留 Trellis task.json 的原始字段，同时补充 Dashboard 派生信息。
type Task struct {
	ProjectID    string          `json:"projectId"`
	Key          string          `json:"key"`
	ID           string          `json:"id"`
	Directory    string          `json:"directoryName"`
	Name         string          `json:"name"`
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	Status       string          `json:"status"`
	RuntimePhase string          `json:"runtimePhase"`
	DevType      *string         `json:"devType,omitempty"`
	Scope        *string         `json:"scope,omitempty"`
	Package      *string         `json:"package,omitempty"`
	Priority     string          `json:"priority"`
	Creator      string          `json:"creator"`
	Assignee     string          `json:"assignee"`
	CreatedAt    string          `json:"createdAt"`
	CompletedAt  *string         `json:"completedAt,omitempty"`
	Branch       *string         `json:"branch,omitempty"`
	BaseBranch   *string         `json:"baseBranch,omitempty"`
	WorktreePath *string         `json:"worktreePath,omitempty"`
	Commit       *string         `json:"commit,omitempty"`
	PRURL        *string         `json:"prUrl,omitempty"`
	Subtasks     json.RawMessage `json:"subtasks"`
	Children     json.RawMessage `json:"children"`
	Parent       *string         `json:"parent,omitempty"`
	RelatedFiles json.RawMessage `json:"relatedFiles"`
	Notes        string          `json:"notes"`
	Meta         json.RawMessage `json:"meta"`
	Archived     bool            `json:"archived"`
	ArchiveMonth string          `json:"archiveMonth,omitempty"`
	SourcePath   string          `json:"sourcePath"`
	SourceHash   string          `json:"-"`
	// IndexHash 汇总 task.json、文档和 Context 指纹，只用于判断单任务是否需要重写缓存。
	IndexHash      string    `json:"-"`
	ModifiedAt     time.Time `json:"modifiedAt"`
	ArtifactCount  int       `json:"artifactCount"`
	ContextIssues  int       `json:"contextIssues"`
	ActiveSessions int       `json:"activeSessions"`
}

// Artifact 是任务目录中的可阅读文档或研究材料。
type Artifact struct {
	ProjectID   string    `json:"projectId"`
	TaskKey     string    `json:"taskKey"`
	Kind        string    `json:"kind"`
	Name        string    `json:"name"`
	Path        string    `json:"path"`
	ContentType string    `json:"contentType"`
	Content     string    `json:"content,omitempty"`
	Size        int64     `json:"size"`
	Hash        string    `json:"hash"`
	ModifiedAt  time.Time `json:"modifiedAt"`
}

// ContextEntry 表示 implement.jsonl 或 check.jsonl 中的一行。
type ContextEntry struct {
	ProjectID string `json:"projectId"`
	TaskKey   string `json:"taskKey"`
	Action    string `json:"action"`
	Line      int    `json:"line"`
	Type      string `json:"type"`
	File      string `json:"file,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Example   bool   `json:"example"`
	Duplicate bool   `json:"duplicate"`
	Valid     bool   `json:"valid"`
	Exists    bool   `json:"exists"`
	Error     string `json:"error,omitempty"`
}

// Session 对应 .trellis/.runtime/sessions 下的会话指针。
type Session struct {
	ProjectID   string          `json:"projectId"`
	Key         string          `json:"key"`
	Platform    string          `json:"platform"`
	CurrentTask string          `json:"currentTask"`
	TaskKey     string          `json:"taskKey,omitempty"`
	LastSeenAt  *time.Time      `json:"lastSeenAt,omitempty"`
	CurrentRun  json.RawMessage `json:"currentRun,omitempty"`
	Stale       bool            `json:"stale"`
	SourcePath  string          `json:"sourcePath"`
	// SourceHash 只参与增量索引，不暴露给前端。
	SourceHash string `json:"-"`
}

// WorkflowState 来自 workflow.md 中的 [workflow-state:name] 标记。
type WorkflowState struct {
	ProjectID string `json:"projectId"`
	Name      string `json:"name"`
	Label     string `json:"label"`
	Order     int    `json:"order"`
}

// TrellisSnapshot 是一次完整且可原子替换的文件系统读取结果。
type TrellisSnapshot struct {
	Tasks          []Task          `json:"tasks"`
	Artifacts      []Artifact      `json:"artifacts"`
	ContextEntries []ContextEntry  `json:"contextEntries"`
	Sessions       []Session       `json:"sessions"`
	WorkflowStates []WorkflowState `json:"workflowStates"`
	TasksHash      string          `json:"tasksHash"`
	SessionsHash   string          `json:"sessionsHash"`
	SpecsHash      string          `json:"specsHash"`
	Stats          ScanStats       `json:"-"`
}

// ScanStats 记录一次扫描实际遍历与读取规模，供结构化性能日志使用。
type ScanStats struct {
	WalkEntries int
	RawBytes    int64
}

// TaskBundle 是单任务增量扫描的完整缓存单元。
type TaskBundle struct {
	Task           Task
	Artifacts      []Artifact
	ContextEntries []ContextEntry
	Stats          ScanStats
}

// TaskRuntimeState 是 Session 对任务列表产生的轻量派生状态。
type TaskRuntimeState struct {
	RuntimePhase   string
	ActiveSessions int
}

// SessionIndexSnapshot 是只扫描 Session 资源的结果。
type SessionIndexSnapshot struct {
	Sessions  []Session
	TaskState map[string]TaskRuntimeState
	Hash      string
	Stats     ScanStats
}

// GitFile 表示 porcelain 状态中的单个工作区文件。
type GitFile struct {
	Path      string `json:"path"`
	OldPath   string `json:"oldPath,omitempty"`
	Index     string `json:"index"`
	Worktree  string `json:"worktree"`
	Status    string `json:"status"`
	Untracked bool   `json:"untracked"`
	Conflict  bool   `json:"conflict"`
}

// Worktree 表示 git worktree list --porcelain 的一个记录。
type Worktree struct {
	Path     string `json:"path"`
	Head     string `json:"head"`
	Branch   string `json:"branch,omitempty"`
	Dirty    bool   `json:"dirty"`
	TaskKey  string `json:"taskKey,omitempty"`
	Bare     bool   `json:"bare"`
	Detached bool   `json:"detached"`
	Locked   string `json:"locked,omitempty"`
	Prunable string `json:"prunable,omitempty"`
}

// GitSnapshot 是可以比较 Hash 的 Git 工作区只读快照。
type GitSnapshot struct {
	ProjectID    string     `json:"projectId"`
	Branch       string     `json:"branch"`
	Head         string     `json:"head"`
	Upstream     string     `json:"upstream,omitempty"`
	Ahead        int        `json:"ahead"`
	Behind       int        `json:"behind"`
	Modified     int        `json:"modified"`
	Added        int        `json:"added"`
	Deleted      int        `json:"deleted"`
	LinesAdded   int        `json:"linesAdded"`
	LinesDeleted int        `json:"linesDeleted"`
	Untracked    int        `json:"untracked"`
	Conflicted   int        `json:"conflicted"`
	Dirty        bool       `json:"dirty"`
	Files        []GitFile  `json:"files"`
	Worktrees    []Worktree `json:"worktrees"`
	Hash         string     `json:"-"`
	UpdatedAt    time.Time  `json:"updatedAt"`
	Error        string     `json:"error,omitempty"`
}

// GitSummary 是概览页需要的轻量 Git 表示，不包含文件与 Worktree 明细。
type GitSummary struct {
	ProjectID    string    `json:"projectId"`
	Branch       string    `json:"branch"`
	Head         string    `json:"head"`
	Upstream     string    `json:"upstream,omitempty"`
	Ahead        int       `json:"ahead"`
	Behind       int       `json:"behind"`
	Modified     int       `json:"modified"`
	Added        int       `json:"added"`
	Deleted      int       `json:"deleted"`
	LinesAdded   int       `json:"linesAdded"`
	LinesDeleted int       `json:"linesDeleted"`
	Untracked    int       `json:"untracked"`
	Conflicted   int       `json:"conflicted"`
	Dirty        bool      `json:"dirty"`
	Worktrees    int       `json:"worktrees"`
	UpdatedAt    time.Time `json:"updatedAt"`
	Error        string    `json:"error,omitempty"`
}

// Summary 返回与完整快照同一时刻生成的轻量概览数据。
func (snapshot GitSnapshot) Summary() GitSummary {
	return GitSummary{
		ProjectID: snapshot.ProjectID,
		Branch:    snapshot.Branch, Head: snapshot.Head, Upstream: snapshot.Upstream,
		Ahead: snapshot.Ahead, Behind: snapshot.Behind,
		Modified: snapshot.Modified, Added: snapshot.Added, Deleted: snapshot.Deleted,
		LinesAdded: snapshot.LinesAdded, LinesDeleted: snapshot.LinesDeleted,
		Untracked: snapshot.Untracked, Conflicted: snapshot.Conflicted, Dirty: snapshot.Dirty,
		Worktrees: len(snapshot.Worktrees), UpdatedAt: snapshot.UpdatedAt, Error: snapshot.Error,
	}
}

// GitCommit 是最近提交接口的稳定响应模型。
type GitCommit struct {
	Hash      string    `json:"hash"`
	ShortHash string    `json:"shortHash"`
	Author    string    `json:"author"`
	Email     string    `json:"email"`
	Subject   string    `json:"subject"`
	CreatedAt time.Time `json:"createdAt"`
}

// ActivityEvent 记录有意义的状态变化，而不是每次轮询。
type ActivityEvent struct {
	ID        int64           `json:"id"`
	ProjectID string          `json:"projectId"`
	TaskKey   string          `json:"taskKey,omitempty"`
	Type      string          `json:"type"`
	Source    string          `json:"source"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	CreatedAt time.Time       `json:"createdAt"`
}

// DashboardSnapshot 聚合概览页首屏所需数据，减少串行请求。
type DashboardSnapshot struct {
	Project                 Project           `json:"project"`
	Statistics              TaskStatistics    `json:"statistics"`
	CompletionTrend         []DailyCompletion `json:"completionTrend"`
	GitCommitTrend          []DailyCompletion `json:"gitCommitTrend"`
	GitCommitTrendAvailable bool              `json:"gitCommitTrendAvailable"`
	ActiveTasks             []Task            `json:"activeTasks"`
	Sessions                []Session         `json:"sessions"`
	Git                     *GitSummary       `json:"git,omitempty"`
	RecentActivity          []ActivityEvent   `json:"recentActivity"`
}

// DailyCompletion 表示某个自然日对应的计数，可用于任务完成或 Git 提交趋势。
type DailyCompletion struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

type TaskStatistics struct {
	Total          int            `json:"total"`
	Active         int            `json:"active"`
	Archived       int            `json:"archived"`
	Blocked        int            `json:"blocked"`
	CompletedToday int            `json:"completedToday"`
	ByStatus       map[string]int `json:"byStatus"`
}

type TaskFilter struct {
	Archived *bool
	Status   string
	Priority string
	Assignee string
	Query    string
	Limit    int
	Offset   int
}

type TaskPage struct {
	Items  []Task `json:"items"`
	Total  int    `json:"total"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}

type ActivityPage struct {
	Items   []ActivityEvent `json:"items"`
	FirstID int64           `json:"firstId"`
	LastID  int64           `json:"lastId"`
	HasMore bool            `json:"hasMore"`
}
