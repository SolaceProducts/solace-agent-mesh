// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const roundTripWorkflow = `kind: workflow
name: marker-workflow
description: Marker workflow that runs the marker agent.
spec:
  appConfig:
    nodes:
      - id: answer
        type: agent
        agent_name: marker-agent
        input:
          question: "{{workflow.input.question}}"
    output_mapping:
      result: "{{answer.output}}"
`

const roundTripAgentAdditions = `  connectors: [marker-postgres]
  skills:
    - name: Lookup
      description: Looks up an answer for the user.
`

const roundTripModelConfigID = "0190f5e7-7a8b-7c9d-8e0f-1a2b3c4d5e6f"

var roundTripEval = map[string]string{
	"datasets/marker-dataset.yaml": `kind: dataset
name: marker-dataset
description: Marker dataset
spec:
  examples_file: marker-dataset.examples.csv
`,
	"datasets/marker-dataset.examples.csv": `sequence_number,prompt,expected_response
1,What is 2 + 2?,4
2,Say hello,
`,
	"datasets/marker-workflow-dataset.yaml": `kind: dataset
name: marker-workflow-dataset
description: Marker workflow dataset
spec:
  examples_file: marker-workflow-dataset.examples.csv
`,
	"datasets/marker-workflow-dataset.examples.csv": "sequence_number,prompt,expected_response,input_data,expected_path,expected_facts\n" +
		`1,,,"{""question"":""What is 2 + 2?""}","{""nodes"":{""answer"":""succeeded""},""tools"":{""peer_marker-agent"":""called""}}","{""required"":[""4""],""forbidden"":[""five""]}"` + "\n",
	"experiments/marker-workflow-experiment.yaml": `kind: experiment
name: marker-workflow-experiment
description: Marker workflow experiment
spec:
  datasetId: marker-workflow-dataset
  targetWorkflow: marker-workflow
  evaluatorIds: [marker-judge]
  runAs: marker-workflow-evals
`,
	"evaluators/marker-judge.yaml": `kind: evaluator
name: marker-judge
description: Marker judge
spec:
  type: llm_judge
  model: marker-model
  promptTemplate: "Is the answer right? {{ Response }}"
  choiceScores:
    "yes": 1
    "no": 0
`,
	"experiments/marker-experiment.yaml": `kind: experiment
name: marker-experiment
description: Marker experiment
spec:
  datasetId: marker-dataset
  targetAgent: marker-agent
  evaluatorIds: [marker-judge]
  primaryEvaluatorId: marker-judge
  runsPerExample: 2
  runAs: marker-evals
  models:
    - modelConfigId: ` + roundTripModelConfigID + `
`,
}

var roundTripWorkflowRunAs = map[string]string{
	"rbac/roles/marker-workflow-invoke.yaml": `kind: rbacRole
name: marker-workflow-invoke
spec:
  scopes:
    - "workflow:*:invoke"
`,
	"rbac/system-users/marker-workflow-evals.yaml": `kind: systemUser
name: marker-workflow-evals
spec:
  roleNames:
    - marker-workflow-invoke
`,
}

const roundTripMixedCaseModel = `kind: model
name: marker-mixed-case-model
spec:
  provider: OpenAI
  modelName: gpt-4o
  apiBase: https://api.openai.com/v1
  authConfig:
    type: apikey
    api_key: sk-mixed-case
`

const roundTripManifest = `kind: manifest
name: marker
target:
  url: {{url}}
  auth:
    type: bearer_token
    envVar: SAM_PLATFORM_TOKEN
resources:
  models: [marker-model, marker-switch-model, marker-mixed-case-model]
  connectors: [marker-postgres]
  entrypoints: [marker-slack]
  skills: [marker-skill, ` + markerToolSkill + `]
  toolsets: [` + markerToolset + `]
  agents: [marker-agent]
  workflows: [marker-workflow]
  datasets: [marker-dataset, marker-workflow-dataset]
  evaluators: [marker-judge]
  experiments: [marker-experiment, marker-workflow-experiment]
  rbacRoles: [marker-operator, marker-reader, marker-workflow-invoke]
  rbacGrants: [marker-readers]
  rbacClaimMappings: [marker-admins]
  systemUsers: [marker-bot, marker-ci, marker-workflow-evals]
platform:
  defaultRoles:
    roles: [marker-reader]
  webuiSettings:
    appName: Marker Assistant
    collectFeedback: false
    disclaimerText: ""
  profileProvider:
    toolset: ` + roundTripProfileToolset + `
`

const roundTripProfileToolset = "marker-profile"

var roundTripRBAC = map[string]string{
	"rbac/roles/marker-operator.yaml": `kind: rbacRole
name: marker-operator
description: Operates the marker agents.
spec:
  scopes:
    - "` + rbac.ScopeAgentBuilderRead + `"
  users:
    - jane.doe@example.com
    - type: email
      value: ops@example.com
    - type: sub
      value: u-123
      issuer: https://idp.example.com
`,
	"rbac/roles/marker-reader.yaml": `kind: rbacRole
name: marker-reader
spec:
  scopes:
    - "` + rbac.ScopeEvaluationInvoke + `"
  inherits: [marker-operator]
`,
	"rbac/grants/marker-readers.yaml": `kind: rbacGrant
name: marker-readers
spec:
  roleName: marker-reader
  users:
    - reader@example.com
`,
	"rbac/claim-mappings/marker-admins.yaml": `kind: rbacClaimMapping
name: marker-admins
spec:
  oidcProvider: azure
  claimValue: marker-admins
  roleNames: [marker-operator]
`,
	"rbac/system-users/marker-bot.yaml": `kind: systemUser
name: marker-bot
spec:
  displayName: Marker Bot
  roleNames: [marker-operator]
`,
	"rbac/system-users/marker-ci.yaml": `kind: systemUser
name: marker-ci
spec:
  roleNames: [marker-reader]
`,
}

func TestConfig_UnchangedRepoPlansNoChanges(t *testing.T) {
	rt := newRoundTrip(t)
	rt.apply()
	rt.assertNoChanges("the plan right after apply")
	rt.assertWritesOnV2()
	if got := rt.plan().Summary.Redeploy; got != 3 {
		t.Errorf("redeploys planned after a --no-deploy apply = %d, want the agent, workflow and entrypoint", got)
	}
}

func TestConfig_OverlaysRemovedFromYAMLAreCleared(t *testing.T) {
	rt := newRoundTrip(t)
	rt.apply()

	rt.write("agents/marker-agent.yaml", `kind: agent
name: marker-agent
description: Marker agent
spec:
  systemPrompt: You are a marker agent.
  modelProvider: [marker-model]
  toolsets: [`+markerToolset+`]
  skillRefs: [`+markerToolSkill+`]
`+roundTripAgentAdditions)
	if got := rt.plan(); got.Summary.Update != 1 {
		t.Fatalf("plan after removing the overlays = %+v, want one update", got.Changes)
	}
	rt.apply()
	rt.assertNoChanges("the plan after clearing the overlays")

	agent := rt.getAgent(platform.APIV2Prefix)
	if !isEmptyJSON(agent["toolsetConfigs"]) || !isEmptyJSON(agent["skillConfigs"]) {
		t.Errorf("stored overlays after clearing: toolsetConfigs %v, skillConfigs %v", agent["toolsetConfigs"], agent["skillConfigs"])
	}
}

func TestConfig_EntrypointPublicURLRemovedFromYAMLIsCleared(t *testing.T) {
	const after, line = "  type: slack\n", "  publicUrl: https://slack.example.com\n"
	rt := newRoundTrip(t)
	slack := strings.Replace(rt.files["entrypoints/marker-slack.yaml"], after, after+line, 1)
	rt.write("entrypoints/marker-slack.yaml", slack)
	rt.apply()
	rt.assertNoChanges("the plan after setting publicUrl")

	rt.write("entrypoints/marker-slack.yaml", strings.Replace(slack, line, "", 1))
	if got := rt.plan(); got.Summary.Update != 1 {
		t.Fatalf("plan after removing publicUrl = %+v, want one update", got.Changes)
	}
	rt.apply()
	rt.assertNoChanges("the plan after removing publicUrl")
}

func TestConfig_EntrypointValueRemovedFromYAMLIsKept(t *testing.T) {
	const after, line = "  values:\n", "    default_agent_name: marker-agent\n"
	rt := newRoundTrip(t)
	slack := strings.Replace(rt.files["entrypoints/marker-slack.yaml"], after, after+line, 1)
	rt.write("entrypoints/marker-slack.yaml", slack)
	rt.apply()

	rt.write("entrypoints/marker-slack.yaml", strings.Replace(slack, line, "", 1))
	rt.assertNoChanges("the plan after removing default_agent_name")
	rt.apply()
	if got := rt.storedEntrypointValue("default_agent_name"); got != "marker-agent" {
		t.Errorf("default_agent_name after removing it from the YAML = %v, want it kept", got)
	}

	rt.write("entrypoints/marker-slack.yaml", strings.Replace(slack, line, "    default_agent_name: \"\"\n", 1))
	if got := rt.plan(); got.Summary.Update != 1 {
		t.Fatalf("plan after emptying default_agent_name = %+v, want one update", got.Changes)
	}
	rt.apply()
	rt.assertNoChanges("the plan after emptying default_agent_name")
	if got := rt.storedEntrypointValue("default_agent_name"); got != "" {
		t.Errorf("default_agent_name after emptying it = %v, want \"\"", got)
	}
}

func TestConfig_PulledEvalKindsPlanNoChanges(t *testing.T) {
	rt := newRoundTrip(t)
	rt.apply()
	pulled := t.TempDir()
	runSamForMarker(t, "pull", "config", "pull", "--url", rt.srv.URL, "--output", pulled, "--no-dotenv")
	for rel := range roundTripEval {
		raw, err := os.ReadFile(filepath.Join(pulled, rel))
		if err != nil {
			t.Fatalf("pull wrote no %s: %v", rel, err)
		}
		rt.write(rel, string(raw))
	}
	rt.assertNoChanges("the plan of the pulled eval files")
	if !strings.Contains(rt.files["experiments/marker-experiment.yaml"], "modelConfigId: "+roundTripModelConfigID) ||
		strings.Contains(rt.files["experiments/marker-experiment.yaml"], "alias:") {
		t.Errorf("pulled experiment should name its model by modelConfigId alone:\n%s", rt.files["experiments/marker-experiment.yaml"])
	}
}

func TestConfig_EvalFieldsRemovedFromYAMLAreCleared(t *testing.T) {
	rt := newRoundTrip(t)
	rt.apply()

	rt.write("datasets/marker-dataset.yaml", strings.Replace(rt.files["datasets/marker-dataset.yaml"], "description: Marker dataset\n", "", 1))
	rt.write("datasets/marker-dataset.examples.csv", strings.Replace(rt.files["datasets/marker-dataset.examples.csv"], "2 + 2?,4", "2 + 2?,", 1))
	experiment := rt.files["experiments/marker-experiment.yaml"]
	for _, line := range []string{"description: Marker experiment\n", "  primaryEvaluatorId: marker-judge\n", "  runAs: marker-evals\n",
		"  models:\n    - modelConfigId: " + roundTripModelConfigID + "\n"} {
		experiment = strings.Replace(experiment, line, "", 1)
	}
	rt.write("experiments/marker-experiment.yaml", experiment)
	if got := rt.plan(); got.Summary.Update != 2 {
		t.Fatalf("plan after removing the eval fields = %+v, want the dataset and the experiment updated", got.Changes)
	}
	rt.apply()
	rt.assertNoChanges("the plan after clearing the eval fields")

	dataset := rt.getV2("/evaluations/datasets", "marker-dataset")
	if dataset["description"] != nil {
		t.Errorf("dataset description after clearing = %v", dataset["description"])
	}
	var examples struct {
		Data []map[string]any `json:"data"`
	}
	rt.getJSON(platform.APIV2Prefix+"/evaluations/datasets/"+dataset["id"].(string)+"/examples", &examples)
	for _, ex := range examples.Data {
		if ex["expectedResponse"] != nil {
			t.Errorf("example %v expectedResponse after clearing = %v", ex["sequenceNumber"], ex["expectedResponse"])
		}
	}
	got := rt.getV2("/evaluations/experiments", "marker-experiment")
	for _, field := range []string{"description", "primaryEvaluatorId", "runAs"} {
		if got[field] != nil {
			t.Errorf("experiment %s after clearing = %v", field, got[field])
		}
	}
	if models, _ := got["models"].([]any); len(models) != 0 {
		t.Errorf("experiment models after clearing = %v", got["models"])
	}
}

const roundTripDecisionEvaluator = `kind: evaluator
name: marker-decision
description: Marker decision evaluator
spec:
  type: decision
  model: marker-decision-model
  question:
    type: choice
    instructions: Does the response answer the question?
    criteria:
      - key: fully
      - key: not_at_all
  choiceScores:
    fully: 1.0
    not_at_all: 0.0
{{escalation}}`

const roundTripDecisionEscalation = `  minConfidence: 0.8
  escalateToEvaluatorId: marker-escalation-judge
`

func TestConfig_DecisionEvaluatorEscalationIsUpdatedAndCleared(t *testing.T) {
	t.Setenv(features.EnvVarName(features.KeyEvalDecisionEvaluator), "true")
	t.Setenv(features.EnvVarName(features.KeyWorkflowDecisionNode), "true")
	rt := newRoundTrip(t)
	rt.write("models/marker-decision-model.yaml", `kind: model
name: marker-decision-model
spec:
  provider: openai
  modelName: gpt-6-luna
  modelType: decision
  apiBase: https://api.openai.com/v1
  authConfig:
    type: apikey
    api_key: sk-marker-decision
`)
	rt.write("evaluators/marker-decision.yaml", strings.ReplaceAll(roundTripDecisionEvaluator, "{{escalation}}", ""))
	manifest := strings.Replace(rt.files["manifests/marker.yaml"], "models: [marker-model,", "models: [marker-decision-model, marker-model,", 1)
	rt.write("manifests/marker.yaml", strings.Replace(manifest, "evaluators: [marker-judge]", "evaluators: [marker-judge, marker-decision]", 1))
	rt.apply()
	rt.assertNoChanges("the plan after creating the decision evaluator")

	rt.write("manifests/marker.yaml", strings.Replace(rt.files["manifests/marker.yaml"], "marker-decision]", "marker-decision, marker-escalation-judge]", 1))
	rt.write("evaluators/marker-escalation-judge.yaml", strings.Replace(roundTripEval["evaluators/marker-judge.yaml"], "name: marker-judge", "name: marker-escalation-judge", 1))
	rt.write("evaluators/marker-decision.yaml", strings.ReplaceAll(roundTripDecisionEvaluator, "{{escalation}}", roundTripDecisionEscalation))
	if got := rt.plan(); got.Summary.Create != 1 || got.Summary.Update != 1 {
		t.Fatalf("plan after adding the escalation = %+v, want the judge created and the decision evaluator updated", got.Changes)
	}
	rt.apply()
	rt.assertNoChanges("the plan after adding the escalation")
	judge := rt.getV2("/evaluations/evaluators", "marker-escalation-judge")
	decision := rt.getV2("/evaluations/evaluators", "marker-decision")
	if decision["escalateToEvaluatorId"] != judge["id"] || decision["minConfidence"] != 0.8 {
		t.Fatalf("decision evaluator escalates to %v below %v, want %v below 0.8", decision["escalateToEvaluatorId"], decision["minConfidence"], judge["id"])
	}

	rt.write("evaluators/marker-decision.yaml", strings.ReplaceAll(roundTripDecisionEvaluator, "{{escalation}}", ""))
	if got := rt.plan(); got.Summary.Update != 1 {
		t.Fatalf("plan after removing the escalation = %+v, want the decision evaluator updated", got.Changes)
	}
	rt.apply()
	rt.assertNoChanges("the plan after clearing the escalation")
	decision = rt.getV2("/evaluations/evaluators", "marker-decision")
	for _, field := range []string{"minConfidence", "escalateToEvaluatorId"} {
		if decision[field] != nil {
			t.Errorf("decision evaluator %s after clearing = %v", field, decision[field])
		}
	}
}

func TestConfig_ExampleExpectationsRemovedFromCSVAreCleared(t *testing.T) {
	rt := newRoundTrip(t)
	rt.apply()

	rt.write("datasets/marker-workflow-dataset.examples.csv", "sequence_number,prompt,expected_response,input_data\n"+
		`1,,,"{""question"":""What is 2 + 2?""}"`+"\n")
	if got := rt.plan(); got.Summary.Update != 1 {
		t.Fatalf("plan after removing the expectations = %+v, want the dataset updated", got.Changes)
	}
	rt.apply()
	rt.assertNoChanges("the plan after clearing the expectations")

	prefix := platform.APIV2Prefix
	dataset := rt.getListed(prefix, "/evaluations/datasets", "marker-workflow-dataset")
	var examples struct {
		Data []map[string]any `json:"data"`
	}
	rt.getJSON(prefix+"/evaluations/datasets/"+dataset["id"].(string)+"/examples", &examples)
	if len(examples.Data) != 1 {
		t.Fatalf("examples = %v, want one", examples.Data)
	}
	for _, field := range []string{"expectedPath", "expectedFacts"} {
		if examples.Data[0][field] != nil {
			t.Errorf("example %s after clearing = %v", field, examples.Data[0][field])
		}
	}
	if examples.Data[0]["inputData"] == nil {
		t.Error("clearing the expectations also cleared the input")
	}
}

func (rt *roundTrip) getV2(path, name string) map[string]any {
	rt.t.Helper()
	return rt.getListed(platform.APIV2Prefix, path, name)
}

func (rt *roundTrip) getListed(prefix, path, name string) map[string]any {
	rt.t.Helper()
	var list struct {
		Data []map[string]any `json:"data"`
	}
	rt.getJSON(prefix+path, &list)
	for _, item := range list.Data {
		if item["name"] == name {
			return item
		}
	}
	rt.t.Fatalf("%s is not listed at %s", name, path)
	return nil
}

func TestConfig_ModelAndConnectorSettingsRemovedFromYAMLAreCleared(t *testing.T) {
	for _, tc := range []struct{ name, file, after, line, field string }{
		{"model description", "models/marker-model.yaml", "name: marker-model\n", "description: Marker model for the round trip.\n", "description"},
		{"model maxInputTokens", "models/marker-model.yaml", "  modelName: gpt-4o\n", "  maxInputTokens: 64000\n", "maxInputTokens"},
		{"model modelParams", "models/marker-model.yaml", "  modelName: gpt-4o\n", "  modelParams:\n    temperature: 0.2\n", "modelParams"},
		{"model authConfig setting", "models/marker-switch-model.yaml", "    type: apikey\n", "    max_retries: 3\n", "authConfig.max_retries"},
		{"connector value", "connectors/marker-postgres.yaml", "    database: markerdb\n", "    port: 5433\n", "values.port"},
		{"connector description", "connectors/marker-postgres.yaml", "name: marker-postgres\n", "description: Marker connector\n", "description"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := newRoundTrip(t)
			content := rt.files[tc.file]
			if !strings.Contains(content, tc.line) {
				content = strings.Replace(content, tc.after, tc.after+tc.line, 1)
			}
			rt.write(tc.file, content)
			rt.apply()
			rt.assertNoChanges("the plan after setting " + tc.name)

			rt.write(tc.file, strings.Replace(content, tc.line, "", 1))
			if got := rt.plan(); got.Summary.Update != 1 {
				t.Fatalf("plan after removing %s = %+v, want one update", tc.name, got.Changes)
			}
			rt.apply()
			rt.assertNoChanges("the plan after removing " + tc.name)

			collection, _, _ := strings.Cut(tc.file, "/")
			name := strings.TrimSuffix(filepath.Base(tc.file), ".yaml")
			if v := lookupPath(rt.listed(platform.APIV2Prefix, collection, name), tc.field); !isEmptyJSON(v) {
				t.Errorf("stored %s after removing it = %v, want it cleared", tc.field, v)
			}
		})
	}
}

func TestConfig_ToolsetSettingsRemovedFromYAMLAreCleared(t *testing.T) {
	const file = "toolsets/" + markerToolset + ".yaml"
	for _, tc := range []struct{ name, after, line string }{
		{"the description", "name: " + markerToolset + "\n", "description: Marker toolset\n"},
		{"a setting", "  config:\n", "    region: eu-west-1\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := newRoundTrip(t)
			withLine := rt.files[file]
			if !strings.Contains(withLine, tc.line) {
				withLine = strings.Replace(withLine, tc.after, tc.after+tc.line, 1)
			}
			rt.write(file, withLine)
			rt.apply()
			rt.assertNoChanges("the plan after setting " + tc.name)

			rt.write(file, strings.Replace(withLine, tc.line, "", 1))
			if got := rt.plan(); got.Summary.Update != 1 {
				t.Fatalf("plan after removing %s = %+v, want one update", tc.name, got.Changes)
			}
			rt.apply()
			rt.assertNoChanges("the plan after removing " + tc.name)
		})
	}
}

func TestConfig_ModelAuthSwitchLeavesNoOldSettings(t *testing.T) {
	rt := newRoundTrip(t)
	apikey := rt.files["models/marker-switch-model.yaml"]
	rt.write("models/marker-switch-model.yaml", strings.Replace(apikey, `    type: apikey
    api_key: replaced-by-oauth2-in-round-2
`, `    type: oauth2
    client_id: marker-client
    token_url: https://auth.example.com/token
    scope: models.read
    client_secret: marker-oauth2-secret
`, 1))
	rt.apply()
	rt.assertNoChanges("the plan after setting up oauth2")

	rt.write("models/marker-switch-model.yaml", apikey)
	if got := rt.plan(); got.Summary.Update != 1 {
		t.Fatalf("plan after switching to apikey = %+v, want one update", got.Changes)
	}
	rt.apply()
	rt.assertNoChanges("the plan after switching to apikey")

	model := rt.listed(platform.APIV2Prefix, "models", "marker-switch-model")
	if model["authType"] != "apikey" {
		t.Errorf("authType = %v, want apikey", model["authType"])
	}
	authConfig, _ := model["authConfig"].(map[string]any)
	for key, v := range authConfig {
		if key != "type" && key != "api_key" && v != nil {
			t.Errorf("stored authConfig keeps %s=%v from oauth2: %v", key, v, authConfig)
		}
	}
	if _, kept := authConfig["client_secret"]; kept {
		t.Errorf("stored authConfig keeps the oauth2 client_secret: %v", authConfig)
	}
}

func TestConfig_ModelFieldsLeftOutOfYAMLAreKept(t *testing.T) {
	rt := newRoundTrip(t)
	rt.apply()

	before := rt.listed(platform.APIV2Prefix, "models", "marker-model")
	model := rt.files["models/marker-model.yaml"]
	model = strings.Replace(model, "  apiBase: https://api.openai.com/v1\n", "", 1)
	model = strings.Replace(model, "    type: apikey\n", "", 1)
	rt.write("models/marker-model.yaml", model)
	rt.assertNoChanges("the plan without apiBase and authConfig.type")
	rt.apply()

	after := rt.listed(platform.APIV2Prefix, "models", "marker-model")
	if after["apiBase"] != before["apiBase"] || after["authType"] != before["authType"] {
		t.Errorf("apiBase %v → %v, authType %v → %v; want both kept", before["apiBase"], after["apiBase"], before["authType"], after["authType"])
	}
}

func TestConfig_ModelProviderSwitchResolvesTheNewDefaultAPIBase(t *testing.T) {
	rt := newRoundTrip(t)
	rt.apply()

	model := rt.files["models/marker-model.yaml"]
	model = strings.Replace(model, "  apiBase: https://api.openai.com/v1\n", "", 1)
	model = strings.Replace(model, "  provider: openai\n  modelName: gpt-4o\n", "  provider: anthropic\n  modelName: claude-opus-4-7\n", 1)
	rt.write("models/marker-model.yaml", model)
	if got := rt.plan(); got.Summary.Update != 1 {
		t.Fatalf("plan after switching provider = %+v, want one update", got.Changes)
	}
	rt.apply()
	rt.assertNoChanges("the plan after switching provider")

	if got, want := rt.listed(platform.APIV2Prefix, "models", "marker-model")["apiBase"], "https://api.anthropic.com"; got != want {
		t.Errorf("apiBase = %v, want the new provider's default %q", got, want)
	}
}

func TestConfig_EditToAnUnregisteredConnectorTypeIsPlanned(t *testing.T) {
	rt := newRoundTrip(t)
	plugin := `kind: connector
name: marker-plugin
description: Marker connector of an unregistered type
spec:
  type: plugin_only
  subtype: custom
  values:
    token: marker-plugin-token
    region: eu
`
	rt.write("connectors/marker-plugin.yaml", plugin)
	rt.write("manifests/marker.yaml", strings.Replace(rt.files["manifests/marker.yaml"], "connectors: [marker-postgres]", "connectors: [marker-postgres, marker-plugin]", 1))
	rt.apply()
	rt.assertNoChanges("the plan after creating the connector")

	rt.write("connectors/marker-plugin.yaml", strings.Replace(plugin, "region: eu", "region: us", 1))
	if got := rt.plan(); got.Summary.Update != 1 {
		t.Fatalf("plan after editing the connector = %+v, want one update", got.Changes)
	}
	rt.apply()
	rt.assertNoChanges("the plan after applying the edit")
}

func TestConfig_SkillDescriptionChangeReachesV2(t *testing.T) {
	const file = "skills/marker-skill/SKILL.md"
	rt := newRoundTrip(t)
	rt.apply()

	rt.write(file, strings.Replace(rt.files[file], "description: Marker skill\n", "description: Marker skill, updated\n", 1))
	if got := rt.plan(); got.Summary.Update != 1 {
		t.Fatalf("plan after editing the description = %+v, want one update", got.Changes)
	}
	rt.apply()
	rt.assertNoChanges("the plan after the description change")

	var skill struct {
		Data struct{ Description string } `json:"data"`
	}
	rt.getJSON(platform.APIV2Prefix+"/skills/"+rt.idOf("/skills", "marker-skill"), &skill)
	if skill.Data.Description != "Marker skill, updated" {
		t.Errorf("stored description = %q, want the edited one", skill.Data.Description)
	}
}

func (rt *roundTrip) idOf(collection, name string) string {
	rt.t.Helper()
	var list struct {
		Data []struct{ ID, Name string } `json:"data"`
	}
	rt.getJSON(platform.APIV2Prefix+collection+"?origin=custom", &list)
	for _, row := range list.Data {
		if row.Name == name {
			return row.ID
		}
	}
	rt.t.Fatalf("%s is not listed at %s", name, collection)
	return ""
}

const apiV1Platform = "/api/v1/platform"

type roundTrip struct {
	t        *testing.T
	srv      *httptest.Server
	core     *platform.PlatformCore
	requests *platformRequests
	repo     string
	manifest string
	files    map[string]string
}

func newRoundTrip(t *testing.T) *roundTrip {
	t.Helper()
	t.Setenv(features.EnvVarName(features.KeyWorkflowEvals), "true")
	srv, core, requests := newRecordedSecretMarkerPlatformCore(t, store.TestDBURL(t))
	seedProfileProviderToolset(t, core.DB())
	repo := t.TempDir()
	t.Setenv("SAM_PLATFORM_TOKEN", platformToken.value(1))
	writeSecretMarkerRepo(t, repo, srv.URL, 1)
	writeMarkerToolsetBundle(t, repo)
	rt := &roundTrip{t: t, srv: srv, requests: requests, repo: repo, manifest: filepath.Join(repo, "manifests", "marker.yaml"), files: map[string]string{}}
	for _, rel := range append(slices.Collect(maps.Keys(secretMarkerRepo)), "models/marker-switch-model.yaml") {
		raw, err := os.ReadFile(filepath.Join(repo, rel))
		if err != nil {
			t.Fatal(err)
		}
		rt.files[rel] = string(raw)
	}
	rt.write("agents/marker-agent.yaml", rt.files["agents/marker-agent.yaml"]+roundTripAgentAdditions)
	rt.write("workflows/marker-workflow.yaml", roundTripWorkflow)
	for rel, content := range roundTripRBAC {
		rt.write(rel, content)
	}
	rt.core = core
	for rel, content := range roundTripEval {
		rt.write(rel, content)
	}
	for rel, content := range roundTripWorkflowRunAs {
		rt.write(rel, content)
	}
	rt.write("models/marker-mixed-case-model.yaml", roundTripMixedCaseModel)
	rt.write("manifests/marker.yaml", strings.ReplaceAll(roundTripManifest, "{{url}}", srv.URL))
	return rt
}

func seedProfileProviderToolset(t *testing.T, db *store.DB) {
	t.Helper()
	repo := store.NewToolPackageRepo(db)
	description := "Enriches the signed-in user's profile."
	pkg := &store.ToolPackage{
		Name: roundTripProfileToolset, Description: &description, DiscoveryStatus: "ready",
		Capabilities: `{"profileProvider": true}`, CreatedBy: "fixture", UpdatedBy: "fixture",
	}
	if err := repo.Create(t.Context(), pkg); err != nil {
		t.Fatalf("seed profile provider toolset: %v", err)
	}
	toolDescription := "Looks the user up."
	if err := repo.UpsertToolFile(t.Context(), &store.ToolPackageFile{
		PackageID: pkg.ID, ToolName: "enrich_authenticated_user", ToolDescription: &toolDescription,
		TimeoutSeconds: 30, SandboxProfile: "default",
	}); err != nil {
		t.Fatalf("seed profile provider tool: %v", err)
	}
}

func (rt *roundTrip) write(rel, content string) {
	rt.t.Helper()
	path := filepath.Join(rt.repo, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		rt.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		rt.t.Fatal(err)
	}
	rt.files[rel] = content
}

func (rt *roundTrip) apply() {
	rt.t.Helper()
	runSamForMarker(rt.t, "apply", "config", "apply", "--manifest", rt.manifest, "--no-deploy", "--no-interactive", "--no-dotenv")
}

type roundTripPlan struct {
	Changes []reconcile.PlanOp    `json:"changes"`
	Summary reconcile.PlanSummary `json:"summary"`
}

func (rt *roundTrip) plan() roundTripPlan {
	rt.t.Helper()
	out, _ := runSam("plan", "config", "plan", "--manifest", rt.manifest, "--format", "json", "--no-dotenv")
	start := strings.Index(out.text, "{")
	if start < 0 {
		rt.t.Fatalf("plan printed no JSON:\n%s", out.text)
	}
	var p roundTripPlan
	if err := json.NewDecoder(strings.NewReader(out.text[start:])).Decode(&p); err != nil {
		rt.t.Fatalf("decode plan: %v\n%s", err, out.text)
	}
	return p
}

var redeployCollections = map[string]string{"agent": "/agents", "workflow": "/workflows", "entrypoint": "/entrypoints"}

func (rt *roundTrip) assertNoChanges(when string) {
	rt.t.Helper()
	p := rt.plan()
	var pending []string
	for _, op := range p.Changes {
		switch op.Action {
		case reconcile.ActionUnchanged, reconcile.ActionDelete:
		case reconcile.ActionRedeploy:
			collection := redeployCollections[op.Kind]
			if status, _ := rt.getV2(collection, op.Name)["deploymentStatus"].(string); status != "not_deployed" {
				pending = append(pending, op.Kind+"/"+op.Name+" redeploy of a resource that is "+status)
			}
		default:
			pending = append(pending, op.Kind+"/"+op.Name+" "+op.Action)
		}
	}
	if len(pending) > 0 {
		rt.t.Errorf("%s wants to change %v", when, pending)
	}
}

var v2Kinds = []string{"/agents", "/workflows", "/entrypoints", "/rbac/roles", "/rbac/grants", "/rbac/claimMappings", "/rbac/serviceAccounts"}

func (rt *roundTrip) assertWritesOnV2() {
	rt.t.Helper()
	for _, kind := range v2Kinds {
		var onV2 int
		for _, req := range rt.requests.all() {
			method, path, _ := strings.Cut(req, " ")
			if method != http.MethodPost {
				continue
			}
			switch path {
			case platform.APIV2Prefix + kind:
				onV2++
			case apiV1Platform + kind:
				rt.t.Errorf("%s went to v1", req)
			}
		}
		if onV2 == 0 {
			rt.t.Errorf("no create of %s reached %s", kind, platform.APIV2Prefix+kind)
		}
	}
}

func (rt *roundTrip) getAgent(prefix string) map[string]any {
	rt.t.Helper()
	return rt.getResource(prefix, "/agents", "marker-agent")
}

func (rt *roundTrip) storedEntrypointValue(key string) any {
	rt.t.Helper()
	values, _ := rt.getResource(platform.APIV2Prefix, "/entrypoints", "marker-slack")["values"].(map[string]any)
	return values[key]
}

func (rt *roundTrip) getResource(prefix, collection, name string) map[string]any {
	rt.t.Helper()
	var list struct {
		Data []map[string]any `json:"data"`
	}
	rt.getJSON(prefix+collection, &list)
	for _, a := range list.Data {
		if a["name"] == name {
			id, _ := a["id"].(string)
			var single map[string]any
			rt.getJSON(prefix+collection+"/"+id, &single)
			if data, wrapped := single["data"].(map[string]any); wrapped && prefix == platform.APIV2Prefix {
				return data
			}
			return single
		}
	}
	rt.t.Fatalf("%s is not listed at %s", name, prefix+collection)
	return nil
}

func (rt *roundTrip) listed(prefix, collection, name string) map[string]any {
	rt.t.Helper()
	var list struct {
		Data []map[string]any `json:"data"`
	}
	rt.getJSON(prefix+"/"+collection, &list)
	for _, row := range list.Data {
		if row["name"] == name || row["alias"] == name {
			return row
		}
	}
	rt.t.Fatalf("%s %q is not listed at %s", collection, name, prefix)
	return nil
}

func lookupPath(obj map[string]any, path string) any {
	var v any = obj
	for key := range strings.SplitSeq(path, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[key]
	}
	return v
}

func isEmptyJSON(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case map[string]any:
		return len(x) == 0
	case []any:
		return len(x) == 0
	}
	return false
}

func (rt *roundTrip) getJSON(path string, into any) {
	rt.t.Helper()
	resp, err := rt.srv.Client().Get(rt.srv.URL + path)
	if err != nil {
		rt.t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		rt.t.Fatalf("read %s: %v", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		rt.t.Fatalf("GET %s: status %d: %s", path, resp.StatusCode, raw)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		rt.t.Fatalf("decode %s: %v", path, err)
	}
}

func TestConfig_RBACUpdatesConverge(t *testing.T) {
	rt := newRoundTrip(t)
	users := store.NewUsersRepo(rt.core.DB())
	alice := &store.User{Issuer: "https://idp.example.com", Sub: "alice-sub", Email: new("alice@example.com"), CreatedBy: "login", UpdatedBy: "login"}
	if err := users.UpsertUser(t.Context(), alice, nil); err != nil {
		t.Fatalf("sign in alice: %v", err)
	}
	rt.write("rbac/grants/marker-readers.yaml", rt.files["rbac/grants/marker-readers.yaml"]+"    - type: sub\n      value: alice-sub\n      issuer: https://idp.example.com\n")
	rt.apply()
	rt.assertNoChanges("the plan right after apply")
	aliceGrant := rt.grantIDFor("alice-sub")

	for rel, edit := range map[string][2]string{
		"rbac/roles/marker-operator.yaml":        {"Operates the marker agents.", "Runs the marker agents."},
		"rbac/roles/marker-reader.yaml":          {"  inherits: [marker-operator]\n", ""},
		"rbac/grants/marker-readers.yaml":        {"    - type: sub\n      value: alice-sub\n      issuer: https://idp.example.com\n", "    - alice@example.com\n"},
		"rbac/claim-mappings/marker-admins.yaml": {"roleNames: [marker-operator]", "roleNames: [marker-operator, marker-reader]"},
		"rbac/system-users/marker-bot.yaml":      {"  displayName: Marker Bot\n", ""},
		"rbac/system-users/marker-ci.yaml":       {"roleNames: [marker-reader]", "roleNames: [marker-operator]"},
	} {
		rt.write(rel, strings.Replace(rt.files[rel], edit[0], edit[1], 1))
	}
	manifest := rt.files["manifests/marker.yaml"]
	manifest = strings.Replace(manifest, "roles: [marker-reader]", "roles: [marker-reader, marker-operator]", 1)
	manifest = strings.Replace(manifest, "appName: Marker Assistant", "appName: Marker Assistant Two", 1)
	manifest = strings.Replace(manifest, "toolset: "+roundTripProfileToolset+"\n", "toolset: "+roundTripProfileToolset+"\n    claim: upn\n", 1)
	rt.write("manifests/marker.yaml", manifest)

	got := rt.plan()
	changed := map[string]int{}
	for _, op := range got.Changes {
		if op.Action == reconcile.ActionUnchanged || op.Action == reconcile.ActionDelete {
			continue
		}
		if op.Kind == "platform" {
			changed[op.Name]++
		} else {
			changed[op.Kind]++
		}
	}
	for _, kind := range []string{"rbacRole", "rbacGrant", "rbacClaimMapping", "systemUser", "defaultRoles", "webuiSettings", "profileProvider"} {
		if changed[kind] == 0 {
			t.Errorf("plan after the edits changes no %s: %v", kind, changed)
		}
	}
	rt.apply()
	rt.assertNoChanges("the plan after applying the edits")
	if id := rt.grantIDFor("alice-sub"); id != aliceGrant {
		t.Errorf("alice's grant is %s after re-recording its subject, want it kept as %s", id, aliceGrant)
	}
}

func (rt *roundTrip) grantIDFor(sub string) string {
	rt.t.Helper()
	var list struct {
		Data []struct {
			ID   string `json:"id"`
			User *struct {
				Sub string `json:"sub"`
			} `json:"user"`
		} `json:"data"`
	}
	rt.getJSON(platform.APIV2Prefix+"/rbac/grants?expand=user&pageSize=100", &list)
	for _, g := range list.Data {
		if g.User != nil && g.User.Sub == sub {
			return g.ID
		}
	}
	rt.t.Fatalf("no grant is held by %s", sub)
	return ""
}
