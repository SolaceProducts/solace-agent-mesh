# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Credential redaction: parity with SAM-Go's redactor, plus the behaviours the SDK relies on."""

from __future__ import annotations

import json
import pathlib
import time

import pytest

from sam_link_sdk import Usage, _adapter
from sam_link_sdk.link.v2 import Limits, event_body
from sam_link_sdk.redact import REDACTED, redact_string, redact_value

CASES = json.loads((pathlib.Path(__file__).parent / "fixtures" / "go_redact_cases.json").read_text())


@pytest.mark.parametrize("index", range(len(CASES)))
def test_matches_the_go_redactor(index):
    case = CASES[index]
    assert redact_value(case["input"]) == case["output"], f"case {index}: {json.dumps(case['input'])[:120]}"


def test_the_original_is_never_changed():
    original = {"password": "hunter2hunter2", "nested": [{"token": "abcdefghijkl"}], "n": 1}
    snapshot = json.dumps(original)
    redacted = redact_value(original)
    assert json.dumps(original) == snapshot
    assert redacted["password"] == REDACTED and redacted["nested"][0]["token"] == REDACTED and redacted["n"] == 1


def test_names_stay_visible_and_only_the_secret_goes():
    assert redact_string("export API_KEY=abcdef1234567890 now") == f"export API_KEY={REDACTED} now"
    assert redact_value({"password": "x"}) == {"password": REDACTED}


def test_token_counts_and_metadata_are_not_credentials():
    value = {"max_tokens": 4096, "token_url": "https://idp.example.com/token", "auth_type": "oauth2"}
    assert redact_value(value) == value


def test_other_types_are_normalised_or_marked():
    class Row:
        def __str__(self):
            return "row-1"

    assert redact_value({"obj": Row(), "raw": b"\x00\x01", "t": (1, 2)}) == {
        "obj": "row-1",
        "raw": "<binary 2 bytes>",
        "t": [1, 2],
    }

    class Bad:
        def __str__(self):
            raise ValueError("no")

    assert redact_value(Bad()) == "<unserializable value>"


def test_long_text_is_capped_and_cheap_to_scan():
    started = time.perf_counter()
    out = redact_string("a-b." * 6000 + " password=abcdefghijkl")
    assert time.perf_counter() - started < 2.0
    assert len(out.encode()) <= 16384
    started = time.perf_counter()
    redact_string("token=abcdefghijkl " * 3000)
    assert time.perf_counter() - started < 2.0


def test_reported_steps_are_redacted_before_they_reach_the_link():
    start = _adapter.tool_started("login", {"user": "bob", "password": "hunter2hunter2"}, "c1")
    assert start.args == {"user": "bob", "password": REDACTED}
    result = _adapter.tool_finished(
        "fetch", {"headers": {"Authorization": "Bearer abcdefghijklmnop"}, "body": "ok"}, "c1"
    )
    assert result.result == {"headers": {"Authorization": REDACTED}, "body": "ok"}
    assert _adapter.tool_finished("fetch", "token=abcdefghijkl", "c1").result == f"token={REDACTED}"
    response = _adapter.model_finished(
        "m", "", [{"name": "login", "args": {"api_key": "abcdef123456"}, "id": "c1"}], Usage()
    )
    sent = json.dumps([event_body(e, "m", Limits()) for e in (start, result, response)])
    assert "hunter2" not in sent and "abcdefghijklmnop" not in sent and "abcdef123456" not in sent
    assert event_body(response, "m", Limits())["toolCalls"][0]["args"] == {"api_key": REDACTED}
