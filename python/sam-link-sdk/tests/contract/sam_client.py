# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Drives SAM from the outside, as a user or an administrator would: its HTTP API and the `sam task send` CLI.

Every call is bounded. Assertions in the scenarios rest on what these calls return, never on SDK internals.
"""

from __future__ import annotations

import json
import subprocess
import time
import urllib.error
import urllib.request
import uuid
from collections.abc import Callable
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

HTTP_TIMEOUT_SECONDS = 15
TERMINAL_STATES = frozenset({"completed", "failed", "canceled", "cancelled", "error", "timeout", "rejected"})


class SamApiError(AssertionError):
    pass


@dataclass
class Response:
    status: int
    body: Any


@dataclass
class TaskView:
    """What SAM recorded about one task."""

    task_id: str
    status: str
    record: dict
    error_message: str | None
    entries: list[dict] = field(default_factory=list)

    def entry(self, task_id: str | None = None) -> dict:
        wanted = task_id or self.task_id
        for entry in self.entries:
            if entry.get("taskId") == wanted:
                return entry
        raise AssertionError(f"no events recorded for task {wanted}")

    def events(self, task_id: str | None = None) -> list[dict]:
        return self.entry(task_id).get("events") or []

    def signals(self, task_id: str | None = None) -> list[dict]:
        """The data parts (model and tool steps, progress) of the task's status updates, in order."""
        out = []
        for event in self.events(task_id):
            for part in _status_parts(event):
                if part.get("kind") == "data" and isinstance(part.get("data"), dict):
                    out.append(part["data"])
        return out

    def streamed_text(self, task_id: str | None = None) -> list[str]:
        return [
            part["text"]
            for event in self.events(task_id)
            for part in _status_parts(event)
            if part.get("kind") == "text" and isinstance(part.get("text"), str)
        ]

    def final(self, task_id: str | None = None) -> dict:
        """The final response's JSON-RPC payload."""
        finals = [
            e
            for e in self.events(task_id)
            if e.get("direction") in ("task", "error")
            or (
                e.get("direction") != "request"
                and isinstance(e.get("full_payload"), dict)
                and "error" in e["full_payload"]
            )
        ]
        if not finals:
            raise AssertionError(f"SAM recorded no final response for task {task_id or self.task_id}")
        return finals[-1]["full_payload"]

    def answer(self, task_id: str | None = None) -> str:
        result = self.final(task_id).get("result") or {}
        parts = ((result.get("status") or {}).get("message") or {}).get("parts") or []
        return "".join(p.get("text", "") for p in parts if p.get("kind") == "text")


def _status_parts(event: dict) -> list[dict]:
    if event.get("direction") != "status-update":
        return []
    result = (event.get("full_payload") or {}).get("result") or {}
    message = (result.get("status") or {}).get("message") or {}
    return [p for p in message.get("parts") or [] if isinstance(p, dict)]


def wait_until(predicate: Callable[[], Any], timeout: float, what: str, interval: float = 0.25) -> Any:
    deadline = time.monotonic() + timeout
    while True:
        value = predicate()
        if value:
            return value
        if time.monotonic() >= deadline:
            raise AssertionError(f"timed out after {timeout:g} s waiting for {what}")
        time.sleep(interval)


class SamClient:
    def __init__(
        self,
        base_url: str,
        binary: Path | None = None,
        home: Path | None = None,
        workdir: Path | None = None,
        user: str = "",
    ):
        """With `user`, every HTTP request is that user's (SAM's X-SAM-User-ID, honoured with login off); without it,
        SAM's development user's (TEST_USER)."""
        self.base_url = base_url.rstrip("/")
        self.user = user
        self._binary = binary
        self._home = home
        self._workdir = workdir

    def request(self, method: str, path: str, body: Any = None, timeout: float = HTTP_TIMEOUT_SECONDS) -> Response:
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(self.base_url + path, data=data, method=method)
        if data is not None:
            req.add_header("Content-Type", "application/json")
        if self.user:
            req.add_header("X-SAM-User-ID", self.user)
            req.add_header("X-SAM-User-Email", self.user)
        try:
            with urllib.request.urlopen(req, timeout=timeout) as resp:
                return Response(resp.status, _json(resp.read()))
        except urllib.error.HTTPError as exc:
            return Response(exc.code, _json(exc.read()))

    def get(self, path: str) -> Any:
        resp = self.request("GET", path)
        if resp.status != 200:
            raise SamApiError(f"GET {path}: HTTP {resp.status}: {str(resp.body)[:500]}")
        return resp.body

    def agent_cards(self) -> list[dict]:
        return list(self.get("/api/v1/agentCards").get("data") or [])

    def agent_card(self, name: str) -> dict | None:
        return next((c for c in self.agent_cards() if c.get("name") == name), None)

    def wait_listed(self, name: str, timeout: float = 20) -> dict:
        return wait_until(lambda: self.agent_card(name), timeout, f"{name} to be listed")

    def wait_delisted(self, name: str, timeout: float = 20) -> None:
        wait_until(lambda: self.agent_card(name) is None, timeout, f"{name} to leave the agent list")

    def send(self, agent: str, text: str, context_id: str = "") -> str:
        """Submit a chat message to an agent, as the web UI does; returns SAM's task id."""
        resp = self.submit(agent, text, context_id)
        if resp.status != 200 or not isinstance(resp.body, dict) or "result" not in resp.body:
            raise SamApiError(f"submitting a task to {agent}: HTTP {resp.status}: {str(resp.body)[:500]}")
        return resp.body["result"]["id"]

    def submit(self, agent: str, text: str, context_id: str = "") -> Response:
        message: dict[str, Any] = {
            "role": "user",
            "kind": "message",
            "messageId": str(uuid.uuid4()),
            "parts": [{"kind": "text", "text": text}],
            "metadata": {"agent_name": agent},
        }
        if context_id:
            message["contextId"] = context_id
        body = {"jsonrpc": "2.0", "id": str(uuid.uuid4()), "method": "message/stream", "params": {"message": message}}
        return self.request("POST", "/api/v1/message:stream", body)

    def status(self, task_id: str) -> dict | None:
        resp = self.request("GET", f"/api/v1/tasks/{task_id}/status")
        return resp.body if resp.status == 200 and isinstance(resp.body, dict) else None

    def wait_done(self, task_id: str, timeout: float = 30) -> TaskView:
        """Wait for the task to end, then read what SAM recorded: its status and its events.

        The final event and the task row are written by separate paths: read until the final event is there.
        """

        def finished() -> dict | None:
            st = self.status(task_id)
            task = (st or {}).get("task") or {}
            if st and not st.get("isRunning") and str(task.get("status")) in TERMINAL_STATES:
                return st
            return None

        st = wait_until(finished, timeout, f"task {task_id} to finish")
        view = TaskView(task_id, st["task"]["status"], st["task"], st.get("errorMessage"))
        wait_until(lambda: self._has_final(view), 10, f"task {task_id}'s final event to be recorded")
        return view

    def _has_final(self, view: TaskView) -> bool:
        view.entries = self.events(view.task_id)
        try:
            view.final()
            return True
        except AssertionError:
            return False

    def events(self, task_id: str) -> list[dict]:
        return list(self.get(f"/api/v1/tasks/{task_id}/events").get("data") or [])

    def cancel(self, task_id: str) -> Response:
        return self.request("POST", f"/api/v1/tasks/{task_id}:cancel", {})

    def sse(self, task_id: str, timeout: float = 20) -> list[tuple[str, dict]]:
        """Replay the task's event stream (from the first event) until its final event."""
        req = urllib.request.Request(f"{self.base_url}/api/v1/sse/subscribe/{task_id}")
        events: list[tuple[str, dict]] = []
        deadline = time.monotonic() + timeout
        name, data = "", []
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            for raw in resp:
                line = raw.decode("utf-8", errors="replace").rstrip("\r\n")
                if line.startswith("event:"):
                    name = line[6:].strip()
                elif line.startswith("data:"):
                    data.append(line[5:].strip())
                elif not line and data:
                    payload = _json("\n".join(data).encode())
                    events.append((name, payload if isinstance(payload, dict) else {"raw": payload}))
                    if name in ("final_response", "error") or _is_final(payload):
                        break
                    name, data = "", []
                if time.monotonic() > deadline:
                    raise AssertionError(f"SSE for task {task_id} did not end within {timeout:g} s")
        return events

    def cli_send(
        self, agent: str, text: str, outdir: Path, timeout: float = 60, *extra: str
    ) -> subprocess.CompletedProcess:
        return self._cli(["task", "send", text, "-a", agent, *extra], outdir, timeout)

    def cli_answer_approval(
        self, request_file: Path, approve: bool, outdir: Path, timeout: float = 60
    ) -> subprocess.CompletedProcess:
        flag = "--hil-approve" if approve else "--hil-deny"
        return self._cli(["task", "send", flag, str(request_file)], outdir, timeout)

    def cli_answer_questions(
        self, request_file: Path, answers: list[str | list[str]], outdir: Path, timeout: float = 60
    ) -> subprocess.CompletedProcess:
        """Answer a question prompt as the web UI's form posts it: one answer per question, q0, q1, ...; a list
        answers a multi-select question with the labels chosen."""
        answer = json.dumps({"answers": {f"q{i}": a for i, a in enumerate(answers)}})
        return self._cli(["task", "send", "--hil-respond", str(request_file), "--hil-answer", answer], outdir, timeout)

    def _cli(self, args: list[str], outdir: Path, timeout: float) -> subprocess.CompletedProcess:
        if self._binary is None:
            raise RuntimeError("no sam binary for the CLI")
        outdir.mkdir(parents=True, exist_ok=True)
        env = {"PATH": "/usr/bin:/bin", "HOME": str(self._workdir or outdir), "SAM_HOME": str(self._home or outdir)}
        command = [
            str(self._binary),
            *args,
            "--url",
            self.base_url,
            "--output-dir",
            str(outdir),
            "--no-stim",
            "--timeout",
            f"{int(timeout)}s",
            "--open-browser=false",
        ]
        return subprocess.run(
            command,
            capture_output=True,
            text=True,
            timeout=timeout + 15,
            env=env,
            cwd=str(outdir),
            stdin=subprocess.DEVNULL,
        )

    def stop_agent(self, name: str, reason: str) -> Response:
        return self.request("POST", "/api/v1/platform/stoppedAgents", {"agentName": name, "reason": reason})

    def release_agent(self, stop_id: str) -> Response:
        return self.request("DELETE", f"/api/v1/platform/stoppedAgents/{stop_id}")

    def agent_usage(self, name: str) -> Response:
        query = f"usage/agents/byName?name={urllib.request.quote(name)}"
        return self._usage(f"/api/v2/platform/{query}", f"/api/v1/platform/{query}")

    def own_usage(self) -> Response:
        return self._usage("/api/v2/platform/userUsage", "/api/v1/platform/user/usage")

    def _usage(self, v2: str, v1: str) -> Response:
        """SAM serves usage on its v2 platform API, wrapped in `data`, and older builds only on v1, which newer ones
        answer 410; the body is returned as v1 gave it, whichever answered."""
        response = self.request("GET", v2)
        if response.status in (404, 405):
            return self.request("GET", v1)
        body = response.body.get("data", response.body) if isinstance(response.body, dict) else response.body
        return Response(response.status, body)


def _is_final(payload: Any) -> bool:
    result = payload.get("result") if isinstance(payload, dict) else None
    return (isinstance(result, dict) and (result.get("kind") == "task" or result.get("final") is True)) or (
        isinstance(payload, dict) and "error" in payload
    )


def _json(raw: bytes) -> Any:
    try:
        return json.loads(raw) if raw else None
    except ValueError:
        return raw.decode("utf-8", errors="replace")
