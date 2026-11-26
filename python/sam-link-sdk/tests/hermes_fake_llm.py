# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""A scripted OpenAI-compatible chat-completions server, for driving a Hermes agent offline.

Each turn is ``{"text": str, "reasoning": str, "calls": [(id, name, args)], "text_after_calls": str, "usage": (input,
output), "status": int, "hold": threading.Event, "reached": threading.Event}``. ``text_after_calls`` is text the
response writes after its tool calls (streamed after their deltas). A turn with a ``status`` fails the request with that
HTTP status; one with ``hold`` waits for the event before it answers. Every request body is kept in ``requests``. With `route` (a request body -> a key),
`turns` maps each key to its own script, so agents that call the model concurrently (a parent and its subagent) each
get theirs.
"""

from __future__ import annotations

import json
import re
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


class FakeLLM:
    def __init__(self, turns, route=None):
        self.turns = {key: list(script) for key, script in turns.items()} if route else list(turns)
        self.requests: list[dict] = []
        self._lock = threading.Lock()
        outer = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_GET(self):
                self.send_response(404)
                self.end_headers()

            def do_POST(self):
                """Hermes probes the endpoint for model facts; that is not a scripted turn."""
                body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))) or b"{}")
                if "messages" not in body:
                    self.send_response(404)
                    self.end_headers()
                    return
                with outer._lock:
                    outer.requests.append(body)
                    script = outer.turns.setdefault(route(body), []) if route else outer.turns
                    turn = script.pop(0) if script else {"text": "(no more scripted turns)"}
                if turn.get("reached") is not None:
                    turn["reached"].set()
                if turn.get("hold") is not None:
                    turn["hold"].wait(30)
                if turn.get("status"):
                    error = {"message": turn.get("message", "scripted failure"), "type": "api_error"}
                    if turn.get("code"):
                        error["code"] = turn["code"]
                    payload = json.dumps({"error": error}).encode()
                    self.send_response(turn["status"])
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(payload)))
                    self.end_headers()
                    self.wfile.write(payload)
                    return
                if body.get("stream"):
                    outer._stream(self, body, turn)
                else:
                    outer._respond(self, body, turn)

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.base_url = f"http://127.0.0.1:{self.server.server_address[1]}/v1"

    def close(self):
        self.server.shutdown()
        self.server.server_close()

    @staticmethod
    def _usage(turn):
        i, o = turn.get("usage", (10, 2))
        return {"prompt_tokens": i, "completion_tokens": o, "total_tokens": i + o}

    @staticmethod
    def _calls(turn):
        return [
            {"index": n, "id": cid, "type": "function", "function": {"name": name, "arguments": json.dumps(args)}}
            for n, (cid, name, args) in enumerate(turn.get("calls", []))
        ]

    def _respond(self, handler, body, turn):
        message = {"role": "assistant", "content": turn.get("text", "") + turn.get("text_after_calls", "")}
        if turn.get("reasoning"):
            message["reasoning_content"] = turn["reasoning"]
        calls = self._calls(turn)
        if calls:
            message["tool_calls"] = [{k: v for k, v in c.items() if k != "index"} for c in calls]
        payload = json.dumps(
            {
                "id": "chatcmpl-fake",
                "object": "chat.completion",
                "created": int(time.time()),
                "model": "fake-model",
                "choices": [
                    {
                        "index": 0,
                        "message": message,
                        "finish_reason": turn.get("finish") or ("tool_calls" if calls else "stop"),
                    }
                ],
                "usage": self._usage(turn),
            }
        ).encode()
        handler.send_response(200)
        handler.send_header("Content-Type", "application/json")
        handler.send_header("Content-Length", str(len(payload)))
        handler.end_headers()
        handler.wfile.write(payload)

    def _stream(self, handler, body, turn):
        handler.send_response(200)
        handler.send_header("Content-Type", "text/event-stream")
        handler.end_headers()

        def send(delta, finish=None, usage=None, choices=True):
            chunk = {
                "id": "chatcmpl-fake",
                "object": "chat.completion.chunk",
                "created": int(time.time()),
                "model": "fake-model",
                "choices": [{"index": 0, "delta": delta, "finish_reason": finish}] if choices else [],
            }
            if usage:
                chunk["usage"] = usage
            handler.wfile.write(b"data: " + json.dumps(chunk).encode() + b"\n\n")
            handler.wfile.flush()

        send({"role": "assistant", "content": ""})
        if turn.get("reasoning"):
            send({"reasoning_content": turn["reasoning"]})
        for piece in re.findall(r"\S+\s*|\s+", turn.get("text", "")):
            send({"content": piece})
        calls = self._calls(turn)
        for call in calls:
            arguments = call["function"]["arguments"]
            half = len(arguments) // 2
            opening = {**call, "function": {"name": call["function"]["name"], "arguments": ""}}
            send({"tool_calls": [opening]})
            for part in (arguments[:half], arguments[half:]):
                send({"tool_calls": [{"index": call["index"], "function": {"arguments": part}}]})
        for piece in re.findall(r"\S+\s*|\s+", turn.get("text_after_calls", "")):
            send({"content": piece})
        send({}, finish=turn.get("finish") or ("tool_calls" if calls else "stop"))
        if (body.get("stream_options") or {}).get("include_usage"):
            send({}, usage=self._usage(turn), choices=False)
        handler.wfile.write(b"data: [DONE]\n\n")
        handler.wfile.flush()
