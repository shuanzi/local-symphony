package observability

import (
	"encoding/json"
	"strings"
	"testing"
)

// Synthetic sentinels used by redaction tests. These match the
// `SYNTHETIC_*` pattern enforced by the redactedFailureDetails
// scrubber in internal/agent/codex. Any new sentinel MUST be added
// to this list and to the no-leak assertions in
// TestDiagnosticsExportDoesNotLeakCodexSentinels.
const (
	RedactionSentinelPromptBody = "SYNTHETIC_PROMPT_BODY_do_not_leak_in_diagnostics"
	RedactionSentinelCodexLog   = "SYNTHETIC_CODEX_LOG_do_not_leak_in_diagnostics"
	RedactionSentinelOwnerNonce = "SYNTHETIC_OWNER_NONCE_do_not_leak_in_diagnostics"
	RedactionSentinelSecret     = "SYNTHETIC_API_SECRET_do_not_leak_in_diagnostics"
)

// AllRedactionSentinels is the canonical list every export-leak
// test must iterate. The set intentionally overlaps with the
// sentinel list in internal/agent/codex/preflight_test.go so the
// two layers fail closed at the same boundary.
func AllRedactionSentinels() []string {
	return []string{
		RedactionSentinelPromptBody,
		RedactionSentinelCodexLog,
		RedactionSentinelOwnerNonce,
		RedactionSentinelSecret,
	}
}

// assertNoSentinelsInDiagnostics walks the diagnostics envelope
// looking for the redacted-sentinel pattern. It is meant to be
// called by tests that have just generated a diagnostics payload
// (or read it back from `observability.Export`) and want to confirm
// no raw prompt / raw Codex log / raw secret content slipped
// through. The match is performed on the JSON-marshalled string
// representation so a map / slice value still triggers the leak
// alarm.
func assertNoSentinelsInDiagnostics(t *testing.T, payload any) {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal diagnostics payload: %v", err)
	}
	body := string(b)
	for _, sentinel := range AllRedactionSentinels() {
		if strings.Contains(body, sentinel) {
			t.Fatalf("diagnostics payload leaks sentinel %q: %s", sentinel, body)
		}
	}
}

// assertDiagnosticsFieldShape walks the diagnostics envelope and
// asserts the `codex` sub-object has the keys the schema promises.
// It is intentionally a thin wrapper around the diagnostics map so
// the contract test is decoupled from the map-shape change in
// codexAvailability.
func assertDiagnosticsFieldShape(t *testing.T, codexField map[string]any) {
	t.Helper()
	required := []string{"available", "version", "support", "metadata", "fixture_support", "last_preflight", "warning"}
	for _, key := range required {
		if _, ok := codexField[key]; !ok {
			t.Fatalf("diagnostics.codex missing key %q; have keys %v", key, mapKeysOf(codexField))
		}
	}
	support, ok := codexField["support"].(map[string]any)
	if !ok {
		t.Fatalf("diagnostics.codex.support is %T, want map[string]any", codexField["support"])
	}
	for _, sub := range []string{"cli", "model", "sandbox"} {
		if _, ok := support[sub]; !ok {
			t.Fatalf("diagnostics.codex.support missing %q", sub)
		}
	}
	fixture, ok := codexField["fixture_support"].(map[string]any)
	if !ok {
		t.Fatalf("diagnostics.codex.fixture_support is %T, want map[string]any", codexField["fixture_support"])
	}
	for _, sub := range []string{"schema_available", "metadata_available", "transcript_available"} {
		if _, ok := fixture[sub]; !ok {
			t.Fatalf("diagnostics.codex.fixture_support missing %q", sub)
		}
	}
	preflight, ok := codexField["last_preflight"].(map[string]any)
	if !ok {
		t.Fatalf("diagnostics.codex.last_preflight is %T, want map[string]any", codexField["last_preflight"])
	}
	for _, sub := range []string{"ran_at", "available", "failure_code", "failure_reason", "failure_message"} {
		if _, ok := preflight[sub]; !ok {
			t.Fatalf("diagnostics.codex.last_preflight missing %q", sub)
		}
	}
}

func mapKeysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
