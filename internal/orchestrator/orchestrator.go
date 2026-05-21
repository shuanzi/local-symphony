package orchestrator

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"local-symphony/internal/agent/fake"
	"local-symphony/internal/config"
	"local-symphony/internal/core"
	"local-symphony/internal/review"
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
	if !wf.Validation.Valid {
		return nil, core.NewError(core.ErrWorkflowInvalid, "WORKFLOW.md is invalid", map[string]any{"errors": wf.Validation.Errors, "warnings": wf.Validation.Warnings})
	}
	run, err := o.Store.ClaimRun(issueRef, reasonOrDefault(reason, "manual"), "fake", wf.Config.Agent.MaxConcurrentAgents)
	if err != nil {
		return nil, err
	}
	issue, err := o.Store.GetIssue(run.IssueID)
	if err != nil {
		return nil, err
	}
	if fake.SelectedOutcome() == fake.OutcomeHold {
		if err := o.Store.UpdateRunStatus(run.ID, core.RunRunning, map[string]any{"started_at": core.Now()}); err != nil {
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

func reasonOrDefault(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
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
	ws, err := mgr.Prepare(run, issue)
	if err != nil {
		_ = o.Store.FailRun(runID, core.FailureWorkspacePrepareFailed, err.Error(), core.RunFailed)
		return nil
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
	ph := sha256.Sum256([]byte(prompt))
	ch := sha256.Sum256([]byte(issue.ID + runID))
	_, _ = o.Store.CreatePromptSnapshot(runID, wfID, hex.EncodeToString(ch[:]), hex.EncodeToString(ph[:]), filepath.Join(o.Store.RepoRoot, ".symphony", "artifacts", issue.Identifier, runID))
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
	switch fake.SelectedOutcome() {
	case fake.OutcomeFailure:
		code := core.FailureCodexProtocolError
		if v := os.Getenv("SYMPHONY_FAKE_FAILURE_CODE"); v != "" {
			code = core.FailureCode(v)
		}
		_ = runAfterHook(o.Store, wf, ws.Path, runID, issue.ID)
		if !o.runIsActive(runID) {
			return nil
		}
		_ = o.Store.FailRun(runID, code, "fake runner failure", core.RunFailed)
		return nil
	case fake.OutcomeMissingHandoff:
		_ = runAfterHook(o.Store, wf, ws.Path, runID, issue.ID)
		if !o.runIsActive(runID) {
			return nil
		}
		_ = o.Store.FailRun(runID, core.FailureMissingHandoff, "fake runner completed without handoff", core.RunCompletedWithoutHandoff)
		return nil
	default:
		if err := fake.Run(ws.Path, issue.Identifier, token, gw); err != nil {
			_ = o.Store.FailRun(runID, core.FailureToolGatewayFailed, err.Error(), core.RunFailed)
			return nil
		}
	}
	if !o.runIsActive(runID) {
		return nil
	}
	_ = runAfterHook(o.Store, wf, ws.Path, runID, issue.ID)
	if !o.runIsActive(runID) {
		return nil
	}
	if _, err := o.Store.GetHandoffByRun(runID); err != nil {
		_ = o.Store.FailRun(runID, core.FailureMissingHandoff, "handoff missing", core.RunCompletedWithoutHandoff)
		return nil
	}
	if !o.runIsActive(runID) {
		return nil
	}
	rpID, err := review.Generator{Store: o.Store}.Generate(runID)
	if err != nil {
		_ = o.Store.FailRun(runID, core.FailureReviewPacketFailed, err.Error(), core.RunFailed)
		return nil
	}
	return o.Store.CompleteRunWithReview(runID, rpID)
}

func (o Orchestrator) runIsActive(runID string) bool {
	run, err := o.Store.GetRun(runID)
	return err == nil && core.IsActiveRunStatus(run.Status)
}

func (o Orchestrator) advanceRun(runID string, status core.RunStatus, fields map[string]any) bool {
	if !o.runIsActive(runID) {
		return false
	}
	if err := o.Store.UpdateRunStatus(runID, status, fields); err != nil {
		return false
	}
	return o.runIsActive(runID)
}

func runAfterHook(st *store.Store, wf *config.Workflow, cwd, runID, issueID string) error {
	if wf.Config.Hooks.AfterRun == nil || strings.TrimSpace(*wf.Config.Hooks.AfterRun) == "" {
		_ = st.AppendEvent("hook.after_run.completed", "hook", &issueID, &runID, map[string]any{"configured": false})
		return nil
	}
	cmdText := *wf.Config.Hooks.AfterRun
	_ = st.AppendEvent("hook.after_run.started", "hook", &issueID, &runID, map[string]any{"command": "redacted"})
	cmd := exec.Command("/bin/sh", "-c", cmdText)
	cmd.Dir = cwd
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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
		_ = st.AppendEvent("hook.after_run.output", "hook", &issueID, &runID, map[string]any{"output": output.String()})
		_ = st.AppendEvent("hook.after_run.failed", "hook", &issueID, &runID, map[string]any{"error": err.Error()})
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = st.AppendEvent("hook.after_run.output", "hook", &issueID, &runID, map[string]any{"output": output.String()})
		_ = st.AppendEvent("hook.after_run.failed", "hook", &issueID, &runID, map[string]any{"error": err.Error()})
		return err
	}
	if err := cmd.Start(); err != nil {
		_ = st.AppendEvent("hook.after_run.output", "hook", &issueID, &runID, map[string]any{"output": output.String()})
		_ = st.AppendEvent("hook.after_run.failed", "hook", &issueID, &runID, map[string]any{"error": err.Error()})
		return err
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
		_ = st.AppendEvent("hook.after_run.output", "hook", &issueID, &runID, map[string]any{"output": output.String()})
		if err != nil {
			_ = st.AppendEvent("hook.after_run.failed", "hook", &issueID, &runID, map[string]any{"error": err.Error()})
			return err
		}
		_ = st.AppendEvent("hook.after_run.completed", "hook", &issueID, &runID, map[string]any{})
		return nil
	case <-timer.C:
		killHookProcess(cmd.Process)
		waitForHookReadersAfterKill(done, stdout, stderr)
		_ = st.AppendEvent("hook.after_run.output", "hook", &issueID, &runID, map[string]any{"output": output.String()})
		_ = st.AppendEvent("hook.after_run.timeout", "hook", &issueID, &runID, map[string]any{})
		return fmt.Errorf("after_run timeout")
	}
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
	issues, err := o.Store.ListIssues(store.ListIssueOptions{States: []string{"Ready", "Rework"}, Limit: 50, Sort: "priority"})
	if err != nil {
		return err
	}
	for _, iss := range issues {
		if iss.DispatchPaused || iss.ActiveRunID != nil || len(iss.BlockedBy) > 0 {
			continue
		}
		_, _ = o.DispatchIssue(iss.Identifier, "scheduler")
	}
	return nil
}
