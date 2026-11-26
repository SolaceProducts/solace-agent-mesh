# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""sam_link_sdk.agentcore: the doorbell handler on Bedrock AgentCore Runtime, and secrets from Secrets Manager."""

from __future__ import annotations

import asyncio
import threading

import pytest

pytest.importorskip("bedrock_agentcore")

from starlette.testclient import TestClient

from sam_link_sdk import agentcore

LINK_URL = "wss://sam.example.com/gw/agent-link/v1/connect"


class FakeAgent:
    def __init__(self, seen, release: threading.Event | None = None, fail: bool = False):
        self.seen, self.release, self.fail = seen, release, fail

    async def handle_wake(self):
        self.seen.append(self.status())
        if self.release is not None:
            while not self.release.is_set():
                await asyncio.sleep(0.01)
        if self.fail:
            raise RuntimeError("the entrypoint refused the token")
        return 2


def _app(monkeypatch, **agent):
    monkeypatch.setenv("SAM_LINK_URL", LINK_URL)
    seen: list = []
    holder = {}

    def build(link_url):
        assert link_url == LINK_URL
        a = FakeAgent(seen, **agent)
        a.status = lambda: holder["core"].get_current_ping_status().value
        return a

    holder["core"] = agentcore.app(build)
    return holder["core"], seen


def test_a_wake_serves_the_queue_and_reports_busy_while_serving(monkeypatch):
    core, seen = _app(monkeypatch)
    with TestClient(core) as client:
        reply = client.post("/invocations", json={"jsonrpc": "2.0", "method": "message/send"})
        assert reply.status_code == 200 and reply.json()["messages"] == 2
        assert seen == ["HealthyBusy"]
        assert client.get("/ping").json()["status"] == "Healthy"


def test_a_failed_wake_answers_an_error_so_sam_rings_again(monkeypatch):
    core, _ = _app(monkeypatch, fail=True)
    with TestClient(core) as client:
        reply = client.post("/invocations", json={})
        assert reply.status_code == 500
        assert client.get("/ping").json()["status"] == "Healthy"


def test_serving_continues_after_the_request_is_dropped(monkeypatch):
    release = threading.Event()
    core, seen = _app(monkeypatch, release=release)
    handler = core.handlers["main"]

    async def drop_then_release():
        request = asyncio.ensure_future(handler({}))
        await asyncio.sleep(0.05)
        request.cancel()
        await asyncio.sleep(0.05)
        assert core.get_current_ping_status().value == "HealthyBusy"
        release.set()
        for _ in range(100):
            if core.get_current_ping_status().value == "Healthy":
                return
            await asyncio.sleep(0.01)
        raise AssertionError("serving never finished")

    asyncio.run(drop_then_release())
    assert seen == ["HealthyBusy"]


def test_no_link_address_is_an_error(monkeypatch):
    monkeypatch.delenv("SAM_LINK_URL", raising=False)
    core = agentcore.app(lambda url: FakeAgent([]))
    with TestClient(core) as client:
        assert client.post("/invocations", json={}).status_code == 500


class FakeSecrets:
    def __init__(self, values):
        self.values, self.asked = values, []

    def get_secret_value(self, SecretId):  # noqa: N803 - boto3's parameter name
        self.asked.append(SecretId)
        return {"SecretString": self.values[SecretId]}


def test_secret_arns_become_the_variables_they_name():
    env = {
        "SAM_LINK_TOKEN_SECRET_ID": "arn:secret:link",
        "AZURE_OPENAI_API_KEY_SECRET_ID": "arn:secret:model",
        "SAM_LINK_AGENT": "claims-assistant",
        "EMPTY_SECRET_ID": "",
    }
    client = FakeSecrets({"arn:secret:link": "link-token", "arn:secret:model": "key"})
    assert agentcore.load_secrets(env, client) == ["AZURE_OPENAI_API_KEY", "SAM_LINK_TOKEN"]
    assert env["SAM_LINK_TOKEN"] == "link-token" and env["AZURE_OPENAI_API_KEY"] == "key"
    assert "EMPTY" not in env


def test_without_secret_arns_secrets_manager_is_not_called():
    assert agentcore.load_secrets({"SAM_LINK_TOKEN": "link-token"}, client=object()) == []
