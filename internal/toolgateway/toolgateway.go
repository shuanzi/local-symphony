package toolgateway

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
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

const defaultArtifactMaxBytes int64 = 10 * 1024 * 1024

var httpClient = &http.Client{Timeout: 30 * time.Second}

type Gateway struct{ Store *store.Store }

type Request struct {
	Tool   string         `json:"tool"`
	Input  map[string]any `json:"input"`
	Client map[string]any `json:"client,omitempty"`
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
	runID, issueID, scope, err := g.Store.ValidateToolTokenWithScope(security.HashToken(token))
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
	if err := validateTokenScope(scope, req.Tool, issue.Workspace.Path); err != nil {
		return apiErrResp(err)
	}
	if err := validateToolInput(req.Tool, req.Input); err != nil {
		return apiErrResp(err)
	}
	if rel, e := filepath.Rel(issue.Workspace.Path, cwd); e != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return errResp("tool_gateway_failed", "cwd is outside workspace", nil)
	}
	_ = g.Store.RecordToolCall(issueID, runID, req.Tool, "started", req.Input, nil, "", "")
	out, callErr := g.dispatch(issue, runID, scope, req)
	if callErr != nil {
		ae := core.AsAPIError(callErr)
		_ = g.Store.RecordToolCall(issueID, runID, req.Tool, "failed", req.Input, nil, string(ae.Code), ae.Message)
		return errResp(string(ae.Code), ae.Message, ae.Details)
	}
	_ = g.Store.RecordToolCall(issueID, runID, req.Tool, "succeeded", req.Input, out, "", "")
	return out
}

func (g Gateway) dispatch(issue *core.Issue, runID string, scope map[string]any, req Request) (Response, error) {
	switch req.Tool {
	case "issue.get":
		return Response{Success: true, Tool: req.Tool, Data: issue}, nil
	case "issue.comment":
		body, err := requiredString(req.Input, "body")
		if err != nil {
			return Response{}, err
		}
		if err := g.Store.AddComment(issue.ID, "agent", body, &runID); err != nil {
			return Response{}, err
		}
		return Response{Success: true, Tool: req.Tool, IssueIdentifier: issue.Identifier, Data: map[string]any{"comment_status": "created"}}, nil
	case "issue.block":
		reason, err := requiredString(req.Input, "reason")
		if err != nil {
			return Response{}, err
		}
		if err := g.Store.BlockRunByAgent(runID, reason); err != nil {
			return Response{}, err
		}
		return Response{Success: true, Tool: req.Tool, IssueIdentifier: issue.Identifier, Data: map[string]any{"blocked": true}}, nil
	case "artifact.attach":
		rel, err := requiredString(req.Input, "path")
		if err != nil {
			return Response{}, err
		}
		kind, err := requiredString(req.Input, "kind")
		if err != nil {
			return Response{}, err
		}
		target, err := security.ContainedPath(issue.Workspace.Path, rel)
		if err != nil {
			return Response{}, core.NewError(core.ErrToolGatewayFailed, err.Error(), nil)
		}
		st, err := os.Stat(target)
		if err != nil || !st.Mode().IsRegular() {
			return Response{}, core.NewError(core.ErrInvalidRequest, "artifact path must exist and be a regular file", nil)
		}
		sizeBytes := st.Size()
		max := artifactMaxBytesFromScope(scope)
		if sizeBytes > max {
			return Response{}, core.NewError(core.ErrInvalidRequest, "artifact exceeds max size", nil)
		}
		b, err := os.ReadFile(target)
		if err != nil {
			return Response{}, core.NewError(core.ErrToolGatewayFailed, "read artifact: "+err.Error(), nil)
		}
		sizeBytes = int64(len(b))
		if sizeBytes > max {
			return Response{}, core.NewError(core.ErrInvalidRequest, "artifact exceeds max size", nil)
		}
		sha := security.SHA256Bytes(b)
		artDir := filepath.Join(g.Store.RepoRoot, ".symphony", "artifacts", issue.Identifier, runID, "agent")
		if err := os.MkdirAll(artDir, 0o755); err != nil {
			return Response{}, core.NewError(core.ErrToolGatewayFailed, "create artifact directory: "+err.Error(), nil)
		}
		dst := filepath.Join(artDir, filepath.Clean(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return Response{}, core.NewError(core.ErrToolGatewayFailed, "create artifact directory: "+err.Error(), nil)
		}
		f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return Response{}, core.NewError(core.ErrToolGatewayFailed, "write artifact: "+err.Error(), nil)
		}
		if _, err := f.Write(b); err != nil {
			_ = f.Close()
			_ = os.Remove(dst)
			return Response{}, core.NewError(core.ErrToolGatewayFailed, "write artifact: "+err.Error(), nil)
		}
		if err := f.Close(); err != nil {
			_ = os.Remove(dst)
			return Response{}, core.NewError(core.ErrToolGatewayFailed, "write artifact: "+err.Error(), nil)
		}
		aid := core.NewID("art_")
		desc, err := optionalString(req.Input, "description")
		if err != nil {
			_ = os.Remove(dst)
			return Response{}, err
		}
		if err := g.Store.InsertArtifact(store.ArtifactRecord{ID: aid, IssueID: &issue.ID, RunID: &runID, Kind: kind, Path: dst, SizeBytes: sizeBytes, SHA256: &sha, Redacted: true, Description: core.NullableString(desc)}); err != nil {
			_ = os.Remove(dst)
			return Response{}, core.NewError(core.ErrToolGatewayFailed, "insert artifact metadata: "+err.Error(), nil)
		}
		return Response{Success: true, Tool: req.Tool, Data: map[string]any{"artifact_id": aid}}, nil
	case "followup.create":
		title, err := requiredString(req.Input, "title")
		if err != nil {
			return Response{}, err
		}
		desc, err := optionalString(req.Input, "description")
		if err != nil {
			return Response{}, err
		}
		prio, err := optionalPriority(req.Input, "priority")
		if err != nil {
			return Response{}, err
		}
		ac, err := requiredStringSlice(req.Input, "acceptance_criteria")
		if err != nil {
			return Response{}, err
		}
		labels, err := requiredNonBlankStringSlice(req.Input, "labels")
		if err != nil {
			return Response{}, err
		}
		ni, err := g.Store.CreateFollowupIssue(issue.ID, runID, store.CreateIssueInput{Title: title, Description: desc, AcceptanceCriteria: ac, Priority: prio, Labels: labels, CreatedByType: "agent", CreatedByRunID: &runID})
		if err != nil {
			return Response{}, err
		}
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

func requiredString(in map[string]any, key string) (string, error) {
	v, ok := in[key]
	if !ok {
		return "", core.NewError(core.ErrInvalidRequest, key+" is required", nil)
	}
	s, ok := v.(string)
	if !ok {
		return "", core.NewError(core.ErrInvalidRequest, key+" must be string", nil)
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", core.NewError(core.ErrInvalidRequest, key+" is required", nil)
	}
	return s, nil
}

func optionalString(in map[string]any, key string) (string, error) {
	v, ok := in[key]
	if !ok || v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", core.NewError(core.ErrInvalidRequest, key+" must be string", nil)
	}
	return strings.TrimSpace(s), nil
}

func optionalPriority(in map[string]any, key string) (int, error) {
	v, ok := in[key]
	if !ok {
		return 3, nil
	}
	var n int64
	switch x := v.(type) {
	case int:
		n = int64(x)
	case int64:
		n = x
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) || math.Trunc(x) != x {
			return 0, core.NewError(core.ErrInvalidRequest, key+" must be an integer", nil)
		}
		if x < 1 || x > 5 {
			return 0, core.NewError(core.ErrInvalidRequest, key+" must be between 1 and 5", nil)
		}
		n = int64(x)
	case json.Number:
		parsed, err := x.Int64()
		if err != nil {
			return 0, core.NewError(core.ErrInvalidRequest, key+" must be an integer", nil)
		}
		n = parsed
	default:
		return 0, core.NewError(core.ErrInvalidRequest, key+" must be number", nil)
	}
	if n < 1 || n > 5 {
		return 0, core.NewError(core.ErrInvalidRequest, key+" must be between 1 and 5", nil)
	}
	return int(n), nil
}

func allowed(t string) bool {
	for _, x := range Registry {
		if x == t {
			return true
		}
	}
	return false
}

func validateTokenScope(scope map[string]any, tool, workspacePath string) error {
	if scope == nil {
		return core.NewError(core.ErrToolTokenInvalid, "tool token scope invalid", nil)
	}
	if err := validateScopedWorkspace(scope, workspacePath); err != nil {
		return err
	}
	tools, err := scopedTools(scope)
	if err != nil {
		return err
	}
	for _, scopedTool := range tools {
		if scopedTool == tool {
			return nil
		}
	}
	return core.NewError(core.ErrForbidden, "tool is not allowed by token scope", map[string]any{"tool": tool})
}

func validateScopedWorkspace(scope map[string]any, workspacePath string) error {
	v, ok := scope["workspace_path"]
	if !ok {
		return core.NewError(core.ErrToolTokenInvalid, "tool token scope missing workspace_path", nil)
	}
	scopedWorkspace, ok := v.(string)
	if !ok || strings.TrimSpace(scopedWorkspace) == "" {
		return core.NewError(core.ErrToolTokenInvalid, "tool token scope workspace_path invalid", nil)
	}
	if !sameCleanAbsPath(scopedWorkspace, workspacePath) {
		return core.NewError(core.ErrForbidden, "workspace is not allowed by token scope", nil)
	}
	return nil
}

func scopedTools(scope map[string]any) ([]string, error) {
	v, ok := scope["tools"]
	if !ok {
		return nil, core.NewError(core.ErrToolTokenInvalid, "tool token scope missing tools", nil)
	}
	switch a := v.(type) {
	case []string:
		return a, nil
	case []any:
		out := make([]string, 0, len(a))
		for _, item := range a {
			s, ok := item.(string)
			if !ok || strings.TrimSpace(s) == "" {
				return nil, core.NewError(core.ErrToolTokenInvalid, "tool token scope tools invalid", nil)
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, core.NewError(core.ErrToolTokenInvalid, "tool token scope tools invalid", nil)
	}
}

func sameCleanAbsPath(a, b string) bool {
	aa, errA := filepath.Abs(filepath.Clean(a))
	bb, errB := filepath.Abs(filepath.Clean(b))
	if errA != nil || errB != nil {
		return false
	}
	return aa == bb
}

func validateToolInput(tool string, in map[string]any) error {
	allowedKeys, ok := toolInputKeys[tool]
	if !ok {
		return core.NewError(core.ErrToolGatewayFailed, "unsupported tool", nil)
	}
	for key := range in {
		if !allowedKeys[key] {
			return core.NewError(core.ErrInvalidRequest, key+" is not allowed for "+tool, nil)
		}
	}
	if tool == "artifact.attach" {
		kind, err := requiredString(in, "kind")
		if err != nil {
			return err
		}
		if !allowedArtifactKind(kind) {
			return core.NewError(core.ErrInvalidRequest, "kind is invalid", nil)
		}
	}
	if tool == "followup.create" {
		if _, err := requiredStringSlice(in, "acceptance_criteria"); err != nil {
			return err
		}
		if _, err := requiredNonBlankStringSlice(in, "labels"); err != nil {
			return err
		}
	}
	if tool == "handoff.submit" {
		for _, key := range []string{"changed_files", "tests", "risks", "verification", "followups"} {
			if _, ok := in[key]; !ok {
				return core.NewError(core.ErrInvalidRequest, key+" is required", nil)
			}
		}
		if v, ok := in["target_state"]; ok {
			s, ok := v.(string)
			if !ok || strings.TrimSpace(s) != "Human Review" {
				return core.NewError(core.ErrInvalidRequest, "target_state must be Human Review", nil)
			}
		}
	}
	return nil
}

var toolInputKeys = map[string]map[string]bool{
	"issue.get":       {},
	"issue.comment":   {"body": true},
	"issue.block":     {"reason": true, "details": true},
	"artifact.attach": {"path": true, "kind": true, "description": true},
	"followup.create": {"title": true, "description": true, "acceptance_criteria": true, "labels": true, "priority": true},
	"handoff.submit":  {"summary": true, "changed_files": true, "tests": true, "risks": true, "verification": true, "followups": true, "target_state": true},
}

func allowedArtifactKind(kind string) bool {
	switch kind {
	case "test_output", "patch", "changed_files", "untracked_files", "diffstat", "prompt_snapshot", "codex_log", "review_packet", "agent_file", "diagnostic", "other":
		return true
	default:
		return false
	}
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
func requiredStringSlice(in map[string]any, key string) ([]string, error) {
	v, ok := in[key]
	if !ok {
		return []string{}, nil
	}
	switch a := v.(type) {
	case []string:
		return a, nil
	case []any:
		out := make([]string, 0, len(a))
		for _, x := range a {
			s, ok := x.(string)
			if !ok {
				return nil, core.NewError(core.ErrInvalidRequest, key+" must be string array", nil)
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, core.NewError(core.ErrInvalidRequest, key+" must be string array", nil)
	}
}

func requiredNonBlankStringSlice(in map[string]any, key string) ([]string, error) {
	values, err := requiredStringSlice(in, key)
	if err != nil {
		return nil, err
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return nil, core.NewError(core.ErrInvalidRequest, key+" must contain non-blank strings", nil)
		}
	}
	return values, nil
}

func artifactMaxBytesFromScope(scope map[string]any) int64 {
	if scope == nil {
		return defaultArtifactMaxBytes
	}
	switch v := scope["artifact_max_bytes"].(type) {
	case int:
		if v > 0 {
			return int64(v)
		}
	case int64:
		if v > 0 {
			return v
		}
	case float64:
		if v > 0 {
			return int64(v)
		}
	case json.Number:
		if n, err := v.Int64(); err == nil && n > 0 {
			return n
		}
	}
	return defaultArtifactMaxBytes
}

func canonicalHandoff(in map[string]any) (map[string]any, error) {
	summary, err := requiredString(in, "summary")
	if err != nil {
		return nil, err
	}
	target := "Human Review"
	if rawTarget, ok := in["target_state"]; ok {
		s, ok := rawTarget.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return nil, core.NewError(core.ErrInvalidRequest, "target_state must be Human Review", nil)
		}
		target = strings.TrimSpace(s)
	}
	if target != "Human Review" {
		return nil, core.NewError(core.ErrInvalidRequest, "target_state must be Human Review", nil)
	}
	changedFiles, err := requiredNonBlankStringSlice(in, "changed_files")
	if err != nil {
		return nil, err
	}
	tests, err := requiredStringSlice(in, "tests")
	if err != nil {
		return nil, err
	}
	risks, err := requiredStringSlice(in, "risks")
	if err != nil {
		return nil, err
	}
	verification, err := requiredStringSlice(in, "verification")
	if err != nil {
		return nil, err
	}
	followups, err := requiredStringSlice(in, "followups")
	if err != nil {
		return nil, err
	}
	out := map[string]any{"summary": summary, "changed_files": changedFiles, "tests": tests, "risks": risks, "verification": verification, "followups": followups, "target_state": "Human Review"}
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
	if req.Input == nil {
		req.Input = map[string]any{}
	}
	b, _ := json.Marshal(req)
	hreq, err := http.NewRequest("POST", strings.TrimRight(endpoint, "/")+"/tool/v1/call", bytes.NewReader(b))
	if err != nil {
		return errResp(string(core.ErrToolGatewayFailed), err.Error(), nil)
	}
	hreq.Header.Set("Authorization", "Bearer "+token)
	hreq.Header.Set("Content-Type", "application/json")
	if cwd, err := os.Getwd(); err == nil {
		hreq.Header.Set("X-Symphony-Cwd", cwd)
	}
	resp, err := httpClient.Do(hreq)
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

type TokenOptions struct {
	ArtifactMaxBytes  int64
	DisableFollowups  bool
	DisableIssueBlock bool
}

func NewTokenForRun(st *store.Store, run *core.RunAttempt, workspacePath string) (string, error) {
	return NewTokenForRunWithOptions(st, run, workspacePath, TokenOptions{})
}

func NewTokenForRunWithOptions(st *store.Store, run *core.RunAttempt, workspacePath string, opts TokenOptions) (string, error) {
	token := security.NewToken()
	expires := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339Nano)
	artifactMaxBytes := opts.ArtifactMaxBytes
	if artifactMaxBytes <= 0 {
		artifactMaxBytes = defaultArtifactMaxBytes
	}
	_, err := st.CreateToolToken(run.ID, security.HashToken(token), map[string]any{"run_id": run.ID, "issue_id": run.IssueID, "workspace_path": workspacePath, "tools": scopedRegistry(opts), "artifact_max_bytes": artifactMaxBytes}, expires)
	return token, err
}

func scopedRegistry(opts TokenOptions) []string {
	tools := make([]string, 0, len(Registry))
	for _, tool := range Registry {
		if opts.DisableIssueBlock && tool == "issue.block" {
			continue
		}
		if opts.DisableFollowups && tool == "followup.create" {
			continue
		}
		tools = append(tools, tool)
	}
	return tools
}
