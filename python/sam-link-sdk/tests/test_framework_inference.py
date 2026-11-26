# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""An agent that names no framework is described as built with the framework of the one adapter it imported."""

from __future__ import annotations

import subprocess
import sys
import textwrap

import pytest

from sam_link_sdk import _frameworks
from sam_link_sdk._health import Health
from sam_link_sdk.agent import _describe
from sam_link_sdk.config import AgentConfig

from .test_adapter_surface import ADAPTERS, FRAMEWORK

FRAMEWORK_NAME = {
    "crewai": "CrewAI",
    "adk": "Google ADK",
    "strands": "Strands Agents",
    "openai_agents": "OpenAI Agents SDK",
    "agent_framework": "Microsoft Agent Framework",
    "langchain": "LangChain",
    "hermes": "Hermes Agent",
}
"""The name each adapter records for its framework."""


@pytest.fixture
def none_adopted(monkeypatch):
    monkeypatch.setattr(_frameworks, "_adopted", {})


def _framework_described(config: AgentConfig) -> str:
    return _describe(config, Health()).declared.framework


def test_the_only_imported_adapter_names_the_framework(none_adopted):
    _frameworks.adopt("CrewAI")
    _frameworks.adopt("CrewAI")

    assert _frameworks.adopted() == ("CrewAI",)
    assert (
        _framework_described(AgentConfig(name="claims-assistant", description="Triages insurance claims")) == "CrewAI"
    )


def test_the_framework_the_author_names_wins(none_adopted):
    _frameworks.adopt("CrewAI")

    assert (
        _framework_described(
            AgentConfig(name="claims-assistant", description="Triages insurance claims", framework="LangGraph")
        )
        == "LangGraph"
    )


def test_no_adapter_leaves_the_framework_unnamed(none_adopted):
    assert _framework_described(AgentConfig(name="claims-assistant", description="Triages insurance claims")) == ""


def test_adapters_for_two_frameworks_leave_the_framework_unnamed(none_adopted):
    _frameworks.adopt("LangChain")
    _frameworks.adopt("OpenAI Agents SDK")

    assert _frameworks.adopted() == ("LangChain", "OpenAI Agents SDK")
    assert _framework_described(AgentConfig(name="claims-assistant", description="Triages insurance claims")) == ""


def test_every_adapter_has_a_framework_name():
    assert set(FRAMEWORK_NAME) == set(ADAPTERS)


@pytest.mark.parametrize("adapter", ADAPTERS)
def test_importing_an_adapter_records_its_framework(adapter):
    if FRAMEWORK[adapter]:
        pytest.importorskip(FRAMEWORK[adapter])
    code = textwrap.dedent(
        f"""
        import sam_link_sdk.{adapter}
        from sam_link_sdk._frameworks import adopted
        print(repr(adopted()))
        """
    )
    result = subprocess.run([sys.executable, "-c", code], capture_output=True, text=True, timeout=120)

    assert result.returncode == 0, result.stderr
    assert result.stdout.strip() == repr((FRAMEWORK_NAME[adapter],))
