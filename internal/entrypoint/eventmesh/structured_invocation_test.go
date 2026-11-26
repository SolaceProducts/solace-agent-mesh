// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/structured"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/sacexpr"
)

var (
	decisionOutputSchema = map[string]any{
		"type":       "object",
		"properties": map[string]any{"action": map[string]any{"type": "string"}},
		"required":   []any{"action"},
	}
	orderInputSchema = map[string]any{
		"type":       "object",
		"properties": map[string]any{"orderId": map[string]any{"type": "string"}},
		"required":   []any{"orderId"},
	}
	textInputSchema = map[string]any{
		"type":       "object",
		"properties": map[string]any{"text": map[string]any{"type": "string"}},
	}
)

const structuredOutputTopic = "decisions/out"

func structuredOutputHandlers() map[string]*OutputHandlerConfig {
	outputs := successAndErrorOutputs()
	outputs["ok_out"].TopicExpression = "static:" + structuredOutputTopic
	outputs["ok_out"].PayloadExpression = "task_response:structured_output"
	return outputs
}

func agentStructuredRequest(t *testing.T, msg a2a.Message, agentInputSchema map[string]any) (*structured.Handler, *structured.Context) {
	t.Helper()
	h, err := structured.NewHandler(structured.HandlerConfig{InputSchema: agentInputSchema, Logger: slog.Default()})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	req := h.ExtractRequest(msg)
	if req == nil {
		t.Fatalf("agent found no structured invocation request in parts %+v", msg.Parts)
	}
	sc := h.BuildContext(req)
	return h, sc
}

func TestStructuredInvocationFor(t *testing.T) {
	withSchema := &StructuredInvocationConfig{OutputSchema: decisionOutputSchema}
	inputOnly := &StructuredInvocationConfig{InputSchema: orderInputSchema}
	cases := []struct {
		name    string
		handler *EventHandlerConfig
		want    *StructuredInvocationConfig
	}{
		{"agent with output schema", &EventHandlerConfig{TargetAgentName: "decider", StructuredInvocation: withSchema}, withSchema},
		{"agent with input schema only", &EventHandlerConfig{TargetAgentName: "decider", StructuredInvocation: inputOnly}, inputOnly},
		{"agent by expression with schema", &EventHandlerConfig{TargetAgentNameExpression: "input.payload:agent", StructuredInvocation: withSchema}, withSchema},
		{"agent without block", &EventHandlerConfig{TargetAgentName: "decider"}, nil},
		{"agent with empty block", &EventHandlerConfig{TargetAgentName: "decider", StructuredInvocation: &StructuredInvocationConfig{}}, nil},
		{"workflow with schema", &EventHandlerConfig{TargetWorkflowName: "ClassifyWorkflow", StructuredInvocation: withSchema}, nil},
		{"workflow by expression with schema", &EventHandlerConfig{TargetWorkflowNameExpr: "input.payload:workflow", StructuredInvocation: withSchema}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := structuredInvocationFor(tc.handler); got != tc.want {
				t.Errorf("structuredInvocationFor = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestStructuredInvocationMessage_OutputSchemaOnly_AgentReadsTextInput(t *testing.T) {
	si := &StructuredInvocationConfig{OutputSchema: decisionOutputSchema}
	msg := structuredInvocationMessage(si, "em-gw", "decide", "task-1", "approve order 42?", "approve order 42?", nil)

	h, sc := agentStructuredRequest(t, msg, nil)
	if sc.Request.WorkflowName != "em-gw" || sc.Request.NodeID != "decide" {
		t.Errorf("request caller = (%q, %q), want (em-gw, decide)", sc.Request.WorkflowName, sc.Request.NodeID)
	}
	if sc.Request.SuggestedOutputFilename != "em-gw_decide_task-1.json" {
		t.Errorf("suggested_output_filename = %q", sc.Request.SuggestedOutputFilename)
	}
	if !reflect.DeepEqual(sc.OutputSchema, decisionOutputSchema) {
		t.Errorf("output schema = %v, want the rule's", sc.OutputSchema)
	}
	if errs := h.ValidateInput(msg, sc); errs != nil {
		t.Errorf("agent rejected text input: %v", errs)
	}
	if data := msg.Parts[0].Data.(map[string]any); data["input_data"] != nil {
		t.Errorf("request carries input_data %v, which the agent never reads", data["input_data"])
	}
}

func TestStructuredInvocationMessage_TextInputSchema_SendsNoFilePart(t *testing.T) {
	si := &StructuredInvocationConfig{InputSchema: textInputSchema}
	msg := structuredInvocationMessage(si, "em-gw", "decide", "task-1", "approve order 42?", "approve order 42?", nil)

	for _, p := range msg.Parts {
		if p.Kind == "file" {
			t.Errorf("unexpected file part %+v for a single-text input schema", p.File)
		}
	}
}

func TestStructuredInvocationMessage_OutputSchemaOnly_AgentOwnInputSchemaValidatesPayload(t *testing.T) {
	si := &StructuredInvocationConfig{OutputSchema: decisionOutputSchema}
	msg := structuredInvocationMessage(si, "em-gw", "decide", "task-1", "", map[string]any{"orderId": "42"}, nil)

	h, sc := agentStructuredRequest(t, msg, orderInputSchema)
	if errs := h.ValidateInput(msg, sc); errs != nil {
		t.Errorf("agent with its own input schema rejected the payload: %v", errs)
	}
}

func TestStructuredInvocationMessage_ObjectInputSchema_InputTravelsAsFirstFilePart(t *testing.T) {
	si := &StructuredInvocationConfig{InputSchema: orderInputSchema, OutputSchema: decisionOutputSchema}
	artifactPart := a2a.NewFilePart("artifact://app/u/s/attachment.pdf", "attachment.pdf", "application/pdf")

	for _, tc := range []struct {
		name  string
		input any
		valid bool
	}{
		{"object value", map[string]any{"orderId": "42"}, true},
		{"JSON string", `{"orderId":"42"}`, true},
		{"object missing a required field", map[string]any{"other": 1}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := structuredInvocationMessage(si, "em-gw", "decide", "task-1", "", tc.input, []a2a.Part{artifactPart})

			h, sc := agentStructuredRequest(t, msg, nil)
			errs := h.ValidateInput(msg, sc)
			if tc.valid && errs != nil {
				t.Errorf("agent rejected valid input: %v", errs)
			}
			if !tc.valid && errs == nil {
				t.Error("agent accepted input that violates the input schema")
			}
			if last := msg.Parts[len(msg.Parts)-1]; last.File == nil || last.File.URI != artifactPart.File.URI {
				t.Errorf("artifact file part not kept after the input: %+v", msg.Parts)
			}
		})
	}
}

func submitAndCapture(t *testing.T, adapter *EventMeshAdapter, brk broker.Broker, handler *EventHandlerConfig, target string, payload map[string]any) (a2a.Message, string) {
	t.Helper()
	msg, taskID, _ := submitAndCaptureWithProps(t, adapter, brk, handler, target, payload)
	return msg, taskID
}

func submitAndCaptureWithProps(t *testing.T, adapter *EventMeshAdapter, brk broker.Broker, handler *EventHandlerConfig, target string, payload map[string]any) (a2a.Message, string, a2a.UserProperties) {
	t.Helper()

	queue, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err = brk.Subscribe(queue, a2a.AgentRequestTopic("test", target)); err != nil {
		t.Fatalf("subscribe topic: %v", err)
	}

	body, _ := json.Marshal(payload)
	adapter.processIncomingMessage(t.Context(), handler, &broker.Message{
		Topic:          "in/orders",
		Payload:        body,
		UserProperties: map[string]string{"user_id": "alice"},
	})

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	recvMsg, err := brk.Receive(ctx, queue)
	if err != nil || recvMsg == nil {
		t.Fatalf("receive: err=%v msg=%v", err, recvMsg)
	}
	var req struct {
		ID     string `json:"id"`
		Params struct {
			Message a2a.Message `json:"message"`
		} `json:"params"`
	}
	if err := json.Unmarshal(recvMsg.Payload, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	return req.Params.Message, req.ID, a2a.DecodeUserProperties(recvMsg.UserProperties)
}

func TestEventMeshSubmitsWithTheEventTrigger(t *testing.T) {
	plain := func(*EventHandlerConfig) {}
	structured := func(h *EventHandlerConfig) {
		h.StructuredInvocation = &StructuredInvocationConfig{OutputSchema: decisionOutputSchema}
	}
	for name, configure := range map[string]func(*EventHandlerConfig){"plain": plain, "structured": structured} {
		t.Run(name, func(t *testing.T) {
			adapter, brk, _ := setupAdapterForAuth(t, defaultAuthAdapterOpts())
			handler := adapter.cfg.EventHandlers[0]
			configure(handler)

			_, _, props := submitAndCaptureWithProps(t, adapter, brk, handler, "TestAgent", map[string]any{"msg": "hi"})

			if props.Trigger != a2a.TriggerEvent {
				t.Errorf("trigger = %q, want %q", props.Trigger, a2a.TriggerEvent)
			}
		})
	}
}

func TestTransformMessage_AgentTargetWithSchema_SubmitsStructuredRequest(t *testing.T) {
	adapter, brk, _ := setupAdapterForAuth(t, defaultAuthAdapterOpts())
	handler := adapter.cfg.EventHandlers[0]
	handler.StructuredInvocation = &StructuredInvocationConfig{OutputSchema: decisionOutputSchema}

	msg, taskID := submitAndCapture(t, adapter, brk, handler, "TestAgent", map[string]any{"msg": "approve order 42?"})
	if msg.ContextID == "" {
		t.Error("submitted message has no ContextID")
	}
	h, sc := agentStructuredRequest(t, msg, nil)
	if sc.Request.NodeID != "h1" {
		t.Errorf("node_id = %q, want the rule name h1", sc.Request.NodeID)
	}
	if errs := h.ValidateInput(msg, sc); errs != nil {
		t.Errorf("agent rejected the submitted input: %v", errs)
	}

	r, found, _ := adapter.ingress.routing(t.Context(), taskID)
	if !found || r.UserID != "alice" || !r.SentStructuredRequest {
		t.Errorf("task routing = %+v, want a structured task for userID alice", r)
	}
}

func TestTransformMessage_PromptTemplateRule_ValidatesEventPayload(t *testing.T) {
	adapter, brk, _ := setupAdapterForAuth(t, defaultAuthAdapterOpts())
	handler := adapter.cfg.EventHandlers[0]
	handler.InputExpression = "template:Decide on order {{text://input.payload:orderId}}. Response Format: JSON"
	handler.StructuredInvocation = &StructuredInvocationConfig{InputSchema: orderInputSchema, OutputSchema: decisionOutputSchema}

	msg, _ := submitAndCapture(t, adapter, brk, handler, "TestAgent", map[string]any{"orderId": "42"})

	h, sc := agentStructuredRequest(t, msg, nil)
	if errs := h.ValidateInput(msg, sc); errs != nil {
		t.Errorf("agent rejected the event payload: %v", errs)
	}
	if want := "Decide on order 42. Response Format: JSON"; msg.Parts[1].Text != want {
		t.Errorf("text part = %q, want the rendered prompt %q", msg.Parts[1].Text, want)
	}
}

func inlineInput(t *testing.T, msg a2a.Message) any {
	t.Helper()
	for _, p := range msg.Parts {
		if p.Kind != "file" || p.File == nil || p.File.Bytes == "" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(p.File.Bytes)
		if err != nil {
			t.Fatalf("decode inline input: %v", err)
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatalf("unmarshal inline input: %v", err)
		}
		return v
	}
	return nil
}

func TestTransformMessage_InputExpressionBySchema(t *testing.T) {
	payload := map[string]any{"order": map[string]any{"orderId": "42"}, "orderId": "7", "note": "rush"}
	wholePayload := map[string]any{"order": map[string]any{"orderId": "42"}, "orderId": "7", "note": "rush"}
	payloadJSON, _ := json.Marshal(payload)
	const template = "template:Decide on {{text://input.payload:note}}"

	cases := []struct {
		name       string
		expression string
		schema     map[string]any
		wantText   string
		wantInput  any
	}{
		{"absent x none", "", nil, string(payloadJSON), wholePayload},
		{"absent x text", "", textInputSchema, string(payloadJSON), nil},
		{"absent x object", "", orderInputSchema, string(payloadJSON), wholePayload},
		{"selector x object", "input.payload:order", orderInputSchema, `{"orderId":"42"}`, map[string]any{"orderId": "42"}},
		{"selector x text", "input.payload:note", textInputSchema, "rush", nil},
		{"template x none", template, nil, "Decide on rush", wholePayload},
		{"template x object", template, orderInputSchema, "Decide on rush", wholePayload},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			adapter, brk, _ := setupAdapterForAuth(t, defaultAuthAdapterOpts())
			handler := adapter.cfg.EventHandlers[0]
			handler.InputExpression = tc.expression
			handler.StructuredInvocation = &StructuredInvocationConfig{InputSchema: tc.schema, OutputSchema: decisionOutputSchema}

			msg, _ := submitAndCapture(t, adapter, brk, handler, "TestAgent", payload)

			if got := msg.Parts[1].Text; got != tc.wantText {
				t.Errorf("text part = %q, want %q", got, tc.wantText)
			}
			if got := inlineInput(t, msg); !reflect.DeepEqual(got, tc.wantInput) {
				t.Errorf("inline input = %v, want %v", got, tc.wantInput)
			}
			h, sc := agentStructuredRequest(t, msg, nil)
			if errs := h.ValidateInput(msg, sc); errs != nil {
				t.Errorf("agent rejected the input: %v", errs)
			}
		})
	}
}

func TestTransformMessage_PlainRule_Unchanged(t *testing.T) {
	payload := map[string]any{"orderId": "7", "note": "rush"}
	cases := []struct {
		name       string
		expression string
		wantText   string
	}{
		{"absent", "", ""},
		{"selector", "input.payload:note", "rush"},
		{"template", "template:Decide on {{text://input.payload:note}}", "Decide on rush"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			adapter, brk, _ := setupAdapterForAuth(t, defaultAuthAdapterOpts())
			handler := adapter.cfg.EventHandlers[0]
			handler.InputExpression = tc.expression

			msg, _ := submitAndCapture(t, adapter, brk, handler, "TestAgent", payload)

			if len(msg.Parts) != 1 || msg.Parts[0].Kind != "text" || msg.Parts[0].Text != tc.wantText {
				t.Errorf("parts = %+v, want one text part %q", msg.Parts, tc.wantText)
			}
			if msg.ContextID != "" {
				t.Errorf("ContextID = %q, want none for a plain rule", msg.ContextID)
			}
		})
	}
}

func TestTransformMessage_WorkflowTargetWithSchema_SendsPlainRequest(t *testing.T) {
	adapter, brk, scopes := setupAdapterForAuth(t, defaultAuthAdapterOpts())
	scopes.scopes = []string{"*:*:*"}
	handler := adapter.cfg.EventHandlers[0]
	handler.TargetAgentName = ""
	handler.TargetWorkflowName = "ClassifyWorkflow"
	handler.StructuredInvocation = &StructuredInvocationConfig{InputSchema: orderInputSchema, OutputSchema: decisionOutputSchema}

	msg, taskID := submitAndCapture(t, adapter, brk, handler, "ClassifyWorkflow", map[string]any{"msg": "classify", "orderId": "42"})

	for _, p := range msg.Parts {
		if p.Kind != "text" {
			t.Errorf("workflow request carries a %s part %+v, want the text input only", p.Kind, p)
		}
	}
	if msg.ContextID != "" {
		t.Errorf("ContextID = %q, want none for a workflow target", msg.ContextID)
	}
	r, found, _ := adapter.ingress.routing(t.Context(), taskID)
	if !found || r.SentStructuredRequest {
		t.Errorf("task routing = %+v, want no structured request recorded", r)
	}
}

func TestInit_UsesTheGatewayArtifactService(t *testing.T) {
	opts := defaultAuthAdapterOpts()
	opts.artifacts = newTestArtifactSvc()
	adapter, _, _ := setupAdapterForAuth(t, opts)

	if adapter.artifacts != opts.artifacts {
		t.Error("adapter does not use the gateway's artifact service, so structured output can never load")
	}
}

func TestWarnStructuredInvocationMisconfig(t *testing.T) {
	schema := &StructuredInvocationConfig{OutputSchema: decisionOutputSchema}
	cfg := &Config{
		EventHandlers: []*EventHandlerConfig{
			{Name: "wf", TargetWorkflowName: "ClassifyWorkflow", StructuredInvocation: schema},
			{Name: "bad_schema", TargetAgentName: "decider", OnSuccess: "structured", OnError: "errors", StructuredInvocation: &StructuredInvocationConfig{OutputSchema: map[string]any{"type": "not-a-type"}}},
			{Name: "text_out", TargetAgentName: "decider", OnSuccess: "text", OnError: "errors", StructuredInvocation: schema},
			{Name: "full_out", TargetAgentName: "decider", OnSuccess: "full", OnError: "errors", StructuredInvocation: schema},
			{Name: "no_on_error", TargetAgentName: "decider", OnSuccess: "structured", StructuredInvocation: schema},
			{Name: "transformed", TargetAgentName: "decider", OnSuccess: "transformed", OnError: "errors", StructuredInvocation: schema},
			{Name: "listed", TargetAgentName: "decider", OnSuccess: "listed", OnError: "errors", StructuredInvocation: schema},
			{Name: "whole", TargetAgentName: "decider", OnSuccess: "whole", OnError: "errors", StructuredInvocation: schema},
			{Name: "whole_transform", TargetAgentName: "decider", OnSuccess: "whole_transform", OnError: "errors", StructuredInvocation: schema},
			{Name: "good", TargetAgentName: "decider", OnSuccess: "structured", OnError: "errors", StructuredInvocation: schema},
			{Name: "plain", TargetAgentName: "decider", OnSuccess: "text"},
		},
		OutputHandlers: map[string]*OutputHandlerConfig{
			"text":       {Name: "text", PayloadExpression: "task_response:text"},
			"full":       {Name: "full", PayloadExpression: "task_response:a2a_task_response"},
			"structured": {Name: "structured", PayloadExpression: "task_response:structured_output"},
			"errors":     {Name: "errors", PayloadExpression: "task_response:text"},
			"transformed": {Name: "transformed", PayloadExpression: "user_data.decision:", OutputTransforms: []sacexpr.TransformConfig{
				{SourceExpression: "task_response:structured_output", DestExpression: "user_data.decision:"},
			}},
			"listed": {Name: "listed", PayloadExpression: "user_data.items:", OutputTransforms: []sacexpr.TransformConfig{
				{SourceListExpression: "task_response:structured_output.items", DestListExpression: "user_data.items:"},
			}},
			"whole": {Name: "whole", PayloadExpression: "task_response:"},
			"whole_transform": {Name: "whole_transform", PayloadExpression: "user_data.response:", OutputTransforms: []sacexpr.TransformConfig{
				{SourceExpression: "task_response:", DestExpression: "user_data.response:"},
			}},
		},
	}
	var buf bytes.Buffer
	warnStructuredInvocationMisconfig(cfg, true, slog.New(slog.NewTextHandler(&buf, nil)))
	logs := buf.String()

	const noStructuredOutput = "does not publish structured_output"
	for _, want := range []struct{ message, handler string }{
		{"schemas ignored for a workflow target", "wf"},
		{"schema does not compile", "bad_schema"},
		{noStructuredOutput, "text_out"},
		{noStructuredOutput, "full_out"},
		{"has no on_error", "no_on_error"},
	} {
		if !logLineHas(logs, want.message, "handler="+want.handler) {
			t.Errorf("missing warning %q for handler %s in:\n%s", want.message, want.handler, logs)
		}
	}
	for _, quiet := range []string{"good", "transformed", "listed", "whole", "whole_transform", "plain"} {
		if logLineHasField(logs, "handler="+quiet) {
			t.Errorf("warned about correctly configured rule %s:\n%s", quiet, logs)
		}
	}
}

func TestInit_WarnsWhenStructuredOutputCannotLoad(t *testing.T) {
	buf := captureDefaultLogger(t)

	opts := defaultAuthAdapterOpts()
	opts.structuredInvocation = map[string]any{"output_schema": decisionOutputSchema}
	setupAdapterForAuth(t, opts)

	if !logLineHas(buf.String(), "without an artifact service", "handler=h1") {
		t.Errorf("Init logged no missing-artifact-service warning:\n%s", buf.String())
	}
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func captureDefaultLogger(t *testing.T) *lockedBuffer {
	t.Helper()
	buf := &lockedBuffer{}
	previous, writer, flags := slog.Default(), log.Writer(), log.Flags()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() {
		slog.SetDefault(previous)
		log.SetOutput(writer)
		log.SetFlags(flags)
	})
	return buf
}

func logLineHasField(logs, field string) bool {
	for line := range strings.SplitSeq(logs, "\n") {
		if slices.Contains(strings.Fields(line), field) {
			return true
		}
	}
	return false
}

func logLineHas(logs string, parts ...string) bool {
	for line := range strings.SplitSeq(logs, "\n") {
		matched := true
		for _, part := range parts {
			matched = matched && strings.Contains(line, part)
		}
		if matched {
			return true
		}
	}
	return false
}

func structuredResultTask(state a2a.TaskState, result map[string]any) *a2a.Task {
	return &a2a.Task{
		ID:        "t",
		ContextID: "ctx-1",
		Kind:      "task",
		Status: a2a.TaskStatus{
			State:   state,
			Message: &a2a.Message{Role: "agent", Parts: []a2a.Part{a2a.NewDataPart(result)}},
		},
		Metadata: map[string]any{"agent_name": "decider"},
	}
}

func successResult(name string, version int) map[string]any {
	return map[string]any{
		"type":                string(a2a.SignalStructuredInvocationRes),
		"status":              "success",
		"retry_count":         float64(0),
		"output_artifact_ref": map[string]any{"name": name, "version": float64(version)},
	}
}

func soleStructuredOutput(t *testing.T, mockBrk *mockPublishBroker) string {
	t.Helper()
	msgs := mockBrk.published()
	if len(msgs) != 1 || msgs[0].Topic != structuredOutputTopic {
		t.Fatalf("published %+v, want one message on %s", msgs, structuredOutputTopic)
	}
	return string(msgs[0].Payload)
}

func TestHandleFinalResponse_StructuredResult_PublishesValidatedOutput(t *testing.T) {
	mockBrk := &mockPublishBroker{}
	a := newTestAdapter(nil, structuredOutputHandlers())
	a.dataBroker = mockBrk
	a.artifacts = newTestArtifactSvc()

	loc := a.artifacts.Resolve("decider", "alice", "ctx-1", "decision.json")
	version, err := a.artifacts.Inner.Save(t.Context(), loc, []byte(`{"action":"approve"}`), "application/json")
	if err != nil {
		t.Fatalf("save artifact: %v", err)
	}
	a.seedPending(t, "t", &pendingTask{handlerCfg: agentRule(), userID: "alice"})

	a.handleFinalResponse(t.Context(), "t", structuredResultTask(a2a.TaskStateCompleted, successResult("decision.json", version)))

	if got := soleStructuredOutput(t, mockBrk); got != `{"action":"approve"}` {
		t.Errorf("structured_output = %s, want the validated artifact content", got)
	}
}

func TestHandleFinalResponse_StructuredResult_LoadsTheReferencedVersion(t *testing.T) {
	mockBrk := &mockPublishBroker{}
	a := newTestAdapter(nil, structuredOutputHandlers())
	a.dataBroker = mockBrk
	a.artifacts = newTestArtifactSvc()

	loc := a.artifacts.Resolve("decider", "alice", "ctx-1", "decision.json")
	first, err := a.artifacts.Inner.Save(t.Context(), loc, []byte(`{"action":"reject"}`), "application/json")
	if err != nil {
		t.Fatalf("save artifact: %v", err)
	}
	if _, err := a.artifacts.Inner.Save(t.Context(), loc, []byte(`{"action":"approve"}`), "application/json"); err != nil {
		t.Fatalf("save artifact: %v", err)
	}
	a.seedPending(t, "t", &pendingTask{handlerCfg: agentRule(), userID: "alice"})

	a.handleFinalResponse(t.Context(), "t", structuredResultTask(a2a.TaskStateCompleted, successResult("decision.json", first)))

	if got := soleStructuredOutput(t, mockBrk); got != `{"action":"reject"}` {
		t.Errorf("structured_output = %s, want the version the result references", got)
	}
}

func TestHandleFinalResponse_StructuredOutputUnavailable(t *testing.T) {
	missingRef := successResult("", 0)
	delete(missingRef, "output_artifact_ref")
	cases := []struct {
		name      string
		artifacts bool
		result    map[string]any
	}{
		{"artifact missing", true, successResult("gone.json", 1)},
		{"no artifact reference", true, missingRef},
		{"no artifact service", false, successResult("decision.json", 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mockBrk := &mockPublishBroker{}
			a := newTestAdapter(nil, structuredOutputHandlers())
			a.dataBroker = mockBrk
			if tc.artifacts {
				a.artifacts = newTestArtifactSvc()
			}
			a.seedPending(t, "t", &pendingTask{handlerCfg: agentRule(), userID: "alice", sentStructuredRequest: true})

			a.handleFinalResponse(t.Context(), "t", structuredResultTask(a2a.TaskStateCompleted, tc.result))

			payload := soleErrorPayload(t, mockBrk)
			if want := errorOnlyPayload(structuredOutputUnavailableMsg); !reflect.DeepEqual(payload, want) {
				t.Errorf("error payload = %v, want %v", payload, want)
			}
		})
	}
}

func TestHandleFinalResponse_CompletedTaskWithUnsuccessfulResult_PublishesOnErrorHandler(t *testing.T) {
	for _, status := range []string{"error", "failed", ""} {
		t.Run("status "+status, func(t *testing.T) {
			mockBrk := &mockPublishBroker{}
			a := newTestAdapter(nil, successAndErrorOutputs())
			a.dataBroker = mockBrk
			a.seedPending(t, "t", &pendingTask{handlerCfg: agentRule(), userID: "alice", sentStructuredRequest: true})

			a.handleFinalResponse(t.Context(), "t", structuredResultTask(a2a.TaskStateCompleted, map[string]any{
				"type":          string(a2a.SignalStructuredInvocationRes),
				"status":        status,
				"error_message": "proxied agent could not validate its output",
			}))

			payload := soleErrorPayload(t, mockBrk)
			if want := errorOnlyPayload("proxied agent could not validate its output"); !reflect.DeepEqual(payload, want) {
				t.Errorf("error payload = %v, want %v", payload, want)
			}
		})
	}
}

func TestHandleFinalResponse_StructuredResult_NonJSONOutputPublishesAsString(t *testing.T) {
	mockBrk := &mockPublishBroker{}
	a := newTestAdapter(nil, structuredOutputHandlers())
	a.dataBroker = mockBrk
	a.artifacts = newTestArtifactSvc()
	loc := a.artifacts.Resolve("decider", "alice", "ctx-1", "decision.txt")
	version, err := a.artifacts.Inner.Save(t.Context(), loc, []byte("approve"), "text/plain")
	if err != nil {
		t.Fatalf("save artifact: %v", err)
	}
	a.seedPending(t, "t", &pendingTask{handlerCfg: agentRule(), userID: "alice"})

	a.handleFinalResponse(t.Context(), "t", structuredResultTask(a2a.TaskStateCompleted, successResult("decision.txt", version)))

	if got := soleStructuredOutput(t, mockBrk); got != `"approve"` {
		t.Errorf("structured_output = %s, want the artifact text as a string", got)
	}
}

func workflowStructuredResultTask(result map[string]any) *a2a.Task {
	task := structuredResultTask(a2a.TaskStateCompleted, result)
	task.Metadata = map[string]any{"agent_name": "ClassifyWorkflow", "workflow_name": "ClassifyWorkflow"}
	return task
}

func TestHandleFinalResponse_WorkflowStructuredResult_LoadsWorkflowOutput(t *testing.T) {
	mockBrk := &mockPublishBroker{}
	a := newTestAdapter(nil, structuredOutputHandlers())
	a.dataBroker = mockBrk
	a.artifacts = newTestArtifactSvc()
	loc := a.artifacts.Resolve("ClassifyWorkflow", "alice", "ctx-1", "wf_output.json")
	version, err := a.artifacts.Inner.Save(t.Context(), loc, []byte(`{"action":"approve"}`), "application/json")
	if err != nil {
		t.Fatalf("save artifact: %v", err)
	}
	a.seedPending(t, "t", &pendingTask{handlerCfg: workflowRule(), userID: "alice"})

	a.handleFinalResponse(t.Context(), "t", workflowStructuredResultTask(successResult("wf_output.json", version)))

	if got := soleStructuredOutput(t, mockBrk); got != `{"action":"approve"}` {
		t.Errorf("structured_output = %s, want the workflow output artifact", got)
	}
}

func TestHandleFinalResponse_WorkflowStructuredResult_MissingOutputPublishesOnErrorHandler(t *testing.T) {
	mockBrk := &mockPublishBroker{}
	a := newTestAdapter(nil, structuredOutputHandlers())
	a.dataBroker = mockBrk
	a.artifacts = newTestArtifactSvc()
	a.seedPending(t, "t", &pendingTask{handlerCfg: workflowRule(), userID: "alice"})

	a.handleFinalResponse(t.Context(), "t", workflowStructuredResultTask(successResult("wf_output.json", 1)))

	payload := soleErrorPayload(t, mockBrk)
	if want := errorOnlyPayload(structuredOutputUnavailableMsg); !reflect.DeepEqual(payload, want) {
		t.Errorf("error payload = %v, want %v", payload, want)
	}
}

type cancellationAwareStore struct {
	*memory.Store
}

func (s cancellationAwareStore) Load(ctx context.Context, loc artifact.Locator, version *int) ([]byte, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	return s.Store.Load(ctx, loc, version)
}

type stalledStore struct {
	*memory.Store
}

func (stalledStore) Load(ctx context.Context, _ artifact.Locator, _ *int) ([]byte, string, error) {
	<-ctx.Done()
	return nil, "", ctx.Err()
}

func TestHandleFinalResponse_StalledOutputRead_GivesUpAtTheBound(t *testing.T) {
	mockBrk := &mockPublishBroker{}
	a := newTestAdapter(nil, structuredOutputHandlers())
	a.dataBroker = mockBrk
	a.artifacts = &artifact.ScopedService{Inner: stalledStore{memory.New()}, Scope: "namespace", Namespace: "test-ns"}
	a.outputLoadTimeout = 20 * time.Millisecond
	a.seedPending(t, "t", &pendingTask{handlerCfg: agentRule(), userID: "alice", sentStructuredRequest: true})

	done := make(chan struct{})
	go func() {
		defer close(done)
		a.handleFinalResponse(t.Context(), "t", structuredResultTask(a2a.TaskStateCompleted, successResult("decision.json", 1)))
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a stalled output read held the response worker past its bound")
	}

	payload := soleErrorPayload(t, mockBrk)
	if want := errorOnlyPayload(structuredOutputTimeoutMsg); !reflect.DeepEqual(payload, want) {
		t.Errorf("error payload = %v, want %v", payload, want)
	}
}

func TestCaptureDefaultLogger_RestoresStandardLog(t *testing.T) {
	writer, flags := log.Writer(), log.Flags()
	t.Run("capture", func(t *testing.T) { captureDefaultLogger(t) })
	if log.Writer() != writer || log.Flags() != flags {
		t.Errorf("standard log left redirected: writer %T flags %d, want %T flags %d", log.Writer(), log.Flags(), writer, flags)
	}
}

func TestHandleFinalResponse_ShutdownMidResponse_PublishesTheSucceededRun(t *testing.T) {
	for _, deferred := range []bool{true, false} {
		t.Run(fmt.Sprintf("deferredACK=%v", deferred), func(t *testing.T) {
			mockBrk := &mockPublishBroker{}
			a := newTestAdapter(nil, structuredOutputHandlers())
			a.dataBroker = mockBrk
			a.artifacts = &artifact.ScopedService{Inner: cancellationAwareStore{memory.New()}, Scope: "namespace", Namespace: "test-ns"}
			loc := a.artifacts.Resolve("decider", "alice", "ctx-1", "decision.json")
			version, err := a.artifacts.Inner.Save(t.Context(), loc, []byte(`{"action":"approve"}`), "application/json")
			if err != nil {
				t.Fatalf("save artifact: %v", err)
			}

			var acked, nacked atomic.Bool
			if deferred {
				a.holdFor(t, "t", testMsg(&acked, &nacked), AckFailurePolicy{Action: "nack", NackOutcome: "failed"})
			}
			a.seedPending(t, "t", &pendingTask{handlerCfg: agentRule(), userID: "alice", sentStructuredRequest: true, deferredACK: deferred})

			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			a.handleFinalResponse(ctx, "t", structuredResultTask(a2a.TaskStateCompleted, successResult("decision.json", version)))

			if got := soleStructuredOutput(t, mockBrk); got != `{"action":"approve"}` {
				t.Errorf("structured_output = %s, want the run's validated output", got)
			}
			if deferred && (!acked.Load() || nacked.Load()) {
				t.Errorf("deferred ACK acked=%v nacked=%v, want settled as a published success", acked.Load(), nacked.Load())
			}
		})
	}
}

func TestHandleFinalResponse_FailedStructuredRun_NoOnError_TextCarriesTheFailure(t *testing.T) {
	mockBrk := &mockPublishBroker{}
	a := newTestAdapter(nil, map[string]*OutputHandlerConfig{
		"ok_out": {Name: "ok_out", TopicExpression: "static:" + successTopic, PayloadExpression: "task_response:text", PayloadFormat: "text"},
	})
	a.dataBroker = mockBrk
	rule := agentRule()
	rule.OnError = ""
	a.seedPending(t, "t", &pendingTask{handlerCfg: rule, userID: "alice", sentStructuredRequest: true})

	a.handleFinalResponse(t.Context(), "t", structuredResultTask(a2a.TaskStateFailed, map[string]any{
		"type":          string(a2a.SignalStructuredInvocationRes),
		"status":        "error",
		"error_message": "Output validation failed after max retries",
	}))

	msgs := mockBrk.published()
	if len(msgs) != 1 || msgs[0].Topic != successTopic {
		t.Fatalf("published %+v, want one message on %s", msgs, successTopic)
	}
	if got := string(msgs[0].Payload); got != "Output validation failed after max retries" {
		t.Errorf("success payload = %q, want the failure message", got)
	}
}

func durableRule() *EventHandlerConfig {
	return &EventHandlerConfig{Name: "h1", TargetAgentName: "TestAgent", OnSuccess: "ok_out", OnError: "err_out"}
}

func submitOnInstanceA(t *testing.T, shared ingressStore) (a2a.Message, string) {
	t.Helper()
	adapter, brk, _ := setupAdapterForAuth(t, defaultAuthAdapterOpts())
	adapter.ingress = shared
	adapter.held = newHeldEvents(shared, adapter.logger)
	handler := adapter.cfg.EventHandlers[0]
	handler.StructuredInvocation = &StructuredInvocationConfig{OutputSchema: decisionOutputSchema}
	return submitAndCapture(t, adapter, brk, handler, "TestAgent", map[string]any{"msg": "approve order 42?"})
}

func instanceB(shared ingressStore, mockBrk *mockPublishBroker) *EventMeshAdapter {
	b := newTestAdapter([]*EventHandlerConfig{durableRule()}, structuredOutputHandlers())
	b.dataBroker = mockBrk
	b.artifacts = newTestArtifactSvc()
	b.ingress = shared
	b.held = newHeldEvents(shared, b.logger)
	return b
}

func TestEndToEnd_SecondInstanceLoadsStructuredOutputForTheSubmittingUser(t *testing.T) {
	shared := newMemIngressStore()
	msg, taskID := submitOnInstanceA(t, shared)

	mockBrk := &mockPublishBroker{}
	b := instanceB(shared, mockBrk)
	loc := b.artifacts.Resolve("decider", "alice", msg.ContextID, "decision.json")
	version, err := b.artifacts.Inner.Save(t.Context(), loc, []byte(`{"action":"approve"}`), "application/json")
	if err != nil {
		t.Fatalf("save artifact: %v", err)
	}
	task := structuredResultTask(a2a.TaskStateCompleted, successResult("decision.json", version))
	task.ID, task.ContextID = taskID, msg.ContextID

	b.handleFinalResponse(t.Context(), taskID, task)

	if got := soleStructuredOutput(t, mockBrk); got != `{"action":"approve"}` {
		t.Errorf("structured_output = %s, want the output saved for the submitting user", got)
	}
}

func TestEndToEnd_SecondInstanceRoutesCancelledStructuredRunToOnError(t *testing.T) {
	shared := newMemIngressStore()
	_, taskID := submitOnInstanceA(t, shared)

	mockBrk := &mockPublishBroker{}
	b := instanceB(shared, mockBrk)
	task := finalTask(a2a.TaskStateCancelled, "Task cancelled")
	task.ID = taskID

	b.handleFinalResponse(t.Context(), taskID, task)

	payload := soleErrorPayload(t, mockBrk)
	if want := errorOnlyPayload("Task cancelled"); !reflect.DeepEqual(payload, want) {
		t.Errorf("error payload = %v, want %v", payload, want)
	}
}

func TestHandleFinalResponse_WorkflowPlainResult_StructuredOutputIsWorkflowOutput(t *testing.T) {
	mockBrk := &mockPublishBroker{}
	a := newTestAdapter(nil, structuredOutputHandlers())
	a.dataBroker = mockBrk
	a.seedPending(t, "t", &pendingTask{handlerCfg: workflowRule()})

	task := finalTask(a2a.TaskStateCompleted, `Workflow "ClassifyWorkflow" completed successfully.`)
	task.Status.Message.Parts = append([]a2a.Part{a2a.NewDataPart(map[string]any{"action": "approve"})}, task.Status.Message.Parts...)
	a.handleFinalResponse(t.Context(), "t", task)

	if got := soleStructuredOutput(t, mockBrk); got != `{"action":"approve"}` {
		t.Errorf("structured_output = %s, want the workflow output data part", got)
	}
}

func TestHandleFinalResponse_CancelledStructuredAgentRun_PublishesOnErrorHandler(t *testing.T) {
	mockBrk := &mockPublishBroker{}
	a := newTestAdapter(nil, successAndErrorOutputs())
	a.dataBroker = mockBrk
	a.seedPending(t, "t", &pendingTask{handlerCfg: agentRule(), userID: "alice", sentStructuredRequest: true})

	a.handleFinalResponse(t.Context(), "t", finalTask(a2a.TaskStateCancelled, "Task cancelled"))

	payload := soleErrorPayload(t, mockBrk)
	if want := errorOnlyPayload("Task cancelled"); !reflect.DeepEqual(payload, want) {
		t.Errorf("error payload = %v, want %v", payload, want)
	}
}

func TestHandleFinalResponse_FailedAgentStructuredResult_PublishesOnErrorHandler(t *testing.T) {
	mockBrk := &mockPublishBroker{}
	a := newTestAdapter(nil, successAndErrorOutputs())
	a.dataBroker = mockBrk
	a.seedPending(t, "t", &pendingTask{handlerCfg: agentRule(), userID: "alice", sentStructuredRequest: true})

	const failure = "output failed schema validation after 2 retries"
	a.handleFinalResponse(t.Context(), "t", structuredResultTask(a2a.TaskStateFailed, map[string]any{
		"type":          string(a2a.SignalStructuredInvocationRes),
		"status":        "error",
		"error_message": failure,
	}))

	payload := soleErrorPayload(t, mockBrk)
	if want := errorOnlyPayload(failure); !reflect.DeepEqual(payload, want) {
		t.Errorf("error payload = %v, want %v", payload, want)
	}
}
