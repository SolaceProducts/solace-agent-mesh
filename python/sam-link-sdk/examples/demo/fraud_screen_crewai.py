# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Fraud Screen: a CrewAI crew that scores a claim's fraud risk, asking the records agent for the claim history.

Started by stack/start_agents.sh (README.md); on its own: `uv run --extra crewai python fraud_screen_crewai.py`.
"""

from __future__ import annotations

import asyncio
import os
from datetime import date

# CrewAI sends anonymous telemetry and execution traces to its own service by default; keep task content in.
for _var, _value in (("CREWAI_DISABLE_TELEMETRY", "true"), ("CREWAI_TRACING_ENABLED", "false")):
    os.environ.setdefault(_var, _value)
if not os.environ.get("DEMO_OTLP_ENDPOINT"):
    os.environ.setdefault("OTEL_SDK_DISABLED", "true")

from common import CLAIMS, POLICIES, litellm_model, mesh_settings  # noqa: E402
from crewai import LLM, Agent, Crew, Task  # noqa: E402
from crewai.tools import tool  # noqa: E402

from sam_link_sdk import AgentConfig, MeshAgent, Skill, TaskContext  # noqa: E402
from sam_link_sdk.crewai import mesh_agent_tools, run_agent, tool_infos  # noqa: E402


@tool("fraud_signals")
def fraud_signals(claim_id: str) -> dict:
    """Rule-based fraud signals for a claim: policy age, lapsed status and the amount against the limit."""
    claim = CLAIMS.get(claim_id.upper())
    if claim is None:
        return {"error": f"no claim {claim_id}"}
    policy = POLICIES[claim["policy_id"]]
    filed = date.fromisoformat(claim["filed"])
    return {
        "policy_age_days": (filed - date.fromisoformat(policy["start_date"])).days,
        "policy_lapsed": policy["status"] != "active",
        "amount_vs_limit": round(claim["amount_usd"] / policy["limit_usd"], 2),
        "policy_id": claim["policy_id"],
    }


TOOLS = [fraud_signals]


def build_model() -> LLM:
    return LLM(**litellm_model())


def build_crew(ctx: TaskContext) -> Crew:
    analyst = Agent(
        role="Fraud analyst",
        goal="Score the fraud risk of an insurance claim from its signals and the policy's claim history",
        backstory="Get the claim's signals, then get the policy's earlier claims from the records agent. Two or more claims "
        "in 60 days, a policy under 90 days old, or a lapsed policy each raise the risk. Low risk: none. Medium: one. High: two or more.",
        llm=build_model(),
        tools=[*TOOLS, *mesh_agent_tools(ctx)],
        verbose=False,
    )
    screen = Task(
        description="Screen this for fraud: {request}",
        expected_output="The risk (low, medium or high) and the signals behind it, in two sentences.",
        agent=analyst,
    )
    return Crew(agents=[analyst], tasks=[screen], verbose=False)


async def main() -> None:
    async def handle(ctx: TaskContext) -> str:
        return await run_agent(ctx, build_crew)

    config = AgentConfig(
        name="fraud-screen",
        display_name="Fraud Screen",
        description="Scores the fraud risk of a claim (low, medium or high) with the reasons (built with CrewAI)",
        framework="CrewAI",
        peers=["records-lookup"],
        skills=[Skill(id="screen", name="Screen a claim", description="Fraud risk for a claim id")],
        tools=tool_infos(TOOLS),
        **mesh_settings(),
    )
    await MeshAgent(config, handle).run_forever()


if __name__ == "__main__":
    asyncio.run(main())
