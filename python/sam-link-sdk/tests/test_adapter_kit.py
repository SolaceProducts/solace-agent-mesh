# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The adapters' shared kit (sam_link_sdk._adapter) builds each step the same way for every adapter: a model request's
arguments redacted; a tool's arguments and result redacted and every string in them capped (SAM-Go's
truncateStatusText); a result of only ``{"error": ...}`` a failed call; usage in SAM's buckets."""

from __future__ import annotations

from collections.abc import Mapping
from types import SimpleNamespace
from typing import Any

import pytest

from sam_link_sdk import _adapter
from sam_link_sdk.captext import MAX_RESULT_STRING_BYTES, cap_text
from sam_link_sdk.context import tool_name
from sam_link_sdk.events import ModelFinished, ModelStarted, ToolCall, ToolFinished, ToolStarted
from sam_link_sdk.redact import redact_value
from sam_link_sdk.usage import Usage

from .adapter_task import adapter_task

LONG = "x" * (MAX_RESULT_STRING_BYTES + 100)
SECRET_ARGS = {"policy_id": "P-1182", "api_key": "sk-live-abc", "nested": {"password": "pw", "note": LONG}, "n": 3}

CALLS = [
    [],
    [{"id": "call_a", "name": "lookup_policy", "args": {"policy_id": "P-1"}}],
    [{"id": "call_a", "name": "lookup_policy", "args": SECRET_ARGS}, {"id": "call_b", "name": "route", "args": {}}],
    [{"id": "call_c", "name": "no_args", "args": None}],
]
USAGES = [
    Usage(),
    Usage(
        input_tokens=100,
        output_tokens=10,
        cached_input_tokens=40,
        cache_write_tokens=5,
        reasoning_tokens=7,
        total_tokens=110,
    ),
]


def capped(value: Any) -> Any:
    """Every string inside `value` capped, lists and tuples as lists, as SAM shows a tool's arguments and results."""
    if isinstance(value, str):
        return cap_text(value)
    if isinstance(value, Mapping):
        return {k: capped(v) for k, v in value.items()}
    if isinstance(value, (list, tuple)):
        return [capped(v) for v in value]
    return value


def shown(value: Any) -> Any:
    return capped(redact_value(value))


@pytest.mark.parametrize("model", ["fake-model", ""])
def test_a_model_call(model):
    assert _adapter.model_started(model) == ModelStarted(model)


@pytest.mark.parametrize("text", ["", "P-1 is gold.", LONG])
@pytest.mark.parametrize("calls", CALLS)
@pytest.mark.parametrize("usage", USAGES)
def test_a_model_response(text, calls, usage):
    want = ModelFinished(
        model="m",
        text=text,
        tool_calls=tuple(ToolCall(c["id"], c["name"], redact_value(c.get("args") or {})) for c in calls),
        usage=usage,
    )
    assert _adapter.model_finished("m", text, calls, usage) == want


@pytest.mark.parametrize("args", [None, {}, {"policy_id": "P-1"}, SECRET_ARGS, {"items": ["a", LONG, ("t", 1)]}])
def test_a_tool_start(args):
    assert _adapter.tool_started("lookup_policy", args, "call_a") == ToolStarted(
        call_id="call_a", name="lookup_policy", args=shown(dict(args)) if args is not None else None
    )


@pytest.mark.parametrize(
    "result",
    [
        None,
        "plain text",
        LONG,
        3,
        True,
        {"tier": "gold"},
        ["a", {"token": "t0k"}],
        SECRET_ARGS,
        {"error": "backend down"},
        {"error": "x", "detail": "not a failure: more than an error"},
        {"error": LONG},
    ],
)
def test_a_tool_result(result):
    built = _adapter.tool_finished("lookup_policy", result, "call_a")
    data = shown(result)
    assert built == ToolFinished(
        call_id="call_a", name="lookup_policy", result=data, failed=isinstance(data, Mapping) and set(data) == {"error"}
    )
    assert built.failed == (isinstance(result, dict) and set(result) == {"error"})


def test_secrets_are_redacted_and_long_strings_capped_without_touching_the_framework_s_objects():
    args = {"api_key": "sk-live-abc", "note": LONG}
    started = _adapter.tool_started("t", args, "c")
    assert started.args["api_key"] != "sk-live-abc" and len(started.args["note"].encode()) <= MAX_RESULT_STRING_BYTES
    assert args == {"api_key": "sk-live-abc", "note": LONG}


def test_pending_calls_pair_a_start_with_its_call():
    pending = _adapter.PendingCalls()
    pending.expect([{"id": "c1", "name": "lookup"}, {"id": "c2", "name": "lookup"}])
    assert pending.take("lookup", "c2") == "c2"
    assert pending.take("lookup") == "c1"
    assert pending.take("lookup").startswith("call_")
    assert pending.take("other", "given") == "given"


@pytest.mark.parametrize(
    "raw,expected",
    [
        ({"a": 1}, {"a": 1}),
        ('{"a": 1}', {"a": 1}),
        ("", {}),
        (None, {}),
        ("[1]", {"input": [1]}),
        ("not json", {"input": "not json"}),
        (5, {"input": 5}),
    ],
)
def test_arguments_are_read_from_a_dict_or_json_text(raw, expected):
    assert _adapter.parse_args(raw) == expected


@pytest.mark.parametrize(
    "value,expected",
    [
        (' {"a": 1} ', {"a": 1}),
        ("[1, 2]", [1, 2]),
        ("{broken", "{broken"),
        ("plain", "plain"),
        (3, 3),
        (None, None),
        (object, str(object)),
    ],
)
def test_results_are_json_data(value, expected):
    assert _adapter.plain_result(value) == expected


def test_the_texts_the_model_sees_are_the_same_in_every_adapter():
    from sam_link_sdk.prompting import ApprovalResult, UserAnswer

    assert (
        _adapter.refusal("route_claim", ApprovalResult(False, "denied"))
        == "The user did not approve this call to route_claim (denied), so it was not run."
    )
    assert (
        _adapter.access_denied("route_claim")
        == "Access denied: you do not have the scopes required to use route_claim."
    )
    assert _adapter.answer_text(UserAnswer("answered", ("yes",))) == "yes"
    assert _adapter.answer_text(UserAnswer("timed_out")) == "The user did not answer (timed_out)."
    assert _adapter.join_prompts("base", "", "extra") == "base\n\nextra"


@pytest.mark.parametrize("names", ["route_claim", b"route_claim", 3])
def test_names_that_are_not_a_list_of_names_are_a_type_error_rather_than_a_silent_choice(names):
    with pytest.raises(TypeError, match="list of tool names"):
        _adapter.name_filter(names)


def test_names_that_hold_anything_but_strings_are_a_type_error():
    with pytest.raises(TypeError, match="not SimpleNamespace"):
        _adapter.name_filter(["route_claim", SimpleNamespace(name="lookup_policy")])
    assert [n for n in ("route_claim", "lookup_policy") if _adapter.name_filter(n for n in ["route_claim"])(n)] == [
        "route_claim"
    ]
    assert _adapter.name_filter(None)("anything")


def test_an_approval_covers_the_named_tools_among_those_given_and_refuses_a_name_that_is_none_of_them():
    """A name that is none of the tools is refused.

    A name misspelt next to the real tool is the case that matters, because it would leave the real tool unguarded.
    """
    tools = [SimpleNamespace(name="lookup_policy"), SimpleNamespace(tool_name="route_claim")]
    covers = _adapter.approval_filter(tools, ["route_claim"])
    assert [n for n in ("lookup_policy", "route_claim", "peer_billing") if covers(n)] == ["route_claim"]
    with pytest.raises(ValueError, match="route_clam"):
        _adapter.approval_filter(tools, ["route_clam"])
    assert [n for n in ("lookup_policy", "route_claim", "other") if _adapter.approval_filter(tools, None)(n)] == [
        "lookup_policy",
        "route_claim",
    ]
    with pytest.raises(TypeError):
        _adapter.approval_filter(tools, "route_claim")


def test_a_tool_s_name_is_read_the_way_each_framework_spells_it():
    def route_claim_impl():
        pass

    assert tool_name(SimpleNamespace(tool_name="route_claim", __name__="route_claim_impl")) == "route_claim"
    assert tool_name(SimpleNamespace(name="route_claim")) == "route_claim"
    assert tool_name({"type": "function", "function": {"name": "route_claim"}}) == "route_claim"
    assert tool_name({"name": "route_claim"}) == "route_claim"
    assert tool_name(route_claim_impl) == "route_claim_impl"
    assert tool_name(object()) == ""


def _scoped_task(records):
    """A task of alice's, whom SAM granted none of route_claim's scopes."""
    return adapter_task(
        "T",
        "x",
        scopes={"route_claim": ["tool:claims:route"]},
        declared=["lookup_policy"],
        granted=[],
        audit=records.append,
        user_id="alice",
    )


async def test_a_runs_scopes_hide_and_refuse_a_denied_tool_and_audit_it_once():
    records = []
    async with _scoped_task(records) as task:
        scopes = _adapter.ToolScopes(task.ctx)
        tools = [SimpleNamespace(name="lookup_policy"), SimpleNamespace(tool_name="route_claim")]
        assert scopes.visible(tools) == tools[:1]
        assert not scopes.permits("route_claim") and scopes.permits("lookup_policy")
        assert scopes.denied == {"route_claim"}
    assert [(r.message, r.fields["tool"]) for r in records] == [("tool access denied", "route_claim")]


async def test_permitted_tools_matches_a_strands_tool_by_the_name_the_model_calls_it():
    strands = pytest.importorskip("strands")

    @strands.tool(name="route_claim")
    def route(claim_id: str) -> str:
        """Route a claim."""
        return "fast-track"

    @strands.tool
    def lookup_policy(policy_id: str) -> str:
        """Look up a policy."""
        return "gold"

    records = []
    async with _scoped_task(records) as task:
        assert task.ctx.permitted_tools([lookup_policy, route]) == [lookup_policy]
    assert [r.fields["tool"] for r in records] == ["route_claim"]


def test_a_refused_call_is_the_same_steps_and_result_in_every_adapter():
    started, finished = _adapter.refusal_steps("route_claim", {"claim_id": "C-1"}, "call_r")
    assert started == _adapter.tool_started("route_claim", {"claim_id": "C-1"}, "call_r")
    assert finished.failed and finished.result == {"error": _adapter.access_denied("route_claim")}
    assert _adapter.refused_result("route_claim") == finished.result
    assert str(_adapter.ToolAccessDenied("route_claim")) == _adapter.access_denied("route_claim")
