# Hermes Agent (Nous Research)

[Hermes Agent](https://github.com/NousResearch/hermes-agent) is a self-hosted agent that runs on its own (CLI, messaging
service, scheduled jobs), not a library, so `sam_link_sdk.hermes` embeds its `AIAgent` and reaches it through the plugin
hooks Hermes offers. It is experimental: Hermes is not on PyPI, so you install a checkout yourself. The adapter is
written against the Hermes checkout `b65e078c` on Python 3.14.

## Install

Hermes needs Python 3.14: its checkout declares `>=3.11,<3.15`, but every one of its dependencies is marked
`python_version >= '3.14'`, so on an older Python it installs without them and fails at import. It is not published as a
library: install a checkout and the SDK into one environment (the commands below run in `python/sam-link-sdk` of a checkout of this
repository, `-e .` being the SDK), and set `HERMES_HOME` to a directory of its own so Hermes'
state stays away from any `~/.hermes`:

```bash
git clone https://github.com/NousResearch/hermes-agent.git /path/to/hermes-agent
git -C /path/to/hermes-agent checkout b65e078cdc77ebe83979a2d5355179535f340e89
uv venv --python 3.14 && uv pip install -e /path/to/hermes-agent -e . anthropic==0.87.0
export HERMES_HOME=$PWD/.hermes-home
```

The adapter reaches parts of Hermes that Hermes does not promise to keep, so pin the
checkout and test your agent when you move it.

**Settings** (`HERMES_HOME/config.yaml`; `examples/claims_triage_hermes.py` writes them if the file is missing, the minimal agent below does not):

| Setting | Why |
|---|---|
| `tools.tool_search.enabled: off` | Hermes hides tools behind a search tool once many are enabled; the model, per-tool scopes and approvals must see the real tools. |
| `approvals.mode: manual` | A person decides, through SAM. The default `smart` first asks a guardian model call. |
| `plugins.hook_callback_timeout: 600` | SAM's approval is asked from a Hermes hook, which Hermes cuts off after 30 s by default (600 s at most); the approval prompt closes just before that, refusing the call. |

## Minimal agent

`run_agent(ctx, build)` runs one mesh task: `build(ctx)` makes a fresh agent (an `AIAgent` keeps its conversation and
runs a turn on a blocking thread, which `run_agent` moves off the event loop). `agent_defaults(ctx)` are the `AIAgent`
arguments a mesh task needs.

This is the whole agent, `examples/minimal_hermes.py`. Set `OPENAI_API_KEY` (and `OPENAI_BASE_URL` for an OpenAI-compatible endpoint), and `HERMES_HOME` as in Install and the SAM variables from the [quick start](../../README.md#quick-start), then run it with `python minimal_hermes.py`:

```python
import asyncio
import json
import logging
import os

from run_agent import AIAgent
from tools.registry import registry

from sam_link_sdk import AgentConfig, MeshAgent, TaskContext
from sam_link_sdk.hermes import agent_defaults, run_agent

SCHEMA = {
    "name": "lookup_policy",
    "description": "Look up an insurance policy by id.",
    "parameters": {
        "type": "object",
        "properties": {"policy_id": {"type": "string"}},
        "required": ["policy_id"],
    },
}


def lookup_policy(args: dict, **kwargs) -> str:
    return json.dumps({"policy_id": args["policy_id"], "status": "active", "covers": "water damage"})


def build_model() -> dict:
    return {
        "base_url": os.environ.get("OPENAI_BASE_URL"),
        "api_key": os.environ["OPENAI_API_KEY"],
        "model": "gpt-4o-mini",
    }


async def main() -> None:
    logging.basicConfig(level=logging.INFO)
    registry.register(
        name="lookup_policy",
        toolset="claims",
        description=SCHEMA["description"],
        handler=lookup_policy,
        schema=SCHEMA,
    )
    model = build_model()

    def build(ctx: TaskContext) -> AIAgent:
        return AIAgent(
            **agent_defaults(ctx),
            **model,
            enabled_toolsets=["claims"],
            ephemeral_system_prompt="You triage insurance claims.",
        )

    async def handle(ctx: TaskContext) -> str:
        return await run_agent(ctx, build)

    config = AgentConfig(name="claims-triage", description="Triages insurance claims")
    await MeshAgent(config, handle).run_forever()


if __name__ == "__main__":
    asyncio.run(main())
```

Keyword arguments: `input`, `memory` (a `ConversationMemory`), `include_request_context` (added to the system prompt),
`max_model_calls` (10: set as the agent's `max_iterations`), `stream_text` ([Streaming](#streaming)), `approval` and
`extra_tools` (this task's tools, added to the agent's own). A tool the user lacks the scopes for is left out of the run, and the
hook refuses a call to it anyway, reporting it as every adapter does
([security.md](../security.md#required-scopes-rbac)).

- **Isolation:** Hermes keeps long-term memory, learns skills from what it does and stores every session. Those belong
  to the Hermes install, not to the people who use it through the mesh, so `agent_defaults` turns them off for a mesh
  task and the conversation comes from the task's `contextId` (`memory=`), as for the other adapters.
- **Not sandboxed:** Hermes can run commands and edit files on its host, and the mesh sees each step but does not
  contain it. Enable only the toolsets the agent needs.

`examples/claims_triage_hermes.py` is the claims-triage agent.

## Steps

`run_agent` installs process-wide hooks once and routes each to the task whose agent called it (by the agent's session
id); there is no `step_reporter` to attach. `pre_api_request` / `post_api_request` give the model steps, with token
usage (input, output, cache, reasoning) from the response, and `pre_tool_call` / `post_tool_call` the tool steps, paired
by call id. Hermes builds that usage itself, each count it cannot find set to 0, so a provider that renamed its usage
fields reaches the adapter as a usage of zeros: SAM records no usage for the call and no `token usage unread` warning
is logged ([behaviour.md](../behaviour.md)). A model call Hermes retries (a rate limit, say) is reported once. A turn that reaches `max_model_calls` (Hermes' `max_iterations`) fails the task
with "Stopped: the agent reached its limit of N model calls (max_model_calls)." (Hermes asks the model once more for a
summary, which is not used). A failed model call
is recorded with its category for the task's error reply. A tool's result is a failed step when Hermes reports it
failed, or when it is `{"error": ...}` and nothing else, the rule every adapter follows.

- **Nested runs:** a subagent Hermes starts with `delegate_task` is a new `AIAgent` with a session of its own and the
  parent's toolsets. Hermes announces it (`subagent_start`), and the adapter routes the child's session to the parent's
  task: its model calls and tool calls are reported, checked against the task's scopes, asked for approval and stopped
  as the parent's. Hermes runs a top-level agent's delegations in the background, so a subagent can outlive its
  parent's turn: when the turn ends the adapter interrupts it (Hermes fans the interrupt out to live subagents, and
  stops the session's background delegations), and until `subagent_stop` every tool call it makes is refused. A
  session that was ever a mesh task's subagent stays refused after that, and a cancelled turn's run is closed at once,
  so a tool that ignores the interrupt does not hold up other Hermes agents in the process. While any mesh task runs,
  a tool call from a session no task knows is refused (fail closed: it may be a child the adapter was not told about);
  with no mesh task running, other Hermes agents in the process are left alone. Per guarantee:

| Nested run | Scopes | Approvals | Stop | Steps and tokens |
|---|---|---|---|---|
| Subagents, while the parent's turn lasts (in the background too) | refused (hook) | asked (`approval=`) | interrupted with the parent | reported |
| A background subagent after the parent's turn ends | every tool call refused | every tool call refused | interrupted when the turn ends | not reported: the task has ended |
| An agent a tool starts outside `delegate_task` | refused while any mesh run is open, then not SAM's (registry tools cannot carry a check) | the same | not reached | not reported |

- **Sessions:** each delivery of a task needs a session of its own, and `agent_defaults(ctx)` gives one (`sam-<task
  id>-<suffix>`): a run given up whose tool has not returned yet keeps its session, and SAM may deliver the task again
  meanwhile. The turn's task id is that session too: Hermes keys its terminal, browser and file caches by it and tears
  them down when the turn ends, so a given-up run's late end leaves the redelivery's alone (the conversation is
  `memory=`'s, keyed by the task's chat, not by this id). `run_agent` refuses an agent whose session id another
  running agent holds (`SessionInUse`, from `sam_link_sdk`), since their tool calls could not be told apart.
- **Tool call ids:** Hermes runs a `pre_tool_call` callback once at a time per tool call id across the process, and
  refuses a call whose id is already in that hook ("pre_tool_call plugin callback timed out or is still running").
  An approval waits in that hook, and a provider that numbers its calls (`call_0`) gives every conversation the same
  ids, so one task's call would be refused while another task waits on an approval. `post_api_request` therefore
  renames a call whose id an open mesh run in the process already holds (its own earlier calls and the conversation it
  replays included), with Hermes' own duplicate repair (`<id>_d<n>`), before Hermes reads the calls: the model step, the
  tool steps and the result the model is told carry the new id. A run holds its ids until its turn ends. Calls of
  Hermes agents that are not mesh tasks are not renamed, and if Hermes skips `post_api_request` (it abandoned that hook
  before) the ids stay as the model gave them.
- **A stop during the turn:** a Hermes hook can refuse a tool call but not a model call, so once the task is stopped
  the hooks interrupt the agent (`hard_interrupt`, on a thread of its own; a closed run interrupts nothing); its turn
  ends and `run_agent` ends in the stop.

## Scopes on the tool

Hermes builds its tools inside the agent (its tool registry), so there is no tool object to guard: declare the scopes
in the card, `AgentConfig(tools=tool_infos(agent_tools, scopes={"route_claim": ["tool:claims:route"]}))`, and the
`pre_tool_call` hook refuses a call to one for a user without them (in subagents too). Hermes has no `with_scopes`; what
the hook covers in a nested run is under Nested runs above.

## Approvals and questions

`with_approval(*, names=None, message="")` is a policy for `run_agent(..., approval=...)` (Hermes builds its tools
inside the agent, so it takes none; `names` is keyword-only and must be a list of names, each a tool of the built agent
or of `extra_tools=`, which `run_agent` checks, else a ValueError): the named tools ask the user
through SAM before each call, from Hermes' `pre_tool_call` hook (hence the hook timeout above). The prompt closes 5 s
before Hermes would abandon the hook, so the call is refused and reported as refused rather than left without a result
(an abandoned hook also makes Hermes skip it for every tool call in the process for a minute). Hermes' own
dangerous-command prompt (its `terminal` tool) uses the same SAM prompt. Hermes' `clarify` tool (enable its toolset)
asks the user a question through SAM. A refused or timed-out prompt does not run the tool.

## Peers

`mesh_agent_tools(ctx, agents=None)`, given to the run: `run_agent(ctx, build, extra_tools=mesh_agent_tools(ctx))`. Hermes
keeps one process-wide tool registry, so each peer tool is registered there once and answers for the task that calls
it.

## Streaming

`run_agent(..., stream_text=True)` (the default) sets the agent's `stream_delta_callback` and sends the text Hermes
streams to the chat as it is written. Hermes also hands that callback the text a response writes once it has begun a
tool call, and puts a paragraph break before the first text after a tool round; neither is the answer, so the adapter
leaves both out (it watches `tool_gen_callback` for the call's start). Callbacks the agent was built with still get
every call. Text a response writes before its first tool call is streamed too, since nothing tells it from the answer
until the call arrives; the answer `run_agent` returns is Hermes' `final_response`, the last response's text. Pass
`stream_text=False` to send the answer whole when the run ends.

## Stop

Mesh tasks stop on their own: cancelling a mesh task (a cancel, a limit, or SAM's stop) interrupts the agent
(`hard_interrupt`). It stops before its next model call, and a tool that waits ends if it checks
`tools.interrupt.is_interrupted()`; a tool that is busy without checking runs to its end.

A Hermes install does more than serve the mesh, so install the guard where the process starts, before the agents run:
`guard = stop_guard(mesh_agent)` (a `StopGuard`), then `guard.watch(agent)` for each agent. While SAM has the
agent stopped it refuses every tool call in the process, drops inbound messages at Hermes' messaging gateway, and
interrupts the watched agents that are in a turn. An idle agent is not interrupted: Hermes keeps an interrupt until a
turn takes it, so the agent's first turn after the release would end at once without calling the model.
`guard.check()` raises `AgentStopped` for your own entry points.

## Files

No adapter reads `ctx.files` or attaches files for you ([README](README.md#limits-tools-and-files)): read `ctx.files` in the handler and pass what the model should see as `input=`, and attach output with `ctx.attach(name, content, mime_type)`. Hermes builds its tools inside the agent, so a tool the model can call to attach a file is a Hermes tool of your own (registered in Hermes' registry, finding the task with `sam_link_sdk.current_task()`), or the handler attaches after the run.

## What is not supported

- `step_reporter`: Hermes' hooks are process-wide; `run_agent` installs them.
- `ask_user_tool`: use Hermes' own `clarify` tool, which `run_agent` routes to SAM's question prompt.
