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
3. If fixture missing, fail before starting run with unsupported_codex_version.
4. If fixture exists, run adapter compatibility checks.
5. Only then launch codex app-server.
```

## CI

Default CI uses fake runner only. Real Codex tests run only when explicitly enabled:

```bash
SYMPHONY_TEST_CODEX=1 go test ./internal/agent/codex -run Integration
```
