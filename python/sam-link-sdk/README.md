# Solace Agent Mesh Link SDK

Status: **Early Access**, version 0.1.0. The API can change in any release, and [CHANGELOG.md](https://github.com/SolaceProducts/solace-agent-mesh/blob/main/python/sam-link-sdk/CHANGELOG.md) lists each
change. Apache 2.0, part of the [Solace Agent Mesh](https://solace.com/products/agent-mesh/) (SAM) project.

`sam-link-sdk` lets an agent you wrote in Python join a running Solace Agent Mesh as a first-class agent. Write a handler,
or use LangChain, LangGraph, the OpenAI Agents SDK, Google ADK, CrewAI, Strands Agents, Microsoft Agent Framework, Hermes
Agent, or any framework that emits OpenTelemetry GenAI spans. The agent connects to SAM over one WebSocket, the link. It
then appears in SAM's agent list, can be chatted with from the web UI, shows its model and tool steps in the Activity
Monitor, reports token usage to SAM's analytics, and is governed by SAM: required scopes, tool approvals and the audit
trail. The SDK does not run a mesh of its own.

## Install

Python 3.11 or later. The base install is the core and the link and needs only `websockets`; each framework has an extra
([pick your framework](#pick-your-framework)).

```bash
pip install sam-link-sdk
pip install "sam-link-sdk[langchain]"
```

## Quick start

You need a SAM with the `agent_link` app enabled and its URL ([what that is](https://github.com/SolaceProducts/solace-agent-mesh/blob/main/python/sam-link-sdk/docs/agent-link.md#what-you-need); on a
shared SAM it is the administrator's step), and SAM's `sam` command line client to try the agent.
[Agent Mesh Desktop](https://github.com/SolaceProducts/solace-agent-mesh#installing-agent-mesh-desktop) gives you both on
your own computer.

1. Issue the agent's token. The command writes it to the file and prints its SHA-256 digest:

   ```bash
   sam-link-sdk link-token --out claims-triage.link-token
   ```

2. Register the agent in SAM: add `claims-triage` and the digest to the `agent_link` app's configuration and restart SAM
   ([the configuration](https://github.com/SolaceProducts/solace-agent-mesh/blob/main/python/sam-link-sdk/docs/agent-link.md#configure-sam)).

3. Point the agent at SAM:

   ```bash
   export SAM_LINK_URL=wss://<sam-host>/gw/<gateway_id>/v1/connect
   export SAM_LINK_TOKEN_FILE=$PWD/claims-triage.link-token
   ```

   Use `ws://localhost:8800/gw/agent-link/v1/connect` for a SAM on your own machine over plain HTTP (8800 is the port of SAM's web UI).

4. Save this as `agent.py` and run `python agent.py`. It needs no model:

```python
import asyncio
import logging

from sam_link_sdk import AgentConfig, MeshAgent, TaskContext


async def handle(ctx: TaskContext) -> str:
    await ctx.progress("Looking the claim up")
    return f"Received: {ctx.text}"


async def main() -> None:
    logging.basicConfig(level=logging.INFO)
    config = AgentConfig(name="claims-triage", description="Triages insurance claims")
    await MeshAgent(config, handle).run_forever()


if __name__ == "__main__":
    asyncio.run(main())
```

The log shows `agent link open agent=claims-triage ... protocol=2` and `agent started`. Check that SAM lists the agent,
then send it a task:

```bash
curl -s https://<sam-host>/api/v1/agentCards | jq '.data[].name'
sam task send "Triage claim P-1182" --agent claims-triage --url https://<sam-host>
```

The reply is `Received: Triage claim P-1182`. Or pick the agent in the web UI's chat. If it does not join,
[docs/agent-link.md](https://github.com/SolaceProducts/solace-agent-mesh/blob/main/python/sam-link-sdk/docs/agent-link.md#when-the-agent-does-not-join) lists what the log says and what to change.

## Pick your framework

Each page starts with a short agent you can run, and every adapter shares the same names
([docs/frameworks/README.md](https://github.com/SolaceProducts/solace-agent-mesh/blob/main/python/sam-link-sdk/docs/frameworks/README.md)). The LangChain agent, for example, is this handler in place of
the one above (`pip install "sam-link-sdk[openai]"`, and set
`OPENAI_API_KEY`):

```python
async def handle(ctx: TaskContext) -> str:
    return await run_agent(ctx, model, [lookup_policy], system_prompt="You triage insurance claims.")
```

with `model = ChatOpenAI(model="gpt-4o-mini", stream_usage=True)`. On an OpenAI-compatible endpoint (a LiteLLM proxy, a
local server), `stream_usage=True` is what makes SAM's token analytics show real counts instead of zero. The full
runnable file is [docs/frameworks/langchain.md](https://github.com/SolaceProducts/solace-agent-mesh/blob/main/python/sam-link-sdk/docs/frameworks/langchain.md#minimal-agent).

| Framework | Extra | Minimum version |
|---|---|---|
| [LangChain](https://github.com/SolaceProducts/solace-agent-mesh/blob/main/python/sam-link-sdk/docs/frameworks/langchain.md) | `langchain` (`openai` adds `langchain-openai`) | langchain-core 1.6.9 |
| [LangGraph](https://github.com/SolaceProducts/solace-agent-mesh/blob/main/python/sam-link-sdk/docs/frameworks/langchain.md) | `langgraph` | langgraph 1.2.14 |
| [OpenAI Agents SDK](https://github.com/SolaceProducts/solace-agent-mesh/blob/main/python/sam-link-sdk/docs/frameworks/openai-agents.md) | `openai-agents` | openai-agents 0.20.0 |
| [Google ADK](https://github.com/SolaceProducts/solace-agent-mesh/blob/main/python/sam-link-sdk/docs/frameworks/adk.md) | `adk` | google-adk 2.14.1 |
| [CrewAI](https://github.com/SolaceProducts/solace-agent-mesh/blob/main/python/sam-link-sdk/docs/frameworks/crewai.md) | `crewai` | crewai 1.15.27 |
| [Strands Agents](https://github.com/SolaceProducts/solace-agent-mesh/blob/main/python/sam-link-sdk/docs/frameworks/strands.md) | `strands` | strands-agents 1.61.1 |
| [Microsoft Agent Framework](https://github.com/SolaceProducts/solace-agent-mesh/blob/main/python/sam-link-sdk/docs/frameworks/agent-framework.md) | `agent-framework` | agent-framework-core 1.22.0 |
| [OpenTelemetry GenAI spans](https://github.com/SolaceProducts/solace-agent-mesh/blob/main/python/sam-link-sdk/docs/opentelemetry.md), for any other framework | `otel` | opentelemetry-sdk 1.45.2 |
| [Hermes Agent](https://github.com/SolaceProducts/solace-agent-mesh/blob/main/python/sam-link-sdk/docs/frameworks/hermes.md) (experimental, no extra) | | a checkout, Python 3.14 |

The extras `foundry` and `agentcore` are for request-driven runtimes ([docs/serverless.md](https://github.com/SolaceProducts/solace-agent-mesh/blob/main/python/sam-link-sdk/docs/serverless.md)).

## Documentation

[docs/README.md](https://github.com/SolaceProducts/solace-agent-mesh/blob/main/python/sam-link-sdk/docs/README.md) has a list of tasks ("I want to add tools, ask the user, call another agent, limit
cost, deploy serverless") and a short glossary. Working on the SDK itself: [CONTRIBUTING.md](https://github.com/SolaceProducts/solace-agent-mesh/blob/main/python/sam-link-sdk/CONTRIBUTING.md).

## Getting help

- Bugs: email [support@solace.com](mailto:support@solace.com).
- Feature requests, questions and discussion: [Solace Community](https://community.solace.com/c/solace-agent-mesh).
- Security vulnerabilities: email [support@solace.com](mailto:support@solace.com) ([SECURITY.md](https://github.com/SolaceProducts/solace-agent-mesh/blob/main/python/sam-link-sdk/SECURITY.md)).

## Licence

Apache License 2.0, in [LICENSE](https://github.com/SolaceProducts/solace-agent-mesh/blob/main/python/sam-link-sdk/LICENSE).
