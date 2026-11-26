# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The versions the SDK claims to support are the ones CI tests, never ones written by hand.

The lowest supported versions are the lower bounds in pyproject.toml, which the floor cells of python-link-sdk-ci.yml
install. These tests hold the docs, each adapter's TESTED_VERSION, the source map and cells in compat/frameworks.json,
the cells CI runs and the Hermes pin to those sources.
"""

from __future__ import annotations

import json
import re
import sys
import tomllib
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[1]
FRAMEWORKS = json.loads((ROOT / "compat" / "frameworks.json").read_text())["frameworks"]
PINS = json.loads((ROOT / "compat" / "pins.json").read_text())
EXTRAS = tomllib.loads((ROOT / "pyproject.toml").read_text())["project"]["optional-dependencies"]
REQUIREMENT = re.compile(r"^([A-Za-z0-9_.-]+)(?:\[[^\]]*\])?>=([^,;\s]+)")
DOCS = sorted((ROOT / "docs").rglob("*.md"))

TESTED_VERSIONS = {
    "src/sam_link_sdk/langchain/__init__.py": {
        "TESTED_VERSION": "langchain-core",
        "LANGGRAPH_TESTED_VERSION": "langgraph",
    },
    "src/sam_link_sdk/openai_agents/__init__.py": {"TESTED_VERSION": "openai-agents"},
    "src/sam_link_sdk/adk/__init__.py": {"TESTED_VERSION": "google-adk"},
    "src/sam_link_sdk/crewai/__init__.py": {"TESTED_VERSION": "crewai"},
    "src/sam_link_sdk/strands/__init__.py": {"TESTED_VERSION": "strands-agents"},
    "src/sam_link_sdk/agent_framework/__init__.py": {"TESTED_VERSION": "agent-framework-core"},
    "src/sam_link_sdk/otel_steps.py": {"TESTED_VERSION": "opentelemetry-sdk"},
    "src/sam_link_sdk/foundry.py": {"TESTED_VERSION": "azure-ai-agentserver-invocations"},
    "src/sam_link_sdk/agentcore.py": {"TESTED_VERSION": "bedrock-agentcore"},
}
"""Where each adapter states the framework version its FrameworkDrift message calls tested: the lowest supported one."""

HERMES_MENTIONS = {
    "src/sam_link_sdk/hermes/steps.py": r'"checkout ([0-9a-f]{8,40})"',
    "docs/frameworks/hermes.md": r"checkout `?([0-9a-f]{8,40})`?",
    "docs/contributing/adapters.md": r"checkout `([0-9a-f]{8,40})`",
    "examples/claims_triage_hermes.py": r"written against ([0-9a-f]{8,40})",
    "tests/test_tool_caches.py": r"checkout ([0-9a-f]{8,40})",
}


def _floors() -> dict[str, str]:
    floors: dict[str, str] = {}
    for requirements in EXTRAS.values():
        for requirement in requirements:
            match = REQUIREMENT.match(requirement)
            assert match, f"{requirement!r} in [project.optional-dependencies] has no lower bound (>=)"
            name, floor = match.group(1).lower(), match.group(2)
            assert floors.setdefault(name, floor) == floor, f"{name} has two lower bounds: {floors[name]} and {floor}"
    return floors


FLOORS = _floors()
PACKAGE_NAMES = sorted(FLOORS, key=len, reverse=True)
VERSION_MENTION = re.compile(rf"\b({'|'.join(re.escape(n) for n in PACKAGE_NAMES)}) (\d+(?:\.\d+)+(?:[a-z]+\d+)?)\b")


def test_the_source_map_names_every_extra_and_only_its_own_packages():
    assert set(FRAMEWORKS) - {"hermes"} == set(EXTRAS)
    for extra, source in FRAMEWORKS.items():
        names = {REQUIREMENT.match(r).group(1).lower() for r in EXTRAS.get(extra, [])}
        assert set(source["packages"]) == names, f"compat/frameworks.json {extra}: packages differ from the extra"
        assert re.fullmatch(r"[\w.-]+/[\w.-]+", source["repo"]), extra
        assert source["docs"].startswith("https://"), extra
        assert source["adapter"].startswith("sam_link_sdk."), extra


def test_each_adapter_is_tested_with_the_lowest_version_it_supports():
    for path, constants in TESTED_VERSIONS.items():
        text = (ROOT / path).read_text()
        for constant, package in constants.items():
            match = re.search(rf'^{constant} = "([^"]+)"$', text, re.MULTILINE)
            assert match, f"{path} has no {constant}"
            assert match.group(1) == FLOORS[package], (
                f"{path} {constant} is {match.group(1)}, the lower bound of {package} in pyproject.toml is "
                f"{FLOORS[package]}"
            )


@pytest.mark.parametrize("doc", DOCS, ids=[str(d.relative_to(ROOT)) for d in DOCS])
def test_the_docs_state_the_lowest_supported_versions_pyproject_sets(doc):
    for number, line in enumerate(doc.read_text().splitlines(), 1):
        if "Tested with" not in line and "Lowest supported" not in line:
            continue
        for package, version in VERSION_MENTION.findall(line):
            assert version == FLOORS[package], (
                f"{doc.relative_to(ROOT)}:{number} says {package} {version}; its lower bound in pyproject.toml is "
                f"{FLOORS[package]}"
            )


def test_every_mention_of_the_hermes_commit_is_the_pinned_one():
    pinned = PINS["hermes"]
    assert re.fullmatch(r"[0-9a-f]{40}", pinned)
    for path, pattern in HERMES_MENTIONS.items():
        found = re.findall(pattern, (ROOT / path).read_text())
        assert found, f"{path} no longer names the Hermes commit; update HERMES_MENTIONS"
        for commit in found:
            assert pinned.startswith(commit), f"{path} names Hermes {commit}; compat/pins.json pins {pinned}"


def test_every_cell_installs_real_extras_and_runs_modules_and_frameworks_that_exist():
    cells = json.loads((ROOT / "compat" / "frameworks.json").read_text())["cells"]
    e2e_frameworks = set(re.findall(r'Framework\(\s*"(\w+)"', (ROOT / "tests" / "e2e" / "conftest.py").read_text()))
    assert set(cells) == set(FRAMEWORKS) - {"openai"}
    for name, cell in cells.items():
        assert set(cell["extras"]) <= set(EXTRAS), name
        assert cell["extras"] or cell.get("record"), f"cell {name} installs no extra and records no version"
        assert cell["e2e"] is None or cell["e2e"] in e2e_frameworks, name
        for module in cell["unit"]:
            assert (ROOT / module).is_file(), f"cell {name} runs {module}, which does not exist"


def test_every_adapter_test_module_runs_in_a_cell():
    sys.path.insert(0, str(ROOT / "ci"))
    try:
        import check_skips
    finally:
        sys.path.remove(str(ROOT / "ci"))
    cells = json.loads((ROOT / "compat" / "frameworks.json").read_text())["cells"]
    covered = {module for cell in cells.values() for module in cell["unit"]}
    missing = [m for m in check_skips.ADAPTER_MODULES if m not in covered and m != "tests/test_fail_closed.py"]
    assert not missing, f"no compatibility cell runs {missing}"


def test_ci_runs_every_cell_at_its_lowest_supported_release():
    cells = json.loads((ROOT / "compat" / "frameworks.json").read_text())["cells"]
    workflow = (ROOT.parents[1] / ".github" / "workflows" / "python-link-sdk-ci.yml").read_text()
    match = re.search(r"floor-cells:.*?cell: \[([^\]]*)\]", workflow, re.DOTALL)
    assert match, "python-link-sdk-ci.yml has no floor-cells matrix"
    listed = {cell.strip() for cell in match.group(1).split(",")}
    assert listed == set(cells) - {"hermes"}
