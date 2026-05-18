package store

import (
	"path/filepath"
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
