package codex

import (
	"encoding/json"
	"os"
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

func TestRunPreflightRedactsSyntheticSentinelsFromDetails(t *testing.T) {
	root := t.TempDir()
	// Inject a poisoned compatibility.json whose SupportedNotifications
	// carries a raw-prompt sentinel. The redaction policy must strip
	// it from the FailureDetails map before diagnostics export.
	version := "9.9.9-poison"
	schemaDir := filepath.Join(root, "schema", version)
	if err := os.MkdirAll(schemaDir, 0o755); err != nil {
		t.Fatalf("mkdir schema: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "transcripts", version), 0o755); err != nil {
		t.Fatalf("mkdir transcripts: %v", err)
	}
	poisoned := `{
  "codex_version": "9.9.9-poison",
  "protocol_version": "protocol-poison-v1",
  "schema_version": "schema-poison-v1",
  "supported_notifications": ["` + syntheticPromptBody + `", "handoff"],
  "supported_requests": ["` + syntheticCodexLog + `"],
  "experimental_api": false
}`
	if err := os.WriteFile(filepath.Join(schemaDir, compatibilityMetadataFile), []byte(poisoned), 0o600); err != nil {
		t.Fatalf("write poisoned metadata: %v", err)
	}
	summary := RunPreflight(PreflightOptions{
		Command:       "codex-fake",
		VersionOutput: "codex 9.9.9-poison",
		FixtureRoot:   root,
		Now:           fixedNow,
	})
	if summary.Available {
		t.Fatalf("Available = true, want false (poisoned metadata should fail validation)")
	}
	// The poisoned compatibility.json will trip malformed_metadata
	// (the SupportedNotifications now contains a non-conventional
	// value but actually... it's still a string array; it will
	// proceed to the schema check and we should expect
	// missing_schema_fixture because we did not create schema.json).
	// Either way, the failure details must not contain the sentinel.
	b, _ := json.Marshal(summary.FailureDetails)
	body := string(b)
	for _, sentinel := range []string{syntheticPromptBody, syntheticCodexLog, syntheticSecret} {
		if strings.Contains(body, sentinel) {
			t.Fatalf("FailureDetails leaks sentinel %q in: %s", sentinel, body)
		}
	}
	if summary.FailureMessage != "" && strings.Contains(summary.FailureMessage, syntheticPromptBody) {
		t.Fatalf("FailureMessage leaks sentinel: %q", summary.FailureMessage)
	}
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
