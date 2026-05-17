package review

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
		return "", err
	}
	changed, untracked := collectChanges(issue.Workspace.Path)
	if len(handoff.ChangedFiles) > 0 {
		for _, f := range handoff.ChangedFiles {
			if !contains(changed, f) {
				changed = append(changed, f)
			}
		}
	}
	sort.Strings(changed)
	patch := gitx.DiffBinary(issue.Workspace.Path)
	if patch == "" {
		patch = syntheticPatch(issue.Workspace.Path, untracked)
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
	_ = os.WriteFile(changedPath, []byte(strings.Join(changed, "\n")+"\n"), 0o644)
	_ = os.WriteFile(patchPath, []byte(patch), 0o644)
	diffstat := gitx.DiffNumstat(issue.Workspace.Path)
	if diffstat == "" {
		diffstat = "0\t0\tgenerated\n"
	}
	_ = os.WriteFile(diffstatPath, []byte(diffstat), 0o644)
	ub, _ := json.MarshalIndent(untracked, "", "  ")
	_ = os.WriteFile(untrackedPath, ub, 0o644)
	promptID := ""
	if run.WorkflowSnapshotID != nil {
		ctx := map[string]any{"issue_identifier": issue.Identifier, "run_id": runID}
		cb, _ := json.MarshalIndent(ctx, "", "  ")
		_ = os.WriteFile(filepath.Join(root, "prompt", "context.json"), cb, 0o644)
		_ = os.WriteFile(filepath.Join(root, "prompt", "rendered_prompt.redacted.md"), []byte("[redacted prompt snapshot metadata only]\n"), 0o644)
		_ = os.WriteFile(filepath.Join(root, "prompt", "prompt_meta.json"), []byte(`{"redacted":true}`), 0o644)
		_ = os.WriteFile(filepath.Join(root, "prompt", "tool_manifest.md"), []byte("issue.get\nissue.comment\nissue.block\nartifact.attach\nfollowup.create\nhandoff.submit\n"), 0o644)
		h := sha256.Sum256(cb)
		ph := sha256.Sum256([]byte("redacted"))
		pid, err := g.Store.CreatePromptSnapshot(runID, *run.WorkflowSnapshotID, hex.EncodeToString(h[:]), hex.EncodeToString(ph[:]), root)
		if err == nil {
			promptID = pid
		}
	}
	rpID := core.NewID("rp_tmp_")
	packetNo := nextPacketNo(g.Store, issue.ID)
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
	jb, _ := json.MarshalIndent(packet, "", "  ")
	_ = os.WriteFile(reviewJSONPath, jb, 0o644)
	_ = os.WriteFile(reviewMDPath, []byte(renderMarkdown(issue, handoff, changed, run)), 0o644)
	// Insert immutable packet with the final ID, then rewrite review.json with the final id.
	finalID, err := g.Store.InsertReviewPacket(issue.ID, runID, handoff.ID, root, reviewMDPath, reviewJSONPath, patchPath, changedPath, untrackedPath, diffstatPath, promptID)
	if err != nil {
		return "", err
	}
	packet["id"] = finalID
	jb, _ = json.MarshalIndent(packet, "", "  ")
	_ = os.WriteFile(reviewJSONPath, jb, 0o644)
	for _, item := range []struct{ kind, path string }{{"review_packet", reviewMDPath}, {"review_packet", reviewJSONPath}, {"patch", patchPath}, {"changed_files", changedPath}, {"untracked_files", untrackedPath}, {"diffstat", diffstatPath}} {
		st, _ := os.Stat(item.path)
		sha := fileSHA(item.path)
		_ = g.Store.InsertArtifact(store.ArtifactRecord{ID: core.NewID("art_"), IssueID: &issue.ID, RunID: &runID, ReviewPacketID: &finalID, Kind: item.kind, Path: item.path, SizeBytes: sizeOf(st), SHA256: &sha, Redacted: true})
	}
	return finalID, nil
}

func collectChanges(root string) ([]string, []UntrackedInfo) {
	changed := []string{}
	untracked := []UntrackedInfo{}
	if lines, err := gitx.StatusPorcelain(root); err == nil && len(lines) > 0 {
		for _, l := range lines {
			if len(l) < 4 {
				continue
			}
			path := strings.TrimSpace(l[3:])
			if security.IsProtectedPath(path) {
				continue
			}
			if !contains(changed, path) {
				changed = append(changed, path)
			}
			if strings.HasPrefix(l, "??") {
				untracked = append(untracked, untrackedInfo(root, path))
			}
		}
		return changed, untracked
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
		if security.IsProtectedPath(rel) {
			return nil
		}
		changed = append(changed, filepath.ToSlash(rel))
		untracked = append(untracked, untrackedInfo(root, rel))
		return nil
	})
	return changed, untracked
}
func untrackedInfo(root, path string) UntrackedInfo {
	p := filepath.Join(root, path)
	st, err := os.Stat(p)
	if err != nil {
		return UntrackedInfo{Path: path, PatchIncluded: false, Reason: core.StringPtr("stat failed")}
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return UntrackedInfo{Path: path, SizeBytes: st.Size(), PatchIncluded: false, Reason: core.StringPtr("read failed")}
	}
	sha := security.SHA256Bytes(b)
	patchIncluded := st.Size() <= 1024*1024 && !bytesContainNUL(b)
	var reason *string
	if !patchIncluded {
		reason = core.StringPtr("binary or large file omitted from patch")
	}
	return UntrackedInfo{Path: filepath.ToSlash(path), SizeBytes: st.Size(), SHA256: sha, PatchIncluded: patchIncluded, Reason: reason}
}
func syntheticPatch(root string, u []UntrackedInfo) string {
	var b strings.Builder
	for _, x := range u {
		if !x.PatchIncluded {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, x.Path))
		if err != nil {
			continue
		}
		b.WriteString("diff --git a/")
		b.WriteString(x.Path)
		b.WriteString(" b/")
		b.WriteString(x.Path)
		b.WriteString("\nnew file mode 100644\n--- /dev/null\n+++ b/")
		b.WriteString(x.Path)
		b.WriteString("\n@@\n")
		for _, line := range strings.Split(string(data), "\n") {
			if line != "" {
				b.WriteByte('+')
				b.WriteString(line)
				b.WriteByte('\n')
			}
		}
	}
	return b.String()
}
func bytesContainNUL(b []byte) bool {
	for _, c := range b {
		if c == 0 {
			return true
		}
	}
	return false
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
func fileSHA(path string) string {
	b, _ := os.ReadFile(path)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func sizeOf(st os.FileInfo) int64 {
	if st == nil {
		return 0
	}
	return st.Size()
}
func nextPacketNo(st *store.Store, issueID string) int {
	row, err := st.Project.QueryOne(`SELECT COALESCE(MAX(packet_no),0)+1 AS n FROM review_packets WHERE issue_id=?`, issueID)
	if err != nil {
		return 1
	}
	return row["n"].Int()
}
