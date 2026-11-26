A toolset is a discoverable collection of tools (Python or Go binaries)
that agents and workflows can call. The metadata (name, description,
shared config) lives in YAML; the actual tool code lives in a per-toolset
directory under the same `toolsets/` tree. Built-in toolsets do not need a
YAML entry — list them by ID under `spec.toolsets:` on the agent or
workflow that uses them.

## Built-in toolset IDs

Built-in toolsets are pre-installed on every platform instance. Reference
them by their platform ID in `spec.toolsets:` on the agent — no toolset
YAML file is needed. Use `sam api GET /api/v2/platform/toolsets` to get
the live list for your instance.

| ID | Legacy `group_name` | Description |
|---|---|---|
| `builtin_artifact_tools` | `artifact_management` | Create, read, update, and manage artifacts |
| `builtin_web_request_tools` | `web_tools` | HTTP GET/POST/PUT/DELETE requests to external services |
| `builtin_research_tools` | `research` | Iterative multi-step web research + Google search |
| `builtin_image_tools` | `image_tools` | Describe, generate, and edit images; describe audio |
| `builtin_file_tools` | `general_agent_tools` † | Convert PDF, DOCX, XLSX, HTML, CSV, PPTX to Markdown; extract per-page PDF text |
| `builtin_time_tools` | `general_agent_tools` † | Get current time and date |
| `hil_tools` | `hil_tools` | Human-in-the-loop: `ask_user_question` |
| `data_analysis` | `data_analysis` | SQL queries, JMESPath transforms, SQLite, structured merge |
| `scheduling_tools` | `scheduling_tools` | Create and manage scheduled tasks in-chat: `schedule_task` (create), `list_scheduled_tasks`, `update_scheduled_task`, `delete_scheduled_task`, `set_scheduled_task_enabled` |

† `builtin_file_tools` and `builtin_time_tools` carve specific tools out of
the legacy `general_agent_tools` group (the file-conversion and
text-extraction tools, and `get_current_time`, respectively); there is no exact one-to-one legacy alias.

```yaml
# agents/my-agent.yaml
kind: agent
name: my-agent
description: "Agent that searches the web and manages artifacts."
spec:
  toolsets:
    - builtin_research_tools
    - builtin_artifact_tools
    - hil_tools
  ...
```

> **Note:** the legacy runtime group-name aliases in the middle column
> (`artifact_management`, `research`, `web_tools`, `image_tools`, …) are also
> accepted in `spec.toolsets:` — they resolve to the same built-in toolsets as
> the `builtin_*` IDs. Either form validates; prefer the `builtin_*` IDs for
> consistency. These names go under `spec.toolsets:`, **never** inside an
> agent's `additionalConfigurations.tools` (which the platform rejects).

## Binding a toolset to a workflow

Workflows bind toolsets the same way: a `toolsets:` list on the workflow
spec (alongside the workflow definition), accepting the same names as
agent `spec.toolsets:`. At deploy time the platform expands each toolset
into its tools; a custom toolset's tools register as `<toolset>__<tool>`,
which is the name a `type: tool` node references in `tool_name:`. Toolset
`spec.config` (secrets, shared defaults) applies exactly as it does for
agents, and toolset re-uploads/config changes auto-redeploy bound
workflows. See the workflow kind reference for the node shape.

```yaml
# workflows/greeter.yaml
kind: workflow
name: greeter
description: "Calls a toolset tool directly, without an agent."
spec:
  toolsets:
    - echo-tools
  workflow:
    nodes:
      - id: greet
        type: tool
        tool_name: echo-tools__greet
        input: { name: "{{workflow.input.name}}" }
    output_mapping:
      greeting: "{{greet.output}}"
```

## On-disk layout

Everything for a toolset lives under `toolsets/`:

```
toolsets/
  <name>.yaml             kind: toolset metadata + spec.config
  <name>/
    src/                  build sources (author flow — Go go.mod, Python
                          requirements.txt, or build.sh/build.bat —
                          see tool-build.md)
    <name>.zip            pre-built bundle (mirror flow — see below)
    dist/<os>-<arch>/     per-target build output (gitignored)
    .sam-cache/build/
      <os>-<arch>/        per-target build cache (gitignored)
```

Each toolset is in exactly one of two workflows, which the resolver
picks up purely from filesystem state:

- **Author flow** — `toolsets/<name>/src/` exists. `sam config plan` /
  `apply` runs the build pipeline, zips `dist/`, and uploads.
- **Mirror flow** — `toolsets/<name>/<name>.zip` exists. The pre-built
  zip is uploaded as-is. This is the canonical output of
  `sam config pull`: pull from Agent Mesh-A, apply to Agent Mesh-B without rebuilding.

Both `src/` and `<name>.zip` present at the same toolset is a hard
error — the two workflows are mutually exclusive. Delete whichever path
doesn't match your current intent.

## CLI lifecycle

The full set of commands that touch a toolset, in roughly the order an
author hits them:

| Command | What it does |
|---|---|
| `sam toolset init NAME [PATH] --lang go\|python` | Scaffolds `toolsets/<name>.yaml` + `toolsets/<name>/src/` with a working samtoolsdk skeleton and `build.sh`/`build.bat`. Go tools get the SDK source vendored from the CLI binary into `_sdk/samtoolsdk/` (no network needed). Python tools declare `sam-tool-sdk>=0.1,<0.2` from PyPI in `pyproject.toml` (network needed at `pip install` time). |
| `sam toolset sync [PATH] [--name N] [--lang …]` | Re-vendors the embedded Go SDK in one or every `toolsets/*/src/`. Run after a CLI upgrade. No-op for Python tools (which pull from PyPI). |
| `sam config plan` | Shows the diff. Builds (or cache-hits) author-flow toolsets and shows `[BUILD: built/cache-hit <os>/<arch>]` (target tuple included so a stale-platform cache hit is visible); mirror-flow toolsets show `[BUILD: none]`. |
| `sam config apply` | Builds, zips, uploads. Waits for platform `discoveryStatus = ready` before PATCHing `spec.config` so the toolset has its declared schema by the time config lands. |
| `sam config pull` | Mirror flow. Writes `toolsets/<name>.yaml` (with secrets rewritten as `${TOOLSET_<NAME>_<KEY>}`) and `toolsets/<name>/<name>.zip` (verbatim bundle bytes). The output is a `sam config apply`-ready repo. |
| `sam config cache prune` | Walks every `toolsets/*/.sam-cache/` and applies the prune policy (default: 30-day age cap). Cache never auto-prunes. |
| `--no-build` | Skips builds (soft-skip on plan, hard-error on apply). For CI flows that build in a separate step. |

See `references/tool-build.md` for the deep dive on the build pipeline
(resolution order, script contract, `dist/` convention, cross-platform).

## Scaffolding a new tool

`sam toolset init NAME [PATH] --lang go|python` scaffolds a working
tool directory under `toolsets/<name>/src/`.

- **Go tools**: the Go SDK source is embedded in the CLI binary and
  written to `toolsets/<name>/src/_sdk/samtoolsdk/` at scaffold time. The
  generated `go.mod` references it via a `replace` directive so the tree
  builds offline. `sam toolset sync` re-vendors after a CLI upgrade.
- **Python tools**: the scaffold's `pyproject.toml` depends on
  `sam-tool-sdk>=0.1,<0.2` from PyPI. Local dev uses `pip install -e .`
  into a venv for IDE autocomplete; the build (see below) re-installs
  into the deployment-target Lambda Layer via `pip install --target`.

See `references/tool-build.md` § "Scaffolding a new tool" for
path-resolution rules and language-specific examples. For the tool
**authoring** API — what to write inside the tool itself — see
`references/tool-build.md` § "Authoring pitfalls" (Go: `sdk.NewTool`,
`sdk.OK`/`Error`, `WithData`/`WithDataObjects`) and § "Python tool
authoring" (Python: `tool_cli`, `ToolResult.ok`/`error`,
`SandboxToolContextFacade`, `DynamicTool`, plus the legacy-tool
migration steps).

## Build behavior (author flow)

`sam config plan` and `sam config apply` build tool sources before
bundling them — Go and Python sources compile via convention, anything
else can ship a `build.sh` / `build.bat` pair. Built bundles are cached
per-toolset AND per-target under
`toolsets/<name>/.sam-cache/build/<os>-<arch>/` keyed by source content
hash + target tuple + bundler format version, so warm-cache plans are
no-ops, a local `darwin/arm64` build can't poison a `linux/arm64`
deploy, and a CLI upgrade that changes how a source tree maps onto zip
entries invalidates the cache instead of re-uploading a stale bundle. `--no-build`
skips builds (soft-skip on plan, hard-error on apply).

`sam config cache prune` walks every `toolsets/*/.sam-cache/` under the
repo root and applies the prune policy uniformly.

If a Go tool's vendored SDK directory (`_sdk/`) is missing at build
time — e.g. you cloned a repo where `_sdk/` is gitignored — the build
pipeline re-injects the embedded SDK from the CLI binary before running
`build.sh`. A one-line info log surfaces the action. Set
`SAM_TOOL_SDK_REFRESH=1` to force a re-vendor on every build. Python
tools have no equivalent self-heal — `pip install` from PyPI handles
dependency resolution on each build.

See `references/tool-build.md` for the full resolution order, build
script contract, cache layout, and CLI flags. The skill's
`sam config cache prune` subcommand is documented there too.

## Toolset-level config

A toolset's `spec.config:` block declares **shared default values** for
the toolset's declared config fields (the same fields each tool's
`samtoolsdk.ConfigSchemaField` describes). On `sam config apply`, those
values are PATCHed to the toolset's configuration and become the
default that every agent using the toolset inherits.

```yaml
kind: toolset
name: openai-tools
description: "OpenAI tool package."
spec:
  config:
    api_base: ${OPENAI_API_BASE, https://api.openai.com/v1}
    api_key: ${OPENAI_API_KEY}    # secret; resolved from the local env
```

Authoring rules:

- **Put secrets at the toolset level**, not per-agent. One API key
  serves every agent using the toolset; the per-agent
  `toolsetConfigs:` block is for non-secret tunables (model name,
  temperature, …) that diverge across agents.
- `${VAR}` and `${VAR, default}` references are substituted from the
  process environment at apply time (the same `configloader.ExpandVars`
  pass the rest of the YAML uses). Set the env var before running
  `sam config apply` — secrets never round-trip through git.
- Secret fields the platform returns redacted as `<REDACTED>` on
  subsequent GETs are dynamically excluded from the plan-time diff, so
  re-plans of an unchanged repo emit no spurious UPDATE for the
  toolset.
- Every `spec.config` key must be declared in a tool's config schema
  (`samtoolsdk.ConfigSchemaField`, or `@with_config_schema` in Python)
  or be a reserved key (`auth`, `hil`, `required_scopes`), or name a
  tool that declares a config schema (a per-tool map such as
  `post_entry: {api_base: ...}`, which wins over a flat key for that
  tool). `sam config apply` rejects any other
  key after discovery, before writing config; `sam config plan` also
  rejects it when the bundle is unchanged and already discovered.

### Reserved key: `auth` (deployer OAuth credentials)

For tools that declare `samtoolsdk.WithAuth` (or its Python equivalent), the
SDK contributes the OAuth *protocol shape* — `type`, scope list, authorization
URL, token URL — but **not** the per-deployment OAuth client identity. That
half lives in `spec.config.auth`, a reserved key inside the same config map:

```yaml
kind: toolset
name: powerbi_query
spec:
  config:
    auth:
      credential:
        client_id: ${POWERBI_OAUTH_CLIENT_ID}
        client_secret: ${POWERBI_OAUTH_CLIENT_SECRET}  # confidential clients only
      scheme:
        disable_consent_prompt: true              # optional (Microsoft Entra ID)
        resource: https://mcp.example.com/mcp     # optional (RFC 8707 resource indicator)
        refresh_url: https://auth.example/refresh # optional
        token_endpoint_auth_method: client_secret_post # optional
```

`audience` and `disable_pkce` are left out of the example because Microsoft
Entra ID uses neither. Set `disable_pkce: true` only for a provider that
rejects PKCE.

Validator rules (enforced on `sam config apply`):

- `auth.credential` accepts `client_id`, `client_secret`, `username`,
  `password`, and `token`. `client_secret` is the app-level secret of a
  confidential OAuth client (Microsoft Entra ID, Atlassian 3LO); it is sent on
  the code exchange and on every token refresh. Each user's own access and
  refresh tokens never appear here — the runtime keeps them in the per-user
  credential store. `client_secret`, `password`, and `token` read back as
  `<REDACTED>`.
- `auth.scheme` accepts **only** `audience`, `resource`, `refresh_url`,
  `token_endpoint_auth_method`, `disable_pkce`, and `disable_consent_prompt`
  (the last two are booleans). The SDK-declared `authorization_url`,
  `token_url`, and `scopes` are authoritative — overriding them is rejected
  with HTTP 400 to prevent silent drift.
- `auth.scheme.disable_consent_prompt: true` drops `prompt=consent` from the
  sign-in URL. Set it for Microsoft Entra ID: Entra reads `prompt=consent` as
  a request for the user's own consent even after an admin has consented, so
  in tenants that block user consent every user sees "Approval required".
  Leave it unset for Google, which re-issues a refresh token only on a forced
  consent.
- `auth.type` accepts `oauth2`, `basic`, or `bearer` and overrides the SDK
  declaration, so one tool binary can use OAuth2 for one agent and static
  auth for another.
- Tools cannot declare a `ConfigSchemaField` whose `key` is `auth`; the SDK
  panics at registration and the platform rejects the upload as a fallback.

Rotating `auth.credential.client_secret` (or `password` / `token`): change the
environment variable and run `sam config apply`. The plan asks the platform
whether the stored secret still matches and pushes a config-only update when
it doesn't, even though the redacted value never shows in the plan diff. The
check runs only for toolsets with no other planned change: when the same
apply also uploads a new bundle or changes the toolset's metadata, run
`sam config apply` a second time to push the new secret.

At deploy time the platform merges this block on top of the SDK-declared
auth and emits it as the per-tool `auth:` block in the runtime YAML AWE
consumes.

### Reserved key: `hil` (human-in-the-loop approval)

To gate a toolset's tools behind a user-approval prompt — the same
pre-dispatch HIL that MCP-connector tools support — set a reserved `hil`
key inside the config map. It is keyed by **tool name** (a package may
register several tools, each gated independently), and each value is a
single-tool HIL entry (the `builtin`/`sam_remote` shape — NOT the MCP
`tools:` sub-map form):

```yaml
kind: toolset
name: atlassian_rest_request
spec:
  config:
    base_url: https://api.atlassian.com
    hil:
      atlassian_rest_request:           # the tool name, not the toolset name
        # Gate writes only; reads (GET/HEAD) pass through unattended.
        require_approval_when:
          - arg: method
            not_in: [GET, get, HEAD, head]
        approval_message: "{{.method}} {{.path}} — approve this Atlassian write?"
        show_args: true
```

Entry fields: `require_approval` (bool), `require_approval_when` (list of
conditional-gating rules — see *Conditional gating* in
`references/design/agent-design.md`), `approval_message` (Go
`text/template` over the call args), `show_args` (bool), `timeout`
(duration string, e.g. `"30m"`). A call gates when `require_approval` is
true OR any rule matches.

Validator rules (enforced on `sam config apply`):

- Keys must be tool names the package registers; an unknown name is a 400.
- The MCP-only `tools:` sub-map is rejected here — a toolset block is keyed
  by tool name directly.
- Each `require_approval_when` rule needs a non-empty `arg` and exactly one
  operator (`eq`, `in`, `not_in`, `exists`, `not_exists`, `gt`, `lt`,
  `gte`, `lte`).
- Tools cannot declare a `ConfigSchemaField` whose `key` is `hil`.

`hil` works in the per-agent `toolsetConfigs` overlay too; an agent's entry
for a given tool replaces the toolset default for that tool. At deploy time
the platform emits the resolved entry as the per-tool `hil:` block in the
runtime YAML AWE consumes.

### Reserved key: `required_scopes` (per-tool RBAC)

To gate a **custom STR tool package's** tools behind RBAC — so only callers
whose identity holds the scope may invoke them — set a reserved
`required_scopes` key inside the config map. (Built-in toolset groups can't be
gated this way — they have no package config surface.) Like `hil` it is keyed
by **tool name**, and each value is a non-empty list of scope strings the
caller must satisfy (**all** of them — AND semantics):

```yaml
kind: toolset
name: compliance_tools
spec:
  config:
    required_scopes:
      run_migrations:                                    # the tool name
        - tool:compliance_tools__run_migrations:invoke   # tool:<toolset>__<tool>:<verb>
```

Use the canonical shape `tool:<toolset>__<tool>:<verb>` — segment 2 is the
registered tool name the deployer emits (`<toolset>__<tool>`), so a grant of
that scope (or a wildcard like `tool:compliance_tools__*:invoke` /
`tool:*:*`) matches. A tool with no entry is ungated beyond the agent's own
invoke scope.

Validator rules (enforced on `sam config apply`):

- Keys must be tool names the package registers; an unknown name is a 422.
  Because this is a security control it fails **closed**: if the package has
  no discovered tools yet, every entry is rejected rather than accepted.
- Each value must be a non-empty list of concrete `tool:` scopes — segment 1
  must be `tool`, and a required scope may **not** contain a `*` wildcard or
  its older `_` spelling (those match only on the granted side, so as a requirement
  they can only fail closed). Empty strings and a per-tool empty list are
  rejected.
- Tools cannot declare a `ConfigSchemaField` whose `key` is `required_scopes`.

**Removing a gate:** set `required_scopes: {}` (the explicit "clear all"
spelling — it converges under `sam config apply`). Simply *omitting* the key
does **not** remove a stored gate — like `auth`/`hil`, an absent key is
preserved from the stored value.

`required_scopes` works in the per-agent `toolsetConfigs` overlay too. The
overlay is **union-merged** with the toolset default per tool: since a caller
must hold *every* listed scope (AND), adding scopes only makes the gate
stricter. So an agent overlay can **only add** requirements (narrow the gate) —
it can never remove a scope the toolset owner authored. This matters because
the two layers sit behind different route scopes (`agent_builder:*:update` vs
`toolset:*:update`); union-merge guarantees an agent editor cannot weaken a
toolset-owner's gate. An absent or ill-typed overlay value contributes nothing,
so the toolset default always stands.

Because the union is AND-ed, the caller needs **every** scope across both
layers: a toolset default of `…:read` plus an overlay of `…:invoke` requires
both, not either. Nothing warns at author time if no role holds the whole set,
so keep the two layers on the same verb unless you intend the conjunction —
and note there is deliberately no way to *relax* a toolset gate for a single
agent; to loosen it, change the toolset default.

**Granting the scope:** authoring a gate does not create a grant. `tool:`
scopes are not in the assignable catalog and the roles UI has no free-text
scope field, so the gate must be granted declaratively — add the scope to a
role in your RBAC roles manifest and `sam config apply` it. Until a role
grants it, the gated tool is invokable only by holders of `tool:*:*`, which the
built-in `sam_manager` role grants; the role editor cannot grant it.

At deploy time the platform emits the resolved list as the per-tool
`required_scopes:` block. Enforcement is **agent-side**: the agent process
checks it at dispatch (`Set.Dispatch` → `AuthorizeTool`) and hides the tool
from the model at list time (`FilterTools`). The STR worker itself does not
re-check per-tool scopes.

## Per-agent overlay (`toolsetConfigs`)

A toolset's `spec.config` is the **shared default** — every agent that
uses the toolset inherits those values. To override values for a
specific agent (e.g. point that agent at a different region, raise its
timeout, swap models), declare a `toolsetConfigs[*]` entry on the
agent's YAML:

```yaml
# agents/sales-bot.yaml
kind: agent
name: sales-bot
spec:
  toolsets:
    - openai-tools          # references toolsets/openai-tools.yaml
  toolsetConfigs:
    - toolsetName: openai-tools
      configValues:
        chat_completion:            # tool name the toolset registers
          api_base: https://api.openai.example.internal/v1   # agent-specific override
          verbose: true                                      # not set at toolset level
```

`configValues` is **nested by tool name**: each top-level key is a tool
the toolset registers, mapping to that tool's `field → value` config.
This differs from the toolset's own `spec.config`, which you write flat —
`sam config apply` fans each flat key out to every tool that declares it.
The overlay is sent as written, so a flat overlay is rejected with
`unknown tool name for this toolset`.

At deploy time the runtime merges the two sources per key:
**`toolsetConfigs[*].configValues` wins where keys collide**, otherwise
the toolset-level `spec.config` value falls through. Keys that appear
only in `spec.config` are inherited unchanged; keys that appear only in
the agent overlay are added on top.

The shape of `configValues` is the same for both toolset natures;
only validation differs:

| Toolset nature | `configValues` shape | Validation |
|---|---|---|
| `kind: toolset` package (this kind) | **Nested by tool name**, e.g. `{fetch_report: {api_key: …, timeout: 30}}`. The reserved `auth`, `hil`, and `required_scopes` keys stay at the top level. | Schema-validated against each tool's `config_schema`. A package that declares no `config_schema` on any tool skips validation and stores whatever you send, but those values reach no tool. |
| Builtin toolset (e.g. `builtin_research_tools`) | **Nested by tool name**, e.g. `{deep_research: {max_iterations: 2, sources: [web]}}`. Top-level keys must be tools the toolset registers, or the reserved `hil` key (approval settings keyed by tool name, as described in the Reserved key: `hil` section). | The agent runtime validates the inner per-tool config shape when it loads the config. Credential keys (`google_search_api_key`, the Datadog `api_key` and `app_key`, the image tools' `api_key` and `gemini_api_key`) must be strings and read back as `<REDACTED>`; use `${VAR}` for them. Built-in toolsets have no toolset-level config, so unlike a package secret their credentials belong in this per-agent block. |

Authoring guidance:

- **Put secrets at the toolset level** (`spec.config`), not in the
  agent overlay. The overlay is for non-secret per-agent tunables.
- The overlay is the right place for fields that diverge across
  agents that share the toolset — region, model name, temperature,
  verbosity.
- Round-trip on pull: the agent's `toolsetConfigs[*].configValues`
  comes back as literal values. Secret fields inside the overlay are
  rewritten as `${TOOLSET_<NAME>_<KEY>}` placeholders just like
  `spec.config`.
- The reserved `auth` key works in the overlay too — agents that need
  a different OAuth client identity than the toolset default supply
  their own `auth.credential.client_id` here. Merge semantics match
  every other key: agent overlay wins where keys collide, so the
  agent's full `auth` block replaces the toolset's. To tweak one field
  copy the toolset block and edit.
- **`deep_research` with a `web_search` connector:** when the agent
  also lists a `web_search` connector under `spec.connectors`, the
  connector supplies `deep_research`'s search provider, key and
  `api_base` at deploy, and the Google keys in its `configValues`
  block (`google_search_api_key`, `google_cse_id`, `api_base`) are not
  used. Its other keys (`max_sources_per_iteration`,
  `interactive_plan_verification`, a `safe_search` that is on) still
  apply.

## Mirror flow (pull → apply)

`sam config pull` downloads:

- `toolsets/<name>.yaml` — metadata + `spec.config` with secrets
  rewritten as `${TOOLSET_<NAME>_<KEY>}` placeholders.
- `toolsets/<name>/<name>.zip` — the platform's stored bundle bytes,
  verbatim.

A subsequent `sam config apply` against a different Agent Mesh instance picks
up the zip, skips the build pipeline, and uploads the bundle as-is.
Secrets are not pulled — the placeholders must be filled by env vars or
`<SAM home>/secrets/<toolset>.env` before apply succeeds.

**Pulled non-secret values are literal** — `${VAR}` references are not
preserved across the round-trip; only secret fields come back as
placeholders. If you want to re-template after a pull, re-add the
`${VAR}` reference by hand. The same caveat already applies to connector
`authConfig` and model `apiKey` fields.
