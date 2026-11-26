# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""
Example: typed numeric parameters with tool_cli.

This is the canonical single-function pattern (README "Quick Start").
Declare Python types on the parameters — the SDK turns them into the JSON
Schema the LLM sees and coerces any string-encoded values back to the right
type before your function runs.  So if the LLM emits ``top_n: "5"`` you
still receive ``int(5)`` — no defensive casting needed.

Run in schema-discovery mode:
    python numeric_params_tool.py --schema

Run with a runner_args.json:
    python numeric_params_tool.py runner_args.json

runner_args.json (minimal):
    {
      "args": {"values": [4, 8, 15, 16, 23], "top_n": "3"},
      "output_dir": "/tmp"
    }
"""

from sam_tool_sdk import ToolResult, tool_cli


async def top_n_average(values: list, top_n: int, scale: float = 1.0) -> ToolResult:
    """Compute the average of the top-N values in a list.

    Args:
        values: list of numbers to rank
        top_n: how many top values to include in the average
        scale: multiply the result by this factor (default 1.0)
    """
    # top_n is declared int — coercion guarantees it even when the LLM
    # sends "3" as a JSON string.
    if top_n <= 0:
        return ToolResult.error("top_n must be positive")

    sorted_vals = sorted(values, reverse=True)
    selected = sorted_vals[:top_n]
    avg = (sum(selected) / len(selected)) * scale

    return ToolResult.ok(
        message=f"Average of top {top_n} values: {avg}",
        data={"average": avg, "selected": selected, "top_n": top_n},
    )


# Expose as a console script.
# pyproject.toml entry:
#   [project.scripts]
#   top_n_average = "numeric_params_tool:cli"
cli = tool_cli(top_n_average)


if __name__ == "__main__":
    cli()
