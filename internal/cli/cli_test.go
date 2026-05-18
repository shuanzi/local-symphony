package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"local-symphony/internal/app"
	"local-symphony/internal/core"
	"local-symphony/internal/security"
	"local-symphony/internal/store"
)

func TestServeOptionsFromArgsParsesAddr(t *testing.T) {
	opts, err := serveOptionsFromArgs([]string{"--project", ".", "--addr", "127.0.0.1:3777", "--no-open"})
	if err != nil {
		t.Fatalf("serveOptionsFromArgs returned error: %v", err)
	}
	if opts.Host != "127.0.0.1" {
		t.Fatalf("Host = %q, want 127.0.0.1", opts.Host)
	}
	if opts.Port != 3777 {
		t.Fatalf("Port = %d, want 3777", opts.Port)
	}
	if !opts.NoOpen {
		t.Fatalf("NoOpen = false, want true")
	}
}

func TestServeOptionsFromArgsKeepsHostPort(t *testing.T) {
	opts, err := serveOptionsFromArgs([]string{"--host", "localhost", "--port", "3888"})
	if err != nil {
		t.Fatalf("serveOptionsFromArgs returned error: %v", err)
	}
	if opts.Host != "localhost" || opts.Port != 3888 {
		t.Fatalf("opts = %#v, want localhost:3888", opts)
	}
}

func TestServeOptionsFromArgsRejectsNonNumericPort(t *testing.T) {
	_, err := serveOptionsFromArgs([]string{"--port", "abc"})
	if err == nil {
		t.Fatal("serveOptionsFromArgs succeeded, want invalid_request")
	}
	if got := core.AsAPIError(err).Code; got != core.ErrInvalidRequest {
		t.Fatalf("error code = %s, want %s", got, core.ErrInvalidRequest)
	}
}

func TestStatusReturnsErrorWhenQueriesFail(t *testing.T) {
	tests := []struct {
		name      string
		dropTable string
	}{
		{name: "issues query", dropTable: "issues"},
		{name: "runs query", dropTable: "run_attempts"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			st, err := store.InitProject(dir, "APP")
			if err != nil {
				t.Fatalf("InitProject: %v", err)
			}
			if err := st.Project.Exec("DROP TABLE " + tt.dropTable); err != nil {
				t.Fatalf("drop %s: %v", tt.dropTable, err)
			}
			st.Close()

			code := Main([]string{"status", "--project", dir})
			if code != core.ExitCodeForError(core.ErrInternal) {
				t.Fatalf("status exit code = %d, want %d", code, core.ExitCodeForError(core.ErrInternal))
			}
		})
	}
}

func TestIssueCreateRejectsInvalidPriority(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "non integer", args: []string{"--priority", "abc"}},
		{name: "fractional", args: []string{"--priority", "2.5"}},
		{name: "empty equals", args: []string{"--priority="}},
		{name: "missing value", args: []string{"--priority"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			st, err := store.InitProject(dir, "APP")
			if err != nil {
				t.Fatalf("InitProject: %v", err)
			}
			st.Close()

			args := []string{"issue", "create", "--project", dir, "--title", "Invalid priority"}
			args = append(args, tt.args...)
			code := Main(args)
			if code != core.ExitCodeForError(core.ErrInvalidRequest) {
				t.Fatalf("issue create exit code = %d, want %d", code, core.ExitCodeForError(core.ErrInvalidRequest))
			}

			st, err = store.Open(dir)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer st.Close()
			issues, err := st.ListIssues(store.ListIssueOptions{Limit: 10})
			if err != nil {
				t.Fatalf("ListIssues: %v", err)
			}
			if len(issues) != 0 {
				t.Fatalf("created %d issues with invalid priority, want 0", len(issues))
			}
		})
	}
}

func TestIssueCreateDefaultsPriorityWhenFlagMissing(t *testing.T) {
	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	st.Close()

	code := Main([]string{"issue", "create", "--project", dir, "--title", "Default priority"})
	if code != 0 {
		t.Fatalf("issue create exit code = %d, want 0", code)
	}

	st, err = store.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	issues, err := st.ListIssues(store.ListIssueOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("created %d issues, want 1", len(issues))
	}
	if issues[0].Priority != 3 {
		t.Fatalf("priority = %d, want default 3", issues[0].Priority)
	}
}

func TestRunAcceptsCustomIssuePrefix(t *testing.T) {
	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	issue, err := st.CreateIssue(store.CreateIssueInput{
		Title:              "Custom prefix",
		Description:        "desc",
		AcceptanceCriteria: []string{"done"},
		Priority:           3,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if issue.Identifier != "APP-1" {
		t.Fatalf("identifier = %q, want APP-1", issue.Identifier)
	}
	if _, err := st.TransitionIssue(issue.Identifier, core.StateReady, "", ""); err != nil {
		t.Fatalf("TransitionIssue: %v", err)
	}
	st.Close()
	t.Setenv("SYMPHONY_FAKE_RUNNER_OUTCOME", "hold")

	if code := Main([]string{"run", "APP-1", "--project", dir}); code != 0 {
		t.Fatalf("run APP-1 exit code = %d, want 0", code)
	}
}

func TestOpenDescriptorMintsDashboardOpenToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	cliToken := security.NewToken()
	if err := os.MkdirAll(filepath.Join(home, ".symphony"), 0o700); err != nil {
		t.Fatalf("mkdir cli session dir: %v", err)
	}
	session := map[string]any{"project_id": st.ProjectID, "token": cliToken}
	sessionBody, _ := json.Marshal(session)
	if err := os.WriteFile(filepath.Join(home, ".symphony", "cli-session.json"), sessionBody, 0o600); err != nil {
		t.Fatalf("write cli session: %v", err)
	}
	seenAuth := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/open-token" {
			t.Fatalf("path = %s, want /api/v1/auth/open-token", r.URL.Path)
		}
		seenAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"open_token":"open_test_token"},"meta":{}}`))
	}))
	t.Cleanup(server.Close)
	if err := st.CreateRuntimeDescriptor(server.URL, server.URL, 1234); err != nil {
		t.Fatalf("CreateRuntimeDescriptor: %v", err)
	}
	st.Close()

	desc, err := openDescriptor(dir)
	if err != nil {
		t.Fatalf("openDescriptor: %v", err)
	}
	if seenAuth != "Bearer "+cliToken {
		t.Fatalf("Authorization = %q, want bearer CLI token", seenAuth)
	}
	if _, ok := desc["open_token"]; ok {
		t.Fatalf("descriptor exposed bare open_token: %#v", desc)
	}
	dashboardURL, _ := desc["dashboard_url"].(string)
	if !strings.HasPrefix(dashboardURL, server.URL+"/?open_token=open_test_token") {
		t.Fatalf("dashboard_url = %q", dashboardURL)
	}
}

func TestReadCLISessionTokenReadsProjectScopedSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	projectID := "project_a"
	sessionDir := filepath.Join(home, ".symphony", "cli-sessions")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatalf("mkdir cli session dir: %v", err)
	}
	sessionBody, _ := json.Marshal(map[string]any{"project_id": projectID, "token": "scoped-token"})
	if err := os.WriteFile(filepath.Join(sessionDir, projectID+".json"), sessionBody, 0o600); err != nil {
		t.Fatalf("write cli session: %v", err)
	}

	token, err := readCLISessionToken(projectID)
	if err != nil {
		t.Fatalf("readCLISessionToken: %v", err)
	}
	if token != "scoped-token" {
		t.Fatalf("token = %q, want scoped-token", token)
	}
}

func TestReadCLISessionTokenReadsSanitizedProjectScopedSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	projectID := "../../cli-session"
	sessionPath := app.CLISessionPath(projectID)
	if err := os.MkdirAll(filepath.Dir(sessionPath), 0o700); err != nil {
		t.Fatalf("mkdir cli session dir: %v", err)
	}
	sessionBody, _ := json.Marshal(map[string]any{"project_id": projectID, "token": "sanitized-token"})
	if err := os.WriteFile(sessionPath, sessionBody, 0o600); err != nil {
		t.Fatalf("write cli session: %v", err)
	}

	token, err := readCLISessionToken(projectID)
	if err != nil {
		t.Fatalf("readCLISessionToken: %v", err)
	}
	if token != "sanitized-token" {
		t.Fatalf("token = %q, want sanitized-token", token)
	}
}

func TestReadCLISessionTokenFallsBackToLegacySession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	projectID := "project_a"
	sessionDir := filepath.Join(home, ".symphony")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatalf("mkdir cli session dir: %v", err)
	}
	sessionBody, _ := json.Marshal(map[string]any{"project_id": projectID, "token": "legacy-token"})
	if err := os.WriteFile(filepath.Join(sessionDir, "cli-session.json"), sessionBody, 0o600); err != nil {
		t.Fatalf("write legacy cli session: %v", err)
	}

	token, err := readCLISessionToken(projectID)
	if err != nil {
		t.Fatalf("readCLISessionToken: %v", err)
	}
	if token != "legacy-token" {
		t.Fatalf("token = %q, want legacy-token", token)
	}
}

func TestReadCLISessionTokenRejectsWrongProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	projectID := "project_a"
	sessionDir := filepath.Join(home, ".symphony", "cli-sessions")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatalf("mkdir cli session dir: %v", err)
	}
	sessionBody, _ := json.Marshal(map[string]any{"project_id": "project_b", "token": "scoped-token"})
	if err := os.WriteFile(filepath.Join(sessionDir, projectID+".json"), sessionBody, 0o600); err != nil {
		t.Fatalf("write cli session: %v", err)
	}

	_, err := readCLISessionToken(projectID)
	if err == nil {
		t.Fatal("readCLISessionToken succeeded, want invalid project error")
	}
	if got := core.AsAPIError(err).Code; got != core.ErrUnauthorized {
		t.Fatalf("error code = %s, want %s", got, core.ErrUnauthorized)
	}
}

func TestReadCLISessionTokenRejectsEmptyToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	projectID := "project_a"
	sessionDir := filepath.Join(home, ".symphony", "cli-sessions")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatalf("mkdir cli session dir: %v", err)
	}
	sessionBody, _ := json.Marshal(map[string]any{"project_id": projectID, "token": " \t\n"})
	if err := os.WriteFile(filepath.Join(sessionDir, projectID+".json"), sessionBody, 0o600); err != nil {
		t.Fatalf("write cli session: %v", err)
	}

	_, err := readCLISessionToken(projectID)
	if err == nil {
		t.Fatal("readCLISessionToken succeeded, want empty token error")
	}
	if got := core.AsAPIError(err).Code; got != core.ErrUnauthorized {
		t.Fatalf("error code = %s, want %s", got, core.ErrUnauthorized)
	}
}

func TestReadCLISessionTokenRejectsInvalidLegacySession(t *testing.T) {
	tests := []struct {
		name      string
		projectID string
		token     string
	}{
		{name: "wrong project", projectID: "project_b", token: "legacy-token"},
		{name: "empty token", projectID: "project_a", token: " \t\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			sessionDir := filepath.Join(home, ".symphony")
			if err := os.MkdirAll(sessionDir, 0o700); err != nil {
				t.Fatalf("mkdir cli session dir: %v", err)
			}
			sessionBody, _ := json.Marshal(map[string]any{"project_id": tt.projectID, "token": tt.token})
			if err := os.WriteFile(filepath.Join(sessionDir, "cli-session.json"), sessionBody, 0o600); err != nil {
				t.Fatalf("write legacy cli session: %v", err)
			}

			_, err := readCLISessionToken("project_a")
			if err == nil {
				t.Fatal("readCLISessionToken succeeded, want invalid legacy session error")
			}
			if got := core.AsAPIError(err).Code; got != core.ErrUnauthorized {
				t.Fatalf("error code = %s, want %s", got, core.ErrUnauthorized)
			}
		})
	}
}
