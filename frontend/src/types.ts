export type ProjectMode = "observer" | "control";

export interface SystemCapabilities {
  platform: string;
  directoryPicker: boolean;
}

export interface RevisionBundle {
  generation: string;
  day: string;
  tasks: number;
  sessions: number;
  git: number;
  activity: number;
  specs: number;
  agents: number;
  codegraph?: string;
  updatedAt: string;
}

export type CodeGraphDirection = "callers" | "callees";

export interface CodeGraphLanguageStat {
  name: string;
  fileCount: number;
}

export type CodeGraphSyncMode = "sync" | "rebuild";
export type CodeGraphOperationState = "running" | "succeeded" | "failed";

export interface CodeGraphOperation {
  mode: CodeGraphSyncMode;
  state: CodeGraphOperationState;
  startedAt: string;
  finishedAt?: string;
  message?: string;
}

export interface CodeGraphStatus {
  available: boolean;
  reason?: "not_initialized" | "incompatible_schema" | "busy" | "unreadable" | string;
  message?: string;
  revision: string;
  indexedAt?: string;
  fileCount?: number;
  nodeCount?: number;
  edgeCount?: number;
  databaseBytes?: number;
  languages?: CodeGraphLanguageStat[];
  schemaVersions?: number[];
  cliAvailable: boolean;
  operation?: CodeGraphOperation;
}

export interface CodeGraphSymbol {
  id: string;
  kind: string;
  name: string;
  qualifiedName: string;
  filePath: string;
  language: string;
  startLine: number;
  endLine: number;
  signature?: string;
}

export interface CodeGraphStructureEntry {
  id: string;
  type: "directory" | "file" | "symbol";
  name: string;
  path: string;
  language?: string;
  fileCount?: number;
  nodeCount?: number;
  size?: number;
  expandable: boolean;
  symbol?: CodeGraphSymbol;
}

export interface CodeGraphPage<T> {
  items: T[];
  total: number;
  limit: number;
  offset: number;
  hasMore: boolean;
}

export interface CodeGraphRelation {
  id: number;
  kind: string;
  direction: CodeGraphDirection;
  line?: number;
  provenance?: string;
  source: CodeGraphSymbol;
  target: CodeGraphSymbol;
}

export interface CodeGraphRelationPage {
  symbol: CodeGraphSymbol;
  direction: CodeGraphDirection;
  items: CodeGraphRelation[];
  total: number;
  limit: number;
  offset: number;
  hasMore: boolean;
}

export interface Project {
  id: string;
  name: string;
  root: string;
  mode: ProjectMode;
  createdAt: string;
  updatedAt: string;
  indexedAt?: string;
  indexError?: string;
  activeTaskCount: number;
  revisions: RevisionBundle;
}

export interface Subtask {
  id?: string;
  title?: string;
  name?: string;
  status?: string;
  completed?: boolean;
  [key: string]: unknown;
}

export interface Task {
  projectId: string;
  key: string;
  id: string;
  directoryName: string;
  name: string;
  title: string;
  description: string;
  status: string;
  runtimePhase: string;
  devType?: string;
  scope?: string;
  package?: string;
  priority: string;
  creator: string;
  assignee: string;
  createdAt: string;
  completedAt?: string;
  branch?: string;
  baseBranch?: string;
  worktreePath?: string;
  commit?: string;
  prUrl?: string;
  subtasks?: Subtask[] | string | null;
  children?: string[] | string | null;
  parent?: string;
  relatedFiles?: string[] | string | null;
  notes: string;
  meta?: Record<string, unknown> | string | null;
  archived: boolean;
  archiveMonth?: string;
  sourcePath: string;
  modifiedAt: string;
  artifactCount: number;
  contextIssues: number;
  activeSessions: number;
}

export interface TaskPage {
  items: Task[];
  total: number;
  limit: number;
  offset: number;
}

export interface Artifact {
  projectId: string;
  taskKey: string;
  kind: string;
  name: string;
  path: string;
  contentType: string;
  content?: string;
  size: number;
  hash: string;
  modifiedAt: string;
}

export interface ContextEntry {
  projectId: string;
  taskKey: string;
  action: string;
  line: number;
  type: "file" | "directory" | "example" | string;
  file?: string;
  reason?: string;
  example: boolean;
  duplicate: boolean;
  valid: boolean;
  exists: boolean;
  error?: string;
}

export interface Session {
  projectId: string;
  key: string;
  platform: string;
  currentTask: string;
  taskKey?: string;
  lastSeenAt?: string;
  currentRun?: Record<string, unknown> | string | null;
  stale: boolean;
  sourcePath: string;
}

export interface WorkflowState {
  projectId: string;
  name: string;
  label: string;
  order: number;
}

export interface GitFile {
  path: string;
  oldPath?: string;
  index: string;
  worktree: string;
  status: string;
  untracked: boolean;
  conflict: boolean;
}

export interface Worktree {
  path: string;
  head: string;
  branch?: string;
  dirty: boolean;
  taskKey?: string;
  bare: boolean;
  detached: boolean;
  locked?: string;
  prunable?: string;
}

export interface GitSnapshot {
  projectId: string;
  branch: string;
  head: string;
  upstream?: string;
  ahead: number;
  behind: number;
  modified: number;
  added: number;
  deleted: number;
  linesAdded: number;
  linesDeleted: number;
  untracked: number;
  conflicted: number;
  dirty: boolean;
  files: GitFile[];
  worktrees: Worktree[];
  updatedAt: string;
  error?: string;
}

export interface GitSummary {
  projectId: string;
  branch: string;
  head: string;
  upstream?: string;
  ahead: number;
  behind: number;
  modified: number;
  added: number;
  deleted: number;
  linesAdded: number;
  linesDeleted: number;
  untracked: number;
  conflicted: number;
  dirty: boolean;
  worktrees: number;
  updatedAt: string;
  error?: string;
}

export interface GitCommit {
  hash: string;
  shortHash: string;
  author: string;
  email: string;
  subject: string;
  createdAt: string;
}

export interface ActivityEvent {
  id: number;
  projectId: string;
  taskKey?: string;
  type: string;
  source: string;
  payload?: Record<string, unknown> | string | null;
  createdAt: string;
}

export interface ActivityPage {
  items: ActivityEvent[];
  firstId: number;
  lastId: number;
  hasMore: boolean;
}

export interface TaskStatistics {
  total: number;
  active: number;
  archived: number;
  blocked: number;
  completedToday: number;
  byStatus: Record<string, number>;
}

export interface DailyCount {
  date: string;
  count: number;
}

export interface DashboardSnapshot {
  project: Project;
  statistics: TaskStatistics;
  completionTrend: DailyCount[];
  gitCommitTrend: DailyCount[];
  gitCommitTrendAvailable: boolean;
  activeTasks: Task[];
  sessions: Session[];
  git?: GitSummary;
  recentActivity: ActivityEvent[];
}

export type CodexUsageScope = "project" | "all";
export type CodexUsageDays = 7 | 30 | 90;
export type CodexUsageMetric = "tokens" | "cost";

export interface CodexUsageItem {
  date: string;
  tokens: number;
  costUsd: number;
  costBreakdown: CodexUsageCostBreakdown;
  costPartial: boolean;
}

export interface CodexUsageCostBreakdown {
  uncachedInputUsd: number;
  cachedInputUsd: number;
  outputUsd: number;
  cacheWriteUsd: number;
}

export interface CodexUsageResponse {
  scope: CodexUsageScope;
  days: CodexUsageDays;
  dateFrom: string;
  dateTo: string;
  totalTokens: number;
  totalCostUsd: number;
  costPartial: boolean;
  sessionCount: number;
  skippedFiles: number;
  items: CodexUsageItem[];
}

export interface TaskDetail {
  task: Task;
  artifacts: Artifact[];
  context: ContextEntry[];
  sessions: Session[];
  activity: ActivityEvent[];
}

export interface ApiErrorBody {
  code?: string;
  message?: string;
  details?: unknown;
}

export interface ProjectInput {
  name: string;
  root: string;
  mode: ProjectMode;
}
