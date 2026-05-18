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
