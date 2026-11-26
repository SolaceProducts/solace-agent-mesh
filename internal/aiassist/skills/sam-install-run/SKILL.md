---
name: sam-install-run
description: Use when someone wants to install or try Solace Agent Mesh on their own machine — downloading and launching the desktop app, first chat with the built-in agents, or pre-flight checks for a local trial. Not for shared/team deployments (sam-deploy), building agents beyond the first chat (sam-author-agent), or fixing a broken installation (sam-troubleshoot).
---

# sam-install-run

This skill gets one person from zero to chatting with Agent Mesh on their own machine. Deciding question: *is anyone else depending on this instance?* Yes → `sam-deploy`. Once they're chatting and want to build something → `sam-author-agent`.

**Tool preference overrides the audience axis.** If a solo user specifically wants **Helm / Kubernetes / a cluster** — even just to try it themselves — that is a legitimate local evaluation, not a team deployment: route them to `sam-deploy`'s Helm quickstart (local/connected eval) rather than steering them onto desktop. Don't reserve Helm for shared use.

## Where everything comes from

**All artifacts — desktop installers, the `sam` CLI binary, Helm charts — come from the Solace product portal: https://products.solace.com/ (login required; navigate to `Agent_Mesh`).** No portal access → Solace account team or https://solace.com/support.

- **Never** point users at GitHub (the product repos are not public).
- **Never** suggest `pip install solace-agent-mesh` — that is the *Python* implementation, a different runtime. There is no pip route to this product.

## The one hard prerequisite

**An LLM API key** (any OpenAI-compatible provider, or Anthropic). Everything else — broker, database, storage — runs embedded and local in the desktop trial (in-process dev broker, on-disk SQLite, local filesystem storage under the data dir); nothing external to stand up. Don't *push* a user toward standing up brokers, Postgres, or Kubernetes for a solo trial — but if they specifically want Helm, the Helm quickstart bundles its own broker + persistence and is a valid solo eval (path 2).

## Paths, in order

1. **Desktop app — the default recommendation** (macOS, Windows, Linux). Download from the portal, launch, supply the LLM key, chat. See [references/trial-paths.md](references/trial-paths.md). *Caveat: desktop installers are rolling out — if the portal doesn't list one for the user's OS yet, the Helm quickstart (path 2) runs locally as a single-node trial; fall back to it without apology.*
2. **Helm quickstart — a local single-node trial or for users who want Kubernetes** (minikube/Kind/Colima, single-node, embedded broker + bundled persistence). Runs entirely on the user's own machine. Don't offer this proactively over desktop, but surface it when the user names Helm/Kubernetes/a cluster, or when a desktop installer isn't available for their OS — then hand off to `sam-deploy` → Helm quickstart for the mechanics (don't reproduce them here).

## What first run actually looks like

No example gallery (yet). The chat UI opens with two agents ready:
- **Orchestrator** — the conversational entry point; chat with it immediately. (The only DB-seeded built-in agent.)
- **Builder** (shown as *Solace Agent Mesh Builder*) — the AI-assisted creation path: describe an agent in plain language and it drafts one. Bundled with the desktop/embedded runtime, so it's always present. This is the bridge to `sam-author-agent`.

## Hard rules

- **Go product only.** No `pip install`, no `sam init` (the Go CLI has no scaffold verb — the desktop app boots from bundled defaults), no `solace-ai-connector`, no Python Agent Mesh ports/paths.
- **One product, no feature tiers.** Agent Mesh has no community/enterprise feature split and no `-enterprise` binaries — every build carries the same features. The only build variation is product analytics (below): a `-desktop-free-` installer, when a release publishes one, is the same app with analytics compiled in. Never present a "base vs enterprise" feature choice (it isn't one).
- **The desktop app has no fixed port** — it picks a free one at launch and opens the WebUI for you. Take the URL from the app or its startup log, never from memory; the CLI reaches it with `--target desktop`.
- **Pre-flight on a laptop**: `sam doctor` runs a local pre-flight by default (the `local` context — `SAM_DOCTOR_CONTEXT` is optional now, needed only for `wheel`/`helm`), checking the LLM key, ports, and runtime before first start — cheaper than debugging a blank chat.
- **The desktop instance is self-contained and local.** It does not share agents, models, or config with any hosted/web Agent Mesh the user has elsewhere (e.g. a company support assistant) — its agents live in its own on-disk SQLite. Don't assume something built in one instance appears in the other; to carry config across, use `sam config pull`/`apply` against the desktop origin (closing note below). There is no automatic sync.
- A failing *trial* (won't start, blank UI, LLM errors) routes to `sam-troubleshoot` / `sam-operate` only after `sam doctor` has been run and read.
- **Product analytics is on in free downloads and absent everywhere else.** Free downloads of Agent Mesh (desktop bundle files whose names contain `-desktop-free-`, for example `solace-agent-mesh-<version>-desktop-free-macos-<arch>.dmg`) collect product analytics, and collection is on by default. To turn it off, run `sam analytics disable`, or turn off **Usage analytics** under **Privacy** in the **User Account Settings** dialog; before sign-in that dialog is not available, so `sam analytics disable` is the only way. Both record the same per-installation decision, and `sam analytics status` reads it without changing it (`Not recorded` when nobody has answered yet). Enterprise downloads, the standalone `sam` CLI, the container images and the Helm chart include no product analytics and no `sam analytics` command, and no setting or environment variable adds it. A deployment with its own `session_service.database_url` keeps the decision there, so point `--database-url` (or `SAM_ANALYTICS_DATABASE_URL` when the URL carries a password) at it. Say plainly that analytics is on unless turned off, and never suggest editing the record directly.

## References

| Topic | File |
|---|---|
| The desktop trial flow in detail + pre-flight | [references/trial-paths.md](references/trial-paths.md) |

Graduation hand-offs: "now I want it to do X" → `sam-author-agent` (often just: talk to the Builder agent); "my team should use this" → `sam-deploy` (and `sam config pull` carries what they built — see `sam-declarative-config`).

The desktop instance runs a full platform whose config API is fronted by the entrypoint proxy on a port chosen at launch, so `sam config apply --target desktop` targets it directly (the CLI looks up the port the running app bound) — you can iterate declarative config against the desktop instance, not just `pull` from it. See `sam-declarative-config`.
