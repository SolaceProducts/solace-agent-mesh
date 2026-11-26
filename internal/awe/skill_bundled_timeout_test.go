// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/str"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/remote"
)

func TestSkillBundledToolDefersTimeoutToManifest(t *testing.T) {
	svc := newTestServices()
	brk := svc.Broker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}

	ag := agent.New(agent.AgentConfig{Name: "bundled-timeout-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}

	const namespace = "bundledtimeout"
	exec := remote.NewExecutor(brk, namespace, ag.Name())
	if err := exec.Start(t.Context()); err != nil {
		t.Fatalf("start executor: %v", err)
	}
	t.Cleanup(func() { exec.Stop() })

	const toolName = "sam-test-harness__generate_test_plan"
	q, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	if subErr := brk.Subscribe(q, remotetool.InvokeTopic(namespace, toolName)); subErr != nil {
		t.Fatalf("subscribe: %v", subErr)
	}

	r := &skillToolRegistrar{
		agent:    ag,
		executor: exec,
		tools:    ag.Tools(),
		logger:   slog.Default(),
	}
	r.RegisterSkillBundledTool(remotetool.SkillBundledTool{
		ToolName:    toolName,
		Description: "Generate a structured test plan",
	})

	registered, ok := ag.Tools().GetTool(toolName).(tool.BuiltinTool)
	if !ok {
		t.Fatalf("%s not registered as an executable tool", toolName)
	}
	if got := registered.(*remote.Tool).TimeoutSeconds(); got != 0 {
		t.Errorf("registered ToolConfig.TimeoutSeconds = %d, want 0 (defer to manifest)", got)
	}

	go func() {
		_, _ = registered.Execute(t.Context(), map[string]any{
			"componentName": "my-agent",
			"componentKind": "agent",
		}, &tool.Context{AgentName: ag.Name(), UserID: "u1", SessionID: "s1"})
	}()

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatalf("receive invoke: %v", err)
	}

	var envelope struct {
		Params remotetool.InvokeParams `json:"params"`
	}
	if err := json.Unmarshal(msg.Payload, &envelope); err != nil {
		t.Fatalf("unmarshal invoke: %v", err)
	}

	if got := envelope.Params.TimeoutSeconds; got != 0 {
		t.Errorf("wire timeout_seconds = %d, want 0 so the skill's manifest timeout_seconds wins at the STR", got)
	}

	wantTTL := time.Duration(str.DefaultRemoteToolTimeoutSeconds)*time.Second + exec.InvokeBuffer()
	if msg.TTL != wantTTL {
		t.Errorf("invoke TTL = %v, want %v (%ds default + %v invoke buffer)",
			msg.TTL, wantTTL, str.DefaultRemoteToolTimeoutSeconds, exec.InvokeBuffer())
	}
}
