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
//     found via exec.LookPath, surface ReasonCodexNotInstalled.
//  3. If the probe produced empty output AND exec.LookPath finds
//     the binary (so it should have been able to run), surface
//     ReasonMalformedVersion. This protects against the
//     DetectVersionForCommand implementation losing malformed
//     output by returning "" for a real parse failure.
//
// Tests assert the three branches independently.
func classifyEmptyOrMalformedVersion(probeOutput, command string) PreflightReason {
	if strings.TrimSpace(probeOutput) != "" {
		return ReasonMalformedVersion
	}
	binary := firstTokenOfCommand(command)
	if binary == "" {
		return ReasonCodexNotInstalled
	}
	if _, err := exec.LookPath(binary); err != nil {
		return ReasonCodexNotInstalled
	}
	return ReasonMalformedVersion
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
// substring (uppercase letters / digits / underscores following the
// literal `SYNTHETIC_` prefix) and replaces the run with
// `[REDACTED]`. The same pattern is used by the redaction golden
// fixtures; keeping the substitution identical lets tests assert
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
		for end < len(value) && (value[end] == '_' ||
			(value[end] >= 'A' && value[end] <= 'Z') ||
			(value[end] >= '0' && value[end] <= '9')) {
			end++
		}
		value = value[:idx] + "[REDACTED]" + value[end:]
	}
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
