# Tool build reference

`sam config plan` and `sam config apply` build tool sources before
bundling them for upload. The build resolver picks a strategy from the
contents of `toolsets/<name>/src/`, runs it (when needed), and caches
the resulting zip at
`toolsets/<name>/.sam-cache/build/<targetOS>-<targetArch>/<hash>.zip`
so subsequent plans against the same source AND target are no-ops. The
target tuple is also mixed into the hash, so a cache zip dropped into
the wrong subdir fails the integrity check rather than silently
serving a binary for the wrong platform. Each toolset has its own
cache — removing the toolset directory cleans up its cache.

This describes the **author flow only**. If `toolsets/<name>/<name>.zip`
exists (mirror flow, e.g. after `sam config pull`), the resolver
uploads the bundle as-is and the build pipeline is skipped entirely.

This file is the authoring contract for the build pipeline. Read it when
you add a new toolset, change one's build, or hit `[BUILD: …]` output
you don't recognise.

## Scaffolding a new tool

The Agent Mesh tool SDK ships **embedded in the `sam` CLI binary** for both
Go and Python:

- **Go SDK** — the full `pkg/samtoolsdk/` source tree is baked into
  the CLI. `sam toolset init --lang go` writes it to
  `toolsets/<name>/src/_sdk/samtoolsdk/` and points the generated
  `go.mod` at it via a `replace` directive. No network access is
  required at scaffold time or build time.
- **Python SDK** — `sam-tool-sdk` is a normal PyPI package. The
  scaffolded `pyproject.toml` pins it narrowly
  (`sam-tool-sdk>=0.1,<0.2`) so a breaking 0.x release does not silently
  break existing tools. Local dev uses `pip install -e .` into a venv
  for IDE autocomplete and unit tests; the build uses
  `pip install --target dist/<os>-<arch>/python .` to drop the SDK,
  every transitive dep, and the tool's own source into the deployment
  Lambda Layer. Both steps need network access to reach PyPI.

The same embedded blob is **also used at build time**: if a tool's
SDK directory is missing when `sam config apply` runs (e.g. you cloned
a repo where `_sdk/` is `.gitignore`d), the build pipeline re-injects
the SDK from the CLI binary before invoking `build.sh`. A single-line
info log surfaces the action:

```
sam: vendored samtoolsdk into <name>/_sdk/ (was missing)
```

If the SDK is already present, the injector stays silent. Set
`SAM_TOOL_SDK_REFRESH=1` in your environment to force a re-vendor on
every build — useful after a CLI upgrade when you'd rather not commit
a new `_sdk/`. Without that opt-in, an existing `_sdk/` is left alone.

### `sam toolset init`

```
sam toolset init NAME [PATH] --lang go|python [--force]
```

- **NAME** — toolset directory name (e.g. `pptx`). Validated as a safe
  path component: no `/`, no leading dot.
- **PATH** *(optional)* — flexible target. Three forms; first match wins:
  1. Full path ending in `toolsets/<name>` → used verbatim; `src/` is
     created inside.
  2. A `toolsets/` directory → toolset is created as
     `<path>/<name>/src/`.
  3. Anything else → treated as the repo root; toolset is created as
     `<path>/toolsets/<name>/src/`.
  Defaults to the current working directory.
- **`--lang go|python`** — required. No default; the user must choose.
- **`--force`** — overwrite an existing non-empty target.

### Init examples

Go tool, scaffolded from the repo root:

```
$ sam toolset init pptx --lang go
scaffolded go tool at /repo/toolsets/pptx/src
  injected SDK vendor copy from CLI binary

Next steps:
  cd /repo/toolsets/pptx/src
  ./build.sh
```

Layout written:

```
toolsets/pptx.yaml         # kind: toolset, name: pptx
toolsets/pptx/
  src/
    main.go                # samtoolsdk-driven greet() skeleton
    go.mod                 # replace samtoolsdk => ./_sdk/samtoolsdk
    _sdk/samtoolsdk/       # embedded SDK source (gitignored by default)
    build.sh / .bat        # cross-platform build scripts
    manifest.yaml          # STR registration (kind/runtime/executable)
    README.md
```

Python tool, scaffolded against a `toolsets/` directory:

```
$ sam toolset init weather ./my-repo/toolsets --lang python
scaffolded python tool at /my-repo/toolsets/weather/src
```

Layout written:

```
toolsets/weather.yaml
toolsets/weather/
  src/
    pyproject.toml          # depends on sam-tool-sdk>=0.1,<0.2 from PyPI;
                            # declares [project.scripts] weather = "weather:cli"
    src/weather/
      __init__.py           # re-exports cli (the tool_cli callable)
      __main__.py           # tool function + cli = tool_cli(greet)
    build.sh                # pip install --target dist/<os>-<arch>/python .
    build.bat               # Windows equivalent of build.sh
    manifest.yaml           # runtime: python, executable: python/bin/weather
    README.md
```

Specific path with overwrite:

```
sam toolset init pptx ./my-repo/toolsets/pptx --lang go --force
```

### How the scaffolded toolset builds

The scaffold writes **both** `build.sh`/`build.bat` *and* the
language-convention markers (`go.mod` for Go, `pyproject.toml` for
Python). Because the resolver tries the host-appropriate build script
first, every freshly-scaffolded toolset takes the **script path**, not
the convention path. The scripts themselves shell out to the language
toolchain — they're thin wrappers, not custom builds.

Both generated `build.sh` scripts require `SAM_TOOL_TARGET_OS` and
`SAM_TOOL_TARGET_ARCH` (`sam config apply` exports them; export both
yourself for a manual build) and write their output to
`$SAM_TOOL_BUILD_OUT`. Read the scaffolded script for the exact steps.

For Python, the `pip install --target dist/<os>-<arch>/python .` step installs the
tool's own package (declared in `pyproject.toml`), plus every transitive
dep including `sam-tool-sdk`, into `python/`. The `[project.scripts]`
entry is dropped as a console script at `python/bin/<name>` — the path
the STR manifest's `executable:` field points at.

Two implications worth being explicit about:

- **To customise the build**, edit `build.sh` (and the matching
  `build.bat`) in place. The script's contract — populate
  `$SAM_TOOL_BUILD_OUT` with everything the runtime needs and copy
  `manifest.yaml` alongside — is the only thing the pipeline enforces.
- **To opt into the pure convention path**, delete `build.sh` and
  `build.bat`. With only `go.mod` (or `requirements.txt` /
  `pyproject.toml`) present, the resolver falls to the convention
  strategy and invokes the language toolchain directly via `os/exec`.
  Convention builds are slightly faster (no shell, no script parse) and
  cross-platform without a separate `.bat`, but you lose the
  customisation hook.

### Why `_sdk/` (not `vendor/`)?

Go treats a top-level `vendor/` directory
as a complete dependency snapshot and refuses to build unless every
transitive dependency is present there. Naming the directory `_sdk/`
sidesteps that, and the leading underscore additionally hides it from
`go build ./...` of any surrounding module.

### `sam toolset sync`

```
sam toolset sync [PATH] [--name NAME] [--lang go|python]
```

Re-vendors the embedded SDK into existing tools. PATH accepts the same
three forms as `init`; without `--name` a repo-wide invocation refreshes
every tool under `toolsets/*/src/`. `--name` restricts to a single tool;
`--lang` overrides the per-tool language detection (defaults to
inferring from `go.mod` or `pyproject.toml`).

Examples:

```
sam toolset sync                              # CWD → every toolsets/*/src/
sam toolset sync ./my-repo                    # repo root → every toolsets/*/src/
sam toolset sync ./my-repo/toolsets/pptx      # single toolset
sam toolset sync --name pptx                  # name-scoped refresh in a multi-toolset repo
```

`sync` always overwrites whatever is present — no diff check. Pair it
with a CLI upgrade to refresh stale vendored SDK copies in one shot.

## Resolution order

The resolver inspects the toolset source dir in this order. The first
match wins:

1. **Host-appropriate build script.**
   - On Mac/Linux: `build.sh`.
   - On Windows: `build.bat`.
   - If the **other** OS's script is the only one present (e.g. only
     `build.sh` on Windows), the resolver hard-errors with
     `toolset "foo" has build.sh but no build.bat — not buildable on windows`.
2. **Go convention.** A `go.mod` at the source dir root selects this
   strategy. The build runs `go build -o dist/<name>[.exe] <entry>`,
   where `<entry>` is `.` (a root-level `package main`) or
   `./cmd/<name>` when a single subdirectory under `cmd/` exists.
   Multiple `cmd/` subdirectories is a hard error — provide
   `build.sh` + `build.bat` to disambiguate.
3. **Python convention.** `requirements.txt` (preferred) or
   `pyproject.toml` selects this strategy. The build runs
   `pip install --target dist/<os>-<arch>/python --platform <target>
   --python-version <ver> --only-binary=:all: .` so the tool's own
   package, its declared deps, and `sam-tool-sdk` all land in
   `python/` as an AWS-Lambda-Layer-style bundle the STR can load
   directly. The `[project.scripts]` console-script entry is dropped at
   `dist/<os>-<arch>/python/bin/<name>`, with its shebang rewritten as
   described under "Python scripts in `python/bin/`" below.
4. **Pre-built artifact.** A non-source executable named `<name>` /
   `<name>.exe` at the source dir root with no Go/Python/script source
   alongside is bundled as-is.
5. **Fallback.** Anything else is bundled as-is. Useful for raw-asset toolsets that need no compilation.

## Build script contract

When a script-based plan runs, the resolver invokes it directly via
`os/exec` (no shell interpretation). The script receives:

- **Working directory:** the toolset source dir
  (`toolsets/<name>/src/`).
- **Env vars:**
  - `SAM_TOOL_NAME` — the toolset name (= the directory name under
    `toolsets/`).
  - `SAM_TOOL_BUILD_OUT` — absolute path to the per-target dist
    subdir (`toolsets/<name>/dist/<targetOS>-<targetArch>/`). The
    bundler zips this directory verbatim, so populate it with
    everything the runtime needs (binaries, vendored deps, config
    files, …). Always write to `$SAM_TOOL_BUILD_OUT` rather than a
    bare `dist/` — two builds for different targets in the same
    workspace must land in distinct subdirs or the second one
    overwrites the first.
  - `SAM_TOOL_TARGET_OS` / `SAM_TOOL_TARGET_ARCH` — the deployment
    target, **discovered by `sam config apply` from the platform the
    manifest points at** (the STR's actual runtime), not assumed by the
    script. Honour these in every cross-compile (`GOOS="$SAM_TOOL_TARGET_OS"
    GOARCH="$SAM_TOOL_TARGET_ARCH" go build …`) and do **not** default the
    arch — a wrong guess ships a binary the STR rejects at discovery with
    "exec format error". The build pipeline runs `file`/magic verification
    on the produced binary and refuses to cache one that doesn't match the
    requested target.
  - `SAM_TOOL_SDK_VERSION` — the samtoolsdk version when known.
  - `GOFLAGS` — your own value (exported or set with `go env -w`) with
    `-trimpath -buildvcs=false` appended, so a Go build hashes the same
    from any checkout directory.
- **Exec bit:** the script must be executable on Mac/Linux (`chmod +x`).
  The resolver does not chmod on your behalf.
- **Python scripts in `python/bin/`:** after every build, including
  a `build.sh`, each script there whose shebang names a Python
  interpreter (pip console scripts and your own) is rewritten to
  `#!/usr/bin/env python3`. Interpreter flags (`-u`, `-E`) and version
  pins (`python3.12`) are dropped. Every `python/*.dist-info/direct_url.json`
  is deleted and `RECORD` updated to match. The STR runs any
  `#!/usr/bin/env python*` script, `python2` included, under its
  resolved Python 3, so the bundle hashes the same from any checkout
  directory.
- **Exit code:** non-zero exits surface the last 50 lines of combined
  stdout/stderr in the error message.

Authoring `build.sh` / `build.bat` is the same trust boundary as
authoring tool code itself: both run with the privileges of the
authoring repo, and the produced `dist/` payload runs in the STR
sandbox at execution time.

## `dist/` output convention

Every strategy lands its outputs in
`<source-dir>/dist/<targetOS>-<targetArch>/`. The bundler zips that
per-target subdir deterministically (sorted entries, fixed mtime,
executable bit preserved on individual files) and uploads the zip —
never the raw source tree. Sibling target subdirs (e.g. a `darwin-arm64/`
left behind from a local laptop build) are ignored.

| Strategy | What lands in `dist/<os>-<arch>/` |
|---|---|
| `script` | Whatever the script writes to `$SAM_TOOL_BUILD_OUT`. Authoring contract is yours — but honour `SAM_TOOL_TARGET_OS`/`ARCH` in every cross-compile. |
| `go` | The single compiled binary (`dist/<os>-<arch>/<name>` or `dist/<os>-<arch>/<name>.exe`). Source files are NOT copied. |
| `python` | AWS-Lambda-Layer-style: `dist/<os>-<arch>/python/` contains the tool's own package, every transitive dep including `sam-tool-sdk`, and the `[project.scripts]` console-script entry at `python/bin/<name>`. The STR adds `dist/<os>-<arch>/python/` to `PYTHONPATH` at runtime and invokes the console-script binary directly. |

The bundler always wipes and re-creates the active per-target subdir
before running a build, so stale outputs from a prior strategy can't
sneak into the zip. Other target subdirs survive — switching between
`linux/arm64` and `darwin/arm64` builds in the same workspace is a fast
toggle, not a full rebuild.

## Cache

Built bundles are stored per-toolset AND per-target at
`toolsets/<name>/.sam-cache/build/<targetOS>-<targetArch>/<hash>.zip`,
where `<hash>` is computed from:

- The plan kind discriminator.
- The target tuple (`linux/arm64`, `darwin/arm64`, …) — so a hash
  computed for one target can never collide with another.
- Sorted bytes of every file under the source dir except outputs
  (`dist/`, `.sam-cache/`, `vendor/`, `.deps/`, `python/`, `_sdk/`)
  and the standard bundler exclusions (`.git`, `node_modules`,
  `__pycache__`, …).
- The build script bytes (`build.sh` + `build.bat` when present).
- The samtoolsdk version pinned in `go.mod` / `requirements.txt`.
- The CLI's bundle format version, so a CLI upgrade that changes what a
  build or the bundler produces rebuilds instead of serving an older zip.

Cache hits short-circuit the build step entirely. The plan output marks
them with `[BUILD: cache-hit <os>/<arch>]`. Cache misses run the build,
mark the op `[BUILD: built <os>/<arch> (Δs)]`, and write the resulting
zip back. Per-toolset isolation means deleting a toolset directory
removes its cache automatically; per-target segmentation means a local
`darwin/arm64` build can never be served against a `linux/arm64` apply
(the bug this layout was introduced to fix).

On the first cache write, the following patterns are auto-added to
`<repo-root>/.gitignore` so build outputs don't leak into git:

```
toolsets/*/.sam-cache/
toolsets/*/dist/
toolsets/*/src/vendor/
```

### Pruning

The cache never auto-prunes — predictability over magic. Run on demand:

```
sam config cache prune              # entries older than 30 days
sam config cache prune --max-age 168h
sam config cache prune --max-size 1073741824   # 1 GiB cap
sam config cache prune --all                   # wipe everything
```

`--all` is mutually exclusive with `--max-age` / `--max-size`.

## `--no-build` flag

For CI flows that build in a separate step, both plan and apply accept
`--no-build`:

| Subcommand | `--no-build` behavior |
|---|---|
| `sam config plan --no-build` | Skip builds. Cache hits resolve normally. Misses surface as `[BUILD: skipped — will run at apply]`. No error. |
| `sam config apply --no-build` | Strict: cache hits resolve normally; misses are a hard error. |

Typical CI pattern:

```sh
sam config plan         # cold: builds + populates each toolset's cache
# ... CI uploads toolsets/*/.sam-cache/build/ as a build artifact ...
sam config apply --no-build   # warm: cache hits resolve, no toolchains needed
```

`sam config plan --no-build` followed by `sam config apply` (without
`--no-build`) builds on apply — the apply path doesn't carry plan-time
decisions, so it re-resolves every toolset from scratch.

## Authoring pitfalls

These bite first-time authors. Reading order matters: the rules are
listed roughly in the order they manifest as visible failures.

### Optional vs required parameters

A parameter field is **optional** when it is either a pointer type OR
its `json:` tag carries `,omitempty`. Either signal alone is enough.

```go
// Pointer form. Branch on nil in the handler.
type FindUsersParams struct {
    Department *string `json:"department,omitempty"`
    Limit      *int    `json:"limit,omitempty"`
}

// Value-with-omitempty form. Branch on zero value in the handler;
// callers that wanted to send the zero value explicitly can't.
type FindUsersParams struct {
    Department string `json:"department,omitempty"`
    Limit      int    `json:"limit,omitempty"`
}
```

Both forms put the same fields in `properties` and leave them out of
`required` — the resulting JSON schema is identical. They differ in
two ways the SDK does care about:

1. **In your handler**, the pointer form lets you distinguish "caller
   omitted the field" (`nil`) from "caller explicitly sent the zero
   value" (`*p == ""` / `*p == 0`). The value form collapses both
   into the zero value. Prefer the pointer form when that distinction
   matters (e.g. `Limit=0` should mean "let the tool default" rather
   than "return 0 results").

2. **At `--schema` discovery time**, the value-with-omitempty form
   triggers an authoring warning (see below). The pointer form is
   silent.

A non-pointer field without `,omitempty` is **required**.

If you forget BOTH signals on a field that should be optional, the
STR rejects calls that omit it with `"Invoking <toolname>() failed as
the following mandatory input parameters are not present:
<field-list>"`. The quickest diagnostic is
`sam api /api/v2/platform/toolsets/<id> --jq '[.data.tools[] |
{name:.toolName, required:.parameters.required}]'` — anything that
shouldn't be required tells you which field needs a pointer or an
omitempty.

The SDK also emits a stderr warning at `--schema` discovery time when
the two signals disagree — a non-pointer field with `,omitempty` is
honoured as optional, but the warning surfaces the mismatch so you
can switch to a pointer if you wanted the stronger "this can be
nil-distinguished-from-zero" guarantee:

```
samtoolsdk: FindUsersParams.Department has json `,omitempty` but is
non-pointer (string); treating as optional. Use a pointer type (e.g.
*string) and drop `,omitempty` to silence this warning, or remove
`,omitempty` if the field should be required.
```

### Tool `Description` vs `WithInstructions`

`sdk.NewTool(name, description, …)` — the **description** (short
one-liner, second arg) is what the platform stores as the tool's
public DTO field and what the agent prompt's tool-selection table
shows. Keep it tight and accurate; it's the LLM's primary signal for
"which tool do I want." It is **required and must be non-empty**:
`sdk.NewTool` panics at registration (and the Python SDK raises at
discovery) on a blank description, and strict providers such as Amazon
Bedrock reject a tool advertised with an empty one.

`sdk.WithInstructions(text)` — the **instructions** are longer prose
guidance the agent surfaces alongside the tool. Useful for nuanced
"when to use this" notes, but historically the agent's prompt builder
has been observed to scramble these across tools in the rendered
prompt, so don't depend on instructions for correctness — make the
description + JSON schema self-sufficient.

### Returning data: `WithData` vs `WithDataObjects`

Tool handlers return `*sdk.Result` via one of the constructors
(`sdk.OK`, `sdk.Error`, `sdk.Partial`) plus optional `ResultOption`
modifiers. There are **two** ways to attach payload data, and the
choice matters for LLM context budget:

- **`sdk.WithData(map[string]any{...})`** — inline. Every key is
  spread at the top level of the JSON the agent feeds back to the
  LLM. Use this for **small, scalar summaries**: counts, flags, a
  single record, a rendered markdown snippet the LLM should reason
  over directly. Whatever you put here is paid for in tokens on
  every subsequent turn of the conversation.

- **`sdk.WithDataObjects(sdk.DataObject{...})`** — content items
  the agent may store as **versioned artifacts** instead of inlining.
  Use this for **bulky or binary payloads**: JSON trees, list/search
  results that can grow, images, generated documents. The LLM gets a
  small reference (`artifacts_created: [{filename, version, size_bytes,
  mime_type, description}]`) and can drill in later with the built-in
  artifact tools (`load_artifact`, `artifact_grep`,
  `transform_data_with_jmespath`, …) without re-fetching.

A `DataObject.Disposition` controls how the agent routes each one:

| Disposition | Behavior |
|---|---|
| `DispositionAuto` *(default if unset)* | Binary content, or text larger than the agent's `tool_result_auto_artifact_threshold_bytes` (default 8192), is saved as an artifact and the LLM gets a reference. Text at or below the threshold is shown inline and also saved. `tool_result_inline_truncation_bytes` (default 65536) is the absolute inline ceiling. |
| `DispositionArtifact` | Always save as artifact. Use for content the LLM never benefits from seeing inline (images, large blobs, anything the user-facing UI will render from storage). |
| `DispositionInline` | Always return content directly to the LLM as a string under `inline_outputs[].content`. Use sparingly — the artifact path is almost always better. |
| `DispositionArtifactWithPreview` | Save as artifact AND emit a short `preview` string the LLM sees alongside the reference. |

The SDK auto-detects binary content (non-UTF-8 bytes) and base64-encodes
on the wire — you pass raw `Content []byte`, the framework handles
encoding and `IsBinary` signalling for you. Don't base64-encode in your
handler.

**Hybrid pattern (recommended for any list/tree/large-record tool):**
attach small summary fields via `WithData` and the bulky payload via
`WithDataObjects(DispositionAuto)`. Counts and flags stay cheap to
glance at; the heavy payload only crosses the LLM context boundary
when it's actually small enough to be worth inlining.

```go
func getOrgChart(ctx context.Context, p Params, tc *sdk.ToolContext) (*sdk.Result, error) {
    tree, markdown, err := provider.getOrgChart(ctx, p.UserID)
    if err != nil {
        return sdk.Error("org chart failed: " + err.Error()), nil
    }
    if tree == nil {
        return sdk.OK("No directory record."), nil
    }
    treeJSON, err := json.Marshal(tree)
    if err != nil {
        return sdk.Error("encode tree: " + err.Error()), nil
    }
    return sdk.OK("Org chart retrieved.",
        // markdown is the rendered summary the LLM should reason over —
        // keep it inline so it shows up directly in the tool result.
        sdk.WithData(map[string]any{"markdown": markdown}),
        // The full JSON tree can be 50+ records deep; let the framework
        // promote it to an artifact when it crosses the auto-artifact threshold (64 KB by default).
        sdk.WithDataObjects(sdk.DataObject{
            Name:        "org_chart.json",
            Content:     treeJSON,
            MIMEType:    "application/json",
            Disposition: sdk.DispositionAuto,
            Description: "Org chart tree for " + p.UserID,
        }),
    ), nil
}
```

**Pure-artifact case** — for content that should never round-trip
through the LLM context (images, generated PDFs, large CSVs), use
`DispositionArtifact` so the size check is bypassed:

```go
return sdk.OK("Profile picture retrieved.",
    sdk.WithData(map[string]any{"mime_type": contentType}),
    sdk.WithDataObjects(sdk.DataObject{
        Name:        "profile_picture.jpg",
        Content:     rawImageBytes,   // raw bytes; SDK base64-encodes on wire
        MIMEType:    contentType,
        Disposition: sdk.DispositionArtifact,
        Description: "Profile picture for " + userID,
    }),
), nil
```

**Anti-pattern: serializing structured data into a string under
`WithData`.** If you find yourself doing
`sdk.WithData(map[string]any{"results": jsonString})` to make a
"compact" inline payload, that's the wrong shape — switch to
`WithDataObjects(...)` and let the framework decide.

### `WithDataObjects` is mandatory for data-driven payloads

Any tool whose response could plausibly be JSON, CSV, YAML, XML, or
any other structured/textual blob — and where the **size is driven
by data, not by a fixed shape** — MUST attach the body via
`WithDataObjects(... DispositionAuto)`, not `WithData`. This rule
covers:

- HTTP / REST wrappers (Salesforce, Jira, Datadog, custom services).
- SOQL / SQL / GraphQL query tools.
- File processors and converters that emit text.
- Tools that return list / search / describe / schema payloads.
- Anything where one call can return 50 records and the next returns
  50 000.

`WithData` for the body is a footgun: a single large response is
carried in every subsequent turn's prompt, up to the agent's
`tool_result_inline_truncation_bytes` ceiling (64 KB by default).
Past the ceiling the whole response is saved as a JSON artifact and
the LLM gets a reference plus the response's small fields, so it
must query the artifact for anything larger. `DispositionAuto` keeps
small responses inline (≤64 KB by default, after which the framework
promotes to an artifact) so you
get the right behavior on both ends of the size spectrum for free.

A small **summary** under `WithData` (status code, row count,
truncation flag, "next page" token) is fine — those are tiny scalars
the LLM should be able to branch on without a tool call. The bulk
payload belongs in the artifact.

### Let the LLM name its artifacts

Tools that produce artifacts MUST expose `output_filename` (or
`output_filename_prefix` for multi-file tools) and `description` as
optional parameters. A tool that hard-codes a single filename —
`query_result.json` for every SOQL call, `response.json` for every
REST call — gives the LLM no way to tell results apart in a
multi-call turn: every write lands on the same name, only the
version number changes, and `«artifact_content:query_result.json»`
silently resolves to "whichever was last". The fix is one tiny
schema addition:

```go
type Params struct {
    // ... operation-specific fields ...

    OutputFilename *string `json:"output_filename,omitempty" desc:"Filename for the response artifact, including extension (.json/.csv/.md/...). Pick something specific to this call (e.g. 'open_p1_cases.json', 'case_12345_with_comments.json') so you can reference it later via «artifact_content:...». Default: derived from the request shape."`
    Description    *string `json:"description,omitempty" desc:"Short human-readable description of what's in this artifact. Surfaced in the user's artifact list and in the artifact's metadata."`
}
```

Wire it through in the handler:

```go
name := defaultArtifactName(...)
if p.OutputFilename != nil && *p.OutputFilename != "" {
    name = *p.OutputFilename
}
description := fmt.Sprintf("...sensible default...")
if p.Description != nil && *p.Description != "" {
    description = *p.Description
}
return sdk.OK("...",
    sdk.WithDataObjects(sdk.DataObject{
        Name:        name,
        Content:     body,
        MIMEType:    "application/json",
        Disposition: sdk.DispositionAuto,
        Description: description,
    }),
), nil
```

Light validation belongs in the handler: reject `..` and `/`, cap
length at ~80 characters, ensure the extension matches the actual
content type. The artifact store versions same-named writes, so a
reused name is safe — but the *point* of letting the LLM choose is
that it stops doing that on its own.

For tools that produce **multiple artifacts per call** (e.g. a
multi-object query that fans out into one artifact per record type,
an archive extractor, a multi-file generator), expose
`output_filename_prefix` instead:

```go
OutputFilenamePrefix *string `json:"output_filename_prefix,omitempty" desc:"Prefix for the artifact filenames this call produces. The tool appends a stable per-output suffix (e.g. '<prefix>_cases.json', '<prefix>_contacts.json'). Default: derived from the call."`
```

The handler is responsible for the suffix scheme — pick one that's
stable across runs and self-explanatory in a listing.

### Poisoned build cache

`sam config plan/apply` keys the build cache by a hash of (plan kind,
target tuple, source files, build script bytes, SDK version). A cache
hit short-circuits the build entirely (`[BUILD: cache-hit <os>/<arch>]`).
If a build was once produced with a stale SDK (or with a `build.sh`
that ignored `SAM_TOOL_TARGET_*` on an older CLI) that bad zip
stays in the cache and the cache-hit keeps re-uploading it.

Cross-target poisoning specifically is prevented by segmenting both
the cache path (`.sam-cache/build/<os>-<arch>/<hash>.zip`) and the
`dist/` output (`dist/<os>-<arch>/<binary>`) by the target. A
darwin/arm64 build can no longer overwrite a linux/arm64 entry — they
live in different subdirectories. The `[BUILD: cache-hit darwin/arm64]`
line surfaces the target so an operator notices when the wrong
platform's binary is being reused.

Symptoms: platform `discoveryStatus: failed` with `exec format error`
after a `[BUILD: cache-hit]` run (only possible now if the toolset was
ever built on an old CLI without the segmented layout); or new
tools you registered in `main.go` are missing from `--schema` output
of the cached binary.

Cure: bust the cache for that toolset, then re-apply.

```bash
rm -rf toolsets/<name>/.sam-cache/build/
sam config apply --manifest manifests/dev.yaml
```

### Re-uploading a toolset that's already in use

Re-uploading a toolset that is referenced by deployed agents overwrites
the stored package and auto-redeploys every affected agent via the
outbox publisher (the redeploy is async; the first attempt may
transiently fail with `tool package not ready` while discovery is
pending, followed by a retry that succeeds — both are normal and the
apply returns success once the upload itself lands). The platform does
not gate the overwrite; confirmation lives in the web UI, which prompts
before reimporting an in-use toolset.

`sam config apply` re-uploads only when it detects the bundle changed: it
diffs the local bundle's SHA-256 against the platform-stored content hash,
so any content change re-uploads and a byte-identical bundle is a true
no-op (no size-collision false-negative, no force flag needed).

### STR per-invocation lifetime — no cross-call cache

Every STR tool dispatch spawns a fresh worker process. Any
`sync.Map`, in-memory struct cache, or "lazy init once" you wire into
your tool's `main()` lives for **exactly one tool call**. If your
tool pays a meaningful warm-up cost (large directory pull, expensive
connection setup, big in-memory index), every call pays it from
cold.

There is currently no SDK-provided cross-invocation cache. Workarounds
authors have used: persist the snapshot to a temp file under
`/tmp/sam-str/<toolset>/...` with a timestamp, then on next start
reuse the snapshot when it's young enough; or design the API so the
first tool call fetches and subsequent calls in the same conversation
take filter/page parameters instead. Be aware of the limitation when
choosing how to shape your tools.

### Skill bundles vs STR toolsets

A `kind: skill` can attach **built-in** tools via its SKILL.md
`tools:` frontmatter, and uploaded Secure Tool Runtime toolsets via its
`toolsets:` frontmatter — a flat list of toolset names, expanded into
that toolset's tools when the skill is published. Each listed toolset
must already be uploaded to the platform.

Alternatively, list the toolset in the **agent's** `toolsets:` block so
every conversation with that agent gets it. Either way the skill body
can name the resulting tools (`<toolset>__<tool>`, double-underscore
separator — see [skill design](./design/skill-design.md)) as prompt
guidance.

## Python tool authoring

Python tools are written against the `sam-tool-sdk` package (resolved from
PyPI via `pyproject.toml`, not vendored). This section is the Python
counterpart to the Go authoring guidance above — same concepts (`OK` /
`Error`, inline data vs artifacts, config schema, timeouts), expressed in
the Python API.

### Entry point: `tool_cli`

A function-based tool is a plain function (sync or async) wrapped with
`tool_cli`, exposed as a console script. STR runs the script with `--schema`
at discovery time and with a `runner_args.json` path at invocation time —
`tool_cli` handles both.

```python
# weather_tool.py
from sam_tool_sdk import tool_cli, ToolResult, SandboxToolContextFacade


async def get_weather(city: str, units: str = "metric", ctx: SandboxToolContextFacade = None) -> ToolResult:
    """Look up the current weather for a city.

    Args:
        city: the city to look up
        units: "metric" or "imperial"
    """
    if ctx is not None:
        ctx.send_status(f"Fetching weather for {city}...")
    # ... call a weather API ...
    return ToolResult.ok(message=f"Weather for {city}", data={"tempC": 21})


cli = tool_cli(get_weather)
```

```toml
# pyproject.toml
[project]
name = "sam-tool-weather"
version = "0.1.0"
dependencies = ["sam-tool-sdk>=0.1,<0.2"]

[project.scripts]
weather = "weather_tool:cli"          # manifest executable: python/bin/weather
```

### Schema derivation from type hints

`tool_cli` builds the tool's JSON schema by introspecting the function
signature — you do **not** hand-write the schema for function tools:

- Each annotated parameter becomes a schema property. Supported types:
  `str` → string, `int` → integer, `float` → number, `bool` → boolean,
  `list[X]` → array, `dict` → object, and nested `TypedDict` /
  `@dataclass` / pydantic `BaseModel` → nested object schemas.
- A parameter with a default value is **optional**; one without is
  **required** (the Python analog of Go's pointer / `,omitempty` rule).
- Per-parameter descriptions come from the docstring's `Args:` block.
- A parameter annotated `SandboxToolContextFacade` is **injected by the
  framework**, not exposed to the LLM — name it whatever you like.
- A parameter annotated `Artifact` (or `list[Artifact]`) is pre-loaded by
  STR before the call (see Artifacts below).

When inference isn't enough (dynamic enums, runtime-computed shapes), use
`@with_dynamic_schema(fn)` to supply the schema explicitly, the same
escape hatch as Go's `WithDynamicSchema`.

### Returning results: `ToolResult`

Handlers return `ToolResult` via a classmethod constructor — the Python
mirror of Go's `sdk.OK` / `sdk.Error` / `sdk.Partial` / `sdk.Pending`:

| Constructor | Use |
|---|---|
| `ToolResult.ok(message, data=, data_objects=)` | success |
| `ToolResult.error(message, code=, data=)` | failure the LLM should see |
| `ToolResult.partial(message, data=, data_objects=)` | partial success |
| `ToolResult.pending(message, ...)` | long-running / deferred |
| `ToolResult.auth_required(message, ...)` | tool needs credentials first |

Returning a plain `dict` is also accepted (legacy-compatible) but
`ToolResult` is preferred — it's the only way to attach artifacts.

`data=` is **inline** — spread into the JSON the LLM sees every turn, so
keep it to small scalar summaries (the Python analog of `WithData`).
`data_objects=` is for **bulky/binary payloads** the agent may store as
versioned artifacts (`WithDataObjects`):

```python
from sam_tool_sdk import ToolResult, DataObject, DataDisposition

return ToolResult.ok(
    message="Generated report",
    data={"rowCount": 1042},                       # inline scalar summary
    data_objects=[
        DataObject(
            name="report.csv",
            content=csv_bytes,
            mime_type="text/csv",
            disposition=DataDisposition.ARTIFACT,   # AUTO / ARTIFACT / INLINE / ARTIFACT_WITH_PREVIEW
            description="Full report",
        )
    ],
)
```

`DataDisposition` semantics match the Go `Disposition` table above:
`AUTO` (framework decides by size/binary), `ARTIFACT` (always store),
`INLINE` (always return to the LLM), `ARTIFACT_WITH_PREVIEW` (store +
short preview string).

### The context facade

A `SandboxToolContextFacade`-typed parameter gives the tool runtime
access:

- `ctx.send_status(text)` — emit a streaming status line to the user.
- `ctx.call_llm(system_prompt, user_prompt, temperature=)` — call back to
  the agent's LLM (synchronous; raises `IPCError` if STR provided no IPC
  socket for this invocation).
- `await ctx.load_artifact(filename, version=, as_text=)` and
  `await ctx.list_artifacts()` are async; `ctx.save_artifact(name, content,
  ...)` is synchronous.
- `ctx.get_config(key, default)` — read resolved operator config.
- `ctx.session_id`, `ctx.user_id`, `ctx.app_name`, `ctx.task_id`.

Note: `ctx.send_signal(...)` is **not supported** in the STR sandbox and
raises — use `send_status` for user-visible progress.

### Artifacts as parameters

Annotate a parameter `Artifact` to have STR pre-load the named artifact's
bytes before your function runs:

```python
from sam_tool_sdk import Artifact, ToolResult

async def summarize(doc: Artifact) -> ToolResult:
    """Summarize an uploaded document."""
    text = doc.as_text()                       # bytes already loaded by STR
    return ToolResult.ok(message=f"Summarized {doc.filename}", data={"chars": len(text)})
```

### Operator config: `@with_config_schema`

Declare config the operator supplies (API keys, endpoints) — rendered as
a form by the platform, validated at deploy time. Wire-compatible with
Go's `ConfigSchemaField`:

```python
from sam_tool_sdk import with_config_schema, ConfigSchemaField

@with_config_schema([
    ConfigSchemaField(key="api_token", type="string", required=True, secret=True),
    ConfigSchemaField(key="endpoint", type="string"),
])
async def call_api(tool_config, query: str) -> ToolResult:
    token = tool_config["api_token"]           # injected; precedence: agent > toolset > schema default
    ...
```

### Timeouts

```python
from sam_tool_sdk import tool_timeout

@tool_timeout(seconds=120)                                       # per-invocation timeout
async def long_job(ctx=None) -> ToolResult:
    ...
```

### Class-based and provider tools

For multiple related tools or full control over the schema, subclass
`DynamicTool` (one tool) or `DynamicToolProvider` + `@register_tool`
(many). A `DynamicTool` hand-writes `parameters_schema` as a JSON Schema
dict:

```python
from sam_tool_sdk import DynamicTool, DynamicToolProvider, ToolResult

class CountWords(DynamicTool):
    @property
    def tool_name(self): return "count_words"
    @property
    def tool_description(self): return "Count words in text."
    @property
    def parameters_schema(self):
        return {"type": "object",
                "properties": {"text": {"type": "string"}},
                "required": ["text"]}
    async def _run_async_impl(self, args, tool_context=None, credential=None):
        return ToolResult.ok(message="counted", data={"n": len(args["text"].split())})

# provider — many tools from one class, each via @register_tool
class MathTools(DynamicToolProvider):
    def create_tools(self, tool_config=None): return []   # decorated tools auto-included

@MathTools.register_tool
async def add(self, a: float, b: float) -> dict:
    """Add two numbers."""
    return {"sum": a + b}
```

Wrap the class/provider for the console script with `dynamic_tool_cli(...)`
or `provider_cli(...)` instead of `tool_cli(...)`.

### Migrating an existing Python Agent Mesh tool

A tool written for the full `solace_agent_mesh` package needs a small,
mechanical rewrite — typically 30–60 minutes, no logic redesign:

1. **Imports** — `from solace_agent_mesh.agent.tools.dynamic_tool import
   DynamicTool` → `from sam_tool_sdk import DynamicTool`. Same for
   `ToolResult`, `DataObject`, etc. There is no compatibility shim; the
   import path must change.
2. **`parameters_schema`** — return a plain JSON Schema dict instead of a
   `google.genai.types.Schema` object:
   ```python
   # old
   return adk_types.Schema(type=adk_types.Type.OBJECT,
                           properties={"q": adk_types.Schema(type=adk_types.Type.STRING)},
                           required=["q"])
   # new
   return {"type": "object", "properties": {"q": {"type": "string"}}, "required": ["q"]}
   ```
3. **Context** — `ToolContextFacade` → `SandboxToolContextFacade`. Method
   names (`send_status`, `load_artifact`, `get_config`, …) are unchanged.
   The one behavioral change: `send_signal(...)` now raises rather than
   delivering — switch signal-based progress to `send_status`.
4. **Returns** — a tool that returned a raw `dict` keeps working; adopt
   `ToolResult` only if you want artifact handling.

The on-disk/runner contract (`runner_args.json`, `result.json`, status
pipe) is unchanged, so once imports + schema are updated and the tool is
repackaged into the Lambda-Layer layout, the Go STR runs it unmodified.

## Cross-platform notes

- Mac/Linux toolsets that don't need Windows can ship just `build.sh`.
- Windows-supporting toolsets must ship **both** `build.sh` and
  `build.bat` (one per host).
- Convention-based builds (Go and Python) work on every host without
  shell scripts because the resolver invokes the language toolchain
  directly via `os/exec`.

## Cross-compile target

The deployed STR is almost always a different architecture from the
author's dev machine. `sam config apply` handles this automatically for
**both Go and Python** convention toolsets by asking the platform for the
toolset build target and building for the returned `(GOOS, GOARCH)`. The arch is a property of the deployment, so it is
**discovered, never guessed**. Resolution order:

1. `SAM_TOOL_TARGET_OS` / `SAM_TOOL_TARGET_ARCH` env vars (explicit pin wins).
2. Platform endpoint (the STR's actual reported runtime).

There is no silent default. If the platform is unreachable, or it reports
no STR runtime yet (response `source: "fallback"`, i.e. no STR has
announced its arch), `sam config apply` **fails hard** rather than guess a
target — a wrong guess builds cleanly and then fails at STR discovery with
"exec format error". Wait for an STR to connect, or pin
`SAM_TOOL_TARGET_OS` / `SAM_TOOL_TARGET_ARCH` explicitly and rerun.

Go convention cross-compiles via `GOOS`/`GOARCH`/`CGO_ENABLED=0`. Python
convention passes the equivalent pip flags
(`--platform manylinux2014_aarch64`, `--python-version`,
`--only-binary=:all:`) so a Mac build laptop produces manylinux wheels
the linux STR can load. Pure-Python tools with no compiled deps are
arch-independent either way.

A mixed-fleet response (multiple distinct STR architectures online) is a
hard apply error citing the observed targets — pin one explicitly via
the env vars and rerun. The resolved `SAM_TOOL_TARGET_OS` /
`SAM_TOOL_TARGET_ARCH` are also passed through to author-supplied
`build.sh` / `build.bat` so custom builds can honour them.

For builds outside `sam config apply` (hand-rolled `go build`, CI),
`sam toolset build-target --url <url>` prints the resolved target
in `text` (`linux/arm64`), `--format json`, or `--format shell` form. The
shell form is `eval`-friendly:

```bash
eval "$(sam toolset build-target --url https://platform.example.com --format shell)"
go build -o tools/mytool/dist/mytool ./tools/mytool/source/...
```

**Arch mismatch is the most common reason tool discovery fails in
production** — symptom is a platform `discoveryStatus=failed` with
`"binary cannot run on this host: it's Mach-O 64-bit ... but STR is
running on linux/arm64"`. If `sam config apply` is doing the build, the
CLI picks the right arch automatically; otherwise, run
`sam toolset build-target` and feed the result to your build.

## Worked examples

### A tiny Go toolset

```
toolsets/customer-tools.yaml      # kind: toolset, name: customer-tools
toolsets/customer-tools/
  src/
    go.mod
    main.go                       # package main, samtoolsdk-driven
```

Plan output:

```
* CREATE  toolset/customer-tools  [BUILD: built linux/arm64 (1.2s)]    customer-tools.yaml
```

A second `sam config plan` with no source changes:

```
= UPDATE  toolset/customer-tools  [BUILD: cache-hit linux/arm64]  customer-tools.yaml
```

### A tiny Python toolset

```
toolsets/web-tools.yaml
toolsets/web-tools/
  src/
    pyproject.toml                # sam-tool-sdk>=0.1,<0.2 + requests
                                  # [project.scripts] web-tools = "web_tools:cli"
    src/web_tools/
      __init__.py
      __main__.py                 # cli = tool_cli(fetch_url)
    build.sh                      # cross-arch pip install --target dist/.../python
    manifest.yaml                 # executable: python/bin/web-tools
```

Plan output (cold):

```
* CREATE  toolset/web-tools  [BUILD: built linux/arm64 (3.8s)]   web-tools.yaml
```

`toolsets/web-tools/dist/linux-arm64/` after build contains
`python/requests/...`, `python/sam_tool_sdk/...`, and `python/bin/<name>`
(the console-script entry declared in `pyproject.toml` under
`[project.scripts]`). The whole `python/` tree is on the STR's
PYTHONPATH at runtime.

### A custom build with `build.sh` + `build.bat`

```
toolsets/exotic-tool.yaml
toolsets/exotic-tool/
  src/
    build.sh                      # uses cargo, custom node toolchain, etc.
    build.bat                     # Windows equivalent
    ...                           # whatever the scripts know how to build
```

The script is responsible for populating `toolsets/exotic-tool/dist/`.
The resolver runs the host-appropriate script and zips whatever lands.

### A pulled (mirror-flow) toolset — no build

```
toolsets/billing-tools.yaml       # metadata from `sam config pull`
toolsets/billing-tools/
  billing-tools.zip               # bundle bytes from `sam config pull`
```

Plan output:

```
* CREATE  toolset/billing-tools   [BUILD: none]   billing-tools.yaml
```

The pre-built zip is uploaded verbatim — the build pipeline is skipped
entirely. This is the canonical state of a freshly-pulled toolset.
