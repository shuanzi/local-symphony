package observability

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"local-symphony/internal/store"
)

func TestExportDoesNotAllowProjectIDPathTraversal(t *testing.T) {
	st, err := store.InitProject(t.TempDir(), "LOC")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	st.ProjectID = "../../../outside"

	path, err := Export(st)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	root := filepath.Join(st.RepoRoot, ".symphony", "exports")
	rel, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatalf("relative export path: %v", err)
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") {
		t.Fatalf("diagnostics export escaped base: base=%q path=%q rel=%q", root, path, rel)
	}
	if got := filepath.Base(path); !strings.HasPrefix(got, "diagnostics-project_") || strings.Contains(got, "..") || strings.ContainsAny(got, `/\`) {
		t.Fatalf("diagnostics export filename is not sanitized: %q", got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat diagnostics export: %v", err)
	}
}

func TestDiagnosticsReadsSchemaVersionsFromStore(t *testing.T) {
	st, err := store.InitProject(t.TempDir(), "LOC")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Project.Exec(`UPDATE schema_meta SET value='2' WHERE key='schema_version'`); err != nil {
		t.Fatalf("update project schema version: %v", err)
	}

	diag := Diagnostics(st)
	database, ok := diag["database"].(map[string]any)
	if !ok {
		t.Fatalf("database diagnostics has type %T, want map[string]any", diag["database"])
	}
	if got := database["app_schema_version"]; got != "1" {
		t.Fatalf("app_schema_version = %v, want 1", got)
	}
	if got := database["app_version_status"]; got != "supported" {
		t.Fatalf("app_version_status = %v, want supported", got)
	}
	if got := database["project_schema_version"]; got != "2" {
		t.Fatalf("project_schema_version = %v, want 2", got)
	}
	if got := database["project_version_status"]; got != "unsupported" {
		t.Fatalf("project_version_status = %v, want unsupported", got)
	}
}

func TestDiagnosticsIncludesStoredRuntimeDescriptorWithoutSecrets(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	st, err := store.InitProject(t.TempDir(), "LOC")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.CreateRuntimeDescriptor("http://127.0.0.1:1111", "http://127.0.0.1:2222", os.Getpid()); err != nil {
		t.Fatalf("CreateRuntimeDescriptor: %v", err)
	}

	diag := Diagnostics(st)
	daemon, ok := diag["daemon"].(map[string]any)
	if !ok {
		t.Fatalf("daemon diagnostics has type %T, want map[string]any", diag["daemon"])
	}
	desc, ok := daemon["runtime_descriptor"].(map[string]any)
	if !ok {
		t.Fatalf("runtime_descriptor has type %T, want map[string]any", daemon["runtime_descriptor"])
	}
	if got := desc["api_url"]; got != "http://127.0.0.1:1111" {
		t.Fatalf("runtime descriptor api_url = %v, want stored value", got)
	}
	if got := desc["tool_gateway_endpoint"]; got != "http://127.0.0.1:2222" {
		t.Fatalf("runtime descriptor tool_gateway_endpoint = %v, want stored value", got)
	}
	if got := intValue(desc["daemon_pid"]); got != os.Getpid() {
		t.Fatalf("runtime descriptor daemon_pid = %d, want %d", got, os.Getpid())
	}
	fp, ok := desc["owner_nonce_fingerprint"].(string)
	if !ok || len(fp) != 8 {
		t.Fatalf("runtime descriptor owner_nonce_fingerprint = %v, want 8-char fingerprint", desc["owner_nonce_fingerprint"])
	}
	if desc["heartbeat_at"] == nil {
		t.Fatalf("runtime descriptor heartbeat_at missing")
	}
	if desc["heartbeat_ttl_ms"] == nil {
		t.Fatalf("runtime descriptor heartbeat_ttl_ms missing")
	}
	if desc["acquired_at"] == nil {
		t.Fatalf("runtime descriptor acquired_at missing")
	}
	assertNoSensitiveRuntimeDescriptorFields(t, desc)
}

func intValue(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

func assertNoSensitiveRuntimeDescriptorFields(t *testing.T, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal runtime descriptor: %v", err)
	}
	var walk func(any)
	walk = func(node any) {
		t.Helper()
		switch x := node.(type) {
		case map[string]any:
			for k, child := range x {
				key := strings.ToLower(k)
				if key == "token" || key == "owner_token" || strings.Contains(key, "secret") || key == "owner_nonce" {
					t.Fatalf("runtime descriptor contains sensitive field %q in %s", k, string(b))
				}
				walk(child)
			}
		case []any:
			for _, child := range x {
				walk(child)
			}
		}
	}
	walk(v)
}

// TestDiagnosticsDefaultRuntimeDescriptorIsSchemaValid exercises the
// no-serve path: when no runtime descriptor row exists (CLI invocation
// before any daemon has run), the diagnostics output must still emit
// the full DiagnosticsRuntimeDescriptor shape with null values for the
// nonce-related fields. This is the regression test for C3 review P2.
func TestDiagnosticsDefaultRuntimeDescriptorIsSchemaValid(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	st, err := store.InitProject(t.TempDir(), "LOC")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)

	diag := Diagnostics(st)
	daemon, ok := diag["daemon"].(map[string]any)
	if !ok {
		t.Fatalf("daemon diagnostics has type %T, want map[string]any", diag["daemon"])
	}
	desc, ok := daemon["runtime_descriptor"].(map[string]any)
	if !ok {
		t.Fatalf("runtime_descriptor has type %T, want map[string]any", daemon["runtime_descriptor"])
	}
	for _, field := range []string{
		"api_url",
		"tool_gateway_endpoint",
		"daemon_pid",
		"acquired_at",
		"heartbeat_at",
		"heartbeat_ttl_ms",
		"owner_nonce_fingerprint",
	} {
		if _, ok := desc[field]; !ok {
			t.Fatalf("runtime descriptor default projection missing %q; have keys %v", field, mapKeys(desc))
		}
	}
	if got := desc["api_url"]; got != nil {
		t.Fatalf("runtime descriptor default api_url = %v, want nil", got)
	}
	if got := desc["tool_gateway_endpoint"]; got != nil {
		t.Fatalf("runtime descriptor default tool_gateway_endpoint = %v, want nil", got)
	}
	if got := desc["daemon_pid"]; got != nil {
		t.Fatalf("runtime descriptor default daemon_pid = %v, want nil", got)
	}
	if got := desc["owner_nonce_fingerprint"]; got != nil {
		t.Fatalf("runtime descriptor default owner_nonce_fingerprint = %v, want nil", got)
	}
	assertNoSensitiveRuntimeDescriptorFields(t, desc)
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestDiagnosticsCodexAvailabilityShapeIsContract(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	st, err := store.InitProject(t.TempDir(), "LOC")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	diag := Diagnostics(st)
	codexField, ok := diag["codex"].(map[string]any)
	if !ok {
		t.Fatalf("diag[codex] is %T, want map[string]any", diag["codex"])
	}
	assertDiagnosticsFieldShape(t, codexField)
}

func TestDiagnosticsCodexAvailabilityDoesNotLeakSentinels(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	st, err := store.InitProject(t.TempDir(), "LOC")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	diag := Diagnostics(st)
	// The Diagnostics payload itself must never carry synthetic
	// sentinels. The redactedFailureDetails scrubber in the
	// adapter layer handles the source path; the diagnostics
	// layer is the second line of defence and must not
	// re-introduce a leak surface.
	assertNoSentinelsInDiagnostics(t, diag)
}

func TestDiagnosticsExportDoesNotLeakCodexSentinels(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	st, err := store.InitProject(t.TempDir(), "LOC")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	path, err := Export(st)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	body := string(data)
	for _, sentinel := range AllRedactionSentinels() {
		if strings.Contains(body, sentinel) {
			t.Fatalf("diagnostics export %q leaks sentinel %q: %s", path, sentinel, body)
		}
	}
}

// TestDiagnosticsPreflightSuccessHasNullFailureReason is the
// round-5 (PR #24 review) F1 regression: a successful preflight
// must surface `failure_reason` as JSON null in the diagnostics
// envelope, not as the empty string. The diagnostics schema and
// OpenAPI declare `failure_reason` as an enum of canonical
// failure codes; the empty string is NOT a member of that enum,
// so a healthy Codex installation that runs through
// `CodexAvailability` would fail contract validation if the
// field is left as the zero value. The fix: when
// `summary.FailureReason` is empty (success path), the
// projection must put nil into the map (which serializes to
// `null` under encoding/json).
func TestDiagnosticsPreflightSuccessHasNullFailureReason(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// Point the preflight at the codex package's hermetic
	// fixtures so the test exercises the SUCCESS path: an
	// installed-looking codex binary (via
	// SYMPHONY_CODEX_VERSION_OUTPUT) whose compatibility
	// metadata parses cleanly. Without this, the preflight
	// fails with `missing_fixture` and the failure_reason
	// enum value masks the empty-string bug we are trying
	// to catch. We also set a default codex command so the
	// preflight's not-installed classifier does not fire
	// (the env var below makes the version probe return a
	// valid value).
	t.Setenv("SYMPHONY_CODEX_FIXTURE_ROOT", codexFixtureRootForTest(t))
	t.Setenv("SYMPHONY_CODEX_VERSION_OUTPUT", "codex 0.0.0-test")
	st, err := store.InitProject(t.TempDir(), "LOC")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	diag := Diagnostics(st)
	codexField, ok := diag["codex"].(map[string]any)
	if !ok {
		t.Fatalf("diag[codex] is %T, want map[string]any", diag["codex"])
	}
	preflight, ok := codexField["last_preflight"].(map[string]any)
	if !ok {
		t.Fatalf("diag[codex].last_preflight is %T, want map[string]any", codexField["last_preflight"])
	}
	reason, present := preflight["failure_reason"]
	if !present {
		t.Fatalf("last_preflight.failure_reason missing from diagnostics envelope")
	}
	// The success path must produce JSON null (Go nil), not
	// the empty string. Any other value (including "") fails
	// the contract validator's enum check.
	if reason != nil {
		t.Fatalf("last_preflight.failure_reason = %v (%T), want nil (JSON null) on success", reason, reason)
	}
	// Also assert via JSON round-trip so we know the value
	// is serialized as the JSON null literal, not omitted
	// or "null"-the-string.
	body, _ := json.Marshal(preflight)
	if !strings.Contains(string(body), `"failure_reason":null`) {
		t.Fatalf("last_preflight JSON must carry failure_reason:null on success; got %s", string(body))
	}
}

// TestDiagnosticsPreflightReadsWorkflowCodexConfig is the
// round-5 (PR #24 review) F2 regression: the diagnostics
// preflight must use the operator-configured WORKFLOW.md
// `codex.command` and `codex.experimental_api` values, not
// the hard-coded defaults. The previous implementation called
// `codex.RunPreflight(codex.PreflightOptions{})` with no
// args, so the preflight always probed `codex` and required
// `experimental_api=false`, which is the OPPOSITE of what
// the actual run path uses. A workflow that points
// `codex.command` at a custom binary (or expects
// `experimental_api=true`) would see diagnostics report
// "Codex unavailable" while the run path actually worked,
// or vice versa. The fix threads the workflow config into
// the preflight options.
//
// Test strategy: the workflow sets
// `codex.experimental_api=true`, the fixture has
// `experimental_api=false`. The preflight must honour the
// workflow setting and report
// `experimental_api_not_supported`. If the preflight falls
// back to the default `ExperimentalAPI=false`, it would
// succeed (because the fixture's `experimental_api=false`
// matches the preflight's `ExperimentalAPI=false`) and the
// diagnostics would show `available=true` — the exact
// wrong answer the round-5 review called out.
func TestDiagnosticsPreflightReadsWorkflowCodexConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// Copy the codex package's hermetic fixtures into a
	// temp dir so the preflight walks the success path of
	// `SelectFixtureMetadata`. The fixture's
	// `experimental_api` is `false`; the workflow will
	// request `experimental_api=true` so the propagation
	// check has a real signal (failure vs. success).
	fixtureRoot := t.TempDir()
	src := codexFixtureRootForTest(t)
	copyFixtureDir(t, src, fixtureRoot)
	t.Setenv("SYMPHONY_CODEX_FIXTURE_ROOT", fixtureRoot)
	// Use a version the test fixture supports. The
	// testdata fixture is for `0.0.0-test`; we make the
	// version probe report that version.
	t.Setenv("SYMPHONY_CODEX_VERSION_OUTPUT", "codex 0.0.0-test")
	root := t.TempDir()
	// WORKFLOW.md: experimental_api=true is the key
	// config the diagnostics layer was ignoring.
	workflow := `---
tracker:
  kind: local
  dispatch_candidate_states: [Ready]
codex:
  command: codex
  experimental_api: true
---
prompt body
`
	if err := os.WriteFile(filepath.Join(root, "WORKFLOW.md"), []byte(workflow), 0o600); err != nil {
		t.Fatalf("write WORKFLOW.md: %v", err)
	}
	st, err := store.InitProject(root, "LOC")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	diag := Diagnostics(st)
	codexField, ok := diag["codex"].(map[string]any)
	if !ok {
		t.Fatalf("diag[codex] is %T, want map[string]any", diag["codex"])
	}
	preflight, ok := codexField["last_preflight"].(map[string]any)
	if !ok {
		t.Fatalf("diag[codex].last_preflight is %T, want map[string]any", codexField["last_preflight"])
	}
	// The preflight must surface the failure that the
	// workflow's `experimental_api=true` triggers against
	// a fixture that has `experimental_api=false`. The
	// preflight reason is the union of workflow config
	// and fixture state: if the workflow setting is
	// ignored, the preflight would either succeed (if the
	// preflight defaults to `experimental_api=false`)
	// or fall back to a different failure code (if it
	// probed a different binary).
	reason, _ := preflight["failure_reason"].(string)
	if reason != "experimental_api_not_supported" {
		t.Fatalf("last_preflight.failure_reason = %q, want %q (workflow experimental_api=true must propagate to preflight)", reason, "experimental_api_not_supported")
	}
}

// copyFixtureDir copies a directory tree from src to dst
// recursively. Used by the F2 regression to spin up a
// private copy of the codex package's hermetic fixtures so
// the test does not mutate package-internal state.
func copyFixtureDir(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("mkdir dst: %v", err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("readdir %s: %v", src, err)
	}
	for _, e := range entries {
		srcPath := filepath.Join(src, e.Name())
		dstPath := filepath.Join(dst, e.Name())
		if e.IsDir() {
			copyFixtureDir(t, srcPath, dstPath)
			continue
		}
		data, err := os.ReadFile(srcPath)
		if err != nil {
			t.Fatalf("read %s: %v", srcPath, err)
		}
		if err := os.WriteFile(dstPath, data, 0o600); err != nil {
			t.Fatalf("write %s: %v", dstPath, err)
		}
	}
}

func TestDiagnosticsCodexAvailabilityRedactsPoisonedFixture(t *testing.T) {
	// Build a poisoned fixture under SYMPHONY_CODEX_FIXTURE_ROOT
	// whose compatibility.json's supported_notifications list
	// contains a synthetic-prompt sentinel. The diagnostics
	// envelope must scrub the sentinel from any field that
	// surfaces the value (last_preflight.failure_message,
	// last_preflight.failure_details, metadata, etc.).
	poisonRoot := t.TempDir()
	t.Setenv("SYMPHONY_CODEX_FIXTURE_ROOT", poisonRoot)
	version := "9.9.9-poison"
	schemaDir := filepath.Join(poisonRoot, "schema", version)
	if err := os.MkdirAll(schemaDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(poisonRoot, "transcripts", version), 0o755); err != nil {
		t.Fatalf("mkdir transcripts: %v", err)
	}
	poisoned := `{
  "codex_version": "9.9.9-poison",
  "protocol_version": "protocol-poison-v1",
  "schema_version": "schema-poison-v1",
  "supported_notifications": ["` + RedactionSentinelPromptBody + `"],
  "supported_requests": ["` + RedactionSentinelCodexLog + `"],
  "experimental_api": false
}`
	if err := os.WriteFile(filepath.Join(schemaDir, "compatibility.json"), []byte(poisoned), 0o600); err != nil {
		t.Fatalf("write poisoned metadata: %v", err)
	}
	t.Setenv("HOME", t.TempDir())
	st, err := store.InitProject(t.TempDir(), "LOC")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	diag := Diagnostics(st)
	b, _ := json.Marshal(diag)
	body := string(b)
	for _, sentinel := range AllRedactionSentinels() {
		if strings.Contains(body, sentinel) {
			t.Fatalf("diagnostics envelope leaks sentinel %q: %s", sentinel, body)
		}
	}
}

// codexFixtureRootForTest returns the absolute path of the
// codex package's hermetic testdata fixture root. The
// diagnostics tests use this to drive the preflight down the
// SUCCESS path (compatibility metadata parses cleanly, no
// missing-fixture / missing-metadata failure). The codex
// package's testdata directory is the canonical source of
// "happy-path" fixtures and is consulted via
// SYMPHONY_CODEX_FIXTURE_ROOT so the test never depends on
// the operator's installed codex or fixture layout.
func codexFixtureRootForTest(t *testing.T) string {
	t.Helper()
	// Walk up from this test's package to find the codex
	// package's testdata directory. The diagnostics package
	// sits at internal/observability, the codex package at
	// internal/agent/codex; the relative path is
	// ../agent/codex/testdata.
	abs, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	candidate := filepath.Join(abs, "..", "agent", "codex", "testdata")
	if _, err := os.Stat(candidate); err != nil {
		t.Fatalf("codex testdata not found at %s: %v", candidate, err)
	}
	return candidate
}
