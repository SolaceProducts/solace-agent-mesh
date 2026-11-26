# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Unit tests for SandboxToolContextFacade identity properties."""

from __future__ import annotations

import json
import os
import tempfile

from sam_tool_sdk.context_facade import SandboxToolContextFacade

def _make_facade(**overrides) -> SandboxToolContextFacade:
    with tempfile.TemporaryDirectory() as tmp:
        kwargs = {
            "status_pipe_path": os.path.join(tmp, "pipe"),
            "tool_config": {},
            "artifacts": {},
            "output_dir": tmp,
            "user_id": "u1",
            "session_id": "s1",
            "app_name": "app",
        }
        kwargs.update(overrides)
        return SandboxToolContextFacade(**kwargs)

def test_task_id_default_is_empty_string() -> None:
    facade = _make_facade()
    assert facade.task_id == ""

def test_task_id_round_trips_through_constructor() -> None:
    facade = _make_facade(task_id="task-abc-123")
    assert facade.task_id == "task-abc-123"

def test_identity_properties_round_trip() -> None:
    facade = _make_facade(
        user_id="alice",
        session_id="session-42",
        app_name="agent-mesh",
        task_id="task-99",
    )
    assert facade.user_id == "alice"
    assert facade.session_id == "session-42"
    assert facade.app_name == "agent-mesh"
    assert facade.task_id == "task-99"

def test_user_profile_default_is_empty() -> None:
    facade = _make_facade()
    assert facade.user_profile == {}
    assert facade.state["a2a_context"]["a2a_user_config"] == {}

def test_user_profile_parsed_from_blob() -> None:
    blob = json.dumps(
        {
            "_enterprise_capabilities": ["tool:*:execute"],
            "user_profile": {"id": "u-123", "work_email": "alice@example.com"},
        }
    )
    facade = _make_facade(a2a_user_config=blob)
    assert facade.user_profile == {"id": "u-123", "work_email": "alice@example.com"}
    nested = facade.state["a2a_context"]["a2a_user_config"]["user_profile"]
    assert nested["id"] == "u-123"
    assert nested["work_email"] == "alice@example.com"

def test_user_profile_tolerates_invalid_blob() -> None:
    facade = _make_facade(a2a_user_config="{not valid json")
    assert facade.user_profile == {}
    assert facade.state["a2a_context"]["a2a_user_config"] == {}

def test_user_profile_non_object_blob_is_ignored() -> None:
    facade = _make_facade(a2a_user_config="[1, 2, 3]")
    assert facade.user_profile == {}
