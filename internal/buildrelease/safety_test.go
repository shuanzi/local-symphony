// Package buildrelease contains tests that lock in the safety guarantees
// of scripts/build-release.sh as required by the D5 codex review
// rounds 1 + 2:
//
//	F1-r0 [P1] (R1)  scripts/build-release.sh must not data-destructively
//	                 delete the repo's `web/` source tree. Two
//	                 protections: (a) early-exit guard rejecting
//	                 OUT_DIR that equals or falls under a dangerous
//	                 source subdir of $ROOT; (b) the destructive
//	                 `rm -rf` is scoped to $OUT_DIR/web/dist.
//
//	F2-r0 [P2] (R1)  web install must be deterministic — frozen install
//	                 against the committed web/package-lock.json. The
//	                 script must invoke `npm ci` (not `npm install`).
//
//	F1-r2 [P1] (R2)  The R1 guard overreached by rejecting "every child
//	                 of $ROOT" — the documented default
//	                 `bash scripts/build-release.sh` uses
//	                 OUT_DIR=$ROOT/dist, which is a legitimate
//	                 output location. The guard must allow $ROOT/dist
//	                 and reject only source subdirs (web/,
//	                 scripts/, internal/, cmd/, docs/, schemas/,
//	                 api/, etc.).
//
//	F2-r2 [P1] (R2)  R1's "use npm ci" fix is meaningless if
//	                 web/package-lock.json is excluded by .gitignore
//	                 — a clean checkout / git archive / CI will not
//	                 have the lockfile, and `npm ci` fails
//	                 immediately. The lockfile must be tracked.
//
//	F3-r2 [P2] (R2)  The R1 `if [ ! -d $ROOT/web/node_modules ]` guard
//	                 skips `npm ci` when node_modules already
//	                 exists. That means a developer's stale
//	                 node_modules (from a previous `pnpm install` or
//	                 a `latest`-resolved `npm install`) gets
//	                 packaged into the release. `npm ci` must run
//	                 unconditionally.
//
//	PR23-P2-1 [P2]   The pre-guard `mkdir -p "$OUT_DIR"` runs
//	                 BEFORE the overlap check. When the caller
//	                 points OUT_DIR at a rejected path
//	                 (e.g. $ROOT/web/forbidden), the directory
//	                 is created on disk before the guard
//	                 rejects the run. The guard is supposed
//	                 to be fail-closed; creating rejected
//	                 paths is a side effect that violates
//	                 that contract. Fix: defer `mkdir -p`
//	                 until after the overlap check accepts
//	                 the target.
//
// Tests in this package exercise the script in two modes:
//
//   - **In-isolated-tempdir tests (F1-r0)**: the script is COPIED
//     to a fresh tmp workdir so the script's auto-derived $ROOT is
//     the tmp dir, never the real repo.
//
//   - **In-clean-git-archive tests (F2-r2)**: `git archive HEAD` is
//     extracted to a t.TempDir() so the test sees exactly what a CI
//     fresh checkout would see. This catches a class of bugs where
//     the developer's working tree has untracked-but-on-disk state
//     that masks a missing-in-source-tree problem.
package buildrelease

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

// F1 — second protection layer: OUT_DIR that falls inside a real
// source subdirectory of $ROOT (the list the R2 guard enumerates)
// must be rejected. The R2 guard narrowed the rejection list to
// specific source subdirs (web/, scripts/, internal/, cmd/, docs/,
// schemas/, api/, examples/, tests/, db/) so the documented default
// $ROOT/dist still works. A path under $ROOT/web is the canonical
// dangerous case the R1 guard was trying to catch.
func TestBuildReleaseRejectsOUTDirUnderRoot(t *testing.T) {
	root := t.TempDir()
	copyMinimumWebTree(t, root)
	copyMinimumGoModule(t, root)
	outDir := filepath.Join(root, "web", "build-output") // under $ROOT/web

	out, exit := runIsolatedScriptWithRoot(t, root, outDir)
	if exit == 0 {
		t.Fatalf("OUT_DIR under $ROOT/web was accepted (exit 0). Output:\n%s", out)
	}
	if !strings.Contains(out, "refusing to run") {
		t.Fatalf("rejection error missing. Output:\n%s", out)
	}
	assertWebSourceTreeIntact(t, root, out)
}

// PR23-P2-1 — fail-closed guard must not leave filesystem traces
// behind. The R1 guard rejects OUT_DIR that overlaps a source
// subdirectory of $ROOT, but the script's pre-guard
// `mkdir -p "$OUT_DIR"` (used so `cd "$OUT_DIR" && pwd -P` can
// resolve the path) created the rejected directory on disk BEFORE
// the rejection ran. A fail-closed guard that mutates the
// filesystem as a side effect of being invoked is not actually
// fail-closed. This test asserts that pointing OUT_DIR at
// $ROOT/web/<forbidden-subdir> leaves NO trace of `<forbidden-subdir>`
// under $ROOT/web after the script exits with the rejection error.
func TestBuildReleaseMkdirDoesNotCreateRejectedOutDir(t *testing.T) {
	root := t.TempDir()
	copyMinimumWebTree(t, root)
	copyMinimumGoModule(t, root)
	forbidden := filepath.Join(root, "web", "forbidden-subdir")

	// Sanity check: the forbidden path must NOT exist before the run.
	if _, err := os.Stat(forbidden); err == nil {
		t.Fatalf("precondition violated: %s already exists", forbidden)
	}

	out, exit := runIsolatedScriptWithRoot(t, root, forbidden)
	if exit == 0 {
		t.Fatalf("OUT_DIR=$ROOT/web/forbidden-subdir was accepted (exit 0). Output:\n%s", out)
	}
	if !strings.Contains(out, "refusing to run") {
		t.Fatalf("rejection error missing. Output:\n%s", out)
	}

	// The whole point of the fix: the rejected target path must
	// NOT have been created on disk as a side effect of the
	// guard's `mkdir -p` line running before the overlap check.
	if _, err := os.Stat(forbidden); err == nil {
		t.Fatalf("fail-closed guard is not actually fail-closed: %s was created on disk by the pre-guard `mkdir -p` BEFORE the overlap check rejected the path. Script output:\n%s", forbidden, out)
	} else if !os.IsNotExist(err) {
		t.Fatalf("unexpected stat error on %s: %v", forbidden, err)
	}

	// And the parent ($ROOT/web) must also NOT have grown a
	// forbidden-subdir entry.
	entries, err := os.ReadDir(filepath.Join(root, "web"))
	if err != nil {
		t.Fatalf("readdir $ROOT/web: %v", err)
	}
	for _, e := range entries {
		if e.Name() == "forbidden-subdir" {
			t.Fatalf("$ROOT/web contains a `forbidden-subdir` entry after the rejected run. ls -la output:\n%s", strings.Join(dirListing(t, filepath.Join(root, "web")), "\n"))
		}
	}
}

// dirListing returns a human-readable listing of dir for failure
// messages. Uses `ls -la` when available; falls back to ReadDir
// names if not.
func dirListing(t *testing.T, dir string) []string {
	t.Helper()
	cmd := exec.Command("ls", "-la", dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		es, _ := os.ReadDir(dir)
		names := []string{}
		for _, e := range es {
			names = append(names, e.Name())
		}
		return names
	}
	return strings.Split(strings.TrimRight(string(out), "\n"), "\n")
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

// F1-r2 — regression test: the documented default command
// `bash scripts/build-release.sh` resolves OUT_DIR to $ROOT/dist
// (see the `OUT_DIR="${OUT_DIR:-$ROOT/dist}"` line at the top of
// the script). R1's guard rejected this default. R2 narrows the
// guard so $ROOT/dist is allowed.
//
// To run the script in this mode we copy it into a fresh tmp
// workdir so the script's auto-derived $ROOT is the tmp dir, and
// we set OUT_DIR explicitly to the tmp-dir/dist path. The test
// asserts the script does NOT exit 2 with the "refusing to run"
// message that R1 emitted.
func TestBuildReleaseAcceptsDefaultOUTDir(t *testing.T) {
	root := t.TempDir()
	copyMinimumWebTree(t, root)
	copyMinimumGoModule(t, root)
	outDir := filepath.Join(root, "dist") // mirrors $ROOT/dist

	out, exit := runIsolatedScriptWithRoot(t, root, outDir)
	if exit != 0 {
		t.Fatalf("OUT_DIR=$ROOT/dist (the documented default) was rejected. exit=%d\nOutput:\n%s\nThe R1 guard overreached; the F1-r2 fix should narrow it to source subdirs only.", exit, out)
	}
	if strings.Contains(out, "refusing to run") {
		t.Fatalf("OUT_DIR=$ROOT/dist triggered the R1 refusal message. Output:\n%s", out)
	}
}

// F3-r2 — regression test: even when $ROOT/web/node_modules
// already exists (developer has run a previous install), the
// script must still invoke `npm ci` so the on-disk dependencies
// match the committed lockfile exactly. We verify by stubbing
// `npm` with a logging stub that records its invocations to a
// file; if `npm ci` is not in the call log, the F3-r2 bug is back.
func TestBuildReleaseAlwaysRunsNpmCi(t *testing.T) {
	root := t.TempDir()
	copyMinimumWebTree(t, root)
	copyMinimumGoModule(t, root)
	stagePrebuiltWebDist(t, root)
	// Stage node_modules so the (broken) R1 `if [ ! -d ... ]` guard
	// would have skipped npm ci.
	if err := os.MkdirAll(filepath.Join(root, "web", "node_modules"), 0o755); err != nil {
		t.Fatalf("mkdir web/node_modules: %v", err)
	}
	// Stub npm with a logging shim. We don't actually need
	// network; we only need to observe that `npm ci` is called.
	binDir := filepath.Join(root, "fake-bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir fake-bin: %v", err)
	}
	logPath := filepath.Join(root, "npm-invocations.log")
	stub := "#!/usr/bin/env bash\necho \"$@\" >> \"" + logPath + "\"\nexit 0\n"
	if err := os.WriteFile(filepath.Join(binDir, "npm"), []byte(stub), 0o755); err != nil {
		t.Fatalf("write fake npm: %v", err)
	}
	outDir := t.TempDir() + "-sibling"
	pathWithFakeNpm := binDir + string(os.PathListSeparator) + os.Getenv("PATH")
	_, _ = runIsolatedScriptWithRoot(t, root, outDir, "SKIP_WEB=0", "PATH="+pathWithFakeNpm)

	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read npm invocation log: %v", err)
	}
	log := string(logData)
	// `npm ci` may be called bare, as `npm ci --no-audit --no-fund`,
	// or as part of a longer command. We assert the log contains
	// the literal token `ci` *as a separate argv* (preceded by a
	// space or at start of line). `npm run build` is also called
	// and would log `run build` — that's not what we want.
	lines := strings.Split(strings.TrimSpace(log), "\n")
	calledCi := false
	for _, line := range lines {
		// We look for an invocation that has `ci` as a standalone
		// argument (the second token after `npm`).
		fields := strings.Fields(line)
		if len(fields) >= 1 && fields[0] == "ci" {
			calledCi = true
			break
		}
	}
	if !calledCi {
		t.Fatalf("`npm ci` was not called even though web/node_modules exists. The F3-r2 `if [ ! -d ... ]` guard is back. Invocation log:\n%s\nFull script output:\n%s", log, log)
	}
}

func TestBuildReleaseNpmCiIncludesDevDependencies(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "scripts", "build-release.sh"))
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	script := stripShellComments(string(b))

	for _, line := range strings.Split(script, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		for i := 0; i+1 < len(fields); i++ {
			if fields[i] == "npm" && fields[i+1] == "ci" {
				for _, field := range fields[i+2:] {
					if field == "--include=dev" {
						return
					}
				}
				t.Fatalf("scripts/build-release.sh calls npm ci without --include=dev:\n  %s\nNODE_ENV=production would omit Vite/TypeScript dev dependencies.", strings.TrimSpace(line))
			}
		}
	}
	t.Fatalf("scripts/build-release.sh does not call npm ci")
}

func TestBuildReleaseFailsEarlyForCGOCrossCompileWithoutCrossCompiler(t *testing.T) {
	root := t.TempDir()
	copyMinimumWebTree(t, root)
	copyMinimumGoModule(t, root)
	binDir := filepath.Join(root, "fake-bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir fake-bin: %v", err)
	}
	logPath := filepath.Join(root, "go-invocations.log")
	stub := fmt.Sprintf(`#!/usr/bin/env bash
echo "$@" >> %q
if [ "$1" = "env" ]; then
  shift
  for key in "$@"; do
    case "$key" in
      GOHOSTOS) echo %q ;;
      GOHOSTARCH) echo %q ;;
      *) echo "" ;;
    esac
  done
  exit 0
fi
exit 99
`, logPath, runtime.GOOS, runtime.GOARCH)
	if err := os.WriteFile(filepath.Join(binDir, "go"), []byte(stub), 0o755); err != nil {
		t.Fatalf("write fake go: %v", err)
	}
	targetOS, targetArch := nonHostTarget()
	out, exit := runIsolatedScriptWithRoot(t, root, filepath.Join(root, "dist"),
		"GOOS="+targetOS,
		"GOARCH="+targetArch,
		"CGO_ENABLED=1",
		"CC=",
		"CC_FOR_TARGET=",
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)

	if exit != 2 {
		t.Fatalf("CGO cross compile without CC/CC_FOR_TARGET should fail early with exit 2, got %d.\nOutput:\n%s", exit, out)
	}
	if !strings.Contains(out, "CC_FOR_TARGET") || !strings.Contains(out, "CGO") {
		t.Fatalf("early failure should explain the missing cross C compiler. Output:\n%s", out)
	}
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read go invocation log: %v", err)
	}
	if strings.Contains(string(logData), "build") {
		t.Fatalf("script invoked go build instead of failing before compilation. go log:\n%s\nOutput:\n%s", string(logData), out)
	}
}

func TestBuildReleaseMapsCCForTargetToCCForCGOCrossCompile(t *testing.T) {
	root := t.TempDir()
	copyMinimumWebTree(t, root)
	copyMinimumGoModule(t, root)
	binDir := filepath.Join(root, "fake-bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir fake-bin: %v", err)
	}
	logPath := filepath.Join(root, "go-invocations.log")
	stub := fmt.Sprintf(`#!/usr/bin/env bash
echo "$@ CC=${CC:-}" >> %q
if [ "$1" = "env" ]; then
  shift
  for key in "$@"; do
    case "$key" in
      GOHOSTOS) echo %q ;;
      GOHOSTARCH) echo %q ;;
      *) echo "" ;;
    esac
  done
  exit 0
fi
exit 0
`, logPath, runtime.GOOS, runtime.GOARCH)
	if err := os.WriteFile(filepath.Join(binDir, "go"), []byte(stub), 0o755); err != nil {
		t.Fatalf("write fake go: %v", err)
	}
	targetOS, targetArch := nonHostTarget()
	out, exit := runIsolatedScriptWithRoot(t, root, filepath.Join(root, "dist"),
		"GOOS="+targetOS,
		"GOARCH="+targetArch,
		"CGO_ENABLED=1",
		"CC=",
		"CC_FOR_TARGET=target-gcc",
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)

	if exit != 0 {
		t.Fatalf("CGO cross compile with CC_FOR_TARGET should reach go build, got exit %d.\nOutput:\n%s", exit, out)
	}
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read go invocation log: %v", err)
	}
	log := string(logData)
	if !strings.Contains(log, "build") {
		t.Fatalf("script did not invoke go build. go log:\n%s\nOutput:\n%s", log, out)
	}
	if !strings.Contains(log, "build -trimpath") || !strings.Contains(log, "CC=target-gcc") {
		t.Fatalf("go build did not receive CC from CC_FOR_TARGET. go log:\n%s\nOutput:\n%s", log, out)
	}
}

// F2-r2 — rewritten to use `git archive` so a developer's stray
// web/package-lock.json on disk cannot mask a missing-in-source
// lockfile. The test extracts `git archive HEAD` to a t.TempDir()
// and verifies that web/package-lock.json is present, web/pnpm-lock.yaml
// is absent, and web/package-lock.json is NOT excluded by .gitignore.
func TestBuildReleaseLockfileStoryIsConsistent(t *testing.T) {
	root := repoRoot(t)

	// (a) .gitignore must NOT exclude web/package-lock.json. R1's
	// mistake was to add it; R2 removes it.
	gi, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	for _, line := range strings.Split(string(gi), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if trimmed == "web/package-lock.json" {
			t.Fatalf(".gitignore still excludes web/package-lock.json; a clean checkout / git archive will not have the lockfile and `npm ci` will fail. Remove that line.")
		}
	}

	// (b) Run the same checks under a `git archive` clean extract
	// so on-disk-only state cannot mask a missing-in-source
	// problem. The `git archive` output is what a CI fresh
	// checkout would have; the developer's stray worktree files
	// are NOT included.
	clean := extractCleanTarball(t, root)
	plPath := filepath.Join(clean, "web", "package-lock.json")
	if _, err := os.Stat(plPath); err != nil {
		t.Fatalf("web/package-lock.json missing from git archive extract at %s. err=%v\nA clean checkout cannot run `npm ci` without a tracked lockfile.", plPath, err)
	}
	plData, err := os.ReadFile(plPath)
	if err != nil {
		t.Fatalf("read web/package-lock.json from clean extract: %v", err)
	}
	if !strings.Contains(string(plData), `"lockfileVersion"`) {
		t.Fatalf("web/package-lock.json in clean extract is not a valid lockfile (missing lockfileVersion).")
	}

	ylPath := filepath.Join(clean, "web", "pnpm-lock.yaml")
	if _, err := os.Stat(ylPath); err == nil {
		t.Fatalf("web/pnpm-lock.yaml present in clean extract; pick one lockfile to avoid silent drift.")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat web/pnpm-lock.yaml in clean extract: %v", err)
	}
}

// F1-r3 — regression: docs/RELEASE_NOTES.md and README.md promise
// Node 18+ LTS as the supported runtime for the dashboard build.
// R2's `npm install --package-lock-only` was run on Node 25 and
// resolved to Vite 8.0.16 / @vitejs/plugin-react 6.0.2, both of
// which require Node `^20.19.0 || >=22.12.0`. A CI that runs the
// documented Node 18 LTS would fail at `npm run build` with
// `ReferenceError: CustomEvent is not defined`.
//
// This test parses web/package-lock.json from a clean `git
// archive` extract and asserts that NO package in the resolved
// dependency tree declares an `engines.node` field that excludes
// Node 18. The supported constraint is
// "Node 18 must be a possible installation target", which
// translates to: any `engines.node` constraint that appears must
// NOT require Node >=20 as a hard floor (e.g. `^20.19.0` or
// `>=22.12.0` or `>=20`).
//
// This is a static lockfile check; the CI clean-room Node 18
// build is exercised by the spec's clean-tarball verification.
func TestBuildReleaseLockfileEnginesCompatibleWithNode18(t *testing.T) {
	root := repoRoot(t)
	clean := extractCleanTarball(t, root)
	plPath := filepath.Join(clean, "web", "package-lock.json")
	plData, err := os.ReadFile(plPath)
	if err != nil {
		t.Fatalf("read web/package-lock.json from clean extract: %v", err)
	}
	// Parse the npm lockfile. The shape is a flat object with a
	// top-level "packages" key whose values are package objects
	// (one per installed package, keyed by install path).
	var lockfile struct {
		Packages map[string]struct {
			Name     string `json:"name"`
			Version  string `json:"version"`
			Engines  any    `json:"engines"`
			HasBin   any    `json:"hasBin"`
			Resolved string `json:"resolved"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(plData, &lockfile); err != nil {
		t.Fatalf("parse web/package-lock.json: %v", err)
	}
	if lockfile.Packages == nil {
		t.Fatalf("web/package-lock.json has no `packages` key; lockfile is malformed")
	}
	// The supported Node 18 contract: any `engines.node` field
	// that appears must either be absent or include at least one
	// semver alternative that permits a Node 18 runtime.
	disallowed := []string{}
	for installPath, pkg := range lockfile.Packages {
		// The empty key (installPath == "") is the workspace root
		// itself; its engines.node field is a top-level package
		// constraint, not a transitive one. We still want to
		// check it because the workspace root's `engines` is
		// what npm actually uses to refuse installs.
		rawEngines, ok := pkg.Engines.(map[string]any)
		if !ok || rawEngines == nil {
			continue
		}
		nodeRaw, ok := rawEngines["node"]
		if !ok {
			continue
		}
		nodeConstraint, ok := nodeRaw.(string)
		if !ok {
			continue
		}
		if !node18Compatible(nodeConstraint) {
			disallowed = append(disallowed, fmt.Sprintf("%s@%s (%s) engines.node=%q", pkg.Name, pkg.Version, installPath, nodeConstraint))
		}
	}
	if len(disallowed) > 0 {
		t.Fatalf("web/package-lock.json contains %d package(s) whose engines.node excludes Node 18 (the documented supported runtime):\n  %s\n\nPin the offending top-level deps in web/package.json to Node-18-compatible versions (Vite ^5.4.x, @vitejs/plugin-react ^4.3.x, React 18) and regenerate the lockfile on Node 18, OR bump docs/RELEASE_NOTES.md to declare the new minimum.", len(disallowed), strings.Join(disallowed, "\n  "))
	}
}

// node18Compatible reports whether a single `engines.node`
// constraint string allows installation on Node 18. The supported
// shapes are:
//
//   - `">=18"` / `">=18.0.0"` (Node 18 is the floor)
//   - `"^18.0.0"` / `"^18"` (Node 18 major only)
//   - `">=16"` / `">=14"` / etc. (Node 18 is above the floor)
//   - `"^18 || >=20"` multi-clause where AT LEAST ONE clause
//     allows Node 18 (npm honours the first matching clause)
//   - `""` (empty) — treated as no constraint
//
// It returns false (incompatible) for shapes that hard-exclude
// Node 18, e.g. `^20`, `>=20`, `>=20.19.0`, `>=22.12.0`,
// `^20.19.0 || >=22.12.0`, etc.
func node18Compatible(constraint string) bool {
	trimmed := strings.TrimSpace(constraint)
	if trimmed == "" {
		return true
	}
	// Split on `||` for multi-clause constraints. AT LEAST ONE
	// clause must be Node-18-compatible for the whole constraint
	// to be (npm applies the first matching clause; a package
	// advertising "^18 || >=20" is installable on Node 18 via the
	// first clause).
	for _, clause := range strings.Split(trimmed, "||") {
		clause = strings.TrimSpace(clause)
		if node18CompatibleClause(clause) {
			return true
		}
	}
	return false
}

func TestNode18CompatibleRejectsRangesThatExcludeNode18(t *testing.T) {
	tests := []struct {
		constraint string
		want       bool
	}{
		{">=18", true},
		{"^18.0.0 || >=20.0.0", true},
		{">=16 <19", true},
		{"<=16", false},
		{"<18", false},
		{">=16 <18", false},
		{"~20", false},
		{"20.0.0", false},
		{"=20.0.0", false},
		{"^20.19.0 || >=22.12.0", false},
	}
	for _, tt := range tests {
		t.Run(tt.constraint, func(t *testing.T) {
			if got := node18Compatible(tt.constraint); got != tt.want {
				t.Fatalf("node18Compatible(%q) = %v, want %v", tt.constraint, got, tt.want)
			}
		})
	}
}

func node18CompatibleClause(clause string) bool {
	clause = strings.TrimSpace(clause)
	if clause == "" {
		return true
	}
	constraints := nodeEngineConstraintTokens(clause)
	if len(constraints) == 0 {
		return true
	}
	for _, constraint := range constraints {
		if !node18ConstraintAllowsNode18(constraint) {
			return false
		}
	}
	return true
}

func nodeEngineConstraintTokens(clause string) []string {
	fields := strings.Fields(clause)
	tokens := make([]string, 0, len(fields))
	for i := 0; i < len(fields); i++ {
		field := fields[i]
		if isComparatorOnly(field) && i+1 < len(fields) {
			tokens = append(tokens, field+fields[i+1])
			i++
			continue
		}
		tokens = append(tokens, field)
	}
	return tokens
}

func isComparatorOnly(s string) bool {
	switch s {
	case ">=", "<=", "==", "!=", ">", "<", "=", "^", "~":
		return true
	default:
		return false
	}
}

func node18ConstraintAllowsNode18(token string) bool {
	comparator, major, ok := parseNodeEngineToken(token)
	if !ok {
		return true
	}
	switch comparator {
	case "", "=", "==":
		return major == 18
	case "!=":
		return true
	case ">=", ">":
		return major <= 18
	case "<":
		return major > 18
	case "<=":
		return major >= 18
	case "^", "~":
		return major == 18
	default:
		return true
	}
}

func parseNodeEngineToken(token string) (string, int, bool) {
	token = strings.TrimSpace(token)
	comparator := ""
	rest := token
	for _, prefix := range []string{">=", "<=", "==", "!=", ">", "<", "=", "^", "~"} {
		if strings.HasPrefix(rest, prefix) {
			comparator = prefix
			rest = strings.TrimSpace(strings.TrimPrefix(rest, prefix))
			break
		}
	}
	if rest == "" {
		return "", 0, false
	}
	if rest[0] == 'v' || rest[0] == 'V' {
		rest = rest[1:]
	}
	major := ""
	for _, c := range rest {
		if c == '.' || c == '-' || c == ' ' || c == 'x' || c == 'X' {
			break
		}
		if c < '0' || c > '9' {
			return "", 0, false
		}
		major += string(c)
	}
	if major == "" {
		return "", 0, false
	}
	return comparator, majorInt(major), true
}

func nonHostTarget() (string, string) {
	if runtime.GOOS != "linux" {
		return "linux", runtime.GOARCH
	}
	if runtime.GOARCH != "arm64" {
		return runtime.GOOS, "arm64"
	}
	return runtime.GOOS, "amd64"
}

func majorInt(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// extractCleanTarball runs `git archive HEAD` from the repo root
// and extracts the tar into a fresh t.TempDir(). The result is
// what a CI fresh checkout of HEAD would have on disk.
//
// If the caller is not inside a git working tree (e.g. the test
// is being run from a clean-tarball extract that itself has no
// .git directory), the function falls back to the in-place
// directory and the test will then read whatever files are
// committed at that path. The clean-tarball end-to-end validation
// in the spec (which does the archive + extract in a single
// script-level command) is the authoritative way to verify
// "what a CI fresh checkout sees".
func extractCleanTarball(t *testing.T, repoRoot string) string {
	t.Helper()
	// Quick check: is there a .git dir we can archive from?
	if _, err := os.Stat(filepath.Join(repoRoot, ".git")); err != nil {
		t.Logf("not in a git worktree (%v); falling back to in-place repoRoot. Run the clean-tarball spec for authoritative CI-equivalent verification.", err)
		return repoRoot
	}
	dir := t.TempDir()
	cmd := exec.Command("git", "archive", "HEAD")
	cmd.Dir = repoRoot
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("git archive HEAD: %v", err)
	}
	untar := exec.Command("tar", "-x", "-C", dir)
	untar.Stdin = &buf
	untar.Stderr = os.Stderr
	if err := untar.Run(); err != nil {
		t.Fatalf("tar -x: %v", err)
	}
	return dir
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
// `pnpm install` re-resolve `latest` and break reproducibility). The
// rewritten TestBuildReleaseLockfileStoryIsConsistent at the top of
// this file is the authoritative version; this block is intentionally
// left empty (it was the R1 version that did not protect against
// developer-stray-lockfile masking).
//
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
