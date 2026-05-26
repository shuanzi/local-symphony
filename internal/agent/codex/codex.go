package codex

import (
	"bufio"
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
	"local-symphony/internal/toolgateway"
)

const (
	compatibilityMetadataFile = "compatibility.json"
	maxProtocolLineBytes      = 16 * 1024 * 1024
)

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
	cmd := exec.Command(parts[0], "--version")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

type protocolMessage struct {
	Type            string         `json:"type"`
	CodexVersion    string         `json:"codex_version"`
	ProtocolVersion string         `json:"protocol_version"`
	SchemaVersion   string         `json:"schema_version"`
	ExperimentalAPI bool           `json:"experimental_api"`
	ThreadID        string         `json:"thread_id"`
	TurnID          string         `json:"turn_id"`
	Message         string         `json:"message"`
	FailureCode     string         `json:"failure_code"`
	Error           string         `json:"error"`
	Payload         map[string]any `json:"payload"`
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
			result, terminal, err := handleProtocolLine(req, session.selected, item.line, &session.handshakeDone, &startupC, &handoffReceived, &session.threadID)
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

func handleProtocolLine(req agent.RunRequest, selected *SelectedFixture, line string, handshakeDone *bool, startupC *<-chan time.Time, handoffReceived *bool, threadID *string) (agent.RunResult, bool, error) {
	var msg protocolMessage
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		return agent.RunResult{}, false, err
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
