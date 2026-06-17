package review

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"local-symphony/internal/core"
	"local-symphony/internal/db"
	"local-symphony/internal/store"
)

// SafeSummary is the redacted, prompt-safe projection of a review packet.
//
// D4 / R16 contract: the fields below are the only payload that flows
// from a previous review packet into a Rework prompt. Raw prompt / raw
// Codex log / secret artifact content MUST NOT appear here. Downstream
// prompt renderers MUST consume this DTO only.
type SafeSummary struct {
	ReviewPacketID    string            `json:"review_packet_id"`
	PacketNo          int               `json:"packet_no"`
	RunID             string            `json:"run_id"`
	SourceIssueState  string            `json:"source_issue_state"`
	Status            string            `json:"status"`
	CreatedAt         string            `json:"created_at"`
	BranchName        string            `json:"branch_name,omitempty"`
	BaseRef           string            `json:"base_ref,omitempty"`
	BaseRefConfig     string            `json:"base_ref_config,omitempty"`
	BaseSHA           string            `json:"base_sha,omitempty"`
	HeadSHA           string            `json:"head_sha,omitempty"`
	Summary           string            `json:"summary"`
	Acceptance        []string          `json:"acceptance_criteria"`
	Tests             []string          `json:"tests"`
	Risks             []string          `json:"risks"`
	Verification      []string          `json:"verification"`
	Followups         []string          `json:"followups"`
	ChangedFiles      []string          `json:"changed_files"`
	Diffstat          string            `json:"diffstat"`
	ToolCallCount     int               `json:"tool_call_count"`
	ApprovalCount     int               `json:"approval_count"`
	FailureCode       string            `json:"failure_code,omitempty"`
	FailureMessage    string            `json:"failure_message,omitempty"`
	HowToContinue     string            `json:"how_to_continue"`
	SafeSummarySHA256 string            `json:"safe_summary_sha256"`
	Extra             map[string]string `json:"extra,omitempty"`
}

// rawArtifactRefusalKinds mirrors the D1 raw-refusal boundary. The list
// is duplicated here so review does not import agent/codex and so the
// redaction policy has a single source of truth inside the review
// package for the safe summary DTO.
// Note: "secrets" is intentionally excluded because it appears in
// ordinary English prose (e.g. risk note: "exposes secrets in logs")
// and causes false-positive rejections. "secret_artifact" covers the
// artifact-kind case.
var rawArtifactRefusalKinds = map[string]struct{}{
	"codex_log":        {},
	"codex_events":     {},
	"prompt_snapshot":  {},
	"prompt_rendered":  {},
	"prompt_context":   {},
	"secret_artifact":  {},
	"codex_final_dump": {},
	"raw_codex_log":    {},
	"raw_prompt":       {},
	"raw_prompt_log":   {},
}

// refusalKindBlocklist is used to scan structured payloads (e.g. JSON
// object values) for any refusal kind that must never enter a safe
// summary.
func refusalKindBlocklist() []string {
	out := make([]string, 0, len(rawArtifactRefusalKinds))
	for k := range rawArtifactRefusalKinds {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// BuildSafeSummaryFromRun reads the latest review packet directory for
// runID and projects a SafeSummary from the on-disk artifacts. It only
// touches fields that already live in the redacted review.json /
// review.md / diffstat.txt outputs; it never reads codex logs, raw
// prompts, or secret artifacts.
func BuildSafeSummaryFromRun(s *store.Store, runID string) (*SafeSummary, error) {
	run, err := s.GetRun(runID)
	if err != nil {
		return nil, err
	}
	issue, err := s.GetIssue(run.IssueID)
	if err != nil {
		return nil, err
	}
	return BuildSafeSummaryFromIssue(s, issue, run)
}

// BuildSafeSummaryFromIssue loads the latest review packet for issue
// and projects a SafeSummary. The function reads only metadata it
// trusts: the on-disk redacted review.json / review.md / diffstat.txt
// plus the run_attempts row.
func BuildSafeSummaryFromIssue(s *store.Store, issue *core.Issue, run *core.RunAttempt) (*SafeSummary, error) {
	if issue == nil {
		return nil, core.NewError(core.ErrReviewPacketRequired, "issue is nil", nil)
	}
	if run == nil {
		return nil, core.NewError(core.ErrReviewPacketRequired, "run is nil", nil)
	}
	return buildSafeSummaryFromIssueCore(s, issue, run, nil)
}

// BuildSafeSummaryFromIssueWithPrev is the D4 / R16 rework-aware
// variant. When `prev` is non-nil the safe summary is projected
// from the *previous* run's review packet and run_attempts row —
// because a Rework-dispatched run has no review packet of its own
// yet, and using the current run's source_issue_state would corrupt
// the snapshot (the current run is in Rework state by definition).
// Callers MUST pass `prev` whenever they have it (the orchestrator's
// rework injector always does).
func BuildSafeSummaryFromIssueWithPrev(s *store.Store, issue *core.Issue, run *core.RunAttempt, prev *core.RunAttempt) (*SafeSummary, error) {
	if issue == nil {
		return nil, core.NewError(core.ErrReviewPacketRequired, "issue is nil", nil)
	}
	if run == nil {
		return nil, core.NewError(core.ErrReviewPacketRequired, "run is nil", nil)
	}
	return buildSafeSummaryFromIssueCore(s, issue, run, prev)
}

// buildSafeSummaryFromIssueCore is the shared projection path. When
// prev is non-nil the function reads the previous run's review
// packet, run_attempts row, and source_issue_state, then returns a
// SafeSummary that reflects the *previous* dispatch's state. When
// prev is nil the function falls back to the current run.
func buildSafeSummaryFromIssueCore(s *store.Store, issue *core.Issue, run *core.RunAttempt, prev *core.RunAttempt) (*SafeSummary, error) {
	sourceRun := run
	if prev != nil {
		sourceRun = prev
	}
	packet, err := latestReviewPacketForRun(s, issue, sourceRun)
	if err != nil {
		return nil, err
	}
	summary := &SafeSummary{
		ReviewPacketID:   packet["id"].String(),
		PacketNo:         packet["packet_no"].Int(),
		RunID:            packet["run_id"].String(),
		SourceIssueState: string(sourceRun.SourceIssueState),
		Status:           packet["status"].String(),
		CreatedAt:        packet["created_at"].String(),
	}
	if issue.BranchName != nil {
		summary.BranchName = *issue.BranchName
	}
	if issue.BaseRef != nil {
		summary.BaseRef = *issue.BaseRef
	}
	if issue.BaseRefConfig != nil {
		summary.BaseRefConfig = *issue.BaseRefConfig
	}
	if issue.BaseSHA != nil {
		summary.BaseSHA = *issue.BaseSHA
	}
	if summary.SourceIssueState == "" {
		summary.SourceIssueState = string(core.StateReady)
	}
	root := packet["root_path"].String()
	if root != "" {
		if err := hydrateSafeSummaryFromArtifacts(summary, root); err != nil {
			return nil, err
		}
	}
	// Counts come from prompt_snapshot + tool_calls table, but stay
	// conservative and only consult run_attempts / review_packets to
	// avoid pulling raw payload bodies.
	summary.ToolCallCount = safeSummaryToolCallCount(s, packet["run_id"].String())
	summary.ApprovalCount = safeSummaryApprovalCount(s, packet["run_id"].String())
	if fc := packet["failure_code"].String(); fc != "" {
		summary.FailureCode = fc
	}
	if fm := packet["failure_message"].String(); fm != "" {
		summary.FailureMessage = fm
	}
	summary.HowToContinue = "Use Send to Rework with a reason, or Mark Done with an acceptance reason."
	if err := summary.Seal(); err != nil {
		return nil, err
	}
	return summary, nil
}

// Seal computes SafeSummarySHA256 over the redacted payload and runs
// the refusal-kind sentinel scan to guarantee no raw prompt / log /
// secret artifact is present in the rendered text.
func (s *SafeSummary) Seal() error {
	if s == nil {
		return fmt.Errorf("safe summary is nil")
	}
	if s.Extra == nil {
		s.Extra = map[string]string{}
	}
	if err := s.ScanForRawArtifacts(); err != nil {
		return err
	}
	clone := *s
	clone.SafeSummarySHA256 = ""
	b, err := json.Marshal(&clone)
	if err != nil {
		return err
	}
	h := sha256.Sum256(b)
	s.SafeSummarySHA256 = hex.EncodeToString(h[:])
	return nil
}

// ScanForRawArtifacts rejects any text field that contains a known
// raw-artifact refusal kind. The check is intentionally simple: we
// treat refusal kind labels as a hard blocklist and refuse to
// project the summary if any blocklist token is embedded in
// human-readable text. Empty / unset fields are fine.
func (s *SafeSummary) ScanForRawArtifacts() error {
	if s == nil {
		return fmt.Errorf("safe summary is nil")
	}
	stringsToScan := []string{
		s.Summary, s.HowToContinue,
		s.FailureMessage, s.BranchName, s.BaseRef, s.BaseRefConfig, s.BaseSHA, s.HeadSHA, s.Status, s.SourceIssueState,
	}
	stringsToScan = append(stringsToScan, s.Acceptance...)
	stringsToScan = append(stringsToScan, s.Tests...)
	stringsToScan = append(stringsToScan, s.Risks...)
	stringsToScan = append(stringsToScan, s.Verification...)
	stringsToScan = append(stringsToScan, s.Followups...)
	// PR #27 / D4 F5: ChangedFiles entries are file system paths.
	// The raw-artifact blocklist is about artifact *kind* labels,
	// not file names. Exclude ChangedFiles from the refusal-kind
	// scan so legitimate paths like "docs/raw_prompt.md" or
	// "secrets.txt" are not blocked.
	//
	// D4 F5b: Diffstat contains numstat rows with file system
	// paths (e.g. "docs/raw_prompt.md" or "secrets.txt"). Those
	// paths are not artifact kind labels; scanning them triggers
	// false-positive rejection on blocklist tokens embedded in
	// path segments. Diffstat is excluded for the same reason
	// ChangedFiles was — they are both file-path collections.
	stringsToScan = append(stringsToScan, s.FailureCode)
	for _, extra := range s.Extra {
		stringsToScan = append(stringsToScan, extra)
	}
	for _, raw := range stringsToScan {
		if raw == "" {
			continue
		}
		if hit := scanRefusalKind(raw); hit != "" {
			return fmt.Errorf("safe summary contains raw artifact marker %q: rejected", hit)
		}
	}
	return nil
}

// ToMarkdown renders the safe summary as a deterministic markdown
// block suitable for inclusion in a Rework prompt. The renderer is
// the only sanctioned way to surface previous review metadata to a
// Rework prompt; it MUST NOT read raw prompt / log / secret
// artifacts and MUST NOT include the SafeSummarySHA256 hash.
func (s *SafeSummary) ToMarkdown() (string, error) {
	if s == nil {
		return "", fmt.Errorf("safe summary is nil")
	}
	if err := s.ScanForRawArtifacts(); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("# Previous Review Packet (Safe Summary)\n\n")
	fmt.Fprintf(&b, "- Review packet: %s (packet #%d)\n", s.ReviewPacketID, s.PacketNo)
	if s.SourceIssueState != "" {
		fmt.Fprintf(&b, "- Source state: %s\n", s.SourceIssueState)
	}
	if s.Status != "" {
		fmt.Fprintf(&b, "- Status: %s\n", s.Status)
	}
	if s.BranchName != "" {
		fmt.Fprintf(&b, "- Branch: %s\n", s.BranchName)
	}
	if s.BaseRef != "" || s.BaseRefConfig != "" {
		fmt.Fprintf(&b, "- Base: %s (config %s)\n", s.BaseRef, s.BaseRefConfig)
	}
	if s.BaseSHA != "" {
		fmt.Fprintf(&b, "- Base SHA: %s\n", s.BaseSHA)
	}
	if s.HeadSHA != "" {
		fmt.Fprintf(&b, "- Head SHA: %s\n", s.HeadSHA)
	}
	if s.FailureCode != "" {
		fmt.Fprintf(&b, "- Failure code: %s\n", s.FailureCode)
	}
	if s.FailureMessage != "" {
		fmt.Fprintf(&b, "- Failure message: %s\n", s.FailureMessage)
	}
	fmt.Fprintf(&b, "- Tool calls observed: %d\n", s.ToolCallCount)
	fmt.Fprintf(&b, "- Approvals observed: %d\n", s.ApprovalCount)
	b.WriteString("\n## Summary\n")
	b.WriteString(strings.TrimSpace(s.Summary))
	b.WriteString("\n\n## Acceptance Criteria\n")
	b.WriteString(bullet(s.Acceptance))
	b.WriteString("\n\n## Tests\n")
	b.WriteString(bullet(s.Tests))
	b.WriteString("\n\n## Risks\n")
	b.WriteString(bullet(s.Risks))
	b.WriteString("\n\n## Verification\n")
	b.WriteString(bullet(s.Verification))
	b.WriteString("\n\n## Followups\n")
	b.WriteString(bullet(s.Followups))
	b.WriteString("\n\n## Changed Files\n")
	b.WriteString(bullet(s.ChangedFiles))
	if strings.TrimSpace(s.Diffstat) != "" {
		b.WriteString("\n\n## Diffstat\n```\n")
		b.WriteString(strings.TrimSpace(s.Diffstat))
		b.WriteString("\n```\n")
	}
	b.WriteString("\n## How to Continue\n")
	b.WriteString(strings.TrimSpace(s.HowToContinue))
	b.WriteString("\n")
	return b.String(), nil
}

func latestReviewPacketForRun(s *store.Store, issue *core.Issue, run *core.RunAttempt) (map[string]db.Value, error) {
	if issue == nil || run == nil {
		return nil, core.NewError(core.ErrReviewPacketRequired, "issue/run is nil", nil)
	}
	row, err := s.Project.QueryOne(`SELECT id, issue_id, run_id, handoff_id, packet_no, status, root_path, review_md_path, review_json_path, patch_path, changed_files_path, untracked_files_path, diffstat_path, prompt_snapshot_id, failure_code, failure_message, created_at FROM review_packets WHERE issue_id=? AND run_id=? ORDER BY packet_no DESC LIMIT 1`, issue.ID, run.ID)
	if err != nil {
		return nil, core.NewError(core.ErrReviewPacketRequired, "review packet required", nil)
	}
	return row, nil
}

func hydrateSafeSummaryFromArtifacts(summary *SafeSummary, root string) error {
	reviewJSONPath := filepath.Join(root, "review.json")
	diffstatPath := filepath.Join(root, "diffstat.txt")
	changedPath := filepath.Join(root, "changed-files.txt")
	if data, err := os.ReadFile(reviewJSONPath); err == nil {
		var packet map[string]any
		if err := json.Unmarshal(data, &packet); err == nil {
			summary.Summary = pickString(packet, "handoff", "summary")
			summary.Tests = pickStringSlice(packet, "handoff", "tests")
			summary.Risks = pickStringSlice(packet, "handoff", "risks")
			summary.Verification = pickStringSlice(packet, "handoff", "verification")
			summary.Followups = pickStringSlice(packet, "handoff", "followups")
			summary.Acceptance = pickStringSlice(packet, "issue", "acceptance_criteria")
			summary.ChangedFiles = pickStringSlice(packet, "changed_files")
			if head, ok := packet["git"].(map[string]any)["head_sha"].(string); ok {
				summary.HeadSHA = head
			}
		}
	}
	// D4 / R16: review.json handoff.summary is the structured source of
	// truth. The previous implementation OVERWROTE summary.Summary by
	// re-reading review.md's `## Summary` section and truncating it at
	// the first `\n## ` sub-heading. Because renderMarkdown injects
	// h.Summary verbatim after `## Summary`, a summary containing a
	// `## ` sub-heading (e.g. "Implemented parser\n\n## Edge cases...")
	// was truncated to just the text before that sub-heading, dropping
	// the rest. We no longer re-read review.md for the summary.
	if data, err := os.ReadFile(diffstatPath); err == nil {
		summary.Diffstat = strings.TrimSpace(string(data))
	}
	if data, err := os.ReadFile(changedPath); err == nil {
		summary.ChangedFiles = splitNonEmptyLines(string(data))
	}
	return nil
}

func pickString(m map[string]any, keys ...string) string {
	cur := any(m)
	for _, k := range keys {
		asMap, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = asMap[k]
	}
	if s, ok := cur.(string); ok {
		return s
	}
	return ""
}

func pickStringSlice(m map[string]any, keys ...string) []string {
	cur := any(m)
	for _, k := range keys {
		asMap, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = asMap[k]
	}
	switch v := cur.(type) {
	case []string:
		return append([]string{}, v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func splitNonEmptyLines(s string) []string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

func safeSummaryToolCallCount(s *store.Store, runID string) int {
	if runID == "" {
		return 0
	}
	row, err := s.Project.QueryOne(`SELECT COUNT(*) AS c FROM tool_calls WHERE run_id=?`, runID)
	if err != nil {
		return 0
	}
	return row["c"].Int()
}

func safeSummaryApprovalCount(s *store.Store, runID string) int {
	if runID == "" {
		return 0
	}
	row, err := s.Project.QueryOne(`SELECT COUNT(*) AS c FROM approval_requests WHERE run_id=?`, runID)
	if err != nil {
		return 0
	}
	return row["c"].Int()
}

// scanRefusalKind returns the matching raw-artifact marker if any of
// the blocklist tokens are present in raw. The check is case
// insensitive and matches at token boundaries so legitimate text
// like "tools called", "codex launch", or paths like
// "internal/secrets/store.go" do not false-positive (the token
// "secrets" must appear as a stand-alone marker, not embedded in a
// path segment or identifier). PR #27 / D4 F5.
func scanRefusalKind(raw string) string {
	low := strings.ToLower(raw)
	for _, kind := range refusalKindBlocklist() {
		if containsCIToken(low, kind) {
			return kind
		}
	}
	return ""
}

func containsCIToken(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	hl := len(haystack)
	nl := len(needle)
	for i := 0; i+nl <= hl; i++ {
		// Left boundary: the character immediately before the
		// match (if any) must be a "boundary" character
		// (whitespace or sentence punctuation). '/' '_' '-' and
		// alnum bytes are NOT treated as boundaries so substrings
		// inside paths/identifiers do not false-positive.
		// PR #27 / D4 F5.
		if i > 0 && !isLeftBoundaryCI(haystack, i) {
			continue
		}
		// Right boundary: same rule.
		if i+nl < hl && !isRightBoundaryCI(haystack, i+nl) {
			continue
		}
		match := true
		for j := 0; j < nl; j++ {
			a := haystack[i+j]
			b := needle[j]
			if a >= 'A' && a <= 'Z' {
				a += 32
			}
			if b >= 'A' && b <= 'Z' {
				b += 32
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func isLeftBoundaryCI(s string, tokenStart int) bool {
	pos := tokenStart - 1
	if isPathPunctuationBoundary(s, pos) {
		return false
	}
	return isBoundaryByteCI(s[pos])
}

func isRightBoundaryCI(s string, tokenEnd int) bool {
	if isPathPunctuationBoundary(s, tokenEnd) {
		return false
	}
	return isBoundaryByteCI(s[tokenEnd])
}

func isPathPunctuationBoundary(s string, pos int) bool {
	if pos <= 0 || pos+1 >= len(s) {
		return false
	}
	if s[pos] != '.' {
		return false
	}
	return isPathNameByteCI(s[pos-1]) && isPathNameByteCI(s[pos+1])
}

func isPathNameByteCI(b byte) bool {
	if b >= 'A' && b <= 'Z' {
		b += 32
	}
	return (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '_' || b == '-'
}

// isBoundaryByteCI reports whether b is a "boundary" byte — a
// whitespace, sentence punctuation, or structural delimiter that
// separates tokens in human-readable prose. It is intentionally
// *not* a path/identifier boundary detector: '/' '_' '-' and alnum
// bytes are treated as non-boundary so substrings like "secrets"
// or "prompt_snapshot" inside paths or identifiers do not match
// the raw-artifact blocklist. PR #27 / D4 F5.
func isBoundaryByteCI(b byte) bool {
	if b >= 'A' && b <= 'Z' {
		b += 32
	}
	switch b {
	case ' ', '\t', '\n', '\r', '\f', '\v':
		return true
	case '.', ',', ';', ':', '!', '?':
		return true
	case '(', ')', '[', ']', '{', '}':
		return true
	case '"', '\'', '`':
		return true
	}
	return false
}
