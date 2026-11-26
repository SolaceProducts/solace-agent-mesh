// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"errors"
	"net/http"
	"os"
	goruntime "runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	platformidentity "github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/webuisettings"
)

func fakeGatewayFactory(appConfig map[string]any, svc runtime.Services) (*GatewayCore, error) {
	adapter := &recordingAdapter{}
	core := NewGatewayCore(adapter, appConfig)
	core.SetServices(svc)
	return core, nil
}

func testServices(t *testing.T) runtime.Services {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	rt := runtime.NewRuntime(brk, nil)
	return rt.Services()
}

func TestGatewayExecutor_LoadAndInit(t *testing.T) {
	svc := testServices(t)
	exe := NewGatewayExecutor(svc, fakeGatewayFactory)

	if err := exe.LoadConfig("gw-1", map[string]any{"namespace": "test"}); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	gws := exe.Gateways()
	if len(gws) != 1 {
		t.Fatalf("Gateways() = %d, want 1", len(gws))
	}

	instances := exe.ListInstances()
	if len(instances) != 1 {
		t.Fatalf("ListInstances() = %d, want 1", len(instances))
	}
	if instances[0].Name != "gw-1" {
		t.Errorf("Name = %q, want %q", instances[0].Name, "gw-1")
	}
	if instances[0].Status != StatusCreated {
		t.Errorf("Status = %q, want %q", instances[0].Status, StatusCreated)
	}
}

func TestGatewayExecutor_MultipleGateways(t *testing.T) {
	svc := testServices(t)
	exe := NewGatewayExecutor(svc, fakeGatewayFactory)

	for _, name := range []string{"gw-http", "gw-slack", "gw-eventmesh"} {
		if err := exe.LoadConfig(name, map[string]any{"namespace": "test"}); err != nil {
			t.Fatalf("LoadConfig(%s): %v", name, err)
		}
	}

	if len(exe.Gateways()) != 3 {
		t.Fatalf("Gateways() = %d, want 3", len(exe.Gateways()))
	}

	instances := exe.ListInstances()
	names := make([]string, len(instances))
	for i, inst := range instances {
		names[i] = inst.Name
	}
	if names[0] != "gw-http" || names[1] != "gw-slack" || names[2] != "gw-eventmesh" {
		t.Errorf("names = %v, want [gw-http gw-slack gw-eventmesh]", names)
	}
}

func TestGatewayExecutor_InitStartStop(t *testing.T) {
	svc := testServices(t)
	exe := NewGatewayExecutor(svc, fakeGatewayFactory)
	_ = exe.LoadConfig("gw-1", map[string]any{"namespace": "test"})

	ctx := context.Background()

	if err := exe.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := exe.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	info, err := exe.GetInstance("gw-1")
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if info.Status != StatusRunning {
		t.Errorf("Status after Start = %q, want %q", info.Status, StatusRunning)
	}

	if err := exe.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	info, _ = exe.GetInstance("gw-1")
	if info.Status != StatusStopped {
		t.Errorf("Status after Stop = %q, want %q", info.Status, StatusStopped)
	}
}

func TestGatewayExecutor_GetInstanceNotFound(t *testing.T) {
	svc := testServices(t)
	exe := NewGatewayExecutor(svc, fakeGatewayFactory)

	_, err := exe.GetInstance("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent gateway")
	}
}

func TestGatewayExecutor_AddAndStart(t *testing.T) {
	svc := testServices(t)
	exe := NewGatewayExecutor(svc, fakeGatewayFactory)

	ctx := context.Background()
	if err := exe.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := exe.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	info, err := exe.AddAndStart(ctx, "dynamic-gw", map[string]any{"namespace": "test"})
	if err != nil {
		t.Fatalf("AddAndStart: %v", err)
	}
	if info.Status != StatusRunning {
		t.Errorf("Status = %q, want %q", info.Status, StatusRunning)
	}

	if len(exe.Gateways()) != 1 {
		t.Errorf("Gateways() = %d, want 1", len(exe.Gateways()))
	}
}

func TestGatewayExecutor_AddAndStart_RunsOnGatewayBeforeInitHook(t *testing.T) {
	svc := testServices(t)
	exe := NewGatewayExecutor(svc, fakeGatewayFactory)

	var hookRan bool
	var hookGotID string
	exe.SetGatewayHooks(&GatewayHooks{
		OnGatewayBeforeInit: func(_ context.Context, core *GatewayCore, _ map[string]any) error {
			hookRan = true
			hookGotID = core.GatewayID()
			return nil
		},
	})

	ctx := context.Background()
	if err := exe.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := exe.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	_, err := exe.AddAndStart(ctx, "dyn-gw", map[string]any{
		"namespace":  "test",
		"gateway_id": "dyn-gw",
	})
	if err != nil {
		t.Fatalf("AddAndStart: %v", err)
	}
	if !hookRan {
		t.Error("OnGatewayBeforeInit was not invoked for runtime-added gateway")
	}
	if hookGotID != "dyn-gw" {
		t.Errorf("hook saw GatewayID = %q, want dyn-gw", hookGotID)
	}
}

func TestGatewayExecutor_InstallsWebuiSettingsRegistry(t *testing.T) {
	svc := testServices(t)
	exe := NewGatewayExecutor(svc, fakeGatewayFactory)

	registry := webuisettings.NewRegistry(nil, nil)
	var built *GatewayCore
	exe.SetGatewayHooks(&GatewayHooks{
		WebuiSettingsRegistry: registry,
		OnGatewayBeforeInit: func(_ context.Context, core *GatewayCore, _ map[string]any) error {
			built = core
			return nil
		},
	})

	ctx := context.Background()
	if err := exe.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := exe.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := exe.AddAndStart(ctx, "dyn-gw", map[string]any{
		"namespace":  "test",
		"gateway_id": "dyn-gw",
	}); err != nil {
		t.Fatalf("AddAndStart: %v", err)
	}
	if built == nil {
		t.Fatal("no gateway core was built")
	}
	if built.WebuiSettingsRegistry() != registry {
		t.Error("gateway did not receive the registry from GatewayHooks; DB overrides would never reach it")
	}
}

func TestGatewayExecutor_AddAndStart_HookErrorAbortsAndRemovesPlaceholder(t *testing.T) {
	svc := testServices(t)
	exe := NewGatewayExecutor(svc, fakeGatewayFactory)
	exe.SetGatewayHooks(&GatewayHooks{
		OnGatewayBeforeInit: func(context.Context, *GatewayCore, map[string]any) error {
			return errors.New("simulated pre-init failure")
		},
	})

	ctx := context.Background()
	_ = exe.Init(ctx, nil)
	_ = exe.Start(ctx)

	_, err := exe.AddAndStart(ctx, "abort-gw", map[string]any{"namespace": "test", "gateway_id": "abort-gw"})
	if err == nil {
		t.Fatal("AddAndStart returned nil despite hook error")
	}
	if !strings.Contains(err.Error(), "simulated pre-init failure") {
		t.Errorf("error missing hook reason: %v", err)
	}
	if len(exe.Gateways()) != 0 {
		t.Errorf("placeholder should be removed on hook error; got %d gateways", len(exe.Gateways()))
	}
}

func TestGatewayExecutor_AddAndStart_Duplicate(t *testing.T) {
	svc := testServices(t)
	exe := NewGatewayExecutor(svc, fakeGatewayFactory)
	_ = exe.LoadConfig("gw-1", map[string]any{"namespace": "test"})

	ctx := context.Background()
	_, err := exe.AddAndStart(ctx, "gw-1", map[string]any{"namespace": "test"})
	if err == nil {
		t.Fatal("expected error for duplicate name")
	}
}

func TestGatewayExecutor_RemoveInstance(t *testing.T) {
	svc := testServices(t)
	exe := NewGatewayExecutor(svc, fakeGatewayFactory)
	_ = exe.LoadConfig("gw-1", map[string]any{"namespace": "test"})

	ctx := context.Background()
	_ = exe.Init(ctx, nil)
	_ = exe.Start(ctx)

	if err := exe.RemoveInstance(ctx, "gw-1"); err != nil {
		t.Fatalf("RemoveInstance: %v", err)
	}

	if len(exe.Gateways()) != 0 {
		t.Errorf("Gateways() = %d, want 0", len(exe.Gateways()))
	}
}

func TestGatewayExecutor_RemoveInstance_ColdCache_DegradedServices_NoPanic(t *testing.T) {
	exe := NewGatewayExecutor(nil, fakeGatewayFactory)
	exe.AllowRuntimeManagement = true
	if err := exe.RemoveInstance(context.Background(), "no-services-gw"); err != nil {
		t.Errorf("RemoveInstance with nil services should succeed silently, got: %v", err)
	}
}

func TestGatewayExecutor_RemoveInstance_ColdCache_DeprovisionsByName(t *testing.T) {
	svc, brk := testServicesWithTracking(t)
	exe := NewGatewayExecutor(svc, fakeGatewayFactory)

	err := exe.RemoveInstance(context.Background(), "ghost-gw")
	if err != nil {
		t.Fatalf("RemoveInstance on cold cache should succeed (idempotent deprovision), got: %v", err)
	}

	names := brk.deprovisionedNames()
	for _, want := range []string{
		"solace-agent-mesh/q/gdk/gateway/ghost-gw",
		"solace-agent-mesh/q/gdk/whatsapp_ingress/ghost-gw",
		"solace-agent-mesh/q/gdk/whatsapp_ingress/ghost-gw/v2",
	} {
		if !slices.Contains(names, want) {
			t.Errorf("DeprovisionQueue calls = %v, want to include %q", names, want)
		}
	}
}

func TestGatewayExecutor_RemoveInstance_KeepQueues_ColdCache_NoDeprovision(t *testing.T) {
	svc, brk := testServicesWithTracking(t)
	exe := NewGatewayExecutor(svc, fakeGatewayFactory)

	if err := exe.removeInstance(context.Background(), "ghost-gw", false); err != nil {
		t.Fatalf("stop-only RemoveInstance on cold cache should be a no-op success, got: %v", err)
	}

	if names := brk.deprovisionedNames(); len(names) != 0 {
		t.Errorf("stop-only delete must not touch broker queues; DeprovisionQueue calls = %v", names)
	}
}

type erroringDeprovisionGWBroker struct {
	*broker.DevBroker
	err error
}

func (b *erroringDeprovisionGWBroker) DeprovisionQueue(_ string) error {
	return b.err
}

func TestGatewayExecutor_RemoveInstance_ColdCache_BrokerError_Propagates(t *testing.T) {
	sentinel := errors.New("simulated broker DeprovisionQueue failure")
	brk := &erroringDeprovisionGWBroker{
		DevBroker: broker.NewDevBroker(),
		err:       sentinel,
	}
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	svc := runtime.NewRuntime(brk, nil).Services()
	exe := NewGatewayExecutor(svc, fakeGatewayFactory)
	exe.AllowRuntimeManagement = true

	err := exe.RemoveInstance(context.Background(), "doomed-gw")
	if err == nil {
		t.Fatal("expected RemoveInstance to return the broker error; got nil — orphan-queue regression?")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("returned error does not wrap sentinel: %v", err)
	}
}

func TestGatewayExecutor_StopAndStartInstance(t *testing.T) {
	svc := testServices(t)
	exe := NewGatewayExecutor(svc, fakeGatewayFactory)
	_ = exe.LoadConfig("gw-1", map[string]any{"namespace": "test"})

	ctx := context.Background()
	_ = exe.Init(ctx, nil)
	_ = exe.Start(ctx)

	if err := exe.StopInstance(ctx, "gw-1"); err != nil {
		t.Fatalf("StopInstance: %v", err)
	}
	info, _ := exe.GetInstance("gw-1")
	if info.Status != StatusStopped {
		t.Errorf("Status = %q, want %q", info.Status, StatusStopped)
	}
	if info.Enabled {
		t.Error("Enabled should be false after StopInstance")
	}

	if err := exe.StartInstance(ctx, "gw-1"); err != nil {
		t.Fatalf("StartInstance: %v", err)
	}
	info, _ = exe.GetInstance("gw-1")
	if info.Status != StatusRunning {
		t.Errorf("Status = %q, want %q", info.Status, StatusRunning)
	}
	if !info.Enabled {
		t.Error("Enabled should be true after StartInstance")
	}
}

func TestGatewayExecutor_RuntimeManagementDisabled(t *testing.T) {
	svc := testServices(t)
	exe := NewGatewayExecutor(svc, fakeGatewayFactory)
	exe.AllowRuntimeManagement = false

	ctx := context.Background()

	_, err := exe.AddAndStart(ctx, "gw-1", map[string]any{"namespace": "test"})
	if err == nil {
		t.Fatal("expected error when runtime management disabled")
	}

	err = exe.RemoveInstance(ctx, "gw-1")
	if err == nil {
		t.Fatal("expected error when runtime management disabled")
	}

	err = exe.StopInstance(ctx, "gw-1")
	if err == nil {
		t.Fatal("expected error when runtime management disabled")
	}

	err = exe.StartInstance(ctx, "gw-1")
	if err == nil {
		t.Fatal("expected error when runtime management disabled")
	}
}

func TestGatewayExecutor_GatewayHooks(t *testing.T) {
	svc := testServices(t)

	var signerCalled atomic.Bool
	hooks := &GatewayHooks{
		TaskSigner: taskSignerFunc(func(_ *auth.Subject, _ string) (string, error) {
			signerCalled.Store(true)
			return "token", nil
		}),
	}

	exe := NewGatewayExecutor(svc, fakeGatewayFactory)
	exe.SetGatewayHooks(hooks)
	_ = exe.LoadConfig("gw-1", map[string]any{"namespace": "test"})

	gw := exe.GetGateway("gw-1")
	if gw == nil {
		t.Fatal("GetGateway returned nil")
	}
}

func TestGatewayExecutor_GetGateway(t *testing.T) {
	svc := testServices(t)
	exe := NewGatewayExecutor(svc, fakeGatewayFactory)
	_ = exe.LoadConfig("gw-1", map[string]any{"namespace": "test"})

	gw := exe.GetGateway("gw-1")
	if gw == nil {
		t.Fatal("GetGateway returned nil")
	}

	gw = exe.GetGateway("nonexistent")
	if gw != nil {
		t.Fatal("GetGateway should return nil for nonexistent")
	}
}

func TestGatewayExecutor_InitWithNoGateways(t *testing.T) {
	svc := testServices(t)
	exe := NewGatewayExecutor(svc, fakeGatewayFactory)

	ctx := context.Background()
	if err := exe.Init(ctx, nil); err != nil {
		t.Fatalf("Init with no gateways should not error: %v", err)
	}
}

type deprovisionTrackingBroker struct {
	*broker.DevBroker
	mu            sync.Mutex
	deprovisioned []string
}

func (b *deprovisionTrackingBroker) DeprovisionQueue(name string) error {
	b.mu.Lock()
	b.deprovisioned = append(b.deprovisioned, name)
	b.mu.Unlock()
	return b.DevBroker.DeprovisionQueue(name)
}

func (b *deprovisionTrackingBroker) deprovisionedNames() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.deprovisioned))
	copy(out, b.deprovisioned)
	return out
}

func testServicesWithTracking(t *testing.T) (runtime.Services, *deprovisionTrackingBroker) {
	t.Helper()
	brk := &deprovisionTrackingBroker{DevBroker: broker.NewDevBroker()}
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	rt := runtime.NewRuntime(brk, nil)
	return rt.Services(), brk
}

func TestGatewayExecutor_RemoveInstance_DeprovisionsQueues(t *testing.T) {
	svc, brk := testServicesWithTracking(t)
	exe := NewGatewayExecutor(svc, fakeGatewayFactory)
	exe.AllowRuntimeManagement = true

	ctx := context.Background()
	if err := exe.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := exe.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := exe.AddAndStart(ctx, "gw-42", map[string]any{"namespace": "test", "gateway_id": "gw-42"}); err != nil {
		t.Fatalf("AddAndStart: %v", err)
	}

	if err := exe.RemoveInstance(ctx, "gw-42"); err != nil {
		t.Fatalf("RemoveInstance: %v", err)
	}

	names := brk.deprovisionedNames()
	wantEvent := "test/q/gdk/gateway/gw-42"
	var sawEvent bool
	for _, n := range names {
		if n == wantEvent {
			sawEvent = true
		}
	}
	if !sawEvent {
		t.Errorf("DeprovisionQueue calls = %v, want to include %q", names, wantEvent)
	}
}

type failStartAdapter struct{ recordingAdapter }

func (a *failStartAdapter) Start(_ context.Context) error {
	return context.Canceled
}

func TestGatewayExecutor_AddAndStart_KeepsQueuesOnStartFailure(t *testing.T) {
	svc, brk := testServicesWithTracking(t)

	failStartFactory := func(appConfig map[string]any, svc runtime.Services) (*GatewayCore, error) {
		core := NewGatewayCore(&failStartAdapter{}, appConfig)
		core.SetServices(svc)
		return core, nil
	}

	exe := NewGatewayExecutor(svc, failStartFactory)
	exe.AllowRuntimeManagement = true

	ctx := context.Background()
	_ = exe.Init(ctx, nil)
	_ = exe.Start(ctx)

	if _, err := exe.AddAndStart(ctx, "kept-gw", map[string]any{"namespace": "test", "gateway_id": "kept-gw"}); err == nil {
		t.Fatal("expected Start to fail")
	}
	if names := brk.deprovisionedNames(); len(names) != 0 {
		t.Errorf("a failed start deleted queues %v; only an undeploy or a delete removes them", names)
	}
}

func TestGatewayExecutor_AddAndStart_InitFailure(t *testing.T) {
	svc := testServices(t)

	failFactory := func(_ map[string]any, svc runtime.Services) (*GatewayCore, error) {
		adapter := &recordingAdapter{}
		core := NewGatewayCore(adapter, nil)
		core.SetServices(svc)
		return core, nil
	}

	exe := NewGatewayExecutor(svc, failFactory)
	ctx := context.Background()
	_ = exe.Init(ctx, nil)
	_ = exe.Start(ctx)

	_, err := exe.AddAndStart(ctx, "fail-gw", nil)
	if err == nil {
		t.Fatal("expected error from Init failure")
	}

	if len(exe.Gateways()) != 0 {
		t.Errorf("Gateways() = %d, want 0 (placeholder should be cleaned up)", len(exe.Gateways()))
	}
}

var errAdapterRefused = errors.New("adapter refused to start")

type refusingAdapter struct{ recordingAdapter }

func (a *refusingAdapter) Init(context.Context, *GatewayCore) error { return errAdapterRefused }

func openFDs() (int, bool) {
	for _, dir := range []string{"/proc/self/fd", "/dev/fd"} {
		if entries, err := os.ReadDir(dir); err == nil {
			return len(entries), true
		}
	}
	return 0, false
}

func TestGatewayExecutor_AddAndStart_InitFailureReleasesDatabase(t *testing.T) {
	svc := testServices(t)
	dbURL := store.TestDBURL(t)
	factory := func(appConfig map[string]any, svc runtime.Services) (*GatewayCore, error) {
		core := NewGatewayCore(&refusingAdapter{}, appConfig)
		core.SetServices(svc)
		return core, nil
	}
	exe := NewGatewayExecutor(svc, factory)
	ctx := context.Background()
	attempt := func() {
		t.Helper()
		_, err := exe.AddAndStart(ctx, "refused-gw", map[string]any{
			"namespace":       "test",
			"gateway_id":      "refused-gw",
			"session_service": map[string]any{"type": "sql", "database_url": dbURL},
		})
		if !errors.Is(err, errAdapterRefused) {
			t.Fatalf("AddAndStart error = %v, want the adapter's refusal", err)
		}
	}

	attempt()
	baseGoroutines := goruntime.NumGoroutine()
	baseFDs, countsFDs := openFDs()

	const attempts = 20
	for range attempts {
		attempt()
	}

	const slack = 3
	var goroutines, fds int
	deadline := time.Now().Add(5 * time.Second)
	for {
		goroutines = goruntime.NumGoroutine()
		fds, _ = openFDs()
		settled := goroutines <= baseGoroutines+slack && (!countsFDs || fds <= baseFDs+slack)
		if settled || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if goroutines > baseGoroutines+slack {
		t.Errorf("goroutines grew from %d to %d over %d refused starts", baseGoroutines, goroutines, attempts)
	}
	if countsFDs && fds > baseFDs+slack {
		t.Errorf("open file descriptors grew from %d to %d over %d refused starts", baseFDs, fds, attempts)
	}
}

type stopCountingAdapter struct {
	recordingAdapter
	stopCount atomic.Int32
}

func (a *stopCountingAdapter) Stop(_ context.Context) error {
	a.stopCount.Add(1)
	return nil
}

func TestGatewayExecutor_PerNameLock_ConcurrentRemove_NoDoubleStop(t *testing.T) {
	svc := testServices(t)
	adapter := &stopCountingAdapter{}
	factory := func(appConfig map[string]any, svc runtime.Services) (*GatewayCore, error) {
		core := NewGatewayCore(adapter, appConfig)
		core.SetServices(svc)
		return core, nil
	}
	exe := NewGatewayExecutor(svc, factory)

	ctx := context.Background()
	if err := exe.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := exe.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := exe.AddAndStart(ctx, "gw-race", map[string]any{"namespace": "test", "gateway_id": "gw-race"}); err != nil {
		t.Fatalf("AddAndStart: %v", err)
	}

	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := range 2 {
		wg.Go(func() {
			results[i] = exe.RemoveInstance(ctx, "gw-race")
		})
	}
	wg.Wait()

	for i, err := range results {
		if err != nil {
			t.Errorf("results[%d] = %v, want nil (both calls succeed after the per-name lock serialises)", i, err)
		}
	}

	if got := adapter.stopCount.Load(); got != 1 {
		t.Errorf("Stop call count = %d, want 1 (concurrent RemoveInstance double-stopped without per-name lock)", got)
	}

	if len(exe.Gateways()) != 0 {
		t.Errorf("Gateways() = %d, want 0", len(exe.Gateways()))
	}
}

func TestGatewayExecutor_PerNameLock_DifferentNamesParallel(t *testing.T) {
	svc := testServices(t)
	exe := NewGatewayExecutor(svc, fakeGatewayFactory)

	ctx := context.Background()
	if err := exe.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := exe.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range 4 {
		wg.Go(func() {
			name := []string{"a", "b", "c", "d"}[i]
			_, errs[i] = exe.AddAndStart(ctx, name, map[string]any{"namespace": "test", "gateway_id": name})
		})
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("AddAndStart[%d]: %v", i, err)
		}
	}
	if len(exe.Gateways()) != 4 {
		t.Errorf("Gateways() = %d, want 4", len(exe.Gateways()))
	}
}

type taskSignerFunc func(*auth.Subject, string) (string, error)

func (f taskSignerFunc) SignTask(id *auth.Subject, taskID string) (string, error) {
	return f(id, taskID)
}

type fixedIdentityResolver struct{ id string }

func (fixedIdentityResolver) ResolveIdentity(context.Context, string) (*auth.Subject, error) {
	return auth.NewTestSubject(auth.SubjectFields{UserID: "stub"}), nil
}

func TestGatewayExecutor_SetGatewayHooksBackfills(t *testing.T) {
	svc := testServices(t)
	exe := NewGatewayExecutor(svc, fakeGatewayFactory)

	if err := exe.LoadConfig("gw-1", map[string]any{"namespace": "test"}); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	gws := exe.Gateways()
	if len(gws) != 1 {
		t.Fatalf("Gateways() = %d, want 1", len(gws))
	}
	if got := gws[0].IdentityResolver(); got != nil {
		t.Fatalf("IdentityResolver() = %v before SetGatewayHooks, want nil", got)
	}

	exe.SetGatewayHooks(&GatewayHooks{
		IdentityResolver: fixedIdentityResolver{},
	})

	if got := gws[0].IdentityResolver(); got == nil {
		t.Error("IdentityResolver() = nil after SetGatewayHooks — backfill contract broken; " +
			"hook setup in any caller (cmd/gateway, embedded provider) relies on this " +
			"to wire hooks into gateways loaded by bootstrap.NewGatewayExecutor")
	}
}

type fakeIdentityPlatformSvc struct{}

func (fakeIdentityPlatformSvc) Resolve(_ context.Context, _ map[string]any) (*platformidentity.Resolution, error) {
	return &platformidentity.Resolution{EnrichmentStatus: platformidentity.EnrichmentOK}, nil
}

func (fakeIdentityPlatformSvc) SearchUsers(_ context.Context, _ string, _ int) ([]*platformidentity.Profile, error) {
	return nil, nil
}

func TestGatewayExecutor_IdentityPlatformService_Wired(t *testing.T) {
	svc := testServices(t)
	exe := NewGatewayExecutor(svc, fakeGatewayFactory)

	if err := exe.LoadConfig("gw-1", map[string]any{"namespace": "test", "gateway_id": "gw-1"}); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if got := exe.Gateways()[0].IdentityService(); got != nil {
		t.Fatalf("IdentityService = %v before SetGatewayHooks, want nil", got)
	}

	exe.SetGatewayHooks(&GatewayHooks{
		IdentityPlatformService: fakeIdentityPlatformSvc{},
	})

	if got := exe.Gateways()[0].IdentityService(); got == nil {
		t.Error("IdentityService = nil after SetGatewayHooks — hook not threaded through applyGatewayHooks")
	}
}

func TestGatewayExecutor_BuildResolvers_PerGatewayAndRuntime(t *testing.T) {
	svc := testServices(t)
	exe := NewGatewayExecutor(svc, fakeGatewayFactory)

	var calls []string
	exe.SetGatewayHooks(&GatewayHooks{
		BuildResolvers: func(gatewayID string, appConfig map[string]any) (ScopeResolver, IdentityResolver, error) {
			calls = append(calls, gatewayID)
			if got, _ := appConfig["gateway_id"].(string); got != gatewayID {
				t.Errorf("BuildResolvers received appConfig for %q while called for %q", got, gatewayID)
			}
			return nil, fixedIdentityResolver{}, nil
		},
	})

	if err := exe.LoadConfig("static-gw", map[string]any{"namespace": "test", "gateway_id": "static-gw"}); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := exe.AddAndStart(ctx, "runtime-gw", map[string]any{"namespace": "test", "gateway_id": "runtime-gw"}); err != nil {
		t.Fatalf("AddAndStart: %v", err)
	}

	if len(calls) != 2 {
		t.Errorf("BuildResolvers called %d times, want 2 (static + runtime)", len(calls))
	}
	if !slices.Contains(calls, "static-gw") {
		t.Errorf("BuildResolvers not called for static-gw; calls=%v", calls)
	}
	if !slices.Contains(calls, "runtime-gw") {
		t.Errorf("BuildResolvers not called for runtime-gw — silent-bypass regression for runtime-added gateways; calls=%v", calls)
	}

	for _, gw := range exe.Gateways() {
		if gw.IdentityResolver() == nil {
			t.Errorf("gateway %q IdentityResolver = nil — BuildResolvers result not wired", gw.GatewayID())
		}
	}
}

func TestGatewayExecutor_BuildResolvers_SupersedesStaticFields(t *testing.T) {
	svc := testServices(t)
	exe := NewGatewayExecutor(svc, fakeGatewayFactory)

	staticResolver := fixedIdentityResolver{id: "static"}
	dynamicResolver := fixedIdentityResolver{id: "dynamic"}

	exe.SetGatewayHooks(&GatewayHooks{
		IdentityResolver: staticResolver,
		BuildResolvers: func(_ string, _ map[string]any) (ScopeResolver, IdentityResolver, error) {
			return nil, dynamicResolver, nil
		},
	})

	if err := exe.LoadConfig("gw", map[string]any{"namespace": "test"}); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	got, ok := exe.Gateways()[0].IdentityResolver().(fixedIdentityResolver)
	if !ok {
		t.Fatalf("IdentityResolver = %T, want fixedIdentityResolver", exe.Gateways()[0].IdentityResolver())
	}
	if got.id != "dynamic" {
		t.Errorf("IdentityResolver.id = %q, want %q — BuildResolvers must win over static field", got.id, "dynamic")
	}
}

func TestGatewayExecutor_BuildResolvers_ErrorIsFailSoft(t *testing.T) {
	svc := testServices(t)
	exe := NewGatewayExecutor(svc, fakeGatewayFactory)

	exe.SetGatewayHooks(&GatewayHooks{
		BuildResolvers: func(_ string, _ map[string]any) (ScopeResolver, IdentityResolver, error) {
			return nil, nil, errors.New("authz config invalid")
		},
	})

	if err := exe.LoadConfig("gw", map[string]any{"namespace": "test"}); err != nil {
		t.Fatalf("LoadConfig must not fail when BuildResolvers errors (fail-soft contract): %v", err)
	}

	got := exe.Gateways()[0].IdentityResolver()
	if got != nil {
		t.Errorf("IdentityResolver = %v, want nil after BuildResolvers error", got)
	}
}

type stubProjectSharing struct{}

func (stubProjectSharing) GetSharedProjectIDs(context.Context, string) ([]string, error) {
	return nil, nil
}
func (stubProjectSharing) GetProjectShares(context.Context, string) (*ProjectSharesResponse, error) {
	return nil, nil
}
func (stubProjectSharing) BatchShare(context.Context, string, []ShareRequest, string) (*BatchShareResponse, error) {
	return nil, nil
}
func (stubProjectSharing) BatchUnshare(context.Context, string, []string) (*BatchDeleteResponse, error) {
	return nil, nil
}
func (stubProjectSharing) DeleteAllShares(context.Context, string) error { return nil }
func (stubProjectSharing) HasViewAccess(context.Context, string, string) (bool, error) {
	return false, nil
}

func TestGatewayExecutor_ConfigureProjectSharing_PerGatewayAndRuntime(t *testing.T) {
	svc := testServices(t)
	exe := NewGatewayExecutor(svc, fakeGatewayFactory)

	var calls []string
	exe.SetGatewayHooks(&GatewayHooks{
		ConfigureProjectSharing: func(core *GatewayCore) error {
			calls = append(calls, core.GatewayID())
			core.SetProjectSharingService(stubProjectSharing{})
			return nil
		},
	})

	if err := exe.LoadConfig("static-gw", map[string]any{"namespace": "test", "gateway_id": "static-gw"}); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := exe.AddAndStart(ctx, "runtime-gw", map[string]any{"namespace": "test", "gateway_id": "runtime-gw"}); err != nil {
		t.Fatalf("AddAndStart: %v", err)
	}

	if len(calls) != 2 {
		t.Errorf("ConfigureProjectSharing called %d times, want 2 (static + runtime)", len(calls))
	}
	if !slices.Contains(calls, "static-gw") {
		t.Errorf("ConfigureProjectSharing not called for static-gw; calls=%v", calls)
	}
	if !slices.Contains(calls, "runtime-gw") {
		t.Errorf("ConfigureProjectSharing not called for runtime-gw — silent-bypass regression for runtime-added gateways; calls=%v", calls)
	}

	for _, gw := range exe.Gateways() {
		if gw.ProjectSharing() == nil {
			t.Errorf("gateway %q ProjectSharing = nil — ConfigureProjectSharing result not wired", gw.GatewayID())
		}
	}
}

func TestGatewayExecutor_ConfigureProjectSharing_ErrorIsFailSoft(t *testing.T) {
	svc := testServices(t)
	exe := NewGatewayExecutor(svc, fakeGatewayFactory)

	exe.SetGatewayHooks(&GatewayHooks{
		ConfigureProjectSharing: func(_ *GatewayCore) error {
			return errors.New("project sharing service unavailable")
		},
	})

	if err := exe.LoadConfig("gw", map[string]any{"namespace": "test"}); err != nil {
		t.Fatalf("LoadConfig must not fail when ConfigureProjectSharing errors (fail-soft contract): %v", err)
	}

	if got := exe.Gateways()[0].ProjectSharing(); got != nil {
		t.Errorf("ProjectSharing = %v, want nil after ConfigureProjectSharing error", got)
	}
}

type stubPromptSharing struct{}

func (stubPromptSharing) GetShares(context.Context, string) ([]PromptShareResponse, error) {
	return nil, nil
}

func (stubPromptSharing) BatchShare(context.Context, string, []ShareRequest, string) (*BatchPromptShareResponse, error) {
	return nil, nil
}

func (stubPromptSharing) BulkReplaceShares(context.Context, string, []ShareRequest, string) (*PromptShareReplaceResult, error) {
	return nil, nil
}

func (stubPromptSharing) UpdateShare(context.Context, string, string, string, string) (*PromptShareResponse, error) {
	return nil, nil
}

func (stubPromptSharing) DeleteShare(context.Context, string, string, string) error {
	return nil
}

func (stubPromptSharing) DeleteAllShares(context.Context, string, string) error {
	return nil
}

func TestGatewayExecutor_ConfigurePromptSharing_PerGatewayAndRuntime(t *testing.T) {
	svc := testServices(t)
	exe := NewGatewayExecutor(svc, fakeGatewayFactory)

	var calls []string
	exe.SetGatewayHooks(&GatewayHooks{
		ConfigurePromptSharing: func(core *GatewayCore) error {
			calls = append(calls, core.GatewayID())
			core.SetPromptSharingService(stubPromptSharing{})
			return nil
		},
	})

	if err := exe.LoadConfig("static-gw", map[string]any{"namespace": "test", "gateway_id": "static-gw"}); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := exe.AddAndStart(ctx, "runtime-gw", map[string]any{"namespace": "test", "gateway_id": "runtime-gw"}); err != nil {
		t.Fatalf("AddAndStart: %v", err)
	}

	if len(calls) != 2 {
		t.Errorf("ConfigurePromptSharing called %d times, want 2 (static + runtime)", len(calls))
	}
	if !slices.Contains(calls, "static-gw") {
		t.Errorf("ConfigurePromptSharing not called for static-gw; calls=%v", calls)
	}
	if !slices.Contains(calls, "runtime-gw") {
		t.Errorf("ConfigurePromptSharing not called for runtime-gw — silent-bypass regression for runtime-added gateways; calls=%v", calls)
	}

	for _, gw := range exe.Gateways() {
		if gw.PromptSharing() == nil {
			t.Errorf("gateway %q PromptSharing = nil — ConfigurePromptSharing result not wired", gw.GatewayID())
		}
	}
}

func TestGatewayExecutor_ConfigurePromptSharing_ErrorIsFailSoft(t *testing.T) {
	svc := testServices(t)
	exe := NewGatewayExecutor(svc, fakeGatewayFactory)

	exe.SetGatewayHooks(&GatewayHooks{
		ConfigurePromptSharing: func(_ *GatewayCore) error {
			return errors.New("prompt sharing service unavailable")
		},
	})

	if err := exe.LoadConfig("gw", map[string]any{"namespace": "test"}); err != nil {
		t.Fatalf("LoadConfig must not fail when ConfigurePromptSharing errors (fail-soft contract): %v", err)
	}

	if got := exe.Gateways()[0].PromptSharing(); got != nil {
		t.Errorf("PromptSharing = %v, want nil after ConfigurePromptSharing error", got)
	}
}

type deferredAdapter struct {
	recordingAdapter
	onReady func(http.Handler)
	handler http.Handler
}

func (a *deferredAdapter) SetOnHandlerReady(cb func(http.Handler)) {
	a.onReady = cb
}

func (a *deferredAdapter) Init(_ context.Context, _ *GatewayCore) error {
	a.handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	if a.onReady != nil {
		a.onReady(a.handler)
	}
	return nil
}

func (a *deferredAdapter) MountIntent() MountSpec { return MountSpec{Kind: MountNamed} }

var _ DeferredHTTPAdapter = (*deferredAdapter)(nil)

func deferredGatewayFactory(appConfig map[string]any, svc runtime.Services) (*GatewayCore, error) {
	adapter := &deferredAdapter{}
	core := NewGatewayCore(adapter, appConfig)
	core.SetServices(svc)
	return core, nil
}

func TestGatewayExecutor_AddAndStart_RegistersHandlerUnderGatewayIDNotAppName(t *testing.T) {
	svc := testServices(t)
	exe := NewGatewayExecutor(svc, deferredGatewayFactory)

	var registeredName string
	var registeredHandler http.Handler
	exe.OnGatewayReady = func(name string, h http.Handler) {
		registeredName = name
		registeredHandler = h
	}

	ctx := context.Background()
	if err := exe.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := exe.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	const samAppName = "11111111-1111-1111-1111-111111111111"
	const slug = "my-slug"
	_, err := exe.AddAndStart(ctx, samAppName, map[string]any{
		"namespace":  "test",
		"gateway_id": slug,
	})
	if err != nil {
		t.Fatalf("AddAndStart: %v", err)
	}

	if registeredName != slug {
		t.Fatalf("proxy handler registered under %q, want %q (the slug from gateway_id, not the SAM app name)", registeredName, slug)
	}
	if registeredHandler == nil {
		t.Fatal("OnGatewayReady was never called")
	}
}

func TestGatewayExecutor_OnGatewayBeforeRemove_CalledOnRemoveInstance(t *testing.T) {
	svc := testServices(t)
	exe := NewGatewayExecutor(svc, fakeGatewayFactory)

	var beforeRemoveCalls []string
	exe.SetGatewayHooks(&GatewayHooks{
		OnGatewayBeforeRemove: func(_ context.Context, core *GatewayCore, _ map[string]any) error {
			beforeRemoveCalls = append(beforeRemoveCalls, core.GatewayID())
			return nil
		},
	})

	ctx := context.Background()
	if err := exe.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := exe.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if _, err := exe.AddAndStart(ctx, "gw-rm", map[string]any{"namespace": "test", "gateway_id": "gw-rm"}); err != nil {
		t.Fatalf("AddAndStart: %v", err)
	}
	if err := exe.RemoveInstance(ctx, "gw-rm"); err != nil {
		t.Fatalf("RemoveInstance: %v", err)
	}

	if len(beforeRemoveCalls) != 1 || beforeRemoveCalls[0] != "gw-rm" {
		t.Errorf("OnGatewayBeforeRemove calls = %v, want [gw-rm]", beforeRemoveCalls)
	}
}

type teardownOrderAdapter struct {
	deferredAdapter
	record func(string)
}

func (a *teardownOrderAdapter) Stop(_ context.Context) error {
	a.record("adapterStop")
	return nil
}

func TestGatewayExecutor_RemoveInstance_UnregistersRouteBeforeAdapterStop(t *testing.T) {
	svc := testServices(t)

	var mu sync.Mutex
	var order []string
	record := func(event string) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, event)
	}

	exe := NewGatewayExecutor(svc, func(appConfig map[string]any, svc runtime.Services) (*GatewayCore, error) {
		core := NewGatewayCore(&teardownOrderAdapter{record: record}, appConfig)
		core.SetServices(svc)
		return core, nil
	})
	exe.OnGatewayReady = func(string, http.Handler) {}
	exe.OnGatewayRemoved = func(string) { record("proxyUnregister") }

	ctx := context.Background()
	if err := exe.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := exe.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := exe.AddAndStart(ctx, "gw-order", map[string]any{"namespace": "test", "gateway_id": "gw-order"}); err != nil {
		t.Fatalf("AddAndStart: %v", err)
	}
	if err := exe.RemoveInstance(ctx, "gw-order"); err != nil {
		t.Fatalf("RemoveInstance: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{"proxyUnregister", "adapterStop"}
	if !slices.Equal(order, want) {
		t.Fatalf("teardown order = %v, want %v (the route must die before the adapter's context is cancelled, and unregistration must happen exactly once)", order, want)
	}
}

func TestGatewayExecutor_RemoveInstance_UnregistersRouteWhenStopFails(t *testing.T) {
	svc := testServices(t)

	exe := NewGatewayExecutor(svc, func(appConfig map[string]any, svc runtime.Services) (*GatewayCore, error) {
		core := NewGatewayCore(&deprovisionErrorAdapter{err: errors.New("deprovision queues failure")}, appConfig)
		core.SetServices(svc)
		return core, nil
	})

	var unregistered []string
	exe.OnGatewayRemoved = func(name string) { unregistered = append(unregistered, name) }

	ctx := context.Background()
	if err := exe.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := exe.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := exe.AddAndStart(ctx, "gw-stop-fail", map[string]any{"namespace": "test", "gateway_id": "gw-stop-fail"}); err != nil {
		t.Fatalf("AddAndStart: %v", err)
	}

	if err := exe.RemoveInstance(ctx, "gw-stop-fail"); err == nil {
		t.Fatal("RemoveInstance must report the stop failure")
	}

	if len(unregistered) != 1 || unregistered[0] != "gw-stop-fail" {
		t.Fatalf("unregistered = %v, want [gw-stop-fail] even though the stop failed", unregistered)
	}
}

func TestGatewayExecutor_OnGatewayBeforeRemove_RollsBackOnInitFailure(t *testing.T) {
	svc := testServices(t)

	failInitFactory := func(appConfig map[string]any, svc runtime.Services) (*GatewayCore, error) {
		noNamespace := map[string]any{"gateway_id": appConfig["gateway_id"]}
		adapter := &recordingAdapter{}
		core := NewGatewayCore(adapter, noNamespace)
		core.SetServices(svc)
		return core, nil
	}

	exe := NewGatewayExecutor(svc, failInitFactory)

	var preInitCalls, rollbackCalls []string
	exe.SetGatewayHooks(&GatewayHooks{
		OnGatewayBeforeInit: func(_ context.Context, core *GatewayCore, _ map[string]any) error {
			preInitCalls = append(preInitCalls, core.GatewayID())
			return nil
		},
		OnGatewayBeforeRemove: func(_ context.Context, core *GatewayCore, _ map[string]any) error {
			rollbackCalls = append(rollbackCalls, core.GatewayID())
			return nil
		},
	})

	ctx := context.Background()
	_, err := exe.AddAndStart(ctx, "fail-gw", map[string]any{"namespace": "test", "gateway_id": "fail-gw"})
	if err == nil {
		t.Fatal("AddAndStart must fail when Init fails")
	}

	if len(preInitCalls) != 1 {
		t.Errorf("OnGatewayBeforeInit called %d times, want 1", len(preInitCalls))
	}
	if len(rollbackCalls) != 1 || rollbackCalls[0] != "fail-gw" {
		t.Errorf("OnGatewayBeforeRemove (rollback) calls = %v, want [fail-gw]", rollbackCalls)
	}
	if len(exe.Gateways()) != 0 {
		t.Errorf("Gateways() = %d after failed AddAndStart, want 0", len(exe.Gateways()))
	}
}

func TestGatewayExecutor_OnGatewayBeforeRemove_RollsBackOnStartFailure(t *testing.T) {
	svc := testServices(t)

	failStartFactory := func(appConfig map[string]any, svc runtime.Services) (*GatewayCore, error) {
		core := NewGatewayCore(&failStartAdapter{}, appConfig)
		core.SetServices(svc)
		return core, nil
	}

	exe := NewGatewayExecutor(svc, failStartFactory)

	var rollbackCalls []string
	exe.SetGatewayHooks(&GatewayHooks{
		OnGatewayBeforeInit: func(_ context.Context, core *GatewayCore, _ map[string]any) error {
			return nil
		},
		OnGatewayBeforeRemove: func(_ context.Context, core *GatewayCore, _ map[string]any) error {
			rollbackCalls = append(rollbackCalls, core.GatewayID())
			return nil
		},
	})

	ctx := context.Background()
	_, err := exe.AddAndStart(ctx, "fail-start-gw", map[string]any{"namespace": "test", "gateway_id": "fail-start-gw"})
	if err == nil {
		t.Fatal("AddAndStart must fail when Start fails")
	}

	if len(rollbackCalls) != 1 || rollbackCalls[0] != "fail-start-gw" {
		t.Errorf("OnGatewayBeforeRemove (rollback) calls = %v, want [fail-start-gw]", rollbackCalls)
	}
	if len(exe.Gateways()) != 0 {
		t.Errorf("Gateways() = %d after failed AddAndStart, want 0", len(exe.Gateways()))
	}
}

func TestGatewayExecutor_OnGatewayBeforeRemove_NotCalledWhenPreInitSkipped(t *testing.T) {
	svc := testServices(t)

	failInitFactory := func(_ map[string]any, svc runtime.Services) (*GatewayCore, error) {
		adapter := &recordingAdapter{}
		core := NewGatewayCore(adapter, nil)
		core.SetServices(svc)
		return core, nil
	}

	exe := NewGatewayExecutor(svc, failInitFactory)

	var rollbackCalls int
	exe.SetGatewayHooks(&GatewayHooks{
		OnGatewayBeforeRemove: func(_ context.Context, _ *GatewayCore, _ map[string]any) error {
			rollbackCalls++
			return nil
		},
	})

	ctx := context.Background()
	_, err := exe.AddAndStart(ctx, "no-pre-init-gw", map[string]any{"namespace": "test", "gateway_id": "no-pre-init-gw"})
	if err == nil {
		t.Fatal("AddAndStart must fail when Init fails")
	}

	if rollbackCalls != 0 {
		t.Errorf("OnGatewayBeforeRemove called %d times, want 0 (no pre-init hook was set)", rollbackCalls)
	}
}

func TestGatewayExecutor_RemoveInstance_UnregistersUnderGatewayID(t *testing.T) {
	svc := testServices(t)
	exe := NewGatewayExecutor(svc, deferredGatewayFactory)

	var registeredName, removedName string
	exe.OnGatewayReady = func(name string, _ http.Handler) {
		registeredName = name
	}
	exe.OnGatewayRemoved = func(name string) {
		removedName = name
	}

	ctx := context.Background()
	if err := exe.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := exe.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	const samAppName = "22222222-2222-2222-2222-222222222222"
	const slug = "your-slug"
	if _, err := exe.AddAndStart(ctx, samAppName, map[string]any{"namespace": "test", "gateway_id": slug}); err != nil {
		t.Fatalf("AddAndStart: %v", err)
	}
	if registeredName != slug {
		t.Fatalf("registered under %q, want %q", registeredName, slug)
	}
	if err := exe.RemoveInstance(ctx, samAppName); err != nil {
		t.Fatalf("RemoveInstance: %v", err)
	}
	if removedName != slug {
		t.Fatalf("unregistered under %q, want %q (must match registration key)", removedName, slug)
	}
}

func TestGatewayExecutor_RemoveInstance_KeepQueues_DispatchesToStopNotRemove(t *testing.T) {
	svc, brk := testServicesWithTracking(t)

	adapter := &stopCountingAdapter{}
	factory := func(appConfig map[string]any, svc runtime.Services) (*GatewayCore, error) {
		core := NewGatewayCore(adapter, appConfig)
		core.SetServices(svc)
		return core, nil
	}

	exe := NewGatewayExecutor(svc, factory)
	exe.AllowRuntimeManagement = true

	var removedName string
	exe.OnGatewayRemoved = func(name string) {
		removedName = name
	}

	ctx := t.Context()
	if err := exe.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := exe.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	const gwName = "resync-gw"
	if _, err := exe.AddAndStart(ctx, gwName, map[string]any{"namespace": "test", "gateway_id": gwName}); err != nil {
		t.Fatalf("AddAndStart: %v", err)
	}

	if err := exe.removeInstance(ctx, gwName, false); err != nil {
		t.Fatalf("removeInstance(deprovisionQueues=false): %v", err)
	}

	if got := adapter.stopCount.Load(); got != 1 {
		t.Errorf("adapter.Stop call count = %d, want 1 (core.Stop must be dispatched)", got)
	}
	if names := brk.deprovisionedNames(); len(names) != 0 {
		t.Errorf("DeprovisionQueue must not be called on keep-queues remove; got %v", names)
	}
	if len(exe.Gateways()) != 0 {
		t.Errorf("Gateways() = %d, want 0 after remove", len(exe.Gateways()))
	}
	if removedName != gwName {
		t.Errorf("OnGatewayRemoved called with %q, want %q", removedName, gwName)
	}
}

type deprovisionErrorAdapter struct {
	recordingAdapter
	err error
}

func (a *deprovisionErrorAdapter) DeprovisionAdapterQueues(_ context.Context) error {
	return a.err
}

func TestGatewayExecutor_RemoveInstance_StopError_SetsStatusErrorAndKeepsGateway(t *testing.T) {
	svc := testServices(t)

	deprovErr := errors.New("deprovision queues failure")
	adapter := &deprovisionErrorAdapter{err: deprovErr}
	factory := func(appConfig map[string]any, svc runtime.Services) (*GatewayCore, error) {
		core := NewGatewayCore(adapter, appConfig)
		core.SetServices(svc)
		return core, nil
	}

	exe := NewGatewayExecutor(svc, factory)
	exe.AllowRuntimeManagement = true

	ctx := t.Context()
	if err := exe.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := exe.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	const gwName = "stop-error-gw"
	if _, err := exe.AddAndStart(ctx, gwName, map[string]any{"namespace": "test", "gateway_id": gwName}); err != nil {
		t.Fatalf("AddAndStart: %v", err)
	}

	err := exe.removeInstance(ctx, gwName, true)
	if err == nil {
		t.Fatal("removeInstance: want error, got nil")
	}
	if !strings.Contains(err.Error(), "stop gateway") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "stop gateway")
	}

	info, getErr := exe.GetInstance(gwName)
	if getErr != nil {
		t.Fatalf("GetInstance after stop error: %v", getErr)
	}
	if info.Status != StatusError {
		t.Errorf("status = %q, want %q", info.Status, StatusError)
	}

	if len(exe.Gateways()) != 1 {
		t.Errorf("Gateways() = %d, want 1 (failed stop must not remove the gateway)", len(exe.Gateways()))
	}
}

func TestGatewayExecutor_StartsDeployedEntrypointsAndKeepsQueuesOnFailure(t *testing.T) {
	ctx := context.Background()
	deployed := func(names ...string) func(context.Context) ([]control.EntrypointApply, error) {
		return func(context.Context) ([]control.EntrypointApply, error) {
			var out []control.EntrypointApply
			for _, n := range names {
				out = append(out, control.EntrypointApply{Name: n, DeploymentID: "d1", AppConfig: map[string]any{"namespace": "test", "gateway_id": n}})
			}
			return out, nil
		}
	}

	svc, _ := testServicesWithTracking(t)
	exe := NewGatewayExecutor(svc, fakeGatewayFactory)
	exe.AllowRuntimeManagement = true
	exe.SetDeployedEntrypoints(deployed("slack-1"), nil)
	if err := exe.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := exe.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = exe.Stop(ctx) })
	if exe.GetGateway("slack-1") == nil {
		t.Fatal("the deployed entrypoint was not started")
	}

	svc, brk := testServicesWithTracking(t)
	failing := NewGatewayExecutor(svc, func(appConfig map[string]any, svc runtime.Services) (*GatewayCore, error) {
		if appConfig["gateway_id"] != "broken-1" {
			return fakeGatewayFactory(appConfig, svc)
		}
		core := NewGatewayCore(&failStartAdapter{}, appConfig)
		core.SetServices(svc)
		return core, nil
	})
	failing.AllowRuntimeManagement = true
	failing.SetDeployedEntrypoints(deployed("broken-1", "slack-2"), nil)
	_ = failing.Init(ctx, nil)
	if err := failing.Start(ctx); err != nil {
		t.Fatalf("a deployed entrypoint that fails to start took the GWE down: %v", err)
	}
	t.Cleanup(func() { _ = failing.Stop(ctx) })
	if failing.GetGateway("slack-2") == nil || failing.GetGateway("broken-1") != nil {
		t.Fatal("want the working entrypoint started and the broken one stopped")
	}
	for _, n := range brk.deprovisionedNames() {
		if n == "test/q/gdk/gateway/broken-1" {
			t.Fatalf("a failed system start deleted the entrypoint's event queue")
		}
	}
	svc, _ = testServicesWithTracking(t)
	unnamed := NewGatewayExecutor(svc, fakeGatewayFactory)
	unnamed.SetDeployedEntrypoints(func(context.Context) ([]control.EntrypointApply, error) {
		return []control.EntrypointApply{{DeploymentID: "d1", AppConfig: map[string]any{}}}, nil
	}, nil)
	_ = unnamed.Init(ctx, nil)
	if err := unnamed.Start(ctx); err != nil {
		t.Fatalf("an unnamed deployed entry must be skipped: %v", err)
	}
	t.Cleanup(func() { _ = unnamed.Stop(ctx) })

	svc, _ = testServicesWithTracking(t)
	unreadable := NewGatewayExecutor(svc, fakeGatewayFactory)
	unreadable.SetDeployedEntrypoints(func(context.Context) ([]control.EntrypointApply, error) { return nil, context.DeadlineExceeded }, nil)
	_ = unreadable.Init(ctx, nil)
	if err := unreadable.Start(ctx); err == nil {
		t.Fatal("a GWE that cannot read its deployed entrypoints must not start")
	}
}
