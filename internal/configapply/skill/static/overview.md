## Overview

An Agent Mesh declarative-config repository captures the desired state of an Agent Mesh
platform as plain YAML. One **manifest** document lists which resources
to apply; per-kind YAML files (one per resource) live under
well-known top-level directories; non-config artefacts (skills, tool
binaries) live in their own directories.

`sam config plan` shows the diff between the manifest and the running
platform; `sam config apply` reconciles the platform to match. The same
files round-trip through `sam config pull`, which serialises a running
platform back into a fresh repo.

**Apply targets (`--url`, or the manifest's `target.url`).** The same
plan/apply/pull commands work against any running platform:
- A deployed platform — `--url https://platform.example.com`.
- The **desktop app** — it runs a full platform whose admin API is fronted by
  the entrypoint proxy (`http://127.0.0.1:8800` by default; if that port is taken
  it falls back to a free one, so use the instance's actual loopback origin).
  The reserved **`--target desktop`** resolves to a running desktop app's live
  origin for you (or `target.name: desktop`, on any `-m` command), no
  `sam auth login` needed. This is the desktop→cluster promotion path: `pull`
  from the desktop instance, `apply` to the cluster — or `apply` straight back
  to iterate.

Auth is only required when the target has OIDC/RBAC enabled (`sam auth login`
first); a default desktop instance is unauthenticated.

**Starting from scratch?** If the platform already has configuration —
agents, toolsets, models created through the UI —
don't hand-author YAML from a blank repo. Run `sam config pull` first to
capture the current state into a complete, ready-to-apply repo, then edit
from there. It's the canonical starting point for adopting declarative
config on an existing platform: you get every resource, correctly shaped,
instead of reverse-engineering the schema by hand.

The schema for every kind is auto-generated from the runtime DTOs.
`sam config schema show <kind>` prints the live truth; this skill's
schema sections are a snapshot from the CLI version that wrote it
(see `metadata.version` in the frontmatter — after a CLI upgrade, re-run
your `sam ai-assistance skill install` command with `--force` to refresh).
