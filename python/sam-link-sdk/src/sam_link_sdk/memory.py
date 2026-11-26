# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Per-conversation memory for SDK agents, keyed by the SAM session (the user and the contextId).

SAM's own agents keep a conversation's history for as long as the chat lives, so a follow-up question sees the
earlier turns. An SDK agent is stateless unless it keeps history itself. This is a small bounded store for that: it
holds the completed turns of each conversation, drops the oldest turns past ``max_turns``, drops conversations idle
for ``ttl_seconds``, and drops the least recently used conversation past ``max_conversations``.

The store lives in this process. With several replicas a follow-up can land on a replica that has not seen the
conversation, and a restart forgets everything; use a shared store (anything with the same four methods) if that
matters. History holds the user's words and the model's answers, so it is never logged.

Key it by ``ctx.session_id``: that is empty for a run-based task (a peer call or a workflow node), which then
neither reads nor adds history, as on SAM-Go agents.
"""

from __future__ import annotations

import threading
import time
from collections import OrderedDict, deque
from collections.abc import Callable
from dataclasses import dataclass, field
from typing import Any


@dataclass
class Turn:
    items: list[Any]
    user_text: str = ""
    answer_text: str = ""


@dataclass
class _Conversation:
    turns: deque[Turn] = field(default_factory=deque)
    touched: float = 0.0


class ConversationMemory:
    def __init__(self, *, max_turns: int = 20, max_conversations: int = 1000, ttl_seconds: float = 3600.0):
        self.max_turns = max_turns
        self.max_conversations = max_conversations
        self.ttl_seconds = ttl_seconds
        self._clock: Callable[[], float] = time.monotonic
        self._conversations: OrderedDict[str, _Conversation] = OrderedDict()
        self._lock = threading.Lock()
        """Framework adapters call from worker threads as well as the event loop."""

    def _live(self, session_id: str) -> _Conversation | None:
        conversation = self._conversations.get(session_id)
        if conversation is None:
            return None
        if self.ttl_seconds > 0 and self._clock() - conversation.touched > self.ttl_seconds:
            del self._conversations[session_id]
            return None
        return conversation

    def history(self, session_id: str) -> list[Any]:
        """The stored items of the conversation's turns, oldest first. Empty for a new or expired conversation."""
        with self._lock:
            conversation = self._live(session_id) if session_id else None
            if conversation is None:
                return []
            return [item for turn in conversation.turns for item in turn.items]

    def remember(self, session_id: str, items: list[Any], user_text: str = "", answer_text: str = "") -> None:
        """Add a completed turn. Remember a turn only once it has finished: tool calls and their results must
        stay paired, so a turn cut short by a failure is not stored."""
        with self._lock:
            if not session_id:
                return
            conversation = self._live(session_id)
            if conversation is None:
                conversation = self._conversations[session_id] = _Conversation()
            conversation.turns.append(Turn(list(items), user_text, answer_text))
            while self.max_turns > 0 and len(conversation.turns) > self.max_turns:
                conversation.turns.popleft()
            conversation.touched = self._clock()
            self._conversations.move_to_end(session_id)
            while self.max_conversations > 0 and len(self._conversations) > self.max_conversations:
                self._conversations.popitem(last=False)

    def transcript(self, session_id: str) -> str:
        """The conversation as plain text (``User: ...`` / ``Assistant: ...``), for frameworks that take a text
        prompt rather than a message list."""
        with self._lock:
            conversation = self._live(session_id) if session_id else None
            if conversation is None:
                return ""
            lines: list[str] = []
            for turn in conversation.turns:
                if turn.user_text:
                    lines.append(f"User: {turn.user_text}")
                if turn.answer_text:
                    lines.append(f"Assistant: {turn.answer_text}")
            return "\n".join(lines)

    def forget(self, session_id: str) -> None:
        with self._lock:
            self._conversations.pop(session_id, None)

    def __len__(self) -> int:
        with self._lock:
            return len(self._conversations)
