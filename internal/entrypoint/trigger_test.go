// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func TestBuildMessageStampsSchedulerSource(t *testing.T) {
	svc := &ScheduledTaskService{}
	userMeta := `{"source":"ui"}`
	task := &store.ScheduledTask{
		Name:            "Nightly",
		TargetAgentName: "agent1",
		TaskMessage:     `[{"kind":"text","text":"go"}]`,
		TaskMetadata:    &userMeta,
		Timezone:        "UTC",
	}
	msg := svc.buildMessage(task, "exec-1", "session-1")

	if src, _ := msg.Metadata["source"].(string); src != a2a.SourceScheduler {
		t.Fatalf("message source = %q, want %q", src, a2a.SourceScheduler)
	}
	if got := triggerFromMessage(msg); got != a2a.TriggerScheduled {
		t.Fatalf("trigger = %q, want %q", got, a2a.TriggerScheduled)
	}
}

func TestBuildMessageStampsTheScheduledTaskID(t *testing.T) {
	svc := &ScheduledTaskService{}
	spoofed := `{"scheduledTaskId":"spoofed"}`
	task := &store.ScheduledTask{
		ID:              "sched-7",
		Name:            "Nightly",
		TargetAgentName: "agent1",
		TaskMessage:     `[{"kind":"text","text":"go"}]`,
		TaskMetadata:    &spoofed,
		Timezone:        "UTC",
	}

	msg := svc.buildMessage(task, "exec-1", "session-1")

	if got, _ := msg.Metadata[a2a.MetadataScheduledTaskID].(string); got != "sched-7" {
		t.Fatalf("scheduledTaskId = %q, want sched-7", got)
	}
}

func TestTriggerFromMessage(t *testing.T) {
	scheduled := a2a.Message{Metadata: map[string]any{"source": a2a.SourceScheduler}}
	if got := triggerFromMessage(scheduled); got != a2a.TriggerScheduled {
		t.Errorf("scheduler source trigger = %q, want %q", got, a2a.TriggerScheduled)
	}

	web := a2a.Message{Metadata: map[string]any{"source": "web"}}
	if got := triggerFromMessage(web); got != a2a.TriggerInteractive {
		t.Errorf("web source trigger = %q, want %q", got, a2a.TriggerInteractive)
	}

	if got := triggerFromMessage(a2a.Message{}); got != a2a.TriggerInteractive {
		t.Errorf("no-metadata trigger = %q, want %q", got, a2a.TriggerInteractive)
	}
}
