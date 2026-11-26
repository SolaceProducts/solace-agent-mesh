// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package testharness

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func TestGenerateTestPlan_NothingDeclaredAndPlannerSaysUnspecified(t *testing.T) {
	t.Parallel()
	for _, answer := range []string{`"unspecified"`, `""`} {
		dir := t.TempDir()
		wire, _, _ := invokeGenerateTestPlanIn(t, dir, map[string]any{
			"componentName": "DocPipeline", "componentKind": "workflow", "description": bareWorkflowDescription,
		}, `{"sampleInput":{"document_text":"x"},"expectedOutput":`+answer+`}`)
		if got := planFromWire(t, wire)["expectedOutput"]; got != expectedOutputUnspecified {
			t.Errorf("answer %s: returned expectedOutput = %v", answer, got)
		}
		if got := persistedPlan(t, dir)["expectedOutput"]; got != expectedOutputUnspecified {
			t.Errorf("answer %s: persisted expectedOutput = %v", answer, got)
		}
		if msg, _ := wire["message"].(string); !strings.Contains(msg, "unspecified") {
			t.Errorf("answer %s: message = %q", answer, msg)
		}
	}
}

func TestGenerateTestPlan_PlannerGivingNoAnswerForADeclaredWorkflowGetsTheDeclaration(t *testing.T) {
	t.Parallel()
	wire, _, _ := invokeGenerateTestPlan(t, map[string]any{
		"componentName": "DocPipeline", "componentKind": "workflow", "description": docPipelineDescription,
	}, `{"sampleInput":{"document_text":"x"},"expectedOutput":"  "}`)
	if got := planFromWire(t, wire)["expectedOutput"]; got != docAsDeclared {
		t.Errorf("expectedOutput = %v, want %q", got, docAsDeclared)
	}
}

func TestGenerateRecommendations_PlanMadeBeforeTheWorkflowDeclaredAnything(t *testing.T) {
	t.Parallel()
	plan, _, _ := invokeGenerateTestPlan(t, map[string]any{
		"componentName": "DocPipeline", "componentKind": "workflow", "description": bareWorkflowDescription,
	}, `{"sampleInput":{"document_text":"x"},"expectedOutput":"unspecified"}`)
	planned := planFromWire(t, plan)["expectedOutput"]

	wire, prompt := invokeGenerateRecommendations(t,
		docRunArgs(planned, strPtr(docPipelineDescription)),
		`{"summary":"Both declared fields came back.","recommendations":[]}`)
	report := planFromWire(t, wire)
	if got := report["expectedOutput"]; got != docAsDeclared {
		t.Errorf("report expectedOutput = %v, want %q", got, docAsDeclared)
	}
	if summary, _ := report["summary"].(string); strings.Contains(summary, "No expected output is declared") {
		t.Errorf("summary = %q", summary)
	}
	if msg, _ := wire["message"].(string); strings.Contains(msg, "only completion was checked") {
		t.Errorf("a declared component was reported as only completion-checked: %q", msg)
	}
	if !strings.Contains(prompt, docAsDeclared) || strings.Contains(prompt, "declares nothing") {
		t.Errorf("reviewer prompt:\n%s", prompt)
	}
}

func TestGenerateRecommendations_ClearedExpectationOfAnUndeclaredComponentReadsAsUnspecified(t *testing.T) {
	t.Parallel()
	for _, cleared := range []string{"", "unspecified", "Not specified."} {
		wire, prompt := invokeGenerateRecommendations(t,
			docRunArgs(cleared, strPtr(bareWorkflowDescription)), `{"summary":"Completed.","recommendations":[]}`)
		if got := planFromWire(t, wire)["expectedOutput"]; got != expectedOutputUnspecified {
			t.Errorf("expectation %q: report expectedOutput = %v", cleared, got)
		}
		if !strings.Contains(prompt, expectedOutputUnspecified) {
			t.Errorf("expectation %q: reviewer prompt:\n%s", cleared, prompt)
		}
	}
}

func TestGenerateRecommendations_UnspecifiedRunWithNoRecommendationsDoesNotClaimItWasGraded(t *testing.T) {
	t.Parallel()
	wire, _ := invokeGenerateRecommendations(t,
		docRunArgs(expectedOutputUnspecified, strPtr(bareWorkflowDescription)),
		`{"summary":"Completed.","recommendations":[]}`)
	if msg, _ := wire["message"].(string); !strings.Contains(msg, "only completion was checked") || strings.Contains(msg, "matched the expected") {
		t.Errorf("message = %q", msg)
	}
}

func TestPromptsNameTheDeclaredOutputLabelThePublisherWrites(t *testing.T) {
	t.Parallel()
	label := strings.TrimSuffix(a2a.DeclaredOutputLabel, ":")
	for name, text := range map[string]string{
		"planner system prompt":  testPlanSystemPrompt,
		"reviewer system prompt": recommendationsSystemPrompt,
	} {
		if !strings.Contains(text, `"`+label) {
			t.Errorf("%s does not mention %q", name, label)
		}
	}
}
