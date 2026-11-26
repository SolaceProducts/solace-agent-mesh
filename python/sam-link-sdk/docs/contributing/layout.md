# Repository layout

Commands, from the SDK's directory `python/sam-link-sdk` (Python 3.11+, [uv](https://docs.astral.sh/uv/)):

```bash
uv sync --all-extras
uv run pytest                    # unit tests, no network
uv run pytest -m contract        # against a real SAM build (testing.md)
uv run pytest -m e2e tests/e2e   # the claims-triage examples on a real SAM build (testing.md)
```


| Path | What it holds |
|---|---|
| `src/sam_link_sdk/agent.py`, `tasks.py`, `stop.py`, `serverless.py` | The core, as one `MeshAgent` and the collaborators it owns ([architecture.md](../architecture.md#the-core)). `agent.py`: `MeshAgent` (lifecycle, description, the public API) and the host object its transport calls; `tasks.py`: the task runner (registry, running the handler, per-task limits, concurrency and cancel); `stop.py`: SAM's stop (`check`, `guard`, `on_stop_change`, saved under `state_dir`, failing closed); `serverless.py`: `handle_wake`, `announce`, `retire`. |
| `src/sam_link_sdk/context.py`, `_run.py` | `TaskContext`, the handler's view of a task, and `_TaskRun`, the core's state for the same task (its run state, channel to the transport, usage, limits and tool scopes), which the core and the adapters reach as `ctx._run`. |
| `src/sam_link_sdk/_model.py`, `src/sam_link_sdk/events.py` | `_model.py`: the internal `Transport` interface and its data types (`IncomingTask`, `Prompt`, `TaskOutcome`, `PeerCall`, `AgentDescription`, ...), named after [link-protocol.md](../link-protocol.md). `events.py`: the step events a handler reports (public). No topics, properties or envelopes. |
| `src/sam_link_sdk/link/` | `LinkTransport`: the agent link ([agent-link.md](../agent-link.md)): `connection.py` (the WebSocket, the welcome, reconnects), `transport.py` (the protocol's state), `describer.py` (the card), `usage_ledger.py` (token usage), `giveback.py` (tasks given back), `v2.py` (its frames). |
| `src/sam_link_sdk/cli.py` | The `sam-link-sdk` CLI: `sam-link-sdk link-token`. |
| `src/sam_link_sdk/usage.py`, `memory.py`, `redact.py`, `audit.py`, `metrics.py`, `llm_errors.py` | Token totals, conversation memory, credential redaction, the audit trail, OpenTelemetry metrics, model-failure categories. |
| `src/sam_link_sdk/<framework>/` | One adapter per framework (`langchain`, `openai_agents`, `adk`, `crewai`, `strands`, `agent_framework`, `hermes`), each with `steps.py` (reporting steps), `stop.py` (SAM's stop outside the mesh), `tools.py` (approvals, asking the user, the tool manifest), `peer_tools.py` and `run.py` (`run_agent`), and no other modules. |
| `src/sam_link_sdk/_adapter.py` | What the adapters share: step events, call-id pairing, token usage, per-tool scopes and the one refusal, the approval and ask-user flows. |
| `src/sam_link_sdk/otel_steps.py` | Any framework's OpenTelemetry GenAI spans as steps ([opentelemetry.md](../opentelemetry.md)). |
| `src/sam_link_sdk/foundry.py`, `agentcore.py`, `hosted.py` | Request-driven runtime entry points ([serverless.md](../serverless.md)). |
| `examples/` | [examples.md](../examples.md). |
| `tests/` | Unit tests; `tests/contract/` runs scripted agents against a real SAM-Go, and `tests/e2e/` runs each framework's claims-triage example against one with only the model scripted ([testing.md](testing.md)). `test_adapter_conformance.py` drives every adapter through the same scenarios; `test_adapter_surface.py` checks their names and layout; `test_docs.py` checks these pages' code and links. |
