# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""
Example: artifact in → artifact out with tool_cli.

Annotate a parameter as ``Artifact`` and the SDK pre-loads the file content
before your function runs.  Return a ``DataObject`` with
``disposition=DataDisposition.ARTIFACT`` to produce an output artifact the
agent can attach to its reply.

Run in schema-discovery mode:
    python artifact_inout_tool.py --schema

Run with a runner_args.json:
    python artifact_inout_tool.py runner_args.json

runner_args.json (sketch — the STR populates real paths):
    {
      "args": {"doc": "report.txt"},
      "artifact_metadata": {
        "doc": {
          "filename": "report.txt",
          "local_path": "/sandbox/input/report.txt",
          "mime_type": "text/plain"
        }
      },
      "output_dir": "/tmp/output"
    }
"""

from sam_tool_sdk import Artifact, DataDisposition, DataObject, ToolResult, tool_cli


async def uppercase_file(doc: Artifact, suffix: str = "_upper") -> ToolResult:
    """Upper-case every character in a text file and return it as a new artifact.

    Args:
        doc: the input text file
        suffix: appended to the output filename before the extension
    """
    # doc is an Artifact — the SDK loaded the file bytes before calling us.
    text = doc.as_text()
    transformed = text.upper()

    # Derive an output filename from the input.
    parts = doc.filename.rsplit(".", 1)
    if len(parts) == 2:
        out_name = f"{parts[0]}{suffix}.{parts[1]}"
    else:
        out_name = f"{doc.filename}{suffix}"

    return ToolResult.ok(
        message=f"Produced {out_name} ({len(transformed)} chars)",
        data_objects=[
            DataObject(
                name=out_name,
                content=transformed.encode(),
                mime_type="text/plain",
                disposition=DataDisposition.ARTIFACT,
                description="Upper-cased version of the input file",
            )
        ],
    )


# pyproject.toml entry:
#   [project.scripts]
#   uppercase_file = "artifact_inout_tool:cli"
cli = tool_cli(uppercase_file)


if __name__ == "__main__":
    cli()
