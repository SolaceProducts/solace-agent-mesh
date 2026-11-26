# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Regression tests for serialize_result — wire-format parity with Go SDK.

Before the fix, serialize_result called model_dump() which skipped the
pydantic to_serializable() override. Python tool results would then:
  1. Not carry the _schema / _schema_version markers that the STR's
     schema-marked parse path keys on.
  2. Drop binary data_objects as non-JSON-serializable bytes (no base64
     encoding, no is_binary flag).

Both the Go SDK (pkg/samtoolsdk/result.go:marshalWire) and the Python
ToolResult.to_serializable both emit {_schema, _schema_version, status,
message, data, data_objects, error_code}. After the fix, these tests
assert the Python path matches.
"""

from __future__ import annotations

from sam_tool_sdk.tool_result import (
    DataObject,
    ToolResult,
    TOOL_RESULT_SCHEMA_VERSION,
)
from sam_tool_sdk.tool_runner import serialize_result

def test_serialize_result_emits_schema_markers() -> None:
    result = ToolResult.ok(message="done")
    out = serialize_result(result)

    assert out["_schema"] == "ToolResult"
    assert out["_schema_version"] == TOOL_RESULT_SCHEMA_VERSION
    assert out["status"] == "success"
    assert out["message"] == "done"
    assert "data" not in out
    assert "data_objects" not in out
    assert out["error_code"] is None

def test_serialize_result_base64_encodes_binary_data_object() -> None:
    png_magic = bytes([0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A])
    obj = DataObject(
        name="pixel.png",
        mime_type="image/png",
        content=png_magic,
    )
    result = ToolResult.ok(message="ok", data_objects=[obj])
    out = serialize_result(result)

    assert len(out["data_objects"]) == 1
    do = out["data_objects"][0]
    assert do["name"] == "pixel.png"
    assert do["mime_type"] == "image/png"
    assert do["is_binary"] is True
    assert isinstance(do["content"], str)
    assert do["content"] != png_magic

def test_serialize_result_error_with_error_code() -> None:
    result = ToolResult.error(message="File not found", code="FILE_NOT_FOUND")
    out = serialize_result(result)

    assert out["_schema"] == "ToolResult"
    assert out["status"] == "error"
    assert out["error_code"] == "FILE_NOT_FOUND"
    assert out["message"] == "File not found"

def test_serialize_result_partial_preserves_status() -> None:
    result = ToolResult.partial(message="halfway", data={"progress": 50})
    out = serialize_result(result)

    assert out["_schema"] == "ToolResult"
    assert out["status"] == "partial"
    assert out["data"] == {"progress": 50}

def test_serialize_result_pending_carries_hil_metadata() -> None:
    result = ToolResult.pending(
        message="awaiting approval",
        data={"hil_request_id": "abc-123", "hil_source": "approval_workflow"},
    )
    out = serialize_result(result)

    assert out["_schema"] == "ToolResult"
    assert out["status"] == "pending"
    assert out["message"] == "awaiting approval"
    assert out["data"]["hil_request_id"] == "abc-123"
    assert out["data"]["hil_source"] == "approval_workflow"

def test_serialize_result_none_returns_empty_shape() -> None:
    out = serialize_result(None)
    assert out == {"text": None, "data_objects": []}

def test_serialize_result_plain_dict_unchanged() -> None:
    out = serialize_result({"custom": "value"})
    assert out == {"custom": "value"}

def test_serialize_result_string_wraps_as_text() -> None:
    out = serialize_result("hello")
    assert out == {"text": "hello", "data_objects": []}
