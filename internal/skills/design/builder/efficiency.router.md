---
name: sam-efficiency
description: Design guidance for cutting LLM token usage and latency in Agent Mesh — keep data out of context, instantiate templates, load skills on demand, structured output, parallelism, compaction, and model/runtime settings.
tags:
  - builder
  - design
  - efficiency
  - sam
---

You now have access to the Agent Mesh efficiency guide.

This skill contains a single reference file: **efficiency-guide.md** — seven levers for cutting token
usage and wall-clock latency, and when to reach for each. Its organising idea: **the LLM should
orchestrate data, not carry it.**

**Read it when a design is settled but expensive**, and before shipping anything that will run at
volume. It covers *what to do and why*; the exact YAML fields live in the per-kind references it
links to.

**How to read it efficiently:**

- `grep_skill_resources` with `skill_name: "sam-efficiency"`, `case_insensitive: true`, and
  `context_lines: 3` to locate a topic (e.g. `"embed|artifact_content"`, `"cach"`,
  `"compact|summariz"`).
- Then `read_skill_resource` with `start_line`/`end_line` to read just that section.

**Find the section by the question you are holding** — grep the heading text to jump:

| Question | Section |
|---|---|
| How do I keep a large file or query result out of context? | 1. Keep large data out of the model's context |
| How do I deliver a big payload to the user cheaply? | 1. Keep large data out of the model's context |
| How do I slice artifact data before the model sees it? | 1. Keep large data out of the model's context |
| How do I produce a report without the LLM typing it? | 2. Instantiate templates instead of generating files |
| The instruction and tool list are too big — what moves? | 3. Load knowledge on demand, not up front |
| How do I stop re-prompting for parseable output? | 4. Use structured output to avoid re-prompting |
| How do I run independent steps concurrently? | 5. Parallelize independent work |
| How do I stop a long conversation from getting expensive? | 6. Manage long-running context |
| Which model, and what caps should I set? | 7. Model and runtime settings |
| How do I get prompt caching to work for me? | 7. Model and runtime settings |

**Symptoms** — it works, but it costs more than it should. Each row names the section that explains
it:

| Symptom | Section |
|---|---|
| A big tool result is re-sent on every subsequent turn | 1. Keep large data out of the model's context |
| The model reads a whole file just to summarize it | 1. Keep large data out of the model's context |
| The model spends most of its output tokens typing a document | 2. Instantiate templates instead of generating files |
| One agent carries every tool and all the knowledge in the system | 3. Load knowledge on demand, not up front |
| 3–5 round-trips before the output parses | 4. Use structured output to avoid re-prompting |
| Independent branches run one after another and wall-clock is slow | 5. Parallelize independent work |
| Cost climbs every turn even when each task is small | 6. Manage long-running context |
| The prompt cache never seems to hit | 7. Model and runtime settings |
| Users wait for the whole answer before seeing anything | 7. Model and runtime settings |
| A tool-calling loop burned tokens until the task was killed | 7. Model and runtime settings |
| It works, but the shape of it feels wasteful | Anti-patterns |

For **high-level design principles**, refer to `sam-best-practices`.
For **packaging knowledge as a skill**, refer to `sam-skill-design`.
For **agent instruction, structured output, and model selection**, refer to `sam-agent-design`.
For **declarative fan-out across nodes**, refer to `sam-workflow-design`.
