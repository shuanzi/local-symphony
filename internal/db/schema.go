package db

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const fallbackAppSchema = `PRAGMA foreign_keys = ON;
CREATE TABLE IF NOT EXISTS schema_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
INSERT OR IGNORE INTO schema_meta(key, value) VALUES ('schema_name', 'v1_app');
INSERT OR IGNORE INTO schema_meta(key, value) VALUES ('schema_version', '1');
CREATE TABLE IF NOT EXISTS projects (id TEXT PRIMARY KEY, name TEXT NOT NULL, repo_root TEXT NOT NULL UNIQUE, project_db_path TEXT NOT NULL, workflow_path TEXT NOT NULL, issue_prefix TEXT NOT NULL DEFAULT 'LOC', created_at TEXT NOT NULL, updated_at TEXT NOT NULL, last_opened_at TEXT);
CREATE TABLE IF NOT EXISTS app_settings (key TEXT PRIMARY KEY, value_json TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS local_sessions (id TEXT PRIMARY KEY, project_id TEXT NOT NULL, kind TEXT NOT NULL CHECK (kind IN ('cli','browser','desktop')), token_hash TEXT NOT NULL UNIQUE, csrf_hash TEXT, user_label TEXT, created_at TEXT NOT NULL, last_seen_at TEXT, expires_at TEXT, revoked_at TEXT);
CREATE TABLE IF NOT EXISTS open_tokens (id TEXT PRIMARY KEY, project_id TEXT NOT NULL, token_hash TEXT NOT NULL UNIQUE, expires_at TEXT NOT NULL, consumed_at TEXT, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS runtime_descriptors (project_id TEXT PRIMARY KEY, api_url TEXT NOT NULL, tool_gateway_endpoint TEXT NOT NULL, daemon_pid INTEGER NOT NULL, owner_nonce TEXT NOT NULL, heartbeat_at INTEGER NOT NULL, heartbeat_ttl_ms INTEGER NOT NULL DEFAULT 30000, acquired_at INTEGER NOT NULL, started_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS runtime_owner_events (id TEXT PRIMARY KEY, project_id TEXT NOT NULL, event_type TEXT NOT NULL, actor_type TEXT NOT NULL, data_json TEXT NOT NULL, redacted INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL, FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE);
CREATE INDEX IF NOT EXISTS idx_runtime_owner_events_project ON runtime_owner_events(project_id, created_at);
`

const fallbackProjectSchema = `PRAGMA foreign_keys = ON;
CREATE TABLE IF NOT EXISTS schema_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
INSERT OR IGNORE INTO schema_meta(key, value) VALUES ('schema_name', 'v1_project');
INSERT OR IGNORE INTO schema_meta(key, value) VALUES ('schema_version', '1');
CREATE TABLE IF NOT EXISTS project_info (id TEXT PRIMARY KEY, name TEXT NOT NULL, repo_root TEXT NOT NULL, issue_prefix TEXT NOT NULL DEFAULT 'LOC', created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS project_settings (key TEXT PRIMARY KEY, value_json TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS counters (name TEXT PRIMARY KEY, value INTEGER NOT NULL CHECK (value >= 0));
INSERT OR IGNORE INTO counters(name, value) VALUES ('issue_sequence', 0);
CREATE TABLE IF NOT EXISTS workflow_snapshots (id TEXT PRIMARY KEY, status TEXT NOT NULL CHECK (status IN ('valid','invalid')), source_path TEXT NOT NULL, config_json TEXT NOT NULL, prompt_body_sha256 TEXT NOT NULL, validation_errors_json TEXT NOT NULL DEFAULT '[]', created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS issues (id TEXT PRIMARY KEY, sequence_no INTEGER NOT NULL UNIQUE CHECK (sequence_no > 0), identifier TEXT NOT NULL UNIQUE, title TEXT NOT NULL, description TEXT NOT NULL DEFAULT '', acceptance_criteria_json TEXT NOT NULL DEFAULT '[]', state TEXT NOT NULL CHECK (state IN ('Inbox','Ready','Working','Rework','Blocked','Human Review','Done','Cancelled','Duplicate')), priority INTEGER NOT NULL DEFAULT 3 CHECK (priority BETWEEN 1 AND 5), dispatch_paused INTEGER NOT NULL DEFAULT 0 CHECK (dispatch_paused IN (0,1)), dispatch_pause_reason TEXT, dispatch_paused_at TEXT, latest_run_id TEXT, latest_review_packet_id TEXT, created_by_type TEXT NOT NULL DEFAULT 'operator' CHECK (created_by_type IN ('operator','agent','system')), created_by_run_id TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, completed_at TEXT, archived_at TEXT);
CREATE INDEX IF NOT EXISTS idx_issues_state_priority_created ON issues(state, priority, created_at, identifier); CREATE INDEX IF NOT EXISTS idx_issues_dispatch ON issues(state, dispatch_paused);
CREATE TABLE IF NOT EXISTS issue_state_history (id TEXT PRIMARY KEY, issue_id TEXT NOT NULL, from_state TEXT, to_state TEXT NOT NULL, actor_type TEXT NOT NULL, actor_id TEXT, run_id TEXT, reason TEXT, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS issue_labels (issue_id TEXT NOT NULL, label TEXT NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY(issue_id,label));
CREATE TABLE IF NOT EXISTS issue_comments (id TEXT PRIMARY KEY, issue_id TEXT NOT NULL, run_id TEXT, author_type TEXT NOT NULL, body TEXT NOT NULL, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS issue_relations (id TEXT PRIMARY KEY, source_issue_id TEXT NOT NULL, target_issue_id TEXT NOT NULL, relation_type TEXT NOT NULL, active INTEGER NOT NULL DEFAULT 1, created_by_type TEXT NOT NULL, created_by_run_id TEXT, created_at TEXT NOT NULL, resolved_at TEXT, CHECK(source_issue_id <> target_issue_id));
CREATE UNIQUE INDEX IF NOT EXISTS idx_issue_relations_unique_active ON issue_relations(source_issue_id,target_issue_id,relation_type) WHERE active=1;
CREATE TABLE IF NOT EXISTS workspaces (id TEXT PRIMARY KEY, issue_id TEXT NOT NULL UNIQUE, path TEXT NOT NULL UNIQUE, branch_name TEXT NOT NULL, base_ref_config TEXT NOT NULL DEFAULT 'auto', base_ref TEXT NOT NULL, base_sha TEXT NOT NULL, status TEXT NOT NULL CHECK(status IN ('prepared','conflict','missing')), created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS run_attempts (id TEXT PRIMARY KEY, issue_id TEXT NOT NULL, attempt_no INTEGER NOT NULL CHECK(attempt_no>0), workspace_id TEXT, workflow_snapshot_id TEXT, status TEXT NOT NULL, dispatch_reason TEXT NOT NULL DEFAULT 'manual', source_issue_state TEXT NOT NULL, runner_kind TEXT NOT NULL, base_ref_config TEXT, base_ref TEXT, base_sha TEXT, branch_name TEXT, failure_code TEXT, failure_message TEXT, started_at TEXT, ended_at TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, UNIQUE(issue_id, attempt_no));
CREATE TABLE IF NOT EXISTS run_events (seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE, project_id TEXT NOT NULL, issue_id TEXT, run_id TEXT, event_type TEXT NOT NULL, actor_type TEXT NOT NULL, data_json TEXT NOT NULL, redacted INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS idx_run_events_run_seq ON run_events(run_id, seq); CREATE INDEX IF NOT EXISTS idx_run_events_issue_seq ON run_events(issue_id, seq); CREATE INDEX IF NOT EXISTS idx_run_events_seq ON run_events(seq);
CREATE TABLE IF NOT EXISTS approval_requests (id TEXT PRIMARY KEY, run_id TEXT NOT NULL, issue_id TEXT NOT NULL, kind TEXT NOT NULL CHECK (kind IN ('command','file_change','network')), status TEXT NOT NULL CHECK (status IN ('pending','approved_once','approved_for_run','approved_for_session','denied','auto_denied','cancelled','timeout')), request_json TEXT NOT NULL, decision_json TEXT, reason TEXT, timeout_ms INTEGER CHECK (timeout_ms IS NULL OR timeout_ms > 0), expires_at TEXT, created_at TEXT NOT NULL, resolved_at TEXT);
CREATE TABLE IF NOT EXISTS run_tool_tokens (id TEXT PRIMARY KEY, run_id TEXT NOT NULL, issue_id TEXT NOT NULL, token_hash TEXT NOT NULL UNIQUE, scope_json TEXT NOT NULL, created_at TEXT NOT NULL, expires_at TEXT NOT NULL, last_used_at TEXT, revoked_at TEXT);
CREATE TABLE IF NOT EXISTS tool_calls (id TEXT PRIMARY KEY, issue_id TEXT NOT NULL, run_id TEXT NOT NULL, tool_name TEXT NOT NULL, status TEXT NOT NULL, input_hash TEXT, input_json_redacted TEXT, output_hash TEXT, output_json_redacted TEXT, error_code TEXT, error_message TEXT, started_at TEXT NOT NULL, ended_at TEXT);
CREATE TABLE IF NOT EXISTS handoffs (id TEXT PRIMARY KEY, issue_id TEXT NOT NULL, run_id TEXT NOT NULL UNIQUE, payload_hash TEXT NOT NULL, payload_json_redacted TEXT NOT NULL, summary TEXT NOT NULL, changed_files_json TEXT NOT NULL DEFAULT '[]', tests_json TEXT NOT NULL DEFAULT '[]', risks_json TEXT NOT NULL DEFAULT '[]', verification_json TEXT NOT NULL DEFAULT '[]', followups_json TEXT NOT NULL DEFAULT '[]', target_state TEXT NOT NULL DEFAULT 'Human Review' CHECK(target_state='Human Review'), submitted_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS artifacts (id TEXT PRIMARY KEY, issue_id TEXT, run_id TEXT, review_packet_id TEXT, kind TEXT NOT NULL, path TEXT NOT NULL, mime_type TEXT, size_bytes INTEGER, sha256 TEXT, redacted INTEGER NOT NULL DEFAULT 1, description TEXT, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS prompt_snapshots (id TEXT PRIMARY KEY, run_id TEXT NOT NULL UNIQUE, workflow_snapshot_id TEXT, runtime_envelope_version TEXT NOT NULL, tool_manifest_version TEXT NOT NULL, context_hash TEXT NOT NULL, rendered_prompt_hash TEXT NOT NULL, context_json_path TEXT NOT NULL, redacted_prompt_path TEXT NOT NULL, prompt_meta_json_path TEXT NOT NULL, tool_manifest_path TEXT NOT NULL, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS review_packets (id TEXT PRIMARY KEY, issue_id TEXT NOT NULL, run_id TEXT NOT NULL UNIQUE, handoff_id TEXT NOT NULL, packet_no INTEGER NOT NULL CHECK(packet_no>0), status TEXT NOT NULL, root_path TEXT NOT NULL, review_md_path TEXT, review_json_path TEXT, patch_path TEXT, changed_files_path TEXT, untracked_files_path TEXT, diffstat_path TEXT, prompt_snapshot_id TEXT, failure_code TEXT, failure_message TEXT, created_at TEXT NOT NULL, UNIQUE(issue_id, packet_no));
`

func ReadSchema(root, rel string) (string, error) {
	candidates := []string{
		filepath.Join(root, rel),
		filepath.Join(findModuleRoot(root), rel),
	}
	for _, p := range candidates {
		if b, err := os.ReadFile(p); err == nil {
			return string(b), nil
		}
	}
	if rel == "db/schema/v1_app.sql" {
		return fallbackAppSchema, nil
	}
	if rel == "db/schema/v1_project.sql" {
		return fallbackProjectSchema, nil
	}
	return "", os.ErrNotExist
}

func findModuleRoot(start string) string {
	cur, _ := filepath.Abs(start)
	for {
		if _, err := os.Stat(filepath.Join(cur, "go.mod")); err == nil {
			return cur
		}
		next := filepath.Dir(cur)
		if next == cur {
			return start
		}
		cur = next
	}
}

// runtimeOwnerNonceColumns is the schema version "2" upgrade for runtime_descriptors.
// Each ALTER is idempotent because ALTER TABLE ADD COLUMN fails fast if the column
// already exists; the migration helper only attempts columns that PRAGMA table_info
// reports missing.
var runtimeOwnerNonceColumns = []struct {
	Name    string
	DDL     string
	Default string
}{
	{Name: "owner_nonce", DDL: "ALTER TABLE runtime_descriptors ADD COLUMN owner_nonce TEXT NOT NULL DEFAULT ''", Default: "''"},
	{Name: "heartbeat_at", DDL: "ALTER TABLE runtime_descriptors ADD COLUMN heartbeat_at INTEGER NOT NULL DEFAULT 0", Default: "0"},
	{Name: "heartbeat_ttl_ms", DDL: "ALTER TABLE runtime_descriptors ADD COLUMN heartbeat_ttl_ms INTEGER NOT NULL DEFAULT 30000", Default: "30000"},
	{Name: "acquired_at", DDL: "ALTER TABLE runtime_descriptors ADD COLUMN acquired_at INTEGER NOT NULL DEFAULT 0", Default: "0"},
}

// MigrateAppSchema brings an existing v1 app DB up to the current runtime_descriptors
// schema. It is idempotent and only inspects runtime_descriptors because the only
// in-place schema evolution is the owner nonce / heartbeat columns. New app DBs
// are created with the full schema from fallbackAppSchema / v1_app.sql and do not
// need to call this.
func MigrateAppSchema(database *DB) error {
	hasTable, err := tableExists(database, "runtime_descriptors")
	if err != nil {
		return err
	}
	if hasTable {
		cols, err := tableColumnSet(database, "runtime_descriptors")
		if err != nil {
			return err
		}
		for _, col := range runtimeOwnerNonceColumns {
			if _, ok := cols[col.Name]; ok {
				continue
			}
			if err := database.Exec(col.DDL); err != nil {
				return fmt.Errorf("add column %s: %w", col.Name, err)
			}
		}
	}
	if err := ensureRuntimeOwnerEventsTable(database); err != nil {
		return err
	}
	return nil
}

const runtimeOwnerEventsDDL = `CREATE TABLE IF NOT EXISTS runtime_owner_events (id TEXT PRIMARY KEY, project_id TEXT NOT NULL, event_type TEXT NOT NULL, actor_type TEXT NOT NULL, data_json TEXT NOT NULL, redacted INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL, FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE);
CREATE INDEX IF NOT EXISTS idx_runtime_owner_events_project ON runtime_owner_events(project_id, created_at);
`

func ensureRuntimeOwnerEventsTable(database *DB) error {
	exists, err := tableExists(database, "runtime_owner_events")
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return database.ExecScript(runtimeOwnerEventsDDL)
}

func tableExists(database *DB, name string) (bool, error) {
	row, err := database.QueryOne(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name)
	if err != nil {
		if isMissingRow(err) {
			return false, nil
		}
		return false, err
	}
	if row == nil {
		return false, nil
	}
	return row["name"].String() == name, nil
}

func tableColumnSet(database *DB, name string) (map[string]struct{}, error) {
	rows, err := database.Query(`PRAGMA table_info(` + name + `)`)
	if err != nil {
		return nil, err
	}
	cols := map[string]struct{}{}
	for _, r := range rows {
		cols[r["name"].String()] = struct{}{}
	}
	return cols, nil
}

func isMissingRow(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}

// MigrateProjectSchema brings an existing v1 project DB up to the
// current artifacts.kind CHECK enum. It is idempotent: a fresh
// project DB is created with the full CHECK from v1_project.sql /
// fallbackProjectSchema, so a no-op call must remain safe. The
// in-place evolution is the artifacts CHECK widening performed by
// D1's review packet work — new kinds (codex_events, prompt_rendered,
// prompt_context, prompt_meta, prompt_tool_manifest, secret_artifact,
// secrets) are appended to the enum so the dashboard's refusal flow
// (content_url=null) can be exercised end to end.
//
// SQLite has no ALTER TABLE … ALTER CONSTRAINT, so a widened CHECK
// requires rebuilding the artifacts table. The migration copies all
// rows from the old artifacts table into a freshly-declared
// artifacts table that uses the new CHECK, then drops the old
// table. Foreign keys on issue_id / run_id / review_packet_id are
// rewritten as nullable (ON DELETE SET NULL), matching the v1
// production schema. The legacy_kind_probe insert is used as the
// idempotency sentinel: if it already succeeds, the CHECK is
// already wide enough and the migration returns early.
func MigrateProjectSchema(database *DB) error {
	hasArtifacts, err := tableExists(database, "artifacts")
	if err != nil {
		return err
	}
	if !hasArtifacts {
		return nil
	}
	if artifactsKindProbe(database) == nil {
		return nil
	}

	rebuild := `
PRAGMA foreign_keys = OFF;
BEGIN TRANSACTION;
CREATE TABLE artifacts_new (
  id TEXT PRIMARY KEY,
  issue_id TEXT,
  run_id TEXT,
  review_packet_id TEXT,
  kind TEXT NOT NULL CHECK (kind IN ('test_output','patch','changed_files','untracked_files','diffstat','prompt_snapshot','prompt_rendered','prompt_context','prompt_meta','prompt_tool_manifest','codex_log','codex_events','secret_artifact','secrets','review_packet','agent_file','diagnostic','other')),
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
INSERT INTO artifacts_new(id,issue_id,run_id,review_packet_id,kind,path,mime_type,size_bytes,sha256,redacted,description,created_at)
  SELECT id,issue_id,run_id,review_packet_id,kind,path,mime_type,size_bytes,sha256,redacted,description,created_at FROM artifacts;
DROP TABLE artifacts;
ALTER TABLE artifacts_new RENAME TO artifacts;
CREATE INDEX IF NOT EXISTS idx_artifacts_run_kind ON artifacts(run_id, kind);
CREATE INDEX IF NOT EXISTS idx_artifacts_issue_kind ON artifacts(issue_id, kind);
COMMIT;
PRAGMA foreign_keys = ON;
`
	if err := database.ExecScript(rebuild); err != nil {
		return fmt.Errorf("rebuild artifacts with widened kind CHECK: %w", err)
	}
	if artifactsKindProbe(database) != nil {
		return fmt.Errorf("rebuild succeeded but artifacts.kind CHECK still rejects new kinds")
	}
	return nil
}

// artifactsKindProbe attempts to insert a sentinel row with one of
// the new artifact kinds. If the insert succeeds the CHECK already
// permits the new kinds and the migration is a no-op. The sentinel
// row is rolled back inside the same transaction so callers do not
// observe it.
func artifactsKindProbe(database *DB) error {
	return database.WithTx(func(tx *Tx) error {
		if err := tx.Exec(
			`INSERT INTO artifacts(id, kind, path, redacted, created_at) VALUES('art_kind_probe','codex_events','probe',1,'2026-06-09T00:00:00Z')`,
		); err != nil {
			return err
		}
		return tx.Exec(`DELETE FROM artifacts WHERE id='art_kind_probe'`)
	})
}
