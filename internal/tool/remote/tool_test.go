// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/str"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestToolImplementsBuiltinTool(t *testing.T) {
	t.Parallel()
	var _ tool.BuiltinTool = (*Tool)(nil)
}

func TestDescription_FallsBackToInit(t *testing.T) {
	t.Parallel()
	rt := NewTool(&ToolConfig{ToolName: "execute_python_code"}, nil)
	if got := rt.Description(); got != "" {
		t.Fatalf("pre-init Description = %q, want empty (no YAML, no init yet)", got)
	}
	rt.applyInit(&remotetool.InitParams{
		ToolName:     "execute_python_code",
		Description:  "Run Python over artifacts in a sandbox.",
		Instructions: "instr",
	}, false)
	if got := rt.Description(); got != "Run Python over artifacts in a sandbox." {
		t.Errorf("post-init Description = %q, want the STR-sent description", got)
	}
}

func TestApplyInit_TracksTheLatestAdvertisedTimeout(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		inits []int
		want  int
	}{
		{name: "no init advertises nothing", want: 0},
		{name: "advertised timeout is kept", inits: []int{3600}, want: 3600},
		{name: "a changed timeout replaces the old one", inits: []int{3600, 900}, want: 900},
		{name: "an init without a timeout clears it", inits: []int{3600, 0}, want: 0},
		{name: "a negative timeout is ignored", inits: []int{-5}, want: 0},
		{name: "an absurd timeout is bounded", inits: []int{1 << 40}, want: maxAdvertisedTimeoutSeconds},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rt := NewTool(&ToolConfig{ToolName: "long_tool", TimeoutSeconds: 30}, nil)
			for i, secs := range tc.inits {
				rt.applyInit(&remotetool.InitParams{
					ToolName:       "long_tool",
					Description:    "runs long",
					Instructions:   strings.Repeat("i", i+1),
					TimeoutSeconds: secs,
				}, false)
			}
			if got := int(rt.advertisedTimeoutSeconds.Load()); got != tc.want {
				t.Errorf("advertised timeout = %d, want %d", got, tc.want)
			}
			if got := rt.TimeoutSeconds(); got != 30 {
				t.Errorf("TimeoutSeconds() = %d, want the configured 30; the advertised timeout must not masquerade as config", got)
			}
		})
	}
}

func TestWaitTimeoutSeconds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		req  InvokeRequest
		want int
	}{
		{name: "the caller's override wins", req: InvokeRequest{TimeoutSeconds: 30, AdvertisedTimeout: 3600}, want: 30},
		{name: "the advertised timeout sizes the wait without an override", req: InvokeRequest{AdvertisedTimeout: 3600}, want: 3600},
		{name: "an STR that advertises nothing gets the default", req: InvokeRequest{}, want: str.DefaultRemoteToolTimeoutSeconds},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := waitTimeoutSeconds(&tc.req); got != tc.want {
				t.Errorf("waitTimeoutSeconds = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestAvailable_TracksInitUnavailable(t *testing.T) {
	t.Parallel()
	rt := NewTool(&ToolConfig{ToolName: "t"}, nil)
	if !rt.Available() {
		t.Fatal("tool should start available (init pending)")
	}
	rt.markInitUnavailable()
	if rt.Available() {
		t.Fatal("tool should be unavailable after init retries exhaust")
	}
	rt.applyInit(&remotetool.InitParams{ToolName: "t", Description: "desc"}, false)
	if !rt.Available() {
		t.Fatal("tool should self-heal to available once an init is accepted")
	}
}

func TestDescription_YAMLWinsOverInit(t *testing.T) {
	t.Parallel()
	rt := NewTool(&ToolConfig{ToolName: "t", Description: "operator desc"}, nil)
	rt.applyInit(&remotetool.InitParams{ToolName: "t", Description: "init desc"}, false)
	if got := rt.Description(); got != "operator desc" {
		t.Errorf("Description = %q, want operator YAML (cfg.Description) to win", got)
	}
}

func TestDescription_EmptyBroadcastDoesNotClobber(t *testing.T) {
	t.Parallel()
	rt := NewTool(&ToolConfig{ToolName: "t"}, nil)
	rt.applyInit(&remotetool.InitParams{ToolName: "t", Description: "first desc", Instructions: "i1"}, false)
	if got := rt.Description(); got != "first desc" {
		t.Fatalf("after first init Description = %q, want %q", got, "first desc")
	}
	rt.applyInit(&remotetool.InitParams{ToolName: "t", Instructions: "i2-changed"}, false)
	if got := rt.Description(); got != "first desc" {
		t.Errorf("empty-Description broadcast clobbered it: Description = %q, want %q", got, "first desc")
	}
}

func TestToolMetadata(t *testing.T) {
	t.Parallel()
	cfg := &ToolConfig{
		ToolName:    "process_file",
		Description: "Process a file",
		Parameters: map[string]ParamConfig{
			"input_file": {Type: "artifact", Description: "File", Required: true},
			"format":     {Type: "string", Description: "Format", Required: false},
		},
	}

	rt := NewTool(cfg, nil)
	if rt.Name() != "process_file" {
		t.Errorf("Name = %q", rt.Name())
	}
	if rt.Description() != "Process a file" {
		t.Errorf("Description = %q", rt.Description())
	}
	if !rt.IsLongRunning() {
		t.Error("remote tools should be long-running")
	}
	if rt.ArtifactParams() != nil {
		t.Error("ArtifactParams should be nil (STR handles loading)")
	}

	schema := rt.Parameters()
	if schema == nil {
		t.Fatal("Parameters should not be nil")
	}
	if len(schema.Properties) != 2 {
		t.Errorf("expected 2 properties, got %d", len(schema.Properties))
	}
}

func TestToolExecuteSuccess(t *testing.T) {
	t.Parallel()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}

	exe := NewExecutor(brk, testNamespace, testAgent, WithInitTimeout(2*time.Second), WithInvokeBuffer(2*time.Second))
	startExecutor(t, exe)

	cfg := &ToolConfig{
		ToolName:       "echo",
		Description:    "Echo tool",
		TimeoutSeconds: 5,
		SandboxProfile: "standard",
	}
	rt := NewTool(cfg, exe)

	simulateSTR(t, brk, "echo", func(corrID, replyTo, _ string) {
		result := remotetool.InvokeResult{
			ToolResult:      map[string]any{"message": "hello back"},
			ExecutionTimeMs: 50,
		}
		resp := a2a.NewResponse(corrID, result)
		payload, _ := json.Marshal(resp)
		_ = brk.Publish(context.Background(), replyTo, &broker.Message{Payload: payload})
	})

	toolCtx := &tool.Context{
		UserID:    "user-1",
		SessionID: "sess-1",
		AgentName: testAgent,
	}

	result, err := rt.Execute(context.Background(), map[string]any{"message": "hello"}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Errorf("status = %q, want success", result.Status)
	}
	if result.Data["message"] != "hello back" {
		t.Errorf("data = %v", result.Data)
	}
}

func TestToolExecuteWithArtifacts(t *testing.T) {
	t.Parallel()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}

	exe := NewExecutor(brk, testNamespace, testAgent, WithInitTimeout(2*time.Second), WithInvokeBuffer(2*time.Second))
	startExecutor(t, exe)

	cfg := &ToolConfig{
		ToolName:       "artifact_tool",
		Description:    "Tool with artifacts",
		TimeoutSeconds: 5,
		SandboxProfile: "standard",
		Parameters: map[string]ParamConfig{
			"input_file": {Type: "artifact", Required: true},
		},
	}
	rt := NewTool(cfg, exe)

	simulateSTR(t, brk, "artifact_tool", func(corrID, replyTo, _ string) {
		result := remotetool.InvokeResult{
			ToolResult:      map[string]any{"status": "success"},
			ExecutionTimeMs: 100,
			CreatedArtifacts: []remotetool.CreatedArtifact{
				{Filename: "output.pdf", Version: 0, MIMEType: "application/pdf", SizeBytes: 2048},
			},
		}
		resp := a2a.NewResponse(corrID, result)
		payload, _ := json.Marshal(resp)
		_ = brk.Publish(context.Background(), replyTo, &broker.Message{Payload: payload})
	})

	toolCtx := &tool.Context{
		UserID:    "user-1",
		SessionID: "sess-1",
		AgentName: testAgent,
	}

	result, err := rt.Execute(context.Background(), map[string]any{
		"input_file": "data.csv",
	}, toolCtx)

	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Errorf("status = %q", result.Status)
	}
	if len(result.DataObjects) != 1 {
		t.Fatalf("expected 1 data object, got %d", len(result.DataObjects))
	}
	do := result.DataObjects[0]
	if do.Name != "output.pdf" {
		t.Errorf("artifact name = %q", do.Name)
	}
	if do.ArtifactRef == nil || do.ArtifactRef.Filename != "output.pdf" {
		t.Errorf("artifact ref = %v", do.ArtifactRef)
	}
	if do.Disposition != tool.DispositionArtifact {
		t.Errorf("disposition = %q", do.Disposition)
	}
}

func TestToolExecuteFiresEnrichedArtifactHook(t *testing.T) {
	t.Parallel()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}

	exe := NewExecutor(brk, testNamespace, testAgent, WithInitTimeout(2*time.Second), WithInvokeBuffer(2*time.Second))
	startExecutor(t, exe)

	cfg := &ToolConfig{
		ToolName:       "artifact_tool",
		Description:    "Tool with artifacts",
		TimeoutSeconds: 5,
		SandboxProfile: "standard",
		Parameters: map[string]ParamConfig{
			"input_file": {Type: "artifact", Required: true},
		},
	}
	rt := NewTool(cfg, exe)

	simulateSTR(t, brk, "artifact_tool", func(corrID, replyTo, _ string) {
		result := remotetool.InvokeResult{
			ToolResult:      map[string]any{"status": "success"},
			ExecutionTimeMs: 100,
			CreatedArtifacts: []remotetool.CreatedArtifact{
				{Filename: "output.pdf", Version: 0, MIMEType: "application/pdf", SizeBytes: 2048},
			},
		}
		resp := a2a.NewResponse(corrID, result)
		payload, _ := json.Marshal(resp)
		_ = brk.Publish(context.Background(), replyTo, &broker.Message{Payload: payload})
	})

	var got []tool.ArtifactSavedInfo
	toolCtx := &tool.Context{
		UserID:     "user-1",
		SessionID:  "sess-1",
		AgentName:  testAgent,
		ToolCallID: "real-call-1",
		NotifyArtifactSaved: func(_ context.Context, info tool.ArtifactSavedInfo) {
			got = append(got, info)
		},
	}

	result, err := rt.Execute(context.Background(), map[string]any{"input_file": "data.csv"}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("NotifyArtifactSaved fired %d times, want exactly 1", len(got))
	}
	if got[0].ToolCallID != "real-call-1" {
		t.Errorf("hook ToolCallID = %q, want the real tool-call id (host-notify- ids are parked by the WebUI timeline)", got[0].ToolCallID)
	}
	if got[0].Filename != "output.pdf" {
		t.Errorf("hook filename = %q", got[0].Filename)
	}
	if len(result.DataObjects) != 1 || result.DataObjects[0].ArtifactRef == nil {
		t.Fatalf("expected 1 DataObject carrying an ArtifactRef, got %+v", result.DataObjects)
	}
}

func TestNotifyCreatedArtifacts(t *testing.T) {
	t.Parallel()
	var got []tool.ArtifactSavedInfo
	toolCtx := &tool.Context{
		ToolCallID: "call-xyz",
		NotifyArtifactSaved: func(_ context.Context, info tool.ArtifactSavedInfo) {
			got = append(got, info)
		},
	}
	result := &tool.Result{
		DataObjects: []tool.DataObject{
			{Name: "plain.json", Content: []byte("{}")},
			{
				Name:        "report.pdf",
				MIMEType:    "application/pdf",
				Description: "Quarterly report",
				Tags:        []string{"q3"},
				ArtifactRef: &tool.ArtifactRef{Filename: "report.pdf", Version: 2, SizeBytes: 4096},
			},
		},
	}

	(&Tool{}).notifyCreatedArtifacts(context.Background(), toolCtx, result)

	if len(got) != 1 {
		t.Fatalf("hook fired %d times, want 1 (only the ArtifactRef DataObject)", len(got))
	}
	info := got[0]
	if info.Filename != "report.pdf" || info.Version != 2 || info.SizeBytes != 4096 {
		t.Errorf("ref fields = %+v", info)
	}
	if info.Description != "Quarterly report" {
		t.Errorf("Description = %q, want the tool's description", info.Description)
	}
	if info.ToolCallID != "call-xyz" {
		t.Errorf("ToolCallID = %q, want the real tool-call id", info.ToolCallID)
	}
	if len(info.Tags) != 1 || info.Tags[0] != "q3" {
		t.Errorf("Tags = %v, want [q3]", info.Tags)
	}
}

func TestNotifyCreatedArtifactsNoHook(t *testing.T) {
	t.Parallel()
	result := &tool.Result{DataObjects: []tool.DataObject{
		{ArtifactRef: &tool.ArtifactRef{Filename: "x"}},
	}}
	(&Tool{}).notifyCreatedArtifacts(context.Background(), &tool.Context{}, result)
}

func TestToolExecuteError(t *testing.T) {
	t.Parallel()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}

	exe := NewExecutor(brk, testNamespace, testAgent, WithInitTimeout(2*time.Second), WithInvokeBuffer(2*time.Second))
	startExecutor(t, exe)

	cfg := &ToolConfig{
		ToolName:       "fail_tool",
		TimeoutSeconds: 5,
	}
	rt := NewTool(cfg, exe)

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
		_ = brk.Publish(context.Background(), replyTo, &broker.Message{Payload: payload})
	})

	toolCtx := &tool.Context{AgentName: testAgent}
	result, err := rt.Execute(context.Background(), map[string]any{}, toolCtx)
	if err != nil {
		t.Fatalf("Execute should not return Go error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Errorf("status = %q, want error", result.Status)
	}
}

func TestToolExecuteRetry(t *testing.T) {
	t.Parallel()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}

	exe := NewExecutor(brk, testNamespace, testAgent, WithInitTimeout(2*time.Second), WithInvokeBuffer(2*time.Second))
	startExecutor(t, exe)

	cfg := &ToolConfig{
		ToolName:       "retry_tool",
		TimeoutSeconds: 5,
	}
	rt := NewTool(cfg, exe)

	callCount := 0
	q, _ := brk.CreateQueue("retry-str", broker.QueueOptions{Named: true})
	_ = brk.Subscribe(q, remotetool.InvokeTopic(testNamespace, "retry_tool"))

	go func() {
		for range 2 {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			msg, recvErr := brk.Receive(ctx, q)
			cancel()
			if recvErr != nil {
				return
			}
			msg.Ack()
			callCount++

			var req a2a.Request
			_ = json.Unmarshal(msg.Payload, &req)
			replyTo := msg.UserProperties[remotetool.PropReplyTo]

			if callCount == 1 {
				invokeErr := remotetool.InvokeError{
					Code:    remotetool.ErrToolNotAvailable,
					Message: "tool removed",
				}
				errPayload, _ := json.Marshal(invokeErr)
				raw := struct {
					JSONRPC string          `json:"jsonrpc"`
					ID      string          `json:"id"`
					Error   json.RawMessage `json:"error"`
				}{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error:   errPayload,
				}
				payload, _ := json.Marshal(raw)
				_ = brk.Publish(context.Background(), replyTo, &broker.Message{Payload: payload})
			} else {
				result := remotetool.InvokeResult{
					ToolResult: map[string]any{"retried": true},
				}
				resp := a2a.NewResponse(req.ID, result)
				payload, _ := json.Marshal(resp)
				_ = brk.Publish(context.Background(), replyTo, &broker.Message{Payload: payload})
			}
		}
	}()

	toolCtx := &tool.Context{AgentName: testAgent}
	result, err := rt.Execute(context.Background(), map[string]any{}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Errorf("status = %q after retry, want success", result.Status)
	}
	if callCount != 2 {
		t.Errorf("expected 2 STR calls (1 retry), got %d", callCount)
	}
}

func TestToolReinitsConnectorToolOnToolNotFound(t *testing.T) {
	t.Parallel()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}

	exe := NewExecutor(brk, testNamespace, testAgent, WithInitTimeout(2*time.Second), WithInvokeBuffer(2*time.Second))
	startExecutor(t, exe)

	cfg := &ToolConfig{
		ToolName:              "mydb_sql_query_019eb14a",
		TimeoutSeconds:        5,
		ToolPassthroughConfig: map[string]any{"_str_binary": "execute_sql_query"},
	}
	rt := NewTool(cfg, exe)

	var mu sync.Mutex
	var invokeCount, initCount int
	initTopic := remotetool.InitRequestTopic(testNamespace, cfg.ToolName)

	q, _ := brk.CreateQueue("reinit-str", broker.QueueOptions{Named: true})
	_ = brk.Subscribe(q, remotetool.InvokeTopic(testNamespace, cfg.ToolName))
	_ = brk.Subscribe(q, initTopic)

	go func() {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			msg, recvErr := brk.Receive(ctx, q)
			cancel()
			if recvErr != nil {
				return
			}
			msg.Ack()

			var req a2a.Request
			_ = json.Unmarshal(msg.Payload, &req)
			replyTo := msg.UserProperties[remotetool.PropReplyTo]

			if msg.Topic == initTopic {
				mu.Lock()
				initCount++
				mu.Unlock()
				resp := a2a.NewResponse(req.ID, remotetool.InitParams{ToolName: cfg.ToolName})
				payload, _ := json.Marshal(resp)
				_ = brk.Publish(context.Background(), replyTo, &broker.Message{Payload: payload})
				continue
			}

			mu.Lock()
			invokeCount++
			n := invokeCount
			mu.Unlock()

			if n == 1 {
				invokeErr := remotetool.InvokeError{
					Code:    remotetool.ErrToolNotFound,
					Message: `tool "mydb_sql_query_019eb14a" is not currently available on this worker`,
				}
				errPayload, _ := json.Marshal(invokeErr)
				raw := struct {
					JSONRPC string          `json:"jsonrpc"`
					ID      string          `json:"id"`
					Error   json.RawMessage `json:"error"`
				}{JSONRPC: "2.0", ID: req.ID, Error: errPayload}
				payload, _ := json.Marshal(raw)
				_ = brk.Publish(context.Background(), replyTo, &broker.Message{Payload: payload})
				continue
			}

			result := remotetool.InvokeResult{ToolResult: map[string]any{"ok": true}}
			resp := a2a.NewResponse(req.ID, result)
			payload, _ := json.Marshal(resp)
			_ = brk.Publish(context.Background(), replyTo, &broker.Message{Payload: payload})
			return
		}
	}()

	toolCtx := &tool.Context{AgentName: testAgent}
	result, err := rt.Execute(context.Background(), map[string]any{}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q after re-init+retry, want success (msg: %s)", result.Status, result.Message)
	}
	mu.Lock()
	defer mu.Unlock()
	if initCount != 1 {
		t.Errorf("expected exactly 1 init_request (re-register), got %d", initCount)
	}
	if invokeCount != 2 {
		t.Errorf("expected 2 invokes (original + retry), got %d", invokeCount)
	}
}

func TestToolDoesNotReinitNonConnectorToolNotFound(t *testing.T) {
	t.Parallel()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	exe := NewExecutor(brk, testNamespace, testAgent, WithInitTimeout(2*time.Second), WithInvokeBuffer(2*time.Second))
	startExecutor(t, exe)

	cfg := &ToolConfig{ToolName: "plain_tool", TimeoutSeconds: 5}
	rt := NewTool(cfg, exe)

	var mu sync.Mutex
	var invokeCount, initCount int
	initTopic := remotetool.InitRequestTopic(testNamespace, cfg.ToolName)
	q, _ := brk.CreateQueue("noreinit-str", broker.QueueOptions{Named: true})
	_ = brk.Subscribe(q, remotetool.InvokeTopic(testNamespace, cfg.ToolName))
	_ = brk.Subscribe(q, initTopic)

	go func() {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			msg, recvErr := brk.Receive(ctx, q)
			cancel()
			if recvErr != nil {
				return
			}
			msg.Ack()
			var req a2a.Request
			_ = json.Unmarshal(msg.Payload, &req)
			replyTo := msg.UserProperties[remotetool.PropReplyTo]
			mu.Lock()
			if msg.Topic == initTopic {
				initCount++
			} else {
				invokeCount++
			}
			mu.Unlock()
			invokeErr := remotetool.InvokeError{Code: remotetool.ErrToolNotFound, Message: "no such tool"}
			errPayload, _ := json.Marshal(invokeErr)
			raw := struct {
				JSONRPC string          `json:"jsonrpc"`
				ID      string          `json:"id"`
				Error   json.RawMessage `json:"error"`
			}{JSONRPC: "2.0", ID: req.ID, Error: errPayload}
			payload, _ := json.Marshal(raw)
			_ = brk.Publish(context.Background(), replyTo, &broker.Message{Payload: payload})
		}
	}()

	result, err := rt.Execute(context.Background(), map[string]any{}, &tool.Context{AgentName: testAgent})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Errorf("status = %q, want error for a non-connector TOOL_NOT_FOUND", result.Status)
	}
	mu.Lock()
	defer mu.Unlock()
	if initCount != 0 {
		t.Errorf("non-connector tool must not re-send init_request, got %d", initCount)
	}
	if invokeCount != 1 {
		t.Errorf("non-connector TOOL_NOT_FOUND must not retry the invoke, got %d invokes", invokeCount)
	}
}

func TestToolSurfacesNotFoundWhenReinitFails(t *testing.T) {
	t.Parallel()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	exe := NewExecutor(brk, testNamespace, testAgent, WithInitTimeout(1*time.Second), WithInvokeBuffer(2*time.Second))
	startExecutor(t, exe)

	cfg := &ToolConfig{
		ToolName:              "gone_sql_query_019eb14a",
		TimeoutSeconds:        5,
		ToolPassthroughConfig: map[string]any{"_str_binary": "execute_sql_query"},
	}
	rt := NewTool(cfg, exe)

	var mu sync.Mutex
	var invokeCount, initCount int
	initTopic := remotetool.InitRequestTopic(testNamespace, cfg.ToolName)
	q, _ := brk.CreateQueue("reinit-fail-str", broker.QueueOptions{Named: true})
	_ = brk.Subscribe(q, remotetool.InvokeTopic(testNamespace, cfg.ToolName))
	_ = brk.Subscribe(q, initTopic)

	go func() {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			msg, recvErr := brk.Receive(ctx, q)
			cancel()
			if recvErr != nil {
				return
			}
			msg.Ack()
			var req a2a.Request
			_ = json.Unmarshal(msg.Payload, &req)
			replyTo := msg.UserProperties[remotetool.PropReplyTo]

			if msg.Topic == initTopic {
				mu.Lock()
				initCount++
				mu.Unlock()
				return
			}
			mu.Lock()
			invokeCount++
			mu.Unlock()
			invokeErr := remotetool.InvokeError{Code: remotetool.ErrToolNotFound, Message: "not on this worker"}
			errPayload, _ := json.Marshal(invokeErr)
			raw := struct {
				JSONRPC string          `json:"jsonrpc"`
				ID      string          `json:"id"`
				Error   json.RawMessage `json:"error"`
			}{JSONRPC: "2.0", ID: req.ID, Error: errPayload}
			payload, _ := json.Marshal(raw)
			_ = brk.Publish(context.Background(), replyTo, &broker.Message{Payload: payload})
		}
	}()

	result, err := rt.Execute(context.Background(), map[string]any{}, &tool.Context{AgentName: testAgent})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Errorf("status = %q, want error when re-init did not re-register", result.Status)
	}
	if result.FailedDetail == nil || result.FailedDetail.Code != string(remotetool.ErrToolNotFound) {
		t.Errorf("surfaced detail = %+v, want the original TOOL_NOT_FOUND code preserved", result.FailedDetail)
	}
	mu.Lock()
	defer mu.Unlock()
	if initCount != 1 {
		t.Errorf("expected exactly 1 re-init attempt, got %d", initCount)
	}
	if invokeCount != 1 {
		t.Errorf("re-init did not re-register, so the retry must be skipped: want 1 invoke, got %d", invokeCount)
	}
}

func TestToolSurfacesNotFoundWhenRetryStillFailsAfterReinit(t *testing.T) {
	t.Parallel()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	exe := NewExecutor(brk, testNamespace, testAgent, WithInitTimeout(2*time.Second), WithInvokeBuffer(2*time.Second))
	startExecutor(t, exe)

	cfg := &ToolConfig{
		ToolName:              "mydb_sql_query_019eb14a",
		TimeoutSeconds:        5,
		ToolPassthroughConfig: map[string]any{"_str_binary": "execute_sql_query"},
	}
	rt := NewTool(cfg, exe)

	var mu sync.Mutex
	var invokeCount, initCount int
	initTopic := remotetool.InitRequestTopic(testNamespace, cfg.ToolName)
	q, _ := brk.CreateQueue("loop-once-str", broker.QueueOptions{Named: true})
	_ = brk.Subscribe(q, remotetool.InvokeTopic(testNamespace, cfg.ToolName))
	_ = brk.Subscribe(q, initTopic)

	go func() {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			msg, recvErr := brk.Receive(ctx, q)
			cancel()
			if recvErr != nil {
				return
			}
			msg.Ack()
			var req a2a.Request
			_ = json.Unmarshal(msg.Payload, &req)
			replyTo := msg.UserProperties[remotetool.PropReplyTo]

			if msg.Topic == initTopic {
				mu.Lock()
				initCount++
				mu.Unlock()
				resp := a2a.NewResponse(req.ID, remotetool.InitParams{ToolName: cfg.ToolName})
				payload, _ := json.Marshal(resp)
				_ = brk.Publish(context.Background(), replyTo, &broker.Message{Payload: payload})
				continue
			}
			mu.Lock()
			invokeCount++
			n := invokeCount
			mu.Unlock()
			invokeErr := remotetool.InvokeError{Code: remotetool.ErrToolNotFound, Message: "still not on this worker"}
			errPayload, _ := json.Marshal(invokeErr)
			raw := struct {
				JSONRPC string          `json:"jsonrpc"`
				ID      string          `json:"id"`
				Error   json.RawMessage `json:"error"`
			}{JSONRPC: "2.0", ID: req.ID, Error: errPayload}
			payload, _ := json.Marshal(raw)
			_ = brk.Publish(context.Background(), replyTo, &broker.Message{Payload: payload})
			if n >= 2 {
				return
			}
		}
	}()

	result, err := rt.Execute(context.Background(), map[string]any{}, &tool.Context{AgentName: testAgent})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Errorf("status = %q, want error after retry still not found", result.Status)
	}
	if result.FailedDetail == nil || result.FailedDetail.Code != string(remotetool.ErrToolNotFound) {
		t.Errorf("surfaced detail = %+v, want the retry's TOOL_NOT_FOUND code preserved", result.FailedDetail)
	}
	mu.Lock()
	defer mu.Unlock()
	if initCount != 1 {
		t.Errorf("expected exactly 1 re-init, got %d", initCount)
	}
	if invokeCount != 2 {
		t.Errorf("expected exactly 2 invokes (original + one retry, no loop), got %d", invokeCount)
	}
}

func TestToolStatusCallback(t *testing.T) {
	t.Parallel()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}

	exe := NewExecutor(brk, testNamespace, testAgent, WithInitTimeout(2*time.Second), WithInvokeBuffer(2*time.Second))
	startExecutor(t, exe)

	cfg := &ToolConfig{
		ToolName:       "status_tool",
		TimeoutSeconds: 5,
	}
	rt := NewTool(cfg, exe)

	simulateSTR(t, brk, "status_tool", func(corrID, replyTo, statusTo string) {
		statusMsg := a2a.Request{
			JSONRPC: "2.0",
			Method:  remotetool.MethodStatus,
			Params: remotetool.StatusParams{
				TaskID:     corrID,
				StatusText: "Working on it...",
				Timestamp:  "2025-02-09T12:00:00Z",
			},
		}
		statusPayload, _ := json.Marshal(statusMsg)
		_ = brk.Publish(context.Background(), statusTo, &broker.Message{Payload: statusPayload})

		time.Sleep(50 * time.Millisecond)

		result := remotetool.InvokeResult{ToolResult: map[string]any{"done": true}}
		resp := a2a.NewResponse(corrID, result)
		payload, _ := json.Marshal(resp)
		_ = brk.Publish(context.Background(), replyTo, &broker.Message{Payload: payload})
	})

	var mu sync.Mutex
	var statusMessages []string
	toolCtx := &tool.Context{
		AgentName: testAgent,
		SendStatus: func(msg string) error {
			mu.Lock()
			statusMessages = append(statusMessages, msg)
			mu.Unlock()
			return nil
		},
	}

	result, err := rt.Execute(context.Background(), map[string]any{}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Errorf("status = %q", result.Status)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(statusMessages) != 1 || statusMessages[0] != "Working on it..." {
		t.Errorf("status messages = %v", statusMessages)
	}
}

func TestToolBuildArtifactReferences(t *testing.T) {
	t.Parallel()
	cfg := &ToolConfig{
		ToolName: "test",
		Parameters: map[string]ParamConfig{
			"input_file": {Type: "artifact", Required: true},
			"format":     {Type: "string", Required: false},
		},
	}
	rt := NewTool(cfg, nil)

	refs := rt.buildArtifactReferences(map[string]any{
		"input_file": "data.csv",
		"format":     "json",
	})

	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	ref, ok := refs["input_file"]
	if !ok {
		t.Fatal("missing input_file ref")
	}
	if ref.Filename != "data.csv" {
		t.Errorf("filename = %q", ref.Filename)
	}
}

func TestToolBuildArtifactReferencesNoArtifacts(t *testing.T) {
	t.Parallel()
	cfg := &ToolConfig{
		ToolName: "test",
		Parameters: map[string]ParamConfig{
			"query": {Type: "string"},
		},
	}
	rt := NewTool(cfg, nil)

	refs := rt.buildArtifactReferences(map[string]any{"query": "hello"})
	if refs != nil {
		t.Errorf("expected nil refs, got %v", refs)
	}
}

func TestToolBuildArtifactReferences_StructList(t *testing.T) {
	t.Parallel()
	cfg := &ToolConfig{
		ToolName: "concatenate_audio",
		Parameters: map[string]ParamConfig{
			"clips_to_join": {
				Type:        "artifact_struct_list",
				FilenameKey: "filename",
				Required:    true,
			},
			"output_filename": {Type: "string", Required: false},
		},
	}
	rt := NewTool(cfg, nil)

	refs := rt.buildArtifactReferences(map[string]any{
		"clips_to_join": []any{
			map[string]any{"filename": "intro.mp3", "pause_after_ms": 500},
			map[string]any{"filename": "outro.wav:3"},
		},
		"output_filename": "combined.mp3",
	})

	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d (%v)", len(refs), refs)
	}
	first, ok := refs["clips_to_join[0]"]
	if !ok {
		t.Fatalf("missing clips_to_join[0] ref (have %v)", refs)
	}
	if first.Filename != "intro.mp3" {
		t.Errorf("[0].Filename = %q, want intro.mp3", first.Filename)
	}
	if first.Version != -1 {
		t.Errorf("[0].Version = %d, want -1 (latest)", first.Version)
	}
	second, ok := refs["clips_to_join[1]"]
	if !ok {
		t.Fatalf("missing clips_to_join[1] ref")
	}
	if second.Filename != "outro.wav" {
		t.Errorf("[1].Filename = %q, want outro.wav", second.Filename)
	}
	if second.Version != 3 {
		t.Errorf("[1].Version = %d, want 3", second.Version)
	}
}

func TestApplyInit_Dedup(t *testing.T) {
	t.Parallel()
	cfg := &ToolConfig{
		ToolName:    "dedup_tool",
		Description: "Test dedup",
	}
	rt := NewTool(cfg, nil)

	params := &remotetool.InitParams{
		ToolName:     "dedup_tool",
		Instructions: "Use this tool to echo.",
		Parameters: map[string]any{
			"properties": map[string]any{
				"message": map[string]any{"type": "string"},
			},
			"required": []any{"message"},
		},
	}

	rt.applyInit(params, false)
	if rt.Instructions() != "Use this tool to echo." {
		t.Errorf("instructions = %q after first apply", rt.Instructions())
	}
	fp1 := rt.initFingerprint

	rt.applyInit(params, false)
	fp2 := rt.initFingerprint
	if fp1 != fp2 {
		t.Error("fingerprint changed on identical applyInit — dedup failed")
	}
}

func TestApplyInit_VolumeParamsPropagate(t *testing.T) {
	t.Parallel()
	rt := NewTool(&ToolConfig{ToolName: "volume_read", Description: "read"}, nil)

	if vps := rt.VolumeParams(); vps != nil {
		t.Fatalf("expected no volume params before init, got %v", vps)
	}

	rt.applyInit(&remotetool.InitParams{
		ToolName: "volume_read",
		VolumeParams: []remotetool.VolumeParam{
			{Name: "workspace", Mode: "readonly", MountPath: "/workspace"},
		},
	}, true)

	vps := rt.VolumeParams()
	if len(vps) != 1 {
		t.Fatalf("expected 1 volume param after init, got %d", len(vps))
	}
	if vps[0].Mode != "readonly" || vps[0].MountPath != "/workspace" || vps[0].Name != "workspace" {
		t.Errorf("volume param not propagated correctly: %+v", vps[0])
	}
}

func TestApplyInit_DeployConfigVolumeParamsWin(t *testing.T) {
	t.Parallel()
	rt := NewTool(&ToolConfig{
		ToolName: "volume_write",
		VolumeParams: []VolumeParamConfig{
			{Name: "workspace", Mode: "readwrite", MountPath: "/workspace"},
		},
	}, nil)

	rt.applyInit(&remotetool.InitParams{
		ToolName: "volume_write",
		VolumeParams: []remotetool.VolumeParam{
			{Name: "workspace", Mode: "readonly", MountPath: "/elsewhere"},
		},
	}, true)

	vps := rt.VolumeParams()
	if len(vps) != 1 || vps[0].Mode != "readwrite" || vps[0].MountPath != "/workspace" {
		t.Errorf("deploy-config volume param should win, got %+v", vps)
	}
}

func TestApplyInit_Changed(t *testing.T) {
	t.Parallel()
	cfg := &ToolConfig{
		ToolName:    "change_tool",
		Description: "Test change",
	}
	rt := NewTool(cfg, nil)

	params1 := &remotetool.InitParams{
		ToolName:     "change_tool",
		Instructions: "Version 1",
		Parameters: map[string]any{
			"properties": map[string]any{
				"message": map[string]any{"type": "string"},
			},
		},
	}
	rt.applyInit(params1, false)
	fp1 := rt.initFingerprint

	params2 := &remotetool.InitParams{
		ToolName:     "change_tool",
		Instructions: "Version 2 — updated",
		Parameters: map[string]any{
			"properties": map[string]any{
				"message": map[string]any{"type": "string"},
			},
		},
	}
	rt.applyInit(params2, false)
	fp2 := rt.initFingerprint

	if fp1 == fp2 {
		t.Error("fingerprint should change when instructions differ")
	}
	if rt.Instructions() != "Version 2 — updated" {
		t.Errorf("instructions = %q, want updated", rt.Instructions())
	}
}

func TestApplyInit_BroadcastDoesNotOverwriteRequest(t *testing.T) {
	t.Parallel()
	cfg := &ToolConfig{
		ToolName:    "config_tool",
		Description: "Tool with config",
	}
	rt := NewTool(cfg, nil)

	requestParams := &remotetool.InitParams{
		ToolName:     "config_tool",
		Instructions: "Config-aware: Active template profile: solace_2025",
		Parameters: map[string]any{
			"properties": map[string]any{
				"message": map[string]any{"type": "string"},
			},
		},
	}
	rt.applyInit(requestParams, true)

	if rt.Instructions() != "Config-aware: Active template profile: solace_2025" {
		t.Fatalf("instructions after request = %q", rt.Instructions())
	}

	broadcastParams := &remotetool.InitParams{
		ToolName:     "config_tool",
		Instructions: "Static schema without config",
		Parameters: map[string]any{
			"properties": map[string]any{
				"message": map[string]any{"type": "string"},
			},
		},
	}
	rt.applyInit(broadcastParams, false)

	if rt.Instructions() != "Config-aware: Active template profile: solace_2025" {
		t.Errorf("broadcast overwrote config-aware schema: instructions = %q", rt.Instructions())
	}
}

func TestApplyInit_BroadcastAppliesWhenNoRequest(t *testing.T) {
	t.Parallel()
	cfg := &ToolConfig{
		ToolName:    "static_tool",
		Description: "Tool without config",
	}
	rt := NewTool(cfg, nil)

	broadcastParams := &remotetool.InitParams{
		ToolName:     "static_tool",
		Instructions: "Static schema",
		Parameters: map[string]any{
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
			},
		},
	}
	rt.applyInit(broadcastParams, false)

	if rt.Instructions() != "Static schema" {
		t.Errorf("instructions = %q, want 'Static schema'", rt.Instructions())
	}
}

func TestApplyInit_AuthHotSwap(t *testing.T) {
	t.Parallel()
	cfg := &ToolConfig{
		ToolName:    "hotswap_tool",
		Description: "Tool that gets auth via init",
	}
	rt := NewTool(cfg, nil)

	if rt.AuthHandler() != nil {
		t.Fatal("expected nil handler before any init")
	}

	type notifyCall struct {
		oldKey     string
		newHandler tool.AuthResponseHandler
	}
	var (
		mu      sync.Mutex
		notifyN []notifyCall
	)
	rt.SetAuthHandlerNotifier(func(oldKey string, newHandler tool.AuthResponseHandler) {
		mu.Lock()
		defer mu.Unlock()
		notifyN = append(notifyN, notifyCall{oldKey: oldKey, newHandler: newHandler})
	})

	params1 := &remotetool.InitParams{
		ToolName: "hotswap_tool",
		Auth: &remotetool.AuthDeclaration{
			Type: "oauth2",
			Scheme: &remotetool.AuthDeclarationScheme{
				AuthorizationURL: "https://idp.example.com/authorize",
				TokenURL:         "https://idp.example.com/token",
				Scopes:           []string{"read"},
			},
		},
	}
	rt.applyInit(params1, false)

	ah1 := rt.AuthHandler()
	if ah1 == nil {
		t.Fatal("handler nil after first init with auth")
	}
	if ah1.Config().Type != "oauth2" {
		t.Errorf("type = %q", ah1.Config().Type)
	}
	if ah1.Config().Scheme == nil || ah1.Config().Scheme.TokenURL != "https://idp.example.com/token" {
		t.Errorf("scheme not threaded through: %+v", ah1.Config().Scheme)
	}
	if len(ah1.Config().Scheme.Scopes) != 1 || ah1.Config().Scheme.Scopes[0] != "read" {
		t.Errorf("scopes = %v", ah1.Config().Scheme.Scopes)
	}

	mu.Lock()
	if len(notifyN) != 1 {
		t.Fatalf("notify calls = %d, want 1", len(notifyN))
	}
	if notifyN[0].oldKey != "" {
		t.Errorf("first notify oldKey = %q, want empty", notifyN[0].oldKey)
	}
	if notifyN[0].newHandler == nil {
		t.Error("first notify newHandler nil")
	}
	mu.Unlock()

	rt.applyInit(params1, false)
	if rt.AuthHandler() != ah1 {
		t.Error("handler swapped on identical re-broadcast")
	}
	mu.Lock()
	if len(notifyN) != 1 {
		t.Errorf("notify called again on dedup; calls = %d", len(notifyN))
	}
	mu.Unlock()

	params2 := &remotetool.InitParams{
		ToolName: "hotswap_tool",
		Auth: &remotetool.AuthDeclaration{
			Type: "oauth2",
			Scheme: &remotetool.AuthDeclarationScheme{
				AuthorizationURL: "https://idp.example.com/authorize",
				TokenURL:         "https://idp.example.com/token",
				Scopes:           []string{"read", "write"},
			},
		},
	}
	rt.applyInit(params2, false)

	ah2 := rt.AuthHandler()
	if ah2 == ah1 {
		t.Error("handler should have been swapped")
	}
	if ah2 == nil {
		t.Fatal("handler nil after rebuild")
	}
	if len(ah2.Config().Scheme.Scopes) != 2 {
		t.Errorf("rebuilt scopes = %v", ah2.Config().Scheme.Scopes)
	}

	mu.Lock()
	if len(notifyN) != 2 {
		t.Fatalf("notify calls = %d, want 2", len(notifyN))
	}
	if notifyN[1].oldKey != ah1.CredentialKey() {
		t.Errorf("second notify oldKey = %q, want %q", notifyN[1].oldKey, ah1.CredentialKey())
	}
	mu.Unlock()
}

func TestApplyInit_AuthMergeWithDeployer(t *testing.T) {
	t.Parallel()
	cfg := &ToolConfig{
		ToolName: "merge_tool",
		Auth: &toolauth.AuthConfig{
			Type:          "oauth2",
			CredentialKey: "operator_key",
			Credential: &toolauth.AuthCredential{
				ClientID:     "deployer-client",
				ClientSecret: "deployer-secret",
			},
		},
	}
	rt := NewTool(cfg, nil)

	if rt.AuthHandler() == nil {
		t.Fatal("expected deployer-derived handler at construction")
	}
	if rt.AuthHandler().CredentialKey() != "operator_key" {
		t.Errorf("initial credential key = %q", rt.AuthHandler().CredentialKey())
	}

	params := &remotetool.InitParams{
		ToolName: "merge_tool",
		Auth: &remotetool.AuthDeclaration{
			Type: "oauth2",
			Scheme: &remotetool.AuthDeclarationScheme{
				AuthorizationURL: "https://tool.example.com/authorize",
				TokenURL:         "https://tool.example.com/token",
				Scopes:           []string{"calendar.read"},
			},
		},
	}
	rt.applyInit(params, false)

	merged := rt.AuthHandler()
	if merged == nil {
		t.Fatal("handler nil after merge")
	}
	if merged.CredentialKey() != "operator_key" {
		t.Errorf("credential key after merge = %q (deployer should win)", merged.CredentialKey())
	}
	if merged.Config().Credential == nil || merged.Config().Credential.ClientID != "deployer-client" {
		t.Errorf("deployer credentials lost in merge: %+v", merged.Config().Credential)
	}
	if merged.Config().Scheme == nil || merged.Config().Scheme.TokenURL != "https://tool.example.com/token" {
		t.Errorf("tool-declared scheme not picked up: %+v", merged.Config().Scheme)
	}
	if len(merged.Config().Scheme.Scopes) != 1 || merged.Config().Scheme.Scopes[0] != "calendar.read" {
		t.Errorf("tool-declared scopes lost: %v", merged.Config().Scheme.Scopes)
	}
}

func TestApplyInit_AuthEmptyTypeIsNoop(t *testing.T) {
	t.Parallel()
	rt := NewTool(&ToolConfig{ToolName: "no_type_tool"}, nil)

	var calls int
	rt.SetAuthHandlerNotifier(func(string, tool.AuthResponseHandler) {
		calls++
	})

	rt.applyInit(&remotetool.InitParams{
		ToolName: "no_type_tool",
		Auth:     &remotetool.AuthDeclaration{},
	}, false)

	if rt.AuthHandler() != nil {
		t.Error("expected no handler for AuthDeclaration with empty Type")
	}
	if calls != 1 {
		t.Errorf("notify calls = %d, want 1 (for the nil-handler transition)", calls)
	}
}

func TestApplyInit_AuthWithoutSchemeStillBuildsHandler(t *testing.T) {
	t.Parallel()
	rt := NewTool(&ToolConfig{ToolName: "broken_tool"}, nil)

	rt.applyInit(&remotetool.InitParams{
		ToolName: "broken_tool",
		Auth:     &remotetool.AuthDeclaration{Type: "oauth2"},
	}, false)

	if rt.AuthHandler() == nil {
		t.Fatal("expected handler even with Scheme nil")
	}
	if rt.AuthHandler().Config().Type != "oauth2" {
		t.Errorf("type = %q", rt.AuthHandler().Config().Type)
	}
}

func TestToolRegisterWithSet(t *testing.T) {
	t.Parallel()
	cfg := &ToolConfig{
		ToolName:    "remote_tool",
		Description: "A remote tool",
	}
	rt := NewTool(cfg, nil)

	ts := tool.NewSet()
	if err := ts.Register(rt); err != nil {
		t.Fatal(err)
	}

	got := ts.GetTool("remote_tool")
	if got == nil {
		t.Fatal("tool not found in set")
	}
	if got.Name() != "remote_tool" {
		t.Errorf("name = %q", got.Name())
	}
}

func TestApplyInit_RejectsWrongToolName(t *testing.T) {
	t.Parallel()

	siblingMismatchRequest := &remotetool.InitParams{
		ToolName:     "sibling_tool",
		Instructions: "Sibling's instructions — must not leak",
		Parameters: map[string]any{
			"properties": map[string]any{
				"sibling_field": map[string]any{"type": "string"},
			},
			"required": []any{"sibling_field"},
		},
	}
	emptyToolNameBroadcast := &remotetool.InitParams{
		ToolName:     "",
		Instructions: "Legacy broadcast instructions",
		Parameters: map[string]any{
			"properties": map[string]any{
				"legacy_field": map[string]any{"type": "string"},
			},
		},
	}
	exactMatchRequest := &remotetool.InitParams{
		ToolName:     "my_tool",
		Instructions: "Refreshed instructions",
		Parameters: map[string]any{
			"properties": map[string]any{
				"refreshed_field": map[string]any{"type": "string"},
			},
		},
	}

	cases := []struct {
		name             string
		incoming         *remotetool.InitParams
		fromRequest      bool
		wantInstructions string
	}{
		{
			name:             "explicit mismatch on init_request is rejected",
			incoming:         siblingMismatchRequest,
			fromRequest:      true,
			wantInstructions: "",
		},
		{
			name:             "explicit mismatch on broadcast is rejected",
			incoming:         siblingMismatchRequest,
			fromRequest:      false,
			wantInstructions: "",
		},
		{
			name:             "empty ToolName accepted for legacy broadcast paths",
			incoming:         emptyToolNameBroadcast,
			fromRequest:      false,
			wantInstructions: "Legacy broadcast instructions",
		},
		{
			name:             "exact match on init_request is accepted",
			incoming:         exactMatchRequest,
			fromRequest:      true,
			wantInstructions: "Refreshed instructions",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := &ToolConfig{
				ToolName:    "my_tool",
				Description: "Tool under test",
			}
			rt := NewTool(cfg, nil)

			rt.applyInit(tc.incoming, tc.fromRequest)

			if rt.Instructions() != tc.wantInstructions {
				t.Errorf("instructions = %q, want %q", rt.Instructions(), tc.wantInstructions)
			}
		})
	}
}
