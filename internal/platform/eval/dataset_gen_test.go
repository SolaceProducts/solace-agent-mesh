// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type fakeGatewayClient struct {
	taskID    string
	result    map[string]any
	submitErr error
	pollErr   error

	gotURL    string
	gotAgent  string
	gotPrompt string
	gotAuth   string
	gotWait   time.Duration
	calls     int
}

func (f *fakeGatewayClient) SubmitTask(_ context.Context, gatewayURL, agentName, prompt, auth string) (string, error) {
	f.calls++
	f.gotURL, f.gotAgent, f.gotPrompt, f.gotAuth = gatewayURL, agentName, prompt, auth
	if f.submitErr != nil {
		return "", f.submitErr
	}
	return f.taskID, nil
}

func (f *fakeGatewayClient) PollTask(_ context.Context, _, taskID, _ string, wait time.Duration) (map[string]any, error) {
	f.gotWait = wait
	if f.pollErr != nil {
		return nil, f.pollErr
	}
	if taskID != f.taskID {
		return nil, nil //nolint:nilnil
	}
	return f.result, nil
}

type stubRegistry struct{ card *a2a.AgentCard }

func (f *stubRegistry) Get(string) *a2a.AgentCard { return f.card }

func stubResponseFromText(body string) map[string]any {
	return map[string]any{
		"status": map[string]any{
			"message": map[string]any{
				"parts": []any{
					map[string]any{"kind": "text", "text": body},
				},
			},
		},
	}
}

func newDatasetSvcWithFake(t *testing.T, fake *fakeGatewayClient) (context.Context, *evalSvcSet) {
	t.Helper()
	ctx, set := newEvalSvcs(t)
	set.dataset = set.dataset.WithAIGen(AIGenConfig{
		Client:        fake,
		AgentRegistry: &stubRegistry{card: &a2a.AgentCard{Name: "AgentX", Description: "test agent"}},
		Orchestrator:  "Orchestrator",
		Timeout:       5 * time.Second,
	})
	return ctx, set
}

func TestExtractResponseText_TextPart(t *testing.T) {
	got := ExtractResponseText(stubResponseFromText("hello world"))
	if got != "hello world" {
		t.Fatalf("got %q", got)
	}
}

func TestExtractResponseText_RootEnvelope(t *testing.T) {
	got := ExtractResponseText(map[string]any{
		"status": map[string]any{
			"message": map[string]any{
				"parts": []any{
					map[string]any{"root": map[string]any{"kind": "text", "text": "via root"}},
				},
			},
		},
	})
	if got != "via root" {
		t.Fatalf("got %q", got)
	}
}

func TestExtractResponseText_TypeKeyAndJoin(t *testing.T) {
	got := ExtractResponseText(map[string]any{
		"status": map[string]any{
			"message": map[string]any{
				"parts": []any{
					map[string]any{"type": "text", "text": "line1"},
					map[string]any{"kind": "text", "text": "line2"},
					map[string]any{"kind": "data", "text": "ignored"},
				},
			},
		},
	})
	if got != "line1\nline2" {
		t.Fatalf("got %q", got)
	}
}

func TestExtractResponseText_EmptyOrMalformed(t *testing.T) {
	cases := []map[string]any{
		nil,
		{},
		{"status": nil},
		{"status": map[string]any{"message": nil}},
		{"status": map[string]any{"message": map[string]any{}}},
	}
	for _, tc := range cases {
		if got := ExtractResponseText(tc); got != "" {
			t.Fatalf("expected empty for %v, got %q", tc, got)
		}
	}
}

func TestExtractJSONBlock_StripsFences(t *testing.T) {
	in := "```json\n{\"a\": 1}\n```"
	got, err := extractJSONBlock(in, false)
	if err != nil || got == nil {
		t.Fatalf("extractJSONBlock err=%v got=%v", err, got)
	}
	m, _ := got.(map[string]any)
	if v, _ := m["a"].(float64); v != 1 {
		t.Fatalf("got %v", got)
	}
}

func TestExtractJSONBlock_ToleratesPythonLiteralsAndComments(t *testing.T) {
	in := `prefix text {"x": None, "y": True, "z": False, // comment
		"arr": [1, 2,],
	}`
	got, err := extractJSONBlock(in, false)
	if err != nil || got == nil {
		t.Fatalf("got=%v err=%v", got, err)
	}
	m := got.(map[string]any)
	if m["x"] != nil {
		t.Fatalf("x: %v", m["x"])
	}
	if m["y"] != true {
		t.Fatalf("y: %v", m["y"])
	}
	if m["z"] != false {
		t.Fatalf("z: %v", m["z"])
	}
}

func TestExtractJSONBlock_ArrayOnly(t *testing.T) {
	in := "garbage [1, 2, 3] trailing"
	got, err := extractJSONBlock(in, true)
	if err != nil || got == nil {
		t.Fatalf("got=%v err=%v", got, err)
	}
	if _, ok := got.([]any); !ok {
		t.Fatalf("expected array, got %T", got)
	}
}

func TestExtractJSONBlock_PrefersEarlierObjectVsArray(t *testing.T) {
	in := "before {\"a\":1} then [1,2]"
	got, _ := extractJSONBlock(in, true)
	if _, ok := got.(map[string]any); !ok {
		t.Fatalf("expected object, got %T", got)
	}
}

func TestExtractJSONBlock_UrlInStringNotStripped(t *testing.T) {
	in := `{"link": "https://example.com/path"}`
	got, err := extractJSONBlock(in, false)
	if err != nil || got == nil {
		t.Fatalf("err=%v got=%v", err, got)
	}
	m := got.(map[string]any)
	if m["link"] != "https://example.com/path" {
		t.Fatalf("got %v", m["link"])
	}
}

func TestParseGeneratedDataset_Happy(t *testing.T) {
	in := `{"name":"Mesh Eval","description":"desc","examples":[{"prompt":"hi","expected_response":"hello"},{"prompt":"q2"}]}`
	got, err := parseGeneratedDataset(in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Mesh Eval" || got.Description != "desc" || len(got.Examples) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got.Examples[0].ExpectedResponse == nil || *got.Examples[0].ExpectedResponse != "hello" {
		t.Fatalf("expected_response missing on row 0: %+v", got.Examples[0])
	}
	if got.Examples[1].ExpectedResponse != nil {
		t.Fatalf("row 1 should have nil expected_response: %+v", got.Examples[1])
	}
}

func TestParseGeneratedDataset_TruncationIsRuneSafe(t *testing.T) {
	name := strings.Repeat("a", 254) + "é"
	desc := strings.Repeat("a", 999) + "é"
	in := `{"name":"` + name + `","description":"` + desc + `","examples":[{"prompt":"x"}]}`
	got, err := parseGeneratedDataset(in)
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(got.Name) {
		t.Errorf("name contains invalid UTF-8: %q", got.Name)
	}
	if !utf8.ValidString(got.Description) {
		t.Errorf("description contains invalid UTF-8: %q", got.Description)
	}
}

func TestParseGeneratedDataset_FallbackName(t *testing.T) {
	got, err := parseGeneratedDataset(`{"name":"","examples":[{"prompt":"x"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.Name, "Untitled Generated Dataset (") || !strings.HasSuffix(got.Name, ")") {
		t.Fatalf("expected fallback name, got %q", got.Name)
	}
}

func TestParseGeneratedDataset_MalformedReturnsBRV(t *testing.T) {
	cases := map[string]string{
		"not json":         "no json here",
		"non-object":       "[1,2,3]",
		"missing examples": `{"name":"x"}`,
		"empty examples":   `{"name":"x","examples":[]}`,
		"bad row types":    `{"name":"x","examples":["string","not-object"]}`,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parseGeneratedDataset(in)
			if err == nil {
				t.Fatal("expected error")
			}
			var brv *BusinessRuleViolationError
			if !errors.As(err, &brv) {
				t.Fatalf("expected BRV, got %T: %v", err, err)
			}
			if brv.Code != "DATASET_GEN_INVALID_RESPONSE" {
				t.Fatalf("code=%q", brv.Code)
			}
		})
	}
}

func TestParseGeneratedExamples_ObjectAndArrayShapes(t *testing.T) {
	wrapped := `{"examples":[{"prompt":"a"}]}`
	bare := `[{"prompt":"a"}]`
	for _, in := range []string{wrapped, bare} {
		got, err := parseGeneratedExamples(in)
		if err != nil {
			t.Fatalf("input=%q err=%v", in, err)
		}
		if len(got) != 1 || got[0].Prompt != "a" {
			t.Fatalf("got %+v", got)
		}
	}
}

func TestParseGeneratedExamples_RejectsEmpty(t *testing.T) {
	for _, in := range []string{`{"examples":[]}`, `[]`, `[{"prompt":""}]`} {
		_, err := parseGeneratedExamples(in)
		if err == nil {
			t.Fatalf("input=%q: expected BRV", in)
		}
	}
}

func TestBuildCreatePrompt_ContainsKeyClauses(t *testing.T) {
	out := buildCreatePrompt(
		"AgentX",
		agentContext{Description: "d", Instructions: "sys", Capabilities: "Toolsets: t"},
		3,
		"focus me",
	)
	required := []string{
		"target agent 'AgentX'",
		"- Name: AgentX",
		"- Description: d",
		"- Instructions / system prompt: sys",
		"- Capabilities: Toolsets: t",
		"Generate EXACTLY 3",
		"USER INSTRUCTIONS (MUST FOLLOW):",
		`"""focus me"""`,
		`{"name": "...", "description": "..."`,
	}
	for _, want := range required {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in prompt", want)
		}
	}
}

func TestBuildAppendPrompt_IncludesExisting(t *testing.T) {
	exVal := "expected"
	existing := []*store.EvalExample{
		{Prompt: "first prompt", ExpectedResponse: &exVal},
		{Prompt: "second prompt"},
	}
	out := buildAppendPrompt("AgentX", agentContext{}, 2, existing, "")
	for _, want := range []string{
		"EXISTING EXAMPLES",
		"- PROMPT: first prompt",
		"  EXPECTED: expected",
		"- PROMPT: second prompt",
		`{"examples": [{"prompt"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestGenerateDataset_HappyPath(t *testing.T) {
	body := `{"name":"My DS","description":"d","examples":[{"prompt":"q1","expected_response":"r1"},{"prompt":"q2"}]}`
	fake := &fakeGatewayClient{
		taskID: "task-1",
		result: stubResponseFromText(body),
	}
	ctx, set := newDatasetSvcWithFake(t, fake)

	resp, err := set.dataset.GenerateDataset(
		ctx,
		GenerateDatasetRequest{TargetAgent: "AgentX", NumExamples: 2},
		"alice",
		"http://gw.example",
		"Bearer abc",
	)
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil {
		t.Fatal("resp nil")
	}
	if resp.Name != "My DS" || resp.ExampleCount != 2 {
		t.Fatalf("got name=%q examples=%d", resp.Name, resp.ExampleCount)
	}
	if fake.gotURL != "http://gw.example" {
		t.Fatalf("gateway URL: %q", fake.gotURL)
	}
	if fake.gotAgent != "Orchestrator" {
		t.Fatalf("orchestrator: %q", fake.gotAgent)
	}
	if fake.gotAuth != "Bearer abc" {
		t.Fatalf("auth: %q", fake.gotAuth)
	}
	if !strings.Contains(fake.gotPrompt, "AgentX") {
		t.Fatalf("prompt missing target agent: %q", fake.gotPrompt[:200])
	}
}

func TestGenerateDataset_TruncatesOvershoot(t *testing.T) {
	body := `{"name":"DS","examples":[{"prompt":"a"},{"prompt":"b"},{"prompt":"c"},{"prompt":"d"}]}`
	fake := &fakeGatewayClient{taskID: "t", result: stubResponseFromText(body)}
	ctx, set := newDatasetSvcWithFake(t, fake)

	resp, err := set.dataset.GenerateDataset(
		ctx,
		GenerateDatasetRequest{TargetAgent: "AgentX", NumExamples: 2},
		"alice", "http://gw", "",
	)
	if err != nil {
		t.Fatal(err)
	}
	if resp.ExampleCount != 2 {
		t.Fatalf("expected 2 examples after truncation, got %d", resp.ExampleCount)
	}
}

func TestGenerateDataset_UniqueNameSuffix(t *testing.T) {
	body := `{"name":"Dup","examples":[{"prompt":"q"}]}`
	fake := &fakeGatewayClient{taskID: "t", result: stubResponseFromText(body)}
	ctx, set := newDatasetSvcWithFake(t, fake)

	mustCreateDataset(ctx, t, set.dataset, "Dup")
	resp, err := set.dataset.GenerateDataset(
		ctx,
		GenerateDatasetRequest{TargetAgent: "AgentX", NumExamples: 1},
		"alice", "http://gw", "",
	)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Name == "Dup" {
		t.Fatalf("name not disambiguated: %q", resp.Name)
	}
	if !strings.HasPrefix(resp.Name, "Dup (") {
		t.Fatalf("expected dated suffix, got %q", resp.Name)
	}
}

func TestGenerateDataset_SubmitFailureBRV(t *testing.T) {
	fake := &fakeGatewayClient{}
	ctx, set := newDatasetSvcWithFake(t, fake)

	_, err := set.dataset.GenerateDataset(
		ctx,
		GenerateDatasetRequest{TargetAgent: "AgentX", NumExamples: 1},
		"alice", "http://gw", "",
	)
	brv := asBusinessRuleErr(t, err)
	if brv.Code != "DATASET_GEN_FAILED" {
		t.Fatalf("code=%q msg=%q", brv.Code, brv.Message)
	}
}

func TestGenerateDataset_PollTimeoutBRV(t *testing.T) {
	fake := &fakeGatewayClient{taskID: "t"}
	ctx, set := newDatasetSvcWithFake(t, fake)

	_, err := set.dataset.GenerateDataset(
		ctx,
		GenerateDatasetRequest{TargetAgent: "AgentX", NumExamples: 1},
		"alice", "http://gw", "",
	)
	brv := asBusinessRuleErr(t, err)
	if !strings.Contains(brv.Message, "timeout") {
		t.Fatalf("expected timeout msg, got %q", brv.Message)
	}
}

func TestGenerateDataset_AIGenDisabledIsFeatureDisabled(t *testing.T) {
	ctx, set := newEvalSvcs(t)
	_, err := set.dataset.GenerateDataset(
		ctx,
		GenerateDatasetRequest{TargetAgent: "AgentX", NumExamples: 1},
		"alice", "http://gw", "",
	)
	var fd *FeatureDisabledError
	if !errors.As(err, &fd) {
		t.Fatalf("expected FeatureDisabledError, got %T: %v", err, err)
	}
}

func TestGenerateDataset_ValidationErrors(t *testing.T) {
	fake := &fakeGatewayClient{}
	ctx, set := newDatasetSvcWithFake(t, fake)

	cases := []GenerateDatasetRequest{
		{TargetAgent: "", NumExamples: 1},
		{TargetAgent: "AgentX", NumExamples: 0},
		{TargetAgent: "AgentX", NumExamples: MaxExamplesPerDataset + 1},
	}
	for _, req := range cases {
		_, err := set.dataset.GenerateDataset(ctx, req, "alice", "http://gw", "")
		if err == nil {
			t.Fatalf("expected error for %+v", req)
		}
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("expected ValidationError for %+v, got %T", req, err)
		}
	}
	if fake.calls != 0 {
		t.Fatalf("validation error should short-circuit before SubmitTask, calls=%d", fake.calls)
	}
}

func TestGenerateExamples_HappyPath(t *testing.T) {
	body := `{"examples":[{"prompt":"q3"},{"prompt":"q4","expected_response":"r4"}]}`
	fake := &fakeGatewayClient{taskID: "t", result: stubResponseFromText(body)}
	ctx, set := newDatasetSvcWithFake(t, fake)

	dsID := mustCreateDataset(ctx, t, set.dataset, "DS-existing")
	if _, err := set.example.Create(ctx, dsID, CreateExampleRequest{Prompt: "q1"}); err != nil {
		t.Fatal(err)
	}

	rows, err := set.dataset.GenerateExamples(
		ctx, dsID,
		GenerateExamplesRequest{TargetAgent: "AgentX", NumExamples: 2},
		"alice", "http://gw", "",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d examples", len(rows))
	}
	if rows[0].SequenceNumber != 2 || rows[1].SequenceNumber != 3 {
		t.Fatalf("expected seq 2/3, got %d/%d", rows[0].SequenceNumber, rows[1].SequenceNumber)
	}

	all, _ := set.example.List(ctx, dsID)
	if len(all) != 3 {
		t.Fatalf("expected 3 total examples, got %d", len(all))
	}
}

func TestGenerateExamples_RejectsAtCapBeforeOrchestratorCall(t *testing.T) {
	fake := &fakeGatewayClient{}
	ctx, set := newDatasetSvcWithFake(t, fake)
	const cap = 10
	withMaxExamples(t, set, cap)

	dsID := mustCreateDataset(ctx, t, set.dataset, "DS-cap")
	for i := range cap - 1 {
		if _, err := set.example.Create(ctx, dsID, CreateExampleRequest{Prompt: stringRepeat("x", 4)}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	_, err := set.dataset.GenerateExamples(
		ctx, dsID,
		GenerateExamplesRequest{TargetAgent: "AgentX", NumExamples: 5},
		"alice", "http://gw", "",
	)
	brv := asBusinessRuleErr(t, err)
	if brv.Code != "DATASET_EXAMPLE_CAP_REACHED" {
		t.Fatalf("code=%q", brv.Code)
	}
	if fake.calls != 0 {
		t.Fatalf("orchestrator should not have been called when over cap")
	}
}

func stringRepeat(s string, n int) string { return strings.Repeat(s, n) }

func TestSplitSSEBlocks(t *testing.T) {
	tail, blocks := splitSSEBlocks("a\n\nb\n\nc")
	if tail != "c" || len(blocks) != 2 || blocks[0] != "a" || blocks[1] != "b" {
		t.Fatalf("tail=%q blocks=%v", tail, blocks)
	}
}

func TestParseSSEBlock_DataLineConcatenation(t *testing.T) {
	got := parseSSEBlock("data: {\"a\":1}\ndata: ", discardLogger())
	if got == nil || got["a"] == nil {
		t.Fatalf("got %v", got)
	}
}
