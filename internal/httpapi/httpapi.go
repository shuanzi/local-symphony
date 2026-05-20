package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"local-symphony/internal/config"
	"local-symphony/internal/core"
	"local-symphony/internal/db"
	"local-symphony/internal/observability"
	"local-symphony/internal/orchestrator"
	"local-symphony/internal/security"
	"local-symphony/internal/store"
	"local-symphony/internal/toolgateway"
)

const sessionCookieName = "symphony_session"

type Server struct {
	Store     *store.Store
	Orch      orchestrator.Orchestrator
	csrfToken string
}

func New(st *store.Store) *Server {
	return &Server{Store: st, Orch: orchestrator.Orchestrator{Store: st}, csrfToken: security.NewToken()}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/tool/v1/call", s.handleTool)
	mux.HandleFunc("/api/v1/", s.handleAPI)
	mux.HandleFunc("/", s.serveDashboard)
	return mux
}

func (s *Server) serveDashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	distRoot, indexPath, found := dashboardDist(s.Store.RepoRoot)
	if found {
		requestPath := strings.TrimPrefix(r.URL.Path, "/")
		if requestPath == "" {
			requestPath = "index.html"
		}
		filePath := filepath.Join(distRoot, requestPath)
		if safePath, ok := safeContainedFilePath(filePath, distRoot); ok {
			if st, err := os.Stat(safePath); err == nil && !st.IsDir() {
				http.ServeFile(w, r, safePath)
				return
			}
		}
		http.ServeFile(w, r, indexPath)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html><html><head><title>Local Symphony</title></head><body><h1>Local Symphony</h1><p>The REST API is available under <code>/api/v1</code>; Tool Gateway is available at <code>/tool/v1/call</code>.</p><p>Dashboard assets were not found in a trusted install location. Set <code>SYMPHONY_DASHBOARD_DIST</code> to the absolute path of a built dashboard dist directory, or install assets next to the <code>symphony</code> executable.</p></body></html>`))
}

func (s *Server) handleTool(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method", 405)
		return
	}
	token := bearerTokenFromHeader(r.Header.Get("Authorization"))
	var req toolgateway.Request
	if err := readRequiredStrictJSONObjectBody(r, &req, map[string]bool{"tool": true, "input": true, "client": true}); err != nil {
		writeJSON(w, 400, toolgateway.Response{Success: false, Error: &toolgateway.ToolError{Code: "invalid_request", Message: err.Error()}})
		return
	}
	if strings.TrimSpace(req.Tool) == "" || req.Input == nil {
		writeJSON(w, 400, toolgateway.Response{Success: false, Error: &toolgateway.ToolError{Code: "invalid_request", Message: "tool and input are required"}})
		return
	}
	cwd := r.Header.Get("X-Symphony-Cwd")
	if cwd == "" {
		cwd = s.Store.RepoRoot
	}
	resp := toolgateway.Gateway{Store: s.Store}.Call(token, cwd, req)
	code := 200
	if resp.Error != nil {
		code = 400
		if resp.Error.Code == "tool_token_invalid" {
			code = 401
		}
	}
	writeJSON(w, code, resp)
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	if path == "" {
		path = "/"
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !isPublicAPI(path, r.Method) && !s.authorizeAPI(w, r) {
		return
	}
	if requiresCSRF(path, r.Method) {
		if !s.authorizeCommand(w, r) {
			return
		}
	}
	switch {
	case r.Method == "GET" && path == "/health":
		ok(w, map[string]any{"ok": true, "project_id": s.Store.ProjectID})
	case r.Method == "GET" && path == "/state":
		s.state(w)
	case r.Method == "GET" && path == "/events":
		s.events(w, r, "")
	case r.Method == "GET" && path == "/events/stream":
		s.sse(w, r, "", "")
	case r.Method == "POST" && path == "/auth/exchange":
		s.authExchange(w, r)
	case r.Method == "GET" && path == "/auth/session":
		if !s.validSession(r) {
			ok(w, map[string]any{"authenticated": false, "project_id": s.Store.ProjectID})
			return
		}
		ok(w, map[string]any{"authenticated": true, "project_id": s.Store.ProjectID, "csrf_token": s.csrfToken, "csrf": s.csrfToken})
	case r.Method == "POST" && path == "/auth/logout":
		s.authLogout(w, r)
	case r.Method == "POST" && path == "/auth/open-token":
		s.openToken(w, r)
	case r.Method == "POST" && path == "/auth/cli-token/rotate":
		s.rotateCLIToken(w, r)
	case path == "/issues" && r.Method == "GET":
		s.listIssues(w, r)
	case path == "/issues" && r.Method == "POST":
		s.createIssue(w, r)
	case strings.HasPrefix(path, "/issues/"):
		s.issueRoutes(w, r, strings.TrimPrefix(path, "/issues/"))
	case path == "/runs" && r.Method == "GET":
		runs, err := s.Store.ListRuns()
		if err != nil {
			apiErr(w, err)
			return
		}
		ok(w, runs)
	case strings.HasPrefix(path, "/runs/"):
		s.runRoutes(w, r, strings.TrimPrefix(path, "/runs/"))
	case path == "/approvals" && r.Method == "GET":
		a, err := s.Store.PendingApprovals()
		if err != nil {
			apiErr(w, err)
			return
		}
		ok(w, a)
	case strings.HasPrefix(path, "/approvals/"):
		s.approvalRoutes(w, r, strings.TrimPrefix(path, "/approvals/"))
	case strings.HasPrefix(path, "/reviews/"):
		s.reviewRoutes(w, r, strings.TrimPrefix(path, "/reviews/"))
	case strings.HasPrefix(path, "/artifacts/"):
		s.artifactRoutes(w, r, strings.TrimPrefix(path, "/artifacts/"))
	case path == "/workflow" && r.Method == "GET":
		wf, _ := config.Load(s.Store.RepoRoot)
		ok(w, map[string]any{"workflow_path": wf.Path, "validation": wf.Validation, "config": wf.Config})
	case path == "/workflow/validate" && r.Method == "POST":
		s.workflowValidate(w, r)
	case path == "/workflow/render-preview" && r.Method == "POST":
		s.workflowPreview(w, r)
	case path == "/workflow/reload" && r.Method == "POST":
		if err := requireEmptyObjectBody(r); err != nil {
			apiErr(w, err)
			return
		}
		wf, _ := config.Load(s.Store.RepoRoot)
		ok(w, map[string]any{"reloaded": wf.Validation.Valid, "validation": wf.Validation})
	case path == "/diagnostics" && r.Method == "GET":
		ok(w, observability.Diagnostics(s.Store))
	case path == "/diagnostics/export" && r.Method == "POST":
		includeRawLogs, err := diagnosticsExportOptions(r)
		if err != nil {
			apiErr(w, err)
			return
		}
		if includeRawLogs {
			apiErr(w, core.NewError(core.ErrRawLogAccessUnsupported, "raw log export is not supported", nil))
			return
		}
		p, err := observability.Export(s.Store)
		if err != nil {
			apiErr(w, err)
			return
		}
		id := core.NewID("art_")
		if err := s.Store.InsertArtifact(store.ArtifactRecord{ID: id, Kind: "diagnostic", Path: p, Redacted: true}); err != nil {
			apiErr(w, err)
			return
		}
		ok(w, map[string]any{"artifact_id": id, "path": p})
	default:
		apiErr(w, core.NewError(core.ErrNotFound, "route not found", map[string]any{"path": path}))
	}
}

func (s *Server) state(w http.ResponseWriter) {
	issues, err := s.Store.ListIssues(store.ListIssueOptions{Limit: 20})
	if err != nil {
		apiErr(w, err)
		return
	}
	runs, err := s.Store.ListRuns()
	if err != nil {
		apiErr(w, err)
		return
	}
	ok(w, map[string]any{"project_id": s.Store.ProjectID, "repo_root": s.Store.RepoRoot, "issues": issues, "runs": runs})
}
func (s *Server) listIssues(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, err := parseIssueLimit(q.Get("limit"))
	if err != nil {
		apiErr(w, err)
		return
	}
	sort := q.Get("sort")
	if sort != "" && sort != "priority" && sort != "updated" && sort != "identifier" {
		apiErr(w, core.NewError(core.ErrInvalidRequest, "sort must be one of priority, updated, or identifier", nil))
		return
	}
	opts := store.ListIssueOptions{Sort: q.Get("sort"), Query: q.Get("q")}
	if states := repeatedCSV(q["state"]); len(states) > 0 {
		for _, state := range states {
			if !validIssueState(state) {
				apiErr(w, core.NewError(core.ErrInvalidRequest, "state must be a valid issue state", nil))
				return
			}
		}
		opts.States = states
	}
	dispatchPaused, err := parseOptionalBool(q.Get("dispatch_paused"), "dispatch_paused")
	if err != nil {
		apiErr(w, err)
		return
	}
	opts.DispatchPaused = dispatchPaused
	opts.Labels = repeatedCSV(q["label"])
	cursor := 0
	if rawCursor := strings.TrimSpace(q.Get("cursor")); rawCursor != "" {
		n, err := strconv.Atoi(rawCursor)
		if err != nil || n < 0 {
			apiErr(w, core.NewError(core.ErrInvalidRequest, "cursor must be a non-negative integer offset", nil))
			return
		}
		cursor = n
	}
	opts.Limit = limit + 1
	opts.Offset = cursor
	items, err := s.Store.ListIssues(opts)
	if err != nil {
		apiErr(w, err)
		return
	}
	var nextCursor any
	if len(items) > limit {
		items = items[:limit]
		nextCursor = strconv.Itoa(cursor + limit)
	}
	ok(w, map[string]any{"items": items, "page": map[string]any{"limit": limit, "next_cursor": nextCursor, "has_more": nextCursor != nil}})
}
func (s *Server) createIssue(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title              string   `json:"title"`
		Description        string   `json:"description"`
		AcceptanceCriteria []string `json:"acceptance_criteria"`
		Priority           int      `json:"priority"`
		Labels             []string `json:"labels"`
	}
	allowedFields := map[string]bool{
		"title":               true,
		"description":         true,
		"acceptance_criteria": true,
		"priority":            true,
		"labels":              true,
	}
	if readBodyDisallowUnknownFields(r, &in, w, allowedFields) {
		return
	}
	iss, err := s.Store.CreateIssue(store.CreateIssueInput{Title: in.Title, Description: in.Description, AcceptanceCriteria: in.AcceptanceCriteria, Priority: in.Priority, Labels: in.Labels})
	if err != nil {
		apiErr(w, err)
		return
	}
	created(w, iss)
}

func (s *Server) issueRoutes(w http.ResponseWriter, r *http.Request, rest string) {
	parts := strings.Split(rest, "/")
	ref := parts[0]
	if len(parts) == 1 {
		if r.Method == "GET" {
			iss, err := s.Store.GetIssue(ref)
			if err != nil {
				apiErr(w, err)
				return
			}
			ok(w, iss)
			return
		}
		if r.Method == "PATCH" {
			var raw map[string]any
			rawBody, err := decodeSingleJSONObjectBody(json.NewDecoder(r.Body))
			if err != nil {
				apiErr(w, err)
				return
			}
			dec := json.NewDecoder(bytes.NewReader(rawBody))
			dec.UseNumber()
			if err := dec.Decode(&raw); err != nil {
				apiErr(w, core.NewError(core.ErrInvalidRequest, err.Error(), nil))
				return
			}
			allowedFields := map[string]bool{
				"title":               true,
				"description":         true,
				"priority":            true,
				"acceptance_criteria": true,
				"labels":              true,
			}
			for key := range raw {
				if !allowedFields[key] {
					apiErr(w, core.NewError(core.ErrInvalidRequest, "unsupported field: "+key, nil))
					return
				}
			}
			fields := map[string]any{}
			if rawValue, exists := raw["title"]; exists {
				v, ok := rawValue.(string)
				if !ok {
					apiErr(w, core.NewError(core.ErrInvalidRequest, "title must be a string", nil))
					return
				}
				fields["title"] = v
			}
			if rawValue, exists := raw["description"]; exists {
				v, ok := rawValue.(string)
				if !ok {
					apiErr(w, core.NewError(core.ErrInvalidRequest, "description must be a string", nil))
					return
				}
				fields["description"] = v
			}
			if rawPriority, exists := raw["priority"]; exists {
				v, ok := rawPriority.(json.Number)
				if !ok {
					apiErr(w, core.NewError(core.ErrInvalidRequest, "priority must be an integer", nil))
					return
				}
				parsed, err := strconv.ParseInt(v.String(), 10, 0)
				if err != nil {
					apiErr(w, core.NewError(core.ErrInvalidRequest, "priority must be an integer", nil))
					return
				}
				fields["priority"] = int(parsed)
			}
			if rawValue, exists := raw["acceptance_criteria"]; exists {
				arr, ok := rawValue.([]any)
				if !ok {
					apiErr(w, core.NewError(core.ErrInvalidRequest, "acceptance_criteria must be an array", nil))
					return
				}
				values, err := toStrings("acceptance_criteria", arr, false)
				if err != nil {
					apiErr(w, err)
					return
				}
				fields["acceptance_criteria"] = values
			}
			if rawValue, exists := raw["labels"]; exists {
				arr, ok := rawValue.([]any)
				if !ok {
					apiErr(w, core.NewError(core.ErrInvalidRequest, "labels must be an array", nil))
					return
				}
				values, err := toStrings("labels", arr, true)
				if err != nil {
					apiErr(w, err)
					return
				}
				fields["labels"] = values
			}
			iss, err := s.Store.UpdateIssue(ref, fields)
			if err != nil {
				apiErr(w, err)
				return
			}
			ok(w, iss)
			return
		}
	}
	if len(parts) >= 2 {
		switch parts[1] {
		case "transition":
			if len(parts) == 2 && r.Method == "POST" {
				var in struct {
					State       string `json:"state"`
					Reason      string `json:"reason"`
					DuplicateOf string `json:"duplicate_of"`
				}
				allowedFields := map[string]bool{"state": true, "reason": true, "duplicate_of": true}
				if readBodyDisallowUnknownFields(r, &in, w, allowedFields) {
					return
				}
				if in.DuplicateOf != "" && core.IssueState(in.State) != core.StateDuplicate {
					apiErr(w, core.NewError(core.ErrInvalidRequest, "duplicate_of is only valid when state is Duplicate", nil))
					return
				}
				iss, err := s.Store.TransitionIssue(ref, core.IssueState(in.State), in.Reason, in.DuplicateOf)
				if err != nil {
					apiErr(w, err)
					return
				}
				ok(w, iss)
				return
			}
		case "comments":
			if len(parts) == 2 && r.Method == "POST" {
				var in struct {
					Body string `json:"body"`
				}
				if readBodyDisallowUnknownFields(r, &in, w, map[string]bool{"body": true}) {
					return
				}
				err := s.Store.AddComment(ref, "operator", in.Body, nil)
				if err != nil {
					apiErr(w, err)
					return
				}
				iss, err := s.Store.GetIssue(ref)
				if err != nil {
					apiErr(w, err)
					return
				}
				ok(w, iss)
				return
			}
		case "blockers":
			if len(parts) == 2 && r.Method == "POST" {
				var in struct {
					BlockedBy string `json:"blocked_by"`
				}
				if readBodyDisallowUnknownFields(r, &in, w, map[string]bool{"blocked_by": true}) {
					return
				}
				iss, err := s.Store.AddBlocker(ref, in.BlockedBy)
				if err != nil {
					apiErr(w, err)
					return
				}
				ok(w, iss)
				return
			}
			if len(parts) == 3 && r.Method == "DELETE" {
				iss, err := s.Store.RemoveBlocker(ref, parts[2])
				if err != nil {
					apiErr(w, err)
					return
				}
				ok(w, iss)
				return
			}
		case "duplicates":
			if len(parts) == 3 && r.Method == "DELETE" {
				iss, err := s.Store.RemoveDuplicate(ref, parts[2])
				if err != nil {
					apiErr(w, err)
					return
				}
				ok(w, iss)
				return
			}
		case "dispatch":
			if len(parts) == 2 && r.Method == "POST" {
				res, err := s.Orch.DispatchIssue(ref, "manual")
				if err != nil {
					apiErr(w, err)
					return
				}
				ok(w, res)
				return
			}
		case "dispatch-pause":
			if len(parts) == 2 && r.Method == "POST" {
				var in struct {
					Reason string `json:"reason"`
				}
				if readBodyDisallowUnknownFields(r, &in, w, map[string]bool{"reason": true}) {
					return
				}
				iss, err := s.Store.DispatchPause(ref, in.Reason)
				if err != nil {
					apiErr(w, err)
					return
				}
				ok(w, iss)
				return
			}
		case "dispatch-resume":
			if len(parts) == 2 && r.Method == "POST" {
				var in struct {
					Reason string `json:"reason"`
				}
				if readBodyDisallowUnknownFields(r, &in, w, map[string]bool{"reason": true}) {
					return
				}
				iss, err := s.Store.DispatchResume(ref, in.Reason)
				if err != nil {
					apiErr(w, err)
					return
				}
				ok(w, iss)
				return
			}
		case "events":
			if len(parts) == 3 && parts[2] == "stream" {
				if r.Method != http.MethodGet {
					http.Error(w, "method", http.StatusMethodNotAllowed)
					return
				}
				iss, err := s.Store.GetIssue(ref)
				if err != nil {
					apiErr(w, err)
					return
				}
				s.sse(w, r, "", iss.ID)
				return
			}
		}
	}
	apiErr(w, core.NewError(core.ErrNotFound, "issue route not found", nil))
}

func (s *Server) runRoutes(w http.ResponseWriter, r *http.Request, rest string) {
	parts := strings.Split(rest, "/")
	id := parts[0]
	if len(parts) == 1 && r.Method == "GET" {
		run, err := s.Store.GetRun(id)
		if err != nil {
			apiErr(w, err)
			return
		}
		ok(w, run)
		return
	}
	if len(parts) >= 2 {
		switch parts[1] {
		case "events":
			if len(parts) == 2 && r.Method == "GET" {
				s.events(w, r, id)
				return
			}
			if len(parts) == 3 && parts[2] == "stream" {
				if r.Method != http.MethodGet {
					http.Error(w, "method", http.StatusMethodNotAllowed)
					return
				}
				if _, err := s.Store.GetRun(id); err != nil {
					apiErr(w, err)
					return
				}
				s.sse(w, r, id, "")
				return
			}
		case "cancel":
			if len(parts) == 2 && r.Method == "POST" {
				var in struct {
					Reason string `json:"reason"`
				}
				if readOptionalBodyDisallowUnknownFields(r, &in, w, map[string]bool{"reason": true}) {
					return
				}
				if err := s.Store.CancelRun(id, in.Reason); err != nil {
					apiErr(w, err)
					return
				}
				run, err := s.Store.GetRun(id)
				if err != nil {
					apiErr(w, err)
					return
				}
				ok(w, run)
				return
			}
		}
	}
	apiErr(w, core.NewError(core.ErrNotFound, "run route not found", nil))
}
func (s *Server) events(w http.ResponseWriter, r *http.Request, runID string) {
	after, err := parseAfterSeq(r.URL.Query().Get("after_seq"))
	if err != nil {
		apiErr(w, err)
		return
	}
	ev, err := s.Store.RunEvents(runID, after, 200)
	if err != nil {
		apiErr(w, err)
		return
	}
	ok(w, ev)
}
func (s *Server) sse(w http.ResponseWriter, r *http.Request, runID, issueID string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		apiErr(w, core.NewError(core.ErrInternal, "streaming is not supported", nil))
		return
	}
	after, _ := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64)
	if qAfter, err := strconv.ParseInt(r.URL.Query().Get("after_seq"), 10, 64); err == nil && qAfter > after {
		after = qAfter
	}
	writeEvents := func() error {
		var ev []core.RunEvent
		var err error
		if issueID != "" {
			ev, err = s.Store.IssueEvents(issueID, after, 200)
		} else {
			ev, err = s.Store.RunEvents(runID, after, 200)
		}
		if err != nil {
			return err
		}
		for _, e := range ev {
			b, _ := json.Marshal(e)
			fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.Seq, e.EventType, b)
			fmt.Fprintf(w, "id: %d\ndata: %s\n\n", e.Seq, b)
			after = e.Seq
		}
		flusher.Flush()
		return nil
	}
	writeSSEError := func(err error) {
		ae := core.AsAPIError(err)
		b, _ := json.Marshal(map[string]any{"code": ae.Code, "message": ae.Message})
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", b)
		flusher.Flush()
	}
	if err := writeEvents(); err != nil {
		apiErr(w, err)
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if err := writeEvents(); err != nil {
				writeSSEError(err)
				return
			}
		case <-heartbeat.C:
			_, _ = io.WriteString(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}
func (s *Server) approvalRoutes(w http.ResponseWriter, r *http.Request, rest string) {
	parts := strings.Split(rest, "/")
	if len(parts) == 2 && parts[1] == "decide" && r.Method == "POST" {
		var in struct {
			Decision string `json:"decision"`
			Reason   string `json:"reason"`
		}
		if readBodyDisallowUnknownFields(r, &in, w, map[string]bool{"decision": true, "reason": true}) {
			return
		}
		status := map[string]string{"approve_once": "approved_once", "approve_for_run": "approved_for_run", "approve_for_session": "approved_for_session", "deny": "denied", "cancel_run": "cancelled"}[in.Decision]
		if status == "" {
			apiErr(w, core.NewError(core.ErrInvalidRequest, "unsupported approval decision", map[string]any{"decision": in.Decision}))
			return
		}
		if err := s.Store.DecideApproval(parts[0], status, in.Reason); err != nil {
			apiErr(w, err)
			return
		}
		ok(w, map[string]any{"id": parts[0], "status": status})
		return
	}
	apiErr(w, core.NewError(core.ErrNotFound, "approval route not found", nil))
}
func (s *Server) reviewRoutes(w http.ResponseWriter, r *http.Request, rest string) {
	parts := strings.Split(rest, "/")
	ref := parts[0]
	if len(parts) == 1 && r.Method == "GET" {
		row, err := s.Store.ReviewPacketRow(ref)
		if err != nil {
			apiErr(w, err)
			return
		}
		arts, err := s.Store.ArtifactsForReview(row["id"].String())
		if err != nil {
			apiErr(w, err)
			return
		}
		files := []map[string]any{}
		for _, a := range arts {
			cu := any(nil)
			if a.Kind != "prompt_snapshot" {
				cu = "/api/v1/artifacts/" + a.ID + "/content"
			}
			files = append(files, map[string]any{"kind": a.Kind, "artifact_id": a.ID, "path": a.Path, "redacted": a.Redacted, "content_url": cu})
		}
		ok(w, map[string]any{
			"id":              row["id"].String(),
			"issue_id":        row["issue_id"].String(),
			"run_id":          row["run_id"].String(),
			"packet_no":       row["packet_no"].Int(),
			"status":          row["status"].String(),
			"root_path":       row["root_path"].String(),
			"artifacts":       files,
			"files":           files,
			"created_at":      row["created_at"].String(),
			"failure_code":    nil,
			"failure_message": nil,
		})
		return
	}
	if len(parts) == 2 && r.Method == "POST" {
		var in struct {
			Reason string `json:"reason"`
		}
		if readBodyDisallowUnknownFields(r, &in, w, map[string]bool{"reason": true}) {
			return
		}
		if parts[1] == "send-to-rework" {
			iss, err := s.Store.SendToRework(ref, in.Reason)
			if err != nil {
				apiErr(w, err)
				return
			}
			ok(w, iss)
			return
		}
		if parts[1] == "mark-done" {
			iss, err := s.Store.MarkDone(ref, in.Reason)
			if err != nil {
				apiErr(w, err)
				return
			}
			ok(w, iss)
			return
		}
	}
	apiErr(w, core.NewError(core.ErrNotFound, "review route not found", nil))
}
func (s *Server) artifactRoutes(w http.ResponseWriter, r *http.Request, rest string) {
	parts := strings.Split(rest, "/")
	if r.Method != http.MethodGet {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	art, err := s.Store.GetArtifact(parts[0])
	if err != nil {
		apiErr(w, err)
		return
	}
	if len(parts) == 1 {
		ok(w, art)
		return
	}
	if len(parts) == 2 && parts[1] == "content" {
		if art.Kind == "codex_log" || art.Kind == "prompt_snapshot" {
			apiErrWithStatus(w, http.StatusForbidden, core.NewError(core.ErrRawLogAccessUnsupported, "raw prompt/log access is not supported", nil))
			return
		}
		root1 := filepath.Join(s.Store.RepoRoot, ".symphony", "artifacts")
		root2 := filepath.Join(s.Store.RepoRoot, ".symphony", "exports")
		safePath, ok := safeContainedFilePathAllowMissing(art.Path, root1)
		if !ok {
			safePath, ok = safeContainedFilePathAllowMissing(art.Path, root2)
		}
		if !ok {
			apiErr(w, core.NewError(core.ErrForbidden, "artifact path is outside allowed roots", nil))
			return
		}
		http.ServeFile(w, r, safePath)
		return
	}
	apiErr(w, core.NewError(core.ErrNotFound, "artifact route not found", nil))
}

func (s *Server) workflowValidate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		DryRun *bool `json:"dry_run"`
	}
	if readOptionalBodyDisallowUnknownFields(r, &in, w, map[string]bool{"dry_run": true}) {
		return
	}
	if in.DryRun != nil && !*in.DryRun {
		apiErr(w, core.NewError(core.ErrInvalidRequest, "dry_run must be true", nil))
		return
	}
	wf, _ := config.Load(s.Store.RepoRoot)
	ok(w, map[string]any{"source": "current_filesystem", "workflow_path": wf.Path, "validation": wf.Validation, "side_effects": map[string]any{"effective_config_replaced": false, "last_valid_config_updated": false, "prompt_rendered": false, "run_dispatched": false, "review_artifacts_written": false}})
}
func (s *Server) workflowPreview(w http.ResponseWriter, r *http.Request) {
	if err := requireEmptyObjectBody(r); err != nil {
		apiErr(w, err)
		return
	}
	wf, _ := config.Load(s.Store.RepoRoot)
	if !wf.Validation.Valid {
		apiErr(w, core.NewError(core.ErrWorkflowInvalid, "no effective workflow", nil))
		return
	}
	prompt, err := config.RenderPrompt(wf, map[string]any{"issue": map[string]any{"identifier": "LOC-0", "title": "Preview"}, "run": map[string]any{"id": "run_preview"}, "workspace": map[string]any{"path": "redacted"}})
	if err != nil {
		apiErr(w, core.NewError(core.ErrPromptRenderFailed, err.Error(), nil))
		return
	}
	sum := sha256.Sum256([]byte(prompt))
	ok(w, map[string]any{
		"source":                  "effective",
		"rendered_prompt_preview": "[redacted: prompt body is not exposed by the API]",
		"prompt_metadata": map[string]any{
			"rendered_prompt_sha256": hex.EncodeToString(sum[:]),
			"rendered_prompt_bytes":  len([]byte(prompt)),
		},
		"validation":          wf.Validation,
		"redactions_applied":  []string{"prompt_body"},
		"raw_prompt_exposed":  false,
		"raw_codex_log_shown": false,
	})
}

func diagnosticsExportOptions(r *http.Request) (bool, error) {
	var in struct {
		IncludeRawLogs *bool `json:"include_raw_logs"`
	}
	if err := readOptionalStrictJSONObjectBody(r, &in, map[string]bool{"include_raw_logs": true}); err != nil {
		return false, err
	}
	if in.IncludeRawLogs != nil {
		return *in.IncludeRawLogs, nil
	}
	return false, nil
}

func requireEmptyObjectBody(r *http.Request) error {
	var in map[string]json.RawMessage
	if err := readOptionalStrictJSONObjectBody(r, &in, nil); err != nil {
		return err
	}
	if in != nil && len(in) > 0 {
		return core.NewError(core.ErrInvalidRequest, "request body must be an empty object", nil)
	}
	return nil
}

func readBodyDisallowUnknownFields(r *http.Request, v any, w http.ResponseWriter, allowedFields map[string]bool) bool {
	if err := readRequiredStrictJSONObjectBody(r, v, allowedFields); err != nil {
		apiErr(w, err)
		return true
	}
	return false
}

func readOptionalBodyDisallowUnknownFields(r *http.Request, v any, w http.ResponseWriter, allowedFields map[string]bool) bool {
	if err := readOptionalStrictJSONObjectBody(r, v, allowedFields); err != nil {
		apiErr(w, err)
		return true
	}
	return false
}

func readRequiredStrictJSONObjectBody(r *http.Request, v any, allowedFields map[string]bool) error {
	rawBody, err := decodeSingleJSONObjectBody(json.NewDecoder(r.Body))
	if err != nil {
		return err
	}
	return decodeStrictJSONObject(rawBody, v, allowedFields)
}

func readOptionalStrictJSONObjectBody(r *http.Request, v any, allowedFields map[string]bool) error {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return core.NewError(core.ErrInvalidRequest, "read request body failed", nil)
	}
	if strings.TrimSpace(string(body)) == "" {
		return nil
	}
	rawBody, err := decodeSingleJSONObjectBody(json.NewDecoder(bytes.NewReader(body)))
	if err != nil {
		return err
	}
	return decodeStrictJSONObject(rawBody, v, allowedFields)
}

func decodeStrictJSONObject(rawBody json.RawMessage, v any, allowedFields map[string]bool) error {
	if allowedFields != nil {
		var rawFields map[string]json.RawMessage
		if err := json.Unmarshal(rawBody, &rawFields); err != nil {
			return core.NewError(core.ErrInvalidRequest, err.Error(), nil)
		}
		for key, raw := range rawFields {
			if !allowedFields[key] {
				return core.NewError(core.ErrInvalidRequest, "unsupported field: "+key, nil)
			}
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				return core.NewError(core.ErrInvalidRequest, key+" must not be null", nil)
			}
		}
	}
	dec := json.NewDecoder(bytes.NewReader(rawBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return core.NewError(core.ErrInvalidRequest, err.Error(), nil)
	}
	return nil
}

func decodeSingleJSONObjectBody(dec *json.Decoder) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		if err == io.EOF {
			return nil, core.NewError(core.ErrInvalidRequest, "request body must be a JSON object", nil)
		}
		return nil, core.NewError(core.ErrInvalidRequest, err.Error(), nil)
	}
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, core.NewError(core.ErrInvalidRequest, "request body must be a JSON object", nil)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, core.NewError(core.ErrInvalidRequest, "request body must contain a single JSON object", nil)
	}
	return raw, nil
}

func ok(w http.ResponseWriter, data any) {
	writeJSON(w, 200, core.SuccessEnvelope{Data: data, Meta: map[string]any{"request_id": core.NewID("req_"), "server_time": core.Now()}})
}
func created(w http.ResponseWriter, data any) {
	writeJSON(w, 201, core.SuccessEnvelope{Data: data, Meta: map[string]any{"request_id": core.NewID("req_"), "server_time": core.Now()}})
}
func apiErr(w http.ResponseWriter, err error) {
	ae := core.AsAPIError(err)
	status := 400
	if ae.Code == core.ErrNotFound {
		status = 404
	}
	if ae.Code == core.ErrUnauthorized {
		status = 401
	}
	if ae.Code == core.ErrForbidden || ae.Code == core.ErrCSRFRequired {
		status = 403
	}
	if ae.Code == core.ErrInvalidStateTransition ||
		ae.Code == core.ErrApprovalNotPending ||
		ae.Code == core.ErrIssueAlreadyRunning ||
		ae.Code == core.ErrIssueDispatchPaused ||
		ae.Code == core.ErrIssueBlocked ||
		ae.Code == core.ErrConcurrencyLimitReached ||
		ae.Code == core.ErrReviewPacketRequired {
		status = 409
	}
	if ae.Code == core.ErrInternal {
		status = 500
	}
	apiErrWithStatus(w, status, err)
}
func apiErrWithStatus(w http.ResponseWriter, status int, err error) {
	ae := core.AsAPIError(err)
	writeJSON(w, status, core.ErrorEnvelope{Error: map[string]any{"code": ae.Code, "message": ae.Message, "details": ae.Details, "request_id": core.NewID("req_")}})
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func toStrings(field string, a []any, rejectBlank bool) ([]string, error) {
	out := []string{}
	for _, x := range a {
		s, ok := x.(string)
		if !ok {
			return nil, core.NewError(core.ErrInvalidRequest, field+" entries must be strings", nil)
		}
		if rejectBlank && strings.TrimSpace(s) == "" {
			return nil, core.NewError(core.ErrInvalidRequest, field+" entries must be non-empty strings", nil)
		}
		out = append(out, s)
	}
	return out, nil
}
func repeatedCSV(values []string) []string {
	out := []string{}
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func parseIssueLimit(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return 50, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > 200 {
		return 0, core.NewError(core.ErrInvalidRequest, "limit must be an integer from 1 to 200", nil)
	}
	return limit, nil
}

func parseOptionalBool(raw, name string) (*bool, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true":
		v := true
		return &v, nil
	case "false":
		v := false
		return &v, nil
	default:
		return nil, core.NewError(core.ErrInvalidRequest, name+" must be true or false", nil)
	}
}

func validIssueState(raw string) bool {
	switch core.IssueState(raw) {
	case core.StateInbox, core.StateReady, core.StateWorking, core.StateRework, core.StateBlocked, core.StateHumanReview, core.StateDone, core.StateCancelled, core.StateDuplicate:
		return true
	default:
		return false
	}
}

func parseAfterSeq(raw string) (int64, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	after, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || after < 0 {
		return 0, core.NewError(core.ErrInvalidRequest, "after_seq must be a non-negative integer", nil)
	}
	return after, nil
}

func under(p, root string) bool {
	ap, _ := filepath.Abs(p)
	ar, _ := filepath.Abs(root)
	rel, err := filepath.Rel(ar, ap)
	return err == nil && (rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)))
}

func safeContainedFilePath(p, root string) (string, bool) {
	safePath, ok := safeContainedFilePathAllowMissing(p, root)
	if !ok {
		return "", false
	}
	if _, err := os.Stat(safePath); err != nil {
		return "", false
	}
	return safePath, true
}

func safeContainedFilePathAllowMissing(p, root string) (string, bool) {
	if !under(p, root) {
		return "", false
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}
	resolvedPath, err := filepath.EvalSymlinks(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return p, true
		}
		return "", false
	}
	if !under(resolvedPath, resolvedRoot) {
		return "", false
	}
	return resolvedPath, true
}

type dashboardCandidate struct {
	path     string
	explicit bool
}

func dashboardDist(repoRoot string) (string, string, bool) {
	candidates := []dashboardCandidate{}
	if configured := strings.TrimSpace(os.Getenv("SYMPHONY_DASHBOARD_DIST")); configured != "" {
		if abs, err := filepath.Abs(configured); err == nil {
			candidates = append(candidates, dashboardCandidate{path: abs, explicit: true})
		}
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates, dashboardCandidate{path: filepath.Join(exeDir, "web", "dist")})
		if filepath.Base(exeDir) != "bin" {
			candidates = append(candidates, dashboardCandidate{path: filepath.Join(exeDir, "..", "web", "dist")})
		}
		candidates = append(candidates,
			dashboardCandidate{path: filepath.Join(exeDir, "..", "share", "local-symphony", "web", "dist")},
		)
	}
	for _, candidate := range candidates {
		distRoot := filepath.Clean(candidate.path)
		if !candidate.explicit && repoRoot != "" && under(distRoot, repoRoot) {
			continue
		}
		indexPath := filepath.Join(distRoot, "index.html")
		if safeIndexPath, ok := safeContainedFilePath(indexPath, distRoot); ok {
			if st, err := os.Stat(safeIndexPath); err == nil && !st.IsDir() {
				return distRoot, safeIndexPath, true
			}
		}
	}
	return "", "", false
}

func requiresCSRF(path, method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return path != "/auth/exchange" && path != "/auth/open-token" && path != "/auth/cli-token/rotate"
}

func isPublicAPI(path, method string) bool {
	return (method == http.MethodGet && (path == "/health" || path == "/auth/session")) ||
		(method == http.MethodPost && path == "/auth/exchange")
}

func (s *Server) authorizeAPI(w http.ResponseWriter, r *http.Request) bool {
	if s.validBearerSession(r) || s.validSession(r) {
		return true
	}
	apiErr(w, core.NewError(core.ErrUnauthorized, "session required", nil))
	return false
}

func (s *Server) authorizeCommand(w http.ResponseWriter, r *http.Request) bool {
	if s.validBearerSession(r) {
		return true
	}
	if !s.validSession(r) {
		apiErr(w, core.NewError(core.ErrUnauthorized, "browser session invalid", nil))
		return false
	}
	if r.Header.Get("X-Symphony-CSRF") != s.csrfToken {
		apiErr(w, core.NewError(core.ErrCSRFRequired, "CSRF token required", nil))
		return false
	}
	return true
}

func (s *Server) authExchange(w http.ResponseWriter, r *http.Request) {
	var in struct {
		OpenToken string `json:"open_token"`
	}
	if readBodyDisallowUnknownFields(r, &in, w, map[string]bool{"open_token": true}) {
		return
	}
	if strings.TrimSpace(in.OpenToken) == "" {
		apiErr(w, core.NewError(core.ErrInvalidRequest, "open_token is required", nil))
		return
	}
	sessionToken, expiresAt, err := s.exchangeOpenToken(in.OpenToken)
	if err != nil {
		apiErr(w, err)
		return
	}
	s.setSessionCookie(w, r, sessionToken, expiresAt)
	ok(w, map[string]any{"session": "created", "authenticated": true, "csrf_token": s.csrfToken, "csrf": s.csrfToken, "expires_at": expiresAt})
}

func (s *Server) exchangeOpenToken(openToken string) (string, string, error) {
	now := core.Now()
	hash := security.HashToken(openToken)
	sessionToken := security.NewToken()
	expiresAt := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339Nano)
	if err := s.Store.App.WithTx(func(tx *db.Tx) error {
		row, err := tx.QueryOne(`SELECT id,expires_at FROM open_tokens WHERE project_id=? AND token_hash=? AND consumed_at IS NULL`, s.Store.ProjectID, hash)
		if errors.Is(err, os.ErrNotExist) {
			return core.NewError(core.ErrUnauthorized, "open token invalid", nil)
		}
		if err != nil {
			return err
		}
		if expiredAtOrBefore(row["expires_at"].String(), now) {
			return core.NewError(core.ErrUnauthorized, "open token expired", nil)
		}
		if err := tx.Exec(`INSERT INTO local_sessions(id,project_id,kind,token_hash,csrf_hash,user_label,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?)`, core.NewID("ses_"), s.Store.ProjectID, "browser", security.HashToken(sessionToken), security.HashToken(s.csrfToken), "local-dashboard", now, expiresAt); err != nil {
			return err
		}
		return tx.Exec(`UPDATE open_tokens SET consumed_at=? WHERE id=?`, now, row["id"].String())
	}); err != nil {
		return "", "", err
	}
	return sessionToken, expiresAt, nil
}

func (s *Server) openToken(w http.ResponseWriter, r *http.Request) {
	if !s.validBearerSession(r) {
		apiErr(w, core.NewError(core.ErrUnauthorized, "bearer token invalid", nil))
		return
	}
	token := security.NewToken()
	expiresAt := time.Now().UTC().Add(5 * time.Minute).Format(time.RFC3339Nano)
	if err := s.Store.App.Exec(`INSERT INTO open_tokens(id,project_id,token_hash,expires_at,created_at) VALUES(?,?,?,?,?)`, core.NewID("open_"), s.Store.ProjectID, security.HashToken(token), expiresAt, core.Now()); err != nil {
		apiErr(w, err)
		return
	}
	ok(w, map[string]any{"open_token": token, "expires_at": expiresAt})
}

func (s *Server) rotateCLIToken(w http.ResponseWriter, r *http.Request) {
	token := bearerToken(r)
	if token == "" {
		apiErr(w, core.NewError(core.ErrUnauthorized, "bearer token invalid", nil))
		return
	}
	replacement, expiresAt, err := s.rotateBearerSession(token)
	if err != nil {
		apiErr(w, err)
		return
	}
	ok(w, map[string]any{"token": replacement, "expires_at": expiresAt})
}

func (s *Server) rotateBearerSession(token string) (string, *string, error) {
	now := core.Now()
	tokenHash := security.HashToken(token)
	var expiresAt *string
	replacement := security.NewToken()
	if err := s.Store.App.WithTx(func(tx *db.Tx) error {
		row, err := tx.QueryOne(`SELECT id,kind,expires_at,revoked_at,user_label FROM local_sessions WHERE project_id=? AND token_hash=? AND kind IN ('cli','desktop')`, s.Store.ProjectID, tokenHash)
		if errors.Is(err, os.ErrNotExist) {
			return core.NewError(core.ErrUnauthorized, "bearer token invalid", nil)
		}
		if err != nil {
			return err
		}
		if !row["revoked_at"].Null && row["revoked_at"].String() != "" {
			return core.NewError(core.ErrUnauthorized, "bearer token revoked", nil)
		}
		if !row["expires_at"].Null && row["expires_at"].String() != "" && expiredAtOrBefore(row["expires_at"].String(), now) {
			return core.NewError(core.ErrUnauthorized, "bearer token expired", nil)
		}
		if !row["expires_at"].Null && row["expires_at"].String() != "" {
			v := row["expires_at"].String()
			expiresAt = &v
		}
		label := row["user_label"].String()
		if label == "" {
			label = "local-cli"
		}
		if err := tx.Exec(`UPDATE local_sessions SET revoked_at=? WHERE id=?`, now, row["id"].String()); err != nil {
			return err
		}
		return tx.Exec(`INSERT INTO local_sessions(id,project_id,kind,token_hash,user_label,created_at,expires_at) VALUES(?,?,?,?,?,?,?)`, core.NewID("ses_"), s.Store.ProjectID, row["kind"].String(), security.HashToken(replacement), label, now, expiresAt)
	}); err != nil {
		return "", nil, err
	}
	return replacement, expiresAt, nil
}

func (s *Server) authLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		_ = s.Store.App.Exec(`UPDATE local_sessions SET revoked_at=? WHERE project_id=? AND token_hash=? AND kind='browser'`, core.Now(), s.Store.ProjectID, security.HashToken(cookie.Value))
	}
	s.clearSessionCookie(w)
	ok(w, map[string]any{"logged_out": true})
}

func (s *Server) validBearerSession(r *http.Request) bool {
	token := bearerToken(r)
	if token == "" {
		return false
	}
	return s.validStoredSession(security.HashToken(token), "cli", "desktop")
}

func bearerToken(r *http.Request) string {
	return bearerTokenFromHeader(r.Header.Get("Authorization"))
}

func bearerTokenFromHeader(header string) string {
	if len(header) <= len("Bearer ") || header[len("Bearer")] != ' ' {
		return ""
	}
	if !strings.EqualFold(header[:len("Bearer")], "Bearer") {
		return ""
	}
	token := header[len("Bearer "):]
	if token == "" {
		return ""
	}
	return token
}

func (s *Server) validSession(r *http.Request) bool {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	return s.validStoredSession(security.HashToken(cookie.Value), "browser")
}

func (s *Server) validStoredSession(tokenHash string, kinds ...string) bool {
	args := []any{s.Store.ProjectID, tokenHash}
	placeholders := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		placeholders = append(placeholders, "?")
		args = append(args, kind)
	}
	row, err := s.Store.App.QueryOne(`SELECT id,kind,expires_at,revoked_at FROM local_sessions WHERE project_id=? AND token_hash=? AND kind IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return false
	}
	if !row["revoked_at"].Null && row["revoked_at"].String() != "" {
		return false
	}
	expiresAt := row["expires_at"]
	if row["kind"].String() == "browser" && (expiresAt.Null || expiresAt.String() == "") {
		return false
	}
	if !expiresAt.Null && expiresAt.String() != "" {
		if expiredAtOrBefore(expiresAt.String(), core.Now()) {
			return false
		}
	}
	_ = s.Store.App.Exec(`UPDATE local_sessions SET last_seen_at=? WHERE id=?`, core.Now(), row["id"].String())
	return true
}

func expiredAtOrBefore(expiresAt, now string) bool {
	expires, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil {
		return true
	}
	current, err := time.Parse(time.RFC3339Nano, now)
	if err != nil {
		return true
	}
	return !expires.After(current)
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token, expiresAt string) {
	expires, _ := time.Parse(time.RFC3339Nano, expiresAt)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}
