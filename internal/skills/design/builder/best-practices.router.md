---
name: sam-best-practices
description: Design guidance for Solace Agent Mesh — decision frameworks for component selection, LLM context management, instruction writing, tool selection, and common pitfalls.
tags:
  - builder
  - design
  - best-practices
  - sam
---

You now have access to the Agent Mesh best-practices guide.

This skill contains a single reference file: **sam-best-practices.md** — the decision framework for
choosing components, the policies to trade off between them, how to write an agent instruction, and
the pitfalls that produce a working-but-poor design.

**Read it before deciding what to build**, while the shape of the solution is still open. Once
components are chosen, the per-component guides (`sam-agent-design`, `sam-workflow-design`,
`sam-skill-design`) carry the authoring detail.

**How to read it efficiently:**

- `grep_skill_resources` with `skill_name: "sam-best-practices"`, `case_insensitive: true`, and
  `context_lines: 3` to locate a topic (e.g. `"split|delegat"`, `"autonomous|interactive"`,
  `"pitfall"`).
- Then `read_skill_resource` with `start_line`/`end_line` to read just that section.

**Find the section by the question you are holding** — grep the heading text to jump:

| Question | Section |
|---|---|
| Agent, workflow, or skill — which component? | Decision Framework for Component Selection |
| Why does any of this matter? | The Core Principle |
| Should this be a workflow? | Use workflows when the process structure is known |
| How do I handle events arriving from a broker? | Use event mesh entrypoints for event-driven integration |
| Should I split this into multiple agents? | Split into multiple agents when focus is lost |
| When should an agent hand off to another agent? | Use peer delegation for cross-domain expertise |
| When is knowledge better as a skill? | Add skills for on-demand knowledge |
| What am I trading off between designs? | Guiding Policies |
| How do I structure an agent instruction? | Instruction Writing |
| Will a user be present at runtime? | Interactive vs. Autonomous Mode |
| Which kind of tool should I build? | Tool Selection |
| How do I write a tool description the model will use correctly? | Tool Descriptions Matter |
| How do I stop free-form output from drifting? | Structured Output as a Forcing Function |
| How should I scope and size a skill? | Skill Design — Keep Skills Focused, Associate Tools with Skills, Write Descriptive SKILL.md Files, Size Appropriately |
| Does "skill" mean the bundle or the capability? | Terminology |
| What actually fills up the context window? | Performance and Cost |

**Symptoms** — a design that works but is wrong. Each row names the section that explains it:

| Symptom | Section |
|---|---|
| One agent does everything and is mediocre at all of it | The Kitchen Sink Agent |
| The agent loads the wrong skill, or several at once | Skill Overload |
| A tool gets called with wrong parameters, or ignored entirely | Tool Description Neglect |
| One malformed early output corrupted everything downstream | Missing Validation Boundaries |
| The agent blocks forever waiting for input nobody will give | Autonomous Agent with Interactive Assumptions |
| An event-triggered agent never finishes | Interactive Agent Behind an Event Mesh Entrypoint |
| Context is lost between agents and debugging is hard | Premature Multi-Agent Split |
| Fine at 3 turns, degraded by 30 | Ignoring Context Accumulation |
| The agent calls the first plausible tool rather than the best one | Reason Before Acting |
| The agent retries a failing tool forever, or gives up immediately | Explicit Error Recovery |
| The instruction tries to cover everything up front | Progressive Disclosure of Context |

For **core concepts and terminology**, refer to `sam-concepts`.
For **authoring an agent**, refer to `sam-agent-design`.
For **authoring a workflow**, refer to `sam-workflow-design`.
For **packaging knowledge as a skill**, refer to `sam-skill-design`.
For **cutting token usage and latency**, refer to `sam-efficiency`.
