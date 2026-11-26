# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Every agent the contract scenarios use. SAM's link configuration is fixed when SAM boots, so the roster is too."""

from __future__ import annotations

REGISTERED_OWNER = "bob@example.com"
REGISTERED_TEAM = "Contract Tests"

ROLES: dict[str, tuple[str, ...]] = {
    "probe": (),
    "listing": (),
    "steps": (),
    "stream": (),
    "cancel": (),
    "approval": (),
    "caller": ("callee",),
    "callee": (),
    "guarded": ("callee",),
    "outsider": (),
    "stop": (),
    "stop-caller": ("stop",),
    "token": (),
    "fails": (),
    "spend": (),
    "redact": (),
    "questions": (),
    "multiselect": (),
    "toomany": (),
    "workflow": (),
    "raw": (),
    "reconnect": (),
    "relist": (),
    "replica": (),
    "scoped": (),
    "scopes-redescribed": (),
    "held-cancel": (),
    "queued": (),
    "files": (),
    "card": ("callee",),
    "gated": (),
    "cache": (),
    "refused-shapes": (),
    "strict-json": (),
    "usage-models": (),
}
"""By role (agent ct-<role>). Value: the roles this role may call as peers (the entrypoint's per-agent allowlist)."""


def agent_name(role: str) -> str:
    return f"ct-{role}"
