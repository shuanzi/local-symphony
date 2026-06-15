package review

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"local-symphony/internal/core"
	"local-symphony/internal/store"
)

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func mustReviewPacketIDForRun(t *testing.T, st *store.Store, runID string) string {
	t.Helper()
	row, err := st.Project.QueryOne(`SELECT id FROM review_packets WHERE run_id=? ORDER BY packet_no DESC LIMIT 1`, runID)
	if err != nil {
		t.Fatalf("query review packet: %v", err)
	}
	return row["id"].String()
}

const testMaxUntrackedPatchBytes int64 = 1024 * 1024

// TestReviewArtifactMetadataMatchesPostRewrite verifies that the
// artifacts row stamped for review.json reports size and SHA256 of
// the *final* on-disk contents (i.e. after the safe_summary post-
// rewrite). The previous implementation hashed and inserted the
// review.json metadata before the post-transaction safe_summary
// rewrite, leaving the artifact row out of sync with the file the
// next operator (or the follow-on Rework injector) reads.
func TestReviewArtifactMetadataMatchesPostRewrite(t *testing.T) {
	st := newReviewTestStore(t)
	issue, run := prepareReviewRun(t, st)

	if _, err := (Generator{Store: st}).Generate(run.ID); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	reviewJSONPath := filepath.Join(st.RepoRoot, ".symphony", "artifacts", issue.Identifier, run.ID, "review.json")
	contents, err := os.ReadFile(reviewJSONPath)
	if err != nil {
		t.Fatalf("read review.json: %v", err)
	}
	wantSHA := sha256Hex(contents)
	wantSize := int64(len(contents))

	arts, err := st.ArtifactsForReview(mustReviewPacketIDForRun(t, st, run.ID))
	if err != nil {
		t.Fatalf("ArtifactsForReview: %v", err)
	}
	var reviewJSONArt *store.ArtifactRecord
	for _, a := range arts {
		if a.Kind == "review_packet" && a.Path == reviewJSONPath {
			reviewJSONArt = a
			break
		}
	}
	if reviewJSONArt == nil {
		t.Fatalf("review.json artifact row not found among %d artifacts", len(arts))
	}
	if reviewJSONArt.SHA256 == nil || *reviewJSONArt.SHA256 != wantSHA {
		t.Fatalf("review.json artifact SHA = %v, want %s (artifact metadata out of sync with on-disk file)", reviewJSONArt.SHA256, wantSHA)
	}
	if reviewJSONArt.SizeBytes != wantSize {
		t.Fatalf("review.json artifact size = %d, want %d (artifact metadata out of sync with on-disk file)", reviewJSONArt.SizeBytes, wantSize)
	}
}

// TestSafeSummaryAllowsSecretPathAsChangedFile verifies that
// legitimate file paths such as "internal/secrets/store.go" are not
// rejected by the raw-artifact refusal-kind scan. The previous
// substring-match implementation had a token like "secrets" in the
// blocklist, which collided with the path "secrets/" in the
// ChangedFiles field of the safe summary. The blocklist must
// respect token boundaries (so the substring "secrets" embedded in
// a path segment is OK as long as it is not a stand-alone marker).
// PR #27 / D4 F5.
func TestSafeSummaryAllowsSecretPathAsChangedFile(t *testing.T) {
	s := &SafeSummary{
		ReviewPacketID:   "rp_1",
		PacketNo:         1,
		RunID:            "run_1",
		SourceIssueState: string(core.StateReady),
		Status:           "generated",
		Summary:          "Refactored the credential storage helper.",
		Acceptance:       []string{"acceptance-1"},
		Tests:            []string{"go test ./..."},
		Risks:            []string{"none"},
		Verification:     []string{"manual smoke"},
		ChangedFiles:     []string{"internal/secrets/store.go"},
		HowToContinue:    "Use Send to Rework with a reason, or Mark Done with an acceptance reason.",
	}
	if err := s.Seal(); err != nil {
		t.Fatalf("Seal rejected legitimate path: %v", err)
	}
	md, err := s.ToMarkdown()
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if !strings.Contains(md, "internal/secrets/store.go") {
		t.Fatalf("markdown dropped legitimate path: %s", md)
	}
}

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

func TestGenerateUpdatesExistingPromptSnapshotContextHash(t *testing.T) {
	st := newReviewTestStore(t)
	issue, run := prepareReviewRun(t, st)
	wfID, err := st.CreateWorkflowSnapshot("valid", filepath.Join(st.RepoRoot, "WORKFLOW.md"), `{}`, "prompt-hash", "[]")
	if err != nil {
		t.Fatalf("CreateWorkflowSnapshot: %v", err)
	}
	if err := st.AttachWorkflowSnapshot(run.ID, wfID); err != nil {
		t.Fatalf("AttachWorkflowSnapshot: %v", err)
	}
	root := filepath.Join(st.RepoRoot, ".symphony", "artifacts", issue.Identifier, run.ID)
	if _, err := st.CreatePromptSnapshot(run.ID, wfID, "stale-context-hash", "rendered-hash", root); err != nil {
		t.Fatalf("CreatePromptSnapshot: %v", err)
	}

	if _, err := (Generator{Store: st}).Generate(run.ID); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	ctx := map[string]any{"issue_identifier": issue.Identifier, "run_id": run.ID}
	cb, err := json.MarshalIndent(ctx, "", "  ")
	if err != nil {
		t.Fatalf("marshal expected prompt context: %v", err)
	}
	wantHash := sha256Hex(cb)
	row, err := st.Project.QueryOne(`SELECT context_hash, redacted_prompt_path FROM prompt_snapshots WHERE run_id=?`, run.ID)
	if err != nil {
		t.Fatalf("query prompt snapshot: %v", err)
	}
	if got := row["context_hash"].String(); got != wantHash {
		t.Fatalf("context_hash = %q, want %q", got, wantHash)
	}
	if got := row["redacted_prompt_path"].String(); got != filepath.Join(root, "prompt/rendered_prompt.redacted.md") {
		t.Fatalf("redacted_prompt_path = %q, want rendered prompt path", got)
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
