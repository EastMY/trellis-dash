package store

const schemaSQL = `
CREATE TABLE IF NOT EXISTS cache_metadata (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

INSERT OR IGNORE INTO cache_metadata(key, value)
VALUES ('generation', lower(hex(randomblob(16))));

CREATE TABLE IF NOT EXISTS projects (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    root        TEXT NOT NULL UNIQUE,
    mode        TEXT NOT NULL DEFAULT 'observer',
    generation  TEXT NOT NULL DEFAULT (lower(hex(randomblob(16)))),
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    indexed_at  TEXT,
    index_error TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS resource_revisions (
    project_id    TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    resource_type TEXT NOT NULL,
    revision      INTEGER NOT NULL DEFAULT 0,
    updated_at    TEXT NOT NULL,
    PRIMARY KEY (project_id, resource_type)
);

CREATE TABLE IF NOT EXISTS resource_states (
    project_id    TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    resource_type TEXT NOT NULL,
    content_hash  TEXT NOT NULL,
    PRIMARY KEY (project_id, resource_type)
);

CREATE TABLE IF NOT EXISTS tasks (
    project_id       TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    task_key         TEXT NOT NULL,
    task_id          TEXT NOT NULL,
    directory_name   TEXT NOT NULL,
    name             TEXT NOT NULL,
    title            TEXT NOT NULL,
    description      TEXT NOT NULL,
    status           TEXT NOT NULL,
    runtime_phase    TEXT NOT NULL,
    dev_type         TEXT,
    scope            TEXT,
    package_name     TEXT,
    priority         TEXT NOT NULL,
    creator          TEXT NOT NULL,
    assignee         TEXT NOT NULL,
    created_at       TEXT NOT NULL,
    completed_at     TEXT,
    branch           TEXT,
    base_branch      TEXT,
    worktree_path    TEXT,
    commit_hash      TEXT,
    pr_url           TEXT,
    subtasks_json    TEXT NOT NULL DEFAULT '[]',
    children_json    TEXT NOT NULL DEFAULT '[]',
    parent_id        TEXT,
    related_files_json TEXT NOT NULL DEFAULT '[]',
    notes            TEXT NOT NULL,
    meta_json        TEXT NOT NULL DEFAULT '{}',
    archived         INTEGER NOT NULL DEFAULT 0,
    archive_month    TEXT NOT NULL DEFAULT '',
    source_path      TEXT NOT NULL,
    source_hash      TEXT NOT NULL,
    index_hash       TEXT NOT NULL DEFAULT '',
    modified_at      TEXT NOT NULL,
    artifact_count   INTEGER NOT NULL DEFAULT 0,
    context_issues   INTEGER NOT NULL DEFAULT 0,
    active_sessions  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (project_id, task_key)
);

CREATE INDEX IF NOT EXISTS idx_tasks_project_status
    ON tasks(project_id, archived, status, modified_at DESC);
CREATE INDEX IF NOT EXISTS idx_tasks_assignee
    ON tasks(project_id, assignee, priority);

CREATE TABLE IF NOT EXISTS task_artifacts (
    project_id   TEXT NOT NULL,
    task_key     TEXT NOT NULL,
    kind         TEXT NOT NULL,
    name         TEXT NOT NULL,
    path         TEXT NOT NULL,
    content_type TEXT NOT NULL,
    content      TEXT NOT NULL,
    size         INTEGER NOT NULL,
    hash         TEXT NOT NULL,
    modified_at  TEXT NOT NULL,
    PRIMARY KEY (project_id, task_key, path),
    FOREIGN KEY (project_id, task_key) REFERENCES tasks(project_id, task_key) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS task_context_entries (
    project_id TEXT NOT NULL,
    task_key   TEXT NOT NULL,
    action     TEXT NOT NULL,
    line_no    INTEGER NOT NULL,
    file_path  TEXT NOT NULL,
    reason     TEXT NOT NULL,
	entry_type  TEXT NOT NULL DEFAULT 'file',
    is_example INTEGER NOT NULL DEFAULT 0,
	is_duplicate INTEGER NOT NULL DEFAULT 0,
    is_valid   INTEGER NOT NULL DEFAULT 0,
    file_exists INTEGER NOT NULL DEFAULT 0,
    error      TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (project_id, task_key, action, line_no),
    FOREIGN KEY (project_id, task_key) REFERENCES tasks(project_id, task_key) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS runtime_sessions (
    project_id   TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    session_key  TEXT NOT NULL,
    platform     TEXT NOT NULL,
    current_task TEXT NOT NULL,
    task_key     TEXT NOT NULL,
    last_seen_at TEXT,
    current_run_json TEXT NOT NULL DEFAULT 'null',
    stale        INTEGER NOT NULL DEFAULT 0,
    source_path  TEXT NOT NULL,
    source_hash  TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (project_id, session_key)
);

CREATE TABLE IF NOT EXISTS workflow_states (
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    label      TEXT NOT NULL,
    sort_order INTEGER NOT NULL,
    PRIMARY KEY (project_id, name)
);

CREATE TABLE IF NOT EXISTS git_snapshots (
    project_id   TEXT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    snapshot_json TEXT NOT NULL,
    summary_json TEXT NOT NULL DEFAULT '{}',
    content_hash TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS activity_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    task_key    TEXT NOT NULL DEFAULT '',
    event_type  TEXT NOT NULL,
    source      TEXT NOT NULL,
    payload_json TEXT NOT NULL DEFAULT '{}',
    created_at  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_activity_project_id
    ON activity_events(project_id, id DESC);
CREATE INDEX IF NOT EXISTS idx_activity_project_task_id
    ON activity_events(project_id, task_key, id DESC);

CREATE TABLE IF NOT EXISTS index_errors (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    source_path TEXT NOT NULL,
    message     TEXT NOT NULL,
    created_at  TEXT NOT NULL
);
`
