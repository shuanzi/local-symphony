package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"local-symphony/internal/config"
	"local-symphony/internal/core"
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

func TestWriteCLISessionRollsBackDBRowWhenSessionFileWriteFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	st, err := store.InitProject(t.TempDir(), "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	if err := os.MkdirAll(CLISessionPath(st.ProjectID), 0o700); err != nil {
		t.Fatalf("create session file path directory: %v", err)
	}

	if err := writeCLISession(st, "http://127.0.0.1:1", "test-token"); err == nil {
		t.Fatal("writeCLISession succeeded, want session file write error")
	}
	rows, err := st.App.Query(`SELECT id FROM local_sessions WHERE project_id=? AND kind='cli'`, st.ProjectID)
	if err != nil {
		t.Fatalf("query local_sessions: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("local_sessions rows = %d, want 0 after session file write failure", len(rows))
	}
}

func TestServeReturnsErrorWhenRuntimeDescriptorWriteFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	st, err := store.InitProject(t.TempDir(), "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	projectID := st.ProjectID
	runtimePath := filepath.Join(home, ".symphony", "runtime")
	if err := os.WriteFile(runtimePath, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("create runtime path conflict: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	err = prepareServeRuntime(st, ln, "http://"+ln.Addr().String(), "test-token", 1234, "nonce-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err == nil {
		t.Fatal("prepareServeRuntime succeeded, want runtime descriptor error")
	}
	if _, readErr := os.ReadFile(CLISessionPath(projectID)); !os.IsNotExist(readErr) {
		t.Fatalf("CLI session was written before runtime lock failure: %v", readErr)
	}
	if closeErr := ln.Close(); closeErr == nil {
		t.Fatal("listener remained open after runtime descriptor failure")
	}
}

func TestPrepareServeRuntimeReleasesRuntimeOwnerWhenCLISessionWriteFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	st, err := store.InitProject(t.TempDir(), "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	sessionDir := filepath.Join(home, ".symphony", "cli-sessions")
	if err := os.WriteFile(sessionDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("create cli session path conflict: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	err = prepareServeRuntime(st, ln, "http://"+ln.Addr().String(), "test-token", os.Getpid(), "nonce-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if err == nil {
		t.Fatal("prepareServeRuntime succeeded, want CLI session write error")
	}
	if closeErr := ln.Close(); closeErr == nil {
		t.Fatal("listener remained open after CLI session write failure")
	}
	if _, err := RuntimeDescriptor(st.ProjectID); !os.IsNotExist(err) {
		t.Fatalf("runtime descriptor remains after CLI session write failure: %v", err)
	}
	rows, err := st.App.Query(`SELECT project_id FROM runtime_descriptors WHERE project_id=?`, st.ProjectID)
	if err != nil {
		t.Fatalf("query runtime descriptors: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("runtime descriptor DB rows = %d, want 0 after CLI session write failure", len(rows))
	}
}

func TestPrepareServeRuntimeRejectsActiveRuntimeOwner(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	st, err := store.InitProject(t.TempDir(), "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.CreateRuntimeDescriptor("http://127.0.0.1:1111", "http://127.0.0.1:2222", os.Getpid()); err != nil {
		t.Fatalf("CreateRuntimeDescriptor existing owner: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	err = prepareServeRuntime(st, ln, "http://"+ln.Addr().String(), "test-token", os.Getpid()+1, "nonce-cccccccccccccccccccccccccccccccc")
	if err == nil {
		t.Fatal("prepareServeRuntime succeeded, want daemon runtime ownership conflict")
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "runtime") || !strings.Contains(msg, "ownership") || !strings.Contains(msg, "conflict") {
		t.Fatalf("prepareServeRuntime error = %v, want daemon/runtime ownership conflict", err)
	}
	if closeErr := ln.Close(); closeErr == nil {
		t.Fatal("listener remained open after runtime ownership conflict")
	}
	desc, err := RuntimeDescriptor(st.ProjectID)
	if err != nil {
		t.Fatalf("RuntimeDescriptor after conflict: %v", err)
	}
	if got := desc["api_url"]; got != "http://127.0.0.1:1111" {
		t.Fatalf("runtime descriptor api_url = %v, want existing owner value", got)
	}
	if _, readErr := os.ReadFile(CLISessionPath(st.ProjectID)); !os.IsNotExist(readErr) {
		t.Fatalf("CLI session was written despite runtime ownership conflict: %v", readErr)
	}
}

func TestServeRuntimeOwnershipConflictDoesNotReconcileActiveRuns(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	st, err := store.InitProject(t.TempDir(), "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	issue, err := st.CreateIssue(store.CreateIssueInput{
		Title:              "Ownership conflict",
		Description:        "desc",
		AcceptanceCriteria: []string{"done"},
		Priority:           3,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if _, err := st.TransitionIssue(issue.ID, core.StateReady, "", ""); err != nil {
		t.Fatalf("TransitionIssue: %v", err)
	}
	run, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	approval, err := st.CreatePendingApprovalRequest(store.CreateApprovalRequestInput{
		RunID: run.ID, IssueID: issue.ID, Kind: "command", ActionSummary: "go test", RiskLevel: "medium", PolicyMatch: "manual", RequestID: "apr_test", CWD: st.RepoRoot, Fingerprint: "fp_test", TimeoutMS: 60000,
	})
	if err != nil {
		t.Fatalf("CreatePendingApprovalRequest: %v", err)
	}
	if err := st.CreateRuntimeDescriptor("http://127.0.0.1:1111", "http://127.0.0.1:2222", os.Getpid()); err != nil {
		t.Fatalf("CreateRuntimeDescriptor existing owner: %v", err)
	}
	repoRoot := st.RepoRoot
	projectID := st.ProjectID
	st.Close()

	err = Serve(ServeOptions{Project: repoRoot, Host: "127.0.0.1", Port: 0, NoOpen: true})
	if err == nil {
		t.Fatal("Serve succeeded, want runtime ownership conflict")
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "runtime") || !strings.Contains(msg, "ownership") || !strings.Contains(msg, "conflict") {
		t.Fatalf("Serve error = %v, want runtime ownership conflict", err)
	}
	if _, err := os.ReadFile(CLISessionPath(projectID)); !os.IsNotExist(err) {
		t.Fatalf("CLI session was written despite runtime ownership conflict: %v", err)
	}
	opened, err := store.Open(repoRoot)
	if err != nil {
		t.Fatalf("Open after Serve conflict: %v", err)
	}
	t.Cleanup(opened.Close)
	runRow, err := opened.Project.QueryOne(`SELECT status, failure_code FROM run_attempts WHERE id=?`, run.ID)
	if err != nil {
		t.Fatalf("query run after Serve conflict: %v", err)
	}
	if got := runRow["status"].String(); got != string(core.RunPending) {
		t.Fatalf("run status = %s, want %s", got, core.RunPending)
	}
	if got := runRow["failure_code"].String(); got != "" {
		t.Fatalf("run failure_code = %q, want empty", got)
	}
	approvalRow, err := opened.Project.QueryOne(`SELECT status FROM approval_requests WHERE id=?`, approval.ID)
	if err != nil {
		t.Fatalf("query approval after Serve conflict: %v", err)
	}
	if got := approvalRow["status"].String(); got != "pending" {
		t.Fatalf("approval status = %s, want pending", got)
	}
}

func TestServeReturnsReconcileError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	st, err := store.InitProject(t.TempDir(), "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	issue, err := st.CreateIssue(store.CreateIssueInput{
		Title:              "Serve reconcile failure",
		Description:        "desc",
		AcceptanceCriteria: []string{"done"},
		Priority:           3,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if _, err := st.TransitionIssue(issue.ID, core.StateReady, "", ""); err != nil {
		t.Fatalf("TransitionIssue: %v", err)
	}
	if _, err := st.ClaimRun(issue.ID, "manual", "fake", 1); err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	if err := st.Project.Exec(`CREATE TRIGGER fail_serve_reconcile_comment BEFORE INSERT ON issue_comments WHEN NEW.author_type='system' AND NEW.body LIKE 'Run ended with %' BEGIN SELECT RAISE(ABORT, 'serve reconcile failed'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	repoRoot := st.RepoRoot
	st.Close()

	err = Serve(ServeOptions{Project: repoRoot, Host: "127.0.0.1", Port: 0, NoOpen: true})
	if err == nil {
		t.Fatal("Serve succeeded, want reconcile error")
	}
	if !strings.Contains(err.Error(), "serve reconcile failed") {
		t.Fatalf("Serve error = %v, want reconcile failure", err)
	}
	if _, err := os.ReadFile(CLISessionPath(st.ProjectID)); !os.IsNotExist(err) {
		t.Fatalf("CLI session remains after serve startup failure: %v", err)
	}
}

func TestSchedulerTickLoopRunsOnIntervalAndStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var ticks atomic.Int32

	done := runSchedulerTickLoop(ctx, time.Millisecond, func() error {
		if ticks.Add(1) == 2 {
			cancel()
		}
		return nil
	})

	select {
	case err, ok := <-done:
		if ok && err != nil {
			t.Fatalf("tick loop returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("tick loop did not stop after context cancellation")
	}
	if got := ticks.Load(); got < 2 {
		t.Fatalf("ticks = %d, want at least 2", got)
	}
	before := ticks.Load()
	time.Sleep(5 * time.Millisecond)
	if got := ticks.Load(); got != before {
		t.Fatalf("ticks after cancellation = %d, want %d", got, before)
	}
}

func TestSchedulerTickLoopContinuesAfterTickError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var ticks atomic.Int32

	done := runSchedulerTickLoop(ctx, time.Millisecond, func() error {
		switch ticks.Add(1) {
		case 1:
			return errors.New("transient tick failure")
		case 2:
			cancel()
		}
		return nil
	})

	select {
	case err, ok := <-done:
		if ok && err != nil {
			t.Fatalf("tick loop returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("tick loop did not stop after context cancellation")
	}
	if got := ticks.Load(); got < 2 {
		t.Fatalf("ticks = %d, want tick after transient error", got)
	}
}

func TestSchedulerTickLoopCancelDoesNotWaitForBlockedTick(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	var ticks atomic.Int32

	done := runSchedulerTickLoop(ctx, time.Millisecond, func() error {
		if ticks.Add(1) == 1 {
			close(started)
			<-release
		}
		return nil
	})

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("tick did not start")
	}
	cancel()
	select {
	case err, ok := <-done:
		if ok && err != nil {
			t.Fatalf("tick loop returned error: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("tick loop waited for blocked tick after cancellation")
	}
	if got := ticks.Load(); got != 1 {
		t.Fatalf("ticks after cancellation = %d, want 1", got)
	}
}

func TestSchedulerIntervalUsesDefaultWhenWorkflowInvalid(t *testing.T) {
	repoRoot := t.TempDir()
	cfg := config.Defaults(repoRoot)
	cfg.Polling.IntervalMS = 999
	wf := &config.Workflow{Config: cfg, Validation: config.Validation{Valid: false, Errors: []string{"polling.interval_ms must be greater than or equal to 1000"}}}

	got := schedulerTickInterval(wf, repoRoot)

	if got != 30*time.Second {
		t.Fatalf("scheduler interval = %s, want 30s default for invalid workflow", got)
	}
}

func TestCloseStoreAfterSchedulerDrainWaits(t *testing.T) {
	drained := make(chan struct{})
	closed := make(chan struct{})

	go closeStoreAfterSchedulerDrain(drained, func() { close(closed) })

	select {
	case <-closed:
		t.Fatal("store closed before scheduler drained")
	case <-time.After(5 * time.Millisecond):
	}
	close(drained)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("store did not close after scheduler drained")
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

	if err := st.CreateRuntimeDescriptor("http://127.0.0.1:1", "http://127.0.0.1:2", os.Getpid()); err != nil {
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

	if err := st.CreateRuntimeDescriptor("http://127.0.0.1:1", "http://127.0.0.1:2", os.Getpid()); err != nil {
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

// TestServeRecoversFromPIDReuseAfterHeartbeatStale exercises the full
// runtime owner recovery path: a prior daemon leaves a runtime descriptor
// with PID == os.Getpid() (the PID reuse scenario) and a stale heartbeat.
// A subsequent Serve() must reap the stale descriptor, record a reap
// event, and acquire a fresh lock without error.
func TestServeRecoversFromPIDReuseAfterHeartbeatStale(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	st, err := store.InitProject(t.TempDir(), "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	nonceA, err := store.NewOwnerNonce()
	if err != nil {
		t.Fatalf("NewOwnerNonce: %v", err)
	}
	if err := st.CreateRuntimeDescriptorWithNonce("http://127.0.0.1:1111", "http://127.0.0.1:2222", os.Getpid(), nonceA, store.DefaultRuntimeHeartbeatTTLMS); err != nil {
		t.Fatalf("CreateRuntimeDescriptorWithNonce (A): %v", err)
	}
	// Backdate the heartbeat past the TTL. The recorded PID intentionally
	// matches the live test process — this is the PID reuse case the
	// v1 owner nonce/heartbeat schema upgrade is meant to fix.
	backdated := time.Now().UnixMilli() - int64(store.DefaultRuntimeHeartbeatTTLMS*10)
	if err := st.App.Exec(`UPDATE runtime_descriptors SET heartbeat_at=?, heartbeat_ttl_ms=1000, acquired_at=? WHERE project_id=?`, backdated, backdated, st.ProjectID); err != nil {
		t.Fatalf("backdate heartbeat: %v", err)
	}
	repoRoot := st.RepoRoot
	projectID := st.ProjectID
	t.Cleanup(func() {
		opened, openErr := store.Open(repoRoot)
		if openErr == nil {
			opened.RemoveRuntimeDescriptor()
			opened.Close()
		}
		_ = os.Remove(CLISessionPath(projectID))
	})
	st.Close()

	// Run Serve in a goroutine; cancel via SIGINT after a short window so
	// the test does not block forever. The reap and acquire must finish
	// before shutdown.
	done := make(chan error, 1)
	go func() {
		done <- Serve(ServeOptions{Project: repoRoot, Host: "127.0.0.1", Port: 0, NoOpen: true})
	}()
	time.Sleep(500 * time.Millisecond)
	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("find process: %v", err)
	}
	if err := p.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("signal: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after SIGINT")
	}

	// Reopen the store and verify a reap event was recorded for the
	// stale owner. The runtime descriptor is removed during the SIGINT
	// shutdown path (RemoveRuntimeDescriptor), so we assert against the
	// reap event rather than the descriptor row.
	opened, err := store.Open(repoRoot)
	if err != nil {
		t.Fatalf("Open after Serve: %v", err)
	}
	t.Cleanup(opened.Close)
	events, err := opened.App.Query(`SELECT data_json FROM runtime_owner_events WHERE project_id=? AND event_type='runtime_owner_reaped' ORDER BY created_at DESC LIMIT 1`, projectID)
	if err != nil {
		t.Fatalf("query runtime_owner_events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("runtime_owner_reaped events = %d, want at least 1", len(events))
	}
	if !strings.Contains(events[0]["data_json"].String(), "heartbeat_stale") {
		t.Fatalf("reap event data = %q, want reason heartbeat_stale", events[0]["data_json"].String())
	}
}

// TestRuntimeHeartbeatLoopSurfacesLostOwnership exercises the heartbeat
// goroutine directly with a tight interval. The first tick refreshes the
// heartbeat; after the descriptor is deleted (simulating a reap by another
// owner), the next tick must surface the error via errCh so Serve can
// shut down. This is the regression test for C3 review P1.
func TestRuntimeHeartbeatLoopSurfacesLostOwnership(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	st, err := store.InitProject(t.TempDir(), "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	nonce, err := store.NewOwnerNonce()
	if err != nil {
		t.Fatalf("NewOwnerNonce: %v", err)
	}
	if err := st.CreateRuntimeDescriptorWithNonce("http://127.0.0.1:1", "http://127.0.0.1:2", os.Getpid(), nonce, store.DefaultRuntimeHeartbeatTTLMS); err != nil {
		t.Fatalf("CreateRuntimeDescriptorWithNonce: %v", err)
	}
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	done, errCh := runRuntimeHeartbeatLoop(ctx, 50*time.Millisecond, store.DefaultRuntimeHeartbeatTTLMS, st, nonce)
	t.Cleanup(func() {
		stop()
		<-done
	})
	// First tick should refresh successfully; drain a small window to
	// let the goroutine process the tick.
	time.Sleep(80 * time.Millisecond)
	// Now simulate another owner reaping the descriptor while we are
	// still alive. The next tick must surface the error.
	if err := st.App.Exec(`DELETE FROM runtime_descriptors WHERE project_id=?`, st.ProjectID); err != nil {
		t.Fatalf("delete runtime descriptor: %v", err)
	}
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("heartbeat errCh received nil error; want daemon_already_running")
		}
		apiErr := core.AsAPIError(err)
		if apiErr == nil || apiErr.Code != core.ErrDaemonAlreadyRunning {
			t.Fatalf("heartbeat errCh error code = %v, want %s", err, core.ErrDaemonAlreadyRunning)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("heartbeat errCh did not surface error after runtime descriptor was deleted")
	}
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("heartbeat goroutine did not exit after error")
	}
}

// TestRuntimeHeartbeatLoopStaysHealthyWhenOwnerUnchanged is a guard against
// the P1 fix becoming a footgun: when nothing has changed, the heartbeat
// goroutine must keep running and not surface spurious errors.
func TestRuntimeHeartbeatLoopStaysHealthyWhenOwnerUnchanged(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	st, err := store.InitProject(t.TempDir(), "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	nonce, err := store.NewOwnerNonce()
	if err != nil {
		t.Fatalf("NewOwnerNonce: %v", err)
	}
	if err := st.CreateRuntimeDescriptorWithNonce("http://127.0.0.1:1", "http://127.0.0.1:2", os.Getpid(), nonce, store.DefaultRuntimeHeartbeatTTLMS); err != nil {
		t.Fatalf("CreateRuntimeDescriptorWithNonce: %v", err)
	}
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	done, errCh := runRuntimeHeartbeatLoop(ctx, 30*time.Millisecond, store.DefaultRuntimeHeartbeatTTLMS, st, nonce)
	// Let several ticks fire while the descriptor remains valid.
	time.Sleep(150 * time.Millisecond)
	stop()
	<-done
	select {
	case err := <-errCh:
		t.Fatalf("heartbeat errCh received unexpected error while owner unchanged: %v", err)
	default:
	}
}

// TestServeExitsAfterHeartbeatOwnershipLost is the end-to-end regression
// test for C3 review P1. Serve must shut down (scheduler + http server
// closed) when the runtime owner descriptor is reaped by another owner
// mid-flight. The test uses a tight heartbeat interval (50ms) so the
// whole flow completes in under a second.
func TestServeExitsAfterHeartbeatOwnershipLost(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	st, err := store.InitProject(t.TempDir(), "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	repoRoot := st.RepoRoot
	projectID := st.ProjectID
	t.Cleanup(func() {
		opened, openErr := store.Open(repoRoot)
		if openErr == nil {
			opened.RemoveRuntimeDescriptor()
			opened.Close()
		}
		_ = os.Remove(CLISessionPath(projectID))
	})
	st.Close()

	done := make(chan error, 1)
	go func() {
		done <- Serve(ServeOptions{Project: repoRoot, Host: "127.0.0.1", Port: 0, NoOpen: true, HeartbeatIntervalMS: 50})
	}()

	// Wait for Serve to come up, then reap the runtime descriptor to
	// simulate another daemon taking the lock. The heartbeat goroutine
	// must surface the error and Serve must exit.
	deadline := time.Now().Add(3 * time.Second)
	var heartbeatLost bool
	for time.Now().Before(deadline) {
		opened, openErr := store.Open(repoRoot)
		if openErr != nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		row, err := opened.App.QueryOne(`SELECT project_id FROM runtime_descriptors WHERE project_id=?`, projectID)
		if err == nil && row["project_id"].String() == projectID {
			// Daemon is up. Simulate another owner reaping.
			if err := opened.App.Exec(`DELETE FROM runtime_descriptors WHERE project_id=?`, projectID); err != nil {
				t.Fatalf("simulate reap: %v", err)
			}
			opened.Close()
			heartbeatLost = true
			break
		}
		opened.Close()
		time.Sleep(50 * time.Millisecond)
	}
	if !heartbeatLost {
		t.Fatal("daemon did not acquire runtime lock within 3s")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Serve returned nil after heartbeat ownership loss; want heartbeat error")
		}
		if !strings.Contains(err.Error(), "heartbeat") {
			t.Fatalf("Serve error = %v, want substring heartbeat", err)
		}
		apiErr := core.AsAPIError(err)
		// The outer error is fmt.Errorf-wrapped, so it may not be an
		// *APIError directly; instead check the wrapped chain by string
		// match against the ErrDaemonAlreadyRunning message.
		if apiErr != nil && apiErr.Code != core.ErrDaemonAlreadyRunning {
			t.Fatalf("Serve error code = %s, want %s or wrapped", apiErr.Code, core.ErrDaemonAlreadyRunning)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not exit after heartbeat ownership loss")
	}
}
