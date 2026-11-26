# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""CrewAI adapter, fed by real CrewAI events on the real (thread-pool) event bus from a fake crew."""

from __future__ import annotations

import asyncio
import time
from datetime import UTC, datetime
from types import SimpleNamespace

import pytest

pytest.importorskip("crewai")

from crewai.events import crewai_event_bus
from crewai.events.types.llm_events import LLMCallCompletedEvent, LLMCallStartedEvent, LLMCallType
from crewai.events.types.tool_usage_events import ToolUsageErrorEvent, ToolUsageFinishedEvent, ToolUsageStartedEvent

from sam_link_sdk.crewai import run_agent
from sam_link_sdk.events import ToolCall, ToolFinished, ToolStarted

from .adapter_task import adapter_task, steps


def now():
    return datetime.now(UTC)


class FakeCrew:
    """Emits the event sequence a native-tool-calling CrewAI run produces, from the kickoff thread."""

    def __init__(self, script, answer):
        self.script, self.answer = script, answer

    def kickoff(self, inputs=None):
        self.inputs = inputs
        for event in self.script():
            crewai_event_bus.emit(self, event)
        return SimpleNamespace(raw=self.answer)


def llm_start(call_id, model="claude-sonnet-5"):
    return LLMCallStartedEvent(call_id=call_id, model=model, messages=None)


def llm_end(call_id, response, call_type, usage, model="claude-sonnet-5"):
    return LLMCallCompletedEvent(call_id=call_id, model=model, response=response, call_type=call_type, usage=usage)


def tool_start(name, args):
    return ToolUsageStartedEvent(tool_name=name, tool_args=args)


def tool_end(name, args, output):
    return ToolUsageFinishedEvent(tool_name=name, tool_args=args, started_at=now(), finished_at=now(), output=output)


def two_tool_script():
    anthropic_block = SimpleNamespace(id="toolu_a", name="lookup_policy", input={"policy_id": "P-1"})
    openai_call = {
        "id": "call_b",
        "type": "function",
        "function": {"name": "lookup_policy", "arguments": '{"policy_id": "P-2"}'},
    }
    return [
        llm_start("L1"),
        llm_end(
            "L1",
            [anthropic_block, openai_call],
            LLMCallType.TOOL_CALL,
            {
                "input_tokens": 561,
                "output_tokens": 69,
                "total_tokens": 630,
                "cached_prompt_tokens": 10,
                "cache_creation_tokens": 4,
            },
        ),
        tool_start("lookup_policy", {"policy_id": "P-1"}),
        tool_end("lookup_policy", {"policy_id": "P-1"}, '{"tier": "gold"}'),
        tool_start("lookup_policy", {"policy_id": "P-2"}),
        tool_end("lookup_policy", {"policy_id": "P-2"}, '{"tier": "silver"}'),
        llm_start("L2"),
        llm_end(
            "L2",
            "P-1 is gold, P-2 is silver.",
            LLMCallType.LLM_CALL,
            {"input_tokens": 641, "output_tokens": 19, "total_tokens": 660},
        ),
    ]


async def test_events_are_ordered_paired_and_counted():
    crew = FakeCrew(two_tool_script, "P-1 is gold, P-2 is silver.")
    async with adapter_task() as task:
        assert await run_agent(task.ctx, lambda _: crew) == "P-1 is gold, P-2 is silver."
        sent = steps(task)
        tu = task.ctx._run.usage.final_token_usage()
    assert crew.inputs == {"request": "Triage claims P-1 and P-2"}

    assert [e.kind for e in sent] == [
        "model_started",
        "model_finished",
        "tool_started",
        "tool_finished",
        "tool_started",
        "tool_finished",
        "model_started",
        "model_finished",
    ]
    assert sent[1].tool_calls == (
        ToolCall("toolu_a", "lookup_policy", {"policy_id": "P-1"}),
        ToolCall("call_b", "lookup_policy", {"policy_id": "P-2"}),
    )
    assert [e.call_id for e in sent if isinstance(e, ToolStarted)] == ["toolu_a", "call_b"]
    assert [e.call_id for e in sent if isinstance(e, ToolFinished)] == ["toolu_a", "call_b"]
    assert [e.result for e in sent if isinstance(e, ToolFinished)] == [{"tier": "gold"}, {"tier": "silver"}]
    assert (sent[1].usage.cached_input_tokens, sent[1].usage.cache_write_tokens) == (10, 4)
    assert (sent[7].text, sent[7].tool_calls) == ("P-1 is gold, P-2 is silver.", ())

    assert (tu["total_input_tokens"], tu["total_output_tokens"], tu["total_cache_write_tokens"]) == (1202, 88, 4)
    assert list(tu["by_model"]) == ["claude-sonnet-5"]


async def test_kickoff_inputs_of_your_own_still_carry_the_users_request():
    """The example passes no inputs of its own, so no end-to-end run shows what the crew is kicked off with."""
    crew = FakeCrew(two_tool_script, "done")
    async with adapter_task(text="Triage P-1") as task:
        await run_agent(task.ctx, lambda _: crew, kickoff_inputs={"tier": "gold"})
    assert crew.inputs == {"tier": "gold", "request": "Triage P-1"}


async def test_a_crew_object_is_refused_before_it_is_kicked_off():
    """A kickoff fills a crew's task templates in place, so two tasks sharing one crew can run on each other's
    request. Agent Mesh cannot see which crew a handler passes, and the example builds one per task, so this is shown
    here: one crew object is refused for every task, and never kicked off."""
    crew = FakeCrew(two_tool_script, "done")
    async with adapter_task("T1") as t1, adapter_task("T2") as t2:
        for ctx in (t1.ctx, t2.ctx):
            with pytest.raises(TypeError, match="takes build"):
                await run_agent(ctx, crew)
    assert not hasattr(crew, "inputs")


class FailingCrew(FakeCrew):
    def kickoff(self, inputs=None):
        for event in self.script():
            crewai_event_bus.emit(self, event)
        raise ValueError("the crew broke")


def pump_tasks():
    return [t for t in asyncio.all_tasks() if t.get_name().startswith("sam-link-sdk-crewai-") and not t.done()]


async def test_a_kickoff_that_fails_closes_the_event_pump_and_still_reports_what_the_crew_did():
    """The pump is an asyncio task of the process, so SAM sees a leak of it only as memory; it is read here. A crew that
    fails after its first steps must have those steps reported, and leave nothing running."""
    async with adapter_task() as task:
        with pytest.raises(ValueError, match="the crew broke"):
            await run_agent(task.ctx, lambda _: FailingCrew(two_tool_script, ""))
        sent = steps(task)
        assert pump_tasks() == []
    assert [e.kind for e in sent].count("tool_finished") == 2


class WithTemplates(FakeCrew):
    def __init__(self, description, **kwargs):
        super().__init__(two_tool_script, "done")
        self.tasks = [SimpleNamespace(description=description, expected_output="", agent=None)]
        self.agents = [SimpleNamespace(role="triager", goal="route", backstory=kwargs.get("backstory", ""))]


async def test_the_conversation_and_the_request_context_reach_a_crew_whose_templates_do_not_name_them():
    """CrewAI fills in only the placeholders the templates have, so a crew without them was told nothing of the earlier
    turns. A template that names a placeholder keeps its own place for it."""
    from sam_link_sdk.memory import ConversationMemory
    from sam_link_sdk.request_context import RequestContext

    memory = ConversationMemory()
    memory.remember("S", [], "Earlier question", "Earlier answer")
    plain = WithTemplates("Handle {request}")
    named = WithTemplates("Handle {request} given {history}", backstory="Context: {request_context}")
    async with adapter_task(
        context_id="S", request_context=RequestContext(system_purpose="Answer as the claims desk.")
    ) as task:
        for crew in (plain, named):
            await run_agent(task.ctx, lambda _, crew=crew: crew, memory=memory, include_request_context=True)
    assert plain.tasks[0].description.startswith("Conversation so far:\n{history}\n\n")
    assert "{request_context}" in plain.tasks[0].description
    assert plain.tasks[0].description.endswith("Handle {request}")
    assert named.tasks[0].description == "Handle {request} given {history}"


async def test_a_tool_finished_under_its_sanitized_name_is_reported_under_the_name_it_started_with():
    """CrewAI's text (ReAct) tool calling starts a tool under the name the model wrote and finishes it under
    sanitize_tool_name's spelling: a peer tool such as peer_records-lookup finishes as peer_records_lookup."""

    def script():
        return [
            llm_start("L1"),
            llm_end("L1", "Action: peer_records-lookup", LLMCallType.LLM_CALL, {"input_tokens": 5, "output_tokens": 1}),
            tool_start("peer_records-lookup", {"task_description": "Earlier claims on P-3301"}),
            tool_end("peer_records_lookup", {"task_description": "Earlier claims on P-3301"}, "Two earlier claims."),
        ]

    async with adapter_task() as task:
        assert await run_agent(task.ctx, lambda _: FakeCrew(script, "done")) == "done"
        sent = steps(task)
    [start] = [e for e in sent if isinstance(e, ToolStarted)]
    [end] = [e for e in sent if isinstance(e, ToolFinished)]
    assert (start.name, end.name) == ("peer_records-lookup", "peer_records-lookup")
    assert start.call_id == end.call_id
    assert end.result == "Two earlier claims."


async def test_tool_error_and_concurrent_crews_route_to_their_own_task():
    def failing():
        return [
            llm_start("E1"),
            llm_end(
                "E1",
                [SimpleNamespace(id="toolu_x", name="broken", input={})],
                LLMCallType.TOOL_CALL,
                {"input_tokens": 5, "output_tokens": 1},
            ),
            tool_start("broken", {}),
            ToolUsageErrorEvent(tool_name="broken", tool_args={}, error="backend down"),
        ]

    async with adapter_task("T1") as t1, adapter_task("T2") as t2:
        a1, a2 = await asyncio.gather(
            run_agent(t1.ctx, lambda _: FakeCrew(two_tool_script, "one")),
            run_agent(t2.ctx, lambda _: FakeCrew(failing, "two")),
        )
        s1, s2 = steps(t1), steps(t2)
    assert (a1, a2) == ("one", "two")
    assert len(s1) == 8
    assert [e.kind for e in s2] == ["model_started", "model_finished", "tool_started", "tool_finished"]
    assert s2[3] == ToolFinished("toolu_x", "broken", {"error": "backend down"}, failed=True)


class ParallelCrew:
    """Runs a batch of native tool calls the way CrewAI does: each on a pool thread in a copy of the kickoff's context
    (its own event scope). `finish_order` forces the order in which the calls finish."""

    def __init__(self, before, batch, finish_order, after, answer="done"):
        self.before, self.batch, self.finish_order, self.after, self.answer = before, batch, finish_order, after, answer

    def kickoff(self, inputs=None):
        import contextvars
        import threading
        from concurrent.futures import ThreadPoolExecutor

        for event in self.before:
            crewai_event_bus.emit(self, event)
        started = [threading.Event() for _ in self.batch]
        turn = [threading.Event() for _ in self.batch]

        def run(i, name, args, output):
            crewai_event_bus.emit(self, tool_start(name, args))
            started[i].set()
            turn[i].wait(5)
            crewai_event_bus.emit(self, tool_end(name, args, output))

        with ThreadPoolExecutor(len(self.batch)) as pool:
            futures = []
            for i, (name, args, output) in enumerate(self.batch):
                futures.append(pool.submit(contextvars.copy_context().run, run, i, name, args, output))
                started[i].wait(5)
            for i in self.finish_order:
                turn[i].set()
                futures[i].result(5)
        crewai_event_bus.flush(5.0)
        for event in self.after:
            crewai_event_bus.emit(self, event)
        return SimpleNamespace(raw=self.answer)


def pairs(sent):
    starts = {e.call_id: e.args for e in sent if isinstance(e, ToolStarted)}
    return {e.call_id: (starts.get(e.call_id), e.result) for e in sent if isinstance(e, ToolFinished)}


async def crew_steps(crew):
    """The steps a run of ``crew`` reports to its task."""
    async with adapter_task() as task:
        await run_agent(task.ctx, lambda _: crew)
        return steps(task)


async def test_parallel_calls_to_one_tool_pair_by_their_start_event_when_they_finish_out_of_order():
    calls = [
        SimpleNamespace(id="toolu_a", name="lookup_policy", input={"policy_id": "P-1"}),
        SimpleNamespace(id="toolu_b", name="lookup_policy", input={"policy_id": "P-2"}),
    ]
    crew = ParallelCrew(
        before=[llm_start("L1"), llm_end("L1", calls, LLMCallType.TOOL_CALL, {"input_tokens": 5, "output_tokens": 1})],
        batch=[
            ("lookup_policy", {"policy_id": "P-1"}, '{"tier": "gold"}'),
            ("lookup_policy", {"policy_id": "P-2"}, '{"tier": "silver"}'),
        ],
        finish_order=[1, 0],
        after=[llm_start("L2"), llm_end("L2", "done", LLMCallType.LLM_CALL, {"input_tokens": 6, "output_tokens": 1})],
    )
    async with adapter_task() as task:
        assert await run_agent(task.ctx, lambda _: crew) == "done"
        sent = steps(task)
    assert [e.call_id for e in sent if isinstance(e, ToolFinished)] == ["toolu_b", "toolu_a"]
    assert pairs(sent) == {
        "toolu_a": ({"policy_id": "P-1"}, {"tier": "gold"}),
        "toolu_b": ({"policy_id": "P-2"}, {"tier": "silver"}),
    }


async def test_a_tool_start_takes_the_model_call_with_its_arguments_whatever_order_the_starts_arrive_in():
    calls = [
        SimpleNamespace(id="toolu_a", name="lookup_policy", input={"policy_id": "P-1"}),
        SimpleNamespace(id="toolu_b", name="lookup_policy", input={"policy_id": "P-2"}),
    ]
    crew = ParallelCrew(
        before=[llm_start("L1"), llm_end("L1", calls, LLMCallType.TOOL_CALL, {"input_tokens": 5, "output_tokens": 1})],
        batch=[
            ("lookup_policy", {"policy_id": "P-2"}, '{"tier": "silver"}'),
            ("lookup_policy", {"policy_id": "P-1"}, '{"tier": "gold"}'),
        ],
        finish_order=[0, 1],
        after=[],
    )
    sent = await crew_steps(crew)
    assert [e.call_id for e in sent if isinstance(e, ToolStarted)] == ["toolu_b", "toolu_a"]
    assert pairs(sent) == {
        "toolu_a": ({"policy_id": "P-1"}, {"tier": "gold"}),
        "toolu_b": ({"policy_id": "P-2"}, {"tier": "silver"}),
    }


async def test_repeated_identical_calls_pair_deterministically_in_start_order():
    calls = [SimpleNamespace(id=f"toolu_{i}", name="lookup_policy", input={"policy_id": "P-1"}) for i in (1, 2, 3)]

    def script():
        events = [llm_start("L1"), llm_end("L1", calls, LLMCallType.TOOL_CALL, {"input_tokens": 5, "output_tokens": 1})]
        for output in ("first", "second", "third"):
            events += [
                tool_start("lookup_policy", {"policy_id": "P-1"}),
                tool_end("lookup_policy", {"policy_id": "P-1"}, output),
            ]
        return events

    for _ in range(3):
        sent = await crew_steps(FakeCrew(script, "done"))
        assert [(cid, data) for cid, (_, data) in pairs(sent).items()] == [
            ("toolu_1", "first"),
            ("toolu_2", "second"),
            ("toolu_3", "third"),
        ]


async def test_a_call_crewai_never_ran_does_not_lend_its_id_to_a_later_call():
    """CrewAI runs only the first call of a batch that includes a result_as_answer or max_usage_count tool, then asks
    the model again. The next completion's call must keep its own id."""
    first = [
        SimpleNamespace(id="toolu_ran", name="lookup_policy", input={"policy_id": "P-1"}),
        SimpleNamespace(id="toolu_never_ran", name="lookup_policy", input={"policy_id": "P-2"}),
    ]
    second = [SimpleNamespace(id="toolu_next", name="lookup_policy", input={"policy_id": "P-2"})]

    def script():
        return [
            llm_start("L1"),
            llm_end("L1", first, LLMCallType.TOOL_CALL, {"input_tokens": 5, "output_tokens": 1}),
            tool_start("lookup_policy", {"policy_id": "P-1"}),
            tool_end("lookup_policy", {"policy_id": "P-1"}, '"gold"'),
            llm_start("L2"),
            llm_end("L2", second, LLMCallType.TOOL_CALL, {"input_tokens": 5, "output_tokens": 1}),
            tool_start("lookup_policy", {"policy_id": "P-2"}),
            tool_end("lookup_policy", {"policy_id": "P-2"}, '"silver"'),
        ]

    assert list(pairs(await crew_steps(FakeCrew(script, "done")))) == ["toolu_ran", "toolu_next"]


async def test_a_failure_the_tool_declared_is_reported_as_failed_with_what_the_agent_saw():
    from crewai.tools.tool_failure import ToolFailure

    def script():
        return [
            llm_start("L1"),
            llm_end(
                "L1",
                [SimpleNamespace(id="toolu_x", name="post_message", input={})],
                LLMCallType.TOOL_CALL,
                {"input_tokens": 5, "output_tokens": 1},
            ),
            tool_start("post_message", {}),
            ToolUsageFinishedEvent(
                tool_name="post_message",
                tool_args={},
                started_at=now(),
                finished_at=now(),
                output="channel_not_found (code: channel_not_found)",
                failure=ToolFailure(message="channel_not_found", code="channel_not_found"),
            ),
        ]

    result = [e for e in await crew_steps(FakeCrew(script, "done")) if isinstance(e, ToolFinished)][0]
    assert result.result == {"error": "channel_not_found (code: channel_not_found)"}
    assert result.call_id == "toolu_x"


class HookedCrew:
    """A one-tool crew as CrewAI runs it: the agent's LLM is called before each model-call event, and CrewAI's tool
    hooks run around the tool, on the kickoff thread."""

    def __init__(self):
        self.agent = SimpleNamespace(llm=SimpleNamespace(call=lambda messages, **kwargs: ""), function_calling_llm=None)
        self.agents = [self.agent]

    def kickoff(self, inputs=None):
        from crewai.hooks import ToolCallHookContext
        from crewai.hooks.tool_hooks import run_after_tool_call_hooks, run_before_tool_call_hooks

        args = {"policy_id": "P-1"}
        self.agent.llm.call([])
        crewai_event_bus.emit(self, llm_start("L1"))
        crewai_event_bus.emit(
            self,
            llm_end(
                "L1",
                [SimpleNamespace(id="toolu_a", name="lookup_policy", input=args)],
                LLMCallType.TOOL_CALL,
                {"input_tokens": 5, "output_tokens": 1},
            ),
        )
        crewai_event_bus.emit(self, tool_start("lookup_policy", args))
        run_before_tool_call_hooks(ToolCallHookContext(tool_name="lookup_policy", tool_input=dict(args), tool=None))
        run_after_tool_call_hooks(
            ToolCallHookContext(tool_name="lookup_policy", tool_input=dict(args), tool=None, tool_result="gold")
        )
        crewai_event_bus.emit(self, tool_end("lookup_policy", args, "gold"))
        self.agent.llm.call([])
        crewai_event_bus.emit(self, llm_start("L2"))
        crewai_event_bus.emit(
            self, llm_end("L2", "P-1 is gold.", LLMCallType.LLM_CALL, {"input_tokens": 9, "output_tokens": 3})
        )
        return SimpleNamespace(raw="P-1 is gold.")


async def test_a_handler_thread_that_delivers_an_event_late_does_not_reorder_the_steps(monkeypatch):
    from sam_link_sdk.crewai import steps as crewai_steps

    capture = crewai_steps._Run.capture

    def slow_capture(self, event):
        if isinstance(event, ToolUsageFinishedEvent):
            time.sleep(10 * crewai_steps.REORDER_WINDOW_SECONDS)
        capture(self, event)

    monkeypatch.setattr(crewai_steps._Run, "capture", slow_capture)

    sent = await crew_steps(HookedCrew())

    assert [e.kind for e in sent] == [
        "model_started",
        "model_finished",
        "tool_started",
        "tool_finished",
        "model_started",
        "model_finished",
    ]


class DeniedToolCrew:
    """A one-tool crew whose tool the user may not use, as CrewAI runs it: the start event comes before the hooks, the
    before hook refuses the call, and the tool's end event carries the text the after hook returned."""

    def __init__(self):
        self.agent = SimpleNamespace(llm=SimpleNamespace(call=lambda messages, **kwargs: ""), function_calling_llm=None)
        self.agents = [self.agent]

    def kickoff(self, inputs=None):
        from crewai.hooks import ToolCallHookContext
        from crewai.hooks.tool_hooks import run_after_tool_call_hooks, run_before_tool_call_hooks

        args = {"claim_id": "C-1"}
        self.agent.llm.call([])
        crewai_event_bus.emit(self, llm_start("L1"))
        crewai_event_bus.emit(
            self,
            llm_end(
                "L1",
                [SimpleNamespace(id="toolu_r", name="route_claim", input=args)],
                LLMCallType.TOOL_CALL,
                {"input_tokens": 5, "output_tokens": 1},
            ),
        )
        crewai_event_bus.emit(self, tool_start("route_claim", args))
        blocked = run_before_tool_call_hooks(
            ToolCallHookContext(tool_name="route_claim", tool_input=dict(args), tool=None)
        )
        assert blocked
        refusal = run_after_tool_call_hooks(
            ToolCallHookContext(
                tool_name="route_claim",
                tool_input=dict(args),
                tool=None,
                tool_result="Tool execution blocked by hook. Tool: route_claim",
            )
        )
        crewai_event_bus.emit(self, tool_end("route_claim", args, refusal))
        self.agent.llm.call([])
        crewai_event_bus.emit(self, llm_start("L2"))
        crewai_event_bus.emit(
            self,
            llm_end("L2", "I may not route claims.", LLMCallType.LLM_CALL, {"input_tokens": 9, "output_tokens": 3}),
        )
        return SimpleNamespace(raw="I may not route claims.")


async def test_a_refused_call_whose_start_was_handled_before_the_hook_ran_is_one_start_and_one_end(monkeypatch):
    from sam_link_sdk.crewai import steps as crewai_steps

    refuses = crewai_steps._Run.refuses

    def late_refuses(self, *names):
        time.sleep(10 * crewai_steps.REORDER_WINDOW_SECONDS)
        return refuses(self, *names)

    monkeypatch.setattr(crewai_steps._Run, "refuses", late_refuses)

    async with adapter_task(scopes={"route_claim": ["tool:claims:route"]}, granted=[]) as task:
        await run_agent(task.ctx, lambda _: DeniedToolCrew())
        sent = steps(task)

    assert [e.kind for e in sent] == [
        "model_started",
        "model_finished",
        "tool_started",
        "tool_finished",
        "model_started",
        "model_finished",
    ]
    started, finished = (next(e for e in sent if isinstance(e, kind)) for kind in (ToolStarted, ToolFinished))
    assert started.call_id == finished.call_id == "toolu_r"
    assert finished.result["error"].startswith("Access denied")
