package cli

import (
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
	apiURL, _ := desc["api_url"].(string)
	token, err := readCLISessionToken(st.ProjectID)
	if err != nil {
		return nil, err
	}
	openToken, err := requestOpenToken(apiURL, token)
	if err != nil {
		return nil, err
	}
	desc["dashboard_url"] = strings.TrimRight(apiURL, "/") + "/?open_token=" + url.QueryEscape(openToken)
	return desc, nil
}

func readCLISessionToken(projectID string) (string, error) {
	token, err := readCLISessionTokenFromPath(app.CLISessionPath(projectID), projectID)
	if err == nil || !os.IsNotExist(err) {
		return token, err
	}
	return readCLISessionTokenFromPath(app.LegacyCLISessionPath(), projectID)
}

func readCLISessionTokenFromPath(path, projectID string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var session struct {
		ProjectID string `json:"project_id"`
		Token     string `json:"token"`
	}
	if err := json.Unmarshal(b, &session); err != nil {
		return "", err
	}
	if session.ProjectID != projectID || strings.TrimSpace(session.Token) == "" {
		return "", core.NewError(core.ErrUnauthorized, "CLI session is not valid for this project", nil)
	}
	return session.Token, nil
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
		return dispatchWithStore(ctx, projectRoot, args[1:], issueCreateViaDaemon(args[1:]), issueCreateLocal(args[1:]))
	case "list":
		return dispatchWithStore(ctx, projectRoot, args[1:], issueListViaDaemon(args[1:]), issueListLocal(args[1:]))
	case "show":
		if len(args) < 2 {
			return printErr(core.NewError(core.ErrInvalidRequest, "issue ref required", nil))
		}
		return dispatchWithStore(ctx, projectRoot, args[2:], issueShowViaDaemon(args[1]), issueShowLocal(args[1]))
	case "update":
		if len(args) < 2 {
			return printErr(core.NewError(core.ErrInvalidRequest, "issue ref required", nil))
		}
		return dispatchWithStore(ctx, projectRoot, args[2:], issueUpdateViaDaemon(args[1], args[2:]), issueUpdateLocal(args[1], args[2:]))
	case "transition":
		if len(args) < 3 {
			return printErr(core.NewError(core.ErrInvalidRequest, "usage: issue transition REF STATE", nil))
		}
		reason := flagValue(args[3:], "--reason", "")
		dup := flagValue(args[3:], "--duplicate-of", "")
		return dispatchWithStore(ctx, projectRoot, args[3:],
			issueTransitionViaDaemon(args[1], args[2], reason, dup),
			issueTransitionLocal(args[1], args[2], reason, dup))
	case "comment":
		if len(args) < 2 {
			return printErr(core.NewError(core.ErrInvalidRequest, "issue ref required", nil))
		}
		body := flagValue(args[2:], "--body", "")
		return dispatchWithStore(ctx, projectRoot, args[2:], issueCommentViaDaemon(args[1], body), issueCommentLocal(args[1], body))
	case "blocker":
		if len(args) < 4 {
			return printErr(core.NewError(core.ErrInvalidRequest, "usage: issue blocker add/remove REF BLOCKER", nil))
		}
		if args[1] == "add" {
			return dispatchWithStore(ctx, projectRoot, args[4:],
				issueBlockerAddViaDaemon(args[2], args[3]),
				issueBlockerAddLocal(args[2], args[3]))
		}
		if args[1] == "remove" {
			return dispatchWithStore(ctx, projectRoot, args[4:],
				issueBlockerRemoveViaDaemon(args[2], args[3]),
				issueBlockerRemoveLocal(args[2], args[3]))
		}
	case "duplicate":
		if len(args) >= 4 && args[1] == "remove" {
			return dispatchWithStore(ctx, projectRoot, args[4:],
				issueDuplicateRemoveViaDaemon(args[2], args[3]),
				issueDuplicateRemoveLocal(args[2], args[3]))
		}
	case "dispatch":
		if len(args) < 2 {
			return printErr(core.NewError(core.ErrInvalidRequest, "issue ref required", nil))
		}
		return dispatchWithStore(ctx, projectRoot, args[2:], issueDispatchViaDaemon(args[1]), issueDispatchLocal(args[1]))
	case "dispatch-pause":
		if len(args) < 2 {
			return printErr(core.NewError(core.ErrInvalidRequest, "issue ref required", nil))
		}
		reason := flagValue(args[2:], "--reason", "")
		if strings.TrimSpace(reason) == "" {
			return printErr(core.NewError(core.ErrInvalidRequest, "reason is required", nil))
		}
		return dispatchWithStore(ctx, projectRoot, args[2:],
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
		return dispatchWithStore(ctx, projectRoot, args[2:],
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
		return dispatchWithStore(ctx, projectRoot, args[1:], runDispatchViaDaemon(args[0]), runDispatchLocal(args[0]))
	}
	switch args[0] {
	case "list":
		return dispatchWithStore(ctx, projectRoot, args[1:], runListViaDaemon(), runListLocal())
	case "show":
		if len(args) < 2 {
			return printErr(core.NewError(core.ErrInvalidRequest, "run id required", nil))
		}
		return dispatchWithStore(ctx, projectRoot, args[2:], runShowViaDaemon(args[1]), runShowLocal(args[1]))
	case "events":
		if len(args) < 2 {
			return printErr(core.NewError(core.ErrInvalidRequest, "run id required", nil))
		}
		return dispatchWithStore(ctx, projectRoot, args[2:], runEventsViaDaemon(args[1]), runEventsLocal(args[1]))
	case "cancel":
		if len(args) < 2 {
			return printErr(core.NewError(core.ErrInvalidRequest, "run id required", nil))
		}
		reason := flagValue(args[2:], "--reason", "operator cancelled")
		return dispatchWithStore(ctx, projectRoot, args[2:], runCancelViaDaemon(args[1], reason), runCancelLocal(args[1], reason))
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
		return dispatchWithStore(ctx, projectRoot, args[1:], approvalListViaDaemon(), approvalListLocal())
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
		return dispatchWithStore(ctx, projectRoot, args[2:],
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
		return dispatchWithStore(ctx, projectRoot, args[2:],
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
		return dispatchWithStore(ctx, projectRoot, args[2:],
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
		return dispatchWithStore(ctx, projectRoot, args[1:], reviewGetViaDaemon(ref), reviewGetLocal(ref))
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
		return dispatchWithStore(ctx, projectRoot, args[1:],
			workflowDataFromClient("validate"),
			workflowData)
	case "reload":
		return dispatchWithStore(ctx, projectRoot, args[1:],
			workflowDataFromClient("reload"),
			func(st *store.Store) (any, error) {
				wf, _ := config.Load(st.RepoRoot)
				return map[string]any{"reloaded": wf.Validation.Valid, "validation": wf.Validation}, nil
			})
	case "show":
		return dispatchWithStore(ctx, projectRoot, args[1:],
			workflowDataFromClient("show"),
			func(st *store.Store) (any, error) { wf, _ := config.Load(st.RepoRoot); return wf, nil })
	}
	return printErr(core.NewError(core.ErrInvalidRequest, "unknown workflow command", nil))
}
func cmdDiagnostics(args []string) int {
	ctx := contextWithTimeout()
	projectRoot := flagValue(args, "--project", ".")
	if len(args) > 0 && args[0] == "export" {
		return dispatchWithStore(ctx, projectRoot, args[1:],
			diagnosticsExportViaDaemon,
			diagnosticsExportData)
	}
	return dispatchWithStore(ctx, projectRoot, args, diagnosticsViaDaemon, diagnosticsData)
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
	// contains a valid project db.
	if hasFlag(args, "--logout") {
		projectID, err := loginResolveProjectID(flagValue(args, "--project", "."))
		if err != nil {
			return printErr(err)
		}
		if err := daemonclient.DeleteSessionFile(projectID); err != nil {
			return printErr(err)
		}
		return printJSON(map[string]any{"logged_out": true, "project_id": projectID})
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
	return printJSON(map[string]any{
		"project_id": st.ProjectID,
		"session":    "unauthenticated",
	})
}

// loginResolveProjectID opens the project store at the given root and
// returns its project_id. It is split out so that the --logout path
// can resolve a project_id even when the rest of the command would
// otherwise short-circuit on missing flags.
func loginResolveProjectID(projectRoot string) (string, error) {
	st, err := store.Open(projectRoot)
	if err != nil {
		return "", err
	}
	defer st.Close()
	return st.ProjectID, nil
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
  symphony init --issue-prefix LOC
  symphony serve --project . --addr 127.0.0.1:3777 --no-open
  symphony open
  symphony status
  symphony issue create|list|show|update|transition|comment|blocker|duplicate|dispatch|dispatch-pause|dispatch-resume
  symphony run|run list|run show|run events|run cancel
  symphony approval list|approval decide --approve-once|--approve-for-run|--approve-for-session|--deny|--cancel-run
  symphony review ISSUE|review send-to-rework|review mark-done|review path
  symphony workflow validate|reload|show
  symphony diagnostics|diagnostics export
  symphony tool issue get|comment|block; artifact attach; followup create; handoff submit --json -

Operator commands prefer the local daemon (symphony serve) and fall back to
the on-disk store when the daemon is unreachable. The CLI session is minted
automatically when 'symphony serve' starts; rotate it with
'symphony tool login' (token path is separate from the operator REST
session). When no daemon is running and the on-disk store cannot satisfy a
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
