# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The Hermes adapter at its hook boundary, without Hermes installed: the hooks are called from a worker thread with the
keyword payloads Hermes uses (test_hermes drives the real AIAgent when Hermes is installed)."""

from __future__ import annotations

import asyncio
import json
from types import SimpleNamespace

import pytest

from sam_link_sdk.events import ModelFinished, ToolFinished, ToolStarted
from sam_link_sdk.hermes import steps as hermes_steps, with_approval

from .adapter_task import adapter_task, steps


async def _call_tool(run: hermes_steps.Run, name: str, result: str, **post) -> None:
    kw = {"session_id": run.session_id}

    def turn():
        hermes_steps.pre_tool_call(**kw, tool_name=name, args={"policy_id": "P-1"}, tool_call_id="c1")
        hermes_steps.post_tool_call(**kw, tool_name=name, tool_call_id="c1", result=result, **post)

    hermes_steps.register_run(run)
    try:
        await asyncio.to_thread(turn)
    finally:
        hermes_steps.unregister_run(run)


@pytest.mark.parametrize(
    "result,post,failed",
    [
        (json.dumps({"error": "backend down", "code": "unavailable"}), {"status": "error"}, True),
        ("backend down", {"status": "error", "error_message": "backend down"}, True),
        (json.dumps({"error": "backend down"}), {}, True),
        (json.dumps({"error": "backend down", "code": "unavailable"}), {}, False),
    ],
    ids=["error-and-code-marked-failed", "text-marked-failed", "only-error", "error-and-code-unmarked"],
)
async def test_a_tool_s_outcome_follows_the_one_rule_every_adapter_reports_by(result, post, failed):
    """_adapter.reported_result: failed when Hermes marks it so or the result is only {"error": ...}; a result that
    merely carries an error key among others is a success, as in every other adapter."""
    records = []
    async with adapter_task(audit=records.append) as task:
        await _call_tool(
            hermes_steps.Run(task.ctx, asyncio.get_running_loop(), "sam-T"), "lookup_policy", result, **post
        )
        [end] = task.transport.events(task.task_id, ToolFinished)
    assert (set(end.result) == {"error"}) is failed and end.failed is failed
    assert [r.message for r in records] == ["tool execution failed" if failed else "tool execution succeeded"]


async def test_a_successful_tool_is_reported_as_it_answered():
    async with adapter_task() as task:
        await _call_tool(
            hermes_steps.Run(task.ctx, asyncio.get_running_loop(), "sam-T"),
            "lookup_policy",
            json.dumps({"tier": "gold"}),
            status="success",
        )
        [end] = task.transport.events(task.task_id, ToolFinished)
    assert end.result == {"tier": "gold"}


def test_with_approval_takes_names_only_as_a_keyword_and_only_names():
    with pytest.raises(TypeError):
        with_approval(["route_claim"])  # type: ignore[misc]
    with pytest.raises(TypeError, match="list of tool names"):
        with_approval(names="route_claim")
    with pytest.raises(TypeError, match="tool names"):
        with_approval(names=[SimpleNamespace(name="route_claim")])
    assert with_approval(names=["route_claim"]).covers("route_claim")


class FakeHermes:
    """The parts of Hermes the adapter touches, as modules in sys.modules, and an agent whose turn calls the
    process-wide hooks as Hermes does: the model's calls, each tool call (blocked when a pre_tool_call hook says so),
    and a delegated subagent with a session of its own (``subagent_start``, then the child's calls)."""

    def __init__(self, monkeypatch, hook_timeout=30.0):
        import sys
        import types

        self.hooks: dict[str, list] = {}
        self.ran: list[tuple[str, str]] = []
        self.interrupted: list[str] = []
        self.hard_interrupts: list[str] = []
        plugins = types.ModuleType("hermes_cli.plugins")
        plugins.get_plugin_manager = lambda: SimpleNamespace(_hooks=self.hooks)
        plugins.VALID_HOOKS = {
            "pre_api_request",
            "post_api_request",
            "api_request_error",
            "pre_tool_call",
            "post_tool_call",
            "subagent_start",
            "subagent_stop",
        }
        plugins._resolve_hook_callback_timeout = lambda: hook_timeout
        approval_context = types.ModuleType("tools.approval_context")
        approval_context.set_hermes_interactive_context = lambda value: None
        approval_context.reset_hermes_interactive_context = lambda token: None
        terminal = types.ModuleType("tools.terminal_tool")
        self.command_approvals: list = []
        terminal.set_approval_callback = lambda callback: self.command_approvals.append(callback)
        registry = types.ModuleType("tools.registry")
        registry.registry = SimpleNamespace(register=lambda **kw: None)
        delegation = types.ModuleType("tools.async_delegation")
        delegation.interrupt_for_session = lambda parent_session_id="", **kw: self.interrupted.append(parent_session_id)
        sanitization = types.ModuleType("agent.message_sanitization")
        sanitization.uniquify_tool_call_ids = lambda tool_calls, taken=(): tool_calls
        for name, module in {
            "hermes_cli": types.ModuleType("hermes_cli"),
            "hermes_cli.plugins": plugins,
            "tools": types.ModuleType("tools"),
            "tools.approval_context": approval_context,
            "tools.terminal_tool": terminal,
            "tools.registry": registry,
            "tools.async_delegation": delegation,
            "agent": types.ModuleType("agent"),
            "agent.message_sanitization": sanitization,
        }.items():
            monkeypatch.setitem(sys.modules, name, module)

    def fire(self, hook, **kw):
        results = [callback(**kw) for callback in list(self.hooks.get(hook, []))]
        return next((r for r in results if r is not None), None)

    def call_tool(self, session, name, call_id, args=None):
        blocked = self.fire("pre_tool_call", session_id=session, tool_name=name, args=args or {}, tool_call_id=call_id)
        if blocked is None:
            self.ran.append((session, name))
            self.fire(
                "post_tool_call",
                session_id=session,
                tool_name=name,
                tool_call_id=call_id,
                result="ok",
                status="success",
            )
        return blocked

    def model_call(self, session, calls=(), usage=(10, 2)):
        self.fire("pre_api_request", session_id=session, model="fake-model")
        message = SimpleNamespace(
            content="", tool_calls=[SimpleNamespace(id=c, name=n, arguments="{}") for c, n in calls]
        )
        self.fire(
            "post_api_request",
            session_id=session,
            model="fake-model",
            assistant_message=message,
            usage={"input_tokens": usage[0], "output_tokens": usage[1]},
        )

    def agent(self, tools, turn, session="sam-T"):
        hermes = self

        class Agent:
            session_id = session
            ephemeral_system_prompt = ""
            stream_delta_callback = None
            tool_gen_callback = None

            def __init__(self):
                self.tools = [{"type": "function", "function": {"name": n}} for n in tools]
                self.valid_tool_names = set(tools)

            def run_conversation(self, text, conversation_history=None, task_id=""):
                turn(hermes, self)
                return {"final_response": "done", "messages": [], "completed": True}

            def hard_interrupt(self, message=None, **kw):
                hermes.hard_interrupts.append(self.session_id)

        return Agent()


SCOPED = {"scopes": {"route_claim": ["tool:claims:route"]}, "declared": ["lookup_policy"], "granted": []}


async def test_a_delegated_subagent_is_checked_and_reported_as_its_parent_task(monkeypatch):
    """Hermes' delegate_task runs a child AIAgent with a session of its own and the parent's toolsets: the child's
    scoped tool is refused, and its model call, tokens and tool steps reach the parent's task."""
    hermes = FakeHermes(monkeypatch)

    def turn(h, agent):
        h.model_call("sam-T", [("c0", "delegate_task")])
        h.fire("subagent_start", parent_session_id="sam-T", child_session_id="child-1")
        h.model_call("child-1", [("c1", "route_claim"), ("c2", "lookup_policy")], usage=(7, 3))
        h.call_tool("child-1", "route_claim", "c1")
        h.call_tool("child-1", "lookup_policy", "c2")
        h.fire("subagent_stop", parent_session_id="sam-T", child_session_id="child-1")

    from sam_link_sdk.hermes import run_agent

    async with adapter_task(**SCOPED) as task:
        assert (
            await run_agent(task.ctx, lambda c: hermes.agent(["lookup_policy", "route_claim", "delegate_task"], turn))
            == "done"
        )
        sent = steps(task)
    assert hermes.ran == [("child-1", "lookup_policy")]
    results = {e.name: e.result for e in sent if isinstance(e, ToolFinished)}
    assert results["route_claim"] == {"error": "Access denied: you do not have the scopes required to use route_claim."}
    assert results["lookup_policy"] == "ok"
    assert [type(e) for e in sent].count(ModelFinished) == 2
    assert task.ctx._run.usage.final_token_usage()["total_input_tokens"] == 17


async def test_while_a_mesh_task_runs_a_session_no_task_knows_is_refused_and_otherwise_left_alone(monkeypatch):
    hermes = FakeHermes(monkeypatch)
    blocked = []

    def turn(h, agent):
        """A turn of a subagent SAM was not told about."""
        blocked.append(h.call_tool("stranger", "lookup_policy", "c9"))

    from sam_link_sdk.hermes import run_agent

    async with adapter_task() as task:
        await run_agent(task.ctx, lambda c: hermes.agent(["lookup_policy"], turn))
    assert blocked[0]["action"] == "block" and "belongs to no mesh task" in blocked[0]["message"]
    assert hermes.call_tool("stranger", "lookup_policy", "c10") is None


async def test_two_runs_cannot_share_a_session(monkeypatch):
    async with adapter_task() as first, adapter_task() as second:
        loop = asyncio.get_running_loop()
        run = hermes_steps.Run(first.ctx, loop, "shared")
        hermes_steps.register_run(run)
        try:
            with pytest.raises(hermes_steps.SessionInUse):
                hermes_steps.register_run(hermes_steps.Run(second.ctx, loop, "shared"))
        finally:
            hermes_steps.unregister_run(run)


async def test_an_approval_ends_before_hermes_abandons_the_hook_and_the_call_is_reported_refused(monkeypatch):
    """The approval's timeout is Hermes' hook timeout, 30 s, less a 5 s margin and the 5 s the agent link waits past a
    prompt's timeout for its answer.
    """
    hermes = FakeHermes(monkeypatch, hook_timeout=30.0)
    asked = []

    def turn(h, agent):
        h.model_call("sam-T", [("c1", "route_claim")])
        h.call_tool("sam-T", "route_claim", "c1")

    from sam_link_sdk.hermes import run_agent

    async def request_approval(name, args, *, message="", timeout_seconds=None):
        asked.append(timeout_seconds)
        return SimpleNamespace(approved=False, outcome="timed_out")

    async with adapter_task() as task:
        task.ctx.request_approval = request_approval
        await run_agent(
            task.ctx, lambda c: hermes.agent(["route_claim"], turn), approval=with_approval(names=["route_claim"])
        )
        [end] = task.transport.events(task.task_id, ToolFinished)
    assert asked == [20.0] and hermes.ran == []
    assert "did not approve" in end.result["error"]


async def test_an_approval_naming_a_tool_the_agent_lacks_next_to_its_tools_is_refused(monkeypatch):
    hermes = FakeHermes(monkeypatch)
    from sam_link_sdk.hermes import run_agent

    async with adapter_task() as task:
        with pytest.raises(ValueError, match="route_clam"):
            await run_agent(
                task.ctx,
                lambda c: hermes.agent(["route_claim"], lambda h, a: None),
                approval=with_approval(names=["route_clam"]),
            )


async def test_a_scoped_extra_tool_is_hidden_from_the_model_too(monkeypatch):
    from sam_link_sdk.hermes import run_agent
    from sam_link_sdk.hermes.peer_tools import MeshTool

    hermes = FakeHermes(monkeypatch)
    offered = []

    def turn(h, agent):
        offered.append(sorted(agent.valid_tool_names))

    peer = MeshTool("peer_billing", "Billing", {"type": "object"}, lambda args, call_id: "")
    async with adapter_task(
        scopes={"peer_billing": ["agent:billing:invoke"]}, declared=["lookup_policy"], granted=[]
    ) as task:
        await run_agent(task.ctx, lambda c: hermes.agent(["lookup_policy"], turn), extra_tools=[peer])
    assert offered == [["lookup_policy"]]


def test_the_approval_wait_leaves_out_the_grace_the_agent_link_adds_to_a_prompt():
    from sam_link_sdk.link.transport import ANSWER_GRACE_SECONDS

    assert hermes_steps.ANSWER_GRACE_SECONDS == ANSWER_GRACE_SECONDS


async def test_a_background_subagent_that_outlives_its_parents_turn_is_interrupted_and_refused_for_good(monkeypatch):
    """Hermes runs a top-level agent's delegate_task in the background: the turn returns while the child still runs.
    The child is interrupted, and every tool call it makes afterwards is refused, before and after subagent_stop and
    whether or not another mesh task runs (H4)."""
    hermes = FakeHermes(monkeypatch)

    def turn(h, agent):
        h.model_call("sam-T", [("c0", "delegate_task")])
        h.fire("subagent_start", parent_session_id="sam-T", child_session_id="child-bg")
        h.model_call("child-bg", [("c1", "lookup_policy")], usage=(7, 3))
        h.call_tool("child-bg", "lookup_policy", "c1")

    from sam_link_sdk.hermes import run_agent

    async with adapter_task() as task:
        assert await run_agent(task.ctx, lambda c: hermes.agent(["lookup_policy", "delegate_task"], turn)) == "done"
    assert hermes.ran == [("child-bg", "lookup_policy")]
    assert hermes.hard_interrupts == ["sam-T"] and hermes.interrupted == ["sam-T"]
    assert not hermes_steps.runs_active()

    after_turn = hermes.call_tool("child-bg", "lookup_policy", "c2")
    hermes.fire("subagent_stop", parent_session_id="sam-T", child_session_id="child-bg")
    after_stop = hermes.call_tool("child-bg", "lookup_policy", "c3")
    assert after_turn["action"] == "block" and "has ended" in after_turn["message"]
    assert after_stop["action"] == "block" and "belongs to no mesh task" in after_stop["message"]
    assert hermes.ran == [("child-bg", "lookup_policy")]
    assert hermes.call_tool("stranger", "lookup_policy", "c4") is None


async def test_a_turn_whose_subagents_all_stopped_interrupts_nothing(monkeypatch):
    hermes = FakeHermes(monkeypatch)

    def turn(h, agent):
        h.fire("subagent_start", parent_session_id="sam-T", child_session_id="child-sync")
        h.call_tool("child-sync", "lookup_policy", "c1")
        h.fire("subagent_stop", parent_session_id="sam-T", child_session_id="child-sync")

    from sam_link_sdk.hermes import run_agent

    async with adapter_task() as task:
        await run_agent(task.ctx, lambda c: hermes.agent(["lookup_policy", "delegate_task"], turn))
    assert hermes.ran == [("child-sync", "lookup_policy")] and hermes.hard_interrupts == [] and hermes.interrupted == []


async def test_an_approval_that_fails_to_be_asked_refuses_the_call_and_ends_its_step(monkeypatch):
    from sam_link_sdk.prompting import PromptTooLarge

    hermes = FakeHermes(monkeypatch)
    blocked = []

    def turn(h, agent):
        h.model_call("sam-T", [("c1", "route_claim")])
        blocked.append(h.call_tool("sam-T", "route_claim", "c1"))

    from sam_link_sdk.hermes import run_agent

    async def request_approval(name, args, *, message="", timeout_seconds=None):
        raise PromptTooLarge("too many options")

    async with adapter_task() as task:
        task.ctx.request_approval = request_approval
        await run_agent(
            task.ctx, lambda c: hermes.agent(["route_claim"], turn), approval=with_approval(names=["route_claim"])
        )
        tool_steps = [(type(e), e.call_id) for e in task.transport.events(task.task_id, (ToolStarted, ToolFinished))]
    assert hermes.ran == [] and blocked[0]["action"] == "block"
    assert tool_steps == [(ToolStarted, "c1"), (ToolFinished, "c1")]


async def test_a_cancelled_run_whose_tool_hangs_refuses_its_own_calls_and_holds_up_no_other_agent(monkeypatch):
    """A tool that never checks Hermes' interrupt keeps the cancelled turn's thread alive: the run is closed at the
    cancel, so its own late calls are refused, and it no longer counts as a running mesh task for other agents."""
    hermes = FakeHermes(monkeypatch)
    started, release = asyncio.Event(), asyncio.Event()
    loop = asyncio.get_running_loop()
    late = []

    def turn(h, agent):
        loop.call_soon_threadsafe(started.set)
        asyncio.run_coroutine_threadsafe(release.wait(), loop).result(5)
        late.append(h.call_tool("sam-T", "lookup_policy", "c-late"))

    from sam_link_sdk.hermes import run_agent

    async with adapter_task() as mesh_task:
        task = asyncio.create_task(run_agent(mesh_task.ctx, lambda c: hermes.agent(["lookup_policy"], turn)))
        await started.wait()
        task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await task
        assert not hermes_steps.runs_active()
        assert hermes.call_tool("other-hermes-agent", "lookup_policy", "c-other") is None
        release.set()
        for _ in range(200):
            if late:
                break
            await asyncio.sleep(0.01)
    assert late[0]["action"] == "block"
    assert hermes.ran == [("other-hermes-agent", "lookup_policy")]


async def test_former_subagents_and_closed_runs_are_bounded_and_a_forgotten_one_fails_closed_while_a_run_is_open(
    monkeypatch,
):
    """A long-lived process: a closed run whose subagent never reports subagent_stop is let go after
    CLOSED_RUN_SECONDS (its TaskContext with it), the subagent still refused as a former one; former subagents are
    remembered for CHILD_MEMORY_SECONDS and at most MAX_CHILD_SESSIONS; one forgotten is refused only while a mesh run
    is open."""
    now = [1000.0]
    monkeypatch.setattr(hermes_steps, "_clock", lambda: now[0])
    monkeypatch.setattr(hermes_steps, "MAX_CHILD_SESSIONS", 3)
    monkeypatch.setattr(hermes_steps, "_ever_children", type(hermes_steps._ever_children)())
    async with adapter_task() as task:
        _bounded_runs(task.ctx, now)


def _bounded_runs(ctx, now):
    run = hermes_steps.Run(ctx, asyncio.get_running_loop(), "sam-bounded")
    hermes_steps.register_run(run)
    hermes_steps.subagent_start(parent_session_id="sam-bounded", child_session_id="child-0")
    hermes_steps.end_turn(run)

    now[0] += hermes_steps.CLOSED_RUN_SECONDS + 1
    assert hermes_steps.run_for("child-0") is None
    assert (
        hermes_steps.pre_tool_call(session_id="child-0", tool_name="t", args={}, tool_call_id="c")["action"] == "block"
    )

    for n in range(1, 5):
        other = hermes_steps.Run(ctx, asyncio.get_running_loop(), f"sam-{n}")
        hermes_steps.register_run(other)
        hermes_steps.subagent_start(parent_session_id=f"sam-{n}", child_session_id=f"child-{n}")
        hermes_steps.subagent_stop(child_session_id=f"child-{n}")
        hermes_steps.end_turn(other)
    assert len(hermes_steps._ever_children) == 3 and "child-0" not in hermes_steps._ever_children
    assert hermes_steps.pre_tool_call(session_id="child-0", tool_name="t", args={}, tool_call_id="c") is None

    now[0] += hermes_steps.CHILD_MEMORY_SECONDS + 1
    open_run = hermes_steps.Run(ctx, asyncio.get_running_loop(), "sam-open")
    hermes_steps.register_run(open_run)
    try:
        assert not hermes_steps.was_child("child-4")
        blocked = hermes_steps.pre_tool_call(session_id="child-4", tool_name="t", args={}, tool_call_id="c")
        assert blocked["action"] == "block"
    finally:
        hermes_steps.end_turn(open_run)
    assert hermes_steps.pre_tool_call(session_id="child-4", tool_name="t", args={}, tool_call_id="c") is None


async def test_a_task_delivered_again_while_a_given_up_run_lingers_gets_a_session_of_its_own(monkeypatch):
    """The stand-in for test_hermes' real-Hermes test: agents built with agent_defaults get a session per delivery,
    so a second delivery of a task whose given-up run still holds its session runs, and the stale turn's late tool
    call is refused by its own (closed) run rather than reaching the new one."""
    import threading

    from sam_link_sdk.hermes import agent_defaults, run_agent

    hermes = FakeHermes(monkeypatch)
    started, release, late = threading.Event(), threading.Event(), []

    def stuck(h, agent):
        started.set()
        release.wait(10)
        late.append(h.call_tool(agent.session_id, "lookup_policy", "c-late"))

    def quick(h, agent):
        h.call_tool(agent.session_id, "lookup_policy", "c1")

    async with adapter_task() as first_task:
        first_ctx = first_task.ctx
        first = asyncio.create_task(
            run_agent(
                first_ctx, lambda c: hermes.agent(["lookup_policy"], stuck, session=agent_defaults(c)["session_id"])
            )
        )
        try:
            await asyncio.wait_for(asyncio.to_thread(started.wait, 10), 15)
            first_ctx._run.halt()
            first.cancel()
            await asyncio.gather(first, return_exceptions=True)
            async with adapter_task() as second_task:
                second_ctx = second_task.ctx
                assert second_ctx.task_id == first_ctx.task_id
                assert (
                    await run_agent(
                        second_ctx,
                        lambda c: hermes.agent(["lookup_policy"], quick, session=agent_defaults(c)["session_id"]),
                    )
                    == "done"
                )
        finally:
            release.set()
    for _ in range(100):
        if late:
            break
        await asyncio.sleep(0.02)
    assert late[0]["action"] == "block" and len(hermes.ran) == 1


async def test_a_stopped_runs_interrupt_never_holds_up_the_hook_and_a_closed_run_interrupts_nothing():
    """Hermes' hard stop may wait for a compression commit in flight; the hook that saw the stop (on the conversation
    thread) must not wait with it, so the interrupt runs on a thread of its own. A closed run's agent object may
    already serve another run, so a closed run's late hook interrupts nothing."""
    import threading

    release, interrupted = threading.Event(), []

    def slow_interrupt():
        interrupted.append("open")
        release.wait(5)

    async with adapter_task() as task, adapter_task() as closed_task:
        await _interrupts(task.ctx, closed_task.ctx, slow_interrupt, release, interrupted)


async def _interrupts(ctx, closed_ctx, slow_interrupt, release, interrupted):
    run = hermes_steps.Run(ctx, asyncio.get_running_loop(), "sam-interrupt")
    run.interrupt = slow_interrupt
    hermes_steps.register_run(run)
    try:
        ctx._run.halt()
        returned = await asyncio.wait_for(
            asyncio.to_thread(hermes_steps.pre_api_request, session_id="sam-interrupt", model="fake-model"), 2
        )
        assert returned is None
        for _ in range(100):
            if interrupted:
                break
            await asyncio.sleep(0.01)
        assert interrupted == ["open"]
    finally:
        release.set()
        hermes_steps.end_turn(run)

    closed = hermes_steps.Run(closed_ctx, asyncio.get_running_loop(), "sam-closed")
    closed.interrupt = lambda: interrupted.append("closed")
    hermes_steps.register_run(closed)
    hermes_steps.close_run(closed)
    closed_ctx._run.halt()
    try:
        assert closed.stop_if_halted() is True
        await asyncio.sleep(0.05)
        assert interrupted == ["open"]
    finally:
        hermes_steps.end_turn(closed)


@pytest.mark.parametrize("halt", ["halted", "cancelled"])
async def test_hermes_own_dangerous_command_prompt_approved_after_the_stop_runs_nothing(halt, monkeypatch, tmp_path):
    """Hermes' terminal asks SAM's approval for a dangerous command through the callback run_agent hands it; an
    approval that comes after the task was stopped or cancelled denies the command (here: removing a scratch directory
    under the test's tmp_path)."""
    from types import SimpleNamespace

    from sam_link_sdk.hermes import run_agent

    scratch = tmp_path / "scratch"
    scratch.mkdir()
    (scratch / "keep.txt").write_text("x")
    hermes = FakeHermes(monkeypatch)
    decisions: list[str] = []

    def turn(h, agent):
        decision = h.command_approvals[-1](f"rm -rf {scratch}", "Deletes a directory.")
        decisions.append(decision)
        if decision == "once":
            (scratch / "keep.txt").unlink()

    async with adapter_task() as task:

        async def request_approval(name, args, *, message="", timeout_seconds=None):
            await stop_task(task, halt)
            return SimpleNamespace(approved=True, outcome="approved")

        task.ctx.request_approval = request_approval
        try:
            await task.run(lambda ctx: run_agent(ctx, lambda c: hermes.agent([], turn)))
        except (Exception, asyncio.CancelledError):
            pass
        for _ in range(200):
            if decisions:
                break
            await asyncio.sleep(0.01)
        if halt == "cancelled":
            assert task.answer.state == "canceled"
    assert decisions == ["deny"] and (scratch / "keep.txt").exists()


async def stop_task(task, halt: str) -> None:
    """Stop the task while its run works: SAM's cancel ("cancelled"), or a stop such as a limit ("halted")."""
    if halt == "cancelled":
        await task.cancel()
    else:
        task.ctx._run.halt()
