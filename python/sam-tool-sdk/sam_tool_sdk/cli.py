# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""
CLI wrappers for SAM STR tool executables.

These wrappers turn Python tool functions/classes into standalone executables
that speak the same protocol as Go STR tools:

- ``--schema`` → JSON schema output (tool discovery)
- ``<argsfile>`` → execute the tool (reads args, writes result.json)

Usage in pyproject.toml::

    [project.scripts]
    my_tool = "my_module:cli"

Three patterns are supported:

1. **Function-based** (``tool_cli``)::

        from sam_tool_sdk import tool_cli, ToolResult

        async def my_tool(query: str) -> ToolResult:
            ...

        cli = tool_cli(my_tool)

2. **Class-based** (``dynamic_tool_cli``)::

        from sam_tool_sdk import dynamic_tool_cli, DynamicTool

        class MyTool(DynamicTool):
            ...

        cli = dynamic_tool_cli(MyTool)

3. **Provider** (``provider_cli``) — one executable, multiple tools::

        from sam_tool_sdk import provider_cli, DynamicToolProvider

        class MyTools(DynamicToolProvider):
            @MyTools.register_tool
            async def tool_a(self, x: str) -> dict: ...
            @MyTools.register_tool
            async def tool_b(self, y: int) -> dict: ...

        cli = provider_cli(MyTools)
"""

import inspect
import json
import logging
import sys
from pathlib import Path
from typing import Any, Callable, Dict, List, Optional, Tuple, Type

from ._lazy import asyncio
from .http_failure import execution_failure
from .tool_options import VolumeMount

log = logging.getLogger(__name__)

_SCHEMA_OVERRIDE_KEYS: Tuple[str, ...] = (
    "name",
    "description",
    "instructions",
    "parameters",
    "config_schema",
)

def _parse_schema_args(
    argv: List[str],
) -> Tuple[bool, Optional[Dict[str, Any]], Optional[str]]:
    """Parse the CLI argv into ``(schema_mode, config, argsfile)``.

    Matches Go's ``run.go`` argv parser: a bare ``--schema`` flag toggles
    discovery mode; an optional ``--config <file>`` loads the
    per-deployment config that's passed to dynamic-schema callbacks. The
    remaining positional (if any) is the runner_args.json path.
    """
    schema_mode = False
    config_file: Optional[str] = None
    positional: Optional[str] = None
    i = 0
    while i < len(argv):
        token = argv[i]
        if token == "--schema":
            schema_mode = True
            i += 1
        elif token == "--config" and i + 1 < len(argv):
            config_file = argv[i + 1]
            i += 2
        elif positional is None:
            positional = token
            i += 1
        else:
            i += 1

    config: Optional[Dict[str, Any]] = None
    if config_file:
        try:
            config = json.loads(Path(config_file).read_text())
        except Exception as e:
            print(
                f"samtoolsdk: reading config {config_file}: {e}",
                file=sys.stderr,
            )
            sys.exit(1)
        if not isinstance(config, dict):
            print(
                f"samtoolsdk: config {config_file} must be a JSON object",
                file=sys.stderr,
            )
            sys.exit(1)

    return schema_mode, config, positional

def _apply_schema_override(
    schema: Dict[str, Any], override: Optional[Dict[str, Any]]
) -> Tuple[Optional[str], Dict[str, Any]]:
    """Apply an override returned by a dynamic-schema callback.

    Returns ``(name_override, merged_schema)``. ``name_override`` is
    ``None`` unless the callback supplied a non-empty ``name`` key; the
    caller is responsible for re-keying the ``tools`` map when that
    happens. Other keys are merged onto a copy of ``schema``.
    """
    if not override:
        return None, schema
    merged = dict(schema)
    name_override: Optional[str] = None
    for key, value in override.items():
        if key not in _SCHEMA_OVERRIDE_KEYS:
            continue
        if value is None:
            continue
        if key == "name":
            if isinstance(value, str) and value:
                name_override = value
            continue
        merged[key] = value
    return name_override, merged

def _function_schema(func: Callable) -> Dict[str, Any]:
    """Build --schema JSON output for a single function-based tool."""
    from .tool_runner import discover_schema
    return _build_schema_from_func(func)

def _build_schema_from_func(func: Callable) -> Dict[str, Any]:
    """Build schema JSON for a function, reusing tool_runner's introspection."""
    from .tool_runner import (
        _python_type_to_json_schema,
        _is_tool_context_facade_param,
        _is_artifact_annotation,
        _parse_docstring,
        _require_description,
    )
    from typing import get_origin, get_args, Union

    sig = inspect.signature(func)
    description, instructions, param_descriptions = _parse_docstring(func)
    _require_description(getattr(func, "__name__", "tool"), description)

    properties = {}
    required = []
    artifact_params = {}

    for name, param in sig.parameters.items():
        if name in ("self", "cls", "kwargs"):
            continue

        ann = param.annotation

        if _is_tool_context_facade_param(ann):
            continue
        if name == "tool_config":
            continue

        schema_type = _python_type_to_json_schema(ann)
        if schema_type is None:
            continue

        prop = dict(schema_type)
        if name in param_descriptions:
            prop["description"] = param_descriptions[name]
        properties[name] = prop

        if param.default is inspect.Parameter.empty:
            required.append(name)

        if _is_artifact_annotation(ann):
            is_optional = param.default is not inspect.Parameter.empty
            artifact_params[name] = {"is_list": False, "is_optional": is_optional}
        elif ann is not inspect.Parameter.empty:
            origin = get_origin(ann)
            args = get_args(ann)
            if origin is list and args and _is_artifact_annotation(args[0]):
                is_optional = param.default is not inspect.Parameter.empty
                artifact_params[name] = {"is_list": True, "is_optional": is_optional}
            elif origin is Union:
                non_none = [a for a in args if a is not type(None)]
                if len(non_none) == 1:
                    inner = non_none[0]
                    if _is_artifact_annotation(inner):
                        artifact_params[name] = {"is_list": False, "is_optional": True}
                    else:
                        inner_origin = get_origin(inner)
                        inner_args = get_args(inner)
                        if inner_origin is list and inner_args and _is_artifact_annotation(inner_args[0]):
                            artifact_params[name] = {"is_list": True, "is_optional": True}

    tool_schema = {
        "description": description,
        "parameters": {
            "type": "object",
            "properties": properties,
        },
    }
    if required:
        tool_schema["parameters"]["required"] = required
    if artifact_params:
        tool_schema["artifact_params"] = artifact_params
    if instructions:
        tool_schema["instructions"] = instructions

    if hasattr(func, "_config_schema") and func._config_schema:
        from .config_schema import _assert_no_reserved_keys

        _assert_no_reserved_keys(func._config_schema)
        tool_schema["config_schema"] = func._config_schema

    if hasattr(func, "_timeout_seconds") and func._timeout_seconds:
        tool_schema["timeout_seconds"] = func._timeout_seconds

    if hasattr(func, "_volume_params") and func._volume_params:
        tool_schema["volume_params"] = func._volume_params

    if getattr(func, "_effect", None):
        tool_schema["effect"] = func._effect

    return tool_schema

def _dynamic_tool_schema(tool_instance) -> Dict[str, Any]:
    """Build --schema JSON for a DynamicTool instance."""
    schema = {
        "description": tool_instance.tool_description,
        "parameters": tool_instance.parameters_schema,
    }

    ap = tool_instance.artifact_params
    if ap:
        artifact_params = {}
        for name, info in ap.items():
            artifact_params[name] = {
                "is_list": getattr(info, "is_list", False),
                "is_optional": getattr(info, "is_optional", False),
            }
        schema["artifact_params"] = artifact_params

    auth = tool_instance.auth_config
    if auth:
        schema["auth"] = auth

    config = tool_instance.config_schema
    if config:
        from .config_schema import _assert_no_reserved_keys

        normalized: List[Dict[str, Any]] = []
        for entry in config:
            if isinstance(entry, dict):
                normalized.append(entry)
            elif hasattr(entry, "to_dict"):
                normalized.append(entry.to_dict())
            else:
                normalized.append({"key": getattr(entry, "key", "")})
        _assert_no_reserved_keys(normalized)
        schema["config_schema"] = config

    timeout = getattr(tool_instance, "timeout_seconds", None)
    if timeout:
        schema["timeout_seconds"] = timeout

    vps = getattr(tool_instance, "volume_params", None)
    if vps:
        schema["volume_params"] = [
            vp.to_dict() if hasattr(vp, "to_dict") else vp for vp in vps
        ]

    effect = getattr(tool_instance, "effect", None)
    if effect:
        from .tool_options import _EFFECTS

        if effect not in _EFFECTS:
            raise ValueError(
                f"DynamicTool.effect must be one of {_EFFECTS!r}, got {effect!r}"
            )
        schema["effect"] = effect

    return schema

def _run_function_tool(func: Callable, runner_args: Dict[str, Any]) -> Dict[str, Any]:
    """Execute a function-based tool from argsfile data."""
    from .tool_runner import (
        serialize_result,
        _extract_output_artifacts,
        _detect_special_params,
        _build_artifact_objects,
        _LegacyContextWrapper,
    )
    from .context_facade import SandboxToolContextFacade
    from .dynamic_tool import _coerce_args_to_schema

    args = runner_args.get("args", {})
    tool_config = runner_args.get("tool_config", {})
    artifact_paths = runner_args.get("artifact_paths", {})
    artifact_metadata = runner_args.get("artifact_metadata", {})
    status_pipe = runner_args.get("status_pipe", "")
    output_dir = runner_args.get("output_dir", "/sandbox/output")
    user_id = runner_args.get("user_id", "unknown")
    session_id = runner_args.get("session_id", "unknown")
    app_name = runner_args.get("app_name", "")
    auth_token = runner_args.get("tool_oauth_token", "")
    task_id = runner_args.get("task_id", "")
    ipc_socket = runner_args.get("ipc_socket", "")
    a2a_user_config = runner_args.get("a2a_user_config", "")
    volume_mounts = [
        VolumeMount.from_dict(vm) for vm in runner_args.get("volume_mounts", [])
    ]

    artifacts_by_filename: Dict[str, Dict[str, Any]] = {}
    for _param_name, meta in artifact_metadata.items():
        filename = meta.get("filename")
        if filename:
            artifacts_by_filename[filename] = {
                "local_path": meta["local_path"],
                "mime_type": meta.get("mime_type", "application/octet-stream"),
                "version": meta.get("version", 0),
            }

    context = SandboxToolContextFacade(
        status_pipe_path=status_pipe,
        tool_config=tool_config,
        artifacts=artifacts_by_filename,
        output_dir=output_dir,
        user_id=user_id,
        session_id=session_id,
        app_name=app_name,
        artifact_paths=artifact_paths,
        auth_token=auth_token,
        task_id=task_id,
        ipc_socket_path=ipc_socket,
        volume_mounts=volume_mounts,
        a2a_user_config=a2a_user_config,
    )

    try:
        args = _coerce_args_to_schema(args, _build_schema_from_func(func).get("parameters"))

        ctx_param_name, artifact_params = _detect_special_params(func)

        if ctx_param_name or artifact_params:
            kwargs = dict(args)
            if artifact_params:
                artifact_objects = _build_artifact_objects(artifact_params, artifact_metadata, args)
                kwargs.update(artifact_objects)
            if ctx_param_name:
                kwargs[ctx_param_name] = context
            if inspect.iscoroutinefunction(func):
                result = asyncio.run(func(**kwargs))
            else:
                result = func(**kwargs)
        else:
            legacy_ctx = _LegacyContextWrapper(context)
            if inspect.iscoroutinefunction(func):
                result = asyncio.run(func(legacy_ctx, **args))
            else:
                result = func(legacy_ctx, **args)

        result = _extract_output_artifacts(result, output_dir)
        return {"result": serialize_result(result)}

    except Exception as e:
        log.exception("Tool execution failed: %s", e)
        return execution_failure(e)

def _run_class_tool(tool_class: Type, runner_args: Dict[str, Any]) -> Dict[str, Any]:
    """Execute a DynamicTool class from argsfile data."""
    from .tool_runner import run_class_tool_async, serialize_result
    from .context_facade import SandboxToolContextFacade
    from .dynamic_tool import _coerce_args_to_schema

    args = runner_args.get("args", {})
    tool_config = runner_args.get("tool_config", {})
    artifact_paths = runner_args.get("artifact_paths", {})
    artifact_metadata = runner_args.get("artifact_metadata", {})
    status_pipe = runner_args.get("status_pipe", "")
    output_dir = runner_args.get("output_dir", "/sandbox/output")
    user_id = runner_args.get("user_id", "unknown")
    session_id = runner_args.get("session_id", "unknown")
    app_name = runner_args.get("app_name", "")
    auth_token = runner_args.get("tool_oauth_token", "")
    task_id = runner_args.get("task_id", "")
    ipc_socket = runner_args.get("ipc_socket", "")
    a2a_user_config = runner_args.get("a2a_user_config", "")
    volume_mounts = [
        VolumeMount.from_dict(vm) for vm in runner_args.get("volume_mounts", [])
    ]

    artifacts_by_filename: Dict[str, Dict[str, Any]] = {}
    for _param_name, meta in artifact_metadata.items():
        filename = meta.get("filename")
        if filename:
            artifacts_by_filename[filename] = {
                "local_path": meta["local_path"],
                "mime_type": meta.get("mime_type", "application/octet-stream"),
                "version": meta.get("version", 0),
            }

    context = SandboxToolContextFacade(
        status_pipe_path=status_pipe,
        tool_config=tool_config,
        artifacts=artifacts_by_filename,
        output_dir=output_dir,
        user_id=user_id,
        session_id=session_id,
        app_name=app_name,
        artifact_paths=artifact_paths,
        auth_token=auth_token,
        task_id=task_id,
        ipc_socket_path=ipc_socket,
        volume_mounts=volume_mounts,
        a2a_user_config=a2a_user_config,
    )

    try:
        tool_instance = tool_class(tool_config=tool_config)

        from .tool_runner import (
            _ArtifactTypeInfo,
            _build_artifact_objects,
            _extract_output_artifacts,
            serialize_result,
        )

        ctx_facade_param_name = getattr(tool_instance, "ctx_facade_param_name", None)
        artifact_params_info = getattr(tool_instance, "artifact_params", {})

        args = _coerce_args_to_schema(args, getattr(tool_instance, "parameters_schema", None))
        kwargs = dict(args)

        if artifact_params_info:
            local_artifact_params = {}
            for name, info in artifact_params_info.items():
                local_artifact_params[name] = _ArtifactTypeInfo(
                    is_artifact=getattr(info, "is_artifact", True),
                    is_list=getattr(info, "is_list", False),
                    is_optional=getattr(info, "is_optional", False),
                )
            artifact_objects = _build_artifact_objects(
                local_artifact_params, artifact_metadata, args
            )
            kwargs.update(artifact_objects)

        if ctx_facade_param_name:
            kwargs[ctx_facade_param_name] = context

        result = asyncio.run(tool_instance._run_async_impl(args=kwargs, tool_context=context))
        result = _extract_output_artifacts(result, output_dir)
        return {"result": serialize_result(result)}

    except Exception as e:
        log.exception("Tool execution failed: %s", e)
        return execution_failure(e)

def _run_provider_tool(
    provider_class: Type,
    tool_name: str,
    runner_args: Dict[str, Any],
) -> Dict[str, Any]:
    """Execute a specific tool from a DynamicToolProvider."""
    from .tool_runner import (
        _ArtifactTypeInfo,
        _build_artifact_objects,
        _extract_output_artifacts,
        serialize_result,
    )
    from .context_facade import SandboxToolContextFacade
    from .dynamic_tool import _coerce_args_to_schema

    args = runner_args.get("args", {})
    tool_config = runner_args.get("tool_config", {})
    artifact_metadata = runner_args.get("artifact_metadata", {})
    artifact_paths = runner_args.get("artifact_paths", {})
    status_pipe = runner_args.get("status_pipe", "")
    output_dir = runner_args.get("output_dir", "/sandbox/output")
    user_id = runner_args.get("user_id", "unknown")
    session_id = runner_args.get("session_id", "unknown")
    app_name = runner_args.get("app_name", "")
    auth_token = runner_args.get("tool_oauth_token", "")
    task_id = runner_args.get("task_id", "")
    ipc_socket = runner_args.get("ipc_socket", "")
    a2a_user_config = runner_args.get("a2a_user_config", "")
    volume_mounts = [
        VolumeMount.from_dict(vm) for vm in runner_args.get("volume_mounts", [])
    ]

    artifacts_by_filename: Dict[str, Dict[str, Any]] = {}
    for _param_name, meta in artifact_metadata.items():
        filename = meta.get("filename")
        if filename:
            artifacts_by_filename[filename] = {
                "local_path": meta["local_path"],
                "mime_type": meta.get("mime_type", "application/octet-stream"),
                "version": meta.get("version", 0),
            }

    context = SandboxToolContextFacade(
        status_pipe_path=status_pipe,
        tool_config=tool_config,
        artifacts=artifacts_by_filename,
        output_dir=output_dir,
        user_id=user_id,
        session_id=session_id,
        app_name=app_name,
        artifact_paths=artifact_paths,
        auth_token=auth_token,
        task_id=task_id,
        ipc_socket_path=ipc_socket,
        volume_mounts=volume_mounts,
        a2a_user_config=a2a_user_config,
    )

    try:
        provider = provider_class()
        provider._tool_config = tool_config or {}
        all_tools = provider.get_all_tools_for_framework(tool_config)

        tool_instance = None
        for t in all_tools:
            if t.tool_name == tool_name:
                tool_instance = t
                break

        if tool_instance is None:
            return {"error": f"Tool {tool_name!r} not found in provider {provider_class.__name__}"}

        ctx_facade_param_name = getattr(tool_instance, "ctx_facade_param_name", None)
        artifact_params_info = getattr(tool_instance, "artifact_params", {})

        args = _coerce_args_to_schema(args, getattr(tool_instance, "parameters_schema", None))
        kwargs = dict(args)

        if artifact_params_info:
            local_artifact_params = {}
            for name, info in artifact_params_info.items():
                local_artifact_params[name] = _ArtifactTypeInfo(
                    is_artifact=getattr(info, "is_artifact", True),
                    is_list=getattr(info, "is_list", False),
                    is_optional=getattr(info, "is_optional", False),
                )
            artifact_objects = _build_artifact_objects(
                local_artifact_params, artifact_metadata, args
            )
            kwargs.update(artifact_objects)

        if ctx_facade_param_name:
            kwargs[ctx_facade_param_name] = context

        result = asyncio.run(tool_instance._run_async_impl(args=kwargs, tool_context=context))
        result = _extract_output_artifacts(result, output_dir)
        return {"result": serialize_result(result)}

    except Exception as e:
        log.exception("Tool execution failed: %s", e)
        return execution_failure(e)

def _read_argsfile(path: str) -> Dict[str, Any]:
    """Read and parse the runner_args.json file."""
    return json.loads(Path(path).read_text())

def _normalize_envelope(result: Dict[str, Any]) -> Dict[str, Any]:
    """Normalize the result envelope to always-both-keys, matching Go's wire format.

    Go's pkg/samtoolsdk/run.go emits ``{"result": ..., "error": ""}`` on
    every invocation. Python's runner internals return one or the other.
    The STR consumer treats absent keys and zero values equivalently, but
    a uniform shape simplifies parsing on both sides.
    """
    return {
        "result": result.get("result"),
        "error": result.get("error", ""),
    }

def _write_result(runner_args: Dict[str, Any], result: Dict[str, Any]) -> None:
    """Write the result to result.json (or stdout as fallback)."""
    envelope = _normalize_envelope(result)
    result_file = runner_args.get("result_file")
    if result_file:
        Path(result_file).write_text(json.dumps(envelope, default=str))
        log.info("Result written to: %s", result_file)
    else:
        print(json.dumps(envelope, default=str))

def tool_cli(func: Callable) -> Callable:
    """Create a CLI entry point for a function-based tool.

    Returns a callable suitable for ``[project.scripts]`` in pyproject.toml.

    The returned callable handles:
    - ``--schema``: Output JSON schema for the tool
    - ``<argsfile>``: Execute the tool with the given arguments

    Example::

        from sam_tool_sdk import tool_cli, ToolResult

        async def my_tool(query: str) -> ToolResult:
            \"\"\"Search for things.\"\"\"
            ...

        cli = tool_cli(my_tool)

        # pyproject.toml:
        # [project.scripts]
        # my_tool = "my_module:cli"
    """
    def main():
        logging.basicConfig(
            level=logging.INFO,
            format="[sandbox] %(levelname)s %(name)s: %(message)s",
            stream=sys.stderr,
        )

        schema_mode, config, argsfile = _parse_schema_args(sys.argv[1:])

        if schema_mode:
            try:
                schema = _build_schema_from_func(func)
                tool_name = func.__name__
                fn = getattr(func, "_dynamic_schema", None)
                if config is not None and callable(fn):
                    override = fn(config)
                    name_override, schema = _apply_schema_override(schema, override)
                    if name_override:
                        tool_name = name_override
                output = {"tools": {tool_name: schema}}
                json.dump(output, sys.stdout, indent=2)
                print()
            except Exception as e:
                print(f"Schema discovery failed: {e}", file=sys.stderr)
                sys.exit(1)
            sys.exit(0)

        if argsfile is None:
            print(
                f"Usage: {sys.argv[0]} [--schema [--config <file>] | <argsfile>]",
                file=sys.stderr,
            )
            sys.exit(1)

        runner_args = _read_argsfile(argsfile)
        result = _run_function_tool(func, runner_args)
        _write_result(runner_args, result)

    return main

def dynamic_tool_cli(tool_class: Type) -> Callable:
    """Create a CLI entry point for a DynamicTool class.

    Returns a callable suitable for ``[project.scripts]`` in pyproject.toml.

    Example::

        from sam_tool_sdk import dynamic_tool_cli, DynamicTool

        class MyTool(DynamicTool):
            ...

        cli = dynamic_tool_cli(MyTool)
    """
    def main():
        logging.basicConfig(
            level=logging.INFO,
            format="[sandbox] %(levelname)s %(name)s: %(message)s",
            stream=sys.stderr,
        )

        schema_mode, config, argsfile = _parse_schema_args(sys.argv[1:])

        if schema_mode:
            try:
                instance = tool_class(tool_config=config or {})
                schema = _dynamic_tool_schema(instance)
                tool_name = instance.tool_name
                if config is not None:
                    override = instance.dynamic_schema(config)
                    name_override, schema = _apply_schema_override(schema, override)
                    if name_override:
                        tool_name = name_override
                output = {"tools": {tool_name: schema}}
                json.dump(output, sys.stdout, indent=2)
                print()
            except Exception as e:
                print(f"Schema discovery failed: {e}", file=sys.stderr)
                sys.exit(1)
            sys.exit(0)

        if argsfile is None:
            print(
                f"Usage: {sys.argv[0]} [--schema [--config <file>] | <argsfile>]",
                file=sys.stderr,
            )
            sys.exit(1)

        runner_args = _read_argsfile(argsfile)
        result = _run_class_tool(tool_class, runner_args)
        _write_result(runner_args, result)

    return main

def provider_cli(provider_class: Type) -> Callable:
    """Create a CLI entry point for a DynamicToolProvider (multi-tool).

    One executable exposes multiple tools via ``--schema`` and dispatches
    to the correct tool via the ``tool_name`` field in the argsfile.

    Returns a callable suitable for ``[project.scripts]`` in pyproject.toml.

    Example::

        from sam_tool_sdk import provider_cli, DynamicToolProvider

        class MyTools(DynamicToolProvider):
            @MyTools.register_tool
            async def tool_a(self, x: str) -> dict: ...

            @MyTools.register_tool
            async def tool_b(self, y: int) -> dict: ...

            def create_tools(self, tool_config=None):
                return []

        cli = provider_cli(MyTools)
    """
    def main():
        logging.basicConfig(
            level=logging.INFO,
            format="[sandbox] %(levelname)s %(name)s: %(message)s",
            stream=sys.stderr,
        )

        schema_mode, config, argsfile = _parse_schema_args(sys.argv[1:])

        if schema_mode:
            try:
                provider = provider_class()
                all_tools = provider.get_all_tools_for_framework(
                    tool_config=config or {}
                )
                provider_config = provider.config_schema
                tools_schema: Dict[str, Any] = {}
                for tool in all_tools:
                    schema = _dynamic_tool_schema(tool)
                    if "config_schema" not in schema and provider_config:
                        schema["config_schema"] = provider_config
                    tool_name = tool.tool_name
                    if config is not None:
                        override = tool.dynamic_schema(config)
                        name_override, schema = _apply_schema_override(schema, override)
                        if name_override:
                            tool_name = name_override
                    tools_schema[tool_name] = schema
                output = {"tools": tools_schema}
                json.dump(output, sys.stdout, indent=2)
                print()
            except Exception as e:
                print(f"Schema discovery failed: {e}", file=sys.stderr)
                sys.exit(1)
            sys.exit(0)

        if argsfile is None:
            print(
                f"Usage: {sys.argv[0]} [--schema [--config <file>] | <argsfile>]",
                file=sys.stderr,
            )
            sys.exit(1)

        runner_args = _read_argsfile(argsfile)
        tool_name = runner_args.get("tool_name", "")
        if not tool_name:
            print("Error: tool_name is required in argsfile for provider tools", file=sys.stderr)
            sys.exit(1)

        result = _run_provider_tool(provider_class, tool_name, runner_args)
        _write_result(runner_args, result)

    return main
