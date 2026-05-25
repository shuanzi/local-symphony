package codex

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-symphony/internal/agent"
	"local-symphony/internal/core"
	"local-symphony/internal/store"
	"local-symphony/internal/toolgateway"
)

func TestRunnerImplementsAgentRunner(t *testing.T) {
	var _ agent.Runner = &Runner{}
	var _ agent.ClosableRunner = &Runner{}
}

func TestIntegrationPreflightFixtureGate(t *testing.T) {
	if os.Getenv("SYMPHONY_TEST_CODEX") != "1" {
		t.Skip("set SYMPHONY_TEST_CODEX=1 to run against the installed Codex binary")
	}
	if err := PreflightFixtureGate(); err != nil {
		apiErr := core.AsAPIError(err)
		if apiErr.Code == core.APIErrorCode(core.FailureUnsupportedCodexVersion) {
			t.Skipf("installed Codex is not covered by committed fixtures: %v", err)
		}
		t.Fatalf("PreflightFixtureGate: %v", err)
	}
}

func TestPreflightFixtureGateSupportedVersion(t *testing.T) {
	selected, err := SelectFixtureMetadata(GateOptions{
		VersionOutput: "codex 0.0.0-test\n",
		FixtureRoot:   filepath.Join("testdata"),
	})
	if err != nil {
		t.Fatalf("SelectFixtureMetadata() error = %v", err)
	}

	if selected.Metadata.CodexVersion != "0.0.0-test" {
		t.Fatalf("CodexVersion = %q, want 0.0.0-test", selected.Metadata.CodexVersion)
	}
	if selected.Metadata.ProtocolVersion != "protocol-test-v1" {
		t.Fatalf("ProtocolVersion = %q, want protocol-test-v1", selected.Metadata.ProtocolVersion)
	}
	if len(selected.Metadata.SupportedNotifications) == 0 {
		t.Fatal("SupportedNotifications is empty")
	}
	if len(selected.Metadata.SupportedRequests) == 0 {
		t.Fatal("SupportedRequests is empty")
	}
	if selected.SchemaDir == "" || selected.TranscriptDir == "" {
		t.Fatalf("fixture directories were not selected: %#v", selected)
	}
}

func TestPreflightFixtureGateMissingFixtureFailsUnsupported(t *testing.T) {
	_, err := SelectFixtureMetadata(GateOptions{
		VersionOutput: "codex 9.9.9\n",
		FixtureRoot:   t.TempDir(),
	})
	requireAPIErrorCode(t, err, core.APIErrorCode(core.FailureUnsupportedCodexVersion))
}

func TestPreflightFixtureGateMalformedVersionFailsUnsupported(t *testing.T) {
	_, err := SelectFixtureMetadata(GateOptions{
		VersionOutput: "codex version bananas\n",
		FixtureRoot:   filepath.Join("testdata"),
	})
	requireAPIErrorCode(t, err, core.APIErrorCode(core.FailureUnsupportedCodexVersion))
}

func TestPreflightFixtureGateMetadataVersionDriftFailsUnsupported(t *testing.T) {
	root := t.TempDir()
	writeTestFixture(t, root, "1.2.3", `{
		"codex_version": "1.2.4",
		"protocol_version": "protocol-test-v1",
		"schema_version": "schema-test-v1",
		"supported_notifications": ["codex.initialized"],
		"supported_requests": ["initialize"],
		"experimental_api": false
	}`)

	_, err := SelectFixtureMetadata(GateOptions{
		VersionOutput: "codex 1.2.3\n",
		FixtureRoot:   root,
	})
	requireAPIErrorCode(t, err, core.APIErrorCode(core.FailureUnsupportedCodexVersion))
}

func TestPreflightFixtureGateExperimentalAPIFailsWhenFixtureDoesNotSupportIt(t *testing.T) {
	_, err := SelectFixtureMetadata(GateOptions{
		VersionOutput:   "codex 0.0.0-test\n",
		FixtureRoot:     filepath.Join("testdata"),
		ExperimentalAPI: true,
	})
	requireAPIErrorCode(t, err, core.APIErrorCode(core.FailureUnsupportedCodexVersion))
}

func TestValidateHandshakeMetadata(t *testing.T) {
	selected, err := SelectFixtureMetadata(GateOptions{
		VersionOutput: "codex 0.0.0-test\n",
		FixtureRoot:   filepath.Join("testdata"),
	})
	if err != nil {
		t.Fatalf("SelectFixtureMetadata() error = %v", err)
	}

	err = ValidateHandshakeMetadata(selected.Metadata, HandshakeMetadata{
		CodexVersion:    "0.0.0-test",
		ProtocolVersion: "protocol-test-v1",
		SchemaVersion:   "schema-test-v1",
		ExperimentalAPI: false,
	})
	if err != nil {
		t.Fatalf("ValidateHandshakeMetadata() error = %v", err)
	}

	err = ValidateHandshakeMetadata(selected.Metadata, HandshakeMetadata{
		CodexVersion:    "0.0.0-test",
		ProtocolVersion: "protocol-drift",
		SchemaVersion:   "schema-test-v1",
		ExperimentalAPI: false,
	})
	requireAPIErrorCode(t, err, core.APIErrorCode(core.FailureCodexProtocolError))
}

func TestRunnerLaunchesFixtureProcessAndEmitsNormalizedEvents(t *testing.T) {
	st, run, issue, workspacePath, token := newCodexRunnerFixture(t)
	script := writeFakeCodexBinary(t, `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "codex 0.0.0-test"
  exit 0
fi
if [ "$1" != "app-server" ]; then
  echo "unexpected command: $*" >&2
  exit 2
fi
[ -n "$SYMPHONY_PROJECT_ID" ] || exit 3
[ "$SYMPHONY_RUN_ID" = "`+run.ID+`" ] || exit 4
[ "$SYMPHONY_ISSUE_ID" = "`+issue.ID+`" ] || exit 5
[ "$SYMPHONY_ISSUE_IDENTIFIER" = "`+issue.Identifier+`" ] || exit 6
[ -n "$SYMPHONY_TOOL_TOKEN" ] || exit 8
pwd > "$SYMPHONY_WORKSPACE_PATH/cwd"
echo raw-stderr-should-not-leak >&2
read start_turn
printf '%s\n' "$start_turn" > "$SYMPHONY_WORKSPACE_PATH/start-turn.json"
printf '%s\n' '{"type":"handshake","codex_version":"0.0.0-test","protocol_version":"protocol-test-v1","schema_version":"schema-test-v1","experimental_api":false}'
printf '%s\n' '{"type":"thread_started","thread_id":"thread_fixture"}'
printf '%s\n' '{"type":"turn_started","turn_id":"turn_fixture"}'
printf '%s\n' '{"type":"turn_progress","message":"SECRET_PROGRESS_SHOULD_NOT_LEAK","raw_payload":"must_not_be_stored"}'
printf '%s\n' '{"type":"approval_requested","payload":{"kind":"command","raw":"must_not_be_stored"}}'
printf '%s\n' '{"type":"approval_resolved","payload":{"decision":"approve_once","raw":"must_not_be_stored"}}'
printf '%s\n' '{"type":"tool_call","payload":{"tool":"issue.get","raw":"must_not_be_stored"}}'
printf '%s\n' '{"type":"handoff","payload":{"summary":"Codex fixture completed.","changed_files":[],"tests":[],"risks":[],"verification":[],"followups":[],"target_state":"Human Review"}}'
printf '%s\n' '{"type":"turn_completed"}'
`)

	result, err := (&Runner{Command: script + " app-server"}).Run(context.Background(), agent.RunRequest{
		Run:       run,
		Issue:     issue,
		Workspace: &core.WorkspaceSummary{Path: workspacePath},
		ProjectID: st.ProjectID,
		Prompt:    "REAL_PROMPT_SHOULD_REACH_CODEX",
		ToolToken: token,
		Timeouts:  agent.TimeoutPolicy{StartupMS: 1000, TurnMS: 1000, StallMS: 1000},
		Gateway:   toolgateway.Gateway{Store: st},
		EmitEvent: func(eventType string, data map[string]any) error {
			return st.AppendEvent(eventType, "codex", &issue.ID, &run.ID, data)
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Kind != agent.RunResultSucceeded {
		t.Fatalf("result = %#v, want %s", result, agent.RunResultSucceeded)
	}
	assertWorkspaceCWD(t, workspacePath)
	assertStartTurn(t, filepath.Join(workspacePath, "start-turn.json"), "REAL_PROMPT_SHOULD_REACH_CODEX", false, "")
	if _, err := st.GetHandoffByRun(run.ID); err != nil {
		t.Fatalf("GetHandoffByRun: %v", err)
	}
	assertCodexEvents(t, st, run.ID, []string{
		"agent.process_started",
		"agent.handshake_completed",
		"agent.thread_started",
		"agent.turn_started",
		"agent.turn_progress",
		"agent.tool_call_observed",
		"agent.turn_completed",
		"agent.process_exited",
	})
	assertRunEventCount(t, st, run.ID, "approval.requested", 1)
	assertRunEventCount(t, st, run.ID, "approval.resolved", 1)
	rows, err := st.Project.Query(`SELECT data_json FROM run_events WHERE run_id=?`, run.ID)
	if err != nil {
		t.Fatalf("query events: %v", err)
	}
	for _, row := range rows {
		data := row["data_json"].String()
		if strings.Contains(data, "must_not_be_stored") || strings.Contains(data, "raw-stderr-should-not-leak") || strings.Contains(data, "SECRET_PROGRESS_SHOULD_NOT_LEAK") {
			t.Fatalf("raw Codex payload leaked into event data: %s", data)
		}
	}
}

func TestRunnerTurnCompletedWithoutHandoffReturnsMissingHandoff(t *testing.T) {
	_, run, issue, workspacePath, token := newCodexRunnerFixture(t)
	script := writeFakeCodexBinary(t, `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "codex 0.0.0-test"
  exit 0
fi
read start_turn
printf '%s\n' '{"type":"handshake","codex_version":"0.0.0-test","protocol_version":"protocol-test-v1","schema_version":"schema-test-v1","experimental_api":false}'
printf '%s\n' '{"type":"thread_started","thread_id":"thread_fixture"}'
printf '%s\n' '{"type":"turn_started","turn_id":"turn_fixture"}'
printf '%s\n' '{"type":"turn_completed"}'
while true; do sleep 1; done
`)
	runner := &Runner{Command: script + " app-server"}
	defer runner.Close(context.Background(), agent.RunRequest{})

	result, err := runner.Run(context.Background(), agent.RunRequest{
		Run:       run,
		Issue:     issue,
		Workspace: &core.WorkspaceSummary{Path: workspacePath},
		ToolToken: token,
		Timeouts:  agent.TimeoutPolicy{StartupMS: 1000, TurnMS: 1000, StallMS: 1000},
		Gateway:   toolgateway.Gateway{Store: nil},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Kind != agent.RunResultMissingHandoff || result.FailureCode != core.FailureMissingHandoff {
		t.Fatalf("result = %#v, want missing handoff", result)
	}
}

func TestRunnerContinuationReusesProcessThreadAndPrompt(t *testing.T) {
	st, run, issue, workspacePath, token := newCodexRunnerFixture(t)
	script := writeFakeCodexBinary(t, `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "codex 0.0.0-test"
  exit 0
fi
read first_turn
printf '%s\n' "$first_turn" > "$SYMPHONY_WORKSPACE_PATH/turn-1.json"
printf '%s\n' '{"type":"handshake","codex_version":"0.0.0-test","protocol_version":"protocol-test-v1","schema_version":"schema-test-v1","experimental_api":false}'
printf '%s\n' '{"type":"thread_started","thread_id":"thread_fixture"}'
printf '%s\n' '{"type":"turn_started","turn_id":"turn_1"}'
printf '%s\n' '{"type":"turn_completed"}'
read second_turn
printf '%s\n' "$second_turn" > "$SYMPHONY_WORKSPACE_PATH/turn-2.json"
printf '%s\n' '{"type":"turn_started","turn_id":"turn_2"}'
printf '%s\n' '{"type":"handoff","payload":{"summary":"Continuation handoff.","changed_files":[],"tests":[],"risks":[],"verification":[],"followups":[],"target_state":"Human Review"}}'
printf '%s\n' '{"type":"turn_completed"}'
while true; do sleep 1; done
`)
	runner := &Runner{Command: script + " app-server"}

	baseReq := agent.RunRequest{
		Run:       run,
		Issue:     issue,
		Workspace: &core.WorkspaceSummary{Path: workspacePath},
		ProjectID: st.ProjectID,
		Prompt:    "FIRST_PROMPT",
		ToolToken: token,
		Timeouts:  agent.TimeoutPolicy{StartupMS: 1000, TurnMS: 1000, StallMS: 1000},
		Gateway:   toolgateway.Gateway{Store: st},
	}
	result, err := runner.Run(context.Background(), baseReq)
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if result.Kind != agent.RunResultMissingHandoff {
		t.Fatalf("first result = %#v, want missing handoff", result)
	}
	baseReq.Prompt = "CONTINUATION_PROMPT"
	baseReq.IsContinuation = true
	result, err = runner.Run(context.Background(), baseReq)
	if err != nil {
		t.Fatalf("continuation Run: %v", err)
	}
	if result.Kind != agent.RunResultSucceeded {
		t.Fatalf("continuation result = %#v, want success", result)
	}
	assertStartTurn(t, filepath.Join(workspacePath, "turn-1.json"), "FIRST_PROMPT", false, "")
	assertStartTurn(t, filepath.Join(workspacePath, "turn-2.json"), "CONTINUATION_PROMPT", true, "thread_fixture")
}

func TestRunnerTurnTimeoutFails(t *testing.T) {
	_, run, issue, workspacePath, token := newCodexRunnerFixture(t)
	script := writeFakeCodexBinary(t, `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "codex 0.0.0-test"
  exit 0
fi
printf '%s\n' '{"type":"handshake","codex_version":"0.0.0-test","protocol_version":"protocol-test-v1","schema_version":"schema-test-v1","experimental_api":false}'
sleep 2
`)

	result, err := (&Runner{Command: script + " app-server"}).Run(context.Background(), agent.RunRequest{
		Run:       run,
		Issue:     issue,
		Workspace: &core.WorkspaceSummary{Path: workspacePath},
		ToolToken: token,
		Timeouts:  agent.TimeoutPolicy{StartupMS: 1000, TurnMS: 25, StallMS: 1000},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Kind != agent.RunResultFailed || result.FailureCode != core.FailureTurnTimeout {
		t.Fatalf("result = %#v, want turn timeout", result)
	}
}

func TestHandleProtocolLineRejectsHandoffBeforeHandshake(t *testing.T) {
	req := agent.RunRequest{
		Gateway: toolgateway.Gateway{Store: nil},
	}
	handshakeDone := false
	var startupC <-chan time.Time
	handoffReceived := false
	threadID := ""

	_, done, err := handleProtocolLine(req, nil, `{"type":"handoff","payload":{"summary":"x"}}`, &handshakeDone, &startupC, &handoffReceived, &threadID)
	if err == nil || !strings.Contains(err.Error(), "handoff before handshake") {
		t.Fatalf("err = %v, want handoff before handshake", err)
	}
	if done {
		t.Fatal("done = true, want false")
	}
	if handoffReceived {
		t.Fatal("handoffReceived = true, want false")
	}
}

func TestHandleProtocolLineRejectsTurnCompletedBeforeHandshake(t *testing.T) {
	req := agent.RunRequest{}
	handshakeDone := false
	var startupC <-chan time.Time
	handoffReceived := true
	threadID := ""

	_, done, err := handleProtocolLine(req, nil, `{"type":"turn_completed"}`, &handshakeDone, &startupC, &handoffReceived, &threadID)
	if err == nil || !strings.Contains(err.Error(), "turn completed before handshake") {
		t.Fatalf("err = %v, want turn completed before handshake", err)
	}
	if done {
		t.Fatal("done = true, want false")
	}
}

func TestRunnerStallTimeoutFails(t *testing.T) {
	_, run, issue, workspacePath, token := newCodexRunnerFixture(t)
	script := writeFakeCodexBinary(t, `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "codex 0.0.0-test"
  exit 0
fi
printf '%s\n' '{"type":"handshake","codex_version":"0.0.0-test","protocol_version":"protocol-test-v1","schema_version":"schema-test-v1","experimental_api":false}'
sleep 2
`)

	result, err := (&Runner{Command: script + " app-server"}).Run(context.Background(), agent.RunRequest{
		Run:       run,
		Issue:     issue,
		Workspace: &core.WorkspaceSummary{Path: workspacePath},
		ToolToken: token,
		Timeouts:  agent.TimeoutPolicy{StartupMS: 1000, TurnMS: 1000, StallMS: 25},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Kind != agent.RunResultFailed || result.FailureCode != core.FailureStallTimeout {
		t.Fatalf("result = %#v, want stall timeout", result)
	}
}

func TestRunnerStartupTimeoutFails(t *testing.T) {
	_, run, issue, workspacePath, token := newCodexRunnerFixture(t)
	script := writeFakeCodexBinary(t, `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "codex 0.0.0-test"
  exit 0
fi
sleep 2
`)

	result, err := (&Runner{Command: script + " app-server"}).Run(context.Background(), agent.RunRequest{
		Run:       run,
		Issue:     issue,
		Workspace: &core.WorkspaceSummary{Path: workspacePath},
		ToolToken: token,
		Timeouts:  agent.TimeoutPolicy{StartupMS: 25, TurnMS: 1000, StallMS: 1000},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Kind != agent.RunResultFailed || result.FailureCode != core.FailureCodexStartupFailed {
		t.Fatalf("result = %#v, want startup failure", result)
	}
}

func TestRunnerHandshakeMismatchFailsProtocolError(t *testing.T) {
	_, run, issue, workspacePath, token := newCodexRunnerFixture(t)
	script := writeFakeCodexBinary(t, `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "codex 0.0.0-test"
  exit 0
fi
printf '%s\n' '{"type":"handshake","codex_version":"0.0.0-test","protocol_version":"wrong-protocol","schema_version":"schema-test-v1","experimental_api":false}'
`)

	result, err := (&Runner{Command: script + " app-server"}).Run(context.Background(), agent.RunRequest{
		Run:       run,
		Issue:     issue,
		Workspace: &core.WorkspaceSummary{Path: workspacePath},
		ToolToken: token,
		Timeouts:  agent.TimeoutPolicy{StartupMS: 1000, TurnMS: 1000, StallMS: 1000},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Kind != agent.RunResultFailed || result.FailureCode != core.FailureCodexProtocolError {
		t.Fatalf("result = %#v, want protocol failure", result)
	}
}

func assertWorkspaceCWD(t *testing.T, workspacePath string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(workspacePath, "cwd"))
	if err != nil {
		t.Fatalf("read cwd marker: %v", err)
	}
	actual, err := filepath.EvalSymlinks(strings.TrimSpace(string(got)))
	if err != nil {
		t.Fatalf("EvalSymlinks actual cwd: %v", err)
	}
	expected, err := filepath.EvalSymlinks(workspacePath)
	if err != nil {
		t.Fatalf("EvalSymlinks workspace: %v", err)
	}
	if actual != expected {
		t.Fatalf("process cwd = %q, want %q", actual, expected)
	}
}

func assertStartTurn(t *testing.T, path, wantPrompt string, wantContinuation bool, wantThreadID string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read start turn %s: %v", path, err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode start turn %s: %v", path, err)
	}
	if got["prompt"] != wantPrompt {
		t.Fatalf("prompt = %q, want %q", got["prompt"], wantPrompt)
	}
	if got["continuation"] != wantContinuation {
		t.Fatalf("continuation = %v, want %v", got["continuation"], wantContinuation)
	}
	if wantThreadID == "" {
		if _, ok := got["thread_id"]; ok {
			t.Fatalf("thread_id present = %v, want absent", got["thread_id"])
		}
		return
	}
	if got["thread_id"] != wantThreadID {
		t.Fatalf("thread_id = %q, want %q", got["thread_id"], wantThreadID)
	}
}

func requireAPIErrorCode(t *testing.T, err error, code core.APIErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %s", code)
	}
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error type = %T, want *core.APIError", err)
	}
	if apiErr.Code != code {
		t.Fatalf("error code = %s, want %s", apiErr.Code, code)
	}
}

func newCodexRunnerFixture(t *testing.T) (*store.Store, *core.RunAttempt, *core.Issue, string, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	st, err := store.InitProject(t.TempDir(), "TST")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	issue, err := st.CreateIssue(store.CreateIssueInput{
		Title:              "Codex runner fixture",
		Description:        "desc",
		AcceptanceCriteria: []string{"done"},
		Priority:           3,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if _, err := st.TransitionIssue(issue.ID, core.StateReady, "", ""); err != nil {
		t.Fatalf("TransitionIssue: %v", err)
	}
	run, err := st.ClaimRun(issue.ID, "manual", "codex", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	if err := st.UpdateRunStatus(run.ID, core.RunStartingAgent, nil); err != nil {
		t.Fatalf("UpdateRunStatus starting: %v", err)
	}
	workspacePath := t.TempDir()
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if _, err := st.CreateOrUpdateWorkspace(issue.ID, workspacePath, "test", "auto", "main", "base-sha"); err != nil {
		t.Fatalf("CreateOrUpdateWorkspace: %v", err)
	}
	token, err := toolgateway.NewTokenForRun(st, run, workspacePath)
	if err != nil {
		t.Fatalf("NewTokenForRun: %v", err)
	}
	if err := st.UpdateRunStatus(run.ID, core.RunRunning, map[string]any{"started_at": core.Now()}); err != nil {
		t.Fatalf("UpdateRunStatus running: %v", err)
	}
	run, err = st.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	return st, run, issue, workspacePath, token
}

func writeFakeCodexBinary(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codex-fixture")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	return path
}

func assertCodexEvents(t *testing.T, st *store.Store, runID string, want []string) {
	t.Helper()
	rows, err := st.Project.Query(`SELECT event_type, actor_type, redacted FROM run_events WHERE run_id=? AND event_type LIKE 'agent.%' ORDER BY seq ASC`, runID)
	if err != nil {
		t.Fatalf("query codex events: %v", err)
	}
	if len(rows) != len(want) {
		t.Fatalf("event count = %d, want %d", len(rows), len(want))
	}
	for i, eventType := range want {
		if got := rows[i]["event_type"].String(); got != eventType {
			t.Fatalf("event %d type = %s, want %s", i, got, eventType)
		}
		if got := rows[i]["actor_type"].String(); got != "codex" {
			t.Fatalf("event %d actor = %s, want codex", i, got)
		}
		if !rows[i]["redacted"].Bool() {
			t.Fatalf("event %d redacted = false, want true", i)
		}
	}
}

func assertRunEventCount(t *testing.T, st *store.Store, runID, eventType string, want int) {
	t.Helper()
	rows, err := st.Project.Query(`SELECT id FROM run_events WHERE run_id=? AND event_type=?`, runID, eventType)
	if err != nil {
		t.Fatalf("query run events: %v", err)
	}
	if len(rows) != want {
		t.Fatalf("%s event count = %d, want %d", eventType, len(rows), want)
	}
}

func writeTestFixture(t *testing.T, root, version, metadata string) {
	t.Helper()
	schemaDir := filepath.Join(root, "schema", version)
	transcriptDir := filepath.Join(root, "transcripts", version)
	if err := os.MkdirAll(schemaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(transcriptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(schemaDir, "compatibility.json"), []byte(metadata), 0o644); err != nil {
		t.Fatal(err)
	}
}
