// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillinit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/sdkvendor"
)

const manifestFileRef = "`tools/manifest.yaml`"

func renderSkillMD(name string) string {
	return `---
name: ` + name + `
description: ` + name + ` skill (replace this description with what the skill does and when an agent should load it).
# Optional frontmatter (uncomment and edit as needed):
# tags: [` + name + `, example]
# toolsets: [my-existing-toolset]   # enable a deployed toolset by name when this skill loads
---

# ` + name + `

Describe what this skill teaches the agent and when to load it.

## References

Drop reference docs under ` + "`references/`" + ` and point the agent at them
from here. The agent reads them on demand via ` + "`read_skill_resource`" + `.

## Assets

Ship static files (a logo, a boilerplate doc, a report template) under
` + "`assets/`" + `. An agent brings one into the session with the
` + "`instantiate_template`" + ` tool. To ship a **report template**, drop a single
` + "`.samt`" + ` file (produced by ` + "`package_template`" + ` — it bundles the body and
its contract into one file) under ` + "`assets/`" + `. The agent reads its contract
with ` + "`read_template`" + ` and renders it with ` + "`instantiate_template`" + `,
binding each declared ` + "`data_input`" + ` by logical name via that tool's
` + "`data_inputs`" + ` arg.

## Tools

Bundle tools under ` + "`tools/`" + ` with a ` + manifestFileRef + `.
Run ` + "`sam skill init " + name + " --with-tool`" + ` to scaffold a sample tool
(add ` + "`--lang python`" + ` for a Python tool instead of Go), then
` + "`sam skill validate " + name + "`" + ` to confirm the STR can discover it
before you package and upload.
`
}

func writeSampleGoTool(skillDir, toolDir, name, version string) error {
	files := map[string]struct {
		body string
		mode os.FileMode
	}{
		filepath.Join(skillDir, "tools", "manifest.yaml"): {body: renderToolManifest(name), mode: 0o644},
		filepath.Join(toolDir, "main.go"):                 {body: renderGoMain(name), mode: 0o644},
		filepath.Join(toolDir, "go.mod"):                  {body: renderGoMod(name), mode: 0o644},
		filepath.Join(toolDir, "README.md"):               {body: renderToolReadme(name, version), mode: 0o644},
		filepath.Join(toolDir, ".gitignore"):              {body: "dist/\n" + sdkvendor.VendorDirName() + "/\n", mode: 0o644},
	}
	for full, f := range files {
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(f.body), f.mode); err != nil {
			return fmt.Errorf("write %s: %w", full, err)
		}
	}
	return nil
}

func renderToolManifest(name string) string {
	key := sanitizeToolKey(name)
	return `version: 1
tools:
  ` + key + `_greet:
    runtime: go
    tool_dir: ` + name + `
    executable: ` + name + `
    timeout_seconds: 60
`
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

func renderToolReadme(name, version string) string {
	vendorDir := sdkvendor.VendorDirName() + "/" + sdkvendor.GoSDKDirName()
	return `# ` + name + ` (skill tool)

Scaffolded by ` + "`sam skill init --with-tool`" + ` (SAM CLI version ` + version + `).

This is a bundled Go tool for the ` + name + ` skill. The build pipeline
("` + "sam skill package" + `" / "` + "sam config apply" + `") compiles it for the
deployment target because ` + manifestFileRef + ` declares it with
` + "`runtime: go`" + `. The Go SDK is vendored at ` + "`" + vendorDir + "/`" + ` and
wired in via ` + "`go.mod`" + `'s ` + "`replace`" + ` directive — no ` + "`go get`" + `
step is needed.
`
}

func writeSamplePyTool(skillDir, toolDir, name, version string) error {
	pyName := sanitizePythonModule(name)
	srcDir := filepath.Join(toolDir, "src", pyName)
	files := map[string]struct {
		body string
		mode os.FileMode
	}{
		filepath.Join(skillDir, "tools", "manifest.yaml"): {body: renderPyToolManifest(name), mode: 0o644},
		filepath.Join(toolDir, "pyproject.toml"):          {body: renderPyProject(name, pyName, version), mode: 0o644},
		filepath.Join(srcDir, "__init__.py"):              {body: renderPyInit(), mode: 0o644},
		filepath.Join(srcDir, "__main__.py"):              {body: renderPyMain(), mode: 0o644},
		filepath.Join(toolDir, "README.md"):               {body: renderPyToolReadme(name, version), mode: 0o644},
		filepath.Join(toolDir, ".gitignore"):              {body: "dist/\n.venv/\n__pycache__/\n*.egg-info/\n", mode: 0o644},
	}
	for full, f := range files {
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(f.body), f.mode); err != nil {
			return fmt.Errorf("write %s: %w", full, err)
		}
	}
	return nil
}

func renderPyToolManifest(name string) string {
	key := sanitizeToolKey(name)
	return `version: 1
tools:
  ` + key + `_greet:
    runtime: python
    tool_dir: ` + name + `
    executable: python/bin/` + name + `
    timeout_seconds: 60
`
}

func renderPyProject(name, pyName, version string) string {
	return `[build-system]
requires = ["hatchling"]
build-backend = "hatchling.build"

[project]
name = "` + pyName + `"
version = "0.1.0"
description = "SAM skill tool scaffold (` + name + `)"
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

func renderPyInit() string {
	return `"""SAM skill tool package — re-exports the tool_cli entry point."""

from .__main__ import cli

__all__ = ["cli"]
`
}

func renderPyMain() string {
	return `"""Scaffold for a SAM Python skill tool. Implement greet() below.

The contract:
- A function wrapped with ` + "`tool_cli`" + ` becomes the tool's
  schema-discoverable entry point.
- Annotate parameters with types; the SDK reflects these into a JSON
  schema the agent reads via --schema.
- Annotate the docstring's Args section to give each parameter a
  human-readable description (shown to the LLM).
- Annotate one parameter as ` + "`SandboxToolContextFacade`" + ` to get
  access to send_status / load_artifact / call_llm — the SDK injects it
  at runtime and excludes it from the schema.
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

func renderPyToolReadme(name, version string) string {
	pyName := sanitizePythonModule(name)
	return `# ` + name + ` (skill tool)

Scaffolded by ` + "`sam skill init --with-tool --lang python`" + ` (SAM CLI version ` + version + `).

This is a bundled Python tool for the ` + name + ` skill. The build pipeline
("` + "sam skill package" + `" / "` + "sam config apply" + `") packages it in
AWS-Lambda-Layer style because ` + manifestFileRef + ` declares it with
` + "`runtime: python`" + ` — ` + "`pip install --target python/`" + ` vendors
` + "`sam-tool-sdk`" + ` and every transitive dep, and drops the console script
at ` + "`python/bin/" + name + "`" + `. Native wheels are resolved for the
deployed STR's platform and Python version automatically.

## Local development

` + "```" + `bash
python -m venv .venv
.venv/bin/pip install -e .
.venv/bin/python -m ` + pyName + ` --schema | jq .
` + "```" + `

Run ` + "`sam skill validate " + name + "`" + ` to confirm the STR can discover
the tool before you package and upload.
`
}

func sanitizePythonModule(name string) string {
	s := strings.ToLower(name)
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, " ", "_")
	if s == "" {
		return "sam_skill_tool"
	}
	if !isLetter(s[0]) {
		s = "_" + s
	}
	return s
}

func isLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func sanitizeGoModule(name string) string {
	s := strings.ToLower(name)
	s = strings.ReplaceAll(s, " ", "-")
	if s == "" {
		return "sam-skill-tool"
	}
	return s
}

func sanitizeToolKey(name string) string {
	s := strings.ToLower(name)
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, " ", "_")
	if s == "" {
		return "sam_skill_tool"
	}
	return s
}
