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
