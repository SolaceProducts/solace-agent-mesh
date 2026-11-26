// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
)

func TestDeploymentIDReplacesOnlyAnotherDeployment(t *testing.T) {
	ctx := context.Background()
	exe := NewExecutor(newTestServices())
	var started []*mockInstance
	exe.RegisterKind("agent", func(cfg config.Config) (Instance, error) {
		inst := newMockInstance(cfg.GetString("name"), "agent")
		started = append(started, inst)
		return inst, nil
	})
	start := func(id string) error {
		body, _ := json.Marshal(map[string]any{"kind": "agent", "name": "a", control.DeploymentIDField: id})
		_, err := exe.HandleManagement(ctx, "post", []string{"instances"}, body, nil)
		return err
	}
	remove := func() error {
		_, err := exe.HandleManagement(ctx, "delete", []string{"instances", "a"}, nil, nil)
		return err
	}

	if err := start("d1"); err != nil {
		t.Fatalf("first start: %v", err)
	}
	if err := start("d1"); !control.IsAlreadyExistsError(err) {
		t.Fatalf("the deployment it runs: got %v, want already exists", err)
	}
	if err := start("d2"); err != nil {
		t.Fatalf("another deployment: %v", err)
	}
	if got := started[0].stopCalled.Load(); got != 1 {
		t.Fatalf("another deployment must replace the running instance: first instance stopped %d times", got)
	}
	if err := remove(); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := start("d2"); err != nil {
		t.Fatalf("a start after a remove: %v", err)
	}
	if n := len(exe.ListInstances()); n != 1 {
		t.Fatalf("%d instances after the start, want 1", n)
	}
}

type replacingInstance struct {
	*mockInstance
	replaceStops atomic.Int32
}

func (r *replacingInstance) StopForReplace(ctx context.Context) error {
	r.replaceStops.Add(1)
	return r.Stop(ctx)
}

func TestDeploymentReplaceHandsWorkToTheNewCopy(t *testing.T) {
	ctx := context.Background()
	exe := NewExecutor(newTestServices())
	var started []*replacingInstance
	exe.RegisterKind("agent", func(cfg config.Config) (Instance, error) {
		inst := &replacingInstance{mockInstance: newMockInstance(cfg.GetString("name"), "agent")}
		started = append(started, inst)
		return inst, nil
	})
	for _, id := range []string{"d1", "d2"} {
		body, _ := json.Marshal(map[string]any{"kind": "agent", "name": "a", control.DeploymentIDField: id})
		if _, err := exe.HandleManagement(ctx, "post", []string{"instances"}, body, nil); err != nil {
			t.Fatalf("start %s: %v", id, err)
		}
	}
	if got := started[0].replaceStops.Load(); got != 1 {
		t.Errorf("the replaced instance got %d replace stops, want 1", got)
	}
}
