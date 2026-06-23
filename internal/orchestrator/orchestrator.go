package orchestrator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	var promptHash string
	var reworkRec store.ReworkSnapshotRecord
	var reworkErr error
	if run.SourceIssueState == core.StateRework {
		prompt, promptHash, reworkRec, reworkErr = o.injectReworkContext(issue, run, prompt, wf)
		if reworkErr != nil {
			_ = o.Store.FailRun(runID, core.FailurePromptRenderFailed, fmt.Sprintf("rework context: %v", reworkErr), core.RunFailed)
			return nil
		}
		// Write a metadata-only redacted artifact (NOT the raw
		// rendered prompt). PR #27 / D4 F2: the previous
		// implementation wrote "[redacted]\n" + the full rendered
		// prompt body, violating the raw-prompt logging boundary
		// (rendered prompts can echo issue description + workflow
		// prompt). When a Rework run failed before the review
		// packet was generated, the file stayed on disk for the
		// next operator — and the review packet generator never
		// overwrites this dedicated rework artifact.
		promptLen := len(prompt)
		promptHashBytes := sha256.Sum256([]byte(prompt))
		promptHashHex := hex.EncodeToString(promptHashBytes[:])
		redactedMeta := fmt.Sprintf("# redacted\n\nThe rendered rework prompt is not persisted to disk in raw form.\n\n- redaction: metadata-only\n- prompt_length_bytes: %d\n- prompt_sha256: %s\n- review_reason_redacted: false (see rework_snapshots row)\n", promptLen, promptHashHex)
		if err := os.WriteFile(filepath.Join(promptDir, "rework_prompt.redacted.md"), []byte(redactedMeta), 0o644); err != nil {
			_ = o.Store.FailRun(runID, core.FailurePromptRenderFailed, fmt.Sprintf("write redacted rework prompt: %v", err), core.RunFailed)
			return nil
		}
		if err := os.WriteFile(filepath.Join(promptDir, "rendered_prompt.redacted.md"), []byte(redactedMeta), 0o644); err != nil {
			_ = o.Store.FailRun(runID, core.FailurePromptRenderFailed, fmt.Sprintf("write redacted rendered prompt: %v", err), core.RunFailed)
			return nil
		}
		// ph now refers to the hash of the prompt *with* rework
		// injection; this is what the agent will actually see.
		ph := sha256.Sum256([]byte(prompt))
		ch := sha256.Sum256([]byte(issue.ID + runID))
		psID, psErr := o.Store.CreatePromptSnapshot(runID, wfID, hex.EncodeToString(ch[:]), hex.EncodeToString(ph[:]), promptRoot)
		if psErr != nil {
			_ = o.Store.FailRun(runID, core.FailurePromptRenderFailed, fmt.Sprintf("create prompt snapshot: %v", psErr), core.RunFailed)
			return nil
		}
		reworkRec.PromptSnapshotID = psID
		reworkRec.PromptHash = promptHash
		if _, err := o.Store.CreateReworkSnapshot(reworkRec); err != nil {
			_ = o.Store.FailRun(runID, core.FailurePromptRenderFailed, fmt.Sprintf("create rework snapshot: %v", err), core.RunFailed)
			return nil
		}
	} else {
		// PR #27 / D4 F2: write a metadata-only redacted artifact
		// (not the raw rendered prompt). The previous implementation
		// wrote the full rendered prompt body; a redaction label
		// does not satisfy the raw-prompt logging boundary.
		promptLen := len(prompt)
		promptHashBytes := sha256.Sum256([]byte(prompt))
		promptHashHex := hex.EncodeToString(promptHashBytes[:])
		redactedMeta := fmt.Sprintf("# redacted\n\nThe rendered prompt is not persisted to disk in raw form.\n\n- redaction: metadata-only\n- prompt_length_bytes: %d\n- prompt_sha256: %s\n", promptLen, promptHashHex)
		_ = os.WriteFile(filepath.Join(promptDir, "rendered_prompt.redacted.md"), []byte(redactedMeta), 0o644)
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
func (o Orchestrator) injectReworkContext(issue *core.Issue, run *core.RunAttempt, basePrompt string, wf *config.Workflow) (string, string, store.ReworkSnapshotRecord, error) {
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
	// PR #27 / D4 F3: when the previous run is known, build the
	// safe summary directly from it. The previous implementation
	// tried the current run first and only fell back to the
	// previous run on error; that meant a Rework-dispatched run
	// (SourceIssueState=Rework, no review packet of its own) would
	// surface a SafeSummary stamped with the *current* run's
	// source_issue_state — corrupting snapshot metadata.
	summary, err := review.BuildSafeSummaryFromIssueWithPrev(o.Store, issue, run, prevRun)
	if err != nil {
		return "", "", emptyRec, fmt.Errorf("build safe summary: %w", err)
	}
	if reviewPacketID == "" {
		reviewPacketID = summary.ReviewPacketID
	}
	baseSHA := ""
	if issue.BaseSHA != nil {
		baseSHA = *issue.BaseSHA
	}
	cumulativeDiffSHA := o.computeCumulativeDiffSHA(issue, run, baseSHA, wf.Config.Approvals.ProtectedPaths)
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

// filteredTrackedDiff returns the git diff of tracked changes against
// HEAD, omitting any files that match security.IsProtectedPath. This
// prevents protected-tracked files (e.g. committed-but-modified .env)
// from leaking their content into cumulative_diff_sha hashes that are
// persisted in rework_snapshots.
func filteredTrackedDiff(root string, protectedPaths []string) []byte {
	safe, err := filteredTrackedDiffPathspecs(root, protectedPaths)
	if err != nil {
		return nil
	}
	if len(safe) == 0 {
		return nil
	}
	// D4 / R16: pass --literal-pathspecs so a safe changed file
	// literally named `*` (or any glob/magic pathspec) is treated as
	// a literal path, not expanded by git. Without this, a file named
	// `*` passed to `git diff HEAD -- '*'` would act as a glob and
	// could emit a modified protected .env patch into cumulative_diff_sha.
	// Mirrors internal/gitx/gitx.go DiffBinaryPaths.
	args := append([]string{"-C", root, "--literal-pathspecs", "diff", "--find-renames", "--find-copies", "--find-copies-harder", "HEAD", "--"}, safe...)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil
	}
	return out
}

func filteredTrackedDiffPathspecs(root string, protectedPaths []string) ([]string, error) {
	out, err := exec.Command("git", "-C", root, "diff", "--name-status", "--find-renames", "--find-copies", "--find-copies-harder", "-z", "HEAD").Output()
	if err != nil || len(out) == 0 {
		return nil, err
	}
	fields := strings.Split(string(out), "\x00")
	if len(fields) > 0 && fields[len(fields)-1] == "" {
		fields = fields[:len(fields)-1]
	}
	type diffRecord struct {
		status string
		paths  []string
	}
	records := []diffRecord{}
	for i := 0; i < len(fields); {
		status := fields[i]
		i++
		pathCount := 1
		if strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C") {
			pathCount = 2
		}
		if i+pathCount > len(fields) {
			return nil, fmt.Errorf("parse git diff name-status")
		}
		paths := fields[i : i+pathCount]
		i += pathCount
		records = append(records, diffRecord{status: status, paths: paths})
	}
	// D4 / R16 round 4 — SECURITY-FIRST, fail-closed on ANY protected
	// delete, rename, or copy. An added tracked file (status A) whose
	// path is NOT protected could still be a verbatim copy of a
	// deleted/renamed/copied protected file's bytes: when git does not
	// detect a rename/copy from the protected file to this A file (e.g.
	// content was rewritten past the similarity threshold, or a copy
	// with no rename), `git diff --name-status` reports the protected
	// file as `D`/`R`/`C` and `A <public>` as a separate record. The
	// per-record path check below would keep the A record, and
	// `git diff HEAD -- public` would emit its bytes (which equal the
	// protected secret) into cumulative_diff_sha.
	//
	// Round 1/2 tried per-file content-hash matching of the A record's
	// index blob (`git show :<path>`) against the deleted protected set.
	// Round 2 found P1 leak #2: `git show :<path>` reads the INDEX blob
	// but `git diff HEAD -- <path>` emits the WORKTREE version. For an
	// `AM` record (staged safe content, worktree overwritten with
	// protected bytes), the index blob is safe (no match) so the path is
	// kept, but the diff emits the protected worktree bytes → leak.
	//
	// Round 4 found P1 leak #3: a protected file staged as a RENAME
	// (`git mv .env renamed.txt`, an `R` record, NOT `D`) was invisible
	// to round 3's `--diff-filter=D` probe, so hashesUnknown stayed
	// false and the A record was kept → leak. deletedProtectedContentHashes
	// now treats a protected R/C SOURCE the same as a protected D.
	//
	// Content-hash matching cannot be made safe in the
	// protected-delete/rename/copy case (a protected file's
	// pre-operation WORKTREE content — the bytes that could have been
	// copied — is unrecoverable when unstaged modifications existed, and
	// undetectable after the operation; see
	// deletedProtectedContentHashes). So we FAIL CLOSED: when ANY
	// protected tracked file is deleted, renamed, or copied
	// (hashesUnknown=true) we SKIP ALL A records — their (possibly
	// protected) diff bytes never enter cumulative_diff_sha. This also
	// closes P1#2's index-vs-worktree mismatch: the A file's diff never
	// runs.
	//
	// D4 / R16 round 6 — closes the MODIFIED-source-copy P1 leak. When
	// NO protected file is deleted/renamed/copied (the common case,
	// hashesUnknown=false) but a protected file is MODIFIED then copied
	// into a new A file while the source REMAINS (edit .env, `cp .env
	// public.txt`, `git add public.txt`), git reports `M .env` +
	// `A public.txt` — NOT a `C` record — so the round-5 copy-aware
	// R/C-source check does not fire and the A record was kept, leaking
	// the modified protected bytes. The protected source REMAINS, so its
	// content is fully recoverable (workspace + HEAD + index); we
	// content-hash-match each A record's WORKSPACE content against the
	// set of all recoverable protected versions and SKIP the A record on
	// a match. See existingProtectedContentHashes for the full rationale
	// and the documented filler-copy residual risk.
	_, hashesUnknown := deletedProtectedContentHashes(root, protectedPaths)
	// D4/R16 round-13 (codex finding M): always build
	// existingHashes, even when hashesUnknown=true. The
	// HEAD/index versions of deleted protected files are
	// still recoverable via git show and provide the hash
	// set for content-matching M/T records.
	var existingHashes map[string]bool
	existingHashes = existingProtectedContentHashes(root, protectedPaths)
	safe := []string{}
	for _, record := range records {
		// Per-record protected check: a record whose source or
		// destination path is protected (including a rename/copy away
		// from a protected .env) is skipped entirely so its bytes
		// never enter the diff.
		protected := false
		for _, path := range record.paths {
			if security.IsProtectedPathWithConfig(path, protectedPaths) {
				protected = true
				break
			}
		}
		if protected {
			continue
		}
		// Added tracked file (status A) or copy destination (status C):
		// fail-closed on any protected delete/rename/copy/typechange.
		// `record.paths[len-1]` is the destination path git diff would
		// emit.
		//
		// D4/R16 round-9: the C case closes the symlinked-protected-source
		// leak (finding B). `git diff --find-copies-harder` reports a
		// verbatim copy of a protected file as `C<score> <src> <dst>` —
		// NOT `A` — when git can match it to a tracked source blob. When
		// the protected file is a SYMLINK to a regular secret (`.env ->
		// shared/env`), the copy is detected as `C shared/env public.txt`
		// (source = the non-protected regular target), so the per-record
		// protected-source check does NOT fire, the A-record content-hash
		// check does NOT run (status is C, not A), and `git diff HEAD --
		// public.txt` hashed the protected bytes into cumulative_diff_sha.
		// Round 9's Stat-follows-symlinks fix puts the symlinked protected
		// bytes into existingHashes; we now content-hash-check C
		// destinations too (same fail-closed + content-hash logic as A),
		// so the copy is suppressed.
		if strings.HasPrefix(record.status, "A") || strings.HasPrefix(record.status, "C") {
			if hashesUnknown {
				// A protected tracked file was deleted/renamed/copied/
				// typechanged → cannot rule out this A/C file holding
				// modified-then-copied protected bytes → skip (fail
				// closed). The index-vs-worktree mismatch (P1#2) is moot:
				// the file's diff never runs.
				continue
			}
			// Round 6/9: no protected delete/rename/copy/typechange, but the
			// A/C file may hold protected bytes — a copy of a MODIFIED
			// protected file (A, source remains), or a verbatim copy git
			// matched to a non-protected source that equals protected
			// content reachable via a protected symlink (C, finding B).
			// Hash the file's WORKSPACE content (the bytes `git diff
			// HEAD -- <path>` emits) and skip it if the hash matches any
			// recoverable protected version. On read error, SKIP (fail
			// closed for that file — cannot prove it is safe).
			//
			// Fast path: when there are NO existing protected files at all
			// (len(existingHashes)==0), no A/C file can hold protected
			// content, so we skip the read+hash and keep the record
			// directly — full common-case correlation with zero per-file
			// I/O.
			dst := record.paths[len(record.paths)-1]
			if len(existingHashes) > 0 {
				// D4/R16 round-18 (codex finding R18-1): for an A/C
				// symlink, git diff emits the symlink TARGET TEXT, not
				// the target file content. hashWorkspaceFile follows
				// symlinks (os.Stat) and hashes the target file — but
				// the emitted bytes are the target text. When the
				// target text is protected content but also names a
				// benign regular file, hashWorkspaceFile would hash the
				// benign file, miss the protected-content match, and
				// keep the A/C record → protected target text folded
				// into cumulative_diff_sha. Hash the target text first
				// via os.Readlink (same approach as the M/T/R branches).
				h, ok := hashTypechangeEmittedBytes(root, dst)
				if !ok {
					continue
				}
				if existingHashes[h] {
					continue
				}
			}
		}
		// D4/R16 round-8 — modified tracked file (status M): a tracked
		// non-protected file overwritten with protected bytes (`cp .env
		// config.txt`, config.txt already tracked → `M config.txt`) is
		// reported by `git diff --name-status` as `M`, NOT `A`, so the
		// A-only check missed it and `git diff HEAD -- config.txt` hashed
		// the protected bytes into cumulative_diff_sha. The M record's
		// workspace content matches an existing protected version's hash,
		// so the same content-hash check suppresses it. This is the
		// source-REMAINS case (unknown=false). When hashesUnknown=true we
		// now ALSO fail-closed M records (round-13 finding M) — a
		// protected delete whose bytes were copied onto a tracked M
		// file (`cp .env config.txt && git rm .env`) would otherwise
		// leak into cumulative_diff_sha.
		if strings.HasPrefix(record.status, "M") {
			if hashesUnknown {
				// D4/R16 round-13 (codex finding M): when a protected
				// tracked file was deleted/renamed/copied/typechanged
				// (hashesUnknown=true), the pre-operation bytes are
				// unrecoverable. An M record whose workspace content
				// was overwritten with protected bytes (`cp .env
				// config.txt && git rm .env`) would leak into
				// cumulative_diff_sha. Fail closed: skip ALL M records.
				continue
			}
			if len(existingHashes) > 0 {
				dst := record.paths[len(record.paths)-1]
				// D4/R16 round-13 (codex finding O): for a modified
				// symlink, git diff emits the symlink TARGET TEXT,
				// not the target file content. Check the target
				// text first via os.Readlink before falling back
				// to hashWorkspaceFile (which follows symlinks).
				full := filepath.Join(root, dst)
				if target, rerr := os.Readlink(full); rerr == nil {
					// M file is a symlink → git diff emits
					// target text. Hash the target text.
					h2 := sha256.Sum256([]byte(target))
					h := hex.EncodeToString(h2[:])
					if existingHashes[h] {
						continue
					}
				} else {
					h, ok := hashWorkspaceFile(full)
					if !ok {
						// Cannot read → cannot prove
						// safe → skip (fail closed for
						// THIS file).
						continue
					}
					if existingHashes[h] {
						continue
					}
				}
			}
		}
		// D4/R16 round-10 (codex finding F) — typechange (status T) on a
		// NON-protected path. A tracked non-protected file whose type
		// changed (e.g. regular file → symlink) is reported as `T
		// config.txt`; `git diff HEAD -- config.txt` emits the symlink
		// TARGET text, so a typechange whose target is copied from a
		// protected file (`rm config.txt; ln -s "$(cat .env)" config.txt`)
		// hashes the protected bytes into cumulative_diff_sha. (A
		// typechange on a PROTECTED path already triggers hashesUnknown
		// via deletedProtectedContentHashes' --diff-filter=DRCT; this
		// handles the non-protected path.) Like the M guard, this only
		// content-hash-checks when !hashesUnknown; we do NOT blanket
		// fail-closed T records (preserve an unrelated typechange). For a
		// symlink, hash the target text (what git diff emits); otherwise
		// hash the workspace file content.
		if strings.HasPrefix(record.status, "T") {
			if hashesUnknown {
				// D4/R16 round-17 (codex finding R17-2): a protected
				// file was deleted/renamed/copied/typechanged, so the
				// pre-operation worktree bytes may be unrecoverable.
				// A typechanged non-protected file (e.g. a symlink whose
				// target is modified-then-deleted protected bytes) can
				// hold those bytes, and existingHashes is nil in this
				// mode so the content-hash match below cannot run. Fail
				// closed: skip T records while hashesUnknown=true
				// (mirrors the A/C/M fail-closed).
				continue
			}
			if len(existingHashes) > 0 {
				dst := record.paths[len(record.paths)-1]
				h, ok := hashTypechangeEmittedBytes(root, dst)
				if !ok {
					// Cannot read the emitted bytes → cannot prove they
					// are safe → skip (fail closed for THIS file), mirroring
					// the M guard.
					continue
				}
				if existingHashes[h] {
					// Emitted bytes match a recoverable version of an
					// existing protected file → skip so its bytes never
					// enter the diff.
					continue
				}
			}
		}
		// D4/R16 round-16 (codex finding R16-3) — renamed destination
		// (status R). A tracked non-protected file renamed to a new path
		// whose destination carries copied protected bytes (`cat .env >>
		// old.txt; git mv old.txt new.txt; git rm -f .env`) is reported
		// by `git diff --name-status --find-renames` as `R<score> <src>
		// <dst>`. The destination is not an A/C/M/T record, so the guards
		// above did not cover it and `git diff HEAD -- <dst>` hashed the
		// protected bytes into cumulative_diff_sha. When the rename SOURCE
		// is protected the per-record protected-source check above already
		// skipped it; this handles a NON-protected source whose
		// DESTINATION carries protected bytes. When hashesUnknown=true
		// fail-closed the destination (mirror of the A/C fail-closed);
		// otherwise content-hash-match the destination's emitted bytes
		// (symlink target text first, then workspace content) against
		// existingHashes.
		if strings.HasPrefix(record.status, "R") {
			if hashesUnknown {
				continue
			}
			if len(existingHashes) > 0 {
				dst := record.paths[len(record.paths)-1]
				h, ok := hashTypechangeEmittedBytes(root, dst)
				if !ok {
					continue
				}
				if existingHashes[h] {
					continue
				}
			}
		}
		safe = append(safe, record.paths[len(record.paths)-1])
	}
	return safe, nil
}

func (o Orchestrator) computeCumulativeDiffSHA(issue *core.Issue, run *core.RunAttempt, baseSHA string, protectedPaths []string) string {
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
	// PR #27 / D4 F1: incorporate the worktree's uncommitted state
	// (status --porcelain + diff content) into the cumulative diff
	// SHA. Without this, two reworks taken against the same
	// base+HEAD but with different uncommitted agent work produce
	// identical cumulative_diff_sha values, which breaks prompt /
	// diagnostic correlation between successive reworks.
	statusOut, _ := exec.Command("git", "-C", root, "status", "--porcelain=v1", "-z", "-uall").Output()
	diffOut := filteredTrackedDiff(root, protectedPaths)
	untrackedDigest := cumulativeUntrackedDigest(root, protectedPaths)
	h := sha256.Sum256([]byte(baseSHA + "\x00" + head + "\x00" + string(statusOut) + "\x00" + string(diffOut) + "\x00" + untrackedDigest))
	return hex.EncodeToString(h[:])
}

// isProtectedUntrackedPath reports whether a workspace-relative
// untracked file path looks like a protected secret/credential file
// whose content must not be hashed into cumulative_diff_sha.
// Uses security.IsProtectedPath (the same logic as reviewSafePath
// in internal/review). Substring heuristics for keywords like
// "secret", "token", "key", "credential" are intentionally NOT used
// because they cause false positives on ordinary files
// (e.g. test_tokenizer.go, api_key_utils.go, tokenbucket.go).
func isProtectedUntrackedPath(rel string, protectedPaths []string) bool {
	return security.IsProtectedPathWithConfig(rel, protectedPaths)
}

func cumulativeUntrackedDigest(root string, protectedPaths []string) string {
	out, err := exec.Command("git", "-C", root, "ls-files", "--others", "--exclude-standard", "-z").Output()
	if err != nil || len(out) == 0 {
		return ""
	}
	// D4 / R16 round 4 — SECURITY-FIRST, fail-closed on ANY protected
	// delete, rename, or copy. deletedProtectedContentHashes now returns:
	//   - (nil, unknown=true) when ANY protected tracked file is deleted,
	//     renamed, or copied (D on a protected path, or R/C whose source
	//     path is protected);
	//   - (empty non-nil, unknown=false) when NO protected file is
	//     deleted/renamed/copied (the common case).
	//
	// Protected-operation case (hashesUnknown=true): a protected file's
	// pre-operation WORKTREE content — the bytes that could have been
	// copied into an untracked file — is unrecoverable when unstaged
	// modifications existed, and that is undetectable after the
	// operation. Content-hash matching therefore CANNOT be made safe, so
	// we FAIL CLOSED: for EVERY non-path-protected untracked file we
	// write a SENTINEL (path + mode + fixed marker, NO content, NO
	// content hash). This preserves path-level (worktree-level)
	// correlation but NOT content-level correlation, and protected bytes
	// never enter the digest. Symlink targets are also suppressed via
	// the sentinel (a symlink could point at a protected file). No file
	// content is read in this branch.
	//
	// Common case (no protected operation, hashesUnknown=false): untracked
	// regular-file content is STREAMED into a sha256 hasher (bounded
	// memory via io.Copy) and the hex hash is written — full
	// content-level correlation. Symlink targets are written as before.
	// The OOM gate `len(protectedHashes) > 0` is now always false in the
	// protected-operation case (the set is nil there) and always false in
	// the common case (empty set), so suppression is driven purely by
	// hashesUnknown; we keep the streaming hash for the common case.
	//
	// D4 / R16 round 6 — closes the untracked MODIFIED-source-copy P1 leak.
	// When no protected file is deleted/renamed/copied but a protected file
	// is MODIFIED then copied into an untracked file (edit .env, `cp .env
	// safe.txt`, source REMAINS), the round-4/5 logic hashed safe.txt's
	// content into the digest, leaking the modified protected bytes. The
	// protected source REMAINS, so we content-hash-match each untracked
	// regular file against existingProtectedContentHashes (workspace+HEAD+
	// index of all existing protected files): on a match we write a SENTINEL
	// (suppress content); otherwise we write the content hash. The file is
	// read ONCE and the hash is reused for both the match check and the
	// digest write (bounded memory, no double read).
	_, hashesUnknown := deletedProtectedContentHashes(root, protectedPaths)
	// D4/R16 round-13 (codex finding M): always build
	// existingHashes, even when hashesUnknown=true.
	var existingHashes map[string]bool
	existingHashes = existingProtectedContentHashes(root, protectedPaths)
	paths := strings.Split(string(out), "\x00")
	paths = paths[:len(paths)-1]
	sort.Strings(paths)
	h := sha256.New()
	wrote := false
	for _, rel := range paths {
		if rel == "" {
			continue
		}
		// PR #27 / D4 F5c: skip protected untracked files
		// (e.g. .env, id_rsa, secrets.txt). Their content
		// must not leak into cumulative_diff_sha which is
		// persisted in rework_snapshots and rendered into
		// prompts. See isProtectedUntrackedPath below.
		if isProtectedUntrackedPath(rel, protectedPaths) {
			continue
		}
		path := filepath.Join(root, rel)
		info, err := os.Lstat(path)
		if err != nil {
			continue
		}
		wrote = true
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(info.Mode().String()))
		_, _ = h.Write([]byte{0})
		if hashesUnknown {
			// FAIL CLOSED: a protected tracked file was
			// deleted/renamed/copied, so the pre-operation worktree
			// content of ANY untracked file is suspect (it could be a
			// copy of modified-then-deleted/renamed/copied protected
			// bytes). Suppress content for ALL non-path-protected
			// untracked files — regular files AND symlinks (a symlink
			// could target a protected file) — by writing a fixed
			// sentinel marker. No file content is read. The path and
			// mode are still written above, so the file's existence is
			// reflected (path-level correlation) without leaking any
			// bytes.
			_, _ = h.Write([]byte("suppressed:deleted-protected-content-match"))
			_, _ = h.Write([]byte{0})
			continue
		}
		// Common case (no protected delete): hash the untracked regular
		// file's content by STREAMING into a sha256 hasher (bounded
		// memory via io.Copy). The hash is reused for BOTH the
		// protected-content-match check AND the digest write, so the file
		// is read ONCE. Non-regular files (symlinks) write their target
		// as before (no protected delete → no suspicion that a target
		// points at protected content; content-hash-match is for regular
		// files).
		//
		// Round 6: if the untracked regular file's content hash matches an
		// existing protected file's recoverable version (workspace/HEAD/
		// index), it is a copy of protected content → write a SENTINEL
		// (suppress content) so the protected bytes never enter the
		// digest. Otherwise write the content hash (full correlation).
		if info.Mode().IsRegular() {
			// Stream the file's content into a sha256 hasher ONCE
			// (bounded memory via io.Copy) and reuse the resulting hex
			// hash for both the protected-content-match check and the
			// digest write (no double read).
			fileHash, ok := hashWorkspaceFile(path)
			if !ok {
				// Read error: fail closed for this file — write a
				// sentinel so we neither leak nor correlate its content.
				_, _ = h.Write([]byte("suppressed:read-error"))
				_, _ = h.Write([]byte{0})
				continue
			}
			if len(existingHashes) > 0 && existingHashes[fileHash] {
				// Untracked file is a copy of existing protected content
				// (including a MODIFIED protected source) → suppress.
				_, _ = h.Write([]byte("suppressed:existing-protected-content-match"))
				_, _ = h.Write([]byte{0})
				continue
			}
			_, _ = h.Write([]byte(fileHash))
		} else if info.Mode()&os.ModeSymlink != 0 {
			if target, err := os.Readlink(path); err == nil {
				// D4/R16 round-12 (codex finding K): an
				// untracked symlink whose target text is
				// copied from a protected file must not
				// leak into cumulative_diff_sha.
				// Hash-match the target text against
				// existingHashes; on match write a sentinel.
				if len(existingHashes) > 0 {
					h2 := sha256.Sum256([]byte(target))
					targetHash := hex.EncodeToString(h2[:])
					if existingHashes[targetHash] {
						_, _ = h.Write([]byte("suppressed:existing-protected-content-match"))
						_, _ = h.Write([]byte{0})
						continue
					}
				}
				_, _ = h.Write([]byte(target))
			}
		}
		_, _ = h.Write([]byte{0})
	}
	if !wrote {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// deletedProtectedContentHashes reports whether the worktree state is
// safe for content-level correlation of untracked / added-tracked files
// against deleted protected content.
//
// D4 / R16 round 4 — SECURITY-FIRST, fail-closed on ANY protected delete,
// rename, OR copy.
//
// Round 3 failed closed only when a protected tracked file was DELETED
// (a `D` record from `git diff --diff-filter=D`). Round 3's codex review
// found P1 leak #3: when a protected file is staged as a RENAME or COPY
// (e.g. `git mv .env renamed.txt`, reported as an `R` record, NOT a `D`
// record) and a separate staged ADDED file (`A`) contains the copied
// protected bytes (with enough filler to avoid git's rename/copy
// detection), `--diff-filter=D` does NOT see the `R` record as a
// deletion → hashesUnknown=false → the A record is kept →
// `git diff HEAD -- public.txt` emits the protected bytes into
// cumulative_diff_sha. A protected file that is RENAMED or COPIED is just
// as dangerous as one that is DELETED: its bytes are moved/copied to a
// new path and can be further copied into an added/untracked file, and
// the pre-rename worktree content is equally unrecoverable when unstaged
// modifications existed.
//
// Round 1/2's earlier root cause still applies to all three cases (D, R,
// C): when a protected file is MODIFIED WITH UNSTAGED EDITS before being
// deleted/renamed/copied, the worktree bytes (the only bytes that could
// have been copied) are GONE and unrecoverable; `git show :.env` returns
// the old index/HEAD blob, so a copy holding the modified bytes does NOT
// match a hash set and is let through. The pre-operation WORKTREE content
// is fundamentally unrecoverable when unstaged modifications existed, and
// after the operation this is UNDETECTABLE (index==HEAD looks identical
// whether the file was unmodified-then-deleted/renamed/copied or
// unstaged-modified-then-deleted/renamed/copied).
//
// D4 / R16 round 8 — a protected TYPECHANGE (a `T` record: a tracked file
// whose type changed, e.g. a regular file modified then replaced by a
// symlink) is the same class of unrecoverability: the pre-typechange
// worktree bytes are gone once the regular file is replaced, and a copy of
// them would only match the now-absent modified bytes — not the
// HEAD/index/symlink-target versions a content-hash check builds. So a
// protected `T` triggers fail-closed just like D/R/C.
//
// Therefore content-hash matching CANNOT be made safe in the
// protected-delete/rename/copy/typechange case. We fail closed instead:
//
//   - If ANY protected tracked file is DELETED, RENAMED, COPIED, or
//     TYPECHANGED (i.e. a `D` or `T` record on a protected path, OR an
//     `R`/`C` record whose SOURCE path is protected) → return (nil, true).
//     The caller suppresses ALL untracked content (sentinel: path + fixed
//     marker, NO content) and SKIPS all added-tracked (A) records from the
//     diff pathspec. Protected bytes never enter cumulative_diff_sha.
//
//   - If NO protected tracked file is deleted/renamed/copied/typechanged
//     (the COMMON case) → return (empty non-nil map, false). The caller
//     hashes untracked content normally (full content-level correlation)
//     and keeps A records.
//
// This is the user-approved P1>P2 trade-off: P1 security (never leak
// protected bytes) wins over P2 content-level diagnostic correlation,
// but ONLY in the (uncommon) protected-delete/rename/copy/typechange
// case; the common case keeps full correlation.
//
// hashesUnknown is true whenever the name-status enumeration fails OR any
// record is a protected delete (D on a protected path) or a protected
// rename/copy source (R/C whose SOURCE path is protected), or typechanged
// (T on a protected path). It is false only when enumeration succeeds and
// no protected path is deleted, used as the source of a rename/copy, or
// typechanged.
func deletedProtectedContentHashes(root string, protectedPaths []string) (hashes map[string]bool, unknown bool) {
	// Enumerate D, R, C, T records against HEAD. `--find-renames` and
	// `--find-copies*` make git report renames/copies (R/C) instead of
	// collapsing them to a D + A pair; `--diff-filter=DRCT` keeps only
	// deletes, renames, copies, and typechanges so we can inspect their
	// sources/paths. The status field is NUL-separated from the path
	// field(s); for R/C the source and destination paths are BOTH present
	// (source first), for D/T only the single path. This mirrors the
	// parsing in filteredTrackedDiffPathspecs.
	out, err := exec.Command("git", "-C", root, "diff", "--name-status", "--find-renames", "--find-copies", "--find-copies-harder", "--diff-filter=DRCT", "-z", "HEAD").Output()
	if err != nil {
		// Enumeration failed → cannot prove no protected file was
		// deleted/renamed/copied/typechanged → fail closed.
		return nil, true
	}
	if len(out) == 0 {
		// No deletes/renames/copies/typechanges at all → no protected
		// operation → content hashing proceeds normally (no suppression
		// work; callers must NOT os.ReadFile untracked files for matching).
		return map[string]bool{}, false
	}
	fields := strings.Split(string(out), "\x00")
	if len(fields) > 0 && fields[len(fields)-1] == "" {
		fields = fields[:len(fields)-1]
	}
	for i := 0; i < len(fields); {
		status := fields[i]
		i++
		pathCount := 1
		if strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C") {
			pathCount = 2
		}
		if i+pathCount > len(fields) {
			// Malformed name-status output → cannot prove safety →
			// fail closed.
			return nil, true
		}
		paths := fields[i : i+pathCount]
		i += pathCount
		if strings.HasPrefix(status, "D") {
			// A deleted tracked file. If it is protected, fail closed.
			if security.IsProtectedPathWithConfig(paths[0], protectedPaths) {
				return nil, true
			}
			continue
		}
		if strings.HasPrefix(status, "T") {
			// D4/R16 round-8: a TYPECHANGE on a protected tracked file
			// (e.g. a regular file modified then replaced by a symlink).
			// The pre-typechange worktree bytes are unrecoverable once the
			// regular file is gone, and a copy would only match the
			// now-absent modified bytes — not the HEAD/index/symlink-target
			// versions a content-hash check builds — so content-hash
			// matching cannot be made safe. A `T` record carries a single
			// path (the typechanged file); if it is protected, fail closed
			// so the caller skips ALL A/M records and suppresses ALL
			// untracked content.
			if security.IsProtectedPathWithConfig(paths[0], protectedPaths) {
				return nil, true
			}
			continue
		}
		// status starts with R or C: paths[0] is the SOURCE path. A
		// protected file that is renamed or copied is just as dangerous
		// as one that is deleted — its bytes are moved/copied to a new
		// path and can be further copied into an added/untracked file,
		// and the pre-rename/copy worktree content is equally
		// unrecoverable when unstaged modifications existed. The
		// destination is handled by the per-record protected-path check
		// in filteredTrackedDiffPathspecs; here we trigger the
		// fail-closed so A records get skipped.
		if security.IsProtectedPathWithConfig(paths[0], protectedPaths) {
			return nil, true
		}
	}
	// Deletes/renames/copies/typechanges exist but NONE touch a protected
	// source/path → no protected operation → content hashing proceeds
	// normally.
	return map[string]bool{}, false
}

// existingProtectedContentHashes builds the set of SHA256 content hashes of
// ALL recoverable versions of EVERY protected file that currently exists in
// the worktree (tracked OR untracked-protected). It is ONLY safe to call
// when deletedProtectedContentHashes returned unknown=false — i.e. no
// protected tracked file was deleted/renamed/copied-away, so every protected
// file REMAINS and its content is fully recoverable.
//
// D4 / R16 round 6 — closes the MODIFIED-source-copy P1 leak.
//
// Root cause (round-5 codex review): `git diff --find-copies-harder HEAD`
// compares a copy's bytes against the protected file's HEAD blob. When the
// protected source was MODIFIED before being copied (edit .env to
// SECRET=new, `cp .env public.txt`, `git add public.txt`, source REMAINS),
// git reports `M .env` + `A public.txt` — NOT a `C` record — because the
// copy holds the modified (workspace) bytes, but --find-copies-harder
// compares against the unmodified HEAD blob. The copy-aware helper
// (protectedCopyDestinations in review.go / the R/C-source check in
// filteredTrackedDiffPathspecs) therefore returns unknown=false, the A
// record is kept, and the protected modified bytes are hashed into
// cumulative_diff_sha. The same applies to untracked copies.
//
// Key insight: when the protected file is NOT deleted/renamed/copied-away
// (unknown=false — the source REMAINS), the protected file's content IS
// recoverable in full from three versions:
//   - workspace content (os.ReadFile — the modified bytes a `cp` copies);
//   - HEAD content (`git show HEAD:<path>` — the committed bytes);
//   - index content (`git show :<path>` — the staged bytes).
//
// A copy of the protected file (into an A record or an untracked file) will
// have content matching ONE of these versions. So we can SAFELY
// content-hash-match: build the union of SHA256 hashes of all recoverable
// versions of all existing protected files, then for each A record /
// untracked file hash its workspace content and suppress if it matches any
// hash in the set. This closes:
//   - the modified-source-copy leak (workspace hash matches);
//   - the verbatim-copy-from-HEAD leak (HEAD hash matches);
//   - the staged-modified-copy leak (index hash matches).
//
// Candidate protected files are enumerated from THREE sources so a protected
// file is never missed:
//   - `git ls-files -z` (tracked);
//   - `git ls-files --others --exclude-standard -z` (untracked, NOT ignored);
//   - `git ls-files --others --ignored --exclude-standard -z` (IGNORED files).
//
// The third enumeration is essential: a protected file IGNORED by .gitignore
// (the common case — .env is typically gitignored) is OMITTED by the second
// enumeration, so without the third an ignored protected .env is never hashed
// into the set and a copy of it (staged or untracked) is wrongly treated as
// safe and emitted into changes.patch / cumulative_diff_sha (D4/R16 round-7
// fix A). Each enumerated path whose IsProtectedPathWithConfig is true is a
// protected file. For each, all three versions are streamed into a sha256
// hasher (bounded memory via io.Copy); read errors are skipped (a version may
// be absent — e.g. an untracked-new or ignored protected file has no
// HEAD/index version, a deleted-in-worktree-but-present-in-index file has no
// workspace version).
//
// Residual risk (documented): a "filler copy" — an A/untracked file whose
// content is protected content + extra filler, so its hash does NOT match
// any protected version — is a deliberate content-obfuscation evasion that
// this content-hash-match cannot detect when the source REMAINS. When the
// source is deleted/renamed/copied-away (unknown=true) the fail-closed
// branch suppresses ALL A + ALL untracked, covering even filler copies.
//
// Returns an empty (non-nil) set when no protected files exist → no A /
// untracked file can match → all kept, full content-level correlation in the
// common case.
func existingProtectedContentHashes(root string, protectedPaths []string) map[string]bool {
	set := map[string]bool{}
	// Enumerate candidate protected files: tracked + untracked-non-ignored +
	// ignored. The ignored enumeration is required so a gitignored protected
	// .env (which `--others --exclude-standard` omits) is still hashed.
	candidates := []string{}
	for _, args := range [][]string{
		{"ls-files", "-z"},
		{"ls-files", "--others", "--exclude-standard", "-z"},
		{"ls-files", "--others", "--ignored", "--exclude-standard", "-z"},
	} {
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).Output()
		if err != nil {
			continue
		}
		for _, p := range strings.Split(string(out), "\x00") {
			if p == "" {
				continue
			}
			candidates = append(candidates, p)
		}
	}
	// D4/R16 round-16 (codex finding R16-1/O): ALSO enumerate protected
	// files present in HEAD via `git ls-tree -r HEAD --name-only`. A
	// protected file that was DELETED (`git rm -f .env`) is gone from
	// the index and worktree, so the three enumerations above no longer
	// list it — its HEAD blob hash was never added to the set, and a
	// modified tracked file holding the copied bytes (`cp .env
	// config.txt && git rm -f .env`) failed the content-hash match and
	// the protected bytes were folded into cumulative_diff_sha /
	// changes.patch. ls-tree lists the file at its HEAD-committed path
	// so `git show HEAD:<path>` recovers the blob hash even after a
	// staged delete.
	if out, err := exec.Command("git", "-C", root, "ls-tree", "-r", "HEAD", "--name-only", "-z").Output(); err == nil {
		for _, p := range strings.Split(string(out), "\x00") {
			if p == "" {
				continue
			}
			candidates = append(candidates, p)
		}
	}
	for _, rel := range candidates {
		if !security.IsProtectedPathWithConfig(rel, protectedPaths) {
			continue
		}
		// (a) workspace version — the modified bytes a `cp` copies. Stream
		// into a hasher (bounded memory). Skip on read error (file may be
		// deleted in workspace but present in index/HEAD).
		if h, ok := hashWorkspaceFile(filepath.Join(root, rel)); ok {
			set[h] = true
			// D4/R16 round-12 (codex findings H/I): `$(cat .env)`
			// strips trailing newlines, so
			// `ln -s "$(cat .env)" leak` creates a symlink
			// whose target lacks those newlines. Add
			// trailing-newline-stripped (rtrim) content-hash
			// variants so hashTypechangeEmittedBytes /
			// matchesTypechangeTracked detect the copy
			// regardless of how many newlines were stripped.
			// Read the file bytes to derive the rtrim hash.
			if data, rerr := os.ReadFile(filepath.Join(root, rel)); rerr == nil {
				trimmed := string(bytes.TrimRight(data, "\n"))
				if len(trimmed) < len(data) {
					h2 := sha256.Sum256([]byte(trimmed))
					trimmedHash := hex.EncodeToString(h2[:])
					if trimmedHash != h {
						set[trimmedHash] = true
					}
				}
			}
		}
		// (b) HEAD version — the committed bytes. Skip on error (file may
		// be untracked-new, no HEAD version).
		if h, ok := hashGitBlob(root, "HEAD:"+rel); ok {
			set[h] = true
			// D4/R16 round-13 (codex finding Q): add
			// trailing-newline-stripped (rtrim) variant for
			// HEAD blob too. `$(git show :.env)` strips
			// trailing newlines, so a symlink target
			// created from the staged blob text may not
			// match the exact blob hash.
			if rtrimHash, ok := hashGitBlobRTrim(root, "HEAD:"+rel); ok && rtrimHash != h {
				set[rtrimHash] = true
			}
		}
		// (c) index version — the staged bytes. Skip on error (file may be
		// untracked, no index version).
		if h, ok := hashGitBlob(root, ":"+rel); ok {
			set[h] = true
			// D4/R16 round-13 (codex finding Q): same rtrim
			// variant for the staged/index blob.
			if rtrimHash, ok := hashGitBlobRTrim(root, ":"+rel); ok && rtrimHash != h {
				set[rtrimHash] = true
			}
		}
	}
	return set
}

// hashWorkspaceFile streams a worktree file's content into a sha256 hasher
// (bounded memory via io.Copy) and returns the hex hash. Returns ok=false on
// any error (file absent, permission, etc.).
//
// D4/R16 round-8 fix: refuse to open non-regular files (FIFOs, devices) so
// a protected path that is a FIFO, a device, or a symlink to /dev/zero
// cannot block os.Open/io.Copy for the full duration of rework prompt
// generation (computeCumulativeDiffSHA calls this for every enumerated
// protected file AND every added/untracked candidate).
//
// D4/R16 round-9 fix: the round-8 Lstat guard also skipped symlinks, which
// dropped a protected path that is a symlink to a REGULAR secret file (e.g.
// `.env -> ../shared/env`) from existingHashes — a copy made through that
// symlink then failed the content-hash suppression and the secret leaked
// into cumulative_diff_sha. We now Stat (follows symlinks): a symlink whose
// target is a regular file is hashed; a symlink whose target is a
// FIFO/device/socket (e.g. -> /dev/zero) resolves to a non-regular mode and
// is skipped BEFORE opening (no block). A broken/looping symlink fails Stat
// → ok=false (skip). A special file yields ok=false, which callers treat as
// fail-closed (the protected version is simply not added to existingHashes;
// an added/untracked file that cannot be hashed is skipped rather than
// correlated).
func hashWorkspaceFile(path string) (string, bool) {
	st, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	// Skip non-regular targets (FIFO/device/socket, whether the path itself
	// or a symlink's target). os.Stat follows symlinks, so a symlink to a
	// regular secret file resolves to a regular mode and IS hashed below.
	if !st.Mode().IsRegular() {
		return "", false
	}
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", false
	}
	return hex.EncodeToString(h.Sum(nil)), true
}

// hashTypechangeEmittedBytes returns the SHA256 of the bytes `git diff HEAD
// -- <rel>` would EMIT for a typechanged (T) tracked file at root/rel — i.e.
// the content that would be folded into cumulative_diff_sha. For a regular
// file → symlink typechange, git diff emits the symlink TARGET text (the
// stored link target), so we hash os.Readlink's result. For a symlink →
// regular (or other) typechange, git diff emits the new regular file's
// content, so we hash the workspace file via hashWorkspaceFile. Returns
// ok=false when the bytes cannot be read (caller keeps the record — a
// non-readable typechange cannot be content-matched, and unlike A we do not
// blanket fail-closed T records on read error; the protected-source
// fail-closed via hashesUnknown covers the dangerous protected-operation
// case).
//
// D4/R16 round-10 (codex finding F).
func hashTypechangeEmittedBytes(root, rel string) (string, bool) {
	full := filepath.Join(root, rel)
	if target, err := os.Readlink(full); err == nil {
		// Workspace path is a symlink → git diff emits the target text.
		h := sha256.Sum256([]byte(target))
		return hex.EncodeToString(h[:]), true
	}
	// Not a symlink → git diff emits the workspace file content.
	return hashWorkspaceFile(full)
}
// any error (spec refers to an absent blob, git failure, etc.).
//
// D4/R16 round-7 fix B: a NON-ZERO exit from `git show` means the blob does
// not exist (e.g. an untracked/ignored protected file has no HEAD/index
// version). git writes NO stdout in that case, so io.Copy reads 0 bytes with
// copyErr=nil — previously the code ignored cmd.Wait()'s error and returned
// sha256("") as a VALID protected hash, which then matched any unrelated
// EMPTY added/untracked file and wrongly suppressed it. We now treat a
// non-nil Wait error (non-zero exit) as ok=false (absent blob): only add a
// hash when `git show` succeeded (exit 0).
func hashGitBlob(root, spec string) (string, bool) {
	cmd := exec.Command("git", "-C", root, "show", spec)
	r, err := cmd.StdoutPipe()
	if err != nil {
		return "", false
	}
	if err := cmd.Start(); err != nil {
		return "", false
	}
	h := sha256.New()
	_, copyErr := io.Copy(h, r)
	waitErr := cmd.Wait()
	if copyErr != nil || waitErr != nil {
		return "", false // git show failed (no such blob, non-zero exit) → absent
	}
	return hex.EncodeToString(h.Sum(nil)), true
}

// hashGitBlobRTrim streams `git -C root show <spec>` output, reads the
// full blob content, trims trailing newlines, and returns the SHA256 hex
// hash of the trimmed content. D4/R16 round-13 (codex finding Q): `$(git
// show :.env)` strips trailing newlines, so a symlink target created from
// a staged blob text may not match the exact blob hash. The rtrim variant
// is added to existingHashes alongside the exact blob hash.
func hashGitBlobRTrim(root, spec string) (string, bool) {
	cmd := exec.Command("git", "-C", root, "show", spec)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	trimmed := string(bytes.TrimRight(out, "\n"))
	if len(trimmed) == len(out) {
		// No trailing newlines were stripped — the rtrim hash is
		// identical to the exact blob hash, and the caller already
		// added that.
		return "", false
	}
	h := sha256.Sum256([]byte(trimmed))
	return hex.EncodeToString(h[:]), true
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
