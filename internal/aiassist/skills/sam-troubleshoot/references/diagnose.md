# Diagnosis: logs, traceID, sam-doctor, health

## Make logs queryable first

Set `LOG_FORMAT=json` on every component so slog fields parse cleanly. Logging is `log/slog` throughout — structured key-value pairs, no global logger.

**Identifier fields (camelCase):** `traceID`, `taskID`, `agentName`, `corrID`/`reqID`, `messageID`, `contextID`, `userID`, `publisherID`. **Metric fields (snake_case):** `duration_ms`, `elapsed_ms`, `timeout_s`. (`eventSeq` and `publisherId` are A2A wire user-property keys, not slog keys — grep the sequence as `lastSeq`/`currentSeq`/`gap`.)

Field roles when reading a chain:
- `traceID` — one per user task, end to end. The thing you grep.
- `taskID` — entrypoint-minted task addressing (SSE, cancel, artifacts).
- `corrID`/`reqID` — pending-request key for Secure Tool Runtime (STR) calls and peer delegation.
- `contextID` — A2A session/conversation grouping.
- `eventSeq` + `publisherId` — per-publisher monotonic sequence (gap detector input).

## Follow a request across components

```
# JSON logs
jq 'select(.traceID=="<uuid>")' <logs>
# text logs
grep 'traceID=<uuid>' <logs>
# Datadog Logs
@traceID:<uuid>
```

The chain crosses entrypoint → agent (AWE) → STR → tool, and survives peer delegation. Where it stops tells you the failing hop: entrypoint logs the request but no task published → entrypoint/routing; task published but agent never picks it up → broker connectivity or topic-namespace mismatch; agent picks up then stalls → LLM call (auth/egress/model).

**"Agent never discovered" check:** before chasing logs, confirm the entrypoint even knows the agent exists — the agent should appear in the UI's agent list (agents publish an agent card on the discovery topic at startup; a healthy AWE logs the card publish). If the agent isn't listed, the problem is registration/discovery (agent not started, broker namespace mismatch, or card never published), not request routing.

**Per-publisher stream gaps** (silent-failure detector): the entrypoint watches `(taskID, publisherId, eventSeq)`. A `>+1` jump logs `event sequence: gap detected on per-publisher stream` at ERROR (fields `taskID`, `traceID`, `publisherID`, `lastSeq`, `currentSeq`, `gap` — the `traceID` on the line is your pivot back to the full chain); a non-increase logs `event sequence: duplicate or out-of-order event` at WARN. Either means events were dropped/split — suspect a shared broker subscription, duplicate `agent_name` or entrypoint id across pods, or a misrouted queue, **before** suspecting the tool.

## sam-doctor — preflight checks

Validates the environment independent of a running Agent Mesh. Runs as the Helm pre-install/pre-upgrade hook automatically, or as the `sam doctor` CLI subcommand.

```
SAM_DOCTOR_CONTEXT=helm  sam-doctor               # cluster preflight
SAM_DOCTOR_CONTEXT=local sam-doctor --verbose     # local/dev
SAM_DOCTOR_CONTEXT=wheel sam-doctor --no-fail-on-error
```

Checks (per context): **broker connectivity** (TCP + Solace auth), **LLM connectivity** (endpoint reachable + key accepted), **database** reachability + auth, **object storage** reachability + auth, **TLS certificate** validity, **OIDC** discovery reachability, plus **runtime version** and **port availability** (local/wheel contexts only — Helm omits both, since the pod has a known runtime and a fresh network namespace). Output is a PASS/FAIL/WARN/SKIP table with a reason per row; non-zero exit on a blocking failure unless `--no-fail-on-error`.

Controls:
- `SAM_DOCTOR_CONTEXT` selects the check set; **unset defaults to `local`** (developer-machine: unconfigured services SKIP rather than fail) and still runs — it prints `SAM_DOCTOR_CONTEXT not set — defaulting to 'local'…`. Set `helm`/`wheel` for the strict deployment check sets. A **misspelled** value is a hard error, not a silent pass.
- `SAM_DOCTOR_SKIP_CHECKS=broker_connectivity,oidc_provider` skips named checks (comma-separated; use the check's internal name, e.g. `oidc_provider`, not `oidc`).

## Health endpoints — two distinct surfaces

1. **Entrypoint request-path `/health`** — shallow liveness ("the HTTP listener is up"), returns 200. Fine for an LB ping; **not** diagnostic of component health.
2. **Workload health server** — the real one. `GET /health` returns 200 `{"status":"healthy"}` or 503 `{"status":"unhealthy","error":"component <name> unhealthy: ..."}` aggregating every component's `Health()`. `GET /ready` returns readiness (`/ready` also reflects broker-connection status). Point Kubernetes liveness/readiness probes here.

   **A 200 does not mean every agent is serving.** On an AWE hosting several agents or workflows, one instance failing to start is deliberately isolated so it cannot crash-loop the pod and take its healthy neighbours down with it — the aggregate stays 200 while that one instance is absent. So `/health` 200 plus "agent not found" from a caller is a consistent, expected combination, not a contradiction. When an agent is missing, do **not** stop at the health endpoint:

   - Check what is actually serving rather than assuming everything deployed is: `sam api /api/v2/platform/agents` (workflows: `/api/v2/platform/workflows`) lists platform-managed components with a `runtimeStatus` **object** — filter on `.runtimeStatus.status`, never the field itself. `running` means live on the mesh now; `degraded` means live but reporting problems (read `.runtimeStatus.problems`). A `toolSource:` problem raised because an MCP server was unreachable when the agent started clears by itself: the agent retries that server after 5 s, 15 s and 30 s, then every minute, and registers its tools once the server accepts a connection, so bringing the server up is enough. A server at the wrong address is retried the same way but never connects, so correct the connector and redeploy. The AWE log shows which case applies: `mcp client connection failed; retrying in background` (WARN) at startup, then `mcp toolset still unreachable; retrying` (WARN) per attempt and `registered MCP toolset (late)` once it loads, or `mcp toolset retry gave up; toolset tools not registered` (ERROR) when a retry hits a failure that needs a redeploy; filter on `connectorId`. Entries on a server that any entry with `hil`, `volume_bindings` or `network_bindings` also names are not retried, and neither is a server that refuses the agent's credentials: the startup line for those is `mcp client connection failed; toolset tools not registered` (ERROR) with `retryInBackground=false`. They, and every other tool-source failure, stay until a redeploy; `starting` is the short post-deploy grace window (agents only — workflows skip it); a **deployed** agent that stays `disconnected` is hosted nowhere. The status does not name the failed phase — the recorded cause does; read it in the AWE log line below, then redeploy (see `sam-operate`). A failed *deploy* never reaches `disconnected`: it shows `stopped` with `deploymentStatus` of `deploy_failed` in the same listing, and the AWE log carries no failure line — the error went back over the control response only — so read the platform log instead. (There is no `sam control` command and no `/api/v1/control` HTTP API — both were removed; live lifecycle actions go through the builder UI.)
   - Search the AWE log at startup for `instance failed to initialize` / `instance failed to start` (both ERROR, one per failed instance, with `name` and the underlying cause). This is the primary signal, and it is emitted once at boot — if the pod has been up a long time and logs have rotated, use the agents listing above instead.
   - A 503 from an AWE means *nothing* is serving there, not "something is wrong". An AWE where every instance fails to start — whether at init or at start — exits during boot instead, so a pod in `CrashLoopBackOff` is that case and its logs name the cause. A 503 on a pod that booted fine usually means the instances died afterwards — but not always: a control-plane `enabled=true` on the pod's only instance, whose Start then fails, also lands `alive == 0` and returns 503 with nothing having died.
   - Statically configured (YAML) instances have no platform row at all — they never appear in `/api/v2/platform/agents`. A running one publishes a card (`GET /api/v1/agentCards`); a failed one is absent there, and the failure shows up **only** in the AWE log — there is no deployment status to inspect in the UI.

Health-server ports on Kubernetes: the gateway pod (which also hosts the platform service) `:9090`, the AWE `:8090`, and the STR `:8090`; change one with the `--health-addr` flag or YAML `management_server.port`. Under local `sam run`, the spawned processes share the host's loopback, so each gets its own default: gateway `:9090`, platform `:9091`, AWE `:8090`, STR `:8092`. The desktop app picks its gateway and platform ports at launch (it prefers the defaults and falls back to any free port) and prints the bound addresses on the `embedded SAM started` log line. Its health server does not float: it binds loopback `:8090` (override with `HEALTH_ADDR`), and if that port is taken it logs a `management endpoint failed to bind` ERROR and runs without health probes. The address it did bind is on the `management server listening` line.

## Per-deployment-mode log access

| Mode | Components | Get logs |
|---|---|---|
| Kubernetes / Helm | GWE, AWE, STR as separate pods; the platform service runs inside the GWE pod | `kubectl logs <pod>` (`--previous` on crashloop); `kubectl describe pod <pod>` for events |
| Desktop (single process) | all components run in one process | the app's log output / stdout |

Do **not** assume pod label selectors (e.g. `app=solace-agent-mesh`) — they depend on the Helm chart's conventions; list pods and read the names rather than guessing a selector.

## Common startup/runtime errors and where they originate

| Error shape | Meaning | Action |
|---|---|---|
| `parsing YAML: ...` / `missing required '<key>'` | bad config; a misspelled key is silently dropped then surfaces as "missing required" | fix YAML at the named line/key |
| `solace broker: connect to <url>: ...` (Access denied / VPN not found / tls: ... unknown authority) | broker connectivity / auth / TLS | check broker URL, credentials, VPN, trust store; `sam-doctor` broker check |
| `broker connect failed, retrying` (WARN, every 5 s) | the Entrypoint Executor (EPE), AWE or STR cannot reach the broker at start; it retries for up to 30 min with `/ready` at 503 | same checks as the row above; a rejected credential, unknown VPN, invalid client name or ACL denial exits at once instead |
| `broker connection lost, exiting so the pod restarts` (ERROR) | EPE, AWE or STR lost its broker connection; Kubernetes restarts it | check broker reachability from that pod; one line per pod is expected on a broker restart or failover |
| `environment variable <KEY> not set for provider <p>` | LLM key missing | set the key on the component that runs the LLM (the agent/AWE) |
| `open ... database: ...` / `run migrations: goose up ...` | DB unreachable or migration failed | check DSN + permissions; see sam-operate upgrades for migration failures |
| `goose up (table=..., versions=[...]): ... lock timeout` / `migration still running` | a migration waited on a table lock held by another session | the `blockedBy` field on `migration still running` names the holding sessions; end or wait them out, then let the pod restart (sam-operate upgrades) |
| `async index build gave up after repeated failures` (ERROR), or from a pod that takes over as leader `async index builds gave up and are skipped until attempts is reset` (WARN) | a background index build on the gateway failed 10 times | read `last_error` in `gateway_async_indexes`, fix the cause and reset the row; the leading gateway pod builds it within ten minutes (sam-operate upgrades) |
| `leader job keeps failing` (ERROR), after `leader job failed` (WARN) lines with the same `job` | work that runs on one pod for the whole deployment (data retention, task record cleanup, index builds, sweepers) failed three runs in a row; the pod keeps serving | read the `leader job failed` lines for the cause; `leader term started` names the pod that leads, and `leader job recovered` follows the next successful run |
| `listen :<port>: bind: address already in use` | port conflict | `lsof -i :<port>`; kill stale process or change port |
| `... agent delegation denied` | caller lacks `agent:<name>:invoke` scope | RBAC config → sam-operate |

## Existing customer docs to point users at

These are published troubleshooting pages (cite the docs site, not repo paths): the installation **troubleshoot** page (deployment-time failures), the administering **scenario-troubleshooting** page (day-two failures), and the installation **monitor** page (health-probe wiring + traceID queries). They cover most failure classes above; this skill adds the symptom-routing and traceID-first workflow on top.
