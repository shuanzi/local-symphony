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

	req := httptest.NewRequest(http.MethodPost, "/tool/v1/call", strings.NewReader(`{"tool":"issue.get","input":{}}`))
	req.Header.Set("Authorization", "Bearer "+token)
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

func TestDiagnosticsExportRejectsUnsupportedBodyOptions(t *testing.T) {
	tests := []struct {
		name string
		body string
		code core.APIErrorCode
	}{
		{name: "malformed JSON", body: `{"include_raw_logs":`, code: core.ErrInvalidRequest},
		{name: "null body", body: `null`, code: core.ErrInvalidRequest},
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
