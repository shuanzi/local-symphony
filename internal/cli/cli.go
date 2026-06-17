package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"local-symphony/internal/app"
	"local-symphony/internal/config"
	"local-symphony/internal/core"
	"local-symphony/internal/daemonclient"
	"local-symphony/internal/httpapi"
	"local-symphony/internal/store"
	"local-symphony/internal/toolgateway"
)

var openTokenHTTPClient = &http.Client{Timeout: 30 * time.Second}

func Main(args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" {
		printHelp()
		return 0
	}
	switch args[0] {
	case "init":
		return cmdInit(args[1:])
	case "serve":
		return cmdServe(args[1:])
	case "open":
		return cmdOpen(args[1:])
	case "status":
		return runStatusCommand(contextWithTimeout(), args[1:])
	case "issue":
		return cmdIssue(args[1:])
	case "run":
		return cmdRun(args[1:])
	case "approval":
		return cmdApproval(args[1:])
	case "review":
		return cmdReview(args[1:])
	case "workflow":
		return cmdWorkflow(args[1:])
	case "diagnostics":
		return cmdDiagnostics(args[1:])
	case "tool":
		return cmdTool(args[1:])
	case "login":
		return cmdLogin(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", args[0])
		printHelp()
		return 2
	}
}

func cmdInit(args []string) int {
	prefix := flagValue(args, "--issue-prefix", "LOC")
	project := flagValue(args, "--project", ".")
	st, err := store.InitProject(project, prefix)
	if err != nil {
		return printErr(err)
	}
	defer st.Close()
	return printJSON(map[string]any{"project_id": st.ProjectID, "repo_root": st.RepoRoot, "project_db_path": st.ProjectDBPath, "issue_prefix": st.IssuePrefix, "next": "symphony serve --project . --no-open"})
}
func cmdServe(args []string) int {
	opts, err := serveOptionsFromArgs(args)
	if err != nil {
		return printErr(err)
	}
	if err := app.Serve(opts); err != nil {
		return printErr(err)
	}
	return 0
}

func serveOptionsFromArgs(args []string) (app.ServeOptions, error) {
	opts := app.ServeOptions{Project: flagValue(args, "--project", "."), Host: flagValue(args, "--host", "127.0.0.1"), NoOpen: hasFlag(args, "--no-open")}
	port := 0
	if p := flagValue(args, "--port", ""); p != "" {
		parsedPort, err := strconv.Atoi(p)
		if err != nil {
			return opts, core.NewError(core.ErrInvalidRequest, "--port must be numeric", map[string]any{"port": p})
		}
		port = parsedPort
	}
	opts.Port = port
	if addr := flagValue(args, "--addr", ""); addr != "" {
		host, rawPort, err := net.SplitHostPort(addr)
		if err != nil {
			return opts, core.NewError(core.ErrInvalidRequest, "--addr must be host:port", map[string]any{"addr": addr})
		}
		parsedPort, err := strconv.Atoi(rawPort)
		if err != nil {
			return opts, core.NewError(core.ErrInvalidRequest, "--addr port must be numeric", map[string]any{"addr": addr})
		}
		if host == "" {
			host = "127.0.0.1"
		}
		opts.Host = host
		opts.Port = parsedPort
	}
	return opts, nil
}
func cmdOpen(args []string) int {
	desc, err := openDescriptor(flagValue(args, "--project", "."))
	if err != nil {
		// openDescriptor already returns a friendly ErrDaemonUnavailable-style
		// error; surface the standardized guidance message in addition to the
		// underlying envelope.
		return printErr(err)
	}
	return printJSON(desc)
}

// openDescriptor reads the project's runtime descriptor, mints a
// dashboard `?open_token=...` URL, and returns the descriptor with
// the dashboard_url field appended.
//
// Trust boundary: the runtime descriptor's api_url is a value
// persisted on disk; a poisoned or stale descriptor could point at
// any host. We MUST NOT send a CLI bearer to that URL until we have
// independently confirmed it advertises our project_id — otherwise
// a copied, rotated, or attacker-controlled descriptor would
// exfiltrate the bearer to the wrong daemon. We route the
// descriptor's api_url through daemonclient.Discover (with the
// api_url as a hint), which runs the loopback + /health
// project_id guard before any token is dispatched. A descriptor
// pointing at a non-loopback host, an unreachable endpoint, or a
// wrong-project daemon is rejected with ErrDaemonUnavailable so
// the operator gets a single, action-oriented error pointing at
// `symphony serve`.
//
// Context plumbing: the project store's ProjectID is the trust
// anchor. The bearer itself is loaded from the project-scoped
// session file with the same repo_root check the rest of the
// command tree uses, so a copied project DB cannot re-use a
// foreign checkout's CLI session for the open-token mint.
func openDescriptor(project string) (map[string]any, error) {
	st, err := store.Open(project)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	desc, err := app.RuntimeDescriptor(st.ProjectID)
	if err != nil {
		return nil, err
	}
	runtimeURL, _ := desc["api_url"].(string)
	runtimeURL = strings.TrimSpace(runtimeURL)
	if runtimeURL == "" {
		return nil, core.NewError(core.ErrInvalidRequest, "runtime descriptor is missing api_url", nil)
	}
	token, err := readCLISessionToken(st.ProjectID, st.RepoRoot)
	if err != nil {
		return nil, err
	}
	ctx := contextWithTimeout()
	// Discover (with runtimeURL as a hint) runs the loopback
	// guard + /health project_id check. The runtimeURL is the
	// ONLY candidate tried; a poisoned or stale descriptor
	// pointing at the wrong project's daemon (or a non-loopback
	// host) is rejected before the bearer leaves this process.
	disc, derr := daemonclient.Discover(ctx, st.ProjectID, st.RepoRoot, false, runtimeURL)
	if derr != nil {
		// Wrap the discovery error so the operator gets the
		// standardized daemon_unavailable envelope pointing
		// them at `symphony serve` / `symphony open --help`.
		return nil, core.NewError(core.ErrDaemonUnavailable,
			"runtime descriptor points at an unreachable or wrong-project daemon; restart with 'symphony serve'",
			map[string]any{"project_id": st.ProjectID, "cause": derr.Error()})
	}
	openToken, err := requestOpenToken(disc.BaseURL, token)
	if err != nil {
		return nil, err
	}
	desc["dashboard_url"] = strings.TrimRight(disc.BaseURL, "/") + "/?open_token=" + url.QueryEscape(openToken)
	return desc, nil
}

func readCLISessionToken(projectID, repoRoot string) (string, error) {
	token, err := readCLISessionTokenFromPath(app.CLISessionPath(projectID), projectID, repoRoot)
	if err == nil || !os.IsNotExist(err) {
		return token, err
	}
	return readCLISessionTokenFromPath(app.LegacyCLISessionPath(), projectID, repoRoot)
}

func readCLISessionTokenFromPath(path, projectID, repoRoot string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var session struct {
		ProjectID string `json:"project_id"`
		RepoRoot  string `json:"repo_root,omitempty"`
		Token     string `json:"token"`
	}
	if err := json.Unmarshal(b, &session); err != nil {
		return "", err
	}
	if session.ProjectID != projectID || strings.TrimSpace(session.Token) == "" {
		return "", core.NewError(core.ErrUnauthorized, "CLI session is not valid for this project", nil)
	}
	if err := checkCLISessionRepoRoot(session.RepoRoot, repoRoot); err != nil {
		return "", err
	}
	return session.Token, nil
}

// checkCLISessionRepoRoot mirrors daemonclient's repo_root guard. A
// session file with a non-empty RepoRoot that does not match the
// caller's normalised repoRoot is rejected. Pre-repo_root sessions
// (empty RepoRoot) and callers without a repoRoot (legacy paths) are
// both accepted so the new check is strictly additive.
//
// Round 6 HIGH #1 fix: EvalSymlinks failures no longer fall through
// silently. When the original checkout referenced by a persisted
// repo_root is gone (move, delete, container restart) the path
// resolution fails; the previous code treated that as "skip the
// check", which let a foreign bearer be accepted. The trust
// boundary must be fail-closed: an unresolvable path is a
// authorization failure, not a skip.
func checkCLISessionRepoRoot(persisted, caller string) error {
	if caller == "" {
		return nil
	}
	if strings.TrimSpace(persisted) == "" {
		return nil
	}
	want, err := normaliseRepoRootForCompare(caller)
	if err != nil {
		return core.NewError(core.ErrUnauthorized, "CLI session is not valid for this project repository", nil)
	}
	got, err := normaliseRepoRootForCompare(persisted)
	if err != nil {
		return core.NewError(core.ErrUnauthorized, "CLI session is not valid for this project repository", nil)
	}
	if want != got {
		return core.NewError(core.ErrUnauthorized, "CLI session is not valid for this project repository", nil)
	}
	return nil
}

func normaliseRepoRootForCompare(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func requestOpenToken(apiURL, token string) (string, error) {
	if strings.TrimSpace(apiURL) == "" {
		return "", core.NewError(core.ErrInvalidRequest, "runtime descriptor is missing api_url", nil)
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(apiURL, "/")+"/api/v1/auth/open-token", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := openTokenHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var payload struct {
		Data struct {
			OpenToken string `json:"open_token"`
		} `json:"data"`
		Error *core.APIError `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		if payload.Error != nil {
			return "", payload.Error
		}
		return "", core.NewError(core.ErrInternal, string(body), nil)
	}
	if payload.Data.OpenToken == "" {
		return "", core.NewError(core.ErrInternal, "open token response is missing open_token", nil)
	}
	return payload.Data.OpenToken, nil
}

func cmdIssue(args []string) int {
	if len(args) == 0 {
		printIssueHelp()
		return 2
	}
	ctx := contextWithTimeout()
	projectRoot := flagValue(args[1:], "--project", ".")
	switch args[0] {
	case "create":
		// Parse + validate flags before the dispatcher. This way
		// invalid input (e.g. --priority=abc) fails consistently
		// regardless of whether the daemon is reachable; the
		// dispatcher's no-fallback rule for mutating commands
		// never silently swallows a validation error.
		if _, err := parseIssueCreateArgs(args[1:]); err != nil {
			return printErr(err)
		}
		return dispatchWithStore(ctx, projectRoot, args[1:], true, issueCreateViaDaemon(args[1:]), issueCreateLocal(args[1:]))
	case "list":
		return dispatchWithStore(ctx, projectRoot, args[1:], false, issueListViaDaemon(args[1:]), issueListLocal(args[1:]))
	case "show":
		if len(args) < 2 {
			return printErr(core.NewError(core.ErrInvalidRequest, "issue ref required", nil))
		}
		return dispatchWithStore(ctx, projectRoot, args[2:], false, issueShowViaDaemon(args[1]), issueShowLocal(args[1]))
	case "update":
		if len(args) < 2 {
			return printErr(core.NewError(core.ErrInvalidRequest, "issue ref required", nil))
		}
		return dispatchWithStore(ctx, projectRoot, args[2:], true, issueUpdateViaDaemon(args[1], args[2:]), issueUpdateLocal(args[1], args[2:]))
	case "transition":
		if len(args) < 3 {
			return printErr(core.NewError(core.ErrInvalidRequest, "usage: issue transition REF STATE", nil))
		}
		reason := flagValue(args[3:], "--reason", "")
		dup := flagValue(args[3:], "--duplicate-of", "")
		return dispatchWithStore(ctx, projectRoot, args[3:], true,
			issueTransitionViaDaemon(args[1], args[2], reason, dup),
			issueTransitionLocal(args[1], args[2], reason, dup))
	case "comment":
		if len(args) < 2 {
			return printErr(core.NewError(core.ErrInvalidRequest, "issue ref required", nil))
		}
		body := flagValue(args[2:], "--body", "")
		return dispatchWithStore(ctx, projectRoot, args[2:], true, issueCommentViaDaemon(args[1], body), issueCommentLocal(args[1], body))
	case "blocker":
		if len(args) < 4 {
			return printErr(core.NewError(core.ErrInvalidRequest, "usage: issue blocker add/remove REF BLOCKER", nil))
		}
		if args[1] == "add" {
			return dispatchWithStore(ctx, projectRoot, args[4:], true,
				issueBlockerAddViaDaemon(args[2], args[3]),
				issueBlockerAddLocal(args[2], args[3]))
		}
		if args[1] == "remove" {
			return dispatchWithStore(ctx, projectRoot, args[4:], true,
				issueBlockerRemoveViaDaemon(args[2], args[3]),
				issueBlockerRemoveLocal(args[2], args[3]))
		}
	case "duplicate":
		if len(args) >= 4 && args[1] == "remove" {
			return dispatchWithStore(ctx, projectRoot, args[4:], true,
				issueDuplicateRemoveViaDaemon(args[2], args[3]),
				issueDuplicateRemoveLocal(args[2], args[3]))
		}
	case "dispatch":
		if len(args) < 2 {
			return printErr(core.NewError(core.ErrInvalidRequest, "issue ref required", nil))
		}
		return dispatchWithStore(ctx, projectRoot, args[2:], true, issueDispatchViaDaemon(args[1]), issueDispatchLocal(args[1]))
	case "dispatch-pause":
		if len(args) < 2 {
			return printErr(core.NewError(core.ErrInvalidRequest, "issue ref required", nil))
		}
		reason := flagValue(args[2:], "--reason", "")
		if strings.TrimSpace(reason) == "" {
			return printErr(core.NewError(core.ErrInvalidRequest, "reason is required", nil))
		}
		return dispatchWithStore(ctx, projectRoot, args[2:], true,
			issueDispatchPauseViaDaemon(args[1], reason),
			issueDispatchPauseLocal(args[1], reason))
	case "dispatch-resume":
		if len(args) < 2 {
			return printErr(core.NewError(core.ErrInvalidRequest, "issue ref required", nil))
		}
		reason := flagValue(args[2:], "--reason", "")
		if strings.TrimSpace(reason) == "" {
			return printErr(core.NewError(core.ErrInvalidRequest, "reason is required", nil))
		}
		return dispatchWithStore(ctx, projectRoot, args[2:], true,
			issueDispatchResumeViaDaemon(args[1], reason),
			issueDispatchResumeLocal(args[1], reason))
	}
	return printErr(core.NewError(core.ErrInvalidRequest, "unknown issue command", nil))
}

func cmdRun(args []string) int {
	if len(args) == 0 {
		printRunHelp()
		return 2
	}
	ctx := contextWithTimeout()
	projectRoot := flagValue(args[1:], "--project", ".")
	if isIssueRefArg(args[0]) {
		return dispatchWithStore(ctx, projectRoot, args[1:], true, runDispatchViaDaemon(args[0]), runDispatchLocal(args[0]))
	}
	switch args[0] {
	case "list":
		return dispatchWithStore(ctx, projectRoot, args[1:], false, runListViaDaemon(), runListLocal())
	case "show":
		if len(args) < 2 {
			return printErr(core.NewError(core.ErrInvalidRequest, "run id required", nil))
		}
		return dispatchWithStore(ctx, projectRoot, args[2:], false, runShowViaDaemon(args[1]), runShowLocal(args[1]))
	case "events":
		if len(args) < 2 {
			return printErr(core.NewError(core.ErrInvalidRequest, "run id required", nil))
		}
		return dispatchWithStore(ctx, projectRoot, args[2:], false, runEventsViaDaemon(args[1]), runEventsLocal(args[1]))
	case "cancel":
		if len(args) < 2 {
			return printErr(core.NewError(core.ErrInvalidRequest, "run id required", nil))
		}
		reason := flagValue(args[2:], "--reason", "operator cancelled")
		return dispatchWithStore(ctx, projectRoot, args[2:], true, runCancelViaDaemon(args[1], reason), runCancelLocal(args[1], reason))
	}
	return printErr(core.NewError(core.ErrInvalidRequest, "unknown run command", nil))
}

func isIssueRefArg(s string) bool {
	if strings.HasPrefix(s, "iss_") {
		return true
	}
	prefix, seq, ok := strings.Cut(s, "-")
	if !ok || prefix == "" || seq == "" {
		return false
	}
	for _, r := range prefix {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	for _, r := range seq {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func cmdApproval(args []string) int {
	if len(args) == 0 {
		return printErr(core.NewError(core.ErrInvalidRequest, "approval command required", nil))
	}
	ctx := contextWithTimeout()
	projectRoot := flagValue(args[1:], "--project", ".")
	switch args[0] {
	case "list":
		return dispatchWithStore(ctx, projectRoot, args[1:], false, approvalListViaDaemon(), approvalListLocal())
	case "decide":
		if len(args) < 2 {
			return printErr(core.NewError(core.ErrInvalidRequest, "approval id required", nil))
		}
		decision := ""
		for _, p := range []string{"approve-once", "approve-for-run", "approve-for-session", "deny", "cancel-run"} {
			if hasFlag(args[2:], "--"+p) {
				if d, ok := approvalDecisionString("--" + p); ok {
					decision = d
				}
				break
			}
		}
		if decision == "" {
			return printErr(core.NewError(core.ErrInvalidRequest, "approval decision required", nil))
		}
		reason := flagValue(args[2:], "--reason", "")
		// local store uses the internal status name; map from decision
		status := map[string]string{
			"approve_once":         "approved_once",
			"approve_for_run":      "approved_for_run",
			"approve_for_session":  "approved_for_session",
			"deny":                 "denied",
			"cancel_run":           "cancelled",
		}[decision]
		return dispatchWithStore(ctx, projectRoot, args[2:], true,
			approvalDecideViaDaemon(args[1], decision, reason),
			approvalDecideLocal(args[1], status, reason))
	}
	return printErr(core.NewError(core.ErrInvalidRequest, "unknown approval command", nil))
}
func cmdReview(args []string) int {
	if len(args) == 0 {
		printReviewHelp()
		return 2
	}
	ctx := contextWithTimeout()
	projectRoot := flagValue(args[1:], "--project", ".")
	switch args[0] {
	case "send-to-rework":
		if len(args) < 2 {
			return printErr(core.NewError(core.ErrInvalidRequest, "issue ref required", nil))
		}
		reason := flagValue(args[2:], "--reason", "")
		if strings.TrimSpace(reason) == "" {
			return printErr(core.NewError(core.ErrInvalidRequest, "reason is required", nil))
		}
		return dispatchWithStore(ctx, projectRoot, args[2:], true,
			reviewSendToReworkViaDaemon(args[1], reason),
			reviewSendToReworkLocal(args[1], reason))
	case "mark-done":
		if len(args) < 2 {
			return printErr(core.NewError(core.ErrInvalidRequest, "issue ref required", nil))
		}
		reason := flagValue(args[2:], "--reason", "")
		if strings.TrimSpace(reason) == "" {
			return printErr(core.NewError(core.ErrInvalidRequest, "reason is required", nil))
		}
		return dispatchWithStore(ctx, projectRoot, args[2:], true,
			reviewMarkDoneViaDaemon(args[1], reason),
			reviewMarkDoneLocal(args[1], reason))
	case "path":
		if len(args) < 2 {
			return printErr(core.NewError(core.ErrInvalidRequest, "issue ref required", nil))
		}
		// review path is a local-store-only flow: it surfaces the
		// filesystem path to a review packet and never needs the daemon.
		return withStore(args[2:], reviewMetaFor(args[1]))
	default:
		ref := args[0]
		return dispatchWithStore(ctx, projectRoot, args[1:], false, reviewGetViaDaemon(ref), reviewGetLocal(ref))
	}
}
func reviewMetaFor(ref string) func(*store.Store) (any, error) {
	return func(st *store.Store) (any, error) {
		row, err := st.ReviewPacketRow(ref)
		if err != nil {
			return nil, err
		}
		arts, err := st.ArtifactsForReview(row["id"].String())
		if err != nil {
			return nil, err
		}
		return map[string]any{"id": row["id"].String(), "run_id": row["run_id"].String(), "packet_no": row["packet_no"].Int(), "status": row["status"].String(), "root_path": row["root_path"].String(), "artifacts": arts}, nil
	}
}
func reviewMeta(st *store.Store) (any, error) {
	return nil, core.NewError(core.ErrInvalidRequest, "review path requires issue ref", nil)
}

func cmdWorkflow(args []string) int {
	if len(args) == 0 {
		return printErr(core.NewError(core.ErrInvalidRequest, "workflow command required", nil))
	}
	ctx := contextWithTimeout()
	projectRoot := flagValue(args[1:], "--project", ".")
	switch args[0] {
	case "validate":
		// workflow validate is a read-only filesystem inspection:
		// it loads the on-disk WORKFLOW.md, runs the validation
		// pipeline, and returns the result without writing
		// anything. The daemon endpoint is the authoritative
		// path when available, but offline operators must still
		// be able to validate their workflow — that's the whole
		// point of the local workflowData fallback.
		return dispatchWithStore(ctx, projectRoot, args[1:], false,
			workflowDataFromClient("validate"),
			workflowData)
	case "reload":
		return dispatchWithStore(ctx, projectRoot, args[1:], true,
			workflowDataFromClient("reload"),
			func(st *store.Store) (any, error) {
				wf, _ := config.Load(st.RepoRoot)
				return map[string]any{"reloaded": wf.Validation.Valid, "validation": wf.Validation}, nil
			})
	case "show":
		return dispatchWithStore(ctx, projectRoot, args[1:], false,
			workflowDataFromClient("show"),
			func(st *store.Store) (any, error) { wf, _ := config.Load(st.RepoRoot); return wf, nil })
	}
	return printErr(core.NewError(core.ErrInvalidRequest, "unknown workflow command", nil))
}
func cmdDiagnostics(args []string) int {
	ctx := contextWithTimeout()
	projectRoot := flagValue(args, "--project", ".")
	if len(args) > 0 && args[0] == "export" {
		return dispatchWithStore(ctx, projectRoot, args[1:], true,
			diagnosticsExportViaDaemon,
			diagnosticsExportData)
	}
	return dispatchWithStore(ctx, projectRoot, args, false, diagnosticsViaDaemon, diagnosticsData)
}

// cmdLogin verifies the operator's CLI bearer session against the daemon.
// The CLI session is normally minted by `symphony serve` and stored in
// ~/.symphony/cli-sessions/<project>.json. The `symphony login` command is
// the operator-visible way to ask "am I logged in?" and to surface a
// helpful, project-aware error when the daemon is not running.
//
// --list and --logout do not require a project store; they operate on
// the local session files only. The default (no flags) probes the
// daemon and needs a project root to resolve a project_id.
func cmdLogin(args []string) int {
	ctx := contextWithTimeout()

	// --list does not need a project store; list all saved sessions.
	if hasFlag(args, "--list") {
		sessions, err := daemonclient.ReadAllSessionFiles()
		if err != nil {
			return printErr(err)
		}
		out := make([]map[string]any, 0, len(sessions))
		for _, s := range sessions {
			out = append(out, map[string]any{"project_id": s.ProjectID, "repo_root": s.RepoRoot, "api_url": s.APIURL, "created_at": s.CreatedAt})
		}
		return printJSON(map[string]any{"sessions": out})
	}

	// --logout needs the project_id to delete the right file; resolve
	// it lazily so the command works as long as the project root
	// contains a valid project db. We also wipe the legacy
	// ~/.symphony/cli-session.json file because users upgraded
	// from pre-v1.1 still rely on it for token lookup, and a stale
	// legacy token would otherwise keep authenticating after
	// "logout" reports success.
	//
	// Critical: before deleting the local files, we MUST call the
	// daemon's /auth/cli-sessions/current DELETE endpoint to mark
	// the local_sessions row as revoked. Otherwise a copied
	// bearer token (e.g. exfiltrated before the operator ran
	// logout) would still authorize mutating REST calls. If the
	// daemon is reachable and the token is recognised but the
	// revoke call FAILS, we must NOT report success and we
	// must NOT delete the local files — the operator needs
	// both the file (to retry) and a non-zero exit to know the
	// revoke was not confirmed.
	if hasFlag(args, "--logout") {
		projectRoot := flagValue(args, "--project", ".")
		projectID, repoRoot, err := loginResolveProject(projectRoot)
		if err != nil {
			return printErr(err)
		}
		revokeStatus, matched, revokeErr, keepFiles := logoutRevoke(ctx, projectID, repoRoot)
		out := map[string]any{
			"logged_out":    !keepFiles,
			"project_id":    projectID,
			"revoke_status": revokeStatus,
			"bearer_matched": matched,
		}
		if revokeErr != nil {
			out["revoke_error"] = revokeErr.Error()
		}
		if keepFiles {
			// Degraded: daemon could not be reached, or the
			// revoke call failed for a recoverable reason.
			// Preserve the local files so the operator can
			// retry; return exit 7 with the structured error.
			return printErr(core.NewError(core.ErrDaemonUnavailable, "logout did not confirm server-side revocation; local files preserved for retry", out))
		}
		// Safe to delete: revoke succeeded, the token was not
		// recognised (nothing to revoke), or the daemon is
		// reachable but we had no token to revoke in the first
		// place (no_bearer).
		//
		// The legacy ~/.symphony/cli-session.json file is only
		// deleted when its persisted project_id matches the
		// project we are logging out of. A residual legacy file
		// owned by a DIFFERENT project is preserved and
		// reported in the operator output. The fix is required
		// because the legacy file is single-instance (not
		// per-project): a multi-project operator who upgraded
		// from pre-v1.1 keeps ONE legacy file whose project_id
		// is whichever project they last logged into. An
		// unconditional delete would silently wipe the other
		// project's legacy bearer record while leaving its
		// server-side row still valid. We must not auto-revoke
		// the other project (chained side-effects); we
		// preserve and report.
		if err := daemonclient.DeleteSessionFile(projectID); err != nil {
			return printErr(err)
		}
		legacyResidual, legacyPath, legacyErr := deleteLegacySessionIfOwnedBy(projectID)
		if legacyErr != nil {
			return printErr(legacyErr)
		}
		if legacyResidual != "" {
			out["legacy_preserved"] = map[string]any{
				"reason": "residual legacy session belongs to a different project",
				"project_id": legacyResidual,
				"path": legacyPath,
			}
		}
		return printJSON(out)
	}

	projectRoot := flagValue(args, "--project", ".")
	st, err := store.Open(projectRoot)
	if err != nil {
		return printErr(err)
	}
	defer st.Close()

	dc, err := newDaemonContext(ctx, st)
	if err != nil {
		// Distinguish "no daemon" (offline guidance) from "daemon
		// reachable but no session" (auth error). The newDaemonContext
		// contract guarantees any returned err here is auth-related
		// when a URL is resolvable.
		if errors.Is(err, daemonclient.ErrSessionMissing) {
			return printErr(core.NewError(core.ErrUnauthorized, "CLI session missing for this project; run 'symphony serve' to mint a new session", map[string]any{"project_id": st.ProjectID}))
		}
		return printErr(err)
	}
	if !dc.Available {
		fmt.Fprintln(os.Stderr, daemonUnavailableMessage())
		return 7
	}
	// Probe /api/v1/auth/session to confirm the bearer is recognized.
	data, err := dc.Client.UnwrapMap(ctx, "GET", "/api/v1/auth/session", nil)
	if err != nil {
		return printErr(err)
	}
	if v, ok := data["authenticated"].(bool); ok && v {
		return printJSON(map[string]any{
			"project_id": st.ProjectID,
			"repo_root":  st.RepoRoot,
			"api_url":    dc.Client.BaseURL,
			"session":    "active",
			"created_at": "redacted",
		})
	}
	// Bearer (or cookie) was presented but the daemon did not
	// recognise it. This branch must NOT exit 0 — operators and
	// scripts that rely on `symphony login` to confirm session
	// health expect non-zero when the token is expired or revoked.
	// Render an explicit error envelope and return exit 7.
	return printErr(core.NewError(core.ErrUnauthorized, "CLI session is invalid; run 'symphony serve' to refresh", map[string]any{"project_id": st.ProjectID, "session": "unauthenticated"}))
}

// loginResolveProject opens the project store at the given root and
// returns its (project_id, repo_root). The repo_root is the absolute,
// symlink-resolved path the store recorded on init; propagating it
// into logoutRevoke means the repo_root guard in
// daemonclient.ReadSessionFile compares against the ACTUAL checkout
// the operator is running from. A copied project DB inherits the
// foreign project_id but its session file's repo_root will not match
// the new checkout, and the guard rejects it before the bearer is
// sent. This closes the round-5 HIGH #2 finding: prior versions
// passed an empty project_root to logoutRevoke (the
// lookupProjectRootForRevoke stub), which silently skipped the
// repo_root check and let a copied DB call DELETE on the foreign
// project daemon.
func loginResolveProject(projectRoot string) (string, string, error) {
	st, err := store.Open(projectRoot)
	if err != nil {
		return "", "", err
	}
	defer st.Close()
	return st.ProjectID, st.RepoRoot, nil
}

// logoutRevoke calls the daemon's revoke endpoint to mark the
// CLI bearer as revoked. The returned tuple is
// (status, matched, err, keepFiles). status is one of:
//
//	"revoked"      daemon reachable and bearer matched; safe to delete local
//	"not_matched"  daemon reachable but the bearer was not the one it knew
//	                about, e.g. a rotated token
//	"no_bearer"    daemon reachable but no local token to revoke
//	"degraded"     daemon unreachable or call failed; keepFiles == true
//
// keepFiles is true when the caller must NOT delete the
// local session files — the operator needs the file to
// retry revocation, and we have not confirmed that the
// server-side row is revoked.
//
// Per-source degraded tracking (round-5 HIGH #3): when the
// project-scoped session file's revoke call DEGRADES
// (daemon unreachable, network error, etc.) we MUST NOT
// treat a subsequent legacy-file `not_matched` as success.
// Prior versions of this function short-circuited on the
// first non-degraded reply, so a degraded project-scoped
// revoke followed by a legacy file the daemon no longer
// recognises (e.g. an old token) would report
// revoke_status=not_matched, the caller would then delete
// the project-scoped session file, and the operator's
// current bearer would stay valid server-side. The fix
// tracks degraded state across sources: a degraded
// authoritative revoke (the project-scoped file whose
// api_url was used to mint the bearer) cannot be cleared
// by a non-authoritative source's terminal reply.
//
// Round 6 HIGH #2 (project-scoped validation failure is
// sticky): when the project-scoped session file EXISTS but
// fails validation (project_id mismatch, repo_root guard,
// EvalSymlinks failure, or any error ReadSessionFile raises
// for content reasons), the file is unusable as a revoke
// source. It is also not safe to delete: an unvalidated
// project-scoped file is a foreign bearer record (copied
// project DB, deleted checkout, or an unresolvable
// repo_root). Deleting it would report `logged_out: true`
// while the daemon-side local_sessions row stays active and
// the bearer remains usable. The fix treats a project-scoped
// validation failure the same way as a degraded revoke: it
// is sticky across the fallback chain, the legacy file's
// terminal reply cannot clear it, and the caller keeps the
// file (and exits degraded).
func logoutRevoke(ctx context.Context, projectID, projectRoot string) (status string, matched bool, err error, keepFiles bool) {
	// Per-source tracking. `degraded` is sticky across the
	// fallback chain: once any reachable source fails to
	// confirm server-side revocation we must not let a
	// different source's not_matched / no_bearer silently
	// clear that failure, because the operator's CURRENT
	// bearer is the one whose server-side row is unverified.
	degraded := false
	tried := false
	// projectScopedValidationFailed captures the case where
	// the project-scoped file exists but its content failed
	// validation (project_id mismatch, repo_root guard, etc.).
	// We do NOT treat that file as a usable revoke source AND
	// we do NOT let the caller delete it: a foreign project
	// DB whose session file failed validation has a bearer we
	// have not (and cannot) confirm revoked server-side.
	projectScopedValidationFailed := false

	// Authoritative source: the project-scoped session file
	// (api_url + token). This is the one the bearer was
	// minted for; a degraded call here is the only one that
	// is truly unrecoverable for the current token.
	//
	// Round 6 HIGH #2: when the project-scoped file EXISTS
	// but fails validation, the result tuple carries
	// validationFailed=true AND usable=true. The caller
	// branch below treats this as sticky degraded: the
	// file is not deleted, the legacy file's terminal
	// reply cannot clear the failure, and the discovery
	// fallback chain is not consulted.
	//
	// usable=false is reserved for the missing-file case,
	// which is the only outcome where the project-scoped
	// source is genuinely absent and we should fall
	// through to the legacy source and (if needed) the
	// discovery chain.
	if revStatus, m, _, ok, valFailed := logoutRevokeFromFile(ctx, projectID, projectRoot, app.CLISessionPath(projectID)); ok {
		tried = true
		if valFailed {
			projectScopedValidationFailed = true
			degraded = true
			// fall through to legacy
		} else {
			switch revStatus {
			case "revoked", "not_matched", "no_bearer":
				// not_matched still clears degraded: a
				// reachable daemon explicitly told us
				// "I do not know this token", which is
				// a positive terminal result for the
				// current source. The local file can
				// safely be deleted because there is
				// nothing to revoke server-side.
				return revStatus, m, nil, false
			case "degraded":
				degraded = true
				// fall through to legacy
			}
		}
	}
	// Legacy source: pre-v1.1 single-file session. A
	// not_matched / no_bearer reply from the legacy file
	// MUST NOT clear a degraded state from the
	// project-scoped source, because the operator's
	// CURRENT bearer (the one the project-scoped file
	// holds) is still unverified server-side.
	if revStatus, m, _, ok, _ := logoutRevokeFromFile(ctx, projectID, projectRoot, app.LegacyCLISessionPath()); ok {
		tried = true
		switch revStatus {
		case "revoked", "not_matched", "no_bearer":
			if degraded {
				// The current-token revoke never
				// confirmed; do not let a stale
				// legacy token's terminal reply
				// delete the file we still need
				// to retry with.
				return "degraded", false, nil, true
			}
			return revStatus, m, nil, false
		case "degraded":
			degraded = true
		}
	}

	if projectScopedValidationFailed {
		// Round 6 HIGH #2: the project-scoped file exists
		// but failed validation. We must NOT have let a
		// legacy file's terminal reply clear the sticky
		// degraded state above; reaching this point
		// means the legacy path also ended in a way that
		// did not revoke the current bearer. Report
		// degraded so the caller keeps the project-scoped
		// file. (If we returned earlier via the legacy
		// branch, degraded was already set and keepFiles
		// is already true.)
		return "degraded", false, nil, true
	}

	// No local token to revoke. Try the discovery chain so we
	// can still contact the daemon and ask it to revoke any
	// bearer it knows about.
	if !tried {
		// The project-scoped file did not have both token+api_url.
		// We can attempt discovery to find the daemon URL.
		disc, derr := daemonclient.Discover(ctx, projectID, projectRoot, false, "")
		if derr == nil {
			client, cerr := daemonclient.New(ctx, daemonclient.Config{
				ProjectID:           projectID,
				ProjectRoot:         projectRoot,
				BaseURL:             disc.BaseURL,
				Token:               "", // no local token; idempotent revoke
				AllowRemoteDaemonURL: false,
			})
			if cerr == nil {
				m, e := client.RevokeCLISession(ctx)
				if e == nil {
					return "no_bearer", m, nil, false
				}
			}
		}
		// Discovery failed or revoke failed: degraded.
		_ = disc
		return "degraded", false, nil, true
	}

	// We had at least one token+api_url but the revoke
	// call(s) failed. Degraded: preserve local files so the
	// operator can retry.
	return "degraded", false, nil, true
}

// logoutRevokeFromFile reads the session file at path, builds a
// daemon client with the bearer, and calls RevokeCLISession.
// The returned tuple is (status, matched, err, usable, validationFailed).
// A usable file with a successful revoke returns
// ("revoked", true, nil, true, false); a degraded revoke
// returns ("degraded", false, <err>, true, false); an unusable
// file because it does not exist returns
// ("", false, nil, false, false); an unusable file that
// exists but failed validation (project_id mismatch,
// repo_root guard, EvalSymlinks failure, parse error, or
// empty token/api_url) returns ("", false, nil, false, true).
//
// validationFailed is sticky: when the project-scoped file
// fails validation the caller must NOT delete it and must
// NOT let any other source's terminal reply override that
// decision. An unvalidated project-scoped file is a foreign
// bearer record; deleting it would report `logged_out:true`
// while the daemon-side local_sessions row stays active.
//
// Trust boundary: the saved session's api_url is the URL the
// bearer was minted for. We MUST verify that the daemon at
// that URL advertises the project_id we are logging out of
// before sending the bearer — otherwise a stale api_url
// pointing at a different project's daemon (loopback host
// collision, leftover from a prior dev session) would
// receive a CLI bearer it has no business seeing, and the
// "matched=false" response would otherwise map to
// "not_matched" → caller deletes local files. By routing
// the saved api_url through Discover's /health project_id
// check we either get a clean revoke on the right daemon
// or a degraded result that preserves the local files for
// the operator to retry.
//
// repo_root guard: the session file is loaded through
// daemonclient.ReadSessionFile with the caller's actual
// project root. A file whose persisted repo_root does not
// match the caller's checkout is treated as validationFailed
// (matching the `loadCLISessionToken` and project-scoped
// session lookup paths) so a copied project DB cannot
// trigger an outbound bearer revoke for a different repo's
// session. Round 6 HIGH #2: the caller (logoutRevoke) does
// NOT delete the project-scoped file when validationFailed
// is true, regardless of any other source's terminal reply.
func logoutRevokeFromFile(ctx context.Context, projectID, projectRoot, path string) (status string, matched bool, err error, usable bool, validationFailed bool) {
	sf, readErr := daemonclient.ReadSessionFile(path, projectID, projectRoot)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			// File is genuinely absent; fall through
			// to the next source without preserving.
			// usable=false signals "this source is
			// not present" so the caller can try the
			// next source (or the discovery chain).
			return "", false, nil, false, false
		}
		// File exists but failed validation
		// (project_id mismatch, repo_root guard, parse
		// error, EvalSymlinks failure, etc.). The
		// caller must not delete this file and must
		// treat the failure as sticky degraded.
		// usable=true + validationFailed=true tells
		// the caller "we DID find a project-scoped
		// file, it failed validation, and the file
		// is preserved". This is the round-6 HIGH #2
		// fix: the project-scoped file is not
		// silently dropped, and the caller does not
		// let a legacy file's terminal reply
		// authorise its deletion.
		return "", false, nil, true, true
	}
	if sf.Token == "" || sf.APIURL == "" {
		// File exists but lacks the data we need to
		// call revoke. This is also a validation-style
		// failure: the file is not safe to use as a
		// revoke source, and deleting it locally would
		// lose the project_id binding we need to
		// re-issue or rotate. Treat as
		// validationFailed so the caller preserves it.
		return "", false, nil, true, true
	}
	// Discover (with the saved api_url as a hint) runs the
	// /health project_id guard. If the saved URL points at
	// the wrong project's daemon, is unreachable, or is
	// otherwise unusable, Discover returns
	// ErrDaemonUnavailable and we keep the local files
	// intact for the operator to retry.
	disc, derr := daemonclient.Discover(ctx, projectID, projectRoot, false, sf.APIURL)
	if derr != nil {
		return "degraded", false, derr, true, false
	}
	client, cerr := daemonclient.New(ctx, daemonclient.Config{
		ProjectID:   projectID,
		ProjectRoot: projectRoot,
		BaseURL:     disc.BaseURL,
		Token:       sf.Token,
	})
	if cerr != nil {
		return "degraded", false, cerr, true, false
	}
	m, e := client.RevokeCLISession(ctx)
	if e != nil {
		return "degraded", false, e, true, false
	}
	if m {
		return "revoked", true, nil, true, false
	}
	return "not_matched", false, nil, true, false
}

// deleteLegacySessionIfOwnedBy removes the legacy
// ~/.symphony/cli-session.json file ONLY when its persisted
// project_id matches the project we are logging out of. The
// returned residualProjectID is non-empty when the legacy
// file exists, parses, and belongs to a different project —
// the caller surfaces this in the operator-visible output so
// the operator knows to clean it up out-of-band.
//
// Resolution rules:
//   - missing file   → delete (no-op), residual ""
//   - unreadable     → preserve, residual "" (we cannot tell
//                       ownership; deleting might remove a
//                       foreign project's record)
//   - empty / unset
//     project_id     → delete (no ownership claim means it
//                       cannot be foreign); residual ""
//   - same project   → delete, residual ""
//   - foreign project → preserve, residual "<project_id>"
//
// The path is returned so the operator knows exactly where the
// preserved residual lives. It is the responsibility of the
// calling test or operator to clean it up out-of-band; we do
// not auto-revoke foreign projects (chained side effects).
func deleteLegacySessionIfOwnedBy(currentProjectID string) (residualProjectID, residualPath string, err error) {
	path := app.LegacyCLISessionPath()
	residualPath = path
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return "", "", nil
		}
		// Unreadable: cannot prove ownership. Preserve so a
		// foreign project is not silently wiped.
		return "", path, nil
	}
	var probe struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		// Unparseable: same as unreadable — preserve rather
		// than delete a record we cannot attribute.
		return "", path, nil
	}
	owned := probe.ProjectID == "" || probe.ProjectID == currentProjectID
	if !owned {
		return probe.ProjectID, path, nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return "", "", err
	}
	return "", "", nil
}

func cmdTool(args []string) int {
	if len(args) < 2 {
		return printErr(core.NewError(core.ErrInvalidRequest, "tool command required", nil))
	}
	toolName := ""
	rest := []string{}
	switch args[0] {
	case "issue":
		if len(args) >= 2 {
			if args[1] == "get" {
				toolName = "issue.get"
				rest = args[2:]
			} else if args[1] == "comment" {
				toolName = "issue.comment"
				rest = args[2:]
			} else if args[1] == "block" {
				toolName = "issue.block"
				rest = args[2:]
			}
		}
	case "artifact":
		if len(args) >= 2 && args[1] == "attach" {
			toolName = "artifact.attach"
			rest = args[2:]
		}
	case "followup":
		if len(args) >= 2 && args[1] == "create" {
			toolName = "followup.create"
			rest = args[2:]
		}
	case "handoff":
		if len(args) >= 2 && args[1] == "submit" {
			toolName = "handoff.submit"
			rest = args[2:]
		}
	}
	if toolName == "" {
		return printErr(core.NewError(core.ErrInvalidRequest, "unknown tool command", nil))
	}
	input := map[string]any{}
	if toolName != "issue.get" {
		js := flagValue(rest, "--json", "")
		if js == "" {
			return printErr(core.NewError(core.ErrInvalidRequest, "--json is required", nil))
		}
		b := []byte{}
		var err error
		if js == "-" {
			b, err = io.ReadAll(os.Stdin)
		} else {
			b, err = os.ReadFile(js)
		}
		if err != nil {
			return printErr(err)
		}
		if err := json.Unmarshal(b, &input); err != nil {
			return printErr(core.NewError(core.ErrInvalidRequest, err.Error(), nil))
		}
	}
	endpoint := os.Getenv("SYMPHONY_TOOL_ENDPOINT")
	token := os.Getenv("SYMPHONY_TOOL_TOKEN")
	if endpoint == "" || token == "" {
		return printErr(core.NewError(core.ErrToolTokenInvalid, "SYMPHONY_TOOL_ENDPOINT and SYMPHONY_TOOL_TOKEN are required", nil))
	}
	resp := toolgateway.HTTPClientCall(endpoint, token, toolgateway.Request{Tool: toolName, Input: input})
	if resp.Error != nil {
		_ = printJSON(resp)
		// All tool-gateway errors map to exit code 7 (operator-actionable
		// conflict). v1's exit code policy does not single out
		// handoff_conflict; the prior if/return 7 was dead code.
		return 7
	}
	return printJSON(resp)
}

func withStore(args []string, fn func(*store.Store) (any, error)) int {
	st, err := store.Open(flagValue(args, "--project", "."))
	if err != nil {
		return printErr(err)
	}
	defer st.Close()
	data, err := fn(st)
	if err != nil {
		return printErr(err)
	}
	return printJSON(data)
}
func printJSON(v any) int { b, _ := json.MarshalIndent(v, "", "  "); fmt.Println(string(b)); return 0 }
func printErr(err error) int {
	ae := core.AsAPIError(err)
	b, _ := json.MarshalIndent(core.ErrorEnvelope{Error: map[string]any{"code": ae.Code, "message": ae.Message, "details": ae.Details}}, "", "  ")
	fmt.Fprintln(os.Stderr, string(b))
	return core.ExitCodeForError(ae.Code)
}
func flagValue(args []string, name, def string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == name && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(args[i], name+"=") {
			return strings.TrimPrefix(args[i], name+"=")
		}
	}
	return def
}
func flagValuePresent(args []string, name string) (string, bool) {
	for i := 0; i < len(args); i++ {
		if args[i] == name {
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				return args[i+1], true
			}
			return "", true
		}
		if strings.HasPrefix(args[i], name+"=") {
			return strings.TrimPrefix(args[i], name+"="), true
		}
	}
	return "", false
}
func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name {
			return true
		}
	}
	return false
}
func multiFlag(args []string, name string) []string {
	out := []string{}
	for i := 0; i < len(args); i++ {
		if args[i] == name && i+1 < len(args) {
			out = append(out, args[i+1])
			i++
		}
		if strings.HasPrefix(args[i], name+"=") {
			out = append(out, strings.TrimPrefix(args[i], name+"="))
		}
	}
	return out
}

func printHelp() {
	fmt.Println(`symphony - local-first agent workflow control plane

Commands:
  symphony init --project . --issue-prefix LOC
  symphony serve --project . [--host 127.0.0.1] [--port 7331 | --addr 127.0.0.1:7331] [--no-open]
  symphony open --project .
  symphony status
  symphony issue create|list|show|update|transition|comment|blocker|duplicate|dispatch|dispatch-pause|dispatch-resume
  symphony run|run list|run show|run events|run cancel
  symphony approval list|approval decide --approve-once|--approve-for-run|--approve-for-session|--deny|--cancel-run
  symphony review ISSUE|review send-to-rework|review mark-done|review path
  symphony workflow validate|reload|show
  symphony diagnostics|diagnostics export
  symphony tool issue get|comment|block; artifact attach; followup create; handoff submit --json -

Operator commands prefer the local daemon (symphony serve) and fall back to
the on-disk store when the daemon is unreachable. The CLI bearer session is
minted automatically when 'symphony serve' starts and is written to
'~/.symphony/cli-sessions/<project>.json' (mode 0600). 'symphony login'
verifies the current project session against the daemon; 'symphony login
--list' enumerates every saved session across all projects; 'symphony login
--logout' deletes the local file and revokes the server-side row. Token
rotation is available via 'POST /api/v1/auth/cli-token/rotate' on the
daemon. When no daemon is running and the on-disk store cannot satisfy a
command, the CLI prints: 'daemon is not running, start with symphony serve
or run symphony open --help for project init'. The CLI never auto-starts a
daemon.

No v1 commands exist for publish, create-pr, backup, restore, migrate, audit, workspace-delete, secret, project settings, issue delete, arbitrary state mutation.`)
}
func printIssueHelp() {
	fmt.Println("issue commands include --duplicate-of, dispatch-pause, dispatch-resume")
}
func printRunHelp() {
	fmt.Println("run LOC-1 | run list | run show run_... | run events run_... --follow | run cancel run_... --reason ...")
}
func printReviewHelp() {
	fmt.Println("review LOC-1 | review send-to-rework LOC-1 --reason ... | review mark-done LOC-1 --reason ... | review path LOC-1")
}

func HTTPHandlerForTests(st *store.Store) http.Handler { return httpapi.New(st).Handler() }
func ProjectRootFromCWD() string                       { p, _ := filepath.Abs("."); return p }
