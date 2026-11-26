# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Regulations: a Hermes Agent that knows the settlement rules of each jurisdiction.

Hermes needs Python 3.14 and its own state directory (HERMES_HOME), so it runs in an environment of its own, as
docs/frameworks/hermes.md installs it. stack/start_agents.sh starts it when HERMES_PYTHON names that environment's
interpreter.
"""

from __future__ import annotations

import asyncio
import json
import os
from pathlib import Path

if home := os.environ.get("HERMES_HOME"):
    config = Path(home) / "config.yaml"
    if not config.exists():
        config.parent.mkdir(parents=True, exist_ok=True)
        config.write_text(
            "tools:\n  tool_search:\n    enabled: off\napprovals:\n  mode: manual\nplugins:\n  hook_callback_timeout: 600\n"
        )

from common import RULES, mesh_settings, openai_compatible_model
from run_agent import AIAgent
from tools.registry import registry

from sam_link_sdk import AgentConfig, MeshAgent, Skill, TaskContext
from sam_link_sdk.hermes import agent_defaults, run_agent, tool_infos

TOOLSET = "regulations"


def lookup_rule(args: dict, **kw) -> str:
    jurisdiction, topic = str(args.get("jurisdiction", "")).title(), str(args.get("topic", "")).lower()
    matches = [text for (j, t), text in RULES.items() if j == jurisdiction and (topic in t or t in topic)]
    return json.dumps({"rules": matches} if matches else {"error": f"no rule on '{topic}' in {jurisdiction}"})


def register_tools() -> None:
    registry.register(
        name="lookup_rule",
        toolset=TOOLSET,
        description="Look up the claim-handling rules of a jurisdiction on a topic.",
        schema={
            "name": "lookup_rule",
            "description": "Look up the claim-handling rules of a jurisdiction on a topic, such as "
            "a settlement deadline or a kind of loss.",
            "parameters": {
                "type": "object",
                "properties": {"jurisdiction": {"type": "string"}, "topic": {"type": "string"}},
                "required": ["jurisdiction", "topic"],
            },
        },
        handler=lookup_rule,
    )


PROMPT = (
    "You are the regulations desk of an insurance company. Answer questions about the claim-handling rules of a "
    "jurisdiction with the lookup_rule tool, citing each rule's number. If there is no rule, say so."
)


def build_model() -> dict:
    """The model arguments of an ``AIAgent``."""
    llm = openai_compatible_model()
    return {"base_url": llm["base_url"], "api_key": llm["api_key"], "model": llm["model"]}


def build_agent(ctx: TaskContext) -> AIAgent:
    agent = AIAgent(**agent_defaults(ctx), **build_model(), enabled_toolsets=[TOOLSET], ephemeral_system_prompt=PROMPT)
    # Hermes' own session store is not part of the mesh's record.
    agent._persist_disabled = True
    return agent


async def main() -> None:
    register_tools()

    async def handle(ctx: TaskContext) -> str:
        return await run_agent(ctx, build_agent)

    config = AgentConfig(
        name="regulations",
        display_name="Regulations",
        description="Knows the settlement rules of each jurisdiction and cites them (built with Hermes Agent)",
        framework="Hermes Agent",
        skills=[Skill(id="rules", name="Look up a rule", description="Claim-handling rules by jurisdiction and topic")],
        tools=tool_infos(
            [{"name": "lookup_rule", "description": "Look up the claim-handling rules of a jurisdiction on a topic."}]
        ),
        **mesh_settings(),
    )
    await MeshAgent(config, handle).run_forever()


if __name__ == "__main__":
    asyncio.run(main())
