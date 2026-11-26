// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/structured"
)

func tripSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"destination": map[string]any{"type": "string"},
			"needsHotel":  map[string]any{"type": "boolean"},
		},
		"required": []any{"destination", "needsHotel"},
	}
}

func instanceWithInputSchema(t *testing.T, schema map[string]any) *WorkflowInstance {
	t.Helper()
	var logs bytes.Buffer
	w := &WorkflowInstance{
		cfg:    &WorkflowConfig{Name: "trip", InputSchema: schema},
		logger: slog.New(slog.NewTextHandler(&logs, nil)),
	}
	w.initInputSchema()
	return w
}

func TestTextRunInput(t *testing.T) {
	requiredTextSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text":     map[string]any{"type": "string"},
			"language": map[string]any{"type": "string", "enum": []any{"en", "fr"}},
		},
		"required": []any{"text"},
	}
	optionalTextSchema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"text": map[string]any{"type": "string"}},
	}
	numberSchema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"n": map[string]any{"type": "integer"}},
	}

	tests := []struct {
		name   string
		schema map[string]any
		text   string
		want   map[string]any
	}{
		{
			name:   "no schema keeps a JSON message as text",
			schema: nil,
			text:   `{"destination":"Porto","needsHotel":true}`,
			want:   map[string]any{"text": `{"destination":"Porto","needsHotel":true}`},
		},
		{
			name:   "empty schema keeps a JSON message as text",
			schema: map[string]any{},
			text:   `{"destination":"Porto"}`,
			want:   map[string]any{"text": `{"destination":"Porto"}`},
		},
		{
			name:   "an object that satisfies the schema and names only declared fields is the input",
			schema: tripSchema(),
			text:   `{"destination":"Porto","needsHotel":true}`,
			want:   map[string]any{"destination": "Porto", "needsHotel": true},
		},
		{
			name:   "false is a value, not a missing field",
			schema: tripSchema(),
			text:   `{"destination":"Porto","needsHotel":false}`,
			want:   map[string]any{"destination": "Porto", "needsHotel": false},
		},
		{
			name:   "surrounding whitespace is ignored",
			schema: tripSchema(),
			text:   "\n  {\"destination\":\"Porto\",\"needsHotel\":true}  \n",
			want:   map[string]any{"destination": "Porto", "needsHotel": true},
		},
		{
			name:   "prose stays text",
			schema: tripSchema(),
			text:   "plan me a trip to Porto",
			want:   map[string]any{"text": "plan me a trip to Porto"},
		},
		{
			name:   "a JSON array is not an input document",
			schema: tripSchema(),
			text:   `[{"destination":"Porto","needsHotel":true}]`,
			want:   map[string]any{"text": `[{"destination":"Porto","needsHotel":true}]`},
		},
		{
			name:   "malformed JSON stays text",
			schema: tripSchema(),
			text:   `{"destination":"Porto",}`,
			want:   map[string]any{"text": `{"destination":"Porto",}`},
		},
		{
			name:   "an object that violates the schema is returned when the text wrapper does too, so the rejection names its fields",
			schema: tripSchema(),
			text:   `{"needsHotel":"banana"}`,
			want:   map[string]any{"needsHotel": "banana"},
		},
		{
			name:   "an empty object is not a document the schema recognises",
			schema: optionalTextSchema,
			text:   `{}`,
			want:   map[string]any{"text": "{}"},
		},
		{
			name:   "an object with an undeclared key is the input when the text wrapper fails the schema",
			schema: tripSchema(),
			text:   `{"destination":"Porto","needsHotel":true,"source":"crm"}`,
			want:   map[string]any{"destination": "Porto", "needsHotel": true, "source": "crm"},
		},
		{
			name:   "a declared-fields object that violates the schema stays text when the text wrapper satisfies it",
			schema: numberSchema,
			text:   `{"n":"seven"}`,
			want:   map[string]any{"text": `{"n":"seven"}`},
		},
		{
			name:   "a schema with no declared properties recognises no object",
			schema: map[string]any{"type": "object"},
			text:   `{"destination":"Porto"}`,
			want:   map[string]any{"text": `{"destination":"Porto"}`},
		},
		{
			name:   "required text with optional language: a pasted export with extra keys stays text",
			schema: requiredTextSchema,
			text:   `{"type":"message","text":"hi","user":"U1"}`,
			want:   map[string]any{"text": `{"type":"message","text":"hi","user":"U1"}`},
		},
		{
			name:   "required text with optional language: an object naming only declared fields is the input",
			schema: requiredTextSchema,
			text:   `{"text":"hello","language":"fr"}`,
			want:   map[string]any{"text": "hello", "language": "fr"},
		},
		{
			name:   "required text: an object missing text stays text",
			schema: requiredTextSchema,
			text:   `{"destination":"Porto"}`,
			want:   map[string]any{"text": `{"destination":"Porto"}`},
		},
		{
			name:   "optional text, object naming an undeclared field stays text",
			schema: optionalTextSchema,
			text:   `{"orderId":7}`,
			want:   map[string]any{"text": `{"orderId":7}`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := instanceWithInputSchema(t, tc.schema)
			if got := w.textRunInput(tc.text); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("textRunInput(%q) = %#v, want %#v", tc.text, got, tc.want)
			}
		})
	}
}

func TestExtractWorkflowInput_TextJSONReadsAsDocumentOnlyWithSchema(t *testing.T) {
	msg := a2a.Message{
		Kind:  "message",
		Role:  a2a.RoleUser,
		Parts: []a2a.Part{{Kind: "text", Text: `{"destination":"Porto","needsHotel":true}`}},
	}

	withSchema := instanceWithInputSchema(t, tripSchema())
	input, isStructured := withSchema.extractWorkflowInput(t.Context(), msg, a2a.UserProperties{}, discardLogger())
	if isStructured {
		t.Error("isStructured = true, want false for a plain-text message")
	}
	if input["destination"] != "Porto" || input["needsHotel"] != true {
		t.Errorf("input = %#v, want the parsed document", input)
	}
	if violations := withSchema.inputViolations(input); len(violations) != 0 {
		t.Errorf("a satisfying document was rejected: %v", violations)
	}

	withoutSchema := instanceWithInputSchema(t, nil)
	input, _ = withoutSchema.extractWorkflowInput(t.Context(), msg, a2a.UserProperties{}, discardLogger())
	if _, parsed := input["destination"]; parsed {
		t.Errorf("a workflow with no input_schema parsed its text message: %#v", input)
	}
	if input["text"] != `{"destination":"Porto","needsHotel":true}` {
		t.Errorf("input[text] = %#v, want the raw message", input["text"])
	}
}

func TestInputViolations(t *testing.T) {
	w := instanceWithInputSchema(t, tripSchema())

	if got := w.inputViolations(map[string]any{"destination": "Porto", "needsHotel": true}); len(got) != 0 {
		t.Errorf("valid input reported violations: %v", got)
	}

	joined := strings.Join(w.inputViolations(map[string]any{"needsHotel": "banana"}), "\n")
	for _, field := range []string{"destination", "needsHotel"} {
		if !strings.Contains(joined, field) {
			t.Errorf("violations do not name %q:\n%s", field, joined)
		}
	}

	var nilInstance *WorkflowInstance
	if got := nilInstance.inputViolations(map[string]any{}); got != nil {
		t.Errorf("an executor built without an instance reported violations: %v", got)
	}
	if got := instanceWithInputSchema(t, nil).inputViolations(map[string]any{}); got != nil {
		t.Errorf("a workflow with no input_schema reported violations: %v", got)
	}
}

func TestRequiredInputHint(t *testing.T) {
	if got, want := requiredInputHint(tripSchema()),
		"Send the input as a JSON object. Required: destination (string), needsHotel (boolean)."; got != want {
		t.Errorf("hint = %q, want %q", got, want)
	}

	withOptional := map[string]any{
		"properties": map[string]any{
			"text":     map[string]any{"type": "string"},
			"language": map[string]any{},
			"audience": map[string]any{},
		},
		"required": []string{"text"},
	}
	if got, want := requiredInputHint(withOptional),
		"Send the input as a JSON object. Required: text (string). Optional: audience, language."; got != want {
		t.Errorf("hint = %q, want %q", got, want)
	}

	if got := requiredInputHint(map[string]any{"type": "object"}); got != "" {
		t.Errorf("a schema naming no field produced a hint: %q", got)
	}
}

func TestInitInputSchema_UncompilableSchemaLeavesEnforcementOffAndWarns(t *testing.T) {
	var logs bytes.Buffer
	w := &WorkflowInstance{
		cfg:    &WorkflowConfig{Name: "trip", InputSchema: map[string]any{"type": "strng"}},
		logger: slog.New(slog.NewTextHandler(&logs, nil)),
	}

	w.initInputSchema()

	if w.inputSchema != nil {
		t.Error("an uncompilable input_schema was kept; every run would be rejected")
	}
	if got := w.inputViolations(map[string]any{"anything": 1}); got != nil {
		t.Errorf("input was rejected against an uncompilable schema: %v", got)
	}
	if !strings.Contains(logs.String(), "level=WARN") {
		t.Errorf("an uncompilable input_schema was not reported at WARN:\n%s", logs.String())
	}
}

type rejection struct {
	state     *WorkflowExecutionState
	published []capturedMessage
	logs      string
	final     map[string]any
	finalText string
}

func startRejectedRun(t *testing.T, schema map[string]any, structuredCaller bool, start EventStart) rejection {
	t.Helper()
	cfg := &WorkflowConfig{
		Name:        "trip",
		FailFast:    true,
		InputSchema: schema,
		Nodes: []*NodeConfig{
			{ID: "lookup", Type: "agent", AgentName: "lookup_agent"},
			{ID: "cleanup", Type: "agent", AgentName: "cleanup_agent"},
		},
		OnExit: &ExitHandler{OnFailure: "cleanup"},
	}
	dag, err := BuildDependencyGraph(cfg.Nodes[:1])
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState(cfg.Name, "e1")
	e, brk := newCapturingExecutor(t, cfg, dag, state)
	e.instance.inputSchema = instanceWithInputSchema(t, schema).inputSchema
	e.execCtx.UserProps.ReplyTo = "test/reply"
	e.execCtx.IsStructuredInvocation = structuredCaller
	var logs bytes.Buffer
	e.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	start.TaskID = "test-task"
	start.IsStructuredInvocation = structuredCaller
	if err := e.Advance(t.Context(), &start); err != nil {
		t.Fatalf("Advance: %v", err)
	}

	out := rejection{state: state, logs: logs.String()}
	brk.mu.Lock()
	out.published = append(out.published, brk.messages...)
	brk.mu.Unlock()
	for _, m := range out.published {
		if m.Topic == "test/reply" {
			out.final = extractDataPart(t, m.Payload)
			out.finalText = failedTaskText(t, m.Payload)
		}
	}
	return out
}

func assertNothingRan(t *testing.T, r rejection) {
	t.Helper()
	if !r.state.Finalized || r.state.PendingOutcome != "failure" {
		t.Fatalf("Finalized = %v, PendingOutcome = %q; want a finalized failure", r.state.Finalized, r.state.PendingOutcome)
	}
	if r.state.ExitHandlersRan || r.state.InExitHandler {
		t.Error("the on_exit chain ran for a run rejected at the door")
	}
	if len(r.state.PendingNodes) != 0 || len(r.state.CompletedNodes) != 0 {
		t.Errorf("a node was dispatched: pending=%v completed=%v", r.state.PendingNodes, r.state.CompletedNodes)
	}
	if len(r.state.Timers) != 0 {
		t.Errorf("a timer was armed for a rejected run: %v", r.state.Timers)
	}
	if strings.Contains(r.logs, "starting workflow execution") {
		t.Errorf("a rejected run logged that it started:\n%s", r.logs)
	}
	for _, m := range r.published {
		if m.Topic == "test/reply" {
			continue
		}
		if data := extractDataPart(t, m.Payload); data["type"] == "workflow_node_execution_start" {
			t.Errorf("a node started: %v", data["node_id"])
		}
	}
}

func TestEventStart_InvalidInputFailsRunBeforeAnyNode(t *testing.T) {
	r := startRejectedRun(t, tripSchema(), false, EventStart{Input: map[string]any{"needsHotel": "banana"}})

	assertNothingRan(t, r)
	if !strings.HasPrefix(r.finalText, "Workflow ") || !strings.Contains(r.finalText, structured.InputValidationFailedPrefix) {
		t.Fatalf("final failure text = %q, want it to carry %q", r.finalText, structured.InputValidationFailedPrefix)
	}
	for _, want := range []string{
		"destination", "needsHotel", "want boolean",
		"Send the input as a JSON object. Required: destination (string), needsHotel (boolean).",
	} {
		if !strings.Contains(r.finalText, want) {
			t.Errorf("final failure does not contain %q: %s", want, r.finalText)
		}
	}
}

func TestEventStart_InvalidInputFailsStructuredCallerWithStructuredResult(t *testing.T) {
	r := startRejectedRun(t, tripSchema(), true, EventStart{Input: map[string]any{"needsHotel": "banana"}})

	assertNothingRan(t, r)
	if r.final["type"] != "structured_invocation_result" || r.final["status"] != "error" {
		t.Fatalf("final data part = %#v, want a structured_invocation_result with status error", r.final)
	}
	message, _ := r.final["error_message"].(string)
	if !strings.HasPrefix(message, structured.InputValidationFailedPrefix) ||
		!strings.Contains(message, "destination") || !strings.Contains(message, "needsHotel") {
		t.Errorf("error_message = %q, want the prefix and both fields", message)
	}
}

func TestEventStart_RejectionKeepsCallerValuesOutOfLogsAndBoundsTheMessage(t *testing.T) {
	schema := tripSchema()
	schema["properties"].(map[string]any)["destination"] = map[string]any{"type": "string", "pattern": "^[A-Za-z]+$"}
	secret := "CANARY-" + strings.Repeat("x", 5000)

	r := startRejectedRun(t, schema, false, EventStart{
		Input: map[string]any{"destination": secret, "needsHotel": true},
	})

	assertNothingRan(t, r)
	if !strings.Contains(r.finalText, "CANARY-") {
		t.Errorf("the caller was not told which value was refused: %s", r.finalText)
	}
	if len(r.finalText) > maxViolationChars+500 {
		t.Errorf("failure message is %d bytes, want it bounded near %d", len(r.finalText), maxViolationChars)
	}
	if strings.Contains(r.logs, "CANARY-") {
		t.Errorf("a caller's input value reached the logs:\n%s", r.logs)
	}
	if !strings.Contains(r.logs, "input validation failure (") {
		t.Errorf("the rejection was not logged without values:\n%s", r.logs)
	}
}

func failedTaskText(t *testing.T, payload []byte) string {
	t.Helper()
	var resp struct {
		Result struct {
			Status struct {
				State   string `json:"state"`
				Message struct {
					Parts []struct {
						Kind string `json:"kind"`
						Text string `json:"text"`
						Data struct {
							ErrorMessage string `json:"error_message"`
						} `json:"data"`
					} `json:"parts"`
				} `json:"message"`
			} `json:"status"`
		} `json:"result"`
	}
	if err := json.Unmarshal(payload, &resp); err != nil {
		t.Fatalf("unmarshal final response: %v", err)
	}
	if resp.Result.Status.State != string(a2a.TaskStateFailed) {
		t.Fatalf("task state = %q, want %q", resp.Result.Status.State, a2a.TaskStateFailed)
	}
	for _, p := range resp.Result.Status.Message.Parts {
		switch p.Kind {
		case "text":
			return p.Text
		case "data":
			return p.Data.ErrorMessage
		}
	}
	t.Fatal("final failure carries no text or data part")
	return ""
}
