// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestDeploymentIntentsAndDesiredReportADatabaseFailure(t *testing.T) {
	db, err := Open("sqlite:///"+filepath.Join(t.TempDir(), "closed.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	intents := NewDeploymentIntentRepo(db)
	checks := map[string]error{
		"put":         intents.Put(ctx, &DeploymentIntent{ID: "d1", Kind: IntentKindAgent, ObjectID: "a1", Payload: "{}"}),
		"delete":      intents.Delete(ctx, "d1"),
		"list":        func() error { _, err := intents.List(ctx, IntentKindAgent); return err }(),
		"newest":      func() error { _, err := intents.Newest(ctx, IntentKindAgent); return err }(),
		"entrypoints": func() error { _, err := NewGatewayDeploymentRepo(db).Desired(ctx); return err }(),
		"for":         func() error { _, err := intents.For(ctx, IntentKindAgent, "a1"); return err }(),
		"agent":       func() error { _, err := NewAgentDeploymentRepo(db).DesiredFor(ctx, "a1"); return err }(),
		"workflow":    func() error { _, err := NewWorkflowDeploymentRepo(db).DesiredFor(ctx, "w1"); return err }(),
		"entrypoint":  func() error { _, err := NewGatewayDeploymentRepo(db).DesiredFor(ctx, "g1"); return err }(),
	}
	for name, err := range checks {
		if err == nil {
			t.Errorf("%s on a closed database reported no error", name)
		}
	}
}

func TestWhileInFlight_ANewerDeployOfTheObjectWaitsForTheCleanup(t *testing.T) {
	ctx := t.Context()
	repo := NewDeploymentIntentRepo(TestDB(t))
	undeploy := &DeploymentIntent{ID: idgen.New(), Kind: IntentKindAgent, ObjectID: "agent-x", Payload: "{}"}
	if err := repo.PutReplacingOthers(ctx, undeploy); err != nil {
		t.Fatalf("put the undeploy: %v", err)
	}
	redeployed := make(chan error, 1)

	ran, err := repo.WhileInFlight(ctx, undeploy.ID, func() error {
		go func() {
			redeployed <- repo.PutReplacingOthers(ctx, &DeploymentIntent{ID: idgen.New(), Kind: IntentKindAgent, ObjectID: "agent-x", Payload: "{}"})
		}()
		select {
		case err := <-redeployed:
			return fmt.Errorf("a newer deploy began while the undeploy's cleanup ran (%v); it must wait for the cleanup", err)
		case <-time.After(300 * time.Millisecond):
			return nil
		}
	})
	if err != nil || !ran {
		t.Fatalf("WhileInFlight = %v, %v, want the cleanup run alone", ran, err)
	}
	if err := <-redeployed; err != nil {
		t.Fatalf("the newer deploy after the cleanup: %v", err)
	}
	if ran, err := repo.WhileInFlight(ctx, undeploy.ID, func() error { return nil }); err != nil || ran {
		t.Fatalf("cleanup after the newer deploy replaced the undeploy: ran %v, %v, want skipped", ran, err)
	}
}
