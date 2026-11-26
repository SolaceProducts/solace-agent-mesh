// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"slices"
	"testing"
)

func TestListAgentNames(t *testing.T) {
	exe := NewExecutor(newTestServices())
	exe.AddInstance(newMockInstance("agent-a", "agent"))
	exe.AddInstance(newMockInstance("flow-1", "workflow"))
	exe.AddInstance(newMockInstance("agent-b", "agent"))

	got := exe.ListAgentNames()
	slices.Sort(got)
	want := []string{"agent-a", "agent-b"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v (workflow kind must be excluded)", got, want)
	}
}

func TestListAgentNames_Empty(t *testing.T) {
	exe := NewExecutor(newTestServices())
	if got := exe.ListAgentNames(); len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

func TestListAgentNames_PrefersDisplayName(t *testing.T) {
	exe := NewExecutor(newTestServices())
	withDisplay := newMockInstance("a1b2-uuid", "agent")
	withDisplay.displayName = "Sales Assistant"
	exe.AddInstance(withDisplay)
	exe.AddInstance(newMockInstance("plain-slug", "agent"))

	got := exe.ListAgentNames()
	slices.Sort(got)
	want := []string{"Sales Assistant", "plain-slug"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v (display name preferred, slug fallback)", got, want)
	}
}
