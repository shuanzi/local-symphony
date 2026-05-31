package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-symphony/internal/core"
	"local-symphony/internal/db"
)

func TestInitProjectClosesDBsAfterInitInsertFailureCanRetry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoRoot := filepath.Join(t.TempDir(), "repo")
	schemaDir := filepath.Join(repoRoot, "db", "schema")
	if err := os.MkdirAll(schemaDir, 0o755); err != nil {
		t.Fatalf("mkdir schema dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(schemaDir, "v1_project.sql"), []byte(`
CREATE TABLE project_info (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	repo_root TEXT NOT NULL,
	issue_prefix TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TRIGGER fail_project_info_insert BEFORE INSERT ON project_info BEGIN SELECT RAISE(ABORT, 'init insert failed'); END;
`), 0o644); err != nil {
		t.Fatalf("write bad schema: %v", err)
	}

	if st, err := InitProject(repoRoot, "TST"); err == nil {
		st.Close()
		t.Fatal("InitProject succeeded, want insert failure")
	}
	assertNoOpenSQLiteFile(t, filepath.Join(repoRoot, ".symphony", "project.db"))
	assertNoOpenSQLiteFile(t, db.AppDBPath())

	if err := os.RemoveAll(filepath.Join(repoRoot, "db")); err != nil {
		t.Fatalf("remove bad schema: %v", err)
	}
	removeSQLiteFiles(t, filepath.Join(repoRoot, ".symphony", "project.db"))
	removeSQLiteFiles(t, db.AppDBPath())

	st, err := InitProject(repoRoot, "TST")
	if err != nil {
		t.Fatalf("InitProject retry: %v", err)
	}
	st.Close()
}

func TestOpenReturnsProjectInfoQueryError(t *testing.T) {
	tests := []struct {
		name    string
		breakDB func(t *testing.T, st *Store)
	}{
		{
			name: "missing table",
			breakDB: func(t *testing.T, st *Store) {
				t.Helper()
				if err := st.Project.Exec(`DROP TABLE project_info`); err != nil {
					t.Fatalf("drop project_info: %v", err)
				}
			},
		},
		{
			name: "empty table",
			breakDB: func(t *testing.T, st *Store) {
				t.Helper()
				if err := st.Project.Exec(`DELETE FROM project_info`); err != nil {
					t.Fatalf("delete project_info: %v", err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			repoRoot := filepath.Join(t.TempDir(), "repo")
			st, err := InitProject(repoRoot, "TST")
			if err != nil {
				t.Fatalf("InitProject: %v", err)
			}
			tt.breakDB(t, st)
			st.Close()

			opened, err := Open(repoRoot)
			if err == nil {
				opened.Close()
				t.Fatal("Open succeeded, want project_info query error")
			}
			assertNoOpenSQLiteFile(t, filepath.Join(repoRoot, ".symphony", "project.db"))
			assertNoOpenSQLiteFile(t, db.AppDBPath())

			removeSQLiteFiles(t, filepath.Join(repoRoot, ".symphony", "project.db"))
			removeSQLiteFiles(t, db.AppDBPath())
		})
	}
}

func TestOpenRepopulatesProjectMetadataAfterAppDBRebuild(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoRoot := filepath.Join(t.TempDir(), "repo")
	st, err := InitProject(repoRoot, "TST")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	appDBPath := st.AppDBPath
	st.Close()

	removeSQLiteFiles(t, appDBPath)
	opened, err := Open(repoRoot)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer opened.Close()

	rows, err := opened.App.Query(`SELECT id, repo_root, issue_prefix FROM projects WHERE id=?`, opened.ProjectID)
	if err != nil {
		t.Fatalf("query projects: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("project rows = %d, want 1", len(rows))
	}
	if got := rows[0]["repo_root"].String(); got != opened.RepoRoot {
		t.Fatalf("repo_root = %q, want %q", got, opened.RepoRoot)
	}
	if got := rows[0]["issue_prefix"].String(); got != "TST" {
		t.Fatalf("issue_prefix = %q, want TST", got)
	}
	if err := opened.App.Exec(`INSERT INTO local_sessions(id,project_id,kind,token_hash,user_label,created_at) VALUES(?,?,?,?,?,?)`, core.NewID("ses_"), opened.ProjectID, "cli", "token_hash", "test-session", core.Now()); err != nil {
		t.Fatalf("insert local session after app DB rebuild: %v", err)
	}
}

func TestOpenRejectsUnsupportedProjectSchemaVersion(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoRoot := filepath.Join(t.TempDir(), "repo")
	st, err := InitProject(repoRoot, "TST")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	if err := st.Project.Exec(`UPDATE schema_meta SET value='2' WHERE key='schema_version'`); err != nil {
		t.Fatalf("update project schema version: %v", err)
	}
	projectDBPath := st.ProjectDBPath
	appDBPath := st.AppDBPath
	st.Close()

	opened, err := Open(repoRoot)
	if err == nil {
		opened.Close()
		t.Fatal("Open succeeded, want unsupported project schema version")
	}
	apiErr := core.AsAPIError(err)
	if apiErr.Code != core.ErrUnsupportedDBVersion {
		t.Fatalf("Open error code = %s, want %s", apiErr.Code, core.ErrUnsupportedDBVersion)
	}
	assertUnsupportedDBVersionDetails(t, apiErr, projectDBPath, "2")
	assertNoOpenSQLiteFile(t, projectDBPath)
	assertNoOpenSQLiteFile(t, appDBPath)
}

func TestOpenRejectsUnsupportedAppSchemaVersion(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoRoot := filepath.Join(t.TempDir(), "repo")
	st, err := InitProject(repoRoot, "TST")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	if err := st.App.Exec(`UPDATE schema_meta SET value='0' WHERE key='schema_version'`); err != nil {
		t.Fatalf("update app schema version: %v", err)
	}
	projectDBPath := st.ProjectDBPath
	appDBPath := st.AppDBPath
	st.Close()

	opened, err := Open(repoRoot)
	if err == nil {
		opened.Close()
		t.Fatal("Open succeeded, want unsupported app schema version")
	}
	apiErr := core.AsAPIError(err)
	if apiErr.Code != core.ErrUnsupportedDBVersion {
		t.Fatalf("Open error code = %s, want %s", apiErr.Code, core.ErrUnsupportedDBVersion)
	}
	assertUnsupportedDBVersionDetails(t, apiErr, appDBPath, "0")
	assertNoOpenSQLiteFile(t, projectDBPath)
	assertNoOpenSQLiteFile(t, appDBPath)
}

func TestOpenRejectsMissingAndUnparseableProjectSchemaVersion(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(t *testing.T, st *Store)
		detected string
	}{
		{
			name: "missing schema_version",
			mutate: func(t *testing.T, st *Store) {
				t.Helper()
				if err := st.Project.Exec(`DELETE FROM schema_meta WHERE key='schema_version'`); err != nil {
					t.Fatalf("delete schema version: %v", err)
				}
			},
			detected: "missing_schema_version",
		},
		{
			name: "unparseable schema_version",
			mutate: func(t *testing.T, st *Store) {
				t.Helper()
				if err := st.Project.Exec(`UPDATE schema_meta SET value='v1' WHERE key='schema_version'`); err != nil {
					t.Fatalf("update schema version: %v", err)
				}
			},
			detected: "v1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			repoRoot := filepath.Join(t.TempDir(), "repo")
			st, err := InitProject(repoRoot, "TST")
			if err != nil {
				t.Fatalf("InitProject: %v", err)
			}
			tt.mutate(t, st)
			projectDBPath := st.ProjectDBPath
			st.Close()

			opened, err := Open(repoRoot)
			if err == nil {
				opened.Close()
				t.Fatal("Open succeeded, want unsupported project schema version")
			}
			apiErr := core.AsAPIError(err)
			if apiErr.Code != core.ErrUnsupportedDBVersion {
				t.Fatalf("Open error code = %s, want %s", apiErr.Code, core.ErrUnsupportedDBVersion)
			}
			assertUnsupportedDBVersionDetails(t, apiErr, projectDBPath, tt.detected)
			assertNoOpenSQLiteFile(t, projectDBPath)
		})
	}
}

func TestInitProjectDoesNotRepairExistingProjectDBWithoutSchemaMeta(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoRoot := filepath.Join(t.TempDir(), "repo")
	st, err := InitProject(repoRoot, "TST")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	projectDBPath := st.ProjectDBPath
	if err := st.Project.Exec(`DROP TABLE schema_meta`); err != nil {
		t.Fatalf("drop schema_meta: %v", err)
	}
	st.Close()

	opened, err := InitProject(repoRoot, "TST")
	if err == nil {
		opened.Close()
		t.Fatal("InitProject succeeded, want unsupported DB version")
	}
	apiErr := core.AsAPIError(err)
	if apiErr.Code != core.ErrUnsupportedDBVersion {
		t.Fatalf("InitProject error code = %s, want %s", apiErr.Code, core.ErrUnsupportedDBVersion)
	}
	assertUnsupportedDBVersionDetails(t, apiErr, projectDBPath, "missing_schema_meta")

	raw, err := db.Open(projectDBPath)
	if err != nil {
		t.Fatalf("open raw project db: %v", err)
	}
	defer raw.Close()
	if _, err := raw.QueryOne(`SELECT value FROM schema_meta WHERE key='schema_version'`); err == nil {
		t.Fatal("schema_meta was repaired, want existing DB left unchanged")
	}
}

func TestOpenInitializesMissingAppDB(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoRoot := filepath.Join(t.TempDir(), "repo")
	st, err := InitProject(repoRoot, "TST")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	appDBPath := st.AppDBPath
	st.Close()
	removeSQLiteFiles(t, appDBPath)

	opened, err := Open(repoRoot)
	if err != nil {
		t.Fatalf("Open with missing app DB: %v", err)
	}
	defer opened.Close()
	row, err := opened.App.QueryOne(`SELECT value FROM schema_meta WHERE key='schema_version'`)
	if err != nil {
		t.Fatalf("read app schema version: %v", err)
	}
	if got := row["value"].String(); got != "1" {
		t.Fatalf("app schema version = %q, want 1", got)
	}
}

func TestGetAndListIssuesPropagateLabelQueryError(t *testing.T) {
	st := newStoreTestStore(t)
	issue, err := st.CreateIssue(CreateIssueInput{
		Title:              "Label query failure",
		Description:        "desc",
		AcceptanceCriteria: []string{"done"},
		Priority:           3,
		Labels:             []string{"store"},
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := st.Project.Exec(`DROP TABLE issue_labels`); err != nil {
		t.Fatalf("drop issue_labels: %v", err)
	}

	if _, err := st.GetIssue(issue.ID); err == nil {
		t.Fatal("GetIssue succeeded, want label query error")
	}
	if _, err := st.ListIssues(ListIssueOptions{}); err == nil {
		t.Fatal("ListIssues succeeded, want label query error")
	}
}

func TestGetIssuePropagatesActiveRunLookupError(t *testing.T) {
	st := newStoreTestStore(t)
	issue, err := st.CreateIssue(CreateIssueInput{
		Title:              "Active run lookup failure",
		Description:        "desc",
		AcceptanceCriteria: []string{"done"},
		Priority:           3,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := st.Project.Exec(`ALTER TABLE run_attempts RENAME TO run_attempts_shadow`); err != nil {
		t.Fatalf("rename run_attempts: %v", err)
	}
	if err := st.Project.Exec(`CREATE TABLE run_attempts (id TEXT PRIMARY KEY, issue_id TEXT NOT NULL, attempt_no INTEGER NOT NULL, status TEXT NOT NULL)`); err != nil {
		t.Fatalf("create malformed run_attempts: %v", err)
	}
	if err := st.Project.Exec(`INSERT INTO run_attempts(id, issue_id, attempt_no, status) VALUES(?,?,?,?)`, "run_active_lookup", issue.ID, 1, string(core.RunCompleted)); err != nil {
		t.Fatalf("insert malformed run_attempts row: %v", err)
	}

	if _, err := st.GetIssue(issue.ID); err == nil {
		t.Fatal("GetIssue succeeded, want active run lookup error")
	}
}

func TestGetIssuePropagatesHydrationQueryErrors(t *testing.T) {
	tests := []struct {
		name      string
		tableName string
	}{
		{name: "workspace summary", tableName: "workspaces"},
		{name: "latest review packet", tableName: "review_packets"},
		{name: "relations", tableName: "issue_relations"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := newStoreTestStore(t)
			issue, err := st.CreateIssue(CreateIssueInput{
				Title:              "Hydration query failure",
				Description:        "desc",
				AcceptanceCriteria: []string{"done"},
				Priority:           3,
			})
			if err != nil {
				t.Fatalf("CreateIssue: %v", err)
			}
			if err := st.Project.Exec(`DROP TABLE ` + tt.tableName); err != nil {
				t.Fatalf("drop %s: %v", tt.tableName, err)
			}

			if _, err := st.GetIssue(issue.ID); err == nil {
				t.Fatalf("GetIssue succeeded after dropping %s, want hydration query error", tt.tableName)
			}
		})
	}
}

func TestCancelRunRejectsCompletedRunWithoutChangingIssue(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareCompletedReviewRun(t, st)

	err := st.CancelRun(run.ID, "operator changed their mind")
	if err == nil {
		t.Fatal("CancelRun succeeded, want invalid_state_transition")
	}
	if got := core.AsAPIError(err).Code; got != core.ErrInvalidStateTransition {
		t.Fatalf("CancelRun error code = %s, want %s", got, core.ErrInvalidStateTransition)
	}

	gotRun, err := st.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if gotRun.Status != core.RunCompleted {
		t.Fatalf("run status = %s, want %s", gotRun.Status, core.RunCompleted)
	}

	gotIssue, err := st.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if gotIssue.State != core.StateHumanReview {
		t.Fatalf("issue state = %s, want %s", gotIssue.State, core.StateHumanReview)
	}
}

func TestFailRunRejectsCompletedRunWithoutChangingRunOrIssue(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareCompletedReviewRun(t, st)

	err := st.FailRun(run.ID, core.FailureToolGatewayFailed, "stale failure", core.RunFailed)
	if err == nil {
		t.Fatal("FailRun succeeded, want invalid_state_transition")
	}
	if got := core.AsAPIError(err).Code; got != core.ErrInvalidStateTransition {
		t.Fatalf("FailRun error code = %s, want %s", got, core.ErrInvalidStateTransition)
	}
	assertRunStatus(t, st, run.ID, core.RunCompleted)
	assertIssueState(t, st, issue.ID, core.StateHumanReview)
}

func TestFailRunRejectsCancelledRunWithoutChangingRunOrIssue(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareCancelledRun(t, st)

	err := st.FailRun(run.ID, core.FailureToolGatewayFailed, "stale failure", core.RunFailed)
	if err == nil {
		t.Fatal("FailRun succeeded, want invalid_state_transition")
	}
	if got := core.AsAPIError(err).Code; got != core.ErrInvalidStateTransition {
		t.Fatalf("FailRun error code = %s, want %s", got, core.ErrInvalidStateTransition)
	}
	assertRunStatus(t, st, run.ID, core.RunCancelled)
	assertIssueState(t, st, issue.ID, core.StateReady)
}

func TestFailRunRollsBackWhenAuditCommentInsertFails(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareActiveRun(t, st, "FailRun comment failure")
	if err := st.Project.Exec(`CREATE TRIGGER fail_run_failure_comment BEFORE INSERT ON issue_comments WHEN NEW.author_type='system' AND NEW.body LIKE 'Run ended with %' BEGIN SELECT RAISE(ABORT, 'failure comment failed'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	err := st.FailRun(run.ID, core.FailureToolGatewayFailed, "gateway failed", core.RunFailed)
	if err == nil {
		t.Fatal("FailRun succeeded, want audit comment error")
	}
	assertRunStatus(t, st, run.ID, core.RunPending)
	assertIssueState(t, st, issue.ID, core.StateWorking)
	assertNoRunFailureAudit(t, st, run.ID)
}

func TestFailRunRollsBackWhenAuditEventInsertFails(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareActiveRun(t, st, "FailRun event failure")
	if err := st.Project.Exec(`CREATE TRIGGER fail_run_failed_event BEFORE INSERT ON run_events WHEN NEW.event_type='run.failed' BEGIN SELECT RAISE(ABORT, 'run failed event failed'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	err := st.FailRun(run.ID, core.FailureToolGatewayFailed, "gateway failed", core.RunFailed)
	if err == nil {
		t.Fatal("FailRun succeeded, want audit event error")
	}
	assertRunStatus(t, st, run.ID, core.RunPending)
	assertIssueState(t, st, issue.ID, core.StateWorking)
	assertNoRunFailureAudit(t, st, run.ID)
}

func TestFailRunRollsBackWhenSchedulerPausedEventInsertFails(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareActiveRun(t, st, "FailRun scheduler event failure")
	if err := st.Project.Exec(`CREATE TRIGGER fail_scheduler_paused_event BEFORE INSERT ON run_events WHEN NEW.event_type='scheduler.paused' BEGIN SELECT RAISE(ABORT, 'scheduler paused event failed'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	err := st.FailRun(run.ID, core.FailureToolGatewayFailed, "gateway failed", core.RunFailed)
	if err == nil {
		t.Fatal("FailRun succeeded, want scheduler event error")
	}
	assertRunStatus(t, st, run.ID, core.RunPending)
	assertIssueState(t, st, issue.ID, core.StateWorking)
	assertNoRunFailureAudit(t, st, run.ID)
}

func TestCompleteRunWithReviewRejectsCompletedRunWithoutChangingRunOrIssue(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareCompletedReviewRun(t, st)
	reviewPacketID := latestReviewPacketID(t, st, run.ID)

	err := st.CompleteRunWithReview(run.ID, reviewPacketID)
	if err == nil {
		t.Fatal("CompleteRunWithReview succeeded, want invalid_state_transition")
	}
	if got := core.AsAPIError(err).Code; got != core.ErrInvalidStateTransition {
		t.Fatalf("CompleteRunWithReview error code = %s, want %s", got, core.ErrInvalidStateTransition)
	}
	assertRunStatus(t, st, run.ID, core.RunCompleted)
	assertIssueState(t, st, issue.ID, core.StateHumanReview)
}

func TestCompleteRunWithReviewRejectsCancelledRunWithoutChangingRunOrIssue(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareCancelledRun(t, st)
	reviewPacketID := insertReviewPacketForRun(t, st, issue.ID, run.ID)

	err := st.CompleteRunWithReview(run.ID, reviewPacketID)
	if err == nil {
		t.Fatal("CompleteRunWithReview succeeded, want invalid_state_transition")
	}
	if got := core.AsAPIError(err).Code; got != core.ErrInvalidStateTransition {
		t.Fatalf("CompleteRunWithReview error code = %s, want %s", got, core.ErrInvalidStateTransition)
	}
	assertRunStatus(t, st, run.ID, core.RunCancelled)
	assertIssueState(t, st, issue.ID, core.StateReady)
}

func TestCompleteRunWithReviewRollsBackWhenStateHistoryInsertFails(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareActiveRun(t, st, "CompleteRunWithReview history failure")
	reviewPacketID := insertReviewPacketForRun(t, st, issue.ID, run.ID)
	if err := st.Project.Exec(`CREATE TRIGGER fail_complete_review_history BEFORE INSERT ON issue_state_history WHEN NEW.reason='review packet generated' BEGIN SELECT RAISE(ABORT, 'review history failed'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	err := st.CompleteRunWithReview(run.ID, reviewPacketID)
	if err == nil {
		t.Fatal("CompleteRunWithReview succeeded, want state history error")
	}
	assertRunStatus(t, st, run.ID, core.RunPending)
	assertIssueState(t, st, issue.ID, core.StateWorking)
	assertIssueLatestReviewPacketID(t, st, issue.ID, &reviewPacketID)
	assertNoReviewGeneratedEvent(t, st, run.ID)
}

func TestCompleteRunWithReviewRollsBackWhenEventInsertFails(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareActiveRun(t, st, "CompleteRunWithReview event failure")
	reviewPacketID := insertReviewPacketForRun(t, st, issue.ID, run.ID)
	if err := st.Project.Exec(`CREATE TRIGGER fail_complete_review_event BEFORE INSERT ON run_events WHEN NEW.event_type='review.packet_generated' BEGIN SELECT RAISE(ABORT, 'review event failed'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	err := st.CompleteRunWithReview(run.ID, reviewPacketID)
	if err == nil {
		t.Fatal("CompleteRunWithReview succeeded, want event insert error")
	}
	assertRunStatus(t, st, run.ID, core.RunPending)
	assertIssueState(t, st, issue.ID, core.StateWorking)
	assertIssueLatestReviewPacketID(t, st, issue.ID, &reviewPacketID)
	assertNoReviewGeneratedEvent(t, st, run.ID)
}

func TestCompleteRunWithReviewRejectsReviewPacketForDifferentRun(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareActiveRunWithMaxConcurrent(t, st, "CompleteRunWithReview wrong run", 2)
	otherIssue, otherRun := prepareActiveRunWithMaxConcurrent(t, st, "CompleteRunWithReview packet owner", 2)
	reviewPacketID := insertReviewPacketForRun(t, st, otherIssue.ID, otherRun.ID)

	err := st.CompleteRunWithReview(run.ID, reviewPacketID)
	if err == nil {
		t.Fatal("CompleteRunWithReview succeeded, want review packet ownership error")
	}
	assertRunStatus(t, st, run.ID, core.RunPending)
	assertIssueState(t, st, issue.ID, core.StateWorking)
	assertIssueLatestReviewPacketID(t, st, issue.ID, nil)
}

func TestCompleteRunWithReviewRejectsNonGeneratedReviewPacket(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareActiveRun(t, st, "CompleteRunWithReview failed packet")
	reviewPacketID := insertReviewPacketForRun(t, st, issue.ID, run.ID)
	if err := st.Project.Exec(`UPDATE review_packets SET status='failed' WHERE id=?`, reviewPacketID); err != nil {
		t.Fatalf("mark review packet failed: %v", err)
	}

	err := st.CompleteRunWithReview(run.ID, reviewPacketID)
	if err == nil {
		t.Fatal("CompleteRunWithReview succeeded, want review packet status error")
	}
	assertRunStatus(t, st, run.ID, core.RunPending)
	assertIssueState(t, st, issue.ID, core.StateWorking)
	assertIssueLatestReviewPacketID(t, st, issue.ID, &reviewPacketID)
}

func TestMarkDoneRollsBackWhenAuditCommentInsertFails(t *testing.T) {
	st := newStoreTestStore(t)
	issue, _ := prepareCompletedReviewRun(t, st)
	if err := st.Project.Exec(`CREATE TRIGGER fail_review_audit_comment BEFORE INSERT ON issue_comments WHEN NEW.author_type='operator' AND NEW.body='mark done' BEGIN SELECT RAISE(ABORT, 'review audit comment failed'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	_, err := st.MarkDone(issue.ID, "mark done")
	if err == nil {
		t.Fatal("MarkDone succeeded, want audit insert error")
	}
	assertIssueState(t, st, issue.ID, core.StateHumanReview)
	row, err := st.Project.QueryOne(`SELECT completed_at FROM issues WHERE id=?`, issue.ID)
	if err != nil {
		t.Fatalf("get issue completed_at: %v", err)
	}
	if row["completed_at"].String() != "" {
		t.Fatalf("completed_at = %q, want empty after rollback", row["completed_at"].String())
	}
	rows, err := st.Project.Query(`SELECT id FROM run_events WHERE issue_id=? AND event_type IN ('review.marked_done','issue.completed')`, issue.ID)
	if err != nil {
		t.Fatalf("query review events: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("review completion events = %d, want 0 after rollback", len(rows))
	}
}

func TestReviewActionRollsBackWhenAuditEventInsertFails(t *testing.T) {
	tests := []struct {
		name       string
		action     string
		failEvent  string
		reason     string
		wantStatus core.IssueState
	}{
		{name: "mark done review event", action: "done", failEvent: "review.marked_done", reason: "mark done event", wantStatus: core.StateHumanReview},
		{name: "mark done completed event", action: "done", failEvent: "issue.completed", reason: "completed event", wantStatus: core.StateHumanReview},
		{name: "send to rework event", action: "rework", failEvent: "review.sent_to_rework", reason: "rework event", wantStatus: core.StateHumanReview},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := newStoreTestStore(t)
			issue, _ := prepareCompletedReviewRun(t, st)
			trigger := `CREATE TRIGGER fail_review_audit_event BEFORE INSERT ON run_events WHEN NEW.event_type='` + tt.failEvent + `' BEGIN SELECT RAISE(ABORT, 'review audit event failed'); END`
			if err := st.Project.Exec(trigger); err != nil {
				t.Fatalf("create trigger: %v", err)
			}

			var err error
			switch tt.action {
			case "done":
				_, err = st.MarkDone(issue.ID, tt.reason)
			case "rework":
				_, err = st.SendToRework(issue.ID, tt.reason)
			default:
				t.Fatalf("unknown action %q", tt.action)
			}
			if err == nil {
				t.Fatal("review action succeeded, want audit event error")
			}
			assertIssueState(t, st, issue.ID, tt.wantStatus)
			row, err := st.Project.QueryOne(`SELECT completed_at FROM issues WHERE id=?`, issue.ID)
			if err != nil {
				t.Fatalf("get issue completed_at: %v", err)
			}
			if row["completed_at"].String() != "" {
				t.Fatalf("completed_at = %q, want empty after rollback", row["completed_at"].String())
			}
			rows, err := st.Project.Query(`SELECT id FROM run_events WHERE issue_id=? AND event_type IN ('review.marked_done','issue.completed','review.sent_to_rework')`, issue.ID)
			if err != nil {
				t.Fatalf("query review events: %v", err)
			}
			if len(rows) != 0 {
				t.Fatalf("review action events = %d, want 0 after rollback", len(rows))
			}
		})
	}
}

func TestMarkDonePropagatesActiveRunLookupErrorWithoutChangingIssue(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareCompletedReviewRun(t, st)
	poisonRunID := core.NewID("run_")
	now := core.Now()
	if err := st.Project.Exec(`ALTER TABLE run_attempts RENAME TO run_attempts_shadow`); err != nil {
		t.Fatalf("rename run_attempts: %v", err)
	}
	if err := st.Project.Exec(`INSERT INTO run_attempts_shadow(id,issue_id,attempt_no,status,dispatch_reason,source_issue_state,runner_kind,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, poisonRunID, issue.ID, 2, string(core.RunPending), "manual", string(core.StateReady), "fake", now, now); err != nil {
		t.Fatalf("insert poison run: %v", err)
	}
	if err := st.Project.Exec(`CREATE INDEX idx_run_attempts_shadow_issue ON run_attempts_shadow(issue_id)`); err != nil {
		t.Fatalf("create shadow issue index: %v", err)
	}
	if err := st.Project.Exec(`CREATE VIEW run_attempts AS SELECT id,issue_id,attempt_no,workspace_id,workflow_snapshot_id,CASE WHEN id='` + poisonRunID + `' THEN json_extract('bad json','$.x') ELSE status END AS status,dispatch_reason,source_issue_state,runner_kind,base_ref_config,base_ref,base_sha,branch_name,failure_code,failure_message,started_at,ended_at,created_at,updated_at FROM run_attempts_shadow`); err != nil {
		t.Fatalf("create run_attempts view: %v", err)
	}

	_, err := st.MarkDone(issue.ID, "active lookup should fail")

	if dropErr := st.Project.Exec(`DROP VIEW run_attempts`); dropErr != nil {
		t.Fatalf("drop run_attempts view: %v", dropErr)
	}
	if deleteErr := st.Project.Exec(`DELETE FROM run_attempts_shadow WHERE id=?`, poisonRunID); deleteErr != nil {
		t.Fatalf("delete poison run: %v", deleteErr)
	}
	if restoreErr := st.Project.Exec(`ALTER TABLE run_attempts_shadow RENAME TO run_attempts`); restoreErr != nil {
		t.Fatalf("restore run_attempts: %v", restoreErr)
	}
	if err == nil {
		t.Fatal("MarkDone succeeded, want active run lookup error")
	}
	assertErrorContains(t, err, "malformed JSON")
	assertIssueState(t, st, issue.ID, core.StateHumanReview)
	assertRunStatus(t, st, run.ID, core.RunCompleted)
	rows, qerr := st.Project.Query(`SELECT id FROM run_events WHERE issue_id=? AND event_type IN ('review.marked_done','issue.completed')`, issue.ID)
	if qerr != nil {
		t.Fatalf("query review events: %v", qerr)
	}
	if len(rows) != 0 {
		t.Fatalf("review completion events = %d, want 0", len(rows))
	}
}

func TestReviewActionRejectsActiveRunInsertedDuringUpdate(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareCompletedReviewRun(t, st)
	if err := st.Project.Exec(`CREATE TRIGGER race_review_active_run BEFORE UPDATE OF state ON issues
WHEN NEW.id='` + issue.ID + `' AND NEW.state='Done'
BEGIN
	INSERT INTO run_attempts(id,issue_id,attempt_no,status,dispatch_reason,source_issue_state,runner_kind,created_at,updated_at)
	VALUES('run_review_race', NEW.id, 2, 'pending', 'manual', OLD.state, 'fake', NEW.updated_at, NEW.updated_at);
END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	_, err := st.MarkDone(issue.ID, "done after race")
	if err == nil {
		t.Fatal("MarkDone succeeded, want active run error")
	}
	if got := core.AsAPIError(err).Code; got != core.ErrIssueAlreadyRunning {
		t.Fatalf("MarkDone error code = %s, want %s", got, core.ErrIssueAlreadyRunning)
	}
	assertIssueState(t, st, issue.ID, core.StateHumanReview)
	assertRunStatus(t, st, run.ID, core.RunCompleted)
	assertRunAttemptCount(t, st, issue.ID, 1)
	rows, qerr := st.Project.Query(`SELECT id FROM run_events WHERE issue_id=? AND event_type IN ('review.marked_done','issue.completed')`, issue.ID)
	if qerr != nil {
		t.Fatalf("query review events: %v", qerr)
	}
	if len(rows) != 0 {
		t.Fatalf("review completion events = %d, want 0", len(rows))
	}
}

func TestReviewActionRejectsReviewPacketChangedDuringUpdate(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareCompletedReviewRun(t, st)
	reviewPacketID := latestReviewPacketID(t, st, run.ID)
	if err := st.Project.Exec(`CREATE TRIGGER race_review_packet_status AFTER UPDATE OF state ON issues
WHEN NEW.id='` + issue.ID + `' AND NEW.state='Rework'
BEGIN
	UPDATE review_packets SET status='failed' WHERE id='` + reviewPacketID + `';
END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	_, err := st.SendToRework(issue.ID, "rework after packet race")
	if err == nil {
		t.Fatal("SendToRework succeeded, want review packet error")
	}
	if got := core.AsAPIError(err).Code; got != core.ErrReviewPacketRequired {
		t.Fatalf("SendToRework error code = %s, want %s", got, core.ErrReviewPacketRequired)
	}
	assertIssueState(t, st, issue.ID, core.StateHumanReview)
	assertRunStatus(t, st, run.ID, core.RunCompleted)
	row := getReviewPacketRow(t, st, reviewPacketID)
	if row["status"].String() != "generated" {
		t.Fatalf("review packet status = %s, want generated", row["status"].String())
	}
}

func TestReviewActionRejectsCompletedRunChangedDuringUpdate(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareCompletedReviewRun(t, st)
	if err := st.Project.Exec(`CREATE TRIGGER race_review_run_status AFTER UPDATE OF state ON issues
WHEN NEW.id='` + issue.ID + `' AND NEW.state='Done'
BEGIN
	UPDATE run_attempts SET status='failed' WHERE id='` + run.ID + `';
END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	_, err := st.MarkDone(issue.ID, "done after run race")
	if err == nil {
		t.Fatal("MarkDone succeeded, want review packet error")
	}
	if got := core.AsAPIError(err).Code; got != core.ErrReviewPacketRequired {
		t.Fatalf("MarkDone error code = %s, want %s", got, core.ErrReviewPacketRequired)
	}
	assertIssueState(t, st, issue.ID, core.StateHumanReview)
	assertRunStatus(t, st, run.ID, core.RunCompleted)
}

func TestReviewActionPropagatesCompletedRunLoadError(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareCompletedReviewRun(t, st)
	if err := st.Project.Exec(`ALTER TABLE run_attempts RENAME TO run_attempts_shadow`); err != nil {
		t.Fatalf("rename run_attempts: %v", err)
	}
	if err := st.Project.Exec(`CREATE VIEW run_attempts AS
SELECT id, issue_id,
       CASE WHEN id='` + run.ID + `' THEN json_extract('bad json','$.x') ELSE attempt_no END AS attempt_no,
       workspace_id, workflow_snapshot_id, status, dispatch_reason, source_issue_state, runner_kind,
       base_ref_config, base_ref, base_sha, branch_name, failure_code, failure_message,
       started_at, ended_at, created_at, updated_at
FROM run_attempts_shadow`); err != nil {
		t.Fatalf("create run_attempts error view: %v", err)
	}

	_, err := st.MarkDone(issue.ID, "run load should fail")

	if dropErr := st.Project.Exec(`DROP VIEW run_attempts`); dropErr != nil {
		t.Fatalf("drop run_attempts view: %v", dropErr)
	}
	if restoreErr := st.Project.Exec(`ALTER TABLE run_attempts_shadow RENAME TO run_attempts`); restoreErr != nil {
		t.Fatalf("restore run_attempts: %v", restoreErr)
	}
	if err == nil {
		t.Fatal("MarkDone succeeded, want run load error")
	}
	assertErrorContains(t, err, "malformed JSON")
	if got := core.AsAPIError(err).Code; got == core.ErrReviewPacketRequired {
		t.Fatalf("MarkDone error code = %s, want backend error", got)
	}
	assertIssueState(t, st, issue.ID, core.StateHumanReview)
	assertRunStatus(t, st, run.ID, core.RunCompleted)
}

func TestUpdateRunStatusRejectsCompletedRunWithoutChangingRunOrIssue(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareCompletedReviewRun(t, st)

	err := st.UpdateRunStatus(run.ID, core.RunRunning, map[string]any{"started_at": core.Now()})
	if err == nil {
		t.Fatal("UpdateRunStatus succeeded, want invalid_state_transition")
	}
	if got := core.AsAPIError(err).Code; got != core.ErrInvalidStateTransition {
		t.Fatalf("UpdateRunStatus error code = %s, want %s", got, core.ErrInvalidStateTransition)
	}
	assertRunStatus(t, st, run.ID, core.RunCompleted)
	assertIssueState(t, st, issue.ID, core.StateHumanReview)
}

func TestUpdateRunStatusRejectsTerminalTargetWithoutChangingRunOrIssue(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareActiveRun(t, st, "UpdateRunStatus terminal target")

	err := st.UpdateRunStatus(run.ID, core.RunCompleted, map[string]any{"ended_at": core.Now()})
	if err == nil {
		t.Fatal("UpdateRunStatus succeeded, want invalid_state_transition")
	}
	if got := core.AsAPIError(err).Code; got != core.ErrInvalidStateTransition {
		t.Fatalf("UpdateRunStatus error code = %s, want %s", got, core.ErrInvalidStateTransition)
	}
	assertRunStatus(t, st, run.ID, core.RunPending)
	assertIssueState(t, st, issue.ID, core.StateWorking)
}

func TestUpdateRunStatusRejectsProtectedFieldWithoutChangingRunOrIssue(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareActiveRun(t, st, "UpdateRunStatus protected field")

	err := st.UpdateRunStatus(run.ID, core.RunRunning, map[string]any{"status": string(core.RunCompleted)})
	if err == nil {
		t.Fatal("UpdateRunStatus succeeded, want invalid_request")
	}
	if got := core.AsAPIError(err).Code; got != core.ErrInvalidRequest {
		t.Fatalf("UpdateRunStatus error code = %s, want %s", got, core.ErrInvalidRequest)
	}
	assertRunStatus(t, st, run.ID, core.RunPending)
	assertIssueState(t, st, issue.ID, core.StateWorking)
}

func TestTransitionIssueStillCancelsActiveRun(t *testing.T) {
	st := newStoreTestStore(t)
	issue, err := st.CreateIssue(CreateIssueInput{
		Title:              "Active run cancellation",
		Description:        "desc",
		AcceptanceCriteria: []string{"done"},
		Priority:           3,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if _, err := st.TransitionIssue(issue.ID, core.StateReady, "", ""); err != nil {
		t.Fatalf("TransitionIssue ready: %v", err)
	}
	run, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}

	gotIssue, err := st.TransitionIssue(issue.ID, core.StateBlocked, "blocked by operator", "")
	if err != nil {
		t.Fatalf("TransitionIssue blocked: %v", err)
	}
	if gotIssue.State != core.StateBlocked {
		t.Fatalf("issue state = %s, want %s", gotIssue.State, core.StateBlocked)
	}
	gotRun, err := st.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if gotRun.Status != core.RunCancelled {
		t.Fatalf("run status = %s, want %s", gotRun.Status, core.RunCancelled)
	}
	if gotRun.FailureCode == nil || *gotRun.FailureCode != core.FailureIssueStateChanged {
		t.Fatalf("failure code = %v, want %s", gotRun.FailureCode, core.FailureIssueStateChanged)
	}
}

func TestTransitionIssueRejectsUnknownStateWithoutChangingIssue(t *testing.T) {
	st := newStoreTestStore(t)
	issue, err := st.CreateIssue(CreateIssueInput{
		Title:       "Unknown transition state",
		Description: "desc",
		Priority:    3,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	_, err = st.TransitionIssue(issue.ID, core.IssueState("Bogus"), "bad state", "")
	if err == nil {
		t.Fatal("TransitionIssue succeeded, want invalid_request")
	}
	if got := core.AsAPIError(err).Code; got != core.ErrInvalidRequest {
		t.Fatalf("TransitionIssue error code = %s, want %s", got, core.ErrInvalidRequest)
	}
	assertIssueState(t, st, issue.ID, core.StateInbox)
}

func TestTransitionIssueRejectsStateChangedDuringUpdate(t *testing.T) {
	st := newStoreTestStore(t)
	issue := prepareReadyIssue(t, st, "Transition state race")
	if err := st.Project.Exec(`CREATE TRIGGER race_transition_state BEFORE UPDATE OF state ON issues
WHEN OLD.id='` + issue.ID + `' AND NEW.state='Blocked'
BEGIN
	UPDATE issues SET state='Done', completed_at=NEW.updated_at WHERE id=OLD.id;
	SELECT RAISE(IGNORE);
END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	_, err := st.TransitionIssue(issue.ID, core.StateBlocked, "blocked after race", "")

	if err == nil {
		t.Fatal("TransitionIssue succeeded, want invalid_state_transition")
	}
	if got := core.AsAPIError(err).Code; got != core.ErrInvalidStateTransition {
		t.Fatalf("TransitionIssue error code = %s, want %s", got, core.ErrInvalidStateTransition)
	}
	assertIssueState(t, st, issue.ID, core.StateReady)
	rows, qerr := st.Project.Query(`SELECT id FROM issue_state_history WHERE issue_id=? AND to_state=?`, issue.ID, string(core.StateBlocked))
	if qerr != nil {
		t.Fatalf("query state history: %v", qerr)
	}
	if len(rows) != 0 {
		t.Fatalf("blocked history rows = %d, want 0", len(rows))
	}
}

func TestTransitionIssueToReadyCancelsActiveRunAndRevokesToken(t *testing.T) {
	st := newStoreTestStore(t)
	issue, err := st.CreateIssue(CreateIssueInput{
		Title:              "Ready cancels active run",
		Description:        "desc",
		AcceptanceCriteria: []string{"done"},
		Priority:           3,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if _, err := st.TransitionIssue(issue.ID, core.StateReady, "", ""); err != nil {
		t.Fatalf("TransitionIssue ready: %v", err)
	}
	run, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	if err := st.UpdateRunStatus(run.ID, core.RunStartingAgent, nil); err != nil {
		t.Fatalf("UpdateRunStatus: %v", err)
	}
	if _, err := st.CreateToolToken(run.ID, "token-hash", map[string]any{"workspace": "/tmp/workspace"}, core.Now()); err != nil {
		t.Fatalf("CreateToolToken: %v", err)
	}

	gotIssue, err := st.TransitionIssue(issue.ID, core.StateReady, "back to ready", "")
	if err != nil {
		t.Fatalf("TransitionIssue ready: %v", err)
	}

	if gotIssue.State != core.StateReady {
		t.Fatalf("issue state = %s, want %s", gotIssue.State, core.StateReady)
	}
	gotRun, err := st.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if gotRun.Status != core.RunCancelled {
		t.Fatalf("run status = %s, want %s", gotRun.Status, core.RunCancelled)
	}
	row, err := st.Project.QueryOne(`SELECT revoked_at FROM run_tool_tokens WHERE run_id=?`, run.ID)
	if err != nil {
		t.Fatalf("get tool token: %v", err)
	}
	if row["revoked_at"].String() == "" {
		t.Fatal("tool token revoked_at is empty, want revoked timestamp")
	}
}

func TestCancelRunRollsBackWhenToolTokenRevokeFails(t *testing.T) {
	st := newStoreTestStore(t)
	issue, err := st.CreateIssue(CreateIssueInput{
		Title:              "Cancel token revoke failure",
		Description:        "desc",
		AcceptanceCriteria: []string{"done"},
		Priority:           3,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if _, err := st.TransitionIssue(issue.ID, core.StateReady, "", ""); err != nil {
		t.Fatalf("TransitionIssue ready: %v", err)
	}
	run, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	if err := st.UpdateRunStatus(run.ID, core.RunRunning, nil); err != nil {
		t.Fatalf("UpdateRunStatus: %v", err)
	}
	expiresAt := time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)
	if _, err := st.CreateToolToken(run.ID, "token-hash-revoke-fails", map[string]any{"workspace": "/tmp/workspace"}, expiresAt); err != nil {
		t.Fatalf("CreateToolToken: %v", err)
	}
	if err := st.Project.Exec(`CREATE TRIGGER fail_token_revoke BEFORE UPDATE OF revoked_at ON run_tool_tokens WHEN NEW.revoked_at IS NOT NULL BEGIN SELECT RAISE(ABORT, 'revoke failed'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	err = st.CancelRun(run.ID, "operator cancelled")
	if err == nil {
		t.Fatal("CancelRun succeeded, want revoke error")
	}
	assertRunStatus(t, st, run.ID, core.RunRunning)
	assertIssueState(t, st, issue.ID, core.StateWorking)
	row, err := st.Project.QueryOne(`SELECT revoked_at FROM run_tool_tokens WHERE run_id=?`, run.ID)
	if err != nil {
		t.Fatalf("get tool token: %v", err)
	}
	if row["revoked_at"].String() != "" {
		t.Fatalf("tool token revoked_at = %q, want empty after rollback", row["revoked_at"].String())
	}
	gotRunID, gotIssueID, err := st.ValidateToolToken("token-hash-revoke-fails")
	if err != nil {
		t.Fatalf("ValidateToolToken: %v", err)
	}
	if gotRunID != run.ID || gotIssueID != issue.ID {
		t.Fatalf("ValidateToolToken = (%s, %s), want (%s, %s)", gotRunID, gotIssueID, run.ID, issue.ID)
	}
}

func TestCancelRunRollsBackWhenCancelledEventAppendFails(t *testing.T) {
	st := newStoreTestStore(t)
	issue, err := st.CreateIssue(CreateIssueInput{
		Title:              "Cancel event failure",
		Description:        "desc",
		AcceptanceCriteria: []string{"done"},
		Priority:           3,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if _, err := st.TransitionIssue(issue.ID, core.StateReady, "", ""); err != nil {
		t.Fatalf("TransitionIssue ready: %v", err)
	}
	run, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	if err := st.UpdateRunStatus(run.ID, core.RunRunning, nil); err != nil {
		t.Fatalf("UpdateRunStatus: %v", err)
	}
	expiresAt := time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)
	if _, err := st.CreateToolToken(run.ID, "token-hash-cancel-event-fails", map[string]any{"workspace": "/tmp/workspace"}, expiresAt); err != nil {
		t.Fatalf("CreateToolToken: %v", err)
	}
	if err := st.Project.Exec(`CREATE TRIGGER fail_run_cancelled_event BEFORE INSERT ON run_events WHEN NEW.event_type='run.cancelled' BEGIN SELECT RAISE(ABORT, 'run cancelled event failed'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	err = st.CancelRun(run.ID, "operator cancelled")
	if err == nil {
		t.Fatal("CancelRun succeeded, want event append error")
	}
	assertRunStatus(t, st, run.ID, core.RunRunning)
	assertIssueState(t, st, issue.ID, core.StateWorking)
	row, err := st.Project.QueryOne(`SELECT revoked_at FROM run_tool_tokens WHERE run_id=?`, run.ID)
	if err != nil {
		t.Fatalf("get tool token: %v", err)
	}
	if row["revoked_at"].String() != "" {
		t.Fatalf("tool token revoked_at = %q, want empty after rollback", row["revoked_at"].String())
	}
	gotRunID, gotIssueID, err := st.ValidateToolToken("token-hash-cancel-event-fails")
	if err != nil {
		t.Fatalf("ValidateToolToken: %v", err)
	}
	if gotRunID != run.ID || gotIssueID != issue.ID {
		t.Fatalf("ValidateToolToken = (%s, %s), want (%s, %s)", gotRunID, gotIssueID, run.ID, issue.ID)
	}
}

func TestValidateToolTokenRejectsMalformedExpiry(t *testing.T) {
	st := newStoreTestStore(t)
	_, run := prepareActiveRun(t, st, "Malformed tool token expiry")
	if err := st.UpdateRunStatus(run.ID, core.RunRunning, nil); err != nil {
		t.Fatalf("UpdateRunStatus: %v", err)
	}
	if _, err := st.CreateToolToken(run.ID, "token-hash-malformed-expiry", map[string]any{"workspace": "/tmp/workspace"}, "not-rfc3339"); err != nil {
		t.Fatalf("CreateToolToken: %v", err)
	}

	_, _, err := st.ValidateToolToken("token-hash-malformed-expiry")
	if err == nil {
		t.Fatal("ValidateToolToken succeeded, want tool_token_invalid")
	}
	if got := core.AsAPIError(err).Code; got != core.ErrToolTokenInvalid {
		t.Fatalf("ValidateToolToken error code = %s, want %s", got, core.ErrToolTokenInvalid)
	}
}

func TestCreateToolTokenRejectsCancelledRun(t *testing.T) {
	st := newStoreTestStore(t)
	_, run := prepareCancelledRun(t, st)

	_, err := st.CreateToolToken(run.ID, "token-hash", map[string]any{"workspace": "/tmp/workspace"}, core.Now())
	if err == nil {
		t.Fatal("CreateToolToken succeeded, want invalid_state_transition")
	}
	if got := core.AsAPIError(err).Code; got != core.ErrInvalidStateTransition {
		t.Fatalf("CreateToolToken error code = %s, want %s", got, core.ErrInvalidStateTransition)
	}
}

func TestTransitionIssueRollsBackWhenActiveRunCancelFails(t *testing.T) {
	st := newStoreTestStore(t)
	issue, err := st.CreateIssue(CreateIssueInput{
		Title:              "Cancel failure rollback",
		Description:        "desc",
		AcceptanceCriteria: []string{"done"},
		Priority:           3,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if _, err := st.TransitionIssue(issue.ID, core.StateReady, "", ""); err != nil {
		t.Fatalf("TransitionIssue ready: %v", err)
	}
	run, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	if err := st.Project.Exec(`CREATE TRIGGER fail_cancel_run BEFORE UPDATE OF status ON run_attempts WHEN NEW.status='cancelled' BEGIN SELECT RAISE(ABORT, 'cancel failed'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	_, err = st.TransitionIssue(issue.ID, core.StateBlocked, "blocked by operator", "")
	if err == nil {
		t.Fatal("TransitionIssue succeeded, want cancel error")
	}
	assertRunStatus(t, st, run.ID, core.RunPending)
	assertIssueState(t, st, issue.ID, core.StateWorking)
}

func TestTransitionIssuePropagatesTerminalReopenResetError(t *testing.T) {
	st := newStoreTestStore(t)
	issue, _ := prepareCompletedReviewRun(t, st)
	if _, err := st.MarkDone(issue.ID, "done"); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}
	if err := st.Project.Exec(`CREATE TRIGGER fail_terminal_reopen_reset BEFORE UPDATE OF completed_at ON issues WHEN OLD.state='Done' AND NEW.completed_at IS NULL BEGIN SELECT RAISE(ABORT, 'terminal reset failed'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	_, err := st.TransitionIssue(issue.ID, core.StateReady, "", "")
	if err == nil {
		t.Fatal("TransitionIssue succeeded, want terminal reset error")
	}
	assertErrorContains(t, err, "terminal reset failed")
	assertIssueState(t, st, issue.ID, core.StateDone)
}

func TestTransitionIssueRollsBackWhenReasonCommentInsertFails(t *testing.T) {
	st := newStoreTestStore(t)
	issue := prepareReadyIssue(t, st, "TransitionIssue reason comment failure")
	if err := st.Project.Exec(`CREATE TRIGGER fail_transition_reason_comment BEFORE INSERT ON issue_comments WHEN NEW.author_type='operator' AND NEW.body='blocked by operator' BEGIN SELECT RAISE(ABORT, 'reason comment failed'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	_, err := st.TransitionIssue(issue.ID, core.StateBlocked, "blocked by operator", "")
	if err == nil {
		t.Fatal("TransitionIssue succeeded, want reason comment error")
	}
	assertErrorContains(t, err, "reason comment failed")
	assertIssueState(t, st, issue.ID, core.StateReady)
	rows, qerr := st.Project.Query(`SELECT id FROM issue_state_history WHERE issue_id=? AND to_state=?`, issue.ID, string(core.StateBlocked))
	if qerr != nil {
		t.Fatalf("query state history: %v", qerr)
	}
	if len(rows) != 0 {
		t.Fatalf("blocked state history rows = %d, want 0 after rollback", len(rows))
	}
}

func TestFailRunPropagatesIssueStateSelectError(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareActiveRun(t, st, "FailRun select error")
	replaceIssuesWithStateSelectErrorView(t, st)

	err := st.FailRun(run.ID, core.FailureToolGatewayFailed, "gateway failed", core.RunFailed)
	if err == nil {
		t.Fatal("FailRun succeeded, want issue state select error")
	}
	assertErrorContains(t, err, "missing_state_source")
	restoreIssuesTable(t, st)
	assertRunStatus(t, st, run.ID, core.RunPending)
	assertIssueState(t, st, issue.ID, core.StateWorking)
}

func TestCancelRunPropagatesIssueStateSelectError(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareActiveRun(t, st, "CancelRun select error")
	replaceIssuesWithStateSelectErrorView(t, st)

	err := st.CancelRun(run.ID, "operator cancelled")
	if err == nil {
		t.Fatal("CancelRun succeeded, want issue state select error")
	}
	assertErrorContains(t, err, "missing_state_source")
	restoreIssuesTable(t, st)
	assertRunStatus(t, st, run.ID, core.RunPending)
	assertIssueState(t, st, issue.ID, core.StateWorking)
}

func TestReconcileStaleActiveRunsReturnsFailRunError(t *testing.T) {
	st := newStoreTestStore(t)
	issue, err := st.CreateIssue(CreateIssueInput{
		Title:              "Reconcile failure",
		Description:        "desc",
		AcceptanceCriteria: []string{"done"},
		Priority:           3,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if _, err := st.TransitionIssue(issue.ID, core.StateReady, "", ""); err != nil {
		t.Fatalf("TransitionIssue ready: %v", err)
	}
	run, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	if err := st.Project.Exec(`CREATE TRIGGER fail_stale_run_reconcile BEFORE UPDATE OF status ON run_attempts WHEN NEW.status='failed' BEGIN SELECT RAISE(ABORT, 'stale reconcile failed'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	err = st.ReconcileStaleActiveRuns()
	if err == nil {
		t.Fatal("ReconcileStaleActiveRuns succeeded, want FailRun error")
	}
	assertRunStatus(t, st, run.ID, core.RunPending)
	assertIssueState(t, st, issue.ID, core.StateWorking)
}

func TestReconcileStaleActiveRunsContinuesAfterFailRunError(t *testing.T) {
	st := newStoreTestStore(t)
	issue1, run1 := prepareActiveRunWithMaxConcurrent(t, st, "Reconcile failed run", 2)
	issue2, run2 := prepareActiveRunWithMaxConcurrent(t, st, "Reconcile successful run", 2)
	if err := st.Project.Exec(`CREATE TRIGGER fail_one_stale_run_reconcile BEFORE UPDATE OF status ON run_attempts WHEN OLD.id='` + run1.ID + `' AND NEW.status='failed' BEGIN SELECT RAISE(ABORT, 'stale reconcile failed'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	err := st.ReconcileStaleActiveRuns()
	if err == nil {
		t.Fatal("ReconcileStaleActiveRuns succeeded, want FailRun error")
	}
	assertRunStatus(t, st, run1.ID, core.RunPending)
	assertIssueState(t, st, issue1.ID, core.StateWorking)
	assertRunStatus(t, st, run2.ID, core.RunFailed)
	assertIssueState(t, st, issue2.ID, core.StateReady)
}

func TestReconcileStaleActiveRunsCancelsPendingApproval(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareActiveRun(t, st, "Reconcile approval")
	approval, err := st.CreatePendingApprovalRequest(CreateApprovalRequestInput{
		RunID:         run.ID,
		IssueID:       issue.ID,
		Kind:          "command",
		ActionSummary: "Run command after restart",
	})
	if err != nil {
		t.Fatalf("CreatePendingApprovalRequest: %v", err)
	}

	if err := st.ReconcileStaleActiveRuns(); err != nil {
		t.Fatalf("ReconcileStaleActiveRuns: %v", err)
	}

	assertRunStatus(t, st, run.ID, core.RunFailed)
	gotApproval := getApprovalRow(t, st, approval.ID)
	if gotApproval["status"].String() != "cancelled" {
		t.Fatalf("approval status = %s, want cancelled", gotApproval["status"].String())
	}
	if gotApproval["resolved_at"].String() == "" {
		t.Fatal("approval resolved_at is empty after reconcile")
	}
	err = st.DecideApproval(approval.ID, "approved_once", "operator approved stale request")
	if err == nil {
		t.Fatal("DecideApproval succeeded, want approval_not_pending")
	}
	if got := core.AsAPIError(err).Code; got != core.ErrApprovalNotPending {
		t.Fatalf("DecideApproval error code = %s, want %s", got, core.ErrApprovalNotPending)
	}
}

func TestReconcileStaleActiveRunsRollsBackWhenApprovalCancelFails(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareActiveRun(t, st, "Reconcile approval rollback")
	approval, err := st.CreatePendingApprovalRequest(CreateApprovalRequestInput{
		RunID:         run.ID,
		IssueID:       issue.ID,
		Kind:          "command",
		ActionSummary: "Run command after restart",
	})
	if err != nil {
		t.Fatalf("CreatePendingApprovalRequest: %v", err)
	}
	if err := st.Project.Exec(`CREATE TRIGGER fail_reconcile_approval_cancel BEFORE UPDATE OF status ON approval_requests WHEN OLD.id='` + approval.ID + `' AND NEW.status='cancelled' BEGIN SELECT RAISE(ABORT, 'approval cancel failed'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	err = st.ReconcileStaleActiveRuns()
	if err == nil {
		t.Fatal("ReconcileStaleActiveRuns succeeded, want approval cancel error")
	}
	assertErrorContains(t, err, "approval cancel failed")
	assertRunStatus(t, st, run.ID, core.RunPending)
	assertIssueState(t, st, issue.ID, core.StateWorking)
	gotApproval := getApprovalRow(t, st, approval.ID)
	if gotApproval["status"].String() != "pending" {
		t.Fatalf("approval status = %s, want pending after rollback", gotApproval["status"].String())
	}
}

func TestDecideApprovalCancelRejectsCompletedRunWithoutChangingApproval(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareCompletedReviewRun(t, st)
	approvalID := insertPendingApproval(t, st, run.ID, issue.ID)

	err := st.DecideApproval(approvalID, "cancelled", "operator cancelled stale approval")
	if err == nil {
		t.Fatal("DecideApproval succeeded, want invalid_state_transition")
	}
	if got := core.AsAPIError(err).Code; got != core.ErrInvalidStateTransition {
		t.Fatalf("DecideApproval error code = %s, want %s", got, core.ErrInvalidStateTransition)
	}

	gotApproval := getApprovalRow(t, st, approvalID)
	if gotApproval["status"].String() != "pending" {
		t.Fatalf("approval status = %s, want pending", gotApproval["status"].String())
	}
	if gotApproval["reason"].String() != "" {
		t.Fatalf("approval reason = %q, want empty", gotApproval["reason"].String())
	}
	if gotApproval["resolved_at"].String() != "" {
		t.Fatalf("approval resolved_at = %q, want empty", gotApproval["resolved_at"].String())
	}

	gotRun, err := st.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if gotRun.Status != core.RunCompleted {
		t.Fatalf("run status = %s, want %s", gotRun.Status, core.RunCompleted)
	}
}

func TestDecideApprovalRejectsInactiveRunForNonCancelDecision(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareCompletedReviewRun(t, st)
	approvalID := insertPendingApproval(t, st, run.ID, issue.ID)

	err := st.DecideApproval(approvalID, "approved_once", "operator approved stale approval")
	if err == nil {
		t.Fatal("DecideApproval succeeded, want invalid_state_transition")
	}
	if got := core.AsAPIError(err).Code; got != core.ErrInvalidStateTransition {
		t.Fatalf("DecideApproval error code = %s, want %s", got, core.ErrInvalidStateTransition)
	}
	gotApproval := getApprovalRow(t, st, approvalID)
	if gotApproval["status"].String() != "pending" {
		t.Fatalf("approval status = %s, want pending", gotApproval["status"].String())
	}
}

func TestDecideApprovalCancelCancelsActiveRunAndApproval(t *testing.T) {
	st := newStoreTestStore(t)
	issue, err := st.CreateIssue(CreateIssueInput{
		Title:              "Approval cancel active run",
		Description:        "desc",
		AcceptanceCriteria: []string{"done"},
		Priority:           3,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if _, err := st.TransitionIssue(issue.ID, core.StateReady, "", ""); err != nil {
		t.Fatalf("TransitionIssue ready: %v", err)
	}
	run, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	approvalID := insertPendingApproval(t, st, run.ID, issue.ID)

	if err := st.DecideApproval(approvalID, "cancel_run", "operator cancelled active approval"); err != nil {
		t.Fatalf("DecideApproval: %v", err)
	}

	gotApproval := getApprovalRow(t, st, approvalID)
	if gotApproval["status"].String() != "cancelled" {
		t.Fatalf("approval status = %s, want cancelled", gotApproval["status"].String())
	}
	gotRun, err := st.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if gotRun.Status != core.RunCancelled {
		t.Fatalf("run status = %s, want %s", gotRun.Status, core.RunCancelled)
	}
}

func TestPendingApprovalsProjectsStructuredFieldsWithoutOpaqueJSON(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareActiveRun(t, st, "Approval projection")
	pendingID := core.NewID("apr_")
	resolvedID := core.NewID("apr_")
	timeoutID := core.NewID("apr_")
	if err := st.Project.Exec(`INSERT INTO approval_requests(id,run_id,issue_id,kind,status,request_json,timeout_ms,expires_at,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		pendingID, run.ID, issue.ID, "command", "pending", `{"action_summary":"Run go test","risk_level":"medium","policy_match":"command.review","command":"secret command","path":"/tmp/secret"}`, 30000, "2026-05-26T10:01:00Z", "2026-05-26T10:00:00Z"); err != nil {
		t.Fatalf("insert pending approval: %v", err)
	}
	if err := st.Project.Exec(`INSERT INTO approval_requests(id,run_id,issue_id,kind,status,request_json,created_at,resolved_at,reason,decision_json) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		resolvedID, run.ID, issue.ID, "network", "denied", `{"url":"https://example.invalid/private"}`, "2026-05-26T09:00:00Z", "2026-05-26T09:02:00Z", "too broad", `{"decision":"deny"}`); err != nil {
		t.Fatalf("insert resolved approval: %v", err)
	}
	if err := st.Project.Exec(`INSERT INTO approval_requests(id,run_id,issue_id,kind,status,request_json,timeout_ms,expires_at,created_at,resolved_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		timeoutID, run.ID, issue.ID, "file_change", "timeout", `{"raw_request":"do not expose"}`, 1000, "2026-05-26T08:00:01Z", "2026-05-26T08:00:00Z", "2026-05-26T08:00:01Z"); err != nil {
		t.Fatalf("insert timeout approval: %v", err)
	}

	approvals, err := st.PendingApprovals()
	if err != nil {
		t.Fatalf("PendingApprovals: %v", err)
	}
	if len(approvals) != 3 {
		t.Fatalf("approvals len = %d, want 3", len(approvals))
	}
	gotPending := approvals[0]
	if gotPending.ID != pendingID || gotPending.RunID != run.ID || gotPending.IssueID != issue.ID || gotPending.Kind != "command" || gotPending.Status != "pending" {
		t.Fatalf("pending approval identity/status = %#v", gotPending)
	}
	if gotPending.ActionSummary != "Run go test" || gotPending.RiskLevel != "medium" || gotPending.PolicyMatch != "command.review" {
		t.Fatalf("pending structured fields = %#v", gotPending)
	}
	if gotPending.RequestedAt != "2026-05-26T10:00:00Z" || gotPending.CreatedAt != "2026-05-26T10:00:00Z" {
		t.Fatalf("pending timestamps = requested %q created %q", gotPending.RequestedAt, gotPending.CreatedAt)
	}
	if gotPending.TimeoutMS == nil || *gotPending.TimeoutMS != 30000 {
		t.Fatalf("pending timeout_ms = %#v, want 30000", gotPending.TimeoutMS)
	}
	if gotPending.ExpiresAt == nil || *gotPending.ExpiresAt != "2026-05-26T10:01:00Z" {
		t.Fatalf("pending expires_at = %#v", gotPending.ExpiresAt)
	}
	if gotPending.ResolvedAt != nil || gotPending.Reason != nil {
		t.Fatalf("pending nullable fields = resolved_at %#v reason %#v, want nil", gotPending.ResolvedAt, gotPending.Reason)
	}
	gotResolved := approvals[1]
	if gotResolved.ID != resolvedID {
		t.Fatalf("resolved approval id = %s, want %s", gotResolved.ID, resolvedID)
	}
	if gotResolved.ActionSummary != "network approval "+resolvedID || gotResolved.RiskLevel != "unknown" || gotResolved.PolicyMatch != "unclassified" {
		t.Fatalf("resolved defaults = %#v", gotResolved)
	}
	if gotResolved.TimeoutMS != nil || gotResolved.ExpiresAt != nil {
		t.Fatalf("resolved timeout fields = %#v %#v, want nil", gotResolved.TimeoutMS, gotResolved.ExpiresAt)
	}
	if gotResolved.ResolvedAt == nil || *gotResolved.ResolvedAt != "2026-05-26T09:02:00Z" || gotResolved.Reason == nil || *gotResolved.Reason != "too broad" {
		t.Fatalf("resolved nullable fields = resolved_at %#v reason %#v", gotResolved.ResolvedAt, gotResolved.Reason)
	}
	gotTimeout := approvals[2]
	if gotTimeout.ID != timeoutID || gotTimeout.Status != "timeout" {
		t.Fatalf("timeout approval = %#v", gotTimeout)
	}
	if gotTimeout.ActionSummary != "file_change approval "+timeoutID || gotTimeout.RiskLevel != "unknown" || gotTimeout.PolicyMatch != "unclassified" {
		t.Fatalf("timeout defaults = %#v", gotTimeout)
	}
	b, err := json.Marshal(approvals)
	if err != nil {
		t.Fatalf("marshal approvals: %v", err)
	}
	for _, forbidden := range []string{"request_json", "decision_json", `"request"`, `"decision"`, "secret command", "/tmp/secret", "example.invalid", "raw_request"} {
		if strings.Contains(string(b), forbidden) {
			t.Fatalf("approval projection leaked %q in JSON %s", forbidden, string(b))
		}
	}
}

func TestCreatePendingApprovalRequestStoresStructuredFieldsAndTimeout(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareActiveRun(t, st, "Approval helper")

	approval, err := st.CreatePendingApprovalRequest(CreateApprovalRequestInput{
		RunID:         run.ID,
		IssueID:       issue.ID,
		Kind:          "command",
		ActionSummary: "Run go test ./internal/store",
		RiskLevel:     "medium",
		PolicyMatch:   "command.review",
		RequestID:     "codex_req_123",
		TimeoutMS:     5000,
	})
	if err != nil {
		t.Fatalf("CreatePendingApprovalRequest: %v", err)
	}

	if approval.ID == "" || approval.RunID != run.ID || approval.IssueID != issue.ID || approval.Kind != "command" || approval.Status != "pending" {
		t.Fatalf("approval identity/status = %#v", approval)
	}
	if approval.ActionSummary != "Run go test ./internal/store" || approval.RiskLevel != "medium" || approval.PolicyMatch != "command.review" {
		t.Fatalf("approval structured fields = %#v", approval)
	}
	if approval.TimeoutMS == nil || *approval.TimeoutMS != 5000 || approval.ExpiresAt == nil {
		t.Fatalf("approval timeout fields = timeout_ms %#v expires_at %#v", approval.TimeoutMS, approval.ExpiresAt)
	}

	row := getApprovalRow(t, st, approval.ID)
	var request map[string]any
	if err := json.Unmarshal([]byte(row["request_json"].String()), &request); err != nil {
		t.Fatalf("decode request_json: %v", err)
	}
	for _, key := range []string{"action_summary", "risk_level", "policy_match", "request_id"} {
		if _, ok := request[key]; !ok {
			t.Fatalf("request_json missing %s: %s", key, row["request_json"].String())
		}
	}

	if err := st.MarkApprovalTimeout(approval.ID, "approval timed out"); err != nil {
		t.Fatalf("MarkApprovalTimeout: %v", err)
	}
	got := getApprovalRow(t, st, approval.ID)
	if got["status"].String() != "timeout" {
		t.Fatalf("approval status = %s, want timeout", got["status"].String())
	}
	if got["resolved_at"].String() == "" {
		t.Fatal("resolved_at is empty after timeout")
	}
	err = st.DecideApproval(approval.ID, "approved_once", "too late")
	if err == nil {
		t.Fatal("DecideApproval after timeout succeeded, want approval_not_pending")
	}
	if got := core.AsAPIError(err).Code; got != core.ErrApprovalNotPending {
		t.Fatalf("DecideApproval error code = %s, want %s", got, core.ErrApprovalNotPending)
	}
}

func TestHasApprovedForRunApprovalRequiresPolicyOrActionMatch(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareActiveRun(t, st, "Approval run scope match")
	if err := st.Project.Exec(`INSERT INTO approval_requests(id,run_id,issue_id,kind,status,request_json,created_at) VALUES(?,?,?,?,?,?,?)`,
		core.NewID("apr_"), run.ID, issue.ID, "network", "approved_for_run", `{}`, core.Now()); err != nil {
		t.Fatalf("insert approved approval: %v", err)
	}

	ok, err := st.HasApprovedForRunApproval(CreateApprovalRequestInput{
		RunID:   run.ID,
		IssueID: issue.ID,
		Kind:    "network",
	})
	if err != nil {
		t.Fatalf("HasApprovedForRunApproval: %v", err)
	}
	if ok {
		t.Fatal("HasApprovedForRunApproval returned true without policy/action match keys")
	}

	if err := st.Project.Exec(`INSERT INTO approval_requests(id,run_id,issue_id,kind,status,request_json,created_at) VALUES(?,?,?,?,?,?,?)`,
		core.NewID("apr_"), run.ID, issue.ID, "network", "approved_for_run", `{"policy_match":"network.example"}`, core.Now()); err != nil {
		t.Fatalf("insert policy approval: %v", err)
	}
	ok, err = st.HasApprovedForRunApproval(CreateApprovalRequestInput{
		RunID:       run.ID,
		IssueID:     issue.ID,
		Kind:        "network",
		PolicyMatch: "network.example",
	})
	if err != nil {
		t.Fatalf("HasApprovedForRunApproval: %v", err)
	}
	if !ok {
		t.Fatal("HasApprovedForRunApproval returned false for matching policy")
	}
}

func TestApprovedForRunApprovalDoesNotMatchWhenStoredCWDIsUnknown(t *testing.T) {
	if approvalRequestMatchesInput(`{"action_summary":"go test","cwd":"/workspace/a"}`, CreateApprovalRequestInput{ActionSummary: "go test"}) {
		t.Fatal("approvalRequestMatchesInput returned true without matching cwd")
	}
}

func TestHasApprovedForRunApprovalRequiresCWDForCommandReuse(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareActiveRun(t, st, "Approval run scope command cwd")
	if err := st.Project.Exec(`INSERT INTO approval_requests(id,run_id,issue_id,kind,status,request_json,created_at) VALUES(?,?,?,?,?,?,?)`,
		core.NewID("apr_"), run.ID, issue.ID, "command", "approved_for_run", `{"action_summary":"go test"}`, core.Now()); err != nil {
		t.Fatalf("insert command approval: %v", err)
	}

	ok, err := st.HasApprovedForRunApproval(CreateApprovalRequestInput{
		RunID:         run.ID,
		IssueID:       issue.ID,
		Kind:          "command",
		ActionSummary: "go test",
	})
	if err != nil {
		t.Fatalf("HasApprovedForRunApproval: %v", err)
	}
	if ok {
		t.Fatal("HasApprovedForRunApproval returned true for command approval without cwd")
	}
}

func TestHasApprovedForRunApprovalRequiresCommandActionMatch(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareActiveRun(t, st, "Approval run scope command fingerprint")
	if err := st.Project.Exec(`INSERT INTO approval_requests(id,run_id,issue_id,kind,status,request_json,created_at) VALUES(?,?,?,?,?,?,?)`,
		core.NewID("apr_"), run.ID, issue.ID, "command", "approved_for_run", `{"cwd":"/workspace","policy_match":"command.review","action_summary":"go test"}`, core.Now()); err != nil {
		t.Fatalf("insert command approval: %v", err)
	}

	ok, err := st.HasApprovedForRunApproval(CreateApprovalRequestInput{
		RunID:         run.ID,
		IssueID:       issue.ID,
		Kind:          "command",
		CWD:           "/workspace",
		PolicyMatch:   "command.review",
		ActionSummary: "go test ./other",
	})
	if err != nil {
		t.Fatalf("HasApprovedForRunApproval: %v", err)
	}
	if ok {
		t.Fatal("HasApprovedForRunApproval returned true for different command with same cwd and policy")
	}
}

func TestCreatePendingApprovalRequestRejectsInactiveRun(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareCompletedReviewRun(t, st)

	_, err := st.CreatePendingApprovalRequest(CreateApprovalRequestInput{
		RunID:         run.ID,
		IssueID:       issue.ID,
		Kind:          "command",
		ActionSummary: "Run stale command",
	})
	if err == nil {
		t.Fatal("CreatePendingApprovalRequest succeeded, want invalid_state_transition")
	}
	if got := core.AsAPIError(err).Code; got != core.ErrInvalidStateTransition {
		t.Fatalf("CreatePendingApprovalRequest error code = %s, want %s", got, core.ErrInvalidStateTransition)
	}
}

func TestDecideApprovalReturnsNotPendingWhenFinalUpdateMissesPending(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareActiveRun(t, st, "Approval final update race")
	approvalID := insertPendingApproval(t, st, run.ID, issue.ID)
	if err := st.Project.Exec(`CREATE TRIGGER race_approval_decision BEFORE UPDATE OF status ON approval_requests
WHEN OLD.id='` + approvalID + `' AND NEW.status='approved_once'
BEGIN
	UPDATE approval_requests SET status='denied', reason='other operator', resolved_at=NEW.resolved_at WHERE id=OLD.id;
	SELECT RAISE(IGNORE);
END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	err := st.DecideApproval(approvalID, "approved_once", "operator approved stale request")
	if err == nil {
		t.Fatal("DecideApproval succeeded, want approval_not_pending")
	}
	if got := core.AsAPIError(err).Code; got != core.ErrApprovalNotPending {
		t.Fatalf("DecideApproval error code = %s, want %s", got, core.ErrApprovalNotPending)
	}
	gotApproval := getApprovalRow(t, st, approvalID)
	if gotApproval["status"].String() != "pending" {
		t.Fatalf("approval status = %s, want pending after rollback", gotApproval["status"].String())
	}
}

func TestDecideApprovalCancelRunReturnsNotPendingWhenApprovalChangesBeforeFinalUpdate(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareActiveRun(t, st, "Approval cancel_run final update race")
	approvalID := insertPendingApproval(t, st, run.ID, issue.ID)
	if err := st.Project.Exec(`CREATE TRIGGER race_cancel_run_approval BEFORE UPDATE OF status ON run_attempts
WHEN OLD.id='` + run.ID + `' AND NEW.status='cancelled'
BEGIN
	UPDATE approval_requests SET status='approved_once', reason='other operator', resolved_at=NEW.updated_at WHERE id='` + approvalID + `' AND status='pending';
END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	err := st.DecideApproval(approvalID, "cancel_run", "operator cancelled stale approval")
	if err == nil {
		t.Fatal("DecideApproval succeeded, want approval_not_pending")
	}
	if got := core.AsAPIError(err).Code; got != core.ErrApprovalNotPending {
		t.Fatalf("DecideApproval error code = %s, want %s", got, core.ErrApprovalNotPending)
	}
	gotApproval := getApprovalRow(t, st, approvalID)
	if gotApproval["status"].String() != "pending" {
		t.Fatalf("approval status = %s, want pending after rollback", gotApproval["status"].String())
	}
	assertRunStatus(t, st, run.ID, core.RunPending)
	assertIssueState(t, st, issue.ID, core.StateWorking)
}

func TestUpdateIssuePropagatesDescriptionWriteError(t *testing.T) {
	st := newStoreTestStore(t)
	issue, err := st.CreateIssue(CreateIssueInput{
		Title:       "Update error propagation",
		Description: "original desc",
		Priority:    3,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := st.Project.Exec(`CREATE TRIGGER fail_issue_description_update BEFORE UPDATE OF description ON issues BEGIN SELECT RAISE(ABORT, 'blocked description update'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	_, err = st.UpdateIssue(issue.ID, map[string]any{"description": "new desc"})
	if err == nil {
		t.Fatal("UpdateIssue succeeded, want description write error")
	}
	got, err := st.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.Description != "original desc" {
		t.Fatalf("description = %q, want original desc", got.Description)
	}
}

func TestUpdateIssueRejectsInvalidPriorityWithoutPartialUpdate(t *testing.T) {
	st := newStoreTestStore(t)
	issue, err := st.CreateIssue(CreateIssueInput{
		Title:       "Atomic update",
		Description: "original desc",
		Priority:    3,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	_, err = st.UpdateIssue(issue.ID, map[string]any{"title": "changed title", "priority": 9})
	if err == nil {
		t.Fatal("UpdateIssue succeeded, want invalid priority")
	}
	if got := core.AsAPIError(err).Code; got != core.ErrInvalidRequest {
		t.Fatalf("UpdateIssue error = %s, want %s", got, core.ErrInvalidRequest)
	}
	got, err := st.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.Title != "Atomic update" {
		t.Fatalf("title = %q, want unchanged", got.Title)
	}
	if got.Priority != 3 {
		t.Fatalf("priority = %d, want unchanged 3", got.Priority)
	}
}

func TestUpdateIssuePropagatesLabelWriteError(t *testing.T) {
	st := newStoreTestStore(t)
	issue, err := st.CreateIssue(CreateIssueInput{
		Title:       "Label error propagation",
		Description: "desc",
		Priority:    3,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := st.Project.Exec(`DROP TABLE issue_labels`); err != nil {
		t.Fatalf("drop issue_labels: %v", err)
	}

	_, err = st.UpdateIssue(issue.ID, map[string]any{"labels": []string{"blocked"}})
	if err == nil {
		t.Fatal("UpdateIssue succeeded, want label write error")
	}
}

func TestRemoveBlockerPropagatesRelationUpdateError(t *testing.T) {
	st := newStoreTestStore(t)
	blocked, err := st.CreateIssue(CreateIssueInput{
		Title:       "Blocked issue",
		Description: "desc",
		Priority:    3,
	})
	if err != nil {
		t.Fatalf("CreateIssue blocked: %v", err)
	}
	blocker, err := st.CreateIssue(CreateIssueInput{
		Title:       "Blocker issue",
		Description: "desc",
		Priority:    3,
	})
	if err != nil {
		t.Fatalf("CreateIssue blocker: %v", err)
	}
	if _, err := st.AddBlocker(blocked.ID, blocker.ID); err != nil {
		t.Fatalf("AddBlocker: %v", err)
	}
	if err := st.Project.Exec(`CREATE TRIGGER fail_remove_blocker BEFORE UPDATE OF active ON issue_relations WHEN OLD.relation_type='blocks' AND NEW.active=0 BEGIN SELECT RAISE(ABORT, 'remove blocker failed'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	_, err = st.RemoveBlocker(blocked.ID, blocker.ID)
	if err == nil {
		t.Fatal("RemoveBlocker succeeded, want relation update error")
	}
	rows, err := st.Project.Query(`SELECT id FROM issue_relations WHERE source_issue_id=? AND target_issue_id=? AND relation_type='blocks' AND active=1`, blocked.ID, blocker.ID)
	if err != nil {
		t.Fatalf("query active blocker relation: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("active blocker relations = %d, want 1", len(rows))
	}
}

func TestRemoveDuplicatePropagatesRelationUpdateError(t *testing.T) {
	st := newStoreTestStore(t)
	duplicate, err := st.CreateIssue(CreateIssueInput{
		Title:       "Duplicate issue",
		Description: "desc",
		Priority:    3,
	})
	if err != nil {
		t.Fatalf("CreateIssue duplicate: %v", err)
	}
	canonical, err := st.CreateIssue(CreateIssueInput{
		Title:       "Canonical issue",
		Description: "desc",
		Priority:    3,
	})
	if err != nil {
		t.Fatalf("CreateIssue canonical: %v", err)
	}
	if _, err := st.TransitionIssue(duplicate.ID, core.StateDuplicate, "same work", canonical.ID); err != nil {
		t.Fatalf("TransitionIssue duplicate: %v", err)
	}
	if err := st.Project.Exec(`CREATE TRIGGER fail_remove_duplicate BEFORE UPDATE OF active ON issue_relations WHEN OLD.relation_type='duplicates' AND NEW.active=0 BEGIN SELECT RAISE(ABORT, 'remove duplicate failed'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	_, err = st.RemoveDuplicate(duplicate.ID, canonical.ID)
	if err == nil {
		t.Fatal("RemoveDuplicate succeeded, want relation update error")
	}
	rows, err := st.Project.Query(`SELECT id FROM issue_relations WHERE source_issue_id=? AND target_issue_id=? AND relation_type='duplicates' AND active=1`, duplicate.ID, canonical.ID)
	if err != nil {
		t.Fatalf("query active duplicate relation: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("active duplicate relations = %d, want 1", len(rows))
	}
}

func TestTransitionIssuePropagatesDuplicateRelationLookupError(t *testing.T) {
	st := newStoreTestStore(t)
	duplicate, err := st.CreateIssue(CreateIssueInput{
		Title:       "Duplicate issue",
		Description: "desc",
		Priority:    3,
	})
	if err != nil {
		t.Fatalf("CreateIssue duplicate: %v", err)
	}
	canonical, err := st.CreateIssue(CreateIssueInput{
		Title:       "Canonical issue",
		Description: "desc",
		Priority:    3,
	})
	if err != nil {
		t.Fatalf("CreateIssue canonical: %v", err)
	}
	other, err := st.CreateIssue(CreateIssueInput{
		Title:       "Other canonical issue",
		Description: "desc",
		Priority:    3,
	})
	if err != nil {
		t.Fatalf("CreateIssue other: %v", err)
	}
	if err := st.Project.Exec(`INSERT INTO issue_relations(id,source_issue_id,target_issue_id,relation_type,active,created_by_type,created_at) VALUES(?,?,?,?,?,?,?)`, core.NewID("rel_"), duplicate.ID, other.ID, "duplicates", 1, "operator", core.Now()); err != nil {
		t.Fatalf("insert existing duplicate relation: %v", err)
	}
	quotedDuplicateID := "'" + strings.ReplaceAll(duplicate.ID, "'", "''") + "'"
	if err := st.Project.Exec(`ALTER TABLE issue_relations RENAME TO issue_relations_shadow`); err != nil {
		t.Fatalf("rename issue_relations: %v", err)
	}
	if err := st.Project.Exec(`CREATE VIEW issue_relations AS
SELECT id, source_issue_id,
       CASE WHEN source_issue_id=` + quotedDuplicateID + ` AND relation_type='duplicates' THEN json_extract('bad json','$.x') ELSE target_issue_id END AS target_issue_id,
       relation_type, active, created_by_type, created_by_run_id, created_at, resolved_at
FROM issue_relations_shadow`); err != nil {
		t.Fatalf("create failing issue_relations view: %v", err)
	}
	if err := st.Project.Exec(`CREATE TRIGGER insert_issue_relations_view INSTEAD OF INSERT ON issue_relations BEGIN
INSERT INTO issue_relations_shadow(id,source_issue_id,target_issue_id,relation_type,active,created_by_type,created_by_run_id,created_at,resolved_at)
VALUES(NEW.id,NEW.source_issue_id,NEW.target_issue_id,NEW.relation_type,NEW.active,NEW.created_by_type,NEW.created_by_run_id,NEW.created_at,NEW.resolved_at);
END`); err != nil {
		t.Fatalf("create issue_relations insert trigger: %v", err)
	}

	_, err = st.TransitionIssue(duplicate.ID, core.StateDuplicate, "duplicate", canonical.ID)
	if err == nil {
		t.Fatal("TransitionIssue succeeded, want duplicate relation lookup error")
	}
	assertErrorContains(t, err, "malformed JSON")
	row, err := st.Project.QueryOne(`SELECT state FROM issues WHERE id=?`, duplicate.ID)
	if err != nil {
		t.Fatalf("query issue state: %v", err)
	}
	if got := core.IssueState(row["state"].String()); got != core.StateInbox {
		t.Fatalf("issue state = %s, want %s", got, core.StateInbox)
	}
	rows, err := st.Project.Query(`SELECT id FROM issue_relations_shadow WHERE source_issue_id=? AND relation_type='duplicates' AND active=1`, duplicate.ID)
	if err != nil {
		t.Fatalf("query duplicate relations: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("active duplicate relations = %d, want 1", len(rows))
	}
	rows, err = st.Project.Query(`SELECT id FROM issue_relations_shadow WHERE source_issue_id=? AND target_issue_id=? AND relation_type='duplicates' AND active=1`, duplicate.ID, canonical.ID)
	if err != nil {
		t.Fatalf("query canonical duplicate relation: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("canonical duplicate relations = %d, want 0", len(rows))
	}
}

func TestDispatchPauseResumeRejectTerminalIssueWithoutChangingDispatchFields(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, st *Store) *core.Issue
	}{
		{
			name: "done",
			setup: func(t *testing.T, st *Store) *core.Issue {
				t.Helper()
				issue, _ := prepareCompletedReviewRun(t, st)
				done, err := st.MarkDone(issue.ID, "done")
				if err != nil {
					t.Fatalf("MarkDone: %v", err)
				}
				return done
			},
		},
		{
			name: "cancelled",
			setup: func(t *testing.T, st *Store) *core.Issue {
				t.Helper()
				issue, err := st.CreateIssue(CreateIssueInput{
					Title:       "Cancelled issue",
					Description: "desc",
					Priority:    3,
				})
				if err != nil {
					t.Fatalf("CreateIssue: %v", err)
				}
				cancelled, err := st.TransitionIssue(issue.ID, core.StateCancelled, "cancelled", "")
				if err != nil {
					t.Fatalf("TransitionIssue cancelled: %v", err)
				}
				return cancelled
			},
		},
		{
			name: "duplicate",
			setup: func(t *testing.T, st *Store) *core.Issue {
				t.Helper()
				duplicate, err := st.CreateIssue(CreateIssueInput{
					Title:       "Duplicate issue",
					Description: "desc",
					Priority:    3,
				})
				if err != nil {
					t.Fatalf("CreateIssue duplicate: %v", err)
				}
				canonical, err := st.CreateIssue(CreateIssueInput{
					Title:       "Canonical issue",
					Description: "desc",
					Priority:    3,
				})
				if err != nil {
					t.Fatalf("CreateIssue canonical: %v", err)
				}
				issue, err := st.TransitionIssue(duplicate.ID, core.StateDuplicate, "duplicate", canonical.ID)
				if err != nil {
					t.Fatalf("TransitionIssue duplicate: %v", err)
				}
				return issue
			},
		},
	}

	for _, tt := range tests {
		for _, action := range []string{"pause", "resume"} {
			t.Run(tt.name+"_"+action, func(t *testing.T) {
				st := newStoreTestStore(t)
				issue := tt.setup(t, st)

				var err error
				if action == "pause" {
					_, err = st.DispatchPause(issue.ID, "pause")
				} else {
					_, err = st.DispatchResume(issue.ID, "resume")
				}
				if err == nil {
					t.Fatalf("Dispatch%s succeeded, want invalid_state_transition", action)
				}
				if got := core.AsAPIError(err).Code; got != core.ErrInvalidStateTransition {
					t.Fatalf("Dispatch%s error code = %s, want %s", action, got, core.ErrInvalidStateTransition)
				}
				got, err := st.GetIssue(issue.ID)
				if err != nil {
					t.Fatalf("GetIssue: %v", err)
				}
				if got.DispatchPaused != issue.DispatchPaused {
					t.Fatalf("dispatch_paused = %v, want unchanged %v", got.DispatchPaused, issue.DispatchPaused)
				}
				if got.DispatchPauseReason != nil {
					t.Fatalf("dispatch_pause_reason = %q, want nil", *got.DispatchPauseReason)
				}
			})
		}
	}
}

func TestDispatchPauseRollsBackWhenActiveRunAppearsDuringUpdate(t *testing.T) {
	st := newStoreTestStore(t)
	issue := prepareReadyIssue(t, st, "Dispatch pause active race")
	if err := st.Project.Exec(`CREATE TRIGGER race_pause_active_run BEFORE UPDATE OF dispatch_paused ON issues
WHEN NEW.id='` + issue.ID + `' AND NEW.dispatch_paused=1 AND OLD.dispatch_paused=0
BEGIN
	INSERT INTO run_attempts(id,issue_id,attempt_no,status,dispatch_reason,source_issue_state,runner_kind,created_at,updated_at)
	VALUES('run_pause_race', NEW.id, 1, 'pending', 'manual', OLD.state, 'fake', NEW.updated_at, NEW.updated_at);
END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	_, err := st.DispatchPause(issue.ID, "pause")
	if err == nil {
		t.Fatal("DispatchPause succeeded, want active run error")
	}
	if got := core.AsAPIError(err).Code; got != core.ErrIssueAlreadyRunning {
		t.Fatalf("DispatchPause error code = %s, want %s", got, core.ErrIssueAlreadyRunning)
	}
	assertDispatchPaused(t, st, issue.ID, false)
	assertRunAttemptCount(t, st, issue.ID, 0)
}

func TestDispatchPauseRollsBackWhenEventInsertFails(t *testing.T) {
	st := newStoreTestStore(t)
	issue := prepareReadyIssue(t, st, "Dispatch pause event failure")
	if err := st.Project.Exec(`CREATE TRIGGER fail_dispatch_pause_event BEFORE INSERT ON run_events WHEN NEW.event_type='issue.dispatch_paused' BEGIN SELECT RAISE(ABORT, 'dispatch pause event failed'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	_, err := st.DispatchPause(issue.ID, "pause")
	if err == nil {
		t.Fatal("DispatchPause succeeded, want event insert error")
	}
	assertErrorContains(t, err, "dispatch pause event failed")
	assertDispatchPaused(t, st, issue.ID, false)
	assertIssueCommentCount(t, st, issue.ID, "pause", 0)
}

func TestDispatchResumeRollsBackWhenEventInsertFails(t *testing.T) {
	st := newStoreTestStore(t)
	issue := prepareReadyIssue(t, st, "Dispatch resume event failure")
	paused, err := st.DispatchPause(issue.ID, "pause")
	if err != nil {
		t.Fatalf("DispatchPause: %v", err)
	}
	if !paused.DispatchPaused {
		t.Fatal("DispatchPause did not pause issue")
	}
	if err := st.Project.Exec(`CREATE TRIGGER fail_dispatch_resume_event BEFORE INSERT ON run_events WHEN NEW.event_type='issue.dispatch_resumed' BEGIN SELECT RAISE(ABORT, 'dispatch resume event failed'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	_, err = st.DispatchResume(issue.ID, "resume")
	if err == nil {
		t.Fatal("DispatchResume succeeded, want event insert error")
	}
	assertErrorContains(t, err, "dispatch resume event failed")
	assertDispatchPaused(t, st, issue.ID, true)
	assertIssueCommentCount(t, st, issue.ID, "resume", 0)
}

func TestDispatchPauseResumeRejectActiveRunWithoutChangingDispatchFields(t *testing.T) {
	for _, action := range []string{"pause", "resume"} {
		t.Run(action, func(t *testing.T) {
			st := newStoreTestStore(t)
			issue, _ := prepareActiveRun(t, st, "Dispatch "+action+" active run")

			var err error
			if action == "pause" {
				_, err = st.DispatchPause(issue.ID, "pause")
			} else {
				_, err = st.DispatchResume(issue.ID, "resume")
			}
			if err == nil {
				t.Fatalf("Dispatch%s succeeded, want active run error", action)
			}
			if got := core.AsAPIError(err).Code; got != core.ErrIssueAlreadyRunning {
				t.Fatalf("Dispatch%s error code = %s, want %s", action, got, core.ErrIssueAlreadyRunning)
			}
			assertDispatchPaused(t, st, issue.ID, false)
		})
	}
}

func TestDispatchPauseResumeInsertOperatorComments(t *testing.T) {
	st := newStoreTestStore(t)
	issue := prepareReadyIssue(t, st, "Dispatch pause resume comments")

	if _, err := st.DispatchPause(issue.ID, "pause for operator"); err != nil {
		t.Fatalf("DispatchPause: %v", err)
	}
	assertIssueCommentCount(t, st, issue.ID, "pause for operator", 1)

	if _, err := st.DispatchResume(issue.ID, "resume for operator"); err != nil {
		t.Fatalf("DispatchResume: %v", err)
	}
	assertIssueCommentCount(t, st, issue.ID, "resume for operator", 1)
}

func TestClaimRunPropagatesBlockerQueryError(t *testing.T) {
	st := newStoreTestStore(t)
	issue, err := st.CreateIssue(CreateIssueInput{
		Title:              "Dispatch blocker query failure",
		Description:        "desc",
		AcceptanceCriteria: []string{"done"},
		Priority:           3,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if _, err := st.TransitionIssue(issue.ID, core.StateReady, "", ""); err != nil {
		t.Fatalf("TransitionIssue ready: %v", err)
	}
	if err := st.Project.Exec(`DROP TABLE issue_relations`); err != nil {
		t.Fatalf("drop issue_relations: %v", err)
	}

	_, err = st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err == nil {
		t.Fatal("ClaimRun succeeded, want blocker query error")
	}
	row, err := st.Project.QueryOne(`SELECT COUNT(*) AS c FROM run_attempts WHERE issue_id=?`, issue.ID)
	if err != nil {
		t.Fatalf("count run attempts: %v", err)
	}
	if got := row["c"].Int(); got != 0 {
		t.Fatalf("run attempts = %d, want 0", got)
	}
}

func TestClaimRunUsesSchemaAllowedActorTypesForDispatchEvents(t *testing.T) {
	st := newStoreTestStore(t)
	issue := prepareReadyIssue(t, st, "ClaimRun dispatch event actor types")

	run, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}

	rows, err := st.Project.Query(`SELECT event_type, actor_type FROM run_events WHERE run_id=? AND event_type IN ('run.claimed','issue.state_changed') ORDER BY event_type`, run.ID)
	if err != nil {
		t.Fatalf("query dispatch events: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("dispatch event count = %d, want 2", len(rows))
	}
	for _, row := range rows {
		if got := row["actor_type"].String(); got != "system" {
			t.Fatalf("%s actor_type = %q, want system", row["event_type"].String(), got)
		}
	}

	hist, err := st.Project.QueryOne(`SELECT actor_type FROM issue_state_history WHERE issue_id=? AND run_id=? AND reason='dispatch'`, issue.ID, run.ID)
	if err != nil {
		t.Fatalf("query dispatch history: %v", err)
	}
	if got := hist["actor_type"].String(); got != "orchestrator" {
		t.Fatalf("dispatch history actor_type = %q, want orchestrator", got)
	}
}

func TestClaimRunRollsBackWhenStateHistoryInsertFails(t *testing.T) {
	st := newStoreTestStore(t)
	issue := prepareReadyIssue(t, st, "ClaimRun history failure")
	if err := st.Project.Exec(`CREATE TRIGGER fail_claim_history BEFORE INSERT ON issue_state_history WHEN NEW.reason='dispatch' BEGIN SELECT RAISE(ABORT, 'claim history failed'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	_, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err == nil {
		t.Fatal("ClaimRun succeeded, want state history error")
	}
	assertIssueState(t, st, issue.ID, core.StateReady)
	assertRunAttemptCount(t, st, issue.ID, 0)
	assertNoClaimDispatchEvents(t, st, issue.ID)
}

func TestClaimRunRollsBackWhenEventInsertFails(t *testing.T) {
	st := newStoreTestStore(t)
	issue := prepareReadyIssue(t, st, "ClaimRun event failure")
	if err := st.Project.Exec(`CREATE TRIGGER fail_claim_event BEFORE INSERT ON run_events WHEN NEW.event_type='run.claimed' BEGIN SELECT RAISE(ABORT, 'claim event failed'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	_, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err == nil {
		t.Fatal("ClaimRun succeeded, want event insert error")
	}
	assertIssueState(t, st, issue.ID, core.StateReady)
	assertRunAttemptCount(t, st, issue.ID, 0)
	assertNoClaimDispatchEvents(t, st, issue.ID)
}

func TestClaimRunPropagatesActiveRunLookupError(t *testing.T) {
	st := newStoreTestStore(t)
	issue := prepareReadyIssue(t, st, "ClaimRun active lookup failure")
	if err := st.Project.Exec(`ALTER TABLE run_attempts RENAME TO run_attempts_shadow`); err != nil {
		t.Fatalf("rename run_attempts: %v", err)
	}

	_, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err == nil {
		t.Fatal("ClaimRun succeeded, want active run lookup error")
	}

	if err := st.Project.Exec(`ALTER TABLE run_attempts_shadow RENAME TO run_attempts`); err != nil {
		t.Fatalf("restore run_attempts: %v", err)
	}
	assertIssueState(t, st, issue.ID, core.StateReady)
	assertRunAttemptCount(t, st, issue.ID, 0)
	assertNoClaimDispatchEvents(t, st, issue.ID)
}

func TestClaimRunPropagatesConcurrencyCountError(t *testing.T) {
	st := newStoreTestStore(t)
	issue := prepareReadyIssue(t, st, "ClaimRun concurrency count failure")
	if err := st.Project.Exec(`ALTER TABLE run_attempts RENAME TO run_attempts_shadow`); err != nil {
		t.Fatalf("rename run_attempts: %v", err)
	}
	if err := st.Project.Exec(`CREATE TABLE run_attempts_probe(id TEXT, issue_id TEXT, attempt_no INTEGER, status_raw TEXT, created_at TEXT)`); err != nil {
		t.Fatalf("create probe table: %v", err)
	}
	if err := st.Project.Exec(`CREATE INDEX idx_run_attempts_probe_issue ON run_attempts_probe(issue_id)`); err != nil {
		t.Fatalf("create probe issue index: %v", err)
	}
	if err := st.Project.Exec(`INSERT INTO run_attempts_probe(id,issue_id,attempt_no,status_raw,created_at) VALUES(?,?,?,?,?)`, core.NewID("run_"), "issue_poison", 1, string(core.RunPending), core.Now()); err != nil {
		t.Fatalf("insert poison run: %v", err)
	}
	if err := st.Project.Exec(`CREATE VIEW run_attempts AS SELECT id,issue_id,attempt_no,CASE WHEN issue_id='issue_poison' THEN json_extract('bad json','$.x') ELSE status_raw END AS status,created_at FROM run_attempts_probe`); err != nil {
		t.Fatalf("create run_attempts view: %v", err)
	}

	_, err := st.ClaimRun(issue.ID, "manual", "fake", 1)

	if dropErr := st.Project.Exec(`DROP VIEW run_attempts`); dropErr != nil {
		t.Fatalf("drop run_attempts view: %v", dropErr)
	}
	if dropErr := st.Project.Exec(`DROP TABLE run_attempts_probe`); dropErr != nil {
		t.Fatalf("drop probe table: %v", dropErr)
	}
	if restoreErr := st.Project.Exec(`ALTER TABLE run_attempts_shadow RENAME TO run_attempts`); restoreErr != nil {
		t.Fatalf("restore run_attempts: %v", restoreErr)
	}
	if err == nil {
		t.Fatal("ClaimRun succeeded, want concurrency count error")
	}
	assertErrorContains(t, err, "malformed JSON")
	assertIssueState(t, st, issue.ID, core.StateReady)
	assertRunAttemptCount(t, st, issue.ID, 0)
	assertNoClaimDispatchEvents(t, st, issue.ID)
}

func TestClaimRunPropagatesAttemptNumberError(t *testing.T) {
	st := newStoreTestStore(t)
	issue := prepareReadyIssue(t, st, "ClaimRun attempt number failure")
	if err := st.Project.Exec(`ALTER TABLE run_attempts RENAME TO run_attempts_shadow`); err != nil {
		t.Fatalf("rename run_attempts: %v", err)
	}
	if err := st.Project.Exec(`CREATE TABLE run_attempts(id TEXT, issue_id TEXT, status TEXT, created_at TEXT)`); err != nil {
		t.Fatalf("create replacement run_attempts: %v", err)
	}

	_, err := st.ClaimRun(issue.ID, "manual", "fake", 1)

	if dropErr := st.Project.Exec(`DROP TABLE run_attempts`); dropErr != nil {
		t.Fatalf("drop replacement run_attempts: %v", dropErr)
	}
	if restoreErr := st.Project.Exec(`ALTER TABLE run_attempts_shadow RENAME TO run_attempts`); restoreErr != nil {
		t.Fatalf("restore run_attempts: %v", restoreErr)
	}
	if err == nil {
		t.Fatal("ClaimRun succeeded, want attempt number error")
	}
	assertErrorContains(t, err, "no such column: attempt_no")
	assertIssueState(t, st, issue.ID, core.StateReady)
	assertRunAttemptCount(t, st, issue.ID, 0)
	assertNoClaimDispatchEvents(t, st, issue.ID)
}

func TestCreateOrUpdateWorkspacePropagatesLookupError(t *testing.T) {
	st := newStoreTestStore(t)
	issue := prepareReadyIssue(t, st, "Workspace lookup failure")
	quotedIssueID := "'" + strings.ReplaceAll(issue.ID, "'", "''") + "'"
	if err := st.Project.Exec(`ALTER TABLE workspaces RENAME TO workspaces_shadow`); err != nil {
		t.Fatalf("rename workspaces: %v", err)
	}
	if err := st.Project.Exec(`CREATE VIEW workspaces AS
SELECT json_extract('bad json','$.x') AS id, ` + quotedIssueID + ` AS issue_id, '/tmp/poison' AS path, 'poison' AS branch_name, 'auto' AS base_ref_config, 'main' AS base_ref, 'base' AS base_sha, 'prepared' AS status, 'created' AS created_at, 'updated' AS updated_at
UNION ALL
SELECT id, issue_id, path, branch_name, base_ref_config, base_ref, base_sha, status, created_at, updated_at FROM workspaces_shadow`); err != nil {
		t.Fatalf("create failing workspaces view: %v", err)
	}
	if err := st.Project.Exec(`CREATE TRIGGER insert_workspaces_view INSTEAD OF INSERT ON workspaces BEGIN
INSERT INTO workspaces_shadow(id,issue_id,path,branch_name,base_ref_config,base_ref,base_sha,status,created_at,updated_at)
VALUES(NEW.id,NEW.issue_id,NEW.path,NEW.branch_name,NEW.base_ref_config,NEW.base_ref,NEW.base_sha,NEW.status,NEW.created_at,NEW.updated_at);
END`); err != nil {
		t.Fatalf("create workspaces insert trigger: %v", err)
	}

	_, err := st.CreateOrUpdateWorkspace(issue.ID, "/tmp/workspace", "branch", "auto", "main", "base")
	if err == nil {
		t.Fatal("CreateOrUpdateWorkspace succeeded, want lookup error")
	}
	assertErrorContains(t, err, "malformed JSON")
	row, err := st.Project.QueryOne(`SELECT COUNT(*) AS c FROM workspaces_shadow WHERE issue_id=?`, issue.ID)
	if err != nil {
		t.Fatalf("count workspaces: %v", err)
	}
	if got := row["c"].Int(); got != 0 {
		t.Fatalf("workspace rows = %d, want 0", got)
	}
}

func TestClaimRunRollsBackRunClaimedEventWhenStateChangedEventFails(t *testing.T) {
	st := newStoreTestStore(t)
	issue := prepareReadyIssue(t, st, "ClaimRun state changed event failure")
	if err := st.Project.Exec(`CREATE TRIGGER fail_claim_state_changed_event BEFORE INSERT ON run_events WHEN NEW.event_type='issue.state_changed' BEGIN SELECT RAISE(ABORT, 'claim state changed event failed'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	_, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err == nil {
		t.Fatal("ClaimRun succeeded, want issue.state_changed event error")
	}
	assertIssueState(t, st, issue.ID, core.StateReady)
	assertRunAttemptCount(t, st, issue.ID, 0)
	assertNoClaimDispatchEvents(t, st, issue.ID)
}

func TestClaimRunReturnsClaimedRunWhenPostCommitReadFails(t *testing.T) {
	st := newStoreTestStore(t)
	issue := prepareReadyIssue(t, st, "ClaimRun post-commit read failure")
	quotedIssueID := "'" + strings.ReplaceAll(issue.ID, "'", "''") + "'"
	if err := st.Project.Exec(`ALTER TABLE run_attempts RENAME TO run_attempts_shadow`); err != nil {
		t.Fatalf("rename run_attempts: %v", err)
	}
	if err := st.Project.Exec(`CREATE VIEW run_attempts AS
SELECT id, issue_id, attempt_no, workspace_id, workflow_snapshot_id,
       CASE WHEN issue_id=` + quotedIssueID + ` THEN json_extract('bad json','$.x') ELSE status END AS status,
       dispatch_reason, source_issue_state, runner_kind, base_ref_config, base_ref, base_sha, branch_name,
       failure_code, failure_message, started_at, ended_at, created_at, updated_at
FROM run_attempts_shadow`); err != nil {
		t.Fatalf("create failing run_attempts view: %v", err)
	}
	if err := st.Project.Exec(`CREATE TRIGGER insert_run_attempts_view INSTEAD OF INSERT ON run_attempts BEGIN
	INSERT INTO run_attempts_shadow(id,issue_id,attempt_no,workspace_id,workflow_snapshot_id,status,dispatch_reason,source_issue_state,runner_kind,base_ref_config,base_ref,base_sha,branch_name,failure_code,failure_message,started_at,ended_at,created_at,updated_at)
	VALUES(NEW.id,NEW.issue_id,NEW.attempt_no,NEW.workspace_id,NEW.workflow_snapshot_id,NEW.status,NEW.dispatch_reason,NEW.source_issue_state,NEW.runner_kind,NEW.base_ref_config,NEW.base_ref,NEW.base_sha,NEW.branch_name,NEW.failure_code,NEW.failure_message,NEW.started_at,NEW.ended_at,NEW.created_at,NEW.updated_at);
END`); err != nil {
		t.Fatalf("create insert trigger: %v", err)
	}

	run, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun returned post-commit read error: %v", err)
	}
	if run.ID == "" || run.IssueID != issue.ID || run.IssueIdentifier != issue.Identifier || run.AttemptNo != 1 || run.Status != core.RunPending {
		t.Fatalf("claimed run = %#v, want populated pending run for issue %s", run, issue.ID)
	}
	row, err := st.Project.QueryOne(`SELECT COUNT(*) AS c FROM run_attempts_shadow WHERE issue_id=?`, issue.ID)
	if err != nil {
		t.Fatalf("count shadow run attempts: %v", err)
	}
	if got := row["c"].Int(); got != 1 {
		t.Fatalf("shadow run attempts = %d, want 1", got)
	}
	row, err = st.Project.QueryOne(`SELECT state FROM issues WHERE id=?`, issue.ID)
	if err != nil {
		t.Fatalf("query issue state: %v", err)
	}
	if got := core.IssueState(row["state"].String()); got != core.StateWorking {
		t.Fatalf("issue state = %s, want %s", got, core.StateWorking)
	}
}

func TestInsertReviewPacketRollsBackWhenIssuePointerUpdateFails(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareActiveRun(t, st, "Review packet pointer failure")
	handoff, err := st.InsertHandoff(issue.ID, run.ID, "payload-hash", map[string]any{
		"summary":      "ready for review",
		"target_state": "Human Review",
	})
	if err != nil {
		t.Fatalf("InsertHandoff: %v", err)
	}
	if err := st.Project.Exec(`CREATE TRIGGER fail_latest_review_packet_update BEFORE UPDATE OF latest_review_packet_id ON issues BEGIN SELECT RAISE(ABORT, 'latest review packet update failed'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	_, err = st.InsertReviewPacket(issue.ID, run.ID, handoff.ID, st.RepoRoot, "review.md", "review.json", "patch.diff", "changed.txt", "untracked.txt", "diffstat.txt", "")
	if err == nil {
		t.Fatal("InsertReviewPacket succeeded, want issue pointer update error")
	}
	row, err := st.Project.QueryOne(`SELECT COUNT(*) AS c FROM review_packets WHERE issue_id=?`, issue.ID)
	if err != nil {
		t.Fatalf("count review packets: %v", err)
	}
	if got := row["c"].Int(); got != 0 {
		t.Fatalf("review packet count = %d, want 0", got)
	}
	got, err := st.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.LatestReviewPacketID != nil {
		t.Fatalf("LatestReviewPacketID = %v, want nil", *got.LatestReviewPacketID)
	}
}

func TestInsertHandoffRollsBackWhenSubmittedEventFails(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareActiveRun(t, st, "Handoff event failure")
	if err := st.Project.Exec(`CREATE TRIGGER fail_handoff_submitted_event BEFORE INSERT ON run_events WHEN NEW.event_type='handoff.submitted' BEGIN SELECT RAISE(ABORT, 'handoff submitted event failed'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	_, err := st.InsertHandoff(issue.ID, run.ID, "payload-hash", map[string]any{
		"summary":      "ready for review",
		"target_state": "Human Review",
	})
	if err == nil {
		t.Fatal("InsertHandoff succeeded, want handoff event error")
	}
	assertErrorContains(t, err, "handoff submitted event failed")
	rows, qerr := st.Project.Query(`SELECT id FROM handoffs WHERE run_id=?`, run.ID)
	if qerr != nil {
		t.Fatalf("query handoffs: %v", qerr)
	}
	if len(rows) != 0 {
		t.Fatalf("handoff rows = %d, want 0 after rollback", len(rows))
	}
}

func TestInsertHandoffPropagatesLookupError(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareActiveRun(t, st, "Handoff lookup failure")
	quotedRunID := "'" + strings.ReplaceAll(run.ID, "'", "''") + "'"
	quotedIssueID := "'" + strings.ReplaceAll(issue.ID, "'", "''") + "'"
	if err := st.Project.Exec(`ALTER TABLE handoffs RENAME TO handoffs_shadow`); err != nil {
		t.Fatalf("rename handoffs: %v", err)
	}
	if err := st.Project.Exec(`CREATE VIEW handoffs AS
SELECT json_extract('bad json','$.x') AS id, ` + quotedIssueID + ` AS issue_id, ` + quotedRunID + ` AS run_id, 'existing-hash' AS payload_hash, '{}' AS payload_json_redacted, 'poison' AS summary, '[]' AS changed_files_json, '[]' AS tests_json, '[]' AS risks_json, '[]' AS verification_json, '[]' AS followups_json, 'Human Review' AS target_state, 'submitted' AS submitted_at
UNION ALL
SELECT id, issue_id, run_id, payload_hash, payload_json_redacted, summary, changed_files_json, tests_json, risks_json, verification_json, followups_json, target_state, submitted_at FROM handoffs_shadow`); err != nil {
		t.Fatalf("create failing handoffs view: %v", err)
	}
	if err := st.Project.Exec(`CREATE TRIGGER insert_handoffs_view INSTEAD OF INSERT ON handoffs BEGIN
INSERT INTO handoffs_shadow(id,issue_id,run_id,payload_hash,payload_json_redacted,summary,changed_files_json,tests_json,risks_json,verification_json,followups_json,target_state,submitted_at)
VALUES(NEW.id,NEW.issue_id,NEW.run_id,NEW.payload_hash,NEW.payload_json_redacted,NEW.summary,NEW.changed_files_json,NEW.tests_json,NEW.risks_json,NEW.verification_json,NEW.followups_json,NEW.target_state,NEW.submitted_at);
END`); err != nil {
		t.Fatalf("create handoffs insert trigger: %v", err)
	}

	_, err := st.InsertHandoff(issue.ID, run.ID, "payload-hash", map[string]any{
		"summary":      "ready for review",
		"target_state": "Human Review",
	})
	if err == nil {
		t.Fatal("InsertHandoff succeeded, want lookup error")
	}
	assertErrorContains(t, err, "malformed JSON")
	row, err := st.Project.QueryOne(`SELECT COUNT(*) AS c FROM handoffs_shadow WHERE run_id=?`, run.ID)
	if err != nil {
		t.Fatalf("count handoffs: %v", err)
	}
	if got := row["c"].Int(); got != 0 {
		t.Fatalf("handoff rows = %d, want 0", got)
	}
	rows, err := st.Project.Query(`SELECT id FROM run_events WHERE run_id=? AND event_type='handoff.submitted'`, run.ID)
	if err != nil {
		t.Fatalf("query handoff submitted events: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("handoff submitted events = %d, want 0", len(rows))
	}
}

func TestRecordToolCallStoresRedactedJSONWithoutSecretLikeValues(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareActiveRun(t, st, "Tool call redaction")
	secretInput := "sk_live_secret_value"
	secretOutput := "ghp_secret_value"
	secretInputKey := "prompt: include sk_live_key_from_dynamic_key"
	secretOutputKey := "stdout ghp_key_from_dynamic_key"

	if err := st.RecordToolCall(issue.ID, run.ID, "shell", "completed", map[string]any{
		"command":      "deploy",
		"token":        secretInput,
		secretInputKey: "redacted by key summary",
	}, map[string]any{
		"stdout":        secretOutput,
		secretOutputKey: "redacted by key summary",
	}, "", ""); err != nil {
		t.Fatalf("RecordToolCall: %v", err)
	}

	row, err := st.Project.QueryOne(`SELECT input_json_redacted, output_json_redacted FROM tool_calls WHERE run_id=?`, run.ID)
	if err != nil {
		t.Fatalf("query tool_call: %v", err)
	}
	inputRedacted := row["input_json_redacted"].String()
	outputRedacted := row["output_json_redacted"].String()
	if strings.Contains(inputRedacted, secretInput) {
		t.Fatalf("input_json_redacted contains secret-like input: %q", inputRedacted)
	}
	if strings.Contains(outputRedacted, secretOutput) {
		t.Fatalf("output_json_redacted contains secret-like output: %q", outputRedacted)
	}
	if strings.Contains(inputRedacted, secretInputKey) {
		t.Fatalf("input_json_redacted contains secret-like key: %q", inputRedacted)
	}
	if strings.Contains(outputRedacted, secretOutputKey) {
		t.Fatalf("output_json_redacted contains secret-like key: %q", outputRedacted)
	}
	if inputRedacted == "" || outputRedacted == "" {
		t.Fatalf("redacted JSON fields must be populated, got input=%q output=%q", inputRedacted, outputRedacted)
	}
}

func TestCreateFollowupIssueRollsBackWhenRelationInsertConflicts(t *testing.T) {
	st := newStoreTestStore(t)
	issue, run := prepareActiveRun(t, st, "Followup relation conflict")
	_, err := st.CreateFollowupIssue(issue.ID, run.ID, CreateIssueInput{
		Title:              "Existing followup",
		Description:        "desc",
		AcceptanceCriteria: []string{"done"},
		Priority:           3,
		CreatedByType:      "agent",
		CreatedByRunID:     &run.ID,
	})
	if err != nil {
		t.Fatalf("CreateFollowupIssue existing: %v", err)
	}
	if err := st.Project.Exec(`CREATE UNIQUE INDEX fail_second_followup_relation ON issue_relations(relation_type) WHERE relation_type='followup_of'`); err != nil {
		t.Fatalf("create relation conflict index: %v", err)
	}

	_, err = st.CreateFollowupIssue(issue.ID, run.ID, CreateIssueInput{
		Title:              "Conflicting followup",
		Description:        "desc",
		AcceptanceCriteria: []string{"done"},
		Priority:           3,
		CreatedByType:      "agent",
		CreatedByRunID:     &run.ID,
	})
	if err == nil {
		t.Fatal("CreateFollowupIssue succeeded, want relation insert conflict")
	}
	row, err := st.Project.QueryOne(`SELECT COUNT(*) AS c FROM issues`)
	if err != nil {
		t.Fatalf("count issues: %v", err)
	}
	if got := row["c"].Int(); got != 2 {
		t.Fatalf("issue count = %d, want 2", got)
	}
	row, err = st.Project.QueryOne(`SELECT value FROM counters WHERE name='issue_sequence'`)
	if err != nil {
		t.Fatalf("read issue sequence: %v", err)
	}
	if got := row["value"].Int(); got != 2 {
		t.Fatalf("issue sequence = %d, want 2", got)
	}
	row, err = st.Project.QueryOne(`SELECT COUNT(*) AS c FROM run_events WHERE event_type='issue.created'`)
	if err != nil {
		t.Fatalf("count issue.created events: %v", err)
	}
	if got := row["c"].Int(); got != 2 {
		t.Fatalf("issue.created events = %d, want 2", got)
	}
	row, err = st.Project.QueryOne(`SELECT COUNT(*) AS c FROM issue_relations WHERE relation_type='followup_of'`)
	if err != nil {
		t.Fatalf("count followup relations: %v", err)
	}
	if got := row["c"].Int(); got != 1 {
		t.Fatalf("followup relations = %d, want 1", got)
	}
}

func newStoreTestStore(t *testing.T) *Store {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	repoRoot := filepath.Join(t.TempDir(), "repo")
	st, err := InitProject(repoRoot, "TST")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	return st
}

func removeSQLiteFiles(t *testing.T, path string) {
	t.Helper()
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			t.Fatalf("remove %s: %v", p, err)
		}
	}
}

func assertNoOpenSQLiteFile(t *testing.T, path string) {
	t.Helper()
	count, ok := openFileDescriptorCount(t, path)
	if !ok {
		t.Skip("cannot inspect process file descriptors")
	}
	if count != 0 {
		t.Fatalf("%s has %d open file descriptors, want 0", path, count)
	}
}

func assertUnsupportedDBVersionDetails(t *testing.T, apiErr *core.APIError, dbPath, detected string) {
	t.Helper()
	if got := apiErr.Details["db_path"]; got != dbPath {
		t.Fatalf("db_path detail = %v, want %s", got, dbPath)
	}
	if got := apiErr.Details["detected_version"]; got != detected {
		t.Fatalf("detected_version detail = %v, want %s", got, detected)
	}
	if got := apiErr.Details["expected_version"]; got != "1" {
		t.Fatalf("expected_version detail = %v, want 1", got)
	}
	guidance, ok := apiErr.Details["operator_guidance"].(string)
	if !ok || guidance == "" {
		t.Fatalf("operator_guidance detail missing: %#v", apiErr.Details["operator_guidance"])
	}
	lower := strings.ToLower(guidance)
	for _, want := range []string{"compatible binary", "operator-maintained backup", "new project db", "does not provide automatic"} {
		if !strings.Contains(lower, want) {
			t.Fatalf("operator guidance %q missing %q", guidance, want)
		}
	}
}

func openFileDescriptorCount(t *testing.T, path string) (int, bool) {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}
	canonicalAbs := abs
	if evaluated, err := filepath.EvalSymlinks(abs); err == nil {
		canonicalAbs = evaluated
	}
	for _, dir := range []string{"/proc/self/fd", "/dev/fd"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		count := 0
		for _, entry := range entries {
			target, err := os.Readlink(filepath.Join(dir, entry.Name()))
			if err != nil {
				continue
			}
			target = strings.TrimSuffix(target, " (deleted)")
			canonicalTarget := target
			if evaluated, err := filepath.EvalSymlinks(target); err == nil {
				canonicalTarget = evaluated
			}
			if target == abs || canonicalTarget == canonicalAbs || strings.HasPrefix(target, abs+" ") {
				count++
			}
		}
		return count, true
	}
	return 0, false
}

func prepareActiveRun(t *testing.T, st *Store, title string) (*core.Issue, *core.RunAttempt) {
	t.Helper()
	return prepareActiveRunWithMaxConcurrent(t, st, title, 1)
}

func prepareReadyIssue(t *testing.T, st *Store, title string) *core.Issue {
	t.Helper()
	issue, err := st.CreateIssue(CreateIssueInput{
		Title:              title,
		Description:        "desc",
		AcceptanceCriteria: []string{"done"},
		Priority:           3,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	issue, err = st.TransitionIssue(issue.ID, core.StateReady, "", "")
	if err != nil {
		t.Fatalf("TransitionIssue: %v", err)
	}
	return issue
}

func prepareActiveRunWithMaxConcurrent(t *testing.T, st *Store, title string, maxConcurrent int) (*core.Issue, *core.RunAttempt) {
	t.Helper()
	issue := prepareReadyIssue(t, st, title)
	run, err := st.ClaimRun(issue.ID, "manual", "fake", maxConcurrent)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	return issue, run
}

func prepareCompletedReviewRun(t *testing.T, st *Store) (*core.Issue, *core.RunAttempt) {
	t.Helper()
	issue, err := st.CreateIssue(CreateIssueInput{
		Title:              "Completed run cancellation",
		Description:        "desc",
		AcceptanceCriteria: []string{"done"},
		Priority:           3,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if _, err := st.TransitionIssue(issue.ID, core.StateReady, "", ""); err != nil {
		t.Fatalf("TransitionIssue: %v", err)
	}
	run, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	handoff, err := st.InsertHandoff(issue.ID, run.ID, "payload-hash", map[string]any{
		"summary":      "ready for review",
		"target_state": "Human Review",
	})
	if err != nil {
		t.Fatalf("InsertHandoff: %v", err)
	}
	reviewPacketID, err := st.InsertReviewPacket(issue.ID, run.ID, handoff.ID, st.RepoRoot, "review.md", "review.json", "patch.diff", "changed.txt", "untracked.txt", "diffstat.txt", "")
	if err != nil {
		t.Fatalf("InsertReviewPacket: %v", err)
	}
	if err := st.CompleteRunWithReview(run.ID, reviewPacketID); err != nil {
		t.Fatalf("CompleteRunWithReview: %v", err)
	}
	issue, err = st.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	run, err = st.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	return issue, run
}

func prepareCancelledRun(t *testing.T, st *Store) (*core.Issue, *core.RunAttempt) {
	t.Helper()
	issue, err := st.CreateIssue(CreateIssueInput{
		Title:              "Cancelled run",
		Description:        "desc",
		AcceptanceCriteria: []string{"done"},
		Priority:           3,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if _, err := st.TransitionIssue(issue.ID, core.StateReady, "", ""); err != nil {
		t.Fatalf("TransitionIssue: %v", err)
	}
	run, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	if err := st.CancelRun(run.ID, "operator cancelled"); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}
	issue, err = st.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	run, err = st.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	return issue, run
}

func replaceIssuesWithStateSelectErrorView(t *testing.T, st *Store) {
	t.Helper()
	if err := st.Project.Exec(`ALTER TABLE issues RENAME TO issues_shadow`); err != nil {
		t.Fatalf("rename issues table: %v", err)
	}
	if err := st.Project.Exec(`CREATE VIEW issues AS SELECT id, identifier, (SELECT value FROM missing_state_source) AS state, dispatch_paused, dispatch_pause_reason, dispatch_paused_at, updated_at FROM issues_shadow`); err != nil {
		t.Fatalf("create issues error view: %v", err)
	}
	if err := st.Project.Exec(`CREATE TRIGGER ignore_issue_update INSTEAD OF UPDATE ON issues BEGIN SELECT 1; END`); err != nil {
		t.Fatalf("create issue update trigger: %v", err)
	}
}

func restoreIssuesTable(t *testing.T, st *Store) {
	t.Helper()
	if err := st.Project.Exec(`DROP VIEW issues`); err != nil {
		t.Fatalf("drop issues error view: %v", err)
	}
	if err := st.Project.Exec(`ALTER TABLE issues_shadow RENAME TO issues`); err != nil {
		t.Fatalf("restore issues table: %v", err)
	}
}

func insertReviewPacketForRun(t *testing.T, st *Store, issueID, runID string) string {
	t.Helper()
	handoff, err := st.InsertHandoff(issueID, runID, core.NewID("hash_"), map[string]any{
		"summary":      "ready for review",
		"target_state": "Human Review",
	})
	if err != nil {
		t.Fatalf("InsertHandoff: %v", err)
	}
	reviewPacketID, err := st.InsertReviewPacket(issueID, runID, handoff.ID, st.RepoRoot, "review.md", "review.json", "patch.diff", "changed.txt", "untracked.txt", "diffstat.txt", "")
	if err != nil {
		t.Fatalf("InsertReviewPacket: %v", err)
	}
	return reviewPacketID
}

func latestReviewPacketID(t *testing.T, st *Store, runID string) string {
	t.Helper()
	row, err := st.Project.QueryOne(`SELECT id FROM review_packets WHERE run_id=? ORDER BY packet_no DESC LIMIT 1`, runID)
	if err != nil {
		t.Fatalf("latest review packet: %v", err)
	}
	return row["id"].String()
}

func getReviewPacketRow(t *testing.T, st *Store, reviewPacketID string) map[string]db.Value {
	t.Helper()
	row, err := st.Project.QueryOne(`SELECT * FROM review_packets WHERE id=?`, reviewPacketID)
	if err != nil {
		t.Fatalf("get review packet: %v", err)
	}
	return row
}

func assertRunStatus(t *testing.T, st *Store, runID string, want core.RunStatus) {
	t.Helper()
	gotRun, err := st.GetRun(runID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if gotRun.Status != want {
		t.Fatalf("run status = %s, want %s", gotRun.Status, want)
	}
}

func assertIssueState(t *testing.T, st *Store, issueID string, want core.IssueState) {
	t.Helper()
	gotIssue, err := st.GetIssue(issueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if gotIssue.State != want {
		t.Fatalf("issue state = %s, want %s", gotIssue.State, want)
	}
}

func assertDispatchPaused(t *testing.T, st *Store, issueID string, want bool) {
	t.Helper()
	gotIssue, err := st.GetIssue(issueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if gotIssue.DispatchPaused != want {
		t.Fatalf("dispatch_paused = %v, want %v", gotIssue.DispatchPaused, want)
	}
}

func assertIssueLatestReviewPacketID(t *testing.T, st *Store, issueID string, want *string) {
	t.Helper()
	row, err := st.Project.QueryOne(`SELECT latest_review_packet_id FROM issues WHERE id=?`, issueID)
	if err != nil {
		t.Fatalf("get issue latest_review_packet_id: %v", err)
	}
	got := row["latest_review_packet_id"].String()
	if want == nil {
		if got != "" {
			t.Fatalf("latest_review_packet_id = %q, want empty", got)
		}
		return
	}
	if got != *want {
		t.Fatalf("latest_review_packet_id = %q, want %q", got, *want)
	}
}

func assertRunAttemptCount(t *testing.T, st *Store, issueID string, want int) {
	t.Helper()
	row, err := st.Project.QueryOne(`SELECT COUNT(*) AS c FROM run_attempts WHERE issue_id=?`, issueID)
	if err != nil {
		t.Fatalf("count run attempts: %v", err)
	}
	if got := row["c"].Int(); got != want {
		t.Fatalf("run attempts = %d, want %d", got, want)
	}
}

func assertNoClaimDispatchEvents(t *testing.T, st *Store, issueID string) {
	t.Helper()
	rows, err := st.Project.Query(`SELECT id FROM run_events WHERE issue_id=? AND event_type IN ('run.claimed','issue.state_changed')`, issueID)
	if err != nil {
		t.Fatalf("query claim dispatch events: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("claim dispatch events = %d, want 0 after rollback", len(rows))
	}
}

func assertNoReviewGeneratedEvent(t *testing.T, st *Store, runID string) {
	t.Helper()
	rows, err := st.Project.Query(`SELECT id FROM run_events WHERE run_id=? AND event_type='review.packet_generated'`, runID)
	if err != nil {
		t.Fatalf("query review generated events: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("review generated events = %d, want 0 after rollback", len(rows))
	}
}

func assertErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("error is nil, want %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want to contain %q", err.Error(), want)
	}
}

func assertNoRunFailureAudit(t *testing.T, st *Store, runID string) {
	t.Helper()
	commentRows, err := st.Project.Query(`SELECT id FROM issue_comments WHERE run_id=? AND body LIKE 'Run ended with %'`, runID)
	if err != nil {
		t.Fatalf("query failure comments: %v", err)
	}
	if len(commentRows) != 0 {
		t.Fatalf("failure comments = %d, want 0 after rollback", len(commentRows))
	}
	eventRows, err := st.Project.Query(`SELECT id FROM run_events WHERE run_id=? AND event_type IN ('run.failed','scheduler.paused')`, runID)
	if err != nil {
		t.Fatalf("query failure events: %v", err)
	}
	if len(eventRows) != 0 {
		t.Fatalf("failure events = %d, want 0 after rollback", len(eventRows))
	}
}

func assertIssueCommentCount(t *testing.T, st *Store, issueID, body string, want int) {
	t.Helper()
	row, err := st.Project.QueryOne(`SELECT COUNT(*) AS c FROM issue_comments WHERE issue_id=? AND author_type='operator' AND body=?`, issueID, body)
	if err != nil {
		t.Fatalf("count issue comments: %v", err)
	}
	if got := row["c"].Int(); got != want {
		t.Fatalf("operator comments with body %q = %d, want %d", body, got, want)
	}
}

func insertPendingApproval(t *testing.T, st *Store, runID, issueID string) string {
	t.Helper()
	id := core.NewID("apr_")
	if err := st.Project.Exec(`INSERT INTO approval_requests(id,run_id,issue_id,kind,status,request_json,created_at) VALUES(?,?,?,?,?,?,?)`, id, runID, issueID, "command", "pending", `{"command":"test"}`, core.Now()); err != nil {
		t.Fatalf("insert approval: %v", err)
	}
	return id
}

func getApprovalRow(t *testing.T, st *Store, id string) map[string]db.Value {
	t.Helper()
	row, err := st.Project.QueryOne(`SELECT * FROM approval_requests WHERE id=?`, id)
	if err != nil {
		t.Fatalf("get approval: %v", err)
	}
	return row
}
