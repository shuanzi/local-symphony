package store

import (
	"path/filepath"
	"testing"

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
