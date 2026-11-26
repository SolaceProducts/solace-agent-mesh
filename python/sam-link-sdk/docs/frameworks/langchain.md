# LangChain and LangGraph

`sam_link_sdk.langchain` serves LangChain and LangGraph alike: LangGraph passes a run config's callbacks to every model and
tool call inside a graph. Tested with langchain-core 1.6.9 and langgraph 1.2.14; a newer version is checked for the
hooks the adapter uses when it is imported.

## Install

```bash
pip install "sam-link-sdk[langchain]"     # langchain-core
pip install "sam-link-sdk[langgraph]"     # langchain-core and langgraph
pip install "sam-link-sdk[openai]"        # langchain-core and langchain-openai (ChatOpenAI), as the examples use
```

## Minimal agent

`run_agent(ctx, model, tools)` is a minimal tool-calling loop on `langchain-core` (model, then tools, then the model,
until it answers without tool calls):

This is the whole agent, `examples/minimal_langchain.py`. Set `OPENAI_API_KEY` (and `OPENAI_BASE_URL` for an OpenAI-compatible endpoint) and the SAM variables from the [quick start](../../README.md#quick-start), then run it with `python minimal_langchain.py`:

```python
import asyncio
import logging

from langchain_core.tools import tool
from langchain_openai import ChatOpenAI

from sam_link_sdk import AgentConfig, MeshAgent, TaskContext
from sam_link_sdk.langchain import run_agent


@tool
def lookup_policy(policy_id: str) -> str:
    """Look up an insurance policy by id."""
    return f"{policy_id}: active, covers water damage"


def build_model() -> ChatOpenAI:
    return ChatOpenAI(model="gpt-4o-mini", stream_usage=True)


async def main() -> None:
    logging.basicConfig(level=logging.INFO)
    model = build_model()

    async def handle(ctx: TaskContext) -> str:
        return await run_agent(ctx, model, [lookup_policy], system_prompt="You triage insurance claims.")

    config = AgentConfig(name="claims-triage", description="Triages insurance claims")
    await MeshAgent(config, handle).run_forever()


if __name__ == "__main__":
    asyncio.run(main())
```

The same agent as a LangGraph graph, `examples/minimal_langgraph.py` (`run_graph` runs the graph and keeps each chat under its own thread):

```python
import asyncio
import logging

from langchain_core.tools import tool
from langchain_openai import ChatOpenAI
from langgraph.checkpoint.memory import MemorySaver
from langgraph.graph import START, MessagesState, StateGraph
from langgraph.prebuilt import ToolNode, tools_condition

from sam_link_sdk import AgentConfig, MeshAgent, TaskContext
from sam_link_sdk.langchain import run_graph


@tool
def lookup_policy(policy_id: str) -> str:
    """Look up an insurance policy by id."""
    return f"{policy_id}: active, covers water damage"


def build_model() -> ChatOpenAI:
    return ChatOpenAI(model="gpt-4o-mini", stream_usage=True)


def build_graph(model: ChatOpenAI):
    bound = model.bind_tools([lookup_policy])

    async def agent(state: MessagesState, config):
        return {"messages": [await bound.ainvoke(state["messages"], config)]}

    graph = StateGraph(MessagesState)
    graph.add_node("agent", agent)
    graph.add_node("tools", ToolNode([lookup_policy]))
    graph.add_edge(START, "agent")
    graph.add_conditional_edges("agent", tools_condition)
    graph.add_edge("tools", "agent")
    return graph.compile(checkpointer=MemorySaver())


async def main() -> None:
    logging.basicConfig(level=logging.INFO)
    graph = build_graph(build_model())

    async def handle(ctx: TaskContext) -> str:
        result = await run_graph(ctx, graph)
        return result["messages"][-1].text

    config = AgentConfig(name="claims-triage", description="Triages insurance claims")
    await MeshAgent(config, handle).run_forever()


if __name__ == "__main__":
    asyncio.run(main())
```

`tools` is the agent's whole tool list (LangChain has no agent object that holds them), which is why it is positional
here and `extra_tools` elsewhere. Its keyword arguments: `input` (instead of the task's text), `memory` (a
`ConversationMemory`), `include_request_context` (appends SAM's request context to the system prompt),
`max_model_calls` (10), `stream_text` (default True, see Streaming) and `system_prompt`; a run that is still calling
tools after `max_model_calls` model calls fails the task, saying it reached that limit.
A tool the user lacks the scopes for is not offered to the model, and a call to it anyway is refused (see Steps).

Your own LangChain runnable or LangGraph graph takes the step reporter in its run config instead:

```python
from langchain_core.messages import HumanMessage
from sam_link_sdk.langchain import step_reporter


async def handle(ctx):
    result = await graph.ainvoke(
        {"messages": [HumanMessage(ctx.text)]}, config={"callbacks": [step_reporter(ctx)], "recursion_limit": 16}
    )
    return result["messages"][-1].text
```

Inside your own nodes, pass the node's `config` down to the model (`await model.ainvoke(messages, config)`). The step
reporter refuses a call to a tool the user lacks the scopes for in any graph it is given to, subgraphs included (see
Steps); to keep the tool away from the model too, build the graph per task with `ctx.permitted_tools(tools)`. For
conversation memory use a checkpointer with `ctx.session_id` as the thread id (`run_graph` does that for you). `examples/claims_triage_langchain.py` and
`examples/claims_triage_langgraph.py` (an explicit `StateGraph`: model node, `ToolNode`, back) are the claims-triage
agent on each.

## Steps

`step_reporter(ctx)` returns a `SamLinkCallbackHandler`. It reports each model call and response and each tool's start
and result, with token usage from the message's `usage_metadata` (input, output, cache read and creation, reasoning; cache tokens only where the provider's client puts them in
the usage, as Anthropic's, Bedrock's and LiteLLM's do, not the OpenAI Chat Completions client's writes),
and refuses a call to a tool the user lacks the scopes for.

- **Call ids:** a tool step pairs with the model's call by its tool-call id, parallel calls of one response included.
  A model call without ids gets `call_<hex>` ids, written back into the `AIMessage` so the
  `ToolMessage` carries the same id. A tool started without a tool-call id (invoked with plain arguments, not by a
  model's tool call) takes the oldest pending call id for its name.
- **Failed tools:** a tool that raises, or returns an error `ToolMessage` (`handle_tool_error`), is a failed step.
  LangGraph's prebuilt `ToolNode` re-raises a tool's exception unless it is built with `handle_tool_errors`: the step is
  reported as failed first, then the run ends with the error.
  A tool built with `handle_tool_error` that raises `ToolException` is not re-raised: LangChain returns an error
  `ToolMessage` to the model, and the step is shown as failed with that error (`CLAIMS_HANDLE_TOOL_ERRORS` in the example).
- **Failed model calls** are recorded with their category for the task's error reply.
- **Reasoning** (with `stream_thinking`): Anthropic-style thinking blocks and `reasoning_content`. `ChatOpenAI`, which
  the example uses, drops the `reasoning_content` an OpenAI-compatible endpoint sends, so there is none to stream
  through it (a model class that keeps it, such as `ChatDeepSeek`, or Anthropic's, does).
- **Per-tool scopes:** LangChain and LangGraph hand the run config's callbacks to every tool call, subgraphs included,
  so the handler sees each tool start before the tool runs. A callback cannot replace a tool's result, only end the
  call by raising: the handler reports the refusal as every adapter does and raises `ToolAccessDenied`
  ([security.md](../security.md#required-scopes-rbac)). `run_agent` gives it to the model as the call's error result.
  In a graph it is the tool's exception: a `ToolNode` built with `handle_tool_errors=True` gives it to the model, the
  default `ToolNode` ends the run with it, as it does for any tool that raises. It is the handler's one synchronous
  callback, because LangChain runs a synchronous tool's callbacks on a worker thread and there lets an asynchronous
  callback's exception pass, the tool running anyway. A `ToolNode` with `handle_tool_errors=True` gives the model
  LangGraph's own wrapping of it (`Error: ToolAccessDenied('Access denied: ...')\n Please fix your mistakes.`); build
  it with `handle_tool_errors=lambda error: str(error)` to give the model the refusal text as it is.
- **Nested runs:** whatever a tool runs (a subgraph, an agent of its own, a model) takes the run config, and with it
  the step reporter: its steps and tokens are reported and its tools checked. An approved tool (`with_approval`) runs
  with the wrapper's config too; its own start and result are reported once, as the wrapper's. The model and tool
  callbacks are synchronous, so a stopped task's run ends at its next model or tool call even on the worker thread a
  synchronous tool runs on. Per guarantee:

| Nested run | Scopes | Approvals | Stop | Steps and tokens |
|---|---|---|---|---|
| Subgraphs, and runnables a tool runs with the run config | refused (step reporter and tool) | asked | ends at the next model or tool call | reported |
| A runnable run without the callbacks, or on a thread without the context | refused by the tool (`with_scopes`) | the `with_approval` tool asks for its own task, and refuses outside it | a scoped tool refuses; model calls go on | not reported |

## Scopes on the tool

Declare a tool's scopes on the tool, and build the card from the guarded tools:

```python
from sam_link_sdk.langchain import tool_infos, with_scopes

TOOLS = with_scopes([lookup_policy, route_claim], {"route_claim": ["tool:claims:route"]})
config = AgentConfig(name="claims-triage", description="Triages claims", tools=tool_infos(TOOLS))
```

The guarded tool is the framework's own tool type with the check in front of its body. Wherever it is called from, it
finds the mesh task it runs for (`sam_link_sdk.current_task()`: `run_agent` sets it, the mesh agent runs your handler with
it, and the framework carries it into the asyncio tasks and threads it starts with the context) and runs only when that
task's user holds the scopes, the card declares them, and the task has been neither stopped nor answered; outside a mesh
task it refuses. The step reporter still hides the tool and refuses a call to it first, reported and audited once; the
tool's own check is what holds where no hook sees the run ([security.md](../security.md#required-scopes-rbac)).
`tool_infos(tools)` reads the scopes off the guarded tools; a name that is none of the tools is a `ValueError`. A
refused call raises a `ToolException`: a tool with `handle_tool_error` gives the model the refusal, and
otherwise it is the tool's exception.

## Approvals and questions

```python
from sam_link_sdk.langchain import ask_user_tool, with_approval


async def handle(ctx):
    tools = [*with_approval(ctx, my_tools, names=["route_claim"], message="Routing moves money."), ask_user_tool(ctx)]
    return await run_agent(ctx, model, tools)
```

`with_approval(ctx, tools, *, names=None, message="")`; `names` must be a list of names. The user sees the arguments the
model gave (an injected argument such as a peer tool's call id is not shown), and an approved call reaches the tool as
the model's tool call, so a peer tool keeps its call id. A refusal is raised as a `ToolException` with
`handle_tool_error` on, so LangChain turns it into an error result for the model (without it, LangGraph's `ToolNode`
would let the exception end the task).

LangGraph's own approval is `interrupt()`. A node or tool that calls
`interrupt({"tool": "route_claim", "args": {...}, "message": "..."})` pauses the graph, and `run_graph(ctx, graph, *,
input=None, config=None, stream_text=True)` (in place of `graph.ainvoke(input, config)`) asks the user through Agent Mesh and resumes the
graph on the same thread with `Command(resume=...)`, until it ends. `input` defaults to `{"messages":
[HumanMessage(ctx.text)]}`. `run_graph` adds the step reporter to `config`'s callbacks unless it is there already, so
the graph's steps are reported and a tool the user lacks the scopes for is refused. `run_graph` takes no `memory` or `include_request_context`: the checkpointer keeps the conversation, and a node that
wants SAM's context reads `ctx.request_context.context_prompt()`. The graph needs a checkpointer, as
LangGraph needs for any interrupt. Unless `config["configurable"]` names a `thread_id`, the thread is the SAM session
(`ctx.session_id`), so the checkpointer keeps the chat's conversation; a run-based task (a peer call or a workflow node)
runs on a thread of its own, new for each delivery, deleted from the checkpointer when the run ends.

```python
from langgraph.types import interrupt
from sam_link_sdk.langchain import run_graph


@tool
async def route_claim(policy_id: str, queue: str) -> dict:
    """Route a claim to a work queue."""
    decision = interrupt({"tool": "route_claim", "args": {"policy_id": policy_id, "queue": queue}})
    if decision is not True:
        return {"error": decision}
    return do_route(policy_id, queue)


result = await run_graph(ctx, graph)
```

`interrupt()` returns `True` when the user approved, else the text to give the model, the same text `with_approval`
gives for a refusal (denied, cancelled, timed out, a channel that cannot show a prompt, a stopped task); the tool then
does not run the call. `args` and `message` are optional; arguments are redacted as in `with_approval`. The step
reporter does not report the call as failed when the graph pauses, and reports it once, when it runs. LangGraph runs
the interrupted node again from its start on resume, so keep what must happen once (the call itself) after the
`interrupt()`. A tool the user lacks the scopes for is refused before its body runs, so it never reaches its interrupt.
An interrupt that is not an approval request (not a mapping with a `tool`) raises a ValueError: only approvals are
asked. LangChain's `run_agent` loop has no pause, so it has no interrupts.

## Peers

`mesh_agent_tools(ctx, agents=None)` gives one `peer_<name>` tool per mesh agent the task's user may invoke; build them
per task: `tools = [*my_tools, *mesh_agent_tools(ctx)]`. The peer's steps nest under the model's tool call.

## Streaming

`run_agent(..., stream_text=True)` streams the model (`astream`) and sends its text to the chat as it is written; the
chunks are added up into the message, so tool calls work as before. Token usage reaches the step only if the chat model
reports it when streaming: with `ChatOpenAI`, pass `stream_usage=True` for an endpoint that is not OpenAI's. Pass
`stream_text=False` to send the answer whole when the run ends.

`run_graph(..., stream_text=True)` does the same for a LangGraph graph: it runs `graph.astream` instead of
`ainvoke` and sends the text of the AI messages the graph's model writes, not a tool's output or the user's input. A
run resumed after an approval streams too, and the interrupt handling is unchanged. The model has to stream inside
your node (`ChatOpenAI` does when the node calls `ainvoke` with the run's `config`). Pass `stream_text=False` to
send the answer whole when the run ends.

## Stop

Mesh tasks stop on their own. For runs outside the mesh (your own API, a notebook, a job), `stop_guard(agent)` returns
a callback handler (`StopGuard`) that raises `AgentStopped` at the start of every chain, model call and tool
while SAM has stopped the agent. Attach it where the model or graph is built, so every run is covered:
`ChatOpenAI(..., callbacks=[stop_guard(agent)])` or `graph.with_config(callbacks=[stop_guard(agent)])`.

## Files

No adapter reads `ctx.files` or attaches files for you ([README](README.md#limits-tools-and-files)): read `ctx.files` in the handler and pass what the model should see as `input=`. To return a file, give the model a tool that calls `ctx.attach`, `run_agent(ctx, model, [*my_tools, attach_report])`:

```python
from langchain_core.tools import tool


@tool
async def attach_report(name: str, text: str) -> str:
    """Attach a text file to the answer."""
    ctx.attach(name, text.encode(), "text/plain")
    return "attached"
```

The file reaches the user with the answer.

## What is not supported

- A LangGraph node cache over scoped or approval-guarded tools: a `ToolNode` with a `cache_policy` answers a hit without
  running the node, so neither the step reporter nor the tool's own check sees the call. Give such a node no
  `cache_policy`.
- `run_agent` is a minimal loop. For a graph of your own use `run_graph`; for another runnable, use `step_reporter`
  and stream with `ctx.emit_text` yourself.
- Hiding a tool from a graph's model: the graph is yours, so build it with `ctx.permitted_tools(tools)`; the call is
  refused either way.
