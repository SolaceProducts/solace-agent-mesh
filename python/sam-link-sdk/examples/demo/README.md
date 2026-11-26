# Claims desk: eight agents, eight frameworks, one mesh

A small insurance claims desk built from eight agents, each in a different agent framework. They join one Solace
Agent Mesh (SAM) through the SDK's agent link and hand a claim to each other through SAM. It shows what the SDK gives an
agent whatever framework built it: SAM lists it, you chat with it, its model and tool steps show in Activity, it calls
its peers through SAM, a risky tool waits for a person's approval, tool results are redacted, and SAM can stop it.

Every name, policy and number is made up. The agents call a real model, so they need a model key.

```
claims-intake (LangChain) ─┬─ policy-desk (Google ADK) ───── regulations (Hermes Agent)
                           ├─ fraud-screen (CrewAI) ──────── records-lookup (Strands Agents)
                           ├─ repair-estimator (LangGraph) ─ parts-pricing (OpenAI Agents SDK)
                           └─ settlement-review (Microsoft Agent Framework)
```

| Agent | Framework | What it does | SDK features it shows |
|---|---|---|---|
| `claims_intake_langchain.py` | LangChain | Takes a claim through the desk | `run_agent`, peers (`AgentConfig.peers`, `mesh_agent_tools`), a fan-out to three peers at once |
| `policy_desk_adk.py` | Google ADK | Coverage and payable amount; asks Regulations for the rule | `run_agent` with an ADK `Runner`, `step_reporter`, peers |
| `regulations_hermes.py` | Hermes Agent | Cites the settlement rule of a jurisdiction | `run_agent` with a Hermes `AIAgent` (Python 3.14, optional) |
| `fraud_screen_crewai.py` | CrewAI | Scores fraud risk; asks Records Lookup for the claim history | `run_agent` with `build(ctx)`, peers |
| `records_lookup_strands.py` | Strands Agents | Earlier claims on a policy | `run_agent`, redaction of a credential in a tool result |
| `repair_estimator_langgraph.py` | LangGraph | Totals a repair; asks Parts Pricing for prices | `run_graph`, peers |
| `parts_pricing_openai.py` | OpenAI Agents SDK | Catalogue prices and labour hours | `run_agent`, tools |
| `settlement_review_maf.py` | Microsoft Agent Framework | Settles a claim; a payout over $5,000 waits for a person | `with_approval` |

`common.py` holds the made-up data and the settings the agents share. Each agent file is one agent: its tools, its
model, its handler and its card.

## Prerequisites

- Python 3.11 or later and [uv](https://docs.astral.sh/uv/). Regulations needs Python 3.14 and its own environment
  ([docs/frameworks/hermes.md](../../docs/frameworks/hermes.md)); without it the desk runs with seven agents, and Policy
  Desk reports that it cannot reach Regulations.
- A running Solace Agent Mesh with the `agent_link` app enabled, and a way to restart it. Adding agents
  to SAM is a change to SAM's configuration ([docs/agent-link.md](../../docs/agent-link.md#configure-sam)),
  so on a shared deployment it is the administrator's step.
- A model key. Policy Desk and Fraud Screen reach the model through LiteLLM, so their model name carries its provider;
  the other agents call an OpenAI-compatible endpoint with the plain name:

  ```bash
  export LLM_SERVICE_ENDPOINT=https://api.openai.com/v1
  export LLM_SERVICE_API_KEY=<your key>
  export LLM_SERVICE_GENERAL_MODEL_NAME=openai/<model>
  export DEMO_OPENAI_MODEL=<model>
  ```

  For Anthropic, set `DEMO_OPENAI_BASE_URL=https://api.anthropic.com/v1/`, `DEMO_OPENAI_MODEL=claude-sonnet-5` and
  `LLM_SERVICE_GENERAL_MODEL_NAME=anthropic/claude-sonnet-5`. `common.py` lists every setting.

## Start it

Run these from the SDK's directory, `python/sam-link-sdk`.

1. Install the SDK with every framework extra:

   ```bash
   uv sync --all-extras
   ```

2. Issue a link token for each agent. This writes the tokens (never committed) and their digests to
   `examples/demo/stack/tokens/`:

   ```bash
   examples/demo/stack/link_tokens.sh
   ```

3. Add the agents to SAM. `examples/demo/stack/agent_link.yaml` is the `agent_link` app for all eight agents, each
   known by its token's digest and allowed to call only its own peers. Export the digests, add the file to the
   configuration SAM loads, and restart SAM:

   ```bash
   set -a; . examples/demo/stack/tokens/digests.env; set +a
   ```

   Its broker settings are the ones for a local `sam run --embedded`; change them to your deployment's.

4. Start the agents, with SAM's URL (`ws://<sam host>:<port>/gw/agent-link/v1/connect`, or `wss://` for a
   deployment with TLS):

   ```bash
   export SAM_LINK_URL=ws://localhost:8800/gw/agent-link/v1/connect
   examples/demo/stack/start_agents.sh
   ```

   Logs go to `$TMPDIR/sam-demo/<agent>.log` (`DEMO_LOG_DIR` moves them). A good start logs
   `agent link open agent=<name>` and then `agent started`. If an agent does not join, see "When the agent does not
   join" in [docs/agent-link.md](../../docs/agent-link.md). To run one agent in the foreground instead, with its own
   token: `SAM_LINK_TOKEN=$(cat examples/demo/stack/tokens/policy-desk.link-token) uv run --extra adk python
   examples/demo/policy_desk_adk.py`.

   To include Regulations, point `HERMES_PYTHON` at the interpreter of the Hermes environment before
   `start_agents.sh`.

## What to try

Open SAM's web UI.

- **The agents are listed.** The Agents page shows all eight, each with its framework, skills and tools, next to SAM's
  own agents.
- **Hand a claim through the desk.** Chat with Claims Intake: "Process claim C-7781". Open Activity on the answer: one
  claim flows LangChain, then Google ADK, CrewAI, LangGraph and Microsoft Agent Framework (and Hermes, Strands and the
  OpenAI Agents SDK a hop further), with every model call and tool step in one timeline and the token usage per agent.
- **Approve a payout.** C-7781 settles for $7,400, over Settlement Review's $5,000 limit, so SAM asks you to approve
  `settle_large` even though the request came from an agent two hops away. Deny it once to see the claim end without a
  payment. C-7790 ($3,150) settles without asking.
- **See a refusal.** C-7802 is on a lapsed policy: Policy Desk reports it uncovered and Fraud Screen scores it high
  risk, so Settlement Review does not pay.
- **See a redaction.** Ask Records Lookup: "Which policy is claim C-7802 on?" Its archive client returns an API token
  with the answer; the tool result in Activity shows `<REDACTED>`.
- **Stop an agent mid-claim.** Start "Process claim C-7790", and stop Parts Pricing from SAM's Agents page while Repair
  Estimator waits for prices (Parts Pricing takes 10 s per price; `PARTS_SUPPLIER_SECONDS` changes that). The claim ends
  with the estimate unavailable instead of hanging. A stop is SAM's: SAM gives a stopped agent no tasks, and
  the agent keeps the stop across a restart until SAM releases it.
- **Call an agent without the UI.** `sam task send "Is claim C-7781 covered?" --agent policy-desk --url http://<sam host>:<port>`.

The claims are C-7781 (home, water damage, $8,400), C-7790 (auto, collision, $3,150) and C-7802 (home, storm, $14,900,
lapsed policy).

### OpenTelemetry metrics (optional)

With `DEMO_OTLP_ENDPOINT` set, each agent also exports OpenTelemetry metrics (model calls, tokens, tool runs). To try it,
run a collector that receives OTLP/HTTP on port 4318 and serves Prometheus on 8889:

```bash
docker run -d --name sam-demo-otel -p 4318:4318 -p 8889:8889 \
  -v "$PWD/examples/demo/stack/otel-collector.yaml:/etc/otelcol/config.yaml" otel/opentelemetry-collector:latest
export DEMO_OTLP_ENDPOINT=http://localhost:4318
examples/demo/stack/start_agents.sh
curl -s localhost:8889/metrics | grep -i sam
```

See [docs/opentelemetry.md](../../docs/opentelemetry.md).

## Stop it

```bash
examples/demo/stack/stop_agents.sh
docker rm -f sam-demo-otel    # only if you started the collector
```

Remove the agents from SAM's configuration and restart it to take them off SAM, and delete
`examples/demo/stack/tokens/` to discard the tokens.
