# `github.com/bytedance/sonic`: Solace-written stand-in

## Why this exists

Bifrost (`github.com/maximhq/bifrost/core`, our LLM client) imports the ByteDance JSON library `github.com/bytedance/sonic`, which pulled ByteDance and CloudWeGo modules (`bytedance/gopkg`, `sonic/loader`, `cloudwego/base64x`, `twitchyliquid64/golang-asm`) into every shipped binary, although SAM's own code never imported it.

This directory is a module that **declares the same module path** and implements the symbols Bifrost calls on top of Go's `encoding/json`. The root `go.mod` points the path here with a `replace` directive, so Bifrost compiles unchanged and no ByteDance or CloudWeGo code is built into SAM.

## What it contains

- Written by Solace from scratch, using Bifrost's call sites and the `encoding/json` documentation. It contains **no code, tests or copyright text from the original library**, so there is no upstream `LICENSE` to carry.
- Standard library imports only (`bytes`, `encoding/json`, `errors`, `fmt`, `io`). The module has no dependencies.
- The original stays visible by name only. `go version -m <binary>` shows `github.com/bytedance/sonic <upstream version> => ./third_party/sonic`, the `require` line in the root `go.mod` stays because Bifrost declares it, and `go list -m all` and `go mod graph` still name the original's own requirements (`bytedance/gopkg`, `sonic/loader`, `cloudwego/base64x`, `twitchyliquid64/golang-asm`, `golang.org/x/arch`). None of them is downloaded or built.
- The `replace` applies only when this repository is the main module. A program outside it that imports `pkg/tools`, the one public package that reaches Bifrost, builds the original library.

This is not a vendored copy, so the copy, pin and audit steps of the [vendoring policy](../README.md#vendoring-policy) do not apply. Advisories filed against `bytedance/sonic` do not apply either; dismiss them with a link to this file. The Dependabot `ignore` entry stops version updates only, so a security-update PR also needs dismissing by hand.

## Surface

| Symbol | Behaviour |
|--------|-----------|
| `Marshal`, `MarshalString`, `MarshalIndent` | `encoding/json` with HTML escaping off |
| `Unmarshal`, `UnmarshalString` | `encoding/json`; numbers into `any` decode as `float64` |
| `Valid` | `json.Valid` |
| `Config{EscapeHTML, SortMapKeys, UseInt64}.Froze()` | returns an `API` with `Marshal`, `MarshalIndent`, `Unmarshal` |
| `ConfigDefault`, `ConfigStd` | HTML escaping off and on |
| `Get`, `GetFromString` | value at an object-key path, read up to the first matching key |
| `Node` | `Exists`, `Raw` (source text), `String` |
| `ErrNotExist`, `ErrUnsupportedType` | missing path; `String` on an object or array |
| `NoCopyRawMessage` | raw JSON value |

Known differences from the original library:

- Invalid UTF-8 in a string becomes U+FFFD when encoding and when decoding, as in `encoding/json`; the original passes it through when decoding. The original writes the raw character; `encoding/json` wrote the `\ufffd` escape on darwin/arm64 and the raw character on linux/amd64 in CI. Bifrost's own `TestOrderedMapMarshal_InvalidUTF8` expects the raw form.
- `Marshal` escapes U+2028 and U+2029, compacts `json.Marshaler` and `RawMessage` output, and writes negative zero as `-0`.
- `Unmarshal` and `Valid` reject raw control characters inside strings.
- Map keys are always sorted; `SortMapKeys` is accepted but has no effect.
- `UseInt64` applies only when the target is a `*any`, the only way Bifrost uses it. Integers that do not fit in `int64` decode as `float64`; numbers outside the `float64` range are an error.
- `NoCopyRawMessage` copies its input, because `encoding/json` reuses its buffer.
- `Get` path elements must be object keys (strings). Like the original, it stops at the first matching key, so the first duplicate wins and a document cut off after the key still yields it. Malformed JSON before the key is an error.

## Extending it when Bifrost changes

A Bifrost upgrade that calls a new sonic symbol fails to compile, so nothing ships silently. To fix it:

1. List what the new Bifrost version uses. The first command finds package-level symbols, the second any aliased or subpackage import; methods called on `Config.Froze()` values show up only as build errors.
   ```bash
   bifrost="$(go list -m -f '{{.Dir}}' github.com/maximhq/bifrost/core)"
   grep -rhoE 'sonic\.[A-Za-z_]+' --include='*.go' "$bifrost" | sort | uniq -c
   grep -rhE '"github.com/bytedance/[^"]*"' --include='*.go' "$bifrost" | sort | uniq -c
   ```
2. Implement the missing symbol on `encoding/json` and add a test row in `sonic_test.go`. Never copy code from the original library.
3. Update the surface table above, then run `make check-sonic` from the repository root.
4. Run Bifrost's own tests against this module from the repository root, where the `replace` applies: `go test github.com/maximhq/bifrost/core/providers/...`. Against Bifrost v1.15.2 the expected failures are `TestOrderedMapMarshal_InvalidUTF8` (see the UTF-8 difference above) and the Mistral test package, which calls `ConfigDefault.NewDecoder`, a test-only symbol this module does not provide, plus any test that needs network access or credentials.

If a new symbol cannot be built reasonably on `encoding/json`, the fallback is a generated copy of Bifrost with its sonic calls rewritten to `encoding/json`.

## Local modifications

| Date | Change |
|------|--------|
| 2026-11-19 | Initial version, covering Bifrost v1.15.2 |
