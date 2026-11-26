# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The bot that maintains the SDK cannot change the files that hold it to its rules."""

from __future__ import annotations

import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "ci"))
import check_frozen  # noqa: E402

sys.path.remove(str(ROOT / "ci"))

GLOBS = check_frozen.patterns((ROOT / "ci" / "frozen-paths.txt").read_text())
PYPROJECT = (ROOT / "pyproject.toml").read_text()


def test_the_guardrails_the_source_map_and_the_workflows_are_frozen():
    for path in (
        "python/sam-link-sdk/ci/check_skips.py",
        "python/sam-link-sdk/tests/fixtures/public_api.json",
        "python/sam-link-sdk/tests/contract/test_link_contract.py",
        "python/sam-link-sdk/compat/frameworks.json",
        "python/sam-link-sdk/tests/test_adapter_conformance.py",
        "python/sam-link-sdk/tests/e2e/test_adapter_promises.py",
        ".github/workflows/python-link-sdk-ci.yml",
        ".github/actions/link-sdk-compat-cell/action.yml",
    ):
        assert check_frozen.frozen(path, GLOBS), path


def test_adapters_their_tests_and_the_lock_are_not():
    for path in (
        "python/sam-link-sdk/src/sam_link_sdk/strands/steps.py",
        "python/sam-link-sdk/tests/test_strands.py",
        "python/sam-link-sdk/uv.lock",
        "python/sam-link-sdk/CHANGELOG.md",
        "python/sam-link-sdk/compat/pins.json",
    ):
        assert not check_frozen.frozen(path, GLOBS), path


def test_the_bot_may_move_a_dependency_in_pyproject_but_not_a_tool_setting():
    moved = PYPROJECT.replace('"crewai>=1.15.27"', '"crewai>=1.15.30"')
    assert check_frozen.violations([check_frozen.PYPROJECT], GLOBS, PYPROJECT, moved) == []
    loosened = PYPROJECT.replace('exclude-newer = "7 days"', 'exclude-newer = "1 day"')
    assert check_frozen.violations([check_frozen.PYPROJECT], GLOBS, PYPROJECT, loosened) == [
        f"{check_frozen.PYPROJECT} [tool]"
    ]
