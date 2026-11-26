# Naming the files a workflow produces

A file a step produces is stored per **user, session, filename and version**. The user is whoever the run
acts for, so two people's files never mix — a workflow **cannot** hand a file to a different user by
agreeing on a name, and a read under the wrong user finds nothing rather than erroring. (That partition
collapses to a single identity only where an operator has pinned one, such as an entrypoint configured
with a fixed identity, or a deployment running without auth.) Above the user sits a component scope: one
shared space per deployment by default, though an operator can narrow it so that a producer and a consumer
on opposite sides of that line no longer see each other's files. Saving the same filename again adds a
**new version** rather than replacing it. Two consequences catch authors out.

## Runs do not automatically get their own session

A workflow inherits the session of whatever started it:

| Started by | Session |
|---|---|
| a person in chat | the conversation's — **shared by every run in it** |
| another agent delegating | the calling agent's, deliberately, for continuity |
| a scheduled fire | a fresh one each time |

So running the same workflow twice in one conversation writes **two versions of the same file**, and a
later reader silently gets the newer one. What keeps runs apart is the **filename**, not the session.

Inheriting a session on delegation also decides what the callee can *read*. Because files are found by
session rather than by what the call passed, a delegated agent or workflow can list and read **every** file
already in that session — including ones from earlier, unrelated turns — not just the data you handed it.
Worth knowing before delegating into a session that holds files the callee has no business seeing.

One filename form opts out of the session entirely: a name **prefixed `user:`** is stored per user rather
than per session, so it persists across every conversation that user has. That is occasionally what you
want, but it means a name copied from another config can silently give you cross-session storage where you
expected per-run isolation.

Put something per-run in any name you choose. `{{workflow.run_id}}` is the direct way (see
[data-flow.md](data-flow.md)); if you would rather not depend on it, have the caller pass a run label as
an ordinary input and thread that through, or derive one in an upstream node and read it from that node's
output.

## A parallel fan-out collides on a shared name

Branches running at the same time that write the same filename become successive versions of one file, and
a branch may read a sibling's data instead of its own. This is silent — every branch reports success.

The engine already keeps its **own** per-branch files apart: the input it hands each `map` branch, and the
output filename it *suggests* to that branch's agent, both carry the branch index. The exposure is names
chosen elsewhere — one an agent picks for itself, or a literal you hardcode in the branch's config. Note
the suggested name is only a suggestion, passed to the agent as part of the request; nothing enforces that
the agent uses it.

**Give each branch its own basename**, derived from the branch rather than one default shared by all of
them. In a `map` node the item index or key is the natural component.

**How you would notice:** identical filenames across branches, or two branches reporting byte-identical
results. Confirm by carrying a provenance value in the data itself — the branch's own index or key, or the
source it read from — and checking each result names its own. Comparing the numbers proves nothing,
because two branches can legitimately produce the same ones.

## A consuming step needs a concrete filename

There is no way to ask for "the file the previous step made" by session or URI. A consumer needs an actual
name, and there are two ways to give it one:

- **bind it from the producer's output**, when the producing tool returns a filename — read it as
  `{{<node-id>.output.filename}}`;
- **set an explicit output filename** on the producing step and name that same literal in the consumer.

Use the second when the producing tool does not report a name, or when an agent node produced the file —
an agent node's output is the file's contents, not its name. For the `web_request` specifics, see
[builtin-tools.md](builtin-tools.md).

**Why the first option is not always available.** A tool node's `{{…output.*}}` namespace comes from the
tool's structured **data** map — plus `message`, `status` and `created_artifact`, injected only when the
tool did not already set those keys. `created_artifact` names the artifact this call saved, pinned to the
version it wrote (`report.json:3`), with the bare name in `created_artifact_name` and the number in
`created_artifact_version`; all three are present only when it saved **exactly one**, and for a call that
saved several they are absent, because the template language cannot index a list. So a single tool-produced artifact is reachable from
`output_mapping` without the tool reporting it, and for anything else the tool must *also* return the name
in its structured data.

The pin matters inside a `map`: its iterations, and any workflows they call, share one artifact session,
so a bare name reads whichever iteration wrote it last. Pass `created_artifact` unchanged to a step that
reads the file; built-in tools that read an artifact (`load_artifact`, `append_to_artifact`,
`artifact_grep`, and the data-analysis tools) accept `name:version`. Where a step needs the
filename itself — an output filename to rewrite it, or `delete_artifact`, which removes every version and
so rejects a pinned reference — use `created_artifact_name`.

**There is no conventional key for that name, so do not guess it.** `web_request` returns `filename`;
`query_data_with_sql` returns `output_filename`. A tool's **result** keys are a separate namespace from its
**input parameter** names, and whether the two coincide varies per tool — `web_request` *takes*
`output_artifact_filename` but *returns* `filename`, while `query_data_with_sql` both takes and returns
`output_filename`. For a **built-in**, look it up instead of guessing: `builtin-tools.md` in this skill
lists the input parameters and result fields of every built-in a built agent can bind, generated from the
registry — a tool that runs in the Secure Tool Runtime lists its parameters but declares no result fields, and a few
list no parameters either, so treat a gap as "read the tool's own description," not "no such tool." For anything else —
MCP, OpenAPI, a toolset or a connector — the result keys are not declared anywhere you can read, so confirm
the key from the tool's own documentation or from one real run and bind exactly that. Assuming the input
parameter name carries over, or carrying a name across from another tool, gets you a field the upstream
tool never produced: the node fails when the reference resolves, whatever the tool is, and on a built-in
the workflow additionally warns about it as it starts. Tool type gates the warning, not the failure.

Two consequences worth separating:

- **Consuming a tool** that does not report its name: when the step saved exactly one artifact, read
  `{{<node>.output.created_artifact}}` — the framework names it whether the tool did or not. Only for a
  step that saves zero or several does the second option remain, since there is no way to index a set:
  set the name explicitly on the producing step and hardcode that same literal downstream.
- **Writing a tool** that creates a file: return the filename in structured data, not only on the
  artifact itself, or no workflow can name what you produced. (And avoid a `message` key in that data
  unless you mean to shadow the injected one — `{{node.output.message}}` then means something
  tool-specific.)
