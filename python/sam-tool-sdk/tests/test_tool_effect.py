# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""A tool's declared effect reaches the ``--schema`` output as ``effect``.

Mirrors Go's ``WithEffect``: the agent treats a tool that declares nothing as
side-effecting, so the key is omitted when nothing was declared.
"""

from __future__ import annotations

import pytest

from sam_tool_sdk import EFFECT_READ_ONLY, EFFECT_SIDE_EFFECTING, DynamicTool, tool_effect
from sam_tool_sdk.cli import _build_schema_from_func, _dynamic_tool_schema


@tool_effect(EFFECT_READ_ONLY)
def _lookup(order_id: str):
    """Looks up an order."""
    return None


def _undeclared(order_id: str):
    """Does something with an order."""
    return None


def test_function_tool_emits_declared_effect() -> None:
    assert _build_schema_from_func(_lookup)["effect"] == "read_only"


def test_function_tool_without_declaration_omits_effect() -> None:
    assert "effect" not in _build_schema_from_func(_undeclared)


def test_tool_effect_rejects_unknown_value() -> None:
    with pytest.raises(ValueError, match="tool_effect must be one of"):
        tool_effect("readonly")


class _Updater(DynamicTool):
    @property
    def tool_name(self) -> str:
        return "update_order"

    @property
    def tool_description(self) -> str:
        return "Updates an order."

    @property
    def parameters_schema(self):
        return {"type": "object", "properties": {}}

    @property
    def effect(self):
        return EFFECT_SIDE_EFFECTING

    async def _run_async_impl(self, args, tool_context=None, credential=None):
        return None


def test_dynamic_tool_emits_declared_effect() -> None:
    assert _dynamic_tool_schema(_Updater())["effect"] == "side_effecting"
