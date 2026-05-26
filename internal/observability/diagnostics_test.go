package observability

import (
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
