# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""What the gateway tells an agent about the request beyond the user's words.

SAM's own agents put these into their system prompt: the assistant's purpose and desired response format that the
gateway is configured with, the user's profile and timezone, and a per-turn conversation context such as the Slack
thread the message was posted in. An SDK agent gets the same values on ``ctx.request_context`` and can render them as
the same ``## Section`` blocks with ``context_prompt()``.
"""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class RequestContext:
    system_purpose: str = ""
    response_format: str = ""
    project_id: str = ""
    conversation_context: str = ""
    """Foreign text the gateway has already fenced as untrusted (for example a Slack thread). SAM-Go injects it verbatim
    into the system prompt each turn and never stores it in the conversation history.
    """
    user_profile: str = ""
    user_timezone: str = ""

    def context_prompt(self) -> str:
        """The non-empty sections as SAM-Go renders them: ``## <label>`` blocks separated by blank lines.
        Empty when the gateway sent none of them."""
        sections = (
            ("System Purpose", self.system_purpose),
            ("Desired Response Format", self.response_format),
            ("Inquiring User Profile", self.user_profile),
            ("Conversation Context", self.conversation_context),
        )
        return "\n\n".join(f"## {label}\n\n{content}" for label, content in sections if content)
