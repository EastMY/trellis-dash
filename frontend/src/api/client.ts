import type {
  ActivityPage,
  Artifact,
  DashboardSnapshot,
  GitCommit,
  GitSnapshot,
  Project,
  ProjectInput,
  RevisionBundle,
  Session,
  SystemCapabilities,
  Task,
  TaskDetail,
  TaskPage,
  WorkflowState,
} from "../types";

const API_ROOT = "/api/v1";

/** 统一保留响应体与 ETag，使轮询接口在 304 时仍能返回稳定数据。 */
const responseCache = new Map<string, unknown>();
const etagCache = new Map<string, string>();
const responseCacheSizes = new Map<string, number>();
const MAX_CACHE_ENTRIES = 64;
const MAX_CACHE_ENTRY_BYTES = 4 * 1024 * 1024;
const MAX_CACHE_TOTAL_BYTES = 16 * 1024 * 1024;
let responseCacheBytes = 0;

function deleteCachedResponse(key: string): void {
  responseCache.delete(key);
  etagCache.delete(key);
  responseCacheBytes -= responseCacheSizes.get(key) ?? 0;
  responseCacheSizes.delete(key);
}

function cacheResponse(key: string, value: unknown, etag: string | null, approximateBytes: number): void {
  deleteCachedResponse(key);
  // 没有校验器或响应过大时不建立第二份长期副本，也不会发送无法兑现的 If-None-Match。
  if (!etag || approximateBytes > MAX_CACHE_ENTRY_BYTES) return;
  responseCache.set(key, value);
  etagCache.set(key, etag);
  responseCacheSizes.set(key, approximateBytes);
  responseCacheBytes += approximateBytes;
  while (responseCache.size > MAX_CACHE_ENTRIES || responseCacheBytes > MAX_CACHE_TOTAL_BYTES) {
    const oldest = responseCache.keys().next().value as string | undefined;
    if (!oldest) break;
    deleteCachedResponse(oldest);
  }
}

function cachedResponse<T>(key: string): T | undefined {
  if (!responseCache.has(key)) return undefined;
  const value = responseCache.get(key) as T;
  const etag = etagCache.get(key);
  const size = responseCacheSizes.get(key) ?? 0;
  // Map 的插入顺序作为轻量 LRU；命中后移到队尾。
  responseCache.delete(key);
  etagCache.delete(key);
  responseCacheSizes.delete(key);
  responseCache.set(key, value);
  if (etag) etagCache.set(key, etag);
  responseCacheSizes.set(key, size);
  return value;
}

export class ApiError extends Error {
  readonly status: number;
  readonly code?: string;
  readonly details?: unknown;

  constructor(message: string, status: number, code?: string, details?: unknown) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.details = details;
  }
}

function unwrap<T>(body: unknown): T {
  if (body && typeof body === "object" && "data" in body) {
    return (body as { data: T }).data;
  }
  return body as T;
}

async function parseError(response: Response): Promise<ApiError> {
  let body: {
    message?: string;
    code?: string;
    details?: unknown;
    error?: { message?: string; code?: string; details?: unknown };
  } = {};
  try {
    body = (await response.json()) as typeof body;
  } catch {
    // 非 JSON 错误仍由 HTTP 状态提供可诊断信息。
  }
  const error = body.error ?? body;
  return new ApiError(
    error.message || `请求失败（HTTP ${response.status}）`,
    response.status,
    error.code,
    error.details,
  );
}

async function request<T>(path: string, init: RequestInit = {}, cacheable = false): Promise<T> {
  const method = init.method ?? "GET";
  const key = `${method} ${path}`;
  const headers = new Headers(init.headers);
  headers.set("Accept", "application/json");

  if (init.body && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  if (cacheable) {
    const etag = etagCache.get(key);
    if (etag) headers.set("If-None-Match", etag);
  }

  const response = await fetch(`${API_ROOT}${path}`, { ...init, headers });
  if (response.status === 304) {
    const cached = cachedResponse<T>(key);
    if (cached !== undefined) return cached;
    throw new ApiError("服务返回 304，但本地没有可复用的响应", 304);
  }
  if (!response.ok) throw await parseError(response);

  if (response.status === 204) return undefined as T;
  const rawBody = await response.text();
  let decoded: unknown;
  try {
    decoded = rawBody ? JSON.parse(rawBody) : undefined;
  } catch {
    throw new ApiError("服务返回了无效 JSON", response.status);
  }
  const body = unwrap<T>(decoded);
  if (cacheable) {
    cacheResponse(key, body, response.headers.get("ETag"), rawBody.length * 2);
  }
  return body;
}

async function requestText(path: string): Promise<string> {
  const key = `GET ${path}`;
  const headers = new Headers({ Accept: "text/plain, application/json" });
  const etag = etagCache.get(key);
  if (etag) headers.set("If-None-Match", etag);
  const response = await fetch(`${API_ROOT}${path}`, {
    headers,
  });
  if (response.status === 304) {
    const cached = cachedResponse<string>(key);
    if (cached !== undefined) return cached;
    throw new ApiError("服务返回 304，但本地没有可复用的 Diff", 304);
  }
  if (!response.ok) throw await parseError(response);
  const contentType = response.headers.get("Content-Type") ?? "";
  let content: string;
  if (contentType.includes("application/json")) {
    const value = unwrap<string | { diff?: string; content?: string }>(await response.json());
    content = typeof value === "string" ? value : (value.diff ?? value.content ?? "");
  } else {
    content = await response.text();
  }
  cacheResponse(key, content, response.headers.get("ETag"), content.length * 2);
  return content;
}

function query(params: Record<string, string | number | boolean | undefined>): string {
  const search = new URLSearchParams();
  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== "") search.set(key, String(value));
  });
  const encoded = search.toString();
  return encoded ? `?${encoded}` : "";
}

function normalizeProjects(value: Project[] | { items?: Project[] }): Project[] {
  return Array.isArray(value) ? value : (value.items ?? []);
}

function normalizeTasks(value: TaskPage | Task[]): TaskPage {
  if (Array.isArray(value)) {
    return { items: value, total: value.length, limit: value.length, offset: 0 };
  }
  return { ...value, items: value.items ?? [] };
}

interface TaskQuery {
  archived?: boolean;
  status?: string;
  priority?: string;
  assignee?: string;
  q?: string;
  limit?: number;
  offset?: number;
}

export const api = {
  getSystemCapabilities(): Promise<SystemCapabilities> {
    return request<SystemCapabilities>("/system/capabilities", {}, true);
  },

  async selectDirectory(): Promise<string | undefined> {
    const result = await request<{ path: string } | undefined>("/system/directory-picker", {
      method: "POST",
    });
    return result?.path;
  },

  async listProjects(): Promise<Project[]> {
    return normalizeProjects(await request<Project[] | { items?: Project[] }>("/projects", {}, true));
  },

  async createProject(input: ProjectInput): Promise<Project> {
    const project = await request<Project>("/projects", {
      method: "POST",
      body: JSON.stringify(input),
    });
    clearApiCache();
    return project;
  },

  async deleteProject(projectId: string): Promise<void> {
    await request<void>(`/projects/${encodeURIComponent(projectId)}`, { method: "DELETE" });
    clearApiCache();
  },

  rescanProject(projectId: string): Promise<Project> {
    return request<Project>(`/projects/${encodeURIComponent(projectId)}/rescan`, { method: "POST" });
  },

  getDashboard(projectId: string): Promise<DashboardSnapshot> {
    return request<DashboardSnapshot>(`/projects/${encodeURIComponent(projectId)}/dashboard`, {}, true);
  },

  getRevision(projectId: string): Promise<RevisionBundle> {
    return request<RevisionBundle | { resources: RevisionBundle; updatedAt?: string }>(
      `/projects/${encodeURIComponent(projectId)}/revision`,
      {},
      true,
    ).then((value) => {
      if ("resources" in value) {
        return {
          ...value.resources,
          updatedAt: value.resources.updatedAt || value.updatedAt || new Date(0).toISOString(),
        };
      }
      return value;
    });
  },

  async getTasks(
    projectId: string,
    params: TaskQuery = {},
  ): Promise<TaskPage> {
    const path = `/projects/${encodeURIComponent(projectId)}/tasks${query({
      archived: params.archived,
      status: params.status,
      priority: params.priority,
      assignee: params.assignee,
      q: params.q,
      limit: params.limit,
      offset: params.offset,
    })}`;
    return normalizeTasks(await request<TaskPage | Task[]>(path, {}, true));
  },

  getTask(projectId: string, taskKey: string): Promise<TaskDetail> {
    return request<TaskDetail>(
      `/projects/${encodeURIComponent(projectId)}/tasks/${encodeURIComponent(taskKey)}`,
      {},
      true,
    );
  },

  getArtifact(projectId: string, taskKey: string, path: string): Promise<Artifact> {
    return request<Artifact>(
      `/projects/${encodeURIComponent(projectId)}/tasks/${encodeURIComponent(taskKey)}/artifact${query({ path })}`,
      {},
      true,
    );
  },

  async getSessions(projectId: string): Promise<Session[]> {
    const result = await request<Session[] | { items?: Session[] }>(
      `/projects/${encodeURIComponent(projectId)}/sessions`,
      {},
      true,
    );
    return Array.isArray(result) ? result : (result.items ?? []);
  },

  getGitStatus(projectId: string): Promise<GitSnapshot> {
    return request<GitSnapshot>(`/projects/${encodeURIComponent(projectId)}/git/status`, {}, true);
  },

  async getGitCommits(projectId: string): Promise<GitCommit[]> {
    const result = await request<GitCommit[] | { items?: GitCommit[] }>(
      `/projects/${encodeURIComponent(projectId)}/git/commits`,
      {},
      true,
    );
    return Array.isArray(result) ? result : (result.items ?? []);
  },

  getGitDiff(projectId: string, path?: string, staged = false): Promise<string> {
    return requestText(`/projects/${encodeURIComponent(projectId)}/git/diff${query({ path, staged: staged || undefined })}`);
  },

  pushGit(projectId: string): Promise<{ branch: string; upstream: string; refreshed: boolean; warning?: string }> {
    return request(`/projects/${encodeURIComponent(projectId)}/git/push`, { method: "POST" });
  },

  getActivity(projectId: string, afterId = 0, limit = 100, beforeId = 0): Promise<ActivityPage> {
    return request<ActivityPage>(
      `/projects/${encodeURIComponent(projectId)}/activity${query({ afterId: afterId || undefined, beforeId: beforeId || undefined, limit })}`,
      {},
      true,
    );
  },

  async getWorkflowStates(projectId: string): Promise<WorkflowState[]> {
    const result = await request<WorkflowState[] | { items?: WorkflowState[] }>(
      `/projects/${encodeURIComponent(projectId)}/workflow-states`,
      {},
      true,
    );
    return Array.isArray(result) ? result : (result.items ?? []);
  },
};

export function clearApiCache(): void {
  responseCache.clear();
  etagCache.clear();
  responseCacheSizes.clear();
  responseCacheBytes = 0;
}
