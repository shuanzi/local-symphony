// Package daemonclient implements the local operator CLI's REST client for the
// symphony daemon. It discovers the daemon endpoint, persists a CLI bearer
// session, and unwraps the {data, meta} success envelope / {error} error
// envelope that the daemon returns.
package daemonclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"local-symphony/internal/app"
	"local-symphony/internal/core"
	"local-symphony/internal/db"
	"local-symphony/internal/security"
)

// EnvOverride is the environment variable that lets operators point the CLI
// at a non-default daemon URL. It takes precedence over the on-disk daemon
// config and the project runtime descriptor.
const EnvOverride = "SYMPHONY_DAEMON_URL"

// DefaultTimeout caps every HTTP request issued by the client. The CLI uses
// short, bounded calls so a hung daemon never blocks the operator terminal.
var DefaultTimeout = 30 * time.Second

// Client speaks to a local-symphony daemon. It is safe to share across
// goroutines; the underlying http.Client is immutable after construction.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
	Token      string
	ProjectID  string
}

// Config captures the inputs used to construct a Client. Discovery resolves a
// concrete BaseURL by precedence, and the token (when present) is sent as a
// Bearer credential on every request.
type Config struct {
	ProjectRoot string
	ProjectID   string
	BaseURL     string
	Token       string
	HTTPClient  *http.Client
	// AllowRemoteDaemonURL opts the client out of the loopback
	// host check that v1 enforces. Production CLI invocations
	// should never set this; it exists for tests and
	// explicit-development-against-remote fixtures. The
	// default (false) rejects any daemon URL whose host is
	// not in 127.0.0.0/8, ::1, or `localhost`.
	AllowRemoteDaemonURL bool
}

// Discovery surfaces the resolved daemon URL and the source that produced it.
// Sources are exposed to the operator so error messages can point them at the
// right knob to flip.
type Discovery struct {
	BaseURL string
	Source  string
}

// ErrDaemonUnavailable is returned when no running daemon can be located. The
// caller is expected to render a stderr-only message that points the operator
// at `symphony serve` / `symphony open --help` and exit with code 7.
var ErrDaemonUnavailable = errors.New("daemon_unavailable: no local symphony daemon is reachable")

// ErrSessionMissing is returned when a CLI bearer session cannot be located
// for the current project. The caller should treat this as daemon_unavailable
// from the operator's perspective: the user must run `symphony serve` first.
var ErrSessionMissing = errors.New("unauthorized: CLI bearer session missing for this project")

// NetworkError wraps a low-level transport error (DNS, refused, reset)
// that the CLI dispatcher should treat as an invitation to fall back to
// the local store. It is exported so callers can errors.As on it.
type NetworkError struct{ Err error }

func (e *NetworkError) Error() string { return "network: " + e.Err.Error() }
func (e *NetworkError) Unwrap() error { return e.Err }

// Discover walks the discovery precedence chain and returns the first
// usable daemon URL. It never starts a daemon; an operator must
// launch it out-of-band.
//
// baseURLHint, when non-empty, is prepended to the candidate
// list. Callers that already hold a daemon URL — e.g. the
// api_url persisted in a session file — pass it here so the
// reachability / project_id check runs against that exact
// endpoint before any fallback. The hint is the FIRST candidate
// tried so a saved api_url pointing at the wrong project's
// daemon is rejected (or any subsequent candidate wins) instead
// of being silently honored. A stale or cross-project api_url
// MUST NOT carry a CLI bearer to a foreign daemon.
//
// Project trust boundary: every candidate URL is reachable-tested,
// and the daemon's /api/v1/health response must report
// `data.project_id` equal to the requested projectID. A stale
// runtime descriptor, session api_url, or user daemon config
// pointing at the wrong project's daemon is rejected so a CLI
// bearer for project A is never sent to a daemon hosting
// project B. Discovery returns ErrDaemonUnavailable when no
// candidate matches.
func Discover(ctx context.Context, projectID, projectRoot string, allowRemote bool, baseURLHint string) (Discovery, error) {
	if projectID == "" {
		return Discovery{}, fmt.Errorf("daemonclient: projectID is required")
	}
	candidates := discoverCandidates(projectID, projectRoot, baseURLHint)
	for _, c := range candidates {
		base, err := normalizeBaseURL(c.URL, allowRemote)
		if err != nil {
			continue
		}
		gotProjectID, ok := reachable(ctx, base, projectID)
		if !ok {
			continue
		}
		if gotProjectID != "" && gotProjectID != projectID {
			// Mismatched project daemon: skip and keep looking.
			continue
		}
		return Discovery{BaseURL: base, Source: c.Source}, nil
	}
	return Discovery{}, fmt.Errorf("%w: no candidate daemon reported project_id=%s", ErrDaemonUnavailable, projectID)
}

// discoverCandidate is a single (URL, source) pair to probe.
type discoverCandidate struct {
	URL    string
	Source string
}

// discoverCandidates enumerates every daemon URL the discovery
// process knows about, in precedence order. The slice is
// deterministic so tests can assert which source won the race.
//
// baseURLHint, when non-empty, REPLACES the candidate list with
// just the hint. This is the only way a caller can short-circuit
// discovery to a specific URL while still routing through the
// project_id trust check. Saved session files use it so a stale
// or cross-project api_url is rejected before any CLI bearer is
// sent to that endpoint; the bearer for project A is never
// delivered to a daemon hosting project B by falling back to
// some other configured URL.
func discoverCandidates(projectID, projectRoot, baseURLHint string) []discoverCandidate {
	if hint := strings.TrimSpace(baseURLHint); hint != "" {
		return []discoverCandidate{{URL: hint, Source: "hint:base_url"}}
	}
	out := []discoverCandidate{}
	if raw := strings.TrimSpace(os.Getenv(EnvOverride)); raw != "" {
		out = append(out, discoverCandidate{URL: raw, Source: "env:" + EnvOverride})
	}
	if path, ok := userDaemonConfigPath(); ok {
		if raw, err := readDaemonConfigURL(path); err == nil && raw != "" {
			out = append(out, discoverCandidate{URL: raw, Source: "config:" + path})
		}
	}
	if projectRoot != "" {
		if descPath := db.RuntimeDescriptorPath(projectID); descPath != "" {
			if base, err := readRuntimeDescriptorAPIURL(descPath); err == nil && base != "" {
				out = append(out, discoverCandidate{URL: base, Source: "runtime:" + descPath})
			}
		}
	}
	if sessionBase, ok := sessionFileAPIURL(projectID); ok && sessionBase != "" {
		out = append(out, discoverCandidate{URL: sessionBase, Source: "session:" + app.CLISessionPath(projectID)})
	}
	return out
}

// New constructs a Client. When cfg.Token is empty, New will attempt to load
// a project-scoped bearer session from disk before returning. The caller
// should treat an ErrSessionMissing return as "user must run `symphony serve`
// first"; the CLI is not allowed to mint its own CLI session.
func New(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.ProjectID == "" {
		return nil, fmt.Errorf("daemonclient: projectID is required")
	}
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		disc, err := Discover(ctx, cfg.ProjectID, cfg.ProjectRoot, cfg.AllowRemoteDaemonURL, "")
		if err != nil {
			return nil, err
		}
		baseURL = disc.BaseURL
	}
	normalized, err := normalizeBaseURL(baseURL, cfg.AllowRemoteDaemonURL)
	if err != nil {
		return nil, err
	}
	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		token, err = loadCLISessionToken(cfg.ProjectID)
		if err != nil {
			return nil, err
		}
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: DefaultTimeout}
	}
	return &Client{BaseURL: normalized, HTTPClient: httpClient, Token: token, ProjectID: cfg.ProjectID}, nil
}

// ErrorPayload mirrors the daemon's {error:{code,message,details,request_id}}
// envelope. The client surfaces this verbatim so CLI callers can present the
// upstream error without losing details.
type ErrorPayload struct {
	Code    APIErrorCode  `json:"code"`
	Message string        `json:"message"`
	Details map[string]any `json:"details"`
}

// Envelope is the daemon's success wrapper. Only Data is required; Meta is
// preserved verbatim in the typed response and dropped from the unwrapped
// map[string]any surface CLI callers print.
type Envelope struct {
	Data json.RawMessage `json:"data"`
	Meta map[string]any  `json:"meta"`
}

// APIErrorCode is a type alias for core.APIErrorCode so callers can switch on
// the wire-format code without importing core directly.
type APIErrorCode = core.APIErrorCode

// Response is the typed result of Do. StatusCode is the upstream HTTP status,
// Data is the unwrapped payload (or nil for error responses), and Error is
// non-nil for HTTP errors.
type Response struct {
	StatusCode int
	RequestID  string
	Data       json.RawMessage
	Raw        []byte
	Error      *core.APIError
}

// Do executes the HTTP request and unwraps the envelope. The caller passes
// body=nil for GET/DELETE.
func (c *Client) Do(ctx context.Context, method, path string, body any) (Response, error) {
	if c == nil {
		return Response{}, fmt.Errorf("daemonclient: nil client")
	}
	var bodyReader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return Response{}, fmt.Errorf("daemonclient: marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(buf)
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	target, err := buildTargetURL(c.BaseURL, path)
	if err != nil {
		return Response{}, fmt.Errorf("daemonclient: build url: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bodyReader)
	if err != nil {
		return Response{}, fmt.Errorf("daemonclient: new request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return Response{}, &NetworkError{Err: fmt.Errorf("%w: %v", ErrDaemonUnavailable, err)}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return Response{}, fmt.Errorf("daemonclient: read body: %w", err)
	}
	out := Response{StatusCode: resp.StatusCode, Raw: raw}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var env Envelope
		if len(bytes.TrimSpace(raw)) == 0 {
			return out, nil
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			return Response{}, fmt.Errorf("daemonclient: decode envelope: %w (body=%s)", err, summarize(raw))
		}
		out.Data = env.Data
		return out, nil
	}
	ae, err := decodeError(raw, resp.StatusCode)
	if err != nil {
		return Response{}, err
	}
	out.Error = ae
	return out, ae
}

// Get issues a GET and unwraps the data payload into a typed value. When
// noData is true, an empty body is treated as success.
func (c *Client) Get(ctx context.Context, path string, out any) error {
	resp, err := c.Do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if len(resp.Data) == 0 {
		return nil
	}
	return json.Unmarshal(resp.Data, out)
}

// Post issues a POST with a JSON body and unwraps the data payload.
func (c *Client) Post(ctx context.Context, path string, body, out any) error {
	resp, err := c.Do(ctx, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	if out == nil || len(resp.Data) == 0 {
		return nil
	}
	return json.Unmarshal(resp.Data, out)
}

// Patch issues a PATCH with a JSON body and unwraps the data payload.
func (c *Client) Patch(ctx context.Context, path string, body, out any) error {
	resp, err := c.Do(ctx, http.MethodPatch, path, body)
	if err != nil {
		return err
	}
	if out == nil || len(resp.Data) == 0 {
		return nil
	}
	return json.Unmarshal(resp.Data, out)
}

// Delete issues a DELETE and unwraps the data payload.
func (c *Client) Delete(ctx context.Context, path string, out any) error {
	resp, err := c.Do(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	if out == nil || len(resp.Data) == 0 {
		return nil
	}
	return json.Unmarshal(resp.Data, out)
}

// RevokeCLISession tells the daemon to mark the bearer token
// carried by this client as revoked. It is the daemon-side
// half of `symphony login --logout`; the local CLI session
// file is deleted separately by DeleteSessionFile. The call
// is idempotent on the server side: a no-bearer or
// already-revoked token still returns 200.
//
// Returns the daemon's reported `matched` field so the
// caller can surface degraded logout (daemon reachable but
// the token wasn't recognised) to the operator.
func (c *Client) RevokeCLISession(ctx context.Context) (matched bool, err error) {
	out := struct {
		Revoked bool `json:"revoked"`
		Matched bool `json:"matched"`
	}{}
	if err := c.Delete(ctx, "/api/v1/auth/cli-sessions/current", &out); err != nil {
		return false, err
	}
	return out.Matched, nil
}

// Unwrap returns the raw data as a generic map. The CLI prints this directly
// so stable object structures are preserved.
func (c *Client) UnwrapMap(ctx context.Context, method, path string, body any) (map[string]any, error) {
	resp, err := c.Do(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	if len(resp.Data) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		return nil, fmt.Errorf("daemonclient: decode data as object: %w (body=%s)", err, summarize(resp.Raw))
	}
	return out, nil
}

// UnwrapArray returns the raw data as a generic slice. The CLI uses this
// for endpoints whose `data` payload is a JSON array (e.g. /api/v1/runs,
// /api/v1/approvals) so the envelope unwrap stays consistent with
// UnwrapMap. Decoding an array with UnwrapMap would fail with
// "json: cannot unmarshal object into Go value of type map[string]any"
// — that's the regression this helper exists to prevent.
func (c *Client) UnwrapArray(ctx context.Context, method, path string, body any) ([]any, error) {
	resp, err := c.Do(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	if len(resp.Data) == 0 {
		return []any{}, nil
	}
	var out []any
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		return nil, fmt.Errorf("daemonclient: decode data as array: %w (body=%s)", err, summarize(resp.Raw))
	}
	return out, nil
}

// buildTargetURL composes a final request URL by joining a base URL with
// a path that may include a query string. url.JoinPath would percent-
// encode the `?` separator, breaking GET filters like
// `/api/v1/issues?limit=1`. The fix is to split at the first `?`, join
// the path with url.JoinPath, and assign the query to RawQuery on the
// resolved URL.
func buildTargetURL(base, path string) (string, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	pathPart, rawQuery := splitPathQuery(path)
	joined, err := url.JoinPath(base, pathPart)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(joined)
	if err != nil {
		return "", err
	}
	if rawQuery != "" {
		parsed.RawQuery = rawQuery
	}
	return parsed.String(), nil
}

// splitPathQuery returns the path and the raw query string of a URL
// path. The input is expected to look like "/api/v1/issues?limit=1";
// the second return is empty when no query is present.
func splitPathQuery(p string) (string, string) {
	if i := strings.Index(p, "?"); i >= 0 {
		return p[:i], p[i+1:]
	}
	return p, ""
}

func decodeError(raw []byte, status int) (*core.APIError, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return core.NewError(core.ErrInternal, fmt.Sprintf("daemon returned status %d with empty body", status), map[string]any{"status": status}), nil
	}
	var env struct {
		Error ErrorPayload `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return core.NewError(core.ErrInternal, fmt.Sprintf("daemon returned status %d with non-JSON body", status), map[string]any{"status": status, "body": summarize(raw)}), nil
	}
	code := env.Error.Code
	if code == "" {
		code = core.APIErrorCode(httpCodeToCode(status))
	}
	details := env.Error.Details
	if details == nil {
		details = map[string]any{}
	}
	return core.NewError(code, env.Error.Message, details), nil
}

// httpCodeToCode maps a small set of HTTP statuses to a core error code when
// the daemon forgot to include a {code,...} envelope. It deliberately errs on
// the side of internal_error so callers always get a non-empty code.
func httpCodeToCode(status int) string {
	switch status {
	case http.StatusBadRequest:
		return string(core.ErrInvalidRequest)
	case http.StatusUnauthorized:
		return string(core.ErrUnauthorized)
	case http.StatusForbidden:
		return string(core.ErrForbidden)
	case http.StatusNotFound:
		return string(core.ErrNotFound)
	default:
		return string(core.ErrInternal)
	}
}

// normalizeBaseURL parses a daemon URL, validates its scheme
// (http or https), and enforces the v1 loopback baseline:
// only hosts in 127.0.0.0/8, ::1, or `localhost` are
// accepted by default. This guard sits in front of any
// bearer request so a poisoned SYMPHONY_DAEMON_URL,
// daemon.json, runtime descriptor, or session api_url
// cannot route the CLI bearer to a remote endpoint that
// mimics the project_id. Pass `allowRemote=true` to opt
// out (test-only).
func normalizeBaseURL(raw string, allowRemote bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("daemonclient: empty daemon URL")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("daemonclient: parse daemon URL %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("daemonclient: daemon URL %q must use http or https", raw)
	}
	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("daemonclient: daemon URL %q has no host", raw)
	}
	if !allowRemote && !security.IsLoopbackHost(host) {
		return "", fmt.Errorf("daemonclient: non-loopback daemon URL rejected: %s (set allowRemote=true to override)", host)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String(), nil
}

// reachable pings the daemon's public /health endpoint and, on a
// 200 response, decodes the {data, meta} envelope to read the
// daemon's `project_id`. The discovery caller MUST verify the
// returned project_id matches the operating project; this
// function returns ("", false) when the response is missing the
// field so a daemon that fails to advertise its project_id is
// treated as unreachable. Auth is verified on the actual command
// calls.
func reachable(ctx context.Context, base, expectedProjectID string) (string, bool) {
	httpClient := &http.Client{Timeout: 2 * time.Second}
	target, err := url.JoinPath(base, "/api/v1/health")
	if err != nil {
		return "", false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", false
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	gotProjectID := parseHealthProjectID(body)
	return gotProjectID, gotProjectID != ""
}

// parseHealthProjectID reads the project_id out of a /api/v1/health
// response body. The daemon returns
//
//	{"data":{"ok":true,"project_id":"prj_..."},"meta":{}}
//
// so we tolerate either envelope-presence (the data key nested
// under `data`) or flat-shape. An empty string is returned for
// any body we cannot parse.
func parseHealthProjectID(body []byte) string {
	if len(bytes.TrimSpace(body)) == 0 {
		return ""
	}
	// Fast-path: envelope shape.
	var env struct {
		Data struct {
			ProjectID string `json:"project_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err == nil && env.Data.ProjectID != "" {
		return env.Data.ProjectID
	}
	// Fallback: flat shape (older daemons or test fixtures).
	var flat struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal(body, &flat); err == nil {
		return flat.ProjectID
	}
	return ""
}

// userDaemonConfigPath returns the path of the user-level daemon URL config.
// We return ok=false when HOME is unset so callers fall through to the
// runtime descriptor.
func userDaemonConfigPath() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	return filepath.Join(home, ".symphony", "daemon.json"), true
}

func readDaemonConfigURL(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return "", err
	}
	if raw, ok := m["api_url"].(string); ok {
		return raw, nil
	}
	if raw, ok := m["daemon_url"].(string); ok {
		return raw, nil
	}
	return "", nil
}

func readRuntimeDescriptorAPIURL(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return "", err
	}
	if raw, ok := m["api_url"].(string); ok {
		return raw, nil
	}
	return "", nil
}

// sessionFileAPIURL peeks at the project-scoped session file for a stored
// api_url. It is only consulted after the explicit configuration sources
// fail; the session file is not authoritative and may lag behind a daemon
// restart on a different port.
func sessionFileAPIURL(projectID string) (string, bool) {
	if projectID == "" {
		return "", false
	}
	path := app.CLISessionPath(projectID)
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var sf SessionFile
	if err := json.Unmarshal(b, &sf); err != nil {
		return "", false
	}
	if sf.ProjectID != projectID || strings.TrimSpace(sf.APIURL) == "" {
		return "", false
	}
	return sf.APIURL, true
}

// summarize returns a short, safe prefix of a raw body for inclusion in error
// messages. It deliberately does not return the whole body; raw prompts, raw
// Codex logs, and raw secret artifacts can flow through here.
func summarize(b []byte) string {
	const limit = 240
	if len(b) <= limit {
		return string(b)
	}
	return string(b[:limit]) + "...(truncated)"
}
