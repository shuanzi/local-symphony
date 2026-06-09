package observability

import (
	"local-symphony/internal/agent/codex"
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
	lastPreflight := map[string]any{
		"ran_at":         summary.RanAt,
		"available":      summary.Available,
		"failure_code":   summary.FailureCode,
		"failure_reason": string(summary.FailureReason),
		"failure_message": summary.FailureMessage,
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
func CodexAvailability() map[string]any {
	return codexAvailability(codex.RunPreflight(codex.PreflightOptions{}))
}
