package review

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"local-symphony/internal/core"
	"local-symphony/internal/store"
)

const testMaxUntrackedPatchBytes int64 = 1024 * 1024

func TestGenerateReturnsErrorAndDoesNotInsertPacketWhenReviewArtifactWriteFails(t *testing.T) {
	st := newReviewTestStore(t)
	issue, run := prepareReviewRun(t, st)

	conflictPath := filepath.Join(st.RepoRoot, ".symphony", "artifacts", issue.Identifier, run.ID, "review.md")
	if err := os.MkdirAll(conflictPath, 0o755); err != nil {
		t.Fatalf("create conflicting review.md directory: %v", err)
	}

	_, err := (Generator{Store: st}).Generate(run.ID)
	if err == nil {
		t.Fatal("Generate succeeded, want review_packet_failed")
	}
	if got := core.AsAPIError(err).Code; got != core.ErrReviewPacketFailed {
		t.Fatalf("Generate error code = %s, want %s", got, core.ErrReviewPacketFailed)
	}
	assertReviewPacketCount(t, st, run.ID, 0)
}

func TestGenerateInsertsReviewPacketWithinOuterTransaction(t *testing.T) {
	st := newReviewTestStore(t)
	issue, run := prepareReviewRun(t, st)

	packetID, err := (Generator{Store: st}).Generate(run.ID)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	assertReviewPacketCount(t, st, run.ID, 1)
	got, err := st.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.LatestReviewPacketID == nil || *got.LatestReviewPacketID != packetID {
		t.Fatalf("LatestReviewPacketID = %v, want %s", got.LatestReviewPacketID, packetID)
	}
}

func TestGenerateOmitsTrackedProtectedDiffFromPatch(t *testing.T) {
	st := newReviewTestStore(t)
	workspace := initGitWorkspace(t)
	writeFile(t, workspace, ".env", "SECRET=original\n")
	writeFile(t, workspace, "app.txt", "old\n")
	runGit(t, workspace, "add", ".")
	runGit(t, workspace, "commit", "-m", "base")

	writeFile(t, workspace, ".env", "SECRET=changed\n")
	writeFile(t, workspace, "app.txt", "new\n")
	issue, run := prepareReviewRunWithWorkspace(t, st, workspace, []string{"app.txt", ".env"})

	_, err := (Generator{Store: st}).Generate(run.ID)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	patch := readReviewArtifact(t, st, issue, run, "changes.patch")
	if strings.Contains(patch, "SECRET=changed") || strings.Contains(patch, "diff --git a/.env b/.env") {
		t.Fatalf("changes.patch leaked protected diff:\n%s", patch)
	}
	if !strings.Contains(patch, "diff --git a/app.txt b/app.txt") || !strings.Contains(patch, "+new") {
		t.Fatalf("changes.patch missing allowed tracked diff:\n%s", patch)
	}
}

func TestGenerateTreatsChangedFilesAsLiteralPathspecs(t *testing.T) {
	st := newReviewTestStore(t)
	workspace := initGitWorkspace(t)
	writeFile(t, workspace, ".env", "SECRET=original\n")
	writeFile(t, workspace, "app.txt", "old\n")
	runGit(t, workspace, "add", ".")
	runGit(t, workspace, "commit", "-m", "base")

	writeFile(t, workspace, ".env", "SECRET=changed\n")
	writeFile(t, workspace, "app.txt", "new\n")
	issue, run := prepareReviewRunWithWorkspace(t, st, workspace, []string{"app.txt", ":(glob)**"})

	_, err := (Generator{Store: st}).Generate(run.ID)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	patch := readReviewArtifact(t, st, issue, run, "changes.patch")
	if strings.Contains(patch, "SECRET=changed") || strings.Contains(patch, "diff --git a/.env b/.env") {
		t.Fatalf("changes.patch leaked protected diff through pathspec magic:\n%s", patch)
	}
	if !strings.Contains(patch, "diff --git a/app.txt b/app.txt") || !strings.Contains(patch, "+new") {
		t.Fatalf("changes.patch missing allowed tracked diff:\n%s", patch)
	}
}

func TestGenerateDoesNotReincludeProtectedRenameFromHandoff(t *testing.T) {
	st := newReviewTestStore(t)
	workspace := initGitWorkspace(t)
	writeFile(t, workspace, ".env", "SECRET=original\n")
	writeFile(t, workspace, "app.txt", "old\n")
	runGit(t, workspace, "add", ".")
	runGit(t, workspace, "commit", "-m", "base")

	runGit(t, workspace, "mv", ".env", "safe.txt")
	writeFile(t, workspace, "app.txt", "new\n")
	issue, run := prepareReviewRunWithWorkspace(t, st, workspace, []string{"app.txt", "safe.txt"})

	_, err := (Generator{Store: st}).Generate(run.ID)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	patch := readReviewArtifact(t, st, issue, run, "changes.patch")
	if strings.Contains(patch, "SECRET=original") || strings.Contains(patch, "safe.txt") || strings.Contains(patch, ".env") {
		t.Fatalf("changes.patch leaked protected rename via handoff path:\n%s", patch)
	}
	if !strings.Contains(patch, "diff --git a/app.txt b/app.txt") || !strings.Contains(patch, "+new") {
		t.Fatalf("changes.patch missing allowed tracked diff:\n%s", patch)
	}
}

func TestGenerateIncludesRenamedPathDiff(t *testing.T) {
	st := newReviewTestStore(t)
	workspace := initGitWorkspace(t)
	writeFile(t, workspace, "old.txt", "shared\nold\n")
	runGit(t, workspace, "add", ".")
	runGit(t, workspace, "commit", "-m", "base")

	runGit(t, workspace, "mv", "old.txt", "new.txt")
	writeFile(t, workspace, "new.txt", "shared\nnew\n")
	issue, run := prepareReviewRunWithWorkspace(t, st, workspace, nil)

	_, err := (Generator{Store: st}).Generate(run.ID)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	patch := readReviewArtifact(t, st, issue, run, "changes.patch")
	if !strings.Contains(patch, "rename from old.txt") || !strings.Contains(patch, "rename to new.txt") || !strings.Contains(patch, "+new") {
		t.Fatalf("changes.patch missing rename metadata and destination diff:\n%s", patch)
	}
	changed := readReviewArtifact(t, st, issue, run, "changed-files.txt")
	if !strings.Contains(changed, "new.txt\n") {
		t.Fatalf("changed-files.txt missing renamed destination:\n%s", changed)
	}
}

func TestGenerateIncludesUntrackedFilesInsideDirectories(t *testing.T) {
	st := newReviewTestStore(t)
	workspace := initGitWorkspace(t)
	writeFile(t, workspace, "app.txt", "base\n")
	runGit(t, workspace, "add", ".")
	runGit(t, workspace, "commit", "-m", "base")

	writeFile(t, workspace, "notes/todo.txt", "nested untracked\n")
	issue, run := prepareReviewRunWithWorkspace(t, st, workspace, nil)

	_, err := (Generator{Store: st}).Generate(run.ID)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	patch := readReviewArtifact(t, st, issue, run, "changes.patch")
	if !strings.Contains(patch, "diff --git a/notes/todo.txt b/notes/todo.txt") || !strings.Contains(patch, "+nested untracked") {
		t.Fatalf("changes.patch missing nested untracked file:\n%s", patch)
	}
}

func TestGenerateAppendsUntrackedSyntheticPatchWhenTrackedDiffExists(t *testing.T) {
	st := newReviewTestStore(t)
	workspace := initGitWorkspace(t)
	writeFile(t, workspace, "app.txt", "old\n")
	runGit(t, workspace, "add", ".")
	runGit(t, workspace, "commit", "-m", "base")

	writeFile(t, workspace, "app.txt", "new\n")
	writeFile(t, workspace, "notes.txt", "untracked content\n")
	issue, run := prepareReviewRunWithWorkspace(t, st, workspace, []string{"app.txt", "notes.txt"})

	_, err := (Generator{Store: st}).Generate(run.ID)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	patch := readReviewArtifact(t, st, issue, run, "changes.patch")
	if !strings.Contains(patch, "diff --git a/app.txt b/app.txt") || !strings.Contains(patch, "+new") {
		t.Fatalf("changes.patch missing tracked diff:\n%s", patch)
	}
	if !strings.Contains(patch, "diff --git a/notes.txt b/notes.txt") || !strings.Contains(patch, "+untracked content") {
		t.Fatalf("changes.patch missing untracked synthetic patch:\n%s", patch)
	}
}

func TestSyntheticPatchPreservesBlankLines(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "notes.txt", "first\n\nthird\n")

	patch := syntheticPatch(root, []UntrackedInfo{{Path: "notes.txt", PatchIncluded: true}})
	if !strings.Contains(patch, "+first\n+\n+third\n") {
		t.Fatalf("synthetic patch did not preserve blank line:\n%s", patch)
	}
}

func TestUntrackedInfoOmitsLargeFileWithoutHashingContents(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "large.txt", strings.Repeat("x", int(testMaxUntrackedPatchBytes)+1))

	info := untrackedInfo(root, "large.txt")
	if info.Path != "large.txt" || info.SizeBytes <= testMaxUntrackedPatchBytes {
		t.Fatalf("unexpected untracked info: %+v", info)
	}
	if info.PatchIncluded {
		t.Fatalf("large file PatchIncluded = true, want false")
	}
	if info.SHA256 != "" {
		t.Fatalf("large file SHA256 = %q, want empty to avoid reading contents", info.SHA256)
	}
	if info.Reason == nil || *info.Reason != "binary or large file omitted from patch" {
		t.Fatalf("large file reason = %v, want binary/large omission", info.Reason)
	}
	if patch := syntheticPatch(root, []UntrackedInfo{info}); patch != "" {
		t.Fatalf("large file synthetic patch = %q, want empty", patch)
	}
}

func TestGenerateOmitsUntrackedSymlinkTargetsFromPatch(t *testing.T) {
	st := newReviewTestStore(t)
	workspace := initGitWorkspace(t)
	writeFile(t, workspace, "app.txt", "base\n")
	runGit(t, workspace, "add", ".")
	runGit(t, workspace, "commit", "-m", "base")

	writeFile(t, workspace, ".env", "SECRET=protected\n")
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("SECRET=outside\n"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.Symlink(".env", filepath.Join(workspace, "safe-env.txt")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "safe-outside.txt")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	issue, run := prepareReviewRunWithWorkspace(t, st, workspace, []string{"safe-env.txt", "safe-outside.txt"})

	_, err := (Generator{Store: st}).Generate(run.ID)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	patch := readReviewArtifact(t, st, issue, run, "changes.patch")
	if strings.Contains(patch, "SECRET=protected") || strings.Contains(patch, "SECRET=outside") {
		t.Fatalf("changes.patch leaked symlink target:\n%s", patch)
	}
	untracked := readUntrackedArtifact(t, st, issue, run)
	for _, path := range []string{"safe-env.txt", "safe-outside.txt"} {
		info, ok := untracked[path]
		if !ok {
			t.Fatalf("untracked artifact missing %s: %+v", path, untracked)
		}
		if info.PatchIncluded {
			t.Fatalf("%s PatchIncluded = true, want false", path)
		}
	}
}

func newReviewTestStore(t *testing.T) *store.Store {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	repoRoot := filepath.Join(t.TempDir(), "repo")
	st, err := store.InitProject(repoRoot, "TST")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	return st
}

func prepareReviewRun(t *testing.T, st *store.Store) (*core.Issue, *core.RunAttempt) {
	t.Helper()
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "changed.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatalf("write workspace file: %v", err)
	}
	return prepareReviewRunWithWorkspace(t, st, workspace, []string{"changed.txt"})
}

func prepareReviewRunWithWorkspace(t *testing.T, st *store.Store, workspace string, changedFiles []string) (*core.Issue, *core.RunAttempt) {
	t.Helper()
	issue, err := st.CreateIssue(store.CreateIssueInput{
		Title:              "Review packet write failure",
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
	wsID, err := st.CreateOrUpdateWorkspace(issue.ID, workspace, "review-test", "auto", "main", "base-sha")
	if err != nil {
		t.Fatalf("CreateOrUpdateWorkspace: %v", err)
	}
	if err := st.SetRunWorkspace(run.ID, wsID, "review-test", "auto", "main", "base-sha"); err != nil {
		t.Fatalf("SetRunWorkspace: %v", err)
	}
	if _, err := st.InsertHandoff(issue.ID, run.ID, "payload-hash", map[string]any{
		"summary":       "ready for review",
		"changed_files": changedFiles,
		"target_state":  "Human Review",
	}); err != nil {
		t.Fatalf("InsertHandoff: %v", err)
	}
	issue, err = st.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	return issue, run
}

func assertReviewPacketCount(t *testing.T, st *store.Store, runID string, want int) {
	t.Helper()
	row, err := st.Project.QueryOne(`SELECT COUNT(*) AS c FROM review_packets WHERE run_id=? AND status='generated'`, runID)
	if err != nil {
		t.Fatalf("count review packets: %v", err)
	}
	if got := row["c"].Int(); got != want {
		t.Fatalf("generated review packet count = %d, want %d", got, want)
	}
}

func initGitWorkspace(t *testing.T) string {
	t.Helper()
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	runGit(t, workspace, "init")
	runGit(t, workspace, "config", "user.email", "review@example.test")
	runGit(t, workspace, "config", "user.name", "Review Test")
	return workspace
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func writeFile(t *testing.T, root, rel, data string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func readReviewArtifact(t *testing.T, st *store.Store, issue *core.Issue, run *core.RunAttempt, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(st.RepoRoot, ".symphony", "artifacts", issue.Identifier, run.ID, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

func readUntrackedArtifact(t *testing.T, st *store.Store, issue *core.Issue, run *core.RunAttempt) map[string]UntrackedInfo {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(st.RepoRoot, ".symphony", "artifacts", issue.Identifier, run.ID, "untracked-files.json"))
	if err != nil {
		t.Fatalf("read untracked-files.json: %v", err)
	}
	var items []UntrackedInfo
	if err := json.Unmarshal(b, &items); err != nil {
		t.Fatalf("unmarshal untracked-files.json: %v", err)
	}
	out := map[string]UntrackedInfo{}
	for _, item := range items {
		out[item.Path] = item
	}
	return out
}
