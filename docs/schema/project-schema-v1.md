# Project DB Schema v1

Path:

```text
<repo>/.symphony/symphony.db
```

## SQL

```sql
CREATE TABLE schema_version (
  version INTEGER NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE project_info (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  repo_root TEXT NOT NULL,
  issue_prefix TEXT NOT NULL DEFAULT 'LOC',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE project_settings (
  key TEXT PRIMARY KEY,
  value_json TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE counters (
  key TEXT PRIMARY KEY,
  value INTEGER NOT NULL
);

CREATE TABLE issues (
  id TEXT PRIMARY KEY,
  identifier TEXT NOT NULL UNIQUE,
  sequence_no INTEGER NOT NULL UNIQUE,
  title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  acceptance_criteria_json TEXT NOT NULL DEFAULT '[]',
  state TEXT NOT NULL CHECK (state IN ('Inbox','Ready','Working','Rework','Blocked','Human Review','Done','Cancelled','Duplicate')),
  priority INTEGER NOT NULL DEFAULT 3,
  estimate TEXT,
  external_ref TEXT,
  dispatch_paused INTEGER NOT NULL DEFAULT 0 CHECK (dispatch_paused IN (0, 1)),
  dispatch_pause_reason TEXT,
  dispatch_paused_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  completed_at TEXT,
  archived_at TEXT
);

CREATE INDEX idx_issues_state_priority_created ON issues(state, priority, created_at);
CREATE INDEX idx_issues_updated_at ON issues(updated_at);
CREATE INDEX idx_issues_dispatch_paused ON issues(dispatch_paused);

CREATE TABLE issue_labels (
  issue_id TEXT NOT NULL,
  label TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (issue_id, label),
  FOREIGN KEY (issue_id) REFERENCES issues(id) ON DELETE CASCADE
);

CREATE INDEX idx_issue_labels_label ON issue_labels(label);

CREATE TABLE issue_comments (
  id TEXT PRIMARY KEY,
  issue_id TEXT NOT NULL,
  run_id TEXT,
  author_type TEXT NOT NULL CHECK (author_type IN ('operator', 'agent', 'system')),
  author_id TEXT,
  body TEXT NOT NULL,
  created_at TEXT NOT NULL,
  FOREIGN KEY (issue_id) REFERENCES issues(id) ON DELETE CASCADE
);

CREATE INDEX idx_issue_comments_issue_created ON issue_comments(issue_id, created_at);

CREATE TABLE issue_relations (
  id TEXT PRIMARY KEY,
  source_issue_id TEXT NOT NULL,
  target_issue_id TEXT NOT NULL,
  relation_type TEXT NOT NULL CHECK (relation_type IN ('blocks', 'duplicates', 'followup_of')),
  created_by_type TEXT NOT NULL CHECK (created_by_type IN ('operator', 'agent', 'system')),
  created_by_run_id TEXT,
  created_at TEXT NOT NULL,
  FOREIGN KEY (source_issue_id) REFERENCES issues(id) ON DELETE CASCADE,
  FOREIGN KEY (target_issue_id) REFERENCES issues(id) ON DELETE CASCADE,
  UNIQUE (source_issue_id, target_issue_id, relation_type)
);

CREATE INDEX idx_issue_relations_source ON issue_relations(source_issue_id, relation_type);
CREATE INDEX idx_issue_relations_target ON issue_relations(target_issue_id, relation_type);

CREATE TABLE issue_state_history (
  id TEXT PRIMARY KEY,
  issue_id TEXT NOT NULL,
  from_state TEXT,
  to_state TEXT NOT NULL,
  actor_type TEXT NOT NULL CHECK (actor_type IN ('operator', 'agent', 'system', 'orchestrator')),
  actor_id TEXT,
  run_id TEXT,
  reason TEXT,
  created_at TEXT NOT NULL,
  FOREIGN KEY (issue_id) REFERENCES issues(id) ON DELETE CASCADE
);

CREATE INDEX idx_issue_state_history_issue_created ON issue_state_history(issue_id, created_at);

CREATE TABLE workspaces (
  id TEXT PRIMARY KEY,
  issue_id TEXT NOT NULL UNIQUE,
  path TEXT NOT NULL UNIQUE,
  branch_name TEXT NOT NULL,
  base_ref TEXT NOT NULL,
  base_sha TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('planned','creating','ready','in_use','error','cleanup_pending','removed')),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  last_used_at TEXT,
  removed_at TEXT,
  FOREIGN KEY (issue_id) REFERENCES issues(id) ON DELETE CASCADE
);

CREATE INDEX idx_workspaces_issue ON workspaces(issue_id);
CREATE INDEX idx_workspaces_status ON workspaces(status);

CREATE TABLE run_attempts (
  id TEXT PRIMARY KEY,
  issue_id TEXT NOT NULL,
  workspace_id TEXT,
  attempt_no INTEGER NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('pending','preparing_workspace','rendering_prompt','starting_agent','running','completed','completed_without_handoff','failed','cancelled')),
  dispatch_reason TEXT NOT NULL CHECK (dispatch_reason IN ('manual', 'scheduler', 'retry', 'rework')),
  agent_runtime TEXT NOT NULL DEFAULT 'codex',
  codex_command TEXT,
  codex_version TEXT,
  process_pid INTEGER,
  process_group_id INTEGER,
  thread_id TEXT,
  turn_id TEXT,
  session_id TEXT,
  failure_code TEXT,
  failure_message TEXT,
  started_at TEXT,
  ended_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY (issue_id) REFERENCES issues(id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE SET NULL,
  UNIQUE (issue_id, attempt_no)
);

CREATE INDEX idx_run_attempts_issue_created ON run_attempts(issue_id, created_at);
CREATE INDEX idx_run_attempts_status ON run_attempts(status);
CREATE INDEX idx_run_attempts_session ON run_attempts(session_id);

CREATE TABLE run_events (
  seq INTEGER PRIMARY KEY AUTOINCREMENT,
  id TEXT NOT NULL UNIQUE,
  issue_id TEXT,
  run_id TEXT,
  type TEXT NOT NULL,
  severity TEXT NOT NULL CHECK (severity IN ('debug', 'info', 'warning', 'error')),
  summary TEXT NOT NULL,
  data_json TEXT NOT NULL DEFAULT '{}',
  raw_ref TEXT,
  created_at TEXT NOT NULL,
  FOREIGN KEY (issue_id) REFERENCES issues(id) ON DELETE CASCADE,
  FOREIGN KEY (run_id) REFERENCES run_attempts(id) ON DELETE CASCADE
);

CREATE INDEX idx_run_events_run_seq ON run_events(run_id, seq);
CREATE INDEX idx_run_events_issue_seq ON run_events(issue_id, seq);
CREATE INDEX idx_run_events_type ON run_events(type);
CREATE INDEX idx_run_events_created ON run_events(created_at);

CREATE TABLE approval_requests (
  id TEXT PRIMARY KEY,
  issue_id TEXT NOT NULL,
  run_id TEXT NOT NULL,
  type TEXT NOT NULL CHECK (type IN ('command', 'file_change', 'network')),
  status TEXT NOT NULL CHECK (status IN ('pending','auto_approved','auto_denied','approved','denied','cancelled','expired')),
  risk_level TEXT NOT NULL CHECK (risk_level IN ('low', 'medium', 'high', 'critical')),
  command TEXT,
  cwd TEXT,
  file_path TEXT,
  file_action TEXT,
  network_host TEXT,
  network_protocol TEXT,
  network_port INTEGER,
  reason TEXT,
  policy_match TEXT,
  decision TEXT,
  decision_reason TEXT,
  decided_by TEXT,
  requested_at TEXT NOT NULL,
  resolved_at TEXT,
  FOREIGN KEY (issue_id) REFERENCES issues(id) ON DELETE CASCADE,
  FOREIGN KEY (run_id) REFERENCES run_attempts(id) ON DELETE CASCADE
);

CREATE INDEX idx_approval_requests_status ON approval_requests(status, requested_at);
CREATE INDEX idx_approval_requests_run ON approval_requests(run_id, requested_at);
CREATE INDEX idx_approval_requests_issue ON approval_requests(issue_id, requested_at);

CREATE TABLE run_tool_tokens (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL,
  issue_id TEXT NOT NULL,
  token_hash TEXT NOT NULL UNIQUE,
  scope_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  last_used_at TEXT,
  revoked_at TEXT,
  FOREIGN KEY (run_id) REFERENCES run_attempts(id) ON DELETE CASCADE,
  FOREIGN KEY (issue_id) REFERENCES issues(id) ON DELETE CASCADE
);

CREATE INDEX idx_run_tool_tokens_run ON run_tool_tokens(run_id);
CREATE INDEX idx_run_tool_tokens_expires ON run_tool_tokens(expires_at);

CREATE TABLE tool_calls (
  id TEXT PRIMARY KEY,
  issue_id TEXT NOT NULL,
  run_id TEXT NOT NULL,
  tool_name TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('started', 'succeeded', 'failed')),
  input_hash TEXT,
  input_json_redacted TEXT,
  output_hash TEXT,
  output_json_redacted TEXT,
  error_code TEXT,
  error_message TEXT,
  started_at TEXT NOT NULL,
  ended_at TEXT,
  FOREIGN KEY (issue_id) REFERENCES issues(id) ON DELETE CASCADE,
  FOREIGN KEY (run_id) REFERENCES run_attempts(id) ON DELETE CASCADE
);

CREATE INDEX idx_tool_calls_run_started ON tool_calls(run_id, started_at);
CREATE INDEX idx_tool_calls_issue_started ON tool_calls(issue_id, started_at);
CREATE INDEX idx_tool_calls_tool_name ON tool_calls(tool_name);

CREATE TABLE handoffs (
  id TEXT PRIMARY KEY,
  issue_id TEXT NOT NULL,
  run_id TEXT NOT NULL,
  summary TEXT NOT NULL,
  changed_files_json TEXT NOT NULL DEFAULT '[]',
  tests_json TEXT NOT NULL DEFAULT '[]',
  risks_json TEXT NOT NULL DEFAULT '[]',
  verification_json TEXT NOT NULL DEFAULT '[]',
  followups_json TEXT NOT NULL DEFAULT '[]',
  target_state TEXT NOT NULL DEFAULT 'Human Review',
  submitted_at TEXT NOT NULL,
  FOREIGN KEY (issue_id) REFERENCES issues(id) ON DELETE CASCADE,
  FOREIGN KEY (run_id) REFERENCES run_attempts(id) ON DELETE CASCADE,
  UNIQUE (run_id)
);

CREATE INDEX idx_handoffs_issue_submitted ON handoffs(issue_id, submitted_at);

CREATE TABLE artifacts (
  id TEXT PRIMARY KEY,
  issue_id TEXT NOT NULL,
  run_id TEXT,
  kind TEXT NOT NULL CHECK (kind IN ('test_output','patch','changed_files','prompt_snapshot','codex_log','review_packet','agent_file','diagnostic','other')),
  path TEXT NOT NULL,
  mime_type TEXT,
  size_bytes INTEGER,
  sha256 TEXT,
  redacted INTEGER NOT NULL DEFAULT 1 CHECK (redacted IN (0, 1)),
  created_at TEXT NOT NULL,
  FOREIGN KEY (issue_id) REFERENCES issues(id) ON DELETE CASCADE,
  FOREIGN KEY (run_id) REFERENCES run_attempts(id) ON DELETE SET NULL
);

CREATE INDEX idx_artifacts_issue_created ON artifacts(issue_id, created_at);
CREATE INDEX idx_artifacts_run_created ON artifacts(run_id, created_at);
CREATE INDEX idx_artifacts_kind ON artifacts(kind);

CREATE TABLE review_packets (
  id TEXT PRIMARY KEY,
  issue_id TEXT NOT NULL,
  run_id TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('generated', 'partial', 'failed')),
  root_path TEXT NOT NULL,
  review_md_path TEXT,
  review_json_path TEXT,
  patch_path TEXT,
  changed_files_path TEXT,
  handoff_id TEXT,
  prompt_snapshot_id TEXT,
  failure_code TEXT,
  failure_message TEXT,
  created_at TEXT NOT NULL,
  FOREIGN KEY (issue_id) REFERENCES issues(id) ON DELETE CASCADE,
  FOREIGN KEY (run_id) REFERENCES run_attempts(id) ON DELETE CASCADE,
  FOREIGN KEY (handoff_id) REFERENCES handoffs(id) ON DELETE SET NULL
);

CREATE INDEX idx_review_packets_issue_created ON review_packets(issue_id, created_at);
CREATE INDEX idx_review_packets_run ON review_packets(run_id);

CREATE TABLE workflow_snapshots (
  id TEXT PRIMARY KEY,
  workflow_path TEXT NOT NULL,
  workflow_sha TEXT NOT NULL,
  config_hash TEXT NOT NULL,
  prompt_template_hash TEXT NOT NULL,
  validation_status TEXT NOT NULL CHECK (validation_status IN ('valid', 'invalid')),
  effective_config_json TEXT NOT NULL DEFAULT '{}',
  validation_errors_json TEXT NOT NULL DEFAULT '[]',
  created_at TEXT NOT NULL
);

CREATE INDEX idx_workflow_snapshots_created ON workflow_snapshots(created_at);
CREATE INDEX idx_workflow_snapshots_sha ON workflow_snapshots(workflow_sha);

CREATE TABLE prompt_snapshots (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL,
  workflow_snapshot_id TEXT,
  runtime_envelope_version TEXT NOT NULL,
  tool_manifest_version TEXT NOT NULL,
  context_hash TEXT NOT NULL,
  rendered_prompt_hash TEXT NOT NULL,
  context_json_path TEXT,
  redacted_prompt_path TEXT,
  tool_manifest_path TEXT,
  created_at TEXT NOT NULL,
  FOREIGN KEY (run_id) REFERENCES run_attempts(id) ON DELETE CASCADE,
  FOREIGN KEY (workflow_snapshot_id) REFERENCES workflow_snapshots(id) ON DELETE SET NULL
);

CREATE INDEX idx_prompt_snapshots_run ON prompt_snapshots(run_id);
CREATE INDEX idx_prompt_snapshots_created ON prompt_snapshots(created_at);
```

## Initialization

Initialize:

```sql
INSERT INTO counters (key, value) VALUES ('issue_sequence', 0);
```

## PRAGMA

```sql
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
```
