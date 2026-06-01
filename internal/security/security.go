package security

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func NewToken() string {
	var b [24]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		panic("crypto/rand failed while generating token: " + err.Error())
	}
	return "sym_" + hex.EncodeToString(b[:])
}
func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
func SHA256Bytes(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

type PolicyOutcome string

const (
	PolicyAllow  PolicyOutcome = "allow"
	PolicyReview PolicyOutcome = "review"
	PolicyDeny   PolicyOutcome = "deny"
)

type Policy struct {
	NetworkDefault   PolicyOutcome
	NetworkAllowlist []string
	ProtectedPaths   []string
}

type PolicyDecision struct {
	Outcome     PolicyOutcome
	Reason      string
	PolicyMatch string
}

type CommandRequest struct {
	Argv        []string
	CommandLine string
	Paths       []string
}

type NetworkRequest struct {
	Host string
}

func DefaultPolicy() Policy {
	return Policy{
		NetworkDefault: PolicyDeny,
		ProtectedPaths: []string{".env", ".env.*", "**/*.pem", "**/*.key", "**/*_rsa", "**/*_ed25519", ".ssh/**", ".aws/**", ".gcp/**", ".azure/**", ".kube/**", ".npmrc", ".pypirc", ".netrc"},
	}
}

func (p Policy) EvaluateCommand(req CommandRequest) PolicyDecision {
	argv := cleanArgv(req.Argv)
	if len(argv) == 0 && strings.TrimSpace(req.CommandLine) != "" {
		argv = shellFields(req.CommandLine)
	}
	if protected := p.firstProtectedPath(append(append([]string{}, req.Paths...), commandPathCandidates(argv)...)); protected != "" {
		return PolicyDecision{Outcome: PolicyDeny, Reason: "protected_path", PolicyMatch: "protected_path.default_deny"}
	}
	if len(argv) == 0 {
		return PolicyDecision{Outcome: PolicyReview, Reason: "command_unclassified", PolicyMatch: "command.review.unclassified"}
	}
	if hasShellControlOperator(argv) {
		return PolicyDecision{Outcome: PolicyReview, Reason: "command_compound", PolicyMatch: "command.review.compound"}
	}
	switch {
	case commandMatches(argv, "git", "push"),
		commandMatches(argv, "git", "push", "--force"),
		commandMatches(argv, "gh", "pr", "create"),
		commandMatches(argv, "gh", "pr", "merge"),
		commandMatches(argv, "sudo"),
		commandMatches(argv, "ssh"),
		commandMatches(argv, "scp"),
		commandMatches(argv, "docker", "run", "--privileged"),
		commandMatches(argv, "rm", "-rf", "/"),
		commandMatches(argv, "rm", "-rf", "~"),
		shellPipeToShell(argv, "curl"),
		shellPipeToShell(argv, "wget"):
		return PolicyDecision{Outcome: PolicyDeny, Reason: "command_deny", PolicyMatch: "command.deny.default"}
	case commandMatches(argv, "git", "status"),
		commandMatches(argv, "git", "diff"),
		commandMatches(argv, "git", "log"),
		commandMatches(argv, "rg"),
		commandMatches(argv, "go", "test", "./..."),
		commandMatches(argv, "pytest"),
		commandMatches(argv, "npm", "test"),
		commandMatches(argv, "pnpm", "test"),
		commandMatches(argv, "cargo", "test"),
		allowedSymphonyTool(argv):
		return PolicyDecision{Outcome: PolicyAllow, Reason: "command_allow", PolicyMatch: "command.allow.default"}
	case commandMatches(argv, "cat"),
		commandMatches(argv, "grep"),
		commandMatches(argv, "find"),
		commandMatches(argv, "ls"),
		commandMatches(argv, "npm", "install"),
		commandMatches(argv, "pnpm", "install"),
		commandMatches(argv, "yarn", "install"),
		commandMatches(argv, "pip", "install"),
		commandMatches(argv, "go", "mod", "download"),
		commandMatches(argv, "cargo", "fetch"),
		commandMatches(argv, "make"),
		commandMatches(argv, "docker", "build"):
		return PolicyDecision{Outcome: PolicyReview, Reason: "command_review", PolicyMatch: "command.review.default"}
	default:
		return PolicyDecision{Outcome: PolicyReview, Reason: "command_unclassified", PolicyMatch: "command.review.unclassified"}
	}
}

func (p Policy) EvaluateNetwork(req NetworkRequest) PolicyDecision {
	host := strings.ToLower(strings.TrimSpace(req.Host))
	for _, allowed := range p.NetworkAllowlist {
		if host != "" && host == strings.ToLower(strings.TrimSpace(allowed)) {
			return PolicyDecision{Outcome: PolicyAllow, Reason: "network_allowlist", PolicyMatch: "network.allowlist"}
		}
	}
	if p.NetworkDefault == PolicyReview {
		return PolicyDecision{Outcome: PolicyReview, Reason: "network_default_review", PolicyMatch: "network.default_review"}
	}
	if p.NetworkDefault == PolicyAllow {
		return PolicyDecision{Outcome: PolicyAllow, Reason: "network_default_allow", PolicyMatch: "network.default_allow"}
	}
	return PolicyDecision{Outcome: PolicyDeny, Reason: "network_default_deny", PolicyMatch: "network.default_deny"}
}

func (p Policy) EvaluateProtectedPaths(paths []string) PolicyDecision {
	if protected := p.firstProtectedPath(paths); protected != "" {
		return PolicyDecision{Outcome: PolicyDeny, Reason: "protected_path", PolicyMatch: "protected_path.default_deny"}
	}
	return PolicyDecision{Outcome: PolicyAllow, Reason: "no_protected_path", PolicyMatch: "protected_path.none"}
}

func (p Policy) firstProtectedPath(paths []string) string {
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path != "" && p.isProtectedPath(path) {
			return path
		}
	}
	return ""
}

func (p Policy) isProtectedPath(candidate string) bool {
	if IsProtectedPath(candidate) {
		return true
	}
	candidate = filepath.ToSlash(strings.TrimSpace(candidate))
	for _, pattern := range p.ProtectedPaths {
		if protectedPatternMatches(pattern, candidate) {
			return true
		}
	}
	return false
}

func protectedPatternMatches(pattern, candidate string) bool {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	candidate = strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(candidate)), "./")
	if pattern == "" || candidate == "" {
		return false
	}
	if pattern == candidate {
		return true
	}
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		return candidate == prefix || strings.HasPrefix(candidate, prefix+"/")
	}
	if strings.HasPrefix(pattern, "**/") {
		suffix := strings.TrimPrefix(pattern, "**/")
		if ok, _ := filepath.Match(suffix, filepath.Base(candidate)); ok {
			return true
		}
	}
	if ok, _ := filepath.Match(pattern, candidate); ok {
		return true
	}
	if ok, _ := filepath.Match(pattern, filepath.Base(candidate)); ok {
		return true
	}
	return false
}

func cleanArgv(argv []string) []string {
	out := make([]string, 0, len(argv))
	for _, part := range argv {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func commandMatches(argv []string, prefix ...string) bool {
	if len(argv) < len(prefix) {
		return false
	}
	for i, part := range prefix {
		if argv[i] != part {
			return false
		}
	}
	return true
}

func allowedSymphonyTool(argv []string) bool {
	if !commandMatches(argv, "symphony", "tool") || len(argv) < 3 {
		return false
	}
	switch strings.Join(argv[2:intMin(len(argv), 4)], " ") {
	case "issue get", "issue comment", "issue block", "artifact attach", "followup create", "handoff submit":
		return true
	default:
		return false
	}
}

func intMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func shellPipeToShell(argv []string, downloader string) bool {
	if len(argv) < 3 || argv[0] != downloader {
		return false
	}
	for i := 1; i+1 < len(argv); i++ {
		if argv[i] == "|" && (argv[i+1] == "sh" || argv[i+1] == "bash") {
			return true
		}
	}
	return false
}

func hasShellControlOperator(argv []string) bool {
	for _, arg := range argv {
		switch arg {
		case "|", "||", "&&", ";", "&":
			return true
		}
	}
	return false
}

func commandPathCandidates(argv []string) []string {
	if len(argv) < 2 {
		return nil
	}
	out := []string{}
	for _, arg := range argv[1:] {
		if arg == "|" {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			if before, after, ok := strings.Cut(arg, "="); ok && before != "" && after != "" {
				arg = after
			} else {
				continue
			}
		}
		if strings.Contains(arg, "/") || strings.Contains(arg, ".") || strings.HasPrefix(arg, "~") {
			out = append(out, arg)
		}
	}
	return out
}

func shellFields(command string) []string {
	var fields []string
	var cur strings.Builder
	var quote rune
	escaped := false
	for _, r := range command {
		if escaped {
			cur.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
				continue
			}
			cur.WriteRune(r)
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if strings.TrimSpace(string(r)) == "" {
			if cur.Len() > 0 {
				fields = append(fields, cur.String())
				cur.Reset()
			}
			continue
		}
		cur.WriteRune(r)
	}
	if cur.Len() > 0 {
		fields = append(fields, cur.String())
	}
	return fields
}

func IsProtectedPath(path string) bool {
	base := filepath.Base(path)
	if base == ".env" || strings.HasPrefix(base, ".env.") || base == ".npmrc" || base == ".pypirc" || base == ".netrc" {
		return true
	}
	low := strings.ToLower(path)
	if strings.HasSuffix(low, ".pem") || strings.HasSuffix(low, ".key") || strings.HasSuffix(low, "_rsa") || strings.HasSuffix(low, "_ed25519") {
		return true
	}
	parts := strings.Split(filepath.ToSlash(low), "/")
	for _, p := range parts {
		if p == ".ssh" || p == ".aws" || p == ".gcp" || p == ".azure" || p == ".kube" {
			return true
		}
	}
	return false
}

func ContainedPath(root, rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) {
		return "", errors.New("path must be workspace-relative")
	}
	target := filepath.Clean(filepath.Join(root, rel))
	rr, _ := filepath.Abs(root)
	tt, _ := filepath.Abs(target)
	r, err := filepath.Rel(rr, tt)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(r, "..") || filepath.IsAbs(r) {
		return "", errors.New("path traversal rejected")
	}
	if resolved, err := filepath.EvalSymlinks(tt); err == nil {
		rr2, _ := filepath.EvalSymlinks(rr)
		if rr2 == "" {
			rr2 = rr
		}
		rel2, err := filepath.Rel(rr2, resolved)
		if err != nil || strings.HasPrefix(rel2, "..") || filepath.IsAbs(rel2) {
			return "", errors.New("symlink escape rejected")
		}
		if IsProtectedPath(rel2) {
			return "", errors.New("protected path denied")
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if IsProtectedPath(rel) {
		return "", errors.New("protected path denied")
	}
	return target, nil
}

func RedactString(s string) string {
	if len(s) > 2000 {
		return s[:2000] + "\n[truncated/redacted]"
	}
	return s
}
