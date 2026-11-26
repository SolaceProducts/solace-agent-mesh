// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"math"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
)

func TestToolExecutionAuditKeepsSubMillisecondResolution(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)

	auditLog := &recordingAuditLogger{}
	svc := &testServices{
		brk: brk,
		authenticator: &fakeAuthenticator{
			identity: &auth.Identity{
				UserID:    "alice@example.com",
				Scopes:    []string{"tool:echo_tool:invoke"},
				AuthToken: "valid-jwt",
			},
		},
		toolAuth:    &denyToolAuthorizer{denied: map[string]bool{}, audit: auditLog},
		auditLogger: auditLog,
	}
	worker := newTestWorker(t, svc, manifestPath)

	ctx := context.Background()
	if err := worker.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := worker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Stop(ctx) })

	const samples = 5
	for range samples {
		if _, err := invokeToolOverBroker(t, ctx, brk, "echo_tool", map[string]string{
			remotetool.PropAuthToken: "valid-jwt",
		}); err != nil {
			t.Fatal(err)
		}
	}

	if len(auditLog.toolExecutions) != samples {
		t.Fatalf("recorded %d tool execution events, want %d; without them this test asserts "+
			"nothing about the duration at all", len(auditLog.toolExecutions), samples)
	}

	fractional := 0
	durations := make([]float64, 0, samples)
	for _, e := range auditLog.toolExecutions {
		durations = append(durations, e.DurationMs)
		if e.DurationMs < 0 {
			t.Errorf("negative execution duration %v", e.DurationMs)
		}
		if e.DurationMs != math.Trunc(e.DurationMs) {
			fractional++
		}
	}
	if fractional == 0 {
		t.Errorf("all %d recorded durations are whole milliseconds (%v); the call site is "+
			"truncating, so any execution finishing inside a millisecond audits as 0 — "+
			"indistinguishable from one that never ran", samples, durations)
	}
}
