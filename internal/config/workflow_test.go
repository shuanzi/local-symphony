package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLoadPathRejectsIntegerWithTrailingCharacters(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "WORKFLOW.md")
	content := `---
agent:
  max_concurrent_agents: 3oops
---
Do the work.
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	w, err := LoadPath(path, dir)
	if err != nil {
		t.Fatalf("LoadPath returned error: %v", err)
	}
	if w.Validation.Valid {
		t.Fatalf("Validation.Valid = true, want false")
	}
	if !slices.Contains(w.Validation.Errors, "max_concurrent_agents must be integer") {
		t.Fatalf("Validation.Errors = %v, want max_concurrent_agents integer error", w.Validation.Errors)
	}
}

func TestLoadPathRejectsTrailingCharactersForDeclaredWorkflowIntegers(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		wantErr string
	}{
		{
			name: "polling interval",
			config: `polling:
  interval_ms: 30000ms`,
			wantErr: "interval_ms must be integer",
		},
		{
			name: "codex startup timeout",
			config: `codex:
  startup_timeout_ms: 60000ms`,
			wantErr: "startup_timeout_ms must be integer",
		},
		{
			name: "codex read timeout",
			config: `codex:
  read_timeout_ms: 5000ms`,
			wantErr: "read_timeout_ms must be integer",
		},
		{
			name: "hooks max output",
			config: `hooks:
  max_output_bytes: 65536bytes`,
			wantErr: "max_output_bytes must be integer",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "WORKFLOW.md")
			content := "---\n" + tt.config + "\n---\nDo the work.\n"
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatalf("write workflow: %v", err)
			}

			w, err := LoadPath(path, dir)
			if err != nil {
				t.Fatalf("LoadPath returned error: %v", err)
			}
			if w.Validation.Valid {
				t.Fatalf("Validation.Valid = true, want false")
			}
			if !slices.Contains(w.Validation.Errors, tt.wantErr) {
				t.Fatalf("Validation.Errors = %v, want %q", w.Validation.Errors, tt.wantErr)
			}
		})
	}
}

func TestLoadPathRejectsNonPositiveArtifactMaxBytes(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "zero", value: "0"},
		{name: "negative", value: "-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "WORKFLOW.md")
			content := `---
tools:
  artifact_max_bytes: ` + tt.value + `
---
Do the work.
`
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatalf("write workflow: %v", err)
			}

			w, err := LoadPath(path, dir)
			if err != nil {
				t.Fatalf("LoadPath returned error: %v", err)
			}
			if w.Validation.Valid {
				t.Fatalf("Validation.Valid = true, want false")
			}
			if !slices.Contains(w.Validation.Errors, "tools.artifact_max_bytes must be greater than 0") {
				t.Fatalf("Validation.Errors = %v, want artifact_max_bytes positive error", w.Validation.Errors)
			}
		})
	}
}

func TestLoadPathRejectsNegativeHookMaxOutputBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "WORKFLOW.md")
	content := `---
hooks:
  max_output_bytes: -1
---
Do the work.
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	w, err := LoadPath(path, dir)
	if err != nil {
		t.Fatalf("LoadPath returned error: %v", err)
	}
	if w.Validation.Valid {
		t.Fatalf("Validation.Valid = true, want false")
	}
	if !slices.Contains(w.Validation.Errors, "hooks.max_output_bytes must be greater than or equal to 0") {
		t.Fatalf("Validation.Errors = %v, want hooks max_output_bytes non-negative error", w.Validation.Errors)
	}
}

func TestLoadPathHonorsAgentMaxTurnsAliasWhenMaxTurnsPerRunUnset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "WORKFLOW.md")
	content := `---
agent:
  max_turns: 1
  max_handoff_continuations: 0
---
Do the work.
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	w, err := LoadPath(path, dir)
	if err != nil {
		t.Fatalf("LoadPath returned error: %v", err)
	}
	if !w.Validation.Valid {
		t.Fatalf("Validation.Valid = false, errors = %v", w.Validation.Errors)
	}
	if got := w.Config.Agent.MaxTurnsPerRun; got != 1 {
		t.Fatalf("agent.max_turns_per_run = %d, want 1", got)
	}
}

func TestLoadPathPrefersAgentMaxTurnsPerRunOverAlias(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "WORKFLOW.md")
	content := `---
agent:
  max_turns_per_run: 1
  max_turns: 2
  max_handoff_continuations: 0
---
Do the work.
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	w, err := LoadPath(path, dir)
	if err != nil {
		t.Fatalf("LoadPath returned error: %v", err)
	}
	if !w.Validation.Valid {
		t.Fatalf("Validation.Valid = false, errors = %v", w.Validation.Errors)
	}
	if got := w.Config.Agent.MaxTurnsPerRun; got != 1 {
		t.Fatalf("agent.max_turns_per_run = %d, want 1", got)
	}
}

func TestLoadPathInvalidAgentMaxTurnsPerRunIsNotMaskedByAlias(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "WORKFLOW.md")
	content := `---
agent:
  max_turns_per_run: bad
  max_turns: 1
  max_handoff_continuations: 0
---
Do the work.
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	w, err := LoadPath(path, dir)
	if err != nil {
		t.Fatalf("LoadPath returned error: %v", err)
	}
	if w.Validation.Valid {
		t.Fatalf("Validation.Valid = true, want false")
	}
	if !slices.Contains(w.Validation.Errors, "max_turns_per_run must be integer") {
		t.Fatalf("Validation.Errors = %v, want max_turns_per_run integer error", w.Validation.Errors)
	}
	if got := w.Config.Agent.MaxTurnsPerRun; got != Defaults(dir).Agent.MaxTurnsPerRun {
		t.Fatalf("agent.max_turns_per_run = %d, want default after invalid explicit field", got)
	}
}

func TestLoadPathExpandsOnlyCurrentUserHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("user home is unavailable")
	}
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "bare tilde", raw: `'~'`, want: home},
		{name: "tilde slash", raw: `~/ws`, want: filepath.Join(home, "ws")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "WORKFLOW.md")
			content := `---
workspace:
  root: ` + tt.raw + `
---
Do the work.
`
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatalf("write workflow: %v", err)
			}

			w, err := LoadPath(path, dir)
			if err != nil {
				t.Fatalf("LoadPath returned error: %v", err)
			}
			if !w.Validation.Valid {
				t.Fatalf("Validation.Valid = false, errors = %v", w.Validation.Errors)
			}
			if got := filepath.Clean(w.Config.Workspace.Root); got != filepath.Clean(tt.want) {
				t.Fatalf("workspace.root = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLoadPathRejectsUnsupportedTildeUserExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("user home is unavailable")
	}
	tests := []string{"~user", "~foo", "~../outside"}
	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "WORKFLOW.md")
			content := `---
workspace:
  root: ` + raw + `
---
Do the work.
`
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatalf("write workflow: %v", err)
			}

			w, err := LoadPath(path, dir)
			if err != nil {
				t.Fatalf("LoadPath returned error: %v", err)
			}
			if w.Validation.Valid {
				t.Fatalf("Validation.Valid = true, want false for %q", raw)
			}
			if strings.HasPrefix(filepath.Clean(w.Config.Workspace.Root), filepath.Clean(home)+string(os.PathSeparator)) || filepath.Clean(w.Config.Workspace.Root) == filepath.Clean(home) {
				t.Fatalf("workspace.root = %q was expanded under home %q", w.Config.Workspace.Root, home)
			}
			if !slices.Contains(w.Validation.Errors, "unsupported home directory expansion in config path: "+raw) {
				t.Fatalf("Validation.Errors = %v, want unsupported tilde error for %q", w.Validation.Errors, raw)
			}
		})
	}
}
