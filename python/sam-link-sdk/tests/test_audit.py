# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Audit records: tool executions, delegation, SAM's stop and release; ids and outcomes only. (Who may run the agent,
and who the user is, is SAM's to audit: the entrypoint checks them before it delivers a task.)"""

from __future__ import annotations

import json
import logging

from sam_link_sdk import PeerResult
from sam_link_sdk._model import PEER_CALL_DENIED, PEER_CALL_GRANTED, PEER_REFUSED, PeerInfo
from sam_link_sdk.audit import AuditEvent, AuditLogger, log_sink
from sam_link_sdk.events import ToolFinished, ToolStarted

from .agent_rig import RecordingTransport, start_agent, task_context


class Collector:
    def __init__(self):
        self.events: list[AuditEvent] = []

    def __call__(self, event: AuditEvent) -> None:
        self.events.append(event)

    def messages(self) -> list[str]:
        return [e.message for e in self.events]


async def test_no_audit_for_a_task_that_only_answers():
    sink = Collector()
    agent, transport = await start_agent(lambda ctx: "ok", audit_sink=sink)
    await transport.deliver("T1", "SECRET PROMPT")
    assert sink.events == []
    await agent.stop()


async def test_tool_executions_are_audited_without_arguments_or_results():
    sink = Collector()
    async with task_context("T1", "x", context_id="CTX", user_id="u1", audit_sink=sink) as task:
        ctx = task.ctx
        await ctx.emit_event(ToolStarted("c1", "lookup_policy", {"policy_id": "P-1182", "password": "hunter2hunter2"}))
        await ctx.emit_event(ToolFinished("c1", "lookup_policy", {"tier": "gold"}))
        await ctx.emit_event(ToolStarted("c2", "route_claim", {"queue": "x"}))
        await ctx.emit_event(ToolFinished("c2", "route_claim", {"error": "boom: secret detail"}, failed=True))

    assert sink.messages() == ["tool execution succeeded", "tool execution failed"]
    ok, failed = sink.events
    assert ok.fields["tool"] == "lookup_policy" and ok.fields["userID"] == "u1" and ok.fields["sessionID"] == "CTX"
    assert isinstance(ok.fields.get("duration_ms", 0), int)
    assert failed.fields["error"] == "tool_error" and failed.level == logging.WARNING
    everything = json.dumps([e.as_dict() for e in sink.events])
    for leaked in ("P-1182", "hunter2", "gold", "boom", "secret detail"):
        assert leaked not in everything


async def test_a_peer_call_is_audited_as_sam_decided_it_and_not_at_all_when_sam_decided_nothing():
    """What each way a call ends on the agent link is audited as: test_link_protocol."""
    sink = Collector()

    async def handler(ctx):
        states = [
            (await ctx.call_agent(peer, "Is P-1 active?")).state for peer in ("policy-bot", "billing-bot", "slow-bot")
        ]
        return " / ".join(states)

    peers = [PeerInfo("policy-bot"), PeerInfo("billing-bot"), PeerInfo("slow-bot")]
    agent, transport = await start_agent(handler, RecordingTransport(peers), audit_sink=sink)
    await transport.deliver("T1", user_id="u1")
    await transport.reply(0, PeerResult("policy-bot", "completed", "P-1 is active.", sam_decision=PEER_CALL_GRANTED))
    await transport.reply(
        1, PeerResult("billing-bot", "error", f"{PEER_REFUSED}: not allowed", sam_decision=PEER_CALL_DENIED)
    )
    await transport.reply(2, PeerResult("slow-bot", "error", f"{PEER_REFUSED}: could not be delivered"))
    assert transport.finished["T1"].text == "completed / error / error"
    assert sink.messages() == ["agent delegation granted", "agent delegation denied"]
    assert dict(sink.events[1].fields) == {"userID": "u1", "taskID": "T1", "agentName": "billing-bot"}
    assert sink.events[1].level == logging.WARNING
    assert "P-1 is active" not in json.dumps([e.as_dict() for e in sink.events])
    await agent.stop()


def test_default_sink_writes_one_json_line(caplog):
    with caplog.at_level(logging.DEBUG, logger="sam_link_sdk.audit"):
        AuditLogger().tool_access(user_id="u1", task_id="T1", tool="route_claim", allowed=False)
    [record] = caplog.records
    assert record.levelno == logging.WARNING
    assert json.loads(record.getMessage()) == {
        "msg": "tool access denied",
        "seq": 1,
        "userID": "u1",
        "taskID": "T1",
        "tool": "route_claim",
    }


def test_a_failing_sink_does_not_break_the_caller():
    def broken(event):
        raise RuntimeError("siem down")

    AuditLogger(broken).tool_access(user_id="u1", task_id="T1", tool="route_claim", allowed=True)
    assert log_sink is not None
