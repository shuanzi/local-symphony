package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode"

	"local-symphony/internal/agent"
	"local-symphony/internal/core"
	"local-symphony/internal/store"
	"local-symphony/internal/toolgateway"
)

const (
	compatibilityMetadataFile = "compatibility.json"
	maxProtocolLineBytes      = 16 * 1024 * 1024
)

var versionProbeTimeout = 10 * time.Second

type Support struct {
	Supported bool   `json:"supported"`
	Version   string `json:"version"`
	Reason    string `json:"reason,omitempty"`
}

type CompatibilityMetadata struct {
	CodexVersion           string   `json:"codex_version"`
	ProtocolVersion        string   `json:"protocol_version"`
	SchemaVersion          string   `json:"schema_version"`
	SupportedNotifications []string `json:"supported_notifications"`
	SupportedRequests      []string `json:"supported_requests"`
	ExperimentalAPI        bool     `json:"experimental_api"`
}

type GateOptions struct {
	VersionOutput   string
	FixtureRoot     string
	ExperimentalAPI bool
}

type SelectedFixture struct {
	Version       string
	SchemaDir     string
	TranscriptDir string
	Metadata      CompatibilityMetadata
}

type HandshakeMetadata struct {
	CodexVersion    string
	ProtocolVersion string
	SchemaVersion   string
	ExperimentalAPI bool
}

type Runner struct {
	Command         string
	FixtureRoot     string
	ExperimentalAPI bool
	session         *processSession
}

var codexVersionTokenPattern = regexp.MustCompile(`^v?([0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?)$`)

func (r *Runner) Run(ctx context.Context, req agent.RunRequest) (agent.RunResult, error) {
	if r.session != nil && r.session.closed {
		r.session = nil
	}
	if r.session != nil {
		return r.runTurn(ctx, req, r.session)
	}
	selected, err := SelectFixtureMetadata(GateOptions{
		VersionOutput:   DetectVersionForCommand(r.command()),
		FixtureRoot:     r.FixtureRoot,
		ExperimentalAPI: r.ExperimentalAPI,
	})
	if err != nil {
		apiErr := core.AsAPIError(err)
		code := core.FailureCode(apiErr.Code)
		if code == "" {
			code = core.FailureCodexProtocolError
		}
		return agent.RunResult{Kind: agent.RunResultFailed, FailureCode: code, FailureMessage: apiErr.Message}, nil
	}
	emit(req, "codex.version_checked", map[string]any{"codex_version": selected.Version, "protocol_version": selected.Metadata.ProtocolVersion, "schema_version": selected.Metadata.SchemaVersion})
	session, result, err := r.startAppServer(req, selected)
	if result.Kind != "" || err != nil {
		return result, err
	}
	r.session = session
	return r.runTurn(ctx, req, session)
}

func (r *Runner) Close(_ context.Context, req agent.RunRequest) error {
	if r.session == nil || r.session.closed {
		return nil
	}
	_ = r.closeSession(req)
	return nil
}

func (r Runner) command() string {
	if strings.TrimSpace(r.Command) == "" {
		return "codex app-server"
	}
	return strings.TrimSpace(r.Command)
}

func DetectVersion() string {
	return DetectVersionForCommand("codex app-server")
}

func DetectVersionForCommand(command string) string {
	parts := commandParts(command)
	if len(parts) == 0 {
		return ""
	}
	for _, candidate := range versionProbeCandidates(parts) {
		out, err := versionProbeOutput(candidate)
		if err != nil {
			continue
		}
		output := strings.TrimSpace(string(out))
		if _, err := ParseCodexVersionOutput(output); err == nil {
			return output
		}
	}
	return ""
}

func versionProbeCandidates(parts []string) [][]string {
	direct := []string{parts[0], "--version"}
	if len(parts) == 1 {
		return [][]string{direct}
	}
	full := append(append([]string{}, parts...), "--version")
	if len(parts) == 2 && parts[1] == "app-server" {
		return [][]string{direct}
	}
	return [][]string{full, direct}
}

func versionProbeOutput(parts []string) ([]byte, error) {
	return commandOutputWithTimeout(parts, versionProbeTimeout)
}

func commandOutputWithTimeout(parts []string, timeout time.Duration) ([]byte, error) {
	if len(parts) == 0 {
		return nil, fmt.Errorf("command is empty")
	}
	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()
	select {
	case err := <-waitCh:
		return stdout.Bytes(), err
	case <-time.After(timeout):
		terminateProcessGroup(cmd.Process)
		return nil, waitForProcess(waitCh, cmd.Process)
	}
}

type protocolMessage struct {
	JSONRPC         string          `json:"jsonrpc"`
	ID              json.RawMessage `json:"id"`
	Method          string          `json:"method"`
	Type            string          `json:"type"`
	CodexVersion    string          `json:"codex_version"`
	ProtocolVersion string          `json:"protocol_version"`
	SchemaVersion   string          `json:"schema_version"`
	ExperimentalAPI bool            `json:"experimental_api"`
	ThreadID        string          `json:"thread_id"`
	TurnID          string          `json:"turn_id"`
	Message         string          `json:"message"`
	FailureCode     string          `json:"failure_code"`
	Error           string          `json:"error"`
	Payload         map[string]any  `json:"payload"`
	Params          map[string]any  `json:"params"`
}

type approvalResponseTarget struct {
	requestID            string
	jsonrpcID            json.RawMessage
	permissionsResponse  bool
	requestedPermissions map[string]any
}

type stdoutItem struct {
	line string
	err  error
}

type processSession struct {
	cmd            *exec.Cmd
	stdin          io.WriteCloser
	lines          chan stdoutItem
	waitCh         chan error
	stderrCounter  *byteCounter
	selected       *SelectedFixture
	handshakeDone  bool
	threadID       string
	processExited  bool
	closed         bool
	processStarted bool
	jsonrpcItems   map[string]map[string]any
}

func (r *Runner) startAppServer(req agent.RunRequest, selected *SelectedFixture) (*processSession, agent.RunResult, error) {
	parts := commandParts(r.command())
	if len(parts) == 0 {
		return nil, failed(core.FailureCodexStartupFailed, "codex command is empty"), nil
	}
	cmd := exec.Command(parts[0], parts[1:]...)
	if req.Workspace != nil {
		cmd.Dir = req.Workspace.Path
	}
	cmd.Env = codexEnv(req)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, failed(core.FailureCodexStartupFailed, err.Error()), nil
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, failed(core.FailureCodexStartupFailed, err.Error()), nil
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, failed(core.FailureCodexStartupFailed, err.Error()), nil
	}
	stderrCounter := &byteCounter{}
	if err := cmd.Start(); err != nil {
		return nil, failed(core.FailureCodexStartupFailed, err.Error()), nil
	}
	emit(req, "agent.process_started", map[string]any{"command": "redacted", "cwd": cmd.Dir, "codex_version": selected.Version})
	go func() { _, _ = io.Copy(stderrCounter, stderr) }()
	lines := make(chan stdoutItem, 128)
	go func() {
		scanStdout(stdout, lines)
	}()
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	return &processSession{
		cmd:            cmd,
		stdin:          stdin,
		lines:          lines,
		waitCh:         waitCh,
		stderrCounter:  stderrCounter,
		selected:       selected,
		processStarted: true,
		jsonrpcItems:   map[string]map[string]any{},
	}, agent.RunResult{}, nil
}

func (r *Runner) runTurn(ctx context.Context, req agent.RunRequest, session *processSession) (agent.RunResult, error) {
	if session == nil || session.closed {
		return failed(core.FailureCodexProtocolError, "codex session is closed"), nil
	}
	startTurn := map[string]any{"type": "start_turn", "prompt": req.Prompt, "continuation": req.IsContinuation}
	if session.threadID != "" {
		startTurn["thread_id"] = session.threadID
	}
	if err := json.NewEncoder(session.stdin).Encode(startTurn); err != nil {
		_ = r.closeSession(req)
		return failed(core.FailureCodexProtocolError, "codex stdin write failed"), nil
	}

	startup := time.NewTimer(timeoutDuration(req.Timeouts.StartupMS, 60*time.Second))
	defer startup.Stop()
	turn := time.NewTimer(timeoutDuration(req.Timeouts.TurnMS, time.Hour))
	defer turn.Stop()
	stall := time.NewTimer(timeoutDuration(req.Timeouts.StallMS, 5*time.Minute))
	defer stall.Stop()
	var read *time.Timer
	var readC <-chan time.Time
	if req.Timeouts.ReadMS > 0 {
		read = time.NewTimer(time.Duration(req.Timeouts.ReadMS) * time.Millisecond)
		defer read.Stop()
		readC = read.C
	}
	startupC := startup.C
	if session.handshakeDone {
		startupC = nil
	}
	turnC := turn.C
	stallC := stall.C
	handoffReceived := false

	for {
		select {
		case <-ctx.Done():
			_ = r.closeSession(req)
			return failed(core.FailureOperatorCancelled, "operator cancelled"), nil
		case <-startupC:
			if !session.handshakeDone {
				_ = r.closeSession(req)
				return failed(core.FailureCodexStartupFailed, "codex startup timeout"), nil
			}
		case <-turnC:
			_ = r.closeSession(req)
			return failed(core.FailureTurnTimeout, "codex turn timeout"), nil
		case <-stallC:
			_ = r.closeSession(req)
			return failed(core.FailureStallTimeout, "codex stall timeout"), nil
		case <-readC:
			_ = r.closeSession(req)
			return failed(core.FailureCodexProtocolError, "codex read timeout"), nil
		case item, ok := <-session.lines:
			if !ok {
				err := waitForProcess(session.waitCh, session.cmd.Process)
				session.closed = true
				session.processExited = true
				emitProcessExited(req, err, session.stderrCounter)
				r.session = nil
				if err != nil {
					return failed(core.FailureCodexProtocolError, err.Error()), nil
				}
				return failed(core.FailureCodexProtocolError, "codex process exited before terminal turn result"), nil
			}
			if read != nil {
				stopTimer(read)
				read = nil
				readC = nil
			}
			if item.err != nil {
				_ = r.closeSession(req)
				return failed(core.FailureCodexProtocolError, "codex stdout read failed"), nil
			}
			resetTimer(stall, timeoutDuration(req.Timeouts.StallMS, 5*time.Minute))
			if isPostHandshakeApprovalRequest(item.line, session.handshakeDone) {
				result, terminal, err := r.handleApprovalRequest(ctx, req, session, item.line, turnC)
				if err != nil {
					emit(req, "agent.protocol_error", map[string]any{"message": "redacted"})
					_ = r.closeSession(req)
					return failed(core.FailureCodexProtocolError, "codex protocol error"), nil
				}
				if terminal {
					_ = r.closeSession(req)
					return result, nil
				}
				resetTimer(stall, timeoutDuration(req.Timeouts.StallMS, 5*time.Minute))
				continue
			}
			result, terminal, err := handleProtocolLine(req, session.selected, item.line, &session.handshakeDone, &startupC, &handoffReceived, &session.threadID, session.jsonrpcItems)
			if err != nil {
				emit(req, "agent.protocol_error", map[string]any{"message": "redacted"})
				_ = r.closeSession(req)
				return failed(core.FailureCodexProtocolError, "codex protocol error"), nil
			}
			if terminal {
				if result.Kind == agent.RunResultMissingHandoff {
					return result, nil
				}
				_ = r.closeSession(req)
				return result, nil
			}
		}
	}
}

func isPostHandshakeApprovalRequest(line string, handshakeDone bool) bool {
	if !handshakeDone {
		return false
	}
	var msg protocolMessage
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		return false
	}
	if msg.Type == "approval_requested" && strings.TrimSpace(payloadString(msg.Payload, "request_id")) != "" {
		return true
	}
	return isJSONRPCApprovalMethod(msg.Method) && len(bytes.TrimSpace(msg.ID)) != 0
}

func (r *Runner) handleApprovalRequest(ctx context.Context, req agent.RunRequest, session *processSession, line string, turnC <-chan time.Time) (agent.RunResult, bool, error) {
	if req.Gateway.Store == nil {
		return agent.RunResult{}, false, fmt.Errorf("approval store is required")
	}
	var msg protocolMessage
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		return agent.RunResult{}, false, err
	}
	msg = mergeJSONRPCApprovalItemDetails(msg, session.jsonrpcItems)
	approvalInput, responseTarget, err := approvalInputFromMessage(req, msg)
	if err != nil {
		return agent.RunResult{}, false, err
	}
	if handled, err := r.tryApprovedForRunDecision(req, session, approvalInput, responseTarget); err != nil {
		return failed(core.FailureCodexProtocolError, "codex stdin write failed"), true, nil
	} else if handled {
		return agent.RunResult{}, false, nil
	}
	approval, err := req.Gateway.Store.CreatePendingApprovalRequest(approvalInput)
	if err != nil {
		return agent.RunResult{}, false, err
	}
	emit(req, "approval.requested", map[string]any{"kind": "redacted", "approval_id": approval.ID})
	result, terminal, err := r.waitForApprovalDecision(ctx, req, session, approval.ID, responseTarget, approvalInput.TimeoutMS, turnC)
	if err != nil || terminal {
		return result, terminal, err
	}
	return agent.RunResult{}, false, nil
}

func (r *Runner) tryApprovedForRunDecision(req agent.RunRequest, session *processSession, approvalInput store.CreateApprovalRequestInput, target approvalResponseTarget) (bool, error) {
	if target.permissionsResponse {
		return false, nil
	}
	ok, err := req.Gateway.Store.HasApprovedForRunApproval(approvalInput)
	if err != nil || !ok {
		return false, err
	}
	if err := writeApprovalDecision(session.stdin, approvalIDForCodex("", target.requestID), target, "approve_for_run"); err != nil {
		return false, err
	}
	emit(req, "approval.resolved", map[string]any{"decision": "redacted"})
	return true, nil
}

func approvalInputFromMessage(req agent.RunRequest, msg protocolMessage) (store.CreateApprovalRequestInput, approvalResponseTarget, error) {
	if isJSONRPCApprovalMethod(msg.Method) {
		return approvalInputFromJSONRPCRequest(req, msg)
	}
	kind := strings.TrimSpace(payloadString(msg.Payload, "kind"))
	switch kind {
	case "command", "file_change", "network":
	default:
		return store.CreateApprovalRequestInput{}, approvalResponseTarget{}, fmt.Errorf("unsupported approval kind %q", kind)
	}
	requestID := strings.TrimSpace(payloadString(msg.Payload, "request_id"))
	return store.CreateApprovalRequestInput{
		RunID:         req.Run.ID,
		IssueID:       req.Issue.ID,
		Kind:          kind,
		ActionSummary: payloadString(msg.Payload, "action_summary"),
		RiskLevel:     payloadString(msg.Payload, "risk_level"),
		PolicyMatch:   payloadString(msg.Payload, "policy_match"),
		RequestID:     requestID,
		CWD:           payloadString(msg.Payload, "cwd"),
		Fingerprint:   payloadString(msg.Payload, "fingerprint"),
		TimeoutMS:     payloadInt64(msg.Payload, "timeout_ms"),
	}, approvalResponseTarget{requestID: requestID}, nil
}

func approvalInputFromJSONRPCRequest(req agent.RunRequest, msg protocolMessage) (store.CreateApprovalRequestInput, approvalResponseTarget, error) {
	requestID := jsonRPCIDString(msg.ID)
	if requestID == "" {
		return store.CreateApprovalRequestInput{}, approvalResponseTarget{}, fmt.Errorf("json-rpc approval id is required")
	}
	kind := jsonRPCApprovalKind(msg)
	target := approvalResponseTarget{requestID: requestID, jsonrpcID: append(json.RawMessage(nil), msg.ID...)}
	if strings.TrimSpace(msg.Method) == "item/permissions/requestApproval" {
		target.permissionsResponse = true
		target.requestedPermissions = jsonRPCRequestedPermissions(msg.Params)
	}
	return store.CreateApprovalRequestInput{
		RunID:         req.Run.ID,
		IssueID:       req.Issue.ID,
		Kind:          kind,
		ActionSummary: jsonRPCApprovalActionSummary(kind, msg.Params),
		RiskLevel:     payloadString(msg.Params, "risk_level"),
		PolicyMatch:   payloadString(msg.Params, "policy_match"),
		RequestID:     requestID,
		CWD:           payloadString(msg.Params, "cwd"),
		Fingerprint:   jsonRPCApprovalFingerprint(kind, msg.Params),
		TimeoutMS:     payloadInt64(msg.Params, "timeout_ms"),
	}, target, nil
}

func jsonRPCApprovalKind(msg protocolMessage) string {
	switch strings.TrimSpace(msg.Method) {
	case "item/fileChange/requestApproval":
		return "file_change"
	case "item/permissions/requestApproval":
		return jsonRPCPermissionApprovalKind(msg.Params)
	}
	if hasNetworkApprovalContext(msg.Params) {
		return "network"
	}
	return "command"
}

func jsonRPCPermissionApprovalKind(params map[string]any) string {
	permissions, _ := params["permissions"].(map[string]any)
	if _, ok := permissions["network"]; ok {
		return "network"
	}
	if _, ok := permissions["fileSystem"]; ok {
		return "file_change"
	}
	if _, ok := permissions["filesystem"]; ok {
		return "file_change"
	}
	return "file_change"
}

func jsonRPCRequestedPermissions(params map[string]any) map[string]any {
	permissions, ok := params["permissions"].(map[string]any)
	if !ok || permissions == nil {
		return map[string]any{}
	}
	return permissions
}

func jsonRPCApprovalFingerprint(kind string, params map[string]any) string {
	switch kind {
	case "command":
		return jsonRPCValueFingerprint("command", params["command"])
	case "file_change":
		if value := payloadString(params, "grantRoot"); value != "" {
			return "grantRoot:" + value
		}
		if value := payloadString(params, "path"); value != "" {
			return "path:" + value
		}
		return jsonRPCValueFingerprint("changes", params["changes"])
	case "network":
		if value := jsonRPCValueFingerprint("networkApprovalContext", params["networkApprovalContext"]); value != "" {
			return value
		}
		if additional, ok := params["additionalPermissions"].(map[string]any); ok {
			if value := jsonRPCValueFingerprint("additionalPermissions.network", additional["network"]); value != "" {
				return value
			}
		}
		if permissions, ok := params["permissions"].(map[string]any); ok {
			if value := jsonRPCValueFingerprint("permissions.network", permissions["network"]); value != "" {
				return value
			}
		}
		if value := jsonRPCValueFingerprint("policyAmendment", params["policyAmendment"]); value != "" {
			return value
		}
		return jsonRPCValueFingerprint("policy_amendment", params["policy_amendment"])
	default:
		return ""
	}
}

func jsonRPCValueFingerprint(key string, value any) string {
	if value == nil {
		return ""
	}
	b, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return key + ":" + string(b)
}

func hasNetworkApprovalContext(params map[string]any) bool {
	if hasJSONRPCCommand(params) {
		return false
	}
	if _, ok := params["networkApprovalContext"]; ok {
		return true
	}
	additional, ok := params["additionalPermissions"].(map[string]any)
	if !ok {
		return false
	}
	network, ok := additional["network"].(map[string]any)
	if !ok {
		return false
	}
	enabled, _ := network["enabled"].(bool)
	return enabled
}

func hasJSONRPCCommand(params map[string]any) bool {
	command, ok := params["command"]
	if !ok || command == nil {
		return false
	}
	switch value := command.(type) {
	case string:
		return strings.TrimSpace(value) != ""
	case []any:
		return len(value) > 0
	default:
		return true
	}
}

func isJSONRPCApprovalMethod(method string) bool {
	switch strings.TrimSpace(method) {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval", "item/permissions/requestApproval":
		return true
	default:
		return false
	}
}

func jsonRPCApprovalActionSummary(kind string, params map[string]any) string {
	if kind != "command" {
		if summary := jsonRPCPermissionActionSummary(params); summary != "" {
			return summary
		}
	}
	if kind == "command" {
		if summary := jsonRPCCommandActionSummary(params); summary != "" {
			return summary
		}
	}
	if kind == "file_change" {
		if summary := jsonRPCFileChangeActionSummary(params); summary != "" {
			return summary
		}
	}
	if value := payloadString(params, "action_summary"); value != "" {
		return value
	}
	if value := payloadString(params, "reason"); value != "" {
		return value
	}
	if kind == "command" {
		return jsonRPCCommandSummary(params["command"])
	}
	return payloadString(params, "summary")
}

func jsonRPCPermissionActionSummary(params map[string]any) string {
	permissions, ok := params["permissions"].(map[string]any)
	if !ok || !jsonRPCPermissionsIncludeMixedGrants(permissions) {
		return ""
	}
	summary := payloadString(params, "reason")
	if summary == "" {
		summary = payloadString(params, "action_summary")
	}
	if summary == "" {
		summary = payloadString(params, "summary")
	}
	permissionsJSON, err := json.Marshal(permissions)
	if err != nil {
		return summary
	}
	if summary == "" {
		return "permissions: " + string(permissionsJSON)
	}
	return summary + " | permissions: " + string(permissionsJSON)
}

func jsonRPCPermissionsIncludeMixedGrants(permissions map[string]any) bool {
	hasNetwork := false
	if _, ok := permissions["network"]; ok {
		hasNetwork = true
	}
	return hasNetwork && jsonRPCPermissionsIncludeFilesystemGrant(permissions)
}

func jsonRPCPermissionsIncludeFilesystemGrant(permissions map[string]any) bool {
	if _, ok := permissions["fileSystem"]; ok {
		return true
	}
	if _, ok := permissions["filesystem"]; ok {
		return true
	}
	return false
}

func jsonRPCCommandActionSummary(params map[string]any) string {
	command := jsonRPCCommandDisplay(params["command"])
	if command == "" {
		return ""
	}
	cwd := payloadString(params, "cwd")
	reason := payloadString(params, "reason")
	actionSummary := payloadString(params, "action_summary")
	if cwd == "" && reason == "" && actionSummary == "" {
		return command
	}
	parts := []string{command}
	if cwd != "" {
		parts = append(parts, "cwd: "+cwd)
	}
	if reason != "" {
		parts = append(parts, "reason: "+reason)
	}
	if actionSummary != "" {
		parts = append(parts, "summary: "+actionSummary)
	}
	return strings.Join(parts, " | ")
}

func jsonRPCCommandDisplay(value any) string {
	switch command := value.(type) {
	case string:
		return strings.TrimSpace(command)
	case []any:
		parts := make([]string, 0, len(command))
		for _, part := range command {
			text, ok := part.(string)
			if !ok {
				return ""
			}
			parts = append(parts, text)
		}
		b, err := json.Marshal(parts)
		if err != nil {
			return ""
		}
		return "argv: " + string(b)
	default:
		return ""
	}
}

func jsonRPCFileChangeActionSummary(params map[string]any) string {
	parts := make([]string, 0, 5)
	if reason := payloadString(params, "reason"); reason != "" {
		parts = append(parts, reason)
	} else if summary := payloadString(params, "action_summary"); summary != "" {
		parts = append(parts, summary)
	} else if summary := payloadString(params, "summary"); summary != "" {
		parts = append(parts, summary)
	}
	if value := payloadString(params, "grantRoot"); value != "" {
		parts = append(parts, "grantRoot: "+value)
	}
	if value := payloadString(params, "path"); value != "" {
		parts = append(parts, "path: "+value)
	}
	if value := jsonRPCSummaryValue("paths", params["paths"]); value != "" {
		parts = append(parts, value)
	}
	if value := payloadString(params, "diff"); value != "" {
		parts = append(parts, "diff: "+value)
	}
	if value := jsonRPCSummaryValue("changes", params["changes"]); value != "" {
		parts = append(parts, value)
	}
	return strings.Join(parts, " | ")
}

func jsonRPCSummaryValue(label string, value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		text = strings.TrimSpace(text)
		if text == "" {
			return ""
		}
		return label + ": " + text
	}
	b, err := json.Marshal(value)
	if err != nil || len(b) == 0 || string(b) == "null" {
		return ""
	}
	return label + ": " + string(b)
}

func jsonRPCCommandSummary(value any) string {
	switch command := value.(type) {
	case string:
		return strings.TrimSpace(command)
	case []any:
		parts := make([]string, 0, len(command))
		for _, part := range command {
			text, ok := part.(string)
			if !ok {
				return ""
			}
			parts = append(parts, text)
		}
		return strings.TrimSpace(strings.Join(parts, " "))
	default:
		return ""
	}
}

func jsonRPCIDString(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(string(raw))
}

func (r *Runner) waitForApprovalDecision(ctx context.Context, req agent.RunRequest, session *processSession, approvalID string, target approvalResponseTarget, timeoutMS int64, turnC <-chan time.Time) (agent.RunResult, bool, error) {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	var timeoutC <-chan time.Time
	var timeout *time.Timer
	if timeoutMS > 0 {
		timeout = time.NewTimer(time.Duration(timeoutMS) * time.Millisecond)
		defer timeout.Stop()
		timeoutC = timeout.C
	}
	for {
		select {
		case <-ctx.Done():
			if err := markApprovalCancelledIfPending(req.Gateway.Store, approvalID, "operator cancelled"); err != nil {
				return agent.RunResult{}, false, err
			}
			return failed(core.FailureOperatorCancelled, "operator cancelled"), true, nil
		case <-turnC:
			if err := markApprovalTimeoutIfPending(req.Gateway.Store, approvalID, "codex turn timeout"); err != nil {
				return agent.RunResult{}, false, err
			}
			return failed(core.FailureTurnTimeout, "codex turn timeout"), true, nil
		case err := <-session.waitCh:
			session.closed = true
			session.processExited = true
			emitProcessExited(req, err, session.stderrCounter)
			r.session = nil
			if cancelErr := markApprovalCancelledIfPending(req.Gateway.Store, approvalID, "codex process exited"); cancelErr != nil {
				return agent.RunResult{}, false, cancelErr
			}
			if err != nil {
				return failed(core.FailureCodexProtocolError, err.Error()), true, nil
			}
			return failed(core.FailureCodexProtocolError, "codex process exited before approval decision"), true, nil
		case <-timeoutC:
			if err := markApprovalTimeoutIfPending(req.Gateway.Store, approvalID, "approval timed out"); err != nil {
				return agent.RunResult{}, false, err
			}
			return failed(core.FailureApprovalTimeout, "approval timed out"), true, nil
		case <-ticker.C:
			approval, err := req.Gateway.Store.ApprovalByID(approvalID)
			if err != nil {
				return agent.RunResult{}, false, err
			}
			if approval.Status == "timeout" {
				return failed(core.FailureApprovalTimeout, "approval timed out"), true, nil
			}
			decision, ok := approvalDecisionForStatus(approval.Status)
			if !ok {
				continue
			}
			if decision == "cancel_run" {
				return failed(core.FailureOperatorCancelled, "operator cancelled"), true, nil
			}
			if err := writeApprovalDecision(session.stdin, approvalIDForCodex(approvalID, target.requestID), target, decision); err != nil {
				return failed(core.FailureCodexProtocolError, "codex stdin write failed"), true, nil
			}
			emit(req, "approval.resolved", map[string]any{"decision": "redacted"})
			return agent.RunResult{}, false, nil
		}
	}
}

func markApprovalTimeoutIfPending(st *store.Store, approvalID, reason string) error {
	if err := st.MarkApprovalTimeout(approvalID, reason); err != nil {
		if core.AsAPIError(err).Code == core.ErrApprovalNotPending {
			return nil
		}
		return err
	}
	return nil
}

func markApprovalCancelledIfPending(st *store.Store, approvalID, reason string) error {
	if err := st.MarkApprovalCancelled(approvalID, reason); err != nil {
		if core.AsAPIError(err).Code == core.ErrApprovalNotPending {
			return nil
		}
		return err
	}
	return nil
}

func payloadString(payload map[string]any, key string) string {
	value, ok := payload[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func payloadInt64(payload map[string]any, key string) int64 {
	switch value := payload[key].(type) {
	case float64:
		if value > 0 {
			return int64(value)
		}
	case int64:
		if value > 0 {
			return value
		}
	case int:
		if value > 0 {
			return int64(value)
		}
	}
	return 0
}

func approvalDecisionForStatus(status string) (string, bool) {
	switch status {
	case "approved_once":
		return "approve_once", true
	case "approved_for_run":
		return "approve_for_run", true
	case "approved_for_session":
		return "approve_for_session", true
	case "denied":
		return "deny", true
	case "cancelled":
		return "cancel_run", true
	default:
		return "", false
	}
}

func approvalIDForCodex(approvalID, requestID string) string {
	if strings.TrimSpace(requestID) != "" {
		return strings.TrimSpace(requestID)
	}
	return approvalID
}

func writeApprovalDecision(w io.Writer, approvalID string, target approvalResponseTarget, decision string) error {
	if len(bytes.TrimSpace(target.jsonrpcID)) != 0 {
		if target.permissionsResponse {
			return writeJSONRPCPermissionApprovalDecision(w, target.jsonrpcID, decision, target.requestedPermissions)
		}
		return writeJSONRPCApprovalDecision(w, target.jsonrpcID, jsonRPCApprovalDecision(decision))
	}
	return json.NewEncoder(w).Encode(map[string]string{
		"type":        "approval_decision",
		"approval_id": approvalID,
		"decision":    decision,
	})
}

func jsonRPCApprovalDecision(decision string) string {
	switch decision {
	case "approve_once", "approve_for_run":
		return "accept"
	case "approve_for_session":
		return "acceptForSession"
	case "deny":
		return "decline"
	case "cancel_run":
		return "cancel"
	default:
		return decision
	}
}

func writeJSONRPCPermissionApprovalDecision(w io.Writer, id json.RawMessage, decision string, permissions map[string]any) error {
	result := map[string]any{"permissions": map[string]any{}}
	switch decision {
	case "approve_once", "approve_for_run", "approve_for_session":
		if permissions != nil {
			result["permissions"] = permissions
		}
		if decision == "approve_for_session" {
			result["scope"] = "session"
		}
	}
	return json.NewEncoder(w).Encode(map[string]any{
		"id":     json.RawMessage(bytes.TrimSpace(id)),
		"result": result,
	})
}

func writeJSONRPCApprovalDecision(w io.Writer, id json.RawMessage, decision string) error {
	var buf bytes.Buffer
	buf.WriteString(`{"id":`)
	buf.Write(bytes.TrimSpace(id))
	buf.WriteString(`,"result":{"decision":`)
	decisionJSON, err := json.Marshal(decision)
	if err != nil {
		return err
	}
	buf.Write(decisionJSON)
	buf.WriteString("}}\n")
	_, err = w.Write(buf.Bytes())
	return err
}

func (r *Runner) closeSession(req agent.RunRequest) error {
	if r.session == nil || r.session.closed {
		return nil
	}
	session := r.session
	session.closed = true
	_ = session.stdin.Close()
	terminateProcessGroup(session.cmd.Process)
	waitErr := waitForProcess(session.waitCh, session.cmd.Process)
	session.processExited = true
	emitProcessExited(req, waitErr, session.stderrCounter)
	r.session = nil
	return waitErr
}

func handleProtocolLine(req agent.RunRequest, selected *SelectedFixture, line string, handshakeDone *bool, startupC *<-chan time.Time, handoffReceived *bool, threadID *string, jsonrpcItems map[string]map[string]any) (agent.RunResult, bool, error) {
	var msg protocolMessage
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		return agent.RunResult{}, false, err
	}
	if msg.Type == "" && strings.TrimSpace(msg.Method) != "" {
		if !*handshakeDone {
			return agent.RunResult{}, false, fmt.Errorf("json-rpc notification before handshake")
		}
		if isExpectedJSONRPCNotification(msg.Method) {
			if strings.TrimSpace(msg.Method) == "item/started" {
				rememberJSONRPCStartedItem(msg.Params, jsonrpcItems)
			}
			return agent.RunResult{}, false, nil
		}
		return agent.RunResult{}, false, fmt.Errorf("unsupported json-rpc notification %q", msg.Method)
	}
	switch msg.Type {
	case "handshake":
		err := ValidateHandshakeMetadata(selected.Metadata, HandshakeMetadata{CodexVersion: msg.CodexVersion, ProtocolVersion: msg.ProtocolVersion, SchemaVersion: msg.SchemaVersion, ExperimentalAPI: msg.ExperimentalAPI})
		if err != nil {
			return agent.RunResult{}, false, err
		}
		*handshakeDone = true
		*startupC = nil
		emit(req, "agent.handshake_completed", map[string]any{"codex_version": msg.CodexVersion, "protocol_version": msg.ProtocolVersion, "schema_version": msg.SchemaVersion})
	case "thread_started":
		if !*handshakeDone {
			return agent.RunResult{}, false, fmt.Errorf("thread started before handshake")
		}
		if strings.TrimSpace(msg.ThreadID) != "" {
			*threadID = msg.ThreadID
		}
		emit(req, "agent.thread_started", map[string]any{"thread_id": msg.ThreadID})
	case "turn_started":
		if !*handshakeDone {
			return agent.RunResult{}, false, fmt.Errorf("turn started before handshake")
		}
		emit(req, "agent.turn_started", map[string]any{"turn_id": msg.TurnID})
	case "turn_progress":
		if !*handshakeDone {
			return agent.RunResult{}, false, fmt.Errorf("turn progress before handshake")
		}
		emit(req, "agent.turn_progress", map[string]any{"message": redactedMessage(msg.Message)})
	case "handoff":
		if !*handshakeDone {
			return agent.RunResult{}, false, fmt.Errorf("handoff before handshake")
		}
		if len(msg.Payload) == 0 {
			return agent.RunResult{}, false, fmt.Errorf("handoff payload is required")
		}
		resp := req.Gateway.Call(req.ToolToken, workspacePath(req), toolgateway.Request{Tool: "handoff.submit", Input: msg.Payload})
		if resp.Error != nil {
			return failed(core.FailureToolGatewayFailed, "codex handoff submit failed"), true, nil
		}
		*handoffReceived = true
	case "approval_requested":
		if !*handshakeDone {
			return agent.RunResult{}, false, fmt.Errorf("approval requested before handshake")
		}
		emit(req, "approval.requested", map[string]any{"kind": "redacted"})
	case "approval_resolved":
		if !*handshakeDone {
			return agent.RunResult{}, false, fmt.Errorf("approval resolved before handshake")
		}
		emit(req, "approval.resolved", map[string]any{"decision": "redacted"})
	case "tool_call":
		if !*handshakeDone {
			return agent.RunResult{}, false, fmt.Errorf("tool call before handshake")
		}
		emit(req, "agent.tool_call_observed", map[string]any{"tool": "redacted"})
	case "turn_completed":
		if !*handshakeDone {
			return agent.RunResult{}, false, fmt.Errorf("turn completed before handshake")
		}
		emit(req, "agent.turn_completed", map[string]any{})
		if !*handoffReceived {
			return agent.RunResult{Kind: agent.RunResultMissingHandoff, FailureCode: core.FailureMissingHandoff, FailureMessage: "handoff missing"}, true, nil
		}
		return agent.RunResult{Kind: agent.RunResultSucceeded}, true, nil
	case "turn_failed":
		if !*handshakeDone {
			return agent.RunResult{}, false, fmt.Errorf("turn failed before handshake")
		}
		code := core.FailureCodexProtocolError
		if strings.TrimSpace(msg.FailureCode) != "" {
			code = core.FailureCode(msg.FailureCode)
		}
		emit(req, "agent.turn_failed", map[string]any{"failure_code": code, "message": "redacted"})
		return failed(code, "codex turn failed"), true, nil
	default:
		return agent.RunResult{}, false, fmt.Errorf("unsupported codex message type %q", msg.Type)
	}
	return agent.RunResult{}, false, nil
}

func rememberJSONRPCStartedItem(params map[string]any, items map[string]map[string]any) {
	if len(params) == 0 || items == nil {
		return
	}
	details := copyStringAnyMap(params)
	if item, ok := params["item"].(map[string]any); ok {
		details = copyStringAnyMap(item)
		for key, value := range params {
			if key == "item" {
				continue
			}
			if _, exists := details[key]; !exists {
				details[key] = value
			}
		}
	}
	itemID := jsonRPCItemID(details)
	if itemID == "" {
		itemID = jsonRPCItemID(params)
	}
	if itemID == "" {
		return
	}
	items[itemID] = details
}

func mergeJSONRPCApprovalItemDetails(msg protocolMessage, items map[string]map[string]any) protocolMessage {
	if strings.TrimSpace(msg.Method) != "item/fileChange/requestApproval" || len(items) == 0 {
		return msg
	}
	itemID := jsonRPCItemID(msg.Params)
	if itemID == "" {
		return msg
	}
	details, ok := items[itemID]
	if !ok || len(details) == 0 {
		return msg
	}
	merged := copyStringAnyMap(details)
	for key, value := range msg.Params {
		merged[key] = value
	}
	msg.Params = merged
	return msg
}

func jsonRPCItemID(params map[string]any) string {
	for _, key := range []string{"itemId", "item_id", "id"} {
		if value := payloadString(params, key); value != "" {
			return value
		}
	}
	if item, ok := params["item"].(map[string]any); ok {
		return jsonRPCItemID(item)
	}
	return ""
}

func copyStringAnyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func isExpectedJSONRPCNotification(method string) bool {
	switch strings.TrimSpace(method) {
	case "serverRequest/resolved", "item/started", "item/completed":
		return true
	default:
		return false
	}
}

func ParseCodexVersionOutput(output string) (string, error) {
	for _, field := range strings.Fields(strings.TrimSpace(output)) {
		matches := codexVersionTokenPattern.FindStringSubmatch(field)
		if len(matches) == 2 {
			return matches[1], nil
		}
	}
	return "", fmt.Errorf("could not parse codex version from output")
}

func SelectFixtureMetadata(options GateOptions) (*SelectedFixture, error) {
	version, err := ParseCodexVersionOutput(options.VersionOutput)
	if err != nil {
		return nil, unsupportedCodexVersion("malformed_version", map[string]any{"version_output": strings.TrimSpace(options.VersionOutput)})
	}

	root := options.FixtureRoot
	if root == "" {
		root = defaultFixtureRoot()
	}
	schemaDir := filepath.Join(root, "schema", version)
	transcriptDir := filepath.Join(root, "transcripts", version)
	if !isDir(schemaDir) || !isDir(transcriptDir) {
		return nil, unsupportedCodexVersion("missing_fixture", map[string]any{"codex_version": version})
	}

	metadataPath := filepath.Join(schemaDir, compatibilityMetadataFile)
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		return nil, unsupportedCodexVersion("missing_metadata", map[string]any{"codex_version": version})
	}
	var metadata CompatibilityMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, unsupportedCodexVersion("malformed_metadata", map[string]any{"codex_version": version})
	}
	if err := validateCompatibilityMetadata(version, metadata); err != nil {
		return nil, err
	}
	if options.ExperimentalAPI && !metadata.ExperimentalAPI {
		return nil, unsupportedCodexVersion("experimental_api_not_supported", map[string]any{"codex_version": version})
	}
	if !isFile(filepath.Join(schemaDir, "schema.json")) {
		return nil, unsupportedCodexVersion("missing_schema_fixture", map[string]any{"codex_version": version})
	}
	transcriptPath := filepath.Join(transcriptDir, "happy-path.jsonl")
	if !isFile(transcriptPath) {
		return nil, unsupportedCodexVersion("missing_transcript_fixture", map[string]any{"codex_version": version})
	}
	selected := &SelectedFixture{
		Version:       version,
		SchemaDir:     schemaDir,
		TranscriptDir: transcriptDir,
		Metadata:      metadata,
	}
	if err := ValidateTranscriptFixture(selected, "happy-path.jsonl"); err != nil {
		return nil, unsupportedCodexVersion("invalid_transcript_fixture", map[string]any{"codex_version": version, "error": err.Error()})
	}

	return selected, nil
}

func PreflightFixtureGate() error {
	_, err := SelectFixtureMetadata(GateOptions{VersionOutput: DetectVersion()})
	return err
}

func ValidateHandshakeMetadata(expected CompatibilityMetadata, actual HandshakeMetadata) error {
	if expected.CodexVersion != actual.CodexVersion {
		return codexProtocolError("codex_version_mismatch", expected, actual)
	}
	if expected.ProtocolVersion != actual.ProtocolVersion {
		return codexProtocolError("protocol_version_mismatch", expected, actual)
	}
	if expected.SchemaVersion != actual.SchemaVersion {
		return codexProtocolError("schema_version_mismatch", expected, actual)
	}
	if expected.ExperimentalAPI != actual.ExperimentalAPI {
		return codexProtocolError("experimental_api_mismatch", expected, actual)
	}
	return nil
}

func ValidateTranscriptFixture(selected *SelectedFixture, name string) error {
	if selected == nil {
		return fmt.Errorf("selected fixture is required")
	}
	path := filepath.Join(selected.TranscriptDir, name)
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), maxProtocolLineBytes)
	lineNo := 0
	handshakeSeen := false
	terminalSeen := false
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var msg protocolMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			return fmt.Errorf("line %d: %w", lineNo, err)
		}
		if !validProtocolMessageType(msg.Type) {
			return fmt.Errorf("line %d: unsupported message type %q", lineNo, msg.Type)
		}
		if msg.Type == "handshake" {
			if err := ValidateHandshakeMetadata(selected.Metadata, HandshakeMetadata{
				CodexVersion:    msg.CodexVersion,
				ProtocolVersion: msg.ProtocolVersion,
				SchemaVersion:   msg.SchemaVersion,
				ExperimentalAPI: msg.ExperimentalAPI,
			}); err != nil {
				return fmt.Errorf("line %d: %w", lineNo, err)
			}
			handshakeSeen = true
		}
		if msg.Type == "turn_completed" || msg.Type == "turn_failed" {
			terminalSeen = true
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if !handshakeSeen {
		return fmt.Errorf("missing handshake")
	}
	if !terminalSeen {
		return fmt.Errorf("missing terminal turn message")
	}
	return nil
}

func validProtocolMessageType(messageType string) bool {
	switch messageType {
	case "handshake", "thread_started", "turn_started", "turn_progress", "handoff", "approval_requested", "approval_resolved", "tool_call", "turn_completed", "turn_failed":
		return true
	default:
		return false
	}
}

func scanStdout(stdout io.Reader, lines chan<- stdoutItem) {
	defer close(lines)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), maxProtocolLineBytes)
	for scanner.Scan() {
		lines <- stdoutItem{line: scanner.Text()}
	}
	if err := scanner.Err(); err != nil && !strings.Contains(err.Error(), "file already closed") {
		lines <- stdoutItem{err: err}
	}
}

func finishAfterTerminate(req agent.RunRequest, cmd *exec.Cmd, waitCh <-chan error, stderrCounter *byteCounter, code core.FailureCode, message string) (agent.RunResult, error) {
	terminateProcessGroup(cmd.Process)
	waitErr := waitForProcess(waitCh, cmd.Process)
	emitProcessExited(req, waitErr, stderrCounter)
	return failed(code, message), nil
}

func waitForProcess(waitCh <-chan error, process *os.Process) error {
	select {
	case err := <-waitCh:
		return err
	case <-time.After(2 * time.Second):
		killProcessGroup(process)
		return <-waitCh
	}
}

func terminateProcessGroup(process *os.Process) {
	if process == nil {
		return
	}
	if pgid, err := syscall.Getpgid(process.Pid); err == nil {
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
		return
	}
	_ = process.Signal(syscall.SIGTERM)
}

func killProcessGroup(process *os.Process) {
	if process == nil {
		return
	}
	if pgid, err := syscall.Getpgid(process.Pid); err == nil {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		return
	}
	_ = process.Kill()
}

func emit(req agent.RunRequest, eventType string, data map[string]any) {
	if req.EmitEvent == nil {
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	_ = req.EmitEvent(eventType, data)
}

func emitProcessExited(req agent.RunRequest, err error, stderrCounter *byteCounter) {
	exitCode := 0
	if err != nil {
		exitCode = 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}
	emit(req, "agent.process_exited", map[string]any{"exit_code": exitCode, "stderr_bytes": stderrCounter.Count(), "stderr_redacted": true})
}

func failed(code core.FailureCode, message string) agent.RunResult {
	return agent.RunResult{Kind: agent.RunResultFailed, FailureCode: code, FailureMessage: message}
}

func commandParts(command string) []string {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}

	parts := []string{}
	var current strings.Builder
	inSingleQuote := false
	inDoubleQuote := false
	escaped := false
	tokenStarted := false

	flush := func() {
		if !tokenStarted {
			return
		}
		parts = append(parts, current.String())
		current.Reset()
		tokenStarted = false
	}

	for _, r := range command {
		if escaped {
			current.WriteRune(r)
			tokenStarted = true
			escaped = false
			continue
		}
		if inSingleQuote {
			if r == '\'' {
				inSingleQuote = false
				continue
			}
			current.WriteRune(r)
			tokenStarted = true
			continue
		}
		if inDoubleQuote {
			switch r {
			case '"':
				inDoubleQuote = false
			case '\\':
				escaped = true
			default:
				current.WriteRune(r)
				tokenStarted = true
			}
			continue
		}
		switch {
		case unicode.IsSpace(r):
			flush()
		case r == '\'':
			inSingleQuote = true
			tokenStarted = true
		case r == '"':
			inDoubleQuote = true
			tokenStarted = true
		case r == '\\':
			escaped = true
			tokenStarted = true
		default:
			current.WriteRune(r)
			tokenStarted = true
		}
	}
	if escaped {
		current.WriteRune('\\')
	}
	flush()
	return parts
}

func codexEnv(req agent.RunRequest) []string {
	env := minimalHostEnv()
	if req.ProjectID != "" {
		env = append(env, "SYMPHONY_PROJECT_ID="+req.ProjectID)
	}
	if req.Run != nil {
		env = append(env, "SYMPHONY_RUN_ID="+req.Run.ID)
	}
	if req.Issue != nil {
		env = append(env, "SYMPHONY_ISSUE_ID="+req.Issue.ID, "SYMPHONY_ISSUE_IDENTIFIER="+req.Issue.Identifier)
	}
	if req.Workspace != nil {
		env = append(env, "SYMPHONY_WORKSPACE_PATH="+req.Workspace.Path)
	}
	if req.ToolEndpoint != "" {
		env = append(env, "SYMPHONY_TOOL_ENDPOINT="+req.ToolEndpoint)
	}
	if req.ToolToken != "" {
		env = append(env, "SYMPHONY_TOOL_TOKEN="+req.ToolToken)
	}
	return env
}

func minimalHostEnv() []string {
	keys := []string{"PATH", "HOME", "TMPDIR", "TMP", "TEMP", "SHELL", "USER", "LOGNAME", "LANG", "LC_ALL"}
	env := make([]string, 0, len(keys))
	seen := map[string]bool{}
	for _, key := range keys {
		if seen[key] {
			continue
		}
		seen[key] = true
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func timeoutDuration(ms int, fallback time.Duration) time.Duration {
	if ms <= 0 {
		return fallback
	}
	return time.Duration(ms) * time.Millisecond
}

func resetTimer(timer *time.Timer, d time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(d)
}

func stopTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func workspacePath(req agent.RunRequest) string {
	if req.Workspace == nil {
		return ""
	}
	return req.Workspace.Path
}

func redactedMessage(message string) string {
	return "redacted"
}

type byteCounter struct {
	n int64
}

func (c *byteCounter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return len(p), nil
}

func (c *byteCounter) Count() int64 {
	if c == nil {
		return 0
	}
	return c.n
}

func validateCompatibilityMetadata(version string, metadata CompatibilityMetadata) error {
	switch {
	case metadata.CodexVersion == "":
		return unsupportedCodexVersion("metadata_missing_codex_version", map[string]any{"codex_version": version})
	case metadata.CodexVersion != version:
		return unsupportedCodexVersion("metadata_codex_version_mismatch", map[string]any{"detected_version": version, "metadata_version": metadata.CodexVersion})
	case metadata.ProtocolVersion == "":
		return unsupportedCodexVersion("metadata_missing_protocol_version", map[string]any{"codex_version": version})
	case metadata.SchemaVersion == "":
		return unsupportedCodexVersion("metadata_missing_schema_version", map[string]any{"codex_version": version})
	case len(metadata.SupportedNotifications) == 0:
		return unsupportedCodexVersion("metadata_missing_supported_notifications", map[string]any{"codex_version": version})
	case len(metadata.SupportedRequests) == 0:
		return unsupportedCodexVersion("metadata_missing_supported_requests", map[string]any{"codex_version": version})
	default:
		return nil
	}
}

func defaultFixtureRoot() string {
	if root := strings.TrimSpace(os.Getenv("SYMPHONY_CODEX_FIXTURE_ROOT")); root != "" {
		return root
	}
	candidates := deployableFixtureRootCandidates()
	for _, candidate := range candidates {
		if isDir(candidate) {
			return candidate
		}
	}
	if repoFixtureRoot := findRepoFixtureRoot(); repoFixtureRoot != "" {
		return repoFixtureRoot
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return filepath.Join(os.TempDir(), "local-symphony-missing-codex-fixtures")
}

func deployableFixtureRootCandidates() []string {
	exePath, err := os.Executable()
	if err != nil {
		return nil
	}
	exeDir := filepath.Dir(exePath)
	return []string{
		filepath.Join(exeDir, "fixtures", "codex"),
		filepath.Join(exeDir, "..", "share", "local-symphony", "fixtures", "codex"),
		filepath.Join(exeDir, "..", "fixtures", "codex"),
	}
}

func findRepoFixtureRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		candidate := filepath.Join(wd, "internal", "agent", "codex", "testdata")
		if isLocalSymphonyRepoRoot(wd) && isDir(candidate) {
			return candidate
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			return ""
		}
		wd = parent
	}
}

func isLocalSymphonyRepoRoot(path string) bool {
	data, err := os.ReadFile(filepath.Join(path, "go.mod"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "module" {
			return fields[1] == "local-symphony"
		}
	}
	return false
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func unsupportedCodexVersion(reason string, details map[string]any) *core.APIError {
	if details == nil {
		details = map[string]any{}
	}
	details["reason"] = reason
	return core.NewError(
		core.APIErrorCode(core.FailureUnsupportedCodexVersion),
		"unsupported Codex version: "+reason,
		details,
	)
}

func codexProtocolError(reason string, expected CompatibilityMetadata, actual HandshakeMetadata) *core.APIError {
	return core.NewError(
		core.APIErrorCode(core.FailureCodexProtocolError),
		"Codex handshake metadata mismatch: "+reason,
		map[string]any{
			"reason": reason,
			"expected": map[string]any{
				"codex_version":    expected.CodexVersion,
				"protocol_version": expected.ProtocolVersion,
				"schema_version":   expected.SchemaVersion,
				"experimental_api": expected.ExperimentalAPI,
			},
			"actual": map[string]any{
				"codex_version":    actual.CodexVersion,
				"protocol_version": actual.ProtocolVersion,
				"schema_version":   actual.SchemaVersion,
				"experimental_api": actual.ExperimentalAPI,
			},
		},
	)
}
