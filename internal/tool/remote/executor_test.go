// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

const (
	testNamespace = "ns"
	testAgent     = "test-agent"
)

func newTestExecutor(t *testing.T, opts ...ExecutorOption) (*Executor, *broker.DevBroker) {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defaults := []ExecutorOption{
		WithInitTimeout(2 * time.Second),
		WithInvokeBuffer(2 * time.Second),
	}
	allOpts := make([]ExecutorOption, 0, len(defaults)+len(opts))
	allOpts = append(allOpts, defaults...)
	allOpts = append(allOpts, opts...)
	exe := NewExecutor(brk, testNamespace, testAgent, allOpts...)
	return exe, brk
}

func startExecutor(t *testing.T, exe *Executor) {
	t.Helper()
	if err := exe.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { exe.Stop() })
}

func simulateSTR(t *testing.T, brk *broker.DevBroker, toolName string, makeResponse func(corrID, replyTo, statusTo string)) {
	t.Helper()
	q, err := brk.CreateQueue("str-"+toolName, broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	invokeTopic := remotetool.InvokeTopic(testNamespace, toolName)
	if err := brk.Subscribe(q, invokeTopic); err != nil {
		t.Fatal(err)
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		msg, recvErr := brk.Receive(ctx, q)
		if recvErr != nil {
			return
		}
		msg.Ack()

		corrID := msg.CorrelationID
		if corrID == "" {
			var req a2a.Request
			if jsonErr := json.Unmarshal(msg.Payload, &req); jsonErr == nil {
				corrID = req.ID
			}
		}
		replyTo := msg.UserProperties[remotetool.PropReplyTo]
		statusTo := msg.UserProperties[remotetool.PropStatusTo]
		makeResponse(corrID, replyTo, statusTo)
	}()
}

func TestExecutorInvokeStampsTTL(t *testing.T) {
	exe, brk := newTestExecutor(t)
	startExecutor(t, exe)

	const toolName = "ttl_tool"
	captureQ, err := brk.CreateQueue("capture-ttl", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = brk.Subscribe(captureQ, remotetool.InvokeTopic(testNamespace, toolName)); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const toolTimeoutSeconds = 7
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = exe.Invoke(ctx, "corr-ttl-1", &InvokeRequest{
			ToolName:       toolName,
			TimeoutSeconds: toolTimeoutSeconds,
			Args:           map[string]any{"q": "x"},
		}, nil)
	}()

	captureCtx, ccancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ccancel()
	msg, err := brk.Receive(captureCtx, captureQ)
	if err != nil {
		t.Fatalf("capture invoke message: %v", err)
	}
	msg.Ack()

	wantTTL := time.Duration(toolTimeoutSeconds)*time.Second + exe.InvokeBuffer()
	if msg.TTL != wantTTL {
		t.Errorf("invoke message TTL = %v, want %v", msg.TTL, wantTTL)
	}

	cancel()
	<-done
}

func TestExecutorStartStop(t *testing.T) {
	t.Parallel()
	exe, _ := newTestExecutor(t)
	startExecutor(t, exe)
	if err := exe.Start(context.Background()); err != nil {
		t.Errorf("second start should be no-op: %v", err)
	}
}

func TestExecutorInvokeNotStarted(t *testing.T) {
	t.Parallel()
	exe, _ := newTestExecutor(t)
	_, err := exe.Invoke(context.Background(), "corr-1", &InvokeRequest{
		ToolName:       "test",
		TimeoutSeconds: 1,
	}, nil)
	if err == nil {
		t.Fatal("expected error when not started")
	}
}

func TestExecutorInvokeSuccess(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t)
	startExecutor(t, exe)

	simulateSTR(t, brk, "echo", func(corrID, replyTo, _ string) {
		result := remotetool.InvokeResult{
			ToolResult:      map[string]any{"message": "hello"},
			ExecutionTimeMs: 42,
			TimedOut:        false,
		}
		resp := a2a.NewResponse(corrID, result)
		payload, _ := json.Marshal(resp)
		_ = brk.Publish(context.Background(), replyTo, &broker.Message{
			Payload: payload,
		})
	})

	resp, err := exe.Invoke(context.Background(), "corr-1", &InvokeRequest{
		ToolName:       "echo",
		Args:           map[string]any{"message": "hello"},
		AppName:        testAgent,
		UserID:         "user-1",
		SessionID:      "sess-1",
		TimeoutSeconds: 5,
		SandboxProfile: "standard",
	}, nil)

	if err != nil {
		t.Fatalf("invoke failed: %v", err)
	}
	if resp.Result.ExecutionTimeMs != 42 {
		t.Errorf("execution_time_ms = %d, want 42", resp.Result.ExecutionTimeMs)
	}
	msg, ok := resp.Result.ToolResult["message"]
	if !ok || msg != "hello" {
		t.Errorf("tool_result = %v", resp.Result.ToolResult)
	}
}

func TestExecutorInvokeError(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t)
	startExecutor(t, exe)

	simulateSTR(t, brk, "fail_tool", func(corrID, replyTo, _ string) {
		invokeErr := remotetool.InvokeError{
			Code:    remotetool.ErrExecutionError,
			Message: "ValueError: bad input",
		}
		errPayload, _ := json.Marshal(invokeErr)
		raw := struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      string          `json:"id"`
			Error   json.RawMessage `json:"error"`
		}{
			JSONRPC: "2.0",
			ID:      corrID,
			Error:   errPayload,
		}
		payload, _ := json.Marshal(raw)
		_ = brk.Publish(context.Background(), replyTo, &broker.Message{
			Payload: payload,
		})
	})

	_, err := exe.Invoke(context.Background(), "corr-2", &InvokeRequest{
		ToolName:       "fail_tool",
		TimeoutSeconds: 5,
	}, nil)

	if err == nil {
		t.Fatal("expected error")
	}
	invokeErr, ok := errors.AsType[*InvokeError](err)
	if !ok {
		t.Fatalf("expected InvokeError, got %T: %v", err, err)
	}
	if invokeErr.Code != remotetool.ErrExecutionError {
		t.Errorf("code = %q, want %q", invokeErr.Code, remotetool.ErrExecutionError)
	}
	if invokeErr.Retryable() {
		t.Error("ErrExecutionError should not be retryable")
	}
}

func TestExecutorInvokeRetryableError(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t)
	startExecutor(t, exe)

	simulateSTR(t, brk, "stale_tool", func(corrID, replyTo, _ string) {
		invokeErr := remotetool.InvokeError{
			Code:    remotetool.ErrToolNotAvailable,
			Message: "tool removed from manifest",
		}
		raw := struct {
			JSONRPC string                 `json:"jsonrpc"`
			ID      string                 `json:"id"`
			Error   remotetool.InvokeError `json:"error"`
		}{
			JSONRPC: "2.0",
			ID:      corrID,
			Error:   invokeErr,
		}
		payload, _ := json.Marshal(raw)
		_ = brk.Publish(context.Background(), replyTo, &broker.Message{
			Payload: payload,
		})
	})

	_, err := exe.Invoke(context.Background(), "corr-3", &InvokeRequest{
		ToolName:       "stale_tool",
		TimeoutSeconds: 5,
	}, nil)

	if err == nil {
		t.Fatal("expected error")
	}
	invokeErr, ok := errors.AsType[*InvokeError](err)
	if !ok {
		t.Fatalf("expected InvokeError, got %T: %v", err, err)
	}
	if !invokeErr.Retryable() {
		t.Error("TOOL_NOT_AVAILABLE should be retryable")
	}
}

func TestExecutorInvokeTimeout(t *testing.T) {
	t.Parallel()
	exe, _ := newTestExecutor(t)
	startExecutor(t, exe)

	start := time.Now()
	_, err := exe.Invoke(context.Background(), "corr-timeout", &InvokeRequest{
		ToolName:       "slow_tool",
		TimeoutSeconds: 1,
	}, nil)

	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed < 2*time.Second || elapsed > 10*time.Second {
		t.Errorf("expected timeout in ~3s, got %v", elapsed)
	}
}

func TestExecutorInvokeContextCancel(t *testing.T) {
	t.Parallel()
	exe, _ := newTestExecutor(t)
	startExecutor(t, exe)

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	_, err := exe.Invoke(ctx, "corr-cancel", &InvokeRequest{
		ToolName:       "slow_tool",
		TimeoutSeconds: 30,
	}, nil)

	if err == nil {
		t.Fatal("expected error on context cancel")
	}
	if !errors.Is(err, context.Canceled) {
		if _, ok := errors.AsType[*InvokeError](err); !ok {
			t.Fatalf("expected context.Canceled or InvokeError, got %T: %v", err, err)
		}
	}
}

func TestExecutorStatusCallback(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t)
	startExecutor(t, exe)

	simulateSTR(t, brk, "status_tool", func(corrID, replyTo, statusTo string) {
		statusMsg := a2a.Request{
			JSONRPC: "2.0",
			Method:  remotetool.MethodStatus,
			Params: remotetool.StatusParams{
				TaskID:     corrID,
				StatusText: "Processing...",
				Timestamp:  "2025-02-09T12:34:56.789Z",
			},
		}
		statusPayload, _ := json.Marshal(statusMsg)
		_ = brk.Publish(context.Background(), statusTo, &broker.Message{
			Payload: statusPayload,
		})

		time.Sleep(50 * time.Millisecond)

		result := remotetool.InvokeResult{
			ToolResult:      map[string]any{"done": true},
			ExecutionTimeMs: 100,
		}
		resp := a2a.NewResponse(corrID, result)
		payload, _ := json.Marshal(resp)
		_ = brk.Publish(context.Background(), replyTo, &broker.Message{
			Payload: payload,
		})
	})

	var mu sync.Mutex
	var statuses []string
	statusCb := func(taskID, text string) {
		mu.Lock()
		defer mu.Unlock()
		statuses = append(statuses, text)
	}

	_, err := exe.Invoke(context.Background(), "corr-status", &InvokeRequest{
		ToolName:       "status_tool",
		TimeoutSeconds: 5,
	}, statusCb)

	if err != nil {
		t.Fatalf("invoke failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(statuses) != 1 || statuses[0] != "Processing..." {
		t.Errorf("statuses = %v, want [Processing...]", statuses)
	}
}

func TestExecutorStopLeavesPendingInvokeToItsCaller(t *testing.T) {
	t.Parallel()
	exe, _ := newTestExecutor(t)
	startExecutor(t, exe)

	invokeCtx, cancelInvoke := context.WithCancel(context.Background())
	defer cancelInvoke()
	done := make(chan error, 1)
	go func() {
		_, err := exe.Invoke(invokeCtx, "corr-stop", &InvokeRequest{
			ToolName:       "no_response",
			TimeoutSeconds: 30,
		}, nil)
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)

	exe.Stop()

	select {
	case err := <-done:
		t.Fatalf("Stop answered the pending invoke: %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	cancelInvoke()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want the caller's cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the invoke did not end with its caller's context")
	}
}

func TestExecutorCreatedArtifacts(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t)
	startExecutor(t, exe)

	simulateSTR(t, brk, "artifact_tool", func(corrID, replyTo, _ string) {
		result := remotetool.InvokeResult{
			ToolResult:      map[string]any{"status": "success"},
			ExecutionTimeMs: 200,
			CreatedArtifacts: []remotetool.CreatedArtifact{
				{Filename: "report.pdf", Version: 0, MIMEType: "application/pdf", SizeBytes: 1024},
				{Filename: "data.csv", Version: 0, MIMEType: "text/csv", SizeBytes: 512},
			},
		}
		resp := a2a.NewResponse(corrID, result)
		payload, _ := json.Marshal(resp)
		_ = brk.Publish(context.Background(), replyTo, &broker.Message{
			Payload: payload,
		})
	})

	resp, err := exe.Invoke(context.Background(), "corr-art", &InvokeRequest{
		ToolName:       "artifact_tool",
		TimeoutSeconds: 5,
	}, nil)

	if err != nil {
		t.Fatalf("invoke failed: %v", err)
	}
	if len(resp.Result.CreatedArtifacts) != 2 {
		t.Fatalf("expected 2 artifacts, got %d", len(resp.Result.CreatedArtifacts))
	}
	if resp.Result.CreatedArtifacts[0].Filename != "report.pdf" {
		t.Errorf("artifact[0].filename = %q", resp.Result.CreatedArtifacts[0].Filename)
	}
	if resp.Result.CreatedArtifacts[1].MIMEType != "text/csv" {
		t.Errorf("artifact[1].mime_type = %q", resp.Result.CreatedArtifacts[1].MIMEType)
	}
}

func TestExecutorUserPropertiesOnInvoke(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t)
	startExecutor(t, exe)

	q, _ := brk.CreateQueue("inspector", broker.QueueOptions{Named: true})
	_ = brk.Subscribe(q, remotetool.InvokeTopic(testNamespace, "inspect_tool"))

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		msg, err := brk.Receive(ctx, q)
		if err != nil {
			return
		}
		msg.Ack()

		replyTo := msg.UserProperties[remotetool.PropReplyTo]
		var req a2a.Request
		_ = json.Unmarshal(msg.Payload, &req)

		result := remotetool.InvokeResult{ToolResult: map[string]any{}}
		resp := a2a.NewResponse(req.ID, result)
		payload, _ := json.Marshal(resp)
		_ = brk.Publish(context.Background(), replyTo, &broker.Message{Payload: payload})
	}()

	propsCh := make(chan map[string]string, 1)
	q2, _ := brk.CreateQueue("props-inspector", broker.QueueOptions{Named: true})
	_ = brk.Subscribe(q2, remotetool.InvokeTopic(testNamespace, "inspect_tool"))
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		msg, err := brk.Receive(ctx, q2)
		if err != nil {
			return
		}
		msg.Ack()
		propsCh <- msg.UserProperties
	}()

	_, err := exe.Invoke(context.Background(), "corr-props", &InvokeRequest{
		ToolName:       "inspect_tool",
		AppName:        testAgent,
		UserID:         "user-42",
		SessionID:      "sess-1",
		TimeoutSeconds: 5,
		AuthToken:      "jwt-abc",
	}, nil)
	if err != nil {
		t.Fatalf("invoke failed: %v", err)
	}

	select {
	case props := <-propsCh:
		if props[remotetool.PropClientID] != testAgent {
			t.Errorf("clientId = %q, want %q", props[remotetool.PropClientID], testAgent)
		}
		if props[remotetool.PropUserID] != "user-42" {
			t.Errorf("userId = %q", props[remotetool.PropUserID])
		}
		if props[remotetool.PropAuthToken] != "jwt-abc" {
			t.Errorf("authToken = %q", props[remotetool.PropAuthToken])
		}
		if props[remotetool.PropReplyTo] == "" {
			t.Error("replyTo should be set")
		}
		if props[remotetool.PropStatusTo] == "" {
			t.Error("statusTo should be set")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for user properties")
	}
}

func TestExecutorA2AUserConfigPropagation(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t)
	startExecutor(t, exe)

	q, _ := brk.CreateQueue("ucfg-inspector", broker.QueueOptions{Named: true})
	_ = brk.Subscribe(q, remotetool.InvokeTopic(testNamespace, "cfg_tool"))

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		msg, err := brk.Receive(ctx, q)
		if err != nil {
			return
		}
		msg.Ack()

		replyTo := msg.UserProperties[remotetool.PropReplyTo]
		var req a2a.Request
		_ = json.Unmarshal(msg.Payload, &req)

		result := remotetool.InvokeResult{ToolResult: map[string]any{}}
		resp := a2a.NewResponse(req.ID, result)
		payload, _ := json.Marshal(resp)
		_ = brk.Publish(context.Background(), replyTo, &broker.Message{Payload: payload})
	}()

	propsCh := make(chan map[string]string, 1)
	q2, _ := brk.CreateQueue("ucfg-props", broker.QueueOptions{Named: true})
	_ = brk.Subscribe(q2, remotetool.InvokeTopic(testNamespace, "cfg_tool"))
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		msg, err := brk.Receive(ctx, q2)
		if err != nil {
			return
		}
		msg.Ack()
		propsCh <- msg.UserProperties
	}()

	userConfig := `{"_enterprise_capabilities":["tool:*"],"model":"gpt-4"}`
	_, err := exe.Invoke(context.Background(), "corr-ucfg", &InvokeRequest{
		ToolName:       "cfg_tool",
		AppName:        testAgent,
		UserID:         "user-1",
		SessionID:      "sess-1",
		TimeoutSeconds: 5,
		AuthToken:      "jwt-xyz",
		A2AUserConfig:  userConfig,
	}, nil)
	if err != nil {
		t.Fatalf("invoke failed: %v", err)
	}

	select {
	case props := <-propsCh:
		if got := props[remotetool.PropA2AUserConfig]; got != userConfig {
			t.Errorf("a2aUserConfig = %q, want %q", got, userConfig)
		}
		if props[remotetool.PropAuthToken] != "jwt-xyz" {
			t.Errorf("authToken = %q", props[remotetool.PropAuthToken])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for user properties")
	}
}

func TestExecutorA2AUserConfigOmittedWhenEmpty(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t)
	startExecutor(t, exe)

	q, _ := brk.CreateQueue("empty-ucfg", broker.QueueOptions{Named: true})
	_ = brk.Subscribe(q, remotetool.InvokeTopic(testNamespace, "plain_tool"))

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		msg, err := brk.Receive(ctx, q)
		if err != nil {
			return
		}
		msg.Ack()
		replyTo := msg.UserProperties[remotetool.PropReplyTo]
		var req a2a.Request
		_ = json.Unmarshal(msg.Payload, &req)
		result := remotetool.InvokeResult{ToolResult: map[string]any{}}
		resp := a2a.NewResponse(req.ID, result)
		payload, _ := json.Marshal(resp)
		_ = brk.Publish(context.Background(), replyTo, &broker.Message{Payload: payload})
	}()

	propsCh := make(chan map[string]string, 1)
	q2, _ := brk.CreateQueue("empty-ucfg-props", broker.QueueOptions{Named: true})
	_ = brk.Subscribe(q2, remotetool.InvokeTopic(testNamespace, "plain_tool"))
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		msg, err := brk.Receive(ctx, q2)
		if err != nil {
			return
		}
		msg.Ack()
		propsCh <- msg.UserProperties
	}()

	_, err := exe.Invoke(context.Background(), "corr-empty", &InvokeRequest{
		ToolName:       "plain_tool",
		AppName:        testAgent,
		UserID:         "user-1",
		SessionID:      "sess-1",
		TimeoutSeconds: 5,
	}, nil)
	if err != nil {
		t.Fatalf("invoke failed: %v", err)
	}

	select {
	case props := <-propsCh:
		if v, exists := props[remotetool.PropA2AUserConfig]; exists && v != "" {
			t.Errorf("a2aUserConfig should be absent or empty, got %q", v)
		}
		if v, exists := props[remotetool.PropAuthToken]; exists && v != "" {
			t.Errorf("authToken should be absent or empty, got %q", v)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for user properties")
	}
}

func TestExtractCorrIDFromTopic(t *testing.T) {
	t.Parallel()
	tests := []struct {
		topic string
		want  string
	}{
		{"ns/a2a/v1/sam_remote_tool/response/agent/corr-123", "corr-123"},
		{"ns/a2a/v1/sam_remote_tool/status/agent/abc-def-ghi", "abc-def-ghi"},
		{"no-slash", ""},
		{"trailing/", ""},
	}
	for _, tt := range tests {
		got := extractCorrIDFromTopic(tt.topic)
		if got != tt.want {
			t.Errorf("extractCorrIDFromTopic(%q) = %q, want %q", tt.topic, got, tt.want)
		}
	}
}

func simulateSTRInitHandler(t *testing.T, brk *broker.DevBroker, initParams map[string]*remotetool.InitParams) {
	t.Helper()
	q, err := brk.CreateQueue("str-init-handler", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	initReqSub := remotetool.InitRequestSubscription(testNamespace)
	if err := brk.Subscribe(q, initReqSub); err != nil {
		t.Fatal(err)
	}

	go func() {
		for range len(initParams) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			msg, recvErr := brk.Receive(ctx, q)
			cancel()
			if recvErr != nil || msg == nil {
				return
			}
			msg.Ack()

			var req a2a.Request
			if jsonErr := json.Unmarshal(msg.Payload, &req); jsonErr != nil {
				continue
			}
			paramsData, _ := json.Marshal(req.Params)
			var irp remotetool.InitRequestParams
			_ = json.Unmarshal(paramsData, &irp)

			replyTo := msg.UserProperties[remotetool.PropReplyTo]
			ip, ok := initParams[irp.ToolName]
			if !ok {
				continue
			}

			resp := a2a.NewResponse(req.ID, ip)
			payload, _ := json.Marshal(resp)
			_ = brk.Publish(context.Background(), replyTo, &broker.Message{
				Payload:       payload,
				CorrelationID: req.ID,
			})
		}
	}()
}

func TestRequestToolSchemas_AllTools(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t)

	toolWithConfig := NewTool(&ToolConfig{
		ToolName:              "tool_with_config",
		Description:           "Has config",
		ToolPassthroughConfig: map[string]any{"key": "value"},
	}, exe)
	toolWithoutConfig := NewTool(&ToolConfig{
		ToolName:    "tool_without_config",
		Description: "No config",
	}, exe)
	exe.RegisterTool(toolWithConfig)
	exe.RegisterTool(toolWithoutConfig)

	simulateSTRInitHandler(t, brk, map[string]*remotetool.InitParams{
		"tool_with_config": {
			ToolName:     "tool_with_config",
			Instructions: "config-aware instructions",
			Parameters:   map[string]any{"properties": map[string]any{"x": map[string]any{"type": "string"}}},
		},
		"tool_without_config": {
			ToolName:     "tool_without_config",
			Instructions: "static instructions",
			Parameters:   map[string]any{"properties": map[string]any{"y": map[string]any{"type": "number"}}},
		},
	})

	startExecutor(t, exe)

	time.Sleep(500 * time.Millisecond)

	if toolWithConfig.Instructions() != "config-aware instructions" {
		t.Errorf("tool_with_config instructions = %q", toolWithConfig.Instructions())
	}
	if toolWithoutConfig.Instructions() != "static instructions" {
		t.Errorf("tool_without_config instructions = %q", toolWithoutConfig.Instructions())
	}
}

func TestRequestToolSchemaWithRetry(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t)

	toolCfg := &ToolConfig{
		ToolName:    "retry_init_tool",
		Description: "Tests retry",
	}
	rt := NewTool(toolCfg, exe)
	exe.RegisterTool(rt)

	go func() {
		time.Sleep(500 * time.Millisecond)

		q, err := brk.CreateQueue("str-delayed-init", broker.QueueOptions{Named: true})
		if err != nil {
			return
		}
		if subErr := brk.Subscribe(q, remotetool.InitRequestSubscription(testNamespace)); subErr != nil {
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		msg, err := brk.Receive(ctx, q)
		if err != nil || msg == nil {
			return
		}
		msg.Ack()

		var req a2a.Request
		_ = json.Unmarshal(msg.Payload, &req)
		replyTo := msg.UserProperties[remotetool.PropReplyTo]

		ip := &remotetool.InitParams{
			ToolName:     "retry_init_tool",
			Instructions: "finally got it",
		}
		resp := a2a.NewResponse(req.ID, ip)
		payload, _ := json.Marshal(resp)
		_ = brk.Publish(context.Background(), replyTo, &broker.Message{
			Payload:       payload,
			CorrelationID: req.ID,
		})
	}()

	startExecutor(t, exe)

	deadline := time.After(10 * time.Second)
	for rt.Instructions() != "finally got it" {
		select {
		case <-deadline:
			t.Fatalf("instructions = %q, expected retry to eventually succeed", rt.Instructions())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func TestExecutorHILFieldPropagation(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t)
	startExecutor(t, exe)

	q, _ := brk.CreateQueue("hil-inspector", broker.QueueOptions{Named: true})
	_ = brk.Subscribe(q, remotetool.InvokeTopic(testNamespace, "hil_tool"))

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		msg, err := brk.Receive(ctx, q)
		if err != nil {
			return
		}
		msg.Ack()

		replyTo := msg.UserProperties[remotetool.PropReplyTo]
		var req a2a.Request
		_ = json.Unmarshal(msg.Payload, &req)

		result := remotetool.InvokeResult{ToolResult: map[string]any{}}
		resp := a2a.NewResponse(req.ID, result)
		payload, _ := json.Marshal(resp)
		_ = brk.Publish(context.Background(), replyTo, &broker.Message{Payload: payload})
	}()

	propsCh := make(chan map[string]string, 1)
	q2, _ := brk.CreateQueue("hil-props", broker.QueueOptions{Named: true})
	_ = brk.Subscribe(q2, remotetool.InvokeTopic(testNamespace, "hil_tool"))
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		msg, err := brk.Receive(ctx, q2)
		if err != nil {
			return
		}
		msg.Ack()
		propsCh <- msg.UserProperties
	}()

	_, err := exe.Invoke(context.Background(), "corr-hil", &InvokeRequest{
		ToolName:       "hil_tool",
		AppName:        testAgent,
		UserID:         "user-1",
		SessionID:      "sess-1",
		TimeoutSeconds: 5,
		ParentTaskID:   "parent-task-abc",
		A2AStatusTopic: "test-ns/a2a/v1/gateway/status/gw-1/parent-task-abc",
		ContextID:      "ctx-session-1",
	}, nil)
	if err != nil {
		t.Fatalf("invoke failed: %v", err)
	}

	select {
	case props := <-propsCh:
		if got := props[remotetool.PropA2AStatusTopic]; got != "test-ns/a2a/v1/gateway/status/gw-1/parent-task-abc" {
			t.Errorf("a2aStatusTopic = %q, want gateway status topic", got)
		}
		if got := props[remotetool.PropContextID]; got != "ctx-session-1" {
			t.Errorf("contextId = %q, want ctx-session-1", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for user properties")
	}
}

func TestDispatchInits_BroadcastTriggersRefetch(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t)

	rt := NewTool(&ToolConfig{
		ToolName:              "wordcount__count_text",
		Description:           "Test tool",
		ToolPassthroughConfig: map[string]any{"key": "value"},
	}, exe)
	exe.RegisterTool(rt)

	q, err := brk.CreateQueue("str-init-refetch", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := brk.Subscribe(q, remotetool.InitRequestSubscription(testNamespace)); subErr != nil {
		t.Fatal(subErr)
	}

	requestCount := 0
	var reqMu sync.Mutex
	go func() {
		for range 2 {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			msg, recvErr := brk.Receive(ctx, q)
			cancel()
			if recvErr != nil || msg == nil {
				return
			}
			msg.Ack()

			var req a2a.Request
			if jsonErr := json.Unmarshal(msg.Payload, &req); jsonErr != nil {
				continue
			}
			replyTo := msg.UserProperties[remotetool.PropReplyTo]

			reqMu.Lock()
			requestCount++
			n := requestCount
			reqMu.Unlock()

			instructions := "schema A"
			if n >= 2 {
				instructions = "schema B"
			}
			ip := &remotetool.InitParams{
				ToolName:     "wordcount__count_text",
				Instructions: instructions,
				Parameters: map[string]any{
					"properties": map[string]any{
						"text": map[string]any{"type": "string"},
					},
				},
			}
			resp := a2a.NewResponse(req.ID, ip)
			payload, _ := json.Marshal(resp)
			_ = brk.Publish(context.Background(), replyTo, &broker.Message{
				Payload:       payload,
				CorrelationID: req.ID,
			})
		}
	}()

	startExecutor(t, exe)

	deadline := time.After(5 * time.Second)
	for rt.Instructions() != "schema A" {
		select {
		case <-deadline:
			t.Fatalf("schema A never applied; instructions = %q", rt.Instructions())
		case <-time.After(20 * time.Millisecond):
		}
	}

	staticInit := &remotetool.InitParams{
		ToolName:     "wordcount__count_text",
		Instructions: "static fallback (should NOT be applied)",
		Parameters: map[string]any{
			"properties": map[string]any{
				"text":   map[string]any{"type": "string"},
				"locale": map[string]any{"type": "string"},
			},
		},
	}
	notification := a2a.NewRequest("", remotetool.MethodInit, staticInit)
	payload, _ := json.Marshal(notification)
	initTopic := remotetool.InitTopic(testNamespace, "wordcount__count_text")
	if err := brk.Publish(context.Background(), initTopic, &broker.Message{Payload: payload}); err != nil {
		t.Fatalf("publish broadcast init: %v", err)
	}

	deadline = time.After(5 * time.Second)
	for rt.Instructions() != "schema B" {
		got := rt.Instructions()
		if got == "static fallback (should NOT be applied)" {
			t.Fatalf("broadcast static schema overwrote config-aware schema; instructions = %q", got)
		}
		select {
		case <-deadline:
			t.Fatalf("schema B never applied; instructions = %q (request count = %d)", got, requestCount)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func TestDispatchInits_BroadcastDedupsSameContent(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t)

	rt := NewTool(&ToolConfig{
		ToolName:              "stable_tool",
		Description:           "Has config",
		ToolPassthroughConfig: map[string]any{"key": "value"},
	}, exe)
	exe.RegisterTool(rt)

	q, err := brk.CreateQueue("str-init-dedup", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := brk.Subscribe(q, remotetool.InitRequestSubscription(testNamespace)); subErr != nil {
		t.Fatal(subErr)
	}
	var reqCount int32
	var reqMu sync.Mutex
	go func() {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			msg, recvErr := brk.Receive(ctx, q)
			cancel()
			if recvErr != nil || msg == nil {
				return
			}
			msg.Ack()

			var req a2a.Request
			if jsonErr := json.Unmarshal(msg.Payload, &req); jsonErr != nil {
				continue
			}
			reqMu.Lock()
			reqCount++
			reqMu.Unlock()

			replyTo := msg.UserProperties[remotetool.PropReplyTo]
			ip := &remotetool.InitParams{
				ToolName:     "stable_tool",
				Instructions: "config-aware",
			}
			resp := a2a.NewResponse(req.ID, ip)
			payload, _ := json.Marshal(resp)
			_ = brk.Publish(context.Background(), replyTo, &broker.Message{
				Payload:       payload,
				CorrelationID: req.ID,
			})
		}
	}()

	startExecutor(t, exe)

	deadline := time.After(5 * time.Second)
	for rt.Instructions() != "config-aware" {
		select {
		case <-deadline:
			t.Fatalf("config-aware schema never applied")
		case <-time.After(20 * time.Millisecond):
		}
	}

	staticInit := &remotetool.InitParams{
		ToolName:     "stable_tool",
		Instructions: "static",
	}
	notification := a2a.NewRequest("", remotetool.MethodInit, staticInit)
	payload, _ := json.Marshal(notification)
	initTopic := remotetool.InitTopic(testNamespace, "stable_tool")

	for range 3 {
		if err := brk.Publish(context.Background(), initTopic, &broker.Message{Payload: payload}); err != nil {
			t.Fatalf("publish broadcast init: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	time.Sleep(200 * time.Millisecond)

	reqMu.Lock()
	final := reqCount
	reqMu.Unlock()
	if final > 2 {
		t.Errorf("identical broadcasts triggered %d init_requests; want at most 2", final)
	}
}

func TestInvokeErrorInterface(t *testing.T) {
	t.Parallel()
	err := &InvokeError{
		Code:    remotetool.ErrSandboxTimeout,
		Message: "timed out",
	}

	if err.Error() != "remote tool error [SANDBOX_TIMEOUT]: timed out" {
		t.Errorf("Error() = %q", err.Error())
	}
	if err.Retryable() {
		t.Error("SANDBOX_TIMEOUT should not be retryable")
	}

	retryErr := &InvokeError{
		Code:    remotetool.ErrToolNotAvailable,
		Message: "stale",
	}
	if !retryErr.Retryable() {
		t.Error("TOOL_NOT_AVAILABLE should be retryable")
	}
}

func TestRegisterTool_LateBoundAfterStart_FetchesSchema(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t)

	simulateSTRInitHandler(t, brk, map[string]*remotetool.InitParams{
		"late_tool": {
			ToolName:     "late_tool",
			Instructions: "late-bound schema delivered",
			Parameters: map[string]any{
				"properties": map[string]any{
					"input_document": map[string]any{"type": "string"},
				},
				"required": []any{"input_document"},
			},
			ArtifactParams: map[string]remotetool.ArtifactParamInfo{
				"input_document": {IsList: false, IsOptional: false},
			},
		},
	})

	startExecutor(t, exe)

	rt := NewTool(&ToolConfig{ToolName: "late_tool", Description: "late-bound"}, exe)
	exe.RegisterTool(rt)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if rt.Instructions() != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := rt.Instructions(); got != "late-bound schema delivered" {
		t.Errorf("late-bound tool didn't pick up schema; Instructions() = %q", got)
	}
	if names := rt.cfg.ArtifactParamNames(); len(names) == 0 || names["input_document"].IsOptional {
		t.Errorf("late-bound tool didn't pick up artifact params; got %#v", names)
	}
}

func TestRegisterTool_BeforeStart_StillFetchesSchema(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t)

	rt := NewTool(&ToolConfig{ToolName: "early_tool", Description: "early-bound"}, exe)
	exe.RegisterTool(rt)

	simulateSTRInitHandler(t, brk, map[string]*remotetool.InitParams{
		"early_tool": {
			ToolName:     "early_tool",
			Instructions: "start-time schema delivered",
			Parameters: map[string]any{
				"properties": map[string]any{
					"input_image": map[string]any{"type": "string"},
				},
				"required": []any{"input_image"},
			},
			ArtifactParams: map[string]remotetool.ArtifactParamInfo{
				"input_image": {IsList: false, IsOptional: false},
			},
		},
	})

	startExecutor(t, exe)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if rt.Instructions() != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := rt.Instructions(); got != "start-time schema delivered" {
		t.Errorf("pre-Start tool didn't pick up schema; Instructions() = %q", got)
	}
}
