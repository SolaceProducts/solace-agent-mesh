---
name: sam-docs
description: Official Solace Agent Mesh documentation — covers architecture, components, configuration, development, deployment, tutorials, and enterprise features.
tags:
  - documentation
  - reference
  - sam
---

You now have access to the full Solace Agent Mesh documentation.

**How to find information efficiently:**

1. **Search first** — use `grep_skill_resources` with `skill_name: "sam-docs"` and a broad regex pattern
   covering key terms (e.g., `"exit handler|exit_handler|on_exit"` or `"session|storage|persist"`).
   Set `case_insensitive: true` for better results. Use `context_lines: 3` to see surrounding text.
2. **Read targeted sections** — use the file paths and line numbers from grep results with
   `read_skill_resource` and `start_line`/`end_line` to read just the relevant section (e.g., 50 lines
   around the match), not the entire file.
3. **Browse only if needed** — use `list_skill_resources` only if grep returns nothing and you need to
   explore the file structure.

The documentation is organized into sections:

- **overview/**: What Solace Agent Mesh is, architecture, choosing your path
- **getting-started/**: Before you begin, try Agent Mesh desktop, build your first project, next steps
- **concepts/**: Gateway Executor/Agent-Workflow Executor/Secure Tool Runtime split, A2A protocol, event-driven mesh, artifacts, sessions, skills, toolsets, runtime services, platform service, request lifecycle, configured-vs-built
- **building/**: Authoring agents, workflows, entrypoints, tools, toolsets, skills, scheduled tasks, the AI assistant
- **webui/**: WebUI builder — connectors (SQL, Amazon Bedrock, Slack, MCP, OpenAPI) and entrypoints (Slack, Teams, event mesh)
- **installing/**: Install, configure, deploy options, air-gapped install, monitoring, troubleshooting
- **administering/**: RBAC, secrets, TLS, observability, health/logging, audit & compliance, upgrades, migrations, production-readiness, maintenance, agent evaluation
- **developer-productivity/**: Developer workflow and productivity tooling
- **reference/**: CLI, environment variables, config schema, built-in tools, glossary
- **tutorials/**: Slack support bot, SQL integration, REST entrypoint, MCP integration, RBAC walkthrough, pro-code agent deployment
- **migrating-from-python/**: Moving from the Python Solace Agent Mesh implementation
- **help/**: FAQ, community & support

If grep finds no matches for your search terms, the topic is likely not covered in the documentation.
Do not repeatedly search with minor variations — instead, answer based on your general knowledge.
