# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Every adapter, and the generic OpenTelemetry one, driven through the same scripted scenarios with fake models.

Each scenario is a list of model turns; each adapter's driver feeds those turns to its framework through the fake the
adapter's own tests use, runs the real framework (or, where noted, its event or hook boundary) as the handler of a task
a real MeshAgent runs (tests/adapter_task.py), and the step events the task sends are normalized: call ids become c1,
c2... in order of first appearance, the tool
events of one model turn are put in call order, and a tool result keeps only its outcome and whether it carries the
tool's output. The outcome (normalized steps, answer or exception, token totals, and for the permission scenario what
the model was offered, what ran, the refused call's result and what was audited) must equal the common expectation,
unless DIFFERENCES encodes that adapter's legitimate difference, with a reason. A change in any adapter or framework
shows as a diff here.

The permission scenario has the model call a tool the user lacks the scopes for, whether or not it was offered (a
model may call a tool it was not offered: from earlier turns, or by mistake). Every adapter that runs a framework
refuses that call the same way: the tool body never runs, the call's steps are its start and the failed result
``{"error": "Access denied: ..."}``, and the audit trail has ``tool access denied`` then ``tool execution failed``.
The nested cases at the end check the same refusal where a framework nests agents (a handoff, a sub-agent, a subgraph,
a crew with two agents).
"""

from __future__ import annotations

import asyncio
import contextvars
import json
import logging
import tempfile
import threading
from collections.abc import Callable
from dataclasses import dataclass, field
from types import SimpleNamespace
from typing import Any

import pytest

from sam_link_sdk.context import TaskContext
from sam_link_sdk.events import ModelFinished, ModelStarted, StepEvent, ToolFinished, ToolStarted

from .adapter_task import adapter_task, steps
from .agent_rig import settle, start_agent

MODEL = "fake-model"
DENIED_SCOPES = {"route_claim": ["tool:claims:route"]}
DENIED_SCENARIO_UNSCOPED = [
    "lookup_policy",
    "ask_router",
    "router",
    "Delegate work to coworker",
    "delegate_work_to_coworker",
    "delegate_task",
]
"""What the card lists without scopes beside route_claim: the tool a framework offers and the tools it runs a nested agent
through. A name the card leaves out is refused once the card declares a scoped tool.
"""
REFUSAL = {"error": "Access denied: you do not have the scopes required to use route_claim."}


@dataclass(frozen=True)
class Turn:
    text: str
    calls: tuple = ()
    usage: tuple = (0, 0)


SCENARIOS: dict[str, list[Turn]] = {
    "plain_answer": [Turn("Claim P-1 goes to fast-track.", (), (10, 5))],
    "one_tool": [
        Turn("Looking it up.", (("call_a", "lookup_policy", {"policy_id": "P-1"}),), (100, 10)),
        Turn("P-1 is gold.", (), (120, 8)),
    ],
    "parallel_tools": [
        Turn(
            "",
            (("call_a", "lookup_policy", {"policy_id": "P-1"}), ("call_b", "lookup_policy", {"policy_id": "P-2"})),
            (100, 10),
        ),
        Turn("Both are gold.", (), (130, 8)),
    ],
    "failed_tool": [
        Turn("", (("call_x", "broken", {"policy_id": "P-9"}),), (100, 10)),
        Turn("The backend is down.", (), (110, 6)),
    ],
    "token_usage": [
        Turn("", (("call_a", "lookup_policy", {"policy_id": "P-1"}),), (100, 10)),
        Turn("", (("call_b", "lookup_policy", {"policy_id": "P-2"}),), (200, 20)),
        Turn("Both are gold.", (), (300, 30)),
    ],
    "denied_tool": [
        Turn("", (("call_r", "route_claim", {"claim_id": "C-1"}),), (10, 2)),
        Turn("I may not route claims.", (), (12, 3)),
    ],
    "cancel": [
        Turn("", (("call_s", "slow_screen", {"policy_id": "P-1"}),), (10, 2)),
        Turn("never reached", (), (12, 3)),
    ],
    "error_shaped_result": [
        Turn("", (("call_e", "find_policy", {"policy_id": "P-9"}),), (10, 2)),
        Turn("No such policy.", (), (12, 3)),
    ],
}
"""A tool that answers {"error": ..., "code": ...} without failing: one rule everywhere (_adapter.reported_result)."""

MC = ("model_call", MODEL)


def answer(text, calls=(), usage=(0, 0)):
    return ("model_answer", text, tuple(calls), usage)


def call(alias, tool, args):
    return (alias, tool, args)


def tool_steps(alias, tool, args, outcome):
    marker = outcome == "ok"
    return [("tool_start", alias, tool, args), ("tool_end", alias, tool, outcome, marker)]


P1, P2 = {"policy_id": "P-1"}, {"policy_id": "P-2"}

EXPECTED: dict[str, dict] = {
    "plain_answer": {
        "steps": [MC, answer("Claim P-1 goes to fast-track.", (), (10, 5))],
        "answer": "Claim P-1 goes to fast-track.",
        "usage": (10, 5, 1),
    },
    "one_tool": {
        "steps": [
            MC,
            answer("Looking it up.", [call("c1", "lookup_policy", P1)], (100, 10)),
            *tool_steps("c1", "lookup_policy", P1, "ok"),
            MC,
            answer("P-1 is gold.", (), (120, 8)),
        ],
        "answer": "P-1 is gold.",
        "usage": (220, 18, 2),
    },
    "parallel_tools": {
        "steps": [
            MC,
            answer("", [call("c1", "lookup_policy", P1), call("c2", "lookup_policy", P2)], (100, 10)),
            *tool_steps("c1", "lookup_policy", P1, "ok"),
            *tool_steps("c2", "lookup_policy", P2, "ok"),
            MC,
            answer("Both are gold.", (), (130, 8)),
        ],
        "answer": "Both are gold.",
        "usage": (230, 18, 2),
    },
    "failed_tool": {
        "steps": [
            MC,
            answer("", [call("c1", "broken", {"policy_id": "P-9"})], (100, 10)),
            *tool_steps("c1", "broken", {"policy_id": "P-9"}, "error"),
            MC,
            answer("The backend is down.", (), (110, 6)),
        ],
        "answer": "The backend is down.",
        "usage": (210, 16, 2),
    },
    "token_usage": {
        "steps": [
            MC,
            answer("", [call("c1", "lookup_policy", P1)], (100, 10)),
            *tool_steps("c1", "lookup_policy", P1, "ok"),
            MC,
            answer("", [call("c2", "lookup_policy", P2)], (200, 20)),
            *tool_steps("c2", "lookup_policy", P2, "ok"),
            MC,
            answer("Both are gold.", (), (300, 30)),
        ],
        "answer": "Both are gold.",
        "usage": (600, 60, 3),
    },
    "denied_tool": {
        "steps": [
            MC,
            answer("", [call("c1", "route_claim", {"claim_id": "C-1"})], (10, 2)),
            *tool_steps("c1", "route_claim", {"claim_id": "C-1"}, "error"),
            MC,
            answer("I may not route claims.", (), (12, 3)),
        ],
        "answer": "I may not route claims.",
        "usage": (22, 5, 2),
        "offered_denied_tool": False,
        "ran": [],
        "refusal": [REFUSAL],
        "audit": [("tool access denied", "route_claim"), ("tool execution failed", "route_claim")],
    },
    "cancel": {
        "steps": [MC, answer("", [call("c1", "slow_screen", P1)], (10, 2)), ("tool_start", "c1", "slow_screen", P1)],
        "raises": "CancelledError",
        "tool_stopped": True,
        "model_calls_after_cancel": 0,
    },
    "error_shaped_result": {
        "steps": [
            MC,
            answer("", [call("c1", "find_policy", {"policy_id": "P-9"})], (10, 2)),
            *tool_steps("c1", "find_policy", {"policy_id": "P-9"}, "ok"),
            MC,
            answer("No such policy.", (), (12, 3)),
        ],
        "answer": "No such policy.",
        "usage": (22, 5, 2),
    },
}


@dataclass(frozen=True)
class Differs:
    """An adapter's legitimate difference from the common expectation: the fields it changes, and why."""

    reason: str
    changes: dict = field(default_factory=dict)


@dataclass(frozen=True)
class NotApplicable:
    reason: str


DIFFERENCES: dict[tuple[str, str], Differs | NotApplicable] = {
    ("adk", "failed_tool"): Differs(
        "the plugin reports the failed tool, then ADK raises the tool's exception out of the run (no second model "
        "call); the SDK answers the task with a JSON-RPC error",
        {
            "steps": EXPECTED["failed_tool"]["steps"][:4],
            "answer": None,
            "raises": "RuntimeError",
            "usage": (100, 10, 1),
        },
    ),
    ("langgraph", "failed_tool"): Differs(
        "LangGraph's prebuilt ToolNode re-raises a tool's exception unless it is built with handle_tool_errors; the "
        "callback reports the failed step first, then the run ends with the error",
        {
            "steps": EXPECTED["failed_tool"]["steps"][:4],
            "answer": None,
            "raises": "RuntimeError",
            "usage": (100, 10, 1),
        },
    ),
    ("langgraph", "denied_tool"): Differs(
        "the graph is built by the caller, so the model is offered the tool (building it with ctx.permitted_tools "
        "hides it); the step reporter refuses the call by raising ToolAccessDenied, which the prebuilt ToolNode, as "
        "for any failing tool, re-raises unless built with handle_tool_errors: the refusal is reported, then the run "
        "ends with it",
        {
            "steps": EXPECTED["denied_tool"]["steps"][:4],
            "offered_denied_tool": True,
            "answer": None,
            "raises": "ToolAccessDenied",
            "usage": (10, 2, 1),
        },
    ),
    ("crewai", "one_tool"): Differs(
        "CrewAI's completion event carries the model's tool calls in place of its text, so text sent with tool calls "
        "is not seen",
        {
            "steps": [
                MC,
                answer("", [call("c1", "lookup_policy", P1)], (100, 10)),
                *tool_steps("c1", "lookup_policy", P1, "ok"),
                MC,
                answer("P-1 is gold.", (), (120, 8)),
            ]
        },
    ),
    ("crewai", "cancel"): Differs(
        "CrewAI runs the crew in a worker thread asyncio cannot interrupt: the mesh task stops at once and publishes "
        "nothing more, but the tool runs on (the crew stops at its next model call, see test_kill_switch_frameworks)",
        {"tool_stopped": False},
    ),
    ("strands", "denied_tool"): Differs(
        "Strands has no public way to take a tool off a built agent, so it is offered unless build(ctx) filters with "
        "ctx.permitted_tools; the before-tool hook refuses the call as every adapter does",
        {"offered_denied_tool": True},
    ),
    ("hermes", "cancel"): NotApplicable(
        "cancelling interrupts the AIAgent (hard_interrupt), which needs Hermes itself; test_hermes covers it when "
        "Hermes is installed"
    ),
    ("otel", "denied_tool"): Differs(
        "the one documented exception: spans only record what happened, so the generic adapter cannot hide or refuse "
        "a tool; it runs, and run_agent logs a warning that the agent's tool scopes are not enforced (check them in "
        "the tools with ctx.check_tool)",
        {
            "steps": [
                MC,
                answer("", [call("c1", "route_claim", {"claim_id": "C-1"})], (10, 2)),
                *tool_steps("c1", "route_claim", {"claim_id": "C-1"}, "ok"),
                MC,
                answer("I may not route claims.", (), (12, 3)),
            ],
            "offered_denied_tool": True,
            "ran": ["route_claim"],
            "refusal": ["fast-track"],
            "audit": [("tool execution succeeded", "route_claim")],
        },
    ),
    ("otel", "cancel"): Differs(
        "a tool is reported when its span ends, and the cancelled task publishes nothing after the cancel, so the "
        "running tool never shows",
        {"steps": EXPECTED["cancel"]["steps"][:2]},
    ),
    ("otel_strands", "denied_tool"): Differs(
        "as otel: Strands' spans cannot refuse the tool",
        {
            "steps": [
                MC,
                answer("", [call("c1", "route_claim", {"claim_id": "C-1"})], (10, 2)),
                *tool_steps("c1", "route_claim", {"claim_id": "C-1"}, "ok"),
                MC,
                answer("I may not route claims.", (), (12, 3)),
            ],
            "offered_denied_tool": True,
            "ran": ["route_claim"],
            "refusal": ["fast-track"],
            "audit": [("tool execution succeeded", "route_claim")],
        },
    ),
    ("otel_strands", "cancel"): Differs(
        "as otel: the tool's span has not ended when the task is cancelled", {"steps": EXPECTED["cancel"]["steps"][:2]}
    ),
}


def _failed(data: Any) -> bool:
    return isinstance(data, dict) and set(data) == {"error"}


def _carries_output(tool: str, data: Any) -> bool:
    text = json.dumps(data, default=str)
    return {
        "lookup_policy": "gold",
        "route_claim": "fast-track",
        "slow_screen": "low risk",
        "find_policy": "not_found",
    }.get(tool, "\x00") in text


def _as_data(value: Any) -> Any:
    return json.loads(json.dumps(value, default=str))


def normalize(events: list[StepEvent]) -> list[tuple]:
    aliases: dict[str, str] = {}

    def alias(call_id):
        return aliases.setdefault(call_id, f"c{len(aliases) + 1}")

    normalized: list[tuple] = []
    for event in events:
        if isinstance(event, ModelStarted):
            normalized.append(("model_call", event.model))
        elif isinstance(event, ModelFinished):
            calls = tuple((alias(c.id), c.name, _as_data(dict(c.args))) for c in event.tool_calls)
            usage = (event.usage.input_tokens, event.usage.output_tokens) if event.usage is not None else (0, 0)
            normalized.append(("model_answer", event.text, calls, usage))
        elif isinstance(event, ToolStarted):
            args = _as_data(dict(event.args)) if event.args is not None else None
            normalized.append(("tool_start", alias(event.call_id), event.name, args))
        elif isinstance(event, ToolFinished):
            data = _as_data(event.result)
            outcome = "error" if _failed(data) else "ok"
            normalized.append(
                (
                    "tool_end",
                    alias(event.call_id),
                    event.name,
                    outcome,
                    outcome == "ok" and _carries_output(event.name, data),
                )
            )
        else:
            raise AssertionError(f"a step the conformance scenarios do not expect: {event!r}")
    return _tool_steps_in_call_order(normalized)


def _tool_steps_in_call_order(steps: list[tuple]) -> list[tuple]:
    """Within the tool events of one model turn, each call's start then end, calls in alias order. Checks first that
    every call's start came before its end."""
    out, block = [], []

    def flush():
        seen = set()
        for step in block:
            if step[0] == "tool_start":
                seen.add(step[1])
            else:
                assert step[1] in seen or not any(s[0] == "tool_start" and s[1] == step[1] for s in block), (
                    f"result before start for {step[1]}"
                )
        out.extend(sorted(block, key=lambda s: (int(s[1][1:]), s[0] == "tool_end")))
        block.clear()

    for step in steps:
        if step[0] in ("tool_start", "tool_end"):
            block.append(step)
        else:
            flush()
            out.append(step)
    flush()
    return out


class Tools:
    """The scenario tools as plain functions, recording what ran. Each driver wraps them in its framework's tool type."""

    def __init__(self) -> None:
        self.ran: list[str] = []
        self.slow_started = asyncio.Event()
        self.slow_stopped: list[bool] = []
        loop = asyncio.get_running_loop()
        tools = self

        def lookup_policy(policy_id: str) -> dict:
            """Look up an insurance policy by id."""
            tools.ran.append("lookup_policy")
            return {"policy_id": policy_id, "tier": "gold"}

        def broken(policy_id: str) -> dict:
            """Look up a policy in the system that is down."""
            tools.ran.append("broken")
            raise RuntimeError("backend down")

        def route_claim(claim_id: str) -> str:
            """Route a claim to a work queue."""
            tools.ran.append("route_claim")
            return "fast-track"

        async def slow_screen(policy_id: str) -> str:
            """Run the slow fraud screen for a policy."""
            tools.ran.append("slow_screen")
            loop.call_soon_threadsafe(tools.slow_started.set)
            try:
                await asyncio.sleep(30)
            finally:
                tools.slow_stopped.append(True)
            return "low risk"

        def find_policy(policy_id: str) -> dict:
            """Find a policy by id."""
            tools.ran.append("find_policy")
            return {"error": f"no policy {policy_id}", "code": "not_found"}

        self.by_name: dict[str, Callable] = {
            f.__name__: f for f in (lookup_policy, broken, route_claim, slow_screen, find_policy)
        }

    def for_scenario(self, scenario: str) -> list[Callable]:
        names = {
            "failed_tool": ["broken"],
            "denied_tool": ["lookup_policy", "route_claim"],
            "cancel": ["slow_screen"],
            "error_shaped_result": ["find_policy"],
        }
        return [self.by_name[n] for n in names.get(scenario, ["lookup_policy"])]


def scenario_task(scenario: str, records: list):
    """The task a scenario runs in. In the permission scenario its user is alice, whom SAM granted none of
    route_claim's scopes; the agent's audit records go to `records`."""
    if scenario == "denied_tool":
        return adapter_task(
            "T",
            "Triage claims P-1 and P-2",
            scopes=DENIED_SCOPES,
            declared=DENIED_SCENARIO_UNSCOPED,
            granted=[],
            audit=records.append,
            user_id="alice",
        )
    return adapter_task("T", "Triage claims P-1 and P-2")


class Driver:
    """Runs one scenario's turns through one adapter. `offered` lists the tool names of each model request, where the
    fake model can see them; `model_calls` counts the turns the fake model answered."""

    def __init__(self, turns: list[Turn], tools: Tools, scenario: str):
        self.turns, self.tools, self.scenario = list(turns), tools, scenario
        self.offered: list[list[str]] = []
        self.model_calls = 0

    async def run(self, ctx: TaskContext) -> str:
        raise NotImplementedError

    def framework_tools(self) -> list:
        """The scenario's tools as the framework's own tool objects; in the permission scenario, route_claim declared
        with its scopes the one way an adapter offers (``with_scopes``), so its own guard and the hooks both apply."""
        tools = self.raw_tools()
        return self.with_scopes(tools, DENIED_SCOPES) if self.scenario == "denied_tool" else tools

    def raw_tools(self) -> list:
        raise NotImplementedError

    def with_scopes(self, tools: list, scopes: dict) -> list:
        """The adapter's ``with_scopes``."""
        raise NotImplementedError

    def tool_infos(self, scopes: dict) -> list:
        """The card tool manifest the adapter's ``tool_infos`` builds from the framework's tools given `scopes`."""
        return self.adapter().tool_infos(self.with_scopes(self.raw_tools(), scopes))

    def adapter(self):
        raise NotImplementedError

    def cleanup(self) -> None:
        """Let go of anything the framework still runs after the scenario (a worker thread asyncio cannot cancel)."""


class LangChainDriver(Driver):
    async def run(self, ctx):
        from sam_link_sdk.langchain import run_agent

        return await run_agent(ctx, self.model(), self.framework_tools())

    def raw_tools(self):
        from langchain_core.tools import tool

        return [tool(f) for f in self.tools.for_scenario(self.scenario)]

    def adapter(self):
        import sam_link_sdk.langchain

        return sam_link_sdk.langchain

    def with_scopes(self, tools, scopes):
        return self.adapter().with_scopes(tools, scopes)

    def model(self):
        from langchain_core.messages import AIMessage

        from .test_callbacks import ToolCallingFake

        driver = self

        def messages():
            for turn in driver.turns:
                driver.model_calls += 1
                i, o = turn.usage
                yield AIMessage(
                    content=turn.text,
                    tool_calls=[{"id": c, "name": n, "args": a} for c, n, a in turn.calls],
                    usage_metadata={"input_tokens": i, "output_tokens": o, "total_tokens": i + o},
                )

        class Recording(ToolCallingFake):
            def bind_tools(self, tools, **kwargs):
                driver.offered.append([t.name for t in tools])
                return self

            def _get_ls_params(self, stop=None, **kwargs):
                return {**super()._get_ls_params(stop=stop, **kwargs), "ls_model_name": MODEL}

        return Recording(model_name=MODEL, messages=messages())


class LangGraphDriver(LangChainDriver):
    async def run(self, ctx):
        pytest.importorskip("langgraph")
        from langchain_core.messages import HumanMessage
        from langgraph.prebuilt import create_react_agent

        from sam_link_sdk.context import bind_task
        from sam_link_sdk.langchain import step_reporter

        graph = create_react_agent(self.model(), self.framework_tools())
        with bind_task(ctx):
            result = await graph.ainvoke(
                {"messages": [HumanMessage(ctx.text)]}, config={"callbacks": [step_reporter(ctx)]}
            )
        return result["messages"][-1].text


class OpenAIAgentsDriver(Driver):
    async def run(self, ctx):
        pytest.importorskip("agents")
        from agents import Agent, ModelResponse

        from sam_link_sdk.openai_agents import run_agent

        from .test_openai_agents import ScriptedModel, call as fn_call, message, usage

        driver = self

        class Recording(ScriptedModel):
            async def get_response(self, system_instructions, input, model_settings, tools, *args, **kwargs):
                driver.offered.append([t.name for t in tools])
                driver.model_calls += 1
                return await super().get_response()

        turns = [
            ([*([message(t.text)] if t.text else []), *(fn_call(c, n, a) for c, n, a in t.calls)], usage(*t.usage))
            for t in self.turns
        ]
        model = Recording(turns)
        model.model = MODEL
        assert ModelResponse
        agent = Agent(name="claims-triage", instructions="x", model=model, tools=self.framework_tools())
        return await run_agent(ctx, agent)

    def raw_tools(self):
        from agents import function_tool

        return [function_tool(f) for f in self.tools.for_scenario(self.scenario)]

    def adapter(self):
        import sam_link_sdk.openai_agents

        return sam_link_sdk.openai_agents

    def with_scopes(self, tools, scopes):
        return self.adapter().with_scopes(tools, scopes)


class AdkDriver(Driver):
    async def run(self, ctx):
        pytest.importorskip("google.adk")
        from google.adk.agents import LlmAgent
        from google.adk.runners import InMemoryRunner
        from google.genai import types

        from sam_link_sdk.adk import run_agent, step_reporter

        from .test_adk import Scripted, fc, usage

        driver = self

        class Recording(Scripted):
            async def generate_content_async(self, llm_request, stream=False):
                driver.offered.append(
                    sorted(d.name for t in llm_request.config.tools or [] for d in t.function_declarations or [])
                )
                driver.model_calls += 1
                async for response in super().generate_content_async(llm_request, stream):
                    yield response

        turns = [
            (
                [*([types.Part(text=t.text)] if t.text else []), *(fc(n, a, call_id=c) for c, n, a in t.calls)],
                usage(*t.usage),
            )
            for t in self.turns
        ]
        agent = LlmAgent(
            name="claims_triage",
            model=Recording(model=MODEL, turns=turns),
            instruction="Triage claims.",
            tools=self.framework_tools(),
        )
        return await run_agent(ctx, InMemoryRunner(agent=agent, app_name="claims", plugins=[step_reporter()]))

    def raw_tools(self):
        return self.tools.for_scenario(self.scenario)

    def adapter(self):
        import sam_link_sdk.adk

        return sam_link_sdk.adk

    def with_scopes(self, tools, scopes):
        return self.adapter().with_scopes(tools, scopes)


class CrewAIDriver(Driver):
    """CrewAI is driven at its event bus and its hooks: the fake crew does, from its kickoff thread, what a crew with
    native tool calling does (as test_crewai does): it calls its agent's LLM with the tools it offers, emits CrewAI's
    events, and runs CrewAI's before- and after-tool-call hooks around each tool, which it runs only if no hook
    blocked it."""

    async def run(self, ctx):
        pytest.importorskip("crewai")
        from crewai.events.types.llm_events import LLMCallType
        from crewai.events.types.tool_usage_events import ToolUsageErrorEvent
        from crewai.hooks import ToolCallHookContext
        from crewai.hooks.tool_hooks import run_after_tool_call_hooks, run_before_tool_call_hooks

        from sam_link_sdk.crewai import run_agent

        from .test_crewai import FakeCrew, llm_end, llm_start, tool_end, tool_start

        driver, tools = self, self.tools.by_name
        self.release = threading.Event()
        offered_specs = [
            {"type": "function", "function": {"name": f.__name__}} for f in self.tools.for_scenario(self.scenario)
        ]

        class Llm:
            def call(self, messages, tools=None, **kwargs):
                driver.offered.append([t["function"]["name"] for t in tools or []])
                return ""

        crew_agent = SimpleNamespace(llm=Llm(), function_calling_llm=None)

        def run_tool(name, args):
            """CrewAI cannot interrupt a tool: it runs until the test lets it finish."""
            if name != "slow_screen":
                return tools[name](**args)
            tools_log = driver.tools
            tools_log.ran.append(name)
            loop.call_soon_threadsafe(tools_log.slow_started.set)
            driver.release.wait(5)
            return "low risk"

        loop = asyncio.get_running_loop()

        def script():
            for n, turn in enumerate(driver.turns):
                driver.model_calls += 1
                crew_agent.llm.call([], tools=offered_specs)
                i, o = turn.usage
                yield llm_start(f"L{n}", model=MODEL)
                response = (
                    [SimpleNamespace(id=c, name=name, input=a) for c, name, a in turn.calls]
                    if turn.calls
                    else turn.text
                )
                yield llm_end(
                    f"L{n}",
                    response,
                    LLMCallType.TOOL_CALL if turn.calls else LLMCallType.LLM_CALL,
                    {"input_tokens": i, "output_tokens": o},
                    model=MODEL,
                )
                for _, name, args in turn.calls:
                    yield tool_start(name, args)
                    hook = ToolCallHookContext(tool_name=name, tool_input=dict(args), tool=None)
                    if run_before_tool_call_hooks(hook):
                        blocked = ToolCallHookContext(
                            tool_name=name,
                            tool_input=dict(args),
                            tool=None,
                            tool_result=f"Tool execution blocked by hook. Tool: {name}",
                        )
                        yield tool_end(name, args, run_after_tool_call_hooks(blocked))
                        continue
                    try:
                        result = run_tool(name, args)
                    except Exception as exc:
                        yield ToolUsageErrorEvent(tool_name=name, tool_args=args, error=str(exc))
                    else:
                        yield tool_end(name, args, json.dumps(result))
                if not turn.calls:
                    return

        class Crew(FakeCrew):
            agents = [crew_agent]

            def kickoff(self, inputs=None):
                super().kickoff(inputs)
                return SimpleNamespace(raw=driver.final_text)

        self.final_text = next((t.text for t in self.turns if not t.calls), "")
        return await run_agent(ctx, lambda _: Crew(script, self.final_text))

    def raw_tools(self):
        from crewai.tools import tool as crew_tool

        return [crew_tool(f.__name__)(f) for f in self.tools.for_scenario(self.scenario)]

    def adapter(self):
        import sam_link_sdk.crewai

        return sam_link_sdk.crewai

    def with_scopes(self, tools, scopes):
        return self.adapter().with_scopes(tools, scopes)

    def cleanup(self) -> None:
        if getattr(self, "release", None) is not None:
            self.release.set()


class StrandsDriver(Driver):
    def agent(self, model):
        from strands import Agent

        return Agent(model=model, tools=self.framework_tools(), callback_handler=None)

    def raw_tools(self):
        from strands import tool

        return [tool(f) for f in self.tools.for_scenario(self.scenario)]

    def adapter(self):
        import sam_link_sdk.strands

        return sam_link_sdk.strands

    def with_scopes(self, tools, scopes):
        return self.adapter().with_scopes(tools, scopes)

    def model(self):
        from .test_strands import ScriptedModel, call as use, text, usage

        driver = self

        class Recording(ScriptedModel):
            def __init__(self, turns):
                super().__init__(turns, model_id=MODEL)
                self.config = {"model_id": MODEL}

            async def stream(self, messages, tool_specs=None, system_prompt=None, **kwargs):
                driver.offered.append([s["name"] for s in tool_specs or []])
                driver.model_calls += 1
                async for event in super().stream(messages, tool_specs, system_prompt, **kwargs):
                    yield event

        return Recording(
            [
                ([*([text(t.text)] if t.text else []), *(use(c, n, a) for c, n, a in t.calls)], usage(*t.usage))
                for t in self.turns
            ]
        )

    async def run(self, ctx):
        pytest.importorskip("strands")
        from sam_link_sdk.strands import run_agent

        return await run_agent(ctx, lambda c: self.agent(self.model()))


class AgentFrameworkDriver(Driver):
    async def run(self, ctx):
        pytest.importorskip("agent_framework")
        from agent_framework import Agent

        from sam_link_sdk.agent_framework import run_agent

        from .test_agent_framework import ScriptedClient, call as fn_call, text, usage

        driver = self

        class Recording(ScriptedClient):
            def _inner_get_response(self, *, messages, stream, options, **kwargs):
                driver.offered.append([getattr(t, "name", t) for t in (options or {}).get("tools") or []])
                driver.model_calls += 1
                return super()._inner_get_response(messages=messages, stream=stream, options=options, **kwargs)

        client = Recording(
            [
                ([*([text(t.text)] if t.text else []), *(fn_call(c, n, a) for c, n, a in t.calls)], usage(*t.usage))
                for t in self.turns
            ],
            model=MODEL,
        )
        return await run_agent(ctx, Agent(client, "You triage claims.", name="triage", tools=self.framework_tools()))

    def raw_tools(self):
        from agent_framework import tool

        return [tool(approval_mode="never_require")(f) for f in self.tools.for_scenario(self.scenario)]

    def adapter(self):
        import sam_link_sdk.agent_framework

        return sam_link_sdk.agent_framework

    def with_scopes(self, tools, scopes):
        return self.adapter().with_scopes(tools, scopes)


class HermesDriver(Driver):
    """Hermes is not installed here, so this drives sam_link_sdk.hermes at its hook boundary: a fake turn calls the hooks
    from a worker thread, in the order and with the keyword payloads Hermes uses (as test_hermes checks against the
    real AIAgent when Hermes is installed), after run_agent's own preparation of the agent's tool list."""

    async def run(self, ctx):
        from sam_link_sdk.hermes import run as hermes_run, steps as hermes_steps

        driver, tools = self, self.tools.by_name
        schemas = self.framework_tools()
        agent = SimpleNamespace(tools=schemas, valid_tool_names={s["function"]["name"] for s in schemas})
        run = hermes_steps.Run(ctx, asyncio.get_running_loop(), "sam-T")
        hermes_run._hide_denied_tools(run, agent)
        self.offered.append(sorted(agent.valid_tool_names))

        def turn() -> str:
            kw = {"session_id": "sam-T"}
            for t in driver.turns:
                driver.model_calls += 1
                hermes_steps.pre_api_request(**kw, model=MODEL)
                message = SimpleNamespace(
                    content=t.text,
                    tool_calls=[SimpleNamespace(id=c, name=n, arguments=json.dumps(a)) for c, n, a in t.calls],
                )
                hermes_steps.post_api_request(
                    **kw,
                    model=MODEL,
                    assistant_message=message,
                    usage={"input_tokens": t.usage[0], "output_tokens": t.usage[1]},
                )
                if not t.calls:
                    return t.text
                for c, name, args in t.calls:
                    if hermes_steps.pre_tool_call(**kw, tool_name=name, args=args, tool_call_id=c) is not None:
                        continue
                    try:
                        result = json.dumps(tools[name](**args))
                    except Exception as exc:
                        result = json.dumps({"error": str(exc)})
                    hermes_steps.post_tool_call(**kw, tool_name=name, tool_call_id=c, result=result)
            return ""

        hermes_steps.register_run(run)
        try:
            return await asyncio.to_thread(contextvars.copy_context().run, turn)
        finally:
            hermes_steps.unregister_run(run)

    def raw_tools(self):
        return [
            {"type": "function", "function": {"name": f.__name__, "description": f.__doc__}}
            for f in self.tools.for_scenario(self.scenario)
        ]

    def with_scopes(self, tools, scopes):
        return tools

    def tool_infos(self, scopes):
        from sam_link_sdk.hermes import tool_infos

        return tool_infos(self.raw_tools(), scopes=scopes)


class OtelDriver(Driver):
    """The generic adapter over scripted GenAI spans (tests/otel_scripted.py), with no framework adapter at all."""

    async def run(self, ctx):
        pytest.importorskip("opentelemetry.sdk")
        from opentelemetry.sdk.trace import TracerProvider

        from sam_link_sdk.otel_steps import run_agent, step_reporter

        from .otel_scripted import ScriptedSpans

        provider = TracerProvider()
        step_reporter(provider)
        driver = self

        class Counting(ScriptedSpans):
            def _chat(self, *args):
                driver.model_calls += 1
                driver.offered.append(sorted(self.tools))
                super()._chat(*args)

        turns = [(t.text, list(t.calls), t.usage) for t in self.turns]
        agent = Counting(provider, turns, {f.__name__: f for f in self.tools.for_scenario(self.scenario)}, model=MODEL)
        return await run_agent(ctx, agent.run)


class OtelStrandsDriver(StrandsDriver):
    """A Strands agent with NO dedicated adapter: only Strands' own OpenTelemetry spans, read by the generic adapter.
    Its tools are Strands' plain ones: what the spans alone can do (test_scope_guard covers a scoped Strands tool
    under this adapter)."""

    def framework_tools(self):
        return self.raw_tools()

    async def run(self, ctx):
        tracer_module = pytest.importorskip("strands.telemetry.tracer")
        from opentelemetry.sdk.trace import TracerProvider

        from sam_link_sdk.otel_steps import run_agent, step_reporter

        provider = TracerProvider()
        step_reporter(provider)
        tracer = tracer_module.Tracer()
        tracer.tracer_provider, tracer.tracer = provider, provider.get_tracer("strands.telemetry.tracer")
        previous = tracer_module._tracer_instance
        tracer_module._tracer_instance = tracer
        try:
            agent = self.agent(self.model())
            result = await run_agent(ctx, agent.invoke_async)
        finally:
            tracer_module._tracer_instance = previous
        return str(result).strip()


DRIVERS: dict[str, type[Driver]] = {
    "langchain": LangChainDriver,
    "langgraph": LangGraphDriver,
    "openai_agents": OpenAIAgentsDriver,
    "adk": AdkDriver,
    "crewai": CrewAIDriver,
    "strands": StrandsDriver,
    "agent_framework": AgentFrameworkDriver,
    "hermes": HermesDriver,
    "otel": OtelDriver,
    "otel_strands": OtelStrandsDriver,
}


async def observe(adapter: str, scenario: str) -> dict:
    """CrewAI reports from its thread-pool event bus, so the steps before the cancel can arrive well after the tool started
    on a slow machine.
    """
    records: list = []
    tools = Tools()
    driver = DRIVERS[adapter](SCENARIOS[scenario], tools, scenario)
    outcome: dict[str, Any] = {}
    async with scenario_task(scenario, records) as task:
        ctx = task.ctx
        if scenario == "cancel":
            run = asyncio.ensure_future(task.run(driver.run))
            await asyncio.wait_for(tools.slow_started.wait(), 5)
            before_cancel = len(expected(adapter, scenario)["steps"])
            await task.transport.until(lambda: len(task.events) >= before_cancel, "the steps before the cancel")
            await asyncio.sleep(0.2)
            calls_at_cancel = driver.model_calls
            await task.cancel()
            try:
                await asyncio.wait_for(run, 5)
                outcome["raises"] = None
            except asyncio.CancelledError:
                outcome["raises"] = "CancelledError"
            await asyncio.sleep(0.1)
            outcome["tool_stopped"] = tools.slow_stopped == [True]
            outcome["model_calls_after_cancel"] = driver.model_calls - calls_at_cancel
        else:
            try:
                outcome["answer"] = await asyncio.wait_for(task.run(driver.run), 20)
                outcome["raises"] = None
            except Exception as exc:
                outcome["answer"], outcome["raises"] = None, type(exc).__name__
            totals = ctx._run.usage.final_token_usage()
            outcome["usage"] = (totals["total_input_tokens"], totals["total_output_tokens"], ctx._run.usage.llm_calls)
        await settle()
        sent = task.events if outcome.get("answer") is None else steps(task, outcome["answer"])
    outcome["steps"] = normalize(sent)
    driver.cleanup()
    if scenario == "denied_tool":
        outcome["offered_denied_tool"] = any("route_claim" in names for names in driver.offered[:1])
        outcome["ran"] = [name for name in tools.ran if name == "route_claim"]
        outcome["refusal"] = [
            _as_data(e.result) for e in sent if isinstance(e, ToolFinished) and e.name == "route_claim"
        ]
        outcome["audit"] = [(r.message, r.fields.get("tool")) for r in records if r.fields.get("tool") == "route_claim"]
    return outcome


def expected(adapter: str, scenario: str) -> dict:
    want = {"raises": None, **EXPECTED[scenario]}
    difference = DIFFERENCES.get((adapter, scenario))
    if isinstance(difference, Differs):
        want.update(difference.changes)
    return want


@pytest.mark.parametrize("scenario", list(SCENARIOS))
@pytest.mark.parametrize("adapter", list(DRIVERS))
async def test_adapter_conformance(adapter, scenario):
    difference = DIFFERENCES.get((adapter, scenario))
    if isinstance(difference, NotApplicable):
        pytest.skip(f"{adapter}/{scenario}: {difference.reason}")
    assert await observe(adapter, scenario) == expected(adapter, scenario)


def test_every_encoded_difference_names_a_real_adapter_and_scenario_and_says_why():
    for (adapter, scenario), difference in DIFFERENCES.items():
        assert adapter in DRIVERS and scenario in SCENARIOS
        assert len(difference.reason) > 20
        if isinstance(difference, Differs):
            assert difference.changes and set(difference.changes) <= {
                "steps",
                "answer",
                "raises",
                "usage",
                "offered_denied_tool",
                "ran",
                "refusal",
                "audit",
                "tool_stopped",
                "model_calls_after_cancel",
            }


def assert_refused_as_everywhere(events: list[StepEvent], records: list, tools: Tools) -> None:
    """The refusal of the permission scenario, wherever the call was made: the tool never ran, the call shows as its
    start and the access-denied result, and the audit trail is the same."""
    assert [n for n in tools.ran if n == "route_claim"] == []
    assert [_as_data(e.args) for e in events if isinstance(e, ToolStarted) and e.name == "route_claim"] == [
        {"claim_id": "C-1"}
    ]
    assert [_as_data(e.result) for e in events if isinstance(e, ToolFinished) and e.name == "route_claim"] == [REFUSAL]
    assert [(r.message, r.fields.get("tool")) for r in records if r.fields.get("tool") == "route_claim"] == EXPECTED[
        "denied_tool"
    ]["audit"]


@pytest.mark.parametrize("via", ["agent", "handoff"])
async def test_openai_agents_refuses_a_denied_tool_of_an_agent_it_hands_off_to(via):
    pytest.importorskip("agents")
    from agents import Agent, function_tool, handoff

    from sam_link_sdk.openai_agents import run_agent

    from .test_openai_agents import ScriptedModel, call as fn_call, message, usage

    tools, records = Tools(), []
    model = ScriptedModel(
        [
            ([fn_call("call_h", "transfer_to_router", {})], usage(1, 1)),
            ([fn_call("call_r", "route_claim", {"claim_id": "C-1"})], usage(1, 1)),
            ([message("I may not route claims.")], usage(1, 1)),
        ]
    )
    router = Agent(name="router", instructions="x", model=model, tools=[function_tool(tools.by_name["route_claim"])])
    front = Agent(name="front", instructions="x", model=model, handoffs=[router if via == "agent" else handoff(router)])
    async with scenario_task("denied_tool", records) as task:
        assert await task.run(lambda ctx: run_agent(ctx, front)) == "I may not route claims."
    assert_refused_as_everywhere(task.events, records, tools)


@pytest.mark.parametrize("shape", ["direct", "through a handoff object"])
async def test_openai_agents_runs_an_agent_tool_on_the_task_and_refuses_the_denied_tool_it_reaches(shape):
    """Agent.as_tool runs its own Runner; the adapter runs it on the task's hooks, so the denied tool the agent behind
    it reaches is refused as everywhere, and every model call of the nested run is reported: A has B.as_tool(); B hands
    off to C through a handoff object; C has the scoped tool."""
    pytest.importorskip("agents")
    from agents import Agent, function_tool, handoff

    from sam_link_sdk.openai_agents import run_agent

    from .test_openai_agents import ScriptedModel, call as fn_call, message, usage

    tools, records = Tools(), []
    turns = [
        ([fn_call("call_o", "ask_router", {"input": "route C-1"})], usage(1, 1)),
        *([([fn_call("call_h", "transfer_to_router", {})], usage(1, 1))] if shape != "direct" else []),
        ([fn_call("call_r", "route_claim", {"claim_id": "C-1"})], usage(3, 1)),
        ([message("I may not route.")], usage(4, 1)),
        ([message(NESTED_ANSWER)], usage(2, 1)),
    ]
    model = ScriptedModel(turns)
    router = Agent(name="router", instructions="x", model=model, tools=[function_tool(tools.by_name["route_claim"])])
    middle = Agent(name="middle", instructions="x", model=model, handoffs=[handoff(router)])
    behind = router if shape == "direct" else middle
    front = Agent(name="front", instructions="x", model=model, tools=[behind.as_tool("ask_router", "Routes claims.")])
    async with scenario_task("denied_tool", records) as task:
        assert await task.run(lambda ctx: run_agent(ctx, front)) == NESTED_ANSWER
    assert_refused_as_everywhere(task.events, records, tools)
    assert sum(isinstance(e, ModelFinished) for e in task.events) == len(turns)


async def test_openai_agents_hooks_end_the_run_on_a_denied_tool_the_sdk_runs_anyway():
    """A tool the hooks could not take off the agent (an MCP server's) is refused when it starts, ending the run."""
    pytest.importorskip("agents")
    from agents import Agent

    from sam_link_sdk import ToolAccessDenied
    from sam_link_sdk.openai_agents import step_reporter

    tools, records = Tools(), []
    async with scenario_task("denied_tool", records) as task:
        hooks = step_reporter(task.ctx, Agent(name="front"))
        with pytest.raises(ToolAccessDenied):
            await hooks.on_tool_start(
                SimpleNamespace(tool_call_id="call_r", tool_arguments='{"claim_id": "C-1"}'),
                hooks.agent,
                SimpleNamespace(name="route_claim"),
            )
        await settle()
        assert_refused_as_everywhere(task.events, records, tools)


async def test_langgraph_refuses_a_denied_tool_inside_a_subgraph():
    """The step reporter reaches every tool of every subgraph through the run config. A ToolNode built with
    handle_tool_errors gives the model the refusal and the run goes on."""
    pytest.importorskip("langgraph")
    from langchain_core.messages import AIMessage, HumanMessage
    from langchain_core.tools import tool
    from langgraph.graph import END, START, MessagesState, StateGraph
    from langgraph.prebuilt import ToolNode, create_react_agent

    from sam_link_sdk.langchain import step_reporter

    from .test_callbacks import ToolCallingFake

    tools, records = Tools(), []
    model = ToolCallingFake(
        messages=iter(
            [
                AIMessage(
                    content="", tool_calls=[{"id": "call_r", "name": "route_claim", "args": {"claim_id": "C-1"}}]
                ),
                AIMessage(content="I may not route claims."),
            ]
        )
    )
    router = create_react_agent(model, ToolNode([tool(tools.by_name["route_claim"])], handle_tool_errors=True))
    parent = StateGraph(MessagesState)
    parent.add_node("router", router)
    parent.add_edge(START, "router")
    parent.add_edge("router", END)
    async with scenario_task("denied_tool", records) as task:
        result = await parent.compile().ainvoke(
            {"messages": [HumanMessage(task.ctx.text)]}, config={"callbacks": [step_reporter(task.ctx)]}
        )
        await settle()
        assert result["messages"][-1].text == "I may not route claims."
        assert_refused_as_everywhere(task.events, records, tools)


async def test_adk_refuses_a_denied_tool_of_a_sub_agent():
    pytest.importorskip("google.adk")
    from google.adk.agents import LlmAgent
    from google.adk.runners import InMemoryRunner
    from google.genai import types

    from sam_link_sdk.adk import run_agent, step_reporter

    from .test_adk import Scripted, fc, usage

    tools, records = Tools(), []
    model = Scripted(
        model=MODEL,
        turns=[
            ([fc("transfer_to_agent", {"agent_name": "router"}, call_id="call_h")], usage(1, 1)),
            ([fc("route_claim", {"claim_id": "C-1"}, call_id="call_r")], usage(1, 1)),
            ([types.Part(text="I may not route claims.")], usage(1, 1)),
        ],
    )
    router = LlmAgent(name="router", model=model, instruction="Route claims.", tools=[tools.by_name["route_claim"]])
    front = LlmAgent(name="front", model=model, instruction="Hand claims to the router.", sub_agents=[router])
    runner = InMemoryRunner(agent=front, app_name="claims", plugins=[step_reporter()])
    async with scenario_task("denied_tool", records) as task:
        assert await task.run(lambda ctx: run_agent(ctx, runner)) == "I may not route claims."
    assert_refused_as_everywhere(task.events, records, tools)


@pytest.fixture
def quiet_crewai(monkeypatch):
    monkeypatch.setenv("CREWAI_DISABLE_TELEMETRY", "true")
    monkeypatch.setenv("OTEL_SDK_DISABLED", "true")


async def test_crewai_refuses_a_denied_tool_of_the_second_agent_of_a_real_crew(quiet_crewai):
    """A real two-agent crew with text (ReAct) tool calling, where CrewAI emits no tool event for a call a hook
    blocks: the adapter's own event reports the refusal."""
    pytest.importorskip("crewai")
    from crewai import Agent, Crew, Task
    from crewai.tools import tool as crew_tool

    from sam_link_sdk.crewai import run_agent

    from .test_kill_switch_frameworks import ScriptedLLM

    tools, records = Tools(), []
    route = crew_tool("route_claim")(tools.by_name["route_claim"])
    intake = Agent(
        role="intake",
        goal="g",
        backstory="b",
        verbose=False,
        max_iter=3,
        llm=ScriptedLLM(["Thought: done\nFinal Answer: P-1 is gold."]),
    )
    router = Agent(
        role="router",
        goal="g",
        backstory="b",
        tools=[route],
        verbose=False,
        max_iter=3,
        llm=ScriptedLLM(
            [
                'Thought: route it\nAction: route_claim\nAction Input: {"claim_id": "C-1"}',
                "Thought: denied\nFinal Answer: I may not route claims.",
            ]
        ),
    )
    crew = Crew(
        agents=[intake, router],
        verbose=False,
        tasks=[
            Task(description="Look up {request}", expected_output="x", agent=intake),
            Task(description="Route {request}", expected_output="x", agent=router),
        ],
    )
    async with scenario_task("denied_tool", records) as task:
        assert await task.run(lambda ctx: run_agent(ctx, lambda _: crew)) == "I may not route claims."
    assert_refused_as_everywhere(task.events, records, tools)


async def test_crewai_hides_and_refuses_a_denied_tool_with_native_tool_calling(quiet_crewai):
    pytest.importorskip("crewai")
    from crewai import Agent, Crew, Task
    from crewai.llms.base_llm import BaseLLM
    from crewai.tools import tool as crew_tool

    from sam_link_sdk.crewai import run_agent

    class NativeLLM(BaseLLM):
        def __init__(self, replies):
            super().__init__(model=MODEL)
            self.replies, self.offered = list(replies), []

        def call(self, messages, tools=None, callbacks=None, available_functions=None, **kwargs):
            self.offered.append([t["function"]["name"] for t in tools or []])
            return self.replies.pop(0)

        def supports_function_calling(self):
            return True

        def supports_stop_words(self):
            return False

        def get_context_window_size(self):
            return 8000

    tools, records = Tools(), []
    llm = NativeLLM(
        [
            [
                {
                    "id": "call_r",
                    "type": "function",
                    "function": {"name": "route_claim", "arguments": json.dumps({"claim_id": "C-1"})},
                }
            ],
            "I may not route claims.",
        ]
    )
    router = Agent(
        role="router",
        goal="g",
        backstory="b",
        llm=llm,
        verbose=False,
        max_iter=3,
        tools=[
            crew_tool("lookup_policy")(tools.by_name["lookup_policy"]),
            crew_tool("route_claim")(tools.by_name["route_claim"]),
        ],
    )
    crew = Crew(
        agents=[router], tasks=[Task(description="Route {request}", expected_output="x", agent=router)], verbose=False
    )
    async with scenario_task("denied_tool", records) as task:
        assert await task.run(lambda ctx: run_agent(ctx, lambda _: crew)) == "I may not route claims."
    assert llm.offered == [["lookup_policy"], ["lookup_policy"]]
    assert_refused_as_everywhere(task.events, records, tools)


LINK_TURNS = [Turn("", (("call_r", "route_claim", {"claim_id": "C-1"}),), (10, 2)), Turn("Done.", (), (12, 3))]
"""On the agent link no identity reaches the agent: SAM checks the user against the scopes the agent described for its
tools and sends, with each task, the ones the user holds (grantedToolScopes). These run every adapter as the handler of
a real MeshAgent on a LinkTransport, with the card's tools built by the adapter's own tool_infos from the tools
with_scopes guarded (Hermes: tool_infos(scopes=...)), so what SAM is told (describe) and what the adapter enforces come
from the same declaration.
"""
SPANS_ONLY = {"otel", "otel_strands"}
"""Read spans after the fact: they cannot refuse a call (the documented exception)"""
GRANTS = {
    "granted": ["tool:claims:route", "claims:*:read"],
    "another_scope_granted": ["tool:claims:*"],
    "nothing_granted": [],
    "no_granted_list": None,
}
"""Exact match: a wildcard SAM granted is not tool:claims:route.

A task frame without grantedToolScopes grants nothing.
"""


def link_tool_infos(adapter: str, driver: Driver) -> list:
    if adapter in SPANS_ONLY:
        from sam_link_sdk import ToolInfo

        return [
            ToolInfo(f.__name__, f.__doc__ or "", DENIED_SCOPES.get(f.__name__, []))
            for f in driver.tools.for_scenario("denied_tool")
        ]
    return driver.tool_infos(DENIED_SCOPES)


async def run_on_the_link(adapter: str, grant: str, monkeypatch) -> dict:
    from sam_link_sdk import AgentConfig, MeshAgent
    from sam_link_sdk.link import LinkTransport

    from .test_link import AGENT, LinkServer, eventually, link_config, task

    monkeypatch.setenv("HOME", tempfile.mkdtemp(prefix="sam-link-sdk-link-"))
    tools, records = Tools(), []
    driver = DRIVERS[adapter](LINK_TURNS, tools, "denied_tool")
    server = await LinkServer().start()
    config = AgentConfig(
        name=AGENT,
        description="d",
        keepalive_seconds=0,
        shutdown_grace_seconds=0,
        tools=link_tool_infos(adapter, driver),
        audit_sink=records.append,
    )
    agent = MeshAgent(config, driver.run, transport=LinkTransport(link_config(server)))
    try:
        await agent.start()
        await eventually(lambda: server.of("consume"))
        frame = task(1, "T1", text="Route claim C-1")
        if GRANTS[grant] is None:
            del frame["task"]["grantedToolScopes"]
        else:
            frame["task"]["grantedToolScopes"] = GRANTS[grant]
        await server.send(frame)
        [result] = await eventually(lambda: [f for f in server.of("result") if f["taskId"] == "T1"], timeout=20)
    finally:
        driver.cleanup()
        await agent.stop()
        await server.stop()
    finished = [
        f["event"]
        for f in server.of("event")
        if f["event"]["kind"] == "tool_finished" and f["event"]["name"] == "route_claim"
    ]
    [described] = server.of("describe")[:1]
    return {
        "described": {t["name"]: t["requiredScopes"] for t in described["agent"]["tools"]},
        "ran": [n for n in tools.ran if n == "route_claim"],
        "finished": [(e["result"], e["failed"]) for e in finished],
        "audit": [(r.message, r.fields.get("tool")) for r in records if r.fields.get("tool") == "route_claim"],
        "result": result,
    }


@pytest.mark.parametrize("grant", list(GRANTS))
@pytest.mark.parametrize("adapter", list(DRIVERS))
async def test_a_link_task_runs_a_scoped_tool_only_when_sam_granted_its_scopes(adapter, grant, monkeypatch, caplog):
    difference = DIFFERENCES.get((adapter, "denied_tool"))
    if isinstance(difference, NotApplicable):
        pytest.skip(difference.reason)
    with caplog.at_level(logging.WARNING, logger="sam_link_sdk.otel_steps"):
        seen = await run_on_the_link(adapter, grant, monkeypatch)

    assert seen["described"] == {"lookup_policy": [], "route_claim": ["tool:claims:route"]}
    if grant == "granted" or adapter in SPANS_ONLY:
        assert seen["ran"] == ["route_claim"]
        assert [failed for _, failed in seen["finished"]] == [False]
        assert "fast-track" in json.dumps(seen["finished"][0][0])
        assert seen["audit"] == [("tool execution succeeded", "route_claim")]
        assert seen["result"].get("text") == "Done."
        if adapter in SPANS_ONLY:
            assert any("not enforced" in r.getMessage() for r in caplog.records)
        return
    assert seen["ran"] == []
    assert seen["finished"] == [(REFUSAL, True)]
    assert seen["audit"] == EXPECTED["denied_tool"]["audit"]


NESTED_USAGE = {"outer_call": (1, 1), "inner_call": (3, 1), "inner_answer": (4, 1), "outer_answer": (2, 1)}
"""Each adapter's framework offers a way for a run to start another (an agent run as a tool, a handoff, a sub-agent, a
subgraph, a delegating manager, a delegated subagent, a tool that runs an agent). The scenario: the outer model calls
the nested agent (usage 1/1), whose model calls the denied route_claim (3/1) and then answers (4/1), and the outer model
answers (2/1). The nested run must be covered as the outer one is: route_claim refused the same way
(nested_denied_tool), and the nested model's steps and tokens reaching the task (nested_steps_and_usage).
"""
NESTED_TOTALS = (10, 4, 4)
NESTED_ANSWER = "Routing was refused."


async def nested_langchain(ctx, tools):
    """A tool behind with_approval that runs a LangGraph agent of its own: the approved tool runs with the run's
    config, so the reporter reaches the agent it runs."""
    from langchain_core.messages import AIMessage
    from langchain_core.tools import tool
    from langgraph.prebuilt import ToolNode, create_react_agent

    from sam_link_sdk.langchain import run_agent, with_approval

    from .test_callbacks import ToolCallingFake

    def ai(text, calls, usage):
        return AIMessage(
            content=text,
            tool_calls=[{"id": c, "name": n, "args": a} for c, n, a in calls],
            usage_metadata={"input_tokens": usage[0], "output_tokens": usage[1], "total_tokens": sum(usage)},
        )

    inner_model = ToolCallingFake(
        messages=iter(
            [
                ai("", [("call_r", "route_claim", {"claim_id": "C-1"})], NESTED_USAGE["inner_call"]),
                ai("I may not route.", [], NESTED_USAGE["inner_answer"]),
            ]
        )
    )
    router = create_react_agent(inner_model, ToolNode([tool(tools.by_name["route_claim"])], handle_tool_errors=True))

    @tool
    async def ask_router(request: str) -> str:
        """Ask the routing agent."""
        result = await router.ainvoke({"messages": [("user", request)]})
        return result["messages"][-1].text

    async def approve(name, args, *, message="", timeout_seconds=None):
        return SimpleNamespace(approved=True, outcome="approved")

    ctx.request_approval = approve
    outer_model = ToolCallingFake(
        messages=iter(
            [
                ai("", [("call_o", "ask_router", {"request": "route C-1"})], NESTED_USAGE["outer_call"]),
                ai(NESTED_ANSWER, [], NESTED_USAGE["outer_answer"]),
            ]
        )
    )
    return await run_agent(ctx, outer_model, with_approval(ctx, [ask_router], names=["ask_router"]))


async def nested_openai_agents(ctx, tools):
    """A handoff object (handoff(router)): the agent it returns is prepared at the handoff."""
    from agents import Agent, function_tool, handoff

    from sam_link_sdk.openai_agents import run_agent

    from .test_openai_agents import ScriptedModel, call as fn_call, message, usage

    model = ScriptedModel(
        [
            ([fn_call("call_o", "transfer_to_router", {})], usage(*NESTED_USAGE["outer_call"])),
            ([fn_call("call_r", "route_claim", {"claim_id": "C-1"})], usage(*NESTED_USAGE["inner_call"])),
            ([message("I may not route.")], usage(*NESTED_USAGE["inner_answer"])),
            ([message(NESTED_ANSWER)], usage(*NESTED_USAGE["outer_answer"])),
        ]
    )
    router = Agent(
        name="router", instructions="x", model=model, tools=[function_tool(tools.by_name["route_claim"])], handoffs=[]
    )
    front = Agent(name="front", instructions="x", model=model, handoffs=[handoff(router)])
    router.handoffs = [front]
    model.turns[2] = ([fn_call("call_b", "transfer_to_front", {})], usage(*NESTED_USAGE["inner_answer"]))
    return await run_agent(ctx, front)


async def nested_adk(ctx, tools):
    """An AgentTool (which includes the Runner's plugins by default)."""
    from google.adk.agents import LlmAgent
    from google.adk.runners import InMemoryRunner
    from google.adk.tools.agent_tool import AgentTool
    from google.genai import types

    from sam_link_sdk.adk import run_agent, step_reporter

    from .test_adk import Scripted, fc, usage

    model = Scripted(
        model=MODEL,
        turns=[
            ([fc("router", {"request": "route C-1"}, call_id="call_o")], usage(*NESTED_USAGE["outer_call"])),
            ([fc("route_claim", {"claim_id": "C-1"}, call_id="call_r")], usage(*NESTED_USAGE["inner_call"])),
            ([types.Part(text="I may not route.")], usage(*NESTED_USAGE["inner_answer"])),
            ([types.Part(text=NESTED_ANSWER)], usage(*NESTED_USAGE["outer_answer"])),
        ],
    )
    router = LlmAgent(name="router", model=model, instruction="Route claims.", tools=[tools.by_name["route_claim"]])
    front = LlmAgent(name="front", model=model, instruction="Ask the router.", tools=[AgentTool(agent=router)])
    return await run_agent(ctx, InMemoryRunner(agent=front, app_name="claims", plugins=[step_reporter()]))


async def nested_crewai(ctx, tools):
    """A hierarchical crew: the manager (built at kickoff) delegates to a worker that calls route_claim."""
    from crewai import Agent, Crew, Process, Task
    from crewai.events import crewai_event_bus
    from crewai.events.types.llm_events import LLMCallType
    from crewai.llms.base_llm import BaseLLM
    from crewai.tools import tool as crew_tool

    from sam_link_sdk.crewai import run_agent

    from .test_crewai import llm_end, llm_start

    class Scripted(BaseLLM):
        """Text (ReAct) replies, with the LLM events a real CrewAI LLM emits around each call."""

        def __init__(self, replies):
            super().__init__(model=MODEL)
            self.replies = list(replies)

        def call(self, messages, tools=None, callbacks=None, available_functions=None, **kwargs):
            reply, (i, o) = self.replies.pop(0)
            crewai_event_bus.emit(self, llm_start(f"L{id(self)}{len(self.replies)}", model=MODEL))
            crewai_event_bus.emit(
                self,
                llm_end(
                    f"L{id(self)}{len(self.replies)}",
                    reply,
                    LLMCallType.LLM_CALL,
                    {"input_tokens": i, "output_tokens": o},
                    model=MODEL,
                ),
            )
            return reply

        def supports_function_calling(self):
            return False

        def supports_stop_words(self):
            return False

        def get_context_window_size(self):
            return 8000

    manager_llm = Scripted(
        [
            (
                "Thought: ask the router\nAction: Delegate work to coworker\n"
                'Action Input: {"task": "route C-1", "context": "claim C-1", "coworker": "router"}',
                NESTED_USAGE["outer_call"],
            ),
            (f"Thought: done\nFinal Answer: {NESTED_ANSWER}", NESTED_USAGE["outer_answer"]),
        ]
    )
    worker_llm = Scripted(
        [
            ('Thought: route it\nAction: route_claim\nAction Input: {"claim_id": "C-1"}', NESTED_USAGE["inner_call"]),
            ("Thought: refused\nFinal Answer: I may not route.", NESTED_USAGE["inner_answer"]),
        ]
    )
    router = Agent(
        role="router",
        goal="g",
        backstory="b",
        llm=worker_llm,
        verbose=False,
        max_iter=3,
        tools=[crew_tool("route_claim")(tools.by_name["route_claim"])],
    )
    manager = Agent(
        role="manager", goal="g", backstory="b", llm=manager_llm, allow_delegation=True, verbose=False, max_iter=3
    )
    crew = Crew(
        agents=[router],
        tasks=[Task(description="Route {request}", expected_output="x")],
        process=Process.hierarchical,
        manager_agent=manager,
        verbose=False,
    )
    return await run_agent(ctx, lambda _: crew)


async def nested_strands(ctx, tools):
    """An agent given to another as a tool (tools=[router]: Strands wraps it with as_tool())."""
    from strands import Agent, tool

    from sam_link_sdk.strands import run_agent

    from .test_strands import ScriptedModel, call, text, usage

    model = ScriptedModel(
        [
            ([call("call_o", "router", {"input": "route C-1"})], usage(*NESTED_USAGE["outer_call"])),
            ([call("call_r", "route_claim", {"claim_id": "C-1"})], usage(*NESTED_USAGE["inner_call"])),
            ([text("I may not route.")], usage(*NESTED_USAGE["inner_answer"])),
            ([text(NESTED_ANSWER)], usage(*NESTED_USAGE["outer_answer"])),
        ]
    )
    router = Agent(
        name="router",
        description="Routes claims.",
        model=model,
        callback_handler=None,
        tools=[tool(tools.by_name["route_claim"])],
    )
    return await run_agent(ctx, lambda c: Agent(model=model, tools=[router], callback_handler=None))


async def nested_agent_framework(ctx, tools):
    """Agent.as_tool(): the sub-agent runs with its own middleware only."""
    from agent_framework import Agent, tool

    from sam_link_sdk.agent_framework import run_agent

    from .test_agent_framework import ScriptedClient, call as fn_call, text, usage

    inner = Agent(
        ScriptedClient(
            [
                ([fn_call("call_r", "route_claim", {"claim_id": "C-1"})], usage(*NESTED_USAGE["inner_call"])),
                ([text("I may not route.")], usage(*NESTED_USAGE["inner_answer"])),
            ],
            model=MODEL,
        ),
        "Route claims.",
        name="router",
        tools=[tool(approval_mode="never_require")(tools.by_name["route_claim"])],
    )
    outer = Agent(
        ScriptedClient(
            [
                ([fn_call("call_o", "router", {"task": "route C-1"})], usage(*NESTED_USAGE["outer_call"])),
                ([text(NESTED_ANSWER)], usage(*NESTED_USAGE["outer_answer"])),
            ],
            model=MODEL,
        ),
        "Ask the router.",
        name="front",
        tools=[inner.as_tool()],
    )
    return await run_agent(ctx, outer)


async def nested_hermes(ctx, tools, monkeypatch):
    """Hermes' delegate_task: a child AIAgent with a session of its own and the parent's toolsets."""
    from sam_link_sdk.hermes import run_agent

    from .test_hermes_hooks import FakeHermes

    hermes = FakeHermes(monkeypatch)
    route = tools.by_name["route_claim"]

    def turn(h, agent):
        h.model_call("sam-T", [("call_o", "delegate_task")], NESTED_USAGE["outer_call"])
        h.fire("subagent_start", parent_session_id="sam-T", child_session_id="child-1")
        h.model_call("child-1", [("call_r", "route_claim")], NESTED_USAGE["inner_call"])
        if (
            h.fire(
                "pre_tool_call",
                session_id="child-1",
                tool_name="route_claim",
                args={"claim_id": "C-1"},
                tool_call_id="call_r",
            )
            is None
        ):
            route(claim_id="C-1")
        h.model_call("child-1", (), NESTED_USAGE["inner_answer"])
        h.fire("subagent_stop", parent_session_id="sam-T", child_session_id="child-1")
        h.model_call("sam-T", (), NESTED_USAGE["outer_answer"])

    await run_agent(ctx, lambda c: hermes.agent(["route_claim", "delegate_task"], turn))
    return NESTED_ANSWER


NESTED = {
    "langchain": nested_langchain,
    "openai_agents": nested_openai_agents,
    "adk": nested_adk,
    "crewai": nested_crewai,
    "strands": nested_strands,
    "agent_framework": nested_agent_framework,
    "hermes": nested_hermes,
}
NESTED_NEEDS = {
    "langchain": "langgraph",
    "openai_agents": "agents",
    "adk": "google.adk",
    "crewai": "crewai",
    "strands": "strands",
    "agent_framework": "agent_framework",
    "hermes": None,
}


async def observe_nested(adapter, monkeypatch):
    if NESTED_NEEDS[adapter]:
        pytest.importorskip(NESTED_NEEDS[adapter])
    if adapter == "crewai":
        monkeypatch.setenv("CREWAI_DISABLE_TELEMETRY", "true")
    records: list = []
    tools = Tools()
    run = NESTED[adapter]
    async with scenario_task("denied_tool", records) as task:
        answer = await task.run(lambda ctx: run(ctx, tools, monkeypatch) if adapter == "hermes" else run(ctx, tools))
        totals = task.ctx._run.usage.final_token_usage()
        llm_calls = task.ctx._run.usage.llm_calls
    return answer, task.events, records, tools, (totals["total_input_tokens"], totals["total_output_tokens"], llm_calls)


@pytest.mark.parametrize("adapter", list(NESTED))
async def test_nested_denied_tool(adapter, monkeypatch):
    answer, events, records, tools, _ = await observe_nested(adapter, monkeypatch)
    assert answer == NESTED_ANSWER
    assert_refused_as_everywhere(events, records, tools)


@pytest.mark.parametrize("adapter", list(NESTED))
async def test_nested_steps_and_usage(adapter, monkeypatch):
    _, events, _, _, totals = await observe_nested(adapter, monkeypatch)
    assert totals == NESTED_TOTALS
    assert sum(isinstance(e, ModelFinished) for e in events) == NESTED_TOTALS[2]


LEFTOVER = ("after_answer_scoped_tool", "after_stop_scoped_tool")
"""A handler that returns while an asyncio task it started (a framework's background run, a thread with the context) goes
on: once the task is answered, and after SAM's stop, that work calls a scoped tool. The tool's own check refuses it (its
body does not run) and nothing is sent for the finished task. The agent marks the context answered
(TaskContext.answered), and the context reads SAM's stop of the agent live after the answer.
"""


def _sent_about(transport, task_id: str) -> dict[str, list]:
    """Everything the agent has sent SAM about one task, by kind."""
    return {
        "events": transport.events(task_id),
        "answers": [o for t, o in transport.answers if t == task_id],
        "prompts": transport.prompts(task_id),
        "peer_calls": transport.peer_calls(task_id),
    }


async def _leftover_call(adapter: str, row: str, tmp_path, mark_by_hand: bool) -> tuple[list, list, list]:
    """Serve one task whose handler leaves a call to a scoped tool running; release it once the task is answered
    (and, for after_stop_scoped_tool, SAM has stopped the agent). Returns what the body ran, the tool's answer, and what
    was sent about the task after the answer.

    SAM granted the user route_claim's scopes: the task being over is the only reason left to refuse the call.
    """
    from sam_link_sdk.config import ToolInfo

    from .test_scope_guard import KITS, SCOPES, Body, guarded

    kit, body = KITS[adapter], Body()
    kit.adapter()
    tool = guarded(kit, body)
    go, done, texts, contexts = asyncio.Event(), asyncio.Event(), [], []

    async def leftover():
        await go.wait()
        texts.append(await kit.call(tool, {"claim_id": "C-1"}))
        done.set()

    async def handler(ctx):
        contexts.append(ctx)
        asyncio.get_running_loop().create_task(leftover())
        return "answered"

    agent, transport = await start_agent(
        handler,
        state_dir=str(tmp_path),
        description="d",
        tools=[ToolInfo("route_claim", "Route.", list(SCOPES["route_claim"]))],
    )
    try:
        await transport.deliver(
            "T1", "Route claim C-1", user_id="u1", granted_tool_scopes=frozenset(SCOPES["route_claim"])
        )
        assert transport.finished["T1"].text == "answered"
        if row == "after_stop_scoped_tool":
            await transport.control(False, "stopped_by_sam")
        if mark_by_hand:
            contexts[0].answered = True
            contexts[0].halted = row == "after_stop_scoped_tool"
        answered = _sent_about(transport, "T1")
        go.set()
        await asyncio.wait_for(done.wait(), 10)
        await settle()
        about_task = [
            sent for kind, so_far in _sent_about(transport, "T1").items() for sent in so_far[len(answered[kind]) :]
        ]
    finally:
        await agent.stop()
    return body.ran, texts, about_task


@pytest.mark.parametrize("row", LEFTOVER)
@pytest.mark.parametrize("adapter", ["langchain", "openai_agents", "adk", "crewai", "strands", "agent_framework"])
async def test_a_scoped_tool_refuses_work_left_running_once_its_context_is_marked_answered(
    adapter, row, monkeypatch, tmp_path
):
    monkeypatch.setenv("HOME", str(tmp_path))
    ran, texts, sent = await _leftover_call(adapter, row, tmp_path, mark_by_hand=True)
    assert ran == [] and sent == []
    assert ("already been answered" if row == "after_answer_scoped_tool" else "was stopped") in texts[0]


@pytest.mark.parametrize("row", LEFTOVER)
@pytest.mark.parametrize("adapter", ["langchain", "openai_agents", "adk", "crewai", "strands", "agent_framework"])
async def test_a_scoped_tool_called_by_work_left_running_after_the_task_does_not_run(
    adapter, row, monkeypatch, tmp_path
):
    monkeypatch.setenv("HOME", str(tmp_path))
    ran, _, sent = await _leftover_call(adapter, row, tmp_path, mark_by_hand=False)
    assert ran == [] and sent == []


@pytest.mark.parametrize("adapter", list(DRIVERS))
async def test_a_run_started_for_an_answered_task_runs_no_tool_and_sends_nothing(adapter):
    """A run the handler left behind that starts (or goes on) once its task is answered: every adapter's hooks refuse
    its model and tool calls, and nothing is published for the task the transport no longer holds."""
    tools = Tools()
    driver = DRIVERS[adapter](SCENARIOS["one_tool"], tools, "one_tool")
    async with scenario_task("one_tool", []) as task:
        assert await task.run(lambda ctx: "answered") == "answered"
        await settle()
        assert task.answer.text == "answered" and task.ctx._run.answered
        sent_at_answer = len(task.events)
        try:
            await driver.run(task.ctx)
        except Exception:
            pass
        finally:
            driver.cleanup()
        await settle()
        assert tools.ran == [] and task.events[sent_at_answer:] == []
