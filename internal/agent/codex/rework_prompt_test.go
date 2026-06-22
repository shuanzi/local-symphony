package codex

import (
	"strings"
	"testing"

	"local-symphony/internal/core"
	"local-symphony/internal/review"
)

func TestBuildReworkPromptIncludesLatestReviewReasonAndSafeSummary(t *testing.T) {
	summary := &review.SafeSummary{
		ReviewPacketID:   "rp_1",
		PacketNo:         1,
		SourceIssueState: string(core.StateReady),
		Status:           "generated",
		Summary:          "Implemented greeting helper",
		Acceptance:       []string{"Acceptance-1"},
		Tests:            []string{"go test ./..."},
		Risks:            []string{"Low risk"},
		Verification:     []string{"Manual smoke test"},
		HowToContinue:    "Use Send to Rework with a reason, or Mark Done with an acceptance reason.",
	}
	if err := summary.Seal(); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	in := ReworkContextInput{
		Run:               &core.RunAttempt{ID: "run_2", IssueID: "iss_1", SourceIssueState: core.StateRework},
		PreviousRun:       &core.RunAttempt{ID: "run_1", IssueID: "iss_1", Status: core.RunCompleted},
		ReviewPacketID:    "rp_1",
		ReviewReason:      "Cover edge case for empty input",
		SafeSummary:       summary,
		WorkspacePath:     "/tmp/workspace",
		BaseSHA:           "base-sha-1",
		CumulativeDiffSHA: "diff-sha-1",
	}
	base := "BASE PROMPT BODY"
	prompt, hash, err := BuildReworkPrompt(base, in)
	if err != nil {
		t.Fatalf("BuildReworkPrompt: %v", err)
	}
	if !strings.Contains(prompt, in.ReviewReason) {
		t.Fatalf("prompt missing review reason: %q", prompt)
	}
	if !strings.Contains(prompt, "Previous Review Packet (Safe Summary)") {
		t.Fatalf("prompt missing safe summary header: %q", prompt)
	}
	if !strings.Contains(prompt, "Implemented greeting helper") {
		t.Fatalf("prompt missing safe summary prose: %q", prompt)
	}
	if !strings.Contains(prompt, "Acceptance-1") {
		t.Fatalf("prompt missing acceptance criteria: %q", prompt)
	}
	if !strings.Contains(prompt, "base-sha-1") {
		t.Fatalf("prompt missing base sha: %q", prompt)
	}
	if !strings.Contains(prompt, "diff-sha-1") {
		t.Fatalf("prompt missing cumulative diff sha: %q", prompt)
	}
	if !strings.HasPrefix(prompt, base) {
		t.Fatalf("prompt does not start with base prompt: %q", prompt)
	}
	if hash == "" {
		t.Fatal("prompt hash is empty")
	}
}

func TestBuildReworkPromptRejectsEmptyReviewReason(t *testing.T) {
	summary := &review.SafeSummary{ReviewPacketID: "rp_1", Status: "generated", Summary: "ok", HowToContinue: "continue"}
	if err := summary.Seal(); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	in := ReworkContextInput{
		Run: &core.RunAttempt{ID: "run_2", IssueID: "iss_1", SourceIssueState: core.StateRework},
		SafeSummary: summary,
	}
	if _, _, err := BuildReworkPrompt("base", in); err == nil {
		t.Fatal("BuildReworkPrompt accepted empty review reason")
	}
}

func TestBuildReworkPromptRejectsNilSafeSummary(t *testing.T) {
	in := ReworkContextInput{
		Run: &core.RunAttempt{ID: "run_2", IssueID: "iss_1", SourceIssueState: core.StateRework},
		ReviewReason: "redo it",
	}
	if _, _, err := BuildReworkPrompt("base", in); err == nil {
		t.Fatal("BuildReworkPrompt accepted nil safe summary")
	}
}

func TestBuildReworkPromptRejectsRawRefusalKindLeak(t *testing.T) {
	summary := &review.SafeSummary{ReviewPacketID: "rp_1", Status: "generated", Summary: "leaked codex_log marker", HowToContinue: "continue"}
	if err := summary.Seal(); err == nil {
		t.Fatal("Seal accepted refusal kind marker")
	}
}

// TestBuildReworkPromptRejectsRawMarkersInReviewReason (R14-1) verifies
// that the operator's Send-to-Rework reason is scanned for raw-artifact
// markers before it is rendered into the next agent prompt or persisted
// on the rework_snapshots row. A reason containing a blocked marker
// (e.g. `kind=raw_prompt` or a stand-alone `codex_log` token) must be
// rejected just like a safe summary containing the same marker.
func TestBuildReworkPromptRejectsRawMarkersInReviewReason(t *testing.T) {
	summary := &review.SafeSummary{ReviewPacketID: "rp_1", Status: "generated", Summary: "ok", HowToContinue: "continue"}
	if err := summary.Seal(); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	cases := []string{
		"kind=raw_prompt",
		"artifact=codex_log",
		"please reuse the codex_log from the last run",
		"raw_prompt was leaked",
	}
	for _, reason := range cases {
		in := ReworkContextInput{
			Run:           &core.RunAttempt{ID: "run_2", IssueID: "iss_1", SourceIssueState: core.StateRework},
			ReviewReason:  reason,
			SafeSummary:   summary,
			BaseSHA:       "b1",
			CumulativeDiffSHA: "d1",
		}
		if _, _, err := BuildReworkPrompt("base", in); err == nil {
			t.Fatalf("BuildReworkPrompt accepted review reason with raw marker %q", reason)
		}
	}
}

// TestBuildReworkPromptAcceptsCleanReviewReason (R14-1 companion)
// verifies that an ordinary rework reason — including one that mentions
// the word "codex" or a path segment that merely contains a blocked
// substring — is NOT rejected by the refusal-kind scan.
func TestBuildReworkPromptAcceptsCleanReviewReason(t *testing.T) {
	summary := &review.SafeSummary{ReviewPacketID: "rp_1", Status: "generated", Summary: "ok", HowToContinue: "continue"}
	if err := summary.Seal(); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	cases := []string{
		"Cover edge case for empty input",
		"Rerun the codex launch smoke test",
		"Update internal/codex_log_reader.go to handle EOF",
		"The raw_prompt_handler needs a fix",
	}
	for _, reason := range cases {
		in := ReworkContextInput{
			Run:           &core.RunAttempt{ID: "run_2", IssueID: "iss_1", SourceIssueState: core.StateRework},
			ReviewReason:  reason,
			SafeSummary:   summary,
			BaseSHA:       "b1",
			CumulativeDiffSHA: "d1",
		}
		prompt, _, err := BuildReworkPrompt("base", in)
		if err != nil {
			t.Fatalf("BuildReworkPrompt rejected clean reason %q: %v", reason, err)
		}
		if !strings.Contains(prompt, reason) {
			t.Fatalf("prompt missing clean review reason %q: %q", reason, prompt)
		}
	}
}

func TestBuildReworkSnapshotRecordPopulatesExpectedFields(t *testing.T) {
	summary := &review.SafeSummary{ReviewPacketID: "rp_1", Status: "generated", Summary: "ok", HowToContinue: "continue"}
	if err := summary.Seal(); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	run := &core.RunAttempt{ID: "run_2", IssueID: "iss_1", SourceIssueState: core.StateRework}
	prev := &core.RunAttempt{ID: "run_1", IssueID: "iss_1"}
	issue := &core.Issue{ID: "iss_1", BaseRef: core.StringPtr("auto"), BaseSHA: core.StringPtr("base-sha-x")}
	in := ReworkContextInput{
		Issue:             issue,
		Run:               run,
		PreviousRun:       prev,
		ReviewPacketID:    "rp_1",
		ReviewReason:      "redo",
		SafeSummary:       summary,
		BaseSHA:           "base-sha-x",
		CumulativeDiffSHA: "diff-sha-y",
	}
	rec := BuildReworkSnapshotRecord("prompt-hash", in, "ps_x")
	if rec.RunID != "run_2" {
		t.Fatalf("RunID = %q, want run_2", rec.RunID)
	}
	if rec.PreviousRunID != "run_1" {
		t.Fatalf("PreviousRunID = %q, want run_1", rec.PreviousRunID)
	}
	if rec.ReviewPacketID != "rp_1" {
		t.Fatalf("ReviewPacketID = %q, want rp_1", rec.ReviewPacketID)
	}
	if rec.BaseSHA != "base-sha-x" {
		t.Fatalf("BaseSHA = %q, want base-sha-x", rec.BaseSHA)
	}
	if rec.CumulativeDiffSHA != "diff-sha-y" {
		t.Fatalf("CumulativeDiffSHA = %q, want diff-sha-y", rec.CumulativeDiffSHA)
	}
	if rec.PromptHash != "prompt-hash" {
		t.Fatalf("PromptHash = %q, want prompt-hash", rec.PromptHash)
	}
	if rec.SafeSummarySHA256 != summary.SafeSummarySHA256 {
		t.Fatalf("SafeSummarySHA256 = %q, want %q", rec.SafeSummarySHA256, summary.SafeSummarySHA256)
	}
}

func TestBuildReworkPromptHashIsDeterministicForSameInput(t *testing.T) {
	summary := &review.SafeSummary{ReviewPacketID: "rp_1", Status: "generated", Summary: "ok", HowToContinue: "continue", Acceptance: []string{"a"}}
	if err := summary.Seal(); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	in := ReworkContextInput{
		Run: &core.RunAttempt{ID: "run_2", IssueID: "iss_1", SourceIssueState: core.StateRework},
		PreviousRun: &core.RunAttempt{ID: "run_1", IssueID: "iss_1"},
		ReviewPacketID: "rp_1", ReviewReason: "redo", SafeSummary: summary,
		BaseSHA: "b1", CumulativeDiffSHA: "d1",
	}
	_, h1, err := BuildReworkPrompt("base", in)
	if err != nil {
		t.Fatalf("BuildReworkPrompt: %v", err)
	}
	_, h2, err := BuildReworkPrompt("base", in)
	if err != nil {
		t.Fatalf("BuildReworkPrompt (2): %v", err)
	}
	if h1 != h2 {
		t.Fatalf("prompt hash is non-deterministic: %s vs %s", h1, h2)
	}
}
