---
name: sam-deploy
description: Use when deploying Solace Agent Mesh with Helm on Kubernetes — local (minikube/Kind/Colima) or cloud (EKS/GKE/AKS), including air-gapped installs; for shared/team use OR a solo user who specifically wants Helm/Kubernetes (the Helm quickstart is a valid single-node local evaluation); choosing chart values (broker, persistence, images, LLM, OIDC/RBAC, ingress); promoting a local/desktop instance to a cluster. Not for a solo trial via the desktop app (sam-install-run), authoring agents/config content (sam-author-agent / sam-declarative-config), or diagnosing an already-broken deployment (sam-troubleshoot / sam-operate).
---

# sam-deploy

This skill stands up Agent Mesh via **Helm** — usually for **shared use** (others depend on the instance), but also for a **solo user who specifically wants Helm/Kubernetes**: the quickstart is a self-contained single-node local evaluation. For a solo trial via the desktop app, see `sam-install-run` — but if the user names Helm/Kubernetes/a cluster, stay here even for a solo eval (tool preference overrides the shared-vs-solo axis). For what runs *on* the deployment (agents, workflows, config) see `sam-author-agent` and `sam-declarative-config`.

## Where the deployment artifacts come from

**Everything ships as one delivery package from the Solace product portal: https://products.solace.com/ (login required).** Navigate: Sign in → Products → `Agent_Mesh` → Enterprise → select the release → open `Current/<release-version>/`. **Pick a 2.x release** (e.g. `2.225.x GA`) — this skill and the go-only chart are the 2.x product line; the older 1.x releases still listed on the portal are the Python-era distribution and do not ship this chart. Do **not** point users at GitHub repositories, and do **not** produce `helm repo add <url>` commands from memory — older README/doc snippets referencing a public Helm quickstart repo are the Python-era flow, not this product's distribution channel. If a user lacks portal access, route them to their Solace account team or https://solace.com/support.

The release folder is a **package you unpack, not a single loadable image** — the chart, the images, and the CLI/desktop builds sit side by side:

| Item | What it is |
|---|---|
| `Charts/solace-agent-mesh-<chart-version>.tar.gz` | the Helm chart — this is what you `helm install` |
| `Images/{amd64,arm64}/*.tar.gz` | container image archives — **air-gapped installs only**; a connected install pulls these from the Solace registry |
| `CLI/`, `Desktop/` | `sam` CLI and desktop-bundle builds — grab the `sam` CLI from `CLI/` (per-OS/arch); it is what you use for `sam doctor` and the `sam config pull/plan/apply` content promotion the hand-offs below rely on |
| `solace-agent-mesh-<release-version>-bom.yaml` | bill of materials — a `file_checksum` per file (verify with `sha256sum`) and an `image_id` per image (verify after `docker load` with `docker inspect --format '{{.Id}}'`) |
| `solace-agent-mesh-<release-version>-release-notes.html` | release notes |

Image-pull credentials for a connected install ship separately as `sam-pull-credentials.json`. Install from the local chart archive under `Charts/`.

**Versions inside the package are independent — read them, don't infer them.** The product *release* (the folder you clicked, e.g. `2.225.14`), the Helm *chart* version (the `Charts/` filename and `Chart.yaml: version`, e.g. `2.0.23`), and each *image* tag (app matches the release; STR, broker, PostgreSQL, SeaweedFS each carry their own) all differ. A reader who assumes the chart file is named for the release will look for a file that does not exist. Take the chart version from the `Charts/` filename; take image tags from the chart's `values.yaml` (below).

The install command (a connected quickstart/production install; air-gapped differs — see [references/helm-airgap.md](references/helm-airgap.md)):

```bash
helm install sam /path/to/solace-agent-mesh-<chart-version>.tar.gz \
  --namespace sam --create-namespace \
  --set-file global.imagePullKey=sam-pull-credentials.json
```

## Start here — anchor on the chart, then author values

**Inspect the downloaded chart before you write a values file.** The keys and commands in this skill are verified, but the chart bundle is the source of truth for the things this skill can't pin — most importantly the **image tags** (which are chart-version-specific; don't carry over a tag from memory or an older bundle) and any defaults that move between versions. Order of operations:

1. **Obtain** the delivery package from the portal (above); the chart `.tar.gz` is under `Charts/`, plus credentials for a connected install.
2. **Extract and skim** it *for reference only*: `tar xzf solace-agent-mesh-<chart-version>.tar.gz`, then read `solace-agent-mesh/values.yaml` (real keys, defaults, and image tags) and the `GETTING STARTED` notes. Don't edit the extracted copy — you install the `.tar.gz` archive as-is and put your overrides in a separate `-f values.yaml`.
3. **Author** `values.yaml` from what you saw + the reference files below.
4. **Dry-run** (`helm install … --dry-run`) and read the rendered output before the real install.

Authoring a values file before ever reading the chart is the common failure mode — it's how stale image tags and version-drifted defaults slip in.

## Paths, in teaching order

1. **Quickstart — local or connected evaluation.** Embedded broker + bundled persistence; the same chart you promote to production. See [references/helm-quickstart.md](references/helm-quickstart.md).
2. **Production — shared cloud (EKS/GKE/AKS) or any real shared instance.** External broker, external DB + object storage, OIDC/TLS/RBAC. Promotion from quickstart is a values change, not a re-architecture. See [references/helm-production.md](references/helm-production.md).
3. **Air-gapped — disconnected clusters.** Stage images into a private registry, redirect the chart. See [references/helm-airgap.md](references/helm-airgap.md).

For a single-user / evaluation path, see `sam-install-run` (desktop). For shared use, use Helm.

## Hard rules

- **Quickstart is self-contained.** Default values bundle an embedded broker (`global.broker.embedded: true`) and in-cluster persistence (`global.persistence.enabled: true`: PostgreSQL + object storage) — don't bolt on a separate broker for a local rehearsal.
- **Pre-flight is built in.** A `sam-doctor` job runs as a Helm pre-install/pre-upgrade hook and fails the install on broken prerequisites (broker, LLM endpoint, DB, storage, OIDC, TLS). If an install fails immediately, read that job's logs first.
- **Production isn't quickstart.** Shared deployments replace the embedded broker and bundled persistence with real services and enable OIDC — a team instance without auth leaks prompts and artifacts.
- **On a shared broker, set a unique `global.persistence.namespaceId` — 3–40 characters, starting with a lowercase letter, then lowercase letters, digits, and hyphens only (no dots, no underscores).** It is the DB/user scope, the broker topic prefix, **and — with bundled persistence (`global.persistence.enabled: true`) — the object-storage (S3) bucket name** (otherwise the bucket is `dataStores.s3.bucketName`). The default `solace-agent-mesh` collides on topics with every other default install (the Solace Cloud broker is shared by default). The chart schema enforces the pattern, so a value like `greg_test` is rejected by `helm install` before anything is deployed. Mirror the hyphenated default. See [references/helm-production.md](references/helm-production.md).
- **Don't fabricate values.** Commands and value keys here are verified against the go-only 2.x chart line; the exact chart version and image tags move between releases. For anything beyond the keys named in these references, read the `values.yaml` in the downloaded chart bundle — don't guess.

## References

| Topic | File |
|---|---|
| Quickstart — local/connected eval: prerequisites, install, access | [references/helm-quickstart.md](references/helm-quickstart.md) |
| Production — external broker/DB/storage, OIDC/TLS/RBAC, workload identity, custom CA | [references/helm-production.md](references/helm-production.md) |
| Air-gapped — image staging + registry redirect | [references/helm-airgap.md](references/helm-airgap.md) |
| Values reference — load-bearing keys + defaults | [references/helm-values.md](references/helm-values.md) |
| Install-time troubleshooting — sam-doctor, pending pods, port-forward, local clusters | [references/helm-troubleshoot.md](references/helm-troubleshoot.md) |

Hand-offs: configuring *content* on the new deployment (agents, entrypoints) → `sam-declarative-config` (`sam config pull/plan/apply` is also the desktop→cluster promotion mechanism); SSO/RBAC depth, secrets promotion, upgrades → `sam-operate`; "deploy failed / pods crashlooping" runtime symptoms → `sam-troubleshoot`.
