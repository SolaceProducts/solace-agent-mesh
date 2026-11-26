// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/webuisettings"
)

func receiveMetadata(t *testing.T, core *GatewayCore, brk *broker.DevBroker, queueName, agentName, taskID string, msg a2a.Message) map[string]any {
	t.Helper()
	return receiveMetadataWithContext(t.Context(), t, core, brk, queueName, agentName, taskID, msg, auth.NewTestSubject(auth.SubjectFields{UserID: "user-1"}))
}

func receiveMetadataWithContext(ctx context.Context, t *testing.T, core *GatewayCore, brk *broker.DevBroker, queueName, agentName, taskID string, msg a2a.Message, caller *auth.Subject, opts ...SubmitOption) map[string]any {
	t.Helper()
	queue, err := brk.CreateQueue(queueName, broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create agent queue: %v", err)
	}
	if subErr := brk.Subscribe(queue, a2a.AgentRequestTopic("test-ns", agentName)); subErr != nil {
		t.Fatalf("subscribe agent: %v", subErr)
	}

	if submitErr := core.SubmitTask(ctx, agentName, taskID, msg, caller, opts...); submitErr != nil {
		t.Fatalf("SubmitTask: %v", submitErr)
	}

	recvCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	received, err := brk.Receive(recvCtx, queue)
	if err != nil || received == nil {
		t.Fatalf("receive: err=%v msg=%v", err, received)
	}

	var req a2a.Request
	if err := json.Unmarshal(received.Payload, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	params, ok := req.Params.(map[string]any)
	if !ok {
		t.Fatalf("params type = %T, want map[string]any", req.Params)
	}
	message, ok := params["message"].(map[string]any)
	if !ok {
		t.Fatalf("message type = %T, want map[string]any", params["message"])
	}
	metadata, _ := message["metadata"].(map[string]any)
	return metadata
}

func stubMessage() a2a.Message {
	return a2a.Message{
		Kind:      "message",
		MessageID: "msg-1",
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewTextPart("Hello agent")},
		ContextID: "session-1",
	}
}

func TestGatewayCore_SubmitTask_StampsOwnConfigForEveryEntrypoint(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	core := NewGatewayCore(&recordingAdapter{}, map[string]any{
		"namespace":       "test-ns",
		"gateway_id":      "test-gw",
		"system_purpose":  "You are a Slack support agent.",
		"response_format": "Reply in Markdown.",
	})
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })

	metadata := receiveMetadata(t, core, brk, "assistant-ctx-q1", "target-agent", "task-1", stubMessage())
	if metadata["system_purpose"] != "You are a Slack support agent." {
		t.Errorf("system_purpose = %v, want the entrypoint's own configured value", metadata["system_purpose"])
	}
	if metadata["response_format"] != "Reply in Markdown." {
		t.Errorf("response_format = %v, want the entrypoint's own configured value", metadata["response_format"])
	}
}

func TestGatewayCore_SubmitTask_DBOverrideOnlyWhenServesWebUI(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	dbOverride := "You are the DB-configured web UI assistant."
	registry := webuisettings.NewRegistry(func(context.Context) (*webuisettings.Settings, error) {
		return &webuisettings.Settings{SystemPurpose: &dbOverride}, nil
	}, nil)
	if _, err := registry.Get(context.Background()); err != nil {
		t.Fatalf("prime registry: %v", err)
	}

	newCore := func(servesWebUI bool, gatewayID, queueName string) *GatewayCore {
		core := NewGatewayCore(&recordingAdapter{}, map[string]any{
			"namespace":      "test-ns",
			"gateway_id":     gatewayID,
			"system_purpose": "Slack's own configured purpose.",
		})
		core.SetServices(runtime.NewRuntime(brk, nil).Services())
		if err := core.Init(context.Background(), nil); err != nil {
			t.Fatalf("Init: %v", err)
		}
		core.SetServesWebUI(servesWebUI)
		core.SetWebuiSettingsRegistry(registry)
		if err := core.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		t.Cleanup(func() { _ = core.Stop(context.Background()) })
		return core
	}

	webUICore := newCore(true, "webui-gw", "assistant-ctx-webui-q")
	metadata := receiveMetadata(t, webUICore, brk, "assistant-ctx-webui-agent-q", "webui-target-agent", "task-webui", stubMessage())
	if metadata["system_purpose"] != dbOverride {
		t.Errorf("servesWebUI=true: system_purpose = %v, want the DB override %q", metadata["system_purpose"], dbOverride)
	}

	slackCore := newCore(false, "slack-gw", "assistant-ctx-slack-q")
	metadata = receiveMetadata(t, slackCore, brk, "assistant-ctx-slack-agent-q", "slack-target-agent", "task-slack", stubMessage())
	if metadata["system_purpose"] != "Slack's own configured purpose." {
		t.Errorf("servesWebUI=false: system_purpose = %v, want Slack's own YAML value, not the DB override", metadata["system_purpose"])
	}
}

func TestGatewayCore_SubmitTask_DoesNotOverwriteCallerSetMetadata(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	core := NewGatewayCore(&recordingAdapter{}, map[string]any{
		"namespace":       "test-ns",
		"gateway_id":      "test-gw",
		"system_purpose":  "The configured purpose.",
		"response_format": "The configured format.",
	})
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })

	msg := stubMessage()
	msg.Metadata = map[string]any{"system_purpose": "caller override"}

	metadata := receiveMetadata(t, core, brk, "assistant-ctx-caller-q", "target-agent", "task-caller", msg)
	if metadata["system_purpose"] != "caller override" {
		t.Errorf("system_purpose = %v, want the caller's own value preserved", metadata["system_purpose"])
	}
	if metadata["response_format"] != "The configured format." {
		t.Errorf("response_format = %v, want the configured value (caller did not set this key)", metadata["response_format"])
	}
}
