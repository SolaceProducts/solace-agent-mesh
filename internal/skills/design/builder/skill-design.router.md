---
name: sam-skill-design
description: Design guidance for Agent Mesh skills — when to create skills, instruction content structure, reference design, agent integration, and common pitfalls.
tags:
  - builder
  - design
  - skill
  - sam
---

You now have access to the Agent Mesh skill design guide.

This skill contains a single reference file: **skill-design-guide.md** — when a skill is the right
container, how to split instruction content from references, the `assets/` template contract, and the
order to build skills and agents in.

**Read it before authoring a skill**, not only when deciding whether to package one. It carries the
bundle shape and the builder workflow, not just the decision.

**How to read it efficiently:**

- `grep_skill_resources` with `skill_name: "sam-skill-design"`, `case_insensitive: true`, and
  `context_lines: 3` to locate a topic (e.g. `"inline|artifact"`, `"template"`, `"size"`).
- Then `read_skill_resource` with `start_line`/`end_line` to read just that section.

**Find the section by the question you are holding** — grep the heading text to jump:

| Question | Section |
|---|---|
| Should this be a skill at all? | When to Create a Skill |
| What is a skill made of? | What Are Skills? |
| What goes in the instruction content? | Instruction Content Writing Guide — Recommended Structure |
| What does a good instruction look like? | Example |
| Inline content or a user-uploaded artifact? | Supplying reference files |
| How do I split the references into files? | File Organization |
| How do I ship a report or document template? | Asset Templates |
| How do I wire the skill to an agent? | Agent Integration |
| Is `skills[]` the same as `agent_card.skills[]`? | Agent Card Skills vs Knowledge Skills |
| What do I name it? | Naming Conventions |
| How big should it be? | Size Guidelines |
| In what order do I create skills and agents? | Builder Workflow for Skills |

**Symptoms** — a skill that loads but does not earn its cost. Each row names the section that
explains it:

| Symptom | Section |
|---|---|
| The instruction content is huge and mostly reference material | Instruction Anti-Patterns |
| The instruction says only "this skill has information about X" | Instruction Anti-Patterns |
| The skill restates what the agent instruction already says | Instruction Anti-Patterns |
| The agent greps the references and finds nothing useful | File Organization |
| One reference file holds everything and searching it is noisy | File Organization |
| The agent is writing a structured document token by token | Asset Templates |
| The agent never loads the skill | Agent Integration |
| The config is rejected when saving the skill | Builder Workflow for Skills — 4. Validate before saving |
| The skill covers so much that agents load it for unrelated tasks | Size Guidelines |

For **skill config schemas**, refer to `sam-skill-schema`.
For **high-level design principles**, refer to `sam-best-practices`.
For **cutting token usage and latency**, refer to `sam-efficiency`.
