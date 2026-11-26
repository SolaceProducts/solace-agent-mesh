// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package testharness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/structured"
	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func TestParseAndValidateTestPlan_OverridesNameAndKind(t *testing.T) {
	t.Parallel()
	body := `{
		"componentName": "WrongName",
		"componentKind": "agent",
		"sampleInput": "hi",
		"expectedOutput": "hello",
		"rationale": "happy path"
	}`
	plan, err := parseAndValidateTestPlan(body, "RealAgent", "workflow")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if plan.ComponentName != "RealAgent" {
		t.Errorf("ComponentName = %q, want %q", plan.ComponentName, "RealAgent")
	}
	if plan.ComponentKind != "workflow" {
		t.Errorf("ComponentKind = %q, want %q", plan.ComponentKind, "workflow")
	}
}

func TestParseAndValidateTestPlan_RequiresSampleAndExpected(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
	}{
		{
			"missing sampleInput",
			`{"componentName":"X","componentKind":"agent","expectedOutput":"e"}`,
		},
		{
			"missing expectedOutput",
			`{"componentName":"X","componentKind":"agent","sampleInput":"i"}`,
		},
		{
			"both missing",
			`{"componentName":"X","componentKind":"agent","rationale":"none"}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseAndValidateTestPlan(c.body, "X", "agent")
			if err == nil {
				t.Error("expected error for missing required field")
			}
		})
	}
}

func TestParseAndValidateTestPlan_AcceptsObjectInputAndOutput(t *testing.T) {
	t.Parallel()
	body := `{
		"sampleInput": {"query":"weather","city":"Seattle"},
		"expectedOutput": {"forecast":"any string","temp":"number"}
	}`
	plan, err := parseAndValidateTestPlan(body, "WeatherWorkflow", "workflow")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	in, ok := plan.SampleInput.(map[string]any)
	if !ok {
		t.Fatalf("SampleInput not an object: %T", plan.SampleInput)
	}
	if in["query"] != "weather" {
		t.Errorf("SampleInput.query = %v, want weather", in["query"])
	}
}

func TestParseAndValidateTestPlan_HandlesFencedJSON(t *testing.T) {
	t.Parallel()
	body := "Here's a plan:\n```json\n{\"sampleInput\":\"hi\",\"expectedOutput\":\"hello\"}\n```"
	plan, err := parseAndValidateTestPlan(body, "X", "agent")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if plan.SampleInput != "hi" || plan.ExpectedOutput != "hello" {
		t.Errorf("got %+v, want sampleInput=hi expectedOutput=hello", plan)
	}
}

func TestBuildTestPlanUserPrompt_IncludesContext(t *testing.T) {
	t.Parallel()
	desc := "translates text"
	hint := "edge case: empty input"
	prompt := buildTestPlanUserPrompt(GenerateTestPlanParams{
		ComponentName: "Translator",
		Description:   &desc,
		UserHint:      &hint,
	}, "agent", nil)
	for _, want := range []string{"Translator", "translates text", "edge case: empty input", "agent"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q\nfull prompt:\n%s", want, prompt)
		}
	}
}

func TestBuildTestPlanUserPrompt_OmitsEmptyOptionals(t *testing.T) {
	t.Parallel()
	prompt := buildTestPlanUserPrompt(GenerateTestPlanParams{
		ComponentName: "Translator",
	}, "agent", nil)
	if strings.Contains(prompt, "input schema") {
		t.Errorf("prompt unexpectedly mentions an input schema when none was given:\n%s", prompt)
	}
	if strings.Contains(prompt, "Component description:") {
		t.Errorf("prompt unexpectedly includes description header for blank Description:\n%s", prompt)
	}
	if strings.Contains(prompt, "User-supplied hint") {
		t.Errorf("prompt unexpectedly includes hint header for blank UserHint:\n%s", prompt)
	}
}

func ticketSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title":  map[string]any{"type": "string"},
			"body":   map[string]any{"type": "string"},
			"tags":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"status": map[string]any{"type": "string", "enum": []any{"available", "pending", "sold"}},
		},
		"required":             []any{"title", "body", "tags", "status"},
		"additionalProperties": false,
	}
}

func TestBuildTestPlanUserPrompt_IncludesInputSchema(t *testing.T) {
	t.Parallel()
	prompt := buildTestPlanUserPrompt(GenerateTestPlanParams{ComponentName: "Triage"}, "workflow", ticketSchema())
	for _, want := range []string{"input schema", `"title"`, `"status"`, `"available"`, "unless the user's hint above asks for invalid input"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q\nfull prompt:\n%s", want, prompt)
		}
	}
}

const (
	conformingPlan = `{"sampleInput":{"title":"T","body":"B","tags":["a"],"status":"available"},"expectedOutput":"ok"}`
	inventedPlan   = `{"sampleInput":{"ticketId":"TCK-1","customerName":"A","priority":"High"},"expectedOutput":"ok"}`
	badEnumPlan    = `{"sampleInput":{"title":"T","body":"B","tags":["a"],"status":"Open"},"expectedOutput":"ok"}`
)

func scriptedLLM(t *testing.T, responses ...string) (func(string) (string, error), *[]string) {
	t.Helper()
	var prompts []string
	return func(prompt string) (string, error) {
		prompts = append(prompts, prompt)
		if len(prompts) > len(responses) {
			t.Fatalf("unexpected LLM call #%d", len(prompts))
		}
		return responses[len(prompts)-1], nil
	}, &prompts
}

func TestPlanConformingToSchema(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		responses      []string
		schema         map[string]any
		wantCalls      int
		wantViolations bool
	}{
		{"conforming sample accepted first try", []string{conformingPlan}, ticketSchema(), 1, false},
		{"invented fields are retried and corrected", []string{inventedPlan, conformingPlan}, ticketSchema(), 2, false},
		{"enum violation is retried and corrected", []string{badEnumPlan, conformingPlan}, ticketSchema(), 2, false},
		{"still non-conforming after the retry returns the plan with violations", []string{inventedPlan, badEnumPlan}, ticketSchema(), 2, true},
		{"string sample for an object schema is a violation", []string{
			`{"sampleInput":"just a sentence","expectedOutput":"ok"}`, `{"sampleInput":"again","expectedOutput":"ok"}`,
		}, ticketSchema(), 2, true},
		{"no schema means no validation and no retry", []string{inventedPlan}, nil, 1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			callLLM, prompts := scriptedLLM(t, c.responses...)
			plan, violations, err := planConformingToSchema(callLLM, "base prompt", "Triage", "workflow", c.schema)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(*prompts) != c.wantCalls {
				t.Fatalf("LLM calls = %d, want %d", len(*prompts), c.wantCalls)
			}
			if (len(violations) > 0) != c.wantViolations {
				t.Fatalf("violations = %v, wantViolations %v", violations, c.wantViolations)
			}
			if plan.ComponentName != "Triage" {
				t.Errorf("ComponentName = %q", plan.ComponentName)
			}
			if c.wantCalls == 2 && !c.wantViolations {
				in, _ := plan.SampleInput.(map[string]any)
				if in["title"] != "T" {
					t.Errorf("plan was not the corrected one: %v", plan.SampleInput)
				}
			}
		})
	}
}

func TestPlanConformingToSchema_RetryPromptCarriesSampleAndViolations(t *testing.T) {
	t.Parallel()
	callLLM, prompts := scriptedLLM(t, inventedPlan, conformingPlan)
	if _, _, err := planConformingToSchema(callLLM, "base prompt", "Triage", "workflow", ticketSchema()); err != nil {
		t.Fatal(err)
	}
	retry := (*prompts)[1]
	for _, want := range []string{"base prompt", "TCK-1", "High", "did not conform", "additional properties", "Return the corrected JSON object", "unless the user's hint asked for invalid input"} {
		if !strings.Contains(retry, want) {
			t.Errorf("retry prompt missing %q:\n%s", want, retry)
		}
	}
}

func TestPlanConformingToSchema_ExhaustedReturnsCappedViolations(t *testing.T) {
	t.Parallel()
	callLLM, _ := scriptedLLM(t, inventedPlan, inventedPlan)
	_, violations, err := planConformingToSchema(callLLM, "p", "Triage", "workflow", ticketSchema())
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) == 0 || len(violations) > maxViolationsInResult+1 {
		t.Fatalf("violations = %d, want 1..%d: %v", len(violations), maxViolationsInResult+1, violations)
	}
	if !strings.Contains(strings.Join(violations, " "), "ticketId") {
		t.Errorf("violations do not name the offending field: %v", violations)
	}
}

func TestPlanConformingToSchema_KeepsTheBetterPlan(t *testing.T) {
	t.Parallel()
	oneOff := `{"sampleInput":{"title":"T","body":"B","tags":["a"],"status":"Open"},"expectedOutput":"first"}`
	allWrong := `{"sampleInput":{"x":1},"expectedOutput":"second"}`
	callLLM, _ := scriptedLLM(t, oneOff, allWrong)
	plan, violations, err := planConformingToSchema(callLLM, "p", "Triage", "workflow", ticketSchema())
	if err != nil {
		t.Fatal(err)
	}
	if plan.ExpectedOutput != "first" || len(violations) == 0 {
		t.Fatalf("kept %v with %v, want the first (closer) plan", plan.ExpectedOutput, violations)
	}
}

func TestPlanConformingToSchema_BetterRetryWinsAndTiesKeepTheFirst(t *testing.T) {
	t.Parallel()
	oneOff := func(marker string) string {
		return `{"sampleInput":{"title":"T","body":"B","tags":["a"],"status":"Open"},"expectedOutput":"` + marker + `"}`
	}
	allWrong := `{"sampleInput":{"x":1},"expectedOutput":"all wrong"}`

	t.Run("retry with fewer violations wins and its violations are reported", func(t *testing.T) {
		t.Parallel()
		callLLM, _ := scriptedLLM(t, allWrong, oneOff("second"))
		plan, violations, err := planConformingToSchema(callLLM, "p", "Triage", "workflow", ticketSchema())
		if err != nil {
			t.Fatal(err)
		}
		if plan.ExpectedOutput != "second" {
			t.Fatalf("kept %v, want the retry", plan.ExpectedOutput)
		}
		if got := strings.Join(violations, " "); !strings.Contains(got, "status") || strings.Contains(got, "missing properties") {
			t.Errorf("violations describe the wrong plan: %v", violations)
		}
	})
	t.Run("a tie keeps the first plan", func(t *testing.T) {
		t.Parallel()
		callLLM, _ := scriptedLLM(t, oneOff("first"), oneOff("second"))
		plan, _, err := planConformingToSchema(callLLM, "p", "Triage", "workflow", ticketSchema())
		if err != nil || plan.ExpectedOutput != "first" {
			t.Fatalf("plan=%v err=%v, want the first plan on a tie", plan.ExpectedOutput, err)
		}
	})
}

func TestPlanConformingToSchema_RetryPromptIsNotCappedLikeTheResult(t *testing.T) {
	t.Parallel()
	props := map[string]any{}
	sample := map[string]any{}
	for i := range 12 {
		name := fmt.Sprintf("field%d", i)
		props[name] = map[string]any{"type": "string"}
		sample[name] = 1
	}
	schema := map[string]any{"type": "object", "properties": props}
	response, err := json.Marshal(map[string]any{"sampleInput": sample, "expectedOutput": "e"})
	if err != nil {
		t.Fatal(err)
	}
	callLLM, prompts := scriptedLLM(t, string(response), string(response))
	_, violations, err := planConformingToSchema(callLLM, "p", "Triage", "workflow", schema)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != maxViolationsInResult+1 || !strings.Contains(violations[maxViolationsInResult], "+9 more not shown") {
		t.Errorf("result should list %d violations and the overflow, got %q", maxViolationsInResult, violations)
	}
	retry := (*prompts)[1]
	if got := strings.Count(retry, "\n- "); got != maxViolationsInRetryPrompt+1 {
		t.Errorf("retry prompt lists %d bullets, want %d violations plus the overflow line:\n%s", got, maxViolationsInRetryPrompt, retry)
	}
	if !strings.Contains(retry, "(+2 more not shown)") {
		t.Errorf("retry prompt should say how many violations it left out:\n%s", retry)
	}
}

func TestPlanConformingToSchema_Errors(t *testing.T) {
	t.Parallel()
	t.Run("unparsable first response is not retried", func(t *testing.T) {
		t.Parallel()
		callLLM, prompts := scriptedLLM(t, "not json at all")
		if _, _, err := planConformingToSchema(callLLM, "p", "Triage", "workflow", ticketSchema()); err == nil {
			t.Fatal("want error")
		}
		if len(*prompts) != 1 {
			t.Errorf("calls = %d, want 1", len(*prompts))
		}
	})
	t.Run("LLM failure on the first call is an error", func(t *testing.T) {
		t.Parallel()
		callLLM := func(string) (string, error) { return "", errors.New("boom") }
		if _, _, err := planConformingToSchema(callLLM, "p", "Triage", "workflow", ticketSchema()); err == nil || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("err = %v, want LLM failure", err)
		}
	})
	t.Run("LLM failure on the retry falls back to the first plan with its violations", func(t *testing.T) {
		t.Parallel()
		calls := 0
		callLLM := func(string) (string, error) {
			calls++
			if calls == 2 {
				return "", errors.New("boom")
			}
			return inventedPlan, nil
		}
		plan, violations, err := planConformingToSchema(callLLM, "p", "Triage", "workflow", ticketSchema())
		if err != nil || len(violations) == 0 || plan.SampleInput == nil {
			t.Fatalf("plan=%v violations=%v err=%v, want the first plan and its violations", plan, violations, err)
		}
	})
	t.Run("unparsable retry falls back to the first plan", func(t *testing.T) {
		t.Parallel()
		callLLM, _ := scriptedLLM(t, inventedPlan, "garbage")
		plan, violations, err := planConformingToSchema(callLLM, "p", "Triage", "workflow", ticketSchema())
		if err != nil || len(violations) == 0 || plan.SampleInput == nil {
			t.Fatalf("plan=%v violations=%v err=%v", plan, violations, err)
		}
	})
}

func TestSummarizeViolations_BoundsCountAndLength(t *testing.T) {
	t.Parallel()
	got := summarizeViolations([]string{strings.Repeat("x", 500), "b", "c", "d", "e"}, 3)
	want := []string{strings.Repeat("x", 200) + "...", "b", "c", "(+2 more not shown)"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := summarizeViolations([]string{"a", "b"}, 3); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("short list changed: %q", got)
	}
	multi := summarizeViolations([]string{strings.Repeat("é", 300)}, 3)
	if want := strings.Repeat("é", 200) + "..."; multi[0] != want {
		t.Errorf("multibyte truncation wrong: %d runes", len([]rune(multi[0])))
	}
}

func TestResolveInputSchema(t *testing.T) {
	t.Parallel()
	withRef := func(ref string) map[string]any {
		return map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"$ref": ref}}}
	}
	const notValid = "not a valid, self-contained JSON Schema"
	cases := []struct {
		name     string
		kind     string
		in       map[string]any
		wantUsed bool
		wantNote string
	}{
		{"valid workflow schema is used", "workflow", ticketSchema(), true, ""},
		{"agent ignores the schema without a note", "agent", ticketSchema(), false, ""},
		{"skill ignores the schema without a note", "skill", ticketSchema(), false, ""},
		{"workflow without a schema says so", "workflow", nil, false, "missing or empty"},
		{"empty schema says so", "workflow", map[string]any{}, false, "missing or empty"},
		{"uncompilable schema is unusable", "workflow", map[string]any{"type": "object", "properties": "nope"}, false, notValid},
		{"non-object root is unusable", "workflow", map[string]any{"type": "array"}, false, "not an object schema"},
		{"non-comparable root type does not panic", "workflow", map[string]any{"type": []any{"object", "null"}}, false, "not an object schema"},
		{"file $ref is refused", "workflow", withRef("file:///etc/hosts"), false, notValid},
		{"relative $ref is refused", "workflow", withRef("secret.json"), false, notValid},
		{"root $schema file is refused", "workflow", map[string]any{"$schema": "file:///etc/hosts", "type": "object"}, false, notValid},
		{"nested $schema file is refused", "workflow", map[string]any{
			"type": "object", "properties": map[string]any{"a": map[string]any{"$schema": "file:///etc/hosts"}},
		}, false, notValid},
		{"$recursiveRef file is refused", "workflow", map[string]any{"type": "object", "$recursiveRef": "file:///etc/hosts"}, false, notValid},
		{"relative $recursiveRef is refused", "workflow", map[string]any{"type": "object", "$recursiveRef": "secret.json"}, false, notValid},
		{"$dynamicRef file is refused", "workflow", map[string]any{"type": "object", "$dynamicRef": "file:///etc/hosts"}, false, notValid},
		{"reference cycle is unusable", "workflow", map[string]any{"type": "object", "$ref": "#"}, false, notValid},
		{"local $ref is allowed", "workflow", map[string]any{
			"type":       "object",
			"$defs":      map[string]any{"s": map[string]any{"type": "string"}},
			"properties": map[string]any{"a": map[string]any{"$ref": "#/$defs/s"}},
		}, true, ""},
		{"a $ref-shaped const value is just data", "workflow", map[string]any{
			"type":       "object",
			"properties": map[string]any{"a": map[string]any{"const": map[string]any{"$ref": "http://x"}}},
		}, true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			schema, note := resolveInputSchema(c.kind, c.in)
			if (schema != nil) != c.wantUsed {
				t.Fatalf("schema used = %v, want %v (note %q)", schema != nil, c.wantUsed, note)
			}
			if !strings.Contains(note, c.wantNote) || (c.wantNote == "" && note != "") {
				t.Errorf("note = %q, want containing %q", note, c.wantNote)
			}
			if strings.Contains(note, "file:") || strings.Contains(note, "/") {
				t.Errorf("note must be a fixed string, not loader or path text: %q", note)
			}
		})
	}
}

func TestResolveInputSchema_RefusesReferenceToExistingFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "secret.json")
	if err := os.WriteFile(path, []byte(`{"type":"string"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	target := (&url.URL{Scheme: "file", Path: path}).String()
	cases := map[string]map[string]any{
		"$ref":          {"type": "object", "properties": map[string]any{"a": map[string]any{"$ref": target}}},
		"$recursiveRef": {"type": "object", "$recursiveRef": target},
		"$dynamicRef":   {"type": "object", "$dynamicRef": target},
		"$schema":       {"type": "object", "$schema": target},
	}
	for name, in := range cases {
		if schema, note := resolveInputSchema("workflow", in); schema != nil || note == "" {
			t.Errorf("%s to an existing file was accepted (note %q)", name, note)
		}
	}
}

func TestResolveInputSchema_MissingTypeDefaultsToObjectWithoutMutatingInput(t *testing.T) {
	t.Parallel()
	in := map[string]any{"properties": map[string]any{"n": map[string]any{"type": "integer"}}}
	schema, note := resolveInputSchema("workflow", in)
	if schema == nil || note != "" {
		t.Fatalf("schema = %v, note = %q", schema, note)
	}
	if _, has := in["type"]; has {
		t.Error("caller's map was mutated")
	}
	if v := structured.Validate("hello", schema); len(v) == 0 {
		t.Error("a string sample must violate a schema whose root defaulted to object")
	}
}

func invokeGenerateTestPlan(t *testing.T, args map[string]any, responses ...string) (wire map[string]any, prompts []string, statuses []string) {
	t.Helper()
	return invokeGenerateTestPlanIn(t, t.TempDir(), args, responses...)
}

func invokeGenerateTestPlanIn(t *testing.T, outputDir string, args map[string]any, responses ...string) (wire map[string]any, prompts []string, statuses []string) {
	t.Helper()
	out, err := sdk.InvokeInProcess(t.Context(), Tools(), "generate_test_plan", args, nil, sdk.InProcessBindings{
		OutputDir: outputDir,
		Status:    func(s string) error { statuses = append(statuses, s); return nil },
		LLM: func(_ context.Context, _, user string, _ float64) (string, error) {
			prompts = append(prompts, user)
			if len(prompts) > len(responses) {
				t.Fatalf("unexpected LLM call #%d", len(prompts))
			}
			return responses[len(prompts)-1], nil
		},
	})
	if err != nil {
		t.Fatalf("InvokeInProcess: %v", err)
	}
	wire, _ = out.(map[string]any)
	return wire, prompts, statuses
}

func schemaArg() map[string]any {
	var m map[string]any
	if err := json.Unmarshal([]byte(`{"type":"object","properties":{"title":{"type":"string"},"status":{"type":"string","enum":["available","sold"]}},"required":["title","status"]}`), &m); err != nil {
		panic(err)
	}
	return m
}

func TestGenerateTestPlan_EndToEnd_UsesInputSchema(t *testing.T) {
	t.Parallel()
	const ok = `{"sampleInput":{"title":"T","status":"sold"},"expectedOutput":"ok"}`
	const bad = `{"sampleInput":{"category":"bug"},"expectedOutput":"ok"}`
	base := func() map[string]any {
		return map[string]any{"componentName": "TicketFlow", "componentKind": "workflow", "inputSchema": schemaArg(), "description": `Invoke the "TicketFlow" workflow. Triages tickets.`}
	}

	t.Run("conforming plan: one call, schema is in the prompt, no warning", func(t *testing.T) {
		t.Parallel()
		wire, prompts, _ := invokeGenerateTestPlan(t, base(), ok)
		if wire["status"] != "success" || len(prompts) != 1 {
			t.Fatalf("status=%v calls=%d wire=%v", wire["status"], len(prompts), wire)
		}
		if !strings.Contains(prompts[0], `"enum"`) || !strings.Contains(prompts[0], "sold") {
			t.Errorf("schema missing from the planner prompt:\n%s", prompts[0])
		}
		if msg, _ := wire["message"].(string); strings.Contains(msg, "NOTE") || strings.Contains(msg, "WARNING") {
			t.Errorf("clean plan carries a warning: %q", msg)
		}
	})

	t.Run("non-conforming twice: plan still returned, result names the violation", func(t *testing.T) {
		t.Parallel()
		wire, prompts, _ := invokeGenerateTestPlan(t, base(), bad, bad)
		if wire["status"] != "success" || len(prompts) != 2 {
			t.Fatalf("status=%v calls=%d wire=%v", wire["status"], len(prompts), wire)
		}
		msg, _ := wire["message"].(string)
		if !strings.Contains(msg, "WARNING") || !strings.Contains(msg, "missing properties") || !strings.Contains(msg, "does not match the workflow's input schema") {
			t.Errorf("message does not report the violation: %q", msg)
		}
	})

	t.Run("garbled schema: plan returned, result says it was not checked", func(t *testing.T) {
		t.Parallel()
		args := base()
		args["inputSchema"] = map[string]any{"type": "bogus"}
		wire, prompts, _ := invokeGenerateTestPlan(t, args, bad)
		msg, _ := wire["message"].(string)
		if wire["status"] != "success" || len(prompts) != 1 || !strings.Contains(msg, "NOTE:") || !strings.Contains(msg, "NOT checked") {
			t.Fatalf("status=%v calls=%d message=%q", wire["status"], len(prompts), msg)
		}
		if strings.Contains(prompts[0], "input schema") {
			t.Errorf("an unusable schema must not reach the prompt:\n%s", prompts[0])
		}
	})

	t.Run("workflow without a schema says it was not checked", func(t *testing.T) {
		t.Parallel()
		args := base()
		delete(args, "inputSchema")
		wire, _, _ := invokeGenerateTestPlan(t, args, bad)
		if msg, _ := wire["message"].(string); !strings.Contains(msg, "NOTE:") || !strings.Contains(msg, "NOT checked") || !strings.Contains(msg, "Tell the user the plan is unchecked") || regexp.MustCompile(`(?i)call generate_test_plan`).MatchString(msg) {
			t.Errorf("message = %q", msg)
		}
	})

	t.Run("agent ignores the schema entirely", func(t *testing.T) {
		t.Parallel()
		args := base()
		args["componentKind"] = "agent"
		wire, prompts, _ := invokeGenerateTestPlan(t, args, `{"sampleInput":"hello","expectedOutput":"hi"}`)
		msg, _ := wire["message"].(string)
		if wire["status"] != "success" || len(prompts) != 1 || strings.Contains(prompts[0], "input schema") || strings.Contains(msg, "NOT checked") {
			t.Fatalf("status=%v calls=%d message=%q prompt=%q", wire["status"], len(prompts), msg, prompts)
		}
	})
}

func TestGenerateTestPlanParams_SchemaIsOptionalInToolSchema(t *testing.T) {
	t.Parallel()
	schema := sdk.BuildSchema[GenerateTestPlanParams]()
	prop, ok := schema.Properties["inputSchema"].(map[string]any)
	if !ok || prop["type"] != "object" {
		t.Fatalf("inputSchema must be exposed to the LLM as an object, got %v", schema.Properties["inputSchema"])
	}
	for _, r := range schema.Required {
		if r == "inputSchema" {
			t.Fatal("inputSchema must stay optional so agent and skill plans are not blocked")
		}
	}
}

func TestStructAsMap_PreservesJSONTags(t *testing.T) {
	t.Parallel()
	plan := TestPlan{
		ComponentName:  "Translator",
		ComponentKind:  "agent",
		SampleInput:    "hi",
		ExpectedOutput: "hello",
		Rationale:      "happy path",
	}
	got := structAsMap(plan)
	for _, k := range []string{"componentName", "componentKind", "sampleInput", "expectedOutput"} {
		if _, ok := got[k]; !ok {
			t.Errorf("missing key %q in %v", k, got)
		}
	}
	if got["componentName"] != "Translator" {
		t.Errorf("componentName = %v, want Translator", got["componentName"])
	}
}

func TestGenerateTestPlan_RejectsBlankComponentName(t *testing.T) {
	t.Parallel()
	res, err := generateTestPlan(t.Context(), GenerateTestPlanParams{ComponentName: " ", ComponentKind: "agent"}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("generateTestPlan: %v", err)
	}
	if res.Status != "error" || !strings.Contains(res.Message, "componentName") {
		t.Errorf("got %+v, want error about componentName", res)
	}
}

func TestGenerateTestPlan_RejectsInvalidKind(t *testing.T) {
	t.Parallel()
	cases := []string{"", "task", "random"}
	for _, k := range cases {
		t.Run(k, func(t *testing.T) {
			t.Parallel()
			res, err := generateTestPlan(t.Context(), GenerateTestPlanParams{ComponentName: "X", ComponentKind: k}, &sdk.ToolContext{})
			if err != nil {
				t.Fatalf("generateTestPlan: %v", err)
			}
			if res.Status != "error" || !strings.Contains(res.Message, "componentKind") {
				t.Errorf("kind=%q: got %+v, want error about componentKind", k, res)
			}
		})
	}
}

func TestGenerateRecommendations_RejectsBlankComponentName(t *testing.T) {
	t.Parallel()
	res, err := generateRecommendations(t.Context(), GenerateRecommendationsParams{
		ComponentName:  " ",
		ComponentKind:  "agent",
		SampleInput:    "s",
		ExpectedOutput: "e",
		ActualOutput:   "a",
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("generateRecommendations: %v", err)
	}
	if res.Status != "error" || !strings.Contains(res.Message, "componentName") {
		t.Errorf("got %+v, want error about componentName", res)
	}
}

func TestGenerateRecommendations_RejectsInvalidKind(t *testing.T) {
	t.Parallel()
	res, err := generateRecommendations(t.Context(), GenerateRecommendationsParams{
		ComponentName:  "X",
		ComponentKind:  "bogus",
		SampleInput:    "s",
		ExpectedOutput: "e",
		ActualOutput:   "a",
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("generateRecommendations: %v", err)
	}
	if res.Status != "error" || !strings.Contains(res.Message, "componentKind") {
		t.Errorf("got %+v, want error about componentKind", res)
	}
}

func TestGenerateRecommendations_RejectsMissingInputs(t *testing.T) {
	t.Parallel()
	base := GenerateRecommendationsParams{
		ComponentName:  "X",
		ComponentKind:  "agent",
		SampleInput:    "s",
		ExpectedOutput: "e",
		ActualOutput:   "a",
	}
	cases := []struct {
		name string
		mod  func(*GenerateRecommendationsParams)
		want string
	}{
		{"sampleInput nil", func(p *GenerateRecommendationsParams) { p.SampleInput = nil }, "sampleInput"},
		{"expectedOutput nil", func(p *GenerateRecommendationsParams) { p.ExpectedOutput = nil }, "expectedOutput"},
		{"actualOutput nil", func(p *GenerateRecommendationsParams) { p.ActualOutput = nil }, "actualOutput"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p := base
			c.mod(&p)
			res, err := generateRecommendations(t.Context(), p, &sdk.ToolContext{})
			if err != nil {
				t.Fatalf("generateRecommendations: %v", err)
			}
			if res.Status != "error" || !strings.Contains(res.Message, c.want) {
				t.Errorf("%s: got %+v, want error about %s", c.name, res, c.want)
			}
		})
	}
}

func TestStructAsMap_AcceptsObjectFields(t *testing.T) {
	t.Parallel()
	plan := TestPlan{
		ComponentName:  "WeatherWorkflow",
		ComponentKind:  "workflow",
		SampleInput:    map[string]any{"city": "Seattle"},
		ExpectedOutput: map[string]any{"forecast": "any"},
	}
	got := structAsMap(plan)
	in, _ := got["sampleInput"].(map[string]any)
	if in["city"] != "Seattle" {
		t.Errorf("sampleInput.city = %v, want Seattle", in["city"])
	}
}

func TestGenerateTestPlan_AcceptsSkillKind(t *testing.T) {
	t.Parallel()
	res, err := generateTestPlan(t.Context(), GenerateTestPlanParams{
		ComponentName: "library-skill",
		ComponentKind: "skill",
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("generateTestPlan: %v", err)
	}
	if strings.Contains(res.Message, "componentKind") {
		t.Errorf("kind=\"skill\" was rejected by the guard: %+v", res)
	}
}

func TestGenerateRecommendations_AcceptsSkillKind(t *testing.T) {
	t.Parallel()
	res, err := generateRecommendations(t.Context(), GenerateRecommendationsParams{
		ComponentName:  "library-skill",
		ComponentKind:  "skill",
		SampleInput:    "find me a book",
		ExpectedOutput: "the agent invokes library_search and replies with a title",
		ActualOutput:   "ok",
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("generateRecommendations: %v", err)
	}
	if strings.Contains(res.Message, "componentKind") {
		t.Errorf("kind=\"skill\" was rejected by the guard: %+v", res)
	}
}
