package httpapi

import (
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
		if under(filePath, distRoot) {
			if st, err := os.Stat(filePath); err == nil && !st.IsDir() {
				http.ServeFile(w, r, filePath)
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
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	var req toolgateway.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, toolgateway.Response{Success: false, Error: &toolgateway.ToolError{Code: "invalid_request", Message: err.Error()}})
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
		ok(w, map[string]any{"rotated": true})
	case path == "/issues" && r.Method == "GET":
		s.listIssues(w, r)
	case path == "/issues" && r.Method == "POST":
		s.createIssue(w, r)
	case strings.HasPrefix(path, "/issues/"):
		s.issueRoutes(w, r, strings.TrimPrefix(path, "/issues/"))
	case path == "/runs" && r.Method == "GET":
		runs, _ := s.Store.ListRuns()
		ok(w, runs)
	case strings.HasPrefix(path, "/runs/"):
		s.runRoutes(w, r, strings.TrimPrefix(path, "/runs/"))
	case path == "/approvals" && r.Method == "GET":
		a, _ := s.Store.PendingApprovals()
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
		wf, _ := config.Load(s.Store.RepoRoot)
		ok(w, map[string]any{"reloaded": wf.Validation.Valid, "validation": wf.Validation})
	case path == "/diagnostics" && r.Method == "GET":
		ok(w, observability.Diagnostics(s.Store))
	case path == "/diagnostics/export" && r.Method == "POST":
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
	issues, _ := s.Store.ListIssues(store.ListIssueOptions{Limit: 20})
	runs, _ := s.Store.ListRuns()
	ok(w, map[string]any{"project_id": s.Store.ProjectID, "repo_root": s.Store.RepoRoot, "issues": issues, "runs": runs})
}
func (s *Server) listIssues(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	opts := store.ListIssueOptions{Limit: store.ParseInt(q.Get("limit"), 50), Sort: q.Get("sort"), Query: q.Get("q")}
	if st := q.Get("state"); st != "" {
		opts.States = strings.Split(st, ",")
	}
	opts.DispatchPaused = store.ParseBool(q.Get("dispatch_paused"))
	items, err := s.Store.ListIssues(opts)
	if err != nil {
		apiErr(w, err)
		return
	}
	ok(w, map[string]any{"items": items, "page": map[string]any{"limit": opts.Limit, "next_cursor": nil, "has_more": false}})
}
func (s *Server) createIssue(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title              string   `json:"title"`
		Description        string   `json:"description"`
		AcceptanceCriteria []string `json:"acceptance_criteria"`
		Priority           int      `json:"priority"`
		Labels             []string `json:"labels"`
	}
	if readBody(r, &in, w) {
		return
	}
	iss, err := s.Store.CreateIssue(store.CreateIssueInput{Title: in.Title, Description: in.Description, AcceptanceCriteria: in.AcceptanceCriteria, Priority: in.Priority, Labels: in.Labels})
	if err != nil {
		apiErr(w, err)
		return
	}
	ok(w, iss)
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
			if readBody(r, &raw, w) {
				return
			}
			fields := map[string]any{}
			if v, ok := raw["title"].(string); ok {
				fields["title"] = v
			}
			if v, ok := raw["description"].(string); ok {
				fields["description"] = v
			}
			if v, ok := raw["priority"].(float64); ok {
				fields["priority"] = int(v)
			}
			if arr, ok := raw["acceptance_criteria"].([]any); ok {
				fields["acceptance_criteria"] = toStrings(arr)
			}
			if arr, ok := raw["labels"].([]any); ok {
				fields["labels"] = toStrings(arr)
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
			if r.Method == "POST" {
				var in struct {
					State       string `json:"state"`
					Reason      string `json:"reason"`
					DuplicateOf string `json:"duplicate_of"`
				}
				if readBody(r, &in, w) {
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
			if r.Method == "POST" {
				var in struct {
					Body string `json:"body"`
				}
				if readBody(r, &in, w) {
					return
				}
				err := s.Store.AddComment(ref, "operator", in.Body, nil)
				if err != nil {
					apiErr(w, err)
					return
				}
				iss, _ := s.Store.GetIssue(ref)
				ok(w, iss)
				return
			}
		case "blockers":
			if len(parts) == 2 && r.Method == "POST" {
				var in struct {
					BlockedBy string `json:"blocked_by"`
				}
				if readBody(r, &in, w) {
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
			if r.Method == "POST" {
				res, err := s.Orch.DispatchIssue(ref, "manual")
				if err != nil {
					apiErr(w, err)
					return
				}
				ok(w, res)
				return
			}
		case "dispatch-pause":
			if r.Method == "POST" {
				var in struct {
					Reason string `json:"reason"`
				}
				if readBody(r, &in, w) {
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
			if r.Method == "POST" {
				var in struct {
					Reason string `json:"reason"`
				}
				if readBody(r, &in, w) {
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
				if _, err := s.Store.GetRun(id); err != nil {
					apiErr(w, err)
					return
				}
				s.sse(w, r, id, "")
				return
			}
		case "cancel":
			if r.Method == "POST" {
				var in struct {
					Reason string `json:"reason"`
				}
				if readBody(r, &in, w) {
					return
				}
				if err := s.Store.CancelRun(id, in.Reason); err != nil {
					apiErr(w, err)
					return
				}
				run, _ := s.Store.GetRun(id)
				ok(w, run)
				return
			}
		}
	}
	apiErr(w, core.NewError(core.ErrNotFound, "run route not found", nil))
}
func (s *Server) events(w http.ResponseWriter, r *http.Request, runID string) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after_seq"), 10, 64)
	ev, _ := s.Store.RunEvents(runID, after, 200)
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
	writeEvents := func() {
		var ev []core.RunEvent
		if issueID != "" {
			ev, _ = s.Store.IssueEvents(issueID, after, 200)
		} else {
			ev, _ = s.Store.RunEvents(runID, after, 200)
		}
		for _, e := range ev {
			b, _ := json.Marshal(e)
			fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.Seq, e.EventType, b)
			fmt.Fprintf(w, "id: %d\ndata: %s\n\n", e.Seq, b)
			after = e.Seq
		}
		flusher.Flush()
	}
	writeEvents()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			writeEvents()
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
		if readBody(r, &in, w) {
			return
		}
		status := map[string]string{"approve_once": "approved_once", "approve_for_run": "approved_for_run", "approve_for_session": "approved_for_session", "deny": "denied", "cancel_run": "cancelled"}[in.Decision]
		if status == "" {
			status = in.Decision
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
		arts, _ := s.Store.ArtifactsForReview(row["id"].String())
		files := []map[string]any{}
		for _, a := range arts {
			cu := any(nil)
			if a.Kind != "prompt_snapshot" {
				cu = "/api/v1/artifacts/" + a.ID + "/content"
			}
			files = append(files, map[string]any{"kind": a.Kind, "artifact_id": a.ID, "path": a.Path, "redacted": a.Redacted, "content_url": cu})
		}
		ok(w, map[string]any{"id": row["id"].String(), "run_id": row["run_id"].String(), "packet_no": row["packet_no"].Int(), "status": row["status"].String(), "root_path": row["root_path"].String(), "files": files})
		return
	}
	if len(parts) == 2 && r.Method == "POST" {
		var in struct {
			Reason string `json:"reason"`
		}
		if readBody(r, &in, w) {
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
			apiErr(w, core.NewError(core.ErrRawLogAccessUnsupported, "raw prompt/log access is not supported", nil))
			return
		}
		root1 := filepath.Join(s.Store.RepoRoot, ".symphony", "artifacts")
		root2 := filepath.Join(s.Store.RepoRoot, ".symphony", "exports")
		if !under(art.Path, root1) && !under(art.Path, root2) {
			apiErr(w, core.NewError(core.ErrForbidden, "artifact path is outside allowed roots", nil))
			return
		}
		http.ServeFile(w, r, art.Path)
		return
	}
	apiErr(w, core.NewError(core.ErrNotFound, "artifact route not found", nil))
}

func (s *Server) workflowValidate(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	if len(strings.TrimSpace(string(b))) > 0 {
		var raw map[string]any
		if err := json.Unmarshal(b, &raw); err != nil {
			apiErr(w, core.NewError(core.ErrInvalidRequest, "malformed JSON", nil))
			return
		}
		for k, v := range raw {
			if k != "dry_run" {
				apiErr(w, core.NewError(core.ErrInvalidRequest, "unsupported field: "+k, nil))
				return
			}
			if bv, ok := v.(bool); !ok || !bv {
				apiErr(w, core.NewError(core.ErrInvalidRequest, "dry_run must be true", nil))
				return
			}
		}
	}
	wf, _ := config.Load(s.Store.RepoRoot)
	ok(w, map[string]any{"source": "current_filesystem", "workflow_path": wf.Path, "validation": wf.Validation, "side_effects": map[string]any{"effective_config_replaced": false, "last_valid_config_updated": false, "prompt_rendered": false, "run_dispatched": false, "review_artifacts_written": false}})
}
func (s *Server) workflowPreview(w http.ResponseWriter, r *http.Request) {
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

func readBody(r *http.Request, v any, w http.ResponseWriter) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil && err != io.EOF {
		apiErr(w, core.NewError(core.ErrInvalidRequest, err.Error(), nil))
		return true
	}
	return false
}
func ok(w http.ResponseWriter, data any) {
	writeJSON(w, 200, core.SuccessEnvelope{Data: data, Meta: map[string]any{"request_id": core.NewID("req_"), "server_time": core.Now()}})
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
	if ae.Code == core.ErrInvalidStateTransition || ae.Code == core.ErrApprovalNotPending {
		status = 409
	}
	if ae.Code == core.ErrInternal {
		status = 500
	}
	writeJSON(w, status, core.ErrorEnvelope{Error: map[string]any{"code": ae.Code, "message": ae.Message, "details": ae.Details, "request_id": core.NewID("req_")}})
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func toStrings(a []any) []string {
	out := []string{}
	for _, x := range a {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
func under(p, root string) bool {
	ap, _ := filepath.Abs(p)
	ar, _ := filepath.Abs(root)
	rel, err := filepath.Rel(ar, ap)
	return err == nil && (rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)))
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
		if st, err := os.Stat(indexPath); err == nil && !st.IsDir() {
			return distRoot, indexPath, true
		}
	}
	return "", "", false
}

func requiresCSRF(path, method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return path != "/auth/exchange" && path != "/auth/open-token"
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
	if readBody(r, &in, w) {
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
	if err := s.Store.App.Exec(`BEGIN IMMEDIATE`); err != nil {
		return "", "", err
	}
	committed := false
	defer func() {
		if !committed {
			_ = s.Store.App.Exec(`ROLLBACK`)
		}
	}()
	row, err := s.Store.App.QueryOne(`SELECT id,expires_at FROM open_tokens WHERE project_id=? AND token_hash=? AND consumed_at IS NULL`, s.Store.ProjectID, hash)
	if errors.Is(err, os.ErrNotExist) {
		return "", "", core.NewError(core.ErrUnauthorized, "open token invalid", nil)
	}
	if err != nil {
		return "", "", err
	}
	if row["expires_at"].String() < now {
		return "", "", core.NewError(core.ErrUnauthorized, "open token expired", nil)
	}
	sessionToken := security.NewToken()
	expiresAt := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339Nano)
	if err := s.Store.App.Exec(`INSERT INTO local_sessions(id,project_id,kind,token_hash,csrf_hash,user_label,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?)`, core.NewID("ses_"), s.Store.ProjectID, "browser", security.HashToken(sessionToken), security.HashToken(s.csrfToken), "local-dashboard", now, expiresAt); err != nil {
		return "", "", err
	}
	if err := s.Store.App.Exec(`UPDATE open_tokens SET consumed_at=? WHERE id=?`, now, row["id"].String()); err != nil {
		return "", "", err
	}
	if err := s.Store.App.Exec(`COMMIT`); err != nil {
		return "", "", err
	}
	committed = true
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

func (s *Server) authLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		_ = s.Store.App.Exec(`UPDATE local_sessions SET revoked_at=? WHERE project_id=? AND token_hash=? AND kind='browser'`, core.Now(), s.Store.ProjectID, security.HashToken(cookie.Value))
	}
	s.clearSessionCookie(w)
	ok(w, map[string]any{"logged_out": true})
}

func (s *Server) validBearerSession(r *http.Request) bool {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" || token == r.Header.Get("Authorization") {
		return false
	}
	return s.validStoredSession(security.HashToken(token), "cli", "desktop")
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
	row, err := s.Store.App.QueryOne(`SELECT id,expires_at,revoked_at FROM local_sessions WHERE project_id=? AND token_hash=? AND kind IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return false
	}
	if !row["revoked_at"].Null && row["revoked_at"].String() != "" {
		return false
	}
	if !row["expires_at"].Null && row["expires_at"].String() != "" && row["expires_at"].String() < core.Now() {
		return false
	}
	_ = s.Store.App.Exec(`UPDATE local_sessions SET last_seen_at=? WHERE id=?`, core.Now(), row["id"].String())
	return true
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
