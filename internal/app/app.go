package app

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"local-symphony/internal/db"
	"local-symphony/internal/httpapi"
	"local-symphony/internal/security"
	"local-symphony/internal/store"
)

type ServeOptions struct {
	Project string
	Host    string
	Port    int
	NoOpen  bool
}

func Serve(opts ServeOptions) error {
	st, err := store.Open(opts.Project)
	if err != nil {
		return err
	}
	defer st.Close()
	_ = st.ReconcileStaleActiveRuns()
	host := opts.Host
	if host == "" {
		host = "127.0.0.1"
	}
	if host != "127.0.0.1" && host != "localhost" {
		return fmt.Errorf("v1 API must bind to loopback")
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, opts.Port))
	if err != nil {
		return err
	}
	addr := "http://" + ln.Addr().String()
	token := security.NewToken()
	if err := writeCLISession(st, addr, token); err != nil {
		_ = ln.Close()
		return err
	}
	_ = st.CreateRuntimeDescriptor(addr, addr, os.Getpid())
	defer st.RemoveRuntimeDescriptor()
	srv := &http.Server{Handler: httpapi.New(st).Handler()}
	errs := make(chan error, 1)
	go func() { errs <- srv.Serve(ln) }()
	fmt.Printf("Local Symphony serving %s\n", addr)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-sig:
		_ = srv.Close()
		return nil
	case err := <-errs:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func writeCLISession(st *store.Store, apiURL, token string) error {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.TempDir()
	}
	path := filepath.Join(home, ".symphony", "cli-session.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	payload := map[string]any{"project_id": st.ProjectID, "repo_root": st.RepoRoot, "api_url": apiURL, "token": token, "created_at": "redacted"}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	if err := st.App.Exec(`INSERT OR REPLACE INTO local_sessions(id,project_id,kind,token_hash,user_label,created_at) VALUES(?,?,?,?,?,?)`, "cli_"+st.ProjectID, st.ProjectID, "cli", security.HashToken(token), "local-cli", "redacted"); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

func RuntimeDescriptor(projectID string) (map[string]any, error) {
	b, err := os.ReadFile(filepath.Join(db.RuntimeDir(), projectID+".json"))
	if err != nil {
		return nil, err
	}
	var m map[string]any
	err = json.Unmarshal(b, &m)
	return m, err
}
