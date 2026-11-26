# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Claims triage with Hermes Agent (Nous Research), joined to Solace Agent Mesh through the agent link.

The agent is Hermes' ``AIAgent``, with the three tools registered in Hermes' tool registry. A Hermes agent keeps its
conversation and runs a turn on a thread of its own, so ``run_agent`` takes a function that builds the agent for each
task. The adapter turns off Hermes' long-term memory, skill learning and session store for mesh tasks, and sends
Hermes' questions (its ``clarify`` tool) and approvals through SAM.

Hermes needs Python 3.14 (its dependencies are marked `python_version >= '3.14'`) and is not on PyPI as a library:
install a checkout and the SDK into one environment.

    git clone https://github.com/NousResearch/hermes-agent.git /path/to/hermes-agent    # written against b65e078c
    uv venv --python 3.14 && uv pip install -e /path/to/hermes-agent -e . anthropic==0.87.0
    export HERMES_HOME=$PWD/.hermes-home        # keep Hermes' state away from your own ~/.hermes
    .venv/bin/python examples/claims_triage_hermes.py

The Hermes settings the adapter relies on (HERMES_HOME/config.yaml) are written here if the file does not exist:
tools reach the model directly (tool search off), a person decides approvals, and a hook may wait as long as an
approval can take.

Environment and scenario: examples/claims_common.py. ``CLAIMS_ASK_USER`` turns on Hermes' own ``clarify`` toolset.
"""

from __future__ import annotations

import asyncio
import inspect
import json
import logging
import os
import time
from pathlib import Path

HERMES_CONFIG = """tools:
  tool_search:
    enabled: off
approvals:
  mode: manual
plugins:
  hook_callback_timeout: 600
"""

if home := os.environ.get("HERMES_HOME"):
    hermes_config = Path(home) / "config.yaml"
    if not hermes_config.exists():
        hermes_config.parent.mkdir(parents=True, exist_ok=True)
        hermes_config.write_text(HERMES_CONFIG)

from claims_common import (  # noqa: E402
    FRAUD_SCREEN_SECONDS,
    INSTRUCTIONS,
    Settings,
    agent_config,
    fraud_screen,
    lookup_policy,
    route_claim,
)
from run_agent import AIAgent  # noqa: E402
from tools.interrupt import is_interrupted  # noqa: E402
from tools.registry import registry  # noqa: E402

from sam_link_sdk import ConversationMemory, MeshAgent, TaskContext  # noqa: E402
from sam_link_sdk.hermes import agent_defaults, mesh_agent_tools, run_agent, tool_infos, with_approval  # noqa: E402

TOOLSET = "claims"


def _fraud_screen(policy_id: str) -> dict:
    deadline = time.monotonic() + FRAUD_SCREEN_SECONDS
    while time.monotonic() < deadline and not is_interrupted():
        time.sleep(0.2)
    return {"error": "interrupted"} if is_interrupted() else {"policy_id": policy_id, "risk": "low"}


def _handler(function):
    """A Hermes tool handler (arguments as a dict, JSON text back) for one of the shared tools."""
    params = list(inspect.signature(function).parameters)

    def handle(args: dict, **kw) -> str:
        return json.dumps(function(*(str(args.get(p, "")) for p in params)))

    return handle


TOOLS = [
    (
        shared.__name__,
        _handler(run),
        inspect.getdoc(shared),
        {p: {"type": "string"} for p in inspect.signature(shared).parameters},
    )
    for shared, run in ((lookup_policy, lookup_policy), (route_claim, route_claim), (fraud_screen, _fraud_screen))
]


def register_tools() -> None:
    for name, handler, description, properties in TOOLS:
        registry.register(
            name=name,
            toolset=TOOLSET,
            description=description,
            handler=handler,
            schema={
                "name": name,
                "description": description,
                "parameters": {"type": "object", "properties": properties, "required": list(properties)},
            },
        )


def build_model(settings: Settings) -> dict:
    """The model arguments of an ``AIAgent``."""
    return {"base_url": settings.llm_base_url, "api_key": settings.llm_api_key, "model": settings.llm_model}


def make_handler(model: dict, settings: Settings | None = None):
    settings = settings or Settings()
    memory = ConversationMemory()
    limits = {"max_model_calls": settings.max_turns} if settings.max_turns else {}
    toolsets = [TOOLSET, *(["clarify"] if settings.ask_user else [])]

    def build(ctx: TaskContext) -> AIAgent:
        # A fresh agent per task, with only these toolsets: Hermes can run commands and edit files on its host.
        return AIAgent(**agent_defaults(ctx), **model, enabled_toolsets=toolsets, ephemeral_system_prompt=INSTRUCTIONS)

    async def handle(ctx: TaskContext) -> str:
        await ctx.progress("Triaging claim")
        return await run_agent(
            ctx,
            build,
            memory=memory,
            **limits,
            extra_tools=mesh_agent_tools(ctx) if settings.use_peers else (),
            approval=with_approval(names=settings.approval_tools) if settings.approval_tools else None,
            stream_text=settings.stream_text,
        )

    return handle


async def main() -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")
    register_tools()
    settings = Settings.from_env()
    config = agent_config(
        settings,
        tool_infos([{"name": n, "description": d} for n, _, d, _ in TOOLS], scopes=settings.tool_scopes),
        framework="Hermes Agent",
    )
    agent = MeshAgent(
        config,
        make_handler(build_model(settings), settings),
    )
    await agent.run_forever()


if __name__ == "__main__":
    asyncio.run(main())
