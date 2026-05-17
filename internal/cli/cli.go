package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"local-symphony/internal/app"
	"local-symphony/internal/config"
	"local-symphony/internal/core"
	"local-symphony/internal/httpapi"
	"local-symphony/internal/observability"
	"local-symphony/internal/orchestrator"
	"local-symphony/internal/store"
	"local-symphony/internal/toolgateway"
)

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
		return withStore(args[1:], func(st *store.Store) (any, error) {
			issues, _ := st.ListIssues(store.ListIssueOptions{Limit: 20})
			runs, _ := st.ListRuns()
			return map[string]any{"project_id": st.ProjectID, "repo_root": st.RepoRoot, "issues": issues, "runs": runs}, nil
		})
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
	st, err := store.Open(flagValue(args, "--project", "."))
	if err != nil {
		return printErr(err)
	}
	defer st.Close()
	desc, err := app.RuntimeDescriptor(st.ProjectID)
	if err != nil {
		return printErr(err)
	}
	return printJSON(desc)
}

func cmdIssue(args []string) int {
	if len(args) == 0 {
		printIssueHelp()
		return 2
	}
	switch args[0] {
	case "create":
		return withStore(args[1:], func(st *store.Store) (any, error) {
			pri := store.ParseInt(flagValue(args[1:], "--priority", "3"), 3)
			ac := multiFlag(args[1:], "--acceptance")
			labels := multiFlag(args[1:], "--label")
			return st.CreateIssue(store.CreateIssueInput{Title: flagValue(args[1:], "--title", ""), Description: flagValue(args[1:], "--description", ""), AcceptanceCriteria: ac, Priority: pri, Labels: labels})
		})
	case "list":
		return withStore(args[1:], func(st *store.Store) (any, error) {
			opts := store.ListIssueOptions{Limit: store.ParseInt(flagValue(args[1:], "--limit", "50"), 50), Sort: flagValue(args[1:], "--sort", "priority"), Query: flagValue(args[1:], "--q", "")}
			if stf := flagValue(args[1:], "--state", ""); stf != "" {
				opts.States = strings.Split(stf, ",")
			}
			issues, err := st.ListIssues(opts)
			if err != nil {
				return nil, err
			}
			return map[string]any{"items": issues, "page": map[string]any{"limit": opts.Limit, "next_cursor": nil, "has_more": false}}, nil
		})
	case "show":
		if len(args) < 2 {
			return printErr(core.NewError(core.ErrInvalidRequest, "issue ref required", nil))
		}
		return withStore(args[2:], func(st *store.Store) (any, error) { return st.GetIssue(args[1]) })
	case "update":
		if len(args) < 2 {
			return printErr(core.NewError(core.ErrInvalidRequest, "issue ref required", nil))
		}
		return withStore(args[2:], func(st *store.Store) (any, error) {
			fields := map[string]any{}
			if v := flagValue(args[2:], "--title", ""); v != "" {
				fields["title"] = v
			}
			if v := flagValue(args[2:], "--description", ""); v != "" {
				fields["description"] = v
			}
			if v := flagValue(args[2:], "--priority", ""); v != "" {
				i, _ := strconv.Atoi(v)
				fields["priority"] = i
			}
			ac := multiFlag(args[2:], "--acceptance")
			if len(ac) > 0 {
				fields["acceptance_criteria"] = ac
			}
			labels := multiFlag(args[2:], "--label")
			if len(labels) > 0 {
				fields["labels"] = labels
			}
			return st.UpdateIssue(args[1], fields)
		})
	case "transition":
		if len(args) < 3 {
			return printErr(core.NewError(core.ErrInvalidRequest, "usage: issue transition REF STATE", nil))
		}
		return withStore(args[3:], func(st *store.Store) (any, error) {
			return st.TransitionIssue(args[1], core.IssueState(args[2]), flagValue(args[3:], "--reason", ""), flagValue(args[3:], "--duplicate-of", ""))
		})
	case "comment":
		if len(args) < 2 {
			return printErr(core.NewError(core.ErrInvalidRequest, "issue ref required", nil))
		}
		return withStore(args[2:], func(st *store.Store) (any, error) {
			if err := st.AddComment(args[1], "operator", flagValue(args[2:], "--body", ""), nil); err != nil {
				return nil, err
			}
			return st.GetIssue(args[1])
		})
	case "blocker":
		if len(args) < 4 {
			return printErr(core.NewError(core.ErrInvalidRequest, "usage: issue blocker add/remove REF BLOCKER", nil))
		}
		if args[1] == "add" {
			return withStore(args[4:], func(st *store.Store) (any, error) { return st.AddBlocker(args[2], args[3]) })
		}
		if args[1] == "remove" {
			return withStore(args[4:], func(st *store.Store) (any, error) { return st.RemoveBlocker(args[2], args[3]) })
		}
	case "duplicate":
		if len(args) >= 4 && args[1] == "remove" {
			return withStore(args[4:], func(st *store.Store) (any, error) { return st.RemoveDuplicate(args[2], args[3]) })
		}
	case "dispatch":
		if len(args) < 2 {
			return printErr(core.NewError(core.ErrInvalidRequest, "issue ref required", nil))
		}
		return withStore(args[2:], func(st *store.Store) (any, error) {
			return orchestrator.Orchestrator{Store: st}.DispatchIssue(args[1], "manual")
		})
	case "dispatch-pause":
		if len(args) < 2 {
			return printErr(core.NewError(core.ErrInvalidRequest, "issue ref required", nil))
		}
		reason := flagValue(args[2:], "--reason", "")
		if strings.TrimSpace(reason) == "" {
			return printErr(core.NewError(core.ErrInvalidRequest, "reason is required", nil))
		}
		return withStore(args[2:], func(st *store.Store) (any, error) { return st.DispatchPause(args[1], reason) })
	case "dispatch-resume":
		if len(args) < 2 {
			return printErr(core.NewError(core.ErrInvalidRequest, "issue ref required", nil))
		}
		reason := flagValue(args[2:], "--reason", "")
		if strings.TrimSpace(reason) == "" {
			return printErr(core.NewError(core.ErrInvalidRequest, "reason is required", nil))
		}
		return withStore(args[2:], func(st *store.Store) (any, error) { return st.DispatchResume(args[1], reason) })
	}
	return printErr(core.NewError(core.ErrInvalidRequest, "unknown issue command", nil))
}

func cmdRun(args []string) int {
	if len(args) == 0 {
		printRunHelp()
		return 2
	}
	if strings.HasPrefix(args[0], "LOC-") || strings.HasPrefix(args[0], "iss_") {
		return withStore(args[1:], func(st *store.Store) (any, error) {
			return orchestrator.Orchestrator{Store: st}.DispatchIssue(args[0], "manual")
		})
	}
	switch args[0] {
	case "list":
		return withStore(args[1:], func(st *store.Store) (any, error) { return st.ListRuns() })
	case "show":
		if len(args) < 2 {
			return printErr(core.NewError(core.ErrInvalidRequest, "run id required", nil))
		}
		return withStore(args[2:], func(st *store.Store) (any, error) { return st.GetRun(args[1]) })
	case "events":
		if len(args) < 2 {
			return printErr(core.NewError(core.ErrInvalidRequest, "run id required", nil))
		}
		return withStore(args[2:], func(st *store.Store) (any, error) { return st.RunEvents(args[1], 0, 500) })
	case "cancel":
		if len(args) < 2 {
			return printErr(core.NewError(core.ErrInvalidRequest, "run id required", nil))
		}
		return withStore(args[2:], func(st *store.Store) (any, error) {
			err := st.CancelRun(args[1], flagValue(args[2:], "--reason", "operator cancelled"))
			if err != nil {
				return nil, err
			}
			return st.GetRun(args[1])
		})
	}
	return printErr(core.NewError(core.ErrInvalidRequest, "unknown run command", nil))
}
func cmdApproval(args []string) int {
	if len(args) == 0 {
		return printErr(core.NewError(core.ErrInvalidRequest, "approval command required", nil))
	}
	switch args[0] {
	case "list":
		return withStore(args[1:], func(st *store.Store) (any, error) { return st.PendingApprovals() })
	case "decide":
		if len(args) < 2 {
			return printErr(core.NewError(core.ErrInvalidRequest, "approval id required", nil))
		}
		return withStore(args[2:], func(st *store.Store) (any, error) {
			status := ""
			for _, p := range []string{"approve-once", "approve-for-run", "approve-for-session", "deny", "cancel-run"} {
				if hasFlag(args[2:], "--"+p) {
					status = map[string]string{"approve-once": "approved_once", "approve-for-run": "approved_for_run", "approve-for-session": "approved_for_session", "deny": "denied", "cancel-run": "cancelled"}[p]
				}
			}
			if status == "" {
				return nil, core.NewError(core.ErrInvalidRequest, "approval decision required", nil)
			}
			err := st.DecideApproval(args[1], status, flagValue(args[2:], "--reason", ""))
			return map[string]any{"id": args[1], "status": status}, err
		})
	}
	return printErr(core.NewError(core.ErrInvalidRequest, "unknown approval command", nil))
}
func cmdReview(args []string) int {
	if len(args) == 0 {
		printReviewHelp()
		return 2
	}
	switch args[0] {
	case "send-to-rework":
		if len(args) < 2 {
			return printErr(core.NewError(core.ErrInvalidRequest, "issue ref required", nil))
		}
		reason := flagValue(args[2:], "--reason", "")
		if strings.TrimSpace(reason) == "" {
			return printErr(core.NewError(core.ErrInvalidRequest, "reason is required", nil))
		}
		return withStore(args[2:], func(st *store.Store) (any, error) { return st.SendToRework(args[1], reason) })
	case "mark-done":
		if len(args) < 2 {
			return printErr(core.NewError(core.ErrInvalidRequest, "issue ref required", nil))
		}
		reason := flagValue(args[2:], "--reason", "")
		if strings.TrimSpace(reason) == "" {
			return printErr(core.NewError(core.ErrInvalidRequest, "reason is required", nil))
		}
		return withStore(args[2:], func(st *store.Store) (any, error) { return st.MarkDone(args[1], reason) })
	case "path":
		if len(args) < 2 {
			return printErr(core.NewError(core.ErrInvalidRequest, "issue ref required", nil))
		}
		return withStore(args[2:], reviewMetaFor(args[1]))
	default:
		ref := args[0]
		return withStore(args[1:], reviewMetaFor(ref))
	}
}
func reviewMetaFor(ref string) func(*store.Store) (any, error) {
	return func(st *store.Store) (any, error) {
		row, err := st.ReviewPacketRow(ref)
		if err != nil {
			return nil, err
		}
		arts, _ := st.ArtifactsForReview(row["id"].String())
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
	switch args[0] {
	case "validate":
		return withStore(args[1:], func(st *store.Store) (any, error) {
			wf, _ := config.Load(st.RepoRoot)
			return map[string]any{"source": "current_filesystem", "workflow_path": wf.Path, "validation": wf.Validation, "side_effects": map[string]any{"effective_config_replaced": false, "last_valid_config_updated": false, "prompt_rendered": false, "run_dispatched": false, "review_artifacts_written": false}}, nil
		})
	case "reload":
		return withStore(args[1:], func(st *store.Store) (any, error) {
			wf, _ := config.Load(st.RepoRoot)
			return map[string]any{"reloaded": wf.Validation.Valid, "validation": wf.Validation}, nil
		})
	case "show":
		return withStore(args[1:], func(st *store.Store) (any, error) { wf, _ := config.Load(st.RepoRoot); return wf, nil })
	}
	return printErr(core.NewError(core.ErrInvalidRequest, "unknown workflow command", nil))
}
func cmdDiagnostics(args []string) int {
	if len(args) > 0 && args[0] == "export" {
		return withStore(args[1:], func(st *store.Store) (any, error) {
			p, err := observability.Export(st)
			if err != nil {
				return nil, err
			}
			return map[string]any{"path": p}, nil
		})
	}
	return withStore(args, func(st *store.Store) (any, error) { return observability.Diagnostics(st), nil })
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
		if resp.Error.Code == "handoff_conflict" {
			return 7
		}
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
