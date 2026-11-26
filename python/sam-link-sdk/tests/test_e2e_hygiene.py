# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The end-to-end suite (tests/e2e) proves what the real examples do on a real Agent Mesh only while nothing in it
replaces part of what runs: no mock library, no monkeypatching, and no reach into the SDK's private modules. This
checks its source for each, so the rule holds without anyone having to remember it."""

from __future__ import annotations

import ast
import pathlib

E2E = pathlib.Path(__file__).resolve().parent / "e2e"


def _violations(path: pathlib.Path) -> list[str]:
    found = []
    for node in ast.walk(ast.parse(path.read_text(), filename=str(path))):
        where = f"{path.name}:{getattr(node, 'lineno', '?')}"
        if isinstance(node, ast.Import):
            for alias in node.names:
                if alias.name.split(".")[0] in ("mock", "pytest_mock") or alias.name.startswith("unittest.mock"):
                    found.append(f"{where} imports {alias.name}")
                if alias.name.startswith("sam_link_sdk.") and any(p.startswith("_") for p in alias.name.split(".")[1:]):
                    found.append(f"{where} imports private {alias.name}")
        elif isinstance(node, ast.ImportFrom):
            module = node.module or ""
            if module.startswith("unittest.mock") or module.split(".")[0] in ("mock", "pytest_mock"):
                found.append(f"{where} imports from {module}")
            if module == "unittest" and any(a.name == "mock" for a in node.names):
                found.append(f"{where} imports unittest.mock")
            if module.startswith("sam_link_sdk") and (
                any(part.startswith("_") for part in module.split(".")[1:])
                or any(a.name.startswith("_") for a in node.names)
            ):
                found.append(f"{where} imports from private {module}")
        elif isinstance(node, ast.Name) and node.id in ("monkeypatch", "mocker"):
            found.append(f"{where} uses {node.id}")
        elif isinstance(node, ast.arg) and node.arg in ("monkeypatch", "mocker"):
            found.append(f"{where} takes the {node.arg} fixture")
        elif (
            isinstance(node, ast.Attribute)
            and node.attr == "patch"
            and isinstance(node.value, ast.Name)
            and node.value.id == "mock"
        ):
            found.append(f"{where} uses mock.patch")
    return found


def test_the_end_to_end_suite_mocks_nothing_patches_nothing_and_imports_no_private_sdk_module():
    files = sorted(E2E.rglob("*.py"))
    assert files, f"no end-to-end tests under {E2E}"
    assert [v for path in files for v in _violations(path)] == []


def test_the_hygiene_check_finds_each_thing_it_forbids(tmp_path):
    bad = tmp_path / "bad.py"
    bad.write_text(
        "from unittest import mock\nimport unittest.mock\nfrom sam_link_sdk._adapter import approve\n"
        "import sam_link_sdk._drift\n\ndef test_x(monkeypatch):\n    mock.patch('x')\n"
    )
    found = "\n".join(_violations(bad))
    for expected in (
        "imports unittest.mock",
        "private sam_link_sdk._adapter",
        "private sam_link_sdk._drift",
        "monkeypatch fixture",
        "mock.patch",
    ):
        assert expected in found, found
