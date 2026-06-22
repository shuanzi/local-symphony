package codex

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"local-symphony/internal/core"
	"local-symphony/internal/review"
	"local-symphony/internal/store"
)

// ReworkContextInput bundles the D4/R16 inputs that the orchestrator
// hands to the rework prompt injector. The struct only references
// redacted, prompt-safe payloads — never raw prompt / log / secret
// artifact bytes.
type ReworkContextInput struct {
	Issue            *core.Issue
	Run              *core.RunAttempt
	PreviousRun      *core.RunAttempt
	ReviewPacketID   string
	ReviewReason     string
	SafeSummary      *review.SafeSummary
	WorkspacePath    string
	BaseSHA          string
	CumulativeDiffSHA string
}

// BuildReworkPrompt takes the base prompt (already rendered by
// config.RenderPrompt) and appends a deterministic rework envelope
// that surfaces the latest review reason and the previous review
// packet safe summary. The function enforces D4 / R16 invariants:
//
//   - raw prompt / raw codex log / secret artifact content MUST NOT
//     be reachable from ReworkContextInput.SafeSummary; we re-run the
//     safe summary's refusal-kind scan as a final safety net;
//   - the resulting prompt is deterministic (same inputs ⇒ same
//     rendered bytes) so prompt snapshot hashes are reproducible;
//   - the redacted prompt hash is exposed via ReworkPromptHash so
//     callers can persist it on the rework snapshot row.
func BuildReworkPrompt(basePrompt string, in ReworkContextInput) (string, string, error) {
	if strings.TrimSpace(in.ReviewReason) == "" {
		return "", "", fmt.Errorf("rework reason is required")
	}
	// Round 14 / R14-1: the operator's Send-to-Rework reason is written
	// verbatim into the next agent prompt (see buildReworkEnvelope) and
	// persisted on the rework_snapshots row. Apply the same refusal-kind
	// scan the safe summary uses so a reason containing a raw-artifact
	// marker (e.g. `kind=raw_prompt` or `codex_log`) cannot reintroduce
	// raw prompt / log / secret artifact references into the prompt.
	if hit := review.ScanRefusalKind(in.ReviewReason); hit != "" {
		return "", "", fmt.Errorf("rework reason contains raw artifact marker %q: rejected", hit)
	}
	if in.SafeSummary == nil {
		return "", "", fmt.Errorf("safe summary is required")
	}
	if err := in.SafeSummary.ScanForRawArtifacts(); err != nil {
		return "", "", fmt.Errorf("safe summary failed refusal-kind scan: %w", err)
	}
	md, err := in.SafeSummary.ToMarkdown()
	if err != nil {
		return "", "", fmt.Errorf("render safe summary markdown: %w", err)
	}
	envelope := buildReworkEnvelope(in, md)
	out := basePrompt
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	out += "\n" + envelope
	if in.Issue != nil && in.Issue.Identifier != "" {
		out += fmt.Sprintf("\n# Issue Identifier\n%s\n", in.Issue.Identifier)
	}
	ph := sha256.Sum256([]byte(out))
	return out, hex.EncodeToString(ph[:]), nil
}

// BuildReworkSnapshotRecord converts the input bundle into a
// store.ReworkSnapshotRecord. It is provided here so the orchestrator
// can stamp a single source of truth for the rework metadata.
func BuildReworkSnapshotRecord(promptHash string, in ReworkContextInput, promptSnapshotID string) store.ReworkSnapshotRecord {
	rec := store.ReworkSnapshotRecord{
		RunID:             in.Run.ID,
		IssueID:           in.Run.IssueID,
		PromptSnapshotID:  promptSnapshotID,
		PromptHash:        promptHash,
		ReviewReason:      in.ReviewReason,
		BaseSHA:           in.BaseSHA,
		CumulativeDiffSHA: in.CumulativeDiffSHA,
	}
	if in.PreviousRun != nil {
		rec.PreviousRunID = in.PreviousRun.ID
	}
	if in.ReviewPacketID != "" {
		rec.ReviewPacketID = in.ReviewPacketID
	}
	if in.SafeSummary != nil {
		rec.SafeSummarySHA256 = in.SafeSummary.SafeSummarySHA256
	}
	if in.Issue != nil {
		if in.Issue.BaseRef != nil {
			rec.BaseRef = *in.Issue.BaseRef
		}
		if rec.BaseSHA == "" && in.Issue.BaseSHA != nil {
			rec.BaseSHA = *in.Issue.BaseSHA
		}
	}
	return rec
}

func buildReworkEnvelope(in ReworkContextInput, safeSummaryMarkdown string) string {
	var b strings.Builder
	b.WriteString("# Rework Context (D4 / R16)\n")
	b.WriteString("\n## Latest Review Reason\n")
	b.WriteString(strings.TrimSpace(in.ReviewReason))
	b.WriteString("\n")
	if in.ReviewPacketID != "" {
		fmt.Fprintf(&b, "\n## Previous Review Packet\n%s\n", in.ReviewPacketID)
	}
	if in.PreviousRun != nil {
		fmt.Fprintf(&b, "\n## Previous Run\n%s (status: %s)\n", in.PreviousRun.ID, in.PreviousRun.Status)
	}
	if in.WorkspacePath != "" {
		fmt.Fprintf(&b, "\n## Workspace\n%s\n", in.WorkspacePath)
	}
	if in.BaseSHA != "" {
		fmt.Fprintf(&b, "\n## Base SHA\n%s\n", in.BaseSHA)
	}
	if in.CumulativeDiffSHA != "" {
		fmt.Fprintf(&b, "\n## Cumulative Diff SHA\n%s\n", in.CumulativeDiffSHA)
	}
	if in.SafeSummary != nil {
		fmt.Fprintf(&b, "\n## Safe Summary SHA256\n%s\n", in.SafeSummary.SafeSummarySHA256)
	}
	b.WriteString("\n## Previous Review Packet (Safe Summary)\n\n")
	b.WriteString(strings.TrimSpace(safeSummaryMarkdown))
	b.WriteString("\n")
	return b.String()
}
