package review

import (
	"encoding/json"
	"fmt"
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

// TestSafeSummaryScanRejectsKeyValueDelimitedRefusalKinds codifies the D4 /
// R16 round-10 fix (codex finding E): a raw-artifact marker written in a
// common key/value form (`kind=raw_prompt`, `artifact=codex_log`) must be
// detected. The previous boundary set treated '=' as a non-boundary byte, so
// the token to the right of '=' was treated as embedded in a larger
// identifier and `Seal` accepted it, letting `BuildReworkPrompt` render the
// raw marker into the next prompt. '=' is now a boundary (it does not appear
// in path segments or identifiers, so this introduces no path false
// positives — docs/raw_prompt.md remains safe via the '.' handling).
func TestSafeSummaryScanRejectsKeyValueDelimitedRefusalKinds(t *testing.T) {
	cases := []struct {
		name  string
		field string
		form  string // e.g. "kind=%s" -> "kind=raw_prompt"
		kind  string
	}{
		{"summary kind=raw_prompt", "summary", "kind=%s", "raw_prompt"},
		{"summary artifact=codex_log", "summary", "artifact=%s", "codex_log"},
		{"tests ref=prompt_snapshot", "tests", "ref=%s", "prompt_snapshot"},
		{"risks source=secret_artifact", "risks", "source=%s", "secret_artifact"},
		{"how_to_continue type=codex_events", "how_to_continue", "type=%s", "codex_events"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			marker := fmt.Sprintf(tc.form, tc.kind)
			s := &SafeSummary{ReviewPacketID: "rp_1", Status: "generated", Summary: "ok", HowToContinue: "continue"}
			switch tc.field {
			case "summary":
				s.Summary = "note: " + marker + " here"
			case "tests":
				s.Tests = []string{"check " + marker}
			case "risks":
				s.Risks = []string{"saw " + marker}
			case "how_to_continue":
				s.HowToContinue = "review " + marker + " next"
			}
			if err := s.Seal(); err == nil {
				t.Fatalf("Seal accepted key/value-delimited raw artifact marker %q in field %s", marker, tc.field)
			}
		})
	}
	// Counterpoint: a legitimate path like docs/raw_prompt.md must STILL be
	// accepted (the '=' boundary change must not break path handling).
	s := &SafeSummary{
		ReviewPacketID: "rp_1", Status: "generated", Summary: "ok", HowToContinue: "continue",
		ChangedFiles: []string{"docs/raw_prompt.md"},
	}
	if err := s.Seal(); err != nil {
		t.Fatalf("Seal rejected legitimate path docs/raw_prompt.md (path false positive): %v", err)
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

func TestSafeSummaryAllowsBlockedWordsInPathLikeText(t *testing.T) {
	s := &SafeSummary{
		ReviewPacketID: "rp_path_text",
		Status:         "generated",
		Summary:        "Reviewed docs/raw_prompt.md and prompt_context.json without exposing raw artifacts.",
		Tests:          []string{"go test ./internal/review ./docs/raw_prompt.md"},
		Risks:          []string{"secrets.txt is a changed file name, not an artifact marker."},
		Verification:   []string{"Checked prompt_context.json metadata path handling."},
		ChangedFiles: []string{
			"docs/raw_prompt.md",
			"secrets.txt",
			"prompt_context.json",
		},
		HowToContinue: "Review the path-like file names only.",
	}
	if err := s.Seal(); err != nil {
		t.Fatalf("Seal rejected path-like text containing blocklisted substrings: %v", err)
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

// TestBuildSafeSummaryPreservesHandoffSummaryWithMarkdownHeadings
// verifies D4 / R16 finding #5: when handoff.summary contains a `## `
// sub-heading (e.g. "Implemented parser\n\n## Edge cases\n- foo"), the
// safe summary MUST preserve the full value. The previous
// implementation overwrote summary.Summary by re-reading review.md's
// `## Summary` section and truncating it at the first `\n## `
// sub-heading, dropping "## Edge cases..." and leaving only
// "Implemented parser". review.json handoff.summary is now the sole
// source of truth for the summary field.
func TestBuildSafeSummaryPreservesHandoffSummaryWithMarkdownHeadings(t *testing.T) {
	st := newReviewTestStore(t)
	issue, run := prepareReviewRun(t, st)
	// Generate a real packet so the schema and foreign keys are wired
	// up and review.md / review.json exist on disk.
	if _, err := (Generator{Store: st}).Generate(run.ID); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	root := filepath.Join(st.RepoRoot, ".symphony", "artifacts", issue.Identifier, run.ID)
	jpath := filepath.Join(root, "review.json")
	mdpath := filepath.Join(root, "review.md")

	fullSummary := "Implemented parser\n\n## Edge cases\n- foo"
	// Inject the sub-heading-bearing summary into review.json's
	// handoff.summary (the structured source of truth).
	data, err := os.ReadFile(jpath)
	if err != nil {
		t.Fatalf("read review.json: %v", err)
	}
	var packet map[string]any
	if err := json.Unmarshal(data, &packet); err != nil {
		t.Fatalf("unmarshal review.json: %v", err)
	}
	handoff, _ := packet["handoff"].(map[string]any)
	handoff["summary"] = fullSummary
	packet["handoff"] = handoff
	jb, _ := json.MarshalIndent(packet, "", "  ")
	if err := os.WriteFile(jpath, jb, 0o644); err != nil {
		t.Fatalf("write review.json: %v", err)
	}
	// Mirror renderMarkdown's output so review.md's `## Summary` section
	// contains the same sub-heading-bearing text. The OLD extractSection
	// would truncate this at the first `\n## ` (i.e. before
	// "## Edge cases"), yielding only "Implemented parser".
	md, err := os.ReadFile(mdpath)
	if err != nil {
		t.Fatalf("read review.md: %v", err)
	}
	mdString := string(md)
	if !strings.Contains(mdString, "## Summary") {
		t.Fatalf("review.md missing ## Summary section: %s", mdString)
	}
	mdString = strings.Replace(mdString, "## Summary\nready for review", "## Summary\n"+fullSummary, 1)
	if err := os.WriteFile(mdpath, []byte(mdString), 0o644); err != nil {
		t.Fatalf("write review.md: %v", err)
	}

	summary, err := BuildSafeSummaryFromRun(st, run.ID)
	if err != nil {
		t.Fatalf("BuildSafeSummaryFromRun: %v", err)
	}
	if summary.Summary != fullSummary {
		t.Fatalf("summary.Summary = %q, want %q (full handoff.summary must be preserved, not truncated at first markdown sub-heading)", summary.Summary, fullSummary)
	}
}

// TestSafeSummaryEscapesChangedFilePathsBeforeMarkdown (R14-2) verifies
// that a Git-valid changed-file path containing a control character —
// e.g. a newline that would otherwise emit `raw_prompt` as its own
// prompt line — is neutralized before being rendered into the Rework
// prompt. ChangedFiles entries are excluded from the refusal-kind scan
// (they are paths, not artifact-kind labels), so the renderer itself
// must escape control bytes so a crafted filename cannot inject a
// marker or break the markdown structure.
func TestSafeSummaryEscapesChangedFilePathsBeforeMarkdown(t *testing.T) {
	s := &SafeSummary{
		ReviewPacketID: "rp_escape",
		Status:         "generated",
		Summary:        "Changed a file whose name embeds a marker.",
		ChangedFiles: []string{
			"docs/x\nraw_prompt",
			"normal/path.go",
		},
		HowToContinue: "Review the escaped paths.",
	}
	if err := s.Seal(); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	md, err := s.ToMarkdown()
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	// The raw, unescaped path must not appear: the embedded newline
	// would render `raw_prompt` as its own line in the prompt.
	if strings.Contains(md, "docs/x\nraw_prompt") {
		t.Fatalf("markdown emitted unescaped newline-bearing path: %q", md)
	}
	// The newline control char must be replaced with a visible escape
	// sequence (backslash-u-000a) so it cannot start a new prompt line.
	if !strings.Contains(md, "docs/x\\u000araw_prompt") {
		t.Fatalf("markdown did not escape the newline control char: %q", md)
	}
	// `raw_prompt` must not appear as a stand-alone line (which would
	// re-inject the raw-artifact marker into the prompt).
	for _, line := range strings.Split(md, "\n") {
		if strings.TrimSpace(line) == "raw_prompt" {
			t.Fatalf("markdown rendered raw_prompt as a stand-alone line: %q", md)
		}
	}
	// The legitimate path is still rendered (escaped form wraps it in
	// backticks but the path text must remain reachable).
	if !strings.Contains(md, "normal/path.go") {
		t.Fatalf("markdown dropped legitimate path: %q", md)
	}
	// The escaped path is wrapped in backticks so it renders literally.
	if !strings.Contains(md, "`docs/x") {
		t.Fatalf("markdown did not wrap escaped path in backticks: %q", md)
	}
}

// TestSafeSummaryEscapesDiffstatBeforeRendering (R17-3) verifies that a
// diffstat containing a backtick-bearing or control-char-bearing filename
// cannot close the diffstat code fence or inject a stand-alone marker line
// into the Rework prompt. Diffstat rows are excluded from the refusal-kind
// scan (they contain file paths), so the renderer must escape them.
func TestSafeSummaryEscapesDiffstatBeforeRendering(t *testing.T) {
	s := &SafeSummary{
		ReviewPacketID: "rp_diffstat",
		Status:         "generated",
		Summary:        "Changed a backtick-bearing file.",
		Diffstat:       "1\t0\t`raw_prompt\n",
		HowToContinue:  "Review the escaped diffstat.",
	}
	if err := s.Seal(); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	md, err := s.ToMarkdown()
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	// The raw newline-bearing diffstat must not appear verbatim (the
	// embedded newline would inject `raw_prompt` as a stand-alone line).
	if strings.Contains(md, "1\t0\t`raw_prompt\n") {
		t.Fatalf("markdown emitted unescaped diffstat: %q", md)
	}
	// `raw_prompt` must not render as a stand-alone prompt line.
	for _, line := range strings.Split(md, "\n") {
		if strings.TrimSpace(line) == "raw_prompt" {
			t.Fatalf("markdown rendered raw_prompt as a stand-alone line: %q", md)
		}
	}
	// The diffstat fence must use more than one backtick (the content
	// contains a backtick, so a single-backtick fence would close early).
	if !strings.Contains(md, "``") {
		t.Fatalf("markdown did not use a multi-backtick diffstat fence: %q", md)
	}
}

// Git-valid changed-file path containing a literal backtick cannot
// close the single-backtick code-span wrapper and leak a raw-artifact
// marker. Round 14 used a single-backtick wrapper with backslash-escaped
// internal backticks, but backslashes are literal inside a CommonMark
// code span, so `` `raw_prompt `` would close the span and render the
// marker as prompt text. The fix uses an N+1 backtick fence.
func TestSafeSummaryEscapesBacktickBearingFilePaths(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		leaked  string // substring that must NOT render as stand-alone prose
	}{
		{"leading backtick + marker", "`raw_prompt", "raw_prompt"},
		{"backtick before marker", "x`raw_prompt", "raw_prompt"},
		{"two backticks + marker", "``raw_prompt", "raw_prompt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &SafeSummary{
				ReviewPacketID: "rp_bt",
				Status:         "generated",
				Summary:        "Changed a backtick-bearing file name.",
				ChangedFiles:   []string{tc.path},
				HowToContinue:  "Review the escaped paths.",
			}
			if err := s.Seal(); err != nil {
				t.Fatalf("Seal: %v", err)
			}
			md, err := s.ToMarkdown()
			if err != nil {
				t.Fatalf("ToMarkdown: %v", err)
			}
			// The marker must never render as a stand-alone prompt
			// line (which would re-inject the raw-artifact marker).
			for _, line := range strings.Split(md, "\n") {
				if strings.TrimSpace(line) == tc.leaked {
					t.Fatalf("marker %q rendered as a stand-alone line: %q", tc.leaked, md)
				}
			}
			// The wrapper must use strictly more backticks than the
			// longest backtick run in the path so it cannot be closed
			// by an interior backtick. Compute the longest run.
			longest := 0
			run := 0
			for i := 0; i < len(tc.path); i++ {
				if tc.path[i] == '`' {
					run++
					if run > longest {
						longest = run
					}
				} else {
					run = 0
				}
			}
			expectedFence := strings.Repeat("`", longest+1)
			// The rendered markdown must contain an opening fence of
			// at least expectedFence length.
			if !strings.Contains(md, expectedFence) {
				t.Fatalf("markdown missing %d-backtick fence (got: %q)", longest+1, md)
			}
			// No backslash-escaped backtick should remain (round-14
			// behavior); the N+1 fence handles backticks without
			// backslash escapes.
			if strings.Contains(md, "\\`") {
				t.Fatalf("markdown used backslash-escaped backtick (R15-1 regression): %q", md)
			}
		})
	}
}

// TestBuildSafeSummaryHandlesMalformedGitMetadata (R14-3) verifies that
// a review.json which is valid JSON but lacks an object-valued `git`
// field does not panic during safe summary projection. A manually-edited
// or partially-migrated packet artifact previously triggered a chained
// type assertion panic (`packet["git"].(map[string]any)["head_sha"]`)
// that took down the worker during Send-to-Rework; the projection must
// degrade to a missing head_sha instead.
func TestBuildSafeSummaryHandlesMalformedGitMetadata(t *testing.T) {
	st := newReviewTestStore(t)
	issue, run := prepareReviewRun(t, st)
	if _, err := (Generator{Store: st}).Generate(run.ID); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	root := filepath.Join(st.RepoRoot, ".symphony", "artifacts", issue.Identifier, run.ID)
	jpath := filepath.Join(root, "review.json")
	data, err := os.ReadFile(jpath)
	if err != nil {
		t.Fatalf("read review.json: %v", err)
	}
	var packet map[string]any
	if err := json.Unmarshal(data, &packet); err != nil {
		t.Fatalf("unmarshal review.json: %v", err)
	}
	// Corrupt the `git` field so it is no longer an object. A panic
	// here surfaces as a failing test (not a hang), proving the guard.
	packet["git"] = "not-an-object"
	// Also test the missing-field case by removing it entirely in a
	// sub-check; the comma-ok guard must handle both.
	jb, _ := json.Marshal(packet)
	if err := os.WriteFile(jpath, jb, 0o644); err != nil {
		t.Fatalf("write review.json: %v", err)
	}
	summary, err := BuildSafeSummaryFromRun(st, run.ID)
	if err != nil {
		t.Fatalf("BuildSafeSummaryFromRun returned error for malformed git metadata (want graceful degrade): %v", err)
	}
	if summary.HeadSHA != "" {
		t.Fatalf("HeadSHA = %q, want empty when git metadata is malformed", summary.HeadSHA)
	}

	// Also cover the entirely-missing `git` field.
	delete(packet, "git")
	jb, _ = json.Marshal(packet)
	if err := os.WriteFile(jpath, jb, 0o644); err != nil {
		t.Fatalf("write review.json (no git): %v", err)
	}
	summary2, err := BuildSafeSummaryFromRun(st, run.ID)
	if err != nil {
		t.Fatalf("BuildSafeSummaryFromRun returned error for missing git metadata (want graceful degrade): %v", err)
	}
	if summary2.HeadSHA != "" {
		t.Fatalf("HeadSHA = %q, want empty when git metadata is absent", summary2.HeadSHA)
	}
}
