package fake

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

func SelectedOutcome() Outcome {
	v := strings.ToLower(os.Getenv("SYMPHONY_FAKE_RUNNER_OUTCOME"))
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
	_ = os.MkdirAll(workspacePath, 0o755)
	if SelectedOutcome() == OutcomeSuccess {
		out := filepath.Join(workspacePath, "symphony-output.txt")
		_ = os.WriteFile(out, []byte(fmt.Sprintf("Fake runner completed %s\n", issueIdentifier)), 0o644)
		resp := gw.Call(token, workspacePath, toolgateway.Request{Tool: "handoff.submit", Input: map[string]any{"summary": "Fake runner completed the requested work.", "changed_files": []any{"symphony-output.txt"}, "tests": []any{"fake runner smoke test passed"}, "risks": []any{}, "verification": []any{"review symphony-output.txt"}, "followups": []any{}, "target_state": "Human Review"}})
		if resp.Error != nil {
			return core.NewError(core.ErrToolGatewayFailed, resp.Error.Message, resp.Error.Details)
		}
	}
	return nil
}
