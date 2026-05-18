package app

import (
	"testing"

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
