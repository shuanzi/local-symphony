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
happy-path transcript
missing-handoff transcript
command approval transcript
file-change approval transcript
network approval transcript
protocol-error transcript
version metadata
```

## Dispatch behavior

```text
1. Detect installed Codex version.
2. Resolve fixture directory.
3. If fixture is missing, record a failed run attempt with `unsupported_codex_version`, restore the issue to its `source_issue_state`, and pause dispatch.
4. This failure must occur before launching the real `codex app-server` process.
5. If fixture exists, run adapter compatibility checks.
6. Only then launch `codex app-server`.
```

## CI

Default CI uses fake runner only. Real Codex tests run only when explicitly enabled:

```bash
SYMPHONY_TEST_CODEX=1 go test ./internal/agent/codex -run Integration
```
