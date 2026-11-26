// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func TestBuildSimplifiedResponse_TextOnly(t *testing.T) {
	task := &a2a.Task{
		Status: a2a.TaskStatus{
			State: "completed",
			Message: &a2a.Message{
				Role: "agent",
				Parts: []a2a.Part{
					a2a.NewTextPart("Hello world"),
				},
			},
		},
	}

	resp := buildSimplifiedResponse(task)

	if resp.Text != "Hello world" {
		t.Errorf("text = %q, want %q", resp.Text, "Hello world")
	}
	if len(resp.Files) != 0 {
		t.Errorf("files = %v, want empty", resp.Files)
	}
	if len(resp.Data) != 0 {
		t.Errorf("data = %v, want empty", resp.Data)
	}
}

func TestBuildSimplifiedResponse_MultipleTextParts(t *testing.T) {
	task := &a2a.Task{
		Status: a2a.TaskStatus{
			State: "completed",
			Message: &a2a.Message{
				Role: "agent",
				Parts: []a2a.Part{
					a2a.NewTextPart("line1"),
					a2a.NewTextPart("line2"),
				},
			},
		},
	}

	resp := buildSimplifiedResponse(task)

	if resp.Text != "line1\nline2" {
		t.Errorf("text = %q, want %q", resp.Text, "line1\nline2")
	}
}

func TestBuildSimplifiedResponse_FileParts(t *testing.T) {
	task := &a2a.Task{
		Status: a2a.TaskStatus{
			State: "completed",
			Message: &a2a.Message{
				Role: "agent",
				Parts: []a2a.Part{
					a2a.NewFilePart("artifact:file.txt", "file.txt", "text/plain"),
				},
			},
		},
	}

	resp := buildSimplifiedResponse(task)

	if len(resp.Files) != 1 {
		t.Fatalf("files count = %d, want 1", len(resp.Files))
	}
	f := resp.Files[0]
	if f.Name != "file.txt" {
		t.Errorf("name = %q", f.Name)
	}
	if f.MimeType != "text/plain" {
		t.Errorf("mimeType = %q", f.MimeType)
	}
	if f.Content != "artifact:file.txt" {
		t.Errorf("content = %q", f.Content)
	}
}

func TestBuildSimplifiedResponse_DataParts(t *testing.T) {
	task := &a2a.Task{
		Status: a2a.TaskStatus{
			State: "completed",
			Message: &a2a.Message{
				Role: "agent",
				Parts: []a2a.Part{
					a2a.NewDataPart(map[string]any{"key": "value"}),
				},
			},
		},
	}

	resp := buildSimplifiedResponse(task)

	if len(resp.Data) != 1 {
		t.Fatalf("data count = %d, want 1", len(resp.Data))
	}
	m, ok := resp.Data[0].(map[string]any)
	if !ok {
		t.Fatalf("data[0] type = %T, want map", resp.Data[0])
	}
	if m["key"] != "value" {
		t.Errorf("data[0].key = %v", m["key"])
	}
}

func TestBuildSimplifiedResponse_NilMessage(t *testing.T) {
	task := &a2a.Task{
		Status: a2a.TaskStatus{
			State:   "completed",
			Message: nil,
		},
	}

	resp := buildSimplifiedResponse(task)
	if resp.Text != "" {
		t.Errorf("text = %q, want empty", resp.Text)
	}
}

func TestBuildSimplifiedResponse_A2ATaskResponse(t *testing.T) {
	task := &a2a.Task{
		ID: "test-123",
		Status: a2a.TaskStatus{
			State: "completed",
			Message: &a2a.Message{
				Role:  "agent",
				Parts: []a2a.Part{a2a.NewTextPart("done")},
			},
		},
	}

	resp := buildSimplifiedResponse(task)

	if resp.A2ATaskResponse == nil {
		t.Fatal("a2a_task_response is nil")
	}
	taskMap, ok := resp.A2ATaskResponse.(map[string]any)
	if !ok {
		t.Fatalf("a2a_task_response type = %T", resp.A2ATaskResponse)
	}
	if taskMap["id"] != "test-123" {
		t.Errorf("task id = %v", taskMap["id"])
	}
}

func TestRewriteTaskResponseStr(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"task_response:text", "input.payload:text"},
		{"task_response:data[0]", "input.payload:data[0]"},
		{"input.payload:text", "input.payload:text"},
		{"template:{{text://task_response:text}}", "template:{{text://input.payload:text}}"},
		{"static:hello", "static:hello"},
	}

	for _, tc := range tests {
		got := rewriteTaskResponseStr(tc.input)
		if got != tc.want {
			t.Errorf("rewrite(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestEncodePayload_JSON(t *testing.T) {
	data := map[string]any{"key": "value"}
	b, err := encodePayload(data, "json")
	if err != nil {
		t.Fatalf("json encode: %v", err)
	}
	if string(b) != `{"key":"value"}` {
		t.Errorf("json = %s", string(b))
	}
}

func TestEncodePayload_Text(t *testing.T) {
	b, err := encodePayload("hello world", "text")
	if err != nil {
		t.Fatalf("text encode: %v", err)
	}
	if string(b) != "hello world" {
		t.Errorf("text = %s", string(b))
	}
}

func TestEncodePayload_YAML(t *testing.T) {
	data := map[string]any{"name": "test"}
	b, err := encodePayload(data, "yaml")
	if err != nil {
		t.Fatalf("yaml encode: %v", err)
	}
	if len(b) == 0 {
		t.Error("yaml output is empty")
	}
}

func TestEncodePayload_DefaultJSON(t *testing.T) {
	data := map[string]any{"a": 1}
	b, err := encodePayload(data, "")
	if err != nil {
		t.Fatalf("default encode: %v", err)
	}
	if string(b) != `{"a":1}` {
		t.Errorf("default = %s", string(b))
	}
}

func TestEncodePayload_TextBytes(t *testing.T) {
	b, err := encodePayload([]byte("raw bytes"), "text")
	if err != nil {
		t.Fatalf("text bytes encode: %v", err)
	}
	if string(b) != "raw bytes" {
		t.Errorf("text bytes = %s", string(b))
	}
}

func TestEncodePayload_TextNonString(t *testing.T) {
	b, err := encodePayload(42, "text")
	if err != nil {
		t.Fatalf("text non-string encode: %v", err)
	}
	if string(b) != "42" {
		t.Errorf("text non-string = %s", string(b))
	}
}

func TestEncodePayload_CSV(t *testing.T) {
	data := map[string]any{"col": "val"}
	b, err := encodePayload(data, "csv")
	if err != nil {
		t.Fatalf("csv encode: %v", err)
	}
	if string(b) != `{"col":"val"}` {
		t.Errorf("csv = %s", string(b))
	}
}

func TestEncodePayload_UnknownFormat(t *testing.T) {
	data := map[string]any{"x": 1}
	b, err := encodePayload(data, "xml")
	if err != nil {
		t.Fatalf("unknown format encode: %v", err)
	}
	if string(b) != `{"x":1}` {
		t.Errorf("unknown format = %s", string(b))
	}
}

type mockPublishBroker struct {
	broker.Broker
	guaranteedErr       error
	beforeGuaranteedAck func()
	mu                  sync.Mutex
	messages            []mockPublished
}

type mockPublished struct {
	Topic      string
	Payload    []byte
	Message    *broker.Message
	Guaranteed bool
}

func (b *mockPublishBroker) Publish(_ context.Context, topic string, msg *broker.Message) error {
	b.record(topic, msg, false)
	return nil
}

func (b *mockPublishBroker) PublishGuaranteed(_ context.Context, topic string, msg *broker.Message) error {
	if b.beforeGuaranteedAck != nil {
		b.beforeGuaranteedAck()
	}
	if b.guaranteedErr != nil {
		return b.guaranteedErr
	}
	b.record(topic, msg, true)
	return nil
}

func (b *mockPublishBroker) record(topic string, msg *broker.Message, guaranteed bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.messages = append(b.messages, mockPublished{Topic: topic, Payload: msg.Payload, Message: msg, Guaranteed: guaranteed})
}

func (b *mockPublishBroker) published() []mockPublished {
	b.mu.Lock()
	defer b.mu.Unlock()
	cp := make([]mockPublished, len(b.messages))
	copy(cp, b.messages)
	return cp
}

func newTestAdapter(handlers []*EventHandlerConfig, outputHandlers map[string]*OutputHandlerConfig) *EventMeshAdapter {
	a := &EventMeshAdapter{
		logger: slog.Default(),
		cfg: &Config{
			EventHandlers:  handlers,
			OutputHandlers: outputHandlers,
		},
	}
	a.ensureIngress()
	return a
}

func TestHandleErrorResponse_UnknownTask(t *testing.T) {
	a := newTestAdapter(nil, nil)
	a.handleErrorResponse(context.Background(), "unknown-task", &a2a.Error{Code: -1, Message: "fail"})
}

func TestHandleErrorResponse_WithOnError(t *testing.T) {
	mockBrk := &mockPublishBroker{}
	a := newTestAdapter(nil, map[string]*OutputHandlerConfig{
		"err_out": {
			Name:              "err_out",
			TopicExpression:   "static:errors/result",
			PayloadExpression: "input.payload:a2a_task_response",
			PayloadFormat:     "json",
		},
	})
	a.dataBroker = mockBrk

	a.seedPending(t, "task-1", &pendingTask{
		handlerCfg: &EventHandlerConfig{
			OnError: "err_out",
		},
		forwardContext: map[string]any{},
	})

	a.handleErrorResponse(context.Background(), "task-1", &a2a.Error{Code: -32000, Message: "agent failed"})

	msgs := mockBrk.published()
	if len(msgs) != 1 {
		t.Fatalf("published %d messages, want 1", len(msgs))
	}
	if msgs[0].Topic != "errors/result" {
		t.Errorf("topic = %q, want errors/result", msgs[0].Topic)
	}

	var payload map[string]any
	if err := json.Unmarshal(msgs[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	errMap, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("payload.error type = %T", payload["error"])
	}
	if errMap["message"] != "agent failed" {
		t.Errorf("error.message = %v", errMap["message"])
	}

	exists := a.pendingExists(t, "task-1")
	if exists {
		t.Error("pending task not cleaned up")
	}
}

func TestHandleErrorResponse_NoOnError(t *testing.T) {
	a := newTestAdapter(nil, nil)
	a.dataBroker = &mockPublishBroker{}

	a.seedPending(t, "task-2", &pendingTask{
		handlerCfg: &EventHandlerConfig{},
	})

	a.handleErrorResponse(context.Background(), "task-2", &a2a.Error{Code: -1, Message: "fail"})

	exists := a.pendingExists(t, "task-2")
	if exists {
		t.Error("pending task not cleaned up")
	}
}

func TestHandleErrorResponse_DeferredACK(t *testing.T) {
	a := newTestAdapter(nil, nil)
	a.dataBroker = &mockPublishBroker{}

	msg := &broker.Message{Payload: []byte("test")}
	a.holdFor(t, "task-3", msg, AckFailurePolicy{Action: "nack", NackOutcome: "rejected"})

	a.seedPending(t, "task-3", &pendingTask{
		handlerCfg:  &EventHandlerConfig{},
		deferredACK: true,
	})

	a.handleErrorResponse(context.Background(), "task-3", &a2a.Error{Code: -1, Message: "fail"})

	if a.heldCount(t) != 0 {
		t.Errorf("pending deferred ACKs = %d, want 0", a.heldCount(t))
	}
}

func TestHandleFinalResponse_TextPayload_Verbatim(t *testing.T) {
	mockBrk := &mockPublishBroker{}
	a := newTestAdapter(nil, map[string]*OutputHandlerConfig{
		"out": {
			Name:              "out",
			TopicExpression:   "static:result/topic",
			PayloadExpression: "task_response:text",
			PayloadFormat:     "text",
		},
	})
	a.dataBroker = mockBrk

	a.seedPending(t, "task-text", &pendingTask{
		handlerCfg: &EventHandlerConfig{OnSuccess: "out"},
	})

	want := "Compliance analysis complete: 1 finding routed SOP_DISPOSITION."
	task := &a2a.Task{
		Status: a2a.TaskStatus{
			State: "completed",
			Message: &a2a.Message{
				Role:  "agent",
				Parts: []a2a.Part{a2a.NewTextPart(want)},
			},
		},
	}
	a.handleFinalResponse(context.Background(), "task-text", task)

	msgs := mockBrk.published()
	if len(msgs) != 1 {
		t.Fatalf("published %d messages, want 1", len(msgs))
	}
	got := string(msgs[0].Payload)
	if got != want {
		t.Errorf("payload = %q, want %q", got, want)
	}
	if a.pendingExists(t, "task-text") {
		t.Error("routing left after completion")
	}
}

func TestHandleFinalResponse_FullA2APayload_NonEmpty(t *testing.T) {
	mockBrk := &mockPublishBroker{}
	a := newTestAdapter(nil, map[string]*OutputHandlerConfig{
		"out": {
			Name:              "out",
			TopicExpression:   "static:result/topic",
			PayloadExpression: "task_response:a2a_task_response",
			PayloadFormat:     "json",
		},
	})
	a.dataBroker = mockBrk

	a.seedPending(t, "task-full", &pendingTask{
		handlerCfg: &EventHandlerConfig{OnSuccess: "out"},
	})

	textBody := "Hello consumer"
	task := &a2a.Task{
		ID:   "task-full",
		Kind: "task",
		Status: a2a.TaskStatus{
			State: "completed",
			Message: &a2a.Message{
				Role:  "agent",
				Parts: []a2a.Part{a2a.NewTextPart(textBody)},
			},
		},
	}
	a.handleFinalResponse(context.Background(), "task-full", task)

	msgs := mockBrk.published()
	if len(msgs) != 1 {
		t.Fatalf("published %d messages, want 1", len(msgs))
	}
	if len(msgs[0].Payload) == 0 {
		t.Fatalf("payload is empty; want non-empty JSON envelope")
	}
	if !strings.Contains(string(msgs[0].Payload), textBody) {
		t.Errorf("payload does not contain expected text %q; got: %s",
			textBody, msgs[0].Payload)
	}
}

func TestHandleFinalResponse_TextPayload_AfterJSONRoundTrip(t *testing.T) {
	mockBrk := &mockPublishBroker{}
	a := newTestAdapter(nil, map[string]*OutputHandlerConfig{
		"out": {
			Name:              "out",
			TopicExpression:   "static:result/topic",
			PayloadExpression: "task_response:text",
			PayloadFormat:     "text",
		},
	})
	a.dataBroker = mockBrk

	a.seedPending(t, "task-rt", &pendingTask{
		handlerCfg: &EventHandlerConfig{OnSuccess: "out"},
	})

	want := "Compliance analysis complete: 6 findings."
	originalTask := a2a.Task{
		ID:   "task-rt",
		Kind: "task",
		Status: a2a.TaskStatus{
			State: "completed",
			Message: &a2a.Message{
				Kind:      "message",
				MessageID: "msg-task-rt-resp",
				Role:      a2a.RoleAgent,
				Parts:     []a2a.Part{a2a.NewTextPart(want)},
			},
		},
	}

	resp := a2a.NewResponse("task-rt", originalTask)
	respBytes, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("agent-side marshal: %v", err)
	}

	var gotResp a2a.Response
	if err = json.Unmarshal(respBytes, &gotResp); err != nil {
		t.Fatalf("gateway-side unmarshal response: %v", err)
	}
	resultBytes, err := json.Marshal(gotResp.Result)
	if err != nil {
		t.Fatalf("gateway-side re-marshal result: %v", err)
	}
	var gotTask a2a.Task
	if err := json.Unmarshal(resultBytes, &gotTask); err != nil {
		t.Fatalf("gateway-side unmarshal task: %v", err)
	}

	if gotTask.Status.Message == nil {
		t.Fatalf("after JSON round-trip, task.Status.Message is nil")
	}
	if len(gotTask.Status.Message.Parts) == 0 {
		t.Fatalf("after JSON round-trip, task.Status.Message.Parts is empty")
	}
	gotText := gotTask.Status.Message.Parts[0].Text
	if gotText != want {
		t.Fatalf("after JSON round-trip, Parts[0].Text = %q, want %q", gotText, want)
	}

	a.handleFinalResponse(context.Background(), "task-rt", &gotTask)

	msgs := mockBrk.published()
	if len(msgs) != 1 {
		t.Fatalf("published %d messages, want 1", len(msgs))
	}
	if string(msgs[0].Payload) != want {
		t.Errorf("payload = %q, want %q", string(msgs[0].Payload), want)
	}
}

func TestHandleFinalResponse_DynamicTopic_Interpolated(t *testing.T) {
	mockBrk := &mockPublishBroker{}
	a := newTestAdapter(nil, map[string]*OutputHandlerConfig{
		"out": {
			Name:              "out",
			TopicExpression:   "template:fossa/analysis/result/{{text://user_data.forward_context:event_id}}",
			PayloadExpression: "task_response:text",
			PayloadFormat:     "text",
		},
	})
	a.dataBroker = mockBrk

	eventID := "abc-123-event-id"
	a.seedPending(t, "task-dyn", &pendingTask{
		handlerCfg:     &EventHandlerConfig{OnSuccess: "out"},
		forwardContext: map[string]any{"event_id": eventID},
	})

	task := &a2a.Task{
		Status: a2a.TaskStatus{
			State: "completed",
			Message: &a2a.Message{
				Role:  "agent",
				Parts: []a2a.Part{a2a.NewTextPart("ok")},
			},
		},
	}
	a.handleFinalResponse(context.Background(), "task-dyn", task)

	msgs := mockBrk.published()
	if len(msgs) != 1 {
		t.Fatalf("published %d messages, want 1", len(msgs))
	}
	wantTopic := "fossa/analysis/result/" + eventID
	if msgs[0].Topic != wantTopic {
		t.Errorf("topic = %q, want %q (template substitution failed)", msgs[0].Topic, wantTopic)
	}
}

func TestHandleFinalResponse_UnknownTask(t *testing.T) {
	a := newTestAdapter(nil, nil)
	task := &a2a.Task{Status: a2a.TaskStatus{State: "completed"}}
	a.handleFinalResponse(context.Background(), "unknown-task", task)
}

func TestHandleFinalResponse_NoOnSuccess(t *testing.T) {
	a := newTestAdapter(nil, nil)

	a.seedPending(t, "task-4", &pendingTask{
		handlerCfg: &EventHandlerConfig{},
	})

	task := &a2a.Task{Status: a2a.TaskStatus{State: "completed"}}
	a.handleFinalResponse(context.Background(), "task-4", task)

	exists := a.pendingExists(t, "task-4")
	if exists {
		t.Error("pending task not cleaned up")
	}
}

func TestHandleFinalResponse_MissingOutputHandler(t *testing.T) {
	a := newTestAdapter(nil, map[string]*OutputHandlerConfig{})

	a.seedPending(t, "task-5", &pendingTask{
		handlerCfg: &EventHandlerConfig{
			OnSuccess: "nonexistent",
		},
	})

	task := &a2a.Task{Status: a2a.TaskStatus{State: "completed"}}
	a.handleFinalResponse(context.Background(), "task-5", task)

	exists := a.pendingExists(t, "task-5")
	if exists {
		t.Error("pending task not cleaned up")
	}
}

func TestHandleFinalResponse_DeferredACK_Success(t *testing.T) {
	mockBrk := &mockPublishBroker{}
	a := newTestAdapter(nil, map[string]*OutputHandlerConfig{
		"out": {
			Name:              "out",
			TopicExpression:   "static:result/topic",
			PayloadExpression: "input.payload:text",
			PayloadFormat:     "text",
		},
	})
	a.dataBroker = mockBrk

	msg := &broker.Message{Payload: []byte("orig")}
	a.holdFor(t, "task-6", msg, AckFailurePolicy{Action: "nack"})

	a.seedPending(t, "task-6", &pendingTask{
		handlerCfg: &EventHandlerConfig{
			OnSuccess: "out",
		},
		deferredACK: true,
	})

	task := &a2a.Task{
		Status: a2a.TaskStatus{
			State: "completed",
			Message: &a2a.Message{
				Role:  "agent",
				Parts: []a2a.Part{a2a.NewTextPart("success")},
			},
		},
	}
	a.handleFinalResponse(context.Background(), "task-6", task)

	if a.heldCount(t) != 0 {
		t.Errorf("pending deferred ACKs = %d, want 0", a.heldCount(t))
	}

	msgs := mockBrk.published()
	if len(msgs) != 1 {
		t.Fatalf("published %d messages, want 1", len(msgs))
	}
	if msgs[0].Topic != "result/topic" {
		t.Errorf("topic = %q", msgs[0].Topic)
	}
}

func TestHandleFinalResponse_NoOnSuccess_DeferredACK(t *testing.T) {
	a := newTestAdapter(nil, nil)

	msg := &broker.Message{Payload: []byte("orig")}
	a.holdFor(t, "task-7", msg, AckFailurePolicy{Action: "nack"})

	a.seedPending(t, "task-7", &pendingTask{
		handlerCfg:  &EventHandlerConfig{},
		deferredACK: true,
	})

	task := &a2a.Task{Status: a2a.TaskStatus{State: "completed"}}
	a.handleFinalResponse(context.Background(), "task-7", task)

	if a.heldCount(t) != 0 {
		t.Errorf("pending deferred ACKs = %d, want 0", a.heldCount(t))
	}
}

func TestHandleFinalResponse_MissingOutputHandler_DeferredACK(t *testing.T) {
	a := newTestAdapter(nil, map[string]*OutputHandlerConfig{})

	msg := &broker.Message{Payload: []byte("orig")}
	a.holdFor(t, "task-8", msg, AckFailurePolicy{Action: "nack"})

	a.seedPending(t, "task-8", &pendingTask{
		handlerCfg: &EventHandlerConfig{
			OnSuccess: "nonexistent",
		},
		deferredACK: true,
	})

	task := &a2a.Task{Status: a2a.TaskStatus{State: "completed"}}
	a.handleFinalResponse(context.Background(), "task-8", task)

	if a.heldCount(t) != 0 {
		t.Errorf("pending deferred ACKs = %d, want 0", a.heldCount(t))
	}
}

func TestConcurrentSuccessAndErrorResponse_ExactlyOnePublishes(t *testing.T) {
	handlers := []*EventHandlerConfig{{Name: "h1", OnSuccess: "ok", OnError: "err"}}
	outputHandlers := map[string]*OutputHandlerConfig{
		"ok":  {Name: "ok", TopicExpression: "static:result/topic", PayloadExpression: "task_response:text", PayloadFormat: "text"},
		"err": {Name: "err", TopicExpression: "static:errors", PayloadExpression: "input.payload:a2a_task_response", PayloadFormat: "json"},
	}
	task := &a2a.Task{
		Status: a2a.TaskStatus{
			State:   "completed",
			Message: &a2a.Message{Role: "agent", Parts: []a2a.Part{a2a.NewTextPart("the answer")}},
		},
	}
	a2aErr := &a2a.Error{Code: -32000, Message: "timed out"}

	const trials = 200
	for i := 0; i < trials; i++ {
		mockBrk := &mockPublishBroker{}
		a := newTestAdapter(handlers, outputHandlers)
		a.dataBroker = mockBrk
		a.seedPending(t, "task-1", &pendingTask{handlerCfg: handlers[0]})

		var wg sync.WaitGroup
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			a.handleFinalResponse(t.Context(), "task-1", task)
		}()
		go func() {
			defer wg.Done()
			<-start
			a.handleErrorResponse(t.Context(), "task-1", a2aErr)
		}()
		close(start)
		wg.Wait()

		if n := len(mockBrk.published()); n != 1 {
			t.Fatalf("trial %d: published %d messages, want exactly 1 — success and the cancel/error path raced for the same task", i, n)
		}
	}
}

func TestHandleFinalResponse_UnknownTask_PublishesNothing(t *testing.T) {
	mockBrk := &mockPublishBroker{}
	a := newTestAdapter(nil, map[string]*OutputHandlerConfig{
		"out": {Name: "out", TopicExpression: "static:result/topic", PayloadExpression: "task_response:text", PayloadFormat: "text"},
	})
	a.dataBroker = mockBrk

	task := &a2a.Task{Status: a2a.TaskStatus{State: "completed"}}
	a.handleFinalResponse(t.Context(), "never-seen", task)

	if msgs := mockBrk.published(); len(msgs) != 0 {
		t.Fatalf("published %d messages for a fully unknown task, want 0", len(msgs))
	}
}
