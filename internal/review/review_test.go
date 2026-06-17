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

// TestGenerateOmitsUntrackedRenameOfProtectedDeletedFile verifies the
// FAIL-CLOSED behavior restored by D4 / R16 round 3: a filesystem rename
// of a protected file to an untracked name (safe.txt) DELETES the
// tracked .env, which is a protected delete. Because the protected
// file's pre-deletion worktree content (the bytes that could have been
// copied into an untracked file) is unrecoverable when unstaged
// modifications existed and undetectable after deletion, ALL
// non-path-protected untracked content is suppressed (denied) — both the
// renamed safe.txt (verbatim copy of the deleted secret) AND the
// unrelated notes.txt (different content). Protected bytes never appear
// in any artifact. The round-2 content-hash-match premise (notes.txt
// preserved) is no longer valid under the security-first decision.
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
	// Fail-closed: protected bytes AND ALL untracked content (both the
	// renamed safe.txt and the unrelated notes.txt) must be suppressed
	// from changes.patch.
	for _, marker := range []string{"SECRET=original", "safe.txt", ".env", "diff --git a/notes.txt b/notes.txt", "ordinary untracked"} {
		if strings.Contains(patch, marker) {
			t.Fatalf("changes.patch leaked content in fail-closed (protected-delete) mode (%q):\n%s", marker, patch)
		}
	}
	changed := readReviewArtifact(t, st, issue, run, "changed-files.txt")
	for _, marker := range []string{"safe.txt", ".env", "notes.txt"} {
		if strings.Contains(changed, marker) {
			t.Fatalf("changed-files.txt leaked content in fail-closed mode (%q):\n%s", marker, changed)
		}
	}
	diffstat := readReviewArtifact(t, st, issue, run, "diffstat.txt")
	for _, marker := range []string{"safe.txt", ".env", "notes.txt"} {
		if strings.Contains(diffstat, marker) {
			t.Fatalf("diffstat.txt leaked content in fail-closed mode (%q):\n%s", marker, diffstat)
		}
	}
	reviewJSON := readReviewArtifact(t, st, issue, run, "review.json")
	if strings.Contains(reviewJSON, "SECRET=original") {
		t.Fatalf("review.json leaked protected filesystem rename bytes:\n%s", reviewJSON)
	}
	if strings.Contains(reviewJSON, `"notes.txt"`) || strings.Contains(reviewJSON, `"safe.txt"`) {
		t.Fatalf("review.json leaked untracked content in fail-closed mode:\n%s", reviewJSON)
	}
	untracked := readUntrackedArtifact(t, st, issue, run)
	for _, path := range []string{"safe.txt", "notes.txt"} {
		info, ok := untracked[path]
		if !ok {
			continue
		}
		if info.PatchIncluded || info.SHA256 != "" {
			t.Fatalf("%s untracked info = %+v in fail-closed mode, want no patch and no sha", path, info)
		}
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

// TestGenerateOmitsUntrackedContentWhenProtectedTrackedFileDeleted
// verifies the FAIL-CLOSED behavior of D4 / R16 round 3: after deleting
// a protected tracked .env, ALL non-path-protected untracked content is
// suppressed (denied) from the review packet — both the verbatim copy
// (public.txt) AND the unrelated safe file (notes.txt). Because the
// protected file's pre-deletion worktree content (the bytes that could
// have been copied into an untracked file) is unrecoverable when unstaged
// modifications existed and undetectable after deletion, content-hash
// matching cannot be made safe; we fail closed. Protected bytes never
// appear in any artifact. The round-2 premise (notes.txt preserved) is
// no longer valid under the security-first decision.
func TestGenerateOmitsUntrackedContentWhenProtectedTrackedFileDeleted(t *testing.T) {
	st := newReviewTestStore(t)
	workspace := initGitWorkspace(t)
	secret := "SECRET=protected\n"
	writeFile(t, workspace, ".env", secret)
	writeFile(t, workspace, "app.txt", "base\n")
	runGit(t, workspace, "add", ".")
	runGit(t, workspace, "commit", "-m", "base")

	// UNSTAGED deletion (protected delete) → fail closed.
	if err := os.Remove(filepath.Join(workspace, ".env")); err != nil {
		t.Fatalf("rm .env: %v", err)
	}
	writeFile(t, workspace, "public.txt", secret)
	writeFile(t, workspace, "notes.txt", "benign note\n")
	issue, run := prepareReviewRunWithWorkspace(t, st, workspace, []string{"public.txt", "notes.txt"})

	_, err := (Generator{Store: st}).Generate(run.ID)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	patch := readReviewArtifact(t, st, issue, run, "changes.patch")
	// Fail-closed: protected bytes AND ALL untracked content (both the
	// verbatim-copy public.txt and the unrelated notes.txt) must be
	// suppressed from changes.patch.
	for _, marker := range []string{"SECRET=protected", "diff --git a/public.txt b/public.txt", "diff --git a/notes.txt b/notes.txt", "benign note"} {
		if strings.Contains(patch, marker) {
			t.Fatalf("changes.patch leaked content in fail-closed (protected-delete) mode (%q):\n%s", marker, patch)
		}
	}
	changed := readReviewArtifact(t, st, issue, run, "changed-files.txt")
	for _, marker := range []string{"public.txt", "notes.txt"} {
		if strings.Contains(changed, marker) {
			t.Fatalf("changed-files.txt leaked untracked content in fail-closed mode (%q):\n%s", marker, changed)
		}
	}
	diffstat := readReviewArtifact(t, st, issue, run, "diffstat.txt")
	for _, marker := range []string{"public.txt", "notes.txt"} {
		if strings.Contains(diffstat, marker) {
			t.Fatalf("diffstat.txt leaked untracked content in fail-closed mode (%q):\n%s", marker, diffstat)
		}
	}
	untracked := readUntrackedArtifact(t, st, issue, run)
	for _, path := range []string{"public.txt", "notes.txt"} {
		info, ok := untracked[path]
		if !ok {
			continue
		}
		if info.PatchIncluded || info.SHA256 != "" {
			t.Fatalf("%s untracked info = %+v in fail-closed mode, want no patch and no sha", path, info)
		}
	}
	// review.json must not leak protected bytes or untracked content.
	reviewJSON := readReviewArtifact(t, st, issue, run, "review.json")
	if strings.Contains(reviewJSON, "SECRET=protected") {
		t.Fatalf("review.json leaked protected bytes:\n%s", reviewJSON)
	}
	if strings.Contains(reviewJSON, `"notes.txt"`) || strings.Contains(reviewJSON, `"public.txt"`) {
		t.Fatalf("review.json leaked untracked content in fail-closed mode:\n%s", reviewJSON)
	}
}

// TestGenerateKeepsUntrackedContentWhenNoProtectedDelete proves the COMMON
// case keeps full content-level correlation in the review packet: with NO
// protected file deleted, an untracked notes.txt appears in
// changes.patch / changed-files.txt / diffstat.txt / untracked-files.json
// with its content and sha. This is the counterpart to the fail-closed
// tests above and guards against regressing common-case behavior when
// tightening the protected-delete path. (The round-2 "keeps unrelated
// untracked when protected deleted" premise is no longer valid under the
// security-first fail-closed decision; that scenario is now covered by
// TestGenerateOmitsUntrackedContentWhenProtectedTrackedFileDeleted.)
func TestGenerateKeepsUntrackedContentWhenNoProtectedDelete(t *testing.T) {
	st := newReviewTestStore(t)
	workspace := initGitWorkspace(t)
	writeFile(t, workspace, "app.txt", "base\n")
	runGit(t, workspace, "add", ".")
	runGit(t, workspace, "commit", "-m", "base")

	// NO protected file is deleted (no .env at all) → no fail-closed →
	// untracked content is preserved.
	writeFile(t, workspace, "notes.txt", "benign note\n")
	issue, run := prepareReviewRunWithWorkspace(t, st, workspace, []string{"notes.txt"})

	_, err := (Generator{Store: st}).Generate(run.ID)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	patch := readReviewArtifact(t, st, issue, run, "changes.patch")
	if !strings.Contains(patch, "diff --git a/notes.txt b/notes.txt") || !strings.Contains(patch, "+benign note") {
		t.Fatalf("changes.patch dropped untracked notes.txt (no protected delete):\n%s", patch)
	}
	changed := readReviewArtifact(t, st, issue, run, "changed-files.txt")
	if !strings.Contains(changed, "notes.txt\n") {
		t.Fatalf("changed-files.txt dropped untracked notes.txt (no protected delete):\n%s", changed)
	}
	diffstat := readReviewArtifact(t, st, issue, run, "diffstat.txt")
	if !strings.Contains(diffstat, "notes.txt") {
		t.Fatalf("diffstat.txt dropped untracked notes.txt (no protected delete):\n%s", diffstat)
	}
	untracked := readUntrackedArtifact(t, st, issue, run)
	info, ok := untracked["notes.txt"]
	if !ok {
		t.Fatalf("untracked-files.json dropped notes.txt (no protected delete): %+v", untracked)
	}
	if !info.PatchIncluded || info.SHA256 == "" {
		t.Fatalf("notes.txt untracked info = %+v, want patch included and sha", info)
	}
}

// TestGenerateFailsClosedOnStagedProtectedDelete codifies the FAIL-CLOSED
// behavior in review.go for a STAGED protected deletion. Round 3
// generalized fail-closed to ANY protected delete (staged OR unstaged)
// because the pre-deletion worktree content is unrecoverable when
// unstaged modifications existed and undetectable after deletion; the
// unstaged variant is covered by
// TestGenerateOmitsUntrackedContentWhenProtectedTrackedFileDeleted. Here,
// with a staged `git rm`, matchesUntracked returns true for EVERY
// non-path-protected untracked file, so ALL untracked content — both the
// benign notes.txt and the verbatim-copy public.txt — is suppressed (no
// patch, no sha), and the protected "SECRET=..." bytes never appear.
func TestGenerateFailsClosedOnStagedProtectedDelete(t *testing.T) {
	st := newReviewTestStore(t)
	workspace := initGitWorkspace(t)
	secret := "SECRET=protected\n"
	writeFile(t, workspace, ".env", secret)
	writeFile(t, workspace, "app.txt", "base\n")
	runGit(t, workspace, "add", ".")
	runGit(t, workspace, "commit", "-m", "base")

	// STAGED deletion: `git rm` removes the index entry → protected
	// content set is unknown → fail closed (suppress all untracked).
	runGit(t, workspace, "rm", ".env")
	writeFile(t, workspace, "public.txt", secret)
	writeFile(t, workspace, "notes.txt", "benign note\n")
	issue, run := prepareReviewRunWithWorkspace(t, st, workspace, []string{"public.txt", "notes.txt"})

	_, err := (Generator{Store: st}).Generate(run.ID)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	patch := readReviewArtifact(t, st, issue, run, "changes.patch")
	// In fail-closed mode ALL untracked content is suppressed: neither
	// the benign notes.txt nor the verbatim-copy public.txt appears.
	for _, marker := range []string{"SECRET=protected", "benign note", "diff --git a/public.txt b/public.txt", "diff --git a/notes.txt b/notes.txt"} {
		if strings.Contains(patch, marker) {
			t.Fatalf("changes.patch leaked untracked content in fail-closed (staged-delete) mode (%q):\n%s", marker, patch)
		}
	}
	untracked := readUntrackedArtifact(t, st, issue, run)
	for _, path := range []string{"public.txt", "notes.txt"} {
		info, ok := untracked[path]
		if !ok {
			continue
		}
		if info.PatchIncluded || info.SHA256 != "" {
			t.Fatalf("%s untracked info = %+v in fail-closed mode, want no patch and no sha", path, info)
		}
	}
	// Protected bytes must never appear anywhere in the packet.
	for _, name := range []string{"changes.patch", "changed-files.txt", "diffstat.txt", "untracked-files.json", "review.json"} {
		art := readReviewArtifact(t, st, issue, run, name)
		if strings.Contains(art, "SECRET=protected") {
			t.Fatalf("%s leaked protected bytes in fail-closed mode:\n%s", name, art)
		}
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

// TestGenerateFailsClosedOnProtectedRename codifies the round-4 FAIL-CLOSED
// behavior in review.go for a STAGED protected RENAME (`git mv .env
// renamed.txt`, an `R` porcelain record, NOT a `D` record). Round 3's
// delete-only check did not see the `R` record as a deletion, so an
// untracked file holding the copied protected bytes (with filler to avoid
// git's rename/copy detection) was preserved → leak. Round 4 makes a
// protected R/C source trigger fail-closed too: matchesUntracked returns
// true for EVERY non-path-protected untracked file, so ALL untracked
// content — both the benign notes.txt and the copy-of-protected
// public.txt — is suppressed (no patch, no sha), and the protected
// "SECRET=..." bytes never appear anywhere in the packet.
func TestGenerateFailsClosedOnProtectedRename(t *testing.T) {
	st := newReviewTestStore(t)
	workspace := initGitWorkspace(t)
	writeFile(t, workspace, ".env", "SECRET=original\n")
	writeFile(t, workspace, "app.txt", "base\n")
	runGit(t, workspace, "add", ".")
	runGit(t, workspace, "commit", "-m", "base")

	// Staged RENAME of the protected file → R porcelain record (NOT D).
	runGit(t, workspace, "mv", ".env", "renamed.txt")
	// public.txt is an untracked copy of the protected bytes with filler
	// (avoids git's copy detection); notes.txt is benign. Under round 3
	// the R record did not trigger fail-closed, so public.txt leaked;
	// round 4 suppresses both.
	var pb strings.Builder
	for i := 0; i < 20; i++ {
		pb.WriteString("filler line 0123456789\n")
	}
	pb.WriteString("SECRET=original\n")
	writeFile(t, workspace, "public.txt", pb.String())
	writeFile(t, workspace, "notes.txt", "benign note\n")
	issue, run := prepareReviewRunWithWorkspace(t, st, workspace, []string{"public.txt", "notes.txt"})

	_, err := (Generator{Store: st}).Generate(run.ID)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	patch := readReviewArtifact(t, st, issue, run, "changes.patch")
	// In fail-closed mode ALL untracked content is suppressed: neither
	// the benign notes.txt nor the copy-of-protected public.txt appears,
	// and the rename itself does not leak SECRET.
	for _, marker := range []string{
		"SECRET=original",
		"benign note",
		"diff --git a/public.txt b/public.txt",
		"diff --git a/notes.txt b/notes.txt",
		"diff --git a/.env b/renamed.txt",
	} {
		if strings.Contains(patch, marker) {
			t.Fatalf("changes.patch leaked content in fail-closed (protected-rename) mode (%q):\n%s", marker, patch)
		}
	}
	changed := readReviewArtifact(t, st, issue, run, "changed-files.txt")
	for _, marker := range []string{"public.txt", "notes.txt"} {
		if strings.Contains(changed, marker) {
			t.Fatalf("changed-files.txt leaked untracked content in fail-closed (protected-rename) mode (%q):\n%s", marker, changed)
		}
	}
	diffstat := readReviewArtifact(t, st, issue, run, "diffstat.txt")
	for _, marker := range []string{"public.txt", "notes.txt"} {
		if strings.Contains(diffstat, marker) {
			t.Fatalf("diffstat.txt leaked untracked content in fail-closed (protected-rename) mode (%q):\n%s", marker, diffstat)
		}
	}
	untracked := readUntrackedArtifact(t, st, issue, run)
	for _, path := range []string{"public.txt", "notes.txt"} {
		info, ok := untracked[path]
		if !ok {
			continue
		}
		if info.PatchIncluded || info.SHA256 != "" {
			t.Fatalf("%s untracked info = %+v in fail-closed (protected-rename) mode, want no patch and no sha", path, info)
		}
	}
	// Protected bytes must never appear anywhere in the packet.
	for _, name := range []string{"changes.patch", "changed-files.txt", "diffstat.txt", "untracked-files.json", "review.json"} {
		art := readReviewArtifact(t, st, issue, run, name)
		if strings.Contains(art, "SECRET=original") {
			t.Fatalf("%s leaked protected bytes in fail-closed (protected-rename) mode:\n%s", name, art)
		}
	}
}

// TestGenerateFailsClosedOnProtectedDeleteStagesAddedCopy codifies the
// D4 / R16 round-5 fix for P1 leak #1 in review.go's collectChanges path.
//
// Scenario: a protected tracked .env is deleted (staged `git rm`) and its
// bytes are copied into a NEW public file that is STAGED as an addition
// (`cp .env public.txt; git add public.txt`). `git status --porcelain`
// reports this as `D .env` + `A public.txt` (porcelain does NOT detect
// that public.txt is a copy/rename of .env). Round 4's fail-closed
// (protectedDeletedContent.unknown=true from the protected `D .env`)
// suppressed only UNTRACKED (??) files; the tracked `A public.txt` still
// entered `changed`, so gitx.DiffBinaryPaths emitted `SECRET=...` into
// changes.patch / diffstat.txt / review.json.
//
// Round 5 makes the fail-closed SKIP ALL tracked A records when
// unknown=true (mirroring the orchestrator's
// filteredTrackedDiffPathspecs), so the staged `A public.txt` is denied
// and its protected bytes never appear in any artifact. An unrelated
// benign untracked file (notes.txt) is also suppressed by the existing
// untracked fail-closed.
func TestGenerateFailsClosedOnProtectedDeleteStagesAddedCopy(t *testing.T) {
	st := newReviewTestStore(t)
	workspace := initGitWorkspace(t)
	secret := "SECRET=original\n"
	writeFile(t, workspace, ".env", secret)
	writeFile(t, workspace, "app.txt", "base\n")
	runGit(t, workspace, "add", ".")
	runGit(t, workspace, "commit", "-m", "base")

	// STAGED delete of the protected file (D .env) + STAGED add of a
	// public file holding the copied protected bytes (A public.txt).
	// Porcelain reports these as two independent records; the A record
	// is the leak vector closed by round 5.
	runGit(t, workspace, "rm", ".env")
	writeFile(t, workspace, "public.txt", secret)
	runGit(t, workspace, "add", "public.txt")
	// notes.txt is a benign untracked file (suppressed by the existing
	// untracked fail-closed).
	writeFile(t, workspace, "notes.txt", "benign note\n")
	issue, run := prepareReviewRunWithWorkspace(t, st, workspace, []string{"public.txt", "notes.txt"})

	_, err := (Generator{Store: st}).Generate(run.ID)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// The staged A public.txt MUST be suppressed: no patch, no
	// changed-files entry, no diffstat entry — and no protected bytes
	// anywhere in the packet.
	for _, name := range []string{"changes.patch", "changed-files.txt", "diffstat.txt", "untracked-files.json", "review.json"} {
		art := readReviewArtifact(t, st, issue, run, name)
		for _, marker := range []string{
			"SECRET=original",
			"diff --git a/public.txt b/public.txt",
			"public.txt",
			"benign note",
			"diff --git a/notes.txt b/notes.txt",
		} {
			if strings.Contains(art, marker) {
				t.Fatalf("%s leaked content in fail-closed (protected-delete + staged-added-copy) mode (%q):\n%s", name, marker, art)
			}
		}
	}
	untracked := readUntrackedArtifact(t, st, issue, run)
	for _, path := range []string{"public.txt", "notes.txt"} {
		info, ok := untracked[path]
		if !ok {
			continue
		}
		if info.PatchIncluded || info.SHA256 != "" {
			t.Fatalf("%s untracked info = %+v in fail-closed mode, want no patch and no sha", path, info)
		}
	}
}

// TestGenerateSkipsAddedCopyOfRemainingProtectedFile codifies the D4 / R16
// round-5 fix for P1 leak #2 in review.go's collectChanges path.
//
// Scenario: a protected tracked .env is copied verbatim into a NEW public
// file that is STAGED as an addition, while the source .env REMAINS
// (`cp .env public.txt; git add public.txt`, no delete). `git status
// --porcelain` reports ONLY `A public.txt` (porcelain does NOT detect
// copies, and there is no D/R record), so protectedDeletedContent.unknown
// stays false and round 4's fail-closed does not trigger. The A record
// passed the per-record reviewSafePath check (public.txt is not itself a
// protected path) and entered `changed`, so gitx.DiffBinaryPaths emitted
// the copied `SECRET=...` bytes into changes.patch / review.json.
//
// Round 5 adds a copy-aware pre-check via `git diff --name-status
// --find-copies-harder`, which reports `C100 .env public.txt` (source
// first, destination second) for a verbatim copy even when the source
// remains. collectChanges now skips A destinations whose R/C source is
// protected, so public.txt is denied and its bytes never appear. An
// unrelated benign tracked-added file (feature.txt) is KEPT to prove the
// common-case correlation is preserved.
func TestGenerateSkipsAddedCopyOfRemainingProtectedFile(t *testing.T) {
	st := newReviewTestStore(t)
	workspace := initGitWorkspace(t)
	secret := "SECRET=original\n"
	writeFile(t, workspace, ".env", secret)
	writeFile(t, workspace, "app.txt", "base\n")
	runGit(t, workspace, "add", ".")
	runGit(t, workspace, "commit", "-m", "base")

	// Verbatim copy of the protected file into a new public file, source
	// .env REMAINS. Staged add. Porcelain reports only `A public.txt`;
	// --find-copies-harder reports `C100 .env public.txt`.
	writeFile(t, workspace, "public.txt", secret)
	runGit(t, workspace, "add", "public.txt")
	// feature.txt is an unrelated benign tracked-added file (NOT a copy
	// of any protected file). It MUST be kept in changed/changes.patch
	// to prove common-case correlation survives the copy-aware skip.
	writeFile(t, workspace, "feature.txt", "new feature\n")
	runGit(t, workspace, "add", "feature.txt")
	issue, run := prepareReviewRunWithWorkspace(t, st, workspace, []string{"public.txt", "feature.txt"})

	// Sanity: confirm git would detect this copy via --find-copies-harder
	// (guards the test against a git version that does not).
	out, err := exec.Command("git", "-C", workspace, "diff", "--name-status", "--find-renames", "--find-copies", "--find-copies-harder", "-z", "HEAD").Output()
	if err != nil {
		t.Fatalf("find-copies-harder probe failed: %v", err)
	}
	if !strings.Contains(string(out), "C100\x00.env\x00public.txt") {
		t.Fatalf("git did not detect the verbatim copy as C100 .env public.txt; test premise invalid:\n%q", string(out))
	}

	_, err = (Generator{Store: st}).Generate(run.ID)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// The copy destination public.txt MUST be suppressed everywhere;
	// protected bytes never appear. The unrelated feature.txt MUST be
	// kept (common-case correlation preserved).
	for _, name := range []string{"changes.patch", "changed-files.txt", "diffstat.txt", "untracked-files.json", "review.json"} {
		art := readReviewArtifact(t, st, issue, run, name)
		for _, marker := range []string{
			"SECRET=original",
			"diff --git a/public.txt b/public.txt",
			"public.txt",
		} {
			if strings.Contains(art, marker) {
				t.Fatalf("%s leaked protected-copy content (copy with source remaining) (%q):\n%s", name, marker, art)
			}
		}
	}
	patch := readReviewArtifact(t, st, issue, run, "changes.patch")
	if !strings.Contains(patch, "diff --git a/feature.txt b/feature.txt") || !strings.Contains(patch, "+new feature") {
		t.Fatalf("changes.patch dropped unrelated tracked-added feature.txt (common-case correlation broken):\n%s", patch)
	}
	changed := readReviewArtifact(t, st, issue, run, "changed-files.txt")
	if !strings.Contains(changed, "feature.txt\n") {
		t.Fatalf("changed-files.txt dropped unrelated tracked-added feature.txt (common-case correlation broken):\n%s", changed)
	}
	if strings.Contains(changed, "public.txt") {
		t.Fatalf("changed-files.txt kept protected-copy destination public.txt:\n%s", changed)
	}
}

// TestGenerateSkipsAddedCopyOfModifiedProtectedFile codifies the D4 / R16
// round-6 fix for the modified-source-copy P1 leak in review.go's
// collectChanges path (the A-record variant).
//
// Scenario: a protected tracked .env is committed (SECRET=old), MODIFIED to
// SECRET=new, then copied verbatim into a new public file (`cp .env
// public.txt`, `git add public.txt`), source .env REMAINS. `git diff
// --name-status --find-copies-harder HEAD` reports `M .env` + `A public.txt`
// — NOT a `C` record — because --find-copies-harder compares the copy
// against the unmodified HEAD blob, but the copy holds the modified
// (workspace) bytes. Round 5's protectedCopyDestinations (--find-copies-
// harder) check therefore does NOT flag public.txt, so the A record entered
// `changed` and gitx.DiffBinaryPaths emitted the copied modified protected
// bytes (SECRET=new) into changes.patch / review.json.
//
// Round 6 closes this: matchesAddedTracked content-hash-matches public.txt's
// WORKSPACE content against existingProtectedContentHashes (workspace +
// HEAD + index of all existing protected files); public.txt's content
// (SECRET=new) matches .env's WORKSPACE hash (SECRET=new) → denied. An
// unrelated safe added feature.txt IS kept.
func TestGenerateSkipsAddedCopyOfModifiedProtectedFile(t *testing.T) {
	st := newReviewTestStore(t)
	workspace := initGitWorkspace(t)
	writeFile(t, workspace, ".env", "SECRET=old\n")
	writeFile(t, workspace, "app.txt", "base\n")
	runGit(t, workspace, "add", ".")
	runGit(t, workspace, "commit", "-m", "base")

	// MODIFY the protected file (the bytes that will be copied).
	writeFile(t, workspace, ".env", "SECRET=new\n")
	// Verbatim copy of the MODIFIED protected workspace bytes into a new
	// public file. Source .env REMAINS.
	modified, err := os.ReadFile(filepath.Join(workspace, ".env"))
	if err != nil {
		t.Fatalf("read modified .env: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "public.txt"), modified, 0o644); err != nil {
		t.Fatalf("write public.txt: %v", err)
	}
	runGit(t, workspace, "add", "public.txt")
	// Unrelated safe added file — MUST be kept.
	writeFile(t, workspace, "feature.txt", "new feature\n")
	runGit(t, workspace, "add", "feature.txt")
	issue, run := prepareReviewRunWithWorkspace(t, st, workspace, []string{"public.txt", "feature.txt"})

	// Sanity: confirm git does NOT detect this as a C record (guards the
	// test premise — the round-5 --find-copies-harder check must NOT fire).
	probe, err := exec.Command("git", "-C", workspace, "diff", "--name-status", "--find-renames", "--find-copies", "--find-copies-harder", "-z", "HEAD").Output()
	if err != nil {
		t.Fatalf("name-status probe: %v", err)
	}
	if strings.Contains(string(probe), "C") {
		t.Fatalf("git unexpectedly detected a C record for modified-source copy; test premise invalid:\n%q", string(probe))
	}

	if _, err := (Generator{Store: st}).Generate(run.ID); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// public.txt (copy of MODIFIED protected content) MUST be suppressed
	// everywhere; the modified protected bytes never appear. The unrelated
	// feature.txt MUST be kept (common-case correlation preserved).
	for _, name := range []string{"changes.patch", "changed-files.txt", "diffstat.txt", "untracked-files.json", "review.json"} {
		art := readReviewArtifact(t, st, issue, run, name)
		for _, marker := range []string{
			"SECRET=new",
			"diff --git a/public.txt b/public.txt",
			"public.txt",
		} {
			if strings.Contains(art, marker) {
				t.Fatalf("%s leaked modified-protected-copy content (%q):\n%s", name, marker, art)
			}
		}
	}
	patch := readReviewArtifact(t, st, issue, run, "changes.patch")
	if !strings.Contains(patch, "diff --git a/feature.txt b/feature.txt") || !strings.Contains(patch, "+new feature") {
		t.Fatalf("changes.patch dropped unrelated tracked-added feature.txt (common-case correlation broken):\n%s", patch)
	}
	changed := readReviewArtifact(t, st, issue, run, "changed-files.txt")
	if !strings.Contains(changed, "feature.txt") {
		t.Fatalf("changed-files.txt dropped unrelated tracked-added feature.txt (common-case correlation broken):\n%s", changed)
	}
}

// TestGenerateSuppressesUntrackedCopyOfExistingProtectedContent codifies the
// D4 / R16 round-6 fix for the untracked modified-source-copy P1 leak in
// review.go's collectChanges path (the untracked ?? variant).
//
// Scenario: a protected tracked .env is committed (SECRET=old), MODIFIED to
// SECRET=new, and an untracked safe.txt is a verbatim copy of the modified
// .env (safe.txt = SECRET=new). Source .env REMAINS. Round 4/5 preserved
// safe.txt with its content (PatchIncluded=true, sha=...) since
// matchesUntracked returned false (no protected delete), so the modified
// protected bytes entered changes.patch (via syntheticPatch) /
// untracked-files.json (sha). Round 6 content-hash-matches safe.txt's
// content against existingProtectedContentHashes (includes .env's workspace
// hash = SECRET=new) → matchesUntracked returns true → safe.txt is denied
// (no patch, no sha, no listing). An unrelated untracked notes.txt IS
// present with its content.
func TestGenerateSuppressesUntrackedCopyOfExistingProtectedContent(t *testing.T) {
	st := newReviewTestStore(t)
	workspace := initGitWorkspace(t)
	writeFile(t, workspace, ".env", "SECRET=old\n")
	writeFile(t, workspace, "app.txt", "base\n")
	runGit(t, workspace, "add", ".")
	runGit(t, workspace, "commit", "-m", "base")

	// MODIFY the protected file.
	writeFile(t, workspace, ".env", "SECRET=new\n")
	// safe.txt is a verbatim copy of the modified protected workspace bytes
	// (untracked). Source .env REMAINS.
	modified, err := os.ReadFile(filepath.Join(workspace, ".env"))
	if err != nil {
		t.Fatalf("read modified .env: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "safe.txt"), modified, 0o644); err != nil {
		t.Fatalf("write safe.txt (copy of modified .env): %v", err)
	}
	// Unrelated untracked notes.txt — MUST be present with its content.
	writeFile(t, workspace, "notes.txt", "plain note\n")
	issue, run := prepareReviewRunWithWorkspace(t, st, workspace, []string{"safe.txt", "notes.txt"})

	if _, err := (Generator{Store: st}).Generate(run.ID); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// safe.txt (untracked copy of MODIFIED protected content) MUST NOT
	// appear in the patch or with a sha; the modified protected bytes
	// never appear anywhere. notes.txt MUST be present with its content.
	for _, name := range []string{"changes.patch", "changed-files.txt", "diffstat.txt", "untracked-files.json", "review.json"} {
		art := readReviewArtifact(t, st, issue, run, name)
		if strings.Contains(art, "SECRET=new") {
			t.Fatalf("%s leaked untracked modified-protected-copy content (SECRET=new):\n%s", name, art)
		}
		if strings.Contains(art, "diff --git a/safe.txt b/safe.txt") {
			t.Fatalf("%s leaked safe.txt synthetic patch (copy of modified protected content):\n%s", name, art)
		}
	}
	// safe.txt must either be absent from the untracked artifact or present
	// WITHOUT content (no patch, no sha). notes.txt must be present WITH
	// content (patch included, sha set).
	untracked := readUntrackedArtifact(t, st, issue, run)
	if info, ok := untracked["safe.txt"]; ok {
		if info.PatchIncluded || info.SHA256 != "" {
			t.Fatalf("safe.txt untracked info = %+v; want no patch and no sha (copy of modified protected content suppressed)", info)
		}
	}
	notesInfo, ok := untracked["notes.txt"]
	if !ok {
		t.Fatalf("untracked artifact dropped unrelated notes.txt (common-case correlation broken): %+v", untracked)
	}
	if !notesInfo.PatchIncluded || notesInfo.SHA256 == "" {
		t.Fatalf("notes.txt untracked info = %+v; want patch included and sha set (unrelated safe content preserved)", notesInfo)
	}
	patch := readReviewArtifact(t, st, issue, run, "changes.patch")
	if !strings.Contains(patch, "diff --git a/notes.txt b/notes.txt") || !strings.Contains(patch, "+plain note") {
		t.Fatalf("changes.patch dropped unrelated untracked notes.txt content (common-case correlation broken):\n%s", patch)
	}
}

// TestGenerateSkipsAddedCopyOfIgnoredProtectedFile codifies the D4 / R16
// round-7 fix A for review.go's collectChanges path (A-record variant of the
// ignored-protected-file leak).
//
// Scenario: a protected .env is IGNORED by .gitignore (the common case — .env
// is typically gitignored), so it is UNTRACKED and never appears in `git
// ls-files` or `git ls-files --others --exclude-standard`. It is then copied
// verbatim into a new public file (`cp .env public.txt`, `git add public.txt`),
// while the source .env REMAINS (ignored). Round 6's existingProtectedContent
// Hashes enumerated only tracked + untracked-non-ignored files, so the ignored
// .env was never hashed into the set → public.txt was treated as safe and its
// protected bytes leaked into changes.patch / review.json.
//
// Round 7 fix A adds a THIRD enumeration: `git ls-files --others --ignored
// --exclude-standard -z`, which lists ignored files. The ignored .env is now
// enumerated, its WORKSPACE content is hashed into existingHashes, and
// public.txt's content matches → matchesAddedTracked denies it. An unrelated
// safe added feature.txt IS kept.
func TestGenerateSkipsAddedCopyOfIgnoredProtectedFile(t *testing.T) {
	st := newReviewTestStore(t)
	workspace := initGitWorkspace(t)
	writeFile(t, workspace, "app.txt", "base\n")
	runGit(t, workspace, "add", ".")
	runGit(t, workspace, "commit", "-m", "base")
	// Make .env gitignored (the common ignored-secret setup).
	writeFile(t, workspace, ".gitignore", ".env\n")
	runGit(t, workspace, "add", ".gitignore")
	runGit(t, workspace, "commit", "-m", "add gitignore")
	// Protected .env (ignored, untracked). Real content so its workspace hash
	// is in existingHashes.
	writeFile(t, workspace, ".env", "SECRET=x\n")

	// Sanity: confirm .env is IGNORED (guards the test premise). The non-
	// ignored enumeration must NOT list it; the ignored enumeration MUST.
	nonIgnored, err := exec.Command("git", "-C", workspace, "ls-files", "--others", "--exclude-standard", "-z").Output()
	if err != nil {
		t.Fatalf("ls-files --others --exclude-standard probe: %v", err)
	}
	if strings.Contains(string(nonIgnored), ".env") {
		t.Fatalf("test premise invalid: .env not actually ignored by --others --exclude-standard:\n%q", string(nonIgnored))
	}
	ignored, err := exec.Command("git", "-C", workspace, "ls-files", "--others", "--ignored", "--exclude-standard", "-z").Output()
	if err != nil {
		t.Fatalf("ls-files --ignored probe: %v", err)
	}
	if !strings.Contains(string(ignored), ".env") {
		t.Fatalf("test premise invalid: .env not listed by --others --ignored --exclude-standard:\n%q", string(ignored))
	}

	// Verbatim copy of the ignored protected .env into a new public file.
	writeFile(t, workspace, "public.txt", "SECRET=x\n")
	runGit(t, workspace, "add", "public.txt")
	// Unrelated safe added file — MUST be kept.
	writeFile(t, workspace, "feature.txt", "new feature\n")
	runGit(t, workspace, "add", "feature.txt")
	issue, run := prepareReviewRunWithWorkspace(t, st, workspace, []string{"public.txt", "feature.txt"})

	if _, err := (Generator{Store: st}).Generate(run.ID); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// public.txt (copy of IGNORED protected content) MUST be suppressed
	// everywhere; the protected bytes never appear. The unrelated feature.txt
	// MUST be kept (common-case correlation preserved).
	for _, name := range []string{"changes.patch", "changed-files.txt", "diffstat.txt", "untracked-files.json", "review.json"} {
		art := readReviewArtifact(t, st, issue, run, name)
		for _, marker := range []string{
			"SECRET=x",
			"diff --git a/public.txt b/public.txt",
			"public.txt",
		} {
			if strings.Contains(art, marker) {
				t.Fatalf("%s leaked ignored-protected-copy content (%q):\n%s", name, marker, art)
			}
		}
	}
	patch := readReviewArtifact(t, st, issue, run, "changes.patch")
	if !strings.Contains(patch, "diff --git a/feature.txt b/feature.txt") || !strings.Contains(patch, "+new feature") {
		t.Fatalf("changes.patch dropped unrelated tracked-added feature.txt (common-case correlation broken):\n%s", patch)
	}
	changed := readReviewArtifact(t, st, issue, run, "changed-files.txt")
	if !strings.Contains(changed, "feature.txt") {
		t.Fatalf("changed-files.txt dropped unrelated tracked-added feature.txt (common-case correlation broken):\n%s", changed)
	}
}

// TestGenerateSuppressesUntrackedCopyOfIgnoredProtectedFile codifies the D4
// / R16 round-7 fix A for review.go's collectChanges path (the untracked ??
// variant of the ignored-protected-file leak).
//
// Scenario: a protected .env is IGNORED by .gitignore (untracked + ignored),
// then copied verbatim into an untracked safe.txt, while .env REMAINS. Round
// 6 did not enumerate ignored files, so the ignored .env's content was not in
// existingHashes and safe.txt (a copy of the protected content) was preserved
// with its content (patch + sha), leaking the protected bytes. Round 7 fix A
// enumerates ignored files so .env's workspace hash is in existingHashes →
// matchesUntracked returns true → safe.txt denied. An unrelated untracked
// notes.txt IS present with its content.
func TestGenerateSuppressesUntrackedCopyOfIgnoredProtectedFile(t *testing.T) {
	st := newReviewTestStore(t)
	workspace := initGitWorkspace(t)
	writeFile(t, workspace, "app.txt", "base\n")
	runGit(t, workspace, "add", ".")
	runGit(t, workspace, "commit", "-m", "base")
	writeFile(t, workspace, ".gitignore", ".env\n")
	runGit(t, workspace, "add", ".gitignore")
	runGit(t, workspace, "commit", "-m", "add gitignore")
	// Protected .env ignored, untracked, with real content.
	writeFile(t, workspace, ".env", "SECRET=x\n")

	// Sanity: confirm .env is ignored.
	nonIgnored, err := exec.Command("git", "-C", workspace, "ls-files", "--others", "--exclude-standard", "-z").Output()
	if err != nil {
		t.Fatalf("ls-files --others --exclude-standard probe: %v", err)
	}
	if strings.Contains(string(nonIgnored), ".env") {
		t.Fatalf("test premise invalid: .env not actually ignored:\n%q", string(nonIgnored))
	}
	ignored, err := exec.Command("git", "-C", workspace, "ls-files", "--others", "--ignored", "--exclude-standard", "-z").Output()
	if err != nil {
		t.Fatalf("ls-files --ignored probe: %v", err)
	}
	if !strings.Contains(string(ignored), ".env") {
		t.Fatalf("test premise invalid: .env not listed by --ignored:\n%q", string(ignored))
	}

	// safe.txt is an untracked verbatim copy of the ignored protected .env.
	writeFile(t, workspace, "safe.txt", "SECRET=x\n")
	// Unrelated untracked notes.txt — MUST be present with its content.
	writeFile(t, workspace, "notes.txt", "plain note\n")
	issue, run := prepareReviewRunWithWorkspace(t, st, workspace, []string{"safe.txt", "notes.txt"})

	if _, err := (Generator{Store: st}).Generate(run.ID); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// safe.txt (untracked copy of IGNORED protected content) MUST NOT appear
	// in the patch or with a sha; the protected bytes never appear. notes.txt
	// MUST be present with its content.
	for _, name := range []string{"changes.patch", "changed-files.txt", "diffstat.txt", "untracked-files.json", "review.json"} {
		art := readReviewArtifact(t, st, issue, run, name)
		if strings.Contains(art, "SECRET=x") {
			t.Fatalf("%s leaked untracked ignored-protected-copy content (SECRET=x):\n%s", name, art)
		}
		if strings.Contains(art, "diff --git a/safe.txt b/safe.txt") {
			t.Fatalf("%s leaked safe.txt synthetic patch (copy of ignored protected content):\n%s", name, art)
		}
	}
	untracked := readUntrackedArtifact(t, st, issue, run)
	if info, ok := untracked["safe.txt"]; ok {
		if info.PatchIncluded || info.SHA256 != "" {
			t.Fatalf("safe.txt untracked info = %+v; want no patch and no sha (copy of ignored protected content suppressed)", info)
		}
	}
	notesInfo, ok := untracked["notes.txt"]
	if !ok {
		t.Fatalf("untracked artifact dropped unrelated notes.txt (common-case correlation broken): %+v", untracked)
	}
	if !notesInfo.PatchIncluded || notesInfo.SHA256 == "" {
		t.Fatalf("notes.txt untracked info = %+v; want patch included and sha set (unrelated safe content preserved)", notesInfo)
	}
	patch := readReviewArtifact(t, st, issue, run, "changes.patch")
	if !strings.Contains(patch, "diff --git a/notes.txt b/notes.txt") || !strings.Contains(patch, "+plain note") {
		t.Fatalf("changes.patch dropped unrelated untracked notes.txt content (common-case correlation broken):\n%s", patch)
	}
}

// TestGenerateKeepsEmptyFileWhenProtectedHasNoBlob codifies the D4 / R16
// round-7 fix B for review.go's collectChanges path (A-record variant of the
// empty-file false-suppression).
//
// Scenario: a protected .env exists in the workspace (real content) but is
// UNTRACKED (NOT ignored here so it IS enumerated) — no HEAD/index version.
// `git show HEAD:.env`/`:.env` both FAIL (non-zero exit, no stdout). Round 6's
// reviewHashGitBlob ignored cmd.Wait()'s error and returned sha256("") as a
// VALID protected hash. An unrelated EMPTY added file (empty.txt, 0 bytes)
// then matched that synthetic empty hash and was WRONGLY SUPPRESSED. Round 7
// fix B treats a non-nil Wait error as ok=false, so sha256("") is NOT a
// protected hash and empty.txt is KEPT.
func TestGenerateKeepsEmptyFileWhenProtectedHasNoBlob(t *testing.T) {
	st := newReviewTestStore(t)
	workspace := initGitWorkspace(t)
	writeFile(t, workspace, "app.txt", "base\n")
	runGit(t, workspace, "add", ".")
	runGit(t, workspace, "commit", "-m", "base")
	// Protected .env untracked with REAL content (workspace hash in set) but
	// NO HEAD/index version.
	writeFile(t, workspace, ".env", "SECRET=real\n")
	// Sanity: confirm `git show HEAD:.env` FAILS (non-zero exit) — the
	// premise of fix B.
	if out, err := exec.Command("git", "-C", workspace, "show", "HEAD:.env").CombinedOutput(); err == nil {
		t.Fatalf("test premise invalid: git show HEAD:.env succeeded on an untracked file:\n%s", string(out))
	}
	// An unrelated EMPTY added file (0 bytes). Its sha256("") must NOT match
	// any protected hash → MUST be kept.
	if err := os.WriteFile(filepath.Join(workspace, "empty.txt"), []byte{}, 0o644); err != nil {
		t.Fatalf("write empty.txt: %v", err)
	}
	runGit(t, workspace, "add", "empty.txt")
	// A non-empty unrelated added file, kept to prove normal correlation.
	writeFile(t, workspace, "feature.txt", "new feature\n")
	runGit(t, workspace, "add", "feature.txt")
	issue, run := prepareReviewRunWithWorkspace(t, st, workspace, []string{"empty.txt", "feature.txt"})

	if _, err := (Generator{Store: st}).Generate(run.ID); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	patch := readReviewArtifact(t, st, issue, run, "changes.patch")
	// The empty file MUST be kept (NOT wrongly suppressed by a synthetic
	// sha256("") protected hash).
	if !strings.Contains(patch, "diff --git a/empty.txt b/empty.txt") {
		t.Fatalf("changes.patch wrongly suppressed unrelated empty added file (fix B regression):\n%s", patch)
	}
	// The non-empty unrelated file MUST be kept.
	if !strings.Contains(patch, "diff --git a/feature.txt b/feature.txt") {
		t.Fatalf("changes.patch dropped unrelated safe added feature.txt:\n%s", patch)
	}
	// The protected .env bytes must never leak anywhere.
	for _, name := range []string{"changes.patch", "changed-files.txt", "diffstat.txt", "untracked-files.json", "review.json"} {
		art := readReviewArtifact(t, st, issue, run, name)
		if strings.Contains(art, "SECRET=real") {
			t.Fatalf("%s leaked protected .env bytes:\n%s", name, art)
		}
	}
	changed := readReviewArtifact(t, st, issue, run, "changed-files.txt")
	if !strings.Contains(changed, "empty.txt") {
		t.Fatalf("changed-files.txt dropped unrelated empty added file (fix B regression):\n%s", changed)
	}
	if !strings.Contains(changed, "feature.txt") {
		t.Fatalf("changed-files.txt dropped unrelated safe added feature.txt:\n%s", changed)
	}
}

// TestGenerateKeepsEmptyUntrackedFileWhenProtectedHasNoBlob codifies the D4
// / R16 round-7 fix B for review.go's collectChanges path (untracked ??
// variant of the empty-file false-suppression).
//
// Scenario: a protected .env exists in the workspace (real content) but is
// UNTRACKED (no HEAD/index version). An unrelated EMPTY untracked file
// (empty.txt, 0 bytes) is present. Round 6's reviewHashGitBlob returned
// sha256("") for the absent .env blob, so empty.txt's sha256("") matched and
// it was WRONGLY SUPPRESSED (denied: no patch, no sha, no listing). Round 7
// fix B makes the absent-blob lookup return ok=false, so sha256("") is NOT a
// protected hash and empty.txt is KEPT with its (empty) content hashed.
func TestGenerateKeepsEmptyUntrackedFileWhenProtectedHasNoBlob(t *testing.T) {
	st := newReviewTestStore(t)
	workspace := initGitWorkspace(t)
	writeFile(t, workspace, "app.txt", "base\n")
	runGit(t, workspace, "add", ".")
	runGit(t, workspace, "commit", "-m", "base")
	// Protected .env untracked with real content (workspace hash in set).
	writeFile(t, workspace, ".env", "SECRET=real\n")
	// Sanity: confirm `git show HEAD:.env` FAILS.
	if out, err := exec.Command("git", "-C", workspace, "show", "HEAD:.env").CombinedOutput(); err == nil {
		t.Fatalf("test premise invalid: git show HEAD:.env succeeded on an untracked file:\n%s", string(out))
	}
	// Unrelated EMPTY untracked file (0 bytes) — MUST be kept (not suppressed).
	if err := os.WriteFile(filepath.Join(workspace, "empty.txt"), []byte{}, 0o644); err != nil {
		t.Fatalf("write empty.txt: %v", err)
	}
	// Unrelated non-empty untracked file, kept to prove normal correlation.
	writeFile(t, workspace, "notes.txt", "plain note\n")
	issue, run := prepareReviewRunWithWorkspace(t, st, workspace, []string{"empty.txt", "notes.txt"})

	if _, err := (Generator{Store: st}).Generate(run.ID); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	untracked := readUntrackedArtifact(t, st, issue, run)
	// empty.txt MUST be present WITH its sha (its content hashed, not
	// suppressed). An empty file has no patch body but must still be LISTED
	// with a sha — the opposite of round 6's false suppression.
	emptyInfo, ok := untracked["empty.txt"]
	if !ok {
		t.Fatalf("untracked artifact dropped unrelated empty.txt (fix B regression): %+v", untracked)
	}
	// sha256 of empty content — must be set (proving it was hashed and kept,
	// not sentinel-suppressed).
	wantSha := sha256Hex(nil)
	if emptyInfo.SHA256 != wantSha {
		t.Fatalf("empty.txt untracked info = %+v; want sha %s (empty file KEPT and hashed, not suppressed)", emptyInfo, wantSha)
	}
	// notes.txt MUST be present with content.
	notesInfo, ok := untracked["notes.txt"]
	if !ok {
		t.Fatalf("untracked artifact dropped unrelated notes.txt (common-case correlation broken): %+v", untracked)
	}
	if !notesInfo.PatchIncluded || notesInfo.SHA256 == "" {
		t.Fatalf("notes.txt untracked info = %+v; want patch included and sha set (unrelated safe content preserved)", notesInfo)
	}
	// The protected .env bytes must never leak anywhere.
	for _, name := range []string{"changes.patch", "changed-files.txt", "diffstat.txt", "untracked-files.json", "review.json"} {
		art := readReviewArtifact(t, st, issue, run, name)
		if strings.Contains(art, "SECRET=real") {
			t.Fatalf("%s leaked protected .env bytes:\n%s", name, art)
		}
	}
}
