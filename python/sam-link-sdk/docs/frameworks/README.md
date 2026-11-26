# Framework adapters

One adapter per framework, and one for any framework that emits OpenTelemetry GenAI spans
([opentelemetry.md](../opentelemetry.md)). Every adapter reports model and tool steps, token usage and the audit trail,
and shares the names below, so knowing one adapter is knowing most of them. Each framework's page starts with a minimal
agent you can run.

| Framework | Extra | Lowest supported | Page |
|---|---|---|---|
| LangChain, LangGraph | `langchain`, `langgraph` | langchain-core 1.6.9, langgraph 1.2.14 | [langchain.md](langchain.md) |
| OpenAI Agents SDK | `openai-agents` | openai-agents 0.20.0 | [openai-agents.md](openai-agents.md) |
| Google ADK | `adk` | google-adk 2.14.1 | [adk.md](adk.md) |
| CrewAI | `crewai` | crewai 1.15.27 | [crewai.md](crewai.md) |
| Strands Agents | `strands` | strands-agents 1.61.1 | [strands.md](strands.md) |
| Microsoft Agent Framework | `agent-framework` | agent-framework-core 1.22.0 | [agent-framework.md](agent-framework.md) |
| Hermes Agent (experimental) | none, see its page | a checkout, Python 3.14 | [hermes.md](hermes.md) |
| Any framework with OpenTelemetry GenAI spans | `otel` | opentelemetry-sdk 1.45.2 | [opentelemetry.md](../opentelemetry.md) |

Lowest supported is the extra's lower bound in `pyproject.toml`, tested with each framework installed alone. The newest
tested is the release in `uv.lock` on main: every change to it passes the full suite first
([framework-releases.md](../contributing/framework-releases.md)). The extras set no upper bound on the framework's
version. A newer version installs, and the adapter checks for the hooks it uses when it is imported and raises
`FrameworkDrift` (exported from `sam_link_sdk`) if one is gone.

## The shared names

| Name | What it is |
|---|---|
| `run_agent(ctx, <framework object>, *, input=None, memory=None, include_request_context=False, max_model_calls=10, stream_text=True, ...) -> str` | Runs the framework on the task and returns the answer, with steps reported and per-tool scopes enforced. The shared keywords come in this order, then the framework's own (`system_prompt`, `run_config`, `kickoff_inputs`, `middleware`, `approval`, `extra_tools`). LangGraph's is `run_graph`. |
| `step_reporter(...)` | The framework's own object that reports steps and refuses a call to a tool the user lacks the scopes for. `run_agent` adds it for you; use it for runs you start yourself. |
| `stop_guard(agent, ...)` | The framework's own object that raises `AgentStopped` while SAM has stopped the agent, for runs outside a mesh task. |
| `with_approval(...)` | Chosen tools ask the task's user before each call. `names` is keyword-only and must be a list of tool names. |
| `ask_user_tool(...)` | A tool the model calls to ask the user a question. |
| `mesh_agent_tools(...)` | One `peer_<name>` tool per agent on the mesh the user may invoke ([delegation.md](../delegation.md)). |
| `with_scopes(tools, scopes)` | The way to declare the scopes a tool needs (tool name to scopes). The tool refuses a user without them ([security.md](../security.md#required-scopes-rbac)). Hermes has none. |
| `tool_infos(tools)` | The card's tool list from the framework's tools, with the scopes `with_scopes` gave them. |

The arguments differ where the framework's shape does. An ADK agent is built once with its Runner, so ADK takes no `ctx`;
CrewAI and Strands take a `build(ctx)` function that makes a fresh crew or agent per task; Hermes builds its tools inside
the agent, so its approvals are a policy for `run_agent`. Each page has the same sections: install, minimal agent,
steps, scopes on the tool, approvals and questions, peers, streaming, stop, files, and what is not supported.

## What each framework supports

| | LangChain / LangGraph | OpenAI Agents | ADK | CrewAI | Strands | Agent Framework | Hermes |
|---|---|---|---|---|---|---|---|
| `run_agent` takes | `model, tools` | `agent` | `runner` | `build` | `build` | `agent` | `build` |
| Streams the answer | yes | yes | yes | yes, the last task | yes | yes | yes |
| Framework's own approval flag asks the user | LangGraph `interrupt()` | `needs_approval` | `require_confirmation` | no such flag | interrupts | `approval_mode` | its dangerous-command prompt |
| Conversation memory | `memory=` or a checkpointer | `memory=` | ADK session per chat | `memory=` | `memory=` | `memory=` | `memory=` |
| Reasoning stream (`stream_thinking`) | yes, not through `ChatOpenAI` | yes | yes | no | yes | yes, not through the Chat Completions client | yes |
| Task-wide limit `max_llm_calls` | yes | yes | yes | yes | yes | yes | yes |

The exact behaviour of each adapter, including nested runs, is in [contributing/adapters.md](../contributing/adapters.md).

## A tool the user may not use

A tool given scopes with `with_scopes` checks the task's grants where it runs, and refuses outside a mesh task. In every
adapter but the OpenTelemetry one, `run_agent` also keeps the tool from the model when the user lacks the scopes, and
refuses a call to it anyway: the tool does not run, the model is told `Access denied: you do not have the scopes
required to use <tool>.`, the call shows as failed, and the audit trail records `tool access denied`. The OpenTelemetry
adapter only reads spans, so it cannot refuse a call (a tool guarded with `with_scopes` still refuses itself).

A tool whose approval is denied or times out does not run either. The model gets an error result (`The user did not
approve this call to <tool> (denied), so it was not run.`), the audit trail records `tool approval refused`, and the task
carries on, so the model can explain or choose something else.

A call counts as failed when the framework says so (the tool raised, or the framework marked the result failed) or when
its result is `{"error": ...}` and nothing else. A result that has an `error` key among others is a success.

## Limits, tools and files

One word means one thing: `max_model_calls` is how many model calls one `run_agent` may make, and `ConversationMemory`'s
`max_turns` is how many conversation turns it keeps. A run that is still calling the model past `max_model_calls` fails
the task saying so (a framework that answers at its limit is marked). The task-wide `max_llm_calls`
([handlers.md](../handlers.md#per-task-limits)) is separate and enforced by the SDK for every framework.

| Framework | `max_model_calls` (default 10) | At the limit |
|---|---|---|
| LangChain | the loop's model calls | the task fails |
| OpenAI Agents | the SDK's `max_turns` | the task fails |
| ADK | `RunConfig.max_llm_calls`, replacing the one in the `run_config` you pass | the task fails |
| Hermes | the built agent's `max_iterations` | the task fails |
| CrewAI | `max_iter` of each agent of the built crew (CrewAI counts per agent) | the agent makes one more model call without tools, and the task completes |
| Agent Framework | not offered: the limit is on the chat client every task shares, set `function_invocation_configuration["max_iterations"]` where you build it | one more model call without tools, and the task completes |
| Strands | not offered: Strands has no limit of its own, only the task-wide `max_llm_calls` applies | n/a |
| LangGraph (`run_graph`) | not offered: `config={"recursion_limit": n}` counts graph steps, not model calls | the task fails |

`tools=` is the agent's whole tool list only where there is no agent object to hold them: LangChain's
`run_agent(ctx, model, tools)`. Where the agent is built elsewhere, `extra_tools=` adds this task's tools (the peers, the
question tool, the approval-wrapped ones) to the agent's own for the run: Agent Framework and Hermes. Elsewhere the
tools go where the framework puts them: in `build(ctx)` (CrewAI, Strands), in `agent.clone(tools=...)` (OpenAI Agents),
in the agent's `tools` and toolsets (ADK).

Files are the handler's to read and write, as in [handlers.md](../handlers.md#files); no adapter does it for you. In the
SAM this release is tested with, an attachment arrives in `ctx.files` with its name and an `artifact://` URI and no
bytes. When SAM sends the bytes, read `ctx.files` in the handler and pass the text you want the model to see as `input=`
(`run_agent(ctx, ..., input=f"{ctx.text}\n\n{file_text}")`). To return a file, call `ctx.attach(name, content,
mime_type)` from a tool you build for the task (a closure over `ctx`), or return `TaskResult(text, files=[...])` from a
handler that wraps `run_agent`. The file reaches the user with the answer.

## Streaming the answer

The user sees the answer as the model writes it: where an adapter streams, its `run_agent` sends each text delta to
`ctx.emit_text`, and SAM's web UI then drops the final task's text instead of showing the answer twice. The model is
called through the framework's streaming API, and a streamed response carries token usage only if the provider is asked
for it. OpenAI Agents asks for you. With LangChain's `ChatOpenAI` on an OpenAI-compatible endpoint (a LiteLLM proxy, a
local server) set `stream_usage=True`, or SAM's token analytics shows zero tokens. Pass `stream_text=False` to send the answer whole when the run ends.

| Framework | Streams by default | How |
|---|---|---|
| LangChain | yes | `astream` in the loop, the chunks added up into the AI message (tool calls included) |
| OpenAI Agents | yes | `Runner.run_streamed`, its text delta events; the agent's model settings ask for usage unless they say otherwise. Text an output guardrail checks is held until the run ends, and text a parallel input guardrail checks until it passes; neither is sent if a guardrail trips |
| ADK | yes | `RunConfig(streaming_mode=StreamingMode.SSE)`, merged into the `run_config` you pass (one that sets a mode keeps it); text from the partial events, which the steps ignore |
| Strands | yes | `stream_async`, its text events |
| Agent Framework | yes | `agent.run(..., stream=True)`, its updates' text |
| LangGraph | yes, through `run_graph` | `graph.astream` in `run_graph` (messages, updates and values modes), the AI messages' text; a resumed run after an approval streams too |
| CrewAI | yes | the last task's model calls streamed for the call (`call_stream_override`), their text from `LLMStreamChunkEvent`; not tool calls, earlier tasks or CrewAI's ReAct thoughts ([crewai.md](crewai.md#streaming)) |
| Hermes | yes | the agent's `stream_delta_callback`, without the text a response writes once it has begun a tool call |

