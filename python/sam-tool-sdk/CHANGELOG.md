# Changelog

All notable changes to `sam-tool-sdk` will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

While the SDK is in `0.x`, breaking changes bump the minor version (`0.1 -> 0.2`),
not the patch.

## [Unreleased]

### Added
- `tool_effect(EFFECT_READ_ONLY | EFFECT_SIDE_EFFECTING)` and the
  `DynamicTool.effect` property declare whether a tool is read_only or
  side_effecting. A tool is read_only only if all three hold: it cannot change anything outside the run, whatever its input; it fetches no URL chosen by its input or by the content of a file it reads; it writes, deletes or replaces no artifact under a filename chosen by its caller or by the tool itself. The run is the one task that called the tool, and changing something outside it means writing, sending, deleting, posting, publishing or indexing in an external system, another session or user scope, or starting a job. Changing the run's own session, such as the skills it has loaded, is not outside it. A filename is chosen by the caller when it comes from a parameter, and by the tool when its code fixes or derives it, including a name set on a data object it returns. A result the runtime itself saves under a fresh filename it picks, which it does only when a tool returns data with no name set (auto-disposition), is not a write. Sending the caller's query to one fixed external service, as a search or lookup does, is not fetching a chosen URL. Every other tool is side_effecting. The value is emitted as `effect` in the `--schema` output;
  a tool that declares nothing is treated as side-effecting.

## [0.1.5] - 2026-11-12

### Changed
- An exception that escapes a tool and carries an HTTP failure status (a
  `response.status_code` from 400 to 599, as on `requests.HTTPError` and
  `httpx.HTTPStatusError`) now returns `ToolResult.error` with an error code
  instead of an uncoded execution error, so the agent and the failure
  analysis see why the call failed. The code follows the status: 401
  `DISPATCH_AUTH_REQUIRED`, 403 `PERMISSION_DENIED`, 408 and 504
  `DISPATCH_TIMEOUT`, 429 `RATE_LIMITED`, any other 4xx
  `DISPATCH_BACKEND_4XX`, any other 5xx `DISPATCH_BACKEND_5XX`. The message is
  `HTTP <status>: <exception text>` with each URL's user name, password, query
  string and fragment removed, and no traceback (the traceback still goes to stderr). A 401 does
  not start the OAuth flow; return `ToolResult.auth_required()` for that. Any
  other exception is reported exactly as before.
- Operators see two side effects for these failures once a tool is rebuilt on
  0.1.5. Files the tool wrote before the error are kept as artifacts, and the
  Secure Tool Runtime counts the call as a successful invocation in its audit
  log and metrics; the agent still records it as a failed tool call.

## [0.1.4] - 2026-10-31

### Changed
- A tool's `instructions` no longer repeat the docstring's `Args:` (or
  `Arguments:`, `Parameters:`) section when
  every line of it is part of the final description of a parameter in the
  tool's schema. The model already reads that text as the parameter
  descriptions, so it was sent twice on every request. An `Args:` section with
  any other line (an entry the parser cannot read, a bullet, grouped names, an
  injected parameter such as the tool context, a parenthesised type slot such
  as `(seconds)` or `(int, max 100)`, an entry a later entry for the same
  parameter replaces, or a trailing note) is kept whole, as before. Parameter
  descriptions are unchanged; they join an entry's lines into one line, so
  an entry's line breaks no longer reach the model. A tool whose only
  instruction text was its `Args:` section now has no `instructions`. Where the docstring's parameter descriptions are
  replaced, by the `parameters` a `@with_dynamic_schema` override returns or by
  the `parameters:` a skill's `tools/manifest.yaml` declares without
  `instructions:`, the model reads that replacement instead, so put each
  parameter's guidance there.

## [0.1.3] - 2026-09-09

### Added
- `SandboxToolContextFacade.user_profile` returns the `user_profile` object the
  gateway forwarded in `a2a_user_config` (an empty dict when absent or
  malformed), so a tool can scope lookups to the authenticated user's directory
  attributes instead of trusting LLM-supplied arguments. The same blob is also
  reachable at `state["a2a_context"]["a2a_user_config"]` for tools ported from
  Python SAM. The blob is unsigned: use it for scoping, context and audit, not
  as the sole basis for an authorization decision.

## [0.1.2] - 2026-06-08

### Changed
- The SDK core is now pydantic-free. `ToolResult` and `DataObject` are plain
  dataclasses; `model_dump()` is preserved and returns the same serializable
  dict as before, so tool code needs no changes. pydantic moves to an optional
  extra (`pip install sam-tool-sdk[pydantic]`), consulted lazily only when a
  tool declares a pydantic `BaseModel` as a parameter. This drops the compiled
  `pydantic_core` extension from the default install; that extension is bound
  to the build host's CPython minor version and failed discovery in the STR
  with `No module named 'pydantic_core._pydantic_core'` whenever the build and
  runtime Python versions differed.

### Fixed
- Tool arguments are now coerced to their declared scalar type before
  dispatch. LLM providers often emit `integer` / `number` / `boolean`
  arguments as JSON strings (e.g. `"5"`); these are now converted to the
  schema-declared type across all three dispatch paths (`tool_cli`,
  `dynamic_tool_cli`, `provider_cli`), matching the typed-struct behavior the
  Go SDK already had. Unparseable strings pass through untouched so the tool
  still surfaces its own domain error.

## [0.1.1] - 2026-05-28

### Changed
- Documentation-only release. Trimmed the README to an author-focused guide
  (removed internal runtime-protocol detail) and removed references to the
  source repository from the published package. No API or behaviour change.

## [0.1.0] - 2026-05-28

First public release on PyPI.

### Added
- `tool_cli` entry point for authoring Python tools that run inside the SAM
  Secure Tool Runtime (STR). Handles `--schema` discovery, typed parameters,
  status updates, and artifact I/O.
- `ToolResult` helpers (`ok`, `error`, `partial`) for returning structured
  results to the agent.
- `DynamicTool` and `DynamicToolProvider` base classes for class-based tools
  and providers that emit multiple tools from a single module.
- `Artifact` parameter type for tools that consume named artifacts; the
  framework pre-loads bytes/text before the tool runs.
- `SandboxToolContextFacade` exposing `task_id`, `session_id`, `user_id`,
  artifact APIs, and status update helpers.
