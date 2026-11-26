# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The license header the publish to the public repository adds to each Python and shell file.

The source here has no header, since SAM's commentcheck allows none. The publish (`.github/source-sync/copy.bara.sky`
in the source repository) writes this one, after a shebang or coding line, into every file of an Apache-2.0 path, so
the tests that read files accept it there and only there. A file that got the BUSL-1.1 header instead was published
under the wrong license, and still fails.
"""

from __future__ import annotations

APACHE_HEADER = (
    "# Copyright Solace Corporation",
    "#",
    "# Use of this software is governed by the Apache License, Version 2.0",
    "# (Apache-2.0) included in the LICENSE-APACHE file.",
)


def _directive_lines(lines: list[str]) -> int:
    count = 1 if lines and lines[0].startswith("#!") else 0
    if len(lines) > count and lines[count].startswith("#") and "coding" in lines[count]:
        count += 1
    return count


def without_published_header(source: str) -> str:
    """The source as it is in the source repository: the header and the blank line the publish puts after it removed."""
    lines = source.split("\n")
    keep = _directive_lines(lines)
    end = keep + len(APACHE_HEADER)
    if tuple(lines[keep:end]) != APACHE_HEADER:
        return source
    if len(lines) > end and lines[end] == "":
        end += 1
    return "\n".join(lines[:keep] + lines[end:])


def blanked_published_header(source: str) -> str:
    """The source with the header's lines emptied, so line numbers still match the published file."""
    lines = source.split("\n")
    keep = _directive_lines(lines)
    end = keep + len(APACHE_HEADER)
    if tuple(lines[keep:end]) != APACHE_HEADER:
        return source
    return "\n".join(lines[:keep] + [""] * len(APACHE_HEADER) + lines[end:])
