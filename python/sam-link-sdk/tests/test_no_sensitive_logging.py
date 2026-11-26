# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Every adapter's run, with DEBUG logging on, has the SDK (the ``sam_link_sdk`` loggers and the audit records) log nothing a
user or a tool put in it: not the prompt, the model's answer, a tool's arguments or result, an error message a tool
raised, or a credential. A framework's own loggers are the framework's (Strands logs a tool's input at DEBUG).

The scenarios are the conformance suite's own (each adapter's driver, tests/test_adapter_conformance.py), with tools
and turns that carry recognisable markers."""

from __future__ import annotations

import logging

import pytest

from .adapter_task import adapter_task
from .test_adapter_conformance import DENIED_SCOPES, DRIVERS, Tools, Turn

PROMPT = "PROMPT-MARKER-4f1c triage the claims"
ARGUMENT = "ARGUMENT-MARKER-77ab"
RESULT = "RESULT-MARKER-91de"
ANSWER = "ANSWER-MARKER-0c3e"
CREDENTIAL = "sk-live-CREDENTIAL-MARKER-5d2a"
ERROR = "ERROR-MARKER-8b60"
MARKERS = (PROMPT, ARGUMENT, RESULT, ANSWER, CREDENTIAL, ERROR)

SCENARIOS = {
    "one_tool": [Turn("", (("call_a", "lookup_policy", {"policy_id": ARGUMENT}),), (10, 2)), Turn(ANSWER, (), (12, 3))],
    "failed_tool": [Turn("", (("call_x", "broken", {"policy_id": ARGUMENT}),), (10, 2)), Turn(ANSWER, (), (12, 3))],
    "denied_tool": [Turn("", (("call_r", "route_claim", {"claim_id": ARGUMENT}),), (10, 2)), Turn(ANSWER, (), (12, 3))],
}


class MarkedTools(Tools):
    """The scenario tools, with a marker in what each returns, raises or is passed."""

    def __init__(self) -> None:
        super().__init__()
        ran = self.ran

        def lookup_policy(policy_id: str) -> dict:
            """Look up an insurance policy by id."""
            ran.append("lookup_policy")
            return {"policy_id": policy_id, "note": RESULT, "access_token": CREDENTIAL}

        def broken(policy_id: str) -> dict:
            """Look up a policy in the system that is down."""
            ran.append("broken")
            raise RuntimeError(f"backend down {ERROR} password={CREDENTIAL}")

        self.by_name["lookup_policy"] = lookup_policy
        self.by_name["broken"] = broken


@pytest.mark.parametrize("scenario", list(SCENARIOS))
@pytest.mark.parametrize("adapter", list(DRIVERS))
async def test_a_run_logs_no_prompt_answer_tool_content_or_credential(adapter, scenario, caplog):
    """A run logs no prompt, answer, tool content or credential.

    The run may raise: a framework that ends the run on a raising tool (the conformance suite's DIFFERENCES) does.
    """
    tools = MarkedTools()
    driver = DRIVERS[adapter](SCENARIOS[scenario], tools, scenario)
    records: list = []
    task = {"scopes": DENIED_SCOPES, "granted": [], "user_id": "alice"} if scenario == "denied_tool" else {}
    async with adapter_task("T", PROMPT, audit=records.append, **task) as mesh_task:
        with caplog.at_level(logging.DEBUG):
            try:
                await mesh_task.run(driver.run)
            except Exception:
                pass
    driver.cleanup()

    ours = [r for r in caplog.records if r.name.startswith("sam_link_sdk")]
    assert ours
    formatter = logging.Formatter()
    seen = "\n".join([*(formatter.format(r) for r in ours), *(f"{r.message} {r.fields}" for r in records)])
    assert [marker for marker in MARKERS if marker in seen] == []


async def test_a_handler_that_raises_logs_no_message_text(caplog):
    from .agent_rig import start_agent

    async def handler(ctx):
        raise RuntimeError(f"{ERROR} password={CREDENTIAL}")

    agent, transport = await start_agent(handler)
    with caplog.at_level(logging.DEBUG):
        await transport.deliver("T1", text=PROMPT)
        await agent.stop()
    ours = "\n".join(logging.Formatter().format(r) for r in caplog.records if r.name.startswith("sam_link_sdk"))
    assert "failed" in ours.lower()
    assert [marker for marker in (PROMPT, ERROR, CREDENTIAL) if marker in ours] == []
