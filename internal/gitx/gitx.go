package gitx

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Result struct {
	Stdout string
	Stderr string
	Err    error
}

func Run(dir string, args ...string) Result {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	return Result{Stdout: out.String(), Stderr: errb.String(), Err: err}
}

func IsRepo(dir string) bool {
	r := Run(dir, "rev-parse", "--is-inside-work-tree")
	return r.Err == nil && strings.TrimSpace(r.Stdout) == "true"
}
func HasHEAD(dir string) bool { return Run(dir, "rev-parse", "--verify", "HEAD").Err == nil }
func HeadSHA(dir string) string {
	return RefSHA(dir, "HEAD")
}
func RefSHA(dir, ref string) string {
	if strings.TrimSpace(ref) == "" {
		ref = "HEAD"
	}
	r := Run(dir, "rev-parse", ref)
	if r.Err != nil {
		return "0000000000000000000000000000000000000000"
	}
	return strings.TrimSpace(r.Stdout)
}
func CurrentBranch(dir string) string {
	r := Run(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if r.Err != nil {
		return "HEAD"
	}
	return strings.TrimSpace(r.Stdout)
}

func WorktreeAdd(repoRoot, path, branch, startPoint string) error {
	if !IsRepo(repoRoot) || !HasHEAD(repoRoot) {
		return copyWorkspace(repoRoot, path)
	}
	if strings.TrimSpace(startPoint) == "" {
		startPoint = "HEAD"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("workspace path already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return err
	}
	r := Run(repoRoot, "worktree", "add", "-b", branch, path, startPoint)
	if r.Err == nil {
		return nil
	}
	msg := strings.TrimSpace(r.Stderr)
	if msg == "" {
		msg = r.Err.Error()
	}
	return fmt.Errorf("git worktree add from %q failed: %s", startPoint, msg)
}

func copyWorkspace(src, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("workspace path already exists: %s", dst)
	} else if !os.IsNotExist(err) {
		return err
	}
	nested, err := isPathWithin(src, dst)
	if err != nil {
		return err
	}
	if nested {
		return fmt.Errorf("workspace destination is inside source workspace: %s", dst)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if rel == "." {
			return nil
		}
		if d.IsDir() && (rel == ".git" || rel == ".symphony") {
			return filepath.SkipDir
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, info.Mode().Perm())
	})
}

func isPathWithin(parent, child string) (bool, error) {
	parentAbs, err := filepath.Abs(parent)
	if err != nil {
		return false, err
	}
	childAbs, err := filepath.Abs(child)
	if err != nil {
		return false, err
	}
	rel, err := filepath.Rel(parentAbs, childAbs)
	if err != nil {
		return false, err
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel), nil
}

func StatusPorcelain(dir string) ([]string, error) {
	r := Run(dir, "status", "--porcelain=v1", "-uall")
	if r.Err != nil {
		return nil, r.Err
	}
	lines := []string{}
	for _, l := range strings.Split(strings.TrimRight(r.Stdout, "\n"), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}
func DiffBinary(dir string) string {
	r := Run(dir, "diff", "--binary", "HEAD", "--")
	if r.Err != nil {
		return ""
	}
	return r.Stdout
}
func DiffBinaryPaths(dir string, paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	args := append([]string{"--literal-pathspecs", "diff", "--binary", "--find-renames", "HEAD", "--"}, paths...)
	r := Run(dir, args...)
	if r.Err != nil {
		return ""
	}
	return r.Stdout
}
func DiffNameOnly(dir string) []string {
	r := Run(dir, "diff", "--name-only", "HEAD", "--")
	if r.Err != nil {
		return nil
	}
	out := []string{}
	for _, l := range strings.Split(strings.TrimSpace(r.Stdout), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}
func DiffNumstat(dir string) string {
	r := Run(dir, "diff", "--numstat", "HEAD", "--")
	if r.Err != nil {
		return ""
	}
	return r.Stdout
}
func DiffNumstatPaths(dir string, paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	args := append([]string{"--literal-pathspecs", "diff", "--numstat", "--find-renames", "HEAD", "--"}, paths...)
	r := Run(dir, args...)
	if r.Err != nil {
		return ""
	}
	return r.Stdout
}

func EnsureUnder(path, root string) error {
	ap, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	ar, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(ar, ap)
	if err != nil {
		return err
	}
	if rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)) {
		return nil
	}
	return errors.New("path escapes root")
}
