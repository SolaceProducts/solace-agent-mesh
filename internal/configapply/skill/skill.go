// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skill

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/codegen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/typeschema"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skills/design"
)

const SkillName = "sam-declarative-config"

const referencesDir = "references"

const designSubdir = "design"

//go:embed static/*.md static/per-kind/*.md static/references/*.md
var staticFS embed.FS

var kindOrder = []string{"model", "agent", "entrypoint", "linkAgent", "workflow", "toolset", "connector", "skill", "dataset", "evaluator", "experiment", "rbacRole", "rbacClaimMapping", "rbacGrant", "serviceAccount", "budget"}

var expectedReferences = func() []string {
	out := []string{
		"layout.md",
		"common-patterns.md",
		"common-mistakes.md",
		"cli-auth.md",
		"inspecting-state.md",
		"ci-pipeline.md",
		"manifest.md",
		"model.md",
		"agent.md",
		"entrypoint.md",
		"linkAgent.md",
		"workflow.md",
		"toolset.md",
		"tool-build.md",
		"connector.md",
		"skill.md",
		"skill-asset-templates.md",
		"dataset.md",
		"evaluator.md",
		"experiment.md",
		"rbacRole.md",
		"rbacClaimMapping.md",
		"rbacGrant.md",
		"serviceAccount.md",
		"budget.md",
		"rbac-scopes.md",
	}
	for _, sk := range design.Skills() {
		out = append(out, designSubdir+"/"+sk.Guide+".md")
	}
	return out
}()

var handDefinedReferenceKinds = []codegen.KindSchema{codegen.RBACRoleSchema, codegen.RBACClaimMappingSchema, codegen.RBACGrantSchema, codegen.SystemUserSchema, codegen.BudgetSchema}

func pluralDirForKind(k string) string {
	switch k {
	case "skill":
		return "skills"
	case "rbacRole":
		return "rbac/roles"
	case "rbacClaimMapping":
		return "rbac/claim-mappings"
	case "rbacGrant":
		return "rbac/grants"
	case "serviceAccount":
		return "rbac/service-accounts"
	}
	if ks, ok := codegen.SchemaData[k]; ok {
		return ks.PluralPath
	}
	return k + "s"
}

type File struct {
	RelPath string
	Body    []byte
}

func BuildTree(version string) ([]File, error) {
	skillBody, err := buildSkillMD(version)
	if err != nil {
		return nil, err
	}
	out := []File{{RelPath: skillMdFilename, Body: skillBody}}

	refs, err := buildReferences()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(refs))
	for k := range refs {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		out = append(out, File{
			RelPath: filepath.Join(referencesDir, name),
			Body:    refs[name],
		})
	}
	return out, nil
}

func BuildSkill(version string) ([]byte, error) {
	return buildSkillMD(version)
}

func buildSkillMD(version string) ([]byte, error) {
	var buf bytes.Buffer

	buf.WriteString("---\n")
	fmt.Fprintf(&buf, "name: %s\n", SkillName)
	buf.WriteString("description: Author Agent Mesh declarative-config repos (manifests, per-kind YAML, skills) and drive sam config plan/apply/pull/migrate. Generated from the CLI's own schemas at the bundled CLI version; re-run `sam ai-assistance skill install --force` after a CLI upgrade.\n")
	buf.WriteString("---\n\n")
	buf.WriteString("# Agent Mesh Declarative Config\n\n")

	if err := writeStatic(&buf, "static/overview.md"); err != nil {
		return nil, err
	}

	buf.WriteString("\n## How to use this skill\n\n")
	buf.WriteString("This skill is split into an orientation file (this SKILL.md) plus a `references/` directory of per-topic detail. Pull in the relevant `references/<topic>.md` when the work calls for it:\n\n")
	buf.WriteString("- **Authoring a manifest** — read `references/manifest.md`.\n")
	buf.WriteString("- **Authoring a resource YAML** — read `references/<kind>.md` for the kind you're touching.\n")
	buf.WriteString("- **Authoring an entrypoint** — `references/entrypoint.md`. Find the section matching the entrypoint type (`## type: slack`, `## type: webhook`, …); its field table + example are the canonical authoring source.\n")
	buf.WriteString("- **Authoring a connector** — `references/connector.md`. Each `## type: <type>` carries one or more `### subtype: <subtype>` sections.\n")
	buf.WriteString("- **Authoring a workflow** — `references/workflow.md`. Top-level workflow shape first, then per-node-type sections (`## node type: agent`, `## node type: switch`, …).\n")
	buf.WriteString("- **Authoring an evaluation corpus** — `references/dataset.md` for examples, `references/evaluator.md` for scorers, `references/experiment.md` to bind a dataset + evaluators to an agent. Trigger experiments from the CLI via `sam eval run <experiment-name>`.\n")
	buf.WriteString("- **Authoring a skill** — `references/skill.md` (bundle layout, frontmatter); `references/skill-asset-templates.md` (`.samt` report templates).\n")
	buf.WriteString("- **Working with toolsets** — `references/toolset.md` is the entry point. It covers the on-disk layout (`toolsets/<name>.yaml` + `toolsets/<name>/{src,<name>.zip}`), the two lifecycle workflows (**author** with sources under `src/`, **mirror** with the pre-built zip from `sam config pull`), the full CLI surface, the toolset-level `spec.config` (secrets + shared defaults), the per-agent `toolsetConfigs[*].configValues` overlay (per-agent overrides, nested by tool name for every toolset kind; only the toolset-level `spec.config` may be written flat), and workflow `toolsets:` binding. Drill into `references/tool-build.md` for the build pipeline — resolution order, the `build.sh`/`build.bat` contract, the `.sam-cache/build/` cache, and `--no-build` semantics.\n")
	buf.WriteString("- **CLI authentication** — `references/cli-auth.md`. Covers `sam auth login/logout/status/list`, the `auth: { type: oauth }` target shape, and the `$XDG_CONFIG_HOME/sam/auth/<target>.json` cache layout. Also documents `sam api` — the generic authenticated HTTP client to poke the entrypoint when authoring or debugging configs (read live resources, walk `PaginatedResponse` lists, reproduce the Agent Mesh UI's calls). CI: `references/ci-pipeline.md`.\n")
	buf.WriteString("- **Inspecting platform state after apply** — `references/inspecting-state.md`. The status-field glossary (`deploymentStatus` / `syncStatus` / `discoveryStatus` / `runtimeStatus`), how to resolve a name → id with `sam api --jq` (paths take UUIDs, not names), and the `/api/v2/platform/agents` vs `/api/v1/agentCards` distinction.\n")
	buf.WriteString("- **Authoring RBAC** — `references/rbacRole.md` for roles and their inline `spec.users` grants, `references/rbacClaimMapping.md` to drive access from an OIDC claim, `references/rbacGrant.md` to grant a managed/in-DB role, `references/serviceAccount.md` for service accounts. The default-role set is in the manifest's `platform` block (`references/manifest.md`). Read `references/rbac-scopes.md` before writing any scope string: the grammar, the assignable-scope catalog, and how to verify a role landed.\n")
	buf.WriteString("- **Repo layout / common patterns / common mistakes** — `references/layout.md`, `references/common-patterns.md`, `references/common-mistakes.md`.\n\n")

	buf.WriteString("## Design guidance\n\n")
	buf.WriteString("Before authoring a config, read the relevant design reference. These cover *what to build and why*, and several also carry authoring mechanics and worked YAML — so read the relevant one even when the thing to build is already decided. Pair them with the resource-kind references above.\n\n")
	buf.WriteString("- **Component selection** (workflow vs agent vs skill, when to split, peer delegation) — `references/design/best-practices.md`.\n")
	buf.WriteString("- **Core concepts** (terminology, agent card model, peer-delegation mechanics) — `references/design/concepts.md`.\n")
	buf.WriteString("- **Agent design** (instruction writing, tool selection, structured output, HIL, agent card, model selection) — `references/design/agent-design.md`.\n")
	buf.WriteString("- **Workflow design** (when to use workflows, design patterns, worked YAML for every node type, reshaping data between chained tool nodes, structured I/O, retry/timeout mechanics, anti-patterns) — `references/design/workflow-design.md`. **Read this before hand-authoring a workflow**, not only when deciding whether to use one.\n")
	buf.WriteString("- **Skill design** (when to package a skill, instruction shape, references layout) — `references/design/skill-design.md`.\n")
	buf.WriteString("- **Efficiency** (cutting LLM token usage + latency: keep data out of context, instantiate templates, load skills on demand, structured output, parallelism, compaction, model/runtime settings) — `references/design/efficiency.md`.\n\n")

	buf.WriteString("## Resource kinds index\n\n")
	buf.WriteString("| Kind | Plural directory | Reference |\n")
	buf.WriteString("|---|---|---|\n")
	fmt.Fprintf(&buf, "| `manifest` | `manifests/` | `references/manifest.md` |\n")
	for _, k := range kindOrder {
		fmt.Fprintf(&buf, "| `%s` | `%s/` | `references/%s.md` |\n", k, pluralDirForKind(k), k)
	}

	buf.WriteString("\n## Pre-flight rules\n\n")
	buf.WriteString("- Every per-resource YAML file declares its own `kind:` (e.g. `kind: agent`). The manifest is `kind: manifest` and lives separately.\n")
	buf.WriteString("- Spec-level field names are **camelCase** (matches the platform REST API). Nested per-type bodies (connector values, workflow nodes, per-type entrypoint values) follow the casing their kind's reference shows — see `references/common-mistakes.md`.\n")
	buf.WriteString("- Use `${VAR}` placeholders for credentials, never literal tokens. See `references/common-patterns.md` for the full substitution rules.\n")
	buf.WriteString("- See `references/common-mistakes.md` before opening a PR.\n")

	return StampVersion(buf.Bytes(), version)
}

func buildReferences() (map[string][]byte, error) {
	out := make(map[string][]byte, len(expectedReferences))

	for _, name := range []string{"layout.md", "common-patterns.md", "common-mistakes.md"} {
		body, err := staticFS.ReadFile("static/" + name)
		if err != nil {
			return nil, fmt.Errorf("read embedded static %s: %w", name, err)
		}
		out[name] = body
	}
	{
		body, err := staticFS.ReadFile("static/references/tool-build.md")
		if err != nil {
			return nil, fmt.Errorf("read embedded static references/tool-build.md: %w", err)
		}
		out["tool-build.md"] = body
	}
	{
		body, err := staticFS.ReadFile("static/references/cli-auth.md")
		if err != nil {
			return nil, fmt.Errorf("read embedded static references/cli-auth.md: %w", err)
		}
		out["cli-auth.md"] = body
	}
	{
		body, err := staticFS.ReadFile("static/references/inspecting-state.md")
		if err != nil {
			return nil, fmt.Errorf("read embedded static references/inspecting-state.md: %w", err)
		}
		out["inspecting-state.md"] = body
	}
	{
		body, err := staticFS.ReadFile("static/references/ci-pipeline.md")
		if err != nil {
			return nil, fmt.Errorf("read embedded static references/ci-pipeline.md: %w", err)
		}
		out["ci-pipeline.md"] = body
	}
	{
		body, err := staticFS.ReadFile("static/references/skill-asset-templates.md")
		if err != nil {
			return nil, fmt.Errorf("read embedded static references/skill-asset-templates.md: %w", err)
		}
		out["skill-asset-templates.md"] = body
	}
	out["manifest.md"] = renderManifestReference()

	for _, k := range []string{"model", "agent", "linkAgent", "toolset", "dataset", "evaluator", "experiment"} {
		ks, ok := codegen.SchemaData[k]
		if !ok {
			continue
		}
		body, err := renderSimpleKindReference(k, ks)
		if err != nil {
			return nil, err
		}
		out[k+".md"] = body
	}

	for _, ks := range handDefinedReferenceKinds {
		body, err := renderSimpleKindReference(ks.Kind, ks)
		if err != nil {
			return nil, err
		}
		out[ks.Kind+".md"] = body
	}

	out["rbac-scopes.md"] = renderRBACScopesReference()

	body, err := renderGatewayReference()
	if err != nil {
		return nil, err
	}
	out["entrypoint.md"] = body

	body, err = renderConnectorReference()
	if err != nil {
		return nil, err
	}
	out["connector.md"] = body

	out["workflow.md"] = renderWorkflowReference()

	body, err = renderSkillReference()
	if err != nil {
		return nil, err
	}
	out["skill.md"] = body

	for _, sk := range design.Skills() {
		body, err := sk.Body(design.CLI)
		if err != nil {
			return nil, fmt.Errorf("read design %s: %w", sk.Guide, err)
		}
		out[designSubdir+"/"+sk.Guide+".md"] = body
	}
	return out, nil
}

func writeStatic(buf *bytes.Buffer, path string) error {
	data, err := staticFS.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read embedded %s: %w", path, err)
	}
	buf.Write(data)
	if !bytes.HasSuffix(data, []byte("\n\n")) {
		buf.WriteString("\n")
	}
	return nil
}

func renderSchemaTable(buf *bytes.Buffer, ks codegen.KindSchema) {
	if ks.Description != "" {
		fmt.Fprintf(buf, "%s\n\n", ks.Description)
	}
	if len(ks.Fields) == 0 {
		buf.WriteString("_No fields._\n")
		return
	}
	buf.WriteString("| Field | Type | Required | Validation | Description |\n")
	buf.WriteString("|---|---|---|---|---|\n")
	for _, f := range ks.Fields {
		req := ""
		if f.Required {
			req = "yes"
		}
		desc := f.Description
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Fprintf(buf, "| `%s` | `%s` | %s | %s | %s |\n",
			f.JSONName, f.DisplayType(), req, ValidationCell(f), MarkdownEscape(desc))
	}
}

func ValidationCell(f codegen.FieldSchema) string {
	parts := []string{}
	if f.MinLen >= 0 && f.MaxLen >= 0 && f.DisplayType() == "integer" {
		parts = append(parts, fmt.Sprintf("%d–%d", f.MinLen, f.MaxLen))
	} else if f.MinLen >= 0 && f.MaxLen >= 0 {
		parts = append(parts, fmt.Sprintf("len %d–%d", f.MinLen, f.MaxLen))
	} else if f.MinLen >= 0 {
		parts = append(parts, fmt.Sprintf("min %d", f.MinLen))
	} else if f.MaxLen >= 0 {
		parts = append(parts, fmt.Sprintf("max %d", f.MaxLen))
	}
	if len(f.OneOf) > 0 {
		parts = append(parts, "one of: "+strings.Join(f.OneOf, ", "))
	}
	if f.Secret {
		parts = append(parts, "secret")
	}
	if f.Pointer {
		parts = append(parts, "tri-state pointer")
	}
	return strings.Join(parts, "; ")
}

func MarkdownEscape(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

func Install(dir, version string, force bool) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve install dir: %w", err)
	}
	skillDir := filepath.Join(abs, SkillName)
	skillMD := filepath.Join(skillDir, skillMdFilename)
	refsDir := filepath.Join(skillDir, referencesDir)

	if !force {
		if info, statErr := os.Stat(skillMD); statErr == nil && info.Size() > 0 {
			return "", fmt.Errorf("refusing to overwrite existing %s — pass --force to replace", skillMD)
		}
	} else {
		if rmErr := os.RemoveAll(refsDir); rmErr != nil {
			return "", fmt.Errorf("wipe references/: %w", rmErr)
		}
		if rmErr := os.Remove(skillMD); rmErr != nil && !os.IsNotExist(rmErr) {
			return "", fmt.Errorf("wipe SKILL.md: %w", rmErr)
		}
	}

	tree, err := BuildTree(version)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return "", fmt.Errorf("create skill dir: %w", err)
	}
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		return "", fmt.Errorf("create references dir: %w", err)
	}

	for _, f := range tree {
		target := filepath.Join(skillDir, f.RelPath)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", fmt.Errorf("ensure dir for %s: %w", f.RelPath, err)
		}
		if err := atomicWrite(target, f.Body); err != nil {
			return "", err
		}
	}
	return skillMD, nil
}

func atomicWrite(path string, body []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

type CheckResult struct {
	SkillMDPath string
	Frontmatter Frontmatter
	Missing     []string
}

func CheckTree(dir string) (CheckResult, error) {
	skillDir := filepath.Join(dir, SkillName)
	skillMD := filepath.Join(skillDir, skillMdFilename)
	fm, err := ParseFrontmatter(skillMD)
	if err != nil {
		return CheckResult{}, err
	}
	res := CheckResult{
		SkillMDPath: skillMD,
		Frontmatter: fm,
	}
	for _, ref := range expectedReferences {
		full := filepath.Join(skillDir, referencesDir, ref)
		if _, err := os.Stat(full); err != nil {
			res.Missing = append(res.Missing, ref)
		}
	}
	return res, nil
}

func StaticFS() fs.FS { return staticFS }

func renderRBACScopesReference() []byte {
	var buf bytes.Buffer
	buf.WriteString("# RBAC scope reference\n\n")
	buf.WriteString("Scopes are the permission strings a role grants. They appear in `rbacRole.spec.scopes`; every other RBAC kind references roles by name and never names a scope directly.\n\n")

	buf.WriteString("## Grammar\n\n")
	buf.WriteString("Every scope is three colon-separated segments: `<category>:<resource>:<verb>`.\n\n")
	buf.WriteString("The second segment addresses *which* instances the scope covers:\n\n")
	buf.WriteString("| Segment 2 | Meaning | Example |\n|---|---|---|\n")
	buf.WriteString("| `<id>` | One named instance | `agent:hr-bot:invoke` |\n")
	buf.WriteString("| `*` | Any instance, or the whole collection where the category has no instances | `agent_builder:*:update` |\n")
	buf.WriteString("| `_` | Older spelling of `*`; some entries in the following catalog, and roles that hold them, still use it | `rbac:_:read` |\n\n")
	buf.WriteString("Write `*` in segment 2, even where the following catalog shows `_`: write `rbac:*:read` for `rbac:_:read`. A `*` covers any instance where the category has instances and the whole collection where it has none, so a `*` alone does not mean the scope takes an instance id.\n\n")
	buf.WriteString("In a **granted** scope, a segment-2 `_` is read as `*` — they are the same permission in two spellings, so a role holding `connector:_:*` satisfies a check for `connector:*:read`. The equivalence applies only to segment 2 of a three-segment scope: a segment-3 `_` is never a wildcard over verbs, and it never rewrites the scope being checked. The equivalence is what makes `*` safe to write for a `_` catalog entry, and it keeps roles that hold `_` working.\n\n")
	buf.WriteString("The bare `*` is a separate, legacy spelling of the universal grant (equivalent to `*:*:*`). Both are live in shipped role seeds; prefer `*:*:*` in new config.\n\n")

	buf.WriteString("## Verbs\n\n")
	buf.WriteString("Use only the verbs that the following assignable scope catalog uses: the CRUD verbs `read`, `create`, `update`, `delete`, plus `invoke` and the per-subject verbs a category lists (such as `read_feedback`). The synonyms `view`, `edit`, `write`, `manage`, `execute` and `delegate` are not used by any gate — a role granting one is stored without error and authorizes nothing.\n\n")

	buf.WriteString("## Invoke scopes are resolved per instance\n\n")
	buf.WriteString("The scope that gates invoking a mesh participant depends on what the participant is, so an invoke grant cannot be written generically:\n\n")
	buf.WriteString("| Target | Scope |\n|---|---|\n")
	buf.WriteString("| Deployed workflow | `workflow:<uuid>:invoke` |\n")
	buf.WriteString("| Platform-deployed agent | `agent:<uuid>:invoke` |\n")
	buf.WriteString("| Plain-named (config) agent | `agent:<name>:invoke` |\n\n")
	buf.WriteString("`agent:*:invoke` does **not** cover deployed workflows — those gate on the `workflow` category. A role meant to invoke both needs a scope in each.\n\n")

	buf.WriteString("## Who may write RBAC config\n\n")
	buf.WriteString("Every RBAC write — roles, grants, claim mappings, service accounts and the default-role set — requires the universal grant (`*:*:*` or the legacy `*`). A caller holding `rbac:*:read` can plan but not apply: `sam config plan` still previews the changes, then each write is refused with \"RBAC management requires a super administrator\". Nothing is partially written, since the refusal is uniform.\n\n")

	buf.WriteString("## Assignable scope catalog\n\n")
	buf.WriteString("Generated from the CLI's own catalog at the bundled version. A scope outside this list, other than the `*` spelling of a `_` entry, is not gated by any route, so it is stored without error and grants nothing")
	var reserved []string
	for _, scope := range rbac.UnassignableScopes() {
		if scopeFeatureEnabled(documentedFeatures(), scope) {
			reserved = append(reserved, "`"+scope+"`")
		}
	}
	if len(reserved) > 0 {
		buf.WriteString(", except " + strings.Join(reserved, ", ") + ": routes enforce these, but they are reserved for the universal grant and the built-in `sam_manager` role, so do not put them on a custom role")
	}
	buf.WriteString(".\n\n")
	buf.WriteString("| Scope | Name | Description |\n|---|---|---|\n")
	reg := documentedFeatures()
	for _, s := range rbac.Catalog() {
		if !scopeFeatureEnabled(reg, s.Scope) {
			continue
		}
		fmt.Fprintf(&buf, "| `%s` | %s | %s |\n", s.Scope, s.Name, s.Description)
	}

	buf.WriteString("\n## Verifying a role after apply\n\n")
	buf.WriteString("Read the stored role back and confirm the scopes are the ones you authored:\n\n")
	buf.WriteString("```\nsam api /api/v2/platform/rbac/roles\n```\n\n")
	buf.WriteString("A clean `sam config plan` on the next run is the other signal: it means the platform's copy matches the repo. Read `references/rbacRole.md` for the role file's own shape.\n")
	return buf.Bytes()
}

var scopeCategoryFeatures = map[string]string{
	"analytics":     features.KeyObservabilityAgent,
	"event_catalog": features.KeyEventCatalog,
	"notify":        features.KeyNotifyService,
}

func scopeFeatureEnabled(reg *features.Registry, scope string) bool {
	category, _, _ := strings.Cut(scope, ":")
	key, owned := scopeCategoryFeatures[category]
	return !owned || reg.IsEnabled(key)
}

func documentedFeatures() *features.Registry {
	reg := features.NewRegistry()
	if err := features.LoadFlags(reg); err != nil {
		slog.Default().With("logger_name", "configapply.skill").
			Warn("failed to load feature catalog; feature-owned scopes will be omitted", "error", err)
	}
	return reg
}

func renderManifestReference() []byte {
	var buf bytes.Buffer
	buf.WriteString("# Manifest reference\n\n")
	if data, err := staticFS.ReadFile("static/manifest.md"); err == nil {
		buf.Write(data)
		if !bytes.HasSuffix(data, []byte("\n\n")) {
			buf.WriteString("\n")
		}
	}
	buf.WriteString("\n## Schema\n\n")
	renderSchemaTable(&buf, manifest.ManifestSchema)
	return buf.Bytes()
}

func renderSimpleKindReference(k string, ks codegen.KindSchema) ([]byte, error) {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "# Kind: `%s`\n\n", k)
	if ks.PluralPath != "" {
		fmt.Fprintf(&buf, "Manifest path: `resources.%s`\n\n", ks.PluralPath)
	}
	if data, err := staticFS.ReadFile("static/per-kind/" + k + ".md"); err == nil {
		buf.Write(data)
		buf.WriteString("\n")
	} else {
		return nil, fmt.Errorf("read static/per-kind/%s.md: %w", k, err)
	}
	buf.WriteString("\n## Schema\n\n")
	renderSchemaTable(&buf, ks)
	buf.WriteString("\n## Example\n\n```yaml\n")
	RenderExample(&buf, ks, "")
	buf.WriteString("```\n")
	return buf.Bytes(), nil
}

func renderGatewayReference() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("# Kind: `entrypoint`\n\n")
	buf.WriteString("Manifest path: `resources.entrypoints`\n\n")
	if data, err := staticFS.ReadFile("static/per-kind/entrypoint.md"); err == nil {
		buf.Write(data)
		buf.WriteString("\n")
	} else {
		return nil, fmt.Errorf("read static/per-kind/entrypoint.md: %w", err)
	}

	buf.WriteString(wrapperSchemaHeader)
	renderSchemaTable(&buf, codegen.SchemaData["entrypoint"])

	buf.WriteString("\n## Per-type detail\n\n")
	buf.WriteString("Each entrypoint type has its own `values:` shape. Find the section matching the `type:` your entrypoint uses.\n\n")
	for _, s := range typeschema.GatewayTypeSchemasSorted() {
		RenderGatewayTypeSchema(&buf, s)
		buf.WriteString("\n### Example\n\n```yaml\n")
		RenderGatewayExample(&buf, "", s)
		buf.WriteString(codeFenceClose)
	}
	return buf.Bytes(), nil
}

func renderConnectorReference() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("# Kind: `connector`\n\n")
	buf.WriteString("Manifest path: `resources.connectors`\n\n")
	if data, err := staticFS.ReadFile("static/per-kind/connector.md"); err == nil {
		buf.Write(data)
		buf.WriteString("\n")
	} else {
		return nil, fmt.Errorf("read static/per-kind/connector.md: %w", err)
	}

	buf.WriteString(wrapperSchemaHeader)
	renderSchemaTable(&buf, codegen.SchemaData["connector"])

	buf.WriteString("\n## Per-(type, subtype) detail\n\n")
	buf.WriteString("Each connector type carries one or more subtypes. Find the `## type:` matching the `type:` field your connector uses, then drill into the right `### subtype:`.\n\n")
	for _, t := range typeschema.ConnectorTypeNames() {
		fmt.Fprintf(&buf, "## type: %s\n\n", t)
		for _, sub := range typeschema.ConnectorSubtypeNames(t) {
			s, err := typeschema.LookupConnectorType(t, sub)
			if err != nil {
				continue
			}
			RenderConnectorSubtypeSchema(&buf, s)
			buf.WriteString("\n#### Example\n\n```yaml\n")
			RenderConnectorExample(&buf, "", s)
			buf.WriteString(codeFenceClose)
		}
	}
	return buf.Bytes(), nil
}

func renderWorkflowReference() []byte {
	var buf bytes.Buffer
	buf.WriteString("# Kind: `workflow`\n\n")
	buf.WriteString("Manifest path: `resources.workflows`\n\n")
	if data, err := staticFS.ReadFile("static/per-kind/workflow.md"); err == nil {
		buf.Write(data)
		buf.WriteString("\n")
	}

	buf.WriteString(wrapperSchemaHeader)
	renderSchemaTable(&buf, codegen.SchemaData["workflow"])

	buf.WriteString("\n## `appConfig:` shape\n\n")
	buf.WriteString("The `appConfig:` payload (the workflow definition itself) follows this shape:\n\n")
	if codegen.WorkflowSchema.Description != "" {
		fmt.Fprintf(&buf, "%s\n\n", codegen.WorkflowSchema.Description)
	}
	buf.WriteString("| Field | Type | Description |\n")
	buf.WriteString("|---|---|---|\n")
	for _, f := range codegen.WorkflowSchema.Documented().Fields {
		desc := f.Description
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Fprintf(&buf, "| `%s` | `%s` | %s |\n", f.JSONName, f.DisplayType(), MarkdownEscape(desc))
	}

	buf.WriteString("\n## Node types\n\n")
	buf.WriteString("Each node has a `type:` selecting one of the shapes below. Common fields (`id`, `type`, `depends_on`, plus the shared optional fields) are listed in every section so each node-type entry is self-contained.\n\n")
	keys := make([]string, 0, len(codegen.WorkflowSchema.Documented().NodeTypes))
	for k := range codegen.WorkflowSchema.Documented().NodeTypes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		nt := codegen.WorkflowSchema.Documented().NodeTypes[k]
		RenderNodeTypeSchema(&buf, nt)
		buf.WriteString("\n")
	}
	return buf.Bytes()
}

func renderSkillReference() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("# Kind: `skill`\n\n")
	buf.WriteString("Manifest path: `resources.skills`\n\n")
	data, err := staticFS.ReadFile("static/per-kind/skill.md")
	if err != nil {
		return nil, fmt.Errorf("read static/per-kind/skill.md: %w", err)
	}
	buf.Write(data)
	buf.WriteString("\n")
	buf.WriteString("\n## Frontmatter shape\n\n")
	buf.WriteString("Each `<skill>/SKILL.md` begins with YAML frontmatter the platform reads:\n\n")
	buf.WriteString("```yaml\n")
	buf.WriteString("---\n")
	buf.WriteString("name: my-skill            # required; a letter, then letters, digits, - or _\n")
	buf.WriteString("description: One-line summary used by the harness for skill discovery.\n")
	buf.WriteString("# tags: [optional, list]   # optional; surfaced in skill listings\n")
	buf.WriteString("---\n")
	buf.WriteString(codeFenceClose)
	buf.WriteString("Any other frontmatter keys are passed through to the platform unchanged. The body of `SKILL.md` is rendered as-is by the harness — keep it terse, with prompts/templates broken out into `assets/`.\n")
	return buf.Bytes(), nil
}

const (
	skillMdFilename     = "SKILL.md"
	wrapperSchemaHeader = "\n## Wrapper schema\n\n"
	codeFenceClose      = "```\n\n"
)
