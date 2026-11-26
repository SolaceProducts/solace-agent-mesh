# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The skip guard of a compatibility cell allows only skips for frameworks the cell does not install."""

from __future__ import annotations

import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "ci"))
import check_skips  # noqa: E402

sys.path.remove(str(ROOT / "ci"))

CELL = check_skips.Cell("strands", ("strands", "openai"), ("tests/test_strands.py",))


def _skipped(reason: str, test_id: str = "tests/test_x.py::test_y") -> check_skips.Outcome:
    return check_skips.Outcome(test_id, test_id.partition("::")[0], "skipped", reason)


def _passed(module: str) -> check_skips.Outcome:
    return check_skips.Outcome(f"{module}::test_ok", module, "passed", "")


def test_a_cell_allows_a_skip_for_a_framework_it_does_not_install():
    outcomes = [_passed("tests/test_strands.py"), _skipped("could not import 'crewai': No module named 'crewai'")]
    assert check_skips.check(outcomes, [], ("tests/test_strands.py",), CELL) == []


def test_a_cell_fails_a_skip_for_a_framework_it_installs():
    outcomes = [_passed("tests/test_strands.py"), _skipped("could not import 'strands.telemetry.tracer'")]
    problems = check_skips.check(outcomes, [], ("tests/test_strands.py",), CELL)
    assert problems == [
        "skipped and not in the allowlist: tests/test_x.py::test_y: could not import 'strands.telemetry.tracer'"
    ]


def test_a_cell_fails_a_skip_for_any_other_reason():
    outcomes = [_passed("tests/test_strands.py"), _skipped("needs a SAM")]
    assert len(check_skips.check(outcomes, [], ("tests/test_strands.py",), CELL)) == 1


def test_a_cell_reads_its_definition_from_the_source_map():
    cell = check_skips.read_cell("langgraph", ROOT / "compat" / "frameworks.json")
    assert cell.provides("langgraph.prebuilt")
    assert not cell.provides("crewai")
    assert "tests/test_langgraph.py" in cell.unit
