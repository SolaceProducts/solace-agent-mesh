# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""An SDK agent as a SAM workflow node: the node's instruction or input as the prompt, its structured input and
invocation on the context, and a text or structured result out. (How the agent link carries a node's request and
result: tests/test_link_protocol.py.)"""

from __future__ import annotations

from sam_link_sdk import TaskResult
from sam_link_sdk._model import RUN, Workflow

from .agent_rig import start_agent

NODE = Workflow("claims-pipeline", "triage")


async def run_node(handler, node_input, instruction=""):
    agent, transport = await start_agent(handler)
    await transport.deliver("T1", instruction, input=node_input, workflow=NODE, session_behavior=RUN, context_id="CTX")
    return agent, transport


async def test_instruction_stays_the_prompt_and_the_input_is_exposed():
    seen = {}

    async def handler(ctx):
        seen.update(
            text=ctx.text,
            structured=ctx.structured_input,
            invocation=ctx.invocation,
            files=ctx.files,
            session_id=ctx.session_id,
        )
        return "ok"

    agent, _ = await run_node(handler, {"message": "from input", "claim": "P-1"}, instruction="Triage the claim")
    assert seen == {
        "text": "Triage the claim",
        "structured": {"message": "from input", "claim": "P-1"},
        "invocation": {"workflow_name": "claims-pipeline", "node_id": "triage"},
        "files": [],
        "session_id": "",
    }
    await agent.stop()


async def test_input_message_is_the_prompt_when_the_node_has_no_instruction():
    seen = {}

    async def handler(ctx):
        seen["text"] = ctx.text
        return "ok"

    agent, _ = await run_node(handler, {"message": "Triage claim P-1182"})
    assert seen["text"] == "Triage claim P-1182"
    await agent.stop()


async def test_input_without_a_message_becomes_json_text():
    seen = {}

    async def handler(ctx):
        seen["text"] = ctx.text
        return "ok"

    agent, _ = await run_node(handler, {"policy": "P-1", "amount": 250})
    assert seen["text"] == '{"amount": 250, "policy": "P-1"}'
    await agent.stop()


async def test_a_structured_result_is_the_answers_data():
    async def handler(ctx):
        return TaskResult("Routed to fast-track.", data={"queue": "fast-track", "ticket": "CLM-1"})

    agent, transport = await run_node(handler, {"message": "x"})
    answer = transport.finished["T1"]
    assert (answer.state, answer.text, answer.data) == (
        "completed",
        "Routed to fast-track.",
        {"queue": "fast-track", "ticket": "CLM-1"},
    )
    await agent.stop()
