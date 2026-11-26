# Examples

## Minimal agents

The shortest runnable agent for each framework, `examples/minimal_<framework>.py`: one tool, one model, one handler. Each
is the first thing on its framework's page ([frameworks/README.md](frameworks/README.md)), and
`examples/minimal_plain.py` is the README's quick start, a handler with no framework.

| Example | Framework | Needs |
|---|---|---|
| `examples/minimal_plain.py` | None | no model |
| `examples/minimal_langchain.py` | LangChain | `pip install "sam-link-sdk[openai]"`, `OPENAI_API_KEY` |
| `examples/minimal_langgraph.py` | LangGraph | `sam-link-sdk[langgraph,openai]`, `OPENAI_API_KEY` |
| `examples/minimal_openai_agents.py` | OpenAI Agents SDK | `sam-link-sdk[openai-agents]`, `OPENAI_API_KEY` |
| `examples/minimal_adk.py` | Google ADK | `sam-link-sdk[adk]`, `OPENAI_API_KEY` |
| `examples/minimal_crewai.py` | CrewAI | `sam-link-sdk[crewai]`, `OPENAI_API_KEY` |
| `examples/minimal_strands.py` | Strands Agents | `sam-link-sdk[strands]`, `OPENAI_API_KEY` |
| `examples/minimal_agent_framework.py` | Microsoft Agent Framework | `sam-link-sdk[agent-framework]`, `OPENAI_API_KEY` |
| `examples/minimal_hermes.py` | Hermes Agent | [frameworks/hermes.md](frameworks/hermes.md) |

Set `OPENAI_BASE_URL` as well for an OpenAI-compatible endpoint. They all join SAM with the variables in the
[quick start](../README.md#quick-start) and the agent name `claims-triage`.

## The claims-triage agent, in every framework

One fuller agent, built in each framework: three tools over three made-up policies (`lookup_policy`, `route_claim`, and
`fraud_screen`, which takes 30 s so cancel can be tried), the same instructions, the same card, the same environment.
It shows approvals, scopes, peers, questions and memory together, so read the minimal agent first.

| Example | Framework | Run with |
|---|---|---|
| `examples/claims_triage_langchain.py` | LangChain (`run_agent`, a minimal tool-calling loop) | `uv run --extra openai python examples/claims_triage_langchain.py` |
| `examples/claims_triage_langgraph.py` | LangGraph (an explicit `StateGraph`) | `uv run --extra langgraph --extra openai python examples/claims_triage_langgraph.py` |
| `examples/claims_triage_openai_agents.py` | OpenAI Agents SDK | `uv run --extra openai-agents python examples/claims_triage_openai_agents.py` |
| `examples/claims_triage_adk.py` | Google ADK | `uv run --extra adk python examples/claims_triage_adk.py` |
| `examples/claims_triage_crewai.py` | CrewAI | `uv run --extra crewai python examples/claims_triage_crewai.py` |
| `examples/claims_triage_strands.py` | Strands Agents | `uv run --extra strands python examples/claims_triage_strands.py` |
| `examples/claims_triage_agent_framework.py` | Microsoft Agent Framework | `uv run --extra agent-framework python examples/claims_triage_agent_framework.py` |
| `examples/claims_triage_hermes.py` | Hermes Agent (Python 3.14; [frameworks/hermes.md](frameworks/hermes.md) for the install) | `.venv/bin/python examples/claims_triage_hermes.py` |
| `examples/claims_triage_otel.py` | Strands with no adapter, reported from its own OpenTelemetry spans | `uv run --extra strands --extra otel python examples/claims_triage_otel.py` |

`examples/claims_common.py` holds what they share: the tools as plain functions, the instructions, the settings read
from the environment, and the card.

Environment, the same for every example:

| Variable | Meaning |
|---|---|
| `SAM_LINK_URL`, `SAM_LINK_TOKEN` | The link's URL and this agent's token (`sam-link-sdk link-token`; [agent-link.md](agent-link.md)). `SAM_LINK_TRUST_STORE_PATH` for SAM with a private CA. |
| `SAM_AGENT_NAME`, `SAM_DISPLAY_NAME` | Default `claims-triage` (the name SAM knows the token by), `Claims Triage`. |
| `LLM_SERVICE_ENDPOINT`, `LLM_SERVICE_API_KEY`, `LLM_SERVICE_GENERAL_MODEL_NAME` | The model, as in SAM's `.env`. SAM's `.env` names the model with its provider prefix (`openai/<model>`), which only the ADK and CrewAI examples take: for the others, set `CLAIMS_LLM_MODEL` to the name without it, or a LiteLLM proxy answers 403. |
| `CLAIMS_PRICING_KEY` | The alias of the SAM model configuration whose prices cost this agent's model calls (`AgentConfig.spend_model_keys`). Without it SAM records the calls' tokens but leaves them unpriced, unless a model configuration's alias is the model's own name. |
| `CLAIMS_LLM_BASE_URL`, `CLAIMS_LLM_MODEL` | Optional overrides of the endpoint and model, for example a provider's own OpenAI-compatible endpoint (`https://api.anthropic.com/v1/`, `claude-sonnet-5`) when `LLM_SERVICE_ENDPOINT` is not OpenAI-compatible. |
| `CLAIMS_PEER_AGENTS` | Mesh agents the triage agent may delegate to, comma-separated, or `*` for every agent the user may invoke (default: none). |
| `CLAIMS_APPROVAL_TOOLS` | Tools that pause the task for the user's approval first, comma-separated (for example `route_claim`); the user approves in the chat or with `sam task send --hil-approve`. |
| `CLAIMS_ASK_USER` | `true` to let the model ask the user questions (Hermes: its `clarify` toolset). |

The ADK and CrewAI examples reach the model through LiteLLM, so their model name carries its provider prefix
(`anthropic/claude-sonnet-5`, or `openai/<model>` for an OpenAI-compatible endpoint such as a LiteLLM proxy); the others
call an OpenAI-compatible Chat Completions endpoint with the model name as it is. The OpenTelemetry example does not use
`CLAIMS_PEER_AGENTS`, `CLAIMS_APPROVAL_TOOLS` or `CLAIMS_ASK_USER`: spans cannot give it those
([opentelemetry.md](opentelemetry.md)).

## Other examples

- `examples/claims_intake.py`: a deterministic agent (no LLM) that shows files in, files out, and required scopes
  (`SAM_REQUIRED_SCOPES`). It reports the files it receives and returns an intake
  report file.
- `examples/serverless/`: request-driven runtimes, Azure AI Foundry and Amazon Bedrock AgentCore
  ([serverless.md](serverless.md)).
- `examples/demo/`: the claims desk demo, below.

## Trying an example

The examples are not in the installed package: clone this repository and run them from its root with
[uv](https://docs.astral.sh/uv/), as the tables say. Register the agent in SAM ([agent-link.md](agent-link.md#configure-sam)),
set the environment, run the example, and check that SAM lists it:

```bash
curl -s https://<sam-host>/api/v1/agentCards | jq '.data[].name'
sam task send "Triage claim P-1182" --agent claims-triage --url https://<sam-host>
```

In the web UI, pick the agent and open the Activity view on the answer. To try cancel, ask "Triage claim P-1182 and run a
fraud check first", then press Stop.

## The claims desk demo

`examples/demo/` is one claims desk built from eight agents, one per framework (LangChain, Google ADK, Hermes Agent,
CrewAI, Strands Agents, LangGraph, OpenAI Agents SDK, Microsoft Agent Framework). They join one SAM through the link and
hand a claim to each other through peer calls; Settlement Review asks a person to approve a large payout, and Records
Lookup shows a credential in a tool result redacted. `examples/demo/README.md` has the prerequisites, how to start it,
what to try and how to stop it.
