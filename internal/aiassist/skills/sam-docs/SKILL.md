---
name: sam-docs
description: The official Solace Agent Mesh documentation as searchable markdown under references/documentation/ — architecture, agents, workflows, entrypoints, tools, skills, artifacts and sessions, declarative config, installing, administering, the CLI and config reference. Grep it before answering a "how does X work" question or citing a field name. Not a routing target on its own; the concierge and the other skills point here for the docs behind a concept.
---

# sam-docs

The complete Solace Agent Mesh documentation ships with this skill as plain markdown under `references/documentation/`. It is the same content as the published documentation, at the version of the `sam` CLI that installed this suite.

## How to use it

1. **Search first.** Run grep or ripgrep over `references/documentation/` with a broad, case-insensitive pattern and a few lines of context:
   `rg -i -n -C 3 'exit handler|exit_handler|on_exit' references/documentation/`
2. **Read the matching section, not the whole page.** Open the file at the matching line and read the enclosing heading's section.
3. **Browse only when search finds nothing.** List the directories under `references/documentation/`; each is named for the topic it holds.
4. **Stop after two searches with no hit.** The topic is not covered here. Answer from your own knowledge and say that the documentation does not cover it, rather than retrying with small variations.

Cite the file path when you quote the documentation so the user can open the page.

## If `references/documentation/` is missing

The `sam` CLI that installed this suite was built without the embedded documentation payload. Release builds carry it; plain development builds do not. Read the published documentation instead: https://docs.solace.com/Agent-Mesh/agent-mesh.htm
