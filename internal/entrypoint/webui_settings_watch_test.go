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
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/webuisettings"
)

func TestWebuiSettingsWatchLoop_QueueNotFoundIsTerminal(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	core := NewGatewayCore(&recordingAdapter{}, map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "webui-gw",
	})
	core.SetServices(runtime.NewRuntime(&queueNotFoundBroker{Broker: brk}, nil).Services())
	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	core.SetWebuiSettingsRegistry(webuisettings.NewRegistry(nil, nil))

	done := make(chan struct{})
	go func() {
		defer close(done)
		core.webuiSettingsWatchLoop(context.Background())
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watch loop did not exit on ErrQueueNotFound — it is spinning on a terminal error")
	}
}

type queueNotFoundBroker struct {
	broker.Broker
}

func (b *queueNotFoundBroker) Receive(context.Context, broker.Queue) (*broker.Message, error) {
	return nil, broker.ErrQueueNotFound
}

func startWatchedCoreWithFetcher(t *testing.T, brk broker.Broker, fetcher func(context.Context) (*webuisettings.Settings, error)) *webuisettings.Registry {
	t.Helper()
	registry := webuisettings.NewRegistry(fetcher, nil)

	core := NewGatewayCore(&recordingAdapter{}, map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "webui-gw",
	})
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	core.SetServesWebUI(true)
	core.SetWebuiSettingsRegistry(registry)
	if err := core.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })
	return registry
}

func startWatchedCore(t *testing.T, brk broker.Broker, fetches *atomic.Int32) *webuisettings.Registry {
	t.Helper()
	registry := webuisettings.NewRegistry(func(context.Context) (*webuisettings.Settings, error) {
		n := fetches.Add(1)
		return &webuisettings.Settings{AppName: ptr(fmt.Sprintf("pulled-v%d", n))}, nil
	}, nil)

	core := NewGatewayCore(&recordingAdapter{}, map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "webui-gw",
	})
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	core.SetServesWebUI(true)
	core.SetWebuiSettingsRegistry(registry)
	if err := core.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })
	return registry
}

func ptr[T any](v T) *T { return &v }

func waitForAppName(t *testing.T, registry *webuisettings.Registry, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last *webuisettings.Settings
	for time.Now().Before(deadline) {
		got, err := registry.Get(context.Background())
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		last = got
		if got != nil && got.AppName != nil && *got.AppName == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("registry never reached appName %q; last value = %+v", want, last)
}

func TestGatewayCore_WebuiSettingsWatch_RowPayloadIsIgnoredAndRePulled(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	var fetches atomic.Int32
	registry := startWatchedCore(t, brk, &fetches)

	if _, err := registry.Get(context.Background()); err != nil {
		t.Fatalf("priming Get: %v", err)
	}
	pulledBefore := fetches.Load()

	payload, err := json.Marshal(webuisettings.Settings{AppName: ptr("Broadcast Acme")})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	topic := a2a.WebuiSettingsUpdateTopic("test-ns")
	if err := brk.Publish(context.Background(), topic, &broker.Message{Topic: topic, Payload: payload}); err != nil {
		t.Fatalf("publish update: %v", err)
	}

	waitForAppName(t, registry, fmt.Sprintf("pulled-v%d", pulledBefore+1))
}

func TestGatewayCore_WebuiSettingsWatch_UndecodablePayloadFallsBackToRefetch(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	var fetches atomic.Int32
	registry := startWatchedCore(t, brk, &fetches)

	if _, err := registry.Get(context.Background()); err != nil {
		t.Fatalf("priming Get: %v", err)
	}

	pulledBefore := fetches.Load()
	if pulledBefore != 1 {
		t.Fatalf("startup warm and the priming Get pulled %d times, want 1 — they must coalesce, or the count below is measuring the wrong thing", pulledBefore)
	}

	topic := a2a.WebuiSettingsUpdateTopic("test-ns")
	if err := brk.Publish(context.Background(), topic, &broker.Message{Topic: topic, Payload: []byte("{not json")}); err != nil {
		t.Fatalf("publish update: %v", err)
	}

	waitForAppName(t, registry, fmt.Sprintf("pulled-v%d", pulledBefore+1))

	if got := fetches.Load(); got != pulledBefore+1 {
		t.Errorf("control plane pulled %d time(s), want exactly 1", got-pulledBefore)
	}
}

func TestGatewayCore_WebuiSettingsWatch_EmptyPayloadFallsBackToRefetch(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	var fetches atomic.Int32
	registry := startWatchedCore(t, brk, &fetches)

	if _, err := registry.Get(context.Background()); err != nil {
		t.Fatalf("priming Get: %v", err)
	}
	pulledBefore := fetches.Load()

	topic := a2a.WebuiSettingsUpdateTopic("test-ns")
	if err := brk.Publish(context.Background(), topic, &broker.Message{Topic: topic, Payload: nil}); err != nil {
		t.Fatalf("publish update: %v", err)
	}

	waitForAppName(t, registry, fmt.Sprintf("pulled-v%d", pulledBefore+1))
}

func TestGatewayCore_WebuiSettingsWatch_KeepsTheCachedRowWhenTheRepullFails(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	var failing atomic.Bool
	registry := startWatchedCoreWithFetcher(t, brk, func(context.Context) (*webuisettings.Settings, error) {
		if failing.Load() {
			return nil, errors.New("platform read fault")
		}
		return &webuisettings.Settings{AppName: ptr("committed")}, nil
	})

	if _, err := registry.Get(context.Background()); err != nil {
		t.Fatalf("priming Get: %v", err)
	}
	failing.Store(true)

	topic := a2a.WebuiSettingsUpdateTopic("test-ns")
	if err := brk.Publish(context.Background(), topic, &broker.Message{Topic: topic, Payload: []byte("{not json")}); err != nil {
		t.Fatalf("publish update: %v", err)
	}

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		got, err := registry.Get(context.Background())
		if err != nil || got == nil || got.AppName == nil || *got.AppName != "committed" {
			t.Fatalf("cached row was dropped while the re-pull was failing (got=%+v err=%v) — every field would fall back to the gateway's YAML layer for the duration", got, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
