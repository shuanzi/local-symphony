package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type IssueState string

const (
	StateInbox       IssueState = "Inbox"
	StateReady       IssueState = "Ready"
	StateWorking     IssueState = "Working"
	StateRework      IssueState = "Rework"
	StateBlocked     IssueState = "Blocked"
	StateHumanReview IssueState = "Human Review"
	StateDone        IssueState = "Done"
	StateCancelled   IssueState = "Cancelled"
	StateDuplicate   IssueState = "Duplicate"
)

type RunStatus string

const (
	RunPending                 RunStatus = "pending"
	RunPreparingWorkspace      RunStatus = "preparing_workspace"
	RunRenderingPrompt         RunStatus = "rendering_prompt"
	RunStartingAgent           RunStatus = "starting_agent"
	RunRunning                 RunStatus = "running"
	RunCompleted               RunStatus = "completed"
	RunCompletedWithoutHandoff RunStatus = "completed_without_handoff"
	RunFailed                  RunStatus = "failed"
	RunCancelled               RunStatus = "cancelled"
)

type FailureCode string

const (
	FailureWorkflowInvalid            FailureCode = "workflow_invalid"
	FailureWorkflowValidationFailed   FailureCode = "workflow_validation_failed"
	FailurePromptRenderFailed         FailureCode = "prompt_render_failed"
	FailureWorkspacePrepareFailed     FailureCode = "workspace_prepare_failed"
	FailureWorkspaceConflict          FailureCode = "workspace_conflict"
	FailureAfterCreateFailed          FailureCode = "after_create_failed"
	FailureBeforeRunFailed            FailureCode = "before_run_failed"
	FailureCodexStartupFailed         FailureCode = "codex_startup_failed"
	FailureUnsupportedCodexVersion    FailureCode = "unsupported_codex_version"
	FailureCodexProtocolError         FailureCode = "codex_protocol_error"
	FailureTurnTimeout                FailureCode = "turn_timeout"
	FailureStallTimeout               FailureCode = "stall_timeout"
	FailureApprovalTimeout            FailureCode = "approval_timeout"
	FailureCommandDenied              FailureCode = "command_denied"
	FailureNetworkDenied              FailureCode = "network_denied"
	FailureProtectedPathDenied        FailureCode = "protected_path_denied"
	FailureToolGatewayFailed          FailureCode = "tool_gateway_failed"
	FailureMissingHandoff             FailureCode = "missing_handoff"
	FailureReviewPacketFailed         FailureCode = "review_packet_failed"
	FailureOperatorCancelled          FailureCode = "operator_cancelled"
	FailureAgentBlocked               FailureCode = "agent_blocked"
	FailureIssueStateChanged          FailureCode = "issue_state_changed"
	FailureCanceledByReconciliation   FailureCode = "canceled_by_reconciliation"
	FailureDaemonRestartedInterrupted FailureCode = "daemon_restarted_run_interrupted"
)

type APIErrorCode string

const (
	ErrUnauthorized             APIErrorCode = "unauthorized"
	ErrForbidden                APIErrorCode = "forbidden"
	ErrCSRFRequired             APIErrorCode = "csrf_required"
	ErrInvalidRequest           APIErrorCode = "invalid_request"
	ErrNotFound                 APIErrorCode = "not_found"
	ErrUnsupportedDBVersion     APIErrorCode = "unsupported_db_version"
	ErrWorkflowInvalid          APIErrorCode = "workflow_invalid"
	ErrWorkflowValidationFailed APIErrorCode = "workflow_validation_failed"
	ErrPromptRenderFailed       APIErrorCode = "prompt_render_failed"
	ErrInvalidStateTransition   APIErrorCode = "invalid_state_transition"
	ErrIssueBlocked             APIErrorCode = "issue_blocked"
	ErrIssueDispatchPaused      APIErrorCode = "issue_dispatch_paused"
	ErrIssueAlreadyRunning      APIErrorCode = "issue_already_running"
	ErrConcurrencyLimitReached  APIErrorCode = "concurrency_limit_reached"
	ErrWorkspaceConflict        APIErrorCode = "workspace_conflict"
	ErrWorkspacePrepareFailed   APIErrorCode = "workspace_prepare_failed"
	ErrReviewPacketRequired     APIErrorCode = "review_packet_required"
	ErrReviewPacketFailed       APIErrorCode = "review_packet_failed"
	ErrToolTokenInvalid         APIErrorCode = "tool_token_invalid"
	ErrToolGatewayFailed        APIErrorCode = "tool_gateway_failed"
	ErrApprovalNotPending       APIErrorCode = "approval_not_pending"
	ErrRawLogAccessUnsupported  APIErrorCode = "raw_log_access_not_supported"
	ErrInternal                 APIErrorCode = "internal_error"
)

type APIError struct {
	Code    APIErrorCode   `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func (e *APIError) Error() string { return string(e.Code) + ": " + e.Message }

func NewError(code APIErrorCode, msg string, details map[string]any) *APIError {
	if msg == "" {
		msg = string(code)
	}
	if details == nil {
		details = map[string]any{}
	}
	return &APIError{Code: code, Message: msg, Details: details}
}

func AsAPIError(err error) *APIError {
	if err == nil {
		return nil
	}
	if e, ok := err.(*APIError); ok {
		return e
	}
	return NewError(ErrInternal, err.Error(), nil)
}

func ExitCodeForError(code APIErrorCode) int {
	switch code {
	case ErrInvalidRequest:
		return 2
	case ErrWorkflowInvalid, ErrWorkflowValidationFailed, ErrPromptRenderFailed:
		return 9
	default:
		return 7
	}
}

func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func NewID(prefix string) string {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s%x", prefix, time.Now().UnixNano())
	}
	return prefix + hex.EncodeToString(b[:])
}

func TrimNonEmpty(s string) string { return strings.TrimSpace(s) }

func IsActiveRunStatus(s RunStatus) bool {
	switch s {
	case RunPending, RunPreparingWorkspace, RunRenderingPrompt, RunStartingAgent, RunRunning:
		return true
	default:
		return false
	}
}

func IsTerminalIssueState(s IssueState) bool {
	return s == StateDone || s == StateCancelled || s == StateDuplicate
}

func IsDispatchState(s IssueState) bool {
	return s == StateReady || s == StateRework
}

type IssueRef struct {
	ID         string     `json:"id"`
	Identifier string     `json:"identifier"`
	Title      string     `json:"title"`
	State      IssueState `json:"state"`
}

type WorkspaceSummary struct {
	ID            string `json:"id"`
	Path          string `json:"path"`
	BranchName    string `json:"branch_name"`
	BaseRef       string `json:"base_ref"`
	BaseRefConfig string `json:"base_ref_config"`
	BaseSHA       string `json:"base_sha"`
	Status        string `json:"status"`
}

type GitSummary struct {
	BranchName    *string `json:"branch_name"`
	BaseRef       *string `json:"base_ref"`
	BaseRefConfig *string `json:"base_ref_config"`
	BaseSHA       *string `json:"base_sha"`
}

type RunSummary struct {
	ID          string       `json:"id"`
	Status      RunStatus    `json:"status"`
	AttemptNo   int          `json:"attempt_no"`
	FailureCode *FailureCode `json:"failure_code,omitempty"`
	CreatedAt   string       `json:"created_at"`
}

type ReviewPacketSummary struct {
	ID             string       `json:"id"`
	RunID          string       `json:"run_id"`
	PacketNo       int          `json:"packet_no"`
	Status         string       `json:"status"`
	CreatedAt      string       `json:"created_at"`
	FailureCode    *FailureCode `json:"failure_code,omitempty"`
	FailureMessage *string      `json:"failure_message,omitempty"`
}

type Issue struct {
	ID                   string               `json:"id"`
	Identifier           string               `json:"identifier"`
	SequenceNo           int                  `json:"sequence_no"`
	Title                string               `json:"title"`
	Description          string               `json:"description"`
	AcceptanceCriteria   []string             `json:"acceptance_criteria"`
	Priority             int                  `json:"priority"`
	State                IssueState           `json:"state"`
	URL                  *string              `json:"url"`
	Labels               []string             `json:"labels"`
	BlockedBy            []IssueRef           `json:"blocked_by"`
	Blocks               []IssueRef           `json:"blocks"`
	DuplicateOf          *IssueRef            `json:"duplicate_of"`
	Duplicates           []IssueRef           `json:"duplicates"`
	FollowupOf           *IssueRef            `json:"followup_of"`
	Followups            []IssueRef           `json:"followups"`
	DispatchPaused       bool                 `json:"dispatch_paused"`
	DispatchPauseReason  *string              `json:"dispatch_pause_reason"`
	DispatchPausedAt     *string              `json:"dispatch_paused_at"`
	BranchName           *string              `json:"branch_name"`
	WorkspacePath        *string              `json:"workspace_path"`
	BaseRef              *string              `json:"base_ref"`
	BaseRefConfig        *string              `json:"base_ref_config"`
	BaseSHA              *string              `json:"base_sha"`
	Workspace            *WorkspaceSummary    `json:"workspace"`
	Git                  *GitSummary          `json:"git"`
	LatestRun            *RunSummary          `json:"latest_run"`
	ActiveRunID          *string              `json:"active_run_id"`
	LatestRunID          *string              `json:"latest_run_id"`
	LatestReviewPacket   *ReviewPacketSummary `json:"latest_review_packet"`
	LatestReviewPacketID *string              `json:"latest_review_packet_id"`
	CreatedAt            string               `json:"created_at"`
	UpdatedAt            string               `json:"updated_at"`
	CompletedAt          *string              `json:"completed_at"`
	ArchivedAt           *string              `json:"archived_at"`
}

type RunAttempt struct {
	ID                 string       `json:"id"`
	IssueID            string       `json:"issue_id"`
	IssueIdentifier    string       `json:"issue_identifier,omitempty"`
	AttemptNo          int          `json:"attempt_no"`
	WorkspaceID        *string      `json:"workspace_id"`
	WorkflowSnapshotID *string      `json:"workflow_snapshot_id"`
	Status             RunStatus    `json:"status"`
	DispatchReason     string       `json:"dispatch_reason"`
	SourceIssueState   IssueState   `json:"source_issue_state"`
	RunnerKind         string       `json:"runner_kind"`
	BaseRefConfig      *string      `json:"base_ref_config"`
	BaseRef            *string      `json:"base_ref"`
	BaseSHA            *string      `json:"base_sha"`
	BranchName         *string      `json:"branch_name"`
	FailureCode        *FailureCode `json:"failure_code"`
	FailureMessage     *string      `json:"failure_message"`
	StartedAt          *string      `json:"started_at"`
	EndedAt            *string      `json:"ended_at"`
	CreatedAt          string       `json:"created_at"`
	UpdatedAt          string       `json:"updated_at"`
}

type RunEvent struct {
	Seq       int64          `json:"seq"`
	ID        string         `json:"id"`
	ProjectID string         `json:"project_id"`
	IssueID   *string        `json:"issue_id,omitempty"`
	RunID     *string        `json:"run_id,omitempty"`
	EventType string         `json:"event_type"`
	ActorType string         `json:"actor_type"`
	Data      map[string]any `json:"data"`
	Redacted  bool           `json:"redacted"`
	CreatedAt string         `json:"created_at"`
}

type Handoff struct {
	ID           string         `json:"id"`
	IssueID      string         `json:"issue_id"`
	RunID        string         `json:"run_id"`
	PayloadHash  string         `json:"payload_hash"`
	Payload      map[string]any `json:"payload"`
	Summary      string         `json:"summary"`
	ChangedFiles []string       `json:"changed_files"`
	Tests        []string       `json:"tests"`
	Risks        []string       `json:"risks"`
	Verification []string       `json:"verification"`
	Followups    []string       `json:"followups"`
	TargetState  string         `json:"target_state"`
	SubmittedAt  string         `json:"submitted_at"`
}

type SuccessEnvelope struct {
	Data any            `json:"data"`
	Meta map[string]any `json:"meta"`
}

type ErrorEnvelope struct {
	Error map[string]any `json:"error"`
}

func JSONBytes(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func StringPtr(s string) *string { return &s }

func NullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func FailurePtr(s string) *FailureCode {
	if s == "" {
		return nil
	}
	f := FailureCode(s)
	return &f
}
