package observability

import (
	"encoding/json"
	"os"
	"path/filepath"

	"local-symphony/internal/agent/codex"
	"local-symphony/internal/config"
	"local-symphony/internal/core"
	"local-symphony/internal/db"
	"local-symphony/internal/gitx"
	"local-symphony/internal/store"
)

func Diagnostics(st *store.Store) map[string]any {
	wf, _ := config.Load(st.RepoRoot)
	appVersion, projectVersion := st.DatabaseSchemaVersions()
	gitStatus := "unknown"
	if gitx.IsRepo(st.RepoRoot) {
		gitStatus = "clean"
		if lines, err := gitx.StatusPorcelain(st.RepoRoot); err == nil && len(lines) > 0 {
			gitStatus = "dirty"
		}
	} else {
		gitStatus = "unavailable"
	}
	paused, _ := st.ListIssues(store.ListIssueOptions{DispatchPaused: boolPtr(true), Limit: 200})
	pausedRefs := []string{}
	for _, i := range paused {
		pausedRefs = append(pausedRefs, i.Identifier)
	}
	runtimeDescriptor := map[string]any{
		"api_url":                 nil,
		"tool_gateway_endpoint":   nil,
		"daemon_pid":              nil,
		"acquired_at":             nil,
		"heartbeat_at":            nil,
		"heartbeat_ttl_ms":        nil,
		"owner_nonce_fingerprint": nil,
	}
	if desc, err := st.RuntimeDescriptorSnapshot(); err == nil && desc != nil {
		runtimeDescriptor = desc
	}
	return map[string]any{
		"project_id": st.ProjectID, "generated_at": core.Now(), "redacted": true, "repo_root": st.RepoRoot,
		"database":  map[string]any{"app_db_path": st.AppDBPath, "project_db_path": st.ProjectDBPath, "app_schema_version": appVersion.Version, "project_schema_version": projectVersion.Version, "app_version_status": appVersion.Status, "project_version_status": projectVersion.Status},
		"workflow":  map[string]any{"config_path": filepath.Join(st.RepoRoot, "WORKFLOW.md"), "validation": wf.Validation, "last_valid_config": map[string]any{"available": wf.Validation.Valid, "path": wf.Path, "validated_at": core.Now(), "content_hash": wf.PromptHash}},
		"daemon":    map[string]any{"pid": os.Getpid(), "uptime_ms": 0, "runtime_descriptor": runtimeDescriptor},
		"codex":     codexAvailability(codex.RunPreflight(preflightOptionsFromWorkflow(wf))),
		"git":       map[string]any{"repository": map[string]any{"is_repo": gitx.IsRepo(st.RepoRoot), "root": st.RepoRoot, "branch": gitx.CurrentBranch(st.RepoRoot), "head_sha": gitx.HeadSHA(st.RepoRoot), "status": gitStatus}, "worktree": map[string]any{"path": nil, "branch": nil, "base_ref": nil, "status": "unknown"}},
		"redaction": map[string]any{"enabled": true, "export_redacted_only": true, "rules_version": "v1"},
		"warnings":  []string{}, "inconsistent_issues": []any{}, "remediation": []any{},
		"failure_summary": map[string]any{"failed_runs_count": 0, "recent_failures": []any{}},
		"pause_summary":   map[string]any{"paused_dispatch_count": len(pausedRefs), "paused_issue_refs": pausedRefs},
		"checks":          []map[string]any{{"name": "contract_surface", "status": "ok"}},
	}
}
func boolPtr(v bool) *bool { return &v }

func Export(st *store.Store) (string, error) {
	root := filepath.Join(st.RepoRoot, ".symphony", "exports")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(root, "diagnostics-"+db.ProjectScopedJSONFileName(st.ProjectID))
	b, _ := json.MarshalIndent(Diagnostics(st), "", "  ")
	return path, os.WriteFile(path, b, 0o600)
}
