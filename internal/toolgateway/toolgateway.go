package toolgateway

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"local-symphony/internal/core"
	"local-symphony/internal/security"
	"local-symphony/internal/store"
)

var Registry = []string{"issue.get", "issue.comment", "issue.block", "artifact.attach", "followup.create", "handoff.submit"}

type Gateway struct{ Store *store.Store }

type Request struct {
	Tool  string         `json:"tool"`
	Input map[string]any `json:"input"`
}

type Response struct {
	Success         bool       `json:"success"`
	Tool            string     `json:"tool,omitempty"`
	Data            any        `json:"data,omitempty"`
	Error           *ToolError `json:"error,omitempty"`
	IssueIdentifier string     `json:"issue_identifier,omitempty"`
	HandoffStatus   string     `json:"handoff_status,omitempty"`
	HandoffID       string     `json:"handoff_id,omitempty"`
}

type ToolError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func (g Gateway) Call(token, cwd string, req Request) Response {
	if req.Input == nil {
		req.Input = map[string]any{}
	}
	if !allowed(req.Tool) {
		return errResp("tool_gateway_failed", "tool is not registered", nil)
	}
	runID, issueID, err := g.Store.ValidateToolToken(security.HashToken(token))
	if err != nil {
		return apiErrResp(err)
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	issue, _ := g.Store.GetIssue(issueID)
	if issue == nil || issue.Workspace == nil {
		return errResp("tool_gateway_failed", "workspace not prepared", nil)
	}
	if rel, e := filepath.Rel(issue.Workspace.Path, cwd); e != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return errResp("tool_gateway_failed", "cwd is outside workspace", nil)
	}
	_ = g.Store.RecordToolCall(issueID, runID, req.Tool, "started", req.Input, nil, "", "")
	out, callErr := g.dispatch(issue, runID, req)
	if callErr != nil {
		ae := core.AsAPIError(callErr)
		_ = g.Store.RecordToolCall(issueID, runID, req.Tool, "failed", req.Input, nil, string(ae.Code), ae.Message)
		return errResp(string(ae.Code), ae.Message, ae.Details)
	}
	_ = g.Store.RecordToolCall(issueID, runID, req.Tool, "succeeded", req.Input, out, "", "")
	return out
}

func (g Gateway) dispatch(issue *core.Issue, runID string, req Request) (Response, error) {
	switch req.Tool {
	case "issue.get":
		return Response{Success: true, Tool: req.Tool, Data: issue}, nil
	case "issue.comment":
		body := strings.TrimSpace(fmt.Sprint(req.Input["body"]))
		if body == "" {
			return Response{}, core.NewError(core.ErrInvalidRequest, "body is required", nil)
		}
		if err := g.Store.AddComment(issue.ID, "agent", body, &runID); err != nil {
			return Response{}, err
		}
		return Response{Success: true, Tool: req.Tool, IssueIdentifier: issue.Identifier, Data: map[string]any{"comment_status": "created"}}, nil
	case "issue.block":
		reason := strings.TrimSpace(fmt.Sprint(req.Input["reason"]))
		if reason == "" {
			return Response{}, core.NewError(core.ErrInvalidRequest, "reason is required", nil)
		}
		_ = g.Store.AddComment(issue.ID, "agent", "Blocked by agent: "+reason, &runID)
		_, _ = g.Store.TransitionIssue(issue.ID, core.StateBlocked, reason, "")
		_ = g.Store.FailRun(runID, core.FailureAgentBlocked, reason, core.RunCancelled)
		return Response{Success: true, Tool: req.Tool, IssueIdentifier: issue.Identifier, Data: map[string]any{"blocked": true}}, nil
	case "artifact.attach":
		rel := strings.TrimSpace(fmt.Sprint(req.Input["path"]))
		kind := strings.TrimSpace(fmt.Sprint(req.Input["kind"]))
		if kind == "" {
			kind = "agent_file"
		}
		target, err := security.ContainedPath(issue.Workspace.Path, rel)
		if err != nil {
			return Response{}, core.NewError(core.ErrToolGatewayFailed, err.Error(), nil)
		}
		st, err := os.Stat(target)
		if err != nil || !st.Mode().IsRegular() {
			return Response{}, core.NewError(core.ErrInvalidRequest, "artifact path must exist and be a regular file", nil)
		}
		max := int64(10 * 1024 * 1024)
		if st.Size() > max {
			return Response{}, core.NewError(core.ErrInvalidRequest, "artifact exceeds max size", nil)
		}
		b, err := os.ReadFile(target)
		if err != nil {
			return Response{}, core.NewError(core.ErrToolGatewayFailed, "read artifact: "+err.Error(), nil)
		}
		sha := security.SHA256Bytes(b)
		artDir := filepath.Join(g.Store.RepoRoot, ".symphony", "artifacts", issue.Identifier, runID, "agent")
		if err := os.MkdirAll(artDir, 0o755); err != nil {
			return Response{}, core.NewError(core.ErrToolGatewayFailed, "create artifact directory: "+err.Error(), nil)
		}
		dst := filepath.Join(artDir, filepath.Base(rel))
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			return Response{}, core.NewError(core.ErrToolGatewayFailed, "write artifact: "+err.Error(), nil)
		}
		aid := core.NewID("art_")
		desc := fmt.Sprint(req.Input["description"])
		if err := g.Store.InsertArtifact(store.ArtifactRecord{ID: aid, IssueID: &issue.ID, RunID: &runID, Kind: kind, Path: dst, SizeBytes: st.Size(), SHA256: &sha, Redacted: true, Description: core.NullableString(desc)}); err != nil {
			return Response{}, core.NewError(core.ErrToolGatewayFailed, "insert artifact metadata: "+err.Error(), nil)
		}
		return Response{Success: true, Tool: req.Tool, Data: map[string]any{"artifact_id": aid}}, nil
	case "followup.create":
		title := strings.TrimSpace(fmt.Sprint(req.Input["title"]))
		if title == "" {
			return Response{}, core.NewError(core.ErrInvalidRequest, "title is required", nil)
		}
		desc := fmt.Sprint(req.Input["description"])
		prio := 3
		if x, ok := req.Input["priority"].(float64); ok {
			prio = int(x)
		}
		ac := toStrings(req.Input["acceptance_criteria"])
		labels := toStrings(req.Input["labels"])
		ni, err := g.Store.CreateIssue(store.CreateIssueInput{Title: title, Description: desc, AcceptanceCriteria: ac, Priority: prio, Labels: labels, CreatedByType: "agent", CreatedByRunID: &runID})
		if err != nil {
			return Response{}, err
		}
		_ = g.Store.Project.Exec(`INSERT OR IGNORE INTO issue_relations(id,source_issue_id,target_issue_id,relation_type,active,created_by_type,created_by_run_id,created_at) VALUES(?,?,?,?,?,?,?,?)`, core.NewID("rel_"), ni.ID, issue.ID, "followup_of", 1, "agent", runID, core.Now())
		return Response{Success: true, Tool: req.Tool, Data: map[string]any{"issue": ni}}, nil
	case "handoff.submit":
		payload, err := canonicalHandoff(req.Input)
		if err != nil {
			return Response{}, err
		}
		hash := canonicalHash(payload)
		h, err := g.Store.InsertHandoff(issue.ID, runID, hash, payload)
		if err != nil {
			return Response{}, err
		}
		return Response{Success: true, Tool: req.Tool, IssueIdentifier: issue.Identifier, HandoffStatus: "received", HandoffID: h.ID, Data: map[string]any{"handoff_id": h.ID, "payload_hash": hash}}, nil
	default:
		return Response{}, core.NewError(core.ErrToolGatewayFailed, "unsupported tool", nil)
	}
}

func allowed(t string) bool {
	for _, x := range Registry {
		if x == t {
			return true
		}
	}
	return false
}
func apiErrResp(err error) Response {
	ae := core.AsAPIError(err)
	return errResp(string(ae.Code), ae.Message, ae.Details)
}
func errResp(code, msg string, details map[string]any) Response {
	if details == nil {
		details = map[string]any{}
	}
	return Response{Success: false, Error: &ToolError{Code: code, Message: msg, Details: details}}
}
func toStrings(v any) []string {
	out := []string{}
	switch a := v.(type) {
	case []string:
		return a
	case []any:
		for _, x := range a {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func canonicalHandoff(in map[string]any) (map[string]any, error) {
	summary := strings.TrimSpace(fmt.Sprint(in["summary"]))
	if summary == "" {
		return nil, core.NewError(core.ErrInvalidRequest, "summary is required", nil)
	}
	target := "Human Review"
	if x, ok := in["target_state"].(string); ok && strings.TrimSpace(x) != "" {
		target = strings.TrimSpace(x)
	}
	if target != "Human Review" {
		return nil, core.NewError(core.ErrInvalidRequest, "target_state must be Human Review", nil)
	}
	out := map[string]any{"summary": summary, "changed_files": toStrings(in["changed_files"]), "tests": toStrings(in["tests"]), "risks": toStrings(in["risks"]), "verification": toStrings(in["verification"]), "followups": toStrings(in["followups"]), "target_state": "Human Review"}
	return out, nil
}
func canonicalHash(v map[string]any) string {
	b := canonicalJSON(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func canonicalJSON(v any) []byte { var buf bytes.Buffer; writeCanonical(&buf, v); return buf.Bytes() }
func writeCanonical(buf *bytes.Buffer, v any) {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, _ := json.Marshal(k)
			buf.Write(kb)
			buf.WriteByte(':')
			writeCanonical(buf, x[k])
		}
		buf.WriteByte('}')
	case []string:
		buf.WriteByte('[')
		for i, s := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			sb, _ := json.Marshal(s)
			buf.Write(sb)
		}
		buf.WriteByte(']')
	case []any:
		buf.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeCanonical(buf, e)
		}
		buf.WriteByte(']')
	default:
		b, _ := json.Marshal(x)
		buf.Write(b)
	}
}

func HTTPClientCall(endpoint, token string, req Request) Response {
	b, _ := json.Marshal(req)
	hreq, _ := http.NewRequest("POST", strings.TrimRight(endpoint, "/")+"/tool/v1/call", bytes.NewReader(b))
	hreq.Header.Set("Authorization", "Bearer "+token)
	hreq.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(hreq)
	if err != nil {
		return errResp("tool_gateway_failed", err.Error(), nil)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out Response
	if err := json.Unmarshal(body, &out); err != nil {
		return errResp("tool_gateway_failed", string(body), nil)
	}
	return out
}

func NewTokenForRun(st *store.Store, run *core.RunAttempt, workspacePath string) (string, error) {
	token := security.NewToken()
	expires := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339Nano)
	_, err := st.CreateToolToken(run.ID, security.HashToken(token), map[string]any{"run_id": run.ID, "issue_id": run.IssueID, "workspace_path": workspacePath, "tools": Registry}, expires)
	return token, err
}
