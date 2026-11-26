// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

const (
	firstSecret  = "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"
	secondSecret = "abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd"
)

func TestEnsureIdentityMintsAMarkerAndNoSecret(t *testing.T) {
	repo := NewProductAnalyticsRepo(newTestDB(t))
	ctx := context.Background()

	marker, secret, err := repo.EnsureIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !isValidInstallID(marker) {
		t.Errorf("marker = %q, want a minted UUID", marker)
	}
	if secret != "" {
		t.Errorf("binding secret = %q, want none until a caller adopts one", secret)
	}
}

func TestReplaceBindingSecretAdoptsAndIsIdempotentUnderARace(t *testing.T) {
	repo := NewProductAnalyticsRepo(newTestDB(t))
	ctx := context.Background()
	seedIdentity(t, repo.db, "granted", grantedMarker, "")

	inForce, err := repo.ReplaceBindingSecret(ctx, "", firstSecret)
	if err != nil {
		t.Fatal(err)
	}
	if inForce != firstSecret {
		t.Errorf("secret in force = %q, want the adopted %q", inForce, firstSecret)
	}

	loser, err := repo.ReplaceBindingSecret(ctx, "", secondSecret)
	if err != nil {
		t.Fatal(err)
	}
	if loser != firstSecret {
		t.Errorf("the second adopter settled on %q, want the key already in force %q", loser, firstSecret)
	}
}

func TestReplaceBindingSecretRefusesAWithdrawnInstall(t *testing.T) {
	repo := NewProductAnalyticsRepo(newTestDB(t))
	ctx := context.Background()
	if err := repo.SetConsent(ctx, "denied"); err != nil {
		t.Fatal(err)
	}

	inForce, err := repo.ReplaceBindingSecret(ctx, "", firstSecret)
	if err != nil {
		t.Fatal(err)
	}
	if inForce != "" {
		t.Errorf("secret in force = %q on a withdrawn install, want none", inForce)
	}
	row, err := repo.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if row.BindingSecret != "" {
		t.Errorf("stored secret = %q, want the withdrawal left it cleared", row.BindingSecret)
	}
}

func TestSetConsentClearsBothHalvesOnAnOptOut(t *testing.T) {
	repo := NewProductAnalyticsRepo(newTestDB(t))
	ctx := context.Background()
	seedIdentity(t, repo.db, "granted", grantedMarker, firstSecret)

	if err := repo.SetConsent(ctx, "denied"); err != nil {
		t.Fatal(err)
	}

	row, err := repo.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if row.InstallID != "" || row.BindingSecret != "" {
		t.Errorf("after opting out marker = %q and secret = %q, want both cleared", row.InstallID, row.BindingSecret)
	}
	marker, secret, err := repo.EnsureIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if marker != "" || secret != "" {
		t.Errorf("EnsureIdentity = (%q, %q) after an opt-out, want empty", marker, secret)
	}
}

func TestReplacingATamperedMarkerDiscardsItsSecret(t *testing.T) {
	repo := NewProductAnalyticsRepo(newTestDB(t))
	ctx := context.Background()
	seedIdentity(t, repo.db, "granted", "not-a-uuid", firstSecret)

	marker, secret, err := repo.EnsureIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !isValidInstallID(marker) {
		t.Errorf("marker = %q, want a minted replacement", marker)
	}
	if secret != "" {
		t.Errorf("secret = %q, want the replaced marker's key discarded with it", secret)
	}
	row, err := repo.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if row.BindingSecret != "" {
		t.Errorf("stored secret = %q, want it cleared alongside the marker", row.BindingSecret)
	}
}

func TestAnInstallMigratedFromTheKeylessSchemaKeepsItsMarker(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	cfg, cfgErr := db.migrateConfig()
	if cfgErr != nil {
		t.Fatal(cfgErr)
	}
	if downErr := dbutil.MigrateDownTo(ctx, cfg, 1); downErr != nil {
		t.Fatalf("roll back to the keyless schema: %v", downErr)
	}

	if _, seedErr := db.ExecContext(ctx,
		`INSERT INTO product_analytics_state (id, install_id, consent_state, created_time, updated_time)
		 VALUES (?, ?, ?, ?, ?)`,
		ProductAnalyticsStateSingletonID, grantedMarker, "granted", 1, 1,
	); seedErr != nil {
		t.Fatalf("seed a pre-upgrade row: %v", seedErr)
	}

	if upErr := db.Migrate(ctx); upErr != nil {
		t.Fatalf("upgrade: %v", upErr)
	}

	repo := NewProductAnalyticsRepo(db)
	marker, secret, err := repo.EnsureIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if marker != grantedMarker {
		t.Errorf("marker after the upgrade = %q, want the one the install already had, %q", marker, grantedMarker)
	}
	if secret != "" {
		t.Errorf("secret after the upgrade = %q, want none until a caller adopts one", secret)
	}
	state, err := repo.ConsentState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state != "granted" {
		t.Errorf("consent after the upgrade = %q, want the recorded grant preserved", state)
	}

	if _, adoptErr := repo.ReplaceBindingSecret(ctx, "", firstSecret); adoptErr != nil {
		t.Fatalf("adopt a key on the upgraded install: %v", adoptErr)
	}
	upgraded, adopted, err := repo.EnsureIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if upgraded != grantedMarker || adopted != firstSecret {
		t.Errorf("upgraded identity = (%q, %q), want the existing marker with the adopted key", upgraded, adopted)
	}
}
