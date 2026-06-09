package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"local-symphony/internal/store"
)

const (
	httpapiSentinelPrompt = "SYNTHETIC_PROMPT_BODY_do_not_leak_in_diagnostics"
	httpapiSentinelLog    = "SYNTHETIC_CODEX_LOG_do_not_leak_in_diagnostics"
)

// TestStateRouteExposesCodexAvailabilityOnSuccess confirms the
// /api/v1/state envelope carries a `codex` block with
// available=true and the parsed protocol/schema metadata when a
// repo-local fixture is present.
func TestStateRouteExposesCodexAvailabilityOnSuccess(t *testing.T) {
	root := t.TempDir()
	if err := copyCodexFixtureForTest(t, root); err != nil {
		t.Fatalf("copy codex fixture: %v", err)
	}
	srv := newTestServerWithRepo(t, root)
	t.Setenv("SYMPHONY_CODEX_FIXTURE_ROOT", filepath.Join(root, "fixtures"))
	t.Setenv("SYMPHONY_CODEX_VERSION_OUTPUT", "codex 0.0.0-test")
	auth := sessionAuth(t, srv)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
	addCookies(req, auth.cookies)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("state status = %d, body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
	data, ok := payload["data"].(map[string]any)
	if !ok {
		t.Fatalf("state data is %T, want map[string]any", payload["data"])
	}
	codex, ok := data["codex"].(map[string]any)
	if !ok {
		t.Fatalf("state.codex is %T, want map[string]any; payload=%#v", data["codex"], payload)
	}
	if got := codex["available"]; got != true {
		t.Fatalf("state.codex.available = %v, want true", got)
	}
	if got := codex["version"]; got != "0.0.0-test" {
		t.Fatalf("state.codex.version = %v, want 0.0.0-test", got)
	}
	support, ok := codex["support"].(map[string]any)
	if !ok {
		t.Fatalf("state.codex.support is %T, want map[string]any", codex["support"])
	}
	if got := support["cli"]; got != "supported" {
		t.Fatalf("state.codex.support.cli = %v, want supported", got)
	}
	metadata, ok := codex["metadata"].(map[string]any)
	if !ok || metadata == nil {
		t.Fatalf("state.codex.metadata is %v, want populated map", codex["metadata"])
	}
	if got := metadata["protocol_version"]; got != "protocol-test-v1" {
		t.Fatalf("state.codex.metadata.protocol_version = %v, want protocol-test-v1", got)
	}
	preflight, ok := codex["last_preflight"].(map[string]any)
	if !ok {
		t.Fatalf("state.codex.last_preflight is %T, want map[string]any", codex["last_preflight"])
	}
	if got, ok := preflight["ran_at"].(string); !ok || got == "" {
		t.Fatalf("state.codex.last_preflight.ran_at is empty")
	}
}

// TestStateRouteReportsUnsupportedCodexVersion confirms the
// unsupported_codex_version warning propagates through the
// /api/v1/state envelope when no fixture is present.
func TestStateRouteReportsUnsupportedCodexVersion(t *testing.T) {
	srv := newTestServer(t)
	t.Setenv("SYMPHONY_CODEX_FIXTURE_ROOT", t.TempDir())
	t.Setenv("SYMPHONY_CODEX_VERSION_OUTPUT", "codex 9.9.9-missing")
	auth := sessionAuth(t, srv)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
	addCookies(req, auth.cookies)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("state status = %d, body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
	data := payload["data"].(map[string]any)
	codex := data["codex"].(map[string]any)
	if got := codex["available"]; got != false {
		t.Fatalf("state.codex.available = %v, want false", got)
	}
	if got := codex["warning"]; got != "unsupported_codex_version" {
		t.Fatalf("state.codex.warning = %v, want unsupported_codex_version", got)
	}
	preflight := codex["last_preflight"].(map[string]any)
	if got := preflight["failure_code"]; got != "unsupported_codex_version" {
		t.Fatalf("state.codex.last_preflight.failure_code = %v, want unsupported_codex_version", got)
	}
}

// TestStateRouteRedactsSentinels ensures the state envelope never
// carries a synthetic-sentinel-shaped substring under any field.
func TestStateRouteRedactsSentinels(t *testing.T) {
	srv := newTestServer(t)
	t.Setenv("SYMPHONY_CODEX_FIXTURE_ROOT", t.TempDir())
	t.Setenv("SYMPHONY_CODEX_VERSION_OUTPUT", "codex 9.9.9-poison")
	auth := sessionAuth(t, srv)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
	addCookies(req, auth.cookies)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	for _, sentinel := range []string{httpapiSentinelPrompt, httpapiSentinelLog, "SYNTHETIC_OWNER_NONCE", "SYNTHETIC_API_SECRET"} {
		if strings.Contains(body, sentinel) {
			t.Fatalf("state body leaks sentinel %q: %s", sentinel, body)
		}
	}
}

// TestDiagnosticsRouteExposesCodexPreflight confirms the
// /api/v1/diagnostics envelope also carries the full codex
// block, with a non-empty ran_at preflight timestamp.
func TestDiagnosticsRouteExposesCodexPreflight(t *testing.T) {
	root := t.TempDir()
	if err := copyCodexFixtureForTest(t, root); err != nil {
		t.Fatalf("copy codex fixture: %v", err)
	}
	srv := newTestServerWithRepo(t, root)
	t.Setenv("SYMPHONY_CODEX_FIXTURE_ROOT", filepath.Join(root, "fixtures"))
	t.Setenv("SYMPHONY_CODEX_VERSION_OUTPUT", "codex 0.0.0-test")
	auth := sessionAuth(t, srv)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics", nil)
	addCookies(req, auth.cookies)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("diagnostics status = %d, body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeEnvelope(t, strings.NewReader(rec.Body.String()))
	data := payload["data"].(map[string]any)
	codex := data["codex"].(map[string]any)
	if got := codex["available"]; got != true {
		t.Fatalf("diagnostics.codex.available = %v, want true", got)
	}
	preflight := codex["last_preflight"].(map[string]any)
	ranAt, _ := preflight["ran_at"].(string)
	if ranAt == "" {
		t.Fatalf("diagnostics.codex.last_preflight.ran_at is empty")
	}
}

// newTestServerWithRepo opens a project at the given root and
// returns a test HTTP server. It is the project-root-aware
// counterpart of newTestServer.
func newTestServerWithRepo(t *testing.T, root string) *Server {
	t.Helper()
	st, err := store.InitProject(root, "LOC")
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	t.Cleanup(st.Close)
	return New(st)
}

// copyCodexFixtureForTest copies the testdata fixture tree the
// internal/agent/codex package ships with into <root>/fixtures.
func copyCodexFixtureForTest(t *testing.T, root string) error {
	t.Helper()
	src, err := codexTestdataDir()
	if err != nil {
		return err
	}
	dst := filepath.Join(root, "fixtures")
	for _, sub := range []string{"schema/0.0.0-test", "transcripts/0.0.0-test"} {
		if err := os.MkdirAll(filepath.Join(dst, sub), 0o755); err != nil {
			return err
		}
	}
	for _, sub := range []string{
		"schema/0.0.0-test/compatibility.json",
		"schema/0.0.0-test/schema.json",
		"transcripts/0.0.0-test/happy-path.jsonl",
	} {
		data, err := os.ReadFile(filepath.Join(src, sub))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dst, sub), data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// codexTestdataDir resolves the internal/agent/codex/testdata
// directory of the working copy. We use the working copy rather
// than the caller's module cache so the test is hermetic.
func codexTestdataDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(wd, "..", "agent", "codex", "testdata")
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		return "", os.ErrNotExist
	}
	return abs, nil
}
