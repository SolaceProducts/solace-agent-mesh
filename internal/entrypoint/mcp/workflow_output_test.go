// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

func workflowCard(cardName string) *a2a.AgentCard {
	card := cardWithDisplayName(cardName, "Support Desk", "Search")
	card.Capabilities.Extensions = append(card.Capabilities.Extensions, a2a.AgentExtension{
		URI:    a2a.ExtensionURIAgentType,
		Params: map[string]any{"type": "workflow"},
	})
	return card
}

func callToolUntilFinal(t *testing.T, card *a2a.AgentCard, state a2a.TaskState, parts ...a2a.Part) mcplib.CallToolResult {
	t.Helper()
	a, brk := newDispatchingToolRegistryAdapter(t)
	a.registerAgentTools(card)
	tools := toolNamesFor(a, card.Name)
	if len(tools) != 1 {
		t.Fatalf("agent tools = %v, want exactly one", tools)
	}

	q, err := brk.CreateQueue("mcp-final-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic(dispatchTestNamespace, card.Name)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	responses := make(chan mcplib.JSONRPCMessage, 1)
	go func() {
		responses <- a.mcpServer.HandleMessage(context.Background(), []byte(`{
			"jsonrpc": "2.0",
			"id": 1,
			"method": "tools/call",
			"params": {"name": "`+tools[0]+`", "arguments": {"message": "go"}}
		}`))
	}()

	recvCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if received, _ := brk.Receive(recvCtx, q); received == nil {
		t.Fatalf("tools/call never dispatched; the handler returned %s", drainResponse(responses))
	}
	taskID, _ := trackedToolCall(t, a)

	a.OnFinalResponse(context.Background(), taskID, &a2a.Task{
		Status: a2a.TaskStatus{State: state, Message: &a2a.Message{Parts: parts}},
	}, nil)

	select {
	case resp := <-responses:
		jsonResp, ok := resp.(mcplib.JSONRPCResponse)
		if !ok {
			t.Fatalf("tools/call response is %T = %+v, want JSONRPCResponse", resp, resp)
		}
		result, ok := jsonResp.Result.(*mcplib.CallToolResult)
		if !ok {
			t.Fatalf("tools/call result is %T, want *CallToolResult", jsonResp.Result)
		}
		return *result
	case <-time.After(2 * time.Second):
		t.Fatal("tools/call never returned after the final response")
		return mcplib.CallToolResult{}
	}
}

func firstText(t *testing.T, result mcplib.CallToolResult) string {
	t.Helper()
	if len(result.Content) == 0 {
		t.Fatal("tool result has no content")
	}
	text, ok := result.Content[0].(mcplib.TextContent)
	if !ok {
		t.Fatalf("first content is %T, want TextContent", result.Content[0])
	}
	return text.Text
}

func wireFields(t *testing.T, result mcplib.CallToolResult) map[string]any {
	t.Helper()
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal tool result: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal tool result: %v", err)
	}
	return fields
}

func TestToolCall_WorkflowReturnsItsOutputAsStructuredContent(t *testing.T) {
	result := callToolUntilFinal(t, workflowCard(alphaCardName), a2a.TaskStateCompleted,
		a2a.NewDataPart(map[string]any{"recommendation": "Meridian Plus"}),
		a2a.NewTextPart(workflowSummary),
	)

	if result.IsError {
		t.Fatalf("workflow result is an error: %+v", result.Content)
	}
	if text := firstText(t, result); !strings.Contains(text, `"recommendation": "Meridian Plus"`) {
		t.Errorf("text = %q, want the workflow output", text)
	}
	structured, ok := wireFields(t, result)["structuredContent"].(map[string]any)
	if !ok || structured["recommendation"] != "Meridian Plus" {
		t.Errorf("structuredContent = %#v, want the output map", structured)
	}
}

func TestToolCall_AgentArtifactNoticeStaysOutOfTheResult(t *testing.T) {
	result := callToolUntilFinal(t, cardWithDisplayName(alphaCardName, "Support Desk", "Search"), a2a.TaskStateCompleted,
		a2a.NewDataPartWithMetadata(
			map[string]any{"filename": "report.md", "version": 0, "status": "success"},
			map[string]any{"tool_name": "_notify_artifact_save"},
		),
		a2a.NewTextPart("Done."),
	)

	if text := firstText(t, result); text != "Done." {
		t.Errorf("text = %q, want the agent's text only", text)
	}
	if _, present := wireFields(t, result)["structuredContent"]; present {
		t.Error("an agent's tool result carries structuredContent")
	}
}

func TestToolCall_FailedTaskIsAnErrorResult(t *testing.T) {
	result := callToolUntilFinal(t, cardWithDisplayName(alphaCardName, "Support Desk", "Search"), a2a.TaskStateFailed,
		a2a.NewDataPart(map[string]any{
			"a2a_signal_type": "tool_execution_error",
			"stack_trace":     "secret-stack",
		}),
		a2a.NewTextPart("Tool execution failed."),
	)

	if !result.IsError {
		t.Error("a failed task returned a non-error tool result")
	}
	if text := firstText(t, result); text != "Tool execution failed." {
		t.Errorf("text = %q, want the failure message alone", text)
	}
	if _, present := wireFields(t, result)["structuredContent"]; present {
		t.Error("a failed task carries structuredContent")
	}
}
