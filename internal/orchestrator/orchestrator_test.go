package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"local-symphony/internal/config"
	"local-symphony/internal/core"
	"local-symphony/internal/store"
)

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
