package observability

import (
	"strings"

	"local-symphony/internal/agent/codex"
	"local-symphony/internal/config"
)

// codexAvailability translates a codex.PreflightSummary into the
// diagnostics-shaped `codex` map. The map is the projection the
// diagnostics schema enforces (additionalProperties=false), so any
// additive change must be mirrored in
// schemas/diagnostics.schema.json and api/openapi.yaml.
//
// Invariants:
//   - `available` is the single source of truth for the operator
//     "is Codex usable right now?" question.
//   - `support` is the cli/model/sandbox three-state block.
//   - `metadata` is only populated when a fixture's compatibility.json
//     was successfully parsed; failures leave it null.
//   - `fixture_support` always reports the on-disk state of the three
//     fixture artifacts (schema.json, compatibility.json,
//     happy-path.jsonl) so operators can tell at a glance which piece
//     is missing.
//   - `last_preflight` is a redacted snapshot of the preflight call:
//     it never carries raw prompt / raw Codex log / raw secret values.
//   - `warning` is a single-string field, distinct from the
//     diagnostics-level `warnings` array; it is only populated when
//     the preflight did NOT succeed, and it surfaces the canonical
//     failure code (`unsupported_codex_version`) so the dashboard can
//     show a stable string.
func codexAvailability(summary codex.PreflightSummary) map[string]any {
	metadata := map[string]any(nil)
	if summary.Metadata != nil {
		metadata = compatibilityMetadataAsMap(summary.Metadata)
	}
	// F1 (round-5 review): on the success path
	// `summary.FailureReason` is the empty string, but the
	// diagnostics schema and OpenAPI declare `failure_reason`
	// as an enum of canonical failure codes — the empty
	// string is NOT a member. Surface the value as JSON null
	// (Go nil) so a healthy Codex installation still passes
	// contract validation. `failure_code` and
	// `failure_message` already use the nullable-string
	// pattern (empty string -> nil) for the same reason.
	var failureReason any = nil
	if summary.FailureReason != "" {
		failureReason = string(summary.FailureReason)
	}
	var failureCode any = nil
	if summary.FailureCode != "" {
		failureCode = summary.FailureCode
	}
	var failureMessage any = nil
	if summary.FailureMessage != "" {
		failureMessage = summary.FailureMessage
	}
	lastPreflight := map[string]any{
		"ran_at":         summary.RanAt,
		"available":      summary.Available,
		"failure_code":   failureCode,
		"failure_reason": failureReason,
		"failure_message": failureMessage,
	}
	out := map[string]any{
		"available":      summary.Available,
		"version":        nil,
		"support":        codexSupportAsMap(summary.Support),
		"metadata":       metadata,
		"fixture_support": fixtureSupportAsMap(summary.FixtureSupport),
		"last_preflight": lastPreflight,
		"warning":        nil,
	}
	if v := summary.Version; v != "" {
		out["version"] = v
	}
	if !summary.Available {
		out["warning"] = unsupportedCodexVersionWarning
	}
	return out
}

const unsupportedCodexVersionWarning = "unsupported_codex_version"

func codexSupportAsMap(s codex.CodexSupport) map[string]any {
	return map[string]any{
		"cli":     string(s.CLI),
		"model":   string(s.Model),
		"sandbox": string(s.Sandbox),
	}
}

func fixtureSupportAsMap(f codex.FixtureSupport) map[string]any {
	return map[string]any{
		"schema_available":      f.SchemaAvailable,
		"metadata_available":    f.MetadataAvailable,
		"transcript_available":  f.TranscriptAvailable,
	}
}

func compatibilityMetadataAsMap(m *codex.CompatibilityMetadata) map[string]any {
	if m == nil {
		return nil
	}
	notes := make([]any, 0, len(m.SupportedNotifications))
	for _, n := range m.SupportedNotifications {
		notes = append(notes, n)
	}
	reqs := make([]any, 0, len(m.SupportedRequests))
	for _, r := range m.SupportedRequests {
		reqs = append(reqs, r)
	}
	return map[string]any{
		"codex_version":           m.CodexVersion,
		"protocol_version":        m.ProtocolVersion,
		"schema_version":          m.SchemaVersion,
		"experimental_api":        m.ExperimentalAPI,
		"supported_notifications": notes,
		"supported_requests":      reqs,
	}
}

// CodexAvailability is the public entry point used by the CLI
// `symphony status` and the HTTP `/state` handler. The preflight
// is re-run on every call so the operator always sees the current
// fixture state; the call is side-effect-free (no app-server
// process group is spawned).
//
// F2 (round-5 review): the preflight must use the
// operator-configured WORKFLOW.md `codex.command` and
// `codex.experimental_api` values, not the hard-coded
// defaults. The previous implementation called
// `codex.RunPreflight(codex.PreflightOptions{})` with no
// args, so the preflight always probed `codex` (the
// basename, plus `app-server` as the subcommand) and
// defaulted to `ExperimentalAPI=false`. A workflow that
// points `codex.command` at a custom binary (or expects
// `experimental_api=true`) would see diagnostics report
// "Codex unavailable" while the actual run path worked,
// or vice versa. The fix threads the workflow config into
// the preflight options. The `repoRoot` argument is the
// project root directory whose `WORKFLOW.md` is the
// source of truth; an empty string means "no workflow
// known", in which case the preflight falls back to the
// previous default behaviour so callers without a
// workflow context (existing tests, smoke checks) keep
// working.
func CodexAvailability(repoRoot string) map[string]any {
	return codexAvailability(codex.RunPreflight(preflightOptionsForRepo(repoRoot)))
}

// preflightOptionsForRepo is the workflow-aware option
// builder shared by CodexAvailability and Diagnostics. The
// `Diagnostics` function already has the workflow loaded
// (it surfaces the validation block), so it passes the
// pointer through directly; this helper is the repo-root
// convenience path for callers that have not yet loaded
// the workflow.
func preflightOptionsForRepo(repoRoot string) codex.PreflightOptions {
	opts := codex.PreflightOptions{}
	if strings.TrimSpace(repoRoot) == "" {
		return opts
	}
	wf, err := config.Load(repoRoot)
	if err != nil || wf == nil {
		return opts
	}
	return preflightOptionsFromWorkflow(wf)
}

// preflightOptionsFromWorkflow is the workflow-aware option
// builder. It propagates `codex.command` and
// `codex.experimental_api` from the parsed WORKFLOW.md
// front-matter into the preflight options. A nil workflow
// or a workflow with the default command is treated as
// "no override" so the preflight falls back to its
// installed-binary default.
func preflightOptionsFromWorkflow(wf *config.Workflow) codex.PreflightOptions {
	opts := codex.PreflightOptions{}
	if wf == nil {
		return opts
	}
	if cmd := strings.TrimSpace(wf.Config.Codex.Command); cmd != "" {
		opts.Command = cmd
	}
	opts.ExperimentalAPI = wf.Config.Codex.ExperimentalAPI
	return opts
}
