package fake

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"local-symphony/internal/agent"
	"local-symphony/internal/core"
	"local-symphony/internal/toolgateway"
)

type Outcome string

const (
	OutcomeSuccess        Outcome = "success"
	OutcomeFailure        Outcome = "failure"
	OutcomeMissingHandoff Outcome = "missing_handoff"
	OutcomeHold           Outcome = "hold"
)

type Runner struct{}

var outcomeSequence struct {
	sync.Mutex
}

func SelectedOutcome() Outcome {
	v := strings.ToLower(os.Getenv("SYMPHONY_FAKE_RUNNER_OUTCOME"))
	return parseOutcome(v)
}

func (Runner) Run(_ context.Context, req agent.RunRequest) (agent.RunResult, error) {
	switch outcome := nextOutcome(); outcome {
	case OutcomeFailure:
		code := core.FailureCodexProtocolError
		if v := os.Getenv("SYMPHONY_FAKE_FAILURE_CODE"); v != "" {
			code = core.FailureCode(v)
		}
		return agent.RunResult{Kind: agent.RunResultFailed, FailureCode: code, FailureMessage: "fake runner failure"}, nil
	case OutcomeMissingHandoff:
		return agent.RunResult{Kind: agent.RunResultMissingHandoff, FailureCode: core.FailureMissingHandoff, FailureMessage: "fake runner completed without handoff"}, nil
	case OutcomeHold:
		return agent.RunResult{Kind: agent.RunResultHeld}, nil
	default:
		if err := submitSuccess(req.Workspace.Path, req.Issue.Identifier, req.ToolToken, req.Gateway); err != nil {
			return agent.RunResult{}, err
		}
		return agent.RunResult{Kind: agent.RunResultSucceeded}, nil
	}
}

func nextOutcome() Outcome {
	raw := os.Getenv("SYMPHONY_FAKE_RUNNER_OUTCOMES")
	if strings.TrimSpace(raw) == "" {
		return SelectedOutcome()
	}
	outcomeSequence.Lock()
	defer outcomeSequence.Unlock()
	parts := strings.Split(raw, ",")
	outcome := parseOutcome(strings.ToLower(strings.TrimSpace(parts[0])))
	if len(parts) > 1 {
		_ = os.Setenv("SYMPHONY_FAKE_RUNNER_OUTCOMES", strings.Join(parts[1:], ","))
	}
	return outcome
}

func parseOutcome(v string) Outcome {
	switch v {
	case "fail", "failure":
		return OutcomeFailure
	case "missing", "missing_handoff", "no_handoff":
		return OutcomeMissingHandoff
	case "hold", "active", "running":
		return OutcomeHold
	default:
		return OutcomeSuccess
	}
}

func Run(workspacePath string, issueIdentifier string, token string, gw toolgateway.Gateway) error {
	if SelectedOutcome() != OutcomeSuccess {
		return nil
	}
	return submitSuccess(workspacePath, issueIdentifier, token, gw)
}

func submitSuccess(workspacePath string, issueIdentifier string, token string, gw toolgateway.Gateway) error {
	_ = os.MkdirAll(workspacePath, 0o755)
	out := filepath.Join(workspacePath, "symphony-output.txt")
	_ = os.WriteFile(out, []byte(fmt.Sprintf("Fake runner completed %s\n", issueIdentifier)), 0o644)
	resp := gw.Call(token, workspacePath, toolgateway.Request{Tool: "handoff.submit", Input: map[string]any{"summary": "Fake runner completed the requested work.", "changed_files": []any{"symphony-output.txt"}, "tests": []any{"fake runner smoke test passed"}, "risks": []any{}, "verification": []any{"review symphony-output.txt"}, "followups": []any{}, "target_state": "Human Review"}})
	if resp.Error != nil {
		return core.NewError(core.ErrToolGatewayFailed, resp.Error.Message, resp.Error.Details)
	}
	return nil
}
