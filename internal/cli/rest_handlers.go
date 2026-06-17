package cli

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"local-symphony/internal/core"
	"local-symphony/internal/daemonclient"
	"local-symphony/internal/orchestrator"
	"local-symphony/internal/store"
)

// The functions in this file are the REST-side halves of every operator
// command. They translate CLI flag shapes into URL paths and JSON bodies,
// invoke the daemon, and return the unwrapped data as map[string]any so
// the dispatcher can print the same shape regardless of source.

// issueListLocal keeps the original local-store behavior so the offline
// fallback still produces stable output.
func issueListLocal(args []string) func(*store.Store) (any, error) {
	return func(st *store.Store) (any, error) {
		opts := store.ListIssueOptions{Limit: store.ParseInt(flagValue(args, "--limit", "50"), 50), Sort: flagValue(args, "--sort", "priority"), Query: flagValue(args, "--q", "")}
		if stf := flagValue(args, "--state", ""); stf != "" {
			opts.States = strings.Split(stf, ",")
		}
		issues, err := st.ListIssues(opts)
		if err != nil {
			return nil, err
		}
		return map[string]any{"items": issues, "page": map[string]any{"limit": opts.Limit, "next_cursor": nil, "has_more": false}}, nil
	}
}

// issueListViaDaemon maps CLI flags to the daemon's /api/v1/issues GET.
func issueListViaDaemon(args []string) func(*daemonclient.Client) (any, error) {
	return func(c *daemonclient.Client) (any, error) {
		q := url.Values{}
		if v := flagValue(args, "--limit", ""); v != "" {
			q.Set("limit", v)
		}
		if v := flagValue(args, "--sort", ""); v != "" {
			q.Set("sort", v)
		}
		if v := flagValue(args, "--q", ""); v != "" {
			q.Set("q", v)
		}
		if v := flagValue(args, "--state", ""); v != "" {
			q.Set("state", v)
		}
		endpoint := "/api/v1/issues"
		if encoded := q.Encode(); encoded != "" {
			endpoint += "?" + encoded
		}
		return c.UnwrapMap(context.Background(), "GET", endpoint, nil)
	}
}

// issueShowLocal / issueShowViaDaemon handle `symphony issue show REF`.
func issueShowLocal(ref string) func(*store.Store) (any, error) {
	return func(st *store.Store) (any, error) {
		return st.GetIssue(ref)
	}
}

func issueShowViaDaemon(ref string) func(*daemonclient.Client) (any, error) {
	return func(c *daemonclient.Client) (any, error) {
		return c.UnwrapMap(context.Background(), "GET", "/api/v1/issues/"+url.PathEscape(ref), nil)
	}
}

// parseIssueCreateArgs pulls the create-issue flags out of args and
// returns a structured input. Validation errors (e.g. non-integer
// --priority) are returned here so the CLI surfaces them before the
// dispatcher decides between daemon and local store. Both
// issueCreateLocal and issueCreateViaDaemon consume the parsed
// input; this guarantees the validation contract is identical
// regardless of which path runs.
func parseIssueCreateArgs(args []string) (map[string]any, error) {
	body := map[string]any{
		"title":               flagValue(args, "--title", ""),
		"description":         flagValue(args, "--description", ""),
		"acceptance_criteria": multiFlag(args, "--acceptance"),
		"labels":              multiFlag(args, "--label"),
	}
	pri := 3
	if rawPriority, ok := flagValuePresent(args, "--priority"); ok {
		parsed, err := strconv.Atoi(rawPriority)
		if err != nil {
			return nil, core.NewError(core.ErrInvalidRequest, "--priority must be an integer", map[string]any{"priority": rawPriority})
		}
		pri = parsed
	}
	body["priority"] = pri
	return body, nil
}

// issueCreateLocal / issueCreateViaDaemon handle `symphony issue create`.
func issueCreateLocal(args []string) func(*store.Store) (any, error) {
	body, err := parseIssueCreateArgs(args)
	if err != nil {
		return func(*store.Store) (any, error) { return nil, err }
	}
	return func(st *store.Store) (any, error) {
		return st.CreateIssue(store.CreateIssueInput{
			Title:              body["title"].(string),
			Description:        body["description"].(string),
			AcceptanceCriteria: stringSlice(body["acceptance_criteria"]),
			Priority:           body["priority"].(int),
			Labels:             stringSlice(body["labels"]),
		})
	}
}

func issueCreateViaDaemon(args []string) func(*daemonclient.Client) (any, error) {
	body, err := parseIssueCreateArgs(args)
	if err != nil {
		return func(*daemonclient.Client) (any, error) { return nil, err }
	}
	return func(c *daemonclient.Client) (any, error) {
		return c.UnwrapMap(context.Background(), "POST", "/api/v1/issues", body)
	}
}

// stringSlice is a small adapter that converts a generic []any to
// []string. It is only used in this file's create-issue glue; the
// daemon client already accepts map[string]any, so the conversion
// happens at the local-store boundary.
func stringSlice(v any) []string {
	if v == nil {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// issueUpdateLocal / issueUpdateViaDaemon handle `symphony issue update`.
func issueUpdateLocal(ref string, args []string) func(*store.Store) (any, error) {
	return func(st *store.Store) (any, error) {
		fields := map[string]any{}
		if v := flagValue(args, "--title", ""); v != "" {
			fields["title"] = v
		}
		if v := flagValue(args, "--description", ""); v != "" {
			fields["description"] = v
		}
		if v := flagValue(args, "--priority", ""); v != "" {
			i, _ := strconv.Atoi(v)
			fields["priority"] = i
		}
		ac := multiFlag(args, "--acceptance")
		if len(ac) > 0 {
			fields["acceptance_criteria"] = ac
		}
		labels := multiFlag(args, "--label")
		if len(labels) > 0 {
			fields["labels"] = labels
		}
		return st.UpdateIssue(ref, fields)
	}
}

func issueUpdateViaDaemon(ref string, args []string) func(*daemonclient.Client) (any, error) {
	return func(c *daemonclient.Client) (any, error) {
		body := map[string]any{}
		if v := flagValue(args, "--title", ""); v != "" {
			body["title"] = v
		}
		if v := flagValue(args, "--description", ""); v != "" {
			body["description"] = v
		}
		if v := flagValue(args, "--priority", ""); v != "" {
			i, _ := strconv.Atoi(v)
			body["priority"] = i
		}
		if ac := multiFlag(args, "--acceptance"); len(ac) > 0 {
			body["acceptance_criteria"] = ac
		}
		if labels := multiFlag(args, "--label"); len(labels) > 0 {
			body["labels"] = labels
		}
		return c.UnwrapMap(context.Background(), "PATCH", "/api/v1/issues/"+url.PathEscape(ref), body)
	}
}

// issueTransitionLocal / issueTransitionViaDaemon handle `symphony issue
// transition REF STATE`.
func issueTransitionLocal(ref, state, reason, duplicateOf string) func(*store.Store) (any, error) {
	return func(st *store.Store) (any, error) {
		return st.TransitionIssue(ref, core.IssueState(state), reason, duplicateOf)
	}
}

func issueTransitionViaDaemon(ref, state, reason, duplicateOf string) func(*daemonclient.Client) (any, error) {
	return func(c *daemonclient.Client) (any, error) {
		body := map[string]any{"state": state, "duplicate_of": duplicateOf}
		if reason != "" {
			body["reason"] = reason
		}
		return c.UnwrapMap(context.Background(), "POST", "/api/v1/issues/"+url.PathEscape(ref)+"/transition", body)
	}
}

// issueCommentLocal / issueCommentViaDaemon handle `symphony issue comment`.
func issueCommentLocal(ref, body string) func(*store.Store) (any, error) {
	return func(st *store.Store) (any, error) {
		if err := st.AddComment(ref, "operator", body, nil); err != nil {
			return nil, err
		}
		return st.GetIssue(ref)
	}
}

func issueCommentViaDaemon(ref, body string) func(*daemonclient.Client) (any, error) {
	return func(c *daemonclient.Client) (any, error) {
		return c.UnwrapMap(context.Background(), "POST", "/api/v1/issues/"+url.PathEscape(ref)+"/comments", map[string]any{"body": body})
	}
}

// issueBlockerAddLocal / issueBlockerAddViaDaemon handle `symphony issue blocker add`.
func issueBlockerAddLocal(ref, blocker string) func(*store.Store) (any, error) {
	return func(st *store.Store) (any, error) { return st.AddBlocker(ref, blocker) }
}

func issueBlockerAddViaDaemon(ref, blocker string) func(*daemonclient.Client) (any, error) {
	return func(c *daemonclient.Client) (any, error) {
		return c.UnwrapMap(context.Background(), "POST", "/api/v1/issues/"+url.PathEscape(ref)+"/blockers", map[string]any{"blocked_by": blocker})
	}
}

func issueBlockerRemoveLocal(ref, blocker string) func(*store.Store) (any, error) {
	return func(st *store.Store) (any, error) { return st.RemoveBlocker(ref, blocker) }
}

func issueBlockerRemoveViaDaemon(ref, blocker string) func(*daemonclient.Client) (any, error) {
	return func(c *daemonclient.Client) (any, error) {
		return c.UnwrapMap(context.Background(), "DELETE", "/api/v1/issues/"+url.PathEscape(ref)+"/blockers/"+url.PathEscape(blocker), nil)
	}
}

func issueDuplicateRemoveLocal(ref, canonical string) func(*store.Store) (any, error) {
	return func(st *store.Store) (any, error) { return st.RemoveDuplicate(ref, canonical) }
}

func issueDuplicateRemoveViaDaemon(ref, canonical string) func(*daemonclient.Client) (any, error) {
	return func(c *daemonclient.Client) (any, error) {
		return c.UnwrapMap(context.Background(), "DELETE", "/api/v1/issues/"+url.PathEscape(ref)+"/duplicates/"+url.PathEscape(canonical), nil)
	}
}

// issueDispatchLocal / issueDispatchViaDaemon handle `symphony issue dispatch`.
func issueDispatchLocal(ref string) func(*store.Store) (any, error) {
	return func(st *store.Store) (any, error) {
		return orchestrator.Orchestrator{Store: st}.DispatchIssue(ref, "manual")
	}
}

func issueDispatchViaDaemon(ref string) func(*daemonclient.Client) (any, error) {
	return func(c *daemonclient.Client) (any, error) {
		return c.UnwrapMap(context.Background(), "POST", "/api/v1/issues/"+url.PathEscape(ref)+"/dispatch", nil)
	}
}

func issueDispatchPauseLocal(ref, reason string) func(*store.Store) (any, error) {
	return func(st *store.Store) (any, error) { return st.DispatchPause(ref, reason) }
}

func issueDispatchPauseViaDaemon(ref, reason string) func(*daemonclient.Client) (any, error) {
	return func(c *daemonclient.Client) (any, error) {
		return c.UnwrapMap(context.Background(), "POST", "/api/v1/issues/"+url.PathEscape(ref)+"/dispatch-pause", map[string]any{"reason": reason})
	}
}

func issueDispatchResumeLocal(ref, reason string) func(*store.Store) (any, error) {
	return func(st *store.Store) (any, error) { return st.DispatchResume(ref, reason) }
}

func issueDispatchResumeViaDaemon(ref, reason string) func(*daemonclient.Client) (any, error) {
	return func(c *daemonclient.Client) (any, error) {
		return c.UnwrapMap(context.Background(), "POST", "/api/v1/issues/"+url.PathEscape(ref)+"/dispatch-resume", map[string]any{"reason": reason})
	}
}

// runListLocal / runListViaDaemon handle `symphony run list`.
// Both paths return the same {"items":[...]} object shape so
// scripts and dashboards see a stable schema regardless of
// whether the daemon is reachable.
func runListLocal() func(*store.Store) (any, error) {
	return func(st *store.Store) (any, error) {
		items, err := st.ListRuns()
		if err != nil {
			return nil, err
		}
		return map[string]any{"items": items}, nil
	}
}

func runListViaDaemon() func(*daemonclient.Client) (any, error) {
	return func(c *daemonclient.Client) (any, error) {
		// /api/v1/runs returns a JSON array, so use UnwrapArray.
		items, err := c.UnwrapArray(context.Background(), "GET", "/api/v1/runs", nil)
		if err != nil {
			return nil, err
		}
		return map[string]any{"items": items}, nil
	}
}

func runShowLocal(id string) func(*store.Store) (any, error) {
	return func(st *store.Store) (any, error) { return st.GetRun(id) }
}

func runShowViaDaemon(id string) func(*daemonclient.Client) (any, error) {
	return func(c *daemonclient.Client) (any, error) {
		return c.UnwrapMap(context.Background(), "GET", "/api/v1/runs/"+url.PathEscape(id), nil)
	}
}

// runEventsLocal returns the same {"items":[...]} object shape
// as the daemon-backed path. Stable schema is required by the
// v1 plan: scripts must not see different output structures
// based on daemon availability.
func runEventsLocal(id string) func(*store.Store) (any, error) {
	return func(st *store.Store) (any, error) {
		items, err := st.RunEvents(id, 0, 500)
		if err != nil {
			return nil, err
		}
		return map[string]any{"items": items}, nil
	}
}

func runEventsViaDaemon(id string) func(*daemonclient.Client) (any, error) {
	return func(c *daemonclient.Client) (any, error) {
		// /api/v1/runs/{id}/events returns a JSON array, so use UnwrapArray.
		items, err := c.UnwrapArray(context.Background(), "GET", "/api/v1/runs/"+url.PathEscape(id)+"/events", nil)
		if err != nil {
			return nil, err
		}
		return map[string]any{"items": items}, nil
	}
}

func runCancelLocal(id, reason string) func(*store.Store) (any, error) {
	return func(st *store.Store) (any, error) {
		if err := st.CancelRun(id, reason); err != nil {
			return nil, err
		}
		return st.GetRun(id)
	}
}

func runCancelViaDaemon(id, reason string) func(*daemonclient.Client) (any, error) {
	return func(c *daemonclient.Client) (any, error) {
		return c.UnwrapMap(context.Background(), "POST", "/api/v1/runs/"+url.PathEscape(id)+"/cancel", map[string]any{"reason": reason})
	}
}

func runDispatchLocal(ref string) func(*store.Store) (any, error) {
	return func(st *store.Store) (any, error) {
		return orchestrator.Orchestrator{Store: st}.DispatchIssue(ref, "manual")
	}
}

func runDispatchViaDaemon(ref string) func(*daemonclient.Client) (any, error) {
	return func(c *daemonclient.Client) (any, error) {
		return c.UnwrapMap(context.Background(), "POST", "/api/v1/issues/"+url.PathEscape(ref)+"/dispatch", nil)
	}
}

// approvalListLocal / approvalListViaDaemon handle `symphony approval list`.
// Both paths return the same {"items":[...]} object shape for
// parity with the daemon-backed path (see runListLocal for the
// design rationale).
func approvalListLocal() func(*store.Store) (any, error) {
	return func(st *store.Store) (any, error) {
		items, err := st.PendingApprovals()
		if err != nil {
			return nil, err
		}
		return map[string]any{"items": items}, nil
	}
}

func approvalListViaDaemon() func(*daemonclient.Client) (any, error) {
	return func(c *daemonclient.Client) (any, error) {
		// /api/v1/approvals returns a JSON array, so use UnwrapArray.
		items, err := c.UnwrapArray(context.Background(), "GET", "/api/v1/approvals", nil)
		if err != nil {
			return nil, err
		}
		return map[string]any{"items": items}, nil
	}
}

func approvalDecideLocal(id, status, reason string) func(*store.Store) (any, error) {
	return func(st *store.Store) (any, error) {
		if err := st.DecideApproval(id, status, reason); err != nil {
			return nil, err
		}
		return map[string]any{"id": id, "status": status}, nil
	}
}

func approvalDecideViaDaemon(id, decision, reason string) func(*daemonclient.Client) (any, error) {
	return func(c *daemonclient.Client) (any, error) {
		return c.UnwrapMap(context.Background(), "POST", "/api/v1/approvals/"+url.PathEscape(id)+"/decide", map[string]any{"decision": decision, "reason": reason})
	}
}

// reviewGetLocal / reviewGetViaDaemon handle `symphony review REF` and
// `symphony review path REF`.
func reviewGetLocal(ref string) func(*store.Store) (any, error) {
	return func(st *store.Store) (any, error) {
		return st.ReviewPacketProjection(ref)
	}
}

func reviewGetViaDaemon(ref string) func(*daemonclient.Client) (any, error) {
	return func(c *daemonclient.Client) (any, error) {
		return c.UnwrapMap(context.Background(), "GET", "/api/v1/reviews/"+url.PathEscape(ref), nil)
	}
}

func reviewSendToReworkLocal(ref, reason string) func(*store.Store) (any, error) {
	return func(st *store.Store) (any, error) { return st.SendToRework(ref, reason) }
}

func reviewSendToReworkViaDaemon(ref, reason string) func(*daemonclient.Client) (any, error) {
	return func(c *daemonclient.Client) (any, error) {
		return c.UnwrapMap(context.Background(), "POST", "/api/v1/reviews/"+url.PathEscape(ref)+"/send-to-rework", map[string]any{"reason": reason})
	}
}

func reviewMarkDoneLocal(ref, reason string) func(*store.Store) (any, error) {
	return func(st *store.Store) (any, error) { return st.MarkDone(ref, reason) }
}

func reviewMarkDoneViaDaemon(ref, reason string) func(*daemonclient.Client) (any, error) {
	return func(c *daemonclient.Client) (any, error) {
		return c.UnwrapMap(context.Background(), "POST", "/api/v1/reviews/"+url.PathEscape(ref)+"/mark-done", map[string]any{"reason": reason})
	}
}

// approvalDecisionString maps a CLI flag name to the daemon's decision
// value. The local store uses internal status names; the daemon uses
// external decision names. We translate at the boundary.
func approvalDecisionString(flagName string) (string, bool) {
	switch flagName {
	case "--approve-once":
		return "approve_once", true
	case "--approve-for-run":
		return "approve_for_run", true
	case "--approve-for-session":
		return "approve_for_session", true
	case "--deny":
		return "deny", true
	case "--cancel-run":
		return "cancel_run", true
	}
	return "", false
}

// dispatchLocal / dispatchViaDaemon are aliases used by the issue and
// run commands; the daemon endpoint is the same.
func dispatchLocal(ref string) func(*store.Store) (any, error) {
	return issueDispatchLocal(ref)
}

func dispatchViaDaemon(ref string) func(*daemonclient.Client) (any, error) {
	return issueDispatchViaDaemon(ref)
}
