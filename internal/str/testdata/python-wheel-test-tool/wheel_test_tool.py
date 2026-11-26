"""Minimal test tool using sam_tool_sdk for wheel pipeline testing."""

from sam_tool_sdk import tool_cli, ToolResult


async def wheel_echo(
    message: str,
    tool_context: "SandboxToolContextFacade" = None,
) -> "ToolResult":
    """Echoes a message back. Used to test wheel-based tool installation.

    Args:
        message: The message to echo back.
        tool_context: Injected by STR tool_runner (unused).

    Returns:
        ToolResult with the echoed message.
    """
    return ToolResult.ok(f"wheel-echo: {message}")


cli = tool_cli(wheel_echo)
