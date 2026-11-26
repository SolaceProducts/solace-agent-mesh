---
name: sam-agent-design
description: Agent design guide — instruction writing, interactive vs autonomous mode, tool selection, structured output, human-in-the-loop approval, peer delegation, sessions, artifacts, agent card, and model selection.
tags:
  - builder
  - design
  - agent
  - sam
---

You now have access to the Agent Mesh agent design guide.

This skill contains a single reference file: **agent-design-guide.md** — the five-section
instruction pattern, the interactive-vs-autonomous split that has to be decided at design time,
and the full human-in-the-loop authoring surface with its per-tool-type shapes.

**Read it before writing an agent's instruction**, not only when choosing between components. The
instruction is the single largest lever on agent quality, and HIL is the part most often authored
wrong in a way that silently does nothing.

**How to read it efficiently:**

- `grep_skill_resources` with `skill_name: "sam-agent-design"`, `case_insensitive: true`, and
  `context_lines: 3` to locate a topic (e.g. `"hil|approval"`, `"structured output"`,
  `"autonomous"`).
- Then `read_skill_resource` with `start_line`/`end_line` to read just that section. The guide is
  long; reading it whole is rarely the right move.

**Find the section by the question you are holding** — grep the heading text to jump:

| Question | Section |
|---|---|
| How do I structure the instruction? | Instruction Writing — Recommended Structure |
| What goes in each of its five parts? | Role and Identity, Core Expertise and Domain, Behavioral Guidelines, Constraints and Boundaries, Skill References |
| Will a user be there at runtime? | Interactive vs Autonomous Agent Design |
| Which tools should this agent carry? | Tool Selection and Composition |
| Individual tools or a whole group? | Tool Groups vs Individual Tools |
| How do I make the LLM pick the right tool? | Tool Description Quality |
| How do I force machine-parseable output? | Structured Output Mode — How to Configure |
| How do I make a tool ask before it acts? | Human-in-the-Loop (HIL) — Entry-level HIL |
| How do I gate one tool but not its siblings? | Per-tool HIL on a multi-tool entry, Which name to key on |
| How do I gate one MCP tool but not its siblings? | Per-tool HIL on a multi-tool entry, Conditional gating |
| How do I write the approval prompt? | Approval-message templating |
| What does the user actually see? | How the approval card renders |
| When should this be two agents? | Peer Delegation — When to Split into Multiple Agents |
| How does one agent reach another? | How Peer Delegation Works |
| Should sessions persist? | Session and Memory Design |
| When should output become an artifact? | Artifact Usage Patterns |
| How do other components discover this agent? | Agent Card Design |
| Which model should it use? | Model Selection |

**Symptoms** — an agent that runs but behaves wrongly. Each row names the section that explains it:

| Symptom | Section |
|---|---|
| The agent blocks forever on a question nobody will answer | Autonomous Agents |
| An agent that is fine in chat stalls behind an entrypoint | Interactive vs Autonomous Agent Design |
| A tool gets called with the wrong arguments, or ignored | Tool Description Quality |
| Output is *almost* valid JSON, every time | Structured Output Mode |
| `hil:` is configured and nothing ever asks for approval | Entry-level HIL, Conditional gating |
| Approval fires on every operation of an OpenAPI or builtin-group entry | Entry-level HIL, Per-tool HIL on a multi-tool entry |
| Approval fires on safe reads as well as writes | Conditional gating (`require_approval_when`) |
| The approval prompt shows raw placeholders or leaks arg values | Approval-message templating, How the approval card renders |
| The approval card names the agent by a UUID-looking string | How the approval card renders |
| The agent tries to do a peer's job badly | When to Split into Multiple Agents |
| Quality decays over a long conversation | Context Accumulation |
| Large tool results swamp the context | Tool Output Thresholds |
| No other component ever delegates to this agent | Writing Good Capability Descriptions |

For **agent config schemas**, refer to `sam-agent-schema`.
For **core concepts and terminology**, refer to `sam-concepts`.
For **high-level design principles**, refer to `sam-best-practices`.
For **cutting token usage and latency**, refer to `sam-efficiency`.
