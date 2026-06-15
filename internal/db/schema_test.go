package db

import (
	"path/filepath"
	"sort"
	"testing"
)

func TestFallbackProjectSchemaConstrainsApprovalRequests(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "project.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	if err := d.ExecScript(fallbackProjectSchema); err != nil {
		t.Fatalf("exec fallback schema: %v", err)
	}

	assertExecFails(t, d, `INSERT INTO approval_requests(id,run_id,issue_id,kind,status,request_json,created_at) VALUES('apr_bad_kind','run_1','iss_1','file','pending','{}','2026-05-26T10:00:00Z')`)
	assertExecFails(t, d, `INSERT INTO approval_requests(id,run_id,issue_id,kind,status,request_json,created_at) VALUES('apr_bad_status','run_1','iss_1','command','expired','{}','2026-05-26T10:00:00Z')`)
	assertExecFails(t, d, `INSERT INTO approval_requests(id,run_id,issue_id,kind,status,request_json,timeout_ms,created_at) VALUES('apr_bad_timeout','run_1','iss_1','command','pending','{}',0,'2026-05-26T10:00:00Z')`)
}

func TestFallbackAppSchemaIncludesRuntimeOwnerNonceColumns(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	if err := d.ExecScript(fallbackAppSchema); err != nil {
		t.Fatalf("exec fallback schema: %v", err)
	}
	got := tableColumnNames(t, d, "runtime_descriptors")
	for _, want := range []string{"owner_nonce", "heartbeat_at", "heartbeat_ttl_ms", "acquired_at"} {
		if _, ok := got[want]; !ok {
			t.Fatalf("runtime_descriptors missing column %q in fallback app schema; have %v", want, sortedKeys(got))
		}
	}
}

func TestMigrateAppSchemaAddsRuntimeOwnerNonceColumns(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	const legacy = `PRAGMA foreign_keys = ON;
CREATE TABLE IF NOT EXISTS schema_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
INSERT OR REPLACE INTO schema_meta(key, value) VALUES ('schema_name', 'v1_app');
INSERT OR REPLACE INTO schema_meta(key, value) VALUES ('schema_version', '1');
CREATE TABLE IF NOT EXISTS projects (id TEXT PRIMARY KEY, name TEXT NOT NULL, repo_root TEXT NOT NULL UNIQUE, project_db_path TEXT NOT NULL, workflow_path TEXT NOT NULL, issue_prefix TEXT NOT NULL DEFAULT 'LOC', created_at TEXT NOT NULL, updated_at TEXT NOT NULL, last_opened_at TEXT);
CREATE TABLE IF NOT EXISTS app_settings (key TEXT PRIMARY KEY, value_json TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS local_sessions (id TEXT PRIMARY KEY, project_id TEXT NOT NULL, kind TEXT NOT NULL CHECK (kind IN ('cli','browser','desktop')), token_hash TEXT NOT NULL UNIQUE, csrf_hash TEXT, user_label TEXT, created_at TEXT NOT NULL, last_seen_at TEXT, expires_at TEXT, revoked_at TEXT);
CREATE TABLE IF NOT EXISTS open_tokens (id TEXT PRIMARY KEY, project_id TEXT NOT NULL, token_hash TEXT NOT NULL UNIQUE, expires_at TEXT NOT NULL, consumed_at TEXT, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS runtime_descriptors (project_id TEXT PRIMARY KEY, api_url TEXT NOT NULL, tool_gateway_endpoint TEXT NOT NULL, daemon_pid INTEGER NOT NULL, started_at TEXT NOT NULL, updated_at TEXT NOT NULL);
`
	if err := d.ExecScript(legacy); err != nil {
		t.Fatalf("exec legacy schema: %v", err)
	}
	if err := d.Exec(`INSERT INTO projects(id,name,repo_root,project_db_path,workflow_path,created_at,updated_at,last_opened_at) VALUES('p1','repo','/r','/r/p.db','/r/WORKFLOW.md','2026-06-05T00:00:00Z','2026-06-05T00:00:00Z','2026-06-05T00:00:00Z')`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if err := d.Exec(`INSERT INTO runtime_descriptors(project_id,api_url,tool_gateway_endpoint,daemon_pid,started_at,updated_at) VALUES('p1','http://127.0.0.1:1','http://127.0.0.1:2',4242,'2026-06-05T00:00:00Z','2026-06-05T00:00:00Z')`); err != nil {
		t.Fatalf("insert legacy runtime descriptor: %v", err)
	}
	if err := MigrateAppSchema(d); err != nil {
		t.Fatalf("MigrateAppSchema: %v", err)
	}
	got := tableColumnNames(t, d, "runtime_descriptors")
	for _, want := range []string{"owner_nonce", "heartbeat_at", "heartbeat_ttl_ms", "acquired_at"} {
		if _, ok := got[want]; !ok {
			t.Fatalf("runtime_descriptors missing column %q after migration; have %v", want, sortedKeys(got))
		}
	}
	row, err := d.QueryOne(`SELECT owner_nonce, heartbeat_at, heartbeat_ttl_ms, acquired_at FROM runtime_descriptors WHERE project_id='p1'`)
	if err != nil {
		t.Fatalf("query migrated runtime descriptor: %v", err)
	}
	if got := row["heartbeat_ttl_ms"].Int(); got != 30000 {
		t.Fatalf("heartbeat_ttl_ms default = %d, want 30000", got)
	}
	if got := row["heartbeat_at"].Int(); got != 0 {
		t.Fatalf("heartbeat_at default = %d, want 0", got)
	}
	if got := row["acquired_at"].Int(); got != 0 {
		t.Fatalf("acquired_at default = %d, want 0", got)
	}
	if got := row["owner_nonce"].String(); got != "" {
		t.Fatalf("owner_nonce default = %q, want empty string", got)
	}
	if err := MigrateAppSchema(d); err != nil {
		t.Fatalf("MigrateAppSchema idempotent: %v", err)
	}
}

func TestMigrateProjectSchemaAddsReworkSnapshotsAndAllowsManualRework(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "project.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	const legacy = `PRAGMA foreign_keys = ON;
CREATE TABLE IF NOT EXISTS schema_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
INSERT OR REPLACE INTO schema_meta(key, value) VALUES ('schema_name', 'v1_project');
INSERT OR REPLACE INTO schema_meta(key, value) VALUES ('schema_version', '1');
CREATE TABLE IF NOT EXISTS issues (id TEXT PRIMARY KEY);
INSERT OR REPLACE INTO issues(id) VALUES('iss_1');
CREATE TABLE IF NOT EXISTS workspaces (id TEXT PRIMARY KEY);
CREATE TABLE IF NOT EXISTS workflow_snapshots (id TEXT PRIMARY KEY);
CREATE TABLE IF NOT EXISTS run_attempts (
  id TEXT PRIMARY KEY,
  issue_id TEXT NOT NULL,
  attempt_no INTEGER NOT NULL CHECK(attempt_no>0),
  workspace_id TEXT,
  workflow_snapshot_id TEXT,
  status TEXT NOT NULL,
  dispatch_reason TEXT NOT NULL DEFAULT 'manual' CHECK(dispatch_reason IN ('manual','scheduler','manual_recovery')),
  source_issue_state TEXT NOT NULL,
  runner_kind TEXT NOT NULL,
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
  UNIQUE(issue_id, attempt_no)
);
`
	if err := d.ExecScript(legacy); err != nil {
		t.Fatalf("exec legacy schema: %v", err)
	}

	if err := MigrateProjectSchema(d); err != nil {
		t.Fatalf("MigrateProjectSchema: %v", err)
	}
	got := tableColumnNames(t, d, "rework_snapshots")
	for _, want := range []string{"id", "run_id", "issue_id", "prompt_hash", "review_reason", "safe_summary_sha256"} {
		if _, ok := got[want]; !ok {
			t.Fatalf("rework_snapshots missing column %q after migration; have %v", want, sortedKeys(got))
		}
	}
	if err := d.Exec(`INSERT INTO run_attempts(id,issue_id,attempt_no,status,dispatch_reason,source_issue_state,runner_kind,created_at,updated_at) VALUES('run_rework','iss_1',1,'pending','manual_rework','Rework','fake','2026-06-15T00:00:00Z','2026-06-15T00:00:00Z')`); err != nil {
		t.Fatalf("manual_rework dispatch_reason rejected after migration: %v", err)
	}
	if err := MigrateProjectSchema(d); err != nil {
		t.Fatalf("MigrateProjectSchema idempotent: %v", err)
	}
}

func tableColumnNames(t *testing.T, d *DB, table string) map[string]struct{} {
	t.Helper()
	rows, err := d.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info %s: %v", table, err)
	}
	cols := map[string]struct{}{}
	for _, r := range rows {
		for k, v := range r {
			if k == "name" {
				cols[v.String()] = struct{}{}
			}
		}
	}
	return cols
}

func sortedKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func assertExecFails(t *testing.T, d *DB, sql string) {
	t.Helper()
	if err := d.Exec(sql); err == nil {
		t.Fatalf("Exec succeeded, want constraint failure for %s", sql)
	}
}
