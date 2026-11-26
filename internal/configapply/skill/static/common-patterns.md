## Common Patterns

**Variable substitution.** Any string field in a per-resource YAML file
— and the values inside the manifest's `variables:` block, but no other
manifest field — can reference a variable with `${VAR}` or
`${VAR, default}`. In a per-resource file the resolution order is the
manifest's `variables:` block, then the process environment; the
`default` form is used when neither is set, and without a default an
unresolved variable is a plan-time error. Inside `variables:` values
only the process environment is consulted, and an unset `${VAR}` there
becomes an empty string rather than an error.

**`!include`.** In the manifest, a line holding only `!include path/to/file`
(indented under the key it fills) is replaced by that file's content,
resolved relative to the manifest. The directive must sit on its own line;
`key: !include file` on one line is not expanded. Only the manifest expands
`!include`. A resource file (agent, workflow, toolset, SKILL.md frontmatter,
and so on) that carries an `!include` tag fails `sam config plan` with an
error naming the file and line, so inline the content or use `${VAR}`.

**Source URLs.** The manifest's `sources:` block accepts pip-style URLs:
`git+https://...@<ref>` or `git+ssh://...@<ref>` (with optional
`#subdirectory=<path>`), `git+file:///abs/path` for a local git
repository, and `file:///abs/path` for a local checkout. Any other
scheme, including plain `https://`, fails with `unsupported scheme`. A pinned ref (tag, full SHA) is cached forever; a floating
ref (branch, short SHA, HEAD) requires `--allow-floating-refs` and
re-fetches every run.

**Resource imports.** A resource entry in the manifest can be a bare
local name (`my-agent`), a `name@source` import
(`research-agent@ai-team-agents`), or an aliased import
(`{from: research-agent@ai-team-agents, as: team-research}`). Imported
resources are fetched from the source repo's matching `<plural>/`
directory.

**Naming.** Resource names reject control characters; beyond that the
length limits vary by kind. Agent, connector, and entrypoint names are
3-255 chars; model aliases 1-100. The schema for
each kind documents the live constraints.

**Secret placeholders.** Pull-side serialisation rewrites
known-credential fields into `${KIND_NAME_FIELD}` placeholders so
the YAML is safe to commit. Apply-side substitutes them back from env
vars; an unresolved placeholder is an apply-time error rather than a
silent leak.

**Running plan and apply in CI.** `sam config plan` and `sam config
apply --dry-run` exit `0` on success and `1` on error. Add
`--detailed-exit-code` to gate on drift: it exits `2` when the plan
succeeds with changes pending (Terraform's `-detailed-exitcode`), and a
real apply rejects it. Deletes count as pending only under `--prune`,
which `plan` also accepts and which deletes nothing there. A pipeline
that applies with `--prune --force` must plan with `--prune` too, or the
plan for a pull request that only removes resources marks each removal
`needs --prune` while the apply deletes it. An unchanged agent, link agent,
workflow or entrypoint counts too when apply would deploy it: its deployment
is out of sync, its `deploymentStatus` is `not_deployed` (an earlier
`apply --no-deploy` created it, or someone undeployed it; the platform
cannot tell these apart), or it is a workflow or link agent whose last deploy
failed. To
keep a declared resource stopped, drop it from the manifest or apply with
`--no-deploy`. Its
plan row adds `(last deploy failed)` when the previous deploy failed; a
deploy that fails every time keeps plan exiting `2` and apply `1` until
the cause is fixed, so re-running apply will not clear it. RBAC changes do
not count when the platform blocks RBAC writes: apply skips them, and
plan lists them with a warning. `--format json`
prints one document on stdout: `changes` (`kind`, `name`, `action`,
`source`), `summary` counts, and `diagnostics`; apply adds `results`
(empty under `--dry-run`). `action` is `create`, `update`, `delete`, `redeploy` or
`unchanged`; a `results` `status` is `created`, `updated`, `deleted`,
`deployed`, `skipped` or `failed`. `summary` counts every listed change, so use `--detailed-exit-code`, not
`summary`, to decide whether changes are pending. `changes` and `summary` carry no
field values; a failed result's `error`, and the error entry in
`diagnostics` when a run fails, can include the platform's response text. A run
that fails still prints the document, with the error as the last
`diagnostics` entry (`level: error`) and empty `changes` if it failed
before the plan finished; an apply whose operations fail also lists each
failure in `results`. An apply refused before it starts (`--prune`
without `--force` when non-interactive) prints only the stderr error. Plan text marks each delete `(needs --prune)`
unless the run prunes. A run
is non-interactive when `CI` is set to a truthy value, stdin or stderr
is not a terminal, or `--no-interactive` is passed. Nothing prompts, no
browser login is offered, and `apply --prune` needs `--force` unless it
is a dry run. Variables already set in the environment win over the
auto-loaded `.env`, while `--env-file` overrides both. `NO_COLOR` or
`--no-color` strips color. Free downloads (`-desktop-free-` desktop
bundles) collect product analytics, on by default; Enterprise downloads,
the standalone `sam` CLI and the container images collect none. On a
free download, set `DO_NOT_TRACK=1` to keep that run's `sam` commands
out of it. For a full CI pipeline (plan on PR,
apply and eval on merge, API-key setup), read
`references/ci-pipeline.md`.

**Plan with the token apply uses.** The platform never returns a stored
secret, so when a manifest sets one (a blank string counts) on a
resource that otherwise matches, plan asks the platform whether the
value changed. That compare needs the kind's update scope
(`connector:*:update`, `entrypoint:*:update`, `toolset:*:update`,
`model_config:*:update`, or `agent_builder:*:update` for agent
overlays). A token without it fails the plan with an error naming the
resource, the field and the scope. Any other failed compare also fails
the plan.
Leaving a secret key out keeps the stored value and needs no compare, so
give a pipeline's plan step the same token as its apply step.

**Pull writes everything or nothing.** `sam config pull` fails, and
writes nothing, when it cannot export a resource completely. That covers
a toolset whose config or bundle download fails or whose bundle is
empty, a connector spec it cannot download, an RBAC row it cannot write
(such as a grant naming a role it cannot resolve), a skill or toolset
name that is not a safe directory name, two resources that map to one
file ignoring case (two budgets or claim mappings get a numeric suffix
instead), and a platform list or default-roles read that errors. Any
other name with `/`, `\` or a leading dot gets a mapped file name (`Sales/EMEA` is written to
`Sales_EMEA.yaml`); its `name:` keeps the original. It only warns about
what a repo cannot hold: kinds whose platform feature is disabled,
built-in roles, dataset example attachments, secret placeholders, a
toolset or skill with no bundle uploaded, a toolset whose discovery
is not `ready` (its config is not pulled), a custom service account it
cannot manage or DB grants widening a built-in one, and a skill whose bundle
fails to download or unpack (it stays in the manifest and any local
`skills/<name>/` is kept; see the skill reference's Pull flow).
