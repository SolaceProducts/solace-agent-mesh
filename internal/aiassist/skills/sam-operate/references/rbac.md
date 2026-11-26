# RBAC: roles, scopes, providers

## Turn it on: `authorization_service.type`

Set in the entrypoint and platform YAML:

| Type | Behavior |
|---|---|
| `none` | every authenticated user gets `["*"]` (all scopes). Dev/test only. |
| `default_rbac` | resolves roles + scopes from YAML files and/or the platform DB. Production. |
| `deny_all` | no scopes granted. |

When the block is omitted, an entrypoint defaults to `none` (every authenticated user gets all scopes) unless `frontend_use_authorization: true` is set, in which case it defaults to `deny_all`. The platform defaults to `deny_all`. Set `type` explicitly on both rather than rely on the default.

(`type: default` is **not** a valid value — that was a baseline invention.)

## Scope syntax

Three colon-separated segments: `<category>:<resource>:<verb>`. Segment 2 is a specific instance name or `*` (any instance, or the whole collection for collection-level and flat-feature actions). Write `*`. Roles may also hold `_` in segment 2, an older spelling that grants exactly what `*` grants; some catalog scopes still use it, and so do roles that hold them. Wildcards: a granted `*` matches one required segment; a trailing `*` matches the rest. Bare `*` is the superadmin grant. Verified scopes:

- **Invocation** — `agent:<name>:invoke` / `agent:*:invoke` gates BOTH a user submitting a task to that agent through the entrypoint AND peer delegation (one agent routing to another) — same scope, both call paths. `workflow:<uuid>:invoke` / `workflow:*:invoke` is **deployed-workflow invocation**; deployed workflows live in their own `workflow` category, so `agent:*:invoke` does NOT cover them. The three-row rule for the required invoke scope: a deployed workflow needs `workflow:<dashed-uuid>:invoke`, a platform-deployed agent needs `agent:<dashed-uuid>:invoke`, and a YAML/built-in agent needs `agent:<name>:invoke`. Segment 2 is always the dashed UUID, never the underscored broker name.
- **Tool execution** — `tool:*:*` is the execute-any-tool wildcard. Most built-in tools require no scope of their own: agent access (`agent:<name>:invoke`) implies access to the tools configured on that agent. `tool:*:read` gates reading the tool catalog.
- **Management CRUD families** — each instance-managed resource follows `<category>:*:create` (collection-level, no instance yet) + `<category>:*:read` / `:update` / `:delete` (per-instance): `agent_builder`, `workflow_builder`, `connector`, `entrypoint`, `skill`, `toolset`, `model_config`. Per-instance `<category>:<name>:…` is accepted by the matcher but handlers still enforce the `*` form today, so a wildcard grant (e.g. `connector:*:*`) authorizes every instance. Link agents (third-party agents registered on an `agent_link` entrypoint, early access) are managed and deployed with the `agent_builder` scopes too; there is no separate link-agent category.
- **Deploy / test verbs** — deploying is a distinct verb, gated per resource: `agent_builder:*:deploy`, `entrypoint:*:deploy`, and — note — **workflow deployment uses `workflow_builder:*:deploy`**, not an entrypoint scope. `agent_builder:*:test` gates the builder's test-run.
- **RBAC management** — `rbac:*:read` (listed in the catalog as `rbac:_:read`, its older spelling) gates reading roles, assignments, and claim mappings, and is the *only* RBAC scope in the catalog. RBAC *writes* are not gated by a scope at all: creating, updating, or deleting roles, grants, and claim mappings requires the superadmin grant (`*:*:*`, or the bare `*`). There is no `rbac:*:create` / `:*:update` / `:*:delete` scope to grant, so never invent one as a way to delegate RBAC administration. `rbac:*:*` is likewise **not** a catalog entry (it appears only in test fixtures) and is refused for writes — don't hand-author it. <!-- scope-audit:ignore rbac:*:* rbac:*:create rbac:*:update rbac:*:delete -->
- **Evaluations** — `evaluation:*:read` / `:*:delete` are ordinary CRUD. Two are privilege-conferring and should not be handed out with the rest: `evaluation:*:create` / `:*:update` also set the **service account an experiment's runs execute as** (`runAs`), so the holder can point a run at any service account and inherit both its scopes and its connected tool credentials, with no second authorization check. `evaluation:*:invoke` triggers a run **and** connects or disconnects the delegated tool credentials that principal holds — treat it as a scope doing double duty, not as a read-only "can press Run" grant.
- **Flat platform features** — `deployment:*:read` (deployment audit log); `builder:*:use` (Builder-section UI umbrella); `profile_provider:*:read` (view) / `:*:update` (change) (identity/profile-provider wiring; GET accepts either scope, so a `:*:update`-only role can still view the wiring, while PUT/DELETE require `:*:update`); `webui_settings:*:read` / `:*:update` (instance-wide Agent Mesh UI branding, assistant defaults, and feedback handling).
- **`activity:*:read`** — the **elevated** Activity Monitor read. Invoking the Activity Monitor is the ordinary `agent:<name>:invoke` gate and every user already sees their **own** activity with no extra scope; this scope adds cross-user reach — another user's reports, the tenant-wide failed-task list, and full per-task traces including per-node event payloads. On the Activities page this scope likewise lists every user's tasks and opens them in full; the narrower per-agent route is `agent:<id>:read_trace`, described in the next bullet. The live stream, the buffered replay and the session title in the list stay limited to the owner and `activity:*:read`. In the Activity Monitor it also reaches every user's feedback: summaries, individual ratings, and the chat session behind any rating. **Content is bundled**: there is no separate content tier to withhold, so granting this grants prompt and response content for every user in the tenant. Treat it as the most sensitive read grant in the catalog.
- **`agent:<id>:read_feedback`** / `agent:*:read_feedback`, **`workflow:<id>:read_feedback`** / `workflow:*:read_feedback`, **`agent:<id>:read_trace`** / `agent:*:read_trace` — segment 2 follows the same rule as `:invoke`: the **dashed UUID** for a platform-deployed agent or workflow, the authored `name` for a YAML-only or built-in one. A grant written in the broker's `agent_<uuid_with_underscores>` instance form validates but matches nothing. The narrower, per-kind alternative to `activity:*:read` for the Feedback Dashboard and the Activities page, and the two are independent: a feedback-read grant opens that subject's feedback rows, on the Feedback page and through the Activity Monitor's feedback answers, and nothing on the Activities page; a trace-read grant ("Can view activities" on an agent card) lists on the Activities page the top-level tasks that agent handled, whoever ran them, service accounts included, each row carrying its status and request text, and opens a task, with full per-node event payloads and the status read, only once every agent that took part is covered (a task with one uncovered agent reports its details as not available), and nothing on the Feedback page. The Activity Monitor's `explain_task` opens a single task by the same coverage rule (every agent in the tree covered, a workflow-rooted tree never), after the owner and `activity:*:read` arms. The Activity Monitor uses both together for sessions: it opens the chat session behind a rating only for a caller who can read that rating and whose trace grants cover every agent in every turn of the session (a workflow-rooted turn never opens this way), and the owner of a session opens their own without either grant, unless another user owns one of its sub-tasks. There is no workflow-level trace verb, by design (a workflow run never opens through trace grants). Granting one of these to one agent or workflow does not imply the others.
- **Budgets and model spend** — `budget:*:read` (view budgets and who is on which), `budget:*:create` / `:update` / `:delete` (user budgets; the Agent Mesh budget is updated, never created or deleted; a delete also needs `budget:*:assign`, because its users revert to the default), `budget:*:assign` (change the default user budget, assign users, grant or cancel additional temporary amounts — assigning and additional amounts also need `budget:*:read`), and `*:*:read_usage` (every user's spend and token usage; your own needs no scope — note segments 1 and 2 are both `*`). Of the built-in roles, only `sam_manager` grants these: it holds `*:*:read_usage` and `budget:*:*`, every budget scope. The role editor offers none of them. The two read scopes can go on a role through `sam config apply`; only super administrators and `sam_manager` hold the four write scopes. Do not put the write scopes on a custom role: nothing stops a holder of `assign` or `update` from raising their own limit, and that holds for `sam_manager` holders too. Details in [budgets.md](budgets.md).
- **Ownership-gated (not CRUD families)** — `project` and `prompt` are gated by resource ownership, not scopes. Their only scope strings, `project:_:share` and `prompt:_:share` (the catalog's `_` spelling), are retained in the catalog but **no longer enforced** today.

The full catalog of fixed-string scopes ships with the entrypoint, plus the per-instance scopes (`agent:<name>:invoke`, `workflow:<uuid>:invoke`); the runtime picks between them per target by the three-row rule above. If a user asks for a scope you can't confirm from the RBAC reference doc, say you'd verify rather than emit a guess.

## Where an operator manages this

Three surfaces write the same role graph, and an operator can use any of them:

- **The User Management page in the Agent Mesh UI** — visible only to a superadmin, with **Claim Mappings**, **Users**, and **Roles** tabs plus a default-roles control. Its role editor grants **agent and workflow invoke scopes, plus per-agent and per-workflow `read_feedback` and per-agent `read_trace` grants**, so it cannot author a role carrying any management scope from the CRUD families above; such a role stays viewable and editable there, and its other scopes are preserved. Roles loaded from operator YAML files are read-only in the UI, so they cannot be granted to a user, named in a claim mapping, or used as a default role. The Users tab lists only identities that have already signed in, so an operator cannot pre-assign a role to someone who has never logged in — point them at a claim mapping for that.
- **`sam config apply`** — the `rbacRole`, `rbacGrant`, `rbacClaimMapping`, and `serviceAccount` kinds, plus `platform.defaultRoles` in the manifest. This is the only one of these surfaces that creates a service account. Deletes need `--prune`.
- **Operator YAML files** loaded at startup — the bootstrap path, and the way to establish the first superadmin, since RBAC writes themselves require one.

When a change made with `sam config apply` doesn't appear in a browser that already has the UI open, the cause is normally the page's own cache rather than a failed apply. Reloading resolves it.

## Where roles come from (two sources of truth)

1. **YAML files** (operator-bootstrapped baseline), pointed to from the authorization service:
   - `role_to_scope_definitions_path` → a roles file: each role has `scopes:` (and optional `inherits:` parents).
   - `user_to_role_assignments_path` → a users file: each identity (email lowercased; OIDC `sub` case-sensitive) lists `roles:`.
2. **Platform DB** — roles/assignments created via the UI/API, auditable. A DB role can't reuse a YAML role's name (returns 409); DB roles can't reference YAML roles.

Use the YAML half for the bootstrap admin + defaults; use the DB half for day-to-day, auditable management.

## Mapping IdP groups to roles: `idp_claims`

Set `user_to_role_provider: idp_claims` on the authorization service, then map a claim's values to roles:

```yaml
authorization_service:
  type: default_rbac
  user_to_role_provider: idp_claims
  idp_claims_config:
    claim_key: groups            # which OIDC claim to read
    mappings:                    # claim value → list of SAM roles
      analysts: [analyst]
      sam-admins: [admin]
```

This is how "users in the `analysts` IdP group get the `analyst` role." `idp_claims` is the supported claim-based provider; **there is no MS Graph provider** — don't offer one. (The IdP must emit the claim — configure that on the OIDC client; see [sso-oidc.md](sso-oidc.md).)

## Service accounts: the principals unattended work runs as

Not every request has a signed-in person behind it, and RBAC resolves against a **principal**, not a request. Machine entrypoints (webhook, event mesh, unauthenticated MCP, shared-channel Slack/Teams) and evaluation runs therefore authorize as a **service account** — a principal Agent Mesh owns, subject `system:<name>`. The `system:` prefix is reserved; an IdP must not issue it.

Service accounts were called *system users* before the rename. The old route (`/api/v2/platform/rbac/systemUsers`, now `/api/v2/platform/rbac/serviceAccounts`), response field (`isSystemUser`, now `isServiceAccount`), and config kind (`systemUser`, now `serviceAccount`) keep working until their removal date; use the new names. The `sam-docs` page `references/documentation/reference/deprecations.md` (System Users Renamed to Service Accounts) gives that date and maps each old name to its replacement.

Four service accounts are built in and need no configuration and no entry in the roles/users files. `default`, `eval` and `evidence` appear on the Users page as read-only service accounts; the deprecated `channel` is not listed there. Of the four, the entrypoint `run_as` picker offers only `default` (alongside your own service accounts), though `channel` and `eval` still work when named in config:

| Principal | Holds | Who runs as it |
|---|---|---|
| `system:default` | `agent:*:invoke`, `workflow:*:invoke` | a machine entrypoint, or a shared-channel Slack or Teams message, whose `run_as` is unset or `"default"` |
| `system:channel` | `agent:Orchestrator:invoke` | a Slack or Teams entrypoint still configured with the deprecated `run_as: "channel-user"`, which is migrated to it with a WARN |
| `system:eval` | `agent:*:invoke`, `workflow:*:invoke`, `tool:*:*` | an evaluation experiment whose `runAs` is unset |
| `system:evidence` | nothing | nobody: it is reserved for model usage Agent Mesh makes on its own behalf; naming it in `run_as` resolves to a principal with no scopes |

Four things to get right when advising:

- **The built-ins cannot be authored or granted to.** Declaring a `serviceAccount` under a built-in name is rejected by both `sam config plan` and the platform API, and so are deleting a built-in or granting it a role, because the embedded roles union with anything granted rather than being replaced — a grant could only widen the principal. To scope something down, declare a service account of your own and point at it.
- **Two spellings, same idea.** An entrypoint names its principal in `run_as` (bare name, snake_case key); an evaluation experiment names its own in `runAs` (camelCase key, on the `experiment` kind). Both accept a bare name and namespace it, so `reporting-evals` becomes `system:reporting-evals`.
- **Fail-closed.** A `runAs` naming a principal that holds no scopes fails the whole run with an error naming the principal — it does not silently fall back to a wildcard. Grant the role and re-run; the grant is picked up without a restart. Declare the `serviceAccount` in the same manifest as the experiment so `sam config apply` lands both.
- **`runAs` cannot name `system:default` or `system:channel`.** Tool credentials are keyed by principal, so an experiment naming one would share a credential with live traffic.

### Delegated tool credentials under a system principal

A service account never completes an interactive login of its own, but an operator can authorize a **delegated tool credential** on its behalf so unattended work reaches a tool that requires a user login. For evaluations this is the **Connect Tools** panel on the experiment detail page.

What an operator needs to know:

- Only OAuth 2.0 credentials are authorizable from a browser. Basic and bearer credentials come from the agent's own configuration and show as such.
- The credential is stored per agent, per principal, per credential key — so it is shared by every experiment running as that principal on that agent, and disconnecting it affects all of them.
- Disconnect is a local delete. It stops Agent Mesh presenting the credential; it revokes nothing at the remote system.
- The grant belongs to whoever clicked Authorize. Recommend authorizing with a shared account at the remote system rather than one person's, so the access does not leave with them.
- Prerequisites are the same as ordinary user-delegated tool access: SSO, a SQL session store, the Trust Manager, and the callback registered at the IdP.
- Scopes: reading the panel is `evaluation:*:read`; authorizing or disconnecting is `evaluation:*:invoke`; adding or removing an agent is `evaluation:*:create` / `evaluation:*:delete`.

These are **platform API** routes under the experiment, driven from the browser. There is no `/api/v1/control/...` HTTP surface at all (it was removed), so never offer a `sam api` recipe against it, for these or anything else.

## Worked shape: gate connector management to admins, give analysts a role

- Define an `admin` role with `*` (or scope it down to `connector:*:*` + what else admins need) and an `analyst` role with the tool/agent scopes that team needs.
- Map IdP groups to those roles via `idp_claims` (`groups` → `admin`/`analyst`), or assign directly in the users file / platform UI.
- For "only admins manage connectors," the gate is `connector:*:*` (or just `connector:*:create` + `connector:*:read|update|delete` to split create from manage) on the `admin` role; for "only analysts may invoke agent X," grant X's invoke scope on the `analyst` role and leave it off everyone else — `agent:<X's dashed UUID>:invoke` for a builder-created agent, `agent:X:invoke` for a YAML/built-in one (the three-row rule above).
- For "this role can run workflows," grant `workflow:*:invoke` (or per-UUID `workflow:<uuid>:invoke`) — plus whatever scopes the workflow's own steps require (below). A workflow invoke grant is **transitive to the agent hops the workflow's own DAG dispatches**: the boundary is authorized once at the workflow receiver and the DAG executor does not re-authorize each hop against the caller. Do **not** add `agent:*:invoke` to cover them; that grants direct invocation of every agent in the mesh that declares no `required_scopes`, which is a much wider grant than "can run workflows." Transitivity covers the **invoke** scope only, and three things still authorize against the propagated original caller. First, the target agent's own `required_scopes:` is evaluated on every inbound hop — a workflow node dispatching to an agent that declares `required_scopes` fails that node with `Access denied: insufficient scopes` (a JSON-RPC `-32600` surfacing as a node error, not an HTTP 403) unless the caller holds those scopes too. Second, a child agent doing its *own* peer delegation via `inter_agent_communication` needs the caller to hold `agent:<peer>:invoke` for those peers. Third, **tools are authorized at dispatch against the same forwarded caller** — so a workflow whose `type: tool` node or whose agents reach a scope-gated tool also needs that tool's scope. So `workflow:*:invoke` alone is enough only for workflows whose agents declare no `required_scopes` and whose steps touch no scope-gated tool; it is never enough for ones that do.

## Other notes

- **`mini_idp_mode: true`** (built-in Keycloak deployments) downgrades RBAC-management endpoints to read-only — group assignment happens in the IdP, not in Agent Mesh.
- **Enforcement points:** tool execution (a tool's `required_scopes`), agent/workflow invocation (the target's invoke scope — `agent:<dashed-uuid>:invoke` for a platform agent, `workflow:<dashed-uuid>:invoke` for a deployed workflow, `agent:<name>:invoke` for a built-in agent; direct user submission AND peer routing share it), control-plane and platform-API calls. Peer delegation propagates the **original caller's** identity, not the delegating agent's scopes (no scope laundering).
- **If RBAC isn't taking effect:** first confirm enforcement is on — on Helm, `sam.authorization.enabled: true` (when `false`, every user is admin). Then inspect the roles, grants and claim mappings with `sam config pull` (the `rbacRole`, `rbacGrant`, `rbacClaimMapping` and `serviceAccount` kinds; the Helm bootstrap `users[]` is not among them); fix them in the config repo and `sam config apply` (→ `sam-declarative-config`). The RBAC pages in the UI edit the same resources.
- There is a published **RBAC reference** page on the docs site (scope reference, authoring, two-sources model) — point operators there for the exhaustive scope list and YAML field detail. Full YAML authoring for the roles/users files: defer to that page / `sam-declarative-config`; name the keys here, don't hand-assemble large files from memory.
