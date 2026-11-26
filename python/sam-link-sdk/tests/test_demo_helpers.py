# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The demo's setup scripts: ``examples/demo/stack/link_tokens.sh`` issues a token for every agent the entrypoint
configuration ``examples/demo/stack/agent_link.yaml`` lists, and the digests that configuration reads."""

from __future__ import annotations

import hashlib
import os
import re
import stat
import subprocess
import sys
from pathlib import Path

import pytest

from .conftest import EXAMPLES

DEMO = EXAMPLES / "demo"
AGENT_LINK = DEMO / "stack" / "agent_link.yaml"


def link_agents() -> dict[str, dict]:
    """The agents the entrypoint configuration lists, by name."""
    yaml = pytest.importorskip("yaml")
    [app] = yaml.safe_load(AGENT_LINK.read_text())["apps"]
    return {a["name"]: a for a in app["app_config"]["gateway_adapter"]["adapter_config"]["agents"]}


def run_link_tokens(tokens: Path, home: Path) -> subprocess.CompletedProcess:
    env = {
        **os.environ,
        "HOME": str(home),
        "DEMO_TOKEN_DIR": str(tokens),
        "SAM_LINK_SDK": str(Path(sys.executable).parent / "sam-link-sdk"),
    }
    return subprocess.run(
        ["bash", str(DEMO / "stack" / "link_tokens.sh")], env=env, capture_output=True, text=True, timeout=120
    )


def test_link_tokens_issues_each_agent_the_entrypoint_lists_and_the_digests_it_reads(tmp_path):
    """Every ${..._TOKEN_SHA256} the entrypoint configuration names is in tokens/digests.env, the digest of that
    agent's token; the tokens are owner-only, never printed, and kept across runs."""
    tokens = tmp_path / "tokens"
    first = run_link_tokens(tokens, tmp_path)
    assert first.returncode == 0, first.stderr
    digests = dict(line.split("=", 1) for line in (tokens / "digests.env").read_text().splitlines())
    agents = link_agents()
    assert len(agents) == 8
    assert {p.name.removesuffix(".link-token") for p in tokens.glob("*.link-token")} == set(agents)
    for name, agent in agents.items():
        [variable] = re.fullmatch(r"\$\{(\w+)\}", agent["token_sha256"]).groups()
        token = (tokens / f"{name}.link-token").read_text()
        assert digests[variable] == hashlib.sha256(token.encode()).hexdigest()
        assert stat.S_IMODE((tokens / f"{name}.link-token").stat().st_mode) == 0o600
        assert token not in first.stdout + first.stderr
    again = run_link_tokens(tokens, tmp_path)
    assert again.returncode == 0, again.stderr
    assert dict(line.split("=", 1) for line in (tokens / "digests.env").read_text().splitlines()) == digests
