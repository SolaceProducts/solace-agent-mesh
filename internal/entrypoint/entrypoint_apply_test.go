// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func entrypointReport(e *GatewayExecutor, name string) (control.EntrypointStatus, bool) {
	for _, s := range e.entrypoints.report() {
		if s.Name == name {
			return s, true
		}
	}
	return control.EntrypointStatus{}, false
}

type testTables struct {
	mu   sync.Mutex
	rows map[string]control.EntrypointApply
}

func (tt *testTables) set(d control.EntrypointApply) {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	tt.rows[d.Name] = d
}

func (tt *testTables) drop(name string) {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	delete(tt.rows, name)
}

func (tt *testTables) list(context.Context) ([]control.EntrypointApply, error) {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	out := make([]control.EntrypointApply, 0, len(tt.rows))
	for _, d := range tt.rows {
		out = append(out, d)
	}
	return out, nil
}

func (tt *testTables) one(_ context.Context, name string) (*control.EntrypointApply, error) {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	if d, ok := tt.rows[name]; ok {
		return &d, nil
	}
	return nil, nil
}

func TestGatewayExecutor_AppliesWhatTheTablesSay(t *testing.T) {
	ctx := context.Background()
	svc, brk := testServicesWithTracking(t)
	var failing atomic.Bool
	starts := atomic.Int32{}
	adapterQueuesDeleted := &atomic.Int32{}
	exe := NewGatewayExecutor(svc, func(appConfig map[string]any, svc runtime.Services) (*GatewayCore, error) {
		starts.Add(1)
		var adapter GatewayAdapter = &adapterQueuesAdapter{deleted: adapterQueuesDeleted}
		if failing.Load() {
			adapter = &failStartAdapter{}
		}
		core := NewGatewayCore(adapter, appConfig)
		core.SetServices(svc)
		return core, nil
	})
	exe.AllowRuntimeManagement = true
	tables := &testTables{rows: map[string]control.EntrypointApply{}}
	exe.SetDeployedEntrypoints(tables.list, tables.one)
	_ = exe.Init(ctx, nil)
	t.Cleanup(func() { _ = exe.Stop(ctx) })
	config := map[string]any{"namespace": "test", "gateway_id": "ep"}
	deploy := func(id string) control.EntrypointApply {
		return control.EntrypointApply{Name: "ep", DeploymentID: id, AppConfig: config}
	}

	tables.set(deploy("d1"))
	stale := deploy("d0")
	exe.applyEntrypoint(ctx, "ep", &stale)
	exe.applyEntrypoint(ctx, "ep", nil)
	if s, _ := entrypointReport(exe, "ep"); s.DeploymentID != "d1" || exe.GetGateway("ep") == nil || starts.Load() != 1 {
		t.Fatalf("report = %+v after %d starts, want d1 from the tables started once", s, starts.Load())
	}

	cards, err := svc.Broker().CreateDirectSubscription()
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Broker().Subscribe(cards, a2a.DiscoveryGatewayCardsTopic("test")); err != nil {
		t.Fatal(err)
	}
	nextCard := func() *broker.Message {
		t.Helper()
		for {
			waitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			msg, err := svc.Broker().Receive(waitCtx, cards)
			cancel()
			if err != nil || msg == nil {
				t.Fatalf("no gateway card published: %v", err)
			}
			if strings.HasSuffix(msg.UserProperties["instance_id"], "/ep") {
				return msg
			}
		}
	}

	failing.Store(true)
	tables.set(deploy("d2"))
	exe.applyEntrypoint(ctx, "ep", nil)
	if s, _ := entrypointReport(exe, "ep"); s.DeploymentID != "d2" || s.Error == "" {
		t.Fatalf("report = %+v, want d2 with its failure", s)
	}
	var failureCard a2a.AgentCard
	if msg := nextCard(); json.Unmarshal(msg.Payload, &failureCard) != nil || failureCard.Name != "ep" || len(a2a.HealthProblemsFromCard(failureCard)) != 1 {
		t.Fatalf("start failure card = %+v, want the entrypoint's card with its failure", failureCard)
	}
	failing.Store(false)
	exe.applyEntrypoint(ctx, "ep", nil)
	if s, _ := entrypointReport(exe, "ep"); s.Error != "" || exe.GetGateway("ep") == nil {
		t.Fatalf("report = %+v, want the failed deployment retried and running", s)
	}
	if msg := nextCard(); msg.UserProperties["removed"] != "true" {
		t.Fatal("the start failure card was not withdrawn once the entrypoint started")
	}

	tables.set(control.EntrypointApply{Name: "ep", DeploymentID: "d3", Undeploy: true})
	exe.applyEntrypoint(ctx, "ep", nil)
	if s, _ := entrypointReport(exe, "ep"); exe.GetGateway("ep") != nil || s.DeploymentID != "d3" || s.Error != "" {
		t.Fatalf("report = %+v, want the entrypoint stopped by its undeploy", s)
	}

	tables.set(deploy("d4"))
	exe.applyEntrypoint(ctx, "ep", nil)
	tables.drop("ep")
	if err := exe.checkEntrypoints(ctx); err != nil {
		t.Fatal(err)
	}
	if _, listed := entrypointReport(exe, "ep"); listed || exe.GetGateway("ep") != nil {
		t.Fatal("an entrypoint gone from the tables is still running")
	}
	if names := brk.deprovisionedNames(); len(names) != 0 {
		t.Fatalf("this GWE deleted queues %v; the leader removes them", names)
	}
	if n := adapterQueuesDeleted.Load(); n != 0 {
		t.Fatalf("this GWE deleted the adapter's own queues %d times; the leader removes them", n)
	}
}

type adapterQueuesAdapter struct {
	recordingAdapter
	deleted *atomic.Int32
}

func (a *adapterQueuesAdapter) DeprovisionAdapterQueues(context.Context) error {
	a.deleted.Add(1)
	return nil
}

func TestGatewayExecutor_AppliesReceivedChangesWithoutTables(t *testing.T) {
	ctx := context.Background()
	svc, _ := testServicesWithTracking(t)
	starts := atomic.Int32{}
	exe := NewGatewayExecutor(svc, func(appConfig map[string]any, svc runtime.Services) (*GatewayCore, error) {
		starts.Add(1)
		return fakeGatewayFactory(appConfig, svc)
	})
	exe.AllowRuntimeManagement = true
	_ = exe.Init(ctx, nil)
	t.Cleanup(func() { _ = exe.Stop(ctx) })
	change := func(id string) *control.EntrypointApply {
		return &control.EntrypointApply{Name: "ep", DeploymentID: id, AppConfig: map[string]any{"namespace": "test", "gateway_id": "ep"}}
	}
	exe.applyEntrypoint(ctx, "ep", change("d1"))
	exe.applyEntrypoint(ctx, "ep", change("d1"))
	exe.applyEntrypoint(ctx, "ep", change("d2"))
	if s, _ := entrypointReport(exe, "ep"); s.DeploymentID != "d2" || starts.Load() != 2 {
		t.Fatalf("report = %+v after %d starts, want d1 started once then d2", s, starts.Load())
	}
	if !exe.ReadinessOpen() {
		t.Fatal("a GWE whose running entrypoint has bound its queues is not ready")
	}
}

func TestEntrypointRetryDelay_GrowsToItsCap(t *testing.T) {
	for failures, want := range map[int]time.Duration{1: 5 * time.Second, 2: 10 * time.Second, 4: 40 * time.Second, 9: 15 * time.Minute, 100: 15 * time.Minute} {
		for range 50 {
			if got := entrypointRetryDelay(failures); got < want*8/10 || got > want*12/10 {
				t.Fatalf("after %d failures the wait is %s, want %s give or take a fifth", failures, got, want)
			}
		}
	}
}

func TestGatewayExecutor_RetriesAFailedStartOnItsBackoff(t *testing.T) {
	ctx := context.Background()
	svc, _ := testServicesWithTracking(t)
	var failing atomic.Bool
	failing.Store(true)
	starts := atomic.Int32{}
	exe := NewGatewayExecutor(svc, func(appConfig map[string]any, svc runtime.Services) (*GatewayCore, error) {
		starts.Add(1)
		if failing.Load() {
			core := NewGatewayCore(&failStartAdapter{}, appConfig)
			core.SetServices(svc)
			return core, nil
		}
		return fakeGatewayFactory(appConfig, svc)
	})
	exe.AllowRuntimeManagement = true
	tables := &testTables{rows: map[string]control.EntrypointApply{}}
	exe.SetDeployedEntrypoints(tables.list, tables.one)
	_ = exe.Init(ctx, nil)
	t.Cleanup(func() { _ = exe.Stop(ctx) })
	config := map[string]any{"namespace": "test", "gateway_id": "ep"}
	tables.set(control.EntrypointApply{Name: "ep", DeploymentID: "d1", AppConfig: config})

	exe.applyEntrypoint(ctx, "ep", nil)
	if err := exe.checkEntrypoints(ctx); err != nil {
		t.Fatal(err)
	}
	if n := starts.Load(); n != 1 {
		t.Fatalf("%d starts, want the check to leave a failed start to its backoff", n)
	}
	now := time.Now()
	if due := exe.entrypoints.dueRetries(now); len(due) != 0 {
		t.Fatalf("due at once: %v, want a wait of about 5 s", due)
	}
	if due := exe.entrypoints.dueRetries(now.Add(7 * time.Second)); len(due) != 1 || due[0] != "ep" {
		t.Fatalf("due after 7 s: %v, want the failed entrypoint", due)
	}

	exe.applyEntrypoint(ctx, "ep", nil)
	cur, _ := exe.entrypoints.current("ep")
	if wait := time.Until(cur.retryAt); cur.failures != 2 || wait < 7*time.Second || wait > 12*time.Second {
		t.Fatalf("after a second failure: %d failures, next retry in %s, want 2 and about 10 s", cur.failures, wait)
	}

	failing.Store(false)
	tables.set(control.EntrypointApply{Name: "ep", DeploymentID: "d2", AppConfig: config})
	if err := exe.checkEntrypoints(ctx); err != nil {
		t.Fatal(err)
	}
	if s, _ := entrypointReport(exe, "ep"); s.DeploymentID != "d2" || s.Error != "" || exe.GetGateway("ep") == nil {
		t.Fatalf("report = %+v, want the new deployment started at the check", s)
	}
	if cur, _ := exe.entrypoints.current("ep"); cur.failures != 0 {
		t.Fatalf("%d failures left after a start, want 0", cur.failures)
	}
}

type switchBindBroker struct {
	*deprovisionTrackingBroker
	bound atomic.Bool
}

func (b *switchBindBroker) Bound(broker.Queue) bool { return b.bound.Load() }

func TestGatewayExecutor_ReadinessOpensOnce(t *testing.T) {
	ctx := context.Background()
	brk := &switchBindBroker{deprovisionTrackingBroker: &deprovisionTrackingBroker{DevBroker: broker.NewDevBroker()}}
	if err := brk.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	exe := NewGatewayExecutor(runtime.NewRuntime(brk, nil).Services(), fakeGatewayFactory)
	exe.AllowRuntimeManagement = true
	_ = exe.Init(ctx, nil)
	t.Cleanup(func() { _ = exe.Stop(ctx) })
	exe.applyEntrypoint(ctx, "ep", &control.EntrypointApply{Name: "ep", DeploymentID: "d1", AppConfig: map[string]any{"namespace": "test", "gateway_id": "ep"}})

	if exe.ReadinessOpen() {
		t.Fatal("ready while the running entrypoint's queues are unbound")
	}
	brk.bound.Store(true)
	if !exe.ReadinessOpen() {
		t.Fatal("not ready once the queues bound")
	}
	brk.bound.Store(false)
	if !exe.ReadinessOpen() {
		t.Fatal("a bind lost after the GWE was ready took it out of service")
	}
}

func TestGatewayExecutor_ReportsTheAttemptOfEachReceivedApply(t *testing.T) {
	ctx := context.Background()
	svc, _ := testServicesWithTracking(t)
	var failing atomic.Bool
	exe := NewGatewayExecutor(svc, func(appConfig map[string]any, svc runtime.Services) (*GatewayCore, error) {
		if failing.Load() {
			core := NewGatewayCore(&failStartAdapter{}, appConfig)
			core.SetServices(svc)
			return core, nil
		}
		return fakeGatewayFactory(appConfig, svc)
	})
	exe.AllowRuntimeManagement = true
	tables := &testTables{rows: map[string]control.EntrypointApply{}}
	exe.SetDeployedEntrypoints(tables.list, tables.one)
	_ = exe.Init(ctx, nil)
	t.Cleanup(func() { _ = exe.Stop(ctx) })
	config := map[string]any{"namespace": "test", "gateway_id": "ep"}
	row := control.EntrypointApply{Name: "ep", DeploymentID: "d1", AppConfig: config, Revision: "rev1"}
	receive := func(attempt string) {
		t.Helper()
		received := row
		received.Attempt = attempt
		exe.applyEntrypoint(ctx, "ep", &received)
	}
	reported := func() control.EntrypointStatus {
		t.Helper()
		s, _ := entrypointReport(exe, "ep")
		return s
	}

	tables.set(row)
	receive("attempt-1")
	if got := reported().Attempt; got != "attempt-1" {
		t.Fatalf("attempt = %q after the first apply, want attempt-1", got)
	}

	receive("attempt-2")
	if got := reported().Attempt; got != "attempt-2" {
		t.Fatalf("attempt = %q after an apply of the revision already applied, want attempt-2", got)
	}

	exe.applyEntrypoint(ctx, "ep", nil)
	if err := exe.checkEntrypoints(ctx); err != nil {
		t.Fatal(err)
	}
	if got := reported().Attempt; got != "attempt-2" {
		t.Fatalf("attempt = %q after applies nobody sent, want attempt-2 kept", got)
	}

	failing.Store(true)
	row.Revision = "rev2"
	tables.set(row)
	receive("attempt-3")
	if s := reported(); s.Attempt != "attempt-3" || s.Error == "" {
		t.Fatalf("report = %+v, want the failed apply under attempt-3", s)
	}
	receive("attempt-4")
	if s := reported(); s.Attempt != "attempt-4" || s.Error == "" {
		t.Fatalf("report = %+v, want the retried failure under attempt-4", s)
	}

	failing.Store(false)
	receive("attempt-5")
	if s := reported(); s.Attempt != "attempt-5" || s.Error != "" {
		t.Fatalf("report = %+v, want the recovered apply under attempt-5", s)
	}

	row.Revision = "rev3"
	tables.set(row)
	if err := exe.checkEntrypoints(ctx); err != nil {
		t.Fatal(err)
	}
	if s := reported(); s.Revision != "rev3" || s.Attempt != "" || s.Error != "" {
		t.Fatalf("report = %+v, want rev3 applied by the poll with no attempt, so no earlier attempt claims it", s)
	}
	receive("attempt-6")
	if s := reported(); s.Revision != "rev3" || s.Attempt != "attempt-6" {
		t.Fatalf("report = %+v, want a late message for rev3 to name its attempt", s)
	}
}

func TestGatewayExecutor_KeepsAReceivedAttemptOnlyForTheDeploymentAndRevisionItWasSentFor(t *testing.T) {
	ctx := context.Background()
	svc, _ := testServicesWithTracking(t)
	var failing atomic.Bool
	exe := NewGatewayExecutor(svc, func(appConfig map[string]any, svc runtime.Services) (*GatewayCore, error) {
		if failing.Load() {
			core := NewGatewayCore(&failStartAdapter{}, appConfig)
			core.SetServices(svc)
			return core, nil
		}
		return fakeGatewayFactory(appConfig, svc)
	})
	exe.AllowRuntimeManagement = true
	tables := &testTables{rows: map[string]control.EntrypointApply{}}
	exe.SetDeployedEntrypoints(tables.list, tables.one)
	_ = exe.Init(ctx, nil)
	t.Cleanup(func() { _ = exe.Stop(ctx) })
	row := control.EntrypointApply{Name: "ep", DeploymentID: "d1", AppConfig: map[string]any{"namespace": "test", "gateway_id": "ep"}, Revision: "rev1"}
	receive := func(attempt string) {
		t.Helper()
		received := row
		received.Attempt = attempt
		exe.applyEntrypoint(ctx, "ep", &received)
	}
	poll := func() {
		t.Helper()
		tables.set(row)
		if err := exe.checkEntrypoints(ctx); err != nil {
			t.Fatal(err)
		}
	}
	reported := func() control.EntrypointStatus {
		t.Helper()
		s, _ := entrypointReport(exe, "ep")
		return s
	}

	tables.set(row)
	receive("attempt-1")
	row.DeploymentID = "d2"
	poll()
	if s := reported(); s.DeploymentID != "d2" || s.Revision != "rev1" || s.Attempt != "" || s.Error != "" {
		t.Fatalf("report = %+v, want d2 at the same revision applied by the poll with no attempt, so the attempt of d1 does not claim it", s)
	}

	receive("attempt-2")
	failing.Store(true)
	row.Revision = "rev2"
	poll()
	if s := reported(); s.Revision != "rev2" || s.Attempt != "" || s.Error == "" {
		t.Fatalf("report = %+v, want the failure of the poll's own apply under no attempt", s)
	}

	failing.Store(false)
	receive("attempt-3")
	if s := reported(); s.Revision != "rev2" || s.Attempt != "attempt-3" || s.Error != "" {
		t.Fatalf("report = %+v, want the retry that succeeded under attempt-3 with no error", s)
	}

	failing.Store(true)
	row.Revision = "rev3"
	tables.set(row)
	receive("attempt-4")
	if s := reported(); s.Revision != "rev3" || s.Attempt != "attempt-4" || s.Error == "" {
		t.Fatalf("report = %+v, want the failure under attempt-4", s)
	}
	exe.applyEntrypoint(ctx, "ep", nil)
	if s := reported(); s.Revision != "rev3" || s.Attempt != "attempt-4" || s.Error == "" {
		t.Fatalf("report = %+v, want the retry nobody sent to keep attempt-4 for the same revision", s)
	}
}
