# SAM Builder Agent — Skills Checklist

Skills for the built-in builder agent. Skills live in `internal/settings/files/skills/` unless noted otherwise.

## Core Concepts & Design

- [x] **sam-concepts** — Component types (including capabilities vs skills distinction, event mesh entrypoint), how they relate, agent anatomy, tool ecosystem, workflow nodes, when to use what
- [x] **sam-best-practices** — Decision framework (LLM context management as core principle), guiding policies, LLM-aware design practices, instruction writing, skill/tool design, common pitfalls, anti-patterns

## Config Schemas (YAML with comments, cross-referenced from Go and Python)

- [x] **sam-agent-schema** — Agent config schema, field descriptions, annotated examples
- [x] **sam-workflow-schema** — Workflow config schema, node type schemas, examples
- [x] **sam-tool-schema** — Tool definition schema, parameter types, examples for built-in/custom/MCP tools
- [x] **sam-gateway-schema** — Entrypoint config schema (event mesh entrypoint, HTTP SSE, webhook)

## Agent Design

- [x] **sam-agent-design** — How to design agents: instruction writing, model selection, tool selection, structured output, HIL, peer delegation

## Workflow Design

- [x] **sam-workflow-design** — Workflow patterns: when to use workflows, structured I/O contracts, node composition, error handling, timeouts, retry strategies, template expressions
- [x] **sam-efficiency** — Token/latency design levers: keep large data out of context, instantiate templates, load skills on demand, structured output, parallelism, compaction, model/runtime settings

## Report Templates

- [x] **sam-templates** — Template lifecycle for `.samt` report templates: `read_template`/`instantiate_template` contracts (substitutions, data_inputs), authoring via `verify_template_candidate`/`package_template`. Lives in `internal/builtinskills/files/` (shared with runtime agents), not this directory
