An entrypoint exposes the platform to external clients (HTTP, Slack, MS
Teams, etc.). The `type` field is immutable after creation. The
`values` field is a free-form object whose shape is dictated by the
entrypoint type's schema — consult the per-type section below (or fetch a
running entrypoint with `sam config pull --only entrypoint --name X -o <dir>`) for
the per-type field set. `publicUrl` is optional, must be an `https://` URL,
and is only meaningful for HTTP-style entrypoints. Removing `publicUrl` from the
YAML clears the stored one at the next `apply`, and the plan row reads
`update + deploy (clears publicUrl)`. A `values` key the file leaves out is
always kept.

### Connection model: outbound (Socket Mode) vs inbound (webhook)

Entrypoint types differ in *which direction* the connection runs, and this
dictates what you must configure outside the platform:

- **Outbound / Socket Mode (e.g. `slack`)** — the entrypoint opens an
  outbound WebSocket to the provider using an app token. No inbound
  endpoint, no public URL, no DNS — it works behind NAT. Just supply the
  tokens in `values`.
- **Inbound webhook (e.g. `teams`, HTTP-style)** — the provider POSTs to
  a public URL the entrypoint exposes. You MUST register that URL on the
  provider side (see below), and the platform host must be publicly
  reachable.

### Inbound webhook URL (the `/gw/<slug>/...` named mount)

Non-root entrypoints are mounted under **`/gw/<slug>/`** on the platform's
public host when the entrypoint has a slug, and under **`/gw/<uuid>/`** when
it does not. The slug is **opt-in and set at creation** — it is never derived
from `name`, and it cannot be changed afterwards. It is 3–63 characters,
starts with a lowercase letter, contains only lowercase letters, digits and
dashes, does not end with a dash, and cannot be one of the reserved words
`oauth`, `api`, `well-known`, `gw` or `health`. The internal UUID changes
every time the entrypoint is deleted and recreated, so **set a slug before
registering a URL with an external provider**: a URL bound to the slug
survives delete/recreate as long as the recreated entrypoint reuses the same
slug, while a URL bound to the UUID breaks.

Write `slug` under `spec:`, not beside `kind` and `name`: `sam config plan`
rejects any top-level key other than `kind`, `name`, `description` and
`spec`. On an existing entrypoint, a `spec.slug` that differs from the live
one fails the plan with "slug is immutable"; delete and recreate the
entrypoint to change it, or omit `slug` to leave the live value alone.

For a **Teams** entrypoint, the Bot Framework **messaging endpoint** to set
in the Azure Bot registration's Configuration blade is therefore:

```
https://<platform-host>/gw/<slug>/api/messages
```

e.g. `https://sam.example.com/gw/teams-bot/api/messages`. You can
sanity-check the path before wiring Azure: a `POST` to it should return
**401** (Bot Framework auth rejecting the unsigned request) — a **404**
means the slug/path is wrong. The mount only exists after the entrypoint is
created, so the order is: apply the entrypoint → read its URL from the
networking details → set the provider URL.

### Single-tenant vs multi-tenant (Teams)

`microsoft_app_tenant_id` is **required for single-tenant Azure Bots**
(Microsoft App Type = SingleTenant): the entrypoint must mint its outbound
Bot Connector token from the bot's *home* tenant. Leave it **empty for
multi-tenant** bots (the token comes from the shared `botframework.com`
authority). Getting this wrong is a classic silent half-failure:
**inbound works** (the task is submitted) but **every outbound reply
fails** with `send activity: HTTP 401: Authorization has been denied for
this request`, because the Connector rejects a token minted from the
wrong authority. If you see inbound-ok / outbound-401, check the tenant
setting and the bot's App Type first.

### `run_as`: the service account an entrypoint authorizes as

`values.run_as` is settable from config on the chat entrypoint types
(`slack`, `teams`). The machine entrypoints carry the same setting in
camelCase: `values.runAs` on `webhook` and `mcp`, and a per-rule
`event_rules[].runAs` on `event_mesh`. This section's examples use the chat
spelling. It is config-only, so `sam config pull` is the only place
you will see it on an existing entrypoint. It names a
`serviceAccount` resource by its bare name, which the platform resolves to the
subject `system:<name>`, so `run_as: ops` means `system:ops`.

```yaml
kind: entrypoint
name: slack-support
description: Slack support channel for Acme customers
spec:
  type: slack
  values:
    bot_token: ${SLACK_BOT_TOKEN}
    app_token: ${SLACK_APP_TOKEN}
    run_as: support-bot
```

Declare the service account in the same config so the two land together, and
remember `roleNames` is the whole role set rather than an addition to it:

```yaml
kind: serviceAccount
name: support-bot
spec:
  roleNames:
    - support_agent_invoke
```

**What it changes.** On `slack` and `teams` it applies only to multi-party
surfaces (a channel or group chat), and 1:1 messages keep the real user;
`use_user_identity_in_channels: true` opts even channels back to the real
user.

Naming a service account also takes the deployment's **default roles** out of the
picture, which is usually the point. Defaults apply only to a principal that
matched nothing *and* is not a service account, so a run-as entrypoint reaches
exactly what its service account was granted instead of falling back to whatever
the defaults happen to hold. The sender is not lost either way, it just stops
carrying authorization: it still keys the session, still routes the reply, and
is recorded for audit.

**Clearing it takes an explicit empty value.** Config apply manages only the
`values` keys the YAML names and the platform keeps any key it is not sent, so
dropping the `run_as:` line plans no change and leaves the service account in place.
Write
`run_as: ""` to fall back to the built-in `system:default`; it does not hand
authorization back to the senders. Channel messages authorize as the real
sender only with `use_user_identity_in_channels: true`.

A blank `runAs` on the machine entrypoints resolves to `system:default` the
same way.

### Resource shape: `type` goes inside `spec`

`kind`, `name` and `description` are top-level; **everything else, `type`
included, lives under `spec`.** The loader unmarshals only those three plus
`spec`, so a `type:` written at the top level is dropped without a warning and
the apply fails at the API with `type is required` — a message naming a field
your YAML plainly sets, which is what makes this one hard to spot.
`description` is required and must be at least 10 characters.

### `systemPurpose` and `responseFormat`: per-entrypoint agent guidance

Every entrypoint type except `agent_link` accepts these two, and they are the
only per-entrypoint way to tell agents what a surface is for and how replies
should be shaped:

```yaml
kind: entrypoint
name: support-webhook
description: Inbound support tickets from the Acme status page
spec:
  type: webhook
  values:
    routePath: /support
    authMode: token
    authToken: ${SUPPORT_WEBHOOK_TOKEN}
    targetType: agent
    targetAgentName: SupportBot
    inputExpression: input.payload
    systemPurpose: |
      You handle inbound support tickets raised from the Acme status page.
      Reporters are customers, not internal staff.
    responseFormat: |
      Two or three short sentences, plain text. No markdown and no links:
      the ticket system renders neither.
```

The gateway stamps both onto every task the entrypoint submits, as the
`system_purpose` and `response_format` A2A metadata keys, so an agent picks them
up through `inject_system_purpose` and `inject_response_format` on its own
config. The built-in agents as seeded have both on (and `inject_user_profile`);
one you have edited or authored declaratively keeps its own setting. An agent
you author has both off unless its config sets them, and with them off it
ignores these values.

Keep each under 4000 bytes: both are injected into every prompt, so an
oversized value is a per-task cost rather than just a large row. Treat that as a
budget rather than a guardrail. It is *enforced* only on the four types whose
values are walked against the gateway registry (`webhook`, `mcp`, `event_mesh`,
`google_chat`), and never on the `snake_case` spelling, which the walker does not
know about. On every other type nothing rejects an oversized value. The limit counts bytes
rather than characters, so non-ASCII text reaches it sooner than its length
suggests.

Neither field is on any create form, so the web UI cannot set them, and
`sam config pull` only ever echoes what a YAML author already wrote. That makes
this the one part of the entrypoint schema where neither of the usual oracles
(the UI form, or pulling a UI-created entrypoint) tells you anything: write them
here.

`snake_case` spellings (`system_purpose`, `response_format`) are accepted too,
for configs written before the camelCase keys existed. An `agent_link`
entrypoint has neither field and refuses both spellings, as it refuses any key
it does not define. The first spelling
holding a **string** wins, empty or not, so `systemPurpose: ""` clears a value
even on a row that still stores `system_purpose` from before.

A bare `systemPurpose:` is not the same thing. YAML parses it as null, not as an
empty string, and a non-string falls through to the next spelling instead of
deciding, so it leaves any stored `system_purpose` in place. That is deliberate:
uncommenting a key from a generated example without filling it in should not
silently drop the value a row is running on. Write `""` to clear.

**Clearing takes an explicit empty value, not a deleted line.** Config apply
manages only the `values` keys the YAML names, so dropping the line plans no
change and leaves the old guidance in place.
Write `systemPurpose: ""` to clear it. This is the same trap `run_as` has, with
one difference worth keeping straight: a blank `run_as` is load-bearing and
reaches the adapter, whereas a blank `systemPurpose` emits no key at all, which
is what lets the layers underneath it apply again.

This is the right place to set them for Slack, Teams, webhook, event-mesh, and
MCP entrypoints. The web UI entrypoint (`httpsse`) is the one
exception worth knowing: it reads the same two keys, but the manifest's
`platform.webuiSettings` block overrides them instance-wide, so set them there
for the web UI and leave the entrypoint's own values for the other surfaces.

A per-task metadata value outranks both, but that is for internal publishers
only, and is not a way for a caller to steer an entrypoint: the web UI strips
both keys from client-supplied metadata precisely so a browser cannot outrank
the instance-wide value, and no other adapter populates them from the inbound
request. Treat them as administrator-owned.

#### Not to be confused with `event_rules[].responseFormat`

On an `event_mesh` entrypoint there is a second, per-rule `responseFormat`
nested inside `event_rules`. The two are different mechanisms:

| | `spec.values.responseFormat` | `event_rules[].responseFormat` |
|---|---|---|
| Scope | the whole entrypoint | that one rule |
| Delivery | stamped as task metadata | appended to the rule's prompt as literal text |
| Reaches | only agents with `inject_response_format` enabled | the agent either way |
| Target types | every rule | agent targets only, silently ignored on workflow targets |

Hoisting a value from the rule level up to `spec.values` therefore changes
behaviour silently: the text stops being appended to the rule's input, and any
agent that has not opted into `inject_response_format` stops seeing it at all.
Both are valid; pick the one whose delivery you want, and do not assume moving
it is a refactor.

### `default_agent_name` must be a real, deployed agent

The schema default for `default_agent_name` is the literal string
`Orchestrator` — the built-in orchestrator's name. That is a placeholder,
not a guarantee: it routes to an agent of that exact name, which only
exists if such an agent is actually deployed. Set `default_agent_name` to
the **exact `name:` of an agent in this same config** (and in the
manifest's `resources.agents`) or one already on the platform. A link agent
counts too (see "Naming a link agent from a workflow or an entrypoint" in
`references/linkAgent.md`). `sam config plan` rejects a name that matches
none of these; one that stops matching later (the agent was removed) is not
silently replaced — every unrouted message gets a reply that the entrypoint
is misconfigured.

### `agent_link`: the entrypoint link agents connect through (Early Access)

An `agent_link` entrypoint lets third-party agents join the mesh over one
WebSocket each, without access to the broker. It carries only connection
limits. The agents themselves are separate resources of kind `linkAgent`, each
naming this entrypoint in `spec.entrypoint` (see `references/linkAgent.md`).

```yaml
kind: entrypoint
name: partner-link
description: Entrypoint that partner agents connect through.
spec:
  type: agent_link
  slug: partner-link
  values:
    maxConnectionsPerAgent: 4
```

- **`values` is optional.** Every limit has a default (see the
  `## type: agent_link` table below), and a limit left out keeps the default.
  `values.agents` does not exist: a key that is not in that table, `agents`
  included, fails the apply. Register agents as `linkAgent` resources.
- **Set a `slug`.** Agents connect to `/gw/<slug>/v1/connect` on the
  platform's public host, over `wss://`. Without a slug the path carries the
  entrypoint's id, which changes if the entrypoint is deleted and recreated,
  and every agent then needs its new address.
- **Order.** `apply` creates and deploys the entrypoint before it deploys the
  link agents that name it, so declare both in one manifest. A link agent
  cannot be deployed while its entrypoint is not.
- **Redeploying disconnects.** Changing a limit plans `update + deploy`, and
  the entrypoint restarts with the new values. The connected link agents are
  disconnected and reconnect; the link SDK does that on its own. The
  entrypoint keeps its link agents across the redeploy.
- **Removing it.** The platform refuses to undeploy an `agent_link` entrypoint
  while link agents are deployed on it, and to delete one while any link agent
  names it. `apply --prune` deletes link agents, and updates the ones that
  move to another entrypoint, before it deletes entrypoints, so removing the
  entrypoint and its link agents, or moving undeployed ones off it, in one
  change works.
