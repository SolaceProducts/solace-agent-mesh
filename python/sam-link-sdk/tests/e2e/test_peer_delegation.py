# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The delegation promise docs/delegation.md makes, held to every adapter's example on a real Agent Mesh: the agent is
offered the peers its setting lists, the model's call to one goes through Agent Mesh to that agent, the peer's answer
comes back to the model as the tool's result, and the peer's steps nest under the call.

The peer agent may call two agents (its entrypoint allowlist) but its own setting lists one: the other must not be
offered, and must not be sent a task. The peer is the same framework's plain agent, with a scripted model of its own,
which saves starting a process only this test would use.
"""

from __future__ import annotations

import json

import pytest

from .conftest import ADAPTERS, APPROVAL, PEER, PEER_LISTED, PLAIN
from .model_script import Turn
from .test_examples_on_agent_mesh import ANSWER, TICKET, run_task, steps, triage_turns

ASK_THE_PEER = "Triage claim P-1182"
CALL_ID = "call_p"
OWN_TOOLS = ["fraud_screen", "lookup_policy", "route_claim"]

TOOL_NAME_UNDERSCORED = {"crewai"}
"""CrewAI turns a tool's name into a valid identifier, so its model is offered the peer's name with underscores (its
steps still carry the peer's own name, which SAM checks against the agent's peers), and it
does not tell a tool its call id, so the peer's steps nest under the agent rather than under the call
(docs/delegation.md). ADK hands the model a function's return as {"result": ...}.
"""
NESTS_UNDER_THE_AGENT = {"crewai"}
RESULT_WRAPPED = {"adk"}


def peer_tool(framework, role: str) -> str:
    name = "peer_" + framework.agent_name(role)
    return name.replace("-", "_") if framework.id in TOOL_NAME_UNDERSCORED else name


def forwarded_from(view, peer: str) -> list[dict]:
    """The metadata of every event the entrypoint forwarded from `peer` into the task's own stream."""
    found = []
    for event in view.events():
        message = (((event.get("full_payload") or {}).get("result") or {}).get("status") or {}).get("message") or {}
        metadata = message.get("metadata") or {}
        if metadata.get("forwarded_from_peer") == peer:
            found.append(metadata)
    return found


@pytest.mark.parametrize("framework", ADAPTERS, indirect=True)
def test_a_peer_call_goes_through_agent_mesh_and_the_peers_answer_comes_back_as_the_tool_result(examples, framework):
    callee, listed_out = examples[PLAIN], examples[APPROVAL]
    callee.model.script(*triage_turns())
    listed_out.model.script()
    tool = peer_tool(framework, PEER_LISTED)
    agent, view = run_task(
        examples,
        PEER,
        Turn(calls=((CALL_ID, tool, {"task_description": ASK_THE_PEER}),)),
        Turn(text="The claims desk routed it.", expects=(CALL_ID,)),
    )

    assert agent.model.violations == [] and callee.model.violations == []
    assert view.status == "completed", view.error_message
    assert view.answer().strip() == "The claims desk routed it."
    assert agent.model.requests[0].offered_tools() == sorted([*OWN_TOOLS, tool])
    assert len(callee.model.requests) == 3
    assert ASK_THE_PEER in callee.model.requests[0].text()
    told = agent.model.requests[1].tool_results()[CALL_ID]
    assert (json.loads(told) == {"result": ANSWER}) if framework.id in RESULT_WRAPPED else (told == ANSWER)
    shown = "peer_" + framework.agent_name(PEER_LISTED)
    [start] = steps(view, "tool_invocation_start", shown)
    assert start["tool_args"] == {"task_description": ASK_THE_PEER}
    [result] = steps(view, "tool_result", shown)
    assert ANSWER in json.dumps(result["result_data"])
    [ticket] = steps(view, "tool_result", "route_claim")
    assert TICKET in json.dumps(ticket["result_data"])
    nested_under = {m.get("function_call_id") for m in forwarded_from(view, callee.name)}
    assert nested_under == ({None} if framework.id in NESTS_UNDER_THE_AGENT else {CALL_ID})
    assert listed_out.model.requests == []
    assert forwarded_from(view, listed_out.name) == []


@pytest.mark.parametrize("framework", ["hermes"], indirect=True)
def test_each_task_calls_the_peer_as_itself_though_hermes_registers_the_peer_tool_once(examples, framework):
    """Hermes keeps one process-wide tool registry, so the peer tool the first task registered answers every later
    task (docs/frameworks/hermes.md): a later task's call must still go out as that task, nested under its own call."""
    callee = examples[PLAIN]
    tool = peer_tool(framework, PEER_LISTED)
    for n in (1, 2):
        call_id, ask = f"call_p{n}", f"{ASK_THE_PEER}, request {n}"
        callee.model.script(*triage_turns())
        agent, view = run_task(
            examples,
            PEER,
            Turn(calls=((call_id, tool, {"task_description": ask}),)),
            Turn(text="The claims desk routed it.", expects=(call_id,)),
        )

        assert agent.model.violations == [] and callee.model.violations == []
        assert view.status == "completed", view.error_message
        assert ask in callee.model.requests[0].text()
        assert agent.model.requests[1].tool_results()[call_id] == ANSWER
        assert {m.get("function_call_id") for m in forwarded_from(view, callee.name)} == {call_id}
