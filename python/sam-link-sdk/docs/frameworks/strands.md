# Strands Agents (AWS)

`sam_link_sdk.strands` runs a Strands `Agent` on a mesh task. Tested with strands-agents 1.61.1; a newer version is checked
for the hooks the adapter uses when it is imported.

## Install

```bash
pip install "sam-link-sdk[strands]"     # strands-agents with its OpenAI model
```

## Minimal agent

A Strands agent holds its conversation and rejects concurrent runs, so `run_agent(ctx, build)` takes a function that
builds a fresh agent for each task, and adds the step reporter to it:

This is the whole agent, `examples/minimal_strands.py`. Set `OPENAI_API_KEY` (and `OPENAI_BASE_URL` for an OpenAI-compatible endpoint) and the SAM variables from the [quick start](../../README.md#quick-start), then run it with `python minimal_strands.py`:

```python
import asyncio
import logging

from strands import Agent, tool
from strands.models.openai import OpenAIModel

from sam_link_sdk import AgentConfig, MeshAgent, TaskContext
from sam_link_sdk.strands import run_agent


@tool
def lookup_policy(policy_id: str) -> str:
    """Look up an insurance policy by id."""
    return f"{policy_id}: active, covers water damage"


def build_model() -> OpenAIModel:
    return OpenAIModel(model_id="gpt-4o-mini")


async def main() -> None:
    logging.basicConfig(level=logging.INFO)
    model = build_model()

    def build(ctx: TaskContext) -> Agent:
        return Agent(
            model=model,
            system_prompt="You triage insurance claims.",
            tools=[lookup_policy],
            callback_handler=None,
        )

    async def handle(ctx: TaskContext) -> str:
        return await run_agent(ctx, build)

    config = AgentConfig(name="claims-triage", description="Triages insurance claims")
    await MeshAgent(config, handle).run_forever()


if __name__ == "__main__":
    asyncio.run(main())
```

Keyword arguments: `input`, `memory` (a `ConversationMemory`), `include_request_context` (appended to the system
prompt) and `stream_text` (default True, see Streaming). The agent's tools belong in `build(ctx)`. There is no
`max_model_calls`: Strands' event loop has no limit on model calls of its own, so only the task-wide `max_llm_calls`
([handlers.md](../handlers.md)) bounds a run. A model that stops for want of output tokens (`max_tokens`) fails the task saying so. `examples/claims_triage_strands.py` is the claims-triage agent; it uses Strands' `OpenAIModel` with any
OpenAI-compatible endpoint, so no OpenAI key is needed.

## Steps

`step_reporter(ctx)` returns `SamLinkHooks`, a hook provider (`Agent(hooks=[step_reporter(ctx)])`; `run_agent` adds
it). Strands' `BeforeModelCallEvent` / `AfterModelCallEvent` / `BeforeToolCallEvent` / `AfterToolCallEvent` give the
steps.

- **Pairing:** tool steps pair with the model's call by `toolUseId`, including parallel calls.
- **Token usage** from the model response's `metadata.usage` (input, output, cache read and write where the provider reports them: Anthropic and Bedrock do, the OpenAI model
  reports reads only; no reasoning tokens). Strands' Anthropic
  and Bedrock models count the cache tokens outside `inputTokens` and inside `totalTokens`; the adapter adds them to
  the input then, so every provider is counted alike. Strands' OpenAI model fails the model call (a `TypeError`) on a
  usage whose `prompt_tokens` and `completion_tokens` were renamed, after the adapter has logged its `token usage
  unread` warning ([behaviour.md](../behaviour.md)).
- **Hook order:** SAM's hooks run before yours, so SAM sees a tool call before an approval hook asks about it.
- **Failed model calls** are recorded with their category for the task's error reply.
- **Reasoning** (with `stream_thinking`): reasoning blocks.
- **Tracing:** Strands' own OpenTelemetry tracing is off unless you configure it (or read it with
  [the OpenTelemetry adapter](../opentelemetry.md)).
- **Per-tool scopes:** the before-tool hook refuses a call to a tool the user lacks the scopes for (`cancel_tool`), so
  the tool does not run, and reports it as every adapter does ([security.md](../security.md#required-scopes-rbac)).
  Strands has no public way to take a tool off a built agent, so build with `ctx.permitted_tools(tools)` to keep it out
  of the model's list too (it matches a `@tool(name=...)` tool by the name the model calls it).
- **Nested runs:** an agent given to another as a tool (`tools=[inner]`, or `inner.as_tool()`) runs with its own hooks.
  When a run starts, the step reporter finds every such agent among the tools, at any depth, and routes its hooks to
  the task for that run: its steps and tokens are reported, its tool calls checked, and the outer agent's
  `with_approval` hooks asked. An agent that runs tools in the background (`Agent(background_tasks=...)`), outer or
  nested, is refused before the run (`NestedRunRefused`): such a tool outlives the model turn, where a stop of the task
  would not reach it. Once the task is stopped the hooks refuse every tool call and end the run at its next model call.
  A `@tool` whose own code builds and runs another `Agent` runs it outside the hooks: a scoped tool there still refuses
  a user without its scopes, but approvals are not asked; give that agent as a tool instead. Per guarantee:

| Nested run | Scopes | Approvals | Stop | Steps and tokens |
|---|---|---|---|---|
| Agents given as tools, at any depth | refused (hooks and tool) | asked | ends at the next model or tool call | reported |
| An agent with `background_tasks` | refused before the run | | | |
| An agent a tool's code builds and runs | refused by the tool (`with_scopes`) | not asked (approval is a hook) | a scoped tool refuses; the task's cancel reaches it when the tool awaits it | not reported |

## Scopes on the tool

Declare a tool's scopes on the tool, and build the card from the guarded tools:

```python
from sam_link_sdk.strands import tool_infos, with_scopes

TOOLS = with_scopes([lookup_policy, route_claim], {"route_claim": ["tool:claims:route"]})  # @tool functions
config = AgentConfig(name="claims-triage", description="Triages claims", tools=tool_infos(TOOLS))
```

The guarded tool is the framework's own tool type with the check in front of its body. Wherever it is called from, it
finds the mesh task it runs for (`sam_link_sdk.current_task()`: `run_agent` sets it, the mesh agent runs your handler with
it, and the framework carries it into the asyncio tasks and threads it starts with the context) and runs only when that
task's user holds the scopes, the card declares them, and the task has been neither stopped nor answered; outside a mesh
task it refuses. The step reporter still hides the tool and refuses a call to it first, reported and audited once; the
tool's own check is what holds where no hook sees the run ([security.md](../security.md#required-scopes-rbac)).
`tool_infos(tools)` reads the scopes off the guarded tools; a name that is none of the tools is a `ValueError`. A
refusal is the call's error result. This holds under the OpenTelemetry adapter too, which cannot refuse a call itself.

## Approvals and questions

Approval is a hook over the run's calls, and asking is a tool; build both in `build(ctx)`. `with_approval(ctx, tools,
*, names=None, message="")` covers the given `tools` (those in `names`, each among `tools`, else a ValueError), as the
other adapters wrap them:

```python
from strands import Agent
from sam_link_sdk.strands import ask_user_tool, with_approval


def build(ctx):
    return Agent(
        model=model,
        tools=[*my_tools, ask_user_tool(ctx)],
        hooks=[with_approval(ctx, my_tools, names=["route_claim"])],
        callback_handler=None,
    )
```

A refused call is cancelled by the hook (`cancel_tool`), so the model gets an error result.

Strands' own approval is the interrupt: a hook's `event.interrupt(name, reason=...)`, or a `Confirm` action from an
`InterventionHandler` (`Agent(interventions=[...])`), ends the run with `stop_reason == "interrupt"`. `run_agent` asks
the user through Agent Mesh for each interrupt, with the interrupt's `reason` as the message, and resumes the agent
with an `interruptResponse` per interrupt (`agent.invoke_async([...])`) until none is left.

- **The answer is a bool:** `True` approves, `False` refuses. Strands leaves the meaning of a response to the hook;
  `Confirm`'s default check (`True`, `"y"`, `"yes"`) and the steering plugin's accept it, and a hook that wants another
  value fails closed (the call is cancelled).
- **The tool the user is asked about** is the call whose tool-use id the interrupt's id carries (Strands builds the id
  from it), with the call's arguments. If no call matches, the interrupt's `name` stands for the tool, with no
  arguments, so name an interrupt after its tool.
- **A refusal** (denied, cancelled, timed out, a channel that cannot show a prompt, a stopped task, a scope the user
  lacks) cancels the call, and the model is told with the same text as `with_approval`'s, not the hook's own
  cancellation message. The call is reported once, when it ends.

## Peers

`mesh_agent_tools(ctx, agents=None)` in `build(ctx)`: `Agent(tools=[..., *mesh_agent_tools(ctx)])`. The peer's steps
nest under the model's tool call.

## Streaming

`run_agent(..., stream_text=True)` runs the agent with `stream_async` and sends its text events to the chat as they
arrive. Pass `stream_text=False` to send the answer whole when the run ends (`invoke_async`).

## Stop

Mesh tasks stop on their own: a cancel, a limit or SAM's stop cancels the asyncio task, including an async tool in
flight, and calls `agent.cancel()`. For agents run outside the mesh, `stop_guard(agent)` returns hooks
(`StopHooks`) that raise `AgentStopped`, unwrapped, when an agent is invoked and before every model and tool
call: `Agent(model=..., hooks=[stop_guard(mesh_agent)])`.

## Files

No adapter reads `ctx.files` or attaches files for you ([README](README.md#limits-tools-and-files)): read `ctx.files` in the handler and pass what the model should see as `input=`. To return a file, give the model a tool that calls `ctx.attach`, a tool in the agent `build(ctx)` makes:

```python
from strands import tool


@tool
def attach_report(name: str, text: str) -> str:
    """Attach a text file to the answer."""
    ctx.attach(name, text.encode(), "text/plain")
    return "attached"
```

The file reaches the user with the answer.

## What is not supported

- Taking a tool off a built agent: Strands has no public way to, so a tool the user lacks the scopes for is offered to
  the model unless `build(ctx)` leaves it out with `ctx.permitted_tools(tools)`, as the example does; the hook refuses
  the call either way (audited once per run).
