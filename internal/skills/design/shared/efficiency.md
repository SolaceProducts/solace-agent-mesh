# Designing for efficiency

This reference is *design guidance* — the levers Agent Mesh gives you to cut LLM token
usage and wall-clock latency, and when to reach for each. It covers **what to do
and why**; for the exact YAML fields, follow the schema references in the
lookup table at the end of this guide.

The single biggest idea: **the LLM should orchestrate data, not carry it.** Most
waste comes from large content flowing *through* the model's context — file
bodies, query results, generated documents — when it only needed to flow *past*
it. The levers below are mostly variations on that theme.

## 1. Keep large data out of the model's context

The model pays input tokens for everything in the conversation history and output
tokens for everything it writes. Both are avoidable for bulk data.

- **Return artifacts; don't inline.** When a tool or agent produces a large
  result, save it as an artifact and return a reference, not the bytes. A 50 KB
  report inlined is 50 KB of history on every subsequent turn; an artifact
  reference is a few dozen bytes. Downstream consumers (the entrypoint, the client)
  load the artifact on demand.
- **Use `«artifact_content:…»` late embeds for user-facing data.** A late embed
  is resolved at the entrypoint on the way to the user — the *data never enters the
  model's context at all*. The model emits a one-line embed directive; the user
  receives the full content. This is the cheapest way to deliver a large payload.
- **Slice before you inject with the embed modifier chain.** When the model *does*
  need to see artifact data, pull only the relevant slice. The `>>>` chain
  filters and reshapes content before it reaches the model:

  ```
  «artifact_content:sales.csv >>> select_cols:month,revenue >>> slice_rows:0:20 >>> format:csv»
  ```

  A 10 MB CSV becomes 20 rows of 2 columns. Each modifier works on one shape:
  `jsonpath` needs JSON; `select_cols`/`select_fields`, `filter_rows_eq`, and
  `slice_rows` work on rows (a CSV, or a JSON list of objects); `slice_lines`,
  `grep`, `head`, and `tail` work on plain text. Reach for these instead of asking
  the model to read a whole file and summarize it.

## 2. Instantiate templates instead of generating files

When an agent's job is to produce a structured document (an HTML report, a
Markdown summary, a formatted export), having the LLM write it token-by-token is
the most expensive way to do it — and the least reliable. Instead, ship a
**skill asset template** and instantiate it: the model produces only the *data*
(a small JSON/CSV artifact), and the template's embeds/Liquid render the document
at serve time. A 200-line report rendered from 100 rows costs the model the ~5 KB
of data it generated, not the ~100 KB of document it would otherwise have typed.

Bind the data by **logical name** via `instantiate_template`'s `data_inputs`
arg — `{ <binding>: <the artifact you produced> }` — and the tool rewrites the
document's references to point at that artifact, pinned to the exact version it
validated, so the model never has to reproduce the template's internal filenames.
The same call works from a **workflow `tool` node**: bind `data_inputs` to an
upstream node's artifact and the workflow renders the document with no LLM in
the loop at all.

A template ships as a single packaged `.samt` file in the skill's `assets/`; see
the skill-design guide for bundling one, and prefer this pattern for any
repeatable report or document deliverable.

## 3. Load knowledge on demand, not up front

Everything in an agent's instructions is paid for on **every** request, whether
or not it's relevant to the task at hand.

- **Package detail into skills, not instructions.** Skills are loaded on demand
  via `load_skill` / `read_skill_resource` — an agent with 50 skills available
  pays for only the two it loads for a given task. Stuffing the same knowledge
  into the system prompt taxes every request.
- **Keep `SKILL.md` an orientation file; put bulk in `references/`.** A skill's
  `SKILL.md` is loaded when the skill loads; its `references/*.md` are pulled in
  only when a specific topic is needed. Split detail out so the orientation stays
  cheap. (This very skill follows that shape.)
- **Right-size the agent's tool set.** Tool definitions are part of the request.
  An agent wired with every toolset pays for every schema on every turn; give it
  the tools its role actually needs and delegate the rest to a peer agent.

See the skill-design and agent-design guides (lookup table at the end of this guide).

## 4. Use structured output to avoid re-prompting

Unstructured "please return JSON" prompting commonly costs 3–5 round-trips as the
model produces slightly-malformed output and gets re-prompted. Declaring an
`outputSchema` makes the runtime validate the result and retry only on real
failure (bounded retries), turning a 3–5 attempt loop into 1–2. Use it for any
agent or workflow node whose output is consumed by a machine rather than read by
a human. See the agent-design guide (structured output) and the workflow schema
reference (structured node I/O), both in the lookup table at the end of this guide.

## 5. Parallelize independent work

Serial fan-out pays a full LLM round-trip per branch; parallel fan-out collapses
the wall-clock.

- **`sub_task` for substantial, independent branches.** Several multi-step pieces
  of work (generating separate documents, researching unrelated questions) issued
  as parallel sub-tasks run concurrently; each sub-task's intermediate tool calls
  stay scoped to it, so the main agent's context stays clean and only final
  results return. Each sub-task is a fresh agent loop that re-pays the system
  prompt and tool definitions, so it only pays off when the work it keeps out of
  context is larger than that setup. Do single tool calls, short lookups, and
  anything whose result you need before continuing inline. Use forked context
  when the branch needs the conversation so far, fresh context when it doesn't.
- **Workflows for declarative fan-out.** Workflow DAG nodes without dependencies
  execute in parallel automatically. For a fixed set of independent steps, a
  workflow expresses the parallelism declaratively and keeps each step's context
  isolated. See the workflow schema reference and the workflow-design guide (lookup
  table at the end of this guide).

Delegate to a **peer agent** when a sub-problem has its own distinct tool set or
knowledge — it keeps each agent's instructions and tools lean (lever 3) rather
than building one agent that carries everything.

## 6. Manage long-running context

For conversations that span many turns, history is the dominant cost.

- **Keep auto-summarization (compaction) on.** It is on by default: when history
  grows past a threshold, the runtime summarizes the oldest portion into a single
  message, replacing tens of thousands of tokens of transcript with a compact
  summary at the cost of one summarization call. Its settings (the `enabled`
  switch, the compaction percentage, and the summary's token cap) sit in the
  agent's auto-summarization block; turn it off only for short, single-turn
  agents where the summarization call never pays back.
- **Don't let agents accumulate unbounded transcripts** without compaction — a
  500-turn conversation re-sends its entire history on every turn otherwise.

## 7. Model and runtime settings

- **Prompt caching is automatic but you can help it.** The runtime marks the
  system prompt and tool definitions for ephemeral caching (short TTL), so
  repeated turns within the window get a large discount on those input tokens.
  You benefit by keeping the system prompt and tool set **stable** across a task —
  dynamically rewriting instructions per turn defeats the cache.
- **Bound runaway loops.** A per-task cap on LLM calls (the agent's max LLM
  calls per task setting, on by default) stops a pathological tool-calling loop
  from burning tokens indefinitely; the final allowed call nudges the model to
  answer rather than call another tool. Lower it for agents whose job is a
  handful of tool calls.
- **Streaming cuts perceived latency**, not token count — enable it for
  interactive agents so users see output as it's produced.
- **Match the model to the job.** Route cheap, high-volume, or simple-classifier
  work to a smaller/faster model and reserve the frontier model for reasoning-heavy
  steps. See the model configuration reference and the agent-design guide (model
  selection), both in the lookup table at the end of this guide.

## Anti-patterns

- **Dumping tool/query output inline** instead of saving an artifact and
  referencing or slicing it.
- **Asking the LLM to author large documents** it could instantiate from a
  template (lever 2).
- **Mega-skills and mega-agents** that carry all knowledge and every tool in the
  system prompt instead of loading on demand and delegating (levers 3, 5).
- **Free-text "return JSON"** where a schema would prevent re-prompting (lever 4).
- **Unbounded conversations** with no compaction (lever 6).
- **Per-turn instruction churn** that defeats prompt caching (lever 7).
