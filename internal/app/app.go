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
	"local-symphony/internal/core"
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
	// HeartbeatIntervalMS overrides the runtime owner heartbeat interval
	// (default 5000ms). When zero, heartbeatConfig resolves the default.
	// Production code paths should leave this unset; tests use it to
	// exercise heartbeat-lost shutdown within a short window.
	HeartbeatIntervalMS int
	// ShutdownContext lets callers stop Serve by cancelling a context
	// rather than signalling the process. Tests use this to avoid
	// raising SIGINT against the entire `go test` process (which on
	// slow CI, or if Serve fails before reaching signal.Notify, can
	// abort the whole test suite). Production callers leave this nil
	// and rely on SIGINT/SIGTERM.
	ShutdownContext context.Context
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
	// Reap any stale runtime owner (heartbeat-stale or dead PID) before
	// we attempt to acquire. This must run before listener bind so a fresh
	// daemon is never told to wait on a defunct lock.
	if _, err := st.ReapStaleRuntimeDescriptors(); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, opts.Port))
	if err != nil {
		return err
	}
	addr := "http://" + ln.Addr().String()
	token := security.NewToken()
	nonce, err := store.NewOwnerNonce()
	if err != nil {
		_ = ln.Close()
		return err
	}
	if err := prepareServeRuntime(st, ln, addr, token, os.Getpid(), nonce); err != nil {
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
		// Gate dispatch on the current owner nonce. If the row has
		// been reaped or superseded (e.g. another daemon took over
		// after we missed a heartbeat), the nonce we hold is no
		// longer the recorded one. Return early so we do not
		// dispatch runs concurrently with the new owner; the
		// heartbeat goroutine will surface the ownership loss to
		// the main loop and trigger graceful shutdown.
		if err := verifyOwnerNonceForDispatch(st, nonce); err != nil {
			return err
		}
		return (orchestrator.Orchestrator{Store: st}).Tick()
	})
	heartbeatCtx, stopHeartbeat := context.WithCancel(context.Background())
	defer stopHeartbeat()
	heartbeatInterval, heartbeatTTL := heartbeatConfig(wf, opts.HeartbeatIntervalMS)
	heartbeatDone, heartbeatErrCh := runRuntimeHeartbeatLoop(heartbeatCtx, heartbeatInterval, heartbeatTTL, st, nonce)
	defer func() {
		stopHeartbeat()
		<-heartbeatDone
	}()
	// Periodic reap covers projects we do not currently own. Reaping
	// happens at a slower cadence than our own heartbeat to amortize the
	// cost across the app DB.
	reapCtx, stopReap := context.WithCancel(context.Background())
	defer stopReap()
	reapDone := runRuntimeReapLoop(reapCtx, reapInterval(), st)
	defer func() {
		stopReap()
		<-reapDone
	}()
	defer func() {
		closeStore = func() {
			closeStoreAfterSchedulerDrain(schedulerDrained, st.Close)
		}
	}()
	fmt.Printf("Local Symphony serving %s\n", addr)
	sig := make(chan os.Signal, 1)
	if opts.ShutdownContext == nil {
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		defer signal.Stop(sig)
	}
	select {
	case <-sig:
		stopReap()
		<-reapDone
		stopHeartbeat()
		<-heartbeatDone
		stopScheduler()
		<-schedulerDone
		_ = srv.Close()
		return nil
	case <-shutdownChannel(opts.ShutdownContext):
		// Caller-supplied shutdown signal (test hook). Same cleanup
		// sequence as SIGINT; the caller stays in control of how
		// the test process is affected.
		stopReap()
		<-reapDone
		stopHeartbeat()
		<-heartbeatDone
		stopScheduler()
		<-schedulerDone
		_ = srv.Close()
		return nil
	case err := <-errs:
		stopReap()
		<-reapDone
		stopHeartbeat()
		<-heartbeatDone
		stopScheduler()
		<-schedulerDone
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	case hbErr := <-heartbeatErrCh:
		// Heartbeat ownership was lost (reaped by another owner, DB
		// error, etc.). Stop the HTTP server and scheduler so we never
		// dispatch concurrently with the new owner, then return an
		// APIError so the operator sees a non-zero exit and can
		// investigate. We preserve the original *APIError type when
		// present so exit code mapping (WP-4) keeps working.
		stopReap()
		<-reapDone
		stopHeartbeat()
		<-heartbeatDone
		stopScheduler()
		<-schedulerDone
		_ = srv.Close()
		if apiErr := core.AsAPIError(hbErr); apiErr != nil && apiErr.Code != core.ErrInternal {
			return core.NewError(apiErr.Code, "runtime heartbeat ownership lost: "+apiErr.Message, apiErr.Details)
		}
		return fmt.Errorf("runtime heartbeat ownership lost: %w", hbErr)
	}
}

// shutdownChannel returns a channel that is closed when the supplied
// context is cancelled. When ctx is nil it returns a never-closing
// channel so the select case is effectively disabled.
func shutdownChannel(ctx context.Context) <-chan struct{} {
	if ctx == nil {
		return nil
	}
	return ctx.Done()
}

// reapInterval is the cadence for the periodic reap goroutine. The default
// of 60s balances observability (an old lock is cleared within one minute
// of going stale) with the cost of a full table scan.
func reapInterval() time.Duration {
	return time.Duration(store.DefaultRuntimeReapIntervalMS) * time.Millisecond
}

// verifyOwnerNonceForDispatch is the scheduler tick gate. It returns nil
// if the recorded owner_nonce still matches the nonce the daemon acquired
// with, ErrDaemonAlreadyRunning otherwise. The check is intentionally
// cheap (one indexed lookup) so it can run on every tick without
// measurable overhead; it is the C3 round-3 review P1 fix that closes
// the window where a stale owner could still dispatch after another
// daemon has reaped/taken over but before the heartbeat ticker noticed.
func verifyOwnerNonceForDispatch(st *store.Store, nonce string) error {
	currentNonce, err := st.GetRuntimeOwnerNonce()
	if err != nil {
		return fmt.Errorf("runtime owner nonce lookup: %w", err)
	}
	if currentNonce != nonce {
		return core.NewError(core.ErrDaemonAlreadyRunning, "runtime owner nonce changed before tick; suppressing dispatch", map[string]any{
			"project_id": st.ProjectID,
		})
	}
	return nil
}

func runRuntimeReapLoop(ctx context.Context, interval time.Duration, st *store.Store) <-chan struct{} {
	done := make(chan struct{})
	if interval <= 0 {
		interval = time.Duration(store.DefaultRuntimeReapIntervalMS) * time.Millisecond
	}
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := st.ReapStaleRuntimeDescriptors(); err != nil {
					fmt.Fprintf(os.Stderr, "runtime reap error: %v\n", err)
				}
			}
		}
	}()
	return done
}

// heartbeatConfig resolves the heartbeat interval/ttl. The default is
// interval=5s, ttl=30s (a 6x safety margin so transient stalls do not
// cause a takeover). Future workflow.yaml keys can override these.
// The overrideMS argument is the test/production override (zero means
// "use the default"); it is kept separate from wf so the function
// signature does not depend on workflow loading order.
func heartbeatConfig(wf *config.Workflow, overrideMS int) (interval time.Duration, ttlMS int) {
	interval = time.Duration(store.DefaultRuntimeHeartbeatIntervalMS) * time.Millisecond
	if overrideMS > 0 {
		interval = time.Duration(overrideMS) * time.Millisecond
	}
	ttlMS = store.DefaultRuntimeHeartbeatTTLMS
	return interval, ttlMS
}

func runRuntimeHeartbeatLoop(ctx context.Context, interval time.Duration, ttlMS int, st *store.Store, nonce string) (<-chan struct{}, <-chan error) {
	done := make(chan struct{})
	errCh := make(chan error, 1)
	if interval <= 0 {
		interval = time.Duration(store.DefaultRuntimeHeartbeatIntervalMS) * time.Millisecond
	}
	if ttlMS <= 0 {
		ttlMS = store.DefaultRuntimeHeartbeatTTLMS
	}
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := st.UpdateRuntimeHeartbeat(st.ProjectID, nonce, ttlMS); err != nil {
					// Heartbeat ownership was lost. Surface the error so
					// Serve can shut down — staying up would mean two
					// daemons dispatching work, which is exactly what
					// C3 single-owner guard is meant to prevent.
					if apiErr := core.AsAPIError(err); apiErr != nil {
						fmt.Fprintf(os.Stderr, "runtime heartbeat lost: %s (%s)\n", apiErr.Code, apiErr.Message)
					} else {
						fmt.Fprintf(os.Stderr, "runtime heartbeat lost: %v\n", err)
					}
					errCh <- err
					return
				}
			}
		}
	}()
	return done, errCh
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

func prepareServeRuntime(st *store.Store, ln net.Listener, addr, token string, pid int, nonce string) error {
	if err := st.CreateRuntimeDescriptorWithNonce(addr, addr, pid, nonce, store.DefaultRuntimeHeartbeatTTLMS); err != nil {
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
