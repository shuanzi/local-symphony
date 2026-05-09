# IS-009 — Security Policy Engine

## Status

Frozen.

## Goal

Define v1 security mechanisms: session token, CSRF, run-scoped tool token, command policy, network policy, protected paths, redaction, and artifact containment.

v1 does not implement full audit log, secret manager, supply-chain risk scoring, remote dashboard, or RBAC.

## Session auth

Browser:

```text
HttpOnly SameSite=Lax cookie
CSRF header for command APIs
```

CLI:

```text
Bearer token
stored in ~/.symphony/cli-session.json
token hash stored in DB
```

Open token:

```text
created by symphony open
one-time
short TTL
exchanged into browser session
```

## Tool token

Scope:

```text
project_id
issue_id
run_id
workspace_path
allowed_tools
expires_at
```

Every call validates:

```text
token hash
not expired
not revoked
run.status = running
issue scope
cwd under workspace
tool allowed
```

## Command policy

Categories:

```text
allow
review
deny
```

Default allow:

```text
git status
git diff
git log
rg
grep
find
ls
cat
go test ./...
pytest
npm test
pnpm test
cargo test
symphony tool issue get
symphony tool issue comment
symphony tool issue block
symphony tool artifact attach
symphony tool followup create
symphony tool handoff
```

`symphony tool ...` commands are allowed only as the agent Tool Gateway entrypoint. They still require `SYMPHONY_TOOL_TOKEN`, run scope validation, cwd-under-workspace validation, daemon-side schema validation, and the fixed tool registry checks in IS-004. This allow rule prevents the shell command policy from blocking the required handoff path; it does not grant operator API privileges to the agent.

Default review:

```text
npm install
pnpm install
yarn install
pip install
go mod download
cargo fetch
make
docker build
```

Default deny:

```text
git push
git push --force
gh pr create
gh pr merge
sudo
rm -rf /
rm -rf ~
curl | sh
wget | sh
ssh
scp
docker run --privileged
```

v1 uses pattern/prefix classification, not deep supply-chain analysis.

Policy outcomes map to canonical failure codes when they terminate a run:

| Policy outcome | Approval row | Run failure code when terminal |
|---|---|---|
| command auto-denied or denied | `auto_denied` or `denied` | `command_denied` |
| network denied | `auto_denied` or `denied` | `network_denied` |
| protected path denied | `auto_denied` or `denied` | `protected_path_denied` |

If the Codex protocol can continue after a denied request, the run may continue and the denial remains an approval/tool event. If the adapter reports turn failure or no handoff is possible because of the denial, the terminal run uses the corresponding failure code above.

## Network policy

Default:

```text
network.default = deny
allowlist = []
```

Requests are denied or converted to Approval Inbox items unless allowlisted by config and mode.

v1 does not implement packet firewall, egress accounting, or dependency-origin attribution.

## Protected paths

Default protected:

```text
.env
.env.*
**/*.pem
**/*.key
**/*_rsa
**/*_ed25519
.ssh/**
.aws/**
.gcp/**
.azure/**
.kube/**
.npmrc
.pypirc
.netrc
```

Rules:

```text
read protected path → deny or approval
write protected path → deny
artifact attach protected path → deny
```

## Redaction

Applies to:

```text
run_events.data_json
tool_calls input/output
prompt snapshots
review packets
diagnostic exports
UI log surfaces
```

Preserve safe metadata such as hash, length category, field name, and safe summary.

## Artifact containment

Artifact content endpoint and attach tool must ensure:

```text
source path in workspace for attaches
artifact path project-local relative
resolved path under .symphony/artifacts or .symphony/exports
no path traversal
protected path denied
```

## v1 event records vs audit

v1 records security-relevant events in:

```text
run_events
tool_calls
approval_requests
structured logs
```

These are operational records, not a compliance-grade audit log.

## Frozen decisions

| ID | Decision |
|---|---|
| IS9-001 | browser cookie + CSRF; CLI bearer token |
| IS9-002 | run-scoped tool token, never reused |
| IS9-003 | command allow/review/deny, no deep supply-chain analysis |
| IS9-004 | network default deny |
| IS9-005 | protected paths deny writes and usually reads |
| IS9-006 | redaction before UI/log/artifact exposure |
| IS9-007 | security records exist, no full audit log |
| IS9-008 | remote dashboard unsupported |
| IS9-009 | required `symphony tool ...` commands are command-policy allowed but still fully validated by Tool Gateway token and schema checks |
| IS9-010 | command, network, and protected-path denials map to canonical terminal failure codes when they end a run |
