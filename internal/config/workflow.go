package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type EffectiveConfig struct {
	Tracker struct {
		Kind                       string   `json:"kind"`
		DispatchCandidateStates    []string `json:"dispatch_candidate_states"`
		ReconciliationActiveStates []string `json:"reconciliation_active_states"`
		TerminalStates             []string `json:"terminal_states"`
	} `json:"tracker"`
	Polling struct {
		IntervalMS int `json:"interval_ms"`
	} `json:"polling"`
	Workspace struct {
		Root string `json:"root"`
	} `json:"workspace"`
	Hooks struct {
		AfterCreate    *string `json:"after_create"`
		BeforeRun      *string `json:"before_run"`
		AfterRun       *string `json:"after_run"`
		TimeoutMS      int     `json:"timeout_ms"`
		MaxOutputBytes int     `json:"max_output_bytes"`
	} `json:"hooks"`
	Git struct {
		Enabled      bool   `json:"enabled"`
		Mode         string `json:"mode"`
		RepoRoot     string `json:"repo_root"`
		BaseRef      string `json:"base_ref"`
		BranchPrefix string `json:"branch_prefix"`
		AgentCommit  string `json:"agent_commit"`
		AutoPush     bool   `json:"auto_push"`
		AutoRebase   bool   `json:"auto_rebase"`
		Submodules   bool   `json:"submodules"`
	} `json:"git"`
	Agent struct {
		MaxConcurrentAgents     int    `json:"max_concurrent_agents"`
		MaxTurnsPerRun          int    `json:"max_turns_per_run"`
		MaxHandoffContinuations int    `json:"max_handoff_continuations"`
		HandoffRequired         bool   `json:"handoff_required"`
		HandoffState            string `json:"handoff_state"`
		PauseOnMissingHandoff   bool   `json:"pause_on_missing_handoff"`
	} `json:"agent"`
	Codex struct {
		Command                 string `json:"command"`
		RequireCommittedFixture bool   `json:"require_committed_fixture"`
		ExperimentalAPI         bool   `json:"experimental_api"`
		TurnTimeoutMS           int    `json:"turn_timeout_ms"`
		StallTimeoutMS          int    `json:"stall_timeout_ms"`
		StartupTimeoutMS        int    `json:"startup_timeout_ms"`
		ReadTimeoutMS           int    `json:"read_timeout_ms"`
	} `json:"codex"`
	Tools struct {
		Gateway                  string `json:"gateway"`
		RequireHandoffTool       bool   `json:"require_handoff_tool"`
		AllowDynamicTools        bool   `json:"allow_dynamic_tools"`
		AllowMCP                 bool   `json:"allow_mcp"`
		AgentCanCreateFollowups  bool   `json:"agent_can_create_followups"`
		AgentCanSetBlocked       bool   `json:"agent_can_set_blocked"`
		AgentCanSetTerminalState bool   `json:"agent_can_set_terminal_state"`
		ArtifactMaxBytes         int64  `json:"artifact_max_bytes"`
	} `json:"tools"`
	Security struct {
		Mode                string `json:"mode"`
		RequireLoopbackAPI  bool   `json:"require_loopback_api"`
		AllowRemoteAPI      bool   `json:"allow_remote_api"`
		RequireSessionToken bool   `json:"require_session_token"`
		RequireCSRF         bool   `json:"require_csrf"`
	} `json:"security"`
	Server struct {
		Host               string `json:"host"`
		Port               int    `json:"port"`
		OpenBrowserOnStart bool   `json:"open_browser_on_start"`
	} `json:"server"`
	Prompt struct {
		MaxContextBytes     int    `json:"max_context_bytes"`
		IncludePreviousRuns bool   `json:"include_previous_runs"`
		PreviousRunLimit    int    `json:"previous_run_limit"`
		IncludeToolManifest bool   `json:"include_tool_manifest"`
		SavePromptSnapshot  string `json:"save_prompt_snapshot"`
	} `json:"prompt"`
	Raw map[string]any `json:"-"`
}

type Validation struct {
	Valid    bool     `json:"valid"`
	Warnings []string `json:"warnings"`
	Errors   []string `json:"errors"`
}

type Workflow struct {
	Path       string          `json:"path"`
	Config     EffectiveConfig `json:"config"`
	PromptBody string          `json:"prompt_body"`
	Validation Validation      `json:"validation"`
	ConfigJSON string          `json:"config_json"`
	PromptHash string          `json:"prompt_hash"`
}

var supportedTop = map[string]bool{"tracker": true, "polling": true, "workspace": true, "hooks": true, "git": true, "agent": true, "codex": true, "approvals": true, "tools": true, "security": true, "observability": true, "server": true, "ui": true, "prompt": true}

func Defaults(repoRoot string) EffectiveConfig {
	var c EffectiveConfig
	c.Tracker.Kind = "local"
	c.Tracker.DispatchCandidateStates = []string{"Ready", "Rework"}
	c.Tracker.ReconciliationActiveStates = []string{"Ready", "Working", "Rework"}
	c.Tracker.TerminalStates = []string{"Done", "Cancelled", "Duplicate"}
	c.Polling.IntervalMS = 30000
	c.Workspace.Root = filepath.Join(homeDir(), ".symphony", "workspaces")
	c.Hooks.TimeoutMS = 300000
	c.Hooks.MaxOutputBytes = 65536
	c.Git.Enabled = true
	c.Git.Mode = "worktree"
	c.Git.RepoRoot = repoRoot
	c.Git.BaseRef = "auto"
	c.Git.BranchPrefix = "symphony"
	c.Git.AgentCommit = "manual"
	c.Git.AutoPush = false
	c.Git.AutoRebase = false
	c.Agent.MaxConcurrentAgents = 3
	c.Agent.MaxTurnsPerRun = 2
	c.Agent.MaxHandoffContinuations = 1
	c.Agent.HandoffRequired = true
	c.Agent.HandoffState = "Human Review"
	c.Agent.PauseOnMissingHandoff = true
	c.Codex.Command = "codex app-server"
	c.Codex.RequireCommittedFixture = true
	c.Codex.StartupTimeoutMS = 60000
	c.Codex.TurnTimeoutMS = 3600000
	c.Codex.StallTimeoutMS = 300000
	c.Codex.ReadTimeoutMS = 5000
	c.Tools.Gateway = "cli"
	c.Tools.RequireHandoffTool = true
	c.Tools.AllowDynamicTools = false
	c.Tools.AllowMCP = false
	c.Tools.AgentCanCreateFollowups = true
	c.Tools.AgentCanSetBlocked = true
	c.Tools.AgentCanSetTerminalState = false
	c.Tools.ArtifactMaxBytes = 10485760
	c.Security.Mode = "balanced-secure"
	c.Security.RequireLoopbackAPI = true
	c.Security.AllowRemoteAPI = false
	c.Security.RequireSessionToken = true
	c.Security.RequireCSRF = true
	c.Server.Host = "127.0.0.1"
	c.Server.Port = 0
	c.Prompt.MaxContextBytes = 200000
	c.Prompt.IncludePreviousRuns = true
	c.Prompt.PreviousRunLimit = 3
	c.Prompt.IncludeToolManifest = true
	c.Prompt.SavePromptSnapshot = "redacted"
	return c
}

func Load(repoRoot string) (*Workflow, error) {
	return LoadPath(filepath.Join(repoRoot, "WORKFLOW.md"), repoRoot)
}
func LoadPath(path, repoRoot string) (*Workflow, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return &Workflow{Path: path, Config: Defaults(repoRoot), Validation: Validation{Valid: false, Errors: []string{err.Error()}}}, nil
	}
	fm, body, warns, errs := splitAndParseFrontMatter(string(b))
	cfg := Defaults(repoRoot)
	cfg.Raw = fm
	for k := range fm {
		if !supportedTop[k] {
			warns = append(warns, "unknown top-level config key: "+k)
		}
	}
	applyMap(&cfg, fm, filepath.Dir(path), &warns, &errs)
	if strings.TrimSpace(body) == "" {
		errs = append(errs, "prompt body must not be empty")
	}
	hardValidate(cfg, &errs)
	body = strings.TrimSpace(body)
	h := sha256.Sum256([]byte(body))
	cj, _ := json.Marshal(cfg)
	sort.Strings(warns)
	sort.Strings(errs)
	return &Workflow{Path: path, Config: cfg, PromptBody: body, Validation: Validation{Valid: len(errs) == 0, Warnings: warns, Errors: errs}, ConfigJSON: string(cj), PromptHash: hex.EncodeToString(h[:])}, nil
}

func splitAndParseFrontMatter(text string) (map[string]any, string, []string, []string) {
	if !strings.HasPrefix(text, "---\n") {
		return map[string]any{}, text, nil, nil
	}
	idx := strings.Index(text[4:], "\n---")
	if idx < 0 {
		return map[string]any{}, text, nil, []string{"unterminated YAML front matter"}
	}
	fmText := text[4 : 4+idx]
	body := text[4+idx+len("\n---"):]
	m, warnings, errors := parseTinyYAML(fmText)
	return m, body, warnings, errors
}

func parseTinyYAML(text string) (map[string]any, []string, []string) {
	root := map[string]any{}
	stack := []map[string]any{root}
	indents := []int{0}
	var warnings, errors []string
	lines := strings.Split(text, "\n")
	for _, raw := range lines {
		line := strings.TrimRight(raw, " \t")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "-") {
			errors = append(errors, "list shorthand is supported only as inline arrays")
			continue
		}
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			errors = append(errors, "invalid YAML line: "+trimmed)
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		for len(indents) > 1 && indent <= indents[len(indents)-1] {
			stack = stack[:len(stack)-1]
			indents = indents[:len(indents)-1]
		}
		cur := stack[len(stack)-1]
		if val == "" {
			child := map[string]any{}
			cur[key] = child
			stack = append(stack, child)
			indents = append(indents, indent)
		} else {
			cur[key] = parseScalar(val)
		}
	}
	return root, warnings, errors
}

func parseScalar(s string) any {
	s = strings.TrimSpace(s)
	if s == "null" || s == "~" {
		return nil
	}
	if s == "true" {
		return true
	}
	if s == "false" {
		return false
	}
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		inner := strings.TrimSpace(s[1 : len(s)-1])
		if inner == "" {
			return []any{}
		}
		parts := strings.Split(inner, ",")
		out := []any{}
		for _, p := range parts {
			out = append(out, parseScalar(strings.TrimSpace(p)))
		}
		return out
	}
	if len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'')) {
		return s[1 : len(s)-1]
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return s
}

func applyMap(c *EffectiveConfig, m map[string]any, baseDir string, warnings, errors *[]string) {
	section := func(name string) map[string]any {
		if v, ok := m[name].(map[string]any); ok {
			return v
		}
		if _, ok := m[name]; ok {
			*errors = append(*errors, name+" must be an object")
		}
		return nil
	}
	if t := section("tracker"); t != nil {
		if v, ok := str(t, "kind", errors); ok {
			c.Tracker.Kind = v
		}
	}
	if w := section("workspace"); w != nil {
		if v, ok := str(w, "root", errors); ok {
			c.Workspace.Root = resolvePath(expandEnv(v, errors), baseDir, errors)
		}
	}
	if p := section("polling"); p != nil {
		if v, ok := intv(p, "interval_ms", errors); ok {
			c.Polling.IntervalMS = v
		}
	}
	if g := section("git"); g != nil {
		if v, ok := str(g, "repo_root", errors); ok {
			c.Git.RepoRoot = resolvePath(expandEnv(v, errors), baseDir, errors)
		}
		if v, ok := str(g, "base_ref", errors); ok {
			c.Git.BaseRef = v
		}
		if v, ok := str(g, "branch_prefix", errors); ok {
			c.Git.BranchPrefix = v
		}
		if v, ok := boolv(g, "auto_push", errors); ok {
			c.Git.AutoPush = v
		}
		if v, ok := boolv(g, "auto_rebase", errors); ok {
			c.Git.AutoRebase = v
		}
	}
	if a := section("agent"); a != nil {
		if v, ok := intv(a, "max_concurrent_agents", errors); ok {
			c.Agent.MaxConcurrentAgents = v
		}
		if v, ok := intv(a, "max_handoff_continuations", errors); ok {
			c.Agent.MaxHandoffContinuations = v
		}
		_, hasMaxTurnsPerRun := a["max_turns_per_run"]
		if v, ok := intv(a, "max_turns_per_run", errors); ok {
			c.Agent.MaxTurnsPerRun = v
		}
		if v, ok := intv(a, "max_turns", errors); ok && !hasMaxTurnsPerRun {
			c.Agent.MaxTurnsPerRun = v
		}
		if v, ok := boolv(a, "handoff_required", errors); ok {
			c.Agent.HandoffRequired = v
		}
		if v, ok := str(a, "handoff_state", errors); ok {
			c.Agent.HandoffState = v
		}
		if v, ok := boolv(a, "pause_on_missing_handoff", errors); ok {
			c.Agent.PauseOnMissingHandoff = v
		}
	}
	if h := section("hooks"); h != nil {
		if v, ok := str(h, "after_run", errors); ok {
			c.Hooks.AfterRun = &v
		}
		if v, ok := str(h, "before_run", errors); ok {
			c.Hooks.BeforeRun = &v
		}
		if v, ok := str(h, "after_create", errors); ok {
			c.Hooks.AfterCreate = &v
		}
		if v, ok := intv(h, "timeout_ms", errors); ok {
			c.Hooks.TimeoutMS = v
		}
		if v, ok := intv(h, "max_output_bytes", errors); ok {
			c.Hooks.MaxOutputBytes = v
		}
	}
	if cx := section("codex"); cx != nil {
		if v, ok := str(cx, "command", errors); ok {
			c.Codex.Command = v
		}
		if v, ok := boolv(cx, "require_committed_fixture", errors); ok {
			c.Codex.RequireCommittedFixture = v
		}
		if v, ok := boolv(cx, "experimental_api", errors); ok {
			c.Codex.ExperimentalAPI = v
		}
		if v, ok := intv(cx, "startup_timeout_ms", errors); ok {
			c.Codex.StartupTimeoutMS = v
		}
		if v, ok := intv(cx, "turn_timeout_ms", errors); ok {
			c.Codex.TurnTimeoutMS = v
		}
		if v, ok := intv(cx, "stall_timeout_ms", errors); ok {
			c.Codex.StallTimeoutMS = v
		}
		if v, ok := intv(cx, "read_timeout_ms", errors); ok {
			c.Codex.ReadTimeoutMS = v
		}
	}
	if t := section("tools"); t != nil {
		if v, ok := boolv(t, "allow_dynamic_tools", errors); ok {
			c.Tools.AllowDynamicTools = v
		}
		if v, ok := boolv(t, "allow_mcp", errors); ok {
			c.Tools.AllowMCP = v
		}
		if v, ok := boolv(t, "agent_can_set_terminal_state", errors); ok {
			c.Tools.AgentCanSetTerminalState = v
		}
		if v, ok := intv(t, "artifact_max_bytes", errors); ok {
			c.Tools.ArtifactMaxBytes = int64(v)
		}
	}
	if s := section("security"); s != nil {
		if v, ok := boolv(s, "allow_remote_api", errors); ok {
			c.Security.AllowRemoteAPI = v
		}
	}
	if srv := section("server"); srv != nil {
		if v, ok := str(srv, "host", errors); ok {
			c.Server.Host = v
		}
		if v, ok := intv(srv, "port", errors); ok {
			c.Server.Port = v
		}
	}
	if p := section("prompt"); p != nil {
		if v, ok := intv(p, "max_context_bytes", errors); ok {
			c.Prompt.MaxContextBytes = v
		}
		if v, ok := boolv(p, "include_previous_runs", errors); ok {
			c.Prompt.IncludePreviousRuns = v
		}
		if v, ok := intv(p, "previous_run_limit", errors); ok {
			c.Prompt.PreviousRunLimit = v
		}
		if v, ok := boolv(p, "include_tool_manifest", errors); ok {
			c.Prompt.IncludeToolManifest = v
		}
		if v, ok := str(p, "save_prompt_snapshot", errors); ok {
			c.Prompt.SavePromptSnapshot = v
		}
	}
}

func str(m map[string]any, k string, errs *[]string) (string, bool) {
	if v, ok := m[k]; ok {
		s, ok := v.(string)
		if !ok {
			*errs = append(*errs, k+" must be string")
			return "", false
		}
		return s, true
	}
	return "", false
}
func intv(m map[string]any, k string, errs *[]string) (int, bool) {
	if v, ok := m[k]; ok {
		switch x := v.(type) {
		case int:
			return x, true
		case int64:
			return int(x), true
		case float64:
			return int(x), true
		default:
			*errs = append(*errs, k+" must be integer")
			return 0, false
		}
	}
	return 0, false
}
func boolv(m map[string]any, k string, errs *[]string) (bool, bool) {
	if v, ok := m[k]; ok {
		b, ok := v.(bool)
		if !ok {
			*errs = append(*errs, k+" must be boolean")
			return false, false
		}
		return b, true
	}
	return false, false
}

var fullEnv = regexp.MustCompile(`^\$[A-Za-z_][A-Za-z0-9_]*$`)

func expandEnv(v string, errs *[]string) string {
	if fullEnv.MatchString(v) {
		name := v[1:]
		out := os.Getenv(name)
		if out == "" {
			*errs = append(*errs, "environment variable "+name+" is unset or empty")
			return v
		}
		return out
	}
	if strings.Contains(v, "{{") || strings.Contains(v, "}}") {
		*errs = append(*errs, "Liquid interpolation is not allowed in config paths")
	}
	return v
}
func resolvePath(v, base string, errs *[]string) string {
	if v == "~" {
		v = homeDir()
	} else if strings.HasPrefix(v, "~/") {
		v = filepath.Join(homeDir(), strings.TrimPrefix(v, "~/"))
	} else if strings.HasPrefix(v, "~") {
		*errs = append(*errs, "unsupported home directory expansion in config path: "+v)
	}
	if !filepath.IsAbs(v) {
		v = filepath.Join(base, v)
	}
	abs, _ := filepath.Abs(v)
	return abs
}
func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil || h == "" {
		return os.TempDir()
	}
	return h
}
func hardValidate(c EffectiveConfig, errs *[]string) {
	if c.Tracker.Kind != "local" {
		*errs = append(*errs, "tracker.kind must equal local")
	}
	if !c.Agent.HandoffRequired {
		*errs = append(*errs, "agent.handoff_required must be true")
	}
	if !c.Agent.PauseOnMissingHandoff {
		*errs = append(*errs, "agent.pause_on_missing_handoff must be true")
	}
	if c.Agent.HandoffState != "Human Review" {
		*errs = append(*errs, "agent.handoff_state must equal Human Review")
	}
	if c.Agent.MaxHandoffContinuations < 0 || c.Agent.MaxHandoffContinuations > 1 {
		*errs = append(*errs, "agent.max_handoff_continuations must be 0 or 1")
	}
	if c.Agent.MaxTurnsPerRun != 1+c.Agent.MaxHandoffContinuations {
		*errs = append(*errs, "agent.max_turns_per_run must equal 1 + max_handoff_continuations")
	}
	if c.Tools.AllowDynamicTools {
		*errs = append(*errs, "tools.allow_dynamic_tools must be false")
	}
	if c.Tools.AllowMCP {
		*errs = append(*errs, "tools.allow_mcp must be false")
	}
	if c.Tools.AgentCanSetTerminalState {
		*errs = append(*errs, "tools.agent_can_set_terminal_state must be false")
	}
	if c.Tools.ArtifactMaxBytes <= 0 {
		*errs = append(*errs, "tools.artifact_max_bytes must be greater than 0")
	}
	if c.Hooks.MaxOutputBytes < 0 {
		*errs = append(*errs, "hooks.max_output_bytes must be greater than or equal to 0")
	}
	if c.Git.BranchPrefix != "symphony" {
		*errs = append(*errs, "git.branch_prefix must equal symphony")
	}
	if c.Git.AutoPush {
		*errs = append(*errs, "git.auto_push must be false")
	}
	if c.Git.AutoRebase {
		*errs = append(*errs, "git.auto_rebase must be false")
	}
	if c.Security.AllowRemoteAPI {
		*errs = append(*errs, "security.allow_remote_api must be false")
	}
	if filepath.Clean(c.Workspace.Root) == filepath.Clean(c.Git.RepoRoot) {
		*errs = append(*errs, "workspace.root must not equal git.repo_root")
	}
}

func RenderPrompt(w *Workflow, ctx map[string]any) (string, error) {
	rendered := w.PromptBody
	// Minimal strict renderer for common variables; unknown {{ }} is rejected.
	re := regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_\.]+)\s*\}\}`)
	var err error
	rendered = re.ReplaceAllStringFunc(rendered, func(m string) string {
		key := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(m, "{{"), "}}"))
		val, ok := lookup(ctx, key)
		if !ok {
			err = fmt.Errorf("unknown prompt variable %s", key)
			return ""
		}
		return fmt.Sprint(val)
	})
	if err != nil {
		return "", err
	}
	if strings.Contains(rendered, "{{") || strings.Contains(rendered, "}}") {
		return "", fmt.Errorf("unsupported prompt expression")
	}
	envelope := "# Symphony Runtime Envelope\n- Work only inside the current workspace.\n- Do not push, create PRs, mark Done, or mutate project settings.\n\n"
	tool := "# Tool Manifest\nUse `symphony tool handoff submit --json -` after completion.\n\n"
	handoff := "# Handoff Contract\nSubmit summary, changed_files, tests, risks, verification, followups, target_state=Human Review.\n\n"
	return envelope + tool + rendered + "\n\n" + handoff, nil
}
func lookup(m map[string]any, key string) (any, bool) {
	var cur any = m
	for _, p := range strings.Split(key, ".") {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = mm[p]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}
