// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const testExternalQueuesType = "test_external_queues"

type externalQueueRecorder struct {
	mu      sync.Mutex
	configs []map[string]any
	err     error
}

func (r *externalQueueRecorder) remove(_ context.Context, appConfig map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.configs = append(r.configs, appConfig)
	return r.err
}

var registerExternalQueueRecorder = sync.OnceValue(func() *externalQueueRecorder {
	r := &externalQueueRecorder{}
	entrypoint.RegisterExternalQueueRemover(testExternalQueuesType, r.remove)
	return r
})

func TestRemoveExternalQueues_UsesTheLastSuccessfulDeployConfig(t *testing.T) {
	ctx := t.Context()
	db := testOpenDB(t)
	gatewayRepo := store.NewGatewayRepo(db)
	deploymentRepo := store.NewGatewayDeploymentRepo(db)
	values := `{}`
	gwID := idgen.New()
	if err := gatewayRepo.Create(ctx, &store.Gateway{ID: gwID, Name: "entrypoint", Type: "event_mesh", Values: &values, CreatedBy: "u"}); err != nil {
		t.Fatal(err)
	}
	snapshot := func(handler string) *string {
		s := "apps:\n  - name: ep\n    app_config:\n      gateway_type: " + testExternalQueuesType + "\n      handler: " + handler + "\n"
		return &s
	}
	rows := []*store.GatewayDeployment{
		{ID: idgen.New(), GatewayID: gwID, Status: DeployStatusSuccess, Action: "deploy", GatewayConfigSnapshot: snapshot("first"), CreatedBy: "u", CreatedTime: 1},
		{ID: idgen.New(), GatewayID: gwID, Status: DeployStatusSuccess, Action: "update", GatewayConfigSnapshot: snapshot("ran"), CreatedBy: "u", CreatedTime: 2},
		{ID: idgen.New(), GatewayID: gwID, Status: DeployStatusFailed, Action: "update", GatewayConfigSnapshot: snapshot("never-ran"), CreatedBy: "u", CreatedTime: 3},
	}
	for _, d := range rows {
		if err := deploymentRepo.Create(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	recorder := registerExternalQueueRecorder()
	recorder.mu.Lock()
	recorder.configs, recorder.err = nil, errors.New("customer broker unreachable")
	recorder.mu.Unlock()

	svc := NewGatewayDeploymentService(deploymentRepo, gatewayRepo, nil, nil, slog.Default())
	svc.removeExternalQueues(ctx, gwID)

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.configs) != 1 || recorder.configs[0]["handler"] != "ran" {
		t.Fatalf("remover got %v, want one call with the last successful deploy's config", recorder.configs)
	}
}

func TestRemoveExternalQueues_NothingToRemove(t *testing.T) {
	ctx := t.Context()
	recorder := registerExternalQueueRecorder()
	recorder.mu.Lock()
	recorder.configs, recorder.err = nil, nil
	recorder.mu.Unlock()

	(&GatewayDeploymentService{}).removeExternalQueues(ctx, idgen.New())

	db := testOpenDB(t)
	gatewayRepo := store.NewGatewayRepo(db)
	deploymentRepo := store.NewGatewayDeploymentRepo(db)
	values := `{}`
	gw1, gw2, gw3 := idgen.New(), idgen.New(), idgen.New()
	for i, id := range []string{gw1, gw2, gw3} {
		if err := gatewayRepo.Create(ctx, &store.Gateway{ID: id, Name: fmt.Sprintf("ep-%d", i), Type: "event_mesh", Values: &values, CreatedBy: "u"}); err != nil {
			t.Fatal(err)
		}
	}
	unparsable := "apps: [unterminated"
	noAppConfig := "apps:\n  - name: ep\n"
	for _, d := range []*store.GatewayDeployment{
		{ID: idgen.New(), GatewayID: gw2, Status: DeployStatusSuccess, Action: "deploy", GatewayConfigSnapshot: &unparsable, CreatedBy: "u", CreatedTime: 1},
		{ID: idgen.New(), GatewayID: gw3, Status: DeployStatusSuccess, Action: "deploy", GatewayConfigSnapshot: &noAppConfig, CreatedBy: "u", CreatedTime: 1},
	} {
		if err := deploymentRepo.Create(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewGatewayDeploymentService(deploymentRepo, gatewayRepo, nil, nil, nil)
	for _, id := range []string{gw1, gw2, gw3} {
		svc.removeExternalQueues(ctx, id)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	svc.removeExternalQueues(ctx, gw1)

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.configs) != 0 {
		t.Errorf("remover called with %v, want no call", recorder.configs)
	}
}

func TestRollOutEntrypoint_Undeploy_RemovesExternalQueuesAfterEveryGWEStopped(t *testing.T) {
	ctx := t.Context()
	db := testOpenDB(t)
	gatewayRepo := store.NewGatewayRepo(db)
	deploymentRepo := store.NewGatewayDeploymentRepo(db)
	values := `{}`
	gwID := idgen.New()
	if err := gatewayRepo.Create(ctx, &store.Gateway{ID: gwID, Name: "entrypoint", Type: "event_mesh", Values: &values, CreatedBy: "u"}); err != nil {
		t.Fatal(err)
	}
	snapshot := "apps:\n  - name: ep\n    app_config:\n      gateway_type: " + testExternalQueuesType + "\n"
	if err := deploymentRepo.Create(ctx, &store.GatewayDeployment{ID: idgen.New(), GatewayID: gwID, Status: DeployStatusSuccess, Action: "deploy", GatewayConfigSnapshot: &snapshot, CreatedBy: "u", CreatedTime: 1}); err != nil {
		t.Fatal(err)
	}
	recorder := registerExternalQueueRecorder()
	recorder.mu.Lock()
	recorder.configs, recorder.err = nil, errors.New("customer broker unreachable")
	recorder.mu.Unlock()
	calls := func() int {
		recorder.mu.Lock()
		defer recorder.mu.Unlock()
		return len(recorder.configs)
	}

	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	tracker := newGWEStatusTracker(brk, "ns")
	tracker.now = func() time.Time { return now }
	tracker.listening = now.Add(-gweStatusSettle)
	svc := NewGatewayDeploymentService(deploymentRepo, gatewayRepo, nil, nil, slog.Default())
	svc.gwes = tracker
	report := func(process, deploymentID string) {
		tracker.observe(control.GatewayStatus{ProcessID: process, Entrypoints: []control.EntrypointStatus{{Name: gwID, DeploymentID: deploymentID}}})
	}
	undeploy := func(id string) *store.GatewayDeployment {
		return &store.GatewayDeployment{ID: id, GatewayID: gwID, Action: DeployActionUndeploy}
	}

	report("A", "gave-up")
	report("B", "running")
	gaveUp, cancel := context.WithCancel(ctx)
	cancel()
	if err := svc.rollOutEntrypoint(gaveUp, undeploy("gave-up"), gwID, nil); err == nil {
		t.Fatal("an undeploy that was not confirmed succeeded")
	}
	if n := calls(); n != 0 {
		t.Fatalf("remover called %d times after the wait gave up, want none", n)
	}

	report("A", "d1")
	done := make(chan error, 1)
	go func() { done <- svc.rollOutEntrypoint(ctx, undeploy("d1"), gwID, nil) }()
	time.Sleep(3 * entrypointRolloutPoll)
	if n := calls(); n != 0 {
		t.Fatalf("remover called %d times while a GWE still runs the entrypoint, want none", n)
	}
	report("B", "d1")
	if err := <-done; err != nil {
		t.Fatalf("undeploy = %v, want success although the remover failed", err)
	}
	if n := calls(); n != 1 {
		t.Errorf("remover called %d times, want once after every GWE stopped", n)
	}
}
