# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Tests for argument coercion across the three dispatch paths.

LLM providers (notably Anthropic and Vertex) routinely emit ``integer`` /
``number`` / ``boolean`` arguments as JSON strings even when the tool's
schema declares the scalar type. The Go SDK never sees this because it
unmarshals into a typed struct; the Python dispatch paths used to forward
the raw kwargs, so a tool doing ``top_keywords > 0`` blew up on ``"5" > 0``.

These tests lock the coercion in: the unit cases pin ``_coerce_scalar`` /
``_coerce_args_to_schema`` behaviour, and the end-to-end cases drive all
three CLI dispatch paths (``tool_cli``, ``dynamic_tool_cli``,
``provider_cli``) to confirm a tool receives the typed value the schema
promised.
"""

from __future__ import annotations

import os
import tempfile
from pathlib import Path
from typing import Any, Dict, List, Optional

from sam_tool_sdk.cli import _run_class_tool, _run_function_tool, _run_provider_tool
from sam_tool_sdk.context_facade import SandboxToolContextFacade
from sam_tool_sdk.dynamic_tool import (
    DynamicTool,
    DynamicToolProvider,
    _coerce_args_to_schema,
    _coerce_scalar,
)

def test_coerce_scalar_integer() -> None:
    assert _coerce_scalar("5", "integer") == 5
    assert isinstance(_coerce_scalar("5", "integer"), int)

def test_coerce_scalar_number() -> None:
    assert _coerce_scalar("2.5", "number") == 2.5
    assert isinstance(_coerce_scalar("2.5", "number"), float)
    assert isinstance(_coerce_scalar("3", "number"), float)

def test_coerce_scalar_boolean() -> None:
    for truthy in ("true", "True", "TRUE", "1"):
        assert _coerce_scalar(truthy, "boolean") is True
    for falsy in ("false", "False", "0"):
        assert _coerce_scalar(falsy, "boolean") is False

def test_coerce_scalar_unrecognized_boolean_stays_string_not_python_truthy() -> None:
    assert _coerce_scalar("maybe", "boolean") == "maybe"
    assert _coerce_scalar("abc", "integer") == "abc"
    assert _coerce_scalar("1.2.3", "number") == "1.2.3"

def test_coerce_scalar_non_string_unchanged() -> None:
    assert _coerce_scalar(5, "integer") == 5
    assert _coerce_scalar(True, "boolean") is True
    assert _coerce_scalar(2.5, "number") == 2.5
    assert _coerce_scalar(None, "integer") is None

def test_coerce_scalar_unknown_type_unchanged() -> None:
    assert _coerce_scalar("hello", "string") == "hello"
    assert _coerce_scalar("[]", "array") == "[]"

_SCHEMA = {
    "type": "object",
    "properties": {
        "n": {"type": "integer"},
        "r": {"type": "number"},
        "b": {"type": "boolean"},
        "s": {"type": "string"},
        "items": {"type": "array", "items": {"type": "string"}},
        "obj": {"type": "object"},
    },
}

def test_coerce_args_to_schema_coerces_declared_scalars() -> None:
    out = _coerce_args_to_schema(
        {"n": "5", "r": "2.5", "b": "true", "s": "7"}, _SCHEMA
    )
    assert out == {"n": 5, "r": 2.5, "b": True, "s": "7"}
    assert isinstance(out["n"], int) and isinstance(out["r"], float)

def test_coerce_args_to_schema_leaves_complex_and_unknown() -> None:
    args = {"items": ["a"], "obj": {"k": "v"}, "extra": "9"}
    assert _coerce_args_to_schema(args, _SCHEMA) == args

def test_coerce_args_to_schema_guards() -> None:
    assert _coerce_args_to_schema({"n": "5"}, None) == {"n": "5"}
    assert _coerce_args_to_schema({"n": "5"}, {}) == {"n": "5"}
    assert _coerce_args_to_schema({"n": "5"}, {"properties": "nope"}) == {"n": "5"}

def test_coerce_args_to_schema_does_not_mutate_input() -> None:
    args = {"n": "5"}
    _coerce_args_to_schema(args, _SCHEMA)
    assert args == {"n": "5"}

def _runner_args(tmp_path: Path, args: Dict[str, Any]) -> Dict[str, Any]:
    return {
        "args": args,
        "tool_config": {},
        "artifact_metadata": {},
        "artifact_paths": {},
        "status_pipe": str(tmp_path / "status_pipe"),
        "output_dir": str(tmp_path / "out"),
        "user_id": "u",
        "session_id": "s",
        "app_name": "app",
        "tool_oauth_token": "",
        "task_id": "t",
        "ipc_socket": "",
        "volume_mounts": [],
        "result_file": str(tmp_path / "result.json"),
    }

def _run(fn, *fn_args) -> Dict[str, Any]:
    with tempfile.TemporaryDirectory() as tmp:
        tmp_path = Path(tmp)
        os.makedirs(tmp_path / "out")
        runner = _runner_args(tmp_path, fn_args[-1])
        out = fn(*fn_args[:-1], runner)
    assert "result" in out, out
    return out["result"]

_STRING_ARGS = {"n": "5", "r": "2.5", "b": "true", "s": "7"}

def _echo_types(n, r, b, s) -> Dict[str, Any]:
    return {
        "status": "success",
        "message": "ok",
        "n": n,
        "b": b,
        "s": s,
        "types": {
            "n": type(n).__name__,
            "r": type(r).__name__,
            "b": type(b).__name__,
            "s": type(s).__name__,
        },
    }

_EXPECTED_TYPES = {"n": "int", "r": "float", "b": "bool", "s": "str"}

class _TypedTool(DynamicTool):
    @property
    def tool_name(self) -> str:
        return "typed_tool"

    @property
    def tool_description(self) -> str:
        return "Echoes the runtime type of each coerced arg."

    @property
    def parameters_schema(self) -> dict:
        return {
            "type": "object",
            "properties": {
                "n": {"type": "integer"},
                "r": {"type": "number"},
                "b": {"type": "boolean"},
                "s": {"type": "string"},
            },
            "required": ["n"],
        }

    async def _run_async_impl(
        self, args: dict, tool_context=None, credential: Optional[str] = None
    ) -> dict:
        return _echo_types(args["n"], args["r"], args["b"], args["s"])

def test_class_path_coerces_string_args() -> None:
    data = _run(_run_class_tool, _TypedTool, dict(_STRING_ARGS))
    assert data["types"] == _EXPECTED_TYPES
    assert data["n"] == 5 and data["b"] is True and data["s"] == "7"

class _TypedProvider(DynamicToolProvider):
    def create_tools(self, tool_config=None) -> List[DynamicTool]:
        return []

@_TypedProvider.register_tool
async def echo(self, n: int, r: float, b: bool, s: str) -> dict:
    """Echo the runtime type of each coerced arg."""
    return _echo_types(n, r, b, s)

def test_provider_path_coerces_string_args() -> None:
    data = _run(_run_provider_tool, _TypedProvider, "echo", dict(_STRING_ARGS))
    assert data["types"] == _EXPECTED_TYPES
    assert data["n"] == 5 and data["b"] is True and data["s"] == "7"

async def _typed_fn(
    n: int, r: float, b: bool, s: str, ctx: SandboxToolContextFacade
) -> dict:
    """Echo the runtime type of each coerced arg."""
    return _echo_types(n, r, b, s)

def test_function_path_coerces_string_args() -> None:
    data = _run(_run_function_tool, _typed_fn, dict(_STRING_ARGS))
    assert data["types"] == _EXPECTED_TYPES
    assert data["n"] == 5 and data["b"] is True and data["s"] == "7"

def test_string_param_stays_string() -> None:
    data = _run(_run_class_tool, _TypedTool, {"n": "1", "r": "1", "b": "1", "s": "12345"})
    assert data["s"] == "12345" and data["types"]["s"] == "str"
