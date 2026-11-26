# Secrets, env promotion, and upgrades

## Secret substitution

The config loader expands placeholders on raw YAML **before parsing**. Forms:

| Form | Behavior |
|---|---|
| `${VAR}` | value if set (even empty); else empty. Bare refs can be enforced as "must be set". |
| `${VAR, default}` | value if set; else the default. |
| `${VAR:-default}` | value if set **and non-empty**; else default. |
| `${VAR:+alt}` | `alt` if set and non-empty; else empty. |

Nesting is depth-1 (`${A, ${B, x}}` resolves; deeper does not). Secrets are **never** literals in YAML: use a `${VAR}` reference, or — on model configs only — a `*_file` mount (see below; there is no universal `*_file` sibling). Runtime YAML does not resolve `vault://` references — those are a `sam config` and skill `tools:` feature (`sam-declarative-config`); to feed a runtime process from Vault, populate its environment or a mounted file (for example with a Vault Agent sidecar).

## File-mounted secrets

The `*_file` convention is a **model/LLM-config credential** feature — not a universal loader rule. On a model config, a credential field can be supplied from a mounted file: `api_key_file`, `oauth_client_secret_file`, Vertex `auth_credentials_file`. (Separately, the GCS artifact store takes a `credentials_file` — a path handed to the cloud SDK, not a model-config key.) Use these with Kubernetes Secret mounts. Elsewhere — broker password, OIDC `client_secret`, the session DB URL — there is no `_file` sibling; use `${VAR}` references instead.

## `!include`

YAML supports `!include path/to/file` for modular config (relative to the config base dir; circular-include and recursion-depth guarded). Useful for keeping an auth or secrets fragment separate.

## Common secret-bearing surfaces

Broker password (`SOLACE_BROKER_PASSWORD` / `broker.broker_password`); LLM key (`ANTHROPIC_API_KEY`/`OPENAI_API_KEY`/… / `model.api_key`); cloud storage creds (AWS/GCS/Azure on `artifact_service.*`); DB password (in `session_service.database_url`); `SESSION_SECRET_KEY` (entrypoint cookie signing); OIDC `client_secret`; Slack `SLACK_APP_TOKEN`/`SLACK_BOT_TOKEN`. Per-connector/per-toolset credentials live in that resource's config (owned by `sam-connectors` / `sam-tools-and-skills`), not here.

## Promotion dev → staging → prod

Same YAML everywhere; the environment supplies the secrets. Keep one set of config files and per-environment `.env` / Kubernetes Secrets:

```
# dev .env            # prod = Kubernetes Secret / Vault
SOLACE_BROKER_PASSWORD=...   ANTHROPIC_API_KEY=...
```

`${VAR}` references make the files environment-agnostic — no edits between stages.

## Rotation (no/low downtime)

- **Broker password:** rotate at the broker first; live sessions stay on old creds, reconnects use the new value.
- **LLM key:** issue new → deploy → revoke old (overlap window).
- **OIDC client secret:** rotate at the IdP, update the env var, restart the entrypoint. In-flight logins fail during restart; existing signed sessions survive (they're signed with `SESSION_SECRET_KEY`, not the IdP secret).
- **`SESSION_SECRET_KEY`:** changing it **invalidates every active session** — all users are forced to log in again. Flag this before recommending it.
- **DB password:** coordinate with active connections; rolling restart of consumers.

The docs site has a published **secrets-management** page with the full surface table and per-deployment rotation procedures — point operators there.

---

## Upgrades

### What changes
Three surfaces: **binary version** (new image tag per workload), **database schema** (embedded migrations auto-applied on pod boot), **YAML config keys** (renames/removals/value restrictions need operator edits). The A2A wire protocol is forward-compatible within a major version; YAML and DB schema are not.

### What to know before advising
- **Read the release notes first** — they call out YAML renames and behavior changes that need edits before the new version starts.
- **Back up the databases before upgrading.** Migrations run automatically when each workload starts and only go forward, so rolling back means restoring the pre-upgrade backup along with the previous image.
- **Pin the image tag or digest**, never a floating tag, so a rollback lands on a known version.
- **A failed migration stops that workload from starting** rather than degrading quietly. The Helm pre-upgrade `sam-doctor` hook catches broken prerequisites (broker, database, LLM, TLS, OIDC) before any pod rolls.
- **`sam config migrate` is unrelated** — it converts legacy YAML files to the current format and never touches a database.
- **A migration waits at most 15 s for a table lock.** If another session holds the lock longer (an open transaction, a long report query), the migration fails with a lock timeout and the pod restarts, instead of hanging until the startup probe kills it. While it waits, the pod logs `migration still running` at WARN every 10 s; `blockedBy` names each session holding the lock (pid, role, application name, state, transaction age), so the operator knows what to end or wait out.

### Index builds after startup (Postgres)
Indexes on tables that grow with every task (for example `task_events`) are not built during migrations, because a large build can outlast the startup probe. A migration queues them instead, and the gateway pod that leads builds them in the background with `CREATE INDEX CONCURRENTLY` once it is serving, checking the queue again every ten minutes. Only one gateway pod leads at a time; it logs `leader term started` when it takes over. The queue is the `gateway_async_indexes` table. The evidence store queues its indexes in `evidence.evidence_async_indexes`, and the pod that leads the platform builds those: the leading gateway pod when the platform runs inside the gateway, or the leading platform pod when the platform runs on its own. Reads and writes keep working during a build, but the first build after an upgrade on a large database can keep the database disk busy for tens of minutes, so upgrade when traffic is low.
- **Watch progress** in the gateway logs: `async index operation in progress` every minute with the build phase and block counts, then a row's `completed_at` is set.
- **Failures retry on their own** on a later ten-minute pass, after a backoff of up to one hour recorded in `attempts`, `last_error` and `next_attempt_at`. After 10 failures the pod logs `async index build gave up after repeated failures` at ERROR and skips that row; a pod that later takes over as leader logs `async index builds gave up and are skipped until attempts is reset` at WARN instead, naming the rows. Fix the cause from `last_error` and set `attempts = 0, next_attempt_at = NULL` on the row; the leading gateway pod builds it on its next pass, within ten minutes, with no restart.
- **To stop a running build**, connect as the role Agent Mesh uses and run `SELECT pg_cancel_backend(q.builder_pid) FROM gateway_async_indexes q JOIN pg_stat_activity a ON a.pid = q.builder_pid WHERE q.name = '<index>' AND q.completed_at IS NULL AND a.state = 'active' AND a.usename = current_user AND a.query ILIKE '%concurrently%' AND a.query ILIKE '%' || q.name || '%'`. The checks matter: `builder_pid` alone can point at a session that has moved on to another row or reused the pid. The gateway drops the half-built index and builds it again on its next pass; a cancel does not count toward the 10 failures. Stopping or restarting the gateway pod does not stop a build that is already running in Postgres; the next gateway finishes the row.
- **A later migration that needs a table under a running build** cancels the build (`cancelled a running async index build so a migration can take its lock`), or, if the build is in its last phase, Postgres aborts one of the two as a deadlock: a build aborted that way starts over, and a migration aborted that way is retried once (`migration aborted by a deadlock; retrying it once`). The migration then applies and a cancelled build starts over; neither a cancel nor a deadlock abort counts toward the build's 10 failures. Until the cancel, up to the 15 s lock timeout, reads and writes on that table wait behind the migration, including those of a gateway still serving during a rolling update.
- **Before rolling the gateway back**, check that every row in `gateway_async_indexes` has `completed_at` set. An older release may expect an index the queue has not built yet.
- **Connection pooling:** the builder and the migration lock need session-level connections, so a transaction-pooling PgBouncer in front of the database is not supported.

For the step-by-step procedure — staging dry-run, roll order, health checks between steps, rollback — point to the Agent Mesh docs: the **Rollback and Upgrade** section of the production readiness checklist and **Running a Pre-Upgrade Dry-Run in Staging** under operator workflows. After an upgrade, confirm agents and workflows report running — in the UI or with `sam api /api/v2/platform/agents` — because a healthy pod does not prove every agent on it started.

### Checking the deployed version
`sam --version` covers the CLI. The long-running binaries each accept a `-version` (also `--version`) flag that prints the build version and exits — handy for confirming what a pod is actually running, alongside the deployed image tag/digest.
