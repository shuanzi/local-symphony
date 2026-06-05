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

// Discover walks the discovery precedence chain and returns the first usable
// daemon URL. It never starts a daemon; an operator must launch it
// out-of-band.
func Discover(ctx context.Context, projectID, projectRoot string) (Discovery, error) {
	if projectID == "" {
		return Discovery{}, fmt.Errorf("daemonclient: projectID is required")
	}
	if raw := strings.TrimSpace(os.Getenv(EnvOverride)); raw != "" {
		base, err := normalizeBaseURL(raw)
		if err != nil {
			return Discovery{}, err
		}
		if reachable(ctx, base) {
			return Discovery{BaseURL: base, Source: "env:" + EnvOverride}, nil
		}
		return Discovery{}, fmt.Errorf("%w: %s=%s is not reachable", ErrDaemonUnavailable, EnvOverride, base)
	}
	if path, ok := userDaemonConfigPath(); ok {
		if raw, err := readDaemonConfigURL(path); err == nil && raw != "" {
			base, err := normalizeBaseURL(raw)
			if err != nil {
				return Discovery{}, err
			}
			if reachable(ctx, base) {
				return Discovery{BaseURL: base, Source: "config:" + path}, nil
			}
		}
	}
	if projectRoot != "" {
		if descPath := db.RuntimeDescriptorPath(projectID); descPath != "" {
			if base, err := readRuntimeDescriptorAPIURL(descPath); err == nil && base != "" {
				if reachable(ctx, base) {
					return Discovery{BaseURL: base, Source: "runtime:" + descPath}, nil
				}
			}
		}
	}
	// Last resort: the persisted session file remembers the daemon URL it
	// was minted against. This is the only way an offline-stale operator
	// can still get a useful "daemon is not reachable" error instead of a
	// session parse error.
	if sessionBase, ok := sessionFileAPIURL(projectID); ok {
		if reachable(ctx, sessionBase) {
			return Discovery{BaseURL: sessionBase, Source: "session:" + app.CLISessionPath(projectID)}, nil
		}
	}
	return Discovery{}, ErrDaemonUnavailable
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
		disc, err := Discover(ctx, cfg.ProjectID, cfg.ProjectRoot)
		if err != nil {
			return nil, err
		}
		baseURL = disc.BaseURL
	}
	normalized, err := normalizeBaseURL(baseURL)
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
	target, err := url.JoinPath(c.BaseURL, path)
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

// normalizeBaseURL strips trailing slashes and ensures the URL has a scheme.
func normalizeBaseURL(raw string) (string, error) {
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
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String(), nil
}

// reachable pings the daemon's public /health endpoint. A 200 response is
// enough; auth is verified on the actual command calls.
func reachable(ctx context.Context, base string) bool {
	httpClient := &http.Client{Timeout: 2 * time.Second}
	target, err := url.JoinPath(base, "/api/v1/health")
	if err != nil {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return false
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
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
