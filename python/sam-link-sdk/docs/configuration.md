# Configuration

An agent is `MeshAgent(config, handler)`: an `AgentConfig` and the handler. It joins SAM through the link, built from
the environment ([agent-link.md](agent-link.md#point-the-agent-at-sam)) as `config.name`.

```python
from sam_link_sdk import AgentConfig, MeshAgent, Skill

config = AgentConfig(
    name="claims-triage",
    description="Triages incoming insurance claims",
    skills=[Skill(id="triage", name="Triage a claim", description="Routes a claim to the right queue")],
)
agent = MeshAgent(config, handle)
```

## AgentConfig

Start with `name`, `description`, `skills` and `tools`; the rest has defaults.

Card and identity:

| Field | Default | Meaning |
|---|---|---|
| `name`, `description` | | The agent's name (`[A-Za-z0-9_-]+`, as SAM names agents) and what it does. |
| `display_name`, `version` | `""`, `1.0.0` | The version is on the card. SAM shows the name its operator configures, so `display_name` only names the agent in its own metrics and traces. |
| `skills` | `[]` | `Skill(id, name, description)`. |
| `tools` | `[]` | `ToolInfo(name, description, required_scopes)`: the card's tool manifest (each adapter's `tool_infos(tools)`, with the scopes `with_scopes` gave the tools; Hermes' `tool_infos(tools, scopes=...)`), and the scopes each tool needs ([security.md](security.md#required-scopes-rbac)). |
| `input_schema`, `output_schema` | `None` | JSON schemas; a workflow takes a node's schemas from the card. |
| `welcome_message`, `suggestions` | `""`, `[]` | The web UI's welcome screen; `Suggestion(label, prompt, auto_send)`. |
| `peers` | `[]` | The mesh agents this one calls, drawn by the console. When set, `mesh_agent_tools` and `ctx.available_agents()` offer only these; calls are still allowed or refused per task. |
| `owner`, `team`, `framework`, `data_access`, `mcp_servers` | | What the card's provenance extension says about the agent (`DataAccess(system, classification, access)`, `McpServer(name, url)`). An empty `framework` takes the framework of the one adapter this process imported (`LangChain` for LangGraph too); set it to name another. |
| `streaming` | `True` | Advertised on the card (`capabilities.streaming`) and nothing else: the agent streams its steps and text whenever the task's caller takes a stream, whatever this says. |
| `required_scopes` | `[]` | Scopes a caller needs ([security.md](security.md)). |

Running tasks:

| Field | Default | Meaning |
|---|---|---|
| `max_llm_calls`, `task_timeout_seconds` | 20, 600 | Per-task limits; 0 turns one off ([handlers.md](handlers.md)). |
| `max_concurrent_tasks` | 0 | Tasks run at once; 0 is no limit. |
| `max_call_depth` | 3 | A task deeper than this is refused, as SAM agents refuse it; 0 disables the check. |
| `keepalive_seconds` | 120 | A silent task gets a progress update this often. |
| `shutdown_grace_seconds` | 20 | How long running tasks may finish on shutdown, and how long an answer held for SAM to deliver its task again waits for it: a stop can take the whole grace with no task running. |
| `max_inline_file_bytes` | 7,000,000 | Base64 file bytes in one final answer. |
| `stream_thinking` | `False` | Send the model's reasoning to the chat. |
| `spend_model_keys` | `{}` | The SAM model configuration that prices each model in SAM's cost ledger ([behaviour.md](behaviour.md)). |

Security and operations:

| Field | Default | Meaning |
|---|---|---|
| `offline_grace_seconds`, `state_dir`, `exit_on_stop` | 900, `~/.sam_link_sdk`, `False` | SAM's stop ([security.md](security.md)). |
| `audit_sink` | `None` | Receives `AuditEvent`s; `None` logs one JSON line per record on `sam_link_sdk.audit`. |
| `metrics` | `None` | `OtelMetrics()` exports SAM's metrics ([opentelemetry.md](opentelemetry.md)). |

## Environment

| Variable | Read by | Meaning |
|---|---|---|
| `SAM_LINK_URL`, `SAM_LINK_TOKEN_FILE` or `SAM_LINK_TOKEN`, `SAM_LINK_TRUST_STORE_PATH`, `SAM_LINK_MAX_FRAME_BYTES`, `SAM_LINK_ALLOW_INSECURE` | `MeshAgent` | The link ([agent-link.md](agent-link.md#point-the-agent-at-sam) says what each does). |
| `SAM_LINK_URL` | `sam_link_sdk.foundry`, `sam_link_sdk.agentcore` | The link URL a woken agent connects to ([serverless.md](serverless.md)). |
| `<NAME>_SECRET_ID` | `sam_link_sdk.agentcore.load_secrets()` | An AWS Secrets Manager secret whose value becomes `<NAME>` before the agent is built (`SAM_LINK_TOKEN_SECRET_ID`, say). |

The examples read more ([examples.md](examples.md)).
