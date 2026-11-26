// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

var readerChecksDuringInit []func(t *testing.T, exe *GatewayExecutor)

type gatedRefusingAdapter struct {
	recordingAdapter
	reached chan struct{}
	release chan struct{}
}

func (a *gatedRefusingAdapter) Init(context.Context, *GatewayCore) error {
	close(a.reached)
	<-a.release
	return errAdapterRefused
}

func databaseConfig(t *testing.T, gatewayID string) map[string]any {
	t.Helper()
	return map[string]any{
		"namespace":           "test",
		"gateway_id":          gatewayID,
		"force_user_identity": "forced-user",
		"session_service":     map[string]any{"type": "sql", "database_url": store.TestDBURL(t)},
	}
}

func TestGatewayExecutor_StoreReadersSkipAGatewayStillInInit(t *testing.T) {
	adapter := &gatedRefusingAdapter{reached: make(chan struct{}), release: make(chan struct{})}
	factory := func(appConfig map[string]any, svc runtime.Services) (*GatewayCore, error) {
		core := NewGatewayCore(adapter, appConfig)
		core.SetServices(svc)
		return core, nil
	}
	exe := NewGatewayExecutor(testServices(t), factory)
	added := make(chan error, 1)
	go func() {
		_, err := exe.AddAndStart(context.Background(), "initializing-gw", databaseConfig(t, "initializing-gw"))
		added <- err
	}()
	<-adapter.reached

	if db := exe.activityStoreDB(); db != nil {
		t.Error("activityStoreDB handed out the store of a gateway still in Init")
	}
	if _, _, err := exe.analyticsService(); err == nil {
		t.Error("analyticsService read the store of a gateway still in Init")
	}
	if id := exe.trustOffDefaultIdentity(); id == "forced-user" {
		t.Error("trustOffDefaultIdentity read the config of a gateway still in Init")
	}
	for _, check := range readerChecksDuringInit {
		check(t, exe)
	}

	close(adapter.release)
	if err := <-added; !errors.Is(err, errAdapterRefused) {
		t.Fatalf("AddAndStart error = %v, want the adapter's refusal", err)
	}
	if db := exe.activityStoreDB(); db != nil {
		t.Error("activityStoreDB handed out the store of a gateway whose Init failed")
	}
}

func TestGatewayCore_InitFailureLeavesNoStore(t *testing.T) {
	core := NewGatewayCore(&refusingAdapter{}, databaseConfig(t, "refused-gw"))
	core.SetServices(testServices(t))

	if err := core.Init(t.Context(), nil); !errors.Is(err, errAdapterRefused) {
		t.Fatalf("Init error = %v, want the adapter's refusal", err)
	}
	if core.Store() != nil {
		t.Error("a core whose Init failed still reports the store it closed")
	}
}

func TestGatewayCore_StopAfterInitFailedWithADatabase(t *testing.T) {
	core := NewGatewayCore(&refusingAdapter{}, databaseConfig(t, "refused-gw"))
	core.SetServices(testServices(t))
	if err := core.Init(t.Context(), nil); !errors.Is(err, errAdapterRefused) {
		t.Fatalf("Init error = %v, want the adapter's refusal", err)
	}

	stopped := make(chan error, 1)
	go func() { stopped <- core.Stop(context.Background()) }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Errorf("Stop after a failed Init = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Stop after a failed Init did not return")
	}
}
