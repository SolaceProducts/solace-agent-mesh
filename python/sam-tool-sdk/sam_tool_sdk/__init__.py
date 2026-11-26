# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""
SAM Tool SDK - Standalone Python SDK for Solace Agent Mesh tool development.

Provides the base classes, types, and runtime needed to develop and execute
Python tools for Solace Agent Mesh (SAM) without depending on the full
``solace_agent_mesh`` package.

Usage::

    from sam_tool_sdk import DynamicTool, ToolResult, Artifact
"""

from .dynamic_tool import DynamicTool, DynamicToolProvider, _FunctionAsDynamicTool
from .tool_result import ToolResult, DataObject, DataDisposition
from .artifact_types import Artifact, ArtifactTypeInfo, get_artifact_info, is_artifact_type
from .context_facade import SandboxToolContextFacade
from .cli import tool_cli, dynamic_tool_cli, provider_cli
from .config_schema import ConfigSchemaField, with_config_schema
from .tool_options import (
    tool_timeout,
    tool_effect,
    EFFECT_READ_ONLY,
    EFFECT_SIDE_EFFECTING,
    with_dynamic_schema,
    with_volume_params,
    VolumeParam,
    VolumeMount,
)
from .ipc import IPCError

__version__ = "0.1.5"

register_tool = DynamicToolProvider.register_tool

__all__ = [
    "DynamicTool",
    "DynamicToolProvider",
    "register_tool",
    "ToolResult",
    "DataObject",
    "DataDisposition",
    "Artifact",
    "ArtifactTypeInfo",
    "get_artifact_info",
    "is_artifact_type",
    "SandboxToolContextFacade",
    "tool_cli",
    "dynamic_tool_cli",
    "provider_cli",
    "ConfigSchemaField",
    "with_config_schema",
    "tool_timeout",
    "tool_effect",
    "EFFECT_READ_ONLY",
    "EFFECT_SIDE_EFFECTING",
    "with_dynamic_schema",
    "with_volume_params",
    "VolumeParam",
    "VolumeMount",
    "IPCError",
]
