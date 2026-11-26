# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The entrypoint's second round of limits (``welcome.limits``), kept by the SDK before it sends, so a task is never
refused mid-way: what is cut (display text, tool payloads), what is refused to the code that asked (too many
questions, options or approval arguments), multi-select questions, truncated answers and peer results, and held
cancels."""

from __future__ import annotations

import asyncio
import json
import logging
import time

import pytest

from sam_link_sdk import Option, PromptTooLarge, Question
from sam_link_sdk._model import Approval, Prompt
from sam_link_sdk.events import ToolFinished, ToolStarted
from sam_link_sdk.link.v2 import (
    TRUNCATED_KEY,
    Limits,
    fit_tool_payload,
    json_values,
    prompt_answer,
    prompt_frame,
    tool_payload_bytes,
)
from sam_link_sdk.prompting import question_prompt
from sam_link_sdk.redact import redact_string

from .test_link import PEERS, entrypoint, events, eventually, results, started, task

NEW_LIMITS = {
    "maxFrameValues": 16384,
    "maxPromptQuestions": 4,
    "maxPromptOptions": 4,
    "maxPromptTextBytes": 2048,
    "maxQuestionHeaderChars": 12,
    "maxApprovalArgs": 64,
    "maxApprovalArgsBytes": 65536,
    "maxToolPayloadBytes": 262144,
}


@pytest.fixture(autouse=True)
def private_state_dir(tmp_path, monkeypatch):
    monkeypatch.setenv("HOME", str(tmp_path))


@pytest.fixture
async def server():
    s = await entrypoint().start()
    yield s
    await s.stop()


def answering(**fields):
    return lambda f: [
        {"type": "prompt_answer", "taskId": f["taskId"], "requestId": f["requestId"], "status": "answered", **fields}
    ]


def test_the_new_limits_are_read_from_the_welcome_with_sam_gos_defaults_for_absent_ones():
    assert Limits.from_welcome({}, 8 << 20) == Limits()
    defaults = Limits()
    assert (
        defaults.max_frame_values,
        defaults.max_prompt_questions,
        defaults.max_prompt_options,
        defaults.max_prompt_text_bytes,
        defaults.max_question_header_chars,
        defaults.max_approval_args,
        defaults.max_approval_args_bytes,
        defaults.max_tool_payload_bytes,
    ) == tuple(NEW_LIMITS.values())
    halved = Limits.from_welcome({k: v // 2 for k, v in NEW_LIMITS.items()}, 8 << 20)
    assert (halved.max_frame_values, halved.max_prompt_questions, halved.max_tool_payload_bytes) == (8192, 2, 131072)


def test_json_values_are_counted_as_the_entrypoint_counts_them():
    assert json_values({"a": "x", "b": [1, None, True]}) == 8
    assert json_values([]) == 1 and json_values("s") == 1


async def test_more_questions_than_the_link_takes_are_refused_to_the_handler_and_never_sent(server):
    got = []

    async def handler(ctx):
        try:
            await ctx.ask_user([Question(f"Q{i}?") for i in range(5)])
        except PromptTooLarge as exc:
            got.append(str(exc))
        return "done"

    agent = await started(server, handler)
    await server.send(task(1, "T1"))
    await eventually(lambda: results(server, "T1"))
    assert got == ["the agent link asks at most 4 questions in one prompt; this one asks 5"]
    assert server.of("prompt") == []
    await agent.stop()


async def test_more_options_than_the_link_takes_are_refused_and_four_are_asked(server):
    got = []

    async def handler(ctx):
        try:
            await ctx.ask_user(Question("Tier?", tuple(Option(f"T{i}") for i in range(5))))
        except PromptTooLarge as exc:
            got.append(str(exc))
        got.append(await ctx.ask_user(Question("Tier?", tuple(Option(f"T{i}") for i in range(4)))))
        return "done"

    server.responders["prompt"] = answering(answers=["T3"])
    agent = await started(server, handler)
    await server.send(task(1, "T1"))
    await eventually(lambda: results(server, "T1"))
    assert got[0] == "the agent link offers at most 4 options per question; question 1 offers 5"
    assert got[1].answers == ("T3",) and len(server.of("prompt")) == 1
    await agent.stop()


def test_display_texts_are_cut_and_a_cut_or_redacted_label_answers_with_what_was_offered():
    """The entrypoint answers with the label as it showed it: cut, or redacted."""
    limits = Limits()
    long_label = "Gold " + "x" * 3000
    secret_label = "token sk-ant-api03-" + "a" * 40
    options = (Option(long_label), Option(secret_label), Option("Bronze", value="b", description="d" * 3000))
    prompt = question_prompt([Question("Which " + "tier " * 600 + "?", options, header="Coverage tier choice")], "", 60)
    frame = prompt_frame("T1", prompt, limits)
    [q] = frame["questions"]
    assert len(q["question"].encode()) <= 2048 and "truncated" in q["question"]
    assert q["header"] == "Coverage tie" and len(q["header"]) == 12
    labels = [o["label"] for o in q["options"]]
    assert len(labels[0].encode()) <= 2048 and len(q["options"][2]["description"].encode()) <= 2048
    redacted = redact_string(secret_label)
    assert redacted != secret_label
    answers = [
        prompt_answer(prompt, prompt.request_id, {"status": "answered", "answers": [said]}, limits).answers[0]
        for said in (labels[0], redacted, "Bronze")
    ]
    assert answers == [long_label, secret_label, "b"]


async def test_a_multi_select_question_is_sent_and_its_joined_answer_maps_back(server):
    got = []

    async def handler(ctx):
        got.append(
            await ctx.ask_user(
                Question(
                    "Which perils?", (Option("Fire", value="fire"), Option("Flood"), Option("Theft")), multi_select=True
                )
            )
        )
        return "done"

    server.responders["prompt"] = answering(answers=["Fire, Flood"])
    agent = await started(server, handler)
    await server.send(task(1, "T1"))
    await eventually(lambda: results(server, "T1"))
    [prompt] = server.of("prompt")
    assert prompt["questions"][0]["multiSelect"] is True
    assert got[0].answers == ("fire, Flood",)
    await agent.stop()


def test_a_single_choice_question_sends_no_multi_select():
    prompt = question_prompt([Question("Tier?", (Option("Gold"),))], "", 60)
    assert "multiSelect" not in prompt_frame("T1", prompt, Limits())["questions"][0]


async def test_answers_the_entrypoint_cut_are_flagged_and_logged(server, caplog):
    got = []

    async def handler(ctx):
        got.append(await ctx.ask_user("Why?"))
        return "done"

    server.responders["prompt"] = answering(answers=["because…[truncated 10 bytes]"], truncated=True)
    agent = await started(server, handler)
    with caplog.at_level(logging.WARNING, logger="sam_link_sdk.link"):
        await server.send(task(1, "T1"))
        await eventually(lambda: results(server, "T1"))
    assert got[0].answers == ("because…[truncated 10 bytes]",)
    assert [r for r in caplog.records if "cut the user's answers" in r.getMessage()]
    await agent.stop()


async def test_approval_arguments_over_the_count_are_refused_and_long_strings_are_cut_to_show(server):
    got = []

    async def handler(ctx):
        try:
            await ctx.request_approval("bulk_update", {f"field{i}": i for i in range(65)})
        except PromptTooLarge as exc:
            got.append(str(exc))
        got.append(await ctx.request_approval("send_letter", {"body": "y" * 100_000, "to": "alice@example.com"}))
        return "done"

    server.responders["prompt"] = answering(approved=True)
    agent = await started(server, handler)
    await server.send(task(1, "T1"))
    await eventually(lambda: results(server, "T1"))
    assert got[0] == "the agent link shows at most 64 approval arguments; this approval has 65"
    [prompt] = server.of("prompt")
    args = prompt["approval"]["args"]
    assert len(json.dumps(args, separators=(",", ":")).encode()) <= 65536
    assert args["to"] == "alice@example.com" and "truncated" in args["body"]
    assert got[1].approved
    await agent.stop()


def test_approval_arguments_too_large_even_cut_are_refused():
    """Approval arguments too large even when cut are refused.

    Small strings cannot be cut further, so the arguments stay too large.
    """
    from sam_link_sdk.link.v2 import prompt_overflow

    many_small = {f"k{i}": list(range(400)) for i in range(60)}
    prompt = Prompt("r1", 60, approval=Approval("bulk", many_small))
    assert prompt_overflow(prompt, Limits()).startswith("the approval's arguments are larger than the agent link shows")


def test_a_tool_payload_over_the_limit_is_cut_string_by_string_and_still_reported():
    body = {
        "kind": "tool_finished",
        "callId": "c1",
        "name": "export",
        "result": {f"r{i}": "z" * 16000 for i in range(30)},
        "failed": False,
    }
    fitted = fit_tool_payload(body, Limits())
    assert tool_payload_bytes(fitted) <= 262144
    assert len(fitted["result"]) == 30 and all("truncated" in v for v in fitted["result"].values())


def test_a_payload_of_many_small_values_becomes_its_cut_json_text_arguments_staying_an_object():
    limits = Limits()
    started_body = {"kind": "tool_started", "callId": "c1", "name": "scan", "args": {"ids": list(range(40_000))}}
    fitted = fit_tool_payload(started_body, limits)
    assert set(fitted["args"]) == {TRUNCATED_KEY} and "truncated" in fitted["args"][TRUNCATED_KEY]
    assert tool_payload_bytes(fitted) <= limits.max_tool_payload_bytes
    assert json_values({"type": "event", "taskId": "T1", "event": fitted}) <= limits.max_frame_values
    calls = {
        "kind": "model_finished",
        "model": "m",
        "text": "",
        "toolCalls": [{"id": f"c{i}", "name": "scan", "args": {"ids": list(range(2000))}} for i in range(10)],
    }
    fitted_calls = fit_tool_payload(calls, limits)
    assert all(set(c["args"]) == {TRUNCATED_KEY} for c in fitted_calls["toolCalls"])
    assert json_values({"type": "event", "taskId": "T1", "event": fitted_calls}) <= limits.max_frame_values


def test_a_payload_within_the_limits_is_sent_as_it_is():
    body = {"kind": "tool_finished", "callId": "c1", "name": "lookup", "result": {"tier": "gold"}, "failed": False}
    assert fit_tool_payload(body, Limits()) == body


async def test_tool_steps_reach_the_entrypoint_within_the_payload_and_value_limits(server):
    async def handler(ctx):
        await ctx.emit_event(ToolStarted("c1", "scan", {"ids": list(range(30_000))}))
        await ctx.emit_event(ToolFinished("c1", "scan", {"pages": ["p" * 16000] * 40}))
        return "done"

    agent = await started(server, handler)
    await server.send(task(1, "T1"))
    await eventually(lambda: results(server, "T1"))
    [start] = events(server, "T1", "tool_started")
    [finish] = events(server, "T1", "tool_finished")
    assert TRUNCATED_KEY in start["args"]
    assert len(json.dumps(finish["result"], separators=(",", ":")).encode()) <= 262144
    assert all(json_values(f) <= 16384 for _, f in server.frames)
    await agent.stop()


async def test_a_result_holding_more_json_values_than_a_frame_takes_drops_its_data(server):
    from sam_link_sdk import TaskResult

    agent = await started(server, lambda ctx: TaskResult("Here are the rows.", data={"rows": list(range(20_000))}))
    await server.send(task(1, "T1"))
    [result] = await eventually(lambda: results(server, "T1"))
    assert "data" not in result and result["text"] == "Here are the rows."
    await agent.stop()


async def test_a_peer_result_the_entrypoint_cut_is_flagged_and_logged_once(server, caplog):
    got = []

    async def handler(ctx):
        got.append(await ctx.call_agent("policy-desk", "Status?"))
        return "asked"

    server.responders["peer_call"] = lambda f: [
        {
            "type": "peer_result",
            "subtaskId": f["subtaskId"],
            "state": "completed",
            "text": "Active…[truncated 9 bytes]",
            "truncated": True,
        }
    ]
    agent = await started(server, handler)
    with caplog.at_level(logging.WARNING, logger="sam_link_sdk.link"):
        await server.send(task(1, "T1"))
        await eventually(lambda: results(server, "T1"))
    assert got[0].truncated and got[0].ok
    assert len([r for r in caplog.records if "cut the peer's result" in r.getMessage()]) == 1
    await agent.stop()


async def test_a_held_cancel_after_a_reconnect_stops_a_running_task(server):
    halted = asyncio.Event()

    async def handler(ctx):
        try:
            await asyncio.sleep(30)
        except asyncio.CancelledError:
            halted.set()
            raise
        return "never"

    agent = await started(server, handler, link={"connect_timeout_seconds": 1})
    await server.send(task(1, "T1"))
    await eventually(lambda: "T1" in agent._runner.tasks)
    server.refusing = True
    await server.conns[0].close(code=1011)
    await eventually(lambda: not agent._transport._connection.connected)
    server.after_welcome = [PEERS, {"type": "cancel", "taskId": "T1"}]
    server.responders["consume"] = lambda f: [task(2, "T1", redelivered=True)]
    server.refusing = False
    [result] = await asyncio.wait_for(eventually(lambda: results(server, "T1")), 5)
    assert result["state"] == "canceled"
    await asyncio.wait_for(halted.wait(), 5)
    await agent.stop()


async def test_a_held_cancel_for_a_task_already_answered_is_ignored_without_error(server, caplog):
    agent = await started(server, lambda ctx: "done", link={"connect_timeout_seconds": 1})
    await server.send(task(1, "T1"))
    await eventually(lambda: results(server, "T1"))
    server.refusing = True
    await server.conns[0].close(code=1011)
    await eventually(lambda: not agent._transport._connection.connected)
    server.after_welcome = [PEERS, {"type": "cancel", "taskId": "T1"}]
    server.refusing = False
    with caplog.at_level(logging.INFO):
        await eventually(lambda: len(server.conns) == 2 and server.of("consume", conn=1))
        await asyncio.sleep(0.2)
    assert [r["state"] for r in results(server, "T1")] == ["completed"]
    assert not [r for r in caplog.records if r.levelno >= logging.ERROR]
    await agent.stop()


async def test_an_adapters_ask_tool_tells_the_model_a_question_it_could_not_ask():
    from sam_link_sdk._adapter import answer_text, ask

    class Ctx:
        async def ask_user(self, question):
            raise PromptTooLarge("the agent link offers at most 4 options per question; question 1 offers 6")

    text = answer_text(await ask(Ctx(), "Which?", ["a", "b", "c", "d", "e", "f"]))
    assert text == (
        "The question was not asked: the agent link offers at most 4 options per question; question 1 "
        "offers 6. Ask again within that limit."
    )


async def test_a_result_over_the_value_limit_keeps_its_files_and_drops_only_its_data(server):
    from sam_link_sdk import File, TaskResult

    agent = await started(
        server,
        lambda ctx: TaskResult(
            "Here are the rows.",
            data={"rows": list(range(20_000))},
            files=[File("summary.txt", b"x" * 100, mime_type="text/plain")],
        ),
    )
    await server.send(task(1, "T1"))
    [result] = await eventually(lambda: results(server, "T1"))
    assert "data" not in result and [f["name"] for f in result["files"]] == ["summary.txt"]
    assert result["text"] == "Here are the rows."
    await agent.stop()


async def test_a_result_over_the_byte_limit_leaves_out_its_largest_files_first_and_names_them():
    from sam_link_sdk import File, TaskResult

    server = await entrypoint(limits={"maxFrameBytes": 64 * 1024}).start()
    try:
        files = [
            File("small.txt", b"s" * 1000, mime_type="text/plain"),
            File("big.bin", b"b" * 60_000, mime_type="application/x"),
        ]
        agent = await started(server, lambda ctx: TaskResult("Done.", files=files))
        await server.send(task(1, "T1"))
        [result] = await eventually(lambda: results(server, "T1"))
        assert [f["name"] for f in result["files"]] == ["small.txt"]
        assert "big.bin" in result["text"] and "small.txt" not in result["text"]
        await agent.stop()
    finally:
        await server.stop()


async def test_a_model_past_the_per_task_cap_is_priced_by_its_own_name_without_a_configured_key(server):
    from sam_link_sdk import Usage
    from sam_link_sdk.events import ModelFinished

    async def handler(ctx):
        for i in range(33):
            ctx.record_usage(f"model-{i}", Usage(input_tokens=1, output_tokens=1))
            await ctx.emit_event(ModelFinished(f"model-{i}", ""))
        return "done"

    agent = await started(server, handler)
    await server.send(task(1, "T1"))
    await eventually(lambda: results(server, "T1"))
    finished = events(server, "T1", "model_finished")
    assert finished[-1]["model"] == "unknown" and finished[-1]["pricingKey"] == "model-32"
    assert "pricingKey" not in finished[0]
    await agent.stop()


def test_a_multi_select_answer_whose_labels_hold_commas_maps_back_to_the_values():
    labels = (Option("Fire, smoke", value="fire"), Option("Flood", value="flood"), Option("Fire", value="fire-only"))
    prompt = question_prompt([Question("Which perils?", labels, multi_select=True)], "", 60)
    assert prompt_answer(
        prompt, prompt.request_id, {"status": "answered", "answers": ["Fire, smoke, Flood"]}, Limits()
    ).answers == ("fire, flood",)
    assert prompt_answer(
        prompt, prompt.request_id, {"status": "answered", "answers": ["Fire, Flood"]}, Limits()
    ).answers == ("fire-only, flood",)
    assert prompt_answer(
        prompt, prompt.request_id, {"status": "answered", "answers": ["Hail, sleet"]}, Limits()
    ).answers == ("Hail, sleet",)


async def test_a_question_whose_options_read_the_same_is_refused_before_it_is_asked(server):
    got = []

    async def handler(ctx):
        for options in ((Option("Gold"), Option("Gold")), (Option("password=hunter2a"), Option("password=hunter2b"))):
            try:
                await ctx.ask_user([Question("Tier?", options)])
            except PromptTooLarge as exc:
                got.append(str(exc))
        return "done"

    agent = await started(server, handler)
    await server.send(task(1, "T1"))
    await eventually(lambda: results(server, "T1"))
    assert len(got) == 2 and all("read the same" in g for g in got)
    assert server.of("prompt") == []
    await agent.stop()


@pytest.mark.parametrize(
    "labels, answer",
    [
        pytest.param(("a", "a, a"), ", ".join(["a"] * 30) + ", x", id="30 parts of labels that compose"),
        pytest.param(("a", "a, a"), ", ".join(["a"] * 1200) + ", x", id="1200 parts"),
        pytest.param(("a", "a, a"), ", ".join(["a"] * 30), id="readable only by naming an option again"),
        pytest.param(("a", "a, a", "a, a, a", "b"), ", ".join(["a"] * 40) + ", c", id="an unreadable tail"),
        pytest.param(("Paris", "France", "Paris, France"), ", ".join(["Paris, France"] * 25) + ", Lyon", id="cities"),
    ],
)
def test_an_unreadable_multi_select_answer_is_taken_as_typed_quickly(labels, answer):
    prompt = question_prompt([Question("Where?", tuple(Option(label) for label in labels), multi_select=True)], "", 60)
    started_at = time.perf_counter()
    read = prompt_answer(prompt, prompt.request_id, {"status": "answered", "answers": [answer]}, Limits())
    assert time.perf_counter() - started_at < 0.1
    assert read.answers == (answer,)


def test_a_multi_select_answer_names_each_option_once_and_no_more_options_than_the_link_offers():
    prompt = question_prompt(
        [
            Question(
                "Where?",
                (
                    Option("Paris", value="paris"),
                    Option("France", value="fr"),
                    Option("Paris, France", value="paris-fr"),
                ),
                multi_select=True,
            )
        ],
        "",
        60,
    )

    def read(answer, limits=Limits()):
        return prompt_answer(prompt, prompt.request_id, {"status": "answered", "answers": [answer]}, limits).answers[0]

    assert read("Paris, France") == "paris-fr"
    assert read("France, Paris, France") == "fr, paris-fr"
    assert read("Paris, France, Paris") == "paris-fr, paris"
    assert read("Paris, Paris") == "Paris, Paris"
    assert read("France, Paris", Limits(max_prompt_options=1)) == "France, Paris"
