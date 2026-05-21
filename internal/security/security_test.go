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
