// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestOnFinalResponse_BumpsSessionUpdatedTime(t *testing.T) {
	adapter, _, sessRepo, _, _ := setupTestAdapterWithDB(t)
	ctx := context.Background()

	sid := idgen.New()
	user := "sam_dev_user"
	if err := sessRepo.Create(ctx, &store.Session{ID: sid, UserID: user}); err != nil {
		t.Fatal(err)
	}
	before, err := sessRepo.GetByID(ctx, sid)
	if err != nil || before == nil {
		t.Fatalf("session not found before OnFinalResponse: %v", err)
	}

	time.Sleep(2 * time.Millisecond)

	taskID := idgen.New()
	adapter.sseManager.RegisterTask(taskID, sid, user)
	_, _, _ = adapter.sseManager.Subscribe(taskID)

	usage := map[string]any{
		"total_input_tokens":  100,
		"total_output_tokens": 50,
	}
	raw := finalResponseFixture(taskID, sid, usage)
	task := taskFromFixture(taskID, sid, usage)

	adapter.OnFinalResponse(ctx, taskID, task, raw)

	after, err := sessRepo.GetByID(ctx, sid)
	if err != nil || after == nil {
		t.Fatalf("session not found after OnFinalResponse: %v", err)
	}
	if after.UpdatedTime <= before.UpdatedTime {
		t.Errorf("session UpdatedTime not bumped on OnFinalResponse: before=%d after=%d",
			before.UpdatedTime, after.UpdatedTime)
	}
}

func TestOnError_BumpsSessionUpdatedTime(t *testing.T) {
	adapter, _, sessRepo, _, _ := setupTestAdapterWithDB(t)
	ctx := context.Background()

	sid := idgen.New()
	user := "sam_dev_user"
	if err := sessRepo.Create(ctx, &store.Session{ID: sid, UserID: user}); err != nil {
		t.Fatal(err)
	}
	before, err := sessRepo.GetByID(ctx, sid)
	if err != nil || before == nil {
		t.Fatalf("session not found before OnError: %v", err)
	}

	time.Sleep(2 * time.Millisecond)

	taskID := idgen.New()
	adapter.sseManager.RegisterTask(taskID, sid, user)
	_, _, _ = adapter.sseManager.Subscribe(taskID)

	raw := []byte(`{"jsonrpc":"2.0","id":"` + taskID + `","error":{"code":-32000,"message":"boom"}}`)

	adapter.OnError(ctx, taskID, &a2a.Error{Code: -32000, Message: "boom"}, raw)

	after, err := sessRepo.GetByID(ctx, sid)
	if err != nil || after == nil {
		t.Fatalf("session not found after OnError: %v", err)
	}
	if after.UpdatedTime <= before.UpdatedTime {
		t.Errorf("session UpdatedTime not bumped on OnError: before=%d after=%d",
			before.UpdatedTime, after.UpdatedTime)
	}
}
