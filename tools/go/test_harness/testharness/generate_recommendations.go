// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package testharness

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type GenerateRecommendationsParams struct {
	ComponentName  string  `json:"componentName" desc:"Name of the wrapped workflow or agent the recommendations are for."`
	ComponentKind  string  `json:"componentKind" desc:"One of 'workflow', 'agent', or 'skill'."`
	SampleInput    any     `json:"sampleInput" desc:"The input that was sent to the wrapped component (mirrors the test plan)."`
	ExpectedOutput any     `json:"expectedOutput" desc:"What the test plan said the component should produce."`
	ActualOutput   any     `json:"actualOutput" desc:"What the wrapped component actually produced when invoked."`
	UserNotes      *string `json:"userNotes,omitempty" desc:"Optional free-form user observations to weight the recommendations."`
	Description    *string `json:"description,omitempty" desc:"For a workflow or agent: the description of the tool that calls it, copied exactly as the tool declares it now, as you see it in your tool list. The run is judged against what it declares."`
}

type Recommendation struct {
	Area       string `json:"area"`
	Finding    string `json:"finding"`
	Suggestion string `json:"suggestion"`
	Priority   string `json:"priority"`
}

type RecommendationsResult struct {
	Summary         string           `json:"summary"`
	Recommendations []Recommendation `json:"recommendations"`
}

const generateRecommendationsInstructions = `## generate_recommendations

Call this tool *after* a test has actually run and you have the wrapped
component's actual output in hand. Pass the same sampleInput and
expectedOutput from the test plan, plus the actualOutput.

The tool returns a list of prioritized recommendations rendered as a
report card. Each recommendation must:
- name a specific configuration area (system prompt, tools, workflow logic,
  model choice),
- give a concrete change, not a vague directive,
- carry an honest priority — high for failures, medium for quality gaps,
  low for nice-to-haves.

Also pass description: the description of the tool that calls the workflow or
agent, copied exactly as it is declared now (as you see it in your tool list,
not from memory of the plan). The run is judged against what the component
declares.

Do not fabricate recommendations when the actual output matched expectation;
return an empty list and let the chat say "everything looks correct". When the
expected output is "Not specified", the component declares no output to hold
the run to: recommend nothing about its output.`

var validRecommendationAreas = map[string]bool{
	"system_prompt":  true,
	"tools":          true,
	"workflow_logic": true,
	"model_choice":   true,
	"other":          true,
}

var validRecommendationPriorities = map[string]bool{
	"high":   true,
	"medium": true,
	"low":    true,
}

const defaultRecommendationPriority = "medium"

func generateRecommendations(ctx context.Context, p GenerateRecommendationsParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	if strings.TrimSpace(p.ComponentName) == "" {
		return sdk.Error("componentName is required"), nil
	}
	kind := strings.ToLower(strings.TrimSpace(p.ComponentKind))
	if kind != "workflow" && kind != "agent" && kind != "skill" {
		return sdk.Error("componentKind must be 'workflow', 'agent', or 'skill'"), nil
	}
	if p.SampleInput == nil {
		return sdk.Error("sampleInput is required"), nil
	}
	if p.ExpectedOutput == nil {
		return sdk.Error("expectedOutput is required"), nil
	}
	if p.ActualOutput == nil {
		return sdk.Error("actualOutput is required"), nil
	}

	p.ExpectedOutput = effectiveExpectation(kind, p.Description, p.ExpectedOutput)
	userPrompt, err := buildRecommendationsUserPrompt(p, kind)
	if err != nil {
		return sdk.Error(fmt.Sprintf("build prompt: %v", err)), nil
	}
	_, expectationUnspecified := gradingFor(kind, p.Description, p.ExpectedOutput)

	response, err := tc.CallLLM(ctx, recommendationsSystemPrompt, userPrompt, 0.2)
	if err != nil {
		return sdk.Error(fmt.Sprintf("LLM call failed: %v", err)), nil
	}

	summary, cleaned, err := parseAndSanitizeRecommendations(response)
	if err != nil {
		return sdk.Error(fmt.Sprintf("could not parse recommendations from LLM response: %v", err)), nil
	}
	if expectationUnspecified {
		summary = strings.TrimSpace(unspecifiedSummaryLead + " " + summary)
	}

	report := TestReport{
		ComponentName:   p.ComponentName,
		ComponentKind:   kind,
		SampleInput:     p.SampleInput,
		ExpectedOutput:  p.ExpectedOutput,
		ActualOutput:    p.ActualOutput,
		Summary:         summary,
		Recommendations: cleaned,
		TaskID:          tc.TaskID,
	}
	reportBytes, err := json.MarshalIndent(report, "", "  ")
	if err == nil {
		if saveErr := tc.SaveArtifact(TestReportArtifactFilename, reportBytes, "application/json"); saveErr != nil {
			_ = tc.SendStatus(fmt.Sprintf("warning: could not save test report artifact: %v", saveErr))
		}
	}

	msg := fmt.Sprintf("%d recommendation(s) generated.", len(cleaned))
	if len(cleaned) == 0 {
		msg = "No issues found — the actual output matched the expected behaviour."
		if expectationUnspecified {
			msg = "Run completed; no expected output is declared, so only completion was checked."
		}
	}
	return sdk.OK(msg, sdk.WithData(structAsMap(report))), nil
}

const unspecifiedSummaryLead = "No expected output is declared for this component, so the output was not graded against one."

func parseAndSanitizeRecommendations(response string) (string, []Recommendation, error) {
	var raw RecommendationsResult
	if _, err := extractJSON(response, &raw); err != nil {
		return "", nil, err
	}
	summary := strings.TrimSpace(raw.Summary)
	cleaned := make([]Recommendation, 0, len(raw.Recommendations))
	for _, r := range raw.Recommendations {
		finding := strings.TrimSpace(r.Finding)
		suggestion := strings.TrimSpace(r.Suggestion)
		if finding == "" || suggestion == "" {
			continue
		}
		area := strings.ToLower(strings.TrimSpace(r.Area))
		if !validRecommendationAreas[area] {
			area = "other"
		}
		priority := strings.ToLower(strings.TrimSpace(r.Priority))
		if !validRecommendationPriorities[priority] {
			priority = defaultRecommendationPriority
		}
		cleaned = append(cleaned, Recommendation{
			Area:       area,
			Finding:    finding,
			Suggestion: suggestion,
			Priority:   priority,
		})
	}
	return summary, cleaned, nil
}

type TestReport struct {
	ComponentName   string           `json:"componentName"`
	ComponentKind   string           `json:"componentKind"`
	SampleInput     any              `json:"sampleInput"`
	ExpectedOutput  any              `json:"expectedOutput"`
	ActualOutput    any              `json:"actualOutput"`
	Summary         string           `json:"summary,omitempty"`
	Recommendations []Recommendation `json:"recommendations"`
	TaskID          string           `json:"taskId,omitempty"`
}

const TestReportArtifactFilename = "test_report.json"

const recommendationsSystemPrompt = `You are a test reviewer for the Solace Agent Mesh AI Builder.
A test was just run against a workflow, agent, or skill. Compare the expected
output to the actual output and produce a list of concrete, prioritized
changes the build agent could apply. For skill kind, recommendations should
target the skill's SKILL.md instructions, bundled-tool wiring, or referenced
resources — not the test agent's wrapper prompt.

Output ONLY a JSON object — no prose, no markdown fences. The shape MUST be:

{
  "summary": "<one plain sentence on how the test went and whether the component met expectations>",
  "recommendations": [
    {
      "area": "system_prompt" | "tools" | "workflow_logic" | "model_choice" | "other",
      "finding": "<what's wrong or could be better — specific to this test>",
      "suggestion": "<concrete change the build agent could implement>",
      "priority": "high" | "medium" | "low"
    }
  ]
}

Rules:
- "summary" is always exactly one sentence in plain text (no markdown, no
  lists). State how successful the run was and the headline reason — e.g.
  "The agent answered correctly but omitted the source citation the prompt
  asked for." Keep it under ~30 words. Always provide it, even when the
  recommendations array is empty.
- "high" priority means the test failed (actual didn't meet expectation in
  a way the user would notice). "medium" is a quality gap. "low" is a
  nice-to-have.
- Every recommendation must reference the actual test data — don't make
  generic suggestions that would apply to any agent.
- Grade on semantic CONTENT and whether the right tools fired, NOT on
  presentation or serialization. Markdown, plain text, and JSON are
  interchangeable: when the expected output doesn't explicitly mandate a wire
  format, do NOT raise a recommendation (and never "high") solely because the
  actual output's format or structure differs from how the expected output
  happened to be written. Treat a format difference as a real finding only
  when the component is explicitly required to produce a specific format (e.g.
  a typed output schema) and the actual output violates it. A chat agent has
  no such requirement — for an "agent" kind, never treat the reply's format or
  serialization as contractual, even if the expected output was written as a
  JSON object.
- The component's declaration, when the request gives one, is the authority
  on what it should return: its own description and, for a workflow, its
  "Declared output fields". The expected output is only a paraphrase of it.
  When the actual output contains the declared output fields and its content
  fits the description, the run satisfied the contract: return an empty
  recommendations array. An output that differs from the expected output in
  field names, wording, structure or detail the contract does not mention is
  NOT a finding. Neither is a quality of the content the contract does not
  state: its length, brevity, tone, style or how much it condenses the input.
  Only content that is empty, in the wrong language, off the declared
  purpose, or contradicts it is a finding.
- Never recommend rewriting, replacing, renaming, adding or removing a step,
  node, output field, system-prompt rule or tool of a component whose actual
  output satisfies its declared contract. Never recommend a change that makes
  the component produce something its declared contract does not name.
- When the expected output says "Not specified", the component declares no
  output to hold the run to (and the request says it declares nothing). Judge only whether the run completed and
  produced a non-empty result that responds to the sample input. Do NOT
  compare it with any expected shape, and make no recommendation about the
  component's output fields, structure, steps or prompt. The only possible
  findings are a run that failed, errored or returned nothing.
- If the actual output already matches the expected behavior, return an
  empty recommendations array. Don't pad with low-confidence findings.
- The build agent reads these recommendations, so write each "suggestion"
  as something it could act on directly (e.g. "Add a system-prompt rule:
  ..." not "Improve the prompt").`

func buildRecommendationsUserPrompt(p GenerateRecommendationsParams, kind string) (string, error) {
	sample, err := jsonStringify(p.SampleInput)
	if err != nil {
		return "", fmt.Errorf("sampleInput: %w", err)
	}
	expected, err := jsonStringify(p.ExpectedOutput)
	if err != nil {
		return "", fmt.Errorf("expectedOutput: %w", err)
	}
	declared, unspecified := gradingFor(kind, p.Description, p.ExpectedOutput)
	actual, err := jsonStringify(p.ActualOutput)
	if err != nil {
		return "", fmt.Errorf("actualOutput: %w", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "A test was just run against the %s named %q.\n\n", kind, p.ComponentName)
	fmt.Fprintf(&b, "Sample input:\n%s\n\n", sample)
	fmt.Fprintf(&b, "Expected output:\n%s\n\n", expected)
	fmt.Fprintf(&b, "Actual output:\n%s\n", actual)
	if contractApplies(kind) {
		writeDeclared(&b, kind, declared, unspecified)
	}
	if p.UserNotes != nil {
		if notes := strings.TrimSpace(*p.UserNotes); notes != "" {
			fmt.Fprintf(&b, "\nUser observations:\n%s\n", notes)
		}
	}
	b.WriteString("\nReturn the JSON object described in the system prompt and nothing else.")
	return b.String(), nil
}

func jsonStringify(v any) (string, error) {
	if s, ok := v.(string); ok {
		return s, nil
	}
	bytes, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}
