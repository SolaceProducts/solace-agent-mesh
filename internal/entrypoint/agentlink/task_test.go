// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestAnAgentOfferingTheProtocolGetsItWithItsLimits(t *testing.T) {
	r := newRig(t, nil, nil)
	r.offTheMesh()
	for _, offer := range [][]string{{subprotocol}, {subprotocol, "sam-agent-link.v1"}, {"sam-agent-link.v1", subprotocol}} {
		c, resp, err := r.dial(agentName, token, offer...)
		if err != nil {
			t.Fatal(err)
		}
		w := c.next(typeWelcome)
		if resp.Header.Get("Sec-WebSocket-Protocol") != subprotocol || w.Protocol != protocolVersion || w.Connection == "" ||
			w.MaxInflight != 2 || w.Limits == nil || *w.Limits != (linkLimits{MaxFrameBytes: defaultMaxFrameBytes, MaxStringBytes: 16384,
			MaxIDBytes: 128, MaxNameBytes: 256, MaxToolCallsPerModelCall: 128, MaxModelsPerTask: 32, MaxOpenPeerCalls: 1024, MaxOpenPrompts: 1024, MaxToolScopes: 64, MaxFailureMessageBytes: 2048,
			MaxFrameValues: 16384, MaxPromptQuestions: 4, MaxPromptOptions: 4, MaxPromptTextBytes: 2048, MaxQuestionHeaderChars: 12,
			MaxApprovalArgs: 64, MaxApprovalArgsBytes: 65536, MaxDescriptionBytes: 4096, MaxSkills: 32, MaxSkillDescriptionBytes: 1024,
			TaskTTLSeconds: 7200, MaxCallDepth: 10, MaxConnectionsPerAgent: 8,
			MaxToolPayloadBytes: 262144}) {
			t.Fatalf("offer %v: welcome %s", offer, w.Raw)
		}
		if peers := c.next(typePeers); peers.Peers == nil || len(*peers.Peers) != 0 {
			t.Fatalf("peers %s", peers.Raw)
		}
	}
}

func TestAnAgentOfferingOnlyTheRetiredFirstVersionIsClosedNamingTheProtocol(t *testing.T) {
	r := newRig(t, nil, nil)
	c, resp, err := r.dial(agentName, token, "sam-agent-link.v1")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Header.Get("Sec-WebSocket-Protocol") != "" {
		t.Fatalf("selected %q", resp.Header.Get("Sec-WebSocket-Protocol"))
	}
	if closeErr := c.closed(); closeErr.Code != 4400 || !strings.Contains(closeErr.Text, subprotocol) {
		t.Fatalf("closed with %d %q", closeErr.Code, closeErr.Text)
	}
}

func TestAnAgentOfferingNoKnownVersionIsClosed(t *testing.T) {
	r := newRig(t, nil, nil)
	c, _, err := r.dial(agentName, token, "sam-agent-link.v3")
	if err != nil {
		t.Fatal(err)
	}
	if closeErr := c.closed(); closeErr.Code != 4400 {
		t.Fatalf("closed with %d", closeErr.Code)
	}
}

func TestAnAgentGetsATaskWithoutTheUsersTokenOrAddresses(t *testing.T) {
	r, c := connectedRig(t, map[string]any{"share_user_profile": true})
	msg := map[string]any{"role": "user", "contextId": "chat-1", "metadata": map[string]any{
		"system_purpose": "Help with claims", "conversation_context": "earlier in the thread",
		"agent_name": agentName, "requesting_user": "bob@example.com", "run_as": "bob@example.com",
		"email_auth": map[string]any{"content_origin": "external_email", "all_pass": false, "internal_trust_tenant_id": "tenant-9"}},
		"parts": []any{
			map[string]any{"kind": "text", "text": "File my claim"},
			map[string]any{"kind": "file", "file": map[string]any{"name": "a.pdf", "mimeType": "application/pdf", "bytes": "aGk="}},
			map[string]any{"kind": "text", "text": "today"},
		}}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "T1", "method": methodSend, "params": map[string]any{"message": msg}})
	props := map[string]string{
		a2a.PropReplyTo: responseTopic("T1"), a2a.PropA2AStatusTopic: statusTopic("T1"), a2a.PropUserID: "alice@example.com",
		a2a.PropTraceID: "trace-1", a2a.PropAuthToken: "tok:alice:T1", "trigger": "interactive",
		a2a.PropA2AUserConfig: `{"user_profile":{"name":"Alice"},"user_timezone":"Europe/Paris","_enterprise_capabilities":["agent:*:invoke"]}`,
	}
	if err := r.brk.PublishGuaranteed(t.Context(), a2a.AgentRequestTopic(testNS, agentName), &broker.Message{Payload: body, UserProperties: props}); err != nil {
		t.Fatal(err)
	}
	f := c.next(typeTask)
	task := f.Task
	if f.TaskID != "T1" || f.Delivery == 0 || task == nil || task.ContextID != "chat-1" || task.Text != "File my claim\ntoday" ||
		len(task.Files) != 1 || task.Files[0].Name != "a.pdf" || task.Files[0].Bytes != "aGk=" || task.SessionBehavior != sessionSession ||
		task.User.ID != "alice@example.com" || task.TraceID != "trace-1" || task.Trigger != "interactive" || task.CalledBy != "" ||
		task.RequestContext.SystemPurpose != "Help with claims" || task.RequestContext.ConversationContext != "earlier in the thread" ||
		task.RequestContext.UserTimezone != "Europe/Paris" || !strings.Contains(task.RequestContext.UserProfile, `"Alice"`) ||
		len(task.Metadata) != 2 || task.Metadata["agent_name"] != agentName || task.Workflow != nil ||
		task.Metadata["email_auth"].(map[string]any)["content_origin"] != "external_email" {
		t.Fatalf("task %s", f.Raw)
	}
	for _, hidden := range []string{"tok:alice:T1", "gw1", "_enterprise_capabilities", "agent:*:invoke", "bob@example.com", "tenant-9"} {
		if strings.Contains(string(f.Raw), hidden) {
			t.Fatalf("the agent received %q: %s", hidden, f.Raw)
		}
	}
}

func TestTheUsersProfileIsWithheldUnlessTheAgentIsTrustedWithIt(t *testing.T) {
	for share, want := range map[bool]string{false: "", true: "Alice"} {
		cfg := map[string]any{}
		if share {
			cfg["share_user_profile"] = true
		}
		r, c := connectedRig(t, cfg)
		r.submit("T1", "tok:alice:T1", map[string]string{a2a.PropA2AUserConfig: `{"user_profile":{"name":"Alice","email":"alice@example.com"},"user_timezone":"Europe/Paris"}`})
		f := c.next(typeTask)
		if profile := f.Task.RequestContext.UserProfile; !strings.Contains(profile, want) || (want == "" && profile != "") ||
			f.Task.RequestContext.UserTimezone != "Europe/Paris" {
			t.Fatalf("share_user_profile %v: task %s", share, f.Raw)
		}
		if !share && strings.Contains(string(f.Raw), "Alice") {
			t.Fatalf("the profile reached the agent: %s", f.Raw)
		}
	}
}

func TestAWorkflowNodeTaskCarriesItsInputAndNode(t *testing.T) {
	r, c := connectedRig(t, nil)
	input := base64.StdEncoding.EncodeToString([]byte(`{"claimId":"C-7","amount":120}`))
	msg := map[string]any{"role": "user", "parts": []any{
		map[string]any{"kind": "data", "data": map[string]any{"type": "structured_invocation_request", "workflow_name": "claims-intake", "node_id": "coverage"}},
		map[string]any{"kind": "file", "file": map[string]any{"name": "input.json", "mimeType": "application/json", "bytes": input}},
		map[string]any{"kind": "text", "text": "Check coverage"},
	}}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "T1", "method": methodSend, "params": map[string]any{"message": msg}})
	props := map[string]string{a2a.PropReplyTo: responseTopic("T1"), a2a.PropUserID: "alice@example.com", a2a.PropAuthToken: "tok:alice:T1"}
	if err := r.brk.PublishGuaranteed(t.Context(), a2a.AgentRequestTopic(testNS, agentName), &broker.Message{Payload: body, UserProperties: props}); err != nil {
		t.Fatal(err)
	}
	task := c.next(typeTask).Task
	in, _ := task.Input.(map[string]any)
	if task.Workflow == nil || task.Workflow.Name != "claims-intake" || task.Workflow.NodeID != "coverage" || in["claimId"] != "C-7" ||
		task.Text != "Check coverage" || len(task.Files) != 0 || task.SessionBehavior != sessionRun {
		t.Fatalf("task %+v", task)
	}
}

func TestACancelReachesTheAgentAsACancelFrame(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	r.cancel("T1")
	if f := c.next(typeCancel); f.TaskID != "T1" || f.Delivery != 0 {
		t.Fatalf("%s", f.Raw)
	}
	waitFor(t, func() bool { return r.brk.acks.Load() == 2 })
}

func TestEveryEventKindPublishesWhatANativeAgentPublishes(t *testing.T) {
	const model = "openai/gpt-4o"
	classify := func(sp *agent.StatusPublisher) { sp.ClassifyModel(agent.ModelFacts(model, "openai", "")) }
	for name, tc := range map[string]struct {
		event  map[string]any
		native func(context.Context, *agent.StatusPublisher)
	}{
		eventModelStarted: {
			event: map[string]any{"kind": eventModelStarted, "model": model},
			native: func(ctx context.Context, sp *agent.StatusPublisher) {
				classify(sp)
				sp.PublishLLMInvocation(ctx, &llm.ChatRequest{}, model)
			},
		},
		eventModelFinished: {
			event: map[string]any{"kind": eventModelFinished, "model": model, "text": "Looking it up",
				"toolCalls": []any{map[string]any{"id": "c1", "name": "lookup", "args": map[string]any{"q": "policy", "limit": 3}}},
				"usage":     map[string]any{"input": 10, "output": 2, "cachedInput": 4, "cacheWrite": 1, "reasoning": 1}},
			native: func(ctx context.Context, sp *agent.StatusPublisher) {
				classify(sp)
				sp.PublishLLMResponse(ctx, model, "Looking it up",
					&llm.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12, CachedTokens: 4, CacheWriteTokens: 1, CacheWritesReported: true, ReasoningTokens: 1},
					agent.LLMResponseToolCall{Name: "lookup", ID: "c1", Args: map[string]any{"q": "policy", "limit": 3}})
			},
		},
		eventToolStarted: {
			event: map[string]any{"kind": eventToolStarted, "callId": "c1", "name": "lookup", "args": map[string]any{"q": "policy"}},
			native: func(ctx context.Context, sp *agent.StatusPublisher) {
				sp.PublishToolInvocationStart(ctx, "lookup", map[string]any{"q": "policy"}, "c1", "")
			},
		},
		eventToolFinished: {
			event: map[string]any{"kind": eventToolFinished, "callId": "c1", "name": "lookup", "result": map[string]any{"rows": 2}},
			native: func(ctx context.Context, sp *agent.StatusPublisher) {
				sp.PublishToolResult(ctx, "lookup", map[string]any{"rows": 2}, "c1")
			},
		},
		"tool_finished failed": {
			event: map[string]any{"kind": eventToolFinished, "callId": "c1", "name": "lookup", "result": "no such table", "failed": true},
			native: func(ctx context.Context, sp *agent.StatusPublisher) {
				sp.PublishToolResult(ctx, "lookup", tool.PublishedResultData(tool.Error("no such table")), "c1")
			},
		},
		eventProgress: {
			event: map[string]any{"kind": eventProgress, "text": "Looking up the policy"},
			native: func(ctx context.Context, sp *agent.StatusPublisher) {
				sp.PublishProgressUpdate(ctx, "Looking up the policy")
			},
		},
		eventText: {
			event:  map[string]any{"kind": eventText, "text": "The claim is "},
			native: func(ctx context.Context, sp *agent.StatusPublisher) { sp.PublishText(ctx, "The claim is ") },
		},
		eventThinking: {
			event: map[string]any{"kind": eventThinking, "text": "checking the dates", "done": true},
			native: func(ctx context.Context, sp *agent.StatusPublisher) {
				sp.PublishGenericSignal(ctx, a2a.ThinkingContentData{Type: string(a2a.SignalThinkingContent), Content: "checking the dates", IsComplete: true},
					string(a2a.SignalThinkingContent))
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			r, c := connectedRig(t, nil)
			r.taskOn(c, "T1", nil)
			published := r.listen(statusTopic("T1"))
			c.send(event("T1", tc.event))
			got := recv(t, published)
			sameMessage(t, got, r.nativeStatus("T1", tc.native))
			if got.UserProperties["publisherId"] != agentName || got.UserProperties["eventSeq"] != "1" {
				t.Fatalf("properties %v", got.UserProperties)
			}
		})
	}
}

func TestAFailedModelCallPublishesNothingOfItsOwnButStampsTheModel(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	published := r.listen(statusTopic("T1"))
	c.send(event("T1", map[string]any{"kind": eventModelFailed, "model": "anthropic/claude-sonnet-4", "category": "rate_limit", "provider": "anthropic"}))
	none(t, published)
	c.send(event("T1", map[string]any{"kind": eventProgress, "text": "retrying"}))
	if ev := signalOf(t, recv(t, published), nil); ev.Metadata[a2a.MetadataModelFamily] == nil || ev.Metadata[a2a.MetadataModelProvider] != "anthropic" {
		t.Fatalf("metadata %v", ev.Metadata)
	}
}

func TestToolCallsOfOneModelCallAreGroupedAsParallel(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	published := r.listen(statusTopic("T1"))
	c.send(event("T1", map[string]any{"kind": eventModelFinished, "model": "gpt-4o", "toolCalls": []any{
		map[string]any{"id": "c1", "name": "lookup"}, map[string]any{"id": "c2", "name": "lookup"}}}))
	recv(t, published)
	var groups []*string
	for _, id := range []string{"c1", "c2", "c3"} {
		c.send(event("T1", map[string]any{"kind": eventToolStarted, "callId": id, "name": "lookup"}))
		var start a2a.ToolInvocationStartData
		signalOf(t, recv(t, published), &start)
		groups = append(groups, start.ParallelGroupID)
	}
	if groups[0] == nil || groups[1] == nil || *groups[0] != *groups[1] || !strings.HasPrefix(*groups[0], "llm_batch_") || groups[2] != nil {
		t.Fatalf("parallel groups %v %v %v", groups[0], groups[1], groups[2])
	}
}

func TestSpendIsRecordedFromModelEventsUnderTheTasksUser(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	ledger := r.listen(a2a.SpendEventTopic(testNS))
	before := time.Now().UnixMilli()
	c.send(event("T1", map[string]any{"kind": eventModelFinished, "model": "gpt-4o", "pricingKey": "gpt-4o-standard",
		"usage": map[string]any{"input": 10, "output": 2, "cachedInput": 4, "cacheWrite": 1}}))
	var filed map[string]any
	if err := json.Unmarshal(recv(t, ledger).Payload, &filed); err != nil {
		t.Fatal(err)
	}
	tokens, _ := filed["tokens"].(map[string]any)
	if filed["agent_name"] != agentName || filed["user_sub"] != "alice" || filed["task_id"] != "T1" || filed["trace_id"] != "trace-1" ||
		filed["model_name"] != "gpt-4o" || filed["model_key"] != "gpt-4o-standard" || filed["root_kind"] != string(a2a.RootKindAgent) ||
		filed["cache_writes_reported"] != true || filed["surface"] != "agent" ||
		tokens["uncached_input"] != 6.0 || tokens["output"] != 2.0 || tokens["cache_read"] != 3.0 || tokens["cache_write"] != 1.0 {
		t.Fatalf("filed %v", filed)
	}
	if completed, _ := filed["completed_time"].(float64); int64(completed) < before {
		t.Fatalf("completed_time %v", filed["completed_time"])
	}
	c.send(event("T1", map[string]any{"kind": eventModelFinished, "model": "gpt-4o"}))
	none(t, ledger)
}

func TestAnAgentCannotFileSpendOfItsOwnOrForAnotherTask(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	ledger := r.listen(a2a.SpendEventTopic(testNS))
	c.send(map[string]any{"type": "spend", "event": map[string]any{"schema_version": 1, "task_id": "T1", "model_name": "gpt-4o",
		"user_sub": "mallory@example.com", "tokens": map[string]any{"uncached_input": 1000000}}})
	c.refused(errCodeUnknownType, "")
	c.send(event("T-other", map[string]any{"kind": eventModelFinished, "model": "gpt-4o", "usage": map[string]any{"input": 10}}))
	c.refused(errCodeUnknownTask, "T-other")
	none(t, ledger)
}

func finalTask(t *testing.T, m *broker.Message) a2a.Task {
	t.Helper()
	var env struct {
		Result a2a.Task   `json:"result"`
		Error  *a2a.Error `json:"error"`
	}
	if err := json.Unmarshal(m.Payload, &env); err != nil || env.Error != nil {
		t.Fatalf("final answer %s: %v", m.Payload, err)
	}
	return env.Result
}

func TestTheFinalTaskCarriesTokenUsageSummedFromModelEvents(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	answers := r.listen(responseTopic("T1"))
	c.send(event("T1", map[string]any{"kind": eventModelFinished, "model": "gpt-4o", "usage": map[string]any{"input": 10, "output": 2, "cachedInput": 4, "cacheWrite": 1, "reasoning": 1}}))
	c.send(event("T1", map[string]any{"kind": eventModelFinished, "model": "anthropic/claude-sonnet-4", "usage": map[string]any{"input": 5, "output": 1}}))
	c.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateCompleted, "text": "Filed.",
		"files": []any{map[string]any{"name": "receipt.txt", "mimeType": "text/plain", "bytes": base64.StdEncoding.EncodeToString([]byte("ok"))}},
		"data":  map[string]any{"claimId": "C-7"}})
	m := recv(t, answers)
	task := finalTask(t, m)
	parts := task.Status.Message.Parts
	usage, _ := task.Metadata["token_usage"].(map[string]any)
	byModel, _ := usage["by_model"].(map[string]any)
	gpt, _ := byModel["gpt-4o"].(map[string]any)
	if task.ID != "T1" || task.Status.State != a2a.TaskStateCompleted || task.Metadata["agent_name"] != agentName ||
		len(parts) != 3 || parts[0].Kind != "file" || parts[0].File.Name != "receipt.txt" || parts[1].Kind != "data" || parts[2].Text != "Filed." ||
		usage["total_input_tokens"] != 15.0 || usage["total_output_tokens"] != 3.0 || usage["total_cached_input_tokens"] != 4.0 ||
		usage["total_cache_write_tokens"] != 1.0 || usage["total_reasoning_tokens"] != 1.0 || usage["total_tokens"] != 18.0 ||
		usage["last_input_tokens"] != 5.0 || len(byModel) != 2 || gpt["input_tokens"] != 10.0 || gpt["cache_write_tokens"] != 1.0 {
		t.Fatalf("final task %s", m.Payload)
	}
	if m.UserProperties[a2a.PropDelegatingAgentName] != agentName || m.UserProperties[a2a.PropUserID] != "alice@example.com" ||
		m.UserProperties[a2a.PropTraceID] != "trace-1" || m.UserProperties["publisherId"] != agentName || m.UserProperties["eventSeq"] != "3" ||
		m.UserProperties[a2a.PropAuthToken] != "" {
		t.Fatalf("properties %v", m.UserProperties)
	}
	c.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateCompleted, "text": "again"})
	c.refused(errCodeUnknownTask, "T1")
}

func TestAFailedModelCallEndsTheTaskWithItsCategoryAndUsage(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	answers := r.listen(responseTopic("T1"))
	c.send(event("T1", map[string]any{"kind": eventModelFinished, "model": "gpt-4o", "usage": map[string]any{"input": 7, "output": 1}}))
	c.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateFailed, "error": map[string]any{"category": "rate_limit", "provider": "openai"}})
	var resp a2a.Response
	if err := json.Unmarshal(recv(t, answers).Payload, &resp); err != nil || resp.Error == nil {
		t.Fatalf("%+v %v", resp, err)
	}
	data, _ := resp.Error.Data.(map[string]any)
	usage, _ := data["token_usage"].(map[string]any)
	if resp.Error.Code != codeInternalError || data[a2a.ErrorDataLLMErrorCategory] != "rate_limit" || data[a2a.ErrorDataLLMProvider] != "openai" ||
		usage["total_input_tokens"] != 7.0 || usage["by_model"] != nil || !strings.Contains(resp.Error.Message, "rate_limit") {
		t.Fatalf("error %+v", resp.Error)
	}
}

func TestACanceledResultIsTheCancelledTaskANativeAgentSends(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	answers := r.listen(responseTopic("T1"))
	c.send(event("T1", map[string]any{"kind": eventModelFinished, "model": "gpt-4o", "usage": map[string]any{"input": 7, "output": 1}}))
	c.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateCanceled})
	task := finalTask(t, recv(t, answers))
	usage, _ := task.Metadata["token_usage"].(map[string]any)
	if task.Status.State != a2a.TaskStateCancelled || task.Status.Message.Parts[0].Text != "Task was cancelled." ||
		task.Status.Message.MessageID != "msg-T1-cancelled" || usage["total_input_tokens"] != 7.0 || usage["by_model"] != nil {
		t.Fatalf("task %+v", task)
	}
}

func TestARunBasedTaskEndsWithAFinalStatusEventBeforeItsAnswer(t *testing.T) {
	r, c := connectedRig(t, nil)
	status := r.listen(statusTopic("T1"))
	answers := r.listen(responseTopic("T1"))
	msg := map[string]any{"role": "user", "metadata": map[string]any{"sessionBehavior": "RUN_BASED"}, "parts": []any{map[string]any{"kind": "text", "text": "hi"}}}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "T1", "method": methodSend, "params": map[string]any{"message": msg}})
	props := map[string]string{a2a.PropReplyTo: responseTopic("T1"), a2a.PropA2AStatusTopic: statusTopic("T1"), a2a.PropUserID: "alice@example.com",
		a2a.PropAuthToken: "tok:alice:T1", "delegating_agent_name": "orchestrator"}
	if err := r.brk.PublishGuaranteed(t.Context(), a2a.AgentRequestTopic(testNS, agentName), &broker.Message{Payload: body, UserProperties: props}); err != nil {
		t.Fatal(err)
	}
	f := c.next(typeTask)
	if f.Task.SessionBehavior != sessionRun || f.Task.CalledBy != "orchestrator" {
		t.Fatalf("task %s", f.Raw)
	}
	c.send(map[string]any{"type": typeAck, "delivery": f.Delivery})
	c.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateCompleted, "text": "done"})
	if ev := signalOf(t, recv(t, status), nil); !ev.Final || ev.Status.Message.Parts[0].Text != "done" {
		t.Fatalf("final status %+v", ev)
	}
	recv(t, answers)
}

func TestAResultThatCouldNotBePublishedCanBeSentAgain(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	answers := r.listen(responseTopic("T1"))
	r.brk.failTopic.Store(responseTopic("T1"))
	c.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateCompleted, "text": "done"})
	c.refused(errCodePublishFailed, "T1")
	r.brk.failTopic.Store("")
	c.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateCompleted, "text": "done"})
	recv(t, answers)
}

func TestUnknownFrameTypesAndEventKindsAreRefused(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	published := r.listen(statusTopic("T1"))
	for _, f := range []map[string]any{
		{"type": "teleport"},
		{"type": "reply", "taskId": "T1", "kind": "status", "payload": json.RawMessage(request("T1"))},
		{"type": "card", "card": map[string]any{"name": agentName}},
		{"type": "card_request", "requestId": "r1"},
		{"type": "peer_request", "peer": peerName, "parentTaskId": "T1", "subtaskId": "S1", "payload": json.RawMessage(request("S1"))},
	} {
		c.send(f)
		c.refused(errCodeUnknownType, "")
	}
	c.send(event("T1", map[string]any{"kind": "telepathy", "text": "x"}))
	c.refused(errCodeUnknownType, "T1")
	none(t, published)
}

func TestMalformedEventsAndResultsAreRefusedWithoutPublishing(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	published := r.listen(statusTopic("T1"))
	answers := r.listen(responseTopic("T1"))
	for _, ev := range []map[string]any{
		{"kind": eventModelStarted},
		{"kind": eventToolStarted, "name": "lookup"},
		{"kind": eventToolStarted, "callId": "c1", "name": "lookup", "args": "not an object"},
		{"kind": eventModelFinished, "model": "gpt-4o", "toolCalls": []any{map[string]any{"id": "c1", "name": "x", "args": []any{1}}}},
		{"kind": eventModelFinished, "model": "gpt-4o", "usage": map[string]any{"input": -5}},
	} {
		c.send(event("T1", ev))
		c.refused(errCodeBadMessage, "T1")
	}
	c.send(map[string]any{"type": typeEvent, "taskId": "T1", "event": "progress"})
	c.refused(errCodeBadMessage, "T1")
	for _, res := range []map[string]any{
		{"type": typeResult, "taskId": "T1", "state": "working"},
		{"type": typeResult, "taskId": "T1", "state": stateCompleted, "files": []any{map[string]any{"name": "a", "bytes": "%%%"}}},
	} {
		c.send(res)
		c.refused(errCodeBadMessage, "T1")
	}
	c.send(map[string]any{"type": typeResult, "taskId": 7})
	c.refused(errCodeBadMessage, "")
	none(t, published)
	none(t, answers)
}

type scopeAuthorizer struct{ auth.NoopAgentAuthorizer }

func (scopeAuthorizer) AuthorizeAgentScopes(_ context.Context, identity *auth.Identity, required []string) error {
	for _, scope := range required {
		if identity == nil || !strings.Contains(strings.Join(identity.Scopes, " "), scope) {
			return errors.New("missing scope")
		}
	}
	return nil
}

func TestTheScopesADescribedAgentRequiresAreEnforcedBeforeDelivery(t *testing.T) {
	r := newRig(t, nil, scopeAuthorizer{})
	c := r.connect()
	c.send(map[string]any{"type": typeDescribe, "agent": map[string]any{"requiredScopes": []any{"agent:claims:special"}}})
	r.startConsuming(c, agentName)
	answers := r.listen(responseTopic("T1"))
	r.submit("T1", "tok:alice:T1", nil)
	if m := recv(t, answers); !strings.Contains(string(m.Payload), msgAccessDenied) {
		t.Fatalf("refusal %s", m.Payload)
	}
	c.quiet()
}

func TestAUserWhoHoldsTheRequiredScopeGetsTheTaskUnlessTheRequestNarrowsItAway(t *testing.T) {
	r := newRig(t, nil, scopeAuthorizer{})
	c := r.connect()
	c.send(map[string]any{"type": typeDescribe, "agent": map[string]any{"requiredScopes": []any{"agent:claims:use"}}})
	r.startConsuming(c, agentName)
	r.taskOn(c, "T1", nil)

	answers := r.listen(responseTopic("T2"))
	r.submit("T2", "tok:alice:T2", map[string]string{a2a.PropA2AUserConfig: `{"_enterprise_capabilities":["agent:*:invoke"]}`})
	if m := recv(t, answers); !strings.Contains(string(m.Payload), msgAccessDenied) {
		t.Fatalf("a request narrowed past the required scope was not refused: %s", m.Payload)
	}
	c.quiet()
}

func TestAnAgentThatRetiresItsCardKeepsItsRequiredScopes(t *testing.T) {
	r := newRig(t, nil, scopeAuthorizer{})
	c := r.connect()
	c.send(map[string]any{"type": typeDescribe, "agent": map[string]any{"requiredScopes": []any{"agent:claims:special"}}})
	r.startConsuming(c, agentName)
	c.send(map[string]any{"type": typeRetire})
	c.send(map[string]any{"type": "no-such-type"})
	c.next(typeError)
	answers := r.listen(responseTopic("T1"))
	r.submit("T1", "tok:alice:T1", nil)
	if m := recv(t, answers); !strings.Contains(string(m.Payload), msgAccessDenied) {
		t.Fatalf("refusal %s", m.Payload)
	}
	c.quiet()
}

func TestAFrameOverTheSizeLimitClosesTheConnection(t *testing.T) {
	r := newRig(t, nil, nil)
	c := r.connect()
	_ = c.ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"event","event":{"kind":"text","text":"`+strings.Repeat("x", defaultMaxFrameBytes)+`"}}`))
	if closeErr := c.closed(); closeErr.Code != 1009 {
		t.Fatalf("closed with %d", closeErr.Code)
	}
}

func TestAFrameHoldingMoreJSONValuesThanTheLimitIsRefusedNamingItsTask(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	status := r.listen(statusTopic("T1"))
	values := func(n int) string { return strings.TrimSuffix(strings.Repeat(`"a",`, n), ",") }
	const ownValues = 1 + 2*2 + 2 + 2*3 + 2
	frame := func(elements int) []byte {
		return []byte(`{"type":"event","taskId":"T1","event":{"kind":"tool_finished","callId":"c1","name":"lookup","result":[` + values(elements) + `]}}`)
	}
	if err := c.ws.WriteMessage(websocket.TextMessage, frame(maxFrameValues-ownValues+1)); err != nil {
		t.Fatal(err)
	}
	if f := c.refused(errCodeLimit, "T1"); f.Message != msgTooManyValues {
		t.Fatalf("%s", f.Raw)
	}
	none(t, status)
	if err := c.ws.WriteMessage(websocket.TextMessage, frame(maxFrameValues-ownValues)); err != nil {
		t.Fatal(err)
	}
	recv(t, status)

	padding := `"padding":[` + values(maxFrameValues) + `]`
	for _, tc := range []struct{ frame, ref string }{
		{`{"type":"ack","delivery":7,` + padding + `}`, "7"},
		{`{"type":"peer_call","subtaskId":"S1",` + padding + `}`, "S1"},
		{`{"type":"peer_call","subtaskId":"a/b",` + padding + `}`, ""},
		{`{"type":"describe",` + padding + `}`, ""},
	} {
		if err := c.ws.WriteMessage(websocket.TextMessage, []byte(tc.frame)); err != nil {
			t.Fatal(err)
		}
		c.refused(errCodeLimit, tc.ref)
	}
}

func TestTheValuesOfAJSONTextAreCountedAsTheSDKCountsThem(t *testing.T) {
	for _, tc := range []struct {
		text   string
		values int
	}{
		{`{}`, 1},
		{`{"a":[1,true,null,"x",{"b":-1.5e3}]}`, 10},
		{`["a\"b,c", "{[", 12 , false]`, 5},
		{` [ ] `, 1},
	} {
		if !withinValues([]byte(tc.text), tc.values) || withinValues([]byte(tc.text), tc.values-1) {
			t.Fatalf("%s does not hold exactly %d values", tc.text, tc.values)
		}
	}
}

func TestAnEventsNamesIdsAndToolCallsAreBounded(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	long := strings.Repeat("m", maxNameBytes+1)
	longID := strings.Repeat("c", maxIDBytes+1)
	calls := make([]any, maxToolCallsPerModelCall+1)
	for i := range calls {
		calls[i] = map[string]any{"id": fmt.Sprintf("c%d", i), "name": "lookup"}
	}
	for _, tc := range []struct {
		event map[string]any
		code  string
	}{
		{map[string]any{"kind": eventModelStarted, "model": long}, errCodeBadMessage},
		{map[string]any{"kind": eventModelFinished, "model": "gpt-4o", "pricingKey": long}, errCodeBadMessage},
		{map[string]any{"kind": eventModelFailed, "model": "gpt-4o", "provider": long}, errCodeBadMessage},
		{map[string]any{"kind": eventToolStarted, "callId": "c1", "name": long}, errCodeBadMessage},
		{map[string]any{"kind": eventToolStarted, "callId": longID, "name": "lookup"}, errCodeBadMessage},
		{map[string]any{"kind": eventModelFinished, "model": "gpt-4o", "toolCalls": []any{map[string]any{"id": longID, "name": "lookup"}}}, errCodeBadMessage},
		{map[string]any{"kind": eventModelFinished, "model": "gpt-4o", "toolCalls": calls}, errCodeLimit},
		{map[string]any{"kind": eventToolFinished, "callId": "c1", "name": "lookup", "result": strings.Repeat("r", maxToolPayloadBytes)}, errCodeLimit},
		{map[string]any{"kind": eventToolStarted, "callId": "c1", "name": "lookup", "args": map[string]any{"q": strings.Repeat("q", maxToolPayloadBytes)}}, errCodeLimit},
		{map[string]any{"kind": eventModelFinished, "model": "gpt-4o", "toolCalls": []any{
			map[string]any{"id": "c1", "name": "lookup", "args": map[string]any{"q": strings.Repeat("q", maxToolPayloadBytes/2)}},
			map[string]any{"id": "c2", "name": "lookup", "args": map[string]any{"q": strings.Repeat("q", maxToolPayloadBytes/2)}}}}, errCodeLimit},
	} {
		c.send(event("T1", tc.event))
		c.refused(tc.code, "T1")
	}
	c.send(map[string]any{"type": typePeerCall, "parentTaskId": "T1", "subtaskId": "S1", "peer": peerName, "text": "x", "callId": longID})
	c.refused(errCodeBadMessage, "S1")

	status := r.listen(statusTopic("T1"))
	for i := range maxModelsPerTask {
		c.send(event("T1", map[string]any{"kind": eventModelStarted, "model": fmt.Sprintf("model-%d", i)}))
		recv(t, status)
	}
	c.send(event("T1", map[string]any{"kind": eventModelStarted, "model": "one-too-many"}))
	c.refused(errCodeLimit, "T1")
	c.send(event("T1", map[string]any{"kind": eventModelStarted, "model": "model-0"}))
	recv(t, status)
}

func TestAToolCallForgetsItsParallelGroupOnceStarted(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	status := r.listen(statusTopic("T1"))
	parallel := func() map[string]string {
		var groups map[string]string
		r.locked(func(s *agentState) { groups = maps.Clone(s.tasks["T1"].run.parallel) })
		return groups
	}
	c.send(event("T1", map[string]any{"kind": eventModelFinished, "model": "gpt-4o", "toolCalls": []any{
		map[string]any{"id": "c1", "name": "lookup"}, map[string]any{"id": "c2", "name": "lookup"}}}))
	recv(t, status)
	c.send(event("T1", map[string]any{"kind": eventToolStarted, "callId": "c1", "name": "lookup"}))
	recv(t, status)
	if groups := parallel(); len(groups) != 1 || groups["c2"] == "" {
		t.Fatalf("parallel groups after c1 started: %v", groups)
	}
	c.send(event("T1", map[string]any{"kind": eventModelFinished, "model": "gpt-4o", "toolCalls": []any{map[string]any{"id": "c3", "name": "lookup"}}}))
	recv(t, status)
	if groups := parallel(); len(groups) != 0 {
		t.Fatalf("parallel groups after the next model call: %v", groups)
	}
}

func TestAResultSentAgainAfterPublishFailedPublishesTheFinalStatusOnceAndKeepsItsSequenceNumber(t *testing.T) {
	r, c := connectedRig(t, nil)
	status := r.listen(statusTopic("T1"))
	answers := r.listen(responseTopic("T1"))
	msg := map[string]any{"role": "user", "metadata": map[string]any{"sessionBehavior": "RUN_BASED"}, "parts": []any{map[string]any{"kind": "text", "text": "hi"}}}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "T1", "method": methodSend, "params": map[string]any{"message": msg}})
	props := map[string]string{a2a.PropReplyTo: responseTopic("T1"), a2a.PropA2AStatusTopic: statusTopic("T1"), a2a.PropUserID: "alice@example.com",
		a2a.PropAuthToken: "tok:alice:T1", "delegating_agent_name": "orchestrator"}
	if err := r.brk.PublishGuaranteed(t.Context(), a2a.AgentRequestTopic(testNS, agentName), &broker.Message{Payload: body, UserProperties: props}); err != nil {
		t.Fatal(err)
	}
	f := c.next(typeTask)
	c.send(map[string]any{"type": typeAck, "delivery": f.Delivery})

	r.brk.failTopic.Store(responseTopic("T1"))
	c.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateCompleted, "text": "done"})
	c.refused(errCodePublishFailed, "T1")
	r.brk.failTopic.Store("")
	c.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateCompleted, "text": "done"})
	if m := recv(t, answers); m.UserProperties["eventSeq"] != "2" {
		t.Fatalf("the answer sent again has eventSeq %s", m.UserProperties["eventSeq"])
	}
	if m := recv(t, status); m.UserProperties["eventSeq"] != "1" {
		t.Fatalf("final status eventSeq %s", m.UserProperties["eventSeq"])
	}
	none(t, status)
}

func TestATaskLargerThanTheFrameLimitIsRefusedToItsCallerAndNotRedelivered(t *testing.T) {
	r := newRig(t, nil, nil)
	r.adapter.cfg.MaxFrameBytes = 64 << 10
	c := r.connect()
	r.consume(c)
	answers := r.listen(responseTopic("T1"))
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "T1", "method": methodSend, "params": map[string]any{"message": map[string]any{
		"role": "user", "parts": []any{map[string]any{"kind": "text", "text": strings.Repeat("claim ", 20_000)}}}}})
	props := map[string]string{a2a.PropReplyTo: responseTopic("T1"), a2a.PropA2AStatusTopic: statusTopic("T1"), a2a.PropUserID: "alice@example.com",
		a2a.PropAuthToken: "tok:alice:T1"}
	if err := r.brk.PublishGuaranteed(t.Context(), a2a.AgentRequestTopic(testNS, agentName), &broker.Message{Payload: body, UserProperties: props}); err != nil {
		t.Fatal(err)
	}
	var resp a2a.Response
	if err := json.Unmarshal(recv(t, answers).Payload, &resp); err != nil || resp.Error == nil || !strings.Contains(resp.Error.Message, "too large") {
		t.Fatalf("%+v %v", resp.Error, err)
	}
	c.quiet()
	if r.brk.acks.Load() != 1 || r.brk.nacks.Load() != 0 {
		t.Fatalf("acks %d nacks %d", r.brk.acks.Load(), r.brk.nacks.Load())
	}
}

func TestTheTaskFrameIsTheOneTheFrameStructEncodes(t *testing.T) {
	awkward := "<b>&amp;</b> \u2028\u2029 \"quoted\" back\\slash \xff\xfe"
	task := &taskObject{ContextID: awkward, Text: awkward, Files: []taskFile{{Name: awkward, Bytes: "AA=="}},
		SessionBehavior: sessionSession, User: taskUser{ID: awkward}, Trigger: awkward, GrantedToolScopes: []string{awkward},
		Metadata: map[string]any{awkward: awkward}, Input: map[string]any{"q": awkward}, Workflow: &taskWorkflow{Name: awkward}}
	for _, taskID := range []string{"T1", awkward} {
		for _, redelivered := range []bool{false, true} {
			for _, delivery := range []uint64{1, 42, math.MaxUint64} {
				encoded, err := encodeTask(taskID, redelivered, task)
				if err != nil {
					t.Fatal(err)
				}
				want, err := json.Marshal(outFrame{Type: typeTask, Delivery: delivery, TaskID: taskID, Redelivered: redelivered, Task: task})
				if err != nil {
					t.Fatal(err)
				}
				got := encoded.frame(delivery)
				if !bytes.Equal(got, want) {
					t.Fatalf("task frame\n%s\nstruct encoding\n%s", got, want)
				}
				if int64(len(got)) > encoded.size() {
					t.Fatalf("a %d-byte frame exceeds its size bound %d", len(got), encoded.size())
				}
			}
		}
	}
}
