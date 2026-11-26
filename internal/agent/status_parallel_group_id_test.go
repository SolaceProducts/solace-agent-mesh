// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func TestFormatParallelGroupID_UniqueInTightLoopWithoutTruncatingTheUUID(t *testing.T) {
	const n = 1000
	seen := make(map[string]struct{}, n)
	for range n {
		id := NewParallelGroupID()
		if !strings.HasPrefix(id, "llm_batch_") {
			t.Fatalf("missing llm_batch_ prefix: %q", id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate parallel group ID minted in tight loop: %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestPublishArtifactNotificationSignals_MultiArtifactSharesGroupID(t *testing.T) {
	h := newToolStatusHarness(t, "task-artifacts-multi")
	artifacts := []artifactCreationRecord{
		{Filename: "a.md", CallID: "call-a", Status: "success"},
		{Filename: "b.md", CallID: "call-b", Status: "success"},
		{Filename: "c.md", CallID: "call-c", Status: "success"},
	}
	h.agent.publishArtifactNotificationSignals(t.Context(), h.sp, artifacts)

	signals := drainStatusSignals(t, h.broker, h.queue, 6)
	var groups []any
	for _, s := range signals {
		if s["type"] == "tool_invocation_start" {
			groups = append(groups, s["parallel_group_id"])
		}
	}
	if len(groups) != 3 {
		t.Fatalf("expected 3 tool_invocation_start signals, got %d", len(groups))
	}
	first, ok := groups[0].(string)
	if !ok || first == "" {
		t.Fatalf("multi-artifact notifications must carry a non-empty parallel_group_id, got %v", groups[0])
	}
	for i, g := range groups {
		if g != first {
			t.Errorf("signal[%d] parallel_group_id = %v, want all equal to %q", i, g, first)
		}
	}
}

func TestPublishArtifactNotificationSignals_SingleArtifactNoGroup(t *testing.T) {
	h := newToolStatusHarness(t, "task-artifacts-single")
	h.agent.publishArtifactNotificationSignals(t.Context(), h.sp, []artifactCreationRecord{
		{Filename: "only.md", CallID: "call-only", Status: "success"},
	})

	signals := drainStatusSignals(t, h.broker, h.queue, 2)
	for _, s := range signals {
		if s["type"] == "tool_invocation_start" && s["parallel_group_id"] != nil {
			t.Errorf("single artifact must not be grouped; parallel_group_id = %v, want nil", s["parallel_group_id"])
		}
	}
}

func TestInjectArtifactNotifications_MultiArtifactSharesGroupID(t *testing.T) {
	h := newToolStatusHarness(t, "task-inject-multi")
	artifacts := []artifactCreationRecord{
		{Filename: "a.md", CallID: "call-a", Status: "success"},
		{Filename: "b.md", CallID: "call-b", Status: "success"},
	}
	var messages []llm.ChatMessage
	h.agent.injectArtifactNotifications(t.Context(), h.sp, "raw", artifacts, &messages)

	signals := drainStatusSignals(t, h.broker, h.queue, 4)
	var groups []any
	for _, s := range signals {
		if s["type"] == "tool_invocation_start" {
			groups = append(groups, s["parallel_group_id"])
		}
	}
	if len(groups) != 2 {
		t.Fatalf("expected 2 tool_invocation_start signals, got %d", len(groups))
	}
	first, ok := groups[0].(string)
	if !ok || first == "" {
		t.Fatalf("multi-artifact notifications must carry a non-empty parallel_group_id, got %v", groups[0])
	}
	if groups[1] != first {
		t.Errorf("parallel_group_id mismatch: %v vs %v (want all equal)", groups[1], first)
	}
}

func TestInjectArtifactNotifications_SingleArtifactNoGroup(t *testing.T) {
	h := newToolStatusHarness(t, "task-inject-single")
	var messages []llm.ChatMessage
	h.agent.injectArtifactNotifications(t.Context(), h.sp, "raw", []artifactCreationRecord{
		{Filename: "only.md", CallID: "call-only", Status: "success"},
	}, &messages)

	signals := drainStatusSignals(t, h.broker, h.queue, 2)
	for _, s := range signals {
		if s["type"] == "tool_invocation_start" && s["parallel_group_id"] != nil {
			t.Errorf("single artifact must not be grouped; parallel_group_id = %v, want nil", s["parallel_group_id"])
		}
	}
}
