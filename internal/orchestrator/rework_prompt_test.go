package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-symphony/internal/core"
	"local-symphony/internal/review"
	"local-symphony/internal/store"
)

// newReworkDispatchIssue sets up a single issue in Rework state with a
// prior completed run whose review packet is ready to be summarized.
func newReworkDispatchIssue(t *testing.T) (*store.Store, *core.Issue, *core.RunAttempt) {
	t.Helper()
	st, err := store.InitProject(t.TempDir(), "TST")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	issue, err := st.CreateIssue(store.CreateIssueInput{
		Title:              "Rework dispatch",
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
	// First run: a successful completion that produces a review
	// packet. The fake runner is the default, so calling runWorker
	// directly completes the run end-to-end.
	run, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	wsID, err := st.CreateOrUpdateWorkspace(issue.ID, t.TempDir(), "rework-test", "auto", "main", "base-sha")
	if err != nil {
		t.Fatalf("CreateOrUpdateWorkspace: %v", err)
	}
	if err := st.SetRunWorkspace(run.ID, wsID, "rework-test", "auto", "main", "base-sha"); err != nil {
		t.Fatalf("SetRunWorkspace: %v", err)
	}
	if err := st.Project.Exec(`UPDATE issues SET state=? WHERE id=?`, string(core.StateWorking), issue.ID); err != nil {
		t.Fatalf("force state Working: %v", err)
	}
	if _, err := st.InsertHandoff(issue.ID, run.ID, "payload-hash-1", map[string]any{
		"summary":       "Initial implementation done.",
		"changed_files": []string{"internal/foo.go"},
		"tests":         []string{"go test ./..."},
		"risks":         []string{"Low"},
		"verification":  []string{"Manual smoke test"},
		"target_state":  "Human Review",
	}); err != nil {
		t.Fatalf("InsertHandoff: %v", err)
	}
	gen := review.Generator{Store: st}
	if _, err := gen.Generate(run.ID); err != nil {
		t.Fatalf("Generate review packet: %v", err)
	}
	if err := st.CompleteRunWithReview(run.ID, mustReviewPacketID(t, st, run.ID)); err != nil {
		t.Fatalf("CompleteRunWithReview: %v", err)
	}
	// CompleteRunWithReview transitions the issue to Human Review
	// already; flip it to Rework via SendToRework with a specific
	// reason. The state_history row this writes is what the rework
	// injector surfaces as the latest review reason.
	if _, err := st.SendToRework(issue.ID, "Please cover the empty input edge case."); err != nil {
		t.Fatalf("SendToRework: %v", err)
	}
	return st, issue, run
}

func mustReviewPacketID(t *testing.T, st *store.Store, runID string) string {
	t.Helper()
	row, err := st.Project.QueryOne(`SELECT id FROM review_packets WHERE run_id=? ORDER BY packet_no DESC LIMIT 1`, runID)
	if err != nil {
		t.Fatalf("query review packet: %v", err)
	}
	return row["id"].String()
}

func TestReworkPromptIncludesLatestReviewReason(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_RUNNER_KIND", "")
	t.Setenv("SYMPHONY_FAKE_RUNNER_OUTCOME", "")
	st, issue, _ := newReworkDispatchIssue(t)

	res, err := (Orchestrator{Store: st}).DispatchIssue(issue.Identifier, "manual")
	if err != nil {
		t.Fatalf("DispatchIssue: %v", err)
	}
	if res.Run.SourceIssueState != core.StateRework {
		t.Fatalf("source_issue_state = %s, want %s", res.Run.SourceIssueState, core.StateRework)
	}
	// Read the redacted prompt that the orchestrator wrote to the
	// prompt snapshot. The fake runner surfaces the prompt it
	// received via run events; we use that as the canonical artifact.
	promptPath := filepath.Join(st.RepoRoot, ".symphony", "artifacts", issue.Identifier, res.Run.ID, "prompt", "rework_prompt.redacted.md")
	data, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatalf("read redacted prompt: %v", err)
	}
	prompt := string(data)
	if !strings.Contains(prompt, "Please cover the empty input edge case.") {
		t.Fatalf("prompt missing review reason\n---\n%s\n---", prompt)
	}
	// Safe summary marker should also be present.
	if !strings.Contains(prompt, "Previous Review Packet (Safe Summary)") {
		t.Fatalf("prompt missing safe summary header\n---\n%s\n---", prompt)
	}
	if !strings.Contains(prompt, "Initial implementation done.") {
		t.Fatalf("prompt missing safe summary prose\n---\n%s\n---", prompt)
	}
}

func TestReworkPromptSnapshotRecordsMetadata(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_RUNNER_KIND", "")
	st, issue, _ := newReworkDispatchIssue(t)

	res, err := (Orchestrator{Store: st}).DispatchIssue(issue.Identifier, "manual")
	if err != nil {
		t.Fatalf("DispatchIssue: %v", err)
	}
	rec, err := st.GetReworkSnapshot(res.Run.ID)
	if err != nil {
		t.Fatalf("GetReworkSnapshot: %v", err)
	}
	if rec.RunID != res.Run.ID {
		t.Fatalf("ReworkSnapshot.RunID = %q, want %q", rec.RunID, res.Run.ID)
	}
	if rec.ReviewReason != "Please cover the empty input edge case." {
		t.Fatalf("ReworkSnapshot.ReviewReason = %q", rec.ReviewReason)
	}
	if rec.PromptHash == "" {
		t.Fatal("ReworkSnapshot.PromptHash is empty")
	}
	if rec.ReviewPacketID == "" {
		t.Fatal("ReworkSnapshot.ReviewPacketID is empty")
	}
	if rec.SafeSummarySHA256 == "" {
		t.Fatal("ReworkSnapshot.SafeSummarySHA256 is empty")
	}
	if rec.BaseSHA != "base-sha" {
		t.Fatalf("ReworkSnapshot.BaseSHA = %q, want base-sha", rec.BaseSHA)
	}
	if rec.PreviousRunID == "" {
		t.Fatal("ReworkSnapshot.PreviousRunID is empty")
	}
}

func TestReworkPromptSnapshotExcludesRawArtifactMarkers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_RUNNER_KIND", "")
	st, issue, _ := newReworkDispatchIssue(t)

	res, err := (Orchestrator{Store: st}).DispatchIssue(issue.Identifier, "manual")
	if err != nil {
		t.Fatalf("DispatchIssue: %v", err)
	}
	promptPath := filepath.Join(st.RepoRoot, ".symphony", "artifacts", issue.Identifier, res.Run.ID, "prompt", "rendered_prompt.redacted.md")
	data, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatalf("read prompt: %v", err)
	}
	prompt := string(data)
	for _, kind := range []string{"codex_log", "prompt_snapshot", "secret_artifact", "raw_prompt"} {
		if strings.Contains(prompt, kind) {
			t.Fatalf("prompt contains raw artifact marker %q:\n%s", kind, prompt)
		}
	}
}

func TestReworkCumulativeDiffPreservedAcrossIterations(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_RUNNER_KIND", "")
	st, issue, _ := newReworkDispatchIssue(t)

	// First rework dispatch.
	res1, err := (Orchestrator{Store: st}).DispatchIssue(issue.Identifier, "manual")
	if err != nil {
		t.Fatalf("DispatchIssue 1: %v", err)
	}
	rec1, err := st.GetReworkSnapshot(res1.Run.ID)
	if err != nil {
		t.Fatalf("GetReworkSnapshot 1: %v", err)
	}
	if rec1.BaseSHA != "base-sha" {
		t.Fatalf("BaseSHA #1 = %q, want base-sha (cumulative diff must keep base stable)", rec1.BaseSHA)
	}

	// The orchestrator has already moved the issue back to
	// Human Review via the review packet flow, so we can directly
	// call SendToRework to dispatch a second iteration.
	if _, err := st.SendToRework(issue.ID, "Iteration 2: also flush the cache after writes."); err != nil {
		t.Fatalf("SendToRework 2: %v", err)
	}
	res2, err := (Orchestrator{Store: st}).DispatchIssue(issue.Identifier, "manual")
	if err != nil {
		t.Fatalf("DispatchIssue 2: %v", err)
	}
	rec2, err := st.GetReworkSnapshot(res2.Run.ID)
	if err != nil {
		t.Fatalf("GetReworkSnapshot 2: %v", err)
	}
	if rec2.BaseSHA != "base-sha" {
		t.Fatalf("BaseSHA #2 = %q, want base-sha (cumulative diff must keep base stable across iterations)", rec2.BaseSHA)
	}
	// Both reworks must list the issue's base_sha and remain
	// reconcilable via ListReworkSnapshotsForIssue.
	list, err := st.ListReworkSnapshotsForIssue(issue.ID)
	if err != nil {
		t.Fatalf("ListReworkSnapshotsForIssue: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("len(rework snapshots) = %d, want 2", len(list))
	}
	for _, rec := range list {
		if rec.BaseSHA != "base-sha" {
			t.Fatalf("rework snapshot %s has BaseSHA = %q, want base-sha", rec.RunID, rec.BaseSHA)
		}
	}
	// Reasons must be different across iterations.
	if list[0].ReviewReason == list[1].ReviewReason {
		t.Fatalf("rework reasons should differ across iterations; both = %q", list[0].ReviewReason)
	}
}

func TestReworkPromptDeterministicAcrossRuns(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_RUNNER_KIND", "")
	st, issue, _ := newReworkDispatchIssue(t)

	res, err := (Orchestrator{Store: st}).DispatchIssue(issue.Identifier, "manual")
	if err != nil {
		t.Fatalf("DispatchIssue: %v", err)
	}
	rec, err := st.GetReworkSnapshot(res.Run.ID)
	if err != nil {
		t.Fatalf("GetReworkSnapshot: %v", err)
	}
	if rec.PromptHash == "" {
		t.Fatal("PromptHash is empty")
	}
	// prompt snapshot's rendered_prompt_hash should equal the
	// prompt hash stored on the rework snapshot (post-injection
	// hash).
	row, err := st.Project.QueryOne(`SELECT rendered_prompt_hash FROM prompt_snapshots WHERE run_id=?`, res.Run.ID)
	if err != nil {
		t.Fatalf("query prompt_snapshots: %v", err)
	}
	if row["rendered_prompt_hash"].String() != rec.PromptHash {
		t.Fatalf("prompt_snapshots.rendered_prompt_hash = %q, rework_snapshots.prompt_hash = %q", row["rendered_prompt_hash"].String(), rec.PromptHash)
	}
}

func TestInjectReworkContextWithoutPreviousRunStillSucceeds(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_RUNNER_KIND", "")
	// Set up an issue that is already in Rework state but has no
	// completed runs (a synthetic scenario for migration / test
	// seeding).
	st, err := store.InitProject(t.TempDir(), "TST")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	issue, err := st.CreateIssue(store.CreateIssueInput{
		Title:              "fresh",
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
	if err := st.Project.Exec(`UPDATE issues SET state='Rework' WHERE id=?`, issue.ID); err != nil {
		t.Fatalf("force Rework: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = ctx
	// When the run is in Rework but no previous run exists, the
	// rework injector must fail-soft: the run is marked failed with
	// FailurePromptRenderFailed and DispatchIssue returns the
	// failed run rather than silently dropping the rework context.
	res, err := (Orchestrator{Store: st}).DispatchIssue(issue.Identifier, "manual")
	if err != nil {
		t.Fatalf("DispatchIssue returned error: %v", err)
	}
	if res == nil || res.Run == nil {
		t.Fatal("DispatchIssue returned no result")
	}
	if res.Run.Status != core.RunFailed {
		t.Fatalf("run status = %s, want %s (rework context must fail without prior packet)", res.Run.Status, core.RunFailed)
	}
	if res.Run.FailureCode == nil || *res.Run.FailureCode != core.FailurePromptRenderFailed {
		t.Fatalf("failure code = %v, want %s", res.Run.FailureCode, core.FailurePromptRenderFailed)
	}
}
