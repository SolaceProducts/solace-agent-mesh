# Helm Install Troubleshooting

Failures you hit during `helm install` / `helm upgrade` on Kubernetes. For a deployment that installed cleanly but misbehaves at runtime (agent won't respond, entrypoint 500s after a working install), see the `sam-troubleshoot` skill.

Placeholders below: `<sam-namespace>` and `<release>` — find both with `helm list --all-namespaces`. (Quickstart uses release `sam`, namespace `sam`.)

## A command sits with no output (it's working, not hung)

Two install-time steps run for minutes with no terminal output, and both are commonly mistaken for a hang — don't Ctrl-C them:

- **`docker load` / `docker push`** of the image archives (air-gapped). The archives are large (the STR image alone is over 1 GB), so each can take minutes. Confirm progress from another shell: `docker images` (loaded images) or `curl -s https://<registry>/v2/_catalog` (pushed images).
- **`helm install` / `helm upgrade`** blocks while the `sam-doctor` pre-install hook pulls its image and validates prerequisites. Watch it with `kubectl get pods -n <sam-namespace> -w` — you'll see the `sam-doctor` pod appear and run. If the command instead returns an error, the hook failed a check (next section).

## helm rejects the values file before anything installs

If `helm install` (or `--dry-run`) fails immediately with `values don't meet the specifications of the schema(s)`, your values file violates the chart's bundled `values.schema.json` — this happens client-side, before the `sam-doctor` hook or any pod. The message names the exact path and the mismatch; fix that path. Common shapes:

- `at '/global/imagePullSecrets/0': got object, want string` — `imagePullSecrets` is a list of **plain string** names, not `- name: …` objects.
- `additional properties not allowed` — a mistyped or unknown key (or a key nested at the wrong level).
- `at '/dataStores/database/host': minLength: got 0, want 1` — a required field is empty; with `global.persistence.enabled: false` the external `dataStores.database.*` fields are required.

Run `helm install … --dry-run` after any values edit to catch these before a real install.

## The pre-install check fails

The pre-install `sam-doctor` job (a Helm pre-install/pre-upgrade hook) validates broker, LLM endpoint, DB, storage, OIDC, and TLS before the workload pods start; a failure makes `helm install` return an error. The job is retained on failure — read its logs:

```bash
kubectl logs -n <sam-namespace> job/<release>-solace-agent-mesh-sam-doctor
```

If the doctor job reports `DeadlineExceeded` and `kubectl logs job/...` returns `error: timed out waiting for the condition`, the doctor **pod never started** (vs. ran and failed a check) — so there are no logs to read. Inspect why the pod couldn't run instead:

```bash
kubectl get events -n <sam-namespace> --sort-by=.lastTimestamp
kubectl describe job -n <sam-namespace> <release>-solace-agent-mesh-sam-doctor
```

The common cause is an image-pull failure (the doctor uses the GWE image) — see the next section.

## Pods stay Pending or ImagePullBackOff

```bash
kubectl get pods -n <sam-namespace>
kubectl describe pod <pod-name> -n <sam-namespace>
```

- `ImagePullBackOff`: confirm `--set-file global.imagePullKey=sam-pull-credentials.json` was passed and the file is a valid `dockerconfigjson` (air-gapped: confirm the registry redirect + pull secret instead).
- `Pending` from insufficient cpu/memory: increase cluster resources. Colima example:
  ```bash
  colima stop && colima start --cpu 10 --memory 16 --disk 100
  ```

## The port-forward command fails

If the Console isn't reachable on `http://localhost:8080`:

```bash
kubectl get pods -n <sam-namespace>
kubectl get svc -n <sam-namespace>
```

The Console (GWE) service is `<release>-solace-agent-mesh-gwe`. If local port 8080 is already in use, forward a different local port, e.g. `8081:80`.

## The Console loads as a blank page

404s for `.js` / `.css` assets mean cached assets from an earlier install — open the Console in a private/incognito window or hard-refresh.

## GWE stuck at 0/1 — object-storage / bucket errors

GWE stays `0/1 Running` (often with a restart) and its startup probe fails (`/health` on `:9090` times out). The usual cause is bundled object storage. GWE logs show:

```
validate S3 bucket "<namespaceId>": HeadBucket ... 404 NotFound
put S3 object ... StatusCode: 500 InternalError
```

The buckets were never created. Check the `s3-init` init container on the GWE pod:

```bash
kubectl logs -n <sam-namespace> <gwe-pod> -c s3-init
```

The init container's log names the storage error — typically bucket creation or credentials against the configured object store.

An invalid `global.persistence.namespaceId` (it is the DB scope and broker topic prefix, and with bundled persistence also the bucket name) no longer gets this far: the chart schema requires `^[a-z]([a-z0-9-]*[a-z0-9])?$`, 3–40 characters, so a value with an underscore, a dot, or uppercase fails `helm install` / `helm upgrade` up front with `values don't meet the specifications of the schema(s)` naming `namespaceId`. Fix the value (mirror the default `solace-agent-mesh`) and re-run the install.

If an earlier install left half-created bundled storage behind, reinstall cleanly, dropping the bundled PVCs:

```bash
helm uninstall <release> -n <sam-namespace>
kubectl delete pvc -n <sam-namespace> --all
helm install <release> ... -f <values>.yaml
```

## Local Kubernetes clusters

Local clusters have two known silent-failure traps:

### Open-file limit too low (rootless Podman)

Rootless Podman caps `nofile` at 524,288, but the broker needs at least **1,048,576** — below that the broker fails silently. Raise it in `~/.config/containers/containers.conf`:

```ini
[containers]
default_ulimits = ["nofile=1048576:1048576"]
```

Then restart the Podman machine:

```bash
podman machine stop && podman machine start
```

### Pod-to-pod DNS fails (minikube default CNI)

The minikube default CNI can break pod-to-pod DNS resolution — start minikube with Calico instead:

```bash
minikube start --cni=calico
```

## Where next

Installed but misbehaving at runtime → the `sam-troubleshoot` skill; values and keys → helm-values.md.
