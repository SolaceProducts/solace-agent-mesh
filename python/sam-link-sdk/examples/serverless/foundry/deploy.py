# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Deploy this folder as an Azure AI Foundry hosted agent (Invocations protocol, remote build) and route the agent's
endpoint to the new version.

    python deploy.py --project-endpoint https://<account>.services.ai.azure.com/api/projects/<project> \\
                     --agent claims-assistant --env-file agent.env

--env-file holds the deployment's environment as KEY=VALUE lines (the link token, the model key); it is read here and
never packaged, and neither is a *.link-token file. The sam_link_sdk SDK is copied into the package from the installed one.
Signs in with DefaultAzureCredential (az login), which needs a role that can create agents on the project.

Needs: pip install azure-ai-projects azure-identity
"""

from __future__ import annotations

import argparse
import stat
import tempfile
import time
import zipfile
from pathlib import Path

import sam_link_sdk

HERE = Path(__file__).parent
SKIP_NAMES = {"__pycache__", "deploy.py", ".venv", "requirements.txt"}
SKIP_SUFFIXES = (".env", ".link-token", ".zip")


def _add(z: zipfile.ZipFile, name: Path | str, data: bytes) -> None:
    """Foundry's remote build reads the package as another user, so every file goes in readable (644): a file it
    cannot read is reported as missing (a requirements.txt written with ZipFile.writestr's default 600 is "No Python
    dependency manifest found")."""
    info = zipfile.ZipInfo(Path(name).as_posix(), date_time=time.localtime()[:6])
    info.external_attr = (stat.S_IFREG | 0o644) << 16
    info.compress_type = zipfile.ZIP_DEFLATED
    z.writestr(info, data)


def _package(zip_path: Path) -> int:
    sdk = Path(sam_link_sdk.__file__).parent
    with zipfile.ZipFile(zip_path, "w", zipfile.ZIP_DEFLATED) as z:
        _add(z, "requirements.txt", (HERE / "requirements.txt").read_bytes())
        for path in HERE.rglob("*"):
            rel = path.relative_to(HERE)
            if path.is_file() and not SKIP_NAMES & set(rel.parts) and not path.name.endswith(SKIP_SUFFIXES):
                _add(z, rel, path.read_bytes())
        for path in sdk.rglob("*"):
            if path.is_file() and "__pycache__" not in path.parts:
                _add(z, Path("sam_link_sdk") / path.relative_to(sdk), path.read_bytes())
    return zip_path.stat().st_size


def _env(path: str | None) -> dict[str, str]:
    if not path:
        return {}
    pairs = (line.strip().split("=", 1) for line in Path(path).read_text().splitlines())
    return {k: v for k, v in (p for p in pairs if len(p) == 2 and not p[0].startswith("#"))}


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("--project-endpoint", required=True)
    parser.add_argument("--agent", required=True, help="the hosted agent's name in Foundry")
    parser.add_argument("--env-file", help="KEY=VALUE lines for the deployment's environment")
    parser.add_argument("--cpu", default="0.5")
    parser.add_argument("--memory", default="1Gi")
    args = parser.parse_args()

    from azure.ai.projects import AIProjectClient
    from azure.ai.projects.models import (
        AgentEndpointConfig,
        CodeConfiguration,
        CodeDependencyResolution,
        FixedRatioVersionSelectionRule,
        HostedAgentDefinition,
        InvocationsProtocolConfiguration,
        ProtocolConfiguration,
        ProtocolVersionRecord,
        VersionSelector,
    )
    from azure.identity import DefaultAzureCredential

    zip_path = Path(tempfile.gettempdir()) / f"{args.agent}.zip"
    print("packaged", _package(zip_path), "bytes")
    environment = {"PYTHONNOUSERSITE": "1", "SAM_AGENT_NAME": args.agent, **_env(args.env_file)}
    with (
        zip_path.open("rb") as code,
        DefaultAzureCredential() as cred,
        AIProjectClient(endpoint=args.project_endpoint, credential=cred) as client,
    ):
        created = client.agents.create_version_from_code(
            agent_name=args.agent,
            description="Solace Agent Mesh agent, woken per request by SAM's agent link",
            definition=HostedAgentDefinition(
                cpu=args.cpu,
                memory=args.memory,
                code_configuration=CodeConfiguration(
                    runtime="python_3_13",
                    entry_point=["python", "main.py"],
                    dependency_resolution=CodeDependencyResolution.REMOTE_BUILD,
                ),
                environment_variables=environment,
                protocol_versions=[ProtocolVersionRecord(protocol="invocations", version="1.0.0")],
            ),
            code=code,
        )
        print("created version", created.version, flush=True)
        status = ""
        for _ in range(90):
            time.sleep(10)
            status = client.agents.get_version(agent_name=args.agent, agent_version=created.version)["status"]
            if status in ("active", "failed"):
                break
        if status != "active":
            raise SystemExit(
                f"version {created.version} is {status or 'not ready'}: "
                f"{dict(client.agents.get_version(agent_name=args.agent, agent_version=created.version))}"
            )
        client.agents.update_details(
            agent_name=args.agent,
            agent_endpoint=AgentEndpointConfig(
                version_selector=VersionSelector(
                    version_selection_rules=[
                        FixedRatioVersionSelectionRule(agent_version=created.version, traffic_percentage=100)
                    ]
                ),
                protocol_configuration=ProtocolConfiguration(invocations=InvocationsProtocolConfiguration()),
            ),
        )
        print("endpoint routed to version", created.version)


if __name__ == "__main__":
    main()
