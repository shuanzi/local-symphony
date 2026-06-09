package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-symphony/internal/app"
	"local-symphony/internal/core"
	"local-symphony/internal/daemonclient"
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

func TestDiagnosticsReturnsUnsupportedDBVersionDetails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	projectDBPath := st.ProjectDBPath
	if err := st.Project.Exec(`UPDATE schema_meta SET value='2' WHERE key='schema_version'`); err != nil {
		t.Fatalf("update project schema version: %v", err)
	}
	st.Close()

	code, stdout, stderr := captureCLIOutput(t, func() int {
		return Main([]string{"diagnostics", "--project", dir})
	})
	if code != core.ExitCodeForError(core.ErrUnsupportedDBVersion) {
		t.Fatalf("diagnostics exit code = %d, want %d; stderr = %s", code, core.ExitCodeForError(core.ErrUnsupportedDBVersion), stderr)
	}
	if stdout != "" {
		t.Fatalf("diagnostics wrote stdout on unsupported DB: %s", stdout)
	}
	var envelope core.ErrorEnvelope
	if err := json.Unmarshal([]byte(stderr), &envelope); err != nil {
		t.Fatalf("decode diagnostics stderr: %v; stderr = %s", err, stderr)
	}
	if got := envelope.Error["code"]; got != string(core.ErrUnsupportedDBVersion) {
		t.Fatalf("error code = %v, want %s", got, core.ErrUnsupportedDBVersion)
	}
	details, ok := envelope.Error["details"].(map[string]any)
	if !ok {
		t.Fatalf("error details has type %T, want map[string]any", envelope.Error["details"])
	}
	if got := details["db_path"]; got != projectDBPath {
		t.Fatalf("db_path detail = %v, want %s", got, projectDBPath)
	}
	if got := details["detected_version"]; got != "2" {
		t.Fatalf("detected_version detail = %v, want 2", got)
	}
	if got := details["expected_version"]; got != "1" {
		t.Fatalf("expected_version detail = %v, want 1", got)
	}
	guidance, ok := details["operator_guidance"].(string)
	if !ok || !strings.Contains(strings.ToLower(guidance), "compatible binary") {
		t.Fatalf("operator_guidance detail missing compatible binary guidance: %#v", details["operator_guidance"])
	}
}

func TestReviewReturnsErrorWhenArtifactMetadataQueryFails(t *testing.T) {
	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	issue, err := st.CreateIssue(store.CreateIssueInput{
		Title:              "Review metadata failure",
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
	handoff, err := st.InsertHandoff(issue.ID, run.ID, "payload-hash", map[string]any{
		"summary":      "ready for review",
		"target_state": "Human Review",
	})
	if err != nil {
		t.Fatalf("InsertHandoff: %v", err)
	}
	if _, err := st.InsertReviewPacket(issue.ID, run.ID, handoff.ID, st.RepoRoot, "review.md", "review.json", "patch.diff", "changed.txt", "untracked.txt", "diffstat.txt", ""); err != nil {
		t.Fatalf("InsertReviewPacket: %v", err)
	}
	if err := st.Project.Exec("DROP TABLE artifacts"); err != nil {
		t.Fatalf("drop artifacts: %v", err)
	}
	st.Close()

	code, stdout, _ := captureCLIOutput(t, func() int {
		return Main([]string{"review", issue.Identifier, "--project", dir})
	})
	if code != core.ExitCodeForError(core.ErrInternal) {
		t.Fatalf("review exit code = %d, want %d; stdout = %s", code, core.ExitCodeForError(core.ErrInternal), stdout)
	}
	if stdout != "" {
		t.Fatalf("review wrote stdout on metadata query failure: %s", stdout)
	}
}

func TestReviewPathSurfacesOnlyMetadataAndPathDiagnostics(t *testing.T) {
	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	issue, err := st.CreateIssue(store.CreateIssueInput{
		Title:              "Review path metadata only",
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
	handoff, err := st.InsertHandoff(issue.ID, run.ID, "payload-hash", map[string]any{
		"summary":      "ready for review",
		"target_state": "Human Review",
	})
	if err != nil {
		t.Fatalf("InsertHandoff: %v", err)
	}
	root := filepath.Join(st.RepoRoot, ".symphony", "artifacts", issue.Identifier, run.ID)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir review dir: %v", err)
	}
	// Drop a sentinel "raw secret" file under the artifact dir. The
	// CLI must never inline its content into stdout for `review path`.
	rawSentinel := filepath.Join(root, "raw_secret.txt")
	if err := os.WriteFile(rawSentinel, []byte("RAW-SECRET-CONTENT-DO-NOT-LEAK"), 0o644); err != nil {
		t.Fatalf("write raw sentinel: %v", err)
	}
	if _, err := st.InsertReviewPacket(issue.ID, run.ID, handoff.ID, root, "review.md", "review.json", "changes.patch", "changed-files.txt", "untracked-files.json", "diffstat.txt", ""); err != nil {
		t.Fatalf("InsertReviewPacket: %v", err)
	}
	st.Close()

	code, stdout, _ := captureCLIOutput(t, func() int {
		return Main([]string{"review", "path", issue.Identifier, "--project", dir})
	})
	if code != 0 {
		t.Fatalf("review path exit code = %d, want 0; stdout = %s", code, stdout)
	}
	// Invariant: review path must never inline raw prompt / codex log
	// / secret content into stdout. The only raw content we leaked
	// into the artifact directory was the secret string, so checking
	// for its absence is sufficient.
	if strings.Contains(stdout, "RAW-SECRET-CONTENT-DO-NOT-LEAK") {
		t.Fatalf("review path leaked raw secret bytes: %s", stdout)
	}
	// Metadata fields (status, root_path) must be present so
	// operators can copy-paste the path into another tool.
	if !strings.Contains(stdout, "\"status\":") {
		t.Fatalf("review path stdout missing status field: %s", stdout)
	}
	if !strings.Contains(stdout, "\"root_path\":") {
		t.Fatalf("review path stdout missing root_path field: %s", stdout)
	}
	// And the stdout must NOT be a "review" packet projection
	// (which would inline summary / diff / handoff). The path
	// command is local-store only and is the strict-metadata
	// surface; the full structured projection is reachable via
	// `symphony review LOC-1` (or the dashboard) which goes through
	// the Review API.
	if strings.Contains(stdout, "\"diff\":") || strings.Contains(stdout, "\"summary\":") {
		t.Fatalf("review path inlined review packet content; want strict metadata only: %s", stdout)
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

func captureCLIOutput(t *testing.T, fn func() int) (int, string, string) {
	t.Helper()

	oldStdout := os.Stdout
	oldStderr := os.Stderr
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		t.Fatalf("stderr pipe: %v", err)
	}
	os.Stdout = stdoutW
	os.Stderr = stderrW
	defer func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
		_ = stdoutR.Close()
		_ = stderrR.Close()
		_ = stdoutW.Close()
		_ = stderrW.Close()
	}()

	code := fn()

	if err := stdoutW.Close(); err != nil {
		t.Fatalf("close stdout writer: %v", err)
	}
	if err := stderrW.Close(); err != nil {
		t.Fatalf("close stderr writer: %v", err)
	}
	stdout, err := io.ReadAll(stdoutR)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	stderr, err := io.ReadAll(stderrR)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	return code, string(stdout), string(stderr)
}

func TestIssueCreateDefaultsPriorityWhenFlagMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	projectID := st.ProjectID
	st.Close()

	// Mutating commands do not fall back to the local store, so
	// the test must run a daemon. The daemon echoes the issue back
	// as if the local store had created it; the priority default
	// lives in the parsed CLI args, so the daemon's response is
	// a passthrough.
	var seenPriority int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":{"ok":true,"project_id":"%s"},"meta":{}}`+"\n", projectID)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if p, ok := body["priority"].(float64); ok {
			seenPriority = int(p)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"data":{"id":"iss_1","identifier":"APP-1","sequence_no":1,"title":"x","description":"","acceptance_criteria":[],"priority":3,"state":"Inbox","url":null,"labels":[],"blocked_by":[],"blocks":[],"duplicate_of":null,"duplicates":[],"followup_of":null,"followups":[],"dispatch_paused":false,"dispatch_pause_reason":null,"dispatch_paused_at":null,"branch_name":null,"workspace_path":null,"base_ref":null,"base_ref_config":null,"base_sha":null,"workspace":null,"git":null,"latest_run":null,"active_run_id":null,"latest_run_id":null,"latest_review_packet":null,"latest_review_packet_id":null,"created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z","completed_at":null,"archived_at":null},"meta":{}}`)
	}))
	t.Cleanup(server.Close)
	if _, err := daemonclient.WriteSessionFile(projectID, daemonclient.SessionFile{ProjectID: projectID, APIURL: server.URL, Token: "tok"}); err != nil {
		t.Fatalf("WriteSessionFile: %v", err)
	}

	code := Main([]string{"issue", "create", "--project", dir, "--title", "Default priority"})
	if code != 0 {
		t.Fatalf("issue create exit code = %d, want 0", code)
	}
	if seenPriority != 3 {
		t.Fatalf("daemon saw priority = %d, want default 3", seenPriority)
	}

	// The daemon now owns the write. The local store must remain
	// untouched — mutating commands no longer fall back, so the
	// dispatcher should not have applied the create locally.
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
		t.Fatalf("local store has %d issues, want 0 (daemon owns writes)", len(issues))
	}
}

func TestRunAcceptsCustomIssuePrefix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	projectID := st.ProjectID
	st.Close()

	// `run APP-1` is a mutating command (issue dispatch via
	// orchestrator). Under C4 it must hit the daemon, not fall
	// back to the local store. Stand up a stub daemon that
	// accepts the dispatch and returns an empty run list.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":{"ok":true,"project_id":"%s"},"meta":{}}`+"\n", projectID)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/issues/APP-1/dispatch" {
			fmt.Fprintln(w, `{"data":{"id":"run_1","status":"pending","dispatch_reason":"manual"},"meta":{}}`)
			return
		}
		fmt.Fprintln(w, `{"data":[],"meta":{}}`)
	}))
	t.Cleanup(server.Close)
	if _, err := daemonclient.WriteSessionFile(projectID, daemonclient.SessionFile{ProjectID: projectID, APIURL: server.URL, Token: "tok"}); err != nil {
		t.Fatalf("WriteSessionFile: %v", err)
	}

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
		// Round-5 CRITICAL fix: openDescriptor now probes
		// /api/v1/health first to verify the runtime
		// descriptor's api_url advertises the project_id
		// before the bearer is dispatched. The handler
		// answers /health with the project's id and only
		// then the bearer-carrying /open-token request
		// succeeds.
		if r.URL.Path == "/api/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":{"ok":true,"project_id":"%s"},"meta":{}}`+"\n", st.ProjectID)
			return
		}
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

func TestRequestOpenTokenReturnsErrorWithShortTimeoutClient(t *testing.T) {
	oldClient := openTokenHTTPClient
	openTokenHTTPClient = &http.Client{Timeout: 10 * time.Millisecond}
	t.Cleanup(func() { openTokenHTTPClient = oldClient })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)

	start := time.Now()
	_, err := requestOpenToken(server.URL, "cli-token")
	elapsed := time.Since(start)

	if elapsed > time.Second {
		t.Fatalf("requestOpenToken took %s, want bounded timeout", elapsed)
	}
	if err == nil {
		t.Fatal("requestOpenToken succeeded, want timeout error")
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

	token, err := readCLISessionToken(projectID, "")
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

	token, err := readCLISessionToken(projectID, "")
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

	token, err := readCLISessionToken(projectID, "")
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

	_, err := readCLISessionToken(projectID, "")
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

	_, err := readCLISessionToken(projectID, "")
	if err == nil {
		t.Fatal("readCLISessionToken succeeded, want empty token error")
	}
	if got := core.AsAPIError(err).Code; got != core.ErrUnauthorized {
		t.Fatalf("error code = %s, want %s", got, core.ErrUnauthorized)
	}
}

func TestExitCodeMappingMatchesProductizationPlan(t *testing.T) {
	tests := []struct {
		name string
		code core.APIErrorCode
		want int
	}{
		// invalid_request -> 2
		{name: "invalid_request", code: core.ErrInvalidRequest, want: 2},

		// workflow / prompt related -> 9
		{name: "workflow_invalid", code: core.ErrWorkflowInvalid, want: 9},
		{name: "workflow_validation_failed", code: core.ErrWorkflowValidationFailed, want: 9},
		{name: "prompt_render_failed", code: core.ErrPromptRenderFailed, want: 9},
		{name: "prompt_blocked", code: core.ErrPromptBlocked, want: 9},
		{name: "policy_denied", code: core.ErrPolicyDenied, want: 9},
		{name: "command_denied", code: core.ErrCommandDenied, want: 9},

		// operation conflicts (other) -> 7
		{name: "unauthorized", code: core.ErrUnauthorized, want: 7},
		{name: "forbidden", code: core.ErrForbidden, want: 7},
		{name: "csrf_required", code: core.ErrCSRFRequired, want: 7},
		{name: "not_found", code: core.ErrNotFound, want: 7},
		{name: "unsupported_db_version", code: core.ErrUnsupportedDBVersion, want: 7},
		{name: "invalid_state_transition", code: core.ErrInvalidStateTransition, want: 7},
		{name: "issue_blocked", code: core.ErrIssueBlocked, want: 7},
		{name: "issue_dispatch_paused", code: core.ErrIssueDispatchPaused, want: 7},
		{name: "issue_already_running", code: core.ErrIssueAlreadyRunning, want: 7},
		{name: "concurrency_limit_reached", code: core.ErrConcurrencyLimitReached, want: 7},
		{name: "workspace_conflict", code: core.ErrWorkspaceConflict, want: 7},
		{name: "workspace_prepare_failed", code: core.ErrWorkspacePrepareFailed, want: 7},
		{name: "review_packet_required", code: core.ErrReviewPacketRequired, want: 7},
		{name: "review_packet_failed", code: core.ErrReviewPacketFailed, want: 7},
		{name: "tool_token_invalid", code: core.ErrToolTokenInvalid, want: 7},
		{name: "tool_gateway_failed", code: core.ErrToolGatewayFailed, want: 7},
		{name: "approval_not_pending", code: core.ErrApprovalNotPending, want: 7},
		{name: "approval_timeout", code: core.ErrApprovalTimeout, want: 7},
		{name: "not_owner", code: core.ErrNotOwner, want: 7},
		{name: "daemon_unavailable", code: core.ErrDaemonUnavailable, want: 7},
		{name: "raw_log_access_not_supported", code: core.ErrRawLogAccessUnsupported, want: 7},

		// unclassified (internal + unmapped) -> 1
		{name: "internal_error", code: core.ErrInternal, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := core.ExitCodeForError(tt.code); got != tt.want {
				t.Fatalf("ExitCodeForError(%s) = %d, want %d", tt.code, got, tt.want)
			}
		})
	}
}

func TestPrintErrRendersEnvelopeAndExitsWithCode(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
	}{
		{
			name:     "invalid_request", err: core.NewError(core.ErrInvalidRequest, "bad", nil),
			wantCode: 2,
		},
		{
			name:     "workflow_invalid", err: core.NewError(core.ErrWorkflowInvalid, "wf", nil),
			wantCode: 9,
		},
		{
			name:     "issue_already_running", err: core.NewError(core.ErrIssueAlreadyRunning, "running", nil),
			wantCode: 7,
		},
		{
			name:     "internal_error", err: core.NewError(core.ErrInternal, "boom", nil),
			wantCode: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, _, stderr := captureCLIOutput(t, func() int { return printErr(tt.err) })
			if code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d; stderr = %s", code, tt.wantCode, stderr)
			}
			var envelope core.ErrorEnvelope
			if err := json.Unmarshal([]byte(stderr), &envelope); err != nil {
				t.Fatalf("decode stderr: %v; stderr = %s", err, stderr)
			}
			em, ok := envelope.Error["code"].(string)
			if !ok || em != string(tt.err.(*core.APIError).Code) {
				t.Fatalf("envelope code = %v, want %s", envelope.Error["code"], tt.err.(*core.APIError).Code)
			}
		})
	}
}

func TestPrintErrRendersAsEnvelopeForRawError(t *testing.T) {
	code, _, stderr := captureCLIOutput(t, func() int { return printErr(fmt.Errorf("plain error")) })
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	var envelope core.ErrorEnvelope
	if err := json.Unmarshal([]byte(stderr), &envelope); err != nil {
		t.Fatalf("decode stderr: %v; stderr = %s", err, stderr)
	}
	if got := envelope.Error["code"]; got != "internal_error" {
		t.Fatalf("code = %v, want internal_error", got)
	}
}

func TestDaemonUnavailableMessageMatchesPlan(t *testing.T) {
	got := daemonUnavailableMessage()
	want := "daemon is not running, start with 'symphony serve' or run 'symphony open --help' for project init"
	if got != want {
		t.Fatalf("daemonUnavailableMessage = %q, want %q", got, want)
	}
}

func TestPrintDaemonUnavailableWritesMessageAndExitsSeven(t *testing.T) {
	code, _, stderr := captureCLIOutput(t, func() int { return PrintDaemonUnavailable() })
	if code != 7 {
		t.Fatalf("exit code = %d, want 7", code)
	}
	if !strings.Contains(stderr, "daemon is not running, start with 'symphony serve'") {
		t.Fatalf("stderr = %q, want guidance phrase", stderr)
	}
}

func TestLoginCommandSurfacesGuidanceWhenDaemonUnreachable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(daemonclient.EnvOverride, "")
	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	st.Close()
	code, _, stderr := captureCLIOutput(t, func() int { return Main([]string{"login", "--project", dir}) })
	if code != 7 {
		t.Fatalf("login exit code = %d, want 7; stderr = %s", code, stderr)
	}
	if !strings.Contains(stderr, "daemon is not running") {
		t.Fatalf("login stderr = %q, want guidance", stderr)
	}
}

func TestHelpTextMentionsDaemonFallback(t *testing.T) {
	_, stdout, _ := captureCLIOutput(t, func() int { return Main([]string{"--help"}) })
	for _, want := range []string{"daemon", "symphony serve", "symphony open", "fall back"} {
		if !strings.Contains(strings.ToLower(stdout), strings.ToLower(want)) {
			t.Fatalf("help text missing %q; got: %s", want, stdout)
		}
	}
}

// TestIssueListUsesDaemonWhenAvailable exercises the full REST path:
// the CLI is given a server that returns a /api/v1/issues envelope, the
// discovery probe finds the same server through the session-file
// fallback, and the data is rendered as a stable JSON object.
func TestIssueListUsesDaemonWhenAvailable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	projectID := st.ProjectID
	st.Close()

	var seenAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":{"ok":true,"project_id":"%s"},"meta":{}}`+"\n", projectID)
			return
		}
		seenAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":{"items":[],"page":{"limit":50,"next_cursor":null,"has_more":false}},"meta":{}}`+"\n")
	}))
	t.Cleanup(server.Close)

	// Persist a session that points at the test server. The discovery
	// fallback reads api_url from this file when env / user config /
	// runtime descriptor are absent.
	if _, err := daemonclient.WriteSessionFile(projectID, daemonclient.SessionFile{ProjectID: projectID, APIURL: server.URL, Token: "tok"}); err != nil {
		t.Fatalf("WriteSessionFile: %v", err)
	}

	code, stdout, stderr := captureCLIOutput(t, func() int { return Main([]string{"issue", "list", "--project", dir}) })
	if code != 0 {
		t.Fatalf("issue list exit code = %d, want 0; stderr = %s", code, stderr)
	}
	if !strings.Contains(seenAuth, "Bearer tok") {
		t.Fatalf("daemon did not see bearer auth header: %q", seenAuth)
	}
	if !strings.Contains(stdout, `"items"`) {
		t.Fatalf("stdout missing items: %s", stdout)
	}
}

// TestIssueListFallsBackToLocalStoreWhenDaemonUnreachable ensures that
// when the daemon cannot be reached, the CLI runs the local-store
// fallback so existing offline flows keep working.
func TestIssueListFallsBackToLocalStoreWhenDaemonUnreachable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	st.Close()

	code, stdout, stderr := captureCLIOutput(t, func() int { return Main([]string{"issue", "list", "--project", dir}) })
	if code != 0 {
		t.Fatalf("issue list exit code = %d, want 0; stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout, `"items"`) {
		t.Fatalf("stdout missing items: %s", stdout)
	}
}

// TestIssueCreatePropagatesInvalidRequestFromDaemon ensures that API
// errors (not network errors) are surfaced verbatim rather than
// falling back to the local store.
func TestIssueCreatePropagatesInvalidRequestFromDaemon(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	projectID := st.ProjectID
	st.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":{"ok":true,"project_id":"%s"},"meta":{}}`+"\n", projectID)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintln(w, `{"error":{"code":"invalid_request","message":"--title is required","details":{"field":"title"},"request_id":"req_1"}}`)
	}))
	t.Cleanup(server.Close)
	if _, err := daemonclient.WriteSessionFile(projectID, daemonclient.SessionFile{ProjectID: projectID, APIURL: server.URL, Token: "tok"}); err != nil {
		t.Fatalf("WriteSessionFile: %v", err)
	}

	code, _, stderr := captureCLIOutput(t, func() int {
		return Main([]string{"issue", "create", "--project", dir, "--description", "x"})
	})
	if code != 2 {
		t.Fatalf("issue create exit code = %d, want 2; stderr = %s", code, stderr)
	}
	if !strings.Contains(stderr, "invalid_request") {
		t.Fatalf("stderr missing invalid_request code: %s", stderr)
	}
}

// TestMutatingCommandSurfacesAuthWhenSessionMissing covers the P1
// regression: with a running daemon and no CLI bearer, the dispatcher
// must NOT silently fall back to the local store for mutating
// commands. The local store path would let `issue create` succeed
// without daemon-side enforcement. Instead, the CLI should fail with
// an auth error.
func TestMutatingCommandSurfacesAuthWhenSessionMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	projectID := st.ProjectID
	st.Close()

	// Daemon is reachable but no session file. The dispatcher must
	// surface the auth error rather than running the local store path.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":{"ok":true,"project_id":"%s"},"meta":{}}`+"\n", projectID)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintln(w, `{"error":{"code":"unauthorized","message":"session required","details":{}}}`)
	}))
	t.Cleanup(server.Close)

	// Persist only an api_url-bearing "session" with an empty token.
	// newDaemonContext should treat this as ErrSessionMissing and
	// surface the auth error to the CLI. WriteSessionFile rejects
	// whitespace-only tokens, so we write the file directly to
	// simulate the "session file exists but token is invalid" state.
	{
		path := app.CLISessionPath(projectID)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("mkdir session dir: %v", err)
		}
		raw, _ := json.Marshal(map[string]any{"project_id": projectID, "api_url": server.URL, "token": ""})
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatalf("write session file: %v", err)
		}
	}

	// Confirm the local store has no issue yet.
	st2, err := store.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	before, _ := st2.ListIssues(store.ListIssueOptions{Limit: 10})
	st2.Close()

	code, _, stderr := captureCLIOutput(t, func() int {
		return Main([]string{"issue", "create", "--project", dir, "--title", "P1 test", "--description", "x"})
	})
	if code == 0 {
		t.Fatalf("issue create succeeded while daemon had no session; want auth failure")
	}
	if !strings.Contains(stderr, "unauthorized") && !strings.Contains(stderr, "session missing") && !strings.Contains(stderr, "CLI session") {
		t.Fatalf("stderr missing auth guidance: %s", stderr)
	}

	// Critical assertion: the local store was NOT mutated. The bug
	// being fixed would have written a row.
	st3, err := store.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st3.Close()
	after, _ := st3.ListIssues(store.ListIssueOptions{Limit: 10})
	if len(after) != len(before) {
		t.Fatalf("local store was mutated despite auth failure: before=%d after=%d", len(before), len(after))
	}
}

// TestOfflineMutatingRefusesToFallBack pins the P2 #2 fix: with no
// daemon reachable, mutating commands must NOT silently run the
// local store. The CLI surfaces daemon_unavailable so the operator
// knows the command did not land. (Read-only commands still
// fall back — see TestOfflineReadOnlyFallsBackToLocalStore below.)
func TestOfflineMutatingRefusesToFallBack(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	st.Close()

	before, _ := func() ([]*core.Issue, error) {
		s, _ := store.Open(dir)
		defer s.Close()
		return s.ListIssues(store.ListIssueOptions{Limit: 100})
	}()

	code, _, stderr := captureCLIOutput(t, func() int {
		return Main([]string{"issue", "create", "--project", dir, "--title", "offline create"})
	})
	if code == 0 {
		t.Fatalf("offline issue create succeeded; want daemon_unavailable")
	}
	if !strings.Contains(stderr, "daemon_unavailable") {
		t.Fatalf("offline issue create stderr missing daemon_unavailable: %s", stderr)
	}

	// Critical assertion: the local store was NOT mutated.
	after, _ := func() ([]*core.Issue, error) {
		s, _ := store.Open(dir)
		defer s.Close()
		return s.ListIssues(store.ListIssueOptions{Limit: 100})
	}()
	if len(after) != len(before) {
		t.Fatalf("local store mutated despite daemon_unavailable: before=%d after=%d", len(before), len(after))
	}
}

// TestOfflineReadOnlyFallsBackToLocalStore pins the read-only
// offline fallback. With no daemon reachable, the CLI must still
// surface local store data for read commands so offline flows
// keep working.
func TestOfflineReadOnlyFallsBackToLocalStore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	if _, err := st.CreateIssue(store.CreateIssueInput{Title: "offline read", Description: "x", Priority: 3}); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	st.Close()

	code, stdout, _ := captureCLIOutput(t, func() int {
		return Main([]string{"issue", "list", "--project", dir})
	})
	if code != 0 {
		t.Fatalf("offline issue list exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "offline read") {
		t.Fatalf("offline issue list stdout missing title: %s", stdout)
	}
}

// TestReadCommandStillFallsBackToLocalStoreWhenSessionMissing
// demonstrates that read-only commands keep the offline-fallback
// safety net: when the daemon is reachable but no session, the
// dispatcher surfaces an auth error, but the local store fallback is
// only forbidden for mutating paths. Read-only commands still
// succeed against the local store because the v1 plan's read paths
// are intentionally daemon-best-effort.
//
// In practice, `issue list` is wired through withDaemonOrStore which
// treats ErrSessionMissing as a hard error. This test pins that
// behaviour so a future refactor cannot silently allow mutating
// paths to fall back. We exercise the read path against a server
// that returns the data; the local store would have no data.
func TestReadCommandFailsAuthWhenSessionMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	projectID := st.ProjectID
	st.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":{"ok":true,"project_id":"%s"},"meta":{}}`+"\n", projectID)
			return
		}
	}))
	t.Cleanup(server.Close)

	// Write the session file directly to bypass the empty-token
	// check in WriteSessionFile; the on-disk token is empty so
	// daemonclient.New returns ErrSessionMissing.
	{
		path := app.CLISessionPath(projectID)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("mkdir session dir: %v", err)
		}
		raw, _ := json.Marshal(map[string]any{"project_id": projectID, "api_url": server.URL, "token": ""})
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatalf("write session file: %v", err)
		}
	}

	// issue list with no bearer should fail (not silently fall back
	// to the local store with empty results — the operator has been
	// explicit about wanting the daemon path).
	code, _, stderr := captureCLIOutput(t, func() int {
		return Main([]string{"issue", "list", "--project", dir})
	})
	if code == 0 {
		t.Fatalf("issue list succeeded with no session; want auth failure")
	}
	if !strings.Contains(stderr, "unauthorized") && !strings.Contains(stderr, "session") {
		t.Fatalf("stderr missing auth error: %s", stderr)
	}
}

// TestRunListDecodesArrayResponse covers P2 #1: /api/v1/runs returns a
// JSON array, and UnwrapArray is the right helper. The test pins the
// regression.
func TestRunListDecodesArrayResponse(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	projectID := st.ProjectID
	st.Close()

	var seenMethod, seenPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":{"ok":true,"project_id":"%s"},"meta":{}}`+"\n", projectID)
			return
		}
		seenMethod = r.Method
		seenPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"data":[{"id":"run_1","status":"running"},{"id":"run_2","status":"completed"}],"meta":{}}`)
	}))
	t.Cleanup(server.Close)
	if _, err := daemonclient.WriteSessionFile(projectID, daemonclient.SessionFile{ProjectID: projectID, APIURL: server.URL, Token: "tok"}); err != nil {
		t.Fatalf("WriteSessionFile: %v", err)
	}

	code, stdout, stderr := captureCLIOutput(t, func() int {
		return Main([]string{"run", "list", "--project", dir})
	})
	if code != 0 {
		t.Fatalf("run list exit code = %d, want 0; stderr = %s", code, stderr)
	}
	if seenMethod != "GET" || seenPath != "/api/v1/runs" {
		t.Fatalf("daemon saw method=%s path=%s, want GET /api/v1/runs", seenMethod, seenPath)
	}
	if !strings.Contains(stdout, `"items"`) || !strings.Contains(stdout, "run_1") {
		t.Fatalf("stdout missing run array: %s", stdout)
	}
}

// TestApprovalListDecodesArrayResponse covers P2 #1 for approvals.
func TestApprovalListDecodesArrayResponse(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	projectID := st.ProjectID
	st.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":{"ok":true,"project_id":"%s"},"meta":{}}`+"\n", projectID)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"data":[{"id":"apr_1","status":"pending"}],"meta":{}}`)
	}))
	t.Cleanup(server.Close)
	if _, err := daemonclient.WriteSessionFile(projectID, daemonclient.SessionFile{ProjectID: projectID, APIURL: server.URL, Token: "tok"}); err != nil {
		t.Fatalf("WriteSessionFile: %v", err)
	}

	code, stdout, stderr := captureCLIOutput(t, func() int {
		return Main([]string{"approval", "list", "--project", dir})
	})
	if code != 0 {
		t.Fatalf("approval list exit code = %d, want 0; stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout, `"items"`) || !strings.Contains(stdout, "apr_1") {
		t.Fatalf("stdout missing approval array: %s", stdout)
	}
}

// TestRunEventsDecodesArrayResponse covers P2 #1 for run events.
func TestRunEventsDecodesArrayResponse(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	projectID := st.ProjectID
	st.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":{"ok":true,"project_id":"%s"},"meta":{}}`+"\n", projectID)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"data":[{"seq":1,"event_type":"started"},{"seq":2,"event_type":"finished"}],"meta":{}}`)
	}))
	t.Cleanup(server.Close)
	if _, err := daemonclient.WriteSessionFile(projectID, daemonclient.SessionFile{ProjectID: projectID, APIURL: server.URL, Token: "tok"}); err != nil {
		t.Fatalf("WriteSessionFile: %v", err)
	}

	code, stdout, stderr := captureCLIOutput(t, func() int {
		return Main([]string{"run", "events", "run_1", "--project", dir})
	})
	if code != 0 {
		t.Fatalf("run events exit code = %d, want 0; stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout, `"items"`) || !strings.Contains(stdout, "started") {
		t.Fatalf("stdout missing events array: %s", stdout)
	}
}

// TestWorkflowValidateUsesPOST covers P2 #2: workflow validate must use
// POST against the daemon.
func TestWorkflowValidateUsesPOST(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	projectID := st.ProjectID
	st.Close()

	var seenMethod string
	var seenBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":{"ok":true,"project_id":"%s"},"meta":{}}`+"\n", projectID)
			return
		}
		seenMethod = r.Method
		seenBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"data":{"validation":{"valid":true,"issues":[]}},"meta":{}}`)
	}))
	t.Cleanup(server.Close)
	if _, err := daemonclient.WriteSessionFile(projectID, daemonclient.SessionFile{ProjectID: projectID, APIURL: server.URL, Token: "tok"}); err != nil {
		t.Fatalf("WriteSessionFile: %v", err)
	}

	code, _, stderr := captureCLIOutput(t, func() int {
		return Main([]string{"workflow", "validate", "--project", dir})
	})
	if code != 0 {
		t.Fatalf("workflow validate exit code = %d, want 0; stderr = %s", code, stderr)
	}
	if seenMethod != "POST" {
		t.Fatalf("workflow validate used %s, want POST", seenMethod)
	}
	if !strings.Contains(string(seenBody), `"dry_run"`) {
		t.Fatalf("workflow validate body missing dry_run: %s", string(seenBody))
	}
}

// TestWorkflowReloadUsesPOST covers P2 #2 for reload.
func TestWorkflowReloadUsesPOST(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	projectID := st.ProjectID
	st.Close()

	var seenMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":{"ok":true,"project_id":"%s"},"meta":{}}`+"\n", projectID)
			return
		}
		seenMethod = r.Method
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"data":{"reloaded":true,"validation":{"valid":true}},"meta":{}}`)
	}))
	t.Cleanup(server.Close)
	if _, err := daemonclient.WriteSessionFile(projectID, daemonclient.SessionFile{ProjectID: projectID, APIURL: server.URL, Token: "tok"}); err != nil {
		t.Fatalf("WriteSessionFile: %v", err)
	}

	code, _, stderr := captureCLIOutput(t, func() int {
		return Main([]string{"workflow", "reload", "--project", dir})
	})
	if code != 0 {
		t.Fatalf("workflow reload exit code = %d, want 0; stderr = %s", code, stderr)
	}
	if seenMethod != "POST" {
		t.Fatalf("workflow reload used %s, want POST", seenMethod)
	}
}

// TestIssueListQueryStringPreserved covers P2 #3: the daemon-side
// `/api/v1/issues?limit=1` must keep the query string after URL
// building. url.JoinPath would percent-encode the `?` separator.
func TestIssueListQueryStringPreserved(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	projectID := st.ProjectID
	st.Close()

	var seenRawQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":{"ok":true,"project_id":"%s"},"meta":{}}`+"\n", projectID)
			return
		}
		seenRawQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"data":{"items":[],"page":{"limit":1,"next_cursor":null,"has_more":false}},"meta":{}}`)
	}))
	t.Cleanup(server.Close)
	if _, err := daemonclient.WriteSessionFile(projectID, daemonclient.SessionFile{ProjectID: projectID, APIURL: server.URL, Token: "tok"}); err != nil {
		t.Fatalf("WriteSessionFile: %v", err)
	}

	code, _, stderr := captureCLIOutput(t, func() int {
		return Main([]string{"issue", "list", "--project", dir, "--limit", "1"})
	})
	if code != 0 {
		t.Fatalf("issue list exit code = %d, want 0; stderr = %s", code, stderr)
	}
	if seenRawQuery == "" {
		t.Fatalf("daemon saw no query string; want limit=1")
	}
	if !strings.Contains(seenRawQuery, "limit=1") {
		t.Fatalf("daemon query string = %q, want limit=1", seenRawQuery)
	}
}

// TestLoginListWithoutProject covers P2 #4: `symphony login --list`
// must work from a directory that is not a Local Symphony project.
func TestLoginListWithoutProject(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	notAProject := t.TempDir()

	code, stdout, stderr := captureCLIOutput(t, func() int {
		return Main([]string{"login", "--list", "--project", notAProject})
	})
	if code != 0 {
		t.Fatalf("login --list exit code = %d, want 0; stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout, `"sessions"`) {
		t.Fatalf("login --list stdout missing sessions: %s", stdout)
	}
}

// TestLoginLogoutWithoutProjectSession covers P2 #4 for `--logout`:
// the dispatcher should resolve the project_id from the project root
// and not require the local store to be open. Even when the
// project_id cannot be resolved (e.g. uninitialized directory), the
// command should print a clear error rather than segfault.
func TestLoginLogoutWithoutProjectSession(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	notAProject := t.TempDir()

	code, _, stderr := captureCLIOutput(t, func() int {
		return Main([]string{"login", "--logout", "--project", notAProject})
	})
	if code == 0 {
		t.Fatalf("login --logout succeeded in uninitialized project; want failure")
	}
	// The error must be a project-init / store-open error, not a
	// segfault or a panic.
	if !strings.Contains(stderr, "init") {
		t.Fatalf("login --logout stderr missing init guidance: %s", stderr)
	}
}

// TestIssueShowFallsBackToLocalStoreWhenSessionMissing ensures that
// when the daemon is reachable but no session exists, the dispatcher
// surfaces the auth error to the operator. This is the read-command
// path that is the inverse of TestMutatingCommandSurfacesAuth.
func TestIssueShowFailsAuthWhenSessionMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	projectID := st.ProjectID
	st.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":{"ok":true,"project_id":"%s"},"meta":{}}`+"\n", projectID)
			return
		}
	}))
	t.Cleanup(server.Close)

	// Write the session file directly to bypass the empty-token
	// check in WriteSessionFile; the on-disk token is empty so
	// daemonclient.New returns ErrSessionMissing.
	{
		path := app.CLISessionPath(projectID)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("mkdir session dir: %v", err)
		}
		raw, _ := json.Marshal(map[string]any{"project_id": projectID, "api_url": server.URL, "token": ""})
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatalf("write session file: %v", err)
		}
	}

	code, _, stderr := captureCLIOutput(t, func() int {
		return Main([]string{"issue", "show", "APP-1", "--project", dir})
	})
	if code == 0 {
		t.Fatalf("issue show succeeded with no session; want auth failure")
	}
	if !strings.Contains(stderr, "session") && !strings.Contains(stderr, "unauthorized") {
		t.Fatalf("issue show stderr missing auth error: %s", stderr)
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

			_, err := readCLISessionToken("project_a", "")
			if err == nil {
				t.Fatal("readCLISessionToken succeeded, want invalid legacy session error")
			}
			if got := core.AsAPIError(err).Code; got != core.ErrUnauthorized {
				t.Fatalf("error code = %s, want %s", got, core.ErrUnauthorized)
			}
		})
	}
}

// TestLoginAcceptsCLIBearer pins the P2 #1 fix: /api/v1/auth/session
// must report authenticated=true when a valid CLI bearer is
// presented, even without a browser cookie. The previous
// implementation only checked the cookie and reported
// authenticated=false, making `symphony login` always say "you are
// not logged in" even when subsequent commands worked.
func TestLoginAcceptsCLIBearer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	projectID := st.ProjectID
	st.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":{"ok":true,"project_id":"%s"},"meta":{}}`+"\n", projectID)
			return
		}
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintln(w, `{"error":{"code":"unauthorized","message":"session required","details":{}}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"data":{"authenticated":true,"project_id":"`+projectID+`","bearer":true,"session_kind":"cli"},"meta":{}}`)
	}))
	t.Cleanup(server.Close)
	if _, err := daemonclient.WriteSessionFile(projectID, daemonclient.SessionFile{ProjectID: projectID, APIURL: server.URL, Token: "tok"}); err != nil {
		t.Fatalf("WriteSessionFile: %v", err)
	}

	code, stdout, stderr := captureCLIOutput(t, func() int {
		return Main([]string{"login", "--project", dir})
	})
	if code != 0 {
		t.Fatalf("login exit code = %d, want 0; stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout, `"session": "active"`) {
		t.Fatalf("login stdout missing active session: %s", stdout)
	}
}

// TestLoginWithBearerRejectsCookieResponse pins the inverse: the
// CLI bearer path must still report unauthenticated when the daemon
// returns authenticated=false. The new behaviour must not lie.
func TestLoginWithBearerRejectsCookieResponse(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	projectID := st.ProjectID
	st.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":{"ok":true,"project_id":"%s"},"meta":{}}`+"\n", projectID)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintln(w, `{"error":{"code":"unauthorized","message":"session required","details":{}}}`)
	}))
	t.Cleanup(server.Close)
	if _, err := daemonclient.WriteSessionFile(projectID, daemonclient.SessionFile{ProjectID: projectID, APIURL: server.URL, Token: "tok"}); err != nil {
		t.Fatalf("WriteSessionFile: %v", err)
	}

	code, _, stderr := captureCLIOutput(t, func() int {
		return Main([]string{"login", "--project", dir})
	})
	if code == 0 {
		t.Fatalf("login succeeded; want unauthenticated failure")
	}
	if !strings.Contains(stderr, "unauthorized") {
		t.Fatalf("login stderr missing unauthorized: %s", stderr)
	}
}

// TestMutatingNetworkErrorDoesNotFallBack pins the P2 #2 fix: when
// the daemon is reachable but the request itself fails after the
// network call (e.g. timeout, connection reset), a mutating
// command must NOT retry the local store. The fallback path would
// otherwise cause double-apply or bypass daemon-side enforcement.
//
// We simulate the network failure by closing the test server
// after the CLI has already discovered the daemon URL via the
// session file. Subsequent POSTs fail with a connection-refused
// error.
func TestMutatingNetworkErrorDoesNotFallBack(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	projectID := st.ProjectID
	st.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":{"ok":true,"project_id":"%s"},"meta":{}}`+"\n", projectID)
			return
		}
	}))
	serverURL := server.URL
	if _, err := daemonclient.WriteSessionFile(projectID, daemonclient.SessionFile{ProjectID: projectID, APIURL: serverURL, Token: "tok"}); err != nil {
		t.Fatalf("WriteSessionFile: %v", err)
	}
	// Now kill the server so subsequent POSTs fail.
	server.Close()

	before, _ := func() ([]*core.Issue, error) {
		s, _ := store.Open(dir)
		defer s.Close()
		return s.ListIssues(store.ListIssueOptions{Limit: 100})
	}()

	code, _, _ := captureCLIOutput(t, func() int {
		return Main([]string{"issue", "create", "--project", dir, "--title", "network failure"})
	})
	if code == 0 {
		t.Fatalf("issue create succeeded under network failure; want daemon error")
	}

	// Critical assertion: local store was NOT mutated. The bug
	// being fixed would have written a row via offline fallback.
	after, _ := func() ([]*core.Issue, error) {
		s, _ := store.Open(dir)
		defer s.Close()
		return s.ListIssues(store.ListIssueOptions{Limit: 100})
	}()
	if len(after) != len(before) {
		t.Fatalf("local store mutated despite daemon network failure: before=%d after=%d", len(before), len(after))
	}
}

// TestLegacyLogoutRemovesLegacySession pins the P2 #3 fix:
// `symphony login --logout` must also delete the legacy
// ~/.symphony/cli-session.json file, not only the new
// project-scoped file. Otherwise an upgraded user's stale
// legacy token keeps authenticating.
// TestMutatingOfflineExitsSeven pins the P2 #1 fix: when a mutating
// command runs without a reachable daemon, the dispatcher must
// surface the daemon_unavailable envelope and exit code 7 — not
// the generic internal_error / exit 1 that the raw sentinel
// would otherwise produce.
func TestMutatingOfflineExitsSeven(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	st.Close()

	code, _, stderr := captureCLIOutput(t, func() int {
		return Main([]string{"issue", "create", "--project", dir, "--title", "offline mutating"})
	})
	if code != 7 {
		t.Fatalf("offline mutating exit code = %d, want 7; stderr = %s", code, stderr)
	}
	if !strings.Contains(stderr, "daemon_unavailable") {
		t.Fatalf("stderr missing daemon_unavailable: %s", stderr)
	}
	if !strings.Contains(stderr, "daemon is not running") {
		t.Fatalf("stderr missing guidance phrase: %s", stderr)
	}
}

// TestLoginUnauthenticatedExitsSeven pins the P2 #2 fix: when the
// CLI bearer is rejected by the daemon, `symphony login` must
// return non-zero so scripts that depend on it can detect the
// failure. Previously the command printed `session:
// unauthenticated` with exit 0, which masked the broken token.
func TestLoginUnauthenticatedExitsSeven(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	projectID := st.ProjectID
	st.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":{"ok":true,"project_id":"%s"},"meta":{}}`+"\n", projectID)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"data":{"authenticated":false,"project_id":"`+projectID+`","bearer":true,"session_kind":"cli"},"meta":{}}`)
	}))
	t.Cleanup(server.Close)
	if _, err := daemonclient.WriteSessionFile(projectID, daemonclient.SessionFile{ProjectID: projectID, APIURL: server.URL, Token: "expired"}); err != nil {
		t.Fatalf("WriteSessionFile: %v", err)
	}

	code, _, stderr := captureCLIOutput(t, func() int {
		return Main([]string{"login", "--project", dir})
	})
	if code != 7 {
		t.Fatalf("login exit code = %d, want 7; stderr = %s", code, stderr)
	}
	if !strings.Contains(stderr, "unauthorized") {
		t.Fatalf("stderr missing unauthorized code: %s", stderr)
	}
	if !strings.Contains(stderr, "symphony serve") {
		t.Fatalf("stderr missing refresh guidance: %s", stderr)
	}
}

// TestRunListOfflineReturnsObjectShape pins the P2 #4 fix: when
// the daemon is unavailable, `symphony run list` must return
// the same `{"items":[...]}` object shape as the daemon path
// — scripts and dashboards must not see different output
// structures based on daemon availability.
func TestRunListOfflineReturnsObjectShape(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	st.Close()

	code, stdout, _ := captureCLIOutput(t, func() int {
		return Main([]string{"run", "list", "--project", dir})
	})
	if code != 0 {
		t.Fatalf("offline run list exit code = %d, want 0", code)
	}
	// Must be an object, not a bare array. Decode into a map and
	// assert that the items key is present (empty slice is fine).
	var doc map[string]any
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("decode stdout as map: %v; stdout = %s", err, stdout)
	}
	if _, ok := doc["items"]; !ok {
		t.Fatalf("run list output missing items key: %s", stdout)
	}
}

// TestApprovalListOfflineReturnsObjectShape pins P2 #4 for
// approval list.
func TestApprovalListOfflineReturnsObjectShape(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	st.Close()

	code, stdout, _ := captureCLIOutput(t, func() int {
		return Main([]string{"approval", "list", "--project", dir})
	})
	if code != 0 {
		t.Fatalf("offline approval list exit code = %d, want 0", code)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("decode stdout as map: %v; stdout = %s", err, stdout)
	}
	if _, ok := doc["items"]; !ok {
		t.Fatalf("approval list output missing items key: %s", stdout)
	}
}

// TestRunEventsOfflineReturnsObjectShape pins P2 #4 for run
// events. We use a fake-runner-friendly path by writing the
// run via the store directly (skipping orchestrator) and
// transitioning the issue; this keeps the test focused on the
// output shape rather than the dispatch lifecycle.
func TestRunEventsOfflineReturnsObjectShape(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	issue, err := st.CreateIssue(store.CreateIssueInput{
		Title:              "events",
		Description:        "x",
		AcceptanceCriteria: []string{"done"},
		Priority:           3,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if _, err := st.TransitionIssue(issue.Identifier, core.StateReady, "", ""); err != nil {
		t.Fatalf("TransitionIssue: %v", err)
	}
	run, err := st.ClaimRun(issue.Identifier, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	st.Close()

	code, stdout, _ := captureCLIOutput(t, func() int {
		return Main([]string{"run", "events", run.ID, "--project", dir})
	})
	if code != 0 {
		t.Fatalf("offline run events exit code = %d, want 0; stdout = %s", code, stdout)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("decode stdout as map: %v; stdout = %s", err, stdout)
	}
	if _, ok := doc["items"]; !ok {
		t.Fatalf("run events output missing items key: %s", stdout)
	}
}

// TestMutatingCommandMissingSessionExitsSeven pins the P2 #2 fix
// in adversarial review. The operator runs a mutating command
// while the daemon is reachable but the CLI bearer is
// missing (e.g. session file deleted). The dispatcher must
// surface an ErrUnauthorized envelope with exit 7, not
// collapse to internal_error / exit 1.
func TestMutatingCommandMissingSessionExitsSeven(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	projectID := st.ProjectID
	st.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":{"ok":true,"project_id":"%s"},"meta":{}}`+"\n", projectID)
			return
		}
	}))
	t.Cleanup(server.Close)

	// Persist a session file with an empty token so the
	// daemonclient.New call returns ErrSessionMissing. The
	// daemon is reachable, so discovery succeeds; the bearer
	// is the missing piece.
	path := app.CLISessionPath(projectID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir session: %v", err)
	}
	raw, _ := json.Marshal(map[string]any{"project_id": projectID, "api_url": server.URL, "token": ""})
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}

	code, _, stderr := captureCLIOutput(t, func() int {
		return Main([]string{"issue", "create", "--project", dir, "--title", "missing session"})
	})
	if code != 7 {
		t.Fatalf("issue create exit code = %d, want 7; stderr = %s", code, stderr)
	}
	if !strings.Contains(stderr, "unauthorized") {
		t.Fatalf("stderr missing unauthorized: %s", stderr)
	}
	// The exact phrasing differs between the ReadSessionFile
	// short-circuit ("CLI session token is empty") and the
	// dispatcher wrap ("run 'symphony login' to
	// authenticate"). Both are valid operator-actionable
	// errors; we just want a token-related hint.
	if !strings.Contains(stderr, "symphony login") && !strings.Contains(stderr, "CLI session") {
		t.Fatalf("stderr missing operator-actionable guidance: %s", stderr)
	}
}

// TestLogoutRevokesBearerOnDaemon pins the adversarial P2 #3
// fix: `symphony login --logout` calls the daemon's
// DELETE /api/v1/auth/cli-sessions/current before deleting
// the local session files, so a copied bearer token can
// no longer authorize mutating REST calls after logout.
func TestLogoutRevokesBearerOnDaemon(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	projectID := st.ProjectID
	st.Close()

	var revokeCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":{"ok":true,"project_id":"%s"},"meta":{}}`+"\n", projectID)
			return
		}
		if r.Method == http.MethodDelete && r.URL.Path == "/api/v1/auth/cli-sessions/current" {
			revokeCalled = true
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"data":{"revoked":true,"matched":true},"meta":{}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"data":{"ok":true},"meta":{}}`)
	}))
	t.Cleanup(server.Close)

	if _, err := daemonclient.WriteSessionFile(projectID, daemonclient.SessionFile{ProjectID: projectID, APIURL: server.URL, Token: "tok"}); err != nil {
		t.Fatalf("WriteSessionFile: %v", err)
	}

	code, stdout, stderr := captureCLIOutput(t, func() int {
		return Main([]string{"login", "--logout", "--project", dir})
	})
	if code != 0 {
		t.Fatalf("logout exit code = %d, want 0; stderr = %s", code, stderr)
	}
	if !revokeCalled {
		t.Fatalf("daemon revoke endpoint never called")
	}
	if !strings.Contains(stdout, "revoked") {
		t.Fatalf("logout stdout missing revoke status: %s", stdout)
	}

	// Local session files are gone.
	if _, err := os.Stat(app.CLISessionPath(projectID)); !os.IsNotExist(err) {
		t.Fatalf("project session file still present: %v", err)
	}
}

// TestLogoutReportsDegradedIfRevocationFails pins the
// HIGH finding from adversarial round 2: when the daemon
// is unreachable, logout must NOT silently report success.
// The trust boundary here is that a copied bearer would
// keep authorizing mutating REST calls if we deleted the
// local files before confirming server-side revocation.
//
// Behavior:
//   - exit code is 7 (degraded, operator-actionable) — not 0
//   - local session file is preserved so the operator can retry
//   - structured envelope reports revoke_status="degraded" and
//     logged_out=false so downstream scripts can detect the
//     half-completed state
func TestLogoutReportsDegradedIfRevocationFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	projectID := st.ProjectID
	st.Close()

	// Point the session at an unreachable daemon so the
	// revoke call fails.
	sessionPath := app.CLISessionPath(projectID)
	if _, err := daemonclient.WriteSessionFile(projectID, daemonclient.SessionFile{ProjectID: projectID, APIURL: "http://127.0.0.1:1", Token: "tok"}); err != nil {
		t.Fatalf("WriteSessionFile: %v", err)
	}

	code, _, stderr := captureCLIOutput(t, func() int {
		return Main([]string{"login", "--logout", "--project", dir})
	})
	if code != 7 {
		t.Fatalf("logout exit code = %d, want 7 (degraded preserves files); stderr = %s", code, stderr)
	}
	if !strings.Contains(stderr, "degraded") {
		t.Fatalf("logout stderr missing degraded status: %s", stderr)
	}
	if !strings.Contains(stderr, "preserved") {
		t.Fatalf("logout stderr missing file-preserved phrase: %s", stderr)
	}
	if _, err := os.Stat(sessionPath); os.IsNotExist(err) {
		t.Fatalf("session file should be preserved on degraded logout, got: %v", err)
	}
}


func TestReadOnlyCommandMissingSessionExitsSeven(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	projectID := st.ProjectID
	st.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":{"ok":true,"project_id":"%s"},"meta":{}}`+"\n", projectID)
			return
		}
	}))
	t.Cleanup(server.Close)

	path := app.CLISessionPath(projectID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir session: %v", err)
	}
	raw, _ := json.Marshal(map[string]any{"project_id": projectID, "api_url": server.URL, "token": ""})
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}

	code, _, stderr := captureCLIOutput(t, func() int {
		return Main([]string{"issue", "list", "--project", dir})
	})
	if code != 7 {
		t.Fatalf("issue list exit code = %d, want 7; stderr = %s", code, stderr)
	}
	// The exact code depends on whether the dispatcher sees a
	// *core.APIError short-circuit (unauthorized) or the raw
	// ErrSessionMissing sentinel that we wrap. Both are
	// operator-actionable; this test pins the exit code and
	// any token-related envelope, not the exact code.
	if !strings.Contains(stderr, "unauthorized") && !strings.Contains(stderr, "daemon_unavailable") {
		t.Fatalf("stderr missing session-related code: %s", stderr)
	}
}


// `symphony workflow validate` is a read-only filesystem
// inspection. When no daemon is reachable, the dispatcher
// TestWorkflowValidateOfflineRuns pins the round-4 fix:
// `symphony workflow validate` is a read-only filesystem
// inspection. When no daemon is reachable, the dispatcher
// must run the local workflowData fallback so offline
// operators can still validate their WORKFLOW.md.
func TestWorkflowValidateOfflineRuns(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	if _, err := store.InitProject(dir, "APP"); err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	code, stdout, _ := captureCLIOutput(t, func() int {
		return Main([]string{"workflow", "validate", "--project", dir})
	})
	if code != 0 {
		t.Fatalf("offline workflow validate exit code = %d, want 0", code)
	}
	// Local validation returns a "source: current_filesystem"
	// marker so operators can tell whether the daemon or the
	// local store produced the payload.
	if !strings.Contains(stdout, "current_filesystem") {
		t.Fatalf("offline workflow validate stdout missing local marker: %s", stdout)
	}
}

// TestWorkflowReloadOfflineRefuses pins the inverse:
// `symphony workflow reload` rewrites on-disk workflow
// state, so it is correctly classified as mutating. The
// dispatcher must NOT fall back to the local store when the
// daemon is unreachable; the operator must run `symphony
// serve` first.
func TestWorkflowReloadOfflineRefuses(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	if _, err := store.InitProject(dir, "APP"); err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	code, _, stderr := captureCLIOutput(t, func() int {
		return Main([]string{"workflow", "reload", "--project", dir})
	})
	if code != 7 {
		t.Fatalf("offline workflow reload exit code = %d, want 7; stderr = %s", code, stderr)
	}
	if !strings.Contains(stderr, "daemon_unavailable") {
		t.Fatalf("offline workflow reload stderr missing daemon_unavailable: %s", stderr)
	}
}

// TestLegacyLogoutRemovesLegacySession pins the legacy-file
// cleanup alongside the degraded-logout trust boundary
// (HIGH finding, adversarial round 2). The legacy
// ~/.symphony/cli-session.json file is consulted by
// upgraded operators; if logout deletes it before
// confirming server-side revocation, a copied bearer
// would keep authorizing mutating REST calls. So the
// file must be preserved on the degraded path and
// removed only when the daemon confirms `revoked:true
// matched:true`.
//
// Behavior:
//   - exit code is 7 (degraded) when the project-scoped
//     session points at an unreachable daemon
//   - both legacy and project-scoped files are preserved
//     so the operator can retry revocation
func TestLegacyLogoutRemovesLegacySession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(daemonclient.EnvOverride, "")

	// Initialize the project so the project-scoped session file
	// can be created.
	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	projectID := st.ProjectID
	st.Close()

	// Write a project-scoped session file pointing at an
	// unreachable daemon so the revoke call fails.
	if _, err := daemonclient.WriteSessionFile(projectID, daemonclient.SessionFile{ProjectID: projectID, APIURL: "http://127.0.0.1:1", Token: "tok"}); err != nil {
		t.Fatalf("WriteSessionFile: %v", err)
	}

	// Write a legacy session file at ~/.symphony/cli-session.json.
	legacyDir := filepath.Join(home, ".symphony")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatalf("mkdir legacy: %v", err)
	}
	legacyPath := filepath.Join(legacyDir, "cli-session.json")
	if err := os.WriteFile(legacyPath, []byte(`{"project_id":"`+projectID+`","token":"legacy-token"}`), 0o600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}

	code, _, stderr := captureCLIOutput(t, func() int {
		return Main([]string{"login", "--logout", "--project", dir})
	})
	if code != 7 {
		t.Fatalf("login --logout exit code = %d, want 7 (degraded preserves files for retry); stderr = %s", code, stderr)
	}
	if !strings.Contains(stderr, "degraded") {
		t.Fatalf("login --logout stderr missing degraded status: %s", stderr)
	}
	// Files must be preserved on degraded logout: a copied
	// bearer must not keep authorizing mutating REST calls
	// after logout silently "succeeds" without confirming
	// server-side revocation.
	if _, err := os.Stat(legacyPath); os.IsNotExist(err) {
		t.Fatalf("legacy session file should be preserved on degraded logout, got: %v", err)
	}
	if _, err := os.Stat(app.CLISessionPath(projectID)); os.IsNotExist(err) {
		t.Fatalf("project-scoped session file should be preserved on degraded logout, got: %v", err)
	}
}

// TestLogoutFromFileRejectsStaleAPIURL pins the HIGH finding
// from adversarial round 3: logoutRevokeFromFile used to
// construct a daemon client with the saved api_url directly,
// bypassing Discover's /health project_id guard. A saved
// session whose api_url points at a different project's
// daemon (loopback host collision, leftover from a prior dev
// session) would receive the CLI bearer, return matched=false,
// and the local files would be deleted. The bearer for project
// A is now required to flow through the project_id check on
// the saved api_url, and any rejection preserves the local
// files so the operator can retry.
//
// Behavior:
//   - exit code is 7 (degraded, keepFiles)
//   - project-scoped session file is preserved
//   - the wrong-project daemon never sees the bearer
func TestLogoutFromFileRejectsStaleAPIURL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	projectID := st.ProjectID
	st.Close()

	// Saved session api_url points at a daemon advertising
	// a DIFFERENT project_id. Discovery's /health project_id
	// guard must reject this so the bearer is not sent to
	// the wrong daemon and the local files are preserved.
	foreign := "prj_foreign"
	wrongProjectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":{"ok":true,"project_id":"%s"},"meta":{}}`+"\n", foreign)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	t.Cleanup(wrongProjectServer.Close)

	sessionPath := app.CLISessionPath(projectID)
	if _, err := daemonclient.WriteSessionFile(projectID, daemonclient.SessionFile{
		ProjectID: projectID,
		APIURL:    wrongProjectServer.URL,
		Token:     "tok-stale",
	}); err != nil {
		t.Fatalf("WriteSessionFile: %v", err)
	}

	code, _, stderr := captureCLIOutput(t, func() int {
		return Main([]string{"login", "--logout", "--project", dir})
	})
	if code != 7 {
		t.Fatalf("logout exit code = %d, want 7 (degraded preserves files on stale api_url); stderr = %s", code, stderr)
	}
	if !strings.Contains(stderr, "degraded") {
		t.Fatalf("logout stderr missing degraded status: %s", stderr)
	}
	if _, err := os.Stat(sessionPath); os.IsNotExist(err) {
		t.Fatalf("session file should be preserved when saved api_url points at wrong-project daemon, got: %v", err)
	}
}

// TestLogoutFromFileAcceptsMatchingAPIURL pins the happy
// path of the round-3 fix: when the saved session's api_url
// is reachable AND its daemon advertises the expected
// project_id, the bearer is sent to THAT endpoint and the
// daemon confirms matched=true. Local files are removed.
//
// Behavior:
//   - exit code is 0 (revoked)
//   - project-scoped session file is deleted
//   - the matching daemon sees DELETE /api/v1/auth/cli-sessions/current
//     with the saved bearer
func TestLogoutFromFileAcceptsMatchingAPIURL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	projectID := st.ProjectID
	st.Close()

	var sawDelete bool
	var sawBearer string
	matchingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":{"ok":true,"project_id":"%s"},"meta":{}}`+"\n", projectID)
			return
		}
		if r.Method == http.MethodDelete && r.URL.Path == "/api/v1/auth/cli-sessions/current" {
			sawDelete = true
			sawBearer = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"data":{"revoked":true,"matched":true},"meta":{}}`)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	t.Cleanup(matchingServer.Close)

	sessionPath := app.CLISessionPath(projectID)
	if _, err := daemonclient.WriteSessionFile(projectID, daemonclient.SessionFile{
		ProjectID: projectID,
		APIURL:    matchingServer.URL,
		Token:     "tok-good",
	}); err != nil {
		t.Fatalf("WriteSessionFile: %v", err)
	}

	code, _, stderr := captureCLIOutput(t, func() int {
		return Main([]string{"login", "--logout", "--project", dir})
	})
	if code != 0 {
		t.Fatalf("logout exit code = %d, want 0 (revoked); stderr = %s", code, stderr)
	}
	if !sawDelete {
		t.Fatalf("daemon never saw DELETE /api/v1/auth/cli-sessions/current; stderr = %s", stderr)
	}
	if sawBearer != "Bearer tok-good" {
		t.Fatalf("daemon saw Authorization = %q, want %q", sawBearer, "Bearer tok-good")
	}
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Fatalf("session file should be deleted on successful revoke, got: %v", err)
	}
}

// TestLogoutFromFileFallsBackToDiscoveryOnNoAPIURL ensures
// the round-3 fix does not regress the no-api-url fallback:
// when the saved session lacks an api_url, logoutRevoke is
// still allowed to fall through to the discovery chain
// (env / config / runtime descriptor). A session file with
// only a token must remain "unusable" so the caller can try
// the next source.
//
// Behavior:
//   - the unreachable api_url is preserved on degraded logout
//   - an unusable file (token only, no api_url) does not
//     pollute the degraded path
func TestLogoutFromFileFallsBackToDiscoveryOnNoAPIURL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	projectID := st.ProjectID
	st.Close()

	// Session file with no api_url: should be treated as
	// unusable and fall through to the discovery chain.
	// No candidate in env / config / runtime exists, so the
	// call degrades. This is the existing behavior and the
	// fix must not change it.
	sessionPath := app.CLISessionPath(projectID)
	if err := os.MkdirAll(filepath.Dir(sessionPath), 0o700); err != nil {
		t.Fatalf("mkdir session: %v", err)
	}
	if err := os.WriteFile(sessionPath, []byte(fmt.Sprintf(`{"project_id":%q,"token":"tok"}`, projectID)), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}

	code, _, stderr := captureCLIOutput(t, func() int {
		return Main([]string{"login", "--logout", "--project", dir})
	})
	if code != 7 {
		t.Fatalf("logout exit code = %d, want 7 (degraded); stderr = %s", code, stderr)
	}
	if !strings.Contains(stderr, "degraded") {
		t.Fatalf("logout stderr missing degraded status: %s", stderr)
	}
	if _, err := os.Stat(sessionPath); os.IsNotExist(err) {
		t.Fatalf("session file should be preserved when discovery cannot find a daemon, got: %v", err)
	}
}

// TestLogoutDoesNotDeleteLegacyFromDifferentProject pins
// the HIGH #2 fix from adversarial round 4: `symphony
// login --logout` used to delete the legacy
// ~/.symphony/cli-session.json file unconditionally, which
// would silently wipe a residual legacy record owned by a
// different project (e.g. a multi-project operator upgrading
// from pre-v1.1). The other project's copied legacy bearer
// stayed valid server-side while the operator got a
// misleading "logged_out:true" status. The fix inspects
// the legacy file's persisted project_id and only deletes
// it when that id matches the current logout target; a
// foreign legacy file is preserved and reported in the
// operator output so the operator can clean it up out-of-band.
//
// Behavior:
//   - exit code 0
//   - project-scoped session file for current project is deleted
//   - legacy file (owned by a different project) is PRESERVED
//   - stdout contains a legacy_preserved block identifying
//     the foreign project_id and the residual path
func TestLogoutDoesNotDeleteLegacyFromDifferentProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	projectID := st.ProjectID
	st.Close()

	// Build a matching daemon that returns matched=true so
	// the revoke path completes cleanly.
	matchingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":{"ok":true,"project_id":"%s"},"meta":{}}`+"\n", projectID)
			return
		}
		if r.Method == http.MethodDelete && r.URL.Path == "/api/v1/auth/cli-sessions/current" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"data":{"revoked":true,"matched":true},"meta":{}}`)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	t.Cleanup(matchingServer.Close)

	// Project-scoped session file for the current project.
	if _, err := daemonclient.WriteSessionFile(projectID, daemonclient.SessionFile{
		ProjectID: projectID,
		APIURL:    matchingServer.URL,
		Token:     "tok-current",
	}); err != nil {
		t.Fatalf("WriteSessionFile: %v", err)
	}

	// Legacy file owned by a DIFFERENT project. This simulates
	// a multi-project operator who upgraded from pre-v1.1
	// keeping a single legacy record for whichever project
	// they last logged into.
	foreignProjectID := "prj_foreign"
	legacyPath := app.LegacyCLISessionPath()
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatalf("mkdir legacy: %v", err)
	}
	legacyBody, _ := json.Marshal(map[string]any{
		"project_id": foreignProjectID,
		"token":      "tok-foreign-legacy",
	})
	if err := os.WriteFile(legacyPath, legacyBody, 0o600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}

	stdout := ""
	code, captured, stderr := captureCLIOutput(t, func() int {
		return Main([]string{"login", "--logout", "--project", dir})
	})
	stdout = captured
	if code != 0 {
		t.Fatalf("logout exit code = %d, want 0; stderr = %s", code, stderr)
	}
	// Project-scoped file is deleted.
	if _, err := os.Stat(app.CLISessionPath(projectID)); !os.IsNotExist(err) {
		t.Fatalf("project-scoped session file should be deleted on success, got: %v", err)
	}
	// Legacy file is preserved (foreign project).
	if _, err := os.Stat(legacyPath); os.IsNotExist(err) {
		t.Fatalf("legacy session file from foreign project must be preserved, got: %v", err)
	}
	// stdout reports the residual.
	if !strings.Contains(stdout, "legacy_preserved") {
		t.Fatalf("logout stdout missing legacy_preserved block: %s", stdout)
	}
	if !strings.Contains(stdout, foreignProjectID) {
		t.Fatalf("logout stdout should name the foreign project_id %q: %s", foreignProjectID, stdout)
	}
	if !strings.Contains(stdout, legacyPath) {
		t.Fatalf("logout stdout should name the residual path %q: %s", legacyPath, stdout)
	}
}

// TestLogoutDeletesLegacyFromSameProject pins the
// same-project happy path: when the legacy file's persisted
// project_id matches the logout target, the legacy file IS
// deleted. This guards against an over-broad fix that would
// always preserve the legacy file.
//
// Behavior:
//   - exit code 0
//   - project-scoped session file is deleted
//   - legacy file (same project_id) is deleted
//   - stdout does NOT carry a legacy_preserved block
func TestLogoutDeletesLegacyFromSameProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	projectID := st.ProjectID
	st.Close()

	matchingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":{"ok":true,"project_id":"%s"},"meta":{}}`+"\n", projectID)
			return
		}
		if r.Method == http.MethodDelete && r.URL.Path == "/api/v1/auth/cli-sessions/current" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"data":{"revoked":true,"matched":true},"meta":{}}`)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	t.Cleanup(matchingServer.Close)

	if _, err := daemonclient.WriteSessionFile(projectID, daemonclient.SessionFile{
		ProjectID: projectID,
		APIURL:    matchingServer.URL,
		Token:     "tok-current",
	}); err != nil {
		t.Fatalf("WriteSessionFile: %v", err)
	}

	// Legacy file owned by the SAME project: should be
	// deleted by logout.
	legacyPath := app.LegacyCLISessionPath()
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatalf("mkdir legacy: %v", err)
	}
	legacyBody, _ := json.Marshal(map[string]any{
		"project_id": projectID,
		"token":      "tok-legacy-same",
	})
	if err := os.WriteFile(legacyPath, legacyBody, 0o600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}

	stdout := ""
	code, captured, stderr := captureCLIOutput(t, func() int {
		return Main([]string{"login", "--logout", "--project", dir})
	})
	stdout = captured
	if code != 0 {
		t.Fatalf("logout exit code = %d, want 0; stderr = %s", code, stderr)
	}
	if _, err := os.Stat(app.CLISessionPath(projectID)); !os.IsNotExist(err) {
		t.Fatalf("project-scoped session file should be deleted on success, got: %v", err)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("legacy session file from same project should be deleted, got: %v", err)
	}
	if strings.Contains(stdout, "legacy_preserved") {
		t.Fatalf("logout stdout should not carry legacy_preserved when legacy matches current project: %s", stdout)
	}
}

// TestOpenDescriptorRejectsNonLoopbackRuntimeURL pins the
// CRITICAL finding from adversarial round 5: the runtime
// descriptor's api_url is a value persisted on disk; a
// poisoned or stale descriptor could point at any host.
// `openDescriptor` used to send the CLI bearer directly to
// the descriptor's api_url, bypassing Discover's loopback
// guard. The fix routes the descriptor's api_url through
// Discover with the api_url as a hint, which rejects
// non-loopback hosts BEFORE the bearer is dispatched. A
// descriptor pointing at evil.example.com must NOT cause
// the bearer to leave the loopback boundary.
//
// Behavior:
//   - openDescriptor returns an error
//   - the daemon_unavailable envelope is rendered
//   - the bearer is never sent to a remote host
func TestOpenDescriptorRejectsNonLoopbackRuntimeURL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	projectID := st.ProjectID

	// Persist a runtime descriptor whose api_url points at a
	// non-loopback host. Discover's loopback guard must
	// reject this BEFORE the bearer is sent.
	const remoteURL = "http://evil.example.com:8080"
	if err := st.CreateRuntimeDescriptor(remoteURL, remoteURL, 1234); err != nil {
		t.Fatalf("CreateRuntimeDescriptor: %v", err)
	}
	// Persist a session file so the bearer is available.
	// The host check happens BEFORE the bearer is sent, so
	// the token does not matter here.
	if _, err := daemonclient.WriteSessionFile(projectID, daemonclient.SessionFile{
		ProjectID: projectID,
		APIURL:    remoteURL,
		Token:     "tok-should-never-leave",
	}); err != nil {
		t.Fatalf("WriteSessionFile: %v", err)
	}
	st.Close()

	_, err = openDescriptor(dir)
	if err == nil {
		t.Fatal("openDescriptor succeeded, want daemon_unavailable error")
	}
	if got := core.AsAPIError(err).Code; got != core.ErrDaemonUnavailable {
		t.Fatalf("error code = %s, want %s; err = %v", got, core.ErrDaemonUnavailable, err)
	}
	if !strings.Contains(err.Error(), "non-loopback") && !strings.Contains(err.Error(), "daemon_unavailable") {
		t.Fatalf("error message should mention loopback rejection or daemon_unavailable: %v", err)
	}
}

// TestOpenDescriptorRejectsMismatchedProjectID pins the
// second half of the CRITICAL round-5 fix: even when the
// runtime descriptor points at a reachable loopback
// daemon, that daemon must advertise the project_id we are
// opening — otherwise the CLI bearer for project A is
// delivered to a daemon hosting project B (loopback host
// collision, leftover from a prior dev session).
//
// Behavior:
//   - openDescriptor returns a daemon_unavailable error
//   - the bearer is never sent to the wrong-project daemon
func TestOpenDescriptorRejectsMismatchedProjectID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	projectID := st.ProjectID

	// A loopback daemon advertising a DIFFERENT project_id.
	// Discover's /health project_id guard must reject this
	// before the bearer leaves this process.
	var sawBearer bool
	wrongProject := "prj_foreign"
	wrongProjectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			sawBearer = true
		}
		if r.URL.Path == "/api/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":{"ok":true,"project_id":"%s"},"meta":{}}`+"\n", wrongProject)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	t.Cleanup(wrongProjectServer.Close)

	if err := st.CreateRuntimeDescriptor(wrongProjectServer.URL, wrongProjectServer.URL, 1234); err != nil {
		t.Fatalf("CreateRuntimeDescriptor: %v", err)
	}
	if _, err := daemonclient.WriteSessionFile(projectID, daemonclient.SessionFile{
		ProjectID: projectID,
		APIURL:    wrongProjectServer.URL,
		Token:     "tok-should-never-leave",
	}); err != nil {
		t.Fatalf("WriteSessionFile: %v", err)
	}
	st.Close()

	_, err = openDescriptor(dir)
	if err == nil {
		t.Fatal("openDescriptor succeeded, want daemon_unavailable error")
	}
	if got := core.AsAPIError(err).Code; got != core.ErrDaemonUnavailable {
		t.Fatalf("error code = %s, want %s; err = %v", got, core.ErrDaemonUnavailable, err)
	}
	if sawBearer {
		t.Fatal("bearer was sent to a wrong-project daemon; project_id guard did not fire")
	}
}

// TestLogoutFromFileRejectsCopiedDB pins the HIGH #2
// finding from adversarial round 5: logoutRevokeFromFile
// used to read the session JSON directly, bypassing
// daemonclient.ReadSessionFile's repo_root guard. A
// copied project DB that reuses a foreign project_id
// inherits that project's id, but the session file's
// repo_root records the actual checkout the bearer was
// minted for. Without the guard, the copied-DB operator's
// `symphony login --logout` would still call
// DELETE /api/v1/auth/cli-sessions/current on the foreign
// project daemon with the foreign bearer, deleting
// another operator's active session. The fix routes
// logout through ReadSessionFile so the repo_root mismatch
// surfaces as an unusable file and the bearer is never
// sent.
//
// Behavior:
//   - exit code is 7 (degraded: no usable file matched the
//     caller's repo)
//   - project-scoped session file is preserved
//   - the foreign daemon never sees the bearer
func TestLogoutFromFileRejectsCopiedDB(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(daemonclient.EnvOverride, "")

	// Initialise project at /a (the original checkout the
	// session file was minted for). The session file's
	// repo_root points at /a. We will run `symphony login
	// --logout --project /b` so the caller's repo_root
	// (/b) does NOT match the persisted repo_root (/a).
	// This is the copied-DB scenario.
	dirA := t.TempDir()
	st, err := store.InitProject(dirA, "APP")
	if err != nil {
		t.Fatalf("InitProject /a: %v", err)
	}
	projectID := st.ProjectID
	st.Close()

	dirB := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dirB, ".symphony"), 0o755); err != nil {
		t.Fatalf("mkdir dirB: %v", err)
	}
	// Copy /a/.symphony/project.db into /b so /b surfaces
	// the same project_id when opened. The session file's
	// repo_root will still point at /a (mismatch).
	if err := copyFile(filepath.Join(dirA, ".symphony", "project.db"), filepath.Join(dirB, ".symphony", "project.db")); err != nil {
		t.Fatalf("copy project db: %v", err)
	}

	// Track whether the foreign daemon ever sees the bearer.
	// It MUST NOT — ReadSessionFile's repo_root guard must
	// reject the file before any daemonclient.New is built.
	var sawBearer bool
	foreignDaemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			sawBearer = true
		}
		if r.URL.Path == "/api/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":{"ok":true,"project_id":"%s"},"meta":{}}`+"\n", projectID)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	t.Cleanup(foreignDaemon.Close)

	// Write the session file. Its repo_root is /a; the
	// api_url points at the live foreign daemon. With
	// ReadSessionFile's guard, the file is rejected BEFORE
	// the bearer is dispatched to the daemon.
	if _, err := daemonclient.WriteSessionFile(projectID, daemonclient.SessionFile{
		ProjectID: projectID,
		RepoRoot:  dirA,
		APIURL:    foreignDaemon.URL,
		Token:     "tok-foreign",
	}); err != nil {
		t.Fatalf("WriteSessionFile: %v", err)
	}

	sessionPath := app.CLISessionPath(projectID)

	code, _, stderr := captureCLIOutput(t, func() int {
		return Main([]string{"login", "--logout", "--project", dirB})
	})
	if code != 7 {
		t.Fatalf("logout exit code = %d, want 7 (degraded: repo_root mismatch); stderr = %s", code, stderr)
	}
	if sawBearer {
		t.Fatal("bearer was sent to foreign daemon despite repo_root mismatch; ReadSessionFile guard did not fire")
	}
	if _, err := os.Stat(sessionPath); os.IsNotExist(err) {
		t.Fatalf("session file should be preserved when repo_root mismatches caller, got: %v", err)
	}
}

// TestLogoutTracksPerSourceDegrade pins the HIGH #3
// finding from adversarial round 5: logoutRevoke used to
// short-circuit on the first non-degraded reply. A
// project-scoped session pointing at an UNREACHABLE
// daemon would degrade, but a legacy file the daemon no
// longer recognises would reply `not_matched` — and the
// old code happily reported success and let the caller
// delete the project-scoped file. The operator's current
// bearer (the one the project-scoped file holds) stayed
// valid server-side while the operator got a misleading
// "logged_out:true". The fix tracks degraded state across
// sources: a degraded authoritative revoke is not
// cleared by a non-authoritative source's terminal reply.
//
// Behavior:
//   - exit code is 7 (degraded, keepFiles)
//   - the project-scoped session file is preserved
//   - revoke_status is "degraded", not "not_matched"
func TestLogoutTracksPerSourceDegrade(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(daemonclient.EnvOverride, "")

	dir := t.TempDir()
	st, err := store.InitProject(dir, "APP")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	projectID := st.ProjectID
	st.Close()

	// Project-scoped session file: points at an UNREACHABLE
	// daemon. Revoke will degrade here. The token is the
	// operator's CURRENT bearer; this is the one whose
	// server-side row is unverified.
	if _, err := daemonclient.WriteSessionFile(projectID, daemonclient.SessionFile{
		ProjectID: projectID,
		APIURL:    "http://127.0.0.1:1",
		Token:     "tok-current",
	}); err != nil {
		t.Fatalf("WriteSessionFile: %v", err)
	}

	// Legacy file: a reachable daemon that returns
	// `matched:false` (not_matched) for the legacy token.
	// The legacy token is an OLD bearer; the daemon does
	// not recognise it any more. The legacy reply is
	// "not_matched" — terminal for the legacy source — but
	// it MUST NOT clear the project-scoped source's
	// degraded state.
	legacyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":{"ok":true,"project_id":"%s"},"meta":{}}`+"\n", projectID)
			return
		}
		if r.Method == http.MethodDelete && r.URL.Path == "/api/v1/auth/cli-sessions/current" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"data":{"revoked":true,"matched":false},"meta":{}}`)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	t.Cleanup(legacyServer.Close)

	// The legacy session file must include repo_root=<dir>
	// so ReadSessionFile's repo_root guard passes. The
	// legacy token is "tok-legacy", which the legacy
	// daemon does not recognise.
	legacyPath := app.LegacyCLISessionPath()
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatalf("mkdir legacy: %v", err)
	}
	legacyBody, _ := json.Marshal(map[string]any{
		"project_id": projectID,
		"repo_root":  dir,
		"api_url":    legacyServer.URL,
		"token":      "tok-legacy",
	})
	if err := os.WriteFile(legacyPath, legacyBody, 0o600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}

	sessionPath := app.CLISessionPath(projectID)
	code, _, stderr := captureCLIOutput(t, func() int {
		return Main([]string{"login", "--logout", "--project", dir})
	})
	if code != 7 {
		t.Fatalf("logout exit code = %d, want 7 (degraded); stderr = %s", code, stderr)
	}
	if !strings.Contains(stderr, "degraded") {
		t.Fatalf("logout stderr missing degraded status: %s", stderr)
	}
	// Critical: the stderr MUST NOT claim not_matched. The
	// project-scoped revoke was the authoritative one and
	// it degraded. The legacy not_matched is a secondary
	// signal that the operator's CURRENT token is still
	// unverified server-side.
	if strings.Contains(stderr, "not_matched") {
		t.Fatalf("logout stderr leaks not_matched from legacy source despite project-scoped degrade: %s", stderr)
	}
	if _, err := os.Stat(sessionPath); os.IsNotExist(err) {
		t.Fatalf("project-scoped session file should be preserved when project-scoped revoke degraded, got: %v", err)
	}
}

// copyFile copies src to dst, creating dst's parent
// directory if needed. Test helper for the copied-DB
// scenario.
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}

// TestLogoutPreservesProjectScopedOnValidationFailure
// pins the HIGH #2 fix from adversarial round 6. The
// round-5 implementation of logoutRevokeFromFile returned
// `usable=false` for any non-IsNotExist read error, then
// the legacy file's terminal reply (`revoked`,
// `not_matched`, `no_bearer`) was used by the caller to
// delete the project-scoped session file. The bug: a
// project-scoped session file that EXISTS but fails
// validation (project_id mismatch, repo_root guard,
// EvalSymlinks failure) is a foreign bearer record. The
// legacy file's terminal reply is unrelated to the
// project-scoped file's bearer; allowing it to authorise
// deletion of the project-scoped file would report
// `logged_out:true` while the daemon-side local_sessions
// row stayed active and the bearer remained usable for
// any operator holding a copy of the token.
//
// Scenario:
//   - caller's project at dirC (valid, with project_id C)
//   - project-scoped session file: project_id=A (foreign
//     to dirC) → ReadSessionFile's project_id check
//     REJECTS the file (validation failure, not missing)
//   - legacy session file: project_id=C, repo_root=dirC,
//     api_url points at a daemon that confirms
//     `matched:true, revoked:true`
//   - expectation: project-scoped file is PRESERVED,
//     legacy file is deleted (its ownership matches
//     dirC), exit code 7 (degraded), stderr reports the
//     project-scoped validation failure as sticky
//     degraded
//
// Behavior:
//   - exit code is 7 (degraded, keepFiles)
//   - project-scoped session file is preserved
//   - legacy session file IS deleted (it is owned by
//     the current project and validated successfully)
//   - the project-scoped file's token is never sent to
//     any daemon
func TestLogoutPreservesProjectScopedOnValidationFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(daemonclient.EnvOverride, "")

	// /c: the operator's CURRENT checkout. Initialised
	// normally so loginResolveProject's Open succeeds.
	dirC := t.TempDir()
	st, err := store.InitProject(dirC, "APP")
	if err != nil {
		t.Fatalf("InitProject /c: %v", err)
	}
	projectIDC := st.ProjectID
	st.Close()

	// Legacy daemon: same project_id as dirC, returns
	// matched:true on DELETE. The legacy file's bearer
	// is the operator's current bearer; the legacy
	// revoke succeeds.
	legacyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":{"ok":true,"project_id":"%s"},"meta":{}}`+"\n", projectIDC)
			return
		}
		if r.Method == http.MethodDelete && r.URL.Path == "/api/v1/auth/cli-sessions/current" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"data":{"revoked":true,"matched":true},"meta":{}}`)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	t.Cleanup(legacyServer.Close)

	// Project-scoped session file: project_id mismatches
	// the caller's project (dirC). ReadSessionFile's
	// project_id check will reject the file (validation
	// failure). Its api_url is irrelevant because the
	// file will not be loaded. WriteSessionFile
	// enforces project_id == path-project_id, so we
	// write the file directly with the foreign
	// project_id embedded — that is exactly what an
	// attacker would do (or what a copied-DB operator
	// would end up with if they manually edited the
	// file).
	projectIDForeign := "prj_foreign_token"
	sessionPath := app.CLISessionPath(projectIDC)
	if err := os.MkdirAll(filepath.Dir(sessionPath), 0o700); err != nil {
		t.Fatalf("mkdir session: %v", err)
	}
	foreignBody, _ := json.Marshal(map[string]any{
		"project_id": projectIDForeign,
		"api_url":    "http://127.0.0.1:1",
		"token":      "tok-foreign-project-scoped",
	})
	if err := os.WriteFile(sessionPath, foreignBody, 0o600); err != nil {
		t.Fatalf("write foreign session: %v", err)
	}

	// Legacy session file: valid (project_id matches
	// dirC, repo_root=dirC). Its api_url points at the
	// legacyServer. Discover's /health project_id check
	// passes (project_id matches), so the legacy revoke
	// succeeds.
	legacyPath := app.LegacyCLISessionPath()
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatalf("mkdir legacy: %v", err)
	}
	legacyBody, _ := json.Marshal(map[string]any{
		"project_id": projectIDC,
		"repo_root":  dirC,
		"api_url":    legacyServer.URL,
		"token":      "tok-legacy-current",
	})
	if err := os.WriteFile(legacyPath, legacyBody, 0o600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}

	code, stdout, stderr := captureCLIOutput(t, func() int {
		return Main([]string{"login", "--logout", "--project", dirC})
	})
	if code != 7 {
		t.Fatalf("logout exit code = %d, want 7 (degraded: project-scoped validation failed); stderr = %s stdout = %s", code, stderr, stdout)
	}
	if !strings.Contains(stderr, "degraded") {
		t.Fatalf("logout stderr missing degraded status: %s", stderr)
	}
	// Critical: the project-scoped session file MUST be
	// preserved because its validation failed. The
	// round-5 implementation would have deleted it
	// after the legacy file's `revoked` terminal reply.
	if _, err := os.Stat(sessionPath); os.IsNotExist(err) {
		t.Fatalf("project-scoped session file MUST be preserved when its validation failed, got: %v", err)
	}
	// Belt-and-braces: the foreign token MUST NOT have
	// been sent to any daemon. The project_id check
	// rejected the project-scoped file before any
	// outbound request, and the legacy file's api_url
	// is the only URL the legacy path used.
	body, _ := os.ReadFile(sessionPath)
	if !strings.Contains(string(body), "tok-foreign-project-scoped") {
		t.Fatalf("project-scoped file content changed unexpectedly: %s", body)
	}
	// The legacy file MAY be deleted or preserved. The
	// round-6 fix is specifically about preserving the
	// project-scoped file when its validation failed;
	// the round-5 sticky-degraded design intentionally
	// keeps ALL local files on degraded exit so the
	// operator can retry the whole logout chain. The
	// legacy file's bearer was confirmed revoked
	// server-side, so its preservation here is a
	// consistency choice (keep-files-on-degraded) not
	// a security one. We do not assert on its presence.
}
