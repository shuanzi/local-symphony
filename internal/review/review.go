package review

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"local-symphony/internal/core"
	"local-symphony/internal/gitx"
	"local-symphony/internal/security"
	"local-symphony/internal/store"
)

type Generator struct{ Store *store.Store }

type UntrackedInfo struct {
	Path          string  `json:"path"`
	SizeBytes     int64   `json:"size_bytes"`
	SHA256        string  `json:"sha256"`
	PatchIncluded bool    `json:"patch_included"`
	Reason        *string `json:"reason"`
}

const maxUntrackedPatchBytes int64 = 1024 * 1024

const (
	untrackedReasonBinaryOrLarge = "binary or large file omitted from patch"
	untrackedReasonSymlink       = "symlink omitted from patch"
	untrackedReasonNonRegular    = "non-regular file omitted from patch"
)

func (g Generator) Generate(runID string) (string, error) {
	run, err := g.Store.GetRun(runID)
	if err != nil {
		return "", err
	}
	issue, err := g.Store.GetIssue(run.IssueID)
	if err != nil {
		return "", err
	}
	if issue.Workspace == nil {
		return "", core.NewError(core.ErrReviewPacketFailed, "workspace is missing", nil)
	}
	handoff, err := g.Store.GetHandoffByRun(runID)
	if err != nil {
		return "", err
	}
	root := filepath.Join(g.Store.RepoRoot, ".symphony", "artifacts", issue.Identifier, runID)
	if err := os.MkdirAll(filepath.Join(root, "prompt"), 0o755); err != nil {
		return "", reviewPacketError("create review packet directory", err)
	}
	changed, untracked, deniedChanged := collectChanges(issue.Workspace.Path)
	if len(handoff.ChangedFiles) > 0 {
		for _, f := range handoff.ChangedFiles {
			if path := reviewSafePath(f); path != "" && !deniedChanged[path] && !contains(changed, path) {
				changed = append(changed, path)
			}
		}
	}
	sort.Strings(changed)
	diffPaths := reviewDiffPaths(changed)
	patch := gitx.DiffBinaryPaths(issue.Workspace.Path, diffPaths)
	if untrackedPatch := syntheticPatch(issue.Workspace.Path, untracked); untrackedPatch != "" {
		if patch != "" && !strings.HasSuffix(patch, "\n") {
			patch += "\n"
		}
		patch += untrackedPatch
	}
	if patch == "" {
		patch = "# No tracked diff captured.\n"
	}
	changedPath := filepath.Join(root, "changed-files.txt")
	patchPath := filepath.Join(root, "changes.patch")
	diffstatPath := filepath.Join(root, "diffstat.txt")
	untrackedPath := filepath.Join(root, "untracked-files.json")
	reviewJSONPath := filepath.Join(root, "review.json")
	reviewMDPath := filepath.Join(root, "review.md")
	if err := os.WriteFile(changedPath, []byte(strings.Join(changed, "\n")+"\n"), 0o644); err != nil {
		return "", reviewPacketError("write changed files artifact", err)
	}
	if err := os.WriteFile(patchPath, []byte(patch), 0o644); err != nil {
		return "", reviewPacketError("write patch artifact", err)
	}
	diffstat := gitx.DiffNumstatPaths(issue.Workspace.Path, diffPaths)
	if diffstat == "" {
		diffstat = "0\t0\tgenerated\n"
	}
	if err := os.WriteFile(diffstatPath, []byte(diffstat), 0o644); err != nil {
		return "", reviewPacketError("write diffstat artifact", err)
	}
	ub, err := json.MarshalIndent(untracked, "", "  ")
	if err != nil {
		return "", reviewPacketError("marshal untracked files artifact", err)
	}
	if err := os.WriteFile(untrackedPath, ub, 0o644); err != nil {
		return "", reviewPacketError("write untracked files artifact", err)
	}
	promptID := ""
	promptContextHash := ""
	promptRenderedHash := ""
	if run.WorkflowSnapshotID != nil {
		ctx := map[string]any{"issue_identifier": issue.Identifier, "run_id": runID}
		cb, err := json.MarshalIndent(ctx, "", "  ")
		if err != nil {
			return "", reviewPacketError("marshal prompt context", err)
		}
		if err := os.WriteFile(filepath.Join(root, "prompt", "context.json"), cb, 0o644); err != nil {
			return "", reviewPacketError("write prompt context", err)
		}
		if err := os.WriteFile(filepath.Join(root, "prompt", "rendered_prompt.redacted.md"), []byte("[redacted prompt snapshot metadata only]\n"), 0o644); err != nil {
			return "", reviewPacketError("write redacted prompt", err)
		}
		if err := os.WriteFile(filepath.Join(root, "prompt", "prompt_meta.json"), []byte(`{"redacted":true}`), 0o644); err != nil {
			return "", reviewPacketError("write prompt metadata", err)
		}
		if err := os.WriteFile(filepath.Join(root, "prompt", "tool_manifest.md"), []byte("issue.get\nissue.comment\nissue.block\nartifact.attach\nfollowup.create\nhandoff.submit\n"), 0o644); err != nil {
			return "", reviewPacketError("write tool manifest", err)
		}
		h := sha256.Sum256(cb)
		ph := sha256.Sum256([]byte("redacted"))
		promptContextHash = hex.EncodeToString(h[:])
		promptRenderedHash = hex.EncodeToString(ph[:])
	}
	var finalID string
	if err := g.Store.WithProjectTx(func(tx store.TxRunner) error {
		if run.WorkflowSnapshotID != nil {
			pid, err := g.Store.CreatePromptSnapshotTx(tx, runID, *run.WorkflowSnapshotID, promptContextHash, promptRenderedHash, root)
			if err != nil {
				return reviewPacketError("create prompt snapshot", err)
			}
			promptID = pid
		}
		rpID := core.NewID("rp_tmp_")
		packetNo := nextPacketNo(tx, issue.ID)
		packet := map[string]any{
			"id": rpID, "packet_no": packetNo, "status": "generated",
			"issue":         map[string]any{"id": issue.ID, "identifier": issue.Identifier, "title": issue.Title, "acceptance_criteria": issue.AcceptanceCriteria},
			"run":           map[string]any{"id": run.ID, "status": "completed", "source_issue_state": run.SourceIssueState},
			"git":           map[string]any{"branch_name": val(issue.BranchName), "base_ref": val(issue.BaseRef), "base_ref_config": val(issue.BaseRefConfig), "base_sha": val(issue.BaseSHA), "head_sha": gitx.HeadSHA(issue.Workspace.Path), "dirty": len(changed) > 0},
			"files":         map[string]any{"review_md_path": "review.md", "review_json_path": "review.json", "patch_path": "changes.patch", "changed_files_path": "changed-files.txt", "untracked_files_path": "untracked-files.json", "diffstat_path": "diffstat.txt"},
			"handoff":       map[string]any{"summary": handoff.Summary, "tests": handoff.Tests, "risks": handoff.Risks, "verification": handoff.Verification, "followups": handoff.Followups, "target_state": "Human Review"},
			"changed_files": changed, "untracked_files": untracked,
			"approvals": []any{}, "tool_calls": []any{},
			"prompt_snapshot": map[string]any{"id": promptID, "rendered_prompt_hash": "redacted", "tool_manifest_path": "prompt/tool_manifest.md"},
			"failure_code":    nil, "failure_message": nil, "created_at": core.Now(),
		}
		jb, err := json.MarshalIndent(packet, "", "  ")
		if err != nil {
			return reviewPacketError("marshal review packet", err)
		}
		if err := os.WriteFile(reviewJSONPath, jb, 0o644); err != nil {
			return reviewPacketError("write review json artifact", err)
		}
		if err := os.WriteFile(reviewMDPath, []byte(renderMarkdown(issue, handoff, changed, run)), 0o644); err != nil {
			return reviewPacketError("write review markdown artifact", err)
		}
		// Insert immutable packet with the final ID, then rewrite review.json with the final id.
		finalID, err = g.Store.InsertReviewPacketTx(tx, issue.ID, runID, handoff.ID, root, reviewMDPath, reviewJSONPath, patchPath, changedPath, untrackedPath, diffstatPath, promptID)
		if err != nil {
			return reviewPacketError("insert review packet", err)
		}
		packet["id"] = finalID
		jb, err = json.MarshalIndent(packet, "", "  ")
		if err != nil {
			return reviewPacketError("marshal final review packet", err)
		}
		if err := os.WriteFile(reviewJSONPath, jb, 0o644); err != nil {
			return reviewPacketError("write final review json artifact", err)
		}
		for _, item := range []struct{ kind, path string }{{"review_packet", reviewMDPath}, {"review_packet", reviewJSONPath}, {"patch", patchPath}, {"changed_files", changedPath}, {"untracked_files", untrackedPath}, {"diffstat", diffstatPath}} {
			st, err := os.Stat(item.path)
			if err != nil {
				return reviewPacketError("stat review artifact", err)
			}
			sha, err := fileSHA(item.path)
			if err != nil {
				return reviewPacketError("hash review artifact", err)
			}
			if err := g.Store.InsertArtifactTx(tx, store.ArtifactRecord{ID: core.NewID("art_"), IssueID: &issue.ID, RunID: &runID, ReviewPacketID: &finalID, Kind: item.kind, Path: item.path, SizeBytes: st.Size(), SHA256: &sha, Redacted: true}); err != nil {
				return reviewPacketError("insert review artifact metadata", err)
			}
		}
		return nil
	}); err != nil {
		return "", err
	}
	return finalID, nil
}

func collectChanges(root string) ([]string, []UntrackedInfo, map[string]bool) {
	changed := []string{}
	untracked := []UntrackedInfo{}
	denied := map[string]bool{}
	if records, err := statusPorcelainRecords(root); err == nil && len(records) > 0 {
		for _, record := range records {
			if len(record.paths) == 0 {
				continue
			}
			paths := record.paths
			safePaths := make([]string, 0, len(paths))
			for _, candidate := range paths {
				path := reviewSafePath(candidate)
				if path == "" {
					if record.renamedOrCopied() && len(paths) > 1 {
						if dst := reviewSafePath(paths[len(paths)-1]); dst != "" {
							denied[dst] = true
						}
					}
					safePaths = nil
					break
				}
				safePaths = append(safePaths, path)
			}
			if len(safePaths) == 0 {
				continue
			}
			for _, path := range safePaths {
				if !contains(changed, path) {
					changed = append(changed, path)
				}
			}
			if record.code == "??" {
				untracked = append(untracked, untrackedInfo(root, safePaths[0]))
			}
		}
		return changed, untracked, denied
	}
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if rel == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		path := reviewSafePath(rel)
		if path == "" {
			return nil
		}
		changed = append(changed, path)
		untracked = append(untracked, untrackedInfo(root, path))
		return nil
	})
	return changed, untracked, denied
}

type statusPorcelainRecord struct {
	code  string
	paths []string
}

func (r statusPorcelainRecord) renamedOrCopied() bool {
	return len(r.code) >= 2 && (r.code[0] == 'R' || r.code[1] == 'R' || r.code[0] == 'C' || r.code[1] == 'C')
}

func statusPorcelainRecords(root string) ([]statusPorcelainRecord, error) {
	cmd := exec.Command("git", "status", "--porcelain=v1", "-z", "-uall")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseStatusPorcelainZ(string(out)), nil
}

func parseStatusPorcelainZ(out string) []statusPorcelainRecord {
	fields := strings.Split(out, "\x00")
	records := []statusPorcelainRecord{}
	for i := 0; i < len(fields); i++ {
		field := fields[i]
		if field == "" || len(field) < 4 {
			continue
		}
		record := statusPorcelainRecord{code: field[:2], paths: []string{field[3:]}}
		if record.renamedOrCopied() {
			if i+1 >= len(fields) || fields[i+1] == "" {
				continue
			}
			record.paths = []string{fields[i+1], field[3:]}
			i++
		}
		records = append(records, record)
	}
	return records
}

func statusPorcelainPaths(line string) []string {
	path := strings.TrimSpace(line[3:])
	if renamedOrCopied(line) {
		if idx := strings.LastIndex(path, " -> "); idx >= 0 {
			return []string{decodeStatusPath(path[:idx]), decodeStatusPath(path[idx+4:])}
		}
	}
	return []string{decodeStatusPath(path)}
}
func renamedOrCopied(line string) bool {
	return len(line) >= 2 && (line[0] == 'R' || line[1] == 'R' || line[0] == 'C' || line[1] == 'C')
}
func decodeStatusPath(path string) string {
	path = strings.TrimSpace(path)
	if strings.HasPrefix(path, `"`) {
		if unquoted, err := strconv.Unquote(path); err == nil {
			return unquoted
		}
	}
	return path
}
func untrackedInfo(root, path string) UntrackedInfo {
	path = filepath.ToSlash(path)
	data, size, reason, err := readUntrackedPatchData(root, path)
	if err != nil {
		return UntrackedInfo{Path: path, SizeBytes: size, PatchIncluded: false, Reason: reason}
	}
	sha := ""
	if data != nil {
		sha = security.SHA256Bytes(data)
	}
	return UntrackedInfo{Path: path, SizeBytes: size, SHA256: sha, PatchIncluded: reason == nil, Reason: reason}
}
func syntheticPatch(root string, u []UntrackedInfo) string {
	var b strings.Builder
	for _, x := range u {
		if !x.PatchIncluded {
			continue
		}
		data, _, reason, err := readUntrackedPatchData(root, x.Path)
		if err != nil || reason != nil {
			continue
		}
		b.WriteString("diff --git a/")
		b.WriteString(x.Path)
		b.WriteString(" b/")
		b.WriteString(x.Path)
		b.WriteString("\nnew file mode 100644\n--- /dev/null\n+++ b/")
		b.WriteString(x.Path)
		b.WriteString("\n@@\n")
		for _, line := range strings.SplitAfter(string(data), "\n") {
			if line == "" {
				continue
			}
			b.WriteByte('+')
			b.WriteString(strings.TrimSuffix(line, "\n"))
			b.WriteByte('\n')
		}
	}
	return b.String()
}
func readUntrackedPatchData(root, path string) ([]byte, int64, *string, error) {
	p := filepath.Join(root, filepath.FromSlash(path))
	st, err := os.Lstat(p)
	if err != nil {
		return nil, 0, core.StringPtr("stat failed"), err
	}
	size := st.Size()
	if st.Mode()&os.ModeSymlink != 0 {
		return nil, size, core.StringPtr(untrackedReasonSymlink), nil
	}
	if !st.Mode().IsRegular() {
		return nil, size, core.StringPtr(untrackedReasonNonRegular), nil
	}
	if size > maxUntrackedPatchBytes {
		return nil, size, core.StringPtr(untrackedReasonBinaryOrLarge), nil
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, size, core.StringPtr("read failed"), err
	}
	defer f.Close()
	if current, err := f.Stat(); err == nil {
		size = current.Size()
		if size > maxUntrackedPatchBytes {
			return nil, size, core.StringPtr(untrackedReasonBinaryOrLarge), nil
		}
	}
	data, err := io.ReadAll(io.LimitReader(f, maxUntrackedPatchBytes+1))
	if err != nil {
		return nil, size, core.StringPtr("read failed"), err
	}
	if int64(len(data)) > maxUntrackedPatchBytes {
		return nil, int64(len(data)), core.StringPtr(untrackedReasonBinaryOrLarge), nil
	}
	if bytesContainNUL(data) {
		return data, size, core.StringPtr(untrackedReasonBinaryOrLarge), nil
	}
	return data, size, nil, nil
}
func bytesContainNUL(b []byte) bool {
	for _, c := range b {
		if c == 0 {
			return true
		}
	}
	return false
}
func reviewSafePath(path string) string {
	path = filepath.ToSlash(filepath.Clean(path))
	if path == "." || strings.HasPrefix(path, "../") || path == ".." || filepath.IsAbs(path) {
		return ""
	}
	if security.IsProtectedPath(path) {
		return ""
	}
	return path
}
func reviewDiffPaths(changed []string) []string {
	out := []string{}
	for _, path := range changed {
		path = reviewSafePath(path)
		if path == "" || contains(out, path) {
			continue
		}
		out = append(out, path)
	}
	return out
}
func contains(a []string, x string) bool {
	for _, v := range a {
		if v == x {
			return true
		}
	}
	return false
}
func renderMarkdown(issue *core.Issue, h *core.Handoff, changed []string, run *core.RunAttempt) string {
	return fmt.Sprintf(`# %s Review Packet

## Summary
%s

## Acceptance Criteria
%s

## Handoff
Target: Human Review

## Changed Files
%s

## Tests
%s

## Risks
%s

## Verification Steps
%s

## Approvals
None recorded.

## Tool Calls
See tool-calls metadata when present.

## Git
Branch: %s
Base: %s

## How to Continue
Use Send to Rework with a reason, or Mark Done with an acceptance reason.
`, issue.Identifier, h.Summary, bullet(issue.AcceptanceCriteria), bullet(changed), bullet(h.Tests), bullet(h.Risks), bullet(h.Verification), val(issue.BranchName), val(issue.BaseSHA))
}
func bullet(v []string) string {
	if len(v) == 0 {
		return "- none"
	}
	out := []string{}
	for _, x := range v {
		out = append(out, "- "+x)
	}
	return strings.Join(out, "\n")
}
func val(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
func reviewPacketError(action string, err error) error {
	if err == nil {
		return nil
	}
	return core.NewError(core.ErrReviewPacketFailed, action+": "+err.Error(), nil)
}
func fileSHA(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
func nextPacketNo(q store.TxRunner, issueID string) int {
	row, err := q.QueryOne(`SELECT COALESCE(MAX(packet_no),0)+1 AS n FROM review_packets WHERE issue_id=?`, issueID)
	if err != nil {
		return 1
	}
	return row["n"].Int()
}
