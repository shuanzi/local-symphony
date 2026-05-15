#!/usr/bin/env python3
"""Validate Local Symphony v1 documentation contract artifacts.

This script is intentionally runnable before product code exists. It checks
syntax plus a small set of cross-file drift rules that are easy for an
implementation agent to accidentally miss.
"""
from __future__ import annotations

import json
import re
import sqlite3
import sys
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[1]
CONTRACT_MANIFEST_REL = "docs/testing/CONTRACT_VALIDATION_MANIFEST.json"

REQUIRED_OPENAPI_ROUTES = frozenset(
    {
        "/health",
        "/state",
        "/events",
        "/events/stream",
        "/auth/exchange",
        "/auth/session",
        "/auth/logout",
        "/auth/open-token",
        "/auth/cli-token/rotate",
        "/issues",
        "/issues/{issue_ref}",
        "/issues/{issue_ref}/transition",
        "/issues/{issue_ref}/comments",
        "/issues/{issue_ref}/blockers",
        "/issues/{issue_ref}/blockers/{blocker_issue_ref}",
        "/issues/{issue_ref}/duplicates/{canonical_issue_ref}",
        "/issues/{issue_ref}/dispatch",
        "/issues/{issue_ref}/dispatch-pause",
        "/issues/{issue_ref}/dispatch-resume",
        "/issues/{issue_ref}/events/stream",
        "/runs",
        "/runs/{run_id}",
        "/runs/{run_id}/events",
        "/runs/{run_id}/events/stream",
        "/runs/{run_id}/cancel",
        "/approvals",
        "/approvals/{approval_id}/decide",
        "/reviews/{issue_ref}",
        "/reviews/{issue_ref}/send-to-rework",
        "/reviews/{issue_ref}/mark-done",
        "/artifacts/{artifact_id}",
        "/artifacts/{artifact_id}/content",
        "/workflow",
        "/workflow/validate",
        "/workflow/render-preview",
        "/workflow/reload",
        "/diagnostics",
        "/diagnostics/export",
    }
)

REQUIRED_CLI_COMMANDS = frozenset(
    {
        "symphony issue duplicate remove",
        "symphony issue dispatch-pause",
        "symphony issue dispatch-resume",
    }
)

REQUIRED_DIAGNOSTICS_FIELDS = frozenset(
    {
        "project_id",
        "generated_at",
        "redacted",
        "repo_root",
        "database",
        "workflow",
        "daemon",
        "codex",
        "git",
        "redaction",
        "warnings",
        "inconsistent_issues",
        "remediation",
        "failure_summary",
        "pause_summary",
        "checks",
    }
)

DIAGNOSTICS_DATABASE_FIELDS = frozenset(
    {
        "app_db_path",
        "project_db_path",
        "app_schema_version",
        "project_schema_version",
        "app_version_status",
        "project_version_status",
    }
)
DIAGNOSTICS_DATABASE_VERSION_STATUS_ENUM = ("supported", "unsupported", "unknown", "missing")
DIAGNOSTICS_SUPPORT_STATUS_ENUM = ("supported", "unsupported", "unknown")
DIAGNOSTICS_GIT_STATUS_ENUM = ("clean", "dirty", "unavailable", "unknown")
DIAGNOSTICS_CHECK_STATUS_ENUM = ("ok", "warning", "error")
DIAGNOSTICS_DEFINITION_REQUIRED_FIELDS = {
    "database": DIAGNOSTICS_DATABASE_FIELDS,
    "workflow": frozenset({"config_path", "validation", "last_valid_config"}),
    "workflowValidation": frozenset({"valid", "warnings", "errors"}),
    "lastValidConfig": frozenset({"available", "path", "validated_at", "content_hash"}),
    "daemon": frozenset({"pid", "uptime_ms", "runtime_descriptor"}),
    "runtimeDescriptor": frozenset({"api_url", "tool_gateway_endpoint", "daemon_pid"}),
    "codex": frozenset({"available", "version", "support"}),
    "codexSupport": frozenset({"cli", "model", "sandbox"}),
    "git": frozenset({"repository", "worktree"}),
    "gitRepository": frozenset({"is_repo", "root", "branch", "head_sha", "status"}),
    "gitWorktree": frozenset({"path", "branch", "base_ref", "status"}),
    "redaction": frozenset({"enabled", "export_redacted_only", "rules_version"}),
    "inconsistentIssue": frozenset({"issue_ref", "problem", "remediation"}),
    "remediation": frozenset({"action", "description"}),
    "failureSummary": frozenset({"failed_runs_count", "recent_failures"}),
    "failureBucket": frozenset({"failure_code", "count"}),
    "pauseSummary": frozenset({"paused_dispatch_count", "paused_issue_refs"}),
    "check": frozenset({"name", "status"}),
}
OPENAPI_DIAGNOSTICS_REQUIRED_FIELDS = {
    "Diagnostics": REQUIRED_DIAGNOSTICS_FIELDS,
    "DiagnosticsDatabase": DIAGNOSTICS_DATABASE_FIELDS,
    "DiagnosticsWorkflow": frozenset({"config_path", "validation", "last_valid_config"}),
    "DiagnosticsWorkflowValidation": frozenset({"valid", "warnings", "errors"}),
    "DiagnosticsLastValidConfig": frozenset({"available", "path", "validated_at", "content_hash"}),
    "DiagnosticsDaemon": frozenset({"pid", "uptime_ms", "runtime_descriptor"}),
    "DiagnosticsRuntimeDescriptor": frozenset({"api_url", "tool_gateway_endpoint", "daemon_pid"}),
    "DiagnosticsCodex": frozenset({"available", "version", "support"}),
    "DiagnosticsCodexSupport": frozenset({"cli", "model", "sandbox"}),
    "DiagnosticsGit": frozenset({"repository", "worktree"}),
    "DiagnosticsGitRepository": frozenset({"is_repo", "root", "branch", "head_sha", "status"}),
    "DiagnosticsGitWorktree": frozenset({"path", "branch", "base_ref", "status"}),
    "DiagnosticsRedaction": frozenset({"enabled", "export_redacted_only", "rules_version"}),
    "DiagnosticsInconsistentIssue": frozenset({"issue_ref", "problem", "remediation"}),
    "DiagnosticsRemediation": frozenset({"action", "description"}),
    "DiagnosticsFailureSummary": frozenset({"failed_runs_count", "recent_failures"}),
    "DiagnosticsFailureBucket": frozenset({"failure_code", "count"}),
    "DiagnosticsPauseSummary": frozenset({"paused_dispatch_count", "paused_issue_refs"}),
    "DiagnosticsCheck": frozenset({"name", "status"}),
    "DiagnosticsExport": frozenset({"artifact_id"}),
}

REQUIRED_FORBIDDEN_CONCEPTS = frozenset(
    {
        "publish",
        "create-pr",
        "backup",
        "restore",
        "migrate",
        "audit",
        "workspace-delete",
        "secret",
        "project settings",
        "issue delete",
        "arbitrary state",
    }
)

REQUIRED_FORBIDDEN_OPENAPI_OPERATIONS = (
    {
        "method": "POST",
        "path": "/git/{issue_ref}/push",
        "reason": "git push",
    },
    {
        "method": "POST",
        "path": "/git/{issue_ref}/publish",
        "reason": "git publish",
    },
    {
        "method": "POST",
        "path": "/git/{issue_ref}/pr",
        "reason": "git pr",
    },
    {
        "method": "POST",
        "path": "/git/{issue_ref}/create-pr",
        "reason": "git create-pr",
    },
    {
        "method": "POST",
        "path": "/db/backup",
        "reason": "database backup",
    },
    {
        "method": "POST",
        "path": "/db/restore",
        "reason": "database restore",
    },
    {
        "method": "POST",
        "path": "/db/migrate",
        "reason": "database migrate",
    },
    {
        "method": "GET",
        "path": "/audit",
        "reason": "audit",
    },
    {
        "method": "POST",
        "path": "/workspaces/{issue_ref}/delete",
        "reason": "workspace delete",
    },
    {
        "method": "POST",
        "path": "/workspaces/{issue_ref}/reset",
        "reason": "workspace reset",
    },
    {
        "method": "POST",
        "path": "/workspaces/{issue_ref}/clean",
        "reason": "workspace clean",
    },
    {
        "method": "POST",
        "path": "/workspaces/{issue_ref}/rebase",
        "reason": "workspace rebase",
    },
    {
        "method": "POST",
        "path": "/workspace-delete",
        "reason": "workspace delete",
    },
    {
        "method": "POST",
        "path": "/secrets",
        "reason": "secrets mutation",
    },
    {
        "method": "PATCH",
        "path": "/secrets/*",
        "reason": "secrets mutation",
    },
    {
        "method": "PATCH",
        "path": "/projects/{project_id}/settings",
        "reason": "project settings mutation",
    },
    {
        "method": "DELETE",
        "path": "/issues/{issue_ref}",
        "reason": "issue delete",
    },
    {
        "method": "PATCH",
        "path": "/state/*",
        "reason": "arbitrary state mutation",
    },
)

REQUIRED_SECURITY_TOPIC_SLUGS = frozenset(
    {
        "loopback_required_bind_validation",
        "browser_session_invalid_expired_revoked",
        "csrf_missing_on_command_api",
        "cli_bearer_invalid_expired",
        "open_token_one_time_use",
        "tool_token_wrong_run_issue_cwd_tool_expired_revoked",
        "command_allow_review_deny_classifications",
        "network_denied_fake_request_and_unknown_network_auto_deny",
        "network_policy_review_path_enters_approval_inbox",
        "protected_path_read_write_denial_auto_denied_terminates_run_pauses_dispatch",
        "artifact_attach_protected_path_denied_failed_tool_call_without_approval_row",
        "artifact_path_traversal_and_symlink_escape",
        "redaction_golden_fixtures",
        "raw_prompt_raw_codex_log_raw_secret_api_refusal",
    }
)

OPENAPI_HTTP_METHODS = frozenset({"get", "put", "post", "delete", "options", "head", "patch", "trace"})


def is_under(path: Path, parent: Path) -> bool:
    try:
        path.resolve().relative_to(parent.resolve())
        return True
    except ValueError:
        return False


def fail(msg: str) -> None:
    print(f"FAIL: {msg}", file=sys.stderr)
    sys.exit(1)


def load_json(rel: str) -> Any:
    return json.loads((ROOT / rel).read_text(encoding="utf-8"))


def require_dict(value: Any, label: str) -> dict[str, Any]:
    if not isinstance(value, dict):
        fail(f"{label} must be an object")
    return value


def assert_required_fields(schema: dict[str, Any], label: str, required_fields: set[str] | frozenset[str]) -> None:
    required = schema.get("required")
    if not isinstance(required, list):
        fail(f"{label}.required must be a list")
    missing_required = required_fields - set(required)
    if missing_required:
        fail(f"{label}.required missing fields: {sorted(missing_required)}")

    properties = schema.get("properties")
    if isinstance(properties, dict):
        missing_properties = required_fields - set(properties)
        if missing_properties:
            fail(f"{label}.properties missing required fields: {sorted(missing_properties)}")


def assert_required_field_map_covers_schema(
    definitions: dict[str, Any],
    label_prefix: str,
    required_fields_by_name: dict[str, frozenset[str]],
) -> None:
    for name, definition in definitions.items():
        if not isinstance(definition, dict) or definition.get("type") != "object":
            continue
        required = definition.get("required")
        if not required:
            continue
        if not isinstance(required, list):
            fail(f"{label_prefix}{name}.required must be a list")
        expected = required_fields_by_name.get(name)
        if expected is None:
            fail(f"{label_prefix}{name}.required fields are not covered by contract validation: {sorted(required)}")
        missing_from_map = set(required) - expected
        if missing_from_map:
            fail(f"{label_prefix}{name}.required fields missing from contract validation map: {sorted(missing_from_map)}")


def assert_const(schema: dict[str, Any], label: str, expected: Any) -> None:
    if schema.get("const") != expected:
        fail(f"{label}.const must be {expected!r}")


def assert_enum(schema: dict[str, Any], label: str, expected: tuple[str, ...]) -> None:
    if schema.get("enum") != list(expected):
        fail(f"{label}.enum must be {list(expected)}")


def assert_additional_properties_false(schema: dict[str, Any], label: str) -> None:
    if schema.get("additionalProperties") is not False:
        fail(f"{label}.additionalProperties must be false")


def assert_failure_code_or_null(schema: dict[str, Any], label: str, expected_ref: str) -> None:
    refs = {item.get("$ref", "") for item in schema.get("anyOf", []) if isinstance(item, dict)}
    has_null = any(item.get("type") == "null" for item in schema.get("anyOf", []) if isinstance(item, dict))
    if expected_ref not in refs or not has_null:
        fail(f"{label} must be {expected_ref} or null")


def load_contract_manifest() -> dict[str, Any]:
    manifest = load_json(CONTRACT_MANIFEST_REL)
    if not isinstance(manifest, dict):
        fail(f"{CONTRACT_MANIFEST_REL} must be a JSON object")
    return manifest


def require_list(manifest: dict[str, Any], path: tuple[str, ...]) -> list[Any]:
    current: Any = manifest
    for key in path:
        if not isinstance(current, dict) or key not in current:
            fail(f"{CONTRACT_MANIFEST_REL} missing {'.'.join(path)}")
        current = current[key]
    if not isinstance(current, list) or not current:
        fail(f"{CONTRACT_MANIFEST_REL} {'.'.join(path)} must be a non-empty list")
    return current


def manifest_strings(value: Any) -> list[str]:
    if isinstance(value, str):
        return [value]
    if isinstance(value, list):
        strings: list[str] = []
        for item in value:
            strings.extend(manifest_strings(item))
        return strings
    if isinstance(value, dict):
        strings = []
        for item in value.values():
            strings.extend(manifest_strings(item))
        return strings
    return []


def slugify_topic(value: Any) -> str:
    slug = re.sub(r"[^a-z0-9]+", "_", str(value).lower()).strip("_")
    return re.sub(r"_+", "_", slug)


def validate_contract_manifest(manifest: dict[str, Any]) -> None:
    required_lists = [
        ("openapi", "required_routes"),
        ("openapi", "forbidden_route_fragments"),
        ("openapi", "forbidden_route_patterns"),
        ("openapi", "forbidden_operations"),
        ("cli", "required_commands"),
        ("cli", "required_help_tokens"),
        ("cli", "forbidden_commands"),
        ("handler_route_inventory", "required_routes"),
        ("dashboard_action_inventory", "required_actions"),
        ("dashboard_action_inventory", "forbidden_actions"),
        ("tool_gateway", "registry_tools"),
        ("docs", "required_directories"),
        ("docs", "required_files"),
        ("docs", "agent_work_order_forbidden_command_like_patterns"),
        ("security_regression", "default_commands"),
        ("security_regression", "topics"),
    ]
    for path in required_lists:
        require_list(manifest, path)

    for rel in require_list(manifest, ("docs", "required_directories")):
        if not isinstance(rel, str) or not (ROOT / rel).is_dir():
            fail(f"{CONTRACT_MANIFEST_REL} required directory is missing: {rel}")
    for rel in require_list(manifest, ("docs", "required_files")):
        if not isinstance(rel, str) or not (ROOT / rel).is_file():
            fail(f"{CONTRACT_MANIFEST_REL} required file is missing: {rel}")

    manifest_text = "\n".join(manifest_strings(manifest)).lower()
    missing_concepts = {concept for concept in REQUIRED_FORBIDDEN_CONCEPTS if concept not in manifest_text}
    if missing_concepts:
        fail(f"{CONTRACT_MANIFEST_REL} missing forbidden TECH_SPEC concepts: {sorted(missing_concepts)}")
    validate_required_forbidden_openapi_operations(manifest)

    default_commands = require_list(manifest, ("security_regression", "default_commands"))
    if any("SYMPHONY_TEST_CODEX=1" in str(command) for command in default_commands):
        fail(f"{CONTRACT_MANIFEST_REL} default security commands must not require real Codex")
    real_codex_command = manifest.get("security_regression", {}).get("real_codex_command")
    if not isinstance(real_codex_command, str) or not real_codex_command.startswith("SYMPHONY_TEST_CODEX=1 "):
        fail(f"{CONTRACT_MANIFEST_REL} must keep real Codex behind SYMPHONY_TEST_CODEX=1")

    security_topic_slugs = {slugify_topic(topic) for topic in require_list(manifest, ("security_regression", "topics"))}
    missing_security_topics = REQUIRED_SECURITY_TOPIC_SLUGS - security_topic_slugs
    if missing_security_topics:
        fail(f"{CONTRACT_MANIFEST_REL} missing security regression topics: {sorted(missing_security_topics)}")

    cli_section_text = "\n".join(manifest_strings(manifest.get("cli", {})))
    cli_required_commands = set(require_list(manifest, ("cli", "required_commands")))
    missing_required_cli_commands = REQUIRED_CLI_COMMANDS - cli_required_commands
    if missing_required_cli_commands:
        fail(f"{CONTRACT_MANIFEST_REL} missing required CLI commands: {sorted(missing_required_cli_commands)}")
    for token in require_list(manifest, ("cli", "required_help_tokens")):
        if str(token) not in cli_section_text:
            fail(f"{CONTRACT_MANIFEST_REL} CLI help token is not covered by the CLI manifest: {token}")

    print(f"ok manifest {CONTRACT_MANIFEST_REL}")


def ref_name(ref: str) -> str:
    return ref.rsplit("/", 1)[-1]


def normalize_openapi_path(path: str) -> str:
    normalized = str(path).strip()
    if not normalized.startswith("/"):
        normalized = f"/{normalized}"
    if normalized == "/api/v1":
        normalized = "/"
    elif normalized.startswith("/api/v1/"):
        normalized = normalized.removeprefix("/api/v1")
    return re.sub(r"/:([^/]+)", r"/{\1}", normalized)


def route_candidates(route: str) -> tuple[str, ...]:
    normalized = normalize_openapi_path(route)
    prefixed = f"/api/v1{normalized}" if normalized != "/" else "/api/v1"
    return tuple(dict.fromkeys((route, normalized, prefixed)))


def forbidden_path_pattern(path: str) -> str:
    normalized = normalize_openapi_path(path)
    wildcard_suffix = normalized.endswith("/*")
    if wildcard_suffix:
        normalized = normalized[:-2]

    segments = []
    for segment in normalized.strip("/").split("/"):
        if not segment:
            continue
        if re.fullmatch(r"\{[^/{}]+\}", segment):
            segments.append(r"(?:\{[^/]+\}|:[^/]+|[^/]+)")
        else:
            segments.append(re.escape(segment).replace(r"\*", ".*"))
    body = "/" + "/".join(segments) if segments else "/"
    if wildcard_suffix:
        body += r"(?:/.*)?"
    return f"^{body}$"


def compile_forbidden_openapi_operation(entry: Any) -> tuple[str, re.Pattern[str], str]:
    if not isinstance(entry, dict):
        fail(f"{CONTRACT_MANIFEST_REL} openapi.forbidden_operations entries must be objects")
    method = str(entry.get("method", "")).upper()
    if method.lower() not in OPENAPI_HTTP_METHODS:
        fail(f"{CONTRACT_MANIFEST_REL} invalid forbidden operation method: {entry!r}")

    path = entry.get("path")
    path_pattern = entry.get("path_pattern")
    if bool(path) == bool(path_pattern):
        fail(f"{CONTRACT_MANIFEST_REL} forbidden operation must define exactly one of path/path_pattern: {entry!r}")
    if path:
        pattern_text = forbidden_path_pattern(str(path))
    else:
        pattern_text = str(path_pattern)
    try:
        pattern = re.compile(pattern_text, re.IGNORECASE)
    except re.error as exc:
        fail(f"{CONTRACT_MANIFEST_REL} invalid forbidden operation path pattern {entry!r}: {exc}")
    reason = str(entry.get("reason") or f"{method} {path or path_pattern}")
    return method, pattern, reason


def forbidden_operation_key(entry: Any) -> tuple[str, str] | None:
    if not isinstance(entry, dict) or not entry.get("path"):
        return None
    method = str(entry.get("method", "")).upper()
    if method.lower() not in OPENAPI_HTTP_METHODS:
        return None
    return method, normalize_openapi_path(str(entry["path"]))


def validate_required_forbidden_openapi_operations(manifest: dict[str, Any]) -> None:
    manifest_operations = require_list(manifest, ("openapi", "forbidden_operations"))
    manifest_operation_keys = {
        key for key in (forbidden_operation_key(entry) for entry in manifest_operations) if key is not None
    }
    for required in REQUIRED_FORBIDDEN_OPENAPI_OPERATIONS:
        required_key = forbidden_operation_key(required)
        if required_key is None or required_key not in manifest_operation_keys:
            fail(
                f"{CONTRACT_MANIFEST_REL} missing required forbidden OpenAPI operation: "
                f"{required['method']} {required['path']}"
            )


def assert_required_and_props_match(
    normalized_schema: dict[str, Any],
    openapi_schema: dict[str, Any],
    normalized_name: str,
    openapi_name: str,
) -> None:
    normalized_defs = normalized_schema["$defs"]
    openapi_components = openapi_schema["components"]["schemas"]
    normalized = normalized_defs[normalized_name]
    openapi = openapi_components[openapi_name]
    normalized_required = set(normalized.get("required", []))
    openapi_required = set(openapi.get("required", []))
    if normalized_required != openapi_required:
        fail(
            f"NormalizedIssue {normalized_name} required fields must match OpenAPI {openapi_name}: "
            f"{sorted(normalized_required ^ openapi_required)}"
        )
    missing_props = normalized_required - set(openapi.get("properties", {}))
    if missing_props:
        fail(f"OpenAPI {openapi_name} missing required NormalizedIssue properties: {sorted(missing_props)}")


def validate_json() -> None:
    schema_paths = sorted((ROOT / "schemas").rglob("*.json"))
    example_paths = sorted((ROOT / "examples").glob("*.json"))
    try:
        from jsonschema import Draft202012Validator  # type: ignore
    except Exception:  # pragma: no cover - optional dependency for bootstrap environments
        Draft202012Validator = None  # type: ignore[assignment]

    for path in schema_paths + example_paths:
        try:
            data = json.loads(path.read_text(encoding="utf-8"))
        except Exception as exc:  # noqa: BLE001
            fail(f"invalid JSON {path.relative_to(ROOT)}: {exc}")
        if Draft202012Validator is not None and is_under(path, ROOT / "schemas"):
            try:
                Draft202012Validator.check_schema(data)
            except Exception as exc:  # noqa: BLE001
                fail(f"invalid JSON Schema {path.relative_to(ROOT)}: {exc}")
        print(f"ok json {path.relative_to(ROOT)}")


def validate_review_packet_schema_contract() -> None:
    schema = load_json("schemas/review_packet.schema.json")
    top_level_required = schema.get("required")
    if not isinstance(top_level_required, list):
        fail("schemas/review_packet.schema.json required must be a list")
    failure_metadata_required = {"failure_code", "failure_message"}
    missing_failure_metadata = failure_metadata_required - set(top_level_required)
    if missing_failure_metadata:
        fail(
            "schemas/review_packet.schema.json required missing failure metadata fields: "
            f"{sorted(missing_failure_metadata)}"
        )

    properties = schema.get("properties")
    if not isinstance(properties, dict):
        fail("schemas/review_packet.schema.json properties must be an object")
    failure_code = properties.get("failure_code")
    if not isinstance(failure_code, dict):
        fail("schemas/review_packet.schema.json properties.failure_code must be an object")
    failure_code_enum = failure_code.get("enum")
    if not isinstance(failure_code_enum, list) or None not in failure_code_enum:
        fail("schemas/review_packet.schema.json failure_code must be canonical failure codes or null")
    failure_codes_schema = load_json("schemas/failure_codes.schema.json")
    canonical_failure_code = (
        failure_codes_schema.get("$defs", {}).get("failureCode", {}) if isinstance(failure_codes_schema, dict) else {}
    )
    canonical_failure_code_enum = canonical_failure_code.get("enum")
    if not isinstance(canonical_failure_code_enum, list):
        fail("schemas/failure_codes.schema.json $defs.failureCode.enum must be a list")
    expected_failure_codes = set(canonical_failure_code_enum)
    actual_failure_codes = {code for code in failure_code_enum if code is not None}
    if actual_failure_codes != expected_failure_codes:
        fail(f"Review packet failure_code mismatch: {sorted(actual_failure_codes ^ expected_failure_codes)}")
    failure_message = properties.get("failure_message")
    if not isinstance(failure_message, dict) or set(failure_message.get("type", [])) != {"string", "null"}:
        fail("schemas/review_packet.schema.json failure_message must allow string or null")

    handoff = schema.get("properties", {}).get("handoff")
    if not isinstance(handoff, dict):
        fail("schemas/review_packet.schema.json must define handoff object properties")

    handoff_required = handoff.get("required")
    if not isinstance(handoff_required, list):
        fail("schemas/review_packet.schema.json handoff.required must be a list")
    required_fields = {"summary", "tests", "risks", "verification", "followups", "target_state"}
    missing_required = required_fields - set(handoff_required)
    if missing_required:
        fail(f"schemas/review_packet.schema.json handoff.required missing fields: {sorted(missing_required)}")
    if "changed_files" in handoff_required:
        fail("schemas/review_packet.schema.json handoff.required must not include changed_files")

    handoff_properties = handoff.get("properties")
    if not isinstance(handoff_properties, dict):
        fail("schemas/review_packet.schema.json handoff.properties must be an object")
    followups = handoff_properties.get("followups")
    if not isinstance(followups, dict):
        fail("schemas/review_packet.schema.json handoff.properties.followups must be an object")
    if followups.get("type") != "array":
        fail("schemas/review_packet.schema.json handoff.properties.followups.type must be array")
    followup_items = followups.get("items")
    if not isinstance(followup_items, dict) or followup_items.get("type") != "string":
        fail("schemas/review_packet.schema.json handoff.properties.followups.items.type must be string")
    if "minItems" in followups:
        fail("schemas/review_packet.schema.json handoff.properties.followups must allow empty arrays")

    target_state = handoff_properties.get("target_state")
    if not isinstance(target_state, dict) or target_state.get("const") != "Human Review":
        fail("schemas/review_packet.schema.json handoff.properties.target_state.const must be Human Review")

    print("ok review packet schema handoff contract schemas/review_packet.schema.json")


def validate_workflow_config_schema_contract() -> None:
    schema = load_json("schemas/workflow_config.schema.json")
    if schema.get("additionalProperties") is not True:
        fail("schemas/workflow_config.schema.json top-level additionalProperties must be true")
    properties = schema.get("properties")
    if not isinstance(properties, dict):
        fail("schemas/workflow_config.schema.json must define known top-level properties")
    git = properties.get("git")
    if not isinstance(git, dict):
        fail("schemas/workflow_config.schema.json properties.git must be an object")
    assert_required_fields(git, "schemas/workflow_config.schema.json properties.git", frozenset({"branch_prefix"}))
    git_properties = git.get("properties")
    if not isinstance(git_properties, dict):
        fail("schemas/workflow_config.schema.json properties.git.properties must be an object")
    branch_prefix = git_properties.get("branch_prefix")
    if not isinstance(branch_prefix, dict) or branch_prefix.get("const") != "symphony":
        fail("schemas/workflow_config.schema.json git.branch_prefix.const must be 'symphony'")
    print("ok workflow config schema contract schemas/workflow_config.schema.json")


def validate_diagnostics_schema_contract() -> None:
    schema = load_json("schemas/diagnostics.schema.json")
    schema = require_dict(schema, "schemas/diagnostics.schema.json")
    assert_additional_properties_false(schema, "schemas/diagnostics.schema.json")
    assert_required_fields(schema, "schemas/diagnostics.schema.json", REQUIRED_DIAGNOSTICS_FIELDS)

    properties = require_dict(schema.get("properties"), "schemas/diagnostics.schema.json.properties")
    defs = require_dict(schema.get("$defs"), "schemas/diagnostics.schema.json.$defs")
    for name, definition in defs.items():
        if isinstance(definition, dict) and definition.get("type") == "object":
            assert_additional_properties_false(definition, f"schemas/diagnostics.schema.json.$defs.{name}")
    assert_required_field_map_covers_schema(
        defs,
        "schemas/diagnostics.schema.json.$defs.",
        DIAGNOSTICS_DEFINITION_REQUIRED_FIELDS,
    )
    for name, required_fields in DIAGNOSTICS_DEFINITION_REQUIRED_FIELDS.items():
        assert_required_fields(
            require_dict(defs.get(name), f"schemas/diagnostics.schema.json.$defs.{name}"),
            f"schemas/diagnostics.schema.json.$defs.{name}",
            required_fields,
        )
    assert_const(require_dict(properties.get("redacted"), "schemas/diagnostics.schema.json.properties.redacted"), "schemas/diagnostics.schema.json.properties.redacted", True)

    database = require_dict(defs.get("database"), "schemas/diagnostics.schema.json.$defs.database")
    assert_required_fields(database, "schemas/diagnostics.schema.json.$defs.database", DIAGNOSTICS_DATABASE_FIELDS)
    assert_enum(
        require_dict(defs.get("databaseVersionStatus"), "schemas/diagnostics.schema.json.$defs.databaseVersionStatus"),
        "schemas/diagnostics.schema.json.$defs.databaseVersionStatus",
        DIAGNOSTICS_DATABASE_VERSION_STATUS_ENUM,
    )

    workflow = require_dict(defs.get("workflow"), "schemas/diagnostics.schema.json.$defs.workflow")
    assert_required_fields(workflow, "schemas/diagnostics.schema.json.$defs.workflow", frozenset({"validation", "last_valid_config"}))
    workflow_validation = require_dict(defs.get("workflowValidation"), "schemas/diagnostics.schema.json.$defs.workflowValidation")
    assert_required_fields(workflow_validation, "schemas/diagnostics.schema.json.$defs.workflowValidation", frozenset({"valid", "warnings", "errors"}))
    last_valid_config = require_dict(defs.get("lastValidConfig"), "schemas/diagnostics.schema.json.$defs.lastValidConfig")
    assert_required_fields(last_valid_config, "schemas/diagnostics.schema.json.$defs.lastValidConfig", frozenset({"available", "path", "validated_at", "content_hash"}))

    daemon = require_dict(defs.get("daemon"), "schemas/diagnostics.schema.json.$defs.daemon")
    assert_required_fields(daemon, "schemas/diagnostics.schema.json.$defs.daemon", frozenset({"pid", "uptime_ms", "runtime_descriptor"}))
    runtime_descriptor = require_dict(defs.get("runtimeDescriptor"), "schemas/diagnostics.schema.json.$defs.runtimeDescriptor")
    assert_required_fields(runtime_descriptor, "schemas/diagnostics.schema.json.$defs.runtimeDescriptor", frozenset({"api_url", "tool_gateway_endpoint", "daemon_pid"}))

    codex = require_dict(defs.get("codex"), "schemas/diagnostics.schema.json.$defs.codex")
    assert_required_fields(codex, "schemas/diagnostics.schema.json.$defs.codex", frozenset({"available", "version", "support"}))
    codex_support = require_dict(defs.get("codexSupport"), "schemas/diagnostics.schema.json.$defs.codexSupport")
    assert_required_fields(codex_support, "schemas/diagnostics.schema.json.$defs.codexSupport", frozenset({"cli", "model", "sandbox"}))
    assert_enum(
        require_dict(defs.get("supportStatus"), "schemas/diagnostics.schema.json.$defs.supportStatus"),
        "schemas/diagnostics.schema.json.$defs.supportStatus",
        DIAGNOSTICS_SUPPORT_STATUS_ENUM,
    )

    git = require_dict(defs.get("git"), "schemas/diagnostics.schema.json.$defs.git")
    assert_required_fields(git, "schemas/diagnostics.schema.json.$defs.git", frozenset({"repository", "worktree"}))
    git_repository = require_dict(defs.get("gitRepository"), "schemas/diagnostics.schema.json.$defs.gitRepository")
    assert_required_fields(git_repository, "schemas/diagnostics.schema.json.$defs.gitRepository", frozenset({"status"}))
    git_worktree = require_dict(defs.get("gitWorktree"), "schemas/diagnostics.schema.json.$defs.gitWorktree")
    assert_required_fields(git_worktree, "schemas/diagnostics.schema.json.$defs.gitWorktree", frozenset({"status"}))
    assert_enum(
        require_dict(defs.get("gitStatus"), "schemas/diagnostics.schema.json.$defs.gitStatus"),
        "schemas/diagnostics.schema.json.$defs.gitStatus",
        DIAGNOSTICS_GIT_STATUS_ENUM,
    )

    redaction = require_dict(defs.get("redaction"), "schemas/diagnostics.schema.json.$defs.redaction")
    assert_required_fields(redaction, "schemas/diagnostics.schema.json.$defs.redaction", frozenset({"enabled", "export_redacted_only", "rules_version"}))
    redaction_properties = require_dict(redaction.get("properties"), "schemas/diagnostics.schema.json.$defs.redaction.properties")
    assert_const(require_dict(redaction_properties.get("enabled"), "schemas/diagnostics.schema.json.$defs.redaction.properties.enabled"), "schemas/diagnostics.schema.json.$defs.redaction.properties.enabled", True)
    assert_const(require_dict(redaction_properties.get("export_redacted_only"), "schemas/diagnostics.schema.json.$defs.redaction.properties.export_redacted_only"), "schemas/diagnostics.schema.json.$defs.redaction.properties.export_redacted_only", True)

    inconsistent_issue = require_dict(defs.get("inconsistentIssue"), "schemas/diagnostics.schema.json.$defs.inconsistentIssue")
    assert_required_fields(inconsistent_issue, "schemas/diagnostics.schema.json.$defs.inconsistentIssue", frozenset({"remediation"}))
    remediation = require_dict(defs.get("remediation"), "schemas/diagnostics.schema.json.$defs.remediation")
    assert_required_fields(remediation, "schemas/diagnostics.schema.json.$defs.remediation", frozenset({"action", "description"}))
    failure_summary = require_dict(defs.get("failureSummary"), "schemas/diagnostics.schema.json.$defs.failureSummary")
    assert_required_fields(failure_summary, "schemas/diagnostics.schema.json.$defs.failureSummary", frozenset({"failed_runs_count", "recent_failures"}))
    pause_summary = require_dict(defs.get("pauseSummary"), "schemas/diagnostics.schema.json.$defs.pauseSummary")
    assert_required_fields(pause_summary, "schemas/diagnostics.schema.json.$defs.pauseSummary", frozenset({"paused_dispatch_count", "paused_issue_refs"}))
    diagnostics_check = require_dict(defs.get("check"), "schemas/diagnostics.schema.json.$defs.check")
    assert_required_fields(diagnostics_check, "schemas/diagnostics.schema.json.$defs.check", frozenset({"name", "status"}))
    assert_enum(
        require_dict(require_dict(diagnostics_check.get("properties"), "schemas/diagnostics.schema.json.$defs.check.properties").get("status"), "schemas/diagnostics.schema.json.$defs.check.properties.status"),
        "schemas/diagnostics.schema.json.$defs.check.properties.status",
        DIAGNOSTICS_CHECK_STATUS_ENUM,
    )
    print("ok diagnostics schema contract schemas/diagnostics.schema.json")


def validate_sql() -> None:
    required_columns = {
        "db/schema/v1_app.sql": {
            "schema_meta": {"key", "value"},
            "projects": {"id", "repo_root", "project_db_path", "workflow_path", "last_opened_at"},
            "app_settings": {"key", "value_json", "updated_at"},
            "local_sessions": {"id", "project_id", "kind", "token_hash", "csrf_hash", "last_seen_at"},
            "open_tokens": {"id", "project_id", "token_hash", "expires_at", "consumed_at"},
            "runtime_descriptors": {"project_id", "api_url", "tool_gateway_endpoint", "daemon_pid"},
        },
        "db/schema/v1_project.sql": {
            "schema_meta": {"key", "value"},
            "project_settings": {"key", "value_json", "updated_at"},
            "issues": {"id", "sequence_no", "identifier", "state", "priority", "completed_at", "archived_at"},
            "issue_state_history": {"id", "issue_id", "from_state", "to_state", "actor_type"},
            "workspaces": {"id", "issue_id", "path", "branch_name", "base_ref_config", "base_ref", "base_sha"},
            "run_attempts": {"id", "issue_id", "attempt_no", "source_issue_state", "failure_code"},
            "approval_requests": {"id", "status", "timeout_ms", "expires_at"},
            "run_tool_tokens": {"id", "scope_json", "last_used_at"},
            "tool_calls": {"id", "status", "input_hash", "input_json_redacted", "output_hash", "output_json_redacted"},
            "handoffs": {"id", "payload_hash", "payload_json_redacted", "summary", "target_state", "submitted_at"},
            "artifacts": {"id", "kind", "path", "mime_type", "redacted"},
            "prompt_snapshots": {"id", "runtime_envelope_version", "rendered_prompt_hash", "context_json_path", "tool_manifest_path"},
            "review_packets": {"id", "packet_no", "status", "root_path", "diffstat_path"},
        },
    }

    for rel, table_columns in required_columns.items():
        path = ROOT / rel
        try:
            con = sqlite3.connect(":memory:")
            con.executescript(path.read_text(encoding="utf-8"))
            rows = con.execute("SELECT value FROM schema_meta WHERE key = 'schema_version'").fetchall()
            if rows != [("1",)]:
                fail(f"{rel} must contain schema_meta schema_version=1")
            for table, expected_cols in table_columns.items():
                actual_cols = {row[1] for row in con.execute(f"PRAGMA table_info({table})")}
                if not actual_cols:
                    fail(f"{rel} missing table {table}")
                missing = expected_cols - actual_cols
                if missing:
                    fail(f"{rel} table {table} missing columns: {sorted(missing)}")
            con.close()
        except SystemExit:
            raise
        except Exception as exc:  # noqa: BLE001
            fail(f"invalid SQLite DDL {rel}: {exc}")
        print(f"ok sql {rel}")


def validate_openapi_diagnostics_contract(data: dict[str, Any]) -> None:
    components = require_dict(data.get("components"), "OpenAPI components")
    schemas = require_dict(components.get("schemas"), "OpenAPI components.schemas")

    diagnostics = require_dict(schemas.get("Diagnostics"), "OpenAPI Diagnostics")
    for name, definition in schemas.items():
        if name.startswith("Diagnostics") and isinstance(definition, dict) and definition.get("type") == "object":
            assert_additional_properties_false(definition, f"OpenAPI {name}")
    openapi_diagnostics_schemas = {
        name: definition
        for name, definition in schemas.items()
        if name.startswith("Diagnostics") and isinstance(definition, dict) and definition.get("type") == "object"
    }
    assert_required_field_map_covers_schema(
        openapi_diagnostics_schemas,
        "OpenAPI ",
        OPENAPI_DIAGNOSTICS_REQUIRED_FIELDS,
    )
    for name, required_fields in OPENAPI_DIAGNOSTICS_REQUIRED_FIELDS.items():
        assert_required_fields(require_dict(schemas.get(name), f"OpenAPI {name}"), f"OpenAPI {name}", required_fields)
    assert_required_fields(diagnostics, "OpenAPI Diagnostics", REQUIRED_DIAGNOSTICS_FIELDS)
    diagnostics_properties = require_dict(diagnostics.get("properties"), "OpenAPI Diagnostics.properties")
    assert_const(require_dict(diagnostics_properties.get("redacted"), "OpenAPI Diagnostics.properties.redacted"), "OpenAPI Diagnostics.properties.redacted", True)

    database = require_dict(schemas.get("DiagnosticsDatabase"), "OpenAPI DiagnosticsDatabase")
    assert_required_fields(database, "OpenAPI DiagnosticsDatabase", DIAGNOSTICS_DATABASE_FIELDS)
    assert_enum(
        require_dict(schemas.get("DiagnosticsDatabaseVersionStatus"), "OpenAPI DiagnosticsDatabaseVersionStatus"),
        "OpenAPI DiagnosticsDatabaseVersionStatus",
        DIAGNOSTICS_DATABASE_VERSION_STATUS_ENUM,
    )

    workflow = require_dict(schemas.get("DiagnosticsWorkflow"), "OpenAPI DiagnosticsWorkflow")
    assert_required_fields(workflow, "OpenAPI DiagnosticsWorkflow", frozenset({"validation", "last_valid_config"}))
    workflow_validation = require_dict(schemas.get("DiagnosticsWorkflowValidation"), "OpenAPI DiagnosticsWorkflowValidation")
    assert_required_fields(workflow_validation, "OpenAPI DiagnosticsWorkflowValidation", frozenset({"valid", "warnings", "errors"}))
    last_valid_config = require_dict(schemas.get("DiagnosticsLastValidConfig"), "OpenAPI DiagnosticsLastValidConfig")
    assert_required_fields(last_valid_config, "OpenAPI DiagnosticsLastValidConfig", frozenset({"available", "path", "validated_at", "content_hash"}))

    daemon = require_dict(schemas.get("DiagnosticsDaemon"), "OpenAPI DiagnosticsDaemon")
    assert_required_fields(daemon, "OpenAPI DiagnosticsDaemon", frozenset({"pid", "uptime_ms", "runtime_descriptor"}))
    runtime_descriptor = require_dict(schemas.get("DiagnosticsRuntimeDescriptor"), "OpenAPI DiagnosticsRuntimeDescriptor")
    assert_required_fields(runtime_descriptor, "OpenAPI DiagnosticsRuntimeDescriptor", frozenset({"api_url", "tool_gateway_endpoint", "daemon_pid"}))

    codex = require_dict(schemas.get("DiagnosticsCodex"), "OpenAPI DiagnosticsCodex")
    assert_required_fields(codex, "OpenAPI DiagnosticsCodex", frozenset({"available", "version", "support"}))
    codex_support = require_dict(schemas.get("DiagnosticsCodexSupport"), "OpenAPI DiagnosticsCodexSupport")
    assert_required_fields(codex_support, "OpenAPI DiagnosticsCodexSupport", frozenset({"cli", "model", "sandbox"}))
    assert_enum(
        require_dict(schemas.get("DiagnosticsSupportStatus"), "OpenAPI DiagnosticsSupportStatus"),
        "OpenAPI DiagnosticsSupportStatus",
        DIAGNOSTICS_SUPPORT_STATUS_ENUM,
    )

    git = require_dict(schemas.get("DiagnosticsGit"), "OpenAPI DiagnosticsGit")
    assert_required_fields(git, "OpenAPI DiagnosticsGit", frozenset({"repository", "worktree"}))
    git_repository = require_dict(schemas.get("DiagnosticsGitRepository"), "OpenAPI DiagnosticsGitRepository")
    assert_required_fields(git_repository, "OpenAPI DiagnosticsGitRepository", frozenset({"status"}))
    git_worktree = require_dict(schemas.get("DiagnosticsGitWorktree"), "OpenAPI DiagnosticsGitWorktree")
    assert_required_fields(git_worktree, "OpenAPI DiagnosticsGitWorktree", frozenset({"status"}))
    assert_enum(
        require_dict(schemas.get("DiagnosticsGitStatus"), "OpenAPI DiagnosticsGitStatus"),
        "OpenAPI DiagnosticsGitStatus",
        DIAGNOSTICS_GIT_STATUS_ENUM,
    )

    redaction = require_dict(schemas.get("DiagnosticsRedaction"), "OpenAPI DiagnosticsRedaction")
    assert_required_fields(redaction, "OpenAPI DiagnosticsRedaction", frozenset({"enabled", "export_redacted_only", "rules_version"}))
    redaction_properties = require_dict(redaction.get("properties"), "OpenAPI DiagnosticsRedaction.properties")
    assert_const(require_dict(redaction_properties.get("enabled"), "OpenAPI DiagnosticsRedaction.properties.enabled"), "OpenAPI DiagnosticsRedaction.properties.enabled", True)
    assert_const(require_dict(redaction_properties.get("export_redacted_only"), "OpenAPI DiagnosticsRedaction.properties.export_redacted_only"), "OpenAPI DiagnosticsRedaction.properties.export_redacted_only", True)

    inconsistent_issue = require_dict(schemas.get("DiagnosticsInconsistentIssue"), "OpenAPI DiagnosticsInconsistentIssue")
    assert_required_fields(inconsistent_issue, "OpenAPI DiagnosticsInconsistentIssue", frozenset({"remediation"}))
    remediation = require_dict(schemas.get("DiagnosticsRemediation"), "OpenAPI DiagnosticsRemediation")
    assert_required_fields(remediation, "OpenAPI DiagnosticsRemediation", frozenset({"action", "description"}))
    failure_summary = require_dict(schemas.get("DiagnosticsFailureSummary"), "OpenAPI DiagnosticsFailureSummary")
    assert_required_fields(failure_summary, "OpenAPI DiagnosticsFailureSummary", frozenset({"failed_runs_count", "recent_failures"}))
    pause_summary = require_dict(schemas.get("DiagnosticsPauseSummary"), "OpenAPI DiagnosticsPauseSummary")
    assert_required_fields(pause_summary, "OpenAPI DiagnosticsPauseSummary", frozenset({"paused_dispatch_count", "paused_issue_refs"}))
    diagnostics_check = require_dict(schemas.get("DiagnosticsCheck"), "OpenAPI DiagnosticsCheck")
    assert_required_fields(diagnostics_check, "OpenAPI DiagnosticsCheck", frozenset({"name", "status"}))
    assert_enum(
        require_dict(require_dict(diagnostics_check.get("properties"), "OpenAPI DiagnosticsCheck.properties").get("status"), "OpenAPI DiagnosticsCheck.properties.status"),
        "OpenAPI DiagnosticsCheck.properties.status",
        DIAGNOSTICS_CHECK_STATUS_ENUM,
    )


def validate_openapi(manifest: dict[str, Any]) -> None:
    path = ROOT / "api/openapi.yaml"
    text = path.read_text(encoding="utf-8")
    if "openapi: 3.1.0" not in text:
        fail("api/openapi.yaml must declare openapi: 3.1.0")
    try:
        import yaml  # type: ignore
    except Exception as exc:  # noqa: BLE001
        fail(f"PyYAML is required to validate OpenAPI and WORKFLOW examples: {exc}")
    try:
        data = yaml.safe_load(text)
    except Exception as exc:  # noqa: BLE001
        fail(f"invalid OpenAPI YAML: {exc}")
    if data.get("openapi") != "3.1.0":
        fail("unexpected OpenAPI version")

    manifest_required_routes = set(require_list(manifest, ("openapi", "required_routes")))
    if manifest_required_routes != REQUIRED_OPENAPI_ROUTES:
        fail(
            f"{CONTRACT_MANIFEST_REL} OpenAPI required_routes drifted from validate_contracts.py: "
            f"{sorted(manifest_required_routes ^ REQUIRED_OPENAPI_ROUTES)}"
        )
    paths = data.get("paths", {})
    missing_routes = manifest_required_routes - set(paths)
    if missing_routes:
        fail(f"missing OpenAPI routes: {sorted(missing_routes)}")
    if "/approvals/{approval_id}/decision" in paths:
        fail("OpenAPI must use /approvals/{approval_id}/decide, not /decision")

    issue_list_get = paths.get("/issues", {}).get("get", {})
    if not isinstance(issue_list_get, dict):
        fail("OpenAPI /issues must define GET")
    issue_list_params = {
        param.get("name"): param for param in issue_list_get.get("parameters", []) if isinstance(param, dict)
    }
    required_issue_list_params = {"state", "label", "q", "dispatch_paused", "limit", "cursor", "sort"}
    missing_issue_list_params = required_issue_list_params - set(issue_list_params)
    if missing_issue_list_params:
        fail(f"OpenAPI GET /issues missing query parameters: {sorted(missing_issue_list_params)}")
    for name in ("state", "label"):
        param = issue_list_params[name]
        schema = param.get("schema", {})
        if param.get("in") != "query":
            fail(f"OpenAPI GET /issues {name} parameter must be in query")
        if param.get("style") != "form" or param.get("explode") is not True:
            fail(f"OpenAPI GET /issues {name} parameter must use style=form and explode=true")
        if schema.get("type") != "array":
            fail(f"OpenAPI GET /issues {name} parameter must be an array")
    if issue_list_params["q"].get("schema", {}).get("type") != "string":
        fail("OpenAPI GET /issues q parameter must be a string")
    if issue_list_params["dispatch_paused"].get("schema", {}).get("type") != "boolean":
        fail("OpenAPI GET /issues dispatch_paused parameter must be a boolean")
    limit_schema = issue_list_params["limit"].get("schema", {})
    if (
        limit_schema.get("type") != "integer"
        or limit_schema.get("minimum") != 1
        or limit_schema.get("maximum") != 200
        or limit_schema.get("default") != 50
    ):
        fail("OpenAPI GET /issues limit parameter must be integer minimum=1 maximum=200 default=50")
    if issue_list_params["cursor"].get("schema", {}).get("type") != "string":
        fail("OpenAPI GET /issues cursor parameter must be a string")
    sort_schema = issue_list_params["sort"].get("schema", {})
    if sort_schema.get("enum") != ["priority", "updated", "identifier"] or sort_schema.get("default") != "priority":
        fail("OpenAPI GET /issues sort parameter must use priority/updated/identifier enum with priority default")
    issue_list_responses = issue_list_get.get("responses", {})
    issue_list_bad_request = issue_list_responses.get("400")
    if (
        not isinstance(issue_list_bad_request, dict)
        or ref_name(issue_list_bad_request.get("$ref", "")) != "Error"
    ):
        fail("OpenAPI GET /issues must document 400 Error for invalid query/filter/sort/cursor")

    openapi_route_candidates = [(route, route_candidates(route)) for route in paths]
    for fragment in require_list(manifest, ("openapi", "forbidden_route_fragments")):
        fragment_text = str(fragment).lower()
        for route, candidates in openapi_route_candidates:
            if any(fragment_text in candidate.lower() for candidate in candidates):
                fail(f"OpenAPI route {route} contains forbidden fragment {fragment}")
    for pattern in require_list(manifest, ("openapi", "forbidden_route_patterns")):
        try:
            compiled = re.compile(str(pattern), re.IGNORECASE)
        except re.error as exc:
            fail(f"{CONTRACT_MANIFEST_REL} invalid forbidden route pattern {pattern!r}: {exc}")
        for route, candidates in openapi_route_candidates:
            if any(compiled.search(candidate) for candidate in candidates):
                fail(f"OpenAPI route {route} matches forbidden pattern {pattern}")

    forbidden_operations = [
        compile_forbidden_openapi_operation(entry)
        for entry in require_list(manifest, ("openapi", "forbidden_operations"))
    ]
    forbidden_operations.extend(
        compile_forbidden_openapi_operation(entry) for entry in REQUIRED_FORBIDDEN_OPENAPI_OPERATIONS
    )
    for route, ops in paths.items():
        if not isinstance(ops, dict):
            continue
        for method in ops:
            if method.lower() not in OPENAPI_HTTP_METHODS:
                continue
            actual_method = method.upper()
            candidates = route_candidates(route)
            for forbidden_method, forbidden_path, reason in forbidden_operations:
                if actual_method == forbidden_method and any(forbidden_path.search(candidate) for candidate in candidates):
                    fail(f"OpenAPI operation {actual_method} {route} is forbidden: {reason}")

    for handler_route in require_list(manifest, ("handler_route_inventory", "required_routes")):
        route_parts = str(handler_route).split()
        if len(route_parts) != 2 or not route_parts[1].startswith("/api/v1/"):
            fail(f"{CONTRACT_MANIFEST_REL} invalid handler route inventory entry: {handler_route}")
        openapi_route = route_parts[1].removeprefix("/api/v1")
        if openapi_route not in paths:
            fail(f"handler route inventory entry missing from OpenAPI paths: {handler_route}")

    for route in ("/issues/{issue_ref}/dispatch-pause", "/issues/{issue_ref}/dispatch-resume"):
        reason_schema = (
            paths.get(route, {})
            .get("post", {})
            .get("requestBody", {})
            .get("content", {})
            .get("application/json", {})
            .get("schema", {})
            .get("properties", {})
            .get("reason", {})
        )
        if not isinstance(reason_schema, dict) or reason_schema.get("pattern") != r"\S":
            fail(f"OpenAPI {route} request reason must use pattern '\\\\S'")

    render_preview_post = paths.get("/workflow/render-preview", {}).get("post")
    if not isinstance(render_preview_post, dict):
        fail("OpenAPI /workflow/render-preview must define POST")
    render_preview_request_ref = (
        render_preview_post.get("requestBody", {})
        .get("content", {})
        .get("application/json", {})
        .get("schema", {})
        .get("$ref")
    )
    if render_preview_request_ref != "#/components/schemas/WorkflowRenderPreviewRequest":
        fail("POST /workflow/render-preview request body must use WorkflowRenderPreviewRequest")
    render_preview_response_ref = (
        render_preview_post.get("responses", {})
        .get("200", {})
        .get("content", {})
        .get("application/json", {})
        .get("schema", {})
        .get("$ref")
    )
    if render_preview_response_ref != "#/components/schemas/WorkflowRenderPreviewEnvelope":
        fail("POST /workflow/render-preview 200 response must use WorkflowRenderPreviewEnvelope")

    workflow_render_request = data["components"]["schemas"]["WorkflowRenderPreviewRequest"]
    render_source = workflow_render_request["properties"]["source"]
    if render_source.get("enum") != ["effective", "candidate"] or render_source.get("default") != "effective":
        fail("WorkflowRenderPreviewRequest.source must default to effective with effective/candidate enum")
    candidate_condition_found = False
    for condition in workflow_render_request.get("allOf", []):
        condition_if = condition.get("if", {})
        condition_then = condition.get("then", {})
        required_source = "source" in condition_if.get("required", [])
        source_is_candidate = condition_if.get("properties", {}).get("source", {}).get("const") == "candidate"
        candidate_required = {
            tuple(branch.get("required", []))
            for branch in condition_then.get("anyOf", [])
            if isinstance(branch, dict)
        }
        requires_candidate_input = {
            ("candidate_workflow_md",),
            ("candidate_config",),
        }.issubset(candidate_required)
        if required_source and source_is_candidate and requires_candidate_input:
            candidate_condition_found = True
            break
    if not candidate_condition_found:
        fail(
            "WorkflowRenderPreviewRequest must require candidate_workflow_md or candidate_config "
            "when source is candidate"
        )

    workflow_render_preview_required = set(data["components"]["schemas"]["WorkflowRenderPreview"].get("required", []))
    expected_render_preview_required = {"source", "rendered_prompt_preview", "validation", "redactions_applied"}
    if not expected_render_preview_required.issubset(workflow_render_preview_required):
        fail(
            "WorkflowRenderPreview.required missing fields: "
            f"{sorted(expected_render_preview_required - workflow_render_preview_required)}"
        )

    # All non-error JSON 2xx responses must be enveloped.
    for route, ops in paths.items():
        for method, op in ops.items():
            if method.startswith("x-"):
                continue
            for status, response in op.get("responses", {}).items():
                if not str(status).startswith("2"):
                    continue
                content = response.get("content", {}) if isinstance(response, dict) else {}
                if "application/json" not in content:
                    continue
                schema = content["application/json"].get("schema", {})
                ref = schema.get("$ref", "") if isinstance(schema, dict) else ""
                if ref.endswith("Envelope"):
                    continue
                if any((part.get("$ref", "").endswith("Envelope") for part in schema.get("allOf", []) if isinstance(part, dict))):
                    continue
                fail(f"{method.upper()} {route} {status} JSON response must use an envelope schema")

    decision_enum = data["components"]["schemas"]["ApprovalDecisionRequest"]["properties"]["decision"]["enum"]
    if decision_enum != ["approve_once", "approve_for_run", "approve_for_session", "deny", "cancel_run"]:
        fail("ApprovalDecisionRequest enum drifted from TECH_SPEC")

    failure_schema = load_json("schemas/failure_codes.schema.json")
    expected_failure_codes = set(failure_schema["$defs"]["failureCode"]["enum"])
    openapi_failure_codes = set(data["components"]["schemas"]["FailureCode"]["enum"])
    if expected_failure_codes != openapi_failure_codes:
        fail(f"FailureCode mismatch: {sorted(expected_failure_codes ^ openapi_failure_codes)}")
    expected_api_errors = set(failure_schema["$defs"]["apiErrorCode"]["enum"])
    openapi_api_errors = set(data["components"]["schemas"]["ApiErrorCode"]["enum"])
    if expected_api_errors != openapi_api_errors:
        fail(f"ApiErrorCode mismatch: {sorted(expected_api_errors ^ openapi_api_errors)}")

    schemas = data["components"]["schemas"]
    issue_list_data: dict[str, Any] | None = None
    for part in schemas["IssueListEnvelope"].get("allOf", []):
        candidate = part.get("properties", {}).get("data") if isinstance(part, dict) else None
        if isinstance(candidate, dict):
            issue_list_data = candidate
            break
    if not issue_list_data or ref_name(issue_list_data.get("$ref", "")) != "IssueListPage":
        fail("OpenAPI IssueListEnvelope.data must reference IssueListPage")

    issue_list_page = schemas["IssueListPage"]
    issue_list_page_required = set(issue_list_page.get("required", []))
    if not {"items", "page"}.issubset(issue_list_page_required):
        fail("OpenAPI IssueListPage.required must include items and page")
    issue_list_page_props = issue_list_page.get("properties", {})
    items_schema = issue_list_page_props.get("items", {})
    if items_schema.get("type") != "array" or ref_name(items_schema.get("items", {}).get("$ref", "")) != "Issue":
        fail("OpenAPI IssueListPage.items must be an array of Issue")
    if ref_name(issue_list_page_props.get("page", {}).get("$ref", "")) != "IssueListPageMeta":
        fail("OpenAPI IssueListPage.page must reference IssueListPageMeta")

    issue_list_page_meta = schemas["IssueListPageMeta"]
    if set(issue_list_page_meta.get("required", [])) != {"limit", "next_cursor", "has_more"}:
        fail("OpenAPI IssueListPageMeta.required must be limit, next_cursor, has_more")
    issue_list_page_meta_props = issue_list_page_meta.get("properties", {})
    if issue_list_page_meta_props.get("limit", {}).get("type") != "integer":
        fail("OpenAPI IssueListPageMeta.limit must be an integer")
    if set(issue_list_page_meta_props.get("next_cursor", {}).get("type", [])) != {"string", "null"}:
        fail("OpenAPI IssueListPageMeta.next_cursor must allow string or null")
    if issue_list_page_meta_props.get("has_more", {}).get("type") != "boolean":
        fail("OpenAPI IssueListPageMeta.has_more must be a boolean")

    transition_request = schemas["IssueTransitionRequest"]
    transition_props = transition_request.get("properties", {})
    if "duplicate_of" not in transition_props:
        fail("OpenAPI IssueTransitionRequest must define duplicate_of")
    if "canonical_issue_ref" in transition_props:
        fail("OpenAPI IssueTransitionRequest must not define canonical_issue_ref")
    reason_schema = transition_props.get("reason", {})
    if (
        not isinstance(reason_schema, dict)
        or reason_schema.get("type") != "string"
        or reason_schema.get("minLength") != 1
        or reason_schema.get("pattern") != r"\S"
    ):
        fail("OpenAPI IssueTransitionRequest.reason must require a non-blank string")
    transition_conditionals = transition_request.get("allOf", [])
    if not isinstance(transition_conditionals, list):
        fail("OpenAPI IssueTransitionRequest.allOf must define transition guard conditionals")
    requires_reason_for_terminal_states = any(
        isinstance(condition, dict)
        and set(condition.get("if", {}).get("properties", {}).get("state", {}).get("enum", []))
        == {"Blocked", "Cancelled", "Duplicate"}
        and "reason" in condition.get("then", {}).get("required", [])
        for condition in transition_conditionals
    )
    forbids_duplicate_of_for_non_duplicate_states = any(
        isinstance(condition, dict)
        and condition.get("if", {}).get("properties", {}).get("state", {}).get("not", {}).get("const")
        == "Duplicate"
        and "duplicate_of" in condition.get("then", {}).get("not", {}).get("required", [])
        for condition in transition_conditionals
    )
    if not requires_reason_for_terminal_states:
        fail("OpenAPI IssueTransitionRequest must require reason for Blocked/Cancelled/Duplicate transitions")
    if not forbids_duplicate_of_for_non_duplicate_states:
        fail("OpenAPI IssueTransitionRequest must forbid duplicate_of unless state is Duplicate")

    normalized_issue_schema = load_json("schemas/normalized_issue.schema.json")
    normalized_failure_codes = set(normalized_issue_schema["$defs"]["failureCode"]["enum"])
    if expected_failure_codes != normalized_failure_codes:
        fail(f"NormalizedIssue failureCode mismatch: {sorted(expected_failure_codes ^ normalized_failure_codes)}")
    normalized_issue_required = set(normalized_issue_schema["required"])
    openapi_issue = data["components"]["schemas"]["Issue"]
    openapi_issue_required = set(openapi_issue.get("required", []))
    if normalized_issue_required != openapi_issue_required:
        fail(
            "OpenAPI Issue required fields must match schemas/normalized_issue.schema.json: "
            f"{sorted(normalized_issue_required ^ openapi_issue_required)}"
        )
    missing_issue_props = normalized_issue_required - set(openapi_issue.get("properties", {}))
    if missing_issue_props:
        fail(f"OpenAPI Issue schema missing NormalizedIssue properties: {sorted(missing_issue_props)}")

    for prop in ("active_run_id", "latest_run_id", "latest_review_packet_id"):
        normalized_pattern = normalized_issue_schema["properties"][prop].get("pattern")
        openapi_pattern = openapi_issue["properties"][prop].get("pattern")
        if normalized_pattern != openapi_pattern:
            fail(
                f"OpenAPI Issue.{prop} pattern must match schemas/normalized_issue.schema.json: "
                f"expected {normalized_pattern!r}, got {openapi_pattern!r}"
            )

    expected_refs = {
        "blocked_by": "IssueRefSummary",
        "blocks": "IssueRefSummary",
        "workspace": "WorkspaceSummary",
        "git": "GitSummary",
        "latest_run": "RunSummary",
        "latest_review_packet": "IssueReviewPacketSummary",
    }
    for prop, expected_ref in expected_refs.items():
        schema = openapi_issue["properties"][prop]
        if prop in {"blocked_by", "blocks"}:
            actual_ref = schema.get("items", {}).get("$ref", "")
        else:
            actual_ref = next((item.get("$ref", "") for item in schema.get("anyOf", []) if "$ref" in item), "")
        if ref_name(actual_ref) != expected_ref:
            fail(f"OpenAPI Issue.{prop} must reference {expected_ref}, got {actual_ref or 'none'}")

    summary_pairs = [
        ("issueRef", "IssueRefSummary"),
        ("workspaceSummary", "WorkspaceSummary"),
        ("gitSummary", "GitSummary"),
        ("runSummary", "RunSummary"),
        ("reviewPacketSummary", "IssueReviewPacketSummary"),
    ]
    for normalized_name, openapi_name in summary_pairs:
        assert_required_and_props_match(normalized_issue_schema, data, normalized_name, openapi_name)

    assert_failure_code_or_null(
        normalized_issue_schema["$defs"]["runSummary"]["properties"]["failure_code"],
        "NormalizedIssue runSummary.failure_code",
        "#/$defs/failureCode",
    )
    assert_failure_code_or_null(
        data["components"]["schemas"]["RunSummary"]["properties"]["failure_code"],
        "OpenAPI RunSummary.failure_code",
        "#/components/schemas/FailureCode",
    )
    review_packet_summary = data["components"]["schemas"]["ReviewPacketSummary"]
    assert_required_fields(review_packet_summary, "OpenAPI ReviewPacketSummary", frozenset({"failure_code", "failure_message"}))
    assert_failure_code_or_null(
        review_packet_summary["properties"]["failure_code"],
        "OpenAPI ReviewPacketSummary.failure_code",
        "#/components/schemas/FailureCode",
    )
    review_packet_failure_message = review_packet_summary["properties"]["failure_message"]
    if set(review_packet_failure_message.get("type", [])) != {"string", "null"}:
        fail("OpenAPI ReviewPacketSummary.failure_message must allow string or null")

    normalized_states = normalized_issue_schema["$defs"]["issueState"]["enum"]
    openapi_states = data["components"]["schemas"]["IssueState"]["enum"]
    if normalized_states != openapi_states:
        fail("NormalizedIssue issueState enum must match OpenAPI IssueState enum")

    run_event_schema = load_json("schemas/run_event.schema.json")
    if "seq" not in run_event_schema.get("required", []):
        fail("schemas/run_event.schema.json must require seq for SSE replay IDs")

    validate_openapi_diagnostics_contract(data)
    print("ok openapi api/openapi.yaml")


def _extract_front_matter(text: str) -> dict[str, Any]:
    if not text.startswith("---\n"):
        return {}
    end = text.find("\n---", 4)
    if end == -1:
        fail("examples/WORKFLOW.default.md has unterminated YAML front matter")
    import yaml  # type: ignore

    parsed = yaml.safe_load(text[4:end])
    if not isinstance(parsed, dict):
        fail("examples/WORKFLOW.default.md front matter must be an object")
    return parsed


def validate_workflow_example() -> None:
    workflow_path = ROOT / "examples/WORKFLOW.default.md"
    config = _extract_front_matter(workflow_path.read_text(encoding="utf-8"))
    for section, key in [("workspace", "root"), ("git", "repo_root")]:
        value = config.get(section, {}).get(key)
        if isinstance(value, str) and re.search(r"\{\{|\}\}", value):
            fail(f"{workflow_path.relative_to(ROOT)} {section}.{key} must not contain Liquid interpolation")
    try:
        from jsonschema import Draft202012Validator  # type: ignore
    except Exception:
        print("warn workflow schema validation skipped: jsonschema not installed")
        return
    schema = load_json("schemas/workflow_config.schema.json")
    errors = sorted(Draft202012Validator(schema).iter_errors(config), key=lambda e: e.path)
    if errors:
        first = errors[0]
        fail(f"WORKFLOW.default.md does not validate: path={list(first.path)} error={first.message}")
    print("ok workflow examples/WORKFLOW.default.md")


def validate_tool_examples() -> None:
    try:
        from jsonschema import Draft202012Validator  # type: ignore
    except Exception:
        print("warn tool example schema validation skipped: jsonschema not installed")
        return

    example_schema_pairs = [
        ("examples/handoff.json", "schemas/tools/handoff_submit.input.schema.json", "handoff.submit"),
        ("examples/followup.json", "schemas/tools/followup_create.input.schema.json", "followup.create"),
    ]
    gateway_schema = load_json("schemas/tool_gateway.schema.json")
    for example_rel, schema_rel, tool_name in example_schema_pairs:
        payload = load_json(example_rel)
        input_schema = load_json(schema_rel)
        Draft202012Validator(input_schema).validate(payload)
        Draft202012Validator(gateway_schema).validate({"tool": tool_name, "input": payload})
        print(f"ok example {example_rel} -> {schema_rel}")


def validate_tool_gateway_manifest(manifest: dict[str, Any]) -> None:
    gateway_schema = load_json("schemas/tool_gateway.schema.json")
    manifest_tools = set(require_list(manifest, ("tool_gateway", "registry_tools")))
    schema_tools = set(gateway_schema["$defs"]["toolName"]["enum"])
    if schema_tools != manifest_tools:
        fail(f"Tool Gateway registry mismatch with {CONTRACT_MANIFEST_REL}: {sorted(schema_tools ^ manifest_tools)}")
    print("ok tool gateway registry schemas/tool_gateway.schema.json")


def forbidden_command_pattern(command: Any) -> re.Pattern[str]:
    tokens = str(command).split()
    if not tokens:
        fail(f"{CONTRACT_MANIFEST_REL} cli.forbidden_commands must not contain blank commands")
    pattern_text = r"(?<![A-Za-z0-9_-])" + r"\s+".join(re.escape(token) for token in tokens) + r"(?![A-Za-z0-9_-])"
    return re.compile(pattern_text, re.IGNORECASE)


def validate_agent_work_order_scope(manifest: dict[str, Any]) -> None:
    patterns = [forbidden_command_pattern(command) for command in require_list(manifest, ("cli", "forbidden_commands"))]
    for pattern in require_list(manifest, ("docs", "agent_work_order_forbidden_command_like_patterns")):
        try:
            patterns.append(re.compile(str(pattern), re.IGNORECASE))
        except re.error as exc:
            fail(f"{CONTRACT_MANIFEST_REL} invalid agent work order forbidden pattern {pattern!r}: {exc}")

    for path in sorted((ROOT / "docs/agent_work_orders").glob("*.md")):
        text = path.read_text(encoding="utf-8")
        for pattern in patterns:
            match = pattern.search(text)
            if match:
                fail(
                    f"{path.relative_to(ROOT)} contains forbidden v1 command-like capability "
                    f"{match.group(0)!r}"
                )
    print("ok docs docs/agent_work_orders/*.md v1 scope")


def main() -> None:
    manifest = load_contract_manifest()
    validate_contract_manifest(manifest)
    validate_json()
    validate_review_packet_schema_contract()
    validate_workflow_config_schema_contract()
    validate_diagnostics_schema_contract()
    validate_sql()
    validate_openapi(manifest)
    validate_tool_gateway_manifest(manifest)
    validate_agent_work_order_scope(manifest)
    validate_workflow_example()
    validate_tool_examples()
    print("contract validation passed")


if __name__ == "__main__":
    main()
