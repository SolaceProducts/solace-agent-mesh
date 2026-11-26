#!/usr/bin/env python3
"""
Standalone test Python tool for STR integration tests.

This script mimics what the sam_tool_sdk tool_runner does: reads runner_args.json,
executes tool logic, writes status updates to the named pipe, and writes result.json.

It does NOT depend on the sam_tool_sdk package.

Supports tools dispatched by the 'function' or 'tool_name' field:
  - echo: echoes a message with optional prefix from config
  - process_file: reads an input artifact, uppercases it, writes output artifact
  - error_tool: always returns an error result
  - multi_artifact: processes multiple input files

Also supports --schema to output JSON tool metadata for STR discovery.
"""

import json
import os
import sys
from pathlib import Path


# --- Schema output for STR discovery ---

TOOL_SCHEMA = {
    "tools": {
        "echo": {
            "description": "Echoes a message with optional prefix",
            "parameters": {
                "type": "object",
                "properties": {
                    "message": {
                        "type": "string",
                        "description": "Message to echo",
                    },
                    "prefix": {
                        "type": "string",
                        "description": "Optional prefix",
                    },
                },
                "required": ["message"],
            },
            "instructions": "Use this tool to echo messages",
        },
        "process_file": {
            "description": "Processes an input file",
            "parameters": {
                "type": "object",
                "properties": {
                    "input": {
                        "type": "string",
                        "description": "Input file to process",
                    },
                },
                "required": ["input"],
            },
            "artifact_params": {
                "input": {"is_list": False, "is_optional": False},
            },
        },
        "error_tool": {
            "description": "Always returns an error",
            "parameters": {
                "type": "object",
                "properties": {
                    "message": {
                        "type": "string",
                        "description": "Error message",
                    },
                },
                "required": ["message"],
            },
        },
        "multi_artifact": {
            "description": "Processes multiple files",
            "parameters": {
                "type": "object",
                "properties": {
                    "files": {
                        "type": "string",
                        "description": "Multiple input files",
                    },
                },
                "required": ["files"],
            },
            "artifact_params": {
                "files": {"is_list": True, "is_optional": False},
            },
        },
        "authed_tool": {
            "description": "Tool requiring OAuth2 authentication",
            "parameters": {
                "type": "object",
                "properties": {
                    "query": {
                        "type": "string",
                        "description": "Query to execute",
                    },
                },
                "required": ["query"],
            },
            "auth": {
                "type": "oauth2",
                "scheme": {
                    "authorization_url": "https://auth.example.com/authorize",
                    "token_url": "https://auth.example.com/token",
                    "scopes": ["read", "write"],
                },
            },
        },
    }
}


def write_status(status_pipe, message):
    """Write a status update to the named pipe."""
    if not status_pipe:
        return
    try:
        with open(status_pipe, "w") as f:
            f.write(json.dumps({"status": message}) + "\n")
            f.flush()
    except OSError:
        pass


def echo_tool(args, tool_config, runner_args):
    """Echo tool: echoes message with optional prefix."""
    write_status(runner_args.get("status_pipe"), "Echoing message...")

    message = args.get("message", "")
    prefix = args.get("prefix", tool_config.get("default_prefix", ""))

    if prefix:
        message = f"{prefix}: {message}"

    write_status(runner_args.get("status_pipe"), "Done")

    return {
        "_schema": "ToolResult",
        "_schema_version": "1.0",
        "status": "success",
        "message": message,
        "data": {
            "user_id": runner_args.get("user_id", ""),
            "session_id": runner_args.get("session_id", ""),
            "app_name": runner_args.get("app_name", ""),
        },
        "data_objects": [],
        "error_code": None,
    }


def process_file_tool(args, tool_config, runner_args):
    """Process file tool: reads artifact, uppercases, writes output."""
    artifact_paths = runner_args.get("artifact_paths", {})
    artifact_meta = runner_args.get("artifact_metadata", {})
    output_dir = runner_args.get("output_dir", "")

    input_path = artifact_paths.get("input")
    if not input_path:
        return {
            "_schema": "ToolResult",
            "_schema_version": "1.0",
            "status": "error",
            "message": "No input artifact provided",
            "data": {},
            "data_objects": [],
            "error_code": "MISSING_ARTIFACT",
        }

    input_meta = artifact_meta.get("input", {})
    filename = input_meta.get("filename", "unknown")

    write_status(runner_args.get("status_pipe"), f"Processing file: {filename}")

    content = Path(input_path).read_text()
    processed = content.upper()

    # Write output artifact to output dir.
    output_name = f"processed_{filename}"
    if output_dir:
        output_path = os.path.join(output_dir, output_name)
        Path(output_path).write_text(processed)

    return {
        "_schema": "ToolResult",
        "_schema_version": "1.0",
        "status": "success",
        "message": f"Processed {filename} ({len(content)} bytes -> {len(processed)} bytes)",
        "data": {
            "input_filename": filename,
            "input_mime_type": input_meta.get("mime_type", ""),
            "output_filename": output_name,
        },
        "data_objects": [
            {
                "name": output_name,
                "content": None,  # Content written to file, STR collects it
                "is_binary": False,
                "mime_type": input_meta.get("mime_type", "text/plain"),
                "disposition": "artifact",
                "description": f"Processed version of {filename}",
                "preview": None,
                "metadata": {},
            }
        ],
        "error_code": None,
    }


def error_tool(args, tool_config, runner_args):
    """Error tool: always returns an error result."""
    message = args.get("message", "An error occurred")
    return {
        "_schema": "ToolResult",
        "_schema_version": "1.0",
        "status": "error",
        "message": message,
        "data": {},
        "data_objects": [],
        "error_code": "TEST_ERROR",
    }


def multi_artifact_tool(args, tool_config, runner_args):
    """Multi-artifact tool: processes multiple input files."""
    artifact_paths = runner_args.get("artifact_paths", {})
    artifact_meta = runner_args.get("artifact_metadata", {})

    write_status(runner_args.get("status_pipe"), "Processing multiple files...")

    # For list artifacts, paths/meta come as dicts with numeric keys.
    files_paths = artifact_paths.get("files", {})
    files_meta = artifact_meta.get("files", {})

    names = []
    total_size = 0
    if isinstance(files_paths, dict):
        for idx in sorted(files_paths.keys(), key=str):
            path = files_paths[idx]
            meta = files_meta.get(idx, {}) if isinstance(files_meta, dict) else {}
            content = Path(path).read_bytes()
            names.append(meta.get("filename", f"file_{idx}"))
            total_size += len(content)

    return {
        "_schema": "ToolResult",
        "_schema_version": "1.0",
        "status": "success",
        "message": f"Processed {len(names)} files, total {total_size} bytes",
        "data": {
            "filenames": names,
            "file_count": len(names),
            "total_size": total_size,
        },
        "data_objects": [],
        "error_code": None,
    }


def authed_tool(args, tool_config, runner_args):
    """Authed tool: requires an OAuth token to execute."""
    token = runner_args.get("tool_oauth_token", "")
    if not token:
        return {
            "_schema": "ToolResult",
            "_schema_version": "1.0",
            "status": "error",
            "message": "No auth token available",
            "data": {},
            "data_objects": [],
            "error_code": "AUTH_REQUIRED",
        }
    query = args.get("query", "")
    return {
        "_schema": "ToolResult",
        "_schema_version": "1.0",
        "status": "success",
        "message": f"Executed with auth: {query}",
        "data": {"has_token": True},
        "data_objects": [],
        "error_code": None,
    }


TOOLS = {
    "echo": echo_tool,
    "process_file": process_file_tool,
    "error_tool": error_tool,
    "multi_artifact": multi_artifact_tool,
    "authed_tool": authed_tool,
}


def main():
    # Support --schema flag for STR discovery.
    if len(sys.argv) >= 2 and sys.argv[1] == "--schema":
        json.dump(TOOL_SCHEMA, sys.stdout, indent=2)
        print()
        sys.exit(0)

    if len(sys.argv) < 2:
        print("Usage: tool.py /path/to/runner_args.json | --schema", file=sys.stderr)
        sys.exit(1)

    args_file = Path(sys.argv[1])
    runner_args = json.loads(args_file.read_text())

    # Support both function (Python SDK convention) and tool_name (Go SDK convention).
    function_name = runner_args.get("function") or runner_args.get("tool_name", "")
    args = runner_args.get("args", {})
    tool_config = runner_args.get("tool_config", {})
    result_file = runner_args.get("result_file")

    try:
        handler = TOOLS.get(function_name)
        if handler is None:
            output = {"result": None, "error": f"Unknown function: {function_name}"}
        else:
            result = handler(args, tool_config, runner_args)
            output = {"result": result, "error": ""}
    except Exception as e:
        output = {"result": None, "error": str(e)}

    if result_file:
        Path(result_file).write_text(json.dumps(output, default=str))
    else:
        print(json.dumps(output, default=str))


if __name__ == "__main__":
    main()
