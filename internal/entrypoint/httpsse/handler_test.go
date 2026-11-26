// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	gwauth "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/google/uuid"
)

func setupTestAdapter(t *testing.T) (*HTTPSSEAdapter, *entrypoint.GatewayCore, broker.Broker) {
	t.Helper()

	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	adapter := NewHTTPSSEAdapter()

	appConfig := map[string]any{
		"namespace":             "test-ns",
		"session_secret_key":    "test-secret",
		"gateway_id":            "test-gw",
		"fastapi_host":          "127.0.0.1",
		"fastapi_port":          0,
		"default_user_identity": "sam_dev_user",
	}
	core := entrypoint.NewGatewayCore(adapter, appConfig)

	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()))
	core.SetServices(rt.Services())

	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatal(err)
	}

	return adapter, core, brk
}

func TestHTTPSSEAdapter_InitMarksServesWebUI(t *testing.T) {
	_, core, _ := setupTestAdapter(t)
	if !core.ServesWebUI() {
		t.Error("httpsse adapter Init did not mark the core as serving the web UI")
	}
}

func TestGatewayCore_ServesWebUIIsNotTheDefault(t *testing.T) {
	core := entrypoint.NewGatewayCore(NewHTTPSSEAdapter(), map[string]any{"namespace": "test-ns"})
	if core.ServesWebUI() {
		t.Error("a core marks itself as serving the web UI before Init; the marker would be meaningless")
	}
}

func TestHandleHealth(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health", nil)
	handleHealth(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["status"] != "A2A Web UI Backend is running" {
		t.Errorf("unexpected status: %s", resp["status"])
	}
}

func TestHandleMessage_InvalidJSON(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)

	mux := http.NewServeMux()
	msgHandler := &messageHandler{core: adapter.core, sseManager: adapter.sseManager}
	mux.HandleFunc("POST /api/v1/message:stream", msgHandler.handleMessageStream)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/message:stream", strings.NewReader("not json"))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}

	var resp JSONRPCResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Code != codeParseError {
		t.Errorf("expected parse error code %d, got %v", codeParseError, resp.Error)
	}
}

func TestHandleMessage_MissingAgentName(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)

	msgHandler := &messageHandler{core: adapter.core, sseManager: adapter.sseManager}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/message:stream", msgHandler.handleMessageStream)

	body := `{"jsonrpc":"2.0","id":"req1","params":{"message":{"kind":"message","role":"user","parts":[{"kind":"text","text":"hello"}]}}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/message:stream", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}

	var resp JSONRPCResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error == nil || resp.Error.Code != codeInvalidParams {
		t.Errorf("expected invalid params error, got %v", resp.Error)
	}
}

func TestHandleMessage_AgentNotFound(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)

	msgHandler := &messageHandler{core: adapter.core, sseManager: adapter.sseManager}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/message:stream", msgHandler.handleMessageStream)

	body := `{"jsonrpc":"2.0","id":"req1","params":{"message":{"kind":"message","role":"user","parts":[{"kind":"text","text":"hello"}],"metadata":{"agent_name":"nonexistent"}}}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/message:stream", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestHandleMessage_Success(t *testing.T) {
	adapter, core, _ := setupTestAdapter(t)

	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
		Name: "test-agent",
		URL:  "solace:test",
	})

	msgHandler := &messageHandler{core: core, sseManager: adapter.sseManager}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/message:stream", msgHandler.handleMessageStream)

	body := `{"jsonrpc":"2.0","id":"req1","params":{"message":{"kind":"message","role":"user","parts":[{"kind":"text","text":"hello"}],"metadata":{"agent_name":"test-agent"}}}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/message:stream", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var resp JSONRPCResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var taskResult TaskResult
	if err := json.Unmarshal(resultBytes, &taskResult); err != nil {
		t.Fatal(err)
	}
	if taskResult.ID == "" {
		t.Error("expected non-empty task ID")
	}
	if taskResult.Kind != "task" {
		t.Errorf("expected kind=task, got %s", taskResult.Kind)
	}
	if taskResult.ContextID == "" {
		t.Error("expected non-empty context ID")
	}

	if !adapter.sseManager.HasTask(taskResult.ID) {
		t.Error("expected task to be registered in SSE manager")
	}
}

func TestHandleMessage_ReturnsTraceID(t *testing.T) {
	adapter, core, _ := setupTestAdapter(t)

	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
		Name: "test-agent",
		URL:  "solace:test",
	})

	msgHandler := &messageHandler{core: core, sseManager: adapter.sseManager}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/message:stream", msgHandler.handleMessageStream)

	body := `{"jsonrpc":"2.0","id":"req1","params":{"message":{"kind":"message","role":"user","parts":[{"kind":"text","text":"hello"}],"metadata":{"agent_name":"test-agent"}}}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/message:stream", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Result TaskResult `json:"result"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Result.TraceID == "" {
		t.Fatal("expected traceId in the submit response")
	}
	if parsed, err := uuid.Parse(resp.Result.TraceID); err != nil || parsed.String() != resp.Result.TraceID {
		t.Errorf("traceId %q is not a canonical UUID", resp.Result.TraceID)
	}
	if got := core.TaskTraceID(resp.Result.ID); got != resp.Result.TraceID {
		t.Errorf("response traceId %q does not match registry traceId %q", resp.Result.TraceID, got)
	}
}

func TestTaskTraceID_UnknownTaskIsEmpty(t *testing.T) {
	_, core, _ := setupTestAdapter(t)

	if got := core.TaskTraceID(idgen.New()); got != "" {
		t.Errorf("TaskTraceID(unknown) = %q, want empty", got)
	}
}

func TestHandleMessage_ResolvesDisplayName(t *testing.T) {
	adapter, core, _ := setupTestAdapter(t)

	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
		Name: "agent_019e1876_5f14_7f40_b463_b564c754128c",
		URL:  "solace:test",
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{{
				URI:    "https://solace.com/a2a/extensions/display-name",
				Params: map[string]any{"display_name": "Jira and Confluence"},
			}},
		},
	})

	msgHandler := &messageHandler{core: core, sseManager: adapter.sseManager}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/message:stream", msgHandler.handleMessageStream)

	body := `{"jsonrpc":"2.0","id":"req1","params":{"message":{"kind":"message","role":"user","parts":[{"kind":"text","text":"hello"}],"metadata":{"agent_name":"Jira and Confluence"}}}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/message:stream", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (display-name resolution), got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp JSONRPCResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	resultBytes, _ := json.Marshal(resp.Result)
	var taskResult TaskResult
	if err := json.Unmarshal(resultBytes, &taskResult); err != nil {
		t.Fatal(err)
	}
	if taskResult.ID == "" {
		t.Error("expected task to be created via display-name lookup")
	}
}

func TestHandleMessage_DelegationDenied(t *testing.T) {
	adapter, core, _ := setupTestAdapter(t)

	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
		Name: "restricted-agent",
		URL:  "solace:test",
	})

	msgHandler := &messageHandler{core: core, sseManager: adapter.sseManager}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/message:stream", msgHandler.handleMessageStream)

	identity := gwauth.NewTestSubject(gwauth.SubjectFields{
		UserID:        "alice",
		Authenticated: true,
		Scopes:        []string{"agent:other-agent:invoke"},
	})

	body := `{"jsonrpc":"2.0","id":"req1","params":{"message":{"kind":"message","role":"user","parts":[{"kind":"text","text":"hi"}],"metadata":{"agent_name":"restricted-agent"}}}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/message:stream", strings.NewReader(body))
	req = req.WithContext(gwauth.ContextWithSubject(req.Context(), identity))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d (body=%s)", rec.Code, rec.Body.String())
	}

	var resp JSONRPCResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected JSON-RPC error in body")
	}
	if !strings.Contains(resp.Error.Message, "restricted-agent") {
		t.Errorf("expected error message to name the agent, got %q", resp.Error.Message)
	}

	taskCount := adapter.sseManager.totalTasks()
	if taskCount != 0 {
		t.Errorf("denied request should clean up its SSE task entry, found %d", taskCount)
	}
}

func TestHandleHILRespond_DelegationDenied(t *testing.T) {
	_, core, _ := setupTestAdapter(t)

	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
		Name: "restricted-agent",
		URL:  "solace:test",
	})

	taskID := idgen.New()
	_ = core.Tasks().Register(taskID, "restricted-agent", "alice", entrypoint.TaskIdentity{})

	hil := &hilHandler{core: core, logger: slog.Default()}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/userInput/respond", hil.handleRespond)

	identity := gwauth.NewTestSubject(gwauth.SubjectFields{
		UserID:        "alice",
		Authenticated: true,
		Scopes:        []string{"agent:other-agent:invoke"},
	})

	body := `{"taskId":"` + taskID + `","agentName":"restricted-agent","requestId":"r1","status":"answered","data":{}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/userInput/respond", strings.NewReader(body))
	req = req.WithContext(gwauth.ContextWithSubject(req.Context(), identity))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "restricted-agent") {
		t.Errorf("expected response body to name the agent, got %q", rec.Body.String())
	}
}

func TestSSESubscribe_FullFlow(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)

	taskID := idgen.New()
	adapter.sseManager.RegisterTask(taskID, "", "")

	subHandler := &subscribeHandler{sseManager: adapter.sseManager, eventLog: adapter.eventLog}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sse/subscribe/{taskId}", subHandler.handleSubscribe)

	server := httptest.NewServer(mux)
	defer server.Close()

	go func() {
		time.Sleep(50 * time.Millisecond)
		adapter.sseManager.Publish(taskID, &SSEEvent{
			EventType: "status_update",
			Data:      []byte(`{"jsonrpc":"2.0","id":"test-task-sse","result":{"kind":"status-update"}}`),
		})
		time.Sleep(20 * time.Millisecond)
		adapter.sseManager.Publish(taskID, &SSEEvent{
			EventType: "final_response",
			Data:      []byte(`{"jsonrpc":"2.0","id":"test-task-sse","result":{"kind":"task"}}`),
			Final:     true,
		})
	}()

	resp, err := http.Get(server.URL + "/api/v1/sse/subscribe/" + taskID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Errorf("expected text/event-stream, got %s", resp.Header.Get("Content-Type"))
	}

	scanner := bufio.NewScanner(resp.Body)
	var events []sseFrame
	var currentEvent sseFrame

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, ": ") {
			continue
		}
		if strings.HasPrefix(line, "event: ") {
			currentEvent.eventType = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			currentEvent.data = strings.TrimPrefix(line, "data: ")
		} else if line == "" && currentEvent.eventType != "" {
			events = append(events, currentEvent)
			currentEvent = sseFrame{}
		}
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 SSE events, got %d", len(events))
	}
	if events[0].eventType != "status_update" {
		t.Errorf("expected first event type status_update, got %s", events[0].eventType)
	}
	if events[1].eventType != "final_response" {
		t.Errorf("expected second event type final_response, got %s", events[1].eventType)
	}

	for _, evt := range events {
		var rpc JSONRPCResponse
		if err := json.Unmarshal([]byte(evt.data), &rpc); err != nil {
			t.Errorf("event data is not valid JSON-RPC: %s", evt.data)
		}
	}
}

func TestSSESubscribe_TaskNotFound(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)

	subHandler := &subscribeHandler{sseManager: adapter.sseManager, eventLog: adapter.eventLog}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sse/subscribe/{taskId}", subHandler.handleSubscribe)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sse/subscribe/"+idgen.New(), nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestSSESubscribe_BufferedReplay(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)

	taskID := idgen.New()
	adapter.sseManager.RegisterTask(taskID, "", "")

	adapter.OnStatusUpdate(context.Background(), taskID, nil, []byte(`{"buffered":1}`))
	adapter.OnFinalResponse(context.Background(), taskID, nil, []byte(`{"buffered":2}`))

	subHandler := &subscribeHandler{sseManager: adapter.sseManager, eventLog: adapter.eventLog}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sse/subscribe/{taskId}", subHandler.handleSubscribe)

	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/v1/sse/subscribe/" + taskID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = buf.ReadFrom(resp.Body)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out reading SSE response")
	}

	body := buf.String()
	if !strings.Contains(body, "event: status_update") {
		t.Error("expected buffered status_update event")
	}
	if !strings.Contains(body, "event: final_response") {
		t.Error("expected buffered final_response event")
	}
}

func TestSSESubscribe_OnErrorEmitsFinalResponseFrame(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)

	taskID := idgen.New()
	adapter.sseManager.RegisterTask(taskID, "", "")

	errPayload := []byte(`{"jsonrpc":"2.0","id":"test-onerror-wire","error":{"code":-32603,"message":"LLM error: model alias \"planning\" unbound"}}`)
	adapter.OnError(context.Background(), taskID, &a2a.Error{Code: -32603, Message: "unbound alias"}, errPayload)

	subHandler := &subscribeHandler{sseManager: adapter.sseManager, eventLog: adapter.eventLog}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sse/subscribe/{taskId}", subHandler.handleSubscribe)

	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/v1/sse/subscribe/" + taskID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	frames := parseSSEFrames(t, resp, 2*time.Second)
	if len(frames) != 1 {
		t.Fatalf("expected 1 SSE frame, got %d: %#v", len(frames), frames)
	}
	got := frames[0]
	if got.eventType != "final_response" {
		t.Errorf("SSE event name must be 'final_response' (matching Python SAM) to avoid collision with EventSource's built-in error event; got %q", got.eventType)
	}
	if got.eventType == "error" {
		t.Error("regression: OnError must not emit `event: error` — it triggers the frontend's onerror handler and shows 'Connection lost'")
	}
	if !strings.Contains(got.data, `"error"`) || !strings.Contains(got.data, "unbound") {
		t.Errorf("expected JSON-RPC error payload in SSE data, got %q", got.data)
	}
}

type sseFrame struct {
	id        string
	eventType string
	data      string
}

func parseSSEFrames(t *testing.T, resp *http.Response, timeout time.Duration) []sseFrame {
	t.Helper()
	frames, closed := scanSSEFrames(t, resp, 0, timeout)
	if !closed {
		t.Fatal("timed out reading SSE frames")
	}
	return frames
}

func TestSSEReconnect_FullFlow(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)

	taskID := idgen.New()
	el := adapter.eventLog

	ctx := context.Background()
	for range 3 {
		seq, _ := el.Append(ctx, taskID, "", "", "status_update", []byte(`{"jsonrpc":"2.0","result":{"kind":"status-update"}}`))

		adapter.sseManager.RegisterTask(taskID, "", "")
		adapter.sseManager.Publish(taskID, &SSEEvent{
			EventType: "status_update",
			Data:      []byte(`{"jsonrpc":"2.0","result":{"kind":"status-update"}}`),
			Sequence:  seq,
		})
	}
	seq, _ := el.Append(ctx, taskID, "", "", "final_response", []byte(`{"jsonrpc":"2.0","result":{"kind":"task"}}`))
	_ = el.Complete(ctx, taskID)
	adapter.sseManager.Publish(taskID, &SSEEvent{
		EventType: "final_response",
		Data:      []byte(`{"jsonrpc":"2.0","result":{"kind":"task"}}`),
		Sequence:  seq,
		Final:     true,
	})

	subHandler := &subscribeHandler{sseManager: adapter.sseManager, eventLog: adapter.eventLog}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sse/subscribe/{taskId}", subHandler.handleSubscribe)

	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/v1/sse/subscribe/" + taskID + "?reconnect=true&lastSequence=2")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Errorf("expected text/event-stream, got %s", resp.Header.Get("Content-Type"))
	}

	frames := parseSSEFrames(t, resp, 3*time.Second)

	if len(frames) != 2 {
		t.Fatalf("expected 2 events (seq 3,4), got %d", len(frames))
	}

	if frames[0].id != "3" {
		t.Errorf("first event id=%s, want 3", frames[0].id)
	}
	if frames[0].eventType != "status_update" {
		t.Errorf("first event type=%s, want status_update", frames[0].eventType)
	}

	if frames[1].id != "4" {
		t.Errorf("second event id=%s, want 4", frames[1].id)
	}
	if frames[1].eventType != "final_response" {
		t.Errorf("second event type=%s, want final_response", frames[1].eventType)
	}
}

func TestSSEReconnect_InvalidSequence(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)

	subHandler := &subscribeHandler{sseManager: adapter.sseManager, eventLog: adapter.eventLog}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sse/subscribe/{taskId}", subHandler.handleSubscribe)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sse/subscribe/some-task?reconnect=true&lastSequence=abc", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestSSEReconnect_LastEventIDHeader(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	taskID := idgen.New()
	el := adapter.eventLog
	ctx := context.Background()
	adapter.sseManager.RegisterTask(taskID, "", "")

	for range 3 {
		seq, _ := el.Append(ctx, taskID, "", "", "status_update", []byte(`{"jsonrpc":"2.0","result":{"kind":"status-update"}}`))
		adapter.sseManager.Publish(taskID, &SSEEvent{EventType: "status_update", Data: []byte(`{"jsonrpc":"2.0","result":{"kind":"status-update"}}`), Sequence: seq})
	}
	seq, _ := el.Append(ctx, taskID, "", "", "final_response", []byte(`{"jsonrpc":"2.0","result":{"kind":"task"}}`))
	_ = el.Complete(ctx, taskID)
	adapter.sseManager.Publish(taskID, &SSEEvent{EventType: "final_response", Data: []byte(`{"jsonrpc":"2.0","result":{"kind":"task"}}`), Sequence: seq, Final: true})

	subHandler := &subscribeHandler{sseManager: adapter.sseManager, eventLog: adapter.eventLog}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sse/subscribe/{taskId}", subHandler.handleSubscribe)
	server := httptest.NewServer(mux)
	defer server.Close()

	req, err := http.NewRequest("GET", server.URL+"/api/v1/sse/subscribe/"+taskID, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Last-Event-ID", "2")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	frames := parseSSEFrames(t, resp, 3*time.Second)
	if len(frames) != 2 {
		t.Fatalf("expected 2 events after Last-Event-ID=2 (seq 3,4), got %d", len(frames))
	}
	if frames[0].id != "3" || frames[1].id != "4" {
		t.Errorf("expected ids [3,4], got [%s,%s]", frames[0].id, frames[1].id)
	}
}

func TestSeqAfter_GuardsOverflow(t *testing.T) {
	if got := seqAfter(5); got != 6 {
		t.Errorf("seqAfter(5) = %d, want 6", got)
	}
	if got := seqAfter(math.MaxInt64); got != math.MaxInt64 {
		t.Errorf("seqAfter(MaxInt64) = %d, want MaxInt64 (no negative wrap)", got)
	}
	if got := seqAfter(math.MaxInt64 - 1); got != math.MaxInt64 {
		t.Errorf("seqAfter(MaxInt64-1) = %d, want MaxInt64", got)
	}
}

func TestSSEReconnect_TaskNotFound(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)

	subHandler := &subscribeHandler{sseManager: adapter.sseManager, eventLog: adapter.eventLog}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sse/subscribe/{taskId}", subHandler.handleSubscribe)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sse/subscribe/"+idgen.New()+"?reconnect=true&lastSequence=5", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestSSE_SequenceIDInNormalFlow(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)

	taskID := idgen.New()
	adapter.sseManager.RegisterTask(taskID, "", "")

	subHandler := &subscribeHandler{sseManager: adapter.sseManager, eventLog: adapter.eventLog}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sse/subscribe/{taskId}", subHandler.handleSubscribe)

	server := httptest.NewServer(mux)
	defer server.Close()

	go func() {
		time.Sleep(50 * time.Millisecond)
		adapter.OnStatusUpdate(context.Background(), taskID, nil, []byte(`{"seq":1}`))
		time.Sleep(20 * time.Millisecond)
		adapter.OnFinalResponse(context.Background(), taskID, nil, []byte(`{"seq":2}`))
	}()

	resp, err := http.Get(server.URL + "/api/v1/sse/subscribe/" + taskID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	frames := parseSSEFrames(t, resp, 3*time.Second)

	if len(frames) != 2 {
		t.Fatalf("expected 2 events, got %d", len(frames))
	}

	if frames[0].id != "1" {
		t.Errorf("first event id=%q, want \"1\"", frames[0].id)
	}
	if frames[1].id != "2" {
		t.Errorf("second event id=%q, want \"2\"", frames[1].id)
	}
}

func TestAdapterEventLog_Integration(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)

	if adapter.eventLog == nil {
		t.Fatal("expected eventLog to be initialized")
	}

	_ = adapter.eventLog //nolint:staticcheck // intentional interface check
}
