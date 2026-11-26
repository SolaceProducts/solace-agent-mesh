# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Wire-format parity tests for ConfigSchemaField — Python vs. Go SDK.

The platform's `tool_package_files.config_schema` storage is parsed by a
single Go struct (`configSchemaField` in toolset_service.go) that keys off
`"key"` and reads `type/description/required/secret/default/options`. For
mixed-language fleets to render uniform forms, the JSON emitted by the
Python SDK must match the Go SDK's `omitempty` shape byte-for-byte.

These tests pin the on-disk shape to what the Go SDK produces for the
equivalent declaration in pkg/samtoolsdk/tool.go.
"""

from __future__ import annotations

import sys
from pathlib import Path

_HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(_HERE.parent / "sam_tool_sdk"))

from config_schema import ConfigSchemaField, with_config_schema  # noqa: E402

def test_zero_value_fields_omitted() -> None:
    f = ConfigSchemaField(key="api_url", type="string")
    assert f.to_dict() == {"key": "api_url", "type": "string"}

def test_full_field_round_trip() -> None:
    f = ConfigSchemaField(
        key="slack_bot_token",
        type="string",
        description="Slack Bot OAuth token (xoxb-...).",
        required=True,
        secret=True,
    )
    assert f.to_dict() == {
        "key": "slack_bot_token",
        "type": "string",
        "description": "Slack Bot OAuth token (xoxb-...).",
        "required": True,
        "secret": True,
    }

def test_default_zero_string_is_emitted() -> None:
    f = ConfigSchemaField(key="prefix", type="string", default="")
    assert f.to_dict() == {"key": "prefix", "type": "string", "default": ""}

def test_options_emitted_when_non_empty() -> None:
    f = ConfigSchemaField(key="region", type="string", options=["us", "eu"])
    assert f.to_dict() == {
        "key": "region",
        "type": "string",
        "options": ["us", "eu"],
    }

def test_with_config_schema_attaches_serialized_list() -> None:
    @with_config_schema(
        [
            ConfigSchemaField(
                key="slack_bot_token",
                description="x",
                required=True,
                secret=True,
            ),
            ConfigSchemaField(key="default_channel"),
        ]
    )
    async def post_message():
        pass

    assert post_message._config_schema == [
        {
            "key": "slack_bot_token",
            "type": "string",
            "description": "x",
            "required": True,
            "secret": True,
        },
        {"key": "default_channel", "type": "string"},
    ]

def test_with_config_schema_accepts_raw_dicts() -> None:
    raw = {"key": "legacy_field", "type": "string", "required": True}

    @with_config_schema([raw])
    async def legacy():
        pass

    assert legacy._config_schema == [raw]

def test_with_config_schema_rejects_unknown_type() -> None:
    try:

        @with_config_schema([42])  # type: ignore[list-item]
        async def bad():
            pass

    except TypeError as e:
        assert "ConfigSchemaField" in str(e)
        return
    raise AssertionError("expected TypeError")

def test_with_config_schema_rejects_reserved_auth_key() -> None:
    try:

        @with_config_schema(
            [ConfigSchemaField(key="auth", type="string", required=True)]
        )
        async def shadow_auth():
            pass

    except ValueError as e:
        assert "auth" in str(e)
        return
    raise AssertionError("expected ValueError for reserved 'auth' key")
