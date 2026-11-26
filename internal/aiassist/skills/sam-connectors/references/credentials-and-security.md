# Connector credentials & security

## The shared-credential model (lead with this)

One connector = one set of credentials = **identical access for every agent attached to it**. Agent Mesh cannot restrict which queries or calls an agent makes through a connector — access control lives at the external system:

- SQL: a dedicated **read-only** DB user, granted only the schemas/tables the agents should see.
- API keys: scoped to the operations needed; use `allow_list` to narrow the exposed operations besides.
- Slack: minimal bot scopes (`chat:write` and only what's needed); the bot posts as the same identity for every agent.
- AWS types (Bedrock, DynamoDB, Neptune, OpenSearch): a least-privilege IAM policy scoped to the one resource the connector reads; prefer `iam` role chaining over long-lived access keys when Agent Mesh runs on AWS, with `external_id` for cross-account.

If two groups of agents need different access levels, create **two connectors** with different credentials — that is the supported isolation mechanism.

## Secrets hygiene in the session

- A credential pasted into chat is **exposed**: say so once, advise rotating it after setup, and never repeat the literal value — not in YAML, not in an `export` line, not in a recap. Identifying *which* credential by a short redacted prefix (`ak_live_…`) is fine; the full value never reappears. Refer to it as `${VAR}` from then on.
- Secret-typed fields (passwords, tokens, keys) are write-only in practice: the UI masks them after save, and `sam config pull` exports them as `${VAR}` environment-variable placeholders. Values are supplied via the environment at apply time.
- Never invent a secrets-handling mechanism (k8s secret refs, other secret-manager syntaxes) the product doesn't have. The supported patterns are `${VAR}` substitution (exact rules: `sam-operate`, secrets reference) and, in `sam config` resource files and a skill's `tools:` block, a `vault://path#field` reference to HashiCorp Vault (rules: `sam-declarative-config`).

## Who can manage connectors

Connector CRUD is RBAC-gated (`connector:*:create` to create; `connector:*:read`/`:update`/`:delete` to manage — per-instance `connector:<name>:…` is accepted by the matcher but not enforced; `connector:*:*` is a valid wildcard grant) — if a user can't see or create connectors in the UI, that's a role/scope question for `sam-operate`, not a missing feature. Connector tools carry no per-tool scopes of their own: whoever can invoke the agent can use every tool its connectors provide, so callers are gated by the agent's invoke scope.

## Change blast radius

Updating or deleting a connector **redeploys every attached agent**. Uploading a changed `api`/`openapi` spec file redeploys the attached agents and workflows too, so they pick up the new spec; re-uploading an unchanged file does not redeploy. Before editing shared credentials in production: check the connector's agent count (shown in the UI), schedule accordingly, and prefer creating a second connector + migrating agents when the change is risky.
