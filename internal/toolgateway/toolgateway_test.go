package toolgateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"local-symphony/internal/core"
	"local-symphony/internal/security"
	"local-symphony/internal/store"
)

func TestHTTPClientCallSendsCurrentWorkingDirectoryHeader(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	seenCWD := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenCWD = r.Header.Get("X-Symphony-Cwd")
		_ = json.NewEncoder(w).Encode(Response{Success: true, Tool: "issue.get"})
	}))
	t.Cleanup(server.Close)

	resp := HTTPClientCall(server.URL, "token", Request{Tool: "issue.get"})

	if !resp.Success {
		t.Fatalf("HTTPClientCall success = false, error = %#v", resp.Error)
	}
	if seenCWD != cwd {
		t.Fatalf("X-Symphony-Cwd = %q, want %q", seenCWD, cwd)
	}
}

func TestArtifactAttachReturnsFailureAndDoesNotInsertArtifactWhenWriteFails(t *testing.T) {
	st := newGatewayTestStore(t)
	issue, run, workspace := prepareGatewayRun(t, st)
	sourcePath := filepath.Join(workspace, "artifact.txt")
	if err := os.WriteFile(sourcePath, []byte("artifact\n"), 0o644); err != nil {
		t.Fatalf("write source artifact: %v", err)
	}
	conflictPath := filepath.Join(st.RepoRoot, ".symphony", "artifacts", issue.Identifier, run.ID, "agent", "artifact.txt")
	if err := os.MkdirAll(conflictPath, 0o755); err != nil {
		t.Fatalf("create conflicting artifact destination: %v", err)
	}
	token, err := NewTokenForRun(st, run, workspace)
	if err != nil {
		t.Fatalf("NewTokenForRun: %v", err)
	}

	resp := (Gateway{Store: st}).Call(token, workspace, Request{
		Tool: "artifact.attach",
		Input: map[string]any{
			"path": "artifact.txt",
			"kind": "agent_file",
		},
	})

	if resp.Success {
		t.Fatalf("artifact.attach success = true, want false")
	}
	if resp.Error == nil || resp.Error.Code != string(core.ErrToolGatewayFailed) {
		t.Fatalf("artifact.attach error = %#v, want %s", resp.Error, core.ErrToolGatewayFailed)
	}
	assertArtifactCount(t, st, run.ID, 0)
}

func TestArtifactAttachUsesTokenArtifactMaxBytes(t *testing.T) {
	st := newGatewayTestStore(t)
	_, run, workspace := prepareGatewayRun(t, st)
	if err := os.WriteFile(filepath.Join(workspace, "artifact.txt"), []byte("ab"), 0o644); err != nil {
		t.Fatalf("write source artifact: %v", err)
	}
	token := security.NewToken()
	expires := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339Nano)
	if _, err := st.CreateToolToken(run.ID, security.HashToken(token), map[string]any{
		"run_id":             run.ID,
		"issue_id":           run.IssueID,
		"workspace_path":     workspace,
		"tools":              Registry,
		"artifact_max_bytes": int64(1),
	}, expires); err != nil {
		t.Fatalf("CreateToolToken: %v", err)
	}

	resp := (Gateway{Store: st}).Call(token, workspace, Request{
		Tool:  "artifact.attach",
		Input: map[string]any{"path": "artifact.txt"},
	})

	if resp.Success {
		t.Fatal("artifact.attach success = true, want max size failure")
	}
	if resp.Error == nil || resp.Error.Code != string(core.ErrInvalidRequest) {
		t.Fatalf("artifact.attach error = %#v, want %s", resp.Error, core.ErrInvalidRequest)
	}
	assertArtifactCount(t, st, run.ID, 0)

	defaultToken, err := NewTokenForRun(st, run, workspace)
	if err != nil {
		t.Fatalf("NewTokenForRun: %v", err)
	}
	resp = (Gateway{Store: st}).Call(defaultToken, workspace, Request{
		Tool:  "artifact.attach",
		Input: map[string]any{"path": "artifact.txt"},
	})
	if !resp.Success {
		t.Fatalf("artifact.attach with default token failed: %#v", resp.Error)
	}
	assertArtifactCount(t, st, run.ID, 1)
}

func TestArtifactAttachRejectsOversizeUnreadableFileBeforeReading(t *testing.T) {
	st := newGatewayTestStore(t)
	issue, run, workspace := prepareGatewayRun(t, st)
	sourcePath := filepath.Join(workspace, "large.bin")
	if err := os.WriteFile(sourcePath, []byte("oversize artifact\n"), 0o644); err != nil {
		t.Fatalf("write source artifact: %v", err)
	}
	if err := os.Chmod(sourcePath, 0o000); err != nil {
		t.Fatalf("chmod source artifact: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(sourcePath, 0o644) })
	if _, err := os.ReadFile(sourcePath); err == nil {
		t.Skip("chmod did not make source artifact unreadable in this environment")
	}
	token := security.NewToken()
	expires := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339Nano)
	if _, err := st.CreateToolToken(run.ID, security.HashToken(token), map[string]any{
		"run_id":             run.ID,
		"issue_id":           run.IssueID,
		"workspace_path":     workspace,
		"tools":              Registry,
		"artifact_max_bytes": int64(1),
	}, expires); err != nil {
		t.Fatalf("CreateToolToken: %v", err)
	}

	resp := (Gateway{Store: st}).Call(token, workspace, Request{
		Tool:  "artifact.attach",
		Input: map[string]any{"path": "large.bin"},
	})

	if resp.Success {
		t.Fatal("artifact.attach success = true, want max size failure")
	}
	if resp.Error == nil || resp.Error.Code != string(core.ErrInvalidRequest) {
		t.Fatalf("artifact.attach error = %#v, want %s", resp.Error, core.ErrInvalidRequest)
	}
	if resp.Error.Message != "artifact exceeds max size" {
		t.Fatalf("artifact.attach message = %q, want max size error before reading", resp.Error.Message)
	}
	storedPath := filepath.Join(st.RepoRoot, ".symphony", "artifacts", issue.Identifier, run.ID, "agent", "large.bin")
	if _, err := os.Stat(storedPath); !os.IsNotExist(err) {
		t.Fatalf("stored artifact exists after max size failure, stat err = %v", err)
	}
	assertArtifactCount(t, st, run.ID, 0)
}

func TestArtifactAttachDefaultsKindToAgentFile(t *testing.T) {
	st := newGatewayTestStore(t)
	_, run, workspace := prepareGatewayRun(t, st)
	if err := os.WriteFile(filepath.Join(workspace, "artifact.txt"), []byte("artifact\n"), 0o644); err != nil {
		t.Fatalf("write source artifact: %v", err)
	}
	token, err := NewTokenForRun(st, run, workspace)
	if err != nil {
		t.Fatalf("NewTokenForRun: %v", err)
	}

	resp := (Gateway{Store: st}).Call(token, workspace, Request{
		Tool:  "artifact.attach",
		Input: map[string]any{"path": "artifact.txt"},
	})

	if !resp.Success {
		t.Fatalf("artifact.attach failed: %#v", resp.Error)
	}
	row, err := st.Project.QueryOne(`SELECT kind FROM artifacts WHERE run_id=?`, run.ID)
	if err != nil {
		t.Fatalf("get artifact: %v", err)
	}
	if got := row["kind"].String(); got != "agent_file" {
		t.Fatalf("artifact kind = %q, want agent_file", got)
	}
}

func TestRequiredStringInputsRejectMissingOrBlank(t *testing.T) {
	tests := []struct {
		name string
		req  Request
	}{
		{
			name: "issue.comment missing body",
			req:  Request{Tool: "issue.comment", Input: map[string]any{}},
		},
		{
			name: "issue.block missing reason",
			req:  Request{Tool: "issue.block", Input: map[string]any{}},
		},
		{
			name: "followup.create missing title",
			req:  Request{Tool: "followup.create", Input: map[string]any{}},
		},
		{
			name: "handoff.submit missing summary",
			req:  Request{Tool: "handoff.submit", Input: map[string]any{}},
		},
		{
			name: "artifact.attach blank path",
			req:  Request{Tool: "artifact.attach", Input: map[string]any{"path": " \t\n"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := newGatewayTestStore(t)
			_, run, workspace := prepareGatewayRun(t, st)
			token, err := NewTokenForRun(st, run, workspace)
			if err != nil {
				t.Fatalf("NewTokenForRun: %v", err)
			}

			resp := (Gateway{Store: st}).Call(token, workspace, tt.req)

			if resp.Success {
				t.Fatalf("%s success = true, want false", tt.req.Tool)
			}
			if resp.Error == nil || resp.Error.Code != string(core.ErrInvalidRequest) {
				t.Fatalf("%s error = %#v, want %s", tt.req.Tool, resp.Error, core.ErrInvalidRequest)
			}
			assertArtifactCount(t, st, run.ID, 0)
		})
	}
}

func TestFollowupCreateRejectsInvalidPriorityWithoutCreatingIssue(t *testing.T) {
	tests := []struct {
		name     string
		priority any
	}{
		{name: "non-integer float", priority: float64(2.5)},
		{name: "non-integer json number", priority: json.Number("2.5")},
		{name: "non-number", priority: "high"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := newGatewayTestStore(t)
			_, run, workspace := prepareGatewayRun(t, st)
			token, err := NewTokenForRun(st, run, workspace)
			if err != nil {
				t.Fatalf("NewTokenForRun: %v", err)
			}

			resp := (Gateway{Store: st}).Call(token, workspace, Request{
				Tool: "followup.create",
				Input: map[string]any{
					"title":    "invalid followup",
					"priority": tt.priority,
				},
			})

			if resp.Success {
				t.Fatal("followup.create success = true, want invalid priority failure")
			}
			if resp.Error == nil || resp.Error.Code != string(core.ErrInvalidRequest) {
				t.Fatalf("followup.create error = %#v, want %s", resp.Error, core.ErrInvalidRequest)
			}
			assertIssueCount(t, st, 1)
		})
	}
}

func TestFollowupCreateRollsBackIssueWhenRelationInsertFails(t *testing.T) {
	st := newGatewayTestStore(t)
	_, run, workspace := prepareGatewayRun(t, st)
	if err := st.Project.Exec(`CREATE TRIGGER fail_followup_relation BEFORE INSERT ON issue_relations WHEN NEW.relation_type='followup_of' BEGIN SELECT RAISE(ABORT, 'followup relation failed'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	token, err := NewTokenForRun(st, run, workspace)
	if err != nil {
		t.Fatalf("NewTokenForRun: %v", err)
	}

	resp := (Gateway{Store: st}).Call(token, workspace, Request{
		Tool: "followup.create",
		Input: map[string]any{
			"title":    "relation failure followup",
			"priority": float64(3),
		},
	})

	if resp.Success {
		t.Fatal("followup.create success = true, want relation failure")
	}
	if resp.Error == nil {
		t.Fatal("followup.create error = nil, want error")
	}
	assertIssueCount(t, st, 1)
	assertFollowupRelationCount(t, st, 0)
}

func TestArtifactAttachPreservesRelativeDirectoriesForSameBaseName(t *testing.T) {
	st := newGatewayTestStore(t)
	issue, run, workspace := prepareGatewayRun(t, st)
	for _, rel := range []string{"a/report.txt", "b/report.txt"} {
		path := filepath.Join(workspace, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(rel+"\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	token, err := NewTokenForRun(st, run, workspace)
	if err != nil {
		t.Fatalf("NewTokenForRun: %v", err)
	}
	gw := Gateway{Store: st}

	for _, rel := range []string{"a/report.txt", "b/report.txt"} {
		resp := gw.Call(token, workspace, Request{Tool: "artifact.attach", Input: map[string]any{"path": rel}})
		if !resp.Success {
			t.Fatalf("artifact.attach %s failed: %#v", rel, resp.Error)
		}
	}

	rows, err := st.Project.Query(`SELECT path FROM artifacts WHERE run_id=? ORDER BY path`, run.ID)
	if err != nil {
		t.Fatalf("list artifacts: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("artifact rows = %d, want 2", len(rows))
	}
	for _, rel := range []string{"a/report.txt", "b/report.txt"} {
		wantPath := filepath.Join(st.RepoRoot, ".symphony", "artifacts", issue.Identifier, run.ID, "agent", rel)
		b, err := os.ReadFile(wantPath)
		if err != nil {
			t.Fatalf("read artifact %s: %v", rel, err)
		}
		if string(b) != rel+"\n" {
			t.Fatalf("artifact %s content = %q, want %q", rel, string(b), rel+"\n")
		}
	}
}

func TestArtifactAttachRejectsDuplicateRelativePathWithoutOverwriting(t *testing.T) {
	st := newGatewayTestStore(t)
	_, run, workspace := prepareGatewayRun(t, st)
	path := filepath.Join(workspace, "report.txt")
	if err := os.WriteFile(path, []byte("first\n"), 0o644); err != nil {
		t.Fatalf("write first artifact: %v", err)
	}
	token, err := NewTokenForRun(st, run, workspace)
	if err != nil {
		t.Fatalf("NewTokenForRun: %v", err)
	}
	gw := Gateway{Store: st}
	first := gw.Call(token, workspace, Request{Tool: "artifact.attach", Input: map[string]any{"path": "report.txt"}})
	if !first.Success {
		t.Fatalf("first artifact.attach failed: %#v", first.Error)
	}
	if err := os.WriteFile(path, []byte("second\n"), 0o644); err != nil {
		t.Fatalf("rewrite workspace artifact: %v", err)
	}

	second := gw.Call(token, workspace, Request{Tool: "artifact.attach", Input: map[string]any{"path": "report.txt"}})

	if second.Success {
		t.Fatal("second artifact.attach success = true, want duplicate path failure")
	}
	storedPath := filepath.Join(st.RepoRoot, ".symphony", "artifacts", "TST-1", run.ID, "agent", "report.txt")
	b, err := os.ReadFile(storedPath)
	if err != nil {
		t.Fatalf("read stored artifact: %v", err)
	}
	if string(b) != "first\n" {
		t.Fatalf("stored artifact content = %q, want first content", string(b))
	}
	assertArtifactCount(t, st, run.ID, 1)
}

func TestArtifactAttachCleansUpFileWhenMetadataInsertFails(t *testing.T) {
	st := newGatewayTestStore(t)
	issue, run, workspace := prepareGatewayRun(t, st)
	path := filepath.Join(workspace, "report.txt")
	if err := os.WriteFile(path, []byte("artifact\n"), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	if err := st.Project.Exec(`CREATE TRIGGER fail_artifact_insert BEFORE INSERT ON artifacts BEGIN SELECT RAISE(ABORT, 'artifact metadata failed'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	token, err := NewTokenForRun(st, run, workspace)
	if err != nil {
		t.Fatalf("NewTokenForRun: %v", err)
	}
	gw := Gateway{Store: st}

	first := gw.Call(token, workspace, Request{Tool: "artifact.attach", Input: map[string]any{"path": "report.txt"}})
	if first.Success {
		t.Fatal("artifact.attach success = true, want metadata failure")
	}
	storedPath := filepath.Join(st.RepoRoot, ".symphony", "artifacts", issue.Identifier, run.ID, "agent", "report.txt")
	if _, err := os.Stat(storedPath); !os.IsNotExist(err) {
		t.Fatalf("stored artifact exists after metadata failure, stat err = %v", err)
	}
	assertArtifactCount(t, st, run.ID, 0)

	if err := st.Project.Exec(`DROP TRIGGER fail_artifact_insert`); err != nil {
		t.Fatalf("drop trigger: %v", err)
	}
	second := gw.Call(token, workspace, Request{Tool: "artifact.attach", Input: map[string]any{"path": "report.txt"}})
	if !second.Success {
		t.Fatalf("retry artifact.attach failed: %#v", second.Error)
	}
	assertArtifactCount(t, st, run.ID, 1)
}

func TestIssueBlockReturnsErrorWhenCommentWriteFails(t *testing.T) {
	st := newGatewayTestStore(t)
	_, run, workspace := prepareGatewayRun(t, st)
	if err := st.Project.Exec(`CREATE TRIGGER fail_agent_block_comment BEFORE INSERT ON issue_comments WHEN NEW.body LIKE 'Blocked by agent:%' BEGIN SELECT RAISE(ABORT, 'comment write failed'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	token, err := NewTokenForRun(st, run, workspace)
	if err != nil {
		t.Fatalf("NewTokenForRun: %v", err)
	}

	resp := (Gateway{Store: st}).Call(token, workspace, Request{
		Tool:  "issue.block",
		Input: map[string]any{"reason": "blocked by test"},
	})

	if resp.Success {
		t.Fatal("issue.block success = true, want false")
	}
	if resp.Error == nil {
		t.Fatal("issue.block error = nil, want error")
	}
	gotRun, err := st.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if gotRun.Status != core.RunRunning {
		t.Fatalf("run status = %s, want %s", gotRun.Status, core.RunRunning)
	}
}

func TestIssueBlockRevokesRunToolToken(t *testing.T) {
	st := newGatewayTestStore(t)
	_, run, workspace := prepareGatewayRun(t, st)
	token, err := NewTokenForRun(st, run, workspace)
	if err != nil {
		t.Fatalf("NewTokenForRun: %v", err)
	}

	resp := (Gateway{Store: st}).Call(token, workspace, Request{
		Tool:  "issue.block",
		Input: map[string]any{"reason": "blocked by test"},
	})

	if !resp.Success {
		t.Fatalf("issue.block failed: %#v", resp.Error)
	}
	row, err := st.Project.QueryOne(`SELECT revoked_at FROM run_tool_tokens WHERE run_id=?`, run.ID)
	if err != nil {
		t.Fatalf("get tool token: %v", err)
	}
	if row["revoked_at"].String() == "" {
		t.Fatal("tool token revoked_at is empty, want revoked timestamp")
	}
}

func TestIssueBlockDoesNotCancelRunWhenBlockingIssueFails(t *testing.T) {
	st := newGatewayTestStore(t)
	issue, run, workspace := prepareGatewayRun(t, st)
	if err := st.Project.Exec(`CREATE TRIGGER fail_agent_block_transition BEFORE UPDATE OF state ON issues WHEN NEW.state='Blocked' BEGIN SELECT RAISE(ABORT, 'block transition failed'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	token, err := NewTokenForRun(st, run, workspace)
	if err != nil {
		t.Fatalf("NewTokenForRun: %v", err)
	}

	resp := (Gateway{Store: st}).Call(token, workspace, Request{
		Tool:  "issue.block",
		Input: map[string]any{"reason": "blocked by test"},
	})

	if resp.Success {
		t.Fatal("issue.block success = true, want false")
	}
	gotRun, err := st.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if gotRun.Status != core.RunRunning {
		t.Fatalf("run status = %s, want %s", gotRun.Status, core.RunRunning)
	}
	gotIssue, err := st.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if gotIssue.State != core.StateWorking {
		t.Fatalf("issue state = %s, want %s", gotIssue.State, core.StateWorking)
	}
}

func newGatewayTestStore(t *testing.T) *store.Store {
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

func prepareGatewayRun(t *testing.T, st *store.Store) (*core.Issue, *core.RunAttempt, string) {
	t.Helper()
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	issue, err := st.CreateIssue(store.CreateIssueInput{
		Title:              "Artifact attach write failure",
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
	wsID, err := st.CreateOrUpdateWorkspace(issue.ID, workspace, "gateway-test", "auto", "main", "base-sha")
	if err != nil {
		t.Fatalf("CreateOrUpdateWorkspace: %v", err)
	}
	if err := st.SetRunWorkspace(run.ID, wsID, "gateway-test", "auto", "main", "base-sha"); err != nil {
		t.Fatalf("SetRunWorkspace: %v", err)
	}
	if err := st.UpdateRunStatus(run.ID, core.RunRunning, nil); err != nil {
		t.Fatalf("UpdateRunStatus: %v", err)
	}
	run, err = st.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	issue, err = st.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	return issue, run, workspace
}

func assertArtifactCount(t *testing.T, st *store.Store, runID string, want int) {
	t.Helper()
	row, err := st.Project.QueryOne(`SELECT COUNT(*) AS c FROM artifacts WHERE run_id=?`, runID)
	if err != nil {
		t.Fatalf("count artifacts: %v", err)
	}
	if got := row["c"].Int(); got != want {
		t.Fatalf("artifact count = %d, want %d", got, want)
	}
}

func assertIssueCount(t *testing.T, st *store.Store, want int) {
	t.Helper()
	row, err := st.Project.QueryOne(`SELECT COUNT(*) AS c FROM issues`)
	if err != nil {
		t.Fatalf("count issues: %v", err)
	}
	if got := row["c"].Int(); got != want {
		t.Fatalf("issue count = %d, want %d", got, want)
	}
}

func assertFollowupRelationCount(t *testing.T, st *store.Store, want int) {
	t.Helper()
	row, err := st.Project.QueryOne(`SELECT COUNT(*) AS c FROM issue_relations WHERE relation_type='followup_of'`)
	if err != nil {
		t.Fatalf("count followup relations: %v", err)
	}
	if got := row["c"].Int(); got != want {
		t.Fatalf("followup relation count = %d, want %d", got, want)
	}
}
