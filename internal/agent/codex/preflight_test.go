package codex

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-symphony/internal/core"
)

const (
	syntheticPromptBody = "SYNTHETIC_PROMPT_BODY_do_not_leak_in_diagnostics"
	syntheticCodexLog   = "SYNTHETIC_CODEX_LOG_do_not_leak_in_diagnostics"
	syntheticSecret     = "SYNTHETIC_OWNER_NONCE_do_not_leak_in_diagnostics"
)

func fixedNow() time.Time {
	return time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
}

func TestRunPreflightMalformedVersion(t *testing.T) {
	summary := RunPreflight(PreflightOptions{
		Command:       "codex-fake",
		VersionOutput: "not a version",
		Now:           fixedNow,
	})
	if summary.Available {
		t.Fatalf("Available = true, want false (malformed_version)")
	}
	if summary.FailureCode != string(core.FailureUnsupportedCodexVersion) {
		t.Fatalf("FailureCode = %q, want unsupported_codex_version", summary.FailureCode)
	}
	if summary.FailureReason != ReasonMalformedVersion {
		t.Fatalf("FailureReason = %q, want %q", summary.FailureReason, ReasonMalformedVersion)
	}
	if summary.Version != "" {
		t.Fatalf("Version = %q, want empty on parse failure", summary.Version)
	}
	if summary.Support.CLI != SupportStatusUnsupported {
		t.Fatalf("Support.CLI = %q, want unsupported", summary.Support.CLI)
	}
	if summary.Support.Model != SupportStatusUnknown {
		t.Fatalf("Support.Model = %q, want unknown", summary.Support.Model)
	}
	if summary.RanAt != "2026-06-09T10:00:00Z" {
		t.Fatalf("RanAt = %q, want 2026-06-09T10:00:00Z", summary.RanAt)
	}
}

func TestRunPreflightCodexNotInstalled(t *testing.T) {
	summary := RunPreflight(PreflightOptions{
		Command:       "definitely-not-installed-codex",
		VersionOutput: "",
		Now:           fixedNow,
	})
	if summary.Available {
		t.Fatalf("Available = true, want false when codex is not installed")
	}
	if summary.FailureReason != ReasonCodexNotInstalled {
		t.Fatalf("FailureReason = %q, want %q", summary.FailureReason, ReasonCodexNotInstalled)
	}
	if !strings.Contains(summary.FailureMessage, "not installed") {
		t.Fatalf("FailureMessage = %q, want substring 'not installed'", summary.FailureMessage)
	}
}

func TestRunPreflightMissingFixture(t *testing.T) {
	emptyRoot := t.TempDir()
	summary := RunPreflight(PreflightOptions{
		Command:       "codex-fake",
		VersionOutput: "codex 9.9.9-test",
		FixtureRoot:   emptyRoot,
		Now:           fixedNow,
	})
	if summary.Available {
		t.Fatalf("Available = true, want false when fixture root is empty")
	}
	if summary.FailureReason != ReasonMissingFixture {
		t.Fatalf("FailureReason = %q, want %q", summary.FailureReason, ReasonMissingFixture)
	}
	if summary.Version != "9.9.9-test" {
		t.Fatalf("Version = %q, want 9.9.9-test", summary.Version)
	}
	if summary.FixtureSupport.SchemaAvailable {
		t.Fatalf("FixtureSupport.SchemaAvailable = true, want false on missing fixture")
	}
	if summary.FixtureSupport.MetadataAvailable {
		t.Fatalf("FixtureSupport.MetadataAvailable = true, want false on missing fixture")
	}
	if summary.FixtureSupport.TranscriptAvailable {
		t.Fatalf("FixtureSupport.TranscriptAvailable = true, want false on missing fixture")
	}
}

func TestRunPreflightSuccess(t *testing.T) {
	root := testdataFixtureRoot(t)
	summary := RunPreflight(PreflightOptions{
		Command:       "codex-fake",
		VersionOutput: "codex 0.0.0-test",
		FixtureRoot:   root,
		Now:           fixedNow,
	})
	if !summary.Available {
		t.Fatalf("Available = false, want true; failure_reason=%q failure_message=%q", summary.FailureReason, summary.FailureMessage)
	}
	if summary.Version != "0.0.0-test" {
		t.Fatalf("Version = %q, want 0.0.0-test", summary.Version)
	}
	if summary.Support.CLI != SupportStatusSupported ||
		summary.Support.Model != SupportStatusSupported ||
		summary.Support.Sandbox != SupportStatusSupported {
		t.Fatalf("Support = %+v, want all supported", summary.Support)
	}
	if summary.Metadata == nil {
		t.Fatalf("Metadata is nil on success")
	}
	if summary.Metadata.ProtocolVersion != "protocol-test-v1" {
		t.Fatalf("Metadata.ProtocolVersion = %q, want protocol-test-v1", summary.Metadata.ProtocolVersion)
	}
	if summary.Metadata.SchemaVersion != "schema-test-v1" {
		t.Fatalf("Metadata.SchemaVersion = %q, want schema-test-v1", summary.Metadata.SchemaVersion)
	}
	if !summary.FixtureSupport.SchemaAvailable {
		t.Fatalf("FixtureSupport.SchemaAvailable = false, want true")
	}
	if !summary.FixtureSupport.MetadataAvailable {
		t.Fatalf("FixtureSupport.MetadataAvailable = false, want true")
	}
	if !summary.FixtureSupport.TranscriptAvailable {
		t.Fatalf("FixtureSupport.TranscriptAvailable = false, want true")
	}
	if summary.FailureReason != "" {
		t.Fatalf("FailureReason = %q, want empty on success", summary.FailureReason)
	}
	if summary.FailureCode != "" {
		t.Fatalf("FailureCode = %q, want empty on success", summary.FailureCode)
	}
}

func TestRunPreflightExperimentalRequired(t *testing.T) {
	root := testdataFixtureRoot(t)
	summary := RunPreflight(PreflightOptions{
		Command:         "codex-fake",
		VersionOutput:   "codex 0.0.0-test",
		FixtureRoot:     root,
		ExperimentalAPI: true,
		Now:             fixedNow,
	})
	if summary.Available {
		t.Fatalf("Available = true, want false when experimental_api is required but fixture disables it")
	}
	if summary.FailureReason != ReasonExperimentalNotSupport {
		t.Fatalf("FailureReason = %q, want %q", summary.FailureReason, ReasonExperimentalNotSupport)
	}
	if !strings.Contains(summary.FailureMessage, "experimental_api") {
		t.Fatalf("FailureMessage = %q, want substring 'experimental_api'", summary.FailureMessage)
	}
}

// TestRunPreflightRedactsCommandBeforeStorage is the round-1 P1
// #1 regression: the stored Command must be the binary basename
// only; arguments that may carry --api-key / wrapper flags /
// tokens must be dropped. A command that contains a synthetic
// secret-shaped token must not let the token survive the
// redaction step (defense in depth).
func TestRunPreflightRedactsCommandBeforeStorage(t *testing.T) {
	cases := []struct {
		name        string
		command     string
		wantCommand string
	}{
		{
			name:        "codex with no args",
			command:     "codex",
			wantCommand: "codex",
		},
		{
			name:        "codex app-server with sentinel flag",
			command:     "codex --api-key=SYNTHETIC_OWNER_NONCE_do_not_leak",
			wantCommand: "codex",
		},
		{
			name:        "absolute path with sentinel arg",
			command:     "/opt/codex/bin/codex --token=SYNTHETIC_API_SECRET_abc",
			wantCommand: "codex",
		},
		{
			name:        "wrapper invocation stores wrapper basename",
			command:     "env CODEX_API_KEY=SYNTHETIC_OWNER_NONCE_x /usr/local/bin/codex-app-server",
			wantCommand: "env",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := redactedCommandForTest(tc.command)
			if got != tc.wantCommand {
				t.Fatalf("redactedCommandForTest(%q) = %q, want %q", tc.command, got, tc.wantCommand)
			}
			for _, sentinel := range []string{"SYNTHETIC_OWNER_NONCE", "SYNTHETIC_API_SECRET", "SYNTHETIC_PROMPT_BODY", "SYNTHETIC_CODEX_LOG"} {
				if strings.Contains(got, sentinel) {
					t.Fatalf("redacted command leaks sentinel %q: %q", sentinel, got)
				}
			}
		})
	}
	// Also assert the value that ends up in the live summary.
	summary := RunPreflight(PreflightOptions{
		Command:       "codex --api-key=SYNTHETIC_OWNER_NONCE_secret_value",
		VersionOutput: "codex 0.0.0-test",
		FixtureRoot:   testdataFixtureRoot(t),
		Now:           fixedNow,
	})
	if got := summary.Command; got != "codex" {
		t.Fatalf("summary.Command = %q, want %q", got, "codex")
	}
	if strings.Contains(summary.Command, "SYNTHETIC_OWNER_NONCE") {
		t.Fatalf("summary.Command leaks sentinel: %q", summary.Command)
	}
}

// TestRunPreflightScrubsSuccessPathMetadata is the round-1 P1
// #2 regression: a poisoned compatibility.json whose
// SupportedNotifications / SupportedRequests carry synthetic
// sentinels must not let those sentinels survive the success
// path. The metadata block on the summary must contain
// "[REDACTED]" in the affected slots but preserve length and
// the rest of the metadata.
func TestRunPreflightScrubsSuccessPathMetadata(t *testing.T) {
	root := t.TempDir()
	version := "9.9.9-poison"
	schemaDir := filepath.Join(root, "schema", version)
	if err := os.MkdirAll(schemaDir, 0o755); err != nil {
		t.Fatalf("mkdir schema: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "transcripts", version), 0o755); err != nil {
		t.Fatalf("mkdir transcripts: %v", err)
	}
	// Construct a compatibility.json that is structurally valid
	// (matching codex_version, schema.json + happy-path.jsonl in
	// place, no extra unsupported fields the validator rejects)
	// but whose SupportedNotifications / SupportedRequests
	// contain synthetic sentinels. The success path must scrub
	// them before assignment.
	poisoned := `{
  "codex_version": "9.9.9-poison",
  "protocol_version": "protocol-poison-v1",
  "schema_version": "schema-poison-v1",
  "supported_notifications": [
    "handoff",
    "SYNTHETIC_PROMPT_BODY_leak_in_metadata",
    "turn/started"
  ],
  "supported_requests": [
    "SYNTHETIC_CODEX_LOG_leak_in_metadata",
    "initialize"
  ],
  "experimental_api": false
}`
	if err := os.WriteFile(filepath.Join(schemaDir, compatibilityMetadataFile), []byte(poisoned), 0o600); err != nil {
		t.Fatalf("write poisoned metadata: %v", err)
	}
	// Provide a minimal schema.json so the schema_available check
	// passes and SelectFixtureMetadata does not bail on
	// missing_schema_fixture.
	schemaStub := `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`
	if err := os.WriteFile(filepath.Join(schemaDir, "schema.json"), []byte(schemaStub), 0o600); err != nil {
		t.Fatalf("write schema stub: %v", err)
	}
	// And a transcript stub that satisfies the validator (must
	// contain a handshake and a terminal turn message; matching
	// the compatibility metadata). The pre-existing
	// ValidateTranscriptFixture in codex.go is strict, so use a
	// transcript that matches the poison fixture's protocol.
	transcript := `{"type":"handshake","codex_version":"9.9.9-poison","protocol_version":"protocol-poison-v1","schema_version":"schema-poison-v1","experimental_api":false}
{"type":"turn_completed"}
`
	if err := os.WriteFile(filepath.Join(root, "transcripts", version, "happy-path.jsonl"), []byte(transcript), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	summary := RunPreflight(PreflightOptions{
		Command:       "codex",
		VersionOutput: "codex 9.9.9-poison",
		FixtureRoot:   root,
		Now:           fixedNow,
	})
	if !summary.Available {
		t.Fatalf("Available = false, want true (the poisoned metadata should still pass the structural gate); failure_reason=%q failure_message=%q", summary.FailureReason, summary.FailureMessage)
	}
	if summary.Metadata == nil {
		t.Fatalf("Metadata is nil on success")
	}
	b, _ := json.Marshal(summary.Metadata)
	body := string(b)
	for _, sentinel := range []string{
		"SYNTHETIC_PROMPT_BODY_leak_in_metadata",
		"SYNTHETIC_CODEX_LOG_leak_in_metadata",
	} {
		if strings.Contains(body, sentinel) {
			t.Fatalf("summary.Metadata leaks sentinel %q: %s", sentinel, body)
		}
	}
	if !strings.Contains(body, "[REDACTED]") {
		t.Fatalf("summary.Metadata should contain [REDACTED], got: %s", body)
	}
	// Length and ordering must be preserved.
	if len(summary.Metadata.SupportedNotifications) != 3 {
		t.Fatalf("SupportedNotifications length = %d, want 3 (no scrubbing of array length)", len(summary.Metadata.SupportedNotifications))
	}
	if len(summary.Metadata.SupportedRequests) != 2 {
		t.Fatalf("SupportedRequests length = %d, want 2", len(summary.Metadata.SupportedRequests))
	}
}

// TestRunPreflightScrubsFailureMessage is the round-1 P1 #3
// regression: when fixture validation fails with attacker-
// controlled detail text (a poisoned metadata_version or a
// transcript-validation error containing a sentinel), the
// FailureMessage must not leak the sentinel. The FailureDetails
// is already redacted; the message string is the residual path.
func TestRunPreflightScrubsFailureMessage(t *testing.T) {
	root := t.TempDir()
	version := "9.9.9-leak"
	schemaDir := filepath.Join(root, "schema", version)
	if err := os.MkdirAll(schemaDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "transcripts", version), 0o755); err != nil {
		t.Fatalf("mkdir transcripts: %v", err)
	}
	// Construct a compatibility.json whose metadata_version
	// contains a synthetic prompt sentinel. The validator will
	// produce an invalid_transcript_fixture error if anything
	// downstream trips, but the simpler attack is a transcript
	// whose handshake includes a poisoned metadata_version —
	// the validator passes it through into details["error"].
	poisoned := `{
  "codex_version": "9.9.9-leak",
  "protocol_version": "protocol-leak-v1",
  "schema_version": "schema-leak-v1",
  "supported_notifications": ["handoff"],
  "supported_requests": ["initialize"],
  "experimental_api": false
}`
	if err := os.WriteFile(filepath.Join(schemaDir, compatibilityMetadataFile), []byte(poisoned), 0o600); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	schemaStub := `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`
	if err := os.WriteFile(filepath.Join(schemaDir, "schema.json"), []byte(schemaStub), 0o600); err != nil {
		t.Fatalf("write schema stub: %v", err)
	}
	// Transcript with a poisoned error field in the handshake
	// validator path: the validator reports details["error"] for
	// invalid transcripts. We push a transcript whose first
	// message type is unsupported, which makes the validator
	// fail with "unsupported message type" — we then expect the
	// preflight to surface invalid_transcript_fixture and the
	// FailureMessage must be redacted of any sentinel that
	// appears in the actual line.
	transcript := `{"type":"SYNTHETIC_PROMPT_BODY_actual_firmware_leak_in_transcript","note":"raw prompt body"}
`
	if err := os.WriteFile(filepath.Join(root, "transcripts", version, "happy-path.jsonl"), []byte(transcript), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	summary := RunPreflight(PreflightOptions{
		Command:       "codex",
		VersionOutput: "codex 9.9.9-leak",
		FixtureRoot:   root,
		Now:           fixedNow,
	})
	if summary.Available {
		t.Fatalf("Available = true, want false (transcript should fail validation)")
	}
	for _, sentinel := range []string{"SYNTHETIC_PROMPT_BODY_actual_firmware_leak_in_transcript"} {
		if strings.Contains(summary.FailureMessage, sentinel) {
			t.Fatalf("FailureMessage leaks sentinel %q: %q", sentinel, summary.FailureMessage)
		}
	}
}

// TestRunPreflightDistinguishesMalformedFromNotInstalled is the
// round-1 P2 #4 regression: an installed codex binary that emits
// a malformed --version line must surface malformed_version, not
// codex_not_installed. The classifier uses exec.LookPath on the
// binary's first token to break the tie when the version probe
// returns empty output.
func TestRunPreflightDistinguishesMalformedFromNotInstalled(t *testing.T) {
	t.Run("installed binary with malformed --version line", func(t *testing.T) {
		// "go" is always present in the test environment. We
		// pass an explicit --version output string that is
		// non-empty but unparseable. The override path skips
		// DetectVersionForCommand entirely, so the only way
		// the classifier runs is via the empty-output branch.
		// To exercise the LookPath branch we use a binary
		// known to be on PATH ("go") with a blank probe
		// output: the classifier sees empty probe + LookPath
		// success -> malformed_version.
		binary, lookErr := exec.LookPath("go")
		if lookErr != nil {
			t.Skipf("go binary not on PATH, cannot test LookPath branch: %v", lookErr)
		}
		_ = binary
		summary := RunPreflight(PreflightOptions{
			Command:       "go",
			VersionOutput: "", // empty -> LookPath branch
			Now:           fixedNow,
		})
		if summary.Available {
			t.Fatalf("Available = true, want false")
		}
		if summary.FailureReason != ReasonMalformedVersion {
			t.Fatalf("FailureReason = %q, want %q (installed binary with empty probe output must NOT be misreported as not-installed)", summary.FailureReason, ReasonMalformedVersion)
		}
	})
	t.Run("truly missing binary", func(t *testing.T) {
		// A name that is guaranteed not to be on PATH (very
		// long random string). LookPath fails -> not_installed.
		binary, err := exec.LookPath("definitely-not-installed-binary-zzzzzz-9k3x")
		if err == nil {
			t.Skipf("unrelated binary on PATH at %q, cannot test missing-binary branch", binary)
		}
		summary := RunPreflight(PreflightOptions{
			Command:       "definitely-not-installed-binary-zzzzzz-9k3x",
			VersionOutput: "",
			Now:           fixedNow,
		})
		if summary.Available {
			t.Fatalf("Available = true, want false")
		}
		if summary.FailureReason != ReasonCodexNotInstalled {
			t.Fatalf("FailureReason = %q, want %q", summary.FailureReason, ReasonCodexNotInstalled)
		}
	})
	t.Run("non-empty unparseable output", func(t *testing.T) {
		// Non-empty garbage output -> malformed_version
		// regardless of whether the binary exists on PATH.
		summary := RunPreflight(PreflightOptions{
			Command:       "codex",
			VersionOutput: "completely unparseable garbage output",
			Now:           fixedNow,
		})
		if summary.Available {
			t.Fatalf("Available = true, want false")
		}
		if summary.FailureReason != ReasonMalformedVersion {
			t.Fatalf("FailureReason = %q, want %q", summary.FailureReason, ReasonMalformedVersion)
		}
	})
}

func TestRunPreflightScrubberStripsSentinels(t *testing.T) {
	details := map[string]any{
		"reason":         "missing_fixture",
		"codex_version":  "1.0.0+" + syntheticPromptBody,
		"stderr_excerpt": "boom " + syntheticCodexLog + " end",
		"tokens":         []string{"a", syntheticSecret, "c"},
		"nested":         map[string]any{"inner": syntheticPromptBody + "/inner"},
		"count":          42,
	}
	scrubbed := failureDetailsForTest(details)
	b, _ := json.Marshal(scrubbed)
	body := string(b)
	for _, sentinel := range []string{syntheticPromptBody, syntheticCodexLog, syntheticSecret} {
		if strings.Contains(body, sentinel) {
			t.Fatalf("scrubbed failure details leak sentinel %q in: %s", sentinel, body)
		}
	}
	if !strings.Contains(body, "[REDACTED]") {
		t.Fatalf("scrubbed output should contain [REDACTED], got: %s", body)
	}
	if !strings.Contains(body, "42") {
		t.Fatalf("scrubbed output should preserve non-string scalars, got: %s", body)
	}
}

// TestCodexScrubCatchesSentinelAfterUnderscore is the round-5
// (PR #24 review) F3 regression: a poisoned fixture that
// plants a synthetic-sentinel-shaped substring after an
// underscore (e.g. `protocol_SYNTHETIC_PROMPT_BODY`,
// `schema_SYNTHETIC_API_SECRET`) must be redacted to
// `[REDACTED]`, not echoed verbatim into the diagnostics
// envelope. The previous detector used a `\b...\b` regex
// (matching word boundaries), and `_` is a word character,
// so the boundary did NOT match before an underscore — the
// detector considered `protocol_SYNTHETIC_PROMPT_BODY` a
// legitimate identifier and the redaction scrubber left it
// alone. The fix extends the unsafe boundary set to also
// include `_`, so a sentinel preceded by an underscore is
// still flagged. The `syntheticSentinelPattern` in codex.go
// and the contract validator's `SYNTHETIC_SENTINEL_RE` must
// agree; the previous shape is preserved for non-underscore
// boundaries (start-of-string, end-of-string, dash, etc.)
// and the underscore case is added as a new boundary. Tests
// pin both the detector (a `protocol_SYNTHETIC_*` value must
// be flagged) and the scrubber (the raw substring must
// become `[REDACTED]`).
func TestCodexScrubCatchesSentinelAfterUnderscore(t *testing.T) {
	// Detector level: a `protocol_SYNTHETIC_PROMPT_BODY`
	// value must be recognised as a sentinel. Without the
	// round-5 fix the regex `\bSYNTHETIC_[A-Z0-9_]+\b` would
	// not match the embedded sentinel because the leading
	// `_` is a word character and `\b` requires a non-word
	// / word transition.
	if !isSyntheticSentinelStringForTest("protocol_SYNTHETIC_PROMPT_BODY") {
		t.Fatalf("detector must flag protocol_SYNTHETIC_PROMPT_BODY (sentinel preceded by underscore)")
	}
	if !isSyntheticSentinelStringForTest("schema_SYNTHETIC_API_SECRET") {
		t.Fatalf("detector must flag schema_SYNTHETIC_API_SECRET (sentinel preceded by underscore)")
	}
	// Scrubber level: a poisoned compatibility.json whose
	// protocol_version carries the underscored sentinel must
	// be redacted end-to-end. The fixture is rejected by
	// the gate (sentinel-shaped protocol_version) so the
	// preflight returns a failure summary, but the failure
	// details (which include the value the validator saw)
	// must NOT carry the raw sentinel.
	scrubbed := scrubbedForDiagnostics("protocol_SYNTHETIC_PROMPT_BODY")
	if strings.Contains(scrubbed, "SYNTHETIC_PROMPT_BODY") {
		t.Fatalf("scrubber left the sentinel intact in %q", scrubbed)
	}
	if !strings.Contains(scrubbed, "[REDACTED]") {
		t.Fatalf("scrubber output should contain [REDACTED], got: %q", scrubbed)
	}
	// And the redaction must also flow through the
	// failureDetailsForTest path so a poisoned detail
	// value never leaks through diagnostics.
	details := map[string]any{
		"protocol_version": "protocol_SYNTHETIC_PROMPT_BODY",
		"schema_version":   "schema_SYNTHETIC_API_SECRET",
		"safe_value":       "1.2.3",
	}
	out := failureDetailsForTest(details)
	b, _ := json.Marshal(out)
	body := string(b)
	for _, sentinel := range []string{"SYNTHETIC_PROMPT_BODY", "SYNTHETIC_API_SECRET"} {
		if strings.Contains(body, sentinel) {
			t.Fatalf("redacted failure details still leak sentinel %q: %s", sentinel, body)
		}
	}
	if !strings.Contains(body, "1.2.3") {
		t.Fatalf("redacted output should preserve non-sentinel values, got: %s", body)
	}
}

func TestAllPreflightReasonsCoversStableSet(t *testing.T) {
	all := AllPreflightReasons()
	if len(all) == 0 {
		t.Fatalf("AllPreflightReasons() returned empty slice")
	}
	seen := map[PreflightReason]bool{}
	for _, r := range all {
		if r == "" {
			t.Fatalf("AllPreflightReasons contains empty reason")
		}
		if seen[r] {
			t.Fatalf("AllPreflightReasons contains duplicate reason %q", r)
		}
		seen[r] = true
	}
	for _, r := range []PreflightReason{ReasonMalformedVersion, ReasonMissingFixture, ReasonCodexNotInstalled} {
		if !seen[r] {
			t.Fatalf("AllPreflightReasons missing %q", r)
		}
	}
}

func TestRunPreflightJSONShapeIsStable(t *testing.T) {
	root := testdataFixtureRoot(t)
	summary := RunPreflight(PreflightOptions{
		Command:       "codex-fake",
		VersionOutput: "codex 0.0.0-test",
		FixtureRoot:   root,
		Now:           fixedNow,
	})
	body, err := PreflightSummaryToJSON(summary)
	if err != nil {
		t.Fatalf("PreflightSummaryToJSON: %v", err)
	}
	for _, key := range []string{
		`"ran_at"`,
		`"command"`,
		`"version"`,
		`"available"`,
		`"support"`,
		`"metadata"`,
		`"fixture_support"`,
	} {
		if !strings.Contains(body, key) {
			t.Fatalf("PreflightSummary JSON missing key %q in: %s", key, body)
		}
	}
}

// testdataFixtureRoot resolves the testdata directory the package's
// own fixtures live under. We use the package-relative path
// (`testdata/...`) so tests are hermetic and never depend on the
// operator's installed codex / fixture layout.
func testdataFixtureRoot(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root := filepath.Join(cwd, "testdata")
	if !scratchDirIsEmpty(root) {
		// Confirm the expected fixture path is present.
		for _, sub := range []string{
			filepath.Join("schema", "0.0.0-test", "compatibility.json"),
			filepath.Join("schema", "0.0.0-test", "schema.json"),
			filepath.Join("transcripts", "0.0.0-test", "happy-path.jsonl"),
		} {
			if _, err := os.Stat(filepath.Join(root, sub)); err != nil {
				t.Fatalf("testdata missing %s: %v", sub, err)
			}
		}
	}
	return root
}

// TestIsSyntheticSentinelStringDistinguishesVersionAndSentinel is
// the round-2 + round-3 P1 regression: legitimate version /
// protocol / schema identifiers must not be flagged as
// sentinel-shaped, and synthetic-sentinel-shaped values (both
// whole-token and embedded matches) must be flagged.
//
// The detector is a word-boundary regex identical to the
// SYNTHETIC_SENTINEL_RE regex in scripts/validate_contracts.py;
// a poisoned fixture that survives the gate would also
// survive the contract validator, so the two layers agree
// on what a sentinel-shaped value looks like.
func TestIsSyntheticSentinelStringDistinguishesVersionAndSentinel(t *testing.T) {
	cases := []struct {
		value    string
		sentinel bool
	}{
		// Plain version / protocol / schema identifiers —
		// must not be flagged.
		{value: "1.2.3", sentinel: false},
		{value: "v1.2.3", sentinel: false},
		{value: "protocol-test-v1", sentinel: false},
		{value: "schema-2024-01", sentinel: false},
		{value: "0.0.0-test", sentinel: false},
		// Whole-token sentinel-shaped values — must be flagged.
		{value: "SYNTHETIC_FOO", sentinel: true},
		{value: "SYNTHETIC_PROMPT_BODY", sentinel: true},
		{value: "SYNTHETIC_OWNER_NONCE_XYZ", sentinel: true},
		{value: "SYNTHETIC_API_SECRET_KEY", sentinel: true},
		// Embedded sentinel-shaped values — round-3 fix. The
		// previous whole-string-prefix detector accepted
		// these as legitimate; the word-boundary regex
		// correctly rejects them.
		{value: "protocol-SYNTHETIC_PROMPT_BODY-v1", sentinel: true},
		{value: "schema-SYNTHETIC_CODEX_LOG-2024", sentinel: true},
		// Embedded sentinels whose trailing `_xxx` suffix
		// contains lowercase letters are NOT detected —
		// the word-boundary regex requires the sentinel
		// body to be a continuous run of [A-Z0-9_], and
		// the contract validator's identical regex agrees.
		// Operators / fixture authors who plant a poisoned
		// value should follow the same convention as the
		// rest of the codebase: upper-snake-case body.
		{value: "v1.0.0+SYNTHETIC_OWNER_NONCE_xxx", sentinel: false},
		{value: "SYNTHETIC_PROMPT_BODY_do_not_leak", sentinel: false},
		// Bare prefix is not a sentinel.
		{value: "SYNTHETIC_", sentinel: false},
		// Lowercase prefix is not a sentinel.
		{value: "synthetic_foo", sentinel: false},
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			if got := isSyntheticSentinelStringForTest(tc.value); got != tc.sentinel {
				t.Fatalf("isSyntheticSentinelString(%q) = %v, want %v", tc.value, got, tc.sentinel)
			}
		})
	}
}

// TestRunPreflightRejectsSentinelShapedProtocolVersion is the
// round-2 P1 #1 regression: a poisoned compatibility.json that
// keeps `codex_version` matching the detected version but
// plants a synthetic-sentinel-shaped value in `protocol_version`
// (the field the round-1 scrubber did NOT cover) must be
// rejected at the validateCompatibilityMetadata gate. The
// preflight must surface
// `ReasonMetadataProtoSentinel`, and the diagnostics envelope
// must never see the sentinel.
func TestRunPreflightRejectsSentinelShapedProtocolVersion(t *testing.T) {
	root := t.TempDir()
	version := "9.9.9-poison"
	schemaDir := filepath.Join(root, "schema", version)
	if err := os.MkdirAll(schemaDir, 0o755); err != nil {
		t.Fatalf("mkdir schema: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "transcripts", version), 0o755); err != nil {
		t.Fatalf("mkdir transcripts: %v", err)
	}
	// Structurally valid compatibility.json: codex_version
	// matches the detected version, supported_notifications /
	// supported_requests carry only conventional entries,
	// experimental_api is false — but protocol_version is
	// poisoned with a synthetic-sentinel-shaped value. The
	// gate must reject the metadata before reaching the
	// happy-path branches. We also provide a schema.json +
	// happy-path.jsonl stub so the validator progresses
	// past the missing_schema_fixture / missing_transcript
	// checks and reaches validateCompatibilityMetadata.
	poisoned := `{
  "codex_version": "9.9.9-poison",
  "protocol_version": "SYNTHETIC_PROMPT_BODY_LEAK",
  "schema_version": "schema-poison-v1",
  "supported_notifications": ["handoff"],
  "supported_requests": ["initialize"],
  "experimental_api": false
}`
	if err := os.WriteFile(filepath.Join(schemaDir, compatibilityMetadataFile), []byte(poisoned), 0o600); err != nil {
		t.Fatalf("write poisoned metadata: %v", err)
	}
	schemaStub := `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`
	if err := os.WriteFile(filepath.Join(schemaDir, "schema.json"), []byte(schemaStub), 0o600); err != nil {
		t.Fatalf("write schema stub: %v", err)
	}
	transcript := `{"type":"handshake","codex_version":"9.9.9-poison","protocol_version":"SYNTHETIC_PROMPT_BODY_LEAK","schema_version":"schema-poison-v1","experimental_api":false}
{"type":"turn_completed"}
`
	if err := os.WriteFile(filepath.Join(root, "transcripts", version, "happy-path.jsonl"), []byte(transcript), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	summary := RunPreflight(PreflightOptions{
		Command:       "codex",
		VersionOutput: "codex 9.9.9-poison",
		FixtureRoot:   root,
		Now:           fixedNow,
	})
	if summary.Available {
		t.Fatalf("Available = true, want false (sentinel-shaped protocol_version must be rejected)")
	}
	if summary.FailureReason != ReasonMetadataProtoSentinel {
		t.Fatalf("FailureReason = %q, want %q", summary.FailureReason, ReasonMetadataProtoSentinel)
	}
	// No field of the summary may carry the sentinel.
	b, _ := json.Marshal(summary)
	body := string(b)
	if strings.Contains(body, "SYNTHETIC_PROMPT_BODY_LEAK") {
		t.Fatalf("summary leaks poisoned protocol_version sentinel: %s", body)
	}
}

// TestRunPreflightRejectsSentinelShapedSchemaVersion mirrors
// the protocol_version case for the schema_version field. The
// two fields go through independent codex-version reasons in
// the round-2 patch, so we assert them separately.
func TestRunPreflightRejectsSentinelShapedSchemaVersion(t *testing.T) {
	root := t.TempDir()
	version := "9.9.9-sentinel-schema"
	schemaDir := filepath.Join(root, "schema", version)
	if err := os.MkdirAll(schemaDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "transcripts", version), 0o755); err != nil {
		t.Fatalf("mkdir transcripts: %v", err)
	}
	poisoned := `{
  "codex_version": "9.9.9-sentinel-schema",
  "protocol_version": "protocol-poison-v1",
  "schema_version": "SYNTHETIC_CODEX_LOG_LEAK",
  "supported_notifications": ["handoff"],
  "supported_requests": ["initialize"],
  "experimental_api": false
}`
	if err := os.WriteFile(filepath.Join(schemaDir, compatibilityMetadataFile), []byte(poisoned), 0o600); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	schemaStub := `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`
	if err := os.WriteFile(filepath.Join(schemaDir, "schema.json"), []byte(schemaStub), 0o600); err != nil {
		t.Fatalf("write schema stub: %v", err)
	}
	transcript := `{"type":"handshake","codex_version":"9.9.9-sentinel-schema","protocol_version":"protocol-poison-v1","schema_version":"SYNTHETIC_CODEX_LOG_LEAK","experimental_api":false}
{"type":"turn_completed"}
`
	if err := os.WriteFile(filepath.Join(root, "transcripts", version, "happy-path.jsonl"), []byte(transcript), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	summary := RunPreflight(PreflightOptions{
		Command:       "codex",
		VersionOutput: "codex 9.9.9-sentinel-schema",
		FixtureRoot:   root,
		Now:           fixedNow,
	})
	if summary.Available {
		t.Fatalf("Available = true, want false (sentinel-shaped schema_version must be rejected)")
	}
	if summary.FailureReason != ReasonMetadataSchemaSentinel {
		t.Fatalf("FailureReason = %q, want %q", summary.FailureReason, ReasonMetadataSchemaSentinel)
	}
}

// TestUnwrapCodexBinaryTokenSkipsKnownWrappers is the round-2
// P2 #2 unwrap-regression. It exercises each recognized wrapper
// (env / sudo / nohup / command / time / xargs -I{}) plus the
// KEY=VALUE env-var prefix and asserts the unwrap returns the
// actual codex binary, not the wrapper name. It also asserts
// the conservative-fallback behaviour: an unrecognized leading
// token is returned as-is so the round-1 first-token tests stay
// green.
func TestUnwrapCodexBinaryTokenSkipsKnownWrappers(t *testing.T) {
	cases := []struct {
		command string
		want    string
	}{
		{command: "codex", want: "codex"},
		{command: "codex app-server", want: "codex"},
		{command: "env CODEX_API_KEY=x codex", want: "codex"},
		{command: "env CODEX_API_KEY=x codex app-server", want: "codex"},
		{command: "sudo -E codex", want: "codex"},
		{command: "nohup codex", want: "codex"},
		{command: "command codex", want: "codex"},
		{command: "time codex", want: "codex"},
		{command: "xargs -I{} codex", want: "codex"},
		{command: "PATH=/opt/bin codex", want: "codex"},
		{command: "PATH=/opt/bin env X=y codex", want: "codex"},
		{command: "/usr/local/bin/codex", want: "/usr/local/bin/codex"},
		// Unknown wrapper: fall back to first-token behavior
		// (the round-1 classifier treats the unknown wrapper
		// as the binary; this is the safe behavior because
		// a new wrapper that exists on PATH could still
		// invoke the real codex successfully).
		{command: "my-custom-runner codex", want: "my-custom-runner"},
	}
	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			if got := unwrapCodexBinaryTokenForTest(tc.command); got != tc.want {
				t.Fatalf("unwrapCodexBinaryToken(%q) = %q, want %q", tc.command, got, tc.want)
			}
		})
	}
}

// TestRunPreflightClassifiesWrappedMissingBinaryAsNotInstalled
// is the round-2 P2 #2 end-to-end regression: a wrapped
// invocation whose wrapped binary is missing must surface
// `ReasonCodexNotInstalled`, not `ReasonMalformedVersion`. The
// pre-2 classifier would have looked up `env` (which exists on
// PATH) and reported `malformed_version`.
func TestRunPreflightClassifiesWrappedMissingBinaryAsNotInstalled(t *testing.T) {
	cases := []struct {
		name    string
		command string
	}{
		{
			name:    "env with missing codex",
			command: "env CODEX_API_KEY=SYNTHETIC_OWNER_NONCE_x codex-not-on-path-zzzz-12345",
		},
		{
			name:    "sudo with missing codex",
			command: "sudo -E codex-not-on-path-zzzz-12345",
		},
		{
			name:    "PATH prefix with missing codex",
			command: "PATH=/opt/bin codex-not-on-path-zzzz-12345",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			summary := RunPreflight(PreflightOptions{
				Command:       tc.command,
				VersionOutput: "",
				Now:           fixedNow,
			})
			if summary.Available {
				t.Fatalf("Available = true, want false")
			}
			if summary.FailureReason != ReasonCodexNotInstalled {
				t.Fatalf("FailureReason = %q, want %q (wrapped invocation with missing wrapped binary must be reported as not-installed, not malformed)", summary.FailureReason, ReasonCodexNotInstalled)
			}
		})
	}
}

// TestUnwrapCodexBinaryTokenRejectsEmptyTokens is the round-3
// P3 panic regression. `commandParts` produces an empty
// token for inputs like `env "" codex` (a quoted empty arg)
// and `sudo   codex` (consecutive spaces produce empty
// tokens between them). The round-2 walker passed empty
// tokens through to isWrapperFlagToken, which then
// indexed `token[0]` on an empty string and panicked at
// runtime. The fix is a 2-line guard in isWrapperFlagToken
// plus a defensive `token == ""` skip in
// unwrapCodexBinaryToken (already present from round 2).
// The test exercises the boundary cases.
func TestUnwrapCodexBinaryTokenRejectsEmptyTokens(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    string
	}{
		{
			name:    "empty quoted arg after env wrapper",
			command: `env "" codex`,
			// The walker must skip the empty token and
			// land on `codex`. Pre-fix: panic.
			want: "codex",
		},
		{
			name:    "empty quoted arg after sudo wrapper",
			command: `sudo "" codex`,
			want:    "codex",
		},
		{
			name:    "multiple consecutive spaces produce empty tokens",
			command: "sudo    codex",
			want:    "codex",
		},
		{
			name:    "tab-separated empty token",
			command: "env\t\tcodex",
			want:    "codex",
		},
		{
			name:    "empty command yields empty result",
			command: "",
			want:    "",
		},
		{
			name:    "all-empty tokens yield empty result",
			command: `"" "" ""`,
			want:    "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("unwrapCodexBinaryToken panicked on %q: %v", tc.command, r)
				}
			}()
			if got := unwrapCodexBinaryTokenForTest(tc.command); got != tc.want {
				t.Fatalf("unwrapCodexBinaryToken(%q) = %q, want %q", tc.command, got, tc.want)
			}
		})
	}
}

// TestRunPreflightDoesNotPanicOnMalformedCommand is the
// round-3 P3 end-to-end regression. The same malformed
// commands must not crash the preflight; they must return
// a non-success summary with a deterministic
// FailureReason. Without the round-3 fix, RunPreflight
// would panic at runtime when the operator's command
// happens to have a quoted empty arg.
func TestRunPreflightDoesNotPanicOnMalformedCommand(t *testing.T) {
	commands := []string{
		`env "" codex`,
		`sudo "" codex`,
		"sudo    codex",
		"env\t\tcodex",
		`"" "" ""`,
		"",
	}
	for _, cmd := range commands {
		t.Run(cmd, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("RunPreflight panicked on command %q: %v", cmd, r)
				}
			}()
			summary := RunPreflight(PreflightOptions{
				Command:       cmd,
				VersionOutput: "",
				Now:           fixedNow,
			})
			if summary.Available {
				t.Fatalf("RunPreflight(%q) returned Available=true, want false", cmd)
			}
			if summary.FailureReason == "" {
				t.Fatalf("RunPreflight(%q) returned empty FailureReason, want a deterministic value", cmd)
			}
		})
	}
}

// TestRunPreflightRejectsEmbeddedSentinelProtocolVersion is
// the round-3 P1 regression: a poisoned compatibility.json
// whose protocol_version embeds a synthetic-sentinel-shaped
// substring (rather than starting with one) is rejected
// at the source. The pre-round-3 detector only matched
// whole-string prefixes, so a fixture that planted
// `protocol-SYNTHETIC_PROMPT_BODY-v1` would have slipped
// through the gate. The word-boundary regex closes the
// gap.
func TestRunPreflightRejectsEmbeddedSentinelProtocolVersion(t *testing.T) {
	root := t.TempDir()
	version := "9.9.9-embedded-sentinel"
	schemaDir := filepath.Join(root, "schema", version)
	if err := os.MkdirAll(schemaDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "transcripts", version), 0o755); err != nil {
		t.Fatalf("mkdir transcripts: %v", err)
	}
	// protocol_version is poisoned with a sentinel embedded
	// in a longer identifier; codex_version matches the
	// detected version; everything else is structurally
	// valid.
	poisoned := `{
  "codex_version": "9.9.9-embedded-sentinel",
  "protocol_version": "protocol-SYNTHETIC_PROMPT_BODY-v1",
  "schema_version": "schema-embedded-v1",
  "supported_notifications": ["handoff"],
  "supported_requests": ["initialize"],
  "experimental_api": false
}`
	if err := os.WriteFile(filepath.Join(schemaDir, compatibilityMetadataFile), []byte(poisoned), 0o600); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	schemaStub := `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`
	if err := os.WriteFile(filepath.Join(schemaDir, "schema.json"), []byte(schemaStub), 0o600); err != nil {
		t.Fatalf("write schema stub: %v", err)
	}
	transcript := `{"type":"handshake","codex_version":"9.9.9-embedded-sentinel","protocol_version":"protocol-SYNTHETIC_PROMPT_BODY-v1","schema_version":"schema-embedded-v1","experimental_api":false}
{"type":"turn_completed"}
`
	if err := os.WriteFile(filepath.Join(root, "transcripts", version, "happy-path.jsonl"), []byte(transcript), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	summary := RunPreflight(PreflightOptions{
		Command:       "codex",
		VersionOutput: "codex 9.9.9-embedded-sentinel",
		FixtureRoot:   root,
		Now:           fixedNow,
	})
	if summary.Available {
		t.Fatalf("Available = true, want false (embedded sentinel in protocol_version must be rejected)")
	}
	if summary.FailureReason != ReasonMetadataProtoSentinel {
		t.Fatalf("FailureReason = %q, want %q", summary.FailureReason, ReasonMetadataProtoSentinel)
	}
	b, _ := json.Marshal(summary)
	body := string(b)
	if strings.Contains(body, "SYNTHETIC_PROMPT_BODY") {
		t.Fatalf("summary leaks embedded sentinel: %s", body)
	}
}

// TestPathPrefixFromCommandExtractsWrapperPATH is the
// round-3 P2 PATH-prefix extractor regression. The
// helper must surface the operator's wrapper PATH so
// lookPathWithWrapperPATH can resolve a codex binary
// that exists only on the operator's augmented PATH.
func TestPathPrefixFromCommandExtractsWrapperPATH(t *testing.T) {
	cases := []struct {
		command string
		want    string
	}{
		{command: "env PATH=/opt/codex/bin codex", want: "/opt/codex/bin"},
		{command: "PATH=/opt/codex/bin codex", want: "/opt/codex/bin"},
		{command: "env X=1 PATH=/a:/b codex", want: "/a:/b"},
		{command: "env PATH=/opt/bin sudo PATH=/usr/bin codex", want: "/usr/bin"},
		{command: "env X=1 codex", want: ""},
		{command: "codex", want: ""},
		{command: "", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			if got := pathPrefixFromCommandForTest(tc.command); got != tc.want {
				t.Fatalf("pathPrefixFromCommand(%q) = %q, want %q", tc.command, got, tc.want)
			}
		})
	}
}

// TestRunPreflightHonorsWrapperPATHForClassification is the
// round-3 P2 end-to-end regression: an operator who wraps
// codex with `env PATH=/tmp/codex-stub codex-stub` where
// `/tmp/codex-stub/codex-stub` is an executable file (a
// stub we create in TempDir) must be classified by
// `malformed_version` (because the stub returns empty
// output) — not `codex_not_installed`. Pre-round-3 the
// classifier would have LookPath'd against the
// process's PATH, which does not include
// `/tmp/codex-stub`, and reported the binary as missing.
func TestRunPreflightHonorsWrapperPATHForClassification(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("cannot run PATH-augmentation test without a go binary to anchor /tmp: %v", err)
	}
	// Create a stub codex binary in a fresh temp dir.
	// The stub prints nothing to stdout (empty --version
	// output), which is the malformed-version signal.
	stubDir := t.TempDir()
	stubPath := filepath.Join(stubDir, "codex-stub")
	stubScript := "#!/bin/sh\n# Pretend to be codex. We deliberately\n# emit empty --version so the preflight\n# classifier reaches the LookPath branch.\nexit 0\n"
	if err := os.WriteFile(stubPath, []byte(stubScript), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	summary := RunPreflight(PreflightOptions{
		Command:       "env PATH=" + stubDir + " codex-stub",
		VersionOutput: "",
		Now:           fixedNow,
	})
	if summary.Available {
		t.Fatalf("Available = true, want false (stub returns empty --version)")
	}
	// The stub exists and is executable; the augmented
	// PATH must let the classifier find it and report
	// malformed_version. Without the round-3 PATH
	// augmentation, the classifier would see
	// `codex_not_installed`.
	if summary.FailureReason != ReasonMalformedVersion {
		t.Fatalf("FailureReason = %q, want %q (PATH-augmented lookup must surface the wrapped codex binary)", summary.FailureReason, ReasonMalformedVersion)
	}
}

// TestVerifyPanicTeamLeadRepro is the team-lead repro of the
// round-3 P3 panic finding. The exact 'env "" codex' input
// that triggered runtime panic in isWrapperFlagToken must
// now return a deterministic result without panicking. The
// pre-flight path (RunPreflight end-to-end) must also not
// crash. Each sub-case recovers from panic so a future
// regression that re-introduces the index-out-of-range
// crash is caught even if the test runs in a process that
// would otherwise abort the suite.
func TestVerifyPanicTeamLeadRepro(t *testing.T) {
	t.Run("env_empty_quoted_arg_codex", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("TEAM-LEAD REPRO: unwrapCodexBinaryToken panicked on 'env \"\" codex': %v", r)
			}
		}()
		if got := unwrapCodexBinaryTokenForTest(`env "" codex`); got != "codex" {
			t.Fatalf("unwrapCodexBinaryToken(`env \"\" codex`) = %q, want %q", got, "codex")
		}
	})
	t.Run("isWrapperFlagToken_empty", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("TEAM-LEAD REPRO: isWrapperFlagToken(\"\") panicked: %v", r)
			}
		}()
		if isWrapperFlagToken("") {
			t.Fatalf("isWrapperFlagToken(\"\") = true, want false (empty token is not a flag)")
		}
	})
	t.Run("RunPreflight_end_to_end_no_panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("TEAM-LEAD REPRO: RunPreflight panicked on 'env \"\" codex': %v", r)
			}
		}()
		summary := RunPreflight(PreflightOptions{
			Command:       `env "" codex`,
			VersionOutput: "",
			Now:           fixedNow,
		})
		if summary.Available {
			t.Fatalf("RunPreflight(`env \"\" codex`) returned Available=true, want false")
		}
		if summary.FailureReason == "" {
			t.Fatalf("RunPreflight(`env \"\" codex`) returned empty FailureReason")
		}
	})
}

// TestVerifyEmbeddedSentinelTeamLeadRepro is the team-lead
// repro of the round-3 P1 embedded-sentinel finding,
// extended for the round-5 F3 sentinel-after-underscore
// finding. The detector must agree with the Python
// contract validator regex 'r"\bSYNTHETIC_[A-Z0-9_]+\b"'
// for the cases team-lead independently verified under
// the round-3 contract (embedded sentinels with '-' on
// either side match; plain versions do not), and the
// round-5 widening adds two more positive cases:
// `protocol_SYNTHETIC_PROMPT_BODY` (sentinel preceded by
// underscore) and `vSYNTHETIC_OWNER_NONCE` (sentinel
// preceded by a lowercase letter) are both flagged.
// The previous team-lead case
// `vSYNTHETIC_OWNER_NONCE: false` was the round-3
// word-boundary argument; the round-5 detector widens
// the prefix boundary to "not in [A-Z0-9]" so an
// underscore OR a lowercase letter before the `S` is
// also a valid boundary. This is the runtime gate's
// stricter position vs. the Python contract regex
// (which the contract validator uses for redaction
// golden fixture validation, a different threat
// surface).
func TestVerifyEmbeddedSentinelTeamLeadRepro(t *testing.T) {
	cases := []struct {
		value    string
		sentinel bool
	}{
		{value: "protocol-SYNTHETIC_PROMPT_BODY-v1", sentinel: true},
		{value: "schema-SYNTHETIC_LOG", sentinel: true},
		// Round-5 widening: a sentinel preceded by an
		// underscore or a lowercase letter is now a
		// positive match. The runtime gate treats
		// both as a valid prefix boundary.
		{value: "vSYNTHETIC_OWNER_NONCE", sentinel: true},
		{value: "protocol_SYNTHETIC_PROMPT_BODY", sentinel: true},
		{value: "schema_SYNTHETIC_API_SECRET", sentinel: true},
		{value: "protocol-test-v1", sentinel: false},
		{value: "1.2.3", sentinel: false},
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			if got := isSyntheticSentinelStringForTest(tc.value); got != tc.sentinel {
				t.Fatalf("isSyntheticSentinelString(%q) = %v, want %v", tc.value, got, tc.sentinel)
			}
		})
	}
}

// TestVerifyWrapperPATHOverrideTeamLeadRepro is the
// team-lead repro of the round-3 P2 PATH-augmentation
// finding. The stub is in t.TempDir() which is NOT on
// the process's PATH. The preflight must use the wrapped
// PATH to find the binary, classify it as
// ReasonMalformedVersion (because the stub returns empty
// --version), and not report ReasonCodexNotInstalled.
func TestVerifyWrapperPATHOverrideTeamLeadRepro(t *testing.T) {
	stubDir := t.TempDir()
	stubPath := stubDir + "/codex-stub"
	stubScript := "#!/bin/sh\n# Pretend to be codex. Empty --version\n# output to force the malformed_version branch.\nexit 0\n"
	if err := os.WriteFile(stubPath, []byte(stubScript), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	summary := RunPreflight(PreflightOptions{
		Command:       "env PATH=" + stubDir + " codex-stub",
		VersionOutput: "",
		Now:           fixedNow,
	})
	if summary.Available {
		t.Fatalf("RunPreflight returned Available=true, want false")
	}
	if summary.FailureReason != ReasonMalformedVersion {
		t.Fatalf("FailureReason = %q, want %q (PATH-augmented lookup must surface wrapped codex)", summary.FailureReason, ReasonMalformedVersion)
	}
	// Sanity: the failure message must NOT contain any
	// synthetic-sentinel-shaped substring; the augmented
	// PATH is a classification input, not a diagnostics
	// value.
	for _, sentinel := range []string{
		"SYNTHETIC_PROMPT_BODY",
		"SYNTHETIC_CODEX_LOG",
		"SYNTHETIC_OWNER_NONCE",
		"SYNTHETIC_API_SECRET",
	} {
		if strings.Contains(summary.FailureMessage, sentinel) {
			t.Fatalf("FailureMessage leaks sentinel %q: %q", sentinel, summary.FailureMessage)
		}
	}
}

// TestLookPathWithWrapperPATHHandlesAbsoluteBinary is the
// round-4 P2 regression: an absolute binary path (e.g.
// `/opt/codex/bin/codex`) must NEVER be resolved through
// PATH search. exec.LookPath's documented contract is
// "If file contains a slash, it is tried directly and
// the PATH is not consulted." The round-3 helper loop
// ignored that contract and built candidates like
// `<PATH-entry>//opt/codex/bin/codex`, which never
// resolve, so a real absolute codex binary was
// misclassified as ReasonCodexNotInstalled.
//
// The failing case the round-3 helper got wrong is the
// combination of a `PATH=<value>` prefix and an
// absolute binary: `Command: "env PATH=/tmp
// /opt/codex/bin/codex"` — the helper loop built
// `/tmp//opt/codex/bin/codex`, which never resolves.
func TestLookPathWithWrapperPATHHandlesAbsoluteBinary(t *testing.T) {
	dir := t.TempDir()
	binPath := dir + "/codex-abs"
	script := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	// The command carries a PATH= prefix that does
	// NOT contain the stub. The round-3 helper loop
	// walks PATH entries and concatenates the
	// separator + the absolute path, producing
	// entries like `<dir>//opt/.../codex-abs`
	// which never resolve. Pre-fix, the helper
	// reported exec.ErrNotFound. Post-fix, the
	// path-qualified binary must short-circuit
	// to a verbatim stat and resolve to the file.
	commandWithPATHPrefix := "env PATH=/does/not/exist " + binPath
	got, err := lookPathWithWrapperPATHForTest(binPath, commandWithPATHPrefix)
	if err != nil {
		t.Fatalf("lookPathWithWrapperPATH(%q, command=%q) returned error: %v", binPath, commandWithPATHPrefix, err)
	}
	if got == "" {
		t.Fatalf("lookPathWithWrapperPATH(%q, command=%q) returned empty path", binPath, commandWithPATHPrefix)
	}
	// The returned path should resolve to the same file.
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("returned path %q is not stat-able: %v", got, err)
	}
}

// TestLookPathWithWrapperPATHHandlesRelativeBinary is the
// round-4 P2 regression for the relative-path case. A
// command that uses `./codex-rel` as the binary name must
// resolve to the literal relative path, not be subject
// to PATH search. The round-3 helper built
// `<PATH-entry>//./codex-rel` which never resolves.
//
// We chdir into the directory holding the stub so the
// relative path is meaningful; the helper must stat the
// literal `./codex-rel` and find it.
func TestLookPathWithWrapperPATHHandlesRelativeBinary(t *testing.T) {
	dir := t.TempDir()
	binPath := dir + "/codex-rel"
	script := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	// Save CWD and chdir to the stub's dir so the
	// relative path is meaningful. Without this, a
	// relative path from a different CWD would
	// correctly return exec.ErrNotFound (the file
	// simply does not exist there), defeating the
	// test.
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() {
		_ = os.Chdir(originalCwd)
	}()
	relBin := "./codex-rel"
	got, err := lookPathWithWrapperPATHForTest(relBin, "")
	if err != nil {
		t.Fatalf("lookPathWithWrapperPATH(%q, command=\"\") returned error: %v", relBin, err)
	}
	if got == "" {
		t.Fatalf("lookPathWithWrapperPATH(%q, command=\"\") returned empty path", relBin)
	}
}

// TestRunPreflightClassifiesAbsoluteBinaryAsMalformedNotNotInstalled
// is the round-4 P2 end-to-end regression. An operator
// who invokes codex via an absolute path (a common
// pattern in CI / containers) must see the binary
// resolved and the classifier must report
// ReasonMalformedVersion (because the stub returns
// empty --version), NOT ReasonCodexNotInstalled. The
// round-3 helper falsely reported not-installed.
func TestRunPreflightClassifiesAbsoluteBinaryAsMalformedNotNotInstalled(t *testing.T) {
	binDir := t.TempDir()
	binPath := binDir + "/codex-abs"
	script := "#!/bin/sh\n# Pretend to be codex. Empty --version\n# output to force the malformed_version branch.\nexit 0\n"
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	// Run the absolute-path invocation directly. The
	// wrapper-prefix PATH augmentation is irrelevant
	// for an absolute path; the classifier must find
	// the binary regardless of the wrapper's PATH
	// value.
	summary := RunPreflight(PreflightOptions{
		Command:       binPath,
		VersionOutput: "",
		Now:           fixedNow,
	})
	if summary.Available {
		t.Fatalf("Available = true, want false (stub returns empty --version)")
	}
	if summary.FailureReason != ReasonMalformedVersion {
		t.Fatalf("FailureReason = %q, want %q (absolute-path binary must be classified as malformed, not not-installed)", summary.FailureReason, ReasonMalformedVersion)
	}
}
