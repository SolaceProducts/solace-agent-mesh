// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval/storage"
)

func TestStorageServiceRoundtrip(t *testing.T) {
	t.Parallel()
	mem := storage.NewMemory()
	svc := NewStorageService(mem, "ns/")
	if !svc.Available() {
		t.Fatalf("expected available")
	}

	data := map[string]any{"prompt": "hi", "trial_index": 0}
	key, err := svc.UploadExecutionData("run-1", "ex-1", 0, data)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	got, err := svc.GetExecutionData(key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got["prompt"] != "hi" {
		t.Fatalf("got = %v", got)
	}

	events := []map[string]any{{"event": "tool_invocation_start"}}
	eventsKey, err := svc.UploadTaskEvents("run-1", "ex-1", 0, events)
	if err != nil {
		t.Fatalf("upload events: %v", err)
	}
	if eventsKey == key {
		t.Fatalf("events key %q must differ from execution key %q", eventsKey, key)
	}

	n, err := svc.DeleteRunData(t.Context(), "run-1")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n != 2 {
		t.Fatalf("delete count = %d, want 2", n)
	}
	if _, err := svc.GetExecutionData(key); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStorageServiceDisabled(t *testing.T) {
	t.Parallel()
	svc := NewStorageService(nil, "ns")
	if svc.Available() {
		t.Fatalf("nil client must not be available")
	}
	if _, err := svc.UploadExecutionData("r", "e", 0, "x"); err == nil {
		t.Fatalf("expected error on disabled storage")
	}
	n, err := svc.DeleteRunData(t.Context(), "r")
	if err != nil || n != 0 {
		t.Fatalf("delete on disabled storage: n=%d err=%v", n, err)
	}
}
