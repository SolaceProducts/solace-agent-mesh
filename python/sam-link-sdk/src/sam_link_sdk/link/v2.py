# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The agent link protocol, frame by frame: the core's values as the protocol's frames, and back.

The agent says what happened; the entrypoint builds SAM's messages (``docs/link-protocol.md``). These functions
reproduce nothing of SAM's internals (no envelopes, signal shapes, cards, approval screens, spend events or token
checks).
``LinkTransport`` (``transport.py``) holds the protocol's state and uses them; ``describer.py`` and ``usage_ledger.py`` hold
its description publishing and its usage accounting. Every frame is built within the
limits the welcome announces (``Limits``), so the entrypoint never refuses one for its size or shape.
"""

from __future__ import annotations

import base64
import binascii
import hashlib
import json
import logging
import math
import re
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from typing import Any, TypeGuard

from .._model import (
    ANSWERED,
    CANCELLED,
    PEER_CALL_GRANTED,
    RUN,
    SESSION,
    TIMED_OUT,
    AgentDescription,
    IncomingTask,
    InputFile,
    PeerInfo,
    PeerResult,
    PeerSkill,
    Prompt,
    PromptAnswer,
    TaskError,
    TaskOutcome,
    Workflow,
)
from ..captext import cap_text, well_formed
from ..events import (
    ModelFailed,
    ModelFinished,
    ModelStarted,
    Progress,
    StepEvent,
    Text,
    Thinking,
    ToolFinished,
    ToolStarted,
)
from ..redact import redact_string
from ..request_context import RequestContext
from ..usage import Usage

log = logging.getLogger("sam_link_sdk.link")

MAX_DESCRIBE_BYTES = 256 * 1024
"""The entrypoint refuses a describe whose agent object is larger than this (SAM-Go maxCardBytes)."""
UNKNOWN_MODEL = "unknown"
GENERIC_FAILURE_TEXT = "internal error"
"""What the user is told for a failure that is not a failed model call (SAM's own agents' -32603 says the same)."""
RESULT_STATES = ("completed", "failed", "canceled")
PROMPT_STATUSES = {"answered": ANSWERED, "canceled": CANCELLED, "timed_out": TIMED_OUT}
"""prompt_answer.status on the wire -> the SDK's own name for it. The wire spells canceled with one l in every field, as
A2A's task states do; the SDK's outcomes keep "cancelled".
"""
PEER_STATES = ("completed", "failed", "canceled", "rejected")
JOIN = ", "
HASHED_ID_PREFIX = "h-"
FRAME_OVERHEAD_BYTES = 4096
TOO_LARGE_TEXT = "The answer was larger than the agent link carries."


@dataclass(frozen=True)
class Limits:
    """What the entrypoint takes (``welcome.limits``); the defaults are SAM-Go's, for a welcome that leaves one out."""

    max_frame_bytes: int = 8 * 1024 * 1024
    max_string_bytes: int = 16384
    max_id_bytes: int = 128
    max_name_bytes: int = 256
    max_tool_calls_per_model_call: int = 128
    max_models_per_task: int = 32
    max_open_peer_calls: int = 1024
    max_open_prompts: int = 1024
    max_tool_scopes: int = 64
    max_failure_message_bytes: int = 2048
    max_frame_values: int = 16384
    max_prompt_questions: int = 4
    max_prompt_options: int = 4
    max_prompt_text_bytes: int = 2048
    max_question_header_chars: int = 12
    max_approval_args: int = 64
    max_approval_args_bytes: int = 65536
    max_tool_payload_bytes: int = 262144
    max_description_bytes: int = 4096
    max_skills: int = 32
    max_skill_description_bytes: int = 1024

    @classmethod
    def from_welcome(cls, limits: Any, configured_max_frame_bytes: int) -> Limits:
        """The welcome's limits; the largest frame is at most the configured one, which the agent was sized for."""
        limits = limits if isinstance(limits, Mapping) else {}
        defaults = cls()

        def read(wire: str, default: int) -> int:
            value = limits.get(wire)
            return value if positive_int(value) else default

        announced_frame = read("maxFrameBytes", configured_max_frame_bytes)
        return cls(
            max_frame_bytes=min(announced_frame, configured_max_frame_bytes),
            max_string_bytes=read("maxStringBytes", defaults.max_string_bytes),
            max_id_bytes=read("maxIdBytes", defaults.max_id_bytes),
            max_name_bytes=read("maxNameBytes", defaults.max_name_bytes),
            max_tool_calls_per_model_call=read("maxToolCallsPerModelCall", defaults.max_tool_calls_per_model_call),
            max_models_per_task=read("maxModelsPerTask", defaults.max_models_per_task),
            max_open_peer_calls=read("maxOpenPeerCalls", defaults.max_open_peer_calls),
            max_open_prompts=read("maxOpenPrompts", defaults.max_open_prompts),
            max_tool_scopes=read("maxToolScopes", defaults.max_tool_scopes),
            max_failure_message_bytes=read("maxFailureMessageBytes", defaults.max_failure_message_bytes),
            max_frame_values=read("maxFrameValues", defaults.max_frame_values),
            max_prompt_questions=read("maxPromptQuestions", defaults.max_prompt_questions),
            max_prompt_options=read("maxPromptOptions", defaults.max_prompt_options),
            max_prompt_text_bytes=read("maxPromptTextBytes", defaults.max_prompt_text_bytes),
            max_question_header_chars=read("maxQuestionHeaderChars", defaults.max_question_header_chars),
            max_approval_args=read("maxApprovalArgs", defaults.max_approval_args),
            max_approval_args_bytes=read("maxApprovalArgsBytes", defaults.max_approval_args_bytes),
            max_tool_payload_bytes=read("maxToolPayloadBytes", defaults.max_tool_payload_bytes),
            max_description_bytes=read("maxDescriptionBytes", defaults.max_description_bytes),
            max_skills=read("maxSkills", defaults.max_skills),
            max_skill_description_bytes=read("maxSkillDescriptionBytes", defaults.max_skill_description_bytes),
        )


def fit_name(name: str, max_bytes: int) -> str:
    """A name (a model, tool, provider or pricing key) cut to what the entrypoint takes. Names only label what
    happened, so cutting one changes nothing but its spelling in SAM."""
    encoded = name.encode("utf-8")
    if len(encoded) <= max_bytes:
        return name
    return encoded[:max_bytes].decode("utf-8", errors="ignore")


def fit_id(value: str, max_bytes: int) -> str:
    """An id (a tool call's, a prompt's) the entrypoint takes: as it is when short enough, else its hash, the same
    for every use of the id so a call's start, finish and model response still pair up."""
    if len(value.encode("utf-8")) <= max_bytes:
        return value
    return (HASHED_ID_PREFIX + hashlib.sha256(value.encode("utf-8")).hexdigest())[:max_bytes]


def tool_scopes(tools: Any) -> list[str]:
    """The distinct scopes the agent's tools require, in first-named order."""
    seen: dict[str, None] = {}
    for tool in tools:
        for scope in tool.required_scopes:
            seen.setdefault(scope, None)
    return list(seen)


def describe_object(desc: AgentDescription) -> dict[str, Any]:
    """The agent object of a describe frame."""
    declared = desc.declared
    return {
        "displayName": desc.display_name,
        "description": desc.description,
        "version": desc.version,
        "skills": [{"id": s.id, "name": s.name, "description": s.description} for s in desc.skills],
        "tools": [
            {"name": t.name, "description": t.description, "requiredScopes": list(t.required_scopes)}
            for t in desc.tools
        ],
        "requiredScopes": list(desc.required_scopes),
        "inputSchema": desc.input_schema,
        "outputSchema": desc.output_schema,
        "welcomeMessage": desc.welcome_message,
        "suggestions": [{"title": s.label, "prompt": s.prompt, "autoSend": s.auto_send} for s in desc.suggestions],
        "peers": list(desc.peers),
        "streaming": desc.streaming,
        "declared": {
            "framework": declared.framework,
            "owner": declared.owner,
            "team": declared.team,
            "sdk": {"name": declared.sdk_name, "version": declared.sdk_version},
            "dataAccess": [
                {"system": d.system, "classification": d.classification, "access": d.access}
                for d in declared.data_access
            ],
            "mcpServers": [{"name": m.name, "url": m.url} for m in declared.mcp_servers],
        },
        "health": [{"id": h.id, "severity": h.severity, "detail": h.detail} for h in desc.health],
    }


def overlong_description(desc: AgentDescription, limits: Limits) -> str:
    """Why the entrypoint would refuse this description for its length (it then gives the agent no task), or "" when
    it takes it."""
    if len(desc.description.encode("utf-8")) > limits.max_description_bytes:
        return f"the agent's description is over {limits.max_description_bytes} bytes"
    if len(desc.skills) > limits.max_skills:
        return f"the agent has {len(desc.skills)} skills; the agent link takes at most {limits.max_skills}"
    for skill in desc.skills:
        if len(skill.name.encode("utf-8")) > limits.max_name_bytes:
            return f"the skill {skill.id!r} has a name over {limits.max_name_bytes} bytes"
        if len(skill.description.encode("utf-8")) > limits.max_skill_description_bytes:
            return f"the skill {skill.id!r} has a description over {limits.max_skill_description_bytes} bytes"
    return ""


def event_body(event: StepEvent, model: str, limits: Limits) -> dict[str, Any] | None:
    """The event object of an event frame, without usage (see LinkTransport.send_event), within ``limits``.
    ``model`` is the model the event is reported under. None for a tool event without its call id or tool name,
    which the entrypoint would refuse.

    A model response's tool calls are listed up to the limit; each call is still reported by its own tool_started and
    tool_finished.
    """
    names, ids = limits.max_name_bytes, limits.max_id_bytes
    model = fit_name(model or UNKNOWN_MODEL, names)
    if isinstance(event, ModelFinished):
        body: dict[str, Any] = {"kind": "model_finished", "model": model, "text": event.text}
        calls = [c for c in event.tool_calls if c.id and c.name]
        if len(calls) < len(event.tool_calls):
            log.warning("dropping tool calls without an id or a name count=%d", len(event.tool_calls) - len(calls))
        if len(calls) > limits.max_tool_calls_per_model_call:
            log.warning(
                "model response lists more tool calls than the agent link carries; listing the first "
                "count=%d listed=%d",
                len(calls),
                limits.max_tool_calls_per_model_call,
            )
            calls = calls[: limits.max_tool_calls_per_model_call]
        if calls:
            body["toolCalls"] = [
                {"id": fit_id(c.id, ids), "name": fit_name(c.name, names), "args": json_value(dict(c.args))}
                for c in calls
            ]
        return body
    if isinstance(event, ModelStarted):
        return {"kind": "model_started", "model": model}
    if isinstance(event, ModelFailed):
        return {
            "kind": "model_failed",
            "model": model,
            "category": event.category,
            "provider": fit_name(event.provider, names),
        }
    if isinstance(event, (ToolStarted, ToolFinished)):
        if not event.call_id or not event.name:
            log.warning("dropping a tool step without a call id or a tool name kind=%s", event.kind)
            return None
        body = {"kind": event.kind, "callId": fit_id(event.call_id, ids), "name": fit_name(event.name, names)}
        if isinstance(event, ToolStarted):
            body["args"] = json_value(dict(event.args)) if event.args is not None else None
        else:
            body.update(result=json_value(event.result), failed=bool(event.failed))
        return body
    if isinstance(event, Thinking):
        return {"kind": "thinking", "text": event.text, "done": event.done}
    if isinstance(event, (Progress, Text)):
        return {"kind": event.kind, "text": event.text}
    raise TypeError(f"not a step event: {type(event).__name__}")


def usage_model_finished(model: str, limits: Limits) -> dict[str, Any]:
    """A model_finished that carries only usage: for usage no reported model call carried."""
    return {"kind": "model_finished", "model": fit_name(model or UNKNOWN_MODEL, limits.max_name_bytes)}


def incoming_task(task_id: str, body: Mapping[str, Any]) -> IncomingTask:
    """The core's task from a task frame's task object.

    A task that does not say whether the user can be prompted is read as not promptable, so a prompt the entrypoint
    would answer cancelled at once is not sent.
    """
    workflow = body.get("workflow")
    user = body.get("user")
    rc = body.get("requestContext")
    rc = rc if isinstance(rc, Mapping) else {}
    metadata = body.get("metadata")
    return IncomingTask(
        task_id=task_id,
        context_id=_str(body.get("contextId")),
        text=_str(body.get("text")),
        files=[f for f in (input_file(entry) for entry in body.get("files") or []) if f is not None],
        input=body.get("input"),
        workflow=Workflow(_str(workflow.get("name")), _str(workflow.get("nodeId")))
        if isinstance(workflow, Mapping)
        else None,
        session_behavior=RUN if body.get("sessionBehavior") == "run" else SESSION,
        user_id=_str(user.get("id")) if isinstance(user, Mapping) else "",
        call_depth=_int(body.get("callDepth")),
        request_context=RequestContext(
            system_purpose=_str(rc.get("systemPurpose")),
            response_format=_str(rc.get("responseFormat")),
            project_id=_str(rc.get("projectId")),
            conversation_context=_str(rc.get("conversationContext")),
            user_profile=_str(rc.get("userProfile")),
            user_timezone=_str(rc.get("userTimezone")),
        ),
        metadata=dict(metadata) if isinstance(metadata, Mapping) else {},
        granted_tool_scopes=granted_tool_scopes(body.get("grantedToolScopes")),
        can_prompt_user=body.get("canPromptUser") is True,
    )


def granted_tool_scopes(raw: Any) -> frozenset[str]:
    """The tool scopes SAM granted for the task's user; anything unreadable grants none."""
    if not isinstance(raw, list):
        return frozenset()
    return frozenset(scope for scope in raw if isinstance(scope, str) and scope)


def input_file(entry: Any) -> InputFile | None:
    if not isinstance(entry, Mapping):
        return None
    content = None
    encoded = entry.get("bytes")
    if isinstance(encoded, str) and encoded:
        try:
            content = base64.b64decode(encoded, validate=True)
        except (binascii.Error, ValueError):
            log.warning("a task file's bytes are not base64; keeping its uri only")
    uri = _str(entry.get("uri")) or None
    if content is None and uri is None:
        return None
    return InputFile(name=_str(entry.get("name")), mime_type=_str(entry.get("mimeType")), content=content, uri=uri)


MAX_TOKENS_PER_MODEL_CALL = 10_000_000
"""The most tokens of one kind (input, output, cached, reasoning, cache writes) the entrypoint takes for one model call.
It refuses a frame over it with the task's id, which the agent could not tell from a refusal of the task's result, so
each count is cut to it."""
MAX_TOKENS_PER_TASK = 100_000_000
"""The most input and output tokens the entrypoint totals for one task; usage past it is not sent, for the same reason."""


def usage_object(usage: Usage) -> dict[str, int]:
    """A model call's usage in SAM's convention, from ``Usage.buckets`` (so SAM derives the same four buckets its own
    agents' spend events carry): ``input`` is every input token, cached ones included; ``cachedInput`` the part
    read from or written to the cache; ``cacheWrite`` the written part of that.

    `cacheWrite` is left out when it is zero: the provider may not report cache writes separately, and `Usage` cannot
    tell that from none.
    """
    b = usage._buckets()

    def bounded(tokens: int) -> int:
        return min(max(tokens, 0), MAX_TOKENS_PER_MODEL_CALL)

    body = {
        "input": bounded(b.input),
        "output": bounded(b.output),
        "cachedInput": bounded(b.cached),
        "reasoning": bounded(usage.reasoning_tokens),
    }
    if b.cache_write > 0:
        body["cacheWrite"] = bounded(b.cache_write)
    return body


def prompt_problem(prompt: Prompt, limits: Limits) -> str:
    """Why the entrypoint would refuse the prompt, checked before sending it (a refusal of a prompt whose requestId
    is unusable could not name it)."""
    rid = prompt.request_id
    if (
        not rid
        or len(rid.encode("utf-8")) > limits.max_id_bytes
        or any(c in rid for c in "/*>")
        or any(c.isspace() for c in rid)
    ):
        return "the request id is not a short identifier"
    if (prompt.approval is None) == (not prompt.questions):
        return "a prompt carries exactly one of an approval or questions"
    if prompt.approval is not None and not prompt.approval.tool:
        return "an approval names its tool"
    if any(not q.question for q in prompt.questions):
        return "every question needs its text"
    return ""


MIN_APPROVAL_STRING_BYTES = 256
"""Below this, cutting an approval's argument strings further would hide what the user is asked to approve."""
PROMPT_FRAME_VALUES = 64
"""An approval's arguments leave this many of the frame's JSON values to the rest of the prompt frame."""


def prompt_overflow(prompt: Prompt, limits: Limits) -> str:
    """Why the link cannot carry the prompt as asked (more questions, or options per question, than the entrypoint
    takes; approval arguments too many or too large to show), or "". The asking code gets this as PromptTooLarge
    rather than a prompt cut short, since a question or argument left out would change what the user answers."""
    if len(prompt.questions) > limits.max_prompt_questions:
        return (
            f"the agent link asks at most {limits.max_prompt_questions} questions in one prompt; this one asks "
            f"{len(prompt.questions)}"
        )
    for i, q in enumerate(prompt.questions):
        if len(q.options) > limits.max_prompt_options:
            return (
                f"the agent link offers at most {limits.max_prompt_options} options per question; question {i + 1} "
                f"offers {len(q.options)}"
            )
        shown = [fit_prompt_text(o.label, limits) for o in q.options]
        if len(set(shown)) < len(shown) or len({redact_string(label) for label in shown}) < len(shown):
            return (
                f"question {i + 1} offers options whose labels read the same as the user sees them, so the "
                "answer could not say which was chosen"
            )
    if prompt.approval is not None:
        _, problem = approval_args(prompt.approval, limits)
        return problem
    return ""


def approval_args(approval: Any, limits: Limits) -> tuple[Any, str]:
    """An approval's arguments as the link carries them, and why it cannot, if so. Strings are cut with SAM's
    truncation marker, as the approval screen cuts them anyway, down to MIN_APPROVAL_STRING_BYTES; the arguments
    themselves are never left out."""
    args = json_value(dict(approval.args))
    if len(args) > limits.max_approval_args:
        return args, (
            f"the agent link shows at most {limits.max_approval_args} approval arguments; this approval has {len(args)}"
        )
    cap = limits.max_string_bytes
    while len(encode(args)) > limits.max_approval_args_bytes and cap >= MIN_APPROVAL_STRING_BYTES:
        args = cap_strings(json_value(dict(approval.args)), cap)
        cap //= 2
    if len(encode(args)) > limits.max_approval_args_bytes:
        return args, (
            f"the approval's arguments are larger than the agent link shows ({limits.max_approval_args_bytes} bytes)"
        )
    if json_values(args) > limits.max_frame_values - PROMPT_FRAME_VALUES:
        return args, (
            f"the approval's arguments hold more JSON values than the agent link carries ({limits.max_frame_values})"
        )
    return args, ""


def fit_prompt_text(text: str, limits: Limits) -> str:
    """A question's text or an option's label or description, cut to the entrypoint's limit with SAM's truncation
    marker: they are display text, so cutting one changes only what is shown."""
    return cap_text(text, limits.max_prompt_text_bytes)


def fit_header(header: str, limits: Limits) -> str:
    """A question's header (a short tab label), cut to the characters the entrypoint takes."""
    return header[: limits.max_question_header_chars]


def prompt_frame(task_id: str, prompt: Prompt, limits: Limits) -> dict[str, Any]:
    """The prompt frame, within the limits: display texts cut, approval arguments fitted (``prompt_overflow`` says
    first whether the prompt fits at all)."""
    frame: dict[str, Any] = {
        "type": "prompt",
        "taskId": task_id,
        "requestId": prompt.request_id,
        "timeoutSeconds": prompt.timeout_seconds,
    }
    if prompt.approval is not None:
        a = prompt.approval
        args, _ = approval_args(a, limits)
        frame["approval"] = {"tool": fit_name(a.tool, limits.max_name_bytes), "args": args, "message": a.message}
        return frame
    questions = []
    for q in prompt.questions:
        asked: dict[str, Any] = {
            "question": fit_prompt_text(q.question, limits),
            "header": fit_header(q.header, limits),
            "options": [
                {"label": fit_prompt_text(o.label, limits), "description": fit_prompt_text(o.description, limits)}
                for o in q.options
            ],
        }
        if q.multi_select:
            asked["multiSelect"] = True
        questions.append(asked)
    frame["questions"] = questions
    if prompt.message:
        frame["message"] = prompt.message
    return frame


def _choices(question: Any, limits: Limits) -> list[tuple[tuple[str, ...], str]]:
    """Per option, what an answer may say for it (the label as sent, and as SAM's redactor shows it), and what the
    handler gets back for it: the option's value, else the label it offered."""
    choices = []
    for o in question.options:
        sent = fit_prompt_text(o.label, limits)
        choices.append((tuple(dict.fromkeys((sent, redact_string(sent)))), o.value or o.label))
    return choices


def _read_choice(answer: str, question: Any, limits: Limits) -> str:
    choices = _choices(question, limits)
    for said, value in choices:
        if answer in said:
            return value
    if question.multi_select and answer:
        picked = _split_choices(answer, [said for said, _ in choices], limits.max_prompt_options)
        if picked is not None:
            return JOIN.join(choices[i][1] for i in picked)
    return answer


MAX_CHOICE_READ_STEPS = 10_000
"""The most label comparisons reading one multi-select answer may take. An answer of offered labels is read in far fewer
(each option is named at most once); past it the answer is taken as typed, an "Other" text.
"""


def _split_choices(answer: str, options: Sequence[tuple[str, ...]], max_parts: int) -> list[int] | None:
    """A multi-select answer (the chosen labels joined with ", ") read back into the options it names, a label that
    itself holds ", " included: each option at most once, at most ``max_parts`` of them, the longest label that fits
    at each point tried first. None when the answer is not made of offered labels (an "Other" text).

    Iterative, and each (position, options named) state is tried once, under MAX_CHOICE_READ_STEPS: labels that
    compose with ", " ("Paris", "France", "Paris, France") cannot make an unreadable answer cost more than that."""
    longest = sum(max(map(len, said)) for said in options) + len(JOIN) * (len(options) - 1)
    if not answer or len(answer) > longest:
        return None
    tries = sorted(((label, i) for i, said in enumerate(options) for label in said if label), key=lambda t: len(t[0]))
    stack: list[tuple[int, int, tuple[int, ...]]] = [(0, 0, ())]
    seen: set[tuple[int, int]] = set()
    steps = 0
    while stack:
        at, named_set, named = stack.pop()
        if (at, named_set) in seen or len(named) >= max_parts:
            continue
        seen.add((at, named_set))
        for label, i in tries:
            steps += 1
            if steps > MAX_CHOICE_READ_STEPS:
                return None
            end = at + len(label)
            if named_set >> i & 1 or not answer.startswith(label, at):
                continue
            if end == len(answer):
                return [*named, i]
            if answer.startswith(JOIN, end):
                stack.append((end + len(JOIN), named_set | 1 << i, (*named, i)))
    return None


def prompt_answer(
    prompt: Prompt, request_id: str, frame: Mapping[str, Any], limits: Limits | None = None
) -> PromptAnswer:
    """A prompt's answer from the entrypoint's answer frame.

    A status this SDK does not know ends the prompt as cancelled, since nothing the user approved or answered came back.
    The entrypoint's form answers with a choice's label as it showed it (cut, redacted), so the handler gets the
    option's value, or the label it offered.
    """
    limits = limits or Limits()
    status = frame.get("status")
    status = PROMPT_STATUSES.get(status, CANCELLED) if isinstance(status, str) else CANCELLED
    if prompt.approval is not None:
        approved = frame.get("approved")
        return PromptAnswer(request_id, status, approved=approved if isinstance(approved, bool) else None)
    if status != ANSWERED:
        return PromptAnswer(request_id, status)
    raw = frame.get("answers")
    answers = [a if isinstance(a, str) else "" for a in raw] if isinstance(raw, list) else []
    answers += [""] * (len(prompt.questions) - len(answers))
    read = tuple(
        _read_choice(a, prompt.questions[i], limits) if i < len(prompt.questions) else a for i, a in enumerate(answers)
    )
    return PromptAnswer(request_id, status, answers=read, truncated=frame.get("truncated") is True)


def result_frame(task_id: str, outcome: TaskOutcome, limits: Limits) -> dict[str, Any]:
    """The result frame for a task's outcome.

    A failed result carries an error, which is what SAM shows rather than the text: a failure the SDK words itself (a
    limit reached, an agent stopped) would otherwise reach the user as "internal error". It is reported as SAM's own
    agents report a failed handler (a JSON-RPC error), and the message is for the user.
    """
    state = "canceled" if outcome.answer_to_cancel else outcome.state
    if state not in RESULT_STATES:
        state = "failed"
    frame: dict[str, Any] = {"type": "result", "taskId": task_id, "state": state, "text": outcome.text}
    error = outcome.error
    if error is None and state == "failed" and outcome.text:
        error = TaskError(message=outcome.text)
    if error is not None and not outcome.answer_to_cancel:
        frame["state"] = "failed"
        if error.category:
            frame["error"] = {"category": error.category, "provider": fit_name(error.provider, limits.max_name_bytes)}
        else:
            message = redact_string(error.message or GENERIC_FAILURE_TEXT)
            frame["error"] = {"message": cap_text(message, limits.max_failure_message_bytes)}
    if frame["state"] == "canceled":
        return frame
    files = [f for f in outcome.files if f.content]
    if files:
        frame["files"] = [
            {"name": f.name, "mimeType": f.mime_type, "bytes": base64.b64encode(f.content).decode()} for f in files
        ]
    empty = [f.name or "unnamed" for f in outcome.files if not f.content]
    if empty:
        log.warning("empty files left out of the result task=%s count=%d", task_id, len(empty))
        note = f"Note: {len(empty)} file(s) were empty and were not included: {', '.join(empty)}."
        frame["text"] = f"{frame['text']}\n\n{note}" if frame["text"] else note
    if outcome.data is not None:
        data = json_value(outcome.data)
        problem = refused_data(data)
        if problem:
            log.warning("structured output left out of the result: %s task=%s", problem, task_id)
        else:
            frame["data"] = data
    return frame


UI_ACTED_TYPES = frozenset(
    {
        "tool_invocation_start",
        "tool_result",
        "llm_invocation",
        "llm_response",
        "agent_progress_update",
        "artifact_creation_progress",
        "artifact_saved",
        "template_block",
        "structured_invocation_request",
        "structured_invocation_result",
        "workflow_execution_start",
        "workflow_node_execution_start",
        "workflow_node_execution_result",
        "workflow_map_progress",
        "workflow_execution_result",
        "deep_research_progress",
        "rag_info_update",
        "deep_research_report",
        "deep_research_plan",
        "deep_research_plan_stale",
        "authentication_required",
        "authentication_result",
        "auth_response",
        "user_input_request",
        "user_input_response",
        "thinking_content",
        "builder_component_progress",
        "gateway_timeout_warning",
        "compaction_notification",
        "redirect",
        "request_build_activate",
    }
)
"""The ``type`` values SAM acts on (the entrypoint's uiActedTypes): a result whose data holds an object of one of these
types, at any depth, is refused."""
TASK_RECORD_KEYS = ("node_id", "node_type", "workflow_name", "trace_id", "retry_group_id", "retry_attempt")
"""The keys of a ``metadata`` object SAM reads as the task's own (the entrypoint's taskRecordKeys): a result whose data
holds such an object, at any depth, is refused."""


def refused_data(value: Any) -> str:
    """Why the entrypoint would refuse a result carrying this data (resultParts), or "". The SDK has acknowledged the
    task's delivery by then, so a refused result would leave the task unanswered: the data is left out instead."""
    stack = [value]
    while stack:
        item = stack.pop()
        if isinstance(item, Mapping):
            kind = item.get("type")
            if isinstance(kind, str) and kind in UI_ACTED_TYPES:
                return f"it holds an object of type {kind!r}, which SAM acts on"
            metadata = item.get("metadata")
            if isinstance(metadata, Mapping):
                key = next((k for k in TASK_RECORD_KEYS if k in metadata), "")
                if key:
                    return f"it holds a metadata object naming {key}, which SAM records for the task"
            stack.extend(item.values())
        elif isinstance(item, list):
            stack.extend(item)
    return ""


def peer_info(entry: Any) -> PeerInfo | None:
    if not isinstance(entry, Mapping) or not _str(entry.get("name")):
        return None
    skills = tuple(
        PeerSkill(_str(s.get("id")), _str(s.get("name")), _str(s.get("description")))
        for s in entry.get("skills") or []
        if isinstance(s, Mapping)
    )
    return PeerInfo(
        name=_str(entry.get("name")),
        display_name=_str(entry.get("displayName")),
        description=_str(entry.get("description")),
        skills=skills,
        on_demand=bool(entry.get("onDemand")),
    )


def peer_result(peer: str, frame: Mapping[str, Any]) -> PeerResult:
    """A peer_result as SAM's own agents read a peer's answer: a peer's JSON-RPC error is an "error" result that
    names the peer and the code."""
    state = _str(frame["state"]) if frame.get("state") in PEER_STATES else "failed"
    text = _str(frame.get("text"))
    error = frame.get("error")
    truncated = frame.get("truncated") is True
    if isinstance(error, Mapping):
        return PeerResult(
            peer,
            "error",
            f'peer agent "{peer}": {_str(error.get("message"))} (code {_int(error.get("code"))})',
            truncated=truncated,
            sam_decision=PEER_CALL_GRANTED,
        )
    return PeerResult(peer, state, text, data=frame.get("data"), truncated=truncated, sam_decision=PEER_CALL_GRANTED)


def json_value(value: Any) -> Any:
    """The value as strict JSON carries it: anything JSON cannot hold becomes its string, a float that is not finite
    (NaN, an infinity) becomes null and a lone surrogate (a file name read with ``surrogateescape``) becomes U+FFFD.
    The entrypoint's decoder refuses a frame holding either, and that refusal cannot name the frame."""
    try:
        loaded = json.loads(json.dumps(value, ensure_ascii=False, default=str))
    except (TypeError, ValueError):
        loaded = str(value)
    return _strict(loaded)


_SURROGATE = re.compile("[\ud800-\udfff]")


def _strict(value: Any) -> Any:
    if isinstance(value, str):
        return _SURROGATE.sub("\ufffd", value)
    if isinstance(value, float):
        return value if math.isfinite(value) else None
    if isinstance(value, dict):
        return {_strict(k): _strict(v) for k, v in value.items()}
    if isinstance(value, list):
        return [_strict(v) for v in value]
    return value


def cap_strings(value: Any, max_bytes: int) -> Any:
    """Every string in the value cut to max_bytes, as the entrypoint itself cuts tool arguments and results."""
    if isinstance(value, str):
        return cap_text(value, max_bytes)
    if isinstance(value, Mapping):
        return {k: cap_strings(v, max_bytes) for k, v in value.items()}
    if isinstance(value, list):
        return [cap_strings(v, max_bytes) for v in value]
    return value


def json_values(value: Any) -> int:
    """The JSON values a value holds as the entrypoint counts them: every object, array, string (object keys
    included), number, true, false and null."""
    if isinstance(value, Mapping):
        return 1 + sum(1 + json_values(v) for v in value.values())
    if isinstance(value, (list, tuple)):
        return 1 + sum(json_values(v) for v in value)
    return 1


EVENT_FRAME_VALUES = 64
"""An event frame's own values besides its tool arguments and results (type, taskId, kind, callId, name, usage, ...)."""
TRUNCATED_KEY = "_truncated"
MIN_TOOL_STRING_BYTES = 256


def _tool_parts(body: Mapping[str, Any]) -> list[tuple[str, int | None]]:
    """Where an event carries tool arguments and results: ``args``, ``result``, each ``toolCalls[i].args``."""
    parts: list[tuple[str, int | None]] = [(k, None) for k in ("args", "result") if body.get(k) is not None]
    for i, call in enumerate(body.get("toolCalls") or []):
        if isinstance(call, Mapping) and call.get("args") is not None:
            parts.append(("args", i))
    return parts


def _get(body: Mapping[str, Any], part: tuple[str, int | None]) -> Any:
    key, i = part
    return body[key] if i is None else body["toolCalls"][i][key]


def _with(body: dict[str, Any], part: tuple[str, int | None], value: Any) -> None:
    key, i = part
    if i is None:
        body[key] = value
    else:
        body["toolCalls"][i] = {**body["toolCalls"][i], key: value}


def tool_payload_bytes(body: Mapping[str, Any]) -> int:
    """An event's tool arguments, result and tool-call arguments together, as sent (the entrypoint's
    maxToolPayloadBytes)."""
    return sum(len(encode(_get(body, part))) for part in _tool_parts(body))


def fit_tool_payload(body: Mapping[str, Any], limits: Limits) -> dict[str, Any]:
    """An event whose tool arguments and results fit ``maxToolPayloadBytes`` and ``maxFrameValues``, so the step
    is still reported: first every string is cut shorter with SAM's truncation marker; failing that (many small
    values), each argument object becomes ``{"_truncated": <its JSON text, cut>}`` and each result its JSON text,
    cut.

    Each part's JSON text, escaped once more inside a string, takes at most twice its length plus the marker, which is
    what the cut is sized for.
    """
    out = dict(body)
    if out.get("toolCalls"):
        out["toolCalls"] = list(out["toolCalls"])
    parts = _tool_parts(out)
    values_budget = limits.max_frame_values - EVENT_FRAME_VALUES

    def fits(candidate: Mapping[str, Any]) -> bool:
        return (
            tool_payload_bytes(candidate) <= limits.max_tool_payload_bytes
            and sum(json_values(_get(candidate, p)) for p in parts) <= values_budget
        )

    if not parts or fits(out):
        return out
    cap = limits.max_string_bytes
    while cap >= MIN_TOOL_STRING_BYTES:
        candidate = dict(out)
        if candidate.get("toolCalls"):
            candidate["toolCalls"] = list(candidate["toolCalls"])
        for part in parts:
            _with(candidate, part, cap_strings(_get(out, part), cap))
        if fits(candidate):
            log.warning(
                "tool arguments and results cut to fit the agent link kind=%s string_bytes=%d", body.get("kind"), cap
            )
            return candidate
        cap //= 2
    share = max(limits.max_tool_payload_bytes // len(parts) // 2 - 64, 64)
    for part in parts:
        text = cap_text(json.dumps(_get(out, part), ensure_ascii=False, separators=(",", ":")), share)
        _with(out, part, {TRUNCATED_KEY: text} if part[0] == "args" else text)
    log.warning(
        "tool arguments and results replaced by their cut JSON text to fit the agent link kind=%s", body.get("kind")
    )
    return out


def json_text(frame: Any) -> str:
    """A frame's JSON text, as the entrypoint decodes it: a lone surrogate left in a string the SDK did not build
    with ``json_value`` (an answer's text, a file's name) becomes U+FFFD, since UTF-8 cannot carry it. A float that is
    not finite raises ValueError rather than going out as bare NaN, which the entrypoint would refuse without naming
    the frame."""
    text = json.dumps(frame, ensure_ascii=False, separators=(",", ":"), allow_nan=False)
    try:
        text.encode("utf-8")
    except UnicodeEncodeError:
        return _SURROGATE.sub("\ufffd", text)
    return text


def encode(frame: Any) -> bytes:
    text = json_text(frame)
    try:
        return text.encode("utf-8")
    except UnicodeEncodeError:
        return well_formed(text).encode("utf-8")


def fits(frame: Mapping[str, Any], limits: Limits) -> bool:
    """Within the welcome's maxFrameBytes and maxFrameValues, which the entrypoint refuses a frame over."""
    return len(encode(frame)) <= limits.max_frame_bytes and json_values(frame) <= limits.max_frame_values


def fit_result(frame: dict[str, Any], limits: Limits) -> dict[str, Any]:
    """A result the link can carry, keeping as much of it as fits. Over ``maxFrameValues``, the structured data
    goes first (it holds the values; a file holds four). Over ``maxFrameBytes``, the largest files go one by
    one, then the data, then the text is cut. The text names the files left out; failing all that, a failed
    answer that says so."""
    if fits(frame, limits):
        return frame
    task_id = frame["taskId"]
    if json_values(frame) > limits.max_frame_values and frame.pop("data", None) is not None:
        log.warning("structured output left out of the result: too many values task=%s", task_id)
    files = list(frame.get("files") or [])
    left_out: list[str] = []
    while files and not fits(frame, limits):
        largest = max(files, key=lambda f: len(f.get("bytes") or ""))
        files.remove(largest)
        left_out.append(largest.get("name") or "unnamed")
        if files:
            frame["files"] = files
        else:
            frame.pop("files", None)
    if left_out:
        log.warning("files left out of the result task=%s count=%d", task_id, len(left_out))
        frame["text"] = (frame.get("text") or "") + (
            f"\n\nNote: {len(left_out)} file(s) were too large to return over the agent link and were not "
            f"included: {', '.join(left_out)}."
        )
    if not fits(frame, limits) and frame.pop("data", None) is not None:
        log.warning("structured output left out of the result task=%s", task_id)
    if fits(frame, limits):
        return frame
    frame["text"] = cap_text(frame.get("text") or "", max(limits.max_frame_bytes - FRAME_OVERHEAD_BYTES, 0))
    if fits(frame, limits):
        return frame
    log.error("result larger than the agent link carries task=%s", task_id)
    return {"type": "result", "taskId": task_id, "state": "failed", "text": TOO_LARGE_TEXT}


def positive_int(value: Any) -> bool:
    return isinstance(value, int) and not isinstance(value, bool) and value > 0


def is_delivery(value: Any) -> TypeGuard[int]:
    return positive_int(value)


def _str(value: Any) -> str:
    return value if isinstance(value, str) else ""


def _int(value: Any) -> int:
    return value if isinstance(value, int) and not isinstance(value, bool) else 0
