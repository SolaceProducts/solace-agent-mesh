# jmespath/go-jmespath — vendored copy

## Source

- **Upstream:** https://github.com/jmespath/go-jmespath
- **License:** Apache-2.0 (see [LICENSE](LICENSE))
- **Pinned tag:** `v0.4.0`
- **Pinned commit:** `3d4fd11601ddca248480565884e34e393313cd62` (2020-09-18)
- **Date copied into this repo:** 2026-04-30
- **Copied by:** an Agent Mesh maintainer

## Why this is vendored

The upstream library is functionally complete and dormant — last meaningful release was v0.4.0 (2020), and the JMESPath language spec itself is stable. The dependency-review pass (PR #303) flagged it as a maintenance/supply-chain risk because there is no active upstream for security patches.

Rather than swap to the community fork (`github.com/jmespath-community/go-jmespath`, ~15 stars, also small bus factor), we vendored the original source into this repo. The decision criteria, procedure, and ongoing rules are documented in [`third_party/README.md`](../README.md#vendoring-policy).

The corresponding work item is GitHub issue #307.

## What was copied

Core source only — 8 Go files plus `LICENSE`:

| File | LOC | Purpose |
|------|-----|---------|
| `api.go` | 49 | Public `Search`, `Compile`, `MustCompile` entry points |
| `lexer.go` | 420 | JMESPath expression tokenizer |
| `parser.go` | 603 | AST parser |
| `interpreter.go` | 418 | AST evaluator |
| `functions.go` | 841 | Built-in JMESPath functions (length, sort, map, etc.) |
| `util.go` | 185 | Reflection helpers (`IsFalse`, `ObjsEqual`) |
| `astnodetype_string.go` | 45 | Generated stringer for AST node types |
| `toktype_string.go` | (53) | Generated stringer for token types |

**Skipped from upstream:**

- `internal/testify/` — vendored testify, only needed for the original repo's own tests
- `cmd/jpgo/` — CLI binary, unused
- `fuzz/` — fuzz target, unused
- All `*_test.go` files — we don't run upstream's tests; the JMESPath spec is frozen and our integration tests exercise `transform_data_with_jmespath` end-to-end

## Local modifications

None as of the initial copy. The `go.mod` was rewritten to drop the testify dev dependency (only used by skipped tests) and to declare `go 1.26`.

Any future modification — including CVE patches — must be appended below with date, author, and rationale.

| Date | Author | Change | Rationale |
|------|--------|--------|-----------|
| _none yet_ | | | |

## Audit notes (2026-04-30, initial copy)

Pre-merge audit by an Agent Mesh maintainer. Scope: all 8 copied source files (~2,549 LOC).

**Imports — stdlib only:**
`bytes`, `encoding/json`, `errors`, `fmt`, `math`, `reflect`, `sort`, `strconv`, `strings`, `unicode`, `unicode/utf8`. No third-party imports.

**Dangerous-pattern scan (all clean):**

| Pattern | Result |
|---------|--------|
| `unsafe` package | none |
| Network I/O (`net/http`, `net.*`) | none |
| File/OS I/O (`os.Open`, `ioutil.*`, `os/exec`, `syscall`) | none |
| `unsafe.Pointer`, `//go:linkname` | none |
| `regexp.MustCompile`, `text/template`, `html/template` | none |

**Reflection use:** the interpreter and `util.go` use `reflect` to introspect arbitrary Go values being evaluated against expressions. This is intrinsic to a generic JMESPath interpreter (the library has to walk `any`-typed maps and slices) and is read-only — no `reflect.Value.Call`, no field-setting via reflection.

**Built-in functions registered** (`functions.go`): `abs`, `avg`, `ceil`, `contains`, `ends_with`, `floor`, `join`, `keys`, `length`, `map`, `max`, `max_by`, `merge`, `min`, `min_by`, `not_null`, `reverse`, `sort`, `sort_by`, `starts_with`, `sum`, `to_array`, `to_number`, `to_string`, `type`, `values`. All are pure data operations — none escape the in-memory evaluation context.

**Verdict:** safe to vendor as-is. The library evaluates expressions against in-memory Go values and returns results; it has no capability to perform I/O, execute code, or escape its evaluation sandbox.

## Audit notes (2026-04-30, deeper review)

A second-pass audit (full read of `api.go`, `lexer.go`, `parser.go`, `interpreter.go`, `functions.go`, `util.go`) surfaced three real availability bugs and one information-disclosure path. The bugs are in upstream library code, not in our use of it — they would equally affect any consumer. They are not RCE or data-exfiltration vectors, but they are reachable from realistic input shapes given that both the JMESPath expression (LLM-controlled) and the data (user-uploaded artifact) are untrusted in our deployment.

### Findings

**H-1, H-2 — Unbounded recursion in parser and interpreter** (`parser.go` ~lines 145-414, `interpreter.go` Execute). The Pratt parser recurses through `nud` for `(`, `!`, `&`, and several productions, with no depth tracking. The interpreter recurses on AST depth in `Execute`. An expression of the shape `((((...))))` of length ~1M produces a stack-overflow which Go's runtime cannot recover — `recover()` does not catch a stack-overflow fatal error.

**H-4 — `contains(haystack, value)` panics on non-comparable types** (`functions.go:454-459`). The implementation uses Go's `==` on `interface{}` values. When the haystack contains slice or map elements, `==` panics with "comparing uncomparable type". Reachable from realistic data: `{"a":[[1,2],[3,4]]}` plus expression `contains(a, [1,2])`. Unlike H-1/H-2, this panic *can* be caught with `recover()`.

**M-1 — Type-mismatch errors echo user data verbatim** (`functions.go:384` — `fmt.Errorf("Invalid type for: %v, expected: %#v", arg, ...)`). Propagated to the caller; an attacker who can prompt the LLM to evaluate `abs(creds.api_key)` against an artifact containing a credential will see the credential printed back in the tool's error message.

**Lower-severity findings** (not addressed in code, listed for record): three places in `parser.go` silently swallow errors (`parser.go:369, 469, 473`); `sort_by` mutates the input slice in place; a few minor numeric/Unicode edge cases. None are reachable as security bugs from our call site.

### Mitigations applied at the call site

The audit's recommended mitigations are implemented in [`pkg/tools/transform_data_jmespath.go`](../../pkg/tools/transform_data_jmespath.go) rather than by patching the vendored library:

1. **Expression length cap** — `maxJMESPathExpressionLen = 4096` bytes. Rejects pathological inputs before they reach the parser. Addresses H-1.
2. **Expression depth cap** — `maxJMESPathExpressionDepth = 32`. Tracks simultaneous open brackets and prefix unary operators; rejects above the cap. Addresses H-1 and H-2.
3. **`recover()` around evaluation** — `safeJMESPathSearch` wraps the evaluation in a deferred recover and converts any panic into a tool error. Addresses H-4 and any other latent panic vectors.
4. **Compile/eval split + error sanitization** — compile errors (which describe the expression text, not the data) are surfaced verbatim; evaluation errors are replaced with a generic "failed during evaluation" message that omits the library's verbatim data echoing. Addresses M-1.

Tests for each mitigation live in `pkg/tools/transform_data_jmespath_test.go` (`TestTransformJMESPath_ExpressionTooLong`, `TestTransformJMESPath_ExpressionTooDeep`, `TestTransformJMESPath_PanicRecovered`, `TestTransformJMESPath_RuntimeErrorDoesNotEchoData`).

### Why not patch the vendored library

Patching the vendored source would give us cleaner diagnostics (e.g., a structured "depth exceeded" error from inside the parser), but it would also create a maintenance debt — every CVE patch we apply going forward needs to be merged against our local modifications. Keeping the vendored copy bit-identical to upstream v0.4.0 means future audits stay simple ("does the diff against upstream v0.4.0 match what's in the Local Modifications table?"). All four findings are practically defeated by the call-site guards above, so there's no operational benefit to in-tree patching.

## How this is wired up

The root `go.mod` keeps the original `require github.com/jmespath/go-jmespath v0.4.0` line so CVE scanners (govulncheck, Dependabot, GitHub Advisory) continue to match advisories against this dependency. A `replace` directive redirects the build to this local copy:

```go
replace github.com/jmespath/go-jmespath => ./third_party/jmespath
```

Consumer code in [pkg/tools/transform_data_jmespath.go](../../pkg/tools/transform_data_jmespath.go) keeps `import "github.com/jmespath/go-jmespath"` unchanged.

## On a CVE alert

1. Read the advisory and identify the vulnerable function/codepath.
2. Check whether our local copy contains it (it will, since we copied v0.4.0 verbatim).
3. Patch the local copy in this directory. Add a row to the "Local modifications" table above.
4. If the scanner needs a version bump to clear the alert, change the `require` line in the root `go.mod` to a synthetic suffixed version (e.g. `v0.4.0-local.1`) — the `replace` keeps the build pointing here.

No upstream pulls — this is owned code from the moment of vendoring forward.
