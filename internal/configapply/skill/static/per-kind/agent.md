An agent is a long-lived LLM-driven entity bound to a model, a set of
toolsets, optional skills, and a system prompt. `type` accepts only
`standard`, so there is no agent type to choose or change.

All authoring fields below live under a top-level `spec:` block (only
`kind:`, `name:`, and `description:` sit at the document root) — see the
**Example** at the end of this reference for the canonical shape. The flat
form (agent fields beside `kind:`) is silently accepted at plan time but
drops the fields at apply time, surfacing as a misleading 422.

> **`systemPrompt` is required for standard agents.** The platform
> rejects any create/update of a `standard` agent (the only supported type)
> that omits it with HTTP 422 `system prompt is required for standard
> agents`. Always set `spec.systemPrompt`; its length is bounded by the platform's `agent_instruction_min_chars` and `agent_instruction_max_chars` (1 and 32000 by default).

> **Removing toolset or skill settings clears them.** An agent's
> `spec.toolsetConfigs` and `spec.skillConfigs` are the whole stored set: take
> an entry, or the whole key, out of the YAML and the next `apply` clears it
> on the platform, including settings saved in the UI, and redeploys the
> agent. When the whole key goes, the plan row says so, e.g.
> `update + deploy (clears toolsetConfigs)`, with a matching warning in
> `diagnostics`. When only some entries go, the row is a plain update, and
> `plan -v` shows which toolset or skill names lose their settings (names
> only, never values). To keep them, declare the blocks in the file before
> applying. Secret keys are the exception inside an entry that stays: a secret
> key removed from a tool block that is still present keeps its stored value.
> Setting it to `""` keeps the stored value too, for a built-in tool
> credential and for a package secret that is declared required or has no
> default; only an optional package secret (one with a default) is cleared by
> `""`.

## `additionalConfigurations` — catch-all (and its forbidden keys)

`additionalConfigurations` is a free-form object for agent config keys the
structured fields above don't model yet (e.g. `supportsStreaming`,
`agentCardPublishing.intervalSeconds`). Keys are camelCase. It is **not** where tools go.

- **`tools` is forbidden here** — the platform computes the deployed
  `tools:` block from `spec.toolsets` and rejects any
  `additionalConfigurations.tools` with HTTP 422 `additionalConfigurations
  may not set "tools"; that key is computed by the platform at deploy time`.
  To give an agent tools, list built-in toolset IDs (or your own toolset
  names) under `spec.toolsets` — see the built-in ID table in
  `references/toolset.md`.
- Keys that duplicate a structured field (e.g. `description`, `toolsets`)
  are likewise rejected — set the structured field instead.
- The runtime AWE agent YAML shape (`tools:` with `group_name`,
  `tool_type`, snake_case) does **not** belong here; that format is for
  hand-authored runtime configs, not the platform declarative API.

The agent has two distinct skill surfaces — they are *not* aliases:

- `skills:` is the **AgentCard capability descriptor list** — the
  per-agent advertisement that other agents and entrypoints read off the
  broker to decide whether to delegate to this agent. Each entry mirrors
  the A2A `AgentSkill` wire shape and is published verbatim on the
  agent card. Maximum 20 entries.
- `skillRefs:` lists **bundled skill packages** by name — runtime
  resources (instructions, references, bundled tools) the agent loads
  on demand via `load_skill`. The reconciler resolves names to platform
  IDs at apply time, so a missing skill is a plan-time error rather
  than a silent ID typo.

## `skills[]` — AgentCard skill entry

Each `skills[]` element describes one capability
the agent advertises. Mirrors the A2A `AgentSkill` wire format.

| Field | Type | Description |
|---|---|---|
| `id` | `string` | Stable identifier the orchestrator uses to route delegation. When omitted, the platform derives it from the skill name at YAML-emit time. Provide an explicit `id` if you rely on a specific routing key. |
| `name` | `string` | Human-readable skill name. Required. |
| `description` | `string` | One-sentence description of the capability. Required. |
| `tags` | `list<string>` | Free-form labels surfaced in the agent card UI; useful for grouping or filtering. |
| `examples` | `list<string>` | Short user prompts that exercise the skill. Surfaced as suggestions in some clients. |
| `inputModes` | `list<string>` | MIME types the skill accepts (e.g. `text`, `application/json`). Defaults to the agent-level `inputModes` when omitted. |
| `outputModes` | `list<string>` | MIME types the skill produces. Defaults to the agent-level `outputModes` when omitted. |
| `required` | `list<string>` | Agent Mesh-specific (not part of the A2A spec). Stored on the platform but not round-tripped through runtime YAML. Retained for backwards compatibility — new authoring should leave it empty. |
| `optional` | `list<string>` | Agent Mesh-specific (not part of the A2A spec). Same caveats as `required`. |

Round-trip property: `sam config apply` followed by `sam config pull`
preserves every A2A field above. `required` / `optional` are dropped
from the YAML round-trip by design and live only on the platform DB.

```yaml
skills:
  - id: research_topics
    name: Research
    description: Research topics in depth and produce a written summary.
    tags: [research, knowledge]
    examples:
      - Tell me about quantum computing
      - Find recent ML papers on diffusion models
    inputModes: [text]
    outputModes: [text, application/json]
```

## Built-in prompt and the file tools

The agent's LLM gets a short system prompt: your `systemPrompt`, a few
working rules, and the tool and peer definitions, without built-in examples,
template and embed instructions or planning guidance. Tool results up to
8 KiB come back inline; a larger result is saved as an artifact and the agent
gets a reference to it.

Every agent also gets the artifact and data-analysis tools, so it can read
those saved results and the files users upload, and turn them into files. To
leave them out of an agent, set `autoInjectArtifactTools: false` or
`autoInjectDataAnalysisTools: false`.

The embed, `artifact_content` and template guides follow the file tools: an
agent that can read saved results gets them, and the template guide only when
the user's gateway shows templates. Any of these can be set explicitly under
`additionalConfigurations`, and a value you set always wins:
`enableExamples`, `enableTemplateInstructions`, `enableEmbedResolution`,
`enableArtifactContentInstruction`, `headless`,
`autoInjectArtifactTools` and `autoInjectDataAnalysisTools`.

## `additionalConfigurations.agentCard.priority` — chat picker order

Integer sort weight for the chat agent dropdown. Higher wins. The
highest-priority non-internal agent becomes the default chat target when
no project default is pinned. Absent or `0` means "no opinion" — the UI
falls back to alphabetical order. Carried on the agent card as the
`https://solace.com/a2a/extensions/sam/priority` extension.

```yaml
spec:
  additionalConfigurations:
    agentCard:
      priority: 100
```

## `additionalConfigurations.agentCard.welcome` — chat welcome screen

Drives the welcome panel shown when a chat session first opens against
this agent. Carried on the agent card as the
`https://solace.com/a2a/extensions/sam/welcome` extension and rendered
by the Agent Mesh chat UI.

| Field | Type | Description |
|---|---|---|
| `message` | `string` | Heading shown on the welcome screen. |
| `suggestions[]` | `list` | Suggestion chips shown below the heading. Up to a handful of starter prompts. |
| `suggestions[].label` | `string` (required) | Button text. |
| `suggestions[].prompt` | `string` (required) | Text injected into the chat input when the chip is clicked. |
| `suggestions[].autoSend` | `boolean` (default `false`) | When `true`, the chip submits the prompt immediately on click. When `false`, the prompt is placed in the input box for the user to edit before sending. |

```yaml
spec:
  additionalConfigurations:
    agentCard:
      welcome:
        message: "What would you like to explore today?"
        suggestions:
          - label: Summarise a document
            prompt: Summarise the attached document in 5 bullet points.
            autoSend: true
          - label: Draft a reply
            prompt: Draft a polite reply to the most recent email.
```
