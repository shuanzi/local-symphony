-- Local Symphony App v1 project-level SQLite schema
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS schema_meta (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

INSERT OR IGNORE INTO schema_meta(key, value) VALUES ('schema_name', 'v1_project');
INSERT OR IGNORE INTO schema_meta(key, value) VALUES ('schema_version', '1');

CREATE TABLE IF NOT EXISTS project_info (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  repo_root TEXT NOT NULL,
  issue_prefix TEXT NOT NULL DEFAULT 'LOC',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS project_settings (
  key TEXT PRIMARY KEY,
  value_json TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS counters (
  name TEXT PRIMARY KEY,
  value INTEGER NOT NULL CHECK (value >= 0)
);

INSERT OR IGNORE INTO counters(name, value) VALUES ('issue_sequence', 0);

CREATE TABLE IF NOT EXISTS workflow_snapshots (
  id TEXT PRIMARY KEY,
  status TEXT NOT NULL CHECK (status IN ('valid','invalid')),
  source_path TEXT NOT NULL,
  config_json TEXT NOT NULL,
  prompt_body_sha256 TEXT NOT NULL,
  validation_errors_json TEXT NOT NULL DEFAULT '[]',
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS issues (
  id TEXT PRIMARY KEY,
  sequence_no INTEGER NOT NULL UNIQUE CHECK (sequence_no > 0),
  identifier TEXT NOT NULL UNIQUE,
  title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  acceptance_criteria_json TEXT NOT NULL DEFAULT '[]',
  state TEXT NOT NULL CHECK (state IN ('Inbox','Ready','Working','Rework','Blocked','Human Review','Done','Cancelled','Duplicate')),
  priority INTEGER NOT NULL DEFAULT 3 CHECK (priority BETWEEN 1 AND 5),
  dispatch_paused INTEGER NOT NULL DEFAULT 0 CHECK (dispatch_paused IN (0,1)),
  dispatch_pause_reason TEXT,
  dispatch_paused_at TEXT,
  latest_run_id TEXT,
  latest_review_packet_id TEXT,
  created_by_type TEXT NOT NULL DEFAULT 'operator' CHECK (created_by_type IN ('operator','agent','system')),
  created_by_run_id TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  completed_at TEXT,
  archived_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_issues_state_priority_created ON issues(state, priority, created_at, identifier);
CREATE INDEX IF NOT EXISTS idx_issues_dispatch ON issues(state, dispatch_paused);

CREATE TABLE IF NOT EXISTS issue_state_history (
  id TEXT PRIMARY KEY,
  issue_id TEXT NOT NULL,
  from_state TEXT CHECK (from_state IS NULL OR from_state IN ('Inbox','Ready','Working','Rework','Blocked','Human Review','Done','Cancelled','Duplicate')),
  to_state TEXT NOT NULL CHECK (to_state IN ('Inbox','Ready','Working','Rework','Blocked','Human Review','Done','Cancelled','Duplicate')),
  actor_type TEXT NOT NULL CHECK (actor_type IN ('operator','agent','system','orchestrator')),
  actor_id TEXT,
  run_id TEXT,
  reason TEXT,
  created_at TEXT NOT NULL,
  FOREIGN KEY(issue_id) REFERENCES issues(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_issue_state_history_issue_created ON issue_state_history(issue_id, created_at);

CREATE TABLE IF NOT EXISTS issue_labels (
  issue_id TEXT NOT NULL,
  label TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY(issue_id, label),
  FOREIGN KEY(issue_id) REFERENCES issues(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS issue_comments (
  id TEXT PRIMARY KEY,
  issue_id TEXT NOT NULL,
  run_id TEXT,
  author_type TEXT NOT NULL CHECK (author_type IN ('operator','agent','system')),
  body TEXT NOT NULL,
  created_at TEXT NOT NULL,
  FOREIGN KEY(issue_id) REFERENCES issues(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_issue_comments_issue_created ON issue_comments(issue_id, created_at);

CREATE TABLE IF NOT EXISTS issue_relations (
  id TEXT PRIMARY KEY,
  source_issue_id TEXT NOT NULL,
  target_issue_id TEXT NOT NULL,
  relation_type TEXT NOT NULL CHECK (relation_type IN ('blocks','followup_of','duplicates')),
  active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
  created_by_type TEXT NOT NULL CHECK (created_by_type IN ('operator','agent','system')),
  created_by_run_id TEXT,
  created_at TEXT NOT NULL,
  resolved_at TEXT,
  FOREIGN KEY(source_issue_id) REFERENCES issues(id) ON DELETE CASCADE,
  FOREIGN KEY(target_issue_id) REFERENCES issues(id) ON DELETE CASCADE,
  CHECK (source_issue_id <> target_issue_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_issue_relations_unique_active
  ON issue_relations(source_issue_id, target_issue_id, relation_type)
  WHERE active = 1;
CREATE INDEX IF NOT EXISTS idx_issue_relations_target_active ON issue_relations(target_issue_id, relation_type, active);

CREATE TABLE IF NOT EXISTS workspaces (
  id TEXT PRIMARY KEY,
  issue_id TEXT NOT NULL UNIQUE,
  path TEXT NOT NULL UNIQUE,
  branch_name TEXT NOT NULL,
  base_ref_config TEXT NOT NULL DEFAULT 'auto',
  base_ref TEXT NOT NULL,
  base_sha TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('prepared','conflict','missing')),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY(issue_id) REFERENCES issues(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS run_attempts (
  id TEXT PRIMARY KEY,
  issue_id TEXT NOT NULL,
  attempt_no INTEGER NOT NULL CHECK (attempt_no > 0),
  workspace_id TEXT,
  workflow_snapshot_id TEXT,
  status TEXT NOT NULL CHECK (status IN ('pending','preparing_workspace','rendering_prompt','starting_agent','running','completed','completed_without_handoff','failed','cancelled')),
  dispatch_reason TEXT NOT NULL DEFAULT 'manual' CHECK (dispatch_reason IN ('manual','scheduler','manual_recovery','manual_rework')),
  source_issue_state TEXT NOT NULL CHECK (source_issue_state IN ('Ready','Rework')),
  runner_kind TEXT NOT NULL CHECK (runner_kind IN ('fake','codex')),
  base_ref_config TEXT,
  base_ref TEXT,
  base_sha TEXT,
  branch_name TEXT,
  failure_code TEXT,
  failure_message TEXT,
  started_at TEXT,
  ended_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY(issue_id) REFERENCES issues(id) ON DELETE CASCADE,
  FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE SET NULL,
  FOREIGN KEY(workflow_snapshot_id) REFERENCES workflow_snapshots(id) ON DELETE SET NULL,
  UNIQUE(issue_id, attempt_no)
);

CREATE INDEX IF NOT EXISTS idx_run_attempts_issue_created ON run_attempts(issue_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_run_attempts_status ON run_attempts(status);

CREATE TABLE IF NOT EXISTS run_events (
  seq INTEGER PRIMARY KEY AUTOINCREMENT,
  id TEXT NOT NULL UNIQUE,
  project_id TEXT NOT NULL,
  issue_id TEXT,
  run_id TEXT,
  event_type TEXT NOT NULL,
  actor_type TEXT NOT NULL CHECK (actor_type IN ('system','operator','agent','codex','hook')),
  data_json TEXT NOT NULL,
  redacted INTEGER NOT NULL DEFAULT 1 CHECK (redacted IN (0,1)),
  created_at TEXT NOT NULL,
  FOREIGN KEY(issue_id) REFERENCES issues(id) ON DELETE SET NULL,
  FOREIGN KEY(run_id) REFERENCES run_attempts(id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_run_events_run_seq ON run_events(run_id, seq);
CREATE INDEX IF NOT EXISTS idx_run_events_issue_seq ON run_events(issue_id, seq);
CREATE INDEX IF NOT EXISTS idx_run_events_seq ON run_events(seq);

CREATE TABLE IF NOT EXISTS approval_requests (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL,
  issue_id TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('command','file_change','network')),
  status TEXT NOT NULL CHECK (status IN ('pending','approved_once','approved_for_run','approved_for_session','denied','auto_denied','cancelled','timeout')),
  request_json TEXT NOT NULL,
  decision_json TEXT,
  reason TEXT,
  timeout_ms INTEGER CHECK (timeout_ms IS NULL OR timeout_ms > 0),
  expires_at TEXT,
  created_at TEXT NOT NULL,
  resolved_at TEXT,
  FOREIGN KEY(run_id) REFERENCES run_attempts(id) ON DELETE CASCADE,
  FOREIGN KEY(issue_id) REFERENCES issues(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_approval_requests_status ON approval_requests(status, created_at);

CREATE TABLE IF NOT EXISTS run_tool_tokens (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL,
  issue_id TEXT NOT NULL,
  token_hash TEXT NOT NULL UNIQUE,
  scope_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  last_used_at TEXT,
  revoked_at TEXT,
  FOREIGN KEY(run_id) REFERENCES run_attempts(id) ON DELETE CASCADE,
  FOREIGN KEY(issue_id) REFERENCES issues(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_run_tool_tokens_run ON run_tool_tokens(run_id);

CREATE TABLE IF NOT EXISTS tool_calls (
  id TEXT PRIMARY KEY,
  issue_id TEXT NOT NULL,
  run_id TEXT NOT NULL,
  tool_name TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('started','succeeded','failed')),
  input_hash TEXT,
  input_json_redacted TEXT,
  output_hash TEXT,
  output_json_redacted TEXT,
  error_code TEXT,
  error_message TEXT,
  started_at TEXT NOT NULL,
  ended_at TEXT,
  FOREIGN KEY(issue_id) REFERENCES issues(id) ON DELETE CASCADE,
  FOREIGN KEY(run_id) REFERENCES run_attempts(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_tool_calls_run_started ON tool_calls(run_id, started_at);

CREATE TABLE IF NOT EXISTS handoffs (
  id TEXT PRIMARY KEY,
  issue_id TEXT NOT NULL,
  run_id TEXT NOT NULL UNIQUE,
  payload_hash TEXT NOT NULL,
  payload_json_redacted TEXT NOT NULL,
  summary TEXT NOT NULL,
  changed_files_json TEXT NOT NULL DEFAULT '[]',
  tests_json TEXT NOT NULL DEFAULT '[]',
  risks_json TEXT NOT NULL DEFAULT '[]',
  verification_json TEXT NOT NULL DEFAULT '[]',
  followups_json TEXT NOT NULL DEFAULT '[]',
  target_state TEXT NOT NULL DEFAULT 'Human Review' CHECK (target_state = 'Human Review'),
  submitted_at TEXT NOT NULL,
  FOREIGN KEY(issue_id) REFERENCES issues(id) ON DELETE CASCADE,
  FOREIGN KEY(run_id) REFERENCES run_attempts(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_handoffs_run_hash ON handoffs(run_id, payload_hash);

CREATE TABLE IF NOT EXISTS artifacts (
  id TEXT PRIMARY KEY,
  issue_id TEXT,
  run_id TEXT,
  review_packet_id TEXT,
  kind TEXT NOT NULL CHECK (kind IN ('test_output','patch','changed_files','untracked_files','diffstat','prompt_snapshot','codex_log','review_packet','agent_file','diagnostic','other')),
  path TEXT NOT NULL,
  mime_type TEXT,
  size_bytes INTEGER CHECK (size_bytes IS NULL OR size_bytes >= 0),
  sha256 TEXT,
  redacted INTEGER NOT NULL DEFAULT 1 CHECK (redacted IN (0,1)),
  description TEXT,
  created_at TEXT NOT NULL,
  FOREIGN KEY(issue_id) REFERENCES issues(id) ON DELETE SET NULL,
  FOREIGN KEY(run_id) REFERENCES run_attempts(id) ON DELETE SET NULL,
  FOREIGN KEY(review_packet_id) REFERENCES review_packets(id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_artifacts_run_kind ON artifacts(run_id, kind);
CREATE INDEX IF NOT EXISTS idx_artifacts_issue_kind ON artifacts(issue_id, kind);

CREATE TABLE IF NOT EXISTS prompt_snapshots (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL UNIQUE,
  workflow_snapshot_id TEXT,
  runtime_envelope_version TEXT NOT NULL,
  tool_manifest_version TEXT NOT NULL,
  context_hash TEXT NOT NULL,
  rendered_prompt_hash TEXT NOT NULL,
  context_json_path TEXT NOT NULL,
  redacted_prompt_path TEXT NOT NULL,
  prompt_meta_json_path TEXT NOT NULL,
  tool_manifest_path TEXT NOT NULL,
  created_at TEXT NOT NULL,
  FOREIGN KEY(run_id) REFERENCES run_attempts(id) ON DELETE CASCADE,
  FOREIGN KEY(workflow_snapshot_id) REFERENCES workflow_snapshots(id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS review_packets (
  id TEXT PRIMARY KEY,
  issue_id TEXT NOT NULL,
  run_id TEXT NOT NULL UNIQUE,
  handoff_id TEXT NOT NULL,
  packet_no INTEGER NOT NULL CHECK (packet_no > 0),
  status TEXT NOT NULL CHECK (status IN ('generated','partial','failed')),
  root_path TEXT NOT NULL,
  review_md_path TEXT,
  review_json_path TEXT,
  patch_path TEXT,
  changed_files_path TEXT,
  untracked_files_path TEXT,
  diffstat_path TEXT,
  prompt_snapshot_id TEXT,
  failure_code TEXT,
  failure_message TEXT,
  created_at TEXT NOT NULL,
  FOREIGN KEY(issue_id) REFERENCES issues(id) ON DELETE CASCADE,
  FOREIGN KEY(run_id) REFERENCES run_attempts(id) ON DELETE CASCADE,
  FOREIGN KEY(handoff_id) REFERENCES handoffs(id) ON DELETE CASCADE,
  FOREIGN KEY(prompt_snapshot_id) REFERENCES prompt_snapshots(id) ON DELETE SET NULL,
  UNIQUE(issue_id, packet_no),
  CHECK (
    status <> 'generated' OR (
      review_md_path IS NOT NULL AND
      review_json_path IS NOT NULL AND
      patch_path IS NOT NULL AND
      changed_files_path IS NOT NULL AND
      untracked_files_path IS NOT NULL
    )
  )
);

CREATE INDEX IF NOT EXISTS idx_review_packets_issue_packet ON review_packets(issue_id, packet_no DESC);

-- D4 / R16: rework snapshot ties the prompt snapshot to the previous
-- review packet that triggered the rework dispatch. base_sha and
-- cumulative_diff_sha are stable references for the cumulative diff
-- semantic (rework preserves the original base_sha and accumulates
-- diff across iterations). prompt_hash is the SHA256 of the rendered
-- prompt (after redaction) and is used by diagnostics to correlate
-- the prompt that was actually sent to the agent.
CREATE TABLE IF NOT EXISTS rework_snapshots (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL UNIQUE,
  issue_id TEXT NOT NULL,
  prompt_snapshot_id TEXT,
  previous_run_id TEXT,
  review_packet_id TEXT,
  base_ref TEXT,
  base_sha TEXT,
  cumulative_diff_sha TEXT,
  prompt_hash TEXT NOT NULL,
  review_reason TEXT NOT NULL,
  safe_summary_sha256 TEXT,
  created_at TEXT NOT NULL,
  FOREIGN KEY(run_id) REFERENCES run_attempts(id) ON DELETE CASCADE,
  FOREIGN KEY(issue_id) REFERENCES issues(id) ON DELETE CASCADE,
  FOREIGN KEY(prompt_snapshot_id) REFERENCES prompt_snapshots(id) ON DELETE SET NULL,
  FOREIGN KEY(previous_run_id) REFERENCES run_attempts(id) ON DELETE SET NULL,
  FOREIGN KEY(review_packet_id) REFERENCES review_packets(id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_rework_snapshots_issue_created ON rework_snapshots(issue_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_rework_snapshots_review_packet ON rework_snapshots(review_packet_id);
