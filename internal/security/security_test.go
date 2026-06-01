package security

import (
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type errReader struct{}

func (errReader) Read([]byte) (int, error) {
	return 0, errors.New("entropy unavailable")
}

func TestNewTokenPanicsWhenRandomSourceFails(t *testing.T) {
	original := rand.Reader
	rand.Reader = errReader{}
	t.Cleanup(func() { rand.Reader = original })

	defer func() {
		if recover() == nil {
			t.Fatalf("NewToken did not fail closed when crypto/rand failed")
		}
	}()
	_ = NewToken()
}

func TestContainedPathRejectsSymlinkToProtectedPath(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("SECRET=1\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".ssh"), 0o700); err != nil {
		t.Fatalf("mkdir .ssh: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ssh", "id_rsa"), []byte("private key\n"), 0o600); err != nil {
		t.Fatalf("write id_rsa: %v", err)
	}

	tests := []struct {
		name   string
		link   string
		target string
	}{
		{name: "env", link: "safe.txt", target: ".env"},
		{name: "ssh key", link: "notes.txt", target: filepath.Join(".ssh", "id_rsa")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.Symlink(tt.target, filepath.Join(root, tt.link)); err != nil {
				t.Skipf("symlink not supported: %v", err)
			}

			if _, err := ContainedPath(root, tt.link); err == nil {
				t.Fatalf("ContainedPath(%q) succeeded, want protected path denial", tt.link)
			}
		})
	}
}

func TestDefaultPolicyEvaluatesCommandAllowReviewDeny(t *testing.T) {
	policy := DefaultPolicy()
	tests := []struct {
		name    string
		command []string
		want    PolicyOutcome
	}{
		{name: "allow test command", command: []string{"go", "test", "./..."}, want: PolicyAllow},
		{name: "review read command", command: []string{"cat", "README.md"}, want: PolicyReview},
		{name: "deny push command", command: []string{"git", "push"}, want: PolicyDeny},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := policy.EvaluateCommand(CommandRequest{Argv: tt.command})
			if got.Outcome != tt.want {
				t.Fatalf("EvaluateCommand(%q) outcome = %s, want %s", tt.command, got.Outcome, tt.want)
			}
		})
	}
}

func TestDefaultPolicyCommandProtectedPathOverrideWins(t *testing.T) {
	got := DefaultPolicy().EvaluateCommand(CommandRequest{Argv: []string{"cat", ".env"}})

	if got.Outcome != PolicyDeny {
		t.Fatalf("protected command outcome = %s, want %s", got.Outcome, PolicyDeny)
	}
	if got.Reason != "protected_path" {
		t.Fatalf("protected command reason = %q, want protected_path", got.Reason)
	}
}

func TestDefaultPolicyDoesNotAllowCompoundShellCommandWithDeniedTail(t *testing.T) {
	got := DefaultPolicy().EvaluateCommand(CommandRequest{CommandLine: "git status && git push origin main"})

	if got.Outcome != PolicyReview {
		t.Fatalf("compound command outcome = %s, want %s", got.Outcome, PolicyReview)
	}
}

func TestDefaultPolicyCommandProtectedPathOverrideChecksFlagValues(t *testing.T) {
	got := DefaultPolicy().EvaluateCommand(CommandRequest{Argv: []string{"go", "test", "./...", "-coverprofile=.env"}})

	if got.Outcome != PolicyDeny {
		t.Fatalf("protected flag path outcome = %s, want %s", got.Outcome, PolicyDeny)
	}
	if got.Reason != "protected_path" {
		t.Fatalf("protected flag path reason = %q, want protected_path", got.Reason)
	}
}

func TestDefaultPolicyNetworkDefaultDenyAndExplicitReview(t *testing.T) {
	got := DefaultPolicy().EvaluateNetwork(NetworkRequest{Host: "example.invalid"})
	if got.Outcome != PolicyDeny {
		t.Fatalf("default network outcome = %s, want %s", got.Outcome, PolicyDeny)
	}

	review := DefaultPolicy()
	review.NetworkDefault = PolicyReview
	got = review.EvaluateNetwork(NetworkRequest{Host: "example.invalid"})
	if got.Outcome != PolicyReview {
		t.Fatalf("review network outcome = %s, want %s", got.Outcome, PolicyReview)
	}
}

func TestDefaultPolicyAllowsNetworkAllowlistBeforeDefaultDeny(t *testing.T) {
	policy := DefaultPolicy()
	policy.NetworkAllowlist = []string{"example.invalid"}

	got := policy.EvaluateNetwork(NetworkRequest{Host: "example.invalid"})

	if got.Outcome != PolicyAllow {
		t.Fatalf("allowlisted network outcome = %s, want %s", got.Outcome, PolicyAllow)
	}
}

func TestDefaultPolicyProtectedPathDenial(t *testing.T) {
	got := DefaultPolicy().EvaluateProtectedPaths([]string{"notes.txt", ".ssh/id_rsa"})

	if got.Outcome != PolicyDeny {
		t.Fatalf("protected path outcome = %s, want %s", got.Outcome, PolicyDeny)
	}
	if got.Reason != "protected_path" {
		t.Fatalf("protected path reason = %q, want protected_path", got.Reason)
	}
}

func TestPolicyProtectedPathUsesConfiguredPatterns(t *testing.T) {
	policy := DefaultPolicy()
	policy.ProtectedPaths = []string{"secrets/**"}

	got := policy.EvaluateProtectedPaths([]string{"secrets/token.txt"})

	if got.Outcome != PolicyDeny {
		t.Fatalf("configured protected path outcome = %s, want %s", got.Outcome, PolicyDeny)
	}
}
