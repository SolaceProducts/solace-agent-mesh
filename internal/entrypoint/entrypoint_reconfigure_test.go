// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type reconfigureLog struct {
	mu      sync.Mutex
	configs []map[string]any
	failing error
}

func (l *reconfigureLog) received() []map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]map[string]any{}, l.configs...)
}

func (l *reconfigureLog) failWith(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failing = err
}

type reconfigurableAdapter struct {
	recordingAdapter
	log *reconfigureLog
}

func (a *reconfigurableAdapter) Reconfigure(_ context.Context, adapterConfig map[string]any) error {
	a.log.mu.Lock()
	defer a.log.mu.Unlock()
	a.log.configs = append(a.log.configs, adapterConfig)
	return a.log.failing
}

func reconfigurableExecutor(t *testing.T) (*GatewayExecutor, *reconfigureLog, *atomic.Int32) {
	t.Helper()
	svc, _ := testServicesWithTracking(t)
	log := &reconfigureLog{}
	starts := &atomic.Int32{}
	exe := NewGatewayExecutor(svc, func(appConfig map[string]any, svc runtime.Services) (*GatewayCore, error) {
		starts.Add(1)
		core := NewGatewayCore(&reconfigurableAdapter{log: log}, appConfig)
		core.SetServices(svc)
		return core, nil
	})
	exe.AllowRuntimeManagement = true
	if err := exe.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init() = %v", err)
	}
	t.Cleanup(func() { _ = exe.Stop(context.Background()) })
	return exe, log, starts
}

func entrypointChange(deploymentID, revision string, agents ...any) *control.EntrypointApply {
	return &control.EntrypointApply{Name: "ep", DeploymentID: deploymentID, Revision: revision, AppConfig: map[string]any{
		"namespace":  "test",
		"gateway_id": "ep",
		"gateway_adapter": map[string]any{
			"type":           "fake",
			"adapter_config": map[string]any{"agents": agents},
		},
	}}
}

func TestGatewayExecutor_ASecondChangeToAReconfigurableEntrypoint(t *testing.T) {
	tests := []struct {
		name            string
		deploymentID    string
		revision        string
		wantStarts      int32
		wantReconfigure []map[string]any
		wantStatus      control.EntrypointStatus
	}{
		{
			name:         "same deployment with a new revision is reconfigured in place",
			deploymentID: "d1", revision: "r2",
			wantStarts:      1,
			wantReconfigure: []map[string]any{{"agents": []any{"alice", "bob"}}},
			wantStatus:      control.EntrypointStatus{Name: "ep", DeploymentID: "d1", Revision: "r2"},
		},
		{
			name:         "new deployment is restarted",
			deploymentID: "d2", revision: "r2",
			wantStarts:      2,
			wantReconfigure: []map[string]any{},
			wantStatus:      control.EntrypointStatus{Name: "ep", DeploymentID: "d2", Revision: "r2"},
		},
		{
			name:         "same deployment and revision is left alone",
			deploymentID: "d1", revision: "r1",
			wantStarts:      1,
			wantReconfigure: []map[string]any{},
			wantStatus:      control.EntrypointStatus{Name: "ep", DeploymentID: "d1", Revision: "r1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exe, log, starts := reconfigurableExecutor(t)
			exe.applyEntrypoint(t.Context(), "ep", entrypointChange("d1", "r1", "alice"))

			exe.applyEntrypoint(t.Context(), "ep", entrypointChange(tt.deploymentID, tt.revision, "alice", "bob"))

			if got := starts.Load(); got != tt.wantStarts {
				t.Errorf("starts after applying %s/%s = %d, want %d", tt.deploymentID, tt.revision, got, tt.wantStarts)
			}
			if diff := cmp.Diff(tt.wantReconfigure, log.received()); diff != "" {
				t.Errorf("Reconfigure calls after applying %s/%s mismatch (-want +got):\n%s", tt.deploymentID, tt.revision, diff)
			}
			got, _ := entrypointReport(exe, "ep")
			if diff := cmp.Diff(tt.wantStatus, got); diff != "" {
				t.Errorf("status after applying %s/%s mismatch (-want +got):\n%s", tt.deploymentID, tt.revision, diff)
			}
		})
	}
}

func TestGatewayExecutor_ANewRevisionRestartsAnAdapterThatCannotReconfigure(t *testing.T) {
	svc, _ := testServicesWithTracking(t)
	starts := atomic.Int32{}
	exe := NewGatewayExecutor(svc, func(appConfig map[string]any, svc runtime.Services) (*GatewayCore, error) {
		starts.Add(1)
		return fakeGatewayFactory(appConfig, svc)
	})
	exe.AllowRuntimeManagement = true
	if err := exe.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init() = %v", err)
	}
	t.Cleanup(func() { _ = exe.Stop(context.Background()) })
	exe.applyEntrypoint(t.Context(), "ep", entrypointChange("d1", "r1", "alice"))

	exe.applyEntrypoint(t.Context(), "ep", entrypointChange("d1", "r2", "alice", "bob"))

	if got := starts.Load(); got != 2 {
		t.Errorf("starts after a new revision = %d, want 2", got)
	}
	got, _ := entrypointReport(exe, "ep")
	want := control.EntrypointStatus{Name: "ep", DeploymentID: "d1", Revision: "r2"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("status after a new revision mismatch (-want +got):\n%s", diff)
	}
}

func TestGatewayExecutor_AnErrorThatNeedsARestartRestartsTheEntrypointInTheSameApply(t *testing.T) {
	exe, log, starts := reconfigurableExecutor(t)
	exe.applyEntrypoint(t.Context(), "ep", entrypointChange("d1", "r1", "alice"))
	log.failWith(fmt.Errorf("max_inflight changed: %w", ErrRestartRequired))

	exe.applyEntrypoint(t.Context(), "ep", entrypointChange("d1", "r2", "alice", "bob"))

	if got := starts.Load(); got != 2 {
		t.Errorf("starts after a Reconfigure that asked for a restart = %d, want 2", got)
	}
	if got := len(log.received()); got != 1 {
		t.Errorf("Reconfigure calls = %d, want 1: the restart follows it at once", got)
	}
	got, _ := entrypointReport(exe, "ep")
	want := control.EntrypointStatus{Name: "ep", DeploymentID: "d1", Revision: "r2"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("status after a restart that followed Reconfigure mismatch (-want +got):\n%s", diff)
	}
}

func TestGatewayExecutor_ARestartAfterAReconfigureThatCannotBeDoneInPlaceReportsAStartThatFails(t *testing.T) {
	svc, _ := testServicesWithTracking(t)
	log := &reconfigureLog{}
	starts := &atomic.Int32{}
	exe := NewGatewayExecutor(svc, func(appConfig map[string]any, svc runtime.Services) (*GatewayCore, error) {
		var adapter GatewayAdapter = &reconfigurableAdapter{log: log}
		if starts.Add(1) > 1 {
			adapter = &failStartAdapter{}
		}
		core := NewGatewayCore(adapter, appConfig)
		core.SetServices(svc)
		return core, nil
	})
	exe.AllowRuntimeManagement = true
	if err := exe.Init(t.Context(), nil); err != nil {
		t.Fatalf("Init() = %v", err)
	}
	t.Cleanup(func() { _ = exe.Stop(context.Background()) })
	exe.applyEntrypoint(t.Context(), "ep", entrypointChange("d1", "r1", "alice"))
	cards, err := svc.Broker().CreateDirectSubscription()
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Broker().Subscribe(cards, a2a.DiscoveryGatewayCardsTopic("test")); err != nil {
		t.Fatal(err)
	}
	log.failWith(fmt.Errorf("max_inflight changed: %w", ErrRestartRequired))

	exe.applyEntrypoint(t.Context(), "ep", entrypointChange("d1", "r2", "alice", "bob"))

	if got := starts.Load(); got != 2 {
		t.Errorf("starts = %d, want 2: the restart that follows the refused Reconfigure", got)
	}
	status, _ := entrypointReport(exe, "ep")
	if status.Error == "" || status.DeploymentID != "d1" || status.Revision != "r2" {
		t.Fatalf("status after a restart that failed to start = %+v, want revision r2 reported with its failure", status)
	}
	failed, _ := exe.entrypoints.current("ep")
	if wait := time.Until(failed.retryAt); failed.failures != 1 || failed.failedSince.IsZero() || wait <= 0 || wait > 10*time.Second {
		t.Fatalf("after the failed restart: %d failures since %v, next retry in %s, want 1 failure and a retry within about 5 s", failed.failures, failed.failedSince, wait)
	}
	var problems []health.Problem
	for len(problems) == 0 {
		waitCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		msg, err := svc.Broker().Receive(waitCtx, cards)
		cancel()
		if err != nil || msg == nil {
			t.Fatalf("no start failure card was published: %v", err)
		}
		var card a2a.AgentCard
		if err := json.Unmarshal(msg.Payload, &card); err != nil {
			t.Fatal(err)
		}
		problems = a2a.HealthProblemsFromCard(card)
	}
	if len(problems) != 1 || problems[0].ID != health.StartFailedProblemID || problems[0].Severity != health.Degraded || problems[0].Detail != string(health.DetailStartFailed) {
		t.Fatalf("the card carries %+v, want one Degraded start_failed problem", problems)
	}
}

func TestGatewayExecutor_AFailedReconfigureIsReportedThenRetriedThroughReconfigure(t *testing.T) {
	exe, log, starts := reconfigurableExecutor(t)
	tables := &testTables{rows: map[string]control.EntrypointApply{}}
	exe.SetDeployedEntrypoints(tables.list, tables.one)
	tables.set(*entrypointChange("d1", "r1", "alice"))
	exe.applyEntrypoint(t.Context(), "ep", nil)
	log.failWith(errors.New("agent bob refused"))
	tables.set(*entrypointChange("d1", "r2", "alice", "bob"))

	exe.applyEntrypoint(t.Context(), "ep", nil)

	failed, _ := entrypointReport(exe, "ep")
	wantFailed := control.EntrypointStatus{Name: "ep", DeploymentID: "d1", Revision: "r2", Error: "agent bob refused"}
	if diff := cmp.Diff(wantFailed, failed); diff != "" {
		t.Errorf("status after a failed Reconfigure mismatch (-want +got):\n%s", diff)
	}
	if err := exe.checkEntrypoints(t.Context()); err != nil {
		t.Fatalf("checkEntrypoints() = %v", err)
	}
	if got := starts.Load(); got != 1 {
		t.Errorf("starts after the periodic check of a failed revision = %d, want 1: its retry waits for the backoff", got)
	}
	log.failWith(nil)

	exe.applyEntrypoint(t.Context(), "ep", nil)

	if got := starts.Load(); got != 1 {
		t.Errorf("starts after retrying a failed Reconfigure = %d, want 1: the retry reconfigures and the healthy agents keep their tasks", got)
	}
	if got := len(log.received()); got != 2 {
		t.Errorf("Reconfigure calls after the retry = %d, want 2: the failed one and its retry", got)
	}
	retried, _ := entrypointReport(exe, "ep")
	wantRetried := control.EntrypointStatus{Name: "ep", DeploymentID: "d1", Revision: "r2"}
	if diff := cmp.Diff(wantRetried, retried); diff != "" {
		t.Errorf("status after retrying a failed Reconfigure mismatch (-want +got):\n%s", diff)
	}
}
