# `third_party/`

This directory holds **vendored copies of external Go libraries** that we have decided to bring in-tree rather than consume as remote module dependencies.

This README is the canonical home of the project's vendoring policy — the rules below apply to anything added under `third_party/`.

## Currently vendored

| Library | Pinned version | Reason | Provenance |
|---------|---------------|--------|------------|
| `github.com/jmespath/go-jmespath` | v0.4.0 | Upstream dormant; small static codebase; security-sensitive (evaluates expressions against artifact data) | [jmespath/PROVENANCE.md](jmespath/PROVENANCE.md) |

## Replaced with Solace-written code

These directories keep an upstream module path but hold no upstream code. They are not vendored copies, so the vendoring procedure below does not apply to them.

| Module path | Reason | Provenance |
|-------------|--------|------------|
| `github.com/bytedance/sonic` | Keeps ByteDance code out of every binary; implements only what Bifrost calls, on `encoding/json` | [sonic/PROVENANCE.md](sonic/PROVENANCE.md) |

## Layout convention

Each vendored library lives in its own subdirectory with this structure:

```
third_party/<libname>/
  PROVENANCE.md   ← upstream URL, pinned commit, audit notes, local mod log
  LICENSE         ← copied verbatim from upstream
  go.mod          ← module path matches upstream so `replace` works transparently
  *.go            ← source files (audit before merging)
```

## How vendored libs are wired into the build

The root `go.mod` keeps the original `require` line so CVE scanners (govulncheck, Dependabot, GitHub Advisory) match advisories against the dependency. A `replace` directive redirects the build to the local copy:

```go
require github.com/example/lib v1.2.3
replace github.com/example/lib => ./third_party/lib
```

Consumer code keeps `import "github.com/example/lib"` unchanged. The replaced module needs its own `go.mod` (in the vendored directory) with a `module` line that matches the upstream module path verbatim.

## Vendoring Policy

We vendor selectively — only when supply-chain isolation outweighs the loss of upstream maintenance.

### When to vendor

A dependency is a candidate for vendoring (via `go.mod` `replace` to `third_party/`) when **all** of the following hold:

- **Security-sensitive use**: the library handles untrusted input, parses or evaluates expressions, or sits in an authentication/cryptography path.
- **Auditable size**: source is small enough to read in one sitting (target: under ~5k LOC of non-test Go).
- **Minimal transitive surface**: zero or near-zero runtime dependencies beyond the Go stdlib.
- **Stable spec/behavior**: the underlying functionality is frozen or extremely slow-moving — we don't need to track upstream features.
- **Supply-chain risk is the actual driver**, not "feels abandoned." Dormant ≠ vulnerable.

### When NOT to vendor

- Large libraries (driver SDKs, framework runtimes, parser/codec stacks with many files).
- Libraries with deep transitive dependency trees — vendoring one pulls in the rest.
- Libraries under active feature development we want to track.
- Libraries with very large user bases (AWS SDK, gRPC, protobuf) where supply-chain risk is already broadly mitigated by widespread use.

### Procedure

1. **Pin** to a specific tag and capture the commit SHA. Document both in `PROVENANCE.md`.
2. **Use `replace`, not import-path renaming** — keep the original `require` line in the root `go.mod` so CVE scanners continue to match advisories.
3. **Copy** the source plus `LICENSE` into `third_party/<libname>/`. Create a per-lib `go.mod` whose module path matches upstream verbatim (this is what makes `replace` resolve).
4. **Skip** test files, CLI binaries, fuzz targets, and vendored dev dependencies (testify, etc.) unless there's a specific reason to include them.
5. **Add a Dependabot ignore rule** for version updates on the vendored module (`.github/dependabot.yml`); leave security updates active.
6. **Audit pass**: read every line of the copied source. Document findings in `PROVENANCE.md` (imports, dangerous patterns checked, conclusion).
7. **Add a row to the "Currently vendored" table above** linking to the new `PROVENANCE.md`.

### Maintenance rules

- **On a CVE alert**: read the advisory, check whether the vulnerable code path is reachable from our usage, patch the local copy if needed, append a row to the "Local modifications" table in the lib's `PROVENANCE.md`. If the scanner needs a version bump to clear the alert, change the `require` line to a synthetic suffixed version (e.g., `v0.4.0-local.1`); the `replace` keeps the build pointing at the local copy.
- **No automatic upstream syncing.** Once vendored, the code is owned. Watching upstream is optional and human-driven.
- **Re-audit** any time the local copy is modified beyond a CVE patch (e.g., bug fixes ported from upstream).
- **Removal**: if a vendored library can be replaced by a better-maintained alternative or removed entirely, delete the directory, drop the `require` and `replace` lines, run `go mod tidy`, and remove the row from the "Currently vendored" table.
