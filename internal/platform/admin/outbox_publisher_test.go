// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestFanOutRedeploys_PerAgentFailureIsolated(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	badErr := errors.New("missing required secret config values for bamboohr")
	res := fanOutRedeploys(ctx,
		[]string{"agent-good-1", "agent-bad", "agent-good-2"},
		func(_ context.Context, _ string) string { return DisplayStatusDeployed },
		func(_ context.Context, id string) error {
			if id == "agent-bad" {
				return badErr
			}
			return nil
		},
	)

	if res.Attempted != 3 {
		t.Errorf("Attempted = %d, want 3", res.Attempted)
	}
	if res.Succeeded != 2 {
		t.Errorf("Succeeded = %d, want 2 (good agents must not be blocked by bad agent)", res.Succeeded)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("Errors = %d entries, want 1", len(res.Errors))
	}
	if res.Errors[0].AgentID != "agent-bad" {
		t.Errorf("Errors[0].AgentID = %q, want agent-bad", res.Errors[0].AgentID)
	}
	if !errors.Is(res.Errors[0].Err, badErr) {
		t.Errorf("Errors[0].Err = %v, want wrap of badErr", res.Errors[0].Err)
	}
	if want := []string{"agent-good-1", "agent-good-2"}; !equalStrSlice(res.RedeployedAgents, want) {
		t.Errorf("RedeployedAgents = %v, want %v", res.RedeployedAgents, want)
	}
}

func TestFanOutRedeploys_AllFailReturnsAggregate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	res := fanOutRedeploys(ctx,
		[]string{"a", "b"},
		func(_ context.Context, _ string) string { return DisplayStatusDeployed },
		func(_ context.Context, id string) error { return errors.New("boom-" + id) },
	)

	if res.Succeeded != 0 {
		t.Errorf("Succeeded = %d, want 0", res.Succeeded)
	}
	if len(res.Errors) != 2 {
		t.Fatalf("Errors = %d entries, want 2", len(res.Errors))
	}
}

func TestFanOutRedeploys_SkipsNonDeployedAgents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	called := map[string]bool{}
	res := fanOutRedeploys(ctx,
		[]string{"deployed-a", "missing-b", "deployed-c"},
		func(_ context.Context, id string) string {
			if id == "missing-b" {
				return "not_deployed"
			}
			return DisplayStatusDeployed
		},
		func(_ context.Context, id string) error {
			called[id] = true
			return nil
		},
	)

	if called["missing-b"] {
		t.Error("deployFn called for not_deployed agent")
	}
	if res.Attempted != 2 {
		t.Errorf("Attempted = %d, want 2 (missing-b should be skipped)", res.Attempted)
	}
	if res.Succeeded != 2 {
		t.Errorf("Succeeded = %d, want 2", res.Succeeded)
	}
}

func TestRedeployEventError(t *testing.T) {
	t.Parallel()

	transient := errors.New("db connection reset")
	recorded := fmt.Errorf("workflow redeploy failed: connector deleted: %w", errRedeployRecorded)
	recordedAgent := fmt.Errorf("agent redeploy failed: connector deleted: %w", errRedeployRecorded)

	cases := []struct {
		name      string
		agents    fanOutResult
		workflows fanOutResult
		wantRetry bool
	}{
		{
			name:      "permanent workflow failure alone never retries",
			workflows: fanOutResult{Attempted: 1, Errors: []agentFailure{{AgentID: "wf-1", Err: recorded}}},
		},
		{
			name:      "transient workflow failure with no successes retries",
			workflows: fanOutResult{Attempted: 1, Errors: []agentFailure{{AgentID: "wf-1", Err: transient}}},
			wantRetry: true,
		},
		{
			name:      "any success absorbs a transient workflow failure",
			agents:    fanOutResult{Attempted: 1, Succeeded: 1},
			workflows: fanOutResult{Attempted: 1, Errors: []agentFailure{{AgentID: "wf-1", Err: transient}}},
		},
		{
			name:      "all agents fail with only permanent workflow failures retries for the agents",
			agents:    fanOutResult{Attempted: 1, Errors: []agentFailure{{AgentID: "agent-1", Err: transient}}},
			workflows: fanOutResult{Attempted: 1, Errors: []agentFailure{{AgentID: "wf-1", Err: recorded}}},
			wantRetry: true,
		},
		{
			name:   "permanent agent failure alone never retries",
			agents: fanOutResult{Attempted: 1, Errors: []agentFailure{{AgentID: "agent-1", Err: recordedAgent}}},
		},
		{
			name:      "permanent failures on both sides never retry",
			agents:    fanOutResult{Attempted: 1, Errors: []agentFailure{{AgentID: "agent-1", Err: recordedAgent}}},
			workflows: fanOutResult{Attempted: 1, Errors: []agentFailure{{AgentID: "wf-1", Err: recorded}}},
		},
		{
			name: "nothing attempted returns nil",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := redeployEventError(tc.agents, tc.workflows)
			if (err != nil) != tc.wantRetry {
				t.Errorf("redeployEventError = %v, want retry=%v", err, tc.wantRetry)
			}
		})
	}
}

func equalStrSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRedeployPublisher_ControlPlaneRejects_CountsItAsAFailureNotASuccess(t *testing.T) {
	h := setupE2E(t, nil)

	agentID := h.createAgent("outbox_emitter_agent", "You help with testing. Answer questions about system behavior.")
	h.deploy(agentID)
	h.waitForInstance(h.agentInstanceName(agentID), 5*time.Second)

	status, err := h.agentRepo.GetDeploymentStatus(h.ctx, agentID)
	if err != nil {
		t.Fatalf("precondition: deployment status: %v", err)
	}
	if status != DisplayStatusDeployed {
		t.Fatalf("precondition: status = %q, want %q", status, DisplayStatusDeployed)
	}
	before := h.latestDeployment(agentID)

	var logged bytes.Buffer
	publisher := NewRedeployPublisher(
		rejectingDeploymentService(t, h), h.agentRepo,
		slog.New(slog.NewTextHandler(&logged, nil)),
	)

	payload, err := json.Marshal(redeployPayload{AgentIDs: []string{agentID}, UserID: "tester"})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	event := store.OutboxEvent{
		ID:          idgen.New(),
		EventType:   "connector.updated",
		AggregateID: idgen.New(),
		Payload:     string(payload),
	}

	if pubErr := publisher.PublishOutboxEvent(h.ctx, event); pubErr != nil {
		t.Fatalf("PublishOutboxEvent returned a retryable error for a recorded rejection: %v", pubErr)
	}

	after := h.latestDeployment(agentID)
	if after.ID == before.ID {
		t.Fatalf("precondition: no new deployment row, so the redeploy never ran")
	}
	if after.Status != DeployStatusFailed {
		t.Fatalf("precondition: redeploy row status = %q, want %q", after.Status, DeployStatusFailed)
	}

	if !strings.Contains(logged.String(), "outbox: redeploy failed") {
		t.Errorf("a rejected redeploy was counted as a success instead of a failure: %q", logged.String())
	}
}

func TestRedeployPublisher_ToolPackageNotReady_LogsWarnOnlyWhileDiscoveryIsPending(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		status    string
		wantLevel string
	}{
		{status: "pending", wantLevel: "WARN"},
		{status: "failed", wantLevel: "ERROR"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			t.Parallel()

			h := setupE2E(t, nil)
			deploys := newDeployLeader(store.NewDeployRequestRepo(h.db), store.NewDeploymentIntentRepo(h.db), slog.Default())
			deploys.check, deploys.poll = 20*time.Millisecond, 20*time.Millisecond
			serveDeploys(deploys, h.deploySvc, h.wfDeploySvc, nil)
			lead(t, deploys)

			pkg := seedE2EToolset(h, "outbox_level_tools", "ready", "run")
			agentID := h.createAgentWithToolsets("outbox_level_agent", "You help with testing. Answer questions about system behavior.", []string{pkg.Name})
			h.deploy(agentID)
			wfID := h.createWorkflow("outbox_level_workflow", map[string]any{
				"toolsets":       []any{pkg.Name},
				"output_mapping": map[string]any{"result": "{{run.output}}"},
				"nodes": []any{
					map[string]any{"id": "run", "type": "tool", "tool_name": pkg.Name + "__run", "input": map[string]any{}},
				},
			})
			h.deployWorkflow(wfID)

			if err := h.toolPackageRepo.UpdateDiscoveryStatus(h.ctx, pkg.ID, tc.status, nil); err != nil {
				t.Fatalf("set package %s: %v", tc.status, err)
			}

			var logged bytes.Buffer
			pub := NewRedeployPublisher(h.deploySvc, h.agentRepo, slog.New(slog.NewTextHandler(&logged, nil))).
				WithWorkflowRedeploy(h.wfDeploySvc, h.workflowRepo)
			event := store.OutboxEvent{
				ID:          idgen.New(),
				EventType:   "tool.uploaded",
				AggregateID: pkg.ID,
				Payload:     fmt.Sprintf(`{"agentIds":[%q],"workflowIds":[%q],"name":%q,"userId":"uploader"}`, agentID, wfID, pkg.Name),
			}

			if pubErr := pub.PublishOutboxEvent(h.ctx, event); pubErr == nil {
				t.Fatal("a redeploy refused by a non-ready tool package must stay retryable, got nil")
			}
			for _, msg := range []string{"outbox: redeploy failed", "outbox: workflow redeploy failed"} {
				if want := fmt.Sprintf("level=%s msg=%q", tc.wantLevel, msg); !strings.Contains(logged.String(), want) {
					t.Errorf("package %s: want %s, got:\n%s", tc.status, want, logged.String())
				}
			}
		})
	}
}
