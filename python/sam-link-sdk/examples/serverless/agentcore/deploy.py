# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""Deploy this folder to Amazon Bedrock AgentCore Runtime (direct code deployment) and wait until it is ready.

    python deploy.py --agent claims_assistant --role-arn arn:aws:iam::<account>:role/<execution role> \\
        --bucket <code bucket> --env-file agent.env \\
        --discovery-url https://<idp>/.well-known/openid-configuration --audience <aud> [--claim appid=<client id>] \\
        [--subnets subnet-a,subnet-b --security-groups sg-x]

It packages the dependencies for AgentCore's Linux arm64 (uv pip install --python-platform aarch64-manylinux2014),
this folder and the installed sam_link_sdk SDK into a zip, uploads it to S3, and creates the runtime, or updates it
when one with that name exists. Prints the runtime's ARN, for the invocation URL in the entrypoint's `wake`
configuration (README.md).

Inbound: only callers with a token from --discovery-url's identity provider, for --audience, and matching each
--claim, can invoke the runtime: that's SAM's agent link entrypoint, which wakes it. Network: with --subnets, the
runtime runs in your VPC, so its connection to SAM's gateway leaves through your NAT gateway's fixed address; without,
it uses AgentCore's public network.

--env-file holds the runtime's environment as KEY=VALUE lines; keep secrets out of it and point at Secrets Manager
instead (NAME_SECRET_ID; see sam_link_sdk.agentcore). The file is never packaged.

Needs: boto3 and uv, with AWS credentials allowed to create AgentCore runtimes and pass the execution role.
"""

from __future__ import annotations

import argparse
import os
import shutil
import stat
import subprocess
import tempfile
import time
import zipfile
from pathlib import Path

import sam_link_sdk

HERE = Path(__file__).parent
SKIP_NAMES = {"__pycache__", "deploy.py", ".venv", "build"}
SKIP_SUFFIXES = (".env", ".link-token", ".zip")
PYTHON = "3.13"


def _build(target: Path) -> None:
    subprocess.run(
        [
            "uv",
            "pip",
            "install",
            "--quiet",
            "--python-platform",
            "aarch64-manylinux2014",
            "--python-version",
            PYTHON,
            "--only-binary=:all:",
            "--target",
            str(target),
            "-r",
            str(HERE / "requirements.txt"),
        ],
        check=True,
    )
    for path in HERE.rglob("*"):
        rel = path.relative_to(HERE)
        if path.is_file() and not SKIP_NAMES & set(rel.parts) and not path.name.endswith(SKIP_SUFFIXES):
            (target / rel).parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(path, target / rel)
    sdk = Path(sam_link_sdk.__file__).parent
    shutil.copytree(sdk, target / "sam_link_sdk", dirs_exist_ok=True, ignore=shutil.ignore_patterns("__pycache__"))


def _zip(source: Path, zip_path: Path) -> int:
    """AgentCore needs files readable (644) and directories traversable (755)."""
    with zipfile.ZipFile(zip_path, "w", zipfile.ZIP_DEFLATED) as z:
        for path in sorted(source.rglob("*")):
            if "__pycache__" in path.parts:
                continue
            info = zipfile.ZipInfo.from_file(path, path.relative_to(source))
            mode = 0o755 if path.is_dir() or os.access(path, os.X_OK) else 0o644
            info.external_attr = ((stat.S_IFDIR if path.is_dir() else stat.S_IFREG) | mode) << 16
            if path.is_dir():
                z.writestr(info, b"")
            else:
                info.compress_type = zipfile.ZIP_DEFLATED
                z.writestr(info, path.read_bytes())
    return zip_path.stat().st_size


def _env(path: str | None) -> dict[str, str]:
    if not path:
        return {}
    pairs = (line.strip().split("=", 1) for line in Path(path).read_text().splitlines())
    return {k: v for k, v in (p for p in pairs if len(p) == 2 and not p[0].startswith("#"))}


def _claims(values: list[str]) -> list[dict]:
    claims = []
    for value in values:
        name, _, expected = value.partition("=")
        claims.append(
            {
                "inboundTokenClaimName": name,
                "inboundTokenClaimValueType": "STRING",
                "authorizingClaimMatchValue": {
                    "claimMatchValue": {"matchValueString": expected},
                    "claimMatchOperator": "EQUALS",
                },
            }
        )
    return claims


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("--agent", required=True, help="the runtime's name: letters, digits and underscores")
    parser.add_argument("--role-arn", required=True, help="the execution role AgentCore runs the agent as")
    parser.add_argument("--bucket", required=True, help="an S3 bucket for the code package")
    parser.add_argument("--region", default=os.environ.get("AWS_REGION", "us-east-1"))
    parser.add_argument("--env-file", help="KEY=VALUE lines for the runtime's environment (no secrets)")
    parser.add_argument("--discovery-url", required=True, help="the identity provider's OpenID configuration URL")
    parser.add_argument("--audience", action="append", default=[], help="an accepted token audience (repeatable)")
    parser.add_argument("--client", action="append", default=[], help="an accepted client_id claim (repeatable)")
    parser.add_argument(
        "--claim", action="append", default=[], help="NAME=VALUE a token's claim must equal (repeatable)"
    )
    parser.add_argument("--subnets", default="", help="comma-separated private subnets (VPC network mode)")
    parser.add_argument("--security-groups", default="", help="comma-separated security groups (VPC network mode)")
    parser.add_argument("--idle-seconds", type=int, default=600, help="end an idle session after this long")
    args = parser.parse_args()

    import boto3

    with tempfile.TemporaryDirectory() as tmp:
        build, zip_path = Path(tmp) / "package", Path(tmp) / f"{args.agent}.zip"
        _build(build)
        print("packaged", _zip(build, zip_path), "bytes", flush=True)
        key = f"{args.agent}/{int(time.time())}.zip"
        boto3.client("s3", region_name=args.region).upload_file(str(zip_path), args.bucket, key)

    authorizer = {"discoveryUrl": args.discovery_url}
    if args.audience:
        authorizer["allowedAudience"] = args.audience
    if args.client:
        authorizer["allowedClients"] = args.client
    if args.claim:
        authorizer["customClaims"] = _claims(args.claim)
    network = (
        {
            "networkMode": "VPC",
            "networkModeConfig": {
                "subnets": args.subnets.split(","),
                "securityGroups": args.security_groups.split(","),
            },
        }
        if args.subnets
        else {"networkMode": "PUBLIC"}
    )
    spec = dict(
        agentRuntimeArtifact={
            "codeConfiguration": {
                "code": {"s3": {"bucket": args.bucket, "prefix": key}},
                "runtime": "PYTHON_" + PYTHON.replace(".", "_"),
                "entryPoint": ["main.py"],
            }
        },
        roleArn=args.role_arn,
        networkConfiguration=network,
        authorizerConfiguration={"customJWTAuthorizer": authorizer},
        protocolConfiguration={"serverProtocol": "HTTP"},
        # An approval holds the session up to 45 minutes; the session itself ends after idle_seconds without work.
        lifecycleConfiguration={"idleRuntimeSessionTimeout": args.idle_seconds, "maxLifetime": 8 * 3600},
        environmentVariables={"PYTHONNOUSERSITE": "1", **_env(args.env_file)},
        description="Solace Agent Mesh agent, woken per request by SAM's agent link",
    )

    control = boto3.client("bedrock-agentcore-control", region_name=args.region)
    existing = next(
        (
            r
            for page in control.get_paginator("list_agent_runtimes").paginate()
            for r in page["agentRuntimes"]
            if r["agentRuntimeName"] == args.agent
        ),
        None,
    )
    if existing:
        runtime = control.update_agent_runtime(agentRuntimeId=existing["agentRuntimeId"], **spec)
    else:
        runtime = control.create_agent_runtime(agentRuntimeName=args.agent, **spec)
    runtime_id, version = runtime["agentRuntimeId"], runtime["agentRuntimeVersion"]
    print("runtime", runtime_id, "version", version, flush=True)
    status = ""
    for _ in range(90):
        time.sleep(10)
        got = control.get_agent_runtime(agentRuntimeId=runtime_id)
        status = got["status"]
        if status in ("READY", "CREATE_FAILED", "UPDATE_FAILED"):
            break
    if status != "READY":
        raise SystemExit(f"runtime {runtime_id} is {status or 'not ready'}: {got.get('failureReason', '')}")
    print("ready:", runtime["agentRuntimeArn"])


if __name__ == "__main__":
    main()
