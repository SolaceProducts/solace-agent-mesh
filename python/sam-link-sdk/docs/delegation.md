# Other agents: delegation and workflows

## Delegating to other mesh agents

An SDK agent can hand a sub-task to any agent on the mesh, such as a SAM agent with the platform's tools, the same way
SAM agents call each other with their `peer_<name>` tools. Every adapter has `mesh_agent_tools`:

```python
from sam_link_sdk.langchain import mesh_agent_tools, run_agent


async def handle(ctx):
    tools = [lookup_policy, *mesh_agent_tools(ctx)]  # or mesh_agent_tools(ctx, ["report-writer"])
    return await run_agent(ctx, model, tools, system_prompt=SYSTEM_PROMPT)
```

How each framework takes them is on its page ([frameworks/README.md](frameworks/README.md)). CrewAI does not tell a
tool its tool-call id, so there the peer's steps nest under the agent rather than the specific tool call. Elsewhere
they nest under the call.

Without an adapter, call `await ctx.call_agent("report-writer", "...", function_call_id=call_id)`. It returns a
`PeerResult`, where `summary()` is the text to give the model. SAM redacts credentials in the peer's answer
(`<REDACTED>`), in its text, its error message and every string of its data, and leaves SAM's own notices out of the
data. `truncated` is true when SAM cut the answer: a string over 16 KiB is cut with SAM's marker (the data is kept),
and an answer still too large for a frame loses its data and has its text cut. `ctx.available_agents()` lists the agents the user may
call. `AgentConfig(peers=[...])` declares the agents this one calls on its card (SAM's peer-agent-topology
extension), so the console draws the mesh, and limits `ctx.available_agents()` and `mesh_agent_tools` to those of them
SAM lists: an agent SAM also lets it call is not offered. Calls are still allowed or refused per task.

- **Discovery:** SAM tells the agent which of the peers configured for it are on the mesh (`peers`), and
  sends the list again when it changes ([agent-link.md](agent-link.md)).
- **Wire:** the agent sends a `peer_call`; SAM builds the request a SAM agent sends (the delegation
  preamble, the parent task, the call depth + 1, the user's identity) and returns the peer's answer as one
  `peer_result` ([link-protocol.md](link-protocol.md)).
- **RBAC:** SAM decides. The agent may call only the peers configured for it on SAM, and only for a user who
  may invoke them (`agent:<name>:invoke`, `workflow:<uuid>:invoke` for a deployed workflow); `mesh_agent_tools` offers
  only the peers SAM lists (of them, those in `AgentConfig.peers` when it lists any). A call SAM does not carry fails at once with `SAM refused the peer call: <reason>`. When
  SAM refused it as not allowed (`forbidden`), the audit trail records `agent delegation denied`; a call SAM could not
  carry for another reason writes no record ([security.md](security.md#audit-trail)).
- **Activity Monitor:** SAM forwards the peer's steps into the task's own event stream, so the trace nests
  them under the tool call.
- **Cancel and timeout:** when the task ends, whatever its outcome (cancelled, stopped, failed, completed, or given
  back), its open peer calls are cancelled: SAM cancels the peer's sub-task when the task's result arrives,
  the SDK sends `peer_cancel` itself for a task it gives back, and a peer result that comes later resumes nothing. A
  peer call waits at most `LinkConfig.peer_call_timeout_seconds` (7200 s, SAM's default task time to live).
- **Text only:** files are not passed either way yet. The agent has no artifact store, so it cannot send `artifact://`
  files or open the ones a peer returns.

## SAM agents calling SDK agents, and SDK agents in workflows


- **A SAM agent calls an SDK agent.** The Orchestrator offers every agent card on the mesh as a `peer_<name>` tool, so
  an SDK agent needs only a clear card description and skills. Its model calls and tool calls appear under the
  Orchestrator's tool call. SAM checks the user: one without `agent:<name>:invoke` neither sees the SDK agent as a tool
  nor can send it a task (SAM hides it).
- **An SDK agent is a workflow node.** The workflow engine sends the node a `structured_invocation_request` data part,
  the node's input as a JSON file part, and the node's `instruction` as text. The SDK handles this for you: `ctx.text`
  is the instruction, or the input's `message` (else the input as JSON) when the node has none; `ctx.structured_input`
  is the parsed input and `ctx.invocation` is `{"workflow_name", "node_id"}`. Returning `TaskResult(text, data={...})`
  makes `data` the node's output, so later nodes and the workflow's `output_mapping` can use named fields
  (`{{route.output.queue}}`). A plain-text answer becomes `{"text": ...}`.
- **Declarative config:** `sam config plan/apply` checks each node's `agent_name` against the agents and link agents
  registered on the *platform* or declared in the manifest. An SDK agent registered as a link agent passes by its
  `name` or its `instanceName`. One that joins through a runtime configuration file is not registered on the
  platform, so the plan fails ("agent ... not found on platform"). Create that workflow through the platform REST API
  (`POST /api/v2/platform/workflows`, then `POST /api/v2/platform/workflowDeployments`); the runtime resolves the
  node's agent from its card. Use the agent's card name (for example `claims-adk`) as `agent_name`. A run in the
  first minute after deploying can log that the target is not among the workflow's discovered cards; the request
  still goes out, and the warning stops once the agents' next cards arrive.
- **Files do not flow through workflow nodes:**
  - *Uploads:* a file attached to the workflow task is read by the engine as JSON. A JSON file becomes the workflow's
    input data, so its fields work in `{{workflow.input.<field>}}`. Any other file (text, PDF, image) fails to parse
    (the workflow logs an ERROR) and is dropped, so no node sees it.
  - *Node requests:* the engine sends a node only its JSON input. It never turns artifact references into file parts
    and never inlines bytes, so `ctx.files` is empty in a workflow node.
  - *Node replies:* a file an SDK agent returns (inline bytes) is ignored. Only the reply's data part, or its text,
    becomes the node's output, so the user never sees the file and the next node never gets it.

  Workflow nodes pass JSON between each other. A small payload can ride as a base64 string in a data field, within SAM's
  message size, but SAM will not treat it as a file. Real file support needs engine changes: store inline
  files from node replies, and inline artifacts into node requests for agents without store access, as SAM
  already does for tasks it submits.
- **Call depth:** two SDK agents that may call each other stop at `max_call_depth` (3): a task at depth 4 is refused
  without running, and the agent that asked gets SAM's own `internal error (code -32603)` for it, not the reason. The
  reason (`rejecting task=... call_depth=4 max=3`) is in the refusing agent's log. SAM lets an agent list
  another as a peer but not itself.
