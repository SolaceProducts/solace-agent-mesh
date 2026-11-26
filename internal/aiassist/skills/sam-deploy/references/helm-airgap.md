# Helm Air-Gapped Install (disconnected cluster)

In a disconnected environment you obtain the delivery package on a connected host, stage the container images into a private registry reachable from the cluster, then install the chart pointed at that registry. Applies to the go-only 2.x chart line (take the exact chart version from the `Charts/` filename in your download).

## Prerequisites

- Kubernetes cluster v1.20 or later; `kubectl`; the Helm CLI v3.0 or later.
- A connected machine (outbound internet) to download the chart and pull/export the images.
- A private container registry reachable from the cluster, with push/pull credentials.
- Image-staging tooling on whichever host pushes to that registry: a container runtime (`docker` / `podman`) or a daemonless copier (`skopeo` / `crane`). See the push recipe below.
- A way to transfer the chart + images into the air-gapped network (removable media or secured transfer).
- An evaluation install needs only the LLM provider reachable from the cluster; a production install also needs the external broker, database, and object store reachable.

## Stage the container images

The portal download is a **delivery package you unpack**, not a single loadable image — for the portal navigation (`… → Enterprise → Current/<release-version>/`) and the full package layout (`Charts/`, `Images/`, `bom.yaml`, …), see the parent [SKILL.md](../SKILL.md#where-the-deployment-artifacts-come-from). The chart archive you install lives under `Charts/`; the images you stage here live under `Images/`.

- Download the image archives from the delivery package's `Images/<arch>/` folder — `amd64/` or `arm64/`, matching your cluster nodes (a connected install would instead pull from the Solace registry).
- Verify each download against `bom.yaml` (the bill of materials). For each file:
  ```bash
  sha256sum <file>
  ```
  The output must match that file's `file_checksum` value in `bom.yaml` (the `file_checksum` carries a `sha256:` prefix; compare without it).
- Transfer the chart archive, the image archives, and `bom.yaml` into the air-gapped network. Pushing the images into your registry is the next section.

**Image sets:** a production air-gapped install needs the `solace-agent-mesh`, `solace-agent-mesh-str`, and `postgres` images (`postgres` runs as a DB-init container that creates the application users/schemas — needed even with an external database). A quickstart air-gapped install additionally needs the `seaweedfs` and `solace-pubsub-enterprise` (embedded broker) images.

## Push the images into your registry

Loading the archives is the single fiddliest step, so here are two concrete paths — pick the one that matches what you have. Both use a container runtime (`docker` / `podman`) or a daemonless copier (`skopeo`); the exact tool doesn't matter.

> **These commands are slow and mostly silent — that is not a hang.** The image archives are large (the STR image alone is well over 1 GB), so a single `docker load` or `docker push` can run for **minutes with no output** before returning. Let each finish; don't Ctrl-C it. To confirm progress from another shell: `docker images` (shows loaded images as they land) or `curl -s https://<registry>/v2/_catalog` (shows what has been pushed).

**A — from the delivery-package archives** (the disconnected default). For each image file:

```bash
docker load -i <image-file>.tar.gz                       # prints the loaded reference
docker tag <loaded-ref> <registry>/<repository>:<tag>
docker push <registry>/<repository>:<tag>
```

The archive **filename is not the image reference** — it carries `-app`/`-str` and `-<arch>` suffixes the chart never uses, so the tag you push to is *not* the filename. `docker load` prints the real embedded ref; retag it to the `<repository>:<tag>` the chart expects. Read the exact tags from the chart's `values.yaml` (they are version-specific); for a recent 2.x release the shape is:

| Downloaded file (under `Images/<arch>/`) | Retag / push as `<registry>/<repository>:<tag>` |
|---|---|
| `solace-agent-mesh-<release-version>-app-<arch>.tar.gz` | `<registry>/solace-agent-mesh:<release-version>` |
| `solace-agent-mesh-<str-version>-str-<arch>.tar.gz` | `<registry>/solace-agent-mesh-str:<str-version>` |
| `postgres-<ver>-<arch>.tar.gz` | `<registry>/postgres:<ver>` |
| `seaweedfs-<ver>-<arch>.tar.gz` | `<registry>/chrislusf/seaweedfs:<ver>` |
| `solace-pubsub-enterprise-<ver>-multi-arch-<arch>.tar.gz` | `<registry>/solace-pubsub-enterprise:<ver>` |

`chrislusf/seaweedfs` keeps its upstream repo path — don't flatten it to `seaweedfs`. Verify each loaded image against its `image_id` in `bom.yaml`: `docker inspect <repository>:<tag> --format '{{.Id}}'`.

Daemonless equivalent (no Docker daemon):

```bash
skopeo copy docker-archive:<image-file>.tar.gz docker://<registry>/<repository>:<tag>
```

**B — mirror registry-to-registry** on a jump host that reaches both a source registry (e.g. the Solace image mirror, pullable with the product pull secret) and your private one:

```bash
skopeo copy docker://<source>/<repository>:<tag> docker://<registry>/<repository>:<tag>
```

Two rules decide whether the pull later succeeds:

1. **The repository leaf must match the chart's image ref** — `solace-agent-mesh`, `solace-agent-mesh-str`, `postgres`, `chrislusf/seaweedfs`, `solace-pubsub-enterprise`. Read the exact repos and tags from the bundle's `values.yaml` (tags are chart-version-specific — don't carry one over from memory).
2. **The registry hostname must be byte-identical** in three places: your push tag, `global.imageRegistry` in the values file, and the cluster's insecure-registry / TLS-trust configuration. A mismatch (`reg:5000` vs `reg.local:5000`) doesn't error here — it surfaces later as an `ImagePullBackOff`.

Confirm what landed before installing:

```bash
curl -s https://<registry>/v2/_catalog
```

## Configure and deploy

**Create a file named `airgap-values.yaml`.** You install the downloaded chart archive as-is — don't unpack and edit it; every override goes in this separate file, passed to `helm install` with `-f`. Start from:

```yaml
global:
  imageRegistry: registry.internal.example.com   # byte-identical to your push hostname (rule 2 above)
  imagePullSecrets:
    - your-registry-secret
```

- `imageRegistry` redirects every image to your registry; the chart builds references as `<registry>/<repository>:<tag>`, so include any project/namespace path in this value. To mirror only some images, set `image.registry` on the individual component instead.
- `imagePullSecrets` is a list of **plain string** secret names (not `- name: …` objects — the chart's `values.schema.json` requires strings and rejects the object form with `got object, want string`), and is **mutually exclusive** with `global.imagePullKey` — an air-gapped install uses the secret, not the `--set-file` key.
- If the cluster reaches the LLM (or other endpoints) through a proxy, add the proxy env vars under `environmentVariables` (`HTTP_PROXY` / `HTTPS_PROXY`). The Platform service ignores them, and connects directly, when it discovers tools from an MCP server, reads an OpenAPI specification URL, checks a Web Search connector's API key, or fetches an external agent's card and OAuth token. Those fetches also refuse loopback and private-network addresses unless `SAM_PLATFORM_ALLOW_PRIVATE_MCP: "true"` is set under `environmentVariables`. That setting applies to the whole Platform service: anyone who can view connectors, or add or edit connectors or external agents, can then make it fetch any loopback or private-network address, so recommend it only when those users are trusted. A host reachable only through the proxy is unavailable to those fetches. An Agent Link Entrypoint created on the Platform service (the **Entrypoints** page or `sam config apply`) likewise ignores the proxy for its wake calls and token requests to a link agent, and refuses loopback and private-network addresses unless `SAM_AGENT_LINK_ALLOW_PRIVATE_WAKE: "true"` is set under `environmentVariables`; anyone who can create or edit link agents can then make it call those addresses.
- For a production air-gapped install, add the external broker / DB / object-store and OIDC/TLS values from helm-production.md to this same file.

Install (no `imagePullKey` — the pull secret is in the values file):

```bash
helm install sam /path/to/solace-agent-mesh-<chart-version>.tar.gz \
  --namespace sam --create-namespace \
  -f airgap-values.yaml
```

`helm install` **blocks while the `sam-doctor` pre-install hook runs** (it pulls its image from your registry and validates prerequisites), so the command can sit with no output for a minute or more — again, not a hang. Watch it from another shell with `kubectl get pods -n sam -w`; if it ultimately fails, read the hook's logs (see helm-troubleshoot.md).

## Verify

```bash
kubectl get pods -n sam -l app.kubernetes.io/instance=sam -w
curl -s https://<your-dns-name>/health
curl -s https://<your-dns-name>/api/v2/platform/health
```

A `404` from `/api/v2/platform/health` while `/health` is OK means the release is older than 2.542 and does not serve them, not that the platform is down; check `/api/v1/platform/health` instead. A `sam` CLI newer than the server stops with an "upgrade Agent Mesh" message for the same reason, and a CLI older than the server gets `410 Gone` on the `/api/v1/platform` routes that moved to `/api/v2`; keep the CLI and the server on the same release.

Full key list → helm-values.md; production externals (broker/DB/storage, OIDC/TLS) → helm-production.md; install failures → helm-troubleshoot.md.
