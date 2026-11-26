# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The request-driven runtime templates (examples/serverless/{foundry,agentcore}) join through the agent link: woken,
announced and retired against the in-process entrypoint, at the URL they are given, needing only what
requirements.txt installs."""

from __future__ import annotations

import asyncio
import importlib
import importlib.util
import pathlib
import subprocess
import sys
import textwrap
import threading
import time

import pytest

from sam_link_sdk.link import LinkTransport

from .test_link import AGENT, TOKEN, entrypoint, results, task, types

pytest.importorskip("strands")

TEMPLATES = pathlib.Path(__file__).resolve().parents[1] / "examples" / "serverless"
RUNTIMES = ("foundry", "agentcore")
NOT_NEEDED = ("solace-pubsubplus", "pyjwt", "langchain-core")
"""Packages the agent link does without."""


def load_template(runtime: str):
    spec = importlib.util.spec_from_file_location(f"template_{runtime}_agent", TEMPLATES / runtime / "agent.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


@pytest.fixture
def link_env(monkeypatch):
    loop = asyncio.new_event_loop()
    thread = threading.Thread(target=loop.run_forever, daemon=True)
    thread.start()
    server = asyncio.run_coroutine_threadsafe(entrypoint().start(), loop).result(5)
    server.loop = loop
    for name in ("BEDROCK_MODEL_ID", "SAM_LINK_TRUST_STORE_PATH"):
        monkeypatch.delenv(name, raising=False)
    for name, value in {
        "SAM_LINK_URL": server.url,
        "SAM_LINK_TOKEN": TOKEN,
        "SAM_AGENT_NAME": AGENT,
        "AZURE_OPENAI_ENDPOINT": "https://models.example.com",
        "AZURE_OPENAI_API_KEY": "not-a-real-key",
        "AZURE_OPENAI_DEPLOYMENT": "gpt-4o-mini",
    }.items():
        monkeypatch.setenv(name, value)
    yield server
    asyncio.run_coroutine_threadsafe(server.stop(), loop).result(5)
    loop.call_soon_threadsafe(loop.stop)
    thread.join(5)


def wait_for(check, timeout: float = 5.0) -> None:
    deadline = time.monotonic() + timeout
    while not check():
        if time.monotonic() > deadline:
            raise AssertionError("condition not met in time")
        time.sleep(0.01)


@pytest.mark.parametrize("runtime", RUNTIMES)
def test_a_template_is_woken_announced_and_retired_over_the_agent_link(runtime, link_env, tmp_path, monkeypatch):
    from sam_link_sdk.hosted import link_url_from, run_operator_command

    monkeypatch.setenv("HOME", str(tmp_path))
    template = load_template(runtime)
    url = link_url_from()
    agent = template.build_agent(url)
    assert url == link_env.url and isinstance(agent._transport, LinkTransport)

    assert asyncio.run(agent.handle_wake(idle_seconds=0.2)) == 0
    run_operator_command(template.build_agent, "announce")
    run_operator_command(template.build_agent, "retire")
    wait_for(lambda: len(link_env.conns) == 3 and types(link_env, 2) == ["retire"])
    assert types(link_env, 0) == ["describe", "consume"]
    assert types(link_env, 1) == ["describe"]
    [described, _] = link_env.of("describe")
    assert (
        described["agent"]["peers"] == ["policy-desk"]
        and "Strands Agents" in described["agent"]["declared"]["framework"]
    )


@pytest.mark.parametrize("runtime", RUNTIMES)
def test_a_template_joins_the_agent_link_at_the_url_it_is_given(runtime, link_env):
    template = load_template(runtime)
    agent = template.build_agent("wss://gateway.example.com/gw/agent-link/v1/connect")
    assert isinstance(agent._transport, LinkTransport)
    assert agent._transport.config.url == "wss://gateway.example.com/gw/agent-link/v1/connect"
    assert agent._transport.config.agent == AGENT


@pytest.mark.parametrize("runtime", RUNTIMES)
def test_a_templates_requirements_hold_the_agent_links_and_nothing_it_does_without(runtime):
    requirements = (TEMPLATES / runtime / "requirements.txt").read_text()
    assert not [p for p in NOT_NEEDED if p in requirements]
    assert "websockets" in requirements
    assert sorted(p.name for p in (TEMPLATES / runtime).glob("requirements*.txt")) == ["requirements.txt"]


@pytest.mark.parametrize("runtime", RUNTIMES)
def test_a_template_loads_none_of_the_packages_the_agent_link_does_without(runtime):
    script = textwrap.dedent(f"""
        import importlib.abc, importlib.util, os, sys

        class Blocked(importlib.abc.MetaPathFinder):
            def find_spec(self, name, path=None, target=None):
                if name.split(".")[0] in ("solace", "jwt", "langchain_core"):
                    raise ModuleNotFoundError(f"No module named {{name!r}} (blocked)", name=name)
                return None

        sys.meta_path.insert(0, Blocked())
        os.environ.update(SAM_LINK_URL="wss://gw.example.com/gw/agent-link/v1/connect", SAM_LINK_TOKEN="t",
                          SAM_AGENT_NAME="claims-assistant", AZURE_OPENAI_ENDPOINT="https://models.example.com",
                          AZURE_OPENAI_API_KEY="k", AZURE_OPENAI_DEPLOYMENT="gpt-4o-mini")
        os.environ.pop("BEDROCK_MODEL_ID", None)
        spec = importlib.util.spec_from_file_location("agent", {str(TEMPLATES / runtime / "agent.py")!r})
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        agent = module.build_agent(os.environ["SAM_LINK_URL"])
        print(type(agent._transport).__name__, sorted(m for m in sys.modules
                                                   if m.split(".")[0] in ("solace", "jwt", "langchain_core")))
    """)
    result = subprocess.run([sys.executable, "-W", "ignore", "-c", script], capture_output=True, text=True, timeout=120)
    assert result.returncode == 0, result.stderr
    assert result.stdout.strip().splitlines()[-1] == "LinkTransport []"


RUNTIME_SDK = {"foundry": "azure.ai.agentserver.invocations", "agentcore": "bedrock_agentcore"}


@pytest.mark.parametrize("runtime", RUNTIMES)
def test_a_templates_deployed_wake_serves_a_task_through_the_runtimes_invocation_route(
    runtime, link_env, tmp_path, monkeypatch
):
    """What the runtime runs (main.py: serve(build_agent)): a POST to the runtime's invocation route wakes the agent,
    which joins the link, serves the task waiting for it (asking the user before pay_claim, which requires a scope the
    user was granted) and answers the POST once its queue is quiet."""
    pytest.importorskip(RUNTIME_SDK[runtime])
    from starlette.testclient import TestClient

    from .test_examples_run import strands_turns

    host = importlib.import_module(f"sam_link_sdk.{runtime}")
    monkeypatch.setenv("HOME", str(tmp_path))
    monkeypatch.setenv("SAM_PAY_CLAIM_SCOPE", "tool:claims:pay")
    link_env.responders["prompt"] = lambda f: [
        {
            "type": "prompt_answer",
            "taskId": f["taskId"],
            "requestId": f["requestId"],
            "status": "answered",
            "approved": True,
        }
    ]
    template = load_template(runtime)
    model = strands_turns(("", [("call_p", "pay_claim", {"claim_id": "C-1", "amount": 100.0})]), ("Paid C-1.", []))
    monkeypatch.setattr(template, "_model", lambda: model if runtime == "foundry" else (model, "fake-model"))
    replies: list = []

    def ring():
        with TestClient(host.app(template.build_agent)) as client:
            replies.append(client.post("/invocations", json={}))

    doorbell = threading.Thread(target=ring)
    doorbell.start()
    wait_for(lambda: link_env.of("consume"), 15)
    asyncio.run_coroutine_threadsafe(
        link_env.send(task(1, "T1", "Pay claim C-1, 100 USD", grantedToolScopes=["tool:claims:pay"])), link_env.loop
    ).result(5)
    doorbell.join(30)
    assert not doorbell.is_alive() and replies[0].status_code == 200
    assert replies[0].json()["served"] is True and replies[0].json()["messages"] >= 1
    [result] = results(link_env, "T1")
    assert result["text"] == "Paid C-1."
    assert [p["approval"]["tool"] for p in link_env.of("prompt")] == ["pay_claim"]
    paid = [f["event"] for f in link_env.of("event") if f["event"].get("kind") == "tool_finished"]
    assert [e["result"]["reference"] for e in paid] == ["PAY-C-1"]


def test_the_foundry_package_holds_its_requirements_and_every_file_is_readable_by_the_remote_build(tmp_path):
    """Foundry's build runs as another user: a file packaged owner-only (600) is reported as missing."""
    import zipfile

    spec = importlib.util.spec_from_file_location("template_foundry_deploy", TEMPLATES / "foundry" / "deploy.py")
    deploy = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(deploy)
    package = tmp_path / "agent.zip"
    deploy._package(package)
    with zipfile.ZipFile(package) as z:
        modes = {info.filename: (info.external_attr >> 16) & 0o777 for info in z.infolist()}
        assert z.read("requirements.txt").decode() == (TEMPLATES / "foundry" / "requirements.txt").read_text()
    assert {"requirements.txt", "main.py", "agent.py", "sam_link_sdk/__init__.py"} <= set(modes)
    assert "deploy.py" not in modes
    assert {name: mode for name, mode in modes.items() if mode != 0o644} == {}
