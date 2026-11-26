## Common Mistakes

- **Assuming the manifest is found.** Apply / plan / migrate read
  `--manifest path/to/manifest.yaml`, falling back to `./manifest.yaml`
  (or `./manifest.yml`) in the current directory. Pass the flag
  explicitly whenever you are not in the repo root, or you will act on
  the wrong manifest or none at all. Pull is the exception (it builds a
  manifest from platform state).
- **Casing mismatch.** A resource's `spec:` field names are camelCase
  (e.g. `modelName`, `systemPrompt`, `apiBase`) — the same names the
  platform's REST API uses — and a camelCase field misspelled in
  snake_case is silently ignored. That rule covers the `spec:` layer
  only. Several nested bodies are snake_case by design: connector
  `values:` (`require_approval`, `require_approval_when`,
  `tool_name_prefix`), workflow node bodies, and per-type entrypoint
  `values:` such as the `event_mesh` type token. Follow the per-kind
  reference for those rather than "correcting" them to camelCase —
  renaming a working `require_approval` silently disables the gate.
- **Hard-coding secrets.** Don't commit auth tokens or API keys.
  Name the token's env var in the manifest's `auth.envVar`, reference
  secrets in resource files via `${VAR}` or a `vault://path#field`
  reference (`references/manifest.md`), or rely on the pull-side
  `${KIND_NAME_FIELD}` placeholder pattern.
- **Floating refs in production.** Sources without a pinned ref
  (branch, short SHA, HEAD) require `--allow-floating-refs` and make
  apply non-reproducible. Pin to a tag or full SHA.
- **Panicking at `plan` deletes on a partial manifest.** `plan` always
  diffs the *whole* platform against the manifest, so a focused manifest
  that lists only one resource shows every other resource as
  `- delete (needs --prune)`.
  That is display-only: `apply` performs creates/updates but **skips
  deletes unless you pass `--prune`** (you'll see `skipped (use --prune
  to delete)`). This is exactly what makes selective single-resource
  manifests safe — apply without `--prune` and the rest of the platform
  is untouched. Only reach for `--prune` with a full manifest that is
  genuinely the complete desired state.
- **Reading `kept (a create in this apply resolved to it)` as a failed
  delete.** The user started signing in with the email the role file
  declares after `plan` ran, so plan showed a create beside a delete of
  the grant they already hold. Apply found the create resolved to that
  grant and kept it. The user still holds the role; the next `plan`
  shows an update that re-records the grant under the declared subject.
- **Omitting `systemPrompt` on a standard agent.** The platform enforces
  it at create/update time: a
  `standard` agent (the only supported type) without a system prompt is
  rejected with HTTP 422 `system prompt is required for standard agents`.
  Always set `spec.systemPrompt`; its length is bounded by the platform's `agent_instruction_min_chars` and `agent_instruction_max_chars` (1 and 32000 by default).
- **Flat agent YAML instead of `spec:`.** Agent fields (`systemPrompt`,
  `toolsets`, …) live under a top-level `spec:` block; only `kind:`,
  `name:`, and `description:` sit at the root. The flat form passes `plan`
  silently but drops the fields at `apply`, surfacing as a misleading 422
  (e.g. a missing `systemPrompt`). Use the `spec:` shape from
  `references/agent.md` (it matches `sam config pull` output).
- **Putting `tools` in `additionalConfigurations`.** `additionalConfigurations`
  is a catch-all for un-modelled config keys, **not** for tools. The platform
  computes the deployed `tools:` block from `spec.toolsets` and rejects
  `additionalConfigurations.tools` with HTTP 422. List built-in toolset IDs
  (`builtin_artifact_tools`, `builtin_web_request_tools`, …) under
  `spec.toolsets` instead — see the ID table in `references/toolset.md`. The
  runtime AWE `tools:` / `group_name` shape never goes in the declarative API.
- **Skill files vs directories.** A `skills:` resource entry points
  at a directory under `skills/<name>/`, not a YAML file. The
  directory must contain a SKILL.md.
- **Mixing `kind` values.** Each per-resource YAML file declares its
  own `kind:` (e.g. `kind: agent`); the manifest's `kind: manifest`
  is unrelated. Setting `kind: agent` inside a manifest is a parse
  error.
- **Trying to mutate immutable fields.** An entrypoint's `type` is
  immutable after creation: `plan` fails with `type change (...) is not
  supported on update`. Delete the entrypoint and apply it again to
  switch type. Agents accept only `type: standard`, so there is no
  agent type to change.
- **Tool param missing both optional signals.** A Go toolset param
  field is optional if it is a pointer type (`*string`, `*int`) OR
  carries `json:",omitempty"`; otherwise it is required. Authors
  who omit both end up with every field in the JSON schema's
  `required` list — the STR rejects any call that omits one with
  "mandatory input parameters are not present". When in doubt,
  pick one signal per field. See `references/tool-build.md`
  "Authoring pitfalls".
- **Re-uploading an in-use toolset.** Re-uploading a toolset that
  deployed agents reference overwrites the package and auto-redeploys
  those agents (async, via the outbox publisher). `sam config apply`
  re-uploads only when the bundle's SHA-256 differs from the
  platform-stored content hash, so any content change re-uploads and an
  unchanged bundle is a true no-op.
- **Stale build-cache poison.** `[BUILD: cache-hit <os>/<arch>]`
  short-circuits the build and re-uploads whatever zip is cached for
  that target. Cross-target poisoning is now prevented by segmenting
  the cache by `<os>-<arch>/` and mixing the target into the hash, so
  a darwin/arm64 zip can no longer satisfy a linux/arm64 apply, and a
  CLI upgrade that changes the bundle format invalidates it too. A
  once-bad zip from a stale SDK or missing tool registrations still
  stays bad until you `rm -rf toolsets/<name>/.sam-cache/build/` and
  re-apply. Confirm by inspecting the cached binary:
  `unzip -p toolsets/<name>/.sam-cache/build/<os>-<arch>/*.zip <name> | file -`.
