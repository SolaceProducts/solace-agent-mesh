# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Human-in-the-loop, tool manifests, reasoning and request context for OpenAI Agents, ADK, CrewAI and LangGraph."""

from __future__ import annotations

import asyncio
import json
from types import SimpleNamespace

import pytest

from sam_link_sdk import ToolInfo
from sam_link_sdk.events import ModelFinished, Thinking
from sam_link_sdk.request_context import RequestContext

from .adapter_task import adapter_task
from .agent_rig import RecordingTransport, start_agent


async def start(handler):
    """A started agent serving `handler`, its transport, and the audit records it writes."""
    events: list = []
    agent, transport = await start_agent(handler, audit_sink=events.append)
    return agent, transport, events


async def prompt_number(transport: RecordingTransport, index: int, task_id: str = "T1"):
    """The index-th prompt the task sent, once it is sent (a framework's worker thread may still be on its way)."""
    for _ in range(500):
        prompts = transport.prompts(task_id)
        if len(prompts) > index:
            return prompts[index]
        await asyncio.sleep(0.01)
    raise AssertionError(f"no prompt {index} for {task_id}: {transport.prompts(task_id)}")


async def approve_next(transport: RecordingTransport, index=0, *, approved=True, task_id="T1"):
    await transport.answer(await prompt_number(transport, index, task_id), approved=approved)


async def final_text(transport: RecordingTransport, task_id="T1"):
    for _ in range(500):
        if task_id in transport.finished:
            break
        await asyncio.sleep(0.01)
    return transport.finished[task_id].text


pytest.importorskip("agents")

from agents import Agent, FunctionTool, ModelResponse, function_tool  # noqa: E402
from agents.tool_context import ToolContext  # noqa: E402
from openai.types.responses import ResponseReasoningItem  # noqa: E402
from openai.types.responses.response_reasoning_item import Summary  # noqa: E402

from sam_link_sdk import openai_agents as oa  # noqa: E402
from sam_link_sdk.openai_agents.steps import SamLinkRunHooks, _tool_result_data  # noqa: E402

from .test_openai_agents import ScriptedModel, call, message, usage  # noqa: E402


@function_tool
def send_email(to: str) -> str:
    """Send an email."""
    return f"sent to {to}"


@function_tool
def lookup(policy_id: str) -> str:
    """Look up a policy."""
    return "gold"


def tool_ctx(name, args):
    return ToolContext(context=None, tool_name=name, tool_call_id="call_1", tool_arguments=json.dumps(args))


def test_openai_tool_manifest():
    assert oa.tool_infos([send_email, lookup]) == [
        ToolInfo("send_email", "Send an email."),
        ToolInfo("lookup", "Look up a policy."),
    ]


async def test_openai_approval_runs_an_approved_tool_and_refuses_a_denied_one():
    async def handler(ctx):
        guarded = oa.with_approval(ctx, [send_email, lookup], names=["send_email"], message="Real email.")
        assert guarded[1] is lookup and isinstance(guarded[0], FunctionTool) and guarded[0].name == "send_email"
        assert guarded[0].params_json_schema == send_email.params_json_schema
        first = await guarded[0].on_invoke_tool(
            tool_ctx("send_email", {"to": "alice@example.com"}), json.dumps({"to": "alice@example.com"})
        )
        second = await guarded[0].on_invoke_tool(
            tool_ctx("send_email", {"to": "bob@example.com"}), json.dumps({"to": "bob@example.com"})
        )
        return f"{first} | {second}"

    agent, transport, events = await start(handler)
    await transport.deliver("T1")
    assert (await prompt_number(transport, 0)).approval.message == "Real email."
    await approve_next(transport, approved=True)
    await approve_next(transport, index=1, approved=False)

    text = await final_text(transport)
    first, second = text.split(" | ")
    assert first == "sent to alice@example.com"
    assert json.loads(second) == {
        "error": "The user did not approve this call to send_email (denied), so it was not run."
    }
    assert [e.message for e in events] == ["tool approval granted", "tool approval refused"]
    await agent.stop()


async def test_openai_ask_user_tool():
    async def handler(ctx):
        tool = oa.ask_user_tool(ctx)
        assert tool.name == "ask_user_question" and tool.params_json_schema["required"] == ["question"]
        return await tool.on_invoke_tool(
            tool_ctx(tool.name, {}), json.dumps({"question": "Queue?", "options": ["a", "b"]})
        )

    agent, transport, _ = await start(handler)
    await transport.deliver("T1")
    [question] = (await prompt_number(transport, 0)).questions
    assert question.question == "Queue?" and [o.value or o.label for o in question.options][:2] == ["a", "b"]
    await transport.answer(transport.prompts("T1")[0], answers=("b",))
    assert await final_text(transport) == "b"
    await agent.stop()


def test_openai_failed_tools_are_recognised_from_the_tracked_failure_not_the_sdk_failure_text():
    failed = "An error occurred while running the tool. Please try again. Error: backend down"
    assert _tool_result_data(failed, failed=True) == {"error": failed}
    assert _tool_result_data("lookup unavailable", failed=True) == {"error": "lookup unavailable"}
    assert _tool_result_data(failed, failed=False) == failed
    assert _tool_result_data('{"tier": "gold"}', failed=False) == {"tier": "gold"}
    assert _tool_result_data("all good", failed=False) == "all good"


async def test_openai_reasoning_summaries_stream_before_the_response_when_enabled():
    reasoning = ResponseReasoningItem(
        id="r1", type="reasoning", summary=[Summary(text="weigh the tiers", type="summary_text")]
    )
    response = ModelResponse(output=[reasoning, message("Fast-track.")], usage=usage(10, 2), response_id=None)

    async with adapter_task(text="hi", stream_thinking=True) as task:
        hooks = SamLinkRunHooks(task.ctx, Agent(name="triage"))
        await hooks.on_llm_end(None, None, response)
        sent = task.events

    assert [type(e) for e in sent] == [Thinking, Thinking, ModelFinished]
    assert sent[0].text == "weigh the tiers" and sent[1].done is True
    assert (sent[2].text, sent[2].tool_calls) == ("Fast-track.", ())


async def test_openai_reasoning_is_silent_by_default():
    reasoning = ResponseReasoningItem(id="r1", type="reasoning", summary=[Summary(text="private", type="summary_text")])
    async with adapter_task(text="hi") as task:
        response = ModelResponse(output=[reasoning, message("ok")], usage=usage(1, 1), response_id=None)
        await SamLinkRunHooks(task.ctx, Agent(name="triage")).on_llm_end(None, None, response)
        sent = task.events
    assert sent and "private" not in repr(sent)


class InstructionCapture(ScriptedModel):
    def __init__(self, turns):
        super().__init__(turns)
        self.system = []

    async def get_response(self, system_instructions, input, *args, **kwargs):
        self.system.append(system_instructions)
        output, u = self.turns.pop(0)
        return ModelResponse(output=output, usage=u, response_id=None)


async def test_openai_request_context_is_appended_to_the_instructions_when_asked():
    model = InstructionCapture(
        [([message("a")], usage(1, 1)), ([message("b")], usage(1, 1)), ([message("c")], usage(1, 1))]
    )
    agent = Agent(name="claims-triage", instructions="You triage.", model=model)

    def dynamic(run_context, current_agent):
        return "Dynamic base."

    async with adapter_task(text="hi", request_context=RequestContext(system_purpose="Help with claims.")) as task:
        ctx = task.ctx
        await oa.run_agent(ctx, agent)
        await oa.run_agent(ctx, agent, include_request_context=True)
        await oa.run_agent(ctx, Agent(name="d", instructions=dynamic, model=model), include_request_context=True)
    assert model.system == [
        "You triage.",
        "You triage.\n\n## System Purpose\n\nHelp with claims.",
        "Dynamic base.\n\n## System Purpose\n\nHelp with claims.",
    ]


async def test_openai_request_context_reaches_the_agent_handed_off_to():
    router_model = InstructionCapture([([message("Routed.")], usage(1, 1))])
    router = Agent(name="router", instructions="You route.", model=router_model)
    front_model = InstructionCapture([([call("call_h", "transfer_to_router", {})], usage(1, 1))])
    front = Agent(name="front", instructions="You are the front desk.", model=front_model, handoffs=[router])
    async with adapter_task(text="hi", request_context=RequestContext(system_purpose="Help with claims.")) as task:
        assert await oa.run_agent(task.ctx, front, include_request_context=True) == "Routed."
    assert front_model.system == ["You are the front desk.\n\n## System Purpose\n\nHelp with claims."]
    assert router_model.system == ["You route.\n\n## System Purpose\n\nHelp with claims."]


pytest.importorskip("google.adk")

from google.adk.models.llm_request import LlmRequest  # noqa: E402
from google.adk.tools import BaseTool  # noqa: E402
from google.genai import types  # noqa: E402

from sam_link_sdk import adk  # noqa: E402
from sam_link_sdk.adk.steps import SamLinkPlugin, _current, _request_prompt  # noqa: E402


def adk_send_email(to: str) -> str:
    """Send an email.

    Args:
        to: recipient
    """
    return f"sent to {to}"


def test_adk_tool_manifest_from_functions_and_tools():
    """The tools inside with_approval are listed with their scopes; other toolsets give their tools per run."""
    scoped = adk.with_scopes([lambda_free], {"lambda_free": ["tool:math:double"]})
    infos = adk.tool_infos([adk_send_email, *adk.with_approval(scoped), adk.ask_user_tool()])
    assert infos == [
        ToolInfo("adk_send_email", "Send an email."),
        ToolInfo("lambda_free", "Double it.", ["tool:math:double"]),
    ]


async def test_adk_approval_toolset_guards_the_chosen_tools():
    [toolset] = adk.with_approval([adk_send_email, lambda_free], names=["adk_send_email"], message="Real email.")
    tools = await toolset.get_tools()
    assert [type(t).__name__ for t in tools] == ["_GuardedTool", "FunctionTool"]
    assert tools[0].name == "adk_send_email" and tools[0]._get_declaration().name == "adk_send_email"

    async def handler(ctx):
        token = _current.set(ctx)
        try:
            first = await tools[0].run_async(args={"to": "alice@example.com"}, tool_context=SimpleNamespace())
            second = await tools[0].run_async(args={"to": "bob@example.com"}, tool_context=SimpleNamespace())
        finally:
            _current.reset(token)
        return json.dumps([first, second])

    agent, transport, _ = await start(handler)
    await transport.deliver("T1")
    await approve_next(transport, approved=True)
    await approve_next(transport, index=1, approved=False)
    first, second = json.loads(await final_text(transport))
    assert first == "sent to alice@example.com"
    assert second == {"error": "The user did not approve this call to adk_send_email (denied), so it was not run."}
    await agent.stop()


def lambda_free(x: int) -> int:
    """Double it."""
    return x * 2


async def test_adk_guarded_tools_fail_closed_outside_a_mesh_task():
    [guarded] = await adk.with_approval([adk_send_email])[0].get_tools()
    result = await guarded.run_async(args={"to": "alice@example.com"}, tool_context=SimpleNamespace())
    assert "unavailable" in result["error"]


async def test_adk_ask_user_toolset():
    [tool] = await adk.ask_user_tool().get_tools()
    assert isinstance(tool, BaseTool) and tool._get_declaration().parameters.required == ["question"]

    async def handler(ctx):
        token = _current.set(ctx)
        try:
            return await tool.run_async(
                args={"question": "Queue?", "options": ["a", "b"]}, tool_context=SimpleNamespace()
            )
        finally:
            _current.reset(token)

    agent, transport, _ = await start(handler)
    await transport.deliver("T1")
    await transport.answer(await prompt_number(transport, 0), answers=("a",))
    assert await final_text(transport) == "a"
    await agent.stop()


async def test_adk_reasoning_parts_stream_and_stay_out_of_the_answer():
    event = SimpleNamespace(
        content=types.Content(
            role="model", parts=[types.Part(text="weigh the tiers", thought=True), types.Part(text="Fast-track.")]
        ),
        partial=False,
        usage_metadata=None,
    )
    async with adapter_task(text="hi", stream_thinking=True) as task:
        plugin, token = SamLinkPlugin(), _current.set(task.ctx)
        try:
            await plugin.on_event_callback(invocation_context=SimpleNamespace(invocation_id="inv-1"), event=event)
        finally:
            _current.reset(token)
        sent = task.events
    assert [type(e) for e in sent] == [Thinking, Thinking, ModelFinished]
    assert sent[0].text == "weigh the tiers"
    assert (sent[2].text, sent[2].tool_calls) == ("Fast-track.", ())


async def test_adk_request_context_is_appended_to_the_model_request():
    plugin = SamLinkPlugin()
    request = LlmRequest(model="gemini-2.0-flash")
    async with adapter_task(text="hi") as task:
        token, prompt = _current.set(task.ctx), _request_prompt.set("## System Purpose\n\nHelp with claims.")
        try:
            await plugin.before_model_callback(
                callback_context=SimpleNamespace(invocation_id="inv-2"), llm_request=request
            )
        finally:
            _request_prompt.reset(prompt)
            _current.reset(token)
    assert "Help with claims." in str(request.config.system_instruction)


pytest.importorskip("crewai")

from crewai.tools import tool as crew_tool  # noqa: E402

from sam_link_sdk import crewai as crew  # noqa: E402


@crew_tool("crew_send_email")
def crew_send_email(to: str) -> str:
    """Send an email."""
    return f"sent to {to}"


def test_crewai_tool_manifest():
    assert crew.tool_infos([crew_send_email]) == [ToolInfo("crew_send_email", crew_send_email.description)]


async def test_crewai_approval_blocks_the_crew_thread_until_the_user_answers():
    async def handler(ctx):
        [guarded] = crew.with_approval(ctx, [crew_send_email], message="Real email.")
        assert guarded.name == "crew_send_email" and guarded.description == crew_send_email.description
        first = await asyncio.to_thread(guarded.run, to="alice@example.com")
        second = await asyncio.to_thread(guarded.run, to="bob@example.com")
        return json.dumps([first, second])

    agent, transport, events = await start(handler)
    await transport.deliver("T1")
    await approve_next(transport, approved=True)
    await approve_next(transport, index=1, approved=False)

    first, second = json.loads(await final_text(transport))
    assert first == "sent to alice@example.com"
    assert json.loads(second) == {
        "error": "The user did not approve this call to crew_send_email (denied), so it was not run."
    }
    assert [e.message for e in events] == ["tool approval granted", "tool approval refused"]
    await agent.stop()


async def test_crewai_ask_user_tool():
    async def handler(ctx):
        tool = crew.ask_user_tool(ctx)
        return await asyncio.to_thread(tool.run, question="Queue?", options=["a", "b"])

    agent, transport, _ = await start(handler)
    await transport.deliver("T1")
    await transport.answer(await prompt_number(transport, 0), answers=("b",))
    assert await final_text(transport) == "b"
    await agent.stop()


async def test_crewai_request_context_goes_into_the_inputs_when_asked():
    seen = []

    class FakeCrew:
        def kickoff(self, inputs):
            seen.append(inputs)
            return SimpleNamespace(raw="done")

    async with adapter_task(text="hi", request_context=RequestContext(system_purpose="Help with claims.")) as task:
        await crew.run_agent(task.ctx, lambda _: FakeCrew(), include_request_context=True)
        await crew.run_agent(task.ctx, lambda _: FakeCrew())
    assert seen[0] == {"request": "hi", "request_context": "## System Purpose\n\nHelp with claims."}
    assert seen[1] == {"request": "hi"}


pytest.importorskip("langgraph")

from langchain_core.messages import AIMessage  # noqa: E402
from langchain_core.tools import tool as lc_tool  # noqa: E402
from langgraph.graph import START, MessagesState, StateGraph  # noqa: E402
from langgraph.prebuilt import ToolNode  # noqa: E402

from sam_link_sdk.langchain import with_approval  # noqa: E402


async def test_langgraph_toolnode_runs_approval_guarded_langchain_tools():
    @lc_tool
    def send_email(to: str) -> str:
        """Send an email."""
        return f"sent to {to}"

    async def handler(ctx):
        graph = StateGraph(MessagesState)
        graph.add_node("tools", ToolNode(with_approval(ctx, [send_email])))
        graph.add_edge(START, "tools")
        graph = graph.compile()
        calls = [
            {"name": "send_email", "args": {"to": "alice@example.com"}, "id": "c1", "type": "tool_call"},
            {"name": "send_email", "args": {"to": "bob@example.com"}, "id": "c2", "type": "tool_call"},
        ]
        first = await graph.ainvoke({"messages": [AIMessage(content="", tool_calls=calls[:1])]})
        second = await graph.ainvoke({"messages": [AIMessage(content="", tool_calls=calls[1:])]})
        return json.dumps(
            [first["messages"][-1].content, second["messages"][-1].content, second["messages"][-1].status]
        )

    agent, transport, _ = await start(handler)
    await transport.deliver("T1")
    await approve_next(transport, approved=True)
    await approve_next(transport, index=1, approved=False)
    first, second, status = json.loads(await final_text(transport))
    assert first == "sent to alice@example.com"
    assert "did not approve this call to send_email (denied)" in second and status == "error"
    await agent.stop()
