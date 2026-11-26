# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Contract scenarios: SDK agents on a real SAM-Go, driven and checked from SAM's side.

Each scenario talks to SAM only as a user or administrator would (its HTTP API, the `sam` CLI) and asserts on what
SAM reports: the agent list, the task's state and answer, the task's recorded events, the spend records.
"""

from __future__ import annotations

import json
import sqlite3
import time

import pytest

from .driver import ToolCall
from .roster import REGISTERED_OWNER, REGISTERED_TEAM
from .sam_client import wait_until
from .sam_server import TEST_USER, TOOL_SCOPE_HELD, TOOL_SCOPE_NOT_HELD

pytestmark = pytest.mark.contract

MODEL = "openai/gpt-4o"
EXT_TOOLS = "https://solace.com/a2a/extensions/sam/tools"
EXT_PROVENANCE = "https://solace.com/a2a/extensions/sam/provenance"
EXT_REGISTRATION = "https://solace.com/a2a/extensions/sam/registration"
SPEND_ROLLUP_WAIT_SECONDS = 90
"""SAM rolls the spend ledger up into its usage views once a minute."""
NO_STOP_API = (404, 405, 410)
"""What POST /api/v1/platform/stoppedAgents answers on a SAM built without the agent stop API: no such route (404, 405), or
410 on a SAM that has retired the rest of /api/v1/platform.
"""


def extension(card: dict, uri: str) -> dict:
    for ext in (card.get("capabilities") or {}).get("extensions") or []:
        if ext.get("uri") == uri:
            return ext.get("params") or {}
    raise AssertionError(f"card {card.get('name')} has no {uri} extension")


def signal_types(signals: list[dict]) -> list[str]:
    return [s.get("type", "") for s in signals]


def test_agent_is_listed_with_its_card_and_delisted_when_it_leaves(mesh):
    async def answer(task):
        return "ok"

    agent = mesh.start(
        "listing",
        answer,
        description="Answers questions about claims.",
        tools=(("lookup_claim", "Looks up a claim by its number."),),
        owner="claims-team@example.com",
        framework="Contract Script",
    )
    card = mesh.client.wait_listed(mesh.name("listing"))

    assert card["description"] == "Answers questions about claims."
    tools = extension(card, EXT_TOOLS)["tools"]
    assert [(t["name"], t["description"]) for t in tools] == [("lookup_claim", "Looks up a claim by its number.")]
    declared = extension(card, EXT_PROVENANCE)
    assert declared["owner"] == "claims-team@example.com"
    assert declared["framework"] == "Contract Script"
    registered = extension(card, EXT_REGISTRATION)
    assert (registered["owner"], registered["team"]) == (REGISTERED_OWNER, REGISTERED_TEAM)

    agent.leave()
    mesh.client.wait_delisted(mesh.name("listing"), timeout=20)


def test_model_and_tool_steps_and_token_usage_are_recorded(mesh):
    lookup = ToolCall("call-ct-lookup-1", "lookup_claim", {"claim": "C-1"})

    async def answer(task):
        await task.model_call(MODEL, tool_calls=(lookup,), input_tokens=120, output_tokens=30)
        await task.tool_call(lookup, {"claim": "C-1", "status": "open"})
        await task.model_call(MODEL, text="Claim C-1 is open.", input_tokens=200, output_tokens=15)
        return "Claim C-1 is open."

    mesh.start("steps", answer)
    mesh.client.wait_listed(mesh.name("steps"))
    task_id = mesh.client.send(mesh.name("steps"), "What is the status of claim C-1?")
    view = mesh.client.wait_done(task_id)

    assert view.status == "completed"
    assert view.answer() == "Claim C-1 is open."
    signals = view.signals()
    steps = [t for t in signal_types(signals) if t != "agent_progress_update"]
    assert steps == [
        "llm_invocation",
        "llm_response",
        "tool_invocation_start",
        "tool_result",
        "llm_invocation",
        "llm_response",
    ]
    first_response = signals[signal_types(signals).index("llm_response")]
    calls = [p["function_call"] for p in first_response["data"]["content"]["parts"] if "function_call" in p]
    assert [(c["id"], c["name"]) for c in calls] == [(lookup.id, lookup.name)]
    start = next(s for s in signals if s["type"] == "tool_invocation_start")
    result = next(s for s in signals if s["type"] == "tool_result")
    assert start["function_call_id"] == result["function_call_id"] == lookup.id
    assert start["tool_name"] == result["tool_name"] == lookup.name
    assert result["result_data"] == {"claim": "C-1", "status": "open"}
    reported = [s["usage"] for s in signals if s["type"] == "llm_response"]
    assert [(u["input_tokens"], u["output_tokens"]) for u in reported] == [(120, 30), (200, 15)]

    usage = view.final()["result"]["metadata"]["token_usage"]
    assert (usage["total_input_tokens"], usage["total_output_tokens"]) == (320, 45)
    assert (view.record["totalInputTokens"], view.record["totalOutputTokens"]) == (320, 45)


def test_streamed_answer_reaches_the_user_in_order(mesh):
    chunks = ["Claim C-2 ", "was closed ", "on 3 March."]

    async def answer(task):
        for chunk in chunks:
            await task.stream(chunk)
        return "".join(chunks)

    mesh.start("stream", answer)
    mesh.client.wait_listed(mesh.name("stream"))
    task_id = mesh.client.send(mesh.name("stream"), "When was claim C-2 closed?")
    view = mesh.client.wait_done(task_id)

    assert view.status == "completed"
    assert view.streamed_text() == chunks
    assert view.answer() == "".join(chunks)
    replayed = [
        part["text"]
        for name, payload in mesh.client.sse(task_id)
        if name == "status_update"
        for part in (((payload.get("result") or {}).get("status") or {}).get("message") or {}).get("parts", [])
        if part.get("kind") == "text"
    ]
    assert replayed == chunks


def test_cancelled_task_stops_the_agent_and_ends_canceled(mesh):
    async def answer(task):
        await task.wait_cancelled(60)
        return "finished without being cancelled"

    agent = mesh.start("cancel", answer)
    mesh.client.wait_listed(mesh.name("cancel"))
    task_id = mesh.client.send(mesh.name("cancel"), "Start a long claim review.")
    seen = agent.wait_for_task()
    assert seen.task_id == task_id
    assert seen.started.wait(10)

    resp = mesh.client.cancel(task_id)
    assert resp.status in (200, 202, 204), resp
    view = mesh.client.wait_done(task_id, timeout=30)

    assert view.status in ("canceled", "cancelled")
    assert seen.finished.wait(10), "the agent's handler kept running after SAM cancelled the task"
    assert seen.cancelled
    assert view.final()["result"]["status"]["state"] == "canceled"


@pytest.mark.parametrize("approve", [True, False], ids=["approved", "denied"])
def test_approval_prompt_reaches_the_user_and_the_answer_reaches_the_agent(mesh, tmp_path, approve):
    pay = ToolCall("call-ct-pay-1", "pay_claim", {"claim": "C-3", "amount": 120})

    async def answer(task):
        if not await task.request_approval(pay.name, pay.args):
            return "Payment of claim C-3 was not approved."
        await task.tool_call(pay, {"paid": True})
        return "Paid claim C-3."

    mesh.start("approval", answer)
    mesh.client.wait_listed(mesh.name("approval"))
    asked = mesh.client.cli_send(mesh.name("approval"), "Pay claim C-3.", tmp_path / "ask")
    prompts = sorted((tmp_path / "ask").glob("hil_request_*.json"))
    assert len(prompts) == 1, f"`sam task send` saw no approval prompt:\n{asked.stdout[-2000:]}\n{asked.stderr[-2000:]}"
    prompt = json.loads(prompts[0].read_text())
    assert prompt["source"] == "tool_approval"
    assert prompt["agent_name"] == mesh.name("approval")

    answered = mesh.client.cli_answer_approval(prompts[0], approve, tmp_path / "answer")
    assert answered.returncode == 0, f"{answered.stdout[-2000:]}\n{answered.stderr[-2000:]}"
    view = mesh.client.wait_done(prompt["task_id"])

    assert view.status == "completed"
    if approve:
        assert view.answer() == "Paid claim C-3."
        assert "tool_result" in signal_types(view.signals())
    else:
        assert view.answer() == "Payment of claim C-3 was not approved."
        assert "tool_invocation_start" not in signal_types(view.signals())


def test_peer_call_reaches_the_peer_and_its_steps_nest_under_the_callers_task(mesh):
    """The caller's own stream carries the peer's steps too, which is what the activity view draws under it."""
    read = ToolCall("call-ct-policy-1", "read_policy", {"policy": "P-9"})

    async def callee(task):
        await task.model_call(MODEL, tool_calls=(read,), input_tokens=40, output_tokens=8)
        await task.tool_call(read, {"policy": "P-9", "active": True})
        return "Policy P-9 is active."

    async def caller(task):
        outcome = await task.call_peer(mesh.name("callee"), "Is policy P-9 active?", call_id="call-ct-ask-peer-1")
        return f"The policy desk says: {outcome.text}" if outcome.ok else f"The peer call failed: {outcome.text}"

    peer = mesh.start("callee", callee)
    mesh.client.wait_listed(mesh.name("callee"))
    mesh.start("caller", caller, peers=(mesh.name("callee"),))
    mesh.client.wait_listed(mesh.name("caller"))
    task_id = mesh.client.send(mesh.name("caller"), "Check policy P-9 with the policy desk.")
    view = mesh.client.wait_done(task_id, timeout=60)

    assert view.status == "completed"
    assert view.answer() == "The policy desk says: Policy P-9 is active."
    assert len(peer.tasks) == 1
    sub_task_id = peer.tasks[0].task_id
    assert sub_task_id != task_id
    nested = view.entry(sub_task_id)
    assert nested["subject"]["name"] == mesh.name("callee")
    peer_steps = view.signals(sub_task_id)
    assert [s["function_call_id"] for s in peer_steps if s["type"] == "tool_result"] == [read.id]
    forwarded = [s for s in view.signals() if s.get("type") == "tool_result"]
    assert [s["function_call_id"] for s in forwarded] == [read.id]


def test_peer_call_spend_is_booked_under_the_agent_the_user_invoked(mesh):
    async def callee(task):
        await task.model_call(MODEL, text="Policy P-10 is active.", input_tokens=21, output_tokens=4)
        return "Policy P-10 is active."

    async def caller(task):
        outcome = await task.call_peer(mesh.name("callee"), "Is policy P-10 active?")
        return outcome.text

    mesh.start("callee", callee)
    mesh.client.wait_listed(mesh.name("callee"))
    mesh.start("caller", caller, peers=(mesh.name("callee"),))
    mesh.client.wait_listed(mesh.name("caller"))
    view = mesh.client.wait_done(mesh.client.send(mesh.name("caller"), "Check policy P-10."), timeout=60)
    assert view.status == "completed"
    sub_task_id = next(e["taskId"] for e in view.entries if e["taskId"] != view.task_id)

    rows = ledger_rows(mesh.link.server, sub_task_id)
    if rows is None:
        pytest.skip("this SAM keeps no spend ledger to read")
    deadline = time.monotonic() + 15
    while not rows and time.monotonic() < deadline:
        time.sleep(0.25)
        rows = ledger_rows(mesh.link.server, sub_task_id)
    assert [(r["agent_name"], r["user_sub"]) for r in rows] == [(mesh.name("callee"), TEST_USER)]
    assert (rows[0]["root_kind"], rows[0]["root_name"]) == ("agent", mesh.name("caller"))


def test_peer_outside_the_callers_allowlist_is_refused(mesh):
    outcomes = {}

    async def outsider(task):
        return "the outsider should never be reached"

    async def guarded(task):
        target = mesh.name("outsider")
        started = time.monotonic()
        outcomes["sdk"] = await task.call_peer(target, "Approve claim C-4.")
        outcomes["sam"] = await task.call_peer(target, "Approve claim C-4.", skip_local_checks=True)
        outcomes["sam_seconds"] = time.monotonic() - started
        return "done"

    reached = mesh.start("outsider", outsider)
    mesh.client.wait_listed(mesh.name("outsider"))
    mesh.start("guarded", guarded)
    mesh.client.wait_listed(mesh.name("guarded"))
    task_id = mesh.client.send(mesh.name("guarded"), "Ask the outsider to approve claim C-4.")
    view = mesh.client.wait_done(task_id, timeout=60)

    assert view.status == "completed"
    assert not outcomes["sdk"].ok
    assert "not available" in outcomes["sdk"].text, outcomes["sdk"]
    assert not outcomes["sam"].ok
    assert "SAM refused the peer call" in outcomes["sam"].text, outcomes["sam"]
    assert outcomes["sam_seconds"] < 30, "a refused peer call should fail at once, not wait out its timeout"
    assert reached.tasks == []
    assert [e for e in view.entries if e.get("taskId") != task_id] == []


def test_stopped_agent_gets_no_tasks_until_released(mesh):
    """SAM may take a stopped agent's card down, and then refuses the task at submission (agent not found) rather than
    failing it at the entrypoint; either way it must never reach the agent.
    """

    async def answer(task):
        return "Claim C-5 is open."

    peer_outcomes = []

    async def ask_stopped_peer(task):
        """The stopped agent's card is gone, so the caller's own client would not even try: send the call anyway, so the
        refusal seen is SAM's entrypoint enforcing the stop, not the stopped agent obeying it.
        """
        peer_outcomes.append(await task.call_peer(mesh.name("stop"), "Status of claim C-5?", skip_local_checks=True))
        return "asked"

    agent = mesh.start("stop", answer)
    name = mesh.name("stop")
    mesh.client.wait_listed(name)
    mesh.start("stop-caller", ask_stopped_peer)
    mesh.client.wait_listed(mesh.name("stop-caller"))
    stop = mesh.client.stop_agent(name, "contract test")
    if stop.status in NO_STOP_API:
        pytest.skip(
            f"the SAM under test has no agent stop API (POST /api/v1/platform/stoppedAgents answered "
            f"{stop.status}); its agent link refuses stopped agents' tasks but nothing can stop one"
        )
    assert stop.status == 201, stop
    stop_id = stop.body["id"]
    try:
        wait_until(lambda: agent.control and agent.control[-1][0] is False, 15, "the agent to hear the stop")
        submitted = mesh.client.submit(name, "What is the status of claim C-5?")
        if submitted.status == 200:
            view = mesh.client.wait_done(submitted.body["result"]["id"])
            assert view.status == "failed"
            assert "stopped" in (view.error_message or "").lower(), view.error_message
        else:
            assert submitted.status == 404, submitted
        asked = mesh.client.wait_done(mesh.client.send(mesh.name("stop-caller"), "Ask the stopped agent."), timeout=60)
        assert asked.status == "completed"
        assert len(peer_outcomes) == 1 and not peer_outcomes[0].ok, peer_outcomes
        assert "stopped" in peer_outcomes[0].text.lower(), peer_outcomes[0]
        assert agent.tasks == [], "SAM delivered a task to an agent it had stopped"
    finally:
        released = mesh.client.release_agent(stop_id)
    assert released.status == 204, released
    wait_until(lambda: agent.control and agent.control[-1][0] is True, 15, "the agent to hear the release")
    mesh.client.wait_listed(name)
    task_id = mesh.client.send(name, "What is the status of claim C-5?")
    view = mesh.client.wait_done(task_id)
    assert view.status == "completed"
    assert view.answer() == "Claim C-5 is open."


def test_wrong_link_token_is_refused(mesh):
    name = mesh.name("token")
    wrong = mesh.driver.try_connect(name, "not-the-agent-token")
    assert not wrong.accepted
    assert "401" in wrong.detail, wrong.detail
    borrowed = mesh.driver.try_connect(name, mesh.token("listing"))
    assert not borrowed.accepted, "SAM accepted one agent's token for another agent"
    assert "401" in borrowed.detail, borrowed.detail
    right = mesh.driver.try_connect(name, mesh.token("token"))
    assert right.accepted, right.detail
    assert mesh.client.agent_card(name) is None


def test_handler_exception_becomes_a_failed_task(mesh):
    """The link's result error for a failure that is not a model's is the JSON-RPC error a SAM-Go agent sends."""

    async def answer(task):
        await task.model_call(MODEL, input_tokens=10, output_tokens=2)
        raise RuntimeError("the claims database is unreachable")

    agent = mesh.start("fails", answer)
    mesh.client.wait_listed(mesh.name("fails"))
    task_id = mesh.client.send(mesh.name("fails"), "What is the status of claim C-6?")
    view = mesh.client.wait_done(task_id)

    assert view.status == "failed"
    assert view.error_message, "SAM recorded no error message for the failed task"
    assert "claims database" not in view.error_message, "the handler's exception text leaked to the user"
    final = view.final()
    assert final.get("error", {}).get("code") == -32603, final
    assert final["error"]["message"] == "internal error", final
    assert agent.tasks[0].error.startswith("RuntimeError")


def test_model_call_spend_is_recorded_for_the_agent_and_the_user(mesh):
    own_before = mesh.client.own_usage()
    if own_before.status == 404:
        pytest.skip("the SAM under test serves no spend read API")
    assert own_before.status == 200, own_before
    before = own_before.body["totals"]["tokens"]

    async def answer(task):
        await task.model_call(MODEL, text="Claim C-7 is approved.", input_tokens=1234, output_tokens=56)
        return "Claim C-7 is approved."

    mesh.start("spend", answer)
    name = mesh.name("spend")
    mesh.client.wait_listed(name)
    task_id = mesh.client.send(name, "Is claim C-7 approved?")
    view = mesh.client.wait_done(task_id)
    assert view.status == "completed"

    def agent_totals():
        resp = mesh.client.agent_usage(name)
        totals = resp.body.get("totals") if resp.status == 200 and isinstance(resp.body, dict) else None
        return totals if totals and totals.get("calls") else None

    totals = wait_until(agent_totals, SPEND_ROLLUP_WAIT_SECONDS, f"SAM's spend records for {name}")
    assert totals["calls"] == 1
    assert (totals["tokens"]["uncachedInput"], totals["tokens"]["output"]) == (1234, 56)

    def own_tokens():
        resp = mesh.client.own_usage()
        tokens = resp.body["totals"]["tokens"] if resp.status == 200 else None
        return tokens if tokens and tokens["uncachedInput"] >= before["uncachedInput"] + 1234 else None

    after = wait_until(own_tokens, SPEND_ROLLUP_WAIT_SECONDS, f"the spend to be attributed to {TEST_USER}")
    assert after["output"] >= before["output"] + 56

    rows = ledger_rows(mesh.link.server, task_id)
    if rows is not None:
        assert [(r["agent_name"], r["user_sub"], r["uncached_input_tokens"], r["output_tokens"]) for r in rows] == [
            (name, TEST_USER, 1234, 56)
        ]


def ledger_rows(server, task_id: str) -> list[dict] | None:
    """SAM's per-call spend ledger rows for one task, read-only; None when this SAM keeps no such table."""
    path = server.home / "data" / "platform.db"
    if not path.is_file():
        return None
    con = sqlite3.connect(f"file:{path}?mode=ro", uri=True, timeout=5)
    con.row_factory = sqlite3.Row
    try:
        rows = con.execute("SELECT * FROM token_spend_ledger WHERE task_id = ?", (task_id,)).fetchall()
    except sqlite3.Error:
        return None
    finally:
        con.close()
    return [dict(r) for r in rows]


REDACTED = "<REDACTED>"
"""The agent sends tool arguments and results as they are (driver_link reports raw step events), so what SAM records is
SAM's own redaction and cap.
"""
MAX_STRING_BYTES = 16384


def test_credentials_in_tool_arguments_and_results_never_reach_sams_records(mesh):
    password, api_token = "hunter2-not-a-real-secret", "tok-not-a-real-secret-1234567890"
    connect = ToolCall("call-ct-connect-1", "connect_claims_db", {"host": "db.example.com", "password": password})

    async def answer(task):
        await task.model_call(MODEL, tool_calls=(connect,), input_tokens=12, output_tokens=3)
        await task.tool_call(connect, {"connected": True, "api_token": api_token})
        return "Connected to the claims database."

    mesh.start("redact", answer)
    mesh.client.wait_listed(mesh.name("redact"))
    view = mesh.client.wait_done(mesh.client.send(mesh.name("redact"), "Connect to the claims database."))

    assert view.status == "completed"
    signals = view.signals()
    start = next(s for s in signals if s["type"] == "tool_invocation_start")
    result = next(s for s in signals if s["type"] == "tool_result")
    response = next(s for s in signals if s["type"] == "llm_response")
    [call] = [p["function_call"] for p in response["data"]["content"]["parts"] if "function_call" in p]
    assert start["tool_args"] == {"host": "db.example.com", "password": REDACTED}
    assert call["args"] == {"host": "db.example.com", "password": REDACTED}
    assert result["result_data"] == {"connected": True, "api_token": REDACTED}
    recorded = json.dumps(view.entries)
    assert password not in recorded and api_token not in recorded


def test_an_oversized_tool_result_string_is_capped_in_sams_records(mesh):
    export = ToolCall("call-ct-export-1", "export_claims", {"year": 2026})
    report = "claim C-8 approved; " * 4000

    async def answer(task):
        await task.tool_call(export, {"report": report})
        return "Exported."

    mesh.start("redact", answer)
    mesh.client.wait_listed(mesh.name("redact"))
    view = mesh.client.wait_done(mesh.client.send(mesh.name("redact"), "Export this year's claims."))

    assert view.status == "completed"
    result = next(s for s in view.signals() if s["type"] == "tool_result")
    capped = result["result_data"]["report"]
    assert len(capped.encode("utf-8")) <= MAX_STRING_BYTES
    assert capped.startswith("claim C-8 approved; ") and "truncated" in capped


def test_a_questions_prompt_reaches_the_user_and_the_answers_reach_the_agent(mesh, tmp_path):
    async def answer(task):
        answers = await task.ask_questions((("Which coverage tier?", ("Gold", "Silver")), ("Why is it urgent?", ())))
        if answers is None:
            return "The user did not answer."
        return f"Tier {answers[0]}, because {answers[1]}."

    mesh.start("questions", answer)
    mesh.client.wait_listed(mesh.name("questions"))
    asked = mesh.client.cli_send(mesh.name("questions"), "Open a claim for policy P-11.", tmp_path / "ask")
    prompts = sorted((tmp_path / "ask").glob("hil_request_*.json"))
    assert len(prompts) == 1, f"`sam task send` saw no questions:\n{asked.stdout[-2000:]}\n{asked.stderr[-2000:]}"
    prompt = json.loads(prompts[0].read_text())
    assert prompt["source"] == "ask_user_question"

    answered = mesh.client.cli_answer_questions(prompts[0], ["Gold", "the roof is leaking"], tmp_path / "answer")
    assert answered.returncode == 0, f"{answered.stdout[-2000:]}\n{answered.stderr[-2000:]}"
    view = mesh.client.wait_done(prompt["task_id"])

    assert view.status == "completed"
    assert view.answer() == "Tier Gold, because the roof is leaking."


def test_a_multi_select_question_comes_back_as_the_chosen_labels_joined(mesh, tmp_path):
    async def answer(task):
        answers = await task.ask_questions((("Which perils apply?", ("Fire", "Flood", "Theft"), True),))
        return "no answer" if answers is None else f"Perils: {answers[0]}."

    mesh.start("multiselect", answer)
    mesh.client.wait_listed(mesh.name("multiselect"))
    asked = mesh.client.cli_send(mesh.name("multiselect"), "Assess claim C-7.", tmp_path / "ask")
    prompts = sorted((tmp_path / "ask").glob("hil_request_*.json"))
    assert len(prompts) == 1, f"`sam task send` saw no questions:\n{asked.stdout[-2000:]}\n{asked.stderr[-2000:]}"
    prompt = json.loads(prompts[0].read_text())
    surface = json.dumps(prompt)
    assert "multipleSelection" in surface, "SAM did not show the question as a multi-select"

    answered = mesh.client.cli_answer_questions(prompts[0], [["Fire", "Theft"]], tmp_path / "answer")
    assert answered.returncode == 0, f"{answered.stdout[-2000:]}\n{answered.stderr[-2000:]}"
    view = mesh.client.wait_done(prompt["task_id"])
    assert view.status == "completed"
    assert view.answer() == "Perils: Fire, Theft."


def test_a_prompt_with_more_questions_than_the_link_takes_is_stopped_in_the_agent(mesh):
    """The limits the SDK kept are the ones SAM announced (SAM-Go's defaults, which the SDK also assumes without them)."""
    from sam_link_sdk import PromptTooLarge

    async def answer(task):
        try:
            await task.ask_questions(tuple((f"Question {i}?", ()) for i in range(5)))
        except PromptTooLarge as exc:
            return f"Not asked: {exc}."
        return "asked"

    agent = mesh.start("toomany", answer)
    mesh.client.wait_listed(mesh.name("toomany"))
    task_id = mesh.client.send(mesh.name("toomany"), "Open a claim.")
    view = mesh.client.wait_done(task_id)
    assert view.status == "completed"
    assert view.answer() == "Not asked: the agent link asks at most 4 questions in one prompt; this one asks 5."
    assert "prompt" not in [t for _, t in agent.sent], "the agent sent the prompt instead of stopping it"
    announced = agent.agent._transport._welcome["limits"]
    assert {
        k: announced.get(k)
        for k in (
            "maxFrameValues",
            "maxPromptQuestions",
            "maxPromptOptions",
            "maxPromptTextBytes",
            "maxQuestionHeaderChars",
            "maxApprovalArgs",
            "maxApprovalArgsBytes",
            "maxToolPayloadBytes",
        )
    } == {
        "maxFrameValues": 16384,
        "maxPromptQuestions": 4,
        "maxPromptOptions": 4,
        "maxPromptTextBytes": 2048,
        "maxQuestionHeaderChars": 12,
        "maxApprovalArgs": 64,
        "maxApprovalArgsBytes": 65536,
        "maxToolPayloadBytes": 262144,
    }


def test_a_workflow_node_task_brings_its_input_and_returns_structured_output(mesh, tmp_path):
    """The web gateway stores an uploaded file as an artifact and passes the node its address, so the input arrives by
    reference, as a file (an input sent inline arrives as `input`; a workflow sends it so, which this harness cannot
    drive: it runs no workflow).
    """
    seen = {}

    async def answer(task):
        seen.update(workflow=task.workflow, input=task.input, files=task.files)
        task.output = {"covered": True, "limit": 5000}
        return "Policy P-12 covers the claim."

    agent = mesh.start("workflow", answer)
    mesh.client.wait_listed(mesh.name("workflow"))
    schema = tmp_path / "input-schema.json"
    schema.write_text(
        json.dumps({"type": "object", "properties": {"policy": {"type": "string"}}, "required": ["policy"]})
    )
    node_input = tmp_path / "input.json"
    node_input.write_text(json.dumps({"policy": "P-12"}))
    sent = mesh.client.cli_send(
        mesh.name("workflow"),
        "Check the coverage.",
        tmp_path / "out",
        60,
        "--si-input-schema",
        str(schema),
        "--file",
        str(node_input),
    )
    assert sent.returncode == 0, f"{sent.stdout[-2000:]}\n{sent.stderr[-2000:]}"
    view = mesh.client.wait_done(agent.wait_for_task().task_id)

    assert view.status == "completed"
    assert seen["workflow"] is not None and seen["workflow"][0] == "cli-test"
    assert seen["input"] is None
    [(name, inline, uri)] = seen["files"]
    assert (
        (name, inline) == ("input.json", False)
        and uri.startswith("artifact://")
        and uri.endswith("input.json?version=0")
    )
    final = view.final()["result"]["status"]["message"]["parts"]
    assert [p["data"] for p in final if p.get("kind") == "data"] == [{"covered": True, "limit": 5000}]
    assert view.answer() == "Policy P-12 covers the claim."


def test_an_offer_without_the_protocol_is_closed_4400(mesh):
    import asyncio

    from websockets.asyncio.client import connect
    from websockets.exceptions import ConnectionClosed

    name = mesh.name("raw")

    async def offer_v1() -> tuple[int | None, str]:
        async with connect(
            mesh.link.server.link_url,
            subprotocols=["sam-agent-link.v1"],
            additional_headers={"Authorization": f"Bearer {mesh.token('raw')}", "Sam-Agent": name},
            open_timeout=10,
        ) as ws:
            try:
                await asyncio.wait_for(ws.recv(), 10)
            except ConnectionClosed as exc:
                return exc.rcvd.code if exc.rcvd else None, exc.rcvd.reason if exc.rcvd else ""
        return None, "not closed"

    assert asyncio.run(offer_v1()) == (4400, "subprotocol sam-agent-link.v2 required")


def test_a_consume_before_describe_is_refused_and_the_sdk_never_sends_one(mesh):
    """The SDK tells this refusal from a refused describe (both have no ref) by the frame it names."""
    import asyncio
    import json as json_

    from websockets.asyncio.client import connect

    name = mesh.name("raw")

    async def consume_first() -> dict:
        async with connect(
            mesh.link.server.link_url,
            subprotocols=["sam-agent-link.v2"],
            additional_headers={"Authorization": f"Bearer {mesh.token('raw')}", "Sam-Agent": name},
            open_timeout=10,
        ) as ws:
            await ws.send(json_.dumps({"type": "consume"}))
            while True:
                frame = json_.loads(await asyncio.wait_for(ws.recv(), 10))
                if frame["type"] == "error":
                    return frame

    refused = asyncio.run(consume_first())
    assert (refused["code"], refused.get("ref", "")) == ("bad_message", "")
    assert refused.get("frame") == "consume", refused

    async def answer(task):
        return "ok"

    agent = mesh.start("reconnect", answer)
    mesh.client.wait_listed(mesh.name("reconnect"))
    agent.drop_connection()
    wait_until(
        lambda: (
            len({c for c, _ in agent.sent}) >= 2 and ("consume" in [t for c, t in agent.sent if c == agent.sent[-1][0]])
        ),
        20,
        "the agent to reconnect and consume",
    )
    for connection in dict.fromkeys(c for c, _ in agent.sent):
        sent = [t for c, t in agent.sent if c == connection]
        assert sent.index("describe") < sent.index("consume"), (connection, sent)
    view = mesh.client.wait_done(mesh.client.send(mesh.name("reconnect"), "Still there?"))
    assert view.status == "completed"


def test_a_stopped_agent_is_delisted_and_relisted_by_sam_without_a_word_from_the_agent(mesh):
    async def answer(task):
        return "ok"

    agent = mesh.start("relist", answer)
    name = mesh.name("relist")
    mesh.client.wait_listed(name)
    stop = mesh.client.stop_agent(name, "contract test")
    if stop.status in NO_STOP_API:
        pytest.skip(f"the SAM under test has no agent stop API ({stop.status})")
    assert stop.status == 201, stop
    sent_before = len(agent.sent)
    try:
        mesh.client.wait_delisted(name)
    finally:
        released = mesh.client.release_agent(stop.body["id"])
    assert released.status == 204, released
    mesh.client.wait_listed(name)
    assert [t for _, t in agent.sent[sent_before:] if t in ("describe", "retire")] == []
    assert [allowed for allowed, _ in agent.control] == [False, True]


def test_one_replica_leaving_keeps_the_agent_listed_until_the_last_one_leaves(mesh):
    async def answer(task):
        return "ok"

    first = mesh.start("replica", answer)
    second = mesh.start("replica", answer)
    name = mesh.name("replica")
    mesh.client.wait_listed(name)
    first.leave()
    assert "retire" in [t for _, t in first.sent]
    deadline = time.monotonic() + 5
    while time.monotonic() < deadline:
        assert mesh.client.agent_card(name) is not None, "one replica's retire took the card down"
        time.sleep(0.5)
    second.leave()
    mesh.client.wait_delisted(name)


def test_a_scoped_tool_runs_only_for_a_user_sam_granted_its_scope(mesh):
    async def answer(task):
        return " ".join(
            f"{tool}={'yes' if task.tool_permitted(tool) else 'no'}"
            for tool in ("lookup_claim", "pay_claim", "void_claim")
        )

    mesh.start(
        "scoped",
        answer,
        tools=(("lookup_claim", "Looks up a claim."), ("pay_claim", "Pays a claim."), ("void_claim", "Voids a claim.")),
        tool_scopes={"pay_claim": (TOOL_SCOPE_HELD,), "void_claim": (TOOL_SCOPE_NOT_HELD,)},
    )
    card = mesh.client.wait_listed(mesh.name("scoped"))
    tools = {
        t["name"]: t.get("required_scopes") or t.get("requiredScopes") for t in extension(card, EXT_TOOLS)["tools"]
    }
    assert tools["pay_claim"] == [TOOL_SCOPE_HELD] and tools["void_claim"] == [TOOL_SCOPE_NOT_HELD], tools
    view = mesh.client.wait_done(mesh.client.send(mesh.name("scoped"), "Which tools may I use?"))
    assert view.status == "completed"
    assert view.answer() == "lookup_claim=yes pay_claim=yes void_claim=no"


def test_a_description_sam_refuses_is_never_followed_by_tasks(mesh):
    """The refused description is never followed by tasks, even though SAM may still list a card.

    SAM-Go publishes the card to the broker when it takes the describe and sends the agent no verdict, so SAM's HTTP
    gateway lists the agent a moment after start returns; once it is listed there is no settle period either. The second
    describe names 40 other scopes, 80 over the two describes and past SAM's 64, so it is refused. SAM then lists no
    card for the agent (the first describe retired it and the second was refused), so it refuses a task at submission;
    one it took anyway must still never reach the refused connection.
    """
    from .driver import AgentSpec
    from .driver_link import _V2Agent

    async def answer(task):
        return "ok"

    def wide(prefix: str) -> AgentSpec:
        tools = tuple((f"{prefix}_{i}", "A narrow tool.") for i in range(40))
        return AgentSpec(
            mesh.name("scopes-redescribed"),
            answer,
            tools=tools,
            tool_scopes={name: (f"tool:ct-wide:{name}",) for name, _ in tools},
        )

    name = mesh.name("scopes-redescribed")
    first = mesh.start("scopes-redescribed", answer, tools=wide("a").tools, tool_scopes=wide("a").tool_scopes)
    mesh.client.wait_listed(name)
    began = time.monotonic()
    view = mesh.client.wait_done(mesh.client.send(name, "Right away."), timeout=15)
    assert view.status == "completed" and time.monotonic() - began < 10, view.status
    first.leave()
    again = _V2Agent(mesh.driver, wide("b"), mesh.token("scopes-redescribed"))
    try:
        mesh.driver.loop.run(again.start())
        wait_until(lambda: again.agent._transport._describer.refusal, 15, "SAM to refuse the description")
        assert "64" in again.agent._transport._describer.refusal
        sent = [t for _, t in again.sent]
        assert sent.index("describe") < sent.index("consume"), sent
        submitted = mesh.client.submit(name, "Nobody should take this.")
        if submitted.status == 200:
            time.sleep(3)
            mesh.client.cancel(submitted.body["result"]["id"])
        else:
            assert submitted.status == 404, submitted
        assert again.tasks == [], "SAM delivered a task under a refused description"
    finally:
        again.close()


def test_a_refused_describe_is_followed_by_a_refused_consume(mesh):
    import asyncio
    import json as json_

    from websockets.asyncio.client import connect

    name = mesh.name("raw")

    async def describe_badly_then_consume() -> list[dict]:
        async with connect(
            mesh.link.server.link_url,
            subprotocols=["sam-agent-link.v2"],
            additional_headers={"Authorization": f"Bearer {mesh.token('raw')}", "Sam-Agent": name},
            open_timeout=10,
        ) as ws:
            await ws.send(json_.dumps({"type": "describe", "agent": 5}))
            await ws.send(json_.dumps({"type": "consume"}))
            errors: list[dict] = []
            while len(errors) < 2:
                frame = json_.loads(await asyncio.wait_for(ws.recv(), 10))
                if frame["type"] == "error":
                    errors.append(frame)
            return errors

    described, consumed = asyncio.run(describe_badly_then_consume())
    assert (described.get("frame"), consumed.get("frame")) == ("describe", "consume"), (described, consumed)
    assert described["code"] == "bad_message" and consumed["code"] == "bad_message"


def test_a_consume_sent_again_after_an_accepted_describe_is_accepted(mesh):
    import asyncio
    import json as json_

    from websockets.asyncio.client import connect

    from sam_link_sdk import AgentConfig, MeshAgent
    from sam_link_sdk.link import LinkConfig, LinkTransport
    from sam_link_sdk.link.v2 import describe_object

    name = mesh.name("raw")
    never_opened = LinkTransport(LinkConfig(url=mesh.link.server.link_url, agent=name, token="unused"))
    description = MeshAgent(
        AgentConfig(name=name, description="Answers claim questions."),
        lambda ctx: "ok",
        transport=never_opened,
    )._host.description()

    async def consume_twice() -> list[dict]:
        async with connect(
            mesh.link.server.link_url,
            subprotocols=["sam-agent-link.v2"],
            additional_headers={"Authorization": f"Bearer {mesh.token('raw')}", "Sam-Agent": name},
            open_timeout=10,
        ) as ws:
            await ws.send(json_.dumps({"type": "describe", "agent": 5}))
            await ws.send(json_.dumps({"type": "describe", "agent": describe_object(description)}))
            await ws.send(json_.dumps({"type": "consume"}))
            await ws.send(json_.dumps({"type": "consume"}))
            errors: list[dict] = []
            try:
                while True:
                    frame = json_.loads(await asyncio.wait_for(ws.recv(), 3))
                    if frame["type"] == "error":
                        errors.append(frame)
            except TimeoutError:
                return errors
            finally:
                await ws.send(json_.dumps({"type": "retire"}))

    errors = asyncio.run(consume_twice())
    assert [e.get("frame") for e in errors] == ["describe"], errors


def test_a_cancel_sent_while_the_agent_is_disconnected_stops_the_task_when_it_is_back(mesh):
    async def answer(task):
        await task.wait_cancelled(60)
        return "never"

    agent = mesh.start("held-cancel", answer)
    name = mesh.name("held-cancel")
    mesh.client.wait_listed(name)
    task_id = mesh.client.send(name, "A long look at claim C-9.")
    record = agent.wait_for_task()
    assert record.started.wait(20)
    agent.drop_connection(offline_seconds=4)
    assert mesh.client.cancel(task_id).status < 300
    view = mesh.client.wait_done(task_id, timeout=60)
    assert view.status == "canceled", view.status
    assert record.finished.wait(20) and record.cancelled


def test_a_task_waiting_for_a_slot_is_answered_when_sam_stops_the_agent(mesh):
    async def answer(task):
        await task.wait_cancelled(60)
        return "never"

    agent = mesh.start("queued", answer, max_concurrent_tasks=1)
    name = mesh.name("queued")
    mesh.client.wait_listed(name)
    first = mesh.client.send(name, "First claim.")
    agent.wait_for_task()
    second = mesh.client.send(name, "Second claim, queued behind the first.")
    wait_until(lambda: len(agent.agent._runner.tasks) == 2, 20, "the second task to wait for the first's slot")
    stop = mesh.client.stop_agent(name, "contract test")
    if stop.status in NO_STOP_API:
        pytest.skip(f"the SAM under test has no agent stop API ({stop.status})")
    assert stop.status == 201, stop
    try:
        for task_id in (first, second):
            view = mesh.client.wait_done(task_id, timeout=30)
            assert view.status == "failed", (task_id, view.status)
            assert "stopped" in (view.answer() + (view.error_message or "")).lower(), view.answer()
        assert [t.text for t in agent.tasks] == ["First claim."]
    finally:
        assert mesh.client.release_agent(stop.body["id"]).status == 204
