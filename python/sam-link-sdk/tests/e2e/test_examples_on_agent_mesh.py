# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Every framework's claims-triage example, run as its own process, on a real Agent Mesh.

Each scenario works Agent Mesh as a user would (its HTTP API and the `sam` CLI) and asserts on two things only: what
Agent Mesh recorded about the task, and what the scripted model received. Nothing inside the agent process is
inspected or replaced, so a test here fails when the example, its framework, the SDK adapter or Agent Mesh stops
doing its part.
"""

from __future__ import annotations

import json
import threading
import time

import pytest

from ..contract.sam_server import TOOL_SCOPE_HELD
from .conftest import (
    ADAPTERS,
    APPROVAL,
    BLOCKED,
    EVERY_EXAMPLE,
    FRAMEWORKS,
    NATIVE,
    PLAIN,
    STOP,
    UNDECLARED,
    Examples,
)
from .model_script import Turn

EXT_TOOLS = "https://solace.com/a2a/extensions/sam/tools"
TASK_TIMEOUT_SECONDS = 90
PROMPT_TIMEOUT_SECONDS = 90
TICKET = "CLM-70434"
"""route_claim's ticket for P-1182 on fast-track (examples/claims_common.py): it exists only if the tool body ran."""
ANSWER = "P-1182 is routed to fast-track."
ASK = "Triage claim P-1182"
NOT_APPROVED = "did not approve this call to route_claim"
DENIED_FROM_THE_CLI = f"{NOT_APPROVED} (cancelled)"
ACCESS_DENIED = "do not have the scopes required to use route_claim"
SECRET = "sk-live-e2e0not0real0123456789abcdef"
QUIET_AFTER_STOP_SECONDS = 5
"""After a cancel, how long the model must hear nothing more. The run was cancelled while its model call was held, so a run
that ignored the cancel would go on (run the tool, call the model) within a second of the answer.
"""

NOT_OFFERED = {"langgraph": "not a valid tool", "hermes": "does not exist"}
"""How a framework shows that route_claim was refused when the user lacks its scope. The SDK's refusal is a tool result
saying access is denied, recorded in Agent Mesh as route_claim's result. LangGraph's prebuilt ToolNode is given only the
tools the user may use, so it answers the model's call itself ("not a valid tool") and no route_claim step is reported;
the model is still told, and was never offered the tool. Hermes leaves a denied tool out of the run (hermes/run.py
`_hide_denied_tools`), so its own dispatcher says the tool does not exist. The value is the wording.
"""


def triage_turns(route_args: dict | None = None) -> list[Turn]:
    """The claim flow every scenario but the stop one scripts: look the policy up, route it, answer."""
    return [
        Turn(calls=(("call_a", "lookup_policy", {"policy_id": "P-1182"}),)),
        Turn(
            calls=(("call_b", "route_claim", route_args or {"policy_id": "P-1182", "queue": "fast-track"}),),
            expects=("call_a",),
        ),
        Turn(text=ANSWER, expects=("call_b",), usage=(380, 15)),
    ]


APPROVING = [(fw, role) for fw in ADAPTERS for role in (APPROVAL, NATIVE) if role in FRAMEWORKS[fw].roles]
"""The agents whose route_claim needs approval: the SDK's with_approval (APPROVAL), and the framework's own flag (NATIVE)
on the frameworks that have one.
"""


def steps(view, kind: str, tool: str) -> list[dict]:
    return [s for s in view.signals() if s.get("type") == kind and s.get("tool_name") == tool]


def recorded(view) -> str:
    """Everything Agent Mesh recorded about the task: its row and every event of its tree."""
    return json.dumps(view.record) + json.dumps(view.entries)


def run_task(examples: Examples, role: str, *turns: Turn, text: str = ASK):
    agent = examples[role]
    agent.model.script(*turns)
    task_id = examples.client.send(agent.name, text)
    return agent, examples.client.wait_done(task_id, timeout=TASK_TIMEOUT_SECONDS)


@pytest.mark.parametrize("framework", EVERY_EXAMPLE, indirect=True)
def test_an_allowed_tool_runs_once_and_its_step_its_result_and_the_answer_reach_agent_mesh(examples):
    agent, view = run_task(examples, PLAIN, *triage_turns())

    assert agent.model.violations == []
    assert view.status == "completed", view.error_message
    assert view.answer().strip() == ANSWER
    assert len(steps(view, "tool_invocation_start", "route_claim")) == 1
    [result] = steps(view, "tool_result", "route_claim")
    assert TICKET in json.dumps(result.get("result_data"))
    requests = agent.model.requests
    assert len(requests) == 3
    assert TICKET in requests[2].tool_results()["call_b"]


@pytest.mark.parametrize("framework", ADAPTERS, indirect=True)
def test_a_tool_outside_the_users_scopes_never_runs_and_the_refusal_is_visible(examples, framework):
    """The harness has one user, so the blocked agent is configured to require a scope that user lacks. The model
    calls route_claim anyway, as a model that ignores the tools it was offered would."""
    agent, view = run_task(examples, BLOCKED, *triage_turns())

    assert agent.model.violations == []
    assert view.status == "completed", view.error_message
    assert TICKET not in agent.model.all_text()
    assert TICKET not in recorded(view)
    told = agent.model.requests[2].tool_results()["call_b"]
    if framework.id in NOT_OFFERED:
        assert "route_claim" not in agent.model.requests[0].offered_tools()
        assert NOT_OFFERED[framework.id] in told
        assert steps(view, "tool_result", "route_claim") == []
    else:
        assert ACCESS_DENIED in told
        [result] = steps(view, "tool_result", "route_claim")
        assert ACCESS_DENIED in json.dumps(result.get("result_data"))


@pytest.mark.parametrize("framework", ADAPTERS, indirect=True)
def test_a_tool_the_card_does_not_declare_never_runs_once_the_card_declares_a_scoped_tool(examples, framework):
    """The undeclared agent's card lists route_claim, which requires a scope the user holds, and leaves out
    lookup_policy, which its framework still offers. The model calls both. A card that names a scoped tool is the
    agent's statement of its tools, so a name it does not know is refused as a denied tool is, and route_claim runs."""
    agent, view = run_task(examples, UNDECLARED, *triage_turns())

    card = examples.client.agent_card(examples[UNDECLARED].name)
    [tools] = [e["params"]["tools"] for e in card["capabilities"]["extensions"] if e["uri"] == EXT_TOOLS]
    declared = {t["name"]: t["requiredScopes"] for t in tools}
    assert "lookup_policy" not in declared and declared["route_claim"] == [TOOL_SCOPE_HELD]
    assert agent.model.violations == []
    assert view.status == "completed", view.error_message
    assert "A. Rivera" not in agent.model.all_text()
    assert "A. Rivera" not in recorded(view)
    assert len(steps(view, "tool_invocation_start", "route_claim")) == 1
    assert TICKET in agent.model.requests[2].tool_results()["call_b"]
    told = agent.model.requests[1].tool_results()["call_a"]
    if framework.id in NOT_OFFERED:
        assert "lookup_policy" not in agent.model.requests[0].offered_tools()
        assert NOT_OFFERED[framework.id] in told
    else:
        assert "do not have the scopes required to use lookup_policy" in told


def ask_for_approval(examples: Examples, tmp_path, role: str) -> tuple:
    """Send the task through `sam task send`, which returns once Agent Mesh shows the user an approval prompt."""
    agent = examples[role]
    agent.model.script(*triage_turns())
    asked = examples.client.cli_send(agent.name, ASK, tmp_path / "ask", PROMPT_TIMEOUT_SECONDS)
    prompts = sorted((tmp_path / "ask").glob("hil_request_*.json"))
    assert len(prompts) == 1, f"`sam task send` saw no approval prompt:\n{asked.stdout[-2000:]}\n{asked.stderr[-2000:]}"
    prompt = json.loads(prompts[0].read_text())
    assert prompt["source"] == "tool_approval"
    assert prompt["agent_name"] == agent.name
    assert "route_claim" in json.dumps(prompt)
    return agent, prompts[0], prompt["task_id"]


def paused_at_the_prompt(examples: Examples, agent, task_id: str) -> None:
    """While the prompt waits, the run is paused: the task is still running, the model has had only its first two
    calls, and nothing the route would produce exists anywhere yet."""
    status = examples.client.status(task_id) or {}
    assert status.get("isRunning") is True, status
    assert len(agent.model.requests) == 2
    assert TICKET not in agent.model.all_text()
    assert TICKET not in json.dumps(examples.client.events(task_id))


@pytest.mark.parametrize("framework, role", APPROVING, indirect=["framework"], scope="module")
def test_an_approved_tool_runs_only_after_the_user_approves_it(examples, tmp_path, role, framework):
    """The run that resumed after the approval streams its answer, as the first run would have."""
    agent, prompt_file, task_id = ask_for_approval(examples, tmp_path, role)
    paused_at_the_prompt(examples, agent, task_id)

    answered_at = time.monotonic()
    answered = examples.client.cli_answer_approval(prompt_file, True, tmp_path / "answer", PROMPT_TIMEOUT_SECONDS)
    assert answered.returncode == 0, f"{answered.stdout[-2000:]}\n{answered.stderr[-2000:]}"
    view = examples.client.wait_done(task_id, timeout=TASK_TIMEOUT_SECONDS)

    assert agent.model.violations == []
    assert view.status == "completed", view.error_message
    assert view.answer().strip() == ANSWER
    requests = agent.model.requests
    assert len(requests) == 3
    assert TICKET in requests[2].tool_results()["call_b"]
    assert requests[2].at > answered_at
    assert len(steps(view, "tool_invocation_start", "route_claim")) == 1
    [result] = steps(view, "tool_result", "route_claim")
    assert TICKET in json.dumps(result.get("result_data"))
    assert requests[2].body.get("stream") is True
    pieces = view.streamed_text()
    assert len(pieces) > 1
    assert "".join(pieces).strip() == ANSWER


@pytest.mark.parametrize("framework, role", APPROVING, indirect=["framework"], scope="module")
def test_a_denied_tool_never_runs_and_the_model_is_told_it_was_not_approved(examples, tmp_path, role):
    agent, prompt_file, task_id = ask_for_approval(examples, tmp_path, role)
    paused_at_the_prompt(examples, agent, task_id)

    answered = examples.client.cli_answer_approval(prompt_file, False, tmp_path / "answer", PROMPT_TIMEOUT_SECONDS)
    assert answered.returncode == 0, f"{answered.stdout[-2000:]}\n{answered.stderr[-2000:]}"
    view = examples.client.wait_done(task_id, timeout=TASK_TIMEOUT_SECONDS)

    assert agent.model.violations == []
    assert view.status == "completed", view.error_message
    assert TICKET not in agent.model.all_text()
    assert TICKET not in recorded(view)
    assert DENIED_FROM_THE_CLI in agent.model.requests[2].tool_results()["call_b"]


@pytest.mark.parametrize("framework", ADAPTERS, indirect=True)
def test_a_run_stopped_during_a_model_call_ends_canceled_and_calls_the_model_no_more(examples):
    """The cancel lands while the model call is held, then the model answers with a tool call. A run that honours the
    cancel goes no further; one that ignores it runs the tool and calls the model again at once. Cancelling during the
    30-second fraud screen instead would need a 30-second wait per framework to see the same thing."""
    agent = examples[STOP]
    release, reached = threading.Event(), threading.Event()
    agent.model.script(
        Turn(calls=(("call_a", "lookup_policy", {"policy_id": "P-1182"}),), hold=release, reached=reached),
        *triage_turns()[1:],
    )
    task_id = examples.client.send(agent.name, ASK)
    assert reached.wait(TASK_TIMEOUT_SECONDS), "the model was never called"

    try:
        cancel = examples.client.cancel(task_id)
        assert cancel.status in (200, 202, 204), cancel
        view = examples.client.wait_done(task_id, timeout=TASK_TIMEOUT_SECONDS)
    finally:
        release.set()

    assert view.status in ("canceled", "cancelled"), view.error_message
    assert agent.model.no_request_beyond(1, within=QUIET_AFTER_STOP_SECONDS), (
        f"the model was called again after the cancel: {len(agent.model.requests)} requests"
    )
    assert steps(view, "tool_result", "lookup_policy") == []


@pytest.mark.parametrize("framework", EVERY_EXAMPLE, indirect=True)
def test_a_secret_in_a_tool_call_and_its_result_reaches_no_agent_mesh_record(examples):
    """lookup_policy echoes its argument into its result, so the credential is in the call and in the result. SAM-Go
    redacts too, so this proves no record holds it, not which side removed it."""
    leaky = {"policy_id": f"P-9 api_key={SECRET}"}
    agent, view = run_task(
        examples,
        PLAIN,
        Turn(calls=(("call_a", "lookup_policy", leaky),)),
        Turn(text="Policy P-9 was not found.", expects=("call_a",)),
    )

    assert agent.model.violations == []
    assert view.status == "completed", view.error_message
    assert SECRET in agent.model.requests[1].tool_results()["call_a"]
    assert len(steps(view, "tool_invocation_start", "lookup_policy")) == 1
    assert len(steps(view, "tool_result", "lookup_policy")) == 1
    assert SECRET not in recorded(view)
