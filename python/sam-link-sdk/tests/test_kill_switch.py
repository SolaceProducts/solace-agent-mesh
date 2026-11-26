# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""SAM's stop: SAM stops and releases an SDK agent (on the agent link, the entrypoint's ``control``), and the core
keeps it: running tasks stop and are answered, new ones are refused, the agent's own entry points refuse work, the stop
is saved for a restart, and listeners hear each change."""

from __future__ import annotations

import asyncio
import time

import pytest

from sam_link_sdk import AgentConfig, AgentStopped, MeshAgent
from sam_link_sdk._model import IncomingTask, PeerResult, PromptAnswer
from sam_link_sdk.events import ModelStarted
from sam_link_sdk.stop import stop_state_path

from .agent_rig import RecordingTransport, settle, start_agent

NAME = "claims-triage"


def cfg(state_dir, **kw):
    return AgentConfig(name=NAME, description="d", keepalive_seconds=0, state_dir=str(state_dir), **kw)


async def test_a_stop_stops_the_agent_everywhere_and_it_stays_stopped_after_a_restart(tmp_path):
    records = []
    agent, transport = await start_agent(lambda ctx: "ok", state_dir=str(tmp_path), audit_sink=records.append)

    await transport.control(False, "stopped_by_sam")
    assert not agent.allowed
    with pytest.raises(AgentStopped) as stopped:
        agent.check()
    assert stopped.value.reason == "stopped_by_sam"

    @agent.guard
    def local_entry_point():
        return "ran"

    with pytest.raises(AgentStopped):
        local_entry_point()

    await transport.deliver("T1")
    assert transport.finished["T1"].state == "failed"
    assert transport.finished["T1"].text == "Agent stopped: SAM has stopped this agent"
    stopped = [r for r in records if r.message == "agent stopped by SAM"]
    assert len(stopped) == 1 and dict(stopped[0].fields) == {"agentName": NAME, "reason": "stopped_by_sam"}
    await agent.stop()

    restarted, _ = await start_agent(lambda ctx: "ok", state_dir=str(tmp_path))
    assert not restarted.allowed
    await restarted.stop()


async def test_a_release_lets_the_agent_run_again_and_is_audited(tmp_path):
    records = []
    agent, transport = await start_agent(lambda ctx: "ok", state_dir=str(tmp_path), audit_sink=records.append)
    await transport.control(False, "stopped_by_sam")
    await transport.control(True, "released_by_sam")
    assert agent.allowed
    await transport.deliver("T1")
    assert transport.finished["T1"].state == "completed"
    assert [(r.message, r.fields.get("reason")) for r in records] == [
        ("agent stopped by SAM", "stopped_by_sam"),
        ("agent released by SAM", "released_by_sam"),
    ]
    await agent.stop()


async def test_a_running_task_is_stopped_and_answered(tmp_path):
    gate = asyncio.Event()

    async def slow(ctx):
        await gate.wait()
        return "finished"

    agent, transport = await start_agent(slow, state_dir=str(tmp_path))
    await transport.deliver("T2")
    await transport.control(False, "stopped_by_sam")
    answer = transport.finished["T2"]
    assert answer.state == "failed"
    assert "an operator stopped this agent" in answer.text
    await agent.stop()


async def test_exit_on_stop_ends_run_forever(tmp_path):
    agent, transport = await start_agent(lambda ctx: "ok", state_dir=str(tmp_path), exit_on_stop=True)
    await transport.control(False, "stopped_by_sam")
    assert agent._lifecycle.exit.is_set()
    await agent.stop()


async def test_guard_works_on_async_functions_and_listeners_hear_the_change(tmp_path):
    heard = []
    agent, transport = await start_agent(lambda ctx: "ok", state_dir=str(tmp_path))
    agent.on_stop_change(lambda allowed, reason: heard.append((allowed, reason)))

    @agent.guard
    async def answer():
        return "ran"

    assert await answer() == "ran"
    await transport.control(False, "stopped_by_sam")
    with pytest.raises(AgentStopped):
        await answer()
    await transport.control(True, "released_by_sam")
    assert await answer() == "ran"
    assert heard == [(False, "stopped_by_sam"), (True, "released_by_sam")]
    await agent.stop()


async def test_an_unreadable_state_file_keeps_the_agent_stopped(tmp_path):
    path = stop_state_path(cfg(tmp_path), RecordingTransport._stop_key)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text("{not json")
    agent, _ = await start_agent(lambda ctx: "ok", state_dir=str(tmp_path))
    assert not agent.allowed
    await agent.stop()


class _RecordingTransport:
    """The smallest transport: it records what the agent answers."""

    _manages_listing = True

    def __init__(self):
        self.finished: dict[str, object] = {}

    def _bind(self, host):
        self.host = host

    async def _open(self, *, serving=True, wait_for_peers=True):
        pass

    async def _consume(self, watch=None):
        pass

    async def _connect(self):
        pass

    async def _stop_background(self):
        pass

    async def _flush(self):
        pass

    async def _disconnect(self):
        pass

    async def _publish_description(self):
        pass

    async def _withdraw_description(self):
        pass

    async def _send_event(self, task_id, event):
        return True

    async def _ask(self, task_id, prompt):
        pass

    async def _finish(self, task_id, outcome):
        self.finished[task_id] = outcome

    async def _call_peer(self, call):
        pass

    def _peers(self):
        return []

    def _usage_sink(self, task_id):
        return None


async def test_a_task_stopped_before_its_first_step_is_answered_and_forgotten(tmp_path):
    from sam_link_sdk._model import IncomingTask

    transport = _RecordingTransport()
    agent = MeshAgent(cfg(tmp_path), lambda ctx: "ok", transport=transport)
    await agent._runner.on_task(IncomingTask(task_id="T1"))
    agent._stopper.on_control(False, "stopped_by_sam")
    await settle()

    assert transport.finished["T1"].state == "failed"
    assert "an operator stopped this agent" in transport.finished["T1"].text
    assert agent._runner.tasks == {}


async def test_a_stop_announced_just_before_shutdown_still_reaches_the_listeners(tmp_path):
    heard = []

    async def listener(allowed, reason):
        await asyncio.sleep(0.05)
        heard.append((allowed, reason))

    agent = MeshAgent(cfg(tmp_path), lambda ctx: "ok", transport=_RecordingTransport())
    agent.on_stop_change(listener)
    agent._stopper.on_control(False, "stopped_by_sam")
    await agent.stop()
    assert heard == [(False, "stopped_by_sam")]


async def test_work_left_running_after_the_answer_is_refused_and_stops_once_sam_stops_the_agent(tmp_path, caplog):
    from sam_link_sdk.context import TaskHalted, current_task

    later, seen = asyncio.Event(), {}

    async def leftover():
        await later.wait()
        ctx = seen["ctx"] = current_task()
        try:
            ctx.check_running()
        except TaskHalted as exc:
            seen["after answer"] = exc.reason
        seen["asked"] = (await ctx.ask_user("Proceed with claim C-1?")).status
        seen["called"] = (await ctx.call_agent("policy-desk", "Check policy P-1")).state
        await later.wait()

    async def handler(ctx):
        seen["work"] = asyncio.ensure_future(leftover())
        return "Claim C-1 is approved."

    transport = _RecordingTransport()
    agent = MeshAgent(cfg(tmp_path), handler, transport=transport)
    with caplog.at_level("DEBUG", logger="sam_link_sdk.task"):
        await agent._runner.on_task(IncomingTask(task_id="T1"))
        await settle()
        assert transport.finished["T1"].state == "completed"
        later.set()
        await settle()
    assert seen["after answer"] == "answered"
    assert (seen["asked"], seen["called"]) == ("cancelled", "error")
    warned = [r for r in caplog.records if "no longer held the task" in r.getMessage()]
    assert [r.levelname for r in warned] == ["WARNING", "DEBUG"]

    agent._stopper.on_control(False, "stopped_by_sam")
    leftover_ctx = seen["ctx"]
    assert leftover_ctx._run.halt_reason == "stopped_by_sam"
    with pytest.raises(AgentStopped):
        leftover_ctx.check_running()
    seen["work"].cancel()
    await agent.stop()


async def test_a_task_that_reaches_the_core_while_the_agent_is_stopped_is_refused_without_running(tmp_path):
    ran = []
    transport = _RecordingTransport()
    agent = MeshAgent(cfg(tmp_path), lambda ctx: ran.append(ctx.task_id) or "ok", transport=transport)
    agent._stopper.on_control(False, "stopped_by_sam")
    await agent._runner.on_task(IncomingTask(task_id="T1"))
    await settle()
    assert ran == [] and transport.finished["T1"].state == "failed"
    assert transport.finished["T1"].text == "Agent stopped: SAM has stopped this agent"
    await agent.stop()


async def test_a_task_stopped_while_running_is_answered_before_a_shutdown_with_no_grace_disconnects(tmp_path):
    class SlowToAnswer(_RecordingTransport):
        def __init__(self):
            super().__init__()
            self.order = []

        async def _finish(self, task_id, outcome):
            await asyncio.sleep(0.05)
            self.order.append(("answered", task_id, outcome.state))

        async def _disconnect(self):
            self.order.append(("disconnected",))

    async def handler(ctx):
        await asyncio.sleep(30)

    transport = SlowToAnswer()
    agent = MeshAgent(cfg(tmp_path, shutdown_grace_seconds=0), handler, transport=transport)
    await agent._runner.on_task(IncomingTask(task_id="T1"))
    await settle()
    agent._stopper.on_control(False, "stopped_by_sam")
    await agent.stop()
    assert transport.order == [("answered", "T1", "failed"), ("disconnected",)]


class _Recording(_RecordingTransport):
    """Records what reaches the transport for each task id."""

    def __init__(self):
        super().__init__()
        self.sent, self.asked, self.called = [], [], []

    async def _send_event(self, task_id, event):
        self.sent.append((task_id, event))
        return True

    async def _ask(self, task_id, prompt):
        self.asked.append(task_id)
        return PromptAnswer(prompt.request_id, "answered", answers=("yes",))

    async def _call_peer(self, call):
        self.called.append(call.parent_task_id)
        return PeerResult(call.agent, "completed", "fine")


@pytest.mark.parametrize("ended_by", ["answer", "cancel", "limit"])
async def test_work_of_an_ended_run_reaches_nothing_of_the_same_task_id_delivered_again(tmp_path, caplog, ended_by):
    new_run, stale_done, got = asyncio.Event(), asyncio.Event(), {}

    async def leftover(ctx):
        await new_run.wait()
        await ctx.progress("A stale step.")
        got["asked"] = (await ctx.ask_user("Approve the stale claim?")).status
        got["called"] = (await ctx.call_agent("policy-desk", "Check the stale policy.")).state
        stale_done.set()

    async def handler(ctx):
        if "first" not in got:
            got["first"] = asyncio.ensure_future(leftover(ctx))
            if ended_by == "limit":
                await ctx.emit_event(ModelStarted("openai/gpt-4o"))
                await ctx.emit_event(ModelStarted("openai/gpt-4o"))
            if ended_by != "answer":
                await asyncio.sleep(30)
            return "The first answer."
        new_run.set()
        await stale_done.wait()
        return "Claim C-1 is approved."

    transport = _Recording()
    agent = MeshAgent(cfg(tmp_path, max_llm_calls=1), handler, transport=transport)
    await agent._runner.on_task(IncomingTask(task_id="T1"))
    await settle()
    if ended_by == "cancel":
        await agent._runner.on_cancel("T1")
    await settle()
    assert "T1" in transport.finished and agent._runner.tasks == {}
    sent_before = len(transport.sent)

    await agent._runner.on_task(IncomingTask(task_id="T1"))
    await asyncio.wait_for(stale_done.wait(), 5)
    await settle()
    assert (got["asked"], got["called"]) == ("cancelled", "error")
    assert transport.asked == [] and transport.called == []
    assert not any("stale" in str(event).lower() for _, event in transport.sent[sent_before:])
    assert transport.finished["T1"].text == "Claim C-1 is approved."
    refused = [r for r in caplog.records if "no longer held the task" in r.getMessage() and r.levelname == "WARNING"]
    assert len(refused) == 1
    await agent.stop()


class _LeaseTransport(_RecordingTransport):
    """A link down past the offline grace, read on the clock before the agent's offline timer applies it: the stop
    reads as lapsed (``disconnected``) live, as a handler's check sees it, before anything has been applied."""

    def lapse(self) -> None:
        stop = self.host._stopper.state
        stop.connection(True)
        stop.connection(False)
        grace = stop._grace
        stop._clock = lambda: time.monotonic() + 10 * grace + 1


async def test_a_run_that_meets_a_stop_the_agent_has_not_applied_yet_is_answered_as_stopped(tmp_path, caplog):
    transport = _LeaseTransport()

    async def handler(ctx):
        transport.lapse()
        ctx.check_running()
        return "never"

    agent = MeshAgent(cfg(tmp_path), handler, transport=transport)
    with caplog.at_level("INFO", logger="sam_link_sdk.agent"):
        await agent._runner.on_task(IncomingTask(task_id="T1"))
        await settle()
    answer = transport.finished["T1"]
    assert answer.state == "failed" and "lost contact with SAM" in answer.text
    assert not [r for r in caplog.records if r.levelname == "ERROR"]
    agent._stopper.on_control(False, "stopped_by_sam")
    await settle()
    assert transport.finished["T1"] is answer
    await agent.stop()


def test_a_stop_check_names_the_agents_stop_once():
    from sam_link_sdk.context import TaskContext, TaskHalted

    ctx = TaskContext._new(
        task_id="T1", context_id="c", text="", agent_name=NAME, agent_stop_reason=lambda: "disconnected"
    )
    with pytest.raises(TaskHalted) as raised:
        ctx.check_running()
    assert isinstance(raised.value, AgentStopped) and raised.value.reason == "disconnected"
    assert str(raised.value) == "agent stopped by SAM (disconnected)"


class _PluginError(RuntimeError):
    """Shaped like ADK's wrapping of a plugin's exception: raised from it."""


class _EventLoopError(Exception):
    """Shaped like Strands' EventLoopException: the hook's exception only as its context."""


def _wrapped_by_a_plugin(halt):
    try:
        raise halt
    except Exception as exc:
        raise _PluginError("Error in plugin claims-guard") from exc


def _wrapped_by_an_event_loop(halt):
    try:
        raise halt
    except Exception:
        raise _EventLoopError("event loop failed")  # noqa: B904


def _in_a_group(halt):
    raise ExceptionGroup("tool calls failed", [ValueError("another tool"), halt])


@pytest.mark.parametrize(
    "wrap",
    [_wrapped_by_a_plugin, _wrapped_by_an_event_loop, _in_a_group],
    ids=["raised from (ADK)", "only as context (Strands)", "exception group"],
)
async def test_a_stop_check_wrapped_by_a_framework_is_answered_as_the_stop(tmp_path, caplog, wrap):
    transport = _LeaseTransport()

    async def handler(ctx):
        transport.lapse()
        try:
            ctx.check_running()
        except Exception as halt:
            wrap(halt)
        return "never"

    agent = MeshAgent(cfg(tmp_path), handler, transport=transport)
    with caplog.at_level("INFO", logger="sam_link_sdk.agent"):
        await agent._runner.on_task(IncomingTask(task_id="T1"))
        await settle()
    assert transport.finished["T1"].state == "failed" and "lost contact with SAM" in transport.finished["T1"].text
    assert not [r for r in caplog.records if r.levelname == "ERROR"]
    await agent.stop()


@pytest.mark.parametrize("reason", ["answered", "cancelled", "stopped_by_sam"])
@pytest.mark.parametrize("wrapped", [False, True], ids=["bare", "wrapped"])
async def test_another_tasks_stop_check_is_a_handler_failure_of_this_one(tmp_path, caplog, reason, wrapped):
    from sam_link_sdk.context import TaskHalted

    async def handler(ctx):
        other = TaskHalted("T-other", reason)
        if wrapped:
            _wrapped_by_a_plugin(other)
        raise other

    transport = _RecordingTransport()
    agent = MeshAgent(cfg(tmp_path), handler, transport=transport)
    await agent._runner.on_task(IncomingTask(task_id="T1"))
    await settle()
    assert transport.finished["T1"].state == "failed" and transport.finished["T1"].text == ""
    assert agent._runner.tasks == {}
    assert [r for r in caplog.records if r.levelname == "ERROR" and "handler failed" in r.getMessage()]
    await agent.stop()


async def test_a_cancellation_the_agent_did_not_cause_is_answered(tmp_path):
    async def handler(ctx):
        inner = asyncio.ensure_future(asyncio.sleep(10))
        inner.cancel()
        return await inner

    transport = _RecordingTransport()
    agent = MeshAgent(cfg(tmp_path), handler, transport=transport)
    await agent._runner.on_task(IncomingTask(task_id="T1"))
    await settle()
    assert transport.finished["T1"].state == "failed" and agent._runner.tasks == {}
    await agent.stop()


class _GivesBack(_RecordingTransport):
    def __init__(self):
        super().__init__()
        self.given_back = []

    async def _give_back(self, task_id):
        self.given_back.append(task_id)


async def _cut_short_from_outside(ctx):
    await asyncio.sleep(30)


async def _swallows_its_own_cancel_then_meets_an_inner_one(ctx):
    """Swallow the run's own cancel, then meet another cancel the way a library does.

    A library breaks out of a wait that way and swallows the cancel without calling `uncancel()`.
    """
    asyncio.current_task().cancel()
    try:
        await asyncio.sleep(1)
    except asyncio.CancelledError:
        pass
    inner = asyncio.ensure_future(asyncio.sleep(10))
    await asyncio.sleep(0)
    inner.cancel()
    await inner


@pytest.mark.parametrize(
    "handler",
    [_cut_short_from_outside, _swallows_its_own_cancel_then_meets_an_inner_one],
    ids=["cancelled from outside", "a swallowed cancel of its own task"],
)
async def test_a_run_cancelled_from_outside_the_agent_is_given_back_for_sam_to_deliver_again(tmp_path, caplog, handler):
    """The cancel comes from outside the agent, not from its own cancel, limit, stop or abandonment: a loop shutting down,
    user code.
    """
    transport = _GivesBack()
    agent = MeshAgent(cfg(tmp_path), handler, transport=transport)
    await agent._runner.on_task(IncomingTask(task_id="T1"))
    run = agent._runner.tasks["T1"].task
    await settle()
    if handler is _cut_short_from_outside:
        run.cancel()
    for _ in range(5):
        await settle()
    assert run.cancelled()
    assert "T1" not in transport.finished and transport.given_back == ["T1"] and agent._runner.tasks == {}
    warned = [r for r in caplog.records if r.levelname == "WARNING" and "cancelled from outside" in r.getMessage()]
    assert len(warned) == 1 and "task=T1" in warned[0].getMessage()
    await agent.stop()


async def test_a_base_exception_out_of_a_handler_is_answered(tmp_path):
    class Escape(BaseException):
        pass

    async def handler(ctx):
        raise Escape()

    transport = _RecordingTransport()
    agent = MeshAgent(cfg(tmp_path), handler, transport=transport)
    await agent._runner.on_task(IncomingTask(task_id="T1"))
    await settle()
    assert transport.finished["T1"].state == "failed" and agent._runner.tasks == {}
    await agent.stop()


def test_late_work_is_noted_once_per_run_whoever_notes_it(caplog):
    from sam_link_sdk.context import TaskContext

    ctx = TaskContext(task_id="T1", context_id="c", text="", agent_name=NAME)
    with caplog.at_level("DEBUG", logger="sam_link_sdk.task"):
        assert ctx._run.note_late_work("run a guarded tool") is True
        assert ctx._run.note_late_work("step progress") is False
        ctx._run.mark_answered()
        assert ctx._run.refused_after_answer("ask the user") is True
    levels = [r.levelname for r in caplog.records if "no longer held the task" in r.getMessage()]
    assert levels == ["WARNING", "DEBUG", "DEBUG"]


async def test_a_handler_that_returned_is_answered_with_its_result_though_a_stop_is_read_that_moment(tmp_path):
    transport = _LeaseTransport()

    async def handler(ctx):
        transport.lapse()
        return "Claim C-1 is approved."

    agent = MeshAgent(cfg(tmp_path), handler, transport=transport)
    await agent._runner.on_task(IncomingTask(task_id="T1"))
    await settle()
    answer = transport.finished["T1"]
    assert (answer.state, answer.text) == ("completed", "Claim C-1 is approved.")
    await agent.stop()


async def test_a_stop_applied_before_a_returned_handlers_answer_is_claimed_answers_with_the_stop(tmp_path):
    class HoldsTheFinalText(_LeaseTransport):
        gate = asyncio.Event()

        async def _send_event(self, task_id, event):
            await self.gate.wait()
            return True

    transport = HoldsTheFinalText()
    agent = MeshAgent(cfg(tmp_path), lambda ctx: "Claim C-1 is approved.", transport=transport)
    await agent._runner.on_task(IncomingTask(task_id="T1"))
    await settle()
    agent._stopper.on_control(False, "stopped_by_sam")
    transport.gate.set()
    await settle()
    answer = transport.finished["T1"]
    assert answer.state == "failed" and "an operator stopped this agent" in answer.text
    await agent.stop()


pytest.importorskip("langchain_core")

from langchain_core.runnables import RunnableLambda  # noqa: E402

from sam_link_sdk.langchain.stop import StopGuard  # noqa: E402


async def test_a_langchain_runnable_refuses_to_run_once_sam_stops_the_agent(tmp_path):
    agent, transport = await start_agent(lambda ctx: "ok", state_dir=str(tmp_path))
    chain = RunnableLambda(lambda x: f"answer to {x}").with_config(callbacks=[StopGuard(agent)])
    assert chain.invoke("q") == "answer to q"
    assert await chain.ainvoke("q") == "answer to q"
    await transport.control(False, "stopped_by_sam")
    with pytest.raises(AgentStopped):
        chain.invoke("q")
    with pytest.raises(AgentStopped):
        await chain.ainvoke("q")
    await agent.stop()
