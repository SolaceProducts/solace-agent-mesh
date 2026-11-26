// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"slices"
	"testing"
	"time"
)

func TestSTRRuntimeRegistry_ObserveAndActive(t *testing.T) {
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	r := newSTRRuntimeRegistry(24*time.Hour, func() time.Time { return now })

	r.Observe("linux", "arm64", "3.11")
	r.Observe("linux", "amd64", "3.11")
	r.Observe("linux", "arm64", "3.11")

	got := r.Active()
	if len(got) != 2 {
		t.Fatalf("Active() len = %d; want 2 (linux/amd64, linux/arm64)", len(got))
	}
	wantTargets := []string{"linux/amd64", "linux/arm64"}
	gotTargets := []string{got[0].Target(), got[1].Target()}
	if !slices.Equal(gotTargets, wantTargets) {
		t.Errorf("Active() targets = %v; want %v (sorted)", gotTargets, wantTargets)
	}
}

func TestSTRRuntimeRegistry_IgnoresEmpty(t *testing.T) {
	r := NewSTRRuntimeRegistry()
	r.Observe("", "arm64", "3.11")
	r.Observe("linux", "", "3.11")
	r.Observe("", "", "3.11")
	if got := r.Active(); len(got) != 0 {
		t.Errorf("Active() should be empty when all observations are partial; got %+v", got)
	}
}

func TestSTRRuntimeRegistry_TTLExpiry(t *testing.T) {
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	r := newSTRRuntimeRegistry(1*time.Hour, func() time.Time { return now })

	r.Observe("linux", "amd64", "3.11")

	now = now.Add(2 * time.Hour)
	got := r.Active()
	if len(got) != 0 {
		t.Errorf("expired entry should be pruned; got %+v", got)
	}

	r.Observe("linux", "arm64", "3.11")
	got = r.Active()
	if len(got) != 1 || got[0].Target() != "linux/arm64" {
		t.Errorf("after fresh Observe, expected [linux/arm64]; got %+v", got)
	}
}
