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
				if key == "token" || key == "owner_token" || strings.Contains(key, "secret") {
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
