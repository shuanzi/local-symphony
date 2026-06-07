package daemonclient

import (
	"context"
	"encoding/json"
	"errors"
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
	"local-symphony/internal/db"
)

func newTestSession(t *testing.T, projectID, apiURL, token string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(app.CLISessionPath(projectID)), 0o700); err != nil {
		t.Fatalf("mkdir cli sessions: %v", err)
	}
	sf := SessionFile{ProjectID: projectID, APIURL: apiURL, Token: token, CreatedAt: "redacted"}
	b, _ := json.MarshalIndent(sf, "", "  ")
	if err := os.WriteFile(app.CLISessionPath(projectID), b, 0o600); err != nil {
		t.Fatalf("write cli session: %v", err)
	}
}

// healthServer wraps a custom handler with a /api/v1/health response so
// Discover's reachability probe sees a healthy daemon.
func healthServer(projectID string, h http.HandlerFunc) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":{"ok":true,"project_id":"%s"},"meta":{}}`+"\n", projectID)
			return
		}
		if h != nil {
			h(w, r)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
}

func TestDiscoverPrefersEnvOverride(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	server := healthServer("prj_x", nil)
	t.Cleanup(server.Close)
	t.Setenv(EnvOverride, server.URL)

	disc, err := Discover(context.Background(), "prj_x", t.TempDir(), true, "")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if disc.BaseURL != server.URL {
		t.Fatalf("BaseURL = %q, want %q", disc.BaseURL, server.URL)
	}
	if !strings.HasPrefix(disc.Source, "env:") {
		t.Fatalf("Source = %q, want env: prefix", disc.Source)
	}
}

func TestDiscoverFallsBackToUserConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvOverride, "")
	server := healthServer("prj_x", nil)
	t.Cleanup(server.Close)
	cfgDir := filepath.Join(home, ".symphony")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "daemon.json"), []byte(`{"api_url":"`+server.URL+`"}`), 0o600); err != nil {
		t.Fatalf("write daemon config: %v", err)
	}

	disc, err := Discover(context.Background(), "prj_x", t.TempDir(), true, "")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if disc.BaseURL != server.URL {
		t.Fatalf("BaseURL = %q, want %q", disc.BaseURL, server.URL)
	}
	if !strings.HasPrefix(disc.Source, "config:") {
		t.Fatalf("Source = %q, want config: prefix", disc.Source)
	}
}

func TestDiscoverFallsBackToRuntimeDescriptor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvOverride, "")
	// No user config on disk; only the runtime descriptor should resolve.
	server := healthServer("prj_runtime", nil)
	t.Cleanup(server.Close)

	projectRoot := t.TempDir()
	projectID := "prj_runtime"
	descPath := db.RuntimeDescriptorPath(projectID)
	if err := os.MkdirAll(filepath.Dir(descPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	payload := map[string]any{"api_url": server.URL, "project_id": projectID, "daemon_pid": os.Getpid()}
	b, _ := json.MarshalIndent(payload, "", "  ")
	if err := os.WriteFile(descPath, b, 0o600); err != nil {
		t.Fatalf("write runtime descriptor: %v", err)
	}

	disc, err := Discover(context.Background(), projectID, projectRoot, true, "")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if disc.BaseURL != server.URL {
		t.Fatalf("BaseURL = %q, want %q", disc.BaseURL, server.URL)
	}
	if !strings.HasPrefix(disc.Source, "runtime:") {
		t.Fatalf("Source = %q, want runtime: prefix", disc.Source)
	}
}

func TestDiscoverReturnsErrDaemonUnavailableWhenNothingConfigured(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(EnvOverride, "")
	_, err := Discover(context.Background(), "prj_x", t.TempDir(), true, "")
	if !errors.Is(err, ErrDaemonUnavailable) {
		t.Fatalf("Discover error = %v, want ErrDaemonUnavailable", err)
	}
}

func TestNewLoadsTokenFromProjectScopedSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	projectID := "prj_token"
	server := healthServer(projectID, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer scoped-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		fmt.Fprintln(w, `{"data":{"ok":true,"project_id":"`+projectID+`"},"meta":{}}`)
	})
	t.Cleanup(server.Close)
	newTestSession(t, projectID, server.URL, "scoped-token")

	c, err := New(context.Background(), Config{ProjectID: projectID, ProjectRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.Token != "scoped-token" {
		t.Fatalf("Token = %q, want scoped-token", c.Token)
	}
}

func TestNewRejectsUnreachableURL(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(EnvOverride, "")
	_, err := New(context.Background(), Config{ProjectID: "prj_unreach", BaseURL: "http://127.0.0.1:1", HTTPClient: &http.Client{Timeout: 50 * time.Millisecond}})
	if err == nil {
		t.Fatal("New succeeded, want unreachable error")
	}
	if !errors.Is(err, ErrDaemonUnavailable) && !errors.Is(err, ErrSessionMissing) {
		t.Fatalf("New error = %v, want ErrDaemonUnavailable or ErrSessionMissing", err)
	}
}

func TestNewReturnsErrSessionMissingWithoutToken(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	server := healthServer("prj_x", nil)
	t.Cleanup(server.Close)
	_, err := New(context.Background(), Config{ProjectID: "prj_x", BaseURL: server.URL})
	if !errors.Is(err, ErrSessionMissing) {
		t.Fatalf("New error = %v, want ErrSessionMissing", err)
	}
}

func TestDoUnwrapsSuccessEnvelope(t *testing.T) {
	server := healthServer("prj_x", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"data":{"issues":[{"id":"iss_1"}],"page":{"limit":1}},"meta":{"request_id":"req_abc"}}`)
	})
	t.Cleanup(server.Close)

	c, err := New(context.Background(), Config{ProjectID: "prj_x", BaseURL: server.URL, Token: "tok"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := c.Do(context.Background(), http.MethodGet, "/api/v1/issues", nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(string(resp.Data), `"iss_1"`) {
		t.Fatalf("Data = %s, want iss_1", string(resp.Data))
	}
}

func TestDoUnwrapsErrorEnvelope(t *testing.T) {
	server := healthServer("prj_x", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintln(w, `{"error":{"code":"invalid_request","message":"bad","details":{"field":"x"},"request_id":"req_1"}}`)
	})
	t.Cleanup(server.Close)

	c, err := New(context.Background(), Config{ProjectID: "prj_x", BaseURL: server.URL, Token: "tok"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.Do(context.Background(), http.MethodGet, "/api/v1/issues", nil)
	if err == nil {
		t.Fatal("Do succeeded, want invalid_request error")
	}
	ae := core.AsAPIError(err)
	if ae.Code != core.ErrInvalidRequest {
		t.Fatalf("code = %s, want invalid_request", ae.Code)
	}
	if ae.Details["field"] != "x" {
		t.Fatalf("details = %#v, want field=x", ae.Details)
	}
}

func TestDoReportsDaemonUnavailableOnNetworkError(t *testing.T) {
	c, err := New(context.Background(), Config{ProjectID: "prj_x", BaseURL: "http://127.0.0.1:1", Token: "tok", HTTPClient: &http.Client{Timeout: 50 * time.Millisecond}})
	if err != nil {
		if !errors.Is(err, ErrDaemonUnavailable) {
			t.Fatalf("New error = %v, want ErrDaemonUnavailable", err)
		}
		return
	}
	_, err = c.Do(context.Background(), http.MethodGet, "/api/v1/issues", nil)
	if !errors.Is(err, ErrDaemonUnavailable) {
		t.Fatalf("Do error = %v, want ErrDaemonUnavailable", err)
	}
}

func TestUnwrapMapReturnsStableObjectStructure(t *testing.T) {
	server := healthServer("prj_x", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"data":{"project_id":"prj_x","issues":[],"runs":[]},"meta":{}}`)
	})
	t.Cleanup(server.Close)

	c, err := New(context.Background(), Config{ProjectID: "prj_x", BaseURL: server.URL, Token: "tok"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := c.UnwrapMap(context.Background(), http.MethodGet, "/api/v1/state", nil)
	if err != nil {
		t.Fatalf("UnwrapMap: %v", err)
	}
	if out["project_id"] != "prj_x" {
		t.Fatalf("project_id = %v, want prj_x", out["project_id"])
	}
	if _, ok := out["meta"]; ok {
		t.Fatalf("UnwrapMap leaked meta: %#v", out)
	}
}

func TestSessionWriteFileIs0600AndReadable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	projectID := "prj_w"
	path, err := WriteSessionFile(projectID, SessionFile{Token: "abc", APIURL: "http://x"})
	if err != nil {
		t.Fatalf("WriteSessionFile: %v", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := st.Mode().Perm(); got != 0o600 {
		t.Fatalf("perm = %o, want 0600", got)
	}
	sf, err := ReadSessionFile(path, projectID, "")
	if err != nil {
		t.Fatalf("ReadSessionFile: %v", err)
	}
	if sf.Token != "abc" {
		t.Fatalf("token = %q, want abc", sf.Token)
	}
}

func TestSessionReadRejectsEmptyToken(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	projectID := "prj_empty"
	path := app.CLISessionPath(projectID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"project_id":"`+projectID+`","token":""}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := ReadSessionFile(path, projectID, ""); err == nil {
		t.Fatal("ReadSessionFile accepted empty token")
	}
}

func TestSessionReadRejectsMismatchedProject(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := app.CLISessionPath("prj_a")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"project_id":"prj_b","token":"x"}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := ReadSessionFile(path, "prj_a", ""); err == nil {
		t.Fatal("ReadSessionFile accepted mismatched project")
	}
}

func TestDeleteSessionFileIsIdempotent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	projectID := "prj_del"
	if err := DeleteSessionFile(projectID); err != nil {
		t.Fatalf("first DeleteSessionFile: %v", err)
	}
	if _, err := WriteSessionFile(projectID, SessionFile{Token: "x"}); err != nil {
		t.Fatalf("WriteSessionFile: %v", err)
	}
	if err := DeleteSessionFile(projectID); err != nil {
		t.Fatalf("second DeleteSessionFile: %v", err)
	}
	if err := DeleteSessionFile(projectID); err != nil {
		t.Fatalf("third DeleteSessionFile: %v", err)
	}
}

func TestReadAllSessionFilesSkipsInvalidEntries(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := filepath.Join(os.Getenv("HOME"), ".symphony", "cli-sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prj_a.json"), []byte(`{"project_id":"prj_a","token":"tok"}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prj_b.json"), []byte(`{"project_id":"prj_b","token":""}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ignored.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	sessions, err := ReadAllSessionFiles()
	if err != nil {
		t.Fatalf("ReadAllSessionFiles: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	if sessions[0].Token != "tok" {
		t.Fatalf("token = %q, want tok", sessions[0].Token)
	}
}

func TestDiscoveryRejectsMismatchedProjectID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvOverride, "")

	// Daemon responds with project_id=other — CLI is looking
	// for prj_abc. Discovery must reject this URL.
	other := "prj_other"
	server := healthServer(other, nil)
	t.Cleanup(server.Close)

	descPath := db.RuntimeDescriptorPath("prj_abc")
	if err := os.MkdirAll(filepath.Dir(descPath), 0o700); err != nil {
		t.Fatalf("mkdir runtime: %v", err)
	}
	if err := os.WriteFile(descPath,
		[]byte(`{"api_url":"`+server.URL+`","project_id":"prj_abc","daemon_pid":1234}`), 0o600); err != nil {
		t.Fatalf("write runtime: %v", err)
	}

	_, err := Discover(context.Background(), "prj_abc", t.TempDir(), true, "")
	if !errors.Is(err, ErrDaemonUnavailable) {
		t.Fatalf("Discover error = %v, want ErrDaemonUnavailable", err)
	}
}

func TestDiscoveryFallsBackWhenRuntimeDescriptorStale(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvOverride, "")

	// Two endpoints: a stale one (runtime descriptor points
	// at it but it reports the wrong project_id) and the
	// correct one (session file api_url). Discovery must
	// skip the stale one and accept the correct one.
	staleProject := "prj_stale"
	stale := healthServer(staleProject, nil)
	t.Cleanup(stale.Close)

	correctProject := "prj_abc"
	correct := healthServer(correctProject, nil)
	t.Cleanup(correct.Close)

	descPath := db.RuntimeDescriptorPath("prj_abc")
	if err := os.MkdirAll(filepath.Dir(descPath), 0o700); err != nil {
		t.Fatalf("mkdir runtime: %v", err)
	}
	if err := os.WriteFile(descPath,
		[]byte(`{"api_url":"`+stale.URL+`","project_id":"prj_abc","daemon_pid":1234}`), 0o600); err != nil {
		t.Fatalf("write runtime: %v", err)
	}
	if _, err := WriteSessionFile("prj_abc", SessionFile{ProjectID: "prj_abc", APIURL: correct.URL, Token: "tok"}); err != nil {
		t.Fatalf("WriteSessionFile: %v", err)
	}

	disc, err := Discover(context.Background(), "prj_abc", t.TempDir(), true, "")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if disc.BaseURL != correct.URL {
		t.Fatalf("BaseURL = %q, want %q (stale=%q skipped)", disc.BaseURL, correct.URL, stale.URL)
	}
	if !strings.HasPrefix(disc.Source, "session:") {
		t.Fatalf("Source = %q, want session: prefix", disc.Source)
	}
}

func TestDiscoveryFallsBackWhenSessionURLStale(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvOverride, "")

	// Runtime descriptor is correct; the session file
	// (last-resort) is stale. Discovery must skip the
	// session URL and accept the runtime descriptor.
	correctProject := "prj_abc"
	correct := healthServer(correctProject, nil)
	t.Cleanup(correct.Close)

	staleProject := "prj_stale"
	stale := healthServer(staleProject, nil)
	t.Cleanup(stale.Close)

	descPath := db.RuntimeDescriptorPath("prj_abc")
	if err := os.MkdirAll(filepath.Dir(descPath), 0o700); err != nil {
		t.Fatalf("mkdir runtime: %v", err)
	}
	if err := os.WriteFile(descPath,
		[]byte(`{"api_url":"`+correct.URL+`","project_id":"prj_abc","daemon_pid":1234}`), 0o600); err != nil {
		t.Fatalf("write runtime: %v", err)
	}
	if _, err := WriteSessionFile("prj_abc", SessionFile{ProjectID: "prj_abc", APIURL: stale.URL, Token: "tok"}); err != nil {
		t.Fatalf("WriteSessionFile: %v", err)
	}

	disc, err := Discover(context.Background(), "prj_abc", t.TempDir(), true, "")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if disc.BaseURL != correct.URL {
		t.Fatalf("BaseURL = %q, want %q (stale session URL skipped)", disc.BaseURL, correct.URL)
	}
	if !strings.HasPrefix(disc.Source, "runtime:") {
		t.Fatalf("Source = %q, want runtime: prefix", disc.Source)
	}
}

// TestDiscoverWithHintAcceptsMatchingProject pins the
// happy path of the round-3 fix: when the saved api_url
// is reachable AND its daemon advertises the expected
// project_id, Discover returns that exact URL.
func TestDiscoverWithHintAcceptsMatchingProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvOverride, "")

	projectID := "prj_abc"
	server := healthServer(projectID, nil)
	t.Cleanup(server.Close)

	disc, err := Discover(context.Background(), projectID, t.TempDir(), true, server.URL)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if disc.BaseURL != server.URL {
		t.Fatalf("BaseURL = %q, want %q (hint accepted)", disc.BaseURL, server.URL)
	}
	if !strings.HasPrefix(disc.Source, "hint:") {
		t.Fatalf("Source = %q, want hint: prefix", disc.Source)
	}
}

// TestDiscoverWithHintRejectsMismatchedProject pins the
// HIGH finding from adversarial round 3: when the saved
// api_url points at a different project's daemon, Discover
// must reject it. The hint is the ONLY candidate (no
// fallback to env / config / runtime) so a CLI bearer for
// project A is never sent to a daemon hosting project B.
func TestDiscoverWithHintRejectsMismatchedProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvOverride, "")

	// Daemon responds with project_id=other — CLI is looking
	// for prj_abc. Discover (with this URL as the hint) must
	// reject and return ErrDaemonUnavailable even though a
	// runtime descriptor pointing at a correct daemon could
	// otherwise resolve the call.
	other := "prj_foreign"
	wrongServer := healthServer(other, nil)
	t.Cleanup(wrongServer.Close)

	correctServer := healthServer("prj_abc", nil)
	t.Cleanup(correctServer.Close)
	descPath := db.RuntimeDescriptorPath("prj_abc")
	if err := os.MkdirAll(filepath.Dir(descPath), 0o700); err != nil {
		t.Fatalf("mkdir runtime: %v", err)
	}
	if err := os.WriteFile(descPath,
		[]byte(`{"api_url":"`+correctServer.URL+`","project_id":"prj_abc","daemon_pid":1234}`), 0o600); err != nil {
		t.Fatalf("write runtime: %v", err)
	}

	_, err := Discover(context.Background(), "prj_abc", t.TempDir(), true, wrongServer.URL)
	if !errors.Is(err, ErrDaemonUnavailable) {
		t.Fatalf("Discover error = %v, want ErrDaemonUnavailable (hint rejected, fallback disabled)", err)
	}
}

// TestDiscoverWithHintRejectsUnreachableURL pins the
// unreachable case of the round-3 fix: a saved api_url
// whose daemon is offline still degrades gracefully. The
// hint is rejected by the reachability probe and Discover
// returns ErrDaemonUnavailable.
func TestDiscoverWithHintRejectsUnreachableURL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvOverride, "")

	// 127.0.0.1:1 is a port that should never accept
	// connections on a healthy host. The 50ms timeout in
	// reachable() keeps the test bounded.
	_, err := Discover(context.Background(), "prj_abc", t.TempDir(), true, "http://127.0.0.1:1")
	if !errors.Is(err, ErrDaemonUnavailable) {
		t.Fatalf("Discover error = %v, want ErrDaemonUnavailable (unreachable hint rejected)", err)
	}
}

func TestParseHealthProjectIDEnvelope(t *testing.T) {
	body := []byte(`{"data":{"ok":true,"project_id":"prj_xyz"},"meta":{"request_id":"req_1"}}`)
	if got := parseHealthProjectID(body); got != "prj_xyz" {
		t.Fatalf("parseHealthProjectID(envelope) = %q, want prj_xyz", got)
	}
	body = []byte(`{"ok":true,"project_id":"prj_flat"}`)
	if got := parseHealthProjectID(body); got != "prj_flat" {
		t.Fatalf("parseHealthProjectID(flat) = %q, want prj_flat", got)
	}
	if got := parseHealthProjectID([]byte(`not json`)); got != "" {
		t.Fatalf("parseHealthProjectID(garbage) = %q, want empty", got)
	}
	if got := parseHealthProjectID(nil); got != "" {
		t.Fatalf("parseHealthProjectID(nil) = %q, want empty", got)
	}
}

func TestNormalizeBaseURL(t *testing.T) {
	cases := map[string]string{
		"http://127.0.0.1:3777/":   "http://127.0.0.1:3777",
		"http://127.0.0.1:3777":    "http://127.0.0.1:3777",
		"  http://localhost:9000 ": "http://localhost:9000",
	}
	for in, want := range cases {
		got, err := normalizeBaseURL(in, true)
		if err != nil {
			t.Fatalf("normalizeBaseURL(%q): %v", in, err)
		}
		if got != want {
			t.Fatalf("normalizeBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := normalizeBaseURL("ftp://nope", true); err == nil {
		t.Fatal("normalizeBaseURL accepted non-http scheme")
	}
	if _, err := normalizeBaseURL("", true); err == nil {
		t.Fatal("normalizeBaseURL accepted empty")
	}
	if _, err := normalizeBaseURL("http://[::1", true); err == nil {
		t.Fatal("normalizeBaseURL accepted malformed URL")
	}
	// v1 default (allowRemote=false) must reject any non-loopback host
	// so a poisoned SYMPHONY_DAEMON_URL, daemon.json, runtime
	// descriptor, or session api_url cannot route the CLI bearer to a
	// remote endpoint that mimics the project_id.
	if _, err := normalizeBaseURL("http://evil.example.com:8080/", false); err == nil {
		t.Fatal("normalizeBaseURL accepted non-loopback host with allowRemote=false")
	}
	if _, err := normalizeBaseURL("https://8.8.8.8/", false); err == nil {
		t.Fatal("normalizeBaseURL accepted public IPv4 with allowRemote=false")
	}
}

func TestSummarizeLimitsLength(t *testing.T) {
	long := strings.Repeat("a", 1024)
	got := summarize([]byte(long))
	if len(got) >= 1024 {
		t.Fatalf("summarize did not truncate: len=%d", len(got))
	}
	if !strings.HasSuffix(got, "...(truncated)") {
		t.Fatalf("summarize suffix = %q", got)
	}
}

func TestDoTreatsEmptySuccessBodyAsNoData(t *testing.T) {
	server := healthServer("prj_x", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	t.Cleanup(server.Close)

	c, err := New(context.Background(), Config{ProjectID: "prj_x", BaseURL: server.URL, Token: "tok"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := c.Do(context.Background(), http.MethodPost, "/api/v1/x", nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("StatusCode = %d, want 204", resp.StatusCode)
	}
	if len(resp.Data) != 0 {
		t.Fatalf("Data = %s, want empty", resp.Data)
	}
}

func TestDecodeErrorHandlesNonJSON(t *testing.T) {
	ae, err := decodeError([]byte("plain text body"), http.StatusInternalServerError)
	if err != nil {
		t.Fatalf("decodeError: %v", err)
	}
	if ae.Code != core.ErrInternal {
		t.Fatalf("code = %s, want internal_error", ae.Code)
	}
}

func TestDecodeErrorDefaultsToStatusCodeWhenEnvelopeLacksCode(t *testing.T) {
	ae, err := decodeError([]byte(`{"error":{"message":"hi"}}`), http.StatusUnauthorized)
	if err != nil {
		t.Fatalf("decodeError: %v", err)
	}
	if ae.Code != core.ErrUnauthorized {
		t.Fatalf("code = %s, want unauthorized", ae.Code)
	}
}

func TestPostSerializesJSONBody(t *testing.T) {
	var seenBody []byte
	server := healthServer("prj_x", func(w http.ResponseWriter, r *http.Request) {
		seenBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"data":{"ok":true},"meta":{}}`)
	})
	t.Cleanup(server.Close)
	c, err := New(context.Background(), Config{ProjectID: "prj_x", BaseURL: server.URL, Token: "tok"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	body := map[string]any{"title": "x"}
	if err := c.Post(context.Background(), "/api/v1/issues", body, nil); err != nil {
		t.Fatalf("Post: %v", err)
	}
	if !strings.Contains(string(seenBody), `"title":"x"`) {
		t.Fatalf("body = %s, want title=x", string(seenBody))
	}
}

func TestBuildTargetURLPreservesQueryString(t *testing.T) {
	cases := []struct {
		base string
		path string
		want string
	}{
		{base: "http://127.0.0.1:7331", path: "/api/v1/issues?limit=1", want: "http://127.0.0.1:7331/api/v1/issues?limit=1"},
		{base: "http://127.0.0.1:7331/", path: "/api/v1/issues?limit=1&state=Ready", want: "http://127.0.0.1:7331/api/v1/issues?limit=1&state=Ready"},
		{base: "http://127.0.0.1:7331", path: "/api/v1/issues", want: "http://127.0.0.1:7331/api/v1/issues"},
		{base: "http://127.0.0.1:7331", path: "api/v1/issues?limit=1", want: "http://127.0.0.1:7331/api/v1/issues?limit=1"},
	}
	for _, c := range cases {
		got, err := buildTargetURL(c.base, c.path)
		if err != nil {
			t.Fatalf("buildTargetURL(%q, %q): %v", c.base, c.path, err)
		}
		if got != c.want {
			t.Fatalf("buildTargetURL(%q, %q) = %q, want %q", c.base, c.path, got, c.want)
		}
	}
}

func TestSplitPathQuery(t *testing.T) {
	cases := []struct {
		in     string
		path   string
		rawQry string
	}{
		{in: "/api/v1/issues?limit=1", path: "/api/v1/issues", rawQry: "limit=1"},
		{in: "/api/v1/issues", path: "/api/v1/issues", rawQry: ""},
		{in: "/api/v1/issues?", path: "/api/v1/issues", rawQry: ""},
		{in: "?", path: "", rawQry: ""},
	}
	for _, c := range cases {
		p, q := splitPathQuery(c.in)
		if p != c.path || q != c.rawQry {
			t.Fatalf("splitPathQuery(%q) = (%q, %q), want (%q, %q)", c.in, p, q, c.path, c.rawQry)
		}
	}
}

func TestDoPreservesQueryStringInRequestURL(t *testing.T) {
	var seenPath, seenRawQuery string
	server := healthServer("prj_x", func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenRawQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"data":{"items":[]},"meta":{}}`)
	})
	t.Cleanup(server.Close)
	c, err := New(context.Background(), Config{ProjectID: "prj_x", BaseURL: server.URL, Token: "tok"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Get(context.Background(), "/api/v1/issues?limit=1", nil); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if seenPath != "/api/v1/issues" {
		t.Fatalf("path = %q, want /api/v1/issues", seenPath)
	}
	if seenRawQuery != "limit=1" {
		t.Fatalf("raw query = %q, want limit=1", seenRawQuery)
	}
}

func TestUnwrapArrayDecodesListResponse(t *testing.T) {
	server := healthServer("prj_x", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"data":[{"id":"run_1"},{"id":"run_2"}],"meta":{}}`)
	})
	t.Cleanup(server.Close)
	c, err := New(context.Background(), Config{ProjectID: "prj_x", BaseURL: server.URL, Token: "tok"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	items, err := c.UnwrapArray(context.Background(), "GET", "/api/v1/runs", nil)
	if err != nil {
		t.Fatalf("UnwrapArray: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %v, want 2", items)
	}
}

func TestUnwrapArrayRejectsObjectResponse(t *testing.T) {
	server := healthServer("prj_x", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"data":{"items":[]},"meta":{}}`)
	})
	t.Cleanup(server.Close)
	c, err := New(context.Background(), Config{ProjectID: "prj_x", BaseURL: server.URL, Token: "tok"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.UnwrapArray(context.Background(), "GET", "/api/v1/runs", nil)
	if err == nil {
		t.Fatal("UnwrapArray accepted an object response, want decode error")
	}
}

func TestRevokeCLISession(t *testing.T) {
	var seenDelete bool
	server := healthServer("prj_x", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && r.URL.Path == "/api/v1/auth/cli-sessions/current" {
			seenDelete = true
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"data":{"revoked":true,"matched":true},"meta":{}}`)
			return
		}
	})
	t.Cleanup(server.Close)
	c, err := New(context.Background(), Config{ProjectID: "prj_x", BaseURL: server.URL, Token: "tok"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	matched, err := c.RevokeCLISession(context.Background())
	if err != nil {
		t.Fatalf("RevokeCLISession: %v", err)
	}
	if !matched {
		t.Fatalf("matched = false, want true")
	}
	if !seenDelete {
		t.Fatalf("daemon never saw DELETE")
	}
}

func TestDeleteLegacySessionFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// Write a legacy session file at the conventional path.
	home, _ := os.UserHomeDir()
	legacy := filepath.Join(home, ".symphony", "cli-session.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(legacy, []byte(`{"project_id":"prj_x","token":"legacy"}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := DeleteLegacySessionFile(); err != nil {
		t.Fatalf("DeleteLegacySessionFile: %v", err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy file still present: %v", err)
	}
	// Idempotent: second call must not fail.
	if err := DeleteLegacySessionFile(); err != nil {
		t.Fatalf("DeleteLegacySessionFile (idempotent): %v", err)
	}
}

// TestReadSessionFileRejectsMismatchedRepoRoot pins the
// HIGH #1 fix from adversarial round 4: ReadSessionFile
// must reject a session file whose persisted repo_root does
// not match the caller's normalised repo_root. A copied
// project DB that reuses a foreign project_id inherits that
// project's id from the new location's metadata, but the
// session file's repo_root records the actual checkout the
// bearer was minted for. A mismatch proves the bearer
// belongs to a different repo and must not be honoured.
func TestReadSessionFileRejectsMismatchedRepoRoot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	projectID := "prj_x"
	foreignRepo := t.TempDir()
	currentRepo := t.TempDir()
	sessionPath := app.CLISessionPath(projectID)
	if err := os.MkdirAll(filepath.Dir(sessionPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body, _ := json.Marshal(map[string]any{
		"project_id": projectID,
		"repo_root":  foreignRepo,
		"token":      "tok-foreign",
	})
	if err := os.WriteFile(sessionPath, body, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := ReadSessionFile(sessionPath, projectID, currentRepo)
	if err == nil {
		t.Fatal("ReadSessionFile accepted session file with mismatched repo_root")
	}
	if got := core.AsAPIError(err).Code; got != core.ErrUnauthorized {
		t.Fatalf("error code = %s, want %s", got, core.ErrUnauthorized)
	}
}

// TestReadSessionFileAcceptsMatchingRepoRoot pins the
// happy path: when the session file's persisted repo_root
// matches the caller's normalised repo_root, the session
// is accepted. Also covers the legacy no-repo_root fallback
// (empty persisted RepoRoot) so older session files that
// predate the repo_root field keep working.
func TestReadSessionFileAcceptsMatchingRepoRoot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	projectID := "prj_match"
	repoRoot := t.TempDir()

	t.Run("matching repo_root", func(t *testing.T) {
		sessionPath := app.CLISessionPath(projectID)
		if err := os.MkdirAll(filepath.Dir(sessionPath), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		body, _ := json.Marshal(map[string]any{
			"project_id": projectID,
			"repo_root":  repoRoot,
			"token":      "tok-match",
		})
		if err := os.WriteFile(sessionPath, body, 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		sf, err := ReadSessionFile(sessionPath, projectID, repoRoot)
		if err != nil {
			t.Fatalf("ReadSessionFile rejected matching repo_root: %v", err)
		}
		if sf.Token != "tok-match" {
			t.Fatalf("token = %q, want tok-match", sf.Token)
		}
	})

	t.Run("empty persisted repo_root is accepted", func(t *testing.T) {
		sessionPath := app.CLISessionPath(projectID)
		body, _ := json.Marshal(map[string]any{
			"project_id": projectID,
			"token":      "tok-no-root",
		})
		if err := os.WriteFile(sessionPath, body, 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, err := ReadSessionFile(sessionPath, projectID, repoRoot); err != nil {
			t.Fatalf("ReadSessionFile rejected session with empty persisted repo_root: %v", err)
		}
	})
}
