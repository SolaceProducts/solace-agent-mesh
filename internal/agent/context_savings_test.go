// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const withheldTestPayload = 20_000

func TestFormatToolResult_WithholdsOnlyTextItRoutedToAnArtifactInsteadOfInlining(t *testing.T) {
	agent, toolCtx := toolResultProcessorFixture(t)
	big := strings.Repeat("x", withheldTestPayload)
	r := &tool.Result{
		Status:  "success",
		Message: "one big, one small, one binary",
		DataObjects: []tool.DataObject{
			{Name: "big.txt", Content: []byte(big), MIMEType: "text/plain"},
			{Name: "small.txt", Content: []byte("tiny"), MIMEType: "text/plain"},
			{Name: "chart.png", Content: tinyPNG, MIMEType: "image/png"},
		},
	}

	text, withheld := agent.formatToolResult(context.Background(), r, toolCtx, nil)

	references := artifactsCreatedBytes(t, text)
	if want := int64(withheldTestPayload) - references; withheld != want || references <= 500 {
		t.Fatalf("withheld = %d, want the big text's %d bytes net of the %d-byte artifacts_created entries (the big text's preview, the small text's and the image's)", withheld, withheldTestPayload, references)
	}
	if !strings.Contains(text, "big.txt") || !strings.Contains(text, "tiny") {
		t.Errorf("tool message = %q, want the reference and the inlined small object", text)
	}
}

func artifactsCreatedBytes(t *testing.T, toolMessage string) int64 {
	t.Helper()
	var result map[string]json.RawMessage
	if err := json.Unmarshal([]byte(toolMessage), &result); err != nil {
		t.Fatalf("tool message is not JSON: %v", err)
	}
	return int64(len(result[artifactsCreatedKey]))
}

func TestWithheldWithinContextWindow_CapsAtTheRoomTheWindowLeavesBesideThePrompt(t *testing.T) {
	const limit = 1_000
	const window = limit * spend.BytesPerEstimatedToken
	cases := map[string]struct {
		withheld, prompt int64
		known            bool
		want             int64
	}{
		"fits beside the prompt":            {withheld: 2_999, prompt: 1_000, known: true, want: 2_999},
		"exactly fills the window":          {withheld: 3_000, prompt: 1_000, known: true, want: 3_000},
		"over the window with the prompt":   {withheld: 3_001, prompt: 1_000, known: true, want: 3_000},
		"prompt alone fills the window":     {withheld: 1_000_000, prompt: window, known: true, want: 0},
		"prompt alone exceeds the window":   {withheld: 1_000_000, prompt: window + 500, known: true, want: 0},
		"nothing withheld":                  {withheld: 0, prompt: 1_000, known: true, want: 0},
		"unknown window leaves it uncapped": {withheld: 1_000_000, prompt: window + 500, known: false, want: 1_000_000},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := withheldWithinContextWindow(c.withheld, c.prompt, limit, c.known); got != c.want {
				t.Errorf("withheldWithinContextWindow(%d, %d, %d, %v) = %d, want %d", c.withheld, c.prompt, limit, c.known, got, c.want)
			}
		})
	}
}

func TestAgentTurnSpend_CapsWhatACallAvoidsByTheModelItRunsOn(t *testing.T) {
	prompt := []llm.ChatMessage{
		{Role: "system", Content: strings.Repeat("s", 2_000)},
		{Role: "user", Content: "q"},
		{Role: "tool", Content: "ref", WithheldBytes: 10_000_000},
	}
	promptBytes := promptTextBytes(prompt)
	const gpt4oWindow = 128_000 * spend.BytesPerEstimatedToken
	cases := map[string]struct {
		model    string
		override *TaskExecutionState
		want     int64
	}{
		"agent model with a known window":    {model: "gpt-4o", want: gpt4oWindow - promptBytes},
		"agent model with an unknown window": {model: "mock-model", want: 10_000_000},
		"per-task override with its own window": {model: "gpt-4o", override: &TaskExecutionState{
			ModelOverride: &llm.BifrostConfig{Model: "override"}, ModelOverrideReportModel: "override", ModelOverrideMaxInputTokens: 1_000,
		}, want: 1_000*spend.BytesPerEstimatedToken - promptBytes},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			agent, _, _ := newTestAgent()
			agent.cfg.Model = c.model
			agent.SetContextSavingsCounting(true)
			state := &TaskExecutionState{UserProps: a2a.UserProperties{ClientID: "u1", UserID: "u1"}}
			if c.override != nil {
				state.ModelOverride = c.override.ModelOverride
				state.ModelOverrideReportModel = c.override.ModelOverrideReportModel
				state.ModelOverrideMaxInputTokens = c.override.ModelOverrideMaxInputTokens
			}

			attribution := agent.agentTurnSpend(context.Background(), state, prompt)

			if attribution == nil || attribution.AvoidedInputBytes == nil {
				t.Fatalf("agentTurnSpend booked no avoided bytes, want %d", c.want)
			}
			if got := *attribution.AvoidedInputBytes; got != c.want {
				t.Errorf("avoided bytes = %d, want %d", got, c.want)
			}
		})
	}
}

func TestFormatToolResult_CountsWhatATooWithheldItself(t *testing.T) {
	agent, toolCtx := toolResultProcessorFixture(t)
	r := tool.OK("saved", tool.WithData(map[string]any{"output_filename": "r.json", "result_preview": []any{1}}), tool.WithWithheldBytes(5_000))

	if _, withheld := agent.formatToolResult(context.Background(), r, toolCtx, nil); withheld != 5_000 {
		t.Errorf("withheld = %d, want the tool's own 5000", withheld)
	}
	if _, withheld := agent.formatToolResult(context.Background(), tool.OK("plain"), toolCtx, nil); withheld != 0 {
		t.Errorf("withheld = %d for a plain result, want 0", withheld)
	}
}

func TestFormatToolResult_CountsASpilledResultNetOfTheViewTheModelGot(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.ToolOutputLLMReturnMaxBytes = defaultToolOutputLLMReturnMaxBytes
	toolCtx, _ := ceilingTestContext()
	oversized := func() *tool.Result {
		return tool.OK("done", tool.WithData(map[string]any{"content": strings.Repeat("x", 200_000)}), tool.WithWithheldBytes(5_000))
	}
	unbounded, _, _ := newTestAgent()
	disabled := false
	unbounded.cfg.ToolResultProcessingEnabled = &disabled
	full, _ := unbounded.formatToolResult(context.Background(), oversized(), toolCtx, nil)

	view, withheld := agent.formatToolResult(context.Background(), oversized(), toolCtx, nil)
	if want := int64(len(full)-len(view)) + 5_000; withheld != want || withheld <= 190_000 {
		t.Errorf("withheld = %d, want the %d-byte result net of the %d-byte view plus the tool's own 5000 = %d", withheld, len(full), len(view), want)
	}

	if _, truncated := agent.formatToolResult(context.Background(), loadArtifactResult(strings.Repeat("row\n", 50_000), 0, 1), toolCtx, nil); truncated != 0 {
		t.Errorf("withheld = %d for a window shortened in place, want 0", truncated)
	}
	if _, unsaved := agent.formatToolResult(context.Background(), oversized(), &tool.Context{AgentName: "test-agent"}, nil); unsaved != 5_000 {
		t.Errorf("withheld = %d for a result that could not be saved, want only the tool's own 5000", unsaved)
	}
}

func TestProcessFileParts_WithholdsATextUploadHandedOverAsASummary(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.MaxInlineArtifactBytes = 2048
	scoped := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = scoped
	csv := []byte(strings.Repeat("a,b,c\n", 1_000))
	saveArtifact(t, scoped, "test-agent", "user-1", "sess-1", "big.csv", csv, "text/csv")
	saveArtifact(t, scoped, "test-agent", "user-1", "sess-1", "pic.png", tinyPNG, "image/png")
	saveArtifact(t, scoped, "test-agent", "user-1", "sess-1", "small.csv", []byte("a,b\n1,2\n"), "text/csv")

	parts := func(names ...string) a2a.Message {
		msg := a2a.Message{ContextID: "sess-1"}
		for _, n := range names {
			msg.Parts = append(msg.Parts, a2a.Part{Kind: "file", File: &a2a.FileContent{URI: "artifact://test-agent/user-1/sess-1/" + n}})
		}
		return msg
	}
	text, _, withheld := agent.processFileParts(context.Background(), parts("big.csv"), a2a.UserProperties{UserID: "user-1"}, "sess-1", "hello", -1)
	summary := strings.TrimPrefix(text, "hello")
	if want := int64(len(csv) - len(summary)); withheld != want || withheld <= 0 {
		t.Errorf("withheld = %d, want the file's %d bytes net of the %d-byte summary", withheld, len(csv), len(summary))
	}
	if _, _, none := agent.processFileParts(context.Background(), parts("pic.png", "small.csv"), a2a.UserProperties{UserID: "user-1"}, "sess-1", "hello", -1); none != 0 {
		t.Errorf("withheld = %d for a binary and an inlined upload, want 0", none)
	}

	inline := a2a.Message{ContextID: "sess-1", Parts: []a2a.Part{{Kind: "file", File: &a2a.FileContent{
		Name: "upload.csv", MIMEType: "text/csv", Bytes: base64.StdEncoding.EncodeToString(csv)}}}}
	text, _, withheld = agent.processFileParts(context.Background(), inline, a2a.UserProperties{UserID: "user-1"}, "sess-1", "hello", -1)
	if want := int64(len(csv) - len(strings.TrimPrefix(text, "hello"))); withheld != want || withheld <= 0 {
		t.Errorf("inline-bytes upload withheld = %d, want %d", withheld, want)
	}
}

func TestProcessFileParts_WithholdsAnUploadWhoseSizeOnlyTheLoadRevealed(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.MaxInlineArtifactBytes = 2048
	scoped := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = scoped
	csv := []byte(strings.Repeat("a,b,c\n", 1_000))
	loc := scoped.Resolve("test-agent", "user-1", "sess-1", "unsized.csv")
	ver, err := scoped.Inner.Save(context.Background(), loc, csv, "text/csv")
	if err != nil {
		t.Fatal(err)
	}
	if err = scoped.Inner.SaveMetadata(context.Background(), loc, ver, &artifact.VersionMetadata{MIMEType: "text/csv"}); err != nil {
		t.Fatal(err)
	}

	msg := a2a.Message{ContextID: "sess-1", Parts: []a2a.Part{{Kind: "file", File: &a2a.FileContent{URI: "artifact://test-agent/user-1/sess-1/unsized.csv"}}}}
	text, _, withheld := agent.processFileParts(context.Background(), msg, a2a.UserProperties{UserID: "user-1"}, "sess-1", "hello", -1)
	if want := int64(len(csv) - len(strings.TrimPrefix(text, "hello"))); withheld != want || withheld <= 0 {
		t.Errorf("withheld = %d, want the loaded %d bytes net of the summary (%d)", withheld, len(csv), want)
	}
}

func bigResultTool(name string) *testBuiltinTool {
	return &testBuiltinTool{
		name:        name,
		description: "returns a large result",
		params:      &tool.JSONSchema{Type: "object", Properties: map[string]any{}},
		execFn: func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
			return &tool.Result{
				Status:  tool.StatusSuccess,
				Message: "rows",
				DataObjects: []tool.DataObject{
					{Name: "rows.json", Content: []byte(strings.Repeat("r", withheldTestPayload)), MIMEType: "application/json"},
				},
			}, nil
		},
	}
}

const uncounted = int64(-1)

func withheldPerCall(calls []llm.ChatRequest) []int64 {
	out := make([]int64, len(calls))
	for i, c := range calls {
		out[i] = uncounted
		if c.Spend != nil && c.Spend.AvoidedInputBytes != nil {
			out[i] = *c.Spend.AvoidedInputBytes
		}
	}
	return out
}

func TestHandleTask_BooksTheReferencesInEachPromptAsAvoidedInputAcrossTurns(t *testing.T) {
	toolCall := func(id string) *llm.ChatResponse {
		return &llm.ChatResponse{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: id, Name: "bigquery", Arguments: `{}`}}}
	}
	agent, mock, _ := newTestAgent(
		toolCall("c1"), toolCall("c2"), &llm.ChatResponse{Content: "done", FinishReason: "stop"},
		toolCall("c3"), &llm.ChatResponse{Content: "done again", FinishReason: "stop"},
	)
	agent.svc.(*mockServices).arts = &artifact.ScopedService{Inner: artmem.New()}
	agent.cfg.MaxLLMCalls = 10
	agent.SetContextSavingsCounting(true)
	if err := agent.Tools().Register(bigResultTool("bigquery")); err != nil {
		t.Fatal(err)
	}
	props := a2a.UserProperties{ClientID: "u1", UserID: "u1"}

	for _, taskID := range []string{"task-savings-1", "task-savings-2"} {
		resp, err := agent.HandleTask(context.Background(), taskID, newUserMessage("query"), props)
		if err != nil || resp.Error != nil {
			t.Fatalf("%s: err %v, A2A error %+v", taskID, err, resp.Error)
		}
	}

	calls := mock.getCalls()
	if len(calls) != 5 {
		t.Fatalf("LLM calls = %d, want 5", len(calls))
	}
	got := withheldPerCall(calls)
	references := referencesPerCall(t, calls)
	if want := []int{0, 1, 2, 0, 1}; !slices.Equal(references, want) {
		t.Fatalf("references in each prompt = %v, want %v: nothing before the first result, then every result of this turn", references, want)
	}
	for i, c := range calls {
		var want int64
		for _, m := range c.Messages {
			if m.WithheldBytes > 0 {
				want += withheldTestPayload - artifactsCreatedBytes(t, m.Content)
			}
		}
		if got[i] != want {
			t.Errorf("call %d avoided %d bytes, want %d: every result in its prompt net of the artifacts_created list the model read", i+1, got[i], want)
		}
	}
	for i, m := range calls[3].Messages {
		if m.Role == "tool" && (m.Content != stubbedToolResult || m.WithheldBytes != 0) {
			t.Errorf("turn 2 history message %d = %q with %d withheld bytes, want the placeholder booking nothing", i, m.Content, m.WithheldBytes)
		}
	}
}

func referencesPerCall(t *testing.T, calls []llm.ChatRequest) []int {
	t.Helper()
	out := make([]int, len(calls))
	for i, c := range calls {
		for _, m := range c.Messages {
			if m.WithheldBytes > 0 {
				out[i]++
			}
		}
	}
	return out
}

func TestHandleTask_BooksNoAvoidedInputWhileCountingIsOff(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "bigquery", Arguments: `{}`}}},
		&llm.ChatResponse{Content: "done", FinishReason: "stop"},
	)
	agent.svc.(*mockServices).arts = &artifact.ScopedService{Inner: artmem.New()}
	if err := agent.Tools().Register(bigResultTool("bigquery")); err != nil {
		t.Fatal(err)
	}

	resp, err := agent.HandleTask(context.Background(), "task-no-savings", newUserMessage("query"), a2a.UserProperties{ClientID: "u1", UserID: "u1"})
	if err != nil || resp.Error != nil {
		t.Fatalf("err %v, A2A error %+v", err, resp.Error)
	}

	calls := mock.getCalls()
	if got := withheldPerCall(calls); got[0] != uncounted || got[1] != uncounted {
		t.Errorf("avoided bytes per call = %v, want neither call counted, not counted at zero", got)
	}
	for _, m := range calls[1].Messages {
		if m.WithheldBytes != 0 {
			t.Errorf("message %q marked with %d withheld bytes while counting is off", m.Role, m.WithheldBytes)
		}
	}
}

func TestBuildMessages_ReplayedHistoryBooksNoWithheldBytes(t *testing.T) {
	agent, _, _ := newTestAgent()
	sess := &session.Session{SessionID: "sess-compacted", Messages: llmMessagesToSessionMessages([]llm.ChatMessage{
		{Role: "user", Content: "q1"},
		{Role: "tool", ToolCallID: "c1", Content: "ref 1", WithheldBytes: 7_000},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "q2"},
		{Role: "tool", ToolCallID: "c2", Content: "ref 2", WithheldBytes: 3_000},
	}, "task-1")}
	messages, _ := agent.buildMessages(context.Background(), nil, sess, "q3", gatewayContext{})
	if got := withheldBytesOf(messages); got != 0 {
		t.Fatalf("withheld over the full history = %d, want 0", got)
	}

	setCompactionMetadata(sess, &CompactionMetadata{Summary: "earlier", CoveredThrough: 3})
	messages, _ = agent.buildMessages(context.Background(), nil, sess, "q3", gatewayContext{})
	if got := withheldBytesOf(messages); got != 0 {
		t.Errorf("withheld after compaction = %d, want 0", got)
	}
}

type overflowOnceLLM struct {
	*mockLLM
	overflowed atomic.Bool
}

func (c *overflowOnceLLM) ChatCompletionStream(ctx context.Context, req *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	if c.overflowed.CompareAndSwap(false, true) {
		return nil, &llm.Error{StatusCode: 400, Message: "prompt is too long: 213456 tokens > 200000 maximum", Provider: "anthropic"}
	}
	return c.mockLLM.ChatCompletionStream(ctx, req)
}

func TestHandleTask_KeepsTheUploadsWithheldBytesAcrossACompactionRetry(t *testing.T) {
	client := &overflowOnceLLM{mockLLM: newMockLLM(
		&llm.ChatResponse{Content: "summary", FinishReason: "stop", Usage: &llm.Usage{PromptTokens: 100, CompletionTokens: 10}},
		&llm.ChatResponse{Content: "done", FinishReason: "stop"},
	)}
	store := newMemSessionStore()
	agent := New(AgentConfig{Name: "test-agent", Model: "test-model", Namespace: "test-ns", MaxLLMCalls: 5})
	agent.cfg.AutoSummarization = &CompactionConfig{Enabled: true, CompactionPercentage: 0.5}
	svc := newMockServices(client, store)
	svc.arts = &artifact.ScopedService{Inner: artmem.New()}
	agent.svc = svc
	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
	agent.SetContextSavingsCounting(true)
	seedSession(store, "test-agent", "user-1", "ctx-1")

	csv := []byte(strings.Repeat("a,b,c\n", 1_000))
	msg := newUserMessage("and italy?")
	msg.Parts = append(msg.Parts, a2a.Part{Kind: "file", File: &a2a.FileContent{
		Name: "upload.csv", MIMEType: "text/csv", Bytes: base64.StdEncoding.EncodeToString(csv)}})

	resp, err := agent.HandleTask(t.Context(), "task-compaction-upload", msg, a2a.UserProperties{ClientID: "user-1", UserID: "user-1"})
	if err != nil || resp.Error != nil {
		t.Fatalf("err %v, A2A error %+v", err, resp.Error)
	}

	calls := client.getCalls()
	retried := calls[len(calls)-1]
	if retried.Spend == nil || retried.Spend.AvoidedInputBytes == nil || *retried.Spend.AvoidedInputBytes <= 0 || *retried.Spend.AvoidedInputBytes >= int64(len(csv)) {
		t.Fatalf("retried call avoided %+v, want the upload net of its summary", retried.Spend)
	}
	booked := *retried.Spend.AvoidedInputBytes
	if got := withheldOnTheTurn(retried.Messages, "and italy?"); got != booked {
		t.Errorf("rebuilt user turn carries %d withheld bytes, want the %d the call booked", got, booked)
	}
	sess, err := store.Get(t.Context(), "test-agent", "user-1", "ctx-1")
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	stored := make([]llm.ChatMessage, len(sess.Messages))
	for i, m := range sess.Messages {
		stored[i] = llm.ChatMessage{Role: m.Role, Content: m.Content, WithheldBytes: m.WithheldBytes}
	}
	if got := withheldOnTheTurn(stored, "and italy?"); got != booked {
		t.Errorf("stored user turn carries %d withheld bytes, want %d so the next turn still counts the upload", got, booked)
	}
}

func withheldOnTheTurn(messages []llm.ChatMessage, prompt string) int64 {
	for _, m := range messages {
		if m.Role == "user" && strings.Contains(m.Content, prompt) {
			return m.WithheldBytes
		}
	}
	return -1
}
