// Package buildrelease contains tests that lock in the safety guarantees
// of scripts/build-release.sh as required by the D5 codex review round 1:
//
//   F1 [P1]  scripts/build-release.sh must not data-destructively delete the
//            repo's `web/` source tree when OUT_DIR resolves to/under
//            $ROOT. Two protections: (a) early-exit guard rejecting
//            OUT_DIR equal to or under $ROOT; (b) the destructive
//            `rm -rf` is scoped to $OUT_DIR/web/dist, not $OUT_DIR/web.
//
//   F2 [P2]  the web install must be deterministic — a frozen install
//            against the committed web/package-lock.json. The script
//            must invoke `npm ci` (not `npm install`); the repo must
//            carry web/package-lock.json while NOT carrying
//            web/pnpm-lock.yaml (a stale conflicting lockfile is a
//            footgun for the next person who runs `pnpm install`).
//
// F1 is exercised by running the shell script in an isolated temp
// workdir (a *copy* of the script, not the real $ROOT) so a regression
// cannot damage the host's source tree. F2 is exercised by static
// inspection of the committed script and lockfile layout.
package buildrelease

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot returns the absolute path of the Local Symphony repo, derived
// from this test file's location (internal/buildrelease/safety_test.go
// → ../ → repo root).
func repoRoot(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// `go test` runs with cwd == the package directory by default.
	// Internal callers can `go test ./internal/buildrelease` from the
	// repo root, in which case cwd is the package dir and the repo
	// root is one level up. Accept both layouts.
	if filepath.Base(cwd) == "buildrelease" {
		return filepath.Clean(filepath.Join(cwd, "..", ".."))
	}
	return cwd
}

// runIsolatedScript copies scripts/build-release.sh to a fresh temp
// directory, lays down the minimum web/ tree the script's F1 guard
// expects to find, and runs the copy with the supplied $OUT_DIR. The
// script auto-derives its own $ROOT from its location, so the real
// repo's $ROOT is never at risk.
func runIsolatedScript(t *testing.T, outDir string, extraEnv ...string) (string, int) {
	t.Helper()
	root := repoRoot(t)
	scriptSrc, err := os.ReadFile(filepath.Join(root, "scripts", "build-release.sh"))
	if err != nil {
		t.Fatalf("read source script: %v", err)
	}
	tmp := t.TempDir()
	dstScript := filepath.Join(tmp, "scripts", "build-release.sh")
	if err := os.MkdirAll(filepath.Dir(dstScript), 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	if err := os.WriteFile(dstScript, scriptSrc, 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	// Lay down the minimum `web/` tree the script's F1 guard will
	// inspect. Only committed files are copied; node_modules is
	// intentionally NOT seeded because the F1 tests run with
	// SKIP_WEB=1.
	mustCopyFromRepo(t, root, "web/package.json", filepath.Join(tmp, "web", "package.json"))
	mustCopyFromRepo(t, root, "web/vite.config.ts", filepath.Join(tmp, "web", "vite.config.ts"))
	mustCopyFromRepo(t, root, "web/tsconfig.json", filepath.Join(tmp, "web", "tsconfig.json"))

	cmd := exec.Command("bash", dstScript)
	cmd.Dir = tmp
	env := append(os.Environ(),
		"OUT_DIR="+outDir,
		"SKIP_WEB=1",
	)
	env = append(env, extraEnv...)
	cmd.Env = env
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err = cmd.Run()
	exit := 0
	if ee, ok := err.(*exec.ExitError); ok {
		exit = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("script exec errored unexpectedly: %v\noutput:\n%s", err, buf.String())
	}
	return buf.String(), exit
}

func mustCopyFromRepo(t *testing.T, root, srcRel, dst string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, srcRel))
	if err != nil {
		t.Fatalf("read %s: %v", srcRel, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(dst), err)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}

// F1 — first protection layer: OUT_DIR that resolves to $ROOT must be
// rejected with a non-zero exit and a clear error. The script must
// not have written any artifact and the source tree under $ROOT must
// be untouched.
func TestBuildReleaseRejectsOUTDirEqualToRoot(t *testing.T) {
	root := t.TempDir()
	copyMinimumWebTree(t, root)
	copyMinimumGoModule(t, root)

	out, exit := runIsolatedScriptWithRoot(t, root, root)
	if exit == 0 {
		t.Fatalf("OUT_DIR=$ROOT was accepted (exit 0). Output:\n%s", out)
	}
	if !strings.Contains(out, "OUT_DIR") {
		t.Fatalf("rejection error does not name OUT_DIR. Output:\n%s", out)
	}
	if !strings.Contains(out, "ROOT") && !strings.Contains(out, "source") && !strings.Contains(out, "overlap") && !strings.Contains(out, "refus") {
		t.Fatalf("rejection error lacks overlap/refuse/source wording. Output:\n%s", out)
	}
	assertWebSourceTreeIntact(t, root, out)
}

// F1 — second protection layer: OUT_DIR that falls UNDER $ROOT (a
// subdirectory of the source tree) must also be rejected, for the
// same reason.
func TestBuildReleaseRejectsOUTDirUnderRoot(t *testing.T) {
	root := t.TempDir()
	copyMinimumWebTree(t, root)
	copyMinimumGoModule(t, root)
	outDir := filepath.Join(root, "build-output")

	out, exit := runIsolatedScriptWithRoot(t, root, outDir)
	if exit == 0 {
		t.Fatalf("OUT_DIR under $ROOT was accepted (exit 0). Output:\n%s", out)
	}
	assertWebSourceTreeIntact(t, root, out)
}

// F1 — third protection layer: even when OUT_DIR is a safe sibling,
// the script must not execute the blanket `rm -rf "$OUT_DIR/web"`
// that would erase a sibling `web/` directory the user happens to
// keep there. The script only needs to overwrite
// `$OUT_DIR/web/dist`, so the destructive line should be scoped to
// that target. This test stages a sentinel file in a pre-existing
// `$OUT_DIR/web/` directory; if the script deletes that sentinel,
// the blanket rm is back.
//
// To exercise the destructive line we cannot use SKIP_WEB=1 (the
// line is gated on the web-build branch). We stage a pre-built
// `$ROOT/web/dist/` and a `$ROOT/web/node_modules/` marker so the
// script skips the `npm install` and `npm run build` calls but still
// reaches the rm/cp section. If `npm` is missing, the script logs a
// warning and skips the web build entirely; this test therefore
// stubs `npm` in the PATH via a temp bin dir.
func TestBuildReleaseDoesNotBlanketDeleteOUTDirWeb(t *testing.T) {
	root := t.TempDir()
	copyMinimumWebTree(t, root)
	copyMinimumGoModule(t, root)
	stagePrebuiltWebDist(t, root)
	stageFakeNpm(t, root)

	outDir := t.TempDir() + "-sibling"
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir outDir: %v", err)
	}
	sentinel := filepath.Join(outDir, "web", "user-data.txt")
	if err := os.MkdirAll(filepath.Dir(sentinel), 0o755); err != nil {
		t.Fatalf("mkdir sentinel dir: %v", err)
	}
	if err := os.WriteFile(sentinel, []byte("user data — must survive"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	pathWithFakeNpm := filepath.Join(root, "fake-bin") + string(os.PathListSeparator) + os.Getenv("PATH")
	_, _ = runIsolatedScriptWithRoot(t, root, outDir, "SKIP_WEB=0", "PATH="+pathWithFakeNpm)

	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("sentinel %s was deleted by the build script; blanket rm -rf $OUT_DIR/web is back. err=%v", sentinel, err)
	}
}

// F2 — first protection: the script must invoke `npm ci` (frozen
// install against web/package-lock.json), not `npm install`. Static
// text inspection of the script is sufficient and is what the
// round-1 reviewer asked for.
func TestBuildReleaseUsesNpmCiNotNpmInstall(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "scripts", "build-release.sh"))
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	script := stripShellComments(string(b))

	if !strings.Contains(script, "npm ci") {
		t.Fatalf("scripts/build-release.sh does not call `npm ci`.\nThe frozen-install contract is broken. Script (comments stripped):\n%s", script)
	}
	// `npm install` (with whitespace) is the destructive variant. The
	// script may still mention `npm install` inside a comment / help
	// text, but the body of the script must not call it.
	lines := strings.Split(script, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		// A line that contains `npm install` AND is not a `npm ci`
		// variant is a regression. We allow the literal text to
		// appear inside shell comments (which we already stripped) or
		// inside a quoted string used for an echo; stripShellComments
		// handles the former. We still allow `npm install` in a doc
		// comment INSIDE a quoted echo statement, but the script
		// currently has no such occurrence. If a future change adds
		// one, the assertion must be revisited.
		if strings.Contains(trimmed, "npm install") && !strings.Contains(trimmed, "npm ci") {
			t.Fatalf("scripts/build-release.sh line %d still calls `npm install`:\n  %s\nThe frozen-install contract requires `npm ci`.", i+1, trimmed)
		}
	}
}

// F2 — second protection: the repo must carry web/package-lock.json
// (so `npm ci` has something to install from) and must NOT carry
// web/pnpm-lock.yaml (a stale conflicting lockfile would let a future
// `pnpm install` re-resolve `latest` and break reproducibility).
func TestBuildReleaseLockfileStoryIsConsistent(t *testing.T) {
	root := repoRoot(t)

	plPath := filepath.Join(root, "web", "package-lock.json")
	if _, err := os.Stat(plPath); err != nil {
		t.Fatalf("web/package-lock.json missing; `npm ci` cannot run. err=%v", err)
	}
	plData, err := os.ReadFile(plPath)
	if err != nil {
		t.Fatalf("read web/package-lock.json: %v", err)
	}
	if !strings.Contains(string(plData), `"lockfileVersion"`) {
		t.Fatalf("web/package-lock.json is not a valid lockfile (missing lockfileVersion).")
	}

	ylPath := filepath.Join(root, "web", "pnpm-lock.yaml")
	if _, err := os.Stat(ylPath); err == nil {
		t.Fatalf("web/pnpm-lock.yaml still present alongside web/package-lock.json; pick one lockfile to avoid silent drift.")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat web/pnpm-lock.yaml: %v", err)
	}
}

// runIsolatedScriptWithRoot is the lower-level runner used by the F1
// tests; it places a fresh copy of the script in `root/scripts/` so
// the script's auto-derived $ROOT is exactly `root`.
func runIsolatedScriptWithRoot(t *testing.T, root, outDir string, extraEnv ...string) (string, int) {
	t.Helper()
	repoRoot := repoRoot(t)
	scriptSrc, err := os.ReadFile(filepath.Join(repoRoot, "scripts", "build-release.sh"))
	if err != nil {
		t.Fatalf("read source script: %v", err)
	}
	dstScript := filepath.Join(root, "scripts", "build-release.sh")
	if err := os.MkdirAll(filepath.Dir(dstScript), 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	if err := os.WriteFile(dstScript, scriptSrc, 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	cmd := exec.Command("bash", dstScript)
	cmd.Dir = root
	env := append(os.Environ(),
		"OUT_DIR="+outDir,
		"SKIP_WEB=1",
	)
	env = append(env, extraEnv...)
	cmd.Env = env
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err = cmd.Run()
	exit := 0
	if ee, ok := err.(*exec.ExitError); ok {
		exit = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("script exec errored unexpectedly: %v\noutput:\n%s", err, buf.String())
	}
	return buf.String(), exit
}

func copyMinimumWebTree(t *testing.T, root string) {
	t.Helper()
	repo := repoRoot(t)
	for _, rel := range []string{"web/package.json", "web/vite.config.ts", "web/tsconfig.json"} {
		mustCopyFromRepo(t, repo, rel, filepath.Join(root, rel))
	}
}

// copyMinimumGoModule stages a minimal Go module under `root` so the
// build script's `go build ./cmd/symphony` succeeds. The code is a
// throwaway `package main` with an empty `main()` — the only thing
// the script needs is a successful `go build` exit so it reaches
// the dangerous `rm -rf` line that the F1 tests are guarding.
func copyMinimumGoModule(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "cmd", "symphony"), 0o755); err != nil {
		t.Fatalf("mkdir cmd/symphony: %v", err)
	}
	mainSrc := []byte("package main\n\nfunc main() {}\n")
	if err := os.WriteFile(filepath.Join(root, "cmd", "symphony", "main.go"), mainSrc, 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	gomod := []byte("module example.com/testbuild\n\ngo 1.23\n")
	if err := os.WriteFile(filepath.Join(root, "go.mod"), gomod, 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
}

// stagePrebuiltWebDist creates a `web/dist/index.html` and
// `web/node_modules/` marker so the build script believes the npm
// install / build steps have already been done. This lets the F1
// test reach the `rm -rf $OUT_DIR/web` line without actually running
// npm.
func stagePrebuiltWebDist(t *testing.T, root string) {
	t.Helper()
	distDir := filepath.Join(root, "web", "dist")
	if err := os.MkdirAll(distDir, 0o755); err != nil {
		t.Fatalf("mkdir web/dist: %v", err)
	}
	if err := os.WriteFile(filepath.Join(distDir, "index.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatalf("write web/dist/index.html: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "web", "node_modules"), 0o755); err != nil {
		t.Fatalf("mkdir web/node_modules: %v", err)
	}
}

// stageFakeNpm creates a `fake-bin/npm` that responds to
// `npm run build` by exit 0. The script's check `command -v npm`
// then succeeds, and the `if [ ! -d "$ROOT/web/node_modules" ]` short
// branch is bypassed because the helper above already seeded
// `node_modules`. The destructive `rm -rf $OUT_DIR/web` is reached
// and that's what this test guards.
func stageFakeNpm(t *testing.T, root string) {
	t.Helper()
	binDir := filepath.Join(root, "fake-bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir fake-bin: %v", err)
	}
	// A shell stub that mimics `npm` enough to keep the script
	// happy: `npm run build` exits 0.
	stub := "#!/usr/bin/env bash\nexit 0\n"
	if err := os.WriteFile(filepath.Join(binDir, "npm"), []byte(stub), 0o755); err != nil {
		t.Fatalf("write fake npm: %v", err)
	}
}

func assertWebSourceTreeIntact(t *testing.T, root, scriptOut string) {
	t.Helper()
	required := []string{
		filepath.Join(root, "web", "package.json"),
		filepath.Join(root, "web", "vite.config.ts"),
		filepath.Join(root, "web", "tsconfig.json"),
	}
	for _, p := range required {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("committed source file missing after rejected run: %s (err=%v)\nScript output:\n%s", p, err, scriptOut)
		}
	}
}

// stripShellComments removes `# …` line comments and inline `# …`
// comments outside of single-quoted strings. It is good enough for
// grep-style assertions on the build script, which has no nested
// quoting or escapes.
func stripShellComments(src string) string {
	var b strings.Builder
	inSingle := false
	inDouble := false
	for i := 0; i < len(src); i++ {
		c := src[i]
		if c == '\'' && !inDouble {
			inSingle = !inSingle
			b.WriteByte(c)
			continue
		}
		if c == '"' && !inSingle {
			inDouble = !inDouble
			b.WriteByte(c)
			continue
		}
		if c == '#' && !inSingle && !inDouble {
			for i < len(src) && src[i] != '\n' {
				i++
			}
			if i < len(src) {
				b.WriteByte('\n')
			}
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}
