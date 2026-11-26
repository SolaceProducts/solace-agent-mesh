// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/awe"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const peerRenderCount = 200

func rawStoredAgent(ctx context.Context, t *testing.T, svc *Service, name string) *store.Agent {
	t.Helper()
	id := agentNamed(ctx, t, svc, name).ID
	raw, _, _, _, _, err := store.NewAgentRepo(svc.deps.DB).GetByID(ctx, id)
	if err != nil {
		t.Fatalf("read stored agent: %v", err)
	}
	return raw
}

func seedRawAdditional(ctx context.Context, t *testing.T, svc *Service, name, raw string) {
	t.Helper()
	id := agentNamed(ctx, t, svc, name).ID
	if _, err := svc.deps.DB.ExecContext(ctx, `UPDATE agents SET additional_configurations = ? WHERE id = ?`, []byte(raw), id); err != nil {
		if !json.Valid([]byte(raw)) && strings.Contains(err.Error(), "invalid input syntax for type json") {
			t.Skip("this database only stores valid JSON, so the row cannot exist here")
		}
		t.Fatalf("seed raw row: %v", err)
	}
}

func renderedPeerBlocks(t *testing.T, row *store.Agent) []string {
	t.Helper()
	prompt := "You are a probe agent whose only job is to be deployed from a stored row."
	distinct := map[string]struct{}{}
	for range peerRenderCount {
		deployed, err := entplatform.NewYAMLService("test-ns", slog.New(slog.DiscardHandler)).BuildAgentConfig(
			&store.Agent{ID: row.ID, Name: row.Name, SystemPrompt: &prompt, Type: "standard", AdditionalConfigurations: row.AdditionalConfigurations},
			nil, []string{"text"}, []string{"text"}, nil)
		if err != nil {
			t.Fatalf("BuildAgentConfig: %v", err)
		}
		apps, _ := deployed["apps"].([]map[string]any)
		if len(apps) == 0 {
			t.Fatalf("deployed config has no apps: %v", deployed)
		}
		appConfig, _ := apps[0]["app_config"].(map[string]any)
		raw, err := json.Marshal(appConfig[interAgentCommunicationKey])
		if err != nil {
			t.Fatalf("marshal deployed block: %v", err)
		}
		distinct[string(raw)] = struct{}{}
	}
	return slices.Sorted(maps.Keys(distinct))
}

func runtimeBlockFor(stored map[string]any) map[string]any {
	out := map[string]any{"allow_list": []any{}}
	for key, value := range stored {
		runtime := key
		for runtimeKey, k := range peerAccessKeys {
			if k.stored == key {
				runtime = runtimeKey
			}
		}
		out[runtime] = value
	}
	return out
}

func jsonStringsEqual(t *testing.T, a, b string) bool {
	t.Helper()
	return jsonEqual(t, json.RawMessage(a), json.RawMessage(b))
}

func hasRuntimeSpelling(raw string) bool {
	return strings.Contains(raw, `"`+interAgentCommunicationKey+`"`) || strings.Contains(raw, `"allow_list"`) ||
		strings.Contains(raw, `"deny_list"`) || strings.Contains(raw, `_timeout_seconds"`)
}

type peerOutcome struct {
	problems []string
	camel    map[string]any
	snake    map[string]any
	warn     []string
}

func inCamel(block map[string]any, warn ...string) peerOutcome {
	return peerOutcome{camel: block, warn: warn}
}
func inSnake(block map[string]any, warn ...string) peerOutcome {
	return peerOutcome{snake: block, warn: warn}
}
func refuses(problems ...string) peerOutcome { return peerOutcome{problems: problems} }

func refusalMessage(problems []string) string {
	remedy := "to go on, rewrite this agent's additionalConfigurations with the agents API (or `sam config pull`, edit, then `sam config apply`; " +
		"both replace the whole blob) so that it holds one interAgentCommunication block with well-formed values and no inter_agent_communication block, " +
		"or leave inter_agent_communication out of this agent's artifact to keep what is stored"
	if strings.Contains(problems[0], "is not an object deploy can read") {
		remedy = "to go on, replace this agent's additionalConfigurations with a JSON object using the agents API or `sam config apply`; " +
			"until then no other change to the agent can be saved either"
	}
	return `agent "` + orchestratorName + `": the stored peer-access settings cannot be updated safely: ` + strings.Join(problems, "; ") + "; " + remedy
}

func TestPeerAccessMatrix(t *testing.T) {
	t.Parallel()
	list := listOf
	const removedTech = "this agent can no longer delegate to Tech: it is not in the allow_list this build states"
	const starRemoved = "the allow_list entry * was removed, so peers only it matched are no longer allowed"
	const bothBlocks = "both interAgentCommunication and inter_agent_communication are stored, and deploy chooses between them by chance"
	const unreadable = "the stored additionalConfigurations is not an object deploy can read, so the agent runs with no additional settings"
	notObject := func(key string, what string) string {
		return "the stored " + key + " is " + what + ", which the runtime reads as allowing every peer"
	}
	otherSpelling := func(block, key, spelledBy string) string {
		return block + "." + key + " is spelled the way " + spelledBy + " spells it"
	}
	const camel, snake = "interAgentCommunication", "inter_agent_communication"
	const names, seconds = "list of peer names", "non-negative whole number of seconds"
	malformed := func(block, key, shape, stored string) string {
		return block + "." + key + " is not a well-formed " + shape + " (stored: " + stored + ")"
	}

	type seedCase struct {
		name    string
		seed    string
		L, D, T peerOutcome
		keeps   string
	}
	same := func(name, seed string, p peerOutcome) seedCase {
		return seedCase{name: name, seed: seed, L: p, D: p, T: p}
	}
	seeds := []seedCase{
		{name: "nothing stored", seed: ``,
			L: inCamel(map[string]any{"allowList": list("Billing")}),
			D: inCamel(map[string]any{"denyList": list("Tech")}),
			T: inCamel(map[string]any{"requestTimeoutSeconds": float64(60)})},
		{name: "unrelated keys only", seed: `{"keep":1}`, keeps: "keep",
			L: inCamel(map[string]any{"allowList": list("Billing")}),
			D: inCamel(map[string]any{"denyList": list("Tech")}),
			T: inCamel(map[string]any{"requestTimeoutSeconds": float64(60)})},
		{name: "platform block, typed", seed: `{"interAgentCommunication":{"allowList":["Tech"],"denyList":["Billing"]}}`,
			L: inCamel(map[string]any{"allowList": list("Billing"), "denyList": list("Billing")}, removedTech),
			D: inCamel(map[string]any{"allowList": list("Tech"), "denyList": list("Tech")}),
			T: inCamel(map[string]any{"allowList": list("Tech"), "denyList": list("Billing"), "requestTimeoutSeconds": float64(60)})},
		{name: "platform block, with an unknown key", seed: `{"interAgentCommunication":{"allowList":["Tech"],"future_key":true}}`,
			L: inCamel(map[string]any{"allowList": list("Billing"), "future_key": true}, removedTech),
			D: inCamel(map[string]any{"allowList": list("Tech"), "future_key": true, "denyList": list("Tech")}),
			T: inCamel(map[string]any{"allowList": list("Tech"), "future_key": true, "requestTimeoutSeconds": float64(60)})},
		{name: "platform block, empty", seed: `{"interAgentCommunication":{}}`,
			L: inCamel(map[string]any{"allowList": list("Billing")}),
			D: inCamel(map[string]any{"denyList": list("Tech")}),
			T: inCamel(map[string]any{"requestTimeoutSeconds": float64(60)})},
		{name: "platform block, wildcard", seed: `{"interAgentCommunication":{"allowList":["*"]}}`,
			L: inCamel(map[string]any{"allowList": list("Billing")}, starRemoved),
			D: inCamel(map[string]any{"allowList": list("*"), "denyList": list("Tech")}),
			T: inCamel(map[string]any{"allowList": list("*"), "requestTimeoutSeconds": float64(60)})},
		{name: "platform block, stored empty allow list", seed: `{"interAgentCommunication":{"allowList":[]}}`,
			L: inCamel(map[string]any{"allowList": list("Billing")}),
			D: inCamel(map[string]any{"allowList": []any{}, "denyList": list("Tech")}),
			T: inCamel(map[string]any{"allowList": []any{}, "requestTimeoutSeconds": float64(60)})},
		{name: "platform block, stored timeouts", seed: `{"interAgentCommunication":{"requestTimeoutSeconds":30,"subTaskTimeoutSeconds":40}}`,
			L: inCamel(map[string]any{"allowList": list("Billing"), "requestTimeoutSeconds": float64(30), "subTaskTimeoutSeconds": float64(40)}),
			D: inCamel(map[string]any{"denyList": list("Tech"), "requestTimeoutSeconds": float64(30), "subTaskTimeoutSeconds": float64(40)}),
			T: inCamel(map[string]any{"requestTimeoutSeconds": float64(60), "subTaskTimeoutSeconds": float64(40)})},
		{name: "platform block, a stored zero timeout", seed: `{"interAgentCommunication":{"requestTimeoutSeconds":0}}`,
			L: inCamel(map[string]any{"allowList": list("Billing"), "requestTimeoutSeconds": float64(0)}),
			D: inCamel(map[string]any{"denyList": list("Tech"), "requestTimeoutSeconds": float64(0)}),
			T: inCamel(map[string]any{"requestTimeoutSeconds": float64(60)})},
		{name: "a whitespace-only blob", seed: "  \n",
			L: inCamel(map[string]any{"allowList": list("Billing")}),
			D: inCamel(map[string]any{"denyList": list("Tech")}),
			T: inCamel(map[string]any{"requestTimeoutSeconds": float64(60)})},
		{name: "runtime block, typed", seed: `{"inter_agent_communication":{"allow_list":["Tech"],"deny_list":["Billing"]}}`,
			L: inSnake(map[string]any{"allow_list": list("Billing"), "deny_list": list("Billing")}, removedTech),
			D: inSnake(map[string]any{"allow_list": list("Tech"), "deny_list": list("Tech")}),
			T: inSnake(map[string]any{"allow_list": list("Tech"), "deny_list": list("Billing"), "request_timeout_seconds": float64(60)})},
		{name: "runtime block, with an unknown key", seed: `{"inter_agent_communication":{"allow_list":["Tech"],"other_key":1}}`,
			L: inSnake(map[string]any{"allow_list": list("Billing"), "other_key": float64(1)}, removedTech),
			D: inSnake(map[string]any{"allow_list": list("Tech"), "other_key": float64(1), "deny_list": list("Tech")}),
			T: inSnake(map[string]any{"allow_list": list("Tech"), "other_key": float64(1), "request_timeout_seconds": float64(60)})},
		{name: "runtime block, empty", seed: `{"inter_agent_communication":{}}`,
			L: inSnake(map[string]any{"allow_list": list("Billing")}),
			D: inSnake(map[string]any{"deny_list": list("Tech")}),
			T: inSnake(map[string]any{"request_timeout_seconds": float64(60)})},
		{name: "runtime block, wildcard", seed: `{"inter_agent_communication":{"allow_list":["*"]}}`,
			L: inSnake(map[string]any{"allow_list": list("Billing")}, starRemoved),
			D: inSnake(map[string]any{"allow_list": list("*"), "deny_list": list("Tech")}),
			T: inSnake(map[string]any{"allow_list": list("*"), "request_timeout_seconds": float64(60)})},
		{name: "runtime block, stored timeout", seed: `{"inter_agent_communication":{"request_timeout_seconds":10000000000}}`,
			L: inSnake(map[string]any{"allow_list": list("Billing"), "request_timeout_seconds": float64(10000000000)}),
			D: inSnake(map[string]any{"deny_list": list("Tech"), "request_timeout_seconds": float64(10000000000)}),
			T: inSnake(map[string]any{"request_timeout_seconds": float64(60)})},

		same("both blocks", `{"interAgentCommunication":{"allowList":["Tech"]},"inter_agent_communication":{"allow_list":["Tech"]}}`, refuses(bothBlocks)),
		same("both blocks, one empty", `{"interAgentCommunication":{"allowList":["Tech"]},"inter_agent_communication":{}}`, refuses(bothBlocks)),
		same("both blocks, both null", `{"interAgentCommunication":null,"inter_agent_communication":null}`, refuses(bothBlocks)),
		same("platform block holding a runtime-spelled key", `{"interAgentCommunication":{"allow_list":["Tech"]}}`,
			refuses(otherSpelling(camel, "allow_list", snake))),
		same("platform block holding both spellings of one key", `{"interAgentCommunication":{"allowList":["Tech"],"allow_list":["Tech"]}}`,
			refuses(otherSpelling(camel, "allow_list", snake))),
		same("runtime block holding a platform-spelled key", `{"inter_agent_communication":{"allowList":["Tech"],"denyList":["Billing"]}}`,
			refuses(otherSpelling(snake, "allowList", camel), otherSpelling(snake, "denyList", camel))),
		same("runtime block holding a platform-spelled wildcard", `{"inter_agent_communication":{"allowList":["*"]}}`,
			refuses(otherSpelling(snake, "allowList", camel))),
		same("platform block, a bare string allow list", `{"interAgentCommunication":{"allowList":"Tech"}}`,
			refuses(malformed(camel, "allowList", names, `text`))),
		same("platform block, a null timeout", `{"interAgentCommunication":{"requestTimeoutSeconds":null}}`,
			refuses(malformed(camel, "requestTimeoutSeconds", seconds, `null`))),
		same("platform block, a boolean allow list", `{"interAgentCommunication":{"allowList":true}}`,
			refuses(malformed(camel, "allowList", names, `a boolean`))),
		same("platform block, an object allow list", `{"interAgentCommunication":{"allowList":{"a":1}}}`,
			refuses(malformed(camel, "allowList", names, `an object`))),
		same("platform block, a list timeout", `{"interAgentCommunication":{"requestTimeoutSeconds":["a"]}}`,
			refuses(malformed(camel, "requestTimeoutSeconds", seconds, `a list`))),
		same("platform block, a huge timeout is cut in the message", `{"interAgentCommunication":{"requestTimeoutSeconds":-`+strings.Repeat("9", 100)+`}}`,
			refuses(malformed(camel, "requestTimeoutSeconds", seconds, `-`+strings.Repeat("9", 39)+`...`))),
		same("platform block, a null deny list", `{"interAgentCommunication":{"denyList":null}}`,
			refuses(malformed(camel, "denyList", names, `null`))),
		same("platform block, a null allow list", `{"interAgentCommunication":{"allowList":null}}`,
			refuses(malformed(camel, "allowList", names, `null`))),
		same("platform block, a negative timeout at the boundary", `{"interAgentCommunication":{"requestTimeoutSeconds":-1}}`,
			refuses(malformed(camel, "requestTimeoutSeconds", seconds, `-1`))),
		same("platform block, a string timeout", `{"interAgentCommunication":{"requestTimeoutSeconds":"30"}}`,
			refuses(malformed(camel, "requestTimeoutSeconds", seconds, `text`))),
		same("runtime block, a list holding null", `{"inter_agent_communication":{"deny_list":["Tech",null]}}`,
			refuses(malformed(snake, "deny_list", names, `a list holding an entry that is not text`))),
		same("runtime block, a negative timeout", `{"inter_agent_communication":{"request_timeout_seconds":-5}}`,
			refuses(malformed(snake, "request_timeout_seconds", seconds, `-5`))),
		same("runtime block, a fractional timeout", `{"inter_agent_communication":{"request_timeout_seconds":1.5}}`,
			refuses(malformed(snake, "request_timeout_seconds", seconds, `1.5`))),
		same("platform block null", `{"interAgentCommunication":null}`, refuses(notObject(camel, "null"))),
		same("runtime block null", `{"inter_agent_communication":null}`, refuses(notObject(snake, "null"))),
		same("platform block a string", `{"interAgentCommunication":"x"}`, refuses(notObject(camel, "not an object"))),
		same("runtime block a string", `{"inter_agent_communication":"x","keep":1}`, refuses(notObject(snake, "not an object"))),
		same("runtime block a bool", `{"inter_agent_communication":true}`, refuses(notObject(snake, "not an object"))),
		same("platform block a number", `{"interAgentCommunication":5}`, refuses(notObject(camel, "not an object"))),
		same("runtime block a list", `{"inter_agent_communication":["Tech"]}`, refuses(notObject(snake, "not an object"))),
		same("trailing data after the blob", `{"interAgentCommunication":{"allowList":["*"]}}garbage`, refuses(unreadable)),
		same("a number a float cannot hold", `{"n":1e400,"interAgentCommunication":{"allowList":["*"]}}`, refuses(unreadable)),
		same("a blob that is an array", `[1]`, refuses(unreadable)),
		same("a blob that is null", `null`, refuses(unreadable)),
	}

	type speech struct {
		name  string
		block string
		pick  func(seedCase) *peerOutcome
	}
	statements := []speech{
		{"states an allow_list", iac(`allow_list: ["Billing"]`), func(s seedCase) *peerOutcome { return &s.L }},
		{"states only a deny_list", iac(`deny_list: ["Tech"]`), func(s seedCase) *peerOutcome { return &s.D }},
		{"states only a timeout", iac(`request_timeout_seconds: 60`), func(s seedCase) *peerOutcome { return &s.T }},
		{"is silent", "", nil},
		{"states its keys in camelCase", iac(`allowList: ["Billing"]`), nil},
		{"states the block in camelCase", "      interAgentCommunication:\n        allowList: [\"Billing\"]", nil},
	}
	unrecognised := map[string]string{
		"states its keys in camelCase":  "inter_agent_communication.allowList is not a recognised key and was not saved",
		"states the block in camelCase": "agent config key interAgentCommunication was not recognised and was not saved",
	}

	const techRow = `{"inter_agent_communication":{"allowList":["*"],"allow_list":["Billing"]}}`
	const billingRow = `{"interAgentCommunication":{"allow_list":["Tech"]},"inter_agent_communication":{"deny_list":["x"]}}`

	for _, sd := range seeds {
		for _, st := range statements {
			for _, dryRun := range []bool{true, false} {
				mode := "apply"
				if dryRun {
					mode = "dry run"
				}
				t.Run(sd.name+"/"+st.name+"/"+mode, func(t *testing.T) {
					t.Parallel()
					svc := newTestService(t)
					ctx := t.Context()
					if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{
						agentComponent("Billing"), agentComponent("Tech"), agentComponent(orchestratorName),
					}}, testActor); err != nil {
						t.Fatalf("seed: %v", err)
					}
					seedRawAdditional(ctx, t, svc, "Tech", techRow)
					seedRawAdditional(ctx, t, svc, "Billing", billingRow)
					if sd.seed != "" {
						seedRawAdditional(ctx, t, svc, orchestratorName, sd.seed)
					}
					rows := func() map[string]string {
						out := map[string]string{}
						for _, name := range []string{orchestratorName, "Tech", "Billing"} {
							out[name] = string(rawStoredAgent(ctx, t, svc, name).AdditionalConfigurations)
						}
						return out
					}
					before := rows()
					beforeAccess := effectiveAccess(t, rawStoredAgent(ctx, t, svc, orchestratorName))

					var outcome *peerOutcome
					if st.pick != nil {
						outcome = st.pick(sd)
					}
					build := []BuildComponentInput{orchestratorWith(t, st.block), agentComponent("Billing"), agentComponent("Tech")}
					resp, err := svc.Apply(ctx, ApplyBuildRequest{DryRun: dryRun, Components: build}, testActor)

					if outcome != nil && outcome.problems != nil {
						ve, ok := errors.AsType[*entplatform.ValidationError](err)
						if !ok {
							t.Fatalf("error = %v, want a validation error", err)
						}
						if want := refusalMessage(outcome.problems); ve.Message != want {
							t.Errorf("message = %q, want %q", ve.Message, want)
						}
						if !slices.Equal(ve.Details[interAgentCommunicationKey], outcome.problems) {
							t.Errorf("details = %v, want exactly the problems %q", ve.Details, outcome.problems)
						}
						if !reflect.DeepEqual(rows(), before) {
							t.Errorf("a refused build wrote something: %v -> %v", before, rows())
						}
						if !dryRun {
							followTheRemedy(ctx, t, svc, build)
						}
						return
					}
					if err != nil {
						t.Fatalf("apply: %v", err)
					}

					wantStatus := statusSucceeded
					if dryRun {
						wantStatus = statusPlanned
					}
					for _, written := range resp.Operations {
						if written.Status != wantStatus {
							t.Errorf("%s %q has status %s, want %s (error %+v)", written.Kind, written.Name, written.Status, wantStatus, written.Error)
						}
					}
					op := opFor(t, resp, kindAgent, orchestratorName)
					wantAction, wantWarn := actionUnchanged, []string(nil)
					if outcome != nil {
						wantAction, wantWarn = actionUpdate, outcome.warn
					} else if text, ok := unrecognised[st.name]; ok {
						wantWarn = []string{text}
					}
					if op.Action != wantAction {
						t.Errorf("action = %s, want %s (changed %v)", op.Action, wantAction, op.ChangedFields)
					}
					if !slices.Equal(op.Warnings, wantWarn) {
						t.Errorf("warnings = %q, want exactly %q", op.Warnings, wantWarn)
					}
					for _, name := range []string{"Billing", "Tech"} {
						if other := opFor(t, resp, kindAgent, name); other.Action != actionUnchanged {
							t.Errorf("%s planned %s, want unchanged", name, other.Action)
						}
					}

					after := rows()
					if dryRun || outcome == nil {
						if !reflect.DeepEqual(after, before) {
							t.Fatalf("a dry run or silent artifact wrote something: %v -> %v", before, after)
						}
					}
					for _, name := range []string{"Tech", "Billing"} {
						if after[name] != before[name] {
							t.Errorf("an agent the build did not change peers on was rewritten (%s): %s -> %s", name, before[name], after[name])
						}
					}
					if dryRun {
						return
					}

					if outcome != nil {
						var blob map[string]any
						if unmarshalErr := json.Unmarshal([]byte(after[orchestratorName]), &blob); unmarshalErr != nil {
							t.Fatalf("stored row is not JSON: %v", unmarshalErr)
						}
						if got, _ := blob[camel].(map[string]any); !reflect.DeepEqual(got, outcome.camel) {
							t.Errorf("stored %s = %v, want %v", camel, got, outcome.camel)
						}
						if got, _ := blob[snake].(map[string]any); !reflect.DeepEqual(got, outcome.snake) {
							t.Errorf("stored %s = %v, want %v", snake, got, outcome.snake)
						}
						if outcome.camel != nil && blob[snake] != nil || outcome.snake != nil && blob[camel] != nil {
							t.Errorf("the write left a block in each spelling: %s", after[orchestratorName])
						}
						if sd.keeps != "" && blob[sd.keeps] == nil {
							t.Errorf("an unrelated stored key was lost: %s", after[orchestratorName])
						}

						rendered := renderedPeerBlocks(t, rawStoredAgent(ctx, t, svc, orchestratorName))
						if len(rendered) != 1 {
							t.Errorf("deployed %d distinct blocks over %d renders: %v", len(rendered), peerRenderCount, rendered)
						}
						checkEffectiveAccess(t, st.name, beforeAccess, effectiveAccess(t, rawStoredAgent(ctx, t, svc, orchestratorName)))
					}

					again, err := svc.Apply(ctx, ApplyBuildRequest{Components: build}, testActor)
					if err != nil {
						t.Fatalf("second apply: %v", err)
					}
					if op := opFor(t, again, kindAgent, orchestratorName); op.Action != actionUnchanged {
						t.Errorf("a repeated apply planned %s (changed %v), want unchanged", op.Action, op.ChangedFields)
					}

					if outcome != nil {
						readBack := agentNamed(ctx, t, svc, orchestratorName)
						if _, err := entplatform.ValidateAgentConfigurations(readBack.AdditionalConfigurations); err != nil {
							t.Errorf("the next UI save of this agent would be refused: %v", err)
						}
						if _, err := svc.deps.Agents.Update(ctx, readBack.ID, entplatform.UpdateAgentRequest{
							Name: readBack.Name, Description: readBack.Description + " edited", SystemPrompt: readBack.SystemPrompt,
							Toolsets: readBack.Toolsets, AdditionalConfigurations: readBack.AdditionalConfigurations,
						}, "ui"); err != nil {
							t.Errorf("saving the agent back unchanged failed: %v", err)
						}
					}
				})
			}
		}
	}
}

func followTheRemedy(ctx context.Context, t *testing.T, svc *Service, build []BuildComponentInput) {
	t.Helper()
	agent := agentNamed(ctx, t, svc, orchestratorName)
	if _, err := svc.deps.Agents.Update(ctx, agent.ID, entplatform.UpdateAgentRequest{
		Name: agent.Name, Description: agent.Description, SystemPrompt: agent.SystemPrompt, Toolsets: agent.Toolsets,
		AdditionalConfigurations: json.RawMessage(`{"interAgentCommunication":{"allowList":["Billing"]}}`),
	}, "ops"); err != nil {
		t.Fatalf("the remedy could not be applied: %v", err)
	}
	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: build}, testActor)
	if err != nil {
		t.Fatalf("the build is still refused after the remedy: %v", err)
	}
	for _, op := range resp.Operations {
		if op.Status != statusSucceeded {
			t.Errorf("%s %q has status %s after the remedy (error %+v)", op.Kind, op.Name, op.Status, op.Error)
		}
	}
}

type access struct {
	Allow, Deny                []string
	Request, Workflow, SubTask int
}

func effectiveAccess(t *testing.T, row *store.Agent) []access {
	t.Helper()
	prompt := "You are a probe agent whose only job is to be deployed from a stored row."
	distinct := map[string]access{}
	for range peerRenderCount {
		deployed, err := entplatform.NewYAMLService("test-ns", slog.New(slog.DiscardHandler)).BuildAgentConfig(
			&store.Agent{ID: row.ID, Name: row.Name, SystemPrompt: &prompt, Type: "standard", AdditionalConfigurations: row.AdditionalConfigurations},
			nil, []string{"text"}, []string{"text"}, nil)
		if err != nil {
			t.Fatalf("BuildAgentConfig: %v", err)
		}
		apps, _ := deployed["apps"].([]map[string]any)
		appConfig, _ := apps[0]["app_config"].(map[string]any)
		delete(appConfig, "model_provider")
		wire, err := json.Marshal(appConfig)
		if err != nil {
			t.Fatalf("serialise the deployed config: %v", err)
		}
		var onTheWire map[string]any
		if unmarshalErr := json.Unmarshal(wire, &onTheWire); unmarshalErr != nil {
			t.Fatalf("read the deployed config back: %v", unmarshalErr)
		}
		parsed, err := awe.ParseAgentConfig(config.FromMap(onTheWire))
		if err != nil {
			t.Fatalf("ParseAgentConfig: %v", err)
		}
		a := access{Allow: parsed.PeerAllowList, Deny: parsed.PeerDenyList,
			Request: parsed.PeerRequestTimeoutSeconds, Workflow: parsed.WorkflowRequestTimeoutSeconds, SubTask: parsed.SubTaskTimeoutSeconds}
		if a.Allow == nil {
			a.Allow = []string{"*"}
		}
		distinct[fmt.Sprintf("%v", a)] = a
	}
	out := make([]access, 0, len(distinct))
	for _, key := range slices.Sorted(maps.Keys(distinct)) {
		out = append(out, distinct[key])
	}
	return out
}

func checkEffectiveAccess(t *testing.T, statement string, before, after []access) {
	t.Helper()
	if len(after) != 1 {
		t.Errorf("deployed %d distinct accesses over %d renders: %v", len(after), peerRenderCount, after)
		return
	}
	now := after[0]
	switch statement {
	case "states an allow_list":
		if !slices.Equal(now.Allow, []string{"Billing"}) {
			t.Errorf("effective allow = %v, want exactly what the artifact stated", now.Allow)
		}
	case "states only a deny_list":
		if !slices.Equal(now.Deny, []string{"Tech"}) {
			t.Errorf("effective deny = %v, want exactly what the artifact stated", now.Deny)
		}
	case "states only a timeout":
		if now.Request != 60 {
			t.Errorf("effective request timeout = %d, want exactly what the artifact stated", now.Request)
		}
	}
	if len(before) != 1 {
		t.Errorf("the row deployed %d ways before the write, and the write was not refused: %v", len(before), before)
		return
	}
	was := before[0]
	if statement != "states an allow_list" && !slices.Equal(was.Allow, now.Allow) {
		t.Errorf("a write that did not state an allow_list changed who the agent may call: %v -> %v", was.Allow, now.Allow)
	}
	if statement != "states only a deny_list" && !slices.Equal(was.Deny, now.Deny) {
		t.Errorf("a write that did not state a deny_list changed who the agent may not call: %v -> %v", was.Deny, now.Deny)
	}
	if statement != "states only a timeout" && was.Request != 0 && was.Request != now.Request {
		t.Errorf("a write that did not state a timeout changed the request timeout: %d -> %d", was.Request, now.Request)
	}
	if statement != "states only a timeout" && was.Request != 0 && (was.Workflow != now.Workflow || was.SubTask != now.SubTask) {
		t.Errorf("a write that did not state a timeout changed the workflow or sub-task timeout: %d/%d -> %d/%d", was.Workflow, was.SubTask, now.Workflow, now.SubTask)
	}
}

func TestPeerAccessSpellingMatrix_Create(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		block     string
		wantStore map[string]any
		wantWarn  []string
	}{
		{"states an allow_list", iac(`allow_list: ["Billing"]`), map[string]any{"allowList": listOf("Billing")}, nil},
		{"states the keys in the runtime spelling", iac(`allow_list: ["*"]`, `deny_list: ["Tech"]`, `request_timeout_seconds: 90`),
			map[string]any{"allowList": listOf("*"), "denyList": listOf("Tech"), "requestTimeoutSeconds": float64(90)}, nil},
		{"is silent", "", nil, nil},
		{"states its keys in camelCase", iac(`allowList: ["Billing"]`), nil,
			[]string{"inter_agent_communication.allowList is not a recognised key and was not saved"}},
		{"states the block in camelCase", "      interAgentCommunication:\n        allowList: [\"Billing\"]", nil,
			[]string{"agent config key interAgentCommunication was not recognised and was not saved"}},
	} {
		for _, dryRun := range []bool{true, false} {
			mode := "apply"
			if dryRun {
				mode = "dry run"
			}
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				svc := newTestService(t)
				ctx := t.Context()
				if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{agentComponent("Billing"), agentComponent("Tech")}}, testActor); err != nil {
					t.Fatalf("seed: %v", err)
				}
				build := []BuildComponentInput{orchestratorWith(t, tc.block)}
				resp, err := svc.Apply(ctx, ApplyBuildRequest{DryRun: dryRun, Components: build}, testActor)
				if err != nil {
					t.Fatalf("apply: %v", err)
				}
				op := opFor(t, resp, kindAgent, orchestratorName)
				if op.Action != actionCreate {
					t.Errorf("action = %s, want create", op.Action)
				}
				if !slices.Equal(op.Warnings, tc.wantWarn) {
					t.Errorf("warnings = %q, want exactly %q", op.Warnings, tc.wantWarn)
				}
				if dryRun {
					agents, _ := svc.deps.listAgents(ctx)
					if slices.ContainsFunc(agents, func(a entplatform.AgentResponse) bool { return a.Name == orchestratorName }) {
						t.Fatal("a dry run created the agent")
					}
					return
				}
				if read := storedPeerAccess(ctx, t, svc, orchestratorName); !reflect.DeepEqual(read, tc.wantStore) {
					t.Errorf("read back = %v, want %v", read, tc.wantStore)
				}
				raw := rawStoredAgent(ctx, t, svc, orchestratorName)
				if hasRuntimeSpelling(string(raw.AdditionalConfigurations)) {
					t.Errorf("a runtime spelling was stored: %s", raw.AdditionalConfigurations)
				}
				rendered := renderedPeerBlocks(t, raw)
				wantRuntime, _ := json.Marshal(runtimeBlockFor(tc.wantStore))
				if len(rendered) != 1 || !jsonStringsEqual(t, rendered[0], string(wantRuntime)) {
					t.Errorf("deployed %v, want exactly %s", rendered, wantRuntime)
				}
				again, err := svc.Apply(ctx, ApplyBuildRequest{Components: build}, testActor)
				if err != nil {
					t.Fatalf("second apply: %v", err)
				}
				if op := opFor(t, again, kindAgent, orchestratorName); op.Action != actionUnchanged {
					t.Errorf("a repeated apply planned %s, want unchanged", op.Action)
				}
			})
		}
	}
}

func TestValidPattern(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		pattern string
		want    bool
	}{
		{"*", true},
		{"Billing", true},
		{"Bill*", true},
		{"b?lling", true},
		{"[ab]illing", true},
		{"[^a]illing", true},
		{"[a-c]illing", true},
		{`Bill\*`, true},
		{`[\]]x`, true},
		{`Bill\[x`, true},
		{`a\\`, true},
		{`Bill*[`, false},
		{`billing_*[`, false},
		{`a*b[`, false},
		{`Bill*\`, false},
		{`Bill*[]`, false},
		{`Bill*[a-]`, false},
		{`Bill*[^]`, false},
		{`Bill\`, false},
		{`Bill[ing`, false},
		{`Bill[]ing`, false},
		{`Bill[^]ing`, false},
		{`Bill[a-]ing`, false},
		{`Bill[z-a]ing`, true},
		{`[!B]illing`, true},
		{`[`, false},
		{`\`, false},
	} {
		t.Run(tc.pattern, func(t *testing.T) {
			if got := validPattern(tc.pattern); got != tc.want {
				t.Errorf("validPattern(%q) = %v, want %v", tc.pattern, got, tc.want)
			}
		})
	}
}

func TestBangClassIsNotNegation(t *testing.T) {
	t.Parallel()
	if !matchesAnyPattern([]string{"[!B]illing"}, "Billing") {
		t.Fatal("[!B]illing no longer matches Billing; the runtime's matcher changed")
	}
	if matchesAnyPattern([]string{"[^B]illing"}, "Billing") {
		t.Fatal("[^B]illing matched Billing")
	}
}
