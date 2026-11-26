# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Regression test: ``DynamicTool._run_async_impl`` must receive the live
``SandboxToolContextFacade`` via the ``tool_context`` parameter.

Earlier revisions of the PR passed ``tool_context=None`` to
``_run_async_impl``, breaking any class-based tool that reads the context
through the documented argument (the standard pattern). This test
captures the context-passing contract so the regression doesn't return.
"""

from __future__ import annotations

import os
import tempfile
from pathlib import Path
from typing import Any, Dict, List, Optional

from sam_tool_sdk.cli import _run_class_tool, _run_provider_tool
from sam_tool_sdk.dynamic_tool import DynamicTool, DynamicToolProvider


_CAPTURED: Dict[str, Any] = {}


class _CapturingTool(DynamicTool):
    @property
    def tool_name(self) -> str:
        return "capturing_tool"

    @property
    def tool_description(self) -> str:
        return "Captures the tool_context for assertion."

    @property
    def parameters_schema(self) -> dict:
        return {"type": "object", "properties": {}, "required": []}

    async def _run_async_impl(
        self, args: dict, tool_context=None, credential: Optional[str] = None
    ) -> dict:
        _CAPTURED["tool_context"] = tool_context
        return {"status": "success", "message": "ok"}


def _runner_args(tmp_path: Path) -> Dict[str, Any]:
    return {
        "args": {},
        "tool_config": {"hello": "world"},
        "artifact_metadata": {},
        "artifact_paths": {},
        "status_pipe": str(tmp_path / "status_pipe"),
        "output_dir": str(tmp_path / "out"),
        "user_id": "u",
        "session_id": "s",
        "app_name": "app",
        "tool_oauth_token": "",
        "task_id": "task-42",
        "ipc_socket": "",
        "volume_mounts": [],
        "result_file": str(tmp_path / "result.json"),
    }


def test_class_tool_receives_live_context() -> None:
    _CAPTURED.clear()
    with tempfile.TemporaryDirectory() as tmp:
        tmp_path = Path(tmp)
        os.makedirs(tmp_path / "out")
        runner = _runner_args(tmp_path)
        out = _run_class_tool(_CapturingTool, runner)
    assert "result" in out, out
    ctx = _CAPTURED.get("tool_context")
    assert ctx is not None, "tool_context must be the facade, got None"
    assert ctx.task_id == "task-42"
    assert ctx.get_config("hello") == "world"


class _Provider(DynamicToolProvider):
    def create_tools(self, tool_config=None) -> List[DynamicTool]:
        return [_CapturingTool(tool_config=tool_config)]


def test_provider_tool_receives_live_context() -> None:
    _CAPTURED.clear()
    with tempfile.TemporaryDirectory() as tmp:
        tmp_path = Path(tmp)
        os.makedirs(tmp_path / "out")
        runner = _runner_args(tmp_path)
        runner["tool_name"] = "capturing_tool"
        out = _run_provider_tool(_Provider, "capturing_tool", runner)
    assert "result" in out, out
    ctx = _CAPTURED.get("tool_context")
    assert ctx is not None, "tool_context must be the facade, got None"
    assert ctx.task_id == "task-42"
