package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"local-symphony/internal/db"
	"local-symphony/internal/store"
)

func TestWriteCLISessionPropagatesAppDBInsertError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	st, err := store.InitProject(t.TempDir(), "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.App.Close(); err != nil {
		t.Fatalf("close app db: %v", err)
	}

	err = writeCLISession(st, "http://127.0.0.1:1", "test-token")
	if err == nil {
		t.Fatal("writeCLISession succeeded, want app DB insert error")
	}
}

func TestWriteCLISessionWritesProjectScopedSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	st, err := store.InitProject(t.TempDir(), "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)

	if err := writeCLISession(st, "http://127.0.0.1:1", "test-token"); err != nil {
		t.Fatalf("writeCLISession: %v", err)
	}

	path := filepath.Join(home, ".symphony", "cli-sessions", st.ProjectID+".json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read project cli session: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat project cli session: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("session mode = %o, want 600", got)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat project cli session dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("session dir mode = %o, want 700", got)
	}
	var session struct {
		ProjectID string `json:"project_id"`
		Token     string `json:"token"`
	}
	if err := json.Unmarshal(b, &session); err != nil {
		t.Fatalf("unmarshal project cli session: %v", err)
	}
	if session.ProjectID != st.ProjectID || session.Token != "test-token" {
		t.Fatalf("session = %#v, want project %q token test-token", session, st.ProjectID)
	}
}

func TestCLISessionPathDoesNotAllowProjectIDPathTraversal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	base := filepath.Join(home, ".symphony", "cli-sessions")

	path := CLISessionPath("../../cli-session")
	rel, err := filepath.Rel(base, path)
	if err != nil {
		t.Fatalf("relative session path: %v", err)
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") {
		t.Fatalf("session path escaped base: base=%q path=%q rel=%q", base, path, rel)
	}
	if got := filepath.Base(path); !strings.HasPrefix(got, "project_") || strings.Contains(got, "..") || strings.ContainsAny(got, `/\`) {
		t.Fatalf("session filename is not sanitized: %q", got)
	}
}

func TestRuntimeDescriptorPreservesSafeProjectIDFileName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	st, err := store.InitProject(t.TempDir(), "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	st.ProjectID = "prj_abc123"

	if err := st.CreateRuntimeDescriptor("http://127.0.0.1:1", "http://127.0.0.1:2", 1234); err != nil {
		t.Fatalf("CreateRuntimeDescriptor: %v", err)
	}
	path := filepath.Join(db.RuntimeDir(), "prj_abc123.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("safe runtime descriptor path not written: %v", err)
	}
	if _, err := RuntimeDescriptor(st.ProjectID); err != nil {
		t.Fatalf("RuntimeDescriptor: %v", err)
	}
	st.RemoveRuntimeDescriptor()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("runtime descriptor still exists after remove: %v", err)
	}
}

func TestRuntimeDescriptorDoesNotAllowProjectIDPathTraversal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	st, err := store.InitProject(t.TempDir(), "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	st.ProjectID = "../../outside-runtime"

	if err := st.CreateRuntimeDescriptor("http://127.0.0.1:1", "http://127.0.0.1:2", 1234); err != nil {
		t.Fatalf("CreateRuntimeDescriptor: %v", err)
	}
	base := db.RuntimeDir()
	unsafePath := filepath.Join(base, st.ProjectID+".json")
	if _, err := os.Stat(unsafePath); err == nil {
		t.Fatalf("runtime descriptor escaped base: base=%q path=%q", base, unsafePath)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat escaped runtime descriptor: %v", err)
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatalf("read runtime dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("runtime dir entries = %d, want 1", len(entries))
	}
	name := entries[0].Name()
	if !strings.HasPrefix(name, "project_") || !strings.HasSuffix(name, ".json") || strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
		t.Fatalf("runtime descriptor filename is not sanitized: %q", name)
	}
	path := filepath.Join(base, name)
	desc, err := RuntimeDescriptor(st.ProjectID)
	if err != nil {
		t.Fatalf("RuntimeDescriptor: %v", err)
	}
	if desc["project_id"] != st.ProjectID {
		t.Fatalf("runtime descriptor project_id = %v, want %q", desc["project_id"], st.ProjectID)
	}
	st.RemoveRuntimeDescriptor()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("runtime descriptor still exists after remove: %v", err)
	}
}
