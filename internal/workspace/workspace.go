package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"local-symphony/internal/config"
	"local-symphony/internal/core"
	"local-symphony/internal/gitx"
	"local-symphony/internal/store"
)

type Manager struct {
	Store  *store.Store
	Config config.EffectiveConfig
}

func (m Manager) Prepare(run *core.RunAttempt, issue *core.Issue) (*core.WorkspaceSummary, error) {
	if issue.Workspace != nil && issue.Workspace.Status == "prepared" {
		_ = m.Store.SetRunWorkspace(run.ID, issue.Workspace.ID, issue.Workspace.BranchName, issue.Workspace.BaseRefConfig, issue.Workspace.BaseRef, issue.Workspace.BaseSHA)
		return issue.Workspace, nil
	}
	root := m.Config.Workspace.Root
	if root == "" {
		root = filepath.Join(os.TempDir(), "symphony-workspaces")
	}
	projectRoot := filepath.Join(root, m.Store.ProjectID)
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		return nil, err
	}
	branch := branchName(m.Config.Git.BranchPrefix, issue.Identifier, run.ID)
	path := filepath.Join(projectRoot, issue.Identifier)
	baseRefConfig := m.Config.Git.BaseRef
	if baseRefConfig == "" {
		baseRefConfig = "auto"
	}
	baseRef := baseRefConfig
	if baseRefConfig == "auto" {
		baseRef = gitx.CurrentBranch(m.Store.RepoRoot)
		if baseRef == "" {
			baseRef = "HEAD"
		}
	}
	baseSHA := gitx.RefSHA(m.Store.RepoRoot, baseRef)
	if gitx.IsRepo(m.Store.RepoRoot) && gitx.HasHEAD(m.Store.RepoRoot) && baseSHA == "0000000000000000000000000000000000000000" {
		return nil, fmt.Errorf("git base ref %q could not be resolved", baseRef)
	}
	if err := gitx.WorktreeAdd(m.Store.RepoRoot, path, branch, baseRef); err != nil {
		return nil, err
	}
	wsID, err := m.Store.CreateOrUpdateWorkspace(issue.ID, path, branch, baseRefConfig, baseRef, baseSHA)
	if err != nil {
		return nil, err
	}
	if err := m.Store.SetRunWorkspace(run.ID, wsID, branch, baseRefConfig, baseRef, baseSHA); err != nil {
		return nil, err
	}
	return &core.WorkspaceSummary{ID: wsID, Path: path, BranchName: branch, BaseRef: baseRef, BaseRefConfig: baseRefConfig, BaseSHA: baseSHA, Status: "prepared"}, nil
}

func branchName(prefix, ident, runID string) string {
	if prefix == "" {
		prefix = "symphony"
	}
	slug := strings.ToLower(ident)
	slug = regexp.MustCompile(`[^a-z0-9-]+`).ReplaceAllString(slug, "-")
	suffix := runID
	if len(suffix) > 12 {
		suffix = suffix[len(suffix)-12:]
	}
	return fmt.Sprintf("%s/%s-%s", prefix, strings.Trim(slug, "-"), suffix)
}
