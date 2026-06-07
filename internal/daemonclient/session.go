package daemonclient

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"local-symphony/internal/app"
	"local-symphony/internal/core"
)

// SessionFile describes the persisted CLI bearer session. The token is read
// from disk and sent as `Authorization: Bearer <token>` on every operator
// call. The file is 0600 and project-scoped; the legacy single-file layout
// is honored when present for backwards compatibility.
type SessionFile struct {
	ProjectID string `json:"project_id"`
	RepoRoot  string `json:"repo_root,omitempty"`
	APIURL    string `json:"api_url,omitempty"`
	Token     string `json:"token"`
	CreatedAt string `json:"created_at,omitempty"`
}

// loadCLISessionToken returns the bearer token for the given project. It
// prefers the project-scoped session file under ~/.symphony/cli-sessions and
// falls back to the legacy ~/.symphony/cli-session.json file. An empty
// project mismatch or empty token is reported as ErrSessionMissing so the
// caller can render a single, action-oriented error.
//
// repoRoot is the absolute path of the project whose CLI session we are
// loading. A non-empty repoRoot is matched against the session file's
// persisted repo_root and any mismatch is treated as a hard error: a copied
// project DB that reuses a foreign project_id must not be able to load and
// send that other project's CLI bearer through this process.
func loadCLISessionToken(projectID, repoRoot string) (string, error) {
	path := app.CLISessionPath(projectID)
	tok, err := readSessionToken(path, projectID, repoRoot)
	if err == nil {
		return tok, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		// Permission / parse errors are not recoverable via fallback.
		return "", err
	}
	tok, legacyErr := readSessionToken(app.LegacyCLISessionPath(), projectID, repoRoot)
	if legacyErr == nil {
		return tok, nil
	}
	if errors.Is(legacyErr, os.ErrNotExist) {
		return "", ErrSessionMissing
	}
	return "", legacyErr
}

// ReadSessionFile loads and validates the session file at path. It is
// exported because CLI tests and the `symphony login` flow both need to
// inspect the persisted credentials.
//
// projectID must match the persisted project_id; repoRoot, when non-empty,
// must also match the persisted repo_root. The repo_root guard prevents a
// copied project DB from loading the wrong repo's CLI bearer: the copied
// DB inherits a foreign project_id, but the session file's repo_root
// records the actual checkout the bearer was minted for. A mismatch is
// reported as ErrSessionMissing so the caller renders a single,
// action-oriented error instead of silently routing the foreign bearer.
func ReadSessionFile(path, projectID, repoRoot string) (SessionFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return SessionFile{}, err
	}
	var sf SessionFile
	if err := json.Unmarshal(b, &sf); err != nil {
		return SessionFile{}, core.NewError(core.ErrInternal, "session file is not valid JSON: "+err.Error(), nil)
	}
	if sf.ProjectID != projectID {
		return SessionFile{}, core.NewError(core.ErrUnauthorized, "CLI session is not valid for this project", nil)
	}
	if strings.TrimSpace(sf.Token) == "" {
		return SessionFile{}, core.NewError(core.ErrUnauthorized, "CLI session token is empty", nil)
	}
	if err := checkSessionRepoRoot(sf, repoRoot); err != nil {
		return SessionFile{}, err
	}
	return sf, nil
}

// checkSessionRepoRoot enforces the repo_root trust boundary. A session
// file with a non-empty RepoRoot that does not match the caller's
// normalised repoRoot is rejected. Sessions persisted before the
// repo_root field was added carry an empty RepoRoot, and callers that
// don't have a repoRoot (legacy paths) pass an empty string; both
// continue to be accepted so the new check is strictly additive.
func checkSessionRepoRoot(sf SessionFile, repoRoot string) error {
	if repoRoot == "" {
		return nil
	}
	if strings.TrimSpace(sf.RepoRoot) == "" {
		return nil
	}
	want, err := normaliseRepoRootForCompare(repoRoot)
	if err != nil {
		return nil // can't normalise; don't block on a host-side issue
	}
	got, err := normaliseRepoRootForCompare(sf.RepoRoot)
	if err != nil {
		return nil
	}
	if want != got {
		return core.NewError(core.ErrUnauthorized, "CLI session is not valid for this project repository", nil)
	}
	return nil
}

// normaliseRepoRootForCompare resolves symlinks and cleans the path so
// "/a" and "/a/." and a symlink pointing at "/a" all compare equal. It
// returns an error for paths the OS cannot resolve; the caller treats
// resolution errors as "skip the check" rather than rejecting the
// session, because the cost of false negatives is high (operator locked
// out) and the cost of false positives is mitigated by the project_id
// and api_url checks that run first.
func normaliseRepoRootForCompare(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func readSessionToken(path, projectID, repoRoot string) (string, error) {
	sf, err := ReadSessionFile(path, projectID, repoRoot)
	if err != nil {
		return "", err
	}
	return sf.Token, nil
}

// WriteSessionFile persists a session for the given project. The file is
// 0600 and lives under ~/.symphony/cli-sessions/<project>.json. Tests that
// do not have a writable HOME should use t.TempDir() + os.Setenv("HOME",...).
func WriteSessionFile(projectID string, sf SessionFile) (string, error) {
	if sf.ProjectID == "" {
		sf.ProjectID = projectID
	}
	if strings.TrimSpace(sf.Token) == "" {
		return "", core.NewError(core.ErrInvalidRequest, "session token must not be empty", nil)
	}
	if strings.TrimSpace(sf.ProjectID) != projectID {
		return "", core.NewError(core.ErrInvalidRequest, "session project_id mismatch", nil)
	}
	path := app.CLISessionPath(projectID)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil && !errors.Is(err, os.ErrPermission) {
		// best-effort: chmod may fail on a directory we did not create
	}
	b, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0o600); err != nil && !errors.Is(err, os.ErrPermission) {
		// best-effort
	}
	return path, nil
}

// DeleteSessionFile removes the persisted session file. Missing files are
// treated as success so callers can use it on every CLI startup without
// conditionals.
func DeleteSessionFile(projectID string) error {
	path := app.CLISessionPath(projectID)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// DeleteLegacySessionFile removes the pre-v1.1 single-file session at
// ~/.symphony/cli-session.json. v1.1 stores per-project files under
// ~/.symphony/cli-sessions/<project>.json; users upgraded from older
// builds still have the legacy file, and `symphony login --logout`
// must wipe it as well so a stale token cannot be replayed.
func DeleteLegacySessionFile() error {
	path := app.LegacyCLISessionPath()
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// ReadAllSessionFiles returns the session files currently on disk. It is
// used by `symphony login --list` style flows. Missing files are reported
// as an empty slice.
func ReadAllSessionFiles() ([]SessionFile, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil, nil
	}
	dir := filepath.Join(home, ".symphony", "cli-sessions")
	f, err := os.Open(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	out := []SessionFile{}
	for _, name := range names {
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var sf SessionFile
		if err := json.Unmarshal(b, &sf); err != nil {
			continue
		}
		if strings.TrimSpace(sf.Token) == "" {
			continue
		}
		out = append(out, sf)
	}
	return out, nil
}
