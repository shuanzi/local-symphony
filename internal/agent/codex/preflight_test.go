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
// the round-2 P1 #1 negative-coverage regression: legitimate
// version / protocol / schema identifiers must not be flagged
// as sentinel-shaped, and synthetic-sentinel-shaped values must
// be flagged.
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
		// Synthetic-sentinel-shaped values — must be flagged.
		{value: "SYNTHETIC_FOO", sentinel: true},
		{value: "SYNTHETIC_PROMPT_BODY", sentinel: true},
		{value: "SYNTHETIC_OWNER_NONCE_XYZ", sentinel: true},
		{value: "SYNTHETIC_API_SECRET_KEY", sentinel: true},
		// Bare prefix is not a sentinel.
		{value: "SYNTHETIC_", sentinel: false},
		// Lowercase prefix is not a sentinel.
		{value: "synthetic_foo", sentinel: false},
		// Mixed-case trailing suffix is not a sentinel —
		// the detector requires the UPPER_SNAKE_CASE shape
		// because the contract validator's
		// SYNTHETIC_SENTINEL_RE uses the same shape, and
		// the redaction scrubber in
		// observability/assertNoSentinelsInDiagnostics
		// relies on the same boundary.
		{value: "SYNTHETIC_PROMPT_BODY_do_not_leak", sentinel: false},
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
