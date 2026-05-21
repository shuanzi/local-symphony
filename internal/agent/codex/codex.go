package codex

import (
	"os/exec"
	"strings"

	"local-symphony/internal/core"
)

type Support struct {
	Supported bool   `json:"supported"`
	Version   string `json:"version"`
	Reason    string `json:"reason,omitempty"`
}

func DetectVersion() string {
	cmd := exec.Command("codex", "--version")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func PreflightFixtureGate() error {
	// v1 real Codex integration is fixture-gated. This implementation intentionally fails closed
	// unless a future committed fixture package wires a concrete version.
	return core.NewError(core.APIErrorCode(core.FailureUnsupportedCodexVersion), "unsupported Codex version: no committed fixture metadata", nil)
}
