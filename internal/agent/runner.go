package agent

import (
	"context"

	"local-symphony/internal/core"
	"local-symphony/internal/toolgateway"
)

type RunResultKind string

const (
	RunResultSucceeded      RunResultKind = "succeeded"
	RunResultMissingHandoff RunResultKind = "missing_handoff"
	RunResultFailed         RunResultKind = "failed"
	RunResultHeld           RunResultKind = "held"
)

type Runner interface {
	Run(context.Context, RunRequest) (RunResult, error)
}

type ClosableRunner interface {
	Close(context.Context, RunRequest) error
}

type EventSink func(eventType string, data map[string]any) error

type RunRequest struct {
	Run                *core.RunAttempt
	Issue              *core.Issue
	Workspace          *core.WorkspaceSummary
	ProjectID          string
	WorkflowSnapshotID string
	Prompt             string
	ToolEndpoint       string
	ToolToken          string
	Timeouts           TimeoutPolicy
	Gateway            toolgateway.Gateway
	IsContinuation     bool
	EmitEvent          EventSink
}

type RunResult struct {
	Kind           RunResultKind
	FailureCode    core.FailureCode
	FailureMessage string
}

type TimeoutPolicy struct {
	StartupMS int
	TurnMS    int
	StallMS   int
	ReadMS    int
}
