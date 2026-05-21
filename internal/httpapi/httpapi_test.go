package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	"local-symphony/internal/toolgateway"
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

func TestToolRoutePassesCWDHeaderToGateway(t *testing.T) {
	srv := newTestServer(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	subdir := filepath.Join(workspace, "nested")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	issue, err := srv.Store.CreateIssue(store.CreateIssueInput{
		Title:              "Tool cwd header",
		Description:        "desc",
		AcceptanceCriteria: []string{"done"},
		Priority:           3,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if _, err := srv.Store.TransitionIssue(issue.ID, core.StateReady, "", ""); err != nil {
		t.Fatalf("TransitionIssue: %v", err)
	}
	run, err := srv.Store.ClaimRun(issue.ID, "manual", "fake", 1)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	wsID, err := srv.Store.CreateOrUpdateWorkspace(issue.ID, workspace, "test", "auto", "main", "base-sha")
	if err != nil {
		t.Fatalf("CreateOrUpdateWorkspace: %v", err)
	}
	if err := srv.Store.SetRunWorkspace(run.ID, wsID, "test", "auto", "main", "base-sha"); err != nil {
		t.Fatalf("SetRunWorkspace: %v", err)
	}
	if err := srv.Store.UpdateRunStatus(run.ID, core.RunRunning, nil); err != nil {
		t.Fatalf("UpdateRunStatus: %v", err)
	}
	token, err := toolgateway.NewTokenForRun(srv.Store, run, workspace)
	if err != nil {
		t.Fatalf("NewTokenForRun: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/tool/v1/call", strings.NewReader(`{"tool":"issue.get","input":{},"client":{"name":"test"}}`))
	req.Header.Set("Authorization", "bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Symphony-Cwd", subdir)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("tool route status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp toolgateway.Response
	if err := json.NewDecoder(strings.NewReader(rec.Body.String())).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("tool route success = false, error = %#v", resp.Error)
	}
}

func TestToolRouteRejectsInvalidTopLevelRequestBody(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "empty body", body: ``},
		{name: "non object", body: `[]`},
		{name: "missing input", body: `{"tool":"issue.get"}`},
		{name: "missing tool", body: `{"input":{}}`},
		{name: "unknown field", body: `{"tool":"issue.get","input":{},"extra":true}`},
		{name: "case variant field", body: `{"Tool":"issue.get","input":{}}`},
		{name: "trailing json", body: `{"tool":"issue.get","input":{}} {}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t)
			req := httptest.NewRequest(http.MethodPost, "/tool/v1/call", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("tool route status = %d, want 400; body = %s", rec.Code, rec.Body.String())
			}
			var resp toolgateway.Response
			if err := json.NewDecoder(strings.NewReader(rec.Body.String())).Decode(&resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp.Success || resp.Error == nil || resp.Error.Code != "invalid_request" {
				t.Fatalf("response = %#v, want invalid_request failure", resp)
			}
		})
	}
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
	if withCSRFRec.Code != http.StatusCreated {
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

func TestCreateIssueRejectsBlankLabels(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "empty label", body: `{"title":"Invalid label","description":"desc","labels":[""]}`},
		{name: "whitespace label", body: `{"title":"Invalid label","description":"desc","labels":["   "]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/issues", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			applySessionAuth(req, sessionAuth(t, srv))
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("create issue status = %d, want 400; body = %s", rec.Code, rec.Body.String())
			}
			payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
			errData := payload["error"].(map[string]any)
			if errData["code"] != string(core.ErrInvalidRequest) {
				t.Fatalf("error = %#v, want invalid_request", errData)
			}
			items, err := srv.Store.ListIssues(store.ListIssueOptions{})
			if err != nil {
				t.Fatalf("ListIssues: %v", err)
			}
			if len(items) != 0 {
				t.Fatalf("created %d issues for invalid label payload", len(items))
			}
		})
	}
}

func TestStateRequiresSessionButSessionEndpointRemainsPublic(t *testing.T) {
	srv := newTestServer(t)

	noAuthReq := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
	noAuthRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(noAuthRec, noAuthReq)
	if noAuthRec.Code != http.StatusUnauthorized {
		t.Fatalf("state without auth status = %d, body = %s", noAuthRec.Code, noAuthRec.Body.String())
	}

	sessionReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	sessionRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(sessionRec, sessionReq)
	if sessionRec.Code != http.StatusOK {
		t.Fatalf("public session status = %d, body = %s", sessionRec.Code, sessionRec.Body.String())
	}
	sessionPayload := decodeEnvelope(t, strings.NewReader(sessionRec.Body.String()))
	sessionData := sessionPayload["data"].(map[string]any)
	if token, _ := sessionData["csrf_token"].(string); token != "" {
		t.Fatalf("unauthenticated session leaked csrf_token %q", token)
	}

	auth := sessionAuth(t, srv)
	authReq := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
	addCookies(authReq, auth.cookies)
	authRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(authRec, authReq)
	if authRec.Code != http.StatusOK {
		t.Fatalf("state with browser session status = %d, body = %s", authRec.Code, authRec.Body.String())
	}
}

func TestStateReturnsInternalErrorWhenStoreQueryFails(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, *Server)
	}{
		{
			name: "list issues fails",
			setup: func(t *testing.T, srv *Server) {
				t.Helper()
				if err := srv.Store.Project.Close(); err != nil {
					t.Fatalf("close project database: %v", err)
				}
			},
		},
		{
			name: "list runs fails",
			setup: func(t *testing.T, srv *Server) {
				t.Helper()
				if err := srv.Store.Project.Exec(`DROP TABLE run_attempts`); err != nil {
					t.Fatalf("drop run_attempts: %v", err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t)
			auth := sessionAuth(t, srv)
			tt.setup(t, srv)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
			addCookies(req, auth.cookies)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("state status = %d, want 500; body = %s", rec.Code, rec.Body.String())
			}
			payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
			errData := payload["error"].(map[string]any)
			if errData["code"] != string(core.ErrInternal) {
				t.Fatalf("error = %#v, want internal_error", errData)
			}
			if _, ok := payload["data"]; ok {
				t.Fatalf("state returned success data on store error: %#v", payload)
			}
		})
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

func TestSessionExpiryUsesParsedTime(t *testing.T) {
	if !expiredAtOrBefore("2026-05-20T00:00:05Z", "2026-05-20T00:00:05.1Z") {
		t.Fatalf("expiry without fractional seconds should be expired after fractional current time")
	}
}

func TestSessionExpiryAtCurrentTimeIsExpired(t *testing.T) {
	if !expiredAtOrBefore("2026-05-20T00:00:05Z", "2026-05-20T00:00:05Z") {
		t.Fatalf("expiry equal to current time should be expired")
	}
}

func TestBrowserSessionMalformedExpiryIsUnauthorized(t *testing.T) {
	srv := newTestServer(t)

	expiresAt := "not-rfc3339"
	token := insertLocalSessionWithExpiry(t, srv, "browser", expiresAt)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("state with malformed session expiry status = %d, want 401; expires_at = %q, body = %s", rec.Code, expiresAt, rec.Body.String())
	}
}

func TestBrowserSessionMissingExpiryIsUnauthorized(t *testing.T) {
	for _, tc := range []struct {
		name  string
		token func(t *testing.T, srv *Server) string
	}{
		{
			name: "null",
			token: func(t *testing.T, srv *Server) string {
				return insertLocalSession(t, srv, "browser")
			},
		},
		{
			name: "empty",
			token: func(t *testing.T, srv *Server) string {
				return insertLocalSessionWithExpiry(t, srv, "browser", "")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(t)
			token := tc.token(t, srv)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
			req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("state with %s browser expiry status = %d, want 401; body = %s", tc.name, rec.Code, rec.Body.String())
			}
		})
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

func TestBearerSessionAuthorizationIsCaseInsensitive(t *testing.T) {
	srv := newTestServer(t)
	for _, scheme := range []string{"Bearer", "bearer", "BEARER"} {
		t.Run(scheme, func(t *testing.T) {
			cliToken := insertLocalSession(t, srv, "cli")
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/open-token", nil)
			req.Header.Set("Authorization", scheme+" "+cliToken)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("open-token with %s scheme status = %d, body = %s", scheme, rec.Code, rec.Body.String())
			}
		})
	}

	cliToken := insertLocalSession(t, srv, "cli")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/open-token", nil)
	req.Header.Set("Authorization", "Basic "+cliToken)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("open-token with invalid scheme status = %d, want 401; body = %s", rec.Code, rec.Body.String())
	}
}

func TestRotateCLITokenRevokesOldBearerAndReturnsReplacement(t *testing.T) {
	srv := newTestServer(t)

	noBearerReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/cli-token/rotate", nil)
	noBearerRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(noBearerRec, noBearerReq)
	if noBearerRec.Code != http.StatusUnauthorized {
		t.Fatalf("rotate without bearer status = %d, body = %s", noBearerRec.Code, noBearerRec.Body.String())
	}

	oldToken := insertLocalSession(t, srv, "cli")
	rotateReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/cli-token/rotate", nil)
	rotateReq.Header.Set("Authorization", "Bearer "+oldToken)
	rotateRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rotateRec, rotateReq)
	if rotateRec.Code != http.StatusOK {
		t.Fatalf("rotate status = %d, body = %s", rotateRec.Code, rotateRec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rotateRec.Body.String()))
	data := payload["data"].(map[string]any)
	newToken, _ := data["token"].(string)
	if newToken == "" || newToken == oldToken {
		t.Fatalf("rotated token = %q, old token = %q", newToken, oldToken)
	}
	if _, ok := data["expires_at"]; !ok {
		t.Fatalf("rotate data = %#v, want expires_at", data)
	}

	oldReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/open-token", nil)
	oldReq.Header.Set("Authorization", "Bearer "+oldToken)
	oldRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(oldRec, oldReq)
	if oldRec.Code != http.StatusUnauthorized {
		t.Fatalf("old bearer open-token status = %d, body = %s", oldRec.Code, oldRec.Body.String())
	}

	newReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/open-token", nil)
	newReq.Header.Set("Authorization", "Bearer "+newToken)
	newRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(newRec, newReq)
	if newRec.Code != http.StatusOK {
		t.Fatalf("new bearer open-token status = %d, body = %s", newRec.Code, newRec.Body.String())
	}
}

func TestRotateCLITokenPreservesExistingExpiry(t *testing.T) {
	srv := newTestServer(t)
	expiresAt := time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)
	oldToken := insertLocalSessionWithExpiry(t, srv, "cli", expiresAt)

	rotateReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/cli-token/rotate", nil)
	rotateReq.Header.Set("Authorization", "Bearer "+oldToken)
	rotateRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rotateRec, rotateReq)
	if rotateRec.Code != http.StatusOK {
		t.Fatalf("rotate status = %d, body = %s", rotateRec.Code, rotateRec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rotateRec.Body.String()))
	data := payload["data"].(map[string]any)
	if got, _ := data["expires_at"].(string); got != expiresAt {
		t.Fatalf("rotated expires_at = %q, want %q; data = %#v", got, expiresAt, data)
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

func TestCreateIssueRejectsUnknownFieldsWithoutCreatingIssue(t *testing.T) {
	srv := newTestServer(t)
	auth := sessionAuth(t, srv)

	body := `{"title":"Unknown field","description":"desc","acceptance_criteria":["done"],"priority":3,"labels":[],"unexpected":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/issues", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	applySessionAuth(req, auth)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create issue status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
	errData := payload["error"].(map[string]any)
	if errData["code"] != string(core.ErrInvalidRequest) {
		t.Fatalf("error = %#v, want invalid_request", errData)
	}
	issues, err := srv.Store.ListIssues(store.ListIssueOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("created %d issues, want none", len(issues))
	}
}

func TestCreateIssueRejectsCaseVariantFieldsWithoutCreatingIssue(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "title", body: `{"Title":"Case bypass","description":"desc","acceptance_criteria":["done"],"priority":3,"labels":[]}`},
		{name: "acceptance criteria", body: `{"title":"Case bypass","description":"desc","Acceptance_Criteria":["done"],"priority":3,"labels":[]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t)
			auth := sessionAuth(t, srv)

			req := httptest.NewRequest(http.MethodPost, "/api/v1/issues", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			applySessionAuth(req, auth)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("create issue status = %d, want 400; body = %s", rec.Code, rec.Body.String())
			}
			payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
			errData := payload["error"].(map[string]any)
			if errData["code"] != string(core.ErrInvalidRequest) {
				t.Fatalf("error = %#v, want invalid_request", errData)
			}
			issues, err := srv.Store.ListIssues(store.ListIssueOptions{Limit: 10})
			if err != nil {
				t.Fatalf("ListIssues: %v", err)
			}
			if len(issues) != 0 {
				t.Fatalf("created %d issues, want none", len(issues))
			}
		})
	}
}

func TestCreateIssueRejectsTrailingJSONWithoutCreatingIssue(t *testing.T) {
	srv := newTestServer(t)
	auth := sessionAuth(t, srv)

	body := `{"title":"Trailing","description":"desc","acceptance_criteria":["done"],"priority":3,"labels":[]} {"unexpected":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/issues", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	applySessionAuth(req, auth)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create issue status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
	errData := payload["error"].(map[string]any)
	if errData["code"] != string(core.ErrInvalidRequest) {
		t.Fatalf("error = %#v, want invalid_request", errData)
	}
	issues, err := srv.Store.ListIssues(store.ListIssueOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("created %d issues, want none", len(issues))
	}
}

func TestCreateIssueRejectsEmptyBodyWithoutCreatingIssue(t *testing.T) {
	srv := newTestServer(t)
	auth := sessionAuth(t, srv)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/issues", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/json")
	applySessionAuth(req, auth)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create issue status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
	errData := payload["error"].(map[string]any)
	if errData["code"] != string(core.ErrInvalidRequest) {
		t.Fatalf("error = %#v, want invalid_request", errData)
	}
	issues, err := srv.Store.ListIssues(store.ListIssueOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("created %d issues, want none", len(issues))
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

func TestBlockerEndpointsReturnUpdatedIssueEnvelope(t *testing.T) {
	srv := newTestServer(t)
	blocked, err := srv.Store.CreateIssue(store.CreateIssueInput{Title: "Blocked", Description: "desc"})
	if err != nil {
		t.Fatalf("CreateIssue blocked: %v", err)
	}
	blocker, err := srv.Store.CreateIssue(store.CreateIssueInput{Title: "Blocker", Description: "desc"})
	if err != nil {
		t.Fatalf("CreateIssue blocker: %v", err)
	}
	auth := sessionAuth(t, srv)

	addReq := httptest.NewRequest(http.MethodPost, "/api/v1/issues/"+blocked.Identifier+"/blockers", strings.NewReader(`{"blocked_by":"`+blocker.Identifier+`"}`))
	addReq.Header.Set("Content-Type", "application/json")
	applySessionAuth(addReq, auth)
	addRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(addRec, addReq)
	if addRec.Code != http.StatusOK {
		t.Fatalf("add blocker status = %d, body = %s", addRec.Code, addRec.Body.String())
	}
	addPayload := decodeEnvelope(t, strings.NewReader(addRec.Body.String()))
	addData := addPayload["data"].(map[string]any)
	if got, _ := addData["identifier"].(string); got != blocked.Identifier {
		t.Fatalf("add blocker data identifier = %q, want %q; data = %#v", got, blocked.Identifier, addData)
	}
	if blockers, _ := addData["blocked_by"].([]any); len(blockers) != 1 {
		t.Fatalf("add blocker blocked_by = %#v, want one blocker", addData["blocked_by"])
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/issues/"+blocked.Identifier+"/blockers/"+blocker.Identifier, nil)
	applySessionAuth(deleteReq, auth)
	deleteRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete blocker status = %d, body = %s", deleteRec.Code, deleteRec.Body.String())
	}
	deletePayload := decodeEnvelope(t, strings.NewReader(deleteRec.Body.String()))
	deleteData := deletePayload["data"].(map[string]any)
	if got, _ := deleteData["identifier"].(string); got != blocked.Identifier {
		t.Fatalf("delete blocker data identifier = %q, want %q; data = %#v", got, blocked.Identifier, deleteData)
	}
	if blockers, _ := deleteData["blocked_by"].([]any); len(blockers) != 0 {
		t.Fatalf("delete blocker blocked_by = %#v, want no blockers", deleteData["blocked_by"])
	}
}

func TestListIssuesSupportsLabelFilterAndCursor(t *testing.T) {
	srv := newTestServer(t)
	if _, err := srv.Store.CreateIssue(store.CreateIssueInput{Title: "Alpha one", Description: "desc", Labels: []string{"alpha"}}); err != nil {
		t.Fatalf("CreateIssue alpha one: %v", err)
	}
	if _, err := srv.Store.CreateIssue(store.CreateIssueInput{Title: "Beta", Description: "desc", Labels: []string{"beta"}}); err != nil {
		t.Fatalf("CreateIssue beta: %v", err)
	}
	if _, err := srv.Store.CreateIssue(store.CreateIssueInput{Title: "Alpha two", Description: "desc", Labels: []string{"alpha", "extra"}}); err != nil {
		t.Fatalf("CreateIssue alpha two: %v", err)
	}
	auth := sessionAuth(t, srv)

	firstReq := httptest.NewRequest(http.MethodGet, "/api/v1/issues?label=alpha&limit=1&sort=identifier", nil)
	addCookies(firstReq, auth.cookies)
	firstRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(firstRec, firstReq)
	if firstRec.Code != http.StatusOK {
		t.Fatalf("first list status = %d, body = %s", firstRec.Code, firstRec.Body.String())
	}
	firstPayload := decodeEnvelope(t, strings.NewReader(firstRec.Body.String()))
	firstData := firstPayload["data"].(map[string]any)
	firstItems := firstData["items"].([]any)
	if len(firstItems) != 1 {
		t.Fatalf("first page items = %#v, want one item", firstItems)
	}
	firstPage := firstData["page"].(map[string]any)
	cursor, _ := firstPage["next_cursor"].(string)
	if cursor == "" {
		t.Fatalf("first page next_cursor = %#v, want cursor", firstPage["next_cursor"])
	}

	secondReq := httptest.NewRequest(http.MethodGet, "/api/v1/issues?label=alpha&limit=1&sort=identifier&cursor="+cursor, nil)
	addCookies(secondReq, auth.cookies)
	secondRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(secondRec, secondReq)
	if secondRec.Code != http.StatusOK {
		t.Fatalf("second list status = %d, body = %s", secondRec.Code, secondRec.Body.String())
	}
	secondPayload := decodeEnvelope(t, strings.NewReader(secondRec.Body.String()))
	secondData := secondPayload["data"].(map[string]any)
	secondItems := secondData["items"].([]any)
	if len(secondItems) != 1 {
		t.Fatalf("second page items = %#v, want one item", secondItems)
	}
	secondPage := secondData["page"].(map[string]any)
	if secondPage["next_cursor"] != nil {
		t.Fatalf("second page next_cursor = %#v, want nil", secondPage["next_cursor"])
	}
	for _, raw := range append(firstItems, secondItems...) {
		item := raw.(map[string]any)
		labels := item["labels"].([]any)
		hasAlpha := false
		for _, label := range labels {
			if label == "alpha" {
				hasAlpha = true
			}
		}
		if !hasAlpha {
			t.Fatalf("item labels = %#v, want alpha", labels)
		}
	}

	andReq := httptest.NewRequest(http.MethodGet, "/api/v1/issues?label=alpha&label=extra&limit=10&sort=identifier", nil)
	addCookies(andReq, auth.cookies)
	andRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(andRec, andReq)
	if andRec.Code != http.StatusOK {
		t.Fatalf("multi-label list status = %d, body = %s", andRec.Code, andRec.Body.String())
	}
	andPayload := decodeEnvelope(t, strings.NewReader(andRec.Body.String()))
	andData := andPayload["data"].(map[string]any)
	andItems := andData["items"].([]any)
	if len(andItems) != 1 {
		t.Fatalf("multi-label items = %#v, want one item with both labels", andItems)
	}
	if got := andItems[0].(map[string]any)["title"]; got != "Alpha two" {
		t.Fatalf("multi-label title = %#v, want Alpha two", got)
	}
}

func TestListIssuesReportsHasMoreForPlainLimitPagination(t *testing.T) {
	srv := newTestServer(t)
	if _, err := srv.Store.CreateIssue(store.CreateIssueInput{Title: "Plain one", Description: "desc"}); err != nil {
		t.Fatalf("CreateIssue plain one: %v", err)
	}
	if _, err := srv.Store.CreateIssue(store.CreateIssueInput{Title: "Plain two", Description: "desc"}); err != nil {
		t.Fatalf("CreateIssue plain two: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/issues?limit=1&sort=identifier", nil)
	addCookies(req, sessionAuth(t, srv).cookies)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
	data := payload["data"].(map[string]any)
	items := data["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %#v, want one item", items)
	}
	page := data["page"].(map[string]any)
	if page["has_more"] != true {
		t.Fatalf("has_more = %#v, want true; page = %#v", page["has_more"], page)
	}
	if cursor, _ := page["next_cursor"].(string); cursor == "" {
		t.Fatalf("next_cursor = %#v, want cursor; page = %#v", page["next_cursor"], page)
	}
}

func TestListIssuesCursorCanReachItemsAfterFirstTwoHundred(t *testing.T) {
	srv := newTestServer(t)
	for i := 1; i <= 205; i++ {
		if _, err := srv.Store.CreateIssue(store.CreateIssueInput{Title: fmt.Sprintf("Paged issue %03d", i), Description: "desc"}); err != nil {
			t.Fatalf("CreateIssue %d: %v", i, err)
		}
	}
	auth := sessionAuth(t, srv)

	firstReq := httptest.NewRequest(http.MethodGet, "/api/v1/issues?limit=200&sort=identifier", nil)
	addCookies(firstReq, auth.cookies)
	firstRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(firstRec, firstReq)
	if firstRec.Code != http.StatusOK {
		t.Fatalf("first list status = %d, body = %s", firstRec.Code, firstRec.Body.String())
	}
	firstPayload := decodeEnvelope(t, strings.NewReader(firstRec.Body.String()))
	firstData := firstPayload["data"].(map[string]any)
	firstItems := firstData["items"].([]any)
	if len(firstItems) != 200 {
		t.Fatalf("first page items = %d, want 200", len(firstItems))
	}
	firstPage := firstData["page"].(map[string]any)
	cursor, _ := firstPage["next_cursor"].(string)
	if cursor == "" || firstPage["has_more"] != true {
		t.Fatalf("first page = %#v, want has_more with next_cursor", firstPage)
	}

	secondReq := httptest.NewRequest(http.MethodGet, "/api/v1/issues?limit=200&sort=identifier&cursor="+cursor, nil)
	addCookies(secondReq, auth.cookies)
	secondRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(secondRec, secondReq)
	if secondRec.Code != http.StatusOK {
		t.Fatalf("second list status = %d, body = %s", secondRec.Code, secondRec.Body.String())
	}
	secondPayload := decodeEnvelope(t, strings.NewReader(secondRec.Body.String()))
	secondData := secondPayload["data"].(map[string]any)
	secondItems := secondData["items"].([]any)
	if len(secondItems) != 5 {
		t.Fatalf("second page items = %d, want 5", len(secondItems))
	}
	secondPage := secondData["page"].(map[string]any)
	if secondPage["next_cursor"] != nil || secondPage["has_more"] != false {
		t.Fatalf("second page = %#v, want final page", secondPage)
	}
}

func TestListIssuesLabelFilterFindsMatchesAfterFirstTwoHundred(t *testing.T) {
	srv := newTestServer(t)
	for i := 1; i <= 200; i++ {
		if _, err := srv.Store.CreateIssue(store.CreateIssueInput{Title: fmt.Sprintf("Unlabeled issue %03d", i), Description: "desc"}); err != nil {
			t.Fatalf("CreateIssue unlabeled %d: %v", i, err)
		}
	}
	if _, err := srv.Store.CreateIssue(store.CreateIssueInput{Title: "Late labeled issue", Description: "desc", Labels: []string{"late"}}); err != nil {
		t.Fatalf("CreateIssue late labeled: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/issues?label=late&limit=1&sort=identifier", nil)
	addCookies(req, sessionAuth(t, srv).cookies)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
	data := payload["data"].(map[string]any)
	items := data["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %#v, want one late-labeled issue", items)
	}
	item := items[0].(map[string]any)
	if item["title"] != "Late labeled issue" {
		t.Fatalf("title = %#v, want Late labeled issue", item["title"])
	}
}

func TestListIssuesRejectsInvalidQueryParameters(t *testing.T) {
	tests := []string{
		"limit=0",
		"limit=201",
		"limit=abc",
		"sort=created",
		"dispatch_paused=maybe",
		"state=Unknown",
		"cursor=-1",
		"cursor=abc",
	}
	for _, query := range tests {
		t.Run(query, func(t *testing.T) {
			srv := newTestServer(t)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/issues?"+query, nil)
			addCookies(req, sessionAuth(t, srv).cookies)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("list issues status = %d, want 400; body = %s", rec.Code, rec.Body.String())
			}
			payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
			errData := payload["error"].(map[string]any)
			if errData["code"] != string(core.ErrInvalidRequest) {
				t.Fatalf("error = %#v, want invalid_request", errData)
			}
		})
	}
}

func TestListRunsReturnsInternalErrorWhenStoreQueryFails(t *testing.T) {
	srv := newTestServer(t)
	auth := sessionAuth(t, srv)
	if err := srv.Store.Project.Close(); err != nil {
		t.Fatalf("close project database: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/runs", nil)
	addCookies(req, auth.cookies)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("list runs status = %d, want 500; body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
	errData := payload["error"].(map[string]any)
	if errData["code"] != string(core.ErrInternal) {
		t.Fatalf("error = %#v, want internal_error", errData)
	}
	if _, ok := payload["data"]; ok {
		t.Fatalf("list runs returned success data on store error: %#v", payload)
	}
}

func TestListApprovalsReturnsInternalErrorWhenStoreQueryFails(t *testing.T) {
	srv := newTestServer(t)
	auth := sessionAuth(t, srv)
	if err := srv.Store.Project.Close(); err != nil {
		t.Fatalf("close project database: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/approvals", nil)
	addCookies(req, auth.cookies)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("list approvals status = %d, want 500; body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
	errData := payload["error"].(map[string]any)
	if errData["code"] != string(core.ErrInternal) {
		t.Fatalf("error = %#v, want internal_error", errData)
	}
	if _, ok := payload["data"]; ok {
		t.Fatalf("list approvals returned success data on store error: %#v", payload)
	}
}

func TestIssueControlRoutesReturnIssueEnvelope(t *testing.T) {
	srv := newTestServer(t)
	issue, err := srv.Store.CreateIssue(store.CreateIssueInput{Title: "Control shape", Description: "desc"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	auth := sessionAuth(t, srv)
	tests := []struct {
		name string
		path string
		body string
		want map[string]any
	}{
		{
			name: "transition",
			path: "/api/v1/issues/" + issue.Identifier + "/transition",
			body: `{"state":"Ready","reason":"ready"}`,
			want: map[string]any{"state": string(core.StateReady)},
		},
		{
			name: "dispatch pause",
			path: "/api/v1/issues/" + issue.Identifier + "/dispatch-pause",
			body: `{"reason":"pause queue"}`,
			want: map[string]any{"dispatch_paused": true, "dispatch_pause_reason": "pause queue"},
		},
		{
			name: "dispatch resume",
			path: "/api/v1/issues/" + issue.Identifier + "/dispatch-resume",
			body: `{"reason":"resume queue"}`,
			want: map[string]any{"dispatch_paused": false},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			applySessionAuth(req, auth)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s status = %d, body = %s", tt.name, rec.Code, rec.Body.String())
			}
			payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
			data := payload["data"].(map[string]any)
			if data["issue"] != nil || data["side_effects"] != nil {
				t.Fatalf("%s data = %#v, want direct issue shape", tt.name, data)
			}
			if got, _ := data["identifier"].(string); got != issue.Identifier {
				t.Fatalf("%s identifier = %q, want %q", tt.name, got, issue.Identifier)
			}
			for key, want := range tt.want {
				if got := data[key]; got != want {
					t.Fatalf("%s %s = %#v, want %#v; data = %#v", tt.name, key, got, want, data)
				}
			}
		})
	}
}

func TestMutationRoutesRejectExtraPathSegmentsWithoutMutating(t *testing.T) {
	srv := newTestServer(t)
	issue, err := srv.Store.CreateIssue(store.CreateIssueInput{
		Title:              "Extra path segments",
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
	dispatchIssue, err := srv.Store.CreateIssue(store.CreateIssueInput{Title: "Dispatch extra segment", Description: "desc"})
	if err != nil {
		t.Fatalf("CreateIssue dispatch: %v", err)
	}
	if _, err := srv.Store.TransitionIssue(dispatchIssue.Identifier, core.StateReady, "", ""); err != nil {
		t.Fatalf("TransitionIssue dispatch: %v", err)
	}
	pauseIssue, err := srv.Store.CreateIssue(store.CreateIssueInput{Title: "Pause extra segment", Description: "desc"})
	if err != nil {
		t.Fatalf("CreateIssue pause: %v", err)
	}
	if _, err := srv.Store.TransitionIssue(pauseIssue.Identifier, core.StateReady, "", ""); err != nil {
		t.Fatalf("TransitionIssue pause: %v", err)
	}
	resumeIssue, err := srv.Store.CreateIssue(store.CreateIssueInput{Title: "Resume extra segment", Description: "desc"})
	if err != nil {
		t.Fatalf("CreateIssue resume: %v", err)
	}
	if _, err := srv.Store.TransitionIssue(resumeIssue.Identifier, core.StateReady, "", ""); err != nil {
		t.Fatalf("TransitionIssue resume: %v", err)
	}
	if _, err := srv.Store.DispatchPause(resumeIssue.Identifier, "pause before route check"); err != nil {
		t.Fatalf("DispatchPause resume issue: %v", err)
	}
	auth := sessionAuth(t, srv)
	tests := []struct {
		name string
		path string
		body string
	}{
		{name: "issue transition", path: "/api/v1/issues/" + issue.Identifier + "/transition/extra", body: `{"state":"Blocked","reason":"should not mutate"}`},
		{name: "issue comments", path: "/api/v1/issues/" + issue.Identifier + "/comments/extra", body: `{"body":"should not mutate"}`},
		{name: "issue dispatch", path: "/api/v1/issues/" + dispatchIssue.Identifier + "/dispatch/extra", body: `{}`},
		{name: "issue dispatch pause", path: "/api/v1/issues/" + pauseIssue.Identifier + "/dispatch-pause/extra", body: `{"reason":"should not mutate"}`},
		{name: "issue dispatch resume", path: "/api/v1/issues/" + resumeIssue.Identifier + "/dispatch-resume/extra", body: `{"reason":"should not mutate"}`},
		{name: "run cancel", path: "/api/v1/runs/" + run.ID + "/cancel/extra", body: `{"reason":"should not mutate"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			applySessionAuth(req, auth)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("%s status = %d, want 404; body = %s", tt.name, rec.Code, rec.Body.String())
			}
		})
	}
	afterIssue, err := srv.Store.GetIssue(issue.Identifier)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if afterIssue.State != core.StateWorking {
		t.Fatalf("issue state = %s, want Working", afterIssue.State)
	}
	row, err := srv.Store.Project.QueryOne(`SELECT COUNT(*) AS n FROM issue_comments WHERE issue_id=? AND body='should not mutate'`, issue.ID)
	if err != nil {
		t.Fatalf("count comments: %v", err)
	}
	if row["n"].Int() != 0 {
		t.Fatalf("inserted %d comments through extra path segment", row["n"].Int())
	}
	afterRun, err := srv.Store.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if afterRun.Status == core.RunCancelled {
		t.Fatalf("run was cancelled through extra path segment")
	}
	dispatchRows, err := srv.Store.Project.Query(`SELECT id FROM run_attempts WHERE issue_id=?`, dispatchIssue.ID)
	if err != nil {
		t.Fatalf("query dispatch run attempts: %v", err)
	}
	if len(dispatchRows) != 0 {
		t.Fatalf("dispatch extra path created %d run attempts", len(dispatchRows))
	}
	afterPauseIssue, err := srv.Store.GetIssue(pauseIssue.Identifier)
	if err != nil {
		t.Fatalf("GetIssue pause: %v", err)
	}
	if afterPauseIssue.DispatchPaused {
		t.Fatalf("pause issue was paused through extra path segment")
	}
	afterResumeIssue, err := srv.Store.GetIssue(resumeIssue.Identifier)
	if err != nil {
		t.Fatalf("GetIssue resume: %v", err)
	}
	if !afterResumeIssue.DispatchPaused {
		t.Fatalf("resume issue was resumed through extra path segment")
	}
}

func TestTypedMutationRoutesRejectInvalidJSONBodies(t *testing.T) {
	srv := newTestServer(t)
	issue, err := srv.Store.CreateIssue(store.CreateIssueInput{Title: "Strict bodies", Description: "desc"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	auth := sessionAuth(t, srv)
	tests := []struct {
		name      string
		path      string
		body      string
		needsAuth bool
	}{
		{name: "issue transition unknown field", path: "/api/v1/issues/" + issue.Identifier + "/transition", body: `{"state":"Ready","reason":"ready","extra":true}`, needsAuth: true},
		{name: "issue transition case variant field", path: "/api/v1/issues/" + issue.Identifier + "/transition", body: `{"State":"Ready","reason":"ready"}`, needsAuth: true},
		{name: "issue comments trailing json", path: "/api/v1/issues/" + issue.Identifier + "/comments", body: `{"body":"hello"} {}`, needsAuth: true},
		{name: "issue comments empty required body", path: "/api/v1/issues/" + issue.Identifier + "/comments", body: ``, needsAuth: true},
		{name: "issue comments null body", path: "/api/v1/issues/" + issue.Identifier + "/comments", body: `{"body":null}`, needsAuth: true},
		{name: "issue blockers non object", path: "/api/v1/issues/" + issue.Identifier + "/blockers", body: `[]`, needsAuth: true},
		{name: "dispatch pause case variant field", path: "/api/v1/issues/" + issue.Identifier + "/dispatch-pause", body: `{"Reason":"pause"}`, needsAuth: true},
		{name: "dispatch resume unknown field", path: "/api/v1/issues/" + issue.Identifier + "/dispatch-resume", body: `{"reason":"resume","extra":true}`, needsAuth: true},
		{name: "run cancel unknown field", path: "/api/v1/runs/run_missing/cancel", body: `{"reason":"stop","extra":true}`, needsAuth: true},
		{name: "run cancel null reason", path: "/api/v1/runs/run_missing/cancel", body: `{"reason":null}`, needsAuth: true},
		{name: "approval decision unknown field", path: "/api/v1/approvals/apr_missing/decide", body: `{"decision":"deny","extra":true}`, needsAuth: true},
		{name: "approval decision null reason", path: "/api/v1/approvals/apr_missing/decide", body: `{"decision":"deny","reason":null}`, needsAuth: true},
		{name: "review action unknown field", path: "/api/v1/reviews/" + issue.Identifier + "/send-to-rework", body: `{"reason":"again","extra":true}`, needsAuth: true},
		{name: "review action null reason", path: "/api/v1/reviews/rvw_missing/send-to-rework", body: `{"reason":null}`, needsAuth: true},
		{name: "auth exchange unknown field", path: "/api/v1/auth/exchange", body: `{"open_token":"not-valid","extra":true}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			if tt.needsAuth {
				applySessionAuth(req, auth)
			}
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s status = %d, want 400; body = %s", tt.name, rec.Code, rec.Body.String())
			}
			payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
			errData := payload["error"].(map[string]any)
			if errData["code"] != string(core.ErrInvalidRequest) {
				t.Fatalf("%s error = %#v, want invalid_request", tt.name, errData)
			}
		})
	}
}

func TestIssueTransitionRejectsDuplicateOfUnlessDuplicateWithoutMutating(t *testing.T) {
	srv := newTestServer(t)
	issue, err := srv.Store.CreateIssue(store.CreateIssueInput{Title: "Duplicate guard", Description: "desc"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	canonical, err := srv.Store.CreateIssue(store.CreateIssueInput{Title: "Canonical", Description: "desc"})
	if err != nil {
		t.Fatalf("CreateIssue canonical: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/issues/"+issue.Identifier+"/transition", strings.NewReader(`{"state":"Ready","reason":"ready","duplicate_of":"`+canonical.Identifier+`"}`))
	req.Header.Set("Content-Type", "application/json")
	applySessionAuth(req, sessionAuth(t, srv))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("transition status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
	errData := payload["error"].(map[string]any)
	if errData["code"] != string(core.ErrInvalidRequest) {
		t.Fatalf("error = %#v, want invalid_request", errData)
	}
	after, err := srv.Store.GetIssue(issue.Identifier)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if after.State != core.StateInbox {
		t.Fatalf("issue state = %s, want Inbox", after.State)
	}
	if after.DuplicateOf != nil {
		t.Fatalf("duplicate_of = %#v, want nil", after.DuplicateOf)
	}
}

func TestIssueTransitionRejectsBlankProvidedReason(t *testing.T) {
	srv := newTestServer(t)
	issue, err := srv.Store.CreateIssue(store.CreateIssueInput{
		Title:              "Blank reason",
		Description:        "desc",
		AcceptanceCriteria: []string{"done"},
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/issues/"+issue.Identifier+"/transition", strings.NewReader(`{"state":"Ready","reason":"   "}`))
	req.Header.Set("Content-Type", "application/json")
	applySessionAuth(req, sessionAuth(t, srv))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("transition status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
	errData := payload["error"].(map[string]any)
	if errData["code"] != string(core.ErrInvalidRequest) {
		t.Fatalf("error = %#v, want invalid_request", errData)
	}
	after, err := srv.Store.GetIssue(issue.Identifier)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if after.State != core.StateInbox {
		t.Fatalf("issue state = %s, want Inbox", after.State)
	}
}

func TestDispatchIssueReturnsImplementedResponseShape(t *testing.T) {
	srv := newTestServer(t)
	issue, err := srv.Store.CreateIssue(store.CreateIssueInput{
		Title:              "Dispatch shape",
		Description:        "desc",
		AcceptanceCriteria: []string{"done"},
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if _, err := srv.Store.TransitionIssue(issue.Identifier, core.StateReady, "ready", ""); err != nil {
		t.Fatalf("TransitionIssue: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/issues/"+issue.Identifier+"/dispatch", nil)
	applySessionAuth(req, sessionAuth(t, srv))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("dispatch status = %d, body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
	data := payload["data"].(map[string]any)
	if _, ok := data["run"].(map[string]any); !ok {
		t.Fatalf("dispatch data missing run object: %#v", data)
	}
	if _, ok := data["issue"].(map[string]any); !ok {
		t.Fatalf("dispatch data missing issue object: %#v", data)
	}
	if _, ok := data["accepted"]; ok {
		t.Fatalf("dispatch data included obsolete accepted field: %#v", data)
	}
	if _, ok := data["run_id"]; ok {
		t.Fatalf("dispatch data included obsolete run_id field: %#v", data)
	}
}

func TestCreateCommentReturnsErrorWhenIssueReloadFails(t *testing.T) {
	srv := newTestServer(t)
	issue, err := srv.Store.CreateIssue(store.CreateIssueInput{Title: "Comment reload", Description: "desc"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := srv.Store.Project.Exec(`CREATE TRIGGER delete_issue_after_comment_insert AFTER INSERT ON issue_comments
BEGIN
  DELETE FROM issues WHERE id = NEW.issue_id;
END;`); err != nil {
		t.Fatalf("create issue comment trigger: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/issues/"+issue.Identifier+"/comments", strings.NewReader(`{"body":"needs followup"}`))
	req.Header.Set("Content-Type", "application/json")
	applySessionAuth(req, sessionAuth(t, srv))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("comment status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
	errData := payload["error"].(map[string]any)
	if errData["code"] != string(core.ErrNotFound) {
		t.Fatalf("error = %#v, want not_found", errData)
	}
	if _, ok := payload["data"]; ok {
		t.Fatalf("comment returned success data on issue reload error: %#v", payload)
	}
}

func TestPatchIssueRejectsInvalidPriority(t *testing.T) {
	srv := newTestServer(t)
	issue, err := srv.Store.CreateIssue(store.CreateIssueInput{Title: "Invalid priority", Description: "desc", Priority: 4})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	auth := sessionAuth(t, srv)

	tests := []struct {
		name string
		body string
	}{
		{name: "fractional", body: `{"priority":2.5}`},
		{name: "exponent", body: `{"priority":1e0}`},
		{name: "precision bypass fractional one", body: `{"priority":1.0000000000000001}`},
		{name: "precision bypass fractional five", body: `{"priority":4.9999999999999999}`},
		{name: "string", body: `{"priority":"3"}`},
		{name: "null", body: `{"priority":null}`},
		{name: "bool", body: `{"priority":true}`},
		{name: "object", body: `{"priority":{"value":3}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPatch, "/api/v1/issues/"+issue.Identifier, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			applySessionAuth(req, auth)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("patch issue status = %d, want 400; body = %s", rec.Code, rec.Body.String())
			}
			payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
			errData := payload["error"].(map[string]any)
			if errData["code"] != string(core.ErrInvalidRequest) {
				t.Fatalf("error = %#v, want invalid_request", errData)
			}
			got, err := srv.Store.GetIssue(issue.Identifier)
			if err != nil {
				t.Fatalf("GetIssue: %v", err)
			}
			if got.Priority != 4 {
				t.Fatalf("priority = %d, want unchanged 4", got.Priority)
			}
		})
	}
}

func TestPatchIssueRejectsOutOfRangePriorityWithoutPartialUpdate(t *testing.T) {
	srv := newTestServer(t)
	issue, err := srv.Store.CreateIssue(store.CreateIssueInput{Title: "Original title", Description: "desc", Priority: 4})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/issues/"+issue.Identifier, strings.NewReader(`{"title":"changed","priority":9}`))
	req.Header.Set("Content-Type", "application/json")
	applySessionAuth(req, sessionAuth(t, srv))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("patch issue status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
	errData := payload["error"].(map[string]any)
	if errData["code"] != string(core.ErrInvalidRequest) {
		t.Fatalf("error = %#v, want invalid_request", errData)
	}
	got, err := srv.Store.GetIssue(issue.Identifier)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.Title != "Original title" {
		t.Fatalf("title = %q, want unchanged", got.Title)
	}
	if got.Priority != 4 {
		t.Fatalf("priority = %d, want unchanged 4", got.Priority)
	}
}

func TestPatchIssueRejectsNonStringTextFieldsWithoutPartialUpdate(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "title", body: `{"title":123,"priority":5}`},
		{name: "description", body: `{"description":false,"priority":5}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t)
			issue, err := srv.Store.CreateIssue(store.CreateIssueInput{Title: "Original title", Description: "desc", Priority: 4})
			if err != nil {
				t.Fatalf("CreateIssue: %v", err)
			}

			req := httptest.NewRequest(http.MethodPatch, "/api/v1/issues/"+issue.Identifier, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			applySessionAuth(req, sessionAuth(t, srv))
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("patch issue status = %d, want 400; body = %s", rec.Code, rec.Body.String())
			}
			payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
			errData := payload["error"].(map[string]any)
			if errData["code"] != string(core.ErrInvalidRequest) {
				t.Fatalf("error = %#v, want invalid_request", errData)
			}
			got, err := srv.Store.GetIssue(issue.Identifier)
			if err != nil {
				t.Fatalf("GetIssue: %v", err)
			}
			if got.Title != "Original title" {
				t.Fatalf("title = %q, want unchanged", got.Title)
			}
			if got.Description != "desc" {
				t.Fatalf("description = %q, want unchanged", got.Description)
			}
			if got.Priority != 4 {
				t.Fatalf("priority = %d, want unchanged 4", got.Priority)
			}
		})
	}
}

func TestPatchIssueRejectsUnknownFieldsWithoutPartialUpdate(t *testing.T) {
	srv := newTestServer(t)
	issue, err := srv.Store.CreateIssue(store.CreateIssueInput{Title: "Original title", Description: "desc", Priority: 4})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/issues/"+issue.Identifier, strings.NewReader(`{"title":"changed","unexpected":true}`))
	req.Header.Set("Content-Type", "application/json")
	applySessionAuth(req, sessionAuth(t, srv))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("patch issue status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
	errData := payload["error"].(map[string]any)
	if errData["code"] != string(core.ErrInvalidRequest) {
		t.Fatalf("error = %#v, want invalid_request", errData)
	}
	got, err := srv.Store.GetIssue(issue.Identifier)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.Title != "Original title" {
		t.Fatalf("title = %q, want unchanged", got.Title)
	}
	if got.Priority != 4 {
		t.Fatalf("priority = %d, want unchanged 4", got.Priority)
	}
}

func TestPatchIssueRejectsTrailingJSONWithoutPartialUpdate(t *testing.T) {
	srv := newTestServer(t)
	issue, err := srv.Store.CreateIssue(store.CreateIssueInput{Title: "Original title", Description: "desc", Priority: 4})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/issues/"+issue.Identifier, strings.NewReader(`{"title":"changed"} {"unexpected":true}`))
	req.Header.Set("Content-Type", "application/json")
	applySessionAuth(req, sessionAuth(t, srv))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("patch issue status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
	errData := payload["error"].(map[string]any)
	if errData["code"] != string(core.ErrInvalidRequest) {
		t.Fatalf("error = %#v, want invalid_request", errData)
	}
	got, err := srv.Store.GetIssue(issue.Identifier)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.Title != "Original title" {
		t.Fatalf("title = %q, want unchanged", got.Title)
	}
	if got.Priority != 4 {
		t.Fatalf("priority = %d, want unchanged 4", got.Priority)
	}
}

func TestPatchIssueRejectsEmptyBodyWithoutPartialUpdate(t *testing.T) {
	srv := newTestServer(t)
	issue, err := srv.Store.CreateIssue(store.CreateIssueInput{Title: "Original title", Description: "desc", Priority: 4})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/issues/"+issue.Identifier, strings.NewReader(""))
	req.Header.Set("Content-Type", "application/json")
	applySessionAuth(req, sessionAuth(t, srv))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("patch issue status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
	errData := payload["error"].(map[string]any)
	if errData["code"] != string(core.ErrInvalidRequest) {
		t.Fatalf("error = %#v, want invalid_request", errData)
	}
	got, err := srv.Store.GetIssue(issue.Identifier)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.Title != "Original title" {
		t.Fatalf("title = %q, want unchanged", got.Title)
	}
	if got.Priority != 4 {
		t.Fatalf("priority = %d, want unchanged 4", got.Priority)
	}
}

func TestPatchIssueRejectsNonStringArrayElementsWithoutChangingIssue(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "acceptance criteria", body: `{"acceptance_criteria":["keep",123]}`},
		{name: "labels", body: `{"labels":["keep",false]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t)
			auth := sessionAuth(t, srv)
			issue, err := srv.Store.CreateIssue(store.CreateIssueInput{
				Title:              "Invalid array patch",
				Description:        "desc",
				AcceptanceCriteria: []string{"existing ac"},
				Priority:           3,
				Labels:             []string{"existing-label"},
			})
			if err != nil {
				t.Fatalf("CreateIssue: %v", err)
			}
			req := httptest.NewRequest(http.MethodPatch, "/api/v1/issues/"+issue.Identifier, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			applySessionAuth(req, auth)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("patch issue status = %d, want 400; body = %s", rec.Code, rec.Body.String())
			}
			payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
			errData := payload["error"].(map[string]any)
			if errData["code"] != string(core.ErrInvalidRequest) {
				t.Fatalf("error = %#v, want invalid_request", errData)
			}
			got, err := srv.Store.GetIssue(issue.Identifier)
			if err != nil {
				t.Fatalf("GetIssue: %v", err)
			}
			if len(got.AcceptanceCriteria) != 1 || got.AcceptanceCriteria[0] != "existing ac" {
				t.Fatalf("acceptance_criteria = %#v, want unchanged", got.AcceptanceCriteria)
			}
			if len(got.Labels) != 1 || got.Labels[0] != "existing-label" {
				t.Fatalf("labels = %#v, want unchanged", got.Labels)
			}
		})
	}
}

func TestPatchIssueRejectsInvalidArrayFieldsWithoutChangingIssue(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "blank label", body: `{"labels":[""]}`},
		{name: "whitespace label", body: `{"labels":["   "]}`},
		{name: "labels null", body: `{"labels":null}`},
		{name: "labels string", body: `{"labels":"x"}`},
		{name: "acceptance criteria object", body: `{"acceptance_criteria":{}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t)
			auth := sessionAuth(t, srv)
			issue, err := srv.Store.CreateIssue(store.CreateIssueInput{
				Title:              "Invalid array field patch",
				Description:        "desc",
				AcceptanceCriteria: []string{"existing ac"},
				Priority:           3,
				Labels:             []string{"existing-label"},
			})
			if err != nil {
				t.Fatalf("CreateIssue: %v", err)
			}
			req := httptest.NewRequest(http.MethodPatch, "/api/v1/issues/"+issue.Identifier, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			applySessionAuth(req, auth)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("patch issue status = %d, want 400; body = %s", rec.Code, rec.Body.String())
			}
			payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
			errData := payload["error"].(map[string]any)
			if errData["code"] != string(core.ErrInvalidRequest) {
				t.Fatalf("error = %#v, want invalid_request", errData)
			}
			got, err := srv.Store.GetIssue(issue.Identifier)
			if err != nil {
				t.Fatalf("GetIssue: %v", err)
			}
			if len(got.AcceptanceCriteria) != 1 || got.AcceptanceCriteria[0] != "existing ac" {
				t.Fatalf("acceptance_criteria = %#v, want unchanged", got.AcceptanceCriteria)
			}
			if len(got.Labels) != 1 || got.Labels[0] != "existing-label" {
				t.Fatalf("labels = %#v, want unchanged", got.Labels)
			}
		})
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

func TestDashboardDoesNotServeSymlinkAssetOutsideDist(t *testing.T) {
	srv := newTestServer(t)
	dist := filepath.Join(t.TempDir(), "dist")
	t.Setenv("SYMPHONY_DASHBOARD_DIST", dist)
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatalf("mkdir dist: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("dashboard index"), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	secretPath := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secretPath, []byte("external dashboard secret"), 0o644); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	if err := os.Symlink(secretPath, filepath.Join(dist, "secret.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/secret.txt", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), "external dashboard secret") {
		t.Fatalf("served external dashboard secret through symlink: status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestDashboardFallbackDoesNotServeSymlinkIndexOutsideDist(t *testing.T) {
	srv := newTestServer(t)
	dist := filepath.Join(t.TempDir(), "dist")
	t.Setenv("SYMPHONY_DASHBOARD_DIST", dist)
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatalf("mkdir dist: %v", err)
	}
	secretPath := filepath.Join(t.TempDir(), "index.html")
	if err := os.WriteFile(secretPath, []byte("external index secret"), 0o644); err != nil {
		t.Fatalf("write secret index: %v", err)
	}
	if err := os.Symlink(secretPath, filepath.Join(dist, "index.html")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/missing-route", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), "external index secret") {
		t.Fatalf("served external dashboard index through symlink fallback: status = %d, body = %s", rec.Code, rec.Body.String())
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

func TestEventStreamEmitsStoredEventOnceAsDefaultMessage(t *testing.T) {
	srv := newTestServer(t)
	if _, err := srv.Store.CreateIssue(store.CreateIssueInput{Title: "SSE issue", Description: "desc"}); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/stream", nil).WithContext(ctx)
	addCookies(req, sessionAuth(t, srv).cookies)
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
	if strings.Contains(body, "event: issue.created\n") {
		t.Fatalf("stream body contains a typed issue.created frame: %q", body)
	}
	if got := strings.Count(body, "\ndata: "); got != 1 {
		t.Fatalf("stream data frame count = %d, want 1; body = %q", got, body)
	}
}

func TestIssueEventStreamUnknownIssueReturnsNotFound(t *testing.T) {
	srv := newTestServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/issues/LOC-404/events/stream", nil).WithContext(ctx)
	addCookies(req, sessionAuth(t, srv).cookies)
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
	addCookies(req, sessionAuth(t, srv).cookies)
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

func TestScopedEventStreamsRejectNonGETMethods(t *testing.T) {
	srv := newTestServer(t)
	issue, err := srv.Store.CreateIssue(store.CreateIssueInput{Title: "Scoped stream methods", Description: "desc"})
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
	auth := sessionAuth(t, srv)
	tests := []struct {
		method string
		path   string
	}{
		{method: http.MethodHead, path: "/api/v1/issues/" + issue.Identifier + "/events/stream"},
		{method: http.MethodPost, path: "/api/v1/issues/" + issue.Identifier + "/events/stream"},
		{method: http.MethodHead, path: "/api/v1/runs/" + run.ID + "/events/stream"},
		{method: http.MethodPost, path: "/api/v1/runs/" + run.ID + "/events/stream"},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			req := httptest.NewRequest(tt.method, tt.path, nil).WithContext(ctx)
			applySessionAuth(req, auth)
			rec := httptest.NewRecorder()
			done := make(chan struct{})
			go func() {
				srv.Handler().ServeHTTP(rec, req)
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(50 * time.Millisecond):
				cancel()
				select {
				case <-done:
				case <-time.After(250 * time.Millisecond):
					t.Fatal("stream route did not return after context cancellation")
				}
			}
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("stream status = %d, want 405; body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestEventStreamReturnsInternalErrorWhenInitialQueryFails(t *testing.T) {
	srv := newTestServer(t)
	auth := sessionAuth(t, srv)
	if err := srv.Store.Project.Close(); err != nil {
		t.Fatalf("close project database: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/stream", nil)
	addCookies(req, auth.cookies)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		srv.Handler().ServeHTTP(rec, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatalf("event stream did not return after initial query failure")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("event stream status = %d, want 500; body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
	errData := payload["error"].(map[string]any)
	if errData["code"] != string(core.ErrInternal) {
		t.Fatalf("error = %#v, want internal_error", errData)
	}
}

func TestIssueEventStreamSendsErrorEventWhenQueryFailsAfterStart(t *testing.T) {
	srv := newTestServer(t)
	issue, err := srv.Store.CreateIssue(store.CreateIssueInput{Title: "SSE issue error", Description: "desc"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	auth := sessionAuth(t, srv)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/issues/"+issue.Identifier+"/events/stream", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	addCookies(req, auth.cookies)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("stream request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("stream status = %d, body = %s", resp.StatusCode, bodyBytes)
	}
	if err := srv.Store.Project.Close(); err != nil {
		t.Fatalf("close project database: %v", err)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	body := string(bodyBytes)
	if !strings.Contains(body, "event: error\n") {
		t.Fatalf("stream body missing error event: %q", body)
	}
	if !strings.Contains(body, `"code":"internal_error"`) {
		t.Fatalf("stream body missing internal_error code: %q", body)
	}
}

func TestRunEventStreamSendsErrorEventWhenQueryFailsAfterStart(t *testing.T) {
	srv := newTestServer(t)
	issue, err := srv.Store.CreateIssue(store.CreateIssueInput{
		Title:              "SSE run error",
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
	auth := sessionAuth(t, srv)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/runs/"+run.ID+"/events/stream", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	addCookies(req, auth.cookies)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("stream request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("stream status = %d, body = %s", resp.StatusCode, bodyBytes)
	}
	if err := srv.Store.Project.Close(); err != nil {
		t.Fatalf("close project database: %v", err)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	body := string(bodyBytes)
	if !strings.Contains(body, "event: error\n") {
		t.Fatalf("stream body missing error event: %q", body)
	}
	if !strings.Contains(body, `"code":"internal_error"`) {
		t.Fatalf("stream body missing internal_error code: %q", body)
	}
}

func TestEventQueriesReturnInternalErrorWhenStoreQueryFails(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "all events", path: "/api/v1/events"},
		{name: "run events", path: "/api/v1/runs/run_missing/events"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t)
			auth := sessionAuth(t, srv)
			if err := srv.Store.Project.Close(); err != nil {
				t.Fatalf("close project database: %v", err)
			}

			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			addCookies(req, auth.cookies)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("event query status = %d, want 500; body = %s", rec.Code, rec.Body.String())
			}
			payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
			errData := payload["error"].(map[string]any)
			if errData["code"] != string(core.ErrInternal) {
				t.Fatalf("error = %#v, want internal_error", errData)
			}
			if _, ok := payload["data"]; ok {
				t.Fatalf("event query returned success data on store error: %#v", payload)
			}
		})
	}
}

func TestEventQueriesRejectInvalidAfterSeq(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "all events negative", path: "/api/v1/events?after_seq=-1"},
		{name: "all events non integer", path: "/api/v1/events?after_seq=abc"},
		{name: "run events negative", path: "/api/v1/runs/run_missing/events?after_seq=-1"},
		{name: "run events non integer", path: "/api/v1/runs/run_missing/events?after_seq=abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t)
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			addCookies(req, sessionAuth(t, srv).cookies)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("event query status = %d, want 400; body = %s", rec.Code, rec.Body.String())
			}
			payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
			errData := payload["error"].(map[string]any)
			if errData["code"] != string(core.ErrInvalidRequest) {
				t.Fatalf("error = %#v, want invalid_request", errData)
			}
		})
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

func TestCancelRunRejectsNonObjectBodyWithoutCancelling(t *testing.T) {
	srv := newTestServer(t)
	issue, err := srv.Store.CreateIssue(store.CreateIssueInput{Title: "Cancel non object", Description: "desc"})
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

	req := httptest.NewRequest(http.MethodPost, "/api/v1/runs/"+run.ID+"/cancel", strings.NewReader(`null`))
	req.Header.Set("Content-Type", "application/json")
	applySessionAuth(req, sessionAuth(t, srv))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("non-object cancel status = %d, body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
	errData := payload["error"].(map[string]any)
	if errData["code"] != string(core.ErrInvalidRequest) {
		t.Fatalf("error = %#v, want invalid_request", errData)
	}
	after, err := srv.Store.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if after.Status == core.RunCancelled {
		t.Fatalf("non-object cancel request cancelled run %#v", after)
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

func TestCancelRunReturnsErrorWhenRunReloadFails(t *testing.T) {
	srv := newTestServer(t)
	issue, err := srv.Store.CreateIssue(store.CreateIssueInput{Title: "Cancel reload", Description: "desc"})
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
	if err := srv.Store.Project.Exec(`CREATE TRIGGER delete_issue_after_cancel_event AFTER INSERT ON run_events
WHEN NEW.event_type = 'run.cancelled'
BEGIN
  DELETE FROM issues WHERE id = NEW.issue_id;
END;`); err != nil {
		t.Fatalf("create cancel event trigger: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/runs/"+run.ID+"/cancel", strings.NewReader(`{"reason":"reload failure"}`))
	req.Header.Set("Content-Type", "application/json")
	applySessionAuth(req, sessionAuth(t, srv))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("cancel status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
	errData := payload["error"].(map[string]any)
	if errData["code"] != string(core.ErrNotFound) {
		t.Fatalf("error = %#v, want not_found", errData)
	}
	if _, ok := payload["data"]; ok {
		t.Fatalf("cancel returned success data on run reload error: %#v", payload)
	}
}

func TestArtifactContentDoesNotServeSymlinkOutsideAllowedRoots(t *testing.T) {
	srv := newTestServer(t)
	artifactsDir := filepath.Join(srv.Store.RepoRoot, ".symphony", "artifacts")
	if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
		t.Fatalf("mkdir artifacts: %v", err)
	}
	goodPath := filepath.Join(artifactsDir, "good.txt")
	if err := os.WriteFile(goodPath, []byte("allowed artifact content"), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	if err := srv.Store.InsertArtifact(store.ArtifactRecord{ID: "art_good", Kind: "diagnostic", Path: goodPath, Redacted: true}); err != nil {
		t.Fatalf("InsertArtifact good: %v", err)
	}
	secretPath := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secretPath, []byte("external artifact secret"), 0o644); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	linkPath := filepath.Join(artifactsDir, "secret-link.txt")
	if err := os.Symlink(secretPath, linkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := srv.Store.InsertArtifact(store.ArtifactRecord{ID: "art_link", Kind: "diagnostic", Path: linkPath, Redacted: true}); err != nil {
		t.Fatalf("InsertArtifact link: %v", err)
	}
	auth := sessionAuth(t, srv)

	goodReq := httptest.NewRequest(http.MethodGet, "/api/v1/artifacts/art_good/content", nil)
	addCookies(goodReq, auth.cookies)
	goodRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(goodRec, goodReq)
	if goodRec.Code != http.StatusOK || !strings.Contains(goodRec.Body.String(), "allowed artifact content") {
		t.Fatalf("legal artifact status = %d, body = %s", goodRec.Code, goodRec.Body.String())
	}

	linkReq := httptest.NewRequest(http.MethodGet, "/api/v1/artifacts/art_link/content", nil)
	addCookies(linkReq, auth.cookies)
	linkRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(linkRec, linkReq)
	if linkRec.Code != http.StatusForbidden && linkRec.Code != http.StatusNotFound {
		t.Fatalf("symlink artifact status = %d, want 403 or 404; body = %s", linkRec.Code, linkRec.Body.String())
	}
	if strings.Contains(linkRec.Body.String(), "external artifact secret") {
		t.Fatalf("served external artifact secret through symlink: body = %s", linkRec.Body.String())
	}
}

func TestArtifactRoutesRejectNonGETMethods(t *testing.T) {
	srv := newTestServer(t)
	artifactsDir := filepath.Join(srv.Store.RepoRoot, ".symphony", "artifacts")
	if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
		t.Fatalf("mkdir artifacts: %v", err)
	}
	artifactPath := filepath.Join(artifactsDir, "method.txt")
	if err := os.WriteFile(artifactPath, []byte("allowed artifact content"), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	if err := srv.Store.InsertArtifact(store.ArtifactRecord{ID: "art_method", Kind: "diagnostic", Path: artifactPath, Redacted: true}); err != nil {
		t.Fatalf("InsertArtifact: %v", err)
	}
	auth := sessionAuth(t, srv)

	for _, method := range []string{http.MethodHead, http.MethodPost, http.MethodPut, http.MethodDelete} {
		for _, path := range []string{"/api/v1/artifacts/art_method", "/api/v1/artifacts/art_method/content"} {
			t.Run(method+" "+path, func(t *testing.T) {
				req := httptest.NewRequest(method, path, nil)
				applySessionAuth(req, auth)
				rec := httptest.NewRecorder()
				srv.Handler().ServeHTTP(rec, req)

				if rec.Code != http.StatusMethodNotAllowed {
					t.Fatalf("artifact route status = %d, want 405; body = %s", rec.Code, rec.Body.String())
				}
				if strings.Contains(rec.Body.String(), "art_method") || strings.Contains(rec.Body.String(), "allowed artifact content") {
					t.Fatalf("artifact route returned artifact body for %s %s: %s", method, path, rec.Body.String())
				}
			})
		}
	}
}

func TestArtifactRawContentReturnsForbiddenWithoutLeakingContent(t *testing.T) {
	srv := newTestServer(t)
	artifactsDir := filepath.Join(srv.Store.RepoRoot, ".symphony", "artifacts")
	if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
		t.Fatalf("mkdir artifacts: %v", err)
	}
	rawPath := filepath.Join(artifactsDir, "raw.log")
	if err := os.WriteFile(rawPath, []byte("raw secret log content"), 0o644); err != nil {
		t.Fatalf("write raw artifact: %v", err)
	}
	if err := srv.Store.InsertArtifact(store.ArtifactRecord{ID: "art_raw", Kind: "codex_log", Path: rawPath, Redacted: false}); err != nil {
		t.Fatalf("InsertArtifact raw: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/artifacts/art_raw/content", nil)
	addCookies(req, sessionAuth(t, srv).cookies)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("raw artifact status = %d, want 403; body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "raw secret log content") {
		t.Fatalf("raw artifact response leaked content: %s", rec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
	errData := payload["error"].(map[string]any)
	if got := errData["code"]; got != string(core.ErrRawLogAccessUnsupported) {
		t.Fatalf("error code = %v, want %s; body = %s", got, core.ErrRawLogAccessUnsupported, rec.Body.String())
	}
	if _, ok := payload["data"]; ok {
		t.Fatalf("raw artifact returned success data: %#v", payload)
	}
}

func TestArtifactContentMissingFileUnderAllowedRootReturnsNotFound(t *testing.T) {
	srv := newTestServer(t)
	artifactsDir := filepath.Join(srv.Store.RepoRoot, ".symphony", "artifacts")
	if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
		t.Fatalf("mkdir artifacts: %v", err)
	}
	missingPath := filepath.Join(artifactsDir, "missing.txt")
	if err := srv.Store.InsertArtifact(store.ArtifactRecord{ID: "art_missing", Kind: "diagnostic", Path: missingPath, Redacted: true}); err != nil {
		t.Fatalf("InsertArtifact missing: %v", err)
	}
	auth := sessionAuth(t, srv)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/artifacts/art_missing/content", nil)
	addCookies(req, auth.cookies)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing artifact status = %d, want 404; body = %s", rec.Code, rec.Body.String())
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

func TestDiagnosticsExportRejectsUnsupportedBodyOptions(t *testing.T) {
	tests := []struct {
		name string
		body string
		code core.APIErrorCode
	}{
		{name: "malformed JSON", body: `{"include_raw_logs":`, code: core.ErrInvalidRequest},
		{name: "null body", body: `null`, code: core.ErrInvalidRequest},
		{name: "null include raw logs", body: `{"include_raw_logs":null}`, code: core.ErrInvalidRequest},
		{name: "raw logs", body: `{"include_raw_logs":true}`, code: core.ErrRawLogAccessUnsupported},
		{name: "unknown field", body: `{"unexpected":true}`, code: core.ErrInvalidRequest},
		{name: "wrong type", body: `{"include_raw_logs":"true"}`, code: core.ErrInvalidRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/diagnostics/export", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			applySessionAuth(req, sessionAuth(t, srv))
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("diagnostics export status = %d, body = %s", rec.Code, rec.Body.String())
			}
			payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
			errData := payload["error"].(map[string]any)
			if got := errData["code"]; got != string(tt.code) {
				t.Fatalf("error code = %v, want %s; body = %s", got, tt.code, rec.Body.String())
			}
		})
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

func TestApprovalRejectsUnsupportedDecision(t *testing.T) {
	srv := newTestServer(t)
	run := prepareCompletedHTTPRun(t, srv)
	approvalID := core.NewID("apr_")
	if err := srv.Store.Project.Exec(`INSERT INTO approval_requests(id,run_id,issue_id,kind,status,request_json,created_at) VALUES(?,?,?,?,?,?,?)`, approvalID, run.ID, run.IssueID, "command", "pending", `{"command":"test"}`, core.Now()); err != nil {
		t.Fatalf("insert approval: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+approvalID+"/decide", strings.NewReader(`{"decision":"approve_forever","reason":"bad input"}`))
	req.Header.Set("Content-Type", "application/json")
	applySessionAuth(req, sessionAuth(t, srv))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unsupported decision status = %d, body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
	errData := payload["error"].(map[string]any)
	if errData["code"] != string(core.ErrInvalidRequest) {
		t.Fatalf("error = %#v, want invalid_request", errData)
	}
	row, err := srv.Store.Project.QueryOne(`SELECT status FROM approval_requests WHERE id=?`, approvalID)
	if err != nil {
		t.Fatalf("get approval: %v", err)
	}
	if row["status"].String() != "pending" {
		t.Fatalf("approval status = %s, want pending", row["status"].String())
	}
}

func TestInternalErrorMapsToHTTP500(t *testing.T) {
	rec := httptest.NewRecorder()
	apiErr(rec, core.NewError(core.ErrInternal, "boom", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("internal error status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestCoreConflictErrorsMapToHTTP409(t *testing.T) {
	for _, code := range []core.APIErrorCode{
		core.ErrIssueAlreadyRunning,
		core.ErrIssueDispatchPaused,
		core.ErrIssueBlocked,
		core.ErrConcurrencyLimitReached,
		core.ErrReviewPacketRequired,
	} {
		t.Run(string(code), func(t *testing.T) {
			rec := httptest.NewRecorder()
			apiErr(rec, core.NewError(code, "conflict", nil))
			if rec.Code != http.StatusConflict {
				t.Fatalf("%s status = %d, body = %s", code, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestWorkflowRoutesReturnImplementedShapes(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantFields []string
	}{
		{
			name:       "status",
			method:     http.MethodGet,
			path:       "/api/v1/workflow",
			wantFields: []string{"workflow_path", "validation", "config"},
		},
		{
			name:       "reload",
			method:     http.MethodPost,
			path:       "/api/v1/workflow/reload",
			body:       `{}`,
			wantFields: []string{"reloaded", "validation"},
		},
		{
			name:       "render preview",
			method:     http.MethodPost,
			path:       "/api/v1/workflow/render-preview",
			body:       `{}`,
			wantFields: []string{"source", "rendered_prompt_preview", "prompt_metadata", "validation", "redactions_applied", "raw_prompt_exposed", "raw_codex_log_shown"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t)
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			if tt.method != http.MethodGet {
				applySessionAuth(req, sessionAuth(t, srv))
			} else {
				addCookies(req, sessionAuth(t, srv).cookies)
			}
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s status = %d, body = %s", tt.name, rec.Code, rec.Body.String())
			}
			payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
			data := payload["data"].(map[string]any)
			for _, field := range tt.wantFields {
				if _, ok := data[field]; !ok {
					t.Fatalf("%s data missing %q: %#v", tt.name, field, data)
				}
			}
		})
	}
}

func TestWorkflowRoutesRejectUnsupportedBodyFields(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
	}{
		{name: "validate null", path: "/api/v1/workflow/validate", body: `null`},
		{name: "validate non object", path: "/api/v1/workflow/validate", body: `[]`},
		{name: "validate null dry run", path: "/api/v1/workflow/validate", body: `{"dry_run":null}`},
		{name: "validate trailing json", path: "/api/v1/workflow/validate", body: `{"dry_run":true} {}`},
		{name: "reload unknown field", path: "/api/v1/workflow/reload", body: `{"dry_run":true}`},
		{name: "reload null", path: "/api/v1/workflow/reload", body: `null`},
		{name: "render preview candidate source", path: "/api/v1/workflow/render-preview", body: `{"source":"candidate"}`},
		{name: "render preview malformed", path: "/api/v1/workflow/render-preview", body: `{"source":`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t)
			req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			applySessionAuth(req, sessionAuth(t, srv))
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("workflow route status = %d, want 400; body = %s", rec.Code, rec.Body.String())
			}
			payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
			errData := payload["error"].(map[string]any)
			if errData["code"] != string(core.ErrInvalidRequest) {
				t.Fatalf("error = %#v, want invalid_request", errData)
			}
		})
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

func TestReviewPacketReturnsErrorWhenArtifactsQueryFails(t *testing.T) {
	srv := newTestServer(t)
	issue, err := srv.Store.CreateIssue(store.CreateIssueInput{Title: "Review artifact reload", Description: "desc"})
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
	handoff, err := srv.Store.InsertHandoff(issue.ID, run.ID, "payload-hash", map[string]any{
		"summary":      "ready for review",
		"target_state": "Human Review",
	})
	if err != nil {
		t.Fatalf("InsertHandoff: %v", err)
	}
	reviewPacketID, err := srv.Store.InsertReviewPacket(issue.ID, run.ID, handoff.ID, srv.Store.RepoRoot, "review.md", "review.json", "patch.diff", "changed.txt", "untracked.txt", "diffstat.txt", "")
	if err != nil {
		t.Fatalf("InsertReviewPacket: %v", err)
	}
	if err := srv.Store.CompleteRunWithReview(run.ID, reviewPacketID); err != nil {
		t.Fatalf("CompleteRunWithReview: %v", err)
	}
	if err := srv.Store.Project.Exec(`DROP TABLE artifacts`); err != nil {
		t.Fatalf("drop artifacts: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/reviews/"+issue.Identifier, nil)
	addCookies(req, sessionAuth(t, srv).cookies)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("review status = %d, want 500; body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
	errData := payload["error"].(map[string]any)
	if errData["code"] != string(core.ErrInternal) {
		t.Fatalf("error = %#v, want internal_error", errData)
	}
	if _, ok := payload["data"]; ok {
		t.Fatalf("review returned success data on artifact query error: %#v", payload)
	}
}

func TestReviewPacketReturns404WhenIssueDoesNotExist(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/reviews/LOC-404", nil)
	addCookies(req, sessionAuth(t, srv).cookies)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("review status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
	errData := payload["error"].(map[string]any)
	if errData["code"] != string(core.ErrNotFound) {
		t.Fatalf("error = %#v, want not_found", errData)
	}
	if _, ok := payload["data"]; ok {
		t.Fatalf("review returned success data for missing issue: %#v", payload)
	}
}

func TestReviewPacketReturns409WhenPacketIsMissing(t *testing.T) {
	srv := newTestServer(t)
	issue, err := srv.Store.CreateIssue(store.CreateIssueInput{Title: "Review packet missing", Description: "desc"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/reviews/"+issue.Identifier, nil)
	addCookies(req, sessionAuth(t, srv).cookies)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("review status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
	errData := payload["error"].(map[string]any)
	if errData["code"] != string(core.ErrReviewPacketRequired) {
		t.Fatalf("error = %#v, want review_packet_required", errData)
	}
	if _, ok := payload["data"]; ok {
		t.Fatalf("review returned success data for missing packet: %#v", payload)
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

func insertLocalSessionWithExpiry(t *testing.T, srv *Server, kind, expiresAt string) string {
	t.Helper()
	token := security.NewToken()
	if err := srv.Store.App.Exec(`INSERT INTO local_sessions(id,project_id,kind,token_hash,user_label,created_at,expires_at) VALUES(?,?,?,?,?,?,?)`, core.NewID("ses_"), srv.Store.ProjectID, kind, security.HashToken(token), "test-session", core.Now(), expiresAt); err != nil {
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
	handoff, err := srv.Store.InsertHandoff(issue.ID, run.ID, "payload-hash", map[string]any{
		"summary":      "ready for review",
		"target_state": "Human Review",
	})
	if err != nil {
		t.Fatalf("InsertHandoff: %v", err)
	}
	reviewPacketID, err := srv.Store.InsertReviewPacket(issue.ID, run.ID, handoff.ID, srv.Store.RepoRoot, "review.md", "review.json", "patch.diff", "changed.txt", "untracked.txt", "diffstat.txt", "")
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
