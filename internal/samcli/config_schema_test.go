// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/skill"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
)

func runRoot(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := newRootCmd()
	root.SilenceUsage = true
	root.SilenceErrors = true
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

func TestConfigSchemaList(t *testing.T) {
	out, _, err := runRoot(t, "config", "schema", "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, want := range []string{"manifest", "model", "agent", "entrypoint", "workflow", "toolset", "connector", "budget"} {
		if !strings.Contains(out, "`"+want+"`") {
			t.Errorf("list output missing kind %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestConfigSchemaListJSON(t *testing.T) {
	out, _, err := runRoot(t, "config", "schema", "list", "--format", "json")
	if err != nil {
		t.Fatalf("list --format json: %v", err)
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	if len(items) < 5 {
		t.Errorf("expected ≥5 kinds, got %d", len(items))
	}
	if items[0]["kind"] != "manifest" {
		t.Errorf("expected first kind manifest, got %v", items[0]["kind"])
	}
}

func TestConfigSchemaShowAgent(t *testing.T) {
	out, _, err := runRoot(t, "config", "schema", "show", "agent")
	if err != nil {
		t.Fatalf("show agent: %v", err)
	}
	for _, want := range []string{
		"# Kind: `agent`",
		"Manifest path: `resources.agents`",
		"`name`",
		"`description`",
		"`systemPrompt`",
		"one of: standard",
		"len 3–255",
		"References other kinds:",
		"connector",
		"skill",
		"toolset",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("show agent missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestConfigSchemaShowLinkAgent(t *testing.T) {
	out, _, err := runRoot(t, "config", "schema", "show", "linkAgent")
	if err != nil {
		t.Fatalf("show linkAgent: %v", err)
	}
	for _, want := range []string{
		"# Kind: `linkAgent`",
		"Manifest path: `resources.linkAgents`",
		"| `entrypoint` | `string` | yes |",
		"| `tokenSha256` | `string` | yes | max 64; secret |",
		"sam-link-sdk link-token",
		"| `wake` | `object` |  |",
		"References other kinds:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("show linkAgent missing %q\n--- output ---\n%s", want, out)
		}
	}
	if strings.Contains(out, "`entrypointId`") {
		t.Errorf("show linkAgent names the API's entrypointId, want the authoring key entrypoint\n--- output ---\n%s", out)
	}
}

func TestConfigSchemaListIncludesLinkAgent(t *testing.T) {
	out, _, err := runRoot(t, "config", "schema", "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "| `linkAgent` | `linkAgents` |") {
		t.Errorf("list output missing the linkAgent kind\n--- output ---\n%s", out)
	}
}

func TestConfigSchemaShowRBACRole(t *testing.T) {
	out, _, err := runRoot(t, "config", "schema", "show", "rbacRole")
	if err != nil {
		t.Fatalf("show rbacRole: %v", err)
	}
	for _, want := range []string{
		"# Kind: `rbacRole`",
		"Manifest path: `resources.rbacRoles`",
		"`scopes`",
		"`inherits`",
		"`users`",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("show rbacRole missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestConfigSchemaShowRBACClaimMapping(t *testing.T) {
	out, _, err := runRoot(t, "config", "schema", "show", "rbacClaimMapping")
	if err != nil {
		t.Fatalf("show rbacClaimMapping: %v", err)
	}
	for _, want := range []string{
		"# Kind: `rbacClaimMapping`",
		"Manifest path: `resources.rbacClaimMappings`",
		"`oidcProvider`",
		"`claimValue`",
		"`roleNames`",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("show rbacClaimMapping missing %q\n--- output ---\n%s", want, out)
		}
	}
	if strings.Contains(out, "`roleIds`") {
		t.Errorf("show rbacClaimMapping must not teach roleIds (authoring uses roleNames)\n--- output ---\n%s", out)
	}
	if _, _, err := runRoot(t, "config", "schema", "show", "rbacAssignment"); err == nil {
		t.Error("expected 'unknown kind' for the removed rbacAssignment kind")
	}
}

func TestConfigSchemaShowRBACGrant(t *testing.T) {
	out, _, err := runRoot(t, "config", "schema", "show", "rbacGrant")
	if err != nil {
		t.Fatalf("show rbacGrant: %v", err)
	}
	for _, want := range []string{
		"# Kind: `rbacGrant`",
		"Manifest path: `resources.rbacGrants`",
		"`roleName`",
		"`users`",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("show rbacGrant missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestConfigSchemaShowSystemUser(t *testing.T) {
	out, stderr, err := runRoot(t, "config", "schema", "show", "serviceAccount")
	if err != nil {
		t.Fatalf("show serviceAccount: %v", err)
	}
	if strings.Contains(stderr, legacyServiceAccountKindWarning) {
		t.Errorf("show serviceAccount warned about the old kind name: %q", stderr)
	}
	legacyOut, legacyStderr, err := runRoot(t, "config", "schema", "show", "systemUser")
	if err != nil {
		t.Fatalf("show systemUser: %v", err)
	}
	if diff := cmp.Diff(out, legacyOut); diff != "" {
		t.Errorf("show systemUser must print the serviceAccount schema (-serviceAccount +systemUser):\n%s", diff)
	}
	if !strings.Contains(legacyStderr, legacyServiceAccountKindWarning) {
		t.Errorf("show systemUser stderr = %q, want the old-name warning", legacyStderr)
	}
	for _, want := range []string{
		"# Kind: `serviceAccount`",
		"Manifest path: `resources.serviceAccounts`",
		"`name`",
		"`displayName`",
		"`roleNames`",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("show serviceAccount missing %q\n--- output ---\n%s", want, out)
		}
	}
	if strings.Contains(out, "`roleIds`") {
		t.Errorf("show serviceAccount must not teach roleIds (authoring uses roleNames)\n--- output ---\n%s", out)
	}
	for _, want := range []string{"revokes", "prune", "rename"} {
		if !strings.Contains(out, want) {
			t.Errorf("show serviceAccount must state the %q semantics\n--- output ---\n%s", want, out)
		}
	}
}

func TestConfigSchemaExampleSystemUser(t *testing.T) {
	out, _, err := runRoot(t, "config", "schema", "example", "serviceAccount", "--name", "ci-bot")
	if err != nil {
		t.Fatalf("example systemUser: %v", err)
	}
	for _, want := range []string{
		"kind: serviceAccount",
		"name: ci-bot",
		"spec:",
		"roleNames:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("example systemUser missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestConfigSchemaExampleRBACGrant(t *testing.T) {
	out, _, err := runRoot(t, "config", "schema", "example", "rbacGrant", "--name", "analyst-grant")
	if err != nil {
		t.Fatalf("example rbacGrant: %v", err)
	}
	for _, want := range []string{
		"kind: rbacGrant",
		"name: analyst-grant",
		"roleName:",
		"users:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("example rbacGrant missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestConfigSchemaListIncludesEveryRBACKind(t *testing.T) {
	out, _, err := runRoot(t, "config", "schema", "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, want := range []string{"`rbacRole`", "`rbacClaimMapping`", "`rbacGrant`", "`serviceAccount`"} {
		if !strings.Contains(out, want) {
			t.Errorf("list missing %s\n--- output ---\n%s", want, out)
		}
	}
	if strings.Contains(out, "`rbacAssignment`") {
		t.Errorf("list must not surface the removed rbacAssignment kind\n--- output ---\n%s", out)
	}
}

func TestConfigSchemaShowUnknown(t *testing.T) {
	_, _, err := runRoot(t, "config", "schema", "show", "nope")
	if err == nil {
		t.Fatal("expected error for unknown kind")
	}
	if !strings.Contains(err.Error(), "unknown kind") {
		t.Errorf("expected 'unknown kind' in error, got %v", err)
	}
}

func TestConfigSchemaExampleAgent(t *testing.T) {
	out, _, err := runRoot(t, "config", "schema", "example", "agent", "--name", "my-agent")
	if err != nil {
		t.Fatalf("example: %v", err)
	}
	for _, want := range []string{
		"kind: agent",
		"name: my-agent",
		"description:",
		"spec:",
		"systemPrompt:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("example missing %q\n--- output ---\n%s", want, out)
		}
	}
	if strings.Contains(out, "# optional: systemPrompt:") {
		t.Errorf("systemPrompt should be required (no # optional prefix); got:\n%s", out)
	}
}

func TestConfigSchemaManifest(t *testing.T) {
	out, _, err := runRoot(t, "config", "schema", "manifest")
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	for _, want := range []string{
		"# Kind: `manifest`",
		"`kind`",
		"one of: manifest",
		"## Example",
		"```yaml",
		"kind: manifest",
		"`platform`",
		"defaultRoles",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("manifest missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestConfigSchemaManifestExampleIsLoadable(t *testing.T) {
	out, _, err := runRoot(t, "config", "schema", "manifest")
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	example := fencedYAMLBlock(t, out)

	m, err := manifest.Parse([]byte(example))
	if err != nil {
		t.Fatalf("the printed example does not parse as a manifest: %v\n--- example ---\n%s", err, example)
	}

	t.Setenv("SAM_PLATFORM_TOKEN", "")
	if m.Target.Auth.EnvVar != "" {
		t.Setenv(m.Target.Auth.EnvVar, "dummy-token")
	}

	tg := target.Target{
		Name: m.Target.Name,
		URL:  m.Target.URL,
		Auth: target.AuthSpec{
			Type:   target.AuthType(m.Target.Auth.Type),
			EnvVar: m.Target.Auth.EnvVar,
		},
	}
	if _, err := tg.Resolve(t.Context(), nil); err != nil {
		t.Errorf("the printed example's target does not resolve: %v\n--- example ---\n%s", err, example)
	}
}

func fencedYAMLBlock(t *testing.T, out string) string {
	t.Helper()
	_, after, found := strings.Cut(out, "```yaml\n")
	if !found {
		t.Fatalf("no ```yaml fence in output:\n%s", out)
	}
	body, _, found := strings.Cut(after, "```")
	if !found {
		t.Fatalf("unterminated ```yaml fence in output:\n%s", out)
	}
	return body
}

func TestConfigSchemaLayout(t *testing.T) {
	out, _, err := runRoot(t, "config", "schema", "layout")
	if err != nil {
		t.Fatalf("layout: %v", err)
	}
	if !strings.Contains(out, "Directory Layout") {
		t.Errorf("layout output missing header\n--- output ---\n%s", out)
	}
}

func TestConfigSchemaShowGatewayWithType(t *testing.T) {
	out, _, err := runRoot(t, "config", "schema", "show", "entrypoint", "--type", "slack")
	if err != nil {
		t.Fatalf("show gateway --type slack: %v", err)
	}
	for _, want := range []string{
		"## type: slack",
		"`bot_token`",
		"`app_token`",
		"secret",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("show gateway --type slack missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestConfigSchemaShowGatewayLists(t *testing.T) {
	out, _, err := runRoot(t, "config", "schema", "show", "entrypoint")
	if err != nil {
		t.Fatalf("show gateway: %v", err)
	}
	for _, want := range []string{
		"# Kind: `entrypoint`",
		"## Entrypoint types",
		"- `slack` —",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("show gateway missing %q", want)
		}
	}
}

func TestConfigSchemaShowLegacyGatewayKindRejected(t *testing.T) {
	_, _, err := runRoot(t, "config", "schema", "show", "gateway")
	if err == nil {
		t.Fatal("expected error for legacy kind \"gateway\"")
	}
	if !strings.Contains(err.Error(), `renamed to "entrypoint"`) {
		t.Errorf("expected rename hint, got: %v", err)
	}
}

func TestConfigSchemaExampleLegacyGatewayKindRejected(t *testing.T) {
	_, _, err := runRoot(t, "config", "schema", "example", "gateway")
	if err == nil {
		t.Fatal("expected error for legacy kind \"gateway\"")
	}
	if !strings.Contains(err.Error(), `renamed to "entrypoint"`) {
		t.Errorf("expected rename hint, got: %v", err)
	}
}

func TestConfigSchemaShowConnectorWithType(t *testing.T) {
	out, _, err := runRoot(t, "config", "schema", "show", "connector", "--type", "sql", "--subtype", "postgres")
	if err != nil {
		t.Fatalf("show connector --type sql --subtype postgres: %v", err)
	}
	for _, want := range []string{
		"## type: sql",
		"### subtype: postgres",
		"`password`",
		"`database`",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("show connector --type sql --subtype postgres missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestConfigSchemaShowWorkflowNodeType(t *testing.T) {
	out, _, err := runRoot(t, "config", "schema", "show", "workflow", "--type", "map")
	if err != nil {
		t.Fatalf("show workflow --type map: %v", err)
	}
	for _, want := range []string{
		"## node type: map",
		"`items`",
		"`node`",
		"`max_items`",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("show workflow --type map missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestConfigSchemaShowWorkflowDefaults(t *testing.T) {
	out, _, err := runRoot(t, "config", "schema", "show", "workflow")
	if err != nil {
		t.Fatalf("show workflow: %v", err)
	}
	for _, want := range []string{
		"## Workflow definition",
		"## Node types",
		"- `agent`",
		"- `switch`",
		"- `loop`",
		"- `map`",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("show workflow missing %q", want)
		}
	}
}

func TestConfigSchemaExampleGateway(t *testing.T) {
	out, _, err := runRoot(t, "config", "schema", "example", "entrypoint", "--type", "slack", "--name", "slackbot")
	if err != nil {
		t.Fatalf("example gateway slack: %v", err)
	}
	for _, want := range []string{
		"kind: entrypoint",
		"name: slackbot",
		"spec:",
		"  type: slack",
		"  values:",
		"bot_token: ${SLACKBOT_BOT_TOKEN}",
		"app_token: ${SLACKBOT_APP_TOKEN}",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("example gateway slack missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestConfigSchemaExampleGateway_TypeOmitted(t *testing.T) {
	out, _, err := runRoot(t, "config", "schema", "example", "entrypoint")
	if err != nil {
		t.Fatalf("example gateway: %v", err)
	}
	if !strings.Contains(out, "# --type omitted; using first known entrypoint type") {
		t.Errorf("expected omitted-type comment, got:\n%s", out)
	}
	if !strings.Contains(out, "kind: entrypoint") {
		t.Errorf("expected kind: entrypoint, got:\n%s", out)
	}
}

func TestConfigSchemaExampleConnector(t *testing.T) {
	out, _, err := runRoot(t, "config", "schema", "example", "connector",
		"--type", "sql", "--subtype", "postgres", "--name", "pg")
	if err != nil {
		t.Fatalf("example connector sql/postgres: %v", err)
	}
	for _, want := range []string{
		"kind: connector",
		"name: pg",
		"spec:",
		"  type: sql",
		"  subtype: postgres",
		"password: ${PG_PASSWORD}",
		"database: ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("example connector sql/postgres missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestConfigSchemaExampleWorkflow(t *testing.T) {
	out, _, err := runRoot(t, "config", "schema", "example", "workflow", "--name", "demo")
	if err != nil {
		t.Fatalf("example workflow: %v", err)
	}
	for _, want := range []string{
		"kind: workflow",
		"name: demo",
		"spec:",
		"  appConfig:",
		"output_mapping:",
		"nodes:",
		"type: agent",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("example workflow missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestConfigSchemaExampleWorkflowReferencesTextInput(t *testing.T) {
	out, _, err := runRoot(t, "config", "schema", "example", "workflow")
	if err != nil {
		t.Fatalf("example workflow: %v", err)
	}
	if !strings.Contains(out, `"{{workflow.input.text}}"`) {
		t.Errorf("example workflow must read the A2A text input via workflow.input.text\n--- output ---\n%s", out)
	}
	if strings.Contains(out, "workflow.input.message") {
		t.Errorf("example workflow references workflow.input.message, which the runtime never sets\n--- output ---\n%s", out)
	}
}

func TestConfigSchemaShowGatewayEventMeshExpandsRules(t *testing.T) {
	out, _, err := runRoot(t, "config", "schema", "show", "entrypoint", "--type", "event_mesh")
	if err != nil {
		t.Fatalf("show gateway --type event_mesh: %v", err)
	}
	for _, want := range []string{
		"## type: event_mesh",
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
		"raw_bytes",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("show gateway --type event_mesh missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestConfigSchemaExampleGatewayEventMesh(t *testing.T) {
	out, _, err := runRoot(t, "config", "schema", "example", "entrypoint", "--type", "event_mesh", "--name", "myem")
	if err != nil {
		t.Fatalf("example gateway event_mesh: %v", err)
	}
	if strings.Contains(out, "null  # TODO") {
		t.Errorf("event_mesh example still has TODO fallback for event_rules:\n%s", out)
	}
	for _, want := range []string{
		"kind: entrypoint",
		"name: myem",
		"  type: event_mesh",
		"event_rules:",
		"name: route_orders",
		"subscriptions:",
		"topic:",
		"targetAgent:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("example gateway event_mesh missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestConfigSchemaShowJSON(t *testing.T) {
	out, _, err := runRoot(t, "config", "schema", "show", "model", "--format", "json")
	if err != nil {
		t.Fatalf("show model json: %v", err)
	}
	var ks map[string]any
	if err := json.Unmarshal([]byte(out), &ks); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	if ks["Kind"] != "model" {
		t.Errorf("expected Kind=model, got %v", ks["Kind"])
	}
	fields, _ := ks["Fields"].([]any)
	if len(fields) == 0 {
		t.Fatalf("expected fields in JSON, got %s", out)
	}
	for _, raw := range fields {
		f, _ := raw.(map[string]any)
		if _, ok := f["GoType"]; ok {
			t.Errorf("field JSON leaks GoType key: %v", f)
		}
		if _, ok := f["type"]; !ok {
			t.Errorf("field JSON missing friendly type key: %v", f)
		}
	}
}

func TestConfigSchemaShowEntrypoint_HidesInternalDetails(t *testing.T) {
	out, _, err := runRoot(t, "config", "schema", "show", "entrypoint")
	if err != nil {
		t.Fatalf("show entrypoint: %v", err)
	}
	for _, want := range []string{"# Kind: `entrypoint`", "- `slack`", "- `whatsapp`"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected released entrypoint schema content %q, got:\n%s", want, out)
		}
	}
	for _, banned := range []string{
		"CreateGatewayRequest",
		"POST /entrypoints",
		"map[string]any",
		"- `email`",
		"- `google_chat`",
	} {
		if strings.Contains(out, banned) {
			t.Errorf("entrypoint schema leaks %q:\n%s", banned, out)
		}
	}
}

func TestConfigSchemaExampleEntrypoint_DefaultTypeNotUnreleased(t *testing.T) {
	out, _, err := runRoot(t, "config", "schema", "example", "entrypoint")
	if err != nil {
		t.Fatalf("example entrypoint: %v", err)
	}
	if strings.Contains(out, "type: email") || strings.Contains(out, "type: whatsapp") || strings.Contains(out, "type: google_chat") {
		t.Errorf("default example must not use an unreleased type:\n%s", out)
	}
}

func TestConfigSchemaExamplesResolve(t *testing.T) {
	type kindLayout struct {
		resourcesKey string
		dir          string
		resolve      func(root string, m *manifest.Manifest) error
	}
	resolved := func(fn func(string, *manifest.Manifest) ([]resolve.ResolvedResource, error)) func(string, *manifest.Manifest) error {
		return func(root string, m *manifest.Manifest) error { _, err := fn(root, m); return err }
	}
	resolveToolsets := func(root string, m *manifest.Manifest) error { _, err := resolve.ResolveToolsets(root, m); return err }
	layouts := map[string]kindLayout{
		"model":            {"models", "models", resolved(resolve.ResolveModels)},
		"agent":            {"agents", "agents", resolved(resolve.ResolveAgents)},
		"entrypoint":       {"entrypoints", "entrypoints", resolved(resolve.ResolveGateways)},
		"linkAgent":        {"linkAgents", "linkAgents", resolved(resolve.ResolveLinkAgents)},
		"workflow":         {"workflows", "workflows", resolved(resolve.ResolveWorkflows)},
		"connector":        {"connectors", "connectors", resolved(resolve.ResolveConnectors)},
		"toolset":          {"toolsets", "toolsets", resolveToolsets},
		"dataset":          {"datasets", "datasets", resolved(resolve.ResolveDatasets)},
		"evaluator":        {"evaluators", "evaluators", resolved(resolve.ResolveEvaluators)},
		"experiment":       {"experiments", "experiments", resolved(resolve.ResolveExperiments)},
		"rbacRole":         {"rbacRoles", filepath.Join("rbac", "roles"), resolved(resolve.ResolveRBACRoles)},
		"rbacGrant":        {"rbacGrants", filepath.Join("rbac", "grants"), resolved(resolve.ResolveRBACGrants)},
		"rbacClaimMapping": {"rbacClaimMappings", filepath.Join("rbac", "claim-mappings"), resolved(resolve.ResolveRBACClaimMappings)},
		"serviceAccount":   {"serviceAccounts", filepath.Join("rbac", "service-accounts"), resolved(resolve.ResolveServiceAccounts)},
		"budget": {"budgets", "budgets", func(root string, m *manifest.Manifest) error {
			_, err := resolve.New(root, m, resolve.Options{}).Budgets(t.Context())
			return err
		}},
	}
	placeholder := regexp.MustCompile(`\$\{([A-Z0-9_]+)\}`)
	const name = "example_res"
	for _, ks := range allKinds() {
		if ks.Kind == "manifest" {
			continue
		}
		layout, ok := layouts[ks.Kind]
		if !ok {
			t.Errorf("kind %q has no resolve layout in this test; add one", ks.Kind)
			continue
		}
		var rendered bytes.Buffer
		skill.RenderExample(&rendered, ks, name)
		cliOut, _, err := runRoot(t, "config", "schema", "example", ks.Kind, "--name", name)
		if err != nil {
			t.Fatalf("schema example %s: %v", ks.Kind, err)
		}
		for source, example := range map[string]string{"skill": rendered.String(), "cli": cliOut} {
			t.Run(ks.Kind+"/"+source, func(t *testing.T) {
				for _, m := range placeholder.FindAllStringSubmatch(example, -1) {
					t.Setenv(m[1], "placeholder")
				}
				root := t.TempDir()
				dir := filepath.Join(root, layout.dir)
				if err := os.MkdirAll(filepath.Join(dir, name, "src"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(example), 0o600); err != nil {
					t.Fatal(err)
				}
				m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{layout.resourcesKey: {{Name: name}}}}
				if err := layout.resolve(root, m); err != nil {
					t.Errorf("example does not resolve: %v\n--- example ---\n%s", err, example)
				}
			})
		}
	}
}
