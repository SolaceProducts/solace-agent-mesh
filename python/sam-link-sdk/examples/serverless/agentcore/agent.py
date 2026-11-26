# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The agent: a Strands agent joined to the mesh with the sam_link_sdk SDK, run on Amazon Bedrock AgentCore Runtime.

Replace the tools, prompt and card with your own. Everything mesh-specific is in build_agent: the agent joins through
SAM's agent link (SAM verifies each user), offers the mesh's agents as tools (here, the policy desk), asks the user
before pay_claim runs, and reports model and tool steps and token usage to SAM.

Environment (the runtime's; see README.md):
  BEDROCK_MODEL_ID         the Bedrock model or inference profile, called with the runtime's execution role; or
  AZURE_OPENAI_ENDPOINT    https://<account>.openai.azure.com, with AZURE_OPENAI_API_KEY and AZURE_OPENAI_DEPLOYMENT,
                           for a model outside AWS (any OpenAI-compatible endpoint works the same way)
  SAM_LINK_URL, SAM_LINK_TOKEN  SAM's agent link entrypoint (wss://<gateway>/gw/<gateway_id>/v1/connect) and the
                           agent's link token (sam-link-sdk link-token); SAM_LINK_TOKEN_SECRET_ID to read it from Secrets
                           Manager (likewise AZURE_OPENAI_API_KEY_SECRET_ID)
  SAM_LINK_TRUST_STORE_PATH  a CA file or a directory of CA certificates for a gateway with a private certificate,
                           relative to this folder or absolute
  SAM_AGENT_PEERS          comma-separated mesh agents it may call (default policy-desk)
  SAM_PAY_CLAIM_SCOPE      optional: a scope pay_claim requires (for example tool:claims:pay); SAM then pays out only
                           for users who hold it, and the call is refused and audited for anyone else
  SAM_SPEND_MODEL_KEY      optional: the SAM model configuration (alias) whose prices cost this model's calls
"""

from __future__ import annotations

import dataclasses
import os
from pathlib import Path

from strands import Agent, tool

from sam_link_sdk import AgentConfig, MeshAgent, Skill, TaskContext
from sam_link_sdk.link import LinkConfig, LinkTransport
from sam_link_sdk.strands import mesh_agent_tools, run_agent, tool_infos, with_approval, with_scopes

NAME = os.environ.get("SAM_AGENT_NAME", "claims-assistant")
PEERS = [p.strip() for p in os.environ.get("SAM_AGENT_PEERS", "policy-desk").split(",") if p.strip()]
HERE = Path(__file__).parent


@tool
def pay_claim(claim_id: str, amount: float) -> dict:
    """Pay out an approved insurance claim."""
    return {"paid": True, "claim_id": claim_id, "amount": amount, "reference": f"PAY-{claim_id}"}


def _scopes() -> dict[str, list[str]]:
    """The scopes each tool requires: SAM_PAY_CLAIM_SCOPE for pay_claim, when set."""
    scope = os.environ.get("SAM_PAY_CLAIM_SCOPE", "").strip()
    return {"pay_claim": [scope]} if scope else {}


TOOLS = with_scopes([pay_claim], _scopes())

PROMPT = (
    "You are a claims assistant for a fictional insurer. For coverage questions, ask the policy desk agent "
    "with its tool and use its answer. To pay a claim, call pay_claim with the claim id and amount. "
    "Answer in two sentences or fewer."
)


def _model():
    """(model, its name in SAM's cost ledger)."""
    if os.environ.get("AZURE_OPENAI_ENDPOINT"):
        from strands.models.openai import OpenAIModel

        endpoint = os.environ["AZURE_OPENAI_ENDPOINT"].rstrip("/")
        deployment = os.environ["AZURE_OPENAI_DEPLOYMENT"]
        return OpenAIModel(
            client_args={"api_key": os.environ["AZURE_OPENAI_API_KEY"], "base_url": f"{endpoint}/openai/v1/"},
            model_id=deployment,
        ), deployment
    from strands.models.bedrock import BedrockModel

    model_id = os.environ["BEDROCK_MODEL_ID"]
    return BedrockModel(model_id=model_id), model_id


def _link(url: str) -> LinkTransport:
    """SAM's agent link at `url` (the entrypoint's connect URL), as this agent (SAM_LINK_TOKEN)."""
    config = dataclasses.replace(LinkConfig.from_env(agent=NAME), url=url)
    if config.trust_store_path and not Path(config.trust_store_path).is_absolute():
        config = dataclasses.replace(config, trust_store_path=str(HERE / config.trust_store_path))
    return LinkTransport(config)


def build_agent(link_url: str) -> MeshAgent:
    model, model_name = _model()

    async def handle(ctx: TaskContext) -> str:
        def make(_ctx: TaskContext) -> Agent:
            return Agent(
                model=model,
                tools=[*TOOLS, *mesh_agent_tools(ctx, PEERS)],
                system_prompt=PROMPT,
                hooks=[with_approval(ctx, TOOLS, names=["pay_claim"])],
                callback_handler=None,
            )

        return await run_agent(ctx, make)

    config = AgentConfig(
        name=NAME,
        display_name=os.environ.get("SAM_AGENT_DISPLAY_NAME", "Claims Assistant"),
        description="Handles insurance claims; runs on Amazon Bedrock AgentCore",
        skills=[Skill(id="claims", name="Claims", description="Checks coverage with the policy desk and pays claims")],
        tools=tool_infos(TOOLS),
        peers=PEERS,
        framework="Strands Agents on Amazon Bedrock AgentCore",
        owner=os.environ.get("SAM_AGENT_OWNER", ""),
        team=os.environ.get("SAM_AGENT_TEAM", ""),
        keepalive_seconds=0,
        spend_model_keys=(
            {model_name: os.environ["SAM_SPEND_MODEL_KEY"]} if os.environ.get("SAM_SPEND_MODEL_KEY") else {}
        ),
    )
    return MeshAgent(config, handle, transport=_link(link_url))
