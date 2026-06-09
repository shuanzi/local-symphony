package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"local-symphony/internal/store"
)

// TestStatusDataIncludesCodexAvailabilityOnSuccess exercises the
// happy path where the repo-local Codex fixture is present and the
// diagnostics.codex block must surface available=true with
// protocol_version / schema_version populated.
func TestStatusDataIncludesCodexAvailabilityOnSuccess(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	if err := copyFixtureForTest(t, root); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	t.Setenv("SYMPHONY_CODEX_FIXTURE_ROOT", filepath.Join(root, "fixtures"))
	// SYMPHONY_CODEX_VERSION_OUTPUT short-circuits the version
	// probe in codex.RunPreflight; we cannot rely on a real
	// codex binary in the test sandbox.
	t.Setenv("SYMPHONY_CODEX_VERSION_OUTPUT", "codex 0.0.0-test")
	st, err := store.InitProject(root, "LOC")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	out, err := statusData(st)
	if err != nil {
		t.Fatalf("statusData: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("statusData returned %T, want map[string]any", out)
	}
	codex, ok := m["codex"].(map[string]any)
	if !ok {
		t.Fatalf("status.codex is %T, want map[string]any", m["codex"])
	}
	if got := codex["available"]; got != true {
		t.Fatalf("status.codex.available = %v, want true", got)
	}
	if got := codex["version"]; got != "0.0.0-test" {
		t.Fatalf("status.codex.version = %v, want 0.0.0-test", got)
	}
	support, ok := codex["support"].(map[string]any)
	if !ok {
		t.Fatalf("status.codex.support is %T, want map[string]any", codex["support"])
	}
	if got := support["cli"]; got != "supported" {
		t.Fatalf("status.codex.support.cli = %v, want supported", got)
	}
	metadata, ok := codex["metadata"].(map[string]any)
	if !ok || metadata == nil {
		t.Fatalf("status.codex.metadata is %v, want populated map", codex["metadata"])
	}
	if got := metadata["protocol_version"]; got != "protocol-test-v1" {
		t.Fatalf("status.codex.metadata.protocol_version = %v, want protocol-test-v1", got)
	}
	fixture, ok := codex["fixture_support"].(map[string]any)
	if !ok {
		t.Fatalf("status.codex.fixture_support is %T, want map[string]any", codex["fixture_support"])
	}
	if got := fixture["transcript_available"]; got != true {
		t.Fatalf("status.codex.fixture_support.transcript_available = %v, want true", got)
	}
	if got := codex["warning"]; got != nil {
		t.Fatalf("status.codex.warning = %v, want nil on success", got)
	}
}

// TestStatusDataReportsUnsupportedCodexVersion confirms the operator
// sees the unsupported_codex_version warning and a populated
// last_preflight block when the fixture root is empty.
func TestStatusDataReportsUnsupportedCodexVersion(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_CODEX_FIXTURE_ROOT", t.TempDir())
	t.Setenv("SYMPHONY_CODEX_VERSION_OUTPUT", "codex 9.9.9-test")
	st, err := store.InitProject(t.TempDir(), "LOC")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	out, err := statusData(st)
	if err != nil {
		t.Fatalf("statusData: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("statusData returned %T, want map[string]any", out)
	}
	codex, ok := m["codex"].(map[string]any)
	if !ok {
		t.Fatalf("status.codex is %T, want map[string]any", m["codex"])
	}
	if got := codex["available"]; got != false {
		t.Fatalf("status.codex.available = %v, want false", got)
	}
	if got := codex["warning"]; got != "unsupported_codex_version" {
		t.Fatalf("status.codex.warning = %v, want unsupported_codex_version", got)
	}
	preflight, ok := codex["last_preflight"].(map[string]any)
	if !ok {
		t.Fatalf("status.codex.last_preflight is %T, want map[string]any", codex["last_preflight"])
	}
	if got := preflight["failure_code"]; got != "unsupported_codex_version" {
		t.Fatalf("status.codex.last_preflight.failure_code = %v, want unsupported_codex_version", got)
	}
	if got := preflight["failure_reason"]; got == "" || got == nil {
		t.Fatalf("status.codex.last_preflight.failure_reason is empty")
	}
}

// TestStatusDataRedactsSentinels confirms the status JSON never
// surfaces synthetic-sentinel-shaped content (raw prompt / raw
// Codex log / raw secret) under any failure path.
func TestStatusDataRedactsSentinels(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_CODEX_FIXTURE_ROOT", t.TempDir())
	t.Setenv("SYMPHONY_CODEX_VERSION_OUTPUT", "codex 9.9.9-poison")
	st, err := store.InitProject(t.TempDir(), "LOC")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	out, err := statusData(st)
	if err != nil {
		t.Fatalf("statusData: %v", err)
	}
	b, _ := json.Marshal(out)
	body := string(b)
	for _, sentinel := range []string{
		"SYNTHETIC_PROMPT_BODY",
		"SYNTHETIC_CODEX_LOG",
		"SYNTHETIC_OWNER_NONCE",
		"SYNTHETIC_API_SECRET",
	} {
		if strings.Contains(body, sentinel) {
			t.Fatalf("status JSON leaks sentinel %q: %s", sentinel, body)
		}
	}
}

// TestStatusSubcommandExposesCodexAvailability goes through the
// full Main dispatcher to make sure the public command surface
// (`symphony status --project <root>`) returns the new codex field
// inside the envelope.
func TestStatusSubcommandExposesCodexAvailability(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	if err := copyFixtureForTest(t, root); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	t.Setenv("SYMPHONY_CODEX_FIXTURE_ROOT", filepath.Join(root, "fixtures"))
	t.Setenv("SYMPHONY_CODEX_VERSION_OUTPUT", "codex 0.0.0-test")
	st, err := store.InitProject(root, "LOC")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	st.Close()
	code, stdout, stderr := captureCLIOutput(t, func() int {
		return Main([]string{"status", "--project", root})
	})
	if code != 0 {
		t.Fatalf("status exit code = %d, want 0 (stderr=%s)", code, stderr)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("status stdout is not JSON: %v\nstdout=%s", err, stdout)
	}
	data, ok := payload["data"].(map[string]any)
	if !ok {
		// The local-store statusData path does not wrap in an
		// envelope; treat the top-level map as the data.
		data = payload
	}
	codex, ok := data["codex"].(map[string]any)
	if !ok {
		t.Fatalf("status payload has no codex block: %s", stdout)
	}
	if got := codex["available"]; got != true {
		t.Fatalf("status.codex.available = %v, want true", got)
	}
}

// copyFixtureForTest copies the testdata fixture tree the agent
// codex package ships with into <root>/fixtures. Tests that need
// a known-good compatibility.json point
// SYMPHONY_CODEX_FIXTURE_ROOT at the resulting directory.
func copyFixtureForTest(t *testing.T, root string) error {
	t.Helper()
	src, err := codexTestdataDir()
	if err != nil {
		return err
	}
	dst := filepath.Join(root, "fixtures")
	for _, sub := range []string{"schema/0.0.0-test", "transcripts/0.0.0-test"} {
		if err := os.MkdirAll(filepath.Join(dst, sub), 0o755); err != nil {
			return err
		}
	}
	for _, sub := range []string{
		"schema/0.0.0-test/compatibility.json",
		"schema/0.0.0-test/schema.json",
		"transcripts/0.0.0-test/happy-path.jsonl",
	} {
		data, err := os.ReadFile(filepath.Join(src, sub))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dst, sub), data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// codexTestdataDir resolves the internal/agent/codex/testdata
// directory of the working copy. We use the working copy rather
// than the caller's module cache so the test is hermetic and
// matches what `go test ./...` from the repo root sees.
func codexTestdataDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(wd, "..", "agent", "codex", "testdata")
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		return "", os.ErrNotExist
	}
	return abs, nil
}
