package codex

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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
printf '%s\n' '{"type":"approval_requested","payload":{"kind":"command","action_summary":"Run fixture command","risk_level":"low","policy_match":"fixture.review","request_id":"codex_req_fixture","timeout_ms":5000,"raw":"must_not_be_stored"}}'
read approval_decision
printf '%s\n' "$approval_decision" > "$SYMPHONY_WORKSPACE_PATH/approval-fixture.json"
printf '%s\n' '{"type":"tool_call","payload":{"tool":"issue.get","raw":"must_not_be_stored"}}'
printf '%s\n' '{"type":"handoff","payload":{"summary":"Codex fixture completed.","changed_files":[],"tests":[],"risks":[],"verification":[],"followups":[],"target_state":"Human Review"}}'
printf '%s\n' '{"type":"turn_completed"}'
`)

	runner := &Runner{Command: script + " app-server"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resultC := make(chan agent.RunResult, 1)
	errC := make(chan error, 1)
	go func() {
		result, err := runner.Run(ctx, codexTestRunRequest(st, run, issue, workspacePath, token))
		resultC <- result
		errC <- err
	}()
	approval := waitForApprovalByRequestID(t, st, "codex_req_fixture")
	if err := st.DecideApproval(approval.ID, "approved_once", "fixture approved"); err != nil {
		t.Fatalf("DecideApproval: %v", err)
	}
	result := waitCodexResult(t, resultC, errC)
	if result.Kind != agent.RunResultSucceeded {
		t.Fatalf("result = %#v, want %s", result, agent.RunResultSucceeded)
	}
	assertWorkspaceCWD(t, workspacePath)
	assertStartTurn(t, filepath.Join(workspacePath, "start-turn.json"), "REAL_PROMPT_SHOULD_REACH_CODEX", false, "")
	assertApprovalDecisionFile(t, filepath.Join(workspacePath, "approval-fixture.json"), "codex_req_fixture", "approve_once")
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

func TestRunnerWritesApprovalDecisionAndCompletesHandoff(t *testing.T) {
	st, run, issue, workspacePath, token := newCodexRunnerFixture(t)
	script := writeFakeCodexBinary(t, `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "codex 0.0.0-test"
  exit 0
fi
read start_turn
printf '%s\n' '{"type":"handshake","codex_version":"0.0.0-test","protocol_version":"protocol-test-v1","schema_version":"schema-test-v1","experimental_api":false}'
printf '%s\n' '{"type":"thread_started","thread_id":"thread_fixture"}'
printf '%s\n' '{"type":"turn_started","turn_id":"turn_fixture"}'
printf '%s\n' '{"type":"approval_requested","payload":{"kind":"command","action_summary":"Run go test","risk_level":"medium","policy_match":"command.review","request_id":"codex_req_approve","timeout_ms":5000,"raw":"must_not_be_stored"}}'
read approval_decision
printf '%s\n' "$approval_decision" > "$SYMPHONY_WORKSPACE_PATH/approval-decision.json"
printf '%s\n' '{"type":"handoff","payload":{"summary":"Approved command completed.","changed_files":[],"tests":[],"risks":[],"verification":[],"followups":[],"target_state":"Human Review"}}'
printf '%s\n' '{"type":"turn_completed"}'
`)
	runner := &Runner{Command: script + " app-server"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resultC := make(chan agent.RunResult, 1)
	errC := make(chan error, 1)
	go func() {
		result, err := runner.Run(ctx, codexTestRunRequest(st, run, issue, workspacePath, token))
		resultC <- result
		errC <- err
	}()

	approval := waitForApprovalByRequestID(t, st, "codex_req_approve")
	if approval.Kind != "command" || approval.ActionSummary != "Run go test" || approval.RiskLevel != "medium" || approval.PolicyMatch != "command.review" {
		t.Fatalf("approval = %#v", approval)
	}
	assertApprovalRequestJSONDoesNotContain(t, st, approval.ID, "must_not_be_stored")
	if err := st.DecideApproval(approval.ID, "approved_once", "ok once"); err != nil {
		t.Fatalf("DecideApproval: %v", err)
	}
	result := waitCodexResult(t, resultC, errC)
	if result.Kind != agent.RunResultSucceeded {
		t.Fatalf("result = %#v, want success", result)
	}
	assertApprovalDecisionFile(t, filepath.Join(workspacePath, "approval-decision.json"), "codex_req_approve", "approve_once")
	assertRunEventCount(t, st, run.ID, "approval.requested", 1)
}

func TestRunnerWritesDenyAndUsesCodexFailureCode(t *testing.T) {
	st, run, issue, workspacePath, token := newCodexRunnerFixture(t)
	script := writeFakeCodexBinary(t, `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "codex 0.0.0-test"
  exit 0
fi
read start_turn
printf '%s\n' '{"type":"handshake","codex_version":"0.0.0-test","protocol_version":"protocol-test-v1","schema_version":"schema-test-v1","experimental_api":false}'
printf '%s\n' '{"type":"thread_started","thread_id":"thread_fixture"}'
printf '%s\n' '{"type":"turn_started","turn_id":"turn_fixture"}'
printf '%s\n' '{"type":"approval_requested","payload":{"kind":"command","action_summary":"Run unsafe command","risk_level":"high","policy_match":"command.deny","request_id":"codex_req_deny","timeout_ms":5000}}'
read approval_decision
printf '%s\n' "$approval_decision" > "$SYMPHONY_WORKSPACE_PATH/approval-deny.json"
printf '%s\n' '{"type":"turn_failed","failure_code":"command_denied"}'
`)
	runner := &Runner{Command: script + " app-server"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resultC := make(chan agent.RunResult, 1)
	errC := make(chan error, 1)
	go func() {
		result, err := runner.Run(ctx, codexTestRunRequest(st, run, issue, workspacePath, token))
		resultC <- result
		errC <- err
	}()

	approval := waitForApprovalByRequestID(t, st, "codex_req_deny")
	if err := st.DecideApproval(approval.ID, "denied", "too risky"); err != nil {
		t.Fatalf("DecideApproval: %v", err)
	}
	result := waitCodexResult(t, resultC, errC)
	if result.Kind != agent.RunResultFailed || result.FailureCode != core.FailureCommandDenied {
		t.Fatalf("result = %#v, want command_denied failure", result)
	}
	if result.FailureCode == core.FailureOperatorCancelled {
		t.Fatalf("deny returned operator_cancelled: %#v", result)
	}
	assertApprovalDecisionFile(t, filepath.Join(workspacePath, "approval-deny.json"), "codex_req_deny", "deny")
}

func TestRunnerApprovalTimeoutMarksRowAndFails(t *testing.T) {
	st, run, issue, workspacePath, token := newCodexRunnerFixture(t)
	script := writeFakeCodexBinary(t, `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "codex 0.0.0-test"
  exit 0
fi
read start_turn
printf '%s\n' '{"type":"handshake","codex_version":"0.0.0-test","protocol_version":"protocol-test-v1","schema_version":"schema-test-v1","experimental_api":false}'
printf '%s\n' '{"type":"thread_started","thread_id":"thread_fixture"}'
printf '%s\n' '{"type":"turn_started","turn_id":"turn_fixture"}'
printf '%s\n' '{"type":"approval_requested","payload":{"kind":"network","action_summary":"Access example.invalid","risk_level":"medium","policy_match":"network.review","request_id":"codex_req_timeout","timeout_ms":25}}'
sleep 2
`)

	result, err := (&Runner{Command: script + " app-server"}).Run(context.Background(), codexTestRunRequest(st, run, issue, workspacePath, token))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Kind != agent.RunResultFailed || result.FailureCode != core.FailureApprovalTimeout {
		t.Fatalf("result = %#v, want approval_timeout", result)
	}
	approval := waitForApprovalByRequestID(t, st, "codex_req_timeout")
	if approval.Status != "timeout" || approval.ResolvedAt == nil {
		t.Fatalf("approval after timeout = %#v", approval)
	}
}

func TestRunnerApprovalCancelRunReturnsOperatorCancelledAndTerminatesProcess(t *testing.T) {
	st, run, issue, workspacePath, token := newCodexRunnerFixture(t)
	script := writeFakeCodexBinary(t, `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "codex 0.0.0-test"
  exit 0
fi
read start_turn
trap 'touch "$SYMPHONY_WORKSPACE_PATH/terminated"; exit 0' TERM
printf '%s\n' '{"type":"handshake","codex_version":"0.0.0-test","protocol_version":"protocol-test-v1","schema_version":"schema-test-v1","experimental_api":false}'
printf '%s\n' '{"type":"thread_started","thread_id":"thread_fixture"}'
printf '%s\n' '{"type":"turn_started","turn_id":"turn_fixture"}'
printf '%s\n' '{"type":"approval_requested","payload":{"kind":"command","action_summary":"Run cancellable command","risk_level":"high","policy_match":"command.review","request_id":"codex_req_cancel","timeout_ms":5000}}'
while true; do sleep 1; done
`)
	runner := &Runner{Command: script + " app-server"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resultC := make(chan agent.RunResult, 1)
	errC := make(chan error, 1)
	go func() {
		result, err := runner.Run(ctx, codexTestRunRequest(st, run, issue, workspacePath, token))
		resultC <- result
		errC <- err
	}()

	approval := waitForApprovalByRequestID(t, st, "codex_req_cancel")
	if err := st.DecideApproval(approval.ID, "cancel_run", "operator cancelled approval"); err != nil {
		t.Fatalf("DecideApproval: %v", err)
	}
	result := waitCodexResult(t, resultC, errC)
	if result.Kind != agent.RunResultFailed || result.FailureCode != core.FailureOperatorCancelled {
		t.Fatalf("result = %#v, want operator_cancelled", result)
	}
	if _, err := os.Stat(filepath.Join(workspacePath, "terminated")); err != nil {
		t.Fatalf("codex process was not terminated: %v", err)
	}
}

func TestRunnerApprovalWaitDoesNotTripStallTimeout(t *testing.T) {
	st, run, issue, workspacePath, token := newCodexRunnerFixture(t)
	script := writeFakeCodexBinary(t, `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "codex 0.0.0-test"
  exit 0
fi
read start_turn
printf '%s\n' '{"type":"handshake","codex_version":"0.0.0-test","protocol_version":"protocol-test-v1","schema_version":"schema-test-v1","experimental_api":false}'
printf '%s\n' '{"type":"thread_started","thread_id":"thread_fixture"}'
printf '%s\n' '{"type":"turn_started","turn_id":"turn_fixture"}'
printf '%s\n' '{"type":"approval_requested","payload":{"kind":"command","action_summary":"Run delayed command","risk_level":"medium","policy_match":"command.review","request_id":"codex_req_slow_approval","timeout_ms":5000}}'
read approval_decision
printf '%s\n' '{"type":"handoff","payload":{"summary":"Delayed approval completed.","changed_files":[],"tests":[],"risks":[],"verification":[],"followups":[],"target_state":"Human Review"}}'
printf '%s\n' '{"type":"turn_completed"}'
`)
	runner := &Runner{Command: script + " app-server"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resultC := make(chan agent.RunResult, 1)
	errC := make(chan error, 1)
	req := codexTestRunRequest(st, run, issue, workspacePath, token)
	req.Timeouts.StallMS = 1000
	req.Timeouts.TurnMS = 3000
	go func() {
		result, err := runner.Run(ctx, req)
		resultC <- result
		errC <- err
	}()

	approval := waitForApprovalByRequestID(t, st, "codex_req_slow_approval")
	time.Sleep(1100 * time.Millisecond)
	if err := st.DecideApproval(approval.ID, "approved_once", "delayed approval"); err != nil {
		t.Fatalf("DecideApproval: %v", err)
	}
	result := waitCodexResult(t, resultC, errC)
	if result.Kind != agent.RunResultSucceeded {
		t.Fatalf("result = %#v, want success after delayed approval", result)
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

func TestRunnerReadTimeoutFails(t *testing.T) {
	_, run, issue, workspacePath, token := newCodexRunnerFixture(t)
	script := writeFakeCodexBinary(t, `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "codex 0.0.0-test"
  exit 0
fi
read start_turn
sleep 2
`)

	result, err := (&Runner{Command: script + " app-server"}).Run(context.Background(), agent.RunRequest{
		Run:       run,
		Issue:     issue,
		Workspace: &core.WorkspaceSummary{Path: workspacePath},
		ToolToken: token,
		Timeouts:  agent.TimeoutPolicy{StartupMS: 1000, TurnMS: 1000, StallMS: 1000, ReadMS: 25},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Kind != agent.RunResultFailed || result.FailureCode != core.FailureCodexProtocolError || result.FailureMessage != "codex read timeout" {
		t.Fatalf("result = %#v, want read timeout protocol failure", result)
	}
}

func TestRunnerReadTimeoutOnlyAppliesBeforeFirstResponse(t *testing.T) {
	st, run, issue, workspacePath, token := newCodexRunnerFixture(t)
	script := writeFakeCodexBinary(t, `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "codex 0.0.0-test"
  exit 0
fi
read start_turn
printf '%s\n' '{"type":"handshake","codex_version":"0.0.0-test","protocol_version":"protocol-test-v1","schema_version":"schema-test-v1","experimental_api":false}'
sleep 0.5
printf '%s\n' '{"type":"thread_started","thread_id":"thread_fixture"}'
printf '%s\n' '{"type":"turn_started","turn_id":"turn_fixture"}'
printf '%s\n' '{"type":"handoff","payload":{"summary":"Codex fixture completed.","changed_files":[],"tests":[],"risks":[],"verification":[],"followups":[],"target_state":"Human Review"}}'
printf '%s\n' '{"type":"turn_completed"}'
`)

	result, err := (&Runner{Command: script + " app-server"}).Run(context.Background(), agent.RunRequest{
		Run:       run,
		Issue:     issue,
		Workspace: &core.WorkspaceSummary{Path: workspacePath},
		ToolToken: token,
		Timeouts:  agent.TimeoutPolicy{StartupMS: 1000, TurnMS: 1000, StallMS: 1000, ReadMS: 250},
		Gateway:   toolgateway.Gateway{Store: st},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Kind != agent.RunResultSucceeded {
		t.Fatalf("result = %#v, want success", result)
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

func TestHandleProtocolLineRejectsTurnFailedBeforeHandshake(t *testing.T) {
	req := agent.RunRequest{}
	handshakeDone := false
	var startupC <-chan time.Time
	handoffReceived := false
	threadID := ""

	_, done, err := handleProtocolLine(req, nil, `{"type":"turn_failed","failure_code":"operator_cancelled"}`, &handshakeDone, &startupC, &handoffReceived, &threadID)
	if err == nil || !strings.Contains(err.Error(), "turn failed before handshake") {
		t.Fatalf("err = %v, want turn failed before handshake", err)
	}
	if done {
		t.Fatal("done = true, want false")
	}
}

func TestHandleProtocolLineRejectsNotificationsBeforeHandshake(t *testing.T) {
	cases := []struct {
		name    string
		line    string
		wantErr string
	}{
		{name: "approval_requested", line: `{"type":"approval_requested"}`, wantErr: "approval requested before handshake"},
		{name: "approval_resolved", line: `{"type":"approval_resolved"}`, wantErr: "approval resolved before handshake"},
		{name: "tool_call", line: `{"type":"tool_call"}`, wantErr: "tool call before handshake"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := agent.RunRequest{}
			handshakeDone := false
			var startupC <-chan time.Time
			handoffReceived := false
			threadID := ""

			_, done, err := handleProtocolLine(req, nil, tc.line, &handshakeDone, &startupC, &handoffReceived, &threadID)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want contains %q", err, tc.wantErr)
			}
			if done {
				t.Fatal("done = true, want false")
			}
		})
	}
}

func TestDefaultFixtureRootSupportsEnvOverride(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("SYMPHONY_CODEX_FIXTURE_ROOT", tmp)
	if got := defaultFixtureRoot(); got != tmp {
		t.Fatalf("defaultFixtureRoot() = %q, want %q", got, tmp)
	}
}

func TestDefaultFixtureRootPrefersRepositoryFixturePathOverCWDTestdata(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})

	root := t.TempDir()
	writeLocalSymphonyGoMod(t, root)
	if err := os.MkdirAll(filepath.Join(root, "testdata"), 0o755); err != nil {
		t.Fatalf("mkdir cwd testdata: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "internal", "agent", "codex", "testdata"), 0o755); err != nil {
		t.Fatalf("mkdir repo fixture root: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	t.Setenv("SYMPHONY_CODEX_FIXTURE_ROOT", "")
	want := filepath.Join("internal", "agent", "codex", "testdata")
	assertSamePath(t, defaultFixtureRoot(), filepath.Join(root, want))
}

func TestDefaultFixtureRootFindsRepositoryFixturePathFromSubdirectory(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})

	root := t.TempDir()
	writeLocalSymphonyGoMod(t, root)
	subdir := filepath.Join(root, "internal", "orchestrator")
	if err := os.MkdirAll(filepath.Join(subdir, "testdata"), 0o755); err != nil {
		t.Fatalf("mkdir cwd testdata: %v", err)
	}
	repoFixtureRoot := filepath.Join(root, "internal", "agent", "codex", "testdata")
	if err := os.MkdirAll(repoFixtureRoot, 0o755); err != nil {
		t.Fatalf("mkdir repo fixture root: %v", err)
	}
	if err := os.Chdir(subdir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	t.Setenv("SYMPHONY_CODEX_FIXTURE_ROOT", "")
	assertSamePath(t, defaultFixtureRoot(), repoFixtureRoot)
}

func TestDefaultFixtureRootDoesNotUseUnrelatedRepositoryFixturePath(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module unrelated\n"), 0o644); err != nil {
		t.Fatalf("write unrelated go.mod: %v", err)
	}
	unrelatedFixtureRoot := filepath.Join(root, "internal", "agent", "codex", "testdata")
	if err := os.MkdirAll(unrelatedFixtureRoot, 0o755); err != nil {
		t.Fatalf("mkdir unrelated fixture root: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	t.Setenv("SYMPHONY_CODEX_FIXTURE_ROOT", "")
	if got := defaultFixtureRoot(); sameExistingPath(t, got, unrelatedFixtureRoot) {
		t.Fatalf("defaultFixtureRoot() = unrelated fixture root %q", got)
	}
}

func TestDefaultFixtureRootDoesNotUseArbitraryCWDTestdata(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})

	root := t.TempDir()
	cwdTestdata := filepath.Join(root, "testdata")
	if err := os.MkdirAll(cwdTestdata, 0o755); err != nil {
		t.Fatalf("mkdir cwd testdata: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	t.Setenv("SYMPHONY_CODEX_FIXTURE_ROOT", "")
	if got := defaultFixtureRoot(); sameExistingPath(t, got, cwdTestdata) {
		t.Fatalf("defaultFixtureRoot() = arbitrary cwd testdata %q", got)
	}
}

func TestCommandPartsPreservesQuotedArguments(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    []string
	}{
		{
			name:    "quoted executable path",
			command: `"/opt/tools/codex wrapper" app-server`,
			want:    []string{"/opt/tools/codex wrapper", "app-server"},
		},
		{
			name:    "quoted and empty argument",
			command: `codex app-server --label "space value" --empty ""`,
			want:    []string{"codex", "app-server", "--label", "space value", "--empty", ""},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := commandParts(tc.command); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("commandParts(%q) = %#v, want %#v", tc.command, got, tc.want)
			}
		})
	}
}

func TestDetectVersionForCommandSupportsQuotedExecutablePath(t *testing.T) {
	binDir := filepath.Join(t.TempDir(), "with spaces")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin dir: %v", err)
	}
	executable := filepath.Join(binDir, "codex wrapper")
	body := `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "codex 0.0.0-test"
  exit 0
fi
exit 1
`
	if err := os.WriteFile(executable, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake codex executable: %v", err)
	}

	command := `"` + executable + `" app-server`
	if got := DetectVersionForCommand(command); got != "codex 0.0.0-test" {
		t.Fatalf("DetectVersionForCommand(%q) = %q, want %q", command, got, "codex 0.0.0-test")
	}
}

func TestDetectVersionForCommandPreservesWrapperArguments(t *testing.T) {
	wrapper := filepath.Join(t.TempDir(), "codex-wrapper")
	body := `#!/bin/sh
if [ "$1" = "app-server" ] && [ "$2" = "--version" ]; then
  echo "codex 0.0.0-test"
  exit 0
fi
echo "unexpected args: $*" >&2
exit 1
`
	if err := os.WriteFile(wrapper, []byte(body), 0o755); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}

	command := `/bin/sh "` + wrapper + `" app-server`
	if got := DetectVersionForCommand(command); got != "codex 0.0.0-test" {
		t.Fatalf("DetectVersionForCommand(%q) = %q, want %q", command, got, "codex 0.0.0-test")
	}
}

func TestDetectVersionForCommandTimesOutDirectProbe(t *testing.T) {
	oldTimeout := versionProbeTimeout
	versionProbeTimeout = 25 * time.Millisecond
	t.Cleanup(func() {
		versionProbeTimeout = oldTimeout
	})
	script := writeFakeCodexBinary(t, `#!/bin/sh
if [ "$1" = "--version" ]; then
  sleep 2
  exit 0
fi
exit 1
`)

	start := time.Now()
	if got := DetectVersionForCommand(script); got != "" {
		t.Fatalf("DetectVersionForCommand() = %q, want empty version after timeout", got)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("DetectVersionForCommand took %s, want bounded by probe timeout", elapsed)
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

func TestScanStdoutAcceptsLargeProtocolLine(t *testing.T) {
	largeLine := `{"type":"turn_progress","message":"` + strings.Repeat("x", 2*1024*1024) + `"}`
	lines := make(chan stdoutItem, 2)

	scanStdout(strings.NewReader(largeLine+"\n"), lines)

	item, ok := <-lines
	if !ok {
		t.Fatal("stdout line channel closed before first line")
	}
	if item.err != nil {
		t.Fatalf("scanStdout error = %v", item.err)
	}
	if item.line != largeLine {
		t.Fatalf("line length = %d, want %d", len(item.line), len(largeLine))
	}
	item, ok = <-lines
	if ok {
		t.Fatalf("unexpected trailing stdout item: %#v", item)
	}
}

func TestValidateTranscriptFixtureAcceptsLargeProtocolLine(t *testing.T) {
	root := t.TempDir()
	version := "1.2.3"
	writeTestFixture(t, root, version, `{
		"codex_version": "1.2.3",
		"protocol_version": "protocol-test-v1",
		"schema_version": "schema-test-v1",
		"supported_notifications": ["codex.initialized"],
		"supported_requests": ["initialize"],
		"experimental_api": false
	}`)
	if err := os.WriteFile(filepath.Join(root, "schema", version, "schema.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	transcript := strings.Join([]string{
		`{"type":"handshake","codex_version":"1.2.3","protocol_version":"protocol-test-v1","schema_version":"schema-test-v1","experimental_api":false}`,
		`{"type":"turn_progress","message":"` + strings.Repeat("x", 128*1024) + `"}`,
		`{"type":"turn_completed"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(root, "transcripts", version, "happy-path.jsonl"), []byte(transcript), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	selected := &SelectedFixture{
		TranscriptDir: filepath.Join(root, "transcripts", version),
		Metadata: CompatibilityMetadata{
			CodexVersion:    version,
			ProtocolVersion: "protocol-test-v1",
			SchemaVersion:   "schema-test-v1",
			ExperimentalAPI: false,
		},
	}

	if err := ValidateTranscriptFixture(selected, "happy-path.jsonl"); err != nil {
		t.Fatalf("ValidateTranscriptFixture: %v", err)
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

func assertSamePath(t *testing.T, got, want string) {
	t.Helper()
	actual, err := realPath(got)
	if err != nil {
		t.Fatalf("EvalSymlinks got path %q: %v", got, err)
	}
	expected, err := realPath(want)
	if err != nil {
		t.Fatalf("EvalSymlinks want path %q: %v", want, err)
	}
	if actual != expected {
		t.Fatalf("path = %q, want %q", actual, expected)
	}
}

func sameExistingPath(t *testing.T, got, want string) bool {
	t.Helper()
	actual, err := realPath(got)
	if err != nil {
		return false
	}
	expected, err := realPath(want)
	if err != nil {
		return false
	}
	return actual == expected
}

func realPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
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

func codexTestRunRequest(st *store.Store, run *core.RunAttempt, issue *core.Issue, workspacePath, token string) agent.RunRequest {
	return agent.RunRequest{
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
	}
}

func waitForApprovalByRequestID(t *testing.T, st *store.Store, requestID string) *store.Approval {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		approvals, err := st.PendingApprovals()
		if err != nil {
			t.Fatalf("PendingApprovals: %v", err)
		}
		for i := range approvals {
			row, err := st.Project.QueryOne(`SELECT request_json FROM approval_requests WHERE id=?`, approvals[i].ID)
			if err != nil {
				t.Fatalf("query approval request_json: %v", err)
			}
			var request map[string]any
			if err := json.Unmarshal([]byte(row["request_json"].String()), &request); err != nil {
				t.Fatalf("decode request_json: %v", err)
			}
			if request["request_id"] == requestID {
				return &approvals[i]
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("approval with request_id %q was not created", requestID)
	return nil
}

func waitCodexResult(t *testing.T, resultC <-chan agent.RunResult, errC <-chan error) agent.RunResult {
	t.Helper()
	select {
	case result := <-resultC:
		if err := <-errC; err != nil {
			t.Fatalf("Run: %v", err)
		}
		return result
	case <-time.After(10 * time.Second):
		t.Fatal("runner did not return")
	}
	return agent.RunResult{}
}

func assertApprovalRequestJSONDoesNotContain(t *testing.T, st *store.Store, approvalID, forbidden string) {
	t.Helper()
	row, err := st.Project.QueryOne(`SELECT request_json FROM approval_requests WHERE id=?`, approvalID)
	if err != nil {
		t.Fatalf("query approval request_json: %v", err)
	}
	if strings.Contains(row["request_json"].String(), forbidden) {
		t.Fatalf("approval request_json leaked %q: %s", forbidden, row["request_json"].String())
	}
}

func assertApprovalDecisionFile(t *testing.T, path, wantApprovalID, wantDecision string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read approval decision: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode approval decision %s: %v", string(data), err)
	}
	if got["type"] != "approval_decision" || got["approval_id"] != wantApprovalID || got["decision"] != wantDecision {
		t.Fatalf("approval decision = %#v, want id %q decision %q", got, wantApprovalID, wantDecision)
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

func writeLocalSymphonyGoMod(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module local-symphony\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
}
