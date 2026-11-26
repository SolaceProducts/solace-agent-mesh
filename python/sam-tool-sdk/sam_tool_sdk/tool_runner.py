# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""
Tool Runner - Executes tools inside the sandbox.

This module is invoked as a subprocess by the Go STR (Secure Tool Runtime)
to run a Python tool function within an isolated environment. It:
1. Reads invocation parameters from a JSON file
2. Detects special type annotations (ToolContextFacade, Artifact)
3. Sets up context and artifact injection (mirroring DynamicTool behavior)
4. Imports and calls the tool function
5. Extracts output artifacts from ToolResult DataObjects
6. Writes the result to a JSON file

For tools WITH type annotations (portable tools):
    - ToolContextFacade param detected -> SandboxToolContextFacade injected by name
    - Artifact params detected -> Artifact objects built from preloaded files
    - ToolResult return handled -> DataObjects extracted to output dir

For tools WITHOUT type annotations (legacy sandbox-only tools):
    - Context passed as first positional arg
    - Dict return passed through unchanged

Usage:
    python -m sam_tool_sdk.tool_runner /path/to/runner_args.json
"""

import importlib
import inspect
import json
import logging
import re
import sys
import traceback
from pathlib import Path
from typing import Any, Dict, List, Optional, Set, Tuple

from ._lazy import asyncio
from .artifact_types import Artifact
from .context_facade import SandboxToolContextFacade
from .http_failure import execution_failure
from .tool_result import DataDisposition

logging.basicConfig(
    level=logging.INFO,
    format="[sandbox] %(levelname)s %(name)s: %(message)s",
    stream=sys.stderr,
)
log = logging.getLogger(__name__)

class _ArtifactTypeInfo:
    __slots__ = ("is_artifact", "is_list", "is_optional")

    def __init__(self, is_artifact=False, is_list=False, is_optional=False):
        self.is_artifact = is_artifact
        self.is_list = is_list
        self.is_optional = is_optional

def _is_tool_context_facade_param(annotation) -> bool:
    """Check if an annotation represents a ToolContextFacade parameter.

    Uses string-based checks first to avoid importing heavy modules.
    Falls back to identity comparison only when the class is already loaded.
    """
    if annotation is None:
        return False

    if annotation is SandboxToolContextFacade:
        return True

    if isinstance(annotation, str):
        if "ToolContextFacade" in annotation or annotation.strip() == "ToolContext":
            return True

    ann_name = getattr(annotation, "__name__", "") or ""
    ann_qualname = getattr(annotation, "__qualname__", "") or ""
    if ann_name in ("ToolContextFacade", "ToolContext") or ann_qualname in ("ToolContextFacade", "ToolContext"):
        return True

    return False

def _is_artifact_annotation(annotation) -> bool:
    """Check if an annotation is the Artifact type (by name, not import)."""
    if annotation is None:
        return False
    if isinstance(annotation, str):
        return annotation.strip() == "Artifact"
    return getattr(annotation, "__name__", "") == "Artifact"

def _detect_special_params(func) -> Tuple[Optional[str], Dict[str, Any]]:
    """Detect ToolContextFacade and Artifact parameters from type annotations.

    Uses lightweight string/name checks to avoid importing heavy modules.

    Returns:
        Tuple of (ctx_param_name, artifact_params_dict)
        - ctx_param_name: Name of the ToolContextFacade parameter, or None
        - artifact_params_dict: Dict of param_name -> ArtifactTypeInfo for Artifact params
    """
    from typing import get_origin, get_args, Union

    sig = inspect.signature(func)
    ctx_param_name = None
    artifact_params = {}

    for name, param in sig.parameters.items():
        if name in ("self", "cls", "kwargs"):
            continue
        ann = param.annotation
        if ann is inspect.Parameter.empty:
            continue

        if _is_tool_context_facade_param(ann):
            ctx_param_name = name
            log.debug("Detected ToolContextFacade param: '%s'", name)
        elif _is_artifact_annotation(ann):
            artifact_params[name] = _ArtifactTypeInfo(is_artifact=True)
            log.debug("Detected Artifact param: '%s'", name)
        else:
            origin = get_origin(ann)
            if origin is not None:
                args = get_args(ann)
                if args:
                    if origin is list and _is_artifact_annotation(args[0]):
                        artifact_params[name] = _ArtifactTypeInfo(
                            is_artifact=True, is_list=True
                        )
                        log.debug("Detected List[Artifact] param: '%s'", name)
                    elif origin is Union:
                        has_none = type(None) in args
                        for arg in args:
                            if arg is type(None):
                                continue
                            if _is_artifact_annotation(arg):
                                artifact_params[name] = _ArtifactTypeInfo(
                                    is_artifact=True, is_optional=has_none
                                )
                                log.debug("Detected Optional[Artifact] param: '%s'", name)
                                break
                            inner_origin = get_origin(arg)
                            inner_args = get_args(arg)
                            if inner_origin is list and inner_args and _is_artifact_annotation(inner_args[0]):
                                artifact_params[name] = _ArtifactTypeInfo(
                                    is_artifact=True, is_list=True, is_optional=has_none
                                )
                                log.debug("Detected Optional[List[Artifact]] param: '%s'", name)
                                break

    return ctx_param_name, artifact_params

def _build_artifact_objects(
    artifact_params: Dict[str, Any],
    artifact_metadata: Dict[str, Dict[str, Any]],
    args: Dict[str, Any],
) -> Dict[str, Any]:
    """Build Artifact objects from preloaded files for annotated parameters.

    Args:
        artifact_params: Dict of param_name -> ArtifactTypeInfo
        artifact_metadata: Dict of param_name -> metadata dict from runner_args
        args: The raw tool arguments (for List[Artifact] filename lists)

    Returns:
        Dict of param_name -> Artifact (or List[Artifact]) to inject as kwargs
    """
    injected = {}

    for param_name, type_info in artifact_params.items():
        if type_info.is_list:
            artifacts = []
            filenames = args.get(param_name, [])
            for idx, _fname in enumerate(filenames):
                ref_key = f"{param_name}[{idx}]"
                ref_meta = artifact_metadata.get(ref_key)
                if ref_meta:
                    ref_path = Path(ref_meta["local_path"])
                    if ref_path.exists():
                        artifacts.append(Artifact(
                            content=ref_path.read_bytes(),
                            filename=ref_meta["filename"],
                            version=ref_meta.get("version", 0),
                            mime_type=ref_meta.get("mime_type", "application/octet-stream"),
                            metadata=dict(ref_meta),
                        ))
                    else:
                        log.warning("Artifact file missing for %s: %s", ref_key, ref_path)
                else:
                    log.warning("No metadata for list artifact: %s", ref_key)
            injected[param_name] = artifacts
            log.debug("Built %d Artifact objects for list param '%s'", len(artifacts), param_name)
        else:
            meta = artifact_metadata.get(param_name)
            if not meta:
                log.warning("No metadata for artifact param: %s", param_name)
                continue
            path = Path(meta["local_path"])
            if not path.exists():
                log.warning("Artifact file missing for %s: %s", param_name, path)
                continue
            injected[param_name] = Artifact(
                content=path.read_bytes(),
                filename=meta["filename"],
                version=meta.get("version", 0),
                mime_type=meta.get("mime_type", "application/octet-stream"),
                metadata=dict(meta),
            )
            log.debug("Built Artifact object for param '%s': %s", param_name, meta["filename"])

    return injected

def _extract_output_artifacts(result: Any, output_dir: str) -> Any:
    """Extract DataObjects with artifact disposition and write to output dir.

    When a portable tool returns ToolResult with DataObjects, artifacts
    need to be written to the output directory so the Go STR worker
    picks them up.

    Args:
        result: The tool's return value (may be ToolResult, dict, etc.)
        output_dir: Path to the output directory

    Returns:
        The result with artifact DataObjects removed (only inline ones remain)
    """
    if not hasattr(result, "data_objects") or not result.data_objects:
        return result

    output_path = Path(output_dir)
    output_path.mkdir(parents=True, exist_ok=True)
    remaining = []

    for obj in result.data_objects:
        disposition = getattr(obj, "disposition", None)
        if disposition in (
            DataDisposition.ARTIFACT,
            DataDisposition.AUTO,
            DataDisposition.ARTIFACT_WITH_PREVIEW,
        ):
            content = obj.content
            if isinstance(content, str):
                content = content.encode("utf-8")
            artifact_path = output_path / obj.name
            artifact_path.write_bytes(content)
            log.info("Extracted output artifact: %s (%d bytes)", obj.name, len(content))
        else:
            remaining.append(obj)

    result.data_objects = remaining
    return result

class _LegacyContextWrapper:
    """Wraps SandboxToolContextFacade with the old sync API for legacy tools.

    Legacy sandbox tools (no type annotations) call sync methods like:
    - ctx.load_artifact(param_name) -> Optional[bytes]
    - ctx.load_artifact_text(param_name) -> Optional[str]
    - ctx.list_artifacts() -> Dict[str, str]
    - ctx.save_artifact(filename, content) -> str
    - ctx.send_status(message) -> bool
    """

    def __init__(self, facade: SandboxToolContextFacade):
        self._facade = facade

    @property
    def user_id(self) -> str:
        return self._facade.user_id

    @property
    def session_id(self) -> str:
        return self._facade.session_id

    @property
    def app_name(self) -> str:
        return self._facade.app_name

    def get_config(self, key: str, default: Any = None) -> Any:
        return self._facade.get_config(key, default)

    def send_status(self, message: str) -> bool:
        return self._facade.send_status(message)

    def load_artifact(self, param_name: str) -> Optional[bytes]:
        """Load artifact by parameter name (sync, legacy API)."""
        return self._facade.load_artifact_by_param(param_name)

    def load_artifact_text(self, param_name: str, encoding: str = "utf-8") -> Optional[str]:
        """Load artifact as text by parameter name (sync, legacy API)."""
        content = self._facade.load_artifact_by_param(param_name)
        if content is None:
            return None
        return content.decode(encoding)

    def list_artifacts(self) -> Dict[str, str]:
        """List artifacts as param_name -> path dict (sync, legacy API)."""
        return dict(self._facade._artifact_paths)

    def save_artifact(self, filename: str, content: bytes,
                      mime_type: str = "application/octet-stream") -> str:
        return self._facade.save_artifact(filename, content, mime_type)

    def save_artifact_text(self, filename: str, content: str,
                           encoding: str = "utf-8",
                           mime_type: str = "text/plain") -> str:
        return self._facade.save_artifact_text(filename, content, encoding, mime_type)

    def list_output_artifacts(self) -> list:
        return self._facade.list_output_artifacts()

def load_tool_function(module_path: str, function_name: str):
    """
    Dynamically import and return a tool function.

    Args:
        module_path: Dot-separated module path (e.g., "mytools.data")
        function_name: Name of the function to call

    Returns:
        The callable function

    Raises:
        ImportError: If module cannot be imported
        AttributeError: If function not found in module
    """
    log.info("Loading tool: %s.%s", module_path, function_name)
    module = importlib.import_module(module_path)
    func = getattr(module, function_name)

    if not callable(func):
        raise TypeError(f"{module_path}.{function_name} is not callable")

    return func

def serialize_result(result: Any) -> Dict[str, Any]:
    """
    Serialize a tool result to a JSON-compatible format.

    Handles ToolResult objects (via to_serializable()), plain dicts, and strings.

    Args:
        result: The tool function's return value

    Returns:
        JSON-serializable dictionary
    """
    if result is None:
        return {"text": None, "data_objects": []}

    if hasattr(result, "to_serializable") and hasattr(result, "data_objects"):
        return result.to_serializable()

    if isinstance(result, dict):
        return result

    if isinstance(result, str):
        return {"text": result, "data_objects": []}

    try:
        return {"text": str(result), "data_objects": []}
    except Exception:
        return {"text": repr(result), "data_objects": []}

def _call_handler(func, *args, **kwargs):
    """Invoke a tool handler, paying the event-loop cost only when necessary.

    A coroutine handler is driven by a throwaway loop via ``asyncio.run``; a
    synchronous handler is called directly, so sync function tools never spin up
    an event loop or import asyncio.
    """
    if inspect.iscoroutinefunction(func):
        return asyncio.run(func(*args, **kwargs))
    return func(*args, **kwargs)

def _prepare_function_call(
    module_path: str,
    function_name: str,
    args: Dict[str, Any],
    context: SandboxToolContextFacade,
    artifact_metadata: Dict[str, Dict[str, Any]],
) -> Tuple[Any, Tuple[Any, ...], Dict[str, Any]]:
    """Resolve a tool function and assemble its positional/keyword arguments.

    Shared by run_tool_async (cli path) and run_tool_sync (runner path) so the
    annotation-aware vs legacy injection logic lives in one place.
    """
    func = load_tool_function(module_path, function_name)
    ctx_param_name, artifact_params = _detect_special_params(func)

    if ctx_param_name or artifact_params:
        log.info(
            "Using portable injection: ctx_param=%s, artifact_params=%s",
            ctx_param_name,
            list(artifact_params.keys()),
        )
        kwargs = dict(args)
        if artifact_params:
            kwargs.update(
                _build_artifact_objects(artifact_params, artifact_metadata, args)
            )
        if ctx_param_name:
            kwargs[ctx_param_name] = context
        return func, (), kwargs

    log.info("Using legacy injection: context as first positional arg")
    return func, (_LegacyContextWrapper(context),), dict(args)

def run_tool_sync(
    module_path: str,
    function_name: str,
    args: Dict[str, Any],
    context: SandboxToolContextFacade,
    artifact_metadata: Dict[str, Dict[str, Any]],
    output_dir: str,
) -> Dict[str, Any]:
    """Synchronous counterpart to run_tool_async.

    Runs a synchronous handler directly -- no event loop, no asyncio import --
    and falls back to ``asyncio.run`` only for a coroutine handler. run_tool
    uses this for function tools, so the common sync case skips the per-call
    event-loop setup and the asyncio import entirely.
    """
    func, pos, kwargs = _prepare_function_call(
        module_path, function_name, args, context, artifact_metadata
    )
    result = _call_handler(func, *pos, **kwargs)
    result = _extract_output_artifacts(result, output_dir)
    return serialize_result(result)

async def run_tool_async(
    module_path: str,
    function_name: str,
    args: Dict[str, Any],
    context: SandboxToolContextFacade,
    artifact_metadata: Dict[str, Dict[str, Any]],
    output_dir: str,
) -> Dict[str, Any]:
    """
    Run a tool function (sync or async) with annotation-aware injection.

    For tools with ToolContextFacade/Artifact type annotations:
    - Detects special params from function signature
    - Builds Artifact objects from preloaded files
    - Injects context and artifacts by parameter name

    For tools without annotations (legacy):
    - Passes context as first positional arg, args as kwargs

    Args:
        module_path: Module path to import
        function_name: Function name to call
        args: Arguments to pass to the function
        context: The sandbox context facade
        artifact_metadata: Rich metadata per artifact param
        output_dir: Path for output artifacts

    Returns:
        Serialized result dictionary
    """
    func, pos, kwargs = _prepare_function_call(
        module_path, function_name, args, context, artifact_metadata
    )

    if inspect.iscoroutinefunction(func):
        result = await func(*pos, **kwargs)
    else:
        result = func(*pos, **kwargs)

    result = _extract_output_artifacts(result, output_dir)

    return serialize_result(result)

async def run_class_tool_async(
    module_path: str,
    class_name: str,
    args: Dict[str, Any],
    context: SandboxToolContextFacade,
    tool_config: Dict[str, Any],
    artifact_metadata: Dict[str, Dict[str, Any]],
    output_dir: str,
) -> Dict[str, Any]:
    """
    Run a DynamicTool class inside the sandbox.

    Instantiates the class with tool_config, reads its artifact_params and
    ctx_facade_param_name properties, injects context and artifacts, and
    calls _run_async_impl(args, tool_context=None).

    Args:
        module_path: Module path containing the DynamicTool class
        class_name: Name of the DynamicTool subclass
        args: Arguments to pass to the tool
        context: The sandbox context facade
        tool_config: Tool configuration
        artifact_metadata: Rich metadata per artifact param
        output_dir: Path for output artifacts

    Returns:
        Serialized result dictionary
    """
    log.info("Loading DynamicTool class: %s.%s", module_path, class_name)
    module = importlib.import_module(module_path)
    tool_class = getattr(module, class_name)

    tool_instance = tool_class(tool_config=tool_config)

    ctx_facade_param_name = getattr(tool_instance, "ctx_facade_param_name", None)
    artifact_params_info = getattr(tool_instance, "artifact_params", {})

    log.info(
        "DynamicTool injection: ctx_param=%s, artifact_params=%s",
        ctx_facade_param_name,
        list(artifact_params_info.keys()) if artifact_params_info else [],
    )

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

    result = await tool_instance._run_async_impl(
        args=kwargs, tool_context=None
    )

    result = _extract_output_artifacts(result, output_dir)

    return serialize_result(result)

async def run_provider_method_async(
    module_path: str,
    class_name: str,
    function_name: str,
    args: Dict[str, Any],
    context: SandboxToolContextFacade,
    tool_config: Dict[str, Any],
    artifact_metadata: Dict[str, Dict[str, Any]],
    output_dir: str,
) -> Dict[str, Any]:
    """
    Run a @register_tool decorated method on a DynamicToolProvider.

    When both class_name (provider) and function_name are specified,
    instantiate the provider, bind the method, and call it with
    annotation-aware injection.

    Args:
        module_path: Module path containing the provider class
        class_name: Name of the DynamicToolProvider subclass
        function_name: Name of the @register_tool decorated function
        args: Arguments to pass to the function
        context: The sandbox context facade
        tool_config: Tool configuration
        artifact_metadata: Rich metadata per artifact param
        output_dir: Path for output artifacts

    Returns:
        Serialized result dictionary
    """
    log.info(
        "Loading provider method: %s.%s.%s", module_path, class_name, function_name
    )
    module = importlib.import_module(module_path)
    provider_class = getattr(module, class_name)
    provider = provider_class()
    provider._tool_config = tool_config or {}

    func = getattr(module, function_name)

    sig = inspect.signature(func)
    has_self = "self" in sig.parameters

    ctx_param_name, artifact_params = _detect_special_params(func)

    kwargs = dict(args)

    if artifact_params:
        artifact_objects = _build_artifact_objects(
            artifact_params, artifact_metadata, args
        )
        kwargs.update(artifact_objects)

    if ctx_param_name:
        kwargs[ctx_param_name] = context

    if "tool_config" in sig.parameters and "tool_config" not in kwargs:
        kwargs["tool_config"] = tool_config

    has_var_keyword = any(
        p.kind == inspect.Parameter.VAR_KEYWORD
        for p in sig.parameters.values()
    )
    if not has_var_keyword:
        valid_params = set(sig.parameters.keys())
        kwargs = {k: v for k, v in kwargs.items() if k in valid_params}

    if has_self:
        if inspect.iscoroutinefunction(func):
            result = await func(provider, **kwargs)
        else:
            result = func(provider, **kwargs)
    else:
        if inspect.iscoroutinefunction(func):
            result = await func(**kwargs)
        else:
            result = func(**kwargs)

    result = _extract_output_artifacts(result, output_dir)

    return serialize_result(result)

def run_init(runner_args: Dict[str, Any]) -> Dict[str, Any]:
    """
    Run a DynamicTool's init() to get enriched metadata.

    This is used by the init protocol to let sandboxed tools provide
    runtime knowledge (like DB schemas) back to the agent. Init runs
    inside the sandbox -- no persistent state is retained after the
    subprocess exits.

    Args:
        runner_args: Dictionary containing:
            - module: Module path
            - class_name: DynamicTool class name
            - tool_config: Tool configuration
            - result_file: Path to write result

    Returns:
        Result dictionary with enriched metadata
    """
    module_path = runner_args["module"]
    class_name = runner_args["class_name"]
    tool_config = runner_args.get("tool_config", {})

    log.info("Running init for DynamicTool: %s.%s", module_path, class_name)

    try:
        module = importlib.import_module(module_path)
        tool_class = getattr(module, class_name)

        tool_instance = tool_class(tool_config=tool_config)

        async def _run_init():
            await tool_instance.init(component=None, tool_config=tool_config)
            try:
                description = tool_instance.tool_description
                _require_description(
                    getattr(tool_instance, "tool_name", class_name), description
                )
                schema = None
                try:
                    raw_schema = tool_instance.parameters_schema
                    if raw_schema is not None:
                        if hasattr(raw_schema, "model_dump"):
                            schema = raw_schema.model_dump(exclude_none=True)
                        elif isinstance(raw_schema, dict):
                            schema = raw_schema
                except Exception as e:
                    log.warning("Failed to read parameters_schema: %s", e)

                ctx_facade_param_name = getattr(
                    tool_instance, "ctx_facade_param_name", None
                )

                return {
                    "tool_description": description,
                    "parameters_schema": schema,
                    "ctx_facade_param_name": ctx_facade_param_name,
                }
            finally:
                try:
                    await tool_instance.cleanup(
                        component=None, tool_config=tool_config
                    )
                except Exception as e:
                    log.warning("cleanup() failed: %s", e)

        result = asyncio.run(_run_init())
        log.info(
            "Init completed: description=%d chars",
            len(result.get("tool_description") or ""),
        )
        return {"result": result}

    except ImportError as e:
        log.error("Failed to import tool module: %s", e)
        return {"error": f"Import error: {e}"}
    except AttributeError as e:
        log.error("Class not found: %s", e)
        return {"error": f"Class not found: {e}"}
    except Exception as e:
        log.exception("Init failed: %s", e)
        tb = traceback.format_exc()
        return {"error": f"Init error: {e}\n{tb}"}

def run_tool(runner_args: Dict[str, Any]) -> Dict[str, Any]:
    """
    Execute a tool with the given arguments.

    Args:
        runner_args: Dictionary containing:
            - module: Module path
            - function: Function name
            - args: Tool arguments
            - tool_config: Tool configuration
            - artifact_paths: Legacy mapping of param names to file paths
            - artifact_metadata: Rich metadata per artifact param
            - status_pipe: Path to status named pipe
            - result_file: Path to write result
            - output_dir: Directory for output artifacts
            - user_id: User ID
            - session_id: Session ID
            - app_name: Application name

    Returns:
        Result dictionary with either 'result' or 'error' key
    """
    module_path = runner_args["module"]
    function_name = runner_args.get("function")
    class_name = runner_args.get("class_name")
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
    from .tool_options import VolumeMount as _VolumeMount
    volume_mounts = [
        _VolumeMount.from_dict(vm) for vm in runner_args.get("volume_mounts", [])
    ]

    log.info(
        "Running tool: %s.%s (user=%s, session=%s)",
        module_path,
        class_name or function_name,
        user_id,
        session_id,
    )

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
        if class_name and function_name:
            result = asyncio.run(
                run_provider_method_async(
                    module_path, class_name, function_name, args, context,
                    tool_config, artifact_metadata, output_dir,
                )
            )
        elif class_name:
            result = asyncio.run(
                run_class_tool_async(
                    module_path, class_name, args, context,
                    tool_config, artifact_metadata, output_dir,
                )
            )
        else:
            result = run_tool_sync(
                module_path, function_name, args, context,
                artifact_metadata, output_dir,
            )
        return {"result": result}

    except ImportError as e:
        log.error("Failed to import tool module: %s", e)
        return {"error": f"Import error: {e}"}

    except AttributeError as e:
        log.error("Tool function/class not found: %s", e)
        return {"error": f"Not found: {e}"}

    except TypeError as e:
        log.error("Tool call error: %s", e)
        return {"error": f"Call error: {e}"}

    except Exception as e:
        log.exception("Tool execution failed: %s", e)
        return execution_failure(e)

def _python_type_to_json_schema(annotation) -> Optional[Dict[str, Any]]:
    """Map a Python type annotation to a JSON Schema type descriptor.

    Thin wrapper over :func:`dynamic_tool._type_to_schema` that preserves
    this module's pre-existing behaviors:

    * String annotations referring to ``ToolResult`` / ``ToolContext`` /
      ``ToolConfig`` are dropped (treated as framework params).
    * ``Artifact`` and ``list[Artifact]`` collapse to ``{type:"string"}``
      (or array-of-strings) instead of recursing.
    * Everything else delegates to the shared recursive walker so
      ``TypedDict`` / ``@dataclass`` / pydantic ``BaseModel`` parameters
      emit ``properties`` + ``required`` rather than a flat
      ``{type:"object"}``.
    """
    from typing import get_args, get_origin

    from .dynamic_tool import _type_to_schema

    if annotation is None or annotation is inspect.Parameter.empty:
        return {"type": "string"}

    if _is_tool_context_facade_param(annotation):
        return None

    if _is_artifact_annotation(annotation):
        return {"type": "string"}

    if isinstance(annotation, str):
        stripped = annotation.strip()
        if "ToolResult" in stripped:
            return None
        if "ToolContext" in stripped or "ToolConfig" in stripped:
            return None
        if stripped == "Artifact":
            return {"type": "string"}

    origin = get_origin(annotation)
    if origin is list:
        args = get_args(annotation)
        if args and _is_artifact_annotation(args[0]):
            return {"type": "array", "items": {"type": "string"}}

    return _type_to_schema(annotation)

_ARGS_HEADERS = ("args:", "arguments:", "parameters:")
_SECTION_HEADERS = ("returns:", "return:", "raises:", "yields:", "notes:", "example:", "examples:")

_CLEAN_ENTRY_HEAD = re.compile(r"\s*[A-Za-z_]\w*\s*$")

_ArgsSection = Tuple[int, int, Dict[int, Optional[str]]]

def _schema_param_names(func) -> Set[str]:
    """Names of the parameters every schema builder (discover_schema, the CLI,
    the dynamic-tool adapter) emits with a docstring description.

    Excluding a name a builder does emit only keeps more text in the
    instructions; including one a builder skips would drop text no parameter
    carries.
    """
    try:
        params = inspect.signature(func).parameters
    except (TypeError, ValueError):
        return set()
    return {
        name
        for name, p in params.items()
        if name not in ("self", "cls", "kwargs", "tool_config", "tool_context")
        and p.kind not in (inspect.Parameter.VAR_POSITIONAL, inspect.Parameter.VAR_KEYWORD)
        and _python_type_to_json_schema(p.annotation) is not None
    }

def _read_args(lines: List[str]) -> Tuple[Dict[str, str], List[_ArgsSection]]:
    """Read the parameter descriptions from a docstring's Args sections.

    Returns the descriptions and, for each Args section, its header line, the
    line that ends it (exclusive) and, for every non-blank line in between, the
    parameter whose final description that line is part of, or None when it
    feeds none: an entry the parser cannot read, a continuation of one, an
    entry whose description the first-colon split cuts short, or an entry that
    a later entry for the same name replaces.
    """
    param_descriptions: Dict[str, str] = {}
    sections: List[_ArgsSection] = []
    in_args = False
    header = 0
    owners: Dict[int, Optional[str]] = {}
    current_param: Optional[str] = None
    current_clean = False
    current_desc_lines: List[str] = []
    entry_lines: List[Tuple[Dict[int, Optional[str]], int]] = []
    saved_entry: Dict[str, List[Tuple[Dict[int, Optional[str]], int]]] = {}

    def save() -> None:
        if not current_param:
            return
        param_descriptions[current_param] = " ".join(current_desc_lines).strip()
        for earlier_owners, line_idx in saved_entry.get(current_param, []):
            earlier_owners[line_idx] = None
        saved_entry[current_param] = entry_lines

    def own(i: int) -> None:
        owners[i] = current_param if current_param and current_clean else None
        if current_param:
            entry_lines.append((owners, i))

    for i, line in enumerate(lines):
        stripped = line.strip()

        if stripped.lower() in _ARGS_HEADERS:
            if in_args:
                sections.append((header, i, owners))
            in_args, header, owners = True, i, {}
            continue
        if in_args and stripped.lower() in _SECTION_HEADERS:
            sections.append((header, i, owners))
            in_args = False
            continue

        if in_args:
            if stripped and not line.startswith("        "):
                save()

                entry_lines = []
                if ":" in stripped:
                    param_part, _, desc_part = stripped.partition(":")
                    param_name = param_part.split("(")[0].strip()
                    if param_name and param_name.isidentifier():
                        current_param = param_name
                        current_clean = bool(_CLEAN_ENTRY_HEAD.match(param_part))
                        current_desc_lines = [desc_part.strip()] if desc_part.strip() else []
                    else:
                        current_param = None
                        current_desc_lines = []
                else:
                    current_param = None
                    current_desc_lines = []
                own(i)
            elif stripped:
                if current_param:
                    current_desc_lines.append(stripped)
                own(i)

    if in_args:
        sections.append((header, len(lines), owners))
    save()

    return param_descriptions, sections

def _carried_args_lines(sections: List[_ArgsSection], schema_params: Set[str]) -> Set[int]:
    """Lines of the Args sections whose every line is part of the description
    of a parameter the schema emits."""
    carried: Set[int] = set()
    for header, end, owners in sections:
        if owners and all(name in schema_params for name in owners.values()):
            carried.update(range(header, end))
    return carried

def _parse_docstring(func) -> Tuple[str, str, Dict[str, str]]:
    """Parse a function's docstring into description, instructions, and param descriptions.

    Returns:
        (description, instructions, param_descriptions)
        - description: first non-empty line
        - instructions: everything after the first paragraph except an Args
          section the parameter descriptions carry line for line, which the
          model would otherwise read twice on every request
        - param_descriptions: dict of param_name -> description from Args section
    """
    doc = inspect.getdoc(func) or ""
    if not doc:
        return "", "", {}

    lines = doc.split("\n")

    description = ""
    first_blank_idx = len(lines)
    for i, line in enumerate(lines):
        stripped = line.strip()
        if not description and stripped:
            description = stripped
        elif description and not stripped:
            first_blank_idx = i
            break

    param_descriptions, args_sections = _read_args(lines)
    carried = _carried_args_lines(args_sections, _schema_param_names(func)) if args_sections else set()
    instructions = "\n".join(
        line for i, line in enumerate(lines) if i >= first_blank_idx and i not in carried
    ).strip()

    return description, instructions, param_descriptions

def _require_description(tool_name: str, description: Optional[str]) -> None:
    """Fail fast when a tool has no LLM-facing description.

    The description is the highest-salience field the LLM uses to choose a tool,
    and strict providers (e.g. Amazon Bedrock's Converse API) reject a tool
    advertised with an empty description, failing the whole request. For a
    function tool the description is the docstring's first line; for a class or
    dynamic tool it is the tool_description property. Enforce it here so the
    author supplies one during development, not at deploy time.
    """
    if not (description and description.strip()):
        raise ValueError(
            f"tool {tool_name!r} has no description; add a docstring (function "
            f"tools) or a non-empty tool_description (class tools) — strict LLM "
            f"providers reject tools advertised with an empty description"
        )

def discover_schema(module_path: str, function_name: str) -> Dict[str, Any]:
    """Discover the schema for a Python tool function by introspection.

    Imports the module, inspects the function signature and docstring,
    and produces a schema dict matching the Go tool --schema format.

    Args:
        module_path: Python module to import (e.g., "python_executor")
        function_name: Function name within the module

    Returns:
        Schema dict with "tools" key containing tool definitions
    """
    from typing import get_origin, get_args, Union

    module = importlib.import_module(module_path)
    func = getattr(module, function_name)
    sig = inspect.signature(func)

    description, instructions, param_descriptions = _parse_docstring(func)
    _require_description(function_name, description)

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

    config_schema = getattr(func, "_config_schema", None)
    if config_schema:
        tool_schema["config_schema"] = config_schema
    timeout_seconds = getattr(func, "_timeout_seconds", None)
    if timeout_seconds:
        tool_schema["timeout_seconds"] = timeout_seconds
    volume_params = getattr(func, "_volume_params", None)
    if volume_params:
        tool_schema["volume_params"] = volume_params

    return {"tools": {function_name: tool_schema}}

def main():
    """
    Main entry point for the tool runner.

    Reads runner arguments from a JSON file and writes result to another JSON file.
    Supports --schema mode for tool discovery.
    """
    if len(sys.argv) >= 2 and sys.argv[1] == "--schema":
        module_path = None
        function_name = None
        i = 2
        while i < len(sys.argv):
            if sys.argv[i] == "--module" and i + 1 < len(sys.argv):
                module_path = sys.argv[i + 1]
                i += 2
            elif sys.argv[i] == "--function" and i + 1 < len(sys.argv):
                function_name = sys.argv[i + 1]
                i += 2
            else:
                i += 1

        if not module_path or not function_name:
            print(
                "Usage: python -m sam_tool_sdk.tool_runner --schema --module <module> --function <function>",
                file=sys.stderr,
            )
            sys.exit(1)

        try:
            schema = discover_schema(module_path, function_name)
            json.dump(schema, sys.stdout, indent=2)
            print()
        except Exception as e:
            print(f"Schema discovery failed: {e}", file=sys.stderr)
            sys.exit(1)
        sys.exit(0)

    if len(sys.argv) < 2:
        print("Usage: python -m sam_tool_sdk.tool_runner <args_file>", file=sys.stderr)
        sys.exit(1)

    args_file = Path(sys.argv[1])

    if not args_file.exists():
        print(f"Error: Arguments file not found: {args_file}", file=sys.stderr)
        sys.exit(1)

    try:
        runner_args = json.loads(args_file.read_text())
    except json.JSONDecodeError as e:
        print(f"Error: Invalid JSON in arguments file: {e}", file=sys.stderr)
        sys.exit(1)

    if runner_args.get("init_mode"):
        result = run_init(runner_args)
    else:
        result = run_tool(runner_args)

    envelope = {"result": result.get("result"), "error": result.get("error", "")}

    result_file = runner_args.get("result_file")
    if result_file:
        Path(result_file).write_text(json.dumps(envelope, default=str))
        log.info("Result written to: %s", result_file)
    else:
        print(json.dumps(envelope, default=str))

if __name__ == "__main__":
    main()
