# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Claims intake: a deterministic agent (no LLM) that shows files in, files out, and required scopes, joined to Solace
Agent Mesh through the agent link.

It reads the files a user attaches in SAM (the gateway sends them inline, because the card says this agent
has no artifact store), and returns an intake report as a file (the gateway stores it in the user's session).

    export SAM_LINK_URL=wss://<gateway>/gw/<gateway_id>/v1/connect SAM_LINK_TOKEN=$(cat claims-intake.link-token)
    SAM_REQUIRED_SCOPES=claims:_:read uv run python examples/claims_intake.py

Environment: SAM_LINK_URL and SAM_LINK_TOKEN (docs/agent-link.md), SAM_AGENT_NAME (default claims-intake),
SAM_REQUIRED_SCOPES (comma-separated scopes a user needs to call it; SAM checks them before it delivers a task).
"""

from __future__ import annotations

import asyncio
import hashlib
import logging
import os

from sam_link_sdk import AgentConfig, File, MeshAgent, Skill, TaskContext, TaskResult


async def handle(ctx: TaskContext) -> TaskResult:
    lines = [f"# Intake report for task {ctx.task_id}", "", f"Request: {ctx.text}", "", "## Attachments"]
    for f in ctx.files:
        if f.content is None:
            lines.append(f"- {f.name}: not delivered inline ({f.uri})")
        else:
            lines.append(
                f"- {f.name} ({f.mime_type or 'unknown type'}, {len(f.content)} bytes, sha256 {hashlib.sha256(f.content).hexdigest()[:12]})"
            )
    if not ctx.files:
        lines.append("- none")
    if ctx.user_id:
        lines += ["", f"Requested by {ctx.user_id}"]
    report = "\n".join(lines) + "\n"
    return TaskResult(
        text=f"Received {len(ctx.files)} attachment(s); the intake report is attached.",
        files=[File(f"intake-{ctx.task_id[:8]}.md", report.encode(), mime_type="text/markdown")],
    )


async def main() -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")
    scopes = [s.strip() for s in os.environ.get("SAM_REQUIRED_SCOPES", "").split(",") if s.strip()]
    config = AgentConfig(
        name=os.environ.get("SAM_AGENT_NAME", "claims-intake"),
        description="Logs incoming claim documents and returns an intake report",
        display_name="Claims Intake",
        skills=[
            Skill(id="intake", name="Log claim documents", description="Checks attachments and files an intake report")
        ],
        required_scopes=scopes,
    )
    agent = MeshAgent(config, handle)
    await agent.run_forever()


if __name__ == "__main__":
    asyncio.run(main())
