// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"testing"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type orderRecorder struct {
	label     string
	fail      bool
	callOrder *[]string
}

func (r *orderRecorder) PublishOutboxEvent(_ context.Context, _ store.OutboxEvent) error {
	*r.callOrder = append(*r.callOrder, r.label)
	if r.fail {
		return errors.New(r.label + " failed")
	}
	return nil
}

func TestBuildOutboxRoutes_SyncRunsBeforeRedeploy(t *testing.T) {
	t.Parallel()

	var callOrder []string
	syncPub := &orderRecorder{label: "sync", callOrder: &callOrder}
	redeployPub := &orderRecorder{label: "redeploy", fail: true, callOrder: &callOrder}

	routes := buildOutboxRoutes(syncPub, redeployPub, nil, nil, nil)
	composite := platpkg.NewCompositeOutboxPublisher(routes, nil)

	event := store.OutboxEvent{
		ID:          "evt-tool-uploaded",
		EventType:   "tool.uploaded",
		AggregateID: "pkg-123",
		Payload:     `{}`,
	}
	if err := composite.PublishOutboxEvent(context.Background(), event); err == nil {
		t.Fatal("expected composite to surface redeploy failure (so outbox retries)")
	}

	if len(callOrder) < 2 {
		t.Fatalf("expected both publishers to be invoked, got %v", callOrder)
	}
	if callOrder[0] != "sync" {
		t.Errorf("publisher order = %v; want sync first, redeploy second (see buildOutboxRoutes doc comment)", callOrder)
	}
	if callOrder[1] != "redeploy" {
		t.Errorf("publisher order = %v; want sync first, redeploy second", callOrder)
	}
}

func TestBuildOutboxRoutes_SkillUploadedSyncsBeforeRedeploy(t *testing.T) {
	t.Parallel()

	var callOrder []string
	syncPub := &orderRecorder{label: "sync", callOrder: &callOrder}
	redeployPub := &orderRecorder{label: "redeploy", callOrder: &callOrder}

	routes := buildOutboxRoutes(syncPub, redeployPub, nil, nil, nil)
	composite := platpkg.NewCompositeOutboxPublisher(routes, nil)

	event := store.OutboxEvent{
		ID:          "evt-skill-uploaded",
		EventType:   "skill.uploaded",
		AggregateID: "skl-123",
		Payload:     `{"name":"my_skill","agentIds":["agent-1"],"userId":"sam"}`,
	}
	if err := composite.PublishOutboxEvent(context.Background(), event); err != nil {
		t.Fatalf("unexpected error from composite: %v", err)
	}

	if len(callOrder) != 2 {
		t.Fatalf("expected both publishers to be invoked once each, got %v", callOrder)
	}
	if callOrder[0] != "sync" || callOrder[1] != "redeploy" {
		t.Errorf("publisher order = %v; want sync first, redeploy second", callOrder)
	}
}

func TestBuildOutboxRoutes_ConnectorEventGoesToRedeployOnly(t *testing.T) {
	t.Parallel()

	var callOrder []string
	syncPub := &orderRecorder{label: "sync", callOrder: &callOrder}
	redeployPub := &orderRecorder{label: "redeploy", callOrder: &callOrder}

	routes := buildOutboxRoutes(syncPub, redeployPub, nil, nil, nil)
	composite := platpkg.NewCompositeOutboxPublisher(routes, nil)

	event := store.OutboxEvent{
		ID:          "evt-connector-updated",
		EventType:   "connector.updated",
		AggregateID: "conn-1",
		Payload:     `{"name":"my_conn","agentIds":["agent-1"],"userId":"sam"}`,
	}
	if err := composite.PublishOutboxEvent(context.Background(), event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(callOrder) != 1 || callOrder[0] != "redeploy" {
		t.Errorf("connector.updated should route to redeploy only; got %v", callOrder)
	}
}

func TestBuildOutboxRoutes_NilPublishersOmitted(t *testing.T) {
	t.Parallel()

	if r := buildOutboxRoutes(nil, nil, nil, nil, nil); len(r) != 0 {
		t.Errorf("buildOutboxRoutes(nil, nil, nil, nil, nil) = %d routes, want 0", len(r))
	}

	var callOrder []string
	syncOnly := &orderRecorder{label: "sync", callOrder: &callOrder}
	routes := buildOutboxRoutes(syncOnly, nil, nil, nil, nil)
	if len(routes) != 2 {
		t.Errorf("syncOnly should yield 2 routes (tool., skill.); got %d", len(routes))
	}

	redeployOnly := &orderRecorder{label: "redeploy", callOrder: &callOrder}
	routes = buildOutboxRoutes(nil, redeployOnly, nil, nil, nil)
	if len(routes) != 3 {
		t.Errorf("redeployOnly should yield 3 routes (connector., tool., skill.); got %d", len(routes))
	}

	modelOnly := &orderRecorder{label: "model", callOrder: &callOrder}
	routes = buildOutboxRoutes(nil, nil, modelOnly, nil, nil)
	if len(routes) != 1 || routes[0].Prefix != modelOutboxPrefix {
		t.Errorf("modelOnly should yield one %q route; got %+v", modelOutboxPrefix, routes)
	}
}
