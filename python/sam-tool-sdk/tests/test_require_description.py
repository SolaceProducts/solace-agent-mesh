# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""A tool must advertise a non-empty description.

The description is the highest-salience field an LLM uses to choose a tool, and
strict providers (e.g. Amazon Bedrock's Converse API) reject a tool advertised
with an empty description, failing the whole request. The SDK enforces this at
schema-discovery time so the author is caught during development.
"""

import pytest

from sam_tool_sdk.cli import _build_schema_from_func
from sam_tool_sdk.tool_runner import _require_description

def test_require_description_rejects_empty():
    for bad in ("", "   ", "\t\n"):
        with pytest.raises(ValueError, match="no description"):
            _require_description("mytool", bad)
    with pytest.raises(ValueError, match="no description"):
        _require_description("mytool", None)

def test_require_description_accepts_nonempty():
    _require_description("mytool", "Does a thing")

def _documented(x: str):
    """Does a documented thing."""
    return None

def _undocumented(x: str):
    return None

def test_build_schema_requires_docstring():
    _build_schema_from_func(_documented)
    with pytest.raises(ValueError, match="no description"):
        _build_schema_from_func(_undocumented)
