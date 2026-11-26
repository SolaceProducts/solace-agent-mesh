# samtoolsdk Examples

Runnable reference tools. Each subdirectory is a standalone `package main` binary that compiles with `CGO_ENABLED=0`.

| Example | Pattern demonstrated |
|---|---|
| [`arithmetic/`](arithmetic/) | Typed `int`/`float64` parameters, status updates, `WithData` result |
| [`artifact-transform/`](artifact-transform/) | Read input `sdk.Artifact`, write output via `DispositionArtifact`; optional `*sdk.Artifact` |
| [`config-driven/`](config-driven/) | `WithConfigSchema`, `WithDynamicSchema`, `tc.GetConfigString` |
| [`multi-tool/`](multi-tool/) | Multiple `sdk.NewTool` calls in one `sdk.Run` |

## Build all examples

```bash
for d in pkg/samtoolsdk/examples/*/; do
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /dev/null "./$d" \
        && echo "OK  $d" \
        || echo "FAIL $d"
done
```

## Run schema discovery

```bash
go build -o /tmp/arithmetic ./pkg/samtoolsdk/examples/arithmetic/
/tmp/arithmetic --schema
```

## Key patterns

**Typed params** — define a struct; non-pointer fields are required, pointer fields are optional:

```go
type Params struct {
    Required string  `json:"required" desc:"always present"`
    Optional *string `json:"optional" desc:"may be nil"`
}
```

**Artifact input** — use `sdk.Artifact` (or `*sdk.Artifact`/`[]sdk.Artifact`); the SDK pre-loads content before the handler runs.

**Artifact output** — return a `sdk.DataObject` with `Disposition: sdk.DispositionArtifact`.

**Config** — declare fields with `sdk.WithConfigSchema`; read them with `tc.GetConfigString(key, default)`.

**Dynamic schema** — register a `sdk.WithDynamicSchema` function to adjust description/name based on the per-agent `tool_config` at `--schema` time.

**Multiple tools** — pass all `sdk.NewTool` results to a single `sdk.Run` call; the STR dispatches via `tool_name` in `runner_args.json`.
