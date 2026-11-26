// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const (
	defaultAlias  = "general"
	changedAlias  = "planning"
	modelKeyHint  = "model_provider"
	otherAlias    = "fast"
	startingAgent = "model_agent"
)

func withConfiguredModels(t *testing.T, svc *Service, aliases ...string) map[string]string {
	t.Helper()
	repo := store.NewModelConfigRepo(svc.deps.DB)
	ids := map[string]string{}
	apiBase := "https://llm.example.com"
	for _, alias := range aliases {
		mc := &store.ModelConfiguration{
			Alias: alias, Provider: "openai", ModelName: "gpt-4o-" + alias, APIBase: &apiBase,
			ModelAuthType: "apikey", ModelAuthConfig: `{"type":"apikey","api_key":"sk-test"}`, ModelParams: "{}",
			CreatedBy: "tester", UpdatedBy: "tester",
		}
		if err := repo.Create(t.Context(), mc); err != nil {
			t.Fatalf("seed model %q: %v", alias, err)
		}
		ids[alias] = mc.ID
	}
	agentSvc := entplatform.NewAgentService(
		store.NewAgentRepo(svc.deps.DB), store.NewAgentDeploymentRepo(svc.deps.DB),
		store.NewConnectorRepo(svc.deps.DB), store.NewToolPackageRepo(svc.deps.DB), nil,
		entplatform.NewModelConfigService(repo, discardLogger()), discardLogger(),
	)
	agentSvc.WithSkillRepo(store.NewSkillRepo(svc.deps.DB))
	svc.deps.Agents = agentSvc
	return ids
}

func agentStating(t *testing.T, appLine, configLine string) BuildComponentInput {
	t.Helper()
	c := agentComponent(startingAgent)
	content := c.Content
	if appLine != "" {
		content = strings.Replace(content, "  - name: "+startingAgent+"\n", "  - name: "+startingAgent+"\n    "+appLine+"\n", 1)
	}
	if configLine != "" {
		content = strings.Replace(content, "      tools:\n", "      "+configLine+"\n      tools:\n", 1)
	}
	if appLine != "" && content == c.Content || configLine != "" && content == c.Content {
		t.Fatalf("fixture did not take %q / %q", appLine, configLine)
	}
	c.Content = content
	return c
}

type modelKeyCase struct {
	name                string
	appLine, configLine string
	wantProvider        []string
	refusedAt           string
}

func modelKeyCases() []modelKeyCase {
	return []modelKeyCase{
		{name: "alias in model_provider", configLine: "model_provider: [planning]", wantProvider: []string{changedAlias}},
		{name: "alias as a bare string", configLine: "model_provider: planning", wantProvider: []string{changedAlias}},
		{name: "model row id in model_provider", configLine: "model_provider: [%ID%]", wantProvider: []string{"%ID%"}},
		{name: "model_provider and a platform-default model", configLine: "model: ${LLM_SERVICE_GENERAL_MODEL_NAME}\n      model_provider: [planning]", wantProvider: []string{changedAlias}},
		{name: "model_provider and an empty model", configLine: "model: \"\"\n      model_provider: [planning]", wantProvider: []string{changedAlias}},
		{name: "model_provider and a null model", configLine: "model:\n      model_provider: [planning]", wantProvider: []string{changedAlias}},

		{name: "alias in app_config.model", configLine: "model: planning", refusedAt: "apps[0].app_config/model"},
		{name: "provider string in app_config.model", configLine: "model: anthropic/claude-sonnet-5", refusedAt: "apps[0].app_config/model"},
		{name: "object form in app_config.model", configLine: "model:\n        model: anthropic/claude-sonnet-5\n        api_base: https://llm.example.com", refusedAt: "apps[0].app_config/model"},
		{name: "env reference to another model in app_config.model", configLine: "model: ${LLM_SERVICE_PLANNING_MODEL_NAME}", refusedAt: "apps[0].app_config/model"},
		{name: "alias beside app_config", appLine: "model: planning", refusedAt: "apps[0].model"},
		{name: "provider string beside app_config", appLine: "model: anthropic/claude-sonnet-5", refusedAt: "apps[0].model"},
		{name: "object form beside app_config", appLine: "model: {model: anthropic/claude-sonnet-5}", refusedAt: "apps[0].model"},
		{name: "model_provider beside app_config", appLine: "model_provider: [planning]", refusedAt: "apps[0].model_provider"},
		{name: "right key plus a wrong model beside app_config", appLine: "model: fast", configLine: "model_provider: [planning]", refusedAt: "apps[0].model"},
		{name: "right key plus a wrong app_config.model", configLine: "model: fast\n      model_provider: [planning]", refusedAt: "apps[0].app_config/model"},

		{name: "empty list returns the agent to the platform default", configLine: "model_provider: []", wantProvider: []string{}},
		{name: "null model_provider returns the agent to the platform default", configLine: "model_provider:", wantProvider: []string{}},
		{name: "two aliases", configLine: "model_provider: [fast, planning]", refusedAt: "apps[0].app_config/model_provider"},
		{name: "the same alias twice", configLine: "model_provider: [planning, planning]", refusedAt: "apps[0].app_config/model_provider"},
		{name: "an empty bare string", configLine: "model_provider: \"\"", refusedAt: "apps[0].app_config/model_provider"},
		{name: "a list holding an empty string", configLine: "model_provider: [\"\"]", refusedAt: "apps[0].app_config/model_provider"},
		{name: "a number", configLine: "model_provider: 3", refusedAt: "apps[0].app_config/model_provider"},
		{name: "a mapping", configLine: "model_provider: {a: b}", refusedAt: "apps[0].app_config/model_provider"},
		{name: "a list of mappings", configLine: "model_provider:\n        - name: planning", refusedAt: "apps[0].app_config/model_provider"},
		{name: "default model with an endpoint of its own", configLine: "model:\n        model: ${LLM_SERVICE_GENERAL_MODEL_NAME}\n        api_base: https://other.example.com", refusedAt: "apps[0].app_config/model"},

		{name: "unknown alias in model_provider", configLine: "model_provider: [no_such_model]", refusedAt: "not configured"},
		{name: "unknown model row id in model_provider", configLine: "model_provider: [11111111-1111-4111-8111-111111111111]", refusedAt: "not configured"},
	}
}

func fillModelRefs(s string, ids map[string]string) string {
	return strings.NewReplacer("%ID%", ids[changedAlias], "%ALIAS%", changedAlias).Replace(s)
}

type outcome struct {
	refused  bool
	message  string
	details  map[string][]string
	statuses []string
}

func runBuild(ctx context.Context, t *testing.T, svc *Service, comp BuildComponentInput, dryRun bool) outcome {
	t.Helper()
	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{comp}, DryRun: dryRun}, testActor)
	if err != nil {
		invalid, ok := errors.AsType[*entplatform.ValidationError](err)
		if !ok {
			t.Fatalf("dryRun=%v: want a validation error, got %T: %v", dryRun, err, err)
		}
		return outcome{refused: true, message: invalid.Message, details: invalid.Details}
	}
	out := outcome{}
	for _, op := range resp.Operations {
		out.statuses = append(out.statuses, op.Status)
		if op.Status == statusFailed {
			out.refused = true
			out.message = op.Error.Message
		}
	}
	return out
}

func TestApply_AgentModelKeys(t *testing.T) {
	t.Parallel()
	for _, existing := range []bool{false, true} {
		for _, tc := range modelKeyCases() {
			name := tc.name + " on a new agent"
			if existing {
				name = tc.name + " on an existing agent"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				svc := newTestService(t)
				ids := withConfiguredModels(t, svc, defaultAlias, changedAlias, otherAlias)
				ctx := t.Context()

				if existing {
					seed := agentStating(t, "", "model_provider: ["+defaultAlias+"]")
					if got := runBuild(ctx, t, svc, seed, false); got.refused {
						t.Fatalf("seeding the agent failed: %+v", got)
					}
				}
				before := agentsByName(ctx, t, svc)

				comp := agentStating(t, fillModelRefs(tc.appLine, ids), fillModelRefs(tc.configLine, ids))
				preview := runBuild(ctx, t, svc, comp, true)
				if !slices.Equal(agentsByName(ctx, t, svc), before) {
					t.Fatalf("a dry run changed the stored agents")
				}
				applied := runBuild(ctx, t, svc, comp, false)

				if preview.refused != applied.refused {
					t.Fatalf("the preview and the apply disagree: preview refused=%v (%q), apply refused=%v (%q)",
						preview.refused, preview.message, applied.refused, applied.message)
				}

				stored := agentsByName(ctx, t, svc)
				if tc.refusedAt != "" {
					assertRefusal(t, "preview", preview, tc.refusedAt)
					assertRefusal(t, "apply", applied, tc.refusedAt)
					if !slices.Equal(stored, before) {
						t.Errorf("a refused build changed the stored agents: before %v, after %v", before, stored)
					}
					return
				}
				if applied.refused {
					t.Fatalf("the artifact was refused: %q", applied.message)
				}
				want := []string{}
				for _, p := range tc.wantProvider {
					want = append(want, fillModelRefs(p, ids))
				}
				got := agentNamed(ctx, t, svc, startingAgent).ModelProvider
				if !slices.Equal(got, want) {
					t.Errorf("stored modelProvider = %v, want %v", got, want)
				}
			})
		}
	}
}

func assertRefusal(t *testing.T, label string, got outcome, refusedAt string) {
	t.Helper()
	if !got.refused {
		t.Fatalf("%s accepted an artifact that states a model the save would drop", label)
	}
	if strings.HasPrefix(refusedAt, "apps[") {
		if !strings.Contains(got.message, refusedAt) {
			t.Errorf("%s message %q does not name %s", label, got.message, refusedAt)
		}
		if !strings.Contains(got.message, modelKeyHint) {
			t.Errorf("%s message %q does not name the key that works, %s", label, got.message, modelKeyHint)
		}
		return
	}
	if !strings.Contains(got.message, refusedAt) {
		t.Errorf("%s message %q does not say %q", label, got.message, refusedAt)
	}
}

func agentsByName(ctx context.Context, t *testing.T, svc *Service) []string {
	t.Helper()
	agents, err := svc.deps.listAgents(ctx)
	if err != nil {
		t.Fatalf("listAgents: %v", err)
	}
	out := make([]string, 0, len(agents))
	for _, a := range agents {
		out = append(out, a.Name+"|"+strings.Join(a.ModelProvider, ",")+"|"+a.SystemPrompt+"|"+a.Description)
	}
	slices.Sort(out)
	return out
}

func TestApply_ModelChangeLeavesTheRestOfTheAgentAlone(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	withConfiguredModels(t, svc, defaultAlias, changedAlias)
	ctx := t.Context()

	if got := runBuild(ctx, t, svc, agentStating(t, "", "model_provider: ["+defaultAlias+"]"), false); got.refused {
		t.Fatalf("seed: %+v", got)
	}
	before := agentNamed(ctx, t, svc, startingAgent)

	change := agentStating(t, "", "model_provider: ["+changedAlias+"]")
	if got := runBuild(ctx, t, svc, change, false); got.refused {
		t.Fatalf("model change refused: %q", got.message)
	}
	after := agentNamed(ctx, t, svc, startingAgent)

	if !slices.Equal(after.ModelProvider, []string{changedAlias}) {
		t.Fatalf("modelProvider = %v, want [%s]", after.ModelProvider, changedAlias)
	}
	if after.SystemPrompt != before.SystemPrompt || after.Description != before.Description ||
		!slices.Equal(after.InputModes, before.InputModes) || !slices.Equal(after.OutputModes, before.OutputModes) ||
		len(after.Toolsets) != len(before.Toolsets) {
		t.Errorf("changing the model changed other settings:\nbefore %+v\nafter  %+v", before, after)
	}

	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{change}}, testActor)
	if err != nil {
		t.Fatalf("reapply: %v", err)
	}
	if op := resp.Operations[0]; op.Action != actionUnchanged {
		t.Errorf("reapplying the same model planned %q (changed %v); the binding does not converge", op.Action, op.ChangedFields)
	}
}

func TestApply_OmittingTheModelKeepsTheStoredBinding(t *testing.T) {
	t.Parallel()
	for name, configLine := range map[string]string{
		"silent":                "",
		"platform default only": "model: ${LLM_SERVICE_GENERAL_MODEL_NAME}",
	} {
		t.Run(name, func(t *testing.T) {
			svc := newTestService(t)
			withConfiguredModels(t, svc, defaultAlias, changedAlias)
			ctx := t.Context()

			if got := runBuild(ctx, t, svc, agentStating(t, "", "model_provider: ["+changedAlias+"]"), false); got.refused {
				t.Fatalf("seed: %+v", got)
			}
			if got := runBuild(ctx, t, svc, agentStating(t, "", configLine), false); got.refused {
				t.Fatalf("apply: %q", got.message)
			}
			if got := agentNamed(ctx, t, svc, startingAgent).ModelProvider; !slices.Equal(got, []string{changedAlias}) {
				t.Errorf("modelProvider = %v, want the stored [%s] kept", got, changedAlias)
			}
		})
	}
}

func TestApply_ModelRefusalListsEveryOffendingKey(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	comp := agentStating(t, "model: planning\n    model_provider: [planning]", "model: fast\n      model_provider: [a, b]")

	_, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{comp}, DryRun: true}, testActor)
	invalid, ok := errors.AsType[*entplatform.ValidationError](err)
	if !ok {
		t.Fatalf("want a validation error, got %v", err)
	}
	for _, path := range []string{"apps[0].model:", "apps[0].model_provider:", "apps[0].app_config/model:", "apps[0].app_config/model_provider:"} {
		if !strings.Contains(invalid.Message, path) {
			t.Errorf("message %q does not report %s", invalid.Message, path)
		}
	}
	if got := invalid.Details["agent "+startingAgent]; len(got) != 4 {
		t.Errorf("details = %v, want four entries under the agent", invalid.Details)
	}
}

func TestPreview_ModelStoreReadErrorLeavesTheRowPlanned(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	withConfiguredModels(t, svc, defaultAlias, changedAlias)
	ctx := t.Context()
	if _, err := svc.deps.DB.ExecContext(ctx, "DROP TABLE model_configurations"); err != nil {
		t.Fatalf("drop model_configurations: %v", err)
	}

	got := runBuild(ctx, t, svc, agentStating(t, "", "model_provider: ["+changedAlias+"]"), true)
	if got.refused || !slices.Equal(got.statuses, []string{statusPlanned}) {
		t.Errorf("a store read error refused the preview or changed its status: %+v", got)
	}
}

func TestValidateModelProviders(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ids := withConfiguredModels(t, svc, defaultAlias)
	ctx := t.Context()

	for _, tc := range []struct {
		name      string
		providers []string
		want      string
	}{
		{name: "none", providers: nil},
		{name: "a configured alias", providers: []string{defaultAlias}},
		{name: "a configured row id", providers: []string{ids[defaultAlias]}},
		{name: "only the first entry is bound", providers: []string{defaultAlias, "no_such_model"}},
		{name: "an unknown alias", providers: []string{"no_such_model"}, want: "not configured"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := svc.deps.Agents.ValidateModelProviders(ctx, tc.providers)
			if (err == nil) != (tc.want == "") || err != nil && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

var malformedModelProviderFields = []malformedFieldCase{
	{name: "model_provider as a mapping", container: "appConfig", key: "model_provider", to: "model_provider: {a: b}", shape: shapeContainer},
	{name: "model_provider entries as mappings", container: "appConfig", key: "model_provider", to: "model_provider:\n        - name: planning", shape: shapeEntries},
	{name: "model_provider as an empty bare scalar", container: "appConfig", key: "model_provider", to: "model_provider: \"\"", shape: shapeEntries},
}

func TestApply_MalformedModelProviderIsRefusedAndTheStoredBindingKept(t *testing.T) {
	t.Parallel()
	for _, tc := range malformedModelProviderFields {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestService(t)
			withConfiguredModels(t, svc, defaultAlias, changedAlias)
			ctx := t.Context()
			if got := runBuild(ctx, t, svc, agentStating(t, "", "model_provider: ["+changedAlias+"]"), false); got.refused {
				t.Fatalf("seed: %+v", got)
			}

			broken := agentStating(t, "", tc.to)
			for _, dryRun := range []bool{true, false} {
				got := runBuild(ctx, t, svc, broken, dryRun)
				if !got.refused || !strings.Contains(got.message, "apps[0].app_config/model_provider") {
					t.Errorf("dryRun=%v: want a refusal naming model_provider, got %+v", dryRun, got)
				}
			}
			if got := agentNamed(ctx, t, svc, startingAgent).ModelProvider; !slices.Equal(got, []string{changedAlias}) {
				t.Errorf("modelProvider = %v, want the stored [%s] kept", got, changedAlias)
			}
		})
	}
}
