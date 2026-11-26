"""
Regenerate the ToolResult wire-format fixtures from Python SAM's real
`ToolResult.to_serializable()` implementation.

Fixtures land alongside this script and are consumed by Go tests in
internal/tool/wire_fixture_test.go and internal/tool/remote/tool_fixture_test.go
to pin wire compatibility between Python and Go.

Regenerate whenever Python bumps TOOL_RESULT_SCHEMA_VERSION or changes the
`to_serializable()` output shape.

Prerequisites:
    1. `pip install pydantic` into a venv (Python SAM ToolResult uses Pydantic).
    2. A local checkout of solace-agent-mesh next to this repo.

Usage:
    python3 regenerate_fixtures.py

The script locates Python SAM via the `SAM_PY_REPO` env var, falling back to
the sibling checkout path. It loads tool_result.py directly (bypassing the
`__init__.py` side-effects that pull in google-adk) so Pydantic is the only
required dependency.
"""

import importlib.util
import json
import os
import sys

DEFAULT_SAM_PY_REPO = os.path.expanduser(
    "~/Desktop/solace-repos/solace-agent-mesh"
)
TOOL_RESULT_REL = "src/solace_agent_mesh/agent/tools/tool_result.py"


def load_tool_result_module():
    repo = os.environ.get("SAM_PY_REPO", DEFAULT_SAM_PY_REPO)
    path = os.path.join(repo, TOOL_RESULT_REL)
    if not os.path.exists(path):
        sys.exit(
            f"tool_result.py not found at {path}. Set SAM_PY_REPO to the "
            f"solace-agent-mesh checkout root."
        )
    spec = importlib.util.spec_from_file_location("sam_tool_result", path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


def write(out_dir, name, result):
    path = os.path.join(out_dir, f"{name}.json")
    with open(path, "w") as f:
        json.dump(result.to_serializable(), f, indent=2, sort_keys=True)
        f.write("\n")
    print(f"wrote {path}")


def main():
    out_dir = os.path.dirname(os.path.abspath(__file__))

    tr = load_tool_result_module()
    ToolResult = tr.ToolResult
    DataObject = tr.DataObject
    DataDisposition = tr.DataDisposition

    write(out_dir, "ok_with_inline_data",
          ToolResult.ok(
              "Generated report",
              data={"row_count": 1000, "summary": "3 files processed"},
          ))

    write(out_dir, "ok_with_text_data_object",
          ToolResult(
              status="success",
              message="Generated report",
              data_objects=[DataObject(
                  name="report.csv",
                  content="col1,col2\n1,2\n3,4",
                  mime_type="text/csv",
                  disposition=DataDisposition.AUTO,
                  description="Analysis report",
              )],
          ))

    write(out_dir, "ok_with_binary_data_object",
          ToolResult(
              status="success",
              message="Rendered chart",
              data_objects=[DataObject(
                  name="chart.png",
                  content=bytes([0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A]),
                  mime_type="image/png",
                  disposition=DataDisposition.ARTIFACT,
                  description="Generated chart",
              )],
          ))

    write(out_dir, "error_with_code",
          ToolResult.error("File not found: data.csv", code="FILE_NOT_FOUND"))

    write(out_dir, "ok_with_preview_and_metadata",
          ToolResult(
              status="success",
              message="Large dataset prepared",
              data={"stats": {"rows": 250000}},
              data_objects=[DataObject(
                  name="dataset.csv",
                  content="col1,col2,col3\n1,2,3\n4,5,6\n... (249997 more rows)",
                  mime_type="text/csv",
                  disposition=DataDisposition.ARTIFACT_WITH_PREVIEW,
                  preview="col1,col2,col3\n1,2,3\n4,5,6\n...(249997 more rows)",
                  description="Full dataset export",
                  metadata={"source": "bigquery", "query_id": "q-42"},
              )],
          ))

    write(out_dir, "ok_with_multiple_data_objects",
          ToolResult(
              status="success",
              message="Two files",
              data_objects=[
                  DataObject(name="first.json", content='{"a": 1}',
                             mime_type="application/json"),
                  DataObject(name="second.json", content='{"b": 2}',
                             mime_type="application/json"),
              ],
          ))

    write(out_dir, "partial_with_error_code",
          ToolResult.partial(
              "Processed 2 of 3 files",
              data={"succeeded": 2, "failed": 1},
              error_code="PARTIAL_FAILURE",
          ))


if __name__ == "__main__":
    main()
