package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"local-symphony/internal/config"
	"local-symphony/internal/db"
	"local-symphony/internal/httpapi"
	"local-symphony/internal/orchestrator"
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
	closeStore := func() { st.Close() }
	defer func() { closeStore() }()
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
	if err := prepareServeRuntime(st, ln, addr, token, os.Getpid()); err != nil {
		return err
	}
	defer st.RemoveRuntimeDescriptor()
	if err := st.ReconcileStaleActiveRuns(); err != nil {
		_ = ln.Close()
		removeCLISession(st)
		return err
	}
	wf, err := config.Load(st.RepoRoot)
	if err != nil {
		_ = ln.Close()
		removeCLISession(st)
		return err
	}
	srv := &http.Server{Handler: httpapi.New(st).Handler()}
	errs := make(chan error, 1)
	go func() { errs <- srv.Serve(ln) }()
	schedulerCtx, stopScheduler := context.WithCancel(context.Background())
	defer stopScheduler()
	schedulerDone, schedulerDrained := runSchedulerTickLoopWithDrain(schedulerCtx, schedulerTickInterval(wf, st.RepoRoot), func() error {
		return (orchestrator.Orchestrator{Store: st}).Tick()
	})
	defer func() {
		closeStore = func() {
			closeStoreAfterSchedulerDrain(schedulerDrained, st.Close)
		}
	}()
	fmt.Printf("Local Symphony serving %s\n", addr)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-sig:
		stopScheduler()
		<-schedulerDone
		_ = srv.Close()
		return nil
	case err := <-errs:
		stopScheduler()
		<-schedulerDone
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func runSchedulerTickLoop(ctx context.Context, interval time.Duration, tick func() error) <-chan error {
	done, _ := runSchedulerTickLoopWithDrain(ctx, interval, tick)
	return done
}

func schedulerTickInterval(wf *config.Workflow, repoRoot string) time.Duration {
	intervalMS := config.Defaults(repoRoot).Polling.IntervalMS
	if wf != nil && wf.Validation.Valid && wf.Config.Polling.IntervalMS >= 1000 {
		intervalMS = wf.Config.Polling.IntervalMS
	}
	return time.Duration(intervalMS) * time.Millisecond
}

func closeStoreAfterSchedulerDrain(drained <-chan struct{}, closeStore func()) {
	if drained != nil {
		<-drained
	}
	if closeStore != nil {
		closeStore()
	}
}

func runSchedulerTickLoopWithDrain(ctx context.Context, interval time.Duration, tick func() error) (<-chan error, <-chan struct{}) {
	done := make(chan error, 1)
	drained := make(chan struct{})
	var tickWG sync.WaitGroup
	go func() {
		<-done
		tickWG.Wait()
		close(drained)
	}()
	go func() {
		defer close(done)
		if interval <= 0 {
			interval = 30 * time.Second
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		tickDone := make(chan error, 1)
		tickInFlight := false
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if tickInFlight {
					continue
				}
				select {
				case <-ctx.Done():
					return
				default:
				}
				tickInFlight = true
				tickWG.Add(1)
				go func() {
					defer tickWG.Done()
					tickDone <- tick()
				}()
			case err := <-tickDone:
				tickInFlight = false
				if err != nil {
					fmt.Fprintf(os.Stderr, "scheduler tick error: %v\n", err)
				}
			}
		}
	}()
	return done, drained
}

func prepareServeRuntime(st *store.Store, ln net.Listener, addr, token string, pid int) error {
	if err := st.CreateRuntimeDescriptor(addr, addr, pid); err != nil {
		_ = ln.Close()
		return err
	}
	if err := writeCLISession(st, addr, token); err != nil {
		st.RemoveRuntimeDescriptorForPID(pid)
		_ = ln.Close()
		return err
	}
	return nil
}

func writeCLISession(st *store.Store, apiURL, token string) error {
	path := CLISessionPath(st.ProjectID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
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
	if err := os.WriteFile(path, b, 0o600); err != nil {
		removeCLISession(st)
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		removeCLISession(st)
		return err
	}
	return nil
}

func removeCLISession(st *store.Store) {
	_ = os.Remove(CLISessionPath(st.ProjectID))
	_ = st.App.Exec(`DELETE FROM local_sessions WHERE id=? AND project_id=? AND kind='cli'`, "cli_"+st.ProjectID, st.ProjectID)
}

func CLISessionPath(projectID string) string {
	return filepath.Join(symphonyHomeDir(), ".symphony", "cli-sessions", db.ProjectScopedJSONFileName(projectID))
}

func LegacyCLISessionPath() string {
	return filepath.Join(symphonyHomeDir(), ".symphony", "cli-session.json")
}

func symphonyHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return os.TempDir()
	}
	return home
}

func RuntimeDescriptor(projectID string) (map[string]any, error) {
	b, err := os.ReadFile(db.RuntimeDescriptorPath(projectID))
	if err != nil {
		return nil, err
	}
	var m map[string]any
	err = json.Unmarshal(b, &m)
	return m, err
}
