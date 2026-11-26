// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics && !solace_id

package clianalytics

import (
	"context"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/activation"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
)

func TestABuildWithoutSolaceIDKeepsLocalCommandsOnTheInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SAM_HOME", home)
	const subject = "auth0|cli-account-test"
	record := activation.Record{
		Account: &activation.Account{Subject: subject, Email: "person@cli-account.test", SignedInAt: time.Now()},
		EULA:    &activation.Acceptance{Subject: subject, AcceptedAt: time.Now()},
	}
	if err := activation.NewStore(home).Save(record); err != nil {
		t.Fatalf("save activation record: %v", err)
	}

	ctx := t.Context()
	sink := &captureSink{}
	r := openReporter(t, testOptions(t, sink))
	if err := r.SetConsent(ctx, productanalytics.ConsentGranted); err != nil {
		t.Fatalf("SetConsent: %v", err)
	}
	r.Track(ctx, productanalytics.EventCLICommandRun, map[string]any{productanalytics.PropCommand: "sam commands"})
	if err := r.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	events := sink.all()
	if len(events) != 1 {
		t.Fatalf("delivered %d events, want 1", len(events))
	}
	if got := events[0].Properties[productanalytics.PropIdentity]; got != "install" {
		t.Errorf("identity = %v, want install", got)
	}
}
