# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The SDK's layers: the agent core knows only its Transport and the standard library; the agent link adds
websockets; nothing else loads unless a framework adapter is imported."""

from __future__ import annotations

import ast
import os
import pathlib
import subprocess
import sys
import textwrap

import pytest

SRC = pathlib.Path(__file__).resolve().parents[1] / "src" / "sam_link_sdk"
CI_WORKFLOW = SRC.parents[1] / ".." / ".." / ".github" / "workflows" / "python-link-sdk-ci.yml"


def ci_workflow() -> str:
    """The SDK's CI workflow, which lives in the source repository only: the public copy skips what checks it, and CI,
    where it always exists, fails on that skip (ci/check_skips.py)."""
    if not CI_WORKFLOW.is_file():
        pytest.skip("the CI workflow is not in this copy of the SDK")
    return CI_WORKFLOW.read_text()


CORE = {
    "_frameworks",
    "_health",
    "_model",
    "_run",
    "_util",
    "agent",
    "audit",
    "captext",
    "config",
    "context",
    "delegation",
    "events",
    "ids",
    "limits",
    "llm_errors",
    "memory",
    "metrics",
    "prompting",
    "redact",
    "request_context",
    "serverless",
    "stop",
    "tasks",
    "usage",
}
"""The agent core: MeshAgent and what it is built from. Everything else under sam_link_sdk is the agent link, a framework
adapter, a runtime wrapper, or the CLI.
"""
LINK_PACKAGE = "websockets"
"""The one package a plain install brings: the agent link's."""
OPT_IN_PACKAGES = ("opentelemetry",)
"""What the core imports on use only, for an opt-in feature (OtelMetrics, the `metrics` extra)."""


def _stdlib(name: str) -> bool:
    return name.split(".")[0] in sys.stdlib_module_names or name == "__future__"


def _imports(module: str) -> set[str]:
    """Every module the core module imports, at any depth of its code (relative imports resolved)."""
    tree = ast.parse((SRC / f"{module}.py").read_text())
    found: set[str] = set()
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            found.update(alias.name for alias in node.names)
        elif isinstance(node, ast.ImportFrom):
            if node.level == 0:
                found.add(node.module or "")
                continue
            base = "sam_link_sdk" if node.level == 1 else None
            assert base is not None, f"{module}: reaches above the package"
            if node.module:
                found.add(f"{base}.{node.module}")
            else:
                found.update(f"{base}.{alias.name}" for alias in node.names)
    return found


@pytest.mark.parametrize("module", sorted(CORE))
def test_the_core_imports_only_the_core(module):
    for target in _imports(module):
        if target.startswith("sam_link_sdk."):
            assert target.split(".")[1] in CORE, (
                f"sam_link_sdk.{module} imports {target}, which is not part of the core"
            )
        else:
            assert _stdlib(target) or target.split(".")[0] in OPT_IN_PACKAGES, (
                f"sam_link_sdk.{module} imports {target}, which is neither the standard library nor an opt-in package"
            )


def test_every_core_module_is_a_module():
    assert all((SRC / f"{m}.py").is_file() for m in CORE)


PLAIN_INSTALL = """
import importlib.abc, sys

ALLOWED = set(sys.stdlib_module_names) | {"sam_link_sdk", "websockets", "__future__"}
STARTUP = {m.split(".")[0] for m in list(sys.modules)}  # the interpreter's own, e.g. a virtualenv's .pth hook

class PlainInstall(importlib.abc.MetaPathFinder):
    def find_spec(self, name, path=None, target=None):
        if name.split(".")[0] not in ALLOWED:
            raise ModuleNotFoundError(f"No module named {name!r} (not in a plain install)", name=name)
        return None

sys.meta_path.insert(0, PlainInstall())

def THIRD_PARTY():
    return sorted({m.split(".")[0] for m in list(sys.modules)} - set(sys.stdlib_module_names) - STARTUP - {"sam_link_sdk"})
"""
"""Runs first in each subprocess below: any package but the standard library, sam_link_sdk and websockets is refused, as on a
plain install, and THIRD_PARTY() lists the packages that loaded besides the standard library and sam_link_sdk.
"""


def _run(script: str) -> str:
    code = PLAIN_INSTALL + textwrap.dedent(script)
    result = subprocess.run([sys.executable, "-c", code], capture_output=True, text=True, timeout=60)
    assert result.returncode == 0, result.stderr
    return result.stdout


def test_the_core_runs_on_any_transport_without_loading_a_transport_implementation():
    out = _run("""
        import sys
        import sam_link_sdk
        from sam_link_sdk import AgentConfig, MeshAgent

        class Stub:
            _manages_listing = True
            def _bind(self, host): self.host = host
            async def _open(self, *, serving=True, wait_for_peers=True): pass
            async def _consume(self, watch=None): pass
            async def _connect(self): pass
            async def _stop_background(self): pass
            async def _flush(self): pass
            async def _disconnect(self): pass
            async def _publish_description(self): pass
            async def _withdraw_description(self): pass
            async def _send_event(self, task_id, event): return True
            async def _ask(self, task_id, prompt): pass
            async def _finish(self, task_id, outcome): pass
            async def _call_peer(self, call): pass
            def _peers(self): return []
            def _usage_sink(self, task_id): return None

        agent = MeshAgent(AgentConfig(name="claims-triage", description="d"), lambda ctx: "ok", transport=Stub())
        assert agent._transport.host is agent._host
        loaded = sorted(m for m in sys.modules if m.startswith("sam_link_sdk.link"))
        print(loaded, THIRD_PARTY())
    """)
    assert out.strip() == "[] []"


def test_building_an_agent_on_the_link_loads_no_package_at_all():
    """websockets loads when the link connects (test_a_link_agent_serves_a_task_on_a_plain_install)."""
    out = _run("""
        import sam_link_sdk, sam_link_sdk.link, sam_link_sdk.cli
        from sam_link_sdk import AgentConfig, MeshAgent
        from sam_link_sdk.link import LinkConfig, LinkTransport

        link = LinkConfig(url="wss://gw.example.com/gw/agent-link/v1/connect", agent="claims-triage", token="t")
        MeshAgent(AgentConfig(name="claims-triage", description="d"), lambda ctx: "ok", transport=LinkTransport(link))
        print(THIRD_PARTY())
    """)
    assert out.strip() == "[]"


REMOVED = (
    "native",
    "signals",
    "scopes",
    "model_facts",
    "broker",
    "topics",
    "gojson",
    "spend",
    "provision",
    "register",
    "trust",
    "killswitch",
    "invocation",
    "wire",
    "peers",
    "hil",
)


@pytest.mark.parametrize("name", REMOVED)
def test_the_removed_modules_are_gone(name):
    import importlib.util

    assert importlib.util.find_spec(f"sam_link_sdk.{name}") is None


def test_the_console_script_names_a_module_that_exists():
    import importlib
    import tomllib

    target = tomllib.loads((SRC.parents[1] / "pyproject.toml").read_text())["project"]["scripts"]["sam-link-sdk"]
    module, function = target.split(":")
    assert callable(getattr(importlib.import_module(module), function))


def test_ci_runs_the_unit_tests_on_every_python_version_the_package_claims():
    """The nightly run's list; a pull request runs only the oldest and newest."""
    import re
    import tomllib

    root = SRC.parents[1]
    classifiers = tomllib.loads((root / "pyproject.toml").read_text())["project"]["classifiers"]
    versions = [c for c in classifiers if re.fullmatch(r"Programming Language :: Python :: 3\.\d+", c)]
    claimed = sorted(c.rsplit(" :: ", 1)[1] for c in versions)
    matrix = re.search(
        r"^\s+python: \$\{\{ fromJSON\(needs\.changes\.outputs\.full == 'true' && '\[(.*?)\]'",
        ci_workflow(),
        flags=re.M,
    )
    assert matrix and sorted(v.strip().strip('"') for v in matrix.group(1).split(",")) == claimed


LINK_ALLOWED = CORE | {"link"}
"""The agent link may use the core, its own modules, the standard library and websockets."""
LINK_MODULES = ("__init__", "config", "connection", "describer", "giveback", "transport", "usage_ledger", "v2")


def _module_imports(path: pathlib.Path, *, top_level_only: bool) -> set[str]:
    tree = ast.parse(path.read_text())
    nodes = tree.body if top_level_only else list(ast.walk(tree))
    found: set[str] = set()
    for node in nodes:
        if isinstance(node, ast.Import):
            found.update(alias.name for alias in node.names)
        elif isinstance(node, ast.ImportFrom):
            if node.level == 0:
                found.add(node.module or "")
                continue
            base = {1: "sam_link_sdk.link", 2: "sam_link_sdk"}[node.level]
            found.add(f"{base}.{node.module}" if node.module else base)
    return found


@pytest.mark.parametrize("module", LINK_MODULES)
def test_the_link_imports_only_the_core_and_itself(module):
    for target in _module_imports(SRC / "link" / f"{module}.py", top_level_only=False):
        if target.startswith("sam_link_sdk."):
            assert target.split(".")[1] in LINK_ALLOWED, f"sam_link_sdk.link.{module} imports {target}"
        else:
            assert _stdlib(target) or target.split(".")[0] == LINK_PACKAGE, (
                f"sam_link_sdk.link.{module} imports {target}"
            )


def test_every_link_module_is_checked():
    assert sorted(p.stem for p in (SRC / "link").glob("*.py")) == sorted(LINK_MODULES)


def test_a_link_agent_serves_a_task_on_a_plain_install():
    out = _run("""
        import asyncio, json, sys
        from websockets.asyncio.server import serve
        from sam_link_sdk import AgentConfig, MeshAgent
        from sam_link_sdk.link import LinkConfig, LinkTransport

        async def main():
            got = asyncio.get_running_loop().create_future()

            async def entrypoint(ws):
                await ws.send(json.dumps({"type": "welcome", "protocol": 2, "agent": "claims-triage", "connection": "c0",
                                          "maxInflight": 4, "enabled": True,
                                          "limits": {"maxFrameBytes": 1048576, "maxStringBytes": 16384}}))
                async for raw in ws:
                    frame = json.loads(raw)
                    if frame["type"] == "consume":
                        await ws.send(json.dumps({"type": "task", "delivery": 1, "taskId": "T1", "task": {
                            "contextId": "", "text": "hello", "files": [], "sessionBehavior": "session",
                            "user": {"id": "alice@example.com"}, "callDepth": 0, "trigger": "", "traceId": "",
                            "calledBy": "", "requestContext": {}, "metadata": {}}}))
                    elif frame["type"] == "result" and not got.done():
                        got.set_result(frame)

            server = await serve(entrypoint, "127.0.0.1", 0, subprotocols=["sam-agent-link.v2"])
            port = next(iter(server.sockets)).getsockname()[1]
            link = LinkConfig(url=f"ws://127.0.0.1:{port}/gw/agent-link/v1/connect", agent="claims-triage", token="t")
            config = AgentConfig(name="claims-triage", description="d", keepalive_seconds=0, shutdown_grace_seconds=0)
            agent = MeshAgent(config, lambda ctx: f"Hello {ctx.user_id}", transport=LinkTransport(link))
            await agent.start()
            result = await asyncio.wait_for(got, 10)
            await agent.stop()
            server.close()
            return result

        result = asyncio.run(main())
        assert result["text"] == "Hello alice@example.com", result
        print(THIRD_PARTY())
    """)
    assert out.strip() == f"[{LINK_PACKAGE!r}]"


ADAPTER_PACKAGES = ("langchain", "openai_agents", "adk", "crewai", "strands", "agent_framework", "hermes")
"""The framework adapters, and what they share, use only the core: never a transport, so an adapter works the same on any
Transport.
"""
ADAPTER_SHARED = {"_adapter", "_drift"}


def _adapter_modules() -> list[str]:
    found = ["otel_steps", "_adapter"]
    for package in ADAPTER_PACKAGES:
        found += [f"{package}.{p.stem}" for p in sorted((SRC / package).glob("*.py"))]
    return found


def _package_imports(module: str) -> set[str]:
    """Every sam_link_sdk module `module` (dotted, under sam_link_sdk) imports, relative imports resolved."""
    parts = module.split(".")
    path = SRC.joinpath(*parts[:-1], f"{parts[-1]}.py")
    package = ["sam_link_sdk", *parts[:-1]]
    found: set[str] = set()
    for node in ast.walk(ast.parse(path.read_text())):
        if isinstance(node, ast.ImportFrom) and node.level:
            base = package[: len(package) - (node.level - 1)]
            assert len(base) >= 1, f"{module}: reaches above the package"
            targets = [node.module] if node.module else [alias.name for alias in node.names]
            found.update(".".join([*base, t]) for t in targets)
        elif isinstance(node, ast.ImportFrom) and (node.module or "").startswith("sam_link_sdk"):
            found.add(node.module)
        elif isinstance(node, ast.Import):
            found.update(alias.name for alias in node.names if alias.name.startswith("sam_link_sdk"))
    return found


@pytest.mark.parametrize("module", _adapter_modules())
def test_the_adapters_import_only_the_core(module):
    own_package = module.split(".")[0] if "." in module else None
    for target in _package_imports(module):
        top = target.split(".")[1]
        assert top in CORE or top in ADAPTER_SHARED or top == own_package, (
            f"sam_link_sdk.{module} imports {target}, which is neither the core nor its own package"
        )


def test_what_the_adapters_share_is_core_only():
    for target in _package_imports("_adapter"):
        assert target.split(".")[1] in CORE, f"sam_link_sdk._adapter imports {target}"


def test_importing_the_adapters_loads_no_transport():
    """The frameworks start threads when imported (CrewAI's event bus, OpenTelemetry's span processors, LiteLLM's workers),
    which may import modules while this runs, and register exit handlers that flush and export over the network. So the
    module list is a snapshot (iterating sys.modules itself can fail with "dictionary changed size"), the answer is a
    marked line rather than whatever is printed last, and the process ends without running those handlers: none of that
    is what this checks, and any of it made the check fail at random.
    """
    script = textwrap.dedent("""
        import importlib, importlib.util, json, os, sys
        for name, framework in [("langchain", "langchain_core"), ("openai_agents", "agents"), ("adk", "google.adk"),
                                ("crewai", "crewai"), ("strands", "strands"), ("agent_framework", "agent_framework"),
                                ("hermes", None), ("otel_steps", "opentelemetry.sdk")]:
            if framework is None or importlib.util.find_spec(framework) is not None:
                importlib.import_module(f"sam_link_sdk.{name}")
        loaded = sorted(m for m in list(sys.modules) if m.startswith("sam_link_sdk.link"))
        print("LOADED " + json.dumps(loaded), flush=True)
        os._exit(0)
    """)
    env = {**os.environ, "CREWAI_DISABLE_TELEMETRY": "true", "CREWAI_TRACING_ENABLED": "false"}
    result = subprocess.run(
        [sys.executable, "-W", "ignore", "-c", script], capture_output=True, text=True, timeout=120, env=env
    )
    assert result.returncode == 0, result.stderr
    [loaded] = [line for line in result.stdout.splitlines() if line.startswith("LOADED ")]
    assert loaded == "LOADED []"


def test_an_unavailable_sam_fails_the_contract_run_where_it_is_required(monkeypatch):
    import pytest as pytest_

    from .contract import conftest as contract

    monkeypatch.delenv(contract.REQUIRED_ENV, raising=False)
    with pytest_.raises(pytest_.skip.Exception):
        contract.sam_unavailable("no SAM-Go")
    monkeypatch.setenv(contract.REQUIRED_ENV, "1")
    with pytest_.raises(pytest_.fail.Exception, match="no SAM-Go"):
        contract.sam_unavailable("no SAM-Go")
    workflow = ci_workflow()
    assert f'{contract.REQUIRED_ENV}: "1"' in workflow
