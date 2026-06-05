-- Local Symphony App v1 app-level SQLite schema
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS schema_meta (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

INSERT OR IGNORE INTO schema_meta(key, value) VALUES ('schema_name', 'v1_app');
INSERT OR IGNORE INTO schema_meta(key, value) VALUES ('schema_version', '1');

CREATE TABLE IF NOT EXISTS projects (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  repo_root TEXT NOT NULL UNIQUE,
  project_db_path TEXT NOT NULL,
  workflow_path TEXT NOT NULL,
  issue_prefix TEXT NOT NULL DEFAULT 'LOC',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  last_opened_at TEXT
);

CREATE TABLE IF NOT EXISTS app_settings (
  key TEXT PRIMARY KEY,
  value_json TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS local_sessions (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('cli','browser','desktop')),
  token_hash TEXT NOT NULL UNIQUE,
  csrf_hash TEXT,
  user_label TEXT,
  created_at TEXT NOT NULL,
  last_seen_at TEXT,
  expires_at TEXT,
  revoked_at TEXT,
  FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_local_sessions_project_kind ON local_sessions(project_id, kind);
CREATE INDEX IF NOT EXISTS idx_local_sessions_token_hash ON local_sessions(token_hash);

CREATE TABLE IF NOT EXISTS open_tokens (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL,
  token_hash TEXT NOT NULL UNIQUE,
  expires_at TEXT NOT NULL,
  consumed_at TEXT,
  created_at TEXT NOT NULL,
  FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_open_tokens_project ON open_tokens(project_id, expires_at);
CREATE INDEX IF NOT EXISTS idx_open_tokens_hash ON open_tokens(token_hash);

CREATE TABLE IF NOT EXISTS runtime_descriptors (
  project_id TEXT PRIMARY KEY,
  api_url TEXT NOT NULL,
  tool_gateway_endpoint TEXT NOT NULL,
  daemon_pid INTEGER NOT NULL,
  owner_nonce TEXT NOT NULL,
  heartbeat_at INTEGER NOT NULL,
  heartbeat_ttl_ms INTEGER NOT NULL DEFAULT 30000,
  acquired_at INTEGER NOT NULL,
  started_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS runtime_owner_events (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL,
  event_type TEXT NOT NULL,
  actor_type TEXT NOT NULL,
  data_json TEXT NOT NULL,
  redacted INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_runtime_owner_events_project ON runtime_owner_events(project_id, created_at);
