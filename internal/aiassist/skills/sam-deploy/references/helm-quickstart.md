# Helm Quickstart (local or connected evaluation)

Stand up Agent Mesh on Kubernetes for evaluation using the Helm chart's self-contained defaults — embedded broker, bundled PostgreSQL, bundled object storage. This is the **same chart** you later promote to production: values change at promotion, the architecture doesn't. Applies to the go-only 2.x chart line (take the exact chart version from the `Charts/` filename in your download).

## Prerequisites

- Kubernetes cluster v1.20 or later.
- Memory scales with which heavy components are enabled, not a flat number. The **embedded broker is the single heaviest component** (needs ≥ 2480 MiB to start, and fails silently when starved — see helm-troubleshoot.md):
  - Full quickstart (embedded broker + bundled persistence): a node with at least **6 vCPU and 16 GiB allocatable memory**.
  - Hybrid (external broker, `global.broker.embedded: false`, with bundled persistence): dropping the embedded broker frees ~2.5 GiB — an **~8 GiB local cluster is workable**.
- A node sized per the above, with `kubectl` and CPU headroom for the bundled datastores.
- `kubectl` configured, plus the **Helm CLI v3.0 or later** (Helm is a client you install locally).
- The chart archive `solace-agent-mesh-<chart-version>.tar.gz` and the image-pull credentials file `sam-pull-credentials.json` (from the Solace product portal — see the parent skill).
- An LLM provider API key — entered on first login in the Console, not needed at install time.
- Local clusters (Apple Silicon, Podman, minikube) have known silent-startup issues — see helm-troubleshoot.md before you start.

## Self-contained defaults

No separate broker or database install. Quickstart defaults bundle:

- `global.broker.embedded: true` — embedded Solace broker.
- `global.persistence.enabled: true` — bundled PostgreSQL (sessions) + bundled object storage (artifacts).
- `sam.authorization.enabled: false` — no login required (everyone is admin); fine for eval, NOT for shared use.

## Install

```bash
helm install sam /path/to/solace-agent-mesh-<chart-version>.tar.gz \
  --namespace sam --create-namespace \
  --set-file global.imagePullKey=sam-pull-credentials.json
```

The pre-install `sam-doctor` check runs before the workload pods; if it fails, `helm install` returns an error (read the job logs — see helm-troubleshoot.md).

### Alternative: install with an existing pull-secret manifest

If you already have a `kubernetes.io/dockerconfigjson` Secret manifest (rather than the raw `sam-pull-credentials.json`), create the namespace and apply the Secret into it **before** `helm install` — the pre-install `sam-doctor` hook must pull its image, and `--create-namespace` would create the namespace too late for that. Then reference the Secret by name with `global.imagePullSecrets` instead of `--set-file global.imagePullKey` (the two are mutually exclusive):

```bash
kubectl create namespace sam
kubectl apply -n sam -f your-pull-secret.yaml
helm install sam /path/to/solace-agent-mesh-<chart-version>.tar.gz \
  --namespace sam \
  --set 'global.imagePullSecrets[0]=<secret-name>'
```

Quote the `--set` argument — in zsh an unquoted `[0]` is glob-expanded and fails with `no matches found`.

## Post-install

1. Wait for pods to reach Running:
   ```bash
   kubectl get pods -n sam -l app.kubernetes.io/instance=sam -w
   ```
2. Port-forward the Console UI (GWE service is `<release>-solace-agent-mesh-gwe`; release `sam`):
   ```bash
   kubectl port-forward -n sam svc/sam-solace-agent-mesh-gwe 8080:80
   ```
3. Open the Console at `http://localhost:8080`.
4. Configure your LLM provider + API key when the Model Configuration prompt appears on first use.

Health checks:

```bash
curl -s http://localhost:8080/health
curl -s http://localhost:8080/api/v2/platform/health
```

A `404` from `/api/v2/platform/health` while `/health` is OK means the release is older than 2.542 and does not serve them, not that the platform is down; check `/api/v1/platform/health` instead. A `sam` CLI newer than the server stops with an "upgrade Agent Mesh" message for the same reason, and a CLI older than the server gets `410 Gone` on the `/api/v1/platform` routes that moved to `/api/v2`; keep the CLI and the server on the same release.

> Any `helm upgrade` or pod restart drops an existing `kubectl port-forward` (the forward is bound to a specific pod) — re-establish it afterward and re-check `/health`.

## Promotion

The same chart promotes to a shared/production deployment by changing values (external broker, external DB + object storage, OIDC/TLS) — not by re-architecting. See helm-production.md.
