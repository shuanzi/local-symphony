package toolgateway

import (
	"os"
	"path/filepath"
	"testing"

	"local-symphony/internal/core"
	"local-symphony/internal/store"
)

func TestArtifactAttachReturnsFailureAndDoesNotInsertArtifactWhenWriteFails(t *testing.T) {
	st := newGatewayTestStore(t)
	issue, run, workspace := prepareGatewayRun(t, st)
	sourcePath := filepath.Join(workspace, "artifact.txt")
	if err := os.WriteFile(sourcePath, []byte("artifact\n"), 0o644); err != nil {
		t.Fatalf("write source artifact: %v", err)
	}
	conflictPath := filepath.Join(st.RepoRoot, ".symphony", "artifacts", issue.Identifier, run.ID, "agent", "artifact.txt")
	if err := os.MkdirAll(conflictPath, 0o755); err != nil {
		t.Fatalf("create conflicting artifact destination: %v", err)
	}
	token, err := NewTokenForRun(st, run, workspace)
	if err != nil {
		t.Fatalf("NewTokenForRun: %v", err)
	}

	resp := (Gateway{Store: st}).Call(token, workspace, Request{
		Tool: "artifact.attach",
		Input: map[string]any{
			"path": "artifact.txt",
			"kind": "agent_file",
		},
	})

	if resp.Success {
		t.Fatalf("artifact.attach success = true, want false")
	}
	if resp.Error == nil || resp.Error.Code != string(core.ErrToolGatewayFailed) {
		t.Fatalf("artifact.attach error = %#v, want %s", resp.Error, core.ErrToolGatewayFailed)
	}
	assertArtifactCount(t, st, run.ID, 0)
}

func newGatewayTestStore(t *testing.T) *store.Store {
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

func prepareGatewayRun(t *testing.T, st *store.Store) (*core.Issue, *core.RunAttempt, string) {
	t.Helper()
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	issue, err := st.CreateIssue(store.CreateIssueInput{
		Title:              "Artifact attach write failure",
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
	wsID, err := st.CreateOrUpdateWorkspace(issue.ID, workspace, "gateway-test", "auto", "main", "base-sha")
	if err != nil {
		t.Fatalf("CreateOrUpdateWorkspace: %v", err)
	}
	if err := st.SetRunWorkspace(run.ID, wsID, "gateway-test", "auto", "main", "base-sha"); err != nil {
		t.Fatalf("SetRunWorkspace: %v", err)
	}
	if err := st.UpdateRunStatus(run.ID, core.RunRunning, nil); err != nil {
		t.Fatalf("UpdateRunStatus: %v", err)
	}
	run, err = st.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	issue, err = st.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	return issue, run, workspace
}

func assertArtifactCount(t *testing.T, st *store.Store, runID string, want int) {
	t.Helper()
	row, err := st.Project.QueryOne(`SELECT COUNT(*) AS c FROM artifacts WHERE run_id=?`, runID)
	if err != nil {
		t.Fatalf("count artifacts: %v", err)
	}
	if got := row["c"].Int(); got != want {
		t.Fatalf("artifact count = %d, want %d", got, want)
	}
}
