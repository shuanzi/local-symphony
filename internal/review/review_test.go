package review

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"local-symphony/internal/core"
	"local-symphony/internal/store"
)

const testMaxUntrackedPatchBytes int64 = 1024 * 1024

// originalHome captures the HOME directory before any test has
// a chance to override it via t.Setenv. Several helpers in this
// package rely on HOME to redirect project DB paths; that
// redirecting also breaks python3's site.getusersitepackages()
// inside the schema-validator subprocess. We freeze the value
// at process start and use it for the validator subprocess
// regardless of the test's later HOME mutation.
var originalHome = os.Getenv("HOME")

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

func TestGenerateIncludesTrackedPathWithSpacesInPatch(t *testing.T) {
	st := newReviewTestStore(t)
	workspace := initGitWorkspace(t)
	writeFile(t, workspace, "space name.txt", "old\n")
	runGit(t, workspace, "add", ".")
	runGit(t, workspace, "commit", "-m", "base")

	writeFile(t, workspace, "space name.txt", "new\n")
	issue, run := prepareReviewRunWithWorkspace(t, st, workspace, nil)

	_, err := (Generator{Store: st}).Generate(run.ID)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	patch := readReviewArtifact(t, st, issue, run, "changes.patch")
	if !strings.Contains(patch, "diff --git a/space name.txt b/space name.txt") || !strings.Contains(patch, "+new") {
		t.Fatalf("changes.patch missing tracked file with spaces:\n%s", patch)
	}
}

func TestGenerateCleanGitWorkspaceDoesNotCreateSyntheticWorkspacePatch(t *testing.T) {
	st := newReviewTestStore(t)
	workspace := initGitWorkspace(t)
	writeFile(t, workspace, "app.txt", "base\n")
	writeFile(t, workspace, "notes/space name.txt", "tracked\n")
	runGit(t, workspace, "add", ".")
	runGit(t, workspace, "commit", "-m", "base")
	issue, run := prepareReviewRunWithWorkspace(t, st, workspace, nil)

	_, err := (Generator{Store: st}).Generate(run.ID)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	patch := readReviewArtifact(t, st, issue, run, "changes.patch")
	if strings.Contains(patch, "diff --git a/app.txt b/app.txt") || strings.Contains(patch, "diff --git a/notes/space name.txt b/notes/space name.txt") {
		t.Fatalf("changes.patch included synthetic clean workspace patch:\n%s", patch)
	}
	changed := strings.TrimSpace(readReviewArtifact(t, st, issue, run, "changed-files.txt"))
	if changed != "" {
		t.Fatalf("changed-files.txt = %q, want empty", changed)
	}
	untracked := readUntrackedArtifact(t, st, issue, run)
	if len(untracked) != 0 {
		t.Fatalf("untracked-files.json = %+v, want empty", untracked)
	}
	var packet struct {
		ChangedFiles []string `json:"changed_files"`
	}
	if err := json.Unmarshal([]byte(readReviewArtifact(t, st, issue, run, "review.json")), &packet); err != nil {
		t.Fatalf("unmarshal review.json: %v", err)
	}
	if len(packet.ChangedFiles) != 0 {
		t.Fatalf("review.json changed_files = %+v, want empty", packet.ChangedFiles)
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
	changed := readReviewArtifact(t, st, issue, run, "changed-files.txt")
	if strings.Contains(changed, "safe.txt") || strings.Contains(changed, ".env") {
		t.Fatalf("changed-files.txt leaked protected rename via handoff path:\n%s", changed)
	}
	diffstat := readReviewArtifact(t, st, issue, run, "diffstat.txt")
	if strings.Contains(diffstat, "safe.txt") || strings.Contains(diffstat, ".env") {
		t.Fatalf("diffstat.txt leaked protected rename via handoff path:\n%s", diffstat)
	}
	var packet struct {
		ChangedFiles []string `json:"changed_files"`
	}
	if err := json.Unmarshal([]byte(readReviewArtifact(t, st, issue, run, "review.json")), &packet); err != nil {
		t.Fatalf("unmarshal review.json: %v", err)
	}
	if contains(packet.ChangedFiles, "safe.txt") || contains(packet.ChangedFiles, ".env") {
		t.Fatalf("review.json changed_files leaked protected rename: %+v", packet.ChangedFiles)
	}
	if !strings.Contains(patch, "diff --git a/app.txt b/app.txt") || !strings.Contains(patch, "+new") {
		t.Fatalf("changes.patch missing allowed tracked diff:\n%s", patch)
	}
}

func TestGenerateOmitsUntrackedRenameOfProtectedDeletedFile(t *testing.T) {
	st := newReviewTestStore(t)
	workspace := initGitWorkspace(t)
	writeFile(t, workspace, ".env", "SECRET=original\n")
	writeFile(t, workspace, "app.txt", "base\n")
	runGit(t, workspace, "add", ".")
	runGit(t, workspace, "commit", "-m", "base")

	if err := os.Rename(filepath.Join(workspace, ".env"), filepath.Join(workspace, "safe.txt")); err != nil {
		t.Fatalf("rename protected file through filesystem: %v", err)
	}
	writeFile(t, workspace, "notes.txt", "ordinary untracked\n")
	issue, run := prepareReviewRunWithWorkspace(t, st, workspace, nil)

	_, err := (Generator{Store: st}).Generate(run.ID)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	patch := readReviewArtifact(t, st, issue, run, "changes.patch")
	if strings.Contains(patch, "SECRET=original") || strings.Contains(patch, "safe.txt") || strings.Contains(patch, ".env") {
		t.Fatalf("changes.patch leaked protected filesystem rename:\n%s", patch)
	}
	if !strings.Contains(patch, "diff --git a/notes.txt b/notes.txt") || !strings.Contains(patch, "+ordinary untracked") {
		t.Fatalf("changes.patch missing ordinary untracked file:\n%s", patch)
	}
	changed := readReviewArtifact(t, st, issue, run, "changed-files.txt")
	if strings.Contains(changed, "safe.txt") || strings.Contains(changed, ".env") {
		t.Fatalf("changed-files.txt leaked protected filesystem rename:\n%s", changed)
	}
	if !strings.Contains(changed, "notes.txt\n") {
		t.Fatalf("changed-files.txt missing ordinary untracked file:\n%s", changed)
	}
	diffstat := readReviewArtifact(t, st, issue, run, "diffstat.txt")
	if strings.Contains(diffstat, "safe.txt") || strings.Contains(diffstat, ".env") {
		t.Fatalf("diffstat.txt leaked protected filesystem rename:\n%s", diffstat)
	}
	if !strings.Contains(diffstat, "1\t0\tnotes.txt") {
		t.Fatalf("diffstat.txt missing ordinary untracked file:\n%s", diffstat)
	}
	reviewJSON := readReviewArtifact(t, st, issue, run, "review.json")
	if strings.Contains(reviewJSON, "SECRET=original") || strings.Contains(reviewJSON, "safe.txt") || strings.Contains(reviewJSON, ".env") {
		t.Fatalf("review.json leaked protected filesystem rename:\n%s", reviewJSON)
	}
	if !strings.Contains(reviewJSON, `"notes.txt"`) {
		t.Fatalf("review.json missing ordinary untracked file:\n%s", reviewJSON)
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

func TestGenerateIncludesRenamedPathWithSpacesDiff(t *testing.T) {
	st := newReviewTestStore(t)
	workspace := initGitWorkspace(t)
	writeFile(t, workspace, "old name.txt", "shared\nold\n")
	runGit(t, workspace, "add", ".")
	runGit(t, workspace, "commit", "-m", "base")

	runGit(t, workspace, "mv", "old name.txt", "new name.txt")
	writeFile(t, workspace, "new name.txt", "shared\nnew\n")
	issue, run := prepareReviewRunWithWorkspace(t, st, workspace, nil)

	_, err := (Generator{Store: st}).Generate(run.ID)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	patch := readReviewArtifact(t, st, issue, run, "changes.patch")
	if !strings.Contains(patch, "rename from old name.txt") || !strings.Contains(patch, "rename to new name.txt") || !strings.Contains(patch, "+new") {
		t.Fatalf("changes.patch missing rename with spaces:\n%s", patch)
	}
	changed := readReviewArtifact(t, st, issue, run, "changed-files.txt")
	if !strings.Contains(changed, "new name.txt\n") {
		t.Fatalf("changed-files.txt missing renamed destination with spaces:\n%s", changed)
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

func TestGenerateIncludesSyntheticUntrackedNumstat(t *testing.T) {
	st := newReviewTestStore(t)
	workspace := initGitWorkspace(t)
	writeFile(t, workspace, "app.txt", "base\n")
	runGit(t, workspace, "add", ".")
	runGit(t, workspace, "commit", "-m", "base")

	writeFile(t, workspace, "notes/todo.txt", "first\nsecond\n")
	issue, run := prepareReviewRunWithWorkspace(t, st, workspace, nil)

	_, err := (Generator{Store: st}).Generate(run.ID)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	patch := readReviewArtifact(t, st, issue, run, "changes.patch")
	if !strings.Contains(patch, "diff --git a/notes/todo.txt b/notes/todo.txt") {
		t.Fatalf("changes.patch missing untracked synthetic patch:\n%s", patch)
	}
	diffstat := readReviewArtifact(t, st, issue, run, "diffstat.txt")
	if strings.Contains(diffstat, "0\t0\tgenerated") {
		t.Fatalf("diffstat.txt used generated fallback despite synthetic patch:\n%s", diffstat)
	}
	if !strings.Contains(diffstat, "2\t0\tnotes/todo.txt") {
		t.Fatalf("diffstat.txt missing synthetic untracked numstat:\n%s", diffstat)
	}
}

func TestGenerateIncludesUntrackedPathWithSpacesInArtifacts(t *testing.T) {
	st := newReviewTestStore(t)
	workspace := initGitWorkspace(t)
	writeFile(t, workspace, "app.txt", "base\n")
	runGit(t, workspace, "add", ".")
	runGit(t, workspace, "commit", "-m", "base")

	writeFile(t, workspace, "notes/space name.txt", "untracked content\n")
	issue, run := prepareReviewRunWithWorkspace(t, st, workspace, nil)

	_, err := (Generator{Store: st}).Generate(run.ID)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	patch := readReviewArtifact(t, st, issue, run, "changes.patch")
	if !strings.Contains(patch, "diff --git a/notes/space name.txt b/notes/space name.txt") || !strings.Contains(patch, "+untracked content") {
		t.Fatalf("changes.patch missing untracked file with spaces:\n%s", patch)
	}
	untracked := readUntrackedArtifact(t, st, issue, run)
	if _, ok := untracked["notes/space name.txt"]; !ok {
		t.Fatalf("untracked artifact missing path with spaces: %+v", untracked)
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

func TestSyntheticPatchMarksFileWithoutTrailingNewline(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "notes.txt", "first line\nlast line")

	patch := syntheticPatch(root, []UntrackedInfo{{Path: "notes.txt", PatchIncluded: true}})
	if !strings.Contains(patch, "+first line\n+last line\n\\ No newline at end of file\n") {
		t.Fatalf("synthetic patch missing no-newline marker:\n%s", patch)
	}
}

func TestSyntheticPatchDoesNotMarkFileWithTrailingNewline(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "notes.txt", "untracked content\n")

	patch := syntheticPatch(root, []UntrackedInfo{{Path: "notes.txt", PatchIncluded: true}})
	if strings.Contains(patch, "\\ No newline at end of file") {
		t.Fatalf("synthetic patch incorrectly added no-newline marker:\n%s", patch)
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

func TestGenerateWritesStructuredFieldsToReviewJSONArtifact(t *testing.T) {
	st := newReviewTestStore(t)
	workspace := initGitWorkspace(t)
	writeFile(t, workspace, "app.txt", "base\n")
	runGit(t, workspace, "add", ".")
	runGit(t, workspace, "commit", "-m", "base")
	writeFile(t, workspace, "app.txt", "new\n")
	issue, run := prepareReviewRunWithWorkspace(t, st, workspace, []string{"app.txt"})

	if _, err := (Generator{Store: st}).Generate(run.ID); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var packet map[string]any
	if err := json.Unmarshal([]byte(readReviewArtifact(t, st, issue, run, "review.json")), &packet); err != nil {
		t.Fatalf("unmarshal review.json: %v", err)
	}
	required := []string{
		"summary",
		"acceptance_criteria",
		"handoff",
		"changed_files",
		"diff",
		"tests",
		"risks",
		"verification",
		"approvals",
		"tool_calls",
		"git",
		"how_to_continue",
	}
	for _, key := range required {
		if _, ok := packet[key]; !ok {
			t.Fatalf("review.json missing required structured field %q: %#v", key, packet)
		}
	}
	if got, _ := packet["summary"].(string); got != "ready for review" {
		t.Fatalf("summary = %q, want %q", got, "ready for review")
	}
	ac, ok := packet["acceptance_criteria"].([]any)
	if !ok || len(ac) == 0 || ac[0] != "done" {
		t.Fatalf("acceptance_criteria = %#v, want at least [done]", packet["acceptance_criteria"])
	}
	ht, ok := packet["handoff"].(map[string]any)
	if !ok {
		t.Fatalf("handoff field not object: %#v", packet["handoff"])
	}
	if ts, _ := ht["target_state"].(string); ts != "Human Review" {
		t.Fatalf("handoff.target_state = %q, want Human Review", ts)
	}
	if diff, _ := packet["diff"].(string); !strings.Contains(diff, "diff --git a/app.txt b/app.txt") {
		t.Fatalf("diff field missing tracked diff: %q", diff)
	}
	if raw, _ := packet["raw_prompt_exposed"].(bool); raw {
		t.Fatalf("raw_prompt_exposed should be false; got %#v", packet["raw_prompt_exposed"])
	}
	if hc, _ := packet["how_to_continue"].(string); !strings.Contains(hc, "Send to Rework") || !strings.Contains(hc, "Mark Done") {
		t.Fatalf("how_to_continue = %q, want operator guidance mentioning Send to Rework / Mark Done", hc)
	}
}

func TestGenerateOmitsLargePatchFromStructuredReviewJSON(t *testing.T) {
	st := newReviewTestStore(t)
	workspace := initGitWorkspace(t)
	writeFile(t, workspace, "large.txt", "base\n")
	runGit(t, workspace, "add", ".")
	runGit(t, workspace, "commit", "-m", "base")
	largeBody := strings.Repeat("large diff line\n", 9000)
	writeFile(t, workspace, "large.txt", largeBody)
	issue, run := prepareReviewRunWithWorkspace(t, st, workspace, []string{"large.txt"})

	if _, err := (Generator{Store: st}).Generate(run.ID); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	patch := readReviewArtifact(t, st, issue, run, "changes.patch")
	if got := strings.Count(patch, "+large diff line"); got < 8000 {
		t.Fatalf("changes.patch did not preserve full patch artifact")
	}
	var packet map[string]any
	if err := json.Unmarshal([]byte(readReviewArtifact(t, st, issue, run, "review.json")), &packet); err != nil {
		t.Fatalf("unmarshal review.json: %v", err)
	}
	diff, _ := packet["diff"].(string)
	if strings.Count(diff, "+large diff line") >= 8000 {
		t.Fatalf("review.json diff embedded the full large patch; len(diff)=%d", len(diff))
	}
	if !strings.Contains(diff, "omitted") || !strings.Contains(diff, "changes.patch") {
		t.Fatalf("review.json diff = %q, want omission message pointing to changes.patch", diff)
	}
}

func TestGenerateOmitsBinaryPatchFromStructuredReviewJSON(t *testing.T) {
	st := newReviewTestStore(t)
	workspace := initGitWorkspace(t)
	binaryPath := filepath.Join(workspace, "bin.dat")
	if err := os.WriteFile(binaryPath, bytesForBinaryPatch(0x04), 0o644); err != nil {
		t.Fatalf("write base binary file: %v", err)
	}
	runGit(t, workspace, "add", ".")
	runGit(t, workspace, "commit", "-m", "base")
	if err := os.WriteFile(binaryPath, bytesForBinaryPatch(0x09), 0o644); err != nil {
		t.Fatalf("write changed binary file: %v", err)
	}
	issue, run := prepareReviewRunWithWorkspace(t, st, workspace, []string{"bin.dat"})

	if _, err := (Generator{Store: st}).Generate(run.ID); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	patch := readReviewArtifact(t, st, issue, run, "changes.patch")
	if !strings.Contains(patch, "GIT binary patch") || !strings.Contains(patch, "literal ") {
		t.Fatalf("changes.patch missing git binary patch content:\n%s", patch)
	}
	var packet map[string]any
	if err := json.Unmarshal([]byte(readReviewArtifact(t, st, issue, run, "review.json")), &packet); err != nil {
		t.Fatalf("unmarshal review.json: %v", err)
	}
	diff, _ := packet["diff"].(string)
	want := "# Diff omitted from structured review JSON: binary patch content is not embedded. See changes.patch artifact for the full patch.\n"
	if diff != want {
		t.Fatalf("review.json diff = %q, want binary omission message pointing to changes.patch", diff)
	}
	if strings.Contains(diff, "GIT binary patch") || strings.Contains(diff, "literal ") {
		t.Fatalf("review.json diff embedded binary patch content: %q", diff)
	}
}

func TestReviewFailureDoesNotTransitionIssueToHumanReview(t *testing.T) {
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
	// Simulate the orchestrator's behavior: a review_packet_failed run
	// is reported through FailRun, not CompleteRunWithReview. Verify
	// the issue is not advanced to Human Review.
	if err := st.FailRun(run.ID, core.FailureReviewPacketFailed, err.Error(), core.RunFailed); err != nil {
		t.Fatalf("FailRun: %v", err)
	}
	got, err := st.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.State == core.StateHumanReview {
		t.Fatalf("issue state = %s after review packet failure, want not Human Review", got.State)
	}
	if !got.DispatchPaused {
		t.Fatalf("issue dispatch_paused = false after review packet failure, want true")
	}
	// No review packet row should be associated with the failed run.
	assertReviewPacketCount(t, st, run.ID, 0)
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

func bytesForBinaryPatch(marker byte) []byte {
	pattern := []byte{0x00, 0x01, 0x02, 0x03, marker, 0x00, 0xff}
	out := make([]byte, 0, len(pattern)*200)
	for i := 0; i < 200; i++ {
		out = append(out, pattern...)
	}
	return out
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

// TestReviewPacketFileSchemaValidatesAgainstGeneratedPacket pins
// the contract that the review.json artifact written by
// Generator.Generate actually validates against
// schemas/review_packet.schema.json. If a future schema change
// (or a Generator regression) breaks the round-trip, the
// jsonschema validator surfaces it directly so we catch it
// before the file ships to operators.
//
// The test calls the same Draft202012Validator that
// scripts/validate_contracts.py uses. We shell out to python3
// because the project intentionally keeps the in-Go test
// dependency surface narrow (no jsonschema-go module is
// imported elsewhere).
func TestReviewPacketFileSchemaValidatesAgainstGeneratedPacket(t *testing.T) {
	st := newReviewTestStore(t)
	workspace := initGitWorkspace(t)
	writeFile(t, workspace, "app.txt", "base\n")
	runGit(t, workspace, "add", ".")
	runGit(t, workspace, "commit", "-m", "base")
	writeFile(t, workspace, "app.txt", "new\n")
	issue, run := prepareReviewRunWithWorkspace(t, st, workspace, []string{"app.txt"})

	if _, err := (Generator{Store: st}).Generate(run.ID); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	reviewPath := filepath.Join(st.RepoRoot, ".symphony", "artifacts", issue.Identifier, run.ID, "review.json")
	if _, err := os.Stat(reviewPath); err != nil {
		t.Fatalf("review.json missing: %v", err)
	}
	schemaPath := filepath.Join(repoRootForReviewTest(t), "schemas", "review_packet.schema.json")
	validateJSONAgainstSchema(t, schemaPath, reviewPath)
}

func TestSchemaValidatorScriptMissingJsonschemaReturnsSkipFailure(t *testing.T) {
	pythonBin, err := exec.LookPath("python3")
	if err != nil {
		t.Skipf("python3 not on PATH: %v", err)
	}
	blockingSite := t.TempDir()
	if err := os.WriteFile(filepath.Join(blockingSite, "jsonschema.py"), []byte(`raise ImportError("forced missing jsonschema")`), 0o644); err != nil {
		t.Fatalf("write jsonschema blocker: %v", err)
	}
	cmd := exec.Command(pythonBin, "-c", schemaValidatorScript(blockingSite), "schema.json", "data.json")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("schema validator exited 0 for missing jsonschema; output:\n%s", string(out))
	}
	if !strings.Contains(string(out), "__SCHEMA_SKIP__") {
		t.Fatalf("schema validator output missing skip marker:\n%s", string(out))
	}
}

// repoRootForReviewTest walks up from the current working
// directory until it finds go.mod so the test can locate the
// schemas/ directory regardless of the test runner's CWD.
func repoRootForReviewTest(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := wd
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not find repo root from %s", wd)
	return ""
}

// validateJSONAgainstSchema shells out to python3 and runs the
// jsonschema Draft202012Validator against the supplied JSON file.
// The validator is the same one scripts/validate_contracts.py
// uses, so test failures mirror contract-validation failures.
//
// We explicitly prepend the user site-packages directory (where
// `pip3 install --user jsonschema` lands it) onto sys.path inside
// the subprocess. The system Python on macOS often does not
// enable the user site by default when launched without a TTY
// (site.ENABLE_USER_SITE is false), so the import would otherwise
// fail even when `pip3 install --user jsonschema` succeeded.
//
// The test process may have HOME overridden (e.g. by the
// review-test helper that uses t.Setenv to isolate state). We
// pass the original HOME captured at process start to the
// subprocess so site.getusersitepackages returns the install
// location rather than a temp directory.
func validateJSONAgainstSchema(t *testing.T, schemaPath, jsonPath string) {
	t.Helper()
	pythonBin, err := exec.LookPath("python3")
	if err != nil {
		t.Skipf("python3 not on PATH: %v", err)
	}
	if originalHome == "" {
		t.Skipf("HOME was empty at process start; cannot resolve python user site-packages")
	}
	cmd := envWithRealHome(t, pythonBin, originalHome)
	cmd.Args = append(cmd.Args, "-c", schemaValidatorScript(userSiteForHome(t, pythonBin, originalHome)), schemaPath, jsonPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "__SCHEMA_SKIP__") {
			t.Skipf("jsonschema unavailable in python3 %q; install with `pip3 install --user jsonschema`:\n%s", pythonBin, string(out))
		}
		if strings.Contains(string(out), "__SCHEMA_FAIL__") {
			t.Fatalf("schema validation failed for %s against %s:\n%s", jsonPath, schemaPath, string(out))
		}
		t.Fatalf("schema validation crashed for %s against %s:\n%s", jsonPath, schemaPath, string(out))
	}
	if !strings.Contains(string(out), "__SCHEMA_OK__") {
		t.Fatalf("schema validation did not report OK for %s:\n%s", jsonPath, string(out))
	}
}

// schemaValidatorScript returns the python source that loads
// the schema + data files from argv and runs the jsonschema
// Draft202012Validator. userSite is prepended to sys.path so
// the import succeeds even when the subprocess is launched
// without user-site enabled.
func schemaValidatorScript(userSite string) string {
	return fmt.Sprintf(`import json, sys
sys.path.insert(0, %q)
try:
    from jsonschema import Draft202012Validator
except ImportError as e:
    print("__SCHEMA_SKIP__:" + str(e))
    sys.exit(2)
schema = json.load(open(sys.argv[1]))
data = json.load(open(sys.argv[2]))
errors = list(Draft202012Validator(schema).iter_errors(data))
if errors:
    lines = []
    for e in errors[:10]:
        path = "/".join(str(p) for p in e.absolute_path) or "(root)"
        lines.append(f"{path}: {e.message}")
    print("__SCHEMA_FAIL__")
    print("\n".join(lines))
    sys.exit(1)
print("__SCHEMA_OK__")
`, userSite)
}

// envWithRealHome returns an *exec.Cmd prepopulated with the
// parent process's environment plus HOME set to realHome (so the
// subprocess's site.getusersitepackages() resolves to the
// real user site rather than a test-isolated temp dir).
func envWithRealHome(t *testing.T, pythonBin, realHome string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(pythonBin)
	cmd.Env = append(os.Environ(), "HOME="+realHome)
	return cmd
}

// userSiteForHome asks python where its user site lives when
// HOME is overridden. Returned as a clean path string.
func userSiteForHome(t *testing.T, pythonBin, realHome string) string {
	t.Helper()
	cmd := exec.Command(pythonBin, "-c", "import site; print(site.getusersitepackages())")
	cmd.Env = append(os.Environ(), "HOME="+realHome)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("query python user site-packages: %v\n%s", err, string(out))
	}
	return strings.TrimSpace(string(out))
}
