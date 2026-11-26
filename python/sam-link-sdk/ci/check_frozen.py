# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Fails a pull request in which the bot that maintains the SDK changed a frozen file (ci/frozen-paths.txt).

The bot merges its own pull requests without a review, so the rules it is held to must be out of its reach: a required
check, not a code owner review (its GitHub App bypasses those). A pull request counts as the bot's when it opened it
or any commit in it is authored or committed by it, as when it pushes a fix onto a Dependabot pull request.
pyproject.toml is half frozen: the bot may move dependencies, never the [tool] settings (the test, lint and uv
configuration).

Usage: python3 ci/check_frozen.py <base sha> <head sha> <true|false: the bot took part>
"""

from __future__ import annotations

import fnmatch
import re
import subprocess
import sys
import tomllib
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
FROZEN = Path(__file__).resolve().parent / "frozen-paths.txt"
PYPROJECT = "python/sam-link-sdk/pyproject.toml"


def patterns(text: str) -> list[str]:
    return [line.strip() for line in text.splitlines() if line.strip() and not line.lstrip().startswith("#")]


def frozen(path: str, globs: list[str]) -> bool:
    for glob in globs:
        regex = re.escape(glob).replace(r"\*\*", ".*").replace(r"\*", "[^/]*")
        if re.fullmatch(regex, path) or fnmatch.fnmatchcase(path, glob):
            return True
    return False


def tool_settings(text: str | None) -> dict:
    if text is None:
        return {}
    return tomllib.loads(text).get("tool", {})


def violations(
    changed: list[str], globs: list[str], pyproject_before: str | None, pyproject_after: str | None
) -> list[str]:
    found = [path for path in changed if frozen(path, globs)]
    if PYPROJECT in changed and tool_settings(pyproject_before) != tool_settings(pyproject_after):
        found.append(f"{PYPROJECT} [tool]")
    return found


def _git(*args: str) -> str:
    return subprocess.run(["git", *args], cwd=ROOT, check=True, capture_output=True, text=True).stdout


def _show(ref: str, path: str) -> str | None:
    result = subprocess.run(["git", "show", f"{ref}:{path}"], cwd=ROOT, capture_output=True, text=True)
    return result.stdout if result.returncode == 0 else None


def main(argv: list[str]) -> int:
    if len(argv) != 4 or argv[3] not in ("true", "false"):
        print(__doc__.strip().splitlines()[-1], file=sys.stderr)
        return 2
    base, head, bot = argv[1], argv[2], argv[3] == "true"
    if not bot:
        print("the bot took no part in this pull request")
        return 0
    changed = _git("diff", "--name-only", f"{base}...{head}").split()
    found = violations(changed, patterns(FROZEN.read_text()), _show(base, PYPROJECT), _show(head, PYPROJECT))
    for path in found:
        print(
            f"::error file={path.split(' ')[0]},title=Frozen file::the bot may not change {path} (ci/frozen-paths.txt)"
        )
    if not found:
        print(f"the bot changed {len(changed)} files, none of them frozen")
    return 1 if found else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
