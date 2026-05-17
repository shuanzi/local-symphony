package review

import (
	"os"
	"path/filepath"
	"testing"

	"local-symphony/internal/core"
	"local-symphony/internal/store"
)

func TestGenerateReturnsErrorAndDoesNotInsertPacketWhenReviewArtifactWriteFails(t *testing.T) {
	st := newReviewTestStore(t)
	issue, run := prepareReviewRun(t, st)

	conflictPath := filepath.Join(st.RepoRoot, ".symphony", "artifacts", issue.Identifier, run.ID, "review.md")
	if err := os.MkdirAll(conflictPath, 0o755); err != nil {
		t.Fatalf("create conflicting review.md directory: %v", err)
	}

	_, err := (Generator{Store: st}).Generate(run.ID)
	if err == nil {
		t.Fatal("Generate succeeded, want review_packet_failed")
	}
	if got := core.AsAPIError(err).Code; got != core.ErrReviewPacketFailed {
		t.Fatalf("Generate error code = %s, want %s", got, core.ErrReviewPacketFailed)
	}
	assertReviewPacketCount(t, st, run.ID, 0)
}

func newReviewTestStore(t *testing.T) *store.Store {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	repoRoot := filepath.Join(t.TempDir(), "repo")
	st, err := store.InitProject(repoRoot, "TST")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	return st
}

func prepareReviewRun(t *testing.T, st *store.Store) (*core.Issue, *core.RunAttempt) {
	t.Helper()
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "changed.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatalf("write workspace file: %v", err)
	}
	issue, err := st.CreateIssue(store.CreateIssueInput{
		Title:              "Review packet write failure",
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
	wsID, err := st.CreateOrUpdateWorkspace(issue.ID, workspace, "review-test", "auto", "main", "base-sha")
	if err != nil {
		t.Fatalf("CreateOrUpdateWorkspace: %v", err)
	}
	if err := st.SetRunWorkspace(run.ID, wsID, "review-test", "auto", "main", "base-sha"); err != nil {
		t.Fatalf("SetRunWorkspace: %v", err)
	}
	if _, err := st.InsertHandoff(issue.ID, run.ID, "payload-hash", map[string]any{
		"summary":       "ready for review",
		"changed_files": []string{"changed.txt"},
		"target_state":  "Human Review",
	}); err != nil {
		t.Fatalf("InsertHandoff: %v", err)
	}
	issue, err = st.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	return issue, run
}

func assertReviewPacketCount(t *testing.T, st *store.Store, runID string, want int) {
	t.Helper()
	row, err := st.Project.QueryOne(`SELECT COUNT(*) AS c FROM review_packets WHERE run_id=? AND status='generated'`, runID)
	if err != nil {
		t.Fatalf("count review packets: %v", err)
	}
	if got := row["c"].Int(); got != want {
		t.Fatalf("generated review packet count = %d, want %d", got, want)
	}
}
