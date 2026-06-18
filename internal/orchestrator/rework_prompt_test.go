package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
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

// newReworkDispatchIssueWithGitWorkspace mirrors
// newReworkDispatchIssue but builds a real git workspace at the
// workspace path (so computeCumulativeDiffSHA can resolve HEAD and
// git diff content). Used by the F1 / F2 tests.
func newReworkDispatchIssueWithGitWorkspace(t *testing.T) (*store.Store, *core.Issue, *core.RunAttempt) {
	t.Helper()
	st, err := store.InitProject(t.TempDir(), "TST")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	issue, err := st.CreateIssue(store.CreateIssueInput{
		Title:              "Rework dispatch with git",
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
	// Real git workspace.
	workspace := t.TempDir()
	gitInit(t, workspace)
	if err := os.WriteFile(filepath.Join(workspace, "app.txt"), []byte("first\n"), 0o644); err != nil {
		t.Fatalf("write app.txt: %v", err)
	}
	gitCommit(t, workspace, "initial")
	wsID, err := st.CreateOrUpdateWorkspace(issue.ID, workspace, "rework-git", "auto", "main", "base-sha")
	if err != nil {
		t.Fatalf("CreateOrUpdateWorkspace: %v", err)
	}
	if err := st.SetRunWorkspace(run.ID, wsID, "rework-git", "auto", "main", "base-sha"); err != nil {
		t.Fatalf("SetRunWorkspace: %v", err)
	}
	if err := st.Project.Exec(`UPDATE issues SET state=? WHERE id=?`, string(core.StateWorking), issue.ID); err != nil {
		t.Fatalf("force state Working: %v", err)
	}
	if _, err := st.InsertHandoff(issue.ID, run.ID, "payload-hash-1", map[string]any{
		"summary":       "Initial implementation done.",
		"changed_files": []string{"app.txt"},
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
	if _, err := st.SendToRework(issue.ID, "Please cover the empty input edge case."); err != nil {
		t.Fatalf("SendToRework: %v", err)
	}
	issue, err = st.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	return st, issue, run
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{{"init"}, {"config", "user.email", "review@example.test"}, {"config", "user.name", "Review Test"}} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}

func gitCommit(t *testing.T, dir, msg string) {
	t.Helper()
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", msg}} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}

// isHex reports whether s is a non-empty lowercase hex string.
func isHex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
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
	// PR #27 / D4 F2: the orchestrator no longer writes the raw
	// rendered prompt to disk. The redacted prompt file is
	// metadata-only. We verify the rework injection took place by
	// reading the rework_snapshots row (which carries the
	// post-injection prompt hash) and the safe_summary_sha256.
	rec, err := st.GetReworkSnapshot(res.Run.ID)
	if err != nil {
		t.Fatalf("GetReworkSnapshot: %v", err)
	}
	if rec.ReviewReason != "Please cover the empty input edge case." {
		t.Fatalf("rework snapshot review reason = %q, want %q", rec.ReviewReason, "Please cover the empty input edge case.")
	}
	if rec.SafeSummarySHA256 == "" {
		t.Fatal("rework snapshot SafeSummarySHA256 is empty")
	}
	if rec.PromptHash == "" {
		t.Fatal("rework snapshot PromptHash is empty")
	}
	// And the redacted prompt file on disk must be metadata-only.
	promptPath := filepath.Join(st.RepoRoot, ".symphony", "artifacts", issue.Identifier, res.Run.ID, "prompt", "rework_prompt.redacted.md")
	data, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatalf("read redacted prompt: %v", err)
	}
	prompt := string(data)
	if !strings.Contains(prompt, "metadata-only") {
		t.Fatalf("redacted prompt is not metadata-only\n---\n%s\n---", prompt)
	}
	// The on-disk file MUST NOT contain any leaked raw prompt
	// prose (issue description, review reason prose, safe summary
	// markdown).
	for _, marker := range []string{
		"Please cover the empty input edge case.",
		"Initial implementation done.",
		"# Previous Review Packet (Safe Summary)",
	} {
		if strings.Contains(prompt, marker) {
			t.Fatalf("redacted prompt leaked raw prompt marker %q\n---\n%s\n---", marker, prompt)
		}
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
	// PR #27 / D4 F2: rework dispatches write a metadata-only
	// `rework_prompt.redacted.md` artifact; the test must inspect
	// that file (not `rendered_prompt.redacted.md`, which is only
	// written on non-rework dispatches and later overwritten by the
	// review packet generator).
	promptPath := filepath.Join(st.RepoRoot, ".symphony", "artifacts", issue.Identifier, res.Run.ID, "prompt", "rework_prompt.redacted.md")
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

// TestCumulativeDiffHashCoversUncommittedChanges verifies that two
// rework snapshots taken against the same base + HEAD SHA but with
// different uncommitted worktree contents produce different
// cumulative_diff_sha values. The previous hash-only-base+HEAD
// scheme collapsed dirty worktree states to the same value, hiding
// agent-applied-but-uncommitted work from prompt/diagnostic
// correlation.
func TestCumulativeDiffHashCoversUncommittedChanges(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_RUNNER_KIND", "")
	st, issue, prev := newReworkDispatchIssueWithGitWorkspace(t)

	// Snapshot #1: clean worktree (prev already wrote a commit).
	hashClean := (Orchestrator{Store: st}).computeCumulativeDiffSHA(issue, prev, "base-sha", nil)
	if hashClean == "" {
		t.Fatal("cumulative diff SHA is empty for clean worktree")
	}

	// Snapshot #2: agent leaves uncommitted work — untracked file plus
	// modified tracked file. The hash must differ.
	ws := issue.Workspace.Path
	if err := os.WriteFile(filepath.Join(ws, "uncommitted.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write uncommitted: %v", err)
	}
	hashDirty := (Orchestrator{Store: st}).computeCumulativeDiffSHA(issue, prev, "base-sha", nil)
	if hashDirty == "" {
		t.Fatal("cumulative diff SHA is empty for dirty worktree")
	}
	if hashDirty == "" {
		t.Fatal("cumulative diff SHA is empty for dirty worktree")
	}
	if hashDirty == hashClean {
		t.Fatalf("cumulative_diff_sha collapsed dirty worktree to clean hash (%q == %q)", hashDirty, hashClean)
	}
}

func TestCumulativeDiffHashCoversUntrackedFileContentChanges(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_RUNNER_KIND", "")
	st, issue, prev := newReworkDispatchIssueWithGitWorkspace(t)

	ws := issue.Workspace.Path
	if err := os.WriteFile(filepath.Join(ws, "notes.txt"), []byte("first\n"), 0o644); err != nil {
		t.Fatalf("write first untracked content: %v", err)
	}
	hashFirst := (Orchestrator{Store: st}).computeCumulativeDiffSHA(issue, prev, "base-sha", nil)
	if hashFirst == "" {
		t.Fatal("cumulative diff SHA is empty for first untracked content")
	}
	if err := os.WriteFile(filepath.Join(ws, "notes.txt"), []byte("second\n"), 0o644); err != nil {
		t.Fatalf("write second untracked content: %v", err)
	}
	hashSecond := (Orchestrator{Store: st}).computeCumulativeDiffSHA(issue, prev, "base-sha", nil)
	if hashSecond == "" {
		t.Fatal("cumulative diff SHA is empty for second untracked content")
	}
	if hashSecond == hashFirst {
		t.Fatalf("cumulative_diff_sha ignored untracked file bytes (%q == %q)", hashSecond, hashFirst)
	}
}

func TestCumulativeUntrackedDigestSkipsProtectedUntrackedFiles(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, issue, _ := newReworkDispatchIssueWithGitWorkspace(t)
	ws := issue.Workspace.Path
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("SECRET=env\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, "id_rsa"), []byte("SECRET=rsa\n"), 0o600); err != nil {
		t.Fatalf("write id_rsa: %v", err)
	}

	if got := cumulativeUntrackedDigest(ws, nil); got != "" {
		t.Fatalf("cumulative untracked digest hashed protected untracked files: %q", got)
	}
}

func TestFilteredTrackedDiffDoesNotReadUnfilteredPatchForProtectedTrackedChange(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, issue, _ := newReworkDispatchIssueWithGitWorkspace(t)
	ws := issue.Workspace.Path
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("SECRET=base\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	gitCommit(t, ws, "add env")
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("SECRET=changed\n"), 0o600); err != nil {
		t.Fatalf("modify .env: %v", err)
	}

	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("LookPath git: %v", err)
	}
	wrapperDir := t.TempDir()
	logPath := filepath.Join(wrapperDir, "forbidden.log")
	wrapper := filepath.Join(wrapperDir, "git")
	script := "#!/bin/sh\n" +
		"argc=$#\n" +
		"cmd1=$1\n" +
		"cmd2=$2\n" +
		"if [ \"$1\" = \"-C\" ]; then\n" +
		"  argc=$(($# - 2))\n" +
		"  cmd1=$3\n" +
		"  cmd2=$4\n" +
		"fi\n" +
		"if [ \"$cmd1\" = \"diff\" ] && [ \"$cmd2\" = \"HEAD\" ] && [ \"$argc\" -eq 2 ]; then\n" +
		"  printf 'forbidden unfiltered diff\\n' >> " + shellQuote(logPath) + "\n" +
		"  printf 'SECRET=changed\\n'\n" +
		"  exit 0\n" +
		"fi\n" +
		"exec " + shellQuote(realGit) + " \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatalf("write git wrapper: %v", err)
	}
	t.Setenv("PATH", wrapperDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	_ = filteredTrackedDiff(ws, nil)

	if data, err := os.ReadFile(logPath); err == nil && len(data) > 0 {
		t.Fatalf("filteredTrackedDiff ran unfiltered git diff HEAD before filtering:\n%s", data)
	}
}

func TestFilteredTrackedDiffSkipsProtectedRenameSource(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, issue, _ := newReworkDispatchIssueWithGitWorkspace(t)
	ws := issue.Workspace.Path
	// Use a multi-line .env so git's rename detection reports an R
	// record (source .env is protected -> the whole record is skipped).
	// A 1-line file 100%-rewritten falls below the similarity
	// threshold and is reported as D .env + A public.txt; that case is
	// covered by TestFilteredTrackedDiffKeepsUnrelatedAddedFileWhenProtectedDeleted,
	// where public.txt carries genuinely new (non-protected-bytes)
	// content and is correctly kept.
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("SECRET=tracked\nEXTRA=line\nMORE=data\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	gitCommit(t, ws, "add env")
	if out, err := exec.Command("git", "-C", ws, "mv", ".env", "public.txt").CombinedOutput(); err != nil {
		t.Fatalf("git mv .env public.txt: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(ws, "public.txt"), []byte("SECRET=changed\nEXTRA=line\nMORE=data\n"), 0o644); err != nil {
		t.Fatalf("modify public.txt: %v", err)
	}

	diff := string(filteredTrackedDiff(ws, nil))
	if strings.Contains(diff, "SECRET=tracked") || strings.Contains(diff, "diff --git a/.env b/public.txt") {
		t.Fatalf("filtered tracked diff leaked protected rename source:\n%s", diff)
	}
}

func TestFilteredTrackedDiffSkipsProtectedCopySource(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, issue, _ := newReworkDispatchIssueWithGitWorkspace(t)
	ws := issue.Workspace.Path
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("SECRET=tracked\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	gitCommit(t, ws, "add env")
	data, err := os.ReadFile(filepath.Join(ws, ".env"))
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, "public.txt"), data, 0o644); err != nil {
		t.Fatalf("copy .env to public.txt: %v", err)
	}
	if out, err := exec.Command("git", "-C", ws, "add", "public.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add public.txt: %v\n%s", err, out)
	}

	diff := string(filteredTrackedDiff(ws, nil))
	if strings.Contains(diff, "SECRET=tracked") || strings.Contains(diff, "diff --git a/.env b/public.txt") || strings.Contains(diff, "diff --git a/public.txt b/public.txt") {
		t.Fatalf("filtered tracked diff leaked protected copy source:\n%s", diff)
	}
}

func TestFilteredTrackedDiffSkipsProtectedDestination(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, issue, _ := newReworkDispatchIssueWithGitWorkspace(t)
	ws := issue.Workspace.Path
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("SECRET=new\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	if out, err := exec.Command("git", "-C", ws, "add", ".env").CombinedOutput(); err != nil {
		t.Fatalf("git add .env: %v\n%s", err, out)
	}

	diff := string(filteredTrackedDiff(ws, nil))
	if strings.Contains(diff, "SECRET=new") || strings.Contains(diff, "diff --git a/.env b/.env") {
		t.Fatalf("filtered tracked diff leaked protected destination:\n%s", diff)
	}
}

// TestCumulativeUntrackedDigestSkipsCopyOfDeletedProtectedContent
// verifies that an untracked file which is a verbatim copy of a deleted
// protected file's bytes has its content suppressed (a sentinel is
// written) so the protected bytes are not hashed. D4 / R16 round 2: the
// deletion is UNSTAGED (filesystem `rm .env`), so the index still holds
// the pre-deletion content (`git show :.env` succeeds) and the
// content-hash set is KNOWN. public.txt == .env HEAD bytes → its hash
// matches the protected set → suppressed. The digest is NON-EMPTY (a
// sentinel reflects the file's existence) and a separate run where
// public.txt carries different safe content yields a DIFFERENT digest.
func TestCumulativeUntrackedDigestSkipsCopyOfDeletedProtectedContent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, issue, _ := newReworkDispatchIssueWithGitWorkspace(t)
	ws := issue.Workspace.Path
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("SECRET=tracked\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	gitCommit(t, ws, "add env")
	secret, err := os.ReadFile(filepath.Join(ws, ".env"))
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, "public.txt"), secret, 0o644); err != nil {
		t.Fatalf("copy .env to public.txt: %v", err)
	}
	// Deleting a protected tracked file triggers FAIL-CLOSED: a protected
	// file's pre-deletion worktree content (the bytes that could have been
	// copied into public.txt) is unrecoverable when unstaged modifications
	// existed and undetectable after deletion, so we suppress ALL
	// non-path-protected untracked content via a sentinel (no content
	// read, no content hashed). The unstaged `rm` here is one instance of
	// the protected-delete case; a staged `git rm` is exercised by
	// TestCumulativeUntrackedDigestFailsClosedOnProtectedDelete.
	if err := os.Remove(filepath.Join(ws, ".env")); err != nil {
		t.Fatalf("rm .env: %v", err)
	}

	// The fail-closed digest must be non-empty (a sentinel reflects the
	// file's existence at path level) and must NOT leak protected bytes.
	// It is a 64-char hex hash of (path + mode + sentinel), never the raw
	// protected content.
	digestCopy := cumulativeUntrackedDigest(ws, nil)
	if digestCopy == "" {
		t.Fatal("cumulative untracked digest is empty for verbatim protected-content copy; want non-empty sentinel digest")
	}
	if len(digestCopy) != 64 || !isHex(digestCopy) {
		t.Fatalf("cumulative untracked digest is not a 64-char hex hash: %q", digestCopy)
	}
	if strings.Contains(digestCopy, "SECRET=tracked") {
		t.Fatalf("cumulative untracked digest leaked protected bytes: %q", digestCopy)
	}

	// Now replace public.txt with different safe content and recompute.
	// Under fail-closed the content is suppressed via the same fixed
	// sentinel regardless of bytes, so content changes must NOT move the
	// digest (only the path set / mode would). public.txt is unchanged
	// as a path, so the two digests must be EQUAL — proving content is
	// suppressed and no protected bytes influence the digest.
	if err := os.WriteFile(filepath.Join(ws, "public.txt"), []byte("plain safe note\n"), 0o644); err != nil {
		t.Fatalf("write different public.txt: %v", err)
	}
	digestSafe := cumulativeUntrackedDigest(ws, nil)
	if digestSafe == "" {
		t.Fatal("cumulative untracked digest is empty for safe public.txt content in fail-closed mode")
	}
	if digestSafe != digestCopy {
		t.Fatalf("fail-closed digest moved when public.txt content changed (%q != %q); want identical sentinel-derived digest (content suppressed)", digestSafe, digestCopy)
	}
}

// TestCumulativeUntrackedDigestFailsClosedOnProtectedDelete codifies the
// FAIL-CLOSED behavior for an UNSTAGED protected deletion (the previously
// named "keeps unrelated" test's premise is no longer valid under the
// security-first decision). When .env is removed with a filesystem `rm`
// (protected delete), the pre-deletion worktree content of ANY untracked
// file is suspect — it could be a copy of modified-then-deleted protected
// bytes, and that is undetectable after deletion. So ALL non-path-
// protected untracked content is suppressed via a sentinel: content
// changes do NOT move the digest (both runs produce the same
// sentinel-derived hash), and protected bytes never appear. This is the
// secrecy-preserving trade-off: we sacrifice content-level correlation in
// the protected-delete case rather than risk leaking modified-then-deleted
// protected bytes. The staged-delete variant is exercised by
// TestCumulativeUntrackedDigestFailsClosedOnStagedProtectedDelete.
func TestCumulativeUntrackedDigestFailsClosedOnProtectedDelete(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, issue, _ := newReworkDispatchIssueWithGitWorkspace(t)
	ws := issue.Workspace.Path
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("SECRET=tracked\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	gitCommit(t, ws, "add env")
	// UNSTAGED deletion (protected delete) → fail closed.
	if err := os.Remove(filepath.Join(ws, ".env")); err != nil {
		t.Fatalf("rm .env: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, "notes.txt"), []byte("hello-A\n"), 0o644); err != nil {
		t.Fatalf("write notes.txt A: %v", err)
	}
	digestA := cumulativeUntrackedDigest(ws, nil)
	if digestA == "" {
		t.Fatal("cumulative untracked digest is empty for notes.txt=hello-A in fail-closed mode; want non-empty sentinel digest")
	}
	if len(digestA) != 64 || !isHex(digestA) {
		t.Fatalf("cumulative untracked digest for notes.txt=hello-A is not a 64-char hex hash: %q", digestA)
	}
	if strings.Contains(digestA, "SECRET=tracked") {
		t.Fatalf("cumulative untracked digest leaked protected bytes in fail-closed mode: %q", digestA)
	}
	if err := os.WriteFile(filepath.Join(ws, "notes.txt"), []byte("hello-B\n"), 0o644); err != nil {
		t.Fatalf("write notes.txt B: %v", err)
	}
	digestB := cumulativeUntrackedDigest(ws, nil)
	if digestB == "" {
		t.Fatal("cumulative untracked digest is empty for notes.txt=hello-B in fail-closed mode; want non-empty sentinel digest")
	}
	if digestA != digestB {
		t.Fatalf("fail-closed digest moved with content changes (%q != %q); want identical sentinel-derived digest (content suppressed)", digestA, digestB)
	}
}

// TestCumulativeUntrackedDigestHashesContentWhenNoProtectedDelete proves
// the COMMON case keeps full content-level correlation: with NO protected
// file deleted, an untracked file's content is hashed into the digest, so
// changing its content moves the digest. This is the counterpart to the
// fail-closed tests above and guards against regressing the common-case
// behavior when tightening the protected-delete path.
func TestCumulativeUntrackedDigestHashesContentWhenNoProtectedDelete(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, issue, _ := newReworkDispatchIssueWithGitWorkspace(t)
	ws := issue.Workspace.Path
	// NO protected file is deleted (no .env at all). Content hashing is
	// active → the digest reflects untracked file bytes.
	if err := os.WriteFile(filepath.Join(ws, "notes.txt"), []byte("hello-A\n"), 0o644); err != nil {
		t.Fatalf("write notes.txt A: %v", err)
	}
	digestA := cumulativeUntrackedDigest(ws, nil)
	if digestA == "" {
		t.Fatal("cumulative untracked digest is empty for notes.txt=hello-A (no protected delete)")
	}
	if len(digestA) != 64 || !isHex(digestA) {
		t.Fatalf("cumulative untracked digest for notes.txt=hello-A is not a 64-char hex hash: %q", digestA)
	}
	if strings.Contains(digestA, "hello-A") {
		t.Fatalf("cumulative untracked digest leaked raw content: %q", digestA)
	}
	if err := os.WriteFile(filepath.Join(ws, "notes.txt"), []byte("hello-B\n"), 0o644); err != nil {
		t.Fatalf("write notes.txt B: %v", err)
	}
	digestB := cumulativeUntrackedDigest(ws, nil)
	if digestB == "" {
		t.Fatal("cumulative untracked digest is empty for notes.txt=hello-B (no protected delete)")
	}
	if digestA == digestB {
		t.Fatalf("cumulative untracked digest collapsed notes.txt content changes in the common case (%q == %q); want content-level correlation", digestA, digestB)
	}
}

// TestFilteredTrackedDiffKeepsAddedFileWhenNoProtectedDelete proves the
// COMMON case keeps added tracked (A) records in the diff: with NO
// protected file deleted, an added generated.go is kept and its content
// changes move the diff. This is the counterpart to the fail-closed
// A-skip tests and guards against regressing common-case behavior.
func TestFilteredTrackedDiffKeepsAddedFileWhenNoProtectedDelete(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, issue, _ := newReworkDispatchIssueWithGitWorkspace(t)
	ws := issue.Workspace.Path
	if err := os.WriteFile(filepath.Join(ws, "app.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write app.txt: %v", err)
	}
	gitCommit(t, ws, "add app")
	// NO protected file is deleted → A records are kept (no content check).
	if err := os.WriteFile(filepath.Join(ws, "generated.go"), []byte("v1\n"), 0o644); err != nil {
		t.Fatalf("write generated.go v1: %v", err)
	}
	if out, err := exec.Command("git", "-C", ws, "add", "generated.go").CombinedOutput(); err != nil {
		t.Fatalf("git add generated.go: %v\n%s", err, out)
	}

	diffV1 := string(filteredTrackedDiff(ws, nil))
	if !strings.Contains(diffV1, "diff --git a/generated.go b/generated.go") {
		t.Fatalf("filtered tracked diff dropped added file generated.go (no protected delete):\n%s", diffV1)
	}
	if !strings.Contains(diffV1, "+v1") {
		t.Fatalf("filtered tracked diff missing generated.go v1 content:\n%s", diffV1)
	}

	// Change generated.go content and recompute; the diff must change.
	if err := os.WriteFile(filepath.Join(ws, "generated.go"), []byte("v2\n"), 0o644); err != nil {
		t.Fatalf("write generated.go v2: %v", err)
	}
	if out, err := exec.Command("git", "-C", ws, "add", "generated.go").CombinedOutput(); err != nil {
		t.Fatalf("git add generated.go v2: %v\n%s", err, out)
	}
	diffV2 := string(filteredTrackedDiff(ws, nil))
	if !strings.Contains(diffV2, "diff --git a/generated.go b/generated.go") {
		t.Fatalf("filtered tracked diff dropped added file generated.go (v2, no protected delete):\n%s", diffV2)
	}
	if diffV1 == diffV2 {
		t.Fatalf("filtered tracked diff did not change when generated.go content changed (v1==v2):\n%s", diffV1)
	}
}

// TestFilteredTrackedDiffSkipsAddedFileOnProtectedDelete codifies the
// FAIL-CLOSED behavior for an added tracked (A) file when a protected
// tracked file is deleted. Round 3 made ANY protected delete trigger
// fail-closed: the A file's pre-deletion worktree content could be a
// copy of modified-then-deleted protected bytes (undetectable after
// deletion), so we SKIP all A records — their diff never runs and
// protected bytes never enter cumulative_diff_sha. The staged-delete
// variant is exercised by
// TestFilteredTrackedDiffFailsClosedOnStagedProtectedDeleteWithAddedFile.
func TestFilteredTrackedDiffSkipsAddedFileOnProtectedDelete(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, issue, _ := newReworkDispatchIssueWithGitWorkspace(t)
	ws := issue.Workspace.Path
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("SECRET=tracked\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	gitCommit(t, ws, "add env")
	// UNSTAGED deletion (protected delete) → fail closed → A skipped.
	if err := os.Remove(filepath.Join(ws, ".env")); err != nil {
		t.Fatalf("rm .env: %v", err)
	}
	// generated.go is safe content (not a copy of .env), but the A
	// record is still skipped because a protected file was deleted.
	if err := os.WriteFile(filepath.Join(ws, "generated.go"), []byte("v1\n"), 0o644); err != nil {
		t.Fatalf("write generated.go v1: %v", err)
	}
	if out, err := exec.Command("git", "-C", ws, "add", "generated.go").CombinedOutput(); err != nil {
		t.Fatalf("git add generated.go: %v\n%s", err, out)
	}
	diff := string(filteredTrackedDiff(ws, nil))
	if strings.Contains(diff, "diff --git a/generated.go b/generated.go") {
		t.Fatalf("filtered tracked diff kept added generated.go in fail-closed (protected-delete) mode:\n%s", diff)
	}
	if strings.Contains(diff, "SECRET=tracked") {
		t.Fatalf("filtered tracked diff leaked protected bytes in fail-closed mode:\n%s", diff)
	}
}

// TestCumulativeUntrackedDigestFailsClosedOnStagedProtectedDelete
// codifies the FAIL-CLOSED behavior for a STAGED protected deletion
// (`git rm`). Round 3 generalized fail-closed to ANY protected delete
// (staged OR unstaged) because the pre-deletion worktree content is
// unrecoverable when unstaged modifications existed and undetectable
// after deletion. The unstaged variant is exercised by
// TestCumulativeUntrackedDigestFailsClosedOnProtectedDelete. Here, with
// a staged delete, ALL non-path-protected untracked content is
// suppressed via a sentinel: changing notes.txt content does NOT move
// the digest (both runs produce the same sentinel-derived hash), and
// protected bytes never appear.
func TestCumulativeUntrackedDigestFailsClosedOnStagedProtectedDelete(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, issue, _ := newReworkDispatchIssueWithGitWorkspace(t)
	ws := issue.Workspace.Path
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("SECRET=tracked\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	gitCommit(t, ws, "add env")
	// STAGED deletion: `git rm` removes the index entry → protected
	// content set is unknown → fail closed.
	if out, err := exec.Command("git", "-C", ws, "rm", ".env").CombinedOutput(); err != nil {
		t.Fatalf("git rm .env: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(ws, "notes.txt"), []byte("hello-A\n"), 0o644); err != nil {
		t.Fatalf("write notes.txt A: %v", err)
	}
	digestA := cumulativeUntrackedDigest(ws, nil)
	if digestA == "" {
		t.Fatal("cumulative untracked digest is empty for notes.txt=hello-A in fail-closed mode; want non-empty sentinel digest")
	}
	if strings.Contains(digestA, "SECRET=tracked") {
		t.Fatalf("cumulative untracked digest leaked protected bytes in fail-closed mode: %q", digestA)
	}
	if err := os.WriteFile(filepath.Join(ws, "notes.txt"), []byte("hello-B\n"), 0o644); err != nil {
		t.Fatalf("write notes.txt B: %v", err)
	}
	digestB := cumulativeUntrackedDigest(ws, nil)
	if digestB == "" {
		t.Fatal("cumulative untracked digest is empty for notes.txt=hello-B in fail-closed mode; want non-empty sentinel digest")
	}
	if digestA != digestB {
		t.Fatalf("fail-closed digest moved with content changes (%q != %q); want identical sentinel-derived digest", digestA, digestB)
	}
}

// TestFilteredTrackedDiffSkipsAddedCopyOfDeletedProtectedContent
// verifies that an ADDED tracked file (status A) whose content is a
// verbatim copy of a deleted protected file's bytes is SKIPPED under
// fail-closed, so its (protected) bytes never enter cumulative_diff_sha.
// D4 / R16 round 3: a protected tracked delete triggers fail-closed for
// ALL A records (the deleted file's pre-deletion worktree content is
// unrecoverable when unstaged modifications existed and undetectable
// after deletion), so the verbatim-copy public.txt is skipped regardless
// of whether its bytes match the deleted secret's HEAD/index blob.
func TestFilteredTrackedDiffSkipsAddedCopyOfDeletedProtectedContent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, issue, _ := newReworkDispatchIssueWithGitWorkspace(t)
	ws := issue.Workspace.Path
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("SECRET=tracked\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	gitCommit(t, ws, "add env")
	secret, err := os.ReadFile(filepath.Join(ws, ".env"))
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	// UNSTAGED deletion (protected delete) → fail closed → A skipped.
	if err := os.Remove(filepath.Join(ws, ".env")); err != nil {
		t.Fatalf("rm .env: %v", err)
	}
	// public.txt is a verbatim copy of deleted .env's HEAD bytes. git
	// reports D .env + A public.txt (no rename detected for a full
	// rewrite), so the per-record path check alone would keep it;
	// fail-closed (any protected delete → skip all A) must skip it.
	if err := os.WriteFile(filepath.Join(ws, "public.txt"), secret, 0o644); err != nil {
		t.Fatalf("copy .env to public.txt: %v", err)
	}
	if out, err := exec.Command("git", "-C", ws, "add", "public.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add public.txt: %v\n%s", err, out)
	}
	diff := string(filteredTrackedDiff(ws, nil))
	if strings.Contains(diff, "SECRET=tracked") {
		t.Fatalf("filtered tracked diff leaked deleted protected bytes via added public.txt:\n%s", diff)
	}
	if strings.Contains(diff, "diff --git a/public.txt b/public.txt") {
		t.Fatalf("filtered tracked diff included added copy-of-protected public.txt:\n%s", diff)
	}

	// Variant: public.txt carries DIFFERENT safe content. Under
	// fail-closed the A record is STILL skipped (a protected file was
	// deleted), so neither the protected copy nor a safe replacement
	// leaks into cumulative_diff_sha.
	if err := os.WriteFile(filepath.Join(ws, "public.txt"), []byte("plain safe note\n"), 0o644); err != nil {
		t.Fatalf("write different public.txt: %v", err)
	}
	if out, err := exec.Command("git", "-C", ws, "add", "public.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add public.txt (safe): %v\n%s", err, out)
	}
	diffSafe := string(filteredTrackedDiff(ws, nil))
	if strings.Contains(diffSafe, "diff --git a/public.txt b/public.txt") {
		t.Fatalf("filtered tracked diff kept safe added public.txt in fail-closed (protected-delete) mode:\n%s", diffSafe)
	}
	if strings.Contains(diffSafe, "SECRET=tracked") {
		t.Fatalf("filtered tracked diff leaked protected bytes (safe variant):\n%s", diffSafe)
	}
}

// TestFilteredTrackedDiffFailsClosedOnStagedProtectedDeleteWithAddedFile
// codifies the FAIL-CLOSED behavior for a STAGED protected deletion when
// an added tracked file exists. Round 3 generalized fail-closed to ANY
// protected delete; the staged case is the most unambiguous (index entry
// gone, modified bytes unrecoverable). The A record is SKIPPED even if
// its content is safe. The unstaged variant is exercised by
// TestFilteredTrackedDiffSkipsAddedFileOnProtectedDelete.
func TestFilteredTrackedDiffFailsClosedOnStagedProtectedDeleteWithAddedFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, issue, _ := newReworkDispatchIssueWithGitWorkspace(t)
	ws := issue.Workspace.Path
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("SECRET=tracked\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	gitCommit(t, ws, "add env")
	// STAGED deletion (protected delete) → fail closed → A skipped.
	if out, err := exec.Command("git", "-C", ws, "rm", ".env").CombinedOutput(); err != nil {
		t.Fatalf("git rm .env: %v\n%s", err, out)
	}
	// public.txt is safe content (not a copy of .env), but the A record
	// is still skipped because a protected file was deleted.
	if err := os.WriteFile(filepath.Join(ws, "public.txt"), []byte("plain safe note\n"), 0o644); err != nil {
		t.Fatalf("write public.txt: %v", err)
	}
	if out, err := exec.Command("git", "-C", ws, "add", "public.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add public.txt: %v\n%s", err, out)
	}
	diff := string(filteredTrackedDiff(ws, nil))
	if strings.Contains(diff, "diff --git a/public.txt b/public.txt") {
		t.Fatalf("filtered tracked diff kept added public.txt in fail-closed (staged-delete) mode:\n%s", diff)
	}
	if strings.Contains(diff, "SECRET=tracked") {
		t.Fatalf("filtered tracked diff leaked protected bytes in fail-closed mode:\n%s", diff)
	}
}

// TestFilteredTrackedDiffUsesLiteralPathspecs verifies that a safe
// changed file literally named `*` is treated as a literal pathspec
// (not a glob) by filteredTrackedDiff, so a modified protected .env is
// not swept into cumulative_diff_sha. D4 / R16 finding #4.
func TestFilteredTrackedDiffUsesLiteralPathspecs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, issue, _ := newReworkDispatchIssueWithGitWorkspace(t)
	ws := issue.Workspace.Path
	// Commit a protected .env and a safe file literally named `*`.
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("SECRET=original\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, "*"), []byte("safe-base\n"), 0o644); err != nil {
		t.Fatalf("write *: %v", err)
	}
	if out, err := exec.Command("git", "-C", ws, "add", "-f", ".env", "*").CombinedOutput(); err != nil {
		t.Fatalf("git add .env *: %v\n%s", err, out)
	}
	gitCommit(t, ws, "add env and literal star file")
	// Modify both: protected .env and the literal `*` file.
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("SECRET=changed\n"), 0o600); err != nil {
		t.Fatalf("modify .env: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, "*"), []byte("safe-modified\n"), 0o644); err != nil {
		t.Fatalf("modify *: %v", err)
	}
	if out, err := exec.Command("git", "-C", ws, "add", ".env", "*").CombinedOutput(); err != nil {
		t.Fatalf("git add modified: %v\n%s", err, out)
	}

	// Verify the pathspec filter keeps the literal `*` file as a safe
	// pathspec and that the executed diff does not leak protected bytes.
	safe, err := filteredTrackedDiffPathspecs(ws, nil)
	if err != nil {
		t.Fatalf("filteredTrackedDiffPathspecs: %v", err)
	}
	foundStar := false
	for _, p := range safe {
		if p == "*" {
			foundStar = true
		}
	}
	if !foundStar {
		t.Fatalf("filteredTrackedDiffPathspecs did not keep literal '*' safe path; safe=%v", safe)
	}
	diff := string(filteredTrackedDiff(ws, nil))
	if strings.Contains(diff, "SECRET=changed") || strings.Contains(diff, "SECRET=original") {
		t.Fatalf("filtered tracked diff leaked protected .env bytes through literal '*' glob expansion:\n%s", diff)
	}
	if strings.Contains(diff, "diff --git a/.env b/.env") {
		t.Fatalf("filtered tracked diff included protected .env diff:\n%s", diff)
	}
}

func TestReworkPromptSnapshotRedactedPathExistsWhenBeforeRunFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_RUNNER_KIND", "")
	st, issue, _ := newReworkDispatchIssue(t)
	body := `---
hooks:
  before_run: "sh -c 'exit 17'"
---
Do the work.
`
	if err := os.WriteFile(filepath.Join(st.RepoRoot, "WORKFLOW.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	res, err := (Orchestrator{Store: st}).DispatchIssue(issue.Identifier, "manual")
	if err != nil {
		t.Fatalf("DispatchIssue: %v", err)
	}
	if res.Run.Status != core.RunFailed {
		t.Fatalf("run status = %s, want %s", res.Run.Status, core.RunFailed)
	}
	row, err := st.Project.QueryOne(`SELECT redacted_prompt_path FROM prompt_snapshots WHERE run_id=?`, res.Run.ID)
	if err != nil {
		t.Fatalf("query prompt snapshot: %v", err)
	}
	path := row["redacted_prompt_path"].String()
	if path == "" {
		t.Fatal("redacted_prompt_path is empty")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("redacted_prompt_path does not exist before review packet generation: %s: %v", path, err)
	}
}

// TestRedactedArtifactDoesNotContainRawPrompt verifies that the
// rework-prompt redacted artifact written under
// .symphony/artifacts/<identifier>/<run>/prompt/rework_prompt.redacted.md
// never contains the raw rendered prompt body. The previous
// implementation wrote "[redacted]\n" + the full rendered prompt;
// that violates the raw-prompt logging boundary (a rendered prompt
// can echo issue description + workflow prompt) and a rework run
// that fails before the review packet is generated leaves the file
// on disk for the next operator.
func TestRedactedArtifactDoesNotContainRawPrompt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_RUNNER_KIND", "")
	st, issue, _ := newReworkDispatchIssue(t)

	// The Rework dispatch writes the redacted artifact at the
	// prompt-rendering step (before the agent runs). Simulate the
	// fail-before-review-packet case by inspecting the artifact
	// written for the first rework run.
	res, err := (Orchestrator{Store: st}).DispatchIssue(issue.Identifier, "manual")
	if err != nil {
		t.Fatalf("DispatchIssue: %v", err)
	}
	promptPath := filepath.Join(st.RepoRoot, ".symphony", "artifacts", issue.Identifier, res.Run.ID, "prompt", "rework_prompt.redacted.md")
	data, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatalf("read redacted prompt artifact: %v", err)
	}
	body := string(data)
	// A redacted artifact MUST NOT contain a body of the rendered
	// prompt. We assert this by checking the file is small
	// (metadata-only) and contains redaction metadata.
	if len(body) > 2048 {
		t.Fatalf("redacted artifact is too large (%d bytes); must be metadata-only, not raw prompt\n---\n%s\n---", len(body), body)
	}
	if !strings.Contains(body, "redacted") && !strings.Contains(body, "metadata") {
		t.Fatalf("redacted artifact missing redaction marker\n---\n%s\n---", body)
	}
	// And MUST NOT contain any of the markers from the actual
	// rendered prompt (issue title, previous review reason prose,
	// or safe summary markdown).
	for _, marker := range []string{
		"Please cover the empty input edge case.", // review reason prose
		"Initial implementation done.",            // handoff summary
		"# Previous Review Packet (Safe Summary)", // safe summary header
	} {
		if strings.Contains(body, marker) {
			t.Fatalf("redacted artifact leaked raw prompt marker %q\n---\n%s\n---", marker, body)
		}
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

// TestFilteredTrackedDiffFailsClosedOnProtectedRename codifies the
// FAIL-CLOSED behavior for round-4 P1 leak #3: when a protected file is
// staged as a RENAME (`git mv .env renamed.txt`, an `R` record, NOT a
// `D` record) AND a separate staged ADDED file (`A public.txt`) contains
// the copied protected bytes (with enough filler to avoid git's
// rename/copy detection), round 3's `--diff-filter=D` probe did NOT see
// the `R` record as a deletion → hashesUnknown stayed false → the A
// record was kept → `git diff HEAD -- public.txt` emitted SECRET into
// cumulative_diff_sha. Round 4 makes a protected R/C SOURCE trigger
// fail-closed too, so the A record is skipped and neither the renamed
// .env nor the copied public.txt leaks.
func TestFilteredTrackedDiffFailsClosedOnProtectedRename(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, issue, _ := newReworkDispatchIssueWithGitWorkspace(t)
	ws := issue.Workspace.Path
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("SECRET=original\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	gitCommit(t, ws, "add env")
	// Staged RENAME of the protected file → R record (NOT a D record).
	if out, err := exec.Command("git", "-C", ws, "mv", ".env", "renamed.txt").CombinedOutput(); err != nil {
		t.Fatalf("git mv .env renamed.txt: %v\n%s", err, out)
	}
	// public.txt contains filler + the copied protected bytes, large
	// enough to fall below git's rename/copy similarity threshold so it
	// is reported as an A record (not a C record out of .env). Under
	// round 3 this A record was kept and leaked SECRET; round 4 skips it.
	var pb strings.Builder
	for i := 0; i < 20; i++ {
		pb.WriteString("filler line ")
		pb.WriteString("0123456789")
		pb.WriteByte('\n')
	}
	pb.WriteString("SECRET=original\n")
	if err := os.WriteFile(filepath.Join(ws, "public.txt"), []byte(pb.String()), 0o644); err != nil {
		t.Fatalf("write public.txt: %v", err)
	}
	if out, err := exec.Command("git", "-C", ws, "add", "public.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add public.txt: %v\n%s", err, out)
	}
	diff := string(filteredTrackedDiff(ws, nil))
	if strings.Contains(diff, "SECRET=original") {
		t.Fatalf("filtered tracked diff leaked protected bytes via added public.txt in protected-rename scenario:\n%s", diff)
	}
	if strings.Contains(diff, "diff --git a/public.txt b/public.txt") {
		t.Fatalf("filtered tracked diff kept added public.txt (A record) in protected-rename fail-closed mode:\n%s", diff)
	}
	if strings.Contains(diff, "diff --git a/.env b/renamed.txt") {
		t.Fatalf("filtered tracked diff leaked protected rename source itself:\n%s", diff)
	}
}

// TestFilteredTrackedDiffFailsClosedOnProtectedCopy mirrors the rename
// test for a COPY source: a protected .env is copied verbatim to a new
// tracked path (git's --find-copies-harder reports a `C` record whose
// SOURCE is .env). Round 4 treats a protected C source like a protected
// D/R source → fail-closed → the added public.txt (A record, filler +
// copied protected bytes) is skipped, and the copy itself does not leak.
func TestFilteredTrackedDiffFailsClosedOnProtectedCopy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, issue, _ := newReworkDispatchIssueWithGitWorkspace(t)
	ws := issue.Workspace.Path
	// A larger protected file so a verbatim copy is detected as a C record.
	envContent := []byte("SECRET=original\nEXTRA=line\nMORE=data\n")
	if err := os.WriteFile(filepath.Join(ws, ".env"), envContent, 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	gitCommit(t, ws, "add env")
	// Copy .env verbatim to a new tracked path (git detects a copy with
	// --find-copies-harder → C record whose source is .env).
	if err := os.WriteFile(filepath.Join(ws, renamedCopyDotEnv), envContent, 0o644); err != nil {
		t.Fatalf("copy .env to %s: %v", renamedCopyDotEnv, err)
	}
	if out, err := exec.Command("git", "-C", ws, "add", renamedCopyDotEnv).CombinedOutput(); err != nil {
		t.Fatalf("git add %s: %v\n%s", renamedCopyDotEnv, err, out)
	}
	// An added tracked file with filler + the protected bytes; under
	// round 4 the protected copy source triggers fail-closed → A skipped.
	var pb strings.Builder
	for i := 0; i < 20; i++ {
		pb.WriteString("filler line ")
		pb.WriteString("0123456789")
		pb.WriteByte('\n')
	}
	pb.WriteString("SECRET=original\n")
	if err := os.WriteFile(filepath.Join(ws, "public.txt"), []byte(pb.String()), 0o644); err != nil {
		t.Fatalf("write public.txt: %v", err)
	}
	if out, err := exec.Command("git", "-C", ws, "add", "public.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add public.txt: %v\n%s", err, out)
	}
	diff := string(filteredTrackedDiff(ws, nil))
	if strings.Contains(diff, "SECRET=original") {
		t.Fatalf("filtered tracked diff leaked protected bytes via added public.txt in protected-copy scenario:\n%s", diff)
	}
	if strings.Contains(diff, "diff --git a/public.txt b/public.txt") {
		t.Fatalf("filtered tracked diff kept added public.txt (A record) in protected-copy fail-closed mode:\n%s", diff)
	}
}

// renamedCopyDotEnv is the destination path used by copy-source tests.
const renamedCopyDotEnv = "renamed.txt"

// TestCumulativeUntrackedDigestFailsClosedOnProtectedRename codifies the
// round-4 FAIL-CLOSED behavior for untracked content when a protected
// file is staged as a RENAME: a protected rename source triggers
// fail-closed (just like a protected delete), so ALL non-path-protected
// untracked content is suppressed via a sentinel — changing notes.txt
// content does NOT move the digest (both runs produce the same
// sentinel-derived hash), and protected bytes never appear.
func TestCumulativeUntrackedDigestFailsClosedOnProtectedRename(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, issue, _ := newReworkDispatchIssueWithGitWorkspace(t)
	ws := issue.Workspace.Path
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("SECRET=original\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	gitCommit(t, ws, "add env")
	if out, err := exec.Command("git", "-C", ws, "mv", ".env", "renamed.txt").CombinedOutput(); err != nil {
		t.Fatalf("git mv .env renamed.txt: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(ws, "notes.txt"), []byte("hello-A\n"), 0o644); err != nil {
		t.Fatalf("write notes.txt A: %v", err)
	}
	digestA := cumulativeUntrackedDigest(ws, nil)
	if digestA == "" {
		t.Fatal("cumulative untracked digest is empty for notes.txt=hello-A in protected-rename fail-closed mode; want non-empty sentinel digest")
	}
	if len(digestA) != 64 || !isHex(digestA) {
		t.Fatalf("cumulative untracked digest for notes.txt=hello-A is not a 64-char hex hash: %q", digestA)
	}
	if strings.Contains(digestA, "SECRET=original") {
		t.Fatalf("cumulative untracked digest leaked protected bytes in protected-rename fail-closed mode: %q", digestA)
	}
	if err := os.WriteFile(filepath.Join(ws, "notes.txt"), []byte("hello-B\n"), 0o644); err != nil {
		t.Fatalf("write notes.txt B: %v", err)
	}
	digestB := cumulativeUntrackedDigest(ws, nil)
	if digestB == "" {
		t.Fatal("cumulative untracked digest is empty for notes.txt=hello-B in protected-rename fail-closed mode; want non-empty sentinel digest")
	}
	if digestA != digestB {
		t.Fatalf("protected-rename fail-closed digest moved with content changes (%q != %q); want identical sentinel-derived digest (content suppressed)", digestA, digestB)
	}
}

// TestFilteredTrackedDiffSkipsAddedCopyOfModifiedProtectedFile codifies the
// D4 / R16 round-6 fix for the modified-source-copy P1 leak in the
// orchestrator's filteredTrackedDiff path.
//
// Scenario: a protected tracked .env is committed (SECRET=old), then MODIFIED
// in the workspace to SECRET=new, then copied verbatim into a new public file
// (`cp .env public.txt`, `git add public.txt`), while the source .env REMAINS.
// `git diff --name-status --find-copies-harder HEAD` reports `M .env` +
// `A public.txt` — NOT a `C` record — because --find-copies-harder compares
// the copy against the unmodified HEAD blob, but the copy holds the modified
// (workspace) bytes. Round 5's copy-aware R/C-source check therefore does
// NOT fire, and the A record was kept → `git diff HEAD -- public.txt` hashed
// the modified protected bytes (SECRET=new) into cumulative_diff_sha.
//
// Round 6 closes this: when no protected file is deleted/renamed/copied (the
// source REMAINS), existingProtectedContentHashes unions the SHA256 hashes of
// all recoverable versions (workspace + HEAD + index) of all existing
// protected files. public.txt's workspace content (SECRET=new) matches .env's
// WORKSPACE content hash (SECRET=new) → the A record is content-hash-matched
// and SUPPRESSED. An unrelated safe added file (feature.txt) IS kept.
//
// Both the unstaged-modification and staged-modification variants are
// exercised (the index hash also matches when .env is staged-modified).
func TestFilteredTrackedDiffSkipsAddedCopyOfModifiedProtectedFile(t *testing.T) {
	variants := []struct {
		name     string
		stageEnv bool
	}{
		{name: "unstaged-modified", stageEnv: false},
		{name: "staged-modified", stageEnv: true},
	}
	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			_, issue, _ := newReworkDispatchIssueWithGitWorkspace(t)
			ws := issue.Workspace.Path
			if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("SECRET=old\n"), 0o600); err != nil {
				t.Fatalf("write .env: %v", err)
			}
			gitCommit(t, ws, "add env")
			// MODIFY the protected file (the bytes that will be copied).
			if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("SECRET=new\n"), 0o600); err != nil {
				t.Fatalf("modify .env: %v", err)
			}
			if v.stageEnv {
				if out, err := exec.Command("git", "-C", ws, "add", ".env").CombinedOutput(); err != nil {
					t.Fatalf("git add .env: %v\n%s", err, out)
				}
			}
			// Verbatim copy of the MODIFIED protected workspace bytes into a
			// new public file. Source .env REMAINS (no delete/rename/copy).
			modified, err := os.ReadFile(filepath.Join(ws, ".env"))
			if err != nil {
				t.Fatalf("read modified .env: %v", err)
			}
			if err := os.WriteFile(filepath.Join(ws, "public.txt"), modified, 0o644); err != nil {
				t.Fatalf("cp .env public.txt: %v", err)
			}
			if out, err := exec.Command("git", "-C", ws, "add", "public.txt").CombinedOutput(); err != nil {
				t.Fatalf("git add public.txt: %v\n%s", err, out)
			}
			// Unrelated safe added file (NOT a copy of any protected file) —
			// MUST be kept to prove common-case correlation survives.
			if err := os.WriteFile(filepath.Join(ws, "feature.txt"), []byte("new feature\n"), 0o644); err != nil {
				t.Fatalf("write feature.txt: %v", err)
			}
			if out, err := exec.Command("git", "-C", ws, "add", "feature.txt").CombinedOutput(); err != nil {
				t.Fatalf("git add feature.txt: %v\n%s", err, out)
			}

			// Sanity: confirm git does NOT detect this as a C record
			// (guards the test premise against a future git change).
			probe, err := exec.Command("git", "-C", ws, "diff", "--name-status", "--find-renames", "--find-copies", "--find-copies-harder", "-z", "HEAD").Output()
			if err != nil {
				t.Fatalf("name-status probe: %v", err)
			}
			if strings.Contains(string(probe), "C") {
				t.Fatalf("git unexpectedly detected a C record for modified-source copy; test premise invalid:\n%q", string(probe))
			}

			diff := string(filteredTrackedDiff(ws, nil))
			// The modified protected bytes MUST NOT leak via public.txt.
			if strings.Contains(diff, "SECRET=new") {
				t.Fatalf("filtered tracked diff leaked modified protected bytes via added copy public.txt:\n%s", diff)
			}
			if strings.Contains(diff, "diff --git a/public.txt b/public.txt") {
				t.Fatalf("filtered tracked diff included added copy-of-modified-protected public.txt:\n%s", diff)
			}
			// The unrelated safe added file MUST be kept.
			if !strings.Contains(diff, "diff --git a/feature.txt b/feature.txt") {
				t.Fatalf("filtered tracked diff dropped unrelated safe added feature.txt (common-case correlation broken):\n%s", diff)
			}
			if !strings.Contains(diff, "+new feature") {
				t.Fatalf("filtered tracked diff missing feature.txt content:\n%s", diff)
			}
		})
	}
}

// TestCumulativeUntrackedDigestSkipsCopyOfExistingProtectedContent codifies
// the D4 / R16 round-6 fix for the untracked modified-source-copy P1 leak in
// the orchestrator's cumulativeUntrackedDigest path.
//
// Scenario: a protected tracked .env is committed (SECRET=old), MODIFIED to
// SECRET=new, and an untracked safe.txt is a verbatim copy of the modified
// .env (safe.txt = SECRET=new). Source .env REMAINS (no delete/rename/copy).
// Round 4/5 hashed safe.txt's content into the digest, leaking the modified
// protected bytes. Round 6 content-hash-matches safe.txt's content against
// existingProtectedContentHashes (which includes .env's workspace hash =
// SECRET=new) and writes a SENTINEL instead, so the protected bytes never
// enter the digest.
//
// Asserts:
//   - the digest is non-empty and a 64-char hex hash (no raw protected bytes);
//   - safe.txt's protected-copy content does NOT move the digest (a separate
//     run where safe.txt has different safe content yields a different digest
//     ONLY because unrelated content changes — actually under round-6 the
//     protected-copy is sentinel-suppressed so changing safe.txt from one
//     protected copy to another protected copy yields the SAME digest; to
//     prove content correlation is preserved for SAFE unrelated files, we
//     also assert that with NO protected file at all, an untracked file's
//     content DOES move the digest).
func TestCumulativeUntrackedDigestSkipsCopyOfExistingProtectedContent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, issue, _ := newReworkDispatchIssueWithGitWorkspace(t)
	ws := issue.Workspace.Path
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("SECRET=old\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	gitCommit(t, ws, "add env")
	// MODIFY the protected file.
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("SECRET=new\n"), 0o600); err != nil {
		t.Fatalf("modify .env: %v", err)
	}
	// safe.txt is a verbatim copy of the modified protected workspace bytes.
	modified, err := os.ReadFile(filepath.Join(ws, ".env"))
	if err != nil {
		t.Fatalf("read modified .env: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, "safe.txt"), modified, 0o644); err != nil {
		t.Fatalf("write safe.txt (copy of modified .env): %v", err)
	}

	digestCopy := cumulativeUntrackedDigest(ws, nil)
	if digestCopy == "" {
		t.Fatal("cumulative untracked digest is empty for untracked copy of modified protected content; want non-empty sentinel digest")
	}
	if len(digestCopy) != 64 || !isHex(digestCopy) {
		t.Fatalf("cumulative untracked digest is not a 64-char hex hash: %q", digestCopy)
	}
	if strings.Contains(digestCopy, "SECRET=new") {
		t.Fatalf("cumulative untracked digest leaked modified protected bytes: %q", digestCopy)
	}

	// Replace safe.txt with a DIFFERENT safe content (not a protected copy).
	// Under round-6 the protected-copy safe.txt was sentinel-suppressed, so
	// its content did NOT move the digest; now with genuinely different
	// (non-protected) content, the content hash DOES move the digest —
	// proving that safe unrelated content is correlated while protected
	// copies are suppressed.
	if err := os.WriteFile(filepath.Join(ws, "safe.txt"), []byte("plain safe note A\n"), 0o644); err != nil {
		t.Fatalf("write safe.txt (safe content A): %v", err)
	}
	digestSafeA := cumulativeUntrackedDigest(ws, nil)
	if digestSafeA == "" {
		t.Fatal("cumulative untracked digest is empty for safe.txt safe content A")
	}
	if digestSafeA == digestCopy {
		t.Fatalf("digest did not change when safe.txt switched from protected-copy (sentinel) to safe content (hashed); want content-level correlation for safe content (%q == %q)", digestSafeA, digestCopy)
	}
	if strings.Contains(digestSafeA, "SECRET=new") {
		t.Fatalf("cumulative untracked digest leaked protected bytes (safe A variant): %q", digestSafeA)
	}
	// Change safe content again → digest moves again (safe-content correlation).
	if err := os.WriteFile(filepath.Join(ws, "safe.txt"), []byte("plain safe note B\n"), 0o644); err != nil {
		t.Fatalf("write safe.txt (safe content B): %v", err)
	}
	digestSafeB := cumulativeUntrackedDigest(ws, nil)
	if digestSafeA == digestSafeB {
		t.Fatalf("digest collapsed safe.txt content changes A->B (%q == %q); want content-level correlation", digestSafeA, digestSafeB)
	}

	// Counterpoint: with NO protected file at all, an untracked file's
	// content DOES move the digest (common-case correlation preserved).
	t.Run("no_protected_file_correlation", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		_, issue2, _ := newReworkDispatchIssueWithGitWorkspace(t)
		ws2 := issue2.Workspace.Path
		if err := os.WriteFile(filepath.Join(ws2, "notes.txt"), []byte("hello-A\n"), 0o644); err != nil {
			t.Fatalf("write notes.txt A: %v", err)
		}
		dA := cumulativeUntrackedDigest(ws2, nil)
		if dA == "" {
			t.Fatal("cumulative untracked digest empty for notes.txt A (no protected file)")
		}
		if err := os.WriteFile(filepath.Join(ws2, "notes.txt"), []byte("hello-B\n"), 0o644); err != nil {
			t.Fatalf("write notes.txt B: %v", err)
		}
		dB := cumulativeUntrackedDigest(ws2, nil)
		if dA == dB {
			t.Fatalf("common-case digest collapsed notes.txt content A->B (%q == %q); want content-level correlation", dA, dB)
		}
	})
}

// TestFilteredTrackedDiffSkipsAddedCopyOfIgnoredProtectedFile codifies the
// D4 / R16 round-7 fix A for the orchestrator's filteredTrackedDiff path.
//
// Scenario: a protected .env is IGNORED by .gitignore (the common case — .env
// is typically gitignored), so it is UNTRACKED and never appears in `git
// ls-files` or `git ls-files --others --exclude-standard`. It is then copied
// verbatim into a new public file (`cp .env public.txt`, `git add public.txt`),
// while the source .env REMAINS (ignored). Round 6's existingProtectedContent
// Hashes enumerated only tracked + untracked-non-ignored files, so the ignored
// .env was never hashed into the set → public.txt (a copy of the protected
// content) was treated as safe and its bytes leaked into the filtered diff.
//
// Round 7 fix A adds a THIRD enumeration: `git ls-files --others --ignored
// --exclude-standard -z`, which lists ignored files. The ignored .env is now
// enumerated, its WORKSPACE content (the bytes a `cp` copies) is hashed into
// existingHashes, and public.txt's workspace content matches → the A record
// is SUPPRESSED. An unrelated safe added file (feature.txt) IS kept.
func TestFilteredTrackedDiffSkipsAddedCopyOfIgnoredProtectedFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, issue, _ := newReworkDispatchIssueWithGitWorkspace(t)
	ws := issue.Workspace.Path
	// Make .env gitignored (the common ignored-secret setup).
	if err := os.WriteFile(filepath.Join(ws, ".gitignore"), []byte(".env\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	// Protected .env (ignored, untracked). Real content so its workspace hash
	// is in existingHashes.
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("SECRET=x\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	// Commit .gitignore so it is tracked (does not affect the ignored .env).
	if out, err := exec.Command("git", "-C", ws, "add", ".gitignore").CombinedOutput(); err != nil {
		t.Fatalf("git add .gitignore: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", ws, "commit", "-m", "add gitignore").CombinedOutput(); err != nil {
		t.Fatalf("git commit gitignore: %v\n%s", err, out)
	}
	// Sanity: confirm .env is IGNORED (guards the test premise). The non-
	// ignored enumeration must NOT list it; the ignored enumeration MUST.
	nonIgnored, err := exec.Command("git", "-C", ws, "ls-files", "--others", "--exclude-standard", "-z").Output()
	if err != nil {
		t.Fatalf("ls-files --others --exclude-standard probe: %v", err)
	}
	if strings.Contains(string(nonIgnored), ".env") {
		t.Fatalf("test premise invalid: .env not actually ignored by --others --exclude-standard:\n%q", string(nonIgnored))
	}
	ignored, err := exec.Command("git", "-C", ws, "ls-files", "--others", "--ignored", "--exclude-standard", "-z").Output()
	if err != nil {
		t.Fatalf("ls-files --ignored probe: %v", err)
	}
	if !strings.Contains(string(ignored), ".env") {
		t.Fatalf("test premise invalid: .env not listed by --others --ignored --exclude-standard:\n%q", string(ignored))
	}

	// Verbatim copy of the ignored protected .env into a new public file.
	envBytes, err := os.ReadFile(filepath.Join(ws, ".env"))
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, "public.txt"), envBytes, 0o644); err != nil {
		t.Fatalf("cp .env public.txt: %v", err)
	}
	if out, err := exec.Command("git", "-C", ws, "add", "public.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add public.txt: %v\n%s", err, out)
	}
	// Unrelated safe added file (NOT a copy of any protected file) — MUST be
	// kept to prove common-case correlation survives.
	if err := os.WriteFile(filepath.Join(ws, "feature.txt"), []byte("new feature\n"), 0o644); err != nil {
		t.Fatalf("write feature.txt: %v", err)
	}
	if out, err := exec.Command("git", "-C", ws, "add", "feature.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add feature.txt: %v\n%s", err, out)
	}

	diff := string(filteredTrackedDiff(ws, nil))
	// The ignored protected bytes MUST NOT leak via public.txt.
	if strings.Contains(diff, "SECRET=x") {
		t.Fatalf("filtered tracked diff leaked ignored-protected bytes via added copy public.txt:\n%s", diff)
	}
	if strings.Contains(diff, "diff --git a/public.txt b/public.txt") {
		t.Fatalf("filtered tracked diff included added copy-of-ignored-protected public.txt:\n%s", diff)
	}
	// The unrelated safe added file MUST be kept.
	if !strings.Contains(diff, "diff --git a/feature.txt b/feature.txt") {
		t.Fatalf("filtered tracked diff dropped unrelated safe added feature.txt (common-case correlation broken):\n%s", diff)
	}
	if !strings.Contains(diff, "+new feature") {
		t.Fatalf("filtered tracked diff missing feature.txt content:\n%s", diff)
	}
}

// TestCumulativeUntrackedDigestSkipsCopyOfIgnoredProtectedFile codifies the
// D4 / R16 round-7 fix A for the orchestrator's cumulativeUntrackedDigest
// path (the untracked-copy variant of the ignored-protected-file leak).
//
// Scenario: a protected .env is IGNORED by .gitignore (untracked + ignored),
// then copied verbatim into an untracked safe.txt, while .env REMAINS. Round
// 6 did not enumerate ignored files, so the ignored .env's content was not in
// existingHashes and safe.txt (a copy of the protected content) was hashed
// into the digest, leaking the protected bytes. Round 7 fix A enumerates
// ignored files so .env's workspace hash is in existingHashes → safe.txt's
// content matches → SENTINEL written, no protected bytes in the digest.
func TestCumulativeUntrackedDigestSkipsCopyOfIgnoredProtectedFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, issue, _ := newReworkDispatchIssueWithGitWorkspace(t)
	ws := issue.Workspace.Path
	if err := os.WriteFile(filepath.Join(ws, ".gitignore"), []byte(".env\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("SECRET=x\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	if out, err := exec.Command("git", "-C", ws, "add", ".gitignore").CombinedOutput(); err != nil {
		t.Fatalf("git add .gitignore: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", ws, "commit", "-m", "add gitignore").CombinedOutput(); err != nil {
		t.Fatalf("git commit gitignore: %v\n%s", err, out)
	}
	// safe.txt is an untracked verbatim copy of the ignored protected .env.
	envBytes, err := os.ReadFile(filepath.Join(ws, ".env"))
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, "safe.txt"), envBytes, 0o644); err != nil {
		t.Fatalf("write safe.txt (copy of ignored .env): %v", err)
	}

	digest := cumulativeUntrackedDigest(ws, nil)
	if digest == "" {
		t.Fatal("cumulative untracked digest is empty for untracked copy of ignored protected content; want non-empty sentinel digest")
	}
	if len(digest) != 64 || !isHex(digest) {
		t.Fatalf("cumulative untracked digest is not a 64-char hex hash: %q", digest)
	}
	if strings.Contains(digest, "SECRET=x") {
		t.Fatalf("cumulative untracked digest leaked ignored-protected bytes: %q", digest)
	}
	// Counterpoint: replacing safe.txt with genuinely safe (non-protected)
	// content moves the digest — safe-content correlation preserved while
	// the protected copy is sentinel-suppressed.
	if err := os.WriteFile(filepath.Join(ws, "safe.txt"), []byte("plain safe note\n"), 0o644); err != nil {
		t.Fatalf("write safe.txt (safe content): %v", err)
	}
	digestSafe := cumulativeUntrackedDigest(ws, nil)
	if digestSafe == digest {
		t.Fatalf("digest did not change when safe.txt switched from ignored-protected-copy (sentinel) to safe content (hashed); want content-level correlation (%q == %q)", digestSafe, digest)
	}
	if strings.Contains(digestSafe, "SECRET=x") {
		t.Fatalf("cumulative untracked digest leaked protected bytes (safe variant): %q", digestSafe)
	}
}

// TestFilteredTrackedDiffKeepsEmptyFileWhenProtectedHasNoBlob codifies the
// D4 / R16 round-7 fix B for the orchestrator's filteredTrackedDiff path.
//
// Scenario: a protected .env exists in the workspace but has NO HEAD/index
// version (untracked, NOT ignored here so it IS enumerated by the non-ignored
// list). `git show HEAD:.env` and `git show :.env` both FAIL (non-zero exit,
// no stdout). Round 6's hashGitBlob ignored cmd.Wait()'s error and returned
// sha256("") (because io.Copy read 0 bytes with copyErr=nil) as a VALID
// protected hash. An unrelated EMPTY added file (empty.txt, 0 bytes) then
// matched that synthetic empty hash and was WRONGLY SUPPRESSED. Round 7 fix B
// treats a non-nil Wait error (non-zero exit) as ok=false (absent blob), so
// sha256("") is NOT added to existingHashes and the empty file is KEPT.
func TestFilteredTrackedDiffKeepsEmptyFileWhenProtectedHasNoBlob(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, issue, _ := newReworkDispatchIssueWithGitWorkspace(t)
	ws := issue.Workspace.Path
	// Protected .env exists in workspace with REAL content (so its workspace
	// hash IS in existingHashes) but is UNTRACKED — no HEAD/index version.
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("SECRET=real\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	// Sanity: confirm `git show HEAD:.env` FAILS (non-zero exit) — the
	// premise of fix B. An untracked file has no HEAD blob.
	if out, err := exec.Command("git", "-C", ws, "show", "HEAD:.env").CombinedOutput(); err == nil {
		t.Fatalf("test premise invalid: git show HEAD:.env succeeded on an untracked file:\n%s", string(out))
	}
	// An unrelated EMPTY added file (0 bytes). Its sha256("") must NOT match
	// any protected hash (because git show on the absent .env blob now
	// returns ok=false), so it MUST be kept.
	if err := os.WriteFile(filepath.Join(ws, "empty.txt"), []byte{}, 0o644); err != nil {
		t.Fatalf("write empty.txt: %v", err)
	}
	if out, err := exec.Command("git", "-C", ws, "add", "empty.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add empty.txt: %v\n%s", err, out)
	}
	// A non-empty unrelated added file, kept to prove normal correlation.
	if err := os.WriteFile(filepath.Join(ws, "feature.txt"), []byte("new feature\n"), 0o644); err != nil {
		t.Fatalf("write feature.txt: %v", err)
	}
	if out, err := exec.Command("git", "-C", ws, "add", "feature.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add feature.txt: %v\n%s", err, out)
	}

	diff := string(filteredTrackedDiff(ws, nil))
	// The empty file MUST be kept (NOT wrongly suppressed by a synthetic
	// sha256("") protected hash).
	if !strings.Contains(diff, "diff --git a/empty.txt b/empty.txt") {
		t.Fatalf("filtered tracked diff wrongly suppressed unrelated empty added file (fix B regression):\n%s", diff)
	}
	// The non-empty unrelated file MUST be kept.
	if !strings.Contains(diff, "diff --git a/feature.txt b/feature.txt") {
		t.Fatalf("filtered tracked diff dropped unrelated safe added feature.txt:\n%s", diff)
	}
	// The protected .env bytes must never leak.
	if strings.Contains(diff, "SECRET=real") {
		t.Fatalf("filtered tracked diff leaked protected .env bytes:\n%s", diff)
	}
}

// TestCumulativeUntrackedDigestKeepsEmptyFileWhenProtectedHasNoBlob codifies
// the D4 / R16 round-7 fix B for the orchestrator's cumulativeUntrackedDigest
// path (the untracked variant of the empty-file false-suppression).
//
// Scenario: a protected .env exists in the workspace (real content) but is
// UNTRACKED (no HEAD/index version). An unrelated EMPTY untracked file
// (empty.txt, 0 bytes) is present. Round 6's hashGitBlob returned sha256("")
// for the absent .env blob, so empty.txt's sha256("") matched and it was
// WRONGLY SUPPRESSED (sentinel) from the digest. Round 7 fix B makes the
// absent-blob lookup return ok=false, so sha256("") is NOT a protected hash
// and empty.txt is KEPT in the digest (its hash, not the raw empty content,
// moves the digest — but it is not suppressed).
func TestCumulativeUntrackedDigestKeepsEmptyFileWhenProtectedHasNoBlob(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, issue, _ := newReworkDispatchIssueWithGitWorkspace(t)
	ws := issue.Workspace.Path
	// Protected .env untracked with real content (workspace hash in set).
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("SECRET=real\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	// Unrelated EMPTY untracked file (0 bytes).
	if err := os.WriteFile(filepath.Join(ws, "empty.txt"), []byte{}, 0o644); err != nil {
		t.Fatalf("write empty.txt: %v", err)
	}
	digestWithEmpty := cumulativeUntrackedDigest(ws, nil)
	if digestWithEmpty == "" {
		t.Fatal("cumulative untracked digest is empty with empty.txt present; want non-empty digest (empty file kept, not suppressed)")
	}
	if len(digestWithEmpty) != 64 || !isHex(digestWithEmpty) {
		t.Fatalf("cumulative untracked digest is not a 64-char hex hash: %q", digestWithEmpty)
	}
	if strings.Contains(digestWithEmpty, "SECRET=real") {
		t.Fatalf("cumulative untracked digest leaked protected bytes: %q", digestWithEmpty)
	}
	// Counterpoint: WITHOUT empty.txt, the digest must DIFFER — proving
	// empty.txt's content (its hash) is contributing and was NOT suppressed.
	// Remove empty.txt and recompute; the digest should change because the
	// empty file's hash is no longer folded in.
	if err := os.Remove(filepath.Join(ws, "empty.txt")); err != nil {
		t.Fatalf("remove empty.txt: %v", err)
	}
	digestNoEmpty := cumulativeUntrackedDigest(ws, nil)
	if digestWithEmpty == digestNoEmpty {
		t.Fatalf("cumulative untracked digest did not change when empty.txt was removed; want empty file KEPT (its hash folded in), not suppressed (fix B regression): %q == %q", digestWithEmpty, digestNoEmpty)
	}
}

// TestFilteredTrackedDiffSkipsModifiedTrackedCopyOfProtectedFile codifies the
// D4 / R16 round-8 fix for the modified-tracked-copy P1 leak in the
// orchestrator's filteredTrackedDiffPathspecs path.
//
// Scenario: a protected tracked .env is committed (SECRET=old), MODIFIED to
// SECRET=new, and an EXISTING tracked non-protected file config.txt is
// OVERWRITTEN with the modified protected bytes (`cp .env config.txt`,
// config.txt already tracked → `git diff --name-status` reports
// `M config.txt`, NOT `A`). Source .env REMAINS. Round 5/6's content-hash
// check only ran for A records, so `git diff HEAD -- config.txt` hashed the
// modified protected bytes (SECRET=new) into cumulative_diff_sha.
//
// Round 8 extends the content-hash check to M records: config.txt's workspace
// content matches .env's workspace hash in existingHashes → the M record is
// skipped. An unrelated modified tracked feature.txt IS kept (the M check is
// content-hash, not fail-closed-all).
func TestFilteredTrackedDiffSkipsModifiedTrackedCopyOfProtectedFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, issue, _ := newReworkDispatchIssueWithGitWorkspace(t)
	ws := issue.Workspace.Path
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("SECRET=old\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, "config.txt"), []byte("base config\n"), 0o644); err != nil {
		t.Fatalf("write config.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, "feature.txt"), []byte("old feature\n"), 0o644); err != nil {
		t.Fatalf("write feature.txt: %v", err)
	}
	gitCommit(t, ws, "base")
	// MODIFY the protected file (the bytes that will be copied).
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("SECRET=new\n"), 0o600); err != nil {
		t.Fatalf("modify .env: %v", err)
	}
	// Overwrite the EXISTING tracked config.txt with the modified protected
	// bytes. git diff --name-status reports `M config.txt`, not `A`.
	modified, err := os.ReadFile(filepath.Join(ws, ".env"))
	if err != nil {
		t.Fatalf("read modified .env: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, "config.txt"), modified, 0o644); err != nil {
		t.Fatalf("overwrite config.txt: %v", err)
	}
	if out, err := exec.Command("git", "-C", ws, "add", "config.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add config.txt: %v\n%s", err, out)
	}
	// Unrelated modified tracked file — MUST be kept.
	if err := os.WriteFile(filepath.Join(ws, "feature.txt"), []byte("new feature\n"), 0o644); err != nil {
		t.Fatalf("modify feature.txt: %v", err)
	}
	if out, err := exec.Command("git", "-C", ws, "add", "feature.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add feature.txt: %v\n%s", err, out)
	}

	// Sanity: confirm git reports config.txt as M (not A).
	probe, err := exec.Command("git", "-C", ws, "diff", "--name-status", "--find-renames", "--find-copies", "--find-copies-harder", "-z", "HEAD").Output()
	if err != nil {
		t.Fatalf("name-status probe: %v", err)
	}
	if !strings.Contains(string(probe), "M\x00config.txt") {
		t.Fatalf("config.txt not reported as a modified (M) record; test premise invalid:\n%q", string(probe))
	}

	diff := string(filteredTrackedDiff(ws, nil))
	// The modified protected bytes MUST NOT leak via config.txt.
	if strings.Contains(diff, "SECRET=new") {
		t.Fatalf("filtered tracked diff leaked modified protected bytes via modified-tracked copy config.txt:\n%s", diff)
	}
	if strings.Contains(diff, "diff --git a/config.txt b/config.txt") {
		t.Fatalf("filtered tracked diff included modified-tracked-copy-of-protected config.txt:\n%s", diff)
	}
	// The unrelated modified feature.txt MUST be kept.
	if !strings.Contains(diff, "diff --git a/feature.txt b/feature.txt") {
		t.Fatalf("filtered tracked diff dropped unrelated modified tracked feature.txt (correlation broken):\n%s", diff)
	}
	if !strings.Contains(diff, "+new feature") {
		t.Fatalf("filtered tracked diff missing feature.txt content:\n%s", diff)
	}
}

// TestFilteredTrackedDiffFailsClosedOnProtectedTypechange codifies the D4 / R16
// round-8 fix for the protected-typechange P1 leak in the orchestrator's
// deletedProtectedContentHashes path.
//
// Scenario: a protected tracked .env is committed (SECRET=old), MODIFIED to
// SECRET=new, copied into a new added public.txt (filler + the secret so it
// is reported as A, not C), then the protected .env is REPLACED BY A SYMLINK
// (`rm .env; ln -s public.txt .env`). `git diff --name-status` reports
// `T .env` (typechange) plus `A public.txt`. Round 4/5/6 used
// `--diff-filter=DRC`, which EXCLUDES T, so a protected typechange left
// hashesUnknown=false and the added public.txt (a copy of the now-unrecoverable
// modified .env bytes) was kept → SECRET=new hashed into cumulative_diff_sha.
//
// Round 8 uses `--diff-filter=DRCT` and treats a protected T as fail-closed:
// hashesUnknown=true → the A record (public.txt) is skipped. The modified
// protected bytes never enter the diff.
func TestFilteredTrackedDiffFailsClosedOnProtectedTypechange(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, issue, _ := newReworkDispatchIssueWithGitWorkspace(t)
	ws := issue.Workspace.Path
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("SECRET=old\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	gitCommit(t, ws, "add env")
	// MODIFY the protected file.
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("SECRET=new\n"), 0o600); err != nil {
		t.Fatalf("modify .env: %v", err)
	}
	// public.txt = filler + the modified protected bytes, large enough to
	// fall below git's copy similarity threshold → reported as A (not C).
	var pb strings.Builder
	for i := 0; i < 20; i++ {
		pb.WriteString("filler line 0123456789\n")
	}
	pb.WriteString("SECRET=new\n")
	if err := os.WriteFile(filepath.Join(ws, "public.txt"), []byte(pb.String()), 0o644); err != nil {
		t.Fatalf("write public.txt: %v", err)
	}
	if out, err := exec.Command("git", "-C", ws, "add", "public.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add public.txt: %v\n%s", err, out)
	}
	// Replace the protected .env with a symlink → typechange (T).
	if err := os.Remove(filepath.Join(ws, ".env")); err != nil {
		t.Fatalf("rm .env: %v", err)
	}
	if err := os.Symlink("public.txt", filepath.Join(ws, ".env")); err != nil {
		t.Fatalf("symlink .env -> public.txt: %v", err)
	}

	// Sanity: confirm git reports a T record on .env under DRCT (and that
	// the old DRC filter would have MISSED it).
	probeDRCT, err := exec.Command("git", "-C", ws, "diff", "--name-status", "--find-renames", "--find-copies", "--find-copies-harder", "--diff-filter=DRCT", "-z", "HEAD").Output()
	if err != nil {
		t.Fatalf("DRCT probe: %v", err)
	}
	if !strings.Contains(string(probeDRCT), "T\x00.env") {
		t.Fatalf("protected .env not reported as a typechange (T) under DRCT; test premise invalid:\n%q", string(probeDRCT))
	}

	diff := string(filteredTrackedDiff(ws, nil))
	if strings.Contains(diff, "SECRET=new") {
		t.Fatalf("filtered tracked diff leaked protected bytes via added public.txt in protected-typechange scenario:\n%s", diff)
	}
	if strings.Contains(diff, "diff --git a/public.txt b/public.txt") {
		t.Fatalf("filtered tracked diff kept added public.txt (A record) in protected-typechange fail-closed mode:\n%s", diff)
	}
}

// TestHashWorkspaceFileSkipsSpecialFiles codifies the D4 / R16 round-8 fix
// for the special-file blocking P2 in the orchestrator's hashWorkspaceFile.
//
// A protected path that is a FIFO, device, or symlink-to-non-regular must
// NOT be opened + io.Copy'd (a FIFO/device can block indefinitely during
// computeCumulativeDiffSHA, which calls this for every enumerated protected
// file and every added/untracked candidate). Round-9 refinement: a symlink
// to a REGULAR file must be FOLLOWED and hashed (a protected path that is a
// symlink to a regular secret must contribute to existingHashes, else a copy
// made through it leaks into cumulative_diff_sha). This test exercises the
// helper directly (not via computeCumulativeDiffSHA) so it cannot hang the
// suite on a FIFO.
func TestHashWorkspaceFileSkipsSpecialFiles(t *testing.T) {
	dir := t.TempDir()

	// FIFO — must be skipped (would block on Open/Read).
	fifoPath := filepath.Join(dir, ".env")
	if err := syscall.Mkfifo(fifoPath, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	if _, ok := hashWorkspaceFile(fifoPath); ok {
		t.Fatalf("hashWorkspaceFile opened a FIFO (would block); want ok=false")
	}

	// Symlink to a FIFO (non-regular target) — must be skipped (the target
	// would block). Resolves to a non-regular mode via os.Stat.
	linkToFifo := filepath.Join(dir, "id_rsa")
	if err := os.Symlink(fifoPath, linkToFifo); err != nil {
		t.Fatalf("symlink to fifo: %v", err)
	}
	if _, ok := hashWorkspaceFile(linkToFifo); ok {
		t.Fatalf("hashWorkspaceFile followed a symlink to a FIFO (would block); want ok=false")
	}

	// Regular file — must hash normally.
	regularPath := filepath.Join(dir, "regular.txt")
	if err := os.WriteFile(regularPath, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write regular: %v", err)
	}
	h := sha256.Sum256([]byte("hello\n"))
	want := hex.EncodeToString(h[:])
	got, ok := hashWorkspaceFile(regularPath)
	if !ok {
		t.Fatalf("hashWorkspaceFile skipped a regular file; want ok=true")
	}
	if got != want {
		t.Fatalf("hashWorkspaceFile regular hash = %q, want %q", got, want)
	}

	// D4/R16 round-9: symlink to a REGULAR file — must be FOLLOWED and
	// hashed (a protected path that is a symlink to a regular secret must
	// contribute to existingHashes, else a copy made through it leaks).
	secretTarget := filepath.Join(dir, "shared-env")
	if err := os.WriteFile(secretTarget, []byte("SECRET=shared\n"), 0o600); err != nil {
		t.Fatalf("write symlink target: %v", err)
	}
	symlinkToRegular := filepath.Join(dir, ".env-link")
	if err := os.Symlink(secretTarget, symlinkToRegular); err != nil {
		t.Fatalf("symlink to regular: %v", err)
	}
	hLink := sha256.Sum256([]byte("SECRET=shared\n"))
	wantLink := hex.EncodeToString(hLink[:])
	gotLink, okLink := hashWorkspaceFile(symlinkToRegular)
	if !okLink {
		t.Fatalf("hashWorkspaceFile skipped a symlink to a regular file; want ok=true (round-9: follow regular symlink targets)")
	}
	if gotLink != wantLink {
		t.Fatalf("hashWorkspaceFile symlink hash = %q, want %q (hash of the regular target's bytes)", gotLink, wantLink)
	}

	// Broken symlink (dangling target) — must be skipped (Stat fails).
	broken := filepath.Join(dir, "broken-link")
	if err := os.Symlink(filepath.Join(dir, "does-not-exist"), broken); err != nil {
		t.Fatalf("symlink broken: %v", err)
	}
	if _, ok := hashWorkspaceFile(broken); ok {
		t.Fatalf("hashWorkspaceFile followed a broken symlink; want ok=false")
	}
}

// TestFilteredTrackedDiffSkipsCopyViaSymlinkedProtectedFile codifies the D4
// / R16 round-9 fix for the symlinked-protected-source P2 leak in the
// orchestrator's cumulative-diff path (finding B).
//
// Scenario: a protected .env is a SYMLINK to a regular secret file
// (`.env -> shared/env`, shared/env = SECRET=real). Round 8's Lstat guard
// skipped ALL symlinks in hashWorkspaceFile, so existingProtectedContent
// Hashes never recorded the protected bytes; a copy made through that
// symlink (into an added public.txt) was then KEPT by
// filteredTrackedDiffPathspecs and `git diff HEAD -- public.txt` hashed the
// protected bytes into cumulative_diff_sha.
//
// Round 9 makes hashWorkspaceFile Stat (follow) symlinks: a symlink whose
// target is regular is hashed, so existingHashes includes the symlinked
// .env's bytes and the copy (public.txt) is content-hash-suppressed. An
// unrelated added file (feature.txt) IS kept.
func TestFilteredTrackedDiffSkipsCopyViaSymlinkedProtectedFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, issue, _ := newReworkDispatchIssueWithGitWorkspace(t)
	ws := issue.Workspace.Path
	if err := os.MkdirAll(filepath.Join(ws, "shared"), 0o755); err != nil {
		t.Fatalf("mkdir shared: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, "shared/env"), []byte("SECRET=real\n"), 0o644); err != nil {
		t.Fatalf("write shared/env: %v", err)
	}
	gitCommit(t, ws, "add shared env")
	// .env -> shared/env (a symlinked protected file; .env is protected by
	// the built-in IsProtectedPath).
	if err := os.Symlink("shared/env", filepath.Join(ws, ".env")); err != nil {
		t.Fatalf("symlink .env -> shared/env: %v", err)
	}
	// Copy the symlinked protected file's content (resolves to SECRET=real)
	// into a new added public file. Source .env REMAINS.
	modified, err := os.ReadFile(filepath.Join(ws, ".env"))
	if err != nil {
		t.Fatalf("read symlinked .env: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, "public.txt"), modified, 0o644); err != nil {
		t.Fatalf("write public.txt: %v", err)
	}
	if out, err := exec.Command("git", "-C", ws, "add", "public.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add public.txt: %v\n%s", err, out)
	}
	// Unrelated safe added file — MUST be kept.
	if err := os.WriteFile(filepath.Join(ws, "feature.txt"), []byte("new feature\n"), 0o644); err != nil {
		t.Fatalf("write feature.txt: %v", err)
	}
	if out, err := exec.Command("git", "-C", ws, "add", "feature.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add feature.txt: %v\n%s", err, out)
	}

	diff := string(filteredTrackedDiff(ws, nil))
	// The symlinked protected bytes MUST NOT leak via public.txt.
	if strings.Contains(diff, "SECRET=real") {
		t.Fatalf("filtered tracked diff leaked symlinked protected bytes via added public.txt:\n%s", diff)
	}
	if strings.Contains(diff, "diff --git a/public.txt b/public.txt") {
		t.Fatalf("filtered tracked diff included added copy-of-symlinked-protected public.txt:\n%s", diff)
	}
	// The unrelated safe added file MUST be kept.
	if !strings.Contains(diff, "diff --git a/feature.txt b/feature.txt") {
		t.Fatalf("filtered tracked diff dropped unrelated safe added feature.txt (correlation broken):\n%s", diff)
	}
}

// TestFilteredTrackedDiffSkipsProtectedBytesInTrackedTypechange codifies the
// D4 / R16 round-10 fix (codex finding F) for the typechange-on-non-
// protected-path P1 leak in the orchestrator's cumulative-diff path.
//
// Scenario: a protected .env is committed (SECRET=real). An existing tracked
// NON-protected config.txt is replaced by a symlink whose target text is
// copied from .env (`rm config.txt; ln -s "$(cat .env)" config.txt`).
// `git diff --name-status` reports `T config.txt` (non-protected path).
// `git diff HEAD -- config.txt` emits the symlink target (`+SECRET=real`),
// hashing the protected bytes into cumulative_diff_sha. Round 8's protected-
// path typechange fail-closed only fires for typechanges on PROTECTED paths.
//
// Round 10: filteredTrackedDiffPathspecs content-hash-checks the emitted
// bytes of a non-protected T record (symlink target, or workspace content)
// against existingHashes; a match skips it. An unrelated modified tracked
// file (feature.txt, old→new) IS kept.
func TestFilteredTrackedDiffSkipsProtectedBytesInTrackedTypechange(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, issue, _ := newReworkDispatchIssueWithGitWorkspace(t)
	ws := issue.Workspace.Path
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("SECRET=real\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, "config.txt"), []byte("base config\n"), 0o644); err != nil {
		t.Fatalf("write config.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, "feature.txt"), []byte("old feature\n"), 0o644); err != nil {
		t.Fatalf("write feature.txt: %v", err)
	}
	gitCommit(t, ws, "base")
	// Replace config.txt with a symlink whose target text IS the protected
	// .env's content.
	secret, err := os.ReadFile(filepath.Join(ws, ".env"))
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	if err := os.Remove(filepath.Join(ws, "config.txt")); err != nil {
		t.Fatalf("rm config.txt: %v", err)
	}
	if err := os.Symlink(string(secret), filepath.Join(ws, "config.txt")); err != nil {
		t.Fatalf("ln -s <secret> config.txt: %v", err)
	}
	if out, err := exec.Command("git", "-C", ws, "add", "config.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add config.txt: %v\n%s", err, out)
	}
	// Unrelated modified tracked file — MUST be kept.
	if err := os.WriteFile(filepath.Join(ws, "feature.txt"), []byte("new feature\n"), 0o644); err != nil {
		t.Fatalf("modify feature.txt: %v", err)
	}
	if out, err := exec.Command("git", "-C", ws, "add", "feature.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add feature.txt: %v\n%s", err, out)
	}

	// Sanity: confirm git reports a T record on config.txt.
	probe, err := exec.Command("git", "-C", ws, "diff", "--name-status", "--find-renames", "--find-copies", "--find-copies-harder", "-z", "HEAD").Output()
	if err != nil {
		t.Fatalf("name-status probe: %v", err)
	}
	if !strings.Contains(string(probe), "T\x00config.txt") {
		t.Fatalf("config.txt not reported as a typechange (T); test premise invalid:\n%q", string(probe))
	}

	diff := string(filteredTrackedDiff(ws, nil))
	// The protected bytes (SECRET=real) emitted via the typechanged
	// config.txt symlink target MUST NOT enter the diff.
	if strings.Contains(diff, "SECRET=real") {
		t.Fatalf("filtered tracked diff leaked protected bytes via tracked typechange config.txt:\n%s", diff)
	}
	if strings.Contains(diff, "diff --git a/config.txt b/config.txt") {
		t.Fatalf("filtered tracked diff included typechanged-protected config.txt:\n%s", diff)
	}
	// The unrelated modified feature.txt MUST be kept.
	if !strings.Contains(diff, "diff --git a/feature.txt b/feature.txt") || !strings.Contains(diff, "+new feature") {
		t.Fatalf("filtered tracked diff dropped unrelated modified tracked feature.txt (correlation broken):\n%s", diff)
	}
}
