// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	gwauth "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

type machineChannelAdapter struct {
	recordingAdapter
	entrypointType string
}

func (a *machineChannelAdapter) MachineIngress() bool { return true }

func (a *machineChannelAdapter) EntrypointType() string { return a.entrypointType }

func submitAndReadChannel(t *testing.T, adapter GatewayAdapter, metadata map[string]any, opts ...SubmitOption) string {
	t.Helper()
	return submitAndReadChannelAs(t, gwauth.ForUserIDOnly("user-1"), adapter, metadata, opts...)
}

func submitAndReadChannelAs(t *testing.T, caller *gwauth.Subject, adapter GatewayAdapter, metadata map[string]any, opts ...SubmitOption) string {
	t.Helper()
	return submitAndReadOrigin(t, adapter, nil, caller, "user-1", metadata, opts...).Channel
}

func submitAndReadOrigin(t *testing.T, adapter GatewayAdapter, appConfig map[string]any, caller *gwauth.Subject, userID string, metadata map[string]any, opts ...SubmitOption) a2a.Origin {
	t.Helper()
	return submitSignedAndReadOrigin(t, adapter, appConfig, nil, caller, userID, metadata, opts...)
}

func submitSignedAndReadOrigin(t *testing.T, adapter GatewayAdapter, appConfig map[string]any, signer TaskSigner, caller *gwauth.Subject, userID string, metadata map[string]any, opts ...SubmitOption) a2a.Origin {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	cfg := map[string]any{"namespace": "test-ns", "gateway_id": "test-gw"}
	maps.Copy(cfg, appConfig)
	core := NewGatewayCore(adapter, cfg)
	core.eventBindActive.Store(true)
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	if signer != nil {
		core.SetTaskSigner(signer)
	}
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(context.Background()) }()

	queue, err := brk.CreateQueue("channel-agent-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(queue, a2a.AgentRequestTopic("test-ns", "target-agent")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	msg := a2a.Message{
		Kind:      "message",
		MessageID: "msg-channel",
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewTextPart("hello")},
		Metadata:  metadata,
	}
	if err = core.SubmitTaskAttributedTo(t.Context(), "target-agent", "task-channel", msg, caller, userID, opts...); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	recvCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	received, err := brk.Receive(recvCtx, queue)
	if err != nil || received == nil {
		t.Fatalf("receive: err=%v msg=%v", err, received)
	}
	return a2a.DecodeUserProperties(received.UserProperties).Origin
}

func TestSubmitTaskStampsTheChannelTheTaskEnteredThrough(t *testing.T) {
	scheduled := map[string]any{"source": a2a.SourceScheduler}
	tests := []struct {
		name     string
		adapter  GatewayAdapter
		metadata map[string]any
		opts     []SubmitOption
		want     string
	}{
		{name: "web ui header", adapter: &recordingAdapter{}, opts: []SubmitOption{FromClientSurface(httputil.SurfaceWebUI)}, want: httputil.SurfaceWebUI},
		{name: "cli header", adapter: &recordingAdapter{}, opts: []SubmitOption{FromClientSurface(httputil.SurfaceCLI)}, want: httputil.SurfaceCLI},
		{name: "api header", adapter: &recordingAdapter{}, opts: []SubmitOption{FromClientSurface(httputil.SurfaceAPI)}, want: httputil.SurfaceAPI},
		{name: "relay overrides the header", adapter: &statusTextAdapter{relayed: httputil.SurfaceSlack}, opts: []SubmitOption{FromClientSurface(httputil.SurfaceWebUI)}, want: httputil.SurfaceSlack},
		{name: "scheduled task", adapter: &recordingAdapter{}, metadata: scheduled, opts: []SubmitOption{FromClientSurface(httputil.SurfaceWebUI)}, want: a2a.ChannelScheduler},
		{name: "scheduled task beats an adapter channel", adapter: &machineChannelAdapter{entrypointType: "webhook"}, metadata: scheduled, want: a2a.ChannelScheduler},
		{name: "webhook adapter", adapter: &machineChannelAdapter{entrypointType: "webhook"}, opts: []SubmitOption{FromClientSurface(httputil.SurfaceAPI)}, want: "webhook"},
		{name: "no surface and no adapter channel", adapter: &recordingAdapter{}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := submitAndReadChannel(t, tt.adapter, tt.metadata, tt.opts...); got != tt.want {
				t.Errorf("published channel = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSubmitTaskStampsTheSystemChannelOnASystemInitiatedTask(t *testing.T) {
	tests := []struct {
		name    string
		adapter GatewayAdapter
		opts    []SubmitOption
	}{
		{name: "relay gateway", adapter: &statusTextAdapter{relayed: httputil.SurfaceSlack}},
		{name: "machine ingress gateway", adapter: &machineChannelAdapter{entrypointType: "webhook"}},
		{name: "web ui header", adapter: &recordingAdapter{}, opts: []SubmitOption{FromClientSurface(httputil.SurfaceWebUI)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caller := gwauth.NewTestSubject(gwauth.SubjectFields{UserID: "system", Authenticated: true, SystemInitiated: true})
			if got := submitAndReadChannelAs(t, caller, tt.adapter, nil, tt.opts...); got != httputil.SurfaceSystem {
				t.Errorf("published channel = %q, want %q", got, httputil.SurfaceSystem)
			}
		})
	}
}

func TestSubmitChannelResolutionOrder(t *testing.T) {
	tests := []struct {
		name    string
		adapter GatewayAdapter
		trigger string
		surface string
		channel string
		system  bool
		want    string
	}{
		{name: "scheduled trigger", adapter: &recordingAdapter{}, trigger: a2a.TriggerScheduled, surface: httputil.SurfaceWebUI, want: a2a.ChannelScheduler},
		{name: "webhook adapter", adapter: &machineChannelAdapter{entrypointType: "webhook"}, trigger: a2a.TriggerEvent, want: "webhook"},
		{name: "mcp adapter", adapter: &machineChannelAdapter{entrypointType: "mcp"}, trigger: a2a.TriggerInteractive, want: "mcp"},
		{name: "event mesh adapter", adapter: &machineChannelAdapter{entrypointType: "event_mesh"}, trigger: a2a.TriggerEvent, want: "event_mesh"},
		{name: "adapter channel beats the surface", adapter: &machineChannelAdapter{entrypointType: "mcp"}, trigger: a2a.TriggerInteractive, surface: httputil.SurfaceCLI, want: "mcp"},
		{name: "header surface", adapter: &recordingAdapter{}, trigger: a2a.TriggerInteractive, surface: httputil.SurfaceCLI, want: httputil.SurfaceCLI},
		{name: "relayed surface", adapter: &recordingAdapter{}, trigger: a2a.TriggerInteractive, surface: httputil.SurfaceTeams, want: httputil.SurfaceTeams},
		{name: "system initiated beats the surface", adapter: &recordingAdapter{}, trigger: a2a.TriggerInteractive, surface: httputil.SurfaceWebUI, system: true, want: httputil.SurfaceSystem},
		{name: "system initiated beats the scheduler", adapter: &recordingAdapter{}, trigger: a2a.TriggerScheduled, system: true, want: httputil.SurfaceSystem},
		{name: "system initiated beats a machine adapter", adapter: &machineChannelAdapter{entrypointType: "webhook"}, trigger: a2a.TriggerEvent, system: true, want: httputil.SurfaceSystem},
		{name: "a declared channel beats the header surface", adapter: &recordingAdapter{}, trigger: a2a.TriggerInteractive, surface: httputil.SurfaceAPI, channel: a2a.ChannelEval, want: a2a.ChannelEval},
		{name: "the scheduler beats a declared channel", adapter: &recordingAdapter{}, trigger: a2a.TriggerScheduled, channel: a2a.ChannelEval, want: a2a.ChannelScheduler},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			core := &GatewayCore{adapter: tt.adapter}
			if got := core.submitChannel(tt.trigger, tt.surface, tt.channel, tt.system); got != tt.want {
				t.Errorf("submitChannel = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestChannelsSAMStampsItselfCollideWithNoSurfaceOrEntrypointType(t *testing.T) {
	for _, own := range []string{a2a.ChannelScheduler, a2a.ChannelEval, a2a.ChannelSystem} {
		if slices.Contains(httputil.SubmitterSurfaces, own) || slices.Contains(taxonomy.GatewayTypes, own) {
			t.Errorf("channel %q is also a client surface or entrypoint type, so a ledger row could not say which produced it", own)
		}
	}
	if a2a.ChannelSystem != httputil.SurfaceSystem {
		t.Errorf("ChannelSystem = %q, want the %q writer surface", a2a.ChannelSystem, httputil.SurfaceSystem)
	}
}
