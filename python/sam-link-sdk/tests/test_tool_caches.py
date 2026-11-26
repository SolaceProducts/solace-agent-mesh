# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""What sits in front of a tool's body in each framework: a result cache, memoisation or replay that could answer a
call to a scoped tool without the tool's own check running.

Found in the installed versions (docs/contributing/adapters.md, "Caches in front of a tool"):

- CrewAI 1.15: a crew's tool-result cache (``Crew(cache=True)``, the default), read before the tool is called, keyed by
  tool name and arguments. sam-link-sdk's wrappers are never cached (``cache_function`` always False), so a guarded
  tool's result, or its refusal, never answers a later call: tested here with a real crew.
- LangGraph 1.2: a node's ``cache_policy`` (with ``compile(cache=...)``) answers a hit without running the node, so a
  ``ToolNode`` holding scoped tools must not have one: pinned here, so a change in LangGraph shows.
- OpenAI Agents 0.20: a run skips a tool call item it has already seen (the same call, by identity) within that run;
  no result reuse across runs. Strands 1.61: its tool-stage middleware may answer from a cache, but the API is private
  (``strands._middleware``) and no cache ships with it. ADK 2.14, Agent Framework 1.22, langchain-core 1.6: caches only
  declarations and schemas, never results. Hermes (checkout b65e078c): drops duplicate tool calls within one model
  response; no result reuse.
"""

from __future__ import annotations

import asyncio
import json

import pytest

from sam_link_sdk.context import bind_task

from .adapter_task import adapter_task
from .test_scope_guard import SCOPE, SCOPES, Body


def _crew(body: Body):
    from crewai import Agent, Crew, Task
    from crewai.llms.base_llm import BaseLLM
    from crewai.tools import tool as crew_tool

    from sam_link_sdk.crewai import with_scopes

    seen: list[str] = []

    class Scripted(BaseLLM):
        """Each kickoff: call route_claim for C-1, then answer with what the tool returned."""

        def __init__(self):
            super().__init__(model="fake-model")
            self.calls = 0

        def call(self, messages, tools=None, callbacks=None, available_functions=None, **kwargs):
            self.calls += 1
            if self.calls % 2:
                return 'Thought: route it\nAction: route_claim\nAction Input: {"claim_id": "C-1"}'
            seen.append(json.dumps(messages[-3:], default=str))
            return "Thought: done\nFinal Answer: done"

        def supports_function_calling(self):
            return False

        def supports_stop_words(self):
            return False

        def get_context_window_size(self):
            return 8000

    [tool] = with_scopes([crew_tool("route_claim")(body.fn)], SCOPES)
    router = Agent(role="router", goal="g", backstory="b", llm=Scripted(), tools=[tool], verbose=False, max_iter=3)
    crew = Crew(
        agents=[router],
        tasks=[Task(description="Route {request}", expected_output="x", agent=router)],
        cache=True,
        verbose=False,
    )
    return crew, seen


@pytest.mark.parametrize("order", ["granted_first", "denied_first"])
async def test_crewai_a_crew_reused_across_tasks_never_answers_a_scoped_tool_from_its_cache(order):
    """A handler that kicks off a shared crew itself (no run_agent, so no sam-link-sdk hook): user A holds the scope, user
    B does not. B never gets A's result, and A is never refused by B's cached refusal."""
    pytest.importorskip("crewai")
    body = Body()
    crew, seen = _crew(body)
    async with (
        adapter_task("T1", scopes=SCOPES, granted=[SCOPE]) as granted,
        adapter_task("T2", scopes=SCOPES, granted=[]) as denied,
    ):
        for task in (granted, denied) if order == "granted_first" else (denied, granted):
            with bind_task(task.ctx):
                await asyncio.to_thread(crew.kickoff, inputs={"request": "C-1"})
    assert body.ran == ["C-1"]
    observed = dict(zip(("first", "second"), seen, strict=False))
    granted_saw = observed["first" if order == "granted_first" else "second"]
    denied_saw = observed["second" if order == "granted_first" else "first"]
    assert "fast-track" in granted_saw and "Access denied" not in granted_saw
    assert "fast-track" not in denied_saw and "Access denied" in denied_saw


async def test_crewai_a_wrapper_carries_every_field_of_the_tool_but_its_cache_function_and_usage_limit():
    """Diffed against the installed BaseTool: a field CrewAI adds shows here, to be carried or excluded on purpose. The
    usage limit stays on the wrapped tool, which claims it for the calls that run (test_refusal_budgets)."""
    pytest.importorskip("crewai")
    from crewai.tools import BaseTool, tool as crew_tool

    from sam_link_sdk.crewai import with_scopes

    body = Body()
    raw = crew_tool("route_claim", result_as_answer=True, max_usage_count=2)(body.fn)
    raw.cache_function = lambda arguments, result: True
    [tool] = with_scopes([raw], SCOPES)
    assert tool.cache_function({}, "fast-track") is False
    async with adapter_task(scopes=SCOPES, granted=[SCOPE]) as task:
        with bind_task(task.ctx):
            assert [tool.run(claim_id=f"C-{n}") for n in range(2)] == ["fast-track", "fast-track"]
            assert "usage limit of 2" in str(tool.run(claim_id="C-3"))
    assert body.ran == ["C-0", "C-1"]

    from sam_link_sdk.crewai.tools import CARRIED_FIELDS, USAGE_FIELDS

    assert set(BaseTool.model_fields) == {*CARRIED_FIELDS, *USAGE_FIELDS, "description_updated", "cache_function"}
    for field in CARRIED_FIELDS:
        assert getattr(tool, field) == getattr(raw, field), field
    assert tool.max_usage_count is None and raw.current_usage_count == 2


async def test_langgraph_a_node_cache_answers_without_running_the_node():
    """Why docs/frameworks/langchain.md says a ToolNode holding scoped tools must have no cache_policy: a hit answers
    the call without the node, so neither the step reporter nor the tool's own check runs."""
    pytest.importorskip("langgraph")
    from langchain_core.messages import AIMessage
    from langgraph.cache.memory import InMemoryCache
    from langgraph.graph import START, MessagesState, StateGraph
    from langgraph.prebuilt import ToolNode
    from langgraph.types import CachePolicy

    from .test_scope_guard import KITS, guarded

    body = Body()
    graph = StateGraph(MessagesState)
    graph.add_node(
        "tools", ToolNode([guarded(KITS["langchain"], body)], handle_tool_errors=True), cache_policy=CachePolicy()
    )
    graph.add_edge(START, "tools")
    graph = graph.compile(cache=InMemoryCache())
    call = AIMessage(content="", tool_calls=[{"id": "c1", "name": "route_claim", "args": {"claim_id": "C-1"}}])
    async with (
        adapter_task("T1", scopes=SCOPES, granted=[SCOPE]) as granted,
        adapter_task("T2", scopes=SCOPES, granted=[]) as denied,
    ):
        with bind_task(granted.ctx):
            first = await graph.ainvoke({"messages": [call]})
        with bind_task(denied.ctx):
            second = await graph.ainvoke({"messages": [call]})
    assert body.ran == ["C-1"] and second["messages"][-1].content == first["messages"][-1].content == "fast-track"
