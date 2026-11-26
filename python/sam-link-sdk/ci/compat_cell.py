# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Writes the result of one compatibility cell (.github/actions/link-sdk-compat-cell) as compat-<cell>-<point>.json.

The file is read by the bot that maintains the SDK, so it holds only fields of a fixed shape: versions, pass or fail,
and the ids of the tests that failed. Nothing a framework release prints reaches it.

The workflow runs it with python3 -I, which leaves its own directory off the import path, so that directory is put
back for check_skips.

Usage: python3 ci/compat_cell.py <cell> <point> <python> <resolved|unresolvable> <installed.json> <out.json> [junit.xml ...]
"""

from __future__ import annotations

import json
import os
import re
import sys
import xml.etree.ElementTree as ElementTree
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from check_skips import module_path

POINTS = ("floor", "newest", "published", "prerelease")
TEST_ID = re.compile(r"^(?:tests|ci)/[\w/]+\.py(::[\w\[\]\-.]+)+$")
VERSION = re.compile(r"^[\w.+!-]+$")
MAX_FAILED = 50


def failed_tests(reports: list[Path]) -> tuple[list[str], int]:
    """The ids of the failed tests, and how many tests ran. An unreadable report counts as a failure of the run."""
    failed, ran = [], 0
    for report in reports:
        if not report.is_file():
            failed.append(f"tests/{report.stem}.py::report_missing")
            continue
        for case in ElementTree.parse(report).getroot().iter("testcase"):
            ran += 1
            if case.find("failure") is None and case.find("error") is None:
                continue
            module, classes = module_path(case.get("classname", ""), Path.cwd())
            test_id = "::".join([module, *classes, case.get("name", "")])
            failed.append(test_id if TEST_ID.match(test_id) else "tests/unknown.py::unreadable_test_id")
    return sorted(set(failed))[:MAX_FAILED], ran


def main(argv: list[str]) -> int:
    if len(argv) < 7:
        print(__doc__.strip().splitlines()[-1], file=sys.stderr)
        return 2
    cell, point, python, resolution, installed_path, out = argv[1:7]
    cells = json.loads(Path("compat/frameworks.json").read_text())
    if cell not in cells["cells"] or point not in POINTS or not re.fullmatch(r"3\.\d+", python):
        print(f"unknown cell {cell!r}, point {point!r} or Python {python!r}", file=sys.stderr)
        return 2
    definition = cells["cells"][cell]
    packages = set(definition.get("record", [])) or {
        p for extra in definition["extras"] for p in cells["frameworks"][extra]["packages"]
    }
    versions = {}
    if resolution == "resolved":
        for dist in json.loads(Path(installed_path).read_text()):
            name = dist["name"].lower()
            if name in packages and VERSION.match(dist["version"]):
                versions[name] = dist["version"]
    failed, ran = failed_tests([Path(r) for r in argv[7:]]) if resolution == "resolved" else ([], 0)
    if resolution != "resolved":
        result = "unresolvable"
    elif failed or ran == 0:
        result = "fail"
    else:
        result = "pass"
    record = {
        "cell": cell,
        "point": point,
        "python": python,
        "result": result,
        "versions": dict(sorted(versions.items())),
        "tests": ran,
        "failed": failed,
        "commit": os.environ.get("GITHUB_SHA", ""),
        "run": f"{os.environ.get('GITHUB_SERVER_URL', '')}/{os.environ.get('GITHUB_REPOSITORY', '')}/actions/runs/{os.environ.get('GITHUB_RUN_ID', '')}",
    }
    Path(out).write_text(json.dumps(record, indent=1) + "\n")
    print(json.dumps(record, indent=1))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
