# SAM Go Tool SDK

Standalone Go SDK for developing tools that run in the Solace Agent Mesh Secure Tool Runtime (STR).

Tool authors import this package to define tools with typed parameter structs, artifact handling, status updates, LLM callbacks, and structured results. Tools are compiled into standalone binaries that the STR spawns as subprocesses.

## Quick Start

```go
package main

import (
    "context"
    sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type GreetParams struct {
    Name string `json:"name" desc:"The person to greet"`
}

func greet(ctx context.Context, p GreetParams, tc *sdk.ToolContext) (*sdk.Result, error) {
    tc.SendStatus("Greeting " + p.Name + "...")
    return sdk.OK("Hello, " + p.Name), nil
}

func main() {
    sdk.Run(sdk.NewTool("greet", "Greets a person", greet))
}
```

> The `description` (second arg) is **required and must be non-empty**. It is the
> LLM's primary signal for choosing the tool, and strict providers such as Amazon
> Bedrock reject a tool advertised with an empty description. `NewTool` panics at
> registration if it is blank.

Build and run:

```bash
go build -o my-tool .
./my-tool --schema            # outputs JSON schema for all tools
```

Register in your STR manifest:

```yaml
version: 1
tools:
  my_tools:
    runtime: go
    executable: ./bin/my-tool
```

The STR automatically discovers all tools in the binary via `--schema`.

## Multi-Tool Binaries

A single binary can host multiple tools:

```go
func main() {
    sdk.Run(
        sdk.NewTool("greet", "Greets a person", greet),
        sdk.NewTool("farewell", "Says goodbye", farewell),
        sdk.NewTool("translate", "Translates text using LLM", translate,
            sdk.WithInstructions("Use this for translation tasks")),
    )
}
```

The `WithInstructions` option injects text into the LLM's system prompt when the tool is available.

## Parameter Structs

Tool parameters are defined as Go structs. Struct tags drive JSON Schema generation:

```go
type SearchParams struct {
    Query   string   `json:"query" desc:"Search query"`
    Limit   *int     `json:"limit" desc:"Max results (optional)"`
    Tags    []string `json:"tags" desc:"Filter tags"`
    Verbose bool     `json:"verbose" desc:"Include details"`
}
```

### Rules

| Go Type | JSON Schema | Required? |
|---------|-------------|-----------|
| `string` | `"string"` | Yes (non-pointer) |
| `*string` | `"string"` | No (pointer = optional) |
| `int`, `int32`, `int64` | `"integer"` | Yes |
| `float32`, `float64` | `"number"` | Yes |
| `bool` | `"boolean"` | Yes |
| `[]T` | `"array"` | Yes |
| `map[string]T` | `"object"` | Yes |
| Nested struct | `"object"` (recursive) | Yes |

### Tags

- `json:"name"` — JSON property name (standard Go)
- `desc:"..."` — LLM-visible description
- Non-pointer fields are required; pointer fields are optional
- A slice, map or interface field tagged `json:",omitempty"` is optional too, since its nil value is an unambiguous "absent"

### Making a parameter optional

Use a pointer type (`*string`, `*int`, …) and nil-check it in your handler:

```go
type StyleParams struct {
    CardStyle *string `json:"card_style" desc:"Optional; omit to inherit the default"`
}

if p.CardStyle != nil {
    use(*p.CardStyle)
}
```

Do **not** reach for `json:",omitempty"` on a non-pointer scalar to signal
optionality. `,omitempty` is an *output* encoding hint (skip the zero value when
marshaling); it says nothing about whether input is required, and for a scalar
the zero value (`""`, `0`, `false`) is indistinguishable from "absent". The SDK
treats such a field as optional but prints a startup warning on every tool and
CLI invocation:

```
samtoolsdk: StyleParams.CardStyle has json `,omitempty` but is non-pointer (string); treating as optional...
```

The pointer form silences the warning and gives your handler a real
present/absent distinction.

## Artifacts

Artifacts are files managed by the SAM artifact store. When a parameter is typed as `Artifact`, the framework automatically pre-loads the file content before calling your handler.

```go
type ProcessParams struct {
    Input    sdk.Artifact   `json:"input" desc:"Input file to process"`
    Template *sdk.Artifact  `json:"template" desc:"Optional template"`
    Sources  []sdk.Artifact `json:"sources" desc:"Multiple source files"`
}

func process(ctx context.Context, p ProcessParams, tc *sdk.ToolContext) (*sdk.Result, error) {
    // p.Input is pre-loaded with Content, Filename, MIMEType, etc.
    text := p.Input.AsText()

    // p.Template may be nil if the user didn't provide one
    if p.Template != nil {
        text = strings.ReplaceAll(p.Template.AsText(), "{{input}}", text)
    }

    // p.Sources is a slice of loaded artifacts
    for _, src := range p.Sources {
        fmt.Printf("Source: %s (%d bytes)\n", src.Filename, len(src.Content))
    }

    return sdk.OK("Processed " + p.Input.Filename), nil
}
```

### How It Works

1. The LLM sees artifact parameters as strings (filenames)
2. The STR pre-loads the artifact files into the sandbox work directory
3. The SDK reads them and builds `Artifact` objects before calling your handler
4. Your handler receives ready-to-use objects with content and metadata

### Artifact Type

```go
type Artifact struct {
    Content  []byte         // Raw file content
    Filename string         // Original filename
    Version  int            // Artifact version
    MIMEType string         // e.g., "text/plain", "image/png"
    Metadata map[string]any // Additional metadata
}

func (a *Artifact) AsText() string  // Content as UTF-8 string
func (a *Artifact) AsBytes() []byte // Raw bytes
```

### Schema Translation

| Go Type | LLM Sees | Required? |
|---------|----------|-----------|
| `Artifact` | `string` (filename) | Yes |
| `*Artifact` | `string` (filename) | No |
| `[]Artifact` | `array` of strings | Yes |

## ToolContext

The `ToolContext` is injected as the third parameter to your handler. It provides runtime services:

```go
func myTool(ctx context.Context, p Params, tc *sdk.ToolContext) (*sdk.Result, error) {
    // Identity
    fmt.Println(tc.UserID)    // Authenticated user
    fmt.Println(tc.SessionID) // Current session
    fmt.Println(tc.AppName)   // Agent name
    profile := tc.UserProfile() // Gateway-forwarded profile; empty map if none
    fmt.Println(profile["id"])  // "id" and "work_email" are pinned to the JWT; other keys are unsigned

    // Status updates (sent to the user/agent)
    tc.SendStatus("Step 1: Loading data...")
    tc.SendStatus("Step 2: Processing...")

    // Tool configuration (from YAML tool_config)
    apiKey, ok := tc.GetConfig("api_key")
    dbURL := tc.GetConfigString("db_url", "localhost:5432")

    // LLM calls (proxied through the STR)
    response, err := tc.CallLLM(ctx,
        "You are a translator",
        "Translate 'hello' to French",
        0.3, // temperature
    )

    // Save output artifacts
    tc.SaveArtifact("output.txt", []byte("result data"), "text/plain")

    return sdk.OK("Done"), nil
}
```

### Methods

| Method | Description |
|--------|-------------|
| `SendStatus(message string) error` | Send a progress update to the user |
| `GetConfig(key string) (any, bool)` | Get a tool config value |
| `GetConfigString(key, default string) string` | Get a string config value with default |
| `UserProfile() map[string]any` | Authenticated user's gateway-forwarded profile. Only `id` and `work_email` are pinned to the JWT; use for scoping, context and audit, not authorization |
| `CallLLM(ctx, system, user string, temp float64) (string, error)` | Make an LLM call via STR |
| `SaveArtifact(filename string, content []byte, mimeType string) error` | Write an output artifact |
| `SearchTurn() (int, bool)` | The search turn the agent assigned this call, for numbering citations; false when none was assigned |

### LLM Callbacks

Tools can make LLM calls that are proxied through the STR's IPC server. This allows tools to use AI capabilities without needing direct access to LLM API keys:

```go
func summarize(ctx context.Context, p SummarizeParams, tc *sdk.ToolContext) (*sdk.Result, error) {
    content := p.Document.AsText()

    summary, err := tc.CallLLM(ctx,
        "You are a document summarizer. Be concise.",
        "Summarize the following document:\n\n" + content,
        0.3,
    )
    if err != nil {
        return sdk.Error("LLM call failed: " + err.Error()), nil
    }

    return sdk.OK(summary), nil
}
```

LLM calls require the STR to be configured with an LLM service. If not available, `CallLLM` returns an error.

## Result Types

All handlers return `*sdk.Result`. Use the constructors:

```go
// Success
sdk.OK("Operation completed")
sdk.OK("Found results", sdk.WithData(map[string]any{"count": 42}))

// Error
sdk.Error("File not found")
sdk.Error("Access denied", sdk.WithErrorCode("PERMISSION_DENIED"))

// Partial (some operations succeeded)
sdk.Partial("3 of 5 items processed")
```

### Result Options

```go
sdk.WithData(map[string]any{...})          // Inline data for the LLM
sdk.WithDataObjects(obj1, obj2)            // Content objects (see below)
sdk.WithErrorCode("MACHINE_READABLE_CODE") // Programmatic error code
sdk.WithSignals(sdk.Signal{...})           // Citations for the user interface, never shown to the LLM
```

`WithSignals` is for web search tools: the agent assigns a search turn only to a tool named `web_search` or `web_search_<suffix>`, and publishes only a `rag_info_update` numbered with that turn (`ToolContext.SearchTurn()`). Send one signal per call with all of the call's sources:

```go
turn, ok := tc.SearchTurn()
if ok {
    opts = append(opts, sdk.WithSignals(sdk.Signal{Type: sdk.SignalRAGInfoUpdate, Data: map[string]any{
        "query": query, "search_type": "web_search", "search_turn": turn, "is_complete": true,
        "sources": []map[string]any{{
            "citationId": fmt.Sprintf("s%dr0", turn), "title": "Page title",
            "sourceUrl": "https://example.com/page", "contentPreview": "snippet",
            "metadata": map[string]any{"title": "Page title", "link": "https://example.com/page"},
        }},
    }}))
}
```

The agent decodes the data strictly: the envelope keys are snake_case and the source keys camelCase, citation ids must be `s<turn>r<n>` (or `img<turn>r<n>` for an image), metadata holds only the string keys `title`, `type`, `link` and `imageUrl`, and every URL must be absolute http or https without credentials and at most 2048 characters. Any other key or value drops the whole signal, and the agent logs why.

### DataObjects

For producing files or large content:

```go
result := sdk.OK("Generated report", sdk.WithDataObjects(
    sdk.DataObject{
        Name:        "report.pdf",
        Content:     pdfBytes,
        MIMEType:    "application/pdf",
        Disposition: sdk.DispositionArtifact,
        Description: "Monthly report",
    },
    sdk.DataObject{
        Name:        "summary.txt",
        Content:     []byte("Key findings..."),
        MIMEType:    "text/plain",
        Disposition: sdk.DispositionArtifactWithPreview,
        Preview:     "Key findings: revenue up 15%...",
    },
))
```

### Dispositions

| Disposition | Behavior |
|-------------|----------|
| `DispositionAuto` | Framework decides based on size/type |
| `DispositionArtifact` | Always store as artifact, return reference |
| `DispositionInline` | Return content directly to the LLM |
| `DispositionArtifactWithPreview` | Store as artifact + include preview |

## Schema Output

Run your binary with `--schema` to output tool metadata:

```bash
./my-tool --schema
```

Output:

```json
{
  "tools": {
    "greet": {
      "description": "Greets a person",
      "parameters": {
        "type": "object",
        "properties": {
          "name": {"type": "string", "description": "The person to greet"}
        },
        "required": ["name"]
      },
      "artifact_params": {},
      "instructions": ""
    }
  }
}
```

The STR uses this to discover tools and validate invocations.

## Manifest Configuration

### Auto-Discovery (Recommended)

A single manifest entry discovers all tools from a binary:

```yaml
version: 1
tools:
  my_go_tools:
    runtime: go
    executable: ./bin/my-tools
    timeout_seconds: 60
    sandbox_profile: standard
```

The STR runs `./bin/my-tools --schema` at startup to discover all tools.

### Configuration Options

| Field | Description | Default |
|-------|-------------|---------|
| `runtime` | Must be `"go"` | `"python"` |
| `executable` | Path to compiled binary | (required) |
| `timeout_seconds` | Per-tool timeout override | 300 |
| `sandbox_profile` | `"restrictive"`, `"standard"`, `"permissive"` | `"standard"` |
| `effect` | `"read_only"` or `"side_effecting"`, for a binary whose `--schema` works but declares no effect; any other value skips the tool in a flat manifest and counts as `"side_effecting"` in a package manifest. When the manifest and the binary's `--schema` both declare it, the stricter value applies. A manifest `"read_only"` is not trusted when `--schema` fails, omits the tool or cannot run on this host: the tool is then `"side_effecting"` and the runtime logs a warning | undeclared (treated as `"side_effecting"`) |

### Declaring a Tool's Effect

Declare whether each tool is read_only or side_effecting, with
`sdk.WithEffect(sdk.EffectReadOnly)` or `sdk.WithEffect(sdk.EffectSideEffecting)`
on `sdk.NewTool`. A tool is read_only only if all three hold: it cannot change anything outside the run, whatever its input; it fetches no URL chosen by its input or by the content of a file it reads; it writes, deletes or replaces no artifact under a filename chosen by its caller or by the tool itself. The run is the one task that called the tool, and changing something outside it means writing, sending, deleting, posting, publishing or indexing in an external system, another session or user scope, or starting a job. Changing the run's own session, such as the skills it has loaded, is not outside it. A filename is chosen by the caller when it comes from a parameter, and by the tool when its code fixes or derives it, including a name set on a data object it returns. A result the runtime itself saves under a fresh filename it picks, which it does only when a tool returns data with no name set (auto-disposition), is not a write. Sending the caller's query to one fixed external service, as a search or lookup does, is not fetching a chosen URL. Every other tool is side_effecting. It is emitted as `effect` in the `--schema` output. A tool that
declares nothing is treated as side-effecting, and an unknown value panics at
registration. Declare it in code or in the manifest, not both.

## Building and Deploying

```bash
# Build your tool
go build -o bin/my-tool ./cmd/my-tool/

# Verify schema output
./bin/my-tool --schema

# Add to STR manifest
cat >> manifest.yaml << 'EOF'
  my_tool:
    runtime: go
    executable: ./bin/my-tool
EOF
```

The STR spawns Go binaries as subprocesses (just like Python tools). Recompiling the binary is sufficient for hot-reload — the next invocation uses the new binary automatically.

## Execution Flow

```
Agent calls tool "greet"
    |
    v
Broker routes to STR (subscribe: invoke/greet)
    |
    v
STR Worker receives message
    |-- Authenticates (enterprise mode)
    |-- Authorizes tool access
    |
    v
SandboxRunner.Execute()
    |-- Creates work directory: /tmp/sam-str-work/str-{instance}/w-{random}/
    |   |-- input/   (pre-loaded artifacts)
    |   |-- output/  (tool-produced artifacts)
    |   |-- tmp/     (the call's TMPDIR, on disk, removed with the work dir)
    |   |-- home/    (the call's HOME under bwrap)
    |-- Creates status.pipe (named FIFO)
    |-- Creates ipc.sock (Unix socket for LLM callbacks)
    |-- Writes runner_args.json
    |-- Spawns: ./my-tool runner_args.json
    |
    v
Go Tool SDK (in subprocess)
    |-- Reads runner_args.json
    |-- Looks up tool by tool_name
    |-- Pre-loads artifacts from input/ dir
    |-- Builds ToolContext (status pipe, IPC client, config)
    |-- Deserializes args into parameter struct
    |-- Calls handler function
    |-- Extracts DataObjects to output/ dir
    |-- Writes result.json
    |
    v
STR collects result
    |-- Reads result.json
    |-- Collects output artifacts from output/ dir
    |-- Publishes response to agent
```

## Comparison with Python SDK

| Aspect | Python SDK | Go SDK |
|--------|-----------|--------|
| Define tool | Function / class / provider | Struct params + handler function |
| Schema | Type annotations auto-generate | Struct tags auto-generate |
| Artifacts | `Artifact` type annotation | `sdk.Artifact` field type |
| Context | `SandboxToolContextFacade` | `*sdk.ToolContext` |
| Results | `ToolResult.ok(msg, data=...)` | `sdk.OK(msg, sdk.WithData(...))` |
| Status | `ctx.send_status(msg)` | `tc.SendStatus(msg)` |
| Config | `ctx.get_config(key)` | `tc.GetConfig(key)` |
| User profile | `ctx.user_profile` | `tc.UserProfile()` |
| LLM calls | Not supported in sandbox | `tc.CallLLM(ctx, ...)` |
| Build | None (interpreted) | `go build -o binary` |
| Deploy | Drop .py + manifest | Drop binary + manifest |
