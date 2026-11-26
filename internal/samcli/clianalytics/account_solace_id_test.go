// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics && solace_id

package clianalytics

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/activation"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
)

const (
	testSubject = "auth0|cli-account-test"
	testEmail   = "person@cli-account.test"
)

func samHomeWith(t *testing.T, record activation.Record) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("SAM_HOME", home)
	if err := activation.NewStore(home).Save(record); err != nil {
		t.Fatalf("save activation record: %v", err)
	}
	return home
}

func homeStore(home string) Options {
	dataDir := filepath.Join(home, "data")
	return Options{DatabaseURL: databaseURL(dataDir), DataDir: dataDir}
}

func signedIn() *activation.Account {
	return &activation.Account{Subject: testSubject, Email: testEmail, SignedInAt: time.Now()}
}

func accepted() activation.Record {
	return activation.Record{
		Account: signedIn(),
		EULA:    &activation.Acceptance{Subject: testSubject, AcceptedAt: time.Now()},
	}
}

func trackOneCommand(t *testing.T, store Options, consent productanalytics.ConsentState) []productanalytics.Event {
	t.Helper()
	ctx := t.Context()
	sink := &captureSink{}
	opts := testOptions(t, sink)
	if store.DatabaseURL != "" {
		opts.DatabaseURL, opts.DataDir = store.DatabaseURL, store.DataDir
	}
	r := openReporter(t, opts)
	if err := r.SetConsent(ctx, consent); err != nil {
		t.Fatalf("SetConsent: %v", err)
	}
	r.Track(ctx, productanalytics.EventCLICommandRun, map[string]any{
		productanalytics.PropCommand: "sam commands",
		productanalytics.PropOutcome: productanalytics.OutcomeSuccess,
	})
	if err := r.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return sink.all()
}

func TestALocalCommandAfterTheLicenseIsFiledUnderTheAccount(t *testing.T) {
	home := samHomeWith(t, accepted())

	events := trackOneCommand(t, homeStore(home), productanalytics.ConsentGranted)
	if len(events) != 1 {
		t.Fatalf("delivered %d events, want 1", len(events))
	}
	ev := events[0]
	if got := ev.Properties[productanalytics.PropIdentity]; got != "account" {
		t.Errorf("identity = %v, want account", got)
	}
	if ev.DistinctID != testSubject {
		t.Error("the event is not filed under the accepted account")
	}
	if got := ev.Properties[productanalytics.PropEmailDomain]; got != "cli-account.test" {
		t.Errorf("emailDomain = %v, want the account's domain", got)
	}
	for key, value := range ev.Properties {
		if value == testEmail {
			t.Errorf("property %s carries the full email", key)
		}
	}
}

func TestALocalCommandBeforeTheLicenseStaysOnTheInstall(t *testing.T) {
	for name, record := range map[string]activation.Record{
		"signed out":                   {},
		"signed in, license not taken": {Account: signedIn()},
	} {
		t.Run(name, func(t *testing.T) {
			home := samHomeWith(t, record)

			events := trackOneCommand(t, homeStore(home), productanalytics.ConsentGranted)
			if len(events) != 1 {
				t.Fatalf("delivered %d events, want 1", len(events))
			}
			if got := events[0].Properties[productanalytics.PropIdentity]; got != "install" {
				t.Errorf("identity = %v, want install", got)
			}
			if events[0].DistinctID == testSubject {
				t.Error("an event from before the license is filed under the account")
			}
			if _, present := events[0].Properties[productanalytics.PropEmailDomain]; present {
				t.Error("an install-side event carries an email domain")
			}
		})
	}
}

func TestTurningAnalyticsOffStopsTheAccountsCommands(t *testing.T) {
	home := samHomeWith(t, accepted())

	if events := trackOneCommand(t, homeStore(home), productanalytics.ConsentDenied); len(events) != 0 {
		t.Errorf("delivered %d events after analytics was turned off, want 0", len(events))
	}
}

func TestACommandOnAnotherStoreStaysOnTheInstall(t *testing.T) {
	samHomeWith(t, accepted())

	events := trackOneCommand(t, Options{}, productanalytics.ConsentGranted)
	if len(events) != 1 {
		t.Fatalf("delivered %d events, want 1", len(events))
	}
	if got := events[0].Properties[productanalytics.PropIdentity]; got != "install" {
		t.Errorf("identity = %v, want install", got)
	}
	if events[0].DistinctID == testSubject {
		t.Error("a command whose consent comes from another store is filed under the home's account")
	}
}
