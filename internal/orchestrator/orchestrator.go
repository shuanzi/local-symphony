package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	agentrunner "local-symphony/internal/agent"
	"local-symphony/internal/agent/codex"
	"local-symphony/internal/agent/fake"
	"local-symphony/internal/config"
	"local-symphony/internal/core"
	"local-symphony/internal/review"
	"local-symphony/internal/security"
	"local-symphony/internal/store"
	"local-symphony/internal/toolgateway"
	"local-symphony/internal/workspace"
)

type Orchestrator struct{ Store *store.Store }

type DispatchResult struct {
	Run       *core.RunAttempt       `json:"run"`
	Issue     *core.Issue            `json:"issue"`
	Workspace *core.WorkspaceSummary `json:"workspace,omitempty"`
}

func (o Orchestrator) DispatchIssue(issueRef, reason string) (*DispatchResult, error) {
	wf, err := config.Load(o.Store.RepoRoot)
	if err != nil {
		return nil, err
	}
	return o.dispatchIssueWithWorkflow(wf, issueRef, reason)
}

func (o Orchestrator) dispatchIssueWithWorkflow(wf *config.Workflow, issueRef, reason string) (*DispatchResult, error) {
	if !wf.Validation.Valid {
		return nil, core.NewError(core.ErrWorkflowInvalid, "WORKFLOW.md is invalid", map[string]any{"errors": wf.Validation.Errors, "warnings": wf.Validation.Warnings})
	}
	runnerKind := selectedRunnerKind()
	run, err := o.Store.ClaimRun(issueRef, reasonOrDefault(reason, "manual"), runnerKind, wf.Config.Agent.MaxConcurrentAgents)
	if err != nil {
		return nil, err
	}
	issue, err := o.Store.GetIssue(run.IssueID)
	if err != nil {
		return nil, err
	}
	if err := o.runWorker(run.ID, wf); err != nil {
		return nil, err
	}
	run, err = o.Store.GetRun(run.ID)
	if err != nil {
		return nil, err
	}
	issue, err = o.Store.GetIssue(run.IssueID)
	if err != nil {
		return nil, err
	}
	return &DispatchResult{Run: run, Issue: issue, Workspace: issue.Workspace}, nil
}

func selectedRunnerKind() string {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("SYMPHONY_RUNNER_KIND")), "codex") {
		return "codex"
	}
	return "fake"
}

func reasonOrDefault(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}

func shouldRunAfterCreate(st *store.Store, issue *core.Issue) (bool, error) {
	if issue.Workspace == nil || issue.Workspace.Status != "prepared" {
		return true, nil
	}
	rows, err := st.Project.Query(
		`SELECT event_type FROM run_events WHERE issue_id=? AND event_type IN ('hook.after_create.started','hook.after_create.output','hook.after_create.completed','hook.after_create.failed','hook.after_create.timeout') ORDER BY seq DESC LIMIT 1`,
		issue.ID,
	)
	if err != nil {
		return false, err
	}
	if len(rows) == 0 {
		return true, nil
	}
	return rows[0]["event_type"].String() != "hook.after_create.completed", nil
}

func (o Orchestrator) runWorker(runID string, wf *config.Workflow) error {
	run, err := o.Store.GetRun(runID)
	if err != nil {
		return err
	}
	issue, err := o.Store.GetIssue(run.IssueID)
	if err != nil {
		return err
	}
	if !o.advanceRun(runID, core.RunPreparingWorkspace, map[string]any{}) {
		return nil
	}
	mgr := workspace.Manager{Store: o.Store, Config: wf.Config}
	runAfterCreate, err := shouldRunAfterCreate(o.Store, issue)
	if err != nil {
		_ = o.Store.FailRun(runID, core.FailureAfterCreateFailed, fmt.Sprintf("load after_create state: %v", err), core.RunFailed)
		return nil
	}
	ws, err := mgr.Prepare(run, issue)
	if err != nil {
		_ = o.Store.FailRun(runID, core.FailureWorkspacePrepareFailed, err.Error(), core.RunFailed)
		return nil
	}
	active, activeErr := o.runIsActive(runID)
	if activeErr != nil || !active {
		return nil
	}
	if runAfterCreate {
		hookCtx, stopHookContext := o.runContext(runID)
		err := runWorkflowHook(hookCtx, o.Store, wf, ws.Path, runID, issue.ID, "after_create", wf.Config.Hooks.AfterCreate)
		stopHookContext()
		if err != nil {
			o.failRunAfterHook(runID, issue.ID, ws.Path, wf, core.FailureAfterCreateFailed, err.Error(), core.RunFailed)
			return nil
		}
		active, activeErr = o.runIsActive(runID)
		if activeErr != nil || !active {
			return nil
		}
	}
	if !o.advanceRun(runID, core.RunRenderingPrompt, map[string]any{}) {
		return nil
	}
	wfID, err := o.Store.CreateWorkflowSnapshot("valid", wf.Path, wf.ConfigJSON, wf.PromptHash, "[]")
	if err != nil {
		_ = o.Store.FailRun(runID, core.FailurePromptRenderFailed, fmt.Sprintf("create workflow snapshot: %v", err), core.RunFailed)
		return nil
	}
	if err := o.Store.AttachWorkflowSnapshot(runID, wfID); err != nil {
		_ = o.Store.FailRun(runID, core.FailurePromptRenderFailed, fmt.Sprintf("attach workflow snapshot: %v", err), core.RunFailed)
		return nil
	}
	prompt, err := config.RenderPrompt(wf, map[string]any{"issue": map[string]any{"identifier": issue.Identifier, "title": issue.Title, "description": issue.Description}, "run": map[string]any{"id": runID}, "workspace": map[string]any{"path": ws.Path}})
	if err != nil {
		_ = o.Store.FailRun(runID, core.FailurePromptRenderFailed, err.Error(), core.RunFailed)
		return nil
	}
	promptRoot := filepath.Join(o.Store.RepoRoot, ".symphony", "artifacts", issue.Identifier, runID)
	promptDir := filepath.Join(promptRoot, "prompt")
	if err := os.MkdirAll(promptDir, 0o755); err != nil {
		_ = o.Store.FailRun(runID, core.FailurePromptRenderFailed, fmt.Sprintf("create prompt dir: %v", err), core.RunFailed)
		return nil
	}
	// D4 / R16: when this run was dispatched as a Rework, fetch the
	// previous review reason and safe summary, and stamp them into
	// the rendered prompt + a rework_snapshots row. The prompt
	// snapshot hash reflects the *post-injection* prompt so
	// diagnostics can correlate what was actually sent to the agent
	// with the prior review packet.
	if run.SourceIssueState == core.StateRework {
		prompt, promptHash, reworkRec, reworkErr := o.injectReworkContext(issue, run, prompt)
		if reworkErr != nil {
			_ = o.Store.FailRun(runID, core.FailurePromptRenderFailed, fmt.Sprintf("rework context: %v", reworkErr), core.RunFailed)
			return nil
		}
		// Write the redacted prompt + meta so diagnostics and tests
		// can introspect what the agent received. We use a dedicated
		// `rework_prompt.redacted.md` filename so the review packet
		// generator (which overwrites rendered_prompt.redacted.md)
		// does not clobber this artifact.
		if err := os.WriteFile(filepath.Join(promptDir, "rework_prompt.redacted.md"), []byte("[redacted]\n"+prompt), 0o644); err != nil {
			_ = o.Store.FailRun(runID, core.FailurePromptRenderFailed, fmt.Sprintf("write redacted rework prompt: %v", err), core.RunFailed)
			return nil
		}
		// ph now refers to the hash of the prompt *with* rework
		// injection; this is what the agent will actually see.
		ph := sha256.Sum256([]byte(prompt))
		ch := sha256.Sum256([]byte(issue.ID + runID))
		psID, psErr := o.Store.CreatePromptSnapshot(runID, wfID, hex.EncodeToString(ch[:]), hex.EncodeToString(ph[:]), promptRoot)
		if psErr == nil && psID != "" {
			reworkRec.PromptSnapshotID = psID
			reworkRec.PromptHash = promptHash
			_, _ = o.Store.CreateReworkSnapshot(reworkRec)
		}
	} else {
		_ = os.WriteFile(filepath.Join(promptDir, "rendered_prompt.redacted.md"), []byte("[redacted]\n"+prompt), 0o644)
		ph := sha256.Sum256([]byte(prompt))
		ch := sha256.Sum256([]byte(issue.ID + runID))
		_, _ = o.Store.CreatePromptSnapshot(runID, wfID, hex.EncodeToString(ch[:]), hex.EncodeToString(ph[:]), promptRoot)
	}
	active, activeErr = o.runIsActive(runID)
	if activeErr != nil || !active {
		return nil
	}
	hookCtx, stopHookContext := o.runContext(runID)
	err = runWorkflowHook(hookCtx, o.Store, wf, ws.Path, runID, issue.ID, "before_run", wf.Config.Hooks.BeforeRun)
	stopHookContext()
	if err != nil {
		o.failRunAfterHook(runID, issue.ID, ws.Path, wf, core.FailureBeforeRunFailed, err.Error(), core.RunFailed)
		return nil
	}
	if !o.advanceRun(runID, core.RunStartingAgent, map[string]any{}) {
		return nil
	}
	token, err := toolgateway.NewTokenForRunWithOptions(o.Store, run, ws.Path, toolgateway.TokenOptions{
		ArtifactMaxBytes:  wf.Config.Tools.ArtifactMaxBytes,
		DisableFollowups:  !wf.Config.Tools.AgentCanCreateFollowups,
		DisableIssueBlock: !wf.Config.Tools.AgentCanSetBlocked,
	})
	if err != nil {
		_ = o.Store.FailRun(runID, core.FailureToolGatewayFailed, err.Error(), core.RunFailed)
		return nil
	}
	if !o.advanceRun(runID, core.RunRunning, map[string]any{"started_at": core.Now()}) {
		return nil
	}
	gw := toolgateway.Gateway{Store: o.Store}
	runner := runnerForRun(run, wf)
	runCtx, stopRunContext := o.runContext(runID)
	defer stopRunContext()
	runReq := agentrunner.RunRequest{
		Run:                run,
		Issue:              issue,
		Workspace:          ws,
		ProjectID:          o.Store.ProjectID,
		WorkflowSnapshotID: wfID,
		Prompt:             prompt,
		ToolEndpoint:       wf.Config.Tools.Gateway,
		ToolToken:          token,
		Timeouts:           runnerTimeoutPolicy(wf),
		Gateway:            gw,
		EmitEvent: func(eventType string, data map[string]any) error {
			return o.Store.AppendEvent(eventType, "codex", &issue.ID, &runID, data)
		},
	}
	defer closeRunner(runCtx, runner, runReq)
	result, err := runner.Run(runCtx, runReq)
	if err != nil {
		_ = o.Store.FailRun(runID, core.FailureToolGatewayFailed, err.Error(), core.RunFailed)
		return nil
	}
	if result.Kind == agentrunner.RunResultHeld {
		return nil
	}
	if result.Kind == agentrunner.RunResultMissingHandoff && wf.Config.Agent.MaxHandoffContinuations > 0 {
		active, activeErr := o.runIsActive(runID)
		if activeErr != nil || !active {
			return nil
		}
		_ = o.Store.AppendEvent("agent.handoff_continuation_requested", "system", &issue.ID, &runID, map[string]any{"continuation": 1, "prompt": "redacted"})
		continuationReq := runReq
		continuationReq.Prompt = handoffContinuationPrompt()
		continuationReq.IsContinuation = true
		result, err = runner.Run(runCtx, continuationReq)
		if err != nil {
			_ = o.Store.FailRun(runID, core.FailureToolGatewayFailed, err.Error(), core.RunFailed)
			return nil
		}
	}
	active, activeErr = o.runIsActive(runID)
	if activeErr != nil || !active {
		return nil
	}
	afterRunCtx, stopAfterRunContext := o.runContext(runID)
	_ = runAfterHook(afterRunCtx, o.Store, wf, ws.Path, runID, issue.ID)
	stopAfterRunContext()
	active, activeErr = o.runIsActive(runID)
	if activeErr != nil || !active {
		return nil
	}
	switch result.Kind {
	case agentrunner.RunResultFailed:
		_ = o.Store.FailRun(runID, failureCodeOrDefault(result.FailureCode, core.FailureCodexProtocolError), failureMessageOrDefault(result.FailureMessage, "runner failed"), core.RunFailed)
		return nil
	case agentrunner.RunResultMissingHandoff:
		_ = o.Store.FailRun(runID, core.FailureMissingHandoff, failureMessageOrDefault(result.FailureMessage, "handoff missing"), core.RunCompletedWithoutHandoff)
		return nil
	}
	if _, err := o.Store.GetHandoffByRun(runID); err != nil {
		_ = o.Store.FailRun(runID, core.FailureMissingHandoff, "handoff missing", core.RunCompletedWithoutHandoff)
		return nil
	}
	active, activeErr = o.runIsActive(runID)
	if activeErr != nil || !active {
		return nil
	}
	rpID, err := review.Generator{Store: o.Store}.Generate(runID)
	if err != nil {
		_ = o.Store.FailRun(runID, core.FailureReviewPacketFailed, err.Error(), core.RunFailed)
		return nil
	}
	return o.Store.CompleteRunWithReview(runID, rpID)
}

// injectReworkContext implements D4 / R16: given a Rework-dispatched
// run, locate the previous review packet, build a safe summary, and
// append a deterministic rework envelope (latest review reason + safe
// summary markdown) to the rendered prompt. It also returns a
// half-populated ReworkSnapshotRecord (without PromptSnapshotID /
// final PromptHash) that the caller stamps after CreatePromptSnapshot
// so the prompt hash matches the post-injection prompt.
//
// Cumulative diff semantic: we keep the issue's BaseSHA stable across
// iterations and re-derive CumulativeDiffSHA from the workspace's
// current HEAD vs. BaseSHA. If the workspace cannot be resolved, the
// cumulative diff SHA is left empty and the safe summary reflects
// only the previous review packet.
func (o Orchestrator) injectReworkContext(issue *core.Issue, run *core.RunAttempt, basePrompt string) (string, string, store.ReworkSnapshotRecord, error) {
	emptyRec := store.ReworkSnapshotRecord{RunID: run.ID, IssueID: run.IssueID, ReviewReason: ""}
	if issue == nil || run == nil {
		return "", "", emptyRec, fmt.Errorf("issue/run is nil")
	}
	// Locate the previous run for the issue. The most recent
	// non-active, completed run is the one whose review packet is
	// the previous review packet.
	prev, err := o.Store.LatestCompletedRunForIssue(issue.ID, run.ID)
	if err != nil {
		return "", "", emptyRec, fmt.Errorf("locate previous run: %w", err)
	}
	reviewPacketID := ""
	var prevRun *core.RunAttempt
	if prev != nil {
		prevRun = prev
		if rp, err := o.Store.LatestReviewPacketIDForRun(prev.ID); err == nil {
			reviewPacketID = rp
		}
	}
	reason := o.Store.LatestReviewReasonForIssue(issue.ID, reviewPacketID)
	if strings.TrimSpace(reason) == "" {
		reason = run.DispatchReason
	}
	if strings.TrimSpace(reason) == "" {
		reason = "manual_rework"
	}
	summary, err := review.BuildSafeSummaryFromRun(o.Store, run.ID)
	if err != nil {
		// Fallback: try the previous run.
		if prev != nil {
			summary, err = review.BuildSafeSummaryFromRun(o.Store, prev.ID)
		}
		if err != nil {
			return "", "", emptyRec, fmt.Errorf("build safe summary: %w", err)
		}
	}
	if reviewPacketID == "" {
		reviewPacketID = summary.ReviewPacketID
	}
	baseSHA := ""
	if issue.BaseSHA != nil {
		baseSHA = *issue.BaseSHA
	}
	cumulativeDiffSHA := o.computeCumulativeDiffSHA(issue, run, baseSHA)
	in := codex.ReworkContextInput{
		Issue:             issue,
		Run:               run,
		PreviousRun:       prevRun,
		ReviewPacketID:    reviewPacketID,
		ReviewReason:      reason,
		SafeSummary:       summary,
		BaseSHA:           baseSHA,
		CumulativeDiffSHA: cumulativeDiffSHA,
	}
	if issue.Workspace != nil {
		in.WorkspacePath = issue.Workspace.Path
	}
	newPrompt, promptHash, err := codex.BuildReworkPrompt(basePrompt, in)
	if err != nil {
		return "", "", emptyRec, fmt.Errorf("build rework prompt: %w", err)
	}
	rec := codex.BuildReworkSnapshotRecord(promptHash, in, "")
	return newPrompt, promptHash, rec, nil
}

func (o Orchestrator) computeCumulativeDiffSHA(issue *core.Issue, run *core.RunAttempt, baseSHA string) string {
	if issue == nil || issue.Workspace == nil || strings.TrimSpace(issue.Workspace.Path) == "" {
		return ""
	}
	root := issue.Workspace.Path
	cmd := exec.Command("git", "-C", root, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	head := strings.TrimSpace(string(out))
	if head == "" {
		return ""
	}
	h := sha256.Sum256([]byte(baseSHA + "\x00" + head))
	return hex.EncodeToString(h[:])
}

func runnerForRun(run *core.RunAttempt, wf *config.Workflow) agentrunner.Runner {
	if run.RunnerKind == "codex" {
		return &codex.Runner{Command: wf.Config.Codex.Command, ExperimentalAPI: wf.Config.Codex.ExperimentalAPI, Policy: securityPolicyFromConfig(wf.Config)}
	}
	return fake.Runner{}
}

func securityPolicyFromConfig(cfg config.EffectiveConfig) security.Policy {
	policy := security.DefaultPolicy()
	switch cfg.Approvals.Network.Default {
	case string(security.PolicyReview):
		policy.NetworkDefault = security.PolicyReview
	case string(security.PolicyDeny):
		policy.NetworkDefault = security.PolicyDeny
	}
	policy.NetworkAllowlist = append([]string{}, cfg.Approvals.Network.Allowlist...)
	policy.ProtectedPaths = append([]string{}, cfg.Approvals.ProtectedPaths...)
	return policy
}

func closeRunner(ctx context.Context, runner agentrunner.Runner, req agentrunner.RunRequest) {
	closer, ok := runner.(agentrunner.ClosableRunner)
	if !ok {
		return
	}
	_ = closer.Close(ctx, req)
}

func handoffContinuationPrompt() string {
	return "Submit the required handoff only. Do not continue implementation work or repeat the original task prompt."
}

func runnerTimeoutPolicy(wf *config.Workflow) agentrunner.TimeoutPolicy {
	return agentrunner.TimeoutPolicy{
		StartupMS: wf.Config.Codex.StartupTimeoutMS,
		TurnMS:    wf.Config.Codex.TurnTimeoutMS,
		StallMS:   wf.Config.Codex.StallTimeoutMS,
		ReadMS:    wf.Config.Codex.ReadTimeoutMS,
	}
}

func failureCodeOrDefault(code core.FailureCode, fallback core.FailureCode) core.FailureCode {
	if code == "" {
		return fallback
	}
	return code
}

func failureMessageOrDefault(message, fallback string) string {
	if strings.TrimSpace(message) == "" {
		return fallback
	}
	return message
}

func (o Orchestrator) failRunAfterHook(runID, issueID, workspacePath string, wf *config.Workflow, code core.FailureCode, message string, status core.RunStatus) {
	afterRunCtx, stopAfterRunContext := o.runContext(runID)
	_ = runAfterHook(afterRunCtx, o.Store, wf, workspacePath, runID, issueID)
	stopAfterRunContext()
	active, err := o.runIsActive(runID)
	if err != nil || !active {
		return
	}
	_ = o.Store.FailRun(runID, code, message, status)
}

func (o Orchestrator) runIsActive(runID string) (bool, error) {
	run, err := o.Store.GetRun(runID)
	if err != nil {
		return false, err
	}
	return core.IsActiveRunStatus(run.Status), nil
}

func (o Orchestrator) runContext(runID string) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	if active, err := o.runIsActive(runID); err != nil || !active {
		cancel()
	}
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				active, err := o.runIsActive(runID)
				if err != nil {
					continue
				}
				if !active {
					cancel()
					return
				}
			}
		}
	}()
	return ctx, func() {
		close(done)
		cancel()
	}
}

func (o Orchestrator) advanceRun(runID string, status core.RunStatus, fields map[string]any) bool {
	active, err := o.runIsActive(runID)
	if err != nil || !active {
		return false
	}
	if err := o.Store.UpdateRunStatus(runID, status, fields); err != nil {
		return false
	}
	active, err = o.runIsActive(runID)
	return err == nil && active
}

func runAfterHook(ctx context.Context, st *store.Store, wf *config.Workflow, cwd, runID, issueID string) error {
	return runWorkflowHook(ctx, st, wf, cwd, runID, issueID, "after_run", wf.Config.Hooks.AfterRun)
}

func runWorkflowHook(ctx context.Context, st *store.Store, wf *config.Workflow, cwd, runID, issueID, hookName string, command *string) error {
	eventPrefix := "hook." + hookName
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if command == nil || strings.TrimSpace(*command) == "" {
		return st.AppendEvent(eventPrefix+".completed", "hook", &issueID, &runID, map[string]any{"configured": false})
	}
	cmdText := *command
	if err := st.AppendEvent(eventPrefix+".started", "hook", &issueID, &runID, map[string]any{"command": "redacted"}); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", cmdText)
	cmd.Dir = cwd
	cmd.Env = hookEnv(st, runID, issueID, cwd)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		killHookProcess(cmd.Process)
		return nil
	}
	cmd.WaitDelay = 2 * time.Second
	timeout := time.Duration(wf.Config.Hooks.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	maxOutputBytes := wf.Config.Hooks.MaxOutputBytes
	if maxOutputBytes < 0 {
		maxOutputBytes = 0
	}
	output := &boundedHookOutput{limit: maxOutputBytes}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return appendHookFailureEvent(st, eventPrefix, issueID, runID, output, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return appendHookFailureEvent(st, eventPrefix, issueID, runID, output, err)
	}
	if err := cmd.Start(); err != nil {
		return appendHookFailureEvent(st, eventPrefix, issueID, runID, output, err)
	}
	var readers sync.WaitGroup
	readers.Add(2)
	go copyHookOutput(&readers, output, stdout)
	go copyHookOutput(&readers, output, stderr)
	done := make(chan error, 1)
	go func() {
		readers.Wait()
		done <- cmd.Wait()
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		if ctxErr := ctx.Err(); ctxErr != nil {
			if appendErr := appendHookOutputEvent(st, eventPrefix, issueID, runID, output); appendErr != nil {
				return errors.Join(ctxErr, appendErr)
			}
			if appendErr := st.AppendEvent(eventPrefix+".cancelled", "hook", &issueID, &runID, map[string]any{}); appendErr != nil {
				return errors.Join(ctxErr, appendErr)
			}
			return ctxErr
		}
		if err != nil {
			return appendHookFailureEvent(st, eventPrefix, issueID, runID, output, err)
		}
		if err := appendHookOutputEvent(st, eventPrefix, issueID, runID, output); err != nil {
			return err
		}
		if err := st.AppendEvent(eventPrefix+".completed", "hook", &issueID, &runID, map[string]any{}); err != nil {
			return err
		}
		return nil
	case <-ctx.Done():
		killHookProcess(cmd.Process)
		waitForHookReadersAfterKill(done, stdout, stderr)
		if err := appendHookOutputEvent(st, eventPrefix, issueID, runID, output); err != nil {
			return errors.Join(ctx.Err(), err)
		}
		if err := st.AppendEvent(eventPrefix+".cancelled", "hook", &issueID, &runID, map[string]any{}); err != nil {
			return errors.Join(ctx.Err(), err)
		}
		return ctx.Err()
	case <-timer.C:
		killHookProcess(cmd.Process)
		waitForHookReadersAfterKill(done, stdout, stderr)
		if err := appendHookOutputEvent(st, eventPrefix, issueID, runID, output); err != nil {
			return err
		}
		if err := st.AppendEvent(eventPrefix+".timeout", "hook", &issueID, &runID, map[string]any{}); err != nil {
			return err
		}
		return fmt.Errorf("%s timeout", hookName)
	}
}

func hookEnv(st *store.Store, runID, issueID, workspacePath string) []string {
	env := minimalHookHostEnv()
	if st.ProjectID != "" {
		env = append(env, "SYMPHONY_PROJECT_ID="+st.ProjectID)
	}
	if runID != "" {
		env = append(env, "SYMPHONY_RUN_ID="+runID)
	}
	if issueID != "" {
		env = append(env, "SYMPHONY_ISSUE_ID="+issueID)
	}
	if workspacePath != "" {
		env = append(env, "SYMPHONY_WORKSPACE_PATH="+workspacePath)
	}
	return env
}

func minimalHookHostEnv() []string {
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

func appendHookFailureEvent(st *store.Store, eventPrefix, issueID, runID string, output *boundedHookOutput, cause error) error {
	if err := appendHookOutputEvent(st, eventPrefix, issueID, runID, output); err != nil {
		return errors.Join(cause, err)
	}
	if err := st.AppendEvent(eventPrefix+".failed", "hook", &issueID, &runID, map[string]any{"error": cause.Error()}); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func appendHookOutputEvent(st *store.Store, eventPrefix, issueID, runID string, output *boundedHookOutput) error {
	return st.AppendEvent(eventPrefix+".output", "hook", &issueID, &runID, map[string]any{"output": redactedHookOutput(output.String())})
}

func redactedHookOutput(output string) string {
	if output == "" {
		return ""
	}
	return "redacted"
}

func waitForHookReadersAfterKill(done <-chan error, stdout, stderr io.Closer) {
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = stdout.Close()
		_ = stderr.Close()
		<-done
	}
}

func copyHookOutput(wg *sync.WaitGroup, output *boundedHookOutput, r io.Reader) {
	defer wg.Done()
	_, _ = io.Copy(output, r)
}

func killHookProcess(process *os.Process) {
	if process == nil {
		return
	}
	if pgid, err := syscall.Getpgid(process.Pid); err == nil {
		if err := syscall.Kill(-pgid, syscall.SIGKILL); err == nil {
			return
		}
	}
	_ = process.Kill()
}

type boundedHookOutput struct {
	mu    sync.Mutex
	limit int
	buf   []byte
}

func (b *boundedHookOutput) Write(p []byte) (int, error) {
	n := len(p)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit <= 0 || len(b.buf) >= b.limit {
		return n, nil
	}
	remaining := b.limit - len(b.buf)
	if len(p) > remaining {
		p = p[:remaining]
	}
	b.buf = append(b.buf, p...)
	return n, nil
}

func (b *boundedHookOutput) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

func (o Orchestrator) Tick() error {
	wf, err := config.Load(o.Store.RepoRoot)
	if err != nil {
		return err
	}
	if !wf.Validation.Valid {
		return core.NewError(core.ErrWorkflowInvalid, "WORKFLOW.md is invalid", map[string]any{"errors": wf.Validation.Errors, "warnings": wf.Validation.Warnings})
	}
	issues, err := o.Store.ListIssues(store.ListIssueOptions{States: wf.Config.Tracker.DispatchCandidateStates, Limit: 50, Sort: "priority"})
	if err != nil {
		return err
	}
	for _, iss := range issues {
		if _, err := o.dispatchIssueWithWorkflow(wf, iss.Identifier, "scheduler"); err != nil && !isSchedulerSkippableDispatchError(err) {
			return err
		}
	}
	return nil
}

func isSchedulerSkippableDispatchError(err error) bool {
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.Code {
	case core.ErrInvalidRequest,
		core.ErrInvalidStateTransition,
		core.ErrIssueBlocked,
		core.ErrIssueDispatchPaused,
		core.ErrIssueAlreadyRunning,
		core.ErrConcurrencyLimitReached:
		return true
	default:
		return false
	}
}
