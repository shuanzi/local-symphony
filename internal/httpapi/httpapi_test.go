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

	"local-symphony/internal/core"
	"local-symphony/internal/security"
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
	auth := sessionAuth(t, srv)

	body := `{"title":"CSRF issue","description":"desc","acceptance_criteria":["done"],"priority":3,"labels":[]}`
	noSessionReq := httptest.NewRequest(http.MethodPost, "/api/v1/issues", strings.NewReader(body))
	noSessionReq.Header.Set("Content-Type", "application/json")
	noSessionReq.Header.Set("X-Symphony-CSRF", auth.csrf)
	noSessionRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(noSessionRec, noSessionReq)
	if noSessionRec.Code != http.StatusUnauthorized {
		t.Fatalf("missing session status = %d, want 401", noSessionRec.Code)
	}

	noCSRFReq := httptest.NewRequest(http.MethodPost, "/api/v1/issues", strings.NewReader(body))
	noCSRFReq.Header.Set("Content-Type", "application/json")
	addCookies(noCSRFReq, auth.cookies)
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
	applySessionAuth(withCSRFReq, auth)
	withCSRFRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(withCSRFRec, withCSRFReq)
	if withCSRFRec.Code != http.StatusOK {
		t.Fatalf("valid CSRF status = %d, body = %s", withCSRFRec.Code, withCSRFRec.Body.String())
	}
}

func TestSessionWithoutCookieDoesNotExposeCSRFToken(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("session status = %d", rec.Code)
	}
	payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
	data := payload["data"].(map[string]any)
	if data["authenticated"] != false {
		t.Fatalf("session data = %#v, want authenticated false", data)
	}
	if token, _ := data["csrf_token"].(string); token != "" {
		t.Fatalf("unauthenticated session leaked csrf_token %q", token)
	}
	if token, _ := data["csrf"].(string); token != "" {
		t.Fatalf("unauthenticated session leaked csrf %q", token)
	}
}

func TestExchangeRequiresValidOpenTokenAndCreatesSessionCookie(t *testing.T) {
	srv := newTestServer(t)

	badReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/exchange", strings.NewReader(`{"open_token":"not-valid"}`))
	badReq.Header.Set("Content-Type", "application/json")
	badRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusUnauthorized {
		t.Fatalf("invalid exchange status = %d, body = %s", badRec.Code, badRec.Body.String())
	}
	if cookies := badRec.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("invalid exchange set cookies: %#v", cookies)
	}

	openToken := insertOpenToken(t, srv)
	exchangeReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/exchange", strings.NewReader(`{"open_token":"`+openToken+`"}`))
	exchangeReq.Header.Set("Content-Type", "application/json")
	exchangeRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(exchangeRec, exchangeReq)
	if exchangeRec.Code != http.StatusOK {
		t.Fatalf("valid exchange status = %d, body = %s", exchangeRec.Code, exchangeRec.Body.String())
	}
	cookies := exchangeRec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatalf("valid exchange did not set a session cookie")
	}
	payload := decodeEnvelope(t, strings.NewReader(exchangeRec.Body.String()))
	data := payload["data"].(map[string]any)
	if data["authenticated"] != true {
		t.Fatalf("exchange data = %#v, want authenticated true", data)
	}
	if token, _ := data["csrf_token"].(string); token == "" {
		t.Fatalf("exchange data = %#v, want csrf_token", data)
	}

	sessionReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	for _, cookie := range cookies {
		sessionReq.AddCookie(cookie)
	}
	sessionRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(sessionRec, sessionReq)
	if sessionRec.Code != http.StatusOK {
		t.Fatalf("session status = %d, body = %s", sessionRec.Code, sessionRec.Body.String())
	}
	sessionPayload := decodeEnvelope(t, strings.NewReader(sessionRec.Body.String()))
	sessionData := sessionPayload["data"].(map[string]any)
	if sessionData["authenticated"] != true {
		t.Fatalf("session data = %#v, want authenticated true", sessionData)
	}
	if token, _ := sessionData["csrf_token"].(string); token == "" {
		t.Fatalf("session data = %#v, want csrf_token", sessionData)
	}

	reuseReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/exchange", strings.NewReader(`{"open_token":"`+openToken+`"}`))
	reuseReq.Header.Set("Content-Type", "application/json")
	reuseRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(reuseRec, reuseReq)
	if reuseRec.Code != http.StatusUnauthorized {
		t.Fatalf("reused exchange status = %d, body = %s", reuseRec.Code, reuseRec.Body.String())
	}
}

func TestOpenTokenRequiresBearerSession(t *testing.T) {
	srv := newTestServer(t)

	noBearerReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/open-token", nil)
	noBearerRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(noBearerRec, noBearerReq)
	if noBearerRec.Code != http.StatusUnauthorized {
		t.Fatalf("open-token without bearer status = %d, body = %s", noBearerRec.Code, noBearerRec.Body.String())
	}

	cliToken := insertLocalSession(t, srv, "cli")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/open-token", nil)
	req.Header.Set("Authorization", "Bearer "+cliToken)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("open-token with bearer status = %d, body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
	data := payload["data"].(map[string]any)
	openToken, _ := data["open_token"].(string)
	if openToken == "" {
		t.Fatalf("open-token data = %#v, want open_token", data)
	}

	exchangeReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/exchange", strings.NewReader(`{"open_token":"`+openToken+`"}`))
	exchangeReq.Header.Set("Content-Type", "application/json")
	exchangeRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(exchangeRec, exchangeReq)
	if exchangeRec.Code != http.StatusOK {
		t.Fatalf("exchange minted open token status = %d, body = %s", exchangeRec.Code, exchangeRec.Body.String())
	}
}

func TestLogoutRevokesBrowserSession(t *testing.T) {
	srv := newTestServer(t)
	auth := sessionAuth(t, srv)

	logoutReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	applySessionAuth(logoutReq, auth)
	logoutRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(logoutRec, logoutReq)
	if logoutRec.Code != http.StatusOK {
		t.Fatalf("logout status = %d, body = %s", logoutRec.Code, logoutRec.Body.String())
	}

	sessionReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	addCookies(sessionReq, auth.cookies)
	sessionRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(sessionRec, sessionReq)
	if sessionRec.Code != http.StatusOK {
		t.Fatalf("session status = %d, body = %s", sessionRec.Code, sessionRec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(sessionRec.Body.String()))
	data := payload["data"].(map[string]any)
	if data["authenticated"] != false {
		t.Fatalf("session data = %#v, want authenticated false after logout", data)
	}

	body := `{"title":"After logout","description":"desc","acceptance_criteria":["done"],"priority":3,"labels":[]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/issues", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	applySessionAuth(req, auth)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("mutation after logout status = %d, body = %s", rec.Code, rec.Body.String())
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

func TestCancelRunRejectsMalformedJSONWithoutCancelling(t *testing.T) {
	srv := newTestServer(t)
	issue, err := srv.Store.CreateIssue(store.CreateIssueInput{Title: "Cancel malformed", Description: "desc"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if _, err := srv.Store.TransitionIssue(issue.Identifier, core.StateReady, "", ""); err != nil {
		t.Fatalf("TransitionIssue: %v", err)
	}
	run, err := srv.Store.ClaimRun(issue.Identifier, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/runs/"+run.ID+"/cancel", strings.NewReader(`{"reason":`))
	req.Header.Set("Content-Type", "application/json")
	applySessionAuth(req, sessionAuth(t, srv))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed cancel status = %d, body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
	errData := payload["error"].(map[string]any)
	if errData["code"] != "invalid_request" {
		t.Fatalf("error = %#v, want invalid_request", errData)
	}
	after, err := srv.Store.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if after.Status == core.RunCancelled {
		t.Fatalf("malformed cancel request cancelled run %#v", after)
	}
}

func TestCancelRunAllowsEmptyBody(t *testing.T) {
	srv := newTestServer(t)
	issue, err := srv.Store.CreateIssue(store.CreateIssueInput{Title: "Cancel empty", Description: "desc"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if _, err := srv.Store.TransitionIssue(issue.Identifier, core.StateReady, "", ""); err != nil {
		t.Fatalf("TransitionIssue: %v", err)
	}
	run, err := srv.Store.ClaimRun(issue.Identifier, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/runs/"+run.ID+"/cancel", nil)
	applySessionAuth(req, sessionAuth(t, srv))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty cancel status = %d, body = %s", rec.Code, rec.Body.String())
	}
	after, err := srv.Store.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if after.Status != core.RunCancelled {
		t.Fatalf("run status = %s, want cancelled", after.Status)
	}
}

func TestCancelRunCompletedRunReturnsConflict(t *testing.T) {
	srv := newTestServer(t)
	run := prepareCompletedHTTPRun(t, srv)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/runs/"+run.ID+"/cancel", strings.NewReader(`{"reason":"too late"}`))
	req.Header.Set("Content-Type", "application/json")
	applySessionAuth(req, sessionAuth(t, srv))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("completed cancel status = %d, body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
	errData := payload["error"].(map[string]any)
	if errData["code"] != string(core.ErrInvalidStateTransition) {
		t.Fatalf("error = %#v, want invalid_state_transition", errData)
	}

	after, err := srv.Store.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if after.Status != core.RunCompleted {
		t.Fatalf("run status = %s, want completed", after.Status)
	}
}

func TestDiagnosticsExportReturnsErrorWhenArtifactInsertFails(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.Store.Project.Exec(`CREATE TRIGGER fail_diagnostic_artifact_insert BEFORE INSERT ON artifacts
WHEN NEW.kind = 'diagnostic'
BEGIN
  SELECT RAISE(FAIL, 'artifact insert failed');
END;`); err != nil {
		t.Fatalf("create failing artifact trigger: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/diagnostics/export", nil)
	applySessionAuth(req, sessionAuth(t, srv))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("diagnostics export status = %d, want 500; body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
	if data, ok := payload["data"].(map[string]any); ok {
		if artifactID, _ := data["artifact_id"].(string); artifactID != "" {
			t.Fatalf("error response included successful artifact_id %q", artifactID)
		}
	}
	if _, ok := payload["error"].(map[string]any); !ok {
		t.Fatalf("diagnostics export response missing error envelope: %#v", payload)
	}
}

func TestDiagnosticsExportReturnsArtifactIDAndPath(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/diagnostics/export", nil)
	applySessionAuth(req, sessionAuth(t, srv))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("diagnostics export status = %d, body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
	data := payload["data"].(map[string]any)
	if artifactID, _ := data["artifact_id"].(string); !strings.HasPrefix(artifactID, "art_") {
		t.Fatalf("artifact_id = %q, want art_ prefix", artifactID)
	}
	if path, _ := data["path"].(string); path == "" {
		t.Fatalf("diagnostics export data = %#v, want path", data)
	}
}

func TestApprovalNotPendingMapsToConflict(t *testing.T) {
	srv := newTestServer(t)
	run := prepareCompletedHTTPRun(t, srv)
	approvalID := core.NewID("apr_")
	if err := srv.Store.Project.Exec(`INSERT INTO approval_requests(id,run_id,issue_id,kind,status,request_json,created_at) VALUES(?,?,?,?,?,?,?)`, approvalID, run.ID, run.IssueID, "command", "denied", `{"command":"test"}`, core.Now()); err != nil {
		t.Fatalf("insert approval: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+approvalID+"/decide", strings.NewReader(`{"decision":"deny","reason":"again"}`))
	req.Header.Set("Content-Type", "application/json")
	applySessionAuth(req, sessionAuth(t, srv))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("approval not pending status = %d, body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
	errData := payload["error"].(map[string]any)
	if errData["code"] != string(core.ErrApprovalNotPending) {
		t.Fatalf("error = %#v, want approval_not_pending", errData)
	}
}

func TestInternalErrorMapsToHTTP500(t *testing.T) {
	rec := httptest.NewRecorder()
	apiErr(rec, core.NewError(core.ErrInternal, "boom", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("internal error status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestWorkflowPreviewDoesNotReturnRenderedPromptBody(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflow/render-preview", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	applySessionAuth(req, sessionAuth(t, srv))
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
	return sessionAuth(t, srv).csrf
}

type testSessionAuth struct {
	csrf    string
	cookies []*http.Cookie
}

func sessionAuth(t *testing.T, srv *Server) testSessionAuth {
	t.Helper()
	openToken := insertOpenToken(t, srv)
	exchangeReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/exchange", strings.NewReader(`{"open_token":"`+openToken+`"}`))
	exchangeReq.Header.Set("Content-Type", "application/json")
	exchangeRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(exchangeRec, exchangeReq)
	if exchangeRec.Code != http.StatusOK {
		t.Fatalf("exchange status = %d, body = %s", exchangeRec.Code, exchangeRec.Body.String())
	}
	cookies := exchangeRec.Result().Cookies()

	sessionReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	addCookies(sessionReq, cookies)
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
	return testSessionAuth{csrf: token, cookies: cookies}
}

func applySessionAuth(req *http.Request, auth testSessionAuth) {
	req.Header.Set("X-Symphony-CSRF", auth.csrf)
	addCookies(req, auth.cookies)
}

func addCookies(req *http.Request, cookies []*http.Cookie) {
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
}

func insertOpenToken(t *testing.T, srv *Server) string {
	t.Helper()
	token := security.NewToken()
	expiresAt := time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano)
	if err := srv.Store.App.Exec(`INSERT INTO open_tokens(id,project_id,token_hash,expires_at,created_at) VALUES(?,?,?,?,?)`, core.NewID("open_"), srv.Store.ProjectID, security.HashToken(token), expiresAt, core.Now()); err != nil {
		t.Fatalf("insert open token: %v", err)
	}
	return token
}

func insertLocalSession(t *testing.T, srv *Server, kind string) string {
	t.Helper()
	token := security.NewToken()
	if err := srv.Store.App.Exec(`INSERT INTO local_sessions(id,project_id,kind,token_hash,user_label,created_at) VALUES(?,?,?,?,?,?)`, core.NewID("ses_"), srv.Store.ProjectID, kind, security.HashToken(token), "test-session", core.Now()); err != nil {
		t.Fatalf("insert local session: %v", err)
	}
	return token
}

func prepareCompletedHTTPRun(t *testing.T, srv *Server) *core.RunAttempt {
	t.Helper()
	issue, err := srv.Store.CreateIssue(store.CreateIssueInput{
		Title:              "Completed HTTP cancel",
		Description:        "desc",
		AcceptanceCriteria: []string{"done"},
		Priority:           3,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if _, err := srv.Store.TransitionIssue(issue.Identifier, core.StateReady, "", ""); err != nil {
		t.Fatalf("TransitionIssue: %v", err)
	}
	run, err := srv.Store.ClaimRun(issue.Identifier, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	handoff, err := srv.Store.InsertHandoff(issue.Identifier, run.ID, "payload-hash", map[string]any{
		"summary":      "ready for review",
		"target_state": "Human Review",
	})
	if err != nil {
		t.Fatalf("InsertHandoff: %v", err)
	}
	reviewPacketID, err := srv.Store.InsertReviewPacket(issue.Identifier, run.ID, handoff.ID, srv.Store.RepoRoot, "review.md", "review.json", "patch.diff", "changed.txt", "untracked.txt", "diffstat.txt", "")
	if err != nil {
		t.Fatalf("InsertReviewPacket: %v", err)
	}
	if err := srv.Store.CompleteRunWithReview(run.ID, reviewPacketID); err != nil {
		t.Fatalf("CompleteRunWithReview: %v", err)
	}
	run, err = srv.Store.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	return run
}
