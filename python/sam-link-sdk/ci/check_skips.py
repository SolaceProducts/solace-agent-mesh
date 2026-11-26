# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Fails a test run that skipped a test for a reason ci/allowed-skips.txt does not list.

Most framework tests start with `pytest.importorskip`, so a framework that stops importing skips its tests instead of
failing them, and the run stays green with fewer tests. This reads pytest's JUnit XML report and fails on any skip the
allowlist does not name, and on an adapter test module in which no test passed. The contract and end-to-end runs hold
no adapter test modules, so `--any-modules` leaves that second check out for them.

A compatibility cell (python-link-sdk-compat.yml) installs one framework alone, so `--cell <name>` also allows a skip
for a module that cell does not install (compat/frameworks.json lists what it does), and checks only its own adapter
test modules. A skip for a module the cell installs still fails it.

Usage: python3 ci/check_skips.py <junit.xml> <allowed-skips.txt> [--any-modules | --cell <name>]
"""

from __future__ import annotations

import json
import re
import sys
import xml.etree.ElementTree as ElementTree
from dataclasses import dataclass
from pathlib import Path

ADAPTER_MODULES = (
    "tests/test_adk.py",
    "tests/test_agent_framework.py",
    "tests/test_agentcore.py",
    "tests/test_crewai.py",
    "tests/test_fail_closed.py",
    "tests/test_langgraph.py",
    "tests/test_openai_agents.py",
    "tests/test_otel_steps.py",
    "tests/test_strands.py",
)
"""One module per framework adapter or runtime wrapper. Each must have at least one passing test, so an adapter whose tests
all vanish (a renamed module, a collection that finds nothing) fails the run too.
"""


@dataclass(frozen=True)
class Outcome:
    test_id: str
    module: str
    status: str
    reason: str


@dataclass(frozen=True)
class Allowed:
    prefix: str
    reason: str
    line: int

    def covers(self, outcome: Outcome) -> bool:
        return outcome.test_id.startswith(self.prefix) and self.reason in outcome.reason


def module_path(dotted: str, root: Path) -> tuple[str, list[str]]:
    """Splits a JUnit classname (`tests.test_x.TestY`) into the test file and the class names after it."""
    parts = [part for part in dotted.split(".") if part]
    for end in range(len(parts), 0, -1):
        candidate = "/".join(parts[:end]) + ".py"
        if (root / candidate).is_file():
            return candidate, parts[end:]
    return dotted, []


def read_report(report: Path, root: Path) -> list[Outcome]:
    """Read the passed and skipped tests from a JUnit report.

    A module skipped at collection (an `importorskip` at its top) says only "collection skipped" in the message, so its
    reason is read from the element's text.
    """
    outcomes = []
    for case in ElementTree.parse(report).getroot().iter("testcase"):
        classname = case.get("classname", "")
        name = case.get("name", "")
        if classname:
            module, classes = module_path(classname, root)
            test_id = "::".join([module, *classes, name])
        else:
            module, _ = module_path(name, root)
            test_id = module
        skipped = case.find("skipped")
        if case.find("failure") is not None or case.find("error") is not None:
            status, reason = "failed", ""
        elif skipped is not None and skipped.get("type") == "pytest.xfail":
            status, reason = "xfailed", skipped.get("message", "")
        elif skipped is not None:
            message = skipped.get("message", "")
            status, reason = "skipped", (skipped.text or message) if message == "collection skipped" else message
        else:
            status, reason = "passed", ""
        outcomes.append(Outcome(test_id, module, status, reason))
    return outcomes


def read_allowlist(path: Path) -> list[Allowed]:
    """Each entry is `<test id prefix> | <text the skip reason contains>`, under a comment that says why."""
    entries = []
    commented = False
    for number, raw in enumerate(path.read_text().splitlines(), start=1):
        line = raw.strip()
        if not line:
            commented = False
            continue
        if line.startswith("#"):
            commented = True
            continue
        prefix, separator, reason = line.partition(" | ")
        if not separator or not prefix.strip() or not reason.strip():
            raise ValueError(f"{path}:{number}: expected `<test id prefix> | <skip reason text>`, got {raw!r}")
        if not commented:
            raise ValueError(f"{path}:{number}: an entry needs a comment above it saying why the skip is allowed")
        entries.append(Allowed(prefix.strip(), reason.strip(), number))
    return entries


MISSING_MODULE = re.compile(r"(?:could not import|No module named) '([\w.]+)'")


@dataclass(frozen=True)
class Cell:
    name: str
    imports: tuple[str, ...]
    unit: tuple[str, ...]

    def provides(self, module: str) -> bool:
        return any(module == name or module.startswith(name + ".") for name in self.imports)

    def not_installed(self, outcome: Outcome) -> bool:
        """A skip because a module this cell does not install is missing."""
        missing = MISSING_MODULE.search(outcome.reason)
        return missing is not None and not self.provides(missing.group(1))


def read_cell(name: str, frameworks: Path) -> Cell:
    cells = json.loads(frameworks.read_text())["cells"]
    if name not in cells:
        raise ValueError(f"{frameworks} has no cell {name!r}; it has {', '.join(cells)}")
    return Cell(name, tuple(cells[name]["imports"]), tuple(cells[name]["unit"]))


def check(
    outcomes: list[Outcome],
    allowlist: list[Allowed],
    adapter_modules: tuple[str, ...] = ADAPTER_MODULES,
    cell: Cell | None = None,
) -> list[str]:
    problems = []
    if not outcomes:
        problems.append("the report holds no tests")
    for outcome in outcomes:
        allowed = any(entry.covers(outcome) for entry in allowlist) or (
            cell is not None and cell.not_installed(outcome)
        )
        if outcome.status == "skipped" and not allowed:
            problems.append(f"skipped and not in the allowlist: {outcome.test_id}: {outcome.reason}")
    for module in adapter_modules:
        if not any(o.module == module and o.status == "passed" for o in outcomes):
            problems.append(f"no test passed in {module}")
    return problems


def main(argv: list[str]) -> int:
    usage = (
        len(argv) == 3 or (len(argv) == 4 and argv[3] == "--any-modules") or (len(argv) == 5 and argv[3] == "--cell")
    )
    if not usage:
        print(__doc__.strip().splitlines()[-1], file=sys.stderr)
        return 2
    report, allowlist_path = Path(argv[1]), Path(argv[2])
    cell = read_cell(argv[4], Path("compat/frameworks.json")) if len(argv) == 5 else None
    outcomes = read_report(report, Path.cwd())
    allowlist = read_allowlist(allowlist_path)
    counts: dict[str, int] = {}
    for outcome in outcomes:
        counts[outcome.status] = counts.get(outcome.status, 0) + 1
    print(", ".join(f"{count} {status}" for status, count in sorted(counts.items())))
    unused = [e for e in allowlist if not any(o.status == "skipped" and e.covers(o) for o in outcomes)]
    for entry in unused:
        print(f"note: {allowlist_path}:{entry.line} matched no skip in this run ({entry.prefix})")
    if cell is not None:
        modules = tuple(m for m in ADAPTER_MODULES if m in cell.unit)
    elif len(argv) == 4:
        modules = ()
    else:
        modules = ADAPTER_MODULES
    problems = check(outcomes, allowlist, modules, cell)
    for problem in problems:
        print(f"::error title=Skip guard::{problem}")
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
