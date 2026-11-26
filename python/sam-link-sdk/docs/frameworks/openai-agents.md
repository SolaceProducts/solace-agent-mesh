# OpenAI Agents SDK

`sam_link_sdk.openai_agents` runs an `agents.Agent` on a mesh task. Tested with openai-agents 0.20.0; a newer version is
checked for the hooks the adapter uses when it is imported.

## Install

```bash
pip install "sam-link-sdk[openai-agents]"
```

## Minimal agent

This is the whole agent, `examples/minimal_openai_agents.py`. Set `OPENAI_API_KEY` (and `OPENAI_BASE_URL` for an OpenAI-compatible endpoint) and the SAM variables from the [quick start](../../README.md#quick-start), then run it with `python minimal_openai_agents.py`:

```python
import asyncio
import logging

from agents import Agent, OpenAIChatCompletionsModel, function_tool, set_tracing_disabled
from openai import AsyncOpenAI

from sam_link_sdk import AgentConfig, MeshAgent, TaskContext
from sam_link_sdk.openai_agents import run_agent


@function_tool
def lookup_policy(policy_id: str) -> str:
    """Look up an insurance policy by id."""
    return f"{policy_id}: active, covers water damage"


def build_model() -> OpenAIChatCompletionsModel:
    return OpenAIChatCompletionsModel(model="gpt-4o-mini", openai_client=AsyncOpenAI())


async def main() -> None:
    logging.basicConfig(level=logging.INFO)
    set_tracing_disabled(True)
    triage = Agent(
        name="claims-triage",
        instructions="You triage insurance claims.",
        model=build_model(),
        tools=[lookup_policy],
    )

    async def handle(ctx: TaskContext) -> str:
        return await run_agent(ctx, triage)

    config = AgentConfig(name="claims-triage", description="Triages insurance claims")
    await MeshAgent(config, handle).run_forever()


if __name__ == "__main__":
    asyncio.run(main())
```

Keyword arguments: `input`, `memory` (a `ConversationMemory`), `include_request_context` (appended to the agent's
instructions for this run), `max_model_calls` (10, the SDK's `max_turns`), `stream_text` (default True, see Streaming)
and `run_config`. The agent's tools are the agent's: add per-task ones with `agent.clone(tools=[...])`. A run that reaches `max_model_calls`, or that a guardrail
trips, fails the task with a message saying which (not `internal error`). A tool the user lacks the scopes for is not
offered to the model, and a call to it is refused (see Steps). A call to a tool the agent does not
have gives the model the SDK's "not found" error instead of failing the run, as in the other adapters.
An input guardrail built with `run_in_parallel=False` runs before the model is called. One that runs in parallel (the
default) checks while the first turn is written, so that turn's text is held until every such guardrail has passed, and
dropped when one trips. An output guardrail runs after
the model has answered, and Agent Mesh has no way to take back text it was streamed, so the text an output guardrail
checks is held until the run ends (see Streaming): when it trips, the task fails with the guardrail's message and the
user never sees the blocked answer.
`examples/claims_triage_openai_agents.py` is the claims-triage agent; it uses
`OpenAIChatCompletionsModel` with any OpenAI-compatible endpoint, so no OpenAI key is needed.

**Tracing:** `run_agent` turns off OpenAI trace export unless you pass your own `run_config`; otherwise the SDK uploads
each task's model inputs and tool results to the OpenAI dashboard.

## Steps

`step_reporter(ctx, agent, *, run_config=None)` returns a `SamLinkRunHooks`, per-run `RunHooks` that report each model
call and response and each tool's start and result. The SDK's hooks see neither a tool's exception nor a way to refuse
a call, so the hooks come with `hooks.agent`, a copy of `agent` (and of every agent it hands off to) prepared for the
task; run that copy. The hooks raise `RuntimeError` when they are given an agent they did not prepare, so a run cannot
quietly go without failure tracking and the scope check. With your own `Runner.run`:

```python
from agents import Runner
from sam_link_sdk.openai_agents import step_reporter


async def handle(ctx):
    hooks = step_reporter(ctx, triage_agent)
    result = await Runner.run(hooks.agent, ctx.text, hooks=hooks)
    return result.final_output
```

- **Pairing:** the SDK passes each function tool's `tool_call_id` to the tool hooks, so tool steps pair with the
  model's `function_call` by id, including parallel calls.
- **Token usage** comes from `ModelResponse.usage`: input, output, cached and reasoning tokens, and cache-write tokens only when the model goes through LiteLLM.
- **Failed tools:** the SDK catches a tool's exception (or, with `timeout_behavior="error_as_result"`, its timeout)
  inside the tool and gives the model a message instead, so no hook sees the exception. `hooks.agent` has copies of
  the function tools whose failure formatter records the call before returning the tool's own message, so the step
  shows as failed with the text the model saw, whatever `failure_error_function` the tool has. This covers the agents
  it hands off to, listed directly or through `handoff(...)`. Not copied, so their failures show as results: tools with
  an output schema (programmatic tool calling) and tools an agent loads from MCP servers.
- **Per-tool scopes:** a tool the user lacks the scopes for ([security.md](../security.md#required-scopes-rbac)) is left
  out of `hooks.agent` and of every agent it hands off to, so the model is not offered it. A call to it anyway is a tool
  the SDK does not find: `run_agent` gives the model SAM's refusal as the call's result and reports the call as refused,
  as every adapter does. The SDK runs a tool the hooks could not take off the agent (an MCP server's) without asking;
  such a call is refused when it starts, by ending the run with `ToolAccessDenied`.
- **Nested runs:** agents a run hands off to, directly or through `handoff(...)`, run in the same `Runner` and are
  prepared like the first: their steps, tokens, scopes and approvals are the task's. An agent used as a tool
  (`Agent.as_tool`) runs a `Runner` of its own, which gets only the hooks given to `as_tool`. So `hooks.agent` has a
  copy of each such tool whose every call runs a copy of its agent prepared like the first, with run hooks of its own
  that report to the same task (then the `as_tool` hooks, if any), and with the calling run's config made to refuse a
  denied tool the same way (or the `as_tool` run config, if it has one). The agent tool's model calls and tool calls
  are steps of the task between the tool call's start and its result: Agent Mesh's steps carry no parent, so they show
  in order, not nested under it. Its model calls count toward `max_llm_calls`, its tools the user lacks the scopes
  for are hidden and refused, its approvals are asked, and its model and tool calls check the task's stop first. This
  holds at any depth (an agent tool's agent may hand off, or have agent tools of its own) and behind `with_scopes` or
  `with_approval` on the agent tool. A tool
  marked as an agent tool whose agent cannot be found is refused (`NestedRunRefused`): before the run when the
  run's own agents have it, as the agent tool's failure when the agent of an agent tool has it (that agent is prepared
  when the tool is called).
  Something the task's hooks raise inside an agent tool's run (a stop) is the agent tool's failure, which the SDK tells
  the calling model; the task then stops at the calling run's next model or tool call. Per guarantee:

| Nested run | Scopes | Approvals | Stop | Steps and tokens |
|---|---|---|---|---|
| Agents handed off to | refused (hooks and tool) | asked | the task's cancel ends the run | reported |
| Agents run as tools (`Agent.as_tool`), at any depth | refused (hooks and tool) | asked | the task's cancel ends the run; each model and tool call checks the stop | reported, between the agent tool's start and result |
| A `Runner` a tool's code starts | refused by the tool (`with_scopes`) | the `with_approval` tool asks for its own task, and refuses outside it | a scoped tool refuses; the task's cancel reaches it when the tool awaits it | not reported |

- **Handoffs** become a progress update ("Handing off to ...").
- **Failed model calls:** the exception `Runner.run` raises is classified by the agent when it is a provider client
  library's error.
- **Reasoning** (with `stream_thinking`): the response's reasoning summaries.

## Scopes on the tool

Declare a tool's scopes on the tool, and build the card from the guarded tools:

```python
from sam_link_sdk.openai_agents import tool_infos, with_scopes

TOOLS = with_scopes([lookup_policy, route_claim], {"route_claim": ["tool:claims:route"]})  # function tools
config = AgentConfig(name="claims-triage", description="Triages claims", tools=tool_infos(TOOLS))
```

The guarded tool is the framework's own tool type with the check in front of its body. Wherever it is called from, it
finds the mesh task it runs for (`sam_link_sdk.current_task()`: `run_agent` sets it, the mesh agent runs your handler with
it, and the framework carries it into the asyncio tasks and threads it starts with the context) and runs only when that
task's user holds the scopes, the card declares them, and the task has been neither stopped nor answered; outside a mesh
task it refuses. The step reporter still hides the tool and refuses a call to it first, reported and audited once; the
tool's own check is what holds where no hook sees the run ([security.md](../security.md#required-scopes-rbac)).
`tool_infos(tools)` reads the scopes off the guarded tools; a name that is none of the tools is a `ValueError`. Only
function tools can carry the check: naming a hosted tool is a `ValueError`. A refusal is the call's error result.

## Approvals and questions

Build the agent's tools per task:

```python
from sam_link_sdk.openai_agents import ask_user_tool, run_agent, with_approval


async def handle(ctx):
    tools = [*with_approval(ctx, triage_agent.tools, names=["route_claim"]), ask_user_tool(ctx)]
    return await run_agent(ctx, triage_agent.clone(tools=tools))
```

`with_approval(ctx, tools, *, names=None, message="")` wraps function tools; hosted tools are returned unchanged.
`names` must be a list of names, each a function tool among `tools` (else a ValueError). A refusal is the tool's result,
`{"error": ...}` as JSON text.

A tool you mark with the SDK's own flag, `function_tool(route_claim, needs_approval=True)`, needs no `with_approval`:
`run_agent` asks the user through Agent Mesh for each call the SDK holds, with the tool's name and arguments, and
resumes the run (from `result.to_state()`) with the answer. A refusal (denied, cancelled, timed out, a channel that
cannot show a prompt, a stopped task) is told to the model with the same text as `with_approval`'s. A tool that
needs approval and also lacks the user's scopes is left out of the run, so it is never asked about.

## Peers

`mesh_agent_tools(ctx, agents=None)`, per task: `agent.clone(tools=[*agent.tools, *mesh_agent_tools(ctx)])`. The peer's
steps nest under the model's tool call.

## Streaming

`run_agent(..., stream_text=True)` runs the agent with `Runner.run_streamed` and sends the model's text deltas to the
chat as they arrive. A streamed response carries token usage only if the request asks for it, and the SDK asks only of
OpenAI's own endpoint, so the prepared agent's model settings set `include_usage` unless you set it. Pass
`stream_text=False` to send the answer whole when the run ends (`Runner.run`).

An output guardrail checks the answer only once the model has written it, and Agent Mesh has no way to take back or
replace text it was streamed (its web UI keeps the streamed text of a task that then fails). So while the answering
agent has output guardrails, or the run config has, its text is held (as the first turn's is while a parallel input
guardrail checks it) and sent in one piece when the run ends with no
guardrail tripped; when one trips, the task fails with the guardrail's message and the held text is dropped. Text an
agent without output guardrails writes (before a handoff to one that has them, say) streams as usual. An agent tool's
own run (`Agent.as_tool`) is not streamed to the chat; only the calling run's text is.

## Stop

Mesh tasks stop on their own (the task's asyncio task is cancelled, including an async tool in flight). For runs
outside the mesh, `stop_guard(agent)` returns run hooks (`StopHooks`) that raise `AgentStopped` when an agent
starts and before every model and tool call: `Runner.run(my_agent, text, hooks=stop_guard(mesh_agent))`.

## Files

No adapter reads `ctx.files` or attaches files for you ([README](README.md#limits-tools-and-files)): read `ctx.files` in the handler and pass what the model should see as `input=`. To return a file, give the model a tool that calls `ctx.attach`, `run_agent(ctx, agent.clone(tools=[*agent.tools, attach_report]))`:

```python
from agents import function_tool


@function_tool
def attach_report(name: str, text: str) -> str:
    """Attach a text file to the answer."""
    ctx.attach(name, text.encode(), "text/plain")
    return "attached"
```

The file reaches the user with the answer.

## What is not supported

- Failure tracking for the tools listed under "Failed tools" above.
- A tool the user lacks the scopes for, served by an MCP server: refused by ending the run, since the SDK gives the
  hooks no way to answer the call (see Steps).
- Agent tool steps nested under the tool call that ran them: Agent Mesh's steps carry no parent, so they show in
  order between the call's start and result (see Steps).
- Streaming an answer while an output guardrail checks it (see Streaming).
- A guardrail function or a tool whose own code calls `Runner.run` on another agent: that run is outside SAM (its
  steps, tokens and tool calls are not seen). Give the agent to this one as a handoff.
