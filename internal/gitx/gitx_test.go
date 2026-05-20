package gitx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyWorkspaceCopiesGitPrefixProjectFilesAndSkipsInternalDirs(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")

	writeTestFile(t, src, ".gitignore", "ignored\n")
	writeTestFile(t, src, ".gitattributes", "* text=auto\n")
	writeTestFile(t, src, ".gitmodules", "[submodule]\n")
	writeTestFile(t, src, ".github/workflows/build.yml", "name: build\n")
	writeTestFile(t, src, ".git/config", "[core]\n")
	writeTestFile(t, src, ".symphony/state.json", "{}\n")

	if err := copyWorkspace(src, dst); err != nil {
		t.Fatalf("copyWorkspace() error = %v", err)
	}

	for rel, want := range map[string]string{
		".gitignore":                  "ignored\n",
		".gitattributes":              "* text=auto\n",
		".gitmodules":                 "[submodule]\n",
		".github/workflows/build.yml": "name: build\n",
	} {
		got, err := os.ReadFile(filepath.Join(dst, rel))
		if err != nil {
			t.Fatalf("expected %s to be copied: %v", rel, err)
		}
		if string(got) != want {
			t.Fatalf("%s content = %q, want %q", rel, got, want)
		}
	}

	for _, rel := range []string{".git/config", ".symphony/state.json"} {
		if _, err := os.Stat(filepath.Join(dst, rel)); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be skipped, stat error = %v", rel, err)
		}
	}
}

func TestCopyWorkspaceExistingDestinationReturnsErrorAndPreservesContents(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")

	writeTestFile(t, src, "file.txt", "new\n")
	writeTestFile(t, dst, "file.txt", "old\n")

	if err := copyWorkspace(src, dst); err == nil {
		t.Fatal("copyWorkspace() error = nil, want existing destination error")
	}

	got, err := os.ReadFile(filepath.Join(dst, "file.txt"))
	if err != nil {
		t.Fatalf("read existing destination file: %v", err)
	}
	if string(got) != "old\n" {
		t.Fatalf("existing destination file = %q, want preserved old content", got)
	}
}

func writeTestFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}
