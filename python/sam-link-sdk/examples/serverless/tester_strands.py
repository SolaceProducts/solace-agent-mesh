# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Serverless Tester: a request-driven Strands agent for the cases a per-request process has to get right: a tool
that waits for a person's approval, and a question delegated to another mesh agent (policy-desk).

    python tester_strands.py wake | announce

Mesh: SAM_LINK_URL and SAM_LINK_TOKEN, this agent's own token; the entrypoint lists policy-desk as its peer.
"""

from __future__ import annotations

import asyncio
import logging
import os
import sys

from strands import Agent, tool
from strands.models.openai import OpenAIModel

from sam_link_sdk import AgentConfig, MeshAgent, Skill, TaskContext
from sam_link_sdk.strands import mesh_agent_tools, run_agent, tool_infos, with_approval

NAME = "serverless-tester"
PEERS = ["policy-desk"]
NOTES: list[str] = []


@tool
def file_note(text: str) -> dict:
    """File a note on the customer's record."""
    NOTES.append(text)
    return {"filed": True, "text": text}


TOOLS = [file_note]
PROMPT = (
    "You test a request-driven agent. To file a note, call file_note. For questions about a claim's coverage, "
    "ask the policy desk agent with its tool and report its answer in one sentence."
)


def build() -> MeshAgent:
    base_url = os.environ.get("DEMO_OPENAI_BASE_URL") or os.environ["LLM_SERVICE_ENDPOINT"]
    model_id = os.environ.get("DEMO_OPENAI_MODEL") or os.environ["LLM_SERVICE_GENERAL_MODEL_NAME"].removeprefix(
        "openai/"
    )
    model = OpenAIModel(
        client_args={"api_key": os.environ["LLM_SERVICE_API_KEY"], "base_url": base_url}, model_id=model_id
    )

    async def handle(ctx: TaskContext) -> str:
        def make(_ctx: TaskContext) -> Agent:
            return Agent(
                model=model,
                tools=[*TOOLS, *mesh_agent_tools(ctx, PEERS)],
                system_prompt=PROMPT,
                hooks=[with_approval(ctx, TOOLS, names=["file_note"])],
                callback_handler=None,
            )

        return await run_agent(ctx, make)

    config = AgentConfig(
        name=NAME,
        display_name="Serverless Tester",
        description="Tests approvals and delegation from a request-driven agent",
        skills=[Skill(id="test", name="Test", description="Files notes; asks the policy desk")],
        tools=tool_infos(TOOLS),
        peers=PEERS,
        framework="Strands Agents (serverless)",
        keepalive_seconds=0,
    )
    return MeshAgent(config, handle)


async def main(mode: str) -> None:
    logging.basicConfig(level=logging.INFO, format=f"%(asctime)s [{mode} %(process)d] %(name)s %(message)s")
    agent = build()
    if mode == "announce":
        await agent.announce()
    else:
        await agent.handle_wake()


if __name__ == "__main__":
    asyncio.run(main(sys.argv[1] if len(sys.argv) > 1 else "wake"))
