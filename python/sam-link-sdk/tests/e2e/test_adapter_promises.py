# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The promises docs/behaviour.md, docs/handlers.md and docs/security.md make about a step, held to every framework's
example on a real Agent Mesh.

Each adapter builds its own steps from its framework's events (the model it called, the usage it read, the calls a
response asked for, a tool's outcome), so each promise here is one an adapter can break on a framework release. Like
test_examples_on_agent_mesh.py, a test asserts only on what Agent Mesh recorded and what the scripted model received.
"""

from __future__ import annotations

import dataclasses
import json

import pytest

from ..contract.sam_client import wait_until
from ..contract.sam_server import TOOL_SCOPE_HELD, TOOL_SCOPE_NOT_HELD
from .conftest import (
    ADAPTERS,
    APPROVAL,
    BLOCKED,
    BLOCKED_OUTPUT_WORD,
    BLOCKED_WORD,
    EVERY_EXAMPLE,
    FRAMEWORK_MAX_TURNS,
    FRAMEWORKS,
    FRAUD_SCREEN_SECONDS,
    HANDLED,
    LIMITED,
    NESTED,
    NESTED_MAX_LLM_CALLS,
    PLAIN,
    REPLY,
    STOP,
    STOP_MAX_LLM_CALLS,
    SYSTEM_PURPOSE,
    WHOLE,
)
from .model_script import Turn
from .test_examples_on_agent_mesh import (
    ACCESS_DENIED,
    ANSWER,
    ASK,
    EXT_TOOLS,
    PROMPT_TIMEOUT_SECONDS,
    TASK_TIMEOUT_SECONDS,
    TICKET,
    recorded,
    run_task,
    steps,
    triage_turns,
)

MAX_STRING_BYTES = 16384
"""Every string inside a tool's arguments and result is cut to this on the wire (docs/behaviour.md)."""
MODEL_SEES_ERROR_CUT = {"hermes": 2048}
"""Hermes cuts a tool's error text to 2048 characters before the model sees it (tools/registry.py `_bound_error_text`), and
lookup_policy reports its failure as an error, so the model gets this much of the long argument, not all of it.
"""
FAILED = "error"
"""What a tool result Agent Mesh shows as failed carries beside what the tool returned."""


@pytest.mark.parametrize("framework", EVERY_EXAMPLE, indirect=True)
def test_two_tool_calls_in_one_model_response_both_run_and_both_results_reach_the_model(examples):
    agent, view = run_task(
        examples,
        PLAIN,
        Turn(
            calls=(
                ("call_a", "lookup_policy", {"policy_id": "P-1182"}),
                ("call_b", "lookup_policy", {"policy_id": "P-2040"}),
            )
        ),
        Turn(text="P-1182 is gold and P-2040 is silver.", expects=("call_a", "call_b")),
    )

    assert agent.model.violations == []
    assert view.status == "completed", view.error_message
    starts = steps(view, "tool_invocation_start", "lookup_policy")
    assert sorted(s["function_call_id"] for s in starts) == ["call_a", "call_b"]
    assert len({s.get("parallel_group_id") for s in starts}) == 1
    results = {s["function_call_id"]: json.dumps(s["result_data"]) for s in steps(view, "tool_result", "lookup_policy")}
    assert "gold" in results["call_a"] and "silver" in results["call_b"]
    told = agent.model.requests[1].tool_results()
    assert "gold" in told["call_a"] and "silver" in told["call_b"]


@pytest.mark.parametrize("framework", EVERY_EXAMPLE, indirect=True)
def test_every_model_step_names_the_model_the_agent_called(examples, framework):
    """Named as the agent configured it or as its framework sent it to the provider: CrewAI's native OpenAI client
    sends `fake-model` for `openai/fake-model`. Never a placeholder such as "default" or "unknown"."""
    agent, view = run_task(examples, PLAIN, *triage_turns())

    assert view.status == "completed", view.error_message
    names = {framework.model, agent.model.requests[0].body["model"]}
    signals = view.signals()
    asked = [s["request"].get("model") for s in signals if s.get("type") == "llm_invocation"]
    answered = [s["usage"].get("model") for s in signals if s.get("type") == "llm_response"]
    assert len(asked) == len(answered) == 3
    assert set(asked) | set(answered) <= names, (asked, answered, names)


USAGE_AGENTS = [(fw, role) for fw in EVERY_EXAMPLE for role in (PLAIN, WHOLE) if role in FRAMEWORKS[fw].roles]
"""Every example's plain agent, and its whole-answer agent where it has one: a framework may read the usage of a streamed
response from another place than that of a whole one.
"""
REASONING_TOKENS = 6
NO_REASONING_TOKENS = {
    "strands": "Strands' usage has no field for them",
    "otel": "it runs a Strands agent",
}
"""The examples that do not tell SAM the reasoning tokens a response's usage reports
(`completion_tokens_details.reasoning_tokens`).
"""


@pytest.mark.parametrize("framework, role", USAGE_AGENTS, indirect=["framework"], scope="module")
def test_each_model_calls_token_usage_reaches_agent_mesh_and_adds_up_on_the_task(examples, framework, role):
    """Each count as the provider reported it, on each step and in the task's totals, so a usage field an adapter no
    longer finds fails here rather than leaving SAM's usage and cost records short."""
    first, second, last = triage_turns()
    _, view = run_task(examples, role, first, second, dataclasses.replace(last, reasoning_tokens=REASONING_TOKENS))

    assert view.status == "completed", view.error_message
    reported = [
        (s["usage"]["input_tokens"], s["usage"]["output_tokens"], s["usage"].get("reasoning_tokens"))
        for s in view.signals()
        if s.get("type") == "llm_response"
    ]
    reasoning = 0 if framework.id in NO_REASONING_TOKENS else REASONING_TOKENS
    assert reported == [(300, 20, 0), (300, 20, 0), (380, 15, reasoning)]
    assert (view.record["totalInputTokens"], view.record["totalOutputTokens"]) == (980, 55)


RENAMED_USAGE = ("tokens_in", "tokens_out")
"""A provider release that renamed the input and output counts of its usage (`total_tokens` keeps its name)."""
UNREAD_USAGE_WARNING = "token usage unread: "
UNREAD_USAGE_NOT_SEEN = {
    "adk": "LiteLLM, and ADK after it, read prompt_tokens and completion_tokens with a default of 0 and recompute the "
    "total from them",
    "hermes": "Hermes builds its own usage payload, each count it cannot find set to 0",
    "otel": "the run fails before the model call's span carries any usage (see RENAMED_USAGE_FAILS_THE_RUN)",
}
"""The examples whose framework hands the adapter a usage with no token count left in it when the provider renamed its
keys, so the adapter cannot tell it from a provider that reports no usage, and does not warn. The usage SAM records is
still none, never made up.
"""
RENAMED_USAGE_FAILS_THE_RUN = {
    "strands": "Strands' OpenAI model hands its usage over with the input and output counts missing, then fails "
    "adding them up (TypeError)",
    "otel": "it runs a Strands agent",
}
"""The examples whose framework fails the model call on such a usage instead of reading it as zero: loud, not silent."""


def unread_usage_warnings(agent) -> list[str]:
    log = agent.process.log_path.read_text(errors="replace")
    return [line for line in log.splitlines() if UNREAD_USAGE_WARNING in line]


@pytest.mark.parametrize("framework", EVERY_EXAMPLE, indirect=True)
def test_a_usage_under_renamed_keys_is_recorded_as_none_and_the_agent_warns_once(examples, framework):
    """A model response that produced output and whose usage holds token counts under keys the adapter does not read
    is recorded with no usage (nothing made up), and the agent logs one warning naming the adapter, the model and the
    usage's field names, however many such responses follow (docs/behaviour.md). A usage that counts nothing, as a
    local model's may, is no reason to warn: no count was lost."""
    agent = examples[PLAIN]
    _, uncounted = run_task(
        examples,
        PLAIN,
        Turn(calls=(("call_a", "lookup_policy", {"policy_id": "P-1182"}),), usage=(0, 0)),
        Turn(text=ANSWER, expects=("call_a",), usage=(0, 0)),
    )
    assert uncounted.status == "completed", uncounted.error_message
    assert unread_usage_warnings(agent) == []
    turns = (
        Turn(calls=(("call_a", "lookup_policy", {"policy_id": "P-1182"}),), usage_keys=RENAMED_USAGE),
        Turn(text=ANSWER, expects=("call_a",), usage=(380, 15), usage_keys=RENAMED_USAGE),
    )
    views = [run_task(examples, PLAIN, *turns)[1] for _ in range(2)]

    fails = framework.id in RENAMED_USAGE_FAILS_THE_RUN
    for view in views:
        assert view.status == ("failed" if fails else "completed"), view.error_message
        reported = [s["usage"] for s in view.signals() if s.get("type") == "llm_response"]
        assert len(reported) == (0 if framework.id == "otel" else 1 if fails else 2)
        assert all(u.get("input_tokens", 0) == u.get("output_tokens", 0) == 0 for u in reported), reported
        assert not view.record.get("totalInputTokens") and not view.record.get("totalOutputTokens")
    warnings = unread_usage_warnings(agent)
    if framework.id in UNREAD_USAGE_NOT_SEEN:
        assert warnings == []
        return
    [warning] = warnings
    adapter = "langchain" if framework.id == "langgraph" else framework.id
    assert f"sam_link_sdk.{adapter} read no input and output tokens" in warning
    names = {framework.model, agent.model.requests[0].body["model"]}
    assert any(f"model={name} " in warning for name in names), warning
    fields = warning.split("usage_fields=", 1)[1]
    assert "total" in fields and not any(ch.isdigit() for ch in fields), fields


@pytest.mark.parametrize("framework", EVERY_EXAMPLE, indirect=True)
def test_the_steps_of_a_run_reach_agent_mesh_in_the_order_they_happened(examples):
    _, view = run_task(examples, PLAIN, *triage_turns())

    assert view.status == "completed", view.error_message
    order = [
        s["type"]
        for s in view.signals()
        if s.get("type") in ("llm_invocation", "llm_response", "tool_invocation_start", "tool_result")
    ]
    assert order == ["llm_invocation", "llm_response", "tool_invocation_start", "tool_result"] * 2 + [
        "llm_invocation",
        "llm_response",
    ]


REPORTS_CACHE_WRITES = {"adk", "hermes"}
"""The frameworks that report the cache writes a model response carries beside its reads (`cache_creation_input_tokens`,
which a Chat Completions response has no field for): ADK through LiteLLM, which maps it, and Hermes. The other
frameworks' OpenAI clients read `prompt_tokens_details.cached_tokens` only, so SAM is told the reads and no writes.
"""


@pytest.mark.parametrize("framework", EVERY_EXAMPLE, indirect=True)
def test_cached_input_tokens_a_models_response_reports_reach_agent_mesh_within_the_input(examples, framework):
    """`cached_input_tokens` is the reads and the writes together, `cache_write_tokens` the writes (docs/handlers.md);
    `input_tokens` stays what the provider reported, the cached tokens not added to it."""
    _, view = run_task(
        examples,
        PLAIN,
        Turn(
            calls=(("call_a", "lookup_policy", {"policy_id": "P-1182"}),), usage=(300, 20), cached=100, cache_write=40
        ),
        Turn(text=ANSWER, expects=("call_a",), usage=(380, 15), cached=250),
    )

    assert view.status == "completed", view.error_message
    reported = [
        {key: value for key, value in s["usage"].items() if key != "model"}
        for s in view.signals()
        if s.get("type") == "llm_response"
    ]
    writes = framework.id in REPORTS_CACHE_WRITES
    assert reported == [
        {
            "input_tokens": 300,
            "output_tokens": 20,
            "cached_input_tokens": 140 if writes else 100,
            "reasoning_tokens": 0,
            **({"cache_write_tokens": 40} if writes else {}),
        },
        {"input_tokens": 380, "output_tokens": 15, "cached_input_tokens": 250, "reasoning_tokens": 0},
    ]
    assert (view.record["totalInputTokens"], view.record["totalOutputTokens"]) == (680, 35)


@pytest.mark.parametrize("framework", EVERY_EXAMPLE, indirect=True)
def test_a_tool_that_returns_an_error_is_shown_as_failed_and_the_model_is_told(examples):
    agent, view = run_task(
        examples,
        PLAIN,
        Turn(calls=(("call_a", "lookup_policy", {"policy_id": "P-404"}),)),
        Turn(text="There is no policy P-404.", expects=("call_a",)),
    )

    assert agent.model.violations == []
    assert view.status == "completed", view.error_message
    assert "no policy P-404" in agent.model.requests[1].tool_results()["call_a"]
    [result] = steps(view, "tool_result", "lookup_policy")
    assert result["result_data"] == {"error": "no policy P-404", "status": FAILED}


@pytest.mark.parametrize("framework", EVERY_EXAMPLE, indirect=True)
def test_a_tools_long_argument_and_result_are_cut_on_the_wire_and_reach_the_model_whole(examples, framework):
    """lookup_policy echoes its argument into its error, so one long argument makes both long."""
    long_id = "P-" + "7" * 40_000
    agent, view = run_task(
        examples,
        PLAIN,
        Turn(calls=(("call_a", "lookup_policy", {"policy_id": long_id}),)),
        Turn(text="There is no such policy.", expects=("call_a",)),
    )

    assert agent.model.violations == []
    assert view.status == "completed", view.error_message
    told = agent.model.requests[1].tool_results()["call_a"]
    if cut := MODEL_SEES_ERROR_CUT.get(framework.id):
        assert long_id not in told
        assert cut < len(told) < cut + 100
        assert told.endswith('[truncated]"}')
    else:
        assert long_id in told
    [start] = steps(view, "tool_invocation_start", "lookup_policy")
    [result] = steps(view, "tool_result", "lookup_policy")
    for shown in (start["tool_args"]["policy_id"], result["result_data"]["error"]):
        assert len(shown.encode("utf-8")) <= MAX_STRING_BYTES
        assert "[truncated" in shown


@pytest.mark.parametrize("framework", ADAPTERS, indirect=True)
def test_the_agent_card_lists_each_tool_with_the_scopes_it_requires(examples):
    for role, scope in ((PLAIN, TOOL_SCOPE_HELD), (BLOCKED, TOOL_SCOPE_NOT_HELD)):
        card = examples.client.agent_card(examples[role].name)
        [tools] = [e["params"]["tools"] for e in card["capabilities"]["extensions"] if e["uri"] == EXT_TOOLS]
        scopes = {t["name"]: t["requiredScopes"] for t in tools}
        assert scopes == {"lookup_policy": [], "route_claim": [scope], "fraud_screen": []}, role


FAILED_CALLS = {
    "auth_failure": (
        401,
        "Incorrect API key provided: sk-e2e-not-a-real-key",
        "",
        "LLM error: the model call failed (auth_failure)",
    ),
    "rate_limit": (
        429,
        "Slow down on the prompt P-1182 claim",
        "",
        "The AI provider is rate-limiting requests; try again shortly: the model call failed (rate_limit)",
    ),
    "timeout": (408, "Gave up on the prompt P-1182 claim", "", "LLM error: the model call failed (timeout)"),
    "context_overflow": (
        400,
        "Your prompt P-1182 claim is over the limit",
        "context_length_exceeded",
        "The conversation history has become too long for the AI model to process. This can happen "
        "after extended conversations. To continue, please start a new conversation.",
    ),
    "content_filtered": (
        400,
        "The response to P-1182 claim was filtered due to the content management policy",
        "",
        "LLM error: the model call failed (content_filtered)",
    ),
}
"""What a provider answers with (status, the words it uses, its error code), and what Agent Mesh is told. The wording is
SAM's for the category; the provider's own words, which may quote the prompt, are in none of it. The 429 and the
context-length case carry no word the message matching would rescue them with, so the status and the code decide.
"""
SLOW_TO_GIVE_UP = {("strands", "rate_limit"), ("otel", "rate_limit"), ("hermes", "timeout")}
"""These back off for minutes before they give up on a failing call (Strands, so the OpenTelemetry example, a rate limit:
about two minutes; Hermes a timeout: over four), so they are left out rather than waited for.
"""
FAILING_TURNS = 30
"""Enough failing turns for a framework that retries: CrewAI tries a rate limit five times and every other failure three,
each of them through OpenAI's client, which tries three times itself.
"""
PROVIDER_NAMED = {("adk", "*"): None, ("hermes", "*"): None, ("otel", "auth_failure"): "other"}
"""The provider the error data names, where it is not the client library's own ("openai"): ADK reaches the model through
LiteLLM, which names none, and Hermes's failure is read from its own error report. The OpenTelemetry example learns of
an authentication failure from its span, whose error type is the bare class name, so no library is named.
"""


@pytest.mark.parametrize(
    "framework, category",
    [
        pytest.param(fw, category, id=f"{category}-{fw}")
        for fw in EVERY_EXAMPLE
        for category in FAILED_CALLS
        if (fw, category) not in SLOW_TO_GIVE_UP
    ],
    indirect=["framework"],
    scope="module",
)
def test_a_failed_model_call_fails_the_task_with_its_category_and_without_the_providers_text(
    examples, framework, category
):
    """The provider's message may quote the prompt, so Agent Mesh is told the failure's class, never its text
    (docs/security.md). The category is read from the exception the framework's client raised, or from the call
    the framework recorded as failed, so each framework is held to every category."""
    status, provider_text, code, message = FAILED_CALLS[category]
    agent = examples[PLAIN]
    agent.model.script(*[Turn(status=status, message=provider_text, code=code) for _ in range(FAILING_TURNS)])
    view = examples.client.wait_done(examples.client.send(agent.name, ASK), timeout=TASK_TIMEOUT_SECONDS)

    assert view.status == "failed"
    error = view.final()["error"]
    assert error["data"]["llm_error_category"] == category
    assert error["data"].get("llm_provider") == PROVIDER_NAMED.get(
        (framework.id, category), PROVIDER_NAMED.get((framework.id, "*"), "openai")
    )
    assert error["message"] == message
    assert provider_text not in recorded(view)
    assert steps(view, "tool_invocation_start", "lookup_policy") == []


ASKING = [fw for fw in ADAPTERS if fw != "hermes"]
"""Hermes asks through its own clarify tool, whose arguments differ, so its form is left out rather than guessed."""


@pytest.mark.parametrize("framework", ASKING, indirect=True)
def test_a_question_the_model_asks_reaches_the_user_and_the_answer_reaches_the_model(examples, tmp_path):
    agent = examples[APPROVAL]
    agent.model.script(
        Turn(
            calls=(
                ("call_q", "ask_user_question", {"question": "Which coverage tier?", "options": ["Gold", "Silver"]}),
            )
        ),
        Turn(text="Gold it is.", expects=("call_q",)),
    )
    asked = examples.client.cli_send(agent.name, ASK, tmp_path / "ask", PROMPT_TIMEOUT_SECONDS)
    prompts = sorted((tmp_path / "ask").glob("hil_request_*.json"))
    assert len(prompts) == 1, f"`sam task send` saw no question:\n{asked.stdout[-2000:]}\n{asked.stderr[-2000:]}"
    prompt = json.loads(prompts[0].read_text())
    assert prompt["source"] == "ask_user_question"
    assert "Which coverage tier?" in json.dumps(prompt)
    assert len(agent.model.requests) == 1

    answered = examples.client.cli_answer_questions(prompts[0], ["Gold"], tmp_path / "answer", PROMPT_TIMEOUT_SECONDS)
    assert answered.returncode == 0, f"{answered.stdout[-2000:]}\n{answered.stderr[-2000:]}"
    view = examples.client.wait_done(prompt["task_id"], timeout=TASK_TIMEOUT_SECONDS)

    assert agent.model.violations == []
    assert view.status == "completed", view.error_message
    assert view.answer().strip() == "Gold it is."
    assert "Gold" in agent.model.requests[1].tool_results()["call_q"]


@pytest.mark.parametrize("framework", ADAPTERS, indirect=True)
def test_a_run_cancelled_while_a_tool_runs_ends_canceled_and_the_tools_result_never_reaches_the_model(examples):
    """The cancel lands inside the fraud screen. A run that honours it never hands the screen's result to the model
    (docs/handlers.md); one that ignores it calls the model again once the screen ends."""
    agent = examples[STOP]
    agent.model.script(
        Turn(calls=(("call_f", "fraud_screen", {"policy_id": "P-1182"}),)),
        Turn(text="P-1182 screened as low risk.", expects=("call_f",)),
    )
    task_id = examples.client.send(agent.name, "Run a fraud check on P-1182")
    wait_until(
        lambda: "tool_invocation_start" in json.dumps(examples.client.events(task_id)),
        TASK_TIMEOUT_SECONDS,
        "the fraud screen to start",
    )

    cancel = examples.client.cancel(task_id)
    assert cancel.status in (200, 202, 204), cancel
    view = examples.client.wait_done(task_id, timeout=TASK_TIMEOUT_SECONDS)

    assert view.status in ("canceled", "cancelled"), view.error_message
    assert agent.model.no_request_beyond(1, within=FRAUD_SCREEN_SECONDS + 4), (
        f"the model was called again after the cancel: {len(agent.model.requests)} requests"
    )
    assert steps(view, "tool_result", "fraud_screen") == []


@pytest.mark.parametrize("framework", [fw for fw in FRAMEWORKS if STOP in FRAMEWORKS[fw].roles], indirect=True)
def test_a_task_that_reaches_max_llm_calls_fails_saying_so_and_the_call_over_the_limit_never_reaches_the_model(
    examples,
):
    """docs/handlers.md: the call over the limit is stopped as it starts, and the task fails saying which limit."""
    agent = examples[STOP]
    lookups = [
        Turn(calls=((f"call_{n}", "lookup_policy", {"policy_id": "P-1182"}),), expects=(f"call_{n - 1}",) if n else ())
        for n in range(STOP_MAX_LLM_CALLS + 2)
    ]
    agent.model.script(*lookups)
    view = examples.client.wait_done(examples.client.send(agent.name, ASK), timeout=TASK_TIMEOUT_SECONDS)

    assert view.status == "failed"
    assert view.error_message == f"Stopped: the task reached the limit of {STOP_MAX_LLM_CALLS} model calls."
    assert agent.model.no_request_beyond(STOP_MAX_LLM_CALLS, within=3), (
        f"{len(agent.model.requests)} model calls reached the model, the limit is {STOP_MAX_LLM_CALLS}"
    )
    assert len(agent.model.requests) == STOP_MAX_LLM_CALLS


COMPLETES_AT_ITS_LIMIT = ("agent_framework", "crewai")
WITH_A_LOOP_LIMIT = [fw for fw in FRAMEWORKS if LIMITED in FRAMEWORKS[fw].roles and fw not in COMPLETES_AT_ITS_LIMIT]
HERMES_CALLS_AT_ITS_LIMIT = FRAMEWORK_MAX_TURNS + 1
"""Hermes asks the model once more at its limit, for a summary it does not use (the script gives it one): the task fails
all the same.
"""
FRAMEWORK_LIMIT = {
    "langchain": (
        f"Stopped: the agent reached its limit of {FRAMEWORK_MAX_TURNS} model calls (max_model_calls).",
        FRAMEWORK_MAX_TURNS,
    ),
    "langgraph": ("Stopped: the agent reached the LangGraph limit on steps (recursion_limit).", 2),
    "openai_agents": (
        "Stopped: the agent reached the OpenAI Agents limit on model calls (max_model_calls).",
        FRAMEWORK_MAX_TURNS,
    ),
    "adk": ("Stopped: the agent reached the ADK limit on model calls (max_model_calls).", FRAMEWORK_MAX_TURNS),
    "hermes": (
        f"Stopped: the agent reached its limit of {FRAMEWORK_MAX_TURNS} model calls (max_model_calls).",
        HERMES_CALLS_AT_ITS_LIMIT,
    ),
}
"""What each framework says at its own limit, and how many model calls it lets through first. LangGraph counts graph steps
(a model step and a tool step per round), so its limit of 3 stops after the second model call.
"""


@pytest.mark.parametrize("framework", WITH_A_LOOP_LIMIT, indirect=True)
def test_a_run_that_reaches_its_frameworks_own_limit_fails_saying_which_limit(examples, framework):
    """Agent Framework and CrewAI make one more model call for a final answer at their limit instead, so they end
    completed (next test)."""
    turns = [
        Turn(calls=((f"call_{i}", "lookup_policy", {"policy_id": "P-1182"}),), expects=(f"call_{i - 1}",) if i else ())
        for i in range(FRAMEWORK_MAX_TURNS + 3)
    ]
    if framework.id == "hermes":
        turns[FRAMEWORK_MAX_TURNS:] = [Turn(text="Work so far.", expects=(f"call_{FRAMEWORK_MAX_TURNS - 1}",))]
    agent, view = run_task(examples, LIMITED, *turns)

    message, calls = FRAMEWORK_LIMIT[framework.id]
    assert agent.model.violations == []
    assert (view.status, view.error_message) == ("failed", message)
    assert len(agent.model.requests) == calls


@pytest.mark.parametrize("framework", ["strands"], indirect=True)
def test_an_answer_cut_off_at_the_models_output_limit_fails_saying_so(examples):
    """Strands raises when the model stops for want of output tokens, which the agent would otherwise answer as an
    internal error. The other frameworks hand the cut-off text on as the answer."""
    agent, view = run_task(examples, PLAIN, Turn(text="Claim P-1182 goes to", finish="length"))

    assert (view.status, view.error_message) == (
        "failed",
        "Stopped: the agent reached the model's limit on output tokens (max_tokens).",
    )
    assert len(agent.model.requests) == 1


@pytest.mark.parametrize("framework", ["crewai"], indirect=True)
def test_a_crew_whose_task_guardrail_keeps_rejecting_its_answer_fails_saying_a_guardrail_blocked_it(examples):
    """CrewAI raises a bare Exception when a task's guardrail still fails after its retries (the example's allows one),
    which the agent would otherwise answer as an internal error. The guardrail's own words, which a user wrote, and the
    rejected answer stay out of the message."""
    answer = f"Claim P-1182 is {BLOCKED_OUTPUT_WORD}."
    agent, view = run_task(examples, LIMITED, Turn(text=answer), Turn(text=answer))

    assert (view.status, view.error_message) == ("failed", "Stopped: a guardrail of the crew blocked its answer.")
    assert len(agent.model.requests) == 2
    assert BLOCKED_OUTPUT_WORD not in view.error_message
    assert view.streamed_text() == []


FINAL_CALL = {
    "agent_framework": (["fraud_screen", "lookup_policy", "route_claim"], "none"),
    "crewai": ([], None),
}
"""What the one more model call at the limit is offered. Agent Framework keeps the tools in the request and forbids them
(`tool_choice: none`); CrewAI takes them out of it.
"""


@pytest.mark.parametrize("framework", COMPLETES_AT_ITS_LIMIT, indirect=True)
def test_a_run_that_reaches_its_frameworks_own_limit_makes_one_more_model_call_and_completes_with_its_answer(
    examples, framework
):
    """docs/handlers.md: Agent Framework (`max_iterations`) and CrewAI (`max_iter`) do not fail at the limit. They ask the
    model once more, without tools, and its answer is the task's."""
    turns = [
        Turn(calls=((f"call_{i}", "lookup_policy", {"policy_id": "P-1182"}),), expects=(f"call_{i - 1}",) if i else ())
        for i in range(FRAMEWORK_MAX_TURNS)
    ]
    agent, view = run_task(examples, LIMITED, *turns, Turn(text=ANSWER, expects=(f"call_{FRAMEWORK_MAX_TURNS - 1}",)))

    assert agent.model.violations == []
    assert (view.status, view.error_message) == ("completed", None)
    assert view.answer().strip() == ANSWER
    assert len(agent.model.requests) == FRAMEWORK_MAX_TURNS + 1
    assert len(steps(view, "tool_invocation_start", "lookup_policy")) == FRAMEWORK_MAX_TURNS
    final = agent.model.requests[-1]
    assert (final.offered_tools(), final.body.get("tool_choice")) == FINAL_CALL[framework.id]
    assert agent.model.no_request_beyond(FRAMEWORK_MAX_TURNS + 1, within=2)


@pytest.mark.parametrize("framework", ["openai_agents"], indirect=True)
def test_a_request_an_input_guardrail_blocks_fails_naming_the_guardrail_and_never_reaches_the_model(examples):
    agent, view = run_task(examples, LIMITED, Turn(text="unused"), text=f"Triage the {BLOCKED_WORD} claim P-1182")

    assert (view.status, view.error_message) == ("failed", "Stopped: the guardrail blocked_word blocked this request.")
    assert agent.model.requests == []


@pytest.mark.parametrize("framework", ["openai_agents"], indirect=True)
def test_an_answer_an_output_guardrail_blocks_fails_naming_the_guardrail_and_never_reaches_the_user(examples):
    """An output guardrail runs after the model has answered, and Agent Mesh cannot take back text it was streamed, so
    the agent holds the text of a run an output guardrail checks until the guardrail passes
    (docs/frameworks/openai-agents.md): the user sees the failure, never the blocked answer."""
    answer = f"Claim P-1182 is {BLOCKED_OUTPUT_WORD}."
    agent, view = run_task(examples, LIMITED, Turn(text=answer))

    assert (view.status, view.error_message) == (
        "failed",
        "Stopped: the guardrail blocked_output_word blocked the agent's answer.",
    )
    assert len(agent.model.requests) == 1
    assert view.answer() == ""
    assert view.streamed_text() == []


TRIAGE_CALL = ("call_t", "triage_claim", {"input": ASK})
"""The nested agent's intake agent hands the claim to the triage agent, run as its tool (Agent.as_tool)."""
TRIAGE_ANSWER = "P-1182 is gold and active: fast-track."
REFUSED_ANSWER = "I may not route the claim."


@pytest.mark.parametrize("framework", ["openai_agents"], indirect=True)
def test_an_agent_run_as_a_tool_runs_on_the_task_its_steps_reach_agent_mesh_and_its_answer_is_the_tool_result(
    examples,
):
    """docs/frameworks/openai-agents.md: the triage agent behind the intake agent's triage_claim runs on the same task,
    so its model call and its tool call are steps of the task, between the start and the result of triage_claim."""
    agent, view = run_task(
        examples,
        NESTED,
        Turn(calls=(TRIAGE_CALL,)),
        Turn(calls=(("call_a", "lookup_policy", {"policy_id": "P-1182"}),)),
        Turn(text=TRIAGE_ANSWER, expects=("call_a",)),
        Turn(text=ANSWER, expects=("call_t",)),
    )

    assert agent.model.violations == []
    assert view.status == "completed", view.error_message
    assert view.answer().strip() == ANSWER
    assert TRIAGE_ANSWER in agent.model.requests[3].tool_results()["call_t"]
    kinds = [
        (s.get("type"), s.get("tool_name"))
        for s in view.signals()
        if s.get("type") in ("llm_response", "tool_invocation_start", "tool_result")
    ]
    assert kinds == [
        ("llm_response", None),
        ("tool_invocation_start", "triage_claim"),
        ("llm_response", None),
        ("tool_invocation_start", "lookup_policy"),
        ("tool_result", "lookup_policy"),
        ("llm_response", None),
        ("tool_result", "triage_claim"),
        ("llm_response", None),
    ]
    [result] = steps(view, "tool_result", "triage_claim")
    assert TRIAGE_ANSWER in json.dumps(result.get("result_data"))


@pytest.mark.parametrize("framework", ["openai_agents"], indirect=True)
def test_a_tool_outside_the_users_scopes_on_an_agent_run_as_a_tool_never_runs_and_the_refusal_is_visible(examples):
    """The triage agent's route_claim requires a scope the user lacks: it is not offered to that agent's model, and its
    call anyway is refused as the intake agent's own would be."""
    agent, view = run_task(
        examples,
        NESTED,
        Turn(calls=(TRIAGE_CALL,)),
        Turn(calls=(("call_b", "route_claim", {"policy_id": "P-1182", "queue": "fast-track"}),)),
        Turn(text=REFUSED_ANSWER, expects=("call_b",)),
        Turn(text=ANSWER, expects=("call_t",)),
    )

    assert agent.model.violations == []
    assert view.status == "completed", view.error_message
    assert view.answer().strip() == ANSWER
    assert REFUSED_ANSWER in agent.model.requests[3].tool_results()["call_t"]
    assert "route_claim" not in agent.model.requests[1].offered_tools()
    assert ACCESS_DENIED in agent.model.requests[2].tool_results()["call_b"]
    [result] = steps(view, "tool_result", "route_claim")
    assert ACCESS_DENIED in json.dumps(result.get("result_data"))
    assert TICKET not in recorded(view)


@pytest.mark.parametrize("framework", ["openai_agents"], indirect=True)
def test_the_model_calls_of_an_agent_run_as_a_tool_count_toward_max_llm_calls(examples):
    """The intake agent makes one model call; the triage agent's calls push the task over its limit, and the call
    over it never reaches the model."""
    lookups = [
        Turn(calls=((f"call_{n}", "lookup_policy", {"policy_id": "P-1182"}),), expects=(f"call_{n - 1}",) if n else ())
        for n in range(NESTED_MAX_LLM_CALLS + 1)
    ]
    agent, view = run_task(examples, NESTED, Turn(calls=(TRIAGE_CALL,)), *lookups)

    assert view.status == "failed"
    assert view.error_message == f"Stopped: the task reached the limit of {NESTED_MAX_LLM_CALLS} model calls."
    assert agent.model.no_request_beyond(NESTED_MAX_LLM_CALLS, within=3), (
        f"{len(agent.model.requests)} model calls reached the model, the limit is {NESTED_MAX_LLM_CALLS}"
    )
    assert len(agent.model.requests) == NESTED_MAX_LLM_CALLS


@pytest.mark.parametrize("framework", EVERY_EXAMPLE, indirect=True)
def test_each_model_response_agent_mesh_records_is_what_the_model_answered(examples):
    """The response is shown to the user as the model gave it: its tool calls with their ids and arguments, or its
    text. Usage reaches Agent Mesh by its own path, so a response that went missing would still be priced."""
    _, view = run_task(examples, PLAIN, *triage_turns())

    assert view.status == "completed", view.error_message
    responses = [s["data"]["content"]["parts"] for s in view.signals() if s.get("type") == "llm_response"]
    assert responses == [
        [{"function_call": {"id": "call_a", "name": "lookup_policy", "args": {"policy_id": "P-1182"}}}],
        [
            {
                "function_call": {
                    "id": "call_b",
                    "name": "route_claim",
                    "args": {"policy_id": "P-1182", "queue": "fast-track"},
                }
            }
        ],
        [{"text": ANSWER}],
    ]


FOLLOW_UP = "And what is its deductible?"
WITH_REQUEST_CONTEXT = ["langchain", "openai_agents", "adk", "crewai", "strands", "agent_framework"]


@pytest.mark.parametrize("framework", WITH_REQUEST_CONTEXT, indirect=True)
def test_what_the_gateway_says_about_the_request_reaches_the_model_only_when_the_agent_asks_for_it(examples):
    """The gateway is configured with a system purpose, which SAM sends with every task as request context
    (docs/handlers.md): `include_request_context=True` puts it in the prompt, and an agent that does not ask for it
    leaves it out."""
    asking, answered = run_task(examples, APPROVAL, Turn(text=ANSWER))
    plain, _ = run_task(examples, PLAIN, Turn(text=ANSWER))

    assert answered.status == "completed", answered.error_message
    assert SYSTEM_PURPOSE in asking.model.requests[0].text()
    assert SYSTEM_PURPOSE not in plain.model.all_text()


@pytest.mark.parametrize("framework", EVERY_EXAMPLE, indirect=True)
def test_a_follow_up_in_the_same_chat_sees_the_earlier_turn_and_a_new_chat_does_not(examples):
    agent, first = run_task(examples, PLAIN, *triage_turns())
    agent.model.script(Turn(text="The deductible is 500."))
    follow_up = examples.client.wait_done(
        examples.client.send(agent.name, FOLLOW_UP, first.record["sessionId"]), timeout=TASK_TIMEOUT_SECONDS
    )
    in_the_same_chat = agent.model.requests[-1].text()
    agent.model.script(Turn(text="Which claim?"))
    fresh = examples.client.wait_done(examples.client.send(agent.name, FOLLOW_UP), timeout=TASK_TIMEOUT_SECONDS)
    in_a_new_chat = agent.model.requests[-1].text()

    assert (follow_up.status, fresh.status) == ("completed", "completed")
    assert ASK in in_the_same_chat and ANSWER in in_the_same_chat and FOLLOW_UP in in_the_same_chat
    assert FOLLOW_UP in in_a_new_chat and ASK not in in_a_new_chat and ANSWER not in in_a_new_chat


LAST_POLICY = "The policy this user last looked up, if any: P-2040"
"""What the ADK example puts in its instruction from the user's ADK `user:` state once the user has looked P-2040 up."""


@pytest.mark.parametrize("framework", ["adk"], indirect=True)
def test_adk_user_state_is_kept_per_sam_user(examples, second_user):
    """docs/frameworks/adk.md: each SAM user is an ADK user, so what ADK keeps for a user (`user:` state, its memory
    service) reaches that user's later chats and never another user's. The example keeps the policy a user last looked
    up in `user:last_policy` and puts it in the instruction."""
    agent, looked_up = run_task(
        examples,
        PLAIN,
        Turn(calls=(("call_a", "lookup_policy", {"policy_id": "P-2040"}),)),
        Turn(text="P-2040 is silver.", expects=("call_a",)),
    )
    agent.model.script(Turn(text="Which claim?"))
    other = second_user.wait_done(second_user.send(agent.name, FOLLOW_UP), timeout=TASK_TIMEOUT_SECONDS)
    in_another_users_chat = agent.model.requests[-1].text()
    agent.model.script(Turn(text="Which claim?"))
    own = examples.client.wait_done(examples.client.send(agent.name, FOLLOW_UP), timeout=TASK_TIMEOUT_SECONDS)
    in_the_users_next_chat = agent.model.requests[-1].text()

    assert (looked_up.status, other.status, own.status) == ("completed", "completed", "completed")
    assert LAST_POLICY in in_the_users_next_chat
    assert LAST_POLICY not in in_another_users_chat


@pytest.mark.parametrize("framework", ADAPTERS, indirect=True)
def test_a_tool_outside_the_users_scopes_is_not_offered_to_the_model(examples):
    agent, view = run_task(examples, BLOCKED, Turn(text="I cannot route claims for you."))

    assert view.status == "completed", view.error_message
    offered = agent.model.requests[0].offered_tools()
    assert "route_claim" not in offered and "lookup_policy" in offered, offered


RAISE_ENDS_THE_RUN = {"langgraph", "adk"}
"""What a framework does after a tool raises. Most hand the error to the model and go on. LangGraph's ToolNode (without
handle_tool_errors) and ADK (without an on_tool_error callback that recovers) end the run, so the task fails; Agent
Framework tells the model only that the function failed unless include_detailed_errors is set. Agent Mesh shows the
failed call with the tool's own error in every case (OpenAI Agents and Strands word it as they tell the model).
"""
TOLD_WITHOUT_DETAIL = {"agent_framework": "Error: Function failed."}


@pytest.mark.parametrize("framework", EVERY_EXAMPLE, indirect=True)
def test_a_tool_that_raises_is_shown_as_failed_with_its_error(examples, framework):
    agent, view = run_task(
        examples,
        PLAIN,
        Turn(calls=(("call_a", "lookup_policy", {"policy_id": "X-1"}),)),
        Turn(text="That policy id is malformed.", expects=("call_a",)),
    )

    [result] = steps(view, "tool_result", "lookup_policy")
    assert result["result_data"]["status"] == FAILED
    assert "malformed policy id X-1" in result["result_data"]["error"]
    if framework.id in RAISE_ENDS_THE_RUN:
        assert (view.status, view.error_message) == ("failed", "internal error")
        assert len(agent.model.requests) == 1
        return
    assert agent.model.violations == []
    assert view.status == "completed", view.error_message
    told = agent.model.requests[1].tool_results()["call_a"]
    if framework.id in TOLD_WITHOUT_DETAIL:
        assert told == TOLD_WITHOUT_DETAIL[framework.id]
    else:
        assert "malformed policy id X-1" in told


@pytest.mark.parametrize("framework", EVERY_EXAMPLE, indirect=True)
def test_a_tool_that_raises_a_model_clients_error_is_not_reported_as_a_model_failure(examples, framework):
    """The tool calls another service through the OpenAI client, which times out. Where the framework ends the run on a
    raising tool the task fails, and Agent Mesh must not be told the model call timed out: the model was never
    called after the tool."""
    _agent, view = run_task(
        examples,
        PLAIN,
        Turn(calls=(("call_a", "lookup_policy", {"policy_id": "P-BACKEND"}),)),
        Turn(text="The policy backend is down.", expects=("call_a",)),
    )

    [result] = steps(view, "tool_result", "lookup_policy")
    assert result["result_data"]["status"] == FAILED
    if framework.id in RAISE_ENDS_THE_RUN:
        assert (view.status, view.error_message) == ("failed", "internal error")
        assert "llm_error_category" not in (view.final()["error"].get("data") or {})
        assert "llm_provider" not in (view.final()["error"].get("data") or {})
        return
    assert view.status == "completed", view.error_message


@pytest.mark.parametrize(
    "framework",
    [fw for fw in EVERY_EXAMPLE if (fw, "auth_failure") not in SLOW_TO_GIVE_UP],
    indirect=True,
    scope="module",
)
def test_a_model_call_that_fails_after_a_tool_ran_still_fails_the_task_with_its_category(examples):
    status, provider_text, code, message = FAILED_CALLS["auth_failure"]
    agent = examples[PLAIN]
    agent.model.script(
        Turn(calls=(("call_a", "lookup_policy", {"policy_id": "P-1182"}),)),
        *[Turn(status=status, message=provider_text, code=code) for _ in range(FAILING_TURNS)],
    )
    view = examples.client.wait_done(examples.client.send(agent.name, ASK), timeout=TASK_TIMEOUT_SECONDS)

    assert view.status == "failed"
    assert view.final()["error"]["data"]["llm_error_category"] == "auth_failure"
    assert view.final()["error"]["message"] == message
    assert len(steps(view, "tool_result", "lookup_policy")) == 1


REASONING = "The policy is gold and active, so it goes to fast-track."
STREAMS_REASONING = {"openai_agents", "adk", "strands", "hermes"}
"""What reaches the chat when stream_thinking is on. These frameworks hand the adapter the reasoning a Chat Completions
response carries (`reasoning_content`). The others do not, each for its own reason (docs/contributing/adapters.md).
"""
NO_REASONING_FROM_THE_CLIENT = {
    "langchain": "ChatOpenAI drops reasoning_content, so the message the adapter reads has none",
    "langgraph": "the same ChatOpenAI",
    "agent_framework": "OpenAIChatCompletionClient drops reasoning_content; only its Responses client has reasoning",
    "crewai": "CrewAI's events carry no reasoning",
}


def thinking(view) -> list[dict]:
    return [s for s in view.signals() if s.get("type") == "thinking_content"]


@pytest.mark.parametrize("framework", ADAPTERS, indirect=True)
def test_the_models_reasoning_is_not_sent_to_the_chat_unless_stream_thinking_is_on(examples):
    """It is model-generated content that SAM would show and keep, so it is off by default (docs/handlers.md)."""
    agent, view = run_task(examples, PLAIN, Turn(text=ANSWER, reasoning=REASONING))

    assert agent.model.violations == []
    assert view.status == "completed", view.error_message
    assert view.answer().strip() == ANSWER
    assert thinking(view) == []
    assert REASONING not in recorded(view)


@pytest.mark.parametrize("framework", ADAPTERS, indirect=True)
def test_with_stream_thinking_the_models_reasoning_reaches_the_chat_apart_from_the_answer(examples, framework):
    agent, view = run_task(examples, APPROVAL, Turn(text=ANSWER, reasoning=REASONING))

    assert agent.model.violations == []
    assert view.status == "completed", view.error_message
    assert view.answer().strip() == ANSWER
    events = thinking(view)
    if framework.id in NO_REASONING_FROM_THE_CLIENT:
        assert events == []
        assert REASONING not in recorded(view)
        return
    assert framework.id in STREAMS_REASONING
    assert "".join(e["content"] for e in events) == REASONING
    assert [e["is_complete"] for e in events][-1] is True
    assert REASONING not in view.answer()
    assert REASONING not in "".join(view.streamed_text())


STREAMS = ADAPTERS


@pytest.mark.parametrize("framework", STREAMS, indirect=True)
def test_the_answer_reaches_agent_mesh_in_pieces_as_the_model_writes_it(examples):
    agent, view = run_task(examples, PLAIN, *triage_turns())

    assert agent.model.violations == []
    assert view.status == "completed", view.error_message
    assert [bool(r.body.get("stream")) for r in agent.model.requests] == [True, True, True]
    pieces = view.streamed_text()
    assert len(pieces) > 1
    assert "".join(pieces).strip() == ANSWER
    assert view.answer().strip() == ANSWER


@pytest.mark.parametrize("framework", STREAMS, indirect=True)
def test_with_stream_text_off_the_answer_reaches_agent_mesh_whole(examples):
    """Strands streams from its model either way (invoke_async drains the stream), so the model's request says nothing."""
    agent, view = run_task(examples, WHOLE, *triage_turns())

    assert agent.model.violations == []
    assert view.status == "completed", view.error_message
    assert [piece.strip() for piece in view.streamed_text()] == [ANSWER]
    assert view.answer().strip() == ANSWER


@pytest.mark.parametrize("framework", ["hermes"], indirect=True)
def test_hermes_streams_the_answer_and_not_the_text_a_response_writes_after_its_tool_call(examples):
    """Hermes hands its stream callback the text a response writes once it has begun a tool call, and puts a paragraph
    break before the first text after a tool round; neither is the answer (docs/frameworks/hermes.md)."""
    after_the_call = "Checking the policy tier now."
    turns = triage_turns()
    agent, view = run_task(examples, PLAIN, dataclasses.replace(turns[0], text_after_calls=after_the_call), *turns[1:])

    assert agent.model.violations == []
    assert view.status == "completed", view.error_message
    pieces = view.streamed_text()
    assert len(pieces) > 1
    assert "".join(pieces) == ANSWER
    assert after_the_call not in "".join(pieces)
    assert view.answer().strip() == ANSWER


NUMBERED_CALL_ID = "call_0"
"""A provider that numbers its tool calls gives the first call of every conversation this id."""
OTHER_ASK = "What tier is policy P-2040?"
OTHER_ANSWER = "P-2040 is silver."


def call_ids(view, kind: str) -> list[str]:
    """The call ids of a task's model responses (`llm_response`) or of its tool steps."""
    if kind == "llm_response":
        return [
            part["function_call"]["id"]
            for s in view.signals()
            if s.get("type") == kind
            for part in s["data"]["content"]["parts"]
            if "function_call" in part
        ]
    return [s["function_call_id"] for s in view.signals() if s.get("type") == kind]


@pytest.mark.parametrize("framework", ["hermes"], indirect=True)
def test_hermes_runs_a_tasks_tool_while_another_task_waits_on_an_approval_for_a_call_with_the_same_id(
    examples, tmp_path
):
    """Hermes runs one `pre_tool_call` hook at a time per tool call id across its process, and the approval waits in
    that hook, so a second task whose model numbers its call the same way must not find it taken
    (docs/frameworks/hermes.md). The harness has one user, so the two tasks are two conversations of that user."""
    agent = examples[APPROVAL]
    agent.model.script(
        Turn(calls=((NUMBERED_CALL_ID, "route_claim", {"policy_id": "P-1182", "queue": "fast-track"}),)),
        Turn(calls=((NUMBERED_CALL_ID, "lookup_policy", {"policy_id": "P-2040"}),)),
        Turn(text=OTHER_ANSWER),
        Turn(text=ANSWER, expects=(NUMBERED_CALL_ID,)),
    )
    asked = examples.client.cli_send(agent.name, ASK, tmp_path / "ask", PROMPT_TIMEOUT_SECONDS)
    [prompt_file] = sorted((tmp_path / "ask").glob("hil_request_*.json")) or [None]
    assert prompt_file is not None, f"`sam task send` saw no approval prompt:\n{asked.stdout[-2000:]}"
    waiting = json.loads(prompt_file.read_text())["task_id"]

    other = examples.client.wait_done(examples.client.send(agent.name, OTHER_ASK), timeout=TASK_TIMEOUT_SECONDS)

    assert other.status == "completed", other.error_message
    assert other.answer().strip() == OTHER_ANSWER
    told = json.dumps(agent.model.requests[2].tool_results())
    assert "silver" in told, told
    [looked_up] = steps(other, "tool_result", "lookup_policy")
    assert "silver" in json.dumps(looked_up["result_data"])
    assert steps(other, "tool_invocation_start", "route_claim") == []
    assert call_ids(other, "tool_invocation_start") == call_ids(other, "tool_result") == call_ids(other, "llm_response")

    answered = examples.client.cli_answer_approval(prompt_file, True, tmp_path / "answer", PROMPT_TIMEOUT_SECONDS)
    assert answered.returncode == 0, f"{answered.stdout[-2000:]}\n{answered.stderr[-2000:]}"
    first = examples.client.wait_done(waiting, timeout=TASK_TIMEOUT_SECONDS)

    assert agent.model.violations == []
    assert first.status == "completed", first.error_message
    assert first.answer().strip() == ANSWER
    [routed] = steps(first, "tool_result", "route_claim")
    assert TICKET in json.dumps(routed["result_data"])
    assert TICKET in agent.model.requests[3].tool_results()[NUMBERED_CALL_ID]
    assert steps(first, "tool_invocation_start", "lookup_policy") == []
    assert call_ids(first, "tool_invocation_start") == call_ids(first, "tool_result") == [NUMBERED_CALL_ID]
    assert call_ids(first, "llm_response") == [NUMBERED_CALL_ID]


TRIAGE_RESULT = "P-1182 is routed to fast-track as ticket CLM-1."


@pytest.mark.parametrize(
    "reply",
    [ANSWER, f"Thought: I now can give a great answer\nFinal Answer: {ANSWER}\n"],
    ids=["as_written", "react_form"],
)
@pytest.mark.parametrize("framework", ["crewai"], indirect=True)
def test_a_crew_streams_only_its_last_agents_answer(examples, reply):
    """The first agent's model calls are not streamed and its result does not reach the chat; the second agent has no
    tools, so CrewAI parses its text, and only the answer in it streams (docs/frameworks/crewai.md)."""
    agent, view = run_task(
        examples, REPLY, *triage_turns()[:2], Turn(text=TRIAGE_RESULT, expects=("call_b",)), Turn(text=reply)
    )

    assert agent.model.violations == []
    assert view.status == "completed", view.error_message
    assert [bool(r.body.get("stream")) for r in agent.model.requests] == [False, False, False, True]
    pieces = view.streamed_text()
    assert len(pieces) > 1
    assert "".join(pieces) == ANSWER
    assert view.answer() == ANSWER


@pytest.mark.parametrize("framework", ["langgraph"], indirect=True)
def test_a_tool_that_raises_is_told_to_the_model_when_its_langgraph_tool_handles_the_error(examples):
    """LangGraph ends the run on a raising tool only without handle_tool_errors (RAISE_ENDS_THE_RUN). A tool built
    with handle_tool_error that raises ToolException returns an error ToolMessage instead, which the step reporter
    shows as failed."""
    agent, view = run_task(
        examples,
        HANDLED,
        Turn(calls=(("call_a", "lookup_policy", {"policy_id": "X-1"}),)),
        Turn(text="That policy id is malformed.", expects=("call_a",)),
    )

    [result] = steps(view, "tool_result", "lookup_policy")
    assert result["result_data"]["status"] == FAILED
    assert "malformed policy id X-1" in result["result_data"]["error"]
    assert agent.model.violations == []
    assert "malformed policy id X-1" in agent.model.requests[1].tool_results()["call_a"]
    assert view.status == "completed", view.error_message
    assert view.answer() == "That policy id is malformed."
