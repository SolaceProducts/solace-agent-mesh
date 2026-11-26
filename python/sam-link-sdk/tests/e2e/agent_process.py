# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Runs one claims-triage example as its own process, exactly as a developer would (``python
examples/claims_triage_<framework>.py``), joined to the SAM under test through the agent link.

Nothing in the example is patched: it reads its settings from an environment built here from scratch (PATH, a HOME
under the test's temporary directory, the link settings, the CLAIMS_* settings), so none of the developer's own keys
reach it. Its output is kept in a log file, shown on failure with the link token and the model key removed.
"""

from __future__ import annotations

import os
import signal
import subprocess
from dataclasses import dataclass, field
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
EXAMPLES = REPO_ROOT / "examples"
STOP_GRACE_SECONDS = 15
LOG_TAIL_CHARS = 6000
PASSTHROUGH_ENV = ("PATH", "TMPDIR", "LANG", "LC_ALL", "SYSTEMROOT")
MODEL_API_KEY = "e2e-model-key-not-real"
"""Not a real key: the scripted model accepts anything, and the scrub below proves the log never shows it anyway."""


@dataclass
class AgentProcess:
    """One example agent: its process, its environment and its log."""

    name: str
    example: str
    python: str
    root: Path
    env: dict[str, str]
    secrets: tuple[str, ...] = ()
    args: tuple[str, ...] = ()
    process: subprocess.Popen | None = field(default=None, repr=False)

    @property
    def log_path(self) -> Path:
        return self.root / f"{self.name}.log"

    def start(self) -> None:
        self.root.mkdir(parents=True, exist_ok=True)
        home = self.root / f"{self.name}-home"
        home.mkdir(exist_ok=True)
        env = {k: os.environ[k] for k in PASSTHROUGH_ENV if k in os.environ}
        env.update(HOME=str(home), PYTHONPATH=str(REPO_ROOT / "src"), PYTHONDONTWRITEBYTECODE="1", PYTHONUNBUFFERED="1")
        env.update(self.env)
        with open(self.log_path, "wb") as log:
            self.process = subprocess.Popen(
                [self.python, str(EXAMPLES / self.example), *self.args],
                cwd=str(home),
                env=env,
                stdout=log,
                stderr=subprocess.STDOUT,
                stdin=subprocess.DEVNULL,
                start_new_session=True,
            )

    def alive(self) -> bool:
        return self.process is not None and self.process.poll() is None

    def stop(self) -> None:
        """SIGTERM the example's own process group, then SIGKILL after a grace period."""
        proc, self.process = self.process, None
        if proc is None or proc.poll() is not None:
            return
        self._signal(proc, signal.SIGTERM)
        try:
            proc.wait(timeout=STOP_GRACE_SECONDS)
        except subprocess.TimeoutExpired:
            self._signal(proc, signal.SIGKILL)
            proc.wait(timeout=STOP_GRACE_SECONDS)

    @staticmethod
    def _signal(proc: subprocess.Popen, sig: int) -> None:
        try:
            os.killpg(proc.pid, sig)
        except ProcessLookupError:
            pass

    def log_tail(self) -> str:
        try:
            text = self.log_path.read_text(errors="replace")[-LOG_TAIL_CHARS:]
        except OSError:
            return "(no agent log)"
        for value in self.secrets:
            if value:
                text = text.replace(value, "<redacted>")
        return text
