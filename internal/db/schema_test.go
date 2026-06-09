package db

import (
	"path/filepath"
	"sort"
	"strings"
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

// TestProjectSchemaArtifactsKindCheckAcceptsNewKinds pins the
// production v1_project.sql CHECK constraint on artifacts.kind to the
// full enum surfaced by the API and the tool gateway. The fallback
// schema in fallbackProjectSchema intentionally omits the CHECK so
// that older code paths cannot regress, but the production schema
// file ships with the strict CHECK and must be kept in sync with
// allowedArtifactKind in internal/toolgateway. The kinds added by
// D1's review packet work — codex_events, prompt_rendered,
// prompt_context, prompt_meta, prompt_tool_manifest, secret_artifact,
// secrets — must be accepted by the CHECK on a freshly applied
// project schema so the dashboard's refusal_box (content_url=null)
// can be exercised end to end.
func TestProjectSchemaArtifactsKindCheckAcceptsNewKinds(t *testing.T) {
	schema, err := ReadSchema(".", "db/schema/v1_project.sql")
	if err != nil {
		t.Fatalf("ReadSchema v1_project.sql: %v", err)
	}
	if strings.Contains(schema, "fallbackProjectSchema") {
		t.Fatal("ReadSchema returned the fallback constant; expected the production v1_project.sql file on disk")
	}
	if !strings.Contains(schema, "CREATE TABLE IF NOT EXISTS artifacts") {
		t.Fatalf("v1_project.sql does not define the artifacts table; cannot validate CHECK")
	}

	d, err := Open(filepath.Join(t.TempDir(), "project.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := d.ExecScript(schema); err != nil {
		t.Fatalf("exec v1_project.sql: %v", err)
	}

	newKinds := []string{
		"codex_events",
		"prompt_rendered",
		"prompt_context",
		"prompt_meta",
		"prompt_tool_manifest",
		"secret_artifact",
		"secrets",
	}
	for _, kind := range newKinds {
		t.Run(kind, func(t *testing.T) {
			err := d.Exec(
				`INSERT INTO artifacts(id, kind, path, redacted, created_at) VALUES(?, ?, ?, 1, ?)`,
				"art_"+kind, kind, "raw/"+kind+".bin", "2026-06-09T00:00:00Z",
			)
			if err != nil {
				t.Fatalf("InsertArtifact kind=%s rejected by production v1_project.sql CHECK: %v", kind, err)
			}
		})
	}
}

// TestMigrateProjectSchemaWidenArtifactsKindCheck installs the
// legacy (pre-D1) v1_project.sql artifacts.kind CHECK on a fresh
// project database, then asserts that MigrateProjectSchema rebuilds
// the artifacts table with the widened CHECK so the new kinds are
// accepted without dropping rows. The migration must be idempotent:
// re-running it on an already-migrated database must be a no-op.
func TestMigrateProjectSchemaWidenArtifactsKindCheck(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "project.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	const legacySchema = `PRAGMA foreign_keys = ON;
CREATE TABLE IF NOT EXISTS schema_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
INSERT OR IGNORE INTO schema_meta(key, value) VALUES ('schema_name', 'v1_project');
INSERT OR IGNORE INTO schema_meta(key, value) VALUES ('schema_version', '1');
CREATE TABLE IF NOT EXISTS issues (id TEXT PRIMARY KEY, sequence_no INTEGER NOT NULL UNIQUE CHECK (sequence_no > 0), identifier TEXT NOT NULL UNIQUE, title TEXT NOT NULL, description TEXT NOT NULL DEFAULT '', acceptance_criteria_json TEXT NOT NULL DEFAULT '[]', state TEXT NOT NULL, priority INTEGER NOT NULL DEFAULT 3, dispatch_paused INTEGER NOT NULL DEFAULT 0, dispatch_pause_reason TEXT, dispatch_paused_at TEXT, latest_run_id TEXT, latest_review_packet_id TEXT, created_by_type TEXT NOT NULL DEFAULT 'operator', created_by_run_id TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, completed_at TEXT, archived_at TEXT);
CREATE TABLE IF NOT EXISTS run_attempts (id TEXT PRIMARY KEY, issue_id TEXT NOT NULL, attempt_no INTEGER NOT NULL CHECK(attempt_no>0), status TEXT NOT NULL, dispatch_reason TEXT NOT NULL DEFAULT 'manual', source_issue_state TEXT NOT NULL, runner_kind TEXT NOT NULL, base_ref_config TEXT, base_ref TEXT, base_sha TEXT, branch_name TEXT, failure_code TEXT, failure_message TEXT, started_at TEXT, ended_at TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, UNIQUE(issue_id, attempt_no), FOREIGN KEY(issue_id) REFERENCES issues(id) ON DELETE CASCADE);
CREATE TABLE IF NOT EXISTS review_packets (id TEXT PRIMARY KEY, issue_id TEXT NOT NULL, run_id TEXT NOT NULL UNIQUE, handoff_id TEXT NOT NULL, packet_no INTEGER NOT NULL CHECK(packet_no>0), status TEXT NOT NULL, root_path TEXT NOT NULL, review_md_path TEXT, review_json_path TEXT, patch_path TEXT, changed_files_path TEXT, untracked_files_path TEXT, diffstat_path TEXT, prompt_snapshot_id TEXT, failure_code TEXT, failure_message TEXT, created_at TEXT NOT NULL, FOREIGN KEY(issue_id) REFERENCES issues(id) ON DELETE CASCADE, FOREIGN KEY(run_id) REFERENCES run_attempts(id) ON DELETE CASCADE, UNIQUE(issue_id, packet_no));
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
);`
	if err := d.ExecScript(legacySchema); err != nil {
		t.Fatalf("exec legacy schema: %v", err)
	}
	if err := d.Exec(`INSERT INTO artifacts(id, kind, path, redacted, created_at) VALUES('art_legacy','codex_log','raw/codex_log.bin',1,'2026-06-09T00:00:00Z')`); err != nil {
		t.Fatalf("insert legacy artifact: %v", err)
	}

	if err := MigrateProjectSchema(d); err != nil {
		t.Fatalf("MigrateProjectSchema: %v", err)
	}
	// Idempotent: re-running must not fail.
	if err := MigrateProjectSchema(d); err != nil {
		t.Fatalf("MigrateProjectSchema idempotent: %v", err)
	}

	// Legacy row preserved.
	row, err := d.QueryOne(`SELECT kind FROM artifacts WHERE id='art_legacy'`)
	if err != nil {
		t.Fatalf("query legacy artifact: %v", err)
	}
	if got := row["kind"].String(); got != "codex_log" {
		t.Fatalf("legacy artifact kind = %q, want codex_log", got)
	}

	// New kinds now accepted.
	for _, kind := range []string{"codex_events", "prompt_rendered", "prompt_context", "prompt_meta", "prompt_tool_manifest", "secret_artifact", "secrets"} {
		if err := d.Exec(`INSERT INTO artifacts(id, kind, path, redacted, created_at) VALUES(?, ?, ?, 1, ?)`,
			"art_"+kind, kind, "raw/"+kind+".bin", "2026-06-09T00:00:00Z"); err != nil {
			t.Fatalf("MigrateProjectSchema: new kind %q still rejected: %v", kind, err)
		}
	}
}
