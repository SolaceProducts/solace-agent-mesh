# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Tool-level option decorators that mirror the Go SDK's functional options.

Each decorator attaches an attribute to the function (or DynamicTool
subclass) that the schema builders read at `--schema` time. The wire-format
keys (``timeout_seconds``, ``volume_params``) match the Go SDK exactly so
the STR worker and platform parse a single shape regardless of language.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Callable, Dict, List


@dataclass
class VolumeParam:
    """A volume declaration this tool requires.

    Mirrors the Go SDK's ``VolumeParamDecl``. The platform reads these
    entries from the ``--schema`` output to provision and mount volumes
    before invoking the tool.

    Fields:
        name:         logical identifier (e.g. ``"workspace"``)
        mount_path:   path inside the sandbox (e.g. ``"/workspace"``)
        mode:         ``"readonly"`` or ``"readwrite"``
        description:  shown in the platform UI
    """

    name: str
    mount_path: str
    mode: str = "readonly"
    description: str = ""

    def to_dict(self) -> Dict[str, Any]:
        """Serialize to the wire shape the platform expects.

        Drops the empty description so the JSON matches Go's ``omitempty``
        behavior byte-for-byte.
        """
        out: Dict[str, Any] = {
            "name": self.name,
            "mount_path": self.mount_path,
            "mode": self.mode,
        }
        if self.description:
            out["description"] = self.description
        return out


@dataclass
class VolumeMount:
    """A volume mount provided by the STR at tool invocation time.

    Mirrors the Go SDK's ``VolumeMount``. Exposed via
    ``SandboxToolContextFacade.volume_mounts`` and
    ``SandboxToolContextFacade.get_volume_mount_path()``.
    """

    volume_id: str
    mount_path: str
    read_only: bool = False

    @classmethod
    def from_dict(cls, data: Dict[str, Any]) -> "VolumeMount":
        return cls(
            volume_id=data.get("volume_id", ""),
            mount_path=data.get("mount_path", ""),
            read_only=data.get("read_only", False),
        )


def tool_timeout(seconds: int) -> Callable[[Callable], Callable]:
    """Set the tool's execution timeout, in seconds.

    Emitted as ``timeout_seconds`` in the ``--schema`` output. It is a default:
    a ``timeout_seconds`` on the tool's manifest entry wins, and this value
    applies only when the manifest is silent. Mirrors Go's
    ``sdk.WithTimeout(seconds)``.

    Usage::

        from sam_tool_sdk import tool_cli, tool_timeout, ToolResult

        @tool_timeout(300)
        async def render_pdf(path: str) -> ToolResult:
            ...

        cli = tool_cli(render_pdf)
    """
    if not isinstance(seconds, int) or seconds <= 0:
        raise ValueError(
            f"tool_timeout seconds must be a positive int, got {seconds!r}"
        )

    def decorator(func: Callable) -> Callable:
        func._timeout_seconds = seconds  # type: ignore[attr-defined]
        return func

    return decorator


EFFECT_READ_ONLY = "read_only"
EFFECT_SIDE_EFFECTING = "side_effecting"
_EFFECTS = (EFFECT_READ_ONLY, EFFECT_SIDE_EFFECTING)


def tool_effect(effect: str) -> Callable[[Callable], Callable]:
    """Declare whether the tool is read_only or side_effecting.

    A tool is read_only only if all three hold: it cannot change anything outside the run, whatever its input; it fetches no URL chosen by its input or by the content of a file it reads; it writes, deletes or replaces no artifact under a filename chosen by its caller or by the tool itself. The run is the one task that called the tool, and changing something outside it means writing, sending, deleting, posting, publishing or indexing in an external system, another session or user scope, or starting a job. Changing the run's own session, such as the skills it has loaded, is not outside it. A filename is chosen by the caller when it comes from a parameter, and by the tool when its code fixes or derives it, including a name set on a data object it returns. A result the runtime itself saves under a fresh filename it picks, which it does only when a tool returns data with no name set (auto-disposition), is not a write. Sending the caller's query to one fixed external service, as a search or lookup does, is not fetching a chosen URL. Every other tool is side_effecting.

    Emitted as ``effect`` in the ``--schema`` output; the agent advertises it
    on its agent card. A tool that declares nothing is treated as
    side-effecting, so declare ``EFFECT_READ_ONLY`` only on a tool that meets
    every condition above. Mirrors Go's ``sdk.WithEffect(...)``.

    Usage::

        from sam_tool_sdk import tool_cli, tool_effect, EFFECT_READ_ONLY

        @tool_effect(EFFECT_READ_ONLY)
        async def lookup_order(order_id: str) -> ToolResult:
            ...

        cli = tool_cli(lookup_order)
    """
    if effect not in _EFFECTS:
        raise ValueError(
            f"tool_effect must be one of {_EFFECTS!r}, got {effect!r}"
        )

    def decorator(func: Callable) -> Callable:
        func._effect = effect  # type: ignore[attr-defined]
        return func

    return decorator


def with_volume_params(
    params: List[VolumeParam],
) -> Callable[[Callable], Callable]:
    """Declare volume parameters this tool needs at runtime.

    Emitted as ``volume_params`` in the ``--schema`` output so the STR
    and platform know which volumes to provision and mount. Mirrors Go's
    ``sdk.WithVolumeParams(...)``.

    Usage::

        from sam_tool_sdk import tool_cli, with_volume_params, VolumeParam

        @with_volume_params([
            VolumeParam(name="workspace", mount_path="/workspace", mode="readwrite"),
        ])
        async def render(path: str) -> ToolResult:
            ...

        cli = tool_cli(render)
    """
    serialized: List[Dict[str, Any]] = []
    for entry in params:
        if isinstance(entry, VolumeParam):
            serialized.append(entry.to_dict())
        elif isinstance(entry, dict):
            serialized.append(entry)
        else:
            raise TypeError(
                f"volume_params entry must be VolumeParam or dict, got {type(entry).__name__}"
            )

    def decorator(func: Callable) -> Callable:
        func._volume_params = serialized  # type: ignore[attr-defined]
        return func

    return decorator


def with_dynamic_schema(
    fn: Callable[[Dict[str, Any]], Any],
) -> Callable[[Callable], Callable]:
    """Register a per-deployment dynamic schema override.

    The callback is invoked when the platform runs ``--schema --config
    <toolset_config.json>``. It receives the parsed config dict and
    returns either ``None`` (use the static schema) or a dict that may
    override any of:

    * ``name`` (str) — change the LLM-visible tool name
    * ``description`` (str) — change the LLM-visible description
    * ``instructions`` (str) — system-prompt instructions
    * ``parameters`` (dict) — replace the JSON Schema for parameters
    * ``config_schema`` (list) — replace the operator config-schema

    Mirrors Go's ``sdk.WithDynamicSchema`` + ``sdk.SchemaOverride`` so the
    same per-agent specialization works in both languages.

    Usage::

        from sam_tool_sdk import tool_cli, with_dynamic_schema, ToolResult

        def override(config: dict):
            backend = config.get("backend", "mongo")
            return {
                "description": f"Query the {backend} backend",
                "parameters": _schema_for(backend),
            }

        @with_dynamic_schema(override)
        async def query(query: str) -> ToolResult:
            ...

        cli = tool_cli(query)
    """
    if not callable(fn):
        raise TypeError("with_dynamic_schema expects a callable")

    def decorator(func: Callable) -> Callable:
        func._dynamic_schema = fn  # type: ignore[attr-defined]
        return func

    return decorator
