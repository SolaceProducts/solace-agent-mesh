# Helm Production Deployment

Deploy Agent Mesh on a real shared cluster (EKS/GKE/AKS or any shared instance) with an external broker, external PostgreSQL, external object storage, and OIDC/TLS/RBAC.

This is the **same chart** as the quickstart — promotion is a values change, not a re-architecture. There are no cloud-specific chart variants. You build a `production-values.yaml` that disables the bundled components and points at real services. Applies to the go-only 2.x chart line (take the exact chart version from the `Charts/` filename in your download). The parent skill covers artifact sourcing and the install command shape; this file is the production-values walkthrough plus deploy/verify.

## Prerequisites (beyond the quickstart)

- Kubernetes v1.20 or later; `kubectl`; Helm CLI v3.0 or later.
- The chart archive `solace-agent-mesh-<chart-version>.tar.gz` and the image-pull credentials file `sam-pull-credentials.json` (from the Solace product portal).
- Node(s) with at least **4 vCPU and 16 GiB allocatable memory** for the GWE/AWE/STR workloads (the external layout removes the embedded broker and bundled datastores).
- An external Solace broker (Solace Cloud or self-managed PubSub+): secured SMF URL, Message VPN, client username, password.
- Managed PostgreSQL with admin credentials that can create databases and users.
- An S3-compatible object store (AWS S3, Azure Blob Storage, or Google Cloud Storage).
- A DNS name and TLS certificate for it.
- An OIDC identity provider for SSO and RBAC.

The chart runs the `sam-doctor` pre-install check plus template-time cluster-resource checks (it verifies referenced Secrets, ConfigMaps, StorageClass, and IngressClass exist). Template-time checks need `get` RBAC; disable them with `validations.clusterResourceChecks: false`.

## Build production-values.yaml

### Disable embedded components

Set a deployment namespace id and turn off the bundled broker and datastores:

```yaml
global:
  broker:
    embedded: false
  persistence:
    enabled: false
    namespaceId: "your-deployment-id"
```

> **On a shared broker, set `global.persistence.namespaceId` to a value no one else on that broker uses.** It prefixes Agent Mesh's broker topics and scopes its database (and, with bundled persistence, names its storage bucket), so the default `solace-agent-mesh` collides with any other default install — likely on Solace Cloud, where brokers are shared. Use lowercase letters, digits and hyphens (format rules: [helm-values.md](helm-values.md)), and pick it before the instance holds data: changing it later points Agent Mesh at fresh, empty databases.

### External broker

`broker.url` is a secured SMF URL (`tcps://...:55443`) or WebSocket Secure URL (`wss://...:443`):

```yaml
broker:
  url: "tcps://your-broker.messaging.solace.cloud:55443"
  vpn: "your-vpn"
  clientUsername: "your-username"
  password: "your-password"
```

### LLM (optional)

Optional at install. Non-empty endpoint, key, and model names seed the `planning` and `general` model aliases at startup:

```yaml
llmService:
  llmServiceEndpoint: "https://api.openai.com/v1"
  llmServiceApiKey: "your-api-key"
  planningModel: "gpt-4o"
  generalModel: "gpt-4o"
```

### External PostgreSQL

```yaml
dataStores:
  database:
    host: "mydb.abc123.us-east-1.rds.amazonaws.com"
    port: "5432"
    adminUsername: "postgres"
    adminPassword: "your-admin-password"
    applicationPassword: "your-application-password"
```

- `port` defaults to `5432`. `adminUsername` / `adminPassword` are used by a DB-init container to create the application users and databases. `applicationPassword` is the shared password for all Agent Mesh database users (web UI, orchestrator, platform, agents) — required for external persistence.
- **Rotation warning:** the init container creates users only if they don't already exist; it does NOT change an existing user's password. To rotate, set a new `global.persistence.namespaceId` (creates fresh users and databases) or change the password directly in the database.
- Supabase pooler users: also set `dataStores.database.supabaseTenantId` to the project ID.

### External object storage

`type` is `s3`, `azure`, or `gcs`. S3 example:

```yaml
dataStores:
  objectStorage:
    type: "s3"
  s3:
    bucketName: "my-sam-artifacts"
    connectorSpecBucketName: "my-sam-artifacts"
    region: "us-east-1"
    accessKey: "your-access-key"
    secretKey: "your-secret-key"
```

- `connectorSpecBucketName` may equal `bucketName`. Leave `dataStores.s3.endpointUrl` empty for AWS S3; set it for MinIO or other S3-compatible stores. For azure/gcs, populate the `dataStores.azure` or `dataStores.gcs` block instead. Omit `accessKey` / `secretKey` when using workload identity (below).

### Ingress + TLS

TLS terminates at the ingress:

```yaml
sam:
  dnsName: "sam.example.com"
ingress:
  enabled: true
  className: "nginx"
  host: "sam.example.com"
  tls:
    - secretName: sam-tls
      hosts:
        - sam.example.com
```

- `sam.dnsName` is the external hostname; it drives the host portion of every emitted URL, including the OIDC redirect URI. `ingress.host` (defaults to empty) is the Ingress rule's host — set it, usually to the same value as `sam.dnsName`. `ingress.className` is `nginx`, `alb`, `traefik`, or `gce`.
- Deploys and updates of agents, workflows, and entrypoints, and rollbacks of agents and workflows, are synchronous: each stops the running instance and then starts the new one, and each step can take up to 60 seconds. A failed workflow deployment rolls back to the last good one, which takes at least another 60 seconds, and more for a workflow with several instances. Set the request and idle timeouts on the ingress controller and on any load balancer in front of Agent Mesh to at least 120 seconds (at least 180 seconds to cover a failed workflow deployment, and higher for a workflow with several instances), or a slow deploy is cut off before Agent Mesh answers.

### OIDC + RBAC

```yaml
sam:
  authorization:
    enabled: true
  oauthProvider:
    oidc:
      issuer: "https://login.microsoftonline.com/YOUR-TENANT-ID/v2.0"
      clientId: "your-client-id"
      clientSecret: "your-client-secret"
```

- `authorization.enabled: false` means every user has admin access — enable it for any shared instance.
- **Register this exact OIDC callback URI with your IdP: `https://<your-dns-name>/api/v1/auth/callback`** (substitute `sam.dnsName`). Login fails if the registered URI doesn't match exactly.

### Session secret

```yaml
sam:
  sessionSecretKey: "your-session-secret"
```

- If empty, the chart generates one on first install and keeps it stable across upgrades. To set your own: `openssl rand -hex 32`.

## Authenticate to cloud storage with workload identity

Prefer workload identity over static keys:

```yaml
dataStores:
  objectStorage:
    type: "s3"
    workloadIdentity:
      enabled: true
samDeployment:
  serviceAccount:
    name: "sam-solace-agent-mesh-sa"
    annotations:
      eks.amazonaws.com/role-arn: "arn:aws:iam::123456789012:role/my-sam-role"
```

GWE, AWE and STR share this one service account (default `<release>-solace-agent-mesh-sa`). `serviceAccount.name` is required whenever `serviceAccount.annotations` or `dataStores.objectStorage.workloadIdentity.enabled` is set; the render fails without it. Upgrading a release whose cloud identity trusts the former per-component `-gwe-sa` name: set `name` to that former name (the render error prints it), or re-point the cloud identity at the new name.

Per-cloud service-account annotation:

- `eks.amazonaws.com/role-arn` (AWS IRSA)
- `azure.workload.identity/client-id` (Azure)
- `iam.gke.io/gcp-service-account` (GCP)

Omit the static credentials (`accessKey` / `secretKey`, `accountKey`, or `credentialsJson`) when workload identity is enabled.

## Azure Workload Identity for models

To let Microsoft Foundry (`azure_openai`) models sign in to Azure without a stored key:

```yaml
samDeployment:
  serviceAccount:
    name: "sam-solace-agent-mesh-sa"
  azureWorkloadIdentity:
    enabled: true
    # audience: api://AzureADTokenExchange   # default; ignored when Microsoft's webhook mounts the token
```

- The chart mounts a projected token into GWE/AWE/STR and sets `SAM_AZURE_WORKLOAD_IDENTITY_ENABLED=true` and `AZURE_FEDERATED_TOKEN_FILE`. If the pods carry `azure.workload.identity/use: "true"` (Azure Blob workload identity, or `podLabels`), the webhook supplies the token and the chart sets only the flag.
- Enable only in a namespace that runs one organization's Agent Mesh: any pod running as the service account can sign in as every identity that trusts it.
- The cluster's service account issuer must be publicly reachable; any Kubernetes cluster works (AKS, EKS, GKE, others), no Azure webhook needed. The Azure identity then needs a federated credential for that issuer, `system:serviceaccount:<namespace>:<service-account>`, and the audience; the **Models** form shows all four values. Model setup lives in `sam-author-agent` / `sam-declarative-config`.
- `podLabels` values must be strings (quote `"true"`).

## Trust custom CA certificates

Create a ConfigMap whose data key ends in `.crt`:

```bash
kubectl create configmap truststore \
  --from-file=ca.crt=/path/to/your-ca.crt \
  -n sam
```

Enable injection:

```yaml
samDeployment:
  customCA:
    enabled: true
    configMapName: truststore
```

A `ca-merge` init container appends the certs to the shared system trust bundle; the ConfigMap must exist at install time. To rotate: replace the ConfigMap, then `kubectl rollout restart` the workloads (pods read the bundle only at startup).

## Deploy

Dry-run first:

```bash
helm install sam /path/to/solace-agent-mesh-<chart-version>.tar.gz \
  --namespace sam --create-namespace --dry-run \
  --set-file global.imagePullKey=sam-pull-credentials.json \
  -f production-values.yaml
```

Then install (drop `--dry-run`). Post-install output reports it's configured for production use and prints the Console URL from `sam.dnsName`.

Converting an existing quickstart release is a `helm upgrade` (same creds file and values) — but **migrating data from the embedded PostgreSQL to an external database is not automatic**; plan that separately.

Tip: use `extraSecretEnvironmentVars` to source env vars from existing Kubernetes Secrets instead of putting secrets in values.

## Verify

```bash
kubectl get pods -n sam -l app.kubernetes.io/instance=sam -w
curl -s https://sam.example.com/health
curl -s https://sam.example.com/api/v2/platform/health
kubectl logs -n sam -l app.kubernetes.io/instance=sam --tail 200
```

A `404` from `/api/v2/platform/health` while `/health` is OK means the release is older than 2.542 and does not serve them, not that the platform is down; check `/api/v1/platform/health` instead. A `sam` CLI newer than the server stops with an "upgrade Agent Mesh" message for the same reason, and a CLI older than the server gets `410 Gone` on the `/api/v1/platform` routes that moved to `/api/v2`; keep the CLI and the server on the same release.

The Console at `https://<your-dns-name>` redirects to your IdP for login.

---

Full key list and defaults → helm-values.md; air-gapped clusters → helm-airgap.md; install failures → helm-troubleshoot.md.
