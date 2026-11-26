# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""
Example: operator config (config_schema) with tool_cli.

Use ``@with_config_schema`` to declare the config fields the operator must
supply at deploy time (API keys, base URLs, etc.).  The platform renders a
config form, validates required fields, and injects the values into
``tool_config`` before your function runs.  Secret fields are redacted in
the UI and API responses.

Run in schema-discovery mode:
    python config_schema_tool.py --schema

Run with a runner_args.json:
    python config_schema_tool.py runner_args.json

runner_args.json (minimal):
    {
      "args": {"prompt": "What is 2 + 2?"},
      "tool_config": {"api_base": "https://api.example.com", "api_key": "sk-test"},
      "output_dir": "/tmp"
    }
"""

from sam_tool_sdk import ConfigSchemaField, ToolResult, tool_cli, with_config_schema


@with_config_schema([
    ConfigSchemaField(
        key="api_base",
        type="string",
        description="Base URL of the upstream API.",
        required=True,
    ),
    ConfigSchemaField(
        key="api_key",
        type="string",
        description="API secret key.",
        required=True,
        secret=True,
    ),
    ConfigSchemaField(
        key="timeout_seconds",
        type="number",
        description="Request timeout in seconds.",
        required=False,
    ),
])
async def call_api(tool_config, prompt: str) -> ToolResult:
    """Send a prompt to a configurable upstream API.

    Args:
        prompt: the text prompt to send
    """
    # tool_config is injected by the framework; type annotation is omitted
    # intentionally so the parameter is not surfaced in the tool schema.
    api_base = tool_config.get("api_base", "")
    api_key = tool_config.get("api_key", "")
    timeout = float(tool_config.get("timeout_seconds", 30))

    # Real implementation would call the API here.
    # We return a placeholder so this example runs without a live service.
    return ToolResult.ok(
        message=f"Would POST to {api_base}/completions (timeout={timeout}s)",
        data={"prompt": prompt, "api_base": api_base},
    )


# pyproject.toml entry:
#   [project.scripts]
#   call_api = "config_schema_tool:cli"
cli = tool_cli(call_api)


if __name__ == "__main__":
    cli()
