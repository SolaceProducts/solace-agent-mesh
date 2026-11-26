// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package governor

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

type usageClient struct {
	usage    *llm.Usage
	attempts int
}

func (c usageClient) ChatCompletion(context.Context, *llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{Content: "ok", Usage: c.usage, Attempts: c.attempts}, nil
}

func (c usageClient) ChatCompletionStream(context.Context, *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	ch := make(chan llm.StreamChunk, 2)
	ch <- llm.StreamChunk{ContentDelta: "ok", Attempts: c.attempts}
	ch <- llm.StreamChunk{FinishReason: "stop", Usage: c.usage, Attempts: c.attempts}
	close(ch)
	return ch, nil
}

type capturedRow struct {
	UserID            *string `db:"user_id"`
	UserSub           *string `db:"user_sub"`
	AgentID           *string `db:"agent_id"`
	AgentName         *string `db:"agent_name"`
	WorkflowID        *string `db:"workflow_id"`
	WorkflowName      *string `db:"workflow_name"`
	RootKind          *string `db:"root_kind"`
	RootID            *string `db:"root_id"`
	RootName          *string `db:"root_name"`
	EntrypointID      *string `db:"entrypoint_id"`
	EntrypointName    *string `db:"entrypoint_name"`
	TriggerKind       *string `db:"trigger_kind"`
	Surface           string  `db:"surface"`
	ModelConfigID     *string `db:"model_config_id"`
	TraceID           *string `db:"trace_id"`
	UncachedInput     int64   `db:"uncached_input_tokens"`
	Output            int64   `db:"output_tokens"`
	CacheRead         int64   `db:"cache_read_tokens"`
	CacheWrite        int64   `db:"cache_write_tokens"`
	UncachedInputCost *int64  `db:"uncached_input_cost"`
	OutputCost        *int64  `db:"output_cost"`
	CacheReadCost     *int64  `db:"cache_read_cost"`
	CacheWriteCost    *int64  `db:"cache_write_cost"`
}

const capturedColumns = `user_id, user_sub, agent_id, agent_name, workflow_id, workflow_name,
	root_kind, root_id, root_name, entrypoint_id, entrypoint_name, trigger_kind, surface,
	model_config_id, trace_id, uncached_input_tokens, output_tokens, cache_read_tokens,
	cache_write_tokens, uncached_input_cost, output_cost, cache_read_cost, cache_write_cost`

const (
	capturedUserID     = "0192f1a0-0000-7000-8000-000000000011"
	capturedWorkflowID = "0192f1a0-0000-7000-8000-000000000013"
	capturedEntryID    = "0192f1a0-0000-7000-8000-000000000015"
)

func TestRecordedLLMCallsAreFiledAsAttributedPricedLedgerRows(t *testing.T) {
	r := startRig(t)
	pub := spend.NewPublisher(r.broker, testNamespace, discardLogger)
	t.Cleanup(func() { _ = pub.Close(context.Background()) })
	usage := &llm.Usage{PromptTokens: 1000, CachedTokens: 600, CacheWriteTokens: 200, CompletionTokens: 50, TotalTokens: 1050}
	client := spend.NewRecordingClient(usageClient{usage: usage}, "bound-model", pub)
	attribution := func(taskID string) *llm.SpendAttribution {
		return &llm.SpendAttribution{
			UserID: capturedUserID, AgentName: "agent-1", WorkflowID: capturedWorkflowID, WorkflowNodeID: "node-1",
			RootKind: "workflow", RootName: "wf-root", RootNodeID: "root-node", EntrypointID: capturedEntryID,
			TriggerKind: "chat", Surface: "agent", ModelKey: r.models.cacheBrokenOut.Alias,
			TaskID: taskID, TraceID: "trace-1", SessionID: "session-1",
		}
	}

	if _, err := client.ChatCompletion(t.Context(), &llm.ChatRequest{Spend: attribution("task-unary")}); err != nil {
		t.Fatalf("unary call: %v", err)
	}
	stream, err := client.ChatCompletionStream(t.Context(), &llm.ChatRequest{Model: "streamed-model", Spend: attribution("task-stream")})
	if err != nil {
		t.Fatalf("stream call: %v", err)
	}
	for range stream {
	}

	wantCost := spend.CostOf(spend.Tokens{UncachedInput: 400, Output: 50, CacheRead: 400, CacheWrite: 200}, r.models.cacheBrokenOutFor)
	for _, taskID := range []string{"task-unary", "task-stream"} {
		var row *capturedRow
		eventually(t, "the call of "+taskID+" to be filed", func() bool {
			found, err := dbutil.QueryRow[capturedRow](t.Context(), r.db,
				`SELECT `+capturedColumns+` FROM token_spend_ledger WHERE task_id = ?`, taskID)
			row = found
			return err == nil && row != nil
		})
		if !holds(row.UserID, capturedUserID) || row.UserSub != nil || !holds(row.TraceID, "trace-1") ||
			row.Surface != "agent" || !holds(row.TriggerKind, "chat") {
			t.Errorf("%s row not attributed as recorded: %+v", taskID, row)
		}
		if row.AgentID != nil || !holds(row.AgentName, "agent-1") ||
			!holds(row.WorkflowID, capturedWorkflowID) || row.WorkflowName != nil ||
			!holds(row.EntrypointID, capturedEntryID) || row.EntrypointName != nil {
			t.Errorf("%s row lost an id or name of its pairs: %+v", taskID, row)
		}
		if !holds(row.RootKind, "workflow") || row.RootID != nil || !holds(row.RootName, "wf-root") {
			t.Errorf("%s row lost its root: %+v", taskID, row)
		}
		if !holds(row.ModelConfigID, r.models.cacheBrokenOut.ID) {
			t.Errorf("%s model_config_id = %v, want %s", taskID, row.ModelConfigID, r.models.cacheBrokenOut.ID)
		}
		if row.UncachedInput != 400 || row.Output != 50 || row.CacheRead != 400 || row.CacheWrite != 200 {
			t.Errorf("%s tokens = %d/%d/%d/%d", taskID, row.UncachedInput, row.Output, row.CacheRead, row.CacheWrite)
		}
		gotCost := [4]*int64{row.UncachedInputCost, row.OutputCost, row.CacheReadCost, row.CacheWriteCost}
		for i, want := range [4]int64{wantCost.UncachedInput, wantCost.Output, wantCost.CacheRead, wantCost.CacheWrite} {
			if gotCost[i] == nil || *gotCost[i] != want {
				t.Errorf("%s cost bucket %d = %v, want %d", taskID, i, gotCost[i], want)
			}
		}
	}
	if n := countRows(t, r.db); n != 2 {
		t.Errorf("ledger holds %d rows, want one per call", n)
	}
}

func holds(got *string, want string) bool {
	return got != nil && *got == want
}

func TestRecordedCallsFileTheirAttemptCountInTheDetails(t *testing.T) {
	r := startRig(t)
	pub := spend.NewPublisher(r.broker, testNamespace, discardLogger)
	t.Cleanup(func() { _ = pub.Close(context.Background()) })
	usage := &llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}
	attribution := func(taskID string) *llm.SpendAttribution {
		return &llm.SpendAttribution{
			UserID: capturedUserID, Surface: "agent", ModelKey: r.models.cacheBrokenOut.Alias, TaskID: taskID,
		}
	}
	retried := spend.NewRecordingClient(usageClient{usage: usage, attempts: 2}, "bound-model", pub)
	unreported := spend.NewRecordingClient(usageClient{usage: usage}, "bound-model", pub)

	if _, err := retried.ChatCompletion(t.Context(), &llm.ChatRequest{Spend: attribution("task-unary-retried")}); err != nil {
		t.Fatalf("unary call: %v", err)
	}
	stream, err := retried.ChatCompletionStream(t.Context(), &llm.ChatRequest{Spend: attribution("task-stream-retried")})
	if err != nil {
		t.Fatalf("stream call: %v", err)
	}
	for range stream {
	}
	if _, err := unreported.ChatCompletion(t.Context(), &llm.ChatRequest{Spend: attribution("task-unreported")}); err != nil {
		t.Fatalf("unreported call: %v", err)
	}

	for taskID, want := range map[string]*int{"task-unary-retried": new(2), "task-stream-retried": new(2), "task-unreported": nil} {
		var got *int
		eventually(t, "the details of "+taskID+" to be filed", func() bool {
			found, err := dbutil.QueryRow[struct {
				AttemptCount *int `db:"attempt_count"`
			}](t.Context(), r.db,
				`SELECT d.attempt_count FROM token_spend_ledger_details d
				 JOIN token_spend_ledger l ON l.id = d.id WHERE l.task_id = ?`, taskID)
			if err != nil || found == nil {
				return false
			}
			got = found.AttemptCount
			return true
		})
		if (got == nil) != (want == nil) || (got != nil && *got != *want) {
			t.Errorf("%s attempt_count = %v, want %v", taskID, got, want)
		}
	}
}

func TestARecordedCallFilesWhetherItsProviderReportedCacheWrites(t *testing.T) {
	r := startRig(t)
	pub := spend.NewPublisher(r.broker, testNamespace, discardLogger)
	t.Cleanup(func() { _ = pub.Close(context.Background()) })
	attribution := &llm.SpendAttribution{UserID: capturedUserID, Surface: "agent", ModelKey: r.models.cacheBrokenOut.Alias}

	for taskID, reported := range map[string]bool{"task-native-claude": true, "task-claude-behind-gateway": false} {
		usage := &llm.Usage{PromptTokens: 1000, CachedTokens: 600, CompletionTokens: 50, TotalTokens: 1050, CacheWritesReported: reported}
		client := spend.NewRecordingClient(usageClient{usage: usage}, "bound-model", pub)
		call := *attribution
		call.TaskID = taskID
		if _, err := client.ChatCompletion(t.Context(), &llm.ChatRequest{Spend: &call}); err != nil {
			t.Fatalf("%s: %v", taskID, err)
		}

		var got *bool
		eventually(t, "the details of "+taskID+" to be filed", func() bool {
			found, err := dbutil.QueryRow[struct {
				CacheWritesReported *bool `db:"cache_writes_reported"`
			}](t.Context(), r.db,
				`SELECT d.cache_writes_reported FROM token_spend_ledger_details d
				 JOIN token_spend_ledger l ON l.id = d.id WHERE l.task_id = ?`, taskID)
			if err != nil || found == nil {
				return false
			}
			got = found.CacheWritesReported
			return true
		})
		if got == nil || *got != reported {
			t.Errorf("%s cache_writes_reported = %v, want %t", taskID, got, reported)
		}
	}
}

type billedFailureClient struct{ usageClient }

func (billedFailureClient) ChatCompletion(context.Context, *llm.ChatRequest) (*llm.ChatResponse, error) {
	return nil, &llm.Error{Message: "upstream reset", StatusCode: 502, Attempts: 3,
		BilledUsage: &llm.Usage{PromptTokens: 800, CompletionTokens: 20, TotalTokens: 820}}
}

func TestAFailedCallFilesItsBoundModelInTheProviderQualifiedForm(t *testing.T) {
	r := startRig(t)
	pub := spend.NewPublisher(r.broker, testNamespace, discardLogger)
	t.Cleanup(func() { _ = pub.Close(context.Background()) })

	for bound, want := range map[string]string{
		"vertex_ai/gemini-2.5-pro":   "vertex/gemini-2.5-pro",
		"gpt-4o":                     "openai/gpt-4o",
		"anthropic/claude-haiku-4-5": "anthropic/claude-haiku-4-5",
	} {
		taskID := "task-" + bound
		client := spend.NewRecordingClient(billedFailureClient{}, bound, pub)
		if _, err := client.ChatCompletion(t.Context(), &llm.ChatRequest{Spend: &llm.SpendAttribution{
			UserID: capturedUserID, Surface: "agent", TaskID: taskID,
		}}); err == nil {
			t.Fatalf("%s: the call did not fail", bound)
		}

		var got *string
		eventually(t, "the details of "+taskID+" to be filed", func() bool {
			found, err := dbutil.QueryRow[struct {
				SentModelName *string `db:"sent_model_name"`
			}](t.Context(), r.db,
				`SELECT d.sent_model_name FROM token_spend_ledger_details d
				 JOIN token_spend_ledger l ON l.id = d.id WHERE l.task_id = ?`, taskID)
			if err != nil || found == nil {
				return false
			}
			got = found.SentModelName
			return true
		})
		if !holds(got, want) {
			t.Errorf("bound model %s filed sent_model_name %v, want %s", bound, deref(got), want)
		}
	}
}
