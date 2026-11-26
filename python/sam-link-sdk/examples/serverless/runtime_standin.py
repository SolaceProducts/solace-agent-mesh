# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""A stand-in for a request-driven agent runtime (AWS AgentCore Runtime, Azure AI Foundry hosted agents): nothing
runs until its invocation endpoint is called, and every call starts a fresh agent process.

    python runtime_standin.py <agent script> [--port 9310]

POST /invocations is the wake SAM's agent link entrypoint sends when a request waits for the agent (the agent's
`wake` in the entrypoint's configuration, here http://localhost:<port>/invocations). The endpoint answers 202 at once
and starts `<agent script> wake`, which connects back over the agent link, serves what waits and exits, so a long
task never holds the entrypoint's HTTP call open. The body of the POST is not read: everything the agent serves comes
over the agent link.
"""

from __future__ import annotations

import argparse
import logging
import subprocess
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

log = logging.getLogger("runtime")


def spawn(script: str, mode: str) -> int:
    return subprocess.Popen([sys.executable, script, mode], stdin=subprocess.DEVNULL).pid


def handler(script: str) -> type[BaseHTTPRequestHandler]:
    class Invocations(BaseHTTPRequestHandler):
        def do_POST(self):  # noqa: N802 (http.server's naming)
            self.rfile.read(int(self.headers.get("Content-Length") or 0))
            if self.path.split("?")[0].rstrip("/") != "/invocations":
                self.send_response(404)
                self.end_headers()
                return
            pid = spawn(script, "wake")
            log.info("wake: started agent process pid=%d", pid)
            self.send_response(202)
            self.send_header("Content-Length", "0")
            self.end_headers()

        def log_message(self, *_):
            pass

    return Invocations


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("script")
    parser.add_argument("--port", type=int, default=9310)
    args = parser.parse_args()
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(name)s %(message)s")
    log.info("runtime listening on :%d for %s", args.port, args.script)
    ThreadingHTTPServer(("127.0.0.1", args.port), handler(args.script)).serve_forever()


if __name__ == "__main__":
    main()
