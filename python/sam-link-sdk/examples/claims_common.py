# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""What every claims-triage example shares: the scenario, the environment it reads, and the agent's card.

Each ``claims_triage_<framework>.py`` builds the same agent with its framework's own tool, model and agent types
around these: three tools over three made-up policies, one set of instructions, and the same card. Every example has
the same sections in the same order (tools, model, handler, joining the mesh), offers ``make_handler(model,
settings)`` for tests, and joins the mesh through the agent link (docs/agent-link.md).

Run one (pick the framework's extra; the LangChain one needs ``--extra openai``)::

    export LLM_SERVICE_ENDPOINT=https://<model-endpoint> LLM_SERVICE_API_KEY=<key> LLM_SERVICE_GENERAL_MODEL_NAME=<model>
    export SAM_LINK_URL=wss://<gateway>/gw/<gateway_id>/v1/connect SAM_LINK_TOKEN=$(cat claims-triage.link-token)
    uv run --extra strands python examples/claims_triage_strands.py

Environment, the same for every example:

    SAM_LINK_URL, SAM_LINK_TOKEN    the agent link entrypoint and this agent's token (``sam-link-sdk link-token``);
                                    SAM_LINK_TRUST_STORE_PATH for an entrypoint with a private CA
    SAM_AGENT_NAME                  default claims-triage (the name the entrypoint knows the token by)
    SAM_DISPLAY_NAME                default "Claims Triage"
    LLM_SERVICE_ENDPOINT, LLM_SERVICE_API_KEY, LLM_SERVICE_GENERAL_MODEL_NAME   the model, as in SAM's .env (whose
                                    openai/<model> prefix only the ADK and CrewAI examples take: for the others set
                                    CLAIMS_LLM_MODEL to the name without it)
    CLAIMS_LLM_BASE_URL, CLAIMS_LLM_MODEL   optional overrides of the endpoint and the model
    CLAIMS_PRICING_KEY              the alias of the SAM model configuration whose prices cost the model's calls
                                    (AgentConfig.spend_model_keys); without it SAM leaves them unpriced
    CLAIMS_MAX_LLM_CALLS            model calls a task may start (default: the SDK's, 20)
    CLAIMS_MAX_TURNS                the run's limit on model calls: run_agent's max_model_calls (OpenAI Agents,
                                    LangChain, ADK, CrewAI, Hermes), LangGraph recursion_limit, Agent Framework
                                    max_iterations (set on its client); default: run_agent's 10, LangGraph's 16,
                                    Agent Framework's own
    CLAIMS_STREAM_TEXT              false to send the answer whole at the end of the run instead of streaming it, on the
                                    examples whose adapter streams (every run_agent has stream_text, default True)
    CLAIMS_BLOCKED_WORD             OpenAI Agents only: an input guardrail blocks a request containing it
    CLAIMS_BLOCKED_OUTPUT_WORD      OpenAI Agents and CrewAI: an output guardrail blocks an answer containing it
    CLAIMS_AGENT_TOOL               OpenAI Agents only: true to run the triage agent as a tool (Agent.as_tool,
                                    triage_claim) of an intake agent that answers the user (default: off)
    CLAIMS_STREAM_THINKING          true to send the model's reasoning to the chat (AgentConfig.stream_thinking), on the
                                    examples whose framework exposes it (default: off)
    CLAIMS_REQUEST_CONTEXT          true to put the gateway's request context (system purpose, user profile) in the
                                    model's prompt (``include_request_context=True``), on the examples that use
                                    ``run_agent`` of a framework with a prompt to put it in (default: off)
    CLAIMS_HANDLE_TOOL_ERRORS       LangGraph only: true builds the tools with handle_tool_error, raising ToolException, so a
                                    failing tool is told to the model instead of ending the run (default: it ends)
    CLAIMS_CREW_REPLY               CrewAI only: true adds a second crew agent, without tools, that writes the reply to
                                    the claimant from the triage result, so the crew's answer is the second agent's
                                    (default: one agent)
    CLAIMS_FRAUD_SCREEN_SECONDS     how long the fraud screen takes (default 30), for trying a cancel sooner
    CLAIMS_KEEPALIVE_SECONDS        how long a task may be silent before the agent reports "Still working"
                                    (default: the SDK's, 120)
    CLAIMS_TASK_TIMEOUT_SECONDS     how long a task may run before it is stopped (default: the SDK's, 600)
    CLAIMS_SHUTDOWN_GRACE_SECONDS   how long running tasks may finish after SIGTERM (default: the SDK's, 20)
    CLAIMS_LLM_CONTEXT_WINDOW       the model's context window in tokens, for a model the framework does not know
                                    (CrewAI otherwise assumes a small one and summarizes long tool results away)
    CLAIMS_PEER_AGENTS              mesh agents the triage agent may delegate to, comma-separated (its card's peers,
                                    AgentConfig.peers), or * for every agent the user may invoke (default: none)
    CLAIMS_APPROVAL_TOOLS           tools that pause the task for the user's approval first, comma-separated (for
                                    example route_claim)
    CLAIMS_NATIVE_APPROVAL          true to make route_claim need approval with the framework's own flag (OpenAI Agents
                                    needs_approval, Agent Framework approval_mode, ADK require_confirmation, Strands
                                    interrupt, LangGraph interrupt) instead of the SDK's with_approval; the run still asks
                                    the user through Agent Mesh. Not on the examples whose framework has no such flag
    CLAIMS_ASK_USER                 true to let the model ask the user questions
    CLAIMS_ROUTE_SCOPE              a scope route_claim requires (for example tool:claims:route): SAM then runs it
                                    only for users who hold it; for anyone else the call is refused and audited
                                    (docs/security.md). Default: none, every user may route.
    CLAIMS_UNDECLARED_TOOL          a tool name to leave off the agent's card while the framework still offers it, to show
                                    that a tool the card does not declare is refused once the card declares a scoped tool
                                    (docs/security.md). Default: none, the card lists every tool.

The ADK and CrewAI examples take the model name with its provider prefix (``anthropic/claude-sonnet-5``, or
``openai/<model>`` for an OpenAI-compatible endpoint such as a LiteLLM proxy): ADK reaches the model through LiteLLM,
CrewAI through its own client for that provider. The others call an OpenAI-compatible Chat Completions endpoint with
the model name as it is.

To try cancel, ask "Triage claim P-1182 and run a fraud check first", then press Stop: the fraud screen takes 30 s.
"""

from __future__ import annotations

import asyncio
import os
import time
import zlib
from dataclasses import dataclass

from sam_link_sdk import AgentConfig, Skill, Suggestion
from sam_link_sdk.config import ToolInfo

POLICIES = {
    "P-1182": {"tier": "gold", "holder": "A. Rivera", "deductible": 250, "status": "active"},
    "P-2040": {"tier": "silver", "holder": "J. Chen", "deductible": 1000, "status": "active"},
    "P-3300": {"tier": "bronze", "holder": "M. Okafor", "deductible": 2500, "status": "lapsed"},
}
QUEUES = ("fast-track", "standard", "manual-review")
FRAUD_SCREEN_SECONDS = float(os.environ.get("CLAIMS_FRAUD_SCREEN_SECONDS") or 30)

INSTRUCTIONS = """You triage insurance claims.
Look up the policy first. Route gold policies that are active to fast-track, other active policies to
standard, and lapsed or unknown policies to manual-review. Run the fraud screen only if the user asks
for a fraud check. Answer in one or two sentences naming the queue and ticket."""


def lookup_policy(policy_id: str) -> dict:
    """Look up an insurance policy by id (for example P-1182). Returns tier, deductible and status."""
    if not policy_id.upper().startswith("P-"):
        raise ValueError(f"malformed policy id {policy_id}")
    if policy_id.upper() == "P-BACKEND":
        import httpx
        import openai

        raise openai.APITimeoutError(request=httpx.Request("GET", "https://policy-backend.example/policies"))
    return POLICIES.get(policy_id.upper(), {"error": f"no policy {policy_id}"})


def route_claim(policy_id: str, queue: str) -> dict:
    """Route a claim to a work queue: fast-track, standard, or manual-review."""
    if queue not in QUEUES:
        return {"error": f"unknown queue {queue}"}
    ticket = zlib.crc32(f"{policy_id}:{queue}".encode()) % 100000
    return {"policy_id": policy_id, "queue": queue, "ticket": f"CLM-{ticket:05d}"}


async def fraud_screen(policy_id: str) -> dict:
    """Run the slow fraud screen for a policy (takes about 30 seconds). Only use when asked for a fraud check."""
    await asyncio.sleep(FRAUD_SCREEN_SECONDS)
    return {"policy_id": policy_id, "risk": "low"}


def fraud_screen_blocking(policy_id: str) -> dict:
    """Run the slow fraud screen for a policy (takes about 30 seconds). Only use when asked for a fraud check."""
    time.sleep(FRAUD_SCREEN_SECONDS)
    return {"policy_id": policy_id, "risk": "low"}


def _names(value: str) -> list[str]:
    return [n.strip() for n in value.split(",") if n.strip()]


def _seconds(name: str) -> float | None:
    return float(os.environ[name]) if os.environ.get(name) else None


@dataclass(frozen=True)
class Settings:
    agent_name: str = "claims-triage"
    display_name: str = "Claims Triage"
    llm_base_url: str = ""
    llm_api_key: str = ""
    llm_model: str = ""
    pricing_key: str = ""
    use_peers: bool = False
    peer_agents: tuple[str, ...] | None = None
    approval_tools: tuple[str, ...] = ()
    native_approval: bool = False
    ask_user: bool = False
    route_scope: str = ""
    undeclared_tool: str = ""
    llm_context_window: int | None = None
    max_llm_calls: int | None = None
    max_turns: int | None = None
    blocked_word: str = ""
    blocked_output_word: str = ""
    agent_tool: bool = False
    stream_thinking: bool = False
    request_context: bool = False
    handle_tool_errors: bool = False
    crew_reply: bool = False
    keepalive_seconds: float | None = None
    task_timeout_seconds: float | None = None
    shutdown_grace_seconds: float | None = None
    stream_text: bool = True

    @property
    def tool_scopes(self) -> dict[str, list[str]]:
        """The scopes each tool requires, for ``with_scopes(TOOLS, ...)`` (Hermes: ``tool_infos(..., scopes=...)``):
        the tool refuses a user without them, and the card says so."""
        return {"route_claim": [self.route_scope]} if self.route_scope else {}

    @classmethod
    def from_env(cls) -> Settings:
        peers = os.environ.get("CLAIMS_PEER_AGENTS", "").strip()
        return cls(
            agent_name=os.environ.get("SAM_AGENT_NAME", cls.agent_name),
            display_name=os.environ.get("SAM_DISPLAY_NAME", cls.display_name),
            llm_base_url=os.environ.get("CLAIMS_LLM_BASE_URL") or os.environ.get("LLM_SERVICE_ENDPOINT", ""),
            llm_api_key=os.environ.get("LLM_SERVICE_API_KEY", ""),
            llm_model=os.environ.get("CLAIMS_LLM_MODEL") or os.environ.get("LLM_SERVICE_GENERAL_MODEL_NAME", ""),
            pricing_key=os.environ.get("CLAIMS_PRICING_KEY", "").strip(),
            use_peers=bool(peers),
            peer_agents=None if peers == "*" else tuple(_names(peers)),
            approval_tools=tuple(_names(os.environ.get("CLAIMS_APPROVAL_TOOLS", ""))),
            native_approval=os.environ.get("CLAIMS_NATIVE_APPROVAL") == "true",
            ask_user=os.environ.get("CLAIMS_ASK_USER") == "true",
            route_scope=os.environ.get("CLAIMS_ROUTE_SCOPE", "").strip(),
            undeclared_tool=os.environ.get("CLAIMS_UNDECLARED_TOOL", "").strip(),
            llm_context_window=int(os.environ["CLAIMS_LLM_CONTEXT_WINDOW"])
            if os.environ.get("CLAIMS_LLM_CONTEXT_WINDOW")
            else None,
            max_llm_calls=int(os.environ["CLAIMS_MAX_LLM_CALLS"]) if os.environ.get("CLAIMS_MAX_LLM_CALLS") else None,
            max_turns=int(os.environ["CLAIMS_MAX_TURNS"]) if os.environ.get("CLAIMS_MAX_TURNS") else None,
            blocked_word=os.environ.get("CLAIMS_BLOCKED_WORD", "").strip(),
            blocked_output_word=os.environ.get("CLAIMS_BLOCKED_OUTPUT_WORD", "").strip(),
            agent_tool=os.environ.get("CLAIMS_AGENT_TOOL") == "true",
            stream_thinking=os.environ.get("CLAIMS_STREAM_THINKING") == "true",
            request_context=os.environ.get("CLAIMS_REQUEST_CONTEXT") == "true",
            handle_tool_errors=os.environ.get("CLAIMS_HANDLE_TOOL_ERRORS") == "true",
            crew_reply=os.environ.get("CLAIMS_CREW_REPLY") == "true",
            keepalive_seconds=_seconds("CLAIMS_KEEPALIVE_SECONDS"),
            task_timeout_seconds=_seconds("CLAIMS_TASK_TIMEOUT_SECONDS"),
            shutdown_grace_seconds=_seconds("CLAIMS_SHUTDOWN_GRACE_SECONDS"),
            stream_text=os.environ.get("CLAIMS_STREAM_TEXT") != "false",
        )


def agent_config(settings: Settings, tools: list[ToolInfo], framework: str) -> AgentConfig:
    return AgentConfig(
        name=settings.agent_name,
        description="Triages incoming insurance claims",
        display_name=settings.display_name,
        framework=framework,
        skills=[Skill(id="triage", name="Triage a claim", description="Routes a claim to the right queue")],
        tools=[t for t in tools if t.name != settings.undeclared_tool],
        peers=list(settings.peer_agents or []),
        welcome_message="Send me a claim or policy id and I will route it to the right queue.",
        suggestions=[Suggestion("Triage P-1182", "Triage claim P-1182", auto_send=True)],
        **({"max_llm_calls": settings.max_llm_calls} if settings.max_llm_calls is not None else {}),
        **({"stream_thinking": True} if settings.stream_thinking else {}),
        **({"spend_model_keys": {settings.llm_model: settings.pricing_key}} if settings.pricing_key else {}),
        **{
            name: value
            for name, value in (
                ("keepalive_seconds", settings.keepalive_seconds),
                ("task_timeout_seconds", settings.task_timeout_seconds),
                ("shutdown_grace_seconds", settings.shutdown_grace_seconds),
            )
            if value is not None
        },
    )
