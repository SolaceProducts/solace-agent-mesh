// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func contextUsageHandler(t *testing.T) (http.Handler, *store.SessionRepo, *store.ChatTaskRepo, *store.TaskRepo) {
	t.Helper()
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)
	db := adapter.core.Store()
	taskRepo := store.NewTaskRepo(db)

	h := &sessionHandler{
		core:     adapter.core,
		sessRepo: sessRepo,
		chatRepo: chatRepo,
		taskRepo: taskRepo,
		sseRepo:  sseRepo,
		logger:   adapter.logger,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions/{id}/contextUsage", h.handleContextUsage)
	return mux, sessRepo, chatRepo, taskRepo
}

func TestContextUsage_SessionNotFound(t *testing.T) {
	mux, _, _, _ := contextUsageHandler(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions/missing/contextUsage", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestContextUsage_SessionNotOwned(t *testing.T) {
	mux, sessRepo, _, _ := contextUsageHandler(t)

	sid := idgen.New()
	if err := sessRepo.Create(context.Background(),
		&store.Session{ID: sid, UserID: "someone-else"}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions/"+sid+"/contextUsage", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestContextUsage_NoTasks(t *testing.T) {
	mux, sessRepo, _, _ := contextUsageHandler(t)

	sid := idgen.New()
	if err := sessRepo.Create(context.Background(),
		&store.Session{ID: sid, UserID: "sam_dev_user"}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions/"+sid+"/contextUsage", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	var resp ContextUsageResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.SessionID != sid {
		t.Errorf("sessionId = %q, want %q", resp.SessionID, sid)
	}
	if resp.CurrentContextTokens != 0 || resp.PromptTokens != 0 || resp.CompletionTokens != 0 {
		t.Errorf("expected zero token counts, got %+v", resp)
	}
	if resp.MaxInputTokens != nil {
		t.Errorf("MaxInputTokens should be nil for unknown model, got %d", *resp.MaxInputTokens)
	}
	if resp.UsagePercentage != 0 {
		t.Errorf("UsagePercentage = %v, want 0", resp.UsagePercentage)
	}
}

func TestContextUsage_HappyPath_StampedLimit(t *testing.T) {
	mux, sessRepo, chatRepo, taskRepo := contextUsageHandler(t)

	sid := idgen.New()
	ctx := context.Background()
	if err := sessRepo.Create(ctx, &store.Session{ID: sid, UserID: "sam_dev_user"}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := chatRepo.Create(ctx, &store.ChatTask{
			ID: idgen.New(), SessionID: sid, UserID: "sam_dev_user",
			MessageBubbles: "[]",
		}); err != nil {
			t.Fatal(err)
		}
	}
	in1, out1 := 1000, 200
	t1Details := `{"by_model": {"claude-opus-4-7": {"input_tokens": 1000, "output_tokens": 200, "max_input_tokens": 200000}}}`
	if err := taskRepo.Create(ctx, &store.Task{
		ID: idgen.New(), UserID: "sam_dev_user",
		StartTime:     time.Now().Add(-time.Minute).UnixMilli(),
		ExecutionMode: "foreground", SessionID: &sid,
		TotalInputTokens: &in1, TotalOutputTokens: &out1,
		TokenUsageDetails: &t1Details,
	}); err != nil {
		t.Fatal(err)
	}
	in2, out2 := 8421, 1240
	t2Details := `{"by_model": {"claude-opus-4-7": {"input_tokens": 8421, "output_tokens": 1240, "max_input_tokens": 200000}}}`
	if err := taskRepo.Create(ctx, &store.Task{
		ID: idgen.New(), UserID: "sam_dev_user",
		StartTime:     time.Now().UnixMilli(),
		ExecutionMode: "foreground", SessionID: &sid,
		TotalInputTokens: &in2, TotalOutputTokens: &out2,
		TokenUsageDetails: &t2Details,
	}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions/"+sid+"/contextUsage", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	var resp ContextUsageResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if resp.CurrentContextTokens != in2 {
		t.Errorf("CurrentContextTokens = %d, want %d", resp.CurrentContextTokens, in2)
	}
	if resp.PromptTokens != in1+in2 {
		t.Errorf("PromptTokens = %d, want %d", resp.PromptTokens, in1+in2)
	}
	if resp.CompletionTokens != out1+out2 {
		t.Errorf("CompletionTokens = %d, want %d", resp.CompletionTokens, out1+out2)
	}
	if resp.MaxInputTokens == nil || *resp.MaxInputTokens != 200000 {
		t.Errorf("MaxInputTokens want 200000, got %v", resp.MaxInputTokens)
	}
	if resp.Model != "claude-opus-4-7" {
		t.Errorf("Model = %q, want claude-opus-4-7", resp.Model)
	}
	if resp.TotalTasks != 2 || resp.TotalMessages != 4 {
		t.Errorf("totalTasks/totalMessages = %d/%d, want 2/4",
			resp.TotalTasks, resp.TotalMessages)
	}
	wantPct := 4.2
	if resp.UsagePercentage != wantPct {
		t.Errorf("UsagePercentage = %v, want %v", resp.UsagePercentage, wantPct)
	}
}

func TestContextUsage_FallsBackToModelInfo(t *testing.T) {
	mux, sessRepo, _, taskRepo := contextUsageHandler(t)

	sid := idgen.New()
	ctx := context.Background()
	if err := sessRepo.Create(ctx, &store.Session{ID: sid, UserID: "sam_dev_user"}); err != nil {
		t.Fatal(err)
	}
	in, out := 1000, 50
	details := `{"by_model": {"gpt-4o": {"input_tokens": 1000, "output_tokens": 50}}}`
	if err := taskRepo.Create(ctx, &store.Task{
		ID: idgen.New(), UserID: "sam_dev_user",
		StartTime:     time.Now().UnixMilli(),
		ExecutionMode: "foreground", SessionID: &sid,
		TotalInputTokens: &in, TotalOutputTokens: &out,
		TokenUsageDetails: &details,
	}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions/"+sid+"/contextUsage", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	var resp ContextUsageResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.MaxInputTokens == nil || *resp.MaxInputTokens != 128_000 {
		t.Errorf("MaxInputTokens want 128000 (from modelinfo), got %v",
			resp.MaxInputTokens)
	}
}

func TestContextUsage_UnknownModelReturnsNullLimit(t *testing.T) {
	mux, sessRepo, _, taskRepo := contextUsageHandler(t)

	sid := idgen.New()
	ctx := context.Background()
	if err := sessRepo.Create(ctx, &store.Session{ID: sid, UserID: "sam_dev_user"}); err != nil {
		t.Fatal(err)
	}
	in, out := 100, 10
	details := `{"by_model": {"made-up-model": {"input_tokens": 100, "output_tokens": 10}}}`
	if err := taskRepo.Create(ctx, &store.Task{
		ID: idgen.New(), UserID: "sam_dev_user",
		StartTime:     time.Now().UnixMilli(),
		ExecutionMode: "foreground", SessionID: &sid,
		TotalInputTokens: &in, TotalOutputTokens: &out,
		TokenUsageDetails: &details,
	}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions/"+sid+"/contextUsage", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp ContextUsageResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.MaxInputTokens != nil {
		t.Errorf("MaxInputTokens should be nil for unknown model, got %d",
			*resp.MaxInputTokens)
	}
	if resp.UsagePercentage != 0 {
		t.Errorf("UsagePercentage = %v, want 0 (no denominator)",
			resp.UsagePercentage)
	}
}

func TestContextUsage_QueryModelOverride(t *testing.T) {
	mux, sessRepo, _, taskRepo := contextUsageHandler(t)

	sid := idgen.New()
	ctx := context.Background()
	if err := sessRepo.Create(ctx, &store.Session{ID: sid, UserID: "sam_dev_user"}); err != nil {
		t.Fatal(err)
	}
	in, out := 100, 10
	details := `{"by_model": {"gpt-4o": {"input_tokens": 100, "output_tokens": 10, "max_input_tokens": 128000}, "claude-opus-4-7": {"input_tokens": 50, "output_tokens": 5, "max_input_tokens": 200000}}}`
	if err := taskRepo.Create(ctx, &store.Task{
		ID: idgen.New(), UserID: "sam_dev_user",
		StartTime:     time.Now().UnixMilli(),
		ExecutionMode: "foreground", SessionID: &sid,
		TotalInputTokens: &in, TotalOutputTokens: &out,
		TokenUsageDetails: &details,
	}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions/"+sid+"/contextUsage?model=claude-opus-4-7", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp ContextUsageResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Model != "claude-opus-4-7" {
		t.Errorf("Model = %q, want claude-opus-4-7 (query override)", resp.Model)
	}
	if resp.MaxInputTokens == nil || *resp.MaxInputTokens != 200_000 {
		t.Errorf("MaxInputTokens want 200000 (claude limit), got %v", resp.MaxInputTokens)
	}
}

func TestContextUsage_CachedTokensSurfaced(t *testing.T) {
	mux, sessRepo, _, taskRepo := contextUsageHandler(t)

	sid := idgen.New()
	ctx := context.Background()
	if err := sessRepo.Create(ctx, &store.Session{ID: sid, UserID: "sam_dev_user"}); err != nil {
		t.Fatal(err)
	}
	in, out, cached := 1000, 50, 250
	details := `{"by_model": {"gpt-4o": {"input_tokens": 1000, "output_tokens": 50}}}`
	if err := taskRepo.Create(ctx, &store.Task{
		ID: idgen.New(), UserID: "sam_dev_user",
		StartTime:     time.Now().UnixMilli(),
		ExecutionMode: "foreground", SessionID: &sid,
		TotalInputTokens:       &in,
		TotalOutputTokens:      &out,
		TotalCachedInputTokens: &cached,
		TokenUsageDetails:      &details,
	}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions/"+sid+"/contextUsage", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp ContextUsageResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.CachedTokens != cached {
		t.Errorf("CachedTokens = %d, want %d", resp.CachedTokens, cached)
	}
}

func TestBuildContextUsage_CompactionCostRow(t *testing.T) {
	realID := "real-task-1"
	in1, out1, cached1 := 5000, 200, 100
	realDetails := `{"by_model": {"gpt-4o": {"input_tokens": 5000, "output_tokens": 200, "max_input_tokens": 128000}}}`
	costID := "any-uuid-id"
	costIn, costOut := 11_200, 380
	costDetails := `{"post_compaction_remaining_tokens": 4800}`
	tasks := []*store.Task{
		{
			ID: costID, StartTime: 200,
			TotalInputTokens:  &costIn,
			TotalOutputTokens: &costOut,
			TokenUsageDetails: &costDetails,
			Kind:              store.TaskKindCompactionCost,
		},
		{
			ID: realID, StartTime: 100,
			TotalInputTokens:       &in1,
			TotalOutputTokens:      &out1,
			TotalCachedInputTokens: &cached1,
			TokenUsageDetails:      &realDetails,
		},
	}
	resp := buildContextUsage(context.Background(), "sess-1", tasks, 1, "", "")
	if resp.CurrentContextTokens != 4800 {
		t.Errorf("CurrentContextTokens = %d, want 4800 (post-compaction remaining)",
			resp.CurrentContextTokens)
	}
	if resp.CachedTokens != cached1 {
		t.Errorf("CachedTokens = %d, want %d (from real-latest)",
			resp.CachedTokens, cached1)
	}
	if resp.PromptTokens != in1+costIn {
		t.Errorf("PromptTokens = %d, want %d", resp.PromptTokens, in1+costIn)
	}
	if resp.CompletionTokens != out1+costOut {
		t.Errorf("CompletionTokens = %d, want %d", resp.CompletionTokens, out1+costOut)
	}
	if resp.Model != "gpt-4o" {
		t.Errorf("Model = %q, want gpt-4o (resolved from real-latest)", resp.Model)
	}
}

func TestBuildContextUsage_FailedLatestKeepsModelFromEarlierTask(t *testing.T) {
	failedIn, failedOut := 300000, 2000
	failedStatus := "failed"
	failedDetails := `{"total_input_tokens": 300000, "total_output_tokens": 2000, "last_input_tokens": 60000}`
	completedIn := 9000
	completedDetails := `{"by_model": {"claude-3-7-sonnet": {"input_tokens": 9000, "output_tokens": 300, "max_input_tokens": 150000}}, "last_input_tokens": 9000}`
	tasks := []*store.Task{
		{
			ID: "failed-task", StartTime: 200,
			Status:            &failedStatus,
			TotalInputTokens:  &failedIn,
			TotalOutputTokens: &failedOut,
			TokenUsageDetails: &failedDetails,
		},
		{
			ID: "completed-task", StartTime: 100,
			TotalInputTokens:  &completedIn,
			TokenUsageDetails: &completedDetails,
		},
	}

	resp := buildContextUsage(t.Context(), "sess-1", tasks, 2, "", "gpt-4o-mini")

	if resp.Model != "claude-3-7-sonnet" {
		t.Errorf("Model = %q, want claude-3-7-sonnet (from the newest task with a by_model block)", resp.Model)
	}
	if resp.MaxInputTokens == nil || *resp.MaxInputTokens != 150000 {
		t.Errorf("MaxInputTokens = %v, want 150000 (stamped on the earlier task)", resp.MaxInputTokens)
	}
	if resp.CurrentContextTokens != 60000 {
		t.Errorf("CurrentContextTokens = %d, want 60000 (the failed task's last_input_tokens)", resp.CurrentContextTokens)
	}
	if resp.UsagePercentage != 40 {
		t.Errorf("UsagePercentage = %v, want 40", resp.UsagePercentage)
	}
}

func TestBuildContextUsage_DecisionOnlyWorkflowRunLeavesTheChatContextAlone(t *testing.T) {
	workflowIn := 400
	workflowDetails := `{"total_input_tokens": 400, "by_source": {"decision": {"input_tokens": 400}}, "by_model": {"jev-1.13.0": {"input_tokens": 400, "output_tokens": 0, "kind": "decision"}}}`
	chatIn := 9000
	chatDetails := `{"by_model": {"claude-3-7-sonnet": {"input_tokens": 9000, "output_tokens": 300, "max_input_tokens": 150000}}, "last_input_tokens": 9000}`
	tasks := []*store.Task{
		{ID: "workflow-run", StartTime: 200, TotalInputTokens: &workflowIn, TokenUsageDetails: &workflowDetails},
		{ID: "chat-turn", StartTime: 100, TotalInputTokens: &chatIn, TokenUsageDetails: &chatDetails},
	}

	resp := buildContextUsage(t.Context(), "sess-1", tasks, 2, "", "gpt-4o-mini")

	if resp.Model != "claude-3-7-sonnet" {
		t.Errorf("Model = %q, want the chat model, not the decision model", resp.Model)
	}
	if resp.CurrentContextTokens != 9000 {
		t.Errorf("CurrentContextTokens = %d, want 9000 from the chat turn, not the decision prompts", resp.CurrentContextTokens)
	}
	if resp.PromptTokens != 9400 {
		t.Errorf("PromptTokens = %d, want 9400: decision usage still counts toward the session's totals", resp.PromptTokens)
	}
}

func TestBuildContextUsage_AToolsOwnModelNeverBecomesTheChatModel(t *testing.T) {
	in := 1000
	details := `{"by_model": {` +
		`"gpt-4o": {"input_tokens": 300, "output_tokens": 30, "max_input_tokens": 128000},` +
		`"gemini-2.5-pro": {"input_tokens": 700, "output_tokens": 70, "max_input_tokens": 1000000, "kind": "tool"}` +
		`}, "last_input_tokens": 120000}`
	tasks := []*store.Task{{ID: "chat-turn", StartTime: 100, TotalInputTokens: &in, TokenUsageDetails: &details}}

	resp := buildContextUsage(t.Context(), "sess-1", tasks, 1, "", "gpt-4o-mini")

	if resp.Model != "gpt-4o" {
		t.Errorf("Model = %q, want the agent's gpt-4o, not the tool's model", resp.Model)
	}
	if resp.MaxInputTokens == nil || *resp.MaxInputTokens != 128000 {
		t.Errorf("MaxInputTokens = %v, want the chat model's 128000", resp.MaxInputTokens)
	}
	if resp.UsagePercentage != 93.8 {
		t.Errorf("UsagePercentage = %v, want 93.8", resp.UsagePercentage)
	}
}

func TestBuildContextUsage_NoModelBreakdownFallsBackToDefault(t *testing.T) {
	failedIn := 300000
	failedStatus := "failed"
	failedDetails := `{"total_input_tokens": 300000, "last_input_tokens": 60000}`
	tasks := []*store.Task{{
		ID: "failed-task", StartTime: 200,
		Status:            &failedStatus,
		TotalInputTokens:  &failedIn,
		TokenUsageDetails: &failedDetails,
	}}

	resp := buildContextUsage(t.Context(), "sess-1", tasks, 1, "", "gpt-4o-mini")

	if resp.Model != "gpt-4o-mini" {
		t.Errorf("Model = %q, want the gpt-4o-mini default", resp.Model)
	}
	if resp.MaxInputTokens == nil || *resp.MaxInputTokens != 128000 {
		t.Errorf("MaxInputTokens = %v, want 128000 (modelinfo for the default)", resp.MaxInputTokens)
	}
}

func TestContextUsage_PrefersLastInputTokens(t *testing.T) {
	mux, sessRepo, _, taskRepo := contextUsageHandler(t)

	sid := idgen.New()
	ctx := context.Background()
	if err := sessRepo.Create(ctx, &store.Session{ID: sid, UserID: "sam_dev_user"}); err != nil {
		t.Fatal(err)
	}
	totalIn, totalOut := 203722, 3017
	lastIn := 31077
	details := `{"last_input_tokens": 31077,
		"by_model": {"claude-opus-4-7": {"input_tokens": 203722, "output_tokens": 3017, "max_input_tokens": 200000}}}`
	if err := taskRepo.Create(ctx, &store.Task{
		ID: idgen.New(), UserID: "sam_dev_user",
		StartTime:     time.Now().UnixMilli(),
		ExecutionMode: "foreground", SessionID: &sid,
		TotalInputTokens:  &totalIn,
		TotalOutputTokens: &totalOut,
		TokenUsageDetails: &details,
	}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions/"+sid+"/contextUsage", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	var resp ContextUsageResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.CurrentContextTokens != lastIn {
		t.Errorf("CurrentContextTokens = %d, want %d (last_input_tokens, not the inflated sum %d)",
			resp.CurrentContextTokens, lastIn, totalIn)
	}
	if resp.PromptTokens != totalIn {
		t.Errorf("PromptTokens = %d, want %d (cumulative)", resp.PromptTokens, totalIn)
	}
}

func TestContextUsage_FallbackToTotalInputTokens(t *testing.T) {
	mux, sessRepo, _, taskRepo := contextUsageHandler(t)

	sid := idgen.New()
	ctx := context.Background()
	if err := sessRepo.Create(ctx, &store.Session{ID: sid, UserID: "sam_dev_user"}); err != nil {
		t.Fatal(err)
	}
	in, out := 5000, 800
	details := `{"by_model": {"claude-opus-4-7": {"input_tokens": 5000, "output_tokens": 800, "max_input_tokens": 200000}}}`
	if err := taskRepo.Create(ctx, &store.Task{
		ID: idgen.New(), UserID: "sam_dev_user",
		StartTime:     time.Now().UnixMilli(),
		ExecutionMode: "foreground", SessionID: &sid,
		TotalInputTokens:  &in,
		TotalOutputTokens: &out,
		TokenUsageDetails: &details,
	}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions/"+sid+"/contextUsage", nil)
	mux.ServeHTTP(rec, req)

	var resp ContextUsageResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.CurrentContextTokens != in {
		t.Errorf("CurrentContextTokens = %d, want %d (fallback to TotalInputTokens)",
			resp.CurrentContextTokens, in)
	}
}

func TestContextUsage_ExcludesPeerSubtasks(t *testing.T) {
	mux, sessRepo, _, taskRepo := contextUsageHandler(t)

	sid := idgen.New()
	ctx := context.Background()
	if err := sessRepo.Create(ctx, &store.Session{ID: sid, UserID: "sam_dev_user"}); err != nil {
		t.Fatal(err)
	}
	parentID := idgen.New()
	in1, out1 := 4000, 500
	t1Details := `{"last_input_tokens": 4000, "by_model": {"claude-opus-4-7": {"input_tokens": 4000, "output_tokens": 500, "max_input_tokens": 200000}}}`
	if err := taskRepo.Create(ctx, &store.Task{
		ID: parentID, UserID: "sam_dev_user",
		StartTime:     time.Now().Add(-time.Minute).UnixMilli(),
		ExecutionMode: "foreground", SessionID: &sid,
		TotalInputTokens:  &in1,
		TotalOutputTokens: &out1,
		TokenUsageDetails: &t1Details,
	}); err != nil {
		t.Fatal(err)
	}
	peerIn, peerOut := 99999, 7777
	peerDetails := `{"last_input_tokens": 99999, "by_model": {"claude-sonnet-4-6": {"input_tokens": 99999, "output_tokens": 7777, "max_input_tokens": 1000000}}}`
	if err := taskRepo.Create(ctx, &store.Task{
		ID: idgen.New(), UserID: "sam_dev_user",
		ParentTaskID:  &parentID,
		StartTime:     time.Now().UnixMilli(),
		ExecutionMode: "foreground", SessionID: &sid,
		TotalInputTokens:  &peerIn,
		TotalOutputTokens: &peerOut,
		TokenUsageDetails: &peerDetails,
	}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions/"+sid+"/contextUsage", nil)
	mux.ServeHTTP(rec, req)

	var resp ContextUsageResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.CurrentContextTokens != in1 {
		t.Errorf("CurrentContextTokens = %d, want %d (peer subtask must be excluded)",
			resp.CurrentContextTokens, in1)
	}
	if resp.PromptTokens != in1 {
		t.Errorf("PromptTokens = %d, want %d (peer subtask must not be summed)",
			resp.PromptTokens, in1)
	}
	if resp.Model != "claude-opus-4-7" {
		t.Errorf("Model = %q, want claude-opus-4-7 (real task's model, not peer's)", resp.Model)
	}
}

func TestDefaultModelFromConfig(t *testing.T) {
	if got := defaultModelFromConfig(nil); got != "" {
		t.Errorf("nil config: got %q, want empty", got)
	}
	cfg := &entrypoint.GatewayConfig{}
	if got := defaultModelFromConfig(cfg); got != "" {
		t.Errorf("empty ModelRaw: got %q, want empty", got)
	}
	cfg.ModelRaw = "claude-opus-4-7"
	if got := defaultModelFromConfig(cfg); got != "claude-opus-4-7" {
		t.Errorf("string ModelRaw: got %q, want claude-opus-4-7", got)
	}
	cfg.ModelRaw = map[string]any{"model": "gpt-4o"}
	if got := defaultModelFromConfig(cfg); got != "gpt-4o" {
		t.Errorf("map ModelRaw: got %q, want gpt-4o", got)
	}
	cfg.ModelRaw = map[string]any{"other": "thing"}
	if got := defaultModelFromConfig(cfg); got != "" {
		t.Errorf("map without model key: got %q, want empty", got)
	}
	cfg.ModelRaw = 42
	if got := defaultModelFromConfig(cfg); got != "" {
		t.Errorf("int ModelRaw: got %q, want empty", got)
	}
}

func TestReadNumber(t *testing.T) {
	cases := []struct {
		name   string
		input  any
		want   int
		wantOK bool
	}{
		{name: "float64", input: float64(8421), want: 8421, wantOK: true},
		{name: "int", input: 100, want: 100, wantOK: true},
		{name: "int64", input: int64(2_000_000), want: 2_000_000, wantOK: true},
		{name: "string", input: "100", want: 0, wantOK: false},
		{name: "nil", input: nil, want: 0, wantOK: false},
		{name: "json.Number numeric", input: json.Number("123"), want: 123, wantOK: true},
		{name: "json.Number invalid", input: json.Number("abc"), want: 0, wantOK: false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := readNumber(tt.input)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("readNumber(%v) = (%d, %v), want (%d, %v)",
					tt.input, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestReadPostCompactionRemaining(t *testing.T) {
	if got := readPostCompactionRemaining(nil); got != 0 {
		t.Errorf("nil task: %d, want 0", got)
	}
	emptyDetails := ""
	if got := readPostCompactionRemaining(&store.Task{TokenUsageDetails: &emptyDetails}); got != 0 {
		t.Errorf("empty details: %d, want 0", got)
	}
	bad := `{not json`
	if got := readPostCompactionRemaining(&store.Task{TokenUsageDetails: &bad}); got != 0 {
		t.Errorf("malformed details: %d, want 0", got)
	}
	ok := `{"post_compaction_remaining_tokens": 4800}`
	if got := readPostCompactionRemaining(&store.Task{TokenUsageDetails: &ok}); got != 4800 {
		t.Errorf("valid details: %d, want 4800", got)
	}
}

func TestDominantModelFromUsage(t *testing.T) {
	if _, ok := dominantModelFromUsage(nil); ok {
		t.Error("nil task should return false")
	}
	empty := ""
	if _, ok := dominantModelFromUsage(&store.Task{TokenUsageDetails: &empty}); ok {
		t.Error("empty details should return false")
	}
	details := `{"by_model": {"a": {"input_tokens": 10}, "b": {"input_tokens": 50}}}`
	got, ok := dominantModelFromUsage(&store.Task{TokenUsageDetails: &details})
	if !ok || got != "b" {
		t.Errorf("dominantModelFromUsage = (%q, %v), want (b, true)", got, ok)
	}
	noBy := `{"foo": "bar"}`
	if _, ok := dominantModelFromUsage(&store.Task{TokenUsageDetails: &noBy}); ok {
		t.Error("missing by_model should return false")
	}
}

func TestStampedMaxInputForModel(t *testing.T) {
	if got := stampedMaxInputForModel(nil, "x"); got != 0 {
		t.Errorf("nil task: %d, want 0", got)
	}
	details := `{"by_model": {"gpt-4o": {"max_input_tokens": 128000}, "claude-opus-4-7": {"max_input_tokens": 200000}}}`
	if got := stampedMaxInputForModel(&store.Task{TokenUsageDetails: &details}, "gpt-4o"); got != 128_000 {
		t.Errorf("exact match: %d, want 128000", got)
	}
	missDetails := `{"by_model": {"some-model": {"max_input_tokens": 50000}}}`
	if got := stampedMaxInputForModel(&store.Task{TokenUsageDetails: &missDetails}, "different-model"); got != 50_000 {
		t.Errorf("fallback match: %d, want 50000", got)
	}
	noEntries := `{"by_model": {}}`
	if got := stampedMaxInputForModel(&store.Task{TokenUsageDetails: &noEntries}, "x"); got != 0 {
		t.Errorf("no entries: %d, want 0", got)
	}
}

func TestComputeUsagePercentage(t *testing.T) {
	max200k := 200_000
	maxZero := 0
	cases := []struct {
		name    string
		current int
		max     *int
		want    float64
	}{
		{name: "nil max", current: 100, max: nil, want: 0},
		{name: "zero max", current: 100, max: &maxZero, want: 0},
		{name: "zero current", current: 0, max: &max200k, want: 0},
		{name: "rounds to one decimal", current: 8421, max: &max200k, want: 4.2},
		{name: "caps at 100", current: 500_000, max: &max200k, want: 100},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := computeUsagePercentage(tt.current, tt.max)
			if got != tt.want {
				t.Errorf("computeUsagePercentage(%d, %v) = %v, want %v",
					tt.current, tt.max, got, tt.want)
			}
		})
	}
}

func TestContextUsage_CancelledTaskUsesLastInputTokens(t *testing.T) {
	mux, sessRepo, _, taskRepo := contextUsageHandler(t)

	sid := idgen.New()
	ctx := t.Context()
	if err := sessRepo.Create(ctx, &store.Session{ID: sid, UserID: "sam_dev_user"}); err != nil {
		t.Fatal(err)
	}
	totalIn, totalOut := 300000, 4000
	lastIn := 20000
	cancelled := "canceled"
	details := `{"total_input_tokens": 300000, "total_output_tokens": 4000, "last_input_tokens": 20000}`
	if err := taskRepo.Create(ctx, &store.Task{
		ID: idgen.New(), UserID: "sam_dev_user",
		StartTime:     time.Now().UnixMilli(),
		ExecutionMode: "foreground", SessionID: &sid,
		Status:            &cancelled,
		TotalInputTokens:  &totalIn,
		TotalOutputTokens: &totalOut,
		TokenUsageDetails: &details,
	}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions/"+sid+"/contextUsage?model=gpt-4o", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	var resp ContextUsageResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.CurrentContextTokens != lastIn {
		t.Errorf("CurrentContextTokens = %d, want %d (last call, not the %d cumulative sum)",
			resp.CurrentContextTokens, lastIn, totalIn)
	}
	if resp.UsagePercentage >= 100 {
		t.Errorf("UsagePercentage = %v, want well under 100 — the indicator is pinned full",
			resp.UsagePercentage)
	}
}
