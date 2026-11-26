# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Regression tests for the pydantic-free SDK core.

pydantic pulls in pydantic_core, whose compiled .so is bound to the CPython
minor version it was built against. A bundled Python tool built against a
different minor than the STR runtime then fails discovery at import with
`No module named 'pydantic_core._pydantic_core'`. The SDK core was made
pydantic-free so the bundle imports under any CPython 3.10+ the STR ships;
pydantic stays an optional extra, consulted lazily only when a tool author
declares a pydantic BaseModel as a tool parameter.

These tests lock that guarantee: importing the package and round-tripping
ToolResult/DataObject must work with pydantic genuinely absent, so a stray
top-level `import pydantic` can never silently creep back into the import
path. The absence is simulated in a fresh subprocess (the dev venv has
pydantic installed for the optional-path test), so it holds regardless of
what is installed.
"""

from __future__ import annotations

import subprocess
import sys
import textwrap

from sam_tool_sdk import DataObject, DataDisposition, ToolResult

def test_core_imports_and_roundtrips_without_pydantic() -> None:
    script = textwrap.dedent(
        """
        import sys
        sys.modules["pydantic"] = None
        sys.modules["pydantic_core"] = None

        import sam_tool_sdk
        from sam_tool_sdk import ToolResult, DataObject, DataDisposition

        assert "pydantic" not in sys.modules or sys.modules["pydantic"] is None

        r = ToolResult.ok(
            "done",
            data={"x": 1},
            data_objects=[
                DataObject(name="a.txt", content="hello"),
                DataObject(name="b.bin", content=b"\\xff\\xfe",
                           disposition=DataDisposition.ARTIFACT),
            ],
        )
        ser = r.to_serializable()
        assert ser["_schema"] == "ToolResult"
        assert ser["data"] == {"x": 1}
        objs = ser["data_objects"]
        assert objs[0]["disposition"] == "auto" and objs[0]["is_binary"] is False
        assert objs[1]["disposition"] == "artifact" and objs[1]["is_binary"] is True

        rt = ToolResult.from_serialized(ser)
        assert rt.status == "success" and rt.message == "done"
        assert rt.data_objects[1].content == b"\\xff\\xfe"

        # The lazy pydantic path must still not have imported pydantic.
        assert sys.modules.get("pydantic") is None
        print("PYDANTIC_FREE_OK")
        """
    )
    proc = subprocess.run(
        [sys.executable, "-c", script],
        capture_output=True,
        text=True,
    )
    assert proc.returncode == 0, f"subprocess failed:\nstdout={proc.stdout}\nstderr={proc.stderr}"
    assert "PYDANTIC_FREE_OK" in proc.stdout

def test_model_dump_shim_returns_canonical_wire() -> None:
    result = ToolResult.ok("done", data={"n": 1})
    assert result.model_dump() == result.to_serializable()
    assert result.model_dump()["_schema"] == "ToolResult"

    obj = DataObject(name="c.bin", content=b"\xff", disposition=DataDisposition.ARTIFACT)
    assert obj.model_dump() == obj.to_serializable()
    assert obj.model_dump()["disposition"] == "artifact"
