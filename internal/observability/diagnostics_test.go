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
