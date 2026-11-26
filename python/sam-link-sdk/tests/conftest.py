# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

from __future__ import annotations

import importlib.util
import json
import logging
import os
import pathlib
import re
import shutil
import sys
import tempfile

import pytest

REAL_SAM_LINK_SDK_DIR = pathlib.Path(os.path.expanduser("~")) / ".sam_link_sdk"
"""Every test runs with HOME in a temporary directory of the session, set here (before any test module imports a framework
that resolves its storage under HOME when imported): SAM's stop saved under the default state_dir, a framework's own
storage (CrewAI under Library/Application Support) and anything else keyed on HOME stay out of the developer's home. A
test that needs a HOME of its own still sets one.
"""
SESSION_HOME = tempfile.mkdtemp(prefix="sam-link-sdk-tests-home-")
os.environ["HOME"] = SESSION_HOME

os.environ["OPENAI_AGENTS_DISABLE_TRACING"] = "true"
"""The OpenAI Agents SDK exports a trace of every Runner.run it was not told to skip (an Agent.as_tool's own run, a test's
bare Runner.run) to OpenAI over the network; the SDK reads this switch once, on first use.
"""


def _sam_link_sdk_dir_state() -> dict[str, float]:
    try:
        return {entry.name: entry.stat().st_mtime for entry in REAL_SAM_LINK_SDK_DIR.iterdir()}
    except OSError:
        return {}


_REAL_SAM_LINK_SDK_BEFORE = _sam_link_sdk_dir_state()


TEST_STOP_FILE = re.compile(r"^((127\.0\.0\.1|localhost|__1)_(3[2-9]\d{3}|[4-6]\d{4})__.*|recording)\.stop\.json$")
"""What the tests could write under the real ~/.sam_link_sdk, new or changed: SAM's stop for an agent on a loopback entrypoint
at an ephemeral port (the unit tests' link entrypoints and the contract suite's SAM bind port 0), or under the test
vehicle's key. Anything else there belongs to an agent running on this machine, which may write it at any time.
"""


def _written_by_tests(before: dict[str, float], after: dict[str, float]) -> list[str]:
    return sorted(name for name, mtime in after.items() if before.get(name) != mtime and TEST_STOP_FILE.match(name))


def pytest_sessionfinish(session, exitstatus):
    """Fail the run if it wrote what only the tests would write under the real ~/.sam_link_sdk."""
    shutil.rmtree(SESSION_HOME, ignore_errors=True)
    written = _written_by_tests(_REAL_SAM_LINK_SDK_BEFORE, _sam_link_sdk_dir_state())
    if written:
        print(f"\nERROR: the tests wrote under the real {REAL_SAM_LINK_SDK_DIR}: {', '.join(written)}", file=sys.stderr)
        session.exitstatus = pytest.ExitCode.TESTS_FAILED


CELL_ENV = "SAM_COMPAT_CELL"
"""A compatibility cell (compat/frameworks.json, cells) installs one framework alone. The modules every adapter shares
(conformance, drift) would otherwise run the other adapters' cases there too, against whatever release of their
framework came in as a dependency, or none, so only the cell's own adapters' cases run."""


def _adapter_id(value: object) -> str:
    return str(value).rsplit(".", 1)[-1].replace("-", "_")


def _names(framework: str, ids: set[str]) -> bool:
    """A case id names an adapter alone or as its first part (``otel_strands``: OpenTelemetry over Strands)."""
    return any(framework == i or framework.startswith(i + "_") for i in ids)


def pytest_collection_modifyitems(config, items):
    cell = os.environ.get(CELL_ENV, "").strip()
    if not cell:
        return
    sources = json.loads((pathlib.Path(__file__).resolve().parents[1] / "compat" / "frameworks.json").read_text())
    if cell not in sources["cells"]:
        raise pytest.UsageError(f"{CELL_ENV}={cell!r}: expected one of {', '.join(sources['cells'])}")
    adapters = {name: _adapter_id(f["adapter"]) for name, f in sources["frameworks"].items() if f.get("adapter")}
    known = set(adapters.values()) | {_adapter_id(name) for name in sources["cells"]}
    own = {adapters[extra] for extra in sources["cells"][cell]["extras"] if extra in adapters} | {_adapter_id(cell)}
    kept, deselected = [], []
    for item in items:
        params = getattr(getattr(item, "callspec", None), "params", {})
        framework = _adapter_id(params.get("adapter", params.get("runtime", "")))
        (deselected if _names(framework, known) and not _names(framework, own) else kept).append(item)
    if deselected:
        config.hook.pytest_deselected(items=deselected)
        items[:] = kept


@pytest.fixture
def openai_rate_limited():
    """A 429 response to build an openai error from, made with the HTTP library the installed openai builds its errors
    with: httpx before openai 3, httpx2 from it. A framework's lowest supported release allows either."""
    from openai import _exceptions

    http = getattr(_exceptions, "httpx2", None) or _exceptions.httpx
    return http.Response(429, request=http.Request("POST", "http://x/v1/chat/completions"), json={})


@pytest.fixture(autouse=True)
def logging_as_it_was():
    """Some frameworks configure logging for the whole process when they first run (Hermes sets the root level and
    quiets libraries such as websockets; the Foundry host adds a console handler and an OpenTelemetry one to the root
    logger). Put every logger's level back after each test, and take the handlers a test left on the root logger off
    it, so a framework one test used does not decide what another test's caplog sees, or write to a stream pytest has
    closed by the time the process exits.

    pytest's own capture handlers are left alone: it swaps them in and out around each phase of a test, so they are its
    to remove.
    """

    def loggers():
        return {
            name: logger
            for name, logger in logging.root.manager.loggerDict.items()
            if isinstance(logger, logging.Logger)
        }

    root_level, root_handlers = logging.root.level, list(logging.root.handlers)
    before = {name: (logger.level, logger.disabled, logger.propagate) for name, logger in loggers().items()}
    yield
    logging.root.setLevel(root_level)
    for handler in logging.root.handlers[:]:
        if handler not in root_handlers and not type(handler).__module__.startswith("_pytest"):
            logging.root.removeHandler(handler)
    for name, logger in loggers().items():
        level, disabled, propagate = before.get(name, (logging.NOTSET, False, True))
        logger.setLevel(level)
        logger.disabled = disabled
        logger.propagate = propagate


EXAMPLES = pathlib.Path(__file__).resolve().parents[1] / "examples"


def load_example(name: str):
    """Import examples/<name>.py as a module, as `python examples/<name>.py` would (examples/ on the path)."""
    sys.path.insert(0, str(EXAMPLES))
    try:
        spec = importlib.util.spec_from_file_location(name, EXAMPLES / f"{name}.py")
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        return module
    finally:
        sys.path.remove(str(EXAMPLES))
