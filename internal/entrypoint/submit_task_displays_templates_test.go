// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type statusTextAdapter struct {
	recordingAdapter
	rendersFor func(surface string) bool
	relayed    string
}

func (a *statusTextAdapter) RendersStatusText(surface string) bool {
	return a.rendersFor != nil && a.rendersFor(surface)
}

func (a *statusTextAdapter) RelayedSurface() string { return a.relayed }

type displaysTemplatesCase struct {
	name          string
	adapter       GatewayAdapter
	embedsOff     bool
	arts          *artifact.ScopedService
	metadata      map[string]any
	identity      *auth.Subject
	opts          []SubmitOption
	wantDisplayed bool
}

func TestSubmitTaskStampsDisplaysTemplateSignals(t *testing.T) {
	namespaceArts := func() *artifact.ScopedService {
		return &artifact.ScopedService{Inner: memory.New(), Scope: artifact.ScopeNamespace, Namespace: "test-ns"}
	}
	renderer := func() GatewayAdapter {
		return &statusTextAdapter{rendersFor: func(string) bool { return true }}
	}
	rendersOnlyFor := func(want string) func(string) bool {
		return func(surface string) bool { return surface == want }
	}
	webUIRenderer := func() GatewayAdapter { return &statusTextAdapter{rendersFor: rendersOnlyFor(httputil.SurfaceWebUI)} }
	slackRelay := func(rendersFor string) GatewayAdapter {
		return &statusTextAdapter{rendersFor: rendersOnlyFor(rendersFor), relayed: httputil.SurfaceSlack}
	}
	forged := map[string]any{a2a.MetadataDisplaysTemplateSignals: true}

	tests := []displaysTemplatesCase{
		{name: "status-text renderer with namespace artifacts and embed resolution", adapter: renderer(), arts: namespaceArts(), wantDisplayed: true},
		{name: "a client-supplied false is overwritten when the gateway displays", adapter: renderer(), arts: namespaceArts(), metadata: map[string]any{a2a.MetadataDisplaysTemplateSignals: false}, wantDisplayed: true},
		{name: "adapter that does not render status text", adapter: &recordingAdapter{}, arts: namespaceArts()},
		{name: "adapter that declines to render status text", adapter: &statusTextAdapter{}, arts: namespaceArts()},
		{name: "embed resolution off", adapter: renderer(), embedsOff: true, arts: namespaceArts()},
		{name: "agent-scoped artifacts", adapter: renderer(), arts: &artifact.ScopedService{Inner: memory.New(), Scope: "agent"}},
		{name: "no artifact service", adapter: renderer()},
		{name: "scheduled task", adapter: renderer(), arts: namespaceArts(), metadata: map[string]any{"source": a2a.SourceScheduler}},
		{name: "system-initiated identity", adapter: renderer(), arts: namespaceArts(), identity: auth.NewTestSubject(auth.SubjectFields{UserID: "sentinel", Authenticated: true, SystemInitiated: true})},
		{name: "a client-supplied true is removed when the adapter does not render", adapter: &recordingAdapter{}, arts: namespaceArts(), metadata: forged},
		{name: "a client-supplied true is removed for a scheduled task", adapter: renderer(), arts: namespaceArts(), metadata: map[string]any{"source": a2a.SourceScheduler, a2a.MetadataDisplaysTemplateSignals: true}},
		{name: "surface the adapter renders for", adapter: webUIRenderer(), arts: namespaceArts(), opts: []SubmitOption{FromClientSurface(httputil.SurfaceWebUI)}, wantDisplayed: true},
		{name: "surface the adapter does not render for", adapter: webUIRenderer(), arts: namespaceArts(), opts: []SubmitOption{FromClientSurface(httputil.SurfaceAPI)}},
		{name: "a client-supplied true is removed for a surface the adapter does not render for", adapter: webUIRenderer(), arts: namespaceArts(), metadata: forged, opts: []SubmitOption{FromClientSurface(httputil.SurfaceAPI)}},
		{name: "no declared surface", adapter: webUIRenderer(), arts: namespaceArts()},
		{name: "the relayed surface is what the adapter is asked about", adapter: slackRelay(httputil.SurfaceSlack), arts: namespaceArts(), opts: []SubmitOption{FromClientSurface(httputil.SurfaceAPI)}, wantDisplayed: true},
		{name: "a client surface cannot stand in for the relayed one", adapter: slackRelay(httputil.SurfaceWebUI), arts: namespaceArts(), opts: []SubmitOption{FromClientSurface(httputil.SurfaceWebUI)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metadata := submitForDisplaysTemplates(t, tt)
			got, present := metadata[a2a.MetadataDisplaysTemplateSignals]
			if tt.wantDisplayed && got != true {
				t.Errorf("%s = %v, want true", a2a.MetadataDisplaysTemplateSignals, got)
			}
			if !tt.wantDisplayed && present {
				t.Errorf("%s = %v, want the key absent", a2a.MetadataDisplaysTemplateSignals, got)
			}
		})
	}
}

func submitForDisplaysTemplates(t *testing.T, tc displaysTemplatesCase) map[string]any {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	var opts []runtime.Option
	if tc.arts != nil {
		opts = append(opts, runtime.WithArtifacts(tc.arts))
	}
	core := NewGatewayCore(tc.adapter, map[string]any{
		"namespace":               "test-ns",
		"gateway_id":              "test-gw",
		"enable_embed_resolution": !tc.embedsOff,
	})
	core.SetServices(runtime.NewRuntime(brk, nil, opts...).Services())
	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })

	caller := tc.identity
	if caller == nil {
		caller = auth.NewTestSubject(auth.SubjectFields{UserID: "user-1"})
	}
	msg := stubMessage()
	msg.Metadata = tc.metadata
	return receiveMetadataWithContext(t.Context(), t, core, brk, "displays-templates-q", "target-agent", "task-1", msg, caller, tc.opts...)
}
