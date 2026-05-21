package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"local-symphony/internal/config"
	"local-symphony/internal/core"
	"local-symphony/internal/store"
)

func TestPrepareUsesConfiguredBaseRefForWorktreeAndMetadata(t *testing.T) {
	repoRoot := t.TempDir()
	git(t, repoRoot, "init", "-b", "main")
	git(t, repoRoot, "config", "user.email", "test@example.com")
	git(t, repoRoot, "config", "user.name", "Test User")

	markerPath := filepath.Join(repoRoot, "marker.txt")
	if err := os.WriteFile(markerPath, []byte("from main\n"), 0o644); err != nil {
		t.Fatalf("write main marker: %v", err)
	}
	git(t, repoRoot, "add", "marker.txt")
	git(t, repoRoot, "commit", "-m", "main")
	mainSHA := strings.TrimSpace(git(t, repoRoot, "rev-parse", "main"))

	git(t, repoRoot, "checkout", "-b", "topic")
	if err := os.WriteFile(markerPath, []byte("from topic\n"), 0o644); err != nil {
		t.Fatalf("write topic marker: %v", err)
	}
	git(t, repoRoot, "commit", "-am", "topic")

	st, err := store.InitProject(repoRoot, "LOC")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)

	issue, err := st.CreateIssue(store.CreateIssueInput{
		Title:              "Use configured base",
		Description:        "Prepare workspace from configured base ref.",
		AcceptanceCriteria: []string{"Workspace uses configured base ref."},
		Priority:           3,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	issue, err = st.TransitionIssue(issue.ID, core.StateReady, "ready", "")
	if err != nil {
		t.Fatalf("TransitionIssue: %v", err)
	}
	run, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}

	cfg := config.Defaults(repoRoot)
	cfg.Workspace.Root = filepath.Join(t.TempDir(), "workspaces")
	cfg.Git.BaseRef = "main"

	ws, err := Manager{Store: st, Config: cfg}.Prepare(run, issue)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	gotMarker, err := os.ReadFile(filepath.Join(ws.Path, "marker.txt"))
	if err != nil {
		t.Fatalf("read workspace marker: %v", err)
	}
	if string(gotMarker) != "from main\n" {
		t.Fatalf("workspace marker = %q, want main content", gotMarker)
	}
	if ws.BaseSHA != mainSHA {
		t.Fatalf("BaseSHA = %q, want main SHA %q", ws.BaseSHA, mainSHA)
	}
	if ws.BaseRef != "main" {
		t.Fatalf("BaseRef = %q, want main", ws.BaseRef)
	}
	if ws.BaseRefConfig != "main" {
		t.Fatalf("BaseRefConfig = %q, want main", ws.BaseRefConfig)
	}

	storedRun, err := st.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if storedRun.BaseSHA == nil || *storedRun.BaseSHA != mainSHA {
		t.Fatalf("stored run BaseSHA = %v, want %q", storedRun.BaseSHA, mainSHA)
	}
	if storedRun.BaseRefConfig == nil || *storedRun.BaseRefConfig != "main" {
		t.Fatalf("stored run BaseRefConfig = %v, want main", storedRun.BaseRefConfig)
	}
}

func TestPrepareFailsWhenConfiguredBaseRefIsInvalid(t *testing.T) {
	repoRoot := t.TempDir()
	git(t, repoRoot, "init", "-b", "main")
	git(t, repoRoot, "config", "user.email", "test@example.com")
	git(t, repoRoot, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repoRoot, "marker.txt"), []byte("from main\n"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	git(t, repoRoot, "add", "marker.txt")
	git(t, repoRoot, "commit", "-m", "main")

	st, err := store.InitProject(repoRoot, "LOC")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)

	issue, err := st.CreateIssue(store.CreateIssueInput{
		Title:              "Invalid configured base",
		Description:        "Prepare should fail closed when the configured base ref is invalid.",
		AcceptanceCriteria: []string{"Workspace is not prepared from the wrong source."},
		Priority:           3,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	issue, err = st.TransitionIssue(issue.ID, core.StateReady, "ready", "")
	if err != nil {
		t.Fatalf("TransitionIssue: %v", err)
	}
	run, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}

	cfg := config.Defaults(repoRoot)
	cfg.Workspace.Root = filepath.Join(t.TempDir(), "workspaces")
	cfg.Git.BaseRef = "missing-base"

	if _, err := (Manager{Store: st, Config: cfg}).Prepare(run, issue); err == nil {
		t.Fatal("Prepare succeeded with an invalid configured base ref")
	}
	storedRun, err := st.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if storedRun.WorkspaceID != nil || storedRun.BaseRef != nil || storedRun.BaseSHA != nil {
		t.Fatalf("run workspace metadata = workspace_id:%v base_ref:%v base_sha:%v, want none", storedRun.WorkspaceID, storedRun.BaseRef, storedRun.BaseSHA)
	}
	if _, err := os.Stat(filepath.Join(cfg.Workspace.Root, issue.Identifier)); err == nil {
		t.Fatalf("workspace directory was created for invalid base ref")
	}
}

func TestPrepareFailsWithInvalidBaseRefEvenWhenWorkspacePathExists(t *testing.T) {
	repoRoot := t.TempDir()
	git(t, repoRoot, "init", "-b", "main")
	git(t, repoRoot, "config", "user.email", "test@example.com")
	git(t, repoRoot, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repoRoot, "marker.txt"), []byte("from main\n"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	git(t, repoRoot, "add", "marker.txt")
	git(t, repoRoot, "commit", "-m", "main")

	st, err := store.InitProject(repoRoot, "LOC")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)

	issue, err := st.CreateIssue(store.CreateIssueInput{
		Title:              "Invalid base with stale path",
		Description:        "Prepare should fail before accepting stale workspace content.",
		AcceptanceCriteria: []string{"Stale path does not mask an invalid base ref."},
		Priority:           3,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	issue, err = st.TransitionIssue(issue.ID, core.StateReady, "ready", "")
	if err != nil {
		t.Fatalf("TransitionIssue: %v", err)
	}
	run, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}

	cfg := config.Defaults(repoRoot)
	cfg.Workspace.Root = filepath.Join(t.TempDir(), "workspaces")
	cfg.Git.BaseRef = "missing-base"
	stalePath := filepath.Join(projectWorkspaceRoot(cfg.Workspace.Root, st.ProjectID), issue.Identifier)
	if err := os.MkdirAll(stalePath, 0o755); err != nil {
		t.Fatalf("create stale workspace path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stalePath, "marker.txt"), []byte("stale\n"), 0o644); err != nil {
		t.Fatalf("write stale marker: %v", err)
	}

	if _, err := (Manager{Store: st, Config: cfg}).Prepare(run, issue); err == nil {
		t.Fatal("Prepare succeeded with an invalid configured base ref and stale workspace path")
	}
	storedRun, err := st.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if storedRun.WorkspaceID != nil || storedRun.BaseRef != nil || storedRun.BaseSHA != nil {
		t.Fatalf("run workspace metadata = workspace_id:%v base_ref:%v base_sha:%v, want none", storedRun.WorkspaceID, storedRun.BaseRef, storedRun.BaseSHA)
	}
}

func TestPreparePropagatesRunWorkspaceBindingErrorForPreparedWorkspace(t *testing.T) {
	repoRoot := t.TempDir()
	st, err := store.InitProject(repoRoot, "LOC")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)

	issue := prepareReadyWorkspaceIssue(t, st, "Prepared workspace binding failure")
	run, err := st.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	if _, err := st.CreateOrUpdateWorkspace(issue.ID, filepath.Join(t.TempDir(), "workspace"), "branch", "auto", "main", "base-sha"); err != nil {
		t.Fatalf("CreateOrUpdateWorkspace: %v", err)
	}
	issue, err = st.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.Workspace == nil || issue.Workspace.Status != "prepared" {
		t.Fatalf("issue workspace = %#v, want prepared workspace", issue.Workspace)
	}
	if err := st.Project.Exec(`CREATE TRIGGER fail_run_workspace_bind BEFORE UPDATE OF workspace_id ON run_attempts BEGIN SELECT RAISE(ABORT, 'workspace bind failed'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	_, err = (Manager{Store: st, Config: config.Defaults(repoRoot)}).Prepare(run, issue)
	if err == nil {
		t.Fatal("Prepare succeeded, want workspace binding error")
	}
	if !strings.Contains(err.Error(), "workspace bind failed") {
		t.Fatalf("Prepare error = %v, want workspace bind failed", err)
	}
}

func TestProjectWorkspaceRootDoesNotAllowProjectIDPathTraversal(t *testing.T) {
	root := t.TempDir()
	path := projectWorkspaceRoot(root, "../../../outside")
	rel, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatalf("relative workspace root: %v", err)
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") {
		t.Fatalf("workspace root escaped base: base=%q path=%q rel=%q", root, path, rel)
	}
	if got := filepath.Base(path); !strings.HasPrefix(got, "project_") || strings.Contains(got, "..") || strings.ContainsAny(got, `/\`) {
		t.Fatalf("workspace root segment is not sanitized: %q", got)
	}
}

func prepareReadyWorkspaceIssue(t *testing.T, st *store.Store, title string) *core.Issue {
	t.Helper()
	issue, err := st.CreateIssue(store.CreateIssueInput{
		Title:              title,
		Description:        "desc",
		AcceptanceCriteria: []string{"done"},
		Priority:           3,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	issue, err = st.TransitionIssue(issue.ID, core.StateReady, "ready", "")
	if err != nil {
		t.Fatalf("TransitionIssue: %v", err)
	}
	return issue
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
