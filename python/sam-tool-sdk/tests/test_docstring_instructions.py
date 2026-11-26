# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The Args section of a tool's docstring reaches the model once, as the
parameter descriptions, not a second time inside the instructions; anything
the parameter descriptions would not carry stays in the instructions."""

from typing import Any, Optional

import pytest

from sam_tool_sdk.context_facade import SandboxToolContextFacade
from sam_tool_sdk.tool_runner import _parse_docstring


async def lookup(customer_id: str, limit: int = 10) -> Any:
    """Look up a customer's orders.

    Use the customer_id from the CRM, never the email address.

    Args:
        customer_id: The CRM customer id, e.g. "CUST-01693".
        limit: How many orders to return,
            newest first.

    Notes:
        Cancelled orders are included.
    """


async def no_args(text: str) -> Any:
    """Echo text.

    Keep it short.
    """


def test_instructions_leave_out_an_args_section_the_schema_carries():
    description, instructions, params = _parse_docstring(lookup)

    assert description == "Look up a customer's orders."
    assert "never the email address" in instructions
    assert "Cancelled orders are included." in instructions
    assert "Args:" not in instructions
    assert "CRM customer id" not in instructions and "newest first" not in instructions
    assert "\n\n\n" not in instructions
    assert params == {"customer_id": 'The CRM customer id, e.g. "CUST-01693".',
                      "limit": "How many orders to return, newest first."}


def test_instructions_without_an_args_section_are_unchanged():
    _, instructions, params = _parse_docstring(no_args)

    assert instructions == "Keep it short."
    assert params == {}


async def trailing_note(a: str) -> Any:
    """Do a thing.

    Args:
        a: The input.

    Note:
        Never exceed 5 calls a minute.
    """


async def trailing_warning(a: str) -> Any:
    """Do a thing.

    Args:
        a: The input.

    WARNING: the upstream API is billed per call.
    """


async def trailing_keyword_args(a: str, **kwargs: Any) -> Any:
    """Do a thing.

    Args:
        a: The input.

    Keyword Args:
        retries: How often to retry.
    """


@pytest.mark.parametrize("func, kept", [
    (trailing_note, "Never exceed 5 calls a minute."),
    (trailing_warning, "WARNING: the upstream API is billed per call."),
    (trailing_keyword_args, "retries: How often to retry."),
])
def test_text_after_the_args_block_stays_in_the_instructions(func, kept):
    _, instructions, _ = _parse_docstring(func)

    assert kept in instructions


async def bullet_entries(a: str, b: str) -> Any:
    """Do a thing.

    Args:
        - a: The first input.
        - b: The second input.
    """


async def grouped_entries(a: str, b: str) -> Any:
    """Do a thing.

    Args:
        a, b: The two inputs, in order.
    """


async def injected_context(a: str, tool_context: SandboxToolContextFacade = None) -> Any:
    """Do a thing.

    Args:
        a: The input.
        tool_context: Only call send_status once per run.
    """


async def unknown_param(a: str) -> Any:
    """Do a thing.

    Args:
        a: The input.
        mode: Pass "fast" when the user is in a hurry.
    """


@pytest.mark.parametrize("func, kept", [
    (bullet_entries, "- a: The first input."),
    (grouped_entries, "a, b: The two inputs, in order."),
    (injected_context, "tool_context: Only call send_status once per run."),
    (unknown_param, 'mode: Pass "fast" when the user is in a hurry.'),
])
def test_an_args_block_the_schema_does_not_fully_carry_is_kept_whole(func, kept):
    _, instructions, _ = _parse_docstring(func)

    assert "Args:" in instructions
    assert kept in instructions


async def args_inside_an_example(a: str) -> Any:
    """Do a thing.

    Example:
        Args:
            a: "x"
    """


def test_an_orphan_line_under_an_args_header_in_an_example_is_kept():
    _, instructions, _ = _parse_docstring(args_inside_an_example)

    assert 'a: "x"' in instructions


async def deep_entries(a: str) -> Any:
    """Do a thing.

    Args:
            a: Entries indented as deep as a continuation.
    """


async def hanging_continuation(a: str) -> Any:
    """Do a thing.

    Args:
      a: The first line of a hanging entry,
        whose second line the parser does not join.
    """


async def colon_in_type(a: str) -> Any:
    """Do a thing.

    Args:
        a (Literal['x:y']): Pick x or y.
    """


async def param_named_notes(a: str, notes: str) -> Any:
    """Do a thing.

    Args:
        a: The input.
        notes:
            Free text for the reviewer.
    """


async def optional_context(a: str, ctx: Optional[SandboxToolContextFacade] = None) -> Any:
    """Do a thing.

    Args:
        a: The input.
        ctx: Report progress through it.
    """


@pytest.mark.parametrize("func, kept", [
    (deep_entries, "a: Entries indented as deep as a continuation."),
    (hanging_continuation, "whose second line the parser does not join."),
    (colon_in_type, "a (Literal['x:y']): Pick x or y."),
    (param_named_notes, "Free text for the reviewer."),
    (optional_context, "ctx: Report progress through it."),
])
def test_text_the_parameter_descriptions_would_not_carry_stays_in_the_instructions(func, kept):
    _, instructions, _ = _parse_docstring(func)

    assert kept in instructions


def _tool(doc: str):
    async def tool(a: str, b: str = "", tool_config: Optional[dict] = None, **kwargs: Any) -> Any:
        pass

    tool.__doc__ = doc
    return tool


def _method(doc: str):
    class Tool:
        async def run(self, a: str) -> Any:
            pass

    Tool.run.__doc__ = doc
    return Tool.run


GUIDE = "Do a thing.\n\nGuide.\n\n"


@pytest.mark.parametrize("header", ["Returns:", "Return:", "Raises:", "Yields:", "Example:", "Examples:", "Notes:"])
def test_an_args_section_ends_at_each_section_header(header):
    _, instructions, params = _parse_docstring(_tool(GUIDE + f"Args:\n    a: One.\n\n{header}\n    Text.\n"))

    assert instructions == f"Guide.\n\n{header}\n    Text."
    assert params == {"a": "One."}


@pytest.mark.parametrize("header", ["Args:", "Arguments:", "Parameters:"])
def test_each_args_header_is_read_and_left_out(header):
    _, instructions, params = _parse_docstring(_tool(GUIDE + f"{header}\n    a: One.\n    b: Two.\n"))

    assert instructions == "Guide."
    assert params == {"a": "One.", "b": "Two."}


def test_two_carried_args_sections_are_both_left_out():
    _, instructions, params = _parse_docstring(_tool(GUIDE + "Args:\n    a: One.\n\nArgs:\n    b: Two.\n"))

    assert instructions == "Guide."
    assert params == {"a": "One.", "b": "Two."}


def test_a_second_args_section_the_schema_does_not_carry_keeps_its_header():
    _, instructions, _ = _parse_docstring(_tool(GUIDE + "Args:\n    a: One.\n\nArgs:\n    - b: Two.\n"))

    assert instructions == "Guide.\n\nArgs:\n    - b: Two."


def test_an_orphan_line_before_the_first_entry_keeps_the_section():
    _, instructions, _ = _parse_docstring(_tool(GUIDE + "Args:\n        Always pass a.\n    a: One.\n"))

    assert "Always pass a." in instructions


@pytest.mark.parametrize("make, name", [
    (_tool, "tool_config"),
    (_tool, "kwargs"),
    (_method, "self"),
])
def test_a_documented_parameter_no_builder_emits_keeps_the_section(make, name):
    _, instructions, _ = _parse_docstring(make(GUIDE + f"Args:\n    a: One.\n    {name}: Needs an api_key.\n"))

    assert "Needs an api_key." in instructions


def test_text_between_the_name_and_the_colon_keeps_the_section():
    _, instructions, _ = _parse_docstring(_tool(GUIDE + "Args:\n    a (int) optional: One.\n"))

    assert "a (int) optional: One." in instructions


@pytest.mark.parametrize("doc", [
    GUIDE + "Args:\n    a: Never exceed 100.\n    b: Two.\n    a: One.\n",
    GUIDE + "Args:\n    a: Never exceed 100.\n\nReturns:\n    Rows.\n\nParameters:\n    a: One.\n",
    GUIDE + "Args:\n    a: Never exceed 100.\n\nNotes:\n    x\n\nArgs:\n    a: One.\n",
])
def test_a_parameter_described_twice_keeps_the_description_it_replaces(doc):
    _, instructions, params = _parse_docstring(_tool(doc))

    assert params["a"] == "One."
    assert "a: Never exceed 100." in instructions


def test_blank_lines_outside_an_args_section_are_unchanged():
    _, instructions, _ = _parse_docstring(_tool("Do a thing.\n\nKeep.\n\n\n\nMore.\n"))

    assert instructions == "Keep.\n\n\n\nMore."


@pytest.mark.parametrize("entry", [
    "a (int, max 100, never above the plan quota): One.",
    "a (str, must be lowercase): One.",
    "a (seconds): One.",
    "a (deprecated): One.",
    "a (csv | json): One.",
    "a (Literal['open', 'closed']): One.",
    "a (str[max 100 chars]): One.",
    "a (datetime): One.",
    "a (int): One.",
    "a (int, optional): One.",
])
def test_any_type_slot_keeps_the_section(entry):
    _, instructions, params = _parse_docstring(_tool(GUIDE + f"Args:\n    {entry}\n    b: Two.\n"))

    assert entry in instructions
    assert params == {"a": "One.", "b": "Two."}


def test_a_line_indented_less_than_a_continuation_is_an_entry_as_on_main():
    _, instructions, params = _parse_docstring(_tool(GUIDE + "Args:\n    a: One.\n       seven.\n"))

    assert params == {"a": "One."}
    assert "seven." in instructions


def test_a_long_type_slot_is_read_in_linear_time():
    entry = "a (" + "x[y] | " * 5000 + "z!): One."

    _, instructions, _ = _parse_docstring(_tool(GUIDE + f"Args:\n    {entry}\n"))

    assert entry in instructions


def test_every_section_after_an_args_section_is_kept():
    _, instructions, _ = _parse_docstring(
        _tool(GUIDE + "Args:\n    a: One.\n\nReturns:\n    Rows.\n\nRaises:\n    Boom.\n"))

    assert instructions == "Guide.\n\nReturns:\n    Rows.\n\nRaises:\n    Boom."


def test_a_line_without_a_colon_keeps_the_section():
    _, instructions, params = _parse_docstring(_tool(GUIDE + "Args:\n    a: One.\n    Note that a is required.\n"))

    assert "Note that a is required." in instructions
    assert params == {"a": "One."}


def test_prose_under_a_second_args_header_is_kept():
    _, instructions, _ = _parse_docstring(_tool(GUIDE + "Args:\n    a: One.\n\nParameters:\n    Some prose.\n"))

    assert "Some prose." in instructions


def test_grouped_names_are_not_a_parameter():
    _, _, params = _parse_docstring(_tool(GUIDE + "Args:\n    a, b: The two.\n    - b: The second.\n"))

    assert params == {}


@pytest.mark.parametrize("between", ["", "Returns:\n    R.\n\n"])
def test_a_continuation_after_a_second_args_header_joins_the_previous_parameter_as_on_main(between):
    _, _, params = _parse_docstring(_tool(GUIDE + f"Args:\n    a: One.\n\n{between}Args:\n        orphan.\n    b: Two.\n"))

    assert params == {"a": "One. orphan.", "b": "Two."}


@pytest.mark.parametrize("doc", [
    GUIDE + "Args:\n    a: One.\n\nReturns:\n    R.\n\nArgs:\n    a: Two.\n\nNotes:\n    N.\n\nArgs:\n    a: Three.\n",
    GUIDE + "Args:\n    a: One.\n    a: Two.\n    a: Three.\n",
])
def test_three_entries_for_one_parameter_keep_the_two_replaced(doc):
    _, instructions, params = _parse_docstring(_tool(doc))

    assert params["a"] == "Three."
    assert "a: One." in instructions and "a: Two." in instructions


def test_a_replaced_entry_a_second_args_header_continues_is_kept():
    _, instructions, _ = _parse_docstring(
        _tool(GUIDE + "Args:\n    a: Never exceed 100.\n\nReturns:\n    R.\n\nArgs:\n        orphan.\n    a: One.\n"))

    assert "a: Never exceed 100." in instructions and "orphan." in instructions


def test_a_carried_section_beside_a_replaced_entry_is_still_left_out():
    _, instructions, _ = _parse_docstring(_tool(
        GUIDE + "Args:\n    a: Never exceed 100.\n\nReturns:\n    R.\n\nArgs:\n    b: Two.\n\nNotes:\n    N.\n\nArgs:\n    a: One.\n"))

    assert instructions == "Guide.\n\nArgs:\n    a: Never exceed 100.\n\nReturns:\n    R.\n\nNotes:\n    N."


def _unannotated_context(doc: str):
    async def tool(a: str, tool_context=None) -> Any:
        pass

    tool.__doc__ = doc
    return tool


def _plain_kwargs(doc: str):
    async def tool(a: str, kwargs: Optional[dict] = None) -> Any:
        pass

    tool.__doc__ = doc
    return tool


def _classmethod(doc: str):
    class Tool:
        @classmethod
        async def run(cls, a: str) -> Any:
            pass

    Tool.run.__func__.__doc__ = doc
    return Tool.run.__func__


@pytest.mark.parametrize("make, name", [
    (_unannotated_context, "tool_context"),
    (_plain_kwargs, "kwargs"),
    (_classmethod, "cls"),
])
def test_a_documented_parameter_named_like_a_skipped_one_keeps_the_section(make, name):
    _, instructions, _ = _parse_docstring(make(GUIDE + f"Args:\n    a: One.\n    {name}: Needs an api_key.\n"))

    assert "Needs an api_key." in instructions
