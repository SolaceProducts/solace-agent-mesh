// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type toolStatusHarness struct {
	agent  *Agent
	sp     *StatusPublisher
	broker broker.Broker
	queue  broker.Queue
}

func newToolStatusHarness(t *testing.T, taskID string) *toolStatusHarness {
	t.Helper()
	agent, _, _ := newTestAgent()

	mb := broker.NewDevBroker()
	if err := mb.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mb.Disconnect(t.Context()) })

	svc := agent.svc.(*mockServices)
	svc.brk = mb

	statusTopic := "test-ns/a2a/v1/agent/status/test-agent/" + taskID
	sp := NewStatusPublisher(mb, StatusPublisherConfig{StatusTopic: statusTopic, TaskID: taskID, ContextID: "ctx-" + taskID, AgentName: "test-agent", ClientID: "client-1", UserID: "user-1"}, slog.Default())

	q, err := mb.CreateQueue("queue-"+taskID, broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := mb.Subscribe(q, statusTopic); subErr != nil {
		t.Fatal(subErr)
	}
	return &toolStatusHarness{agent: agent, sp: sp, broker: mb, queue: q}
}

func (h *toolStatusHarness) registerTool(t *testing.T, name string, execFn stubExecFn) {
	t.Helper()
	if err := h.agent.Tools().Register(&testBuiltinTool{
		name:        name,
		description: "stub tool for dispatch-status tests",
		execFn:      execFn,
	}); err != nil {
		t.Fatalf("register %q: %v", name, err)
	}
}

func (h *toolStatusHarness) dispatch(ctx context.Context, calls []llm.ToolCall, parallelGroupID string) []*tool.Result {
	tCtx := &tool.Context{
		AgentName:       h.agent.cfg.Name,
		UserID:          "user-1",
		SessionID:       "ctx-1",
		ArtifactService: &artifact.ScopedService{Inner: memory.New()},
	}
	return h.agent.dispatchToolCallsWithStatus(ctx, calls, tCtx, h.sp, parallelGroupID)
}

func TestDispatchToolCallsWithStatus_SingleInProcessTool(t *testing.T) {
	h := newToolStatusHarness(t, "task-single-1")
	h.registerTool(t, "echo", func(_ context.Context, args map[string]any, _ *tool.Context) (*tool.Result, error) {
		return tool.OK("echo:" + args["msg"].(string)), nil
	})

	results := h.dispatch(t.Context(), []llm.ToolCall{
		{ID: "call-A", Name: "echo", Arguments: `{"msg":"hello"}`},
	}, "")

	if len(results) != 1 || results[0] == nil || results[0].Status == tool.StatusError {
		t.Fatalf("dispatch results = %+v, want one OK result", results)
	}

	signals := drainStatusSignals(t, h.broker, h.queue, 2)

	inv := signals[0]
	if inv["type"] != "tool_invocation_start" {
		t.Errorf("signal[0].type = %v, want tool_invocation_start", inv["type"])
	}
	if inv["tool_name"] != "echo" {
		t.Errorf("invocation.tool_name = %v, want echo", inv["tool_name"])
	}
	if inv["function_call_id"] != "call-A" {
		t.Errorf("invocation.function_call_id = %v, want call-A", inv["function_call_id"])
	}
	if inv["parallel_group_id"] != nil {
		t.Errorf("invocation.parallel_group_id = %v, want null for solo tool call", inv["parallel_group_id"])
	}
	args, ok := inv["tool_args"].(map[string]any)
	if !ok {
		t.Fatalf("invocation.tool_args missing/wrong shape: %+v", inv)
	}
	if args["msg"] != "hello" {
		t.Errorf("invocation.tool_args.msg = %v, want hello", args["msg"])
	}

	res := signals[1]
	if res["type"] != "tool_result" {
		t.Errorf("signal[1].type = %v, want tool_result", res["type"])
	}
	if res["tool_name"] != "echo" {
		t.Errorf("result.tool_name = %v, want echo", res["tool_name"])
	}
	if res["function_call_id"] != "call-A" {
		t.Errorf("result.function_call_id = %v, want call-A (must match invocation)", res["function_call_id"])
	}
	if res["result_data"] != "echo:hello" {
		t.Errorf("result.result_data = %v, want echo:hello", res["result_data"])
	}
}

func TestDispatchToolCallsWithStatus_ParallelToolsShareGroupID(t *testing.T) {
	h := newToolStatusHarness(t, "task-parallel-1")
	h.registerTool(t, "tool_a", func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
		return tool.OK("a"), nil
	})
	h.registerTool(t, "tool_b", func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
		return tool.OK("b"), nil
	})

	groupID := "llm_batch_test1234"
	results := h.dispatch(t.Context(), []llm.ToolCall{
		{ID: "call-A", Name: "tool_a", Arguments: `{}`},
		{ID: "call-B", Name: "tool_b", Arguments: `{}`},
	}, groupID)
	if len(results) != 2 {
		t.Fatalf("results len = %d, want 2", len(results))
	}

	signals := drainStatusSignals(t, h.broker, h.queue, 4)
	var invocations, toolResults []map[string]any
	for _, s := range signals {
		switch s["type"] {
		case "tool_invocation_start":
			invocations = append(invocations, s)
		case "tool_result":
			toolResults = append(toolResults, s)
		default:
			t.Errorf("unexpected signal type: %v", s["type"])
		}
	}
	if len(invocations) != 2 {
		t.Fatalf("got %d invocation events, want 2", len(invocations))
	}
	if len(toolResults) != 2 {
		t.Fatalf("got %d result events, want 2", len(toolResults))
	}

	for _, inv := range invocations {
		if inv["parallel_group_id"] != groupID {
			t.Errorf("invocation %v parallel_group_id = %v, want %q",
				inv["function_call_id"], inv["parallel_group_id"], groupID)
		}
	}

	invIDs := []string{
		invocations[0]["function_call_id"].(string),
		invocations[1]["function_call_id"].(string),
	}
	resIDs := []string{
		toolResults[0]["function_call_id"].(string),
		toolResults[1]["function_call_id"].(string),
	}
	sort.Strings(invIDs)
	sort.Strings(resIDs)
	if invIDs[0] != "call-A" || invIDs[1] != "call-B" {
		t.Errorf("invocation IDs = %v, want [call-A call-B]", invIDs)
	}
	if resIDs[0] != "call-A" || resIDs[1] != "call-B" {
		t.Errorf("result IDs = %v, want [call-A call-B] (each invocation must pair with one result)", resIDs)
	}
}

func TestDispatchToolCallsWithStatus_ErrorPathStillEmitsResult(t *testing.T) {
	h := newToolStatusHarness(t, "task-err-1")
	h.registerTool(t, "boom", func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
		return nil, errors.New("kaboom")
	})

	results := h.dispatch(t.Context(), []llm.ToolCall{
		{ID: "call-X", Name: "boom", Arguments: `{}`},
	}, "")

	if len(results) != 1 || results[0] == nil {
		t.Fatalf("results = %+v, want one error result", results)
	}
	if results[0].Status != tool.StatusError {
		t.Errorf("result.Status = %v, want StatusError", results[0].Status)
	}

	signals := drainStatusSignals(t, h.broker, h.queue, 2)
	if signals[0]["type"] != "tool_invocation_start" {
		t.Errorf("signal[0].type = %v, want tool_invocation_start", signals[0]["type"])
	}
	if signals[1]["type"] != "tool_result" {
		t.Errorf("signal[1].type = %v, want tool_result (error path must still emit)", signals[1]["type"])
	}
	if signals[1]["function_call_id"] != "call-X" {
		t.Errorf("error result.function_call_id = %v, want call-X", signals[1]["function_call_id"])
	}
	resData, ok := signals[1]["result_data"].(map[string]any)
	if !ok {
		t.Fatalf("error result.result_data should be a status/message map, got %T: %v", signals[1]["result_data"], signals[1]["result_data"])
	}
	if resData["status"] != string(tool.StatusError) {
		t.Errorf("result_data.status = %v, want error", resData["status"])
	}
	if msg, _ := resData["message"].(string); msg == "" {
		t.Errorf("error result.result_data.message should carry the error text, got %v", resData["message"])
	}
}

func TestDispatchToolCallsWithStatus_DispatchFailureCarriesClosedCode(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"deadline", fmt.Errorf("remote call: %w", context.DeadlineExceeded), "DISPATCH_TIMEOUT"},
		{"backend 5xx", errors.New("upstream MCP: HTTP 503 Service Unavailable"), "DISPATCH_BACKEND_5XX"},
		{"unclassified", errors.New("kaboom"), "DISPATCH_INTERNAL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newToolStatusHarness(t, "task-dispatch-code-"+strings.ReplaceAll(tc.name, " ", "-"))
			h.registerTool(t, "boom", func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
				return nil, tc.err
			})

			results := h.dispatch(t.Context(), []llm.ToolCall{{ID: "call-D", Name: "boom", Arguments: `{}`}}, "")
			if len(results) != 1 || results[0] == nil || results[0].ErrorCode != tc.want {
				t.Fatalf("results = %+v, want one result with ErrorCode %s", results, tc.want)
			}

			signals := drainStatusSignals(t, h.broker, h.queue, 2)
			resData, _ := signals[1]["result_data"].(map[string]any)
			if resData["error_code"] != tc.want || resData["status"] != tool.StatusError {
				t.Errorf("result_data = %#v, want status error and error_code %s", resData, tc.want)
			}
		})
	}
}

func TestDispatchToolCallsWithStatus_ToolFailureCarriesItsErrorCode(t *testing.T) {
	h := newToolStatusHarness(t, "task-tool-code-1")
	h.registerTool(t, "limited", func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
		return tool.Error("slow down", tool.WithErrorCode("RATE_LIMITED")), nil
	})

	h.dispatch(t.Context(), []llm.ToolCall{{ID: "call-R", Name: "limited", Arguments: `{}`}}, "")

	signals := drainStatusSignals(t, h.broker, h.queue, 2)
	resData, _ := signals[1]["result_data"].(map[string]any)
	if resData["error_code"] != "RATE_LIMITED" {
		t.Errorf("result_data = %#v, want error_code RATE_LIMITED", resData)
	}
}

func TestDispatchToolCallsWithStatus_HILGateDeniesWhenCannotPrompt(t *testing.T) {
	h := newToolStatusHarness(t, "task-hil-deny-1")

	var called bool
	h.registerTool(t, "danger", func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
		called = true
		return tool.OK("ran"), nil
	})

	h.agent.SetHILToolConfigs(map[string]HILToolConfig{
		"danger": {RequireApproval: true},
	})

	results := h.dispatch(t.Context(), []llm.ToolCall{
		{ID: "call-D", Name: "danger", Arguments: `{}`},
	}, "")

	if called {
		t.Fatal("gated tool was dispatched on a gateway that can't prompt — it must be denied for safety, not run")
	}
	if len(results) != 1 || results[0] == nil || results[0].Status != tool.StatusError {
		t.Fatalf("results = %+v, want one StatusError denial", results)
	}
	if !strings.Contains(results[0].Message, "approval") {
		t.Errorf("denial message = %q, want it to mention approval", results[0].Message)
	}

	signals := drainStatusSignals(t, h.broker, h.queue, 2)
	if signals[1]["type"] != "tool_result" {
		t.Errorf("signal[1].type = %v, want tool_result for the denial", signals[1]["type"])
	}
}

func TestDispatchToolCallsWithStatus_DataObjectContentReachesWire(t *testing.T) {
	const rows = `[{"region":"north","total_orders":1204}]`

	h := newToolStatusHarness(t, "task-dataobject-1")
	h.registerTool(t, "sales_sql_query", func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
		return tool.OK("Query returned 28 rows", tool.WithDataObjects(tool.DataObject{
			Name:        "sql_query_result.json",
			Content:     []byte(rows),
			MIMEType:    "application/json",
			Disposition: tool.DispositionInline,
		})), nil
	})

	h.dispatch(t.Context(), []llm.ToolCall{
		{ID: "call-SQL", Name: "sales_sql_query", Arguments: `{"query":"SELECT 1"}`},
	}, "")

	signals := drainStatusSignals(t, h.broker, h.queue, 2)
	res := signals[1]
	if res["type"] != "tool_result" {
		t.Fatalf("signal[1].type = %v, want tool_result", res["type"])
	}
	data, ok := res["result_data"].(map[string]any)
	if !ok {
		t.Fatalf("result_data = %T (%v), want a map carrying the DataObject content", res["result_data"], res["result_data"])
	}
	if data["content"] != rows {
		t.Errorf("result_data.content = %v, want the rows JSON", data["content"])
	}
	if data["message"] != "Query returned 28 rows" {
		t.Errorf("result_data.message = %v, want the summary to survive", data["message"])
	}
}

func TestDispatchToolCallsWithStatus_RedactsCredentialsButDispatchesThem(t *testing.T) {
	h := newToolStatusHarness(t, "task-redact-1")

	var received map[string]any
	h.registerTool(t, "fetch", func(_ context.Context, args map[string]any, _ *tool.Context) (*tool.Result, error) {
		received = args
		return tool.OK("fetched"), nil
	})

	calls := []llm.ToolCall{{
		ID:        "call-R",
		Name:      "fetch",
		Arguments: `{"url":"https://api.example.com/v1?api_key=sk-live-4242&q=weather","headers":{"Authorization":"Bearer sk-live-9999"}}`,
	}}
	results := h.dispatch(t.Context(), calls, "")
	if len(results) != 1 || results[0] == nil || results[0].Status == tool.StatusError {
		t.Fatalf("dispatch results = %+v, want one OK result", results)
	}

	headers, ok := received["headers"].(map[string]any)
	if !ok {
		t.Fatalf("tool received no headers map: %+v", received)
	}
	if headers["Authorization"] != "Bearer sk-live-9999" {
		t.Errorf("tool received Authorization = %v, want the real credential", headers["Authorization"])
	}
	if got, _ := received["url"].(string); !strings.Contains(got, "api_key=sk-live-4242") {
		t.Errorf("tool received url = %q, want the real query intact", got)
	}
	signals := drainStatusSignals(t, h.broker, h.queue, 2)
	inv := signals[0]
	if inv["type"] != "tool_invocation_start" {
		t.Fatalf("signal[0].type = %v, want tool_invocation_start", inv["type"])
	}
	published, err := json.Marshal(inv)
	if err != nil {
		t.Fatalf("marshal published signal: %v", err)
	}
	for _, secret := range []string{"sk-live-4242", "sk-live-9999"} {
		if strings.Contains(string(published), secret) {
			t.Errorf("published tool_invocation_start leaked %q: %s", secret, published)
		}
	}
	if !strings.Contains(string(published), "q=weather") {
		t.Errorf("published event dropped the non-credential query: %s", published)
	}
}

func TestDispatchToolCallsWithStatus_HILApprovalPublishesRedactedSurface(t *testing.T) {
	h := newToolStatusHarness(t, "task-hil-approve-1")

	var received map[string]any
	h.registerTool(t, "web_request", func(_ context.Context, args map[string]any, _ *tool.Context) (*tool.Result, error) {
		received = args
		return tool.OK("fetched"), nil
	})

	h.agent.SetHILToolConfigs(map[string]HILToolConfig{
		"web_request": {
			RequireApproval: true,
			ShowArgs:        true,
			ApprovalMessage: "Send to {{.url}}?",
			Timeout:         30 * time.Second,
		},
	})

	h.agent.svc.(*mockServices).featureRegistry = notifyEnabledRegistry(true)
	notifyQueue, err := h.broker.CreateQueue("notify-approval", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if err = h.broker.Subscribe(notifyQueue, a2a.NotifyRequestSubscription("test-ns")); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	tCtx := &tool.Context{
		AgentName:           h.agent.cfg.Name,
		UserID:              "user-1",
		SessionID:           "ctx-1",
		ArtifactService:     &artifact.ScopedService{Inner: memory.New()},
		GatewayCapabilities: map[string]bool{tool.CapInteractiveUserInput: true},
	}

	calls := []llm.ToolCall{{
		ID:        "call-A",
		Name:      "web_request",
		Arguments: `{"url":"https://api.example.com/v1?api_key=sk-live-4242&q=weather"}`,
	}}

	resCh := make(chan []*tool.Result, 1)
	go func() { resCh <- h.agent.dispatchToolCallsWithStatus(t.Context(), calls, tCtx, h.sp, "") }()

	signals := drainStatusSignals(t, h.broker, h.queue, 2)
	prompt := signals[1]
	if prompt["type"] != "user_input_request" {
		t.Fatalf("signal[1].type = %v, want user_input_request", prompt["type"])
	}
	if prompt["source"] != "tool_approval" {
		t.Errorf("source = %v, want tool_approval", prompt["source"])
	}
	requestID, _ := prompt["request_id"].(string)
	if !strings.HasPrefix(requestID, "hil-approval-") {
		t.Fatalf("request_id = %q, want a hil-approval- id", requestID)
	}

	notifyCtx, cancelNotify := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancelNotify()
	notifyMsg, err := h.broker.Receive(notifyCtx, notifyQueue)
	if err != nil {
		t.Fatalf("did not receive the approval NotifyRequest: %v", err)
	}
	var notifyReq a2a.NotifyRequest
	if err = json.Unmarshal(notifyMsg.Payload, &notifyReq); err != nil {
		t.Fatalf("unmarshal NotifyRequest: %v", err)
	}
	if notifyReq.RequestID != requestID || notifyReq.Metadata["session_id"] != tCtx.SessionID {
		t.Errorf("NotifyRequest requestID=%q session_id=%v, want %q and %q", notifyReq.RequestID, notifyReq.Metadata["session_id"], requestID, tCtx.SessionID)
	}

	surface, err := json.Marshal(prompt["surface"])
	if err != nil {
		t.Fatalf("marshal surface: %v", err)
	}
	for _, secret := range []string{"sk-live-4242"} {
		if strings.Contains(string(surface), secret) {
			t.Errorf("published approval surface leaked %q: %s", secret, surface)
		}
	}
	if !strings.Contains(string(surface), "q=weather") {
		t.Errorf("approval surface dropped the non-credential query: %s", surface)
	}

	if h.agent.deliverHILResponse(&a2a.UserInputResponseData{
		Type:      "user_input_response",
		RequestID: requestID,
		Status:    "answered",
		Data:      map[string]any{"approved": true},
	}, hilSenderUser(tCtx.UserID)) != hilDelivered {
		t.Fatal("deliverHILResponse found no pending request for the published id")
	}

	select {
	case results := <-resCh:
		if len(results) != 1 || results[0] == nil || results[0].Status == tool.StatusError {
			t.Fatalf("results = %+v, want one OK result after approval", results)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("dispatch did not return after the approval was delivered")
	}

	if got, _ := received["url"].(string); !strings.Contains(got, "sk-live-4242") {
		t.Errorf("approved tool received a redacted url: %q", got)
	}
}
