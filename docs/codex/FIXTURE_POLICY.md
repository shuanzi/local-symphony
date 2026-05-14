# Codex Fixture Policy

## Rule

A Codex protocol version is supported only when committed fixtures exist under:

```text
internal/agent/codex/testdata/schema/<codex-version>/
internal/agent/codex/testdata/transcripts/<codex-version>/
```

## Required fixture contents

```text
schema.json or generated TypeScript types
fixture metadata with installed Codex version and generated protocol/schema version
happy-path transcript
missing-handoff transcript
command approval transcript
file-change approval transcript
network approval transcript
protocol-error transcript
static compatibility metadata when generated protocol/schema version differs from installed Codex version
```

## Dispatch behavior

```text
1. Detect installed Codex version without launching the long-lived real `codex app-server` process.
2. Resolve committed fixture metadata/static compatibility metadata for that installed Codex version.
3. Read expected generated protocol/schema version from that metadata.
4. Resolve fixture directory for the installed Codex version plus generated protocol/schema version.
5. If fixture or compatibility metadata is missing, record a failed run attempt with `unsupported_codex_version`, restore the issue to its `source_issue_state`, and pause dispatch.
6. This failure must occur before launching the real `codex app-server` process.
7. If fixture exists, run adapter compatibility checks against committed metadata.
8. Only then launch `codex app-server`.
```

The prelaunch gate does not discover generated protocol/schema version from a real app-server handshake. That version comes from committed fixture metadata or static compatibility metadata. If the post-launch initialize handshake later contradicts the selected metadata or exposes a schema mismatch, fail the run with `codex_protocol_error`.

## CI

Default CI uses fake runner only. Real Codex tests run only when explicitly enabled:

```bash
SYMPHONY_TEST_CODEX=1 go test ./internal/agent/codex -run Integration
```
