# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""
Example: multi-tool provider with provider_cli.

One executable — one schema output — multiple tools.  This is the preferred
pattern for related tools that share config, helpers, or state (e.g. all
tools in a "math" or "data" skill).

Register async functions as tools with ``@MyProvider.register_tool`` AFTER the
class body (the class must be bound before its classmethod can decorate).
``provider_cli`` routes each invocation to the right tool based on
``tool_name`` in the argsfile.

Run in schema-discovery mode (lists ALL tools in one JSON blob):
    python multi_tool_provider.py --schema

Run a specific tool:
    python multi_tool_provider.py runner_args.json
    # runner_args.json must contain  "tool_name": "add"  (or "stats")

runner_args.json for "add":
    {
      "tool_name": "add",
      "args": {"a": "3.5", "b": "1.5"},
      "output_dir": "/tmp"
    }

runner_args.json for "stats":
    {
      "tool_name": "stats",
      "args": {"values": [1, 2, 3, 4, 5], "precision": "2"},
      "output_dir": "/tmp"
    }
"""

from sam_tool_sdk import DynamicToolProvider, ToolResult, provider_cli


class MathTools(DynamicToolProvider):
    """A provider that exposes simple arithmetic tools."""

    def create_tools(self, tool_config=None):
        # Tools are declared via @MathTools.register_tool below; there are no
        # additional programmatic DynamicTool instances to create here.
        return []


# register_tool turns an async function into a discoverable tool. It must be
# applied AFTER the class body — decorating inside the body raises NameError
# because the class name isn't bound yet. Type annotations on parameters
# become the JSON Schema the LLM sees; numeric params (float, int) are coerced
# from whatever string the LLM sends.

@MathTools.register_tool
async def add(self, a: float, b: float) -> dict:
    """Add two numbers.

    Args:
        a: first operand
        b: second operand
    """
    return ToolResult.ok(message=f"{a} + {b} = {a + b}", data={"result": a + b}).model_dump()


@MathTools.register_tool
async def stats(self, values: list, precision: int = 2) -> dict:
    """Compute basic statistics for a list of numbers.

    Args:
        values: list of numeric values
        precision: decimal places in the result (default 2)
    """
    if not values:
        return ToolResult.error("values list must not be empty").model_dump()

    n = len(values)
    mean = sum(values) / n
    sorted_v = sorted(values)
    median = sorted_v[n // 2] if n % 2 else (sorted_v[n // 2 - 1] + sorted_v[n // 2]) / 2

    return ToolResult.ok(
        message=f"Stats over {n} values",
        data={
            "mean": round(mean, precision),
            "median": round(float(median), precision),
            "min": min(values),
            "max": max(values),
            "count": n,
        },
    ).model_dump()


# pyproject.toml entry:
#   [project.scripts]
#   math_tools = "multi_tool_provider:cli"
cli = provider_cli(MathTools)


if __name__ == "__main__":
    cli()
