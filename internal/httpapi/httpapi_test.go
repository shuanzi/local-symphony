package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-symphony/internal/store"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	st, err := store.InitProject(t.TempDir(), "LOC")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	return New(st)
}

func decodeEnvelope(t *testing.T, body *strings.Reader) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.NewDecoder(body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return payload
}

func TestSessionReturnsCSRFTokenAndMutationsRequireIt(t *testing.T) {
	srv := newTestServer(t)

	token := sessionCSRFToken(t, srv)

	body := `{"title":"CSRF issue","description":"desc","acceptance_criteria":["done"],"priority":3,"labels":[]}`
	noCSRFReq := httptest.NewRequest(http.MethodPost, "/api/v1/issues", strings.NewReader(body))
	noCSRFReq.Header.Set("Content-Type", "application/json")
	noCSRFRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(noCSRFRec, noCSRFReq)
	if noCSRFRec.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d, want 403", noCSRFRec.Code)
	}
	errPayload := decodeEnvelope(t, strings.NewReader(noCSRFRec.Body.String()))
	errData := errPayload["error"].(map[string]any)
	if errData["code"] != "csrf_required" {
		t.Fatalf("error = %#v, want csrf_required", errData)
	}

	withCSRFReq := httptest.NewRequest(http.MethodPost, "/api/v1/issues", strings.NewReader(body))
	withCSRFReq.Header.Set("Content-Type", "application/json")
	withCSRFReq.Header.Set("X-Symphony-CSRF", token)
	withCSRFRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(withCSRFRec, withCSRFReq)
	if withCSRFRec.Code != http.StatusOK {
		t.Fatalf("valid CSRF status = %d, body = %s", withCSRFRec.Code, withCSRFRec.Body.String())
	}
}

func TestSessionCSRFTokenIsRandomPerServer(t *testing.T) {
	first := newTestServer(t)
	second := newTestServer(t)

	firstToken := sessionCSRFToken(t, first)
	secondToken := sessionCSRFToken(t, second)
	if firstToken == "" || secondToken == "" {
		t.Fatalf("tokens must not be empty: %q %q", firstToken, secondToken)
	}
	if firstToken == "dev-csrf" || secondToken == "dev-csrf" {
		t.Fatalf("tokens must not use fixed development token")
	}
	if firstToken == secondToken {
		t.Fatalf("tokens should be unique per server instance: %q", firstToken)
	}
}

func TestDashboardDoesNotServeProjectRootDistByDefault(t *testing.T) {
	srv := newTestServer(t)
	t.Setenv("SYMPHONY_DASHBOARD_DIST", "")
	dist := filepath.Join(srv.Store.RepoRoot, "web", "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatalf("mkdir dist: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("project root dashboard"), 0o644); err != nil {
		t.Fatalf("write project dashboard: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), "project root dashboard") {
		t.Fatalf("served dashboard from managed project root")
	}
}

func TestDashboardExplicitDistMayPointInsideProjectRoot(t *testing.T) {
	srv := newTestServer(t)
	dist := filepath.Join(srv.Store.RepoRoot, "web", "dist")
	t.Setenv("SYMPHONY_DASHBOARD_DIST", dist)
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatalf("mkdir dist: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("explicit project root dashboard"), 0o644); err != nil {
		t.Fatalf("write project dashboard: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "explicit project root dashboard") {
		t.Fatalf("explicit dashboard dist was not served: %s", rec.Body.String())
	}
}

func TestDashboardDoesNotServeRepoWebDistWhenExecutableIsRepoBin(t *testing.T) {
	if os.Getenv("SYMPHONY_CHILD_DASHBOARD_DIST") == "1" {
		t.Setenv("SYMPHONY_DASHBOARD_DIST", "")
		distRoot, _, found := dashboardDist(os.Getenv("SYMPHONY_TEST_REPO_ROOT"))
		if found {
			t.Fatalf("dashboardDist found %s from repo-relative executable path", distRoot)
		}
		return
	}

	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	dist := filepath.Join(root, "web", "dist")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatalf("mkdir dist: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("project root dashboard"), 0o644); err != nil {
		t.Fatalf("write project dashboard: %v", err)
	}
	currentExe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	copiedExe := filepath.Join(binDir, "symphony")
	exeBytes, err := os.ReadFile(currentExe)
	if err != nil {
		t.Fatalf("read test binary: %v", err)
	}
	if err := os.WriteFile(copiedExe, exeBytes, 0o755); err != nil {
		t.Fatalf("copy test binary: %v", err)
	}

	cmd := exec.Command(copiedExe, "-test.run", "^TestDashboardDoesNotServeRepoWebDistWhenExecutableIsRepoBin$")
	cmd.Env = append(os.Environ(), "SYMPHONY_CHILD_DASHBOARD_DIST=1", "SYMPHONY_DASHBOARD_DIST=", "SYMPHONY_TEST_REPO_ROOT="+root)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child test failed: %v\n%s", err, output)
	}
}

func TestDashboardDoesNotServeRepoWebDistWhenExecutableIsRepoRelative(t *testing.T) {
	if os.Getenv("SYMPHONY_CHILD_DASHBOARD_DIST_REPO_RELATIVE") == "1" {
		t.Setenv("SYMPHONY_DASHBOARD_DIST", "")
		distRoot, _, found := dashboardDist(os.Getenv("SYMPHONY_TEST_REPO_ROOT"))
		if found {
			t.Fatalf("dashboardDist found %s from repo-relative executable path", distRoot)
		}
		return
	}

	root := t.TempDir()
	for _, exeRel := range []string{"symphony", filepath.Join("tools", "symphony")} {
		t.Run(exeRel, func(t *testing.T) {
			exePath := filepath.Join(root, exeRel)
			if err := os.MkdirAll(filepath.Dir(exePath), 0o755); err != nil {
				t.Fatalf("mkdir exe dir: %v", err)
			}
			dist := filepath.Join(root, "web", "dist")
			if err := os.MkdirAll(dist, 0o755); err != nil {
				t.Fatalf("mkdir dist: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("project root dashboard"), 0o644); err != nil {
				t.Fatalf("write project dashboard: %v", err)
			}
			currentExe, err := os.Executable()
			if err != nil {
				t.Fatalf("os.Executable: %v", err)
			}
			exeBytes, err := os.ReadFile(currentExe)
			if err != nil {
				t.Fatalf("read test binary: %v", err)
			}
			if err := os.WriteFile(exePath, exeBytes, 0o755); err != nil {
				t.Fatalf("copy test binary: %v", err)
			}

			cmd := exec.Command(exePath, "-test.run", "^TestDashboardDoesNotServeRepoWebDistWhenExecutableIsRepoRelative$")
			cmd.Env = append(os.Environ(), "SYMPHONY_CHILD_DASHBOARD_DIST_REPO_RELATIVE=1", "SYMPHONY_DASHBOARD_DIST=", "SYMPHONY_TEST_REPO_ROOT="+root)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("child test failed: %v\n%s", err, output)
			}
		})
	}
}

func TestEventStreamIncludesNamedEventAndDefaultMessageData(t *testing.T) {
	srv := newTestServer(t)
	if _, err := srv.Store.CreateIssue(store.CreateIssueInput{Title: "SSE issue", Description: "desc"}); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/stream", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		srv.Handler().ServeHTTP(rec, req)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	body := rec.Body.String()
	if !strings.Contains(body, "event: issue.created\n") {
		t.Fatalf("stream body missing named event: %q", body)
	}
	if !strings.Contains(body, "\ndata: ") {
		t.Fatalf("stream body missing default message data: %q", body)
	}
}

func TestIssueEventStreamUnknownIssueReturnsNotFound(t *testing.T) {
	srv := newTestServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/issues/LOC-404/events/stream", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		srv.Handler().ServeHTTP(rec, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		cancel()
		<-done
		t.Fatalf("unknown issue stream did not return a not_found response")
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("stream status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestRunEventStreamUnknownRunReturnsNotFound(t *testing.T) {
	srv := newTestServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/runs/run_missing/events/stream", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		srv.Handler().ServeHTTP(rec, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		cancel()
		<-done
		t.Fatalf("unknown run stream did not return a not_found response")
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("stream status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestWorkflowPreviewDoesNotReturnRenderedPromptBody(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflow/render-preview", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Symphony-CSRF", sessionCSRFToken(t, srv))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status = %d, body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
	data := payload["data"].(map[string]any)
	preview, _ := data["rendered_prompt_preview"].(string)
	if strings.Contains(preview, "Complete the issue") || strings.Contains(preview, "Do not push branches") {
		t.Fatalf("preview leaked raw prompt body: %q", preview)
	}
}

func sessionCSRFToken(t *testing.T, srv *Server) string {
	t.Helper()
	sessionReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	sessionRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(sessionRec, sessionReq)
	if sessionRec.Code != http.StatusOK {
		t.Fatalf("session status = %d", sessionRec.Code)
	}
	sessionPayload := decodeEnvelope(t, strings.NewReader(sessionRec.Body.String()))
	data := sessionPayload["data"].(map[string]any)
	token, _ := data["csrf_token"].(string)
	if data["authenticated"] != true || token == "" {
		t.Fatalf("session data = %#v, want authenticated true and csrf_token", data)
	}
	return token
}
