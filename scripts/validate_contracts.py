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


def ref_name(ref: str) -> str:
    return ref.rsplit("/", 1)[-1]


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


def validate_openapi() -> None:
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

    required_routes = {
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
    paths = data.get("paths", {})
    missing_routes = required_routes - set(paths)
    if missing_routes:
        fail(f"missing OpenAPI routes: {sorted(missing_routes)}")
    if "/approvals/{approval_id}/decision" in paths:
        fail("OpenAPI must use /approvals/{approval_id}/decide, not /decision")

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

    normalized_failure_code = normalized_issue_schema["$defs"]["runSummary"]["properties"]["failure_code"]
    normalized_failure_refs = {item.get("$ref", "") for item in normalized_failure_code.get("anyOf", [])}
    normalized_failure_null = any(item.get("type") == "null" for item in normalized_failure_code.get("anyOf", []))
    if "#/$defs/failureCode" not in normalized_failure_refs or not normalized_failure_null:
        fail("NormalizedIssue runSummary.failure_code must be failureCode or null")

    openapi_failure_code = data["components"]["schemas"]["RunSummary"]["properties"]["failure_code"]
    openapi_failure_refs = {item.get("$ref", "") for item in openapi_failure_code.get("anyOf", [])}
    openapi_failure_null = any(item.get("type") == "null" for item in openapi_failure_code.get("anyOf", []))
    if "#/components/schemas/FailureCode" not in openapi_failure_refs or not openapi_failure_null:
        fail("OpenAPI RunSummary.failure_code must be FailureCode or null")

    normalized_states = normalized_issue_schema["$defs"]["issueState"]["enum"]
    openapi_states = data["components"]["schemas"]["IssueState"]["enum"]
    if normalized_states != openapi_states:
        fail("NormalizedIssue issueState enum must match OpenAPI IssueState enum")

    run_event_schema = load_json("schemas/run_event.schema.json")
    if "seq" not in run_event_schema.get("required", []):
        fail("schemas/run_event.schema.json must require seq for SSE replay IDs")
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


def main() -> None:
    validate_json()
    validate_sql()
    validate_openapi()
    validate_workflow_example()
    validate_tool_examples()
    print("contract validation passed")


if __name__ == "__main__":
    main()
