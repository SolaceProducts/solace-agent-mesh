# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""IPC client for in-sandbox LLM callbacks.

Mirrors the Go SDK's pkg/samtoolsdk/ipc.go. Talks JSON-RPC 2.0 over a
Unix domain socket to the STR worker. Newline-delimited framing, one
request at a time per client. The STR opens the socket and writes the
path into runner_args.json's "ipc_socket" field; the tool connects on
first call and reuses the connection.
"""

from __future__ import annotations

import json
import logging
import socket
import threading
from typing import Optional


log = logging.getLogger(__name__)


class IPCError(Exception):
    """Raised when the STR returns a JSON-RPC error or the socket fails."""


class IPCClient:
    """Talks JSON-RPC 2.0 to the STR over a Unix domain socket.

    Lazy connect: the socket is opened on the first request. A threading
    lock serializes requests so the newline-delimited framing stays in
    lockstep. Mirrors the Go ipcClient — same wire shape, same one-at-a-time
    discipline.
    """

    def __init__(self, socket_path: str) -> None:
        self._socket_path = socket_path
        self._lock = threading.Lock()
        self._conn: Optional[socket.socket] = None
        self._reader_buf = b""
        self._next_id = 0

    def _ensure_connected(self) -> socket.socket:
        if self._conn is None:
            sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
            try:
                sock.connect(self._socket_path)
            except OSError as e:
                sock.close()
                raise IPCError(
                    f"connecting to IPC socket {self._socket_path}: {e}"
                ) from e
            self._conn = sock
        return self._conn

    def _read_line(self, conn: socket.socket) -> bytes:
        while b"\n" not in self._reader_buf:
            chunk = conn.recv(4096)
            if not chunk:
                raise IPCError("IPC socket closed before response was received")
            self._reader_buf += chunk
        line, _, rest = self._reader_buf.partition(b"\n")
        self._reader_buf = rest
        return line

    def call(self, method: str, params: dict) -> dict:
        """Send a JSON-RPC request, return the result dict.

        Raises IPCError on transport failure or if the server returned an
        error envelope.
        """
        with self._lock:
            self._next_id += 1
            req_id = str(self._next_id)
            req = {
                "jsonrpc": "2.0",
                "id": req_id,
                "method": method,
                "params": params,
            }
            payload = (json.dumps(req) + "\n").encode("utf-8")

            conn = self._ensure_connected()
            try:
                conn.sendall(payload)
            except OSError as e:
                raise IPCError(f"writing IPC request: {e}") from e

            try:
                line = self._read_line(conn)
            except OSError as e:
                raise IPCError(f"reading IPC response: {e}") from e

            try:
                resp = json.loads(line)
            except json.JSONDecodeError as e:
                raise IPCError(f"unmarshaling IPC response: {e}") from e

            err = resp.get("error")
            if err is not None:
                code = err.get("code", 0)
                message = err.get("message", "")
                raise IPCError(f"IPC error {code}: {message}")

            result = resp.get("result")
            if not isinstance(result, dict):
                raise IPCError(
                    f"IPC response missing dict result: {resp!r}"
                )
            return result

    def call_llm(
        self,
        system_prompt: str,
        user_prompt: str,
        temperature: float = 0.7,
    ) -> str:
        """Invoke the STR's LLM callback. Returns the generated text."""
        result = self.call(
            "llm/call",
            {
                "system_prompt": system_prompt,
                "user_prompt": user_prompt,
                "temperature": temperature,
            },
        )
        text = result.get("text")
        if not isinstance(text, str):
            raise IPCError(f"llm/call result missing text field: {result!r}")
        return text

    def close(self) -> None:
        with self._lock:
            if self._conn is not None:
                try:
                    self._conn.close()
                except OSError:
                    pass
                self._conn = None
                self._reader_buf = b""
