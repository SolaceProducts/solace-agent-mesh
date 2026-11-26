// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func scheduledRunUserTimezone(t *testing.T, tz string) string {
	t.Helper()
	core, brk := setupTaskLoggingCore(t)
	svc := core.ScheduledTasks()
	if svc == nil {
		t.Fatal("ScheduledTasks() returned nil")
	}
	q, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "test-agent")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	task := makeIdentityTestTask(idgen.New(), "alice@example.com")
	task.Timezone = tz
	insertTask(t, svc, task)

	if _, err = svc.TriggerTaskNow(t.Context(), task.ID, "alice@example.com"); err != nil {
		t.Fatalf("TriggerTaskNow: %v", err)
	}
	if err = svc.RunPasses(t.Context()); err != nil {
		t.Fatalf("RunPasses: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatalf("expected the scheduled run to publish a task: %v", err)
	}
	var cfg struct {
		UserTimezone string `json:"user_timezone"`
	}
	if blob := a2a.DecodeUserProperties(msg.UserProperties).A2AUserConfig; blob != "" {
		if err := json.Unmarshal([]byte(blob), &cfg); err != nil {
			t.Fatalf("a2aUserConfig is not a JSON object: %v (%q)", err, blob)
		}
	}
	return cfg.UserTimezone
}

func TestScheduledRun_ForwardsTaskTimezoneToAgent(t *testing.T) {
	if got := scheduledRunUserTimezone(t, "Asia/Tokyo"); got != "Asia/Tokyo" {
		t.Errorf("published user_timezone = %q, want the task's Asia/Tokyo", got)
	}
}

func TestScheduledRun_UnusableTaskTimezoneFallsBackToGatewayDefault(t *testing.T) {
	if got := scheduledRunUserTimezone(t, "Not/A_Real_Zone"); got != "UTC" {
		t.Errorf("published user_timezone = %q, want the UTC default", got)
	}
}
