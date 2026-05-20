package orchestrator

import (
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

	if err := runAfterHook(st, wf, t.TempDir(), run.ID, issue.ID); err != nil {
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

func TestRunAfterHookStoresOnlyMaxOutputBytesFromLargeOutput(t *testing.T) {
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

	if err := runAfterHook(st, wf, t.TempDir(), run.ID, issue.ID); err != nil {
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
	if len(got) != cfg.Hooks.MaxOutputBytes {
		t.Fatalf("hook output length = %d, want %d", len(got), cfg.Hooks.MaxOutputBytes)
	}
	if want := "0123456789abcdef0123456789abcde"; got != want {
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
		done <- runAfterHook(st, wf, tempDir, run.ID, issue.ID)
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
	if got := data["output"]; got != "abc" {
		t.Fatalf("hook output = %q, want bounded output", got)
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

func newRunWorkerTestWorkflow(st *store.Store) *config.Workflow {
	return &config.Workflow{
		Path:       filepath.Join(st.RepoRoot, "WORKFLOW.md"),
		Config:     config.Defaults(st.RepoRoot),
		PromptBody: "Do the work.",
		Validation: config.Validation{Valid: true},
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

	if err := runAfterHook(st, wf, missingCWD, run.ID, issue.ID); err == nil {
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
