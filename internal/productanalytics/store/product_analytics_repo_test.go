// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

const grantedMarker = "33333333-3333-3333-3333-333333333333"

func seedState(t *testing.T, db *DB, consentState, installID string) {
	t.Helper()
	seedIdentity(t, db, consentState, installID, "")
}

func seedIdentity(t *testing.T, db *DB, consentState, installID, bindingSecret string) {
	t.Helper()
	now := time.Now().UnixMilli()
	insertSQL := `INSERT INTO product_analytics_state (id, install_id, binding_secret, consent_state, created_time, updated_time)
		VALUES (?, ?, ?, ?, ?, ?)`
	upsertSQL := dbutil.UpsertSQL(db.Dialect(), insertSQL, "id",
		[]string{"install_id", "binding_secret", "consent_state", "updated_time"})
	if _, err := db.ExecContext(context.Background(), upsertSQL,
		ProductAnalyticsStateSingletonID, installID, bindingSecret, consentState, now, now); err != nil {
		t.Fatalf("seed product_analytics_state: %v", err)
	}
}

func requireRow(t *testing.T, repo *ProductAnalyticsRepo) *ProductAnalyticsState {
	t.Helper()
	row, err := repo.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if row == nil {
		t.Fatal("no product_analytics_state row")
	}
	return row
}

func TestProductAnalyticsRepo_EnsureInstallIDCreatesAndPersists(t *testing.T) {
	repo := NewProductAnalyticsRepo(newTestDB(t))
	ctx := context.Background()

	first, _, err := repo.EnsureIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !isValidInstallID(first) {
		t.Fatalf("install id %q is not a UUID", first)
	}

	second, _, err := repo.EnsureIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Errorf("marker changed across calls: %q then %q", first, second)
	}
}

func TestProductAnalyticsRepo_EnsureInstallIDReplacesTamperedValue(t *testing.T) {
	db := newTestDB(t)
	repo := NewProductAnalyticsRepo(db)
	seedState(t, db, "granted", "not-a-uuid")

	replaced, _, err := repo.EnsureIdentity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !isValidInstallID(replaced) {
		t.Fatalf("tampered marker not replaced with a UUID, got %q", replaced)
	}
	if got := requireRow(t, repo).InstallID; got != replaced {
		t.Errorf("stored marker = %q, want the returned %q", got, replaced)
	}
}

func TestProductAnalyticsRepo_EnsureInstallIDReplacesTamperedValueWhileUnset(t *testing.T) {
	db := newTestDB(t)
	repo := NewProductAnalyticsRepo(db)
	seedState(t, db, consentStateUnset, "not-a-uuid")
	ctx := context.Background()

	replaced, _, err := repo.EnsureIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !isValidInstallID(replaced) {
		t.Fatalf("tampered marker not replaced for an unanswered install, got %q", replaced)
	}

	state, err := repo.ConsentState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state != consentStateUnset {
		t.Errorf("consent = %q, want unset — replacing the marker must not record a decision", state)
	}
}

func TestProductAnalyticsRepo_SetConsentKeepsTheMarker(t *testing.T) {
	repo := NewProductAnalyticsRepo(newTestDB(t))
	ctx := context.Background()

	held, _, err := repo.EnsureIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if setErr := repo.SetConsent(ctx, "granted"); setErr != nil {
		t.Fatal(setErr)
	}

	row := requireRow(t, repo)
	if row.InstallID != held {
		t.Errorf("stored marker = %q, want the held %q", row.InstallID, held)
	}
	if row.ConsentState != "granted" {
		t.Errorf("stored consent = %q, want granted", row.ConsentState)
	}
}

func TestProductAnalyticsRepo_SetConsentCreatesTheRow(t *testing.T) {
	repo := NewProductAnalyticsRepo(newTestDB(t))

	if err := repo.SetConsent(context.Background(), "granted"); err != nil {
		t.Fatal(err)
	}

	row := requireRow(t, repo)
	if row.ConsentState != "granted" {
		t.Errorf("stored consent = %q, want granted", row.ConsentState)
	}
	if !isValidInstallID(row.InstallID) {
		t.Errorf("stored marker = %q, want a UUID for a collecting install", row.InstallID)
	}
}

func TestProductAnalyticsRepo_SetConsentBeforeInstallIDPersistsBoth(t *testing.T) {
	repo := NewProductAnalyticsRepo(newTestDB(t))
	ctx := context.Background()

	if err := repo.SetConsent(ctx, "granted"); err != nil {
		t.Fatal(err)
	}
	created := requireRow(t, repo).InstallID

	id, _, err := repo.EnsureIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if id != created {
		t.Errorf("install id = %q, want the marker the decision wrote %q", id, created)
	}
	state, err := repo.ConsentState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state != "granted" {
		t.Errorf("consent = %q, want granted (EnsureInstallID must not reset it)", state)
	}
}

func TestProductAnalyticsRepo_SetConsentDeniedClearsTheMarker(t *testing.T) {
	repo := NewProductAnalyticsRepo(newTestDB(t))
	ctx := context.Background()

	if _, _, err := repo.EnsureIdentity(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetConsent(ctx, consentStateDenied); err != nil {
		t.Fatal(err)
	}

	row := requireRow(t, repo)
	if row.InstallID != "" {
		t.Errorf("install id after withdrawal = %q, want empty", row.InstallID)
	}
	if row.ConsentState != consentStateDenied {
		t.Errorf("consent = %q, want denied", row.ConsentState)
	}
}

func TestProductAnalyticsRepo_SetConsentDeniedCreatesTheRowWithoutAMarker(t *testing.T) {
	repo := NewProductAnalyticsRepo(newTestDB(t))

	if err := repo.SetConsent(context.Background(), consentStateDenied); err != nil {
		t.Fatal(err)
	}

	row := requireRow(t, repo)
	if row.InstallID != "" {
		t.Errorf("install id = %q, want empty for a denial", row.InstallID)
	}
	if row.ConsentState != consentStateDenied {
		t.Errorf("consent = %q, want denied", row.ConsentState)
	}
}

func TestProductAnalyticsRepo_EnsureInstallIDReplacesTamperedValueWhileConsentIsBlank(t *testing.T) {
	db := newTestDB(t)
	repo := NewProductAnalyticsRepo(db)
	seedState(t, db, "", "not-a-uuid")

	replaced, _, err := repo.EnsureIdentity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !isValidInstallID(replaced) {
		t.Fatalf("marker for a row with no recorded decision = %q, want a UUID", replaced)
	}
}

func TestProductAnalyticsRepo_EnsureInstallIDMintsNoMarkerAfterWithdrawal(t *testing.T) {
	db := newTestDB(t)
	repo := NewProductAnalyticsRepo(db)
	seedState(t, db, consentStateDenied, "")

	minted, _, err := repo.EnsureIdentity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if minted != "" {
		t.Errorf("EnsureInstallID minted %q after a withdrawal, want no marker", minted)
	}

	row := requireRow(t, repo)
	if row.InstallID != "" {
		t.Errorf("stored marker = %q, want cleared — the withdrawal was overwritten", row.InstallID)
	}
	if row.ConsentState != consentStateDenied {
		t.Errorf("stored consent = %q, want denied", row.ConsentState)
	}
}

func TestProductAnalyticsRepo_ConsentRoundTrip(t *testing.T) {
	repo := NewProductAnalyticsRepo(newTestDB(t))
	ctx := context.Background()

	state, err := repo.ConsentState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state != "" {
		t.Errorf("unwritten consent = %q, want empty", state)
	}

	if setErr := repo.SetConsent(ctx, "granted"); setErr != nil {
		t.Fatal(setErr)
	}
	state, err = repo.ConsentState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state != "granted" {
		t.Errorf("consent = %q, want granted", state)
	}
}

func TestProductAnalyticsRepo_ConsentUnsetRoundTrips(t *testing.T) {
	repo := NewProductAnalyticsRepo(newTestDB(t))
	ctx := context.Background()

	held, _, err := repo.EnsureIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetConsent(ctx, consentStateUnset); err != nil {
		t.Fatal(err)
	}

	row := requireRow(t, repo)
	if row.ConsentState != consentStateUnset {
		t.Errorf("consent = %q, want unset", row.ConsentState)
	}
	if row.InstallID != held {
		t.Errorf("marker after writing unset = %q, want the held %q", row.InstallID, held)
	}
}

func TestIsValidInstallID(t *testing.T) {
	cases := map[string]bool{
		"11111111-1111-1111-1111-111111111111": true,
		"":                                     false,
		"not-a-uuid":                           false,
		"urn:uuid:11111111-1111-1111-1111-111111111111": false,
		"{11111111-1111-1111-1111-111111111111}":        false,
		"11111111111111111111111111111111":              false,
		"11111111-1111-1111-1111-111111111111-extra":    false,
		" 11111111-1111-1111-1111-111111111111":         false,
		"11111111-1111-1111-1111-111111111111 trail":    false,
	}
	for id, want := range cases {
		if got := isValidInstallID(id); got != want {
			t.Errorf("isValidInstallID(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestProductAnalyticsRepo_EnsureInstallIDConvergesUnderConcurrency(t *testing.T) {
	repo := NewProductAnalyticsRepo(newTestDB(t))
	assertConvergentMarkers(t, repo)
}

func TestProductAnalyticsRepo_EnsureInstallIDConvergesWhenReplacingConcurrently(t *testing.T) {
	db := newTestDB(t)
	repo := NewProductAnalyticsRepo(db)
	seedState(t, db, "granted", "not-a-uuid")
	assertConvergentMarkers(t, repo)
}

func assertConvergentMarkers(t *testing.T, repo *ProductAnalyticsRepo) {
	t.Helper()
	ctx := context.Background()

	const callers = 8
	markers := make([]string, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range callers {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			markers[idx], _, errs[idx] = repo.EnsureIdentity(ctx)
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
	}
	for i, got := range markers {
		if got != markers[0] {
			t.Errorf("caller %d saw marker %q, caller 0 saw %q — concurrent use did not converge", i, got, markers[0])
		}
	}
	if !isValidInstallID(markers[0]) {
		t.Fatalf("callers converged on %q, which is not a usable marker", markers[0])
	}
	if got := requireRow(t, repo).InstallID; got != markers[0] {
		t.Errorf("persisted marker = %q, want the one callers observed %q", got, markers[0])
	}
}

func TestProductAnalyticsRepo_EnsureInstallIDSurvivesARepeatedGrant(t *testing.T) {
	repo := NewProductAnalyticsRepo(newTestDB(t))
	ctx := context.Background()

	held, _, err := repo.EnsureIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if setErr := repo.SetConsent(ctx, consentStateGranted); setErr != nil {
		t.Fatal(setErr)
	}
	if setErr := repo.SetConsent(ctx, consentStateGranted); setErr != nil {
		t.Fatal(setErr)
	}

	id, _, err := repo.EnsureIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if id != held {
		t.Errorf("install id = %q after a repeated grant, want the original %q", id, held)
	}
}

func TestProductAnalyticsRepo_EnsureInstallIDDoesNotMintAfterWithdrawal(t *testing.T) {
	repo := NewProductAnalyticsRepo(newTestDB(t))
	ctx := context.Background()

	if _, _, err := repo.EnsureIdentity(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetConsent(ctx, consentStateDenied); err != nil {
		t.Fatal(err)
	}

	id, _, err := repo.EnsureIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if id != "" {
		t.Errorf("install id = %q after withdrawal, want none", id)
	}

	row, err := repo.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if row.InstallID != "" {
		t.Errorf("a marker was written back into a denied row: %q", row.InstallID)
	}
}
