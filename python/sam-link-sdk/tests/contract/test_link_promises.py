# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Contract scenarios for what the docs promise an agent author and SAM can show: the card, files, the agent's own
required scopes, approval arguments and cache tokens."""

from __future__ import annotations

import base64
import json
import math
import os
import uuid

import pytest

from sam_link_sdk import DataAccess, McpServer, Skill, Suggestion

from .driver import ToolCall
from .roster import REGISTERED_OWNER, REGISTERED_TEAM
from .sam_client import SamApiError, wait_until
from .sam_server import TEST_USER, TOOL_SCOPE_HELD, TOOL_SCOPE_NOT_HELD
from .test_link_contract import EXT_PROVENANCE, EXT_REGISTRATION, MODEL, REDACTED, extension, ledger_rows, signal_types

pytestmark = pytest.mark.contract

EXT_SCHEMAS = "https://solace.com/a2a/extensions/sam/schemas"
EXT_WELCOME = "https://solace.com/a2a/extensions/sam/welcome"
EXT_ARTIFACT_ACCESS = "https://solace.com/a2a/extensions/sam/artifact-access"
EXT_REQUIRED_SCOPES = "https://solace.com/a2a/extensions/sam/required-scopes"
EXT_PEERS = "https://solace.com/a2a/extensions/peer-agent-topology"


def test_every_field_an_agent_describes_is_on_the_card_sam_serves(mesh):
    """SAM draws only the peers the entrypoint allows the agent to call: ct-outsider is declared but not allowed."""
    input_schema = {"type": "object", "properties": {"claim": {"type": "string"}}, "required": ["claim"]}
    output_schema = {"type": "object", "properties": {"covered": {"type": "boolean"}}}

    async def answer(task):
        return "ok"

    mesh.start(
        "card",
        answer,
        peers=("ct-callee", "ct-outsider"),
        config=dict(
            version="2.3.4",
            skills=[Skill("triage", "Triage", "Sorts incoming claims.")],
            input_schema=input_schema,
            output_schema=output_schema,
            welcome_message="Welcome to claims.",
            suggestions=[
                Suggestion("Open claims", "Show open claims", auto_send=True),
                Suggestion("Help", "What can you do?"),
            ],
            streaming=False,
            team="Claims Team",
            data_access=[DataAccess("claims-db", "confidential", "read-write")],
            mcp_servers=[McpServer("claims-mcp", "https://mcp.example.com")],
            required_scopes=[TOOL_SCOPE_HELD],
        ),
    )
    card = mesh.client.wait_listed(mesh.name("card"))

    assert card["version"] == "2.3.4"
    assert [(s["id"], s["name"], s["description"]) for s in card["skills"]] == [
        ("triage", "Triage", "Sorts incoming claims.")
    ]
    assert card["capabilities"]["streaming"] is False
    schemas = extension(card, EXT_SCHEMAS)
    assert (schemas["input_schema"], schemas["output_schema"]) == (input_schema, output_schema)
    welcome = extension(card, EXT_WELCOME)
    assert welcome["welcome_message"] == "Welcome to claims."
    assert welcome["suggestions"] == [
        {"label": "Open claims", "prompt": "Show open claims", "auto_send": True},
        {"label": "Help", "prompt": "What can you do?", "auto_send": False},
    ]
    assert extension(card, EXT_ARTIFACT_ACCESS) == {"artifact_store": False}
    declared = extension(card, EXT_PROVENANCE)
    assert declared["team"] == "Claims Team"
    assert declared["data_access"] == [
        {"system": "claims-db", "classification": "confidential", "access": "read-write"}
    ]
    assert declared["mcp_servers"] == [{"name": "claims-mcp", "url": "https://mcp.example.com"}]
    registered = extension(card, EXT_REGISTRATION)
    assert (registered["owner"], registered["team"]) == (REGISTERED_OWNER, REGISTERED_TEAM)
    assert extension(card, EXT_REQUIRED_SCOPES) == {"scopes": [TOOL_SCOPE_HELD]}
    assert extension(card, EXT_PEERS) == {"peer_agent_names": [mesh.name("callee")]}


def test_an_agent_requiring_a_scope_the_user_lacks_is_hidden_and_its_tasks_are_refused(mesh):
    """A scope the user lacks hides the agent and gets its tasks refused.

    The agent publishes its description after the link opens, and until SAM holds it a task gets "agent not found"; the
    agent is hidden from this user, so there is no listing to wait on. A refusal that says the scope is missing shows
    SAM holds the description, so the agent's absence from the list is the scope's doing.
    """

    async def answer(task):
        return "never"

    agent = mesh.start("gated", answer, config=dict(required_scopes=[TOOL_SCOPE_NOT_HELD]))
    name = mesh.name("gated")

    sent: list[str] = []

    def submitted() -> bool:
        try:
            sent.append(mesh.client.send(name, "Pay claim C-4."))
        except SamApiError as e:
            if "agent not found" not in str(e):
                raise
            return False
        return True

    wait_until(submitted, 30, "SAM to hold the gated agent's description")
    view = mesh.client.wait_done(sent[0])

    assert view.status == "failed"
    assert "insufficient scopes" in (view.error_message or "")
    assert mesh.client.agent_card(name) is None
    assert agent.tasks == [], "SAM delivered a task to an agent whose scopes the user lacks"


def test_an_agent_requiring_a_scope_the_user_holds_is_listed_and_serves_the_task(mesh):
    async def answer(task):
        return "Claim C-5 is open."

    agent = mesh.start("card", answer, config=dict(required_scopes=[TOOL_SCOPE_HELD]))
    mesh.client.wait_listed(mesh.name("card"))

    view = mesh.client.wait_done(mesh.client.send(mesh.name("card"), "Status of claim C-5?"))

    assert (view.status, view.answer()) == ("completed", "Claim C-5 is open.")
    assert [t.text for t in agent.tasks] == ["Status of claim C-5?"]


def send_with_file(client, agent: str, text: str, name: str, content: bytes, mime_type: str):
    part = {"kind": "file", "file": {"name": name, "mimeType": mime_type, "bytes": base64.b64encode(content).decode()}}
    message = {
        "role": "user",
        "kind": "message",
        "messageId": str(uuid.uuid4()),
        "parts": [{"kind": "text", "text": text}, part],
        "metadata": {"agent_name": agent},
    }
    body = {"jsonrpc": "2.0", "id": str(uuid.uuid4()), "method": "message/stream", "params": {"message": message}}
    return client.request("POST", "/api/v1/message:stream", body)


def test_a_file_sent_with_a_task_reaches_the_agent_and_a_file_it_attaches_reaches_the_user(mesh):
    """The pinned SAM's web gateway stores an uploaded file as an artifact and passes the agent its address, so the
    file arrives by `uri` and the agent cannot read its bytes. The same gateway does not store the agent's files in
    the user's session: the answer itself carries them, which is all the link protocol promises."""
    seen = {}
    report = b"claim,amount\nC-6,120\n"

    async def answer(task):
        seen.update(files=task.files, bytes=task.file_bytes)
        task.attach("report.csv", report, "text/csv")
        return "Attached the report."

    mesh.start("files", answer)
    mesh.client.wait_listed(mesh.name("files"))
    sent = send_with_file(mesh.client, mesh.name("files"), "Summarise this.", "claims.txt", b"C-6 paid", "text/plain")
    assert sent.status == 200, sent
    view = mesh.client.wait_done(sent.body["result"]["id"])

    assert view.status == "completed"
    [(name, inline, uri)] = seen["files"]
    assert (name, inline, seen["bytes"]) == ("claims.txt", False, {})
    assert uri.startswith("artifact://") and uri.endswith(f"/{view.record['sessionId']}/claims.txt?version=0"), uri
    parts = view.final()["result"]["status"]["message"]["parts"]
    files = [p["file"] for p in parts if p.get("kind") == "file"]
    assert [(f["name"], f["mimeType"], base64.b64decode(f["bytes"])) for f in files] == [
        ("report.csv", "text/csv", report)
    ]
    assert view.answer() == "Attached the report."


def test_a_credential_in_an_approvals_arguments_is_not_shown_to_the_user_or_recorded(mesh, tmp_path):
    secret = "hunter2-not-a-real-secret"

    async def answer(task):
        approved = await task.request_approval("connect_claims_db", {"host": "db.example.com", "password": secret})
        return "Connected." if approved else "Not approved."

    mesh.start("approval", answer)
    mesh.client.wait_listed(mesh.name("approval"))
    asked = mesh.client.cli_send(mesh.name("approval"), "Connect to the claims database.", tmp_path / "ask")
    prompts = sorted((tmp_path / "ask").glob("hil_request_*.json"))
    assert len(prompts) == 1, f"`sam task send` saw no approval prompt:\n{asked.stdout[-2000:]}\n{asked.stderr[-2000:]}"
    prompt = json.loads(prompts[0].read_text())
    shown = [
        c["text"]["literalString"]
        for c in prompt["surface"]["components"]
        if isinstance(c.get("text"), dict) and "literalString" in c["text"]
    ]

    assert secret not in prompts[0].read_text()
    assert [t for t in shown if t in ("host", "db.example.com", "password", REDACTED)] == [
        "host",
        "db.example.com",
        "password",
        REDACTED,
    ]

    answered = mesh.client.cli_answer_approval(prompts[0], False, tmp_path / "answer")
    assert answered.returncode == 0, f"{answered.stdout[-2000:]}\n{answered.stderr[-2000:]}"
    view = mesh.client.wait_done(prompt["task_id"])
    assert view.status == "completed"
    assert secret not in json.dumps(view.entries)
    assert secret not in mesh.link.server.log_text()


def test_cache_read_and_write_tokens_are_priced_at_their_own_rates(mesh):
    """A call of 1000 input tokens, 600 read from the cache and 100 written to it: 300 are uncached. At USD per
    million of 2 input, 10 output, 0.5 cache read and 3 cache write, SAM's ledger costs each bucket by its own rate
    (a cost is in units of 1e-10 USD)."""
    priced = mesh.client.request(
        "POST",
        "/api/v2/platform/models",
        {
            "alias": "ct-priced",
            "provider": "openai",
            "modelName": "gpt-4o-contract",
            "authConfig": {"type": "apikey", "apiKey": "unused"},
            "pricing": {"input": "2", "output": "10", "cacheRead": "0.5", "cacheWrite": "3"},
        },
    )
    if priced.status == 404:
        pytest.skip("the SAM under test has no model configuration API to price a model with")
    assert priced.status == 201, priced

    async def answer(task):
        await task.model_call(
            MODEL,
            text="Claim C-7 is approved.",
            input_tokens=1000,
            output_tokens=50,
            cached_input_tokens=600,
            cache_write_tokens=100,
        )
        return "Claim C-7 is approved."

    mesh.start("cache", answer, config=dict(spend_model_keys={MODEL: "ct-priced"}))
    name = mesh.name("cache")
    mesh.client.wait_listed(name)
    task_id = mesh.client.send(name, "Is claim C-7 approved?")
    view = mesh.client.wait_done(task_id)
    assert view.status == "completed"

    [response] = [s for s in view.signals() if s["type"] == "llm_response"]
    assert {
        k: response["usage"][k] for k in ("input_tokens", "output_tokens", "cached_input_tokens", "cache_write_tokens")
    } == {"input_tokens": 1000, "output_tokens": 50, "cached_input_tokens": 700, "cache_write_tokens": 100}
    totals = view.final()["result"]["metadata"]["token_usage"]
    assert (
        totals["total_input_tokens"],
        totals["total_output_tokens"],
        totals["total_cached_input_tokens"],
        totals["total_cache_write_tokens"],
    ) == (1000, 50, 700, 100)

    rows = wait_until(lambda: ledger_rows(mesh.link.server, task_id), 30, "SAM's spend record of the call")
    assert [
        (
            r["agent_name"],
            r["user_sub"],
            r["uncached_input_tokens"],
            r["output_tokens"],
            r["cache_read_tokens"],
            r["cache_write_tokens"],
        )
        for r in rows
    ] == [(name, TEST_USER, 300, 50, 600, 100)]
    assert [
        (r["uncached_input_cost"], r["output_cost"], r["cache_read_cost"], r["cache_write_cost"], r["total_cost"])
        for r in rows
    ] == [(6_000_000, 5_000_000, 3_000_000, 3_000_000, 17_000_000)]


@pytest.mark.parametrize(
    "shape",
    ["empty-file", "signal-data", "task-metadata-data"],
)
def test_an_answer_whose_file_or_data_sam_would_refuse_still_completes_the_task(mesh, shape):
    """SAM refuses a result carrying a file of no bytes or data shaped as one of its signals or as the task's own
    metadata (result.go, resultParts). The SDK leaves those out, so the user gets the answer at once rather than when
    SAM's task TTL expires."""

    async def answer(task):
        if shape == "empty-file":
            task.attach("rows.csv", b"", "text/csv")
        elif shape == "signal-data":
            task.output = {"next": {"type": "redirect", "url": "https://example.com"}}
        else:
            task.output = {"metadata": {"trace_id": "t-1"}, "covered": True}
        return "Claim C-8 is covered."

    mesh.start("refused-shapes", answer)
    name = mesh.name("refused-shapes")
    mesh.client.wait_listed(name)
    view = mesh.client.wait_done(mesh.client.send(name, "Is claim C-8 covered?"))

    assert view.status == "completed"
    assert view.answer().startswith("Claim C-8 is covered.")
    parts = view.final()["result"]["status"]["message"]["parts"]
    assert [p for p in parts if p.get("kind") in ("file", "data")] == []
    if shape == "empty-file":
        assert "rows.csv" in view.answer()


def test_values_json_cannot_carry_reach_sam_as_strict_json(mesh, tmp_path):
    """NaN and infinities become null, a file name read with surrogateescape keeps a replacement character, and an
    approval that waits for ever is still shown: SAM's decoder refuses a frame that is not strict JSON, which would
    leave the task unanswered."""
    name_bytes = b"report-\xff.csv"
    call = ToolCall("call-ct-stats-1", "claim_stats", {"claim": "C-9"})

    async def answer(task):
        await task.tool_call(call, {"mean": float("nan")})
        if not await task.request_approval("pay_claim", {"claim": "C-9"}, timeout_seconds=math.inf):
            return "Not approved."
        task.attach(os.fsdecode(name_bytes), b"claim,amount\n", "text/csv")
        task.output = {"mean": float("nan"), "max": float("inf"), "min": float("-inf")}
        return "Statistics for claim C-9."

    mesh.start("strict-json", answer)
    name = mesh.name("strict-json")
    mesh.client.wait_listed(name)
    asked = mesh.client.cli_send(name, "Statistics for claim C-9.", tmp_path / "ask")
    prompts = sorted((tmp_path / "ask").glob("hil_request_*.json"))
    assert len(prompts) == 1, f"`sam task send` saw no approval prompt:\n{asked.stdout[-2000:]}\n{asked.stderr[-2000:]}"
    prompt = json.loads(prompts[0].read_text())
    answered = mesh.client.cli_answer_approval(prompts[0], True, tmp_path / "answer")
    assert answered.returncode == 0, f"{answered.stdout[-2000:]}\n{answered.stderr[-2000:]}"
    view = mesh.client.wait_done(prompt["task_id"])

    assert view.status == "completed"
    assert view.answer() == "Statistics for claim C-9."
    assert signal_types(view.signals()).count("tool_result") == 1
    assert "NaN" not in json.dumps(view.entries)
    parts = view.final()["result"]["status"]["message"]["parts"]
    assert [p["data"] for p in parts if p.get("kind") == "data"] == [{"mean": None, "max": None, "min": None}]
    assert [p["file"]["name"] for p in parts if p.get("kind") == "file"] == ["report-�.csv"]


def test_tokens_recorded_for_one_model_are_never_billed_under_another(mesh):
    """`record_usage` for a model no step names, then a model call that reports no usage of its own: SAM files the
    tokens under the model they were recorded for."""
    other = "ct-other-model"

    async def answer(task):
        task.record_usage(other, input_tokens=5000, output_tokens=100)
        await task.model_call(MODEL, text="Claim C-10 is open.")
        return "Claim C-10 is open."

    mesh.start("usage-models", answer)
    name = mesh.name("usage-models")
    mesh.client.wait_listed(name)
    view = mesh.client.wait_done(mesh.client.send(name, "Is claim C-10 open?"))

    assert view.status == "completed"
    by_model = view.final()["result"]["metadata"]["token_usage"]["by_model"]
    assert list(by_model) == [other]
    assert (by_model[other]["input_tokens"], by_model[other]["output_tokens"]) == (5000, 100)
