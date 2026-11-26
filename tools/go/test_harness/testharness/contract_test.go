// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package testharness

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

const (
	docPipelineDescription = `Invoke the "DocPipeline" workflow. Summarizes a document and translates the summary into Spanish. ` +
		`Declared output fields: english_summary, spanish_summary.`
	bareWorkflowDescription = `Invoke the "DocPipeline" workflow.`
	bareAgentDescription    = `Delegate a task to the "Translator" agent. Skills: translate, detect`
	guessedExpectation      = `{"keywords":"extracted keywords","summary":"a summary","wordCount":"number"}`
)

func strPtr(s string) *string { return &s }

const docAsDeclared = "As declared by the workflow: Summarizes a document and translates the summary into Spanish. " +
	"Declared output fields: english_summary, spanish_summary."

func TestBuildTestPlanUserPrompt_GivesThePlannerTheDeclaration(t *testing.T) {
	t.Parallel()
	prompt := buildTestPlanUserPrompt(GenerateTestPlanParams{
		ComponentName: "DocPipeline",
		Description:   strPtr(docPipelineDescription),
	}, "workflow", nil)
	for _, want := range []string{
		"What the workflow declares about itself",
		"Summarizes a document and translates the summary into Spanish.",
		"Declared output fields: english_summary, spanish_summary.",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "Invoke the") {
		t.Errorf("the tool's fixed opening sentence reached the planner as if the workflow had declared it:\n%s", prompt)
	}
}

func TestBuildTestPlanUserPrompt_SaysWhenNothingIsDeclared(t *testing.T) {
	t.Parallel()
	for _, description := range []*string{nil, strPtr(""), strPtr(bareWorkflowDescription), strPtr(bareAgentDescription)} {
		prompt := buildTestPlanUserPrompt(GenerateTestPlanParams{ComponentName: "DocPipeline", Description: description}, "workflow", nil)
		if !strings.Contains(prompt, "declares nothing about what it returns") {
			t.Errorf("description %v: prompt does not say nothing is declared:\n%s", description, prompt)
		}
		if strings.Contains(prompt, "declares about itself") || strings.Contains(prompt, "Skills") {
			t.Errorf("description %v: prompt invents a declaration:\n%s", description, prompt)
		}
	}
}

func TestBuildTestPlanUserPrompt_SkillKeepsItsFreeFormDescription(t *testing.T) {
	t.Parallel()
	prompt := buildTestPlanUserPrompt(GenerateTestPlanParams{ComponentName: "Library", Description: strPtr("finds books")}, "skill", nil)
	if !strings.Contains(prompt, "Component description:\nfinds books") || strings.Contains(prompt, "declares nothing") {
		t.Errorf("skill prompt changed:\n%s", prompt)
	}
}

func TestTestPlanSystemPrompt_ForbidsGuessingTheExpectedOutput(t *testing.T) {
	t.Parallel()
	prompt := strings.Join(strings.Fields(testPlanSystemPrompt), " ")
	for _, want := range []string{
		"DECLARATION says it produces, and nothing else",
		"never infer an output from it",
		"exactly the field names listed there",
		"Only when the request says the component declares nothing",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("planner system prompt does not say %q", want)
		}
	}
}

func planFromWire(t *testing.T, wire map[string]any) map[string]any {
	t.Helper()
	data, ok := wire["data"].(map[string]any)
	if !ok {
		t.Fatalf("no data in %v", wire)
	}
	return data
}

func persistedPlan(t *testing.T, dir string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, TestPlanArtifactFilename))
	if err != nil {
		t.Fatal(err)
	}
	var plan map[string]any
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestGenerateTestPlan_ComponentDeclaringNothingGetsAnUnspecifiedExpectation(t *testing.T) {
	t.Parallel()
	guess := `{"sampleInput":{"document_text":"x"},"expectedOutput":` + guessedExpectation + `}`
	for name, args := range map[string]map[string]any{
		"workflow with only the tool's opening sentence": {"componentName": "DocPipeline", "componentKind": "workflow", "description": bareWorkflowDescription},
		"agent with only skill names":                    {"componentName": "Translator", "componentKind": "agent", "description": bareAgentDescription},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			wire, _, _ := invokeGenerateTestPlanIn(t, dir, args, guess)
			if wire["status"] != "success" {
				t.Fatalf("wire = %v", wire)
			}
			if got := planFromWire(t, wire)["expectedOutput"]; got != expectedOutputUnspecified {
				t.Errorf("returned expectedOutput = %v, want the unspecified statement", got)
			}
			if got := persistedPlan(t, dir)["expectedOutput"]; got != expectedOutputUnspecified {
				t.Errorf("persisted expectedOutput = %v, want the unspecified statement", got)
			}
			if msg, _ := wire["message"].(string); !strings.Contains(msg, "unspecified") {
				t.Errorf("result message does not tell the caller the expectation is unspecified: %q", msg)
			}
		})
	}
}

func TestGenerateTestPlan_PlannerGivingUpOnADeclaredComponentIsHeldToTheDeclaration(t *testing.T) {
	t.Parallel()
	const description = `Delegate a task to the "Helper" agent. A helpful assistant.`
	dir := t.TempDir()
	wire, _, _ := invokeGenerateTestPlanIn(t, dir, map[string]any{
		"componentName": "Helper", "componentKind": "agent", "description": description,
	}, `{"sampleInput":"hi","expectedOutput":"unspecified"}`)
	expected, _ := planFromWire(t, wire)["expectedOutput"].(string)
	if want := "As declared by the agent: A helpful assistant."; expected != want {
		t.Fatalf("expectedOutput = %q, want %q", expected, want)
	}
	if got := persistedPlan(t, dir)["expectedOutput"]; got != expected {
		t.Errorf("persisted expectedOutput = %v, want %q", got, expected)
	}
	if msg, _ := wire["message"].(string); strings.Contains(msg, "unspecified") {
		t.Errorf("message says the expectation is unspecified for a component that declares something: %q", msg)
	}

	run := docRunArgs(expected, strPtr(description))
	run["componentName"], run["componentKind"] = "Helper", "agent"
	report, prompt := invokeGenerateRecommendations(t, run, `{"summary":"Replied in Spanish.","recommendations":[]}`)
	if summary := summaryOf(t, report); strings.Contains(summary, "No expected output is declared") {
		t.Errorf("the report claims nothing is declared: %q", summary)
	}
	if !strings.Contains(prompt, expected) || strings.Contains(prompt, "declares nothing") {
		t.Errorf("reviewer must be graded against the same declared expectation the card showed:\n%s", prompt)
	}
}

func TestGenerateTestPlan_MissingDescriptionIsAnErrorForWorkflowAndAgent(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"workflow", "agent"} {
		for _, description := range []any{nil, "", "  "} {
			args := map[string]any{"componentName": "X", "componentKind": kind}
			if description != nil {
				args["description"] = description
			}
			wire, prompts, _ := invokeGenerateTestPlan(t, args)
			msg, _ := wire["message"].(string)
			if wire["status"] != "error" || !strings.Contains(msg, "description is required") || len(prompts) != 0 {
				t.Errorf("%s with description %v: status=%v message=%q llmCalls=%d", kind, description, wire["status"], msg, len(prompts))
			}
		}
	}
}

func TestGenerateTestPlan_DeclaredContractReachesThePlannerAndItsExpectationIsKept(t *testing.T) {
	t.Parallel()
	derived := `{"sampleInput":{"document_text":"x"},"expectedOutput":{"english_summary":"summary of the document","spanish_summary":"that summary in Spanish"}}`
	wire, prompts, _ := invokeGenerateTestPlan(t, map[string]any{
		"componentName": "DocPipeline", "componentKind": "workflow", "description": docPipelineDescription,
	}, derived)
	if wire["status"] != "success" || len(prompts) != 1 {
		t.Fatalf("status=%v calls=%d wire=%v", wire["status"], len(prompts), wire)
	}
	if !strings.Contains(prompts[0], "english_summary, spanish_summary") {
		t.Errorf("declared output fields did not reach the planner:\n%s", prompts[0])
	}
	expected, _ := planFromWire(t, wire)["expectedOutput"].(map[string]any)
	if _, ok := expected["spanish_summary"]; !ok || len(expected) != 2 {
		t.Errorf("expectedOutput = %v, want the planner's declared-fields expectation", expected)
	}
	if msg, _ := wire["message"].(string); strings.Contains(msg, "unspecified") {
		t.Errorf("a component with a declaration was reported as unspecified: %q", msg)
	}
}

func TestGenerateTestPlan_AgentAndSkillKinds(t *testing.T) {
	t.Parallel()
	t.Run("an agent that declares a purpose keeps the planner's expectation", func(t *testing.T) {
		t.Parallel()
		wire, prompts, _ := invokeGenerateTestPlan(t, map[string]any{
			"componentName": "Translator", "componentKind": "agent",
			"description": `Delegate a task to the "Translator" agent. Translates text into Spanish. Skills: translate`,
		}, `{"sampleInput":"hello","expectedOutput":"the text in Spanish"}`)
		if got := planFromWire(t, wire)["expectedOutput"]; got != "the text in Spanish" {
			t.Errorf("expectedOutput = %v", got)
		}
		if !strings.Contains(prompts[0], "Translates text into Spanish.") || strings.Contains(prompts[0], "Skills") {
			t.Errorf("planner prompt should carry the purpose and not the skill names:\n%s", prompts[0])
		}
	})
	t.Run("a skill is not held to a description it was never given", func(t *testing.T) {
		t.Parallel()
		wire, _, _ := invokeGenerateTestPlan(t, map[string]any{"componentName": "Library", "componentKind": "skill"},
			`{"sampleInput":"find a book","expectedOutput":"calls the search tool, then lists a book"}`)
		if got := planFromWire(t, wire)["expectedOutput"]; got == expectedOutputUnspecified {
			t.Error("a skill plan was made unspecified")
		}
	})
}

func invokeGenerateRecommendations(t *testing.T, args map[string]any, response string) (wire map[string]any, prompt string) {
	t.Helper()
	out, err := sdk.InvokeInProcess(t.Context(), Tools(), "generate_recommendations", args, nil, sdk.InProcessBindings{
		OutputDir: t.TempDir(),
		Status:    func(string) error { return nil },
		LLM: func(_ context.Context, _, user string, _ float64) (string, error) {
			prompt = user
			return response, nil
		},
	})
	if err != nil {
		t.Fatalf("InvokeInProcess: %v", err)
	}
	wire, _ = out.(map[string]any)
	return wire, prompt
}

func docRunArgs(expected any, description *string) map[string]any {
	args := map[string]any{
		"componentName":  "DocPipeline",
		"componentKind":  "workflow",
		"sampleInput":    map[string]any{"document_text": "x"},
		"expectedOutput": expected,
		"actualOutput": map[string]any{
			"message": "Workflow completed successfully",
			"output":  map[string]any{"english_summary": "s", "spanish_summary": "r"},
		},
	}
	if description != nil {
		args["description"] = *description
	}
	return args
}

func summaryOf(t *testing.T, wire map[string]any) string {
	t.Helper()
	if wire["status"] != "success" {
		t.Fatalf("wire = %v", wire)
	}
	summary, _ := planFromWire(t, wire)["summary"].(string)
	return summary
}

func TestGenerateRecommendations_UnspecifiedExpectationIsReportedAsSuch(t *testing.T) {
	t.Parallel()
	wire, prompt := invokeGenerateRecommendations(t,
		docRunArgs(expectedOutputUnspecified, strPtr(bareWorkflowDescription)),
		`{"summary":"The run completed and returned both summaries.","recommendations":[]}`)
	summary := summaryOf(t, wire)
	if want := unspecifiedSummaryLead + " The run completed and returned both summaries."; summary != want {
		t.Errorf("summary = %q, want %q", summary, want)
	}
	if !strings.Contains(prompt, "declares nothing about what it returns") || !strings.Contains(prompt, expectedOutputUnspecified) {
		t.Errorf("reviewer was not told nothing is declared:\n%s", prompt)
	}
}

func TestGenerateRecommendations_SkillIsNeverUnspecified(t *testing.T) {
	t.Parallel()
	args := docRunArgs(expectedOutputUnspecified, strPtr("finds books"))
	args["componentKind"] = "skill"
	wire, prompt := invokeGenerateRecommendations(t, args, `{"summary":"Found a book.","recommendations":[]}`)
	if summary := summaryOf(t, wire); summary != "Found a book." {
		t.Errorf("summary = %q, a skill has no declaration to be unspecified against", summary)
	}
	if strings.Contains(prompt, "declares nothing") {
		t.Errorf("reviewer prompt:\n%s", prompt)
	}
}

func TestGenerateRecommendations_DeclarationOutranksAnUnspecifiedExpectation(t *testing.T) {
	t.Parallel()
	wire, prompt := invokeGenerateRecommendations(t,
		docRunArgs(expectedOutputUnspecified, strPtr(docPipelineDescription)),
		`{"summary":"Both declared fields came back.","recommendations":[]}`)
	if summary := summaryOf(t, wire); summary != "Both declared fields came back." {
		t.Errorf("summary = %q, must not carry the unspecified lead", summary)
	}
	if strings.Contains(prompt, "Not specified") || !strings.Contains(prompt, docAsDeclared) ||
		!strings.Contains(prompt, "Declared output fields: english_summary, spanish_summary.") {
		t.Errorf("reviewer prompt should carry the declaration and no unspecified statement:\n%s", prompt)
	}
}

func TestGenerateRecommendations_ExpectationTheUserEditedIsGradedAsWritten(t *testing.T) {
	t.Parallel()
	wire, prompt := invokeGenerateRecommendations(t,
		docRunArgs("Not specified exactly, but the result must include a title", strPtr(bareWorkflowDescription)),
		`{"summary":"Missing the title.","recommendations":[]}`)
	if summary := summaryOf(t, wire); summary != "Missing the title." {
		t.Errorf("summary = %q, an edited expectation must not be treated as unspecified", summary)
	}
	if !strings.Contains(prompt, "must include a title") {
		t.Errorf("the user's expectation did not reach the reviewer:\n%s", prompt)
	}
}

func TestGenerateRecommendations_HandsTheReviewerTheDeclaration(t *testing.T) {
	t.Parallel()
	wire, prompt := invokeGenerateRecommendations(t,
		docRunArgs(`{"summary":"s","translated_summary":"t"}`, strPtr(docPipelineDescription)),
		`{"summary":"ok","recommendations":[]}`)
	if summary := summaryOf(t, wire); summary != "ok" {
		t.Errorf("summary = %q, a declared workflow must keep the reviewer's own sentence", summary)
	}
	if !strings.Contains(prompt, "translated_summary") || strings.Contains(prompt, docAsDeclared) {
		t.Errorf("the passed expectation did not reach the reviewer:\n%s", prompt)
	}
	for _, want := range []string{
		"Summarizes a document and translates the summary into Spanish.",
		"Declared output fields: english_summary, spanish_summary.",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q\n%s", want, prompt)
		}
	}
}

func TestGenerateRecommendations_NoDescriptionLeavesARealExpectationAlone(t *testing.T) {
	t.Parallel()
	_, prompt := invokeGenerateRecommendations(t, docRunArgs("both summaries", nil), `{"summary":"x","recommendations":[]}`)
	if strings.Contains(prompt, "declares nothing") || strings.Contains(prompt, "declares about itself") {
		t.Errorf("reviewer was told something false about the declaration:\n%s", prompt)
	}
}

func TestRecommendationsSystemPrompt_ForbidsRewritingAComponentThatMeetsItsContract(t *testing.T) {
	t.Parallel()
	prompt := strings.Join(strings.Fields(recommendationsSystemPrompt), " ")
	for _, want := range []string{
		"declaration",
		"satisfied the contract: return an empty recommendations array",
		"Never recommend rewriting",
		`"Not specified"`,
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("reviewer system prompt does not say %q", want)
		}
	}
}

func TestExpectedOutputUnspecified_Wording(t *testing.T) {
	t.Parallel()
	const want = "Not specified: this component does not declare what it returns, so the run is judged only on whether it completed and produced a result."
	if expectedOutputUnspecified != want {
		t.Errorf("expectedOutputUnspecified = %q", expectedOutputUnspecified)
	}
}

func TestIsUnspecifiedExpectation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		expected any
		want     bool
	}{
		{expectedOutputUnspecified, true},
		{"  " + strings.ToUpper(expectedOutputUnspecified) + "\n", true},
		{"unspecified", true},
		{"Not specified.", true},
		{expectedOutputUnspecified + " Must also include a title.", false},
		{"Not specified: this component does\n  not declare what it returns, so the run is judged only on whether it completed and produced a result.", true},
		{"Not specified exactly, but must include X", false},
		{"Not specified: " + "something else", false},
		{"The summary and its translation", false},
		{map[string]any{"a": "b"}, false},
		{nil, false},
	}
	for _, tt := range tests {
		if got := isUnspecifiedExpectation(tt.expected); got != tt.want {
			t.Errorf("isUnspecifiedExpectation(%v) = %v, want %v", tt.expected, got, tt.want)
		}
	}
}
