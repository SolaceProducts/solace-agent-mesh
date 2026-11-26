# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""
Defines the ToolResult abstraction for structured tool responses with automatic
artifact handling.

Tools can return either a raw dict (backward compatible) or a ToolResult for
enhanced handling where the framework automatically manages artifact storage
based on DataObject disposition.

This module also provides serialization/deserialization for ToolResult to support
remote execution where results must be transmitted as JSON.
"""

import base64
import logging
from dataclasses import dataclass, field
from enum import Enum
from typing import Any, Dict, List, Optional, Union

log = logging.getLogger(__name__)

TOOL_RESULT_SCHEMA_VERSION = "1.0"

class DataDisposition(str, Enum):
    """
    Defines how a DataObject should be handled by the framework.

    - AUTO: Framework decides based on content size and type
    - ARTIFACT: Always store as artifact, return reference to LLM
    - INLINE: Always return content inline (may be truncated if too large)
    - ARTIFACT_WITH_PREVIEW: Store as artifact and include preview for LLM
    """
    AUTO = "auto"
    ARTIFACT = "artifact"
    INLINE = "inline"
    ARTIFACT_WITH_PREVIEW = "artifact_with_preview"

@dataclass
class DataObject:
    """
    Represents a single piece of data produced by a tool that can be returned
    inline or stored as an artifact.

    The framework will process DataObjects based on their disposition, handling
    artifact storage, preview generation, and response formatting automatically.

    Examples::

        # Text content with auto disposition (framework decides):
        DataObject(
            name="analysis_results.json",
            content='{"key": "value"}',
            mime_type="application/json",
            description="Analysis results"
        )

        # Binary content requiring artifact storage:
        DataObject(
            name="generated_chart.png",
            content=chart_bytes,
            mime_type="image/png",
            disposition=DataDisposition.ARTIFACT,
            description="Generated chart"
        )

        # Large content with explicit preview:
        DataObject(
            name="large_dataset.csv",
            content=csv_string,
            mime_type="text/csv",
            disposition=DataDisposition.ARTIFACT_WITH_PREVIEW,
            preview="col1,col2,col3\\n1,2,3\\n4,5,6\\n...(1000 more rows)",
            description="Full dataset"
        )
    """
    name: str
    content: Union[str, bytes]
    mime_type: str = "text/plain"
    disposition: DataDisposition = DataDisposition.AUTO
    description: Optional[str] = None
    preview: Optional[str] = None
    metadata: Optional[Dict[str, Any]] = None

    def to_serializable(self) -> Dict[str, Any]:
        """
        Convert DataObject to a JSON-serializable dictionary.

        Bytes content is treated as text when it is valid UTF-8 (matching
        the Go SDK's ``utf8.Valid`` check in ``result.go``); only truly
        non-UTF-8 bytes are base64-encoded with ``is_binary=True``. Keeps
        the wire shape identical across Go and Python tools so the STR
        can branch on ``is_binary`` consistently.
        """
        content = self.content
        is_binary = False

        if isinstance(content, bytes):
            try:
                content = content.decode("utf-8")
            except UnicodeDecodeError:
                content = base64.b64encode(content).decode("utf-8")
                is_binary = True

        return {
            "name": self.name,
            "content": content,
            "is_binary": is_binary,
            "mime_type": self.mime_type,
            "disposition": self.disposition.value if isinstance(self.disposition, DataDisposition) else str(self.disposition),
            "description": self.description,
            "preview": self.preview,
            "metadata": self.metadata,
        }

    def model_dump(self, *_args: Any, **_kwargs: Any) -> Dict[str, Any]:
        """Back-compat shim for code that called pydantic's ``.model_dump()``.

        Returns the canonical wire dict (``to_serializable``). Positional and
        keyword args (e.g. ``exclude_none``) are accepted and ignored — the
        wire shape is fixed, not pydantic-configurable.
        """
        return self.to_serializable()

    @classmethod
    def from_serialized(cls, data: Dict[str, Any]) -> "DataObject":
        """
        Create a DataObject from a serialized dictionary.

        Handles base64-decoding of binary content.
        """
        content = data.get("content", "")
        if data.get("is_binary", False):
            content = base64.b64decode(content)

        return cls(
            name=data["name"],
            content=content,
            mime_type=data.get("mime_type", "text/plain"),
            disposition=DataDisposition(data.get("disposition", "auto")),
            description=data.get("description"),
            preview=data.get("preview"),
            metadata=data.get("metadata"),
        )

@dataclass
class ToolResult:
    """
    Structured result type for tools, enabling automatic artifact handling.

    Tools can return either a raw dict (backward compatible) or a ToolResult
    for enhanced handling. The framework detects ToolResult instances and
    processes them through the ToolResultProcessor.

    Examples::

        # Simple success with inline message:
        ToolResult(
            status="success",
            message="Operation completed successfully",
            data={"count": 42, "items": ["a", "b", "c"]}
        )

        # Success with auto-artifact handling:
        ToolResult(
            status="success",
            message="Generated report with 1000 rows",
            data_objects=[
                DataObject(
                    name="report.csv",
                    content=csv_content,
                    mime_type="text/csv",
                    description="Analysis report"
                )
            ]
        )

        # Error result:
        ToolResult.error("File not found: data.csv", code="FILE_NOT_FOUND")

        # Using convenience constructors:
        ToolResult.ok("Done", data={"count": 42})
        ToolResult.partial("Some items failed", error_code="PARTIAL_FAILURE")
    """
    status: str = "success"
    message: str = ""
    data: Optional[Dict[str, Any]] = None
    data_objects: List[DataObject] = field(default_factory=list)
    error_code: Optional[str] = None

    @classmethod
    def ok(
        cls,
        message: str,
        data: Optional[Dict[str, Any]] = None,
        data_objects: Optional[List[DataObject]] = None,
    ) -> "ToolResult":
        """
        Convenience constructor for successful results.

        Args:
            message: Human-readable success message
            data: Optional inline data dict
            data_objects: Optional list of DataObjects

        Returns:
            ToolResult with status="success"
        """
        return cls(
            status="success",
            message=message,
            data=data,
            data_objects=data_objects or [],
        )

    @classmethod
    def error(
        cls,
        message: str,
        code: Optional[str] = None,
        data: Optional[Dict[str, Any]] = None,
    ) -> "ToolResult":
        """
        Convenience constructor for error results.

        Args:
            message: Human-readable error message
            code: Optional machine-readable error code
            data: Optional additional error details

        Returns:
            ToolResult with status="error"
        """
        return cls(
            status="error",
            message=message,
            error_code=code,
            data=data,
        )

    @classmethod
    def auth_required(
        cls,
        message: str = "Authentication required",
    ) -> "ToolResult":
        """
        Convenience constructor for auth-required errors.

        Returns a ToolResult with error_code="AUTH_REQUIRED" that triggers
        the OAuth flow in the framework. Use this when get_auth_token()
        returns an empty string and the tool requires authentication.

        Args:
            message: Human-readable message about the auth requirement

        Returns:
            ToolResult with status="error" and error_code="AUTH_REQUIRED"
        """
        return cls(
            status="error",
            message=message,
            error_code="AUTH_REQUIRED",
        )

    @classmethod
    def partial(
        cls,
        message: str,
        data: Optional[Dict[str, Any]] = None,
        data_objects: Optional[List[DataObject]] = None,
        error_code: Optional[str] = None,
    ) -> "ToolResult":
        """
        Convenience constructor for partial success results.

        Use when some operations succeeded but others failed.

        Args:
            message: Human-readable message explaining partial success
            data: Optional inline data dict
            data_objects: Optional list of successfully created DataObjects
            error_code: Optional error code for the failures

        Returns:
            ToolResult with status="partial"
        """
        return cls(
            status="partial",
            message=message,
            data=data,
            data_objects=data_objects or [],
            error_code=error_code,
        )

    @classmethod
    def pending(
        cls,
        message: str,
        data: Optional[Dict[str, Any]] = None,
        data_objects: Optional[List[DataObject]] = None,
    ) -> "ToolResult":
        """
        Convenience constructor for pending results.

        Use when the tool is waiting for an external response such as
        human-in-the-loop approval. The STR worker intercepts this status
        and publishes a UserInputRequest if HIL metadata is present, so
        the data field should carry hil_request_id, hil_source, and any
        other fields the HIL workflow needs.

        Args:
            message: Human-readable message describing what is pending
            data: HIL metadata (hil_request_id, hil_source, etc.)
            data_objects: Optional list of DataObjects

        Returns:
            ToolResult with status="pending"
        """
        return cls(
            status="pending",
            message=message,
            data=data,
            data_objects=data_objects or [],
        )

    def to_serializable(self) -> Dict[str, Any]:
        """
        Convert ToolResult to a JSON-serializable dictionary.

        Matches the Go SDK's wireResult byte-for-byte. `data` and
        `data_objects` keys are omitted when empty (Go's ``omitempty``);
        `error_code` is always emitted (null when unset, like Go's
        ``*string`` pointer); `message` is always a non-null string.

        Returns:
            Dictionary that can be JSON-serialized and sent over the wire.
        """
        out: Dict[str, Any] = {
            "_schema": "ToolResult",
            "_schema_version": TOOL_RESULT_SCHEMA_VERSION,
            "status": self.status,
            "message": self.message or "",
            "error_code": self.error_code,
        }
        if self.data:
            out["data"] = self.data
        if self.data_objects:
            out["data_objects"] = [obj.to_serializable() for obj in self.data_objects]
        return out

    def model_dump(self, *_args: Any, **_kwargs: Any) -> Dict[str, Any]:
        """Back-compat shim for tools that called pydantic's ``.model_dump()``.

        Returns the canonical wire dict (``to_serializable``) — the same shape
        the STR receives when a tool returns the ToolResult instance directly,
        complete with the ``_schema`` markers and base64 binary handling the
        old pydantic ``.model_dump()`` skipped. Positional and keyword args
        (e.g. ``exclude_none``) are accepted and ignored.
        """
        return self.to_serializable()

    @classmethod
    def from_serialized(cls, data: Dict[str, Any]) -> "ToolResult":
        """
        Create a ToolResult from a serialized dictionary.

        Args:
            data: Dictionary from JSON deserialization

        Returns:
            ToolResult instance

        Raises:
            ValueError: If the data doesn't match expected format
        """
        if data.get("_schema") != "ToolResult":
            raise ValueError(
                f"Invalid schema marker: expected 'ToolResult', got '{data.get('_schema')}'"
            )

        version = data.get("_schema_version", "unknown")
        if version != TOOL_RESULT_SCHEMA_VERSION:
            log.warning(
                "ToolResult schema version mismatch: expected %s, got %s. "
                "Attempting to parse anyway.",
                TOOL_RESULT_SCHEMA_VERSION,
                version,
            )

        data_objects = []
        for obj_data in data.get("data_objects", []):
            try:
                data_objects.append(DataObject.from_serialized(obj_data))
            except Exception as e:
                log.warning("Failed to deserialize DataObject: %s. Skipping.", e)

        return cls(
            status=data.get("status", "success"),
            message=data.get("message") or "",
            data=data.get("data"),
            data_objects=data_objects,
            error_code=data.get("error_code"),
        )

    @staticmethod
    def is_serialized_tool_result(data: Any) -> bool:
        """
        Check if a dictionary appears to be a serialized ToolResult.

        Args:
            data: Value to check

        Returns:
            True if data looks like a serialized ToolResult
        """
        if not isinstance(data, dict):
            return False
        return data.get("_schema") == "ToolResult"
