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

	"local-symphony/internal/config"
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
	// D4/R16 round-9: honor the workflow's configured protected_paths
	// (approvals.protected_paths) in review collection, not just the
	// built-in defaults. The orchestrator already threads these into its
	// cumulative_diff_sha hashing via IsProtectedPathWithConfig; review
	// collection must use the SAME policy so a custom protected path such
	// as `secrets/**` is excluded from changes.patch / review.json and
	// included in the protected-content hash set. config.Load falls back
	// to Defaults (which include the built-in protected paths) when
	// WORKFLOW.md is absent or unreadable, so this never returns nil.
	protectedPaths := loadProtectedPaths(g.Store, run, g.Store.RepoRoot)
	changed, untracked, deniedChanged := collectChanges(issue.Workspace.Path, protectedPaths)
	if len(handoff.ChangedFiles) > 0 {
		for _, f := range handoff.ChangedFiles {
			if path := reviewSafePath(f, protectedPaths); path != "" && !deniedChanged[path] && !contains(changed, path) {
				changed = append(changed, path)
			}
		}
	}
	sort.Strings(changed)
	diffPaths := reviewDiffPaths(changed, protectedPaths)
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
	if untrackedStat := syntheticNumstat(issue.Workspace.Path, untracked); untrackedStat != "" {
		if diffstat != "" && !strings.HasSuffix(diffstat, "\n") {
			diffstat += "\n"
		}
		diffstat += untrackedStat
	}
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
			// D4 / R16: if the orchestrator already created a prompt
			// snapshot for this run (the case for Rework dispatches
			// where the snapshot is stamped with the post-injection
			// prompt hash), preserve that hash instead of
			// overwriting it with the synthetic "redacted" hash.
			existing, lookupErr := tx.QueryOne(`SELECT id FROM prompt_snapshots WHERE run_id=?`, runID)
			var pid string
			if lookupErr == nil {
				pid = existing["id"].String()
				if err := tx.Exec(`UPDATE prompt_snapshots SET workflow_snapshot_id=COALESCE(workflow_snapshot_id, ?), runtime_envelope_version=?, tool_manifest_version=?, context_hash=?, context_json_path=?, redacted_prompt_path=?, prompt_meta_json_path=?, tool_manifest_path=? WHERE id=?`, *run.WorkflowSnapshotID, "v1", "v1", promptContextHash, filepath.Join(root, "prompt/context.json"), filepath.Join(root, "prompt/rendered_prompt.redacted.md"), filepath.Join(root, "prompt/prompt_meta.json"), filepath.Join(root, "prompt/tool_manifest.md"), pid); err != nil {
					return reviewPacketError("update prompt snapshot", err)
				}
			} else {
				var err error
				pid, err = g.Store.CreatePromptSnapshotTx(tx, runID, *run.WorkflowSnapshotID, promptContextHash, promptRenderedHash, root)
				if err != nil {
					return reviewPacketError("create prompt snapshot", err)
				}
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
	// D4 / R16: post-transaction, embed the safe summary into
	// review.json so follow-on Rework dispatches can read it from
	// the persisted packet instead of recomputing from disk
	// artifacts. We rebuild the safe summary with the final packet
	// id and re-marshal review.json.
	//
	// PR #27 / D4 F4: after the rewrite, recompute the on-disk
	// SHA256 + size and update the artifacts row so the metadata
	// reflects the *post-rewrite* content. The previous
	// implementation stamped the artifact row with the pre-rewrite
	// SHA/size, leaving a stale metadata row out of sync with the
	// file the next operator or Rework injector reads.
	if safe, safeErr := BuildSafeSummaryFromIssue(g.Store, issue, run); safeErr == nil && safe != nil {
		safe.ReviewPacketID = finalID
		if data, rerr := os.ReadFile(reviewJSONPath); rerr == nil {
			var packet map[string]any
			if uerr := json.Unmarshal(data, &packet); uerr == nil {
				packet["safe_summary"] = safe
				if jb, merr := json.MarshalIndent(packet, "", "  "); merr == nil {
					if werr := os.WriteFile(reviewJSONPath, jb, 0o644); werr == nil {
						newSHA := sha256.Sum256(jb)
						newSHAHex := hex.EncodeToString(newSHA[:])
						newSize := int64(len(jb))
						if uerr := g.Store.Project.Exec(
							`UPDATE artifacts SET size_bytes=?, sha256=? WHERE review_packet_id=? AND kind='review_packet' AND path=?`,
							newSize, newSHAHex, finalID, reviewJSONPath,
						); uerr != nil {
							return "", reviewPacketError("update review.json artifact metadata", uerr)
						}
					}
				}
			}
		}
	}
	return finalID, nil
}

// loadProtectedPaths returns the protected_paths policy that governs THIS
// run, for use by review collection (IsProtectedPathWithConfig).
//
// D4/R16 round-9: review collection honors the workflow's configured
// protected_paths, not just the built-in defaults, so custom protected
// paths such as `secrets/**` are protected in review artifacts too.
//
// D4/R16 round-10 (codex finding D): read the policy from the run's
// CAPTURED workflow snapshot (run.WorkflowSnapshotID → workflow_snapshots
// .config_json), NOT a fresh config.Load of the live WORKFLOW.md. A
// WORKFLOW.md edit/removal between dispatch and review generation would
// otherwise change which paths were protected for this run, letting a
// protected file/copy leak into changes.patch / review.json under a
// different policy than the one the run was dispatched under. The snapshot
// config_json is the exact EffectiveConfig captured at dispatch time.
//
// Fallbacks (only when the snapshot is absent/unreadable, e.g. a run
// dispatched before workflow snapshots existed): config.Load(repoRoot)
// (live WORKFLOW.md, falling back to Defaults internally), then the
// built-in DefaultPolicy().ProtectedPaths. These fallbacks never return
// nil, so the returned slice is always usable.
func loadProtectedPaths(st *store.Store, run *core.RunAttempt, repoRoot string) []string {
	if run != nil && run.WorkflowSnapshotID != nil && *run.WorkflowSnapshotID != "" {
		if cfgJSON, err := st.GetWorkflowSnapshotConfigJSON(*run.WorkflowSnapshotID); err == nil && cfgJSON != "" {
			var cfg struct {
				Approvals struct {
					ProtectedPaths []string `json:"protected_paths"`
				} `json:"approvals"`
			}
			if jsonErr := json.Unmarshal([]byte(cfgJSON), &cfg); jsonErr == nil && cfg.Approvals.ProtectedPaths != nil {
				return cfg.Approvals.ProtectedPaths
			}
		}
	}
	// Fallback: live config (matches the orchestrator's config.Load path).
	if wf, err := config.Load(repoRoot); wf != nil && err == nil {
		return wf.Config.Approvals.ProtectedPaths
	}
	return security.DefaultPolicy().ProtectedPaths
}

func collectChanges(root string, protectedPaths []string) ([]string, []UntrackedInfo, map[string]bool) {
	changed := []string{}
	untracked := []UntrackedInfo{}
	denied := map[string]bool{}
	if records, err := statusPorcelainRecords(root); err == nil {
		protectedDeleted := protectedDeletedContent(root, records, protectedPaths)
		// D4 / R16 round 5 — copy-aware protected-copy detection for
		// TRACKED ADDED (A) records. `git status --porcelain` detects
		// renames (R) but NOT copies (C): a verbatim copy of a protected
		// file whose source REMAINS (`cp .env public.txt && git add
		// public.txt`, no delete) is reported by porcelain as a plain
		// `A public.txt`, NOT a `C` record. The per-record reviewSafePath
		// check below only rejects records whose OWN path is protected, so
		// such an A record passes through, enters `changed`, and
		// gitx.DiffBinaryPaths emits its patch — which contains the copied
		// protected bytes — into changes.patch / diffstat.txt / review.json.
		// `--find-copies-harder` makes git report the copy as `C100 .env
		// public.txt` (source first, destination second), so we can detect
		// the protected source and SKIP the destination A record. This
		// mirrors the orchestrator's filteredTrackedDiffPathspecs.
		//
		// copyDestFailClosed is true when the name-status enumeration fails
		// or is malformed: we cannot prove no A record is a protected copy,
		// so the caller fails closed (skips ALL tracked A records).
		copyDestinations, copyDestOK := protectedCopyDestinations(root, protectedPaths)
		copyDestFailClosed := !copyDestOK
		for _, record := range records {
			if len(record.paths) == 0 {
				continue
			}
			paths := record.paths
			safePaths := make([]string, 0, len(paths))
			for _, candidate := range paths {
				path := reviewSafePath(candidate, protectedPaths)
				if path == "" {
					if record.renamedOrCopied() && len(paths) > 1 {
						if dst := reviewSafePath(paths[len(paths)-1], protectedPaths); dst != "" {
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
			if record.code == "??" && protectedDeleted.matchesUntracked(root, safePaths[0]) {
				denied[safePaths[0]] = true
				continue
			}
			// D4 / R16 round 5/6 — fail-closed + copy-aware + content-hash
			// skip for TRACKED ADDED (A) records. The `changed` list feeds
			// gitx.DiffBinaryPaths, whose patch would emit the added file's
			// bytes. An A file may hold copied protected bytes in three cases
			// that porcelain alone cannot detect:
			//
			//   (a) A protected tracked file was deleted/renamed/copied
			//       (protectedDeleted.unknown=true): the A file could be a
			//       copy of the protected bytes (e.g. `rm .env; cp .env
			//       public.txt; git add public.txt`). Round 4's fail-closed
			//       only suppressed UNTRACKED (??) files in this case; the
			//       staged `A public.txt` still entered `changed` and leaked.
			//       Round 5 made the fail-closed skip ALL tracked A records
			//       when unknown=true (mirrors the orchestrator).
			//
			//   (b) A protected file is copied VERBATIM FROM HEAD but the
			//       source REMAINS (unknown=false): porcelain reports the
			//       copy as `A` (porcelain does not detect copies). Round 5
			//       detects this via --find-copies-harder (copyDestinations
			//       holds the A destinations whose R/C source is protected)
			//       and skips them.
			//
			//   (c) A protected file is MODIFIED then copied while the source
			//       REMAINS (unknown=false): `git diff --find-copies-harder
			//       HEAD` compares the copy against the unmodified HEAD blob,
			//       so a copy of the MODIFIED (workspace) bytes is reported as
			//       `M .env` + `A public.txt` — NOT a `C` record — and
			//       copyDestinations does NOT flag it. Round 6 closes this:
			//       matchesAddedTracked content-hash-matches the A file's
			//       WORKSPACE content against existingHashes (workspace +
			//       HEAD + index of all existing protected files) and skips
			//       on a match.
			//
			// An unrelated A file (no protected operation, not a protected
			// copy) is KEPT — its patch is safe and correlation is preserved
			// in the common case.
			//
			// Residual risk (documented, matches orchestrator behavior): a
			// "filler copy" — a copy with enough added/changed content that
			// its hash does NOT match any recoverable protected version — is
			// an undetected deliberate-evasion leak when the source REMAINS
			// (unknown=false). When unknown=true (protected
			// delete/rename/copy) the fail-closed skip of ALL A records
			// covers even filler copies. This mirrors the orchestrator,
			// which also cannot detect filler copies when the source remains.
			if record.code != "??" && isAddedRecord(record.code) {
				dst := safePaths[len(safePaths)-1]
				// (a) fail-closed on protected delete/rename/copy/typechange
				//     or --find-copies-harder enumeration failure; (b)
				//     verbatim-from-HEAD copy fast-path; (c) round-6
				//     content-hash match against existing protected content
				//     (covers the modified-source copy).
				if protectedDeleted.unknown || copyDestFailClosed || copyDestinations[dst] || protectedDeleted.matchesAddedTracked(root, dst) {
					denied[dst] = true
					continue
				}
			}
			if record.code != "??" && isModifiedTrackedRecord(record.code) {
				// D4/R16 round-8 — modified-tracked-copy guard. A tracked
				// non-protected file whose workspace content was overwritten
				// with protected bytes (`cp .env config.txt`, config.txt
				// already tracked → `M config.txt`) would otherwise enter
				// `changed` and gitx.DiffBinaryPaths would emit the protected
				// bytes into changes.patch. This is the source-REMAINS case
				// the review finding describes, so it only fires when
				// unknown=false (no protected delete/rename/copy/typechange):
				// existingHashes is built and a content-hash match suppresses
				// the M record. When unknown=true we do NOT fail-closed M
				// records (unlike A) — an unrelated in-progress modification
				// such as app.txt old→new is preserved, matching the
				// user-approved trade-off codified by
				// TestGenerateDoesNotReincludeProtectedRenameFromHandoff. A
				// protected delete whose bytes were copied onto a tracked M
				// file in the same worktree is a residual evasion the
				// unknown=true fail-closed covers only for A/untracked.
				dst := safePaths[len(safePaths)-1]
				if protectedDeleted.matchesModifiedTracked(root, dst) {
					denied[dst] = true
					continue
				}
			}
			if record.code != "??" && record.typechange() {
				// D4/R16 round-10 (codex finding F) — typechange-on-non-
				// protected-path guard. A tracked non-protected file whose
				// type changes (e.g. regular file → symlink) is reported as
				// `T config.txt`. gitx.DiffBinaryPaths emits the symlink
				// TARGET text into changes.patch (`+SECRET=...`), so a
				// typechange whose target is copied from a protected file
				// (`rm config.txt; ln -s "$(cat .env)" config.txt`) leaks
				// the protected bytes. (A typechange on a PROTECTED path is
				// already fail-closed by protectedDeletedContent.unknown;
				// this guard handles the NON-protected path case.) Like the
				// M guard, this only content-hash-checks when unknown=false
				// (source REMAINS); when unknown=true we do NOT blanket
				// fail-closed T records (preserve an unrelated typechange),
				// matching the M trade-off.
				dst := safePaths[len(safePaths)-1]
				if protectedDeleted.matchesTypechangeTracked(root, dst) {
					denied[dst] = true
					continue
				}
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
		path := reviewSafePath(rel, protectedPaths)
		if path == "" {
			return nil
		}
		changed = append(changed, path)
		untracked = append(untracked, untrackedInfo(root, path))
		return nil
	})
	return changed, untracked, denied
}

// protectedDeletedContentSet carries the fail-closed decision AND the
// existing-protected-content hash set for the review packet's
// untracked-file and added-tracked-file handling.
//
// D4 / R16 round 4 — SECURITY-FIRST, fail-closed on ANY protected
// delete, rename, or copy.
//
// Round 1/2 content-hash-matched untracked files against the deleted
// protected file's HEAD/index bytes. Round 2 found P1 leak #1: when a
// protected file is MODIFIED WITH UNSTAGED EDITS before being deleted
// (e.g. edit .env to SECRET=new WITHOUT `git add`, copy it to safe.txt,
// then `rm .env`), the worktree bytes (SECRET=new) are GONE and
// unrecoverable; `git show :.env` returns the old index/HEAD blob
// (SECRET=old), so a copy holding the modified bytes does NOT match the
// hash set and is let through → protected bytes enter changes.patch /
// review.json. The root cause is that the pre-deletion WORKTREE content
// — the only bytes that could have been copied — is fundamentally
// unrecoverable when unstaged modifications existed, and after deletion
// this is UNDETECTABLE (index==HEAD looks identical whether the file
// was unmodified-then-deleted or unstaged-modified-then-deleted).
//
// Round 4 found P1 leak #3: a protected file staged as a RENAME/COPY
// (`git mv .env renamed.txt`, an `R` porcelain record) was invisible
// to round 3's delete-only check, so unknown stayed false and an
// untracked file holding the copied bytes was preserved → leak. A
// protected rename/copy is just as dangerous as a delete, so the
// fail-closed must trigger on protected R/C sources too.
//
// Content-hash matching therefore CANNOT be made safe in the
// protected-delete/rename/copy case. We fail closed: when ANY protected
// tracked file is deleted, renamed, or copied, unknown=true and
// matchesUntracked returns true for EVERY non-path-protected untracked
// file, so collectChanges suppresses (denies) ALL of them — protected
// bytes never appear in any review artifact.
//
// D4 / R16 round 6 — closes the MODIFIED-source-copy P1 leak. When NO
// protected file is deleted/renamed/copied (the common case,
// unknown=false) but a protected file is MODIFIED then copied into a new
// untracked file OR a new tracked-added (A) file while the source REMAINS
// (edit .env, `cp .env safe.txt` or `cp .env public.txt && git add
// public.txt`), `git diff --find-copies-harder HEAD` reports `M .env` +
// `A public.txt` (or just the untracked file) — NOT a `C` record — so
// the round-5 copy-aware --find-copies-harder check does not fire and
// the copied modified protected bytes leaked into changes.patch /
// review.json / untracked-files.json. The protected source REMAINS, so
// its content is fully recoverable (workspace + HEAD + index); we build
// existingHashes = the union of SHA256 hashes of all recoverable versions
// of all existing protected files, and content-hash-match each untracked
// file and each A record's workspace content against it: on a match we
// suppress (deny); otherwise we preserve (full content-level correlation
// in the common case). See existingProtectedContentHashes for the full
// rationale and the documented filler-copy residual risk.
//
// This is the user-approved P1>P2 trade-off: P1 security (never leak
// protected bytes) wins over P2 content-level diagnostic correlation,
// but ONLY in the (uncommon) protected-delete/rename/copy case; the
// common case keeps full correlation.
type protectedDeletedContentSet struct {
	// existingHashes is the set of SHA256 content hashes of all
	// recoverable versions (workspace + HEAD + index) of all existing
	// protected files. Populated ONLY when unknown=false (no protected
	// delete/rename/copy — all protected files REMAIN). Empty (non-nil)
	// when there are no protected files at all → no untracked/A file can
	// match → all preserved. nil when unknown=true (unused; we deny all).
	existingHashes map[string]bool
	unknown        bool
}

// matchesUntracked reports whether an untracked file must be suppressed
// (denied) from the review packet.
//   - When unknown (a protected tracked file was deleted/renamed/copied)
//     it returns true for every file — fail-closed (no content read).
//   - When not unknown and existingHashes is empty (no protected files
//     exist at all) it returns false — full correlation.
//   - When not unknown and existingHashes is non-empty, it content-hash-
//     matches the untracked file's content against existingHashes: a match
//     means the file is a copy of existing protected content (including a
//     MODIFIED protected source) → suppress; otherwise preserve. On read
//     error it returns true (fail closed for that file — cannot prove it
//     is safe).
//
// D4/R16 round-8 fix: when readUntrackedPatchData returns BYTES (data !=
// nil) — which happens for BOTH text files AND small binary files whose
// content contains a NUL byte (reason == binaryOrLarge but data still
// holds the bytes) — the hash check runs against existingHashes. The
// previous code returned false (preserve) as soon as reason != nil, so a
// small binary copy of a protected file was preserved and untrackedInfo
// stamped sha=SHA256Bytes(data), leaking a content-derived fingerprint of
// the protected bytes. Only when data == nil (symlink / non-regular /
// larger than maxUntrackedPatchBytes — no bytes returned, and untrackedInfo
// leaves sha="") does it preserve without a hash check.
func (s protectedDeletedContentSet) matchesUntracked(root, path string) bool {
	if s.unknown {
		// A protected tracked file was deleted/renamed/copied →
		// suppress (deny) this untracked file without reading its
		// content (it could be a copy of modified-then-deleted/renamed/
		// copied protected bytes).
		return true
	}
	if len(s.existingHashes) == 0 {
		// No protected files exist → no protected content to match →
		// preserve (full content-level correlation).
		return false
	}
	data, _, _, err := readUntrackedPatchData(root, path)
	if err != nil {
		// stat/read failure → cannot prove it is not a copy of protected
		// content → fail closed for this file.
		return true
	}
	if data == nil {
		// readUntrackedPatchData returned NO bytes: a symlink, a
		// non-regular file, or a file larger than maxUntrackedPatchBytes.
		// Such a file cannot be a verbatim byte-for-byte copy of a (small
		// text) protected file, and the patch path already omits these
		// (PatchIncluded=false, and untrackedInfo sets sha="" because
		// data==nil). Preserve it in the untracked list (path-level
		// correlation, no byte leak) rather than denying it — matches the
		// pre-round-6 behavior for symlinks/non-regular/large untracked
		// files.
		return false
	}
	// data != nil: readUntrackedPatchData returned the file's bytes (up to
	// maxUntrackedPatchBytes). This covers BOTH text files (reason==nil)
	// AND small BINARY files whose content contains a NUL byte (reason ==
	// binaryOrLarge, but data still holds the actual bytes). The binary
	// case is the D4/R16 round-8 fix: a small binary copy of a protected
	// file (e.g. a protected .env that happens to contain a NUL) must be
	// content-hash-matched against existingHashes BEFORE preserving — the
	// previous code returned false on reason!=nil and untrackedInfo then
	// stamped sha=SHA256Bytes(data), leaking a content-derived fingerprint
	// of the protected bytes into untracked-files.json / review.json.
	if s.existingHashes[security.SHA256Bytes(data)] {
		// Untracked file's content (text OR binary) matches a recoverable
		// version of an existing protected file → it is a copy of protected
		// content (including a MODIFIED protected source) → suppress.
		return true
	}
	// data != nil but no protected-content match: a legitimate binary or
	// text file. When reason != nil (binary) the patch path omits it
	// (PatchIncluded=false) BUT untrackedInfo still stamps sha — that sha
	// is of NON-protected content, so it is safe to emit (it cannot be
	// reversed to protected bytes). Preserve for correlation.
	return false
}

// protectedDeletedContent returns the protected-content decision set.
//
// First, as round 4: if ANY deleted/renamed/copied status-porcelain
// record touches a protected path → return {existingHashes: nil,
// unknown: true} (fail-closed). The pre-operation worktree content of a
// protected file is unrecoverable when unstaged modifications existed
// and undetectable after the operation, so there is no safe content set
// to build; we fail closed immediately on any protected delete OR
// protected rename/copy source.
//
// Else (no protected delete/rename/copy — all protected files REMAIN):
// round 6 builds existingHashes = the set of SHA256 hashes of all
// recoverable versions (workspace + HEAD + index) of all existing
// protected files (tracked + untracked-protected), so matchesUntracked
// and collectChanges can content-hash-match untracked files and A
// records against it. This closes the MODIFIED-source-copy leak (a copy
// of a protected file that was MODIFIED before being copied, while the
// source remains). Returns an empty (non-nil) existingHashes set when
// there are no protected files at all.
//
// D4 / R16 round 4: a protected file staged as a RENAME or COPY (an `R`
// or `C` porcelain record, NOT `D`) is just as dangerous as a delete —
// its bytes are moved/copied to a new path and can be further copied
// into an untracked file, and the pre-rename/copy worktree content is
// equally unrecoverable when unstaged modifications existed. So the
// fail-closed must trigger on protected R/C sources too, not just
// protected D records. For an R/C record, parseStatusPorcelainZ stores
// the SOURCE path at record.paths[0] and the DESTINATION at
// record.paths[1]; we check the source.
//
// D4 / R16 round 8: a protected TYPECHANGE (a `T` porcelain record — a
// tracked file whose type changed, e.g. regular file → symlink) is
// treated the same way. The pre-typechange worktree bytes (the modified
// content that could have been copied) are unrecoverable once the regular
// file is replaced, and a copy would only match the now-absent modified
// bytes — not the HEAD/index/symlink-target versions a content-hash check
// builds — so content-hash matching cannot be made safe. A `T` record
// carries a single path; we check it.
func protectedDeletedContent(root string, records []statusPorcelainRecord, protectedPaths []string) protectedDeletedContentSet {
	for _, record := range records {
		if record.deleted() {
			for _, candidate := range record.paths {
				path := reviewCleanPath(candidate)
				if path == "" || !security.IsProtectedPathWithConfig(path, protectedPaths) {
					continue
				}
				// A protected tracked file was deleted. Its
				// pre-deletion worktree content (the bytes that could
				// have been copied into an untracked file) is
				// unrecoverable when unstaged modifications existed,
				// and undetectable after deletion. Fail closed: deny
				// ALL non-path-protected untracked files.
				return protectedDeletedContentSet{existingHashes: nil, unknown: true}
			}
			continue
		}
		if record.renamedOrCopied() {
			// For an R/C record, parseStatusPorcelainZ stores the
			// SOURCE path at paths[0]. If the source is protected, the
			// protected bytes were moved/copied to a new path and could
			// be further copied into an untracked file; the
			// pre-rename/copy worktree content is unrecoverable when
			// unstaged modifications existed. Fail closed.
			if len(record.paths) == 0 {
				continue
			}
			path := reviewCleanPath(record.paths[0])
			if path == "" || !security.IsProtectedPathWithConfig(path, protectedPaths) {
				continue
			}
			return protectedDeletedContentSet{existingHashes: nil, unknown: true}
		}
		if record.typechange() {
			// D4/R16 round-8: a protected tracked file whose TYPE changed
			// (a `T` porcelain record — e.g. a regular file modified then
			// replaced by a symlink) is just as dangerous as a delete: the
			// pre-typechange WORKTREE content (the modified bytes that
			// could have been copied into an added/untracked file) is
			// unrecoverable once the regular file is gone, and a copy of it
			// would only match the now-absent modified bytes, not the
			// HEAD/index/symlink-target versions a content-hash check
			// builds. Fail closed so ALL untracked + ALL A/M records are
			// suppressed. A `T` record carries a single path (the
			// typechanged file); parseStatusPorcelainZ stores it at
			// paths[0].
			if len(record.paths) == 0 {
				continue
			}
			path := reviewCleanPath(record.paths[0])
			if path == "" || !security.IsProtectedPathWithConfig(path, protectedPaths) {
				continue
			}
			return protectedDeletedContentSet{existingHashes: nil, unknown: true}
		}
	}
	// No protected delete/rename/copy — all protected files REMAIN.
	// Build the existing-protected-content hash set so callers can
	// content-hash-match untracked files and A records against it.
	return protectedDeletedContentSet{existingHashes: existingProtectedContentHashes(root, protectedPaths), unknown: false}
}

// existingProtectedContentHashes builds the set of SHA256 content hashes of
// ALL recoverable versions of EVERY protected file that currently exists in
// the worktree (tracked OR untracked-protected). Mirrors the orchestrator's
// helper of the same name; replicated here because orchestrator and review
// are separate packages (each owns its own protected-path logic).
//
// D4 / R16 round 6 — closes the MODIFIED-source-copy P1 leak. See the
// orchestrator's existingProtectedContentHashes for the full rationale.
// In short: when a protected file is NOT deleted/renamed/copied-away (the
// source REMAINS), its content is recoverable from three versions —
// workspace (os.ReadFile), HEAD (`git show HEAD:<path>`), index
// (`git show :<path>`). A copy of the protected file (into an untracked
// file or an A record) will match ONE of these versions, so we union their
// SHA256 hashes and content-hash-match against the set.
//
// Candidate protected files are enumerated from THREE sources so a protected
// file is never missed:
//   - `git ls-files -z` (tracked);
//   - `git ls-files --others --exclude-standard -z` (untracked, NOT ignored);
//   - `git ls-files --others --ignored --exclude-standard -z` (IGNORED files).
//
// The third enumeration is essential: a protected file IGNORED by .gitignore
// (the common case — .env is typically gitignored) is OMITTED by the second
// enumeration, so without the third an ignored protected .env is never hashed
// into the set and a copy of it (staged or untracked) is wrongly treated as
// safe and emitted into changes.patch / review.json (D4/R16 round-7 fix A).
// Each enumerated path whose IsProtectedPath is true is a protected file.
// For each, all three versions are streamed into a sha256 hasher (bounded
// memory via io.Copy); read errors are skipped (a version may be absent).
//
// Residual risk (documented): a "filler copy" — an untracked/A file whose
// content is protected content + extra filler, so its hash does NOT match
// any protected version — is a deliberate content-obfuscation evasion this
// cannot detect when the source REMAINS. When the source is
// deleted/renamed/copied-away (unknown=true) the fail-closed branch
// suppresses ALL untracked + ALL A, covering even filler copies.
//
// Returns an empty (non-nil) set when no protected files exist.
func existingProtectedContentHashes(root string, protectedPaths []string) map[string]bool {
	set := map[string]bool{}
	// Enumerate candidate protected files: tracked + untracked-non-ignored +
	// ignored. The ignored enumeration is required so a gitignored protected
	// .env (which `--others --exclude-standard` omits) is still hashed.
	candidates := []string{}
	for _, args := range [][]string{
		{"ls-files", "-z"},
		{"ls-files", "--others", "--exclude-standard", "-z"},
		{"ls-files", "--others", "--ignored", "--exclude-standard", "-z"},
	} {
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).Output()
		if err != nil {
			continue
		}
		for _, p := range strings.Split(string(out), "\x00") {
			if p == "" {
				continue
			}
			candidates = append(candidates, p)
		}
	}
	for _, rel := range candidates {
		// D4/R16 round-9: honor configured protected_paths, not just the
		// built-in defaults, so a custom protected path such as
		// `secrets/**` is hashed into the set and a copy of it is suppressed.
		if !security.IsProtectedPathWithConfig(rel, protectedPaths) {
			continue
		}
		// (a) workspace version — the modified bytes a `cp` copies.
		if h, ok := reviewHashWorkspaceFile(filepath.Join(root, rel)); ok {
			set[h] = true
		}
		// (b) HEAD version — the committed bytes.
		if h, ok := reviewHashGitBlob(root, "HEAD:"+rel); ok {
			set[h] = true
		}
		// (c) index version — the staged bytes.
		if h, ok := reviewHashGitBlob(root, ":"+rel); ok {
			set[h] = true
		}
	}
	return set
}

// reviewHashWorkspaceFile streams a worktree file's content into a sha256
// hasher (bounded memory via io.Copy) and returns the hex hash. Returns
// ok=false on any error.
//
// D4/R16 round-8 fix: refuse to open non-regular files (FIFOs, devices) so
// a protected path that is a FIFO, a device, or a symlink to a never-ending
// source such as /dev/zero cannot block os.Open/io.Copy during review packet
// generation.
//
// D4/R16 round-9 fix: the round-8 Lstat guard also skipped symlinks, which
// dropped a protected path that is a symlink to a REGULAR secret file (e.g.
// `.env -> ../shared/env`) from existingHashes — a copy made through that
// symlink then failed the content-hash suppression and the secret leaked.
// We now Stat (follows symlinks): a symlink whose target is a regular file
// is hashed (the common symlinked-.env case); a symlink whose target is a
// FIFO/device/socket (e.g. -> /dev/zero) resolves to a non-regular mode and
// is skipped BEFORE opening (no block). A broken/looping symlink fails Stat
// → ok=false (skip). ok=false means "no hash added"; callers treat an absent
// hash as fail-closed, which is the safe outcome for a special file.
func reviewHashWorkspaceFile(path string) (string, bool) {
	st, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	// Skip non-regular targets (FIFO/device/socket, whether the path itself
	// or a symlink's target). os.Stat follows symlinks, so a symlink to a
	// regular secret file resolves to a regular mode and IS hashed below.
	if !st.Mode().IsRegular() {
		return "", false
	}
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", false
	}
	return hex.EncodeToString(h.Sum(nil)), true
}

// reviewHashGitBlob streams `git -C root show <spec>` output into a sha256
// hasher (bounded memory via io.Copy) and returns the hex hash. Returns
// ok=false on any error.
//
// D4/R16 round-7 fix B: a NON-ZERO exit from `git show` means the blob does
// not exist (e.g. an untracked/ignored protected file has no HEAD/index
// version). git writes NO stdout in that case, so io.Copy reads 0 bytes with
// copyErr=nil — previously the code ignored cmd.Wait()'s error and returned
// sha256("") as a VALID protected hash, which then matched any unrelated
// EMPTY added/untracked file and wrongly suppressed it. We now treat a
// non-nil Wait error (non-zero exit) as ok=false (absent blob): only add a
// hash when `git show` succeeded (exit 0).
func reviewHashGitBlob(root, spec string) (string, bool) {
	cmd := exec.Command("git", "-C", root, "show", spec)
	r, err := cmd.StdoutPipe()
	if err != nil {
		return "", false
	}
	if err := cmd.Start(); err != nil {
		return "", false
	}
	h := sha256.New()
	_, copyErr := io.Copy(h, r)
	waitErr := cmd.Wait()
	if copyErr != nil || waitErr != nil {
		return "", false // git show failed (no such blob, non-zero exit) → absent
	}
	return hex.EncodeToString(h.Sum(nil)), true
}

// matchesAddedTracked reports whether a tracked ADDED (A) file must be
// suppressed (denied) from the review packet's `changed` list. Mirrors
// matchesUntracked for the A-record path.
//   - When unknown (a protected tracked file was deleted/renamed/copied/
//     typechanged) it returns true — fail-closed (skip ALL A records).
//   - When not unknown and existingHashes is empty (no protected files
//     exist at all) it returns false — full correlation (A kept).
//   - When not unknown and existingHashes is non-empty, it content-hash-
//     matches the A file's WORKSPACE content against existingHashes: a
//     match means the A file is a copy of existing protected content
//     (including a MODIFIED protected source) → suppress; otherwise keep.
//     On read error it returns true (fail closed for that file).
func (s protectedDeletedContentSet) matchesAddedTracked(root, path string) bool {
	if s.unknown {
		return true
	}
	if len(s.existingHashes) == 0 {
		return false
	}
	h, ok := reviewHashWorkspaceFile(filepath.Join(root, path))
	if !ok {
		// Cannot read the A file's workspace bytes → cannot prove it
		// is safe → fail closed for this file.
		return true
	}
	return s.existingHashes[h]
}

// matchesModifiedTracked reports whether a tracked MODIFIED (M) file must
// be suppressed (denied) from the review packet's `changed` list.
//
// D4/R16 round-8 — modified-tracked-copy guard. A tracked non-protected
// file whose workspace content was overwritten with protected bytes (`cp
// .env config.txt`, config.txt already tracked → porcelain reports
// `M config.txt`) has workspace content matching an existing protected
// version's hash, so it is suppressed here. This is the source-REMAINS
// case the review finding describes.
//
// Unlike matchesAddedTracked, this does NOT fail-closed when unknown=true.
// When a protected file is deleted/renamed/copied/typechanged, existingHashes
// is nil (the pre-operation protected bytes are unrecoverable), so a
// content-hash check is impossible; but fail-closing ALL M records would
// drop unrelated in-progress modifications (e.g. app.txt old→new during a
// protected rename), breaking the user-approved trade-off codified by
// TestGenerateDoesNotReincludeProtectedRenameFromHandoff. So:
//   - When unknown → return false (keep the M record; the A/untracked
//     fail-closed already covers the copy-of-protected-byte risk for new
//     files).
//   - When not unknown and existingHashes is empty → false (no protected
//     content to match → keep).
//   - When not unknown and existingHashes is non-empty → content-hash-match
//     the M file's workspace content; suppress on a match. On read error
//     return true (fail closed for THIS file — cannot prove it is safe),
//     which is narrower than fail-closing all M records.
func (s protectedDeletedContentSet) matchesModifiedTracked(root, path string) bool {
	if s.unknown {
		return false
	}
	if len(s.existingHashes) == 0 {
		return false
	}
	h, ok := reviewHashWorkspaceFile(filepath.Join(root, path))
	if !ok {
		// Cannot read the M file's workspace bytes → cannot prove it
		// is safe → fail closed for THIS file only.
		return true
	}
	return s.existingHashes[h]
}

// matchesTypechangeTracked reports whether a tracked TYPECHANGED (T) file on
// a NON-protected path must be suppressed (denied) from the review packet's
// `changed` list.
//
// D4/R16 round-10 (codex finding F) — typechange-on-non-protected-path
// guard. A tracked non-protected file whose type changed (e.g. regular file
// → symlink) is reported by porcelain as `T config.txt`; gitx.DiffBinaryPaths
// emits the symlink TARGET text into changes.patch. A typechange whose
// target is copied from a protected file (`rm config.txt; ln -s "$(cat
// .env)" config.txt`) therefore leaks the protected bytes. (A typechange on
// a PROTECTED path is already fail-closed via
// protectedDeletedContent.unknown=true; this handles the non-protected path.)
//
// Like matchesModifiedTracked this does NOT blanket fail-closed when
// unknown=true (an unrelated typechange is preserved, matching the M
// trade-off). When unknown=false it reads the symlink target via os.Readlink
// (the target text is what `git diff` emits), hashes it, and suppresses on a
// match against existingHashes. A non-symlink typechange (e.g. symlink →
// regular) returns false (its content is the regular file's bytes, handled
// by the normal path; and a symlink→regular typechange's emitted content is
// the regular file's, which is safe unless it matches protected content —
// covered by reading the workspace content via reviewHashWorkspaceFile).
func (s protectedDeletedContentSet) matchesTypechangeTracked(root, path string) bool {
	if s.unknown {
		return false
	}
	if len(s.existingHashes) == 0 {
		return false
	}
	full := filepath.Join(root, path)
	// A typechange may be regular→symlink or symlink→regular (or other).
	// Read whatever workspace bytes `git diff` would emit: for a symlink,
	// that is the target text (os.Readlink); for a regular file, the file
	// content. Hash and match either against existingHashes.
	if target, err := os.Readlink(full); err == nil {
		// Workspace path is a symlink → emitted content is the target text.
		h := security.SHA256Bytes([]byte(target))
		if s.existingHashes[h] {
			return true
		}
		// The target text did not match; a symlink whose target is a path
		// (the common case) is safe to keep.
		return false
	}
	// Not a symlink (e.g. symlink→regular typechange): hash the workspace
	// file content and match. On read error fail closed for THIS file.
	h, ok := reviewHashWorkspaceFile(full)
	if !ok {
		return true
	}
	return s.existingHashes[h]
}

type statusPorcelainRecord struct {
	code  string
	paths []string
}

func (r statusPorcelainRecord) renamedOrCopied() bool {
	return len(r.code) >= 2 && (r.code[0] == 'R' || r.code[1] == 'R' || r.code[0] == 'C' || r.code[1] == 'C')
}

// typechange reports whether a porcelain XY code denotes a TYPECHANGE (T)
// — a tracked file whose type changed (e.g. regular file → symlink, or
// symlink → regular file). D4/R16 round-8: a protected typechange is
// treated as fail-closed (same as delete/rename/copy) because the
// pre-typechange worktree bytes are unrecoverable.
func (r statusPorcelainRecord) typechange() bool {
	return len(r.code) >= 2 && (r.code[0] == 'T' || r.code[1] == 'T')
}

// isAddedRecord reports whether a porcelain XY code denotes an ADDED
// tracked file (status A in either the staged-X or worktree-Y column),
// i.e. a file whose bytes are new to the index/worktree and would be
// emitted as a `new file` patch by git diff. `??` (untracked) is NOT an
// added tracked record and is handled separately by collectChanges.
func isAddedRecord(code string) bool {
	return len(code) >= 2 && (code[0] == 'A' || code[1] == 'A')
}

// isModifiedTrackedRecord reports whether a porcelain XY code denotes a
// MODIFIED tracked file (status M in either the staged-X or worktree-Y
// column). D4/R16 round-8: a tracked non-protected file whose workspace
// content is overwritten with protected bytes (e.g. `cp .env config.txt`
// where config.txt is already tracked) is reported by porcelain as
// `M config.txt`, NOT `A`, so the round-5/6 A-only protected-content
// check missed it and gitx.DiffBinaryPaths emitted the protected bytes
// into changes.patch. The content-hash check now covers M records too.
func isModifiedTrackedRecord(code string) bool {
	return len(code) >= 2 && (code[0] == 'M' || code[1] == 'M')
}

// protectedCopyDestinations runs a copy-aware diff
// (`git diff --name-status --find-renames --find-copies
// --find-copies-harder -z HEAD`, the same invocation the orchestrator
// uses in filteredTrackedDiffPathspecs) and returns the set of
// DESTINATION paths whose rename/copy SOURCE is a protected path. Such
// destinations are tracked files (porcelain reports them as `A`, since
// porcelain does not detect copies) whose bytes are a verbatim copy of a
// protected file and must be excluded from `changed` so their patch never
// reaches changes.patch / review.json.
//
// D4 / R16 round 5: porcelain detects renames (R) but NOT copies (C). A
// rename of a protected file is already denied by the per-record
// reviewSafePath check in collectChanges (the R record's source path is
// protected → safePaths becomes nil → denied[dst]=true). The GAP is the
// COPY case: porcelain reports `A <dst>` with no source, so the per-record
// check passes and the copied protected bytes would leak.
// --find-copies-harder closes this gap by reporting `C<score> <src> <dst>`
// (source first, destination second) when a new file is a verbatim (or
// near-verbatim) copy of an existing tracked blob — including copies whose
// source REMAINS (which porcelain and plain `--find-renames` never detect).
//
// The returned ok flag is false when the name-status enumeration fails or
// is malformed: collectChanges cannot prove no A record is a protected
// copy, so it fails closed (skips ALL tracked A records).
//
// Residual risk (documented): a copy with enough filler to fall below the
// similarity threshold is reported as `A` (not `C`), is absent from the
// returned set, and its content differs from the protected file — an
// undetected deliberate-evasion leak when the protected source remains.
// This matches the orchestrator's behavior. When a protected file is
// deleted/renamed/copied (protectedDeletedContent.unknown=true) the
// fail-closed skip of ALL A records covers even filler copies.
func protectedCopyDestinations(root string, protectedPaths []string) (map[string]bool, bool) {
	out, err := exec.Command("git", "-C", root, "diff", "--name-status", "--find-renames", "--find-copies", "--find-copies-harder", "-z", "HEAD").Output()
	if err != nil {
		return nil, false
	}
	destinations := map[string]bool{}
	if len(out) == 0 {
		return destinations, true
	}
	fields := strings.Split(string(out), "\x00")
	if len(fields) > 0 && fields[len(fields)-1] == "" {
		fields = fields[:len(fields)-1]
	}
	for i := 0; i < len(fields); {
		status := fields[i]
		i++
		pathCount := 1
		if strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C") {
			pathCount = 2
		}
		if i+pathCount > len(fields) {
			// Malformed name-status output → cannot prove safety → fail closed.
			return nil, false
		}
		paths := fields[i : i+pathCount]
		i += pathCount
		if !strings.HasPrefix(status, "R") && !strings.HasPrefix(status, "C") {
			continue
		}
		// paths[0] is the SOURCE, paths[1] is the DESTINATION. If the
		// source is protected, the destination is a copy/rename of a
		// protected file and must be skipped.
		src := reviewCleanPath(paths[0])
		if src == "" || !security.IsProtectedPathWithConfig(src, protectedPaths) {
			continue
		}
		dst := reviewCleanPath(paths[1])
		if dst != "" {
			destinations[dst] = true
		}
	}
	return destinations, true
}

func (r statusPorcelainRecord) deleted() bool {
	return len(r.code) >= 2 && (r.code[0] == 'D' || r.code[1] == 'D')
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
		lines := strings.SplitAfter(string(data), "\n")
		for i, line := range lines {
			if line == "" {
				continue
			}
			b.WriteByte('+')
			b.WriteString(strings.TrimSuffix(line, "\n"))
			b.WriteByte('\n')
			if i == len(lines)-1 && !strings.HasSuffix(line, "\n") {
				b.WriteString("\\ No newline at end of file\n")
			}
		}
	}
	return b.String()
}
func syntheticNumstat(root string, u []UntrackedInfo) string {
	var b strings.Builder
	for _, x := range u {
		if !x.PatchIncluded {
			continue
		}
		data, _, reason, err := readUntrackedPatchData(root, x.Path)
		if err != nil || reason != nil {
			continue
		}
		added := 0
		for _, line := range strings.SplitAfter(string(data), "\n") {
			if line != "" {
				added++
			}
		}
		b.WriteString(strconv.Itoa(added))
		b.WriteString("\t0\t")
		b.WriteString(x.Path)
		b.WriteByte('\n')
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
func reviewCleanPath(path string) string {
	path = filepath.ToSlash(filepath.Clean(path))
	if path == "." || strings.HasPrefix(path, "../") || path == ".." || filepath.IsAbs(path) {
		return ""
	}
	return path
}
func reviewSafePath(path string, protectedPaths []string) string {
	path = reviewCleanPath(path)
	if path == "" {
		return ""
	}
	// D4/R16 round-9: honor configured protected_paths, not just the
	// built-in defaults, so a custom protected path such as `secrets/**`
	// is excluded from changes.patch / changed-files / diffstat.
	if security.IsProtectedPathWithConfig(path, protectedPaths) {
		return ""
	}
	return path
}
func reviewDiffPaths(changed []string, protectedPaths []string) []string {
	out := []string{}
	for _, path := range changed {
		path = reviewSafePath(path, protectedPaths)
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
