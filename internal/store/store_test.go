package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-symphony/internal/core"
	"local-symphony/internal/db"
)

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
