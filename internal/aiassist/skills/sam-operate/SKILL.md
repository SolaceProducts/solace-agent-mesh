---
name: sam-operate
description: Use when operating a deployed Solace Agent Mesh instance — managing the runtime lifecycle of components (list, redeploy, undeploy, or remove a specific agent or workflow via the builder UI), turning on SSO/OIDC login for the entrypoint (Keycloak, Azure AD, Okta), configuring RBAC roles and scopes (including which users can use an agent or manage connectors), managing model spend (model pricing, budgets, budget assignments and additional temporary amounts), managing secrets and promoting config across dev/staging/prod, or upgrading Agent Mesh to a new version. Not for diagnosing why something failed or reading task event logs (sam-troubleshoot), authoring agents/tools/entrypoints/connectors (those skills), or first-time install (sam-install-run / sam-deploy).
---

# sam-operate

Operator configuration for a running Solace Agent Mesh: **SSO/OIDC, RBAC, model spend and budgets, secrets/promotion, upgrades.** This is a Go runtime — its auth is **native and in-process**, not the Python Agent Mesh model: there is **no separate `oauth2-proxy` service and no `oauth2_config.yaml`**. If you're about to reach for those, stop. Diagnosis ("X is broken/failing", reading task logs) is `sam-troubleshoot`, not here.

## Picking the area

| Operator task | Reference |
|---|---|
| List what's live / redeploy / undeploy / remove an agent or workflow instance | [Runtime lifecycle](#runtime-lifecycle) (below) |
| Log users in via an IdP (Keycloak/Azure/Okta), self-signed IdP certs | [references/sso-oidc.md](references/sso-oidc.md) |
| Roles & scopes — gate an agent, gate connector management, map IdP groups to roles | [references/rbac.md](references/rbac.md) |
| Model spend — price models, set budgets, assign users, grant an additional temporary amount, spend shows $0 | [references/budgets.md](references/budgets.md) |
| Secrets, `${VAR}` substitution, dev→staging→prod promotion, rotation | [references/secrets-and-upgrades.md](references/secrets-and-upgrades.md) |
| Upgrade Agent Mesh, migrations, rollback | [references/secrets-and-upgrades.md](references/secrets-and-upgrades.md) |

## Runtime lifecycle

Manage the components (agents, workflows, other AWE-hosted instances) **currently running** in a deployed mesh, via the broker-routed control plane. This is the imperative "act on what's live right now" surface — distinct from `sam config apply`, which changes the *declared* set of resources and lets the reconciler converge. To bounce one misbehaving agent without touching the others, redeploy it here.

The **builder UI** is the operator surface for live lifecycle, and its action vocabulary is **deploy, update, undeploy, and delete** — there is no stop/start-in-place anywhere in the product. `sam control` was removed, and so was the `/api/v1/control` HTTP API that briefly succeeded it — never offer a `sam api` recipe against `/api/v1/control/...`, and never offer a "stop" or "start" action; the verbs do not exist. For scripted **status** checks (not lifecycle actions), `sam api /api/v2/platform/agents` and `/api/v2/platform/workflows` list platform-managed components with a `runtimeStatus` **object** — filter on `.runtimeStatus.status` (`running` / `degraded` / `starting` / `disconnected` / `stopped`), never on the field itself; `degraded` means serving but reporting problems, listed in `.runtimeStatus.problems`. Statically configured (YAML) agents appear only as live cards in `GET /api/v1/agentCards`.

"Restart just one agent" = **redeploy it** from the builder UI. **Undeploy is permanent removal of the running instance** (its durable broker queues are deprovisioned), not a "stop" — the agent's definition stays in the platform and can be deployed again. An instance that failed to *initialize* has nothing running to bounce: redeploy it, and for a statically configured instance fix the YAML and restart the pod.

## What lives where (ownership)

- **Entrypoint runtime YAML** owns the OIDC `providers:` catalog and the `authorization_service` (RBAC). These are **not** managed by `sam config apply` and have no builder-UI control — the entrypoint/platform YAML is the real surface, so this skill shows those verified shapes directly.
- **Platform DB** holds the second half of RBAC (roles/assignments created via UI/API) — the "two sources of truth" model alongside the YAML roles.
- **Secrets** are never YAML literals — only `${VAR}` references or `*_file` mounts, sourced from env / .env / Kubernetes Secrets.
- **Resource config** (agents, connectors, toolsets, entrypoints) is `sam-declarative-config` + the builder UI — not this skill.

## Hard rules (each counters an observed failure)

- **Native OIDC, not a proxy.** The Agent Mesh entrypoint does OIDC in-process via a top-level `providers:` catalog. Never describe an `oauth2-proxy` sidecar or `oauth2_config.yaml` — that's Python Agent Mesh and it will mislead.
- **Use the real `authorization_service.type` values:** `none` (all authenticated users get all scopes — dev only), `default_rbac` (YAML + platform DB), `deny_all` (no scopes granted). When the block is omitted an entrypoint defaults to `none` unless `frontend_use_authorization: true` (then `deny_all`); the platform defaults to `deny_all`. Set `type` explicitly rather than rely on the default. Baselines invented `type: default` — that's wrong.
- **Scopes are verified strings, not guesses.** Real (3-segment `<category>:<resource>:<verb>`): `*` and `*:*:*` (the two spellings of the superadmin grant — `*:*:*` is canonical), `tool:*:*`, `agent:<name>:invoke`, `connector:*:create` / `connector:*:read`, `rbac:*:read`, `agent_builder:*:*`, `agent:*:read_feedback` / `workflow:*:read_feedback` / `agent:*:read_trace` (the per-kind read grants for the Feedback Dashboard, task traces and the Activity Monitor's feedback answers — narrower than `activity:*:read`, see [rbac.md](references/rbac.md)), `budget:*:read` / `budget:*:assign` and `*:*:read_usage` (budgets and model spend, see [budgets.md](references/budgets.md)). **`agent:<name>:invoke` gates BOTH direct user invocation through the entrypoint AND peer routing (one agent delegating to another)** — same scope, both call paths. RBAC *writes* need the superadmin grant, not a scope: `rbac:*:read` (listed in the catalog as `rbac:_:read`, its older spelling) is the only RBAC scope in the catalog, so never offer a `create`/`update`/`delete` RBAC scope as a way to delegate RBAC administration — no such scope exists ([rbac.md](references/rbac.md)). If you're unsure a scope exists, say so rather than emit a plausible invention — the failure mode is a wildcard-shaped guess for a category that has no such grant, or an agent scope built from the wrong segment 2. The full catalog ships with the entrypoint; defer to the RBAC reference doc if you need the exhaustive list.
- **Role providers:** YAML files (`role_to_scope_definitions_path`, `user_to_role_assignments_path`) plus the optional **`idp_claims`** provider mapping an OIDC claim (e.g. `groups`) to roles. `idp_claims` is the supported claim-based provider — **there is no MS Graph provider**; don't offer one.
- **Secrets never inline, never echoed.** `${VAR}` / `*_file` only. A secret pasted into chat is exposed — say so once, advise rotation, reference it as `${VAR}` thereafter. **Rotating `SESSION_SECRET_KEY` logs every user out** — flag that before suggesting it.
- **Don't write resource YAML from memory.** For agent/connector/toolset/entrypoint YAML, defer to `sam-declarative-config`. The auth/secrets shapes in this skill's references are the verified operator surface and are fine to show; resource definitions are not.
- **No edition split, no `pip`.** Auth/RBAC are features you configure, never a different build. Upgrades are image-tag rolls (Helm) + auto-applied migrations, not `pip install`.
- **Live lifecycle is this skill — not config-apply, not troubleshoot.** Redeploying/undeploying/removing a *running* instance goes through the builder UI (this skill). Don't push it to `sam-declarative-config` (that changes the declared set, not the live one) or to `sam-troubleshoot` (that's diagnosing *why* something failed, not acting on it). Diagnose with troubleshoot, then act via the UI. Never guess a `sam agent …` / `sam control …` / `kubectl …` command for this, and never offer a `sam api` recipe against `/api/v1/control/...` — `sam control` and the `/api/v1/control` HTTP API were both removed; the live surface is the builder UI, and its verbs are deploy/update/undeploy/delete (no stop/start).


- **Turning telemetry off (or on).** Free downloads (`-desktop-free-` desktop bundles) collect product analytics, on by default; Enterprise downloads, the standalone `sam` CLI and the Helm/container images include none and have no `sam analytics` command. On a free download, `sam analytics status | enable | disable` manages the per-installation decision, and each prints it; `status` reads it without changing anything. Point `--database-url`, or `SAM_ANALYTICS_DATABASE_URL` when it carries a password, at the deployment's `session_service.database_url` so you read or change the record the running deployment actually reads. Details in `sam-install-run`.

## References

| Topic | File |
|---|---|
| SSO/OIDC — providers catalog, fields, provider selection, self-signed certs, session secret | [references/sso-oidc.md](references/sso-oidc.md) |
| RBAC — authorization service types, scope syntax + real scopes, role providers, two sources of truth | [references/rbac.md](references/rbac.md) |
| Model spend — pricing, unpriced-is-not-free, budget scopes and period, default vs pinned users, additional temporary amounts, RBAC scopes | [references/budgets.md](references/budgets.md) |
| Secrets/env promotion + upgrades — substitution forms, file mounts, rotation, upgrade essentials and where the runbook lives | [references/secrets-and-upgrades.md](references/secrets-and-upgrades.md) |
