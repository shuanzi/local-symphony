package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-symphony/internal/config"
	"local-symphony/internal/core"
	"local-symphony/internal/store"
)

func TestDispatchIssueDefaultsToFakeRunnerAndCompletesWithReview(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_RUNNER_KIND", "")
	t.Setenv("SYMPHONY_FAKE_RUNNER_OUTCOME", "")
	st, issue := newReadyDispatchIssue(t)

	res, err := (Orchestrator{Store: st}).DispatchIssue(issue.Identifier, "manual")
	if err != nil {
		t.Fatalf("DispatchIssue: %v", err)
	}

	if res.Run.RunnerKind != "fake" {
		t.Fatalf("runner_kind = %q, want fake", res.Run.RunnerKind)
	}
	if res.Run.Status != core.RunCompleted {
		t.Fatalf("run status = %s, want %s", res.Run.Status, core.RunCompleted)
	}
	if res.Issue.State != core.StateHumanReview {
		t.Fatalf("issue state = %s, want %s", res.Issue.State, core.StateHumanReview)
	}
	if res.Issue.LatestReviewPacketID == nil || *res.Issue.LatestReviewPacketID == "" {
		t.Fatal("latest review packet id is empty")
	}
}

func TestDispatchIssueCodexRunnerFailClosedUnsupportedVersion(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_RUNNER_KIND", "codex")
	st, issue := newReadyDispatchIssue(t)

	res, err := (Orchestrator{Store: st}).DispatchIssue(issue.Identifier, "manual")
	if err != nil {
		t.Fatalf("DispatchIssue: %v", err)
	}

	if res.Run.RunnerKind != "codex" {
		t.Fatalf("runner_kind = %q, want codex", res.Run.RunnerKind)
	}
	if res.Run.Status != core.RunFailed {
		t.Fatalf("run status = %s, want %s", res.Run.Status, core.RunFailed)
	}
	if res.Run.FailureCode == nil || *res.Run.FailureCode != core.FailureUnsupportedCodexVersion {
		t.Fatalf("failure code = %v, want %s", res.Run.FailureCode, core.FailureUnsupportedCodexVersion)
	}
	if !res.Issue.DispatchPaused {
		t.Fatal("issue dispatch_paused = false, want true")
	}
	if res.Issue.DispatchPauseReason == nil || *res.Issue.DispatchPauseReason != string(core.FailureUnsupportedCodexVersion) {
		t.Fatalf("dispatch pause reason = %v, want %s", res.Issue.DispatchPauseReason, core.FailureUnsupportedCodexVersion)
	}
}

func TestDispatchIssueCodexFixtureProcessCompletesWithReview(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_RUNNER_KIND", "codex")
	st, issue := newReadyDispatchIssue(t)
	script := writeCodexFixtureCommand(t)
	writeWorkflowWithCodexCommand(t, st.RepoRoot, script+" app-server")

	res, err := (Orchestrator{Store: st}).DispatchIssue(issue.Identifier, "manual")
	if err != nil {
		t.Fatalf("DispatchIssue: %v", err)
	}

	if res.Run.RunnerKind != "codex" {
		t.Fatalf("runner_kind = %q, want codex", res.Run.RunnerKind)
	}
	if res.Run.Status != core.RunCompleted {
		t.Fatalf("run status = %s, want %s", res.Run.Status, core.RunCompleted)
	}
	if res.Issue.State != core.StateHumanReview {
		t.Fatalf("issue state = %s, want %s", res.Issue.State, core.StateHumanReview)
	}
	assertRunEventCount(t, st, res.Run.ID, "agent.process_started", 1)
	assertRunEventCount(t, st, res.Run.ID, "agent.handshake_completed", 1)
	assertRunEventCount(t, st, res.Run.ID, "agent.turn_completed", 1)
}

func TestDispatchIssueCodexMissingHandoffContinuationUsesSameProcess(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_RUNNER_KIND", "codex")
	st, issue := newReadyDispatchIssue(t)
	script := writeCodexContinuationFixtureCommand(t)
	writeWorkflowWithCodexCommand(t, st.RepoRoot, script+" app-server")

	res, err := (Orchestrator{Store: st}).DispatchIssue(issue.Identifier, "manual")
	if err != nil {
		t.Fatalf("DispatchIssue: %v", err)
	}
	if res.Run.Status != core.RunCompleted {
		t.Fatalf("run status = %s, want %s", res.Run.Status, core.RunCompleted)
	}
	gotIssue, err := st.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if gotIssue.Workspace == nil {
		t.Fatal("workspace is nil")
	}
	assertRunEventCount(t, st, res.Run.ID, "agent.process_started", 1)
	assertRunEventCount(t, st, res.Run.ID, "agent.handoff_continuation_requested", 1)
	assertRecordedStartTurn(t, filepath.Join(gotIssue.Workspace.Path, "turn-1.json"), false, "")
	assertRecordedStartTurn(t, filepath.Join(gotIssue.Workspace.Path, "turn-2.json"), true, "thread_fixture")
}

func TestTickReturnsNonEligibilityDispatchError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_RUNNER_KIND", "")
	st, _ := newReadyDispatchIssue(t)
	if err := st.Project.Exec(`CREATE TRIGGER fail_scheduler_run BEFORE INSERT ON run_attempts WHEN NEW.dispatch_reason='scheduler' BEGIN SELECT RAISE(ABORT, 'scheduler run insert failed'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	err := (Orchestrator{Store: st}).Tick()
	if err == nil {
		t.Fatal("Tick succeeded, want scheduler run insert error")
	}
	if !strings.Contains(err.Error(), "scheduler run insert failed") {
		t.Fatalf("Tick error = %v, want scheduler run insert failure", err)
	}
}

func TestTickSkipsPausedReadyAndWorkingIssues(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	st, err := store.InitProject(t.TempDir(), "TST")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	paused, err := st.CreateIssue(store.CreateIssueInput{
		Title:              "Paused ready",
		Description:        "desc",
		AcceptanceCriteria: []string{"done"},
		Priority:           3,
	})
	if err != nil {
		t.Fatalf("CreateIssue paused: %v", err)
	}
	if _, err := st.TransitionIssue(paused.ID, core.StateReady, "", ""); err != nil {
		t.Fatalf("TransitionIssue paused: %v", err)
	}
	if _, err := st.DispatchPause(paused.ID, "operator pause"); err != nil {
		t.Fatalf("DispatchPause: %v", err)
	}
	working, err := st.CreateIssue(store.CreateIssueInput{
		Title:              "Working issue",
		Description:        "desc",
		AcceptanceCriteria: []string{"done"},
		Priority:           3,
	})
	if err != nil {
		t.Fatalf("CreateIssue working: %v", err)
	}
	if err := st.Project.Exec(`UPDATE issues SET state='Working' WHERE id=?`, working.ID); err != nil {
		t.Fatalf("set working state: %v", err)
	}

	if err := (Orchestrator{Store: st}).Tick(); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	assertRunAttemptCount(t, st, 0)
}

func TestTickUsesWorkflowDispatchCandidateStates(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_RUNNER_KIND", "")
	st, err := store.InitProject(t.TempDir(), "TST")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	body := `---
tracker:
  dispatch_candidate_states: [Rework]
---
Do the work.
`
	if err := os.WriteFile(filepath.Join(st.RepoRoot, "WORKFLOW.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	ready, err := st.CreateIssue(store.CreateIssueInput{
		Title:              "Ready issue",
		Description:        "desc",
		AcceptanceCriteria: []string{"done"},
		Priority:           3,
	})
	if err != nil {
		t.Fatalf("CreateIssue ready: %v", err)
	}
	if _, err := st.TransitionIssue(ready.ID, core.StateReady, "", ""); err != nil {
		t.Fatalf("TransitionIssue ready: %v", err)
	}
	rework, err := st.CreateIssue(store.CreateIssueInput{
		Title:              "Rework issue",
		Description:        "desc",
		AcceptanceCriteria: []string{"done"},
		Priority:           3,
	})
	if err != nil {
		t.Fatalf("CreateIssue rework: %v", err)
	}
	if err := st.Project.Exec(`UPDATE issues SET state='Rework' WHERE id=?`, rework.ID); err != nil {
		t.Fatalf("set rework state: %v", err)
	}

	if err := (Orchestrator{Store: st}).Tick(); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	rows, err := st.Project.Query(`SELECT source_issue_state FROM run_attempts ORDER BY created_at`)
	if err != nil {
		t.Fatalf("query run attempts: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("run attempts = %d, want 1", len(rows))
	}
	if got := rows[0]["source_issue_state"].String(); got != string(core.StateRework) {
		t.Fatalf("source_issue_state = %q, want %q", got, core.StateRework)
	}
}

func TestRunWorkerCancelsCodexProcessWhenRunCancelled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_RUNNER_KIND", "codex")
	st, issue := newReadyDispatchIssue(t)
	script := writeCancellableCodexFixtureCommand(t)
	writeWorkflowWithCodexCommand(t, st.RepoRoot, script+" app-server")
	run, err := st.ClaimRun(issue.ID, "manual", "codex", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	wf, err := config.Load(st.RepoRoot)
	if err != nil {
		t.Fatalf("Load workflow: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- (Orchestrator{Store: st}).runWorker(run.ID, wf)
	}()
	waitForRunEvent(t, st, run.ID, "agent.handshake_completed")
	if err := st.CancelRun(run.ID, "operator cancelled"); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runWorker: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runWorker did not return after cancellation")
	}

	gotRun, err := st.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if gotRun.Status != core.RunCancelled {
		t.Fatalf("run status = %s, want %s", gotRun.Status, core.RunCancelled)
	}
	gotIssue, err := st.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if gotIssue.Workspace == nil {
		t.Fatal("workspace is nil")
	}
	if _, err := os.Stat(filepath.Join(gotIssue.Workspace.Path, "terminated")); err != nil {
		t.Fatalf("codex process was not terminated: %v", err)
	}
}

func TestRunWorkerMissingHandoffWithoutContinuationFailsImmediately(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_FAKE_RUNNER_OUTCOME", "missing_handoff")
	st, run := newRunWorkerTestFixture(t)
	wf := newRunWorkerTestWorkflow(st)
	wf.Config.Agent.MaxHandoffContinuations = 0
	wf.Config.Agent.MaxTurnsPerRun = 1

	if err := (Orchestrator{Store: st}).runWorker(run.ID, wf); err != nil {
		t.Fatalf("runWorker: %v", err)
	}

	gotRun, err := st.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if gotRun.Status != core.RunCompletedWithoutHandoff {
		t.Fatalf("run status = %s, want %s", gotRun.Status, core.RunCompletedWithoutHandoff)
	}
	if gotRun.FailureCode == nil || *gotRun.FailureCode != core.FailureMissingHandoff {
		t.Fatalf("failure code = %v, want %s", gotRun.FailureCode, core.FailureMissingHandoff)
	}
	assertRunEventCount(t, st, run.ID, "agent.handoff_continuation_requested", 0)
}

func TestRunWorkerMissingHandoffContinuationFailsAfterOneRetry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_FAKE_RUNNER_OUTCOMES", "missing_handoff,missing_handoff")
	st, run := newRunWorkerTestFixture(t)
	wf := newRunWorkerTestWorkflow(st)
	wf.Config.Agent.MaxHandoffContinuations = 1
	wf.Config.Agent.MaxTurnsPerRun = 2

	if err := (Orchestrator{Store: st}).runWorker(run.ID, wf); err != nil {
		t.Fatalf("runWorker: %v", err)
	}

	gotRun, err := st.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if gotRun.Status != core.RunCompletedWithoutHandoff {
		t.Fatalf("run status = %s, want %s", gotRun.Status, core.RunCompletedWithoutHandoff)
	}
	if gotRun.FailureCode == nil || *gotRun.FailureCode != core.FailureMissingHandoff {
		t.Fatalf("failure code = %v, want %s", gotRun.FailureCode, core.FailureMissingHandoff)
	}
	assertRunEventCount(t, st, run.ID, "agent.handoff_continuation_requested", 1)
}

func TestRunWorkerMissingHandoffContinuationSuccessGeneratesReview(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_FAKE_RUNNER_OUTCOMES", "missing_handoff,success")
	st, run := newRunWorkerTestFixture(t)
	wf := newRunWorkerTestWorkflow(st)
	wf.Config.Agent.MaxHandoffContinuations = 1
	wf.Config.Agent.MaxTurnsPerRun = 2

	if err := (Orchestrator{Store: st}).runWorker(run.ID, wf); err != nil {
		t.Fatalf("runWorker: %v", err)
	}

	gotRun, err := st.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if gotRun.Status != core.RunCompleted {
		t.Fatalf("run status = %s, want %s", gotRun.Status, core.RunCompleted)
	}
	issue, err := st.GetIssue(run.IssueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.State != core.StateHumanReview {
		t.Fatalf("issue state = %s, want %s", issue.State, core.StateHumanReview)
	}
	if issue.LatestReviewPacketID == nil || *issue.LatestReviewPacketID == "" {
		t.Fatal("latest review packet id is empty")
	}
	assertRunEventCount(t, st, run.ID, "agent.handoff_continuation_requested", 1)
}

func TestRunWorkerRunsAfterRunOnlyAfterFinalRunnerResult(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_FAKE_RUNNER_OUTCOMES", "missing_handoff,missing_handoff")
	st, run := newRunWorkerTestFixture(t)
	marker := filepath.Join(t.TempDir(), "after-run")
	afterRun := fmt.Sprintf("printf x >> %q", marker)
	wf := newRunWorkerTestWorkflow(st)
	wf.Config.Agent.MaxHandoffContinuations = 1
	wf.Config.Agent.MaxTurnsPerRun = 2
	wf.Config.Hooks.AfterRun = &afterRun

	if err := (Orchestrator{Store: st}).runWorker(run.ID, wf); err != nil {
		t.Fatalf("runWorker: %v", err)
	}

	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read after_run marker: %v", err)
	}
	if got := string(data); got != "x" {
		t.Fatalf("after_run executions wrote %q, want one execution", got)
	}
	rows, err := st.Project.Query(`SELECT event_type FROM run_events WHERE run_id=? AND event_type IN ('agent.handoff_continuation_requested','hook.after_run.started') ORDER BY seq ASC`, run.ID)
	if err != nil {
		t.Fatalf("query run events: %v", err)
	}
	want := []string{"agent.handoff_continuation_requested", "hook.after_run.started"}
	if len(rows) != len(want) {
		t.Fatalf("event count = %d, want %d", len(rows), len(want))
	}
	for i, w := range want {
		if got := rows[i]["event_type"].String(); got != w {
			t.Fatalf("event %d = %s, want %s", i, got, w)
		}
	}
}

func TestRunWorkerRunsAfterCreateOnlyForNewWorkspace(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_FAKE_RUNNER_OUTCOME", "")
	st, issue := newReadyDispatchIssue(t)
	marker := filepath.Join(t.TempDir(), "after-create")
	afterCreate := fmt.Sprintf("printf x >> %q", marker)
	wf := newRunWorkerTestWorkflow(st)
	wf.Config.Hooks.AfterCreate = &afterCreate

	firstRun, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun first: %v", err)
	}
	if err := (Orchestrator{Store: st}).runWorker(firstRun.ID, wf); err != nil {
		t.Fatalf("runWorker first: %v", err)
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read after_create marker: %v", err)
	}
	if got := string(data); got != "x" {
		t.Fatalf("after_create executions wrote %q, want one execution", got)
	}
	assertRunEventCount(t, st, firstRun.ID, "hook.after_create.started", 1)

	if _, err := st.TransitionIssue(issue.ID, core.StateReady, "rerun", ""); err != nil {
		t.Fatalf("TransitionIssue Ready: %v", err)
	}
	secondRun, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun second: %v", err)
	}
	if err := (Orchestrator{Store: st}).runWorker(secondRun.ID, wf); err != nil {
		t.Fatalf("runWorker second: %v", err)
	}
	data, err = os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read after_create marker after reuse: %v", err)
	}
	if got := string(data); got != "x" {
		t.Fatalf("after_create executions wrote %q after reused workspace, want still one execution", got)
	}
	assertRunEventCount(t, st, secondRun.ID, "hook.after_create.started", 0)
}

func TestRunWorkerAfterCreateFailureFailsBeforeTokenAndAgent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_FAKE_RUNNER_OUTCOME", "")
	st, issue := newReadyDispatchIssue(t)
	run, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	afterCreate := "printf abcdef; exit 7"
	wf := newRunWorkerTestWorkflow(st)
	wf.Config.Hooks.AfterCreate = &afterCreate
	wf.Config.Hooks.MaxOutputBytes = 3

	if err := (Orchestrator{Store: st}).runWorker(run.ID, wf); err != nil {
		t.Fatalf("runWorker: %v", err)
	}

	assertHookFailureBeforeTokenAndAgent(t, st, run.ID, core.FailureAfterCreateFailed, "exit status 7")
	assertHookEvents(t, st, run.ID, "hook.after_create", []string{"started", "output", "failed"})
	assertHookStartedCommandRedacted(t, st, run.ID, "hook.after_create.started")
	assertHookOutput(t, st, run.ID, "hook.after_create.output", "redacted")
	assertHookEvents(t, st, run.ID, "hook.after_run", []string{"completed"})
}

func TestRunWorkerAfterCreateFailureRunsAfterRunBeforeFail(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_FAKE_RUNNER_OUTCOME", "")
	st, issue := newReadyDispatchIssue(t)
	run, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	marker := filepath.Join(t.TempDir(), "hook-order")
	afterCreate := fmt.Sprintf("printf a >> %q; exit 7", marker)
	afterRun := fmt.Sprintf("printf r >> %q", marker)
	wf := newRunWorkerTestWorkflow(st)
	wf.Config.Hooks.AfterCreate = &afterCreate
	wf.Config.Hooks.AfterRun = &afterRun

	if err := (Orchestrator{Store: st}).runWorker(run.ID, wf); err != nil {
		t.Fatalf("runWorker: %v", err)
	}

	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read hook order marker: %v", err)
	}
	if got := string(data); got != "ar" {
		t.Fatalf("hook order marker = %q, want after_create then after_run", got)
	}
	assertEventOrder(t, st, run.ID, []string{"hook.after_create.failed", "hook.after_run.started", "run.failed"})
}

func TestRunWorkerRetriesAfterCreateAfterFailedAttempt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_FAKE_RUNNER_OUTCOME", "")
	st, issue := newReadyDispatchIssue(t)
	marker := filepath.Join(t.TempDir(), "after-create-attempts")
	afterCreate := fmt.Sprintf(`if [ ! -f %[1]q ]; then printf f > %[1]q; exit 7; fi; printf s >> %[1]q`, marker)
	wf := newRunWorkerTestWorkflow(st)
	wf.Config.Hooks.AfterCreate = &afterCreate

	firstRun, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun first: %v", err)
	}
	if err := (Orchestrator{Store: st}).runWorker(firstRun.ID, wf); err != nil {
		t.Fatalf("runWorker first: %v", err)
	}
	assertHookFailureBeforeTokenAndAgent(t, st, firstRun.ID, core.FailureAfterCreateFailed, "exit status 7")

	if _, err := st.DispatchResume(issue.ID, "retry after_create"); err != nil {
		t.Fatalf("DispatchResume: %v", err)
	}
	secondRun, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun second: %v", err)
	}
	if err := (Orchestrator{Store: st}).runWorker(secondRun.ID, wf); err != nil {
		t.Fatalf("runWorker second: %v", err)
	}

	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read after_create marker: %v", err)
	}
	if got := string(data); got != "fs" {
		t.Fatalf("after_create attempts = %q, want failed attempt then retry success", got)
	}
	assertRunEventCount(t, st, secondRun.ID, "hook.after_create.started", 1)
}

func TestRunWorkerRetriesAfterCreateWhenPreparedWorkspaceHasNoHookEvents(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_FAKE_RUNNER_OUTCOME", "")
	st, issue := newReadyDispatchIssue(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if _, err := st.CreateOrUpdateWorkspace(issue.ID, workspace, "test", "auto", "main", "base-sha"); err != nil {
		t.Fatalf("CreateOrUpdateWorkspace: %v", err)
	}
	run, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	marker := filepath.Join(t.TempDir(), "after-create-no-events")
	afterCreate := fmt.Sprintf("printf retried > %q", marker)
	wf := newRunWorkerTestWorkflow(st)
	wf.Config.Hooks.AfterCreate = &afterCreate

	if err := (Orchestrator{Store: st}).runWorker(run.ID, wf); err != nil {
		t.Fatalf("runWorker: %v", err)
	}

	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read after_create marker: %v", err)
	}
	if got := string(data); got != "retried" {
		t.Fatalf("after_create retry marker = %q, want retried", got)
	}
	assertRunEventCount(t, st, run.ID, "hook.after_create.started", 1)
}

func TestRunWorkerSkipsAfterCreateWhenCancelledDuringPrepare(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_FAKE_RUNNER_OUTCOME", "")
	st, issue := newReadyDispatchIssue(t)
	run, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	now := core.Now()
	if err := st.Project.Exec(`CREATE TRIGGER cancel_run_after_workspace_insert AFTER INSERT ON workspaces BEGIN UPDATE run_attempts SET status='cancelled', failure_code='operator_cancelled', failure_message='cancelled during prepare', ended_at='` + now + `', updated_at='` + now + `' WHERE issue_id=NEW.issue_id AND status IN ('pending','preparing_workspace','rendering_prompt','starting_agent','running'); END`); err != nil {
		t.Fatalf("create cancel trigger: %v", err)
	}
	marker := filepath.Join(t.TempDir(), "after-create-cancelled")
	afterCreate := fmt.Sprintf("printf should-not-run > %q", marker)
	wf := newRunWorkerTestWorkflow(st)
	wf.Config.Hooks.AfterCreate = &afterCreate

	if err := (Orchestrator{Store: st}).runWorker(run.ID, wf); err != nil {
		t.Fatalf("runWorker: %v", err)
	}

	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("after_create marker stat error = %v, want not exist", err)
	}
	gotRun, err := st.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if gotRun.Status != core.RunCancelled {
		t.Fatalf("run status = %s, want %s", gotRun.Status, core.RunCancelled)
	}
	assertRunEventCount(t, st, run.ID, "hook.after_create.started", 0)
}

func TestRunWorkerRetriesAfterCreateAfterInterruptedAttempt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_FAKE_RUNNER_OUTCOME", "")
	st, issue := newReadyDispatchIssue(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if _, err := st.CreateOrUpdateWorkspace(issue.ID, workspace, "test", "auto", "main", "base-sha"); err != nil {
		t.Fatalf("CreateOrUpdateWorkspace: %v", err)
	}
	crashedRunID := "run_crashed_after_create"
	if err := st.AppendEvent("hook.after_create.started", "hook", &issue.ID, &crashedRunID, map[string]any{"command": "redacted"}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	run, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	marker := filepath.Join(t.TempDir(), "after-create-retried")
	afterCreate := fmt.Sprintf("printf retried > %q", marker)
	wf := newRunWorkerTestWorkflow(st)
	wf.Config.Hooks.AfterCreate = &afterCreate

	if err := (Orchestrator{Store: st}).runWorker(run.ID, wf); err != nil {
		t.Fatalf("runWorker: %v", err)
	}

	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read after_create marker: %v", err)
	}
	if got := string(data); got != "retried" {
		t.Fatalf("after_create retry marker = %q, want retried", got)
	}
	assertRunEventCount(t, st, run.ID, "hook.after_create.started", 1)
}

func TestRunWorkerFailsWhenAfterCreateCompletedEventCannotPersist(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_FAKE_RUNNER_OUTCOME", "")
	st, issue := newReadyDispatchIssue(t)
	run, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	if err := st.Project.Exec(`CREATE TRIGGER fail_after_create_completed_insert BEFORE INSERT ON run_events WHEN NEW.event_type='hook.after_create.completed' BEGIN SELECT RAISE(ABORT, 'after_create completion insert failed'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	marker := filepath.Join(t.TempDir(), "after-create-completed")
	afterCreate := fmt.Sprintf("printf side-effect > %q", marker)
	wf := newRunWorkerTestWorkflow(st)
	wf.Config.Hooks.AfterCreate = &afterCreate

	if err := (Orchestrator{Store: st}).runWorker(run.ID, wf); err != nil {
		t.Fatalf("runWorker: %v", err)
	}

	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read after_create marker: %v", err)
	}
	if got := string(data); got != "side-effect" {
		t.Fatalf("after_create marker = %q, want side-effect", got)
	}
	assertHookFailureBeforeTokenAndAgent(t, st, run.ID, core.FailureAfterCreateFailed, "after_create completion insert failed")
	assertHookEvents(t, st, run.ID, "hook.after_create", []string{"started", "output"})
}

func TestRunWorkerCancelsAfterCreateHookProcessGroupWhenRunCancelled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_FAKE_RUNNER_OUTCOME", "")
	st, issue := newReadyDispatchIssue(t)
	run, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	tempDir := t.TempDir()
	started := filepath.Join(tempDir, "after-create-started")
	childOutput := filepath.Join(tempDir, "after-create-child-output")
	afterCreate := fmt.Sprintf("touch %q; (while true; do printf c >> %q; sleep 0.05; done) & wait", started, childOutput)
	wf := newRunWorkerTestWorkflow(st)
	wf.Config.Hooks.AfterCreate = &afterCreate
	wf.Config.Hooks.TimeoutMS = 5000

	done := make(chan error, 1)
	go func() {
		done <- (Orchestrator{Store: st}).runWorker(run.ID, wf)
	}()
	waitForFile(t, started)
	waitForFile(t, childOutput)
	if err := st.CancelRun(run.ID, "cancel during after_create"); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runWorker: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runWorker did not return promptly after after_create cancellation")
	}
	assertChildOutputStopsGrowing(t, childOutput)
	gotRun, err := st.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if gotRun.Status != core.RunCancelled {
		t.Fatalf("run status = %s, want %s", gotRun.Status, core.RunCancelled)
	}
	assertHookEvents(t, st, run.ID, "hook.after_create", []string{"started", "output", "cancelled"})
	assertRunEventCount(t, st, run.ID, "hook.after_run.completed", 0)
}

func TestRunWorkerCancelsBeforeRunHookProcessGroupWhenRunCancelled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_FAKE_RUNNER_OUTCOME", "")
	st, run := newRunWorkerTestFixture(t)
	tempDir := t.TempDir()
	started := filepath.Join(tempDir, "before-run-started")
	childOutput := filepath.Join(tempDir, "before-run-child-output")
	beforeRun := fmt.Sprintf("touch %q; (while true; do printf c >> %q; sleep 0.05; done) & wait", started, childOutput)
	wf := newRunWorkerTestWorkflow(st)
	wf.Config.Hooks.BeforeRun = &beforeRun
	wf.Config.Hooks.TimeoutMS = 5000

	done := make(chan error, 1)
	go func() {
		done <- (Orchestrator{Store: st}).runWorker(run.ID, wf)
	}()
	waitForFile(t, started)
	waitForFile(t, childOutput)
	if err := st.CancelRun(run.ID, "cancel during before_run"); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runWorker: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runWorker did not return promptly after before_run cancellation")
	}
	assertChildOutputStopsGrowing(t, childOutput)
	gotRun, err := st.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if gotRun.Status != core.RunCancelled {
		t.Fatalf("run status = %s, want %s", gotRun.Status, core.RunCancelled)
	}
	assertHookEvents(t, st, run.ID, "hook.before_run", []string{"started", "output", "cancelled"})
	assertRunEventCount(t, st, run.ID, "hook.after_run.completed", 0)
}

func TestRunWorkerSkipsBeforeRunWhenCancelledDuringPromptRender(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_FAKE_RUNNER_OUTCOME", "")
	st, run := newRunWorkerTestFixture(t)
	now := core.Now()
	if err := st.Project.Exec(`CREATE TRIGGER cancel_run_after_prompt_snapshot_insert AFTER INSERT ON prompt_snapshots BEGIN UPDATE run_attempts SET status='cancelled', failure_code='operator_cancelled', failure_message='cancelled during prompt render', ended_at='` + now + `', updated_at='` + now + `' WHERE id=NEW.run_id AND status IN ('pending','preparing_workspace','rendering_prompt','starting_agent','running'); END`); err != nil {
		t.Fatalf("create cancel trigger: %v", err)
	}
	marker := filepath.Join(t.TempDir(), "before-run-cancelled")
	beforeRun := fmt.Sprintf("printf should-not-run > %q", marker)
	wf := newRunWorkerTestWorkflow(st)
	wf.Config.Hooks.BeforeRun = &beforeRun

	if err := (Orchestrator{Store: st}).runWorker(run.ID, wf); err != nil {
		t.Fatalf("runWorker: %v", err)
	}

	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("before_run marker stat error = %v, want not exist", err)
	}
	gotRun, err := st.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if gotRun.Status != core.RunCancelled {
		t.Fatalf("run status = %s, want %s", gotRun.Status, core.RunCancelled)
	}
	assertRunEventCount(t, st, run.ID, "hook.before_run.started", 0)
}

func TestRunWorkerBeforeRunFailureFailsBeforeTokenAndAgent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_FAKE_RUNNER_OUTCOME", "")
	st, run := newRunWorkerTestFixture(t)
	beforeRun := "printf abcdef; exit 7"
	wf := newRunWorkerTestWorkflow(st)
	wf.Config.Hooks.BeforeRun = &beforeRun
	wf.Config.Hooks.MaxOutputBytes = 3

	if err := (Orchestrator{Store: st}).runWorker(run.ID, wf); err != nil {
		t.Fatalf("runWorker: %v", err)
	}

	assertHookFailureBeforeTokenAndAgent(t, st, run.ID, core.FailureBeforeRunFailed, "exit status 7")
	assertHookEvents(t, st, run.ID, "hook.before_run", []string{"started", "output", "failed"})
	assertHookStartedCommandRedacted(t, st, run.ID, "hook.before_run.started")
	assertHookOutput(t, st, run.ID, "hook.before_run.output", "redacted")
	assertHookEvents(t, st, run.ID, "hook.after_run", []string{"completed"})
}

func TestRunWorkerBeforeRunFailureRunsAfterRunBeforeFail(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_FAKE_RUNNER_OUTCOME", "")
	st, run := newRunWorkerTestFixture(t)
	marker := filepath.Join(t.TempDir(), "hook-order")
	beforeRun := fmt.Sprintf("printf b >> %q; exit 7", marker)
	afterRun := fmt.Sprintf("printf r >> %q", marker)
	wf := newRunWorkerTestWorkflow(st)
	wf.Config.Hooks.BeforeRun = &beforeRun
	wf.Config.Hooks.AfterRun = &afterRun

	if err := (Orchestrator{Store: st}).runWorker(run.ID, wf); err != nil {
		t.Fatalf("runWorker: %v", err)
	}

	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read hook order marker: %v", err)
	}
	if got := string(data); got != "br" {
		t.Fatalf("hook order marker = %q, want before_run then after_run", got)
	}
	assertEventOrder(t, st, run.ID, []string{"hook.before_run.failed", "hook.after_run.started", "run.failed"})
}

func TestRunWorkerHookOutputDoesNotStoreSyntheticSecret(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_FAKE_RUNNER_OUTCOME", "")
	st, run := newRunWorkerTestFixture(t)
	beforeRun := "printf SYNTHETIC_SECRET_TOKEN"
	wf := newRunWorkerTestWorkflow(st)
	wf.Config.Hooks.BeforeRun = &beforeRun

	if err := (Orchestrator{Store: st}).runWorker(run.ID, wf); err != nil {
		t.Fatalf("runWorker: %v", err)
	}

	rows, err := st.Project.Query(`SELECT data_json FROM run_events WHERE event_type='hook.before_run.output' AND run_id=?`, run.ID)
	if err != nil {
		t.Fatalf("query hook output event: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("hook output event count = %d, want 1", len(rows))
	}
	if got := rows[0]["data_json"].String(); strings.Contains(got, "SYNTHETIC_SECRET_TOKEN") {
		t.Fatalf("hook output leaked synthetic secret: %s", got)
	}
}

func TestRunWorkerHooksUseMinimalEnvironment(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_FAKE_RUNNER_OUTCOME", "")
	t.Setenv("DAEMON_RAW_SECRET", "should-not-leak")
	st, issue := newReadyDispatchIssue(t)
	run, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	marker := filepath.Join(t.TempDir(), "hook-env")
	afterCreate := fmt.Sprintf(`if [ -n "$DAEMON_RAW_SECRET" ]; then printf leaked > %[1]q; elif [ "$SYMPHONY_RUN_ID" != %[2]q ]; then printf missing-run > %[1]q; elif [ -z "$SYMPHONY_WORKSPACE_PATH" ]; then printf missing-workspace > %[1]q; else printf clean > %[1]q; fi`, marker, run.ID)
	wf := newRunWorkerTestWorkflow(st)
	wf.Config.Hooks.AfterCreate = &afterCreate

	if err := (Orchestrator{Store: st}).runWorker(run.ID, wf); err != nil {
		t.Fatalf("runWorker: %v", err)
	}

	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read hook env marker: %v", err)
	}
	if got := string(data); got != "clean" {
		t.Fatalf("hook env marker = %q, want clean", got)
	}
}

func TestRunWorkerFailsWhenWorkflowSnapshotInsertFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_FAKE_RUNNER_OUTCOME", "")
	st, run := newRunWorkerTestFixture(t)
	if err := st.Project.Exec(`CREATE TRIGGER fail_workflow_snapshot_insert BEFORE INSERT ON workflow_snapshots BEGIN SELECT RAISE(ABORT, 'workflow snapshot insert failed'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	wf := newRunWorkerTestWorkflow(st)

	if err := (Orchestrator{Store: st}).runWorker(run.ID, wf); err != nil {
		t.Fatalf("runWorker: %v", err)
	}

	assertRunFailedBeforeAgent(t, st, run.ID, "create workflow snapshot")
}

func TestRunWorkerFailsWhenWorkflowSnapshotAttachFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_FAKE_RUNNER_OUTCOME", "")
	st, run := newRunWorkerTestFixture(t)
	if err := st.Project.Exec(`CREATE TRIGGER fail_workflow_snapshot_attach BEFORE UPDATE OF workflow_snapshot_id ON run_attempts WHEN NEW.workflow_snapshot_id IS NOT NULL BEGIN SELECT RAISE(ABORT, 'workflow snapshot attach failed'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	wf := newRunWorkerTestWorkflow(st)

	if err := (Orchestrator{Store: st}).runWorker(run.ID, wf); err != nil {
		t.Fatalf("runWorker: %v", err)
	}

	assertRunFailedBeforeAgent(t, st, run.ID, "attach workflow snapshot")
}

func TestRunWorkerStoresWorkflowArtifactMaxBytesInToolTokenScope(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_FAKE_RUNNER_OUTCOME", "missing_handoff")
	st, err := store.InitProject(t.TempDir(), "TST")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	issue, err := st.CreateIssue(store.CreateIssueInput{
		Title:              "Artifact max bytes",
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
	run, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if _, err := st.CreateOrUpdateWorkspace(issue.ID, workspace, "test", "auto", "main", "base-sha"); err != nil {
		t.Fatalf("CreateOrUpdateWorkspace: %v", err)
	}
	cfg := config.Defaults(st.RepoRoot)
	cfg.Tools.ArtifactMaxBytes = 1234
	cfg.Tools.AgentCanCreateFollowups = false
	cfg.Tools.AgentCanSetBlocked = false
	wf := &config.Workflow{
		Path:       filepath.Join(st.RepoRoot, "WORKFLOW.md"),
		Config:     cfg,
		PromptBody: "Do the work.",
		Validation: config.Validation{Valid: true},
	}

	if err := (Orchestrator{Store: st}).runWorker(run.ID, wf); err != nil {
		t.Fatalf("runWorker: %v", err)
	}

	row, err := st.Project.QueryOne(`SELECT scope_json FROM run_tool_tokens WHERE run_id=?`, run.ID)
	if err != nil {
		t.Fatalf("query tool token: %v", err)
	}
	var scope map[string]any
	if err := json.Unmarshal([]byte(row["scope_json"].String()), &scope); err != nil {
		t.Fatalf("decode scope_json: %v", err)
	}
	if got := int64(scope["artifact_max_bytes"].(float64)); got != cfg.Tools.ArtifactMaxBytes {
		t.Fatalf("artifact_max_bytes = %d, want %d", got, cfg.Tools.ArtifactMaxBytes)
	}
	tools, ok := scope["tools"].([]any)
	if !ok {
		t.Fatalf("tools scope = %#v, want array", scope["tools"])
	}
	for _, denied := range []string{"issue.block", "followup.create"} {
		for _, tool := range tools {
			if tool == denied {
				t.Fatalf("tools scope includes disabled tool %q: %#v", denied, tools)
			}
		}
	}
}

func TestDispatchIssuePropagatesIssueReloadFailureOnHold(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_FAKE_RUNNER_OUTCOME", "hold")
	st, err := store.InitProject(t.TempDir(), "TST")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	issue, err := st.CreateIssue(store.CreateIssueInput{
		Title:              "Dispatch reload failure",
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
	if err := st.Project.Exec(`DROP TABLE issue_labels`); err != nil {
		t.Fatalf("drop issue_labels: %v", err)
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("DispatchIssue panicked on issue reload failure: %v", r)
		}
	}()

	_, err = (Orchestrator{Store: st}).DispatchIssue(issue.Identifier, "manual")

	if err == nil || !strings.Contains(err.Error(), "load issue labels") {
		t.Fatalf("DispatchIssue error = %v, want issue reload failure", err)
	}
}

func TestRunWorkerSkipsReviewGenerationWhenRunCancelledAfterHook(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYMPHONY_FAKE_RUNNER_OUTCOME", "")
	st, err := store.InitProject(t.TempDir(), "TST")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	issue, err := st.CreateIssue(store.CreateIssueInput{
		Title:              "Cancel after hook",
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
	run, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if _, err := st.CreateOrUpdateWorkspace(issue.ID, workspace, "test", "auto", "main", "base-sha"); err != nil {
		t.Fatalf("CreateOrUpdateWorkspace: %v", err)
	}
	marker := filepath.Join(t.TempDir(), "after-run-started")
	cancelErr := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(marker); err == nil {
				cancelErr <- st.CancelRun(run.ID, "cancelled during after_run")
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancelErr <- fmt.Errorf("after_run marker was not created")
	}()
	afterRun := fmt.Sprintf("touch %q && sleep 0.2", marker)
	cfg := config.Defaults(st.RepoRoot)
	cfg.Hooks.AfterRun = &afterRun
	wf := &config.Workflow{
		Path:       filepath.Join(st.RepoRoot, "WORKFLOW.md"),
		Config:     cfg,
		PromptBody: "Do the work.",
		Validation: config.Validation{Valid: true},
	}

	if err := (Orchestrator{Store: st}).runWorker(run.ID, wf); err != nil {
		t.Fatalf("runWorker: %v", err)
	}
	if err := <-cancelErr; err != nil {
		t.Fatalf("CancelRun: %v", err)
	}
	gotRun, err := st.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if gotRun.Status != core.RunCancelled {
		t.Fatalf("run status = %s, want %s", gotRun.Status, core.RunCancelled)
	}
	rows, err := st.Project.Query(`SELECT id FROM review_packets WHERE run_id=?`, run.ID)
	if err != nil {
		t.Fatalf("query review packets: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("review packet count = %d, want 0", len(rows))
	}
}

func TestRunAfterHookWithNegativeMaxOutputBytesDoesNotPanic(t *testing.T) {
	st, err := store.InitProject(t.TempDir(), "TST")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	issue, err := st.CreateIssue(store.CreateIssueInput{
		Title:              "Negative hook output limit",
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
	run, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	afterRun := "printf abcdef"
	cfg := config.Defaults(st.RepoRoot)
	cfg.Hooks.AfterRun = &afterRun
	cfg.Hooks.MaxOutputBytes = -1
	wf := &config.Workflow{
		Path:       filepath.Join(st.RepoRoot, "WORKFLOW.md"),
		Config:     cfg,
		PromptBody: "Do the work.",
		Validation: config.Validation{Valid: true},
	}

	if err := runAfterHook(context.Background(), st, wf, t.TempDir(), run.ID, issue.ID); err != nil {
		t.Fatalf("runAfterHook: %v", err)
	}
	rows, err := st.Project.Query(`SELECT data_json FROM run_events WHERE event_type='hook.after_run.output' AND run_id=?`, run.ID)
	if err != nil {
		t.Fatalf("query hook output event: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("hook output event count = %d, want 1", len(rows))
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(rows[0]["data_json"].String()), &data); err != nil {
		t.Fatalf("decode hook output event: %v", err)
	}
	if got := data["output"]; got != "" {
		t.Fatalf("hook output = %q, want empty output for negative max_output_bytes", got)
	}
}

func TestRunAfterHookStoresRedactedOutputFromLargeOutput(t *testing.T) {
	st, err := store.InitProject(t.TempDir(), "TST")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	issue, err := st.CreateIssue(store.CreateIssueInput{
		Title:              "Large hook output",
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
	run, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	afterRun := `i=0; while [ "$i" -lt 1024 ]; do printf 0123456789abcdef; i=$((i+1)); done`
	cfg := config.Defaults(st.RepoRoot)
	cfg.Hooks.AfterRun = &afterRun
	cfg.Hooks.MaxOutputBytes = 31
	wf := &config.Workflow{
		Path:       filepath.Join(st.RepoRoot, "WORKFLOW.md"),
		Config:     cfg,
		PromptBody: "Do the work.",
		Validation: config.Validation{Valid: true},
	}

	if err := runAfterHook(context.Background(), st, wf, t.TempDir(), run.ID, issue.ID); err != nil {
		t.Fatalf("runAfterHook: %v", err)
	}

	rows, err := st.Project.Query(`SELECT data_json FROM run_events WHERE event_type='hook.after_run.output' AND run_id=?`, run.ID)
	if err != nil {
		t.Fatalf("query hook output event: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("hook output event count = %d, want 1", len(rows))
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(rows[0]["data_json"].String()), &data); err != nil {
		t.Fatalf("decode hook output event: %v", err)
	}
	got, _ := data["output"].(string)
	if want := "redacted"; got != want {
		t.Fatalf("hook output = %q, want %q", got, want)
	}
}

func TestRunAfterHookTimeoutRecordsBoundedOutputBeforeReturning(t *testing.T) {
	st, err := store.InitProject(t.TempDir(), "TST")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	issue, err := st.CreateIssue(store.CreateIssueInput{
		Title:              "Timeout hook output",
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
	run, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	tempDir := t.TempDir()
	marker := filepath.Join(tempDir, "output-written")
	afterRun := fmt.Sprintf("printf abcdef; touch %q; sleep 5", marker)
	cfg := config.Defaults(st.RepoRoot)
	cfg.Hooks.AfterRun = &afterRun
	cfg.Hooks.MaxOutputBytes = 3
	cfg.Hooks.TimeoutMS = 500
	wf := &config.Workflow{
		Path:       filepath.Join(st.RepoRoot, "WORKFLOW.md"),
		Config:     cfg,
		PromptBody: "Do the work.",
		Validation: config.Validation{Valid: true},
	}

	started := time.Now()
	done := make(chan error, 1)
	go func() {
		done <- runAfterHook(context.Background(), st, wf, tempDir, run.ID, issue.ID)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("runAfterHook returned before hook wrote output marker: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("hook did not write output marker before deadline")
		}
		time.Sleep(10 * time.Millisecond)
	}
	err = <-done
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("runAfterHook returned after %s, want prompt timeout", elapsed)
	}
	if err == nil || err.Error() != "after_run timeout" {
		t.Fatalf("runAfterHook error = %v, want after_run timeout", err)
	}

	rows, err := st.Project.Query(`SELECT event_type,data_json FROM run_events WHERE event_type LIKE 'hook.after_run.%' AND run_id=? ORDER BY seq ASC`, run.ID)
	if err != nil {
		t.Fatalf("query hook events: %v", err)
	}
	wantTypes := []string{"hook.after_run.started", "hook.after_run.output", "hook.after_run.timeout"}
	if len(rows) != len(wantTypes) {
		t.Fatalf("hook event count = %d, want %d", len(rows), len(wantTypes))
	}
	for i, want := range wantTypes {
		if got := rows[i]["event_type"].String(); got != want {
			t.Fatalf("hook event %d = %s, want %s", i, got, want)
		}
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(rows[1]["data_json"].String()), &data); err != nil {
		t.Fatalf("decode hook output event: %v", err)
	}
	if got := data["output"]; got != "redacted" {
		t.Fatalf("hook output = %q, want redacted output", got)
	}
}

func newRunWorkerTestFixture(t *testing.T) (*store.Store, *core.RunAttempt) {
	t.Helper()
	st, err := store.InitProject(t.TempDir(), "TST")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	issue, err := st.CreateIssue(store.CreateIssueInput{
		Title:              "Workflow snapshot failure",
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
	run, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if _, err := st.CreateOrUpdateWorkspace(issue.ID, workspace, "test", "auto", "main", "base-sha"); err != nil {
		t.Fatalf("CreateOrUpdateWorkspace: %v", err)
	}
	return st, run
}

func newReadyDispatchIssue(t *testing.T) (*store.Store, *core.Issue) {
	t.Helper()
	st, err := store.InitProject(t.TempDir(), "TST")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	issue, err := st.CreateIssue(store.CreateIssueInput{
		Title:              "Dispatch runner",
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
	return st, issue
}

func writeCodexFixtureCommand(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codex-fixture")
	body := `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "codex 0.0.0-test"
  exit 0
fi
read start_turn
printf '%s\n' '{"type":"handshake","codex_version":"0.0.0-test","protocol_version":"protocol-test-v1","schema_version":"schema-test-v1","experimental_api":false}'
printf '%s\n' '{"type":"thread_started","thread_id":"thread_fixture"}'
printf '%s\n' '{"type":"turn_started","turn_id":"turn_fixture"}'
printf '%s\n' '{"type":"turn_progress","message":"working"}'
printf '%s\n' '{"type":"handoff","payload":{"summary":"Codex fixture completed.","changed_files":[],"tests":[],"risks":[],"verification":[],"followups":[],"target_state":"Human Review"}}'
printf '%s\n' '{"type":"turn_completed"}'
`
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write codex fixture command: %v", err)
	}
	return path
}

func writeCodexContinuationFixtureCommand(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codex-continuation")
	body := `#!/bin/sh
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
`
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write codex continuation fixture command: %v", err)
	}
	return path
}

func writeCancellableCodexFixtureCommand(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codex-cancellable")
	body := `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "codex 0.0.0-test"
  exit 0
fi
trap 'touch "$SYMPHONY_WORKSPACE_PATH/terminated"; exit 0' TERM
printf '%s\n' '{"type":"handshake","codex_version":"0.0.0-test","protocol_version":"protocol-test-v1","schema_version":"schema-test-v1","experimental_api":false}'
while true; do sleep 1; done
`
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write cancellable codex fixture command: %v", err)
	}
	return path
}

func writeWorkflowWithCodexCommand(t *testing.T, repoRoot, command string) {
	t.Helper()
	body := fmt.Sprintf(`---
agent:
  max_turns_per_run: 2
  max_handoff_continuations: 1
codex:
  command: %s
---
Do the work.
`, command)
	if err := os.WriteFile(filepath.Join(repoRoot, "WORKFLOW.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
}

func newRunWorkerTestWorkflow(st *store.Store) *config.Workflow {
	return &config.Workflow{
		Path:       filepath.Join(st.RepoRoot, "WORKFLOW.md"),
		Config:     config.Defaults(st.RepoRoot),
		PromptBody: "Do the work.",
		Validation: config.Validation{Valid: true},
	}
}

func assertRecordedStartTurn(t *testing.T, path string, wantContinuation bool, wantThreadID string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read start turn %s: %v", path, err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode start turn %s: %v", path, err)
	}
	if got["continuation"] != wantContinuation {
		t.Fatalf("continuation = %v, want %v", got["continuation"], wantContinuation)
	}
	if wantThreadID == "" {
		if _, ok := got["thread_id"]; ok {
			t.Fatalf("thread_id = %v, want absent", got["thread_id"])
		}
		return
	}
	if got["thread_id"] != wantThreadID {
		t.Fatalf("thread_id = %v, want %s", got["thread_id"], wantThreadID)
	}
}

func waitForRunEvent(t *testing.T, st *store.Store, runID, eventType string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		rows, err := st.Project.Query(`SELECT id FROM run_events WHERE run_id=? AND event_type=?`, runID, eventType)
		if err != nil {
			t.Fatalf("query run events: %v", err)
		}
		if len(rows) > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s event was not recorded before deadline", eventType)
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat %s: %v", path, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s was not created before deadline", path)
}

func assertChildOutputStopsGrowing(t *testing.T, path string) {
	t.Helper()
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat child output before stability check: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat child output after stability check: %v", err)
	}
	if after.Size() != before.Size() {
		t.Fatalf("child output kept growing after hook cancellation: size %d -> %d", before.Size(), after.Size())
	}
}

func assertEventOrder(t *testing.T, st *store.Store, runID string, want []string) {
	t.Helper()
	quoted := make([]string, 0, len(want))
	for _, eventType := range want {
		quoted = append(quoted, "'"+strings.ReplaceAll(eventType, "'", "''")+"'")
	}
	rows, err := st.Project.Query(`SELECT event_type FROM run_events WHERE run_id=? AND event_type IN (`+strings.Join(quoted, ",")+`) ORDER BY seq ASC`, runID)
	if err != nil {
		t.Fatalf("query run events: %v", err)
	}
	if len(rows) != len(want) {
		t.Fatalf("event count = %d, want %d", len(rows), len(want))
	}
	for i, eventType := range want {
		if got := rows[i]["event_type"].String(); got != eventType {
			t.Fatalf("event %d = %s, want %s", i, got, eventType)
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

func assertRunAttemptCount(t *testing.T, st *store.Store, want int) {
	t.Helper()
	rows, err := st.Project.Query(`SELECT COUNT(*) AS c FROM run_attempts`)
	if err != nil {
		t.Fatalf("query run attempt count: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("run attempt count rows = %d, want 1", len(rows))
	}
	if got := rows[0]["c"].Int(); got != want {
		t.Fatalf("run attempt count = %d, want %d", got, want)
	}
}

func assertRunFailedBeforeAgent(t *testing.T, st *store.Store, runID, wantMessage string) {
	t.Helper()
	gotRun, err := st.GetRun(runID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if gotRun.Status != core.RunFailed {
		t.Fatalf("run status = %s, want %s", gotRun.Status, core.RunFailed)
	}
	if gotRun.FailureCode == nil || *gotRun.FailureCode != core.FailurePromptRenderFailed {
		t.Fatalf("failure code = %v, want %s", gotRun.FailureCode, core.FailurePromptRenderFailed)
	}
	if gotRun.FailureMessage == nil || !strings.Contains(*gotRun.FailureMessage, wantMessage) {
		t.Fatalf("failure message = %v, want containing %q", gotRun.FailureMessage, wantMessage)
	}
	tokenRows, err := st.Project.Query(`SELECT id FROM run_tool_tokens WHERE run_id=?`, runID)
	if err != nil {
		t.Fatalf("query run tool tokens: %v", err)
	}
	if len(tokenRows) != 0 {
		t.Fatalf("run tool token count = %d, want 0", len(tokenRows))
	}
	handoffRows, err := st.Project.Query(`SELECT id FROM handoffs WHERE run_id=?`, runID)
	if err != nil {
		t.Fatalf("query handoffs: %v", err)
	}
	if len(handoffRows) != 0 {
		t.Fatalf("handoff count = %d, want 0", len(handoffRows))
	}
}

func assertHookFailureBeforeTokenAndAgent(t *testing.T, st *store.Store, runID string, wantCode core.FailureCode, wantMessage string) {
	t.Helper()
	gotRun, err := st.GetRun(runID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if gotRun.Status != core.RunFailed {
		t.Fatalf("run status = %s, want %s", gotRun.Status, core.RunFailed)
	}
	if gotRun.FailureCode == nil || *gotRun.FailureCode != wantCode {
		t.Fatalf("failure code = %v, want %s", gotRun.FailureCode, wantCode)
	}
	if gotRun.FailureMessage == nil || !strings.Contains(*gotRun.FailureMessage, wantMessage) {
		t.Fatalf("failure message = %v, want containing %q", gotRun.FailureMessage, wantMessage)
	}
	gotIssue, err := st.GetIssue(gotRun.IssueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if gotIssue.State != gotRun.SourceIssueState {
		t.Fatalf("issue state = %s, want restored source state %s", gotIssue.State, gotRun.SourceIssueState)
	}
	if !gotIssue.DispatchPaused {
		t.Fatal("issue dispatch_paused = false, want true")
	}
	if gotIssue.DispatchPauseReason == nil || *gotIssue.DispatchPauseReason != string(wantCode) {
		t.Fatalf("dispatch pause reason = %v, want %s", gotIssue.DispatchPauseReason, wantCode)
	}
	tokenRows, err := st.Project.Query(`SELECT id FROM run_tool_tokens WHERE run_id=?`, runID)
	if err != nil {
		t.Fatalf("query run tool tokens: %v", err)
	}
	if len(tokenRows) != 0 {
		t.Fatalf("run tool token count = %d, want 0", len(tokenRows))
	}
	agentRows, err := st.Project.Query(`SELECT event_type FROM run_events WHERE run_id=? AND event_type LIKE 'agent.%'`, runID)
	if err != nil {
		t.Fatalf("query agent events: %v", err)
	}
	if len(agentRows) != 0 {
		t.Fatalf("agent event count = %d, want 0", len(agentRows))
	}
}

func assertHookEvents(t *testing.T, st *store.Store, runID, prefix string, suffixes []string) {
	t.Helper()
	rows, err := st.Project.Query(`SELECT event_type FROM run_events WHERE run_id=? AND event_type LIKE ? ORDER BY seq ASC`, runID, prefix+".%")
	if err != nil {
		t.Fatalf("query hook events: %v", err)
	}
	if len(rows) != len(suffixes) {
		t.Fatalf("hook event count = %d, want %d", len(rows), len(suffixes))
	}
	for i, suffix := range suffixes {
		want := prefix + "." + suffix
		if got := rows[i]["event_type"].String(); got != want {
			t.Fatalf("hook event %d = %s, want %s", i, got, want)
		}
	}
}

func assertHookStartedCommandRedacted(t *testing.T, st *store.Store, runID, eventType string) {
	t.Helper()
	rows, err := st.Project.Query(`SELECT data_json FROM run_events WHERE run_id=? AND event_type=?`, runID, eventType)
	if err != nil {
		t.Fatalf("query hook started event: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("%s event count = %d, want 1", eventType, len(rows))
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(rows[0]["data_json"].String()), &data); err != nil {
		t.Fatalf("decode hook started event: %v", err)
	}
	if got := data["command"]; got != "redacted" {
		t.Fatalf("hook command = %q, want redacted", got)
	}
}

func assertHookOutput(t *testing.T, st *store.Store, runID, eventType, want string) {
	t.Helper()
	rows, err := st.Project.Query(`SELECT data_json FROM run_events WHERE run_id=? AND event_type=?`, runID, eventType)
	if err != nil {
		t.Fatalf("query hook output event: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("%s event count = %d, want 1", eventType, len(rows))
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(rows[0]["data_json"].String()), &data); err != nil {
		t.Fatalf("decode hook output event: %v", err)
	}
	if got := data["output"]; got != want {
		t.Fatalf("hook output = %q, want %q", got, want)
	}
}

func TestRunAfterHookStartFailureDoesNotPanic(t *testing.T) {
	st, err := store.InitProject(t.TempDir(), "TST")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	issue, err := st.CreateIssue(store.CreateIssueInput{
		Title:              "Start failure hook",
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
	run, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	afterRun := "printf should-not-run"
	cfg := config.Defaults(st.RepoRoot)
	cfg.Hooks.AfterRun = &afterRun
	wf := &config.Workflow{
		Path:       filepath.Join(st.RepoRoot, "WORKFLOW.md"),
		Config:     cfg,
		PromptBody: "Do the work.",
		Validation: config.Validation{Valid: true},
	}
	missingCWD := filepath.Join(t.TempDir(), "missing")

	if err := runAfterHook(context.Background(), st, wf, missingCWD, run.ID, issue.ID); err == nil {
		t.Fatalf("runAfterHook error = nil, want start failure")
	}

	rows, err := st.Project.Query(`SELECT event_type FROM run_events WHERE event_type LIKE 'hook.after_run.%' AND run_id=? ORDER BY seq ASC`, run.ID)
	if err != nil {
		t.Fatalf("query hook events: %v", err)
	}
	wantTypes := []string{"hook.after_run.started", "hook.after_run.output", "hook.after_run.failed"}
	if len(rows) != len(wantTypes) {
		t.Fatalf("hook event count = %d, want %d", len(rows), len(wantTypes))
	}
	for i, want := range wantTypes {
		if got := rows[i]["event_type"].String(); got != want {
			t.Fatalf("hook event %d = %s, want %s", i, got, want)
		}
	}
}
