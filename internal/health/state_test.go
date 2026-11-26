// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package health

import (
	"sync"
	"testing"
	"time"
)

func TestState_SetClear_DistinctKeysDoNotCollide(t *testing.T) {
	var s State

	s.Set("queue:a", Degraded, "broker exhausted")
	s.Set("db:session", Fatal, "connection refused")

	got := s.Snapshot()
	if len(got) != 2 {
		t.Fatalf("Snapshot() = %v, want 2 problems", got)
	}

	s.Clear("queue:a")
	got = s.Snapshot()
	if len(got) != 1 || got[0].ID != "db:session" {
		t.Fatalf("Snapshot() after Clear(queue:a) = %v, want only db:session", got)
	}

	s.Clear("db:session")
	if got := s.Snapshot(); got != nil {
		t.Fatalf("Snapshot() = %v, want nil after clearing everything", got)
	}
}

func TestState_Set_PreservesSinceAcrossRepeatedReports(t *testing.T) {
	var s State

	s.Set("queue:a", Degraded, "attempt 1")
	first := s.Snapshot()[0].Since

	s.Set("queue:a", Degraded, "attempt 2")
	second := s.Snapshot()[0]

	if !second.Since.Equal(first) {
		t.Fatalf("Since changed across repeated Set calls: first=%v second=%v", first, second.Since)
	}
	if second.Detail != "attempt 2" {
		t.Fatalf("Detail = %q, want latest report to overwrite it", second.Detail)
	}
}

func TestState_Clear_UnknownIDIsNoop(t *testing.T) {
	var s State
	s.Clear("never-set")
	if got := s.Snapshot(); got != nil {
		t.Fatalf("Snapshot() = %v, want nil", got)
	}
}

func TestState_Snapshot_EmptyIsNil(t *testing.T) {
	var s State
	if got := s.Snapshot(); got != nil {
		t.Fatalf("Snapshot() on zero value = %v, want nil", got)
	}
}

func TestState_Reset_ClearsAllProblems(t *testing.T) {
	var s State
	s.Set("a", Fatal, "boom")
	s.Set("b", Degraded, "retrying")
	s.Reset()
	if got := s.Snapshot(); got != nil {
		t.Fatalf("Snapshot() after Reset() = %v, want nil", got)
	}
}

func TestState_Reset_ThenSetStartsFreshSince(t *testing.T) {
	var s State
	s.Set("a", Fatal, "boom")
	first := s.Snapshot()[0].Since
	s.Reset()
	time.Sleep(time.Millisecond)
	s.Set("a", Fatal, "boom again")
	second := s.Snapshot()[0].Since
	if !second.After(first) {
		t.Errorf("Since after Reset+Set = %v, want after original Since %v", second, first)
	}
}

func TestState_Reset_OnZeroValueIsNoop(t *testing.T) {
	var s State
	s.Reset()
	if got := s.Snapshot(); got != nil {
		t.Fatalf("Snapshot() after Reset() on zero value = %v, want nil", got)
	}
}

func TestHTTPStatusDetail_InvalidRangeFallsBack(t *testing.T) {
	for _, code := range []int{0, -1, 99, 600} {
		if got := HTTPStatusDetail(code); got != "remote endpoint returned an invalid HTTP status" {
			t.Errorf("HTTPStatusDetail(%d) = %q, want the generic fallback", code, got)
		}
	}
}

func TestHTTPStatusDetail_ValidRangeFormatsCode(t *testing.T) {
	if got := HTTPStatusDetail(503); got != "remote endpoint returned HTTP 503" {
		t.Errorf("HTTPStatusDetail(503) = %q, want it to include the code", got)
	}
}

func TestState_ConcurrentSetClear(t *testing.T) {
	var s State
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			s.Set("k", Degraded, "x")
		}()
		go func() {
			defer wg.Done()
			s.Clear("k")
		}()
	}
	wg.Wait()
	_ = s.Snapshot()
}
