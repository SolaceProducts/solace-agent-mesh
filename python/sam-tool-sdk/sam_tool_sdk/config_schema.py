# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""
Config schema declarations for Python SAM tools.

Mirrors the Go SDK's `pkg/samtoolsdk.ConfigSchemaField` so the platform
parses the same `--schema` JSON shape regardless of which language the tool
is written in. The platform reads these entries to render the toolset
config form, validate per-agent overrides, and enforce required fields at
deploy time.

Usage on a function-based tool::

    from sam_tool_sdk import ConfigSchemaField, with_config_schema

    @with_config_schema([
        ConfigSchemaField(
            key="slack_bot_token",
            type="string",
            description="Slack Bot OAuth token (xoxb-...).",
            required=True,
            secret=True,
        ),
        ConfigSchemaField(key="default_channel", type="string"),
    ])
    async def post_message(tool_config, channel: str, text: str):
        token = tool_config["slack_bot_token"]
        ...

Usage on a DynamicTool subclass (override the property)::

    class MyTool(DynamicTool):
        @property
        def config_schema(self):
            return [
                ConfigSchemaField(key="api_key", type="string", secret=True, required=True).to_dict(),
            ]

The `to_dict()` serializer drops zero-value fields the same way the Go
encoder's `omitempty` does, so the wire format is byte-identical for
equivalent declarations.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Callable, Dict, List

RESERVED_AUTH_CONFIG_KEY = "auth"

@dataclass
class ConfigSchemaField:
    """A single operator-supplied config parameter.

    Fields:
        key:         storage / wire identifier (snake_case)
        type:        "string", "integer", or "boolean"
        description: shown in the toolset config form
        required:    operator must supply a value (deploy fails otherwise);
                     a secret field with no default is implicitly required
                     server-side, but set this flag explicitly to keep
                     intent visible
        secret:      treat as a secret — redacted in API responses and
                     rendered with a password input in the UI
        default:     schema default; used at deploy time when neither the
                     toolset layer nor the agent layer set a value
        options:     enum of allowed values for the UI to render as a
                     select; freeform values are still accepted unless the
                     tool itself validates them
    """

    key: str
    type: str = "string"
    description: str = ""
    required: bool = False
    secret: bool = False
    default: Any = None
    options: List[Any] = field(default_factory=list)

    def to_dict(self) -> Dict[str, Any]:
        """Serialize to the wire shape the platform expects.

        Drops zero-value fields so the JSON matches the Go SDK's
        `omitempty` behaviour byte-for-byte for equivalent declarations.
        """
        out: Dict[str, Any] = {"key": self.key, "type": self.type}
        if self.description:
            out["description"] = self.description
        if self.required:
            out["required"] = True
        if self.secret:
            out["secret"] = True
        if self.default is not None:
            out["default"] = self.default
        if self.options:
            out["options"] = self.options
        return out

def with_config_schema(
    schema: List[ConfigSchemaField],
) -> Callable[[Callable], Callable]:
    """Attach a config schema to a function-based tool.

    The CLI's `--schema` output reads `func._config_schema` and emits each
    entry as a dict. Accepts either `ConfigSchemaField` instances or raw
    dicts (the latter for backwards compat with tools that hand-roll the
    shape) — instances are converted to dicts before storage so the CLI
    sees a uniform list.
    """

    serialized: List[Dict[str, Any]] = []
    for entry in schema:
        if isinstance(entry, ConfigSchemaField):
            serialized.append(entry.to_dict())
        elif isinstance(entry, dict):
            serialized.append(entry)
        else:
            raise TypeError(
                f"config_schema entry must be ConfigSchemaField or dict, got {type(entry).__name__}"
            )
    _assert_no_reserved_keys(serialized)

    def decorator(func: Callable) -> Callable:
        func._config_schema = serialized  # type: ignore[attr-defined]
        return func

    return decorator

def _assert_no_reserved_keys(fields: List[Dict[str, Any]]) -> None:
    """Raise ``ValueError`` if any field key collides with the reserved set.

    Currently the only reserved key is :data:`RESERVED_AUTH_CONFIG_KEY`,
    but kept as a list helper so future reservations (mirroring Go) can be
    added in one place.
    """
    for entry in fields:
        key = entry.get("key")
        if key == RESERVED_AUTH_CONFIG_KEY:
            raise ValueError(
                f"config_schema key {RESERVED_AUTH_CONFIG_KEY!r} is reserved "
                "for deployer OAuth credentials; pick a different name "
                "(e.g. 'auth_token', 'api_key')"
            )
