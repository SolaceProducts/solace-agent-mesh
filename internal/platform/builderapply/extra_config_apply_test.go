// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/awe"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestApplyExtraConfig_PrecedenceRows(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, line, wantErr, wantWarn, wantDescription string
		wantStored                                     map[string]any
		wantAbsent                                     []string
	}{
		{name: "1: description goes to the DTO", line: `description: "A described agent for the row."`, wantAbsent: []string{"description"},
			wantDescription: "An agent created by the plan integration test."},
		{name: "2: a named model is refused", line: "model: openai/gpt-4o", wantErr: "model is not saved"},
		{name: "4: a managed key is silent", line: "skills_base_path: /skills", wantAbsent: []string{"skillsBasePath", "skills_base_path"}},
		{name: "4: required_scopes is managed", line: "required_scopes: [agent:probe:invoke]", wantAbsent: []string{"requiredScopes", "required_scopes"}},
		{name: "4: agent_type is managed", line: "agent_type: workflow", wantAbsent: []string{"agentType", "agent_type"}},
		{name: "5: a formerly dropped knob is saved", line: "max_llm_calls_per_task: 40", wantStored: map[string]any{"maxLlmCallsPerTask": float64(40)}},
		{name: "5: an opaque schema is saved", line: "output_schema: {type: object, required: [status], properties: {status: {type: string}}}",
			wantStored: map[string]any{"outputSchema": map[string]any{"type": "object", "required": []any{"status"}, "properties": map[string]any{"status": map[string]any{"type": "string"}}}}},
		{name: "5: an unknown nested key is filtered and warned", line: "auto_summarization: {enabled: true, bogus: 1}",
			wantStored: map[string]any{"autoSummarization": map[string]any{"enabled": true}}, wantWarn: "agent config key auto_summarization.bogus was not recognised and was not saved"},
		{name: "5: a block left empty by filtering is not stated", line: "auto_summarization: {bogus: 1}",
			wantAbsent: []string{"autoSummarization"}, wantWarn: "agent config key auto_summarization.bogus was not recognised and was not saved"},
		{name: "5: a mistyped value is refused", line: "max_tokens: lots", wantErr: "maxTokens: expected integer"},
		{name: "6: an unknown key is warned", line: "totally_bogus_key: 1", wantWarn: "agent config key totally_bogus_key was not recognised and was not saved"},
		{name: "6: a camelCase spelling is not a registry key", line: "supportsStreaming: true", wantWarn: "agent config key supportsStreaming was not recognised"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newTestService(t)
			build := []BuildComponentInput{agentComponentWithKey(t, "rowed", tc.line)}
			dry, dryErr := svc.Apply(t.Context(), ApplyBuildRequest{DryRun: true, Components: build}, testActor)
			resp, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: build}, testActor)
			if tc.wantErr != "" {
				for which, e := range map[string]error{"dry run": dryErr, "apply": err} {
					if e == nil || !strings.Contains(e.Error(), tc.wantErr) {
						t.Errorf("%s error = %v, want %q", which, e, tc.wantErr)
					}
				}
				return
			}
			if dryErr != nil || err != nil {
				t.Fatalf("dry=%v apply=%v", dryErr, err)
			}
			op := opFor(t, resp, kindAgent, "rowed")
			if !slices.Equal(opFor(t, dry, kindAgent, "rowed").Warnings, op.Warnings) {
				t.Errorf("dry-run and apply warnings differ")
			}
			if (tc.wantWarn == "") != (len(op.Warnings) == 0) || tc.wantWarn != "" && !mentions(op.Warnings, tc.wantWarn) {
				t.Errorf("warnings = %q, want %q", op.Warnings, tc.wantWarn)
			}
			stored := storedAdditional(t.Context(), t, svc, "rowed")
			for k, v := range tc.wantStored {
				if !reflect.DeepEqual(stored[k], v) {
					t.Errorf("stored %s = %v, want %v", k, stored[k], v)
				}
			}
			for _, k := range tc.wantAbsent {
				if _, held := stored[k]; held {
					t.Errorf("%s was written to additionalConfigurations: %v", k, stored)
				}
			}
			if got := agentNamed(t.Context(), t, svc, "rowed").Description; tc.wantDescription != "" && got != tc.wantDescription {
				t.Errorf("stored description = %q, want %q", got, tc.wantDescription)
			}
		})
	}
}

func TestApplyExtraConfig_SetAbsentNull(t *testing.T) {
	t.Parallel()
	const seed = `{"outputSchema":{"type":"object"},"maxTokens":512,"supportsStreaming":true}`
	for _, tc := range []struct {
		name, line string
		wantAction string
		wantStored string
	}{
		{name: "a value replaces the stored one", line: "max_tokens: 1024", wantAction: actionUpdate,
			wantStored: `{"outputSchema":{"type":"object"},"maxTokens":1024,"supportsStreaming":true}`},
		{name: "an absent key leaves what is stored", wantAction: actionUnchanged, wantStored: seed},
		{name: "a null removes the stored key", line: "output_schema: null", wantAction: actionUpdate,
			wantStored: `{"maxTokens":512,"supportsStreaming":true}`},
		{name: "a null for a key not stored changes nothing", line: "input_schema: null", wantAction: actionUnchanged, wantStored: seed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newTestService(t)
			seedStoredRuntimeConfig(t, svc, "carrier", json.RawMessage(seed))
			component := agentComponent("carrier")
			if tc.line != "" {
				component = agentComponentWithKey(t, "carrier", tc.line)
			}
			build := []BuildComponentInput{component}
			dry, err := svc.Apply(t.Context(), ApplyBuildRequest{DryRun: true, Components: build}, testActor)
			if err != nil {
				t.Fatalf("dry run: %v", err)
			}
			resp, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: build}, testActor)
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			dryOp, op := opFor(t, dry, kindAgent, "carrier"), opFor(t, resp, kindAgent, "carrier")
			if dryOp.Action != tc.wantAction || op.Action != tc.wantAction {
				t.Errorf("actions dry=%s apply=%s, want %s", dryOp.Action, op.Action, tc.wantAction)
			}
			if !slices.Equal(dryOp.Warnings, op.Warnings) {
				t.Errorf("dry-run warnings %q differ from apply %q", dryOp.Warnings, op.Warnings)
			}
			var want any
			if unmarshalErr := json.Unmarshal([]byte(tc.wantStored), &want); unmarshalErr != nil {
				t.Fatal(unmarshalErr)
			}
			got := normalizeForCompare(storedAdditional(t.Context(), t, svc, "carrier"))
			if !reflect.DeepEqual(got, want) {
				t.Errorf("stored = %v, want %v", got, want)
			}
			again, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: build}, testActor)
			if err != nil {
				t.Fatalf("re-apply: %v", err)
			}
			if op := opFor(t, again, kindAgent, "carrier"); op.Action != actionUnchanged {
				t.Errorf("re-apply action = %s %v, want %s", op.Action, op.ChangedFields, actionUnchanged)
			}
		})
	}
}

func TestApplyExtraConfig_RemovalOnlySaveConverges(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, seed, line string
		wantFirst        string
	}{
		{name: "removing the only stored key", seed: `{"supportsStreaming":true}`, line: "supports_streaming: null", wantFirst: actionUpdate},
		{name: "a fresh agent stating only a null", line: "input_schema: null", wantFirst: actionCreate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newTestService(t)
			if tc.seed != "" {
				seedStoredRuntimeConfig(t, svc, "emptied", json.RawMessage(tc.seed))
			}
			build := []BuildComponentInput{agentComponentWithKey(t, "emptied", tc.line)}
			first, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: build}, testActor)
			if err != nil {
				t.Fatalf("first apply: %v", err)
			}
			if op := opFor(t, first, kindAgent, "emptied"); op.Action != tc.wantFirst {
				t.Errorf("first action = %s, want %s", op.Action, tc.wantFirst)
			}
			if stored := storedAdditional(t.Context(), t, svc, "emptied"); len(stored) != 0 {
				t.Errorf("stored additionalConfigurations = %v, want none", stored)
			}
			for _, dryRun := range []bool{true, false} {
				again, err := svc.Apply(t.Context(), ApplyBuildRequest{DryRun: dryRun, Components: build}, testActor)
				if err != nil {
					t.Fatalf("second apply (dryRun=%v): %v", dryRun, err)
				}
				if op := opFor(t, again, kindAgent, "emptied"); op.Action != actionUnchanged {
					t.Errorf("second action (dryRun=%v) = %s %v, want %s", dryRun, op.Action, op.ChangedFields, actionUnchanged)
				}
			}
		})
	}
}

func TestApply_FormerlyDroppedKeysReachTheRuntime(t *testing.T) {
	t.Parallel()
	defaults := runtimeScalarsOf(t, nil)
	canonicalOf := runtimeIndex(extraConfigKeys)
	type agentState struct {
		name  string
		seed  func(key string) string
		snake bool
	}
	states := []agentState{
		{name: "a new agent", seed: func(string) string { return "" }},
		{name: "an existing agent storing nothing", seed: func(string) string { return `{}` }},
		{name: "an existing agent storing the default in camelCase", seed: func(key string) string {
			return storedBlob(t, canonicalOf[key], defaults[key])
		}},
		{name: "an existing agent storing the default in snake_case", seed: func(key string) string {
			return storedBlob(t, key, defaults[key])
		}, snake: true},
	}
	for key, authored := range valueOtherThanTheDefault {
		for _, st := range states {
			t.Run(fmt.Sprintf("%s/%s", key, st.name), func(t *testing.T) {
				t.Parallel()
				svc := newTestService(t)
				if seed := st.seed(key); seed != "" {
					seedStoredRuntimeConfig(t, svc, "streamer", json.RawMessage(seed))
				}
				resp, err := svc.Apply(t.Context(), ApplyBuildRequest{
					Components: []BuildComponentInput{agentComponentWithKey(t, "streamer", fmt.Sprintf("%s: %v", key, authored))},
				}, testActor)
				if err != nil {
					t.Fatalf("Apply: %v", err)
				}
				if op := opFor(t, resp, kindAgent, "streamer"); mentions(op.Warnings, "agent config key "+key) {
					t.Errorf("warnings = %q, want none about %s", op.Warnings, key)
				}

				stored := agentNamed(t.Context(), t, svc, "streamer").AdditionalConfigurations
				if got := runtimeScalarsOf(t, stored)[key]; got != authored {
					t.Errorf("the deployed agent runs with %v for %s, want %v", got, key, authored)
				}

				var blob map[string]any
				if err := json.Unmarshal(stored, &blob); err != nil {
					t.Fatalf("stored %s: %v", stored, err)
				}
				_, heldCamel := blob[canonicalOf[key]]
				_, heldSnake := blob[key]
				if heldCamel && heldSnake {
					t.Errorf("both spellings of %s are stored: %v", key, blob)
				}
				if st.snake && !heldSnake {
					t.Errorf("a snake_case seed lost its spelling for %s: %v", key, blob)
				}
			})
		}
	}
}

func runtimeScalarsOf(t *testing.T, stored json.RawMessage) map[string]any {
	t.Helper()
	parsed, err := awe.ParseAgentConfig(config.FromMap(asDeployed(t, probeAppConfig(t, stored))))
	if err != nil {
		t.Fatalf("ParseAgentConfig: %v", err)
	}
	return runtimeScalars(parsed)
}

func storedBlob(t *testing.T, key string, value any) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{key: value})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func agentWithCardExtras(t *testing.T, name, extras string) BuildComponentInput {
	t.Helper()
	c := agentComponent(name)
	const cardDescription = `        description: "An agent created by the plan integration test."` + "\n"
	edited := strings.Replace(c.Content, cardDescription, cardDescription+extras, 1)
	if edited == c.Content {
		t.Fatalf("fixture has no agent_card description to follow")
	}
	c.Content = edited
	return c
}

func TestApplyExtraConfig_AgentCardExtras(t *testing.T) {
	t.Parallel()
	const extras = `        version: "1.2.0"
        documentationUrl: https://docs.example.com/a
        welcome: {message: Hi, suggestions: [{label: Go, prompt: Begin, auto_send: true}]}
        mystery: 1
`
	t.Run("the extras are saved and reach the runtime", func(t *testing.T) {
		t.Parallel()
		svc := newTestService(t)
		build := []BuildComponentInput{agentWithCardExtras(t, "carded", extras)}
		resp, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: build}, testActor)
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		op := opFor(t, resp, kindAgent, "carded")
		if want := "agent config key agent_card.mystery was not recognised and was not saved"; !slices.Equal(op.Warnings, []string{want}) {
			t.Errorf("warnings = %q, want only %q", op.Warnings, want)
		}

		agent := agentNamed(t.Context(), t, svc, "carded")
		wantCard := map[string]any{
			"version":          "1.2.0",
			"documentationUrl": "https://docs.example.com/a",
			"welcome": map[string]any{
				"message":     "Hi",
				"suggestions": []any{map[string]any{"label": "Go", "prompt": "Begin", "autoSend": true}},
			},
		}
		if got := storedAdditional(t.Context(), t, svc, "carded")["agentCard"]; !reflect.DeepEqual(got, any(wantCard)) {
			t.Errorf("stored agentCard = %v, want %v", got, wantCard)
		}

		prompt := agent.SystemPrompt
		deployed, err := entplatform.NewYAMLService("test-ns", slog.New(slog.DiscardHandler)).BuildAgentConfig(
			&store.Agent{ID: "550e8400-e29b-41d4-a716-446655440000", Name: "carded", Description: &agent.Description, SystemPrompt: &prompt, Type: "standard", AdditionalConfigurations: agent.AdditionalConfigurations},
			nil, []string{"text"}, []string{"text"}, nil)
		if err != nil {
			t.Fatalf("BuildAgentConfig: %v", err)
		}
		apps, _ := deployed["apps"].([]map[string]any)
		if len(apps) == 0 {
			t.Fatalf("deployed config has no apps: %v", deployed)
		}
		appConfig, _ := apps[0]["app_config"].(map[string]any)
		delete(appConfig, "model_provider")
		card, _ := asDeployed(t, appConfig)["agent_card"].(map[string]any)
		welcome, _ := card["welcome"].(map[string]any)
		suggestions, _ := welcome["suggestions"].([]any)
		if len(suggestions) != 1 {
			t.Fatalf("deployed welcome = %v, want one suggestion", welcome)
		}
		if first, _ := suggestions[0].(map[string]any); first["auto_send"] != true {
			t.Errorf("deployed suggestion = %v, want auto_send true", first)
		}
		if card["version"] != "1.2.0" || card["description"] != agent.Description {
			t.Errorf("deployed card = %v, want version 1.2.0 and description %q", card, agent.Description)
		}
		if _, parseErr := awe.ParseAgentConfig(config.FromMap(asDeployed(t, appConfig))); parseErr != nil {
			t.Errorf("ParseAgentConfig: %v", parseErr)
		}

		again, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: build}, testActor)
		if err != nil {
			t.Fatalf("re-apply: %v", err)
		}
		if op := opFor(t, again, kindAgent, "carded"); op.Action != actionUnchanged {
			t.Errorf("re-apply action = %s %v, want %s", op.Action, op.ChangedFields, actionUnchanged)
		}
	})

	t.Run("a stored card key the artifact does not state is kept", func(t *testing.T) {
		t.Parallel()
		svc := newTestService(t)
		seedStoredRuntimeConfig(t, svc, "carded", json.RawMessage(`{"agentCard":{"priority":3,"version":"1.0"}}`))
		build := []BuildComponentInput{agentWithCardExtras(t, "carded", "        version: \"1.2.0\"\n")}
		if _, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: build}, testActor); err != nil {
			t.Fatalf("apply: %v", err)
		}
		want := map[string]any{"priority": float64(3), "version": "1.2.0"}
		if got := storedAdditional(t.Context(), t, svc, "carded")["agentCard"]; !reflect.DeepEqual(got, any(want)) {
			t.Errorf("stored agentCard = %v, want %v", got, want)
		}
	})
}

func refusalOf(t *testing.T, which string, err error) *entplatform.ValidationError {
	t.Helper()
	ve, ok := errors.AsType[*entplatform.ValidationError](err)
	if !ok {
		t.Fatalf("%s error = %v, want a validation error", which, err)
	}
	if len(ve.Details["additionalConfigurations"]) == 0 {
		t.Errorf("%s details = %v, want problems under additionalConfigurations", which, ve.Details)
	}
	return ve
}

func TestApplyExtraConfig_StoredSpellings(t *testing.T) {
	t.Parallel()
	const (
		snakeFalse = `{"supports_streaming":false}`
		both       = `{"supportsStreaming":true,"supports_streaming":false}`
	)
	for _, tc := range []struct {
		name, seed, line string
		editsInstruction bool
		wantRefusal      []string
		wantStored       string
	}{
		{name: "a stored runtime spelling is kept", seed: snakeFalse, line: "supports_streaming: true", wantStored: `{"supports_streaming":true}`},
		{name: "both spellings stored are both set", seed: both, line: "supports_streaming: true", wantStored: `{"supportsStreaming":true,"supports_streaming":true}`},
		{name: "both spellings stored are kept when the key is left out", seed: both},
		{name: "a camelCase child inside a runtime block is refused", seed: `{"auto_summarization":{"summaryMaxTokens":1}}`,
			line:        "auto_summarization: {summary_max_tokens: 900}",
			wantRefusal: []string{"auto_summarization.summary_max_tokens is stored as summaryMaxTokens inside a block", extraConfigRemedy}},
		{name: "an unreadable blob is refused", seed: "not json", line: "supports_streaming: true",
			wantRefusal: []string{"not an object deploy can read", unreadableRemedy}},
		{name: "a key the artifact leaves out keeps the stored bytes", seed: snakeFalse},
		{name: "a blob a re-marshal would reorder keeps the stored bytes", seed: `{"zeta": 1,  "supports_streaming": false, "alpha": [1, 2]}`},
		{name: "a save that changes something else keeps the stored bytes", editsInstruction: true, seed: `{"zeta": 1,  "supports_streaming": false, "alpha": [1, 2]}`},
	} {
		for _, dryRun := range []bool{true, false} {
			mode := "apply"
			if dryRun {
				mode = "dry run"
			}
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				t.Parallel()
				svc := newTestService(t)
				ctx := t.Context()
				if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{agentComponent("spelled")}}, testActor); err != nil {
					t.Fatalf("seed: %v", err)
				}
				seedRawAdditional(ctx, t, svc, "spelled", tc.seed)
				before := string(rawStoredAgent(ctx, t, svc, "spelled").AdditionalConfigurations)

				component := agentComponent("spelled")
				if tc.line != "" {
					component = agentComponentWithKey(t, "spelled", tc.line)
				}
				if tc.editsInstruction {
					component.Content = strings.Replace(component.Content, "answers questions.", "answers questions politely.", 1)
				}
				resp, err := svc.Apply(ctx, ApplyBuildRequest{DryRun: dryRun, Components: []BuildComponentInput{component}}, testActor)
				after := string(rawStoredAgent(ctx, t, svc, "spelled").AdditionalConfigurations)

				if tc.wantRefusal != nil {
					ve := refusalOf(t, mode, err)
					for _, want := range tc.wantRefusal {
						if !strings.Contains(ve.Message, want) {
							t.Errorf("message = %q, want it to contain %q", ve.Message, want)
						}
					}
					if after != before {
						t.Errorf("a refused save wrote: %s -> %s", before, after)
					}
					return
				}
				if err != nil {
					t.Fatalf("%s: %v", mode, err)
				}
				op := opFor(t, resp, kindAgent, "spelled")
				if tc.wantStored == "" {
					wantAction := actionUnchanged
					if tc.editsInstruction {
						wantAction = actionUpdate
					}
					if op.Action != wantAction {
						t.Errorf("action = %s %v, want %s", op.Action, op.ChangedFields, wantAction)
					}
					if after != before {
						t.Errorf("stored bytes changed: %s -> %s", before, after)
					}
					return
				}
				if dryRun {
					if after != before {
						t.Errorf("a dry run wrote: %s -> %s", before, after)
					}
					return
				}
				var want any
				if err := json.Unmarshal([]byte(tc.wantStored), &want); err != nil {
					t.Fatal(err)
				}
				if got := normalizeForCompare(storedAdditional(ctx, t, svc, "spelled")); !reflect.DeepEqual(got, want) {
					t.Errorf("stored = %v, want %v", got, want)
				}
			})
		}
	}
}

func TestApplyExtraConfig_Tier2ChecksOnlyWrittenKeys(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()
	seedStoredRuntimeConfig(t, svc, "mistyped", json.RawMessage(`{"maxTokens":"lots"}`))

	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{agentComponentWithKey(t, "mistyped", "max_tokens: lots")}}, testActor); err == nil ||
		!strings.Contains(err.Error(), "maxTokens: expected integer") {
		t.Fatalf("saving the mistyped key: error = %v, want a maxTokens type refusal", err)
	}

	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{agentComponentWithKey(t, "mistyped", "supports_streaming: true")}}, testActor); err != nil {
		t.Fatalf("saving an unrelated key beside a stored mistyped one: %v", err)
	}
	stored := storedAdditional(ctx, t, svc, "mistyped")
	if stored["maxTokens"] != "lots" || stored["supportsStreaming"] != true {
		t.Errorf("stored = %v, want maxTokens left as \"lots\" and supportsStreaming true", stored)
	}
}

func TestApplyExtraConfig_NestedAndOpaqueMerge(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()
	seedStoredRuntimeConfig(t, svc, "merged", json.RawMessage(
		`{"autoSummarization":{"enabled":true,"summaryMaxTokens":800},"outputSchema":{"type":"object","properties":{"a":{},"b":{}}}}`))

	component := agentComponentWithKey(t, "merged",
		"auto_summarization: {compaction_percentage: 0.5}\n      output_schema: {type: object, properties: {a: {}}}")
	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{component}}, testActor); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stored := storedAdditional(ctx, t, svc, "merged")
	wantSummarization := map[string]any{"enabled": true, "summaryMaxTokens": float64(800), "compactionPercentage": 0.5}
	if got := stored["autoSummarization"]; !reflect.DeepEqual(got, any(wantSummarization)) {
		t.Errorf("stored autoSummarization = %v, want %v", got, wantSummarization)
	}
	wantSchema := map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{}}}
	if got := stored["outputSchema"]; !reflect.DeepEqual(got, any(wantSchema)) {
		t.Errorf("stored outputSchema = %v, want %v (property b gone)", got, wantSchema)
	}
}

func TestApplyExtraConfig_LeavesToolsetConfigsAlone(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()
	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{agentComponent("configured")}}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}
	seeded := agentNamed(ctx, t, svc, "configured")
	if _, err := svc.deps.Agents.Update(ctx, seeded.ID, entplatform.UpdateAgentRequest{
		Name:         seeded.Name,
		Description:  seeded.Description,
		SystemPrompt: seeded.SystemPrompt,
		Toolsets:     seeded.Toolsets,
		ToolsetConfigs: []entplatform.ToolsetConfigBinding{{
			ToolsetName:  seeded.Toolsets[0],
			ConfigValues: map[string]any{"list_artifacts": map[string]any{"max_results": 5}},
		}},
	}, "agent-page"); err != nil {
		t.Fatalf("seed toolsetConfigs: %v", err)
	}
	before := agentNamed(ctx, t, svc, "configured").ToolsetConfigs
	if len(before) != 1 {
		t.Fatalf("seeded toolsetConfigs = %v, want one binding", before)
	}

	component := agentComponentWithKey(t, "configured", "output_schema: {type: object}")
	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{component}}, testActor); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if after := agentNamed(ctx, t, svc, "configured").ToolsetConfigs; !reflect.DeepEqual(after, before) {
		t.Errorf("toolsetConfigs = %v, want %v untouched", after, before)
	}
	if got := storedAdditional(ctx, t, svc, "configured")["outputSchema"]; got == nil {
		t.Errorf("output_schema was not saved")
	}
}

func richAgentComponent(name string) BuildComponentInput {
	return BuildComponentInput{
		Kind: kindAgent,
		Name: name,
		Content: `
apps:
  - name: ` + name + `
    app_config:
      agent_name: ` + name + `
      instruction: You are a test agent that answers questions.
      output_schema: {type: object, properties: {status: {type: string}}}
      auto_summarization: {enabled: true, summary_max_tokens: 900}
      supports_streaming: false
      tools:
        - tool_type: builtin-group
          group_name: artifact_management
      agent_card:
        description: "An agent created by the plan integration test."
        version: "1.2.0"
        documentationUrl: https://docs.example.com/a
`,
	}
}

func TestApplyExtraConfig_RichArtifactConverges(t *testing.T) {
	t.Parallel()
	const snakeSeed = `{"output_schema":{"type":"object","properties":{"status":{"type":"string"}}},` +
		`"auto_summarization":{"enabled":true,"summary_max_tokens":900},"supports_streaming":false,` +
		`"agentCard":{"version":"1.2.0","documentationUrl":"https://docs.example.com/a"}}`
	for _, tc := range []struct{ name, seed string }{
		{name: "a new agent"},
		{name: "an existing agent storing snake_case", seed: snakeSeed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newTestService(t)
			ctx := t.Context()
			if tc.seed != "" {
				seedStoredRuntimeConfig(t, svc, "rich", json.RawMessage(tc.seed))
			}
			build := []BuildComponentInput{richAgentComponent("rich")}

			if tc.seed == "" {
				created, err := svc.Apply(ctx, ApplyBuildRequest{Components: build}, testActor)
				if err != nil {
					t.Fatalf("create: %v", err)
				}
				if op := opFor(t, created, kindAgent, "rich"); op.Action != actionCreate || len(op.Warnings) != 0 {
					t.Fatalf("create action = %s warnings = %q, want %s and none", op.Action, op.Warnings, actionCreate)
				}
			}
			stored := storedAdditional(ctx, t, svc, "rich")
			for canonical, runtime := range map[string]string{"outputSchema": "output_schema", "autoSummarization": "auto_summarization",
				"supportsStreaming": "supports_streaming"} {
				_, heldCanonical := stored[canonical]
				_, heldRuntime := stored[runtime]
				if !heldCanonical && !heldRuntime {
					t.Errorf("%s was not saved: %v", canonical, stored)
				}
				if canonical != runtime && heldCanonical && heldRuntime {
					t.Errorf("both spellings of %s are stored: %v", canonical, stored)
				}
			}
			before := string(rawStoredAgent(ctx, t, svc, "rich").AdditionalConfigurations)

			for _, dryRun := range []bool{true, false} {
				resp, err := svc.Apply(ctx, ApplyBuildRequest{DryRun: dryRun, Components: build}, testActor)
				if err != nil {
					t.Fatalf("dry run %v: %v", dryRun, err)
				}
				if op := opFor(t, resp, kindAgent, "rich"); op.Action != actionUnchanged {
					t.Errorf("dry run %v planned %s %v, want %s", dryRun, op.Action, op.ChangedFields, actionUnchanged)
				}
			}
			if after := string(rawStoredAgent(ctx, t, svc, "rich").AdditionalConfigurations); after != before {
				t.Errorf("converged applies rewrote the blob: %s -> %s", before, after)
			}
		})
	}
}

func TestApplyExtraConfig_BrokenPeerBlockLeftAloneWhenUnstated(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()
	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{agentComponent("broken")}}, testActor); err != nil {
		t.Fatalf("seed: %v", err)
	}
	const brokenBlock = `{"allowList":"not-a-list"}`
	seedRawAdditional(ctx, t, svc, "broken", `{"interAgentCommunication":`+brokenBlock+`,"maxTokens":512}`)

	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{agentComponentWithKey(t, "broken", "max_tokens: 900")}}, testActor)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if op := opFor(t, resp, kindAgent, "broken"); op.Action != actionUpdate {
		t.Errorf("action = %s, want %s", op.Action, actionUpdate)
	}
	var blob map[string]json.RawMessage
	if err := json.Unmarshal(rawStoredAgent(ctx, t, svc, "broken").AdditionalConfigurations, &blob); err != nil {
		t.Fatalf("stored blob: %v", err)
	}
	if string(blob["maxTokens"]) != "900" {
		t.Errorf("stored maxTokens = %s, want 900", blob["maxTokens"])
	}
	var got bytes.Buffer
	if err := json.Compact(&got, blob[interAgentCommunicationStoredKey]); err != nil {
		t.Fatalf("stored %s: %v", interAgentCommunicationStoredKey, err)
	}
	if got.String() != brokenBlock {
		t.Errorf("stored %s = %s, want %s unchanged", interAgentCommunicationStoredKey, got.String(), brokenBlock)
	}
}
