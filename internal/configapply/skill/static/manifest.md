## Manifest Reference

The manifest is the entry point for `sam config plan` and `sam config
apply`. It declares the platform target, any imported sources, and the
list of resources to reconcile.

### Minimal example

```yaml
kind: manifest
name: dev
description: Local development environment
target:
  url: https://platform.example.com
  auth:
    type: bearer_token
    envVar: PLATFORM_TOKEN
resources:
  models:
    - default-model
  agents:
    - orchestrator
    - researcher
```

### With imports and variables

```yaml
kind: manifest
name: prod
target:
  url: https://sam.example.com   # a literal — the manifest does not expand ${VAR} here; `--url` overrides it
  auth: { type: bearer_token, envVar: PLATFORM_TOKEN }
variables:
  region: us-east-1
sources:
  shared: git+https://github.com/example/sam-shared.git@v1.2.0
resources:
  models:
    - default-model@shared
  agents:
    - { from: orchestrator@shared, as: prod-orchestrator }
    - local-helper
```

### Vault references (`vault://`)

A string field in a resource file can take its value from HashiCorp Vault
with `vault://<secret-path>#<field>`, for example
`api_key: vault://secret/data/anthropic#api_key`. `sam config plan` and
`apply` resolve it on the machine running the CLI, against `VAULT_ADDR`
with `VAULT_TOKEN` or, when that is unset, `~/.vault-token`. Three rules:

- The reference ends at the next whitespace, quote, `,`, `}` or `]`, so a
  prefix composes (`https://vault://secret/data/api#host`) but a suffix
  does not — `#host/v1` reads `host/v1` as the field name. Put the whole
  value in the secret instead.
- A field that exists but holds `""` is reported unresolved, not
  substituted.
- A `SKILL.md` applied with `sam config` is resolved on the CLI machine
  like any resource file, `tools:` block included, and the resolved value
  is stored in the uploaded skill bundle. Only a skill delivered by the
  filesystem or ZIP routes resolves its `tools:` block in the agent
  process at skill load; that process needs its own `VAULT_ADDR` and
  token, and an unresolved reference there skips that one tool entry.

Full rules, including which skill fields resolve: the `sam-docs` skill,
`building/declarative-config/secrets-and-variables.md` (*Vault References*
and *References Inside a Skill*).

### Interactive OAuth (`auth.type: oauth`)

```yaml
target:
  name: dev                                  # cache key for `sam auth login`
  url:  https://platform.dev.example.com
  auth:
    type: oauth
```

`sam auth login dev --url https://platform.dev.example.com` opens a
browser, completes the loopback PKCE flow, and writes the Agent Mesh token to
`$XDG_CONFIG_HOME/sam/auth/dev.json` — use the manifest's `target.name`
as the positional so the cache key matches. Subsequent `sam config apply` /
`sam config plan` / `sam config pull` runs read it transparently.
Setting `SAM_PLATFORM_TOKEN` still wins over the cache (CI flows are
unaffected). See `references/cli-auth.md` for the full surface.

### RBAC resources

Access control is declared through four resource keys:

| Key | Purpose | Reference |
|---|---|---|
| `rbacRoles` | Named scope bundles, plus the identities granted them via `spec.users` | `references/rbacRole.md` |
| `rbacClaimMappings` | Maps an OIDC claim value to a set of roles | `references/rbacClaimMapping.md` |
| `rbacGrants` | Grants a managed/in-DB role that has no config definition to carry `spec.users` | `references/rbacGrant.md` |
| `serviceAccounts` | Non-human principals (`system:<name>`) holding roles; editing `roleNames` grants and revokes on apply. `systemUsers`, the key used before the rename, is read until 2027-12-01 | `references/serviceAccount.md` |

Role grants live on the role file itself via `spec.users` — there is no
separate `rbacAssignments` key (it was removed; a manifest still
declaring it fails with a migration message).

The default-role set — the roles an identity receives when no grant or claim
mapping matches it — is not a resource key. It lives in the manifest's
`platform` block:

```yaml
platform:
  defaultRoles:
    roles:
      - analyst
```

An absent `defaultRoles` block leaves the platform's default-role set
unmanaged; an empty `roles` list clears the override and restores the
deployment's YAML fallback.

Every name must resolve to a **DB role** — one defined in this repo, one the
platform ships, or one created through the UI/API. A role the operator loads
from YAML at platform startup has `origin: yaml`, is not a DB row, and fails
the apply with `platform.defaultRoles: role "x" is not a known DB role`. That
rules out the roles in a stock Helm `authenticationRbac` block, `sam_user`
among them.

For the scope grammar used in `rbacRoles[*].spec.scopes`, read
`references/rbac-scopes.md`.

### Web UI settings

The web UI's instance-wide branding and assistant defaults — layered over
the gateway's own YAML/env values — also live in the manifest's `platform`
block, not as a resource key:

```yaml
platform:
  webuiSettings:
    appName: "Acme Assistant"
    welcomeMessage: "How can I assist you today?"
    disclaimerText: ""
    logoUrl: "https://cdn.acme.com/logo.svg"
    smallLogoUrl: "https://cdn.acme.com/logo-sm.svg"
    collectFeedback: true
    publishFeedback: false
    feedbackTaskInfoLevel: "none"
    systemPurpose: "You are a support assistant for Acme."
    responseFormat: "Answer in Markdown. Cite sources."
```

Each field has three states. A value sets the override; an explicit `null`
clears it on any apply; a key left out declares nothing, so a plain apply
preserves whatever the platform stores and only `apply --prune` clears it.
Write `null` to clear a field without `--prune`.

Removing the whole `webuiSettings` block from a manifest that still has a
`platform` block marks every stored override for removal, which `apply
--prune` clears. An empty `{}` declares no key either, so it also waits for
`--prune`. A manifest with no `platform` block at all manages none of the
platform settings and never removes them.
`systemPurpose` and `responseFormat` apply to the web UI's own tasks only.
Every other entrypoint (Slack, Teams, webhook, event-mesh, MCP) carries its own pair under `spec.values` and is not reachable from this
block; see the `entrypoint` kind for that.

`sam config schema manifest` renders the live field schema; this
section is a quick orientation, not the canonical reference.
