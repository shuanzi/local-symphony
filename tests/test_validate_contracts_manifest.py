#!/usr/bin/env python3
"""Focused tests for executable manifest contract coverage."""
from __future__ import annotations

import copy
import importlib.util
import json
import shutil
import tempfile
import unittest
from pathlib import Path
from typing import Any

import yaml


ROOT = Path(__file__).resolve().parents[1]

REQUIRED_FORBIDDEN_OPERATION_CASES = (
    ("git push", "POST", "/git/{issue_ref}/push"),
    ("git publish", "POST", "/git/{issue_ref}/publish"),
    ("git pr", "POST", "/git/{issue_ref}/pr"),
    ("git create-pr", "POST", "/git/{issue_ref}/create-pr"),
    ("db backup", "POST", "/db/backup"),
    ("db restore", "POST", "/db/restore"),
    ("db migrate", "POST", "/db/migrate"),
    ("audit", "GET", "/audit"),
    ("workspace delete", "POST", "/workspaces/{issue_ref}/delete"),
    ("workspace reset", "POST", "/workspaces/{issue_ref}/reset"),
    ("workspace clean", "POST", "/workspaces/{issue_ref}/clean"),
    ("workspace rebase", "POST", "/workspaces/{issue_ref}/rebase"),
    ("workspace-delete", "POST", "/workspace-delete"),
    ("secrets create", "POST", "/secrets"),
    ("secrets patch", "PATCH", "/secrets/*"),
    ("project settings", "PATCH", "/projects/{project_id}/settings"),
    ("issue delete", "DELETE", "/issues/{issue_ref}"),
    ("state mutation", "PATCH", "/state/*"),
)

FORBIDDEN_OPENAPI_INJECTION_CASES = (
    ("git", "POST", "/git/{issue_ref}/push"),
    ("db", "POST", "/db/backup"),
    ("audit", "GET", "/audit"),
    ("secrets create", "POST", "/secrets"),
    ("secrets patch", "PATCH", "/secrets/token"),
    ("workspace", "POST", "/workspaces/{issue_ref}/reset"),
    ("issue delete", "DELETE", "/issues/{issue_ref}"),
    ("state mutation", "PATCH", "/state/anything"),
    ("project settings", "PATCH", "/projects/{project_id}/settings"),
    ("prefixed issue delete", "DELETE", "/api/v1/issues/{issue_ref}"),
)

REQUIRED_REDACTION_GOLDEN_FIXTURE_SURFACES = ("prompt", "codex_log", "secret", "diagnostics")


def load_validator():
    spec = importlib.util.spec_from_file_location("validate_contracts", ROOT / "scripts" / "validate_contracts.py")
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load validate_contracts.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def copy_contract_root(temp_root: Path) -> None:
    for rel in ("api", "schemas"):
        shutil.copytree(ROOT / rel, temp_root / rel)


def load_manifest() -> dict[str, Any]:
    return json.loads((ROOT / "docs/testing/CONTRACT_VALIDATION_MANIFEST.json").read_text(encoding="utf-8"))


def write_manifest_required_paths(temp_root: Path, manifest: dict[str, Any]) -> None:
    for rel in manifest["docs"]["required_directories"]:
        (temp_root / rel).mkdir(parents=True, exist_ok=True)
    for rel in manifest["docs"]["required_files"]:
        path = temp_root / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text("placeholder\n", encoding="utf-8")


def is_forbidden_operation(operation: dict[str, Any], method: str, path: str) -> bool:
    return operation.get("method") == method and operation.get("path") == path


class ManifestContractTests(unittest.TestCase):
    def test_manifest_declares_required_contract_inventories(self) -> None:
        validator = load_validator()
        manifest = validator.load_contract_manifest()

        self.assertIn("/issues/{issue_ref}/dispatch-pause", manifest["openapi"]["required_routes"])
        self.assertIn("/issues/{issue_ref}/dispatch-resume", manifest["openapi"]["required_routes"])
        self.assertIn("symphony issue dispatch-pause", manifest["cli"]["required_commands"])
        self.assertIn("symphony issue dispatch-resume", manifest["cli"]["required_commands"])
        self.assertIn("symphony issue duplicate remove", manifest["cli"]["required_commands"])
        self.assertIn("--duplicate-of", manifest["cli"]["required_help_tokens"])
        self.assertIn("issue.get", manifest["tool_gateway"]["registry_tools"])
        self.assertGreaterEqual(len(manifest["handler_route_inventory"]["required_routes"]), 1)
        self.assertGreaterEqual(len(manifest["dashboard_action_inventory"]["required_actions"]), 1)
        self.assertGreaterEqual(len(manifest["security_regression"]["topics"]), 1)

    def test_manifest_redaction_golden_fixture_metadata_is_validated(self) -> None:
        validator = load_validator()

        cases = (
            (
                "missing committed fixture path",
                {
                    "path": "docs/testing/redaction-golden/missing.json",
                    "covers": list(REQUIRED_REDACTION_GOLDEN_FIXTURE_SURFACES),
                },
            ),
            (
                "incomplete fixture metadata",
                {
                    "path": "docs/testing/redaction-golden/redaction-golden.json",
                },
            ),
            (
                "missing prompt coverage",
                {
                    "path": "docs/testing/redaction-golden/redaction-golden.json",
                    "covers": ["codex_log", "secret", "diagnostics"],
                },
            ),
            (
                "missing Codex log coverage",
                {
                    "path": "docs/testing/redaction-golden/redaction-golden.json",
                    "covers": ["prompt", "secret", "diagnostics"],
                },
            ),
            (
                "missing secret coverage",
                {
                    "path": "docs/testing/redaction-golden/redaction-golden.json",
                    "covers": ["prompt", "codex_log", "diagnostics"],
                },
            ),
            (
                "missing diagnostics coverage",
                {
                    "path": "docs/testing/redaction-golden/redaction-golden.json",
                    "covers": ["prompt", "codex_log", "secret"],
                },
            ),
        )

        for name, fixture in cases:
            with self.subTest(name=name):
                manifest = load_manifest()
                manifest["security_regression"]["redaction_golden_fixtures"] = [fixture]

                with self.assertRaises(SystemExit):
                    validator.validate_contract_manifest(manifest)

    def test_manifest_redaction_golden_fixture_path_must_stay_under_repo(self) -> None:
        validator = load_validator()
        manifest = load_manifest()

        with tempfile.TemporaryDirectory() as temp_dir:
            temp_root = Path(temp_dir)
            fixture_path = temp_root / "external-redaction-golden.json"
            fixture_path.write_text((ROOT / "docs/testing/redaction-golden/redaction-golden.json").read_text(encoding="utf-8"), encoding="utf-8")
            manifest["security_regression"]["redaction_golden_fixtures"] = [
                {
                    "path": str(fixture_path),
                    "covers": list(REQUIRED_REDACTION_GOLDEN_FIXTURE_SURFACES),
                }
            ]

            with self.assertRaises(SystemExit):
                validator.validate_contract_manifest(manifest)

    def test_manifest_redaction_golden_fixture_case_content_is_validated(self) -> None:
        validator = load_validator()

        valid_fixture_cases = [
            {
                "surface": surface,
                "input": f"{surface} includes SYNTHETIC_SECRET_TOKEN.",
                "redacted": f"{surface} includes [REDACTED].",
            }
            for surface in REQUIRED_REDACTION_GOLDEN_FIXTURE_SURFACES
        ]
        cases = (
            (
                "missing diagnostics case",
                [
                    case
                    for case in valid_fixture_cases
                    if case["surface"] != "diagnostics"
                ],
            ),
            (
                "blank input",
                [
                    {**case, "input": "   "} if case["surface"] == "prompt" else case
                    for case in valid_fixture_cases
                ],
            ),
            (
                "blank redacted",
                [
                    {**case, "redacted": ""} if case["surface"] == "codex_log" else case
                    for case in valid_fixture_cases
                ],
            ),
            (
                "redacted leaks sentinel",
                [
                    {**case, "redacted": case["input"]} if case["surface"] == "secret" else case
                    for case in valid_fixture_cases
                ],
            ),
            (
                "redacted leaks second synthetic sentinel",
                [
                    {
                        **case,
                        "input": "prompt includes SYNTHETIC_SECRET_ONE and SYNTHETIC_SECRET_TWO.",
                        "redacted": "prompt includes [REDACTED] and SYNTHETIC_SECRET_TWO.",
                    }
                    if case["surface"] == "prompt"
                    else case
                    for case in valid_fixture_cases
                ],
            ),
            (
                "redacted does not match synthetic golden output",
                [
                    {**case, "redacted": "arbitrary"} if case["surface"] == "diagnostics" else case
                    for case in valid_fixture_cases
                ],
            ),
        )

        for name, fixture_cases in cases:
            with self.subTest(name=name):
                manifest = load_manifest()

                with tempfile.TemporaryDirectory() as temp_dir:
                    temp_root = Path(temp_dir)
                    write_manifest_required_paths(temp_root, manifest)
                    fixture_rel = manifest["security_regression"]["redaction_golden_fixtures"][0]["path"]
                    fixture_path = temp_root / fixture_rel
                    fixture_path.parent.mkdir(parents=True, exist_ok=True)
                    fixture_path.write_text(
                        json.dumps(
                            {
                                "name": "redaction-golden",
                                "description": "Synthetic fixture for content validation.",
                                "cases": fixture_cases,
                            },
                            indent=2,
                        )
                        + "\n",
                        encoding="utf-8",
                    )

                    old_root = validator.ROOT
                    validator.ROOT = temp_root
                    try:
                        with self.assertRaises(SystemExit):
                            validator.validate_contract_manifest(manifest)
                    finally:
                        validator.ROOT = old_root

    def test_openapi_forbidden_delete_issue_operation_fails(self) -> None:
        validator = load_validator()
        manifest = load_manifest()

        with tempfile.TemporaryDirectory() as temp_dir:
            temp_root = Path(temp_dir)
            copy_contract_root(temp_root)
            openapi_path = temp_root / "api/openapi.yaml"
            openapi = yaml.safe_load(openapi_path.read_text(encoding="utf-8"))
            openapi["paths"]["/issues/{issue_ref}"]["delete"] = {
                "operationId": "deleteIssue",
                "parameters": [{"$ref": "#/components/parameters/IssueRef"}],
                "responses": {"204": {"description": "Deleted"}},
            }
            openapi_path.write_text(yaml.safe_dump(openapi, sort_keys=False), encoding="utf-8")

            old_root = validator.ROOT
            validator.ROOT = temp_root
            try:
                with self.assertRaises(SystemExit):
                    validator.validate_openapi(manifest)
            finally:
                validator.ROOT = old_root

    def test_openapi_forbidden_operation_fails_when_manifest_omits_it(self) -> None:
        validator = load_validator()
        manifest = load_manifest()
        manifest["openapi"]["forbidden_operations"] = [
            operation
            for operation in manifest["openapi"]["forbidden_operations"]
            if not (operation.get("method") == "DELETE" and operation.get("path") == "/issues/{issue_ref}")
        ]

        with tempfile.TemporaryDirectory() as temp_dir:
            temp_root = Path(temp_dir)
            copy_contract_root(temp_root)
            openapi_path = temp_root / "api/openapi.yaml"
            openapi = yaml.safe_load(openapi_path.read_text(encoding="utf-8"))
            openapi["paths"]["/issues/{issue_ref}"]["delete"] = {
                "operationId": "deleteIssue",
                "parameters": [{"$ref": "#/components/parameters/IssueRef"}],
                "responses": {"204": {"description": "Deleted"}},
            }
            openapi_path.write_text(yaml.safe_dump(openapi, sort_keys=False), encoding="utf-8")

            old_root = validator.ROOT
            validator.ROOT = temp_root
            try:
                with self.assertRaises(SystemExit):
                    validator.validate_openapi(manifest)
            finally:
                validator.ROOT = old_root

    def test_manifest_missing_each_required_forbidden_operation_fails(self) -> None:
        validator = load_validator()

        for name, method, forbidden_path in REQUIRED_FORBIDDEN_OPERATION_CASES:
            with self.subTest(name=name):
                manifest = load_manifest()
                manifest["openapi"]["forbidden_operations"] = [
                    operation
                    for operation in manifest["openapi"]["forbidden_operations"]
                    if not is_forbidden_operation(operation, method, forbidden_path)
                ]

                with self.assertRaises(SystemExit):
                    validator.validate_contract_manifest(manifest)

    def test_openapi_fixed_forbidden_operations_fail_when_manifest_omits_them(self) -> None:
        validator = load_validator()

        for name, method, forbidden_path in FORBIDDEN_OPENAPI_INJECTION_CASES:
            with self.subTest(name=name):
                manifest = load_manifest()
                manifest["openapi"]["forbidden_route_fragments"] = ["__not_a_real_fragment__"]
                manifest["openapi"]["forbidden_route_patterns"] = ["a^"]
                manifest["openapi"]["forbidden_operations"] = [
                    {
                        "method": "GET",
                        "path": "/__not_a_real_route__",
                        "reason": "dummy unrelated forbidden operation",
                    }
                ]

                with tempfile.TemporaryDirectory() as temp_dir:
                    temp_root = Path(temp_dir)
                    copy_contract_root(temp_root)
                    openapi_path = temp_root / "api/openapi.yaml"
                    openapi = yaml.safe_load(openapi_path.read_text(encoding="utf-8"))
                    openapi["paths"].setdefault(forbidden_path, {})[method.lower()] = {
                        "operationId": f"forbidden{name.title().replace(' ', '')}",
                        "responses": {"204": {"description": "Forbidden test operation"}},
                    }
                    openapi_path.write_text(yaml.safe_dump(openapi, sort_keys=False), encoding="utf-8")

                    old_root = validator.ROOT
                    validator.ROOT = temp_root
                    try:
                        with self.assertRaises(SystemExit):
                            validator.validate_openapi(manifest)
                    finally:
                        validator.ROOT = old_root

    def test_manifest_missing_required_forbidden_delete_issue_operation_fails(self) -> None:
        validator = load_validator()
        manifest = load_manifest()
        manifest["openapi"]["forbidden_operations"] = [
            operation
            for operation in manifest["openapi"]["forbidden_operations"]
            if not (operation.get("method") == "DELETE" and operation.get("path") == "/issues/{issue_ref}")
        ]

        with self.assertRaises(SystemExit):
            validator.validate_contract_manifest(manifest)

    def test_manifest_missing_required_forbidden_workspace_reset_operation_fails(self) -> None:
        validator = load_validator()
        manifest = load_manifest()
        manifest["openapi"]["forbidden_operations"] = [
            operation
            for operation in manifest["openapi"]["forbidden_operations"]
            if not is_forbidden_operation(operation, "POST", "/workspaces/{issue_ref}/reset")
        ]

        with self.assertRaises(SystemExit):
            validator.validate_contract_manifest(manifest)

    def test_tool_gateway_enum_drift_fails(self) -> None:
        validator = load_validator()
        manifest = load_manifest()
        manifest["tool_gateway"]["registry_tools"] = [*manifest["tool_gateway"]["registry_tools"], "issue.delete"]

        with self.assertRaises(SystemExit):
            validator.validate_tool_gateway_manifest(manifest)

    def test_tool_gateway_non_blank_input_contract_fails_when_drifted(self) -> None:
        validator = load_validator()
        manifest = load_manifest()

        def mutate_json(temp_root: Path, rel: str, mutate: Any) -> None:
            path = temp_root / rel
            data = json.loads(path.read_text(encoding="utf-8"))
            mutate(data)
            path.write_text(json.dumps(data, indent=2) + "\n", encoding="utf-8")

        cases = (
            (
                "embedded issue.comment body may be blank",
                lambda root: mutate_json(
                    root,
                    "schemas/tool_gateway.schema.json",
                    lambda schema: schema["$defs"]["issueCommentInput"]["properties"]["body"].pop("pattern", None),
                ),
            ),
            (
                "standalone issue.comment body may be empty",
                lambda root: mutate_json(
                    root,
                    "schemas/tools/issue_comment.input.schema.json",
                    lambda schema: schema["properties"]["body"].pop("minLength", None),
                ),
            ),
            (
                "embedded followup title may be blank",
                lambda root: mutate_json(
                    root,
                    "schemas/tool_gateway.schema.json",
                    lambda schema: schema["$defs"]["followupCreateInput"]["properties"]["title"].pop(
                        "pattern", None
                    ),
                ),
            ),
            (
                "standalone followup title may be empty",
                lambda root: mutate_json(
                    root,
                    "schemas/tools/followup_create.input.schema.json",
                    lambda schema: schema["properties"]["title"].pop("minLength", None),
                ),
            ),
            (
                "embedded followup label may be blank",
                lambda root: mutate_json(
                    root,
                    "schemas/tool_gateway.schema.json",
                    lambda schema: schema["$defs"]["followupCreateInput"]["properties"]["labels"]["items"].pop(
                        "pattern", None
                    ),
                ),
            ),
            (
                "standalone followup label may be empty",
                lambda root: mutate_json(
                    root,
                    "schemas/tools/followup_create.input.schema.json",
                    lambda schema: schema["properties"]["labels"]["items"].pop("minLength", None),
                ),
            ),
            (
                "embedded handoff summary may be blank",
                lambda root: mutate_json(
                    root,
                    "schemas/tool_gateway.schema.json",
                    lambda schema: schema["$defs"]["handoffSubmitInput"]["properties"]["summary"].pop(
                        "pattern", None
                    ),
                ),
            ),
            (
                "standalone handoff summary may be empty",
                lambda root: mutate_json(
                    root,
                    "schemas/tools/handoff_submit.input.schema.json",
                    lambda schema: schema["properties"]["summary"].pop("minLength", None),
                ),
            ),
        )
        for name, mutate in cases:
            with self.subTest(name=name):
                with tempfile.TemporaryDirectory() as temp_dir:
                    temp_root = Path(temp_dir)
                    copy_contract_root(temp_root)
                    mutate(temp_root)

                    old_root = validator.ROOT
                    validator.ROOT = temp_root
                    try:
                        with self.assertRaises(SystemExit):
                            validator.validate_tool_gateway_manifest(manifest)
                    finally:
                        validator.ROOT = old_root

    def test_review_packet_handoff_requires_followups_and_human_review_target(self) -> None:
        validator = load_validator()

        validator.validate_review_packet_schema_contract()

    def test_review_packet_handoff_contract_fails_when_required_fields_are_removed(self) -> None:
        validator = load_validator()
        schema = validator.load_json("schemas/review_packet.schema.json")

        for required_field in ("followups", "target_state"):
            with self.subTest(required_field=required_field):
                drifted_schema = copy.deepcopy(schema)
                handoff_required = drifted_schema["properties"]["handoff"]["required"]
                handoff_required.remove(required_field)

                original_load_json = validator.load_json
                validator.load_json = lambda rel, drifted_schema=drifted_schema: (
                    drifted_schema if rel == "schemas/review_packet.schema.json" else original_load_json(rel)
                )
                try:
                    with self.assertRaises(SystemExit):
                        validator.validate_review_packet_schema_contract()
                finally:
                    validator.load_json = original_load_json

    def test_review_packet_handoff_contract_fails_when_target_state_drifts(self) -> None:
        validator = load_validator()
        schema = validator.load_json("schemas/review_packet.schema.json")
        drifted_schema = copy.deepcopy(schema)
        drifted_schema["properties"]["handoff"]["properties"]["target_state"]["const"] = "Review"

        original_load_json = validator.load_json
        validator.load_json = lambda rel: (
            drifted_schema if rel == "schemas/review_packet.schema.json" else original_load_json(rel)
        )
        try:
            with self.assertRaises(SystemExit):
                validator.validate_review_packet_schema_contract()
        finally:
            validator.load_json = original_load_json

    def test_diagnostics_schema_contract_fails_when_nested_contract_drifts(self) -> None:
        validator = load_validator()
        schema = validator.load_json("schemas/diagnostics.schema.json")

        cases = (
            ("root additionalProperties", lambda drifted: drifted.__setitem__("additionalProperties", True)),
            (
                "database additionalProperties",
                lambda drifted: drifted["$defs"]["database"].__setitem__("additionalProperties", True),
            ),
            ("workflow.config_path", lambda drifted: drifted["$defs"]["workflow"]["required"].remove("config_path")),
            ("workflow.unmapped_required", lambda drifted: drifted["$defs"]["workflow"]["required"].append("new_required")),
            ("gitRepository.root", lambda drifted: drifted["$defs"]["gitRepository"]["required"].remove("root")),
            ("gitWorktree.base_ref", lambda drifted: drifted["$defs"]["gitWorktree"]["required"].remove("base_ref")),
            ("database.app_db_path", lambda drifted: drifted["$defs"]["database"]["required"].remove("app_db_path")),
            ("daemon.uptime_ms", lambda drifted: drifted["$defs"]["daemon"]["required"].remove("uptime_ms")),
            ("redaction.enabled const", lambda drifted: drifted["$defs"]["redaction"]["properties"]["enabled"].pop("const")),
            ("redaction.rules_version", lambda drifted: drifted["$defs"]["redaction"]["required"].remove("rules_version")),
            ("inconsistentIssue.issue_ref", lambda drifted: drifted["$defs"]["inconsistentIssue"]["required"].remove("issue_ref")),
            ("inconsistentIssue.problem", lambda drifted: drifted["$defs"]["inconsistentIssue"]["required"].remove("problem")),
            ("failureBucket.count", lambda drifted: drifted["$defs"]["failureBucket"]["required"].remove("count")),
            ("check.name", lambda drifted: drifted["$defs"]["check"]["required"].remove("name")),
            ("check.status", lambda drifted: drifted["$defs"]["check"]["required"].remove("status")),
            (
                "database version status enum",
                lambda drifted: drifted["$defs"]["databaseVersionStatus"]["enum"].remove("missing"),
            ),
        )

        for name, mutate in cases:
            with self.subTest(name=name):
                drifted_schema = copy.deepcopy(schema)
                mutate(drifted_schema)

                original_load_json = validator.load_json
                validator.load_json = lambda rel, drifted_schema=drifted_schema: (
                    drifted_schema if rel == "schemas/diagnostics.schema.json" else original_load_json(rel)
                )
                try:
                    with self.assertRaises(SystemExit):
                        validator.validate_diagnostics_schema_contract()
                finally:
                    validator.load_json = original_load_json

    def test_openapi_diagnostics_contract_fails_when_nested_contract_drifts(self) -> None:
        validator = load_validator()
        manifest = load_manifest()

        cases = (
            ("Diagnostics additionalProperties", lambda schemas: schemas["Diagnostics"].__setitem__("additionalProperties", True)),
            (
                "DiagnosticsDatabase additionalProperties",
                lambda schemas: schemas["DiagnosticsDatabase"].__setitem__("additionalProperties", True),
            ),
            ("workflow.config_path", lambda schemas: schemas["DiagnosticsWorkflow"]["required"].remove("config_path")),
            ("workflow.unmapped_required", lambda schemas: schemas["DiagnosticsWorkflow"]["required"].append("new_required")),
            ("gitRepository.root", lambda schemas: schemas["DiagnosticsGitRepository"]["required"].remove("root")),
            ("gitWorktree.base_ref", lambda schemas: schemas["DiagnosticsGitWorktree"]["required"].remove("base_ref")),
            ("database.app_db_path", lambda schemas: schemas["DiagnosticsDatabase"]["required"].remove("app_db_path")),
            ("daemon.uptime_ms", lambda schemas: schemas["DiagnosticsDaemon"]["required"].remove("uptime_ms")),
            ("redaction.enabled const", lambda schemas: schemas["DiagnosticsRedaction"]["properties"]["enabled"].pop("const")),
            ("redaction.rules_version", lambda schemas: schemas["DiagnosticsRedaction"]["required"].remove("rules_version")),
            ("inconsistentIssue.issue_ref", lambda schemas: schemas["DiagnosticsInconsistentIssue"]["required"].remove("issue_ref")),
            ("inconsistentIssue.problem", lambda schemas: schemas["DiagnosticsInconsistentIssue"]["required"].remove("problem")),
            ("failureBucket.count", lambda schemas: schemas["DiagnosticsFailureBucket"]["required"].remove("count")),
            ("diagnosticsExport.artifact_id", lambda schemas: schemas["DiagnosticsExport"]["required"].remove("artifact_id")),
            ("check.name", lambda schemas: schemas["DiagnosticsCheck"]["required"].remove("name")),
            ("check.status", lambda schemas: schemas["DiagnosticsCheck"]["required"].remove("status")),
            (
                "database version status enum",
                lambda schemas: schemas["DiagnosticsDatabaseVersionStatus"]["enum"].remove("missing"),
            ),
        )

        for name, mutate in cases:
            with self.subTest(name=name):
                with tempfile.TemporaryDirectory() as temp_dir:
                    temp_root = Path(temp_dir)
                    copy_contract_root(temp_root)
                    openapi_path = temp_root / "api/openapi.yaml"
                    openapi = yaml.safe_load(openapi_path.read_text(encoding="utf-8"))
                    mutate(openapi["components"]["schemas"])
                    openapi_path.write_text(yaml.safe_dump(openapi, sort_keys=False), encoding="utf-8")

                    old_root = validator.ROOT
                    validator.ROOT = temp_root
                    try:
                        with self.assertRaises(SystemExit):
                            validator.validate_openapi(manifest)
                    finally:
                        validator.ROOT = old_root

    def test_approval_schema_contract_fails_when_drifted(self) -> None:
        validator = load_validator()
        schema = validator.load_json("schemas/approval_request.schema.json")

        cases = (
            ("additionalProperties", lambda drifted: drifted.__setitem__("additionalProperties", True)),
            ("required action_summary", lambda drifted: drifted["required"].remove("action_summary")),
            ("kind enum", lambda drifted: drifted["properties"]["kind"]["enum"].remove("file_change")),
            ("status enum", lambda drifted: drifted["properties"]["status"]["enum"].append("expired")),
            ("opaque request", lambda drifted: drifted["properties"].__setitem__("request", {"type": "object"})),
        )

        for name, mutate in cases:
            with self.subTest(name=name):
                drifted_schema = copy.deepcopy(schema)
                mutate(drifted_schema)

                original_load_json = validator.load_json
                validator.load_json = lambda rel, drifted_schema=drifted_schema: (
                    drifted_schema if rel == "schemas/approval_request.schema.json" else original_load_json(rel)
                )
                try:
                    with self.assertRaises(SystemExit):
                        validator.validate_approval_schema_contract()
                finally:
                    validator.load_json = original_load_json

    def test_sql_approval_contract_fails_when_enum_checks_drift(self) -> None:
        validator = load_validator()

        cases = (
            (
                "kind check",
                lambda text: text.replace(
                    "kind TEXT NOT NULL CHECK (kind IN ('command','file_change','network'))",
                    "kind TEXT NOT NULL",
                ),
            ),
            (
                "status check",
                lambda text: text.replace(
                    "status TEXT NOT NULL CHECK (status IN ('pending','approved_once','approved_for_run','approved_for_session','denied','auto_denied','cancelled','timeout'))",
                    "status TEXT NOT NULL",
                ),
            ),
            (
                "timeout check",
                lambda text: text.replace(
                    "timeout_ms INTEGER CHECK (timeout_ms IS NULL OR timeout_ms > 0)",
                    "timeout_ms INTEGER",
                ),
            ),
        )

        for name, mutate in cases:
            with self.subTest(name=name):
                with tempfile.TemporaryDirectory() as temp_dir:
                    temp_root = Path(temp_dir)
                    shutil.copytree(ROOT / "db", temp_root / "db")
                    schema_path = temp_root / "db/schema/v1_project.sql"
                    schema_text = schema_path.read_text(encoding="utf-8")
                    drifted_text = mutate(schema_text)
                    self.assertNotEqual(schema_text, drifted_text)
                    schema_path.write_text(drifted_text, encoding="utf-8")

                    old_root = validator.ROOT
                    validator.ROOT = temp_root
                    try:
                        with self.assertRaises(SystemExit):
                            validator.validate_sql()
                    finally:
                        validator.ROOT = old_root

    def test_openapi_approval_contract_fails_when_drifted(self) -> None:
        validator = load_validator()
        manifest = load_manifest()

        cases = (
            ("required action_summary", lambda schemas: schemas["ApprovalRequest"]["required"].remove("action_summary")),
            ("additionalProperties", lambda schemas: schemas["ApprovalRequest"].__setitem__("additionalProperties", True)),
            ("kind enum", lambda schemas: schemas["ApprovalRequest"]["properties"]["kind"]["enum"].remove("file_change")),
            ("opaque request", lambda schemas: schemas["ApprovalRequest"]["properties"].__setitem__("request", {"type": "object"})),
            (
                "decision response",
                lambda schemas: schemas["ApprovalDecisionEnvelope"]["allOf"][1]["properties"]["data"].__setitem__(
                    "$ref", "#/components/schemas/ApprovalDecisionResult"
                ),
            ),
            (
                "list envelope items",
                lambda schemas: schemas["ApprovalListEnvelope"]["allOf"][1]["properties"]["data"]["items"].__setitem__(
                    "$ref", "#/components/schemas/ApprovalDecisionResult"
                ),
            ),
        )

        for name, mutate in cases:
            with self.subTest(name=name):
                with tempfile.TemporaryDirectory() as temp_dir:
                    temp_root = Path(temp_dir)
                    copy_contract_root(temp_root)
                    openapi_path = temp_root / "api/openapi.yaml"
                    openapi = yaml.safe_load(openapi_path.read_text(encoding="utf-8"))
                    mutate(openapi["components"]["schemas"])
                    openapi_path.write_text(yaml.safe_dump(openapi, sort_keys=False), encoding="utf-8")

                    old_root = validator.ROOT
                    validator.ROOT = temp_root
                    try:
                        with self.assertRaises(SystemExit):
                            validator.validate_openapi(manifest)
                    finally:
                        validator.ROOT = old_root

    def test_openapi_approval_route_contract_fails_when_drifted(self) -> None:
        validator = load_validator()
        manifest = load_manifest()

        cases = (
            (
                "list route envelope",
                lambda openapi: openapi["paths"]["/approvals"]["get"]["responses"]["200"]["content"]["application/json"][
                    "schema"
                ].__setitem__("$ref", "#/components/schemas/ApprovalEnvelope"),
            ),
            (
                "decision route envelope",
                lambda openapi: openapi["paths"]["/approvals/{approval_id}/decide"]["post"]["responses"]["200"]["content"][
                    "application/json"
                ]["schema"].__setitem__("$ref", "#/components/schemas/ApprovalEnvelope"),
            ),
        )

        for name, mutate in cases:
            with self.subTest(name=name):
                with tempfile.TemporaryDirectory() as temp_dir:
                    temp_root = Path(temp_dir)
                    copy_contract_root(temp_root)
                    openapi_path = temp_root / "api/openapi.yaml"
                    openapi = yaml.safe_load(openapi_path.read_text(encoding="utf-8"))
                    mutate(openapi)
                    openapi_path.write_text(yaml.safe_dump(openapi, sort_keys=False), encoding="utf-8")

                    old_root = validator.ROOT
                    validator.ROOT = temp_root
                    try:
                        with self.assertRaises(SystemExit):
                            validator.validate_openapi(manifest)
                    finally:
                        validator.ROOT = old_root

    def test_openapi_review_packet_summary_failure_metadata_contract_fails_when_drifted(self) -> None:
        validator = load_validator()
        manifest = load_manifest()

        cases = (
            (
                "failure_code required",
                lambda schemas: schemas["ReviewPacketSummary"]["required"].remove("failure_code"),
            ),
            (
                "failure_message required",
                lambda schemas: schemas["ReviewPacketSummary"]["required"].remove("failure_message"),
            ),
            (
                "failure_code canonical ref",
                lambda schemas: schemas["ReviewPacketSummary"]["properties"].__setitem__(
                    "failure_code",
                    {"type": ["string", "null"]},
                ),
            ),
            (
                "failure_message null allowed",
                lambda schemas: schemas["ReviewPacketSummary"]["properties"]["failure_message"].__setitem__("type", ["string"]),
            ),
        )

        for name, mutate in cases:
            with self.subTest(name=name):
                with tempfile.TemporaryDirectory() as temp_dir:
                    temp_root = Path(temp_dir)
                    copy_contract_root(temp_root)
                    openapi_path = temp_root / "api/openapi.yaml"
                    openapi = yaml.safe_load(openapi_path.read_text(encoding="utf-8"))
                    mutate(openapi["components"]["schemas"])
                    openapi_path.write_text(yaml.safe_dump(openapi, sort_keys=False), encoding="utf-8")

                    old_root = validator.ROOT
                    validator.ROOT = temp_root
                    try:
                        with self.assertRaises(SystemExit):
                            validator.validate_openapi(manifest)
                    finally:
                        validator.ROOT = old_root

    def test_openapi_run_attempt_dispatch_reason_contract_fails_when_drifted(self) -> None:
        validator = load_validator()
        manifest = load_manifest()

        with tempfile.TemporaryDirectory() as temp_dir:
            temp_root = Path(temp_dir)
            copy_contract_root(temp_root)
            openapi_path = temp_root / "api/openapi.yaml"
            openapi = yaml.safe_load(openapi_path.read_text(encoding="utf-8"))
            openapi["components"]["schemas"]["RunAttempt"]["properties"]["dispatch_reason"]["enum"].remove("manual_rework")
            openapi_path.write_text(yaml.safe_dump(openapi, sort_keys=False), encoding="utf-8")

            old_root = validator.ROOT
            validator.ROOT = temp_root
            try:
                with self.assertRaises(SystemExit):
                    validator.validate_openapi(manifest)
            finally:
                validator.ROOT = old_root

    def test_openapi_issue_list_query_param_contract_fails_when_drifted(self) -> None:
        validator = load_validator()
        manifest = load_manifest()

        required_params = ("state", "label", "q", "dispatch_paused", "limit", "cursor", "sort")
        for param_name in required_params:
            with self.subTest(param_name=param_name):
                with tempfile.TemporaryDirectory() as temp_dir:
                    temp_root = Path(temp_dir)
                    copy_contract_root(temp_root)
                    openapi_path = temp_root / "api/openapi.yaml"
                    openapi = yaml.safe_load(openapi_path.read_text(encoding="utf-8"))
                    params = openapi["paths"]["/issues"]["get"]["parameters"]
                    openapi["paths"]["/issues"]["get"]["parameters"] = [
                        param for param in params if param.get("name") != param_name
                    ]
                    openapi_path.write_text(yaml.safe_dump(openapi, sort_keys=False), encoding="utf-8")

                    old_root = validator.ROOT
                    validator.ROOT = temp_root
                    try:
                        with self.assertRaises(SystemExit):
                            validator.validate_openapi(manifest)
                    finally:
                        validator.ROOT = old_root

    def test_openapi_issue_list_array_query_param_items_contract_fails_when_drifted(self) -> None:
        validator = load_validator()
        manifest = load_manifest()

        cases = (
            (
                "state items must reference IssueState",
                "state",
                {"type": "string"},
            ),
            (
                "label items must be strings",
                "label",
                {"$ref": "#/components/schemas/IssueState"},
            ),
        )
        for name, param_name, drifted_items in cases:
            with self.subTest(name=name):
                with tempfile.TemporaryDirectory() as temp_dir:
                    temp_root = Path(temp_dir)
                    copy_contract_root(temp_root)
                    openapi_path = temp_root / "api/openapi.yaml"
                    openapi = yaml.safe_load(openapi_path.read_text(encoding="utf-8"))
                    for param in openapi["paths"]["/issues"]["get"]["parameters"]:
                        if param.get("name") == param_name:
                            param["schema"]["items"] = drifted_items
                            break
                    openapi_path.write_text(yaml.safe_dump(openapi, sort_keys=False), encoding="utf-8")

                    old_root = validator.ROOT
                    validator.ROOT = temp_root
                    try:
                        with self.assertRaises(SystemExit):
                            validator.validate_openapi(manifest)
                    finally:
                        validator.ROOT = old_root

    def test_openapi_issue_list_query_param_location_contract_fails_when_drifted(self) -> None:
        validator = load_validator()
        manifest = load_manifest()

        required_params = ("state", "label", "q", "dispatch_paused", "limit", "cursor", "sort")
        for param_name in required_params:
            with self.subTest(param_name=param_name):
                with tempfile.TemporaryDirectory() as temp_dir:
                    temp_root = Path(temp_dir)
                    copy_contract_root(temp_root)
                    openapi_path = temp_root / "api/openapi.yaml"
                    openapi = yaml.safe_load(openapi_path.read_text(encoding="utf-8"))
                    for param in openapi["paths"]["/issues"]["get"]["parameters"]:
                        if param.get("name") == param_name:
                            param["in"] = "header"
                            break
                    openapi_path.write_text(yaml.safe_dump(openapi, sort_keys=False), encoding="utf-8")

                    old_root = validator.ROOT
                    validator.ROOT = temp_root
                    try:
                        with self.assertRaises(SystemExit):
                            validator.validate_openapi(manifest)
                    finally:
                        validator.ROOT = old_root

    def test_openapi_issue_list_query_param_contract_ignores_same_name_non_query_param(self) -> None:
        validator = load_validator()
        manifest = load_manifest()

        with tempfile.TemporaryDirectory() as temp_dir:
            temp_root = Path(temp_dir)
            copy_contract_root(temp_root)
            openapi_path = temp_root / "api/openapi.yaml"
            openapi = yaml.safe_load(openapi_path.read_text(encoding="utf-8"))
            openapi["paths"]["/issues"]["get"]["parameters"].append(
                {"name": "q", "in": "header", "schema": {"type": "string"}}
            )
            openapi_path.write_text(yaml.safe_dump(openapi, sort_keys=False), encoding="utf-8")

            old_root = validator.ROOT
            validator.ROOT = temp_root
            try:
                validator.validate_openapi(manifest)
            finally:
                validator.ROOT = old_root

    def test_openapi_issue_list_duplicate_query_param_contract_fails_when_drifted(self) -> None:
        validator = load_validator()
        manifest = load_manifest()

        with tempfile.TemporaryDirectory() as temp_dir:
            temp_root = Path(temp_dir)
            copy_contract_root(temp_root)
            openapi_path = temp_root / "api/openapi.yaml"
            openapi = yaml.safe_load(openapi_path.read_text(encoding="utf-8"))
            for param in openapi["paths"]["/issues"]["get"]["parameters"]:
                if param.get("name") == "q":
                    param["schema"] = {"type": "integer"}
                    break
            openapi["paths"]["/issues"]["get"]["parameters"].append(
                {"name": "q", "in": "query", "schema": {"type": "string"}}
            )
            openapi_path.write_text(yaml.safe_dump(openapi, sort_keys=False), encoding="utf-8")

            old_root = validator.ROOT
            validator.ROOT = temp_root
            try:
                with self.assertRaises(SystemExit):
                    validator.validate_openapi(manifest)
            finally:
                validator.ROOT = old_root

    def test_openapi_issue_list_invalid_query_error_contract_fails_when_drifted(self) -> None:
        validator = load_validator()
        manifest = load_manifest()

        with tempfile.TemporaryDirectory() as temp_dir:
            temp_root = Path(temp_dir)
            copy_contract_root(temp_root)
            openapi_path = temp_root / "api/openapi.yaml"
            openapi = yaml.safe_load(openapi_path.read_text(encoding="utf-8"))
            openapi["paths"]["/issues"]["get"]["responses"].pop("400", None)
            openapi_path.write_text(yaml.safe_dump(openapi, sort_keys=False), encoding="utf-8")

            old_root = validator.ROOT
            validator.ROOT = temp_root
            try:
                with self.assertRaises(SystemExit):
                    validator.validate_openapi(manifest)
            finally:
                validator.ROOT = old_root

    def test_openapi_issue_mutation_bad_request_contract_fails_when_drifted(self) -> None:
        validator = load_validator()
        manifest = load_manifest()

        operations = (
            ("/issues", "post"),
            ("/issues/{issue_ref}", "patch"),
            ("/issues/{issue_ref}/transition", "post"),
            ("/issues/{issue_ref}/comments", "post"),
            ("/issues/{issue_ref}/blockers", "post"),
            ("/issues/{issue_ref}/dispatch-pause", "post"),
            ("/issues/{issue_ref}/dispatch-resume", "post"),
        )
        for route, method in operations:
            with self.subTest(route=route, method=method):
                with tempfile.TemporaryDirectory() as temp_dir:
                    temp_root = Path(temp_dir)
                    copy_contract_root(temp_root)
                    openapi_path = temp_root / "api/openapi.yaml"
                    openapi = yaml.safe_load(openapi_path.read_text(encoding="utf-8"))
                    openapi["paths"][route][method]["responses"].pop("400", None)
                    openapi_path.write_text(yaml.safe_dump(openapi, sort_keys=False), encoding="utf-8")

                    old_root = validator.ROOT
                    validator.ROOT = temp_root
                    try:
                        with self.assertRaises(SystemExit):
                            validator.validate_openapi(manifest)
                    finally:
                        validator.ROOT = old_root

    def test_openapi_duplicate_delete_failure_response_contract_fails_when_drifted(self) -> None:
        validator = load_validator()
        manifest = load_manifest()
        route = "/issues/{issue_ref}/duplicates/{canonical_issue_ref}"

        cases = (
            ("missing delete operation", lambda paths: paths[route].pop("delete", None)),
            ("missing 400 response", lambda paths: paths[route]["delete"]["responses"].pop("400", None)),
            ("missing 404 response", lambda paths: paths[route]["delete"]["responses"].pop("404", None)),
            (
                "400 does not reference Error",
                lambda paths: paths[route]["delete"]["responses"].__setitem__(
                    "400",
                    {"description": "Bad request"},
                ),
            ),
            (
                "404 does not reference Error",
                lambda paths: paths[route]["delete"]["responses"].__setitem__(
                    "404",
                    {"description": "Not found"},
                ),
            ),
            (
                "400 does not document invalid_request",
                lambda paths: paths[route]["delete"]["responses"]["400"].__setitem__(
                    "description",
                    "Bad request",
                ),
            ),
            (
                "404 does not document not_found",
                lambda paths: paths[route]["delete"]["responses"]["404"].__setitem__(
                    "description",
                    "Missing relation",
                ),
            ),
            (
                "400 does not document no mutation",
                lambda paths: paths[route]["delete"]["responses"]["400"].__setitem__(
                    "description",
                    "invalid_request for malformed duplicate refs.",
                ),
            ),
            (
                "404 does not document no mutation",
                lambda paths: paths[route]["delete"]["responses"]["404"].__setitem__(
                    "description",
                    "not_found when duplicate refs are unresolved.",
                ),
            ),
        )
        for name, mutate in cases:
            with self.subTest(name=name):
                with tempfile.TemporaryDirectory() as temp_dir:
                    temp_root = Path(temp_dir)
                    copy_contract_root(temp_root)
                    openapi_path = temp_root / "api/openapi.yaml"
                    openapi = yaml.safe_load(openapi_path.read_text(encoding="utf-8"))
                    mutate(openapi["paths"])
                    openapi_path.write_text(yaml.safe_dump(openapi, sort_keys=False), encoding="utf-8")

                    old_root = validator.ROOT
                    validator.ROOT = temp_root
                    try:
                        with self.assertRaises(SystemExit):
                            validator.validate_openapi(manifest)
                    finally:
                        validator.ROOT = old_root

    def test_openapi_issue_text_request_contract_fails_when_drifted(self) -> None:
        validator = load_validator()
        manifest = load_manifest()

        def comment_body_schema(openapi: dict) -> dict:
            return openapi["paths"]["/issues/{issue_ref}/comments"]["post"]["requestBody"]["content"][
                "application/json"
            ]["schema"]["properties"]["body"]

        cases = (
            (
                "create title may be blank",
                lambda openapi: openapi["components"]["schemas"]["IssueCreateRequest"]["properties"]["title"].pop(
                    "pattern", None
                ),
            ),
            (
                "create title may be empty",
                lambda openapi: openapi["components"]["schemas"]["IssueCreateRequest"]["properties"]["title"].pop(
                    "minLength", None
                ),
            ),
            (
                "patch title may be blank",
                lambda openapi: openapi["components"]["schemas"]["IssuePatchRequest"]["properties"]["title"].pop(
                    "pattern", None
                ),
            ),
            (
                "patch title may be empty",
                lambda openapi: openapi["components"]["schemas"]["IssuePatchRequest"]["properties"]["title"].pop(
                    "minLength", None
                ),
            ),
            ("comment body may be blank", lambda openapi: comment_body_schema(openapi).pop("pattern", None)),
            ("comment body may be empty", lambda openapi: comment_body_schema(openapi).pop("minLength", None)),
            (
                "create label may be empty",
                lambda openapi: openapi["components"]["schemas"]["IssueCreateRequest"]["properties"]["labels"][
                    "items"
                ].pop("minLength", None),
            ),
            (
                "create label may be blank",
                lambda openapi: openapi["components"]["schemas"]["IssueCreateRequest"]["properties"]["labels"][
                    "items"
                ].pop("pattern", None),
            ),
            (
                "patch label may be blank",
                lambda openapi: openapi["components"]["schemas"]["IssuePatchRequest"]["properties"]["labels"][
                    "items"
                ].pop("pattern", None),
            ),
            (
                "patch label may be empty",
                lambda openapi: openapi["components"]["schemas"]["IssuePatchRequest"]["properties"]["labels"][
                    "items"
                ].pop("minLength", None),
            ),
        )
        for name, mutate in cases:
            with self.subTest(name=name):
                with tempfile.TemporaryDirectory() as temp_dir:
                    temp_root = Path(temp_dir)
                    copy_contract_root(temp_root)
                    openapi_path = temp_root / "api/openapi.yaml"
                    openapi = yaml.safe_load(openapi_path.read_text(encoding="utf-8"))
                    mutate(openapi)
                    openapi_path.write_text(yaml.safe_dump(openapi, sort_keys=False), encoding="utf-8")

                    old_root = validator.ROOT
                    validator.ROOT = temp_root
                    try:
                        with self.assertRaises(SystemExit):
                            validator.validate_openapi(manifest)
                    finally:
                        validator.ROOT = old_root

    def test_openapi_issue_list_envelope_contract_fails_when_drifted(self) -> None:
        validator = load_validator()
        manifest = load_manifest()

        with tempfile.TemporaryDirectory() as temp_dir:
            temp_root = Path(temp_dir)
            copy_contract_root(temp_root)
            openapi_path = temp_root / "api/openapi.yaml"
            openapi = yaml.safe_load(openapi_path.read_text(encoding="utf-8"))
            openapi["components"]["schemas"]["IssueListEnvelope"]["allOf"][1]["properties"]["data"] = {
                "type": "array",
                "items": {"$ref": "#/components/schemas/Issue"},
            }
            openapi_path.write_text(yaml.safe_dump(openapi, sort_keys=False), encoding="utf-8")

            old_root = validator.ROOT
            validator.ROOT = temp_root
            try:
                with self.assertRaises(SystemExit):
                    validator.validate_openapi(manifest)
            finally:
                validator.ROOT = old_root

    def test_openapi_issue_transition_duplicate_of_contract_fails_when_drifted(self) -> None:
        validator = load_validator()
        manifest = load_manifest()

        cases = (
            ("missing duplicate_of", lambda props: props.pop("duplicate_of", None)),
            ("duplicate_of may be blank", lambda props: props["duplicate_of"].pop("pattern", None)),
            ("duplicate_of may be empty", lambda props: props["duplicate_of"].pop("minLength", None)),
            ("legacy canonical_issue_ref", lambda props: props.__setitem__("canonical_issue_ref", {"type": "string"})),
        )
        for name, mutate in cases:
            with self.subTest(name=name):
                with tempfile.TemporaryDirectory() as temp_dir:
                    temp_root = Path(temp_dir)
                    copy_contract_root(temp_root)
                    openapi_path = temp_root / "api/openapi.yaml"
                    openapi = yaml.safe_load(openapi_path.read_text(encoding="utf-8"))
                    props = openapi["components"]["schemas"]["IssueTransitionRequest"]["properties"]
                    mutate(props)
                    openapi_path.write_text(yaml.safe_dump(openapi, sort_keys=False), encoding="utf-8")

                    old_root = validator.ROOT
                    validator.ROOT = temp_root
                    try:
                        with self.assertRaises(SystemExit):
                            validator.validate_openapi(manifest)
                    finally:
                        validator.ROOT = old_root

    def test_openapi_issue_transition_guard_contract_fails_when_drifted(self) -> None:
        validator = load_validator()
        manifest = load_manifest()

        cases = (
            ("reason may be blank", lambda schema: schema["properties"]["reason"].pop("pattern", None)),
            ("reason may be empty", lambda schema: schema["properties"]["reason"].pop("minLength", None)),
            ("missing reason guard", lambda schema: schema["allOf"].pop(0)),
            ("missing duplicate_of guard", lambda schema: schema["allOf"].pop(1)),
            ("missing allOf guards", lambda schema: schema.pop("allOf", None)),
        )
        for name, mutate in cases:
            with self.subTest(name=name):
                with tempfile.TemporaryDirectory() as temp_dir:
                    temp_root = Path(temp_dir)
                    copy_contract_root(temp_root)
                    openapi_path = temp_root / "api/openapi.yaml"
                    openapi = yaml.safe_load(openapi_path.read_text(encoding="utf-8"))
                    schema = openapi["components"]["schemas"]["IssueTransitionRequest"]
                    mutate(schema)
                    openapi_path.write_text(yaml.safe_dump(openapi, sort_keys=False), encoding="utf-8")

                    old_root = validator.ROOT
                    validator.ROOT = temp_root
                    try:
                        with self.assertRaises(SystemExit):
                            validator.validate_openapi(manifest)
                    finally:
                        validator.ROOT = old_root

    def test_workflow_config_schema_top_level_unknown_keys_must_be_allowed(self) -> None:
        validator = load_validator()
        schema = validator.load_json("schemas/workflow_config.schema.json")
        drifted_schema = copy.deepcopy(schema)
        drifted_schema["additionalProperties"] = False

        original_load_json = validator.load_json
        validator.load_json = lambda rel: drifted_schema
        try:
            with self.assertRaises(SystemExit):
                validator.validate_workflow_config_schema_contract()
        finally:
            validator.load_json = original_load_json

    def test_workflow_config_schema_contract_fails_when_branch_prefix_not_required(self) -> None:
        validator = load_validator()
        schema = validator.load_json("schemas/workflow_config.schema.json")
        drifted_schema = copy.deepcopy(schema)
        drifted_schema["properties"]["git"]["required"].remove("branch_prefix")

        original_load_json = validator.load_json
        validator.load_json = lambda rel: drifted_schema
        try:
            with self.assertRaises(SystemExit):
                validator.validate_workflow_config_schema_contract()
        finally:
            validator.load_json = original_load_json

    def test_agent_work_order_forbidden_cli_commands_from_manifest_fail(self) -> None:
        validator = load_validator()
        manifest = load_manifest()

        for forbidden_command in ("symphony pr", "symphony project settings"):
            with self.subTest(forbidden_command=forbidden_command):
                with tempfile.TemporaryDirectory() as temp_dir:
                    temp_root = Path(temp_dir)
                    work_orders = temp_root / "docs/agent_work_orders"
                    work_orders.mkdir(parents=True)
                    (work_orders / "M0.md").write_text(
                        f"# Work order\n\nDo not add docs. Example: `{forbidden_command}`.\n",
                        encoding="utf-8",
                    )

                    old_root = validator.ROOT
                    validator.ROOT = temp_root
                    try:
                        with self.assertRaises(SystemExit):
                            validator.validate_agent_work_order_scope(manifest)
                    finally:
                        validator.ROOT = old_root

    def test_manifest_missing_required_duplicate_cli_command_fails(self) -> None:
        validator = load_validator()
        manifest = load_manifest()
        manifest["cli"]["required_commands"] = [
            command
            for command in manifest["cli"]["required_commands"]
            if command != "symphony issue duplicate remove"
        ]

        with self.assertRaises(SystemExit):
            validator.validate_contract_manifest(manifest)

    def test_manifest_required_route_drift_fails(self) -> None:
        validator = load_validator()
        manifest = load_manifest()
        manifest["openapi"]["required_routes"] = [
            route for route in manifest["openapi"]["required_routes"] if route != "/issues/{issue_ref}/dispatch-pause"
        ]

        with tempfile.TemporaryDirectory() as temp_dir:
            temp_root = Path(temp_dir)
            copy_contract_root(temp_root)

            old_root = validator.ROOT
            validator.ROOT = temp_root
            try:
                with self.assertRaises(SystemExit):
                    validator.validate_openapi(manifest)
            finally:
                validator.ROOT = old_root

    def test_dispatch_pause_resume_reason_requires_non_blank_pattern(self) -> None:
        validator = load_validator()
        manifest = load_manifest()

        for route in ("/issues/{issue_ref}/dispatch-pause", "/issues/{issue_ref}/dispatch-resume"):
            with self.subTest(route=route):
                with tempfile.TemporaryDirectory() as temp_dir:
                    temp_root = Path(temp_dir)
                    copy_contract_root(temp_root)
                    openapi_path = temp_root / "api/openapi.yaml"
                    openapi = yaml.safe_load(openapi_path.read_text(encoding="utf-8"))
                    reason_schema = (
                        openapi["paths"][route]["post"]["requestBody"]["content"]["application/json"]["schema"][
                            "properties"
                        ]["reason"]
                    )
                    reason_schema.pop("pattern", None)
                    openapi_path.write_text(yaml.safe_dump(openapi, sort_keys=False), encoding="utf-8")

                    old_root = validator.ROOT
                    validator.ROOT = temp_root
                    try:
                        with self.assertRaises(SystemExit):
                            validator.validate_openapi(manifest)
                    finally:
                        validator.ROOT = old_root

    def test_dispatch_pause_resume_rejection_contract_fails_when_drifted(self) -> None:
        validator = load_validator()
        manifest = load_manifest()

        cases = (
            (
                "mentions archived",
                "invalid_state_transition when issue is terminal/archived; "
                "issue_already_running when an active run exists.",
            ),
            (
                "missing invalid state transition",
                "issue_already_running when an active run exists.",
            ),
            (
                "missing active run guard",
                "invalid_state_transition when issue state is Done, Cancelled, or Duplicate.",
            ),
        )
        for route in ("/issues/{issue_ref}/dispatch-pause", "/issues/{issue_ref}/dispatch-resume"):
            for name, description in cases:
                with self.subTest(route=route, name=name):
                    with tempfile.TemporaryDirectory() as temp_dir:
                        temp_root = Path(temp_dir)
                        copy_contract_root(temp_root)
                        openapi_path = temp_root / "api/openapi.yaml"
                        openapi = yaml.safe_load(openapi_path.read_text(encoding="utf-8"))
                        openapi["paths"][route]["post"]["responses"]["409"]["description"] = description
                        openapi_path.write_text(yaml.safe_dump(openapi, sort_keys=False), encoding="utf-8")

                        old_root = validator.ROOT
                        validator.ROOT = temp_root
                        try:
                            with self.assertRaises(SystemExit):
                                validator.validate_openapi(manifest)
                        finally:
                            validator.ROOT = old_root


if __name__ == "__main__":
    unittest.main()
