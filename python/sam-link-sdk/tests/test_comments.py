# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The code has no comments, as SAM-Go's commentcheck requires of python/sam-link-sdk (internal/lint/commentcheck there).

The rule and its scanners are ported from that checker so a violation fails here before it fails in SAM-Go. A comment
is any `#` in Python and shell and any `--` or `/* */` in SQL; the reason goes in a docstring, and the rest is deleted.
"""

from __future__ import annotations

import pathlib
import re
import subprocess

from .published_header import APACHE_HEADER, blanked_published_header

ROOT = pathlib.Path(__file__).resolve().parents[1]

RULE = (
    "comments are not allowed: rename for intent, extract a named function, introduce a named constant, "
    "or state the rule as a test name; a reason goes in a docstring (docs/contributing/testing.md#code-comments)"
)

EXCLUDED_PATH = re.compile(r"(^|/)(testdata|vendor|node_modules|examples)/|^(webui|public)/")
SHELL_DIRECTIVE = re.compile(r"^#(!|\s*shellcheck\s)")
PYTHON_DIRECTIVE = re.compile(r"^#(!|\s*(noqa|type:|pyright:|mypy:|pylint:|fmt:|isort:|ruff:)|.*-\*-.*coding)")
SQL_DIRECTIVE = re.compile(r"^--\s*\+goose\b")
HEREDOC = re.compile(r"<<-?\s*(['\"]?)([A-Za-z_][A-Za-z0-9_]*)['\"]?")
LANGUAGES = {".py": "python", ".sh": "shell", ".bash": "shell", ".sql": "sql"}
UNTRACKED_SKIPS = {".git", ".venv", "node_modules", "__pycache__", ".mypy_cache", ".ruff_cache", ".pytest_cache"}


def python_comment_start(line: str, triple: str) -> tuple[int, str]:
    i = 0
    while i < len(line):
        if triple:
            end = line[i:].find(triple)
            if end < 0:
                return -1, triple
            i += end + 3
            triple = ""
            continue
        ch = line[i]
        if ch == "#":
            return i, ""
        if line.startswith('"""', i) or line.startswith("'''", i):
            triple = line[i : i + 3]
            i += 3
        elif ch in "'\"":
            i += 1
            while i < len(line) and line[i] != ch:
                if line[i] == "\\":
                    i += 1
                i += 1
            i += 1
        else:
            i += 1
    return -1, triple


def python_comments(source: str) -> list[tuple[int, str]]:
    found = []
    triple = ""
    for number, line in enumerate(source.splitlines(), 1):
        start, triple = python_comment_start(line, triple)
        if start < 0:
            continue
        comment = line[start:]
        if (number == 1 and comment.startswith("#!")) or PYTHON_DIRECTIVE.match(comment):
            continue
        found.append((number, comment))
    return found


def shell_comment_start(line: str, quote: str) -> tuple[int, str]:
    i = 0
    while i < len(line):
        ch = line[i]
        if ch == "\\" and quote != "'":
            i += 2
            continue
        if quote:
            if ch == quote:
                quote = ""
        elif ch in "'\"`":
            quote = ch
        elif ch == "#" and (i == 0 or line[i - 1] in " \t;"):
            return i, ""
        i += 1
    return -1, quote


def heredoc_terminator(code: str) -> str:
    match = HEREDOC.search(code)
    if match is None or "<<<" in code:
        return ""
    return match.group(2)


def shell_comments(source: str) -> list[tuple[int, str]]:
    found = []
    heredoc_end = ""
    quote = ""
    for number, line in enumerate(source.splitlines(), 1):
        if heredoc_end:
            if line.lstrip("\t") == heredoc_end:
                heredoc_end = ""
            continue
        start, quote = shell_comment_start(line, quote)
        code = line
        if start >= 0:
            comment = line[start:]
            if (number != 1 or not comment.startswith("#!")) and not SHELL_DIRECTIVE.match(comment):
                found.append((number, comment))
            code = line[:start]
        if not quote:
            heredoc_end = heredoc_terminator(code)
    return found


def sql_comments(source: str) -> list[tuple[int, str]]:
    found = []
    line = 1
    i = 0
    while i < len(source):
        if source[i] == "\n":
            line += 1
        elif source[i] == "'":
            i += 1
            while i < len(source) and source[i] != "'":
                line += source[i] == "\n"
                i += 1
        elif source.startswith("$$", i):
            end = source.find("$$", i + 2)
            if end < 0:
                return found
            line += source.count("\n", i, end + 2)
            i = end + 1
        elif source.startswith("--", i):
            end = source.find("\n", i)
            end = len(source) if end < 0 else end
            comment = source[i:end]
            if not SQL_DIRECTIVE.match(comment):
                found.append((line, comment))
            i = end - 1
        elif source.startswith("/*", i):
            end = source.find("*/", i + 2)
            if end < 0:
                return found
            comment = source[i : end + 2]
            found.append((line, comment))
            line += comment.count("\n")
            i = end + 1
        i += 1
    return found


SCANNERS = {"python": python_comments, "shell": shell_comments, "sql": sql_comments}


def comments_in(path: str, source: str) -> list[tuple[int, str]]:
    language = LANGUAGES.get(pathlib.PurePosixPath(path).suffix)
    return SCANNERS[language](source) if language else []


def checked_files() -> list[str]:
    try:
        listed = subprocess.run(
            ["git", "-C", str(ROOT), "ls-files", "-z", "--cached", "--others", "--exclude-standard"],
            check=True,
            capture_output=True,
        ).stdout.decode()
        paths = [p for p in listed.split("\0") if p]
    except (OSError, subprocess.CalledProcessError):
        paths = [
            p.relative_to(ROOT).as_posix()
            for p in ROOT.rglob("*")
            if p.is_file() and not UNTRACKED_SKIPS.intersection(p.relative_to(ROOT).parts)
        ]
    return sorted(
        p
        for p in set(paths)
        if pathlib.PurePosixPath(p).suffix in LANGUAGES and not EXCLUDED_PATH.search(p) and (ROOT / p).is_file()
    )


def test_code_has_no_comments():
    found = []
    for path in checked_files():
        for number, comment in comments_in(path, blanked_published_header((ROOT / path).read_text())):
            found.append(f"{path}:{number}: {comment.splitlines()[0][:80]}")
    assert not found, RULE + "\n" + "\n".join(found)


def test_python_directives_and_shebang_are_allowed():
    source = (
        "#!/usr/bin/env python3\n"
        "# -*- coding: utf-8 -*-\n"
        "import os  # noqa: F401\n"
        "x: int = foo()  # type: ignore[no-any-return]\n"
        "# fmt: off\n"
        "# ruff: noqa: E501\n"
    )
    assert python_comments(source) == []


def test_python_flags_every_other_comment():
    source = (
        "# a note\n"
        "x = 1  # trailing\n"
        "# pragma: no cover\n"
        "# nosec\n"
        "# SPDX-License-Identifier: Apache-2.0\n"
        "y = 2\n"
        "z = 3  # noqa  # and a reason\n"
    )
    assert [number for number, _ in python_comments(source)] == [1, 2, 3, 4, 5]


def test_python_hash_inside_a_string_or_docstring_is_not_a_comment():
    source = 'a = "# not one"\nb = \'# nor this\'\n"""\n# in a docstring\n"""\nc = f"{a}#"\n'
    assert python_comments(source) == []


def test_python_shebang_is_allowed_only_on_the_first_line():
    assert python_comments("x = 1\n#!/usr/bin/env python3\n") == []
    assert python_comments("\n# note\n") == [(2, "# note")]


def test_shell_allows_shebang_and_shellcheck_and_flags_the_rest():
    source = "#!/usr/bin/env bash\n# shellcheck disable=SC2086\nset -e\n# note\necho a # trailing\necho '#' \"#\" a#b\n"
    assert shell_comments(source) == [(4, "# note"), (5, "# trailing")]


def test_shell_heredoc_body_is_not_scanned():
    source = "cat <<EOF\n# in the body\nEOF\n# after\n"
    assert shell_comments(source) == [(4, "# after")]


def test_sql_flags_line_and_block_comments_but_not_goose_or_strings():
    source = "-- +goose Up\nSELECT '--', 1; -- note\n/* block\n*/\n"
    assert sql_comments(source) == [(2, "-- note"), (3, "/* block\n*/")]


def test_excluded_paths():
    assert EXCLUDED_PATH.search("examples/claims.py")
    assert EXCLUDED_PATH.search("python/x/examples/claims.py")
    assert not EXCLUDED_PATH.search("src/sam_link_sdk/examples.py")
    assert not EXCLUDED_PATH.search("tests/test_examples.py")


def test_the_apache_header_the_publish_adds_is_not_a_comment():
    """The public copy's files carry the header the publish writes, after the shebang, and pass as the source does."""
    source = "#!/usr/bin/env python3\n" + "\n".join(APACHE_HEADER) + "\n\nx = 1\n"
    assert python_comments(blanked_published_header(source)) == []
    assert shell_comments(blanked_published_header(source)) == []


def test_a_busl_header_is_still_a_comment():
    """An SDK file published under BUSL-1.1 instead of Apache-2.0 fails, since that header is not the one accepted."""
    source = (
        "# Copyright Solace Corporation\n#\n"
        "# Use of this software is governed by the Business Source License (BUSL-1.1)\n"
        "# included in the LICENSE file.\n\nx = 1\n"
    )
    assert [number for number, _ in python_comments(blanked_published_header(source))] == [1, 2, 3, 4]
