// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/sdkvendor"
)

func writeGoSkeleton(dest, name, version string) error {
	files := map[string]struct {
		body string
		mode os.FileMode
	}{
		"main.go":       {body: renderGoMain(name), mode: 0o644},
		"go.mod":        {body: renderGoMod(name), mode: 0o644},
		"build.sh":      {body: renderGoBuildSh(name), mode: 0o755},
		"build.bat":     {body: renderGoBuildBat(name), mode: 0o644},
		"manifest.yaml": {body: renderGoManifestYAML(name), mode: 0o644},
		"README.md":     {body: renderGoReadme(name, version), mode: 0o644},
		".gitignore":    {body: "dist/\n" + sdkvendor.VendorDirName() + "/\n", mode: 0o644},
	}
	return writeFiles(dest, files)
}

func writePythonSkeleton(dest, name, version string) error {
	pyName := sanitizePythonModule(name)
	srcDir := filepath.Join("src", pyName)
	files := map[string]struct {
		body string
		mode os.FileMode
	}{
		"pyproject.toml":                     {body: renderPyProject(name, pyName, version), mode: 0o644},
		filepath.Join(srcDir, "__init__.py"): {body: renderPyInit(name), mode: 0o644},
		filepath.Join(srcDir, "__main__.py"): {body: renderPyMain(), mode: 0o644},
		"build.sh":                           {body: renderPyBuildSh(name), mode: 0o755},
		"build.bat":                          {body: renderPyBuildBat(name), mode: 0o644},
		"manifest.yaml":                      {body: renderPyManifestYAML(name), mode: 0o644},
		"README.md":                          {body: renderPyReadme(name, version), mode: 0o644},
		".gitignore":                         {body: "dist/\n.venv/\n__pycache__/\n*.egg-info/\n", mode: 0o644},
	}
	return writeFiles(dest, files)
}

func writeFiles(dest string, files map[string]struct {
	body string
	mode os.FileMode
}) error {
	for rel, f := range files {
		full := filepath.Join(dest, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(f.body), f.mode); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}
	return nil
}

func renderGoMain(name string) string {
	return `package main

import (
	"context"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type GreetParams struct {
	Name string ` + "`json:\"name\" desc:\"Name to greet\"`" + `
}

func greet(ctx context.Context, p GreetParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	_ = ctx
	tc.SendStatus("greeting " + p.Name)
	return sdk.OK("Hello, " + p.Name), nil
}

func main() {
	sdk.Run(
		sdk.NewTool("` + sanitizeToolKey(name) + `_greet",
			"Greets the named person",
			greet,
			sdk.WithEffect(sdk.EffectReadOnly),
		),
	)
}
`
}

func renderGoMod(name string) string {
	replacePath := "./" + sdkvendor.VendorDirName() + "/" + sdkvendor.GoSDKDirName()
	return `module ` + sanitizeGoModule(name) + `

go 1.26

require github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk v0.0.0

// TODO: drop the replace once samtoolsdk is consumable as a stable module.
replace github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk => ` + replacePath + `
`
}

func renderGoBuildSh(name string) string {
	return `#!/usr/bin/env bash
# Builds the tool binary into dist/. Invoked by sam config apply via the
# configapply build pipeline. SAM_TOOL_BUILD_OUT and SAM_TOOL_NAME come
# from the pipeline; SAM_TOOL_SDK_VERSION is informational.
#
# Cross-compile to the STR runtime target. The arch is a property of the
# deployment, not this toolset, so we never default it: a wrong guess
# ships a binary the STR rejects with "exec format error" at discovery.
# sam config apply discovers the target from the platform the manifest
# points at and exports SAM_TOOL_TARGET_OS / SAM_TOOL_TARGET_ARCH; for a
# manual build, export both (e.g. SAM_TOOL_TARGET_OS=linux
# SAM_TOOL_TARGET_ARCH=amd64 ./build.sh).
set -euo pipefail

OUT_DIR="${SAM_TOOL_BUILD_OUT:-dist}"
NAME="${SAM_TOOL_NAME:-` + name + `}"
: "${SAM_TOOL_TARGET_OS:?must be set by sam config apply, or exported for a manual build (e.g. linux)}"
: "${SAM_TOOL_TARGET_ARCH:?must be set by sam config apply, or exported for a manual build (e.g. amd64)}"
TARGET_OS="$SAM_TOOL_TARGET_OS"
TARGET_ARCH="$SAM_TOOL_TARGET_ARCH"

# Windows runs a binary only when its name ends in .exe.
BIN="$NAME"
if [ "$TARGET_OS" = "windows" ]; then
  BIN="$NAME.exe"
fi

mkdir -p "$OUT_DIR"

CGO_ENABLED=0 GOOS="$TARGET_OS" GOARCH="$TARGET_ARCH" \
  go build -trimpath -buildvcs=false -o "$OUT_DIR/$BIN" .

# Drop the manifest into dist/ so the STR can discover the tools.
cp manifest.yaml "$OUT_DIR/manifest.yaml"
`
}

func renderGoBuildBat(name string) string {
	return `@echo off
REM Windows build script. See build.sh for the Mac/Linux equivalent.
REM Cross-compiles to SAM_TOOL_TARGET_OS / SAM_TOOL_TARGET_ARCH. The arch
REM is a property of the deployment, not this toolset, so it is never
REM defaulted: sam config apply discovers it from the target platform and
REM exports it. For a manual build, set both env vars first.
setlocal
if "%SAM_TOOL_BUILD_OUT%"=="" set SAM_TOOL_BUILD_OUT=dist
if "%SAM_TOOL_NAME%"=="" set SAM_TOOL_NAME=` + name + `
if "%SAM_TOOL_TARGET_OS%"=="" ( echo SAM_TOOL_TARGET_OS must be set by sam config apply, or exported for a manual build ^(e.g. linux^) 1>&2 & exit /b 1 )
if "%SAM_TOOL_TARGET_ARCH%"=="" ( echo SAM_TOOL_TARGET_ARCH must be set by sam config apply, or exported for a manual build ^(e.g. amd64^) 1>&2 & exit /b 1 )

if not exist "%SAM_TOOL_BUILD_OUT%" mkdir "%SAM_TOOL_BUILD_OUT%"
set CGO_ENABLED=0
set GOOS=%SAM_TOOL_TARGET_OS%
set GOARCH=%SAM_TOOL_TARGET_ARCH%

REM Windows runs a binary only when its name ends in .exe.
set BIN=%SAM_TOOL_NAME%
if /I "%SAM_TOOL_TARGET_OS%"=="windows" set BIN=%SAM_TOOL_NAME%.exe

go build -trimpath -buildvcs=false -o "%SAM_TOOL_BUILD_OUT%\%BIN%" .
if errorlevel 1 exit /b %errorlevel%

copy /Y manifest.yaml "%SAM_TOOL_BUILD_OUT%\manifest.yaml" >nul
`
}

func renderGoManifestYAML(name string) string {
	return `version: 1
tools:
  ` + sanitizeToolKey(name) + `_tools:
    runtime: go
    executable: ./` + name + `
    timeout_seconds: 60
`
}

func renderPyManifestYAML(name string) string {
	return `version: 1
tools:
  ` + sanitizeToolKey(name) + `_tools:
    runtime: python
    executable: python/bin/` + name + `
    timeout_seconds: 60
`
}

func renderGoReadme(name, version string) string {
	vendorDir := sdkvendor.VendorDirName() + "/" + sdkvendor.GoSDKDirName()
	return `# ` + name + `

Scaffolded by ` + "`sam toolset init`" + ` (SAM CLI version ` + version + `).

## Authoring

To have your AI coding assistant write the tool with the correct
` + "`samtoolsdk`" + ` API, install the Agent Mesh authoring skills once at your
repo root:

` + "```" + `bash
sam ai-assistance skill install                 # OpenAI Codex CLI
sam ai-assistance skill install --target claude # Claude Code
` + "```" + `

Then edit ` + "`main.go`" + `, replacing the scaffolded ` + "`greet`" + ` example
with your tool.

## Build

` + "```" + `bash
./build.sh
` + "```" + `

The Go SDK is vendored at ` + "`" + vendorDir + "/`" + ` and wired in via
` + "`go.mod`" + `'s ` + "`replace`" + ` directive — no ` + "`go get`" + ` step is needed.
(The directory is named ` + "`" + sdkvendor.VendorDirName() + "`" + ` rather than the conventional
` + "`vendor/`" + ` so Go does not switch to vendor-mode for the whole tool.)

Run ` + "`sam toolset sync`" + ` to refresh the vendored SDK after
upgrading the CLI.

## Layout

` + "```" + `
toolsets/` + name + `.yaml            # kind: toolset metadata header
toolsets/` + name + `/
  src/                             # this directory
    main.go                        # tool entry point (uses samtoolsdk)
    go.mod                         # replace samtoolsdk => ./` + vendorDir + `
    ` + vendorDir + `/              # vendored SDK source (gitignored by default)
    build.sh / .bat                # cross-platform build scripts
    manifest.yaml                  # STR registration
  dist/                            # build output (gitignored)
  .sam-cache/                      # build cache (gitignored)
` + "```" + `
`
}

func renderPyProject(name, pyName, version string) string {
	return `[build-system]
requires = ["hatchling"]
build-backend = "hatchling.build"

[project]
name = "` + pyName + `"
version = "0.1.0"
description = "SAM tool scaffold (` + name + `)"
requires-python = ">=3.10"
dependencies = [
    "sam-tool-sdk>=0.1,<0.2",
]

[project.scripts]
` + name + ` = "` + pyName + `:cli"

[tool.hatch.build.targets.wheel]
packages = ["src/` + pyName + `"]

[tool.uv]
# uv before 0.9.17 ignores a relative exclude-newer and would lock without the window.
required-version = ">=0.9.17"
# A release has to be a week old before uv will resolve to it, so a
# compromised version is usually pulled before it reaches this lockfile.
exclude-newer = "7 days"

# SAM CLI version that scaffolded this tool: ` + version + `
`
}

func renderPyInit(_ string) string {
	return `"""SAM tool package — re-exports the tool_cli entry point."""

from .__main__ import cli

__all__ = ["cli"]
`
}

func renderPyMain() string {
	return `"""Scaffold for a SAM Python tool. Implement greet() below.

The contract:
- A function decorated/wrapped with ` + "`tool_cli`" + ` becomes the tool's
  schema-discoverable entry point.
- Annotate parameters with types; the SDK reflects these into a JSON
  schema the agent reads via --schema.
- Annotate the docstring's Args section to give each parameter a
  human-readable description (shown to the LLM).
- Annotate one parameter as ` + "`SandboxToolContextFacade`" + ` to get
  access to send_status / load_artifact / call_llm — the SDK injects it
  at runtime and excludes it from the schema.

See ` + "`internal/str/testdata/python-test-tool/tool.py`" + ` (in the
sam-go repo) for the lower-level runner_args / result.json protocol
reference if you need it.
"""

from sam_tool_sdk import SandboxToolContextFacade, ToolResult, tool_cli


def greet(name: str, ctx: SandboxToolContextFacade) -> ToolResult:
    """Greet the named person.

    Args:
        name: Name of the person to greet.
    """
    ctx.send_status(f"greeting {name}")
    return ToolResult.ok(f"Hello, {name}")


cli = tool_cli(greet)


if __name__ == "__main__":
    cli()
`
}

func renderPyBuildSh(name string) string {
	return `#!/usr/bin/env bash
# Builds the Python tool bundle in AWS-Lambda-Layer style. The pip-install
# step downloads wheels for the deployment's target platform. That target
# is a property of the deployment, not this toolset, so it is never
# defaulted: sam config apply discovers it from the platform the manifest
# points at and exports SAM_TOOL_TARGET_OS / SAM_TOOL_TARGET_ARCH; for a
# manual build, export both first.
set -euo pipefail

OUT_DIR="${SAM_TOOL_BUILD_OUT:-dist}"
: "${SAM_TOOL_TARGET_OS:?must be set by sam config apply, or exported for a manual build (e.g. linux)}"
: "${SAM_TOOL_TARGET_ARCH:?must be set by sam config apply, or exported for a manual build (e.g. amd64)}"
TARGET_OS="$SAM_TOOL_TARGET_OS"
TARGET_ARCH="$SAM_TOOL_TARGET_ARCH"
# Default tracks the STR runtime container's Python (3.11). Native wheels
# built for another version (e.g. 3.13) fail to load at discovery time.
PYTHON_VERSION="${SAM_TOOL_PYTHON_VERSION:-3.11}"

HOST_OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$HOST_OS" in
  darwin) HOST_OS=darwin ;;
  linux)  HOST_OS=linux ;;
  *)      HOST_OS=unknown ;;
esac
HOST_ARCH="$(uname -m)"
case "$HOST_ARCH" in
  x86_64|amd64)  HOST_ARCH=amd64 ;;
  arm64|aarch64) HOST_ARCH=arm64 ;;
  *)             HOST_ARCH=unknown ;;
esac
HOST_PYTHON_VERSION="$(python3 --version 2>&1 | sed 's/Python \([0-9]*\.[0-9]*\).*/\1/')"

PLATFORM_ARGS=()
# Apply --platform when OS/arch differ OR when the host python minor
# differs from the target (same-arch Python version mismatch still
# produces ABI-incompatible wheels).
if [ "$TARGET_OS-$TARGET_ARCH" != "$HOST_OS-$HOST_ARCH" ] || [ "$HOST_PYTHON_VERSION" != "$PYTHON_VERSION" ]; then
  case "$TARGET_OS-$TARGET_ARCH" in
    linux-arm64)   PLATFORM=manylinux2014_aarch64 ;;
    linux-amd64)   PLATFORM=manylinux2014_x86_64 ;;
    darwin-arm64)  PLATFORM=macosx_11_0_arm64 ;;
    darwin-amd64)  PLATFORM=macosx_10_9_x86_64 ;;
    *)
      echo "unsupported target $TARGET_OS-$TARGET_ARCH" >&2
      exit 1
      ;;
  esac
  PLATFORM_ARGS=(--platform "$PLATFORM" --python-version "$PYTHON_VERSION" --only-binary=:all:)
fi

mkdir -p "$OUT_DIR/python"
# ${PLATFORM_ARGS[@]+...} guards the empty-array expansion: bare
# "${PLATFORM_ARGS[@]}" trips 'set -u' on bash < 4.4 (e.g. macOS's system
# bash 3.2) when the native-build path leaves the array empty.
python3 -m pip install --target "$OUT_DIR/python" \
  ${PLATFORM_ARGS[@]+"${PLATFORM_ARGS[@]}"} \
  --upgrade \
  .

cp manifest.yaml "$OUT_DIR/manifest.yaml"
echo "built ${TARGET_OS}/${TARGET_ARCH} tool: $OUT_DIR/python/bin/` + name + `"
`
}

func renderPyBuildBat(name string) string {
	return `@echo off
REM Windows build script. See build.sh for the Mac/Linux equivalent.
REM Cross-compiles to SAM_TOOL_TARGET_OS / SAM_TOOL_TARGET_ARCH. The arch
REM is a property of the deployment, not this toolset, so it is never
REM defaulted: sam config apply discovers it from the target platform and
REM exports it. For a manual build, set both env vars first.
setlocal
if "%SAM_TOOL_BUILD_OUT%"=="" set SAM_TOOL_BUILD_OUT=dist
if "%SAM_TOOL_TARGET_OS%"=="" ( echo SAM_TOOL_TARGET_OS must be set by sam config apply, or exported for a manual build ^(e.g. linux^) 1>&2 & exit /b 1 )
if "%SAM_TOOL_TARGET_ARCH%"=="" ( echo SAM_TOOL_TARGET_ARCH must be set by sam config apply, or exported for a manual build ^(e.g. amd64^) 1>&2 & exit /b 1 )
REM Default tracks the STR runtime container's Python (3.11).
if "%SAM_TOOL_PYTHON_VERSION%"=="" set SAM_TOOL_PYTHON_VERSION=3.11

set HOST_OS=windows
set HOST_ARCH=amd64
if /I "%PROCESSOR_ARCHITECTURE%"=="ARM64" set HOST_ARCH=arm64
if /I "%PROCESSOR_ARCHITEW6432%"=="ARM64" set HOST_ARCH=arm64
for /f "tokens=2 delims= " %%v in ('python3 --version 2^>^&1') do set HOST_PYTHON_RAW=%%v
for /f "tokens=1,2 delims=." %%a in ("%HOST_PYTHON_RAW%") do set HOST_PYTHON_VERSION=%%a.%%b

set PLATFORM_ARGS=
set NEED_PLATFORM=0
if /I not "%SAM_TOOL_TARGET_OS%-%SAM_TOOL_TARGET_ARCH%"=="%HOST_OS%-%HOST_ARCH%" set NEED_PLATFORM=1
if not "%HOST_PYTHON_VERSION%"=="%SAM_TOOL_PYTHON_VERSION%" set NEED_PLATFORM=1
if "%NEED_PLATFORM%"=="1" (
  if "%SAM_TOOL_TARGET_OS%-%SAM_TOOL_TARGET_ARCH%"=="linux-arm64"   set PLATFORM=manylinux2014_aarch64
  if "%SAM_TOOL_TARGET_OS%-%SAM_TOOL_TARGET_ARCH%"=="linux-amd64"   set PLATFORM=manylinux2014_x86_64
  if "%SAM_TOOL_TARGET_OS%-%SAM_TOOL_TARGET_ARCH%"=="darwin-arm64"  set PLATFORM=macosx_11_0_arm64
  if "%SAM_TOOL_TARGET_OS%-%SAM_TOOL_TARGET_ARCH%"=="darwin-amd64"  set PLATFORM=macosx_10_9_x86_64

  if "%PLATFORM%"=="" (
    echo unsupported target %SAM_TOOL_TARGET_OS%-%SAM_TOOL_TARGET_ARCH% >&2
    exit /b 1
  )
  set PLATFORM_ARGS=--platform "%PLATFORM%" --python-version "%SAM_TOOL_PYTHON_VERSION%" --only-binary=:all:
)

if not exist "%SAM_TOOL_BUILD_OUT%\python" mkdir "%SAM_TOOL_BUILD_OUT%\python"
python3 -m pip install --target "%SAM_TOOL_BUILD_OUT%\python" ^
  %PLATFORM_ARGS% ^
  --upgrade ^
  .
if errorlevel 1 exit /b %errorlevel%

copy /Y manifest.yaml "%SAM_TOOL_BUILD_OUT%\manifest.yaml" >nul
echo built %SAM_TOOL_TARGET_OS%/%SAM_TOOL_TARGET_ARCH% tool: %SAM_TOOL_BUILD_OUT%\python\bin\` + name + `
`
}

func renderPyReadme(name, version string) string {
	pyName := sanitizePythonModule(name)
	return `# ` + name + `

Scaffolded by ` + "`sam toolset init`" + ` (SAM CLI version ` + version + `).

## Authoring

To have your AI coding assistant write the tool with the correct
` + "`sam-tool-sdk`" + ` API, install the Agent Mesh authoring skills once at your
repo root:

` + "```" + `bash
sam ai-assistance skill install                 # OpenAI Codex CLI
sam ai-assistance skill install --target claude # Claude Code
` + "```" + `

Then edit the tool source under ` + "`src/`" + ` (the ` + "`__main__.py`" + ` file),
replacing the scaffolded ` + "`greet`" + ` example with your tool.

## Local development

` + "```" + `bash
python -m venv .venv
.venv/bin/pip install -e .
.venv/bin/python -m ` + pyName + ` --schema | jq .
` + "```" + `

The Python ` + "`sam-tool-sdk`" + ` dependency resolves from PyPI through
` + "`pyproject.toml`" + `. IDE autocomplete, REPL testing, and unit tests all
work through the venv.

## Build for deployment

` + "```" + `bash
./build.sh
` + "```" + `

Honors ` + "`SAM_TOOL_TARGET_OS`" + ` and ` + "`SAM_TOOL_TARGET_ARCH`" + ` (defaults
linux/arm64) so the wheel install resolves for the deployed STR target.
Override on the command line for other deployment platforms. Produces an
AWS-Lambda-Layer-style bundle:

` + "```" + `
dist/<os>-<arch>/
  manifest.yaml
  python/
    bin/` + name + `                    # console-script entry point
    ` + pyName + `/                       # this tool's code
    sam_tool_sdk/                # SDK, vendored by pip
    pydantic/, ...               # transitive deps
` + "```" + `

` + "`sam config apply`" + ` packages and uploads this dist directory.

## Layout

` + "```" + `
toolsets/` + name + `.yaml            # kind: toolset metadata header
toolsets/` + name + `/
  src/                             # this directory
    src/` + pyName + `/                  # tool source (` + "`tool_cli(greet)`" + ` lives here)
    pyproject.toml                 # depends on sam-tool-sdk from PyPI
    build.sh / .bat                # cross-platform build scripts
    manifest.yaml                  # STR registration
  dist/<os>-<arch>/                # build output (gitignored)
` + "```" + `
`
}

func sanitizeGoModule(name string) string {
	s := strings.ToLower(name)
	s = strings.ReplaceAll(s, " ", "-")
	if s == "" {
		s = "sam-tool"
	}
	return s
}

func sanitizePythonModule(name string) string {
	s := strings.ToLower(name)
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, " ", "_")
	if s == "" {
		return "sam_tool"
	}
	if !isLetter(s[0]) {
		s = "_" + s
	}
	return s
}

func sanitizeToolKey(name string) string {
	s := strings.ToLower(name)
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, " ", "_")
	if s == "" {
		return "sam_tool"
	}
	return s
}

func isLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
