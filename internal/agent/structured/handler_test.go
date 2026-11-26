// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package structured

import (
	"context"
	"fmt"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

type mockArtifactLoader struct {
	content map[string][]byte
	err     error
}

func (m *mockArtifactLoader) LoadContent(agentName, userID, sessionID, filename string, version *int) ([]byte, int, error) {
	if m.err != nil {
		return nil, 0, m.err
	}
	key := filename + ":latest"
	ver := 0
	if version != nil {
		key = fmt.Sprintf("%s:%d", filename, *version)
		ver = *version
	}
	data, ok := m.content[key]
	if !ok {
		return nil, 0, fmt.Errorf("artifact %q not found", key)
	}
	return data, ver, nil
}

func TestHandler_ExtractRequest_Found(t *testing.T) {
	h, _ := NewHandler(HandlerConfig{})
	msg := a2a.Message{
		Parts: []a2a.Part{
			a2a.NewTextPart("Hello"),
			a2a.NewDataPart(map[string]any{
				"type":          "structured_invocation_request",
				"workflow_name": "test_workflow",
				"node_id":       "node_1",
			}),
		},
	}

	req := h.ExtractRequest(msg)
	if req == nil {
		t.Fatal("expected request, got nil")
	}
	if req.WorkflowName != "test_workflow" {
		t.Errorf("workflow_name = %q, want %q", req.WorkflowName, "test_workflow")
	}
	if req.NodeID != "node_1" {
		t.Errorf("node_id = %q, want %q", req.NodeID, "node_1")
	}
}

func TestHandler_ExtractRequest_NotFound(t *testing.T) {
	h, _ := NewHandler(HandlerConfig{})
	msg := a2a.Message{
		Parts: []a2a.Part{
			a2a.NewTextPart("Just a normal message"),
		},
	}

	req := h.ExtractRequest(msg)
	if req != nil {
		t.Errorf("expected nil for normal message, got %+v", req)
	}
}

func TestHandler_BuildContext_RequestOverrides(t *testing.T) {
	h, _ := NewHandler(HandlerConfig{
		InputSchema:  map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}},
		OutputSchema: map[string]any{"type": "object", "properties": map[string]any{"result": map[string]any{"type": "string"}}},
	})

	reqSchema := map[string]any{"type": "object", "properties": map[string]any{"custom": map[string]any{"type": "integer"}}}
	req := &a2a.StructuredInvocationRequest{
		WorkflowName: "wf",
		NodeID:       "n1",
		InputSchema:  reqSchema,
	}

	ctx := h.BuildContext(req)
	if ctx.InputSchema["properties"].(map[string]any)["custom"] == nil {
		t.Error("expected request input schema to override config")
	}
	if ctx.OutputSchema["properties"].(map[string]any)["result"] == nil {
		t.Error("expected config output schema as fallback")
	}
}

func TestHandler_BuildContext_DefaultTextSchema(t *testing.T) {
	h, _ := NewHandler(HandlerConfig{})
	req := &a2a.StructuredInvocationRequest{
		WorkflowName: "wf",
		NodeID:       "n1",
	}

	ctx := h.BuildContext(req)
	props, ok := ctx.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties in default schema")
	}
	if props["text"] == nil {
		t.Error("expected 'text' property in default schema")
	}
}

func TestHandler_ValidateInput_ValidText(t *testing.T) {
	h, _ := NewHandler(HandlerConfig{})
	req := &a2a.StructuredInvocationRequest{WorkflowName: "wf", NodeID: "n1"}
	sc := h.BuildContext(req)

	msg := a2a.Message{
		Parts: []a2a.Part{a2a.NewTextPart("Hello world")},
	}

	errs := h.ValidateInput(msg, sc)
	if errs != nil {
		t.Errorf("expected valid, got errors: %v", errs)
	}
}

func TestHandler_ValidateInput_InvalidStructured(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"count": map[string]any{"type": "integer"},
		},
		"required": []any{"count"},
	}

	h, _ := NewHandler(HandlerConfig{InputSchema: schema})
	req := &a2a.StructuredInvocationRequest{WorkflowName: "wf", NodeID: "n1"}
	sc := h.BuildContext(req)

	msg := a2a.Message{
		Parts: []a2a.Part{a2a.NewTextPart("Hello")},
	}

	errs := h.ValidateInput(msg, sc)
	if errs == nil {
		t.Error("expected validation errors for non-file message with structured schema")
	}
}

func TestHandler_ParseAndValidateOutput_Success(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
		"required": []any{"name"},
	}

	h, _ := NewHandler(HandlerConfig{OutputSchema: schema})
	req := &a2a.StructuredInvocationRequest{WorkflowName: "wf", NodeID: "n1"}
	sc := h.BuildContext(req)

	loader := &mockArtifactLoader{
		content: map[string][]byte{
			"output.json:0":      []byte(`{"name": "Alice"}`),
			"output.json:latest": []byte(`{"name": "Alice"}`),
		},
	}

	llmOutput := `Here is the result. «result:artifact=output.json:0 status=success»`
	result, feedback, err := h.ParseAndValidateOutput(context.Background(), llmOutput, sc, loader, "agent", "user", "session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if feedback != nil {
		t.Fatalf("unexpected feedback: %v", feedback.Message)
	}
	if result.Status != "success" {
		t.Errorf("status = %q, want %q", result.Status, "success")
	}
	if result.OutputArtifactRef == nil {
		t.Fatal("expected artifact ref")
	}
	if result.OutputArtifactRef.Name != "output.json" {
		t.Errorf("artifact name = %q, want %q", result.OutputArtifactRef.Name, "output.json")
	}
}

func TestHandler_ParseAndValidateOutput_NoEmbed_RetryFeedback(t *testing.T) {
	h, _ := NewHandler(HandlerConfig{})
	req := &a2a.StructuredInvocationRequest{WorkflowName: "wf", NodeID: "n1"}
	sc := h.BuildContext(req)

	llmOutput := `I completed the task but forgot the embed.`
	result, feedback, err := h.ParseAndValidateOutput(context.Background(), llmOutput, sc, nil, "agent", "user", "session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil result (retry), got %+v", result)
	}
	if feedback == nil {
		t.Fatal("expected retry feedback")
	}
	if feedback.Message == "" {
		t.Error("expected non-empty feedback message")
	}
}

func TestHandler_ParseAndValidateOutput_NoEmbed_MaxRetries(t *testing.T) {
	h, _ := NewHandler(HandlerConfig{MaxRetries: 1})
	req := &a2a.StructuredInvocationRequest{WorkflowName: "wf", NodeID: "n1"}
	sc := h.BuildContext(req)
	sc.RetryCount = 1

	llmOutput := `Still no embed.`
	result, feedback, err := h.ParseAndValidateOutput(context.Background(), llmOutput, sc, nil, "agent", "user", "session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if feedback != nil {
		t.Fatalf("expected nil feedback (max retries), got feedback")
	}
	if result == nil {
		t.Fatal("expected error result")
	}
	if result.Status != "error" {
		t.Errorf("status = %q, want %q", result.Status, "error")
	}
}

func TestHandler_ParseAndValidateOutput_NoFeedbackOnceRetriesAreSpent(t *testing.T) {
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"count": map[string]any{"type": "integer"}},
		"required":   []any{"count"},
	}
	const withEmbed = `Done. «result:artifact=output.json status=success»`

	cases := []struct {
		name      string
		llmOutput string
		loader    ArtifactLoader
	}{
		{name: "missing result embed", llmOutput: "no embed here", loader: nil},
		{name: "artifact fails to load", llmOutput: withEmbed, loader: &mockArtifactLoader{err: fmt.Errorf("storage down")}},
		{name: "artifact is not JSON", llmOutput: withEmbed, loader: &mockArtifactLoader{content: map[string][]byte{
			"output.json:latest": []byte(`not json`),
		}}},
		{name: "artifact fails the schema", llmOutput: withEmbed, loader: &mockArtifactLoader{content: map[string][]byte{
			"output.json:latest": []byte(`{"count": "not-a-number"}`),
		}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := NewHandler(HandlerConfig{OutputSchema: schema, MaxRetries: 2})
			sc := h.BuildContext(&a2a.StructuredInvocationRequest{WorkflowName: "wf", NodeID: "n1"})

			sc.RetryCount = sc.MaxRetries - 1
			if _, feedback, _ := h.ParseAndValidateOutput(t.Context(), tc.llmOutput, sc, tc.loader, "agent", "user", "session"); feedback == nil {
				t.Fatal("want retry feedback while a retry remains")
			}

			sc.RetryCount = sc.MaxRetries
			result, feedback, err := h.ParseAndValidateOutput(t.Context(), tc.llmOutput, sc, tc.loader, "agent", "user", "session")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if feedback != nil {
				t.Fatalf("want no retry feedback once retries are spent, got %q", feedback.Message)
			}
			if result == nil || result.Status != "error" {
				t.Fatalf("result = %+v, want a terminal error result", result)
			}
		})
	}
}

func TestHandler_ParseAndValidateOutput_ValidationFailure_Retry(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"count": map[string]any{"type": "integer"},
		},
		"required": []any{"count"},
	}

	h, _ := NewHandler(HandlerConfig{OutputSchema: schema})
	req := &a2a.StructuredInvocationRequest{WorkflowName: "wf", NodeID: "n1"}
	sc := h.BuildContext(req)

	loader := &mockArtifactLoader{
		content: map[string][]byte{
			"output.json:latest": []byte(`{"count": "not-a-number"}`),
		},
	}

	llmOutput := `Done. «result:artifact=output.json status=success»`
	result, feedback, err := h.ParseAndValidateOutput(context.Background(), llmOutput, sc, loader, "agent", "user", "session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil result (retry), got %+v", result)
	}
	if feedback == nil {
		t.Fatal("expected retry feedback for validation failure")
	}
}

func TestHandler_ParseAndValidateOutput_NoSchema_SuccessWithoutValidation(t *testing.T) {
	h, _ := NewHandler(HandlerConfig{})
	req := &a2a.StructuredInvocationRequest{WorkflowName: "wf", NodeID: "n1"}
	sc := h.BuildContext(req)

	llmOutput := `Done. «result:artifact=output.txt status=success»`
	result, feedback, err := h.ParseAndValidateOutput(context.Background(), llmOutput, sc, nil, "agent", "user", "session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if feedback != nil {
		t.Fatalf("unexpected feedback: %v", feedback.Message)
	}
	if result.Status != "success" {
		t.Errorf("status = %q, want %q", result.Status, "success")
	}
}

func TestHandler_ParseAndValidateOutput_NoSchema_MissingArtifactRetries(t *testing.T) {
	h, _ := NewHandler(HandlerConfig{})
	sc := h.BuildContext(&a2a.StructuredInvocationRequest{WorkflowName: "wf", NodeID: "n1"})
	sc.MaxRetries = 1

	llmOutput := `Done. «result:artifact=never_saved.json status=success»`
	result, feedback, err := h.ParseAndValidateOutput(context.Background(), llmOutput, sc, &mockArtifactLoader{}, "agent", "user", "session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil || feedback == nil {
		t.Fatalf("a result naming an unsaved artifact must be retried, got result=%+v feedback=%v", result, feedback)
	}
}

func TestHandler_ParseAndValidateOutput_NoSchema_TextSucceeds(t *testing.T) {
	h, _ := NewHandler(HandlerConfig{})
	sc := h.BuildContext(&a2a.StructuredInvocationRequest{WorkflowName: "wf", NodeID: "n1"})
	sc.MaxRetries = 1
	loader := &mockArtifactLoader{content: map[string][]byte{"report.md:latest": []byte("# Status\n\nPO-1 is overdue")}}

	result, feedback, err := h.ParseAndValidateOutput(context.Background(), `«result:artifact=report.md status=success»`, sc, loader, "agent", "user", "session")
	if err != nil || feedback != nil || result == nil || result.Status != "success" {
		t.Fatalf("a text result must pass on as it is, got result=%+v feedback=%v err=%v", result, feedback, err)
	}
}

func TestHandler_ParseAndValidateOutput_ErrorStatus(t *testing.T) {
	h, _ := NewHandler(HandlerConfig{})
	req := &a2a.StructuredInvocationRequest{WorkflowName: "wf", NodeID: "n1"}
	sc := h.BuildContext(req)

	llmOutput := `Failed. «result:status=error message="cannot process"»`
	result, feedback, err := h.ParseAndValidateOutput(context.Background(), llmOutput, sc, nil, "agent", "user", "session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if feedback != nil {
		t.Fatalf("unexpected feedback: %v", feedback.Message)
	}
	if result.Status != "error" {
		t.Errorf("status = %q, want %q", result.Status, "error")
	}
	if want := taxonomy.AgentReportedErrorMessage + ": cannot process"; result.ErrorMessage != want {
		t.Errorf("error_message = %q, want %q", result.ErrorMessage, want)
	}
}
