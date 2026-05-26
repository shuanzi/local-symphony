package db

import (
	"path/filepath"
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

func assertExecFails(t *testing.T, d *DB, sql string) {
	t.Helper()
	if err := d.Exec(sql); err == nil {
		t.Fatalf("Exec succeeded, want constraint failure for %s", sql)
	}
}
