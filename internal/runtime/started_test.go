// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runtime

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

type gateComponent struct {
	open bool
}

func (g *gateComponent) Init(context.Context, config.Config) error { return nil }
func (g *gateComponent) Start(context.Context) error               { return nil }
func (g *gateComponent) Stop(context.Context) error                { return nil }
func (g *gateComponent) Health() error                             { return nil }
func (g *gateComponent) ReadinessOpen() bool                       { return g.open }

func TestStartedHooksRunAfterStartAndGatesHoldReadiness(t *testing.T) {
	r := NewRuntime(nil, nil)
	gate := &gateComponent{}
	r.Register("gated", gate)
	ran := make(chan struct{})
	r.OnStarted(func() { close(ran) })
	if err := r.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ran:
	case <-time.After(time.Second):
		t.Fatal("the started hook did not run")
	}
	if r.Ready() {
		t.Fatal("ready while a gate is closed")
	}
	hs := NewManagementServer(r, ":0", nil)
	addr, err := hs.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hs.Stop(context.Background()) }()
	if code := readyStatus(t, addr); code != http.StatusServiceUnavailable {
		t.Fatalf("/ready = %d while a gate is closed, want 503", code)
	}

	gate.open = true
	if !r.Ready() {
		t.Fatal("not ready once every gate is open")
	}
	if code := readyStatus(t, addr); code != http.StatusOK {
		t.Fatalf("/ready = %d once every gate is open, want 200", code)
	}
}

func readyStatus(t *testing.T, addr string) int {
	t.Helper()
	resp, err := http.Get("http://" + addr + "/ready")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}
