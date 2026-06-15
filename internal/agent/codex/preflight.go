package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"local-symphony/internal/core"
)

// PreflightReason is the canonical, operator-facing reason a Codex
// preflight failed. It maps onto the `details["reason"]` emitted by
// SelectFixtureMetadata and the legacy PreflightFixtureGate so the
// diagnostics surface can be stable across adapter versions.
//
// These values appear in diagnostics.codex.last_preflight.failure_reason
// and in the codex.warning field on the same object. They are redacted
// of any raw Codex log / raw prompt / raw secret content; failure
// messages and metadata are scrubbed of synthetic sentinels in tests.
type PreflightReason string

const (
	ReasonMalformedVersion       PreflightReason = "malformed_version"
	ReasonMissingFixture         PreflightReason = "missing_fixture"
	ReasonMissingMetadata        PreflightReason = "missing_metadata"
	ReasonMalformedMetadata      PreflightReason = "malformed_metadata"
	ReasonVersionMismatch        PreflightReason = "metadata_codex_version_mismatch"
	ReasonExperimentalNotSupport PreflightReason = "experimental_api_not_supported"
	ReasonMetadataMissingCodex   PreflightReason = "metadata_missing_codex_version"
	ReasonMetadataMissingProto   PreflightReason = "metadata_missing_protocol_version"
	ReasonMetadataMissingSchema  PreflightReason = "metadata_missing_schema_version"
	ReasonMetadataMissingNotes   PreflightReason = "metadata_missing_supported_notifications"
	ReasonMetadataMissingReqs    PreflightReason = "metadata_missing_supported_requests"
	ReasonMetadataProtoSentinel  PreflightReason = "metadata_protocol_version_sentinel"
	ReasonMetadataSchemaSentinel PreflightReason = "metadata_schema_version_sentinel"
	ReasonMetadataCodexSentinel  PreflightReason = "metadata_codex_version_sentinel"
	ReasonMissingSchemaFixture   PreflightReason = "missing_schema_fixture"
	ReasonMissingTranscript      PreflightReason = "missing_transcript_fixture"
	ReasonInvalidTranscript      PreflightReason = "invalid_transcript_fixture"
	ReasonCodexNotInstalled      PreflightReason = "codex_not_installed"
	ReasonUnknown                PreflightReason = "unknown"
)

// AllPreflightReasons returns every defined PreflightReason value.
// The diagnostics layer (and contract validator) needs the full enum
// to validate the `codex.last_preflight.failure_reason` field. The
// slice is returned in declaration order so the schema enum
// stays stable.
func AllPreflightReasons() []PreflightReason {
	return []PreflightReason{
		ReasonMalformedVersion,
		ReasonMissingFixture,
		ReasonMissingMetadata,
		ReasonMalformedMetadata,
		ReasonVersionMismatch,
		ReasonExperimentalNotSupport,
		ReasonMetadataMissingCodex,
		ReasonMetadataMissingProto,
		ReasonMetadataMissingSchema,
		ReasonMetadataMissingNotes,
		ReasonMetadataMissingReqs,
		ReasonMetadataProtoSentinel,
		ReasonMetadataSchemaSentinel,
		ReasonMetadataCodexSentinel,
		ReasonMissingSchemaFixture,
		ReasonMissingTranscript,
		ReasonInvalidTranscript,
		ReasonCodexNotInstalled,
		ReasonUnknown,
	}
}

// CodexSupportStatus is the three-state support status used in
// diagnostics.codex.support. It mirrors the existing diagnostics
// schema's `supportStatus` enum (supported / unsupported / unknown).
type CodexSupportStatus string

const (
	SupportStatusSupported   CodexSupportStatus = "supported"
	SupportStatusUnsupported CodexSupportStatus = "unsupported"
	SupportStatusUnknown     CodexSupportStatus = "unknown"
)

// CodexSupport captures the cli / model / sandbox support surface.
// It matches the diagnostics `codexSupport` sub-object one-to-one.
type CodexSupport struct {
	CLI     CodexSupportStatus `json:"cli"`
	Model   CodexSupportStatus `json:"model"`
	Sandbox CodexSupportStatus `json:"sandbox"`
}

// FixtureSupport reports which on-disk Codex fixture artifacts exist
// for the detected version. The fields are independent: a release may
// ship schema + metadata without a transcript, or vice versa.
type FixtureSupport struct {
	SchemaAvailable     bool `json:"schema_available"`
	MetadataAvailable   bool `json:"metadata_available"`
	TranscriptAvailable bool `json:"transcript_available"`
}

// PreflightOptions tunes RunPreflight. Zero values mean "use the
// installed codex command and the discoverable fixture root". Tests
// pass explicit VersionOutput / FixtureRoot to make preflight
// deterministic and offline.
type PreflightOptions struct {
	Command         string
	VersionOutput   string
	FixtureRoot     string
	ExperimentalAPI bool
	Now             func() time.Time
}

// PreflightSummary is the diagnostics-shaped result of a single
// preflight. It is captured at the moment of the call; no historical
// data is retained by the adapter. Diagnostics code merges the
// summary into the `codex` field on the diagnostics envelope.
type PreflightSummary struct {
	RanAt          string                 `json:"ran_at"`
	Command        string                 `json:"command"`
	Version        string                 `json:"version"`
	Available      bool                   `json:"available"`
	Support        CodexSupport           `json:"support"`
	Metadata       *CompatibilityMetadata `json:"metadata,omitempty"`
	FixtureSupport FixtureSupport         `json:"fixture_support"`
	FailureCode    string                 `json:"failure_code,omitempty"`
	FailureReason  PreflightReason        `json:"failure_reason,omitempty"`
	FailureMessage string                 `json:"failure_message,omitempty"`
	FailureDetails map[string]any         `json:"failure_details,omitempty"`
}

// RunPreflight runs the Codex preflight in a single, side-effect-free
// pass. It does NOT spawn the codex app-server process group; it only
// resolves the installed version, walks the fixture tree, and returns
// a stable summary suitable for diagnostics / `symphony status`.
//
// On success: Available=true, Support.{CLI,Model,Sandbox}=supported,
// Metadata is populated, FixtureSupport reports which fixture files
// were located. On failure: Available=false, Support.CLI=unsupported,
// Support.{Model,Sandbox}=unknown, FailureCode=unsupported_codex_version,
// FailureReason=... (one of the PreflightReason constants),
// FailureMessage is the human-readable cause scrubbed of any raw
// fixture content. FailureDetails carries structured fields
// (codex_version / detected_version / metadata_version) but is
// redacted of raw codex log / raw prompt / raw secret content.
//
// Redaction invariants (P1 round-1 review):
//   - The stored `Command` is the binary basename only; arguments
//     (which may carry `--api-key=...` / wrapper flags / tokens) are
//     dropped before assignment. The basename is also passed through
//     `scrubbedForDiagnostics` as defense in depth.
//   - The stored `Metadata.SupportedNotifications` / `SupportedRequests`
//     are scrubbed of synthetic-sentinel-shaped values so a poisoned
//     compatibility.json cannot leak raw prompt / raw Codex log /
//     raw secret content via the success path.
//   - `FailureMessage` is scrubbed before assignment so attacker-
//     controlled detail text (e.g. a poisoned metadata_version or
//     a transcript-validation error message) cannot leak through the
//     human-readable string.
//   - When the version probe returns empty output the preflight
//     uses `exec.LookPath` to distinguish `codex_not_installed` from
//     `malformed_version`; previously, an installed-but-malformed
//     codex binary was misreported as not-installed.
func RunPreflight(opts PreflightOptions) PreflightSummary {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	command := strings.TrimSpace(opts.Command)
	if command == "" {
		command = "codex app-server"
	}
	versionOutput := opts.VersionOutput
	if versionOutput == "" {
		// SYMPHONY_CODEX_VERSION_OUTPUT is the test-only override
		// for `codex --version`. When unset, the preflight calls
		// DetectVersionForCommand (which may invoke the real codex
		// binary). Setting the env var makes the preflight fully
		// deterministic in offline / sandboxed test environments.
		if override := strings.TrimSpace(os.Getenv("SYMPHONY_CODEX_VERSION_OUTPUT")); override != "" {
			versionOutput = override
		} else {
			versionOutput = DetectVersionForCommand(command)
		}
	}
	ranAt := now().UTC().Format(time.RFC3339)
	summary := PreflightSummary{
		RanAt:   ranAt,
		Command: redactedCommandForDiagnostics(command),
		Version: "",
		Support: CodexSupport{CLI: SupportStatusUnknown, Model: SupportStatusUnknown, Sandbox: SupportStatusUnknown},
		FixtureSupport: FixtureSupport{
			SchemaAvailable:     false,
			MetadataAvailable:   false,
			TranscriptAvailable: false,
		},
	}
	parsedVersion, parseErr := ParseCodexVersionOutput(versionOutput)
	if parseErr != nil {
		// P2 round-1 fix: distinguish "binary not installed" from
		// "binary ran but emitted a malformed --version line". The
		// previous version conflated the two cases and misreported
		// installed-but-malformed codex binaries as missing. The
		// version probe result is "" only when `DetectVersionForCommand`
		// could not find or invoke the binary; in that case we trust
		// the absence as the not-installed signal. When the override
		// env var is set (test-only) and still empty, fall back to
		// exec.LookPath for a stable signal.
		reason := classifyEmptyOrMalformedVersion(versionOutput, command)
		summary.Support = CodexSupport{CLI: SupportStatusUnsupported, Model: SupportStatusUnknown, Sandbox: SupportStatusUnknown}
		summary.FailureCode = string(core.FailureUnsupportedCodexVersion)
		summary.FailureReason = reason
		summary.FailureMessage = scrubbedForDiagnostics(humanMessageForReason(reason, versionOutput, nil))
		summary.FailureDetails = redactedFailureDetails(map[string]any{
			"reason":         string(reason),
			"version_output": scrubbedForDiagnostics(versionOutput),
		})
		return summary
	}
	summary.Version = parsedVersion
	selected, err := SelectFixtureMetadata(GateOptions{
		VersionOutput:   versionOutput,
		FixtureRoot:     opts.FixtureRoot,
		ExperimentalAPI: opts.ExperimentalAPI,
	})
	summary.FixtureSupport = inspectFixtureSupport(opts.FixtureRoot, parsedVersion)
	if err != nil {
		apiErr := core.AsAPIError(err)
		code := string(apiErr.Code)
		if code == "" {
			code = string(core.FailureUnsupportedCodexVersion)
		}
		reason, details := mapSelectFixtureError(err)
		summary.Available = false
		summary.Support = CodexSupport{CLI: SupportStatusUnsupported, Model: SupportStatusUnknown, Sandbox: SupportStatusUnknown}
		summary.FailureCode = code
		summary.FailureReason = reason
		summary.FailureMessage = scrubbedForDiagnostics(humanMessageForReason(reason, parsedVersion, details))
		summary.FailureDetails = redactedFailureDetails(details)
		return summary
	}
	summary.Available = true
	summary.Support = CodexSupport{CLI: SupportStatusSupported, Model: SupportStatusSupported, Sandbox: SupportStatusSupported}
	// P1 round-1 fix: scrub the success-path metadata so a
	// poisoned compatibility.json whose SupportedNotifications /
	// SupportedRequests carry a synthetic-sentinel-shaped string
	// cannot leak raw prompt / raw Codex log / raw secret content
	// via the diagnostics envelope. The other metadata fields are
	// version / protocol / schema identifiers and a boolean
	// (experimental_api) and do not carry user-supplied content,
	// so they bypass the scrubber.
	summary.Metadata = scrubbedMetadata(&selected.Metadata)
	return summary
}

// classifyEmptyOrMalformedVersion decides between
// ReasonCodexNotInstalled (the binary is not on PATH) and
// ReasonMalformedVersion (the binary ran but its --version line
// could not be parsed). The classification matters because the
// operator remediation is different in the two cases
// (install-the-binary vs upgrade-the-binary).
//
// Algorithm:
//  1. If the version probe produced non-empty output, the binary
//     ran and emitted a parseable-but-malformed line. Surface
//     ReasonMalformedVersion.
//  2. If the probe produced empty output AND the binary cannot be
//     found via exec.LookPath (after unwrapping known wrappers
//     such as `env`, `sudo`, `nohup`, `command`, `time`,
//     `xargs -I{}`), surface ReasonCodexNotInstalled.
//  3. If the probe produced empty output AND the unwrapped binary
//     resolves, surface ReasonMalformedVersion. This protects
//     against the DetectVersionForCommand implementation losing
//     malformed output by returning "" for a real parse failure.
//
// Round-3 fix: if the command carries a `PATH=<value>` env-var
// prefix (e.g. `env PATH=/opt/codex/bin codex app-server`),
// the lookup must consult the prefix-augmented PATH. Without
// the augmentation the binary resolves to whatever the
// current process's PATH says, which can falsely report
// ReasonCodexNotInstalled for a codex binary that exists
// only on the operator's wrapped PATH.
//
// Tests assert the three branches independently, the
// wrapped-command unwrap path, and the PATH-prefix augmentation.
func classifyEmptyOrMalformedVersion(probeOutput, command string) PreflightReason {
	if strings.TrimSpace(probeOutput) != "" {
		return ReasonMalformedVersion
	}
	binary := unwrapCodexBinaryToken(command)
	if binary == "" {
		return ReasonCodexNotInstalled
	}
	if _, err := lookPathWithWrapperPATH(binary, command); err != nil {
		return ReasonCodexNotInstalled
	}
	return ReasonMalformedVersion
}

// lookPathWithWrapperPATH resolves binary on PATH, honoring
// a `PATH=<value>` env-var prefix in the operator's command
// string. If the command is
// `env PATH=/opt/codex/bin codex app-server` and
// `/opt/codex/bin/codex` exists, the result is the resolved
// absolute path; if it does not exist, exec.LookPath fails
// and the classifier reports ReasonCodexNotInstalled.
//
// Round-4 fix: the path-qualified-binary contract from
// exec.LookPath's documentation is
//
//	"If file contains a slash, it is tried directly
//	 and the PATH is not consulted."
//
// The round-3 helper loop did not honor that contract
// for the combination of a `PATH=<value>` prefix and
// an absolute binary (e.g. `env PATH=/tmp
// /opt/codex/bin/codex`): the loop built candidates
// like `/tmp//opt/codex/bin/codex`, which never
// resolve, and the helper reported
// ReasonCodexNotInstalled even though the binary
// existed. The fix is a short-circuit: when binary
// contains a path separator, call exec.LookPath
// directly. The wrapper PATH prefix is irrelevant
// for path-qualified binaries; the classifier must
// trust the operator's explicit path.
//
// Tests:
//   - TestLookPathWithWrapperPATHHandlesAbsoluteBinary
//   - TestLookPathWithWrapperPATHHandlesRelativeBinary
//   - TestRunPreflightClassifiesAbsoluteBinaryAsMalformedNotNotInstalled
func lookPathWithWrapperPATH(binary, command string) (string, error) {
	// Path-qualified binary: never consult PATH. The
	// PATH-prefix augmentation in `command` is
	// irrelevant; the operator gave us an explicit
	// path and we honor it. exec.LookPath handles the
	// dir / executable-mode / symlink cases itself.
	if strings.ContainsRune(binary, os.PathSeparator) {
		return exec.LookPath(binary)
	}
	prefix := pathPrefixFromCommand(command)
	if prefix == "" {
		return exec.LookPath(binary)
	}
	currentPATH := os.Getenv("PATH")
	augmented := prefix
	if currentPATH != "" {
		augmented = prefix + string(os.PathListSeparator) + currentPATH
	}
	// We only need LookPath semantics here, not full exec
	// semantics. exec.LookPath is the standard helper, so
	// emulate its behaviour by re-doing the search with
	// the augmented env. Each PATH entry is checked in
	// order; the first executable file with the right
	// name wins.
	for _, dir := range splitPATHList(augmented) {
		if dir == "" {
			continue
		}
		candidate := dir + string(os.PathSeparator) + binary
		info, err := os.Stat(candidate)
		if err != nil {
			continue
		}
		if info.IsDir() {
			continue
		}
		if info.Mode()&0o111 == 0 {
			continue
		}
		return candidate, nil
	}
	return "", exec.ErrNotFound
}

// pathPrefixFromCommand extracts a `PATH=<value>` env-var
// prefix from a wrapper-style command. Returns the path
// list (with all the operator's added directories) or
// "" if no PATH prefix is present. The detection mirrors
// isEnvAssignment so a `PATH=` token anywhere in the argv
// is recognised.
//
// Examples:
//
//	"env PATH=/opt/bin codex"      -> "/opt/bin"
//	"PATH=/opt/bin codex"          -> "/opt/bin"
//	"env X=1 PATH=/a:/b codex"     -> "/a:/b"  (last wins)
//	"env X=1 codex"                -> ""        (no PATH prefix)
func pathPrefixFromCommand(command string) string {
	parts := commandParts(command)
	value := ""
	for _, token := range parts {
		if !isEnvAssignment(token) {
			continue
		}
		eq := strings.IndexByte(token, '=')
		if eq <= 0 {
			continue
		}
		if token[:eq] != "PATH" {
			continue
		}
		value = token[eq+1:]
	}
	return value
}

// splitPATHList yields each entry of a PATH-style list.
// Exposed for tests so the path-splitting semantics are
// pinned to the same algorithm exec.LookPath uses.
func splitPATHList(pathList string) []string {
	if pathList == "" {
		return nil
	}
	return strings.Split(pathList, string(os.PathListSeparator))
}

// firstTokenOfCommand returns the leading binary token of a
// shell-style command string, mirroring the parser used by
// commandParts. Empty input yields "".
func firstTokenOfCommand(command string) string {
	parts := commandParts(command)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

// unwrapCodexBinaryToken walks past known shell-style wrappers
// and env-var prefixes to return the actual codex binary that
// the configured command would invoke. The unwrap list is
// deliberately narrow: it covers the wrappers the round-2
// review identified (env / sudo / nohup / command / time /
// xargs -I{}) plus the standard `KEY=value` env-var prefix that
// shells strip before exec. Any unrecognized leading token is
// returned as-is so we fall back to the round-1 first-token
// behavior on commands we have no special knowledge of.
//
// Examples:
//
//	"codex"                              -> "codex"
//	"codex app-server"                   -> "codex"
//	"env CODEX_API_KEY=x codex"          -> "codex"
//	"sudo -E codex app-server"           -> "codex"
//	"nohup codex"                        -> "codex"
//	"xargs -I{} codex"                   -> "codex"
//	"PATH=/opt/bin codex"                -> "codex"
//	"some-unknown-wrapper codex"         -> "some-unknown-wrapper"
func unwrapCodexBinaryToken(command string) string {
	parts := commandParts(command)
	for i := 0; i < len(parts); {
		token := parts[i]
		if token == "" {
			i++
			continue
		}
		// Skip "KEY=VALUE" env-var prefixes the way the shell
		// would; they are not executables.
		if isEnvAssignment(token) {
			i++
			continue
		}
		if isKnownWrapper(token) {
			// Advance past the wrapper itself. The set of
			// known wrappers is the only place that gets
			// to consume extra argv tokens; everything
			// else is left for the next iteration to
			// classify.
			i++
			if consumeWrapperFlagsFor(token) {
				for i < len(parts) && isWrapperFlagToken(parts[i]) {
					i++
				}
			}
			continue
		}
		return token
	}
	return ""
}

// isEnvAssignment reports whether a command token is the shell
// `KEY=VALUE` form (e.g. `FOO=bar`, `CODEX_API_KEY=secret`).
// The shell strips these from argv before exec; the diagnostic
// classifier must not LookPath them.
func isEnvAssignment(token string) bool {
	eq := strings.IndexByte(token, '=')
	if eq <= 0 {
		return false
	}
	for _, r := range token[:eq] {
		if r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

// knownWrappers is the conservative set of wrapper commands the
// not-installed classifier knows how to step past. The list is
// intentionally a closed set: an unrecognized leading token
// causes the classifier to fall back to the round-1 first-token
// behaviour, which is what the existing tests expect.
var knownWrappers = map[string]struct{}{
	"env":     {},
	"sudo":    {},
	"nohup":   {},
	"command": {},
	"time":    {},
	"xargs":   {},
}

// isKnownWrapper reports whether a command token is a wrapper
// the not-installed classifier knows how to step past.
func isKnownWrapper(token string) bool {
	_, ok := knownWrappers[filepath.Base(token)]
	return ok
}

// consumeWrapperFlagsFor reports whether the named wrapper is
// known to take a contiguous run of flag-shaped arguments
// (e.g. `sudo -E -H codex` or `xargs -I{} codex`) that must be
// consumed alongside the wrapper before the next non-flag
// token. We apply this only to the closed set of known
// wrappers, and the classifier advances over flags greedily:
// a flag token is any token that starts with a single dash
// and is composed of [A-Za-z0-9_-{}] (the { and } are
// permitted so `xargs -I{}` works as a unit).
func consumeWrapperFlagsFor(wrapper string) bool {
	switch filepath.Base(wrapper) {
	case "env", "sudo", "nohup", "command", "time", "xargs":
		return true
	}
	return false
}

// isWrapperFlagToken reports whether a single argv token looks
// like a flag argument (e.g. `-E`, `-H`, `-I{}`, `--user`).
// Used to skip past the optional flag run after a known
// wrapper invocation.
//
// Round-3 fix: the empty-string case must be handled
// explicitly. `env "" codex` and similar malformed commands
// produce an empty token from commandParts; indexing into
// an empty string at `token[0]` panics with index out of
// range. The empty token is not a flag and not a binary
// either, so we return false and let the unwrap walker
// skip past it. The fix is two lines (the `len(token) == 0`
// guard) but it closes a runtime crash that the round-2
// test set did not cover.
func isWrapperFlagToken(token string) bool {
	if len(token) == 0 {
		return false
	}
	if len(token) < 2 || token[0] != '-' || token[1] == '-' {
		// Either too short to be a flag, or a long-form
		// `--key=value` flag that takes a value in the
		// same token and therefore does not consume the
		// next argv slot.
		return token[0] == '-' && len(token) > 1
	}
	for _, r := range token[1:] {
		if r == '_' || r == '-' || r == '{' || r == '}' {
			continue
		}
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

// redactedCommandForDiagnostics returns the binary basename of
// the configured Codex command, with arguments (which may carry
// --api-key / --token / wrapper flags) dropped. The result is
// also passed through scrubbedForDiagnostics as defense in depth
// against a synthetic-sentinel-shaped value sneaking in via a
// caller-provided command string.
//
// This mirrors the pattern agent.process_started events use: the
// stored event reports "command": "redacted" and the run logs
// only the basename. Without this redaction the diagnostics
// surface can reintroduce a raw secret-leak path that the
// process_started event already closed.
func redactedCommandForDiagnostics(command string) string {
	parts := commandParts(command)
	if len(parts) == 0 {
		return scrubbedForDiagnostics(strings.TrimSpace(command))
	}
	binary := filepath.Base(parts[0])
	if binary == "" || binary == "." || binary == ".." {
		return scrubbedForDiagnostics(parts[0])
	}
	return scrubbedForDiagnostics(binary)
}

// scrubbedMetadata returns a copy of the CompatibilityMetadata
// with SupportedNotifications and SupportedRequests scrubbed of
// any synthetic-sentinel-shaped value. The other fields
// (codex_version / protocol_version / schema_version /
// experimental_api) are version / protocol identifiers and a
// boolean; they do not carry operator-supplied content and
// bypass the scrubber. The scrubber preserves the slice length
// and ordering so the diagnostics envelope's shape is stable.
//
// A poisoned compatibility.json whose SupportedNotifications
// still parses as a []string but contains e.g.
// ["SYNTHETIC_PROMPT_BODY_do_not_leak", "handoff"] will be
// normalized to ["[REDACTED]", "handoff"] before export, so the
// success path is no longer a leak surface.
func scrubbedMetadata(m *CompatibilityMetadata) *CompatibilityMetadata {
	if m == nil {
		return nil
	}
	notes := make([]string, 0, len(m.SupportedNotifications))
	for _, s := range m.SupportedNotifications {
		notes = append(notes, scrubbedForDiagnostics(s))
	}
	reqs := make([]string, 0, len(m.SupportedRequests))
	for _, s := range m.SupportedRequests {
		reqs = append(reqs, scrubbedForDiagnostics(s))
	}
	return &CompatibilityMetadata{
		CodexVersion:           m.CodexVersion,
		ProtocolVersion:        m.ProtocolVersion,
		SchemaVersion:          m.SchemaVersion,
		ExperimentalAPI:        m.ExperimentalAPI,
		SupportedNotifications: notes,
		SupportedRequests:      reqs,
	}
}

func inspectFixtureSupport(root, version string) FixtureSupport {
	if strings.TrimSpace(version) == "" {
		return FixtureSupport{}
	}
	if strings.TrimSpace(root) == "" {
		root = defaultFixtureRoot()
	}
	schemaDir := filepath.Join(root, "schema", version)
	transcriptDir := filepath.Join(root, "transcripts", version)
	return FixtureSupport{
		SchemaAvailable:     isFile(filepath.Join(schemaDir, "schema.json")),
		MetadataAvailable:   isFile(filepath.Join(schemaDir, compatibilityMetadataFile)),
		TranscriptAvailable: isFile(filepath.Join(transcriptDir, "happy-path.jsonl")),
	}
}

func mapSelectFixtureError(err error) (PreflightReason, map[string]any) {
	apiErr := core.AsAPIError(err)
	details := map[string]any{}
	for k, v := range apiErr.Details {
		details[k] = v
	}
	rawReason, _ := details["reason"].(string)
	reason := preflightReasonFromDetail(rawReason)
	return reason, details
}

func preflightReasonFromDetail(raw string) PreflightReason {
	switch strings.TrimSpace(raw) {
	case "malformed_version":
		return ReasonMalformedVersion
	case "missing_fixture":
		return ReasonMissingFixture
	case "missing_metadata":
		return ReasonMissingMetadata
	case "malformed_metadata":
		return ReasonMalformedMetadata
	case "metadata_codex_version_mismatch":
		return ReasonVersionMismatch
	case "metadata_missing_codex_version":
		return ReasonMetadataMissingCodex
	case "metadata_missing_protocol_version":
		return ReasonMetadataMissingProto
	case "metadata_missing_schema_version":
		return ReasonMetadataMissingSchema
	case "metadata_protocol_version_sentinel":
		return ReasonMetadataProtoSentinel
	case "metadata_schema_version_sentinel":
		return ReasonMetadataSchemaSentinel
	case "metadata_codex_version_sentinel":
		return ReasonMetadataCodexSentinel
	case "metadata_missing_supported_notifications":
		return ReasonMetadataMissingNotes
	case "metadata_missing_supported_requests":
		return ReasonMetadataMissingReqs
	case "experimental_api_not_supported":
		return ReasonExperimentalNotSupport
	case "missing_schema_fixture":
		return ReasonMissingSchemaFixture
	case "missing_transcript_fixture":
		return ReasonMissingTranscript
	case "invalid_transcript_fixture":
		return ReasonInvalidTranscript
	default:
		return ReasonUnknown
	}
}

func humanMessageForReason(reason PreflightReason, versionOrOutput string, details map[string]any) string {
	versionOrOutput = strings.TrimSpace(versionOrOutput)
	switch reason {
	case ReasonMalformedVersion:
		if versionOrOutput == "" {
			return "could not parse codex version from --version output"
		}
		return "could not parse codex version from --version output: " + versionOrOutput
	case ReasonCodexNotInstalled:
		return "codex binary is not installed or not on PATH"
	case ReasonMissingFixture:
		return "no Codex fixture is registered for the detected codex_version"
	case ReasonMissingMetadata:
		return "compatibility.json is missing for the detected codex_version"
	case ReasonMalformedMetadata:
		return "compatibility.json is malformed"
	case ReasonVersionMismatch:
		detected, _ := details["detected_version"].(string)
		metadata, _ := details["metadata_version"].(string)
		return "compatibility.json codex_version does not match detected version: detected=" + detected + " metadata=" + metadata
	case ReasonMetadataMissingCodex,
		ReasonMetadataMissingProto,
		ReasonMetadataMissingSchema,
		ReasonMetadataMissingNotes,
		ReasonMetadataMissingReqs:
		return "compatibility.json is missing a required field: " + strings.TrimPrefix(string(reason), "metadata_missing_")
	case ReasonMetadataProtoSentinel,
		ReasonMetadataSchemaSentinel,
		ReasonMetadataCodexSentinel:
		return "compatibility.json has a synthetic-sentinel-shaped identifier in a version field; the fixture is rejected"
	case ReasonExperimentalNotSupport:
		return "experimental_api is required by Symphony but the installed Codex fixture does not enable it"
	case ReasonMissingSchemaFixture:
		return "schema.json is missing under the detected codex_version fixture directory"
	case ReasonMissingTranscript:
		return "transcripts/happy-path.jsonl is missing under the detected codex_version fixture directory"
	case ReasonInvalidTranscript:
		errMsg, _ := details["error"].(string)
		return "transcripts/happy-path.jsonl failed validation: " + errMsg
	default:
		if versionOrOutput == "" {
			return "codex preflight failed: unknown reason"
		}
		return "codex preflight failed: unknown reason (codex_version=" + versionOrOutput + ")"
	}
}

// redactedFailureDetails returns a copy of the failure details map
// that strips any value matching the synthetic-sentinel pattern
// (SYNTHETIC_PROMPT_BODY / SYNTHETIC_CODEX_LOG / etc.) so a
// poisoned or attacker-controlled error cannot leak fixture content
// through the diagnostics export path.
func redactedFailureDetails(details map[string]any) map[string]any {
	if len(details) == 0 {
		return nil
	}
	out := make(map[string]any, len(details))
	for k, v := range details {
		switch typed := v.(type) {
		case string:
			out[k] = scrubbedForDiagnostics(typed)
		case []string:
			scrubbed := make([]string, 0, len(typed))
			for _, s := range typed {
				scrubbed = append(scrubbed, scrubbedForDiagnostics(s))
			}
			out[k] = scrubbed
		case map[string]any:
			out[k] = redactedFailureDetails(typed)
		default:
			// Non-string scalars are passed through unchanged:
			// the synthetic-sentinel detector only matches the
			// all-caps-with-underscore pattern, which never
			// appears in numeric or boolean values.
			out[k] = v
		}
	}
	return out
}

// scrubbedForDiagnostics removes any synthetic-sentinel-shaped
// substring (ASCII letters / digits / underscores following the
// literal `SYNTHETIC_` prefix) and replaces the run with
// `[REDACTED]`. Lowercase suffixes are included because repo sentinel
// fixtures intentionally append lowercase payload markers such as
// `_do_not_leak_in_diagnostics`; leaving that suffix behind would still
// expose the poisoned prompt/log/secret payload. The same pattern is used
// by the redaction golden fixtures; keeping the substitution identical lets tests assert
// against a stable surface.
//
// Rationale: a poisoned compatibility.json or --version output could
// embed the operator's prompt body / a Codex log line / a secret
// value prefixed with the sentinel pattern. The redaction policy
// strips those before the value ever reaches the diagnostics
// envelope, satisfying the v1 "no raw prompt / no raw Codex log /
// no raw secret" invariant at the source.
func scrubbedForDiagnostics(value string) string {
	const sentinel = "SYNTHETIC_"
	if !strings.Contains(value, sentinel) {
		return value
	}
	for {
		idx := strings.Index(value, sentinel)
		if idx < 0 {
			return value
		}
		end := idx + len(sentinel)
		for end < len(value) && isSyntheticSentinelRedactionByte(value[end]) {
			end++
		}
		value = value[:idx] + "[REDACTED]" + value[end:]
	}
}

func isSyntheticSentinelRedactionByte(b byte) bool {
	return b == '_' ||
		(b >= 'A' && b <= 'Z') ||
		(b >= 'a' && b <= 'z') ||
		(b >= '0' && b <= '9')
}

// PreflightSummaryToJSON is a tiny convenience used by tests that
// need to assert on the JSON wire form.
func PreflightSummaryToJSON(s PreflightSummary) (string, error) {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// failureDetailsForTest exposes the internal scrubber for tests so
// the package-internal `*_test.go` can assert the redaction policy
// without depending on unexported helpers from outside the package.
func failureDetailsForTest(details map[string]any) map[string]any {
	return redactedFailureDetails(details)
}

// scrubbedMetadataForTest exposes the success-path metadata
// scrubber so the package-internal `*_test.go` can assert that
// a poisoned compatibility.json cannot leak sentinels via the
// Metadata block.
func scrubbedMetadataForTest(m *CompatibilityMetadata) *CompatibilityMetadata {
	return scrubbedMetadata(m)
}

// redactedCommandForTest exposes the command basename scrubber
// so the package-internal `*_test.go` can assert that a
// caller-provided command does not leak its arguments.
func redactedCommandForTest(command string) string {
	return redactedCommandForDiagnostics(command)
}

// unwrapCodexBinaryTokenForTest exposes the wrapper-unwrap
// helper so the package-internal `*_test.go` can assert the
// unwrap behavior for env / sudo / nohup / command / time /
// xargs prefixes.
func unwrapCodexBinaryTokenForTest(command string) string {
	return unwrapCodexBinaryToken(command)
}

// isSyntheticSentinelStringForTest exposes the sentinel-shape
// detector so the package-internal `*_test.go` can assert
// the negative cases (legitimate version strings must not
// be flagged).
func isSyntheticSentinelStringForTest(s string) bool {
	return isSyntheticSentinelString(s)
}

// pathPrefixFromCommandForTest exposes the PATH-prefix
// extractor so the package-internal `*_test.go` can assert
// the wrapper PATH semantics without reaching into the
// internal helper.
func pathPrefixFromCommandForTest(command string) string {
	return pathPrefixFromCommand(command)
}

// lookPathWithWrapperPATHForTest exposes the wrapped
// PATH-augmented LookPath helper so the package-internal
// `*_test.go` can pin the path-qualified binary semantics
// without reaching into the internal helper.
func lookPathWithWrapperPATHForTest(binary, command string) (string, error) {
	return lookPathWithWrapperPATH(binary, command)
}

// scratchDirIsEmpty reports whether the path is either missing or
// contains no entries. Used by tests to confirm the fixture root is
// genuinely absent.
func scratchDirIsEmpty(path string) bool {
	entries, err := os.ReadDir(path)
	if err != nil {
		return os.IsNotExist(err)
	}
	return len(entries) == 0
}

// ensurePlaceholder is a no-op helper kept for symmetry with
// preflight_test.go; removing it would also require updating the
// tests. The placeholder exists so the import block in
// preflight_test.go is the same shape as in preflight.go.
var _ = fmt.Sprintf
