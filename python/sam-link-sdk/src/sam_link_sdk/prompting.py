# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Human in the loop, as the core sees it: questions for the user and the answers that come back.

How a prompt is shown (the entrypoint's own screens on the agent link) is the transport's business.
"""

from __future__ import annotations

import math
from dataclasses import dataclass

from ._model import (
    ANSWERED,
    CANCELLED,
    TIMED_OUT,
    Approval,
    Prompt,
    PromptAnswer,
    PromptOption,
    PromptQuestion,
    PromptTooLarge,
)
from .ids import uuid7_str

__all__ = [
    "ANSWERED",
    "APPROVED",
    "CANCELLED",
    "DEFAULT_TIMEOUT_SECONDS",
    "DENIED",
    "TIMED_OUT",
    "UNREADABLE",
    "ApprovalResult",
    "Option",
    "PromptTooLarge",
    "Question",
    "UserAnswer",
]

DEFAULT_TIMEOUT_SECONDS = 45 * 60
MAX_TIMEOUT_SECONDS = 365 * 24 * 3600.0
"""The longest wait a prompt asks SAM for. SAM cuts any wait at its task TTL anyway, and a longer one (``math.inf`` for
no limit) is not a number the agent link carries."""

SOURCE_ASK = "ask_user_question"
SOURCE_APPROVAL = "tool_approval"

APPROVED, DENIED, UNREADABLE = "approved", "denied", "unreadable"


@dataclass(frozen=True)
class Option:
    label: str
    value: str = ""
    description: str = ""


@dataclass(frozen=True)
class Question:
    text: str
    options: tuple[Option, ...] = ()
    header: str = ""
    multi_select: bool = False


@dataclass(frozen=True)
class UserAnswer:
    """The user's reply to ask_user. `answers` has one string per question, in order, when status is "answered";
    a choice of "Other" is replaced by what the user typed."""

    status: str
    answers: tuple[str, ...] = ()

    @property
    def answered(self) -> bool:
        return self.status == ANSWERED


@dataclass(frozen=True)
class ApprovalResult:
    approved: bool
    outcome: str


def new_request_id(source: str) -> str:
    return ("hil-approval-" if source == SOURCE_APPROVAL else "") + uuid7_str()


def prompt_timeout(seconds: float) -> float:
    """The wait a prompt asks for, as a finite number of seconds: NaN, zero or less is SAM's default (0), anything over
    MAX_TIMEOUT_SECONDS is cut to it."""
    if math.isnan(seconds):
        return 0.0
    return min(max(seconds, 0.0), MAX_TIMEOUT_SECONDS)


def question_prompt(questions: list[Question], message: str, timeout_seconds: float) -> Prompt:
    asked = tuple(
        PromptQuestion(
            q.text, q.header, tuple(PromptOption(o.label, o.description, o.value) for o in q.options), q.multi_select
        )
        for q in questions
    )
    return Prompt(new_request_id(SOURCE_ASK), prompt_timeout(timeout_seconds), questions=asked, message=message)


def approval_prompt(tool: str, args, message: str, timeout_seconds: float) -> Prompt:
    return Prompt(
        new_request_id(SOURCE_APPROVAL),
        prompt_timeout(timeout_seconds),
        approval=Approval(tool, dict(args or {}), message),
    )


def user_answer(answer: PromptAnswer) -> UserAnswer:
    if answer.status != ANSWERED:
        return UserAnswer(answer.status)
    return UserAnswer(answer.status, tuple(answer.answers))


def approval_result(answer: PromptAnswer) -> ApprovalResult:
    """Anything but an explicit approval (denied, cancelled, timed out, unreadable) is not approved."""
    if answer.status == TIMED_OUT:
        return ApprovalResult(False, TIMED_OUT)
    if answer.status != ANSWERED:
        return ApprovalResult(False, CANCELLED)
    if answer.approved is None:
        return ApprovalResult(False, UNREADABLE)
    return ApprovalResult(answer.approved, APPROVED if answer.approved else DENIED)
