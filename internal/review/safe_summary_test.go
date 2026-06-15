package review

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"local-symphony/internal/core"
)

func TestSafeSummarySealIsDeterministicAndExcludesRawRefusalKinds(t *testing.T) {
	s := &SafeSummary{
		ReviewPacketID:   "rp_1",
		PacketNo:         1,
		RunID:            "run_1",
		SourceIssueState: string(core.StateReady),
		Status:           "generated",
		Summary:          "ready for review",
		Acceptance:       []string{"acceptance-1"},
		Tests:            []string{"go test"},
		Risks:            []string{"r1"},
		Verification:     []string{"manual"},
		Followups:        nil,
		ChangedFiles:     []string{"x.txt"},
	}
	if err := s.Seal(); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if s.SafeSummarySHA256 == "" {
		t.Fatal("Seal did not populate SafeSummarySHA256")
	}
	if strings.Contains(s.SafeSummarySHA256, " ") {
		t.Fatalf("Seal produced suspicious hash: %q", s.SafeSummarySHA256)
	}
	// Deterministic: re-seal and compare.
	hash1 := s.SafeSummarySHA256
	if err := s.Seal(); err != nil {
		t.Fatalf("Seal (2): %v", err)
	}
	if s.SafeSummarySHA256 != hash1 {
		t.Fatalf("Seal is non-deterministic: %s vs %s", hash1, s.SafeSummarySHA256)
	}
}

func TestSafeSummaryScanForRawArtifactsRejectsRefusalKindTokens(t *testing.T) {
	// PR #27 / D4 F5: the raw-artifact blocklist is matched at
	// strict token boundaries (whitespace or sentence punctuation).
	// A path-like ChangedFiles entry such as "raw_prompt/x.txt"
	// is NOT a raw artifact marker — the substring is embedded in
	// a larger identifier. We assert whitespace-bounded prose
	// (e.g. "leaked <kind> token") still trips the blocklist, and
	// the path-bounded case is covered by
	// TestSafeSummaryAllowsSecretPathAsChangedFile above.
	cases := []struct {
		name  string
		field string
		kind  string
	}{
		{"summary with codex_log marker", "summary", "codex_log"},
		{"tests with prompt_snapshot marker", "tests", "prompt_snapshot"},
		{"risks with secret_artifact marker", "risks", "secret_artifact"},
		{"changed_files with raw_prompt marker", "changed_files", "raw_prompt"},
		{"diffstat with prompt_rendered marker", "diffstat", "prompt_rendered"},
		{"how_to_continue with codex_events marker", "how_to_continue", "codex_events"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &SafeSummary{ReviewPacketID: "rp_1", Status: "generated", Summary: "ok", HowToContinue: "continue"}
			switch tc.field {
			case "summary":
				s.Summary = "leaked " + tc.kind + " token"
			case "tests":
				s.Tests = []string{"test: " + tc.kind + " seen"}
			case "risks":
				s.Risks = []string{"risk: " + tc.kind}
			case "changed_files":
				// Whitespace-bounded prose inside a ChangedFiles
				// entry is still a leak; the path-like
				// "raw_prompt/x.txt" case is asserted in
				// TestSafeSummaryAllowsSecretPathAsChangedFile.
				s.ChangedFiles = []string{tc.kind + " helper.go"}
			case "diffstat":
				s.Diffstat = tc.kind + " line"
			case "how_to_continue":
				s.HowToContinue = "do " + tc.kind + " now"
			}
			if err := s.Seal(); err == nil {
				t.Fatalf("Seal accepted raw artifact marker %q in field %s", tc.kind, tc.field)
			}
		})
	}
}

func TestSafeSummaryToMarkdownIncludesReasonSafeMetadata(t *testing.T) {
	s := &SafeSummary{
		ReviewPacketID:   "rp_test_1",
		PacketNo:         1,
		RunID:            "run_test_1",
		SourceIssueState: string(core.StateReady),
		Status:           "generated",
		Summary:          "Implemented greeting helper",
		Acceptance:       []string{"Acceptance-1"},
		Tests:            []string{"go test ./..."},
		Risks:            []string{"Low risk"},
		Verification:     []string{"Manual smoke test"},
		Followups:        []string{"Followup-1"},
		ChangedFiles:     []string{"internal/greet/greet.go"},
		ToolCallCount:    3,
		ApprovalCount:    1,
		HowToContinue:    "Use Send to Rework with a reason, or Mark Done with an acceptance reason.",
	}
	if err := s.Seal(); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	md, err := s.ToMarkdown()
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	wants := []string{
		"# Previous Review Packet (Safe Summary)",
		"rp_test_1",
		"Implemented greeting helper",
		"Acceptance-1",
		"go test ./...",
		"internal/greet/greet.go",
		"Tool calls observed: 3",
		"Approvals observed: 1",
		"Use Send to Rework",
	}
	for _, want := range wants {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown missing %q\n%s", want, md)
		}
	}
	// The hash must not leak into the markdown rendering: the hash is
	// metadata only, not a piece of prompt prose.
	if strings.Contains(md, s.SafeSummarySHA256) {
		t.Fatalf("markdown leaked SafeSummarySHA256 hash: %s", md)
	}
	// The rendered markdown must never embed refusal-kind tokens; we
	// already covered Seal, but ToMarkdown also re-runs the scan.
	if strings.Contains(md, "codex_log") {
		t.Fatalf("markdown contains codex_log marker: %s", md)
	}
}

func TestSafeSummaryScanDoesNotFalsePositiveOnLegitimateText(t *testing.T) {
	s := &SafeSummary{
		ReviewPacketID: "rp_x",
		Status:         "generated",
		Summary:        "Reviewed the codex launch flow and patched the approval loop.",
		Acceptance:     []string{"Verified end-to-end with smoke test."},
		Tests:          []string{"go test ./..."},
		Risks:          []string{"None observed."},
		Verification:   []string{"Manual smoke test"},
		ChangedFiles:   []string{"internal/agent/codex/codex.go"},
		HowToContinue:  "Mark Done after final review.",
	}
	if err := s.Seal(); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	md, err := s.ToMarkdown()
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if !strings.Contains(md, "codex/codex.go") {
		t.Fatalf("markdown dropped legitimate path: %s", md)
	}
}

func TestSafeSummaryAllowsBlockedWordsInsideLegitimatePaths(t *testing.T) {
	s := &SafeSummary{
		ReviewPacketID: "rp_paths",
		Status:         "generated",
		Summary:        "Reviewed path-only changes.",
		Tests:          []string{"go test ./internal/review"},
		Risks:          []string{"No raw artifacts included."},
		Verification:   []string{"Checked rework-safe path names."},
		ChangedFiles: []string{
			"internal/secrets/store.go",
			"ui/prompt_snapshot_view.tsx",
		},
		HowToContinue: "Review the file path changes.",
	}
	if err := s.Seal(); err != nil {
		t.Fatalf("Seal rejected legitimate paths with blocklisted substrings: %v", err)
	}
}

func TestSafeSummaryRefusalKindBlocklistIsStable(t *testing.T) {
	// Lock the blocklist so a future edit cannot silently relax the
	// D4 / R16 redaction boundary.
	want := []string{
		"codex_events",
		"codex_final_dump",
		"codex_log",
		"prompt_context",
		"prompt_rendered",
		"prompt_snapshot",
		"raw_codex_log",
		"raw_prompt",
		"raw_prompt_log",
		"secret_artifact",
		"secrets",
	}
	got := refusalKindBlocklist()
	if len(got) != len(want) {
		t.Fatalf("refusal-kind blocklist length = %d, want %d (got %v)", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("refusal-kind blocklist[%d] = %q, want %q (full: %v)", i, got[i], w, got)
		}
	}
}

func TestBuildSafeSummaryFromRunHydratesFromArtifacts(t *testing.T) {
	st := newReviewTestStore(t)
	_, run := prepareReviewRun(t, st)
	if _, err := (Generator{Store: st}).Generate(run.ID); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	summary, err := BuildSafeSummaryFromRun(st, run.ID)
	if err != nil {
		t.Fatalf("BuildSafeSummaryFromRun: %v", err)
	}
	if summary.ReviewPacketID == "" {
		t.Fatal("summary.ReviewPacketID is empty")
	}
	if summary.PacketNo != 1 {
		t.Fatalf("summary.PacketNo = %d, want 1", summary.PacketNo)
	}
	if summary.SourceIssueState != string(core.StateReady) {
		t.Fatalf("SourceIssueState = %q, want Ready", summary.SourceIssueState)
	}
	if summary.Summary == "" {
		t.Fatal("summary.Summary is empty; expected hydrated from handoff")
	}
	if summary.SafeSummarySHA256 == "" {
		t.Fatal("SafeSummarySHA256 is empty after Seal")
	}
	// Banned: do not surface raw artifact kind markers from the
	// handoff / test fixture text.
	if strings.Contains(summary.Summary, "codex_log") {
		t.Fatalf("safe summary leaked codex_log marker: %s", summary.Summary)
	}
}

func TestBuildSafeSummaryFromRunPropagatesBaseAndBranch(t *testing.T) {
	st := newReviewTestStore(t)
	_, run := prepareReviewRun(t, st)
	if _, err := (Generator{Store: st}).Generate(run.ID); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	summary, err := BuildSafeSummaryFromRun(st, run.ID)
	if err != nil {
		t.Fatalf("BuildSafeSummaryFromRun: %v", err)
	}
	if summary.BranchName != "review-test" {
		t.Fatalf("BranchName = %q, want review-test", summary.BranchName)
	}
	if summary.BaseRef != "main" {
		t.Fatalf("BaseRef = %q, want main", summary.BaseRef)
	}
	if summary.BaseRefConfig != "auto" {
		t.Fatalf("BaseRefConfig = %q, want auto", summary.BaseRefConfig)
	}
	if summary.BaseSHA != "base-sha" {
		t.Fatalf("BaseSHA = %q, want base-sha", summary.BaseSHA)
	}
}

func TestBuildSafeSummaryRendersValidJSON(t *testing.T) {
	st := newReviewTestStore(t)
	_, run := prepareReviewRun(t, st)
	if _, err := (Generator{Store: st}).Generate(run.ID); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	summary, err := BuildSafeSummaryFromRun(st, run.ID)
	if err != nil {
		t.Fatalf("BuildSafeSummaryFromRun: %v", err)
	}
	jb, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var roundtrip map[string]any
	if err := json.Unmarshal(jb, &roundtrip); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, key := range []string{"review_packet_id", "summary", "tests", "risks", "verification", "changed_files", "safe_summary_sha256"} {
		if _, ok := roundtrip[key]; !ok {
			t.Fatalf("json missing key %q", key)
		}
	}
}

// TestReworkSafeSummaryUsesPreviousRunFields verifies that when a
// Rework dispatch (current run has no review packet yet) is being
// prepared and the orchestrator passes a `prev` pointer to
// BuildSafeSummaryFromIssue, the resulting summary carries the
// *previous* run's source_issue_state and run_id, not the current
// run's. The previous implementation always read the current
// run's SourceIssueState, so the first Rework after a Ready run
// rendered a safe summary stamped with the current Rework state —
// corrupting the snapshot metadata used by downstream diagnostics.
func TestReworkSafeSummaryUsesPreviousRunFields(t *testing.T) {
	st := newReviewTestStore(t)
	issue, prev := prepareReviewRun(t, st)
	if _, err := (Generator{Store: st}).Generate(prev.ID); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// Force prev's source_issue_state to a known value so we can
	// assert it is preserved on the rework safe summary.
	if err := st.Project.Exec(`UPDATE run_attempts SET source_issue_state=? WHERE id=?`, string(core.StateReady), prev.ID); err != nil {
		t.Fatalf("force prev SourceIssueState: %v", err)
	}
	// Move the issue into Human Review and Rework so a new run can
	// be claimed for the rework dispatch. CompleteRunWithReview is
	// the orchestrator's review-packet-completion path; it
	// transitions Working -> Human Review and is the state SendToRework
	// requires as its precondition.
	rpID := mustReviewPacketIDForRun(t, st, prev.ID)
	if err := st.CompleteRunWithReview(prev.ID, rpID); err != nil {
		t.Fatalf("CompleteRunWithReview: %v", err)
	}
	if _, err := st.SendToRework(issue.ID, "rework test"); err != nil {
		t.Fatalf("SendToRework: %v", err)
	}
	cur, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	if err := st.Project.Exec(`UPDATE run_attempts SET source_issue_state=? WHERE id=?`, string(core.StateRework), cur.ID); err != nil {
		t.Fatalf("force cur SourceIssueState: %v", err)
	}
	curLoaded, err := st.GetRun(cur.ID)
	if err != nil {
		t.Fatalf("GetRun cur: %v", err)
	}
	issue, err = st.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	// Pass prev into the safe summary builder; it should pick up
	// the *previous* run's source_issue_state.
	summary, err := BuildSafeSummaryFromIssueWithPrev(st, issue, curLoaded, prev)
	if err != nil {
		t.Fatalf("BuildSafeSummaryFromIssueWithPrev: %v", err)
	}
	if summary.SourceIssueState != string(core.StateReady) {
		t.Fatalf("SourceIssueState = %q, want %q (rework safe summary must use prev run fields)", summary.SourceIssueState, core.StateReady)
	}
	if summary.RunID != prev.ID {
		t.Fatalf("RunID = %q, want %q (rework safe summary must use prev run id)", summary.RunID, prev.ID)
	}
}

func TestReworkSafeSummaryRequiresPacketForSelectedPreviousRun(t *testing.T) {
	st := newReviewTestStore(t)
	issue, reviewedRun := prepareReviewRun(t, st)
	if _, err := (Generator{Store: st}).Generate(reviewedRun.ID); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	prevWithoutPacket := &core.RunAttempt{
		ID:               core.NewID("run_"),
		IssueID:          issue.ID,
		Status:           core.RunCompleted,
		SourceIssueState: core.StateReady,
	}
	cur := &core.RunAttempt{
		ID:               core.NewID("run_"),
		IssueID:          issue.ID,
		Status:           core.RunRenderingPrompt,
		SourceIssueState: core.StateRework,
	}
	if _, err := BuildSafeSummaryFromIssueWithPrev(st, issue, cur, prevWithoutPacket); err == nil {
		t.Fatal("BuildSafeSummaryFromIssueWithPrev used another run's packet for a previous run with no review packet")
	}
}

func TestBuildSafeSummaryRejectsReviewPacketsWithRawRefusalKinds(t *testing.T) {
	// Synthetic packet that smuggles a raw artifact marker into the
	// handoff summary. The extractor must reject it.
	st := newReviewTestStore(t)
	issue, run := prepareReviewRun(t, st)
	// First generate a real packet so the schema and foreign keys are
	// wired up.
	if _, err := (Generator{Store: st}).Generate(run.ID); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	root := filepath.Join(st.RepoRoot, ".symphony", "artifacts", issue.Identifier, run.ID)
	jpath := filepath.Join(root, "review.json")
	mdpath := filepath.Join(root, "review.md")
	// Inject the marker into both handoff.summary (review.json) and
	// the rendered review.md so the extractor trips regardless of
	// which artifact it picks up first.
	data, err := os.ReadFile(jpath)
	if err != nil {
		t.Fatalf("read review.json: %v", err)
	}
	var packet map[string]any
	if err := json.Unmarshal(data, &packet); err != nil {
		t.Fatalf("unmarshal review.json: %v", err)
	}
	handoff, _ := packet["handoff"].(map[string]any)
	handoff["summary"] = "leaked codex_log marker inside summary"
	packet["handoff"] = handoff
	jb, _ := json.MarshalIndent(packet, "", "  ")
	if err := os.WriteFile(jpath, jb, 0o644); err != nil {
		t.Fatalf("write review.json: %v", err)
	}
	md, err := os.ReadFile(mdpath)
	if err != nil {
		t.Fatalf("read review.md: %v", err)
	}
	mdString := string(md)
	if !strings.Contains(mdString, "## Summary") {
		t.Fatalf("review.md missing ## Summary section: %s", mdString)
	}
	mdString = strings.Replace(mdString, "## Summary\nready for review", "## Summary\nleaked codex_log marker inside summary", 1)
	if err := os.WriteFile(mdpath, []byte(mdString), 0o644); err != nil {
		t.Fatalf("write review.md: %v", err)
	}
	if _, err := BuildSafeSummaryFromRun(st, run.ID); err == nil {
		t.Fatal("BuildSafeSummaryFromRun accepted review.json with codex_log marker")
	}
}
