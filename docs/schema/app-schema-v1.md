# App DB Schema v1

Path:

```text
~/.symphony/app.db
```

## SQL

```sql
CREATE TABLE schema_version (
  version INTEGER NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE registered_projects (
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

CREATE INDEX idx_registered_projects_last_opened
ON registered_projects(last_opened_at);

CREATE TABLE app_settings (
  key TEXT PRIMARY KEY,
  value_json TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE local_sessions (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL CHECK (kind IN ('browser', 'cli', 'desktop')),
  token_hash TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL,
  last_seen_at TEXT,
  expires_at TEXT NOT NULL,
  revoked_at TEXT
);
```

## Rules

```text
schema_version must equal 1.
registered_projects.repo_root is absolute path.
Only token hashes are stored.
No third-party secrets are stored.
```
