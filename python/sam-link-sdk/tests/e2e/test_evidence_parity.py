# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""A link agent registered on the platform reaches the evidence store as a native agent does: its task is traced with
its model and tool steps.

Kept apart from test_platform_parity.py because the evidence store is not in the published source; it reuses that
module's platform with the evidence store turned on.
"""

from __future__ import annotations

import pytest

from ..contract.sam_client import wait_until
from .model_script import ScriptedModel
from .test_platform_parity import MODEL, PRICED_ALIAS, running_platform

EVIDENCE_WAIT_SECONDS = 60


@pytest.fixture(scope="module")
def platform(tmp_path_factory):
    with running_platform(tmp_path_factory, {"SAM_FEATURE_EVIDENCE_STORE": "true"}) as running:
        yield running


def test_evidence_traces_its_task_with_its_model_and_tool_steps(platform):
    agent = platform.register("Evidence Claims")
    model = ScriptedModel()
    process = platform.start(agent, model, CLAIMS_PRICING_KEY=PRICED_ALIAS)
    task = platform.triage(agent, model)
    assert task.status == "completed", process.log_tail()

    def trace():
        resp = platform.client.request("GET", f"/api/v2/platform/evidence/traces/{task.task_id}")
        return resp.body["data"] if resp.status == 200 else None

    found = wait_until(trace, EVIDENCE_WAIT_SECONDS, "the evidence trace of the agent's task")

    assert found["agentName"] == agent.name
    assert found["status"] == "success"
    assert [(s["type"], s.get("target")) for s in found["steps"]] == [
        ("llm_call", None),
        ("tool_call", "lookup_policy"),
        ("llm_call", None),
        ("tool_call", "route_claim"),
        ("llm_call", None),
    ]
    assert {s["model"] for s in found["steps"] if s["type"] == "llm_call"} == {MODEL}
    assert all(s["status"] == "success" for s in found["steps"])
