---
name: sam-concepts
description: Core Agent Mesh concepts — component types, how components relate, agent anatomy, the tool ecosystem, workflow node types, and which component to reach for.
tags:
  - builder
  - design
  - concepts
  - sam
---

You now have access to the Agent Mesh core-concepts guide.

This skill contains a single reference file: **sam-concepts.md** — what each component type is, how
they find and talk to each other, what an agent is made of, and the vocabulary the other skills
assume you already have.

**Read it first if the vocabulary is new**, and read the "When to Use What" table before choosing
a component. The design skills name these concepts without redefining them.

**How to read it efficiently:**

- `grep_skill_resources` with `skill_name: "sam-concepts"`, `case_insensitive: true`, and
  `context_lines: 3` to locate a topic (e.g. `"peer|delegat"`, `"agent card"`, `"event mesh"`).
- Then `read_skill_resource` with `start_line`/`end_line` to read just that section.

**Find the section by the question you are holding** — grep the heading text to jump:

| Question | Section |
|---|---|
| What is Agent Mesh, and why a broker? | What is Solace Agent Mesh |
| What are the building blocks? | Component Types |
| What is an agent vs a workflow vs a skill? | Agent, Workflow, Skill |
| What does "capability" mean here? | Capability (Agent Card Entry) |
| How does anything reach an agent from outside? | Entrypoint |
| How do agents react to broker events? | Event Mesh Entrypoint |
| What is a proxy / toolset / project / prompt? | Proxy, Toolset, Project, Prompt |
| How do components find each other? | Discovery, Communication Model |
| What is an agent actually made of? | Agent Anatomy |
| Where do an agent's tools come from? | Tool Ecosystem |
| What node types can a workflow contain? | Workflow Node Types |
| Which component do I reach for? | When to Use What |
| Where does configuration live? | Configuration |

**Vocabulary traps** — the places the words mislead. Each row names the section that untangles it:

| Confusion | Section |
|---|---|
| "Skill" means two different things | Skill, Capability (Agent Card Entry) |
| Capabilities are configured under a field called `skills` | Capability (Agent Card Entry) |
| Agent or workflow — which decides the next step? | When to Use What |
| A tool, a peer agent, or an MCP server — which is which? | Tool Ecosystem |
| Nothing was wired together and yet agents found each other | Discovery |
| An agent that works in chat but not behind an entrypoint | Event Mesh Entrypoint |

For **design principles and component choice**, refer to `sam-best-practices`.
For **authoring an agent**, refer to `sam-agent-design`.
For **authoring a workflow**, refer to `sam-workflow-design`.
For **configuration schemas**, refer to the schema skills named in the guide's lookup table (its *Working in the Builder* section).
