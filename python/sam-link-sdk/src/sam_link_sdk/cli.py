# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""The ``sam-link-sdk`` command.

    sam-link-sdk link-token --out claims-triage.link-token

``link-token`` issues a token for an agent that joins through SAM's agent link entrypoint (sam_link_sdk.link): it writes
a new random token to a private file and prints the SHA-256 digest the entrypoint's configuration takes. The token
itself is never printed.
"""

from __future__ import annotations

import argparse
import hashlib
import os
import secrets
import stat
import sys
from pathlib import Path

LINK_TOKEN_BYTES = 32


class TokenExists(RuntimeError):
    """The token file exists already: the agent's current token stays."""


def _write_private(path: Path, content: str) -> None:
    """Write `content` to a new file only the owner can read.

    The file is created here or not at all: a file or a link (even a dangling one) already at the path is never written
    through, so the token cannot land where someone else put a link to.
    """
    path.parent.mkdir(parents=True, exist_ok=True)
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0)
    try:
        fd = os.open(path, flags, stat.S_IRUSR | stat.S_IWUSR)
    except FileExistsError:
        raise TokenExists(
            f"{path} exists: the agent's current token stays; remove the file to issue a new one"
        ) from None
    with os.fdopen(fd, "w") as f:
        f.write(content)


def link_token(out: Path) -> str:
    """Write a new agent link token to ``out`` (owner-only) and return its SHA-256 hex digest, the ``token_sha256``
    of the agent in the entrypoint's configuration."""
    token = secrets.token_urlsafe(LINK_TOKEN_BYTES)
    _write_private(out, token)
    return hashlib.sha256(token.encode("utf-8")).hexdigest()


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="sam-link-sdk", description="Tools for an agent on SAM's agent link.")
    sub = parser.add_subparsers(dest="command", required=True)
    token_cmd = sub.add_parser("link-token", help="issue an agent link token; prints the digest SAM's entrypoint takes")
    token_cmd.add_argument("--out", required=True, help="file the token is written to (owner-only); never printed")
    args = parser.parse_args(argv)
    try:
        digest = link_token(Path(args.out))
    except TokenExists as exc:
        print(f"sam-link-sdk: {exc}", file=sys.stderr)
        return 1
    print(digest)
    return 0


if __name__ == "__main__":
    sys.exit(main())
