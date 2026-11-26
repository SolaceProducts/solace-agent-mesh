# Task event logs (STIM)

A **task event log** — called a **STIM file** in Solace Agent Mesh (Agent Mesh) Go — is a YAML record of the full broker event flow for one task: every request, status update, LLM call, tool invocation and result, artifact event, and the final response. It's the tool for "what exactly did this task do, and where did it go wrong."

## Enable capture (two requirements, both needed)

On the entrypoint's `app_config`:

1. A **SQL** session service (SQLite or Postgres — not in-memory):
   ```yaml
   session_service:
     type: sql
     database_url: "sqlite:////absolute/path/to/entrypoint.db"   # 4 slashes = absolute
   ```
2. **Task logging on:**
   ```yaml
   task_logging:
     enabled: true
     # optional: log_status_updates / log_artifact_events / log_file_parts / max_file_part_size_bytes
   ```

Without **both**, the STIM endpoint returns 404 even though tasks run. The `task_events` table (STIM) is separate from the SSE reconnection buffer.

Common enable-time pitfalls:
- **SQLite path needs 4 slashes** for an absolute path (`sqlite:////abs/path`); 3 slashes is a relative path that silently produces an empty DB.
- A stale `sam` process still bound to the port means `sam task send` talks to the old process — `lsof -i :<port>` and kill stragglers.
- Between local runs, remove `.db`, `.db-wal`, `.db-shm` to avoid SQLite lock errors.

## Locate / download the file for a specific task

- **Via the API:** `GET /api/v1/tasks/{taskId}` returns the STIM as a downloadable attachment, `Content-Type: application/yaml`, filename `{rootTaskId}.stim`. It includes the parent task chain + all descendants, so a workflow's subtasks come in one file.
- **Via the CLI:** `sam task send "<msg>" --target desktop -a <AgentName>` (or `-u <entrypoint-url>` for any other deployment) auto-saves the STIM under the task output dir, for example `/tmp/sam-task-{id}/{id}.stim`. `--no-stim` disables that.
- **Find the taskId:** list tasks (`GET /api/v1/tasks`, paginated) or read it from the UI / the entrypoint log line for the conversation.
- **Auth against a deployed entrypoint:** a remote entrypoint with SSO/OIDC on rejects an unauthenticated CLI call with **401**. Run `sam auth login <entrypoint-url>` once first — `sam task send` / `sam api` then reuse the cached token (auto-refreshed), so you can drop `-u` and use `--target <name>`. Bearer tokens are refused over plain `http://` unless you pass `--insecure`. The full CLI-auth surface (`sam auth login/logout/status/list`, token precedence) is owned by `sam-declarative-config`'s CLI-auth reference — go there for details, don't guess token flags.

## Read it

Read the `.stim` YAML directly, using the [event schema](#event-schema-for-reading-the-raw-yaml) below. It records task id, status, timing, every LLM call and tool call, token usage, the subtask tree, and artifact operations — the "which step failed / which tool ran / how many tokens" view.

For a silent tool failure, look for:
- A `tool_invocation_start` with **no matching `tool_result`**.
- A `tool_result` **carrying an error**, in any of four places: `result_data.status` (a string), `result_data.status.state` (a delegated subtask returns the whole Agent-to-Agent (A2A) Task), a non-empty `result_data.error`, or the same shapes under `result` when `result_data` is absent. An explicit success status wins over a descriptive `error` field.
- The flow **ends right after the `request`** with no LLM response or tool events → the task never got past the entrypoint→agent or agent→LLM hop. That's not a tool problem — go back to `traceID` correlation and the broker/LLM checks in [diagnose.md](diagnose.md).

## Event schema (for reading the raw YAML)

Top level:
```yaml
invocation_details:        # task_id, user_id, start/end time, status, initial_request_text, total_tasks, includes_child_tasks
invocation_flow:           # ordered array of events, sorted by created_time
  - id: evt-<prefix>-<seq>
    task_id: <uuid>
    created_time: <epoch-ms>
    topic: <see below — role comes before direction, and the tail differs per family>
    direction: request | status | response | error
    payload: <JSON-RPC envelope>
```

Topic forms (role first, then direction):

```
<namespace>/a2a/v1/agent/request/<agentName>              # no taskId in the topic
<namespace>/a2a/v1/gateway/status/<gatewayID>/<taskID>
<namespace>/a2a/v1/gateway/response/<gatewayID>/<taskID>
<namespace>/a2a/v1/discovery/agentcards
```

Match on the `direction` field rather than parsing the topic: a request topic ends in the
agent name and carries no task id, so matching on the topic does not find a request event.

`direction` meanings: **request** (user/parent → agent), **status** (progress: LLM request/response, tool start/result, artifact ops — the signal is in `payload.result.status.message.parts[*].data.type`, such as `tool_invocation_start`, `tool_result`, `llm_response`), **response** (final, with token usage), **error** (JSON-RPC error envelope). Events are ordered by `created_time`.
