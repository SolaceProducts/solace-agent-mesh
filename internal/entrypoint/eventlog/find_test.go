// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventlog

import (
	"context"
	"testing"
	"time"
)

func TestFindLastEventByDataType_MemoryFindsMostRecent(t *testing.T) {
	t.Parallel()
	m := NewMemoryEventLog()
	ctx := context.Background()

	_, _ = m.Append(ctx, "t1", "", "", "status_update",
		[]byte(`{"result":{"status":{"message":{"parts":[{"kind":"data","data":{"type":"user_input_request","request_id":"r1"}}]}}}}`))
	_, _ = m.Append(ctx, "t1", "", "", "status_update",
		[]byte(`{"result":{"status":{"message":{"parts":[{"kind":"data","data":{"type":"user_input_request","request_id":"r2"}}]}}}}`))

	got := m.FindLastEventByDataType(ctx, "t1", "user_input_request")
	if got == nil {
		t.Fatal("got nil, want most recent payload")
	}
	if !containsSubstring(string(got), `"request_id":"r2"`) {
		t.Errorf("got %s, want payload with request_id r2", got)
	}
}

func TestFindLastEventByDataType_MemoryIgnoresNonStatusUpdateEvents(t *testing.T) {
	t.Parallel()
	m := NewMemoryEventLog()
	ctx := context.Background()

	_, _ = m.Append(ctx, "t1", "", "", "response",
		[]byte(`{"type":"user_input_request","ignored":true}`))
	got := m.FindLastEventByDataType(ctx, "t1", "user_input_request")
	if got != nil {
		t.Errorf("got %s, want nil — non-status_update events must not match", got)
	}
}

func TestFindLastEventByDataType_MemoryUnknownTaskReturnsNil(t *testing.T) {
	t.Parallel()
	m := NewMemoryEventLog()
	if got := m.FindLastEventByDataType(context.Background(), "never-seen", "user_input_request"); got != nil {
		t.Errorf("got %s, want nil for unknown task", got)
	}
}

func TestFindLastEventByDataType_MemoryNoMatchReturnsNil(t *testing.T) {
	t.Parallel()
	m := NewMemoryEventLog()
	ctx := context.Background()
	_, _ = m.Append(ctx, "t1", "", "", "status_update",
		[]byte(`{"result":{"status":{"message":{"parts":[{"kind":"data","data":{"type":"other_type"}}]}}}}`))
	if got := m.FindLastEventByDataType(ctx, "t1", "user_input_request"); got != nil {
		t.Errorf("got %s, want nil when no event has the target type", got)
	}
}

func TestFindSequenceAfterTimestamp_MemoryReturnsFirstAfterCutoff(t *testing.T) {
	t.Parallel()
	m := NewMemoryEventLog()
	ctx := context.Background()

	_, _ = m.Append(ctx, "t1", "", "", "status_update", []byte(`{}`))
	cutoff := time.Now().UnixMilli()
	time.Sleep(2 * time.Millisecond)
	_, _ = m.Append(ctx, "t1", "", "", "status_update", []byte(`{}`))

	seq, err := m.FindSequenceAfterTimestamp(ctx, "t1", cutoff)
	if err != nil {
		t.Fatalf("FindSequenceAfterTimestamp: %v", err)
	}
	if seq < 2 {
		t.Errorf("seq = %d, want >= 2 (the event appended after the cutoff)", seq)
	}
}

func TestFindSequenceAfterTimestamp_MemoryNoEventsAfterCutoff(t *testing.T) {
	t.Parallel()
	m := NewMemoryEventLog()
	ctx := context.Background()

	_, _ = m.Append(ctx, "t1", "", "", "status_update", []byte(`{}`))
	future := time.Now().Add(1 * time.Hour).UnixMilli()
	seq, err := m.FindSequenceAfterTimestamp(ctx, "t1", future)
	if err != nil {
		t.Fatalf("FindSequenceAfterTimestamp: %v", err)
	}
	if seq < 2 {
		t.Errorf("seq = %d, want >= 2 (nextSeq after the one appended event)", seq)
	}
}

func TestFindSequenceAfterTimestamp_MemoryUnknownTaskErrors(t *testing.T) {
	t.Parallel()
	m := NewMemoryEventLog()
	_, err := m.FindSequenceAfterTimestamp(context.Background(), "missing", 0)
	if err == nil {
		t.Error("expected error for unknown task")
	}
}

func TestFindLastEventByDataType_PersistentFindsMostRecent(t *testing.T) {
	p := newTestPersistentEventLog(t)
	ctx := context.Background()
	taskID := tid("t-persist-find")

	_, _ = p.Append(ctx, taskID, tid("sess"), "", "status_update",
		[]byte(`{"result":{"status":{"message":{"parts":[{"kind":"data","data":{"type":"user_input_request","request_id":"r1"}}]}}}}`))
	_, _ = p.Append(ctx, taskID, tid("sess"), "", "status_update",
		[]byte(`{"result":{"status":{"message":{"parts":[{"kind":"data","data":{"type":"user_input_request","request_id":"r2"}}]}}}}`))

	got := p.FindLastEventByDataType(ctx, taskID, "user_input_request")
	if got == nil {
		t.Fatal("got nil, want most recent payload")
	}
	if !containsSubstring(string(got), `"request_id":"r2"`) {
		t.Errorf("got %s, want payload with request_id r2", got)
	}
}

func TestFindLastEventByDataType_PersistentNoMatchReturnsNil(t *testing.T) {
	p := newTestPersistentEventLog(t)
	if got := p.FindLastEventByDataType(context.Background(), "never-seen", "user_input_request"); got != nil {
		t.Errorf("got %s, want nil for unknown task", got)
	}
}

func TestFindSequenceAfterTimestamp_PersistentRoundTrip(t *testing.T) {
	p := newTestPersistentEventLog(t)
	ctx := context.Background()
	taskID := tid("t-persist-ts")

	_, _ = p.Append(ctx, taskID, tid("sess"), "", "status_update", []byte(`{}`))
	cutoff := time.Now().UnixMilli()
	time.Sleep(2 * time.Millisecond)
	_, _ = p.Append(ctx, taskID, tid("sess"), "", "status_update", []byte(`{}`))

	seq, err := p.FindSequenceAfterTimestamp(ctx, taskID, cutoff)
	if err != nil {
		t.Fatalf("FindSequenceAfterTimestamp: %v", err)
	}
	if seq < 2 {
		t.Errorf("seq = %d, want >= 2 (the event appended after cutoff)", seq)
	}
}

func containsSubstring(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
