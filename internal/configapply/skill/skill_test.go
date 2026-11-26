// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skill

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/codegen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/typeschema"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skills/design"
)

var bundleBannedSubstrings = []string{
	"is the request body for", "is the body for", "is the response body for",
	"validate tag", "`omitempty`", `doc:"`, "cfgapply:",
	"WorkflowConfig", "HILDatabaseURL", "DefaultRegistry", "tool.Set", "nameToSnakeCase",
}

var bundleBannedPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?:^|[^.\w-])internal/`),
	regexp.MustCompile(`\bwebui/`),
	regexp.MustCompile(`dev-docs`),
	regexp.MustCompile(`\bdocs/docs`),
	regexp.MustCompile(`github\.com/SolaceDev`),
	regexp.MustCompile(`(?i)\bsam-go\b`),
}

var bundleMethodPathRe = regexp.MustCompile(`(?i)\b(POST|GET|PUT|PATCH|DELETE) +(/[a-zA-Z0-9/_{}.-]+)`)

func TestBuildTree_NoInternalLeaks(t *testing.T) {
	files, err := BuildTree("test")
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}
	for _, f := range files {
		body := string(f.Body)
		for _, b := range bundleBannedSubstrings {
			if strings.Contains(body, b) {
				t.Errorf("%s leaks internal detail %q", f.RelPath, b)
			}
		}
		for _, m := range bundleMethodPathRe.FindAllStringSubmatch(body, -1) {
			if !strings.HasPrefix(m[2], "/api/") {
				t.Errorf("%s leaks internal endpoint path %q", f.RelPath, m[0])
			}
		}
		for _, re := range bundleBannedPatterns {
			for _, hit := range re.FindAllString(body, -1) {
				t.Errorf("%s leaks internal detail %q — customer-facing references carry no source-tree paths, private-repo links or the repository name", f.RelPath, hit)
			}
		}
	}
}

func TestRenderExample_SecretFieldUsesPlaceholder(t *testing.T) {
	ks := codegen.KindSchema{
		Kind:       "fixture",
		PluralPath: "fixtures",
		Fields: []codegen.FieldSchema{
			{JSONName: "name", GoType: "string", Required: true},
			{JSONName: "apiKey", GoType: "string", Required: true, Secret: true},
			{JSONName: "endpoint", GoType: "string", Required: false},
		},
	}
	var buf bytes.Buffer
	RenderExample(&buf, ks, "demo")
	out := buf.String()
	if !strings.Contains(out, "name: demo") {
		t.Errorf("expected 'name: demo' in:\n%s", out)
	}
	if !strings.Contains(out, "apiKey: ${DEMO_APIKEY}") {
		t.Errorf("expected secret placeholder, got:\n%s", out)
	}
	if !strings.Contains(out, "# secret") {
		t.Errorf("expected '# secret' comment, got:\n%s", out)
	}
	if !strings.Contains(out, "# optional: endpoint:") {
		t.Errorf("expected optional field commented out, got:\n%s", out)
	}
}

func TestRenderExample_NestsNonIdentityFieldsUnderSpec(t *testing.T) {
	ks := codegen.KindSchema{
		Kind:       "fixture",
		PluralPath: "fixtures",
		Fields: []codegen.FieldSchema{
			{JSONName: "name", GoType: "string", Required: true},
			{JSONName: "description", GoType: "string", Required: true},
			{JSONName: "systemPrompt", GoType: "string", Required: true},
		},
	}
	var buf bytes.Buffer
	RenderExample(&buf, ks, "demo")
	out := buf.String()

	if !strings.Contains(out, "\nspec:\n") {
		t.Fatalf("expected a spec: block, got:\n%s", out)
	}
	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(line, "systemPrompt:") {
			t.Errorf("systemPrompt must be indented under spec:, found at root:\n%s", out)
		}
	}
	if !strings.Contains(out, "\n  systemPrompt:") {
		t.Errorf("expected systemPrompt nested under spec:, got:\n%s", out)
	}
}

func TestRenderExample_AlwaysEmitsRequiredTopLevelName(t *testing.T) {
	cases := map[string]codegen.KindSchema{
		"model":            codegen.SchemaData["model"],
		"rbacGrant":        codegen.RBACGrantSchema,
		"rbacRole":         codegen.RBACRoleSchema,
		"rbacClaimMapping": codegen.RBACClaimMappingSchema,
		"serviceAccount":   codegen.SystemUserSchema,
	}
	for kind, ks := range cases {
		t.Run(kind, func(t *testing.T) {
			var buf bytes.Buffer
			RenderExample(&buf, ks, "")
			out := buf.String()
			if !strings.HasPrefix(out, "kind: "+kind+"\nname: example_"+kind+"\n") {
				t.Errorf("expected required top-level name right after kind, got:\n%s", out)
			}
			if strings.Contains(out, "\n# optional: name:") {
				t.Errorf("top-level name must not be marked optional, got:\n%s", out)
			}
			if kind == "model" && strings.Contains(out, "alias:") {
				t.Errorf("model example must not author spec.alias (apply derives it from name), got:\n%s", out)
			}
		})
	}
}

func TestRenderExample_KeepsAuthoredSpecName(t *testing.T) {
	for _, ks := range []codegen.KindSchema{codegen.SystemUserSchema, codegen.RBACClaimMappingSchema} {
		var buf bytes.Buffer
		RenderExample(&buf, ks, "demo")
		if !strings.Contains(buf.String(), "\nspec:\n  # optional: name:") {
			t.Errorf("%s example must keep its optional spec.name, got:\n%s", ks.Kind, buf.String())
		}
	}
}

func TestRenderExample_OneOfPicksFirst(t *testing.T) {
	ks := codegen.KindSchema{
		Kind: "fixture",
		Fields: []codegen.FieldSchema{
			{JSONName: "type", GoType: "string", Required: true, OneOf: []string{"alpha", "beta"}},
		},
	}
	var buf bytes.Buffer
	RenderExample(&buf, ks, "")
	if !strings.Contains(buf.String(), "type: \"alpha\"") {
		t.Errorf("expected oneof to pick 'alpha', got:\n%s", buf.String())
	}
}

func TestBuildSkill_HasFrontmatterAndOrientation(t *testing.T) {
	body, err := BuildSkill("v9.9.9")
	if err != nil {
		t.Fatalf("BuildSkill: %v", err)
	}
	src := string(body)
	for _, want := range []string{
		"metadata:\n  version: v9.9.9",
		"## Overview",
		"## How to use this skill",
		"references/entrypoint.md",
		"references/connector.md",
		"references/workflow.md",
		"references/manifest.md",
		"## Resource kinds index",
		"| `budget` | `budgets/` | `references/budget.md` |",
		"| `linkAgent` | `linkAgents/` | `references/linkAgent.md` |",
		"## Pre-flight rules",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("BuildSkill output missing %q", want)
		}
	}
}

func TestBuildSkill_StaysSmall(t *testing.T) {
	body, err := BuildSkill("v0.0.0")
	if err != nil {
		t.Fatalf("BuildSkill: %v", err)
	}
	if len(body) > 9472 {
		t.Errorf("SKILL.md grew to %d bytes; expected <= 9472. Move detail into references/.", len(body))
	}
}

func TestBuildTree_HasAllReferences(t *testing.T) {
	tree, err := BuildTree("v1.0.0")
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}
	if tree[0].RelPath != "SKILL.md" {
		t.Errorf("expected SKILL.md first, got %q", tree[0].RelPath)
	}
	got := map[string]bool{}
	for _, f := range tree {
		got[f.RelPath] = true
	}
	for _, ref := range expectedReferences {
		full := "references/" + ref
		if !got[full] {
			t.Errorf("BuildTree missing %s", full)
		}
	}
}

func TestBuildTree_GatewayReferenceHasPerTypeSections(t *testing.T) {
	tree, err := BuildTree("v1.0.0")
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}
	var body string
	for _, f := range tree {
		if f.RelPath == "references/entrypoint.md" {
			body = string(f.Body)
			break
		}
	}
	if body == "" {
		t.Fatal("entrypoint.md missing from tree")
	}
	for _, want := range []string{
		"## type: slack",
		"bot_token",
		"app_token",
		"```yaml",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("gateway.md missing %q", want)
		}
	}
}

func TestBuildTree_ConnectorReferenceHasPerSubtypeSections(t *testing.T) {
	tree, err := BuildTree("v1.0.0")
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}
	var body string
	for _, f := range tree {
		if f.RelPath == "references/connector.md" {
			body = string(f.Body)
			break
		}
	}
	if body == "" {
		t.Fatal("connector.md missing from tree")
	}
	for _, want := range []string{
		"## type: sql",
		"### subtype: postgres",
		"`password`",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("connector.md missing %q", want)
		}
	}
}

func TestBuildTree_WorkflowReferenceHasNodeTypes(t *testing.T) {
	tree, err := BuildTree("v1.0.0")
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}
	var body string
	for _, f := range tree {
		if f.RelPath == "references/workflow.md" {
			body = string(f.Body)
			break
		}
	}
	if body == "" {
		t.Fatal("workflow.md missing from tree")
	}
	for _, want := range []string{
		"## node type: agent",
		"## node type: switch",
		"## node type: loop",
		"## node type: map",
		"`agent_name`",
		"`cases`",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("workflow.md missing %q", want)
		}
	}
}

func TestBuildTree_EvalReferencesDocumented(t *testing.T) {
	tree, err := BuildTree("v1.0.0")
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}
	body := func(rel string) string {
		for _, f := range tree {
			if f.RelPath == rel {
				return string(f.Body)
			}
		}
		t.Fatalf("%s missing from tree", rel)
		return ""
	}

	dataset := body("references/dataset.md")
	for _, want := range []string{
		"examples_file",
		"sequence_number",
		"expected_response",
		"<name>.examples.csv",
		"```csv",
	} {
		if !strings.Contains(dataset, want) {
			t.Errorf("dataset.md missing %q", want)
		}
	}
	if strings.Contains(dataset, "not part of the dataset YAML in v1") {
		t.Error("dataset.md still contains the stale examples prose")
	}
	if ks, ok := codegen.SchemaData["dataset"]; !ok {
		t.Error("dataset kind missing from codegen.SchemaData")
	} else if !slices.ContainsFunc(ks.Fields, func(f codegen.FieldSchema) bool {
		return f.JSONName == "examples_file"
	}) {
		t.Error("examples_file missing from the dataset schema fields")
	}

	evaluator := body("references/evaluator.md")
	for _, want := range []string{
		"{{ Response }}",
		"{{ Prompt }}",
		"{{ Expected Response }}",
		"{{ Node Output: <node id> }}",
		"{{ Node Input: <node id> }}",
		"defaults to `llm_judge`",
		"choiceScores",
	} {
		if !strings.Contains(evaluator, want) {
			t.Errorf("evaluator.md missing %q", want)
		}
	}
	if strings.Contains(evaluator, "(no description)") {
		t.Error("evaluator.md still renders a field with no description")
	}

	experiment := body("references/experiment.md")
	if !strings.Contains(experiment, "modelConfigId") {
		t.Error("experiment.md missing the models `modelConfigId` shape")
	}
}

func TestBuildTree_RBACReferences(t *testing.T) {
	tree, err := BuildTree("v1.0.0")
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}
	body := func(rel string) string {
		for _, f := range tree {
			if f.RelPath == rel {
				return string(f.Body)
			}
		}
		t.Fatalf("%s missing from tree", rel)
		return ""
	}

	role := body("references/rbacRole.md")
	for _, want := range []string{
		"# Kind: `rbacRole`",
		"spec.users",
		"`scopes`",
		"`users`",
		"rbacAssignment",
	} {
		if !strings.Contains(role, want) {
			t.Errorf("rbacRole.md missing %q", want)
		}
	}

	mapping := body("references/rbacClaimMapping.md")
	for _, want := range []string{"# Kind: `rbacClaimMapping`", "`oidcProvider`", "`roleNames`"} {
		if !strings.Contains(mapping, want) {
			t.Errorf("rbacClaimMapping.md missing %q", want)
		}
	}
	if strings.Contains(mapping, "`roleIds`") {
		t.Errorf("rbacClaimMapping.md must not teach roleIds (authoring uses roleNames)")
	}

	grant := body("references/rbacGrant.md")
	for _, want := range []string{"# Kind: `rbacGrant`", "`roleName`", "`users`"} {
		if !strings.Contains(grant, want) {
			t.Errorf("rbacGrant.md missing %q", want)
		}
	}

	sysUser := body("references/serviceAccount.md")
	for _, want := range []string{
		"# Kind: `serviceAccount`",
		"`roleNames`",
		"rbac/service-accounts/",
		"rbac/system-users/",
		"Removing a role revokes it",
		"`--prune`",
		"cannot be changed in place",
		"sam config pull",
	} {
		if !strings.Contains(sysUser, want) {
			t.Errorf("serviceAccount.md missing %q", want)
		}
	}
	if strings.Contains(sysUser, "`roleIds`") {
		t.Errorf("systemUser.md must not teach roleIds (authoring uses roleNames)")
	}

	scopes := body("references/rbac-scopes.md")
	for _, want := range []string{
		"<category>:<resource>:<verb>",
		"| `_` |",
		"workflow:<uuid>:invoke",
		"does **not** cover deployed workflows",
		"is read as `*`",
		"requires the universal grant",
		"/api/v2/platform/rbac/roles",
	} {
		if !strings.Contains(scopes, want) {
			t.Errorf("rbac-scopes.md missing %q", want)
		}
	}
	for _, gated := range []string{"resourceScopes", "/access", "rbac/scopes"} {
		if strings.Contains(scopes, gated) {
			t.Errorf("rbac-scopes.md references %q, which the rbac feature flag disables by default", gated)
		}
	}
	for _, want := range []string{"`agent:*:invoke`", "`agent_builder:_:create`", "`rbac:_:read`"} {
		if !strings.Contains(scopes, want) {
			t.Errorf("rbac-scopes.md catalog table missing generated entry %q", want)
		}
	}
	if !strings.Contains(scopes, "authorizes nothing") {
		t.Error("rbac-scopes.md must state that synonym verbs authorize nothing")
	}
}

func TestBuildTree_LayoutCoversEveryKindDirectory(t *testing.T) {
	tree, err := BuildTree("v1.0.0")
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}
	var layout string
	for _, f := range tree {
		if f.RelPath == "references/layout.md" {
			layout = string(f.Body)
		}
	}
	if layout == "" {
		t.Fatal("references/layout.md missing from tree")
	}

	for _, k := range kindOrder {
		dir := pluralDirForKind(k)
		if !strings.Contains(layout, dir+"/") {
			t.Errorf("layout.md does not show %q, the directory the resolver walks for kind %q", dir+"/", k)
		}
	}

	if !strings.Contains(layout, "not under a directory\nnamed after the key") {
		t.Error("layout.md must call out that the rbac kinds' directories are not named after their manifest keys")
	}
}

func TestParseFrontmatter_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path, err := Install(dir, "round-trip-1", false)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	fm, err := ParseFrontmatter(path)
	if err != nil {
		t.Fatalf("ParseFrontmatter: %v", err)
	}
	if fm.Name != SkillName {
		t.Errorf("name: got %q want %q", fm.Name, SkillName)
	}
	if fm.Version() != "round-trip-1" {
		t.Errorf("version: got %q want %q", fm.Version(), "round-trip-1")
	}
}

func TestParseFrontmatter_RejectsMissingHeader(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.md")
	if err := os.WriteFile(bad, []byte("no frontmatter here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseFrontmatter(bad); err == nil {
		t.Error("expected error on file without frontmatter")
	}
}

func TestParseFrontmatter_RejectsUnterminated(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.md")
	body := "---\nname: foo\nversion: 1\nbody never closes\n"
	if err := os.WriteFile(bad, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ParseFrontmatter(bad)
	if err == nil {
		t.Fatal("expected error on unterminated frontmatter")
	}
	if !strings.Contains(err.Error(), "unterminated") {
		t.Errorf("expected 'unterminated' in error, got %v", err)
	}
}

func TestInstall_WritesTreeOnDisk(t *testing.T) {
	dir := t.TempDir()
	if _, err := Install(dir, "v1", false); err != nil {
		t.Fatalf("Install: %v", err)
	}
	skillDir := filepath.Join(dir, SkillName)
	for _, want := range append([]string{"SKILL.md"}, expectedReferenceRelPaths()...) {
		full := filepath.Join(skillDir, want)
		if _, err := os.Stat(full); err != nil {
			t.Errorf("missing on disk: %s (%v)", want, err)
		}
	}
}

func TestInstall_ForceRemovesStaleReferences(t *testing.T) {
	dir := t.TempDir()
	if _, err := Install(dir, "v1", false); err != nil {
		t.Fatalf("Install: %v", err)
	}
	stale := filepath.Join(dir, SkillName, "references", "old-removed-kind.md")
	if err := os.WriteFile(stale, []byte("stale\n"), 0o644); err != nil {
		t.Fatalf("plant stale: %v", err)
	}
	if _, err := Install(dir, "v2", true); err != nil {
		t.Fatalf("Install --force: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("expected stale ref removed; stat err = %v", err)
	}
}

func TestCheckTree_ReportsMissingReferences(t *testing.T) {
	dir := t.TempDir()
	if _, err := Install(dir, "v1", false); err != nil {
		t.Fatalf("Install: %v", err)
	}
	target := filepath.Join(dir, SkillName, "references", "entrypoint.md")
	if err := os.Remove(target); err != nil {
		t.Fatalf("remove target: %v", err)
	}
	res, err := CheckTree(dir)
	if err != nil {
		t.Fatalf("CheckTree: %v", err)
	}
	if !slices.Contains(res.Missing, "entrypoint.md") {
		t.Errorf("expected entrypoint.md in Missing, got %v", res.Missing)
	}
}

func TestRenderGatewayExample_SecretPlaceholderFormat(t *testing.T) {
	s := typeschema.GatewayTypeSchema{
		Type: "slack",
		Fields: []typeschema.TypeFieldSchema{
			{Name: "bot_token", Type: "password", Required: true, Secret: true, MinLen: -1, MaxLen: -1},
			{Name: "app_token", Type: "password", Required: true, Secret: true, MinLen: -1, MaxLen: -1},
		},
	}
	var buf bytes.Buffer
	RenderGatewayExample(&buf, "wi18-slack", s)
	out := buf.String()
	for _, want := range []string{
		"${WI18_SLACK_BOT_TOKEN}",
		"${WI18_SLACK_APP_TOKEN}",
		"# secret",
		"spec:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered output missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestRenderTypeFieldsTable_ExpandsEventRules(t *testing.T) {
	fs := []typeschema.TypeFieldSchema{
		{Name: "event_rules", Type: "event_rules", Required: true, MinLen: -1, MaxLen: -1, Hint: "rules"},
	}
	var buf bytes.Buffer
	renderTypeFieldsTable(&buf, fs)
	out := buf.String()
	for _, want := range []string{
		"`event_rules`",
		"**Inner schema for `event_rules` (`event_rules`)**",
		"Each entry is an object with:",
		"`name`",
		"`subscriptions`",
		"`targetAgent`",
		"`targetWorkflowName`",
		"`promptTemplate`",
		"`acknowledgmentPolicy`",
		"`messageFormat`",
		"`inputExpression`",
		"[advanced]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("renderTypeFieldsTable expected %q in output:\n%s", want, out)
		}
	}
}

func TestRenderTypeFieldsTable_LeavesUnregisteredTypeAlone(t *testing.T) {
	fs := []typeschema.TypeFieldSchema{
		{Name: "agent_select", Type: "agent_select", Required: true, MinLen: -1, MaxLen: -1, Hint: "an agent"},
	}
	var buf bytes.Buffer
	renderTypeFieldsTable(&buf, fs)
	out := buf.String()
	if strings.Contains(out, "Inner schema for") {
		t.Errorf("non-registered type should not produce inner schema:\n%s", out)
	}
}

func TestRenderGatewayExample_EventRulesUsesRegistrySnippet(t *testing.T) {
	s, err := typeschema.LookupGatewayType("event_mesh")
	if err != nil {
		t.Skipf("platform does not expose event_mesh schema: %v", err)
	}
	var buf bytes.Buffer
	RenderGatewayExample(&buf, "wi19-eventmesh", s)
	out := buf.String()
	if strings.Contains(out, "null  # TODO") {
		t.Errorf("rendered example still has TODO fallback:\n%s", out)
	}
	for _, want := range []string{
		"event_rules:",
		"name: route_orders",
		"subscriptions:",
		"topic:",
		"targetAgent:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered example missing %q:\n%s", want, out)
		}
	}
}

func TestBuildTree_DesignReferencesEmittedAndByteIdentical(t *testing.T) {
	tree, err := BuildTree("v1.0.0")
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}
	byPath := map[string][]byte{}
	for _, f := range tree {
		byPath[f.RelPath] = f.Body
	}
	for _, sk := range design.Skills() {
		rel := "references/design/" + sk.Guide + ".md"
		body, ok := byPath[rel]
		if !ok {
			t.Errorf("BuildTree missing %s", rel)
			continue
		}
		want, err := sk.Body(design.CLI)
		if err != nil {
			t.Fatalf("%s.Body(CLI): %v", sk.Name, err)
		}
		if !bytes.Equal(body, want) {
			t.Errorf("%s drift: configapply emit (%d bytes) differs from design body (%d bytes)",
				rel, len(body), len(want))
		}
		if strings.HasPrefix(string(body), "---\n") {
			t.Errorf("%s: body carries frontmatter (renders as literal text in a reference)", rel)
		}
	}
}

func TestBuildSkill_DesignGuidanceSectionPresent(t *testing.T) {
	body, err := BuildSkill("v0.0.0")
	if err != nil {
		t.Fatalf("BuildSkill: %v", err)
	}
	src := string(body)
	for _, want := range []string{
		"## Design guidance",
		"references/design/best-practices.md",
		"references/design/concepts.md",
		"references/design/agent-design.md",
		"references/design/workflow-design.md",
		"references/design/skill-design.md",
		"references/design/efficiency.md",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("BuildSkill output missing %q", want)
		}
	}
}

func TestCheckTree_ReportsMissingDesignReference(t *testing.T) {
	dir := t.TempDir()
	if _, err := Install(dir, "v1", false); err != nil {
		t.Fatalf("Install: %v", err)
	}
	target := filepath.Join(dir, SkillName, "references", "design", "best-practices.md")
	if err := os.Remove(target); err != nil {
		t.Fatalf("remove target: %v", err)
	}
	res, err := CheckTree(dir)
	if err != nil {
		t.Fatalf("CheckTree: %v", err)
	}
	if !slices.Contains(res.Missing, "design/best-practices.md") {
		t.Errorf("expected design/best-practices.md in Missing, got %v", res.Missing)
	}
}

func TestBuildTree_GatewayReferenceExpandsEventRules(t *testing.T) {
	tree, err := BuildTree("v1.0.0")
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}
	var body string
	for _, f := range tree {
		if f.RelPath == "references/entrypoint.md" {
			body = string(f.Body)
			break
		}
	}
	if body == "" {
		t.Fatal("entrypoint.md missing from tree")
	}
	for _, want := range []string{
		"## type: event_mesh",
		"**Inner schema for `event_rules` (`event_rules`)**",
		"Each entry is an object with:",
		"`promptTemplate`",
		"name: route_orders",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("gateway.md missing %q (event_rules expansion regression?)", want)
		}
	}
}

func expectedReferenceRelPaths() []string {
	out := make([]string, 0, len(expectedReferences))
	for _, name := range expectedReferences {
		out = append(out, filepath.Join("references", name))
	}
	return out
}

func TestRBACScopesReference_OmitsScopesOfDisabledFeatures(t *testing.T) {
	t.Setenv("SAM_FEATURE_NOTIFY_SERVICE", "false")
	t.Setenv("SAM_FEATURE_OBSERVABILITY_AGENT", "false")
	t.Setenv("SAM_FEATURE_EVENT_CATALOG", "false")
	off := string(renderRBACScopesReference())
	for _, scope := range []string{"`notify:_:send`", "`analytics:_:read`", "`event_catalog:*:read`", "`event_catalog:*:create`", "`event_catalog:*:update`", "`event_catalog:*:delete`"} {
		if strings.Contains(off, scope) {
			t.Errorf("rbac-scopes.md lists %s although its feature is off", scope)
		}
	}
	for _, scope := range []string{"`rbac:_:read`", "`budget:*:read`"} {
		if !strings.Contains(off, scope) {
			t.Errorf("rbac-scopes.md dropped %s, which no feature owns", scope)
		}
	}

	t.Setenv("SAM_FEATURE_NOTIFY_SERVICE", "true")
	t.Setenv("SAM_FEATURE_OBSERVABILITY_AGENT", "true")
	t.Setenv("SAM_FEATURE_EVENT_CATALOG", "true")
	on := string(renderRBACScopesReference())
	for _, scope := range []string{"`notify:_:send`", "`analytics:_:read`", "`event_catalog:*:read`", "`event_catalog:*:delete`", "`budget:*:read`"} {
		if !strings.Contains(on, scope) {
			t.Errorf("rbac-scopes.md omits %s although its feature is on", scope)
		}
	}
	for _, scope := range []string{"`budget:*:create`", "`budget:*:update`", "`budget:*:delete`", "`budget:*:assign`"} {
		if strings.Contains(on, "| "+scope+" |") {
			t.Errorf("rbac-scopes.md lists %s in the catalog, which is not assignable", scope)
		}
		if !strings.Contains(on, scope) {
			t.Errorf("rbac-scopes.md does not name %s among the enforced scopes outside the catalog", scope)
		}
	}
}

func TestRenderConnectorExample_OmitsFieldsHiddenByChosenValues(t *testing.T) {
	s, err := typeschema.LookupConnectorType("api", "")
	if err != nil {
		t.Fatalf("lookup api connector: %v", err)
	}
	var buf bytes.Buffer
	RenderConnectorExample(&buf, "example_api", s)
	out := buf.String()
	if !strings.Contains(out, `auth_type: "none"`) {
		t.Fatalf("expected the api example to choose auth_type none\n--- output ---\n%s", out)
	}
	for _, hidden := range []string{"auth_apikey_value", "auth_http_bearer_token", "auth_http_basic_password", "auth_oauth_client_secret"} {
		if strings.Contains(out, hidden) {
			t.Errorf("api example under auth_type none still emits %q\n--- output ---\n%s", hidden, out)
		}
	}
}

var (
	bundleInlineCodeRe = regexp.MustCompile("`[^`]*`")
	bundleSAMEnvRe     = regexp.MustCompile(`SAM_[A-Z0-9_]+`)
	bundleBareSAMRe    = regexp.MustCompile(`\bSAM\b`)
)

func bundleBareSAMLines(body string) []int {
	var out []int
	inFence := false
	for i, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence || !strings.Contains(line, "SAM") {
			continue
		}
		stripped := bundleInlineCodeRe.ReplaceAllString(line, "")
		stripped = strings.ReplaceAll(stripped, "SAM MCP Entrypoint", "")
		stripped = bundleSAMEnvRe.ReplaceAllString(stripped, "")
		if bundleBareSAMRe.MatchString(stripped) {
			out = append(out, i+1)
		}
	}
	return out
}

func TestBuildTree_NoBareSAMAcronym(t *testing.T) {
	files, err := BuildTree("test")
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}
	for _, f := range files {
		for _, line := range bundleBareSAMLines(string(f.Body)) {
			t.Errorf(`%s:%d uses the bare acronym "SAM" — customer prose says "Solace Agent Mesh" on first reference and "Agent Mesh" after`, f.RelPath, line)
		}
	}
}

func TestBuildTree_FailsForAHandDefinedKindWithoutProse(t *testing.T) {
	saved := handDefinedReferenceKinds
	t.Cleanup(func() { handDefinedReferenceKinds = saved })
	handDefinedReferenceKinds = append(slices.Clone(saved), codegen.KindSchema{Kind: "noProse", PluralPath: "noProses"})

	if _, err := BuildTree("test"); err == nil || !strings.Contains(err.Error(), "static/per-kind/noProse.md") {
		t.Fatalf("BuildTree err = %v, want one naming the missing static/per-kind/noProse.md", err)
	}
}
