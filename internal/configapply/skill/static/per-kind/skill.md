A skill is a reusable instruction-and-asset bundle that agents can
attach via `skillRefs` (clean-spec authoring) or `skillIds` (the wire
shape; the apply layer resolves names to platform UUIDs).

Skills are *directory-shaped* resources. The canonical metadata lives
in `SKILL.md` frontmatter — the same convention as
[agentskills.io](https://agentskills.io). There is **no** separate
`skills/<name>.yaml` header file; the bundle directory IS the resource.

## Directory layout

```
skills/
  <name>/
    SKILL.md           required: YAML frontmatter holds name/description
    references/        optional: per-topic detail markdown
    assets/            optional: report templates, images, helper scripts
```

Files under `assets/` can also be **templates**: a single packaged `.samt`
file ships a report/document template the agent fills and renders, with no
generation code. The `.samt` is produced by the `package_template` tool and
carries its contract internally; copy it into `assets/` as-is. See
`skill-asset-templates.md` for the layout and where the contract is
documented.

The manifest entry under `resources.skills:` names the skill; the
matching directory under `skills/<name>/` carries the body. The
frontmatter `name` MUST match both the directory name and the manifest
entry, or `sam config plan` hard-errors.

## SKILL.md frontmatter

```markdown
---
name: support
description: Customer-support skill — escalation paths and tone guide.
tags: [support, customer-facing]
---

# Support

Body content follows the frontmatter. `name` and `description` are
required; other fields (`tags`, `license`, `tools`, `required_builtins`)
follow the agentskills.io spec and are consumed by the platform on
upload.
```

Renaming a skill means renaming both the directory and the
frontmatter `name` together — the apply layer treats a name change as
delete + create, and any agent's `skillRefs` referencing the old name
will surface a plan-time dangling-reference error until updated.

## Apply flow

1. The resolver reads `skills/<name>/SKILL.md`, parses its
   frontmatter, and extracts the description.
2. The reconciler bundles `skills/<name>/` into a deterministic ZIP
   (sorted entries, fixed mtime — same machinery as toolsets) and
   uploads it to the platform (multipart; description as a form
   field). Re-uploads against an existing skill reuse the same upload endpoint.
3. The platform validates the ZIP server-side: SKILL.md must be at the
   root, and its frontmatter `name` becomes the persisted skill name.
4. Before it writes any agents, apply waits for every skill it uploaded
   to reach `discoveryStatus = ready`, up to 30 seconds in all. Until the
   Secure Tool Runtime has scanned a bundle, the platform rejects an
   agent's `skillConfigs` for that skill's bundled tools. A skill that
   ends `failed`, is still `pending` when the wait runs out, or whose
   status apply cannot read, is reported as a failed skill, and apply
   does not write the agents whose `skillRefs` name it. The platform log
   records why a scan failed. An older platform that refuses to report a
   skill's status to an API key (HTTP 403) gets no wait, and apply prints
   a warning saying so.

Re-uploads are hash-diffed: the reconciler compares the local bundle's
SHA-256 against the platform's stored content hash (`contentHash`)
and skips the upload when they match, so an unchanged skill plans as a
no-op instead of bouncing through pending→ready and redeploying every
bound agent. A description-only edit PATCHes metadata but skips the
upload. A skill stuck in `discoveryStatus: failed` always re-uploads to
retrigger the Secure Tool Runtime scan.

## Pull flow

`sam config pull` downloads each skill's ZIP and unpacks it under
`skills/<name>/`. The platform redacts the values in a skill's `tools:`
entries that it recognises as credentials, and they come back as
`<REDACTED>`: values under secret-named keys (`api_key`, `token`,
`password`), values in `auth`, `headers` and environment-variable maps
other than known-safe settings, and credential-shaped values such as a
`Bearer` token, a JWT, a private key, or a URL that embeds a password or
token. A value it does not recognise is returned as stored, even one
apply resolved from a `${VAR}` or `vault://` reference, so put
credentials in `auth`, `headers` or `environment_variables`, or under a
key that names them, and write them as references there. Pull rewrites
each `<REDACTED>`, in every SKILL.md in the bundle, as a
`${SKILL_<SKILL>_<TOOL>_<FIELD>}` placeholder and lists it with the
other secret placeholders; set those variables before `sam config apply`.
Everything else in SKILL.md is written as stored.

When a skill's download or unpack fails and the output directory already
holds `skills/<name>/`, pull leaves that directory unchanged (also under
`--force`), keeps the skill in the manifest and prints a warning. A skill the
platform has no uploaded bundle for yet is pulled as a synthesized SKILL.md
built from its stored name/description, so you can author the bundle
locally.

Any other failure with no local copy (a server error, an unreadable bundle, or
the platform refusing to export a SKILL.md whose frontmatter uses YAML
anchors, aliases or merge keys, or has no closing `---` line, because it
cannot redact it safely) writes no files for that skill and warns. The
skill stays in the manifest, so plan and apply fail for it until you add
`skills/<name>/` or remove it from the manifest; removing it lets `--prune`
delete the skill from the platform.

A `<REDACTED>` value applied back unchanged keeps the stored value, but
only when the rest of that tool entry is identical to what is stored and
every secret in the entry is still `<REDACTED>`. Change anything else in
the entry (an endpoint, a header, an environment variable), or supply one
of its secrets as a real value, and apply is rejected until you leave all
of the tool's secrets as `<REDACTED>` or supply all of them as real values
or `${VAR}` references.
