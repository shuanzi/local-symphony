#!/usr/bin/env python3
"""Validate Local Symphony v1 documentation contract artifacts.

This script is intentionally lightweight so implementation agents can run it
before code exists. It validates JSON syntax, SQLite DDL executability, and
basic OpenAPI shape. If PyYAML is installed, it also parses OpenAPI YAML.
"""
from __future__ import annotations

import json
import sqlite3
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def fail(msg: str) -> None:
    print(f"FAIL: {msg}", file=sys.stderr)
    sys.exit(1)


def validate_json() -> None:
    for path in sorted((ROOT / "schemas").glob("*.json")) + sorted((ROOT / "examples").glob("*.json")):
        try:
            json.loads(path.read_text(encoding="utf-8"))
        except Exception as exc:  # noqa: BLE001
            fail(f"invalid JSON {path.relative_to(ROOT)}: {exc}")
        print(f"ok json {path.relative_to(ROOT)}")


def validate_sql() -> None:
    for rel in ["db/schema/v1_app.sql", "db/schema/v1_project.sql"]:
        path = ROOT / rel
        try:
            con = sqlite3.connect(":memory:")
            con.executescript(path.read_text(encoding="utf-8"))
            con.close()
        except Exception as exc:  # noqa: BLE001
            fail(f"invalid SQLite DDL {rel}: {exc}")
        print(f"ok sql {rel}")


def validate_openapi() -> None:
    path = ROOT / "api/openapi.yaml"
    text = path.read_text(encoding="utf-8")
    if "openapi: 3.1.0" not in text:
        fail("api/openapi.yaml must declare openapi: 3.1.0")
    if "paths:" not in text or "components:" not in text:
        fail("api/openapi.yaml must include paths and components")
    try:
        import yaml  # type: ignore
    except Exception:
        print("warn openapi yaml parse skipped: PyYAML not installed")
        return
    try:
        data = yaml.safe_load(text)
    except Exception as exc:  # noqa: BLE001
        fail(f"invalid OpenAPI YAML: {exc}")
    if data.get("openapi") != "3.1.0":
        fail("unexpected OpenAPI version")
    for route in ["/health", "/issues", "/runs", "/auth/exchange", "/diagnostics"]:
        if route not in data.get("paths", {}):
            fail(f"missing OpenAPI route {route}")
    print("ok openapi api/openapi.yaml")


def main() -> None:
    validate_json()
    validate_sql()
    validate_openapi()
    print("contract validation passed")


if __name__ == "__main__":
    main()
