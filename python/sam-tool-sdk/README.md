# SAM Tool SDK

Python SDK for building tools that run in [Solace Agent Mesh](https://github.com/SolaceProducts/solace-agent-mesh) (SAM).

## What it does

Write a Python function (or class) and expose it as a SAM tool. Type
annotations become the tool's parameter schema, the docstring supplies the
descriptions the LLM sees, and you return structured results and artifacts.
The SAM runtime discovers the tool and invokes it per call. The only
dependency is `pydantic`.

## Installation

```bash
pip install sam-tool-sdk
```

## Quick Start

### Function-based tool with `tool_cli`

The canonical pattern: author a function, wrap it with `tool_cli`, and expose
the resulting callable as a console script via `pyproject.toml`.

```python
# greet_tool.py
from sam_tool_sdk import tool_cli, ToolResult, SandboxToolContextFacade


async def greet(name: str, excited: bool = False, ctx: SandboxToolContextFacade = None) -> ToolResult:
    """Greet someone by name.

    Args:
        name: who to greet
        excited: add an exclamation mark
    """
    if ctx is not None:
        ctx.send_status(f"Greeting {name}...")
    suffix = "!" if excited else "."
    return ToolResult.ok(message=f"Hello, {name}{suffix}", data={"greeted": name})


cli = tool_cli(greet)
```

```toml
# pyproject.toml
[project]
name = "sam-tool-greet"
version = "0.1.0"
dependencies = ["sam-tool-sdk>=0.1,<0.2"]

[project.scripts]
greet = "greet_tool:cli"
```

Parameter type annotations (`str`, `bool`, `int`, `Artifact`,
`SandboxToolContextFacade`, …) and the docstring's `Args:` block produce the
JSON Schema the LLM sees. The `SandboxToolContextFacade`-annotated parameter is
injected at runtime; everything else becomes a tool argument. Besides
`send_status`, artifacts and config, the facade exposes the caller's identity:
`ctx.user_id`, `ctx.session_id`, `ctx.app_name`, `ctx.task_id`, and
`ctx.user_profile` (the gateway-forwarded profile as a dict, empty when none).
The profile is unsigned, so use it to scope lookups and attribute audit
records, not as the sole basis for an authorization decision.

### Class-based tool (DynamicTool)

For more control over the tool's schema and behavior:

```python
from sam_tool_sdk import DynamicTool, ToolResult

class WordCounter(DynamicTool):
    @property
    def tool_name(self):
        return "count_words"

    @property
    def tool_description(self):
        return "Counts the number of words in the given text."

    @property
    def parameters_schema(self):
        return {
            "type": "object",
            "properties": {
                "text": {"type": "string", "description": "The text to count words in"},
            },
            "required": ["text"],
        }

    async def _run_async_impl(self, args, tool_context=None, credential=None):
        count = len(args["text"].split())
        return ToolResult.ok(f"Found {count} words", data={"count": count}).model_dump()
```

### Provider-based tools (DynamicToolProvider)

When you want to define multiple related tools from a single class:

```python
from sam_tool_sdk import DynamicToolProvider

class MathTools(DynamicToolProvider):
    @MathTools.register_tool
    async def add(self, a: float, b: float) -> dict:
        """Add two numbers."""
        return {"text": str(a + b)}

    @MathTools.register_tool
    async def multiply(self, a: float, b: float) -> dict:
        """Multiply two numbers."""
        return {"text": str(a * b)}

    def create_tools(self, tool_config=None):
        return []  # All tools defined via @register_tool
```

### Declaring operator config (config_schema)

Tools that need operator-supplied configuration (API keys, endpoints, default
channels, …) declare it with `@with_config_schema` so the platform can render a
config form, validate per-agent overrides, and enforce required fields at
deploy time.

```python
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
    ConfigSchemaField(key="api_url", type="string"),
])
async def post_message(tool_config, channel: str, text: str):
    token = tool_config["slack_bot_token"]
    ...
```

Values are resolved at deploy time with precedence: agent override > toolset
default > schema default > 422 if a required field is unset. Fields marked
`secret=True` are redacted on API responses and rendered with a password input
in the UI.

For `DynamicTool` subclasses, override the `config_schema` property to return a
list of `ConfigSchemaField(...).to_dict()` entries.

### Working with artifacts

Tools can receive and produce artifacts using type annotations:

```python
from sam_tool_sdk import Artifact, ToolResult, DataObject, DataDisposition

async def summarize_file(doc: Artifact) -> ToolResult:
    """Summarize the contents of an uploaded document."""
    text = doc.as_text()
    summary = text[:200] + "..."  # placeholder for real summarization

    return ToolResult.ok(
        message=f"Summarized {doc.filename}",
        data={"summary": summary},
        data_objects=[
            DataObject(
                name="summary.txt",
                content=summary,
                mime_type="text/plain",
                disposition=DataDisposition.ARTIFACT,
                description="Document summary",
            )
        ],
    )
```

### Per-tool sandbox options

Decorate a `tool_cli` function (or override the matching `DynamicTool`
properties) to declare timeouts and volume requirements honored at execution
time:

```python
from sam_tool_sdk import tool_cli, tool_timeout, with_volume_params, VolumeParam, ToolResult


@tool_timeout(seconds=120)
@with_volume_params([
    VolumeParam(name="workspace", mount_path="/workspace", mode="readwrite"),
])
async def transform(input_path: str, ctx=None) -> ToolResult:
    """Long-running transformation that needs scratch space."""
    ...

cli = tool_cli(transform)
```

`tool_timeout` overrides the per-invocation timeout; `with_volume_params`
declares mount points provisioned before the tool runs (paths surface via
`ctx.volume_mounts` / `ctx.get_volume_mount_path("/workspace")`, keyed by mount
path).

### Declaring a tool's effect

Declare whether each tool is read_only or side_effecting:

```python
from sam_tool_sdk import tool_cli, tool_effect, EFFECT_READ_ONLY, ToolResult


@tool_effect(EFFECT_READ_ONLY)
async def lookup_order(order_id: str, ctx=None) -> ToolResult:
    """Look up an order by id."""
    ...

cli = tool_cli(lookup_order)
```

A tool is read_only only if all three hold: it cannot change anything outside the run, whatever its input; it fetches no URL chosen by its input or by the content of a file it reads; it writes, deletes or replaces no artifact under a filename chosen by its caller or by the tool itself. The run is the one task that called the tool, and changing something outside it means writing, sending, deleting, posting, publishing or indexing in an external system, another session or user scope, or starting a job. Changing the run's own session, such as the skills it has loaded, is not outside it. A filename is chosen by the caller when it comes from a parameter, and by the tool when its code fixes or derives it, including a name set on a data object it returns. A result the runtime itself saves under a fresh filename it picks, which it does only when a tool returns data with no name set (auto-disposition), is not a write. Sending the caller's query to one fixed external service, as a search or lookup does, is not fetching a chosen URL. Every other tool is side_effecting. A
`DynamicTool` returns either value from its `effect` property. It is emitted as
`effect` in the `--schema` output; a tool that declares nothing is treated as
side-effecting, and any other value raises `ValueError`.

## Packaging

A SAM tool ships as an
[AWS-Lambda-Layer-style](https://docs.aws.amazon.com/lambda/latest/dg/configuration-layers.html)
directory: a `python/bin/<console-script>` entry point plus all dependencies
pre-extracted under `python/`. Build that layout with a single
`pip install --target` invocation:

```bash
pip install --target dist/python \
    --platform manylinux2014_x86_64 \
    --python-version 3.13 \
    --only-binary=:all: \
    .
```

The directory then contains `dist/python/bin/<your-script>`,
`dist/python/sam_tool_sdk/`, `dist/python/pydantic/`, and your own package.
Pair it with a `manifest.yaml` declaring `executable: python/bin/<your-script>`.

## License

Apache-2.0
