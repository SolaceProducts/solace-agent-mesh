---
name: sam-tools-and-skills
description: Use when extending a Solace Agent Mesh agent with custom code or curated tool packages — writing a custom tool in Go (samtoolsdk) or Python (sam-tool-sdk), scaffolding with the `sam toolset` / `sam skill` CLI, packaging tools as a toolset, creating an agent skill bundle (SKILL.md + bundled tools), exposing a REST API to one agent via an inline OpenAPI tool, attaching toolsets/skills to agents, or setting per-agent tool config. Not for no-code data/service access where a connector type fits (sam-connectors), inbound integrations where users or events reach agents (sam-entrypoints), or the agent's own instructions and behavior (sam-author-agent).
---

# sam-tools-and-skills

This skill covers **custom tools and tool packages** for Agent Mesh agents: Go and Python remote tools that run in the STR (Secure Tool Runtime), inline OpenAPI tools, toolsets, and agent skill bundles. **Scope check:** if a no-code connector type fits (SQL, knowledge base, search, document/graph DBs, REST via OpenAPI shared across agents, remote MCP, Slack, event mesh), use `sam-connectors` first — custom code is for when no connector type fits. Inbound (users/events/MCP clients reaching agents) → `sam-entrypoints`. The agent's instructions/behavior → `sam-author-agent`.

## Picking the construct

| The user wants… | Construct | Reference |
|---|---|---|
| Custom logic — **default choice** (compiled binary, typed params, artifacts, zero-network scaffold) | Go remote tool via `pkg/samtoolsdk` | [references/go-tools.md](references/go-tools.md) |
| Custom logic where the user already knows Python or needs a Python-only library | Python remote tool via `sam-tool-sdk` (PyPI) | [references/python-tools.md](references/python-tools.md) |
| One agent calling a REST API from an OpenAPI spec, config-file managed | Inline `tool_type: openapi` tool — but **shared/UI-managed API access is the `api` connector** (`sam-connectors`) | [references/openapi-tools.md](references/openapi-tools.md) |
| A reusable package of always-available tools, uploaded once, attached to many agents | **Toolset** | [references/packaging-and-deploy.md](references/packaging-and-deploy.md) |
| Tools *plus* usage instructions the agent loads on demand (`load_skill`) | **Skill bundle** (SKILL.md + references + optional bundled tools) | [references/packaging-and-deploy.md](references/packaging-and-deploy.md) |

Toolset vs skill in one line: a toolset is bare tools, always on the agent's tool list; a skill is instructions first (progressively disclosed at runtime) with tools optionally bundled. Tools that need workflow guidance → skill; standalone utilities → toolset.

**Prefer Go when the user has no strong preference.** Agent Mesh is a Go product and both languages are first-class, but Go is the smoother default: the scaffold vendors the SDK so builds need no network and no PyPI access, cross-compilation to the deployed STR's arch is built into `sam toolset/skill package`, and a single compiled binary ships with no runtime interpreter or dependency-resolution step. Recommend Go unless the user already works in Python or needs a Python-only library — then Python via `sam-tool-sdk` is fully supported, don't push back.

`sam commands` prints every command and flag of the installed `sam` as JSON. Run it when you want the full CLI surface with all options.

## The intended authoring path (teach this, in order)

1. **Scaffold** — `sam toolset init NAME --lang go|python` or `sam skill init NAME [--with-tool --lang go|python]`. Default to `--lang go` unless the user asked for Python. The scaffold is a *working* tool: compilable code and a `manifest.yaml`, plus `build.sh`/`build.bat` for a **toolset** only — a skill-bundle tool gets no build script, because `validate`/`package` build it for you. Go scaffolds vendor the SDK into `_sdk/samtoolsdk/` from the CLI binary itself — building needs **no network and no access to the Agent Mesh repo**. Python scaffolds depend on `sam-tool-sdk` from PyPI.
2. **Author** — edit the generated tool. Real API names are in the references; do not improvise them.
3. **Validate** — `sam toolset validate` / `sam skill validate` builds for your host and runs the exact `--schema` discovery the STR performs. Run it before every package.
4. **Package & deploy** — `sam toolset package` / `sam skill package` cross-compiles for the deployed STR's architecture and zips for WebUI upload; or keep the source in a declarative-config repo and let `sam config plan` / `apply` build and upload (that flow is owned by `sam-declarative-config`).
5. **Attach** — by name, in the agent builder UI (per-tool config form, secrets masked) or in the agent's declarative spec.

## Hard rules (each counters an observed failure)

- **Go product, Go patterns.** Agent Mesh has **no** `tool_type: python`, no `component_module`/`component_base_path`, no ADK `ToolContext`, no `sam plugin add` — the runtime logs a migration warning for `tool_type: python` and registers nothing. Python tools run as **subprocess remote tools in the STR** via `sam-tool-sdk`. **Never route to `solace-agent-mesh-plugin-creator`** — it is Python Agent Mesh.
- **Scaffold first, never SDK code from memory.** Baseline sessions invented `samtoolsdk.Tool[P]{}`, `tc.LoadArtifact`, `TextResult` — none exist. Start every tool from `sam toolset init` / `sam skill init --with-tool` output and the API tables in the references. If a symbol isn't in the reference, check the scaffold or the SDK source before using it.
- **Remote tools appear as `tool_type: builtin`** on the agent's `tools:` list — the dispatcher falls through to the STR when the name isn't an in-process built-in. Toolset and skill attachment generate these entries for you (`toolsetname__toolname`, `skillname__toolname` — `__` separator, charset `[a-zA-Z0-9_-]`).
- **Two OpenAPI constructs exist — say which and why.** Shared across agents, UI-managed, platform-credentialed → `api` connector (`sam-connectors`). Agent-local, declarative-config-managed → inline `tool_type: openapi`. Recommending an MCP wrapper or a dedicated proxy agent for a plain REST API is wrong when these exist.
- **Never write resource YAML from memory.** Toolset metadata (`kind: toolset`, `spec.config`), agent attachment (`toolsets:`, `toolsetConfigs:`, `skillRefs`), and tool stanzas are authored via the builder UI or the **`sam-declarative-config`** skill (its `toolset.md`, `skill.md`, `tool-build.md`, `agent.md` references are the YAML source of truth). This skill names keys; it does not assemble config blocks. Urgency never licenses memory-YAML.
- **Secrets:** `${VAR}` references, never literals; toolset-level `spec.config` for secrets, per-agent overlay for non-secret tunables. A credential pasted into chat is exposed — say so once, advise rotation, reference it as `${VAR}` thereafter.
- **Cross-compile is part of the product, not the user's problem.** The packaged binary must match the deployed STR's OS/arch; `package` resolves it from the platform (`sam toolset build-target` to inspect; `SAM_TOOL_TARGET_OS`/`SAM_TOOL_TARGET_ARCH` to override). With `--url`, a platform that reports no discovered target stops packaging rather than guessing. **Without `--url`, `--target`, or those env vars, `package` falls back to `linux/arm64`** and only prints a stderr note saying so — so an offline package for any other STR arch must set `SAM_TOOL_TARGET_OS`/`SAM_TOOL_TARGET_ARCH`.

## References

| Topic | File |
|---|---|
| Go tool authoring — verified samtoolsdk API, example, build, sharp edges | [references/go-tools.md](references/go-tools.md) |
| Python tool authoring — sam-tool-sdk patterns, runtime contract, sharp edges | [references/python-tools.md](references/python-tools.md) |
| Inline OpenAPI tools — fields, auth, and the connector-vs-tool discriminator | [references/openapi-tools.md](references/openapi-tools.md) |
| Toolsets & skills — decision, layout, CLI lifecycle, upload, attach, per-agent config | [references/packaging-and-deploy.md](references/packaging-and-deploy.md) |
