# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Tests for the ``--schema --config <file>`` dynamic schema CLI mode.

Mirrors Go's ``run.go`` parser + ``outputSchema`` flow in
``pkg/samtoolsdk/`` — a per-deployment config dict can override the
``name`` / ``description`` / ``instructions`` / ``parameters`` /
``config_schema`` of a tool at schema-emission time.
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any, Dict

import pytest

from sam_tool_sdk.cli import (
    _apply_schema_override,
    _build_schema_from_func,
    _parse_schema_args,
)
from sam_tool_sdk.tool_options import with_dynamic_schema

def test_parse_schema_only() -> None:
    schema_mode, config, argsfile = _parse_schema_args(["--schema"])
    assert schema_mode is True
    assert config is None
    assert argsfile is None

def test_parse_schema_with_config(tmp_path: Path) -> None:
    cfg = tmp_path / "cfg.json"
    cfg.write_text(json.dumps({"backend": "dynamo"}))

    schema_mode, config, argsfile = _parse_schema_args(
        ["--schema", "--config", str(cfg)]
    )
    assert schema_mode is True
    assert config == {"backend": "dynamo"}
    assert argsfile is None

def test_parse_argsfile_path() -> None:
    schema_mode, config, argsfile = _parse_schema_args(["/tmp/runner_args.json"])
    assert schema_mode is False
    assert config is None
    assert argsfile == "/tmp/runner_args.json"

def test_apply_schema_override_merges_description_only() -> None:
    base = {"description": "old", "parameters": {"type": "object"}}
    name_override, merged = _apply_schema_override(base, {"description": "new"})
    assert name_override is None
    assert merged["description"] == "new"
    assert merged["parameters"] == {"type": "object"}

def test_apply_schema_override_renames_tool() -> None:
    name_override, merged = _apply_schema_override(
        {"description": "x"}, {"name": "query_dynamo"}
    )
    assert name_override == "query_dynamo"
    assert merged == {"description": "x"}

def test_apply_schema_override_ignores_unknown_keys() -> None:
    name_override, merged = _apply_schema_override(
        {"description": "x"}, {"frobnicate": True, "description": "y"}
    )
    assert name_override is None
    assert "frobnicate" not in merged
    assert merged["description"] == "y"

def test_apply_schema_override_skips_none_values() -> None:
    name_override, merged = _apply_schema_override(
        {"description": "old"}, {"description": None}
    )
    assert name_override is None
    assert merged["description"] == "old"

def test_with_dynamic_schema_attaches_callback() -> None:
    def cb(config: Dict[str, Any]) -> Dict[str, Any]:
        return {"description": f"backend={config.get('backend')}"}

    @with_dynamic_schema(cb)
    async def my_tool(x: str) -> Any:
        return None

    fn = getattr(my_tool, "_dynamic_schema", None)
    assert fn is cb

def test_with_dynamic_schema_rejects_non_callable() -> None:
    with pytest.raises(TypeError):
        with_dynamic_schema("not a function")  # type: ignore[arg-type]

def test_build_schema_from_func_unaffected_by_decorator() -> None:
    @with_dynamic_schema(lambda c: {"description": "overridden"})
    async def my_tool(name: str) -> Any:
        """Greet someone.

        Args:
            name: The name to greet.
        """
        return None

    schema = _build_schema_from_func(my_tool)
    assert schema["description"].startswith("Greet someone")
