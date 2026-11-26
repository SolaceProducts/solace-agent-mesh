# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The documentation matches the code: every Python block compiles, every sam_link_sdk name it imports or mentions exists, and
every link between pages resolves."""

from __future__ import annotations

import ast
import importlib
import importlib.util
import pathlib
import re

import pytest

from .published_header import without_published_header

ROOT = pathlib.Path(__file__).resolve().parents[1]
PAGES = sorted([ROOT / "README.md", ROOT / "CHANGELOG.md", ROOT / "CONTRIBUTING.md", *(ROOT / "docs").rglob("*.md")])
NEEDS = {
    "langchain": "langchain_core",
    "openai_agents": "agents",
    "adk": "google.adk",
    "crewai": "crewai",
    "strands": "strands",
    "agent_framework": "agent_framework",
    "otel_steps": "opentelemetry.sdk",
    "metrics": None,
    "foundry": "azure.ai.agentserver",
    "agentcore": "bedrock_agentcore",
}


def _id(path: pathlib.Path) -> str:
    return str(path.relative_to(ROOT))


def _blocks(path: pathlib.Path) -> list[str]:
    return re.findall(r"^```python\n(.*?)^```", path.read_text(), flags=re.M | re.S)


def _importable(module: str) -> bool:
    parts = module.split(".")
    need = NEEDS.get(parts[1]) if len(parts) > 1 else None
    return need is None or (
        importlib.util.find_spec(need.split(".")[0]) is not None and importlib.util.find_spec(need) is not None
    )


def _resolve(dotted: str):
    """The object a dotted sam_link_sdk name names: the longest importable module, then attributes."""
    parts = dotted.split(".")
    for i in range(len(parts), 0, -1):
        try:
            obj = importlib.import_module(".".join(parts[:i]))
        except ModuleNotFoundError:
            continue
        for attr in parts[i:]:
            obj = getattr(obj, attr)
        return obj
    raise ModuleNotFoundError(dotted)


def test_every_page_is_checked():
    assert len(PAGES) >= 14 and all(p.is_file() for p in PAGES)


def test_the_index_links_every_page():
    """docs/README.md reaches every page (the contributor pages through CONTRIBUTING.md), directly or through the
    framework index."""
    reached = set()
    for index in (ROOT / "docs" / "README.md", ROOT / "docs" / "frameworks" / "README.md", ROOT / "CONTRIBUTING.md"):
        for target in re.findall(r"\]\(([^)#\s]+)(?:#[^)]*)?\)", index.read_text()):
            if not re.match(r"[a-z]+://", target):
                reached.add((index.parent / target).resolve())
    pages = {p.resolve() for p in (ROOT / "docs").rglob("*.md") if "history" not in p.parts and p.name != "README.md"}
    assert sorted(str(p.relative_to(ROOT / "docs")) for p in pages - reached) == []


@pytest.mark.parametrize("page", PAGES, ids=_id)
def test_every_python_block_compiles_and_imports_names_that_exist(page):
    for block in _blocks(page):
        tree = compile(block, f"{_id(page)} block", "exec", flags=ast.PyCF_ALLOW_TOP_LEVEL_AWAIT | ast.PyCF_ONLY_AST)
        for node in ast.walk(tree):
            if isinstance(node, ast.ImportFrom) and (node.module or "").startswith("sam_link_sdk"):
                if not _importable(node.module):
                    continue
                module = importlib.import_module(node.module)
                missing = [a.name for a in node.names if not hasattr(module, a.name)]
                assert not missing, f"{_id(page)}: {node.module} has no {missing}"
            elif isinstance(node, ast.Import):
                for alias in node.names:
                    if alias.name.startswith("sam_link_sdk") and _importable(alias.name):
                        importlib.import_module(alias.name)


@pytest.mark.parametrize("page", PAGES, ids=_id)
def test_every_sam_link_name_the_text_mentions_exists(page):
    text = page.read_text()
    for dotted in sorted(set(re.findall(r"`(sam_link_sdk(?:\.[A-Za-z_][A-Za-z0-9_]*)+)", text))):
        if not _importable(dotted):
            continue
        _resolve(dotted)


def _anchors(path: pathlib.Path) -> set[str]:
    """The anchors GitHub gives a page's headings (code fences skipped)."""
    text = re.sub(r"^```.*?^```", "", path.read_text(), flags=re.M | re.S)
    found = set()
    for heading in re.findall(r"^#{1,6} (.+?)\s*$", text, flags=re.M):
        slug = re.sub(r"[^\w\- ]", "", heading.lower().replace("`", "")).replace(" ", "-")
        found.add(slug)
    return found


PUBLISHED_BASE = "https://github.com/SolaceProducts/solace-agent-mesh/blob/main/python/sam-link-sdk/"
"""Where this directory is published. README.md links through it, since it is also the PyPI page, where a relative
link has nothing to resolve against."""


@pytest.mark.parametrize("page", PAGES, ids=_id)
def test_every_link_between_pages_resolves(page):
    for target, anchor in re.findall(r"\]\(([^)#\s]*)(?:#([^)\s]*))?\)", page.read_text()):
        if target.startswith(PUBLISHED_BASE):
            destination = ROOT / target.removeprefix(PUBLISHED_BASE)
        elif re.match(r"[a-z]+://|mailto:", target):
            continue
        else:
            destination = page if not target else page.parent / target
        assert destination.exists(), f"{_id(page)} links to {target}, which does not exist"
        if anchor and destination.suffix == ".md":
            assert anchor in _anchors(destination), f"{_id(page)} links to {target}#{anchor}, which has no such heading"


MINIMAL_PAGES = {
    "minimal_plain": "README.md",
    "minimal_langchain": "docs/frameworks/langchain.md",
    "minimal_langgraph": "docs/frameworks/langchain.md",
    "minimal_openai_agents": "docs/frameworks/openai-agents.md",
    "minimal_adk": "docs/frameworks/adk.md",
    "minimal_crewai": "docs/frameworks/crewai.md",
    "minimal_strands": "docs/frameworks/strands.md",
    "minimal_agent_framework": "docs/frameworks/agent-framework.md",
    "minimal_hermes": "docs/frameworks/hermes.md",
}


@pytest.mark.parametrize("name", sorted(MINIMAL_PAGES))
def test_the_page_shows_its_minimal_example_as_the_file_has_it(name):
    """A reader copies the page's code, and it is the code CI runs."""
    source = without_published_header((ROOT / "examples" / f"{name}.py").read_text())
    assert source in (ROOT / MINIMAL_PAGES[name]).read_text()


def test_every_minimal_example_is_on_a_page():
    assert sorted(MINIMAL_PAGES) == sorted(p.stem for p in (ROOT / "examples").glob("minimal_*.py"))


FRAMEWORK_PAGES = ["langchain", "openai-agents", "adk", "crewai", "strands", "agent-framework", "hermes"]
SECTIONS = [
    "Install",
    "Minimal agent",
    "Steps",
    "Scopes on the tool",
    "Approvals and questions",
    "Peers",
    "Streaming",
    "Stop",
    "Files",
    "What is not supported",
]


@pytest.mark.parametrize("name", FRAMEWORK_PAGES)
def test_every_framework_page_has_the_same_sections(name):
    text = (ROOT / "docs" / "frameworks" / f"{name}.md").read_text()
    assert re.findall(r"^## (.+)$", text, flags=re.M) == SECTIONS
