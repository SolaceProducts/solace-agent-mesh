# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Unit tests for the in-sandbox IPC client and context.call_llm wiring."""

from __future__ import annotations

import json
import os
import socket
import tempfile
import threading
from typing import List

import pytest

from sam_tool_sdk.context_facade import SandboxToolContextFacade
from sam_tool_sdk.ipc import IPCClient, IPCError

def _make_facade(**overrides) -> SandboxToolContextFacade:
    tmp = tempfile.mkdtemp()
    kwargs = {
        "status_pipe_path": os.path.join(tmp, "pipe"),
        "tool_config": {},
        "artifacts": {},
        "output_dir": tmp,
        "user_id": "u1",
        "session_id": "s1",
        "app_name": "app",
    }
    kwargs.update(overrides)
    return SandboxToolContextFacade(**kwargs)

def test_call_llm_without_socket_raises() -> None:
    facade = _make_facade()
    with pytest.raises(IPCError, match="no IPC connection"):
        facade.call_llm("system", "user")

def test_call_llm_with_broken_socket_path_degrades_gracefully(capsys) -> None:
    facade = _make_facade(ipc_socket_path="/tmp/sam-tool-sdk-nonexistent-socket")
    captured = capsys.readouterr()
    assert "IPC connection failed" in captured.err
    with pytest.raises(IPCError, match="no IPC connection"):
        facade.call_llm("system", "user")

def _serve_one_request(socket_path: str, response: dict, received: List[bytes]) -> threading.Thread:
    """Spin up a Unix-socket server that accepts one connection, reads one
    newline-terminated request, writes the given response, and exits."""
    sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    sock.bind(socket_path)
    sock.listen(1)

    def serve() -> None:
        try:
            conn, _ = sock.accept()
            with conn:
                buf = b""
                while b"\n" not in buf:
                    chunk = conn.recv(4096)
                    if not chunk:
                        return
                    buf += chunk
                line, _, _ = buf.partition(b"\n")
                received.append(line)
                conn.sendall((json.dumps(response) + "\n").encode("utf-8"))
        finally:
            sock.close()

    t = threading.Thread(target=serve, daemon=True)
    t.start()
    return t

def test_ipc_client_call_llm_roundtrip() -> None:
    tmp = tempfile.mkdtemp()
    socket_path = os.path.join(tmp, "ipc.sock")

    received: List[bytes] = []
    server = _serve_one_request(
        socket_path,
        response={
            "jsonrpc": "2.0",
            "id": "1",
            "result": {"text": "hello from llm"},
        },
        received=received,
    )

    client = IPCClient(socket_path)
    try:
        text = client.call_llm("you are helpful", "say hello", temperature=0.3)
    finally:
        client.close()
    server.join(timeout=2.0)

    assert text == "hello from llm"

    sent = json.loads(received[0])
    assert sent["jsonrpc"] == "2.0"
    assert sent["method"] == "llm/call"
    assert sent["params"] == {
        "system_prompt": "you are helpful",
        "user_prompt": "say hello",
        "temperature": 0.3,
    }

def test_ipc_client_propagates_error_envelope() -> None:
    tmp = tempfile.mkdtemp()
    socket_path = os.path.join(tmp, "ipc.sock")

    server = _serve_one_request(
        socket_path,
        response={
            "jsonrpc": "2.0",
            "id": "1",
            "error": {"code": -32603, "message": "internal error"},
        },
        received=[],
    )

    client = IPCClient(socket_path)
    try:
        with pytest.raises(IPCError, match="internal error"):
            client.call_llm("sys", "user")
    finally:
        client.close()
    server.join(timeout=2.0)

def test_facade_call_llm_delegates_to_ipc_client() -> None:
    tmp = tempfile.mkdtemp()
    socket_path = os.path.join(tmp, "ipc.sock")

    server = _serve_one_request(
        socket_path,
        response={"jsonrpc": "2.0", "id": "1", "result": {"text": "ok"}},
        received=[],
    )

    facade = _make_facade(ipc_socket_path=socket_path)
    try:
        text = facade.call_llm("s", "u")
    finally:
        if facade._ipc_client is not None:
            facade._ipc_client.close()
    server.join(timeout=2.0)

    assert text == "ok"
