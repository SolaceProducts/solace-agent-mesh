// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package testharness

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/structured"
	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type GenerateTestPlanParams struct {
	ComponentName string `json:"componentName" desc:"Name of the wrapped workflow or agent the plan is for."`

	ComponentKind string `json:"componentKind" desc:"One of 'workflow', 'agent', or 'skill'."`

	Description *string `json:"description,omitempty" desc:"For a workflow or agent: the description of the tool that calls it, copied exactly as the tool declares it. The expected output comes only from this. Required for a workflow or agent. For a skill: optional free-form description."`

	UserHint *string `json:"userHint,omitempty" desc:"Optional user-supplied framing for the test (e.g. 'edge case', 'happy path')."`

	InputSchema map[string]any `json:"inputSchema,omitempty" desc:"For a workflow: the JSON Schema (an object with 'properties' and 'required') describing the workflow's input, built from the workflow tool's current parameters. The sample input is generated to conform to it."`
}

type TestPlan struct {
	ComponentName  string `json:"componentName"`
	ComponentKind  string `json:"componentKind"`
	SampleInput    any    `json:"sampleInput"`
	ExpectedOutput any    `json:"expectedOutput"`
	Rationale      string `json:"rationale"`
}

const generateTestPlanInstructions = `## generate_test_plan

Use this tool whenever the user asks for sample inputs, an example test, or
the "Create a test for me" preset. Pass the wrapped component's name and
kind verbatim — those values come from the test harness configuration and
the platform; do not substitute a different name. When the component is a
workflow, also pass inputSchema (the workflow's input JSON Schema) so the
sample input matches the workflow.

Also pass description: the description of the tool that calls the workflow or
agent, copied exactly as it is declared, including any "Declared output
fields" it ends with. The expected output is derived only from it. Never
write or reword the description yourself.

Do not write the sample input or expected output yourself in the chat. The
tool result is rendered as an inline editable card; describing the plan in
prose duplicates that surface and confuses the user.`

func generateTestPlan(ctx context.Context, p GenerateTestPlanParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	if strings.TrimSpace(p.ComponentName) == "" {
		return sdk.Error("componentName is required"), nil
	}
	kind := strings.ToLower(strings.TrimSpace(p.ComponentKind))
	if kind != "workflow" && kind != "agent" && kind != "skill" {
		return sdk.Error("componentKind must be 'workflow', 'agent', or 'skill'"), nil
	}

	if contractApplies(kind) && isBlank(p.Description) {
		return sdk.Error("description is required for a workflow or agent: call generate_test_plan again with " +
			"description set to the wrapped tool's description, copied exactly as the tool declares it"), nil
	}

	schema, schemaNote := resolveInputSchema(kind, p.InputSchema)
	userPrompt := buildTestPlanUserPrompt(p, kind, schema)

	callLLM := func(prompt string) (string, error) {
		return tc.CallLLM(ctx, testPlanSystemPrompt, prompt, 0.3)
	}
	plan, violations, err := planConformingToSchema(callLLM, userPrompt, p.ComponentName, kind, schema)
	if err != nil {
		return sdk.Error(err.Error()), nil
	}

	expectationUnspecified := false
	if contractApplies(kind) {
		declared := declaredBy(p.Description)
		switch {
		case declared == "":
			plan.ExpectedOutput = expectedOutputUnspecified
			expectationUnspecified = true
		case isUnspecifiedExpectation(plan.ExpectedOutput):
			plan.ExpectedOutput = asDeclaredExpectation(kind, declared)
		}
	}

	planBytes, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return sdk.Error(fmt.Sprintf("marshal test plan: %v", err)), nil
	}
	if err := tc.SaveArtifact(TestPlanArtifactFilename, planBytes, "application/json"); err != nil {
		_ = tc.SendStatus(fmt.Sprintf("warning: could not save test plan artifact: %v", err))
	}

	return sdk.OK(testPlanMessage(schemaNote, violations, expectationUnspecified), sdk.WithData(structAsMap(plan))), nil
}

func testPlanMessage(schemaNote string, violations []string, expectationUnspecified bool) string {
	msg := "Test plan generated."
	if expectationUnspecified {
		msg += " NOTE: the component declares nothing about what it returns, so the expected output is " +
			"unspecified and the run will be judged only on completing. Tell the user that."
	}
	if schemaNote != "" {
		msg += " NOTE: the sample input was NOT checked against the workflow's input schema (" + schemaNote +
			"). Tell the user the plan is unchecked."
	}
	if len(violations) > 0 {
		msg += " WARNING: the sample input does not conform to the workflow's input schema: " +
			strings.Join(violations, "; ") + ". Tell the user the plan does not match the workflow's input schema."
	}
	return msg
}

func planConformingToSchema(callLLM func(prompt string) (string, error), userPrompt, componentName, kind string, schema map[string]any) (TestPlan, []string, error) {
	const maxAttempts = 2
	prompt := userPrompt
	var best TestPlan
	var bestViolations []string
	bestCount := 0
	for attempt := range maxAttempts {
		plan, err := requestTestPlan(callLLM, prompt, componentName, kind)
		if err != nil {
			if attempt > 0 {
				return best, bestViolations, nil
			}
			return TestPlan{}, nil, err
		}
		if schema == nil {
			return plan, nil, nil
		}
		all := structured.Validate(plan.SampleInput, schema)
		if len(all) == 0 {
			return plan, nil, nil
		}
		if attempt == 0 || len(all) < bestCount {
			best, bestViolations, bestCount = plan, summarizeViolations(all, maxViolationsInResult), len(all)
		}
		previous, _ := json.Marshal(plan.SampleInput)
		prompt = userPrompt + "\n\nYour previous sampleInput was:\n" + string(previous) +
			"\nIt did not conform to the input schema:\n- " + strings.Join(summarizeViolations(all, maxViolationsInRetryPrompt), "\n- ") +
			"\nReturn the corrected JSON object (unless the user's hint asked for invalid input)."
	}
	return best, bestViolations, nil
}

func requestTestPlan(callLLM func(prompt string) (string, error), prompt, componentName, kind string) (TestPlan, error) {
	response, err := callLLM(prompt)
	if err != nil {
		return TestPlan{}, fmt.Errorf("LLM call failed: %w", err)
	}
	return parseAndValidateTestPlan(response, componentName, kind)
}

const (
	maxViolationsInResult      = 3
	maxViolationsInRetryPrompt = 10
	maxViolationLen            = 200
)

func summarizeViolations(all []string, limit int) []string {
	shown := all[:min(len(all), limit)]
	out := make([]string, 0, len(shown)+1)
	for _, v := range shown {
		if r := []rune(v); len(r) > maxViolationLen {
			v = string(r[:maxViolationLen]) + "..."
		}
		out = append(out, v)
	}
	if extra := len(all) - len(shown); extra > 0 {
		out = append(out, fmt.Sprintf("(+%d more not shown)", extra))
	}
	return out
}

func resolveInputSchema(kind string, in map[string]any) (map[string]any, string) {
	if kind != "workflow" {
		return nil, ""
	}
	if len(in) == 0 {
		return nil, "inputSchema was missing or empty"
	}
	schema, ok := objectSchema(in)
	if !ok {
		return nil, "inputSchema is not an object schema"
	}
	if err := structured.CompileSchema(schema); err != nil {
		return nil, "inputSchema is not a valid, self-contained JSON Schema"
	}
	for _, v := range structured.Validate(map[string]any{}, schema) {
		if strings.Contains(v, "reference cycle") {
			return nil, "inputSchema is not a valid, self-contained JSON Schema"
		}
	}
	return schema, ""
}

func objectSchema(in map[string]any) (map[string]any, bool) {
	t, ok := in["type"]
	if !ok {
		schema := maps.Clone(in)
		schema["type"] = "object"
		return schema, true
	}
	return in, t == "object"
}

const TestPlanArtifactFilename = "test_plan.json"

func parseAndValidateTestPlan(response, componentName, kind string) (TestPlan, error) {
	var plan TestPlan
	if _, err := extractJSON(response, &plan); err != nil {
		return TestPlan{}, fmt.Errorf("could not parse test plan from LLM response: %w", err)
	}

	plan.ComponentName = componentName
	plan.ComponentKind = kind
	if plan.SampleInput == nil {
		return TestPlan{}, fmt.Errorf("LLM response missing required field: sampleInput")
	}
	if plan.ExpectedOutput == nil {
		return TestPlan{}, fmt.Errorf("LLM response missing required field: expectedOutput")
	}
	return plan, nil
}

func structAsMap(v any) map[string]any {
	bytes, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("test_harness: marshal struct to map: %v", err))
	}
	var out map[string]any
	if err := json.Unmarshal(bytes, &out); err != nil {
		panic(fmt.Sprintf("test_harness: unmarshal struct to map: %v", err))
	}
	return out
}

const testPlanSystemPrompt = `You are a test planner for the Solace Agent Mesh AI Builder.
Your job is to design a single, representative test for one workflow, agent, or skill.

Output ONLY a JSON object — no prose, no markdown fences. The shape MUST be:

{
  "sampleInput": <string OR JSON object — the input to send to the component>,
  "expectedOutput": <string OR JSON object — what the component should produce>,
  "rationale": "<one or two sentences explaining why this test is representative>"
}

Rules:
- "sampleInput" is what the user (or another agent) would actually send.
  Match the component's expected interface — if it's a chat agent, this
  is a natural-language string; if it's a workflow with a typed input
  schema, this is the JSON payload that conforms to it. For a skill, this
  is the natural-language ask that should cause the test agent to invoke
  the skill's bundled tools (e.g. "find me a book about <topic>" for a
  library-search skill).
- "expectedOutput" states what the component's DECLARATION says it produces,
  and nothing else. For a workflow or agent the request gives that
  declaration: the component's own description and, for a workflow, a final
  "Declared output fields:" list. Those are the only sources. The component's
  name is only a label: never infer an output from it, from what similar
  components usually return, or from the sample input, and never add a field,
  a step or a deliverable the declaration does not name.
  - A "Declared output fields" list is given: expectedOutput is a JSON object
    with exactly the field names listed there (if the list ends "and N more",
    only the names shown) and no others. Each value is a short description of
    that field's content, taken from the description. Add no quality the
    description does not state (no "concise", "detailed", "formal", a length
    or a count).
  - Only a description is given: expectedOutput is a natural-language STRING
    restating what the description says the component produces. Do NOT wrap
    it in a JSON object, do NOT name fields, and do NOT prescribe a
    presentation format (markdown vs plain text).
  - Only when the request says the component declares nothing: set
    expectedOutput to the string "unspecified". Do not guess; the tool
    replaces it with a fixed statement.
  For a skill, expectedOutput is the response the test agent should give
  AFTER successfully exercising the skill's bundled tools — describe both
  the tool call(s) that should fire and the final user-visible reply.
- The plan must be runnable as-is. Don't ask the user follow-up questions
  in the JSON.
- Pick a happy-path scenario unless the user explicitly asks for an edge
  case.`

func buildTestPlanUserPrompt(p GenerateTestPlanParams, kind string, inputSchema map[string]any) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Generate a test plan for the %s named %q.\n", kind, p.ComponentName)
	if contractApplies(kind) {
		writeDeclared(&b, kind, declaredBy(p.Description), true)
	} else if p.Description != nil {
		if d := strings.TrimSpace(*p.Description); d != "" {
			fmt.Fprintf(&b, "\nComponent description:\n%s\n", d)
		}
	}
	if p.UserHint != nil {
		if h := strings.TrimSpace(*p.UserHint); h != "" {
			fmt.Fprintf(&b, "\nUser-supplied hint about the kind of test they want:\n%s\n", h)
		}
	}
	if len(inputSchema) > 0 {
		schemaJSON, _ := json.MarshalIndent(inputSchema, "", "  ")
		fmt.Fprintf(&b, "\nThe %s's input schema. sampleInput MUST be a JSON object that validates against it: "+
			"use exactly these property names, give every property a realistic value of the declared type "+
			"(respect enums and formats), and add no other properties, unless the user's hint above asks for "+
			"invalid input:\n%s\n", kind, schemaJSON)
	}
	b.WriteString("\nReturn the JSON object described in the system prompt and nothing else.")
	return b.String()
}
