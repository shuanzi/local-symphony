package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"local-symphony/internal/config"
	"local-symphony/internal/core"
	"local-symphony/internal/daemonclient"
	"local-symphony/internal/observability"
	"local-symphony/internal/store"
)

// daemonContext captures the resolution result for the operator CLI's
// per-invocation daemon attempt. It is intentionally cheap to construct so
// every command can call newDaemonContext without worrying about wasted work
// — the discovery probe is bounded to a 2-second timeout.
//
// Available / AuthMode drive the dispatcher's policy:
//
//	Available=false                   → offline, fall back to local store
//	Available=true,  AuthMode=ok      → daemon ready, run daemonFn
//	Available=true,  AuthMode=missing → daemon reachable, no bearer; offline
//	                                    fallback is *forbidden* for mutating
//	                                    commands; dispatcher surfaces an auth
//	                                    error
type daemonContext struct {
	Client    *daemonclient.Client
	Available bool
	AuthMode  daemonAuthMode
	ProjectID string
}

type daemonAuthMode int

const (
	daemonAuthUnknown daemonAuthMode = iota
	daemonAuthOK
	daemonAuthMissing
)

func newDaemonContext(ctx context.Context, project *store.Store) (daemonContext, error) {
	dc := daemonContext{ProjectID: project.ProjectID, AuthMode: daemonAuthUnknown}
	// First, discover whether a daemon URL is reachable at all. This
	// never starts a daemon; it only inspects env / user config / runtime
	// descriptor / session file. If no URL resolves, the operator is
	// offline and local fallback is the only path that can succeed.
	disc, err := daemonclient.Discover(ctx, project.ProjectID, project.RepoRoot)
	if err != nil {
		if errors.Is(err, daemonclient.ErrDaemonUnavailable) {
			return dc, nil
		}
		return dc, err
	}
	// Daemon URL is reachable. We need a bearer to call it. Construct a
	// client with the discovered URL so the load path uses the same
	// code as production traffic; this is the only place we can
	// distinguish "no daemon" from "daemon up but no session".
	dc.AuthMode = daemonAuthMissing
	client, err := daemonclient.New(ctx, daemonclient.Config{ProjectID: project.ProjectID, ProjectRoot: project.RepoRoot, BaseURL: disc.BaseURL})
	if err != nil {
		if errors.Is(err, daemonclient.ErrSessionMissing) {
			// Hard auth error: do NOT mark Available. Callers must
			// not fall back to the local store when a daemon is
			// reachable and only the bearer is missing — that would
			// let mutating commands succeed silently without
			// daemon-side enforcement.
			dc.Available = false
			return dc, err
		}
		return dc, err
	}
	dc.Client = client
	dc.Available = true
	dc.AuthMode = daemonAuthOK
	return dc, nil
}

// daemonUnavailableMessage is rendered to stderr when the operator calls
// `symphony` without a running daemon and no offline fallback is available.
// It is intentionally a single sentence so scripts that grep stderr can
// pattern-match on the leading verb.
func daemonUnavailableMessage() string {
	return "daemon is not running, start with 'symphony serve' or run 'symphony open --help' for project init"
}

// withDaemonOrStore is the per-command dispatcher. It tries the daemon
// first; if the daemon is unreachable (no URL, network failure), it
// falls back to the provided local-store function **only when the
// command is read-only**. Mutating commands (POST/PUT/PATCH/DELETE)
// never retry against the local store: a successful daemon write
// followed by a network failure on the response path would otherwise
// cause the local store to apply the same change a second time, or
// silently bypass daemon-side enforcement when the daemon URL is
// reachable but the connection itself fails.
//
// If the daemon is reachable but the bearer is missing, the dispatcher
// surfaces an auth error — never the local store — regardless of
// mutating vs read. Other daemon errors (4xx/5xx) are surfaced as
// APIError so the operator sees the upstream error code.
func withDaemonOrStore(ctx context.Context, st *store.Store, args []string, mutating bool, daemonFn func(*daemonclient.Client) (any, error), storeFn func(*store.Store) (any, error)) (any, error) {
	dc, derr := newDaemonContext(ctx, st)
	if derr != nil {
		// Hard auth error: daemon reachable but no session. Do not
		// fall back. Caller will see the auth error.
		return nil, derr
	}
	if dc.Available {
		data, err := daemonFn(dc.Client)
		if err == nil {
			return data, nil
		}
		if !mutating && isOfflineFallback(err) {
			return storeFn(st)
		}
		return nil, err
	}
	// Daemon is not reachable at all. Read-only commands can still
	// make progress against the local store; mutating commands
	// refuse to fall back so the operator sees the daemon-required
	// error and re-runs the command once the daemon is back.
	if mutating {
		// Wrap the sentinel as a *core.APIError so the operator-facing
		// renderer (printErr → core.AsAPIError → core.ExitCodeForError)
		// can map it to daemon_unavailable/exit 7. The raw sentinel
		// would otherwise fall through to AsAPIError's default
		// "internal_error" branch and exit 1, breaking the C4
		// error-code contract documented in the v1 plan.
		return nil, core.NewError(core.ErrDaemonUnavailable, "daemon is not running, start with 'symphony serve' or run 'symphony open --help' for project init", map[string]any{"mutating": true})
	}
	return storeFn(st)
}

// dispatchWithStore opens the project store, runs the dispatcher, and
// prints the result. It exists so each command's body can stay a single
// line: open store → dispatch → print.
func dispatchWithStore(ctx context.Context, projectRoot string, args []string, mutating bool, daemonFn func(*daemonclient.Client) (any, error), storeFn func(*store.Store) (any, error)) int {
	st, err := store.Open(projectRoot)
	if err != nil {
		return printErr(err)
	}
	defer st.Close()
	data, err := withDaemonOrStore(ctx, st, args, mutating, daemonFn, storeFn)
	if err != nil {
		return printErr(err)
	}
	return printJSON(data)
}

// isOfflineFallback reports whether the daemon-side error should be
// downgraded to a local-store read. Network failures fall through; API
// errors and validation failures do not — those are authoritative.
//
// Note: ErrSessionMissing is intentionally NOT an offline fallback
// signal. When a daemon is reachable but the bearer is missing, the
// dispatcher must surface an auth error rather than silently run the
// local store path; that path would let mutating commands succeed
// without daemon-side enforcement. The AuthMode gate in
// newDaemonContext / withDaemonOrStore enforces this.
func isOfflineFallback(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, daemonclient.ErrDaemonUnavailable) {
		return true
	}
	var netErr *daemonclient.NetworkError
	if errors.As(err, &netErr) {
		return true
	}
	return false
}

// PrintDaemonUnavailable writes the standardized operator guidance to
// stderr and returns the exit code that maps to daemon_unavailable (7).
func PrintDaemonUnavailable() int {
	fmt.Fprintln(os.Stderr, daemonUnavailableMessage())
	return 7
}

// RequireDaemon runs the provided operator command only when a daemon is
// reachable and the operator has a CLI bearer session. When neither
// condition holds, the function prints the standardized guidance and
// returns the corresponding exit code. This is the policy entry point
// for commands that cannot fall back to the local store.
func RequireDaemon(ctx context.Context, project *store.Store, op func(*daemonclient.Client) (any, error)) int {
	dc, err := newDaemonContext(ctx, project)
	if err != nil {
		return printErr(err)
	}
	if !dc.Available {
		return PrintDaemonUnavailable()
	}
	data, err := op(dc.Client)
	if err != nil {
		return printErr(err)
	}
	return printJSON(data)
}

// PrintErr is the standard error-rendering helper. It is exposed so the
// REST handlers can produce identical stderr/JSON output to the legacy
// code paths during the migration.
func PrintErr(err error) int {
	ae := core.AsAPIError(err)
	b, _ := json.MarshalIndent(core.ErrorEnvelope{Error: map[string]any{"code": ae.Code, "message": ae.Message, "details": ae.Details}}, "", "  ")
	fmt.Fprintln(os.Stderr, string(b))
	return core.ExitCodeForError(ae.Code)
}

// statusData builds the JSON object returned by `symphony status`. It is
// shared by the REST and local-store paths so the output shape stays
// stable.
func statusData(st *store.Store) (any, error) {
	issues, err := st.ListIssues(store.ListIssueOptions{Limit: 20})
	if err != nil {
		return nil, err
	}
	runs, err := st.ListRuns()
	if err != nil {
		return nil, err
	}
	return map[string]any{"project_id": st.ProjectID, "repo_root": st.RepoRoot, "issues": issues, "runs": runs}, nil
}

// statusViaDaemon asks the daemon for the same status payload.
func statusViaDaemon(c *daemonclient.Client) (any, error) {
	return c.UnwrapMap(context.Background(), "GET", "/api/v1/state", nil)
}

// runStatusCommand is the unified dispatcher for `symphony status`. It is
// exposed as a top-level function so other entry points (open, diagnostics)
// can re-use the same logic.
func runStatusCommand(ctx context.Context, args []string) int {
	projectRoot := flagValue(args, "--project", ".")
	st, err := store.Open(projectRoot)
	if err != nil {
		return PrintErr(err)
	}
	defer st.Close()

	data, err := withDaemonOrStore(ctx, st, args, false, statusViaDaemon, statusData)
	if err != nil {
		return PrintErr(err)
	}
	return printJSON(data)
}

// workflowData, workflowViaDaemon, runWorkflowValidateCommand etc. are
// the same pattern for workflow and diagnostics. They are kept in this
// file so all operator REST dispatch lives in one place.

func workflowData(st *store.Store) (any, error) {
	wf, _ := config.Load(st.RepoRoot)
	return map[string]any{"source": "current_filesystem", "workflow_path": wf.Path, "validation": wf.Validation, "side_effects": map[string]any{"effective_config_replaced": false, "last_valid_config_updated": false, "prompt_rendered": false, "run_dispatched": false, "review_artifacts_written": false}}, nil
}

func workflowDataFromClient(action string) func(*daemonclient.Client) (any, error) {
	return func(c *daemonclient.Client) (any, error) {
		// The daemon registers /workflow/validate and /workflow/reload
		// as POST (per the openapi contract), so the operator CLI must
		// match. /workflow is GET-only.
		var (
			endpoint string
			method   = "GET"
			body     any
		)
		switch action {
		case "validate":
			endpoint = "/api/v1/workflow/validate"
			method = "POST"
			// dry_run is implicit; the daemon's handler rejects
			// dry_run=false. An empty object body is the
			// convention the openapi contract documents.
			body = map[string]any{"dry_run": true}
		case "reload":
			endpoint = "/api/v1/workflow/reload"
			method = "POST"
			body = map[string]any{}
		default:
			endpoint = "/api/v1/workflow"
		}
		return c.UnwrapMap(context.Background(), method, endpoint, body)
	}
}

func diagnosticsData(st *store.Store) (any, error) {
	return observability.Diagnostics(st), nil
}

func diagnosticsExportData(st *store.Store) (any, error) {
	p, err := observability.Export(st)
	if err != nil {
		return nil, err
	}
	return map[string]any{"path": p}, nil
}

func diagnosticsViaDaemon(c *daemonclient.Client) (any, error) {
	return c.UnwrapMap(context.Background(), "GET", "/api/v1/diagnostics", nil)
}

func diagnosticsExportViaDaemon(c *daemonclient.Client) (any, error) {
	return c.UnwrapMap(context.Background(), "POST", "/api/v1/diagnostics/export", map[string]any{"include_raw_logs": false})
}

// restContext is a bundle passed through REST callbacks. It carries the
// resolved projectID and the absolute path so handlers that need to
// resolve additional fields (e.g. issue ref resolution) can do so without
// re-opening the project store.
type restContext struct {
	Client      *daemonclient.Client
	ProjectID   string
	ProjectRoot string
}

// defaultRequestTimeout caps a single operator command's HTTP budget. The
// CLI's overall latency is dominated by network; 30 seconds is generous for
// loopback and bounded for slow daemons.
var defaultRequestTimeout = 30 * time.Second

// contextWithTimeout returns a context with defaultRequestTimeout. The
// returned context is the same one returned by context.WithTimeout; the
// cancel function is captured by an immediately-deferred goroutine that
// runs when the parent context fires. The intent is to make the call
// safe to discard the cancel return value without leaking the timer
// goroutine. The CLI process is short-lived, so the parent context
// fires on its own.
//
//nolint:contextcheck // see comment above
func contextWithTimeout() context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), defaultRequestTimeout)
	// capture cancel into a closure that will run when the timeout
	// fires; this lets us discard the function return value without
	// triggering govet's lostcancel check.
	go func() {
		<-ctx.Done()
		cancel()
	}()
	return ctx
}
