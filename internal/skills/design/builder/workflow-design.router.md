---
name: sam-workflow-design
description: Workflow design guide — when to use workflows, node patterns, reshaping data between tool nodes, structured I/O, retry/timeout mechanics, template expressions, testing, and anti-patterns.
tags:
  - builder
  - design
  - workflow
  - sam
---

You now have access to the Agent Mesh workflow design guide.

This skill contains a single reference file: **workflow-design-guide.md** — worked YAML for every
node type, the data-flow mechanics between nodes, and the failure modes that do not announce
themselves.

**Read it before hand-authoring a workflow**, not only when deciding whether to use one. It is the
file that carries the YAML shape; deciding "a workflow is the right tool" is the smaller half of
what it covers.

**How to read it efficiently:**

- `grep_skill_resources` with `skill_name: "sam-workflow-design"`, `case_insensitive: true`, and
  `context_lines: 3` to locate a topic (e.g. `"retry|timeout"`, `"map|parallel"`, `"extract_fields"`).
- Then `read_skill_resource` with `start_line`/`end_line` to read just that section. The guide is
  long; reading it whole is rarely the right move.

**Find the section by the question you are holding** — grep the heading text to jump:

| Question | Section |
|---|---|
| Should this be a workflow at all? | When to Use Workflows |
| How do I wire N nodes together? | Design Patterns — Pipeline, Fan-Out / Fan-In, Conditional Routing, Map, Loop, Nested Workflows |
| How do I classify and route without a full agent turn? | Decision Nodes |
| How do I chain two tools without an LLM? | Reshape Data Between Tool Nodes |
| How do I turn workflow data into a report artifact? | Render a Report Template |
| How do I contract-check what flows between nodes? | Structured I/O Contracts |
| What happens when a node fails? | Error Handling — Retry Strategy, Fail-Fast vs Partial Completion, Exit Handlers |
| Which timeout wins? | Timeout Design |
| How do I reference another node's output? | Template Expression Patterns |
| How do I name the file a step produced? | A tool node's output is its `Data`, plus the artifact it saved |
| How do I test and debug this before trusting it? | Testing and Debugging — Start Small, Use Simple Agents First, Check Template Expressions, Validate Schemas |

**Symptoms** — a workflow that runs but is wrong. Each row names the section that explains it:

| Symptom | Section |
|---|---|
| A tool-to-tool chain reports success but produced nothing useful | Reshape Data Between Tool Nodes |
| The workflow output holds literal `{{node.output.<field>}}` text | An Agent Node Guarantees Only the Fields It Declares |
| A `retry_strategy:` that never seems to fire | Retry Strategy |
| `output_mapping` resolves to nothing for a file a tool just produced (or the step saved several) | A tool node's output is its `Data`, plus the artifact it saved |
| A node hangs on an agent that works fine in chat | Node agents run unattended |
| An array or object arrived at the next node as a string | Type Preservation |
| `field "…" not found in node output` on a downstream node | Reshape Data Between Tool Nodes |
| It runs, but the shape of it feels wrong | Common Anti-Patterns |

For **workflow config schemas**, refer to `sam-workflow-schema`.
For **high-level design principles**, refer to `sam-best-practices`.
For **core concepts**, refer to `sam-concepts`.
