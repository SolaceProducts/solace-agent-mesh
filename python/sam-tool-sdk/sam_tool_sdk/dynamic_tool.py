# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""
Standalone base classes for SAM dynamic tools.

This module provides DynamicTool, DynamicToolProvider, and the register_tool
decorator for defining Python tools that run in the SAM Secure Tool Runtime
(STR). It is a self-contained replacement for the versions in
``solace_agent_mesh.agent.tools.dynamic_tool`` -- no google.adk, google.genai,
or broker dependencies.

Key differences from the solace_agent_mesh version:
- ``parameters_schema`` returns a **JSON Schema dict**, not ``google.genai.types.Schema``
- No embed resolution (handled by tool_runner / the agent before invocation)
- No artifact pre-loading in ``run_async`` (handled by tool_runner)
- No ToolContextFacade injection in ``run_async`` (handled by tool_runner)
"""

import dataclasses
import inspect
import logging
from abc import ABC, abstractmethod
from typing import (
    Any,
    Callable,
    Dict,
    List,
    Literal,
    Optional,
    Set,
    Type,
    Union,
    get_args,
    get_origin,
    get_type_hints,
)

try:
    from typing import is_typeddict  # type: ignore[attr-defined]
except ImportError:
    def is_typeddict(_obj: Any) -> bool:  # type: ignore[no-redef]
        return False

from .artifact_types import ArtifactTypeInfo, get_artifact_info
from .context_facade import SandboxToolContextFacade

log = logging.getLogger(__name__)

def _resolve_pydantic_basemodel() -> Optional[type]:
    """Return ``pydantic.BaseModel`` when pydantic is installed, else ``None``.

    Looked up lazily so the SDK keeps working in environments without
    pydantic; the schema walker only branches on this when callers
    actually use a pydantic model as a tool parameter.
    """
    try:
        from pydantic import BaseModel  # type: ignore
        return BaseModel
    except ImportError:
        return None

def _type_to_schema(annotation: Any) -> Optional[Dict[str, Any]]:
    """Recursively translate a Python type annotation to a JSON Schema dict.

    Mirrors the Go SDK's ``goTypeToSchema`` (`pkg/samtoolsdk/schema.go`)
    so nested ``TypedDict`` / ``@dataclass`` / pydantic ``BaseModel``
    params emit ``{type:"object", properties:{...}, required:[...]}``
    instead of the previous flat ``{type:"object"}``.

    Returns ``None`` for framework-injected params (ToolContextFacade)
    that should be excluded from the schema. Unresolvable annotations
    fall back to ``{type:"string"}`` — same default the Go reflection
    walker uses for unknown reflect kinds.
    """
    if annotation is None or annotation is inspect.Parameter.empty:
        return {"type": "string"}

    if _is_tool_context_facade_param(annotation):
        return None

    if annotation is str:
        return {"type": "string"}
    if annotation is int:
        return {"type": "integer"}
    if annotation is float:
        return {"type": "number"}
    if annotation is bool:
        return {"type": "boolean"}
    if annotation is bytes:
        return {"type": "string"}
    if annotation is Any:
        return {"type": "string"}

    if isinstance(annotation, str):
        return {"type": "string"}

    origin = get_origin(annotation)
    args = get_args(annotation)

    if origin is Union:
        non_none = [a for a in args if a is not type(None)]
        if len(non_none) == 1:
            return _type_to_schema(non_none[0])
        return {"type": "string"}

    if origin is list or annotation is list:
        if args:
            inner = _type_to_schema(args[0]) or {"type": "string"}
            return {"type": "array", "items": inner}
        return {"type": "array", "items": {"type": "string"}}

    if origin is dict or annotation is dict:
        return {"type": "object"}

    if is_typeddict(annotation):
        return _typeddict_to_schema(annotation)

    if dataclasses.is_dataclass(annotation) and isinstance(annotation, type):
        return _dataclass_to_schema(annotation)

    pydantic_base = _resolve_pydantic_basemodel()
    if pydantic_base is not None and isinstance(annotation, type) and issubclass(annotation, pydantic_base):
        return _pydantic_to_schema(annotation)

    return {"type": "string"}

def _typeddict_to_schema(td: Any) -> Dict[str, Any]:
    """Translate a ``TypedDict`` class into a nested object schema.

    ``required`` preserves the declaration order from ``__annotations__``
    so the wire shape stays byte-identical to Go's ``schemaFromStruct``,
    which appends required field names in struct-declaration order.
    """
    try:
        hints = get_type_hints(td)
    except Exception:
        hints = getattr(td, "__annotations__", {}) or {}
    properties: Dict[str, Any] = {}
    for key, hint in hints.items():
        sub = _type_to_schema(hint)
        if sub is not None:
            properties[key] = sub
    required_set = set(getattr(td, "__required_keys__", set(hints.keys())))
    required = [name for name in hints.keys() if name in required_set]
    schema: Dict[str, Any] = {"type": "object", "properties": properties}
    if required:
        schema["required"] = required
    return schema

def _dataclass_to_schema(dc: type) -> Dict[str, Any]:
    """Translate a ``@dataclass`` class into a nested object schema."""
    properties: Dict[str, Any] = {}
    required: List[str] = []
    try:
        hints = get_type_hints(dc)
    except Exception:
        hints = {}
    for f in dataclasses.fields(dc):
        hint = hints.get(f.name, f.type)
        sub = _type_to_schema(hint)
        if sub is None:
            continue
        properties[f.name] = sub
        if f.default is dataclasses.MISSING and f.default_factory is dataclasses.MISSING:  # type: ignore[misc]
            required.append(f.name)
    schema: Dict[str, Any] = {"type": "object", "properties": properties}
    if required:
        schema["required"] = required
    return schema

def _pydantic_to_schema(model: type) -> Dict[str, Any]:
    """Translate a pydantic ``BaseModel`` subclass into a nested schema."""
    fields = getattr(model, "model_fields", None) or getattr(model, "__fields__", {})
    properties: Dict[str, Any] = {}
    required: List[str] = []
    try:
        hints = get_type_hints(model)
    except Exception:
        hints = {}
    for name, info in fields.items():
        hint = hints.get(name) or getattr(info, "annotation", None)
        sub = _type_to_schema(hint)
        if sub is None:
            continue
        properties[name] = sub
        is_required = getattr(info, "is_required", None)
        if callable(is_required):
            if is_required():
                required.append(name)
        elif getattr(info, "required", False):
            required.append(name)
    schema: Dict[str, Any] = {"type": "object", "properties": properties}
    if required:
        schema["required"] = required
    return schema

def _is_tool_context_facade_param(annotation) -> bool:
    """Check if *annotation* represents a ToolContextFacade / SandboxToolContextFacade."""
    if annotation is None:
        return False
    if annotation is SandboxToolContextFacade:
        return True
    if isinstance(annotation, str):
        return "ToolContextFacade" in annotation
    name = getattr(annotation, "__name__", "") or ""
    qualname = getattr(annotation, "__qualname__", "") or ""
    return name in ("ToolContextFacade", "SandboxToolContextFacade", "ToolContext") or qualname in (
        "ToolContextFacade",
        "SandboxToolContextFacade",
        "ToolContext",
    )

class _SchemaDetectionResult:
    """Accumulates special-parameter detection during schema generation."""

    __slots__ = ("schema", "artifact_params", "ctx_facade_param_name")

    def __init__(self):
        self.schema: Optional[dict] = None
        self.artifact_params: Dict[str, ArtifactTypeInfo] = {}
        self.ctx_facade_param_name: Optional[str] = None

    @property
    def artifact_args(self) -> Set[str]:
        return set(self.artifact_params.keys())

def _get_schema_from_signature(
    func: Callable,
    artifact_args: Optional[Set[str]] = None,
    detection_result: Optional[_SchemaDetectionResult] = None,
) -> dict:
    """Introspect *func*'s signature and return a JSON Schema dict.

    Mirrors the logic of the ADK version but produces plain dicts instead of
    ``google.genai.types.Schema`` objects. The wire shape matches the Go
    SDK: required fields go in the ``required`` array, optional fields are
    simply absent from it — no ``nullable`` marker (Go's reflection schema
    does not emit one, and the platform parser ignores extras anyway).
    Per-param descriptions are extracted from the Args section of the
    function's docstring.
    """
    from .tool_runner import _parse_docstring

    sig = inspect.signature(func)
    properties: Dict[str, dict] = {}
    required: List[str] = []
    _description, _instructions, param_descriptions = _parse_docstring(func)

    for param in sig.parameters.values():
        if param.name in ("tool_context", "tool_config", "kwargs", "self", "cls"):
            continue

        param_type = param.annotation
        is_optional = False

        origin = get_origin(param_type)
        args = get_args(param_type)
        if origin is Union and type(None) in args:
            is_optional = True
            param_type = next((t for t in args if t is not type(None)), Any)

        if _is_tool_context_facade_param(param_type):
            if detection_result is not None:
                detection_result.ctx_facade_param_name = param.name
            continue

        original_annotation = param.annotation
        artifact_info = get_artifact_info(original_annotation)

        if artifact_info.is_artifact:
            if artifact_args is not None:
                artifact_args.add(param.name)
            if detection_result is not None:
                detection_result.artifact_params[param.name] = artifact_info

            if artifact_info.is_list:
                properties[param.name] = {
                    "type": "array",
                    "items": {"type": "string"},
                }
            else:
                properties[param.name] = {"type": "string"}
        else:
            schema_for_param = _type_to_schema(param_type) or {"type": "string"}
            properties[param.name] = schema_for_param

        desc = param_descriptions.get(param.name)
        if desc:
            properties[param.name]["description"] = desc

        if param.default is inspect.Parameter.empty and not is_optional:
            required.append(param.name)

    schema = {"type": "object", "properties": properties}
    if required:
        schema["required"] = required
    return schema

def _coerce_scalar(value: Any, json_type: str) -> Any:
    """Coerce a string *value* to the JSON-schema scalar *json_type*.

    Only strings are coerced; any other type is returned unchanged. An
    unparseable string is also returned unchanged so the tool surfaces its
    own domain error rather than a coercion crash. ``boolean`` is
    special-cased because Python's ``bool("false")`` is ``True``.
    """
    if not isinstance(value, str):
        return value
    try:
        if json_type == "integer":
            return int(value)
        if json_type == "number":
            return float(value)
        if json_type == "boolean":
            lowered = value.strip().lower()
            if lowered in ("true", "1"):
                return True
            if lowered in ("false", "0"):
                return False
    except (ValueError, TypeError):
        return value
    return value

def _coerce_args_to_schema(
    args: Dict[str, Any], schema: Optional[Dict[str, Any]]
) -> Dict[str, Any]:
    """Coerce string-valued *args* to the scalar types declared in *schema*.

    LLM providers (notably Anthropic and Vertex) routinely emit numeric and
    boolean arguments as JSON strings even when the tool's schema declares
    ``integer`` / ``number`` / ``boolean``. The Go SDK never sees this
    because it unmarshals into a typed struct; the Python dispatch paths
    passed the raw kwargs straight through, so a tool doing
    ``top_keywords > 0`` blew up on ``"5" > 0``. Coercing here gives every
    dispatch path the typed value the schema promised.

    Only top-level scalar params are coerced; objects, arrays, and keys
    absent from the schema pass through untouched.
    """
    if not isinstance(args, dict) or not isinstance(schema, dict):
        return args
    props = schema.get("properties")
    if not isinstance(props, dict):
        return args
    coerced = dict(args)
    for key, value in args.items():
        prop = props.get(key)
        if not isinstance(prop, dict):
            continue
        json_type = prop.get("type")
        if isinstance(json_type, str):
            coerced[key] = _coerce_scalar(value, json_type)
    return coerced

class DynamicTool(ABC):
    """Base class for dynamic tools that define their own names, descriptions,
    and parameter schemas programmatically.

    Unlike the ``solace_agent_mesh`` version, this does **not** inherit from
    ``google.adk.tools.BaseTool``.  The ``parameters_schema`` property must
    return a **JSON Schema dict** (not ``google.genai.types.Schema``).

    Example::

        from sam_tool_sdk import DynamicTool, ToolResult

        class MyTool(DynamicTool):
            @property
            def tool_name(self): return "my_tool"

            @property
            def tool_description(self): return "Does something useful"

            @property
            def parameters_schema(self):
                return {
                    "type": "object",
                    "properties": {
                        "input": {"type": "string", "description": "The input text"},
                    },
                    "required": ["input"],
                }

            async def _run_async_impl(self, args, tool_context=None, credential=None):
                return ToolResult.ok(f"Processed: {args['input']}").model_dump()
    """

    config_model: Optional[Type] = None

    def __init__(self, tool_config: Optional[Union[dict, Any]] = None):
        self.tool_config = tool_config or {}

    async def init(self, component=None, tool_config=None) -> None:
        """Optional async initialization called once at startup."""
        pass

    async def cleanup(self, component=None, tool_config=None) -> None:
        """Optional async cleanup called once at shutdown."""
        pass

    @property
    @abstractmethod
    def tool_name(self) -> str:
        """Return the function name that the LLM will call."""
        ...

    @property
    @abstractmethod
    def tool_description(self) -> str:
        """Return the description of what this tool does."""
        ...

    @property
    @abstractmethod
    def parameters_schema(self) -> dict:
        """Return a JSON Schema dict defining the tool's parameters.

        Example::

            {
                "type": "object",
                "properties": {
                    "query": {"type": "string", "description": "Search query"},
                    "limit": {"type": "integer", "description": "Max results"},
                },
                "required": ["query"],
            }
        """
        ...

    @property
    def raw_string_args(self) -> List[str]:
        """Argument names that should not have embeds resolved."""
        return []

    @property
    def resolution_type(self) -> Literal["early", "all"]:
        """Determines which embeds to resolve. Defaults to 'early'."""
        return "early"

    @property
    def artifact_args(self) -> List[str]:
        """Argument names whose values should be pre-loaded as Artifact objects."""
        return []

    @property
    def artifact_params(self) -> Dict[str, ArtifactTypeInfo]:
        """Detailed info about artifact parameters (including is_list/is_optional).

        Default implementation creates basic info from :attr:`artifact_args`.
        """
        return {name: ArtifactTypeInfo(is_artifact=True) for name in self.artifact_args}

    @property
    def ctx_facade_param_name(self) -> Optional[str]:
        """Parameter name that should receive a ToolContextFacade, or ``None``."""
        return None

    @property
    def auth_config(self) -> Optional[Dict[str, Any]]:
        """Return auth requirements for this tool, or ``None`` if no auth needed.

        When set, the framework manages credential lookup, token injection,
        and the OAuth flow. Deployers merge their credentials with this config.

        Example for OAuth2::

            {
                "type": "oauth2",
                "scheme": {
                    "authorization_url": "https://accounts.example.com/authorize",
                    "token_url": "https://accounts.example.com/token",
                    "scopes": ["read", "write"],
                }
            }

        Example for bearer::

            {"type": "bearer"}
        """
        return None

    @property
    def config_schema(self) -> Optional[List[Dict[str, Any]]]:
        """Declare config parameters this tool needs from operators.

        Returns a list of field descriptors, or ``None`` if no operator
        config is required::

            [
                {"key": "api_key", "type": "string", "required": True, "secret": True},
                {"key": "base_url", "type": "string", "required": False},
            ]
        """
        return None

    @property
    def timeout_seconds(self) -> Optional[int]:
        """Tool execution timeout, in seconds.

        When set, the value is emitted in the ``--schema`` output as
        ``timeout_seconds``. It is a default: a ``timeout_seconds`` on the
        tool's manifest entry wins, and this value applies only when the
        manifest is silent. Mirrors the Go SDK's ``WithTimeout`` option.
        Return ``None`` (the default) to declare no timeout.
        """
        return None

    @property
    def effect(self) -> Optional[str]:
        """Whether the tool is read_only or side_effecting.

        A tool is read_only only if all three hold: it cannot change anything outside the run, whatever its input; it fetches no URL chosen by its input or by the content of a file it reads; it writes, deletes or replaces no artifact under a filename chosen by its caller or by the tool itself. The run is the one task that called the tool, and changing something outside it means writing, sending, deleting, posting, publishing or indexing in an external system, another session or user scope, or starting a job. Changing the run's own session, such as the skills it has loaded, is not outside it. A filename is chosen by the caller when it comes from a parameter, and by the tool when its code fixes or derives it, including a name set on a data object it returns. A result the runtime itself saves under a fresh filename it picks, which it does only when a tool returns data with no name set (auto-disposition), is not a write. Sending the caller's query to one fixed external service, as a search or lookup does, is not fetching a chosen URL. Every other tool is side_effecting.

        Return ``"read_only"`` or ``"side_effecting"`` (``EFFECT_READ_ONLY`` /
        ``EFFECT_SIDE_EFFECTING``); emitted as ``effect`` in the ``--schema``
        output. Mirrors the Go SDK's ``WithEffect`` option. Return ``None``
        (the default) to declare nothing, which the agent treats as
        side-effecting.
        """
        return None

    @property
    def volume_params(self) -> Optional[List[Any]]:
        """Declare volume parameters this tool requires at runtime.

        Returns a list of ``VolumeParam`` instances (or pre-serialized
        dicts) describing each volume the tool needs. Emitted as
        ``volume_params`` in the ``--schema`` output so the platform
        provisions and mounts them before invocation. Mirrors the Go SDK's
        ``WithVolumeParams`` option. Return ``None`` for tools that don't
        need volumes.
        """
        return None

    def dynamic_schema(
        self, config: Dict[str, Any]
    ) -> Optional[Dict[str, Any]]:
        """Optionally override schema fields based on a per-deployment config.

        Invoked by the CLI when launched with ``--schema --config <file>``.
        Return ``None`` to keep the static schema. To override, return a
        dict containing any subset of ``name``, ``description``,
        ``instructions``, ``parameters``, and ``config_schema``; absent
        keys are kept from the static schema.

        Mirrors the Go SDK's ``WithDynamicSchema`` + ``SchemaOverride``.
        """
        return None

    @abstractmethod
    async def _run_async_impl(
        self, args: dict, tool_context=None, credential: Optional[str] = None
    ) -> dict:
        """Implement the actual tool logic. Must return a dict response."""
        ...

class _FunctionAsDynamicTool(DynamicTool):
    """Wraps a decorated function (from ``@register_tool``) as a DynamicTool."""

    def __init__(
        self,
        func: Callable,
        tool_config: Optional[Union[dict, Any]] = None,
        provider_instance: Optional[Any] = None,
    ):
        super().__init__(tool_config=tool_config)
        self._func = func
        self._provider_instance = provider_instance

        self._detection_result = _SchemaDetectionResult()
        self._schema = _get_schema_from_signature(
            func, detection_result=self._detection_result
        )

        self._is_instance_method = False
        sig = inspect.signature(self._func)
        if sig.parameters:
            first_param = next(iter(sig.parameters.values()))
            if first_param.name == "self":
                self._is_instance_method = True

    @property
    def tool_name(self) -> str:
        return self._func.__name__

    @property
    def tool_description(self) -> str:
        return inspect.getdoc(self._func) or ""

    @property
    def parameters_schema(self) -> dict:
        return self._schema

    @property
    def artifact_args(self) -> List[str]:
        return list(self._detection_result.artifact_args)

    @property
    def artifact_params(self) -> Dict[str, ArtifactTypeInfo]:
        return self._detection_result.artifact_params

    @property
    def ctx_facade_param_name(self) -> Optional[str]:
        return self._detection_result.ctx_facade_param_name

    @property
    def auth_config(self) -> Optional[Dict[str, Any]]:
        if self._provider_instance and hasattr(self._provider_instance, "auth_config"):
            return self._provider_instance.auth_config
        return None

    @property
    def config_schema(self) -> Optional[List[Dict[str, Any]]]:
        if hasattr(self._func, "_config_schema"):
            return self._func._config_schema
        if self._provider_instance and hasattr(self._provider_instance, "config_schema"):
            return self._provider_instance.config_schema
        return None

    def dynamic_schema(
        self, config: Dict[str, Any]
    ) -> Optional[Dict[str, Any]]:
        fn = getattr(self._func, "_dynamic_schema", None)
        if fn is None and self._provider_instance is not None:
            fn = getattr(self._provider_instance, "dynamic_schema", None)
            if fn is not None:
                return fn(config)
            return None
        if fn is None:
            return None
        return fn(config)

    async def _run_async_impl(self, args: dict, tool_context=None, credential=None) -> dict:
        sig = inspect.signature(self._func)
        if "tool_context" in sig.parameters:
            args["tool_context"] = tool_context
        if "tool_config" in sig.parameters:
            args["tool_config"] = self.tool_config

        if self._provider_instance and self._is_instance_method:
            return await self._func(self._provider_instance, **args)
        else:
            return await self._func(**args)

class DynamicToolProvider(ABC):
    """Base class for providers that generate multiple tools from a single
    configuration block.

    Use the ``@register_tool`` class-method decorator to register async
    functions as tools::

        from sam_tool_sdk import DynamicToolProvider

        class MyProvider(DynamicToolProvider):
            @MyProvider.register_tool
            async def greet(self, name: str) -> dict:
                \"\"\"Returns a greeting.\"\"\"
                return {"text": f"Hello, {name}!"}

            def create_tools(self, tool_config=None):
                return []
    """

    config_model: Optional[Type] = None
    _decorated_tools: List[Callable] = []

    @property
    def auth_config(self) -> Optional[Dict[str, Any]]:
        """Return auth requirements shared by all tools in this provider.

        Individual tools can override via their own ``auth_config`` property.
        See :attr:`DynamicTool.auth_config` for format details.
        """
        return None

    @property
    def config_schema(self) -> Optional[List[Dict[str, Any]]]:
        """Declare config parameters shared by all tools in this provider.

        Individual tools can override via their own ``config_schema`` property.
        See :attr:`DynamicTool.config_schema` for format details.
        """
        return None

    def dynamic_schema(
        self, config: Dict[str, Any]
    ) -> Optional[Dict[str, Any]]:
        """Provider-wide dynamic schema override; defaults to no-op.

        Used by tools created via ``@register_tool`` that don't carry
        their own ``@with_dynamic_schema``. Mirrors
        :meth:`DynamicTool.dynamic_schema`.
        """
        return None

    @classmethod
    def register_tool(cls, func: Callable) -> Callable:
        """Decorator to register an async function as a tool."""
        if (
            not hasattr(cls, "_decorated_tools")
            or cls._decorated_tools is DynamicToolProvider._decorated_tools
        ):
            cls._decorated_tools = []
        cls._decorated_tools.append(func)
        return func

    def _create_tools_from_decorators(
        self, tool_config: Optional[Union[dict, Any]] = None
    ) -> List[DynamicTool]:
        """Convert decorated functions into DynamicTool instances."""
        tools = []
        for func in self._decorated_tools:
            adapter = _FunctionAsDynamicTool(func, tool_config, provider_instance=self)
            tools.append(adapter)
        return tools

    def get_all_tools_for_framework(
        self, tool_config: Optional[Union[dict, Any]] = None
    ) -> List[DynamicTool]:
        """Return all tools: decorated + custom from :meth:`create_tools`."""
        return self._create_tools_from_decorators(tool_config) + self.create_tools(tool_config)

    @abstractmethod
    def create_tools(self, tool_config: Optional[Union[dict, Any]] = None) -> List[DynamicTool]:
        """Return a list of custom DynamicTool instances.

        Tools registered with ``@register_tool`` are included automatically;
        only return *additional* programmatically-created tools here.
        """
        ...
