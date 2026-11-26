// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"
	"time"
)

func pendingAuthRow(sender string, expiresAt int64) *WhatsAppPendingAuth {
	return &WhatsAppPendingAuth{
		GatewayID: "wa-gw", SenderID: sender, Kind: PendingAuthKindLogin,
		LookupKey: "phone", LookupValueFP: "fp-" + sender,
		ExpiresAt: expiresAt, CreatedAt: 1, UpdatedAt: 1,
	}
}

func TestPendingAuthSuppressesThenExpires(t *testing.T) {
	repo := NewWhatsAppPendingAuthRepo(TestDB(t))
	ctx := t.Context()

	if err := repo.Upsert(ctx, nil, pendingAuthRow("15551234567", 1000)); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	live, err := repo.Live(ctx, "wa-gw", "15551234567", 500)
	if err != nil || live == nil {
		t.Fatalf("Live before expiry = %v, err = %v; want the row", live, err)
	}
	if live.LookupValueFP != "fp-15551234567" {
		t.Errorf("LookupValueFP = %q, want the stored fingerprint", live.LookupValueFP)
	}

	expired, err := repo.Live(ctx, "wa-gw", "15551234567", 1500)
	if err != nil {
		t.Fatalf("Live after expiry: %v", err)
	}
	if expired != nil {
		t.Error("an expired record still suppressed the prompt; the sender would be silently refused forever")
	}
}

func TestPendingAuthLiveIsNilForAnUnpromptedSender(t *testing.T) {
	repo := NewWhatsAppPendingAuthRepo(TestDB(t))
	row, err := repo.Live(t.Context(), "wa-gw", "15559999999", 1)
	if err != nil {
		t.Fatalf("Live: %v", err)
	}
	if row != nil {
		t.Errorf("an unprompted sender has a record: %+v", row)
	}
}

func TestPendingAuthUpsertReplaces(t *testing.T) {
	repo := NewWhatsAppPendingAuthRepo(TestDB(t))
	ctx := t.Context()

	if err := repo.Upsert(ctx, nil, pendingAuthRow("15551234567", 1000)); err != nil {
		t.Fatalf("first Upsert: %v", err)
	}
	second := pendingAuthRow("15551234567", 2000)
	second.LookupValueFP = "fp-changed"
	if err := repo.Upsert(ctx, nil, second); err != nil {
		t.Fatalf("second Upsert: %v", err)
	}

	row, err := repo.Live(ctx, "wa-gw", "15551234567", 1500)
	if err != nil || row == nil {
		t.Fatalf("Live = %v, err = %v", row, err)
	}
	if row.LookupValueFP != "fp-changed" || row.ExpiresAt != 2000 {
		t.Errorf("record = %+v, want the second prompt's values", row)
	}
}

func TestPendingAuthClear(t *testing.T) {
	repo := NewWhatsAppPendingAuthRepo(TestDB(t))
	ctx := t.Context()

	if err := repo.Upsert(ctx, nil, pendingAuthRow("15551234567", 1000)); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := repo.Clear(ctx, nil, "wa-gw", "15551234567"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	row, err := repo.Live(ctx, "wa-gw", "15551234567", 500)
	if err != nil {
		t.Fatalf("Live: %v", err)
	}
	if row != nil {
		t.Error("the record survived Clear; the next sign-in prompt would be suppressed")
	}
}

func TestPendingAuthDeleteExpired(t *testing.T) {
	repo := NewWhatsAppPendingAuthRepo(TestDB(t))
	ctx := t.Context()

	if err := repo.Upsert(ctx, nil, pendingAuthRow("dead-1", 100)); err != nil {
		t.Fatalf("Upsert dead-1: %v", err)
	}
	if err := repo.Upsert(ctx, nil, pendingAuthRow("alive-1", 9000)); err != nil {
		t.Fatalf("Upsert alive-1: %v", err)
	}

	removed, err := repo.DeleteExpired(ctx, 500)
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	if row, _ := repo.Live(ctx, "wa-gw", "alive-1", 500); row == nil {
		t.Error("the reaper took a live record")
	}
}

func TestPendingAuthTTLOutlastsAHumanGoingToBed(t *testing.T) {
	if DefaultPendingAuthTTL < 12*time.Hour {
		t.Errorf("DefaultPendingAuthTTL = %v, too short for an action that needs a person at a browser", DefaultPendingAuthTTL)
	}
}
